package configapply

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func (engine *Engine) Status(ctx context.Context, operationID string) (SafeResult, error) {
	if !nativeIdentifier(operationID) {
		return SafeResult{}, ErrSelection
	}
	var result SafeResult
	err := engine.run(ctx, func(inventory *nativeInventory) error {
		operation := inventory.operations[operationID]
		if operation == nil {
			return ErrSelection
		}
		status := operation.Phase
		if !terminalPhase(status) && status != "Planned" && status != "NeedsAttention" {
			status = "OutcomeUnknown"
		}
		for _, restore := range inventory.operations {
			if restore.Plan.SourceOperation == operationID && restore.Phase == "Restored" {
				status = "Restored"
			}
		}
		result = operationResult(inventory, operation)
		result.Status = status
		return nil
	})
	return result, err
}

func (engine *Engine) Reconcile(ctx context.Context) ([]SafeResult, error) {
	var results []SafeResult
	err := engine.run(ctx, func(inventory *nativeInventory) error {
		identifiers := make([]string, 0, len(inventory.operations))
		for identifier := range inventory.operations {
			identifiers = append(identifiers, identifier)
		}
		slices.Sort(identifiers)
		for _, identifier := range identifiers {
			if err := ctx.Err(); err != nil {
				return err
			}
			result, err := engine.reconcileOperation(ctx, inventory, inventory.operations[identifier])
			current := inventory.selections[selectionKey(inventory.operations[identifier].Plan.Selection)]
			result.SelectionRevision, result.PolicyRevision = current.SelectionRevision, current.PolicyRevision
			results = append(results, result)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return results, err
}

func operationResult(inventory *nativeInventory, operation *operationRecord) SafeResult {
	selection := inventory.selections[selectionKey(operation.Plan.Selection)]
	return SafeResult{OperationID: operation.Plan.Selection.OperationID, Status: operation.Phase, NeedsAttention: operation.Residue || operation.Phase == "NeedsAttention", SelectionRevision: selection.SelectionRevision, PolicyRevision: selection.PolicyRevision}
}

func (engine *Engine) attention(inventory *nativeInventory, operation *operationRecord) (SafeResult, error) {
	if terminalPhase(operation.Phase) {
		if !operation.Residue {
			operation.Residue = true
			if err := engine.saveOperation(operation, inventory, operation.Phase); err != nil {
				return SafeResult{}, err
			}
		}
		return SafeResult{OperationID: operation.Plan.Selection.OperationID, Status: operation.Phase, NeedsAttention: true}, nil
	}
	if !terminalPhase(operation.Phase) && operation.Phase != "NeedsAttention" {
		if err := engine.saveOperation(operation, inventory, "NeedsAttention"); err != nil {
			return SafeResult{OperationID: operation.Plan.Selection.OperationID, Status: "OutcomeUnknown"}, err
		}
	}
	return SafeResult{OperationID: operation.Plan.Selection.OperationID, Status: "NeedsAttention", NeedsAttention: true}, nil
}

func (engine *Engine) operationBackup(inventory *nativeInventory, operation *operationRecord) (*backupRecord, error) {
	record := inventory.backups[operation.BackupID]
	if record == nil || record.Selection.OperationID != operation.Plan.Selection.OperationID || !selectionSame(record.Selection, operation.Plan.Selection) || record.Backup.Image != operation.Plan.Base {
		return nil, ErrStateInvalid
	}
	return record.Backup, nil
}

func (engine *Engine) reconcileOperation(ctx context.Context, inventory *nativeInventory, operation *operationRecord) (SafeResult, error) {
	result := SafeResult{OperationID: operation.Plan.Selection.OperationID, Status: operation.Phase}
	if operation.Phase == "NeedsAttention" {
		result.NeedsAttention = true
		return result, nil
	}
	if operation.Phase == "Cancelled" && operation.Transaction == "" {
		return result, nil
	}
	if operation.Phase == "Planned" {
		if err := engine.saveOperation(operation, inventory, "Cancelled"); err != nil {
			return result, err
		}
		result.Status = "Cancelled"
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !strings.HasPrefix(operation.Transaction, "cognate-") || !nativeIdentifier(operation.Transaction) {
		return engine.attention(inventory, operation)
	}
	parent, err := engine.private().home.child(".claude", false)
	if err != nil {
		return engine.attention(inventory, operation)
	}
	defer parent.close()
	if !sameDirectory(parent.identity, operation.Plan.Parent) {
		return engine.attention(inventory, operation)
	}
	transaction, err := parent.child(operation.Transaction, false)
	if errors.Is(err, ErrTargetAbsent) && operation.Phase == "Staging" && operation.TransactionIdentity.Inode == 0 {
		if err := engine.saveOperation(operation, inventory, "Cancelled"); err != nil {
			return result, err
		}
		result.Status = "Cancelled"
		return result, nil
	}
	if errors.Is(err, ErrTargetAbsent) && terminalPhase(operation.Phase) {
		return result, nil
	}
	if err != nil {
		return engine.attention(inventory, operation)
	}
	defer transaction.close()
	if !sameDirectory(transaction.identity, operation.TransactionIdentity) || transaction.identity.Mode&0o077 != 0 {
		return engine.attention(inventory, operation)
	}
	if operation.Phase != "Staging" {
		if _, err := engine.operationBackup(inventory, operation); err != nil {
			return engine.attention(inventory, operation)
		}
	}
	if !terminalPhase(operation.Phase) && operation.Phase != "Staging" {
		capacity, data, err := engine.private().vault.read(operation.VaultCapacityName, 4<<20)
		clear(data)
		if err != nil || !capacity.Present || capacity != operation.VaultCapacity {
			return engine.attention(inventory, operation)
		}
	}
	current, data, err := parent.read("settings.json", agentconfig.MaxConfigBytes)
	clear(data)
	if err != nil {
		return engine.attention(inventory, operation)
	}
	holding, data, err := transaction.read("holding", agentconfig.MaxConfigBytes)
	clear(data)
	if err != nil || holding.Present && holding != operation.Plan.Base {
		return engine.attention(inventory, operation)
	}
	stage, data, err := transaction.read("stage", agentconfig.MaxConfigBytes)
	clear(data)
	if err != nil || stage.Present && stage != operation.Stage {
		return engine.attention(inventory, operation)
	}
	if operation.Phase == "AppliedOnDisk" || operation.Phase == "Restored" {
		if operation.Phase == "Restored" {
			if err := engine.pauseAfterRestore(inventory, operation); err != nil {
				return result, err
			}
		}
		if err := engine.cleanup(operation, parent, transaction); err != nil {
			return engine.attention(inventory, operation)
		}
		return result, nil
	}
	if terminalPhase(operation.Phase) {
		if err := engine.cleanup(operation, parent, transaction); err != nil {
			return engine.attention(inventory, operation)
		}
		return result, nil
	}
	if current.Present && current == operation.Stage || operation.RestoreAbsent && !current.Present && operation.Phase == "PublishIntent" && holding == operation.Plan.Base {
		if current.Present && stage.Present {
			return engine.attention(inventory, operation)
		}
		if operation.Plan.Base.Present && holding != operation.Plan.Base {
			return engine.attention(inventory, operation)
		}
		if current.Present {
			if err := parent.syncImage("settings.json", current); err != nil {
				return result, err
			}
		}
		phase := "AppliedOnDisk"
		if operation.Plan.Kind == "restore" {
			phase = "Restored"
		}
		if err := parent.syncWith(transaction); err != nil {
			return result, err
		}
		if err := engine.saveOperation(operation, inventory, phase); err != nil {
			return result, err
		}
		if phase == "Restored" {
			if err := engine.pauseAfterRestore(inventory, operation); err != nil {
				return result, err
			}
		}
		if err := engine.cleanup(operation, parent, transaction); err != nil {
			return engine.attention(inventory, operation)
		}
		result.Status = phase
		return result, nil
	}
	if current == operation.Plan.Base && !holding.Present {
		if err := engine.saveOperation(operation, inventory, "Cancelled"); err != nil {
			return result, err
		}
		if err := engine.cleanup(operation, parent, transaction); err != nil {
			return engine.attention(inventory, operation)
		}
		result.Status = "Cancelled"
		return result, nil
	}
	if !current.Present && holding == operation.Plan.Base && holding.Present {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := transaction.rename("holding", parent, "settings.json"); err != nil {
			return engine.attention(inventory, operation)
		}
		if err := parent.syncWith(transaction); err != nil {
			return result, err
		}
		restored, data, err := parent.read("settings.json", agentconfig.MaxConfigBytes)
		clear(data)
		if err != nil || restored != operation.Plan.Base {
			return engine.attention(inventory, operation)
		}
		if err := engine.saveOperation(operation, inventory, "RolledBack"); err != nil {
			return result, err
		}
		if err := engine.cleanup(operation, parent, transaction); err != nil {
			return engine.attention(inventory, operation)
		}
		result.Status = "RolledBack"
		return result, nil
	}
	return engine.attention(inventory, operation)
}
