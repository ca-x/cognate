package configuration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/agentadapter"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestMCPPrivacyNestedContainers(test *testing.T) {
	canary := []byte("MCP-PRIVATE-CANARY-638124")
	secret := NewMCPSecret(canary)
	defer secret.Close()
	envelope, err := mergeMCPEnvelope(mcpEnvelope{}, mcpDraft().Document, mcpReplacements())
	if err != nil {
		test.Fatal(err)
	}
	defer envelope.Close()
	type hidden struct {
		secret   MCPSecret
		envelope mcpEnvelope
	}
	values := []any{secret, &secret, envelope, &envelope, hidden{secret, envelope}, struct{ Value any }{hidden{secret, envelope}}, []any{hidden{secret, envelope}}, map[string]any{"private": hidden{secret, envelope}}}
	forbidden := []string{string(canary), hex.EncodeToString(canary), strings.ToUpper(hex.EncodeToString(canary)), base64.StdEncoding.EncodeToString(canary), fmt.Sprint(canary), strings.Trim(fmt.Sprint(canary), "[]"), fmt.Sprintf("%#v", canary)}
	for _, value := range values {
		var output bytes.Buffer
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%o", "%b"} {
			fmt.Fprintf(&output, format, value)
		}
		encoded, err := json.Marshal(value)
		output.Write(encoded)
		if err != nil {
			output.WriteString(err.Error())
		}
		slog.New(slog.NewJSONHandler(&output, nil)).Info("synthetic", "value", value)
		for _, needle := range forbidden {
			if strings.Contains(output.String(), needle) {
				test.Fatal("private container leaked sentinel representation")
			}
		}
	}
}

func TestMCPPrivacyActualPluginInputAndErrors(test *testing.T) {
	_, service, _, _ := mcpFixture(test)
	module, err := os.ReadFile("../agentadapter/mcp_claude.mjs")
	if err != nil {
		test.Fatal(err)
	}
	manifest := agentconfig.Manifest{APIVersion: 1, PluginID: agentconfig.MCPPluginID, PluginVersion: agentconfig.MCPPluginVersion, AgentID: "claude", Target: agentconfig.ClaudeMCP, Format: agentconfig.JSON, Capabilities: []agentconfig.Capability{agentconfig.ClaudeMCPPlanV1}}
	checked := strings.Replace(string(module), "const operations = [];", `if(JSON.stringify(input).includes("MCP-PRIVATE-CANARY-638124")) throw new Error("taint"); const operations = [];`, 1)
	service.registry, err = agentadapter.NewRegistry([]agentadapter.Definition{{Manifest: manifest, Module: checked}})
	if err != nil {
		test.Fatal(err)
	}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	created, err := service.Create(test.Context(), "alice", mcpDraft(), mcpReplacements())
	if err != nil || created.Readiness != CredentialComplete {
		test.Fatal("secrets reached real moejs or positive plan failed")
	}
	service.registry, err = agentadapter.NewRegistry([]agentadapter.Definition{{Manifest: manifest, Module: `export function planMCP(){throw new Error("MCP-PRIVATE-CANARY-638124");}`}})
	if err != nil {
		test.Fatal(err)
	}
	zero, err := service.Create(test.Context(), "alice", mcpDraft(), mcpReplacements())
	if !errors.Is(err, ErrUnavailable) || zero.Metadata.ID != "" {
		test.Fatal("plugin failure not safe zero result")
	}
	if strings.Contains(err.Error()+output.String(), "MCP-PRIVATE-CANARY-638124") {
		test.Fatal("raw plugin error or secret reached log")
	}
}

func TestMCPPrivacyErrorsCancelAndViews(test *testing.T) {
	_, service, _, _ := mcpFixture(test)
	for _, canceled := range []bool{false, true} {
		mutations := mcpReplacements()
		ctx, cancel := context.WithCancel(test.Context())
		if canceled {
			cancel()
		}
		view, err := service.Create(ctx, "alice", mcpDraft(), mutations)
		cancel()
		if canceled && (!errors.Is(err, context.Canceled) || view.Metadata.ID != "") {
			test.Fatal("cancellation not zero/sentinel")
		}
		for _, mutation := range mutations {
			if mutation.Replacement.bytes() != nil {
				test.Fatal("input mutation secret not cleared")
			}
		}
		encoded, _ := json.Marshal(view)
		if strings.Contains(string(encoded), "MCP-PRIVATE-CANARY") {
			test.Fatal("view secret leak")
		}
	}
	invalid := mcpDraft()
	invalid.AgentID = "codex"
	mutations := mcpReplacements()
	if _, err := service.Create(test.Context(), "alice", invalid, mutations); !errors.Is(err, ErrUnsupported) {
		test.Fatal("unqualified target accepted")
	}
	for _, mutation := range mutations {
		if mutation.Replacement.bytes() != nil {
			test.Fatal("failure retained secret")
		}
	}
}
