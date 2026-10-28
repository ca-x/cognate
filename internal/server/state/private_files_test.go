package state_test

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ca-x/skillhub/internal/server/state"
)

func TestPrepareDirectoryRejectsSymlinkBeforeCreatingChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix server file checks")
	}
	base, outside := privateDirectory(t), privateDirectory(t)
	if err := os.Symlink(outside, filepath.Join(base, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := state.PrepareDirectory(filepath.Join(base, "alias", "created")); err == nil {
		t.Fatal("accepted symlink ancestor")
	}
	if _, err := os.Stat(filepath.Join(outside, "created")); !os.IsNotExist(err) {
		t.Fatal("created data directory before checking path")
	}
	path := filepath.Join(base, "new", "nested")
	if err := state.PrepareDirectory(path); err != nil {
		t.Fatal(err)
	}
	if err := state.PrepareDirectory(path); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("private directory: %v", err)
	}
}

func TestReadMasterKeyRejectsUnsafePermissionsAndSize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix server file checks")
	}
	base := privateDirectory(t)
	path := filepath.Join(base, "master.key")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := state.ReadMasterKey(path)
	if err != nil || len(read) != 32 {
		t.Fatalf("private key: %v", err)
	}
	clear(read)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReadMasterKey(path); err == nil {
		t.Fatal("read publicly accessible master key")
	}
	if _, err := state.ReadPrivateFile(path, 1024); err == nil {
		t.Fatal("read publicly accessible private file")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(key, 0), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReadMasterKey(path); err == nil {
		t.Fatal("accepted oversized master key")
	}
	link := filepath.Join(base, "linked.key")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReadPrivateFile(link, 1024); err == nil {
		t.Fatal("read private file through symlink")
	}
}
