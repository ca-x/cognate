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

func (service *Service) GeminiProviderDesired(ctx context.Context, owner, id string, expectedRevision int64) (agentconfig.GeminiProviderDesired, error) {
	if expectedRevision < 1 {
		return agentconfig.GeminiProviderDesired{}, ErrInvalid
	}
	metadata, payload, err := service.loadEditable(ctx, owner, id)
	if err != nil {
		return agentconfig.GeminiProviderDesired{}, err
	}
	if metadata.Revision != expectedRevision {
		return agentconfig.GeminiProviderDesired{}, ErrConflict
	}
	if metadata.AgentID != "gemini" {
		return agentconfig.GeminiProviderDesired{}, ErrUnsupported
	}
	provider, secret, extras, err := decodeGeminiEnvelope(payload)
	defer secret.Clear()
	if canceled := ctx.Err(); canceled != nil {
		return agentconfig.GeminiProviderDesired{}, canceled
	}
	if err != nil {
		return agentconfig.GeminiProviderDesired{}, err
	}
	if extras {
		return agentconfig.GeminiProviderDesired{}, ErrUnsupported
	}
	present := len(secretBytes(secret)) != 0
	if provider.AuthMode == agentconfig.GeminiAPIKeyAuth && !present {
		return agentconfig.GeminiProviderDesired{}, ErrInvalid
	}
	desired := geminiDesired(provider, present)
	if err := ctx.Err(); err != nil {
		return agentconfig.GeminiProviderDesired{}, err
	}
	return desired, nil
}

func geminiDesired(provider GeminiProvider, keyPresent bool) agentconfig.GeminiProviderDesired {
	provider = *cloneGeminiProvider(provider)
	return agentconfig.GeminiProviderDesired{AuthMode: provider.AuthMode, Endpoint: provider.Endpoint, KeyTransport: provider.KeyTransport, EnvModel: provider.EnvModel, SettingsModel: provider.SettingsModel, KeyPresent: keyPresent}
}

func geminiEdit(ctx context.Context, metadata state.ConfigurationAsset, payload state.ConfigurationPayload) (EditView, error) {
	provider, secret, extras, err := decodeGeminiEnvelope(payload)
	defer secret.Clear()
	if canceled := ctx.Err(); canceled != nil {
		return EditView{}, canceled
	}
	if err != nil {
		return EditView{}, err
	}
	return geminiView(metadata, provider, secret, extras), nil
}

func geminiView(metadata state.ConfigurationAsset, provider GeminiProvider, secret Secret, extras bool) EditView {
	present := len(secretBytes(secret)) != 0
	view := EditView{Metadata: metadata, GeminiProvider: cloneGeminiProvider(provider), Secrets: []SecretStatus{{Slot: GeminiAPIKeySlot, Present: present}}, Readiness: CredentialComplete, HasPreservedExtras: extras}
	if provider.AuthMode == agentconfig.GeminiAPIKeyAuth && !present {
		view.Readiness = NeedsCredential
	}
	return view
}

func (service *Service) createGemini(ctx context.Context, owner string, input Draft, secret Secret) (EditView, error) {
	defer secret.Clear()
	payload, err := encodeGeminiEnvelope(*input.GeminiProvider, secret)
	if err != nil {
		return EditView{}, err
	}
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	metadata, err := service.store.CreateConfigurationAsset(ctx, owner, state.ConfigurationAssetDraft{AgentID: "gemini", Kind: state.ConfigurationKindProvider, Name: input.Name, SchemaVersion: 1, Payload: payload})
	if err := failure(ctx, err); err != nil {
		return EditView{}, err
	}
	if editableMetadata(metadata) != nil || metadata.AgentID != "gemini" || metadata.Revision != 1 || metadata.Name != input.Name {
		return EditView{}, ErrUnavailable
	}
	view := geminiView(metadata, *input.GeminiProvider, secret, false)
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	return view, nil
}

func (service *Service) updateGemini(ctx context.Context, owner string, metadata state.ConfigurationAsset, payload state.ConfigurationPayload, input Draft, mutations []SecretMutation) (EditView, error) {
	_, current, extras, err := decodeGeminiEnvelope(payload)
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
	if input.GeminiProvider == nil {
		return EditView{}, ErrInvalid
	}
	merged, err := mergeGeminiSecret(current, mutations, false)
	if err != nil {
		return EditView{}, err
	}
	defer merged.Clear()
	encoded, err := encodeGeminiEnvelope(*input.GeminiProvider, merged)
	if err != nil {
		return EditView{}, err
	}
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	updated, err := service.store.UpdateConfigurationAsset(ctx, owner, metadata.ID, metadata.Revision, state.ConfigurationAssetDraft{AgentID: "gemini", Kind: state.ConfigurationKindProvider, Name: input.Name, SchemaVersion: 1, Payload: encoded})
	if err := failure(ctx, err); err != nil {
		return EditView{}, err
	}
	if editableMetadata(updated) != nil || updated.AgentID != "gemini" || updated.ID != metadata.ID || updated.Revision != metadata.Revision+1 || updated.Name != input.Name {
		return EditView{}, ErrUnavailable
	}
	view := geminiView(updated, *input.GeminiProvider, merged, false)
	if err := ctx.Err(); err != nil {
		return EditView{}, err
	}
	return view, nil
}

func mergeGeminiSecret(current Secret, mutations []SecretMutation, creating bool) (Secret, error) {
	if len(mutations) == 0 {
		return cloneSecret(current), nil
	}
	if len(mutations) != 1 || mutations[0].Slot != GeminiAPIKeySlot {
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

func cloneGeminiProvider(provider GeminiProvider) *GeminiProvider {
	if provider.Endpoint != nil {
		provider.Endpoint = new(*provider.Endpoint)
	}
	if provider.KeyTransport != nil {
		provider.KeyTransport = new(*provider.KeyTransport)
	}
	if provider.EnvModel != nil {
		provider.EnvModel = new(*provider.EnvModel)
	}
	if provider.SettingsModel != nil {
		provider.SettingsModel = new(*provider.SettingsModel)
	}
	return &provider
}

func validateGeminiProvider(provider GeminiProvider) error {
	switch provider.AuthMode {
	case agentconfig.GeminiNativeLogin:
		if provider.Endpoint != nil || provider.KeyTransport != nil {
			return ErrInvalid
		}
	case agentconfig.GeminiAPIKeyAuth:
		if provider.KeyTransport == nil || *provider.KeyTransport != agentconfig.GeminiNativeDefault && *provider.KeyTransport != agentconfig.GeminiBearer {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if provider.Endpoint != nil {
		value := *provider.Endpoint
		if !codexLiteral(value, 2048) || strings.ContainsAny(value, "?#\\") {
			return ErrInvalid
		}
		endpoint, err := url.Parse(value)
		if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl) || !utf8.ValidString(endpoint.Path) {
			return ErrInvalid
		}
		if endpoint.Port() != "" {
			port, err := strconv.Atoi(endpoint.Port())
			if err != nil || port < 1 || port > 65535 {
				return ErrInvalid
			}
		}
	}
	for _, model := range []*string{provider.EnvModel, provider.SettingsModel} {
		if model == nil {
			continue
		}
		if len(*model) == 0 || len(*model) > 256 {
			return ErrInvalid
		}
		for index, character := range []byte(*model) {
			alphanumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
			if !alphanumeric && (index == 0 || !strings.ContainsRune("._:/-", rune(character))) {
				return ErrInvalid
			}
		}
	}
	encoded, err := json.Marshal(geminiDesired(provider, provider.AuthMode == agentconfig.GeminiAPIKeyAuth))
	if err != nil || len(encoded) > agentconfig.MaxGeminiDesiredBytes {
		return ErrInvalid
	}
	return nil
}
