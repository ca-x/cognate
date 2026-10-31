//go:build !linux

package state

import (
	"context"
	"os"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func snapshotPlatform(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return maintenance.ErrUnsupported
}
func retainLocalSnapshotSource(string) (*localSnapshotSource, error) { return nil, nil }
func (*localSnapshotSource) recheck() error                          { return maintenance.ErrUnsupported }
func (*localSnapshotSource) inspect(context.Context, LocalSnapshotLimits) error {
	return maintenance.ErrUnsupported
}
func (*localSnapshotSource) newStage() (*localSnapshotStage, error) {
	return nil, maintenance.ErrUnsupported
}
func (*localSnapshotStage) open(string) (*os.File, error) { return nil, maintenance.ErrUnsupported }
func (*localSnapshotStage) cleanup() error                { return maintenance.ErrUnsupported }
