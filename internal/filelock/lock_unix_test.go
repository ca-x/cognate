//go:build unix

package filelock_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/filelock"
	"golang.org/x/sys/unix"
)

func TestAcquireRejectsFIFOWithoutBlocking(t *testing.T) {
	if path := os.Getenv("SKILLHUB_TEST_LOCK_FIFO"); path != "" {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		if lock, err := filelock.Acquire(ctx, path); err == nil {
			_ = lock.Close()
			t.Fatal("accepted FIFO as a lock")
		}
		return
	}
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAcquireRejectsFIFOWithoutBlocking$")
	command.Env = append(os.Environ(), "SKILLHUB_TEST_LOCK_FIFO="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("FIFO lock inspection blocked or failed: %v: %s", err, output)
	}
}
