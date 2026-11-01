package configclient

import (
	"reflect"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

func TestGeminiNativePinParity(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal("real builtin registry")
	}
	var actual agentconfig.Descriptor
	for _, entry := range registry.List() {
		if entry.Manifest.PluginID == "builtin.gemini" {
			actual = entry
		}
	}
	if !reflect.DeepEqual(actual, GeminiNativeAdapterPin()) {
		test.Fatal("independent Gemini pin differs from actual moejs definition")
	}
	pin := GeminiNativeAdapterPin()
	pin.Manifest.Capabilities[0] = "mutated"
	if GeminiNativeAdapterPin().Manifest.Capabilities[0] != agentconfig.GeminiProviderPlanV1 {
		test.Fatal("pin aliases caller memory")
	}
}
