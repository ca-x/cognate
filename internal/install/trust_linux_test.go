package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTrustedTreeGitScopeAndCancellation(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	if err := os.Mkdir(git, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(git, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateTrustedTree(t.Context(), root, false); err != nil {
		t.Fatal(err)
	}
	if err := validateTrustedTree(t.Context(), root, true); err == nil {
		t.Fatal("accepted writable .git in canonical tree")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := validateTrustedTree(ctx, root, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
}
