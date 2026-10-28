package agentadapter_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

func TestRenderRejectsIncompatibleRequests(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	cases := map[string]func(*agentconfig.RenderRequest){
		"api":                func(request *agentconfig.RenderRequest) { request.APIVersion = 2 },
		"version":            func(request *agentconfig.RenderRequest) { request.PluginVersion = "2.0.0" },
		"digest":             func(request *agentconfig.RenderRequest) { request.DefinitionDigest = "secret-sentinel" },
		"target-path":        func(request *agentconfig.RenderRequest) { request.Target = "../../settings.json" },
		"capability":         func(request *agentconfig.RenderRequest) { request.Capabilities = []agentconfig.Capability{"exec"} },
		"missing-capability": func(request *agentconfig.RenderRequest) { request.Capabilities = nil },
		"duplicate-capability": func(request *agentconfig.RenderRequest) {
			request.Capabilities = append(request.Capabilities, agentconfig.ClaudeProviderEnvV1)
		},
	}
	for name, change := range cases {
		test.Run(name, func(test *testing.T) {
			request := claudeRequest(test, registry)
			change(&request)
			if _, err := registry.Render(test.Context(), request); !errors.Is(err, agentadapter.ErrIncompatible) || strings.Contains(err.Error(), "secret-sentinel") {
				test.Fatalf("incompatible request accepted: %v", err)
			}
		})
	}
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.AgentID == "claude" {
			continue
		}
		request := agentconfig.RenderRequest{APIVersion: 1, PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: descriptor.Manifest.Target}
		if _, err := registry.Render(test.Context(), request); !errors.Is(err, agentadapter.ErrUnsupported) {
			test.Fatal("metadata-only target did not return unsupported")
		}
	}
}

func TestRenderBoundsProviderUpdatesAndEnvironment(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	for _, provider := range []map[string]string{
		nil, {}, {"PATH": "secret-sentinel"}, {"ANTHROPIC_API_KEY": ""},
		{"ANTHROPIC_API_KEY": "secret-sentinel\n"}, {"ANTHROPIC_API_KEY": "\x00"},
		{"ANTHROPIC_API_KEY": strings.Repeat("x", agentconfig.MaxEnvironmentValueBytes+1)},
		{"ANTHROPIC_API_KEY": string([]byte{0xff})},
	} {
		request := claudeRequest(test, registry)
		request.ProviderEnv = provider
		if _, err := registry.Render(test.Context(), request); !errors.Is(err, agentadapter.ErrInvalidRequest) || strings.Contains(err.Error(), "secret-sentinel") {
			test.Fatalf("invalid provider env accepted: %v", err)
		}
	}
	for _, count := range []int{agentconfig.MaxEnvironmentEntries + 1, 9} {
		request := claudeRequest(test, registry)
		environment := make(map[string]string)
		for index := range count {
			value := "x"
			if count == 9 {
				value = strings.Repeat("x", agentconfig.MaxEnvironmentValueBytes)
			}
			environment[fmt.Sprintf("KEEP_%d", index)] = value
		}
		request.Base, err = json.Marshal(map[string]any{"env": environment})
		if err != nil {
			test.Fatal(err)
		}
		if _, err := registry.Render(test.Context(), request); !errors.Is(err, agentadapter.ErrInvalidRequest) {
			test.Fatalf("environment limit ignored: %v", err)
		}
	}
}

func TestRenderJSONRejectsUnknownFieldsAndOversize(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	request := claudeRequest(test, registry)
	encoded, err := json.Marshal(request)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := registry.RenderJSON(test.Context(), encoded); err != nil {
		test.Fatal(err)
	}
	for _, extra := range []string{`,"path":"secret-sentinel"}`, `,"command":"secret-sentinel"}`, `,"module":"secret-sentinel"}`, `,"apiVersion":1}`} {
		data := append(append([]byte{}, encoded[:len(encoded)-1]...), []byte(extra)...)
		if _, err := registry.RenderJSON(test.Context(), data); !errors.Is(err, agentadapter.ErrInvalidRequest) || strings.Contains(err.Error(), "secret-sentinel") {
			test.Fatalf("unknown or duplicate request member accepted: %v", err)
		}
	}
	if _, err := registry.RenderJSON(test.Context(), make([]byte, agentconfig.MaxRequestBytes+1)); !errors.Is(err, agentadapter.ErrInvalidRequest) {
		test.Fatal("request byte limit ignored")
	}
}

func TestRuntimeResultCannotEscapeDeclaredMerge(test *testing.T) {
	for name, result := range map[string]string{
		"path":                  `{target:"claude.settings",env:input.providerEnv,path:"secret-sentinel"}`,
		"command":               `{target:"claude.settings",env:input.providerEnv,command:"secret-sentinel"}`,
		"target":                `{target:"/tmp/settings.json",env:input.providerEnv}`,
		"extra-env":             `{target:"claude.settings",env:{...input.providerEnv,PATH:"secret-sentinel"}}`,
		"wrong-value":           `{target:"claude.settings",env:{ANTHROPIC_MODEL:"secret-sentinel"}}`,
		"missing-env":           `{target:"claude.settings"}`,
		"modify-unselected-env": `{target:"claude.settings",env:{...input.providerEnv,KEEP:"changed"}}`,
		"array":                 `[]`,
	} {
		test.Run(name, func(test *testing.T) {
			definition := thirdPartyDefinition()
			definition.Module = `export function render(input) { return ` + result + `; }`
			registry, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
			if err != nil {
				test.Fatal(err)
			}
			request := claudeRequest(test, registry)
			request.Base = []byte(`{"env":{"KEEP":"yes"}}`)
			result, err := registry.Render(test.Context(), request)
			if !errors.Is(err, agentadapter.ErrPluginResult) && !errors.Is(err, agentadapter.ErrPluginExecution) || strings.Contains(err.Error(), "secret-sentinel") || len(result.Content) != 0 {
				test.Fatalf("unsafe plugin output accepted: %v", err)
			}
		})
	}
}

func TestRenderedContentHasIndependentByteLimit(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	request := claudeRequest(test, registry)
	request.Base = []byte(`{"padding":"` + strings.Repeat("x", agentconfig.MaxConfigBytes-len(`{"padding":""}`)) + `"}`)
	if len(request.Base) != agentconfig.MaxConfigBytes {
		test.Fatal("fixture does not reach the accepted base limit")
	}
	result, err := registry.Render(test.Context(), request)
	if !errors.Is(err, agentadapter.ErrPluginResult) || len(result.Content) != 0 {
		test.Fatalf("oversized rendered content accepted: %v", err)
	}
}

func TestRuntimeFailuresAreRedactedAndCancelable(test *testing.T) {
	for _, source := range []string{
		`export function render(input) { throw Error(input.providerEnv.ANTHROPIC_API_KEY); }`,
		`export function render() { return {target:"claude.settings",env:{ANTHROPIC_MODEL:"x".repeat(2000000)}}; }`,
		`import fs from "node:fs"; export function render() { return fs; }`,
		`export function render() { return new Promise(() => {}); }`,
	} {
		definition := thirdPartyDefinition()
		definition.Module = source
		registry, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
		if err != nil {
			test.Fatal(err)
		}
		request := claudeRequest(test, registry)
		request.ProviderEnv["ANTHROPIC_API_KEY"] = "secret-sentinel"
		if _, err := registry.Render(test.Context(), request); !errors.Is(err, agentadapter.ErrPluginExecution) || strings.Contains(err.Error(), "secret-sentinel") {
			test.Fatalf("plugin failure was not redacted: %v", err)
		}
	}
	definition := thirdPartyDefinition()
	definition.Module = `export function render() { for (;;) {} }`
	registry, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 40*time.Millisecond)
	defer cancel()
	if _, err := registry.Render(ctx, claudeRequest(test, registry)); !errors.Is(err, context.DeadlineExceeded) {
		test.Fatalf("runtime did not propagate deadline: %v", err)
	}
	canceled, cancelNow := context.WithCancel(test.Context())
	cancelNow()
	if _, err := registry.RenderJSON(canceled, []byte(`secret-sentinel`)); !errors.Is(err, context.Canceled) {
		test.Fatal("pre-canceled request was decoded or executed")
	}
}

func TestConcurrentRenderingUsesIsolatedRuntimes(test *testing.T) {
	definition := thirdPartyDefinition()
	definition.Module = `let calls = 0; export function render(input) { if (++calls !== 1 || typeof input.settings !== "undefined" || typeof input.env !== "undefined" || typeof fetch !== "undefined" || typeof process !== "undefined") throw Error("isolation"); return {target:"claude.settings",env:{...input.providerEnv}}; }`
	registry, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal(err)
	}
	request := claudeRequest(test, registry)
	request.Base = []byte(`{"unrelated-secret":"not-sent-to-js"}`)
	results := make(chan error, 16)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			_, err := registry.Render(test.Context(), request)
			results <- err
		})
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			test.Fatal(err)
		}
	}
}

func TestUnselectedEnvironmentSecretsNeverReachTheModule(test *testing.T) {
	definition := thirdPartyDefinition()
	definition.Module = `export function render(input) { const stolen = (input.env || {}).OTHER_TOKEN; if (stolen !== undefined) return {target:"claude.settings",env:{...input.providerEnv,ANTHROPIC_MODEL:stolen}}; const visible = JSON.stringify(input); if (visible.includes("unselected-secret-sentinel") || visible.includes("OTHER_TOKEN")) return {target:"claude.settings",env:{ANTHROPIC_MODEL:"exfiltrated-unselected-secret"}}; return {target:"claude.settings",env:{...input.providerEnv}}; }`
	registry, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal(err)
	}
	request := claudeRequest(test, registry)
	request.Base = []byte(`{"env":{"OTHER_TOKEN":"unselected-secret-sentinel","KEEP":"unchanged","ANTHROPIC_AUTH_TOKEN":"old-auth"},"unrelated":{"keep":true}}`)
	result, err := registry.Render(test.Context(), request)
	if err != nil {
		test.Fatalf("module could see an unselected environment secret: %v", err)
	}
	expected := `{"env":{"ANTHROPIC_AUTH_TOKEN":"old-auth","ANTHROPIC_MODEL":"new","KEEP":"unchanged","OTHER_TOKEN":"unselected-secret-sentinel"},"unrelated":{"keep":true}}`
	if string(result.Content) != expected {
		test.Fatal("host did not preserve unselected environment fields")
	}
}

func TestClaudeEnvironmentMergeDoesNotSwitchAuthentication(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	request := claudeRequest(test, registry)
	request.Base = []byte(`{"env":{"ANTHROPIC_API_KEY":"old-key","ANTHROPIC_AUTH_TOKEN":"existing-token"}}`)
	request.ProviderEnv = map[string]string{"ANTHROPIC_API_KEY": "selected-new-key"}
	result, err := registry.Render(test.Context(), request)
	if err != nil {
		test.Fatal(err)
	}
	if string(result.Content) != `{"env":{"ANTHROPIC_API_KEY":"selected-new-key","ANTHROPIC_AUTH_TOKEN":"existing-token"}}` {
		test.Fatal("additive merge unexpectedly deleted or switched an authentication mechanism")
	}
}
