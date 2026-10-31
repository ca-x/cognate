//go:build !linux

package state

import (
	"context"
	"os"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func snapshotWorkerServe() int { return 2 }

func snapshotRunWorker(context.Context, *localSnapshotSource, *localSnapshotStage, *os.File, LocalSnapshotLimits) (snapshotWorkerResponse, bool, error) {
	return snapshotWorkerResponse{}, true, maintenance.ErrUnsupported
}
