package importer_test

import (
	"bytes"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/importer"
)

const openCodeSelectionSettings = `{"npm":"@ai-sdk/openai-compatible","name":"Work","options":{"baseURL":"https://opencode.example/v1","apiKey":"SYNTHETIC-PRIVATE-MARKER","headers":{"X-Tenant":"SYNTHETIC-PRIVATE-MARKER","Authorization":"SYNTHETIC-PRIVATE-MARKER"}},"models":{"model-a":{"name":"Model A","limit":{"context":9007199254740991,"output":4096},"modalities":{"input":["text","image"],"output":["text"]}}}}`

func TestSelectionOpenCodeProjectionAndAdapter(test *testing.T) {
	projection := selectionProviderSource(test, "opencode", []byte(openCodeSelectionSettings))
	view, err := projection.View()
	if err != nil || len(view.Configurations) == 0 || view.Configurations[0].Status != importer.SelectionReady {
		test.Fatal("OpenCode source did not become ready")
	}
	candidate := view.Configurations[0]
	provider := candidate.Draft.OpenCodeProvider
	if provider == nil || provider.ProviderID != "isolated" || provider.Format != "v1" || provider.V1 == nil || *provider.V1.NPM != "@ai-sdk/openai-compatible" || *provider.V1.Options.APIKey != "opencode.api-key" {
		test.Fatal("source provider fields changed")
	}
	if *(*provider.V1.Models)["model-a"].Limit.Context != "9007199254740991" || (*provider.V1.Options.Headers)["Authorization"] != "opencode.value.01" || (*provider.V1.Options.Headers)["X-Tenant"] != "opencode.value.02" || len(candidate.Slots) != 3 {
		test.Fatal("numeric precision or deterministic credential slots lost")
	}
	for _, slot := range candidate.Slots {
		if !slot.Present {
			test.Fatal("source credential presence lost")
		}
	}
	built, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{{CandidateID: candidate.ID, Name: new("Renamed")}}})
	if err != nil {
		test.Fatal("fixed module rejected OpenCode source")
	}
	defer built.Destroy()
	result, err := built.View()
	if err != nil || result.Configurations[0].Draft.Name != "Renamed" {
		test.Fatal("OpenCode review rename failed")
	}
	for _, value := range []any{view, result} {
		encoded, err := json.Marshal(value)
		if err != nil || bytes.Contains(encoded, []byte("SYNTHETIC-PRIVATE-MARKER")) {
			test.Fatal("source credential escaped projection")
		}
	}
}

func TestSelectionOpenCodeRejectsLossySources(test *testing.T) {
	for name, raw := range map[string]string{
		"unknown root":     strings.Replace(openCodeSelectionSettings, `"npm":`, `"extra":true,"npm":`, 1),
		"unknown option":   strings.Replace(openCodeSelectionSettings, `"baseURL":`, `"extra":"SYNTHETIC-PRIVATE-MARKER","baseURL":`, 1),
		"unknown model":    strings.Replace(openCodeSelectionSettings, `"name":"Model A"`, `"name":"Model A","variants":{}`, 1),
		"wrong key type":   strings.Replace(openCodeSelectionSettings, `"apiKey":"SYNTHETIC-PRIVATE-MARKER"`, `"apiKey":42`, 1),
		"duplicate key":    strings.Replace(openCodeSelectionSettings, `"name":"Work"`, `"name":"Work","name":"Other"`, 1),
		"v2":               `{"package":"@ai-sdk/openai","models":{}}`,
		"null options":     `{"options":null}`,
		"query endpoint":   `{"options":{"baseURL":"https://opencode.example/v1?token=private"}}`,
		"fractional limit": `{"models":{"a":{"limit":{"context":1.5}}}}`,
	} {
		test.Run(name, func(test *testing.T) {
			projection := selectionProviderSource(test, "opencode", []byte(raw))
			view, err := projection.View()
			if err != nil || view.Configurations[0].Status != importer.SelectionUnsupportedField || view.Configurations[0].Draft != nil {
				test.Fatal("lossy OpenCode source accepted")
			}
		})
	}
}
