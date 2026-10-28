package state

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/github"
)

func TestCatalogReadFailsIfCredentialOrObservationExpiresDuringQuery(t *testing.T) {
	for _, kind := range []string{"credential", "observation"} {
		for _, query := range []string{"lists", "repositories"} {
			t.Run(kind+"/"+query, func(t *testing.T) {
				directory := t.TempDir()
				if err := os.Chmod(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				key := make([]byte, 32)
				if _, err := rand.Read(key); err != nil {
					t.Fatal(err)
				}
				store, err := OpenWithOptions(t.Context(), filepath.Join(directory, "server.db"), key, Options{ObservationMaxAge: time.Minute})
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				if err := store.CreateUser(t.Context(), "alice"); err != nil {
					t.Fatal(err)
				}
				base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				material := map[string]any{"version": 1, "access": rand.Text()}
				if kind == "credential" {
					material["expires_at"] = base.Add(5 * time.Second)
				}
				data, err := json.Marshal(material)
				if err != nil {
					t.Fatal(err)
				}
				token, err := github.DecodeStoredToken(data)
				clear(data)
				if err != nil {
					t.Fatal(err)
				}
				generation, err := store.PutCredential(t.Context(), "alice", "viewer", "", token)
				if err != nil {
					t.Fatal(err)
				}
				owner, err := findUser(t.Context(), store.state.client, "alice")
				if err != nil {
					t.Fatal(err)
				}
				list, err := store.state.client.GitHubList.Create().SetUserID(owner.ID).SetViewerID("viewer").SetListKey("list").SetCredentialGeneration(generation).SetName("list").SetSlug("list").SetPrivate(true).SetUpdatedAt(base).SetLastAddedAt(base).SetVerifiedAt(base).SetSubscribed(true).SetVisible(true).Save(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				repository, err := store.state.client.GitHubRepository.Create().SetUserID(owner.ID).SetViewerID("viewer").SetRepositoryKey("repo").SetNameWithOwner("org/private").SetPrivate(true).Save(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if err := store.state.client.GitHubMembership.Create().SetListID(list.ID).SetRepositoryID(repository.ID).SetVisible(true).Exec(t.Context()); err != nil {
					t.Fatal(err)
				}
				calls := 0
				store.state.now = func() time.Time {
					calls++
					if calls <= 2 {
						return base.Add(time.Second)
					}
					return base.Add(2 * time.Minute)
				}
				var readErr error
				if query == "lists" {
					_, readErr = store.GitHubLists(context.Background(), "alice")
				} else {
					_, readErr = store.SubscribedRepositories(context.Background(), "alice")
				}
				if kind == "credential" && !errors.Is(readErr, ErrCredentialUnavailable) || kind == "observation" && !errors.Is(readErr, ErrObservationExpired) {
					t.Fatalf("expired catalog read returned: %v", readErr)
				}
			})
		}
	}
}
