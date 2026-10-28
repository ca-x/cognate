package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/skill"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func catalogCASFixture(test *testing.T) (*Store, Category, CollectedRepository) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := Open(test.Context(), filepath.Join(directory, "retry.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	created, err := store.CreateCategory(test.Context(), "alice", "Original")
	if err != nil {
		test.Fatal(err)
	}
	reference, err := repository.ParseReference("org/retry", "")
	if err != nil {
		test.Fatal(err)
	}
	collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Retry", Description: "Metadata only", Path: "skill"}, ContentHash: strings.Repeat("a", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	collected, err = store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil || len(collected.Skills) != 1 {
		test.Fatalf("collected skill fixture: %+v error=%v", collected, err)
	}
	return store, created, collected
}

func interceptCatalogCASRead(store *Store, operation string, afterRead func()) func() {
	active, interleaving := true, false
	interceptor := ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if err == nil && active && !interleaving {
				interleaving = true
				afterRead()
				interleaving = false
			}
			return value, err
		})
	})
	if operation == "publication" {
		store.state.client.CollectedRepository.Intercept(interceptor)
	} else {
		store.state.client.Category.Intercept(interceptor)
	}
	return func() { active = false }
}

func mutateCatalogCAS(ctx context.Context, store *Store, operation string, created Category, collected CollectedRepository) error {
	switch operation {
	case "category-update":
		_, err := store.UpdateCategory(ctx, "alice", created.ID, 1, "Requested")
		return err
	case "category-delete":
		return store.DeleteCategory(ctx, "alice", created.ID, 1)
	default:
		_, err := store.SetPublication(ctx, "alice", collected.ID, 1, true, true)
		return err
	}
}

func assertCatalogCASUnchanged(test *testing.T, store *Store, created Category, collected CollectedRepository) {
	test.Helper()
	entries, err := store.ListCategories(test.Context(), "alice")
	if err != nil {
		test.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.ID == created.ID {
			found = true
			if entry.Name != "Original" || entry.Revision != 1 {
				test.Fatalf("failed mutation changed category: %+v", entry)
			}
		}
	}
	if !found {
		test.Fatal("failed mutation deleted category")
	}
	publication, err := store.Publication(test.Context(), "alice", collected.ID)
	if err != nil || publication.Published || publication.Revision != 1 {
		test.Fatalf("failed mutation changed publication: %+v error=%v", publication, err)
	}
}

func TestPublicCASAfterUnrelatedCommit(test *testing.T) {
	for _, operation := range []string{"category-update", "category-delete", "publication"} {
		test.Run(operation, func(test *testing.T) {
			store, created, collected := catalogCASFixture(test)
			other, err := store.CreateCategory(test.Context(), "alice", "Other")
			if err != nil {
				test.Fatal(err)
			}
			reads := 0
			stop := interceptCatalogCASRead(store, operation, func() {
				reads++
				if reads == 1 {
					if _, err := store.UpdateCategory(test.Context(), "alice", other.ID, 1, "Other winner"); err != nil {
						test.Fatal(err)
					}
				}
			})
			err = mutateCatalogCAS(test.Context(), store, operation, created, collected)
			stop()
			if err != nil || reads != 2 {
				test.Fatalf("unrelated commit must retry, not conflict: reads=%d error=%v", reads, err)
			}
			entries, err := store.ListCategories(test.Context(), "alice")
			if err != nil {
				test.Fatal(err)
			}
			found := false
			foundOther := false
			for _, entry := range entries {
				if entry.ID == other.ID {
					foundOther = true
					if entry.Name != "Other winner" || entry.Revision != 2 {
						test.Fatalf("competing commit changed: %+v", entry)
					}
				}
				if entry.ID == created.ID {
					found = true
					if operation == "category-update" && (entry.Name != "Requested" || entry.Revision != 2) {
						test.Fatalf("requested rename not committed once: %+v", entry)
					}
				}
			}
			if !foundOther || found == (operation == "category-delete") {
				test.Fatalf("category presence after %s: target=%t other=%t", operation, found, foundOther)
			}
			expectedRevision := int64(1)
			if operation == "publication" {
				expectedRevision = 2
			}
			publication, err := store.Publication(test.Context(), "alice", collected.ID)
			if err != nil || publication.Published != (operation == "publication") || publication.Revision != expectedRevision {
				test.Fatalf("publication after retry: %+v error=%v", publication, err)
			}
		})
	}
}

func TestPublicCASRetryBound(test *testing.T) {
	for _, operation := range []string{"category-update", "category-delete", "publication"} {
		test.Run(operation, func(test *testing.T) {
			store, created, collected := catalogCASFixture(test)
			other, err := store.CreateCategory(test.Context(), "alice", "Other")
			if err != nil {
				test.Fatal(err)
			}
			reads := 0
			stop := interceptCatalogCASRead(store, operation, func() {
				reads++
				other, err = store.UpdateCategory(test.Context(), "alice", other.ID, other.Revision, fmt.Sprintf("Other winner %d", reads))
				if err != nil {
					test.Fatal(err)
				}
			})
			ctx, cancel := context.WithTimeout(test.Context(), time.Second)
			defer cancel()
			err = mutateCatalogCAS(ctx, store, operation, created, collected)
			stop()
			sqliteError, ok := errors.AsType[*sqlite.Error](err)
			if reads != 4 || !ok || sqliteError.Code() != sqlite3.SQLITE_BUSY_SNAPSHOT {
				test.Fatalf("retry budget must preserve contention, not claim stale revision: reads=%d error=%v", reads, err)
			}
			if other.Revision != 5 {
				test.Fatalf("competing transactions did not all commit: %+v", other)
			}
			assertCatalogCASUnchanged(test, store, created, collected)
		})
	}
}

func TestPublicCASRetryCancellation(test *testing.T) {
	for _, operation := range []string{"category-update", "category-delete", "publication"} {
		for _, cancelAfter := range []int{0, 1, 2} {
			test.Run(fmt.Sprintf("%s/after-%d-reads", operation, cancelAfter), func(test *testing.T) {
				store, created, collected := catalogCASFixture(test)
				other, err := store.CreateCategory(test.Context(), "alice", "Other")
				if err != nil {
					test.Fatal(err)
				}
				ctx, cancel := context.WithCancel(test.Context())
				defer cancel()
				reads := 0
				stop := interceptCatalogCASRead(store, operation, func() {
					reads++
					other, err = store.UpdateCategory(test.Context(), "alice", other.ID, other.Revision, fmt.Sprintf("Other winner %d", reads))
					if err != nil {
						test.Fatal(err)
					}
					if reads == cancelAfter {
						cancel()
					}
				})
				if cancelAfter == 0 {
					cancel()
				}
				err = mutateCatalogCAS(ctx, store, operation, created, collected)
				stop()
				if !errors.Is(err, context.Canceled) || reads != cancelAfter {
					test.Fatalf("cancellation must stop transactions: reads=%d error=%v", reads, err)
				}
				assertCatalogCASUnchanged(test, store, created, collected)
			})
		}
	}
}

func TestPublicCASRetryDeadlineDuringBackoff(test *testing.T) {
	for _, operation := range []string{"category-update", "category-delete", "publication"} {
		test.Run(operation, func(test *testing.T) {
			synctest.Test(test, func(test *testing.T) {
				store, created, collected := catalogCASFixture(test)
				other, err := store.CreateCategory(test.Context(), "alice", "Other")
				if err != nil {
					test.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(test.Context(), 500*time.Microsecond)
				defer cancel()
				reads := 0
				stop := interceptCatalogCASRead(store, operation, func() {
					reads++
					if _, err := store.UpdateCategory(test.Context(), "alice", other.ID, 1, "Other winner"); err != nil {
						test.Fatal(err)
					}
				})
				err = mutateCatalogCAS(ctx, store, operation, created, collected)
				stop()
				if !errors.Is(err, context.DeadlineExceeded) || reads != 1 {
					test.Fatalf("deadline must interrupt retry backoff: reads=%d error=%v", reads, err)
				}
				assertCatalogCASUnchanged(test, store, created, collected)
			})
		})
	}
}

func TestPublicCASRetryRevalidatesUniqueName(test *testing.T) {
	store, created, collected := catalogCASFixture(test)
	reads := 0
	stop := interceptCatalogCASRead(store, "category-update", func() {
		reads++
		if reads == 1 {
			if _, err := store.CreateCategory(test.Context(), "alice", "Requested"); err != nil {
				test.Fatal(err)
			}
		}
	})
	err := mutateCatalogCAS(test.Context(), store, "category-update", created, collected)
	stop()
	if !errors.Is(err, ErrCategoryExists) || reads != 2 {
		test.Fatalf("fresh transaction must revalidate uniqueness: reads=%d error=%v", reads, err)
	}
	assertCatalogCASUnchanged(test, store, created, collected)
}

func TestPublicCASNonRetryableErrorRollsBack(test *testing.T) {
	for _, operation := range []string{"category-update", "category-delete", "publication"} {
		test.Run(operation, func(test *testing.T) {
			store, created, collected := catalogCASFixture(test)
			if _, err := store.SetSkillCategories(test.Context(), "alice", collected.Skills[0].ID, []string{created.ID}); err != nil {
				test.Fatal(err)
			}
			failure := errors.New("database is locked (517)")
			writes := 0
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					value, err := next.Mutate(ctx, mutation)
					if err != nil {
						return value, err
					}
					writes++
					return nil, failure
				})
			}
			if operation == "publication" {
				store.state.client.CollectedRepository.Use(hook)
			} else {
				store.state.client.Category.Use(hook)
			}
			err := mutateCatalogCAS(test.Context(), store, operation, created, collected)
			if !errors.Is(err, failure) || writes != 1 {
				test.Fatalf("non-SQLite error must not retry or become a conflict: writes=%d error=%v", writes, err)
			}
			assertCatalogCASUnchanged(test, store, created, collected)
			selected, err := store.SkillCategories(test.Context(), "alice", collected.Skills[0].ID)
			if err != nil || len(selected) != 1 || selected[0].ID != created.ID || selected[0].SkillCount != 1 {
				test.Fatalf("failed mutation changed associations: %+v error=%v", selected, err)
			}
		})
	}
}
