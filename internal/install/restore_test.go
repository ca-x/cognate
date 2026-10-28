package install_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/install"
)

func TestRestoreLocalRejectsCancellationAndInvalidName(t *testing.T) {
	session, _, _ := backupFixture(t)
	backup, err := session.BackupLocal(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := session.RestoreLocal(ctx, backup.ID, parent, "copy"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	for _, name := range []string{"", ".", "..", "nested/copy", `nested\copy`} {
		if err := session.RestoreLocal(t.Context(), backup.ID, parent, name); err == nil {
			t.Fatalf("accepted invalid name %q", name)
		}
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("parent changed: %v, %v", entries, err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestRestoreLocalCreatesIndependentCopy(t *testing.T) {
	session, canonical, _ := backupFixture(t)
	if err := os.Mkdir(filepath.Join(canonical, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, ".git", "config"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	backup, err := session.BackupLocal(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	if err := session.RestoreLocal(t.Context(), backup.ID, parent, "copy"); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(parent, "copy")
	info, err := os.Stat(result)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("restored mode = %v, error = %v", info, err)
	}
	contents, err := os.ReadFile(filepath.Join(result, ".git", "config"))
	if err != nil || string(contents) != "private" {
		t.Fatalf("restored .git = %q, error = %v", contents, err)
	}
	if err := os.WriteFile(filepath.Join(result, "SKILL.md"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{canonical, backup.Path} {
		content, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
		if err != nil || string(content) != "skill" {
			t.Fatalf("source %s changed: %q, %v", path, content, err)
		}
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestRestoreLocalZeroRootPermissions(t *testing.T) {
	session, _, directory := backupFixture(t)
	backup, err := session.BackupLocal(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE backups SET root_mode = 0 WHERE backup_key = ?`, backup.ID); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	if err := session.RestoreLocal(t.Context(), backup.ID, parent, "copy"); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(parent, "copy")
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0 {
		t.Fatalf("restored mode = %v, error = %v", info, err)
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "SKILL.md"))
	if err != nil || string(data) != "skill" {
		t.Fatalf("restored content = %q, error = %v", data, err)
	}
}

func TestRestoreLocalCompletionFailureRetainsPublishedCopyAndClaims(t *testing.T) {
	session, _, directory := backupFixture(t)
	backup, err := session.BackupLocal(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_restore BEFORE UPDATE OF status ON operations WHEN NEW.action = 'restore' AND NEW.status = 'completed' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	restoreErr := session.RestoreLocal(t.Context(), backup.ID, parent, "copy")
	if restoreErr == nil {
		t.Fatal("ignored completion failure")
	}
	data, err := os.ReadFile(filepath.Join(parent, "copy", "SKILL.md"))
	if err != nil || string(data) != "skill" {
		t.Fatalf("published copy = %q, error = %v", data, err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Plan.Action != "restore" || pending[0].Status != "needs_attention" || !pending[0].Plan.Targets[1].PublishIntent || pending[0].Plan.Targets[1].Published {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	for _, detail := range []string{pending[0].Plan.ID, filepath.Join(parent, "copy"), "install-status"} {
		if !strings.Contains(restoreErr.Error(), detail) {
			t.Fatalf("restore error %q lacks %q", restoreErr, detail)
		}
	}
	var claims int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM operation_claims WHERE operation_id = (SELECT id FROM operations WHERE operation_key = ?)`, pending[0].Plan.ID).Scan(&claims); err != nil || claims != 4 {
		t.Fatalf("claims = %d, error = %v", claims, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for range 2 {
		pending, err := reopened.RecoverInterruptedOperations(t.Context())
		if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" {
			t.Fatalf("recovered pending = %+v, error = %v", pending, err)
		}
	}
}

func TestRestoreLocalIntentFailureExplainsReconciliation(t *testing.T) {
	session, _, directory := backupFixture(t)
	backup, err := session.BackupLocal(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_restore BEFORE INSERT ON operations WHEN NEW.action = 'restore' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	err = session.RestoreLocal(t.Context(), backup.ID, t.TempDir(), "copy")
	if err == nil || !strings.Contains(err.Error(), "restore operation ") || !strings.Contains(err.Error(), "install-status") {
		t.Fatalf("restore intent failure = %v", err)
	}
}

func TestRestoreLocalRejectsExistingDestinationAndDamagedBackup(t *testing.T) {
	for _, conflict := range []string{"file", "directory", "symlink", "dangling", "damaged"} {
		t.Run(conflict, func(t *testing.T) {
			session, _, _ := backupFixture(t)
			backup, err := session.BackupLocal(t.Context(), "demo")
			if err != nil {
				t.Fatal(err)
			}
			parent := t.TempDir()
			path := filepath.Join(parent, "copy")
			switch conflict {
			case "file":
				err = os.WriteFile(path, []byte("keep"), 0o600)
			case "directory":
				err = os.Mkdir(path, 0o700)
			case "symlink":
				err = os.Symlink(backup.Path, path)
			case "dangling":
				err = os.Symlink("missing", path)
			case "damaged":
				err = os.WriteFile(filepath.Join(backup.Path, "SKILL.md"), []byte("damaged"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := session.RestoreLocal(t.Context(), backup.ID, parent, "copy"); err == nil {
				t.Fatal("accepted conflicting target or damaged backup")
			}
			if conflict == "damaged" {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("created target for damaged backup: %v", err)
				}
			} else if _, err := os.Lstat(path); err != nil {
				t.Fatalf("removed existing target: %v", err)
			}
		})
	}
}
