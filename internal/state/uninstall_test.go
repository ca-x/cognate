package state_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/state"
)

func uninstallFixture(t *testing.T) (*state.Store, state.OperationPlan) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hash, mode := strings.Repeat("a", 64), strings.Repeat("b", 64)
	canonical := filepath.Join(root, "canonical")
	link := filepath.Join(root, "agent")
	canonicalID, linkID, backupID := "linux-dev-ino-v1:7:42", "linux-dev-ino-v1:7:43", "linux-dev-ino-v1:7:44"
	install := state.OperationPlan{ID: "install-one", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: canonical, Kind: "canonical", StageName: ".skillhub-stage-install"}, {Path: link, Kind: "symlink"}}}
	if err := store.BeginOperation(t.Context(), install); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), install.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStageIdentity(t.Context(), install.ID, 0, canonicalID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkStageReady(t.Context(), install.ID, 0, canonicalID, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginPublish(t.Context(), install.ID, 0, canonicalID, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkPublished(t.Context(), install.ID, 0, canonicalID, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginProjection(t.Context(), install.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkProjectionPublished(t.Context(), install.ID, 1, linkID); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInstallation(t.Context(), install.ID); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "snapshot")
	backup := state.OperationPlan{ID: "backup-one", Action: "backup", Slug: "demo", ContentHash: hash, Backup: state.BackupPlan{Format: state.BackupFormatV1, ModeHash: mode, RootMode: 0o755, SourceIdentity: canonicalID, InstallationGeneration: install.ID, InstallationHash: hash}, Targets: []state.OperationTarget{{Path: canonical, Kind: "canonical"}, {Path: snapshot, Kind: "backup", StageName: ".skillhub-stage-backup"}}}
	if err := store.BeginOperation(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), backup.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStageIdentity(t.Context(), backup.ID, 1, backupID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkStageReady(t.Context(), backup.ID, 1, backupID, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginBackupPublish(t.Context(), backup.ID, 1, backupID, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteBackup(t.Context(), backup.ID, backupID); err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "uninstall-one", Action: "uninstall", Slug: "demo", ContentHash: hash, Backup: state.BackupPlan{BackupID: backup.ID, Format: state.BackupFormatV1, ModeHash: mode, RootMode: 0o755, SourceIdentity: canonicalID, InstallationGeneration: install.ID, InstallationHash: hash}, Targets: []state.OperationTarget{{Path: canonical, Kind: "canonical", StageName: ".skillhub-stage-remove-canonical", ExpectedIdentity: canonicalID}, {Path: link, Kind: "symlink", StageName: ".skillhub-stage-remove-link", ExpectedIdentity: linkID}, {Path: snapshot, Kind: "backup_source", ExpectedIdentity: backupID}}}
	return store, plan
}

func TestConcurrentUninstallPlansReserveOnce(t *testing.T) {
	store, plan := uninstallFixture(t)
	other, err := state.Open(t.Context(), filepath.Join(filepath.Dir(plan.Targets[0].Path), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	second := plan
	second.ID = "uninstall-two"
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Go(func() { results <- store.BeginOperation(t.Context(), plan) })
	workers.Go(func() { results <- other.BeginOperation(t.Context(), second) })
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful competing plans = %d", successes)
	}
	pending, err := store.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending plans = %+v, %v", pending, err)
	}
}

func TestUninstallTargetProgressRetainsInstallation(t *testing.T) {
	store, plan := uninstallFixture(t)
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 1, plan.Targets[1].ExpectedIdentity); err == nil {
		t.Fatal("allowed removal before start")
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStageIdentity(t.Context(), plan.ID, 0, plan.Targets[0].ExpectedIdentity); err == nil {
		t.Fatal("recorded an uninstall isolation name as a created stage")
	}
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 0, plan.Targets[0].ExpectedIdentity); err == nil {
		t.Fatal("allowed canonical move before projections")
	}
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 2, plan.Targets[2].ExpectedIdentity); err == nil {
		t.Fatal("allowed backup source removal")
	}
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 1, "linux-dev-ino-v1:7:99"); err == nil {
		t.Fatal("accepted wrong projection identity")
	}
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 1, plan.Targets[1].ExpectedIdentity); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 1, plan.Targets[1].ExpectedIdentity); err == nil {
		t.Fatal("repeated removal intent")
	}
	if err := store.MarkUninstallTargetMoved(t.Context(), plan.ID, 1, "linux-dev-ino-v1:7:99"); err == nil {
		t.Fatal("accepted wrong moved identity")
	}
	if err := store.MarkUninstallTargetMoved(t.Context(), plan.ID, 1, plan.Targets[1].ExpectedIdentity); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 0, plan.Targets[0].ExpectedIdentity); err != nil {
		t.Fatal(err)
	}
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || !entry.Plan.Targets[1].PublishIntent || !entry.Plan.Targets[1].Published {
		t.Fatalf("target progress: %+v, %v", entry, err)
	}
	if _, err := store.Get(t.Context(), plan.Slug); err != nil {
		t.Fatalf("partial uninstall removed installation: %v", err)
	}
}

func TestUninstallRequiresBackupEvenWithoutInstallation(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	plan := state.OperationPlan{ID: "uninstall", Action: "uninstall", Slug: "missing", ContentHash: "invalid", Targets: []state.OperationTarget{{Path: filepath.Join(root, "missing"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("accepted uninstall without a backup")
	}
}

func TestUninstallCompletionIsAtomic(t *testing.T) {
	for _, failure := range []string{"", "projections", "installation", "status", "claims"} {
		t.Run(failure, func(t *testing.T) {
			store, plan := uninstallFixture(t)
			if err := store.BeginOperation(t.Context(), plan); err != nil {
				t.Fatal(err)
			}
			if err := store.StartOperation(t.Context(), plan.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.CompleteUninstall(t.Context(), plan.ID); err == nil {
				t.Fatal("completed with unmoved targets")
			}
			for _, position := range []int{1, 0} {
				identity := plan.Targets[position].ExpectedIdentity
				if err := store.BeginUninstallTarget(t.Context(), plan.ID, position, identity); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkUninstallTargetMoved(t.Context(), plan.ID, position, identity); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.CompleteUninstall(t.Context(), plan.ID); err == nil {
				t.Fatal("completed while isolation paths were not cleaned")
			}
			for _, position := range []int{1, 0} {
				identity := plan.Targets[position].ExpectedIdentity
				if err := store.BeginUninstallCleanup(t.Context(), plan.ID, position, identity); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkUninstallTargetCleaned(t.Context(), plan.ID, position, identity); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(filepath.Dir(plan.Targets[0].Path), "client.db")
			db, err := sql.Open("sqlite3", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if failure != "" {
				triggers := map[string]string{
					"projections":  `CREATE TRIGGER fail_uninstall BEFORE DELETE ON projections BEGIN SELECT RAISE(ABORT, 'injected'); END`,
					"installation": `CREATE TRIGGER fail_uninstall BEFORE DELETE ON installed_skills BEGIN SELECT RAISE(ABORT, 'injected'); END`,
					"status":       `CREATE TRIGGER fail_uninstall BEFORE UPDATE OF status ON operations WHEN NEW.status = 'completed' BEGIN SELECT RAISE(ABORT, 'injected'); END`,
					"claims":       `CREATE TRIGGER fail_uninstall BEFORE DELETE ON operation_claims BEGIN SELECT RAISE(ABORT, 'injected'); END`,
				}
				if _, err := db.ExecContext(t.Context(), triggers[failure]); err != nil {
					t.Fatal(err)
				}
			}
			err = store.CompleteUninstall(t.Context(), plan.ID)
			if (err != nil) != (failure != "") {
				t.Fatalf("completion: %v", err)
			}
			entry, err := store.GetOperation(t.Context(), plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, installationErr := store.Get(t.Context(), plan.Slug)
			if failure == "" && (entry.Status != "completed" || installationErr == nil) || failure != "" && (entry.Status != "active" || installationErr != nil) {
				t.Fatalf("non-atomic completion: %+v, %v", entry, installationErr)
			}
			if _, err := store.GetBackup(t.Context(), plan.Backup.BackupID); err != nil {
				t.Fatalf("backup lost: %v", err)
			}
			var claims int
			if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM operation_claims`).Scan(&claims); err != nil {
				t.Fatal(err)
			}
			if (claims == 0) != (failure == "") {
				t.Fatalf("claims = %d", claims)
			}
		})
	}
}

func TestUninstallRejectsMovedWithoutResultIdentity(t *testing.T) {
	store, plan := uninstallFixture(t)
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(filepath.Dir(plan.Targets[0].Path), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE operation_targets SET publish_intent = 1, published = 1, object_identity = '' WHERE kind = 'symlink' AND operation_id = (SELECT id FROM operations WHERE operation_key = ?)`, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 0, plan.Targets[0].ExpectedIdentity); err == nil {
		t.Fatal("accepted moved target without identity")
	}
}

func TestUninstallProgressSurvivesReopenWithoutCompleting(t *testing.T) {
	store, plan := uninstallFixture(t)
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 1, plan.Targets[1].ExpectedIdentity); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := state.Open(t.Context(), filepath.Join(filepath.Dir(plan.Targets[0].Path), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	entry, err := reopened.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Status != "active" || !entry.Plan.Targets[1].PublishIntent || entry.Plan.Targets[1].Published {
		t.Fatalf("reopened progress: %+v, %v", entry, err)
	}
	if err := reopened.CompleteUninstall(t.Context(), plan.ID); err == nil {
		t.Fatal("completed interrupted uninstall")
	}
	if err := reopened.MarkOperationNeedsAttention(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get(t.Context(), plan.Slug); err != nil {
		t.Fatalf("interruption removed installation: %v", err)
	}
}

func TestUninstallCleanupRequiresMovedTargetsAndPersistsIntent(t *testing.T) {
	store, plan := uninstallFixture(t)
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	linkID := plan.Targets[1].ExpectedIdentity
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 1, linkID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkUninstallTargetMoved(t.Context(), plan.ID, 1, linkID); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginUninstallCleanup(t.Context(), plan.ID, 1, linkID); err == nil {
		t.Fatal("cleanup allowed before all targets moved")
	}
	canonicalID := plan.Targets[0].ExpectedIdentity
	if err := store.BeginUninstallTarget(t.Context(), plan.ID, 0, canonicalID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkUninstallTargetMoved(t.Context(), plan.ID, 0, canonicalID); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginUninstallCleanup(t.Context(), plan.ID, 2, plan.Targets[2].ExpectedIdentity); err == nil {
		t.Fatal("backup can be cleaned")
	}
	if err := store.MarkUninstallTargetCleaned(t.Context(), plan.ID, 1, linkID); err == nil {
		t.Fatal("cleanup result without intent")
	}
	if err := store.BeginUninstallCleanup(t.Context(), plan.ID, 1, linkID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := state.Open(t.Context(), filepath.Join(filepath.Dir(plan.Targets[0].Path), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	entry, err := reopened.GetOperation(t.Context(), plan.ID)
	if err != nil || !entry.Plan.Targets[1].CleanupIntent || entry.Plan.Targets[1].Cleaned {
		t.Fatalf("cleanup after reopen: %+v, %v", entry, err)
	}
	if err := reopened.CompleteUninstall(t.Context(), plan.ID); err == nil {
		t.Fatal("completed with interrupted cleanup")
	}
}

func TestUninstallPlanProtectsResourcesAndRechecksBackup(t *testing.T) {
	store, plan := uninstallFixture(t)
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	for _, target := range plan.Targets {
		conflict := state.OperationPlan{ID: "other-" + target.Kind, Action: "install", Slug: "other", ContentHash: "hash", Targets: []state.OperationTarget{{Path: target.Path, Kind: "canonical"}}}
		if err := store.BeginOperation(t.Context(), conflict); err == nil {
			t.Fatalf("target %s was not reserved", target.Kind)
		}
	}
	path := filepath.Join(filepath.Dir(plan.Targets[0].Path), "client.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE projections SET identity = 'linux-dev-ino-v1:7:99' WHERE kind = 'symlink'`); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("started with changed projection identity")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE projections SET identity = 'linux-dev-ino-v1:7:43' WHERE kind = 'symlink'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE backups SET identity = 'linux-dev-ino-v1:7:99' WHERE backup_key = ?`, plan.Backup.BackupID); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("started with changed backup identity")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE backups SET identity = 'linux-dev-ino-v1:7:44' WHERE backup_key = ?`, plan.Backup.BackupID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE backups SET root_mode = 0 WHERE backup_key = ?`, plan.Backup.BackupID); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("started with changed backup")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := state.Open(t.Context(), path); err == nil {
		_ = reopened.Close()
		t.Fatal("reopened with changed backup")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE backups SET root_mode = ? WHERE backup_key = ?`, plan.Backup.RootMode, plan.Backup.BackupID); err != nil {
		t.Fatal(err)
	}
	reopened, err := state.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	entry, err := reopened.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Status != "planned" {
		t.Fatalf("plan after reopen: %+v, %v", entry, err)
	}
	if err := reopened.CancelPlannedOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get(t.Context(), plan.Slug); err != nil {
		t.Fatalf("cancel removed installation: %v", err)
	}
}

func TestUninstallPlanBindsInstallationAndBackup(t *testing.T) {
	store, plan := uninstallFixture(t)
	for _, mutation := range []string{"backup", "generation", "hash", "mode", "source", "missing-projection", "extra-projection", "kind", "stage", "backup-path", "projection-identity", "snapshot-identity"} {
		t.Run(mutation, func(t *testing.T) {
			invalid := plan
			invalid.Targets = append([]state.OperationTarget(nil), plan.Targets...)
			switch mutation {
			case "backup":
				invalid.Backup.BackupID = "missing"
			case "generation":
				invalid.Backup.InstallationGeneration = "other"
			case "hash":
				invalid.ContentHash = strings.Repeat("c", 64)
			case "mode":
				invalid.Backup.ModeHash = strings.Repeat("c", 64)
			case "source":
				invalid.Backup.SourceIdentity = "linux-dev-ino-v1:7:99"
			case "missing-projection":
				invalid.Targets = append(invalid.Targets[:1], invalid.Targets[2:]...)
			case "extra-projection":
				invalid.Targets = append(invalid.Targets, state.OperationTarget{Path: filepath.Join(filepath.Dir(plan.Targets[0].Path), "other"), Kind: "symlink", StageName: ".skillhub-stage-extra"})
			case "kind":
				invalid.Targets[1].Kind = "canonical"
			case "stage":
				invalid.Targets[1].StageName = "not-a-stage"
			case "backup-path":
				invalid.Targets[2].Path += "-other"
			case "projection-identity":
				invalid.Targets[1].ExpectedIdentity = "linux-dev-ino-v1:7:99"
			case "snapshot-identity":
				invalid.Targets[2].ExpectedIdentity = "linux-dev-ino-v1:7:99"
			}
			if err := store.BeginOperation(t.Context(), invalid); err == nil {
				t.Fatalf("accepted %s mismatch", mutation)
			}
		})
	}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Status != "planned" || len(entry.Plan.Targets) != 3 {
		t.Fatalf("pending uninstall: %+v: %v", entry, err)
	}
	if _, err := store.Get(t.Context(), plan.Slug); err != nil {
		t.Fatalf("planning removed installation: %v", err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
}
