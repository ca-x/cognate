package agentadapter

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/plugin"
)

func (registry *Registry) PlanMCP(ctx context.Context, request agentconfig.MCPPlanRequest) (agentconfig.MCPDeclarationV1, error) {
	if ctx == nil {
		return agentconfig.MCPDeclarationV1{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.MCPDeclarationV1{}, err
	}
	if registry == nil {
		return agentconfig.MCPDeclarationV1{}, ErrUnsupported
	}
	entry, exists := registry.definitions[request.PluginID]
	if !exists {
		return agentconfig.MCPDeclarationV1{}, ErrUnsupported
	}
	manifest := entry.definition.Manifest
	if manifest.PluginID != agentconfig.MCPPluginID || manifest.PluginVersion != agentconfig.MCPPluginVersion || manifest.Target != agentconfig.ClaudeMCP {
		return agentconfig.MCPDeclarationV1{}, ErrUnsupported
	}
	if request.APIVersion != agentconfig.APIVersion || request.PluginVersion != manifest.PluginVersion || request.Target != manifest.Target || request.DefinitionDigest != entry.digest || len(request.Capabilities) != 1 || request.Capabilities[0] != agentconfig.ClaudeMCPPlanV1 {
		return agentconfig.MCPDeclarationV1{}, ErrIncompatible
	}
	slots, err := agentconfig.ValidateMCPDocument(request.Document)
	if err != nil || len(slots) != len(request.Secrets) {
		return agentconfig.MCPDeclarationV1{}, ErrInvalidRequest
	}
	for index, slot := range slots {
		if request.Secrets[index].Ref != slot.Ref {
			return agentconfig.MCPDeclarationV1{}, ErrInvalidRequest
		}
	}
	if _, err := codexJSONWithin(request, agentconfig.MaxMCPPlanBytes); err != nil {
		return agentconfig.MCPDeclarationV1{}, ErrInvalidRequest
	}
	document, err := agentconfig.CloneMCPDocument(request.Document)
	if err != nil {
		return agentconfig.MCPDeclarationV1{}, ErrInvalidRequest
	}
	expected := expectedMCPDeclaration(document)
	input, err := codexJSONWithin(struct {
		Document agentconfig.MCPDocumentV1   `json:"document"`
		Secrets  []agentconfig.MCPSlotStatus `json:"secrets"`
	}{document, request.Secrets}, agentconfig.MaxMCPPlanBytes)
	if err != nil {
		return agentconfig.MCPDeclarationV1{}, ErrInvalidRequest
	}
	var output any
	if err := plugin.RunJSON(ctx, entry.definition.Module, "planMCP", input, &output); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return agentconfig.MCPDeclarationV1{}, err
		}
		return agentconfig.MCPDeclarationV1{}, ErrPluginExecution
	}
	actual, err := codexJSONWithin(output, agentconfig.MaxMCPPlanBytes)
	if err != nil {
		return agentconfig.MCPDeclarationV1{}, ErrPluginResult
	}
	want, err := json.Marshal(expected)
	if err != nil {
		return agentconfig.MCPDeclarationV1{}, ErrPluginResult
	}
	var normalized any
	if json.Unmarshal(want, &normalized) != nil {
		return agentconfig.MCPDeclarationV1{}, ErrPluginResult
	}
	want, err = codexJSONWithin(normalized, agentconfig.MaxMCPPlanBytes)
	if err != nil || !bytes.Equal(actual, want) {
		return agentconfig.MCPDeclarationV1{}, ErrPluginResult
	}
	if err := ctx.Err(); err != nil {
		return agentconfig.MCPDeclarationV1{}, err
	}
	return expected, nil
}

func expectedMCPDeclaration(document agentconfig.MCPDocumentV1) agentconfig.MCPDeclarationV1 {
	declaration := agentconfig.MCPDeclarationV1{Version: 1, Target: agentconfig.ClaudeMCP, Operations: make([]agentconfig.MCPOperation, 0, len(document.Servers))}
	for _, server := range document.Servers {
		operation := agentconfig.MCPOperation{Action: "disable-managed-server", ServerID: server.ID, Name: server.Name, ManagedOnly: true}
		if *server.Enabled {
			operation.Action = "upsert-managed-server"
			operation.Stdio = server.Stdio
			operation.HTTP = server.HTTP
			operation.SSE = server.SSE
		}
		declaration.Operations = append(declaration.Operations, operation)
	}
	return declaration
}
