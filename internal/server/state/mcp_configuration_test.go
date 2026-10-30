package state_test

import (
	"bytes"
	"database/sql"
	"errors"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"strings"
	"testing"
)

func TestMCPStateAEADRevisionOwnerAndQuota(test *testing.T) {
	store, path, _ := configurationFixture(test)
	service := configuration.NewMCP(store)
	draft := configuration.MCPDraft{AgentID: "claude", Name: "MCP state", Document: agentconfig.MCPDocumentV1{Servers: []agentconfig.MCPServer{{ID: "local", Name: "local", Enabled: new(false), Stdio: &agentconfig.MCPStdio{Command: "node", Env: &agentconfig.MCPNamedSlots{Values: []agentconfig.MCPNamedSlot{{Name: "TOKEN", ValueRef: "env/local/TOKEN"}}}}}}}}
	created, err := service.Create(test.Context(), "alice", draft, []configuration.MCPSecretMutation{{Slot: "env/local/TOKEN", Action: configuration.Replace, Replacement: configuration.NewMCPSecret([]byte("synthetic-MCP-state-secret"))}})
	if err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	var ciphertext []byte
	if err := database.QueryRowContext(test.Context(), "SELECT ciphertext FROM configuration_assets WHERE public_id = ?", created.Metadata.ID).Scan(&ciphertext); err != nil {
		test.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("synthetic-MCP-state-secret")) {
		test.Fatal("MCP secret stored plaintext")
	}
	updated, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, draft, nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := database.ExecContext(test.Context(), "UPDATE configuration_assets SET ciphertext = ? WHERE public_id = ?", ciphertext, created.Metadata.ID); err != nil {
		test.Fatal(err)
	}
	if _, err := service.ReadDraft(test.Context(), "alice", updated.Metadata.ID); !errors.Is(err, configuration.ErrUnavailable) {
		test.Fatal("revision replay ignored AAD")
	}
	for index := 1; index < state.MaxConfigurationAssets; index++ {
		_, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindMCP, Name: "capacity", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(`{"version":1,"document":{"servers":[]},"slots":{}}`))})
		if err != nil {
			test.Fatal("quota rejected before existing 250 limit")
		}
	}
	if _, err := service.Create(test.Context(), "alice", draft, nil); !errors.Is(err, configuration.ErrLimit) {
		test.Fatal("MCP bypassed owner quota")
	}
	if _, err := store.CreateConfigurationAsset(test.Context(), "bob", state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindMCP, Name: "over payload", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(strings.Repeat("x", state.MaxConfigurationPayloadBytes+1)))}); !errors.Is(err, state.ErrConfigurationInvalid) {
		test.Fatal("MCP bypassed shared payload budget")
	}
}
