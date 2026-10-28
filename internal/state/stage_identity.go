package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/state/ent/operation"
	"github.com/ca-x/cognate/internal/state/ent/operationtarget"
)

func (store *Store) RecordStageIdentity(ctx context.Context, id string, position int, identity string) error {
	if err := objectid.Validate(identity); err != nil {
		return err
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start stage identity transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	count, err := tx.OperationTarget.Update().Where(
		operationtarget.PositionEQ(position),
		operationtarget.StageNameNEQ(""),
		operationtarget.StageIdentityEQ(""),
		operationtarget.HasOperationWith(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionIn(operation.ActionInstall, operation.ActionBackup, operation.ActionRestore)),
	).SetStageIdentity(identity).Save(ctx)
	if err != nil {
		return fmt.Errorf("record stage identity: %w", err)
	}
	if count != 1 {
		exists, err := tx.OperationTarget.Query().Where(
			operationtarget.PositionEQ(position),
			operationtarget.StageNameNEQ(""),
			operationtarget.StageIdentityEQ(identity),
			operationtarget.HasOperationWith(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionIn(operation.ActionInstall, operation.ActionBackup, operation.ActionRestore)),
		).Exist(ctx)
		if err != nil {
			return fmt.Errorf("inspect stage identity: %w", err)
		}
		if !exists {
			return fmt.Errorf("stage target is not active or identity differs")
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit stage identity: %w", err)
	}
	return nil
}

func validContentHash(hash string) bool {
	if len(hash) != sha256.Size*2 || hash != strings.ToLower(hash) {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

func (store *Store) MarkStageReady(ctx context.Context, id string, position int, identity, hash string) error {
	if err := objectid.Validate(identity); err != nil {
		return err
	}
	if !validContentHash(hash) {
		return fmt.Errorf("invalid planned content hash")
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start stage readiness transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	count, err := tx.OperationTarget.Update().Where(
		operationtarget.PositionEQ(position),
		operationtarget.StageNameNEQ(""),
		operationtarget.StageIdentityEQ(identity),
		operationtarget.StageReadyEQ(false),
		operationtarget.HasOperationWith(operation.OperationKeyEQ(id), operation.StatusEQ(operation.StatusActive), operation.ActionIn(operation.ActionInstall, operation.ActionBackup, operation.ActionRestore), operation.ContentHashEQ(hash)),
	).SetStageReady(true).Save(ctx)
	if err != nil {
		return fmt.Errorf("record stage readiness: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("stage target is not active, is already ready, or identity or hash differs")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit stage readiness: %w", err)
	}
	return nil
}
