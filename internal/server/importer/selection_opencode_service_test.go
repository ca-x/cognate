package importer

import (
	"os"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
)

func TestSelectionOpenCodeServicePreview(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	schema, err := os.ReadFile("testdata/selection/ccswitch-schema24.sql")
	if err != nil {
		test.Fatal(err)
	}
	raw := selectionServiceSnapshot(test, string(schema)+`INSERT INTO providers VALUES('opencode-source','opencode','OpenCode imported','{"options":{"apiKey":"SYNTHETIC-PRIVATE-MARKER"}}','{"opencodeConfigFormat":"v1"}',NULL,NULL,0,0,NULL,NULL,NULL,0,0);`)
	resolved, err := fixture.service.resolve(test.Context(), fixture.owner, SelectionCCSwitch24, raw, SelectionOptions{})
	if err != nil {
		test.Fatal("resolution failed", err)
	}
	resolved.destroy()
	preview := selectionServicePreview(test, fixture, SelectionCCSwitch24, raw, SelectionOptions{})
	preview.Destroy()
}

func TestSelectionOpenCodeTranscriptBindsTypedFields(test *testing.T) {
	draft := func() configuration.Draft {
		return configuration.Draft{Name: "Imported", OpenCodeProvider: &agentconfig.OpenCodeProvider{ProviderID: "work", Format: "v1", V1: &agentconfig.OpenCodeV1{NPM: new("@ai-sdk/openai-compatible"), Options: &agentconfig.OpenCodeV1Options{APIKey: new("opencode.api-key"), BaseURL: new("https://example.invalid/v1"), Headers: new(map[string]string{"A": "opencode.value.01", "B": "opencode.value.02"})}, Models: new(map[string]agentconfig.OpenCodeV1Model{"a": {Name: new("Model A"), Limit: &agentconfig.OpenCodeLimit{Context: new("100000")}}})}}}
	}
	digest := func(input configuration.Draft) [32]byte {
		transcript := selectionTranscriptFor("test/opencode")
		transcript.draft(&input)
		value, err := transcript.result()
		if err != nil {
			test.Fatal("valid OpenCode transcript unavailable")
		}
		return value
	}
	baseline := digest(draft())
	for name, mutate := range map[string]func(*configuration.Draft){
		"asset name":           func(input *configuration.Draft) { input.Name += " changed" },
		"provider id":          func(input *configuration.Draft) { input.OpenCodeProvider.ProviderID += "-new" },
		"npm absent":           func(input *configuration.Draft) { input.OpenCodeProvider.V1.NPM = nil },
		"options absent":       func(input *configuration.Draft) { input.OpenCodeProvider.V1.Options = nil },
		"credential reference": func(input *configuration.Draft) { input.OpenCodeProvider.V1.Options.APIKey = new("opencode.value.03") },
		"endpoint": func(input *configuration.Draft) {
			input.OpenCodeProvider.V1.Options.BaseURL = new("https://other.invalid/v1")
		},
		"header reference": func(input *configuration.Draft) {
			(*input.OpenCodeProvider.V1.Options.Headers)["A"] = "opencode.value.03"
		},
		"models absent": func(input *configuration.Draft) { input.OpenCodeProvider.V1.Models = nil },
		"models empty": func(input *configuration.Draft) {
			input.OpenCodeProvider.V1.Models = new(map[string]agentconfig.OpenCodeV1Model{})
		},
		"numeric field": func(input *configuration.Draft) {
			(*input.OpenCodeProvider.V1.Models)["a"].Limit.Context = new("100001")
		},
	} {
		test.Run(name, func(test *testing.T) {
			input := draft()
			mutate(&input)
			if digest(input) == baseline {
				test.Fatal("changed OpenCode field did not invalidate review")
			}
		})
	}
	input := draft()
	input.OpenCodeProvider.V1.Options.Headers = new(map[string]string{"B": "opencode.value.02", "A": "opencode.value.01"})
	if digest(input) != baseline {
		test.Fatal("map insertion order changed canonical transcript")
	}
	input.CodexProvider = &configuration.CodexProvider{}
	transcript := selectionTranscriptFor("test/opencode")
	transcript.draft(&input)
	if _, err := transcript.result(); err == nil {
		test.Fatal("mixed agent draft accepted")
	}
}
