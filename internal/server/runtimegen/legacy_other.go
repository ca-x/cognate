//go:build !linux

package runtimegen

import "context"

func (runtime *Runtime) CheckLegacy(ctx context.Context, root string) error {
	return ErrUnsupported
}

func (runtime *Runtime) WithFreshRoot(ctx context.Context, root string, initialize func() error) error {
	return ErrUnsupported
}
