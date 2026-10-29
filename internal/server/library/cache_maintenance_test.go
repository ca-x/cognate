package library

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/spf13/afero"
)

func cacheProcessProbe(test *testing.T, directory, mode string) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestManagedCacheProcess$")
	command.Env = append(os.Environ(), "COGNATE_CACHE_PROBE_DIR="+directory, "COGNATE_CACHE_PROBE_MODE="+mode)
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("cache process probe %s failed: %v\n%s", mode, err, output)
	}
}

func TestManagedCacheProcess(test *testing.T) {
	directory := os.Getenv("COGNATE_CACHE_PROBE_DIR")
	if directory == "" {
		test.Skip("subprocess helper")
	}
	mode := os.Getenv("COGNATE_CACHE_PROBE_MODE")
	ctx, cancel := context.WithTimeout(test.Context(), 350*time.Millisecond)
	defer cancel()
	if mode == "default" {
		cache, err := OpenArchiveCache(filepath.Join(directory, "archives"))
		if err != nil {
			test.Fatal(err)
		}
		defer cache.Close()
		data := fixtureArchive(test)
		if err := cache.Put(ctx, archiveHash(data), data); err != nil {
			test.Fatal(err)
		}
		return
	}
	instance, err := maintenance.Join(ctx, directory)
	if mode == "blocked" {
		if instance != nil {
			_ = instance.Close()
		}
		if instance != nil || !errors.Is(err, maintenance.ErrLock) {
			test.Fatal("cooperating process acquired a live cache owner's root")
		}
		return
	}
	if err != nil {
		test.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		test.Fatal(err)
	}
}

type cacheBarrierFS struct {
	afero.Fs
	phase   string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (filesystem *cacheBarrierFS) pause() {
	filesystem.once.Do(func() {
		close(filesystem.entered)
		<-filesystem.release
	})
}

func (filesystem *cacheBarrierFS) OpenFile(name string, flags int, mode os.FileMode) (afero.File, error) {
	file, err := filesystem.Fs.OpenFile(name, flags, mode)
	if err == nil && filesystem.phase == "write" && strings.HasPrefix(name, ".stage-") {
		return &cacheBarrierFile{File: file, filesystem: filesystem}, nil
	}
	return file, err
}

func (filesystem *cacheBarrierFS) Remove(name string) error {
	err := filesystem.Fs.Remove(name)
	if filesystem.phase == "cleanup" && strings.HasPrefix(name, ".stage-") {
		filesystem.pause()
	}
	return err
}

type cacheBarrierFile struct {
	afero.File
	filesystem *cacheBarrierFS
}

func (file *cacheBarrierFile) Write(data []byte) (int, error) {
	count, err := file.File.Write(data)
	file.filesystem.pause()
	return count, err
}

type cacheFailCloseRoot struct {
	io.Closer
	entered  chan struct{}
	release  chan struct{}
	attempts atomic.Int64
	failure  error
}

type cacheCloseTailRoot struct {
	io.Closer
	entered chan struct{}
	release chan struct{}
}

func (root *cacheCloseTailRoot) Close() error {
	err := root.Closer.Close()
	close(root.entered)
	<-root.release
	return err
}

type cacheReplacementContext struct {
	context.Context
	once    sync.Once
	replace func()
}

func (ctx *cacheReplacementContext) Deadline() (time.Time, bool) {
	ctx.once.Do(ctx.replace)
	return ctx.Context.Deadline()
}

func (root *cacheFailCloseRoot) Close() error {
	if root.attempts.Add(1) == 1 {
		close(root.entered)
	}
	<-root.release
	return root.failure
}

func releaseCacheBarrier(signal chan struct{}) {
	select {
	case <-signal:
	default:
		close(signal)
	}
}

func awaitCacheAdmissionClosed(test *testing.T, cache *ArchiveCache) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	for {
		_, release, err := cache.enterWriter(ctx)
		if errors.Is(err, maintenance.ErrQuiescing) {
			return
		}
		if err != nil {
			test.Fatal(err)
		}
		release()
		runtime.Gosched()
	}
}

func managedCacheFixture(test *testing.T) (*state.Store, *ArchiveCache, *maintenance.Instance, string) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), state.Options{MaintenanceManaged: true})
	if errors.Is(err, maintenance.ErrUnsupported) {
		test.Skip("managed instance unsupported")
	}
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	cache, err := OpenArchiveCacheWithOptions(test.Context(), filepath.Join(directory, "archives"), CacheOptions{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = cache.Close() })
	instance, err := maintenance.Join(test.Context(), directory)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = instance.Close() })
	return store, cache, instance, directory
}

func TestManagedCacheDirectPut(test *testing.T) {
	test.Run("admission", func(test *testing.T) {
		_, cache, instance, _ := managedCacheFixture(test)
		data := fixtureArchive(test)
		hash := archiveHash(data)
		exclusive, err := instance.Quiesce(test.Context())
		if err != nil {
			test.Fatal(err)
		}
		defer exclusive.Release()
		if err := cache.Put(test.Context(), hash, data); !errors.Is(err, maintenance.ErrQuiescing) {
			test.Fatal("managed Put published while maintenance owned Exclusive")
		}
		if _, err := os.Lstat(filepath.Join(cache.directory, hash+".tar.gz")); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("rejected managed Put changed the cache")
		}
		exclusive.Release()
		if err := cache.Put(test.Context(), hash, data); err != nil {
			test.Fatal(err)
		}
		stored, err := cache.Read(test.Context(), hash)
		if err != nil || !bytes.Equal(stored, data) {
			test.Fatal("admitted Put did not preserve archive bytes")
		}
	})
	test.Run("cleanup-drain", testManagedCachePutCleanup)
	test.Run("nested-independent-child", testManagedCacheNestedChild)
}

func testManagedCachePutCleanup(test *testing.T) {
	_, cache, instance, _ := managedCacheFixture(test)
	barrier := &cacheBarrierFS{Fs: cache.filesystem, phase: "cleanup", entered: make(chan struct{}), release: make(chan struct{})}
	cache.filesystem = barrier
	defer releaseCacheBarrier(barrier.release)
	data := fixtureArchive(test)
	hash := archiveHash(data)
	finished := make(chan error, 1)
	go func() { finished <- cache.Put(test.Context(), hash, data) }()
	select {
	case <-barrier.entered:
	case <-time.After(time.Second):
		test.Fatal("actual stage cleanup not reached")
	}
	stored, err := cache.Read(test.Context(), hash)
	if err != nil || !bytes.Equal(stored, data) {
		test.Fatal("fixture did not publish before cleanup")
	}
	acquired := make(chan *maintenance.Exclusive, 1)
	go func() {
		exclusive, err := instance.Quiesce(test.Context())
		if err == nil {
			acquired <- exclusive
		}
	}()
	awaitCacheAdmissionClosed(test, cache)
	select {
	case exclusive := <-acquired:
		exclusive.Release()
		test.Fatal("Put admission ended before actual cleanup completed")
	default:
	}
	close(barrier.release)
	if err := <-finished; err != nil {
		test.Fatal(err)
	}
	select {
	case exclusive := <-acquired:
		exclusive.Release()
	case <-time.After(time.Second):
		test.Fatal("Put cleanup did not drain")
	}
	entries, err := os.ReadDir(cache.directory)
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stage-") {
			test.Fatal("Put left a stage after cleanup")
		}
	}
}

func testManagedCacheNestedChild(test *testing.T) {
	_, cache, instance, _ := managedCacheFixture(test)
	parent, releaseParent, err := cache.enterWriter(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer releaseParent()
	child, releaseChild, err := cache.enterWriter(parent)
	if err != nil {
		test.Fatal(err)
	}
	defer releaseChild()
	releaseParent()
	data := fixtureArchive(test)
	if err := cache.Put(parent, archiveHash(data), data); !errors.Is(err, maintenance.ErrLease) {
		test.Fatal("stale cache parent lent admission")
	}
	foreign, _, _, _ := managedCacheFixture(test)
	_, err = foreign.WithMaintenanceWriter(test.Context(), func(ctx context.Context) (int, error) {
		return 0, cache.Put(ctx, archiveHash(data), data)
	})
	if !errors.Is(err, maintenance.ErrLease) {
		test.Fatal("foreign data root lent cache admission")
	}
	acquired := make(chan *maintenance.Exclusive, 1)
	go func() {
		exclusive, err := instance.Quiesce(test.Context())
		if err == nil {
			acquired <- exclusive
		}
	}()
	awaitCacheAdmissionClosed(test, cache)
	select {
	case exclusive := <-acquired:
		exclusive.Release()
		test.Fatal("cache borrowed the parent's count instead of retaining its child")
	default:
	}
	if err := cache.Put(child, archiveHash(data), data); err != nil {
		test.Fatal("live cache child could not publish during drain")
	}
	releaseChild()
	select {
	case exclusive := <-acquired:
		exclusive.Release()
	case <-time.After(time.Second):
		test.Fatal("cache child did not drain")
	}
}

func testManagedCacheCloseDrain(test *testing.T) {
	for _, mode := range []string{"commit", "cancel"} {
		test.Run(mode, func(test *testing.T) {
			store, cache, instance, directory := managedCacheFixture(test)
			if err := store.Close(); err != nil {
				test.Fatal(err)
			}
			if err := instance.Close(); err != nil {
				test.Fatal(err)
			}
			barrier := &cacheBarrierFS{Fs: cache.filesystem, phase: "write", entered: make(chan struct{}), release: make(chan struct{})}
			cache.filesystem = barrier
			defer releaseCacheBarrier(barrier.release)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			data := fixtureArchive(test)
			hash := archiveHash(data)
			finished := make(chan error, 1)
			go func() { finished <- cache.Put(ctx, hash, data) }()
			select {
			case <-barrier.entered:
			case <-time.After(time.Second):
				test.Fatal("real stage write not reached")
			}
			managed := cache.maintenance
			managed.mutex.Lock()
			changed := managed.changed
			managed.mutex.Unlock()
			closed := make(chan error, 1)
			go func() { closed <- cache.Close() }()
			select {
			case <-changed:
			case <-time.After(time.Second):
				test.Fatal("cache Close did not reject new writers")
			}
			if err := cache.Put(test.Context(), hash, data); !errors.Is(err, maintenance.ErrClosed) {
				test.Fatal("closing cache admitted a new Put")
			}
			if _, err := cache.root.Stat("."); err != nil {
				test.Fatal("cache physical root closed before writer drain")
			}
			select {
			case <-closed:
				test.Fatal("cache Close escaped an admitted Put")
			default:
			}
			cacheProcessProbe(test, directory, "blocked")
			if mode == "cancel" {
				cancel()
			}
			close(barrier.release)
			err := <-finished
			if mode == "cancel" && !errors.Is(err, context.Canceled) || mode == "commit" && err != nil {
				test.Fatal("cache Close changed publication/cancellation outcome")
			}
			if err := <-closed; err != nil {
				test.Fatal(err)
			}
			if _, err := cache.root.Stat("."); err == nil {
				test.Fatal("successful managed Close left the physical root open")
			}
			_, err = os.Lstat(filepath.Join(cache.directory, hash+".tar.gz"))
			if mode == "commit" && err != nil || mode == "cancel" && !errors.Is(err, os.ErrNotExist) {
				test.Fatal("closed cache persisted an incorrect publication outcome")
			}
			entries, err := os.ReadDir(cache.directory)
			if err != nil {
				test.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".stage-") {
					test.Fatal("Close completed before stage cleanup")
				}
			}
			cacheProcessProbe(test, directory, "acquired")
		})
	}
}

func testManagedCacheFailedClose(test *testing.T) {
	for _, mode := range []string{"sole", "peer"} {
		test.Run(mode, func(test *testing.T) {
			store, cache, instance, directory := managedCacheFixture(test)
			_ = store.Close()
			_ = instance.Close()
			var peer *ArchiveCache
			if mode == "peer" {
				var err error
				peer, err = OpenArchiveCacheWithOptions(test.Context(), cache.directory, CacheOptions{MaintenanceManaged: true})
				if err != nil {
					test.Fatal(err)
				}
				test.Cleanup(func() { _ = peer.Close() })
			}
			failure := errors.New("synthetic physical root close failure")
			root := &cacheFailCloseRoot{Closer: cache.root, entered: make(chan struct{}), release: make(chan struct{}), failure: failure}
			cache.maintenance.physicalRoot = root
			defer releaseCacheBarrier(root.release)
			test.Cleanup(func() {
				_ = cache.root.Close()
				_ = cache.maintenance.instance.Close()
			})
			closed := make(chan error, 2)
			go func() { closed <- cache.Close() }()
			select {
			case <-root.entered:
			case <-time.After(time.Second):
				test.Fatal("physical root Close not reached")
			}
			data := fixtureArchive(test)
			if err := cache.Put(test.Context(), archiveHash(data), data); !errors.Is(err, maintenance.ErrClosed) {
				test.Fatal("cache admitted Put during physical Close")
			}
			cacheProcessProbe(test, directory, "blocked")
			go func() { closed <- cache.Close() }()
			close(root.release)
			for range 2 {
				if err := <-closed; !errors.Is(err, failure) {
					test.Fatal("cache Close washed physical closure failure")
				}
			}
			file, err := cache.root.OpenFile(".stage-close-failure", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				test.Fatal("fixture did not preserve a real writable root")
			}
			if _, err := file.Write(data); err != nil {
				test.Fatal(err)
			}
			if err := file.Close(); err != nil {
				test.Fatal(err)
			}
			if peer != nil {
				if err := peer.Put(test.Context(), archiveHash(data), data); err != nil {
					test.Fatal("failed cache Close closed a peer cache")
				}
				if err := peer.Close(); err != nil {
					test.Fatal(err)
				}
			}
			for range 3 {
				if err := cache.Close(); !errors.Is(err, failure) {
					test.Fatal("repeated cache Close lost cached failure")
				}
				if err := cache.Put(test.Context(), archiveHash(data), data); !errors.Is(err, maintenance.ErrClosed) {
					test.Fatal("failed cache Close reopened writer admission")
				}
			}
			if root.attempts.Load() != 1 {
				test.Fatal("cache Close implicitly retried unknown physical closure")
			}
			cacheProcessProbe(test, directory, "blocked")
			if err := cache.root.Close(); err != nil {
				test.Fatal(err)
			}
			if err := cache.Close(); !errors.Is(err, failure) {
				test.Fatal("external physical closure cleared cached failure")
			}
			cacheProcessProbe(test, directory, "blocked")
		})
	}
}

func testManagedCacheChildCloseDrain(test *testing.T) {
	store, cache, instance, directory := managedCacheFixture(test)
	_ = store.Close()
	_ = instance.Close()
	parent, releaseParent, err := cache.enterWriter(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer releaseParent()
	child, releaseChild, err := cache.enterWriter(parent)
	if err != nil {
		test.Fatal(err)
	}
	defer releaseChild()
	releaseParent()
	managed := cache.maintenance
	managed.mutex.Lock()
	changed := managed.changed
	managed.mutex.Unlock()
	closed := make(chan error, 1)
	go func() { closed <- cache.Close() }()
	select {
	case <-changed:
	case <-time.After(time.Second):
		test.Fatal("nested cache Close did not start")
	}
	data := fixtureArchive(test)
	if err := cache.Put(test.Context(), archiveHash(data), data); !errors.Is(err, maintenance.ErrClosed) {
		test.Fatal("closing cache accepted a fresh writer")
	}
	if err := cache.Put(child, archiveHash(data), data); err != nil {
		test.Fatal("cache closed physical root before its independent child completed")
	}
	select {
	case <-closed:
		test.Fatal("cache Close escaped its independent admitted child")
	default:
	}
	cacheProcessProbe(test, directory, "blocked")
	releaseChild()
	if err := <-closed; err != nil {
		test.Fatal(err)
	}
	cacheProcessProbe(test, directory, "acquired")
}

func testManagedCachePhysicalCloseTail(test *testing.T) {
	store, cache, instance, directory := managedCacheFixture(test)
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		test.Fatal(err)
	}
	root := &cacheCloseTailRoot{Closer: cache.root, entered: make(chan struct{}), release: make(chan struct{})}
	cache.maintenance.physicalRoot = root
	defer releaseCacheBarrier(root.release)
	closed := make(chan error, 1)
	go func() { closed <- cache.Close() }()
	select {
	case <-root.entered:
	case <-time.After(time.Second):
		test.Fatal("actual physical root Close did not finish")
	}
	if _, err := cache.root.Stat("."); err == nil {
		test.Fatal("fixture did not close the real physical root")
	}
	cacheProcessProbe(test, directory, "blocked")
	close(root.release)
	if err := <-closed; err != nil {
		test.Fatal(err)
	}
	cacheProcessProbe(test, directory, "acquired")
}

func testManagedCachePlatformAndDefaults(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	for _, scenario := range []struct {
		ctx     context.Context
		path    string
		options CacheOptions
		want    error
	}{
		{ctx, "relative", CacheOptions{MaxBytes: -1, MaintenanceManaged: true}, context.Canceled},
		{test.Context(), "relative", CacheOptions{MaintenanceManaged: true}, maintenance.ErrDirectory},
	} {
		cache, err := OpenArchiveCacheWithOptions(scenario.ctx, scenario.path, scenario.options)
		if cache != nil {
			_ = cache.Close()
		}
		if cache != nil || !errors.Is(err, scenario.want) {
			test.Fatal("managed cache changed basic argument precedence")
		}
	}
	cache, err := OpenArchiveCacheWithOptions(test.Context(), filepath.Join(directory, "missing", "archives"), CacheOptions{MaxBytes: -1, MaintenanceManaged: true})
	if cache != nil || err == nil || err.Error() != "repository archive cache budget must be positive and at most 1 TiB" {
		test.Fatal("managed cache platform admission ran before budget validation")
	}
	capability := maintenance.CheckPlatform(test.Context())
	cache, err = OpenArchiveCacheWithOptions(test.Context(), filepath.Join(directory, "missing", "archives"), CacheOptions{MaintenanceManaged: true})
	if cache != nil {
		_ = cache.Close()
	}
	if errors.Is(capability, maintenance.ErrUnsupported) {
		if cache != nil || !errors.Is(err, maintenance.ErrUnsupported) {
			test.Fatal("unsupported cache startup reached filesystem probing")
		}
	} else if cache != nil || !errors.Is(err, os.ErrNotExist) {
		test.Fatal("Unix cache startup changed canonical-parent validation")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		test.Fatal("rejected managed cache startup mutated the filesystem")
	}
	cache, err = OpenArchiveCache(filepath.Join(directory, "archives"))
	if err != nil {
		if errors.Is(capability, maintenance.ErrUnsupported) && err.Error() == "open repository archive cache" {
			return
		}
		test.Fatal(err)
	}
	defer cache.Close()
	if cache.maintenance != nil {
		test.Fatal("default cache implicitly joined managed mode")
	}
	peer, err := OpenArchiveCacheWithOptions(test.Context(), cache.directory, CacheOptions{})
	if err != nil {
		test.Fatal(err)
	}
	defer peer.Close()
	cacheProcessProbe(test, directory, "default")
	data := fixtureArchive(test)
	stored, err := peer.Read(test.Context(), archiveHash(data))
	if err != nil || !bytes.Equal(stored, data) {
		test.Fatal("default peer process did not publish a shared immutable archive")
	}
}

func testManagedCacheIdentityAndFailureCleanup(test *testing.T) {
	if err := maintenance.CheckPlatform(test.Context()); errors.Is(err, maintenance.ErrUnsupported) {
		test.Skip("managed instance unsupported")
	}
	parent := test.TempDir()
	directory := filepath.Join(parent, "data")
	if err := os.Mkdir(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	ctx := &cacheReplacementContext{Context: test.Context(), replace: func() {
		if err := os.Rename(directory, directory+"-old"); err != nil {
			test.Fatal(err)
		}
		if err := os.Mkdir(directory, 0o700); err != nil {
			test.Fatal(err)
		}
	}}
	cache, err := OpenArchiveCacheWithOptions(ctx, filepath.Join(directory, "archives"), CacheOptions{MaintenanceManaged: true})
	if cache != nil {
		_ = cache.Close()
	}
	if cache != nil || !errors.Is(err, maintenance.ErrDirectory) {
		test.Fatal("managed startup accepted a replaced parent identity")
	}
	if _, err := os.Lstat(filepath.Join(directory, "archives")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("parent replacement was detected only after cache mutation")
	}
	cacheProcessProbe(test, directory, "acquired")
	cache, err = OpenArchiveCacheWithOptions(test.Context(), filepath.Join(directory, "archives"), CacheOptions{MaxBytes: 100, MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	second, err := OpenArchiveCacheWithOptions(test.Context(), cache.directory, CacheOptions{MaxBytes: 160, MaintenanceManaged: true})
	if second != nil {
		_ = second.Close()
	}
	if second != nil || !errors.Is(err, ErrCachePolicy) {
		test.Fatal("managed startup accepted a conflicting persisted budget")
	}
	if err := cache.Close(); err != nil {
		test.Fatal(err)
	}
	cacheProcessProbe(test, directory, "acquired")
}

func testManagedCacheIndependentLifetime(test *testing.T) {
	store, cache, instance, directory := managedCacheFixture(test)
	if err := instance.Close(); err != nil {
		test.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		test.Fatal(err)
	}
	cacheProcessProbe(test, directory, "blocked")
	if err := store.CreateUser(test.Context(), "Store-survives-cache"); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	cacheProcessProbe(test, directory, "acquired")
	store, cache, instance, directory = managedCacheFixture(test)
	_ = instance.Close()
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	cacheProcessProbe(test, directory, "blocked")
	data := fixtureArchive(test)
	if err := cache.Put(test.Context(), archiveHash(data), data); err != nil {
		test.Fatal("Store.Close closed the cache's independent physical root")
	}
	if err := cache.Close(); err != nil {
		test.Fatal(err)
	}
	cacheProcessProbe(test, directory, "acquired")
}

func TestManagedCacheStartupAndClose(test *testing.T) {
	test.Run("startup-admission", func(test *testing.T) {
		directory := test.TempDir()
		if err := os.Chmod(directory, 0o700); err != nil {
			test.Fatal(err)
		}
		store, err := state.OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), state.Options{MaintenanceManaged: true})
		if errors.Is(err, maintenance.ErrUnsupported) {
			test.Skip("managed instance unsupported")
		}
		if err != nil {
			test.Fatal(err)
		}
		defer store.Close()
		instance, err := maintenance.Join(test.Context(), directory)
		if err != nil {
			test.Fatal(err)
		}
		defer instance.Close()
		exclusive, err := instance.Quiesce(test.Context())
		if err != nil {
			test.Fatal(err)
		}
		defer exclusive.Release()
		path := filepath.Join(directory, "archives")
		cache, err := OpenArchiveCacheWithOptions(test.Context(), path, CacheOptions{MaintenanceManaged: true})
		if cache != nil {
			_ = cache.Close()
		}
		if cache != nil || !errors.Is(err, maintenance.ErrQuiescing) {
			test.Fatal("cache startup bypassed managed admission")
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("rejected startup created cache filesystem entries")
		}
		exclusive.Release()
		cache, err = OpenArchiveCacheWithOptions(test.Context(), path, CacheOptions{MaintenanceManaged: true})
		if err != nil {
			test.Fatal(err)
		}
		if err := cache.Close(); err != nil {
			test.Fatal(err)
		}
	})
	test.Run("close-drain", testManagedCacheCloseDrain)
	test.Run("close-nested-child", testManagedCacheChildCloseDrain)
	test.Run("failed-physical-close", testManagedCacheFailedClose)
	test.Run("physical-close-tail", testManagedCachePhysicalCloseTail)
	test.Run("platform-and-default", testManagedCachePlatformAndDefaults)
	test.Run("parent-identity-and-startup-cleanup", testManagedCacheIdentityAndFailureCleanup)
	test.Run("independent-components", testManagedCacheIndependentLifetime)
}
