package agentadapter

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/plugin"
)

func (registry *Registry) PlanClaudeProvider(ctx context.Context, request agentconfig.ClaudeProviderPatchRequest) (agentconfig.ClaudeProviderPatchV1, error) {
	if err := ctx.Err(); err != nil {
		return agentconfig.ClaudeProviderPatchV1{}, err
	}
	entry, err := registry.providerPatchDefinition(request)
	if err != nil {
		return agentconfig.ClaudeProviderPatchV1{}, err
	}
	desired := cloneClaudeDesired(request.Desired)
	if !validClaudeDesired(desired) {
		return agentconfig.ClaudeProviderPatchV1{}, ErrInvalidRequest
	}
	expected := expectedClaudePatch(desired)
	input, err := json.Marshal(desired)
	if err != nil || len(input) > agentconfig.MaxEnvironmentBytes {
		return agentconfig.ClaudeProviderPatchV1{}, ErrInvalidRequest
	}
	var output map[string]any
	if err := plugin.RunJSON(ctx, entry.definition.Module, "planClaudeProvider", input, &output); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return agentconfig.ClaudeProviderPatchV1{}, err
		}
		return agentconfig.ClaudeProviderPatchV1{}, ErrPluginExecution
	}
	patch, err := validatedClaudePatch(output, expected)
	if err != nil {
		return agentconfig.ClaudeProviderPatchV1{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.ClaudeProviderPatchV1{}, err
	}
	return patch, nil
}

func (registry *Registry) providerPatchDefinition(request agentconfig.ClaudeProviderPatchRequest) (registeredDefinition, error) {
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
	if !slices.Contains(manifest.Capabilities, agentconfig.ClaudeProviderSwitchV1) {
		return registeredDefinition{}, ErrUnsupported
	}
	if len(request.Capabilities) != 1 || request.Capabilities[0] != agentconfig.ClaudeProviderSwitchV1 {
		return registeredDefinition{}, ErrIncompatible
	}
	return entry, nil
}

func cloneClaudeDesired(desired agentconfig.ClaudeProviderDesired) agentconfig.ClaudeProviderDesired {
	clone := func(value *string) *string {
		if value == nil {
			return nil
		}
		return new(*value)
	}
	desired.Endpoint = clone(desired.Endpoint)
	desired.Models = agentconfig.ClaudeModelsDesired{Default: clone(desired.Models.Default), Haiku: clone(desired.Models.Haiku), Sonnet: clone(desired.Models.Sonnet), Opus: clone(desired.Models.Opus)}
	return desired
}

func validClaudeDesired(desired agentconfig.ClaudeProviderDesired) bool {
	switch desired.AuthMode {
	case agentconfig.ClaudeNativeLogin:
		if desired.APIKeyPresent || desired.AuthTokenPresent || desired.Endpoint != nil {
			return false
		}
	case agentconfig.ClaudeAPIKeyAuth:
		if !desired.APIKeyPresent || desired.AuthTokenPresent {
			return false
		}
	case agentconfig.ClaudeAuthTokenAuth:
		if !desired.AuthTokenPresent || desired.APIKeyPresent {
			return false
		}
	default:
		return false
	}
	if desired.Endpoint != nil {
		value := *desired.Endpoint
		if !validProviderLiteral(value, 2048) || strings.ContainsAny(value, "?#") {
			return false
		}
		endpoint, err := url.Parse(value)
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.Hostname() == "" || endpoint.User != nil {
			return false
		}
	}
	for _, value := range []*string{desired.Models.Default, desired.Models.Haiku, desired.Models.Sonnet, desired.Models.Opus} {
		if value != nil && !validProviderLiteral(*value, 256) {
			return false
		}
	}
	return true
}

func validProviderLiteral(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func expectedClaudePatch(desired agentconfig.ClaudeProviderDesired) agentconfig.ClaudeProviderPatchV1 {
	operations := []agentconfig.ClaudeProviderPatchOperation{
		{Slot: agentconfig.ClaudeEnvAPIKey, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvAuthToken, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvBaseURL, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvModel, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvHaikuModel, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvSonnetModel, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvOpusModel, Action: agentconfig.ClaudeRemove},
	}
	if desired.AuthMode == agentconfig.ClaudeAPIKeyAuth {
		operations[0].Action, operations[0].Reference = agentconfig.ClaudeSetSelectedSecret, new(agentconfig.ClaudeAPIKeySecret)
	}
	if desired.AuthMode == agentconfig.ClaudeAuthTokenAuth {
		operations[1].Action, operations[1].Reference = agentconfig.ClaudeSetSelectedSecret, new(agentconfig.ClaudeAuthTokenSecret)
	}
	for index, value := range []*string{desired.Endpoint, desired.Models.Default, desired.Models.Haiku, desired.Models.Sonnet, desired.Models.Opus} {
		if value != nil {
			operations[index+2].Action, operations[index+2].Literal = agentconfig.ClaudeSetLiteral, new(*value)
		}
	}
	return agentconfig.ClaudeProviderPatchV1{Target: agentconfig.ClaudeSettings, Operations: operations}
}

func validatedClaudePatch(output map[string]any, expected agentconfig.ClaudeProviderPatchV1) (agentconfig.ClaudeProviderPatchV1, error) {
	if len(output) != 2 || output["target"] != string(agentconfig.ClaudeSettings) {
		return agentconfig.ClaudeProviderPatchV1{}, ErrPluginResult
	}
	operations, ok := output["operations"].([]any)
	if !ok || len(operations) != 7 {
		return agentconfig.ClaudeProviderPatchV1{}, ErrPluginResult
	}
	patch := agentconfig.ClaudeProviderPatchV1{Target: agentconfig.ClaudeSettings, Operations: make([]agentconfig.ClaudeProviderPatchOperation, 0, 7)}
	for index, value := range operations {
		operation, ok := value.(map[string]any)
		want := expected.Operations[index]
		if !ok || operation["slot"] != string(want.Slot) || operation["action"] != string(want.Action) {
			return agentconfig.ClaudeProviderPatchV1{}, ErrPluginResult
		}
		actual := agentconfig.ClaudeProviderPatchOperation{Slot: want.Slot, Action: want.Action}
		switch want.Action {
		case agentconfig.ClaudeSetLiteral:
			literal, ok := operation["literal"].(string)
			if len(operation) != 3 || !ok || literal != *want.Literal {
				return agentconfig.ClaudeProviderPatchV1{}, ErrPluginResult
			}
			actual.Literal = new(literal)
		case agentconfig.ClaudeSetSelectedSecret:
			reference, ok := operation["reference"].(string)
			if len(operation) != 3 || !ok || reference != string(*want.Reference) {
				return agentconfig.ClaudeProviderPatchV1{}, ErrPluginResult
			}
			actual.Reference = new(agentconfig.ClaudeSecretSlot(reference))
		case agentconfig.ClaudeRemove:
			if len(operation) != 2 {
				return agentconfig.ClaudeProviderPatchV1{}, ErrPluginResult
			}
		}
		patch.Operations = append(patch.Operations, actual)
	}
	return patch, nil
}
