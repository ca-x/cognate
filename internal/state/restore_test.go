package state_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/state"
)

func TestRestorePlanReservesBackupAndDestination(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source := filepath.Join(root, "installed")
	if err := store.Create(t.Context(), state.Installation{Slug: "demo", ContentHash: "original", Projections: []state.Projection{{Path: source, Kind: "canonical"}}}); err != nil {
		t.Fatal(err)
	}
	hash, modes := strings.Repeat("a", 64), strings.Repeat("b", 64)
	snapshot := filepath.Join(root, "snapshot")
	identity := "linux-dev-ino-v1:7:43"
	backup := state.OperationPlan{ID: "backup-one", Action: "backup", Slug: "demo", ContentHash: hash, Backup: state.BackupPlan{Format: state.BackupFormatV1, ModeHash: modes, RootMode: 0o755, SourceIdentity: "linux-dev-ino-v1:7:42", InstallationHash: "original"}, Targets: []state.OperationTarget{{Path: source, Kind: "canonical"}, {Path: snapshot, Kind: "backup", StageName: ".skillhub-stage-backup"}}}
	if err := store.BeginOperation(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), backup.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStageIdentity(t.Context(), backup.ID, 1, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkStageReady(t.Context(), backup.ID, 1, identity, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginBackupPublish(t.Context(), backup.ID, 1, identity, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteBackup(t.Context(), backup.ID, identity); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	plan := state.OperationPlan{ID: "restore-one", Action: "restore", Slug: "demo", ContentHash: hash, Backup: state.BackupPlan{BackupID: backup.ID, Format: state.BackupFormatV1, ModeHash: modes, RootMode: 0o755, SourceIdentity: identity}, Targets: []state.OperationTarget{{Path: snapshot, Kind: "backup_source"}, {Path: filepath.Join(root, "restored"), Kind: "restore_destination", StageName: ".skillhub-stage-restore"}}}
	if _, err := db.ExecContext(t.Context(), `UPDATE backups SET path = ? WHERE backup_key = ?`, plan.Targets[1].Path, backup.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("accepted backup with inconsistent path and key")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE backups SET path = ? WHERE backup_key = ?`, snapshot, backup.ID); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"backup-id", "hash", "mode-hash", "root-mode", "identity", "source", "source-child", "installed", "stage", "slug"} {
		t.Run(mutation, func(t *testing.T) {
			invalid := plan
			invalid.Targets = append([]state.OperationTarget(nil), plan.Targets...)
			switch mutation {
			case "backup-id":
				invalid.Backup.BackupID = "missing"
			case "hash":
				invalid.ContentHash = strings.Repeat("c", 64)
			case "mode-hash":
				invalid.Backup.ModeHash = strings.Repeat("c", 64)
			case "root-mode":
				invalid.Backup.RootMode = 0
			case "identity":
				invalid.Backup.SourceIdentity = "linux-dev-ino-v1:7:44"
			case "source":
				invalid.Targets[0].Path = filepath.Join(root, "missing")
			case "source-child":
				invalid.Targets[1].Path = filepath.Join(snapshot, "child")
			case "installed":
				invalid.Targets[1].Path = source
			case "stage":
				invalid.Targets[1].StageName = "invalid"
			case "slug":
				invalid.Slug = "other"
			}
			if err := store.BeginOperation(t.Context(), invalid); err == nil {
				t.Fatalf("accepted restore with %s mismatch", mutation)
			}
		})
	}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE backups SET root_mode = 0 WHERE backup_key = ?`, backup.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("started restore with changed backup metadata")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE backups SET root_mode = ? WHERE backup_key = ?`, plan.Backup.RootMode, backup.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE backup_test_copy AS SELECT * FROM backups WHERE backup_key = ?`, backup.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM backups WHERE backup_key = ?`, backup.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("started restore with missing backup")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := state.Open(t.Context(), filepath.Join(root, "client.db")); err == nil {
		_ = reopened.Close()
		t.Fatal("reopened pending restore with missing backup")
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO backups SELECT * FROM backup_test_copy`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TABLE backup_test_copy`); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Plan.Backup != plan.Backup || entry.Status != "planned" {
		t.Fatalf("restore = %+v, error = %v", entry, err)
	}
	conflict := plan
	conflict.ID = "restore-two"
	conflict.Targets = append([]state.OperationTarget(nil), plan.Targets...)
	conflict.Targets[1].Path = filepath.Join(root, "other")
	if err := store.BeginOperation(t.Context(), conflict); err == nil {
		t.Fatal("accepted overlapping pending backup source")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Plan.Backup.BackupID != backup.ID {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStageIdentity(t.Context(), plan.ID, 1, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkStageReady(t.Context(), plan.ID, 1, identity, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRestorePublish(t.Context(), plan.ID, 1, identity, hash); err != nil {
		t.Fatal(err)
	}
	for name, statement := range map[string]string{
		"published": `CREATE TRIGGER fail_restore BEFORE UPDATE OF published ON operation_targets WHEN NEW.kind = 'restore_destination' BEGIN SELECT RAISE(ABORT, 'injected'); END`,
		"status":    `CREATE TRIGGER fail_restore BEFORE UPDATE OF status ON operations WHEN NEW.status = 'completed' BEGIN SELECT RAISE(ABORT, 'injected'); END`,
		"claims":    `CREATE TRIGGER fail_restore BEFORE DELETE ON operation_claims BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if err := store.CompleteRestore(t.Context(), plan.ID, identity); err == nil {
				t.Fatal("completion ignored injected transaction failure")
			}
			current, err := store.GetOperation(t.Context(), plan.ID)
			if err != nil || current.Status != "active" || current.Plan.Targets[1].Published || current.Plan.Targets[1].ObjectIdentity != "" {
				t.Fatalf("partial restore completion: %+v, %v", current, err)
			}
			var claims int
			if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM operation_claims WHERE operation_id = (SELECT id FROM operations WHERE operation_key = ?)`, plan.ID).Scan(&claims); err != nil || claims != 4 {
				t.Fatalf("claims = %d, error = %v", claims, err)
			}
			if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_restore`); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := store.CompleteRestore(t.Context(), plan.ID, identity); err != nil {
		t.Fatal(err)
	}
	entry, err = store.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Status != "completed" || !entry.Plan.Targets[1].Published || entry.Plan.Targets[1].ObjectIdentity != identity {
		t.Fatalf("completed restore = %+v, error = %v", entry, err)
	}
	if _, err := store.GetBackup(t.Context(), backup.ID); err != nil {
		t.Fatalf("restore changed backup: %v", err)
	}
	if _, err := store.Get(t.Context(), "demo"); err != nil {
		t.Fatalf("restore changed installation: %v", err)
	}
}
