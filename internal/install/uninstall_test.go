package install_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ca-x/skillhub/internal/install"
)

func TestPrepareUninstallLocalBacksUpAndReservesTargets(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only installation")
	}
	home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Chmod(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(home, ".agents", "skills")
	projection := filepath.Join(home, ".claude", "skills")
	for _, directory := range []string{canonical, projection} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill content"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
		t.Fatal(err)
	}
	backup, operationID, err := session.PrepareUninstallLocal(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(canonical, "demo"), filepath.Join(projection, "demo")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("planning changed %s: %v", path, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(backup.Path, "SKILL.md"))
	if err != nil || string(data) != "skill content" {
		t.Fatalf("backup contents = %q, error = %v", data, err)
	}
	if _, err := session.Installation(t.Context(), "demo"); err != nil {
		t.Fatal("planning removed installation record")
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "planned" || pending[0].Plan.ID != operationID || pending[0].Plan.Backup.BackupID != backup.ID {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if err := session.CancelPlannedOperation(t.Context(), operationID); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareUninstallReportsBackupAndOperationOnPlanFailure(t *testing.T) {
	session, _, directory := backupFixture(t)
	db, err := sql.Open("sqlite3", filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_uninstall_plan BEFORE INSERT ON operations WHEN NEW.action = 'uninstall' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	backup, operationID, err := session.PrepareUninstallLocal(t.Context(), "demo")
	if err == nil || backup.ID == "" || operationID == "" {
		t.Fatalf("backup = %+v, operation = %q, error = %v", backup, operationID, err)
	}
	if _, err := os.Stat(filepath.Join(backup.Path, "SKILL.md")); err != nil {
		t.Fatalf("completed backup missing: %v", err)
	}
}

func TestPrepareUninstallRejectsChangedInstallation(t *testing.T) {
	for _, change := range []string{"content", "extra", "symlink"} {
		t.Run(change, func(t *testing.T) {
			if runtime.GOOS != "linux" {
				t.Skip("Linux-only installation")
			}
			home, source, stateDirectory := t.TempDir(), t.TempDir(), t.TempDir()
			if err := os.Chmod(stateDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
			canonical := filepath.Join(home, ".agents", "skills")
			projection := filepath.Join(home, ".claude", "skills")
			for _, path := range []string{canonical, projection} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
				t.Fatal(err)
			}
			session, err := install.OpenSession(t.Context(), stateDirectory)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "content":
				if err := os.WriteFile(filepath.Join(canonical, "demo", "SKILL.md"), []byte("user edit"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if err := os.WriteFile(filepath.Join(canonical, "demo", "user.txt"), []byte("user data"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				path := filepath.Join(projection, "demo")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(canonical, "demo"), path); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := session.PrepareUninstallLocal(t.Context(), "demo"); err == nil {
				t.Fatalf("accepted %s change", change)
			}
			backups, err := session.Backups(t.Context())
			if err != nil || len(backups) != 0 {
				t.Fatalf("backups = %+v, error = %v", backups, err)
			}
			pending, err := session.PendingOperations(t.Context())
			if err != nil || len(pending) != 0 {
				t.Fatalf("pending = %+v, error = %v", pending, err)
			}
		})
	}
}
