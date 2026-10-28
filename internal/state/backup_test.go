package state_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/state"
)

func TestBackupCompletionIsAtomic(t *testing.T) {
	for _, failure := range []string{"", "result", "record", "status", "claims"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			database := filepath.Join(root, "client.db")
			store, err := state.Open(t.Context(), database)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			source := filepath.Join(root, "demo")
			installed := state.Installation{Slug: "demo", ContentHash: "original", Projections: []state.Projection{{Path: source, Kind: "canonical"}}}
			if err := store.Create(t.Context(), installed); err != nil {
				t.Fatal(err)
			}
			hash, mode := strings.Repeat("a", 64), strings.Repeat("b", 64)
			sourceID, stageID := "linux-dev-ino-v1:7:42", "linux-dev-ino-v1:7:43"
			plan := state.OperationPlan{ID: "backup-one", Action: "backup", Slug: "demo", ContentHash: hash, Backup: state.BackupPlan{Format: state.BackupFormatV1, ModeHash: mode, RootMode: 0o700, SourceIdentity: sourceID, InstallationHash: "original"}, Targets: []state.OperationTarget{{Path: source, Kind: "canonical"}, {Path: filepath.Join(root, "snapshot"), Kind: "backup", StageName: ".skillhub-stage-backup"}}}
			if err := store.BeginOperation(t.Context(), plan); err != nil {
				t.Fatal(err)
			}
			if err := store.StartOperation(t.Context(), plan.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.RecordStageIdentity(t.Context(), plan.ID, 1, stageID); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkStageReady(t.Context(), plan.ID, 1, stageID, hash); err != nil {
				t.Fatal(err)
			}
			if err := store.BeginBackupPublish(t.Context(), plan.ID, 1, stageID, hash); err != nil {
				t.Fatal(err)
			}
			if failure != "" {
				triggers := map[string]string{
					"result": `CREATE TRIGGER fail_backup BEFORE UPDATE OF published ON operation_targets WHEN NEW.kind = 'backup' BEGIN SELECT RAISE(ABORT, 'injected'); END`,
					"record": `CREATE TRIGGER fail_backup BEFORE INSERT ON backups BEGIN SELECT RAISE(ABORT, 'injected'); END`,
					"status": `CREATE TRIGGER fail_backup BEFORE UPDATE OF status ON operations WHEN NEW.status = 'completed' BEGIN SELECT RAISE(ABORT, 'injected'); END`,
					"claims": `CREATE TRIGGER fail_backup BEFORE DELETE ON operation_claims BEGIN SELECT RAISE(ABORT, 'injected'); END`,
				}
				db, err := sql.Open("sqlite3", database)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(t.Context(), triggers[failure]); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = store.CompleteBackup(t.Context(), plan.ID, stageID)
			if (err != nil) != (failure != "") {
				t.Fatalf("completion error = %v", err)
			}
			entry, err := store.GetOperation(t.Context(), plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			backup, backupErr := store.GetBackup(t.Context(), plan.ID)
			if failure == "" {
				if entry.Status != "completed" || backupErr != nil || backup.SourcePath != source || backup.Identity != stageID || backup.ModeHash != mode {
					t.Fatalf("entry = %+v, backup = %+v, error = %v", entry, backup, backupErr)
				}
				conflict := state.OperationPlan{ID: "other", Action: "install", Slug: "other", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(backup.Path, "child"), Kind: "canonical"}}}
				if err := store.BeginOperation(t.Context(), conflict); err == nil {
					t.Fatal("accepted a target inside a recorded backup")
				}
			} else if entry.Status != "active" || backupErr == nil || entry.Plan.Targets[1].Published || entry.Plan.Targets[1].ObjectIdentity != "" {
				t.Fatalf("partial transaction: entry = %+v, error = %v", entry, backupErr)
			}
			if _, err := store.Get(t.Context(), installed.Slug); err != nil {
				t.Fatalf("backup changed installation: %v", err)
			}
		})
	}
}

func TestBackupPlanRequiresMatchingInstalledSource(t *testing.T) {
	for _, mutation := range []string{"missing", "generation", "source", "identity", "hash", "overlap"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			database := filepath.Join(root, "client.db")
			store, err := state.Open(t.Context(), database)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			source := filepath.Join(root, "demo")
			if mutation != "missing" {
				if err := store.Create(t.Context(), state.Installation{Slug: "demo", ContentHash: "original", Projections: []state.Projection{{Path: source, Kind: "canonical"}}}); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "overlap" {
				if err := store.Create(t.Context(), state.Installation{Slug: "other", ContentHash: "other", Projections: []state.Projection{{Path: filepath.Join(root, "owned"), Kind: "canonical"}}}); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "identity" {
				db, err := sql.Open("sqlite3", database)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(t.Context(), `UPDATE projections SET identity = 'linux-dev-ino-v1:7:99'`); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			plan := state.OperationPlan{ID: "backup", Action: "backup", Slug: "demo", ContentHash: strings.Repeat("a", 64), Backup: state.BackupPlan{Format: state.BackupFormatV1, ModeHash: strings.Repeat("b", 64), RootMode: 0o700, SourceIdentity: "linux-dev-ino-v1:7:42", InstallationHash: "original"}, Targets: []state.OperationTarget{{Path: source, Kind: "canonical"}, {Path: filepath.Join(root, "snapshot"), Kind: "backup", StageName: ".skillhub-stage-backup"}}}
			switch mutation {
			case "generation":
				plan.Backup.InstallationGeneration = "wrong"
			case "source":
				plan.Targets[0].Path = filepath.Join(root, "wrong")
			case "hash":
				plan.Backup.InstallationHash = "wrong"
			case "overlap":
				plan.Targets[1].Path = filepath.Join(root, "owned", "snapshot")
			}
			if err := store.BeginOperation(t.Context(), plan); err == nil {
				t.Fatalf("accepted %s backup plan", mutation)
			}
			pending, err := store.PendingOperations(t.Context())
			if err != nil || len(pending) != 0 {
				t.Fatalf("pending = %+v, error = %v", pending, err)
			}
		})
	}
}
