//go:build !linux

package runtimegen

import (
	"context"
	"os"
)

func (*Runtime) WithWorkerFiles(context.Context, func(root, lock *os.File) error) error {
	return ErrUnsupported
}

func (*OfflineRoot) WithWorkerFiles(context.Context, func(root, lock *os.File) error) error {
	return ErrUnsupported
}
