package mcpserver_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/state"
)

type largeLibrary struct{ mcpserver.RepositoryLibrary }

func (source largeLibrary) List(context.Context, string) ([]state.CollectedRepository, error) {
	entries := make([]state.CollectedRepository, 100)
	for index := range entries {
		entries[index] = state.CollectedRepository{ID: "ABCDEFGHIJKLMNOPQRSTUVWXYZ", NameWithOwner: strings.Repeat("x", 4096)}
	}
	return entries, nil
}

func TestSDKResultLimitIsAnErrorNotTruncation(test *testing.T) {
	fixture := newFixture(test, nil)
	endpoint, err := mcpserver.New(mcpserver.Options{Access: fixture.access, Store: fixture.store, Library: largeLibrary{fixture.library}})
	if err != nil {
		test.Fatal(err)
	}
	server := httptest.NewServer(endpoint)
	defer server.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = endpoint.Close(ctx)
	}()
	_, secret := fixture.token(test, "alice", state.MCPCatalogRead)
	client, _ := sdkClient(test, server, secret)
	if !call(test, client, "catalog_list", mcpserver.PageInput{Limit: 100}).IsError {
		test.Fatal("oversized tool result returned successfully")
	}
	page := decode[mcpserver.CatalogPage](test, call(test, client, "catalog_list", mcpserver.PageInput{Limit: 1}))
	if len(page.Repositories) != 1 || page.NextOffset != 1 {
		test.Fatal("smaller result page unavailable")
	}
}
