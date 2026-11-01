package filelock_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestAcquireExistingNeverInitializes(test *testing.T) {
	path := filepath.Join(test.TempDir(), "absent.lock")
	if lock, err := filelock.AcquireExisting(test.Context(), path); err == nil {
		_ = lock.Close()
		test.Fatal("created missing lock")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("acquisition refusal initialized a file", err)
	}
	owner, err := filelock.Acquire(test.Context(), path)
	if err != nil {
		test.Fatal(err)
	}
	before, err := owner.Stat()
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 60*time.Millisecond)
	defer cancel()
	if lock, err := filelock.AcquireExisting(ctx, path); err == nil || ctx.Err() == nil {
		if lock != nil {
			_ = lock.Close()
		}
		test.Fatal("existing lock bypassed active owner", err)
	}
	if err := owner.Close(); err != nil {
		test.Fatal(err)
	}
	lock, err := filelock.AcquireExisting(test.Context(), path)
	if err != nil {
		test.Fatal(err)
	}
	after, statError := lock.Stat()
	closeError := lock.Close()
	if statError != nil || closeError != nil || !os.SameFile(before, after) {
		test.Fatal("existing lock inode changed", statError, closeError)
	}
}
