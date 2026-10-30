package agentadapter

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"slices"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/plugin"
)

func (registry *Registry) PlanOpenCodeProvider(ctx context.Context, request agentconfig.OpenCodeProviderPlanRequest) (agentconfig.OpenCodeProviderDeclarationV1, error) {
	if ctx == nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, err
	}
	entry, err := registry.openCodeDefinition(request)
	if err != nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, err
	}
	provider, err := agentconfig.CloneOpenCodeProvider(request.Desired.Provider)
	if err != nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrInvalidRequest
	}
	required, err := agentconfig.OpenCodeRequiredSlots(provider)
	if err != nil || len(request.Desired.Slots) != len(required) {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrInvalidRequest
	}
	if _, err := codexJSONWithin(request.Desired, agentconfig.MaxOpenCodeDesiredBytes); err != nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrInvalidRequest
	}
	seen := make(map[string]bool, len(required))
	for _, status := range request.Desired.Slots {
		if seen[status.Slot] || !slices.Contains(required, status.Slot) {
			return agentconfig.OpenCodeProviderDeclarationV1{}, ErrInvalidRequest
		}
		seen[status.Slot] = true
	}
	if _, err := codexJSONWithin(request, agentconfig.MaxOpenCodeRequestBytes); err != nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrInvalidRequest
	}
	input, err := codexJSONWithin(struct {
		Format        string   `json:"format"`
		ProviderID    string   `json:"providerID"`
		RequiredSlots []string `json:"requiredSlots"`
	}{provider.Format, provider.ProviderID, required}, agentconfig.MaxOpenCodeDesiredBytes)
	if err != nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrInvalidRequest
	}
	expected := agentconfig.OpenCodePlanV1{Version: 1, Target: agentconfig.OpenCodeConfig, Mode: "coexist", Format: provider.Format, Container: "provider", ProviderID: provider.ProviderID, DefinitionRef: "host-validated-definition-v1", RequiredSlots: slices.Clone(required)}
	if provider.Format == "v2" {
		expected.Container = "providers"
	}
	var raw any
	if err := plugin.RunJSON(ctx, entry.definition.Module, "planOpenCodeProvider", input, &raw); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return agentconfig.OpenCodeProviderDeclarationV1{}, err
		}
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrPluginExecution
	}
	output, ok := raw.(map[string]any)
	if !ok {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrPluginResult
	}
	encoded, err := codexJSONWithin(output, agentconfig.MaxOpenCodeDeclarationBytes)
	if err != nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrPluginResult
	}
	want, err := codexJSONWithin(expected, agentconfig.MaxOpenCodeDeclarationBytes)
	var members map[string]any
	if err != nil || json.Unmarshal(want, &members) != nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrPluginResult
	}
	want, err = codexJSONWithin(members, agentconfig.MaxOpenCodeDeclarationBytes)
	if err != nil || !bytes.Equal(encoded, want) {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrPluginResult
	}
	result := agentconfig.OpenCodeProviderDeclarationV1{Plan: expected, Provider: provider}
	if _, err := codexJSONWithin(result, agentconfig.MaxOpenCodeDeclarationBytes); err != nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.OpenCodeProviderDeclarationV1{}, err
	}
	return result, nil
}

func (registry *Registry) openCodeDefinition(request agentconfig.OpenCodeProviderPlanRequest) (registeredDefinition, error) {
	if registry == nil {
		return registeredDefinition{}, ErrUnsupported
	}
	entry, exists := registry.definitions[request.PluginID]
	if !exists {
		return registeredDefinition{}, ErrUnsupported
	}
	manifest := entry.definition.Manifest
	if request.APIVersion != agentconfig.APIVersion || request.PluginVersion != manifest.PluginVersion || request.DefinitionDigest != entry.digest || request.Target != manifest.Target || len(request.Capabilities) != 1 || request.Capabilities[0] != agentconfig.OpenCodeProviderPlanV1 {
		return registeredDefinition{}, ErrIncompatible
	}
	if manifest.AgentID != "opencode" || manifest.Target != agentconfig.OpenCodeConfig || manifest.Format != agentconfig.JSON || manifest.PluginVersion != agentconfig.OpenCodePlannerPluginVersion || len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != agentconfig.OpenCodeProviderPlanV1 || entry.definition.Module == "" {
		return registeredDefinition{}, ErrUnsupported
	}
	return entry, nil
}
