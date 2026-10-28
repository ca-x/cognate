package install

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/afero"
)

func TestDiscoverTargetsIncludesExistingAgentsWithoutCreatingSkillDirectories(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	home := filepath.Join(os.TempDir(), "skillhub-test-home")
	claude := filepath.Join(home, ".claude", "skills")
	cursor := filepath.Join(home, ".cursor", "skills")
	for _, path := range []string{claude, cursor, filepath.Join(home, ".gemini")} {
		if err := filesystem.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DiscoverTargets(filesystem, home)
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{{Agent: "claude", SkillsDirectory: claude}, {Agent: "gemini", SkillsDirectory: filepath.Join(home, ".gemini", "skills")}, {Agent: "cursor", SkillsDirectory: cursor}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if _, err := filesystem.Stat(filepath.Join(home, ".gemini", "skills")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("discovery created a directory: %v", err)
	}
}

func TestDiscoverTargetsRejectsNonDirectory(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	home := filepath.Join(os.TempDir(), "skillhub-test-home")
	if err := filesystem.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(filesystem, filepath.Join(home, ".claude", "skills"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverTargets(filesystem, home); err == nil {
		t.Fatal("expected an error for a non-directory target")
	}
}

func TestDiscoverTargetsIncludesAdditionalInstalledAgents(t *testing.T) {
	filesystem := afero.NewMemMapFs()
	home := filepath.Join(os.TempDir(), "skillhub-test-home")
	for _, directory := range []string{filepath.Join(home, ".copilot"), filepath.Join(home, ".qwen"), filepath.Join(home, ".config", "goose")} {
		if err := filesystem.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DiscoverTargets(filesystem, home)
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{Agent: "github-copilot", SkillsDirectory: filepath.Join(home, ".copilot", "skills")},
		{Agent: "goose", SkillsDirectory: filepath.Join(home, ".config", "goose", "skills")},
		{Agent: "qwen-code", SkillsDirectory: filepath.Join(home, ".qwen", "skills")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDiscoverTargetsReportsInspectionErrors(t *testing.T) {
	filesystem := failingTargetFS{Fs: afero.NewMemMapFs()}
	if _, err := DiscoverTargets(filesystem, filepath.Join(os.TempDir(), "home")); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("got %v, want permission error", err)
	}
}

func TestDiscoverTargetsRejectsRelativeHome(t *testing.T) {
	if _, err := DiscoverTargets(afero.NewMemMapFs(), "relative"); err == nil {
		t.Fatal("expected an absolute home directory requirement")
	}
}

type failingTargetFS struct{ afero.Fs }

func (filesystem failingTargetFS) Stat(string) (os.FileInfo, error) {
	return nil, os.ErrPermission
}
