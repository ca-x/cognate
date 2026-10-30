//go:build !unix

package state

import (
	"context"
	"os"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func inspectLocalSnapshotSourcePlatform(context.Context, *os.File, *os.File, uint64, localSnapshotSourceIO) (localSnapshotSourceObservation, error) {
	return localSnapshotSourceObservation{}, maintenance.ErrUnsupported
}
