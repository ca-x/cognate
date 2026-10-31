//go:build linux

package runtimegen

import (
	"context"
	"errors"
	"io"
	"path/filepath"
)

func (runtime *Runtime) CheckLegacy(ctx context.Context, root string) error {
	return runtime.withLegacy(ctx, root, false, nil)
}

func (runtime *Runtime) WithFreshRoot(ctx context.Context, root string, initialize func() error) error {
	if initialize == nil {
		return ErrInvalid
	}
	return runtime.withLegacy(ctx, root, true, initialize)
}

func (runtime *Runtime) withLegacy(ctx context.Context, root string, fresh bool, initialize func() error) error {
	if runtime == nil || runtime.lease == nil || ctx == nil || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return ErrInvalid
	}
	lease := runtime.lease
	lease.mutex.Lock()
	defer lease.mutex.Unlock()
	if lease.done || root != lease.path {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := lease.check(); err != nil {
		return err
	}
	if _, err := lease.fences.Seek(0, io.SeekStart); err != nil {
		return ErrInvalid
	}
	names, err := lease.fences.Readdirnames(1)
	if len(names) != 0 {
		return ErrNeedsAttention
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	if err := legacyEntries(ctx, lease.root, fresh); err != nil {
		return err
	}
	if err := lease.check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if initialize != nil {
		if err := initialize(); err != nil {
			return err
		}
	}
	if err := lease.check(); err != nil {
		return err
	}
	return ctx.Err()
}
