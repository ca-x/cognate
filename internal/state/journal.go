package state

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/stagename"
	"github.com/ca-x/cognate/internal/state/ent"
	"github.com/ca-x/cognate/internal/state/ent/operation"
	"github.com/ca-x/cognate/internal/state/ent/operationclaim"
	"github.com/ca-x/cognate/internal/state/ent/operationtarget"
)

type OperationPlan struct {
	ID                  string
	Action              string
	Slug                string
	ContentHash         string
	SubscriptionManaged bool
	Backup              BackupPlan
	Targets             []OperationTarget
}

type BackupPlan struct {
	BackupID               string
	Format                 string
	ModeHash               string
	RootMode               int
	SourceIdentity         string
	InstallationGeneration string
	InstallationHash       string
}

type OperationTarget struct {
	Path             string
	Kind             string
	StageName        string
	ExpectedIdentity string
	StageIdentity    string
	ObjectIdentity   string
	StageReady       bool
	PublishIntent    bool
	Published        bool
	CleanupIntent    bool
	Cleaned          bool
}

type Operation struct {
	Plan   OperationPlan
	Status string
}

func (store *Store) BeginOperation(ctx context.Context, plan OperationPlan) error {
	if err := ValidateSlug(plan.Slug); err != nil {
		return err
	}
	if len(plan.Targets) == 0 {
		return fmt.Errorf("operation requires at least one target")
	}
	paths := make([]string, len(plan.Targets))
	keys := make([]string, len(plan.Targets))
	var reservations []string
	for index, target := range plan.Targets {
		if plan.Action != "uninstall" && target.ExpectedIdentity != "" {
			return fmt.Errorf("unexpected planned target identity")
		}
		if target.StageIdentity != "" || target.ObjectIdentity != "" || target.StageReady || target.PublishIntent || target.Published || target.CleanupIntent || target.Cleaned {
			return fmt.Errorf("planned target cannot contain stage progress")
		}
		var err error
		paths[index], keys[index], err = plannedProjectionPath(target.Path)
		if err != nil {
			return fmt.Errorf("resolve operation target: %w", err)
		}
		targetKeys, err := targetReservationKeys(paths[index], target.StageName)
		if err != nil {
			return err
		}
		reservations = append(reservations, targetKeys...)
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start operation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if plan.SubscriptionManaged {
		if plan.Action != "install" {
			return fmt.Errorf("subscription ownership requires an installation operation")
		}
		if err := requireDesiredSubscriptionSkill(ctx, tx, plan.Slug, plan.ContentHash); err != nil {
			return err
		}
	}
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	if plan.Action == "backup" {
		if err := checkBackupPlan(ctx, tx, plan, paths, reservations); err != nil {
			return err
		}
	} else if plan.Action == "restore" {
		if err := checkRestorePlan(ctx, tx, plan, paths, reservations); err != nil {
			return err
		}
	} else if plan.Action == "uninstall" {
		if err := checkUninstallPlan(ctx, tx, plan, paths, reservations); err != nil {
			return err
		}
	} else {
		if plan.Backup != (BackupPlan{}) {
			return fmt.Errorf("non-backup operation cannot contain backup metadata")
		}
		if err := checkInstalledConflicts(ctx, tx, plan.Slug, reservations); err != nil {
			return err
		}
	}
	entry, err := tx.Operation.Create().SetOperationKey(plan.ID).SetAction(operation.Action(plan.Action)).SetSlug(plan.Slug).SetContentHash(plan.ContentHash).SetSubscriptionManaged(plan.SubscriptionManaged).
		SetBackupID(plan.Backup.BackupID).SetBackupFormat(plan.Backup.Format).SetBackupModeHash(plan.Backup.ModeHash).SetBackupRootMode(plan.Backup.RootMode).
		SetBackupSourceIdentity(plan.Backup.SourceIdentity).SetBackupInstallationGeneration(plan.Backup.InstallationGeneration).SetBackupInstallationHash(plan.Backup.InstallationHash).Save(ctx)
	if err != nil {
		return fmt.Errorf("record operation intent: %w", err)
	}
	existing, err := tx.OperationTarget.Query().Where(operationtarget.HasOperationWith(operation.StatusNotIn(operation.StatusCompleted, operation.StatusCancelled))).All(ctx)
	if err != nil {
		return fmt.Errorf("inspect pending operation targets: %w", err)
	}
	reserved := make([]string, 0, len(existing))
	for _, target := range existing {
		targetKeys, err := targetReservationKeys(target.Path, target.StageName)
		if err != nil {
			return err
		}
		reserved = append(reserved, targetKeys...)
	}
	if err := validateTargetReservations(reserved, reservations); err != nil {
		return err
	}
	for _, key := range operationClaimKeys(plan.Slug, reservations) {
		if err := tx.OperationClaim.Create().SetOperationID(entry.ID).SetKey(key).Exec(ctx); err != nil {
			return fmt.Errorf("reserve operation resource: %w", err)
		}
	}
	for index, target := range plan.Targets {
		err := tx.OperationTarget.Create().SetOperationID(entry.ID).SetPosition(index).SetPath(paths[index]).SetPathKey(keys[index]).SetStageName(target.StageName).SetExpectedIdentity(target.ExpectedIdentity).SetKind(operationtarget.Kind(target.Kind)).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record operation target: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit operation intent: %w", err)
	}
	return nil
}

func targetReservationKeys(path, stageName string) ([]string, error) {
	keys := []string{projectionPathKey(path)}
	if stageName == "" {
		return keys, nil
	}
	if err := stagename.Validate(stageName); err != nil {
		return nil, fmt.Errorf("invalid operation stage: %w", err)
	}
	stagePath := filepath.Join(filepath.Dir(path), stageName)
	return append(keys, projectionPathKey(stagePath)), nil
}

func validateTargetReservations(existing, targets []string) error {
	reserved := append([]string(nil), existing...)
	for _, target := range targets {
		for _, other := range reserved {
			if pathWithin(target, other) || pathWithin(other, target) {
				return fmt.Errorf("pending operation target paths overlap")
			}
		}
		reserved = append(reserved, target)
	}
	return nil
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func operationClaimKeys(slug string, targets []string) []string {
	if runtime.GOOS == "windows" {
		slug = strings.ToLower(slug)
	}
	claims := make([]string, 0, len(targets)+1)
	claims = append(claims, "slug:"+slug)
	for _, target := range targets {
		claims = append(claims, "target:"+target)
	}
	return claims
}

func ensureOperationClaims(ctx context.Context, client *ent.Client) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func reconcileOperationClaims(ctx context.Context, tx *ent.Tx) error {
	if _, err := tx.Operation.Update().Where(operation.IDLT(0), operation.IDGTE(0)).SetStatus(operation.StatusPlanned).Save(ctx); err != nil {
		return err
	}
	entries, err := tx.Operation.Query().Where(operation.StatusNotIn(operation.StatusCompleted, operation.StatusCancelled)).WithTargets().All(ctx)
	if err != nil {
		return err
	}
	var reserved []string
	for _, entry := range entries {
		if err := ValidateSlug(entry.Slug); err != nil {
			return fmt.Errorf("invalid pending operation slug: %w", err)
		}
		if err := operation.ActionValidator(entry.Action); err != nil {
			return fmt.Errorf("invalid pending operation action: %w", err)
		}
		if err := operation.StatusValidator(entry.Status); err != nil {
			return fmt.Errorf("invalid pending operation status: %w", err)
		}
		if len(entry.Edges.Targets) == 0 {
			return fmt.Errorf("pending operation has no targets")
		}
		keys := make([]string, 0, len(entry.Edges.Targets))
		positions := make([]bool, len(entry.Edges.Targets))
		ordered := make([]OperationTarget, len(entry.Edges.Targets))
		for _, target := range entry.Edges.Targets {
			if target.Position < 0 || target.Position >= len(positions) || positions[target.Position] {
				return fmt.Errorf("invalid pending operation target position")
			}
			positions[target.Position] = true
			ordered[target.Position] = OperationTarget{Path: target.Path, Kind: string(target.Kind), StageName: target.StageName, ExpectedIdentity: target.ExpectedIdentity, StageIdentity: target.StageIdentity, ObjectIdentity: target.ObjectIdentity, StageReady: target.StageReady, PublishIntent: target.PublishIntent, Published: target.Published, CleanupIntent: target.CleanupIntent, Cleaned: target.Cleaned}
			if entry.Action != operation.ActionUninstall && target.ExpectedIdentity != "" {
				return fmt.Errorf("unexpected pending target identity")
			}
			if err := validateProjectionPath(target.Path); err != nil {
				return fmt.Errorf("invalid pending operation target: %w", err)
			}
			if target.PathKey != projectionPathKey(target.Path) {
				return fmt.Errorf("pending operation target key does not match path")
			}
			if err := operationtarget.KindValidator(target.Kind); err != nil {
				return fmt.Errorf("invalid pending operation target kind: %w", err)
			}
			if target.StageIdentity != "" {
				if entry.Status == operation.StatusPlanned || target.StageName == "" {
					return fmt.Errorf("unexpected pending operation stage identity")
				}
				if err := objectid.Validate(target.StageIdentity); err != nil {
					return fmt.Errorf("invalid pending operation stage identity: %w", err)
				}
			}
			if target.StageReady && (target.StageIdentity == "" || !validContentHash(entry.ContentHash)) {
				return fmt.Errorf("invalid pending operation stage readiness")
			}
			if entry.Action == operation.ActionUninstall && (target.StageIdentity != "" || target.StageReady) {
				return fmt.Errorf("uninstall target cannot contain stage progress")
			}
			if entry.Action == operation.ActionUninstall && entry.Status == operation.StatusPlanned && (target.PublishIntent || target.Published) {
				return fmt.Errorf("planned uninstall cannot contain target progress")
			}
			if target.CleanupIntent || target.Cleaned {
				if entry.Action != operation.ActionUninstall || target.Kind == operationtarget.KindBackupSource || !target.Published || !target.CleanupIntent {
					return fmt.Errorf("invalid uninstall cleanup progress")
				}
			}
			if entry.Action == operation.ActionUninstall && target.Published && (target.ObjectIdentity == "" || target.ObjectIdentity != target.ExpectedIdentity) {
				return fmt.Errorf("uninstall moved target lacks expected identity")
			}
			if target.ObjectIdentity != "" {
				validInstall := entry.Action == operation.ActionInstall && (target.Kind == operationtarget.KindCanonical || target.Kind == operationtarget.KindSymlink)
				validBackup := entry.Action == operation.ActionBackup && target.Kind == operationtarget.KindBackup
				validRestore := entry.Action == operation.ActionRestore && target.Kind == operationtarget.KindRestoreDestination
				validUninstall := entry.Action == operation.ActionUninstall && (target.Kind == operationtarget.KindCanonical || target.Kind == operationtarget.KindSymlink) && target.ObjectIdentity == target.ExpectedIdentity
				if !target.Published || !validInstall && !validBackup && !validRestore && !validUninstall {
					return fmt.Errorf("unexpected published object identity")
				}
				if err := objectid.Validate(target.ObjectIdentity); err != nil {
					return fmt.Errorf("invalid published object identity: %w", err)
				}
				if entry.Action != operation.ActionUninstall && (target.Kind == operationtarget.KindCanonical || target.Kind == operationtarget.KindBackup || target.Kind == operationtarget.KindRestoreDestination) && target.ObjectIdentity != target.StageIdentity {
					return fmt.Errorf("canonical object identity differs from stage identity")
				}
			}
			canonicalIntent := target.Kind == operationtarget.KindCanonical && target.StageReady
			projectionIntent := target.Kind == operationtarget.KindSymlink && target.StageName == "" && target.StageIdentity == "" && !target.StageReady
			installIntent := entry.Action == operation.ActionInstall && (canonicalIntent || projectionIntent)
			backupIntent := entry.Action == operation.ActionBackup && target.Kind == operationtarget.KindBackup && target.StageReady
			restoreIntent := entry.Action == operation.ActionRestore && target.Kind == operationtarget.KindRestoreDestination && target.StageReady
			uninstallIntent := entry.Action == operation.ActionUninstall && (target.Kind == operationtarget.KindCanonical || target.Kind == operationtarget.KindSymlink) && target.StageName != "" && target.StageIdentity == "" && !target.StageReady
			if target.PublishIntent && !installIntent && !backupIntent && !restoreIntent && !uninstallIntent || target.Published && !target.PublishIntent {
				return fmt.Errorf("invalid pending operation publish progress")
			}
			targetKeys, err := targetReservationKeys(target.Path, target.StageName)
			if err != nil {
				return err
			}
			keys = append(keys, targetKeys...)
		}
		plan := operationRecord(entry).Plan
		plan.Targets = ordered
		if entry.Action == operation.ActionBackup {
			if err := validateBackupPlan(plan); err != nil {
				return err
			}
		} else if entry.Action == operation.ActionRestore {
			if err := validateRestorePlan(plan); err != nil {
				return err
			}
			paths := make([]string, len(plan.Targets))
			for index, target := range plan.Targets {
				paths[index] = target.Path
			}
			if err := checkRestorePlan(ctx, tx, plan, paths, keys); err != nil {
				return fmt.Errorf("invalid pending restore: %w", err)
			}
		} else if entry.Action == operation.ActionUninstall {
			paths := make([]string, len(plan.Targets))
			canonicalIntent := false
			for index, target := range plan.Targets {
				paths[index] = target.Path
				if target.Kind == "canonical" && target.PublishIntent {
					canonicalIntent = true
				}
			}
			if canonicalIntent {
				for _, target := range plan.Targets {
					if target.Kind == "symlink" && !target.Published {
						return fmt.Errorf("uninstall canonical intent precedes projection completion")
					}
				}
			}
			for _, target := range plan.Targets {
				if target.CleanupIntent {
					for _, other := range plan.Targets {
						if other.Kind != "backup_source" && !other.Published {
							return fmt.Errorf("uninstall cleanup precedes target moves")
						}
					}
				}
			}
			if err := checkUninstallPlan(ctx, tx, plan, paths, keys); err != nil {
				return fmt.Errorf("invalid pending uninstall: %w", err)
			}
		} else if plan.Backup != (BackupPlan{}) {
			return fmt.Errorf("non-backup operation contains backup metadata")
		}
		for _, target := range entry.Edges.Targets {
			if entry.Action != operation.ActionInstall || target.Kind != operationtarget.KindSymlink || !target.PublishIntent {
				continue
			}
			canonicalCount := 0
			for _, candidate := range entry.Edges.Targets {
				if candidate.Kind == operationtarget.KindCanonical {
					if !candidate.Published {
						return fmt.Errorf("projection progress requires a published canonical target")
					}
					canonicalCount++
				}
			}
			if canonicalCount != 1 {
				return fmt.Errorf("projection progress requires exactly one published canonical target")
			}
		}
		if err := validateTargetReservations(reserved, keys); err != nil {
			return err
		}
		reserved = append(reserved, keys...)
		for _, key := range operationClaimKeys(entry.Slug, keys) {
			claim, err := tx.OperationClaim.Query().Where(operationclaim.KeyEQ(key)).Only(ctx)
			if ent.IsNotFound(err) {
				if err := tx.OperationClaim.Create().SetOperationID(entry.ID).SetKey(key).Exec(ctx); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			if claim.OperationID != entry.ID {
				return fmt.Errorf("pending operations claim the same resource")
			}
		}
	}
	return nil
}

func (store *Store) GetOperation(ctx context.Context, id string) (Operation, error) {
	entry, err := store.client.Operation.Query().Where(operation.OperationKeyEQ(id)).WithTargets(orderedTargets).Only(ctx)
	if err != nil {
		return Operation{}, fmt.Errorf("read operation: %w", err)
	}
	return operationRecord(entry), nil
}

func (store *Store) PendingOperations(ctx context.Context) ([]Operation, error) {
	entries, err := store.client.Operation.Query().Where(operation.StatusNotIn(operation.StatusCompleted, operation.StatusCancelled)).Order(ent.Asc(operation.FieldID)).WithTargets(orderedTargets).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read pending operations: %w", err)
	}
	operations := make([]Operation, 0, len(entries))
	for _, entry := range entries {
		operations = append(operations, operationRecord(entry))
	}
	return operations, nil
}

func orderedTargets(query *ent.OperationTargetQuery) {
	query.Order(ent.Asc(operationtarget.FieldPosition))
}

func operationRecord(entry *ent.Operation) Operation {
	result := Operation{Plan: OperationPlan{ID: entry.OperationKey, Action: string(entry.Action), Slug: entry.Slug, ContentHash: entry.ContentHash, Backup: BackupPlan{BackupID: entry.BackupID, Format: entry.BackupFormat, ModeHash: entry.BackupModeHash, RootMode: entry.BackupRootMode, SourceIdentity: entry.BackupSourceIdentity, InstallationGeneration: entry.BackupInstallationGeneration, InstallationHash: entry.BackupInstallationHash}}, Status: string(entry.Status)}
	result.Plan.SubscriptionManaged = entry.SubscriptionManaged
	for _, target := range entry.Edges.Targets {
		result.Plan.Targets = append(result.Plan.Targets, OperationTarget{Path: target.Path, Kind: string(target.Kind), StageName: target.StageName, ExpectedIdentity: target.ExpectedIdentity, StageIdentity: target.StageIdentity, ObjectIdentity: target.ObjectIdentity, StageReady: target.StageReady, PublishIntent: target.PublishIntent, Published: target.Published, CleanupIntent: target.CleanupIntent, Cleaned: target.Cleaned})
	}
	return result
}
