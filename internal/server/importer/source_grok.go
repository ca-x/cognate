package importer

import (
	"context"
	"slices"
	"strings"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/pelletier/go-toml/v2"
)

func parseGrokSelection(ctx context.Context, name string, raw []byte) (configuration.Draft, []SelectionSlot, error) {
	if err := ctx.Err(); err != nil {
		return configuration.Draft{}, nil, err
	}
	root, err := sourceObject(raw)
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	defer clearSourceObject(root)
	if !sourceFields(root, "config") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	config, err := sourceString(root["config"])
	if err != nil || len(config) > 256<<10 {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	var document map[string]any
	if toml.NewDecoder(strings.NewReader(config)).Decode(&document) != nil || !grokSourceFields(document, "models", "model") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	models, modelsOK := document["models"].(map[string]any)
	tables, tablesOK := document["model"].(map[string]any)
	if !modelsOK || !grokSourceFields(models, "default") || !tablesOK || len(tables) != 1 {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	profile, profileOK := models["default"].(string)
	model, modelOK := tables[profile].(map[string]any)
	if !profileOK || !modelOK || !grokSourceFields(model, "model", "base_url", "name", "api_backend", "context_window", "api_key", "env_key") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	for _, field := range []string{"model", "base_url", "name", "api_backend"} {
		if _, valid := model[field].(string); !valid {
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
	}
	window, windowOK := model["context_window"].(int64)
	if !windowOK {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	slots := []SelectionSlot{{Slot: string(configuration.GrokBuildAPIKeySlot)}}
	if value, present := model["api_key"]; present {
		key, valid := value.(string)
		if !valid {
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
		slots[0].Present = key != ""
	}
	var environment *string
	if value, present := model["env_key"]; present {
		key, valid := value.(string)
		if !valid {
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
		environment = new(key)
	}
	draft := configuration.Draft{Name: name, GrokBuildProvider: &configuration.GrokBuildProvider{
		Mode:  agentconfig.GrokBuildCustomModel,
		Model: &configuration.GrokBuildModel{Profile: profile, UpstreamModel: model["model"].(string), Endpoint: model["base_url"].(string), Name: model["name"].(string), APIBackend: model["api_backend"].(string), ContextWindow: window, EnvKey: environment},
	}}
	if _, err := configuration.PrepareImportedDraft(ctx, draft); err != nil {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	return draft, slots, ctx.Err()
}

func grokSourceFields(object map[string]any, allowed ...string) bool {
	for field := range object {
		if !slices.Contains(allowed, field) {
			return false
		}
	}
	return true
}
