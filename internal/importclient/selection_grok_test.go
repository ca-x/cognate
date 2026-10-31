package importclient_test

import (
	"fmt"
	"strings"
	"testing"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/importclient"
)

func TestSelectionImportNameByteContract(test *testing.T) {
	for index, name := range []string{strings.Repeat("a", 81), strings.Repeat("a", 128), strings.Repeat("界", 42) + "ab", strings.Repeat("a", 129), strings.Repeat("界", 43)} {
		test.Run(fmt.Sprint(index), func(test *testing.T) {
			client, fixture, path := selectionFixture(test)
			candidate := fixture.preview.Configurations[0]
			candidate.SafeName, candidate.Draft.Name = name, name
			fixture.mutate = func(receipt *importv1.ImportReceipt) {
				for _, asset := range receipt.CreatedAssets {
					asset.Name = name
				}
			}
			preview, err := client.PreviewFile(test.Context(), importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24, path, "", nil)
			if len(name) > 128 {
				if preview != nil {
					preview.Close()
				}
				if err == nil {
					test.Fatal("oversize name accepted")
				}
				return
			}
			if err != nil {
				test.Fatal("valid source name rejected", err)
			}
			defer preview.Close()
			operation, err := client.BeginSelection(preview, importclient.SelectionChoice{Configurations: []int{1}, AcknowledgedNotices: []string{fixture.preview.Notices[0].Id}})
			if err != nil {
				test.Fatal(err)
			}
			defer operation.Close()
			if _, err := operation.CommitFile(test.Context(), path); err != nil {
				test.Fatal("valid receipt name rejected", err)
			}
		})
	}
}

func TestSelectionGrokClientPreviewAndReceipt(test *testing.T) {
	client, fixture, path := selectionFixture(test)
	candidate := fixture.preview.Configurations[0]
	candidate.KnownAgentId, candidate.SafeName = "grokbuild", "Grok imported"
	candidate.CredentialSlots = []*importv1.ImportCredentialSlot{{SlotId: "grokbuild.api-key", Present: true}}
	candidate.Draft = &configurationv1.Draft{Name: candidate.SafeName, Payload: &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: &configurationv1.GrokBuildProvider{Mode: configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL, Model: &configurationv1.GrokBuildModel{Profile: "work", UpstreamModel: "grok-model", Endpoint: "https://grok.example/v1", Name: "Work model", ApiBackend: "responses", ContextWindow: 9007199254740991, EnvKey: new("GROK_KEY")}}}}
	fixture.mutate = func(receipt *importv1.ImportReceipt) {
		for _, asset := range receipt.CreatedAssets {
			asset.AgentId, asset.Name = "grokbuild", "Grok imported"
		}
	}
	preview, err := client.PreviewFile(test.Context(), importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24, path, "", nil)
	if err != nil {
		test.Fatal("Grok preview not accepted", err)
	}
	defer preview.Close()
	operation, err := client.BeginSelection(preview, importclient.SelectionChoice{Configurations: []int{1}, AcknowledgedNotices: []string{fixture.preview.Notices[0].Id}})
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	if _, err := operation.CommitFile(test.Context(), path); err != nil || fixture.commits.Load() != 1 {
		test.Fatal("Grok receipt unavailable or mutation replayed", err)
	}
}

func TestSelectionGrokClientRejectsInvalidModels(test *testing.T) {
	for name, mutate := range map[string]func(*configurationv1.GrokBuildModel){
		"profile":         func(model *configurationv1.GrokBuildModel) { model.Profile = " padded" },
		"upstream":        func(model *configurationv1.GrokBuildModel) { model.UpstreamModel = "" },
		"name":            func(model *configurationv1.GrokBuildModel) { model.Name = "bad\n" },
		"window":          func(model *configurationv1.GrokBuildModel) { model.ContextWindow++ },
		"env":             func(model *configurationv1.GrokBuildModel) { model.EnvKey = new("") },
		"backend":         func(model *configurationv1.GrokBuildModel) { model.ApiBackend = "unknown" },
		"http":            func(model *configurationv1.GrokBuildModel) { model.Endpoint = "http://grok.example/v1" },
		"query":           func(model *configurationv1.GrokBuildModel) { model.Endpoint += "?private=example" },
		"escaped control": func(model *configurationv1.GrokBuildModel) { model.Endpoint += "/%0A" },
	} {
		test.Run(name, func(test *testing.T) {
			client, fixture, path := selectionFixture(test)
			candidate := fixture.preview.Configurations[0]
			candidate.KnownAgentId = "grokbuild"
			candidate.CredentialSlots = []*importv1.ImportCredentialSlot{{SlotId: "grokbuild.api-key"}}
			model := &configurationv1.GrokBuildModel{Profile: "work", UpstreamModel: "grok-model", Endpoint: "https://grok.example/v1", Name: "Work model", ApiBackend: "responses", ContextWindow: 9007199254740991}
			mutate(model)
			candidate.Draft.Payload = &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: &configurationv1.GrokBuildProvider{Mode: configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL, Model: model}}
			preview, err := client.PreviewFile(test.Context(), importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24, path, "", nil)
			if preview != nil {
				preview.Close()
			}
			if err == nil || fixture.commits.Load() != 0 {
				test.Fatal("invalid model reached review or mutation")
			}
		})
	}
}
