package agentadapter_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

func claudeRequest(test *testing.T, registry *agentadapter.Registry) agentconfig.RenderRequest {
	test.Helper()
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.Target == agentconfig.ClaudeSettings {
			return agentconfig.RenderRequest{APIVersion: 1, PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: agentconfig.ClaudeSettings, Capabilities: []agentconfig.Capability{agentconfig.ClaudeProviderEnvV1}, Base: []byte(`{}`), ProviderEnv: map[string]string{"ANTHROPIC_MODEL": "new"}}
		}
	}
	test.Fatal("Claude adapter is missing")
	return agentconfig.RenderRequest{}
}

func TestClaudeRejectsMalformedBaseWithoutLeakingSecrets(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	for _, base := range []string{
		``, `null`, `[]`, `{"secret-sentinel":`, `{"env":null}`, `{"env":[]}`,
		`{"env":{"KEEP":null}}`, `{"env":{"KEEP":42}}`, `{"env":{"KEEP":{}}}`,
		`{"env":{"KEEP":"first","KEEP":"second"}}`, `{"env":{},"env":{}}`,
		`{"unrelated":{"same":1,"same":2}}`, `{"env":{"__proto__":"secret-sentinel"}}`,
		`{"value":"` + strings.Repeat("x", agentconfig.MaxConfigBytes) + `"}`,
	} {
		request := claudeRequest(test, registry)
		request.Base = []byte(base)
		request.ProviderEnv["ANTHROPIC_API_KEY"] = "secret-sentinel"
		result, err := registry.Render(test.Context(), request)
		if !errors.Is(err, agentadapter.ErrInvalidRequest) || strings.Contains(err.Error(), "secret-sentinel") || len(result.Content) != 0 {
			test.Fatalf("malformed base was not safely rejected (case bytes=%d): %v", len(base), err)
		}
	}
}

func TestClaudeRenderMergesExplicitProviderEnvironment(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	request := claudeRequest(test, registry)
	request.Base = []byte(`{"permissions":{"allow":["Read"]},"env":{"KEEP":"yes","ANTHROPIC_MODEL":"old"},"counter":9007199254740993}`)
	request.ProviderEnv["ANTHROPIC_API_KEY"] = "sentinel-provider-secret"
	original := bytes.Clone(request.Base)
	result, err := registry.Render(test.Context(), request)
	if err != nil {
		test.Fatal(err)
	}
	expected := `{"counter":9007199254740993,"env":{"ANTHROPIC_API_KEY":"sentinel-provider-secret","ANTHROPIC_MODEL":"new","KEEP":"yes"},"permissions":{"allow":["Read"]}}`
	if string(result.Content) != expected || result.Target != agentconfig.ClaudeSettings {
		test.Fatal("render changed unrelated settings or did not merge provider env")
	}
	baseDigest := sha256.Sum256(original)
	newDigest := sha256.Sum256([]byte(expected))
	if result.BaseSHA256 != hex.EncodeToString(baseDigest[:]) || result.NewSHA256 != hex.EncodeToString(newDigest[:]) {
		test.Fatal("render digests do not bind the exact base/new bytes")
	}
	if !bytes.Equal(request.Base, original) || request.ProviderEnv["ANTHROPIC_MODEL"] != "new" {
		test.Fatal("render mutated caller input")
	}
}

func TestClaudeTopLevelFieldLimitRoundTrip(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	for _, fieldCount := range []int{511, 512, 513} {
		for _, hasEnvironment := range []bool{false, true} {
			test.Run(fmt.Sprintf("fields=%d/env=%t", fieldCount, hasEnvironment), func(test *testing.T) {
				settings := make(map[string]jsontext.Value)
				if hasEnvironment {
					settings["env"] = jsontext.Value(`{"KEEP":"unchanged"}`)
				}
				for fieldIndex := range fieldCount - len(settings) {
					settings[fmt.Sprintf("unrelated-%03d", fieldIndex)] = jsontext.Value(`{"counter":9007199254740993,"keep":true}`)
				}
				request := claudeRequest(test, registry)
				request.Base, err = json.Marshal(settings, json.Deterministic(true))
				if err != nil {
					test.Fatal(err)
				}
				original := bytes.Clone(request.Base)
				result, err := registry.Render(test.Context(), request)
				if !bytes.Equal(request.Base, original) {
					test.Fatal("render mutated the base settings")
				}
				if fieldCount == 513 || (fieldCount == 512 && !hasEnvironment) {
					if !errors.Is(err, agentadapter.ErrInvalidRequest) || len(result.Content) != 0 {
						test.Fatalf("settings exceeding the final field limit were not rejected: %v", err)
					}
					return
				}
				if err != nil {
					test.Fatal(err)
				}
				var rendered map[string]jsontext.Value
				if err := json.Unmarshal(result.Content, &rendered); err != nil {
					test.Fatal(err)
				}
				expectedCount := fieldCount
				if !hasEnvironment {
					expectedCount++
				}
				if len(rendered) != expectedCount {
					test.Fatalf("rendered field count = %d, want %d", len(rendered), expectedCount)
				}
				for key, value := range settings {
					if key != "env" && !bytes.Equal(rendered[key], value) {
						test.Fatalf("unrelated setting %q was removed or changed", key)
					}
				}
				var environment map[string]string
				if err := json.Unmarshal(rendered["env"], &environment); err != nil {
					test.Fatal(err)
				}
				if environment["ANTHROPIC_MODEL"] != "new" || (hasEnvironment && environment["KEEP"] != "unchanged") {
					test.Fatal("provider update or unrelated environment field was lost")
				}
				request.Base = result.Content
				roundTrip, err := registry.Render(test.Context(), request)
				if err != nil {
					test.Fatalf("renderer rejected its own output: %v", err)
				}
				if !bytes.Equal(roundTrip.Content, result.Content) || roundTrip.BaseSHA256 != result.NewSHA256 || roundTrip.NewSHA256 != result.NewSHA256 {
					test.Fatal("round-trip changed settings or content digests")
				}
			})
		}
	}
}
