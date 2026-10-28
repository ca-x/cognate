package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/skill"
)

func TestRepresentativePublicCASAfterCompetingCommit(test *testing.T) {
	for _, operation := range []string{"category-update", "category-delete", "publication"} {
		test.Run(operation, func(test *testing.T) {
			directory := test.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			store, err := Open(test.Context(), filepath.Join(directory, "review.db"), make([]byte, 32))
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
			reference, err := repository.ParseReference("org/review", "")
			if err != nil {
				test.Fatal(err)
			}
			collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Review", Description: "Metadata only", Path: "skill"}, ContentHash: strings.Repeat("a", 64)}})
			if err != nil {
				test.Fatal(err)
			}
			interleaved := false
			var competitorError error
			interceptor := ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
					value, err := next.Query(ctx, query)
					if err == nil && !interleaved {
						interleaved = true
						if operation == "publication" {
							_, competitorError = store.SetPublication(test.Context(), "alice", collected.ID, 1, true, true)
						} else {
							_, competitorError = store.UpdateCategory(test.Context(), "alice", created.ID, 1, "Winner")
						}
					}
					return value, err
				})
			})
			var observed error
			expected := ErrCategoryConflict
			if operation == "publication" {
				store.state.client.CollectedRepository.Intercept(interceptor)
				_, observed = store.SetPublication(test.Context(), "alice", collected.ID, 1, true, true)
				expected = ErrPublicationConflict
			} else {
				store.state.client.Category.Intercept(interceptor)
				if operation == "category-update" {
					_, observed = store.UpdateCategory(test.Context(), "alice", created.ID, 1, "Loser")
				} else {
					observed = store.DeleteCategory(test.Context(), "alice", created.ID, 1)
				}
			}
			if !interleaved || competitorError != nil {
				test.Fatalf("competing commit did not occur: interleaved=%t error=%v", interleaved, competitorError)
			}
			if !errors.Is(observed, expected) {
				test.Errorf("stale transaction after committed competitor: got %v; want %v", observed, expected)
			}
			if operation == "publication" {
				current, err := store.Publication(test.Context(), "alice", collected.ID)
				if err != nil || !current.Published || current.Revision != 2 {
					test.Fatalf("winning publication changed: %+v error=%v", current, err)
				}
			} else {
				current, err := store.ListCategories(test.Context(), "alice")
				if err != nil || len(current) != 1 || current[0].Name != "Winner" || current[0].Revision != 2 {
					test.Fatalf("winning category changed: %+v error=%v", current, err)
				}
			}
		})
	}
}
