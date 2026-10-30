package configapply

import (
	"context"
	"crypto/sha256"
	"math"
	"strconv"
	"time"
)

func (state *codexEngineState) checkCodexRoots(target *nativeDirectory) error {
	if state.home.check() != nil || state.state.check() != nil || state.vault.check() != nil || target.check() != nil || state.guard == nil || state.guard.check() != nil {
		return ErrCodexAttention
	}
	return nil
}

func (state *codexEngineState) prepareCodexOperation(ctx context.Context, operation *codexOperation, bodies [3][]byte) (resultError error) {
	if err := state.checkCodexContext(ctx); err != nil {
		return err
	}
	if operation == nil || !codexOperationID(operation.ID) || operation.Write[1] {
		return ErrCodexInvalid
	}
	if operation.Owner != operation.Base.Owner || operation.Origin != operation.Base.Origin || operation.Profile != operation.Base.Profile || operation.Base.Device != state.inventory.Device {
		return ErrCodexInvalid
	}
	existing := state.inventory.Operations[operation.ID]
	if existing != nil && (existing.Candidate.Present || existing.Binding != operation.Binding || existing.Base != operation.Base) {
		return ErrCodexConflict
	}
	if existing == nil && len(state.inventory.Operations) >= codexOperationLimit {
		return ErrCodexCapacity
	}
	if err := state.validateSelection(operation.Base); err != nil {
		return err
	}
	if operation.Base.Revision == math.MaxUint64 || operation.Base.Confirmation == math.MaxUint64 {
		return ErrCodexCapacity
	}
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
	if !sameDirectory(target.identity, operation.Target) || images != operation.Before {
		return ErrCodexConflict
	}
	if err := state.checkCodexRoots(target); err != nil {
		return err
	}
	if !state.now().Before(timeFromCodex(operation.Expires)) {
		return ErrCodexExpired
	}
	for _, pending := range state.inventory.Operations {
		if sameDirectory(pending.Target, operation.Target) && (pending.Owner != operation.Owner || pending.Origin != operation.Origin || pending.Profile != operation.Profile) {
			return ErrCodexConflict
		}
		if pending.ID != operation.ID && pending.State != CodexCompleted && pending.State != CodexRestored && pending.State != CodexAbandonedBeforeWrite {
			return ErrCodexConflict
		}
	}
	if existing != nil {
		operation.VaultReservation = existing.VaultReservation
		operation.TargetReservation = existing.TargetReservation
	}
	if err := state.reserveCodexOperation(target, operation); err != nil {
		return err
	}
	if err := state.checkpointContext(ctx, "operation-reserved"); err != nil {
		return err
	}
	for index := range bodies {
		if len(bodies[index]) > codexRoleLimit {
			return ErrCodexCapacity
		}
		if operation.Write[index] {
			if !operation.After[index].Present && len(bodies[index]) != 0 || operation.After[index].Present && operation.After[index].SHA256 != sha256.Sum256(bodies[index]) {
				return ErrCodexInvalid
			}
		} else if operation.After[index] != operation.Before[index] {
			return ErrCodexInvalid
		}
	}
	clear(originals[1])
	originals[1] = nil
	candidateBodies := codexCopyBodies(bodies)
	defer codexClearBodies(&candidateBodies)
	clear(candidateBodies[1])
	candidateBodies[1] = nil
	operation.Backup, err = state.writeCodexBundle("backup", operation, originals)
	if err != nil {
		return err
	}
	operation.Candidate, err = state.writeCodexBundle("candidate", operation, candidateBodies)
	if err != nil {
		return err
	}
	operation.State = CodexCandidatesDurable
	for index := range operation.Write {
		if operation.Write[index] {
			operation.StageNames[index] = "codex-" + operation.ID + "-" + strconv.Itoa(index) + ".stage"
		}
	}
	state.inventory.Operations[operation.ID] = operation
	if err := state.saveCodexInventory(); err != nil {
		return err
	}
	if err := state.checkpointContext(ctx, "candidates-durable"); err != nil {
		return err
	}
	for _, index := range []int{2, 0} {
		if err := state.checkCodexContext(ctx); err != nil {
			return err
		}
		if !operation.Write[index] || !operation.After[index].Present {
			continue
		}
		stage, err := target.write(operation.StageNames[index], bodies[index])
		if err != nil {
			return err
		}
		operation.Stages[index], operation.After[index] = stage, stage
		if err := state.saveCodexInventory(); err != nil {
			return err
		}
		if err := state.checkpointContext(ctx, "stage-durable-"+strconv.Itoa(index)); err != nil {
			return err
		}
	}
	return nil
}

func (state *codexEngineState) publishCodex(ctx context.Context, operation *codexOperation) (result CodexResult, resultError error) {
	result = codexOperationResult(operation)
	if operation.State == CodexCompleted || operation.State == CodexRestored {
		return result, nil
	}
	if err := state.checkCodexContext(ctx); err != nil {
		return result, err
	}
	if err := state.validateSelection(operation.Base); err != nil {
		return result, err
	}
	if !state.now().Before(timeFromCodex(operation.Expires)) {
		return result, ErrCodexExpired
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
	if !sameDirectory(target.identity, operation.Target) {
		return result, ErrCodexConflict
	}
	if err := state.validateCodexMix(operation, images); err != nil {
		return result, err
	}
	if err := state.validateCodexStages(target, operation, images); err != nil {
		return result, err
	}
	if err := state.checkCodexRoots(target); err != nil {
		return result, err
	}
	for _, index := range []int{2, 0} {
		if !operation.Write[index] || images[index] == operation.After[index] {
			continue
		}
		if err := state.checkCodexContext(ctx); err != nil {
			return codexOperationResult(operation), err
		}
		if !state.now().Before(timeFromCodex(operation.Expires)) {
			return codexOperationResult(operation), ErrCodexExpired
		}
		if err := state.checkCodexRoots(target); err != nil {
			return codexOperationResult(operation), err
		}
		if err := state.validateCodexNow(operation); err != nil {
			return codexOperationResult(operation), err
		}
		operation.State = CodexPublishingCatalog
		if index == 0 {
			operation.State = CodexPublishingConfig
		}
		if operation.Restoring {
			operation.State = CodexRestoring
		}
		if err := state.saveCodexInventory(); err != nil {
			return codexOperationResult(operation), err
		}
		if err := state.checkpointContext(ctx, "before-publish-"+strconv.Itoa(index)); err != nil {
			return codexOperationResult(operation), err
		}
		if err := state.checkCodexRoots(target); err != nil {
			return codexOperationResult(operation), err
		}
		stageName := operation.StageNames[index]
		if operation.After[index].Present {
			stage, data, err := target.read(stageName, codexRoleLimit)
			clear(data)
			if err != nil || !stage.Present || stage != operation.Stages[index] || stage != operation.After[index] {
				return codexOperationResult(operation), ErrCodexAttention
			}
			if operation.Before[index].Present {
				if err := target.exchange(stageName, codexRoleNames[index]); err != nil {
					return codexOperationResult(operation), err
				}
				displaced, data, err := target.read(stageName, codexRoleLimit)
				clear(data)
				if err != nil || displaced != operation.Before[index] {
					return codexOperationResult(operation), ErrCodexUnknown
				}
			} else if err := target.rename(stageName, target, codexRoleNames[index]); err != nil {
				return codexOperationResult(operation), err
			}
		} else {
			if !operation.Before[index].Present {
				return codexOperationResult(operation), ErrCodexInvalid
			}
			if err := target.rename(codexRoleNames[index], target, stageName); err != nil {
				return codexOperationResult(operation), err
			}
			displaced, data, err := target.read(stageName, codexRoleLimit)
			clear(data)
			if err != nil || displaced != operation.Before[index] {
				return codexOperationResult(operation), ErrCodexUnknown
			}
		}
		if err := state.checkpoint("entry-published-" + strconv.Itoa(index)); err != nil {
			return codexOperationResult(operation), err
		}
		if operation.After[index].Present {
			if err := target.syncImage(codexRoleNames[index], operation.After[index]); err != nil {
				return codexOperationResult(operation), err
			}
		}
		if err := target.sync(); err != nil {
			return codexOperationResult(operation), err
		}
		if err := state.checkpointContext(ctx, "directory-synced-"+strconv.Itoa(index)); err != nil {
			return codexOperationResult(operation), err
		}
		if err := state.validateCodexNow(operation); err != nil {
			return codexOperationResult(operation), err
		}
		images[index] = operation.After[index]
	}
	if err := state.checkCodexRoots(target); err != nil {
		return codexOperationResult(operation), err
	}
	current, err := state.codexActualImages(target)
	if err != nil || current != operation.After {
		return codexOperationResult(operation), ErrCodexUnknown
	}
	for _, index := range []int{2, 0} {
		if !operation.Write[index] {
			continue
		}
		stage, data, err := target.read(operation.StageNames[index], codexRoleLimit)
		clear(data)
		if err != nil {
			return codexOperationResult(operation), err
		}
		if stage.Present {
			if stage != operation.Before[index] {
				return codexOperationResult(operation), ErrCodexAttention
			}
			if err := target.removeOwn(operation.StageNames[index], stage); err != nil {
				return codexOperationResult(operation), err
			}
		}
	}
	if err := state.checkpointContext(ctx, "targets-durable"); err != nil {
		return codexOperationResult(operation), err
	}
	if err := state.releaseCodexReservations(target, operation); err != nil {
		return codexOperationResult(operation), err
	}
	if err := state.checkCodexContext(ctx); err != nil {
		return codexOperationResult(operation), err
	}
	if err := state.finishCodex(operation); err != nil {
		return codexOperationResult(operation), err
	}
	return codexOperationResult(operation), nil
}

func (state *codexEngineState) codexActualImages(target *nativeDirectory) ([3]nativeImage, error) {
	var images [3]nativeImage
	for index, name := range codexRoleNames {
		image, data, err := target.read(name, codexRoleLimit)
		clear(data)
		if err != nil {
			return images, err
		}
		images[index] = image
	}
	return images, nil
}

func (state *codexEngineState) validateCodexNow(operation *codexOperation) (resultError error) {
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
	if !sameDirectory(target.identity, operation.Target) {
		return ErrCodexConflict
	}
	return state.validateCodexMix(operation, images)
}

func (state *codexEngineState) validateCodexMix(operation *codexOperation, images [3]nativeImage) error {
	if operation.ReservationsReleased && images != operation.After {
		return ErrCodexAttention
	}
	for index := range images {
		if images[index] != operation.Before[index] && (!operation.Write[index] || images[index] != operation.After[index]) {
			return ErrCodexConflict
		}
	}
	return nil
}

func (state *codexEngineState) finishCodex(operation *codexOperation) error {
	if err := state.validateSelection(operation.Base); err != nil {
		return err
	}
	previousState := operation.State
	key := codexSelectionKey(operation.Origin, operation.Owner, operation.Profile)
	previousSelection, existed := state.inventory.Selections[key]
	selection := operation.Base
	selection.Revision++
	selection.Confirmation++
	selection.Asset = operation.Asset
	selection.Operation = operation.ID
	operation.State = CodexCompleted
	if operation.Restoring {
		selection.Asset = ""
		operation.State = CodexRestored
	}
	state.inventory.Selections[codexSelectionKey(selection.Origin, selection.Owner, selection.Profile)] = selection
	if err := state.saveCodexInventory(); err != nil {
		operation.State = previousState
		if existed {
			state.inventory.Selections[key] = previousSelection
		} else {
			delete(state.inventory.Selections, key)
		}
		return err
	}
	return state.checkpoint("completed-durable")
}

func (state *codexEngineState) validateCodexStages(target *nativeDirectory, operation *codexOperation, images [3]nativeImage) error {
	for index, writes := range operation.Write {
		if !writes {
			continue
		}
		actual, body, err := target.read(operation.StageNames[index], codexRoleLimit)
		clear(body)
		if err != nil {
			return err
		}
		if !actual.Present {
			continue
		}
		wanted := operation.Stages[index]
		if images[index] == operation.After[index] {
			wanted = operation.Before[index]
		}
		if actual != wanted {
			return ErrCodexAttention
		}
	}
	return nil
}

func codexOperationResult(operation *codexOperation) CodexResult {
	if operation == nil {
		return CodexResult{}
	}
	result := CodexResult{OperationID: operation.ID, State: operation.State, NeedsAttention: operation.State == CodexNeedsAttention, SelectionRevision: operation.Base.Revision, ConfirmationRevision: operation.Base.Confirmation}
	if operation.State == CodexCompleted || operation.State == CodexRestored {
		result.SelectionRevision++
		result.ConfirmationRevision++
	}
	return result
}

func timeFromCodex(value int64) time.Time { return time.Unix(0, value) }

func (state *codexEngineState) reserveCodexOperation(target *nativeDirectory, operation *codexOperation) error {
	if operation.VaultReservation.Present && operation.TargetReservation.Present {
		return state.checkCodexReservations(target, operation)
	}
	total := int64(0)
	for _, record := range state.inventory.Operations {
		total += record.Backup.Identity.Size + record.Candidate.Identity.Size
		if !record.ReservationsReleased {
			total += record.VaultReservation.Identity.Size
		}
	}
	if total+4*codexBundleLimit > codexVaultLimit {
		return ErrCodexCapacity
	}
	var err error
	operation.VaultReservation, err = state.vault.reserveAt(operation.ID+".reserve", 2*codexBundleLimit)
	if err != nil {
		return err
	}
	operation.TargetReservation, err = target.reserveAt("codex-"+operation.ID+".reserve", 2*codexRoleLimit)
	if err != nil {
		return err
	}
	state.inventory.Operations[operation.ID] = operation
	return state.saveCodexInventory()
}

func (state *codexEngineState) checkCodexReservations(target *nativeDirectory, operation *codexOperation) error {
	for _, item := range []struct {
		directory *nativeDirectory
		name      string
		image     nativeImage
	}{{state.vault, operation.ID + ".reserve", operation.VaultReservation}, {target, "codex-" + operation.ID + ".reserve", operation.TargetReservation}} {
		actual, body, err := item.directory.read(item.name, 4<<20)
		clear(body)
		if err != nil || actual != item.image || !actual.Present || operation.ReservationsReleased {
			return ErrCodexCapacity
		}
	}
	return nil
}

func (state *codexEngineState) releaseCodexReservations(target *nativeDirectory, operation *codexOperation) error {
	if !operation.ReservationsReleased {
		operation.ReservationsReleased = true
		if err := state.saveCodexInventory(); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		directory *nativeDirectory
		name      string
		image     nativeImage
	}{{state.vault, operation.ID + ".reserve", operation.VaultReservation}, {target, "codex-" + operation.ID + ".reserve", operation.TargetReservation}} {
		if !item.image.Present {
			continue
		}
		actual, body, err := item.directory.read(item.name, 4<<20)
		clear(body)
		if err != nil {
			return err
		}
		if actual.Present {
			if actual != item.image {
				return ErrCodexAttention
			}
			if err := item.directory.removeOwn(item.name, actual); err != nil {
				return err
			}
		}
	}
	return nil
}
