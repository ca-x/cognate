//go:build unix

package state

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDatabaseLinkInspectionDoesNotReleaseProcessLocks(t *testing.T) {
	if path := os.Getenv("SKILLHUB_TEST_LOCKED_FILE"); path != "" {
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 1}
		if err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &lock); !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EACCES) {
			t.Fatalf("expected lock contention, got %v", err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "database")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 1}
	if err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &lock); err != nil {
		t.Fatal(err)
	}
	if err := checkDatabaseLinks(path); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDatabaseLinkInspectionDoesNotReleaseProcessLocks$")
	command.Env = append(os.Environ(), "SKILLHUB_TEST_LOCKED_FILE="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("process lock was lost: %v: %s", err, output)
	}
}

func TestDatabaseInspectionDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	if err := checkDatabaseLinks(alias); err == nil {
		t.Fatal("inspection followed symlink")
	}
}

func TestDatabaseInspectionRejectsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkDatabaseLinks(path); err == nil {
		t.Fatal("inspection accepted FIFO")
	}
}
