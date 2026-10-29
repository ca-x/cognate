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

func (registry *Registry) PlanGeminiProvider(ctx context.Context, request agentconfig.GeminiProviderPlanRequest) (agentconfig.GeminiProviderDeclarationV1, error) {
	if ctx == nil {
		return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.GeminiProviderDeclarationV1{}, err
	}
	entry, err := registry.geminiPlanDefinition(request)
	if err != nil {
		return agentconfig.GeminiProviderDeclarationV1{}, err
	}
	if err := validateGeminiDesired(request.Desired); err != nil {
		return agentconfig.GeminiProviderDeclarationV1{}, err
	}
	if _, err := codexJSONWithin(request, agentconfig.MaxGeminiRequestBytes); err != nil {
		return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidRequest
	}
	desired := cloneGeminiDesired(request.Desired)
	expected := expectedGeminiDeclaration(desired)
	input, err := codexJSONWithin(desired, agentconfig.MaxGeminiDesiredBytes)
	if err != nil {
		return agentconfig.GeminiProviderDeclarationV1{}, ErrInvalidRequest
	}
	var raw any
	if err := plugin.RunJSON(ctx, entry.definition.Module, "planGeminiProvider", input, &raw); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return agentconfig.GeminiProviderDeclarationV1{}, err
		}
		return agentconfig.GeminiProviderDeclarationV1{}, ErrPluginExecution
	}
	output, ok := raw.(map[string]any)
	if !ok {
		return agentconfig.GeminiProviderDeclarationV1{}, ErrPluginResult
	}
	return finalizeGeminiDeclaration(ctx, output, expected)
}

func (registry *Registry) geminiPlanDefinition(request agentconfig.GeminiProviderPlanRequest) (registeredDefinition, error) {
	if registry == nil {
		return registeredDefinition{}, ErrUnsupported
	}
	entry, exists := registry.definitions[request.PluginID]
	if !exists {
		return registeredDefinition{}, ErrUnsupported
	}
	manifest := entry.definition.Manifest
	if request.APIVersion != agentconfig.APIVersion || request.PluginVersion != manifest.PluginVersion || request.DefinitionDigest != entry.digest || request.Target != manifest.Target {
		return registeredDefinition{}, ErrIncompatible
	}
	if manifest.AgentID != "gemini" || manifest.Target != agentconfig.GeminiSettings || manifest.Format != agentconfig.JSON || manifest.PluginVersion != agentconfig.GeminiPlannerPluginVersion || len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != agentconfig.GeminiProviderPlanV1 || entry.definition.Module == "" {
		return registeredDefinition{}, ErrUnsupported
	}
	if len(request.Capabilities) != 1 || request.Capabilities[0] != agentconfig.GeminiProviderPlanV1 {
		return registeredDefinition{}, ErrIncompatible
	}
	return entry, nil
}

func validateGeminiDesired(desired agentconfig.GeminiProviderDesired) error {
	switch desired.AuthMode {
	case agentconfig.GeminiNativeLogin:
		if desired.KeyPresent || desired.Endpoint != nil || desired.KeyTransport != nil {
			return ErrInvalidRequest
		}
	case agentconfig.GeminiAPIKeyAuth:
		if !desired.KeyPresent || desired.KeyTransport == nil || *desired.KeyTransport != agentconfig.GeminiNativeDefault && *desired.KeyTransport != agentconfig.GeminiBearer {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	if desired.Endpoint != nil {
		value := *desired.Endpoint
		if !codexLiteral(value, 2048) || strings.ContainsAny(value, "?#\\") {
			return ErrInvalidRequest
		}
		endpoint, err := url.Parse(value)
		if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl) || !utf8.ValidString(endpoint.Path) {
			return ErrInvalidRequest
		}
		if endpoint.Port() != "" {
			port, err := strconv.Atoi(endpoint.Port())
			if err != nil || port < 1 || port > 65535 {
				return ErrInvalidRequest
			}
		}
	}
	for _, model := range []*string{desired.EnvModel, desired.SettingsModel} {
		if model == nil {
			continue
		}
		if len(*model) == 0 || len(*model) > 256 {
			return ErrInvalidRequest
		}
		for index, character := range []byte(*model) {
			alphanumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
			if !alphanumeric && (index == 0 || !strings.ContainsRune("._:/-", rune(character))) {
				return ErrInvalidRequest
			}
		}
	}
	_, err := codexJSONWithin(desired, agentconfig.MaxGeminiDesiredBytes)
	return err
}

func cloneGeminiDesired(desired agentconfig.GeminiProviderDesired) agentconfig.GeminiProviderDesired {
	desired.Endpoint = cloneCodexOptional(desired.Endpoint)
	desired.KeyTransport = cloneCodexOptional(desired.KeyTransport)
	desired.EnvModel = cloneCodexOptional(desired.EnvModel)
	desired.SettingsModel = cloneCodexOptional(desired.SettingsModel)
	return desired
}

func expectedGeminiDeclaration(desired agentconfig.GeminiProviderDesired) agentconfig.GeminiProviderDeclarationV1 {
	desired = cloneGeminiDesired(desired)
	result := agentconfig.GeminiProviderDeclarationV1{Version: 1, Target: agentconfig.GeminiSettings, AuthMode: desired.AuthMode, Endpoint: desired.Endpoint, KeyTransport: desired.KeyTransport, EnvModel: desired.EnvModel, SettingsModel: desired.SettingsModel, SelectedType: "oauth-personal", RequiredSlots: []agentconfig.GeminiSecretSlot{}, LocalAuthPolicy: agentconfig.GeminiPreserveLocalLoginV1}
	if desired.AuthMode == agentconfig.GeminiAPIKeyAuth {
		result.SelectedType = "gemini-api-key"
		result.CredentialReference = new(agentconfig.GeminiAPIKeySecret)
		result.RequiredSlots = []agentconfig.GeminiSecretSlot{agentconfig.GeminiAPIKeySecret}
	}
	return result
}

func finalizeGeminiDeclaration(ctx context.Context, output map[string]any, expected agentconfig.GeminiProviderDeclarationV1) (agentconfig.GeminiProviderDeclarationV1, error) {
	encoded, err := codexJSONWithin(output, agentconfig.MaxGeminiDeclarationBytes)
	if err != nil {
		return agentconfig.GeminiProviderDeclarationV1{}, ErrPluginResult
	}
	want, err := codexJSONWithin(expected, agentconfig.MaxGeminiDeclarationBytes)
	var members map[string]any
	if err != nil || json.Unmarshal(want, &members) != nil {
		return agentconfig.GeminiProviderDeclarationV1{}, ErrPluginResult
	}
	want, err = codexJSONWithin(members, agentconfig.MaxGeminiDeclarationBytes)
	if err != nil || !bytes.Equal(encoded, want) {
		return agentconfig.GeminiProviderDeclarationV1{}, ErrPluginResult
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.GeminiProviderDeclarationV1{}, err
	}
	return expectedGeminiDeclaration(agentconfig.GeminiProviderDesired{AuthMode: expected.AuthMode, Endpoint: expected.Endpoint, KeyTransport: expected.KeyTransport, EnvModel: expected.EnvModel, SettingsModel: expected.SettingsModel}), nil
}
