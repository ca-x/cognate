package configapply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"reflect"
	"slices"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"google.golang.org/protobuf/proto"
)

var codexQualifiedProfiles = [...]CodexProfileIdentity{}

var codexRoleNames = [...]string{"config.toml", "auth.json", "cognate-model-catalog.json"}

func codexQualified(profile CodexProfileIdentity) bool {
	for _, trusted := range codexQualifiedProfiles {
		if trusted == profile {
			return true
		}
	}
	return false
}

func (engine *CodexEngine) PreviewCodex(ctx context.Context, selection CodexSelection) (CodexPlan, error) {
	state := engine.private()
	data := selection.get[codexSelectionData]()
	if data == nil || data.current.engine != state {
		return CodexPlan{}, ErrCodexInvalid
	}
	var result CodexPlan
	err := state.withLock(ctx, func(ctx context.Context) error {
		if !state.now().Before(data.expires) {
			return ErrCodexExpired
		}
		return ErrCodexUnsupported
	})
	return result, err
}

func (state *codexEngineState) observeCodex() (*nativeDirectory, [3]nativeImage, [3][]byte, error) {
	var images [3]nativeImage
	var bodies [3][]byte
	target, err := state.home.child(".codex", false)
	if err != nil {
		return nil, images, bodies, err
	}
	for index, name := range codexRoleNames {
		images[index], bodies[index], err = target.read(name, codexRoleLimit)
		if err != nil {
			codexClearBodies(&bodies)
			_ = target.close()
			return nil, images, [3][]byte{}, err
		}
	}
	return target, images, bodies, nil
}

func (state *codexEngineState) validateSelection(base codexSelectionRecord) error {
	current, exists := state.inventory.Selections[codexSelectionKey(base.Origin, base.Owner, base.Profile)]
	if !exists {
		current = codexSelectionRecord{Origin: base.Origin, Owner: base.Owner, Profile: base.Profile, Device: state.inventory.Device}
	}
	if current != base {
		return ErrCodexConflict
	}
	return nil
}

func (state *codexEngineState) validatePlan(plan *codexPlanData) (resultError error) {
	if plan == nil || plan.engine != state || plan.selection == nil || !codexQualified(plan.profile) {
		return ErrCodexUnsupported
	}
	if !state.now().Before(plan.view.ExpiresAt) {
		return ErrCodexExpired
	}
	if err := state.validateSelection(plan.selection.current.record); err != nil {
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
	if !sameDirectory(target.identity, plan.target) || images != plan.baseline {
		return ErrCodexConflict
	}
	return nil
}

func (engine *CodexEngine) ConfirmCodex(ctx context.Context, plan CodexPlan, approvedSlots []agentconfig.CodexSecretSlot, finalApproved bool) (CodexAdmission, error) {
	state := engine.private()
	data := plan.get[codexPlanData]()
	if data == nil || data.engine != state || !finalApproved || !slices.Equal(approvedSlots, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}) {
		return CodexAdmission{}, ErrCodexInvalid
	}
	var result CodexAdmission
	err := state.withLock(ctx, func(ctx context.Context) error {
		if err := state.validatePlan(data); err != nil {
			return err
		}
		operation, err := codexOperationFromPlan(data)
		if err != nil {
			return err
		}
		if state.inventory.Operations[operation.ID] != nil {
			return ErrCodexConflict
		}
		target, images, bodies, err := state.observeCodex()
		defer codexClearBodies(&bodies)
		if err != nil {
			return err
		}
		defer target.close()
		if !sameDirectory(target.identity, data.target) || images != data.baseline {
			return ErrCodexConflict
		}
		if err := state.reserveCodexOperation(target, operation); err != nil {
			return err
		}
		result = CodexAdmission{value: new(any(&codexAdmissionData{plan: data}))}
		return nil
	})
	return result, err
}

func (engine *CodexEngine) MaterializeCodex(ctx context.Context, admission CodexAdmission, selected []codexconfig.SelectedSecret) (codexconfig.ProviderCandidate, error) {
	data := admission.get[codexAdmissionData]()
	if data == nil || data.plan == nil || data.plan.engine != engine.private() || data.used || len(selected) != 1 {
		return codexconfig.ProviderCandidate{}, ErrCodexInvalid
	}
	if err := admission.Revalidate(ctx); err != nil {
		return codexconfig.ProviderCandidate{}, err
	}
	state := engine.private()
	state.mutex.Lock()
	if state.closing.Load() || data.used || data.materialized {
		state.mutex.Unlock()
		return codexconfig.ProviderCandidate{}, ErrCodexInvalid
	}
	data.materialized = true
	state.workers.Add(1)
	state.mutex.Unlock()
	defer state.workers.Done()
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(state.life, cancel)
	defer func() { stop(); cancel() }()
	candidate, err := codexconfig.RenderProvider(operation, data.plan.provider, selected)
	if err != nil {
		return codexconfig.ProviderCandidate{}, codexSafeError(err)
	}
	state.mutex.Lock()
	if state.closing.Load() {
		state.mutex.Unlock()
		candidate.Destroy()
		return codexconfig.ProviderCandidate{}, ErrCodexClosed
	}
	data.rendered = true
	data.renderedImages, data.renderedActions = codexCandidateWitness(candidate)
	state.mutex.Unlock()
	return candidate, nil
}

func codexCandidateWitness(candidate codexconfig.ProviderCandidate) ([3]codexconfig.BaselineBinding, [3]codexconfig.Action) {
	var images [3]codexconfig.BaselineBinding
	var actions [3]codexconfig.Action
	for index, role := range []codexconfig.Role{codexconfig.ConfigRole, codexconfig.AuthRole, codexconfig.CatalogRole} {
		image := candidate.CopyImage(role)
		body := image.CopyBytes()
		images[index] = codexconfig.BaselineBinding{Role: role, Presence: image.Presence(), Digest: sha256.Sum256(body)}
		clear(body)
		image.Destroy()
		actions[index] = candidate.Action(role)
	}
	return images, actions
}

func codexBindingsEqual(first, second codexconfig.ProviderBinding) bool {
	return first.CopyBaselines() == second.CopyBaselines() && reflect.DeepEqual(first.CopyDeclaration(), second.CopyDeclaration()) && reflect.DeepEqual(first.CopyObservation(), second.CopyObservation())
}

func (engine *CodexEngine) ApplyCodex(ctx context.Context, admission CodexAdmission, candidate codexconfig.ProviderCandidate) (CodexResult, error) {
	state := engine.private()
	data := admission.get[codexAdmissionData]()
	if data == nil || data.plan == nil || data.plan.engine != state || data.used || !data.rendered {
		return CodexResult{}, ErrCodexInvalid
	}
	var result CodexResult
	err := state.withLock(ctx, func(ctx context.Context) error {
		images, actions := codexCandidateWitness(candidate)
		if images != data.renderedImages || actions != data.renderedActions {
			return ErrCodexInvalid
		}
		if err := state.validatePlan(data.plan); err != nil {
			return err
		}
		binding := candidate.Binding()
		defer binding.Destroy()
		original := data.plan.provider.Binding()
		defer original.Destroy()
		if !codexBindingsEqual(original, binding) || candidate.Action(codexconfig.AuthRole) != codexconfig.Keep {
			return ErrCodexInvalid
		}
		data.used = true
		var bodies [3][]byte
		defer codexClearBodies(&bodies)
		operation, err := codexOperationFromPlan(data.plan)
		if err != nil {
			return err
		}
		operation.State = CodexConsumeAttempted
		for index, role := range []codexconfig.Role{codexconfig.ConfigRole, codexconfig.AuthRole, codexconfig.CatalogRole} {
			image := candidate.CopyImage(role)
			presence := image.Presence()
			bodies[index] = image.CopyBytes()
			image.Destroy()
			action := candidate.Action(role)
			if action != codexconfig.Keep && action != codexconfig.Write {
				return ErrCodexInvalid
			}
			if action == codexconfig.Write {
				if index == 1 || presence != codexconfig.Present {
					return ErrCodexInvalid
				}
				operation.Write[index] = true
				operation.After[index] = nativeImage{Present: true, SHA256: sha256.Sum256(bodies[index])}
			} else if presence != codexconfig.Present && presence != codexconfig.Missing {
				return ErrCodexInvalid
			}
		}
		if err := state.prepareCodexOperation(ctx, operation, bodies); err != nil {
			return err
		}
		result, err = state.publishCodex(ctx, operation)
		return err
	})
	return result, err
}

func codexOperationFromPlan(plan *codexPlanData) (*codexOperation, error) {
	prepared := plan.selection.prepared
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(prepared)
	if err != nil {
		return nil, ErrCodexInvalid
	}
	defer clear(encoded)
	binding := plan.provider.Binding()
	defer binding.Destroy()
	observation := binding.CopyObservation()
	transcript, err := json.Marshal(struct {
		Prepared    []byte
		Declaration agentconfig.CodexProviderDeclarationV1
		Observation codexconfig.ObservationBinding
		Baselines   [3]codexconfig.BaselineBinding
		Profile     CodexProfileIdentity
	}{encoded, binding.CopyDeclaration(), observation, binding.CopyBaselines(), plan.profile})
	if err != nil {
		return nil, ErrCodexInvalid
	}
	defer clear(transcript)
	return &codexOperation{ID: hex.EncodeToString(prepared.OperationId), Owner: prepared.OwnerId, Origin: prepared.ServerOrigin, Profile: prepared.ProfileId, Asset: prepared.AssetId, Binding: sha256.Sum256(transcript), Target: plan.target, Before: plan.baseline, After: plan.baseline, Base: plan.selection.current.record, Expires: plan.view.ExpiresAt.UnixNano(), State: CodexPreviewed, ProfileIdentity: plan.profile, ManagedModels: slices.Clone(observation.ManagedModels)}, nil
}
