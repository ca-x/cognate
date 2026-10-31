//go:build !linux

package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func TestLocalSnapshotUnsupportedBeforeIO(test *testing.T) {
	limits := LocalSnapshotLimits{MaxDBBytes: 1, MaxInventoryBytes: 1, MaxStagingBytes: 1, MaxRecords: 1, SQLiteHeapBytes: 65536, WorkerAddressSpaceBytes: 1 << 30, WorkerCPUSeconds: 1, MaxWALBytes: 1, MaxSHMBytes: 32768, DrainTimeout: time.Second, OperationTimeout: time.Second}
	var store *Store
	called := false
	result, err := store.WithLocalSnapshot(test.Context(), limits, func(context.Context, LocalSnapshotView) error { called = true; return nil })
	if !errors.Is(err, maintenance.ErrUnsupported) || result.get != nil || called {
		test.Fatal("unsupported platform did not refuse before Store/source access", err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	_, err = store.WithLocalSnapshot(ctx, limits, func(context.Context, LocalSnapshotView) error { return nil })
	if !errors.Is(err, context.Canceled) {
		test.Fatal("cancellation precedence changed", err)
	}
}
