//go:build !linux

package state

import (
	"context"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"os"
	"time"
)

func restoreRevokeRun(context.Context, *runtimegen.OfflineRoot, *os.File, *os.File, []byte, RestoreDatabaseEvidence, [16]byte, time.Time, LocalSnapshotLimits) (RestoreRevocationEvidence, error) {
	return RestoreRevocationEvidence{}, ErrRestoreUnsupported
}
