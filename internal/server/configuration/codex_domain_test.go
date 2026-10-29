package configuration_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/agentadapter"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func codexDraft() configuration.Draft {
	return configuration.Draft{Name: "Codex test", CodexProvider: &configuration.CodexProvider{
		AuthMode: agentconfig.CodexProviderKeyAuth,
		Endpoint: "https://provider.example/v1",
		Model:    "model-test",
	}}
}

func TestProviderJSONUsesOnlySelectedBranch(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	for _, sample := range []struct {
		name  string
		draft configuration.Draft
	}{
		{name: "codex", draft: codexDraft()},
		{name: "claude", draft: draft()},
	} {
		test.Run(sample.name, func(test *testing.T) {
			view, err := service.Create(test.Context(), "alice", sample.draft, nil)
			if err != nil {
				test.Fatal(err)
			}
			for _, value := range []any{sample.draft, view} {
				encoded, err := json.Marshal(value)
				if err != nil {
					test.Fatal("marshal provider union")
				}
				var fields map[string]jsontext.Value
				if err := json.Unmarshal(encoded, &fields); err != nil {
					test.Fatal("decode provider union")
				}
				_, hasClaude := fields["provider"]
				_, hasCodex := fields["codex_provider"]
				if hasClaude != (sample.name == "claude") || hasCodex != (sample.name == "codex") {
					test.Error("provider union must serialize exactly the selected branch")
				}
			}
		})
	}
}

func TestCodexProviderAssetsRejectMixedDraft(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	claudeFields := []configuration.ClaudeProvider{
		{AuthMode: configuration.APIKey},
		{Endpoint: new("https://provider.example/v1")},
		{Models: configuration.ClaudeModels{Default: new("model-test")}},
		{Models: configuration.ClaudeModels{Haiku: new("model-test")}},
		{Models: configuration.ClaudeModels{Sonnet: new("model-test")}},
		{Models: configuration.ClaudeModels{Opus: new("model-test")}},
		{Endpoint: new("")},
	}
	for index, provider := range claudeFields {
		input := codexDraft()
		input.Provider = provider
		if _, err := service.Create(test.Context(), "alice", input, nil); !errors.Is(err, configuration.ErrInvalid) {
			test.Fatalf("mixed provider case %d must be rejected", index)
		}
	}
	assets, err := service.List(test.Context(), "alice")
	if err != nil || len(assets) != 0 {
		test.Fatal("invalid mixed input must never persist an asset")
	}
}

func TestCodexProviderAssets(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	view, err := service.Create(test.Context(), "alice", codexDraft(), nil)
	if err != nil {
		test.Fatalf("create Codex provider without credential: %v", err)
	}
	if view.Metadata.AgentID != "codex" || view.CodexProvider == nil || view.Readiness != configuration.NeedsCredential || len(view.Secrets) != 1 || view.Secrets[0].Slot != configuration.CodexProviderKeySlot || view.Secrets[0].Present {
		test.Fatal("Codex provider must expose its own typed branch and one absent slot")
	}
}

func codexReplacement(value string) []configuration.SecretMutation {
	return []configuration.SecretMutation{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte(value))}}
}

func fullCodexDraft() configuration.Draft {
	input := codexDraft()
	input.CodexProvider.ReasoningEffort = new(agentconfig.CodexReasoningEffort("none"))
	input.CodexProvider.Catalog = &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{
		{Model: "model-second"},
		{Model: "model-test", DisplayName: new("Synthetic model"), ContextWindow: new(uint64(2147483647)), SupportsParallelToolCalls: new(false), InputModalities: new([]agentconfig.CodexModality{"image", "text"}), BaseInstructions: new("  synthetic\r\n\tprivate instructions  "), ReasoningLevels: new([]agentconfig.CodexReasoningEffort{"high", "none", "low"}), DefaultReasoningLevel: new(agentconfig.CodexReasoningEffort("low"))},
	}}
	return input
}

func TestCodexProviderAssetsCredentialLifecycle(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	input := fullCodexDraft()
	view, err := service.Create(test.Context(), "alice", input, codexReplacement("  synthetic-codex-key  "))
	if err != nil {
		test.Fatal(err)
	}
	if view.Readiness != configuration.CredentialComplete || !view.Secrets[0].Present || !reflect.DeepEqual(view.CodexProvider, input.CodexProvider) {
		test.Fatal("complete typed asset lost credential or catalog presence/order")
	}
	input.CodexProvider.Catalog.Models[1].Model = "mutated-input"
	view.CodexProvider.Catalog.Models[1].InputModalities = new([]agentconfig.CodexModality{"text"})
	view.CodexProvider.Catalog.Models[1].SupportsParallelToolCalls = new(true)
	reopened, err := service.Edit(test.Context(), "alice", view.Metadata.ID)
	if err != nil || !reflect.DeepEqual(reopened.CodexProvider, fullCodexDraft().CodexProvider) {
		test.Fatal("draft or returned view mutated stored provider")
	}
	for _, mutations := range [][]configuration.SecretMutation{nil, {{Slot: configuration.CodexProviderKeySlot, Action: configuration.Keep}}} {
		reopened, err = service.Update(test.Context(), "alice", reopened.Metadata.ID, reopened.Metadata.Revision, fullCodexDraft(), mutations)
		if err != nil || !reopened.Secrets[0].Present {
			test.Fatal("default/KEEP lost credential")
		}
	}
	_, payload, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", reopened.Metadata.ID)
	if err != nil {
		test.Fatal(err)
	}
	raw := payload.Reveal()
	defer clear(raw)
	var envelope struct {
		Secrets struct {
			ProviderKey string `json:"provider_key"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Secrets.ProviderKey != "  synthetic-codex-key  " {
		test.Fatal("credential replacement was changed or trimmed")
	}
	copied, err := service.Copy(test.Context(), "alice", reopened.Metadata.ID, reopened.Metadata.Revision, "Copied Codex")
	if err != nil || copied.Metadata.ID == reopened.Metadata.ID || copied.Metadata.Revision != 1 || copied.Secrets[0].Present || copied.Readiness != configuration.NeedsCredential {
		test.Fatal("copy must have new identity and no credential")
	}
	_, payload, err = store.ReadConfigurationAssetVersion(test.Context(), "alice", copied.Metadata.ID)
	if err != nil {
		test.Fatal(err)
	}
	copyRaw := payload.Reveal()
	defer clear(copyRaw)
	var copiedEnvelope struct {
		Secrets map[string]jsontext.Value `json:"secrets"`
	}
	if err := json.Unmarshal(copyRaw, &copiedEnvelope); err != nil || len(copiedEnvelope.Secrets) != 0 || bytes.Contains(copyRaw, []byte("synthetic-codex-key")) {
		test.Fatal("decrypted copy retained source credential")
	}
	cleared, err := service.Update(test.Context(), "alice", reopened.Metadata.ID, reopened.Metadata.Revision, fullCodexDraft(), []configuration.SecretMutation{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Clear}})
	if err != nil || cleared.Secrets[0].Present || cleared.Readiness != configuration.NeedsCredential {
		test.Fatal("CLEAR must delete credential")
	}
	if _, err := service.Edit(test.Context(), "bob", cleared.Metadata.ID); !errors.Is(err, configuration.ErrNotFound) {
		test.Fatal("cross-owner read must fail closed")
	}
	if _, err := service.Update(test.Context(), "alice", cleared.Metadata.ID, cleared.Metadata.Revision, draft(), nil); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatal("Codex identity cannot be converted to Claude")
	}
	if _, err := service.Update(test.Context(), "alice", cleared.Metadata.ID, math.MaxInt64, fullCodexDraft(), nil); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatal("revision overflow must be rejected")
	}
	claude, err := service.Create(test.Context(), "alice", draft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := service.Update(test.Context(), "alice", claude.Metadata.ID, claude.Metadata.Revision, codexDraft(), nil); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatal("Claude identity cannot be converted to Codex")
	}
}

func TestCodexProviderAssetsInvalidMutations(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	for _, mutations := range [][]configuration.SecretMutation{
		{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Keep}},
		{{Slot: configuration.APIKeySlot, Action: configuration.Clear}},
		{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Clear, Replacement: configuration.NewSecret([]byte("synthetic"))}},
		{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Replace}},
		{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic\nkey"))}},
		{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Clear}, {Slot: configuration.CodexProviderKeySlot, Action: configuration.Clear}},
	} {
		if _, err := service.Create(test.Context(), "alice", codexDraft(), mutations); !errors.Is(err, configuration.ErrInvalid) {
			test.Fatal("invalid credential mutation accepted")
		}
	}
}

type codexReadBarrier struct {
	configuration.Store
	ready   chan struct{}
	release chan struct{}
}

func (barrier codexReadBarrier) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	metadata, payload, err := barrier.Store.ReadConfigurationAssetVersion(ctx, owner, id)
	select {
	case barrier.ready <- struct{}{}:
	case <-ctx.Done():
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, ctx.Err()
	}
	select {
	case <-barrier.release:
	case <-ctx.Done():
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, ctx.Err()
	}
	return metadata, payload, err
}

func TestCodexProviderAssetConcurrency(test *testing.T) {
	for _, schedule := range []struct{ name, firstWrite string }{
		{name: "contended"},
		{name: "keep-first", firstWrite: "Keep winner"},
		{name: "replace-first", firstWrite: "Replace winner"},
	} {
		test.Run(schedule.name, func(test *testing.T) {
			testCodexProviderAssetConcurrency(test, schedule.firstWrite)
		})
	}
}

func testCodexProviderAssetConcurrency(test *testing.T, firstWrite string) {
	test.Helper()
	store, _, _ := fixture(test)
	created, err := configuration.New(store).Create(test.Context(), "alice", codexDraft(), codexReplacement("synthetic-original"))
	if err != nil {
		test.Fatal(err)
	}
	barrier := &barrierStore{Store: store, arrived: make(chan struct{}, 2), release: make(chan struct{}), firstWrite: firstWrite, firstWriteDone: make(chan struct{})}
	service := configuration.New(barrier)
	var workers sync.WaitGroup
	type result struct {
		name string
		view configuration.EditView
		err  error
	}
	outcomes := make(chan result, 2)
	for _, name := range []string{"Keep winner", "Replace winner"} {
		workers.Go(func() {
			input := fullCodexDraft()
			input.Name = name
			mutations := []configuration.SecretMutation{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Keep}}
			if name == "Replace winner" {
				mutations = codexReplacement("synthetic-new")
			}
			view, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, input, mutations)
			outcomes <- result{name: name, view: view, err: err}
		})
	}
	for range 2 {
		select {
		case <-barrier.arrived:
		case <-test.Context().Done():
			test.Fatal("snapshot barrier cancelled")
		}
	}
	close(barrier.release)
	workers.Wait()
	close(outcomes)
	winner := ""
	failures := 0
	for outcome := range outcomes {
		if outcome.err == nil {
			if winner != "" || outcome.view.Metadata.Revision != 2 || outcome.view.Metadata.Name != outcome.name {
				test.Fatal("multiple winners or inconsistent successful CAS result")
			}
			winner = outcome.name
		} else {
			failures++
			if !reflect.DeepEqual(outcome.view, configuration.EditView{}) || (!errors.Is(outcome.err, configuration.ErrConflict) && (firstWrite != "" || !errors.Is(outcome.err, configuration.ErrUnavailable))) {
				test.Fatalf("loser must return an allowed failure and zero view: %v", outcome.err)
			}
		}
	}
	if winner == "" || failures != 1 || barrier.reads.Load() != 2 || barrier.writes.Load() != 2 {
		test.Fatal("same-revision updates require one winner, one failure and no retry")
	}
	if firstWrite != "" && winner != firstWrite {
		test.Fatal("scheduled committed writer did not win")
	}
	expected := "synthetic-original"
	if winner == "Replace winner" {
		expected = "synthetic-new"
	}
	payload, err := store.ReadConfigurationAsset(test.Context(), "alice", created.Metadata.ID)
	if err != nil {
		test.Fatal(err)
	}
	encoded := payload.Reveal()
	defer clear(encoded)
	var decoded struct {
		Secrets struct {
			ProviderKey string `json:"provider_key"`
		} `json:"secrets"`
	}
	if json.Unmarshal(encoded, &decoded) != nil {
		test.Fatal("cannot inspect synthetic Codex credential")
	}
	if decoded.Secrets.ProviderKey != expected {
		test.Fatal("KEEP rebased onto another revision or losing write changed credential")
	}
	view, err := configuration.New(store).Edit(test.Context(), "alice", created.Metadata.ID)
	if err != nil || view.Metadata.Revision != 2 || view.Metadata.Name != winner || !view.Secrets[0].Present || !reflect.DeepEqual(view.CodexProvider, fullCodexDraft().CodexProvider) {
		test.Fatal("CAS result lost winner metadata, provider or credential")
	}
}

func TestCodexProviderPlannerParity(test *testing.T) {
	store, _, _ := fixture(test)
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	for _, input := range []configuration.Draft{codexDraft(), fullCodexDraft()} {
		view, err := configuration.New(store).Create(test.Context(), "alice", input, codexReplacement("synthetic-never-to-plugin"))
		if err != nil {
			test.Fatal(err)
		}
		provider := view.CodexProvider
		desired := agentconfig.CodexProviderDesired{AuthMode: provider.AuthMode, Endpoint: provider.Endpoint, Model: provider.Model, ReasoningEffort: provider.ReasoningEffort, KeyPresent: view.Secrets[0].Present, Catalog: provider.Catalog}
		var request agentconfig.CodexProviderPlanRequest
		for _, descriptor := range registry.List() {
			if descriptor.Manifest.PluginID == "builtin.codex" {
				request = agentconfig.CodexProviderPlanRequest{APIVersion: descriptor.Manifest.APIVersion, PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: descriptor.Manifest.Target, Capabilities: descriptor.Manifest.Capabilities, Desired: desired}
			}
		}
		plan, err := registry.PlanCodexProvider(test.Context(), request)
		if err != nil {
			test.Fatal("complete asset must match the real plugin planner contract")
		}
		if plan.ProviderID != "cognate" || plan.WireAPI != "responses" || plan.Endpoint != input.CodexProvider.Endpoint || plan.Model != input.CodexProvider.Model || plan.LocalAuthPolicy != "preserve-local-login-v1" || plan.CredentialReference != "codex.provider-key" || !reflect.DeepEqual(plan.RequiredSlots, []agentconfig.CodexSecretSlot{"codex.provider-key"}) || !reflect.DeepEqual(plan.ReasoningEffort, input.CodexProvider.ReasoningEffort) || !reflect.DeepEqual(plan.Catalog, input.CodexProvider.Catalog) {
			test.Fatal("real planner changed private asset semantics/presence/order")
		}
		encoded, err := json.Marshal(request)
		if err != nil || bytes.Contains(encoded, []byte("synthetic-never-to-plugin")) {
			test.Fatal("planner request must only contain credential presence, not its value")
		}
	}
}

func TestCodexProviderInvalidShape(test *testing.T) {
	for name, mutate := range map[string]func(*configuration.CodexProvider){
		"http": func(provider *configuration.CodexProvider) { provider.Endpoint = "http://localhost/v1" },
		"userinfo": func(provider *configuration.CodexProvider) {
			provider.Endpoint = "https://synthetic:password@provider.example/v1"
		},
		"query":           func(provider *configuration.CodexProvider) { provider.Endpoint += "?token=synthetic" },
		"port":            func(provider *configuration.CodexProvider) { provider.Endpoint = "https://provider.example:65536/v1" },
		"whitespace":      func(provider *configuration.CodexProvider) { provider.Endpoint += " " },
		"decoded-control": func(provider *configuration.CodexProvider) { provider.Endpoint += "/%0A" },
		"model":           func(provider *configuration.CodexProvider) { provider.Model = " invalid" },
		"auth":            func(provider *configuration.CodexProvider) { provider.AuthMode = "native-login" },
		"effort": func(provider *configuration.CodexProvider) {
			provider.ReasoningEffort = new(agentconfig.CodexReasoningEffort("unknown"))
		},
		"empty-catalog":   func(provider *configuration.CodexProvider) { provider.Catalog.Models = nil },
		"duplicate-model": func(provider *configuration.CodexProvider) { provider.Catalog.Models[0].Model = "model-test" },
		"missing-primary": func(provider *configuration.CodexProvider) { provider.Model = "not-in-catalog" },
		"zero-context":    func(provider *configuration.CodexProvider) { provider.Catalog.Models[1].ContextWindow = new(uint64(0)) },
		"overflow-context": func(provider *configuration.CodexProvider) {
			provider.Catalog.Models[1].ContextWindow = new(uint64(2147483648))
		},
		"empty-modalities": func(provider *configuration.CodexProvider) {
			provider.Catalog.Models[1].InputModalities = new([]agentconfig.CodexModality{})
		},
		"duplicate-modalities": func(provider *configuration.CodexProvider) {
			provider.Catalog.Models[1].InputModalities = new([]agentconfig.CodexModality{"text", "text"})
		},
		"empty-levels": func(provider *configuration.CodexProvider) {
			provider.Catalog.Models[1].ReasoningLevels = new([]agentconfig.CodexReasoningEffort{})
		},
		"missing-default-level": func(provider *configuration.CodexProvider) {
			provider.Catalog.Models[1].DefaultReasoningLevel = new(agentconfig.CodexReasoningEffort("max"))
		},
		"missing-selected-level": func(provider *configuration.CodexProvider) {
			provider.ReasoningEffort = new(agentconfig.CodexReasoningEffort("max"))
		},
		"control-body": func(provider *configuration.CodexProvider) {
			provider.Catalog.Models[1].BaseInstructions = new("synthetic\x00body")
		},
		"oversized-body": func(provider *configuration.CodexProvider) {
			provider.Catalog.Models[1].BaseInstructions = new(strings.Repeat("a", 2049))
		},
	} {
		test.Run(name, func(test *testing.T) {
			store, _, _ := fixture(test)
			input := fullCodexDraft()
			mutate(input.CodexProvider)
			if _, err := configuration.New(store).Create(test.Context(), "alice", input, nil); !errors.Is(err, configuration.ErrInvalid) {
				test.Fatal("invalid shape accepted")
			}
		})
	}
}

func TestCodexProviderCopySnapshotSemantics(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	source, err := service.Create(test.Context(), "alice", codexDraft(), codexReplacement("synthetic-source-key"))
	if err != nil {
		test.Fatal(err)
	}
	barrier := codexReadBarrier{Store: store, ready: make(chan struct{}, 1), release: make(chan struct{})}
	type result struct {
		view configuration.EditView
		err  error
	}
	finished := make(chan result, 1)
	go func() {
		view, err := configuration.New(barrier).Copy(test.Context(), "alice", source.Metadata.ID, 1, "Snapshot copy")
		finished <- result{view, err}
	}()
	select {
	case <-barrier.ready:
	case <-test.Context().Done():
		test.Fatal("copy did not read source snapshot")
	}
	updated := codexDraft()
	updated.CodexProvider.Model = "updated-model"
	_, updateErr := service.Update(test.Context(), "alice", source.Metadata.ID, 1, updated, nil)
	close(barrier.release)
	copied := <-finished
	if updateErr != nil {
		test.Fatal(updateErr)
	}
	if copied.err != nil || copied.view.CodexProvider.Model != "model-test" || copied.view.Secrets[0].Present {
		test.Fatal("copy must use its validated old snapshot and never reread source secret")
	}
	if _, err := service.Copy(test.Context(), "alice", source.Metadata.ID, 1, "Stale copy"); !errors.Is(err, configuration.ErrConflict) {
		test.Fatal("copy reading after source update must reject stale revision")
	}
}

func TestCodexProviderCrossAgentQuota(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	for index := range state.MaxConfigurationAssets {
		input := draft()
		if index%2 == 0 {
			input = codexDraft()
		}
		if _, err := service.Create(test.Context(), "alice", input, nil); err != nil {
			test.Fatalf("within-budget asset %d: %v", index, err)
		}
	}
	if _, err := service.Create(test.Context(), "alice", codexDraft(), nil); !errors.Is(err, configuration.ErrLimit) {
		test.Fatal("Codex must share the existing cross-agent quota")
	}
	if _, err := service.Create(test.Context(), "bob", codexDraft(), nil); err != nil {
		test.Fatal("one owner's quota must not consume another owner's allowance")
	}
}

func TestCodexProviderCatalogByteBoundary(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	input := codexDraft()
	catalog := &agentconfig.CodexCatalogDesired{}
	for index := range 16 {
		catalog.Models = append(catalog.Models, agentconfig.CodexCatalogModelDesired{Model: strings.Repeat("m", index+1), BaseInstructions: new(strings.Repeat("a", 1800))})
	}
	catalog.Models[0].Model = input.CodexProvider.Model
	catalog.Models = append(catalog.Models, agentconfig.CodexCatalogModelDesired{Model: "padding", BaseInstructions: new(strings.Repeat("b", 1800))})
	encoded, err := json.Marshal(catalog)
	if err != nil {
		test.Fatal(err)
	}
	remaining := agentconfig.MaxCodexCatalogBytes - len(encoded)
	if remaining < 0 || remaining > 2048 {
		test.Fatal("catalog boundary fixture must fit in one valid additional body")
	}
	catalog.Models = append(catalog.Models, agentconfig.CodexCatalogModelDesired{Model: "boundary", BaseInstructions: new("a")})
	encoded, err = json.Marshal(catalog)
	if err != nil {
		test.Fatal(err)
	}
	length := agentconfig.MaxCodexCatalogBytes - len(encoded) + 1
	if length < 1 || length > 2048 {
		test.Fatal("catalog boundary padding is outside the per-body limit")
	}
	catalog.Models[len(catalog.Models)-1].BaseInstructions = new(strings.Repeat("x", length))
	encoded, err = json.Marshal(catalog)
	if err != nil || len(encoded) != agentconfig.MaxCodexCatalogBytes {
		test.Fatal("boundary fixture does not encode to the exact catalog budget")
	}
	input.CodexProvider.Catalog = catalog
	if _, err := service.Create(test.Context(), "alice", input, nil); err != nil {
		test.Fatal("exact catalog byte budget must be accepted")
	}
	catalog.Models[len(catalog.Models)-1].BaseInstructions = new(strings.Repeat("x", length+1))
	if _, err := service.Create(test.Context(), "alice", input, nil); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatal("one-byte-over catalog must be rejected without truncation")
	}
}
