package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
	"github.com/ca-x/cognate/internal/server/ent/collectedskill"
	"github.com/ca-x/cognate/internal/server/ent/pack"
	"github.com/ca-x/cognate/internal/skill"
)

func refreshStores(test *testing.T) (*Store, *Store) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "server.db")
	first, err := Open(test.Context(), path, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = first.Close() })
	for _, owner := range []string{"alice", "bob"} {
		if err := first.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
	}
	second, err := Open(test.Context(), path, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = second.Close() })
	return first, second
}

func assertRefreshUnchanged(test *testing.T, store *Store, base RepositoryRefreshSnapshot, packs ...Pack) {
	test.Helper()
	current, err := store.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil || !reflect.DeepEqual(current, base) {
		test.Fatalf("failed refresh changed repository: %v", err)
	}
	for _, original := range packs {
		current, err := store.Pack(test.Context(), "alice", original.ID)
		if err != nil || !reflect.DeepEqual(current, original) {
			test.Fatalf("failed refresh changed pack: %v", err)
		}
	}
}

func TestRepositoryRefreshReferencedRemovalRenameAndAliasRollback(test *testing.T) {
	for _, operation := range []string{"remove", "rename", "alias"} {
		test.Run(operation, func(test *testing.T) {
			store, peer := refreshStores(test)
			base := refreshCollect(test, store, "org/source", refreshSkill("skills/chosen", "b"), refreshSkill("skills/other", "c"))
			other := refreshCollect(test, store, "org/alias", refreshSkill("skills/same", "b"))
			selection := []PackSelection{{SkillID: base.Repository.Skills[0].ID, Slug: "chosen"}}
			if operation == "alias" {
				selection = append(selection, PackSelection{SkillID: other.Repository.Skills[0].ID, Slug: "chosen"})
			}
			original := refreshPack(test, store, selection...)
			candidate := []repository.ArchivedSkill{refreshSkill("skills/other", "d")}
			expectedError := ErrReferencedSkillRemoved
			switch operation {
			case "rename":
				candidate = append(candidate, refreshSkill("skills/renamed", "b"))
			case "alias":
				candidate = append(candidate, refreshSkill("skills/chosen", "e"))
				expectedError = ErrPackSlugConflict
			}
			result, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, strings.Repeat("f", 64), candidate)
			if !errors.Is(err, expectedError) || !reflect.DeepEqual(result, RepositoryRefreshResult{}) {
				test.Fatalf("unsafe refresh result: %+v %v", result, err)
			}
			assertRefreshUnchanged(test, peer, base, original)
		})
	}
}

func TestRepositoryRefreshCASPublicationAndABA(test *testing.T) {
	store, peer := refreshStores(test)
	originalSkills := []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")}
	base := refreshCollect(test, store, "org/source", originalSkills...)
	for _, owner := range []string{"bob"} {
		result, err := store.RefreshCollectedRepository(test.Context(), owner, base.Repository.ID, base.Version, strings.Repeat("c", 64), originalSkills)
		if !errors.Is(err, ErrRepositoryNotFound) || !reflect.DeepEqual(result, RepositoryRefreshResult{}) {
			test.Fatalf("foreign refresh: %v", err)
		}
		if result, err := store.ReadRepositoryRefresh(test.Context(), owner, base.Repository.ID); !errors.Is(err, ErrRepositoryNotFound) || !reflect.DeepEqual(result, RepositoryRefreshSnapshot{}) {
			test.Fatalf("foreign snapshot: %v", err)
		}
	}
	unchanged, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, base.Version.ArchiveHash, originalSkills)
	if err != nil || unchanged.Changed || unchanged.Version != base.Version {
		test.Fatalf("identical refresh changed version: %v", err)
	}
	for _, candidate := range [][]repository.ArchivedSkill{nil, {refreshSkill("skills/chosen", "c")}, {originalSkills[0], originalSkills[0]}} {
		if _, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, base.Version.ArchiveHash, candidate); !errors.Is(err, ErrInvalidCollection) {
			test.Fatalf("inconsistent same archive accepted: %v", err)
		}
	}
	publication, err := store.SetPublication(test.Context(), "alice", base.Repository.ID, 1, true, true)
	if err != nil {
		test.Fatal(err)
	}
	current, err := store.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, current.Version, strings.Repeat("c", 64), originalSkills); !errors.Is(err, ErrRepositoryRefreshPublished) {
		test.Fatalf("published archive changed: %v", err)
	}
	assertRefreshUnchanged(test, peer, current)
	if _, err := store.SetPublication(test.Context(), "alice", base.Repository.ID, publication.Revision, false, false); err != nil {
		test.Fatal(err)
	}
	current, err = store.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil {
		test.Fatal(err)
	}
	updated, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, current.Version, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d"), refreshSkill("skills/new", "e")})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := peer.SetPublication(test.Context(), "alice", base.Repository.ID, current.Version.PublicationRevision, true, true); !errors.Is(err, ErrPublicationConflict) {
		test.Fatalf("stale publication confirmation accepted: %v", err)
	}
	page, err := peer.ListPublicSkills(test.Context(), PublicSkillQuery{})
	if err != nil || len(page.Skills) != 0 {
		test.Fatalf("refresh implicitly published contents: %v", err)
	}
	if _, err := peer.SetPublication(test.Context(), "alice", base.Repository.ID, updated.Version.PublicationRevision, true, false); !errors.Is(err, ErrPublicationConfirmation) {
		test.Fatalf("confirmation bypass: %v", err)
	}
	if _, err := peer.SetPublication(test.Context(), "alice", base.Repository.ID, updated.Version.PublicationRevision, true, true); err != nil {
		test.Fatal(err)
	}
	if _, err := peer.SetPublication(test.Context(), "alice", base.Repository.ID, updated.Version.PublicationRevision+1, false, false); err != nil {
		test.Fatal(err)
	}
	back, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, RepositoryVersion{ArchiveHash: updated.Version.ArchiveHash, PublicationRevision: updated.Version.PublicationRevision + 2}, base.Version.ArchiveHash, originalSkills)
	if err != nil || back.Version.PublicationRevision != 7 {
		test.Fatalf("ABA restore: %+v %v", back.Version, err)
	}
	if _, err := peer.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, strings.Repeat("f", 64), originalSkills); !errors.Is(err, ErrRepositoryRefreshConflict) {
		test.Fatalf("ABA accepted obsolete tuple: %v", err)
	}
}

func TestRepositoryRefreshArchiveOnlyDoesNotFencePack(test *testing.T) {
	store, peer := refreshStores(test)
	selected := refreshSkill("skills/chosen", "b")
	base := refreshCollect(test, store, "org/source", selected)
	original := refreshPack(test, store, PackSelection{SkillID: base.Repository.Skills[0].ID})
	result, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, strings.Repeat("c", 64), []repository.ArchivedSkill{selected, refreshSkill("skills/new", "d")})
	if err != nil || !result.Changed || result.Added != 1 || result.Updated != 0 || len(result.Packs) != 0 {
		test.Fatalf("archive-only receipt: %+v %v", result, err)
	}
	current, err := peer.Pack(test.Context(), "alice", original.ID)
	if err != nil || current.Revision != original.Revision || current.Items[0].ContentHash != original.Items[0].ContentHash || current.ItemCount != 1 {
		test.Fatalf("new path changed membership/revision: %+v %v", current, err)
	}
}

func TestRepositoryRefreshMultipleChangedMembersFencePackOnce(test *testing.T) {
	store, peer := refreshStores(test)
	base := refreshCollect(test, store, "org/source", refreshSkill("skills/first", "b"), refreshSkill("skills/second", "b"))
	original := refreshPack(test, store, PackSelection{SkillID: base.Repository.Skills[0].ID, Slug: "shared"}, PackSelection{SkillID: base.Repository.Skills[1].ID, Slug: "shared"})
	result, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/first", "d"), refreshSkill("skills/second", "d")})
	if err != nil || result.Updated != 2 || len(result.Packs) != 1 || result.Packs[0].ID != original.ID || result.Packs[0].Revision != 2 {
		test.Fatalf("multiple members did not produce one fence: %+v %v", result, err)
	}
	current, err := peer.Pack(test.Context(), "alice", original.ID)
	if err != nil || current.Revision != 2 || current.ItemCount != 2 || current.Items[0].ContentHash != strings.Repeat("d", 64) || current.Items[1].ContentHash != strings.Repeat("d", 64) {
		test.Fatalf("pack changed more than once: %+v %v", current, err)
	}
}

func TestRepositoryRefreshCompetingTransactions(test *testing.T) {
	for _, competitor := range []string{"refresh", "publish", "pack-edit", "pack-create", "category-assign", "category-delete"} {
		test.Run(competitor, func(test *testing.T) {
			store, peer := refreshStores(test)
			base := refreshCollect(test, store, "org/source", refreshSkill("skills/chosen", "b"), refreshSkill("skills/removed", "c"))
			original := refreshPack(test, store, PackSelection{SkillID: base.Repository.Skills[0].ID})
			category, err := store.CreateCategory(test.Context(), "alice", "Concurrent")
			if err != nil {
				test.Fatal(err)
			}
			if competitor == "category-delete" {
				if _, err := store.SetSkillCategories(test.Context(), "alice", base.Repository.Skills[0].ID, []string{category.ID}); err != nil {
					test.Fatal(err)
				}
			}
			loaded, resume := make(chan struct{}), make(chan struct{})
			var once bool
			store.state.client.CollectedRepository.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
					value, err := next.Query(ctx, query)
					if !once && err == nil {
						once = true
						close(loaded)
						select {
						case <-resume:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
					return value, err
				})
			}))
			type outcome struct {
				result RepositoryRefreshResult
				err    error
			}
			finished := make(chan outcome, 1)
			go func() {
				result, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, strings.Repeat("d", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "e")})
				finished <- outcome{result, err}
			}()
			<-loaded
			switch competitor {
			case "refresh":
				_, err = peer.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, strings.Repeat("f", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "f")})
			case "publish":
				_, err = peer.SetPublication(test.Context(), "alice", base.Repository.ID, 1, true, true)
			case "pack-edit":
				_, err = peer.UpdatePack(test.Context(), "alice", original.ID, 1, PackDraft{Name: "Winner", Items: []PackSelection{{SkillID: base.Repository.Skills[0].ID, Slug: "winner"}}})
			case "pack-create":
				_, err = peer.CreatePack(test.Context(), "alice", PackDraft{Name: "Protect removed", Items: []PackSelection{{SkillID: base.Repository.Skills[1].ID}}})
			case "category-assign":
				_, err = peer.SetSkillCategories(test.Context(), "alice", base.Repository.Skills[0].ID, []string{category.ID})
			case "category-delete":
				err = peer.DeleteCategory(test.Context(), "alice", category.ID, 1)
			}
			close(resume)
			result := <-finished
			if err != nil {
				test.Fatalf("competing commit: %v", err)
			}
			switch competitor {
			case "refresh", "publish":
				if !errors.Is(result.err, ErrRepositoryRefreshConflict) {
					test.Fatalf("stale refresh overwrote competitor: %v", result.err)
				}
			case "pack-create":
				if !errors.Is(result.err, ErrReferencedSkillRemoved) {
					test.Fatalf("refresh missed concurrent reference: %v", result.err)
				}
				assertRefreshUnchanged(test, peer, base, original)
			default:
				if result.err != nil {
					test.Fatal(result.err)
				}
				current, err := peer.Pack(test.Context(), "alice", original.ID)
				if err != nil {
					test.Fatal(err)
				}
				if competitor == "pack-edit" && (current.Revision != 3 || current.Name != "Winner" || current.Items[0].Slug != "winner") {
					test.Fatal("refresh rebased incorrectly over pack edit")
				}
				categories, err := peer.SkillCategories(test.Context(), "alice", base.Repository.Skills[0].ID)
				if err != nil || competitor == "category-assign" && len(categories) != 1 || competitor == "category-delete" && len(categories) != 0 {
					test.Fatalf("category write lost: %v", err)
				}
			}
		})
	}
}

func TestRepositoryRefreshRollbackOverflowAndForeignReference(test *testing.T) {
	for _, failure := range []string{"sql", "pack-overflow", "repository-overflow", "foreign-reference", "cancel"} {
		test.Run(failure, func(test *testing.T) {
			store, peer := refreshStores(test)
			base := refreshCollect(test, store, "org/source", refreshSkill("skills/chosen", "b"))
			original := refreshPack(test, store, PackSelection{SkillID: base.Repository.Skills[0].ID})
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			switch failure {
			case "sql", "cancel":
				store.state.client.Pack.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
						value, err := next.Mutate(ctx, mutation)
						if err != nil {
							return value, err
						}
						if failure == "cancel" {
							cancel()
							return value, nil
						}
						return nil, errors.New("synthetic write failure")
					})
				})
			case "pack-overflow":
				if err := store.state.client.Pack.Update().Where(pack.PublicIDEQ(original.ID)).SetRevision(math.MaxInt64).Exec(ctx); err != nil {
					test.Fatal(err)
				}
				original.Revision = math.MaxInt64
			case "repository-overflow":
				if err := store.state.client.CollectedRepository.Update().Where(collectedrepository.PublicIDEQ(base.Repository.ID)).SetPublicationRevision(math.MaxInt64).Exec(ctx); err != nil {
					test.Fatal(err)
				}
				base.Version.PublicationRevision = math.MaxInt64
				base.Repository.PublicationRevision = math.MaxInt64
			case "foreign-reference":
				owner, err := findUser(ctx, store.state.client, "bob")
				if err != nil {
					test.Fatal(err)
				}
				foreign, err := store.state.client.Pack.Create().SetPublicID(rand.Text()).SetUserID(owner.ID).SetName("corrupt").SetItemCount(1).Save(ctx)
				if err != nil {
					test.Fatal(err)
				}
				selected, err := store.state.client.CollectedSkill.Query().Only(ctx)
				if err != nil {
					test.Fatal(err)
				}
				if err := store.state.client.PackItem.Create().SetPackID(foreign.ID).SetSkillID(selected.ID).SetSlug("foreign").Exec(ctx); err != nil {
					test.Fatal(err)
				}
			}
			result, err := store.RefreshCollectedRepository(ctx, "alice", base.Repository.ID, base.Version, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")})
			if err == nil || !reflect.DeepEqual(result, RepositoryRefreshResult{}) {
				test.Fatalf("failed write leaked success: %+v %v", result, err)
			}
			assertRefreshUnchanged(test, peer, base, original)
		})
	}
}

func TestRepositoryRefreshSnapshotReadIsAtomic(test *testing.T) {
	store, peer := refreshStores(test)
	base := refreshCollect(test, store, "org/source", refreshSkill("skills/chosen", "b"))
	interleaved := false
	var competitorError error
	store.state.client.CollectedSkill.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			if !interleaved {
				interleaved = true
				_, competitorError = peer.RefreshCollectedRepository(ctx, "alice", base.Repository.ID, base.Version, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")})
			}
			return next.Query(ctx, query)
		})
	}))
	result, err := store.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if !interleaved || competitorError != nil || err != nil || !reflect.DeepEqual(result, base) {
		test.Fatalf("snapshot mixed repository/skill generations: %v %v", err, competitorError)
	}
	current, err := peer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil || current.Version.PublicationRevision != 2 || current.Repository.Skills[0].ContentHash != strings.Repeat("d", 64) {
		test.Fatalf("competitor was not durable: %v", err)
	}
}

func TestRepositoryRefreshExistingReadCarriesPublicationVersion(test *testing.T) {
	store, peer := refreshStores(test)
	base := refreshCollect(test, store, "org/source", refreshSkill("skills/chosen", "b"))
	for _, published := range []bool{false, true} {
		if published {
			if _, err := store.SetPublication(test.Context(), "alice", base.Repository.ID, 1, true, true); err != nil {
				test.Fatal(err)
			}
		}
		current, err := peer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
		if err != nil {
			test.Fatal(err)
		}
		detail, err := peer.CollectedRepository(test.Context(), "alice", base.Repository.ID)
		if err != nil {
			test.Fatal(err)
		}
		if detail.PublicationRevision != current.Version.PublicationRevision || detail.Published != published || !reflect.DeepEqual(detail, current.Repository) {
			test.Fatal("existing Get discarded publication metadata")
		}
	}
}

func TestRepositoryRefreshMCPGrantAndCommitGate(test *testing.T) {
	for _, invalidation := range []string{"valid", "forged", "read-only", "wrong-owner", "revoke", "rotate", "expire-at-commit"} {
		test.Run(invalidation, func(test *testing.T) {
			store, peer := refreshStores(test)
			base := refreshCollect(test, store, "org/source", refreshSkill("skills/chosen", "b"))
			original := refreshPack(test, store, PackSelection{SkillID: base.Repository.Skills[0].ID})
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			store.state.now = func() time.Time { return now }
			expiry := now.Add(time.Hour)
			digest := sha256.Sum256([]byte("synthetic refresh authorization"))
			scope, owner := MCPCatalogWrite, "alice"
			if invalidation == "read-only" {
				scope = MCPCatalogRead
			}
			if invalidation == "wrong-owner" {
				owner = "bob"
			}
			credential, err := store.CreateMCPCredential(test.Context(), owner, digest[:], MCPMetadata{Name: "Refresh", Scopes: []string{scope}, ExpiresAt: expiry})
			if err != nil {
				test.Fatal(err)
			}
			grant, err := store.AuthorizeMCP(test.Context(), digest[:], scope)
			if err != nil {
				test.Fatal(err)
			}
			switch invalidation {
			case "forged":
				grant = MCPGrant{Owner: "alice", CredentialID: credential.ID, Revision: 1, Scopes: []string{MCPCatalogWrite}, ExpiresAt: expiry}
			case "revoke":
				_, err = peer.RevokeMCPCredential(test.Context(), owner, credential.ID, 1)
			case "rotate":
				replacement := sha256.Sum256([]byte("synthetic replacement"))
				_, err = peer.RotateMCPCredential(test.Context(), owner, credential.ID, 1, replacement[:])
			case "expire-at-commit":
				store.state.client.Pack.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
						value, err := next.Mutate(ctx, mutation)
						now = expiry
						return value, err
					})
				})
			}
			if err != nil {
				test.Fatal(err)
			}
			ctx := WithMCPAuthorization(test.Context(), grant, MCPCatalogWrite)
			result, err := store.RefreshCollectedRepository(ctx, "alice", base.Repository.ID, base.Version, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/chosen", "d")})
			if invalidation == "valid" {
				if err != nil || !result.Changed {
					test.Fatalf("write-only valid grant failed: %v", err)
				}
			} else {
				if !errors.Is(err, ErrMCPForbidden) && !errors.Is(err, ErrMCPUnauthorized) {
					test.Fatalf("invalid grant bypassed gate: %v", err)
				}
				if !reflect.DeepEqual(result, RepositoryRefreshResult{}) {
					test.Fatal("invalid grant leaked success receipt")
				}
				assertRefreshUnchanged(test, peer, base, original)
			}
		})
	}
}

func refreshSkill(path, hash string) repository.ArchivedSkill {
	return repository.ArchivedSkill{Skill: skill.Skill{Path: path, Name: "Shared", Description: "Useful skill"}, ContentHash: strings.Repeat(hash, 64)}
}

func refreshCollect(test *testing.T, store *Store, name string, skills ...repository.ArchivedSkill) RepositoryRefreshSnapshot {
	test.Helper()
	reference, err := repository.ParseReference(name, "")
	if err != nil {
		test.Fatal(err)
	}
	entry, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("a", 64), skills)
	if err != nil {
		test.Fatal(err)
	}
	result, err := store.ReadRepositoryRefresh(test.Context(), "alice", entry.ID)
	if err != nil {
		test.Fatal(err)
	}
	return result
}

func refreshPack(test *testing.T, store *Store, selections ...PackSelection) Pack {
	test.Helper()
	result, err := store.CreatePack(test.Context(), "alice", PackDraft{Name: "Chosen", Items: selections})
	if err != nil {
		test.Fatal(err)
	}
	return result
}

func TestRepositoryRefreshPreservesIdentityCategoriesAndFencesPacks(test *testing.T) {
	store, observer := refreshStores(test)
	base := refreshCollect(test, store, "org/source", refreshSkill("skills/kept", "b"), refreshSkill("skills/removed", "c"))
	kept, removed := base.Repository.Skills[0], base.Repository.Skills[1]
	category, err := store.CreateCategory(test.Context(), "alice", "Keep")
	if err != nil {
		test.Fatal(err)
	}
	for _, selected := range []string{kept.ID, removed.ID} {
		if _, err := store.SetSkillCategories(test.Context(), "alice", selected, []string{category.ID}); err != nil {
			test.Fatal(err)
		}
	}
	first := refreshPack(test, store, PackSelection{SkillID: kept.ID, Slug: "chosen"})
	second := refreshPack(test, store, PackSelection{SkillID: kept.ID, Slug: "other"})
	other := refreshCollect(test, store, "org/unrelated", refreshSkill("skills/other", "c"))
	unaffected := refreshPack(test, store, PackSelection{SkillID: other.Repository.Skills[0].ID})
	candidate := refreshSkill("skills/kept", "d")
	candidate.Name, candidate.Description = "Renamed title", "Updated description"
	result, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, strings.Repeat("e", 64), []repository.ArchivedSkill{candidate, refreshSkill("skills/added", "f")})
	if err != nil {
		test.Fatal(err)
	}
	if !result.Changed || result.Added != 1 || result.Updated != 1 || result.Removed != 1 || len(result.Packs) != 2 || result.Version.PublicationRevision != 2 {
		test.Fatalf("wrong receipt: %+v", result)
	}
	read, err := observer.ReadRepositoryRefresh(test.Context(), "alice", base.Repository.ID)
	if err != nil || !reflect.DeepEqual(read.Repository, result.Repository) || read.Version != result.Version {
		test.Fatalf("receipt differs from commit: %v", err)
	}
	if read.Repository.Skills[1].ID != kept.ID || read.Repository.CollectedAt != base.Repository.CollectedAt || read.Repository.Manual != base.Repository.Manual {
		test.Fatal("identity or provenance changed")
	}
	categories, err := observer.SkillCategories(test.Context(), "alice", kept.ID)
	if err != nil || len(categories) != 1 || categories[0].ID != category.ID || categories[0].SkillCount != 1 {
		test.Fatalf("category changed: %+v %v", categories, err)
	}
	addedCategories, err := observer.SkillCategories(test.Context(), "alice", read.Repository.Skills[0].ID)
	if err != nil || len(addedCategories) != 0 {
		test.Fatalf("new path inherited categories: %v", err)
	}
	for _, original := range []Pack{first, second} {
		updated, err := observer.Pack(test.Context(), "alice", original.ID)
		if err != nil || updated.Revision != 2 || updated.ItemCount != 1 || updated.Items[0].SkillID != kept.ID || updated.Items[0].Slug != original.Items[0].Slug || updated.Items[0].ContentHash != candidate.ContentHash {
			test.Fatalf("pack not fenced: %+v %v", updated, err)
		}
		if _, err := observer.UpdatePack(test.Context(), "alice", original.ID, 1, PackDraft{Name: "stale", Items: []PackSelection{{SkillID: kept.ID}}}); err == nil {
			test.Fatal("stale pack edit accepted")
		}
	}
	unchanged, err := observer.Pack(test.Context(), "alice", unaffected.ID)
	if err != nil || !reflect.DeepEqual(unchanged, unaffected) {
		test.Fatalf("unrelated pack changed: %v", err)
	}
}

func TestIndependentSourceRefreshOrphanPackReference(test *testing.T) {
	directory := test.TempDir()
	if os.Chmod(directory, 0o700) != nil {
		test.Fatal("private_orphan_fixture=false")
	}
	database := filepath.Join(directory, "state.sqlite")
	store, err := Open(test.Context(), database, make([]byte, 32))
	if err != nil {
		test.Fatal("orphan_fixture_open=false")
	}
	defer store.Close()
	if store.CreateUser(test.Context(), "independent-owner") != nil {
		test.Fatal("orphan_fixture_owner=false")
	}
	reference, err := repository.ParseReference("oracle/orphan", "HEAD")
	if err != nil {
		test.Fatal("orphan_fixture_reference=false")
	}
	skills := []repository.ArchivedSkill{{Path: "skills/kept", Name: "Kept", Description: "Synthetic", ContentHash: strings.Repeat("a", 64)}}
	collected, err := store.CollectRepository(test.Context(), "independent-owner", reference, strings.Repeat("b", 64), skills)
	if err != nil {
		test.Fatal("orphan_fixture_collected=false")
	}
	before, err := store.CollectedRepository(test.Context(), "independent-owner", collected.ID)
	if err != nil {
		test.Fatal("orphan_before_snapshot=false")
	}
	selected, err := store.state.client.CollectedSkill.Query().Where(collectedskill.PublicIDEQ(before.Skills[0].ID)).Only(test.Context())
	if err != nil {
		test.Fatal("orphan_fixture_selected=false")
	}
	corrupt, err := sql.Open("sqlite3", database)
	if err != nil {
		test.Fatal("orphan_fault_connection=false")
	}
	defer corrupt.Close()
	corrupt.SetMaxOpenConns(1)
	if _, err := corrupt.ExecContext(test.Context(), "PRAGMA foreign_keys=OFF"); err != nil {
		test.Fatal("orphan_fault_fk_disabled=false")
	}
	if _, err := corrupt.ExecContext(test.Context(), "INSERT INTO pack_items (pack_id,skill_id,slug) VALUES (?,?,?)", 999999, selected.ID, "orphan"); err != nil {
		test.Fatal("orphan_fault_inserted=false")
	}
	var failures int
	rows, err := corrupt.QueryContext(test.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		test.Fatal("orphan_corruption_observed=false")
	}
	for rows.Next() {
		failures++
	}
	if rows.Close() != nil || failures != 1 {
		test.Fatal("orphan_fixture_exactly_one_fk_failure=false")
	}
	skills[0].ContentHash = strings.Repeat("c", 64)
	result, err := store.RefreshCollectedRepository(test.Context(), "independent-owner", collected.ID, RepositoryVersion{ArchiveHash: before.ArchiveHash, PublicationRevision: before.PublicationRevision}, strings.Repeat("d", 64), skills)
	after, readErr := store.CollectedRepository(test.Context(), "independent-owner", collected.ID)
	test.Logf("orphan_refresh_rejected=%t zero_receipt=%t repository_unchanged=%t", err != nil, reflect.DeepEqual(result, RepositoryRefreshResult{}), readErr == nil && reflect.DeepEqual(before, after))
	if err == nil || !reflect.DeepEqual(result, RepositoryRefreshResult{}) || readErr != nil || !reflect.DeepEqual(before, after) {
		test.Fatal("orphan_reference_failclosed_rollback=false")
	}
}

func TestRepositoryRefreshOrphanReferencesIncludeUnchangedSkillsAndNoop(test *testing.T) {
	for _, mode := range []string{"archive-only", "noop"} {
		test.Run(mode, func(test *testing.T) {
			directory := test.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			path := filepath.Join(directory, "server.db")
			store, err := Open(test.Context(), path, make([]byte, 32))
			if err != nil {
				test.Fatal(err)
			}
			defer store.Close()
			if err := store.CreateUser(test.Context(), "alice"); err != nil {
				test.Fatal(err)
			}
			base := refreshCollect(test, store, "org/source", refreshSkill("skills/unchanged", "b"))
			selected, err := store.state.client.CollectedSkill.Query().Only(test.Context())
			if err != nil {
				test.Fatal(err)
			}
			corrupt, err := sql.Open("sqlite3", path)
			if err != nil {
				test.Fatal(err)
			}
			defer corrupt.Close()
			corrupt.SetMaxOpenConns(1)
			if _, err := corrupt.ExecContext(test.Context(), "PRAGMA foreign_keys=OFF"); err != nil {
				test.Fatal(err)
			}
			if _, err := corrupt.ExecContext(test.Context(), "INSERT INTO pack_items (pack_id,skill_id,slug) VALUES (?,?,?)", 999999, selected.ID, "orphan"); err != nil {
				test.Fatal(err)
			}
			var violations int
			if err := corrupt.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil || violations != 1 {
				test.Fatalf("missing fault injection: count=%d error=%v", violations, err)
			}
			archiveHash := base.Version.ArchiveHash
			if mode == "archive-only" {
				archiveHash = strings.Repeat("c", 64)
			}
			result, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, archiveHash, []repository.ArchivedSkill{refreshSkill("skills/unchanged", "b")})
			if !errors.Is(err, ErrPackSkillUnavailable) || !reflect.DeepEqual(result, RepositoryRefreshResult{}) {
				test.Fatalf("orphan reference was not rejected: %v", err)
			}
			assertRefreshUnchanged(test, store, base)
		})
	}
}

func TestRepositoryRefreshDamagedPackReferenceFailsClosed(test *testing.T) {
	for _, damage := range []string{"foreign-owner", "invalid-id", "invalid-revision", "invalid-count", "invalid-slug", "missing-skill"} {
		test.Run(damage, func(test *testing.T) {
			directory := test.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			path := filepath.Join(directory, "server.db")
			store, err := Open(test.Context(), path, make([]byte, 32))
			if err != nil {
				test.Fatal(err)
			}
			defer store.Close()
			for _, owner := range []string{"alice", "bob"} {
				if err := store.CreateUser(test.Context(), owner); err != nil {
					test.Fatal(err)
				}
			}
			base := refreshCollect(test, store, "org/source", refreshSkill("skills/chosen", "b"))
			original := refreshPack(test, store, PackSelection{SkillID: base.Repository.Skills[0].ID})
			corrupt, err := sql.Open("sqlite3", path)
			if err != nil {
				test.Fatal(err)
			}
			defer corrupt.Close()
			corrupt.SetMaxOpenConns(1)
			if _, err := corrupt.ExecContext(test.Context(), "PRAGMA foreign_keys=OFF"); err != nil {
				test.Fatal(err)
			}
			var query string
			arguments := []any{original.ID}
			switch damage {
			case "foreign-owner":
				owner, err := findUser(test.Context(), store.state.client, "bob")
				if err != nil {
					test.Fatal(err)
				}
				query, arguments = "UPDATE packs SET user_id=? WHERE public_id=?", []any{owner.ID, original.ID}
			case "invalid-id":
				query = "UPDATE packs SET public_id='invalid' WHERE public_id=?"
			case "invalid-revision":
				query = "UPDATE packs SET revision=0 WHERE public_id=?"
			case "invalid-count":
				query = "UPDATE packs SET item_count=2 WHERE public_id=?"
			case "invalid-slug":
				query = "UPDATE pack_items SET slug='../invalid' WHERE pack_id=(SELECT id FROM packs WHERE public_id=?)"
			case "missing-skill":
				if _, err := corrupt.ExecContext(test.Context(), "UPDATE packs SET item_count=2 WHERE public_id=?", original.ID); err != nil {
					test.Fatal(err)
				}
				query = "INSERT INTO pack_items (pack_id,skill_id,slug) SELECT id,999999,'missing' FROM packs WHERE public_id=?"
			}
			injected, err := corrupt.ExecContext(test.Context(), query, arguments...)
			if err != nil {
				test.Fatal(err)
			}
			if changed, err := injected.RowsAffected(); err != nil || changed != 1 {
				test.Fatalf("damage injection did not affect one row: %v", err)
			}
			for _, archiveHash := range []string{base.Version.ArchiveHash, strings.Repeat("c", 64)} {
				result, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, archiveHash, []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")})
				if !errors.Is(err, ErrPackSkillUnavailable) || !reflect.DeepEqual(result, RepositoryRefreshResult{}) {
					test.Fatalf("damaged reference accepted: %v", err)
				}
				assertRefreshUnchanged(test, store, base)
			}
		})
	}
}

func TestRepositoryRefreshHealthyNoopDoesNotWriteReferences(test *testing.T) {
	store, peer := refreshStores(test)
	base := refreshCollect(test, store, "org/source", refreshSkill("skills/chosen", "b"))
	original := refreshPack(test, store, PackSelection{SkillID: base.Repository.Skills[0].ID})
	store.state.client.Use(func(ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
			return nil, errors.New("unexpected mutation during healthy no-op")
		})
	})
	result, err := store.RefreshCollectedRepository(test.Context(), "alice", base.Repository.ID, base.Version, base.Version.ArchiveHash, []repository.ArchivedSkill{refreshSkill("skills/chosen", "b")})
	if err != nil || result.Changed || result.Version != base.Version || len(result.Packs) != 0 {
		test.Fatalf("healthy no-op wrote references: %v", err)
	}
	assertRefreshUnchanged(test, peer, base, original)
}
