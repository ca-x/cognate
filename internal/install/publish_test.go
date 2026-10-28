package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishDirectoryDoesNotOverwriteExistingDirectory(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"stage", "target"} {
		if err := os.Mkdir(filepath.Join(parent, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := publishDirectory(parent, "stage", "target"); err == nil {
		t.Fatal("expected existing destination to be rejected")
	}
	for _, name := range []string{"stage", "target"} {
		if _, err := os.Stat(filepath.Join(parent, name)); err != nil {
			t.Fatalf("%s was removed: %v", name, err)
		}
	}
}

func TestPublishDirectoryMovesStageAtomically(t *testing.T) {
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "stage"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "stage", "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishDirectory(parent, "stage", "target"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(parent, "target", "SKILL.md"))
	if err != nil || string(content) != "skill" {
		t.Fatalf("unexpected content %q: %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(parent, "stage")); !os.IsNotExist(err) {
		t.Fatalf("stage remains: %v", err)
	}
}

func TestPublishDirectoryRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../outside", "nested/child"} {
		if err := publishDirectory(t.TempDir(), name, "target"); err == nil {
			t.Fatalf("accepted source %q", name)
		}
		if err := publishDirectory(t.TempDir(), "stage", name); err == nil {
			t.Fatalf("accepted destination %q", name)
		}
	}
}
