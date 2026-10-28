package state_test

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/state"
	"github.com/ca-x/skillhub/internal/skill"
)

func packFixture(test *testing.T) (*state.Store, []string) {
	test.Helper()
	store, err := state.Open(test.Context(), filepath.Join(privateDirectory(test), "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	for _, user := range []string{"alice", "bob"} {
		if err := store.CreateUser(test.Context(), user); err != nil {
			test.Fatal(err)
		}
	}
	var ids []string
	for index, name := range []string{"org/first", "org/second", "org/conflict", "org/foreign"} {
		owner, hash := "alice", strings.Repeat("a", 64)
		if index == 2 {
			hash = strings.Repeat("b", 64)
		}
		if index == 3 {
			owner = "bob"
		}
		reference, _ := repository.ParseReference(name, "")
		entry, err := store.CollectRepository(test.Context(), owner, reference, strings.Repeat("c", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Shared Skill", Description: "A useful skill", Path: "skills/shared"}, ContentHash: hash}})
		if err != nil {
			test.Fatal(err)
		}
		detail, err := store.CollectedRepository(test.Context(), owner, entry.ID)
		if err != nil {
			test.Fatal(err)
		}
		ids = append(ids, detail.Skills[0].ID)
	}
	return store, ids
}

func TestPackCRUDOwnershipAndRevision(test *testing.T) {
	store, ids := packFixture(test)
	draft := state.PackDraft{Name: "My tools", Description: "Across repositories", Items: []state.PackSelection{{SkillID: ids[0]}, {SkillID: ids[1]}}}
	created, err := store.CreatePack(test.Context(), "alice", draft)
	if err != nil || created.ID == "" || created.Revision != 1 || len(created.Items) != 2 {
		test.Fatalf("create: %+v, %v", created, err)
	}
	if created.Items[0].Slug != "shared-skill" {
		test.Fatalf("unexpected portable slug: %s", created.Items[0].Slug)
	}
	other, err := store.ListPacks(test.Context(), "bob")
	if err != nil || len(other) != 0 {
		test.Fatalf("foreign list: %v", err)
	}
	if _, err := store.Pack(test.Context(), "bob", created.ID); !errors.Is(err, state.ErrPackNotFound) {
		test.Fatalf("foreign detail: %v", err)
	}
	draft.Items = []state.PackSelection{{SkillID: ids[0], Slug: "chosen"}}
	draft.Name = "Revised"
	updated, err := store.UpdatePack(test.Context(), "alice", created.ID, 1, draft)
	if err != nil || updated.Revision != 2 || len(updated.Items) != 1 || updated.Items[0].Slug != "chosen" {
		test.Fatalf("update: %+v, %v", updated, err)
	}
	if _, err := store.UpdatePack(test.Context(), "alice", created.ID, 1, draft); !errors.Is(err, state.ErrPackConflict) {
		test.Fatalf("stale update: %v", err)
	}
	if err := store.DeletePack(test.Context(), "bob", created.ID, 2); !errors.Is(err, state.ErrPackNotFound) {
		test.Fatalf("foreign delete: %v", err)
	}
	if err := store.DeletePack(test.Context(), "alice", created.ID, 1); !errors.Is(err, state.ErrPackConflict) {
		test.Fatalf("stale delete: %v", err)
	}
	if err := store.DeletePack(test.Context(), "alice", created.ID, 2); err != nil {
		test.Fatal(err)
	}
	if _, err := store.Pack(test.Context(), "alice", created.ID); !errors.Is(err, state.ErrPackNotFound) {
		test.Fatalf("deleted pack: %v", err)
	}
}

func TestPackSelectionsRejectForeignSkillsAndSlugCollisions(test *testing.T) {
	store, ids := packFixture(test)
	for _, selection := range [][]state.PackSelection{
		{{SkillID: ids[3]}},
		{{SkillID: ids[0]}, {SkillID: ids[0]}},
		{{SkillID: ids[0], Slug: "../outside"}},
	} {
		if _, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Invalid", Items: selection}); err == nil {
			test.Fatal("invalid selection accepted")
		}
	}
	draft := state.PackDraft{Name: "Conflict", Items: []state.PackSelection{{SkillID: ids[0]}, {SkillID: ids[2]}}}
	if _, err := store.CreatePack(test.Context(), "alice", draft); !errors.Is(err, state.ErrPackSlugConflict) {
		test.Fatalf("slug conflict: %v", err)
	}
	listed, err := store.ListPacks(test.Context(), "alice")
	if err != nil || len(listed) != 0 {
		test.Fatalf("failed creation left records: %v", err)
	}
	draft.Items[1].Slug = "another-skill"
	if _, err := store.CreatePack(test.Context(), "alice", draft); err != nil {
		test.Fatal(err)
	}
}

func TestPackShareStateRevocationExpiryAndDeletion(test *testing.T) {
	store, ids := packFixture(test)
	created, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Shared", Items: []state.PackSelection{{SkillID: ids[0]}}})
	if err != nil {
		test.Fatal(err)
	}
	digest := sha256.Sum256([]byte(rand.Text()))
	expiry := time.Now().Add(time.Hour).Truncate(time.Second)
	shareID := rand.Text()
	if _, err := store.CreatePackShare(test.Context(), "bob", created.ID, shareID, digest[:], &expiry); !errors.Is(err, state.ErrPackNotFound) {
		test.Fatalf("foreign share: %v", err)
	}
	if _, err := store.CreatePackShare(test.Context(), "alice", created.ID, shareID, digest[:], &expiry); err != nil {
		test.Fatal(err)
	}
	shared, err := store.SharedPack(test.Context(), shareID, digest[:])
	if err != nil || shared.ID != created.ID || len(shared.Items) != 1 {
		test.Fatalf("shared pack: %v", err)
	}
	wrong := sha256.Sum256([]byte(rand.Text()))
	if _, err := store.SharedPack(test.Context(), shareID, wrong[:]); !errors.Is(err, state.ErrPackShareUnavailable) {
		test.Fatalf("wrong digest: %v", err)
	}
	if err := store.RevokePackShare(test.Context(), "bob", created.ID, shareID); !errors.Is(err, state.ErrPackNotFound) {
		test.Fatalf("foreign revoke: %v", err)
	}
	if err := store.RevokePackShare(test.Context(), "alice", created.ID, shareID); err != nil {
		test.Fatal(err)
	}
	if _, err := store.SharedPack(test.Context(), shareID, digest[:]); !errors.Is(err, state.ErrPackShareUnavailable) {
		test.Fatalf("revoked share: %v", err)
	}
	expired := time.Now().Add(-time.Second)
	if _, err := store.CreatePackShare(test.Context(), "alice", created.ID, rand.Text(), digest[:], &expired); err == nil {
		test.Fatal("expired share created")
	}
	secondID := rand.Text()
	secondDigest := sha256.Sum256([]byte(rand.Text()))
	if _, err := store.CreatePackShare(test.Context(), "alice", created.ID, secondID, secondDigest[:], nil); err != nil {
		test.Fatal(err)
	}
	if err := store.DeletePack(test.Context(), "alice", created.ID, 1); err != nil {
		test.Fatal(err)
	}
	if _, err := store.SharedPack(test.Context(), shareID, digest[:]); !errors.Is(err, state.ErrPackShareUnavailable) {
		test.Fatalf("deleted pack share: %v", err)
	}
	if _, err := store.SharedPack(test.Context(), secondID, secondDigest[:]); !errors.Is(err, state.ErrPackShareUnavailable) {
		test.Fatalf("deleted pack active share: %v", err)
	}
}

func TestPackFailedUpdateRollsBackRevisionAndMembers(test *testing.T) {
	store, ids := packFixture(test)
	draft := state.PackDraft{Name: "Original", Items: []state.PackSelection{{SkillID: ids[0]}}}
	created, err := store.CreatePack(test.Context(), "alice", draft)
	if err != nil {
		test.Fatal(err)
	}
	draft.Name = "Must roll back"
	draft.Items = []state.PackSelection{{SkillID: ids[3]}}
	if _, err := store.UpdatePack(test.Context(), "alice", created.ID, 1, draft); !errors.Is(err, state.ErrPackSkillUnavailable) {
		test.Fatalf("foreign selection: %v", err)
	}
	current, err := store.Pack(test.Context(), "alice", created.ID)
	if err != nil || current.Revision != 1 || current.Name != "Original" || len(current.Items) != 1 || current.Items[0].SkillID != ids[0] {
		test.Fatalf("failed update changed pack: %+v, %v", current, err)
	}
}

func TestPackConcurrentUpdatesUseRevisionCAS(test *testing.T) {
	store, ids := packFixture(test)
	draft := state.PackDraft{Name: "Original", Items: []state.PackSelection{{SkillID: ids[0]}}}
	created, err := store.CreatePack(test.Context(), "alice", draft)
	if err != nil {
		test.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			_, err := store.UpdatePack(test.Context(), "alice", created.ID, 1, draft)
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		if result == nil {
			successes++
		} else if errors.Is(result, state.ErrPackConflict) {
			conflicts++
		} else {
			test.Fatal(result)
		}
	}
	if successes != 1 || conflicts != 1 {
		test.Fatalf("concurrent edits: successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestPackSkillAuthorizationTracksCurrentRevision(test *testing.T) {
	store, ids := packFixture(test)
	draft := state.PackDraft{Name: "Original", Items: []state.PackSelection{{SkillID: ids[0]}}}
	created, err := store.CreatePack(test.Context(), "alice", draft)
	if err != nil {
		test.Fatal(err)
	}
	shareID := rand.Text()
	digest := sha256.Sum256([]byte(rand.Text()))
	if _, err := store.CreatePackShare(test.Context(), "alice", created.ID, shareID, digest[:], nil); err != nil {
		test.Fatal(err)
	}
	if err := store.AuthorizePackSkill(test.Context(), shareID, digest[:], ids[0], strings.Repeat("a", 64), 1); err != nil {
		test.Fatal(err)
	}
	draft.Items[0].SkillID = ids[1]
	if _, err := store.UpdatePack(test.Context(), "alice", created.ID, 1, draft); err != nil {
		test.Fatal(err)
	}
	for _, check := range []struct {
		id       string
		revision int64
	}{{ids[0], 1}, {ids[1], 1}, {ids[0], 2}} {
		if err := store.AuthorizePackSkill(test.Context(), shareID, digest[:], check.id, strings.Repeat("a", 64), check.revision); !errors.Is(err, state.ErrPackShareUnavailable) {
			test.Fatalf("old member/revision authorized: %v", err)
		}
	}
	if err := store.AuthorizePackSkill(test.Context(), shareID, digest[:], ids[1], strings.Repeat("a", 64), 2); err != nil {
		test.Fatal(err)
	}
}
