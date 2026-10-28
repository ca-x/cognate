package state_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
)

func TestPublicCatalogPrivateIsolationCountsAndKeysetPages(test *testing.T) {
	store, skills := packFixture(test)
	ctx := test.Context()
	shared, err := store.CreateCategory(ctx, "alice", "Shared")
	if err != nil {
		test.Fatal(err)
	}
	other, err := store.CreateCategory(ctx, "alice", "Other")
	if err != nil {
		test.Fatal(err)
	}
	hidden, err := store.CreateCategory(ctx, "alice", "Private category")
	if err != nil {
		test.Fatal(err)
	}
	foreign, err := store.CreateCategory(ctx, "bob", "Private foreign category")
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range []struct {
		owner string
		skill string
		ids   []string
	}{
		{"alice", skills[0], []string{shared.ID}},
		{"alice", skills[1], []string{shared.ID, hidden.ID}},
		{"alice", skills[2], []string{other.ID}},
		{"bob", skills[3], []string{foreign.ID}},
	} {
		if _, err := store.SetSkillCategories(ctx, entry.owner, entry.skill, entry.ids); err != nil {
			test.Fatal(err)
		}
	}
	repositories, err := store.ListCollectedRepositories(ctx, "alice")
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range repositories {
		if entry.NameWithOwner != "org/second" {
			if _, err := store.SetPublication(ctx, "alice", entry.ID, 1, true, true); err != nil {
				test.Fatal(err)
			}
		}
	}
	first, err := store.ListPublicSkills(ctx, state.PublicSkillQuery{Limit: 1})
	if err != nil || len(first.Skills) != 1 || first.NextCursor == "" {
		test.Fatalf("first page: %+v %v", first, err)
	}
	second, err := store.ListPublicSkills(ctx, state.PublicSkillQuery{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Skills) != 1 || second.NextCursor != "" || first.Skills[0].ID >= second.Skills[0].ID {
		test.Fatalf("keyset continuation: %+v %v", second, err)
	}
	for _, entry := range append(first.Skills, second.Skills...) {
		if entry.ID == skills[1] || entry.ID == skills[3] || len(entry.Categories) != 1 || entry.Categories[0].SkillCount != 1 || entry.PublicationRevision != 2 || entry.RepositoryID == "" || entry.RepositoryName == "" || entry.Slug != "shared-skill" || entry.Path != "skills/shared" {
			test.Fatalf("unsafe metadata: %+v", entry)
		}
	}
	for _, input := range []struct {
		query state.PublicSkillQuery
		count int
	}{
		{state.PublicSkillQuery{Query: "SHARED"}, 2},
		{state.PublicSkillQuery{Query: "useful"}, 2},
		{state.PublicSkillQuery{Query: "org/first"}, 1},
		{state.PublicSkillQuery{Query: "no matching skill"}, 0},
		{state.PublicSkillQuery{CategoryID: shared.ID}, 1},
		{state.PublicSkillQuery{CategoryID: hidden.ID}, 0},
		{state.PublicSkillQuery{CategoryID: foreign.ID}, 0},
	} {
		page, err := store.ListPublicSkills(ctx, input.query)
		if err != nil || len(page.Skills) != input.count {
			test.Fatalf("query %+v: %+v %v", input.query, page, err)
		}
	}
	page, err := store.ListPublicCategories(ctx, 1, "")
	if err != nil || len(page.Categories) != 1 || page.NextCursor == "" || page.Categories[0].SkillCount != 1 {
		test.Fatalf("categories first: %+v %v", page, err)
	}
	next, err := store.ListPublicCategories(ctx, 1, page.NextCursor)
	if err != nil || len(next.Categories) != 1 || next.NextCursor != "" || next.Categories[0].ID <= page.Categories[0].ID || next.Categories[0].SkillCount != 1 {
		test.Fatalf("categories next: %+v %v", next, err)
	}
	for _, invalid := range []state.PublicSkillQuery{
		{Limit: 101}, {Query: strings.Repeat("x", 129)}, {Query: string([]byte{255})}, {Query: "bad\x00query"},
		{CategoryID: "bad"}, {Cursor: "not base64"}, {Cursor: strings.Repeat("x", 257)},
		{Query: "changed", Cursor: first.NextCursor}, {CategoryID: shared.ID, Cursor: first.NextCursor}, {Cursor: page.NextCursor},
	} {
		if _, err := store.ListPublicSkills(ctx, invalid); !errors.Is(err, state.ErrPublicCatalogInvalid) {
			test.Fatalf("invalid query accepted %+v: %v", invalid, err)
		}
	}
	for _, cursor := range []string{"invalid", strings.Repeat("x", 257), first.NextCursor} {
		if _, err := store.ListPublicCategories(ctx, 1, cursor); !errors.Is(err, state.ErrPublicCatalogInvalid) {
			test.Fatalf("invalid category cursor accepted: %v", err)
		}
	}
	if _, err := store.ListPublicCategories(ctx, 101, ""); !errors.Is(err, state.ErrPublicCatalogInvalid) {
		test.Fatalf("category limit: %v", err)
	}
	if err := store.DeleteCategory(ctx, "alice", shared.ID, 1); err != nil {
		test.Fatal(err)
	}
	detail, err := store.PublicSkill(ctx, skills[0])
	if err != nil || len(detail.Categories) != 0 || detail.ContentHash != strings.Repeat("a", 64) {
		test.Fatalf("uncategorized public skill lost: %+v %v", detail, err)
	}
}

func TestPublicCatalogDefaultAndMaximumPageSizes(test *testing.T) {
	store, _ := packFixture(test)
	var entries []repository.ArchivedSkill
	for index := range 105 {
		entries = append(entries, repository.ArchivedSkill{Skill: skill.Skill{Name: fmt.Sprintf("Paged %03d", index), Description: "Bounded", Path: fmt.Sprintf("skills/paged-%03d", index)}, ContentHash: strings.Repeat("a", 64)})
	}
	reference, _ := repository.ParseReference("org/paged", "")
	collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), entries)
	if err != nil {
		test.Fatal(err)
	}
	collected, err = store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	for index := range 67 {
		entry, err := store.CreateCategory(test.Context(), "alice", fmt.Sprintf("Paged category %03d", index))
		if err != nil {
			test.Fatal(err)
		}
		if _, err := store.SetSkillCategories(test.Context(), "alice", collected.Skills[index].ID, []string{entry.ID}); err != nil {
			test.Fatal(err)
		}
	}
	if _, err := store.SetPublication(test.Context(), "alice", collected.ID, 1, true, true); err != nil {
		test.Fatal(err)
	}
	page, err := store.ListPublicSkills(test.Context(), state.PublicSkillQuery{})
	if err != nil || len(page.Skills) != 24 || page.NextCursor == "" {
		test.Fatalf("default skills page: len=%d cursor=%q %v", len(page.Skills), page.NextCursor, err)
	}
	page, err = store.ListPublicSkills(test.Context(), state.PublicSkillQuery{Limit: 100})
	if err != nil || len(page.Skills) != 100 || page.NextCursor == "" {
		test.Fatalf("maximum skills page: len=%d %v", len(page.Skills), err)
	}
	next, err := store.ListPublicSkills(test.Context(), state.PublicSkillQuery{Limit: 100, Cursor: page.NextCursor})
	if err != nil || len(next.Skills) != 5 || next.NextCursor != "" {
		test.Fatalf("maximum continuation: len=%d %v", len(next.Skills), err)
	}
	categories, err := store.ListPublicCategories(test.Context(), 0, "")
	if err != nil || len(categories.Categories) != 64 || categories.NextCursor == "" {
		test.Fatalf("default categories page: len=%d %v", len(categories.Categories), err)
	}
	categories, err = store.ListPublicCategories(test.Context(), 100, categories.NextCursor)
	if err != nil || len(categories.Categories) != 3 || categories.NextCursor != "" {
		test.Fatalf("category continuation: len=%d %v", len(categories.Categories), err)
	}
}
