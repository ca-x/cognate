//go:build !linux

package library

import (
	"context"

	"github.com/ca-x/cognate/internal/server/runtimegen"
)

func ReadArchiveRepairInput(context.Context, string) ([]byte, error) {
	return nil, runtimegen.ErrUnsupported
}

func RepairOfflineArchive(context.Context, *runtimegen.OfflineRoot, string, string, []byte) (ArchiveRepairResult, error) {
	return ArchiveRepairResult{}, runtimegen.ErrUnsupported
}
