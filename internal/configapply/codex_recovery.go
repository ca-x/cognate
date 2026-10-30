package configapply

import (
	"context"
	"crypto/sha256"
	"maps"
	"slices"
	"time"

	"github.com/ca-x/cognate/internal/codexconfig"
)

type codexRecoveryData struct {
	engine    *codexEngineState
	id        string
	images    [3]nativeImage
	target    nativeIdentity
	base      codexSelectionRecord
	expires   time.Time
	confirmed bool
	used      bool
	restore   bool
	operation string
	view      SafeCodexPlan
}

func (engine *CodexEngine) StatusCodex(ctx context.Context, operationID string) (CodexResult, error) {
	state := engine.private()
	if !codexOperationID(operationID) {
		return CodexResult{}, ErrCodexInvalid
	}
	var result CodexResult
	err := state.withLock(ctx, func(ctx context.Context) error {
		operation := state.inventory.Operations[operationID]
		if operation == nil {
			return ErrCodexInvalid
		}
		var err error
		result, err = state.inspectCodex(ctx, operation)
		return err
	})
	return result, err
}

func (engine *CodexEngine) ReconcileCodex(ctx context.Context) ([]CodexResult, error) {
	state := engine.private()
	var results []CodexResult
	err := state.withLock(ctx, func(ctx context.Context) error {
		for _, id := range slices.Sorted(maps.Keys(state.inventory.Operations)) {
			result, err := state.inspectCodex(ctx, state.inventory.Operations[id])
			if err != nil {
				return err
			}
			results = append(results, result)
		}
		return nil
	})
	return results, err
}

func (state *codexEngineState) inspectCodex(ctx context.Context, operation *codexOperation) (result CodexResult, resultError error) {
	result = codexOperationResult(operation)
	if err := state.checkCodexContext(ctx); err != nil {
		return result, err
	}
	if operation.State == CodexAbandonedBeforeWrite {
		if err := state.cleanupCodexAbandoned(ctx, operation); err != nil {
			return result, err
		}
		return result, nil
	}
	if operation.State == CodexRetiredUnpublished {
		return result, nil
	}
	if !operation.Candidate.Present {
		if operation.LifecycleVersion == 1 {
			result.RemoteOutcomeUnknown = operation.State != CodexPreviewed
			return result, nil
		}
		result.State = CodexRemoteOutcomeUnknown
		result.RemoteOutcomeUnknown = true
		return result, nil
	}
	if operation.State == CodexCompleted || operation.State == CodexRestored {
		return result, nil
	}
	if err := state.validateCodexOperationObservation(ctx, operation); err != nil {
		return result, err
	}
	target, images, bodies, err := state.observeCodex()
	defer codexClearBodies(&bodies)
	if err != nil {
		return result, err
	}
	defer func() {
		if target.close() != nil {
			resultError = ErrCodexUnknown
		}
	}()
	if !sameDirectory(target.identity, operation.Target) || state.validateCodexMix(operation, images) != nil || state.validateCodexStages(target, operation, images) != nil {
		result.State = CodexNeedsAttention
		result.NeedsAttention = true
		return result, nil
	}
	if images == operation.After {
		if err := state.validateSelection(operation.Base); err != nil {
			result.State = CodexNeedsAttention
			result.NeedsAttention = true
			return result, nil
		}
		if err := state.finishObservedCodex(ctx, target, operation); err != nil {
			return result, err
		}
		return codexOperationResult(operation), nil
	}
	result.State = CodexReconcileRequired
	return result, nil
}

func (state *codexEngineState) finishObservedCodex(ctx context.Context, target *nativeDirectory, operation *codexOperation) error {
	if err := state.checkCodexContext(ctx); err != nil {
		return err
	}
	if err := state.checkCodexRoots(target); err != nil {
		return err
	}
	for _, index := range []int{2, 0} {
		if !operation.Write[index] {
			continue
		}
		if operation.After[index].Present {
			if err := target.syncImage(codexRoleNames[index], operation.After[index]); err != nil {
				return err
			}
		}
		stage, data, err := target.read(operation.StageNames[index], codexRoleLimit)
		clear(data)
		if err != nil {
			return err
		}
		if stage.Present {
			if stage != operation.Before[index] {
				return ErrCodexAttention
			}
			if err := target.removeOwn(operation.StageNames[index], stage); err != nil {
				return err
			}
		}
	}
	if err := target.sync(); err != nil {
		return err
	}
	images, err := state.codexActualImages(target)
	if err != nil || images != operation.After {
		return ErrCodexAttention
	}
	if err := state.releaseCodexReservations(target, operation); err != nil {
		return err
	}
	if err := state.checkpointContext(ctx, "reconcile-targets-durable"); err != nil {
		return err
	}
	if err := state.validateCodexOperationObservation(ctx, operation); err != nil {
		return err
	}
	return state.finishCodex(operation)
}

func (state *codexEngineState) previewCodexRecovery(ctx context.Context, operationID string, restore bool) (result *codexRecoveryData, resultError error) {
	if err := state.checkCodexContext(ctx); err != nil {
		return nil, err
	}
	operation := state.inventory.Operations[operationID]
	if operation == nil || !operation.Candidate.Present {
		return nil, ErrCodexInvalid
	}
	expires, err := state.codexOperationObservationExpiry(ctx, operation)
	if err != nil {
		return nil, err
	}
	if restore && (operation.State != CodexCompleted || operation.Restoring) {
		return nil, ErrCodexInvalid
	}
	if !restore && (operation.State == CodexCompleted || operation.State == CodexRestored) {
		return nil, ErrCodexInvalid
	}
	target, images, bodies, err := state.observeCodex()
	defer codexClearBodies(&bodies)
	if err != nil {
		return nil, err
	}
	defer func() {
		if target.close() != nil {
			result = nil
			resultError = ErrCodexUnknown
		}
	}()
	if !sameDirectory(target.identity, operation.Target) || state.validateCodexMix(operation, images) != nil || state.validateCodexStages(target, operation, images) != nil {
		return nil, ErrCodexConflict
	}
	base := operation.Base
	if restore {
		if images != operation.After {
			return nil, ErrCodexConflict
		}
		base = state.inventory.Selections[codexSelectionKey(operation.Origin, operation.Owner, operation.Profile)]
		if base.Operation != operation.ID || base.Asset != operation.Asset {
			return nil, ErrCodexConflict
		}
	} else if err := state.validateSelection(base); err != nil {
		return nil, err
	}
	next := operationID
	if restore {
		next = codexRandomID()
	}
	data := &codexRecoveryData{engine: state, id: operationID, images: images, target: target.identity, base: base, expires: expires, restore: restore, operation: next}
	data.view = SafeCodexPlan{OperationID: next, AssetID: operation.Asset, C0ProfileID: operation.Profile, SelectionRevision: base.Revision + 1, ConfirmationRevision: base.Confirmation + 1, ExpiresAt: data.expires, RecoverableWindow: true}
	for index, writes := range operation.Write {
		if writes {
			data.view.Changes = append(data.view.Changes, CodexSafeChange{Role: []codexconfig.Role{codexconfig.ConfigRole, codexconfig.AuthRole, codexconfig.CatalogRole}[index], Action: codexconfig.Write, Count: 1})
		}
	}
	return data, nil
}

func (engine *CodexEngine) PreviewCodexRecovery(ctx context.Context, operationID string) (CodexRecoveryPlan, error) {
	state := engine.private()
	var result CodexRecoveryPlan
	err := state.withLock(ctx, func(ctx context.Context) error {
		data, err := state.previewCodexRecovery(ctx, operationID, false)
		if err != nil {
			return err
		}
		result = CodexRecoveryPlan{value: new(any(data))}
		return nil
	})
	return result, err
}

func (engine *CodexEngine) PreviewCodexRestore(ctx context.Context, operationID string) (CodexRestorePlan, error) {
	state := engine.private()
	var result CodexRestorePlan
	err := state.withLock(ctx, func(ctx context.Context) error {
		data, err := state.previewCodexRecovery(ctx, operationID, true)
		if err != nil {
			return err
		}
		result = CodexRestorePlan{value: new(any(data))}
		return nil
	})
	return result, err
}

func (state *codexEngineState) validateRecovery(ctx context.Context, data *codexRecoveryData) (resultError error) {
	if err := state.checkCodexContext(ctx); err != nil {
		return err
	}
	if data == nil || data.engine != state || data.used {
		return ErrCodexInvalid
	}
	if !state.now().Before(data.expires) {
		return ErrCodexExpired
	}
	if err := state.validateCodexOperationObservation(ctx, state.inventory.Operations[data.id]); err != nil {
		return err
	}
	if err := state.validateSelection(data.base); err != nil {
		return err
	}
	target, images, bodies, err := state.observeCodex()
	defer codexClearBodies(&bodies)
	if err != nil {
		return err
	}
	defer func() {
		if target.close() != nil {
			resultError = ErrCodexUnknown
		}
	}()
	if !sameDirectory(target.identity, data.target) || images != data.images {
		return ErrCodexConflict
	}
	return nil
}

func (plan CodexRecoveryPlan) Confirm(ctx context.Context, finalApproved bool) (CodexRecoveryPlan, error) {
	data := plan.get[codexRecoveryData]()
	if data == nil || !finalApproved || data.restore {
		return CodexRecoveryPlan{}, ErrCodexInvalid
	}
	var result CodexRecoveryPlan
	err := data.engine.withLock(ctx, func(ctx context.Context) error {
		if err := data.engine.validateRecovery(ctx, data); err != nil {
			return err
		}
		owned := *data
		owned.confirmed = true
		result = CodexRecoveryPlan{value: new(any(&owned))}
		return nil
	})
	return result, err
}

func (plan CodexRestorePlan) Confirm(ctx context.Context, finalApproved bool) (CodexRestorePlan, error) {
	data := plan.get[codexRecoveryData]()
	if data == nil || !finalApproved || !data.restore {
		return CodexRestorePlan{}, ErrCodexInvalid
	}
	var result CodexRestorePlan
	err := data.engine.withLock(ctx, func(ctx context.Context) error {
		if err := data.engine.validateRecovery(ctx, data); err != nil {
			return err
		}
		owned := *data
		owned.confirmed = true
		result = CodexRestorePlan{value: new(any(&owned))}
		return nil
	})
	return result, err
}

func (engine *CodexEngine) RecoverCodex(ctx context.Context, plan CodexRecoveryPlan) (CodexResult, error) {
	state := engine.private()
	data := plan.get[codexRecoveryData]()
	if data == nil || !data.confirmed || data.restore || data.engine != state {
		return CodexResult{}, ErrCodexInvalid
	}
	var result CodexResult
	err := state.withLock(ctx, func(ctx context.Context) error {
		if err := state.validateRecovery(ctx, data); err != nil {
			return err
		}
		data.used = true
		operation := state.inventory.Operations[data.id]
		if operation == nil {
			return ErrCodexInvalid
		}
		operation.Expires = data.expires.UnixNano()
		if err := state.completeCodexStages(ctx, operation); err != nil {
			return err
		}
		if err := state.validateCodexOperationObservation(ctx, operation); err != nil {
			return err
		}
		var err error
		result, err = state.publishCodex(ctx, operation)
		return err
	})
	return result, err
}

func (state *codexEngineState) completeCodexStages(ctx context.Context, operation *codexOperation) (resultError error) {
	if err := state.checkpointContext(ctx, "recovery-stages-start"); err != nil {
		return err
	}
	bodies, err := state.readCodexBundle("candidate", operation)
	if err != nil {
		return err
	}
	defer codexClearBodies(&bodies)
	target, images, originals, err := state.observeCodex()
	defer codexClearBodies(&originals)
	if err != nil {
		return err
	}
	defer func() {
		if target.close() != nil {
			resultError = ErrCodexUnknown
		}
	}()
	if !sameDirectory(target.identity, operation.Target) || state.validateCodexMix(operation, images) != nil {
		return ErrCodexConflict
	}
	for _, index := range []int{2, 0} {
		if err := state.checkCodexContext(ctx); err != nil {
			return err
		}
		if !operation.Write[index] || !operation.After[index].Present || images[index] == operation.After[index] || operation.Stages[index].Present {
			continue
		}
		if sha256.Sum256(bodies[index]) != operation.After[index].SHA256 {
			return ErrCodexAttention
		}
		stage, err := target.write(operation.StageNames[index], bodies[index])
		if err != nil {
			return err
		}
		operation.Stages[index], operation.After[index] = stage, stage
		if err := state.saveCodexInventory(); err != nil {
			return err
		}
	}
	return state.checkpointContext(ctx, "recovery-stages-durable")
}

func (engine *CodexEngine) RestoreCodex(ctx context.Context, plan CodexRestorePlan) (CodexResult, error) {
	state := engine.private()
	data := plan.get[codexRecoveryData]()
	if data == nil || !data.confirmed || !data.restore || data.engine != state {
		return CodexResult{}, ErrCodexInvalid
	}
	var result CodexResult
	err := state.withLock(ctx, func(ctx context.Context) error {
		if err := state.validateRecovery(ctx, data); err != nil {
			return err
		}
		data.used = true
		source := state.inventory.Operations[data.id]
		if source == nil || source.State != CodexCompleted {
			return ErrCodexInvalid
		}
		bodies, err := state.readCodexBundle("backup", source)
		if err != nil {
			return err
		}
		defer codexClearBodies(&bodies)
		operation := &codexOperation{ID: data.operation, Owner: source.Owner, Origin: source.Origin, Profile: source.Profile, Source: source.ID, Binding: source.Binding, Target: data.target, Before: data.images, After: data.images, Write: source.Write, Base: data.base, Expires: data.expires.UnixNano(), Restoring: true, State: CodexRestorePrepared}
		operation.ProfileIdentity = source.ProfileIdentity
		operation.ManagedModels = slices.Clone(source.ManagedModels)
		operation.ManagedModelsVersion = source.ManagedModelsVersion
		if source.Observation != nil {
			operation.Observation = new(codexCopyLaunchDescriptor(*source.Observation))
		}
		for index := range operation.Write {
			if operation.Write[index] {
				operation.After[index] = nativeImage{Present: source.Before[index].Present}
				if operation.After[index].Present {
					operation.After[index].SHA256 = sha256.Sum256(bodies[index])
				}
			}
		}
		if err := state.prepareCodexOperation(ctx, operation, bodies); err != nil {
			return err
		}
		if err := state.validateCodexOperationObservation(ctx, operation); err != nil {
			return err
		}
		result, err = state.publishCodex(ctx, operation)
		return err
	})
	return result, err
}

func (plan CodexRecoveryPlan) View() SafeCodexPlan {
	if data := plan.get[codexRecoveryData](); data != nil {
		view := data.view
		view.Changes = slices.Clone(view.Changes)
		return view
	}
	return SafeCodexPlan{}
}
func (plan CodexRestorePlan) View() SafeCodexPlan {
	if data := plan.get[codexRecoveryData](); data != nil {
		view := data.view
		view.Changes = slices.Clone(view.Changes)
		return view
	}
	return SafeCodexPlan{}
}
