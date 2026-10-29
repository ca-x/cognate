package configclient_test

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

func TestNativeClientAuthoritativePinParity(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	pin := configclient.NativeAdapterPin()
	found := false
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.PluginID == pin.Manifest.PluginID {
			found = true
			if !reflect.DeepEqual(descriptor, pin) {
				test.Fatal("release pin differs from authoritative manifest/module")
			}
		}
	}
	if !found {
		test.Fatal("missing authoritative pin")
	}
	module, err := os.ReadFile("../server/agentadapter/claude.mjs")
	if err != nil {
		test.Fatal(err)
	}
	var manifests []agentconfig.Manifest
	encoded, err := os.ReadFile("../server/agentadapter/builtins.json")
	if err != nil {
		test.Fatal(err)
	}
	if json.Unmarshal(encoded, &manifests) != nil {
		test.Fatal("invalid authoritative manifests")
	}
	for _, mutation := range []string{"module", "manifest"} {
		definition := agentadapter.Definition{Manifest: pin.Manifest, Module: string(module)}
		if mutation == "module" {
			definition.Module += "\n"
		} else {
			definition.Manifest.PluginVersion = "1.1.1"
		}
		mutated, err := agentadapter.NewRegistry([]agentadapter.Definition{definition})
		if err != nil {
			test.Fatal(err)
		}
		if reflect.DeepEqual(mutated.List()[0], pin) {
			test.Fatal("pin parity negative control accepted definition change")
		}
	}
}

func nativeProductionDependencies(test *testing.T, directory, pattern string) []byte {
	test.Helper()
	command := exec.CommandContext(test.Context(), "go", "list", "-deps", "-f", "{{.ImportPath}}", pattern)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		test.Fatalf("dependency listing failed: %s", output)
	}
	return output
}

func nativeForbiddenDependency(output []byte) bool {
	for line := range strings.SplitSeq(string(output), "\n") {
		if strings.Contains(line, "/internal/server/") || strings.Contains(line, "/moejs") || strings.Contains(line, "/internal/plugin") {
			return true
		}
	}
	return false
}

func TestNativeClientProductionDependencyGate(test *testing.T) {
	if nativeForbiddenDependency(nativeProductionDependencies(test, ".", ".")) {
		test.Fatal("production configclient imports server/plugin/JS engine")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		test.Fatal(err)
	}
	directory := test.TempDir()
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		test.Fatal(err)
	}
	module = bytes.Replace(module, []byte("module github.com/ca-x/cognate"), []byte("module github.com/ca-x/cognate/dependencynegative"), 1)
	module = append(module, []byte("\nrequire github.com/ca-x/cognate v0.0.0\nreplace github.com/ca-x/cognate => "+root+"\n")...)
	if os.WriteFile(filepath.Join(directory, "go.mod"), module, 0600) != nil {
		test.Fatal("negative control module")
	}
	sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		test.Fatal(err)
	}
	if os.WriteFile(filepath.Join(directory, "go.sum"), sums, 0600) != nil {
		test.Fatal("negative control sums")
	}
	if os.WriteFile(filepath.Join(directory, "negative.go"), []byte("package negative\nimport _ \"github.com/ca-x/cognate/internal/server/agentadapter\"\n"), 0600) != nil {
		test.Fatal("negative control source")
	}
	if !nativeForbiddenDependency(nativeProductionDependencies(test, directory, ".")) {
		test.Fatal("mechanical dependency gate missed real forbidden import")
	}
}
