package state_test

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
)

func TestLibraryCollectionIsAtomicIdempotentAndUserScoped(test *testing.T) {
	key := []byte(rand.Text() + "123456")[:32]
	database := filepath.Join(privateDirectory(test), "server.db")
	store, err := state.Open(test.Context(), database, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	for _, user := range []string{"alice", "bob"} {
		if err := store.CreateUser(test.Context(), user); err != nil {
			test.Fatal(err)
		}
	}
	reference, _ := repository.ParseReference("org/repo", "HEAD")
	skills := []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Example", Description: "A skill", Path: "skills/example"}, ContentHash: strings.Repeat("a", 64)}}
	collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), skills)
	if err != nil || collected.ID == "" || collected.SkillCount != 1 || !collected.Manual {
		test.Fatalf("collect: %+v, %v", collected, err)
	}
	duplicate, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("c", 64), skills)
	if err != nil || duplicate.ID != collected.ID || duplicate.ArchiveHash != collected.ArchiveHash {
		test.Fatalf("duplicate replaced content: %+v, %v", duplicate, err)
	}
	listed, err := store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil || len(listed) != 1 {
		test.Fatalf("list: %+v, %v", listed, err)
	}
	other, err := store.ListCollectedRepositories(test.Context(), "bob")
	if err != nil || len(other) != 0 {
		test.Fatalf("cross-user list: %+v, %v", other, err)
	}
	if _, err := store.CollectedRepository(test.Context(), "bob", collected.ID); !errors.Is(err, state.ErrRepositoryNotFound) {
		test.Fatalf("cross-user detail: %v", err)
	}
	detail, err := store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil || len(detail.Skills) != 1 || detail.Skills[0].ContentHash != skills[0].ContentHash || detail.Skills[0].ID == "" {
		test.Fatalf("detail: %+v, %v", detail, err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	store, err = state.Open(test.Context(), database, key)
	if err != nil {
		test.Fatal(err)
	}
	detail, err = store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil || len(detail.Skills) != 1 {
		test.Fatalf("restart lost collection: %+v, %v", detail, err)
	}
}

func TestLibraryInvalidCollectionDoesNotInsertRepository(test *testing.T) {
	store, err := state.Open(test.Context(), filepath.Join(privateDirectory(test), "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	reference, _ := repository.ParseReference("org/repo", "")
	for _, skills := range [][]repository.ArchivedSkill{nil, {{Skill: skill.Skill{Name: "bad", Description: "bad", Path: "../escape"}, ContentHash: strings.Repeat("a", 64)}}} {
		if _, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), skills); err == nil {
			test.Fatal("invalid collection accepted")
		}
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := store.CollectRepository(ctx, "alice", reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "valid", Description: "valid", Path: "."}, ContentHash: strings.Repeat("a", 64)}}); !errors.Is(err, context.Canceled) {
		test.Fatalf("canceled collection: %v", err)
	}
	listed, err := store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil || len(listed) != 0 {
		test.Fatalf("failed collection inserted state: %+v, %v", listed, err)
	}
}

func TestLibraryConcurrentCollectionRemainsIdempotent(test *testing.T) {
	store, err := state.Open(test.Context(), filepath.Join(privateDirectory(test), "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	reference, _ := repository.ParseReference("org/repo", "")
	skills := []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Example", Description: "A skill", Path: "."}, ContentHash: strings.Repeat("a", 64)}}
	results := make(chan state.CollectedRepository, 8)
	failures := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			entry, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), skills)
			results <- entry
			failures <- err
		})
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			test.Fatal(err)
		}
	}
	id := ""
	for entry := range results {
		if id != "" && entry.ID != id {
			test.Fatal("concurrent collection created duplicate identities")
		}
		id = entry.ID
	}
	detail, err := store.CollectedRepository(test.Context(), "alice", id)
	if err != nil || len(detail.Skills) != 1 {
		test.Fatalf("concurrent collection duplicated skills: %v", err)
	}
}
