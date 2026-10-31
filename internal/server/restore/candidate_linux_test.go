package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

func candidateParent(test *testing.T) *os.File {
	test.Helper()
	name := test.TempDir()
	if err := os.Chmod(name, 0700); err != nil {
		test.Fatal(err)
	}
	parent, err := os.Open(name)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = parent.Close() })
	return parent
}

func candidateTestLimits() CandidateLimits {
	return CandidateLimits{Database: state.LocalSnapshotLimits{MaxDBBytes: 8 << 20, MaxInventoryBytes: 4 << 20, MaxStagingBytes: 64 << 20, MaxRecords: 100000, SQLiteHeapBytes: 32 << 20, WorkerAddressSpaceBytes: 16 << 30, WorkerCPUSeconds: 10, MaxWALBytes: 64 << 20, MaxSHMBytes: 1 << 20, OperationTimeout: 10 * time.Second, DrainTimeout: time.Second}, MaxArchives: 1000, MaxInventoryBytes: 8 << 20, MaxContentBytes: 128 << 20, MaxStagingBytes: 128<<20 + 256, ArchiveCacheBytes: 64 << 20, OperationTimeout: 30 * time.Second}
}

func candidateTestSource(test *testing.T, owners int) (CandidateSource, []byte) {
	test.Helper()
	parent := candidateParent(test)
	key := bytes.Repeat([]byte{87}, 32)
	store, err := state.OpenWithOptions(test.Context(), filepath.Join(parent.Name(), "server.db"), key, state.Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	cache, err := library.OpenArchiveCacheWithOptions(test.Context(), filepath.Join(parent.Name(), "archives"), library.CacheOptions{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = cache.Close() })
	archive, err := repository.InspectArchive(test.Context(), archiveTestTar(test, archiveTestEntries()))
	if err != nil {
		test.Fatal(err)
	}
	service, err := library.NewService(store, cache, inventoryFixtureFetcher{archive: archive})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	if err := store.CreateUser(test.Context(), "candidate-unreferenced-owner"); err != nil {
		test.Fatal(err)
	}
	for _, owner := range []string{"candidate-owner-one", "candidate-owner-two"}[:owners] {
		if err := store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
		entry, err := service.Collect(test.Context(), owner, "org/"+owner, "")
		if err != nil {
			test.Fatal(err)
		}
		entry, err = service.Get(test.Context(), owner, entry.ID)
		if err != nil {
			test.Fatal(err)
		}
		pack, err := store.CreatePack(test.Context(), owner, state.PackDraft{Name: "candidate pack", Items: []state.PackSelection{{SkillID: entry.Skills[0].ID, Slug: "first"}, {SkillID: entry.Skills[1].ID, Slug: "second"}}})
		if err != nil {
			test.Fatal(err)
		}
		category, err := store.CreateCategory(test.Context(), owner, "candidate category")
		if err != nil {
			test.Fatal(err)
		}
		if _, err := store.SetSkillCategories(test.Context(), owner, entry.Skills[0].ID, []string{category.ID}); err != nil {
			test.Fatal(err)
		}
		asset, err := store.CreateConfigurationAsset(test.Context(), owner, state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "candidate configuration", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte("candidate-synthetic-configuration-secret"))})
		if err != nil {
			test.Fatal(err)
		}
		if _, err := store.ReplacePackConfigurationAttachments(test.Context(), owner, pack.ID, pack.Revision, 1, []state.PackConfigurationReference{{AssetID: asset.ID, AssetRevision: asset.Revision, TargetID: "codex.provider.user"}}); err != nil {
			test.Fatal(err)
		}
	}
	cryptoParent := candidateParent(test)
	recovery, err := backup.ImportRecoveryKey([16]byte{9}, bytes.Repeat([]byte{13}, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = recovery.Destroy() })
	cryptoLimits := backup.SealLimits{TotalPlainBytes: 1 << 30, TotalCipherBytes: 1 << 30, StagingBytes: 2 << 30, InputStagingBytes: 128 << 20, MaxObjects: 1000, MaxInventoryRecords: 100000, OperationTimeout: time.Minute}
	producer, err := backup.NewSealEngine(cryptoParent, recovery, cryptoLimits)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = producer.Close() })
	captured, err := producer.CaptureSnapshot(test.Context(), service, backup.CaptureOptions{InstanceID: [16]byte{1}, BackupID: [16]byte{2}, AppVersion: "candidate-test", Limits: library.ContentSnapshotLimits{Database: candidateTestLimits().Database, MaxArchives: 1000, MaxInventoryBytes: 8 << 20, MaxContentBytes: 128 << 20}})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = captured.Close() })
	transfer := candidateParent(test)
	var files []*os.File
	if err := captured.WithFiles(test.Context(), func(ctx context.Context, _ backup.ObjectRef, input *os.File) error {
		output, err := os.OpenFile(filepath.Join(transfer.Name(), fmt.Sprint(len(files))), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		_, copyError := io.Copy(output, io.NewSectionReader(input, 0, 1<<30))
		if err := errors.Join(copyError, output.Sync(), output.Close()); err != nil {
			return err
		}
		file, err := os.Open(output.Name())
		if err != nil {
			return err
		}
		files = append(files, file)
		test.Cleanup(func() { _ = file.Close() })
		return ctx.Err()
	}); err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(captured.Close(), producer.Close(), service.Close(), cache.Close(), store.Close()); err != nil {
		test.Fatal(err)
	}
	if err := os.Rename(parent.Name(), parent.Name()+"-offline"); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = os.Rename(parent.Name()+"-offline", parent.Name()) })
	var header [96]byte
	if _, err := files[0].ReadAt(header[:], 0); err != nil {
		test.Fatal(err)
	}
	var attemptID [16]byte
	copy(attemptID[:], header[48:64])
	attempt, err := backup.NewAttempt([16]byte{1}, [16]byte{2}, attemptID)
	if err != nil {
		test.Fatal(err)
	}
	ordered := slices.Clone(files[1 : len(files)-1])
	order := func(file *os.File) uint64 {
		var header [96]byte
		if _, err := file.ReadAt(header[:], 0); err != nil {
			test.Fatal(err)
		}
		priority := map[byte]uint64{3: 0, 2: 1, 1: 2}[header[12]]
		return priority<<32 | uint64(binary.BigEndian.Uint32(header[64:68]))
	}
	slices.SortFunc(ordered, func(first, second *os.File) int {
		if order(first) < order(second) {
			return -1
		}
		if order(first) > order(second) {
			return 1
		}
		return 0
	})
	return func(ctx context.Context, consume func(context.Context, backup.CryptoVerifiedBundle) error) (result error) {
		engine, err := backup.NewSealEngine(cryptoParent, recovery, cryptoLimits)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, engine.Close(), recovery.Destroy()) }()
		session, err := engine.BeginOpen(ctx, files[0], files[len(files)-1], attempt)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, session.Close()) }()
		for _, file := range ordered {
			if err := session.AcceptFile(ctx, file); err != nil {
				return err
			}
		}
		bundle, err := session.Finish(ctx)
		if err != nil {
			return err
		}
		defer func() {
			result = errors.Join(result, bundle.Close())
			for _, file := range files {
				result = errors.Join(result, file.Close())
			}
		}()
		return consume(ctx, bundle)
	}, key
}

func TestCandidatePreparedClosedSource(test *testing.T) {
	for _, owners := range []int{0, 2} {
		test.Run(fmt.Sprint(owners), func(test *testing.T) {
			source, key := candidateTestSource(test, owners)
			parent := candidateParent(test)
			candidate, err := PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
			if err != nil {
				test.Fatal(err)
			}
			defer candidate.Close()
			var retained VerifiedCandidate
			called := 0
			err = candidate.WithVerified(test.Context(), func(ctx context.Context, verified VerifiedCandidate) error {
				called++
				retained = verified
				if !verified.Valid() || verified.ObservationMaxAge() <= 0 || verified.ClosureSHA256() == [32]byte{} {
					test.Fatal("invalid scoped candidate")
				}
				return ctx.Err()
			})
			if err != nil || called != 1 || retained.Valid() {
				test.Fatal("candidate verification did not expire", err, called)
			}
			if err := candidate.Close(); err != nil {
				test.Fatal(err)
			}
			entries, err := os.ReadDir(parent.Name())
			if err != nil || len(entries) != 0 || !bytes.Equal(key, bytes.Repeat([]byte{87}, 32)) {
				test.Fatal("candidate cleanup or borrowed key ownership failed", err)
			}
		})
	}
}

func TestCandidateSourceFailureBarrier(test *testing.T) {
	for _, scenario := range []string{"late-close-error", "ignored-stage-error", "repeated-callback", "no-callback"} {
		test.Run(scenario, func(test *testing.T) {
			source, key := candidateTestSource(test, 2)
			parent := candidateParent(test)
			if scenario == "ignored-stage-error" {
				key = bytes.Repeat([]byte{99}, 32)
			}
			wrapped := func(ctx context.Context, consume func(context.Context, backup.CryptoVerifiedBundle) error) error {
				if scenario == "no-callback" {
					return nil
				}
				err := source(ctx, func(ctx context.Context, bundle backup.CryptoVerifiedBundle) error {
					err := consume(ctx, bundle)
					if scenario == "ignored-stage-error" {
						return nil
					}
					if err != nil {
						return err
					}
					if scenario == "repeated-callback" {
						_ = consume(ctx, bundle)
					}
					return nil
				})
				if scenario == "late-close-error" {
					return errors.Join(err, backup.ErrSealCleanup, errors.New("candidate-private-late-close-detail"))
				}
				return err
			}
			candidate, err := PrepareCandidate(test.Context(), wrapped, parent, key, candidateTestLimits())
			if candidate != nil || err == nil || strings.Contains(err.Error(), "candidate-private-") {
				test.Fatal("failed source admitted or leaked", err)
			}
			entries, readError := os.ReadDir(parent.Name())
			if readError != nil || len(entries) != 0 {
				test.Fatal("failed source left a trusted candidate", readError)
			}
		})
	}
}

func TestCandidateCanceledBeforeSource(test *testing.T) {
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	called := false
	source := func(context.Context, func(context.Context, backup.CryptoVerifiedBundle) error) error {
		called = true
		return nil
	}
	candidate, err := PrepareCandidate(ctx, source, candidateParent(test), bytes.Repeat([]byte{87}, 32), candidateTestLimits())
	if candidate != nil || !errors.Is(err, context.Canceled) || called {
		test.Fatal("canceled preparation invoked source", err, called)
	}
}

func TestCandidateCachePolicyIdentity(test *testing.T) {
	for _, scenario := range []string{"changed-policy", "replaced-policy", "changed-policy-same-identity"} {
		test.Run(scenario, func(test *testing.T) {
			source, key := candidateTestSource(test, 2)
			parent := candidateParent(test)
			candidate, err := PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
			if err != nil {
				test.Fatal(err)
			}
			defer candidate.Close()
			name := filepath.Join(parent.Name(), "candidate", "archives", ".cache-budget")
			content := []byte("skillhub-archive-budget-v1:1\n")
			info, err := os.Stat(name)
			if err != nil {
				test.Fatal(err)
			}
			if scenario == "replaced-policy" {
				content, err = os.ReadFile(name)
				if err != nil {
					test.Fatal(err)
				}
				if err := os.Rename(name, filepath.Join(parent.Name(), "original-budget-preserved")); err != nil {
					test.Fatal(err)
				}
			}
			if scenario == "changed-policy-same-identity" {
				content, err = os.ReadFile(name)
				if err != nil {
					test.Fatal(err)
				}
				content = bytes.Replace(content, []byte("67108864"), []byte("00000001"), 1)
			}
			if err := os.WriteFile(name, content, 0600); err != nil {
				test.Fatal(err)
			}
			if scenario == "changed-policy-same-identity" {
				if err := os.Chtimes(name, info.ModTime(), info.ModTime()); err != nil {
					test.Fatal(err)
				}
			}
			called := false
			err = candidate.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { called = true; return nil })
			if err == nil || called {
				test.Fatal("changed cache-policy file admitted")
			}
		})
	}
}

func TestCandidateCleanupDoesNotMixPathAndDescriptor(test *testing.T) {
	source, key := candidateTestSource(test, 2)
	parent := candidateParent(test)
	candidate, err := PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
	if err != nil {
		test.Fatal(err)
	}
	root := filepath.Join(parent.Name(), "candidate")
	original := filepath.Join(root, "archives")
	moved := filepath.Join(root, "moved-archives")
	if err := os.Rename(original, moved); err != nil {
		test.Fatal(err)
	}
	if err := os.Mkdir(original, 0700); err != nil {
		test.Fatal(err)
	}
	entries, err := os.ReadDir(moved)
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(moved, entry.Name()), filepath.Join(original, entry.Name())); err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(moved, entry.Name()), []byte("candidate-foreign-file-do-not-delete"), 0600); err != nil {
			test.Fatal(err)
		}
	}
	if err := candidate.Close(); !errors.Is(err, ErrCandidateCleanup) {
		test.Fatal("replaced archive directory not refused", err)
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(moved, entry.Name()))
		if err != nil || string(content) != "candidate-foreign-file-do-not-delete" {
			test.Fatal("foreign file deleted through mismatched directory FD", entry.Name(), err)
		}
	}
}

func TestCandidateRealDatabaseBinding(test *testing.T) {
	for _, field := range []string{"schema", "key", "database-bytes", "database-hash"} {
		test.Run(field, func(test *testing.T) {
			source, key := candidateTestSource(test, 2)
			candidate, err := PrepareCandidate(test.Context(), source, candidateParent(test), key, candidateTestLimits())
			if err != nil {
				test.Fatal(err)
			}
			defer candidate.Close()
			switch field {
			case "schema":
				candidate.value.metadata.SchemaSHA256 = strings.Repeat("7", 64)
			case "key":
				candidate.value.metadata.MasterKeyID = strings.Repeat("B", 43)
			case "database-bytes":
				candidate.value.metadata.Database.Bytes++
			case "database-hash":
				candidate.value.metadata.Database.SHA256 = strings.Repeat("8", 64)
				payload, err := os.ReadFile(filepath.Join(candidate.value.files.root.Name(), "inventory.jsonl"))
				if err != nil {
					test.Fatal(err)
				}
				evidence, err := candidate.value.inspectDatabase(test.Context())
				if !errors.Is(err, ErrCandidateInvalid) {
					test.Fatal("fixture binding no longer independent", evidence.Valid(), err)
				}
				candidate.value.metadata.Database.SHA256 = ""
				var actual state.RestoreDatabaseEvidence
				err = candidate.value.files.withFile("server.db", func(file *os.File) error {
					var err error
					actual, err = state.InspectRestoreDatabase(test.Context(), candidate.value.files.root, file, key, candidateTestLimits().Database)
					return err
				})
				if err != nil {
					test.Fatal(err)
				}
				candidate.value.metadata.Database.SHA256 = strings.Repeat("8", 64)
				length, digest, records := actual.References()
				inventoryRewriteHeader(test, &payload, &candidate.value.metadata, inventoryReferences{length, digest, records}, func(*inventoryHeader) {})
			}
			called := false
			err = candidate.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { called = true; return nil })
			if !errors.Is(err, ErrCandidateInvalid) || called {
				test.Fatal("unbound database metadata admitted", err)
			}
		})
	}
}

func TestCandidateFileClosureMutations(test *testing.T) {
	for _, scenario := range []string{"missing", "extra", "archive-bytes", "inventory-bytes", "database-bytes", "symlink", "hardlink"} {
		test.Run(scenario, func(test *testing.T) {
			source, key := candidateTestSource(test, 2)
			parent := candidateParent(test)
			candidate, err := PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
			if err != nil {
				test.Fatal(err)
			}
			defer candidate.Close()
			root := filepath.Join(parent.Name(), "candidate")
			entries, err := os.ReadDir(filepath.Join(root, "archives"))
			if err != nil {
				test.Fatal(err)
			}
			var archive string
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".tar.gz") {
					archive = filepath.Join(root, "archives", entry.Name())
				}
			}
			if archive == "" {
				test.Fatal("archive fixture absent")
			}
			switch scenario {
			case "missing":
				err = os.Remove(archive)
			case "extra":
				err = os.WriteFile(filepath.Join(root, "archives", "extra.tar.gz"), []byte("foreign-extra"), 0600)
			case "archive-bytes":
				err = os.WriteFile(archive, []byte("invalid-archive"), 0600)
			case "inventory-bytes":
				err = os.WriteFile(filepath.Join(root, "inventory.jsonl"), []byte("invalid-inventory"), 0600)
			case "database-bytes":
				err = os.WriteFile(filepath.Join(root, "server.db"), []byte("invalid-database"), 0600)
			case "symlink":
				if err = os.Rename(archive, filepath.Join(parent.Name(), "outside-archive")); err == nil {
					err = os.Symlink(filepath.Join(parent.Name(), "outside-archive"), archive)
				}
			case "hardlink":
				err = os.Link(archive, filepath.Join(parent.Name(), "outside-link"))
			}
			if err != nil {
				test.Fatal(err)
			}
			called := false
			err = candidate.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { called = true; return nil })
			if err == nil || called {
				test.Fatal("nonclosed candidate admitted")
			}
		})
	}
}

func TestCandidateStageRemainsUnready(test *testing.T) {
	source, key := candidateTestSource(test, 2)
	var staged *Candidate
	err := source(test.Context(), func(ctx context.Context, bundle backup.CryptoVerifiedBundle) error {
		var err error
		staged, err = StageCandidate(ctx, bundle, candidateParent(test), key, candidateTestLimits())
		return err
	})
	if err != nil {
		test.Fatal(err)
	}
	defer staged.Close()
	if err := staged.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { test.Fatal("unready stage delivered"); return nil }); !errors.Is(err, ErrCandidateBusy) {
		test.Fatal("direct staging gained source readiness", err)
	}
}

func TestCandidateLateSourceCallbackExpires(test *testing.T) {
	source, key := candidateTestSource(test, 2)
	var late func() error
	wrapped := func(ctx context.Context, consume func(context.Context, backup.CryptoVerifiedBundle) error) error {
		return source(ctx, func(ctx context.Context, bundle backup.CryptoVerifiedBundle) error {
			late = func() error { return consume(ctx, bundle) }
			return consume(ctx, bundle)
		})
	}
	candidate, err := PrepareCandidate(test.Context(), wrapped, candidateParent(test), key, candidateTestLimits())
	if err != nil {
		test.Fatal(err)
	}
	defer candidate.Close()
	if err := late(); !errors.Is(err, ErrCandidateClosed) {
		test.Fatal("escaped source callback remained live", err)
	}
	if err := candidate.WithVerified(test.Context(), func(ctx context.Context, verified VerifiedCandidate) error {
		if !verified.Valid() {
			test.Fatal("late rejected use corrupted valid preparation")
		}
		return nil
	}); err != nil {
		test.Fatal(err)
	}
	if err := candidate.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { test.Fatal("verification replayed"); return nil }); !errors.Is(err, ErrCandidateBusy) {
		test.Fatal("delivered capability was not one-shot", err)
	}
}

func TestCandidateCloseJoinsCallback(test *testing.T) {
	source, key := candidateTestSource(test, 2)
	candidate, err := PrepareCandidate(test.Context(), source, candidateParent(test), key, candidateTestLimits())
	if err != nil {
		test.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer candidate.Close()
	defer unblock()
	verifiedDone := make(chan error, 1)
	var capability VerifiedCandidate
	go func() {
		verifiedDone <- candidate.WithVerified(test.Context(), func(ctx context.Context, verified VerifiedCandidate) error {
			capability = verified
			close(entered)
			<-release
			return ctx.Err()
		})
	}()
	select {
	case <-entered:
	case err := <-verifiedDone:
		test.Fatal("verification failed before callback", err)
	case <-time.After(candidateTestLimits().OperationTimeout):
		test.Fatal("verification did not reach callback")
	}
	closeDone := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Go(func() { closeDone <- candidate.Close() })
	}
	<-candidate.value.ctx.Done()
	if capability.Valid() {
		test.Fatal("canceled capability stayed live")
	}
	select {
	case <-closeDone:
		test.Fatal("Close released candidate while callback still active")
	default:
	}
	unblock()
	wait.Wait()
	if err := <-verifiedDone; !errors.Is(err, context.Canceled) {
		test.Fatal("callback cancel category lost", err)
	}
	for range 2 {
		if err := <-closeDone; err != nil {
			test.Fatal(err)
		}
	}
	if candidate.value.key != [32]byte{} {
		test.Fatal("owned original key not cleared")
	}
}

func TestCandidatePrivacyAndBorrowing(test *testing.T) {
	source, key := candidateTestSource(test, 2)
	parent := candidateParent(test)
	candidate, err := PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
	if err != nil {
		test.Fatal(err)
	}
	defer candidate.Close()
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if output := fmt.Sprintf(format, candidate); strings.Contains(output, parent.Name()) || strings.Contains(output, "candidate-synthetic-configuration-secret") {
			test.Fatal("candidate formatter leaked")
		}
	}
	encoded, err := json.Marshal(candidate)
	if err != nil || string(encoded) != "{}" {
		test.Fatal("candidate JSON exposed fields", err)
	}
	if err := candidate.WithVerified(test.Context(), func(ctx context.Context, verified VerifiedCandidate) error {
		encoded, err := json.Marshal(verified)
		if err != nil || string(encoded) != "{}" {
			test.Fatal("verified JSON exposed fields", err)
		}
		return errors.New("candidate-private-callback-secret")
	}); !errors.Is(err, ErrCandidateInvalid) || strings.Contains(err.Error(), "private") {
		test.Fatal("callback error leaked", err)
	}
	if _, err := parent.Stat(); err != nil || !bytes.Equal(key, bytes.Repeat([]byte{87}, 32)) {
		test.Fatal("borrowed arguments changed", err)
	}
	if !bytes.Equal(candidate.value.key[:], key) {
		test.Fatal("owned key missing before Close")
	}
}

func TestCandidateUnknownConsumerRetainsFence(test *testing.T) {
	source, key := candidateTestSource(test, 2)
	parent := candidateParent(test)
	candidate, err := PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
	if err != nil {
		test.Fatal(err)
	}
	err = candidate.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error {
		return errors.Join(ErrCandidateUncertain, errors.New("candidate-private-consumer-detail"))
	})
	if !errors.Is(err, ErrCandidateUncertain) || strings.Contains(err.Error(), "private") {
		test.Fatal("consumer unknown category lost", err)
	}
	if err := candidate.Close(); !errors.Is(err, ErrCandidateUncertain) {
		test.Fatal("consumer uncertainty was erased by cleanup", err)
	}
	if _, err := os.Stat(filepath.Join(parent.Name(), candidateFence)); err != nil {
		test.Fatal("consumer uncertainty did not fence admission", err)
	}
}

func TestCandidatePhysicalFailureFences(test *testing.T) {
	for _, point := range []string{"database-read-close", "archive-write-close", "archives-close", "root-close", "parent-close", "unlink", "directory-sync", "final-fence-remove"} {
		test.Run(point, func(test *testing.T) {
			source, key := candidateTestSource(test, 2)
			parent := candidateParent(test)
			var candidate *Candidate
			var err error
			operations := defaultCandidateOps()
			if point == "archive-write-close" {
				original := operations.close
				operations.close = func(file *os.File) error {
					err := original(file)
					if strings.HasSuffix(file.Name(), ".tar.gz") {
						return errors.Join(err, errors.New("synthetic-close-uncertain"))
					}
					return err
				}
				candidate, err = prepareCandidate(test.Context(), source, parent, key, candidateTestLimits(), operations)
				if candidate != nil || !errors.Is(err, ErrCandidateUncertain) {
					test.Fatal("uncertain write-close admitted", err)
				}
			} else {
				candidate, err = PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
				if err != nil {
					test.Fatal(err)
				}
				original := operations.close
				operations.close = func(file *os.File) error {
					err := original(file)
					match := point == "database-read-close" && strings.HasSuffix(file.Name(), "/server.db") || point == "archives-close" && strings.HasSuffix(file.Name(), "/archives") || point == "root-close" && strings.HasSuffix(file.Name(), "/candidate") || point == "parent-close" && file.Name() == parent.Name()
					if match {
						return errors.Join(err, errors.New("synthetic-close-uncertain"))
					}
					return err
				}
				if point == "unlink" {
					operations.unlink = func(*os.File, string, bool) error { return ErrCandidateCleanup }
				}
				if point == "directory-sync" {
					operations.sync = func(file *os.File) error {
						if strings.HasSuffix(file.Name(), "/archives") {
							return ErrCandidateCleanup
						}
						return file.Sync()
					}
				}
				if point == "final-fence-remove" {
					operations.finalRemove = func(string) error { return ErrCandidateCleanup }
				}
				candidate.value.files.ops = operations
				if point == "database-read-close" {
					called := false
					err = candidate.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { called = true; return nil })
					if !errors.Is(err, ErrCandidateUncertain) || called {
						test.Fatal("read-close failure delivered capability", err)
					}
				}
				err = candidate.Close()
				if err == nil {
					test.Fatal("physical cleanup failure erased")
				}
				if candidate.value.key != [32]byte{} {
					test.Fatal("failed cleanup retained key")
				}
			}
			if _, err := os.Stat(filepath.Join(parent.Name(), candidateFence)); err != nil {
				test.Fatal("failure lost durable admission fence", err)
			}
			fresh := &candidateFiles{ops: defaultCandidateOps()}
			err = fresh.initialize(parent)
			if !errors.Is(err, ErrCandidateBusy) {
				test.Fatal("new staging bypassed failure fence", err)
			}
			if fresh != nil {
				if err := fresh.cleanup(); err != nil {
					test.Fatal("new rejected attempt could not clean its own duplicate", err)
				}
			}
			if _, err := parent.Stat(); err != nil {
				test.Fatal("borrowed parent closed", err)
			}
		})
	}
}

func TestCandidateAdmissionAndBudgets(test *testing.T) {
	for _, scenario := range []string{"wrong-key", "cache-budget", "staging-budget", "existing-fence", "existing-slot", "public-parent"} {
		test.Run(scenario, func(test *testing.T) {
			source, key := candidateTestSource(test, 2)
			parent := candidateParent(test)
			limits := candidateTestLimits()
			switch scenario {
			case "wrong-key":
				key = bytes.Repeat([]byte{99}, 32)
			case "cache-budget":
				limits.ArchiveCacheBytes = 1
			case "staging-budget":
				limits.MaxStagingBytes = 1
			case "existing-fence":
				if err := os.WriteFile(filepath.Join(parent.Name(), candidateFence), []byte("foreign-fence"), 0600); err != nil {
					test.Fatal(err)
				}
			case "existing-slot":
				if err := os.Mkdir(filepath.Join(parent.Name(), "candidate"), 0700); err != nil {
					test.Fatal(err)
				}
			case "public-parent":
				if err := os.Chmod(parent.Name(), 0755); err != nil {
					test.Fatal(err)
				}
				defer os.Chmod(parent.Name(), 0700)
			}
			candidate, err := PrepareCandidate(test.Context(), source, parent, key, limits)
			if candidate != nil || err == nil {
				test.Fatal("invalid admission or budget accepted")
			}
			if scenario == "wrong-key" && !errors.Is(err, ErrCandidateKey) {
				test.Fatal("wrong key category lost", err)
			}
			if scenario == "cache-budget" || scenario == "staging-budget" {
				if !errors.Is(err, ErrCandidateBudget) {
					test.Fatal("budget category lost", err)
				}
			}
			if scenario == "existing-fence" {
				content, readError := os.ReadFile(filepath.Join(parent.Name(), candidateFence))
				if readError != nil || string(content) != "foreign-fence" {
					test.Fatal("foreign fence removed", readError)
				}
			}
		})
	}
}

func TestCandidateFinalFenceFailureAfterRelease(test *testing.T) {
	source, key := candidateTestSource(test, 2)
	parent := candidateParent(test)
	candidate, err := PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
	if err != nil {
		test.Fatal(err)
	}
	candidate.value.files.ops.finalRemove = func(name string) error {
		files := candidate.value.files
		if files.parent != nil || files.root != nil || files.archives != nil {
			test.Fatal("final fence removed before resource release")
		}
		if err := os.Remove(name); err != nil {
			return err
		}
		return errors.New("synthetic-late-fence-error")
	}
	if err := candidate.Close(); !errors.Is(err, ErrCandidateCleanup) {
		test.Fatal("late cleanup outcome hidden", err)
	}
	if _, err := os.Stat(filepath.Join(parent.Name(), candidateFence)); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("fixture did not really remove final fence", err)
	}
	fresh := &candidateFiles{ops: defaultCandidateOps()}
	err = fresh.initialize(parent)
	if err != nil {
		test.Fatal("known-released resources treated as still locked", err)
	}
	if err := fresh.cleanup(); err != nil {
		test.Fatal(err)
	}
}

func TestCandidateStagePhysicalOwnership(test *testing.T) {
	for _, point := range []string{"fence-close", "database-close", "inventory-close", "fence-sync", "database-sync", "inventory-sync", "root-reader-close", "archive-reader-close"} {
		test.Run(point, func(test *testing.T) {
			source, key := candidateTestSource(test, 2)
			parent := candidateParent(test)
			operations := defaultCandidateOps()
			physicalClose := operations.close
			seen := make(map[*os.File]bool)
			operations.close = func(file *os.File) error {
				if seen[file] {
					test.Fatal("physical close retried")
				}
				seen[file] = true
				err := physicalClose(file)
				name := file.Name()
				match := point == "fence-close" && strings.HasSuffix(name, "/"+candidateFence) || point == "database-close" && strings.HasSuffix(name, "/server.db") || point == "inventory-close" && strings.HasSuffix(name, "/inventory.jsonl") || point == "root-reader-close" && strings.HasSuffix(name, "/candidate") || point == "archive-reader-close" && strings.HasSuffix(name, "/archives")
				if match {
					return errors.Join(err, errors.New("synthetic-stage-close-uncertain"))
				}
				return err
			}
			operations.sync = func(file *os.File) error {
				name := file.Name()
				match := point == "fence-sync" && strings.HasSuffix(name, "/"+candidateFence) || point == "database-sync" && strings.HasSuffix(name, "/server.db") || point == "inventory-sync" && strings.HasSuffix(name, "/inventory.jsonl")
				if match {
					return ErrCandidateCleanup
				}
				return file.Sync()
			}
			candidate, err := prepareCandidate(test.Context(), source, parent, key, candidateTestLimits(), operations)
			if candidate != nil || err == nil {
				test.Fatal("failed stage returned a candidate")
			}
			if strings.Contains(point, "close") && !errors.Is(err, ErrCandidateUncertain) {
				test.Fatal("close uncertainty lost", err)
			}
			if strings.Contains(point, "close") || point == "fence-sync" {
				if _, err := os.Stat(filepath.Join(parent.Name(), candidateFence)); err != nil {
					test.Fatal("uncertain stage lost fence", err)
				}
			}
			if !bytes.Equal(key, bytes.Repeat([]byte{87}, 32)) {
				test.Fatal("borrowed key changed")
			}
		})
	}
}

func TestCandidateVerifierPhysicalOwnership(test *testing.T) {
	for _, suffix := range []string{"/inventory.jsonl", ".tar.gz", "/archives/.cache-budget", "/candidate", "/archives"} {
		test.Run(suffix, func(test *testing.T) {
			source, key := candidateTestSource(test, 2)
			parent := candidateParent(test)
			candidate, err := PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
			if err != nil {
				test.Fatal(err)
			}
			physicalClose := candidate.value.files.ops.close
			candidate.value.files.ops.close = func(file *os.File) error {
				err := physicalClose(file)
				if strings.HasSuffix(file.Name(), suffix) {
					return errors.Join(err, errors.New("synthetic-verifier-close-uncertain"))
				}
				return err
			}
			called := false
			err = candidate.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { called = true; return nil })
			if !errors.Is(err, ErrCandidateUncertain) || called {
				test.Fatal("verifier used unclosed resources", err)
			}
			if err := candidate.Close(); !errors.Is(err, ErrCandidateUncertain) {
				test.Fatal("uncertain verifier cleanup downgraded", err)
			}
			if _, err := os.Stat(filepath.Join(parent.Name(), candidateFence)); err != nil {
				test.Fatal("uncertain verifier lost fence", err)
			}
		})
	}
}

func TestCandidateLastSharedRepositoryRejected(test *testing.T) {
	source, key := candidateTestSource(test, 2)
	candidate, err := PrepareCandidate(test.Context(), source, candidateParent(test), key, candidateTestLimits())
	if err != nil {
		test.Fatal(err)
	}
	defer candidate.Close()
	files := candidate.value.files
	databaseName := filepath.Join(files.root.Name(), "server.db")
	address := (&url.URL{Scheme: "file", Path: filepath.ToSlash(databaseName)}).String() + "?mode=rw&_pragma=journal_mode(DELETE)"
	pool, err := sql.Open("sqlite3", address)
	if err != nil {
		test.Fatal(err)
	}
	var identity int64
	var oldHash string
	if err := pool.QueryRowContext(test.Context(), "SELECT id,content_hash FROM collected_skills ORDER BY id DESC LIMIT 1").Scan(&identity, &oldHash); err != nil {
		test.Fatal(err)
	}
	newHash := strings.Repeat("7", 64)
	if _, err := pool.ExecContext(test.Context(), "UPDATE collected_skills SET content_hash=? WHERE id=?", newHash, identity); err != nil {
		test.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		test.Fatal(err)
	}
	data, err := os.ReadFile(databaseName)
	if err != nil {
		test.Fatal(err)
	}
	digest := sha256.Sum256(data)
	candidate.value.metadata.Database.Bytes = int64(len(data))
	candidate.value.metadata.Database.SHA256 = hex.EncodeToString(digest[:])
	files.entries["server.db"], err = os.Stat(databaseName)
	if err != nil {
		test.Fatal(err)
	}
	evidence, err := candidate.value.inspectDatabase(test.Context())
	if err != nil {
		test.Fatal("content declaration should pass T1", err)
	}
	inventoryName := filepath.Join(files.root.Name(), "inventory.jsonl")
	payload, err := os.ReadFile(inventoryName)
	if err != nil {
		test.Fatal(err)
	}
	lines := bytes.SplitAfter(payload, []byte{'\n'})
	changed := false
	for index, line := range lines[1:] {
		if len(line) == 0 {
			continue
		}
		var row struct {
			Kind string `json:"kind"`
			ID   int64  `json:"id"`
		}
		if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &row); err != nil {
			test.Fatal(err)
		}
		if row.Kind == "skill" && row.ID == identity {
			lines[index+1] = bytes.Replace(line, []byte(oldHash), []byte(newHash), 1)
			changed = true
		}
	}
	if !changed {
		test.Fatal("last shared repository fixture was not changed")
	}
	payload = bytes.Join(lines, nil)
	length, referenceHash, records := evidence.References()
	inventoryRewriteHeader(test, &payload, &candidate.value.metadata, inventoryReferences{length, referenceHash, records}, func(*inventoryHeader) {})
	if err := os.WriteFile(inventoryName, payload, 0600); err != nil {
		test.Fatal(err)
	}
	files.entries["inventory.jsonl"], err = os.Stat(inventoryName)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := readInventory(test.Context(), bytes.NewReader(payload), candidate.value.metadata, inventoryReferences{length, referenceHash, records}, inventoryLimits{candidate.value.limits.MaxInventoryBytes, candidate.value.limits.MaxArchives, candidate.value.limits.MaxContentBytes}); err != nil {
		test.Fatal("self-consistent inventory fixture refused before archive semantics", err)
	}
	called := false
	err = candidate.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { called = true; return nil })
	if !errors.Is(err, ErrCandidateInvalid) || called {
		test.Fatal("last shared repository content mismatch escaped complete verification", err)
	}
}

func TestCandidateEscapedMaterializerIsCanceledAndJoined(test *testing.T) {
	source, key := candidateTestSource(test, 2)
	parent := candidateParent(test)
	entered := make(chan struct{})
	sourceDone := make(chan error, 1)
	var sourceContext context.Context
	operations := defaultCandidateOps()
	operations.sync = func(file *os.File) error {
		if strings.HasSuffix(file.Name(), "/"+candidateFence) {
			close(entered)
			<-sourceContext.Done()
		}
		return file.Sync()
	}
	wrapped := func(ctx context.Context, consume func(context.Context, backup.CryptoVerifiedBundle) error) error {
		sourceContext = ctx
		go func() {
			sourceDone <- source(test.Context(), func(_ context.Context, bundle backup.CryptoVerifiedBundle) error {
				return consume(context.Background(), bundle)
			})
		}()
		select {
		case <-entered:
			return nil
		case err := <-sourceDone:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	candidate, err := prepareCandidate(test.Context(), wrapped, parent, key, candidateTestLimits(), operations)
	if candidate != nil || err == nil {
		test.Fatal("escaped materializer became ready")
	}
	entries, readError := os.ReadDir(parent.Name())
	if readError != nil || len(entries) != 0 {
		test.Fatal("preparation returned before escaped stage cleanup", readError)
	}
	select {
	case <-sourceDone:
	case <-time.After(candidateTestLimits().OperationTimeout):
		test.Fatal("fixture source was not joined")
	}
}

func TestCandidateNilCallbackContextIsRefused(test *testing.T) {
	source := func(ctx context.Context, consume func(context.Context, backup.CryptoVerifiedBundle) error) error {
		_ = consume(nil, backup.CryptoVerifiedBundle{})
		return nil
	}
	candidate, err := PrepareCandidate(test.Context(), source, candidateParent(test), bytes.Repeat([]byte{87}, 32), candidateTestLimits())
	if candidate != nil || !errors.Is(err, ErrCandidateInvalid) {
		test.Fatal("nil source callback context admitted", err)
	}
}

func TestCandidatePanicPreservesUnknownOwnership(test *testing.T) {
	for _, point := range []string{"source", "consumer"} {
		test.Run(point, func(test *testing.T) {
			source, key := candidateTestSource(test, 2)
			parent := candidateParent(test)
			var candidate *Candidate
			observed := false
			func() {
				defer func() {
					if recover() != nil {
						observed = true
					}
				}()
				if point == "source" {
					_, _ = PrepareCandidate(test.Context(), func(ctx context.Context, consume func(context.Context, backup.CryptoVerifiedBundle) error) error {
						if err := source(ctx, consume); err != nil {
							return err
						}
						panic("synthetic-source-panic")
					}, parent, key, candidateTestLimits())
				} else {
					var err error
					candidate, err = PrepareCandidate(test.Context(), source, parent, key, candidateTestLimits())
					if err != nil {
						test.Fatal(err)
					}
					_ = candidate.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { panic("synthetic-consumer-panic") })
				}
			}()
			if !observed {
				test.Fatal("panic was silently converted")
			}
			if candidate != nil {
				if err := candidate.Close(); !errors.Is(err, ErrCandidateUncertain) {
					test.Fatal("panic uncertainty was erased", err)
				}
				if candidate.value.key != [32]byte{} {
					test.Fatal("panic retained owned key")
				}
			}
			if _, err := os.Stat(filepath.Join(parent.Name(), candidateFence)); err != nil {
				test.Fatal("panic lost admission fence", err)
			}
		})
	}
}

func TestCandidateStagePanicCompletesSource(test *testing.T) {
	for _, point := range []string{candidateFence, "parent", "server.db", "inventory.jsonl", ".cache-budget", "archive", "archives", "candidate"} {
		test.Run(point, func(test *testing.T) {
			source, key := candidateTestSource(test, 2)
			parent := candidateParent(test)
			const marker = "synthetic-stage-sync-panic"
			sourceUnwound := make(chan struct{})
			wrapped := func(ctx context.Context, consume func(context.Context, backup.CryptoVerifiedBundle) error) error {
				defer close(sourceUnwound)
				return source(ctx, consume)
			}
			operations := defaultCandidateOps()
			operations.sync = func(file *os.File) error {
				if filepath.Base(file.Name()) == point || point == "parent" && file.Name() == parent.Name() || point == "archive" && strings.HasSuffix(file.Name(), ".tar.gz") {
					panic(marker)
				}
				return file.Sync()
			}
			recovered := make(chan any, 1)
			go func() {
				defer func() { recovered <- recover() }()
				_, _ = prepareCandidate(test.Context(), wrapped, parent, key, candidateTestLimits(), operations)
			}()
			select {
			case <-sourceUnwound:
			case <-time.After(5 * time.Second):
				test.Fatal("source did not unwind")
			}
			select {
			case value := <-recovered:
				if value != marker {
					test.Fatal("original stage panic was lost", value)
				}
			case <-time.After(time.Second):
				test.Fatal("preparation waited on its own unwound callback")
			}
			if !bytes.Equal(key, bytes.Repeat([]byte{87}, 32)) {
				test.Fatal("stage panic changed borrowed key")
			}
			if _, err := parent.Stat(); err != nil {
				test.Fatal("stage panic closed borrowed parent", err)
			}
			if _, err := os.Stat(filepath.Join(parent.Name(), candidateFence)); err != nil {
				test.Fatal("stage panic lost admission fence", err)
			}
			descriptors, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				test.Fatal(err)
			}
			owned := 0
			for _, descriptor := range descriptors {
				name, err := os.Readlink(filepath.Join("/proc/self/fd", descriptor.Name()))
				if err == nil && (name == parent.Name() || strings.HasPrefix(name, parent.Name()+"/")) {
					owned++
				}
			}
			if owned != 1 {
				test.Fatal("stage panic did not close its owned descriptors", owned)
			}
		})
	}
}
