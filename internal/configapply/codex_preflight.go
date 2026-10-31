package configapply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"math"
	"reflect"
	"runtime"
	"slices"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"google.golang.org/protobuf/proto"
)

var codexQualifiedProfiles = []CodexProfileIdentity{}

var codexRoleNames = [...]string{"config.toml", "auth.json", "cognate-model-catalog.json"}

func QualifiedCodexNativeProfile() (CodexProfileIdentity, error) {
	profile := codexNative0166Identity()
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && codexQualified(profile) {
		return profile, nil
	}
	return CodexProfileIdentity{}, ErrCodexUnsupported
}

func codexQualified(profile CodexProfileIdentity) bool {
	for _, trusted := range codexQualifiedProfiles {
		if trusted == profile {
			return true
		}
	}
	return false
}

func codexNative0166Identity() CodexProfileIdentity {
	id, manifest, profile, template := codexconfig.Native0166EffectiveManifest()
	return CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}
}

func codexRecordedProfileKnown(identity CodexProfileIdentity) bool {
	id, manifest, profile, template := codexconfig.Native0166Manifest()
	legacy := CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}
	id, manifest, profile, template = codexconfig.Native0166EffectiveManifest()
	effective := CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}
	return identity == legacy || identity == effective
}

func (engine *CodexEngine) RecordedCodexProfile(ctx context.Context, operationID string) (CodexProfileIdentity, error) {
	if !codexOperationID(operationID) {
		return CodexProfileIdentity{}, ErrCodexInvalid
	}
	state := engine.private()
	var identity CodexProfileIdentity
	err := state.withLock(ctx, func(ctx context.Context) error {
		operation := state.inventory.Operations[operationID]
		if operation == nil {
			return ErrCodexInvalid
		}
		identity = operation.ProfileIdentity
		return ctx.Err()
	})
	if err != nil {
		return CodexProfileIdentity{}, err
	}
	return identity, nil
}

func (engine *CodexEngine) ObserveCodex(ctx context.Context, selection CodexSelection, launch CodexLaunch) (CodexObservation, error) {
	state, input := engine.private(), selection.get[codexSelectionData]()
	if state == nil || input == nil || input.current.engine != state || input.prepared == nil {
		return CodexObservation{}, ErrCodexInvalid
	}
	var result CodexObservation
	err := state.withLock(ctx, func(ctx context.Context) error {
		if !state.now().Before(input.expires) {
			return ErrCodexExpired
		}
		if err := state.validateSelection(input.current.record); err != nil {
			return err
		}
		descriptor, baseline, target, expires, err := state.captureCodexLaunch(ctx, launch)
		if err != nil {
			return err
		}
		if !state.now().Before(minTime(input.expires, expires)) {
			return ErrCodexExpired
		}
		owned, err := codexCopySelection(input)
		if err != nil {
			return err
		}
		result = CodexObservation{value: new(any(&codexObservationData{engine: state, selection: owned, descriptor: descriptor, baseline: baseline, target: target, expires: minTime(input.expires, expires)}))}
		return nil
	})
	return result, err
}

func (engine *CodexEngine) PreviewCodexObserved(ctx context.Context, selection CodexSelection, observation CodexObservation) (CodexPlan, error) {
	state, input, observed := engine.private(), selection.get[codexSelectionData](), observation.get[codexObservationData]()
	if state == nil || input == nil || input.prepared == nil || input.current.engine != state || observed == nil || observed.engine != state || observed.selection == nil || !proto.Equal(input.prepared, observed.selection.prepared) || !reflect.DeepEqual(input.declaration, observed.selection.declaration) || input.current.record != observed.selection.current.record || !input.expires.Equal(observed.selection.expires) {
		return CodexPlan{}, ErrCodexInvalid
	}
	profile := codexNative0166Identity()
	if !codexQualified(profile) {
		return CodexPlan{}, ErrCodexUnsupported
	}
	var result CodexPlan
	err := state.withLock(ctx, func(ctx context.Context) error {
		if err := state.validateSelection(input.current.record); err != nil {
			return err
		}
		if err := state.validateCodexObservation(ctx, observed); err != nil {
			return err
		}
		target, images, bodies, err := state.observeCodex()
		defer codexClearBodies(&bodies)
		if err != nil {
			return err
		}
		defer target.close()
		if images != observed.baseline || !sameDirectory(target.identity, observed.target) {
			return ErrCodexConflict
		}
		var local [3]codexconfig.LocalImage
		for index, image := range images {
			presence := codexconfig.Missing
			if image.Present {
				presence = codexconfig.Present
			}
			local[index], err = codexconfig.NewImage(presence, bodies[index])
			if err != nil {
				for _, image := range local {
					image.Destroy()
				}
				return ErrCodexCapacity
			}
		}
		defer func() {
			for _, image := range local {
				image.Destroy()
			}
		}()
		bundle, err := codexconfig.NewImages(local[0], local[1], local[2])
		if err != nil {
			return ErrCodexInvalid
		}
		defer bundle.Destroy()
		providerOwner, catalogOwner := codexconfig.Absent, codexconfig.Absent
		providerPresent, err := codexconfig.Native0166ProviderPresent(ctx, local[0])
		if err != nil {
			return ErrCodexUnsupported
		}
		if providerPresent {
			providerOwner = codexconfig.Foreign
		}
		if images[2].Present {
			catalogOwner = codexconfig.Foreign
		}
		var managedModels []string
		if previous := state.inventory.Operations[input.current.record.Operation]; previous != nil && previous.State == CodexCompleted && previous.After == images && sameDirectory(previous.Target, target.identity) && (previous.ProfileIdentity == profile || previous.Observation != nil && codexRecordedProfileKnown(previous.ProfileIdentity) && previous.LifecycleVersion == 1 && previous.ManagedModelsVersion == 1) {
			if providerPresent {
				providerOwner = codexconfig.Managed
			}
			if images[2].Present {
				catalogOwner = codexconfig.Managed
			}
			managedModels = slices.Clone(previous.ManagedModels)
		}
		ownership, err := codexconfig.NewOwnership(providerOwner, catalogOwner, managedModels)
		if err != nil {
			return ErrCodexInvalid
		}
		defer ownership.Destroy()
		facts, expires, err := codexconfig.InspectNative0166(ctx, local[0], local[1], state.now())
		if err != nil {
			return ErrCodexUnsupported
		}
		defer facts.Destroy()
		owned, err := codexCopySelection(input)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(struct {
			Descriptor  codexLaunchDescriptor
			Selection   codexSelectionRecord
			Prepared    *nativev1.PreparationBinding
			Declaration agentconfig.CodexProviderDeclarationV1
		}{observed.descriptor, input.current.record, input.prepared, input.declaration}, json.Deterministic(true))
		if err != nil {
			return ErrCodexInvalid
		}
		defer clear(raw)
		resolution, err := codexconfig.NewResolution(profile.ID, codexconfig.CompleteEffective, ownership, facts, sha256.Sum256(raw))
		if err != nil {
			return ErrCodexUnsupported
		}
		defer resolution.Destroy()
		provider, err := codexconfig.CheckProvider(ctx, bundle, input.declaration, resolution)
		if err != nil {
			return codexSafeError(err)
		}
		if input.current.record.Confirmation == math.MaxUint64 {
			provider.Destroy()
			return ErrCodexCapacity
		}
		binding := provider.Binding()
		defer binding.Destroy()
		identity := binding.CopyObservation()
		if identity.ProfileID != profile.ID || identity.ProfileDigest != profile.ProfileDigest || identity.TemplateDigest != profile.TemplateDigest {
			provider.Destroy()
			return ErrCodexUnsupported
		}
		ownedObservation := *observed
		ownedObservation.selection = owned
		ownedObservation.descriptor = codexCopyLaunchDescriptor(observed.descriptor)
		view := SafeCodexPlan{OperationID: hex.EncodeToString(input.prepared.OperationId), AssetID: input.prepared.AssetId, C0ProfileID: input.prepared.ProfileId, AssetRevision: uint64(input.prepared.AssetRevision), SelectionRevision: input.prepared.SelectionRevision, ConfirmationRevision: input.current.record.Confirmation + 1, ExpiresAt: minTime(observed.expires, expires), RequiredSlots: []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, RecoverableWindow: true}
		for _, change := range provider.Preview() {
			action := codexconfig.Write
			if change.Change == codexconfig.ChangeKeep || change.Change == codexconfig.ChangeNativeAlwaysOn {
				action = codexconfig.Keep
			}
			view.Changes = append(view.Changes, CodexSafeChange{Role: change.Role, Field: change.Field, Action: action, Count: 1})
		}
		if !state.now().Before(minTime(input.expires, view.ExpiresAt)) {
			provider.Destroy()
			return ErrCodexExpired
		}
		result = CodexPlan{value: new(any(&codexPlanData{engine: state, selection: owned, view: view, baseline: images, target: target.identity, provider: provider, profile: profile, observation: &ownedObservation}))}
		return nil
	})
	return result, err
}

func (state *codexEngineState) validateCodexObservation(ctx context.Context, expected *codexObservationData) error {
	if expected == nil || expected.engine != state || expected.selection == nil {
		return ErrCodexUnsupported
	}
	if !state.now().Before(expected.expires) {
		return ErrCodexExpired
	}
	actual, images, target, expires, err := state.captureCodexLaunch(ctx, expected.descriptor.Launch)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCodexClosed) {
			return err
		}
		return ErrCodexConflict
	}
	if !state.now().Before(minTime(expected.expires, expected.selection.expires)) {
		return ErrCodexExpired
	}
	if !reflect.DeepEqual(actual, expected.descriptor) || images != expected.baseline || !sameDirectory(target, expected.target) || !state.now().Before(expires) {
		return ErrCodexConflict
	}
	return nil
}

func codexCopySelection(input *codexSelectionData) (*codexSelectionData, error) {
	if input == nil || input.prepared == nil {
		return nil, ErrCodexInvalid
	}
	raw, err := json.Marshal(input.declaration)
	if err != nil {
		return nil, ErrCodexInvalid
	}
	defer clear(raw)
	owned := *input
	owned.prepared = proto.Clone(input.prepared).(*nativev1.PreparationBinding)
	owned.declaration = agentconfig.CodexProviderDeclarationV1{}
	if json.Unmarshal(raw, &owned.declaration) != nil {
		return nil, ErrCodexInvalid
	}
	return &owned, nil
}

func codexCopyLaunchDescriptor(input codexLaunchDescriptor) codexLaunchDescriptor {
	input.Launch.Arguments = slices.Clone(input.Launch.Arguments)
	input.Launch.Environment = slices.Clone(input.Launch.Environment)
	input.Sources = slices.Clone(input.Sources)
	return input
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

func (state *codexEngineState) validatePlanExpiry(plan *codexPlanData) error {
	cutoff := minTime(plan.view.ExpiresAt, plan.selection.expires)
	if plan.observation != nil {
		cutoff = minTime(cutoff, plan.observation.expires)
	}
	if !state.now().Before(cutoff) {
		return ErrCodexExpired
	}
	return nil
}

func (state *codexEngineState) validatePlan(ctx context.Context, plan *codexPlanData) (resultError error) {
	if plan == nil || plan.engine != state || plan.selection == nil || !codexQualified(plan.profile) {
		return ErrCodexUnsupported
	}
	if err := state.validatePlanExpiry(plan); err != nil {
		return err
	}
	if err := state.validateCodexObservation(ctx, plan.observation); err != nil {
		return err
	}
	if err := state.validatePlanExpiry(plan); err != nil {
		return err
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
	return state.validatePlanExpiry(plan)
}

func (engine *CodexEngine) ConfirmCodex(ctx context.Context, plan CodexPlan, approvedSlots []agentconfig.CodexSecretSlot, finalApproved bool) (CodexAdmission, error) {
	return engine.confirmCodex(ctx, plan, approvedSlots, finalApproved, 0)
}

func (engine *CodexEngine) confirmCodex(ctx context.Context, plan CodexPlan, approvedSlots []agentconfig.CodexSecretSlot, finalApproved bool, lifecycle uint32) (CodexAdmission, error) {
	state := engine.private()
	data := plan.get[codexPlanData]()
	if data == nil || data.engine != state || !finalApproved || !slices.Equal(approvedSlots, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}) {
		return CodexAdmission{}, ErrCodexInvalid
	}
	var result CodexAdmission
	err := state.withLock(ctx, func(ctx context.Context) error {
		if err := state.validatePlan(ctx, data); err != nil {
			return err
		}
		operation, err := codexOperationFromPlan(data)
		if err != nil {
			return err
		}
		if state.inventory.Operations[operation.ID] != nil {
			return ErrCodexConflict
		}
		var constraints codexAuthorizationRecord
		if lifecycle == 1 {
			constraints = codexAuthorizationRecord{Operation: [16]byte(data.selection.prepared.OperationId), Slot: agentconfig.CodexProviderKeySecret, Revision: data.view.ConfirmationRevision, DeadlineUnixMS: data.view.ExpiresAt.UnixMilli()}
			operation.LifecycleVersion = 1
			operation.Authorization = new(constraints)
			if err := codexLifecycleShape(operation); err != nil {
				return err
			}
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
		if err := state.validatePlanExpiry(data); err != nil {
			return err
		}
		if lifecycle == 1 && !state.now().Before(timeFromCodex(constraints.DeadlineUnixMS*1_000_000)) {
			return ErrCodexExpired
		}
		if err := state.reserveCodexOperation(target, operation); err != nil {
			return err
		}
		result = CodexAdmission{value: new(any(&codexAdmissionData{plan: data, lifecycle: lifecycle, constraints: constraints}))}
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
	if data.lifecycle == 1 {
		if err := admission.requireConsumed(ctx); err != nil {
			return codexconfig.ProviderCandidate{}, err
		}
	}
	state := engine.private()
	state.mutex.Lock()
	if state.closing.Load() || data.used || data.materialized {
		state.mutex.Unlock()
		return codexconfig.ProviderCandidate{}, ErrCodexInvalid
	}
	if err := state.validatePlanExpiry(data.plan); err != nil {
		state.mutex.Unlock()
		return codexconfig.ProviderCandidate{}, err
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
		if err := state.validatePlan(ctx, data.plan); err != nil {
			return err
		}
		actual := state.inventory.Operations[data.plan.view.OperationID]
		if actual == nil || codexTerminal(actual.State) || !codexAdmissionRecordMatches(data, actual) || data.lifecycle == 1 && actual.State != CodexConsumeAttempted {
			return ErrCodexConflict
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
		operation.LifecycleVersion = actual.LifecycleVersion
		if actual.Authorization != nil {
			operation.Authorization = new(*actual.Authorization)
		}
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
		if data.lifecycle == 1 {
			declaration := original.CopyDeclaration()
			if declaration.Catalog != nil {
				operation.ManagedModels = make([]string, len(declaration.Catalog.Models))
				for index, model := range declaration.Catalog.Models {
					operation.ManagedModels[index] = model.Model
				}
				operation.ManagedModelsVersion = 1
			} else if previous := state.inventory.Operations[operation.Base.Operation]; previous != nil {
				operation.ManagedModelsVersion = previous.ManagedModelsVersion
			} else {
				operation.ManagedModelsVersion = 1
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
		Launch      *codexLaunchDescriptor
	}{encoded, binding.CopyDeclaration(), observation, binding.CopyBaselines(), plan.profile, codexPlanDescriptor(plan)})
	if err != nil {
		return nil, ErrCodexInvalid
	}
	defer clear(transcript)
	return &codexOperation{ID: hex.EncodeToString(prepared.OperationId), Owner: prepared.OwnerId, Origin: prepared.ServerOrigin, Profile: prepared.ProfileId, Asset: prepared.AssetId, Binding: sha256.Sum256(transcript), Target: plan.target, Before: plan.baseline, After: plan.baseline, Base: plan.selection.current.record, Expires: plan.view.ExpiresAt.UnixNano(), State: CodexPreviewed, ProfileIdentity: plan.profile, ManagedModels: slices.Clone(observation.ManagedModels), Observation: codexPlanDescriptor(plan)}, nil
}

func codexPlanDescriptor(plan *codexPlanData) *codexLaunchDescriptor {
	if plan == nil || plan.observation == nil {
		return nil
	}
	return new(codexCopyLaunchDescriptor(plan.observation.descriptor))
}
