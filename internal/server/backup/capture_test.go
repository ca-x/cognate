package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

type captureFetcher struct{ archive *repository.ArchiveSnapshot }

func (fetcher captureFetcher) Fetch(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
	return fetcher.archive, nil
}

func captureFixture(test *testing.T) (*library.Service, *state.Store, *library.ArchiveCache, string, *repository.ArchiveSnapshot) {
	test.Helper()
	if runtime.GOOS != "linux" {
		test.Skip("real managed SQLite snapshot worker requires Linux")
	}
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	store, err := state.OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), bytes.Repeat([]byte{91}, 32), state.Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	cache, err := library.OpenArchiveCacheWithOptions(test.Context(), filepath.Join(directory, "archives"), library.CacheOptions{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = cache.Close() })
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	content := "---\nname: Capture\ndescription: Private capture fixture\n---\ncapture-master-key-secret-sentinel"
	if err := tarWriter.WriteHeader(&tar.Header{Name: "source-main/SKILL.md", Mode: 0644, Size: int64(len(content))}); err != nil {
		test.Fatal(err)
	}
	if _, err := io.WriteString(tarWriter, content); err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(tarWriter.Close(), gzipWriter.Close()); err != nil {
		test.Fatal(err)
	}
	archive, err := repository.InspectArchive(test.Context(), output.Bytes())
	if err != nil {
		test.Fatal(err)
	}
	service, err := library.NewService(store, cache, captureFetcher{archive: archive})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	for _, owner := range []string{"capture-owner-one", "capture-owner-two"} {
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
		if _, err := store.CreatePack(test.Context(), owner, state.PackDraft{Name: "private pack", Items: []state.PackSelection{{SkillID: entry.Skills[0].ID, Slug: "same"}}}); err != nil {
			test.Fatal(err)
		}
		if owner == "capture-owner-one" {
			category, err := store.CreateCategory(test.Context(), owner, "private category")
			if err != nil {
				test.Fatal(err)
			}
			if _, err := store.SetSkillCategories(test.Context(), owner, entry.Skills[0].ID, []string{category.ID}); err != nil {
				test.Fatal(err)
			}
		}
	}
	return service, store, cache, directory, archive
}

func captureOptions() CaptureOptions {
	return CaptureOptions{InstanceID: [16]byte{1}, BackupID: [16]byte{2}, AppVersion: "capture-test", Limits: library.ContentSnapshotLimits{Database: state.LocalSnapshotLimits{MaxDBBytes: 8 << 20, MaxInventoryBytes: 4 << 20, MaxStagingBytes: 64 << 20, MaxRecords: 100000, SQLiteHeapBytes: 32 << 20, WorkerAddressSpaceBytes: 16 << 30, WorkerCPUSeconds: 10, MaxWALBytes: 64 << 20, MaxSHMBytes: 1 << 20, OperationTimeout: 10 * time.Second, DrainTimeout: time.Second}, MaxArchives: 1000, MaxInventoryBytes: 8 << 20, MaxContentBytes: 128 << 20}}
}

func TestCaptureSnapshotOfflineRecovery(test *testing.T) {
	service, store, cache, directory, archive := captureFixture(test)
	engine, key, _ := sealFixture(test)
	captured, err := engine.CaptureSnapshot(test.Context(), service, captureOptions())
	if err != nil {
		test.Fatal("production capture refused", err)
	}
	defer captured.Close()
	metadata, err := captured.Metadata(test.Context())
	if err != nil || metadata.Archives != 1 || metadata.Inventory.Owners != 2 || metadata.Inventory.Records != 9 || metadata.Database.Extents != 1 || metadata.Inventory.Extents != 1 || len(metadata.SchemaSHA256) != 64 || len(metadata.CutID) != 32 || len(metadata.ExternalConfigNames) != 0 {
		test.Fatal("capture metadata omitted the same-cut closure", err)
	}
	if err = service.Close(); err != nil {
		test.Fatal(err)
	}
	if err = cache.Close(); err != nil {
		test.Fatal(err)
	}
	if err = store.Close(); err != nil {
		test.Fatal(err)
	}
	if err = os.Rename(directory, directory+"-offline"); err != nil {
		test.Fatal(err)
	}
	defer os.Rename(directory+"-offline", directory)
	if _, err := os.Stat(filepath.Join(directory, "server.db")); !os.IsNotExist(err) {
		test.Fatal("original source is still available")
	}
	transferred := test.TempDir()
	if err := os.Chmod(transferred, 0700); err != nil {
		test.Fatal(err)
	}
	var pins []ObjectRef
	var copies []*os.File
	err = captured.WithFiles(test.Context(), func(ctx context.Context, ref ObjectRef, input *os.File) error {
		pins = append(pins, ref)
		path := filepath.Join(transferred, fmt.Sprint(len(pins)))
		output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		if err := errors.Join(copyErr, output.Sync(), output.Close()); err != nil {
			return err
		}
		copy, err := os.Open(path)
		if err != nil {
			return err
		}
		copies = append(copies, copy)
		test.Cleanup(func() { _ = copy.Close() })
		sourceStagePrivate(test, engine, []byte("capture-master-key-secret-sentinel"), bytes.Repeat([]byte{91}, 32), bytes.Repeat([]byte{13}, 32))
		return ctx.Err()
	})
	if err != nil {
		test.Fatal(err)
	}
	if len(pins) != 7 {
		test.Fatal("shared archive was duplicated or required artifacts were omitted", len(pins))
	}
	for index, kind := range []ObjectKind{Bootstrap, DataExtent, DataExtent, DataExtent, ManifestPage, ManifestPage, Complete} {
		if pins[index].read().kind != kind {
			test.Fatal("artifact publication order differs", index)
		}
	}
	if pins[4].read().ordinal != 1 || pins[5].read().ordinal != 0 {
		test.Fatal("manifest pages were not followed by the root")
	}
	if err = captured.Close(); err != nil {
		test.Fatal(err)
	}
	if err = engine.Close(); err != nil {
		test.Fatal(err)
	}
	parent, err := os.Open(transferred)
	if err != nil {
		test.Fatal(err)
	}
	defer parent.Close()
	openEngine, err := NewSealEngine(parent, key, SealLimits{TotalPlainBytes: 1 << 30, TotalCipherBytes: 1 << 30, StagingBytes: 2 << 30, InputStagingBytes: 128 << 20, MaxObjects: 100000, MaxInventoryRecords: 100000, OperationTimeout: time.Minute})
	if err != nil {
		test.Fatal(err)
	}
	defer openEngine.Close()
	session, err := openEngine.BeginOpen(test.Context(), copies[0], copies[len(copies)-1], Attempt{read: func() attemptData { return pins[0].read().attempt }})
	if err != nil {
		test.Fatal(err)
	}
	defer session.Close()
	for _, index := range []int{5, 4, 1, 2, 3} {
		if err := session.AcceptFile(test.Context(), copies[index]); err != nil {
			test.Fatal("offline authenticated open failed", err)
		}
	}
	bundle, err := session.Finish(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer bundle.Close()
	var database, inventory, restoredArchive bytes.Buffer
	for _, file := range []struct {
		kind, id string
		output   *bytes.Buffer
		digest   string
	}{
		{kind: "database", id: "database", output: &database, digest: metadata.Database.SHA256},
		{kind: "inventory", id: "inventory", output: &inventory, digest: metadata.Inventory.SHA256},
		{kind: "archive", id: archive.Hash, output: &restoredArchive, digest: archive.Hash},
	} {
		if _, err := bundle.CopyFile(test.Context(), file.kind, file.id, file.output); err != nil {
			test.Fatal(err)
		}
		digest := sha256.Sum256(file.output.Bytes())
		if hex.EncodeToString(digest[:]) != file.digest {
			test.Fatal("offline restored file hash differs", file.kind)
		}
	}
	if int64(database.Len()) != metadata.Database.Bytes || int64(inventory.Len()) != metadata.Inventory.Bytes || !bytes.Equal(restoredArchive.Bytes(), archive.Data) || len(bytes.Split(bytes.TrimSpace(inventory.Bytes()), []byte{'\n'})) != int(metadata.Inventory.Records) || !strings.Contains(inventory.String(), archive.Hash) {
		test.Fatal("restored bytes or inventory closure differ")
	}
	path := filepath.Join(transferred, "restored.db")
	if err := os.WriteFile(path, database.Bytes(), 0600); err != nil {
		test.Fatal(err)
	}
	pool, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}).String())
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	for table, expected := range map[string]int{"users": 2, "collected_repositories": 2, "collected_skills": 2, "packs": 2, "pack_items": 2, "category_skills": 1} {
		var count int
		if err := pool.QueryRowContext(test.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != expected {
			test.Fatal("offline database lost managed content", table, err)
		}
	}
}

func assertCaptureEmpty(test *testing.T, engine SealEngine) {
	test.Helper()
	entries, err := os.ReadDir(engine.get().directory.root.Name())
	if err != nil || len(entries) != 0 {
		test.Fatal("failed capture left publishable artifacts or catalog", err)
	}
	if engine.get().attempt != nil && !engine.get().attempt.closed {
		test.Fatal("failed capture left a live attempt")
	}
}

func TestCaptureSnapshotRejectsBrokenClosure(test *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "permissions", "path", "hash", "count", "inventory-budget", "content-budget", "seal-budget"} {
		test.Run(mode, func(test *testing.T) {
			service, _, _, directory, archive := captureFixture(test)
			engine, _, _ := sealFixture(test)
			options := captureOptions()
			path := filepath.Join(directory, "archives", archive.Hash+".tar.gz")
			switch mode {
			case "missing":
				if err := os.Remove(path); err != nil {
					test.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("corrupt-secret-sentinel"), 0600); err != nil {
					test.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					test.Fatal(err)
				}
			case "path", "hash", "count":
				pool, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: filepath.Join(directory, "server.db")}).String())
				if err != nil {
					test.Fatal(err)
				}
				statement := map[string]string{"path": "UPDATE collected_skills SET path = 'wrong/private/path'", "hash": "UPDATE collected_skills SET content_hash = '" + strings.Repeat("0", 64) + "'", "count": "UPDATE collected_repositories SET skill_count = skill_count + 1"}[mode]
				_, executeError := pool.ExecContext(test.Context(), statement)
				if err := errors.Join(executeError, pool.Close()); err != nil {
					test.Fatal(err)
				}
			case "inventory-budget":
				options.Limits.MaxInventoryBytes = 1
			case "content-budget":
				options.Limits.MaxContentBytes = 1
			case "seal-budget":
				engine.get().limits.InputStagingBytes = 1
			}
			result, err := engine.CaptureSnapshot(test.Context(), service, options)
			if err == nil || result.get != nil || strings.Contains(err.Error(), "secret-sentinel") || strings.Contains(err.Error(), directory) {
				test.Fatal("broken closure produced a bundle or leaked its source", err)
			}
			if mode == "seal-budget" && !errors.Is(err, ErrSealBudget) {
				test.Fatal("library sanitization lost the callback's backup error", err)
			}
			assertCaptureEmpty(test, engine)
			if engine.get().plain != 0 || engine.get().cipher != 0 || engine.get().objects != 0 {
				test.Fatal("failed capture retained certain artifact charges")
			}
		})
	}
}

func TestCaptureSnapshotOuterFailureBarrier(test *testing.T) {
	for _, mode := range []string{"late-source-change", "late-cancel", "extent-close", "page-close", "root-close", "complete-close", "catalog-close"} {
		test.Run(mode, func(test *testing.T) {
			service, _, _, directory, archive := captureFixture(test)
			engine, _, _ := sealFixture(test)
			state := engine.get()
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			originalSync, originalClose := state.ops.sync, state.ops.close
			defer func() { state.ops.sync, state.ops.close = originalSync, originalClose }()
			metadataWrites := 0
			tampered := false
			state.ops.sync = func(file *os.File) error {
				if strings.Contains(filepath.Base(file.Name()), "-2-") || strings.Contains(filepath.Base(file.Name()), "-3-") || strings.Contains(filepath.Base(file.Name()), "-4-") {
					metadataWrites++
				}
				if !tampered && strings.HasSuffix(file.Name(), "-1-00000002") {
					tampered = true
					if mode == "late-source-change" {
						if err := os.Chmod(filepath.Join(directory, "archives", archive.Hash+".tar.gz"), 0644); err != nil {
							test.Error(err)
						}
					} else if mode == "late-cancel" {
						cancel()
					}
				}
				return originalSync(file)
			}
			state.ops.close = func(file *os.File) error {
				err := originalClose(file)
				match := map[string]string{"extent-close": "-1-00000000", "page-close": "-2-00000001", "root-close": "-3-00000000", "complete-close": "-4-00000000"}[mode]
				if match != "" && strings.HasSuffix(file.Name(), match) || mode == "catalog-close" && strings.HasPrefix(filepath.Base(file.Name()), "i-") {
					return errors.New("private-physical-close-sentinel")
				}
				return err
			}
			result, err := engine.CaptureSnapshot(ctx, service, captureOptions())
			if err == nil || result.get != nil || strings.Contains(err.Error(), "private-physical-close-sentinel") {
				test.Fatal("failed capture exposed a usable bundle or private error", err)
			}
			if strings.HasSuffix(mode, "close") && !errors.Is(err, ErrSealUncertain) {
				test.Fatal("callback/cleanup physical uncertainty was downgraded", err)
			}
			if mode == "late-cancel" && !errors.Is(err, context.Canceled) {
				test.Fatal("late cancellation was lost", err)
			}
			if mode == "late-source-change" || mode == "late-cancel" || mode == "extent-close" {
				if metadataWrites != 0 {
					test.Fatal("manifest or complete written before successful source scope closure")
				}
			}
			assertCaptureEmpty(test, engine)
		})
	}
}

func TestCaptureErrorUncertaintyPrecedence(test *testing.T) {
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	for _, critical := range []error{ErrSealUncertain, ErrSealCleanup, library.ErrSnapshotArchiveUncertain, state.ErrLocalSnapshotUncertain, state.ErrLocalSnapshotCleanup} {
		for _, failures := range [][]error{{errors.New("private-source-error"), critical}, {critical, state.ErrLocalSnapshotInvalid}, {ErrSealBudget, fmt.Errorf("private-source-error: %w", critical)}} {
			err := captureError(ctx, failures...)
			if !errors.Is(err, critical) || strings.Contains(err.Error(), "private-source-error") {
				test.Fatal("cleanup uncertainty was downgraded or leaked data", err)
			}
		}
	}
	combined := captureError(ctx, ErrSealUncertain, library.ErrSnapshotArchiveUncertain, state.ErrLocalSnapshotCleanup)
	for _, known := range []error{ErrSealUncertain, library.ErrSnapshotArchiveUncertain, state.ErrLocalSnapshotCleanup} {
		if !errors.Is(combined, known) {
			test.Fatal("combined cleanup failure lost its category")
		}
	}
}

func TestCapturedSnapshotCloseDrainsReaders(test *testing.T) {
	service, _, _, _, _ := captureFixture(test)
	engine, _, _ := sealFixture(test)
	captured, err := engine.CaptureSnapshot(test.Context(), service, captureOptions())
	if err != nil {
		test.Fatal(err)
	}
	defer captured.Close()
	entered, canceled := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	readResult, closeResult := make(chan error, 1), make(chan error, 1)
	visited := 0
	go func() {
		readResult <- captured.WithFiles(test.Context(), func(ctx context.Context, ref ObjectRef, input *os.File) error {
			visited++
			if ref.read().kind != Bootstrap {
				test.Error("reader continued to publish later objects during close")
			}
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		test.Fatal("file consumer did not enter")
	}
	go func() { closeResult <- captured.Close() }()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		test.Fatal("snapshot close did not cancel the consumer")
	}
	select {
	case <-closeResult:
		test.Fatal("snapshot close returned before consumer drainage")
	default:
	}
	unblock.Do(func() { close(release) })
	select {
	case err := <-readResult:
		if !errors.Is(err, context.Canceled) || visited != 1 {
			test.Fatal("closed reader succeeded or exposed later objects", err)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("file consumer did not drain")
	}
	select {
	case err := <-closeResult:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("snapshot close did not finish")
	}
	if _, err := captured.Metadata(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("closed snapshot returned metadata")
	}
	if err := captured.WithFiles(test.Context(), func(context.Context, ObjectRef, *os.File) error { return nil }); !errors.Is(err, ErrClosed) {
		test.Fatal("closed snapshot returned files")
	}
	if err := captured.Close(); err != nil {
		test.Fatal("snapshot close was not idempotent", err)
	}
	assertCaptureEmpty(test, engine)
}

func TestCapturedSnapshotRedactionAndCancellation(test *testing.T) {
	service, _, _, _, _ := captureFixture(test)
	engine, _, _ := sealFixture(test)
	captured, err := engine.CaptureSnapshot(test.Context(), service, captureOptions())
	if err != nil {
		test.Fatal(err)
	}
	defer captured.Close()
	formatted := fmt.Sprintf("%v %+v %#v %s", captured, captured, captured, captured)
	encoded, err := json.Marshal(captured)
	if err != nil || string(encoded) != "{}" || strings.Count(formatted, "[private captured snapshot]") != 4 {
		test.Fatal("captured snapshot default representation is not opaque", err)
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("snapshot", "capture", captured)
	if !strings.Contains(logs.String(), "[private captured snapshot]") || strings.Contains(logs.String(), engine.get().directory.root.Name()) {
		test.Fatal("captured snapshot logs expose private state")
	}
	visited := 0
	err = captured.WithFiles(test.Context(), func(context.Context, ObjectRef, *os.File) error {
		visited++
		return errors.New("consumer-private-secret-sentinel")
	})
	if !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "private-secret-sentinel") || visited != 1 {
		test.Fatal("consumer failure leaked or published subsequent files", err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	err = captured.WithFiles(ctx, func(ctx context.Context, ref ObjectRef, input *os.File) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		test.Fatal("consumer cancellation was not preserved", err)
	}
	if _, err := captured.Metadata(test.Context()); err != nil {
		test.Fatal("canceled transfer destroyed the reusable ciphertext handle", err)
	}
	if err := captured.WithFiles(nil, func(context.Context, ObjectRef, *os.File) error { return nil }); !errors.Is(err, ErrInvalid) {
		test.Fatal("nil transfer context admitted")
	}
	if err := captured.WithFiles(test.Context(), nil); !errors.Is(err, ErrInvalid) {
		test.Fatal("nil transfer callback admitted")
	}
}

func TestCaptureSnapshotPhysicalOuterCleanupFailures(test *testing.T) {
	for _, mode := range []string{"b2-cleanup", "source-close", "b2-and-cipher-cleanup"} {
		test.Run(mode, func(test *testing.T) {
			service, _, _, directory, archive := captureFixture(test)
			engine, _, _ := sealFixture(test)
			physical := engine.get()
			originalSync, originalClose := physical.ops.sync, physical.ops.close
			defer func() { physical.ops.sync, physical.ops.close = originalSync, originalClose }()
			injected, metadataWrites, archiveCloses, cipherCleanupFailures := false, 0, 0, 0
			physical.ops.sync = func(file *os.File) error {
				name := filepath.Base(file.Name())
				if strings.Contains(name, "-2-") || strings.Contains(name, "-3-") || strings.Contains(name, "-4-") {
					metadataWrites++
				}
				if mode != "source-close" && !injected && strings.HasSuffix(name, "-1-00000002") {
					entries, err := os.ReadDir(directory)
					if err != nil {
						return err
					}
					for _, entry := range entries {
						if entry.IsDir() && strings.HasPrefix(entry.Name(), ".snapshot-") {
							if err := os.WriteFile(filepath.Join(directory, entry.Name(), "unexpected-cleanup-entry"), []byte("fault"), 0600); err != nil {
								return err
							}
							injected = true
						}
					}
				}
				if mode == "b2-and-cipher-cleanup" && file == physical.directory.root && physical.attempt.dataCount == 3 && physical.attempt.active == 0 {
					cipherCleanupFailures++
					return errors.New("private-cipher-cleanup-fault")
				}
				return originalSync(file)
			}
			physical.ops.close = func(file *os.File) error {
				err := originalClose(file)
				if mode == "source-close" && strings.HasSuffix(file.Name(), "-1-00000002") {
					archiveCloses++
					if archiveCloses == 2 {
						target := filepath.Join(directory, "archives", archive.Hash+".tar.gz")
						entries, readErr := os.ReadDir("/proc/self/fd")
						if readErr != nil {
							test.Error(readErr)
						}
						for _, entry := range entries {
							name, linkErr := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
							if linkErr != nil || name != target {
								continue
							}
							descriptor, convertErr := strconv.ParseUint(entry.Name(), 10, 64)
							if convertErr != nil {
								test.Error(convertErr)
								continue
							}
							if injected {
								test.Error("more than one live archive descriptor")
								continue
							}
							borrowed := os.NewFile(uintptr(descriptor), target)
							if closeErr := borrowed.Close(); closeErr != nil {
								test.Error(closeErr)
							}
							injected = true
						}
					}
				}
				return err
			}
			result, err := engine.CaptureSnapshot(test.Context(), service, captureOptions())
			if !injected || result.get != nil || err == nil || metadataWrites != 0 {
				test.Fatal("physical outer cleanup fault was missed or completion escaped", err)
			}
			if mode == "source-close" {
				if !errors.Is(err, library.ErrSnapshotArchiveUncertain) {
					test.Fatal("original source close uncertainty class was lost", err)
				}
			} else if !errors.Is(err, state.ErrLocalSnapshotCleanup) {
				test.Fatal("outer B2 cleanup failure was downgraded", err)
			}
			if mode == "b2-and-cipher-cleanup" && (cipherCleanupFailures == 0 || !errors.Is(err, ErrSealCleanup) || !physical.uncertain || physical.disk <= physical.limits.InputStagingBytes) {
				test.Fatal("cipher cleanup failure or retained charge was lost", err)
			}
			if strings.Contains(err.Error(), "private-cipher-cleanup-fault") {
				test.Fatal("physical cleanup failure leaked its raw cause")
			}
			assertCaptureEmpty(test, engine)
		})
	}
}

func TestCaptureChunkWriter64MiBPlusOne(test *testing.T) {
	engine, attempt, _ := sourceAttempt(test)
	builder := &captureBuilder{engine: engine, attempt: attempt}
	defer func() {
		for _, file := range builder.files {
			_ = file.Close()
		}
	}()
	const length = int64(sealExtentLimit + 1)
	chunk := bytes.Repeat([]byte{42}, 32768)
	digest := sha256.New()
	for remaining := length; remaining != 0; remaining -= min(remaining, int64(len(chunk))) {
		_, _ = digest.Write(chunk[:min(remaining, int64(len(chunk)))])
	}
	expected := [32]byte(digest.Sum(nil))
	var escaped *captureChunkWriter
	var buffer []byte
	extents, err := builder.copyFile(test.Context(), "database", "database", length, expected, func(_ context.Context, output io.Writer) (int64, error) {
		escaped = output.(*captureChunkWriter)
		buffer = escaped.buffer
		for remaining := length; remaining != 0; remaining -= min(remaining, int64(len(chunk))) {
			if _, err := output.Write(chunk[:min(remaining, int64(len(chunk)))]); err != nil {
				return 0, err
			}
		}
		return length, nil
	})
	if err != nil || extents != 2 || len(builder.files) != 2 || len(builder.records) != 2 || cap(buffer) != sealExtentLimit {
		test.Fatal("large logical file was refused or not split into two ciphertext extents", err)
	}
	if bytes.Count(buffer, []byte{0}) != len(buffer) || escaped.buffer != nil || escaped.digest != nil {
		test.Fatal("reusable plaintext buffer was not cleared and released")
	}
	if count, err := escaped.Write(chunk); count != 0 || !errors.Is(err, ErrClosed) {
		test.Fatal("chunk writer outlived its copy callback")
	}
	var order sealOrder
	whole := sha256.New()
	for index, row := range builder.records {
		record, err := decodeExtentRecord(row)
		if err != nil || order.accept(record) != nil || row.Ordinal != uint32(index) || row.Offset != int64(index)*sealExtentLimit || row.PlaintextBytes != []int64{sealExtentLimit, 1}[index] {
			test.Fatal("chunk records violate extent size, offset or order", err)
		}
		if err := builder.files[index].WithFile(test.Context(), func(input *os.File) error {
			var encoded [sealHeaderSize]byte
			if _, err := io.ReadFull(input, encoded[:]); err != nil {
				return err
			}
			header, err := parseSealHeader(encoded[:])
			if err != nil {
				return err
			}
			actual, err := sealDecrypt(test.Context(), header, attempt.get().dek[:], input, whole)
			if err != nil || actual != record.plainHash {
				return ErrInvalid
			}
			return nil
		}); err != nil {
			test.Fatal("large-file extent ciphertext did not authenticate", err)
		}
	}
	if [32]byte(whole.Sum(nil)) != expected {
		test.Fatal("two authenticated extents did not reconstruct the logical file hash")
	}
}

func TestCaptureChunkWriterValidatesWholeFile(test *testing.T) {
	for _, mode := range []string{"short", "long", "hash", "receipt", "producer", "ignored-write"} {
		test.Run(mode, func(test *testing.T) {
			engine, attempt, _ := sourceAttempt(test)
			builder := &captureBuilder{engine: engine, attempt: attempt}
			defer func() {
				for _, file := range builder.files {
					_ = file.Close()
				}
			}()
			payload := []byte("whole-file-secret-sentinel")
			extents, err := builder.copyFile(test.Context(), "database", "database", int64(len(payload)), sha256.Sum256(payload), func(_ context.Context, output io.Writer) (int64, error) {
				switch mode {
				case "short":
					_, _ = output.Write(payload[:len(payload)-1])
				case "long":
					_, _ = output.Write(append(bytes.Clone(payload), 1))
				case "hash":
					_, _ = output.Write(bytes.Repeat([]byte{42}, len(payload)))
				case "receipt":
					_, _ = output.Write(payload)
					return 0, nil
				case "producer":
					_, _ = output.Write(payload)
					return int64(len(payload)), errors.New(string(payload))
				case "ignored-write":
					_, _ = output.Write(payload)
					_, _ = output.Write([]byte{1})
				}
				return int64(len(payload)), nil
			})
			if extents != 0 || !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "secret-sentinel") {
				test.Fatal("whole-file length/hash/receipt failure was ignored", err)
			}
		})
	}
}
