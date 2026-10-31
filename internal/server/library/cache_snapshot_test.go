package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/spf13/afero"
)

func TestMain(tests *testing.M) {
	if handled, code := state.SnapshotWorkerDispatch(os.Args); handled {
		os.Exit(code)
	}
	os.Exit(tests.Run())
}

func contentSnapshotLimits() state.LocalSnapshotLimits {
	return state.LocalSnapshotLimits{MaxDBBytes: 8 << 20, MaxInventoryBytes: 4 << 20, MaxStagingBytes: 64 << 20, MaxRecords: 100000, SQLiteHeapBytes: 32 << 20, WorkerAddressSpaceBytes: 16 << 30, WorkerCPUSeconds: 10, MaxWALBytes: 64 << 20, MaxSHMBytes: 1 << 20, OperationTimeout: 10 * time.Second, DrainTimeout: time.Second}
}

func TestSnapshotArchiveSourceActualCut(test *testing.T) {
	store, cache, _, _ := managedCacheFixture(test)
	data := fixtureArchive(test)
	hash := archiveHash(data)
	if err := cache.Put(test.Context(), hash, data); err != nil {
		test.Fatal(err)
	}
	var escaped SnapshotArchiveSource
	err := cache.WithSnapshotSource(test.Context(), store, func(ctx context.Context, source SnapshotArchiveSource) error {
		escaped = source
		if _, err := source.CopyVerified(ctx, hash, io.Discard); !errors.Is(err, maintenance.ErrLease) {
			test.Fatal("non-cut context accepted")
		}
		_, err := store.WithLocalSnapshot(ctx, contentSnapshotLimits(), func(ctx context.Context, view state.LocalSnapshotView) error {
			var output bytes.Buffer
			copied, err := source.CopyVerified(ctx, hash, &output)
			if err != nil || copied.Bytes() != int64(len(data)) || copied.SHA256() != sha256.Sum256(data) || !bytes.Equal(output.Bytes(), data) {
				test.Fatal("same-cut archive copy unavailable", err)
			}
			return nil
		})
		return err
	})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := escaped.CopyVerified(test.Context(), hash, io.Discard); !errors.Is(err, maintenance.ErrLease) {
		test.Fatal("expired source accepted")
	}
}

func TestSnapshotArchiveSourceRejectsUnsafeFile(test *testing.T) {
	for _, fault := range []string{"missing", "hash", "permissions", "hardlink", "symlink", "short write", "close"} {
		test.Run(fault, func(test *testing.T) {
			store, cache, _, directory := managedCacheFixture(test)
			data := fixtureArchive(test)
			hash := archiveHash(data)
			if err := cache.Put(test.Context(), hash, data); err != nil {
				test.Fatal(err)
			}
			path := filepath.Join(directory, "archives", hash+".tar.gz")
			switch fault {
			case "missing":
				if err := os.Remove(path); err != nil {
					test.Fatal(err)
				}
			case "hash":
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(data)), 0600); err != nil {
					test.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					test.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(directory, "alias")); err != nil {
					test.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, filepath.Join(directory, "original")); err != nil {
					test.Fatal(err)
				}
				if err := os.Symlink("../original", path); err != nil {
					test.Fatal(err)
				}
			case "close":
				cache.filesystem = snapshotCloseFS{Fs: cache.filesystem}
			}
			err := cache.WithSnapshotSource(test.Context(), store, func(ctx context.Context, source SnapshotArchiveSource) error {
				_, err := store.WithLocalSnapshot(ctx, contentSnapshotLimits(), func(ctx context.Context, _ state.LocalSnapshotView) error {
					var destination io.Writer = io.Discard
					if fault == "short write" {
						destination = snapshotShortWriter{}
					}
					if copied, err := source.CopyVerified(ctx, hash, destination); err == nil || copied.Bytes() != 0 {
						test.Fatal("unsafe copy returned a receipt")
					}
					return nil
				})
				return err
			})
			if err == nil {
				test.Fatal("ignored archive copy failure became successful scope")
			}
			if fault == "close" && !errors.Is(err, ErrSnapshotArchiveUncertain) {
				test.Fatal("close uncertainty lost")
			}
		})
	}
}

type snapshotShortWriter struct{}

func (snapshotShortWriter) Write(data []byte) (int, error) { return max(0, len(data)-1), nil }

type snapshotCloseFS struct{ afero.Fs }

func (filesystem snapshotCloseFS) Open(name string) (afero.File, error) {
	file, err := filesystem.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return snapshotCloseFile{File: file}, nil
}

type snapshotCloseFile struct{ afero.File }

func (file snapshotCloseFile) Close() error {
	_ = file.File.Close()
	return errors.New("synthetic private close fault")
}

func TestSnapshotArchiveSourceCloseAndWriterBarrier(test *testing.T) {
	store, cache, _, _ := managedCacheFixture(test)
	data := fixtureArchive(test)
	hash := archiveHash(data)
	if err := cache.Put(test.Context(), hash, data); err != nil {
		test.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	defer resume()
	finished := make(chan error, 1)
	go func() {
		finished <- cache.WithSnapshotSource(test.Context(), store, func(ctx context.Context, source SnapshotArchiveSource) error {
			_, err := store.WithLocalSnapshot(ctx, contentSnapshotLimits(), func(ctx context.Context, _ state.LocalSnapshotView) error {
				_, err := source.CopyVerified(ctx, hash, snapshotBarrierWriter{entered: entered, release: release})
				return err
			})
			return err
		})
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		test.Fatal("copy did not reach live cut")
	}
	if err := store.CreateUser(test.Context(), "must-not-enter"); !errors.Is(err, maintenance.ErrQuiescing) {
		test.Fatal("writer passed snapshot cut", err)
	}
	closing := make(chan error, 1)
	go func() { closing <- cache.Close() }()
	deadline := time.Now().Add(time.Second)
	for {
		cache.maintenance.mutex.Lock()
		started := cache.maintenance.closing
		cache.maintenance.mutex.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("cache close did not begin")
		}
		runtime.Gosched()
	}
	select {
	case err := <-closing:
		test.Fatal("root closed during snapshot source", err)
	default:
	}
	resume()
	if err := <-finished; err != nil {
		test.Fatal("admitted source did not complete during close", err)
	}
	if err := <-closing; err != nil {
		test.Fatal(err)
	}
}

type snapshotBarrierWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (writer snapshotBarrierWriter) Write(data []byte) (int, error) {
	close(writer.entered)
	<-writer.release
	return len(data), nil
}

func TestSnapshotArchiveSourceExactStoreAndExpiredCut(test *testing.T) {
	store, cache, _, directory := managedCacheFixture(test)
	other, err := state.OpenWithOptions(test.Context(), filepath.Join(directory, "another.db"), make([]byte, 32), state.Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	defer other.Close()
	data := fixtureArchive(test)
	hash := archiveHash(data)
	if err := cache.Put(test.Context(), hash, data); err != nil {
		test.Fatal(err)
	}
	err = cache.WithSnapshotSource(test.Context(), store, func(ctx context.Context, source SnapshotArchiveSource) error {
		_, err := other.WithLocalSnapshot(ctx, contentSnapshotLimits(), func(ctx context.Context, _ state.LocalSnapshotView) error {
			if _, err := source.CopyVerified(ctx, hash, io.Discard); !errors.Is(err, maintenance.ErrLease) {
				test.Fatal("same gate but different database authorized source", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
		var expired context.Context
		_, err = store.WithLocalSnapshot(ctx, contentSnapshotLimits(), func(ctx context.Context, _ state.LocalSnapshotView) error {
			expired = context.WithoutCancel(ctx)
			return nil
		})
		if err != nil {
			return err
		}
		if _, err := source.CopyVerified(expired, hash, io.Discard); !errors.Is(err, maintenance.ErrLease) {
			test.Fatal("expired cut accepted with cancellation stripped", err)
		}
		return nil
	})
	if err != nil {
		test.Fatal(err)
	}
	for _, invalid := range []*state.Store{nil, {}} {
		if err := cache.WithSnapshotSource(test.Context(), invalid, func(context.Context, SnapshotArchiveSource) error { return nil }); !errors.Is(err, ErrSnapshotArchiveInvalid) {
			test.Fatal("invalid store accepted")
		}
	}
}

func TestSnapshotArchiveCleanupPrecedence(test *testing.T) {
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	for _, failure := range []error{state.ErrLocalSnapshotUncertain, ErrSnapshotArchiveUncertain, state.ErrLocalSnapshotCleanup} {
		if !errors.Is(snapshotArchiveError(ctx, failure), failure) {
			test.Fatal("cancellation hid cleanup failure")
		}
		if !errors.Is(snapshotArchiveMerge(failure, ErrSnapshotArchiveInvalid), failure) || !errors.Is(snapshotArchiveMerge(ErrSnapshotArchiveInvalid, failure), failure) {
			test.Fatal("ordinary source failure hid cleanup failure")
		}
	}
}

func TestSnapshotArchiveEscapedCopyPinsExactCut(test *testing.T) {
	store, cache, _, _ := managedCacheFixture(test)
	data := fixtureArchive(test)
	hash := archiveHash(data)
	if err := cache.Put(test.Context(), hash, data); err != nil {
		test.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	defer resume()
	cutContext := make(chan context.Context, 1)
	copyDone, allDone := make(chan error, 1), make(chan error, 1)
	go func() {
		allDone <- cache.WithSnapshotSource(test.Context(), store, func(ctx context.Context, source SnapshotArchiveSource) error {
			_, err := store.WithLocalSnapshot(ctx, contentSnapshotLimits(), func(cut context.Context, view state.LocalSnapshotView) error {
				go func() {
					copied, err := source.CopyVerified(context.WithoutCancel(cut), hash, snapshotBarrierWriter{entered: entered, release: release})
					if err == nil || copied.Bytes() != 0 {
						copyDone <- errors.New("escaped archive copy returned success")
						return
					}
					copyDone <- nil
				}()
				<-entered
				if _, err := view.Metadata(cut); !errors.Is(err, state.ErrLocalSnapshotBusy) {
					return errors.New("archive reader is not registered with B2")
				}
				cutContext <- cut
				return nil
			})
			return err
		})
	}()
	var cut context.Context
	select {
	case cut = <-cutContext:
	case err := <-allDone:
		test.Fatal("cut registration refused", err)
	case <-time.After(10 * time.Second):
		test.Fatal("cut registration timed out")
	}
	select {
	case <-cut.Done():
	case <-time.After(time.Second):
		test.Fatal("callback did not leave")
	}
	if err := store.CreateUser(test.Context(), "cannot-pass-escaped-copy"); !errors.Is(err, maintenance.ErrQuiescing) {
		test.Fatal("cut released before child close", err)
	}
	select {
	case err := <-allDone:
		test.Fatal("source returned before child drain", err)
	default:
	}
	resume()
	if err := <-copyDone; err != nil {
		test.Fatal(err)
	}
	if err := <-allDone; err == nil {
		test.Fatal("escaped reader made cut successful")
	}
}

func TestSnapshotArchiveCloseTailRechecksOriginalLife(test *testing.T) {
	store, cache, _, _ := managedCacheFixture(test)
	data := fixtureArchive(test)
	hash := archiveHash(data)
	if err := cache.Put(test.Context(), hash, data); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	cache.filesystem = snapshotCancelCloseFS{Fs: cache.filesystem, cancel: cancel}
	err := cache.WithSnapshotSource(ctx, store, func(ctx context.Context, source SnapshotArchiveSource) error {
		_, err := store.WithLocalSnapshot(ctx, contentSnapshotLimits(), func(cut context.Context, _ state.LocalSnapshotView) error {
			copied, err := source.CopyVerified(context.WithoutCancel(cut), hash, io.Discard)
			if err == nil || copied.Bytes() != 0 {
				test.Fatal("late close cancellation returned successful receipt")
			}
			return err
		})
		return err
	})
	if !errors.Is(err, context.Canceled) {
		test.Fatal("original cancellation lost", err)
	}
}

type snapshotCancelCloseFS struct {
	afero.Fs
	cancel context.CancelFunc
}

func (filesystem snapshotCancelCloseFS) Open(name string) (afero.File, error) {
	file, err := filesystem.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return snapshotCancelCloseFile{File: file, cancel: filesystem.cancel}, nil
}

type snapshotCancelCloseFile struct {
	afero.File
	cancel context.CancelFunc
}

func (file snapshotCancelCloseFile) Close() error {
	err := file.File.Close()
	file.cancel()
	return err
}
