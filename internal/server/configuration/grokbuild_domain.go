package configuration

import (
	"context"
	"encoding/json/v2"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

func (service *Service) GrokBuildProviderDesired(ctx context.Context, owner, id string, expectedRevision int64) (agentconfig.GrokBuildProviderDesired, error) {
	if expectedRevision < 1 {
		return agentconfig.GrokBuildProviderDesired{}, ErrInvalid
	}
	metadata, payload, err := service.loadProviderEditable(ctx, owner, id)
	if err != nil {
		return agentconfig.GrokBuildProviderDesired{}, err
	}
	if metadata.Revision != expectedRevision {
		return agentconfig.GrokBuildProviderDesired{}, ErrConflict
	}
	if err := grokBuildMetadata(metadata); err != nil {
		return agentconfig.GrokBuildProviderDesired{}, err
	}
	view, err := grokBuildEdit(ctx, metadata, payload)
	if err != nil {
		return agentconfig.GrokBuildProviderDesired{}, err
	}
	if view.HasPreservedExtras {
		return agentconfig.GrokBuildProviderDesired{}, ErrUnsupported
	}
	if view.Readiness != CredentialComplete {
		return agentconfig.GrokBuildProviderDesired{}, ErrInvalid
	}
	provider := view.GrokBuildProvider
	desired := agentconfig.GrokBuildProviderDesired{Mode: provider.Mode}
	if model := provider.Model; model != nil {
		desired.Model = &agentconfig.GrokBuildModelDesired{Profile: model.Profile, UpstreamModel: model.UpstreamModel, Endpoint: model.Endpoint, Name: model.Name, APIBackend: model.APIBackend, ContextWindow: model.ContextWindow, EnvKey: model.EnvKey, KeyPresent: view.Secrets[0].Present}
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.GrokBuildProviderDesired{}, err
	}
	return desired, nil
}

func grokBuildMetadata(metadata state.ConfigurationAsset) error {
	if err := validateMetadata(metadata); err != nil {
		return err
	}
	if metadata.AgentID != "grokbuild" || metadata.Kind != state.ConfigurationKindProvider || metadata.SchemaVersion != 1 {
		return ErrUnsupported
	}
	return nil
}

func cloneGrokBuildProvider(provider GrokBuildProvider) *GrokBuildProvider {
	if provider.Model != nil {
		model := *provider.Model
		if model.EnvKey != nil {
			model.EnvKey = new(*model.EnvKey)
		}
		provider.Model = &model
	}
	return &provider
}

func validateGrokBuildProvider(provider GrokBuildProvider) error {
	switch provider.Mode {
	case agentconfig.GrokBuildNativeLogin:
		if provider.Model != nil {
			return ErrInvalid
		}
		return nil
	case agentconfig.GrokBuildCustomModel:
	default:
		if provider.Mode == "" {
			return ErrInvalid
		}
		return ErrUnsupported
	}
	model := provider.Model
	if model == nil || !codexLiteral(model.Profile, 128) || !codexLiteral(model.UpstreamModel, 256) || !codexLiteral(model.Name, 128) || !codexLiteral(model.APIBackend, 64) || !validGrokBuildEndpoint(model.Endpoint) || model.ContextWindow < 1 || model.ContextWindow > agentconfig.MaxGrokBuildContextWindow {
		return ErrInvalid
	}
	if model.EnvKey != nil {
		value := *model.EnvKey
		if len(value) == 0 || len(value) > 128 {
			return ErrInvalid
		}
		for index, character := range []byte(value) {
			letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_'
			if !letter && (index == 0 || character < '0' || character > '9') {
				return ErrInvalid
			}
		}
	}
	if model.APIBackend != "responses" {
		return ErrUnsupported
	}
	encoded, err := json.Marshal(provider)
	if err != nil || len(encoded) > agentconfig.MaxGrokBuildDesiredBytes {
		return ErrInvalid
	}
	return nil
}

func validGrokBuildEndpoint(value string) bool {
	if !codexLiteral(value, 2048) || strings.ContainsAny(value, "?#\\") {
		return false
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl) || !utf8.ValidString(endpoint.Path) {
		return false
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
}

func grokBuildEdit(ctx context.Context, metadata state.ConfigurationAsset, payload state.ConfigurationPayload) (EditView, error) {
	provider, secret, extras, err := decodeGrokBuildEnvelope(payload)
	defer secret.Clear()
	if canceled := ctx.Err(); canceled != nil {
		return EditView{}, canceled
	}
	if err != nil {
		return EditView{}, err
	}
	return grokBuildView(ctx, metadata, provider, secret, extras)
}

func grokBuildView(ctx context.Context, metadata state.ConfigurationAsset, provider GrokBuildProvider, secret Secret, extras bool) (EditView, error) {
	present := len(secretBytes(secret)) != 0
	view := EditView{Metadata: metadata, GrokBuildProvider: cloneGrokBuildProvider(provider), Secrets: []SecretStatus{{Slot: GrokBuildAPIKeySlot, Present: present}}, Readiness: CredentialComplete, HasPreservedExtras: extras}
	if provider.Mode == agentconfig.GrokBuildCustomModel && !present && provider.Model.EnvKey == nil {
		view.Readiness = NeedsCredential
	}
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	return view, nil
}

func (service *Service) createGrokBuild(ctx context.Context, owner string, input Draft, secret Secret) (EditView, error) {
	payload, err := encodeGrokBuildEnvelope(*input.GrokBuildProvider, secret)
	if err != nil {
		return EditView{}, err
	}
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	metadata, err := service.store.CreateConfigurationAsset(ctx, owner, state.ConfigurationAssetDraft{AgentID: "grokbuild", Kind: state.ConfigurationKindProvider, Name: input.Name, SchemaVersion: 1, Payload: payload})
	if err := failure(ctx, err); err != nil {
		return EditView{}, err
	}
	if grokBuildMetadata(metadata) != nil || metadata.Revision != 1 || metadata.Name != input.Name {
		return EditView{}, ErrUnavailable
	}
	return grokBuildView(ctx, metadata, *input.GrokBuildProvider, secret, false)
}

func (service *Service) updateGrokBuild(ctx context.Context, owner string, metadata state.ConfigurationAsset, payload state.ConfigurationPayload, input Draft, mutations []SecretMutation) (EditView, error) {
	_, current, extras, err := decodeGrokBuildEnvelope(payload)
	defer current.Clear()
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
	if input.GrokBuildProvider == nil {
		return EditView{}, ErrInvalid
	}
	merged, err := mergeGrokBuildSecret(current, mutations, false)
	defer merged.Clear()
	if err != nil {
		return EditView{}, err
	}
	encoded, err := encodeGrokBuildEnvelope(*input.GrokBuildProvider, merged)
	if err != nil {
		return EditView{}, err
	}
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	updated, err := service.store.UpdateConfigurationAsset(ctx, owner, metadata.ID, metadata.Revision, state.ConfigurationAssetDraft{AgentID: "grokbuild", Kind: state.ConfigurationKindProvider, Name: input.Name, SchemaVersion: 1, Payload: encoded})
	if err := failure(ctx, err); err != nil {
		return EditView{}, err
	}
	if grokBuildMetadata(updated) != nil || updated.ID != metadata.ID || updated.Revision != metadata.Revision+1 || updated.Name != input.Name {
		return EditView{}, ErrUnavailable
	}
	return grokBuildView(ctx, updated, *input.GrokBuildProvider, merged, false)
}

func mergeGrokBuildSecret(current Secret, mutations []SecretMutation, creating bool) (Secret, error) {
	if len(mutations) == 0 {
		return cloneSecret(current), nil
	}
	if len(mutations) != 1 || mutations[0].Slot != GrokBuildAPIKeySlot {
		return Secret{}, ErrInvalid
	}
	mutation := mutations[0]
	if mutation.Action != Replace && len(secretBytes(mutation.Replacement)) != 0 {
		return Secret{}, ErrInvalid
	}
	switch mutation.Action {
	case Keep:
		if !creating {
			return cloneSecret(current), nil
		}
	case Clear:
		return Secret{}, nil
	case Replace:
		if validSecret(mutation.Replacement) {
			return cloneSecret(mutation.Replacement), nil
		}
	}
	return Secret{}, ErrInvalid
}
