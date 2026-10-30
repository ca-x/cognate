package httpapi

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func configurationGrokBuildInput(draft *configurationv1.Draft, mutations []*configurationv1.SecretMutation) (configuration.Draft, []configuration.SecretMutation, error) {
	invalid := func() (configuration.Draft, []configuration.SecretMutation, error) {
		return configuration.Draft{}, nil, configuration.ErrInvalid
	}
	if draft == nil || len(mutations) > 1 {
		return invalid()
	}
	payload, ok := draft.Payload.(*configurationv1.Draft_GrokbuildProvider)
	if !ok || payload == nil || payload.GrokbuildProvider == nil {
		return invalid()
	}
	source := payload.GrokbuildProvider
	provider := &configuration.GrokBuildProvider{}
	switch source.Mode {
	case configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN:
		if source.Model != nil {
			return invalid()
		}
		provider.Mode = agentconfig.GrokBuildNativeLogin
	case configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL:
		if source.Model == nil {
			return invalid()
		}
		model := source.Model
		provider.Mode = agentconfig.GrokBuildCustomModel
		provider.Model = &configuration.GrokBuildModel{Profile: model.Profile, UpstreamModel: model.UpstreamModel, Endpoint: model.Endpoint, Name: model.Name, APIBackend: model.ApiBackend, ContextWindow: model.ContextWindow, EnvKey: configurationClonePointer(model.EnvKey)}
	default:
		return invalid()
	}
	for _, mutation := range mutations {
		if mutation == nil || mutation.SlotId != string(configuration.GrokBuildAPIKeySlot) {
			return invalid()
		}
	}
	secrets, err := configurationSecretInput(mutations)
	if err != nil {
		return invalid()
	}
	return configuration.Draft{Name: draft.Name, GrokBuildProvider: provider}, secrets, nil
}

func configurationGrokBuildView(entry configuration.EditView) (*configurationv1.DraftView, error) {
	invalid := func() (*configurationv1.DraftView, error) { return nil, configuration.ErrUnavailable }
	if entry.Metadata.AgentID != "grokbuild" || entry.Metadata.Kind != state.ConfigurationKindProvider || entry.Metadata.SchemaVersion != 1 || entry.Metadata.ID == "" || entry.Metadata.Name == "" || entry.Metadata.Revision < 1 || entry.GrokBuildProvider == nil || entry.Provider != (configuration.ClaudeProvider{}) || entry.CodexProvider != nil || entry.GeminiProvider != nil || entry.OpenCodeProvider != nil || len(entry.Secrets) != 1 || entry.Secrets[0].Slot != configuration.GrokBuildAPIKeySlot {
		return invalid()
	}
	source := entry.GrokBuildProvider
	provider := &configurationv1.GrokBuildProvider{}
	ready := configuration.CredentialComplete
	switch source.Mode {
	case agentconfig.GrokBuildNativeLogin:
		if source.Model != nil || entry.Secrets[0].Present {
			return invalid()
		}
		provider.Mode = configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN
	case agentconfig.GrokBuildCustomModel:
		if !configurationGrokBuildModelValid(source.Model) {
			return invalid()
		}
		model := source.Model
		provider.Mode = configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL
		provider.Model = &configurationv1.GrokBuildModel{Profile: model.Profile, UpstreamModel: model.UpstreamModel, Endpoint: model.Endpoint, Name: model.Name, ApiBackend: model.APIBackend, ContextWindow: model.ContextWindow, EnvKey: configurationClonePointer(model.EnvKey)}
		if !entry.Secrets[0].Present && model.EnvKey == nil {
			ready = configuration.NeedsCredential
		}
	default:
		return invalid()
	}
	if entry.Readiness != ready {
		return invalid()
	}
	readiness := configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE
	if ready == configuration.NeedsCredential {
		readiness = configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL
	}
	asset, err := configurationAsset(entry.Metadata)
	if err != nil {
		return invalid()
	}
	return &configurationv1.DraftView{Asset: asset, Draft: &configurationv1.Draft{Name: entry.Metadata.Name, Payload: &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: provider}}, Secrets: []*configurationv1.SecretStatus{{SlotId: string(configuration.GrokBuildAPIKeySlot), Present: entry.Secrets[0].Present}}, CredentialReadiness: readiness, HasPreservedExtras: entry.HasPreservedExtras}, nil
}

func configurationGrokBuildModelValid(model *configuration.GrokBuildModel) bool {
	literal := func(value string, limit int) bool {
		return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
	}
	if model == nil || !literal(model.Profile, 128) || !literal(model.UpstreamModel, 256) || !literal(model.Name, 128) || !literal(model.Endpoint, 2048) || model.APIBackend != "responses" || model.ContextWindow < 1 || model.ContextWindow > agentconfig.MaxGrokBuildContextWindow || strings.ContainsAny(model.Endpoint, "?#\\") {
		return false
	}
	endpoint, err := url.Parse(model.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.ContainsAny(endpoint.Path, "\\") || !utf8.ValidString(endpoint.Path) || strings.ContainsFunc(endpoint.Path, unicode.IsControl) {
		return false
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	if model.EnvKey != nil {
		value := *model.EnvKey
		if len(value) == 0 || len(value) > 128 {
			return false
		}
		for index, character := range []byte(value) {
			letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_'
			if !letter && (index == 0 || character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}
