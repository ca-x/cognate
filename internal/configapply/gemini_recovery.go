package configapply

import (
	"context"
	"crypto/sha256"
	"reflect"
	"slices"
	"strconv"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/geminiconfig"
)

type geminiRecoveryData struct {
	engine             *geminiEngineState
	source             geminiOperation
	observed           geminiObserved
	base               geminiSelectionRecord
	id, planID, action string
	view               SafeGeminiPlan
	backup, candidate  nativeImage
	confirmed, used    bool
}

func (data *geminiRecoveryData) destroy() {
	clear(data.source.StatusTicket)
	*data = geminiRecoveryData{}
}

func (state *geminiEngineState) ownedGeminiOperation(id, origin, owner, profile string) (*geminiOperation, error) {
	operation := state.inventory.Operations[id]
	if !codexOperationID(id) || operation == nil || operation.Origin != origin || operation.Owner != owner || operation.Profile != profile {
		return nil, ErrGeminiConflict
	}
	return operation, nil
}

func (plan GeminiRecoveryPlan) View() SafeGeminiPlan {
	if data := plan.get[geminiRecoveryData](); data != nil {
		view := data.view
		view.Changes, view.RequiredSlots = slices.Clone(view.Changes), slices.Clone(view.RequiredSlots)
		return view
	}
	return SafeGeminiPlan{}
}

func (state *geminiEngineState) validateGeminiRecovery(ctx context.Context, data *geminiRecoveryData) (*geminiOperation, error) {
	if data == nil || data.engine != state || data.used {
		return nil, ErrGeminiInvalid
	}
	if !state.now().Before(data.observed.expires) {
		return nil, ErrGeminiExpired
	}
	operation, err := state.ownedGeminiOperation(data.source.ID, data.source.Origin, data.source.Owner, data.source.Profile)
	if err != nil || !reflect.DeepEqual(operation, &data.source) {
		return nil, ErrGeminiConflict
	}
	if err := state.validateSelection(data.base); err != nil {
		return nil, err
	}
	if err := state.validateObservation(ctx, &data.observed, data.observed.baseline, nil); err != nil {
		return nil, err
	}
	return operation, nil
}

func (engine *GeminiEngine) ConfirmGeminiRecovery(ctx context.Context, plan GeminiRecoveryPlan, planID string, slots []agentconfig.GeminiSecretSlot, final bool) error {
	state, data := engine.private(), plan.get[geminiRecoveryData]()
	return state.withLock(ctx, func(ctx context.Context) error {
		if _, err := state.validateGeminiRecovery(ctx, data); err != nil {
			return err
		}
		if data.confirmed || planID != data.planID || !final || !slices.Equal(slots, data.view.RequiredSlots) {
			return ErrGeminiDenied
		}
		data.confirmed = true
		return nil
	})
}

func (engine *GeminiEngine) ExecuteGeminiRecovery(ctx context.Context, plan GeminiRecoveryPlan) (GeminiResult, error) {
	state, data := engine.private(), plan.get[geminiRecoveryData]()
	var result GeminiResult
	err := state.withLock(ctx, func(ctx context.Context) error {
		operation, err := state.validateGeminiRecovery(ctx, data)
		if err != nil {
			return err
		}
		result = geminiOperationResult(operation)
		if !data.confirmed {
			return ErrGeminiDenied
		}
		data.used = true
		switch data.action {
		case "recover":
			operation.PlanID = data.planID
			operation.Backup, operation.Candidate = data.backup, data.candidate
			if err := state.saveGeminiInventory(ctx); err != nil {
				return err
			}
			if err := state.stageGeminiRecovery(ctx, operation, &data.observed); err != nil {
				return err
			}
			result, err = state.publishGemini(ctx, operation, &data.observed)
			return err
		case "restore":
			backup, err := state.readArtifact("backup", operation)
			if err != nil {
				return err
			}
			defer geminiClearBodies(&backup)
			if state.inventory.Operations[data.id] != nil || len(state.inventory.Operations) >= 32 {
				return ErrGeminiCapacity
			}
			restored := &geminiOperation{ID: data.id, PlanID: data.planID, Origin: operation.Origin, Owner: operation.Owner, Profile: operation.Profile, Asset: operation.Base.Asset, Source: operation.ID, AssetRevision: data.view.AssetRevision, Target: operation.Target, Before: data.observed.baseline, Base: data.base, State: GeminiRestorePrepared, Expires: data.observed.expires.UnixMilli(), ProfileIdentity: operation.ProfileIdentity, Observation: data.observed.descriptor, Restoring: true}
			for index, change := range data.view.Changes {
				cleanup := geminiconfig.Preserve
				if change.Cleanup {
					cleanup = geminiconfig.Clear
				}
				restored.Fields[index] = geminiconfig.FieldFact{Field: change.Field, Ownership: change.Ownership, Cleanup: cleanup}
				restored.Actions[index] = geminiconfig.FieldAction{Field: change.Field, Action: change.Action}
			}
			restored.Binding = state.planMAC("gemini-restore-plan-v1", struct {
				Plan, Source string
				Binding      [32]byte
				Before       [2]nativeImage
				Base         geminiSelectionRecord
				Expires      int64
			}{data.planID, operation.ID, operation.Binding, restored.Before, restored.Base, restored.Expires})
			previous := state.inventory.Operations[operation.Base.Operation]
			if previous != nil {
				restored.Managed = previous.Managed
			}
			for index := range restored.Before {
				original := operation.Before[index]
				restored.After[index] = restored.Before[index]
				if original.Present == restored.Before[index].Present && (!original.Present || original.SHA256 == restored.Before[index].SHA256 && original.Identity.Mode&0o777 == restored.Before[index].Identity.Mode&0o777) {
					continue
				}
				restored.Write[index] = true
				restored.StageNames[index] = "gemini-" + restored.ID + "-" + strconv.Itoa(index) + ".stage"
				restored.After[index] = nativeImage{Present: original.Present}
				if original.Present {
					restored.After[index].SHA256 = sha256.Sum256(backup[index])
					restored.FinalModes[index] = original.Identity.Mode & 0o777
				}
			}
			state.inventory.Operations[restored.ID] = restored
			if err := state.prepareGeminiOperation(ctx, restored, backup); err != nil {
				result = geminiOperationResult(restored)
				return err
			}
			result, err = state.publishGemini(ctx, restored, &data.observed)
			return err
		case "abandon", "retire":
			operation.State = GeminiRetiredUnpublished
			if data.action == "abandon" {
				operation.State = GeminiAbandonedBeforeWrite
			}
			operation.PlanID = data.planID
			if err := state.saveGeminiInventory(ctx); err != nil {
				return err
			}
			result = geminiOperationResult(operation)
			return nil
		default:
			return ErrGeminiInvalid
		}
	})
	return result, err
}

func (state *geminiEngineState) inspectGemini(ctx context.Context, operation *geminiOperation) (GeminiResult, error) {
	result := geminiOperationResult(operation)
	if err := state.check(ctx); err != nil {
		return result, err
	}
	target, images, bodies, err := state.observePair()
	defer geminiClearBodies(&bodies)
	if err != nil {
		return result, err
	}
	defer target.close()
	if operation.State == GeminiAbandonedBeforeWrite || operation.State == GeminiRetiredUnpublished {
		if !sameDirectory(target.identity, operation.Target) || images != operation.Before {
			result.State = GeminiNeedsAttention
		}
		return result, nil
	}
	if !sameDirectory(target.identity, operation.Target) || operation.Candidate.Present && (geminiValidateMix(operation, images) != nil || state.validateGeminiPublishedStages(target, operation, images) != nil) || !operation.Candidate.Present && images != operation.Before {
		result.State, result.Applied = GeminiNeedsAttention, false
		return result, nil
	}
	if operation.State == GeminiCompleted || operation.State == GeminiRestored {
		if images != operation.After {
			result.State, result.Applied = GeminiNeedsAttention, false
		}
		return result, nil
	}
	if operation.Candidate.Present {
		result.State = GeminiReconcileRequired
	}
	return result, nil
}

func (engine *GeminiEngine) StatusGemini(ctx context.Context, operation, origin, owner, profile string) (GeminiResult, error) {
	state := engine.private()
	var result GeminiResult
	err := state.withLock(ctx, func(ctx context.Context) error {
		stored, err := state.ownedGeminiOperation(operation, origin, owner, profile)
		if err != nil {
			return err
		}
		result, err = state.inspectGemini(ctx, stored)
		return err
	})
	return result, err
}

func (engine *GeminiEngine) PreviewGeminiRecovery(ctx context.Context, operation, origin, owner, profile, action string, observation GeminiObservation) (GeminiRecoveryPlan, error) {
	state := engine.private()
	var result GeminiRecoveryPlan
	err := state.withLock(ctx, func(ctx context.Context) error {
		stored, err := state.ownedGeminiOperation(operation, origin, owner, profile)
		if err != nil {
			return err
		}
		observed := observation.get[geminiObserved]()
		if observed == nil || observed.engine != state || !reflect.DeepEqual(observed.descriptor, stored.Observation) {
			return ErrGeminiConflict
		}
		if err := state.validateObservation(ctx, observed, observed.baseline, nil); err != nil {
			return err
		}
		if geminiTerminal(stored.State) && action != "restore" {
			return ErrGeminiConflict
		}
		base := stored.Base
		key := geminiSelectionKey(stored.Origin, stored.Owner, stored.Profile)
		if action == "restore" {
			if stored.State != GeminiCompleted || stored.Restoring || observed.baseline != stored.After {
				return ErrGeminiConflict
			}
			current, found := state.inventory.Selections[key]
			if !found || current.Operation != stored.ID {
				return ErrGeminiConflict
			}
			base = current
		}
		if err := state.validateSelection(base); err != nil {
			return err
		}
		for _, pending := range state.inventory.Operations {
			if pending.ID != stored.ID && !geminiTerminal(pending.State) {
				return ErrGeminiConflict
			}
		}
		data := &geminiRecoveryData{engine: state, source: *stored, observed: *observed, base: base, id: stored.ID, planID: codexRandomID(), action: action}
		data.source.StatusTicket = slices.Clone(stored.StatusTicket)
		var restorePresence [6]bool
		switch action {
		case "recover", "restore":
			if action == "recover" && geminiValidateMix(stored, observed.baseline) != nil {
				data.destroy()
				return ErrGeminiConflict
			}
			target, images, bodies, err := state.observePair()
			geminiClearBodies(&bodies)
			if err != nil {
				data.destroy()
				return err
			}
			stageError := state.validateGeminiPublishedStages(target, stored, images)
			closeError := target.close()
			if stageError != nil || closeError != nil {
				data.destroy()
				return ErrGeminiAttention
			}
			available := *stored
			for _, domain := range []string{"backup", "candidate"} {
				image, body, err := state.vault.read(stored.ID+"."+domain, 2*geminiArtifactLimit+1024)
				clear(body)
				if err != nil || !image.Present || image.Identity.Mode&0o077 != 0 {
					data.destroy()
					return ErrGeminiAttention
				}
				if domain == "backup" {
					if stored.Backup.Present && image != stored.Backup {
						data.destroy()
						return ErrGeminiAttention
					}
					available.Backup = image
					data.backup = image
				} else {
					if stored.Candidate.Present && image != stored.Candidate {
						data.destroy()
						return ErrGeminiAttention
					}
					available.Candidate = image
					data.candidate = image
				}
			}
			candidate, err := state.readArtifact("candidate", &available)
			if err != nil {
				data.destroy()
				return err
			}
			defer geminiClearBodies(&candidate)
			candidateBundle, err := geminiImages(available.After, candidate)
			if err != nil {
				data.destroy()
				return err
			}
			candidatePresence, err := geminiconfig.InspectNative0610Fields(ctx, candidateBundle)
			candidateBundle.Destroy()
			if err != nil {
				data.destroy()
				return ErrGeminiUnsupported
			}
			backup, err := state.readArtifact("backup", &available)
			if err != nil {
				data.destroy()
				return err
			}
			defer geminiClearBodies(&backup)
			secret := candidatePresence[0]
			if action == "restore" {
				data.id = codexRandomID()
				bundle, err := geminiImages(stored.Before, backup)
				if err != nil {
					data.destroy()
					return err
				}
				present, err := geminiconfig.InspectNative0610Fields(ctx, bundle)
				bundle.Destroy()
				if err != nil {
					data.destroy()
					return ErrGeminiUnsupported
				}
				secret = present[0]
				restorePresence = present
			}
			data.view.RequiredSlots = []agentconfig.GeminiSecretSlot{}
			if secret {
				data.view.RequiredSlots = []agentconfig.GeminiSecretSlot{agentconfig.GeminiAPIKeySecret}
			}
		case "abandon", "retire":
			if stored.PublicationStarted || observed.baseline != stored.Before || action == "abandon" && (stored.ConsumeStarted || stored.Consumed || stored.RemoteUnknown || stored.State == GeminiAuthorizeAttempted) {
				data.destroy()
				return ErrGeminiConflict
			}
		default:
			data.destroy()
			return ErrGeminiInvalid
		}
		data.view.OperationID, data.view.PlanID, data.view.SourceOperation = data.id, data.planID, stored.ID
		data.view.Origin, data.view.OwnerID, data.view.ProfileID = stored.Origin, stored.Owner, stored.Profile
		data.view.AssetID, data.view.AssetRevision, data.view.NativeProfileID = stored.Asset, stored.AssetRevision, stored.ProfileIdentity.ID
		if action == "restore" {
			data.view.AssetID = stored.Base.Asset
			data.view.AssetRevision = 0
			if previous := state.inventory.Operations[stored.Base.Operation]; previous != nil {
				data.view.AssetRevision = previous.AssetRevision
			}
		}
		data.view.ExpiresAt, data.view.FinalReview, data.view.LocalMaterial = observed.expires, true, action == "recover" || action == "restore"
		data.view.SelectionRevision, data.view.ConfirmationRevision = base.Revision+1, base.Confirmation+1
		for index, field := range geminiFields {
			role := geminiconfig.EnvRole
			if index >= 4 {
				role = geminiconfig.SettingsRole
			}
			data.view.Changes = append(data.view.Changes, GeminiChange{Role: role, Field: field, Ownership: stored.Fields[index].Ownership, Action: stored.Actions[index].Action, Cleanup: stored.Fields[index].Cleanup == geminiconfig.Clear})
		}
		if action == "restore" {
			for index := range data.view.Changes {
				change := &data.view.Changes[index]
				change.Ownership = geminiconfig.Absent
				change.Action = geminiconfig.Keep
				change.Cleanup = false
				if stored.Managed[index] {
					change.Ownership = geminiconfig.Managed
				}
				if restorePresence[index] {
					change.Action = geminiconfig.Write
				} else if stored.Managed[index] {
					change.Action = geminiconfig.Remove
					change.Cleanup = true
				}
			}
		}
		result = GeminiRecoveryPlan{geminiOpaque{new(any(data))}}
		return nil
	})
	return result, err
}

func geminiStageName(id string, epoch uint8, index int) string {
	if epoch == 0 {
		return "gemini-" + id + "-" + strconv.Itoa(index) + ".stage"
	}
	return "gemini-" + id + "-r" + strconv.Itoa(int(epoch)) + "-" + strconv.Itoa(index) + ".stage"
}

func (state *geminiEngineState) validateGeminiPublishedStages(target *nativeDirectory, operation *geminiOperation, images [2]nativeImage) error {
	for index, image := range images {
		if !operation.Write[index] || image == operation.Before[index] {
			continue
		}
		if image != operation.After[index] && !(operation.Restoring && image == operation.Stages[index]) {
			return ErrGeminiAttention
		}
		actual, body, err := target.read(operation.StageNames[index], 256<<10)
		clear(body)
		if err == nil && !actual.Present && (operation.State == GeminiCompleted || operation.State == GeminiRestored) {
			continue
		}
		if err != nil || actual != operation.Before[index] {
			return ErrGeminiAttention
		}
	}
	return nil
}
func geminiValidStageName(id, name string, index int, epoch uint8) bool {
	for current := uint8(0); current <= epoch; current++ {
		if name == geminiStageName(id, current, index) {
			return true
		}
	}
	return false
}
func (state *geminiEngineState) stageGeminiRecovery(ctx context.Context, operation *geminiOperation, observed *geminiObserved) error {
	if err := state.validateObservation(ctx, observed, observed.baseline, nil); err != nil {
		return err
	}
	target, images, bodies, err := state.observePair()
	defer geminiClearBodies(&bodies)
	if err != nil {
		return err
	}
	defer target.close()
	if !sameDirectory(target.identity, operation.Target) || geminiValidateMix(operation, images) != nil {
		return ErrGeminiConflict
	}
	candidates, err := state.readArtifact("candidate", operation)
	if err != nil {
		return err
	}
	defer geminiClearBodies(&candidates)
	var rebuild [2]bool
	for index := range images {
		if !operation.Write[index] || images[index] == operation.After[index] || !operation.After[index].Present || operation.Restoring && images[index] == operation.Stages[index] {
			continue
		}
		actual, body, err := target.read(operation.StageNames[index], 256<<10)
		clear(body)
		if err != nil {
			return err
		}
		if actual.Present && operation.Stages[index].Present && actual == operation.Stages[index] {
			continue
		}
		if actual.Present {
			if len(operation.RetainedStages) >= 8 {
				return ErrGeminiCapacity
			}
			operation.RetainedStages = append(operation.RetainedStages, operation.StageNames[index])
		}
		rebuild[index] = true
	}
	if rebuild == [2]bool{} {
		return nil
	}
	if operation.StageEpoch >= 4 {
		return ErrGeminiCapacity
	}
	operation.StageEpoch++
	for index, needed := range rebuild {
		if !needed {
			continue
		}
		operation.StageNames[index] = geminiStageName(operation.ID, operation.StageEpoch, index)
		operation.Stages[index] = nativeImage{}
		if err := state.saveGeminiInventory(ctx); err != nil {
			return err
		}
		stage, err := target.write(operation.StageNames[index], candidates[index])
		if err != nil {
			return err
		}
		if err := state.checkpoint(ctx, "recovery-stage-written-"+strconv.Itoa(index)); err != nil {
			return err
		}
		operation.Stages[index], operation.After[index] = stage, stage
		if operation.FinalModes[index] != 0 && operation.FinalModes[index] != 0o600 {
			operation.After[index].Identity.Mode = operation.After[index].Identity.Mode&^0o777 | operation.FinalModes[index]
		}
		if err := state.saveGeminiInventory(ctx); err != nil {
			return err
		}
	}
	return nil
}
