package configclient

import (
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
)

const nativeCodexDigest = "f45d146803f9f4f818a5d17ea1cc536b27bc77f49eb059c10c2f305912af02b4"

func CodexNativeAdapterPin() agentconfig.Descriptor {
	return agentconfig.Descriptor{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "builtin.codex", PluginVersion: "1.1.0", AgentID: "codex", Target: agentconfig.CodexConfig, Format: agentconfig.TOML, Capabilities: []agentconfig.Capability{agentconfig.CodexProviderPlanV1}}, DefinitionDigest: nativeCodexDigest}
}

func codexNativeAdapterBinding() *nativev1.AdapterBinding {
	return &nativev1.AdapterBinding{ApiVersion: 1, PluginId: "builtin.codex", PluginVersion: "1.1.0", DefinitionDigest: nativeCodexDigest, RequiredCapabilities: []string{"codex.provider-plan.v1"}}
}
