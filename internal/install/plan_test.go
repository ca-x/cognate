package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/install"
)

func TestPlanLocalTargetsDeduplicatesDirectoryAliases(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(claude, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{".codex", ".gemini", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(canonical, filepath.Join(home, ".codex", "skills")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Symlink(claude, filepath.Join(home, ".cursor", "skills")); err != nil {
		t.Fatal(err)
	}
	plan, err := install.PlanLocalTargets(home, "demo", "test-id", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 2 || plan.Targets[0].Path != filepath.Join(canonical, "demo") || plan.Targets[0].Kind != "canonical" || plan.Targets[1].Path != filepath.Join(claude, "demo") || plan.Targets[1].Kind != "symlink" {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "skills")); !os.IsNotExist(err) {
		t.Fatalf("missing skills directory was created: %v", err)
	}
}

func TestPlanLocalTargetsRejectsInvalidInputs(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"", ".", "..", "../escape", `a\b`} {
		if _, err := install.PlanLocalTargets(home, slug, "test-id", strings.Repeat("a", 64)); err == nil {
			t.Fatalf("accepted slug %q", slug)
		}
	}
	if _, err := install.PlanLocalTargets(home, "demo", "../escape", strings.Repeat("a", 64)); err == nil {
		t.Fatal("accepted invalid operation id")
	}
	if _, err := install.PlanLocalTargets(home, "demo", "test-id", "invalid"); err == nil {
		t.Fatal("accepted invalid hash")
	}
}

func TestPlanLocalTargetsRejectsDanglingSkillsDirectory(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "skills")
	if err := os.Symlink(filepath.Join(home, "missing"), path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := install.PlanLocalTargets(home, "demo", "test-id", strings.Repeat("a", 64)); err == nil {
		t.Fatal("accepted dangling skills directory")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("dangling symlink changed: %v", err)
	}
}
