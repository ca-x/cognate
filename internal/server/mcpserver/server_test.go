package mcpserver_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fetcher func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error)

func (fetch fetcher) Fetch(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
	return fetch(ctx, reference)
}

type fixture struct {
	store   *state.Store
	access  *mcpaccess.Service
	library *library.Service
}

func newFixture(test *testing.T, fetch fetcher) fixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	for _, owner := range []string{"alice", "bob"} {
		if err := store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
	}
	cache, err := library.OpenArchiveCache(filepath.Join(directory, "objects"))
	if err != nil {
		test.Fatal(err)
	}
	if fetch == nil {
		fetch = func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
			data := []byte("verified repository fixture")
			hash := sha256.Sum256(data)
			return &repository.ArchiveSnapshot{Data: data, Hash: hex.EncodeToString(hash[:]), Skills: []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Example", Description: "A useful skill", Path: "."}, ContentHash: strings.Repeat("a", 64)}}}, nil
		}
	}
	libraryService, err := library.NewService(store, cache, fetch)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = libraryService.Close() })
	access, err := mcpaccess.NewService(store)
	if err != nil {
		test.Fatal(err)
	}
	return fixture{store: store, access: access, library: libraryService}
}

func (fixture fixture) endpoint(test *testing.T, options mcpserver.Options) (*mcpserver.Server, *httptest.Server) {
	test.Helper()
	options.Access, options.Store, options.Library = fixture.access, fixture.store, fixture.library
	endpoint, err := mcpserver.New(options)
	if err != nil {
		test.Fatal(err)
	}
	server := httptest.NewServer(endpoint)
	test.Cleanup(server.Close)
	test.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := endpoint.Close(ctx); err != nil {
			test.Error(err)
		}
	})
	return endpoint, server
}

func (fixture fixture) token(test *testing.T, owner string, scopes ...string) (state.MCPCredential, mcpaccess.Secret) {
	test.Helper()
	entry, secret, err := fixture.access.Create(test.Context(), owner, state.MCPMetadata{Name: "SDK client", Scopes: scopes, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	return entry, secret
}

type bearerTransport struct {
	mutex sync.RWMutex
	token string
}

func (transport *bearerTransport) set(token string) {
	transport.mutex.Lock()
	transport.token = token
	transport.mutex.Unlock()
}

func (transport *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	transport.mutex.RLock()
	request.Header.Set("Authorization", "Bearer "+transport.token)
	transport.mutex.RUnlock()
	return http.DefaultTransport.RoundTrip(request)
}

func sdkClient(test *testing.T, server *httptest.Server, token mcpaccess.Secret) (*mcp.ClientSession, *bearerTransport) {
	test.Helper()
	return sdkClientProtocol(test, server, token, "")
}

func sdkClientProtocol(test *testing.T, server *httptest.Server, token mcpaccess.Secret, version string) (*mcp.ClientSession, *bearerTransport) {
	test.Helper()
	transport := &bearerTransport{token: token.Reveal()}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(test.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL + mcpserver.Path, HTTPClient: &http.Client{Transport: transport}, DisableStandaloneSSE: true, MaxRetries: -1}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = session.Close() })
	return session, transport
}

func call(test *testing.T, client *mcp.ClientSession, name string, arguments any) *mcp.CallToolResult {
	test.Helper()
	result, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		test.Fatal(err)
	}
	return result
}

func decode[Output any](test *testing.T, result *mcp.CallToolResult) Output {
	test.Helper()
	if result.IsError {
		test.Fatalf("tool failed: %v", result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		test.Fatal(err)
	}
	var output Output
	if err := json.Unmarshal(data, &output); err != nil {
		test.Fatal(err)
	}
	return output
}

func TestSDKScopesAreExplicitAndIndependent(test *testing.T) {
	fixture := newFixture(test, nil)
	_, server := fixture.endpoint(test, mcpserver.Options{})
	for _, scenario := range []struct {
		scope string
		tool  string
		args  any
	}{
		{state.MCPCatalogRead, "catalog_collect", mcpserver.CollectInput{Repository: "org/forbidden"}},
		{state.MCPCatalogWrite, "catalog_list", mcpserver.PageInput{}},
		{state.MCPPacksRead, "catalog_list", mcpserver.PageInput{}},
		{state.MCPPacksWrite, "packs_list", mcpserver.PageInput{}},
	} {
		test.Run(scenario.scope, func(test *testing.T) {
			entry, secret := fixture.token(test, "alice", scenario.scope)
			client, _ := sdkClient(test, server, secret)
			result := call(test, client, scenario.tool, scenario.args)
			if !result.IsError {
				test.Fatalf("%s authorized %s without its explicit scope", scenario.scope, scenario.tool)
			}
			audits, err := fixture.access.ListAudit(test.Context(), "alice", entry.ID)
			if err != nil || len(audits) < 2 || audits[0].Action != scenario.tool || audits[0].Outcome != "denied" {
				test.Fatal("scope denial was not audited as safe metadata")
			}
		})
	}
}

func TestSDKCatalogAndPackFlowOwnerIsolationAndCAS(test *testing.T) {
	fixture := newFixture(test, nil)
	_, server := fixture.endpoint(test, mcpserver.Options{})
	_, secret := fixture.token(test, "alice", state.MCPCatalogRead, state.MCPCatalogWrite, state.MCPPacksRead, state.MCPPacksWrite)
	client, _ := sdkClient(test, server, secret)
	tools, err := client.ListTools(test.Context(), nil)
	if err != nil {
		test.Fatal(err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema == nil || tool.OutputSchema == nil {
			test.Fatal("tool is not typed")
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"catalog_collect", "catalog_get", "catalog_list", "categories_assign", "categories_create", "categories_delete", "categories_get", "categories_list", "categories_update", "packs_create", "packs_delete", "packs_get", "packs_list", "packs_update", "publication_get", "publication_publish", "publication_unpublish"}) {
		test.Fatalf("unexpected tools: %v", names)
	}
	collected := decode[mcpserver.Repository](test, call(test, client, "catalog_collect", mcpserver.CollectInput{Repository: "org/repo"}))
	got := decode[mcpserver.Repository](test, call(test, client, "catalog_get", mcpserver.IDInput{ID: collected.ID}))
	if len(got.Skills) != 1 || got.NameWithOwner != "org/repo" {
		test.Fatalf("real library detail mismatch: %+v", got)
	}
	page := decode[mcpserver.CatalogPage](test, call(test, client, "catalog_list", mcpserver.PageInput{Limit: 1}))
	if len(page.Repositories) != 1 || page.Repositories[0].ID != collected.ID {
		test.Fatal("collected repository missing from SDK list")
	}
	draft := mcpserver.PackDraft{Name: "My Pack", Items: []mcpserver.PackSelection{{SkillID: got.Skills[0].ID}}}
	created := decode[mcpserver.Pack](test, call(test, client, "packs_create", draft))
	if created.Revision != 1 || len(created.Items) != 1 {
		test.Fatal("created Pack missing revision or selection")
	}
	_, foreignSecret := fixture.token(test, "bob", state.MCPCatalogRead, state.MCPPacksRead, state.MCPPacksWrite)
	foreign, _ := sdkClient(test, server, foreignSecret)
	for _, forbidden := range []struct {
		name string
		args any
	}{
		{"catalog_get", mcpserver.IDInput{ID: collected.ID}},
		{"packs_get", mcpserver.IDInput{ID: created.ID}},
		{"packs_update", mcpserver.UpdatePackInput{ID: created.ID, ExpectedRevision: 1, Draft: draft}},
		{"packs_delete", mcpserver.DeletePackInput{ID: created.ID, ExpectedRevision: 1}},
		{"packs_create", draft},
	} {
		if !call(test, foreign, forbidden.name, forbidden.args).IsError {
			test.Fatalf("cross-owner %s succeeded", forbidden.name)
		}
	}
	if page := decode[mcpserver.CatalogPage](test, call(test, foreign, "catalog_list", mcpserver.PageInput{})); len(page.Repositories) != 0 {
		test.Fatal("foreign catalog leaked")
	}
	if page := decode[mcpserver.PackPage](test, call(test, foreign, "packs_list", mcpserver.PageInput{})); len(page.Packs) != 0 {
		test.Fatal("foreign Packs leaked")
	}
	draft.Name = "Revised"
	updated := decode[mcpserver.Pack](test, call(test, client, "packs_update", mcpserver.UpdatePackInput{ID: created.ID, ExpectedRevision: 1, Draft: draft}))
	if updated.Revision != 2 || updated.Name != "Revised" {
		test.Fatal("CAS update did not advance revision")
	}
	if !call(test, client, "packs_delete", mcpserver.DeletePackInput{ID: created.ID, ExpectedRevision: 1}).IsError {
		test.Fatal("stale revision deleted Pack")
	}
	if result := decode[mcpserver.Deleted](test, call(test, client, "packs_delete", mcpserver.DeletePackInput{ID: created.ID, ExpectedRevision: 2})); !result.Deleted {
		test.Fatal("Pack not deleted")
	}
}

func TestSDKExistingClientLosesAuthorityOnRotationRevokeAndExpiry(test *testing.T) {
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		test.Run(version, func(test *testing.T) { existingClientInvalidation(test, version) })
	}
}

func existingClientInvalidation(test *testing.T, version string) {
	test.Helper()
	fixture := newFixture(test, nil)
	_, server := fixture.endpoint(test, mcpserver.Options{})
	entry, secret := fixture.token(test, "alice", state.MCPCatalogRead)
	client, _ := sdkClientProtocol(test, server, secret, version)
	decode[mcpserver.CatalogPage](test, call(test, client, "catalog_list", mcpserver.PageInput{}))
	rotated, newSecret, err := fixture.access.Rotate(test.Context(), "alice", entry.ID, entry.Revision)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: "catalog_list", Arguments: mcpserver.PageInput{}}); err == nil {
		test.Fatal("existing SDK client retained old rotated authority")
	}
	client, _ = sdkClientProtocol(test, server, newSecret, version)
	decode[mcpserver.CatalogPage](test, call(test, client, "catalog_list", mcpserver.PageInput{}))
	if _, err := fixture.access.Revoke(test.Context(), "alice", entry.ID, rotated.Revision); err != nil {
		test.Fatal(err)
	}
	if _, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: "catalog_list", Arguments: mcpserver.PageInput{}}); err == nil {
		test.Fatal("existing SDK client retained revoked authority")
	}
	_, expiring, err := fixture.access.Create(test.Context(), "alice", state.MCPMetadata{Name: "Expiring", Scopes: []string{state.MCPCatalogRead}, ExpiresAt: time.Now().Add(300 * time.Millisecond)})
	if err != nil {
		test.Fatal(err)
	}
	expiryClient, _ := sdkClientProtocol(test, server, expiring, version)
	time.Sleep(350 * time.Millisecond)
	if _, err := expiryClient.CallTool(test.Context(), &mcp.CallToolParams{Name: "catalog_list", Arguments: mcpserver.PageInput{}}); err == nil {
		test.Fatal("existing SDK client retained expired authority")
	}
}

func TestHTTPBodyOriginQueryAndMethodBounds(test *testing.T) {
	fixture := newFixture(test, nil)
	_, server := fixture.endpoint(test, mcpserver.Options{})
	_, secret := fixture.token(test, "alice", state.MCPCatalogRead)
	for _, scenario := range []struct {
		name   string
		method string
		query  string
		origin string
		body   string
		status int
	}{
		{"oversize", "POST", "", "", strings.Repeat(" ", mcpserver.MaxRequestBytes+1), 413},
		{"query", "POST", "?token=forbidden", "", `{}`, 403},
		{"origin", "POST", "", "https://attacker.invalid", `{}`, 403},
		{"get", "GET", "", "", "", 405},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			request, err := http.NewRequestWithContext(test.Context(), scenario.method, server.URL+mcpserver.Path+scenario.query, strings.NewReader(scenario.body))
			if err != nil {
				test.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+secret.Reveal())
			if scenario.origin != "" {
				request.Header.Set("Origin", scenario.origin)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			response, err := server.Client().Do(request)
			if err != nil {
				test.Fatal(err)
			}
			defer response.Body.Close()
			data, _ := io.ReadAll(response.Body)
			if response.StatusCode != scenario.status || bytes.Contains(data, []byte(secret.Reveal())) || response.Header.Get("Mcp-Session-Id") != "" || response.Header.Get("Cache-Control") != "no-store" {
				test.Fatalf("unexpected bounded response status: %d", response.StatusCode)
			}
		})
	}
}
