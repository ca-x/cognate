//go:build !linux

package runtimegen

import "context"

type rootLease struct{}

func openLease(ctx context.Context, root string, exclusive bool) (*rootLease, error) {
	return nil, ErrUnsupported
}

func (lease *rootLease) close() error { return ErrUnsupported }
