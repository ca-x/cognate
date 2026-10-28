package state_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/skillhub/internal/server/state"
)

func TestCategoryOwnershipRevisionAndSkillIdentity(test *testing.T) {
	store, skills := packFixture(test)
	ctx := test.Context()
	created, err := store.CreateCategory(ctx, "alice", "  Development  Tools ")
	if err != nil || created.Name != "Development Tools" || created.Revision != 1 {
		test.Fatalf("create: %+v %v", created, err)
	}
	if _, err := store.CreateCategory(ctx, "alice", "development tools"); !errors.Is(err, state.ErrCategoryExists) {
		test.Fatalf("normalized duplicate: %v", err)
	}
	foreign, err := store.CreateCategory(ctx, "bob", "Development Tools")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.CreateCategory(ctx, "alice", "e\u0301"); err != nil {
		test.Fatal(err)
	}
	if _, err := store.CreateCategory(ctx, "alice", "É"); !errors.Is(err, state.ErrCategoryExists) {
		test.Fatalf("Unicode normalized duplicate: %v", err)
	}
	for _, name := range []string{"", "   ", strings.Repeat("界", 27), "bad\x00name", string([]byte{255})} {
		if _, err := store.CreateCategory(ctx, "alice", name); !errors.Is(err, state.ErrCategoryInvalid) {
			test.Fatalf("invalid name accepted: %q %v", name, err)
		}
	}
	if _, err := store.UpdateCategory(ctx, "bob", created.ID, 1, "stolen"); !errors.Is(err, state.ErrCategoryNotFound) {
		test.Fatalf("foreign update: %v", err)
	}
	if _, err := store.SetSkillCategories(ctx, "alice", skills[0], []string{created.ID, foreign.ID}); !errors.Is(err, state.ErrCategoryNotFound) {
		test.Fatalf("foreign association: %v", err)
	}
	if _, err := store.SetSkillCategories(ctx, "bob", skills[0], []string{foreign.ID}); !errors.Is(err, state.ErrCategorySkillUnavailable) {
		test.Fatalf("foreign skill: %v", err)
	}
	selected, err := store.SetSkillCategories(ctx, "alice", skills[0], []string{created.ID})
	if err != nil || len(selected) != 1 || selected[0].SkillCount != 1 {
		test.Fatalf("associate: %+v %v", selected, err)
	}
	if _, err := store.SetSkillCategories(ctx, "alice", skills[0], []string{foreign.ID}); !errors.Is(err, state.ErrCategoryNotFound) {
		test.Fatalf("replace foreign: %v", err)
	}
	selected, err = store.SkillCategories(ctx, "alice", skills[0])
	if err != nil || len(selected) != 1 || selected[0].ID != created.ID {
		test.Fatalf("failed replacement changed associations: %+v %v", selected, err)
	}
	updated, err := store.UpdateCategory(ctx, "alice", created.ID, 1, "Engineering")
	if err != nil || updated.Revision != 2 || updated.SkillCount != 1 {
		test.Fatalf("update: %+v %v", updated, err)
	}
	if _, err := store.UpdateCategory(ctx, "alice", created.ID, 1, "stale"); !errors.Is(err, state.ErrCategoryConflict) {
		test.Fatalf("stale update: %v", err)
	}
	if err := store.DeleteCategory(ctx, "bob", created.ID, 2); !errors.Is(err, state.ErrCategoryNotFound) {
		test.Fatalf("foreign delete: %v", err)
	}
	if err := store.DeleteCategory(ctx, "alice", created.ID, 1); !errors.Is(err, state.ErrCategoryConflict) {
		test.Fatalf("stale delete: %v", err)
	}
	if err := store.DeleteCategory(ctx, "alice", created.ID, 2); err != nil {
		test.Fatal(err)
	}
	selected, err = store.SkillCategories(ctx, "alice", skills[0])
	if err != nil || len(selected) != 0 {
		test.Fatalf("delete did not unlink: %+v %v", selected, err)
	}
	repositories, err := store.ListCollectedRepositories(ctx, "alice")
	if err != nil || len(repositories) != 3 {
		test.Fatalf("category deletion removed content: %+v %v", repositories, err)
	}
	for _, repo := range repositories {
		detail, err := store.CollectedRepository(ctx, "alice", repo.ID)
		if err != nil || len(detail.Skills) != 1 || detail.Skills[0].ContentHash != map[bool]string{true: strings.Repeat("b", 64), false: strings.Repeat("a", 64)}[repo.NameWithOwner == "org/conflict"] {
			test.Fatalf("skill identity changed: %+v %v", detail, err)
		}
	}
}

func TestCategoryBounds(test *testing.T) {
	store, skills := packFixture(test)
	var selection []string
	for index := range 100 {
		entry, err := store.CreateCategory(test.Context(), "alice", fmt.Sprintf("Category %03d", index))
		if err != nil {
			test.Fatal(err)
		}
		if index < 9 {
			selection = append(selection, entry.ID)
		}
	}
	if _, err := store.CreateCategory(test.Context(), "alice", "overflow"); !errors.Is(err, state.ErrCategoryLimit) {
		test.Fatalf("category capacity: %v", err)
	}
	if _, err := store.SetSkillCategories(test.Context(), "alice", skills[0], selection); !errors.Is(err, state.ErrCategoryInvalid) {
		test.Fatalf("skill category capacity: %v", err)
	}
	if _, err := store.SetSkillCategories(test.Context(), "alice", skills[0], selection[:8]); err != nil {
		test.Fatal(err)
	}
	if _, err := store.SetSkillCategories(test.Context(), "alice", skills[0], []string{selection[0], selection[0]}); !errors.Is(err, state.ErrCategoryInvalid) {
		test.Fatalf("duplicate selection: %v", err)
	}
}

func TestCategoryConcurrentCAS(test *testing.T) {
	store, _ := packFixture(test)
	created, err := store.CreateCategory(test.Context(), "alice", "Concurrent")
	if err != nil {
		test.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for index := range 8 {
		workers.Go(func() {
			<-start
			_, err := store.UpdateCategory(test.Context(), "alice", created.ID, 1, fmt.Sprintf("Updated %d", index))
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, state.ErrCategoryConflict) {
			test.Errorf("concurrent CAS did not report conflict: %v", err)
		}
	}
	if winners != 1 {
		test.Fatalf("CAS winners=%d", winners)
	}
}
