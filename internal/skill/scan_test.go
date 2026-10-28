package skill_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ca-x/skillhub/internal/skill"
	"github.com/spf13/afero"
)

func TestScanFindsHiddenAndNestedSkills(t *testing.T) {
	fs := afero.NewMemMapFs()
	for path, content := range map[string]string{
		"/repo/.claude/skills/alpha/SKILL.md": "---\nname: Alpha\ndescription: Useful skill\n---\nBody\n",
		"/repo/skills/group/beta/SKILL.md":    "No frontmatter\n",
	} {
		if err := fs.MkdirAll(path[:len(path)-len("/SKILL.md")], 0o755); err != nil {
			t.Fatal(err)
		}
		if err := afero.WriteFile(fs, path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	skills, err := skill.Scan(fs, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("got %d skills, want 2", len(skills))
	}
	if skills[0].Name != "Alpha" || skills[0].Description != "Useful skill" || skills[0].Path != ".claude/skills/alpha" {
		t.Fatalf("unexpected first skill: %+v", skills[0])
	}
	if skills[1].Name != "beta" || skills[1].Description != "beta" || skills[1].Path != "skills/group/beta" {
		t.Fatalf("unexpected fallback skill: %+v", skills[1])
	}
}

func TestScanDoesNotFollowSkillSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "SKILL.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	skills, err := skill.Scan(afero.NewOsFs(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("followed skill symlink: %+v", skills)
	}
}

func TestScanSkipsGitMetadata(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	if err := filesystem.MkdirAll("/repo/.git/hidden", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(filesystem, "/repo/.git/hidden/SKILL.md", []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := skill.Scan(filesystem, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("Git metadata should not contain skills: %+v", skills)
	}
}

func TestScanRejectsMalformedFrontmatter(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	if err := filesystem.MkdirAll("/repo/test", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(filesystem, "/repo/test/SKILL.md", []byte("---\nname: [broken\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Scan(filesystem, "/repo"); err == nil {
		t.Fatal("expected invalid YAML error")
	}
}
