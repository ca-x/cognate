package agentadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/plugin"
)

func (registry *Registry) RenderJSON(ctx context.Context, data []byte) (agentconfig.RenderResult, error) {
	if err := ctx.Err(); err != nil {
		return agentconfig.RenderResult{}, err
	}
	var request agentconfig.RenderRequest
	if len(data) == 0 || len(data) > agentconfig.MaxRequestBytes || json.Unmarshal(data, &request, json.RejectUnknownMembers(true)) != nil {
		return agentconfig.RenderResult{}, ErrInvalidRequest
	}
	return registry.Render(ctx, request)
}

func (registry *Registry) Render(ctx context.Context, request agentconfig.RenderRequest) (agentconfig.RenderResult, error) {
	if err := ctx.Err(); err != nil {
		return agentconfig.RenderResult{}, err
	}
	entry, err := registry.renderDefinition(request)
	if err != nil {
		return agentconfig.RenderResult{}, err
	}
	settings, environment, err := readSettings(request)
	if err != nil {
		return agentconfig.RenderResult{}, err
	}
	input, err := json.Marshal(struct {
		ProviderEnv map[string]string `json:"providerEnv"`
	}{request.ProviderEnv})
	if err != nil || len(input) > agentconfig.MaxEnvironmentBytes {
		return agentconfig.RenderResult{}, ErrInvalidRequest
	}
	var output map[string]any
	if err := plugin.RunJSON(ctx, entry.definition.Module, "render", input, &output); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return agentconfig.RenderResult{}, err
		}
		return agentconfig.RenderResult{}, ErrPluginExecution
	}
	updates, err := validatedEnvironment(output, request.ProviderEnv)
	if err != nil {
		return agentconfig.RenderResult{}, err
	}
	maps.Copy(environment, updates)
	encodedEnv, err := json.Marshal(environment, json.Deterministic(true))
	if err != nil || len(encodedEnv) > agentconfig.MaxEnvironmentBytes {
		return agentconfig.RenderResult{}, ErrPluginResult
	}
	settings["env"] = encodedEnv
	content, err := json.Marshal(settings, json.Deterministic(true))
	if err != nil || len(content) > agentconfig.MaxConfigBytes {
		return agentconfig.RenderResult{}, ErrPluginResult
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.RenderResult{}, err
	}
	baseDigest, newDigest := sha256.Sum256(request.Base), sha256.Sum256(content)
	return agentconfig.RenderResult{Target: request.Target, BaseSHA256: hex.EncodeToString(baseDigest[:]), NewSHA256: hex.EncodeToString(newDigest[:]), Content: content}, nil
}

func (registry *Registry) renderDefinition(request agentconfig.RenderRequest) (registeredDefinition, error) {
	entry, exists := registry.definitions[request.PluginID]
	if !exists {
		return registeredDefinition{}, ErrUnsupported
	}
	manifest := entry.definition.Manifest
	if request.APIVersion != agentconfig.APIVersion || request.PluginVersion != manifest.PluginVersion || request.DefinitionDigest != entry.digest || request.Target != manifest.Target {
		return registeredDefinition{}, ErrIncompatible
	}
	if len(manifest.Capabilities) == 0 {
		return registeredDefinition{}, ErrUnsupported
	}
	if len(request.Capabilities) != 1 || request.Capabilities[0] != agentconfig.ClaudeProviderEnvV1 {
		return registeredDefinition{}, ErrIncompatible
	}
	return entry, nil
}

func readSettings(request agentconfig.RenderRequest) (map[string]jsontext.Value, map[string]string, error) {
	var settings map[string]jsontext.Value
	if len(request.Base) == 0 || len(request.Base) > agentconfig.MaxConfigBytes || json.Unmarshal(request.Base, &settings) != nil || settings == nil {
		return nil, nil, ErrInvalidRequest
	}
	finalFieldCount := len(settings)
	if _, exists := settings["env"]; !exists {
		finalFieldCount++
	}
	if finalFieldCount > 512 {
		return nil, nil, ErrInvalidRequest
	}
	environment := make(map[string]string)
	if encoded, exists := settings["env"]; exists {
		var values map[string]*string
		if len(encoded) > agentconfig.MaxEnvironmentBytes || json.Unmarshal(encoded, &values) != nil || values == nil {
			return nil, nil, ErrInvalidRequest
		}
		for key, value := range values {
			if value == nil {
				return nil, nil, ErrInvalidRequest
			}
			environment[key] = *value
		}
	}
	if len(environment) > agentconfig.MaxEnvironmentEntries || len(request.ProviderEnv) == 0 || len(request.ProviderEnv) > 4 {
		return nil, nil, ErrInvalidRequest
	}
	for key, value := range environment {
		if len(key) == 0 || len(key) > 128 || key == "__proto__" || key == "prototype" || key == "constructor" || len(value) > agentconfig.MaxEnvironmentValueBytes {
			return nil, nil, ErrInvalidRequest
		}
	}
	for key, value := range request.ProviderEnv {
		if !providerKey(key) || len(value) == 0 || len(value) > agentconfig.MaxEnvironmentValueBytes || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
			return nil, nil, ErrInvalidRequest
		}
	}
	entries := len(environment)
	for key := range request.ProviderEnv {
		if _, exists := environment[key]; !exists {
			entries++
		}
	}
	if entries > agentconfig.MaxEnvironmentEntries {
		return nil, nil, ErrInvalidRequest
	}
	return settings, environment, nil
}

func providerKey(key string) bool {
	switch key {
	case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL":
		return true
	default:
		return false
	}
}

func validatedEnvironment(output map[string]any, updates map[string]string) (map[string]string, error) {
	if len(output) != 2 || output["target"] != string(agentconfig.ClaudeSettings) {
		return nil, ErrPluginResult
	}
	values, ok := output["env"].(map[string]any)
	if !ok {
		return nil, ErrPluginResult
	}
	if len(values) != len(updates) {
		return nil, ErrPluginResult
	}
	merged := make(map[string]string, len(values))
	for key, expectedValue := range updates {
		value, ok := values[key].(string)
		if !ok || value != expectedValue {
			return nil, ErrPluginResult
		}
		merged[key] = value
	}
	return merged, nil
}
