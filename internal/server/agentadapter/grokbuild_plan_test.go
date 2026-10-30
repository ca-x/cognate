package agentadapter

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func grokDesired() agentconfig.GrokBuildProviderDesired {
	return agentconfig.GrokBuildProviderDesired{Mode: "custom-model", Model: &agentconfig.GrokBuildModelDesired{Profile: "Local profile", UpstreamModel: "x-ai/grok-4.5", Endpoint: "https://Example.invalid:443/Case/%76", Name: "Synthetic provider", APIBackend: "responses", ContextWindow: 500000, KeyPresent: true}}
}

func grokRequest(test *testing.T, registry *Registry, desired agentconfig.GrokBuildProviderDesired) agentconfig.GrokBuildProviderPlanRequest {
	test.Helper()
	for _, descriptor := range registry.List() {
		manifest := descriptor.Manifest
		if manifest.AgentID == "grokbuild" {
			return agentconfig.GrokBuildProviderPlanRequest{APIVersion: 1, PluginID: manifest.PluginID, PluginVersion: manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: manifest.Target, Capabilities: []agentconfig.Capability{"grokbuild.provider-plan.v1"}, Desired: desired}
		}
	}
	test.Fatal("Grok descriptor missing")
	return agentconfig.GrokBuildProviderPlanRequest{}
}

func grokRegistry(test *testing.T, module string) *Registry {
	test.Helper()
	registry, err := NewRegistry([]Definition{{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "fixture.grokbuild", PluginVersion: "1.0.0", AgentID: "grokbuild", Target: "grokbuild.config", Format: "toml", Capabilities: []agentconfig.Capability{"grokbuild.provider-plan.v1"}}, Module: module}})
	if err != nil {
		test.Fatal(err)
	}
	return registry
}

func TestGrokBuildProviderLiterals(test *testing.T) {
	registry, err := Builtins()
	if err != nil {
		test.Fatal(err)
	}
	for _, mode := range []string{"native", "key", "environment", "both", "max-window"} {
		test.Run(mode, func(test *testing.T) {
			desired := grokDesired()
			want := agentconfig.GrokBuildProviderDeclarationV1{Version: 1, Target: "grokbuild.config", Mode: "custom-model", Model: &agentconfig.GrokBuildModelDesired{Profile: "Local profile", UpstreamModel: "x-ai/grok-4.5", Endpoint: "https://Example.invalid:443/Case/%76", Name: "Synthetic provider", APIBackend: "responses", ContextWindow: 500000, KeyPresent: true}, CredentialReference: new(agentconfig.GrokBuildSecretSlot("grokbuild.api-key")), RequiredSlots: []agentconfig.GrokBuildSecretSlot{"grokbuild.api-key"}, LocalAuthPolicy: "preserve-local-login-v1"}
			if mode == "native" {
				desired = agentconfig.GrokBuildProviderDesired{Mode: "native-login"}
				want.Mode, want.Model, want.CredentialReference, want.RequiredSlots = "native-login", nil, nil, []agentconfig.GrokBuildSecretSlot{}
			}
			if mode == "environment" || mode == "both" {
				desired.Model.EnvKey, want.Model.EnvKey = new("XAI_TEST_KEY"), new("XAI_TEST_KEY")
				if mode == "environment" {
					desired.Model.KeyPresent, want.Model.KeyPresent = false, false
					want.CredentialReference, want.RequiredSlots = nil, []agentconfig.GrokBuildSecretSlot{}
				}
			}
			if mode == "max-window" {
				desired.Model.ContextWindow, want.Model.ContextWindow = 9007199254740991, 9007199254740991
			}
			result, err := registry.PlanGrokBuildProvider(test.Context(), grokRequest(test, registry, desired))
			if err != nil || !reflect.DeepEqual(result, want) {
				test.Fatalf("declaration must exactly preserve typed profile/upstream/credential semantics: %v", err)
			}
		})
	}
}

func TestGrokBuildInvalidBeforeModule(test *testing.T) {
	registry := grokRegistry(test, `export function planGrokBuildProvider() { throw new Error("SHOULD_NOT_EXECUTE"); }`)
	cases := []struct {
		name        string
		mutate      func(*agentconfig.GrokBuildProviderDesired)
		unsupported bool
	}{
		{"unknown-mode", func(value *agentconfig.GrokBuildProviderDesired) { value.Mode = "unknown" }, false},
		{"native-with-model", func(value *agentconfig.GrokBuildProviderDesired) { value.Mode = "native-login" }, false},
		{"missing-model", func(value *agentconfig.GrokBuildProviderDesired) { value.Model = nil }, false},
		{"missing-credential", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.KeyPresent = false }, false},
		{"empty-env", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.EnvKey = new("") }, false},
		{"invalid-env", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.EnvKey = new("XAI=KEY") }, false},
		{"numeric-env", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.EnvKey = new("1XAI_KEY") }, false},
		{"empty-profile", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.Profile = "" }, false},
		{"profile-space", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.Profile = " p" }, false},
		{"profile-long", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.Profile = strings.Repeat("p", 129) }, false},
		{"model-control", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.UpstreamModel = "bad\nmodel" }, false},
		{"name-invalid-utf8", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.Name = string([]byte{255}) }, false},
		{"zero-window", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.ContextWindow = 0 }, false},
		{"negative-window", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.ContextWindow = -1 }, false},
		{"inexact-window", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.ContextWindow = 9007199254740992 }, false},
		{"backend", func(value *agentconfig.GrokBuildProviderDesired) { value.Model.APIBackend = "different-backend" }, true},
	}
	for _, current := range cases {
		test.Run(current.name, func(test *testing.T) {
			desired := grokDesired()
			current.mutate(&desired)
			result, err := registry.PlanGrokBuildProvider(test.Context(), grokRequest(test, registry, desired))
			want := ErrInvalidRequest
			if current.unsupported {
				want = ErrUnsupported
			}
			if !errors.Is(err, want) || !reflect.DeepEqual(result, agentconfig.GrokBuildProviderDeclarationV1{}) {
				test.Fatalf("must refuse before executing throwing module: %v", err)
			}
		})
	}
	for _, endpoint := range []string{"http://localhost/v1", "https://user:password@example.invalid", "https://example.invalid?token=x", "https://example.invalid#x", "https://example.invalid:", "https://example.invalid:65536", "https://example.invalid:0", "https://example.invalid/%0a", "https://example.invalid/%5c", "https://example.invalid/%ff", "https://", " https://example.invalid"} {
		desired := grokDesired()
		desired.Model.Endpoint = endpoint
		if _, err := registry.PlanGrokBuildProvider(test.Context(), grokRequest(test, registry, desired)); !errors.Is(err, ErrInvalidRequest) {
			test.Fatal("invalid endpoint reached module", err)
		}
	}
}

func TestGrokBuildPinnedIdentityAndHostOutput(test *testing.T) {
	registry := grokRegistry(test, grokBuildModule)
	for _, field := range []string{"api", "plugin", "version", "digest", "target", "capabilities"} {
		request := grokRequest(test, registry, grokDesired())
		switch field {
		case "api":
			request.APIVersion++
		case "plugin":
			request.PluginID = "missing"
		case "version":
			request.PluginVersion = "2.0.0"
		case "digest":
			request.DefinitionDigest = strings.Repeat("0", 64)
		case "target":
			request.Target = "codex.config"
		case "capabilities":
			request.Capabilities = append(request.Capabilities, "codex.provider-plan.v1")
		}
		if result, err := registry.PlanGrokBuildProvider(test.Context(), request); err == nil || !reflect.DeepEqual(result, agentconfig.GrokBuildProviderDeclarationV1{}) {
			test.Fatal("mismatched plugin identity accepted")
		}
	}
	valid, err := registry.PlanGrokBuildProvider(test.Context(), grokRequest(test, registry, grokDesired()))
	if err != nil {
		test.Fatal("third-party positive", err)
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		test.Fatal(err)
	}
	for _, mutation := range []string{"result.target='codex.config'", "result.model.profile=result.model.upstreamModel", "result.model.contextWindow=1", "result.model.extra=true", "result.requiredSlots=[]", "delete result.credentialReference", "result.path='/tmp/arbitrary'", "result.model.envKey=null", "delete result.model"} {
		module := "export function planGrokBuildProvider() { const result=" + string(encoded) + "; " + mutation + "; return result; }"
		malicious := grokRegistry(test, module)
		result, err := malicious.PlanGrokBuildProvider(test.Context(), grokRequest(test, malicious, grokDesired()))
		if !errors.Is(err, ErrPluginResult) || !reflect.DeepEqual(result, agentconfig.GrokBuildProviderDeclarationV1{}) {
			test.Fatal("host accepted altered module declaration", err)
		}
	}
}

func TestGrokBuildContextPrivacyAndCopies(test *testing.T) {
	registry := grokRegistry(test, grokBuildModule)
	request := grokRequest(test, registry, grokDesired())
	request.Desired.Model.EnvKey = new("XAI_TEST_KEY")
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			result, err := registry.PlanGrokBuildProvider(test.Context(), request)
			if err != nil || result.Model == request.Desired.Model || result.Model.EnvKey == request.Desired.Model.EnvKey {
				test.Error("planner did not return independent owned model", err)
				return
			}
			*result.Model.EnvKey = "CHANGED"
			result.Model.Profile = "CHANGED"
		})
	}
	workers.Wait()
	if request.Desired.Model.Profile != "Local profile" || *request.Desired.Model.EnvKey != "XAI_TEST_KEY" {
		test.Fatal("result alias modified caller data")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := registry.PlanGrokBuildProvider(ctx, request); !errors.Is(err, context.Canceled) {
		test.Fatal("canceled entry accepted", err)
	}
	if _, err := registry.PlanGrokBuildProvider(nil, request); !errors.Is(err, ErrInvalidRequest) {
		test.Fatal("nil context accepted", err)
	}
	throwing := grokRegistry(test, `export function planGrokBuildProvider() { throw new Error("SYNTHETIC_PRIVATE_CONTENT"); }`)
	if _, err := throwing.PlanGrokBuildProvider(test.Context(), grokRequest(test, throwing, grokDesired())); !errors.Is(err, ErrPluginExecution) || strings.Contains(fmt.Sprintf("%+v", err), "SYNTHETIC_PRIVATE_CONTENT") {
		test.Fatal("private module failure escaped")
	}
	loop := grokRegistry(test, `export function planGrokBuildProvider() { while(true) {} }`)
	deadline, stop := context.WithTimeout(test.Context(), 50*time.Millisecond)
	defer stop()
	if _, err := loop.PlanGrokBuildProvider(deadline, grokRequest(test, loop, grokDesired())); !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal("running interpreter did not cancel", err)
	}
}

func TestGrokBuildBuiltinDeclared(test *testing.T) {
	registry, err := Builtins()
	if err != nil {
		test.Fatal(err)
	}
	for _, descriptor := range registry.List() {
		manifest := descriptor.Manifest
		if manifest.AgentID == "grokbuild" {
			if manifest.PluginID != "builtin.grokbuild" || manifest.PluginVersion != "1.0.0" || manifest.Target != "grokbuild.config" || manifest.Format != "toml" || len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != "grokbuild.provider-plan.v1" || len(descriptor.DefinitionDigest) != 64 {
				test.Fatal("wrong Grok Build descriptor")
			}
			return
		}
	}
	test.Fatal("Grok Build provider declaration capability missing")
}
