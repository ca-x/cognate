package configapply

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func sourceRestored(inventory *nativeInventory, operationID string) bool {
	for _, operation := range inventory.operations {
		if operation.Plan.SourceOperation == operationID && operation.Phase == "Restored" {
			return true
		}
	}
	return false
}

func (engine *Engine) PreviewRestore(ctx context.Context, appliedOperationID string) (*Plan, error) {
	if !nativeIdentifier(appliedOperationID) {
		return nil, ErrSelection
	}
	var plan *Plan
	err := engine.run(ctx, func(inventory *nativeInventory) error {
		source := inventory.operations[appliedOperationID]
		if source == nil || source.Phase != "AppliedOnDisk" || sourceRestored(inventory, appliedOperationID) {
			return ErrConflict
		}
		if _, err := engine.operationBackup(inventory, source); err != nil {
			return err
		}
		for _, operation := range inventory.operations {
			if !terminalPhase(operation.Phase) && operation.Phase != "Planned" {
				return ErrPending
			}
		}
		parent, err := engine.private().home.child(".claude", false)
		if err != nil {
			return err
		}
		defer parent.close()
		base, data, err := parent.read("settings.json", agentconfig.MaxConfigBytes)
		clear(data)
		if err != nil || base != source.Stage || !sameDirectory(parent.identity, source.Plan.Parent) {
			return ErrConflict
		}
		selection, exists := inventory.selections[selectionKey(source.Plan.Selection)]
		if !exists {
			return ErrConflict
		}
		selection.SelectionInput = cloneSelection(selection.SelectionInput)
		now := engine.private().now().UTC()
		selection.OperationID = uuid.New().String()
		selection.PreparationPreparedAt, selection.PreparationExpiresAt, selection.NativeSessionExpiresAt = now, now.Add(5*time.Minute), nil
		plan = newNativePlan(planData{Selection: selection, Kind: "restore", SourceOperation: appliedOperationID, Parent: parent.identity, Base: base, ExpiresAt: now.Add(5 * time.Minute)})
		plan.private().data.SourceCommitment = source.Commitment
		plan.private().data.PreviewedAt = now
		plan.private().commitment = nativeMAC(engine.private().keys.intent, nativePlanBytes(plan.private().data))
		operation := &operationRecord{Plan: plan.private().data, Commitment: plan.private().commitment, Phase: "Planned"}
		return engine.appendRecord(nativeRecord{Type: "operation", ID: plan.OperationID(), Selection: selection, Operation: operation}, inventory)
	})
	return plan, err
}

func (engine *Engine) Restore(ctx context.Context, plan *Plan) (SafeResult, error) {
	var result SafeResult
	err := engine.run(ctx, func(inventory *nativeInventory) error {
		operation, err := engine.validatePlan(plan, inventory, "restore")
		if err != nil {
			return err
		}
		result.OperationID = plan.OperationID()
		if terminalPhase(operation.Phase) {
			result = operationResult(inventory, operation)
			return nil
		}
		source := inventory.operations[plan.private().data.SourceOperation]
		if source == nil || source.Commitment != plan.private().data.SourceCommitment || source.Phase != "AppliedOnDisk" || sourceRestored(inventory, plan.private().data.SourceOperation) {
			return ErrConflict
		}
		backup, err := engine.operationBackup(inventory, source)
		if err != nil {
			return err
		}
		parent, err := engine.private().home.child(".claude", false)
		if err != nil {
			return err
		}
		defer parent.close()
		base, data, err := parent.read("settings.json", agentconfig.MaxConfigBytes)
		defer clear(data)
		if err != nil || base != plan.private().data.Base || base != source.Stage || !sameDirectory(parent.identity, plan.private().data.Parent) {
			return ErrConflict
		}
		result, err = engine.publish(ctx, inventory, operation, parent, data, backup.Data, !backup.Image.Present)
		if err == nil {
			err = engine.pauseAfterRestore(inventory, operation)
		}
		return err
	})
	if errors.Is(err, ErrOutcomeUnknown) {
		result.Status = "OutcomeUnknown"
	}
	return result, err
}

func (engine *Engine) pauseAfterRestore(inventory *nativeInventory, operation *operationRecord) error {
	key := selectionKey(operation.Plan.Selection)
	selection, exists := inventory.selections[key]
	if !exists {
		return ErrStateInvalid
	}
	if selection.SelectionRevision > operation.Plan.Selection.SelectionRevision {
		return nil
	}
	selection.SelectionRevision++
	selection.PolicyRevision++
	selection.AutoApply = false
	if err := engine.appendRecord(nativeRecord{Type: "selection", ID: key, Selection: selection}, inventory); err != nil {
		return err
	}
	inventory.selections[key] = selection
	return nil
}
