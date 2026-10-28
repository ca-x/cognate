//go:build !unix

package maintenance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func TestUnsupportedJoinFailsClosed(test *testing.T) {
	if instance, err := maintenance.Join(test.Context(), test.TempDir()); instance != nil || !errors.Is(err, maintenance.ErrUnsupported) {
		test.Fatalf("unsupported platform admitted an instance: %v", err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if instance, err := maintenance.Join(ctx, "unused"); instance != nil || !errors.Is(err, context.Canceled) {
		test.Fatalf("unsupported canceled Join: %v", err)
	}
	var instance maintenance.Instance
	if _, _, err := instance.Enter(test.Context()); !errors.Is(err, maintenance.ErrClosed) {
		test.Fatal("zero instance admitted a writer")
	}
	if token, err := instance.Quiesce(test.Context()); token != nil || !errors.Is(err, maintenance.ErrClosed) {
		test.Fatal("zero instance issued an exclusive token")
	}
}
