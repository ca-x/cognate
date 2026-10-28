package state

import (
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
	"github.com/ca-x/cognate/internal/server/ent/githubcredential"
)

func sourceStarFixture(test *testing.T) (*Store, *Store, RepositoryRefreshSnapshot, string, time.Time) {
	test.Helper()
	store, peer := refreshStores(test)
	now := time.Now().UTC()
	store.state.now = func() time.Time { return now }
	peer.state.now = store.state.now
	material, err := json.Marshal(map[string]any{"version": 1, "access": "synthetic-source-token", "expires_at": now.Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	token, err := github.DecodeStoredToken(material)
	clear(material)
	if err != nil {
		test.Fatal(err)
	}
	generation, err := store.PutCredential(test.Context(), "alice", "viewer", "", token)
	if err != nil {
		test.Fatal(err)
	}
	owner, err := findUser(test.Context(), store.state.client, "alice")
	if err != nil {
		test.Fatal(err)
	}
	if err := store.state.client.GitHubCredential.Update().SetSyncSequence(1).SetSyncAppliedSequence(1).SetSyncFetchID("synthetic-fetch").Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	base := refreshCollect(test, store, "org/source", refreshSkill("skills/chosen", "b"))
	if err := store.state.client.CollectedRepository.Update().Where(collectedrepository.PublicIDEQ(base.Repository.ID)).SetManual(false).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	list, err := store.state.client.GitHubList.Create().SetUserID(owner.ID).SetViewerID("viewer").SetListKey("selected").SetCredentialGeneration(generation).SetName("selected").SetSlug("selected").SetPrivate(false).SetUpdatedAt(now).SetLastAddedAt(now).SetVerifiedAt(now).SetItemsVerifiedAt(now).SetSubscribed(true).SetVisible(true).Save(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	remote, err := store.state.client.GitHubRepository.Create().SetUserID(owner.ID).SetViewerID("viewer").SetRepositoryKey("remote").SetNameWithOwner("Org/Source").SetPrivate(false).Save(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	if err := store.state.client.GitHubMembership.Create().SetListID(list.ID).SetRepositoryID(remote.ID).SetVisible(true).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	base, err = store.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil {
		test.Fatal(err)
	}
	return store, peer, base, generation, now
}

func TestSourceRefreshStarRejectsChangedAuthority(test *testing.T) {
	for _, invalidation := range []string{"credential-generation", "credential-key", "sequence", "applied", "fetch-id", "private", "repository-identity", "hidden-list", "hidden-member", "missing-member", "list-generation", "stale-list", "future-list", "manual-class"} {
		test.Run(invalidation, func(test *testing.T) {
			store, peer, base, _, now := sourceStarFixture(test)
			ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
			var err error
			switch invalidation {
			case "credential-generation":
				err = peer.state.client.GitHubCredential.Update().SetGeneration("replaced-generation").Exec(test.Context())
			case "credential-key":
				err = peer.state.client.GitHubCredential.Update().SetKeyID("replaced-key").Exec(test.Context())
			case "sequence":
				err = peer.state.client.GitHubCredential.Update().AddSyncSequence(1).Exec(test.Context())
			case "applied":
				err = peer.state.client.GitHubCredential.Update().SetSyncAppliedSequence(0).Exec(test.Context())
			case "fetch-id":
				err = peer.state.client.GitHubCredential.Update().SetSyncFetchID("replacement-fetch").Exec(test.Context())
			case "private":
				err = peer.state.client.GitHubRepository.Update().SetPrivate(true).Exec(test.Context())
			case "repository-identity":
				err = peer.state.client.GitHubRepository.Update().SetNameWithOwner("org/replacement").Exec(test.Context())
			case "hidden-list":
				err = peer.state.client.GitHubList.Update().SetVisible(false).Exec(test.Context())
			case "hidden-member":
				err = peer.state.client.GitHubMembership.Update().SetVisible(false).Exec(test.Context())
			case "missing-member":
				_, err = peer.state.client.GitHubMembership.Delete().Exec(test.Context())
			case "list-generation":
				err = peer.state.client.GitHubList.Update().SetCredentialGeneration("replacement-generation").Exec(test.Context())
			case "stale-list":
				err = peer.state.client.GitHubList.Update().SetVerifiedAt(now.Add(-store.state.observationMaxAge)).Exec(test.Context())
			case "future-list":
				err = peer.state.client.GitHubList.Update().SetVerifiedAt(now.Add(time.Second)).Exec(test.Context())
			case "manual-class":
				err = peer.state.client.CollectedRepository.Update().SetManual(true).Exec(test.Context())
			}
			if err != nil {
				test.Fatal(err)
			}
			if err := store.CheckSourceRefresh(test.Context(), ticket); err == nil {
				test.Fatal("changed star authorization survived post-fetch check")
			}
			result, err := store.ApplySourceRefresh(test.Context(), ticket, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")})
			if err == nil || !reflect.DeepEqual(result, RepositoryRefreshResult{}) {
				test.Fatal("changed star authorization committed")
			}
			current, err := peer.CollectedRepository(test.Context(), "alice", base.Repository.ID)
			if err != nil || current.ArchiveHash != base.Version.ArchiveHash || current.Skills[0].ContentHash != base.Repository.Skills[0].ContentHash {
				test.Fatal("denied authorization changed the source")
			}
		})
	}
}

func TestSourceRefreshBoundsAndEmptyBatch(test *testing.T) {
	store, _ := refreshStores(test)
	batch, err := store.ListSourceRefreshBatch(test.Context(), SourceRefreshCursor{}, 16)
	if err != nil || len(batch.Items) != 0 || batch.Next != (SourceRefreshCursor{}) {
		test.Fatal("empty batch changed cursor")
	}
	for _, bounds := range []struct {
		cursor SourceRefreshCursor
		limit  int
	}{
		{SourceRefreshCursor{}, 0}, {SourceRefreshCursor{}, 17}, {SourceRefreshCursor{AfterOwnerID: -1}, 1}, {SourceRefreshCursor{Slot: -1}, 1}, {SourceRefreshCursor{Slot: 1000}, 1},
	} {
		if _, err := store.ListSourceRefreshBatch(test.Context(), bounds.cursor, bounds.limit); !errors.Is(err, ErrSourceRefreshUnavailable) {
			test.Fatal("invalid cursor/limit accepted")
		}
	}
	if !reflect.DeepEqual((SourceRefreshTicket{}).Snapshot(), RepositoryRefreshSnapshot{}) || !(SourceRefreshTicket{}).Deadline().IsZero() {
		test.Fatal("zero ticket exposed state")
	}
}

func TestSourceRefreshStarRechecksRetryAndCommit(test *testing.T) {
	for _, invalidation := range []string{"rotate", "switch-owner", "revoke", "unselect", "tuple", "expiry-at-commit", "observation-at-commit"} {
		test.Run(invalidation, func(test *testing.T) {
			store, peer, base, generation, now := sourceStarFixture(test)
			ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
			original := refreshPack(test, store, PackSelection{SkillID: base.Repository.Skills[0].ID})
			queries := 0
			var competingError error
			if invalidation == "expiry-at-commit" || invalidation == "observation-at-commit" {
				store.state.client.Pack.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
						value, err := next.Mutate(ctx, mutation)
						store.state.now = func() time.Time {
							if invalidation == "expiry-at-commit" {
								return now.Add(time.Hour)
							}
							return now.Add(store.state.observationMaxAge)
						}
						return value, err
					})
				})
			} else {
				store.state.client.CollectedRepository.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
					return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
						value, err := next.Query(ctx, query)
						queries++
						if queries == 1 && err == nil {
							switch invalidation {
							case "rotate", "switch-owner":
								token, tokenError := github.NewToken("synthetic-rotated-source", nil)
								if tokenError != nil {
									return nil, tokenError
								}
								viewer := "viewer"
								if invalidation == "switch-owner" {
									viewer = "other-viewer"
								}
								_, competingError = peer.PutCredential(ctx, "alice", viewer, generation, token)
							case "revoke":
								competingError = peer.state.client.GitHubCredential.Update().SetStatus(githubcredential.StatusNeedsReauthorization).Exec(ctx)
							case "unselect":
								competingError = peer.SetListSubscription(ctx, "alice", "selected", false)
							case "tuple":
								_, competingError = peer.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, strings.Repeat("e", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "f")})
							}
						}
						return value, err
					})
				}))
			}
			result, err := store.ApplySourceRefresh(test.Context(), ticket, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")})
			if competingError != nil {
				test.Fatalf("competing state did not commit: %v", competingError)
			}
			if err == nil || !reflect.DeepEqual(result, RepositoryRefreshResult{}) {
				test.Fatalf("stale authorization committed: %v", err)
			}
			if invalidation == "tuple" {
				current, err := peer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
				if err != nil || current.Version.ArchiveHash != strings.Repeat("e", 64) {
					test.Fatal("stale candidate overwrote winning tuple")
				}
			} else {
				assertRefreshUnchanged(test, peer, base, original)
			}
			if invalidation == "rotate" || invalidation == "switch-owner" || invalidation == "revoke" || invalidation == "unselect" {
				if queries < 2 {
					test.Fatal("authorization was not reread in a fresh retry")
				}
			}
		})
	}
}

func TestSourceRefreshFairPagesAndOpaqueTargets(test *testing.T) {
	store, peer := refreshStores(test)
	for _, owner := range []string{"alice", "bob"} {
		for _, name := range []string{"manual/first", "plugin/second", "manual/third"} {
			reference, _ := repository.ParseReference(name, "")
			if _, err := store.CollectRepository(test.Context(), owner, reference, strings.Repeat("a", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")}); err != nil {
				test.Fatal(err)
			}
		}
	}
	var cursor SourceRefreshCursor
	seen := make(map[string]bool)
	for range 6 {
		batch, err := store.ListSourceRefreshBatch(test.Context(), cursor, 1)
		if err != nil || len(batch.Items) != 1 {
			test.Fatalf("bounded page: %v", err)
		}
		item := batch.Items[0]
		if _, err := peer.BeginSourceRefresh(test.Context(), item.Target); !errors.Is(err, ErrSourceRefreshUnavailable) {
			test.Fatal("target crossed issuer boundary")
		}
		ticket, err := store.BeginSourceRefresh(test.Context(), item.Target)
		if err != nil {
			test.Fatal(err)
		}
		snapshot := ticket.Snapshot()
		seen[snapshot.Repository.ID] = true
		snapshot.Repository.Skills[0].ContentHash = "mutated-copy"
		if err := store.CheckSourceRefresh(test.Context(), ticket); err != nil || ticket.Snapshot().Repository.Skills[0].ContentHash == "mutated-copy" {
			test.Fatal("snapshot copy modified the ticket")
		}
		cursor = item.Next
	}
	if len(seen) != 6 {
		test.Fatalf("owner/slot rotation starved sources: %d", len(seen))
	}
	if _, err := store.BeginSourceRefresh(test.Context(), SourceRefreshTarget{}); !errors.Is(err, ErrSourceRefreshUnavailable) {
		test.Fatal("zero target accepted")
	}
}

func TestSourceRefreshAbsentContextLeavesOrdinaryWritesUnchanged(test *testing.T) {
	store, _ := refreshStores(test)
	category, err := store.CreateCategory(test.Context(), "alice", "before")
	if err != nil {
		test.Fatal(err)
	}
	store.state.client.GitHubCredential.Intercept(ent.InterceptFunc(func(ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) {
			return nil, errors.New("ordinary write queried source credentials")
		})
	}))
	if _, err := store.UpdateCategory(test.Context(), "alice", category.ID, 1, "after"); err != nil {
		test.Fatal(err)
	}
	if err := store.DeleteCategory(test.Context(), "alice", category.ID, 2); err != nil {
		test.Fatal(err)
	}
}

func TestSourceRefreshStarWithoutListsMetadataChange(test *testing.T) {
	store, peer, base, _, _ := sourceStarFixture(test)
	ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
	original := refreshPack(test, store, PackSelection{SkillID: base.Repository.Skills[0].ID})
	result, err := store.ApplySourceRefresh(test.Context(), ticket, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")})
	if err != nil || !result.Changed {
		test.Fatalf("selected public star was not refreshed: %v", err)
	}
	pack, err := peer.Pack(test.Context(), "alice", original.ID)
	if err != nil || pack.Revision != 2 {
		test.Fatalf("star refresh did not fence pack: %v", err)
	}
}

func TestSourceRefreshStarNoopStillSerializesCredentialRotation(test *testing.T) {
	store, peer, base, generation, _ := sourceStarFixture(test)
	ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
	rotated := false
	var rotationError error
	store.state.client.CollectedRepository.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if !rotated && err == nil {
				rotated = true
				token, err := github.NewToken("synthetic-noop-rotation", nil)
				if err != nil {
					return nil, err
				}
				_, rotationError = peer.PutCredential(test.Context(), "alice", "viewer", generation, token)
				if rotationError == nil {
					current, err := peer.state.client.GitHubCredential.Query().Only(test.Context())
					rotationError = err
					if err == nil && current.ID != ticket.proof.star.credentialID {
						rotationError = errors.New("fixture replaced credential instead of rotating same ID")
					}
				}
			}
			return value, err
		})
	}))
	result, err := store.ApplySourceRefresh(test.Context(), ticket, base.Version.ArchiveHash, []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")})
	if !rotated || rotationError != nil {
		test.Fatalf("same-ID rotation did not win: %v", rotationError)
	}
	if err == nil || !reflect.DeepEqual(result, RepositoryRefreshResult{}) {
		test.Fatal("no-op refresh authorized an old credential snapshot")
	}
}

func sourceRefreshTicketFor(test *testing.T, store *Store, id string) SourceRefreshTicket {
	test.Helper()
	batch, err := store.ListSourceRefreshBatch(test.Context(), SourceRefreshCursor{}, 16)
	if err != nil {
		test.Fatal(err)
	}
	for _, item := range batch.Items {
		ticket, err := store.BeginSourceRefresh(test.Context(), item.Target)
		if err != nil {
			test.Fatal(err)
		}
		if ticket.Snapshot().Repository.ID == id {
			return ticket
		}
	}
	test.Fatal("collected source was not scheduled")
	return SourceRefreshTicket{}
}

func TestSourceRefreshManualTicketAndOriginalTuple(test *testing.T) {
	store, peer := refreshStores(test)
	base := refreshCollect(test, store, "org/source", refreshSkill("skills/chosen", "b"))
	ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
	if err := store.CheckSourceRefresh(test.Context(), ticket); err != nil {
		test.Fatal(err)
	}
	if err := peer.CheckSourceRefresh(test.Context(), ticket); !errors.Is(err, ErrSourceRefreshUnavailable) {
		test.Fatal("ticket crossed Store boundary")
	}
	if err := store.CheckSourceRefresh(test.Context(), SourceRefreshTicket{}); !errors.Is(err, ErrSourceRefreshUnavailable) {
		test.Fatal("zero ticket accepted")
	}
	result, err := store.ApplySourceRefresh(test.Context(), ticket, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")})
	if err != nil || !result.Changed || result.Repository.Skills[0].ID != base.Repository.Skills[0].ID {
		test.Fatalf("automatic source did not commit: %v", err)
	}
	current, err := peer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil || !reflect.DeepEqual(current.Repository, result.Repository) {
		test.Fatal("receipt was not committed")
	}
	if _, err := store.ApplySourceRefresh(test.Context(), ticket, strings.Repeat("e", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "f")}); err == nil {
		test.Fatal("ticket silently rebased")
	}
}
