package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrustedDirectoryResolvesRelativeSymlinks(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"agent", "indirect", "outside/target"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../outside/target", filepath.Join(root, "indirect", "hop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../indirect/hop", filepath.Join(root, "agent", "skills")); err != nil {
		t.Fatal(err)
	}
	if err := validateTrustedDirectory(filepath.Join(root, "agent", "skills")); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedDirectoryChecksDotDotAfterSymlink(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"agent", "outside", "unsafe/child", "unsafe/target"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "unsafe"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "unsafe", "child"), filepath.Join(root, "outside", "subdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside/subdir/../target", filepath.Join(root, "agent", "skills")); err != nil {
		t.Fatal(err)
	}
	if err := validateTrustedDirectory(filepath.Join(root, "agent", "skills")); err == nil {
		t.Fatal("accepted untrusted directory hidden by dot-dot")
	}
}

func TestTrustedDirectoryRejectsSymlinkLoops(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("second", filepath.Join(root, "first")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("first", filepath.Join(root, "second")); err != nil {
		t.Fatal(err)
	}
	if err := validateTrustedDirectory(filepath.Join(root, "first")); err == nil {
		t.Fatal("accepted symlink loop")
	}
}

func TestTrustedDirectoryChecksInputDotDotAfterSymlink(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"base/data", "unsafe/sub", "unsafe/data"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "unsafe"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "unsafe", "sub"), filepath.Join(root, "base", "link")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "base", "link") + "/../data"
	if err := validateTrustedDirectory(source); err == nil {
		t.Fatal("accepted untrusted source hidden by input dot-dot")
	}
}
