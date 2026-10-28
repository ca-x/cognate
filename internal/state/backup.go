package state

import (
	"context"
	"fmt"

	"github.com/ca-x/skillhub/internal/objectid"
	"github.com/ca-x/skillhub/internal/state/ent"
	"github.com/ca-x/skillhub/internal/state/ent/backup"
	"github.com/ca-x/skillhub/internal/state/ent/operation"
	"github.com/ca-x/skillhub/internal/state/ent/operationclaim"
	"github.com/ca-x/skillhub/internal/state/ent/operationtarget"
)

const BackupFormatV1 = "skillhub-snapshot-v1"

type Backup struct {
	ID                     string
	Slug                   string
	Format                 string
	SourcePath             string
	SourceIdentity         string
	InstallationGeneration string
	InstallationHash       string
	ContentHash            string
	ModeHash               string
	RootMode               int
	Path                   string
	Identity               string
}

func validateBackupPlan(plan OperationPlan) error {
	if plan.Action != "backup" || len(plan.Targets) != 2 || plan.Targets[0].Kind != "canonical" || plan.Targets[0].StageName != "" || plan.Targets[1].Kind != "backup" || plan.Targets[1].StageName == "" {
		return fmt.Errorf("backup requires a source and a staged backup target")
	}
	source := plan.Targets[0]
	if source.StageIdentity != "" || source.ObjectIdentity != "" || source.StageReady || source.PublishIntent || source.Published {
		return fmt.Errorf("backup source cannot have publication progress")
	}
	if plan.Backup.BackupID != "" || plan.Backup.Format != BackupFormatV1 || !validContentHash(plan.ContentHash) || !validContentHash(plan.Backup.ModeHash) || plan.Backup.RootMode < 0 || plan.Backup.RootMode > 0o777 || plan.Backup.InstallationHash == "" {
		return fmt.Errorf("invalid backup metadata")
	}
	if err := objectid.Validate(plan.Backup.SourceIdentity); err != nil {
		return fmt.Errorf("invalid backup source identity: %w", err)
	}
	return nil
}

func checkBackupPlan(ctx context.Context, tx *ent.Tx, plan OperationPlan, paths, reservations []string) error {
	if err := validateBackupPlan(plan); err != nil {
		return err
	}
	entries, err := tx.InstalledSkill.Query().WithProjections().All(ctx)
	if err != nil {
		return fmt.Errorf("inspect installed backup source: %w", err)
	}
	var reserved []string
	found := false
	for _, entry := range entries {
		owner := sameSlug(entry.Slug, plan.Slug)
		canonicalCount := 0
		if owner {
			if found || entry.Generation != plan.Backup.InstallationGeneration || entry.ContentHash != plan.Backup.InstallationHash {
				return fmt.Errorf("backup installation generation changed")
			}
			found = true
		}
		for _, projection := range entry.Edges.Projections {
			if owner && projection.Kind == "canonical" {
				canonicalCount++
				if projection.PathKey != projectionPathKey(paths[0]) || projection.Identity != "" && projection.Identity != plan.Backup.SourceIdentity {
					return fmt.Errorf("backup source differs from installation record")
				}
				continue
			}
			reserved = append(reserved, projection.PathKey)
		}
		if owner && canonicalCount != 1 {
			return fmt.Errorf("installed skill requires exactly one canonical source")
		}
	}
	if !found {
		return fmt.Errorf("backup source is not an installed skill")
	}
	backupPaths, err := recordedBackupPaths(ctx, tx)
	if err != nil {
		return err
	}
	reserved = append(reserved, backupPaths...)
	return validateTargetReservations(reserved, reservations)
}

func recordedBackupPaths(ctx context.Context, tx *ent.Tx) ([]string, error) {
	entries, err := tx.Backup.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect existing backups: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.PathKey)
	}
	return paths, nil
}

func (store *Store) CompleteBackup(ctx context.Context, id, identity string) error {
	if err := objectid.Validate(identity); err != nil {
		return err
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start backup completion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	entry, err := tx.Operation.Query().Where(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionEQ(operation.ActionBackup)).WithTargets(orderedTargets).Only(ctx)
	if err != nil {
		return fmt.Errorf("read active backup: %w", err)
	}
	plan := operationRecord(entry).Plan
	if err := validateBackupPlan(plan); err != nil {
		return err
	}
	target := entry.Edges.Targets[1]
	if !target.StageReady || !target.PublishIntent || target.Published || target.ObjectIdentity != "" || target.StageIdentity != identity {
		return fmt.Errorf("backup target is not ready for completion")
	}
	var reservations []string
	paths := make([]string, len(entry.Edges.Targets))
	for index, item := range entry.Edges.Targets {
		paths[index] = item.Path
		keys, err := targetReservationKeys(item.Path, item.StageName)
		if err != nil {
			return err
		}
		reservations = append(reservations, keys...)
	}
	if err := checkBackupPlan(ctx, tx, plan, paths, reservations); err != nil {
		return err
	}
	count, err := tx.OperationTarget.Update().Where(operationtarget.IDEQ(target.ID), operationtarget.PublishIntentEQ(true), operationtarget.PublishedEQ(false), operationtarget.StageIdentityEQ(identity), operationtarget.ObjectIdentityEQ("")).SetPublished(true).SetObjectIdentity(identity).Save(ctx)
	if err != nil {
		return fmt.Errorf("record backup publication: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("backup target is no longer ready")
	}
	if err := tx.Backup.Create().SetBackupKey(entry.OperationKey).SetSlug(entry.Slug).SetFormat(plan.Backup.Format).
		SetSourcePath(paths[0]).SetSourceIdentity(plan.Backup.SourceIdentity).SetInstallationGeneration(plan.Backup.InstallationGeneration).
		SetInstallationHash(plan.Backup.InstallationHash).SetContentHash(entry.ContentHash).SetModeHash(plan.Backup.ModeHash).
		SetRootMode(plan.Backup.RootMode).SetPath(target.Path).SetPathKey(target.PathKey).SetIdentity(identity).Exec(ctx); err != nil {
		return fmt.Errorf("record backup: %w", err)
	}
	count, err = tx.Operation.Update().Where(operation.IDEQ(entry.ID), operation.StatusEQ(operation.StatusActive)).SetStatus(operation.StatusCompleted).Save(ctx)
	if err != nil {
		return fmt.Errorf("complete backup operation: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("backup operation is no longer active")
	}
	if _, err := tx.OperationClaim.Delete().Where(operationclaim.OperationIDEQ(entry.ID)).Exec(ctx); err != nil {
		return fmt.Errorf("release backup claims: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit backup completion: %w", err)
	}
	return nil
}

func (store *Store) GetBackup(ctx context.Context, id string) (Backup, error) {
	entry, err := store.client.Backup.Query().Where(backup.BackupKeyEQ(id)).Only(ctx)
	if err != nil {
		return Backup{}, fmt.Errorf("read backup: %w", err)
	}
	return backupRecord(entry), nil
}

func (store *Store) ListBackups(ctx context.Context) ([]Backup, error) {
	entries, err := store.client.Backup.Query().Order(backup.ByBackupKey()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	result := make([]Backup, 0, len(entries))
	for _, entry := range entries {
		result = append(result, backupRecord(entry))
	}
	return result, nil
}

func backupRecord(entry *ent.Backup) Backup {
	return Backup{ID: entry.BackupKey, Slug: entry.Slug, Format: entry.Format, SourcePath: entry.SourcePath, SourceIdentity: entry.SourceIdentity, InstallationGeneration: entry.InstallationGeneration, InstallationHash: entry.InstallationHash, ContentHash: entry.ContentHash, ModeHash: entry.ModeHash, RootMode: entry.RootMode, Path: entry.Path, Identity: entry.Identity}
}
