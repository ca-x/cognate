package state_test

import (
	"crypto/rand"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/state"
)

func selectedSync(t *testing.T, store *state.Store, fixture *catalogFixture) state.ListSyncResult {
	t.Helper()
	result, err := readSelectedSync(t, store, fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyListSync(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	return result
}

func readSelectedSync(t *testing.T, store *state.Store, fixture *catalogFixture) (state.ListSyncResult, error) {
	t.Helper()
	credential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.BeginListSync(t.Context(), "alice", credential.Generation)
	if err != nil {
		t.Fatal(err)
	}
	return store.ReadSelectedListSync(t.Context(), ticket, fixture.client(t, credential.Token))
}

func TestSelectedSyncWatermarkPersistsAndInvalidates(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(privateDirectory(t), "server.db")
	store, err := state.Open(t.Context(), database, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.PutCredential(t.Context(), "alice", "viewer", "", token)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &catalogFixture{viewer: "viewer", lists: []fixtureList{{id: "selected", repos: []map[string]any{fixtureRepo("repo", "org/skill", false)}}, {id: "other", repos: []map[string]any{}}}}
	selectedSync(t, store, fixture)
	if fixture.itemCalls != 0 {
		t.Fatalf("discovery fetched %d unselected lists", fixture.itemCalls)
	}
	if err := store.SetListSubscription(t.Context(), "alice", "selected", true); err != nil {
		t.Fatal(err)
	}
	selectedSync(t, store, fixture)
	if fixture.itemCalls != 1 {
		t.Fatalf("first selected sync fetched %d lists", fixture.itemCalls)
	}
	lists, err := store.GitHubLists(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	var firstObservation time.Time
	for _, list := range lists {
		if list.ID == "selected" {
			firstObservation = list.VerifiedAt
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), database, key)
	if err != nil {
		t.Fatal(err)
	}
	selectedSync(t, store, fixture)
	if fixture.itemCalls != 1 {
		t.Fatalf("restart lost watermark: %d item reads", fixture.itemCalls)
	}
	lists, err = store.GitHubLists(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, list := range lists {
		if list.ID == "selected" && !list.VerifiedAt.Equal(firstObservation) {
			t.Fatal("unchanged metadata extended the member permission observation")
		}
	}
	repositories, err := store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repositories) != 1 {
		t.Fatalf("unchanged members not retained: %d, %v", len(repositories), err)
	}
	fixture.updatedAt = "2026-02-01T00:00:00Z"
	selectedSync(t, store, fixture)
	if fixture.itemCalls != 2 {
		t.Fatalf("changed watermark not fetched: %d", fixture.itemCalls)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", generation, token); err != nil {
		t.Fatal(err)
	}
	selectedSync(t, store, fixture)
	if fixture.itemCalls != 3 {
		t.Fatalf("credential change reused watermark: %d", fixture.itemCalls)
	}
	for _, selected := range []bool{false, true} {
		if err := store.SetListSubscription(t.Context(), "alice", "selected", selected); err != nil {
			t.Fatal(err)
		}
	}
	selectedSync(t, store, fixture)
	if fixture.itemCalls != 4 {
		t.Fatalf("reselection reused invalidated watermark: %d", fixture.itemCalls)
	}
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE git_hub_lists SET verified_at=?, items_verified_at=? WHERE list_key='selected'`, time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	selectedSync(t, store, fixture)
	if fixture.itemCalls != 5 {
		t.Fatalf("expired observation reused unchanged watermark: %d", fixture.itemCalls)
	}
	previousLists := fixture.lists
	fixture.lists = []fixtureList{}
	selectedSync(t, store, fixture)
	fixture.lists = previousLists
	selectedSync(t, store, fixture)
	if fixture.itemCalls != 6 {
		t.Fatalf("reappearing list reused an invalidated watermark: %d", fixture.itemCalls)
	}
	fixture.updatedAt, fixture.itemError = "2026-03-01T00:00:00Z", true
	if _, err := readSelectedSync(t, store, fixture); err == nil {
		t.Fatal("failed item read reported success")
	}
	repositories, err = store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repositories) != 0 {
		t.Fatalf("failed list read left members visible: %d, %v", len(repositories), err)
	}
	fixture.itemError = false
	selectedSync(t, store, fixture)
	if fixture.itemCalls != 8 {
		t.Fatalf("failed item read advanced the durable watermark: %d", fixture.itemCalls)
	}
}
