package httpapi

import (
	"slices"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func configurationClonePointer[Value any](value *Value) *Value {
	if value == nil {
		return nil
	}
	return new(*value)
}

func configurationCodexEfforts() []agentconfig.CodexReasoningEffort {
	return []agentconfig.CodexReasoningEffort{"", "none", "minimal", "low", "medium", "high", "xhigh", "max"}
}

func configurationCodexEffort(value configurationv1.CodexReasoningEffort) (agentconfig.CodexReasoningEffort, bool) {
	values := configurationCodexEfforts()
	if value <= 0 || int(value) >= len(values) {
		return "", false
	}
	return values[value], true
}

func configurationCodexInput(draft *configurationv1.Draft, mutations []*configurationv1.SecretMutation) (configuration.Draft, []configuration.SecretMutation, error) {
	invalid := func() (configuration.Draft, []configuration.SecretMutation, error) {
		return configuration.Draft{}, nil, configuration.ErrInvalid
	}
	source := draft.GetCodexProvider()
	if source.AuthMode != configurationv1.CodexAuthMode_CODEX_AUTH_MODE_PROVIDER_KEY {
		return invalid()
	}
	provider := &configuration.CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: source.Endpoint, Model: source.Model}
	if source.ReasoningEffort != nil {
		value, valid := configurationCodexEffort(*source.ReasoningEffort)
		if !valid {
			return invalid()
		}
		provider.ReasoningEffort = new(value)
	}
	if source.Catalog != nil {
		provider.Catalog = &agentconfig.CodexCatalogDesired{Models: make([]agentconfig.CodexCatalogModelDesired, 0, len(source.Catalog.Models))}
		for _, sourceModel := range source.Catalog.Models {
			if sourceModel == nil {
				return invalid()
			}
			model := agentconfig.CodexCatalogModelDesired{Model: sourceModel.Model, DisplayName: configurationClonePointer(sourceModel.DisplayName), ContextWindow: configurationClonePointer(sourceModel.ContextWindow), SupportsParallelToolCalls: configurationClonePointer(sourceModel.SupportsParallelToolCalls), BaseInstructions: configurationClonePointer(sourceModel.BaseInstructions)}
			if sourceModel.InputModalities != nil {
				values := make([]agentconfig.CodexModality, 0, len(sourceModel.InputModalities.Values))
				for _, value := range sourceModel.InputModalities.Values {
					switch value {
					case configurationv1.CodexModality_CODEX_MODALITY_TEXT:
						values = append(values, "text")
					case configurationv1.CodexModality_CODEX_MODALITY_IMAGE:
						values = append(values, "image")
					default:
						return invalid()
					}
				}
				model.InputModalities = &values
			}
			if sourceModel.ReasoningLevels != nil {
				values := make([]agentconfig.CodexReasoningEffort, 0, len(sourceModel.ReasoningLevels.Values))
				for _, sourceValue := range sourceModel.ReasoningLevels.Values {
					value, valid := configurationCodexEffort(sourceValue)
					if !valid {
						return invalid()
					}
					values = append(values, value)
				}
				model.ReasoningLevels = &values
			}
			if sourceModel.DefaultReasoningLevel != nil {
				value, valid := configurationCodexEffort(*sourceModel.DefaultReasoningLevel)
				if !valid {
					return invalid()
				}
				model.DefaultReasoningLevel = new(value)
			}
			provider.Catalog.Models = append(provider.Catalog.Models, model)
		}
	}
	for _, mutation := range mutations {
		if mutation == nil || mutation.SlotId != string(configuration.CodexProviderKeySlot) {
			return invalid()
		}
	}
	secrets, err := configurationSecretInput(mutations)
	if err != nil {
		return invalid()
	}
	return configuration.Draft{Name: draft.Name, CodexProvider: provider}, secrets, nil
}

func configurationCodexView(entry configuration.EditView) (*configurationv1.DraftView, error) {
	invalid := func() (*configurationv1.DraftView, error) { return nil, configuration.ErrUnavailable }
	if entry.Metadata.AgentID != "codex" || entry.Metadata.Kind != state.ConfigurationKindProvider || entry.Metadata.SchemaVersion != 1 || entry.CodexProvider == nil || entry.CodexProvider.AuthMode != agentconfig.CodexProviderKeyAuth || len(entry.Secrets) != 1 || entry.Secrets[0].Slot != configuration.CodexProviderKeySlot {
		return invalid()
	}
	readiness := configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL
	if entry.Secrets[0].Present {
		if entry.Readiness != configuration.CredentialComplete {
			return invalid()
		}
		readiness = configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE
	} else if entry.Readiness != configuration.NeedsCredential {
		return invalid()
	}
	asset, err := configurationAsset(entry.Metadata)
	if err != nil {
		return nil, err
	}
	source := entry.CodexProvider
	provider := &configurationv1.CodexProvider{AuthMode: configurationv1.CodexAuthMode_CODEX_AUTH_MODE_PROVIDER_KEY, Endpoint: source.Endpoint, Model: source.Model}
	effort := func(value agentconfig.CodexReasoningEffort) *configurationv1.CodexReasoningEffort {
		index := slices.Index(configurationCodexEfforts(), value)
		if index <= 0 {
			return nil
		}
		return new(configurationv1.CodexReasoningEffort(index))
	}
	if source.ReasoningEffort != nil {
		provider.ReasoningEffort = effort(*source.ReasoningEffort)
		if provider.ReasoningEffort == nil {
			return invalid()
		}
	}
	if source.Catalog != nil {
		provider.Catalog = &configurationv1.CodexCatalog{Models: make([]*configurationv1.CodexCatalogModel, 0, len(source.Catalog.Models))}
		for _, sourceModel := range source.Catalog.Models {
			model := &configurationv1.CodexCatalogModel{Model: sourceModel.Model, DisplayName: configurationClonePointer(sourceModel.DisplayName), ContextWindow: configurationClonePointer(sourceModel.ContextWindow), SupportsParallelToolCalls: configurationClonePointer(sourceModel.SupportsParallelToolCalls), BaseInstructions: configurationClonePointer(sourceModel.BaseInstructions)}
			if sourceModel.InputModalities != nil {
				model.InputModalities = &configurationv1.CodexModalities{}
				for _, value := range *sourceModel.InputModalities {
					switch value {
					case "text":
						model.InputModalities.Values = append(model.InputModalities.Values, configurationv1.CodexModality_CODEX_MODALITY_TEXT)
					case "image":
						model.InputModalities.Values = append(model.InputModalities.Values, configurationv1.CodexModality_CODEX_MODALITY_IMAGE)
					default:
						return invalid()
					}
				}
			}
			if sourceModel.ReasoningLevels != nil {
				model.ReasoningLevels = &configurationv1.CodexReasoningLevels{}
				for _, value := range *sourceModel.ReasoningLevels {
					converted := effort(value)
					if converted == nil {
						return invalid()
					}
					model.ReasoningLevels.Values = append(model.ReasoningLevels.Values, *converted)
				}
			}
			if sourceModel.DefaultReasoningLevel != nil {
				model.DefaultReasoningLevel = effort(*sourceModel.DefaultReasoningLevel)
				if model.DefaultReasoningLevel == nil {
					return invalid()
				}
			}
			provider.Catalog.Models = append(provider.Catalog.Models, model)
		}
	}
	return &configurationv1.DraftView{Asset: asset, Draft: &configurationv1.Draft{Name: asset.Name, Payload: &configurationv1.Draft_CodexProvider{CodexProvider: provider}}, Secrets: []*configurationv1.SecretStatus{{SlotId: string(configuration.CodexProviderKeySlot), Present: entry.Secrets[0].Present}}, CredentialReadiness: readiness, HasPreservedExtras: entry.HasPreservedExtras}, nil
}
