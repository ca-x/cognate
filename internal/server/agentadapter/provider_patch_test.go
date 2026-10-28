package agentadapter_test

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
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

func providerPatchRequest(test *testing.T, registry *agentadapter.Registry) agentconfig.ClaudeProviderPatchRequest {
	test.Helper()
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.Target == agentconfig.ClaudeSettings {
			manifest := descriptor.Manifest
			return agentconfig.ClaudeProviderPatchRequest{APIVersion: 1, PluginID: manifest.PluginID, PluginVersion: manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: manifest.Target, Capabilities: []agentconfig.Capability{agentconfig.ClaudeProviderSwitchV1}, Desired: agentconfig.ClaudeProviderDesired{AuthMode: agentconfig.ClaudeNativeLogin}}
		}
	}
	test.Fatal("Claude adapter is missing")
	return agentconfig.ClaudeProviderPatchRequest{}
}

func removedProviderOperations() []agentconfig.ClaudeProviderPatchOperation {
	return []agentconfig.ClaudeProviderPatchOperation{
		{Slot: agentconfig.ClaudeEnvAPIKey, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvAuthToken, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvBaseURL, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvModel, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvHaikuModel, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvSonnetModel, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvOpusModel, Action: agentconfig.ClaudeRemove},
	}
}

func TestProviderPatchNativeLoginRemovesAllAbsentOverrides(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	patch, err := registry.PlanClaudeProvider(test.Context(), providerPatchRequest(test, registry))
	want := agentconfig.ClaudeProviderPatchV1{Target: agentconfig.ClaudeSettings, Operations: removedProviderOperations()}
	if err != nil || !reflect.DeepEqual(patch, want) {
		test.Fatalf("native-login did not produce seven explicit removals: %v", err)
	}
}

func TestProviderPatchMapsEveryRoleAndSelectedSecret(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	request := providerPatchRequest(test, registry)
	request.Desired = agentconfig.ClaudeProviderDesired{AuthMode: agentconfig.ClaudeAPIKeyAuth, APIKeyPresent: true, Endpoint: new("https://provider.invalid/v1"), Models: agentconfig.ClaudeModelsDesired{Default: new("Default"), Haiku: new("Haiku"), Sonnet: new("Sonnet"), Opus: new("Opus")}}
	want := agentconfig.ClaudeProviderPatchV1{Target: agentconfig.ClaudeSettings, Operations: []agentconfig.ClaudeProviderPatchOperation{
		{Slot: agentconfig.ClaudeEnvAPIKey, Action: agentconfig.ClaudeSetSelectedSecret, Reference: new(agentconfig.ClaudeAPIKeySecret)},
		{Slot: agentconfig.ClaudeEnvAuthToken, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvBaseURL, Action: agentconfig.ClaudeSetLiteral, Literal: new("https://provider.invalid/v1")},
		{Slot: agentconfig.ClaudeEnvModel, Action: agentconfig.ClaudeSetLiteral, Literal: new("Default")},
		{Slot: agentconfig.ClaudeEnvHaikuModel, Action: agentconfig.ClaudeSetLiteral, Literal: new("Haiku")},
		{Slot: agentconfig.ClaudeEnvSonnetModel, Action: agentconfig.ClaudeSetLiteral, Literal: new("Sonnet")},
		{Slot: agentconfig.ClaudeEnvOpusModel, Action: agentconfig.ClaudeSetLiteral, Literal: new("Opus")},
	}}
	patch, err := registry.PlanClaudeProvider(test.Context(), request)
	if err != nil || !reflect.DeepEqual(patch, want) {
		test.Fatalf("complete provider state was not mapped: %v", err)
	}
}

func assertProviderPatchError(test *testing.T, registry *agentadapter.Registry, ctx context.Context, request agentconfig.ClaudeProviderPatchRequest, want error) {
	test.Helper()
	patch, err := registry.PlanClaudeProvider(ctx, request)
	if !errors.Is(err, want) || !reflect.DeepEqual(patch, agentconfig.ClaudeProviderPatchV1{}) {
		test.Fatal("provider patch did not return the required error and zero result")
	}
	if strings.Contains(err.Error(), "private-patch-sentinel") {
		test.Fatal("provider patch error exposed private input")
	}
}

func TestProviderPatchAuthAndAllOptionalPresenceCombinations(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	for _, mode := range []agentconfig.ClaudeAuthMode{agentconfig.ClaudeNativeLogin, agentconfig.ClaudeAPIKeyAuth, agentconfig.ClaudeAuthTokenAuth} {
		for presence := range 32 {
			test.Run(fmt.Sprintf("%s/%02d", mode, presence), func(test *testing.T) {
				request := providerPatchRequest(test, registry)
				request.Desired.AuthMode = mode
				request.Desired.APIKeyPresent = mode == agentconfig.ClaudeAPIKeyAuth
				request.Desired.AuthTokenPresent = mode == agentconfig.ClaudeAuthTokenAuth
				values := []string{"http://localhost:4321/v1", " Default ", "Haiku", "Sonnet", "Opus"}
				fields := []**string{&request.Desired.Endpoint, &request.Desired.Models.Default, &request.Desired.Models.Haiku, &request.Desired.Models.Sonnet, &request.Desired.Models.Opus}
				operations := removedProviderOperations()
				for index, field := range fields {
					if presence&(1<<index) != 0 {
						*field = new(values[index])
						operations[index+2].Action = agentconfig.ClaudeSetLiteral
						operations[index+2].Literal = new(values[index])
					}
				}
				if mode == agentconfig.ClaudeNativeLogin && presence&1 != 0 {
					assertProviderPatchError(test, registry, test.Context(), request, agentadapter.ErrInvalidRequest)
					return
				}
				if mode == agentconfig.ClaudeAPIKeyAuth {
					operations[0].Action, operations[0].Reference = agentconfig.ClaudeSetSelectedSecret, new(agentconfig.ClaudeAPIKeySecret)
				}
				if mode == agentconfig.ClaudeAuthTokenAuth {
					operations[1].Action, operations[1].Reference = agentconfig.ClaudeSetSelectedSecret, new(agentconfig.ClaudeAuthTokenSecret)
				}
				patch, err := registry.PlanClaudeProvider(test.Context(), request)
				if err != nil || !reflect.DeepEqual(patch, agentconfig.ClaudeProviderPatchV1{Target: agentconfig.ClaudeSettings, Operations: operations}) {
					test.Fatal("optional presence was not mapped to exact set/remove operations")
				}
			})
		}
	}
}

func TestProviderPatchRejectsInvalidDesiredState(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	for _, mode := range []agentconfig.ClaudeAuthMode{agentconfig.ClaudeNativeLogin, agentconfig.ClaudeAPIKeyAuth, agentconfig.ClaudeAuthTokenAuth, "unknown-private-patch-sentinel"} {
		for flags := range 4 {
			request := providerPatchRequest(test, registry)
			request.Desired.AuthMode, request.Desired.APIKeyPresent, request.Desired.AuthTokenPresent = mode, flags&1 != 0, flags&2 != 0
			if mode == agentconfig.ClaudeNativeLogin && flags == 0 || mode == agentconfig.ClaudeAPIKeyAuth && flags == 1 || mode == agentconfig.ClaudeAuthTokenAuth && flags == 2 {
				continue
			}
			assertProviderPatchError(test, registry, test.Context(), request, agentadapter.ErrInvalidRequest)
		}
	}
	for _, endpoint := range []string{
		"", "relative", "http:opaque", "https://", "ftp://provider.invalid", "https://user:private-patch-sentinel@provider.invalid",
		"https://provider.invalid?", "https://provider.invalid?q=private-patch-sentinel", "https://provider.invalid#", "https://provider.invalid/#private-patch-sentinel",
		"https://provider.invalid/\n", "https://provider.invalid/\x7f", "https://provider.invalid/\xff", "https://provider.invalid/%zz", "https://host:bad", "http://[broken",
		"https://provider.invalid/" + strings.Repeat("x", 2048),
	} {
		request := providerPatchRequest(test, registry)
		request.Desired.AuthMode, request.Desired.APIKeyPresent, request.Desired.Endpoint = agentconfig.ClaudeAPIKeyAuth, true, new(endpoint)
		assertProviderPatchError(test, registry, test.Context(), request, agentadapter.ErrInvalidRequest)
	}
	for _, invalid := range []string{"", "private-patch-sentinel\n", "\x00", "\x7f", "\xff", strings.Repeat("x", 257), strings.Repeat("é", 129)} {
		for fieldIndex := range 4 {
			request := providerPatchRequest(test, registry)
			fields := []**string{&request.Desired.Models.Default, &request.Desired.Models.Haiku, &request.Desired.Models.Sonnet, &request.Desired.Models.Opus}
			*fields[fieldIndex] = new(invalid)
			assertProviderPatchError(test, registry, test.Context(), request, agentadapter.ErrInvalidRequest)
		}
	}
	request := providerPatchRequest(test, registry)
	request.Desired.AuthMode, request.Desired.APIKeyPresent = agentconfig.ClaudeAPIKeyAuth, true
	prefix := "https://provider.invalid/"
	request.Desired.Endpoint = new(prefix + strings.Repeat("x", 2048-len(prefix)))
	request.Desired.Models = agentconfig.ClaudeModelsDesired{Default: new(strings.Repeat("é", 128)), Haiku: new(strings.Repeat("x", 256)), Sonnet: new(" sonnet "), Opus: new("Opus")}
	patch, err := registry.PlanClaudeProvider(test.Context(), request)
	if err != nil || *patch.Operations[2].Literal != *request.Desired.Endpoint || *patch.Operations[3].Literal != *request.Desired.Models.Default || *patch.Operations[5].Literal != " sonnet " {
		test.Fatal("exact byte bounds or literal preservation failed")
	}
}

func TestProviderPatchRequiresExactIdentityAndDeclaredCapability(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	for _, change := range []func(*agentconfig.ClaudeProviderPatchRequest){
		func(request *agentconfig.ClaudeProviderPatchRequest) { request.APIVersion++ },
		func(request *agentconfig.ClaudeProviderPatchRequest) { request.PluginVersion = "1.0.0" },
		func(request *agentconfig.ClaudeProviderPatchRequest) {
			request.DefinitionDigest = "private-patch-sentinel"
		},
		func(request *agentconfig.ClaudeProviderPatchRequest) { request.Target = "../../settings.json" },
		func(request *agentconfig.ClaudeProviderPatchRequest) { request.Capabilities = nil },
		func(request *agentconfig.ClaudeProviderPatchRequest) {
			request.Capabilities = []agentconfig.Capability{agentconfig.ClaudeProviderEnvV1}
		},
		func(request *agentconfig.ClaudeProviderPatchRequest) {
			request.Capabilities = []agentconfig.Capability{agentconfig.ClaudeProviderSwitchV1, agentconfig.ClaudeProviderSwitchV1}
		},
		func(request *agentconfig.ClaudeProviderPatchRequest) {
			request.Capabilities = []agentconfig.Capability{agentconfig.ClaudeProviderEnvV1, agentconfig.ClaudeProviderSwitchV1}
		},
	} {
		request := providerPatchRequest(test, registry)
		change(&request)
		assertProviderPatchError(test, registry, test.Context(), request, agentadapter.ErrIncompatible)
	}
	request := providerPatchRequest(test, registry)
	request.PluginID = "unknown"
	assertProviderPatchError(test, registry, test.Context(), request, agentadapter.ErrUnsupported)
	definition := thirdPartyDefinition()
	additive, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal(err)
	}
	assertProviderPatchError(test, additive, test.Context(), providerPatchRequest(test, additive), agentadapter.ErrUnsupported)
	definition.Manifest.PluginID, definition.Manifest.PluginVersion = "builtin.claude", "1.0.0"
	definition.Module = "export function render(input) {\n  const env = Object.create(null);\n  for (const key of Object.keys(input.providerEnv)) env[key] = input.providerEnv[key];\n  return {target: \"claude.settings\", env};\n}\n"
	legacy, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal(err)
	}
	legacyRequest := claudeRequest(test, legacy)
	if _, err := registry.Render(test.Context(), legacyRequest); !errors.Is(err, agentadapter.ErrIncompatible) {
		test.Fatal("old exact additive identity was silently upgraded")
	}
	legacyRequest.PluginVersion = "1.1.0"
	if _, err := registry.Render(test.Context(), legacyRequest); !errors.Is(err, agentadapter.ErrIncompatible) {
		test.Fatal("old digest was accepted with a new version")
	}
}

func patchRegistry(test *testing.T, module string) *agentadapter.Registry {
	test.Helper()
	definition := thirdPartyDefinition()
	definition.Manifest.Capabilities = []agentconfig.Capability{agentconfig.ClaudeProviderSwitchV1}
	definition.Module = module
	registry, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
	if err != nil {
		test.Fatal(err)
	}
	return registry
}

func nativePatchModule(test *testing.T, beforeReturn string) string {
	test.Helper()
	encoded, err := json.Marshal(agentconfig.ClaudeProviderPatchV1{Target: agentconfig.ClaudeSettings, Operations: removedProviderOperations()})
	if err != nil {
		test.Fatal(err)
	}
	return `export function planClaudeProvider(input) { const patch = ` + string(encoded) + `; ` + beforeReturn + `; return patch; }`
}

func TestProviderPatchRejectsMaliciousModuleOutputAndInputMutation(test *testing.T) {
	for name, mutation := range map[string]string{
		"missing":                `patch.operations.pop()`,
		"duplicate":              `patch.operations[6] = patch.operations[5]`,
		"reordered":              `patch.operations.reverse()`,
		"extra-slot":             `patch.operations.push({slot:"PATH",action:"remove"})`,
		"extra-member":           `patch.operations[3].command = "private-patch-sentinel"`,
		"arbitrary-path":         `patch.operations[3].slot = "/env/ANTHROPIC_MODEL"`,
		"target":                 `patch.target = "../../settings.json"`,
		"root-extra":             `patch.path = null`,
		"root-command":           `patch.command = ""`,
		"null-operation":         `patch.operations[3] = null`,
		"unknown-action":         `patch.operations[3].action = "exec"`,
		"remove-literal-empty":   `patch.operations[3].literal = ""`,
		"remove-literal-null":    `patch.operations[3].literal = null`,
		"remove-reference-empty": `patch.operations[3].reference = ""`,
		"remove-reference-null":  `patch.operations[3].reference = null`,
		"remove-unknown-null":    `patch.operations[3].value = null`,
		"set-auth-literal":       `patch.operations[0] = {slot:"ANTHROPIC_API_KEY",action:"set-literal",literal:"private-patch-sentinel"}`,
		"set-model-secret":       `patch.operations[3] = {slot:"ANTHROPIC_MODEL",action:"set-selected-secret",reference:"claude.api-key"}`,
		"mutated-input":          `input.models.default = "private-patch-sentinel"; patch.operations[3] = {slot:"ANTHROPIC_MODEL",action:"set-literal",literal:input.models.default}`,
	} {
		test.Run(name, func(test *testing.T) {
			registry := patchRegistry(test, nativePatchModule(test, mutation))
			request := providerPatchRequest(test, registry)
			before, _ := json.Marshal(request)
			assertProviderPatchError(test, registry, test.Context(), request, agentadapter.ErrPluginResult)
			after, _ := json.Marshal(request)
			if string(before) != string(after) {
				test.Fatal("module mutated caller input")
			}
		})
	}
}

func TestProviderPatchLiteralAndSecretUnionsUseActualMemberPresence(test *testing.T) {
	for name, mutation := range map[string]string{
		"literal-changed":               `patch.operations[3].literal = "private-patch-sentinel"`,
		"literal-missing":               `delete patch.operations[3].literal`,
		"literal-null":                  `patch.operations[3].literal = null`,
		"literal-number":                `patch.operations[3].literal = 123`,
		"literal-extra-reference-null":  `patch.operations[3].reference = null`,
		"literal-extra-reference-empty": `patch.operations[3].reference = ""`,
		"reference-changed":             `patch.operations[0].reference = "claude.auth-token"`,
		"reference-other-asset":         `patch.operations[0].reference = "other-asset/claude.api-key"`,
		"reference-missing":             `delete patch.operations[0].reference`,
		"reference-null":                `patch.operations[0].reference = null`,
		"reference-extra-literal-null":  `patch.operations[0].literal = null`,
		"reference-extra-literal-empty": `patch.operations[0].literal = ""`,
	} {
		test.Run(name, func(test *testing.T) {
			module := nativePatchModule(test, `patch.operations[0] = {slot:"ANTHROPIC_API_KEY",action:"set-selected-secret",reference:"claude.api-key"}; patch.operations[3] = {slot:"ANTHROPIC_MODEL",action:"set-literal",literal:input.models.default}; `+mutation)
			registry := patchRegistry(test, module)
			request := providerPatchRequest(test, registry)
			request.Desired.AuthMode, request.Desired.APIKeyPresent = agentconfig.ClaudeAPIKeyAuth, true
			request.Desired.Models.Default = new("chosen-model")
			assertProviderPatchError(test, registry, test.Context(), request, agentadapter.ErrPluginResult)
		})
	}
}

func TestProviderPatchRuntimeIsolationBoundsAndCancellation(test *testing.T) {
	module := `let calls = 0; ` + nativePatchModule(test, `if (++calls !== 1 || Object.keys(input).sort().join(",") !== "apiKeyPresent,authMode,authTokenPresent,models" || Object.keys(input.models).length !== 0 || typeof fetch !== "undefined" || typeof process !== "undefined") throw Error("private-patch-sentinel")`)
	registry := patchRegistry(test, module)
	request := providerPatchRequest(test, registry)
	var workers sync.WaitGroup
	results := make(chan error, 16)
	for range 16 {
		workers.Go(func() {
			patch, err := registry.PlanClaudeProvider(test.Context(), request)
			if err == nil && !reflect.DeepEqual(patch.Operations, removedProviderOperations()) {
				err = errors.New("unexpected patch")
			}
			results <- err
		})
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			test.Fatal("isolated runtime input or instance isolation failed")
		}
	}
	for _, source := range []string{
		`export function planClaudeProvider() { throw Error("private-patch-sentinel") }`,
		`export function planClaudeProvider() { return "x".repeat(2000000) }`,
		`export function planClaudeProvider() { return new Promise(() => {}) }`,
		`import x from "node:fs"; export function planClaudeProvider() { return x }`,
		`export function render() { throw Error("must not fall back") }`,
	} {
		broken := patchRegistry(test, source)
		assertProviderPatchError(test, broken, test.Context(), providerPatchRequest(test, broken), agentadapter.ErrPluginExecution)
	}
	loop := patchRegistry(test, `export function planClaudeProvider() { for (;;) {} }`)
	ctx, cancel := context.WithTimeout(test.Context(), 40*time.Millisecond)
	defer cancel()
	assertProviderPatchError(test, loop, ctx, providerPatchRequest(test, loop), context.DeadlineExceeded)
	canceled, cancelNow := context.WithCancel(test.Context())
	cancelNow()
	assertProviderPatchError(test, registry, canceled, request, context.Canceled)
}

func TestProviderPatchResultsDoNotAliasRequestsOrOtherCalls(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	request := providerPatchRequest(test, registry)
	request.Desired.AuthMode, request.Desired.APIKeyPresent = agentconfig.ClaudeAPIKeyAuth, true
	request.Desired.Models.Default = new("selected")
	before, _ := json.Marshal(request)
	first, err := registry.PlanClaudeProvider(test.Context(), request)
	if err != nil {
		test.Fatal(err)
	}
	after, _ := json.Marshal(request)
	if string(before) != string(after) {
		test.Fatal("provider planner mutated request")
	}
	*first.Operations[3].Literal = "caller-change"
	*first.Operations[0].Reference = "caller-change"
	first.Operations[6].Slot = "caller-change"
	second, err := registry.PlanClaudeProvider(test.Context(), request)
	if err != nil || *request.Desired.Models.Default != "selected" || *second.Operations[3].Literal != "selected" || *second.Operations[0].Reference != agentconfig.ClaudeAPIKeySecret || second.Operations[6].Slot != agentconfig.ClaudeEnvOpusModel {
		test.Fatal("caller could mutate a request or subsequent patch through prior output")
	}
	*request.Desired.Models.Default = "new-request"
	if *second.Operations[3].Literal != "selected" {
		test.Fatal("result aliases caller's desired state")
	}
}
