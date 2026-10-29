package agentadapter

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/plugin"
)

func geminiRequest(test *testing.T, registry *Registry, desired agentconfig.GeminiProviderDesired) agentconfig.GeminiProviderPlanRequest {
	test.Helper()
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.AgentID == "gemini" {
			return agentconfig.GeminiProviderPlanRequest{APIVersion: 1, PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: descriptor.Manifest.Target, Capabilities: []agentconfig.Capability{agentconfig.GeminiProviderPlanV1}, Desired: desired}
		}
	}
	test.Fatal("Gemini descriptor missing")
	return agentconfig.GeminiProviderPlanRequest{}
}

func TestGeminiProviderPlan(test *testing.T) {
	registry, err := Builtins()
	if err != nil {
		test.Fatal("builtin registration failed")
	}
	for _, mode := range []agentconfig.GeminiAuthMode{"native-login", "api-key"} {
		for mask := range 16 {
			desired := agentconfig.GeminiProviderDesired{AuthMode: mode, KeyPresent: mode == "api-key"}
			expected := agentconfig.GeminiProviderDeclarationV1{Version: 1, Target: "gemini.settings", AuthMode: mode, SelectedType: "oauth-personal", RequiredSlots: []agentconfig.GeminiSecretSlot{}, LocalAuthPolicy: "preserve-local-login-v1"}
			if mode == "api-key" {
				desired.KeyTransport = new(agentconfig.GeminiNativeDefault)
				if mask&1 != 0 {
					desired.KeyTransport = new(agentconfig.GeminiBearer)
				}
				if mask&2 != 0 {
					desired.Endpoint = new("https://Example.invalid:443/Case/%76")
					expected.Endpoint = new("https://Example.invalid:443/Case/%76")
				}
				expected.KeyTransport = new(*desired.KeyTransport)
				expected.SelectedType = "gemini-api-key"
				expected.CredentialReference = new(agentconfig.GeminiAPIKeySecret)
				expected.RequiredSlots = []agentconfig.GeminiSecretSlot{"gemini.api-key"}
			}
			if mask&4 != 0 {
				desired.EnvModel, expected.EnvModel = new("Env/Case:1"), new("Env/Case:1")
			}
			if mask&8 != 0 {
				desired.SettingsModel, expected.SettingsModel = new("Settings/Other:2"), new("Settings/Other:2")
			}
			actual, err := registry.PlanGeminiProvider(test.Context(), geminiRequest(test, registry, desired))
			if err != nil || !reflect.DeepEqual(actual, expected) {
				test.Fatal("Gemini complete declaration or independent presence mapping failed")
			}
			for _, pair := range [][2]*string{{desired.Endpoint, actual.Endpoint}, {desired.EnvModel, actual.EnvModel}, {desired.SettingsModel, actual.SettingsModel}} {
				if pair[0] != nil && pair[0] == pair[1] {
					test.Fatal("caller input aliases declaration")
				}
			}
			encoded, err := json.Marshal(actual)
			var document map[string]any
			if err != nil || json.Unmarshal(encoded, &document) != nil || document["version"] != float64(1) || document["requiredSlots"] == nil {
				test.Fatal("declaration version or nonnil empty slots serialization failed")
			}
		}
	}
}

func geminiDesired() agentconfig.GeminiProviderDesired {
	return agentconfig.GeminiProviderDesired{AuthMode: "api-key", Endpoint: new("https://Private.invalid:443/Case/%76"), KeyTransport: new(agentconfig.GeminiBearer), EnvModel: new("Env/Case:1"), SettingsModel: new("Settings/Other:2"), KeyPresent: true}
}

func geminiModuleRegistry(test *testing.T, module string) *Registry {
	test.Helper()
	registry, err := NewRegistry([]Definition{{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "builtin.gemini", PluginVersion: "1.1.0", AgentID: "gemini", Target: agentconfig.GeminiSettings, Format: agentconfig.JSON, Capabilities: []agentconfig.Capability{agentconfig.GeminiProviderPlanV1}}, Module: module}})
	if err != nil {
		test.Fatal("synthetic Gemini definition rejected")
	}
	return registry
}

func TestGeminiProviderDesiredBounds(test *testing.T) {
	registry := geminiModuleRegistry(test, geminiModule)
	for name, mutate := range map[string]func(*agentconfig.GeminiProviderDesired){
		"auth-empty":   func(desired *agentconfig.GeminiProviderDesired) { desired.AuthMode = "" },
		"auth-unknown": func(desired *agentconfig.GeminiProviderDesired) { desired.AuthMode = "vertex" },
		"auth-case":    func(desired *agentconfig.GeminiProviderDesired) { desired.AuthMode = "API-KEY" },
		"missing-key":  func(desired *agentconfig.GeminiProviderDesired) { desired.KeyPresent = false },
		"native-key": func(desired *agentconfig.GeminiProviderDesired) {
			desired.AuthMode = "native-login"
			desired.Endpoint = nil
			desired.KeyTransport = nil
		},
		"native-endpoint": func(desired *agentconfig.GeminiProviderDesired) {
			desired.AuthMode = "native-login"
			desired.KeyPresent = false
			desired.KeyTransport = nil
		},
		"native-transport": func(desired *agentconfig.GeminiProviderDesired) {
			desired.AuthMode = "native-login"
			desired.KeyPresent = false
			desired.Endpoint = nil
		},
		"transport-absent": func(desired *agentconfig.GeminiProviderDesired) { desired.KeyTransport = nil },
		"transport-empty": func(desired *agentconfig.GeminiProviderDesired) {
			desired.KeyTransport = new(agentconfig.GeminiKeyTransport(""))
		},
		"transport-case": func(desired *agentconfig.GeminiProviderDesired) {
			desired.KeyTransport = new(agentconfig.GeminiKeyTransport("Bearer"))
		},
		"transport-header": func(desired *agentconfig.GeminiProviderDesired) {
			desired.KeyTransport = new(agentconfig.GeminiKeyTransport("x-goog-api-key"))
		},
	} {
		test.Run(name, func(test *testing.T) {
			desired := geminiDesired()
			mutate(&desired)
			result, err := registry.PlanGeminiProvider(test.Context(), geminiRequest(test, registry, desired))
			if !errors.Is(err, ErrInvalidRequest) || !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) {
				test.Fatal("invalid desired mode/transport accepted or nonzero result")
			}
		})
	}
	for index, endpoint := range []string{"", " https://host.invalid", "https://host.invalid ", "http://localhost", "https:///path", "https://user@host.invalid", "https://host.invalid?", "https://host.invalid#", "https://host.invalid:", "https://host.invalid:0", "https://host.invalid:65536", "https://host.invalid/a\\b", "https://host.invalid/%5c", "https://host.invalid/%00", "https://host.invalid/%0a", "https://host.invalid/%7f", "https://host.invalid/%ff", "https://host.invalid/\xff", "https://host.invalid/\u0085", "https://host.invalid/" + strings.Repeat("x", 2048)} {
		test.Run(fmt.Sprintf("endpoint-%d", index), func(test *testing.T) {
			desired := geminiDesired()
			desired.Endpoint = new(endpoint)
			result, err := registry.PlanGeminiProvider(test.Context(), geminiRequest(test, registry, desired))
			if !errors.Is(err, ErrInvalidRequest) || !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) || err.Error() != ErrInvalidRequest.Error() {
				test.Fatal("invalid endpoint accepted, echoed, or nonzero result")
			}
		})
	}
	for index, model := range []string{"", " model", "model ", ".model", "model?", "model#", "model\\x", "模型", "model\x00", "model\xff", strings.Repeat("x", 257)} {
		for _, settings := range []bool{false, true} {
			test.Run(fmt.Sprintf("model-%d/settings-%t", index, settings), func(test *testing.T) {
				desired := geminiDesired()
				if settings {
					desired.SettingsModel = new(model)
				} else {
					desired.EnvModel = new(model)
				}
				result, err := registry.PlanGeminiProvider(test.Context(), geminiRequest(test, registry, desired))
				if !errors.Is(err, ErrInvalidRequest) || !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) {
					test.Fatal("invalid model accepted or nonzero result")
				}
			})
		}
	}
	for _, size := range []int{1, 256} {
		desired := geminiDesired()
		desired.Endpoint = new("https://host.invalid/" + strings.Repeat("x", 2048-len("https://host.invalid/")))
		desired.EnvModel = new(strings.Repeat("A", size))
		desired.SettingsModel = new(strings.Repeat("B", size))
		request := geminiRequest(test, registry, desired)
		result, err := registry.PlanGeminiProvider(test.Context(), request)
		encoded, marshalErr := json.Marshal(desired, json.Deterministic(true))
		requestBytes, requestErr := json.Marshal(request, json.Deterministic(true))
		if err != nil || marshalErr != nil || requestErr != nil || len(encoded) > agentconfig.MaxGeminiDesiredBytes || len(requestBytes) > agentconfig.MaxGeminiRequestBytes || *result.Endpoint != *desired.Endpoint || *result.EnvModel != *desired.EnvModel || *result.SettingsModel != *desired.SettingsModel {
			test.Fatal("maximum valid desired rejected, rewritten, or outside cumulative limit")
		}
	}
}

func TestGeminiProviderHostRejectsMaliciousModules(test *testing.T) {
	for name, body := range map[string]string{
		"version": `result.version = 2`, "version-type": `result.version = "1"`,
		"target": `result.target = "claude.settings"`, "mode": `result.authMode = "native-login"`,
		"selected": `result.selectedType = "oauth-personal"`, "endpoint": `result.endpoint = "https://other.invalid"`,
		"env-model": `result.envModel = "Other"`, "settings-model": `result.settingsModel = "Other"`,
		"missing-optional": `delete result.envModel`, "null-optional": `result.envModel = null`,
		"transport": `result.keyTransport = "native-default"`, "reference": `result.credentialReference = "claude.api-key"`,
		"reference-absent": `delete result.credentialReference`, "slots-empty": `result.requiredSlots = []`,
		"slots-extra": `result.requiredSlots.push("claude.api-key")`, "slots-duplicate": `result.requiredSlots.push("gemini.api-key")`,
		"slots-null": `result.requiredSlots = null`, "policy": `result.localAuthPolicy = "replace"`,
		"unknown": `result.unknown = null`, "path": `result.path = "/tmp/private"`,
		"headers": `result.headers = {"Authorization":"PRIVATE_CANARY"}`, "raw-env": `result.env = {"GEMINI_API_KEY":"PRIVATE_CANARY"}`,
		"raw-settings": `result.settings = {}`, "output-limit": `result.envModel = "x".repeat(16385)`,
		"array": `return []`, "null": `return null`, "string": `return "{}"`,
	} {
		test.Run(name, func(test *testing.T) {
			module := strings.Replace(geminiModule, "return result;", body+"; return result;", 1)
			registry := geminiModuleRegistry(test, module)
			result, err := registry.PlanGeminiProvider(test.Context(), geminiRequest(test, registry, geminiDesired()))
			if !errors.Is(err, ErrPluginResult) || !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) || err.Error() != ErrPluginResult.Error() {
				test.Fatal("malicious real module output accepted or unsanitized")
			}
		})
	}
	for index, source := range []string{
		`export function planGeminiProvider(desired) { throw new Error(desired.endpoint + "PRIVATE_CANARY"); }`,
		`export function planGeminiProvider() { return eval("({})"); }`,
		`export function planGeminiProvider() { return fetch("https://provider.invalid"); }`,
		`export function planGeminiProvider() { return {large: "x".repeat(1048577)}; }`,
		`export function wrongHook() { return {}; }`,
	} {
		test.Run(fmt.Sprintf("execution-%d", index), func(test *testing.T) {
			registry := geminiModuleRegistry(test, source)
			result, err := registry.PlanGeminiProvider(test.Context(), geminiRequest(test, registry, geminiDesired()))
			if !errors.Is(err, ErrPluginExecution) || !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) || err.Error() != ErrPluginExecution.Error() {
				test.Fatal("execution failure lost fixed classification or zero result")
			}
		})
	}
	for name, source := range map[string]string{
		"add-optional":    strings.Replace(geminiModule, "return result;", `result.endpoint = "https://other.invalid"; return result;`, 1),
		"native-null-ref": strings.Replace(geminiModule, "return result;", `result.credentialReference = null; return result;`, 1),
	} {
		test.Run(name, func(test *testing.T) {
			registry := geminiModuleRegistry(test, source)
			result, err := registry.PlanGeminiProvider(test.Context(), geminiRequest(test, registry, agentconfig.GeminiProviderDesired{AuthMode: "native-login"}))
			if !errors.Is(err, ErrPluginResult) || !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) {
				test.Fatal("module added absent native field")
			}
		})
	}
}

func TestGeminiProviderCancelLifecycle(test *testing.T) {
	registry := geminiModuleRegistry(test, `export function planGeminiProvider() { while (true) {} }`)
	request := geminiRequest(test, registry, geminiDesired())
	test.Run("pre-cancel", func(test *testing.T) {
		ctx, cancel := context.WithCancel(test.Context())
		cancel()
		result, err := registry.PlanGeminiProvider(ctx, request)
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) {
			test.Fatal("pre-cancel failed")
		}
	})
	test.Run("live-runtime-cancel", func(test *testing.T) {
		ctx, cancel := context.WithCancel(test.Context())
		finished := make(chan error, 1)
		var workers sync.WaitGroup
		workers.Go(func() {
			result, err := registry.PlanGeminiProvider(ctx, request)
			if !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) {
				err = errors.New("nonzero result")
			}
			finished <- err
		})
		defer workers.Wait()
		defer cancel()
		witness, stop := context.WithTimeout(test.Context(), 2*time.Second)
		defer stop()
		buffer := make([]byte, 256<<10)
		observed := false
		for !observed && witness.Err() == nil {
			for _, stack := range bytes.Split(buffer[:runtime.Stack(buffer, true)], []byte("\n\n")) {
				if bytes.Contains(stack, []byte("github.com/MoeForge/moejs.(*Runtime).Call")) && bytes.Contains(stack, []byte("PlanGeminiProvider")) {
					observed = true
				}
			}
			runtime.Gosched()
		}
		cancel()
		if !observed {
			test.Fatal("actual running interpreter not witnessed")
		}
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				test.Fatal("running interpreter cancellation classification failed")
			}
		case <-time.After(5 * time.Second):
			test.Fatal("interpreter failed to drain")
		}
	})
	test.Run("real-three-second-limit", func(test *testing.T) {
		started := time.Now()
		result, err := registry.PlanGeminiProvider(test.Context(), request)
		if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) || time.Since(started) < 3*time.Second {
			test.Fatal("real interpreter budget failed")
		}
	})
	test.Run("fresh-return-gate", func(test *testing.T) {
		ctx, cancel := context.WithCancel(test.Context())
		defer cancel()
		desired := geminiDesired()
		input, err := json.Marshal(desired)
		var output map[string]any
		if err != nil || plugin.RunJSON(ctx, geminiModule, "planGeminiProvider", input, &output) != nil {
			test.Fatal("real hook positive control failed")
		}
		cancel()
		result, err := finalizeGeminiDeclaration(ctx, output, expectedGeminiDeclaration(desired))
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) {
			test.Fatal("fresh return gate failed")
		}
	})
}

func TestGeminiProviderPrivacyAndConcurrentCopies(test *testing.T) {
	var captured bytes.Buffer
	previousOutput, previousFlags, previousPrefix, previousSlog := log.Writer(), log.Flags(), log.Prefix(), slog.Default()
	log.SetOutput(&captured)
	log.SetFlags(0)
	log.SetPrefix("")
	slog.SetDefault(slog.New(slog.NewTextHandler(&captured, nil)))
	test.Cleanup(func() {
		slog.SetDefault(previousSlog)
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})
	for _, source := range []string{geminiModule, `export function planGeminiProvider(desired) { throw new Error(desired.endpoint); }`} {
		registry := geminiModuleRegistry(test, source)
		_, err := registry.PlanGeminiProvider(test.Context(), geminiRequest(test, registry, geminiDesired()))
		if err != nil && err.Error() != ErrPluginExecution.Error() {
			test.Fatal("private provider entered error")
		}
	}
	if captured.Len() != 0 {
		test.Fatal("planner emitted private logs")
	}
	registry := geminiModuleRegistry(test, geminiModule)
	request := geminiRequest(test, registry, geminiDesired())
	results := make(chan agentconfig.GeminiProviderDeclarationV1, 8)
	failures := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			result, err := registry.PlanGeminiProvider(test.Context(), request)
			results <- result
			failures <- err
		})
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			test.Fatal("concurrent read-only planner failed")
		}
	}
	for result := range results {
		if *result.Endpoint != *request.Desired.Endpoint || *result.KeyTransport != *request.Desired.KeyTransport || result.CredentialReference == nil || result.Endpoint == request.Desired.Endpoint || result.KeyTransport == request.Desired.KeyTransport {
			test.Fatal("independent result missing or aliases input")
		}
		*result.Endpoint = "modified"
		*result.KeyTransport = "modified"
		*result.CredentialReference = "modified"
		result.RequiredSlots[0] = "modified"
	}
	result, err := registry.PlanGeminiProvider(test.Context(), request)
	if err != nil || *result.Endpoint != *request.Desired.Endpoint || *result.KeyTransport != "bearer" || *result.CredentialReference != "gemini.api-key" || result.RequiredSlots[0] != "gemini.api-key" {
		test.Fatal("result mutation contaminated caller or subsequent plan")
	}
}

func TestGeminiProviderInputShapeAndCapabilities(test *testing.T) {
	source := strings.Replace(geminiModule, "const apiKey", `if (Object.keys(desired).sort().join(",") !== "authMode,endpoint,envModel,keyPresent,keyTransport,settingsModel") throw new Error("unexpected input shape");
  if (typeof fetch !== "undefined" || typeof require !== "undefined" || typeof process !== "undefined" || typeof console !== "undefined") throw new Error("unexpected host capability");
  const apiKey`, 1)
	registry := geminiModuleRegistry(test, source)
	desired := geminiDesired()
	result, err := registry.PlanGeminiProvider(test.Context(), geminiRequest(test, registry, desired))
	if err != nil || *result.EnvModel != *desired.EnvModel {
		test.Fatal("secret-free desired-only interpreter positive control failed")
	}
	source = strings.Replace(geminiModule, "const apiKey", `desired.envModel = "mutated"; const apiKey`, 1)
	registry = geminiModuleRegistry(test, source)
	result, err = registry.PlanGeminiProvider(test.Context(), geminiRequest(test, registry, desired))
	if !errors.Is(err, ErrPluginResult) || *desired.EnvModel != "Env/Case:1" || !reflect.DeepEqual(result, agentconfig.GeminiProviderDeclarationV1{}) {
		test.Fatal("module changed desired or evaded independent expected")
	}
	encoded, err := json.Marshal(agentconfig.GeminiProviderDesired{AuthMode: "native-login"})
	var input map[string]any
	if err != nil || json.Unmarshal(encoded, &input) != nil || len(input) != 2 || input["keyPresent"] != false {
		test.Fatal("false key presence was omitted or default fields synthesized")
	}
}
