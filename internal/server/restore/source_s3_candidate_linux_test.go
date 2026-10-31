package restore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

func sourceS3CapturedFixture(test *testing.T, owners int) (*sourceS3Fixture, []byte) {
	test.Helper()
	fixture := sourceS3TestFixture(test)
	live := candidateParent(test)
	key := bytes.Repeat([]byte{87}, 32)
	store, err := state.OpenWithOptions(test.Context(), filepath.Join(live.Name(), "server.db"), key, state.Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	cache, err := library.OpenArchiveCacheWithOptions(test.Context(), filepath.Join(live.Name(), "archives"), library.CacheOptions{MaintenanceManaged: true})
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
	if err := store.CreateUser(test.Context(), "review-unused-user"); err != nil {
		test.Fatal(err)
	}
	for ownerIndex := range owners {
		owner := fmt.Sprintf("review-owner-%d", ownerIndex)
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
		pack, err := store.CreatePack(test.Context(), owner, state.PackDraft{Name: "review pack", Items: []state.PackSelection{{SkillID: entry.Skills[0].ID, Slug: "first"}, {SkillID: entry.Skills[1].ID, Slug: "second"}}})
		if err != nil {
			test.Fatal(err)
		}
		category, err := store.CreateCategory(test.Context(), owner, "review category")
		if err != nil {
			test.Fatal(err)
		}
		if _, err := store.SetSkillCategories(test.Context(), owner, entry.Skills[0].ID, []string{category.ID}); err != nil {
			test.Fatal(err)
		}
		asset, err := store.CreateConfigurationAsset(test.Context(), owner, state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "review configuration", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte("review-synthetic-only"))})
		if err != nil {
			test.Fatal(err)
		}
		if _, err := store.ReplacePackConfigurationAttachments(test.Context(), owner, pack.ID, pack.Revision, 1, []state.PackConfigurationReference{{AssetID: asset.ID, AssetRevision: asset.Revision, TargetID: "codex.provider.user"}}); err != nil {
			test.Fatal(err)
		}
	}
	fixture.limits = backup.SealLimits{TotalPlainBytes: 64 << 20, TotalCipherBytes: 64 << 20, StagingBytes: 128 << 20, InputStagingBytes: 8 << 20, MaxObjects: 1000, MaxInventoryRecords: 100000, OperationTimeout: time.Minute}
	recovery, err := backup.ImportRecoveryKey([16]byte{9}, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	defer recovery.Destroy()
	producer, err := backup.NewSealEngine(candidateParent(test), recovery, fixture.limits)
	if err != nil {
		test.Fatal(err)
	}
	defer producer.Close()
	captured, err := producer.CaptureSnapshot(test.Context(), service, backup.CaptureOptions{InstanceID: [16]byte{1}, BackupID: [16]byte{2}, AppVersion: "review-tls-real-db", Limits: library.ContentSnapshotLimits{Database: candidateTestLimits().Database, MaxArchives: 1000, MaxInventoryBytes: 8 << 20, MaxContentBytes: 128 << 20}})
	if err != nil {
		test.Fatal(err)
	}
	defer captured.Close()
	var objects [][]byte
	if err := captured.WithFiles(test.Context(), func(ctx context.Context, _ backup.ObjectRef, input *os.File) error {
		data, err := io.ReadAll(io.NewSectionReader(input, 0, 16<<20))
		objects = append(objects, data)
		return errors.Join(err, ctx.Err())
	}); err != nil {
		test.Fatal(err)
	}
	if len(objects) < 5 || len(objects[0]) != 156 {
		test.Fatal("capture did not produce complete objects")
	}
	var attemptID [16]byte
	copy(attemptID[:], objects[0][48:64])
	fixture.attempt, err = backup.NewAttempt([16]byte{1}, [16]byte{2}, attemptID)
	if err != nil {
		test.Fatal(err)
	}
	prefix := fmt.Sprintf("/synthetic/backups/%x/backups/%x/%x/", [16]byte{1}, [16]byte{2}, attemptID)
	fixture.objects = make(map[string][]byte)
	for objectIndex, data := range objects {
		kind, ordinal := "", uint32(0)
		switch objectIndex {
		case 0:
			kind = "bootstrap"
		case len(objects) - 1:
			kind = "complete"
		default:
			kind = map[byte]string{1: "data", 2: "page", 3: "page"}[data[12]]
			ordinal = binary.BigEndian.Uint32(data[64:68])
		}
		if kind == "" {
			test.Fatal("unexpected fixture object purpose")
		}
		fixture.objects[prefix+fmt.Sprintf("%s/%08x", kind, ordinal)] = data
	}
	if err := errors.Join(captured.Close(), producer.Close(), recovery.Destroy(), service.Close(), cache.Close(), store.Close()); err != nil {
		test.Fatal(err)
	}
	if err := os.Rename(live.Name(), live.Name()+"-offline"); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = os.Rename(live.Name()+"-offline", live.Name()) })
	test.Logf("offline SQLite CaptureSnapshot complete: owners=%d sealed-objects=%d", owners, len(objects))
	return fixture, key
}

func TestS3CandidateCapturedEndToEnd(test *testing.T) {
	for _, scenario := range []string{"empty", "shared", "head-cleanup", "head-unknown", "target-unknown", "cancel-and-unknown", "consumer-error", "last-ciphertext", "wrong-db-key"} {
		test.Run(scenario, func(test *testing.T) {
			owners := 2
			if scenario == "empty" {
				owners = 0
			}
			fixture, dbKey := sourceS3CapturedFixture(test, owners)
			parent := candidateParent(test)
			ctx, cancel := context.WithCancelCause(test.Context())
			defer cancel(nil)
			operations := defaultSourceS3Ops()
			operations.target = func(options backup.S3Options) (sourceS3Target, error) {
				target, err := backup.NewS3Target(options)
				return sourceS3TargetWrapper{sourceS3Target: target, head: func(ctx context.Context, parent *os.File, attempt backup.Attempt, consume func(context.Context, *os.File, *os.File) error) error {
					err := target.WithRestoreHead(ctx, parent, attempt, consume)
					if scenario == "head-cleanup" {
						err = errors.Join(err, backup.ErrSealCleanup)
					}
					if scenario == "head-unknown" {
						err = errors.Join(err, backup.ErrUnavailable)
					}
					return err
				}, close: func() error {
					err := target.Close()
					if scenario == "target-unknown" {
						err = errors.Join(err, backup.ErrUnavailable)
					}
					return err
				}}, err
			}
			if scenario == "cancel-and-unknown" {
				operations.closeBundle = func(bundle backup.CryptoVerifiedBundle) error {
					err := bundle.Close()
					cancel(errors.New("review-private-cancel-cause"))
					return errors.Join(err, syscall.EIO)
				}
			}
			if scenario == "last-ciphertext" {
				last := ""
				for name := range fixture.objects {
					if strings.Contains(name, "/data/") && name > last {
						last = name
					}
				}
				fixture.objects[last][len(fixture.objects[last])-1] ^= 1
			}
			if scenario == "wrong-db-key" {
				dbKey = bytes.Repeat([]byte{99}, 32)
			}
			bridge := s3CandidateSource(fixture.options, fixture.parent, fixture.attempt, [16]byte{9}, fixture.key, fixture.limits, operations)
			callbacks := 0
			stageSucceeded, sourceReturned := false, false
			var borrowed backup.CryptoVerifiedBundle
			source := func(ctx context.Context, consume func(context.Context, backup.CryptoVerifiedBundle) error) error {
				defer func() { sourceReturned = true }()
				return bridge(ctx, func(ctx context.Context, bundle backup.CryptoVerifiedBundle) error {
					callbacks++
					borrowed = bundle
					err := consume(ctx, bundle)
					stageSucceeded = err == nil
					if scenario == "consumer-error" {
						return errors.Join(err, errors.New("review-private-consumer-error"))
					}
					return err
				})
			}
			candidate, err := PrepareCandidate(ctx, source, parent, dbKey, candidateTestLimits())
			success := scenario == "empty" || scenario == "shared"
			if !sourceReturned {
				test.Fatal("Prepare escaped Source return barrier")
			}
			if success {
				if err != nil || candidate == nil || callbacks != 1 || !stageSucceeded {
					test.Fatal("real S3 -> Prepare failed", err, callbacks, stageSucceeded)
				}
				var escaped VerifiedCandidate
				if err := candidate.WithVerified(test.Context(), func(ctx context.Context, verified VerifiedCandidate) error {
					escaped = verified
					if !verified.Valid() || verified.ClosureSHA256() == [32]byte{} {
						test.Fatal("invalid verified capability")
					}
					return ctx.Err()
				}); err != nil || escaped.Valid() {
					test.Fatal("real closure gate or capability expiration failed", err)
				}
				if err := candidate.WithVerified(test.Context(), func(context.Context, VerifiedCandidate) error { test.Fatal("one-shot reused"); return nil }); err == nil {
					test.Fatal("one-shot gate accepted twice")
				}
				if err := candidate.Close(); err != nil {
					test.Fatal(err)
				}
			} else {
				if candidate != nil || err == nil || strings.Contains(err.Error(), "review-private") {
					test.Fatal("failed Source produced ready Candidate or leaked", err)
				}
				if scenario == "head-cleanup" || scenario == "head-unknown" || scenario == "last-ciphertext" {
					if callbacks != 0 {
						test.Fatal("untrusted Source entered materializer")
					}
				} else if callbacks != 1 {
					test.Fatal("negative control did not reach real materializer", callbacks)
				}
				if scenario == "target-unknown" || scenario == "cancel-and-unknown" {
					if err != ErrCandidateUncertain || !stageSucceeded {
						test.Fatal("late uncertainty lost after real successful staging", err, stageSucceeded)
					}
				}
				if scenario == "head-cleanup" && (err != ErrCandidateCleanup || len(fixture.calls) != 4) {
					test.Fatal("head cleanup did not block object downloads", err, len(fixture.calls))
				}
				if scenario == "head-unknown" && (err != ErrCandidateUncertain || len(fixture.calls) != 4) {
					test.Fatal("head unknown demoted or downloads continued", err, len(fixture.calls))
				}
			}
			if callbacks != 0 {
				if _, err := borrowed.Metadata(test.Context()); !errors.Is(err, backup.ErrClosed) {
					test.Fatal("bundle outlived Source", err)
				}
			}
			for _, directory := range []*os.File{parent, fixture.parent} {
				entries, err := os.ReadDir(directory.Name())
				if err != nil || len(entries) != 0 {
					test.Fatal("scope not fully cleaned", err, len(entries))
				}
			}
			if !bytes.Equal(fixture.key, bytes.Repeat([]byte{13}, 32)) {
				test.Fatal("borrowed recovery key modified")
			}
			test.Logf("scenario=%s callbacks=%d stage-ok=%v Source-returned=%v TLS-requests=%d", scenario, callbacks, stageSucceeded, sourceReturned, len(fixture.calls))
		})
	}
}

func TestS3CandidateCapturedColdReuse(test *testing.T) {
	fixture := sourceS3TestFixture(test)
	source := fixture.source()
	entries, err := os.ReadDir(fixture.parent.Name())
	if err != nil || len(entries) != 0 || len(fixture.calls) != 0 {
		test.Fatal("cold constructor acquired resources", err)
	}
	for iteration := range 2 {
		called := 0
		if err := source(test.Context(), func(context.Context, backup.CryptoVerifiedBundle) error { called++; return nil }); err != nil || called != 1 {
			test.Fatal("borrowed options/key/parent not reusable", iteration, err, called)
		}
	}
	if len(fixture.calls) != 32 || !bytes.Equal(fixture.key, bytes.Repeat([]byte{13}, 32)) {
		test.Fatal("reused source did not perform full authentication twice")
	}
}

func TestS3CandidateCapturedCopyStatus(test *testing.T) {
	for _, status := range []backup.VerifyResult{backup.Missing, backup.VerifyUnknown, backup.VerifyConflict} {
		test.Run(fmt.Sprint(status), func(test *testing.T) {
			fixture := sourceS3TestFixture(test)
			operations := defaultSourceS3Ops()
			operations.target = func(options backup.S3Options) (sourceS3Target, error) {
				target, err := backup.NewS3Target(options)
				return sourceS3TargetWrapper{sourceS3Target: target, copy: func(ctx context.Context, ref backup.ObjectRef, output io.Writer) (backup.VerifyResult, error) {
					verified, err := target.CopyExact(ctx, ref, output)
					if verified == backup.Verified && err == nil {
						return status, nil
					}
					return verified, err
				}}, err
			}
			sourceS3AssertFailure(test, fixture, s3CandidateSource(fixture.options, fixture.parent, fixture.attempt, [16]byte{9}, fixture.key, fixture.limits, operations), test.Context(), ErrCandidateInvalid)
			if len(fixture.calls) != 7 {
				test.Fatal("non-Verified result did not stop at first object", len(fixture.calls))
			}
		})
	}
}
