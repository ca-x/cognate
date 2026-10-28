package state

import (
	"context"
	"fmt"

	"github.com/ca-x/skillhub/internal/objectid"
	"github.com/ca-x/skillhub/internal/state/ent/operation"
	"github.com/ca-x/skillhub/internal/state/ent/operationclaim"
	"github.com/ca-x/skillhub/internal/state/ent/operationtarget"
)

func (store *Store) CompleteInstallation(ctx context.Context, id string) error {
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start completion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	entry, err := tx.Operation.Query().Where(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionEQ(operation.ActionInstall)).WithTargets().Only(ctx)
	if err != nil {
		return fmt.Errorf("read active installation: %w", err)
	}
	var keys []string
	canonicalCount := 0
	for _, target := range entry.Edges.Targets {
		if !target.Published || target.Kind != operationtarget.KindCanonical && target.Kind != operationtarget.KindSymlink {
			return fmt.Errorf("installation targets are not all supported and published")
		}
		if err := objectid.Validate(target.ObjectIdentity); err != nil {
			return fmt.Errorf("published target lacks a valid object identity: %w", err)
		}
		if target.Kind == operationtarget.KindCanonical {
			canonicalCount++
		}
		keys = append(keys, target.PathKey)
	}
	if canonicalCount != 1 {
		return fmt.Errorf("installation requires exactly one canonical target")
	}
	if err := checkInstalledConflicts(ctx, tx, entry.Slug, keys); err != nil {
		return err
	}
	if entry.SubscriptionManaged {
		if err := requireDesiredSubscriptionSkill(ctx, tx, entry.Slug, entry.ContentHash); err != nil {
			return err
		}
	}
	installed, err := tx.InstalledSkill.Create().SetSlug(entry.Slug).SetContentHash(entry.ContentHash).SetGeneration(entry.OperationKey).SetSubscriptionManaged(entry.SubscriptionManaged).Save(ctx)
	if err != nil {
		return fmt.Errorf("record installation: %w", err)
	}
	for _, target := range entry.Edges.Targets {
		if err := tx.Projection.Create().SetPath(target.Path).SetPathKey(target.PathKey).SetKind(string(target.Kind)).SetIdentity(target.ObjectIdentity).SetSkill(installed).Exec(ctx); err != nil {
			return fmt.Errorf("record installation target: %w", err)
		}
	}
	count, err := tx.Operation.Update().Where(operation.IDEQ(entry.ID), operation.StatusEQ(operation.StatusActive)).SetStatus(operation.StatusCompleted).Save(ctx)
	if err != nil {
		return fmt.Errorf("complete installation operation: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("installation operation is no longer active")
	}
	if _, err := tx.OperationClaim.Delete().Where(operationclaim.OperationIDEQ(entry.ID)).Exec(ctx); err != nil {
		return fmt.Errorf("release installation claims: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit installation: %w", err)
	}
	return nil
}
