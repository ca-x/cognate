package agentadapter_test

import (
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

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
		if manifest.AgentID == "claude" {
			expectedVersion = "1.1.0"
		}
		if manifest.APIVersion != 1 || manifest.PluginVersion != expectedVersion || len(descriptor.DefinitionDigest) != 64 || expected[manifest.AgentID] != manifest.Target {
			test.Fatal("invalid built-in metadata")
		}
		delete(expected, manifest.AgentID)
		if manifest.AgentID == "claude" && (len(manifest.Capabilities) != 2 || manifest.Capabilities[0] != agentconfig.ClaudeProviderEnvV1 || manifest.Capabilities[1] != "claude.provider-switch.v1") {
			test.Fatal("Claude must declare additive and complete provider capabilities")
		}
		if manifest.AgentID != "claude" && len(manifest.Capabilities) != 0 {
			test.Fatal("metadata-only adapter advertises unsupported rendering")
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
