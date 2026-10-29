package agentadapter_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

func TestCodexProviderBuiltinsIdentity(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal("builtin registration failed")
	}
	var descriptor agentconfig.Descriptor
	for _, candidate := range registry.List() {
		if candidate.Manifest.PluginID == "builtin.codex" {
			descriptor = candidate
		}
	}
	manifest := descriptor.Manifest
	if manifest.PluginVersion != "1.1.0" || manifest.AgentID != "codex" || manifest.Target != agentconfig.CodexConfig || manifest.Format != agentconfig.TOML || len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != agentconfig.CodexProviderPlanV1 || len(descriptor.DefinitionDigest) != 64 {
		test.Fatal("Codex planner identity missing")
	}
	request := agentconfig.CodexProviderPlanRequest{APIVersion: 1, PluginID: manifest.PluginID, PluginVersion: manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: manifest.Target, Capabilities: []agentconfig.Capability{agentconfig.CodexProviderPlanV1}, Desired: agentconfig.CodexProviderDesired{AuthMode: "provider-key", Endpoint: "https://provider.example/v1", Model: "model-main", KeyPresent: true}}
	for name, change := range map[string]func(*agentconfig.CodexProviderPlanRequest){
		"api":         func(request *agentconfig.CodexProviderPlanRequest) { request.APIVersion = 2 },
		"version":     func(request *agentconfig.CodexProviderPlanRequest) { request.PluginVersion = "1.0.0" },
		"digest":      func(request *agentconfig.CodexProviderPlanRequest) { request.DefinitionDigest = "untrusted" },
		"target":      func(request *agentconfig.CodexProviderPlanRequest) { request.Target = "claude.settings" },
		"missing-cap": func(request *agentconfig.CodexProviderPlanRequest) { request.Capabilities = nil },
		"extra-cap": func(request *agentconfig.CodexProviderPlanRequest) {
			request.Capabilities = []agentconfig.Capability{agentconfig.CodexProviderPlanV1, "extra"}
		},
		"duplicate-cap": func(request *agentconfig.CodexProviderPlanRequest) {
			request.Capabilities = []agentconfig.Capability{agentconfig.CodexProviderPlanV1, agentconfig.CodexProviderPlanV1}
		},
	} {
		test.Run(name, func(test *testing.T) {
			mutated := request
			change(&mutated)
			result, err := registry.PlanCodexProvider(test.Context(), mutated)
			if !errors.Is(err, agentadapter.ErrIncompatible) || result.APIVersion != 0 {
				test.Fatal("incompatible identity accepted")
			}
		})
	}
	request.PluginID = "unknown.plugin"
	if _, err := registry.PlanCodexProvider(test.Context(), request); !errors.Is(err, agentadapter.ErrUnsupported) {
		test.Fatal("unknown plugin accepted")
	}
	if _, err := (*agentadapter.Registry)(nil).PlanCodexProvider(context.Background(), request); !errors.Is(err, agentadapter.ErrUnsupported) {
		test.Fatal("nil registry accepted")
	}
}

func TestCodexProviderRenderStillUnsupported(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal("builtin registration failed")
	}
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.AgentID == "codex" {
			manifest := descriptor.Manifest
			request := agentconfig.RenderRequest{APIVersion: 1, PluginID: manifest.PluginID, PluginVersion: manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: manifest.Target, Capabilities: manifest.Capabilities}
			if _, err := registry.Render(test.Context(), request); !errors.Is(err, agentadapter.ErrUnsupported) {
				test.Fatal("planner opened generic renderer")
			}
		}
	}
}

func TestCodexProviderDefinitionFence(test *testing.T) {
	definition := agentadapter.Definition{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "builtin.codex", PluginVersion: "1.1.0", AgentID: "codex", Target: agentconfig.CodexConfig, Format: agentconfig.TOML, Capabilities: []agentconfig.Capability{agentconfig.CodexProviderPlanV1}}, Module: "export function planCodexProvider() { return {}; }"}
	registry, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal("valid Codex definition rejected")
	}
	for name, mutate := range map[string]func(*agentadapter.Definition){
		"wrong-agent":  func(definition *agentadapter.Definition) { definition.Manifest.AgentID = "gemini" },
		"wrong-format": func(definition *agentadapter.Definition) { definition.Manifest.Format = agentconfig.JSON },
		"foreign-cap": func(definition *agentadapter.Definition) {
			definition.Manifest.Capabilities = []agentconfig.Capability{agentconfig.ClaudeProviderEnvV1}
		},
		"unknown-cap": func(definition *agentadapter.Definition) {
			definition.Manifest.Capabilities = []agentconfig.Capability{"codex.future"}
		},
		"duplicate-cap": func(definition *agentadapter.Definition) {
			definition.Manifest.Capabilities = []agentconfig.Capability{agentconfig.CodexProviderPlanV1, agentconfig.CodexProviderPlanV1}
		},
		"no-module":     func(definition *agentadapter.Definition) { definition.Module = "" },
		"metadata-code": func(definition *agentadapter.Definition) { definition.Manifest.Capabilities = nil },
	} {
		test.Run(name, func(test *testing.T) {
			changed := definition
			mutate(&changed)
			if _, err := agentadapter.NewRegistry([]agentadapter.Definition{changed}); !errors.Is(err, agentadapter.ErrInvalidDefinition) {
				test.Fatal("definition capability fence failed")
			}
		})
	}
	oldDigest := registry.List()[0].DefinitionDigest
	definition.Module += "\n"
	changed, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil || changed.List()[0].DefinitionDigest == oldDigest {
		test.Fatal("module bytes not bound")
	}
	request := agentconfig.CodexProviderPlanRequest{APIVersion: 1, PluginID: "builtin.codex", PluginVersion: "1.1.0", DefinitionDigest: oldDigest, Target: agentconfig.CodexConfig, Capabilities: []agentconfig.Capability{agentconfig.CodexProviderPlanV1}}
	if _, err := changed.PlanCodexProvider(test.Context(), request); !errors.Is(err, agentadapter.ErrIncompatible) {
		test.Fatal("same-version replaced module accepted old digest")
	}
	definition.Module, definition.Manifest.PluginVersion, definition.Manifest.Capabilities = "", "1.0.0", nil
	metadata, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal("legacy metadata definition rejected")
	}
	request.PluginVersion, request.DefinitionDigest = "1.0.0", metadata.List()[0].DefinitionDigest
	if _, err := metadata.PlanCodexProvider(test.Context(), request); !errors.Is(err, agentadapter.ErrUnsupported) {
		test.Fatal("legacy metadata gained execution")
	}
}

func thirdPartyDefinition() agentadapter.Definition {
	return agentadapter.Definition{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "third.claude", PluginVersion: "1.2.3", AgentID: "claude", Target: agentconfig.ClaudeSettings, Format: agentconfig.JSON, Capabilities: []agentconfig.Capability{agentconfig.ClaudeProviderEnvV1}}, Module: `export function render(input) { return {target: "claude.settings", env: {...input.providerEnv}}; }`}
}

func TestBuiltinRegistryListsVersionedTargets(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	descriptors := registry.List()
	if len(descriptors) != 3 {
		test.Fatalf("expected three built-in adapters, got %d", len(descriptors))
	}
	expected := map[string]agentconfig.Target{"claude": agentconfig.ClaudeSettings, "codex": agentconfig.CodexConfig, "gemini": agentconfig.GeminiSettings}
	for _, descriptor := range descriptors {
		manifest := descriptor.Manifest
		expectedVersion := "1.0.0"
		if manifest.AgentID == "claude" || manifest.AgentID == "codex" || manifest.AgentID == "gemini" {
			expectedVersion = "1.1.0"
		}
		if manifest.APIVersion != 1 || manifest.PluginVersion != expectedVersion || len(descriptor.DefinitionDigest) != 64 || expected[manifest.AgentID] != manifest.Target {
			test.Fatal("invalid built-in metadata")
		}
		delete(expected, manifest.AgentID)
		if manifest.AgentID == "claude" && (len(manifest.Capabilities) != 2 || manifest.Capabilities[0] != agentconfig.ClaudeProviderEnvV1 || manifest.Capabilities[1] != "claude.provider-switch.v1") {
			test.Fatal("Claude must declare additive and complete provider capabilities")
		}
		if manifest.AgentID == "codex" && (len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != agentconfig.CodexProviderPlanV1) {
			test.Fatal("Codex must declare only its planner capability")
		}
		if manifest.AgentID == "gemini" && (len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != agentconfig.GeminiProviderPlanV1) {
			test.Fatal("Gemini must declare only its planner capability")
		}
	}
	if len(expected) != 0 {
		test.Fatal("built-in agent metadata is missing")
	}
	descriptors[0].Manifest.Capabilities[0] = "unknown"
	if registry.List()[0].Manifest.Capabilities[0] != agentconfig.ClaudeProviderEnvV1 {
		test.Fatal("caller mutated registry capabilities")
	}
}

func TestSwitchOnlyDefinitionCannotUseAdditiveRender(test *testing.T) {
	definition := thirdPartyDefinition()
	definition.Manifest.Capabilities = []agentconfig.Capability{"claude.provider-switch.v1"}
	registry, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal("switch-only definition was not registered")
	}
	result, err := registry.Render(test.Context(), claudeRequest(test, registry))
	if !errors.Is(err, agentadapter.ErrUnsupported) || len(result.Content) != 0 {
		test.Fatal("additive Render accepted an undeclared capability")
	}
}

func TestThirdPartyDefinitionUsesTheSameRegistryAndRuntime(test *testing.T) {
	definition := thirdPartyDefinition()
	encoded, err := json.Marshal(definition)
	if err != nil {
		test.Fatal(err)
	}
	parsed, err := agentadapter.ParseDefinition(encoded)
	if err != nil {
		test.Fatal(err)
	}
	registry, err := agentadapter.NewRegistry([]agentadapter.Definition{parsed})
	if err != nil {
		test.Fatal(err)
	}
	parsed.Manifest.Capabilities[0] = "mutated"
	request := claudeRequest(test, registry)
	result, err := registry.Render(test.Context(), request)
	if err != nil || string(result.Content) != `{"env":{"ANTHROPIC_MODEL":"new"}}` {
		test.Fatalf("third-party renderer did not execute: %v", err)
	}
	changed := definition
	changed.Module += "\n"
	changedRegistry, err := agentadapter.NewRegistry([]agentadapter.Definition{changed})
	if err != nil {
		test.Fatal(err)
	}
	if registry.List()[0].DefinitionDigest == changedRegistry.List()[0].DefinitionDigest {
		test.Fatal("definition digest does not bind source bytes")
	}
	if _, err := changedRegistry.Render(test.Context(), request); !errors.Is(err, agentadapter.ErrIncompatible) {
		test.Fatal("same-version module replacement did not invalidate old preview")
	}
}

func TestRegistryRejectsInvalidDefinitions(test *testing.T) {
	cases := map[string]func(*agentadapter.Definition){
		"api":         func(definition *agentadapter.Definition) { definition.Manifest.APIVersion = 2 },
		"id-path":     func(definition *agentadapter.Definition) { definition.Manifest.PluginID = "../secret-sentinel" },
		"version":     func(definition *agentadapter.Definition) { definition.Manifest.PluginVersion = "latest" },
		"target-path": func(definition *agentadapter.Definition) { definition.Manifest.Target = "/tmp/settings.json" },
		"agent":       func(definition *agentadapter.Definition) { definition.Manifest.AgentID = "unknown" },
		"format":      func(definition *agentadapter.Definition) { definition.Manifest.Format = agentconfig.TOML },
		"capability": func(definition *agentadapter.Definition) {
			definition.Manifest.Capabilities = []agentconfig.Capability{"exec"}
		},
		"duplicate-capability": func(definition *agentadapter.Definition) {
			definition.Manifest.Capabilities = append(definition.Manifest.Capabilities, agentconfig.ClaudeProviderEnvV1)
		},
		"missing-module": func(definition *agentadapter.Definition) { definition.Module = "" },
		"module-limit": func(definition *agentadapter.Definition) {
			definition.Module = strings.Repeat("x", agentconfig.MaxModuleBytes+1)
		},
		"metadata-with-code": func(definition *agentadapter.Definition) { definition.Manifest.Capabilities = nil },
	}
	for name, change := range cases {
		test.Run(name, func(test *testing.T) {
			definition := thirdPartyDefinition()
			change(&definition)
			if _, err := agentadapter.NewRegistry([]agentadapter.Definition{definition}); !errors.Is(err, agentadapter.ErrInvalidDefinition) || strings.Contains(err.Error(), "secret-sentinel") {
				test.Fatalf("unsafe definition accepted: %v", err)
			}
		})
	}
	definition := thirdPartyDefinition()
	if _, err := agentadapter.NewRegistry([]agentadapter.Definition{definition, definition}); !errors.Is(err, agentadapter.ErrInvalidDefinition) {
		test.Fatal("duplicate definition accepted")
	}
	if _, err := agentadapter.NewRegistry(make([]agentadapter.Definition, agentconfig.MaxDefinitions+1)); !errors.Is(err, agentadapter.ErrInvalidDefinition) {
		test.Fatal("registry count limit ignored")
	}
}

func TestDefinitionJSONRejectsUnknownFieldsAndOversize(test *testing.T) {
	encoded, err := json.Marshal(thirdPartyDefinition())
	if err != nil {
		test.Fatal(err)
	}
	for _, nested := range []bool{false, true} {
		var document map[string]any
		if err := json.Unmarshal(encoded, &document); err != nil {
			test.Fatal(err)
		}
		if nested {
			document["manifest"].(map[string]any)["path"] = "secret-sentinel"
		} else {
			document["command"] = "secret-sentinel"
		}
		data, err := json.Marshal(document)
		if err != nil {
			test.Fatal(err)
		}
		if _, err := agentadapter.ParseDefinition(data); !errors.Is(err, agentadapter.ErrInvalidDefinition) || strings.Contains(err.Error(), "secret-sentinel") {
			test.Fatalf("otherwise valid definition accepted unknown fields: %v", err)
		}
	}
	for _, data := range []string{
		`{"manifest":{"apiVersion":1,"path":"secret-sentinel"},"module":""}`,
		`{"manifest":{},"module":"","command":"secret-sentinel"}`,
		`{"manifest":{},"manifest":{},"module":""}`,
		`null`, `{"secret-sentinel":`, strings.Repeat("x", agentconfig.MaxDefinitionBytes+1),
	} {
		if _, err := agentadapter.ParseDefinition([]byte(data)); !errors.Is(err, agentadapter.ErrInvalidDefinition) || strings.Contains(err.Error(), "secret-sentinel") {
			test.Fatalf("unsafe definition JSON accepted: %v", err)
		}
	}
}

func TestGeminiProviderRegistryIdentity(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal("builtin registration failed")
	}
	var descriptor agentconfig.Descriptor
	for _, candidate := range registry.List() {
		if candidate.Manifest.AgentID == "gemini" {
			descriptor = candidate
		}
	}
	manifest := descriptor.Manifest
	if manifest.PluginID != "builtin.gemini" || manifest.PluginVersion != "1.1.0" || manifest.Target != agentconfig.GeminiSettings || manifest.Format != agentconfig.JSON || len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != agentconfig.GeminiProviderPlanV1 || len(descriptor.DefinitionDigest) != 64 {
		test.Fatal("Gemini exact builtin identity missing")
	}
	request := agentconfig.GeminiProviderPlanRequest{APIVersion: 1, PluginID: manifest.PluginID, PluginVersion: manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: manifest.Target, Capabilities: []agentconfig.Capability{agentconfig.GeminiProviderPlanV1}, Desired: agentconfig.GeminiProviderDesired{AuthMode: agentconfig.GeminiNativeLogin}}
	for name, mutate := range map[string]func(*agentconfig.GeminiProviderPlanRequest){
		"api":     func(request *agentconfig.GeminiProviderPlanRequest) { request.APIVersion = 2 },
		"version": func(request *agentconfig.GeminiProviderPlanRequest) { request.PluginVersion = "1.0.0" },
		"digest":  func(request *agentconfig.GeminiProviderPlanRequest) { request.DefinitionDigest = "untrusted" },
		"target":  func(request *agentconfig.GeminiProviderPlanRequest) { request.Target = agentconfig.CodexConfig },
		"no-cap":  func(request *agentconfig.GeminiProviderPlanRequest) { request.Capabilities = nil },
		"foreign-cap": func(request *agentconfig.GeminiProviderPlanRequest) {
			request.Capabilities = []agentconfig.Capability{agentconfig.CodexProviderPlanV1}
		},
		"extra-cap": func(request *agentconfig.GeminiProviderPlanRequest) {
			request.Capabilities = append(request.Capabilities, "extra")
		},
		"duplicate-cap": func(request *agentconfig.GeminiProviderPlanRequest) {
			request.Capabilities = append(request.Capabilities, agentconfig.GeminiProviderPlanV1)
		},
	} {
		test.Run(name, func(test *testing.T) {
			changed := request
			changed.Capabilities = append([]agentconfig.Capability{}, request.Capabilities...)
			mutate(&changed)
			result, err := registry.PlanGeminiProvider(test.Context(), changed)
			if !errors.Is(err, agentadapter.ErrIncompatible) || result.Version != 0 {
				test.Fatal("incompatible Gemini identity accepted")
			}
		})
	}
	render := agentconfig.RenderRequest{APIVersion: 1, PluginID: request.PluginID, PluginVersion: request.PluginVersion, DefinitionDigest: request.DefinitionDigest, Target: request.Target, Capabilities: request.Capabilities}
	if _, err := registry.Render(test.Context(), render); !errors.Is(err, agentadapter.ErrUnsupported) {
		test.Fatal("Gemini planner opened generic Render")
	}
	encoded, err := json.Marshal(render)
	if err != nil {
		test.Fatal("render fixture encoding")
	}
	if _, err := registry.RenderJSON(test.Context(), encoded); !errors.Is(err, agentadapter.ErrUnsupported) {
		test.Fatal("Gemini planner opened RenderJSON")
	}
	request.PluginID = "unknown.plugin"
	if _, err := registry.PlanGeminiProvider(test.Context(), request); !errors.Is(err, agentadapter.ErrUnsupported) {
		test.Fatal("unknown Gemini plugin accepted")
	}
	if _, err := (*agentadapter.Registry)(nil).PlanGeminiProvider(test.Context(), request); !errors.Is(err, agentadapter.ErrUnsupported) {
		test.Fatal("nil Gemini registry accepted")
	}
}

func TestGeminiProviderDefinitionAndLegacyMetadata(test *testing.T) {
	definition := agentadapter.Definition{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "builtin.gemini", PluginVersion: "1.1.0", AgentID: "gemini", Target: agentconfig.GeminiSettings, Format: agentconfig.JSON, Capabilities: []agentconfig.Capability{agentconfig.GeminiProviderPlanV1}}, Module: `export function planGeminiProvider() { return {}; }`}
	registry, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal("valid Gemini definition rejected")
	}
	for name, mutate := range map[string]func(*agentadapter.Definition){
		"agent":  func(definition *agentadapter.Definition) { definition.Manifest.AgentID = "claude" },
		"target": func(definition *agentadapter.Definition) { definition.Manifest.Target = agentconfig.CodexConfig },
		"format": func(definition *agentadapter.Definition) { definition.Manifest.Format = agentconfig.TOML },
		"cap": func(definition *agentadapter.Definition) {
			definition.Manifest.Capabilities = []agentconfig.Capability{agentconfig.ClaudeProviderEnvV1}
		},
		"extra": func(definition *agentadapter.Definition) {
			definition.Manifest.Capabilities = []agentconfig.Capability{agentconfig.GeminiProviderPlanV1, "extra"}
		},
		"duplicate": func(definition *agentadapter.Definition) {
			definition.Manifest.Capabilities = []agentconfig.Capability{agentconfig.GeminiProviderPlanV1, agentconfig.GeminiProviderPlanV1}
		},
		"no-module":     func(definition *agentadapter.Definition) { definition.Module = "" },
		"metadata-code": func(definition *agentadapter.Definition) { definition.Manifest.Capabilities = nil },
	} {
		test.Run(name, func(test *testing.T) {
			changed := definition
			mutate(&changed)
			if _, err := agentadapter.NewRegistry([]agentadapter.Definition{changed}); !errors.Is(err, agentadapter.ErrInvalidDefinition) {
				test.Fatal("invalid Gemini definition registered")
			}
		})
	}
	oldDigest := registry.List()[0].DefinitionDigest
	definition.Module += "\n"
	replaced, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil || replaced.List()[0].DefinitionDigest == oldDigest {
		test.Fatal("Gemini module bytes not pinned")
	}
	request := agentconfig.GeminiProviderPlanRequest{APIVersion: 1, PluginID: "builtin.gemini", PluginVersion: "1.1.0", DefinitionDigest: oldDigest, Target: agentconfig.GeminiSettings, Capabilities: []agentconfig.Capability{agentconfig.GeminiProviderPlanV1}, Desired: agentconfig.GeminiProviderDesired{AuthMode: agentconfig.GeminiNativeLogin}}
	if _, err := replaced.PlanGeminiProvider(test.Context(), request); !errors.Is(err, agentadapter.ErrIncompatible) {
		test.Fatal("old digest authorized replaced Gemini module")
	}
	definition.Manifest.PluginVersion = "1.0.0"
	wrongVersion, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal("version fixture registration failed")
	}
	request.PluginVersion, request.DefinitionDigest = "1.0.0", wrongVersion.List()[0].DefinitionDigest
	if _, err := wrongVersion.PlanGeminiProvider(test.Context(), request); !errors.Is(err, agentadapter.ErrUnsupported) {
		test.Fatal("old version gained new planner capability")
	}
	definition.Module, definition.Manifest.PluginVersion, definition.Manifest.Capabilities = "", "1.0.0", nil
	metadata, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil || len(metadata.List()[0].Manifest.Capabilities) != 0 {
		test.Fatal("legacy Gemini metadata rejected or upgraded")
	}
	request.PluginVersion, request.DefinitionDigest, request.Capabilities = "1.0.0", metadata.List()[0].DefinitionDigest, nil
	if _, err := metadata.PlanGeminiProvider(test.Context(), request); !errors.Is(err, agentadapter.ErrUnsupported) {
		test.Fatal("legacy metadata gained planner")
	}
}
