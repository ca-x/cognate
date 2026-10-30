package importer_test

import (
	"encoding/json/v2"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/importer"
)

func TestSelectionGeminiEndpointDomainParity(test *testing.T) {
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
			encoded, err := json.Marshal(map[string]any{
				"env":    map[string]any{"GOOGLE_GEMINI_BASE_URL": endpoint, "GEMINI_API_KEY": "SYNTHETIC-PRIVATE-MARKER", "GEMINI_MODEL": "gemini-env"},
				"config": map[string]any{"model": map[string]any{"name": "gemini-settings"}, "security": map[string]any{"auth": map[string]any{"selectedType": "gemini-api-key"}}},
			})
			if err != nil {
				test.Fatal("endpoint source encoding failed")
			}
			selectionEndpointDomainParity(test, "gemini", encoded, endpoint, sample.supported)
		})
	}
}

func TestSelectionGeminiIndependentPresence(test *testing.T) {
	for mask := range 4 {
		environment := map[string]any{"GEMINI_API_KEY": "SYNTHETIC-PRIVATE-MARKER"}
		config := map[string]any{}
		if mask&1 != 0 {
			environment["GEMINI_MODEL"] = "gemini-env"
		}
		if mask&2 != 0 {
			config["model"] = map[string]any{"name": "gemini-settings"}
		}
		raw, err := json.Marshal(map[string]any{"env": environment, "config": config})
		if err != nil {
			test.Fatal("synthetic source encoding failed")
		}
		projection := selectionProviderSource(test, "gemini", raw)
		view, _ := projection.View()
		option := importer.SelectionCandidateOptions{CandidateID: view.Configurations[0].ID, GeminiAuthMode: new(agentconfig.GeminiAPIKeyAuth), GeminiKeyTransport: new(agentconfig.GeminiNativeDefault)}
		result, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{option}})
		if err != nil {
			test.Fatal("explicit Gemini API/transport source rejected")
		}
		built, _ := result.View()
		result.Destroy()
		candidate := built.Configurations[0]
		if candidate.Status != importer.SelectionReady || candidate.Draft == nil {
			test.Fatal("Gemini source not ready")
		}
		provider := candidate.Draft.GeminiProvider
		if (provider.EnvModel != nil) != (mask&1 != 0) || (provider.SettingsModel != nil) != (mask&2 != 0) {
			test.Fatal("independent models presence was merged/defaulted")
		}
	}
	for _, mode := range []string{"oauth-personal", "gemini-api-key"} {
		raw, _ := json.Marshal(map[string]any{"env": map[string]any{}, "config": map[string]any{"security": map[string]any{"auth": map[string]any{"selectedType": mode}}}})
		projection := selectionProviderSource(test, "gemini", raw)
		view, _ := projection.View()
		result, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{})
		if err != nil {
			test.Fatal("known Gemini mode source failed")
		}
		built, _ := result.View()
		result.Destroy()
		wanted := importer.SelectionNeedsChoice
		if mode == "oauth-personal" {
			wanted = importer.SelectionReady
		}
		if built.Configurations[0].Status != wanted || view.Configurations[0].Draft == nil {
			test.Fatal("native auth or explicit API transport semantics changed")
		}
	}
}

func TestSelectionGeminiUnknownAndMixedOptions(test *testing.T) {
	for _, raw := range []string{
		`{"env":{"OTHER_ENV":"SYNTHETIC-PRIVATE-MARKER"},"config":{}}`,
		`{"env":{},"config":{"tools":{"command":"SYNTHETIC-PRIVATE-MARKER"}}}`,
		`{"env":{"GEMINI_MODEL":null},"config":{}}`,
		`{"env":{},"config":{"model":{"name":null}}}`,
		`{"env":{},"config":{"security":{"auth":{"selectedType":"future-mode"}}}}`,
	} {
		projection := selectionProviderSource(test, "gemini", []byte(raw))
		view, _ := projection.View()
		if view.Configurations[0].Status != importer.SelectionUnsupportedField || view.Configurations[0].Draft != nil {
			test.Fatal("unknown Gemini functionality silently discarded")
		}
	}
	projection := selectionProviderSource(test, "gemini", []byte(`{"env":{},"config":{}}`))
	view, _ := projection.View()
	for _, option := range []importer.SelectionCandidateOptions{
		{CandidateID: view.Configurations[0].ID, GeminiKeyTransport: new(agentconfig.GeminiBearer)},
		{CandidateID: view.Configurations[0].ID, GeminiAuthMode: new(agentconfig.GeminiNativeLogin), GeminiKeyTransport: new(agentconfig.GeminiBearer)},
		{CandidateID: view.Configurations[0].ID, GeminiAuthMode: new(agentconfig.GeminiAuthMode("unknown"))},
	} {
		if result, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{option}}); err == nil {
			result.Destroy()
			test.Fatal("ambiguous/mixed Gemini option accepted")
		}
	}
}
