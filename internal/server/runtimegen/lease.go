package runtimegen

import (
	"context"
	"errors"
)

var (
	ErrInvalid        = errors.New("runtime lease: invalid or unsafe input")
	ErrUnsupported    = errors.New("runtime lease: unsupported platform")
	ErrNeedsAttention = errors.New("runtime lease: needs attention")
	ErrUncertain      = errors.New("runtime lease: release or persistence uncertain")
)

type Runtime struct{ lease *rootLease }
type OfflineRoot struct{ lease *rootLease }

func OpenRuntime(ctx context.Context, root string) (*Runtime, error) {
	lease, err := openLease(ctx, root, false)
	if err != nil {
		return nil, err
	}
	return &Runtime{lease: lease}, nil
}

func LockOffline(ctx context.Context, root string) (*OfflineRoot, error) {
	lease, err := openLease(ctx, root, true)
	if err != nil {
		return nil, err
	}
	return &OfflineRoot{lease: lease}, nil
}

func (runtime *Runtime) Close() error {
	if runtime == nil || runtime.lease == nil {
		return ErrInvalid
	}
	return runtime.lease.close()
}

func (offline *OfflineRoot) Close() error {
	if offline == nil || offline.lease == nil {
		return ErrInvalid
	}
	return offline.lease.close()
}
