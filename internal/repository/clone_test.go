package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCloneLocalRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	source := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = source
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	runGit("init", "--quiet", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "SKILL.md")
	runGit("commit", "--quiet", "-m", "fixture")
	root, cleanup, err := clone(t.Context(), source, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("temporary clone remains: %v", err)
	}
}
