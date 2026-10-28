package state_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type legacyGateFetcher func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error)

func (fetch legacyGateFetcher) Fetch(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
	return fetch(ctx, reference)
}

func legacyGateSnapshot() *repository.ArchiveSnapshot {
	data := []byte("verified public repository gate fixture")
	digest := sha256.Sum256(data)
	return &repository.ArchiveSnapshot{Data: data, Hash: hex.EncodeToString(digest[:]), Skills: []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Gate skill", Description: "Public test skill", Path: "skills/gate"}, ContentHash: strings.Repeat("d", 64)}}}
}

func legacyGateLibrary(test *testing.T, store *state.Store, fetcher legacyGateFetcher) *library.Service {
	test.Helper()
	cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "objects"))
	if err != nil {
		test.Fatal(err)
	}
	service, err := library.NewService(store, cache, fetcher)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	return service
}

type legacyGateScenario struct {
	store           *state.Store
	library         mcpserver.RepositoryLibrary
	scope           string
	name            string
	args            any
	assertUnchanged func(*testing.T)
	mutationHook    func(ent.Hook)
}

func newLegacyGateScenario(test *testing.T, operation string, beforeFetch func(context.Context) error) legacyGateScenario {
	test.Helper()
	store, skills := packFixture(test)
	scenario := legacyGateScenario{store: store, library: gateLibrary{store}, scope: state.MCPPacksWrite, name: "packs_" + operation}
	if operation == "collect" {
		scenario.name, scenario.scope = "catalog_collect", state.MCPCatalogWrite
		scenario.args = mcpserver.CollectInput{Repository: "org/new-gate"}
		before, err := store.ListCollectedRepositories(test.Context(), "alice")
		if err != nil {
			test.Fatal(err)
		}
		scenario.library = legacyGateLibrary(test, store, func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
			if beforeFetch != nil {
				if err := beforeFetch(ctx); err != nil {
					return nil, err
				}
			}
			return legacyGateSnapshot(), nil
		})
		scenario.mutationHook = store.MCPTestSkillHook
		scenario.assertUnchanged = func(test *testing.T) {
			after, err := store.ListCollectedRepositories(test.Context(), "alice")
			if err != nil || !reflect.DeepEqual(before, after) {
				test.Fatalf("denied collect changed repository/skill state: %v", err)
			}
		}
		return scenario
	}
	initial, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Before", Description: "Original", Items: []state.PackSelection{{SkillID: skills[0], Slug: "first"}, {SkillID: skills[1], Slug: "second"}}})
	if err != nil {
		test.Fatal(err)
	}
	share, err := store.CreatePackShare(test.Context(), "alice", initial.ID, rand.Text(), make([]byte, 32), nil)
	if err != nil {
		test.Fatal(err)
	}
	initial, err = store.Pack(test.Context(), "alice", initial.ID)
	if err != nil {
		test.Fatal(err)
	}
	draft := mcpserver.PackDraft{Name: "After", Items: []mcpserver.PackSelection{{SkillID: skills[1], Slug: "changed"}}}
	scenario.mutationHook = store.MCPTestPackItemHook
	switch operation {
	case "create":
		scenario.args = draft
	case "update":
		scenario.args = mcpserver.UpdatePackInput{ID: initial.ID, ExpectedRevision: initial.Revision, Draft: draft}
	case "delete":
		scenario.args = mcpserver.DeletePackInput{ID: initial.ID, ExpectedRevision: initial.Revision}
		scenario.mutationHook = store.MCPTestPackHook
	default:
		test.Fatalf("unexpected operation: %s", operation)
	}
	scenario.assertUnchanged = func(test *testing.T) {
		packs, err := store.ListPacks(test.Context(), "alice")
		if err != nil || len(packs) != 1 {
			test.Fatalf("denied %s changed Pack count: %v", operation, err)
		}
		after, err := store.Pack(test.Context(), "alice", initial.ID)
		if err != nil || !reflect.DeepEqual(initial, after) {
			test.Fatalf("denied %s changed Pack metadata/revision/items: %v", operation, err)
		}
		shares, err := store.ListPackShares(test.Context(), "alice", initial.ID)
		if err != nil || len(shares) != 1 || shares[0].ID != share.ID {
			test.Fatalf("denied %s removed share: %v", operation, err)
		}
	}
	return scenario
}

func TestMCPHTTPCatalogAndPackInFlightAuthorization(test *testing.T) {
	for _, operation := range []string{"collect", "create", "update", "delete"} {
		for _, invalidation := range []string{"revoke", "rotate", "expire", "scope", "revision"} {
			test.Run(operation+"/"+invalidation, func(test *testing.T) {
				entered, resume := make(chan struct{}), make(chan struct{})
				pause := func(ctx context.Context) error {
					close(entered)
					select {
					case <-resume:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				scenario := newLegacyGateScenario(test, operation, pause)
				now := time.Now()
				var clock atomic.Int64
				clock.Store(now.UnixNano())
				scenario.store.MCPTestClock(func() time.Time { return time.Unix(0, clock.Load()) })
				access, err := mcpaccess.NewService(scenario.store)
				if err != nil {
					test.Fatal(err)
				}
				credential, secret, err := access.Create(test.Context(), "alice", state.MCPMetadata{Name: "Legacy gate", Scopes: []string{scenario.scope}, ExpiresAt: now.Add(time.Hour)})
				if err != nil {
					test.Fatal(err)
				}
				client := gateClientLibrary(test, scenario.store, access, secret, scenario.library)
				if operation != "collect" {
					var queries atomic.Int64
					scenario.store.MCPTestInterceptCredentials(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
						return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
							value, err := next.Query(ctx, query)
							if err == nil && queries.Add(1) == 2 {
								if err := pause(ctx); err != nil {
									return nil, err
								}
							}
							return value, err
						})
					}))
				}
				type outcome struct {
					result *mcp.CallToolResult
					err    error
				}
				finished := make(chan outcome, 1)
				go func() {
					result, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: scenario.name, Arguments: scenario.args})
					finished <- outcome{result, err}
				}()
				select {
				case <-entered:
				case result := <-finished:
					test.Fatalf("call finished before barrier: %+v %v", result.result, result.err)
				case <-time.After(5 * time.Second):
					test.Fatal("authorization barrier not reached")
				}
				switch invalidation {
				case "revoke":
					_, err = access.Revoke(test.Context(), "alice", credential.ID, credential.Revision)
				case "rotate":
					_, _, err = access.Rotate(test.Context(), "alice", credential.ID, credential.Revision)
				case "expire":
					clock.Store(now.Add(time.Hour).UnixNano())
				case "scope":
					_, err = access.Update(test.Context(), "alice", credential.ID, credential.Revision, state.MCPMetadata{Name: "Read only", Scopes: []string{state.MCPCatalogRead}, ExpiresAt: credential.ExpiresAt})
				case "revision":
					_, err = access.Update(test.Context(), "alice", credential.ID, credential.Revision, state.MCPMetadata{Name: "Metadata changed", Scopes: []string{scenario.scope}, ExpiresAt: credential.ExpiresAt})
				}
				close(resume)
				if err != nil {
					test.Fatal(err)
				}
				result := <-finished
				if result.err == nil && !result.result.IsError {
					test.Fatal("stale authority committed")
				}
				scenario.assertUnchanged(test)
			})
		}
	}
}

func TestMCPHTTPCatalogAndPackExpiryAtCommitRollsBack(test *testing.T) {
	for _, operation := range []string{"collect", "create", "update", "delete"} {
		test.Run(operation, func(test *testing.T) {
			scenario := newLegacyGateScenario(test, operation, nil)
			now := time.Now()
			var clock atomic.Int64
			clock.Store(now.UnixNano())
			scenario.store.MCPTestClock(func() time.Time { return time.Unix(0, clock.Load()) })
			access, err := mcpaccess.NewService(scenario.store)
			if err != nil {
				test.Fatal(err)
			}
			_, secret, err := access.Create(test.Context(), "alice", state.MCPMetadata{Name: "Commit expiry", Scopes: []string{scenario.scope}, ExpiresAt: now.Add(time.Hour)})
			if err != nil {
				test.Fatal(err)
			}
			client := gateClientLibrary(test, scenario.store, access, secret, scenario.library)
			scenario.mutationHook(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					value, err := next.Mutate(ctx, mutation)
					if err == nil {
						clock.Store(now.Add(time.Hour).UnixNano())
					}
					return value, err
				})
			})
			result, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: scenario.name, Arguments: scenario.args})
			if err != nil || !result.IsError {
				test.Fatalf("expired mutation committed: %v", err)
			}
			scenario.assertUnchanged(test)
		})
	}
}

func TestMCPHTTPCollectExistingManualFallbackExpiryRollsBack(test *testing.T) {
	store, _ := packFixture(test)
	now := time.Now()
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	store.MCPTestClock(func() time.Time { return time.Unix(0, clock.Load()) })
	access, err := mcpaccess.NewService(store)
	if err != nil {
		test.Fatal(err)
	}
	_, secret, err := access.Create(test.Context(), "alice", state.MCPMetadata{Name: "Fallback expiry", Scopes: []string{state.MCPCatalogWrite}, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	var competitorID string
	service := legacyGateLibrary(test, store, func(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		snapshot := legacyGateSnapshot()
		entry, err := store.CollectRepository(test.Context(), "alice", reference, snapshot.Hash, snapshot.Skills)
		if err != nil {
			return nil, err
		}
		competitorID = entry.ID
		if err := store.MCPTestRepositoryManual(test.Context(), entry.ID, false); err != nil {
			return nil, err
		}
		store.MCPTestPublicationHook(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, mutation)
				if err == nil {
					clock.Store(now.Add(time.Hour).UnixNano())
				}
				return value, err
			})
		})
		return snapshot, nil
	})
	client := gateClientLibrary(test, store, access, secret, service)
	result, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: "catalog_collect", Arguments: mcpserver.CollectInput{Repository: "org/raced-gate"}})
	if err != nil || !result.IsError {
		test.Fatalf("expired manual fallback committed: %v", err)
	}
	entry, err := store.CollectedRepository(test.Context(), "alice", competitorID)
	if err != nil || entry.Manual {
		test.Fatalf("fallback bypassed commit gate: %+v %v", entry, err)
	}
}

func TestMCPHTTPWriteOnlyLegacyScopesStillWork(test *testing.T) {
	for _, operation := range []string{"collect", "create", "update", "delete"} {
		test.Run(operation, func(test *testing.T) {
			scenario := newLegacyGateScenario(test, operation, nil)
			access, err := mcpaccess.NewService(scenario.store)
			if err != nil {
				test.Fatal(err)
			}
			_, secret, err := access.Create(test.Context(), "alice", state.MCPMetadata{Name: "Write only", Scopes: []string{scenario.scope}, ExpiresAt: time.Now().Add(time.Hour)})
			if err != nil {
				test.Fatal(err)
			}
			client := gateClientLibrary(test, scenario.store, access, secret, scenario.library)
			result, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: scenario.name, Arguments: scenario.args})
			if err != nil || result.IsError {
				test.Fatalf("explicit write-only scope denied %s: %v %+v", operation, err, result)
			}
			if operation == "collect" {
				reference, err := repository.ParseReference("org/new-gate", "")
				if err != nil {
					test.Fatal(err)
				}
				entry, err := scenario.store.LookupCollectedRepository(test.Context(), "alice", reference)
				if err != nil || !entry.Manual || entry.SkillCount != 1 {
					test.Fatalf("collection missing: %v", err)
				}
				return
			}
			packs, err := scenario.store.ListPacks(test.Context(), "alice")
			if err != nil {
				test.Fatal(err)
			}
			switch operation {
			case "create":
				if len(packs) != 2 {
					test.Fatal("Pack was not created")
				}
			case "update":
				if len(packs) != 1 || packs[0].Revision != 2 || packs[0].Name != "After" || packs[0].ItemCount != 1 {
					test.Fatal("Pack update was not committed")
				}
			case "delete":
				if len(packs) != 0 {
					test.Fatal("Pack was not deleted")
				}
			}
		})
	}
}

func TestMCPHTTPCollectExistingManualFallbackStillWorks(test *testing.T) {
	store, _ := packFixture(test)
	access, err := mcpaccess.NewService(store)
	if err != nil {
		test.Fatal(err)
	}
	_, secret, err := access.Create(test.Context(), "alice", state.MCPMetadata{Name: "Manual fallback", Scopes: []string{state.MCPCatalogWrite}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	var competitor state.CollectedRepository
	service := legacyGateLibrary(test, store, func(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		snapshot := legacyGateSnapshot()
		entry, err := store.CollectRepository(test.Context(), "alice", reference, snapshot.Hash, snapshot.Skills)
		if err != nil {
			return nil, err
		}
		if err := store.MCPTestRepositoryManual(test.Context(), entry.ID, false); err != nil {
			return nil, err
		}
		competitor, err = store.CollectedRepository(test.Context(), "alice", entry.ID)
		return snapshot, err
	})
	client := gateClientLibrary(test, store, access, secret, service)
	result, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: "catalog_collect", Arguments: mcpserver.CollectInput{Repository: "org/raced-gate"}})
	if err != nil || result.IsError {
		test.Fatalf("valid manual fallback rejected: %v %+v", err, result)
	}
	entry, err := store.CollectedRepository(test.Context(), "alice", competitor.ID)
	if err != nil || !entry.Manual || !reflect.DeepEqual(competitor.Skills, entry.Skills) || competitor.ArchiveHash != entry.ArchiveHash {
		test.Fatalf("fallback changed skill identity/hash instead of only Manual: %v", err)
	}
}
