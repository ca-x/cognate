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

func validateRestorePlan(plan OperationPlan) error {
	if plan.Action != "restore" || len(plan.Targets) != 2 || plan.Targets[0].Kind != "backup_source" || plan.Targets[0].StageName != "" || plan.Targets[1].Kind != "restore_destination" || plan.Targets[1].StageName == "" {
		return fmt.Errorf("restore requires a backup source and a staged destination")
	}
	source := plan.Targets[0]
	if source.StageIdentity != "" || source.ObjectIdentity != "" || source.StageReady || source.PublishIntent || source.Published {
		return fmt.Errorf("restore source cannot have publication progress")
	}
	if plan.Backup.BackupID == "" || plan.Backup.Format != BackupFormatV1 || !validContentHash(plan.ContentHash) || !validContentHash(plan.Backup.ModeHash) || plan.Backup.RootMode < 0 || plan.Backup.RootMode > 0o777 || plan.Backup.InstallationGeneration != "" || plan.Backup.InstallationHash != "" {
		return fmt.Errorf("invalid restore metadata")
	}
	if err := objectid.Validate(plan.Backup.SourceIdentity); err != nil {
		return fmt.Errorf("invalid restore source identity: %w", err)
	}
	return nil
}

func checkRestorePlan(ctx context.Context, tx *ent.Tx, plan OperationPlan, paths, reservations []string) error {
	if err := validateRestorePlan(plan); err != nil {
		return err
	}
	selected, err := tx.Backup.Query().Where(backup.BackupKeyEQ(plan.Backup.BackupID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("inspect restore backup: %w", err)
	}
	if !sameSlug(selected.Slug, plan.Slug) || selected.PathKey != projectionPathKey(paths[0]) || selected.Format != plan.Backup.Format || selected.ContentHash != plan.ContentHash || selected.ModeHash != plan.Backup.ModeHash || selected.RootMode != plan.Backup.RootMode || selected.Identity != plan.Backup.SourceIdentity {
		return fmt.Errorf("restore backup differs from the recorded snapshot")
	}
	installed, err := tx.InstalledSkill.Query().WithProjections().All(ctx)
	if err != nil {
		return fmt.Errorf("inspect installations for restore: %w", err)
	}
	var reserved []string
	for _, entry := range installed {
		for _, projection := range entry.Edges.Projections {
			if err := validateProjectionPath(projection.Path); err != nil || projection.PathKey != projectionPathKey(projection.Path) {
				return fmt.Errorf("installed projection path and key are inconsistent")
			}
			reserved = append(reserved, projection.PathKey)
		}
	}
	backups, err := tx.Backup.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("inspect backups for restore: %w", err)
	}
	for _, entry := range backups {
		if err := validateProjectionPath(entry.Path); err != nil || entry.PathKey != projectionPathKey(entry.Path) {
			return fmt.Errorf("backup path and key are inconsistent")
		}
		if entry.ID != selected.ID {
			reserved = append(reserved, entry.PathKey)
		}
	}
	return validateTargetReservations(reserved, reservations)
}

func (store *Store) CompleteRestore(ctx context.Context, id, identity string) error {
	if err := objectid.Validate(identity); err != nil {
		return err
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start restore completion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	entry, err := tx.Operation.Query().Where(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionEQ(operation.ActionRestore)).WithTargets(orderedTargets).Only(ctx)
	if err != nil {
		return fmt.Errorf("read active restore: %w", err)
	}
	plan := operationRecord(entry).Plan
	if err := validateRestorePlan(plan); err != nil {
		return err
	}
	target := entry.Edges.Targets[1]
	if !target.StageReady || !target.PublishIntent || target.Published || target.ObjectIdentity != "" || target.StageIdentity != identity {
		return fmt.Errorf("restore target is not ready for completion")
	}
	paths := make([]string, len(entry.Edges.Targets))
	var reservations []string
	for index, item := range entry.Edges.Targets {
		paths[index] = item.Path
		keys, err := targetReservationKeys(item.Path, item.StageName)
		if err != nil {
			return err
		}
		reservations = append(reservations, keys...)
	}
	if err := checkRestorePlan(ctx, tx, plan, paths, reservations); err != nil {
		return err
	}
	count, err := tx.OperationTarget.Update().Where(operationtarget.IDEQ(target.ID), operationtarget.PublishIntentEQ(true), operationtarget.PublishedEQ(false), operationtarget.StageIdentityEQ(identity), operationtarget.ObjectIdentityEQ("")).SetPublished(true).SetObjectIdentity(identity).Save(ctx)
	if err != nil {
		return fmt.Errorf("record restore publication: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("restore target is no longer ready")
	}
	count, err = tx.Operation.Update().Where(operation.IDEQ(entry.ID), operation.StatusEQ(operation.StatusActive)).SetStatus(operation.StatusCompleted).Save(ctx)
	if err != nil {
		return fmt.Errorf("complete restore operation: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("restore operation is no longer active")
	}
	if _, err := tx.OperationClaim.Delete().Where(operationclaim.OperationIDEQ(entry.ID)).Exec(ctx); err != nil {
		return fmt.Errorf("release restore claims: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restore completion: %w", err)
	}
	return nil
}
