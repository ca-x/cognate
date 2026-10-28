package objectid_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ca-x/skillhub/internal/objectid"
)

func TestIdentityTracksDirectoryAcrossRename(t *testing.T) {
	parent := t.TempDir()
	original := filepath.Join(parent, "original")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	first, err := objectid.FromFileInfo(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := objectid.Validate(first); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(moved)
	if err != nil {
		t.Fatal(err)
	}
	second, err := objectid.FromFileInfo(info)
	if err != nil || second != first {
		t.Fatalf("identity after rename = %q, error = %v", second, err)
	}
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := objectid.FromFileInfo(info)
	if err != nil || replacement == first {
		t.Fatalf("replacement identity = %q, error = %v", replacement, err)
	}
}

func TestLinkIdentityRequiresSymlink(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "link")
	if err := os.Symlink("missing", path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := objectid.FromLinkInfo(info)
	if err != nil || objectid.Validate(identity) != nil {
		t.Fatalf("identity = %q, error = %v", identity, err)
	}
	directory, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := objectid.FromLinkInfo(directory); err == nil {
		t.Fatal("accepted directory as symlink")
	}
}
