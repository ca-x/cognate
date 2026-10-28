package state

import (
	"context"
	"fmt"

	"github.com/ca-x/skillhub/internal/state/ent/operation"
	"github.com/ca-x/skillhub/internal/state/ent/operationclaim"
)

func (store *Store) StartOperation(ctx context.Context, id string) error {
	return store.transitionOperation(ctx, id, operation.StatusPlanned, operation.StatusActive, false, false)
}

func (store *Store) CancelPlannedOperation(ctx context.Context, id string) error {
	return store.transitionOperation(ctx, id, operation.StatusPlanned, operation.StatusCancelled, true, true)
}

func (store *Store) MarkOperationNeedsAttention(ctx context.Context, id string) error {
	return store.transitionOperation(ctx, id, operation.StatusActive, operation.StatusNeedsAttention, true, false)
}

func (store *Store) transitionOperation(ctx context.Context, id string, from, to operation.Status, repeat, release bool) error {
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start operation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reconcileOperationClaims(ctx, tx); err != nil {
		return fmt.Errorf("reconcile pending operation claims: %w", err)
	}
	count, err := tx.Operation.Update().Where(operation.OperationKeyEQ(id), operation.StatusEQ(from)).SetStatus(to).Save(ctx)
	if err != nil {
		return fmt.Errorf("transition operation: %w", err)
	}
	if count != 1 {
		if !repeat {
			return fmt.Errorf("operation is not in expected state")
		}
		alreadyTransitioned, err := tx.Operation.Query().Where(operation.OperationKeyEQ(id), operation.StatusEQ(to)).Exist(ctx)
		if err != nil {
			return fmt.Errorf("inspect operation: %w", err)
		}
		if !alreadyTransitioned {
			return fmt.Errorf("operation is not in expected state")
		}
	}
	if release {
		if _, err := tx.OperationClaim.Delete().Where(operationclaim.HasOperationWith(operation.OperationKeyEQ(id))).Exec(ctx); err != nil {
			return fmt.Errorf("release operation claims: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit operation transition: %w", err)
	}
	return nil
}
