package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/githubcredential"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
	"github.com/ca-x/cognate/internal/server/ent/nativeconsumerchallenge"
)

func restoreCredentialsPool(testingCase *testing.T, path string) *sql.DB {
	testingCase.Helper()
	address := url.URL{Scheme: "file", Path: path}
	parameters := url.Values{}
	parameters.Add("_pragma", "foreign_keys(1)")
	parameters.Add("_pragma", "busy_timeout(0)")
	address.RawQuery = parameters.Encode()
	pool, err := sql.Open("sqlite3", address.String())
	if err != nil {
		testingCase.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	testingCase.Cleanup(func() {
		if err := pool.Close(); err != nil {
			testingCase.Error("fixture pool close")
		}
	})
	return pool
}

type restoreCredentialsTransport func(*http.Request) (*http.Response, error)

func (transport restoreCredentialsTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestRestoreCredentialsOldRefreshTickets(testingCase *testing.T) {
	for _, mode := range []string{"list", "source"} {
		testingCase.Run(mode, func(testingCase *testing.T) {
			fixture := restoreCredentialsSeed(testingCase)
			store := fixture.native.store
			var ticket ListSyncTicket
			var result ListSyncResult
			var source SourceRefreshTicket
			var client *github.ListsClient
			requests := 0
			if mode == "list" {
				credential, err := store.Credential(testingCase.Context(), "alice")
				if err != nil {
					testingCase.Fatal("active list credential fixture")
				}
				ticket, err = store.BeginListSync(testingCase.Context(), "alice", credential.Generation)
				if err != nil {
					testingCase.Fatal("actual list reservation fixture")
				}
				client, err = github.NewListsClient(credential.Token, &http.Client{Transport: restoreCredentialsTransport(func(*http.Request) (*http.Response, error) {
					requests++
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"viewer":{"id":"viewer-alice","lists":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}`))}, nil
				})})
				if err != nil {
					testingCase.Fatal("synthetic Lists client")
				}
				result, err = store.ReadListSync(testingCase.Context(), ticket, client)
				if err != nil || requests < 1 || result.fetchID == "" {
					testingCase.Fatalf("actual authenticated Lists result fixture: err=%v requests=%d authenticated=%t", err, requests, result.fetchID != "")
				}
			} else {
				if _, err := fixture.pool.ExecContext(testingCase.Context(), "UPDATE collected_repositories SET manual=0"); err != nil {
					testingCase.Fatal("star publication fixture")
				}
				if err := store.state.client.GitHubCredential.Update().SetSyncAppliedSequence(9).Exec(testingCase.Context()); err != nil {
					testingCase.Fatal("star applied sequence fixture")
				}
				if err := store.state.client.GitHubList.Update().SetItemsVerifiedAt(store.state.now()).Exec(testingCase.Context()); err != nil {
					testingCase.Fatal("star observation fixture")
				}
				var err error
				source, err = store.BeginRepositorySourceRefresh(testingCase.Context(), "alice", fixture.shares[0].skill.RepositoryID)
				if err != nil || store.CheckSourceRefresh(testingCase.Context(), source) != nil {
					testingCase.Fatalf("genuinely authorized source ticket fixture: %v", err)
				}
			}
			requestsBeforeRevocation := requests
			tx := restoreCredentialsBegin(testingCase, fixture.pool)
			if err := revokeRestoreCredentialsTx(testingCase.Context(), tx, store.state.now()); err != nil || tx.Commit() != nil {
				testingCase.Fatal("test coordinator revoke/commit")
			}
			if _, err := store.Credential(testingCase.Context(), "alice"); !errors.Is(err, ErrCredentialUnavailable) {
				testingCase.Fatal("retained credential became accessible or anonymous")
			}
			users, err := store.ListSyncUsers(testingCase.Context(), 0, 16)
			if err != nil || len(users) != 0 {
				testingCase.Fatal("retired owners remain scheduled as ready")
			}
			if mode == "list" {
				called := false
				err := store.CommitListSync(testingCase.Context(), result, func(context.Context, *ent.Tx, github.ListsSnapshot) error {
					called = true
					return nil
				})
				if !errors.Is(err, ErrSyncSuperseded) || called {
					testingCase.Fatal("old authenticated ListSyncResult reached its mutation callback")
				}
				if _, err := store.ReadListSync(testingCase.Context(), ticket, client); !errors.Is(err, ErrSyncSuperseded) || requests != requestsBeforeRevocation {
					testingCase.Fatal("old ListSyncTicket reached network after revocation")
				}
			} else {
				before := restoreCredentialsSnapshot(testingCase, fixture.pool)
				if err := store.CheckSourceRefresh(testingCase.Context(), source); err == nil {
					testingCase.Fatal("old source ticket still authorized")
				}
				if _, err := store.ApplySourceRefresh(testingCase.Context(), source, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/retained", "d")}); err == nil {
					testingCase.Fatal("old source ticket published new business data")
				}
				if after := restoreCredentialsSnapshot(testingCase, fixture.pool); !reflect.DeepEqual(before, after) {
					testingCase.Fatal("denied source publication changed database rows")
				}
			}
		})
	}
}

func TestRestoreCredentialsOldNativeGrants(testingCase *testing.T) {
	for _, protocol := range []string{"configuration", "codex"} {
		for _, phase := range []string{"pending", "consumed", "response-lost"} {
			testingCase.Run(protocol+"/"+phase, func(testingCase *testing.T) {
				calls := 0
				var fixture nativeFixture
				var consume func() (NativeMaterialization, error)
				if protocol == "configuration" {
					flow := newNativeConfigurationFlow(testingCase)
					flow.authorize(testingCase)
					fixture = flow.fixture
					if _, err := fixture.store.PreflightNativeConfigurationConsume(testingCase.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof); err != nil {
						testingCase.Fatal("real signed configuration grant was not authorized before revocation")
					}
					consume = func() (NativeMaterialization, error) {
						return fixture.store.ConsumeNativeConfigurationGrant(testingCase.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof, func(_ context.Context, _ ConfigurationAsset, payload ConfigurationPayload, _ nativesessionauth.ApplyFields, _ string, _ int64) (NativeMaterialization, error) {
							calls++
							body := payload.Reveal()
							defer clear(body)
							return NewNativeMaterialization(body)
						})
					}
				} else {
					flow := newCodexStateFlow(testingCase)
					flow.prepare(testingCase)
					flow.issue(testingCase)
					fixture = flow.base.fixture
					if _, err := fixture.store.PreflightNativeCodexConsume(testingCase.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof); err != nil {
						testingCase.Fatal("real signed Codex grant was not authorized before revocation")
					}
					consume = func() (NativeMaterialization, error) {
						return fixture.store.ConsumeNativeCodexGrant(testingCase.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof, func(_ context.Context, _ ConfigurationAsset, payload ConfigurationPayload, _ nativesessionauth.CodexApply, _ string, _ int64) (NativeMaterialization, error) {
							calls++
							body := payload.Reveal()
							defer clear(body)
							return NewNativeMaterialization(body)
						})
					}
				}
				if phase != "pending" {
					var lost error
					if phase == "response-lost" {
						lost = errors.New("synthetic lost consumption acknowledgement")
					}
					response, err := func() (NativeMaterialization, error) {
						materialized, err := consume()
						if err != nil || lost == nil {
							return materialized, err
						}
						materialized.Destroy()
						return NativeMaterialization{}, lost
					}()
					if err != lost || response.present() != (lost == nil) || calls != 1 {
						testingCase.Fatal("actual first consumption or test-side acknowledgement loss failed")
					}
					response.Destroy()
					if lost != nil {
						testingCase.Log("test-side loss after successful consumption; the caller receives an error and no materialization")
					}
				}
				pool := restoreCredentialsPool(testingCase, fixture.path)
				tx := restoreCredentialsBegin(testingCase, pool)
				if err := revokeRestoreCredentialsTx(testingCase.Context(), tx, fixture.store.state.now()); err != nil || tx.Commit() != nil {
					testingCase.Fatal("test coordinator native revoke/commit")
				}
				beforeCalls := calls
				response, err := consume()
				defer response.Destroy()
				if !errors.Is(err, ErrNativeUnauthorized) || response.present() || calls != beforeCalls {
					testingCase.Fatal("old native grant revived or reached the materializer")
				}
			})
		}
	}
}

func TestRestoreCredentialsCommitUnknownCoordinator(testingCase *testing.T) {
	fixture := newNativeFixture(testingCase)
	owner, err := findUser(testingCase.Context(), fixture.store.state.client, "alice")
	if err != nil {
		testingCase.Fatal("commit fixture owner")
	}
	if err := fixture.store.state.client.MCPCredential.Create().SetUserID(owner.ID).SetPublicID(rand.Text()).SetName("synthetic").SetTokenHash(bytes.Repeat([]byte{66}, 32)).SetScopes([]string{MCPCatalogRead}).SetCreatedAt(fixture.store.state.now()).SetExpiresAt(fixture.store.state.now().Add(time.Hour)).Exec(testingCase.Context()); err != nil {
		testingCase.Fatal("commit fixture MCP row")
	}
	fault := errors.New("PRIVATE actual commit succeeded but response lost")
	faults := &restoreCredentialsFaults{commitErr: fault}
	pool := restoreCredentialsFaultPool(testingCase, fixture.path, faults)
	tx := restoreCredentialsBegin(testingCase, pool)
	if err := revokeRestoreCredentialsTx(testingCase.Context(), tx, fixture.store.state.now()); err != nil {
		testingCase.Fatal(err)
	}
	restoreCredentialsNoOwnership(testingCase, faults)
	published := false
	coordinatorOutcome := func() error {
		if err := tx.Commit(); err != nil {
			if err != fault {
				testingCase.Fatal("unexpected test coordinator commit failure")
			}
			return ErrRestoreUncertain
		}
		published = true
		return nil
	}()
	if coordinatorOutcome != ErrRestoreUncertain || published || faults.commits.Load() != 1 {
		testingCase.Fatal("test coordinator failed to withhold publication after actual commit then error")
	}
	observer := restoreCredentialsPool(testingCase, fixture.path)
	var revision int64
	var revoked bool
	if observer.QueryRowContext(testingCase.Context(), "SELECT revision,revoked_at IS NOT NULL FROM mcp_credentials").Scan(&revision, &revoked) != nil || revision != 2 || !revoked || restoreCredentialsCount(testingCase, observer, "sessions") != 0 {
		testingCase.Fatal("actual commit negative control did not take effect")
	}
	if faults.execs.Load() != 8 || faults.commits.Load() != 1 {
		testingCase.Fatal("test-side coordinator replayed an uncertain operation")
	}
	testingCase.Log("test-side coordinator evidence only: commit was applied despite error; no production publisher/reconciler is wired")
}

type restoreCredentialsRecords struct {
	native   nativeFixture
	pool     *sql.DB
	parents  [][32]byte
	children []struct {
		credential NativeConsumerCredential
		operation  []byte
	}
	shares []struct {
		id    string
		hash  []byte
		skill PackSkill
		rev   int64
	}
	mcpHashes [][]byte
	mcpGrant  MCPGrant
	assets    []struct{ owner, id string }
}

func restoreCredentialsSeed(testingCase *testing.T) restoreCredentialsRecords {
	testingCase.Helper()
	fixture := newNativeFixture(testingCase)
	result := restoreCredentialsRecords{native: fixture}
	for ownerIndex, ownerKey := range []string{"alice", "bob"} {
		parent := fixture
		if ownerKey != "alice" {
			parent.parent, parent.digest = nativeTestParent(testingCase, fixture.store, ownerKey, fixture.store.state.now().Add(time.Hour))
		}
		result.parents = append(result.parents, parent.digest)
		for index := range 10 {
			child, preparation, grant := nativeSyntheticPair(testingCase, parent)
			preparation.Phase = nativeapplypreparation.Phase([]string{"bound-with-grant", "expired", "retired"}[index%3])
			if index >= 8 {
				if err := fixture.store.state.client.NativeMaterializationGrant.DeleteOneID(grant.ID).Exec(testingCase.Context()); err != nil {
					testingCase.Fatal("grant-free preparation fixture")
				}
				preparation.Phase = nativeapplypreparation.Phase([]string{"patch-ready", "bound-no-secret"}[index-8])
			}
			if err := fixture.store.state.client.NativeApplyPreparation.UpdateOneID(preparation.ID).SetPhase(preparation.Phase).SetCharge(nativePreparationCharge(preparation)).Exec(testingCase.Context()); err != nil {
				testingCase.Fatal("terminal preparation fixture")
			}
			consumed := int64(0)
			if index&1 != 0 {
				consumed = grant.IssuedMs + 1
			}
			if index < 8 {
				if err := fixture.store.state.client.NativeMaterializationGrant.UpdateOneID(grant.ID).SetConsumedMs(consumed).SetRetired(index&2 != 0).SetExpired(index&4 != 0).Exec(testingCase.Context()); err != nil {
					testingCase.Fatal("terminal grant fixture")
				}
			}
			result.children = append(result.children, struct {
				credential NativeConsumerCredential
				operation  []byte
			}{NativeConsumerCredential{nativeWrap([32]byte(child.TokenHash))}, bytes.Clone(child.OperationID)})
		}
		owner, err := findUser(testingCase.Context(), fixture.store.state.client, ownerKey)
		if err != nil {
			testingCase.Fatal("retained owner fixture")
		}
		if err := fixture.store.state.client.LocalAccount.Create().SetUserID(owner.ID).SetUsername("restore-" + ownerKey).SetPasswordHash(bytes.Repeat([]byte{byte(ownerIndex + 71)}, 32)).Exec(testingCase.Context()); err != nil {
			testingCase.Fatal("retained password fixture")
		}
		if err := fixture.store.state.client.OIDCIdentity.Create().SetUserID(owner.ID).SetIssuer("https://synthetic-issuer.invalid").SetSubject("restore-" + ownerKey).Exec(testingCase.Context()); err != nil {
			testingCase.Fatal("retained OIDC fixture")
		}
		reference, err := repository.ParseReference("org/"+ownerKey, "")
		if err != nil {
			testingCase.Fatal("retained repository reference")
		}
		collected, err := fixture.store.CollectRepository(testingCase.Context(), ownerKey, reference, strings.Repeat("a", 64), []repository.ArchivedSkill{refreshSkill("skills/retained", "b")})
		if err != nil {
			testingCase.Fatal("retained repository fixture")
		}
		publication, err := fixture.store.ReadRepositoryRefresh(testingCase.Context(), ownerKey, collected.ID)
		if err != nil || len(publication.Repository.Skills) != 1 {
			testingCase.Fatal("retained repository publication fixture")
		}
		collected = publication.Repository
		pack, err := fixture.store.CreatePack(testingCase.Context(), ownerKey, PackDraft{Name: "retained", Items: []PackSelection{{SkillID: collected.Skills[0].ID}}})
		if err != nil {
			testingCase.Fatal("retained pack fixture")
		}
		pack, err = fixture.store.Pack(testingCase.Context(), ownerKey, pack.ID)
		if err != nil || len(pack.Items) != 1 {
			testingCase.Fatal("retained pack publication fixture")
		}
		category, err := fixture.store.CreateCategory(testingCase.Context(), ownerKey, "Retained")
		if err != nil {
			testingCase.Fatal("retained category fixture")
		}
		if _, err := fixture.store.SetSkillCategories(testingCase.Context(), ownerKey, collected.Skills[0].ID, []string{category.ID}); err != nil {
			testingCase.Fatal("retained category membership")
		}
		for index, kind := range []ConfigurationKind{ConfigurationKindProvider, ConfigurationKindMCP} {
			asset, err := fixture.store.CreateConfigurationAsset(testingCase.Context(), ownerKey, ConfigurationAssetDraft{AgentID: "codex", Kind: kind, Name: fmt.Sprintf("retained-%d", index), SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic-configuration-" + ownerKey))})
			if err != nil {
				testingCase.Fatal("retained configuration fixture")
			}
			result.assets = append(result.assets, struct{ owner, id string }{ownerKey, asset.ID})
			if index == 0 {
				var packID, assetID int
				packRow, err := fixture.store.state.client.Pack.Query().Where().All(testingCase.Context())
				if err != nil {
					testingCase.Fatal("retained pack row")
				}
				for _, row := range packRow {
					if row.PublicID == pack.ID {
						packID = row.ID
					}
				}
				assets, err := fixture.store.state.client.ConfigurationAsset.Query().All(testingCase.Context())
				if err != nil {
					testingCase.Fatal("retained asset row")
				}
				for _, row := range assets {
					if row.PublicID == asset.ID {
						assetID = row.ID
					}
				}
				if err := fixture.store.state.client.PackConfigurationAttachment.Create().SetPublicID(rand.Text()).SetPackID(packID).SetConfigurationAssetID(assetID).SetAssetRevision(1).SetTargetID("synthetic-target").SetOrdinal(0).Exec(testingCase.Context()); err != nil {
					testingCase.Fatal("retained attachment fixture")
				}
			}
		}
		token, err := github.NewToken("synthetic-github-"+ownerKey, []string{"repo"})
		if err != nil {
			testingCase.Fatal("retained GitHub token fixture")
		}
		generation, err := fixture.store.PutCredential(testingCase.Context(), ownerKey, "viewer-"+ownerKey, "", token)
		if err != nil {
			testingCase.Fatal("retained encrypted credential fixture")
		}
		if err := fixture.store.state.client.GitHubCredential.Update().Where(githubcredential.UserIDEQ(owner.ID)).SetRefreshAttempt("synthetic-refresh").SetSyncSequence(9).SetSyncAppliedSequence(7).SetSyncFetchID("synthetic-fetch").SetCollectionCursor(4).Exec(testingCase.Context()); err != nil {
			testingCase.Fatal("retired scheduling fixture")
		}
		list, err := fixture.store.state.client.GitHubList.Create().SetUserID(owner.ID).SetViewerID("viewer-" + ownerKey).SetListKey("retained").SetCredentialGeneration(generation).SetName("private").SetSlug("private").SetPrivate(true).SetUpdatedAt(fixture.store.state.now()).SetLastAddedAt(fixture.store.state.now()).SetVerifiedAt(fixture.store.state.now()).SetSubscribed(true).SetVisible(true).Save(testingCase.Context())
		if err != nil {
			testingCase.Fatal("retained private list fixture")
		}
		remote, err := fixture.store.state.client.GitHubRepository.Create().SetUserID(owner.ID).SetViewerID("viewer-" + ownerKey).SetRepositoryKey("retained").SetNameWithOwner("org/" + ownerKey).SetPrivate(true).Save(testingCase.Context())
		if err != nil {
			testingCase.Fatal("retained private repository fixture")
		}
		if err := fixture.store.state.client.GitHubMembership.Create().SetListID(list.ID).SetRepositoryID(remote.ID).SetVisible(true).Exec(testingCase.Context()); err != nil {
			testingCase.Fatal("retained private membership fixture")
		}
		if err := fixture.store.state.client.RepositoryPrivateHistory.Create().SetUserID(owner.ID).SetNameWithOwner("org/" + ownerKey).Exec(testingCase.Context()); err != nil {
			testingCase.Fatal("retained private history fixture")
		}
		for index := range 3 {
			shareID := rand.Text()
			hash := bytes.Repeat([]byte{byte(10 + ownerIndex*3 + index)}, 32)
			if _, err := fixture.store.CreatePackShare(testingCase.Context(), ownerKey, pack.ID, shareID, hash, nil); err != nil {
				testingCase.Fatal("share fixture")
			}
			result.shares = append(result.shares, struct {
				id    string
				hash  []byte
				skill PackSkill
				rev   int64
			}{shareID, hash, pack.Items[0], pack.Revision})
			mcpHash := bytes.Repeat([]byte{byte(30 + ownerIndex*3 + index)}, 32)
			mcp, err := fixture.store.CreateMCPCredential(testingCase.Context(), ownerKey, mcpHash, MCPMetadata{Name: "synthetic", Scopes: []string{MCPCatalogRead}, ExpiresAt: fixture.store.state.now().Add(time.Hour)})
			if err != nil {
				testingCase.Fatal("MCP fixture")
			}
			result.mcpHashes = append(result.mcpHashes, mcpHash)
			if ownerIndex == 0 && index == 0 {
				result.mcpGrant, err = fixture.store.AuthorizeMCP(testingCase.Context(), mcpHash, MCPCatalogRead)
				if err != nil {
					testingCase.Fatal("old MCP grant fixture")
				}
			}
			row, err := fixture.store.state.client.MCPCredential.Query().All(testingCase.Context())
			if err != nil {
				testingCase.Fatal("MCP audit owner fixture")
			}
			for _, entry := range row {
				if entry.PublicID == mcp.ID {
					if err := fixture.store.state.client.MCPAudit.Create().SetCredentialID(entry.ID).SetAction("synthetic").SetResource("").SetOutcome("success").SetCreatedAt(fixture.store.state.now()).Exec(testingCase.Context()); err != nil {
						testingCase.Fatal("MCP retained audit fixture")
					}
					if index == 1 {
						if err := fixture.store.state.client.MCPCredential.UpdateOneID(entry.ID).SetExpiresAt(fixture.store.state.now().Add(-time.Second)).Exec(testingCase.Context()); err != nil {
							testingCase.Fatal("expired MCP fixture")
						}
					}
					if index == 2 {
						if err := fixture.store.state.client.MCPCredential.UpdateOneID(entry.ID).SetRevokedAt(fixture.store.state.now().Add(-time.Second)).Exec(testingCase.Context()); err != nil {
							testingCase.Fatal("already-revoked MCP fixture")
						}
					}
				}
			}
		}
	}
	for _, ownerKey := range []string{"alice", "bob"} {
		parent := fixture
		if ownerKey == "bob" {
			parent.parent = OwnerSessionCredential{nativeWrap(result.parents[1])}
			parent.digest = result.parents[1]
		}
		for index := range 3 {
			start := nativeTestStart()
			start.ExpectedOwner = ownerKey
			_, _ = rand.Read(start.OperationID)
			challenge, err := fixture.store.BeginNativeConsumer(testingCase.Context(), parent.parent, start)
			if err != nil {
				testingCase.Fatal("remaining challenge fixture")
			}
			if index == 1 {
				if err := fixture.store.state.client.NativeConsumerChallenge.Update().Where(nativeconsumerchallenge.PublicIDEQ(challenge.ID())).SetUsed(true).Exec(testingCase.Context()); err != nil {
					testingCase.Fatal("used challenge fixture")
				}
			}
		}
	}
	result.pool = restoreCredentialsPool(testingCase, fixture.path)
	if _, err := result.pool.ExecContext(testingCase.Context(), "UPDATE native_consumer_challenges SET issued_ms=?,expires_ms=? WHERE id IN (SELECT min(id) FROM native_consumer_challenges GROUP BY user_id)", fixture.store.state.now().Add(-time.Minute).UnixMilli(), fixture.store.state.now().Add(-time.Second).UnixMilli()); err != nil {
		testingCase.Fatal("expired challenge fixture")
	}
	if _, err := result.pool.ExecContext(testingCase.Context(), "UPDATE pack_shares SET revoked_at=? WHERE id IN (SELECT id FROM pack_shares ORDER BY id LIMIT 1)", fixture.store.state.now().Add(-time.Second)); err != nil {
		testingCase.Fatal("already-revoked share fixture")
	}
	if _, err := result.pool.ExecContext(testingCase.Context(), "UPDATE pack_shares SET expires_at=? WHERE id IN (SELECT id FROM pack_shares ORDER BY id DESC LIMIT 1)", fixture.store.state.now().Add(-time.Second)); err != nil {
		testingCase.Fatal("expired share fixture")
	}
	return result
}

func TestRestoreCredentialsAllOwnersAndRetainedAAD(testingCase *testing.T) {
	for _, status := range []githubcredential.Status{githubcredential.StatusReady, githubcredential.StatusRefreshing, githubcredential.StatusNeedsReauthorization} {
		testingCase.Run(string(status), func(testingCase *testing.T) {
			fixture := restoreCredentialsSeed(testingCase)
			store := fixture.native.store
			if err := store.state.client.GitHubCredential.Update().SetStatus(status).Exec(testingCase.Context()); err != nil {
				testingCase.Fatal("credential status fixture")
			}
			before := restoreCredentialsSnapshot(testingCase, fixture.pool)
			for _, child := range fixture.children {
				if _, err := store.AuthenticateNativeConsumer(testingCase.Context(), child.credential, "https://cognate.example", child.operation); err != nil {
					testingCase.Fatal("old native credentials must be genuinely valid before revocation")
				}
			}
			credentials, err := store.state.client.GitHubCredential.Query().WithUser().All(testingCase.Context())
			if err != nil || len(credentials) != 2 {
				testingCase.Fatal("two retained encrypted credential rows required")
			}
			instant := time.Date(2031, 2, 3, 4, 5, 6, 123456789, time.FixedZone("synthetic", 5*60*60))
			tx := restoreCredentialsBegin(testingCase, fixture.pool)
			if err := revokeRestoreCredentialsTx(testingCase.Context(), tx, instant); err != nil {
				testingCase.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				testingCase.Fatal("test coordinator commit")
			}
			if err := fixture.pool.Close(); err != nil {
				testingCase.Fatal("test coordinator pool close")
			}
			pool := restoreCredentialsPool(testingCase, fixture.native.path)
			after := restoreCredentialsSnapshot(testingCase, pool)
			mutated := []string{"native_materialization_grants", "native_apply_preparations", "native_consumer_challenges", "native_consumer_sessions", "sessions", "pack_shares", "mcp_credentials", "git_hub_credentials"}
			for table, rows := range before {
				if !slices.Contains(mutated, table) && !reflect.DeepEqual(rows, after[table]) {
					testingCase.Fatalf("retained table changed: %s", table)
				}
			}
			for _, table := range mutated[:5] {
				if len(before[table]) == 0 || len(after[table]) != 0 {
					testingCase.Fatalf("auth table was empty before or not fully retired: %s", table)
				}
			}
			for _, table := range []string{"pack_shares", "mcp_credentials"} {
				var mismatch int
				if pool.QueryRowContext(testingCase.Context(), "SELECT count(*) FROM "+table+" WHERE revoked_at IS NOT ?", instant.UTC()).Scan(&mismatch) != nil || mismatch != 0 {
					testingCase.Fatal("all rows did not receive the same canonical operation time")
				}
			}
			var wrongRevision int
			if pool.QueryRowContext(testingCase.Context(), "SELECT count(*) FROM mcp_credentials WHERE revision<>2 OR typeof(revision)<>'integer'").Scan(&wrongRevision) != nil || wrongRevision != 0 {
				testingCase.Fatal("active, expired and revoked MCP rows must advance exactly once")
			}
			for _, previous := range credentials {
				current, err := store.state.client.GitHubCredential.Get(testingCase.Context(), previous.ID)
				if err != nil || current.Generation != previous.Generation || current.KeyID != previous.KeyID || current.ViewerID != previous.ViewerID || current.ClientID != previous.ClientID || !bytes.Equal(current.Ciphertext, previous.Ciphertext) || current.SyncSequence != 9 || current.SyncAppliedSequence != 7 || current.Status != githubcredential.StatusNeedsReauthorization || current.RefreshAttempt != "" || current.SyncFetchID != "" || current.CollectionCursor != 0 {
					testingCase.Fatal("retained AAD/ciphertext/counters changed or scheduling authority survived")
				}
				if _, err := store.decryptCredential(previous.Edges.User.UserKey, current); err != nil {
					testingCase.Fatal("retained credential no longer decrypts under original AAD")
				}
				current.Generation = "synthetic-wrong-generation"
				if _, err := store.decryptCredential(previous.Edges.User.UserKey, current); err == nil {
					testingCase.Fatal("negative control accepted changed generation with unchanged ciphertext")
				}
			}
			for _, asset := range fixture.assets {
				payload, err := store.ReadConfigurationAsset(testingCase.Context(), asset.owner, asset.id)
				if err != nil || !bytes.Equal(payload.Reveal(), []byte("synthetic-configuration-"+asset.owner)) {
					testingCase.Fatal("retained configuration no longer decrypts")
				}
			}
			for _, parent := range fixture.parents {
				if _, _, err := store.SessionUser(testingCase.Context(), parent[:], store.state.now()); !errors.Is(err, ErrSessionInvalid) {
					testingCase.Fatal("old owner session survived")
				}
			}
			for _, child := range fixture.children {
				if _, err := store.AuthenticateNativeConsumer(testingCase.Context(), child.credential, "https://cognate.example", child.operation); !errors.Is(err, ErrNativeUnauthorized) {
					testingCase.Fatal("old native consumer survived")
				}
			}
			for _, share := range fixture.shares {
				if _, err := store.SharedPack(testingCase.Context(), share.id, share.hash); !errors.Is(err, ErrPackShareUnavailable) {
					testingCase.Fatal("old manifest share survived")
				}
				if err := store.AuthorizePackSkill(testingCase.Context(), share.id, share.hash, share.skill.SkillID, share.skill.ContentHash, share.rev); !errors.Is(err, ErrPackShareUnavailable) {
					testingCase.Fatal("old download share survived")
				}
			}
			for _, hash := range fixture.mcpHashes {
				if _, err := store.AuthorizeMCP(testingCase.Context(), hash, MCPCatalogRead); !errors.Is(err, ErrMCPUnauthorized) {
					testingCase.Fatal("old MCP token survived")
				}
			}
			oldContext := WithMCPAuthorization(testingCase.Context(), fixture.mcpGrant, MCPCatalogRead)
			entTx, err := store.state.client.Tx(oldContext)
			if err != nil {
				testingCase.Fatal("old MCP context transaction")
			}
			gateError := store.checkMCPTransaction(oldContext, entTx, "alice", MCPCatalogRead)
			rollbackError := entTx.Rollback()
			if !errors.Is(gateError, ErrMCPUnauthorized) || rollbackError != nil {
				testingCase.Fatal("old MCP authorization context survived")
			}
			freshHash := bytes.Repeat([]byte{94}, 32)
			if err := store.CreatePasswordSession(testingCase.Context(), "restore-alice", bytes.Repeat([]byte{71}, 32), freshHash, store.state.now(), store.state.now().Add(time.Hour)); err != nil {
				testingCase.Fatal("retained local account cannot create a fresh session")
			}
			if _, _, err := store.SessionUser(testingCase.Context(), freshHash, store.state.now()); err != nil {
				testingCase.Fatal("fresh session unavailable")
			}
			if owner, err := store.CreateOIDCSession(testingCase.Context(), OIDCSessionPlan{Issuer: "https://synthetic-issuer.invalid", Subject: "restore-alice", TokenHash: bytes.Repeat([]byte{95}, 32), IssuedAt: store.state.now(), ExpiresAt: store.state.now().Add(time.Hour)}); err != nil || owner != "alice" {
				testingCase.Fatal("retained OIDC identity cannot create a fresh session")
			}
			newMCPHash := bytes.Repeat([]byte{96}, 32)
			if _, err := store.CreateMCPCredential(testingCase.Context(), "alice", newMCPHash, MCPMetadata{Name: "fresh", Scopes: []string{MCPCatalogRead}, ExpiresAt: store.state.now().Add(time.Hour)}); err != nil {
				testingCase.Fatal("fresh explicit MCP authorization failed")
			}
			if _, err := store.AuthorizeMCP(testingCase.Context(), newMCPHash, MCPCatalogRead); err != nil {
				testingCase.Fatal("fresh MCP authorization unavailable")
			}
			packs, err := store.state.client.Pack.Query().Order(ent.Asc("id")).All(testingCase.Context())
			if err != nil || len(packs) != 2 {
				testingCase.Fatal("retained packs missing")
			}
			newShareID, newShareHash := rand.Text(), bytes.Repeat([]byte{97}, 32)
			if _, err := store.CreatePackShare(testingCase.Context(), "alice", packs[0].PublicID, newShareID, newShareHash, nil); err != nil {
				testingCase.Fatal("fresh explicit share authorization failed")
			}
			if _, err := store.SharedPack(testingCase.Context(), newShareID, newShareHash); err != nil {
				testingCase.Fatal("fresh share unavailable")
			}
		})
	}
}

func TestRestoreCredentialsInputs(testingCase *testing.T) {
	for _, kind := range []string{"nil-context", "nil-transaction", "zero-time", "canceled", "deadline", "completed"} {
		testingCase.Run(kind, func(testingCase *testing.T) {
			fixture := newNativeFixture(testingCase)
			faults := &restoreCredentialsFaults{}
			pool := restoreCredentialsFaultPool(testingCase, fixture.path, faults)
			tx := restoreCredentialsBegin(testingCase, pool)
			argument := tx
			ctx := testingCase.Context()
			instant := time.Now().UTC()
			expected := error(ErrRestoreInvalid)
			switch kind {
			case "nil-context":
				ctx = nil
			case "nil-transaction":
				argument = nil
			case "zero-time":
				instant = time.Time{}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(testingCase.Context())
				cancel()
				expected = context.Canceled
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(testingCase.Context(), time.Unix(1, 0))
				defer cancel()
				expected = context.DeadlineExceeded
			case "completed":
				if tx.Rollback() != nil {
					testingCase.Fatal("completed transaction fixture")
				}
				expected = ErrRestoreUncertain
			}
			if err := revokeRestoreCredentialsTx(ctx, argument, instant); err != expected || faults.execs.Load() != 0 || faults.queries.Load() != 0 {
				testingCase.Fatalf("invalid/canceled input escaped fixed early refusal: %v", err)
			}
			if kind != "completed" {
				restoreCredentialsNoOwnership(testingCase, faults)
			} else if faults.begins.Load() != 1 || faults.commits.Load() != 0 || faults.rollbacks.Load() != 1 || faults.closes.Load() != 0 {
				testingCase.Fatal("completed borrowed transaction was replaced or retried")
			}
		})
	}
}

func TestRestoreCredentialsEmptyAuthState(testingCase *testing.T) {
	fixture := newNativeFixture(testingCase)
	if err := fixture.store.RevokeSession(testingCase.Context(), fixture.digest[:]); err != nil {
		testingCase.Fatal("empty auth fixture")
	}
	observer := restoreCredentialsPool(testingCase, fixture.path)
	before := restoreCredentialsSnapshot(testingCase, observer)
	faults := &restoreCredentialsFaults{}
	pool := restoreCredentialsFaultPool(testingCase, fixture.path, faults)
	tx := restoreCredentialsBegin(testingCase, pool)
	if err := revokeRestoreCredentialsTx(testingCase.Context(), tx, time.Now().UTC()); err != nil {
		testingCase.Fatal(err)
	}
	restoreCredentialsNoOwnership(testingCase, faults)
	if faults.execs.Load() != 8 || tx.Commit() != nil {
		testingCase.Fatal("empty admitted database must complete under caller commit")
	}
	if !reflect.DeepEqual(before, restoreCredentialsSnapshot(testingCase, observer)) {
		testingCase.Fatal("empty revocation changed retained state")
	}
}

type restoreCredentialsFaults struct {
	begins    atomic.Int32
	commits   atomic.Int32
	rollbacks atomic.Int32
	closes    atomic.Int32
	execs     atomic.Int32
	queries   atomic.Int32
	readers   atomic.Int32
	readClose atomic.Int32
	affected  []int64
	afterExec func(int) error
	queryFail func(int) error
	readFail  func(int) error
	readValue func(int, []driver.Value)
	closeFail func(int) error
	commitErr error
}

type restoreCredentialsConnector struct {
	base   driver.Driver
	name   string
	faults *restoreCredentialsFaults
}

func (connector restoreCredentialsConnector) Driver() driver.Driver { return connector.base }

func (connector restoreCredentialsConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection, err := connector.base.Open(connector.name)
	if err != nil {
		return nil, err
	}
	return &restoreCredentialsConnection{Conn: connection, faults: connector.faults}, nil
}

type restoreCredentialsConnection struct {
	driver.Conn
	faults *restoreCredentialsFaults
}

func (connection *restoreCredentialsConnection) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	connection.faults.begins.Add(1)
	tx, err := connection.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &restoreCredentialsTransaction{Tx: tx, faults: connection.faults}, nil
}

func (connection *restoreCredentialsConnection) Close() error {
	connection.faults.closes.Add(1)
	return connection.Conn.Close()
}

func (connection *restoreCredentialsConnection) ExecContext(ctx context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	result, err := connection.Conn.(driver.ExecerContext).ExecContext(ctx, query, arguments)
	if err != nil {
		return result, err
	}
	if strings.HasPrefix(query, "DELETE ") || strings.HasPrefix(query, "UPDATE ") {
		affected, err := result.RowsAffected()
		if err != nil {
			return result, err
		}
		connection.faults.affected = append(connection.faults.affected, affected)
		position := int(connection.faults.execs.Add(1))
		if connection.faults.afterExec != nil {
			return result, connection.faults.afterExec(position)
		}
	}
	return result, nil
}

func (connection *restoreCredentialsConnection) QueryContext(ctx context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	position := int(connection.faults.queries.Add(1))
	if connection.faults.queryFail != nil {
		if err := connection.faults.queryFail(position); err != nil {
			return nil, err
		}
	}
	rows, err := connection.Conn.(driver.QueryerContext).QueryContext(ctx, query, arguments)
	if err != nil {
		return nil, err
	}
	connection.faults.readers.Add(1)
	return &restoreCredentialsRows{Rows: rows, faults: connection.faults, position: position}, nil
}

type restoreCredentialsTransaction struct {
	driver.Tx
	faults *restoreCredentialsFaults
}

func (transaction *restoreCredentialsTransaction) Commit() error {
	transaction.faults.commits.Add(1)
	if err := transaction.Tx.Commit(); err != nil {
		return err
	}
	return transaction.faults.commitErr
}

func (transaction *restoreCredentialsTransaction) Rollback() error {
	transaction.faults.rollbacks.Add(1)
	return transaction.Tx.Rollback()
}

type restoreCredentialsRows struct {
	driver.Rows
	faults   *restoreCredentialsFaults
	position int
}

func (rows *restoreCredentialsRows) Next(output []driver.Value) error {
	if rows.faults.readFail != nil {
		if err := rows.faults.readFail(rows.position); err != nil {
			return err
		}
	}
	err := rows.Rows.Next(output)
	if err == nil && rows.faults.readValue != nil {
		rows.faults.readValue(rows.position, output)
	}
	return err
}

func (rows *restoreCredentialsRows) Close() error {
	rows.faults.readClose.Add(1)
	if err := rows.Rows.Close(); err != nil {
		return err
	}
	if rows.faults.closeFail != nil {
		return rows.faults.closeFail(rows.position)
	}
	return nil
}

func restoreCredentialsFaultPool(testingCase *testing.T, path string, faults *restoreCredentialsFaults) *sql.DB {
	testingCase.Helper()
	base := restoreCredentialsPool(testingCase, path)
	address := url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=foreign_keys(1)&_pragma=busy_timeout(0)"}
	pool := sql.OpenDB(restoreCredentialsConnector{base: base.Driver(), name: address.String(), faults: faults})
	pool.SetMaxOpenConns(1)
	testingCase.Cleanup(func() {
		if err := pool.Close(); err != nil {
			testingCase.Error("fault pool close")
		}
	})
	return pool
}

func restoreCredentialsNoOwnership(testingCase *testing.T, faults *restoreCredentialsFaults) {
	testingCase.Helper()
	if faults.begins.Load() != 1 || faults.commits.Load() != 0 || faults.rollbacks.Load() != 0 || faults.closes.Load() != 0 || faults.readers.Load() != faults.readClose.Load() {
		testingCase.Fatal("helper finalized borrowed ownership or left an owned read unclosed")
	}
}

func restoreCredentialsSnapshot(testingCase *testing.T, pool *sql.DB) map[string][]string {
	testingCase.Helper()
	rows, err := pool.QueryContext(testingCase.Context(), "SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name")
	if err != nil {
		testingCase.Fatal("snapshot table query")
	}
	var tables []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			testingCase.Fatal("snapshot table scan")
		}
		tables = append(tables, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		testingCase.Fatal("snapshot table close")
	}
	result := make(map[string][]string, len(tables))
	for _, table := range tables {
		rows, err := pool.QueryContext(testingCase.Context(), `SELECT * FROM "`+strings.ReplaceAll(table, `"`, `""`)+`"`)
		if err != nil {
			testingCase.Fatal("snapshot row query")
		}
		columns, err := rows.Columns()
		if err != nil {
			testingCase.Fatal("snapshot columns")
		}
		result[table] = []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if rows.Scan(pointers...) != nil {
				testingCase.Fatal("snapshot row scan")
			}
			for index, value := range values {
				if instant, ok := value.(time.Time); ok {
					values[index] = instant.UTC().Format(time.RFC3339Nano)
				}
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				testingCase.Fatal("snapshot encoding")
			}
			result[table] = append(result[table], string(encoded))
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			testingCase.Fatal("snapshot read close")
		}
		slices.Sort(result[table])
	}
	return result
}

func TestRestoreCredentialsRollbackEachStatement(testingCase *testing.T) {
	for _, canceled := range []bool{false, true} {
		for step := 1; step <= 8; step++ {
			testingCase.Run(fmt.Sprintf("cancel=%t/after=%d", canceled, step), func(testingCase *testing.T) {
				fixture := restoreCredentialsSeed(testingCase)
				observer := fixture.pool
				before := restoreCredentialsSnapshot(testingCase, observer)
				ctx, cancel := context.WithCancel(testingCase.Context())
				defer cancel()
				faults := &restoreCredentialsFaults{}
				faults.afterExec = func(position int) error {
					if position != step {
						return nil
					}
					if canceled {
						cancel()
						return context.Canceled
					}
					return errors.New("PRIVATE mutation acknowledged then failed /synthetic/path")
				}
				pool := restoreCredentialsFaultPool(testingCase, fixture.native.path, faults)
				tx := restoreCredentialsBegin(testingCase, pool)
				err := revokeRestoreCredentialsTx(ctx, tx, time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC))
				expected := error(ErrRestoreUncertain)
				if canceled {
					expected = context.Canceled
				}
				if err != expected || faults.execs.Load() != int32(step) {
					testingCase.Fatalf("post-statement failure or cancellation not preserved: %v", err)
				}
				if len(faults.affected) != step || faults.affected[step-1] == 0 {
					testingCase.Fatal("the faulted statement did not mutate any real fixture rows")
				}
				restoreCredentialsNoOwnership(testingCase, faults)
				if err := tx.Rollback(); err != nil || faults.rollbacks.Load() != 1 {
					testingCase.Fatal("caller lost actual rollback ownership")
				}
				if after := restoreCredentialsSnapshot(testingCase, observer); !reflect.DeepEqual(before, after) {
					testingCase.Fatal("real caller rollback failed to restore all database rows")
				}
			})
		}
	}
}

func TestRestoreCredentialsPostconditions(testingCase *testing.T) {
	for _, scenario := range []struct{ name, trigger string }{
		{"native", "CREATE TEMP TRIGGER retain_session AFTER DELETE ON sessions BEGIN INSERT INTO sessions(id,token_hash,issued_at,expires_at,user_id) VALUES(old.id,old.token_hash,old.issued_at,old.expires_at,old.user_id); END"},
		{"share-time", "CREATE TEMP TRIGGER retain_share AFTER UPDATE ON pack_shares BEGIN UPDATE pack_shares SET revoked_at=NULL WHERE id=new.id; END"},
		{"mcp-time", "CREATE TEMP TRIGGER retain_mcp_time AFTER UPDATE ON mcp_credentials BEGIN UPDATE mcp_credentials SET revoked_at=NULL WHERE id=new.id; END"},
		{"mcp-revision", "CREATE TEMP TRIGGER retain_mcp_revision AFTER UPDATE ON mcp_credentials BEGIN UPDATE mcp_credentials SET revision=old.revision WHERE id=new.id; END"},
		{"github", "CREATE TEMP TRIGGER retain_github AFTER UPDATE ON git_hub_credentials BEGIN UPDATE git_hub_credentials SET status='ready' WHERE id=new.id; END"},
		{"foreign-key", ""},
	} {
		testingCase.Run(scenario.name, func(testingCase *testing.T) {
			fixture := restoreCredentialsSeed(testingCase)
			before := restoreCredentialsSnapshot(testingCase, fixture.pool)
			faults := &restoreCredentialsFaults{}
			pool := restoreCredentialsFaultPool(testingCase, fixture.native.path, faults)
			if scenario.trigger != "" {
				if _, err := pool.ExecContext(testingCase.Context(), scenario.trigger); err != nil {
					testingCase.Fatal("postcondition fault setup")
				}
			}
			tx := restoreCredentialsBegin(testingCase, pool)
			if scenario.name == "foreign-key" {
				if _, err := tx.ExecContext(testingCase.Context(), "PRAGMA defer_foreign_keys=ON"); err != nil {
					testingCase.Fatal("deferred FK fault setup")
				}
				if _, err := tx.ExecContext(testingCase.Context(), "INSERT INTO oidc_identities(issuer,subject,user_id) VALUES('https://synthetic-invalid.invalid','missing-owner',999999)"); err != nil {
					testingCase.Fatal("actual deferred FK fault setup")
				}
			}
			if err := revokeRestoreCredentialsTx(testingCase.Context(), tx, time.Now().UTC()); err != ErrRestoreInvalid {
				testingCase.Fatalf("successful DML with violated postcondition was accepted: %v", err)
			}
			if faults.execs.Load() != 8 {
				testingCase.Fatal("postcondition fixture did not execute all eight real DML")
			}
			restoreCredentialsNoOwnership(testingCase, faults)
			if tx.Rollback() != nil || !reflect.DeepEqual(before, restoreCredentialsSnapshot(testingCase, fixture.pool)) {
				testingCase.Fatal("caller rollback after a failed postcondition did not restore the full database")
			}
		})
	}
}

func TestRestoreCredentialsReadFailures(testingCase *testing.T) {
	for _, kind := range []string{"query", "cancel-query", "read", "scan", "close", "cancel-close", "cancel-read-close", "empty-result"} {
		for position := 1; position <= 7; position++ {
			testingCase.Run(fmt.Sprintf("%s/read=%d", kind, position), func(testingCase *testing.T) {
				fixture := newNativeFixture(testingCase)
				ctx, cancel := context.WithCancel(testingCase.Context())
				defer cancel()
				fault := errors.New("PRIVATE read/close uncertainty path=/synthetic credential=sentinel")
				var injections atomic.Int32
				faults := &restoreCredentialsFaults{}
				if kind == "query" || kind == "cancel-query" {
					faults.queryFail = func(current int) error {
						if current == position {
							injections.Add(1)
							if kind == "cancel-query" {
								cancel()
								return context.Canceled
							}
							return fault
						}
						return nil
					}
				}
				if kind == "read" || kind == "cancel-read-close" || kind == "empty-result" {
					faults.readFail = func(current int) error {
						if current == position {
							injections.Add(1)
							if kind == "empty-result" {
								return io.EOF
							}
							if kind == "cancel-read-close" {
								return context.Canceled
							}
							return fault
						}
						return nil
					}
				}
				if kind == "scan" {
					faults.readValue = func(current int, output []driver.Value) {
						if current == position {
							injections.Add(1)
							output[0] = "PRIVATE invalid scalar"
						}
					}
				}
				if strings.Contains(kind, "close") {
					faults.closeFail = func(current int) error {
						if current == position {
							injections.Add(1)
							if strings.HasPrefix(kind, "cancel-") {
								cancel()
							}
							return fault
						}
						return nil
					}
				}
				pool := restoreCredentialsFaultPool(testingCase, fixture.path, faults)
				tx := restoreCredentialsBegin(testingCase, pool)
				err := revokeRestoreCredentialsTx(ctx, tx, time.Now().UTC())
				expectedError := error(ErrRestoreUncertain)
				if kind == "cancel-query" {
					expectedError = context.Canceled
				}
				if kind == "empty-result" {
					expectedError = ErrRestoreInvalid
				}
				if err != expectedError {
					testingCase.Fatalf("read/close uncertainty was masked or exposed: %v", err)
				}
				expected := int32(1)
				if kind == "cancel-read-close" {
					expected = 2
				}
				if injections.Load() != expected {
					testingCase.Fatal("read/close failure was not actually exercised")
				}
				restoreCredentialsNoOwnership(testingCase, faults)
				if err := tx.Rollback(); err != nil {
					testingCase.Fatal("caller rollback after read failure")
				}
			})
		}
	}
}

func TestRestoreCredentialsForeignKeysRequired(testingCase *testing.T) {
	fixture := newNativeFixture(testingCase)
	pool := restoreCredentialsPool(testingCase, fixture.path)
	if _, err := pool.ExecContext(testingCase.Context(), "PRAGMA foreign_keys=OFF"); err != nil {
		testingCase.Fatal("foreign key fixture")
	}
	tx := restoreCredentialsBegin(testingCase, pool)
	if err := revokeRestoreCredentialsTx(testingCase.Context(), tx, time.Now().UTC()); err != ErrRestoreInvalid {
		testingCase.Fatalf("foreign_keys=OFF must refuse before writes, got %v", err)
	}
	if restoreCredentialsCount(testingCase, tx, "sessions") != 1 {
		testingCase.Fatal("FK refusal mutated the borrowed transaction")
	}
}

func TestRestoreCredentialsRevisionBounds(testingCase *testing.T) {
	for _, scenario := range []struct {
		name     string
		value    any
		accepted bool
		expected int64
	}{
		{"one", int64(1), true, 2},
		{"last-increment", int64(math.MaxInt64 - 1), true, math.MaxInt64},
		{"exhausted", int64(math.MaxInt64), false, 0},
		{"zero", int64(0), false, 0},
		{"negative", int64(-1), false, 0},
		{"real", 1.5, false, 0},
		{"text", "PRIVATE invalid revision", false, 0},
	} {
		testingCase.Run(scenario.name, func(testingCase *testing.T) {
			fixture := newNativeFixture(testingCase)
			owner, err := findUser(testingCase.Context(), fixture.store.state.client, "alice")
			if err != nil {
				testingCase.Fatal("revision fixture owner")
			}
			if err := fixture.store.state.client.MCPCredential.Create().SetUserID(owner.ID).SetPublicID("restore-revision-boundary").SetName("synthetic").SetTokenHash(bytes.Repeat([]byte{51}, 32)).SetScopes([]string{"skills:read"}).SetCreatedAt(fixture.store.state.now()).SetExpiresAt(fixture.store.state.now().Add(time.Hour)).Exec(testingCase.Context()); err != nil {
				testingCase.Fatal("revision fixture credential")
			}
			pool := restoreCredentialsPool(testingCase, fixture.path)
			if _, err := pool.ExecContext(testingCase.Context(), "UPDATE mcp_credentials SET revision=?", scenario.value); err != nil {
				testingCase.Fatal("revision fixture value")
			}
			tx := restoreCredentialsBegin(testingCase, pool)
			err = revokeRestoreCredentialsTx(testingCase.Context(), tx, time.Now().UTC())
			if !scenario.accepted {
				if err != ErrRestoreInvalid || restoreCredentialsCount(testingCase, tx, "sessions") != 1 {
					testingCase.Fatalf("invalid revision was admitted or partially mutated: %v", err)
				}
				return
			}
			if err != nil {
				testingCase.Fatalf("valid revision refused: %v", err)
			}
			var revision int64
			var storage string
			if err := tx.QueryRowContext(testingCase.Context(), "SELECT revision,typeof(revision) FROM mcp_credentials").Scan(&revision, &storage); err != nil || revision != scenario.expected || storage != "integer" {
				testingCase.Fatal("revision increment lost exact SQLite integer semantics")
			}
		})
	}
}

func restoreCredentialsBegin(testingCase *testing.T, pool *sql.DB) *sql.Tx {
	testingCase.Helper()
	tx, err := pool.BeginTx(testingCase.Context(), nil)
	if err != nil {
		testingCase.Fatal("fixture transaction begin")
	}
	testingCase.Cleanup(func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			testingCase.Error("fixture transaction rollback")
		}
	})
	return tx
}

func restoreCredentialsCount(testingCase *testing.T, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, table string) int64 {
	testingCase.Helper()
	var count int64
	if err := query.QueryRowContext(testingCase.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		testingCase.Fatal("fixture count")
	}
	return count
}

func TestRestoreCredentialsBorrowedTransaction(testingCase *testing.T) {
	fixture := newNativeFixture(testingCase)
	nativeSyntheticPair(testingCase, fixture)
	pool := restoreCredentialsPool(testingCase, fixture.path)
	tx := restoreCredentialsBegin(testingCase, pool)
	instant := time.Date(2031, 2, 3, 4, 5, 6, 123456789, time.UTC)
	if err := revokeRestoreCredentialsTx(testingCase.Context(), tx, instant); err != nil {
		testingCase.Fatalf("revoke borrowed transaction: %v", err)
	}
	for _, table := range []string{"native_materialization_grants", "native_apply_preparations", "native_consumer_challenges", "native_consumer_sessions", "sessions"} {
		if count := restoreCredentialsCount(testingCase, tx, table); count != 0 {
			testingCase.Fatalf("borrowed transaction still contains %s rows=%d", table, count)
		}
	}
	if _, _, err := fixture.store.SessionUser(testingCase.Context(), fixture.digest[:], fixture.store.state.now()); err != nil {
		testingCase.Fatal("helper finalized or exposed the uncommitted transaction")
	}
	if err := tx.Rollback(); err != nil {
		testingCase.Fatal("caller no longer owns rollback")
	}
	if _, _, err := fixture.store.SessionUser(testingCase.Context(), fixture.digest[:], fixture.store.state.now()); err != nil {
		testingCase.Fatal("caller rollback did not restore parent session")
	}
	if count := restoreCredentialsCount(testingCase, pool, "native_materialization_grants"); count != 1 {
		testingCase.Fatal("caller rollback did not restore descendant grant")
	}
}
