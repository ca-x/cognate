package configclient

import (
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
)

const nativeClaudeDigest = "815fce29d6443041f11e29cb67660004f4a15a630917282c965fe7e6a348ee34"

func NativeAdapterPin() agentconfig.Descriptor {
	return agentconfig.Descriptor{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "builtin.claude", PluginVersion: "1.1.0", AgentID: "claude", Target: agentconfig.ClaudeSettings, Format: agentconfig.JSON, Capabilities: []agentconfig.Capability{agentconfig.ClaudeProviderEnvV1, agentconfig.ClaudeProviderSwitchV1}}, DefinitionDigest: nativeClaudeDigest}
}

func nativeAdapterBinding() *nativev1.AdapterBinding {
	return &nativev1.AdapterBinding{ApiVersion: 1, PluginId: "builtin.claude", PluginVersion: "1.1.0", DefinitionDigest: nativeClaudeDigest, RequiredCapabilities: []string{"claude.provider-switch.v1"}}
}

func nativeSafePatch(patch *nativev1.ClaudeProviderPatch) agentconfig.ClaudeProviderPatchV1 {
	slots := []agentconfig.ClaudeEnvSlot{agentconfig.ClaudeEnvAPIKey, agentconfig.ClaudeEnvAuthToken, agentconfig.ClaudeEnvBaseURL, agentconfig.ClaudeEnvModel, agentconfig.ClaudeEnvHaikuModel, agentconfig.ClaudeEnvSonnetModel, agentconfig.ClaudeEnvOpusModel}
	result := agentconfig.ClaudeProviderPatchV1{Target: agentconfig.ClaudeSettings}
	for index, operation := range patch.Operations {
		entry := agentconfig.ClaudeProviderPatchOperation{Slot: slots[index]}
		switch action := operation.Action.(type) {
		case *nativev1.PatchOperation_SetLiteral:
			entry.Action, entry.Literal = agentconfig.ClaudeSetLiteral, new(action.SetLiteral.Value)
		case *nativev1.PatchOperation_SetSelectedSecret:
			reference := agentconfig.ClaudeAPIKeySecret
			if action.SetSelectedSecret.Slot == nativev1.SecretSlot_SECRET_SLOT_CLAUDE_AUTH_TOKEN {
				reference = agentconfig.ClaudeAuthTokenSecret
			}
			entry.Action, entry.Reference = agentconfig.ClaudeSetSelectedSecret, new(reference)
		case *nativev1.PatchOperation_Remove:
			entry.Action = agentconfig.ClaudeRemove
		}
		result.Operations = append(result.Operations, entry)
	}
	return result
}
