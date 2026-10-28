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

func Builtins() (*Registry, error) {
	var manifests []agentconfig.Manifest
	if json.Unmarshal(builtinManifests, &manifests, json.RejectUnknownMembers(true)) != nil {
		return nil, ErrInvalidDefinition
	}
	definitions := make([]Definition, 0, len(manifests))
	for _, manifest := range manifests {
		definition := Definition{Manifest: manifest}
		if manifest.Target == agentconfig.ClaudeSettings {
			definition.Module = claudeModule
		}
		definitions = append(definitions, definition)
	}
	return NewRegistry(definitions)
}
