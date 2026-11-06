package importclient

import (
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
)

func selectionOpenCodeSlots(source *configurationv1.OpenCodeProvider) ([]string, bool) {
	if source == nil || source.GetV1() == nil {
		return nil, false
	}
	input := source.GetV1()
	if input.Extensions != nil {
		return nil, false
	}
	provider := agentconfig.OpenCodeV1{NPM: input.Npm, Name: input.Name}
	if options := input.Options; options != nil {
		if options.Extensions != nil {
			return nil, false
		}
		provider.Options = &agentconfig.OpenCodeV1Options{BaseURL: options.BaseUrl, APIKey: options.ApiKey}
		if options.Headers != nil {
			entries := options.Headers.Entries
			if entries == nil {
				entries = map[string]string{}
			}
			provider.Options.Headers = &entries
		}
	}
	if input.Models != nil {
		models := make(map[string]agentconfig.OpenCodeV1Model, len(input.Models.Entries))
		for id, input := range input.Models.Entries {
			if input == nil || input.Extensions != nil || input.Options != nil || input.Thinking != nil || input.Cost != nil || input.Variants != nil {
				return nil, false
			}
			model := agentconfig.OpenCodeV1Model{Name: input.Name}
			if limit := input.Limit; limit != nil {
				if limit.Extensions != nil {
					return nil, false
				}
				model.Limit = &agentconfig.OpenCodeLimit{Context: limit.Context, Input: limit.Input, Output: limit.Output}
			}
			if modalities := input.Modalities; modalities != nil {
				if modalities.Extensions != nil {
					return nil, false
				}
				model.Modalities = &agentconfig.OpenCodeModalities{}
				for _, entry := range []struct {
					source *configurationv1.OpenCodeStrings
					target **[]string
				}{{modalities.Input, &model.Modalities.Input}, {modalities.Output, &model.Modalities.Output}} {
					if entry.source != nil {
						values := entry.source.Values
						if values == nil {
							values = []string{}
						}
						*entry.target = &values
					}
				}
			}
			models[id] = model
		}
		provider.Models = &models
	}
	slots, err := agentconfig.OpenCodeRequiredSlots(agentconfig.OpenCodeProvider{ProviderID: source.ProviderId, Format: "v1", V1: &provider})
	return slots, err == nil
}
