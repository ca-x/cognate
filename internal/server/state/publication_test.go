package state_test

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
)

func TestPublicationExplicitConfirmationRevisionAndIsolation(test *testing.T) {
	store, skills := packFixture(test)
	repositories, err := store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil {
		test.Fatal(err)
	}
	id := repositories[0].ID
	initial, err := store.Publication(test.Context(), "alice", id)
	if err != nil || initial.Published || initial.Revision != 1 || initial.RepositoryID != id {
		test.Fatalf("default publication: %+v %v", initial, err)
	}
	if _, err := store.SetPublication(test.Context(), "alice", id, 1, true, false); !errors.Is(err, state.ErrPublicationConfirmation) {
		test.Fatalf("missing confirmation: %v", err)
	}
	if _, err := store.SetPublication(test.Context(), "bob", id, 1, true, true); !errors.Is(err, state.ErrRepositoryNotFound) {
		test.Fatalf("foreign publication: %v", err)
	}
	if _, err := store.Publication(test.Context(), "bob", id); !errors.Is(err, state.ErrRepositoryNotFound) {
		test.Fatalf("foreign status: %v", err)
	}
	before, err := store.ListPublicSkills(test.Context(), state.PublicSkillQuery{})
	if err != nil || len(before.Skills) != 0 {
		test.Fatalf("private repository visible: %+v %v", before, err)
	}
	published, err := store.SetPublication(test.Context(), "alice", id, 1, true, true)
	if err != nil || !published.Published || published.Revision != 2 {
		test.Fatalf("publish: %+v %v", published, err)
	}
	unchanged, err := store.SetPublication(test.Context(), "alice", id, 2, true, true)
	if err != nil || unchanged.Revision != 2 {
		test.Fatalf("idempotent publication changed revision: %+v %v", unchanged, err)
	}
	if _, err := store.SetPublication(test.Context(), "alice", id, 1, false, false); !errors.Is(err, state.ErrPublicationConflict) {
		test.Fatalf("stale update: %v", err)
	}
	after, err := store.ListPublicSkills(test.Context(), state.PublicSkillQuery{})
	if err != nil || len(after.Skills) != 1 || after.Skills[0].PublicationRevision != 2 {
		test.Fatalf("published list: %+v %v", after, err)
	}
	entry := after.Skills[0]
	if _, err := store.AuthorizePublicSkill(test.Context(), entry.ID, entry.ContentHash, 1); !errors.Is(err, state.ErrPublicationConflict) {
		test.Fatalf("stale download: %v", err)
	}
	if _, err := store.SetPublication(test.Context(), "alice", id, 2, false, false); err != nil {
		test.Fatal(err)
	}
	for _, hidden := range []string{entry.ID, skills[3], "missing"} {
		if _, err := store.PublicSkill(test.Context(), hidden); !errors.Is(err, state.ErrPublicSkillNotFound) {
			test.Fatalf("private/missing distinguishable: %v", err)
		}
		if _, err := store.AuthorizePublicSkill(test.Context(), hidden, entry.ContentHash, 2); !errors.Is(err, state.ErrPublicSkillNotFound) {
			test.Fatalf("private/missing download: %v", err)
		}
	}
}

func TestPublicationReadsCommittedDatabaseAcrossStores(test *testing.T) {
	path := filepath.Join(privateDirectory(test), "shared.db")
	key := make([]byte, 32)
	first, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = first.Close() })
	if err := first.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	reference, _ := repository.ParseReference("org/shared", "")
	collected, err := first.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Shared", Description: "Committed state", Path: "skills/shared"}, ContentHash: strings.Repeat("a", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	collected, err = first.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	second, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = second.Close() })
	if _, err := first.SetPublication(test.Context(), "alice", collected.ID, 1, true, true); err != nil {
		test.Fatal(err)
	}
	visible, err := second.PublicSkill(test.Context(), collected.Skills[0].ID)
	if err != nil || visible.PublicationRevision != 2 {
		test.Fatalf("committed publish not observed by reopened store: %+v %v", visible, err)
	}
	if _, err := second.SetPublication(test.Context(), "alice", collected.ID, 2, false, false); err != nil {
		test.Fatal(err)
	}
	if _, err := first.PublicSkill(test.Context(), visible.ID); !errors.Is(err, state.ErrPublicSkillNotFound) {
		test.Fatalf("committed unpublish masked by cache: %v", err)
	}
	if _, err := first.AuthorizePublicSkill(test.Context(), visible.ID, visible.ContentHash, 2); !errors.Is(err, state.ErrPublicSkillNotFound) {
		test.Fatalf("committed revocation masked by cache: %v", err)
	}
	status, err := first.Publication(test.Context(), "alice", collected.ID)
	if err != nil || status.Published || status.Revision != 3 {
		test.Fatalf("committed owner status not observed: %+v %v", status, err)
	}
}

func TestPublicationConcurrentCAS(test *testing.T) {
	store, _ := packFixture(test)
	repositories, err := store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil {
		test.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			<-start
			_, err := store.SetPublication(test.Context(), "alice", repositories[0].ID, 1, true, true)
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
		} else if !errors.Is(err, state.ErrPublicationConflict) {
			test.Errorf("concurrent CAS did not report conflict: %v", err)
		}
	}
	if winners != 1 {
		test.Fatalf("CAS winners=%d", winners)
	}
}
