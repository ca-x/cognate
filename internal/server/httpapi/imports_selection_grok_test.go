package httpapi

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/importer"
)

func TestSelectionGrokRealConnectClient(test *testing.T) {
	config := "[models]\ndefault='work'\n[model.work]\nmodel='grok-model'\nbase_url='https://grok.example/v1'\nname='Work model'\napi_backend='responses'\ncontext_window=9007199254740991\napi_key='SYNTHETIC-PRIVATE-MARKER'\n"
	settings, err := json.Marshal(map[string]string{"config": config})
	if err != nil {
		test.Fatal(err)
	}
	statement := `INSERT INTO providers VALUES('grok-source','grokbuild','Grok imported','` + strings.ReplaceAll(string(settings), "'", "''") + `','{}',NULL,NULL,0,0,NULL,NULL,NULL,0,0);`
	for _, lost := range []bool{false, true} {
		test.Run(fmt.Sprint(lost), func(test *testing.T) {
			selectionClientRealStore(test, 4, lost, statement)
		})
	}
}

func TestImportSelectionGrokMapping(test *testing.T) {
	input := configuration.Draft{Name: "Imported", GrokBuildProvider: &configuration.GrokBuildProvider{Mode: agentconfig.GrokBuildCustomModel, Model: &configuration.GrokBuildModel{Profile: "profile", UpstreamModel: "upstream", Endpoint: "https://grok.example/v1", Name: "Model", APIBackend: "responses", ContextWindow: agentconfig.MaxGrokBuildContextWindow, EnvKey: new("GROK_KEY")}}}
	candidate := importer.SelectionConfigurationCandidate{KnownAgentID: "grokbuild", SafeName: input.Name, Draft: &input}
	result, err := importSelectionDraft(candidate)
	if err != nil || result.GetGrokbuildProvider() == nil || result.GetGrokbuildProvider().Mode != configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL || result.GetGrokbuildProvider().Model.ContextWindow != agentconfig.MaxGrokBuildContextWindow || result.GetGrokbuildProvider().Model.GetEnvKey() != "GROK_KEY" {
		test.Fatal("Grok import mapping unavailable")
	}
	*input.GrokBuildProvider.Model.EnvKey = "CHANGED"
	if result.GetGrokbuildProvider().Model.GetEnvKey() != "GROK_KEY" {
		test.Fatal("protocol retains mutable source pointer")
	}
	for _, agent := range []string{"claude", "codex", "gemini"} {
		candidate.KnownAgentID = agent
		if _, err := importSelectionDraft(candidate); err == nil {
			test.Fatal("mixed-agent draft accepted")
		}
	}
	candidate.KnownAgentID = "grokbuild"
	input.GrokBuildProvider.Model.EnvKey = new("")
	if _, err := importSelectionDraft(candidate); err == nil {
		test.Fatal("invalid explicit environment variable accepted")
	}
}
