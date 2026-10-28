package state

import (
	"context"
	"fmt"

	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/state/ent"
	"github.com/ca-x/cognate/internal/state/ent/backup"
	"github.com/ca-x/cognate/internal/state/ent/installedskill"
	"github.com/ca-x/cognate/internal/state/ent/operation"
	"github.com/ca-x/cognate/internal/state/ent/operationclaim"
	"github.com/ca-x/cognate/internal/state/ent/operationtarget"
	"github.com/ca-x/cognate/internal/state/ent/projection"
)

func validateUninstallPlan(plan OperationPlan) error {
	if plan.Action != "uninstall" || len(plan.Targets) < 2 || plan.Targets[len(plan.Targets)-1].Kind != "backup_source" || plan.Targets[len(plan.Targets)-1].StageName != "" {
		return fmt.Errorf("uninstall requires installation targets and a backup source")
	}
	if plan.Backup.BackupID == "" || plan.Backup.Format != BackupFormatV1 || !validContentHash(plan.ContentHash) || !validContentHash(plan.Backup.ModeHash) || plan.Backup.RootMode < 0 || plan.Backup.RootMode > 0o777 || plan.Backup.InstallationGeneration == "" || !validContentHash(plan.Backup.InstallationHash) {
		return fmt.Errorf("invalid uninstall backup metadata")
	}
	if err := objectid.Validate(plan.Backup.SourceIdentity); err != nil {
		return fmt.Errorf("invalid uninstall source identity: %w", err)
	}
	canonicalCount := 0
	for _, target := range plan.Targets {
		if err := objectid.Validate(target.ExpectedIdentity); err != nil {
			return fmt.Errorf("invalid expected uninstall identity: %w", err)
		}
	}
	for _, target := range plan.Targets[:len(plan.Targets)-1] {
		if target.StageName == "" || target.Kind != "canonical" && target.Kind != "symlink" {
			return fmt.Errorf("unsupported uninstall target")
		}
		if target.Kind == "canonical" {
			canonicalCount++
		}
	}
	if canonicalCount != 1 {
		return fmt.Errorf("uninstall requires exactly one canonical target")
	}
	return nil
}

func (store *Store) CompleteUninstall(ctx context.Context, id string) error {
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start uninstall completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile uninstall: %w", err)
	}
	entry, err := tx.Operation.Query().Where(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionEQ(operation.ActionUninstall)).WithTargets(orderedTargets).Only(ctx)
	if err != nil {
		return fmt.Errorf("read active uninstall: %w", err)
	}
	for _, target := range entry.Edges.Targets {
		if target.Kind != operationtarget.KindBackupSource && (!target.PublishIntent || !target.Published || target.ObjectIdentity != target.ExpectedIdentity || !target.CleanupIntent || !target.Cleaned) {
			return fmt.Errorf("uninstall targets are not all cleaned")
		}
	}
	installed, err := tx.InstalledSkill.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("inspect uninstall installation: %w", err)
	}
	var owner *ent.InstalledSkill
	for _, candidate := range installed {
		if sameSlug(candidate.Slug, entry.Slug) {
			if owner != nil {
				return fmt.Errorf("ambiguous uninstall installation")
			}
			owner = candidate
		}
	}
	if owner == nil || owner.Generation != entry.BackupInstallationGeneration || owner.ContentHash != entry.BackupInstallationHash {
		return fmt.Errorf("uninstall installation changed")
	}
	if _, err := tx.Projection.Delete().Where(projection.HasSkillWith(installedskill.IDEQ(owner.ID))).Exec(ctx); err != nil {
		return fmt.Errorf("remove installation projections: %w", err)
	}
	if err := tx.InstalledSkill.DeleteOneID(owner.ID).Exec(ctx); err != nil {
		return fmt.Errorf("remove installation record: %w", err)
	}
	count, err := tx.Operation.Update().Where(operation.IDEQ(entry.ID), operation.StatusEQ(operation.StatusActive)).SetStatus(operation.StatusCompleted).Save(ctx)
	if err != nil {
		return fmt.Errorf("complete uninstall: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("uninstall is no longer active")
	}
	if _, err := tx.OperationClaim.Delete().Where(operationclaim.OperationIDEQ(entry.ID)).Exec(ctx); err != nil {
		return fmt.Errorf("release uninstall claims: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("uninstall completion outcome may be uncertain: %w", err)
	}
	return nil
}

func (store *Store) BeginUninstallTarget(ctx context.Context, id string, position int, identity string) error {
	return store.advanceUninstallTarget(ctx, id, position, identity, false)
}

func (store *Store) MarkUninstallTargetMoved(ctx context.Context, id string, position int, identity string) error {
	return store.advanceUninstallTarget(ctx, id, position, identity, true)
}

func (store *Store) BeginUninstallCleanup(ctx context.Context, id string, position int, identity string) error {
	return store.advanceUninstallCleanup(ctx, id, position, identity, false)
}

func (store *Store) MarkUninstallTargetCleaned(ctx context.Context, id string, position int, identity string) error {
	return store.advanceUninstallCleanup(ctx, id, position, identity, true)
}

func (store *Store) advanceUninstallCleanup(ctx context.Context, id string, position int, identity string, cleaned bool) error {
	if err := objectid.Validate(identity); err != nil {
		return err
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start uninstall cleanup transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile uninstall: %w", err)
	}
	entry, err := tx.Operation.Query().Where(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionEQ(operation.ActionUninstall)).WithTargets(orderedTargets).Only(ctx)
	if err != nil {
		return fmt.Errorf("read active uninstall: %w", err)
	}
	if position < 0 || position >= len(entry.Edges.Targets) {
		return fmt.Errorf("uninstall target not found")
	}
	for _, target := range entry.Edges.Targets {
		if target.Kind != operationtarget.KindBackupSource && !target.Published {
			return fmt.Errorf("uninstall targets must be moved before cleanup")
		}
	}
	target := entry.Edges.Targets[position]
	if target.Kind == operationtarget.KindBackupSource || target.ExpectedIdentity != identity || target.ObjectIdentity != identity || !target.Published || target.CleanupIntent != cleaned || target.Cleaned {
		return fmt.Errorf("uninstall target is not ready for cleanup")
	}
	update := tx.OperationTarget.Update().Where(operationtarget.IDEQ(target.ID), operationtarget.ExpectedIdentityEQ(identity), operationtarget.ObjectIdentityEQ(identity), operationtarget.PublishedEQ(true), operationtarget.CleanupIntentEQ(cleaned), operationtarget.CleanedEQ(false))
	if cleaned {
		update.SetCleaned(true)
	} else {
		update.SetCleanupIntent(true)
	}
	count, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("record uninstall cleanup: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("uninstall cleanup target changed")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit uninstall cleanup: %w", err)
	}
	return nil
}

func (store *Store) advanceUninstallTarget(ctx context.Context, id string, position int, identity string, moved bool) error {
	if err := objectid.Validate(identity); err != nil {
		return err
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start uninstall target transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile uninstall: %w", err)
	}
	entry, err := tx.Operation.Query().Where(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionEQ(operation.ActionUninstall)).WithTargets(orderedTargets).Only(ctx)
	if err != nil {
		return fmt.Errorf("read active uninstall: %w", err)
	}
	if position < 0 || position >= len(entry.Edges.Targets) {
		return fmt.Errorf("uninstall target not found")
	}
	target := entry.Edges.Targets[position]
	if target.Kind != operationtarget.KindCanonical && target.Kind != operationtarget.KindSymlink || target.ExpectedIdentity != identity || target.PublishIntent != moved || target.Published || target.ObjectIdentity != "" {
		return fmt.Errorf("uninstall target is not ready")
	}
	if target.Kind == operationtarget.KindCanonical {
		for _, other := range entry.Edges.Targets {
			if other.Kind == operationtarget.KindSymlink && !other.Published {
				return fmt.Errorf("uninstall projections must move before canonical")
			}
		}
	}
	update := tx.OperationTarget.Update().Where(operationtarget.IDEQ(target.ID), operationtarget.ExpectedIdentityEQ(identity), operationtarget.PublishIntentEQ(moved), operationtarget.PublishedEQ(false), operationtarget.ObjectIdentityEQ(""))
	if moved {
		update.SetPublished(true).SetObjectIdentity(identity)
	} else {
		update.SetPublishIntent(true)
	}
	count, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("record uninstall target: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("uninstall target changed")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit uninstall target progress: %w", err)
	}
	return nil
}

func checkUninstallPlan(ctx context.Context, tx *ent.Tx, plan OperationPlan, paths, reservations []string) error {
	if err := validateUninstallPlan(plan); err != nil {
		return err
	}
	selected, err := tx.Backup.Query().Where(backup.BackupKeyEQ(plan.Backup.BackupID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("inspect uninstall backup: %w", err)
	}
	if !sameSlug(selected.Slug, plan.Slug) || selected.Format != plan.Backup.Format || selected.ContentHash != plan.ContentHash || selected.ModeHash != plan.Backup.ModeHash || selected.RootMode != plan.Backup.RootMode || selected.SourceIdentity != plan.Backup.SourceIdentity || selected.InstallationGeneration != plan.Backup.InstallationGeneration || selected.InstallationHash != plan.Backup.InstallationHash || selected.PathKey != projectionPathKey(paths[len(paths)-1]) || selected.PathKey != projectionPathKey(selected.Path) || selected.Identity != plan.Targets[len(plan.Targets)-1].ExpectedIdentity {
		return fmt.Errorf("uninstall backup differs from the recorded snapshot")
	}
	entries, err := tx.InstalledSkill.Query().WithProjections().All(ctx)
	if err != nil {
		return fmt.Errorf("inspect uninstall installation: %w", err)
	}
	var reserved []string
	found := false
	for _, entry := range entries {
		owner := sameSlug(entry.Slug, plan.Slug)
		if owner {
			if found || entry.Generation != plan.Backup.InstallationGeneration || entry.ContentHash != plan.Backup.InstallationHash || len(entry.Edges.Projections) != len(paths)-1 {
				return fmt.Errorf("uninstall installation changed")
			}
			found = true
		}
		for _, projection := range entry.Edges.Projections {
			if err := validateProjectionPath(projection.Path); err != nil || projection.PathKey != projectionPathKey(projection.Path) {
				return fmt.Errorf("installed projection path and key are inconsistent")
			}
			if !owner {
				reserved = append(reserved, projection.PathKey)
				continue
			}
			if err := objectid.Validate(projection.Identity); err != nil {
				return fmt.Errorf("uninstall target lacks installation identity")
			}
			matched := false
			for index, target := range plan.Targets[:len(plan.Targets)-1] {
				if projection.PathKey == projectionPathKey(paths[index]) && projection.Kind == target.Kind && projection.Identity == target.ExpectedIdentity {
					if target.Kind == "canonical" && (projection.Identity != selected.SourceIdentity || projection.Path != selected.SourcePath) {
						return fmt.Errorf("uninstall canonical differs from backup source")
					}
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("uninstall targets differ from installation")
			}
		}
	}
	if !found {
		return fmt.Errorf("uninstall installation not found")
	}
	backups, err := tx.Backup.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("inspect other backups: %w", err)
	}
	for _, item := range backups {
		if err := validateProjectionPath(item.Path); err != nil || item.PathKey != projectionPathKey(item.Path) {
			return fmt.Errorf("backup path and key are inconsistent")
		}
		if item.ID != selected.ID {
			reserved = append(reserved, item.PathKey)
		}
	}
	return validateTargetReservations(reserved, reservations)
}
