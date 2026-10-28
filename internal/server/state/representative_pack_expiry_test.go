package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/skill"
)

func TestRepresentativePackSkillAuthorizationRejectsExpiryDuringQuery(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	store.state.now = func() time.Time { return now }
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	reference, err := repository.ParseReference("org/representative-expiry", "")
	if err != nil {
		test.Fatal(err)
	}
	contentHash := strings.Repeat("a", 64)
	collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Example", Description: "Representative dummy skill", Path: "skills/example"}, ContentHash: contentHash}})
	if err != nil {
		test.Fatal(err)
	}
	detail, err := store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil || len(detail.Skills) != 1 {
		test.Fatalf("prepare collected skill: %v", err)
	}
	skillID := detail.Skills[0].ID
	created, err := store.CreatePack(test.Context(), "alice", PackDraft{Name: "Example", Items: []PackSelection{{SkillID: skillID}}})
	if err != nil {
		test.Fatal(err)
	}
	digest := sha256.Sum256([]byte("representative dummy share credential"))
	shareID := rand.Text()
	expiry := now.Add(time.Minute)
	if _, err := store.CreatePackShare(test.Context(), "alice", created.ID, shareID, digest[:], &expiry); err != nil {
		test.Fatal(err)
	}
	if err := store.AuthorizePackSkill(test.Context(), shareID, digest[:], skillID, contentHash, created.Revision); err != nil {
		test.Fatalf("unexpired fixture was not authorized: %v", err)
	}
	advanced := false
	store.state.client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if err == nil {
				now = expiry.Add(time.Second)
				advanced = true
			}
			return value, err
		})
	}))
	err = store.AuthorizePackSkill(test.Context(), shareID, digest[:], skillID, contentHash, created.Revision)
	if !advanced {
		test.Fatal("fixture did not advance the clock after the authorization query")
	}
	if !errors.Is(err, ErrPackShareUnavailable) {
		test.Fatalf("authorization completed after share expiry: got %v, want ErrPackShareUnavailable", err)
	}
}
