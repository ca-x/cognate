package agentadapter

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/plugin"
)

func (registry *Registry) PlanGrokBuildProvider(ctx context.Context, request agentconfig.GrokBuildProviderPlanRequest) (agentconfig.GrokBuildProviderDeclarationV1, error) {
	if ctx == nil {
		return agentconfig.GrokBuildProviderDeclarationV1{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.GrokBuildProviderDeclarationV1{}, err
	}
	entry, err := registry.grokBuildDefinition(request)
	if err != nil {
		return agentconfig.GrokBuildProviderDeclarationV1{}, err
	}
	if err := validateGrokBuildDesired(request.Desired); err != nil {
		return agentconfig.GrokBuildProviderDeclarationV1{}, err
	}
	if _, err := codexJSONWithin(request, agentconfig.MaxGrokBuildRequestBytes); err != nil {
		return agentconfig.GrokBuildProviderDeclarationV1{}, ErrInvalidRequest
	}
	desired := cloneGrokBuildDesired(request.Desired)
	expected := grokBuildDeclaration(desired)
	input, err := codexJSONWithin(desired, agentconfig.MaxGrokBuildDesiredBytes)
	if err != nil {
		return agentconfig.GrokBuildProviderDeclarationV1{}, ErrInvalidRequest
	}
	var raw any
	if err := plugin.RunJSON(ctx, entry.definition.Module, "planGrokBuildProvider", input, &raw); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return agentconfig.GrokBuildProviderDeclarationV1{}, err
		}
		return agentconfig.GrokBuildProviderDeclarationV1{}, ErrPluginExecution
	}
	output, ok := raw.(map[string]any)
	if !ok {
		return agentconfig.GrokBuildProviderDeclarationV1{}, ErrPluginResult
	}
	encoded, err := codexJSONWithin(output, agentconfig.MaxGrokBuildDeclarationBytes)
	if err != nil {
		return agentconfig.GrokBuildProviderDeclarationV1{}, ErrPluginResult
	}
	want, err := codexJSONWithin(expected, agentconfig.MaxGrokBuildDeclarationBytes)
	var members map[string]any
	if err != nil || json.Unmarshal(want, &members) != nil {
		return agentconfig.GrokBuildProviderDeclarationV1{}, ErrPluginResult
	}
	want, err = codexJSONWithin(members, agentconfig.MaxGrokBuildDeclarationBytes)
	if err != nil || !bytes.Equal(encoded, want) {
		return agentconfig.GrokBuildProviderDeclarationV1{}, ErrPluginResult
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.GrokBuildProviderDeclarationV1{}, err
	}
	return expected, nil
}

func (registry *Registry) grokBuildDefinition(request agentconfig.GrokBuildProviderPlanRequest) (registeredDefinition, error) {
	if registry == nil {
		return registeredDefinition{}, ErrUnsupported
	}
	entry, exists := registry.definitions[request.PluginID]
	if !exists {
		return registeredDefinition{}, ErrUnsupported
	}
	manifest := entry.definition.Manifest
	if request.APIVersion != agentconfig.APIVersion || request.PluginVersion != manifest.PluginVersion || request.DefinitionDigest != entry.digest || request.Target != manifest.Target || len(request.Capabilities) != 1 || request.Capabilities[0] != agentconfig.GrokBuildProviderPlanV1 {
		return registeredDefinition{}, ErrIncompatible
	}
	if manifest.AgentID != "grokbuild" || manifest.Target != agentconfig.GrokBuildConfig || manifest.Format != agentconfig.TOML || manifest.PluginVersion != agentconfig.GrokBuildPlannerPluginVersion || len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != agentconfig.GrokBuildProviderPlanV1 || entry.definition.Module == "" {
		return registeredDefinition{}, ErrUnsupported
	}
	return entry, nil
}

func validateGrokBuildDesired(desired agentconfig.GrokBuildProviderDesired) error {
	if desired.Mode == agentconfig.GrokBuildNativeLogin {
		if desired.Model != nil {
			return ErrInvalidRequest
		}
		return nil
	}
	if desired.Mode != agentconfig.GrokBuildCustomModel || desired.Model == nil {
		return ErrInvalidRequest
	}
	model := desired.Model
	if !codexLiteral(model.Profile, 128) || !codexLiteral(model.UpstreamModel, 256) || !codexLiteral(model.Name, 128) || !codexLiteral(model.APIBackend, 64) || !grokBuildEndpoint(model.Endpoint) || model.ContextWindow < 1 || model.ContextWindow > agentconfig.MaxGrokBuildContextWindow {
		return ErrInvalidRequest
	}
	if model.EnvKey != nil && !grokBuildEnvKey(*model.EnvKey) || !model.KeyPresent && model.EnvKey == nil {
		return ErrInvalidRequest
	}
	if model.APIBackend != "responses" {
		return ErrUnsupported
	}
	_, err := codexJSONWithin(desired, agentconfig.MaxGrokBuildDesiredBytes)
	return err
}

func grokBuildEndpoint(value string) bool {
	if !codexLiteral(value, 2048) || strings.ContainsAny(value, "?#\\") {
		return false
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl) || !utf8.ValidString(endpoint.Path) {
		return false
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
}

func grokBuildEnvKey(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, character := range []byte(value) {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_'
		if !letter && (index == 0 || character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func cloneGrokBuildDesired(desired agentconfig.GrokBuildProviderDesired) agentconfig.GrokBuildProviderDesired {
	if desired.Model != nil {
		model := *desired.Model
		model.EnvKey = cloneCodexOptional(model.EnvKey)
		desired.Model = &model
	}
	return desired
}

func grokBuildDeclaration(desired agentconfig.GrokBuildProviderDesired) agentconfig.GrokBuildProviderDeclarationV1 {
	desired = cloneGrokBuildDesired(desired)
	result := agentconfig.GrokBuildProviderDeclarationV1{Version: 1, Target: agentconfig.GrokBuildConfig, Mode: desired.Mode, Model: desired.Model, RequiredSlots: []agentconfig.GrokBuildSecretSlot{}, LocalAuthPolicy: "preserve-local-login-v1"}
	if desired.Model != nil && desired.Model.KeyPresent {
		result.CredentialReference = new(agentconfig.GrokBuildAPIKeySecret)
		result.RequiredSlots = []agentconfig.GrokBuildSecretSlot{agentconfig.GrokBuildAPIKeySecret}
	}
	return result
}
