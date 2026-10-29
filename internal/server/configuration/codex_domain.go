package configuration

import (
	"context"
	"encoding/json/v2"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

func (service *Service) loadEditable(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
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
	if err := editableMetadata(metadata); err != nil {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, err
	}
	return metadata, payload, nil
}

func editableMetadata(metadata state.ConfigurationAsset) error {
	if err := validateMetadata(metadata); err != nil {
		return err
	}
	if (metadata.AgentID != "claude" && metadata.AgentID != "codex") || metadata.Kind != state.ConfigurationKindProvider || metadata.SchemaVersion != 1 {
		return ErrUnsupported
	}
	return nil
}

func codexEdit(metadata state.ConfigurationAsset, payload state.ConfigurationPayload) (EditView, error) {
	provider, secret, extras, err := decodeCodexEnvelope(payload)
	if err != nil {
		return EditView{}, err
	}
	defer clear(secret.data)
	return codexView(metadata, provider, secret, extras), nil
}

func codexView(metadata state.ConfigurationAsset, provider CodexProvider, secret Secret, extras bool) EditView {
	present := len(secret.data) != 0
	view := EditView{Metadata: metadata, CodexProvider: cloneCodexProvider(provider), Secrets: []SecretStatus{{Slot: CodexProviderKeySlot, Present: present}}, Readiness: NeedsCredential, HasPreservedExtras: extras}
	if present {
		view.Readiness = CredentialComplete
	}
	return view
}

func (service *Service) createCodex(ctx context.Context, owner string, input Draft, secret Secret) (EditView, error) {
	defer clear(secret.data)
	payload, err := encodeCodexEnvelope(*input.CodexProvider, secret)
	if err != nil {
		return EditView{}, err
	}
	metadata, err := service.store.CreateConfigurationAsset(ctx, owner, state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: input.Name, SchemaVersion: 1, Payload: payload})
	if err := failure(ctx, err); err != nil {
		return EditView{}, err
	}
	if editableMetadata(metadata) != nil || metadata.AgentID != "codex" || metadata.Revision != 1 || metadata.Name != input.Name {
		return EditView{}, ErrUnavailable
	}
	return codexView(metadata, *input.CodexProvider, secret, false), nil
}

func (service *Service) updateCodex(ctx context.Context, owner string, metadata state.ConfigurationAsset, payload state.ConfigurationPayload, input Draft, mutations []SecretMutation) (EditView, error) {
	_, secret, extras, err := decodeCodexEnvelope(payload)
	if err != nil {
		return EditView{}, err
	}
	defer clear(secret.data)
	if extras {
		return EditView{}, ErrUnsupported
	}
	input, err = normalizeDraft(input)
	if err != nil {
		return EditView{}, err
	}
	if input.CodexProvider == nil {
		return EditView{}, ErrInvalid
	}
	merged, err := mergeCodexSecret(secret, mutations, false)
	if err != nil {
		return EditView{}, err
	}
	defer clear(merged.data)
	encoded, err := encodeCodexEnvelope(*input.CodexProvider, merged)
	if err != nil {
		return EditView{}, err
	}
	updated, err := service.store.UpdateConfigurationAsset(ctx, owner, metadata.ID, metadata.Revision, state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: input.Name, SchemaVersion: 1, Payload: encoded})
	if err := failure(ctx, err); err != nil {
		return EditView{}, err
	}
	if editableMetadata(updated) != nil || updated.AgentID != "codex" || updated.ID != metadata.ID || updated.Revision != metadata.Revision+1 || updated.Name != input.Name {
		return EditView{}, ErrUnavailable
	}
	return codexView(updated, *input.CodexProvider, merged, false), nil
}

func mergeCodexSecret(current Secret, mutations []SecretMutation, creating bool) (Secret, error) {
	if len(mutations) == 0 {
		return NewSecret(current.data), nil
	}
	if len(mutations) != 1 || mutations[0].Slot != CodexProviderKeySlot {
		return Secret{}, ErrInvalid
	}
	mutation := mutations[0]
	if mutation.Action != Replace && len(mutation.Replacement.data) != 0 {
		return Secret{}, ErrInvalid
	}
	switch mutation.Action {
	case Keep:
		if creating {
			return Secret{}, ErrInvalid
		}
		return NewSecret(current.data), nil
	case Clear:
		return Secret{}, nil
	case Replace:
		if validSecret(mutation.Replacement) {
			return NewSecret(mutation.Replacement.data), nil
		}
	}
	return Secret{}, ErrInvalid
}

func cloneCodexProvider(provider CodexProvider) *CodexProvider {
	if provider.ReasoningEffort != nil {
		provider.ReasoningEffort = new(*provider.ReasoningEffort)
	}
	if provider.Catalog != nil {
		catalog := agentconfig.CodexCatalogDesired{Models: slices.Clone(provider.Catalog.Models)}
		for index := range catalog.Models {
			model := &catalog.Models[index]
			if model.DisplayName != nil {
				model.DisplayName = new(*model.DisplayName)
			}
			if model.ContextWindow != nil {
				model.ContextWindow = new(*model.ContextWindow)
			}
			if model.SupportsParallelToolCalls != nil {
				model.SupportsParallelToolCalls = new(*model.SupportsParallelToolCalls)
			}
			if model.InputModalities != nil {
				model.InputModalities = new(slices.Clone(*model.InputModalities))
			}
			if model.BaseInstructions != nil {
				model.BaseInstructions = new(*model.BaseInstructions)
			}
			if model.ReasoningLevels != nil {
				model.ReasoningLevels = new(slices.Clone(*model.ReasoningLevels))
			}
			if model.DefaultReasoningLevel != nil {
				model.DefaultReasoningLevel = new(*model.DefaultReasoningLevel)
			}
		}
		provider.Catalog = &catalog
	}
	return &provider
}

func validateCodexProvider(provider CodexProvider, keyPresent bool) error {
	if provider.AuthMode != agentconfig.CodexProviderKeyAuth || !codexLiteral(provider.Endpoint, 2048) || strings.ContainsAny(provider.Endpoint, "?#\\") || !codexModelID(provider.Model) || provider.ReasoningEffort != nil && !codexEffort(*provider.ReasoningEffort) {
		return ErrInvalid
	}
	endpoint, err := url.Parse(provider.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl) || !utf8.ValidString(endpoint.Path) {
		return ErrInvalid
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return ErrInvalid
		}
	}
	if provider.Catalog != nil {
		models := provider.Catalog.Models
		if len(models) < 1 || len(models) > 32 {
			return ErrInvalid
		}
		seen := make(map[string]bool, len(models))
		for _, model := range models {
			if !codexModelID(model.Model) || seen[model.Model] || !validCodexCatalogModel(model) {
				return ErrInvalid
			}
			seen[model.Model] = true
			if model.Model == provider.Model && provider.ReasoningEffort != nil && model.ReasoningLevels != nil && !slices.Contains(*model.ReasoningLevels, *provider.ReasoningEffort) {
				return ErrInvalid
			}
		}
		if !seen[provider.Model] {
			return ErrInvalid
		}
		encoded, err := json.Marshal(provider.Catalog)
		if err != nil || len(encoded) > agentconfig.MaxCodexCatalogBytes {
			return ErrInvalid
		}
	}
	desired := agentconfig.CodexProviderDesired{AuthMode: provider.AuthMode, Endpoint: provider.Endpoint, Model: provider.Model, ReasoningEffort: provider.ReasoningEffort, KeyPresent: keyPresent, Catalog: provider.Catalog}
	encoded, err := json.Marshal(desired)
	if err != nil || len(encoded) > agentconfig.MaxCodexDesiredBytes {
		return ErrInvalid
	}
	return nil
}

func codexLiteral(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

func codexModelID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, character := range []byte(value) {
		alphanumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
		if !alphanumeric && (index == 0 || !strings.ContainsRune("._:/-", rune(character))) {
			return false
		}
	}
	return true
}

func codexEffort(value agentconfig.CodexReasoningEffort) bool {
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func validCodexCatalogModel(model agentconfig.CodexCatalogModelDesired) bool {
	if model.DisplayName != nil && !codexLiteral(*model.DisplayName, 256) || model.ContextWindow != nil && (*model.ContextWindow == 0 || *model.ContextWindow > 2147483647) {
		return false
	}
	if model.BaseInstructions != nil {
		body := *model.BaseInstructions
		if len(body) == 0 || len(body) > 2048 || !utf8.ValidString(body) || strings.ContainsFunc(body, func(character rune) bool {
			return unicode.IsControl(character) && character != '\r' && character != '\n' && character != '\t'
		}) {
			return false
		}
	}
	if model.InputModalities != nil {
		modalities := *model.InputModalities
		if len(modalities) < 1 || len(modalities) > 2 {
			return false
		}
		for index, modality := range modalities {
			if modality != "text" && modality != "image" || slices.Contains(modalities[:index], modality) {
				return false
			}
		}
	}
	if model.ReasoningLevels != nil {
		levels := *model.ReasoningLevels
		if len(levels) < 1 || len(levels) > 7 {
			return false
		}
		for index, level := range levels {
			if !codexEffort(level) || slices.Contains(levels[:index], level) {
				return false
			}
		}
	}
	return model.DefaultReasoningLevel == nil || model.ReasoningLevels != nil && slices.Contains(*model.ReasoningLevels, *model.DefaultReasoningLevel)
}
