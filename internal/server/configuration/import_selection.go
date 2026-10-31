package configuration

import (
	"context"
	"reflect"

	"github.com/ca-x/cognate/internal/server/state"
)

func PrepareImportedDraft(ctx context.Context, input Draft) (state.ConfigurationAssetDraft, error) {
	if err := ctx.Err(); err != nil {
		return state.ConfigurationAssetDraft{}, err
	}
	normalized, err := normalizeDraft(input)
	if err != nil || !reflect.DeepEqual(normalized, input) {
		return state.ConfigurationAssetDraft{}, ErrInvalid
	}
	result := state.ConfigurationAssetDraft{Kind: state.ConfigurationKindProvider, Name: normalized.Name, SchemaVersion: 1}
	switch {
	case normalized.GrokBuildProvider != nil:
		result.AgentID = "grokbuild"
		result.Payload, err = encodeGrokBuildEnvelope(*normalized.GrokBuildProvider, Secret{})
	case normalized.OpenCodeProvider != nil:
		return state.ConfigurationAssetDraft{}, ErrUnsupported
	case normalized.GeminiProvider != nil:
		result.AgentID = "gemini"
		result.Payload, err = encodeGeminiEnvelope(*normalized.GeminiProvider, Secret{})
	case normalized.CodexProvider != nil:
		result.AgentID = "codex"
		result.Payload, err = encodeCodexEnvelope(*normalized.CodexProvider, Secret{})
	default:
		result.AgentID = "claude"
		result.Payload, err = encodeEnvelope(normalized.Provider, credentials{})
	}
	if err != nil {
		return state.ConfigurationAssetDraft{}, err
	}
	if err := ctx.Err(); err != nil {
		return state.ConfigurationAssetDraft{}, err
	}
	return result, nil
}
