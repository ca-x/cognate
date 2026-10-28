package mcpserver_test

import (
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestSDKScopeChangesApplyToAlreadyConnectedClient(test *testing.T) {
	fixture := newFixture(test, nil)
	_, server := fixture.endpoint(test, mcpserver.Options{})
	entry, secret := fixture.token(test, "alice", state.MCPCatalogRead)
	client, _ := sdkClient(test, server, secret)
	decode[mcpserver.CatalogPage](test, call(test, client, "catalog_list", mcpserver.PageInput{}))
	if _, err := fixture.access.Update(test.Context(), "alice", entry.ID, entry.Revision, state.MCPMetadata{Name: "Narrowed", Scopes: []string{state.MCPPacksRead}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		test.Fatal(err)
	}
	if !call(test, client, "catalog_list", mcpserver.PageInput{}).IsError {
		test.Fatal("SDK client retained removed scope without rotating token")
	}
	decode[mcpserver.PackPage](test, call(test, client, "packs_list", mcpserver.PageInput{}))
}
