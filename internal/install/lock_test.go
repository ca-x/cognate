package install

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestInstallationLockSerializesAndRespectsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install.lock")
	first, err := acquireLock(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := acquireLock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := acquireLock(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}
