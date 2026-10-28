package state

import (
	"context"
	"fmt"

	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/state/ent/operation"
	"github.com/ca-x/cognate/internal/state/ent/operationtarget"
)

func (store *Store) BeginPublish(ctx context.Context, id string, position int, identity, hash string) error {
	return store.advancePublish(ctx, id, position, identity, hash, operation.ActionInstall, operationtarget.KindCanonical, false)
}

func (store *Store) MarkPublished(ctx context.Context, id string, position int, identity, hash string) error {
	return store.advancePublish(ctx, id, position, identity, hash, operation.ActionInstall, operationtarget.KindCanonical, true)
}

func (store *Store) BeginBackupPublish(ctx context.Context, id string, position int, identity, hash string) error {
	return store.advancePublish(ctx, id, position, identity, hash, operation.ActionBackup, operationtarget.KindBackup, false)
}

func (store *Store) BeginRestorePublish(ctx context.Context, id string, position int, identity, hash string) error {
	return store.advancePublish(ctx, id, position, identity, hash, operation.ActionRestore, operationtarget.KindRestoreDestination, false)
}

func (store *Store) BeginProjection(ctx context.Context, id string, position int) error {
	return store.advanceProjection(ctx, id, position, "", false)
}

func (store *Store) MarkProjectionPublished(ctx context.Context, id string, position int, identity string) error {
	return store.advanceProjection(ctx, id, position, identity, true)
}

func (store *Store) advanceProjection(ctx context.Context, id string, position int, identity string, published bool) error {
	if published {
		if err := objectid.Validate(identity); err != nil {
			return err
		}
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start projection transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	canonical, err := tx.OperationTarget.Query().Where(operationtarget.KindEQ(operationtarget.KindCanonical), operationtarget.HasOperationWith(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionEQ(operation.ActionInstall))).All(ctx)
	if err != nil {
		return fmt.Errorf("inspect canonical target: %w", err)
	}
	if len(canonical) != 1 || !canonical[0].Published {
		return fmt.Errorf("projection requires exactly one published canonical target")
	}
	update := tx.OperationTarget.Update().Where(
		operationtarget.PositionEQ(position), operationtarget.KindEQ(operationtarget.KindSymlink),
		operationtarget.StageNameEQ(""), operationtarget.StageIdentityEQ(""), operationtarget.StageReadyEQ(false),
		operationtarget.PublishIntentEQ(published), operationtarget.PublishedEQ(false), operationtarget.ObjectIdentityEQ(""),
		operationtarget.HasOperationWith(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionEQ(operation.ActionInstall)),
	)
	if published {
		update.SetPublished(true).SetObjectIdentity(identity)
	} else {
		update.SetPublishIntent(true)
	}
	count, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("record projection progress: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("projection target is not in the expected state")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit projection progress: %w", err)
	}
	return nil
}

func (store *Store) advancePublish(ctx context.Context, id string, position int, identity, hash string, action operation.Action, kind operationtarget.Kind, published bool) error {
	if err := objectid.Validate(identity); err != nil {
		return err
	}
	if !validContentHash(hash) {
		return fmt.Errorf("invalid planned content hash")
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start publication transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	update := tx.OperationTarget.Update().Where(
		operationtarget.PositionEQ(position),
		operationtarget.KindEQ(kind),
		operationtarget.StageReadyEQ(true),
		operationtarget.StageIdentityEQ(identity),
		operationtarget.PublishIntentEQ(published),
		operationtarget.PublishedEQ(false),
		operationtarget.ObjectIdentityEQ(""),
		operationtarget.HasOperationWith(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionEQ(action), operation.ContentHashEQ(hash)),
	)
	if published {
		update.SetPublished(true).SetObjectIdentity(identity)
	} else {
		update.SetPublishIntent(true)
	}
	count, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("record publication progress: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("publication target is not in the expected state")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit publication progress: %w", err)
	}
	return nil
}
