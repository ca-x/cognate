//go:build linux

package maintenance

import (
	"context"
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

var ErrSnapshotGuardUncertain = errors.New("snapshot worker fence cleanup uncertain")

var snapshotGuardRetained struct {
	sync.Mutex
	files []*os.File
}

func snapshotGuardClose(file *os.File) error {
	if file.Close() == nil {
		return nil
	}
	snapshotGuardRetained.Lock()
	snapshotGuardRetained.files = append(snapshotGuardRetained.files, file)
	snapshotGuardRetained.Unlock()
	return ErrSnapshotGuardUncertain
}

func snapshotGuardOpen(root *os.Root, create bool) (*os.File, error) {
	parent, err := root.Open(".")
	if err != nil {
		return nil, ErrLock
	}
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if create {
		flags |= unix.O_CREAT
	}
	descriptor, openError := unix.Openat(int(parent.Fd()), ".snapshot-worker.lock", flags, 0600)
	closeError := snapshotGuardClose(parent)
	if openError != nil {
		if closeError != nil {
			return nil, closeError
		}
		if !create && errors.Is(openError, unix.ENOENT) {
			return nil, nil
		}
		return nil, ErrLock
	}
	file := os.NewFile(uintptr(descriptor), "snapshot worker fence")
	var info unix.Stat_t
	statError := unix.Fstat(descriptor, &info)
	named, nameError := root.Lstat(".snapshot-worker.lock")
	opened, fileError := file.Stat()
	if closeError != nil || statError != nil || nameError != nil || fileError != nil || !os.SameFile(named, opened) || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Mode&0777 != 0600 || info.Nlink != 1 || info.Uid != uint32(os.Geteuid()) {
		if cleanup := snapshotGuardClose(file); cleanup != nil {
			return nil, cleanup
		}
		if closeError != nil {
			return nil, closeError
		}
		return nil, ErrLock
	}
	return file, nil
}

func snapshotGuardAvailable(root *os.Root) error {
	file, err := snapshotGuardOpen(root, false)
	if err != nil || file == nil {
		return err
	}
	lockError := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	closeError := snapshotGuardClose(file)
	if closeError != nil {
		return closeError
	}
	if lockError != nil {
		return ErrLock
	}
	return nil
}

func (instance *Instance) AcquireSnapshotGuard(ctx context.Context, token *Exclusive) (*os.File, error) {
	if ctx == nil || ctx.Err() != nil {
		if ctx != nil {
			return nil, ctx.Err()
		}
		return nil, ErrLease
	}
	if !token.ValidFor(instance) {
		return nil, ErrLease
	}
	registry.Lock()
	var root *os.Root
	for _, entry := range registry.entries {
		if entry.shared == instance.shared && entry.failure == nil && entry.ready == nil {
			root = entry.root
			break
		}
	}
	registry.Unlock()
	if root == nil {
		return nil, ErrClosed
	}
	file, err := snapshotGuardOpen(root, true)
	if err != nil {
		return nil, err
	}
	if unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil || !token.ValidFor(instance) || ctx.Err() != nil {
		if err := snapshotGuardClose(file); err != nil {
			return nil, err
		}
		return nil, ErrLock
	}
	return file, nil
}
