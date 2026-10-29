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

func codexDesired() agentconfig.CodexProviderDesired {
	return agentconfig.CodexProviderDesired{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://provider.example/v1", Model: "model-main", KeyPresent: true}
}

func codexModuleRegistry(test *testing.T, module string) *Registry {
	test.Helper()
	registry, err := NewRegistry([]Definition{{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "builtin.codex", PluginVersion: "1.1.0", AgentID: "codex", Target: agentconfig.CodexConfig, Format: agentconfig.TOML, Capabilities: []agentconfig.Capability{agentconfig.CodexProviderPlanV1}}, Module: module}})
	if err != nil {
		test.Fatal("synthetic module registration failed")
	}
	return registry
}

func codexMutatedModule(body string) string {
	return strings.Replace(codexModule, "export function planCodexProvider", "function baselineCodexProvider", 1) + "\nexport function planCodexProvider(desired) { const result = baselineCodexProvider(desired); " + body + "; return result; }"
}

func TestCodexProviderMaliciousModules(test *testing.T) {
	for name, body := range map[string]string{
		"target":         `result.target = "claude.settings"`,
		"provider":       `result.providerID = "custom"`,
		"wire":           `result.wireAPI = "chat"`,
		"path":           `result.path = "../auth.json"`,
		"secret":         `result.credentialReference = "synthetic-private-replacement"`,
		"literal-key":    `result.key = "synthetic-private-replacement"`,
		"required-slot":  `result.requiredSlots = ["claude.api-key"]`,
		"duplicate-slot": `result.requiredSlots.push("codex.provider-key")`,
		"missing-slot":   `delete result.requiredSlots`,
		"model":          `result.model = "different"`,
		"policy":         `result.localAuthPolicy = "resolved"`,
		"resolved":       `result.requiresOpenAIAuth = false`,
		"unknown-null":   `result.unknown = null`,
		"unknown-empty":  `result.unknown = {}`,
		"hidden-absent":  `delete result.catalog.models[0].supportsParallelToolCalls`,
		"hidden-null":    `result.catalog.models[0].supportsParallelToolCalls = null`,
		"hidden-false":   `result.catalog.models[0].supportsParallelToolCalls = true`,
		"model-order":    `result.catalog.models.reverse()`,
		"level-order":    `result.catalog.models[0].reasoningLevels.reverse()`,
		"default":        `result.catalog.models[0].defaultReasoningLevel = "none"`,
		"float":          `result.catalog.models[0].contextWindow = 1.5`,
		"number-string":  `result.catalog.models[0].contextWindow = "128000"`,
		"nested-extra":   `result.catalog.models[0].unknown = null`,
		"oversize":       `result.endpoint = "a".repeat(65537)`,
	} {
		test.Run(name, func(test *testing.T) {
			registry := codexModuleRegistry(test, codexMutatedModule(body))
			result, err := registry.PlanCodexProvider(test.Context(), codexRequest(test, registry, codexCatalogDesired()))
			if !errors.Is(err, ErrPluginResult) || !reflect.DeepEqual(result, agentconfig.CodexProviderDeclarationV1{}) {
				test.Fatal("malicious module did not fail closed")
			}
		})
	}
}

func TestCodexProviderPluginBoundary(test *testing.T) {
	witness := `const keys = Object.keys(desired).sort().join(","); if (keys !== "authMode,catalog,endpoint,keyPresent,model,reasoningEffort" || desired.keyPresent !== true || typeof fetch !== "undefined" || typeof require !== "undefined" || typeof process !== "undefined") throw new Error("input boundary");`
	registry := codexModuleRegistry(test, codexMutatedModule(witness))
	if _, err := registry.PlanCodexProvider(test.Context(), codexRequest(test, registry, codexCatalogDesired())); err != nil {
		test.Fatal("actual hook input or host capabilities violated boundary")
	}
	for name, body := range map[string]string{"null": "null", "array": "[]", "string": `"private"`, "bool": "false"} {
		test.Run(name, func(test *testing.T) {
			registry := codexModuleRegistry(test, "export function planCodexProvider(desired) { return "+body+"; }")
			result, err := registry.PlanCodexProvider(test.Context(), codexRequest(test, registry, codexDesired()))
			if !errors.Is(err, ErrPluginResult) || result.APIVersion != 0 {
				test.Fatal("non-object module result classification changed")
			}
		})
	}
	for _, source := range []string{`export function otherHook() { return {}; }`, `export function planCodexProvider() { throw new Error("synthetic-private-replacement"); }`, `invalid module syntax`} {
		registry := codexModuleRegistry(test, source)
		result, err := registry.PlanCodexProvider(test.Context(), codexRequest(test, registry, codexDesired()))
		if !errors.Is(err, ErrPluginExecution) || result.APIVersion != 0 || strings.Contains(err.Error(), "synthetic-private") {
			test.Fatal("plugin execution classification or privacy violated")
		}
	}
}

func TestCodexProviderCancelLifecycle(test *testing.T) {
	loop := `export function planCodexProvider() { while (true) {} }`
	registry := codexModuleRegistry(test, loop)
	request := codexRequest(test, registry, codexDesired())
	test.Run("pre-canceled", func(test *testing.T) {
		ctx, cancel := context.WithCancel(test.Context())
		cancel()
		result, err := registry.PlanCodexProvider(ctx, request)
		if !errors.Is(err, context.Canceled) || result.APIVersion != 0 {
			test.Fatal("pre-cancel violated")
		}
	})
	test.Run("live-runtime-cancel", func(test *testing.T) {
		ctx, cancel := context.WithCancel(test.Context())
		defer cancel()
		finished := make(chan error, 1)
		var workers sync.WaitGroup
		workers.Go(func() {
			result, err := registry.PlanCodexProvider(ctx, request)
			if result.APIVersion != 0 {
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
			stacks := buffer[:runtime.Stack(buffer, true)]
			for _, stack := range bytes.Split(stacks, []byte("\n\n")) {
				if bytes.Contains(stack, []byte("github.com/MoeForge/moejs.(*Runtime).Call")) && bytes.Contains(stack, []byte("PlanCodexProvider")) {
					observed = true
				}
			}
			runtime.Gosched()
		}
		cancel()
		if !observed {
			test.Fatal("live runtime call was not witnessed")
		}
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				test.Fatal("runtime cancellation changed")
			}
		case <-time.After(5 * time.Second):
			test.Fatal("runtime failed to drain")
		}
	})
	test.Run("real-three-second-limit", func(test *testing.T) {
		started := time.Now()
		result, err := registry.PlanCodexProvider(test.Context(), request)
		if !errors.Is(err, context.DeadlineExceeded) || result.APIVersion != 0 || time.Since(started) < 3*time.Second {
			test.Fatal("actual runtime budget not enforced")
		}
	})
	test.Run("fresh-gate", func(test *testing.T) {
		ctx, cancel := context.WithCancel(test.Context())
		defer cancel()
		input, err := json.Marshal(codexCatalogDesired())
		var output map[string]any
		if err != nil || plugin.RunJSON(ctx, codexModule, "planCodexProvider", input, &output) != nil {
			test.Fatal("actual hook failed")
		}
		cancel()
		result, err := finalizeCodexDeclaration(ctx, output, expectedCodexDeclaration(codexCatalogDesired()))
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, agentconfig.CodexProviderDeclarationV1{}) {
			test.Fatal("post-result cancellation returned declaration")
		}
	})
}

func TestCodexProviderPrivacy(test *testing.T) {
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
	for _, source := range []string{codexModule, codexMutatedModule(`result.key = "synthetic-private-replacement"`), `export function planCodexProvider(desired) { throw new Error(desired.endpoint + desired.catalog.models[0].baseInstructions); }`} {
		registry := codexModuleRegistry(test, source)
		desired := codexCatalogDesired()
		desired.Endpoint = "https://synthetic-private-endpoint.example/v1"
		desired.Catalog.Models[0].BaseInstructions = new("synthetic-private-body")
		_, err := registry.PlanCodexProvider(test.Context(), codexRequest(test, registry, desired))
		if err != nil && strings.Contains(fmt.Sprint(err), "synthetic-private") {
			test.Fatal("error echoed private configuration")
		}
	}
	if captured.Len() != 0 {
		test.Fatal("configuration path added logs")
	}
}

func TestCodexProviderConcurrentReadOnly(test *testing.T) {
	registry, err := Builtins()
	if err != nil {
		test.Fatal("builtin registration failed")
	}
	request := codexRequest(test, registry, codexCatalogDesired())
	results := make(chan agentconfig.CodexProviderDeclarationV1, 8)
	failures := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			result, err := registry.PlanCodexProvider(test.Context(), request)
			results <- result
			failures <- err
		})
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			test.Fatal("read-only concurrent request failed")
		}
	}
	for result := range results {
		if *result.Catalog.Models[0].DisplayName != "Main" {
			test.Fatal("shared mutable output")
		}
		*result.Catalog.Models[0].DisplayName = "changed"
	}
}

func TestCodexProviderOwnership(test *testing.T) {
	declaration := expectedCodexDeclaration(codexDesired())
	if declaration.ProviderID != "cognate" || declaration.WireAPI != "responses" || declaration.Target != "codex.config" || declaration.LocalAuthPolicy != "preserve-local-login-v1" {
		test.Fatal("fixed ownership or policy changed")
	}
	encoded, err := json.Marshal(declaration)
	if err != nil {
		test.Fatal("ownership encoding failed")
	}
	for _, forbidden := range []string{"auth.json", "openai_base_url", "model_providers.custom", "requires_openai_auth", "review_model", "agents", "memories", "permissions", "operations", "path", "installed"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			test.Fatal("declaration exceeded owned fields")
		}
	}
}

func TestCodexProviderUnresolvedPolicy(test *testing.T) {
	registry, err := Builtins()
	if err != nil {
		test.Fatal("builtin registration failed")
	}
	for _, catalog := range []bool{false, true} {
		desired := codexDesired()
		if catalog {
			desired = codexCatalogDesired()
		}
		request := codexRequest(test, registry, desired)
		result, err := registry.PlanCodexProvider(test.Context(), request)
		if err != nil || result.LocalAuthPolicy != "preserve-local-login-v1" {
			test.Fatal("policy was resolved or omitted")
		}
		request.Desired.KeyPresent = false
		result, err = registry.PlanCodexProvider(test.Context(), request)
		if !errors.Is(err, ErrInvalidRequest) || !reflect.DeepEqual(result, agentconfig.CodexProviderDeclarationV1{}) {
			test.Fatal("missing key fell back to login")
		}
	}
}

func TestCodexProviderValidExtrema(test *testing.T) {
	desired := codexCatalogDesired()
	desired.Endpoint = "https://provider.example/" + strings.Repeat("a", 2048-len("https://provider.example/"))
	desired.Model = "M" + strings.Repeat("a", 127)
	desired.Catalog.Models[0].Model = desired.Model
	desired.Catalog.Models[0].DisplayName = new(strings.Repeat("a", 256))
	desired.Catalog.Models[0].ContextWindow = new(uint64(2147483647))
	desired.Catalog.Models[0].BaseInstructions = new(strings.Repeat("a", 2048))
	desired.Catalog.Models[0].ReasoningLevels = new([]agentconfig.CodexReasoningEffort{"none", "minimal", "low", "medium", "high", "xhigh", "max"})
	if err := validateCodexDesired(desired); err != nil {
		test.Fatal("exact upper bound rejected")
	}
	desired = codexDesired()
	desired.Model = "A"
	desired.Endpoint = "https://x"
	desired.ReasoningEffort = new(agentconfig.CodexReasoningEffort("none"))
	desired.Catalog = &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "A", DisplayName: new("A"), ContextWindow: new(uint64(1)), BaseInstructions: new("\n"), InputModalities: new([]agentconfig.CodexModality{"text"}), ReasoningLevels: new([]agentconfig.CodexReasoningEffort{"none"}), DefaultReasoningLevel: new(agentconfig.CodexReasoningEffort("none"))}}}
	if err := validateCodexDesired(desired); err != nil {
		test.Fatal("lower bound rejected")
	}
	desired.Catalog.Models[0].BaseInstructions = new(strings.Repeat("界", 683))
	if !errors.Is(validateCodexDesired(desired), ErrInvalidRequest) {
		test.Fatal("UTF8 bytes counted as runes")
	}
	desired.Catalog.Models[0].BaseInstructions = new(strings.Repeat("界", 682))
	if validateCodexDesired(desired) != nil {
		test.Fatal("valid UTF8 bytes rejected")
	}
}

func codexCatalogAtSize(test *testing.T, size int) agentconfig.CodexProviderDesired {
	test.Helper()
	desired := codexDesired()
	desired.Catalog = new(agentconfig.CodexCatalogDesired{})
	for index := range 32 {
		model := fmt.Sprintf("model-%02d", index)
		if index == 0 {
			model = desired.Model
		}
		desired.Catalog.Models = append(desired.Catalog.Models, agentconfig.CodexCatalogModelDesired{Model: model, BaseInstructions: new("a")})
	}
	encoded, err := json.Marshal(desired.Catalog, json.Deterministic(true))
	if err != nil {
		test.Fatal("catalog fixture failed")
	}
	remaining := size - len(encoded)
	for index := range desired.Catalog.Models {
		addition := min(remaining, 2047)
		if addition < 0 {
			test.Fatal("catalog size fixture too small")
		}
		desired.Catalog.Models[index].BaseInstructions = new(strings.Repeat("a", 1+addition))
		remaining -= addition
	}
	encoded, err = json.Marshal(desired.Catalog, json.Deterministic(true))
	if err != nil || remaining != 0 || len(encoded) != size {
		test.Fatal("exact accumulated boundary was not constructed")
	}
	return desired
}

func TestCodexProviderAccumulatedBounds(test *testing.T) {
	for _, size := range []int{(32 << 10) - 1, 32 << 10, (32 << 10) + 1} {
		desired := codexCatalogAtSize(test, size)
		err := validateCodexDesired(desired)
		if size <= 32<<10 && err != nil || size > 32<<10 && !errors.Is(err, ErrInvalidRequest) {
			test.Fatal("catalog cumulative guard failed")
		}
	}
	for _, limit := range []int{agentconfig.MaxCodexDesiredBytes, agentconfig.MaxCodexRequestBytes, agentconfig.MaxCodexDeclarationBytes} {
		if encoded, err := codexJSONWithin(strings.Repeat("a", limit-2), limit); err != nil || len(encoded) != limit {
			test.Fatal("encoded guard rejected exact bound")
		}
		if _, err := codexJSONWithin(strings.Repeat("a", limit-1), limit); !errors.Is(err, ErrInvalidRequest) {
			test.Fatal("encoded guard accepted bound plus one")
		}
	}
}

func TestCodexProviderCatalogPresenceMatrix(test *testing.T) {
	registry, err := Builtins()
	if err != nil {
		test.Fatal("builtin registration failed")
	}
	for mask := range 128 {
		desired := codexCatalogDesired()
		model := &desired.Catalog.Models[0]
		if mask&1 == 0 {
			model.DisplayName = nil
		}
		if mask&2 == 0 {
			model.ContextWindow = nil
		}
		if mask&4 == 0 {
			model.SupportsParallelToolCalls = nil
		}
		if mask&8 == 0 {
			model.InputModalities = nil
		}
		if mask&16 == 0 {
			model.BaseInstructions = nil
		}
		if mask&32 == 0 {
			model.ReasoningLevels = nil
		}
		if mask&64 == 0 {
			model.DefaultReasoningLevel = nil
		}
		request := codexRequest(test, registry, desired)
		result, err := registry.PlanCodexProvider(test.Context(), request)
		if mask&64 != 0 && mask&32 == 0 {
			if !errors.Is(err, ErrInvalidRequest) || result.APIVersion != 0 {
				test.Fatal("default without levels accepted")
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(result.Catalog, desired.Catalog) {
			test.Fatal("optional metadata presence lost")
		}
	}
}

func TestCodexProviderDesiredBounds(test *testing.T) {
	bad := map[string]func(*agentconfig.CodexProviderDesired){
		"auth": func(desired *agentconfig.CodexProviderDesired) { desired.AuthMode = "native-login" },
		"http": func(desired *agentconfig.CodexProviderDesired) { desired.Endpoint = "http://127.0.0.1/v1" },
		"userinfo": func(desired *agentconfig.CodexProviderDesired) {
			desired.Endpoint = "https://user:private@provider.example"
		},
		"query":           func(desired *agentconfig.CodexProviderDesired) { desired.Endpoint += "?private=value" },
		"fragment":        func(desired *agentconfig.CodexProviderDesired) { desired.Endpoint += "#private" },
		"port":            func(desired *agentconfig.CodexProviderDesired) { desired.Endpoint = "https://provider.example:65536" },
		"empty-port":      func(desired *agentconfig.CodexProviderDesired) { desired.Endpoint = "https://provider.example:" },
		"slash":           func(desired *agentconfig.CodexProviderDesired) { desired.Endpoint += "\\private" },
		"encoded-control": func(desired *agentconfig.CodexProviderDesired) { desired.Endpoint += "/%0a" },
		"endpoint-size":   func(desired *agentconfig.CodexProviderDesired) { desired.Endpoint += strings.Repeat("a", 2049) },
		"model-empty":     func(desired *agentconfig.CodexProviderDesired) { desired.Model = "" },
		"model-size":      func(desired *agentconfig.CodexProviderDesired) { desired.Model = strings.Repeat("a", 129) },
		"model-space":     func(desired *agentconfig.CodexProviderDesired) { desired.Model = "model private" },
		"model-unicode":   func(desired *agentconfig.CodexProviderDesired) { desired.Model = "模型" },
		"effort": func(desired *agentconfig.CodexProviderDesired) {
			desired.ReasoningEffort = new(agentconfig.CodexReasoningEffort("future"))
		},
		"catalog-empty": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog = new(agentconfig.CodexCatalogDesired{})
		},
		"catalog-count": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models = make([]agentconfig.CodexCatalogModelDesired, 33)
		},
		"window-zero": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].ContextWindow = new(uint64(0))
		},
		"window-over": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].ContextWindow = new(uint64(2147483648))
		},
		"display-empty": func(desired *agentconfig.CodexProviderDesired) { desired.Catalog.Models[0].DisplayName = new("") },
		"display-size": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].DisplayName = new(strings.Repeat("a", 257))
		},
		"display-trim": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].DisplayName = new(" private")
		},
		"invalid-utf8": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].DisplayName = new(string([]byte{255}))
		},
		"body-empty": func(desired *agentconfig.CodexProviderDesired) { desired.Catalog.Models[0].BaseInstructions = new("") },
		"body-size": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].BaseInstructions = new(strings.Repeat("a", 2049))
		},
		"body-control": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].BaseInstructions = new("private\x00")
		},
		"modality-empty": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].InputModalities = new([]agentconfig.CodexModality{})
		},
		"modality-nil": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].InputModalities = new([]agentconfig.CodexModality(nil))
		},
		"modality-unknown": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].InputModalities = new([]agentconfig.CodexModality{"audio"})
		},
		"modality-duplicate": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].InputModalities = new([]agentconfig.CodexModality{"text", "text"})
		},
		"levels-empty": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].ReasoningLevels = new([]agentconfig.CodexReasoningEffort{})
		},
		"levels-nil": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].ReasoningLevels = new([]agentconfig.CodexReasoningEffort(nil))
		},
		"levels-duplicate": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].ReasoningLevels = new([]agentconfig.CodexReasoningEffort{"high", "high"})
		},
		"levels-unknown": func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].ReasoningLevels = new([]agentconfig.CodexReasoningEffort{"future"})
		},
	}
	for name, mutate := range bad {
		test.Run(name, func(test *testing.T) {
			desired := codexCatalogDesired()
			mutate(&desired)
			if !errors.Is(validateCodexDesired(desired), ErrInvalidRequest) {
				test.Fatal("invalid desired accepted")
			}
		})
	}
	for _, desired := range []agentconfig.CodexProviderDesired{codexDesired(), codexCatalogDesired()} {
		if validateCodexDesired(desired) != nil {
			test.Fatal("valid desired rejected")
		}
	}
}

func TestCodexProviderReferences(test *testing.T) {
	for _, mutate := range []func(*agentconfig.CodexProviderDesired){
		func(desired *agentconfig.CodexProviderDesired) { desired.Catalog.Models[1].Model = "model-main" },
		func(desired *agentconfig.CodexProviderDesired) { desired.Model = "missing" },
		func(desired *agentconfig.CodexProviderDesired) {
			desired.Catalog.Models[0].DefaultReasoningLevel = new(agentconfig.CodexReasoningEffort("low"))
		},
		func(desired *agentconfig.CodexProviderDesired) { desired.Catalog.Models[0].ReasoningLevels = nil },
		func(desired *agentconfig.CodexProviderDesired) {
			desired.ReasoningEffort = new(agentconfig.CodexReasoningEffort("low"))
		},
	} {
		desired := codexCatalogDesired()
		mutate(&desired)
		if !errors.Is(validateCodexDesired(desired), ErrInvalidRequest) {
			test.Fatal("invalid reference accepted")
		}
	}
}

func TestCodexProviderKeyReadiness(test *testing.T) {
	desired := codexDesired()
	desired.KeyPresent = false
	if !errors.Is(validateCodexDesired(desired), ErrInvalidRequest) {
		test.Fatal("missing copied or cleared key became ready")
	}
	declaration := expectedCodexDeclaration(codexDesired())
	if declaration.CredentialReference != "codex.provider-key" || !reflect.DeepEqual(declaration.RequiredSlots, []agentconfig.CodexSecretSlot{"codex.provider-key"}) {
		test.Fatal("slot contract changed")
	}
}

func codexOutput(test *testing.T, declaration agentconfig.CodexProviderDeclarationV1) map[string]any {
	test.Helper()
	encoded, err := json.Marshal(declaration)
	var output map[string]any
	if err != nil || json.Unmarshal(encoded, &output) != nil {
		test.Fatal("fixture encoding failed")
	}
	return output
}

func TestCodexProviderHostExactMembers(test *testing.T) {
	expected := expectedCodexDeclaration(codexCatalogDesired())
	for name, mutate := range map[string]func(map[string]any){
		"extra-null": func(output map[string]any) { output["extra"] = nil },
		"resolved":   func(output map[string]any) { output["requiresOpenAIAuth"] = false },
		"target":     func(output map[string]any) { output["target"] = "claude.settings" },
		"policy":     func(output map[string]any) { output["localAuthPolicy"] = "caller-policy" },
		"model":      func(output map[string]any) { output["model"] = "different" },
		"slot":       func(output map[string]any) { output["credentialReference"] = "claude.api-key" },
		"duplicate-slot": func(output map[string]any) {
			output["requiredSlots"] = []any{"codex.provider-key", "codex.provider-key"}
		},
		"hidden-absent": func(output map[string]any) {
			delete(output["catalog"].(map[string]any)["models"].([]any)[0].(map[string]any), "supportsParallelToolCalls")
		},
		"hidden-null": func(output map[string]any) {
			output["catalog"].(map[string]any)["models"].([]any)[0].(map[string]any)["supportsParallelToolCalls"] = nil
		},
		"nested-null": func(output map[string]any) {
			output["catalog"].(map[string]any)["models"].([]any)[1].(map[string]any)["unknown"] = nil
		},
		"model-order": func(output map[string]any) {
			models := output["catalog"].(map[string]any)["models"].([]any)
			models[0], models[1] = models[1], models[0]
		},
	} {
		test.Run(name, func(test *testing.T) {
			output := codexOutput(test, expected)
			mutate(output)
			result, err := validatedCodexDeclaration(output, expected)
			if !errors.Is(err, ErrPluginResult) || !reflect.DeepEqual(result, agentconfig.CodexProviderDeclarationV1{}) {
				test.Fatal("invalid module result accepted")
			}
		})
	}
}

func TestCodexProviderHostExpectedParity(test *testing.T) {
	expected := expectedCodexDeclaration(codexCatalogDesired())
	result, err := validatedCodexDeclaration(codexOutput(test, expected), expected)
	if err != nil || !reflect.DeepEqual(result, expected) {
		test.Fatal("valid host result rejected")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	result, err = finalizeCodexDeclaration(ctx, codexOutput(test, expected), expected)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, agentconfig.CodexProviderDeclarationV1{}) {
		test.Fatal("canceled finalizer returned a declaration")
	}
}

func codexCatalogDesired() agentconfig.CodexProviderDesired {
	desired := codexDesired()
	desired.ReasoningEffort = new(agentconfig.CodexReasoningEffort("high"))
	desired.Catalog = &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{
		{Model: "model-main", DisplayName: new("Main"), ContextWindow: new(uint64(128000)), SupportsParallelToolCalls: new(false), InputModalities: new([]agentconfig.CodexModality{"image", "text"}), BaseInstructions: new("Synthetic private instructions\r\n"), ReasoningLevels: new([]agentconfig.CodexReasoningEffort{"high", "none"}), DefaultReasoningLevel: new(agentconfig.CodexReasoningEffort("high"))},
		{Model: "model-other"},
	}}
	return desired
}

func TestCodexProviderPresenceEncoding(test *testing.T) {
	encoded, err := json.Marshal(codexCatalogDesired())
	if err != nil {
		test.Fatal("encode failed")
	}
	var decoded map[string]any
	if json.Unmarshal(encoded, &decoded) != nil {
		test.Fatal("decode failed")
	}
	models := decoded["catalog"].(map[string]any)["models"].([]any)
	first, second := models[0].(map[string]any), models[1].(map[string]any)
	if len(first) != 8 || len(second) != 1 || first["supportsParallelToolCalls"] != false || second["model"] != "model-other" || first["reasoningLevels"].([]any)[0] != "high" || first["inputModalities"].([]any)[0] != "image" {
		test.Fatal("presence or ordering lost")
	}
	encoded, err = json.Marshal(codexDesired())
	decoded = nil
	if err != nil || json.Unmarshal(encoded, &decoded) != nil {
		test.Fatal("absent encode failed")
	}
	if _, exists := decoded["catalog"]; exists {
		test.Fatal("absent catalog emitted")
	}
}

func TestCodexProviderNeutralSchema(test *testing.T) {
	request := reflect.TypeFor[agentconfig.CodexProviderPlanRequest]()
	if request.NumField() != 7 {
		test.Fatal("request schema expanded")
	}
	desired := reflect.TypeFor[agentconfig.CodexProviderDesired]()
	want := []string{"AuthMode", "Endpoint", "Model", "ReasoningEffort", "KeyPresent", "Catalog"}
	if desired.NumField() != len(want) {
		test.Fatal("desired schema expanded")
	}
	for index, name := range want {
		if desired.Field(index).Name != name {
			test.Fatal("desired has unauthorized member")
		}
	}
	declaration := reflect.TypeFor[agentconfig.CodexProviderDeclarationV1]()
	for _, forbidden := range []string{"Content", "Path", "Resolved", "Installed", "Key", "RequiresOpenAIAuth"} {
		if _, exists := declaration.FieldByName(forbidden); exists {
			test.Fatal("install or secret field admitted")
		}
	}
}

func codexRequest(test *testing.T, registry *Registry, desired agentconfig.CodexProviderDesired) agentconfig.CodexProviderPlanRequest {
	test.Helper()
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.AgentID == "codex" {
			manifest := descriptor.Manifest
			return agentconfig.CodexProviderPlanRequest{APIVersion: 1, PluginID: manifest.PluginID, PluginVersion: manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: manifest.Target, Capabilities: []agentconfig.Capability{agentconfig.CodexProviderPlanV1}, Desired: desired}
		}
	}
	test.Fatal("missing Codex descriptor")
	return agentconfig.CodexProviderPlanRequest{}
}

func TestCodexProviderRealMoejsParity(test *testing.T) {
	registry, err := Builtins()
	if err != nil {
		test.Fatal("builtin registration failed")
	}
	for _, desired := range []agentconfig.CodexProviderDesired{codexDesired(), codexCatalogDesired()} {
		result, err := registry.PlanCodexProvider(test.Context(), codexRequest(test, registry, desired))
		want := agentconfig.CodexProviderDeclarationV1{APIVersion: 1, Target: "codex.config", ProviderID: "cognate", WireAPI: "responses", Endpoint: "https://provider.example/v1", Model: "model-main", ReasoningEffort: desired.ReasoningEffort, Catalog: desired.Catalog, CredentialReference: "codex.provider-key", RequiredSlots: []agentconfig.CodexSecretSlot{"codex.provider-key"}, LocalAuthPolicy: "preserve-local-login-v1"}
		if err != nil || !reflect.DeepEqual(result, want) {
			test.Fatal("real module disagreed with literal contract")
		}
	}
}

func TestCodexProviderDeepClone(test *testing.T) {
	desired := codexCatalogDesired()
	registry, err := Builtins()
	if err != nil {
		test.Fatal("builtin registration failed")
	}
	request := codexRequest(test, registry, desired)
	first, err := registry.PlanCodexProvider(test.Context(), request)
	if err != nil {
		test.Fatal("initial plan failed")
	}
	*first.Catalog.Models[0].DisplayName = "changed"
	(*first.Catalog.Models[0].InputModalities)[0] = "text"
	*first.Catalog.Models[0].SupportsParallelToolCalls = true
	*first.ReasoningEffort = "none"
	first.RequiredSlots[0] = "foreign"
	second, err := registry.PlanCodexProvider(test.Context(), request)
	if err != nil || *second.Catalog.Models[0].DisplayName != "Main" || (*second.Catalog.Models[0].InputModalities)[0] != "image" || *second.Catalog.Models[0].SupportsParallelToolCalls || *second.ReasoningEffort != "high" || second.RequiredSlots[0] != "codex.provider-key" {
		test.Fatal("caller changed another plan or request")
	}
	*desired.Catalog.Models[0].ContextWindow = 1
	*desired.Catalog.Models[0].BaseInstructions = "changed"
	(*desired.Catalog.Models[0].ReasoningLevels)[0] = "low"
	*desired.Catalog.Models[0].DefaultReasoningLevel = "low"
	desired.Catalog.Models[1].Model = "changed"
	if *second.Catalog.Models[0].ContextWindow != 128000 || *second.Catalog.Models[0].BaseInstructions != "Synthetic private instructions\r\n" || (*second.Catalog.Models[0].ReasoningLevels)[0] != "high" || *second.Catalog.Models[0].DefaultReasoningLevel != "high" || second.Catalog.Models[1].Model != "model-other" {
		test.Fatal("request mutation changed published declaration")
	}
}
