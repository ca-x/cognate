package agentadapter

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func openCodeRequest(test *testing.T, registry *Registry, provider agentconfig.OpenCodeProvider) agentconfig.OpenCodeProviderPlanRequest {
	test.Helper()
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.AgentID == "opencode" {
			manifest := descriptor.Manifest
			return agentconfig.OpenCodeProviderPlanRequest{APIVersion: 1, PluginID: manifest.PluginID, PluginVersion: manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: manifest.Target, Capabilities: manifest.Capabilities, Desired: agentconfig.OpenCodeProviderDesired{Provider: provider, Slots: []agentconfig.OpenCodeSlotStatus{{Slot: "opencode.value.01", Present: true}}}}
		}
	}
	test.Fatal("OpenCode descriptor missing")
	return agentconfig.OpenCodeProviderPlanRequest{}
}

type openCodeTailContext struct {
	context.Context
	cancel context.CancelFunc
	direct atomic.Int32
}

func (ctx *openCodeTailContext) Err() error {
	programCounter, _, _, ok := runtime.Caller(1)
	if ok {
		caller := runtime.FuncForPC(programCounter)
		if caller != nil && strings.HasSuffix(caller.Name(), ".(*Registry).PlanOpenCodeProvider") && ctx.direct.Add(1) == 2 {
			ctx.cancel()
		}
	}
	return ctx.Context.Err()
}

func TestOpenCodeProviderReturnTailCancellation(test *testing.T) {
	parent, cancel := context.WithCancel(test.Context())
	defer cancel()
	ctx := &openCodeTailContext{Context: parent, cancel: cancel}
	registry := openCodeRegistry(test, opencodeModule)
	result, err := registry.PlanOpenCodeProvider(ctx, openCodeRequest(test, registry, openCodeDesired("v2")))
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, agentconfig.OpenCodeProviderDeclarationV1{}) || ctx.direct.Load() != 2 {
		test.Fatal("return-tail cancellation exposed a successful declaration")
	}
}

func TestOpenCodeProviderIdentityAndSlots(test *testing.T) {
	registry := openCodeRegistry(test, opencodeModule)
	for name, change := range map[string]func(*agentconfig.OpenCodeProviderPlanRequest){
		"api":     func(request *agentconfig.OpenCodeProviderPlanRequest) { request.APIVersion = 2 },
		"version": func(request *agentconfig.OpenCodeProviderPlanRequest) { request.PluginVersion = "2.0.0" },
		"digest": func(request *agentconfig.OpenCodeProviderPlanRequest) {
			request.DefinitionDigest = strings.Repeat("0", 64)
		},
		"target":      func(request *agentconfig.OpenCodeProviderPlanRequest) { request.Target = agentconfig.ClaudeSettings },
		"cap-missing": func(request *agentconfig.OpenCodeProviderPlanRequest) { request.Capabilities = nil },
		"cap-extra": func(request *agentconfig.OpenCodeProviderPlanRequest) {
			request.Capabilities = []agentconfig.Capability{agentconfig.OpenCodeProviderPlanV1, "extra"}
		},
		"cap-wrong": func(request *agentconfig.OpenCodeProviderPlanRequest) {
			request.Capabilities = []agentconfig.Capability{agentconfig.GeminiProviderPlanV1}
		},
	} {
		test.Run(name, func(test *testing.T) {
			request := openCodeRequest(test, registry, openCodeDesired("v2"))
			change(&request)
			result, err := registry.PlanOpenCodeProvider(test.Context(), request)
			if !errors.Is(err, ErrIncompatible) || !reflect.DeepEqual(result, agentconfig.OpenCodeProviderDeclarationV1{}) {
				test.Fatal("incompatible planner identity accepted")
			}
		})
	}
	for name, slots := range map[string][]agentconfig.OpenCodeSlotStatus{
		"missing":      nil,
		"unreferenced": {{Slot: "opencode.api-key", Present: true}},
		"duplicate":    {{Slot: "opencode.value.01"}, {Slot: "opencode.value.01"}},
	} {
		test.Run(name, func(test *testing.T) {
			request := openCodeRequest(test, registry, openCodeDesired("v2"))
			request.Desired.Slots = slots
			if _, err := registry.PlanOpenCodeProvider(test.Context(), request); !errors.Is(err, ErrInvalidRequest) {
				test.Fatal("unbound slot presence accepted")
			}
		})
	}
	request := openCodeRequest(test, registry, openCodeDesired("v2"))
	if _, err := (*Registry)(nil).PlanOpenCodeProvider(test.Context(), request); !errors.Is(err, ErrUnsupported) {
		test.Fatal("nil registry accepted")
	}
	request.PluginID = "unknown"
	if _, err := registry.PlanOpenCodeProvider(test.Context(), request); !errors.Is(err, ErrUnsupported) {
		test.Fatal("unknown plugin accepted")
	}
	request = openCodeRequest(test, registry, openCodeDesired("v2"))
	if _, err := registry.Render(test.Context(), agentconfig.RenderRequest{APIVersion: 1, PluginID: request.PluginID, PluginVersion: request.PluginVersion, DefinitionDigest: request.DefinitionDigest, Target: request.Target, Capabilities: request.Capabilities}); !errors.Is(err, ErrUnsupported) {
		test.Fatal("generic renderer widened")
	}
}

func TestOpenCodeProviderConcurrentClone(test *testing.T) {
	registry := openCodeRegistry(test, opencodeModule)
	request := openCodeRequest(test, registry, openCodeDesired("v2"))
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			result, err := registry.PlanOpenCodeProvider(test.Context(), request)
			if err != nil {
				test.Error("concurrent planner failed")
				return
			}
			(*result.Provider.V2.Body)["private-location"] = agentconfig.OpenCodeValue{Bool: new(false)}
			result.Plan.RequiredSlots[0] = "changed"
		})
	}
	workers.Wait()
	if (*request.Desired.Provider.V2.Body)["private-location"].Reference == nil || request.Desired.Slots[0].Slot != "opencode.value.01" {
		test.Fatal("parallel results alias input")
	}
}

func TestOpenCodeProviderDesiredBudget(test *testing.T) {
	registry := openCodeRegistry(test, opencodeModule)
	request := openCodeRequest(test, registry, openCodeDesired("v2"))
	request.Desired.Slots = nil
	body := make(map[string]agentconfig.OpenCodeValue)
	for index := range 64 {
		slot := "opencode.api-key"
		if index > 0 {
			slot = fmt.Sprintf("opencode.value.%02d", index)
		}
		body[fmt.Sprintf("reference-%02d", index)] = agentconfig.OpenCodeValue{Reference: new(slot)}
		request.Desired.Slots = append(request.Desired.Slots, agentconfig.OpenCodeSlotStatus{Slot: slot})
	}
	request.Desired.Provider.V2.Body = &body
	found := false
	for count := range 256 {
		body[fmt.Sprintf("%s%03d", strings.Repeat("x", 240), count)] = agentconfig.OpenCodeValue{Bool: new(false)}
		provider, err := json.Marshal(request.Desired.Provider)
		if err != nil {
			test.Fatal("budget fixture encoding failed")
		}
		desired, err := json.Marshal(request.Desired)
		if err != nil {
			test.Fatal("budget fixture encoding failed")
		}
		if len(provider) <= agentconfig.MaxOpenCodeDesiredBytes && len(desired) > agentconfig.MaxOpenCodeDesiredBytes {
			found = true
			break
		}
	}
	if !found {
		test.Fatal("desired budget fixture did not reach boundary")
	}
	if _, err := registry.PlanOpenCodeProvider(test.Context(), request); !errors.Is(err, ErrInvalidRequest) {
		test.Fatal("aggregate desired budget bypassed")
	}
}

func openCodeRegistry(test *testing.T, module string) *Registry {
	test.Helper()
	registry, err := NewRegistry([]Definition{{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "builtin.opencode", PluginVersion: "1.0.0", AgentID: "opencode", Target: agentconfig.OpenCodeConfig, Format: agentconfig.JSON, Capabilities: []agentconfig.Capability{agentconfig.OpenCodeProviderPlanV1}}, Module: module}})
	if err != nil {
		test.Fatal("OpenCode registry failed")
	}
	return registry
}

func openCodeDesired(format string) agentconfig.OpenCodeProvider {
	provider := agentconfig.OpenCodeProvider{ProviderID: "vendor", Format: format}
	if format == "v1" {
		provider.V1 = &agentconfig.OpenCodeV1{Options: &agentconfig.OpenCodeV1Options{APIKey: new("opencode.value.01")}}
	} else {
		provider.V2 = &agentconfig.OpenCodeV2{Body: new(map[string]agentconfig.OpenCodeValue{"private-location": {Reference: new("opencode.value.01")}})}
	}
	return provider
}

func TestOpenCodeProviderPlan(test *testing.T) {
	registry, err := Builtins()
	if err != nil {
		test.Fatal("builtins failed")
	}
	for _, format := range []string{"v1", "v2"} {
		test.Run(format, func(test *testing.T) {
			provider := openCodeDesired(format)
			request := openCodeRequest(test, registry, provider)
			result, err := registry.PlanOpenCodeProvider(test.Context(), request)
			container := "provider"
			if format == "v2" {
				container = "providers"
			}
			if err != nil || !reflect.DeepEqual(result.Provider, provider) || result.Plan.Version != 1 || result.Plan.Target != "opencode.config" || result.Plan.Mode != "coexist" || result.Plan.Container != container || result.Plan.DefinitionRef != "host-validated-definition-v1" || !reflect.DeepEqual(result.Plan.RequiredSlots, []string{"opencode.value.01"}) {
				test.Fatal("reference plan mismatch")
			}
			result.Provider.ProviderID = "changed"
			if format == "v1" {
				*result.Provider.V1.Options.APIKey = "opencode.value.02"
			} else {
				(*result.Provider.V2.Body)["private-location"] = agentconfig.OpenCodeValue{Null: new(true)}
			}
			next, err := registry.PlanOpenCodeProvider(test.Context(), request)
			if err != nil || !reflect.DeepEqual(next.Provider, provider) {
				test.Fatal("result aliases input")
			}
		})
	}
}

func TestOpenCodeProviderJSPrivacy(test *testing.T) {
	module := `export function planOpenCodeProvider(input) {
  if (Object.keys(input).sort().join(",") !== "format,providerID,requiredSlots") throw Error("unsafe input");
  if (JSON.stringify(input).includes("private-location")) throw Error("definition leaked");
  if (typeof fetch !== "undefined" || typeof process !== "undefined" || typeof require !== "undefined") throw Error("host capability");
  return {version:1,target:"opencode.config",mode:"coexist",format:input.format,container:"providers",providerID:input.providerID,definitionRef:"host-validated-definition-v1",requiredSlots:input.requiredSlots};
}`
	registry := openCodeRegistry(test, module)
	if _, err := registry.PlanOpenCodeProvider(test.Context(), openCodeRequest(test, registry, openCodeDesired("v2"))); err != nil {
		test.Fatal("private-free planner failed")
	}
}

func TestOpenCodeProviderHostRejectsModules(test *testing.T) {
	for name, module := range map[string]string{
		"wrong-container": strings.Replace(opencodeModule, `"provider" : "providers"`, `"providers" : "provider"`, 1),
		"wrong-reference": strings.ReplaceAll(opencodeModule, "host-validated-definition-v1", "untrusted-definition"),
		"extra":           strings.Replace(opencodeModule, "version: 1,", "version: 1, extra: true,", 1),
		"null":            `export function planOpenCodeProvider() { return null; }`,
		"throw":           `export function planOpenCodeProvider() { throw Error("synthetic-private"); }`,
		"dynamic-eval":    `export function planOpenCodeProvider() { return eval("({version:1})"); }`,
		"oversize":        `export function planOpenCodeProvider() { return {privateValue:"x".repeat(100000)}; }`,
	} {
		test.Run(name, func(test *testing.T) {
			registry := openCodeRegistry(test, module)
			result, err := registry.PlanOpenCodeProvider(test.Context(), openCodeRequest(test, registry, openCodeDesired("v2")))
			if err == nil || !reflect.DeepEqual(result, agentconfig.OpenCodeProviderDeclarationV1{}) || strings.Contains(err.Error(), "synthetic-private") {
				test.Fatal("unsafe module accepted or leaked")
			}
		})
	}
}

func TestOpenCodeProviderCancellation(test *testing.T) {
	registry := openCodeRegistry(test, `export function planOpenCodeProvider() { while (true) {} }`)
	ctx, cancel := context.WithTimeout(test.Context(), 50*time.Millisecond)
	defer cancel()
	result, err := registry.PlanOpenCodeProvider(ctx, openCodeRequest(test, registry, openCodeDesired("v2")))
	if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(result, agentconfig.OpenCodeProviderDeclarationV1{}) {
		test.Fatal("planner cancellation missing")
	}
}
