//go:build !linux

package maintenance

import (
	"context"
	"errors"
	"os"
)

var ErrSnapshotGuardUncertain = errors.New("snapshot worker fence cleanup uncertain")

func snapshotGuardAvailable(*os.Root) error { return nil }

func (instance *Instance) AcquireSnapshotGuard(ctx context.Context, token *Exclusive) (*os.File, error) {
	if ctx == nil {
		return nil, ErrLease
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnsupported
}
