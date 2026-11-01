//go:build !linux

package library

import (
	"context"

	"github.com/ca-x/cognate/internal/server/runtimegen"
)

func VerifyOfflineArchive(context.Context, *runtimegen.OfflineRoot, string) error {
	return runtimegen.ErrUnsupported
}
