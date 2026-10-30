package agentadapter

import (
	_ "embed"
	"encoding/json/v2"

	"github.com/ca-x/cognate/internal/agentconfig"
)

//go:embed builtins.json
var builtinManifests []byte

//go:embed claude.mjs
var claudeModule string

//go:embed codex.mjs
var codexModule string

//go:embed gemini.mjs
var geminiModule string

//go:embed opencode.mjs
var opencodeModule string

//go:embed mcp_claude.mjs
var mcpClaudeModule string

func Builtins() (*Registry, error) {
	var manifests []agentconfig.Manifest
	if json.Unmarshal(builtinManifests, &manifests, json.RejectUnknownMembers(true)) != nil {
		return nil, ErrInvalidDefinition
	}
	definitions := make([]Definition, 0, len(manifests))
	for _, manifest := range manifests {
		definition := Definition{Manifest: manifest}
		if manifest.Target == agentconfig.ClaudeMCP {
			definition.Module = mcpClaudeModule
		}
		if manifest.Target == agentconfig.ClaudeSettings {
			definition.Module = claudeModule
		}
		if manifest.Target == agentconfig.CodexConfig {
			definition.Module = codexModule
		}
		if manifest.Target == agentconfig.GeminiSettings {
			definition.Module = geminiModule
		}
		if manifest.Target == agentconfig.OpenCodeConfig {
			definition.Module = opencodeModule
		}
		definitions = append(definitions, definition)
	}
	return NewRegistry(definitions)
}
