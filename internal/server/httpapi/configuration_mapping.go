package httpapi

import (
	"math"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func configurationInput(draft *configurationv1.Draft, mutations []*configurationv1.SecretMutation) (configuration.Draft, []configuration.SecretMutation, error) {
	if draft != nil {
		if _, gemini := draft.Payload.(*configurationv1.Draft_GeminiProvider); gemini {
			return configurationGeminiInput(draft, mutations)
		}
	}
	if draft != nil && draft.GetCodexProvider() != nil {
		return configurationCodexInput(draft, mutations)
	}
	if draft == nil || draft.GetClaudeProvider() == nil {
		return configuration.Draft{}, nil, configuration.ErrInvalid
	}
	provider := draft.GetClaudeProvider()
	var mode configuration.AuthMode
	switch provider.AuthMode {
	case configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_NATIVE_LOGIN:
		mode = configuration.NativeLogin
	case configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY:
		mode = configuration.APIKey
	case configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_AUTH_TOKEN:
		mode = configuration.AuthToken
	default:
		return configuration.Draft{}, nil, configuration.ErrInvalid
	}
	input := configuration.Draft{Name: draft.Name, Provider: configuration.ClaudeProvider{Endpoint: provider.Endpoint, AuthMode: mode}}
	if models := provider.Models; models != nil {
		input.Provider.Models = configuration.ClaudeModels{Default: models.DefaultModel, Haiku: models.Haiku, Sonnet: models.Sonnet, Opus: models.Opus}
	}
	secrets, err := configurationSecretInput(mutations)
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	return input, secrets, err
}

func configurationClearSecrets(secrets []configuration.SecretMutation) {
	for index := range secrets {
		secrets[index].Replacement.Clear()
	}
}

func configurationSecretInput(mutations []*configurationv1.SecretMutation) (_ []configuration.SecretMutation, finalErr error) {
	secrets := make([]configuration.SecretMutation, 0, len(mutations))
	defer func() {
		if finalErr != nil {
			configurationClearSecrets(secrets)
		}
	}()
	for _, mutation := range mutations {
		if mutation == nil {
			return nil, configuration.ErrInvalid
		}
		secret := configuration.SecretMutation{Slot: configuration.Slot(mutation.SlotId)}
		switch action := mutation.Action.(type) {
		case *configurationv1.SecretMutation_Keep:
			if action == nil || action.Keep == nil {
				return nil, configuration.ErrInvalid
			}
			secret.Action = configuration.Keep
		case *configurationv1.SecretMutation_Clear:
			if action == nil || action.Clear == nil {
				return nil, configuration.ErrInvalid
			}
			secret.Action = configuration.Clear
		case *configurationv1.SecretMutation_Replace:
			if action == nil {
				return nil, configuration.ErrInvalid
			}
			value := []byte(action.Replace)
			secret.Action, secret.Replacement = configuration.Replace, configuration.NewSecret(value)
			clear(value)
		default:
			return nil, configuration.ErrInvalid
		}
		secrets = append(secrets, secret)
	}
	return secrets, nil
}

func configurationAsset(entry state.ConfigurationAsset) (*configurationv1.Asset, error) {
	var kind configurationv1.AssetKind
	switch entry.Kind {
	case state.ConfigurationKindProvider:
		kind = configurationv1.AssetKind_ASSET_KIND_PROVIDER
	case state.ConfigurationKindMCP:
		kind = configurationv1.AssetKind_ASSET_KIND_MCP
	case state.ConfigurationKindPrompt:
		kind = configurationv1.AssetKind_ASSET_KIND_PROMPT
	case state.ConfigurationKindAgentSettings:
		kind = configurationv1.AssetKind_ASSET_KIND_AGENT_SETTINGS
	case state.ConfigurationKindWorkspace:
		kind = configurationv1.AssetKind_ASSET_KIND_WORKSPACE
	default:
		return nil, configuration.ErrUnavailable
	}
	if entry.SchemaVersion < 1 || uint64(entry.SchemaVersion) > math.MaxUint32 {
		return nil, configuration.ErrUnavailable
	}
	return &configurationv1.Asset{Id: entry.ID, AgentId: entry.AgentID, Kind: kind, Name: entry.Name, SchemaVersion: uint32(entry.SchemaVersion), Revision: entry.Revision, CreatedAtUnix: entry.CreatedAt.Unix(), UpdatedAtUnix: entry.UpdatedAt.Unix()}, nil
}

func configurationView(entry configuration.EditView) (*configurationv1.DraftView, error) {
	if entry.Provider != (configuration.ClaudeProvider{}) && (entry.CodexProvider != nil || entry.GeminiProvider != nil) || entry.CodexProvider != nil && entry.GeminiProvider != nil {
		return nil, configuration.ErrUnavailable
	}
	if entry.GeminiProvider != nil || entry.Metadata.AgentID == "gemini" {
		return configurationGeminiView(entry)
	}
	if entry.CodexProvider != nil || entry.Metadata.AgentID == "codex" {
		return configurationCodexView(entry)
	}
	asset, err := configurationAsset(entry.Metadata)
	if err != nil {
		return nil, err
	}
	var mode configurationv1.ClaudeAuthMode
	switch entry.Provider.AuthMode {
	case configuration.NativeLogin:
		mode = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_NATIVE_LOGIN
	case configuration.APIKey:
		mode = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY
	case configuration.AuthToken:
		mode = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_AUTH_TOKEN
	default:
		return nil, configuration.ErrUnavailable
	}
	var readiness configurationv1.CredentialReadiness
	switch entry.Readiness {
	case configuration.CredentialComplete:
		readiness = configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE
	case configuration.NeedsCredential:
		readiness = configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL
	default:
		return nil, configuration.ErrUnavailable
	}
	models := entry.Provider.Models
	provider := &configurationv1.ClaudeProvider{Endpoint: entry.Provider.Endpoint, AuthMode: mode, Models: &configurationv1.ClaudeModels{DefaultModel: models.Default, Haiku: models.Haiku, Sonnet: models.Sonnet, Opus: models.Opus}}
	view := &configurationv1.DraftView{Asset: asset, Draft: &configurationv1.Draft{Name: asset.Name, Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: provider}}, CredentialReadiness: readiness}
	for _, secret := range entry.Secrets {
		view.Secrets = append(view.Secrets, &configurationv1.SecretStatus{SlotId: string(secret.Slot), Present: secret.Present})
	}
	return view, nil
}
