//go:build !linux

package state

import (
	"context"
	"os"
)

func inspectRestoreDatabase(context.Context, *os.File, *os.File, []byte, LocalSnapshotLimits) (RestoreDatabaseEvidence, error) {
	return RestoreDatabaseEvidence{}, ErrRestoreUnsupported
}
