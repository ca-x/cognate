package skill_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

func TestContentHashContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := skill.ContentHashContext(ctx, afero.NewMemMapFs(), "/missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context cancellation", err)
	}
}

func TestContentHashTracksPathsAndContent(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	for _, root := range []string{"/source/first", "/destination/second"} {
		if err := filesystem.MkdirAll(root+"/assets", 0o755); err != nil {
			t.Fatal(err)
		}
		for path, content := range map[string]string{"SKILL.md": "same skill", "assets/example.txt": "example"} {
			if err := afero.WriteFile(filesystem, root+"/"+path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	first, err := skill.ContentHash(filesystem, "/source/first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := skill.ContentHash(filesystem, "/destination/second")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("same contents should hash equally: %q != %q", first, second)
	}
	if err := afero.WriteFile(filesystem, "/destination/second/assets/example.txt", []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := skill.ContentHash(filesystem, "/destination/second")
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("changed content must change the hash")
	}
	if err := afero.WriteFile(filesystem, "/destination/second/assets/example.txt", []byte("example"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.Rename("/destination/second/assets/example.txt", "/destination/second/assets/renamed.txt"); err != nil {
		t.Fatal(err)
	}
	renamed, err := skill.ContentHash(filesystem, "/destination/second")
	if err != nil {
		t.Fatal(err)
	}
	if first == renamed {
		t.Fatal("changed path must change the hash")
	}
}

func TestContentHashIncludesExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not preserve Unix executable mode")
	}
	root := t.TempDir()
	path := filepath.Join(root, "script")
	if err := os.WriteFile(path, []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := skill.ContentHash(afero.NewOsFs(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := skill.ContentHash(afero.NewOsFs(), root)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("executable mode must affect hash")
	}
}

func TestContentHashRejectsSymlinks(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "external.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := skill.ContentHash(afero.NewOsFs(), directory); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestContentHashIgnoresGitMetadata(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	if err := filesystem.MkdirAll("/repo/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(filesystem, "/repo/SKILL.md", []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(filesystem, "/repo/.git/config", []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := skill.ContentHash(filesystem, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(filesystem, "/repo/.git/config", []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := skill.ContentHash(filesystem, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("Git metadata must not affect the content hash")
	}
}
