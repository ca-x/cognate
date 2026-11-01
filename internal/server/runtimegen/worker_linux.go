//go:build linux

package runtimegen

import (
	"context"
	"errors"
	"io"
	"os"
)

func (runtime *Runtime) WithWorkerFiles(ctx context.Context, consume func(root, lock *os.File) error) error {
	if runtime == nil {
		return ErrInvalid
	}
	return runtime.lease.withWorkerFiles(ctx, consume)
}

func (offline *OfflineRoot) WithWorkerFiles(ctx context.Context, consume func(root, lock *os.File) error) error {
	if offline == nil {
		return ErrInvalid
	}
	return offline.lease.withWorkerFiles(ctx, consume)
}

func (lease *rootLease) withWorkerFiles(ctx context.Context, consume func(root, lock *os.File) error) error {
	if lease == nil || ctx == nil || consume == nil {
		return ErrInvalid
	}
	lease.mutex.Lock()
	defer lease.mutex.Unlock()
	if lease.done {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	check := func() error {
		if _, err := lease.fences.Seek(0, io.SeekStart); err != nil {
			return ErrInvalid
		}
		return lease.admit(ctx)
	}
	if err := check(); err != nil {
		return err
	}
	return errors.Join(consume(lease.root, lease.lock), check())
}
