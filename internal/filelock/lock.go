package filelock

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/gofrs/flock"
)

func Acquire(ctx context.Context, path string) (*flock.Flock, error) {
	return acquire(ctx, path, true)
}

func AcquireExisting(ctx context.Context, path string) (*flock.Flock, error) {
	return acquire(ctx, path, false)
}

func acquire(ctx context.Context, path string, initialize bool) (*flock.Flock, error) {
	before, err := os.Lstat(path)
	if err != nil && (!initialize || !errors.Is(err, fs.ErrNotExist)) {
		return nil, fmt.Errorf("inspect lock: %w", err)
	}
	if err == nil && !before.Mode().IsRegular() {
		return nil, fmt.Errorf("lock must be a regular file")
	}
	flags := lockOpenFlags()
	if !initialize {
		flags &^= os.O_CREATE
	}
	lock := flock.New(path, flock.SetFlag(flags))
	acquired, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	if !acquired {
		return nil, fmt.Errorf("lock was not acquired")
	}
	opened, err := lock.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("inspect acquired lock: %w", err), lock.Close())
	}
	current, err := os.Lstat(path)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("inspect acquired lock path: %w", err), lock.Close())
	}
	if !opened.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(opened, current) || (before != nil && !os.SameFile(before, opened)) {
		return nil, errors.Join(fmt.Errorf("lock file changed or is not regular"), lock.Close())
	}
	return lock, nil
}
