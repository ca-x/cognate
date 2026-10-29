package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/githubcredential"
	"github.com/ca-x/cognate/internal/server/ent/githubrepository"
	"github.com/ca-x/cognate/internal/server/ent/repositoryprivatehistory"
	"github.com/ca-x/cognate/internal/skill"
)

func TestPrivateSourceAccessPolicyAndIssuer(test *testing.T) {
	store, peer := refreshStores(test)
	reference := repository.Reference{NameWithOwner: "org/source"}
	public, err := store.BeginSourceAccess(test.Context(), "alice", reference)
	if err != nil || public.Authenticated() {
		test.Fatalf("unconnected public source: %v", err)
	}
	if err := peer.CheckSourceAccess(test.Context(), public); err == nil {
		test.Fatal("proof crossed Store boundary")
	}
	if err := store.CheckSourceAccess(test.Context(), SourceAccessTicket{}); err == nil {
		test.Fatal("zero proof accepted")
	}
	token, err := github.NewToken("synthetic-private-source", nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := peer.PutCredential(test.Context(), "alice", "viewer", "", token); err != nil {
		test.Fatal(err)
	}
	if err := store.CheckSourceAccess(test.Context(), public); err == nil {
		test.Fatal("public proof survived new connection")
	}
	private, err := store.BeginSourceAccess(test.Context(), "alice", reference)
	if err != nil || !private.Authenticated() {
		test.Fatalf("connected source did not require authentication: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", private, private), "synthetic-private-source") {
		test.Fatal("proof formatting disclosed credentials")
	}
	ctx := store.WithSourceAccess(test.Context(), private)
	if _, err := store.CollectRepository(ctx, "alice", reference, strings.Repeat("a", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")}); !errors.Is(err, ErrSourceAccessUnavailable) {
		test.Fatalf("authenticated collection without metadata accepted: %v", err)
	}
	if err := peer.state.client.GitHubCredential.Update().SetStatus(githubcredential.StatusNeedsReauthorization).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if _, err := store.BeginSourceAccess(test.Context(), "alice", reference); err == nil {
		test.Fatal("invalid connection downgraded to public")
	}
}

func TestPrivateSourceExpiryComparesInstantsNotTimezonePointers(test *testing.T) {
	store, _, base, generation, now := sourceStarFixture(test)
	material, err := json.Marshal(map[string]any{"version": 1, "access": "synthetic-offset-private", "expires_at": now.Add(time.Hour).In(time.FixedZone("fixture-offset", 83*60))})
	if err != nil {
		test.Fatal(err)
	}
	token, err := github.DecodeStoredToken(material)
	clear(material)
	if err != nil {
		test.Fatal(err)
	}
	newGeneration, err := store.PutCredential(test.Context(), "alice", "viewer", generation, token)
	if err != nil {
		test.Fatal(err)
	}
	if err := store.state.client.GitHubList.Update().SetCredentialGeneration(newGeneration).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := store.state.client.GitHubRepository.Update().SetPrivate(true).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
	if err := store.CheckSourceRefresh(test.Context(), ticket); err != nil {
		test.Fatalf("unchanged expiry instant was rejected: %v", err)
	}
	if _, err := store.BindSourceMetadata(test.Context(), ticket.Access(), repository.GitHubMetadata{NodeID: "remote", NameWithOwner: "org/source", Private: true}); err != nil {
		test.Fatalf("unchanged source proof expiry was rejected: %v", err)
	}
}

func TestPrivateSourceBusyRetryRechecksCredential(test *testing.T) {
	store, peer, base, generation, _ := sourceStarFixture(test)
	if err := store.state.client.GitHubRepository.Update().SetPrivate(true).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
	access, err := store.BindSourceMetadata(test.Context(), ticket.Access(), repository.GitHubMetadata{NodeID: "remote", NameWithOwner: "org/source", Private: true})
	if err != nil {
		test.Fatal(err)
	}
	queries := 0
	var competingError error
	store.state.client.CollectedRepository.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			queries++
			if err == nil && queries == 1 {
				token, _ := github.NewToken("synthetic-private-busy-rotation", nil)
				_, competingError = peer.PutCredential(test.Context(), "alice", "viewer", generation, token)
			}
			return value, err
		})
	}))
	if _, err := store.ApplySourceRefresh(store.WithSourceAccess(test.Context(), access), ticket, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")}); err == nil {
		test.Fatal("BUSY retry retained old private authority")
	}
	if competingError != nil || queries < 2 {
		test.Fatalf("real competing commit and fresh retry missing: queries=%d error=%v", queries, competingError)
	}
	assertRefreshUnchanged(test, peer, base)
}

func TestPrivateSourceUnknownCommitIsNotReplayed(test *testing.T) {
	store, peer, base, _, _ := sourceStarFixture(test)
	if err := store.state.client.GitHubRepository.Update().SetPrivate(true).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
	access, err := store.BindSourceMetadata(test.Context(), ticket.Access(), repository.GitHubMetadata{NodeID: "remote", NameWithOwner: "org/source", Private: true})
	if err != nil {
		test.Fatal(err)
	}
	receiptLost := errors.New("synthetic-commit-receipt-lost")
	commits := 0
	store.state.client.CollectedSkill.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			tx, err := mutation.(*ent.CollectedSkillMutation).Tx()
			if err != nil {
				return nil, err
			}
			tx.OnCommit(func(next ent.Committer) ent.Committer {
				return ent.CommitFunc(func(ctx context.Context, transaction *ent.Tx) error {
					if err := next.Commit(ctx, transaction); err != nil {
						return err
					}
					commits++
					return receiptLost
				})
			})
			return next.Mutate(ctx, mutation)
		})
	})
	ctx := store.WithSourceAccess(test.Context(), access)
	result, err := store.ApplySourceRefresh(ctx, ticket, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")})
	if !errors.Is(err, receiptLost) || result.Repository.ID != "" || commits != 1 {
		test.Fatalf("uncertain COMMIT was hidden or replayed: commits=%d error=%v", commits, err)
	}
	readback, err := peer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil || readback.Version.ArchiveHash != strings.Repeat("c", 64) || readback.Version.PublicationRevision != base.Version.PublicationRevision+1 {
		test.Fatal("readback did not expose actual committed outcome")
	}
	if _, err := store.ApplySourceRefresh(ctx, ticket, strings.Repeat("e", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "f")}); !errors.Is(err, ErrRepositoryRefreshConflict) || commits != 1 {
		test.Fatal("old candidate rebased after uncertain COMMIT")
	}
	if count, err := peer.state.client.RepositoryPrivateHistory.Query().Count(test.Context()); err != nil || count != 1 {
		test.Fatalf("actual uncertain commit did not persist one history receipt: count=%d error=%v", count, err)
	}
}

func TestPrivateSourceHistorySurvivesViewerSwitchAndRestart(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	database := filepath.Join(directory, "server.db")
	key := make([]byte, 32)
	store, err := Open(test.Context(), database, key)
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	token, _ := github.NewToken("synthetic-history-owner", nil)
	generation, err := store.PutCredential(test.Context(), "alice", "first-viewer", "", token)
	if err != nil {
		test.Fatal(err)
	}
	reference := repository.Reference{NameWithOwner: "org/history"}
	collect := func(private bool) {
		ticket, err := store.BeginSourceAccess(test.Context(), "alice", reference)
		if err != nil {
			test.Fatal(err)
		}
		ticket, err = store.BindSourceMetadata(test.Context(), ticket, repository.GitHubMetadata{NodeID: "history-node", NameWithOwner: reference.NameWithOwner, Private: private})
		if err != nil {
			test.Fatal(err)
		}
		if _, err := store.CollectRepository(store.WithSourceAccess(test.Context(), ticket), "alice", reference, strings.Repeat("a", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")}); err != nil {
			test.Fatal(err)
		}
	}
	collect(true)
	if _, err := store.PutCredential(test.Context(), "alice", "second-viewer", generation, token); err != nil {
		test.Fatal(err)
	}
	collect(false)
	if _, err := store.state.client.GitHubCredential.Delete().Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), database, key)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.BeginSourceAccess(test.Context(), "alice", reference); !errors.Is(err, ErrSourceAccessUnavailable) {
		test.Fatalf("cross-viewer persisted history downgraded: %v", err)
	}
	if entries, err := reopened.ListCollectedRepositories(test.Context(), "alice"); err != nil || len(entries) != 1 || !entries[0].Manual {
		test.Fatal("restart lost original manual collection")
	}
}

func TestPrivateSourceStarRejectsChangedAuthority(test *testing.T) {
	for _, invalidation := range []string{"generation", "viewer", "key", "revoked", "sequence", "applied", "fetch-id", "private", "unselected", "hidden", "expired", "stale-observation", "stale-cas", "expiry-at-commit"} {
		test.Run(invalidation, func(test *testing.T) {
			store, peer, base, generation, now := sourceStarFixture(test)
			if err := store.state.client.GitHubRepository.Update().SetPrivate(true).Exec(test.Context()); err != nil {
				test.Fatal(err)
			}
			ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
			access, err := store.BindSourceMetadata(test.Context(), ticket.Access(), repository.GitHubMetadata{NodeID: "remote", NameWithOwner: "org/source", Private: true})
			if err != nil {
				test.Fatal(err)
			}
			pack := refreshPack(test, store, PackSelection{SkillID: base.Repository.Skills[0].ID})
			switch invalidation {
			case "generation", "viewer":
				token, _ := github.NewToken("synthetic-rotated-private", nil)
				viewer := "viewer"
				if invalidation == "viewer" {
					viewer = "other-viewer"
				}
				_, err = peer.PutCredential(test.Context(), "alice", viewer, generation, token)
			case "key":
				err = peer.state.client.GitHubCredential.Update().SetKeyID("other-key").Exec(test.Context())
			case "revoked":
				err = peer.state.client.GitHubCredential.Update().SetStatus(githubcredential.StatusNeedsReauthorization).Exec(test.Context())
			case "sequence":
				err = peer.state.client.GitHubCredential.Update().AddSyncSequence(1).Exec(test.Context())
			case "applied":
				err = peer.state.client.GitHubCredential.Update().SetSyncAppliedSequence(0).Exec(test.Context())
			case "fetch-id":
				err = peer.state.client.GitHubCredential.Update().SetSyncFetchID("other-fetch").Exec(test.Context())
			case "private":
				err = peer.state.client.GitHubRepository.Update().SetPrivate(false).Exec(test.Context())
			case "unselected":
				err = peer.SetListSubscription(test.Context(), "alice", "selected", false)
			case "hidden":
				err = peer.state.client.GitHubMembership.Update().SetVisible(false).Exec(test.Context())
			case "expired":
				store.state.now = func() time.Time { return now.Add(time.Hour) }
			case "stale-observation":
				store.state.now = func() time.Time { return now.Add(store.state.observationMaxAge) }
			case "stale-cas":
				_, err = peer.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, strings.Repeat("e", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "f")})
			case "expiry-at-commit":
				store.state.client.Pack.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
						value, err := next.Mutate(ctx, mutation)
						store.state.now = func() time.Time { return now.Add(time.Hour) }
						return value, err
					})
				})
			}
			if err != nil {
				test.Fatal(err)
			}
			if _, err := store.ApplySourceRefresh(store.WithSourceAccess(test.Context(), access), ticket, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")}); err == nil {
				test.Fatal("changed private authority committed")
			}
			if invalidation == "stale-cas" {
				current, err := peer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
				if err != nil || current.Version.ArchiveHash != strings.Repeat("e", 64) {
					test.Fatal("stale candidate overwrote winner")
				}
			} else {
				assertRefreshUnchanged(test, peer, base, pack)
			}
		})
	}
}

func TestPrivateSourceMCPGrantStillRequired(test *testing.T) {
	store, peer, base, _, now := sourceStarFixture(test)
	if err := store.state.client.GitHubRepository.Update().SetPrivate(true).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
	access, err := store.BindSourceMetadata(test.Context(), ticket.Access(), repository.GitHubMetadata{NodeID: "remote", NameWithOwner: "org/source", Private: true})
	if err != nil {
		test.Fatal(err)
	}
	digest := sha256.Sum256([]byte("synthetic-private-mcp"))
	credential, err := store.CreateMCPCredential(test.Context(), "alice", digest[:], MCPMetadata{Name: "private-test", Scopes: []string{MCPCatalogWrite}, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	grant, err := store.AuthorizeMCP(test.Context(), digest[:], MCPCatalogWrite)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := peer.RevokeMCPCredential(test.Context(), "alice", credential.ID, credential.Revision); err != nil {
		test.Fatal(err)
	}
	ctx := WithMCPAuthorization(store.WithSourceAccess(test.Context(), access), grant, MCPCatalogWrite)
	if _, err := store.ApplySourceRefresh(ctx, ticket, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")}); err == nil {
		test.Fatal("GitHub authority replaced revoked MCP authority")
	}
	assertRefreshUnchanged(test, peer, base)
}

func TestPrivateSourceCannotSubstituteFetchedAuthority(test *testing.T) {
	store, peer, base, _, _ := sourceStarFixture(test)
	if err := store.state.client.GitHubRepository.Update().SetPrivate(true).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
	other, err := store.BeginSourceAccess(test.Context(), "alice", repository.Reference{NameWithOwner: "org/other"})
	if err != nil {
		test.Fatal(err)
	}
	other, err = store.BindSourceMetadata(test.Context(), other, repository.GitHubMetadata{NodeID: "other-node", NameWithOwner: "org/other", Private: true})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.ApplySourceRefresh(store.WithSourceAccess(test.Context(), other), ticket, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")}); err == nil {
		test.Fatal("fetched authority for another repository was substituted")
	}
	assertRefreshUnchanged(test, peer, base)
}

func TestPrivateSourceFirstCollectExpiryAtCommitRollsBackMetadata(test *testing.T) {
	store, peer, _, _, now := sourceStarFixture(test)
	reference := repository.Reference{NameWithOwner: "org/new-secret"}
	ticket, err := store.BeginSourceAccess(test.Context(), "alice", reference)
	if err != nil {
		test.Fatal(err)
	}
	ticket, err = store.BindSourceMetadata(test.Context(), ticket, repository.GitHubMetadata{NodeID: "new-node", NameWithOwner: reference.NameWithOwner, Private: true})
	if err != nil {
		test.Fatal(err)
	}
	mutations := 0
	store.state.client.CollectedSkill.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, mutation)
			mutations++
			store.state.now = func() time.Time { return now.Add(time.Hour) }
			return value, err
		})
	})
	if _, err := store.CollectRepository(store.WithSourceAccess(test.Context(), ticket), "alice", reference, strings.Repeat("a", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")}); err == nil {
		test.Fatal("expiry at COMMIT accepted first collection")
	}
	if mutations != 1 {
		test.Fatal("negative control never reached skill write")
	}
	if _, err := peer.LookupCollectedRepository(test.Context(), "alice", reference); !errors.Is(err, ErrRepositoryNotFound) {
		test.Fatalf("failed transaction retained repository: %v", err)
	}
	if exists, err := peer.state.client.GitHubRepository.Query().Where(githubrepository.RepositoryKeyEQ("new-node")).Exist(test.Context()); err != nil || exists {
		test.Fatal("failed transaction retained metadata")
	}
	if exists, err := peer.state.client.RepositoryPrivateHistory.Query().Where(repositoryprivatehistory.NameWithOwnerEQ(reference.NameWithOwner)).Exist(test.Context()); err != nil || exists {
		test.Fatal("failed transaction retained private history")
	}
}

func TestPrivateSourceMetadataPersistsAndCannotDowngrade(test *testing.T) {
	store, peer := refreshStores(test)
	reference := repository.Reference{NameWithOwner: "org/secret"}
	token, _ := github.NewToken("synthetic-metadata-owner", nil)
	if _, err := store.PutCredential(test.Context(), "alice", "viewer", "", token); err != nil {
		test.Fatal(err)
	}
	ticket, err := store.BeginSourceAccess(test.Context(), "alice", reference)
	if err != nil {
		test.Fatal(err)
	}
	ticket, err = store.BindSourceMetadata(test.Context(), ticket, repository.GitHubMetadata{NodeID: "private-node", NameWithOwner: "org/secret", Private: true})
	if err != nil {
		test.Fatal(err)
	}
	ctx := store.WithSourceAccess(test.Context(), ticket)
	if _, err := store.CollectRepository(ctx, "bob", reference, strings.Repeat("a", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")}); err == nil {
		test.Fatal("proof authorized another owner")
	}
	entry, err := store.CollectRepository(ctx, "alice", reference, strings.Repeat("a", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")})
	if err != nil {
		test.Fatal(err)
	}
	remote, err := peer.state.client.GitHubRepository.Query().Where(githubrepository.RepositoryKeyEQ("private-node")).Only(test.Context())
	if err != nil || !remote.Private {
		test.Fatalf("private metadata was not atomic: %v", err)
	}
	if count, err := peer.state.client.GitHubMembership.Query().Count(test.Context()); err != nil || count != 0 {
		test.Fatal("manual collect invented membership")
	}
	if _, err := peer.state.client.GitHubCredential.Delete().Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if _, err := peer.BeginSourceAccess(test.Context(), "alice", reference); err == nil {
		test.Fatal("persisted private history downgraded after credential deletion")
	}
	if _, err := store.CollectRepository(ctx, "alice", reference, entry.ArchiveHash, []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")}); err == nil {
		test.Fatal("idempotent collect ignored revoked proof")
	}
	if public, err := peer.BeginSourceAccess(test.Context(), "bob", reference); err != nil || public.Authenticated() {
		test.Fatalf("private history crossed owner boundary: %v", err)
	}
}

func TestPrivateSourceListProofFencesPrivateBit(test *testing.T) {
	store, peer, base, _, _ := sourceStarFixture(test)
	if err := store.state.client.GitHubRepository.Update().SetPrivate(true).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
	if !ticket.Access().Authenticated() {
		test.Fatal("private list refresh has no authenticated proof")
	}
	if err := peer.state.client.GitHubRepository.Update().SetPrivate(false).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := store.CheckSourceRefresh(test.Context(), ticket); err == nil {
		test.Fatal("private ticket survived private-bit change")
	}
}

func TestIndependentS1HistorySurvivesPublicListsObservation(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "independent-owner"); err != nil {
		test.Fatal(err)
	}
	token, err := github.NewToken("INDEPENDENT-S1-SYNTHETIC-TOKEN", []string{"repo"})
	if err != nil {
		test.Fatal(err)
	}
	generation, err := store.PutCredential(test.Context(), "independent-owner", "viewer", "", token)
	if err != nil {
		test.Fatal(err)
	}
	reference := repository.Reference{NameWithOwner: "org/source"}
	ticket, err := store.BeginSourceAccess(test.Context(), "independent-owner", reference)
	if err != nil {
		test.Fatal(err)
	}
	ticket, err = store.BindSourceMetadata(test.Context(), ticket, repository.GitHubMetadata{NodeID: "source-node", NameWithOwner: reference.NameWithOwner, Private: true})
	if err != nil {
		test.Fatal(err)
	}
	ctx := store.WithSourceAccess(test.Context(), ticket)
	_, err = store.CollectRepository(ctx, "independent-owner", reference, strings.Repeat("a", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "independent", Description: "independent fixture", Path: "skills/independent"}, ContentHash: strings.Repeat("b", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var query struct {
			Variables struct {
				ID string `json:"id"`
			} `json:"variables"`
		}
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			test.Error("independent_graphql_query_invalid")
			writer.WriteHeader(400)
			return
		}
		if query.Variables.ID == "" {
			_, _ = io.WriteString(writer, `{"data":{"viewer":{"id":"viewer","lists":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[{"id":"selected","name":"selected","slug":"selected","isPrivate":false,"updatedAt":"2026-01-01T00:00:00Z","lastAddedAt":"2026-01-01T00:00:00Z"}]}}}}`)
		} else {
			_, _ = io.WriteString(writer, `{"data":{"node":{"__typename":"UserList","id":"selected","name":"selected","slug":"selected","isPrivate":false,"updatedAt":"2026-01-01T00:00:00Z","lastAddedAt":"2026-01-01T00:00:00Z","user":{"id":"viewer"},"items":{"totalCount":1,"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[{"__typename":"Repository","id":"source-node","nameWithOwner":"org/source","isPrivate":false,"databaseId":null}]}}}}`)
		}
	}))
	defer endpoint.Close()
	transport := &http.Client{Transport: independentHistoryTransport(func(request *http.Request) (*http.Response, error) {
		local := request.Clone(request.Context())
		local.URL = request.URL.Clone()
		local.URL.Scheme, local.URL.Host = "http", strings.TrimPrefix(endpoint.URL, "http://")
		return endpoint.Client().Transport.RoundTrip(local)
	})}
	client, err := github.NewListsClient(token, transport)
	if err != nil {
		test.Fatal(err)
	}
	synchronize := func() {
		ticket, err := store.BeginListSync(test.Context(), "independent-owner", generation)
		if err != nil {
			test.Fatal(err)
		}
		result, err := store.ReadListSync(test.Context(), ticket, client)
		if err != nil {
			test.Fatal(err)
		}
		if err := store.ApplyListSync(test.Context(), result); err != nil {
			test.Fatal(err)
		}
	}
	synchronize()
	if err := store.SetListSubscription(test.Context(), "independent-owner", "selected", true); err != nil {
		test.Fatal(err)
	}
	synchronize()
	if count, err := store.state.client.GitHubMembership.Query().Count(test.Context()); err != nil || count != 1 {
		test.Fatal("independent_real_public_list_observation_not_applied")
	}
	if _, err := store.state.client.GitHubCredential.Delete().Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	proof, err := store.BeginSourceAccess(test.Context(), "independent-owner", reference)
	if !errors.Is(err, ErrSourceAccessUnavailable) {
		test.Errorf("independent_historical_private_no_anonymous_downgrade=false authenticated=%t rejected=%t", proof.Authenticated(), err != nil)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(context.Background(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.BeginSourceAccess(test.Context(), "independent-owner", reference); !errors.Is(err, ErrSourceAccessUnavailable) {
		test.Error("independent_historical_private_after_restart=false")
	}
}

type independentHistoryTransport func(*http.Request) (*http.Response, error)

func (transport independentHistoryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestPrivateSourceHistoryMigrationBackfillsOnlyPrivate(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	databasePath := filepath.Join(directory, "server.db")
	key := make([]byte, 32)
	store, err := Open(test.Context(), databasePath, key)
	if err != nil {
		test.Fatal(err)
	}
	for _, owner := range []string{"alice", "bob"} {
		if err := store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
	}
	alice, err := findUser(test.Context(), store.state.client, "alice")
	if err != nil {
		test.Fatal(err)
	}
	bob, err := findUser(test.Context(), store.state.client, "bob")
	if err != nil {
		test.Fatal(err)
	}
	var records []*ent.GitHubRepositoryCreate
	for index := range 260 {
		records = append(records, store.state.client.GitHubRepository.Create().SetUserID(alice.ID).SetViewerID("old-viewer").SetRepositoryKey(fmt.Sprintf("node-%d", index)).SetNameWithOwner(fmt.Sprintf("Org/Legacy-%03d", index)).SetPrivate(true))
	}
	records = append(records,
		store.state.client.GitHubRepository.Create().SetUserID(alice.ID).SetViewerID("second-viewer").SetRepositoryKey("duplicate-node").SetNameWithOwner("org/legacy-000").SetPrivate(true),
		store.state.client.GitHubRepository.Create().SetUserID(bob.ID).SetViewerID("bob-viewer").SetRepositoryKey("bob-node").SetNameWithOwner("org/legacy-000").SetPrivate(false),
		store.state.client.GitHubRepository.Create().SetUserID(alice.ID).SetViewerID("old-viewer").SetRepositoryKey("public-node").SetNameWithOwner("org/public").SetPrivate(false),
		store.state.client.GitHubRepository.Create().SetUserID(alice.ID).SetViewerID("old-viewer").SetRepositoryKey("unprovable-node").SetNameWithOwner("org/unprovable").SetPrivate(false),
	)
	if err := store.state.client.GitHubRepository.CreateBulk(records...).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := database.ExecContext(test.Context(), "DROP TABLE repository_private_histories"); err != nil {
		test.Fatal(err)
	}
	if err := database.Close(); err != nil {
		test.Fatal(err)
	}
	for restart := range 2 {
		reopened, err := Open(test.Context(), databasePath, key)
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() { _ = reopened.Close() })
		if count, err := reopened.state.client.RepositoryPrivateHistory.Query().Count(test.Context()); err != nil || count != 260 {
			test.Fatalf("legacy private backfill restart=%d count=%d error=%v", restart, count, err)
		}
		if err := reopened.state.client.GitHubRepository.Update().SetPrivate(false).Exec(test.Context()); err != nil {
			test.Fatal(err)
		}
		for _, name := range []string{"org/legacy-000", "org/legacy-128", "org/legacy-259"} {
			if _, err := reopened.BeginSourceAccess(test.Context(), "alice", repository.Reference{NameWithOwner: name}); !errors.Is(err, ErrSourceAccessUnavailable) {
				test.Fatalf("backfilled private key lost across restart: %s %v", name, err)
			}
		}
		for owner, name := range map[string]string{"alice": "org/public", "bob": "org/legacy-000"} {
			if ticket, err := reopened.BeginSourceAccess(test.Context(), owner, repository.Reference{NameWithOwner: name}); err != nil || ticket.Authenticated() {
				test.Fatalf("backfill denied public/other owner: %s %v", owner, err)
			}
		}
		if ticket, err := reopened.BeginSourceAccess(test.Context(), "alice", repository.Reference{NameWithOwner: "org/unprovable"}); err != nil || ticket.Authenticated() {
			test.Fatalf("migration invented lost private evidence: %v", err)
		}
		if err := reopened.Close(); err != nil {
			test.Fatal(err)
		}
	}
}

func TestPrivateSourceHistoryRenameViewerOwnerAndPublicBoundaries(test *testing.T) {
	store, peer := refreshStores(test)
	token, _ := github.NewToken("synthetic-private-history-boundaries", nil)
	generation, err := store.PutCredential(test.Context(), "alice", "first-viewer", "", token)
	if err != nil {
		test.Fatal(err)
	}
	collect := func(name string, private bool) {
		test.Helper()
		reference := repository.Reference{NameWithOwner: name}
		ticket, err := store.BeginSourceAccess(test.Context(), "alice", reference)
		if err != nil {
			test.Fatal(err)
		}
		ticket, err = store.BindSourceMetadata(test.Context(), ticket, repository.GitHubMetadata{NodeID: "same-node", NameWithOwner: name, Private: private})
		if err != nil {
			test.Fatal(err)
		}
		if _, err := store.CollectRepository(store.WithSourceAccess(test.Context(), ticket), "alice", reference, strings.Repeat("a", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")}); err != nil {
			test.Fatal(err)
		}
	}
	collect("org/original", true)
	collect("org/public-rename", false)
	collect("org/private-rename", true)
	collect("org/private-rename", true)
	collect("org/private-rename", false)
	if _, err := store.PutCredential(test.Context(), "alice", "second-viewer", generation, token); err != nil {
		test.Fatal(err)
	}
	collect("org/original", false)
	if count, err := peer.state.client.RepositoryPrivateHistory.Query().Count(test.Context()); err != nil || count != 2 {
		test.Fatalf("history lost canonical aliases or duplicated receipts: count=%d error=%v", count, err)
	}
	if count, err := peer.state.client.GitHubRepository.Query().Where(githubrepository.PrivateEQ(true)).Count(test.Context()); err != nil || count != 0 {
		test.Fatalf("history made current private metadata sticky: count=%d error=%v", count, err)
	}
	if _, err := peer.state.client.GitHubCredential.Delete().Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	for _, name := range []string{"org/original", "org/private-rename"} {
		if _, err := peer.BeginSourceAccess(test.Context(), "alice", repository.Reference{NameWithOwner: name}); !errors.Is(err, ErrSourceAccessUnavailable) {
			test.Fatalf("historical key granted anonymous access: %s %v", name, err)
		}
		if ticket, err := peer.BeginSourceAccess(test.Context(), "bob", repository.Reference{NameWithOwner: name}); err != nil || ticket.Authenticated() {
			test.Fatalf("history crossed owner boundary: %s %v", name, err)
		}
	}
	for _, name := range []string{"org/public-rename", "org/unseen"} {
		if ticket, err := peer.BeginSourceAccess(test.Context(), "alice", repository.Reference{NameWithOwner: name}); err != nil || ticket.Authenticated() {
			test.Fatalf("private history denied unrelated/public history: %s %v", name, err)
		}
	}
}

func TestPrivateSourceHistoryInvalidatesAnonymousTicket(test *testing.T) {
	store, peer := refreshStores(test)
	reference := repository.Reference{NameWithOwner: "org/newly-private"}
	public, err := store.BeginSourceAccess(test.Context(), "alice", reference)
	if err != nil || public.Authenticated() {
		test.Fatal("initial anonymous proof unavailable")
	}
	token, _ := github.NewToken("synthetic-history-public-ticket", nil)
	if _, err := peer.PutCredential(test.Context(), "alice", "viewer", "", token); err != nil {
		test.Fatal(err)
	}
	for _, private := range []bool{true, false} {
		ticket, err := peer.BeginSourceAccess(test.Context(), "alice", reference)
		if err != nil {
			test.Fatal(err)
		}
		ticket, err = peer.BindSourceMetadata(test.Context(), ticket, repository.GitHubMetadata{NodeID: "new-private-node", NameWithOwner: reference.NameWithOwner, Private: private})
		if err != nil {
			test.Fatal(err)
		}
		if _, err := peer.CollectRepository(peer.WithSourceAccess(test.Context(), ticket), "alice", reference, strings.Repeat("a", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")}); err != nil {
			test.Fatal(err)
		}
	}
	if _, err := peer.state.client.GitHubCredential.Delete().Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := store.CheckSourceAccess(test.Context(), public); !errors.Is(err, ErrSourceAccessUnavailable) {
		test.Fatalf("old anonymous proof survived historical refusal: %v", err)
	}
}

func TestPrivateSourceHistoryRejectsInvalidMetadata(test *testing.T) {
	store, _ := refreshStores(test)
	reference := repository.Reference{NameWithOwner: "org/source"}
	public, err := store.BeginSourceAccess(test.Context(), "alice", reference)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.BindSourceMetadata(test.Context(), public, repository.GitHubMetadata{NodeID: "node", NameWithOwner: reference.NameWithOwner, Private: true}); err == nil {
		test.Fatal("anonymous caller manufactured private metadata")
	}
	token, _ := github.NewToken("synthetic-history-invalid-metadata", nil)
	if _, err := store.PutCredential(test.Context(), "alice", "viewer", "", token); err != nil {
		test.Fatal(err)
	}
	ticket, err := store.BeginSourceAccess(test.Context(), "alice", reference)
	if err != nil {
		test.Fatal(err)
	}
	for _, metadata := range []repository.GitHubMetadata{
		{NodeID: "", NameWithOwner: reference.NameWithOwner, Private: true},
		{NodeID: "node\nsecret", NameWithOwner: reference.NameWithOwner, Private: true},
		{NodeID: "node", NameWithOwner: "org/other", Private: true},
	} {
		if _, err := store.BindSourceMetadata(test.Context(), ticket, metadata); err == nil {
			test.Fatal("invalid metadata produced a private receipt")
		}
	}
	if _, err := store.BindSourceMetadata(test.Context(), ticket, repository.GitHubMetadata{NodeID: "valid-node", NameWithOwner: reference.NameWithOwner, Private: true}); err != nil {
		test.Fatal(err)
	}
	if count, err := store.state.client.RepositoryPrivateHistory.Query().Count(test.Context()); err != nil || count != 0 {
		test.Fatalf("uncommitted/invalid metadata polluted history: count=%d error=%v", count, err)
	}
}

func TestPrivateSourceHistoryMigrationRollsBackInvalidEvidence(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	databasePath := filepath.Join(directory, "server.db")
	store, err := Open(test.Context(), databasePath, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	owner, err := findUser(test.Context(), store.state.client, "alice")
	if err != nil {
		test.Fatal(err)
	}
	for index, name := range []string{"Org/Valid", "https://github.com/org/invalid"} {
		if err := store.state.client.GitHubRepository.Create().SetUserID(owner.ID).SetViewerID("viewer").SetRepositoryKey(fmt.Sprintf("node-%d", index)).SetNameWithOwner(name).SetPrivate(true).Exec(test.Context()); err != nil {
			test.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	if reopened, err := Open(test.Context(), databasePath, make([]byte, 32)); !errors.Is(err, ErrSourceAccessUnavailable) {
		if reopened != nil {
			_ = reopened.Close()
		}
		test.Fatalf("invalid legacy evidence accepted: %v", err)
	}
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM repository_private_histories").Scan(&count); err != nil || count != 0 {
		test.Fatalf("failed migration partially persisted history: count=%d error=%v", count, err)
	}
	if _, err := database.ExecContext(test.Context(), "UPDATE git_hub_repositories SET name_with_owner = ? WHERE repository_key = ?", "org/repaired", "node-1"); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), databasePath, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	if count, err := reopened.state.client.RepositoryPrivateHistory.Query().Count(test.Context()); err != nil || count != 2 {
		test.Fatalf("valid retry did not migrate both records: count=%d error=%v", count, err)
	}
}

type privateHistoryListFixture struct {
	store      *Store
	peer       *Store
	token      github.Token
	generation string
	name       string
	private    bool
}

func newPrivateHistoryListFixture(test *testing.T) *privateHistoryListFixture {
	test.Helper()
	store, peer := refreshStores(test)
	token, _ := github.NewToken("synthetic-private-history-lists", nil)
	generation, err := store.PutCredential(test.Context(), "alice", "viewer", "", token)
	if err != nil {
		test.Fatal(err)
	}
	fixture := &privateHistoryListFixture{store: store, peer: peer, token: token, generation: generation, name: "Org/List-Private", private: true}
	if err := store.ApplyListSync(test.Context(), fixture.result(test)); err != nil {
		test.Fatal(err)
	}
	if err := store.SetListSubscription(test.Context(), "alice", "selected", true); err != nil {
		test.Fatal(err)
	}
	return fixture
}

func (fixture *privateHistoryListFixture) result(test *testing.T) ListSyncResult {
	test.Helper()
	client, err := github.NewListsClient(fixture.token, &http.Client{Transport: independentHistoryTransport(func(request *http.Request) (*http.Response, error) {
		var query struct {
			Variables struct {
				ID string `json:"id"`
			} `json:"variables"`
		}
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			return nil, err
		}
		metadata := map[string]any{"id": "selected", "name": "selected", "slug": "selected", "isPrivate": false, "updatedAt": "2026-01-01T00:00:00Z", "lastAddedAt": "2026-01-01T00:00:00Z"}
		page := map[string]any{"hasNextPage": false, "endCursor": nil}
		var response any
		if query.Variables.ID == "" {
			response = map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer", "lists": map[string]any{"pageInfo": page, "nodes": []any{metadata}}}}}
		} else {
			metadata["__typename"], metadata["user"] = "UserList", map[string]any{"id": "viewer"}
			metadata["items"] = map[string]any{"totalCount": 1, "pageInfo": page, "nodes": []any{map[string]any{"__typename": "Repository", "id": "history-list-node", "nameWithOwner": fixture.name, "isPrivate": fixture.private, "databaseId": nil}}}
			response = map[string]any{"data": map[string]any{"node": metadata}}
		}
		body, err := json.Marshal(response)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})})
	if err != nil {
		test.Fatal(err)
	}
	ticket, err := fixture.store.BeginListSync(test.Context(), "alice", fixture.generation)
	if err != nil {
		test.Fatal(err)
	}
	result, err := fixture.store.ReadListSync(test.Context(), ticket, client)
	if err != nil {
		test.Fatal(err)
	}
	return result
}

func TestPrivateSourceHistoryListsAtomicAndUnknownCommitNotReplayed(test *testing.T) {
	for _, outcome := range []string{"rollback", "unknown", "superseded"} {
		test.Run(outcome, func(test *testing.T) {
			fixture := newPrivateHistoryListFixture(test)
			store, peer := fixture.store, fixture.peer
			result := fixture.result(test)
			receiptError := errors.New("synthetic-history-commit-error")
			commits := 0
			store.state.client.RepositoryPrivateHistory.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					transaction, err := mutation.(*ent.RepositoryPrivateHistoryMutation).Tx()
					if err != nil {
						return nil, err
					}
					transaction.OnCommit(func(next ent.Committer) ent.Committer {
						return ent.CommitFunc(func(ctx context.Context, transaction *ent.Tx) error {
							if outcome == "rollback" {
								return receiptError
							}
							if err := next.Commit(ctx, transaction); err != nil {
								return err
							}
							commits++
							return receiptError
						})
					})
					return next.Mutate(ctx, mutation)
				})
			})
			if outcome == "superseded" {
				if err := peer.state.client.GitHubCredential.Update().SetStatus(githubcredential.StatusNeedsReauthorization).Exec(test.Context()); err != nil {
					test.Fatal(err)
				}
			}
			err := store.ApplyListSync(test.Context(), result)
			if err == nil || outcome != "superseded" && !errors.Is(err, receiptError) {
				test.Fatalf("injected commit result not observed: %v", err)
			}
			want := 0
			if outcome == "unknown" {
				want = 1
			}
			if count, err := peer.state.client.RepositoryPrivateHistory.Query().Count(test.Context()); err != nil || count != want || commits != want {
				test.Fatalf("non-atomic/replayed history: count=%d commits=%d want=%d error=%v", count, commits, want, err)
			}
			if count, err := peer.state.client.GitHubRepository.Query().Count(test.Context()); err != nil || count != want {
				test.Fatalf("history and current metadata did not commit together: count=%d error=%v", count, err)
			}
			if outcome == "unknown" {
				if err := store.ApplyListSync(test.Context(), result); !errors.Is(err, ErrSyncSuperseded) || commits != 1 {
					test.Fatalf("unknown Lists commit replayed candidate: %v", err)
				}
				fixture.name, fixture.private = "org/current-public", false
				if err := store.ApplyListSync(test.Context(), fixture.result(test)); err != nil {
					test.Fatal(err)
				}
			}
			if _, err := peer.state.client.GitHubCredential.Delete().Exec(test.Context()); err != nil {
				test.Fatal(err)
			}
			_, err = peer.BeginSourceAccess(test.Context(), "alice", repository.Reference{NameWithOwner: "org/list-private"})
			if outcome == "unknown" && !errors.Is(err, ErrSourceAccessUnavailable) || outcome != "unknown" && err != nil {
				test.Fatalf("history refusal inconsistent with physical commit: %v", err)
			}
		})
	}
}

const independentAccessSecret = "INDEPENDENT-S1-ACCESS-SECRET-SENTINEL"

func independentAccessFixture(test *testing.T) (*Store, repository.Reference, SourceAccessTicket, time.Time) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	if err := store.CreateUser(test.Context(), "oracle-owner"); err != nil {
		test.Fatal(err)
	}
	started := time.Now().UTC()
	store.state.now = func() time.Time { return started }
	encoded, err := json.Marshal(map[string]any{"version": 1, "access": independentAccessSecret, "expires_at": started.Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	token, err := github.DecodeStoredToken(encoded)
	clear(encoded)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.PutCredential(test.Context(), "oracle-owner", "oracle-viewer", "", token); err != nil {
		test.Fatal(err)
	}
	reference := repository.Reference{NameWithOwner: "org/independent"}
	ticket, err := store.BeginSourceAccess(test.Context(), "oracle-owner", reference)
	if err != nil {
		test.Fatal(err)
	}
	ticket, err = store.BindSourceMetadata(test.Context(), ticket, repository.GitHubMetadata{NodeID: "independent-node", NameWithOwner: reference.NameWithOwner, Private: true})
	if err != nil {
		test.Fatal(err)
	}
	return store, reference, ticket, started
}

func independentAccessSkills(content string) []repository.ArchivedSkill {
	return []repository.ArchivedSkill{{Skill: skill.Skill{Name: "oracle", Description: "oracle source", Path: "skills/oracle"}, ContentHash: strings.Repeat(content, 64)}}
}

func TestIndependentS1CommitClockRollback(test *testing.T) {
	store, reference, ticket, started := independentAccessFixture(test)
	mutations := 0
	store.state.client.CollectedRepository.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			result, err := next.Mutate(ctx, mutation)
			if err == nil {
				mutations++
				store.state.now = func() time.Time { return started.Add(time.Hour) }
			}
			return result, err
		})
	})
	result, err := store.CollectRepository(store.WithSourceAccess(test.Context(), ticket), "oracle-owner", reference, strings.Repeat("a", 64), independentAccessSkills("b"))
	if err == nil || result.ID != "" || mutations != 1 {
		test.Error("independent_source_commit_clock_fresh=false")
	}
	if count, err := store.state.client.CollectedRepository.Query().Count(test.Context()); err != nil || count != 0 {
		test.Error("independent_source_commit_snapshot_rollback=false")
	}
	if count, err := store.state.client.GitHubRepository.Query().Count(test.Context()); err != nil || count != 0 {
		test.Error("independent_source_commit_metadata_rollback=false")
	}
}

func TestIndependentS1LineageBinding(test *testing.T) {
	store, reference, ticket, _ := independentAccessFixture(test)
	collected, err := store.CollectRepository(store.WithSourceAccess(test.Context(), ticket), "oracle-owner", reference, strings.Repeat("a", 64), independentAccessSkills("b"))
	if err != nil {
		test.Fatal(err)
	}
	refresh, err := store.BeginRepositorySourceRefresh(test.Context(), "oracle-owner", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	bound, err := store.BindSourceMetadata(test.Context(), refresh.Access(), repository.GitHubMetadata{NodeID: "independent-node", NameWithOwner: reference.NameWithOwner, Private: true})
	if err != nil {
		test.Fatal(err)
	}
	other, err := store.BeginSourceAccess(test.Context(), "oracle-owner", reference)
	if err != nil {
		test.Fatal(err)
	}
	other, err = store.BindSourceMetadata(test.Context(), other, repository.GitHubMetadata{NodeID: "independent-node", NameWithOwner: reference.NameWithOwner, Private: true})
	if err != nil {
		test.Fatal(err)
	}
	result, err := store.ApplySourceRefresh(store.WithSourceAccess(test.Context(), other), refresh, strings.Repeat("c", 64), independentAccessSkills("d"))
	if !errors.Is(err, ErrSourceAccessUnavailable) || result.Changed {
		test.Error("independent_source_fetched_lineage_cannot_be_reminted=false")
	}
	readback, err := store.ReadRepositoryRefresh(test.Context(), "oracle-owner", collected.ID)
	if err != nil || readback.Version.ArchiveHash != strings.Repeat("a", 64) {
		test.Error("independent_source_wrong_lineage_preserves_snapshot=false")
	}
	if result, err := store.ApplySourceRefresh(store.WithSourceAccess(test.Context(), bound), refresh, strings.Repeat("c", 64), independentAccessSkills("d")); err != nil || !result.Changed {
		test.Error("independent_source_original_lineage_is_live=false", err)
	}
}

func TestIndependentS1ProofPrivacy(test *testing.T) {
	store, reference, ticket, _ := independentAccessFixture(test)
	collected, err := store.CollectRepository(store.WithSourceAccess(test.Context(), ticket), "oracle-owner", reference, strings.Repeat("a", 64), independentAccessSkills("b"))
	if err != nil {
		test.Fatal(err)
	}
	refresh, err := store.BeginRepositorySourceRefresh(test.Context(), "oracle-owner", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	for index, value := range []any{ticket, refresh, struct{ ticket SourceAccessTicket }{ticket}, struct{ ticket SourceRefreshTicket }{refresh}, []SourceAccessTicket{ticket}, map[string]SourceRefreshTicket{"ticket": refresh}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%t", "%d", "%x", "%s", "%q", "%p", "%f", "%e", "%g", "%b", "%o", "%O", "%c", "%U", "%X", "%F", "%E", "%G", "%T"} {
			if strings.Contains(fmt.Sprintf(format, value), independentAccessSecret) {
				test.Errorf("independent_source_private_container_fmt_redacted=false case=%d verb=%s", index, format)
			}
			var captured bytes.Buffer
			log.New(&captured, "", 0).Printf(format, value)
			if strings.Contains(captured.String(), independentAccessSecret) {
				test.Errorf("independent_source_private_container_log_redacted=false case=%d verb=%s", index, format)
			}
		}
		var output bytes.Buffer
		slog.New(slog.NewJSONHandler(&output, nil)).Info("oracle", "value", value)
		if strings.Contains(output.String(), independentAccessSecret) {
			test.Error("independent_source_slog_redacted=false")
		}
		encoded, _ := json.Marshal(value)
		if bytes.Contains(encoded, []byte(independentAccessSecret)) {
			test.Error("independent_source_JSON_redacted=false")
		}
	}
}

func TestPrivateSourceCapabilityContextPrivacyAndZeroValues(test *testing.T) {
	store, reference, ticket, _ := independentAccessFixture(test)
	zero := SourceAccessTicket{}
	if zero.Authenticated() || zero.Token().Reveal() != "" || !zero.Deadline().IsZero() || zero.Reference() != (repository.Reference{}) {
		test.Fatal("zero source capability exposed authority")
	}
	if err := store.CheckSourceAccess(test.Context(), zero); !errors.Is(err, ErrSourceAccessUnavailable) {
		test.Fatalf("zero source capability accepted: %v", err)
	}
	ctx := store.WithSourceAccess(test.Context(), ticket)
	collected, err := store.CollectRepository(ctx, "oracle-owner", reference, strings.Repeat("a", 64), independentAccessSkills("b"))
	if err != nil {
		test.Fatal(err)
	}
	refresh, err := store.BeginRepositorySourceRefresh(test.Context(), "oracle-owner", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	type privateLeaf struct {
		access  SourceAccessTicket
		refresh SourceRefreshTicket
	}
	type privateContainer struct {
		value   privateLeaf
		pointer *privateLeaf
	}
	leaf := privateLeaf{access: ticket, refresh: refresh}
	deep := privateContainer{value: leaf, pointer: &leaf}
	for index, value := range []any{ctx, struct{ ctx context.Context }{ctx}, []context.Context{ctx}, map[string]any{"context": ctx}, &ticket, deep, &deep, []privateContainer{deep}, map[string]*privateContainer{"proof": &deep}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%t", "%d", "%x", "%s", "%q", "%p", "%f", "%e", "%g", "%b", "%o", "%O", "%c", "%U", "%X", "%F", "%E", "%G", "%T"} {
			if strings.Contains(fmt.Sprintf(format, value), independentAccessSecret) {
				test.Errorf("source capability context fmt disclosed token case=%d verb=%s", index, format)
			}
			var captured bytes.Buffer
			log.New(&captured, "", 0).Printf(format, value)
			if strings.Contains(captured.String(), independentAccessSecret) {
				test.Errorf("source capability context log disclosed token case=%d verb=%s", index, format)
			}
		}
		for _, handler := range []func(*bytes.Buffer) slog.Handler{
			func(output *bytes.Buffer) slog.Handler { return slog.NewTextHandler(output, nil) },
			func(output *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(output, nil) },
		} {
			var output bytes.Buffer
			slog.New(handler(&output)).Info("capability", "value", value)
			if strings.Contains(output.String(), independentAccessSecret) {
				test.Errorf("source capability structured log disclosed token case=%d", index)
			}
		}
		encoded, err := json.Marshal(value)
		if err == nil && bytes.Contains(encoded, []byte(independentAccessSecret)) {
			test.Errorf("source capability JSON disclosed token case=%d", index)
		}
	}
	for _, value := range []any{ticket.Token().Reveal(), struct{ token string }{ticket.Token().Reveal()}} {
		if !strings.Contains(fmt.Sprintf("%v", value), independentAccessSecret) {
			test.Fatal("privacy detector failed raw secret positive control")
		}
		var output bytes.Buffer
		log.New(&output, "", 0).Printf("%v", value)
		if !strings.Contains(output.String(), independentAccessSecret) {
			test.Fatal("logger detector failed raw secret positive control")
		}
	}
	for _, handler := range []func(*bytes.Buffer) slog.Handler{
		func(output *bytes.Buffer) slog.Handler { return slog.NewTextHandler(output, nil) },
		func(output *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(output, nil) },
	} {
		var output bytes.Buffer
		slog.New(handler(&output)).Info("positive control", "value", ticket.Token().Reveal())
		if !strings.Contains(output.String(), independentAccessSecret) {
			test.Fatal("structured logger detector failed raw secret positive control")
		}
	}
	encoded, err := json.Marshal(ticket.Token().Reveal())
	if err != nil || !bytes.Contains(encoded, []byte(independentAccessSecret)) {
		test.Fatal("JSON detector failed raw secret positive control")
	}
}
