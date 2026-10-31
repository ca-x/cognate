//go:build unix

package maintenance

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/ca-x/cognate/internal/filelock"
	"github.com/gofrs/flock"
)

const joinTimeout = time.Second

type directoryEntry struct {
	info       os.FileInfo
	paths      map[string]bool
	path       string
	root       *os.Root
	lock       *flock.Flock
	shared     *sharedGate
	ready      chan struct{}
	references int
	failure    error
}

var registry struct {
	sync.Mutex
	entries []*directoryEntry
}

func CheckPlatform(ctx context.Context) error {
	return ctx.Err()
}

func Join(ctx context.Context, directory string) (*Instance, error) {
	ctx, cancel := context.WithTimeout(ctx, joinTimeout)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		instance, changed, err := joinDirectory(ctx, directory)
		if err != nil || instance != nil {
			return instance, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func joinDirectory(ctx context.Context, directory string) (*Instance, <-chan struct{}, error) {
	info, err := inspectDirectory(directory)
	if err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, ErrDirectory
	}
	retained := false
	defer func() {
		if !retained {
			_ = root.Close()
		}
	}()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, nil, ErrDirectory
	}
	registry.Lock()
	for _, entry := range registry.entries {
		if !os.SameFile(info, entry.info) {
			if entry.paths[directory] {
				registry.Unlock()
				return nil, nil, ErrDirectory
			}
			continue
		}
		if entry.ready != nil {
			changed := entry.ready
			registry.Unlock()
			return nil, changed, nil
		}
		if entry.failure != nil {
			registry.Unlock()
			return nil, nil, entry.failure
		}
		if err := inspectLock(root, entry.lock); err != nil {
			registry.Unlock()
			return nil, nil, err
		}
		if err := ctx.Err(); err != nil {
			registry.Unlock()
			return nil, nil, err
		}
		entry.references++
		entry.paths[directory] = true
		instance := newInstance(entry.shared)
		registry.Unlock()
		return instance, nil, nil
	}
	entry := &directoryEntry{info: info, paths: map[string]bool{directory: true}, path: directory, root: root, ready: make(chan struct{})}
	entry.shared = &sharedGate{changed: make(chan struct{}), release: func() error { return releaseReference(entry) }}
	registry.entries = append(registry.entries, entry)
	retained = true
	registry.Unlock()
	err = lockDirectory(ctx, entry)
	registry.Lock()
	defer registry.Unlock()
	if err != nil {
		registry.entries = slices.DeleteFunc(registry.entries, func(candidate *directoryEntry) bool { return candidate == entry })
		err = errors.Join(err, root.Close())
		close(entry.ready)
		return nil, nil, err
	}
	entry.references = 1
	close(entry.ready)
	entry.ready = nil
	return newInstance(entry.shared), nil, nil
}

func newInstance(shared *sharedGate) *Instance {
	return &Instance{shared: shared, handle: &instanceHandle{done: make(chan struct{})}}
}

func inspectDirectory(directory string) (os.FileInfo, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, ErrDirectory
	}
	var result os.FileInfo
	for current := directory; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() {
			return nil, ErrDirectory
		}
		attributes, ok := info.Sys().(*syscall.Stat_t)
		if !ok || attributes.Uid != uint32(os.Geteuid()) && attributes.Uid != 0 {
			return nil, ErrDirectory
		}
		if current == directory {
			if attributes.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
				return nil, ErrDirectory
			}
			result = info
		} else if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return nil, ErrDirectory
		}
		if filepath.Dir(current) == current {
			return result, nil
		}
	}
}

func lockDirectory(ctx context.Context, entry *directoryEntry) error {
	file, err := entry.root.OpenFile(".instance.lock", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if err := file.Close(); err != nil {
			return ErrLock
		}
	} else if !errors.Is(err, fs.ErrExist) {
		return ErrLock
	}
	if err := inspectLock(entry.root, nil); err != nil {
		return err
	}
	lock, err := filelock.Acquire(ctx, filepath.Join(entry.path, ".instance.lock"))
	if err != nil {
		return errors.Join(ErrLock, ctx.Err())
	}
	info, err := inspectDirectory(entry.path)
	if err != nil || !os.SameFile(info, entry.info) {
		return errors.Join(ErrDirectory, lock.Close())
	}
	if err := errors.Join(inspectLock(entry.root, lock), ctx.Err()); err != nil {
		return errors.Join(err, lock.Close())
	}
	if err := snapshotGuardAvailable(entry.root); err != nil {
		return errors.Join(err, lock.Close())
	}
	entry.lock = lock
	return nil
}

func inspectLock(root *os.Root, lock *flock.Flock) error {
	info, err := root.Lstat(".instance.lock")
	if err != nil || !info.Mode().IsRegular() {
		return ErrLock
	}
	attributes, ok := info.Sys().(*syscall.Stat_t)
	if !ok || attributes.Uid != uint32(os.Geteuid()) || attributes.Nlink != 1 || info.Mode().Perm() != 0o600 {
		return ErrLock
	}
	if lock != nil {
		opened, err := lock.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return ErrLock
		}
	}
	return nil
}

func releaseReference(entry *directoryEntry) error {
	registry.Lock()
	entry.references--
	if entry.references != 0 {
		registry.Unlock()
		return nil
	}
	entry.ready = make(chan struct{})
	registry.Unlock()
	err := errors.Join(entry.lock.Close(), entry.root.Close())
	registry.Lock()
	defer registry.Unlock()
	if err == nil {
		registry.entries = slices.DeleteFunc(registry.entries, func(candidate *directoryEntry) bool { return candidate == entry })
	} else {
		entry.failure = ErrLock
	}
	close(entry.ready)
	entry.ready = nil
	return err
}
