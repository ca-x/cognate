package state_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type gateLibrary struct{ *state.Store }

func (library gateLibrary) List(ctx context.Context, owner string) ([]state.CollectedRepository, error) {
	return library.ListCollectedRepositories(ctx, owner)
}
func (library gateLibrary) Get(ctx context.Context, owner, id string) (state.CollectedRepository, error) {
	return library.CollectedRepository(ctx, owner, id)
}
func (gateLibrary) Collect(context.Context, string, string, string) (state.CollectedRepository, error) {
	return state.CollectedRepository{}, state.ErrInvalidCollection
}

type gateTransport struct{ token string }

func (transport gateTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	request.Header.Set("Authorization", "Bearer "+transport.token)
	return http.DefaultTransport.RoundTrip(request)
}

func gateClient(test *testing.T, store *state.Store, access *mcpaccess.Service, secret mcpaccess.Secret) *mcp.ClientSession {
	test.Helper()
	return gateClientLibrary(test, store, access, secret, gateLibrary{store})
}

func gateClientLibrary(test *testing.T, store *state.Store, access *mcpaccess.Service, secret mcpaccess.Secret, repositoryLibrary mcpserver.RepositoryLibrary) *mcp.ClientSession {
	test.Helper()
	server, err := mcpserver.New(mcpserver.Options{Access: access, Store: store, Library: repositoryLibrary})
	if err != nil {
		test.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	test.Cleanup(func() { httpServer.Close(); _ = server.Close(context.Background()) })
	client := mcp.NewClient(&mcp.Implementation{Name: "gate-test", Version: "1"}, nil)
	session, err := client.Connect(test.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL + mcpserver.Path, HTTPClient: &http.Client{Transport: gateTransport{token: secret.Reveal()}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMCPHTTPInFlightClassificationAuthorization(test *testing.T) {
	for _, operation := range []string{"create", "update", "delete", "assign", "publish", "unpublish"} {
		for _, invalidation := range []string{"revoke", "rotate", "expire", "scope"} {
			test.Run(operation+"/"+invalidation, func(test *testing.T) {
				store, skills := packFixture(test)
				now := time.Now()
				var clock atomic.Int64
				clock.Store(now.UnixNano())
				store.MCPTestClock(func() time.Time { return time.Unix(0, clock.Load()) })
				category, err := store.CreateCategory(test.Context(), "alice", "Before")
				if err != nil {
					test.Fatal(err)
				}
				repositories, err := store.ListCollectedRepositories(test.Context(), "alice")
				if err != nil {
					test.Fatal(err)
				}
				repositoryID := repositories[0].ID
				initialRevision := int64(1)
				if operation == "unpublish" {
					if _, err := store.SetPublication(test.Context(), "alice", repositoryID, 1, true, true); err != nil {
						test.Fatal(err)
					}
					initialRevision = 2
				}
				scope := state.MCPCategoriesWrite
				if operation == "publish" || operation == "unpublish" {
					scope = state.MCPPublicationWrite
				}
				access, err := mcpaccess.NewService(store)
				if err != nil {
					test.Fatal(err)
				}
				credential, secret, err := access.Create(test.Context(), "alice", state.MCPMetadata{Name: "Gate", Scopes: []string{scope}, ExpiresAt: now.Add(time.Hour)})
				if err != nil {
					test.Fatal(err)
				}
				client := gateClient(test, store, access, secret)
				entered, resume := make(chan struct{}), make(chan struct{})
				var queryCount atomic.Int64
				store.MCPTestInterceptCredentials(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
					return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
						value, err := next.Query(ctx, query)
						if err == nil && queryCount.Add(1) == 2 {
							close(entered)
							select {
							case <-resume:
							case <-ctx.Done():
								return nil, ctx.Err()
							}
						}
						return value, err
					})
				}))
				name := "categories_" + operation
				args := map[string]any{"id": category.ID, "expected_revision": 1, "name": "After"}
				if operation == "create" {
					args = map[string]any{"name": "After"}
				}
				if operation == "delete" {
					delete(args, "name")
				}
				if operation == "assign" {
					args = map[string]any{"skill_id": skills[0], "category_ids": []string{category.ID}}
				}
				if operation == "publish" || operation == "unpublish" {
					name = "publication_" + operation
					args = map[string]any{"id": repositoryID, "expected_revision": initialRevision, "confirm_full_contents": true}
				}
				if operation == "unpublish" {
					delete(args, "confirm_full_contents")
				}
				type outcome struct {
					result *mcp.CallToolResult
					err    error
				}
				finished := make(chan outcome, 1)
				go func() {
					result, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
					finished <- outcome{result, err}
				}()
				select {
				case <-entered:
				case result := <-finished:
					test.Fatalf("call finished before barrier (queries=%d): %+v %v", queryCount.Load(), result.result, result.err)
				case <-time.After(5 * time.Second):
					test.Fatal("call never reached authorization barrier")
				}
				switch invalidation {
				case "revoke":
					_, err = access.Revoke(test.Context(), "alice", credential.ID, credential.Revision)
				case "rotate":
					_, _, err = access.Rotate(test.Context(), "alice", credential.ID, credential.Revision)
				case "expire":
					clock.Store(now.Add(time.Hour).UnixNano())
				case "scope":
					_, err = access.Update(test.Context(), "alice", credential.ID, credential.Revision, state.MCPMetadata{Name: "Narrowed", Scopes: []string{state.MCPCatalogRead}, ExpiresAt: now.Add(time.Hour)})
				}
				close(resume)
				if err != nil {
					test.Fatal(err)
				}
				result := <-finished
				if result.err == nil && !result.result.IsError {
					test.Fatal("in-flight stale authority committed")
				}
				categories, err := store.ListCategories(test.Context(), "alice")
				if err != nil || len(categories) != 1 || categories[0].Name != "Before" || categories[0].Revision != 1 {
					test.Fatalf("denied operation changed categories: %+v %v", categories, err)
				}
				selected, err := store.SkillCategories(test.Context(), "alice", skills[0])
				if err != nil || len(selected) != 0 {
					test.Fatalf("denied association committed: %v", err)
				}
				publication, err := store.Publication(test.Context(), "alice", repositoryID)
				if err != nil || publication.Revision != initialRevision || publication.Published != (operation == "unpublish") {
					test.Fatalf("denied publication changed state: %+v %v", publication, err)
				}
			})
		}
	}
}

func TestMCPHTTPExpiryAtClassificationCommitRollsBack(test *testing.T) {
	for _, operation := range []string{"category", "publication"} {
		test.Run(operation, func(test *testing.T) {
			store, _ := packFixture(test)
			now := time.Now()
			var clock atomic.Int64
			clock.Store(now.UnixNano())
			store.MCPTestClock(func() time.Time { return time.Unix(0, clock.Load()) })
			access, err := mcpaccess.NewService(store)
			if err != nil {
				test.Fatal(err)
			}
			scope := state.MCPCategoriesWrite
			if operation == "publication" {
				scope = state.MCPPublicationWrite
			}
			_, secret, err := access.Create(test.Context(), "alice", state.MCPMetadata{Name: "Commit expiry", Scopes: []string{scope}, ExpiresAt: now.Add(time.Hour)})
			if err != nil {
				test.Fatal(err)
			}
			client := gateClient(test, store, access, secret)
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					value, err := next.Mutate(ctx, mutation)
					if err == nil {
						clock.Store(now.Add(time.Hour).UnixNano())
					}
					return value, err
				})
			}
			name, args := "categories_create", map[string]any{"name": "Expired"}
			repositories, err := store.ListCollectedRepositories(test.Context(), "alice")
			if err != nil {
				test.Fatal(err)
			}
			if operation == "category" {
				store.MCPTestCategoryHook(hook)
			} else {
				store.MCPTestPublicationHook(hook)
				name, args = "publication_publish", map[string]any{"id": repositories[0].ID, "expected_revision": 1, "confirm_full_contents": true}
			}
			result, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil || !result.IsError {
				test.Fatalf("expiry after mutation was not rejected: %v", err)
			}
			categories, err := store.ListCategories(test.Context(), "alice")
			if err != nil || len(categories) != 0 {
				test.Fatalf("expired transaction committed category: %v", err)
			}
			publication, err := store.Publication(test.Context(), "alice", repositories[0].ID)
			if err != nil || publication.Published || publication.Revision != 1 {
				test.Fatalf("expired transaction committed publication: %+v %v", publication, err)
			}
		})
	}
}

func TestMCPAuthorizationContextCannotForgeOrEscalateGrant(test *testing.T) {
	store, _ := packFixture(test)
	access, err := mcpaccess.NewService(store)
	if err != nil {
		test.Fatal(err)
	}
	credential, secret, err := access.Create(test.Context(), "alice", state.MCPMetadata{Name: "Context", Scopes: []string{state.MCPCategoriesWrite}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	grant, err := access.Authorize(test.Context(), secret.Reveal(), state.MCPCategoriesWrite)
	if err != nil || grant.Revision != credential.Revision {
		test.Fatalf("grant revision missing: %v", err)
	}
	forged := state.MCPGrant{CredentialID: grant.CredentialID, Owner: grant.Owner, Scopes: grant.Scopes, Revision: grant.Revision, ExpiresAt: grant.ExpiresAt}
	if _, err := store.CreateCategory(state.WithMCPAuthorization(test.Context(), forged, state.MCPCategoriesWrite), "alice", "Forged"); !errors.Is(err, state.ErrMCPForbidden) {
		test.Fatalf("public grant fields forged authority: %v", err)
	}
	if _, err := store.CreateCategory(state.WithMCPAuthorization(test.Context(), grant, state.MCPCategoriesWrite), "bob", "Foreign"); !errors.Is(err, state.ErrMCPForbidden) {
		test.Fatalf("context retargeted owner: %v", err)
	}
	repositories, err := store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil {
		test.Fatal(err)
	}
	grant.Scopes = []string{state.MCPPublicationWrite}
	if _, err := store.SetPublication(state.WithMCPAuthorization(test.Context(), grant, state.MCPPublicationWrite), "alice", repositories[0].ID, 1, true, true); !errors.Is(err, state.ErrMCPForbidden) {
		test.Fatalf("grant scopes forged publication authority: %v", err)
	}
	if _, err := store.CreateCategory(state.WithMCPAuthorization(test.Context(), grant, state.MCPCategoriesRead), "alice", "Wrong scope"); !errors.Is(err, state.ErrMCPForbidden) {
		test.Fatalf("wrong context scope permitted write: %v", err)
	}
	if _, err := store.CreateCategory(state.WithMCPAuthorization(test.Context(), grant, state.MCPCategoriesWrite), "alice", "Valid"); err != nil {
		test.Fatalf("valid proof failed after public fields changed: %v", err)
	}
	if _, err := store.CreateCategory(test.Context(), "bob", "Web owner"); err != nil {
		test.Fatalf("ordinary Web path changed: %v", err)
	}
	updated, err := access.Update(test.Context(), "alice", credential.ID, credential.Revision, state.MCPMetadata{Name: "Metadata changed", Scopes: []string{state.MCPCategoriesWrite}, ExpiresAt: credential.ExpiresAt})
	if err != nil {
		test.Fatal(err)
	}
	grant.Revision = updated.Revision
	if _, err := store.CreateCategory(state.WithMCPAuthorization(test.Context(), grant, state.MCPCategoriesWrite), "alice", "Stale proof"); !errors.Is(err, state.ErrMCPUnauthorized) {
		test.Fatalf("public revision refreshed stale authority: %v", err)
	}
}
