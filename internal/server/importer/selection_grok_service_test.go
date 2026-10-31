package importer

import (
	"encoding/json/v2"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func selectionGrokDraft() configuration.Draft {
	return configuration.Draft{Name: "Imported", GrokBuildProvider: &configuration.GrokBuildProvider{Mode: agentconfig.GrokBuildCustomModel, Model: &configuration.GrokBuildModel{Profile: "work", UpstreamModel: "grok-model", Endpoint: "https://grok.example/v1", Name: "Work model", APIBackend: "responses", ContextWindow: agentconfig.MaxGrokBuildContextWindow, EnvKey: new("GROK_API_KEY")}}}
}

func TestSelectionGrokTranscriptBindsEveryField(test *testing.T) {
	digest := func(draft configuration.Draft) ([32]byte, error) {
		transcript := selectionTranscriptFor("test/grok")
		transcript.draft(&draft)
		return transcript.result()
	}
	baseline, err := digest(selectionGrokDraft())
	if err != nil {
		test.Fatal(err)
	}
	for name, mutate := range map[string]func(*configuration.Draft){
		"name":           func(draft *configuration.Draft) { draft.Name += " changed" },
		"mode":           func(draft *configuration.Draft) { draft.GrokBuildProvider.Mode = agentconfig.GrokBuildNativeLogin },
		"model presence": func(draft *configuration.Draft) { draft.GrokBuildProvider.Model = nil },
		"profile":        func(draft *configuration.Draft) { draft.GrokBuildProvider.Model.Profile += "-new" },
		"upstream":       func(draft *configuration.Draft) { draft.GrokBuildProvider.Model.UpstreamModel += "-new" },
		"endpoint":       func(draft *configuration.Draft) { draft.GrokBuildProvider.Model.Endpoint += "/new" },
		"model name":     func(draft *configuration.Draft) { draft.GrokBuildProvider.Model.Name += " new" },
		"backend":        func(draft *configuration.Draft) { draft.GrokBuildProvider.Model.APIBackend = "other" },
		"window":         func(draft *configuration.Draft) { draft.GrokBuildProvider.Model.ContextWindow-- },
		"env absent":     func(draft *configuration.Draft) { draft.GrokBuildProvider.Model.EnvKey = nil },
		"env empty":      func(draft *configuration.Draft) { draft.GrokBuildProvider.Model.EnvKey = new("") },
		"env value":      func(draft *configuration.Draft) { draft.GrokBuildProvider.Model.EnvKey = new("OTHER_KEY") },
	} {
		test.Run(name, func(test *testing.T) {
			draft := selectionGrokDraft()
			mutate(&draft)
			changed, err := digest(draft)
			if err != nil || changed == baseline {
				test.Fatal("review transcript did not bind Grok field")
			}
		})
	}
	for _, mutate := range []func(*configuration.Draft){
		func(draft *configuration.Draft) { draft.Provider.AuthMode = configuration.APIKey },
		func(draft *configuration.Draft) { draft.CodexProvider = &configuration.CodexProvider{} },
		func(draft *configuration.Draft) { draft.GeminiProvider = &configuration.GeminiProvider{} },
		func(draft *configuration.Draft) { draft.OpenCodeProvider = &agentconfig.OpenCodeProvider{} },
	} {
		draft := selectionGrokDraft()
		mutate(&draft)
		if _, err := digest(draft); err == nil {
			test.Fatal("ambiguous draft accepted")
		}
	}
}

func TestSelectionGrokCommitReadback(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	config := "[models]\ndefault='work'\n[model.work]\nmodel='grok-model'\nbase_url='https://grok.example/v1'\nname='Work model'\napi_backend='responses'\ncontext_window=9007199254740991\napi_key='SYNTHETIC-PRIVATE-MARKER'\nenv_key='GROK_API_KEY'\n"
	settings, err := json.Marshal(map[string]string{"config": config})
	if err != nil {
		test.Fatal(err)
	}
	schema, err := os.ReadFile("testdata/selection/ccswitch-schema24.sql")
	if err != nil {
		test.Fatal(err)
	}
	raw := selectionServiceSnapshot(test, string(schema)+`DELETE FROM providers; DELETE FROM skills; DELETE FROM skill_repos; DELETE FROM mcp_servers; DELETE FROM prompts; INSERT INTO providers VALUES('isolated','grokbuild','Imported','`+strings.ReplaceAll(string(settings), "'", "''")+`','{}',NULL,NULL,0,0,NULL,NULL,NULL,0,0);`)
	preview := selectionServicePreview(test, fixture, SelectionCCSwitch24, raw, SelectionOptions{})
	view, err := preview.View()
	if err != nil || len(view.Configurations) != 1 || view.Configurations[0].Status != SelectionCandidateReady {
		test.Fatal("Grok selection preview unavailable")
	}
	input := SelectionCommitInput{Ticket: preview.RevealTicket(), Source: SelectionCCSwitch24, SourceData: raw, ConfigurationIDs: []string{view.Configurations[0].ID}, OperationID: [16]byte{91}}
	for _, notice := range selectionAllNotices(view) {
		input.AckNoticeIDs = append(input.AckNoticeIDs, notice.ID)
	}
	receipt, err := selectionServiceCommit(test, fixture, input)
	if err != nil || receipt.Phase != state.ImportCompleted || len(receipt.CreatedAssets) != 1 || receipt.CreatedAssets[0].AgentID != "grokbuild" {
		test.Fatal("Grok typed asset not committed", err)
	}
	service := configuration.New(fixture.store)
	edit, err := service.Edit(test.Context(), fixture.owner, receipt.CreatedAssets[0].ID)
	if err != nil || !reflect.DeepEqual(edit.GrokBuildProvider, selectionGrokDraft().GrokBuildProvider) || len(edit.Secrets) != 1 || edit.Secrets[0].Present {
		test.Fatal("Grok fields or omitted credential changed after encrypted SQLite readback")
	}
	if fixture.counts.size() != 0 {
		test.Fatal("configuration-only import fetched repository")
	}
}
