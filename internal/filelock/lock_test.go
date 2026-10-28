package filelock_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ca-x/cognate/internal/filelock"
)

func TestAcquireRejectsSymlinkWithoutModifyingTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "lock")
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if lock, err := filelock.Acquire(t.Context(), alias); err == nil {
		_ = lock.Close()
		t.Fatal("accepted symlink as a lock")
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "keep" {
		t.Fatalf("target changed: %q, %v", content, err)
	}
}
