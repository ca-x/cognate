package install_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ca-x/skillhub/internal/install"
	"github.com/ca-x/skillhub/internal/skill"
	"github.com/ca-x/skillhub/internal/state"
)

func backupFixture(t *testing.T) (*install.Session, string, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only backup")
	}
	home, source, directory := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
		t.Fatal(err)
	}
	return session, filepath.Join(home, ".agents", "skills", "demo"), directory
}

func TestBackupLocalPublishesSnapshotAndRetainsInstallation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only backup")
	}
	home, source, directory := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(home, ".agents", "skills", "demo")
	if err := os.Chmod(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(canonical, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, ".git", "config"), []byte("private changes"), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := session.BackupLocal(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if backup.Slug != "demo" || backup.Format != "skillhub-snapshot-v1" || backup.SourcePath != canonical || backup.Path == "" || backup.ContentHash == "" || backup.ModeHash == "" {
		t.Fatalf("backup = %+v", backup)
	}
	if backup.ContentHash == backup.InstallationHash || backup.InstallationGeneration == "" || backup.RootMode != 0o755 {
		t.Fatalf("unexpected backup metadata: %+v", backup)
	}
	data, err := os.ReadFile(filepath.Join(backup.Path, ".git", "config"))
	if err != nil || string(data) != "private changes" {
		t.Fatalf("data = %q, error = %v", data, err)
	}
	backupRoot, err := os.OpenRoot(backup.Path)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := skill.BackupFingerprintRootContext(t.Context(), backupRoot)
	if closeErr := backupRoot.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || fingerprint.ContentHash != backup.ContentHash || fingerprint.ModeHash != backup.ModeHash || fingerprint.RootMode != 0o700 {
		t.Fatalf("fingerprint = %+v, error = %v", fingerprint, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recorded, err := store.GetBackup(t.Context(), backup.ID)
	if err != nil || recorded != backup {
		t.Fatalf("recorded = %+v, error = %v", recorded, err)
	}
	if _, err := store.Get(t.Context(), "demo"); err != nil {
		t.Fatalf("backup removed installation: %v", err)
	}
	pending, err := store.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestBackupLocalKeepsDistinctSnapshots(t *testing.T) {
	session, canonical, directory := backupFixture(t)
	first, err := session.BackupLocal(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := session.BackupLocal(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.Path == second.Path || first.ContentHash == second.ContentHash {
		t.Fatalf("first = %+v, second = %+v", first, second)
	}
	for _, item := range []struct{ path, content string }{{first.Path, "skill"}, {second.Path, "modified"}} {
		data, err := os.ReadFile(filepath.Join(item.path, "SKILL.md"))
		if err != nil || string(data) != item.content {
			t.Fatalf("data = %q, error = %v", data, err)
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{first.ID, second.ID} {
		if _, err := store.GetBackup(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupLocalFailurePreservesPublishedSnapshotAndClaims(t *testing.T) {
	session, canonical, directory := backupFixture(t)
	db, err := sql.Open("sqlite3", filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_backup BEFORE INSERT ON backups BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.BackupLocal(t.Context(), "demo"); err == nil {
		t.Fatal("ignored backup transaction failure")
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
		if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || pending[0].Plan.Action != "backup" || !pending[0].Plan.Targets[1].PublishIntent || pending[0].Plan.Targets[1].Published {
			t.Fatalf("pending = %+v, error = %v", pending, err)
		}
		data, err := os.ReadFile(filepath.Join(pending[0].Plan.Targets[1].Path, "SKILL.md"))
		if err != nil || string(data) != "skill" {
			t.Fatalf("backup data = %q, error = %v", data, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(canonical, "SKILL.md"))
	if err != nil || string(data) != "skill" {
		t.Fatalf("source data = %q, error = %v", data, err)
	}
	if _, err := reopened.BackupLocal(t.Context(), "demo"); err == nil {
		t.Fatal("ignored pending backup")
	}
}

func TestBackupLocalRejectsReplacedSource(t *testing.T) {
	session, canonical, _ := backupFixture(t)
	if err := os.Rename(canonical, canonical+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.BackupLocal(t.Context(), "demo"); err == nil {
		t.Fatal("accepted replaced source")
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestBackupLocalRejectsWritableBackupRoot(t *testing.T) {
	session, _, directory := backupFixture(t)
	path := filepath.Join(directory, "backups")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := session.BackupLocal(t.Context(), "demo"); err == nil {
		t.Fatal("accepted writable backup root")
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestBackupLocalCanceledBeforeCreatingBackupDirectory(t *testing.T) {
	session, _, directory := backupFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := session.BackupLocal(ctx, "demo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want canceled", err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "backups")); !os.IsNotExist(err) {
		t.Fatalf("created backup directory: %v", err)
	}
}

func TestBackupLocalDoesNotUpgradeLegacyOwnership(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only backup")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "state")
	canonical := filepath.Join(root, "canonical")
	for _, path := range []string{directory, canonical} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(t.Context(), state.Installation{Slug: "legacy", ContentHash: "old-hash", Projections: []state.Projection{{Path: canonical, Kind: "canonical"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := session.BackupLocal(t.Context(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if backup.InstallationGeneration != "" {
		t.Fatalf("legacy backup gained generation: %+v", backup)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.Get(t.Context(), "legacy")
	if err != nil || record.Generation != "" || len(record.Projections) != 1 || record.Projections[0].Identity != "" {
		t.Fatalf("record = %+v, error = %v", record, err)
	}
}

func TestBackupLocalCompletionSurvivesProcessExit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only backup")
	}
	if root := os.Getenv("SKILLHUB_BACKUP_EXIT_FIXTURE"); root != "" {
		home, source, directory := filepath.Join(root, "home"), filepath.Join(root, "source"), filepath.Join(root, "state")
		session, err := install.OpenSession(t.Context(), directory)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.InstallLocal(t.Context(), home, "demo", source); err != nil {
			t.Fatal(err)
		}
		if _, err := session.BackupLocal(t.Context(), "demo"); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	root := t.TempDir()
	for _, path := range []string{filepath.Join(root, "home", ".agents", "skills"), filepath.Join(root, "source"), filepath.Join(root, "state")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "source", "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestBackupLocalCompletionSurvivesProcessExit$")
	command.Env = append(os.Environ(), "SKILLHUB_BACKUP_EXIT_FIXTURE="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v: %s", err, output)
	}
	database := filepath.Join(root, "state", "client.db")
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRowContext(t.Context(), `SELECT backup_key FROM backups`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backup, err := store.GetBackup(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(backup.Path, "SKILL.md"))
	if err != nil || string(data) != "skill" {
		t.Fatalf("data = %q, error = %v", data, err)
	}
	pending, err := store.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestBackupLocalRejectsStateDirectoryInsideSource(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only backup")
	}
	root := t.TempDir()
	canonical := filepath.Join(root, "canonical")
	directory := filepath.Join(canonical, "state")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(t.Context(), state.Installation{Slug: "demo", ContentHash: "legacy", Projections: []state.Projection{{Path: canonical, Kind: "canonical"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.BackupLocal(t.Context(), "demo"); err == nil {
		t.Fatal("accepted state directory inside backup source")
	}
	if _, err := os.Lstat(filepath.Join(directory, "backups")); !os.IsNotExist(err) {
		t.Fatalf("created backup directory in source: %v", err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}
