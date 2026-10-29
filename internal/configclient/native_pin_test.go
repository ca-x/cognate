package configclient

import (
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func TestNativeClientPinImmutable(test *testing.T) {
	pin := NativeAdapterPin()
	pin.Manifest.Capabilities[0] = "bad"
	pin.DefinitionDigest = "bad"
	if actual := NativeAdapterPin(); actual.DefinitionDigest != nativeClaudeDigest || actual.Manifest.Capabilities[0] != agentconfig.ClaudeProviderEnvV1 || actual.Manifest.PluginVersion != "1.1.0" {
		test.Fatal("release pin mutable")
	}
	if len(nativeAdapterBinding().RequiredCapabilities) != 1 {
		test.Fatal("wire pin expanded capability")
	}
}
