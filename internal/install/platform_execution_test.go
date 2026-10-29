//go:build darwin

package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

func TestDarwinExecutionLocalInstall(test *testing.T) {
	root, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil || os.Chmod(root, 0700) != nil {
		test.Fatal("DARWIN_INFRA canonical private root")
	}
	home, source, directory := filepath.Join(root, "home"), filepath.Join(root, "source"), filepath.Join(root, "state")
	for _, path := range []string{home, source, directory, filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			test.Fatal("DARWIN_INFRA private fixture")
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: Demo\ndescription: Synthetic Darwin fixture\n---\noriginal\n"), 0600); err != nil {
		test.Fatal("DARWIN_INFRA source write")
	}
	expected, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		test.Fatal("DARWIN_INFRA source hash")
	}
	session, err := OpenSession(test.Context(), directory)
	if err != nil {
		test.Fatal("DARWIN_INFRA actual SQLite session")
	}
	defer func() {
		if err := session.Close(); err != nil {
			test.Error("session close failed")
		}
	}()
	if err := session.InstallLocal(test.Context(), home, "demo", source); err != nil {
		if strings.Contains(err.Error(), "local installation is unsupported on this platform") {
			test.Log("DARWIN_BASELINE_REJECTION code=install-unsupported")
		}
		test.Fatal("Darwin local installation must succeed")
	}
	canonical := filepath.Join(home, ".agents", "skills", "demo")
	if actual, err := skill.ContentHash(afero.NewOsFs(), canonical); err != nil || actual != expected {
		test.Fatal("canonical hash mismatch")
	}
	if target, err := os.Readlink(filepath.Join(home, ".claude", "skills", "demo")); err != nil || target != canonical {
		test.Fatal("real agent projection mismatch")
	}
	record, err := session.Installation(test.Context(), "demo")
	if err != nil || record.ContentHash != expected {
		test.Fatal("installation record mismatch")
	}
	for _, projection := range record.Projections {
		if !strings.HasPrefix(projection.Identity, "darwin-dev-ino-v1:") {
			test.Fatal("non-Darwin identity journal")
		}
	}
	backup, err := session.BackupLocal(test.Context(), "demo")
	if err != nil {
		test.Fatal("actual backup failed")
	}
	if _, err := session.UninstallLocal(test.Context(), "demo"); err != nil {
		test.Fatal("actual uninstall failed")
	}
	if _, err := os.Lstat(canonical); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("canonical survived uninstall")
	}
	if err := session.RestoreLocal(test.Context(), backup.ID, root, "restored"); err != nil {
		test.Fatal("actual restore failed")
	}
	if actual, err := skill.ContentHash(afero.NewOsFs(), filepath.Join(root, "restored")); err != nil || actual != expected {
		test.Fatal("restored hash mismatch")
	}
}
