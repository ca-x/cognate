package agentadapter

import (
	"context"
	"encoding/json/v2"
	"errors"
	"github.com/ca-x/cognate/internal/agentconfig"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func mcpRequest(test *testing.T, registry *Registry, document agentconfig.MCPDocumentV1) agentconfig.MCPPlanRequest {
	test.Helper()
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.PluginID == agentconfig.MCPPluginID {
			request := agentconfig.MCPPlanRequest{APIVersion: 1, PluginID: agentconfig.MCPPluginID, PluginVersion: agentconfig.MCPPluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: agentconfig.ClaudeMCP, Capabilities: []agentconfig.Capability{agentconfig.ClaudeMCPPlanV1}, Document: document, Secrets: []agentconfig.MCPSlotStatus{}}
			slots, err := agentconfig.ValidateMCPDocument(document)
			if err != nil {
				test.Fatal(err)
			}
			for _, slot := range slots {
				request.Secrets = append(request.Secrets, agentconfig.MCPSlotStatus{Ref: slot.Ref, Present: false})
			}
			return request
		}
	}
	test.Fatal("MCP builtin missing")
	return agentconfig.MCPPlanRequest{}
}

func TestMCPClaudePlanAdversarial(test *testing.T) {
	builtin, err := Builtins()
	if err != nil {
		test.Fatal(err)
	}
	document := agentconfig.MCPDocumentV1{Servers: []agentconfig.MCPServer{{ID: "local", Name: "local", Enabled: new(false), Stdio: &agentconfig.MCPStdio{Command: "node"}}}}
	for _, entry := range []struct{ name, module string }{
		{"empty", `export function planMCP(){return {};}`},
		{"unknown", strings.Replace(mcpClaudeModule, "return {version: 1, target: \"claude.mcp\", operations};", "return {version: 1, target: \"claude.mcp\", operations, surprise: true};", 1)},
		{"enabled-false", strings.ReplaceAll(mcpClaudeModule, "server.enabled", "true")},
		{"missing-operation", strings.Replace(mcpClaudeModule, "operations.push(operation);", "", 1)},
		{"wrong-ref", strings.Replace(mcpClaudeModule, "server.name,", "\"wrong\",", 1)},
		{"io", `export function planMCP(){return process.env;}`},
		{"network", `export function planMCP(){return fetch("https://example.invalid");}`},
		{"hostcap", `import {readFile} from "fs"; export function planMCP(){return readFile("/synthetic");}`},
	} {
		test.Run(entry.name, func(test *testing.T) {
			descriptor := builtin.definitions[agentconfig.MCPPluginID].definition
			descriptor.Module = entry.module
			registry, err := NewRegistry([]Definition{descriptor})
			if err != nil {
				test.Fatal(err)
			}
			result, err := registry.PlanMCP(test.Context(), mcpRequest(test, registry, document))
			if err == nil || !reflect.DeepEqual(result, agentconfig.MCPDeclarationV1{}) {
				test.Fatal("malicious module accepted or nonzero error result")
			}
		})
	}
}

func TestMCPClaudePlanCancelAndClosedMetadata(test *testing.T) {
	registry, err := Builtins()
	if err != nil {
		test.Fatal(err)
	}
	document := agentconfig.MCPDocumentV1{Servers: []agentconfig.MCPServer{}}
	request := mcpRequest(test, registry, document)
	for _, change := range []func(*agentconfig.MCPPlanRequest){
		func(request *agentconfig.MCPPlanRequest) { request.PluginVersion = "2.0.0" },
		func(request *agentconfig.MCPPlanRequest) { request.DefinitionDigest = strings.Repeat("0", 64) },
		func(request *agentconfig.MCPPlanRequest) { request.Target = agentconfig.ClaudeSettings },
		func(request *agentconfig.MCPPlanRequest) { request.Capabilities = []agentconfig.Capability{"unknown"} },
		func(request *agentconfig.MCPPlanRequest) {
			request.Secrets = []agentconfig.MCPSlotStatus{{Ref: "foreign", Present: true}}
		},
	} {
		changed := request
		change(&changed)
		result, err := registry.PlanMCP(test.Context(), changed)
		if err == nil || !reflect.DeepEqual(result, agentconfig.MCPDeclarationV1{}) {
			test.Fatal("metadata bypass")
		}
	}
	definition := registry.definitions[agentconfig.MCPPluginID].definition
	definition.Module = `export function planMCP(){while(true){}}`
	looping, err := NewRegistry([]Definition{definition})
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 40*time.Millisecond)
	defer cancel()
	result, err := looping.PlanMCP(ctx, mcpRequest(test, looping, document))
	if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(result, agentconfig.MCPDeclarationV1{}) {
		test.Fatal("loop not canceled with zero result")
	}
	canceled, stop := context.WithCancel(test.Context())
	stop()
	if _, err := registry.PlanMCP(canceled, request); !errors.Is(err, context.Canceled) {
		test.Fatal("canceled sentinel lost")
	}
}

func TestMCPClaudePlanGolden(test *testing.T) {
	registry, err := Builtins()
	if err != nil {
		test.Fatal(err)
	}
	data, err := os.ReadFile("testdata/claude-mcp-v1.json")
	if err != nil {
		test.Fatal(err)
	}
	var fixture struct {
		Document agentconfig.MCPDocumentV1    `json:"document"`
		Expected agentconfig.MCPDeclarationV1 `json:"expected"`
	}
	if json.Unmarshal(data, &fixture) != nil {
		test.Fatal("invalid fixture")
	}
	actual, err := registry.PlanMCP(test.Context(), mcpRequest(test, registry, fixture.Document))
	if err != nil || !reflect.DeepEqual(actual, fixture.Expected) {
		test.Fatalf("MCP full golden failed: %v", err)
	}
}
