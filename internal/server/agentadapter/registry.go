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
	if manifest.Target == agentconfig.ClaudeMCP {
		return manifest.APIVersion == agentconfig.APIVersion && manifest.PluginID == agentconfig.MCPPluginID && manifest.PluginVersion == agentconfig.MCPPluginVersion && manifest.AgentID == "claude" && manifest.Format == agentconfig.JSON && len(manifest.Capabilities) == 1 && manifest.Capabilities[0] == agentconfig.ClaudeMCPPlanV1 && len(definition.Module) > 0 && len(definition.Module) <= agentconfig.MaxModuleBytes
	}
	if manifest.APIVersion != agentconfig.APIVersion || !identifier.MatchString(manifest.PluginID) || len(manifest.PluginVersion) > 32 || !version.MatchString(manifest.PluginVersion) || len(definition.Module) > agentconfig.MaxModuleBytes {
		return false
	}
	validTarget := manifest.AgentID == "claude" && manifest.Target == agentconfig.ClaudeSettings && manifest.Format == agentconfig.JSON || manifest.AgentID == "codex" && manifest.Target == agentconfig.CodexConfig && manifest.Format == agentconfig.TOML || manifest.AgentID == "gemini" && manifest.Target == agentconfig.GeminiSettings && manifest.Format == agentconfig.JSON || manifest.AgentID == "opencode" && manifest.Target == agentconfig.OpenCodeConfig && manifest.Format == agentconfig.JSON
	validTarget = validTarget || manifest.AgentID == "grokbuild" && manifest.Target == agentconfig.GrokBuildConfig && manifest.Format == agentconfig.TOML
	if !validTarget {
		return false
	}
	if len(manifest.Capabilities) == 0 {
		return definition.Module == ""
	}
	if manifest.Target == agentconfig.GrokBuildConfig {
		return len(manifest.Capabilities) == 1 && manifest.Capabilities[0] == agentconfig.GrokBuildProviderPlanV1 && len(definition.Module) > 0
	}
	if manifest.Target == agentconfig.OpenCodeConfig {
		return len(manifest.Capabilities) == 1 && manifest.Capabilities[0] == agentconfig.OpenCodeProviderPlanV1 && len(definition.Module) > 0
	}
	if manifest.Target == agentconfig.GeminiSettings {
		return len(manifest.Capabilities) == 1 && manifest.Capabilities[0] == agentconfig.GeminiProviderPlanV1 && len(definition.Module) > 0
	}
	if manifest.Target == agentconfig.CodexConfig {
		return len(manifest.Capabilities) == 1 && manifest.Capabilities[0] == agentconfig.CodexProviderPlanV1 && len(definition.Module) > 0
	}
	if len(manifest.Capabilities) > 2 || manifest.Target != agentconfig.ClaudeSettings || len(definition.Module) == 0 {
		return false
	}
	seen := make(map[agentconfig.Capability]bool, 2)
	for _, capability := range manifest.Capabilities {
		if seen[capability] || (capability != agentconfig.ClaudeProviderEnvV1 && capability != agentconfig.ClaudeProviderSwitchV1) {
			return false
		}
		seen[capability] = true
	}
	return true
}
