package importer

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/pelletier/go-toml/v2"
)

func parseCodexSelection(ctx context.Context, name string, raw []byte) (configuration.Draft, []SelectionSlot, error) {
	if err := ctx.Err(); err != nil {
		return configuration.Draft{}, nil, err
	}
	root, err := sourceObject(raw)
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	defer clearSourceObject(root)
	if !sourceFields(root, "auth", "config", "modelCatalog") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	authentication, err := sourceObject(root["auth"])
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	defer clearSourceObject(authentication)
	if !sourceFields(authentication, "OPENAI_API_KEY") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	slots, err := selectionSlots(authentication, []struct{ field, slot string }{{"OPENAI_API_KEY", "codex.provider-key"}})
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	config, err := sourceString(root["config"])
	if err != nil || len(config) > 256<<10 {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	var document struct {
		ModelProvider   string                            `toml:"model_provider"`
		Model           string                            `toml:"model"`
		ReasoningEffort *agentconfig.CodexReasoningEffort `toml:"model_reasoning_effort"`
		Providers       map[string]struct {
			Name               string `toml:"name"`
			BaseURL            string `toml:"base_url"`
			WireAPI            string `toml:"wire_api"`
			RequiresOpenAIAuth *bool  `toml:"requires_openai_auth"`
		} `toml:"model_providers"`
	}
	if toml.NewDecoder(strings.NewReader(config)).DisallowUnknownFields().Decode(&document) != nil {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	if !selectionModel(document.ModelProvider, 128) || len(document.Providers) != 1 {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	provider, present := document.Providers[document.ModelProvider]
	if !present || !selectionText(provider.Name, 128) || provider.WireAPI != "responses" || provider.RequiresOpenAIAuth == nil || !*provider.RequiresOpenAIAuth || !selectionEndpoint(provider.BaseURL, true) || !selectionModel(document.Model, 128) || document.ReasoningEffort != nil && !selectionEffort(*document.ReasoningEffort) {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	desired := configuration.CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: provider.BaseURL, Model: document.Model, ReasoningEffort: document.ReasoningEffort}
	if value, present := root["modelCatalog"]; present {
		catalog, err := parseSelectionCatalog(ctx, value)
		if err != nil {
			return configuration.Draft{}, nil, err
		}
		desired.Catalog = catalog
		found := false
		for _, model := range catalog.Models {
			if model.Model == desired.Model {
				found = true
				if model.ReasoningLevels != nil && desired.ReasoningEffort != nil && !slices.Contains(*model.ReasoningLevels, *desired.ReasoningEffort) {
					return configuration.Draft{}, nil, ErrSelectionUnsupported
				}
			}
		}
		if !found {
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
	}
	encoded, err := json.Marshal(desired)
	if err != nil || len(encoded) > agentconfig.MaxCodexDesiredBytes {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	return configuration.Draft{Name: name, CodexProvider: &desired}, slots, ctx.Err()
}

func parseSelectionCatalog(ctx context.Context, raw []byte) (*agentconfig.CodexCatalogDesired, error) {
	root, err := sourceObject(raw)
	if err != nil {
		return nil, err
	}
	defer clearSourceObject(root)
	if !sourceFields(root, "models") {
		return nil, ErrSelectionUnsupported
	}
	var rows []jsontext.Value
	if len(root["models"]) == 0 || root["models"][0] != '[' || json.Unmarshal(root["models"], &rows) != nil || len(rows) < 1 || len(rows) > 32 {
		return nil, ErrSelectionUnsupported
	}
	defer func() {
		for _, row := range rows {
			clear(row)
		}
	}()
	catalog := &agentconfig.CodexCatalogDesired{Models: make([]agentconfig.CodexCatalogModelDesired, 0, len(rows))}
	seen := make(map[string]bool)
	for _, rawModel := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		model, err := parseSelectionCatalogModel(rawModel)
		if err != nil || seen[model.Model] {
			return nil, ErrSelectionUnsupported
		}
		seen[model.Model] = true
		catalog.Models = append(catalog.Models, model)
	}
	encoded, err := json.Marshal(catalog)
	if err != nil || len(encoded) > agentconfig.MaxCodexCatalogBytes {
		return nil, ErrSelectionUnsupported
	}
	return catalog, nil
}

func parseSelectionCatalogModel(raw []byte) (agentconfig.CodexCatalogModelDesired, error) {
	object, err := sourceObject(raw)
	if err != nil {
		return agentconfig.CodexCatalogModelDesired{}, err
	}
	defer clearSourceObject(object)
	if !sourceFields(object, "model", "displayName", "contextWindow", "supportsParallelToolCalls", "inputModalities", "baseInstructions", "reasoningLevels", "defaultReasoningLevel") {
		return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
	}
	for _, value := range object {
		if string(value) == "null" {
			return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
		}
	}
	contextWindow, hasWindow := object["contextWindow"]
	delete(object, "contextWindow")
	defer clear(contextWindow)
	encoded, err := json.Marshal(object)
	if err != nil {
		return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
	}
	var model agentconfig.CodexCatalogModelDesired
	if json.Unmarshal(encoded, &model) != nil || !selectionModel(model.Model, 128) {
		return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
	}
	if hasWindow {
		decimal := string(contextWindow)
		if len(contextWindow) > 0 && contextWindow[0] == '"' {
			decimal, err = sourceString(contextWindow)
			if err != nil {
				return agentconfig.CodexCatalogModelDesired{}, err
			}
		}
		if decimal == "" || len(decimal) > 20 || len(decimal) > 1 && decimal[0] == '0' {
			return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
		}
		for _, character := range []byte(decimal) {
			if character < '0' || character > '9' {
				return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
			}
		}
		parsed, err := strconv.ParseUint(decimal, 10, 64)
		if err != nil || parsed == 0 || parsed > 2147483647 {
			return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
		}
		model.ContextWindow = new(parsed)
	}
	if model.DisplayName != nil && (!selectionText(*model.DisplayName, 256) || strings.TrimSpace(*model.DisplayName) != *model.DisplayName) {
		return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
	}
	if model.BaseInstructions != nil && (len(*model.BaseInstructions) == 0 || len(*model.BaseInstructions) > 2048 || !utf8.ValidString(*model.BaseInstructions) || strings.ContainsFunc(*model.BaseInstructions, func(character rune) bool {
		return unicode.IsControl(character) && character != '\r' && character != '\n' && character != '\t'
	})) {
		return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
	}
	if model.InputModalities != nil {
		if len(*model.InputModalities) < 1 || len(*model.InputModalities) > 2 {
			return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
		}
		for index, modality := range *model.InputModalities {
			if modality != "image" && modality != "text" || slices.Contains((*model.InputModalities)[:index], modality) {
				return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
			}
		}
	}
	if model.ReasoningLevels != nil {
		if len(*model.ReasoningLevels) < 1 || len(*model.ReasoningLevels) > 7 {
			return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
		}
		for index, level := range *model.ReasoningLevels {
			if !selectionEffort(level) || slices.Contains((*model.ReasoningLevels)[:index], level) {
				return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
			}
		}
	}
	if model.DefaultReasoningLevel != nil && (model.ReasoningLevels == nil || !slices.Contains(*model.ReasoningLevels, *model.DefaultReasoningLevel)) {
		return agentconfig.CodexCatalogModelDesired{}, ErrSelectionUnsupported
	}
	return model, nil
}

func selectionEffort(value agentconfig.CodexReasoningEffort) bool {
	return slices.Contains([]agentconfig.CodexReasoningEffort{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, value)
}
