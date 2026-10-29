package agentadapter

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/plugin"
)

func (registry *Registry) PlanCodexProvider(ctx context.Context, request agentconfig.CodexProviderPlanRequest) (agentconfig.CodexProviderDeclarationV1, error) {
	if err := ctx.Err(); err != nil {
		return agentconfig.CodexProviderDeclarationV1{}, err
	}
	entry, err := registry.codexPlanDefinition(request)
	if err != nil {
		return agentconfig.CodexProviderDeclarationV1{}, err
	}
	if err := validateCodexDesired(request.Desired); err != nil {
		return agentconfig.CodexProviderDeclarationV1{}, err
	}
	if _, err := codexJSONWithin(request, agentconfig.MaxCodexRequestBytes); err != nil {
		return agentconfig.CodexProviderDeclarationV1{}, ErrInvalidRequest
	}
	desired := cloneCodexDesired(request.Desired)
	expected := expectedCodexDeclaration(desired)
	input, err := codexJSONWithin(desired, agentconfig.MaxCodexDesiredBytes)
	if err != nil {
		return agentconfig.CodexProviderDeclarationV1{}, ErrInvalidRequest
	}
	var raw any
	if err := plugin.RunJSON(ctx, entry.definition.Module, "planCodexProvider", input, &raw); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return agentconfig.CodexProviderDeclarationV1{}, err
		}
		return agentconfig.CodexProviderDeclarationV1{}, ErrPluginExecution
	}
	output, ok := raw.(map[string]any)
	if !ok {
		return agentconfig.CodexProviderDeclarationV1{}, ErrPluginResult
	}
	return finalizeCodexDeclaration(ctx, output, expected)
}

func (registry *Registry) codexPlanDefinition(request agentconfig.CodexProviderPlanRequest) (registeredDefinition, error) {
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
	if manifest.AgentID != "codex" || manifest.Target != agentconfig.CodexConfig || manifest.Format != agentconfig.TOML || len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != agentconfig.CodexProviderPlanV1 || entry.definition.Module == "" {
		return registeredDefinition{}, ErrUnsupported
	}
	if len(request.Capabilities) != 1 || request.Capabilities[0] != agentconfig.CodexProviderPlanV1 {
		return registeredDefinition{}, ErrIncompatible
	}
	return entry, nil
}

func validateCodexDesired(desired agentconfig.CodexProviderDesired) error {
	if desired.AuthMode != agentconfig.CodexProviderKeyAuth || !desired.KeyPresent || !codexLiteral(desired.Endpoint, 2048) || strings.ContainsAny(desired.Endpoint, "?#\\") || !codexModelID(desired.Model) || desired.ReasoningEffort != nil && !codexEffort(*desired.ReasoningEffort) {
		return ErrInvalidRequest
	}
	endpoint, err := url.Parse(desired.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.ContainsAny(endpoint.Path, "\\") || strings.ContainsFunc(endpoint.Path, unicode.IsControl) || !utf8.ValidString(endpoint.Path) {
		return ErrInvalidRequest
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return ErrInvalidRequest
		}
	}
	if desired.Catalog != nil {
		models := desired.Catalog.Models
		if len(models) < 1 || len(models) > 32 {
			return ErrInvalidRequest
		}
		seen := make(map[string]bool, len(models))
		for _, model := range models {
			if !codexModelID(model.Model) || seen[model.Model] || !validCodexCatalogModel(model) {
				return ErrInvalidRequest
			}
			seen[model.Model] = true
			if model.Model == desired.Model && desired.ReasoningEffort != nil && model.ReasoningLevels != nil && !slices.Contains(*model.ReasoningLevels, *desired.ReasoningEffort) {
				return ErrInvalidRequest
			}
		}
		if !seen[desired.Model] {
			return ErrInvalidRequest
		}
		if _, err := codexJSONWithin(desired.Catalog, agentconfig.MaxCodexCatalogBytes); err != nil {
			return ErrInvalidRequest
		}
	}
	_, err = codexJSONWithin(desired, agentconfig.MaxCodexDesiredBytes)
	return err
}

func codexLiteral(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

func codexModelID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, character := range []byte(value) {
		alphanumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
		if !alphanumeric && (index == 0 || !strings.ContainsRune("._:/-", rune(character))) {
			return false
		}
	}
	return true
}

func codexEffort(value agentconfig.CodexReasoningEffort) bool {
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func validCodexCatalogModel(model agentconfig.CodexCatalogModelDesired) bool {
	if model.DisplayName != nil && !codexLiteral(*model.DisplayName, 256) || model.ContextWindow != nil && (*model.ContextWindow == 0 || *model.ContextWindow > 2147483647) {
		return false
	}
	if model.BaseInstructions != nil {
		body := *model.BaseInstructions
		if len(body) == 0 || len(body) > 2048 || !utf8.ValidString(body) || strings.ContainsFunc(body, func(character rune) bool {
			return unicode.IsControl(character) && character != '\r' && character != '\n' && character != '\t'
		}) {
			return false
		}
	}
	if model.InputModalities != nil {
		modalities := *model.InputModalities
		if len(modalities) < 1 || len(modalities) > 2 {
			return false
		}
		for index, modality := range modalities {
			if modality != "text" && modality != "image" || slices.Contains(modalities[:index], modality) {
				return false
			}
		}
	}
	if model.ReasoningLevels != nil {
		levels := *model.ReasoningLevels
		if len(levels) < 1 || len(levels) > 7 {
			return false
		}
		for index, level := range levels {
			if !codexEffort(level) || slices.Contains(levels[:index], level) {
				return false
			}
		}
	}
	if model.DefaultReasoningLevel != nil && (model.ReasoningLevels == nil || !slices.Contains(*model.ReasoningLevels, *model.DefaultReasoningLevel)) {
		return false
	}
	return true
}

func codexJSONWithin(value any, limit int) ([]byte, error) {
	encoded, err := json.Marshal(value, json.Deterministic(true))
	if err != nil || len(encoded) > limit {
		return nil, ErrInvalidRequest
	}
	return encoded, nil
}

func cloneCodexOptional[Value any](value *Value) *Value {
	if value == nil {
		return nil
	}
	return new(*value)
}

func cloneCodexDesired(desired agentconfig.CodexProviderDesired) agentconfig.CodexProviderDesired {
	desired.ReasoningEffort = cloneCodexOptional(desired.ReasoningEffort)
	if desired.Catalog == nil {
		return desired
	}
	desired.Catalog = &agentconfig.CodexCatalogDesired{Models: slices.Clone(desired.Catalog.Models)}
	for index := range desired.Catalog.Models {
		model := &desired.Catalog.Models[index]
		model.DisplayName = cloneCodexOptional(model.DisplayName)
		model.ContextWindow = cloneCodexOptional(model.ContextWindow)
		model.SupportsParallelToolCalls = cloneCodexOptional(model.SupportsParallelToolCalls)
		model.BaseInstructions = cloneCodexOptional(model.BaseInstructions)
		model.DefaultReasoningLevel = cloneCodexOptional(model.DefaultReasoningLevel)
		if model.InputModalities != nil {
			model.InputModalities = new(slices.Clone(*model.InputModalities))
		}
		if model.ReasoningLevels != nil {
			model.ReasoningLevels = new(slices.Clone(*model.ReasoningLevels))
		}
	}
	return desired
}

func cloneCodexDeclaration(declaration agentconfig.CodexProviderDeclarationV1) agentconfig.CodexProviderDeclarationV1 {
	desired := cloneCodexDesired(agentconfig.CodexProviderDesired{ReasoningEffort: declaration.ReasoningEffort, Catalog: declaration.Catalog})
	declaration.ReasoningEffort, declaration.Catalog = desired.ReasoningEffort, desired.Catalog
	declaration.RequiredSlots = slices.Clone(declaration.RequiredSlots)
	return declaration
}

func expectedCodexDeclaration(desired agentconfig.CodexProviderDesired) agentconfig.CodexProviderDeclarationV1 {
	desired = cloneCodexDesired(desired)
	return agentconfig.CodexProviderDeclarationV1{APIVersion: agentconfig.APIVersion, Target: agentconfig.CodexConfig, ProviderID: agentconfig.CodexProviderID, WireAPI: agentconfig.CodexWireAPI, Endpoint: desired.Endpoint, Model: desired.Model, ReasoningEffort: desired.ReasoningEffort, Catalog: desired.Catalog, CredentialReference: agentconfig.CodexProviderKeySecret, RequiredSlots: []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, LocalAuthPolicy: agentconfig.CodexPreserveLocalLoginV1}
}

func validatedCodexDeclaration(output map[string]any, expected agentconfig.CodexProviderDeclarationV1) (agentconfig.CodexProviderDeclarationV1, error) {
	encoded, err := codexJSONWithin(output, agentconfig.MaxCodexDeclarationBytes)
	if err != nil {
		return agentconfig.CodexProviderDeclarationV1{}, ErrPluginResult
	}
	want, err := codexJSONWithin(expected, agentconfig.MaxCodexDeclarationBytes)
	var members map[string]any
	if err != nil || json.Unmarshal(want, &members) != nil {
		return agentconfig.CodexProviderDeclarationV1{}, ErrPluginResult
	}
	want, err = codexJSONWithin(members, agentconfig.MaxCodexDeclarationBytes)
	if err != nil || !bytes.Equal(encoded, want) {
		return agentconfig.CodexProviderDeclarationV1{}, ErrPluginResult
	}
	return cloneCodexDeclaration(expected), nil
}

func finalizeCodexDeclaration(ctx context.Context, output map[string]any, expected agentconfig.CodexProviderDeclarationV1) (agentconfig.CodexProviderDeclarationV1, error) {
	result, err := validatedCodexDeclaration(output, expected)
	if err != nil {
		return agentconfig.CodexProviderDeclarationV1{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.CodexProviderDeclarationV1{}, err
	}
	return result, nil
}
