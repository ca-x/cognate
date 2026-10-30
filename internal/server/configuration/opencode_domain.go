package configuration

import (
	"context"
	"encoding/json/v2"
	"slices"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

func (service *Service) OpenCodeProviderDesired(ctx context.Context, owner, id string, expectedRevision int64) (agentconfig.OpenCodeProviderDesired, error) {
	if expectedRevision < 1 {
		return agentconfig.OpenCodeProviderDesired{}, ErrInvalid
	}
	metadata, payload, err := service.loadProviderEditable(ctx, owner, id)
	if err != nil {
		return agentconfig.OpenCodeProviderDesired{}, err
	}
	if metadata.Revision != expectedRevision {
		return agentconfig.OpenCodeProviderDesired{}, ErrConflict
	}
	if metadata.AgentID != "opencode" {
		return agentconfig.OpenCodeProviderDesired{}, ErrUnsupported
	}
	view, err := openCodeEdit(ctx, metadata, payload)
	if err != nil {
		return agentconfig.OpenCodeProviderDesired{}, err
	}
	if view.HasPreservedExtras {
		return agentconfig.OpenCodeProviderDesired{}, ErrUnsupported
	}
	statuses := make([]agentconfig.OpenCodeSlotStatus, len(view.Secrets))
	for index, status := range view.Secrets {
		statuses[index] = agentconfig.OpenCodeSlotStatus{Slot: string(status.Slot), Present: status.Present}
	}
	desired := agentconfig.OpenCodeProviderDesired{Provider: *view.OpenCodeProvider, Slots: statuses}
	encoded, err := json.Marshal(desired)
	if err != nil || len(encoded) > agentconfig.MaxOpenCodeDesiredBytes {
		return agentconfig.OpenCodeProviderDesired{}, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.OpenCodeProviderDesired{}, err
	}
	return desired, nil
}

func cloneOpenCodeProvider(provider agentconfig.OpenCodeProvider) (agentconfig.OpenCodeProvider, error) {
	cloned, err := agentconfig.CloneOpenCodeProvider(provider)
	if err != nil {
		return agentconfig.OpenCodeProvider{}, ErrUnsupported
	}
	return cloned, nil
}

func openCodeMetadata(metadata state.ConfigurationAsset) error {
	if err := validateMetadata(metadata); err != nil {
		return err
	}
	if metadata.AgentID != "opencode" || metadata.Kind != state.ConfigurationKindProvider || metadata.SchemaVersion != 1 {
		return ErrUnsupported
	}
	return nil
}

func (service *Service) loadProviderEditable(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	if err := service.ready(ctx); err != nil {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, err
	}
	metadata, payload, err := service.store.ReadConfigurationAssetVersion(ctx, owner, id)
	if err := failure(ctx, err); err != nil {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, err
	}
	if metadata.ID != id {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, ErrUnavailable
	}
	if metadata.AgentID == "grokbuild" {
		err = grokBuildMetadata(metadata)
	} else if metadata.AgentID == "opencode" {
		err = openCodeMetadata(metadata)
	} else {
		err = editableMetadata(metadata)
	}
	if err != nil {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, err
	}
	return metadata, payload, nil
}

func openCodeEdit(ctx context.Context, metadata state.ConfigurationAsset, payload state.ConfigurationPayload) (EditView, error) {
	provider, secrets, extras, err := decodeOpenCodeEnvelope(payload)
	defer secrets.clear()
	if err != nil {
		return EditView{}, err
	}
	return openCodeView(ctx, metadata, provider, secrets, extras)
}

func openCodeView(ctx context.Context, metadata state.ConfigurationAsset, provider agentconfig.OpenCodeProvider, secrets openCodeCredentials, extras bool) (EditView, error) {
	required, err := agentconfig.OpenCodeRequiredSlots(provider)
	if err != nil {
		return EditView{}, ErrUnsupported
	}
	cloned, err := cloneOpenCodeProvider(provider)
	if err != nil {
		return EditView{}, err
	}
	view := EditView{Metadata: metadata, OpenCodeProvider: &cloned, Secrets: make([]SecretStatus, len(required)), Readiness: CredentialComplete, HasPreservedExtras: extras}
	for index, slot := range required {
		present := len(secretBytes(secrets[Slot(slot)])) > 0
		view.Secrets[index] = SecretStatus{Slot: Slot(slot), Present: present}
		if !present {
			view.Readiness = NeedsCredential
		}
	}
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	return view, nil
}

func (service *Service) createOpenCode(ctx context.Context, owner string, input Draft, secrets openCodeCredentials) (EditView, error) {
	payload, err := encodeOpenCodeEnvelope(*input.OpenCodeProvider, secrets)
	if err != nil {
		return EditView{}, err
	}
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	metadata, err := service.store.CreateConfigurationAsset(ctx, owner, state.ConfigurationAssetDraft{AgentID: "opencode", Kind: state.ConfigurationKindProvider, Name: input.Name, SchemaVersion: 1, Payload: payload})
	if err := failure(ctx, err); err != nil {
		return EditView{}, err
	}
	if openCodeMetadata(metadata) != nil || metadata.Revision != 1 || metadata.Name != input.Name {
		return EditView{}, ErrUnavailable
	}
	return openCodeView(ctx, metadata, *input.OpenCodeProvider, secrets, false)
}

func (service *Service) updateOpenCode(ctx context.Context, owner string, metadata state.ConfigurationAsset, payload state.ConfigurationPayload, input Draft, mutations []SecretMutation) (EditView, error) {
	_, current, extras, err := decodeOpenCodeEnvelope(payload)
	defer current.clear()
	if err != nil {
		return EditView{}, err
	}
	if extras {
		return EditView{}, ErrUnsupported
	}
	input, err = normalizeDraft(input)
	if err != nil {
		return EditView{}, err
	}
	if input.OpenCodeProvider == nil {
		return EditView{}, ErrInvalid
	}
	secrets, err := mergeOpenCodeSecrets(current, *input.OpenCodeProvider, mutations, false)
	defer secrets.clear()
	if err != nil {
		return EditView{}, err
	}
	encoded, err := encodeOpenCodeEnvelope(*input.OpenCodeProvider, secrets)
	if err != nil {
		return EditView{}, err
	}
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	updated, err := service.store.UpdateConfigurationAsset(ctx, owner, metadata.ID, metadata.Revision, state.ConfigurationAssetDraft{AgentID: "opencode", Kind: state.ConfigurationKindProvider, Name: input.Name, SchemaVersion: 1, Payload: encoded})
	if err := failure(ctx, err); err != nil {
		return EditView{}, err
	}
	if openCodeMetadata(updated) != nil || updated.ID != metadata.ID || updated.Revision != metadata.Revision+1 || updated.Name != input.Name {
		return EditView{}, ErrUnavailable
	}
	return openCodeView(ctx, updated, *input.OpenCodeProvider, secrets, false)
}

func mergeOpenCodeSecrets(current openCodeCredentials, provider agentconfig.OpenCodeProvider, mutations []SecretMutation, creating bool) (result openCodeCredentials, finalErr error) {
	required, err := agentconfig.OpenCodeRequiredSlots(provider)
	if err != nil {
		return nil, ErrUnsupported
	}
	if len(mutations) > 64 {
		return nil, ErrInvalid
	}
	result = make(openCodeCredentials, len(current))
	defer func() {
		if finalErr != nil {
			result.clear()
		}
	}()
	for slot, secret := range current {
		result[slot] = cloneSecret(secret)
	}
	seen := make(map[Slot]bool, len(mutations))
	for _, mutation := range mutations {
		if seen[mutation.Slot] || !agentconfig.ValidOpenCodeSlot(string(mutation.Slot)) || mutation.Action != Replace && len(secretBytes(mutation.Replacement)) != 0 {
			return result, ErrInvalid
		}
		seen[mutation.Slot] = true
		switch mutation.Action {
		case Keep:
			if creating || !slices.Contains(required, string(mutation.Slot)) {
				return result, ErrInvalid
			}
		case Clear:
			secret := result[mutation.Slot]
			secret.Clear()
			delete(result, mutation.Slot)
		case Replace:
			if !slices.Contains(required, string(mutation.Slot)) || !validSecret(mutation.Replacement) {
				return result, ErrInvalid
			}
			secret := result[mutation.Slot]
			secret.Clear()
			result[mutation.Slot] = cloneSecret(mutation.Replacement)
		default:
			return result, ErrInvalid
		}
	}
	total := 0
	for slot, secret := range result {
		if !slices.Contains(required, string(slot)) {
			return result, ErrInvalid
		}
		total += len(secretBytes(secret))
		if total > 64<<10 {
			return result, ErrInvalid
		}
	}
	return result, nil
}
