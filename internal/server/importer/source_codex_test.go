package importer_test

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/importer"
)

func TestSelectionCodexEndpointDomainParity(test *testing.T) {
	for _, sample := range []struct {
		path      string
		supported bool
	}{
		{"%0A", false}, {"%5C", false}, {"%FF", false},
		{"%00", false}, {"%7F", false}, {"%C2%85", false},
		{"%2F", true}, {"%20", true}, {"%E4%B8%AD", true},
		{"a%2fb", true}, {"%252F", true}, {"v1", true},
	} {
		test.Run(sample.path, func(test *testing.T) {
			endpoint := "https://endpoint.example/" + sample.path
			settings := codexSelectionSettings(test)
			settings["config"] = strings.Replace(settings["config"].(string), "https://codex.example/v1", endpoint, 1)
			encoded, err := json.Marshal(settings)
			if err != nil {
				test.Fatal("endpoint source encoding failed")
			}
			selectionEndpointDomainParity(test, "codex", encoded, endpoint, sample.supported)
		})
	}
}

func codexSelectionSettings(test *testing.T) map[string]any {
	test.Helper()
	return map[string]any{
		"auth":         map[string]any{"OPENAI_API_KEY": "SYNTHETIC-PRIVATE-MARKER"},
		"config":       "model_provider = \"custom\"\nmodel = \"codex-a\"\nmodel_reasoning_effort = \"low\"\n[model_providers.custom]\nname = \"Custom\"\nbase_url = \"https://codex.example/v1\"\nwire_api = \"responses\"\nrequires_openai_auth = true\n",
		"modelCatalog": map[string]any{"models": []any{map[string]any{"model": "codex-a", "displayName": "Codex A", "contextWindow": uint64(2147483647), "supportsParallelToolCalls": false, "inputModalities": []string{"image", "text"}, "baseInstructions": "  synthetic\r\n\tprovider instructions  ", "reasoningLevels": []string{"high", "none", "low"}, "defaultReasoningLevel": "low"}}},
	}
}

func codexSelectionModel(settings map[string]any) map[string]any {
	return settings["modelCatalog"].(map[string]any)["models"].([]any)[0].(map[string]any)
}

func TestSelectionProvidersLosslessBoundary(test *testing.T) {
	for _, sample := range []struct {
		name      string
		mutate    func(map[string]any)
		supported bool
	}{
		{"uint64-in-range-string", func(settings map[string]any) { codexSelectionModel(settings)["contextWindow"] = "2147483647" }, true},
		{"absence", func(settings map[string]any) {
			for _, field := range []string{"displayName", "contextWindow", "supportsParallelToolCalls", "inputModalities", "baseInstructions", "reasoningLevels", "defaultReasoningLevel"} {
				delete(codexSelectionModel(settings), field)
			}
		}, true},
		{"catalog-absence", func(settings map[string]any) { delete(settings, "modelCatalog") }, true},
		{"uint64-max-string", func(settings map[string]any) { codexSelectionModel(settings)["contextWindow"] = "18446744073709551615" }, false},
		{"uint64-max-number", func(settings map[string]any) { codexSelectionModel(settings)["contextWindow"] = ^uint64(0) }, false},
		{"domain-range", func(settings map[string]any) { codexSelectionModel(settings)["contextWindow"] = uint64(2147483648) }, false},
		{"overflow", func(settings map[string]any) { codexSelectionModel(settings)["contextWindow"] = "18446744073709551616" }, false},
		{"leading-zero", func(settings map[string]any) { codexSelectionModel(settings)["contextWindow"] = "01" }, false},
		{"signed", func(settings map[string]any) { codexSelectionModel(settings)["contextWindow"] = "+1" }, false},
		{"fraction", func(settings map[string]any) { codexSelectionModel(settings)["contextWindow"] = 1.5 }, false},
		{"null", func(settings map[string]any) { codexSelectionModel(settings)["supportsParallelToolCalls"] = nil }, false},
		{"empty-set", func(settings map[string]any) { codexSelectionModel(settings)["inputModalities"] = []string{} }, false},
		{"duplicate-set", func(settings map[string]any) {
			codexSelectionModel(settings)["inputModalities"] = []string{"text", "text"}
		}, false},
		{"unknown-level", func(settings map[string]any) { codexSelectionModel(settings)["reasoningLevels"] = []string{"future"} }, false},
		{"snake-alias", func(settings map[string]any) { codexSelectionModel(settings)["context_window"] = 1024 }, false},
		{"unknown-model-field", func(settings map[string]any) { codexSelectionModel(settings)["newSecret"] = "SYNTHETIC-PRIVATE-MARKER" }, false},
		{"auto-review", func(settings map[string]any) {
			settings["modelCatalog"].(map[string]any)["autoReviewModel"] = "codex-a"
		}, false},
		{"oauth", func(settings map[string]any) {
			settings["auth"].(map[string]any)["access_token"] = "SYNTHETIC-PRIVATE-MARKER"
		}, false},
		{"unknown-toml", func(settings map[string]any) {
			settings["config"] = "disable_response_storage = true\n" + settings["config"].(string)
		}, false},
		{"malformed-toml", func(settings map[string]any) { settings["config"] = "not = [SYNTHETIC-PRIVATE-MARKER" }, false},
	} {
		test.Run(sample.name, func(test *testing.T) {
			settings := codexSelectionSettings(test)
			sample.mutate(settings)
			encoded, err := json.Marshal(settings)
			if err != nil {
				test.Fatal("synthetic source encoding failed")
			}
			projection := selectionProviderSource(test, "codex", encoded)
			view, _ := projection.View()
			candidate := view.Configurations[0]
			if !sample.supported {
				if candidate.Status != importer.SelectionUnsupportedField || candidate.Draft != nil {
					test.Fatal("out-of-domain or unknown provider was silently transformed")
				}
				return
			}
			result, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{})
			if err != nil {
				test.Fatal("qualified Codex source rejected by real adapter")
			}
			defer result.Destroy()
			built, _ := result.View()
			provider := built.Configurations[0]
			if provider.Status != importer.SelectionReady || provider.Draft == nil {
				test.Fatal("qualified source not available")
			}
			if sample.name == "absence" {
				model := provider.Draft.CodexProvider.Catalog.Models[0]
				if model.ContextWindow != nil || model.SupportsParallelToolCalls != nil || model.InputModalities != nil || model.ReasoningLevels != nil {
					test.Fatal("absence was replaced with defaults")
				}
			}
		})
	}
}
