package httpapi

import (
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func configurationGeminiInput(draft *configurationv1.Draft, mutations []*configurationv1.SecretMutation) (configuration.Draft, []configuration.SecretMutation, error) {
	invalid := func() (configuration.Draft, []configuration.SecretMutation, error) {
		return configuration.Draft{}, nil, configuration.ErrInvalid
	}
	if draft == nil || len(mutations) > 1 {
		return invalid()
	}
	payload, valid := draft.Payload.(*configurationv1.Draft_GeminiProvider)
	if !valid || payload == nil || payload.GeminiProvider == nil {
		return invalid()
	}
	source := payload.GeminiProvider
	provider := &configuration.GeminiProvider{Endpoint: configurationClonePointer(source.Endpoint), EnvModel: configurationClonePointer(source.EnvModel), SettingsModel: configurationClonePointer(source.SettingsModel)}
	switch source.AuthMode {
	case configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_NATIVE_LOGIN:
		provider.AuthMode = agentconfig.GeminiNativeLogin
		if source.Endpoint != nil || source.KeyTransport != nil {
			return invalid()
		}
	case configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_API_KEY:
		provider.AuthMode = agentconfig.GeminiAPIKeyAuth
		if source.KeyTransport == nil {
			return invalid()
		}
		switch *source.KeyTransport {
		case configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_NATIVE_DEFAULT:
			provider.KeyTransport = new(agentconfig.GeminiNativeDefault)
		case configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_BEARER:
			provider.KeyTransport = new(agentconfig.GeminiBearer)
		default:
			return invalid()
		}
	default:
		return invalid()
	}
	for _, mutation := range mutations {
		if mutation == nil || mutation.SlotId != string(configuration.GeminiAPIKeySlot) {
			return invalid()
		}
	}
	secrets, err := configurationSecretInput(mutations)
	if err != nil {
		return invalid()
	}
	return configuration.Draft{Name: draft.Name, GeminiProvider: provider}, secrets, nil
}

func configurationGeminiView(entry configuration.EditView) (*configurationv1.DraftView, error) {
	invalid := func() (*configurationv1.DraftView, error) { return nil, configuration.ErrUnavailable }
	if entry.Metadata.AgentID != "gemini" || entry.Metadata.Kind != state.ConfigurationKindProvider || entry.Metadata.SchemaVersion != 1 || entry.Metadata.ID == "" || entry.Metadata.Revision < 1 || entry.Metadata.Name == "" || entry.GeminiProvider == nil || entry.CodexProvider != nil || entry.Provider != (configuration.ClaudeProvider{}) || len(entry.Secrets) != 1 || entry.Secrets[0].Slot != configuration.GeminiAPIKeySlot {
		return invalid()
	}
	source := entry.GeminiProvider
	provider, err := configurationGeminiProvider(source)
	if err != nil {
		return invalid()
	}
	readiness := configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE
	switch source.AuthMode {
	case agentconfig.GeminiNativeLogin:
		if entry.Secrets[0].Present || entry.Readiness != configuration.CredentialComplete {
			return invalid()
		}
	case agentconfig.GeminiAPIKeyAuth:
		if entry.Secrets[0].Present {
			if entry.Readiness != configuration.CredentialComplete {
				return invalid()
			}
		} else {
			if entry.Readiness != configuration.NeedsCredential {
				return invalid()
			}
			readiness = configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL
		}
	default:
		return invalid()
	}
	asset, err := configurationAsset(entry.Metadata)
	if err != nil {
		return nil, err
	}
	return &configurationv1.DraftView{Asset: asset, Draft: &configurationv1.Draft{Name: asset.Name, Payload: &configurationv1.Draft_GeminiProvider{GeminiProvider: provider}}, Secrets: []*configurationv1.SecretStatus{{SlotId: string(configuration.GeminiAPIKeySlot), Present: entry.Secrets[0].Present}}, CredentialReadiness: readiness, HasPreservedExtras: entry.HasPreservedExtras}, nil
}

func configurationGeminiProvider(source *configuration.GeminiProvider) (*configurationv1.GeminiProvider, error) {
	invalid := func() (*configurationv1.GeminiProvider, error) { return nil, configuration.ErrUnavailable }
	if source == nil {
		return invalid()
	}
	provider := &configurationv1.GeminiProvider{Endpoint: configurationClonePointer(source.Endpoint), EnvModel: configurationClonePointer(source.EnvModel), SettingsModel: configurationClonePointer(source.SettingsModel)}
	switch source.AuthMode {
	case agentconfig.GeminiNativeLogin:
		if source.Endpoint != nil || source.KeyTransport != nil {
			return invalid()
		}
		provider.AuthMode = configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_NATIVE_LOGIN
	case agentconfig.GeminiAPIKeyAuth:
		if source.KeyTransport == nil {
			return invalid()
		}
		provider.AuthMode = configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_API_KEY
		switch *source.KeyTransport {
		case agentconfig.GeminiNativeDefault:
			provider.KeyTransport = new(configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_NATIVE_DEFAULT)
		case agentconfig.GeminiBearer:
			provider.KeyTransport = new(configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_BEARER)
		default:
			return invalid()
		}
	default:
		return invalid()
	}
	return provider, nil
}
