package importer_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/state"
)

func selectionOptions(view importer.SelectionView) importer.SelectionOptions {
	options := importer.SelectionOptions{}
	for _, candidate := range view.Configurations {
		switch candidate.AgentID {
		case "claude":
			options.Candidates = append(options.Candidates, importer.SelectionCandidateOptions{CandidateID: candidate.ID, ClaudeAuthMode: new(configuration.APIKey)})
		case "gemini":
			options.Candidates = append(options.Candidates, importer.SelectionCandidateOptions{CandidateID: candidate.ID, GeminiKeyTransport: new(agentconfig.GeminiBearer)})
		}
	}
	return options
}

func TestSelectionProvidersPositive(test *testing.T) {
	raw := ccSwitchSnapshot(test, string(selectionFixture(test, "ccswitch-schema24.sql")))
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, raw)
	if err != nil {
		test.Fatal("synthetic source projection failed")
	}
	defer projection.Destroy()
	view, _ := projection.View()
	candidates, err := importer.BuildSelection(test.Context(), projection, selectionOptions(view))
	if err != nil {
		test.Fatal("real source adapter selection unavailable")
	}
	defer candidates.Destroy()
	result, err := candidates.View()
	if err != nil {
		test.Fatal("adapter result unavailable")
	}
	var golden map[string]configuration.Draft
	if json.Unmarshal(selectionFixture(test, "expected.json"), &golden) != nil {
		test.Fatal("golden fixture invalid")
	}
	directory := test.TempDir()
	if os.Chmod(directory, 0o700) != nil {
		test.Fatal("synthetic private directory unavailable")
	}
	store, err := state.Open(test.Context(), directory+"/state.db", bytes.Repeat([]byte{23}, 32))
	if err != nil {
		test.Fatal("synthetic state unavailable")
	}
	defer store.Close()
	if store.CreateUser(test.Context(), "synthetic-owner") != nil {
		test.Fatal("synthetic owner unavailable")
	}
	service := configuration.New(store)
	count := 0
	for _, candidate := range result.Configurations {
		want, known := golden[candidate.AgentID]
		if !known {
			continue
		}
		if candidate.Status != importer.SelectionReady || candidate.Draft == nil || !reflect.DeepEqual(*candidate.Draft, want) {
			test.Fatal("provider full-field golden/presence/order mismatch")
		}
		created, err := service.Create(test.Context(), "synthetic-owner", *candidate.Draft, nil)
		if err != nil || created.HasPreservedExtras {
			test.Fatal("positive provider rejected by actual existing domain")
		}
		if candidate.AgentID != "claude" && created.Readiness != configuration.NeedsCredential {
			test.Fatal("omitted credentials falsely reported ready")
		}
		count++
	}
	if count != 3 {
		test.Fatal("positive import collapsed to allunsupported")
	}
	encoded, err := json.Marshal(result)
	if err != nil || bytes.Contains(encoded, []byte("SYNTHETIC-PRIVATE-MARKER")) {
		test.Fatal("credential leaked into adapter output")
	}
}

func selectionProviderSource(test *testing.T, agent string, settings []byte) importer.SourceProjection {
	test.Helper()
	escape := func(value string) string { return strings.ReplaceAll(value, "'", "''") }
	statements := string(selectionFixture(test, "ccswitch-schema24.sql")) + `DELETE FROM providers; INSERT INTO providers VALUES('isolated','` + escape(agent) + `','Imported','` + escape(string(settings)) + `','{}',NULL,NULL,0,0,NULL,NULL,NULL,0,0);`
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, ccSwitchSnapshot(test, statements))
	if err != nil {
		test.Fatal("synthetic isolated provider projection failed")
	}
	test.Cleanup(projection.Destroy)
	return projection
}

func selectionEndpointDomainParity(test *testing.T, agent string, settings []byte, endpoint string, supported bool) {
	test.Helper()
	projection := selectionProviderSource(test, agent, settings)
	view, err := projection.View()
	if err != nil || len(view.Configurations) == 0 || view.Configurations[0].AgentID != agent {
		test.Fatal("endpoint source projection unavailable")
	}
	options := importer.SelectionOptions{}
	if view.Configurations[0].Draft != nil {
		options = selectionOptions(view)
	}
	result, err := importer.BuildSelection(test.Context(), projection, options)
	if err != nil {
		test.Fatal("endpoint source failed actual moejs")
	}
	defer result.Destroy()
	built, err := result.View()
	if err != nil || len(built.Configurations) != len(view.Configurations) {
		test.Fatal("endpoint adapter result unavailable")
	}
	candidate := built.Configurations[0]
	if candidate.AgentID != agent || candidate.ID != view.Configurations[0].ID {
		test.Fatal("endpoint adapter changed provider identity")
	}
	encoded, err := json.Marshal(built)
	if err != nil || bytes.Contains(encoded, []byte("SYNTHETIC-PRIVATE-MARKER")) {
		test.Fatal("endpoint adapter output leaked credential")
	}
	if !supported && view.Configurations[0].Status == importer.SelectionUnsupportedField && view.Configurations[0].Draft == nil && candidate.Status == importer.SelectionUnsupportedField && candidate.Draft == nil {
		return
	}
	if candidate.Status != importer.SelectionReady || candidate.Draft == nil {
		test.Fatal("endpoint classification disagrees with existing domain")
	}
	directory := test.TempDir()
	if os.Chmod(directory, 0o700) != nil {
		test.Fatal("endpoint target private directory unavailable")
	}
	store, err := state.Open(test.Context(), directory+"/state.db", bytes.Repeat([]byte{23}, 32))
	if err != nil {
		test.Fatal("endpoint target state unavailable")
	}
	defer store.Close()
	if store.CreateUser(test.Context(), "synthetic-owner") != nil {
		test.Fatal("endpoint target owner unavailable")
	}
	service := configuration.New(store)
	created, err := service.Create(test.Context(), "synthetic-owner", *candidate.Draft, nil)
	if !supported {
		if !errors.Is(err, configuration.ErrInvalid) {
			test.Fatal("invalid endpoint fixture no longer matches target validator")
		}
		test.Fatal("I12_ENDPOINT_DOMAIN_GAP: Ready draft rejected by actual existing configuration validator")
	}
	if err != nil || created.HasPreservedExtras {
		test.Fatal("valid endpoint rejected by actual existing domain")
	}
	readback, err := service.Edit(test.Context(), "synthetic-owner", created.Metadata.ID)
	if err != nil {
		test.Fatal("endpoint target readback unavailable")
	}
	roundTrip := configuration.Draft{Name: readback.Metadata.Name, Provider: readback.Provider, CodexProvider: readback.CodexProvider, GeminiProvider: readback.GeminiProvider}
	if !reflect.DeepEqual(roundTrip, *candidate.Draft) {
		test.Fatal("endpoint target changed typed fields or presence")
	}
	actual := roundTrip.Provider.Endpoint
	if roundTrip.CodexProvider != nil {
		actual = &roundTrip.CodexProvider.Endpoint
	}
	if roundTrip.GeminiProvider != nil {
		actual = roundTrip.GeminiProvider.Endpoint
	}
	if actual == nil || *actual != endpoint {
		test.Fatal("endpoint was silently rewritten")
	}
}

func TestSelectionClaudeEndpointCompatibility(test *testing.T) {
	for _, endpoint := range []string{
		"http://endpoint.example/v1", "https://endpoint.example/%2F", "https://endpoint.example/%20",
		"https://endpoint.example/%E4%B8%AD", "https://endpoint.example/%0A",
		"https://endpoint.example/%5C", "https://endpoint.example/%FF",
	} {
		test.Run(endpoint, func(test *testing.T) {
			encoded, err := json.Marshal(map[string]any{"env": map[string]any{
				"ANTHROPIC_BASE_URL": endpoint, "ANTHROPIC_MODEL": "claude-default",
				"ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku", "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet",
				"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus", "ANTHROPIC_API_KEY": "SYNTHETIC-PRIVATE-MARKER",
			}})
			if err != nil {
				test.Fatal("endpoint source encoding failed")
			}
			selectionEndpointDomainParity(test, "claude", encoded, endpoint, true)
		})
	}
}

func TestSelectionClaudeChoicesAndUnknownFields(test *testing.T) {
	for _, mode := range []configuration.AuthMode{configuration.NativeLogin, configuration.APIKey, configuration.AuthToken} {
		projection := selectionProviderSource(test, "claude", []byte(`{"env":{"ANTHROPIC_MODEL":"claude-a","ANTHROPIC_DEFAULT_HAIKU_MODEL":"haiku","ANTHROPIC_DEFAULT_SONNET_MODEL":"sonnet","ANTHROPIC_DEFAULT_OPUS_MODEL":"opus","ANTHROPIC_API_KEY":"SYNTHETIC-PRIVATE-MARKER"}}`))
		view, _ := projection.View()
		candidate := view.Configurations[0]
		result, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{{CandidateID: candidate.ID, ClaudeAuthMode: new(mode)}}})
		if err != nil {
			test.Fatal("explicit Claude auth choice rejected")
		}
		built, _ := result.View()
		result.Destroy()
		provider := built.Configurations[0]
		if provider.Status != importer.SelectionReady || provider.Draft == nil || provider.Draft.Provider.AuthMode != mode || provider.Draft.Provider.Models.Opus == nil || *provider.Draft.Provider.Models.Opus != "opus" {
			test.Fatal("Claude model/auth mapping incomplete")
		}
	}
	for _, settings := range []string{`{"env":{"UNSUPPORTED":"SYNTHETIC-PRIVATE-MARKER"}}`, `{"env":{},"permissions":{"command":"SYNTHETIC-PRIVATE-MARKER"}}`, `{"env":{"ANTHROPIC_MODEL":null}}`, `{"env":{"ANTHROPIC_MODEL":"first","ANTHROPIC_MODEL":"second"}}`} {
		projection := selectionProviderSource(test, "claude", []byte(settings))
		view, _ := projection.View()
		if view.Configurations[0].Status != importer.SelectionUnsupportedField || view.Configurations[0].Draft != nil {
			test.Fatal("unknown or malformed Claude functionality silently stripped")
		}
	}
}

func TestSelectionProviderRename(test *testing.T) {
	statements := string(selectionFixture(test, "ccswitch-schema24.sql")) + `UPDATE providers SET name=' Imported ' WHERE app_type='codex';`
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, ccSwitchSnapshot(test, statements))
	if err != nil {
		test.Fatal("source name requiring explicit rename lost its preview")
	}
	defer projection.Destroy()
	view, _ := projection.View()
	var identifier string
	for _, candidate := range view.Configurations {
		if candidate.AgentID == "codex" {
			identifier = candidate.ID
			if candidate.Draft == nil || candidate.Status != importer.SelectionNeedsChoice {
				test.Fatal("source name was silently trimmed or discarded")
			}
		}
	}
	options := selectionOptions(view)
	options.Candidates = append(options.Candidates, importer.SelectionCandidateOptions{CandidateID: identifier, Name: new("Explicit renamed")})
	candidates, err := importer.BuildSelection(test.Context(), projection, options)
	if err != nil {
		test.Fatal("explicit rename/re-preview unavailable")
	}
	defer candidates.Destroy()
	built, _ := candidates.View()
	for _, candidate := range built.Configurations {
		if candidate.AgentID == "codex" && (candidate.Draft == nil || candidate.Status != importer.SelectionReady || candidate.Draft.Name != "Explicit renamed") {
			test.Fatal("explicit renamed source not available")
		}
	}
}

func TestSelectionProviderFixtureCases(test *testing.T) {
	var cases []struct {
		Agent    string         `json:"agent"`
		Name     string         `json:"name"`
		Settings jsontext.Value `json:"settings"`
		Status   string         `json:"status"`
	}
	if json.Unmarshal(selectionFixture(test, "provider-cases.json"), &cases) != nil {
		test.Fatal("source fixture invalid")
	}
	for _, sample := range cases {
		projection := selectionProviderSource(test, sample.Agent, sample.Settings)
		view, _ := projection.View()
		candidate := view.Configurations[0]
		if sample.Status == "unsupported-field" {
			if candidate.Status != importer.SelectionUnsupportedField || candidate.Draft != nil {
				test.Fatal("unsupported fixture partially migrated")
			}
			continue
		}
		mode := configuration.AuthToken
		if sample.Name == "Claude native" {
			mode = configuration.NativeLogin
		}
		candidates, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{{CandidateID: candidate.ID, ClaudeAuthMode: new(mode)}}})
		if err != nil {
			test.Fatal("qualified fixture failed actual moejs")
		}
		built, _ := candidates.View()
		candidates.Destroy()
		if built.Configurations[0].Status != importer.SelectionReady {
			test.Fatal("qualified fixture was not actually supported")
		}
	}
}
