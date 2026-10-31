package importer_test

import (
	"bytes"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/importer"
)

func grokSelectionSettings(test *testing.T, config string) []byte {
	test.Helper()
	encoded, err := json.Marshal(map[string]string{"config": config})
	if err != nil {
		test.Fatal(err)
	}
	return encoded
}

const grokSelectionConfig = "[models]\ndefault='work'\n[model.work]\nmodel='grok-model'\nbase_url='https://grok.example/v1'\nname='Work model'\napi_backend='responses'\ncontext_window=9007199254740991\napi_key='SYNTHETIC-PRIVATE-MARKER'\nenv_key='GROK_API_KEY'\n"

func TestSelectionGrokProjectionAndAdapter(test *testing.T) {
	projection := selectionProviderSource(test, "grokbuild", grokSelectionSettings(test, grokSelectionConfig))
	view, err := projection.View()
	if err != nil || len(view.Configurations) < 1 {
		test.Fatal("source inventory unavailable")
	}
	candidate := view.Configurations[0]
	if candidate.Status != importer.SelectionReady || candidate.Draft == nil || candidate.Draft.GrokBuildProvider == nil {
		test.Fatal("explicit Grok model not ready")
	}
	provider := candidate.Draft.GrokBuildProvider
	model := provider.Model
	if provider.Mode != agentconfig.GrokBuildCustomModel || model == nil || model.Profile != "work" || model.UpstreamModel != "grok-model" || model.Endpoint != "https://grok.example/v1" || model.Name != "Work model" || model.APIBackend != "responses" || model.ContextWindow != agentconfig.MaxGrokBuildContextWindow || model.EnvKey == nil || *model.EnvKey != "GROK_API_KEY" {
		test.Fatal("source fields changed or integer precision lost")
	}
	if len(candidate.Slots) != 1 || candidate.Slots[0].Slot != "grokbuild.api-key" || !candidate.Slots[0].Present {
		test.Fatal("credential presence not separated")
	}
	built, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{{CandidateID: candidate.ID, Name: new("Renamed")}}})
	if err != nil {
		test.Fatal("fixed adapter rejected Grok draft")
	}
	defer built.Destroy()
	result, err := built.View()
	if err != nil || result.Configurations[0].Draft.GrokBuildProvider.Model.ContextWindow != model.ContextWindow || result.Configurations[0].Draft.Name != "Renamed" {
		test.Fatal("adapter changed model or rename")
	}
	for _, value := range []any{view, result} {
		encoded, err := json.Marshal(value)
		if err != nil || bytes.Contains(encoded, []byte("SYNTHETIC-PRIVATE-MARKER")) {
			test.Fatal("source credential escaped safe projection")
		}
	}
	_, err = importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{{CandidateID: candidate.ID, ClaudeAuthMode: new(configuration.APIKey)}}})
	if err == nil {
		test.Fatal("cross-agent option accepted")
	}
}

func TestSelectionGrokRejectsLossySources(test *testing.T) {
	for name, config := range map[string]string{
		"empty":             "",
		"missing reference": strings.ReplaceAll(grokSelectionConfig, "default='work'", "default='missing'"),
		"multiple models":   grokSelectionConfig + "[model.other]\nmodel='another'\n",
		"unknown root":      "extra=true\n" + grokSelectionConfig,
		"unknown models":    strings.ReplaceAll(grokSelectionConfig, "[models]", "[models]\nextra=true"),
		"unknown model":     grokSelectionConfig + "extra=true\n",
		"missing field":     strings.ReplaceAll(grokSelectionConfig, "api_backend='responses'\n", ""),
		"wrong secret type": strings.ReplaceAll(grokSelectionConfig, "api_key='SYNTHETIC-PRIVATE-MARKER'", "api_key=7"),
		"empty env":         strings.ReplaceAll(grokSelectionConfig, "env_key='GROK_API_KEY'", "env_key=''"),
		"float window":      strings.ReplaceAll(grokSelectionConfig, "9007199254740991", "500000.0"),
		"negative window":   strings.ReplaceAll(grokSelectionConfig, "9007199254740991", "-1"),
		"window limit":      strings.ReplaceAll(grokSelectionConfig, "9007199254740991", "9007199254740992"),
		"overflow":          strings.ReplaceAll(grokSelectionConfig, "9007199254740991", "9223372036854775808"),
		"insecure endpoint": strings.ReplaceAll(grokSelectionConfig, "https://", "http://"),
		"unknown backend":   strings.ReplaceAll(grokSelectionConfig, "responses", "chat"),
		"case root":         strings.ReplaceAll(grokSelectionConfig, "[models]", "[MODELS]"),
		"case models field": strings.ReplaceAll(grokSelectionConfig, "default='work'", "DEFAULT='work'"),
		"case model table":  strings.ReplaceAll(grokSelectionConfig, "[model.work]", "[MODEL.work]"),
		"case model field":  strings.ReplaceAll(grokSelectionConfig, "model='grok-model'", "MODEL='grok-model'"),
		"case overwrite":    grokSelectionConfig + "MODEL='overridden'\n",
	} {
		test.Run(name, func(test *testing.T) {
			projection := selectionProviderSource(test, "grokbuild", grokSelectionSettings(test, config))
			view, err := projection.View()
			if err != nil || view.Configurations[0].Status != importer.SelectionUnsupportedField || view.Configurations[0].Draft != nil {
				test.Fatal("lossy or invalid source became portable")
			}
		})
	}
}

func TestSelectionGrokOptionalPresence(test *testing.T) {
	for _, secret := range []string{"", "api_key=''\n", "api_key='SYNTHETIC-PRIVATE-MARKER'\n"} {
		for _, environment := range []string{"", "env_key='GROK_API_KEY'\n"} {
			config := strings.ReplaceAll(grokSelectionConfig, "api_key='SYNTHETIC-PRIVATE-MARKER'\nenv_key='GROK_API_KEY'\n", secret+environment)
			projection := selectionProviderSource(test, "grokbuild", grokSelectionSettings(test, config))
			view, err := projection.View()
			if err != nil || view.Configurations[0].Draft == nil {
				test.Fatal("optional fields were made mandatory")
			}
			candidate := view.Configurations[0]
			if candidate.Slots[0].Present != strings.Contains(secret, "SYNTHETIC") || (candidate.Draft.GrokBuildProvider.Model.EnvKey != nil) != (environment != "") {
				test.Fatal("optional presence was defaulted or merged")
			}
		}
	}
}
