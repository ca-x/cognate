package configclient

import (
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
)

const nativeGeminiDigest = "b0ba50fab171fea7889625da4ed9d36dc9b811303a166b66fb23bb1b232bd095"

func GeminiNativeAdapterPin() agentconfig.Descriptor {
	return agentconfig.Descriptor{Manifest: agentconfig.Manifest{APIVersion: 1, PluginID: "builtin.gemini", PluginVersion: "1.1.0", AgentID: "gemini", Target: agentconfig.GeminiSettings, Format: agentconfig.JSON, Capabilities: []agentconfig.Capability{agentconfig.GeminiProviderPlanV1}}, DefinitionDigest: nativeGeminiDigest}
}
func geminiNativeAdapterBinding() *nativev1.AdapterBinding {
	return &nativev1.AdapterBinding{ApiVersion: 1, PluginId: "builtin.gemini", PluginVersion: "1.1.0", DefinitionDigest: nativeGeminiDigest, RequiredCapabilities: []string{"gemini.provider-plan.v1"}}
}
