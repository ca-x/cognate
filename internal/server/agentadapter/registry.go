package agentadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"regexp"
	"slices"
	"strings"

	"github.com/ca-x/cognate/internal/agentconfig"
)

type Definition struct {
	Manifest agentconfig.Manifest `json:"manifest"`
	Module   string               `json:"module"`
}

type registeredDefinition struct {
	definition Definition
	digest     string
}

type Registry struct {
	definitions map[string]registeredDefinition
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
var version = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func ParseDefinition(data []byte) (Definition, error) {
	var definition Definition
	if len(data) == 0 || len(data) > agentconfig.MaxDefinitionBytes || json.Unmarshal(data, &definition, json.RejectUnknownMembers(true)) != nil || !validDefinition(definition) {
		return Definition{}, ErrInvalidDefinition
	}
	return definition, nil
}

func NewRegistry(definitions []Definition) (*Registry, error) {
	if len(definitions) > agentconfig.MaxDefinitions {
		return nil, ErrInvalidDefinition
	}
	registry := &Registry{definitions: make(map[string]registeredDefinition, len(definitions))}
	for _, definition := range definitions {
		if !validDefinition(definition) {
			return nil, ErrInvalidDefinition
		}
		if _, exists := registry.definitions[definition.Manifest.PluginID]; exists {
			return nil, ErrInvalidDefinition
		}
		definition.Manifest.Capabilities = slices.Clone(definition.Manifest.Capabilities)
		encoded, err := json.Marshal(definition, json.Deterministic(true))
		if err != nil {
			return nil, ErrInvalidDefinition
		}
		digest := sha256.Sum256(encoded)
		registry.definitions[definition.Manifest.PluginID] = registeredDefinition{definition: definition, digest: hex.EncodeToString(digest[:])}
	}
	return registry, nil
}

func (registry *Registry) List() []agentconfig.Descriptor {
	descriptors := make([]agentconfig.Descriptor, 0, len(registry.definitions))
	for _, entry := range registry.definitions {
		manifest := entry.definition.Manifest
		manifest.Capabilities = slices.Clone(manifest.Capabilities)
		descriptors = append(descriptors, agentconfig.Descriptor{Manifest: manifest, DefinitionDigest: entry.digest})
	}
	slices.SortFunc(descriptors, func(first, second agentconfig.Descriptor) int {
		return strings.Compare(first.Manifest.PluginID, second.Manifest.PluginID)
	})
	return descriptors
}

func validDefinition(definition Definition) bool {
	manifest := definition.Manifest
	if manifest.APIVersion != agentconfig.APIVersion || !identifier.MatchString(manifest.PluginID) || len(manifest.PluginVersion) > 32 || !version.MatchString(manifest.PluginVersion) || len(definition.Module) > agentconfig.MaxModuleBytes {
		return false
	}
	validTarget := manifest.AgentID == "claude" && manifest.Target == agentconfig.ClaudeSettings && manifest.Format == agentconfig.JSON || manifest.AgentID == "codex" && manifest.Target == agentconfig.CodexConfig && manifest.Format == agentconfig.TOML || manifest.AgentID == "gemini" && manifest.Target == agentconfig.GeminiSettings && manifest.Format == agentconfig.JSON
	if !validTarget {
		return false
	}
	if len(manifest.Capabilities) == 0 {
		return definition.Module == ""
	}
	return len(manifest.Capabilities) == 1 && manifest.Target == agentconfig.ClaudeSettings && manifest.Capabilities[0] == agentconfig.ClaudeProviderEnvV1 && len(definition.Module) != 0
}
