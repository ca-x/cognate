package configclient

import (
	"reflect"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

func TestCodexNativePinParity(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal("builtins unavailable")
	}
	pin := CodexNativeAdapterPin()
	var actual agentconfig.Descriptor
	for _, entry := range registry.List() {
		if entry.Manifest.PluginID == "builtin.codex" {
			actual = entry
		}
	}
	if !reflect.DeepEqual(actual, pin) {
		test.Fatal("real Registry definition differs from independent client pin")
	}
	pin.Manifest.Capabilities[0] = "changed"
	if CodexNativeAdapterPin().Manifest.Capabilities[0] != agentconfig.CodexProviderPlanV1 {
		test.Fatal("pin shares mutable memory")
	}
}
