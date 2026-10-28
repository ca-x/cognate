package state_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/github"
	"github.com/ca-x/skillhub/internal/server/state"
)

type fixtureList struct {
	id    string
	repos []map[string]any
}

type catalogFixture struct {
	viewer    string
	lists     []fixtureList
	itemCalls int
	updatedAt string
	itemError bool
}

func (fixture *catalogFixture) client(t *testing.T, token github.Token) *github.ListsClient {
	t.Helper()
	client, err := github.NewListsClient(token, &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		var query struct {
			Variables struct {
				ID string `json:"id"`
			} `json:"variables"`
		}
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			t.Fatal(err)
		}
		metadata := func(id string) map[string]any {
			updatedAt := fixture.updatedAt
			if updatedAt == "" {
				updatedAt = "2026-01-01T00:00:00Z"
			}
			return map[string]any{"id": id, "name": id, "slug": id, "isPrivate": false, "updatedAt": updatedAt, "lastAddedAt": updatedAt}
		}
		page := map[string]any{"hasNextPage": false, "endCursor": nil}
		var response any
		if query.Variables.ID == "" {
			nodes := make([]any, 0, len(fixture.lists))
			for _, list := range fixture.lists {
				nodes = append(nodes, metadata(list.id))
			}
			response = map[string]any{"data": map[string]any{"viewer": map[string]any{"id": fixture.viewer, "lists": map[string]any{"pageInfo": page, "nodes": nodes}}}}
		} else {
			fixture.itemCalls++
			if fixture.itemError {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			for _, list := range fixture.lists {
				if list.id != query.Variables.ID {
					continue
				}
				node := metadata(list.id)
				node["__typename"] = "UserList"
				node["user"] = map[string]any{"id": fixture.viewer}
				node["items"] = map[string]any{"totalCount": len(list.repos), "pageInfo": page, "nodes": list.repos}
				response = map[string]any{"data": map[string]any{"node": node}}
				break
			}
		}
		if response == nil {
			t.Fatal("unexpected list query")
		}
		body, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func fixtureRepo(id, name string, private bool) map[string]any {
	return map[string]any{"__typename": "Repository", "id": id, "nameWithOwner": name, "databaseId": nil, "isPrivate": private}
}

func applyFixture(t *testing.T, store *state.Store, user string, fixture *catalogFixture) {
	t.Helper()
	credential, err := store.Credential(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.BeginListSync(t.Context(), user, credential.Generation)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ReadListSync(t.Context(), ticket, fixture.client(t, credential.Token))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyListSync(t.Context(), result); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribedListsDeduplicateAndHideUnobservedRepositories(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(privateDirectory(t), "server.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, user := range []string{"alice", "bob"} {
		if err := store.CreateUser(t.Context(), user); err != nil {
			t.Fatal(err)
		}
		token, err := github.NewToken(rand.Text(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutCredential(t.Context(), user, "viewer", "", token); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &catalogFixture{viewer: "viewer", lists: []fixtureList{
		{id: "list-a", repos: []map[string]any{fixtureRepo("repo-1", "org/one", false), fixtureRepo("repo-2", "org/private", true)}},
		{id: "list-b", repos: []map[string]any{fixtureRepo("repo-1", "org/one", false), fixtureRepo("repo-3", "org/three", false)}},
	}}
	applyFixture(t, store, "alice", fixture)
	lists, err := store.GitHubLists(t.Context(), "alice")
	if err != nil || len(lists) != 2 || lists[0].Subscribed || lists[1].Subscribed {
		t.Fatalf("discovered lists = %+v, error = %v", lists, err)
	}
	for _, id := range []string{"list-a", "list-b"} {
		if err := store.SetListSubscription(t.Context(), "alice", id, true); err != nil {
			t.Fatal(err)
		}
	}
	applyFixture(t, store, "alice", fixture)
	repos, err := store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repos) != 3 {
		t.Fatalf("deduplicated repos = %+v, error = %v", repos, err)
	}
	other, err := store.SubscribedRepositories(t.Context(), "bob")
	if err != nil || len(other) != 0 {
		t.Fatalf("other user saw repos = %+v, error = %v", other, err)
	}
	credential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", credential.Generation, replacement); err != nil {
		t.Fatal(err)
	}
	repos, err = store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repos) != 0 {
		t.Fatalf("reauthorization exposed old repositories: %+v, %v", repos, err)
	}
	lists, err = store.GitHubLists(t.Context(), "alice")
	if err != nil || len(lists) != 0 {
		t.Fatalf("reauthorization exposed old lists: %+v, %v", lists, err)
	}
	applyFixture(t, store, "alice", fixture)
	lists, err = store.GitHubLists(t.Context(), "alice")
	if err != nil || len(lists) != 2 || !lists[0].Subscribed || !lists[1].Subscribed {
		t.Fatalf("revalidated subscriptions were lost: %+v, %v", lists, err)
	}
	repos, err = store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repos) != 3 {
		t.Fatalf("revalidated repositories = %+v, %v", repos, err)
	}
	fixture.lists = fixture.lists[:1]
	fixture.lists[0].repos = []map[string]any{fixtureRepo("repo-2", "org/private", true)}
	applyFixture(t, store, "alice", fixture)
	repos, err = store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repos) != 1 || repos[0].ID != "repo-2" || !repos[0].Private {
		t.Fatalf("unobserved repos remain visible: %+v, %v", repos, err)
	}
	lists, err = store.GitHubLists(t.Context(), "alice")
	if err != nil || len(lists) != 1 || lists[0].ID != "list-a" {
		t.Fatalf("missing list was exposed: %+v, %v", lists, err)
	}
	fixture.lists = append(fixture.lists, fixtureList{id: "list-b", repos: []map[string]any{fixtureRepo("repo-3", "org/three", false)}})
	applyFixture(t, store, "alice", fixture)
	lists, err = store.GitHubLists(t.Context(), "alice")
	if err != nil || len(lists) != 2 || !lists[1].Subscribed {
		t.Fatalf("reappearing list lost its subscription: %+v, %v", lists, err)
	}
}

func TestFailedSyncDoesNotExposePreviouslyObservedRepositories(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(privateDirectory(t), "server.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", "", token); err != nil {
		t.Fatal(err)
	}
	fixture := &catalogFixture{viewer: "viewer", lists: []fixtureList{{id: "list-a", repos: []map[string]any{fixtureRepo("private", "org/private", true)}}}}
	applyFixture(t, store, "alice", fixture)
	if err := store.SetListSubscription(t.Context(), "alice", "list-a", true); err != nil {
		t.Fatal(err)
	}
	applyFixture(t, store, "alice", fixture)
	credential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.BeginListSync(t.Context(), "alice", credential.Generation)
	if err != nil {
		t.Fatal(err)
	}
	failing, err := github.NewListsClient(credential.Token, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadListSync(t.Context(), ticket, failing); err == nil {
		t.Fatal("accepted a failed authorization check")
	}
	repos, err := store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repos) != 0 {
		t.Fatalf("failed sync exposed stale repos: %+v, %v", repos, err)
	}
	applyFixture(t, store, "alice", fixture)
	repos, err = store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repos) != 1 {
		t.Fatalf("fresh sync did not restore visibility: %+v, %v", repos, err)
	}
}

func TestSubscriptionChangeInvalidatesInFlightSnapshot(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(privateDirectory(t), "server.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", "", token); err != nil {
		t.Fatal(err)
	}
	fixture := &catalogFixture{viewer: "viewer", lists: []fixtureList{{id: "list-a", repos: []map[string]any{}}}}
	applyFixture(t, store, "alice", fixture)
	credential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.BeginListSync(t.Context(), "alice", credential.Generation)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ReadListSync(t.Context(), ticket, fixture.client(t, credential.Token))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetListSubscription(t.Context(), "alice", "list-a", true); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyListSync(t.Context(), result); !errors.Is(err, state.ErrSyncSuperseded) {
		t.Fatalf("stale selection snapshot applied: %v", err)
	}
	if _, err := store.SubscribedRepositories(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseRejectsCrossUserAndCrossViewerMembership(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDirectory(t), "server.db")
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, user := range []string{"alice", "bob"} {
		if err := store.CreateUser(t.Context(), user); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.ExecContext(t.Context(), `INSERT INTO git_hub_lists (user_id,viewer_id,list_key,name,slug,private,updated_at,last_added_at) VALUES ((SELECT id FROM users WHERE user_key='alice'),'viewer-a','list-a','list-a','list-a',0,'2026-01-01','2026-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, repository := range []struct{ user, viewer, id string }{{"alice", "viewer-a", "valid"}, {"bob", "viewer-a", "other-user"}, {"alice", "viewer-b", "other-viewer"}} {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO git_hub_repositories (user_id,viewer_id,repository_key,name_with_owner,private) VALUES ((SELECT id FROM users WHERE user_key=?),?,?,?,0)`, repository.user, repository.viewer, repository.id, "org/"+repository.id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"other-user", "other-viewer"} {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO git_hub_memberships (list_id,repository_id) VALUES ((SELECT id FROM git_hub_lists WHERE list_key='list-a'),(SELECT id FROM git_hub_repositories WHERE repository_key=?))`, id); err == nil {
			t.Fatalf("accepted cross-boundary membership %s", id)
		}
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO git_hub_memberships (list_id,repository_id) VALUES ((SELECT id FROM git_hub_lists WHERE list_key='list-a'),(SELECT id FROM git_hub_repositories WHERE repository_key='valid'))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE git_hub_lists SET viewer_id='viewer-b' WHERE list_key='list-a'`); err == nil {
		t.Fatal("allowed list ownership to change")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE git_hub_repositories SET user_id=(SELECT id FROM users WHERE user_key='bob') WHERE repository_key='valid'`); err == nil {
		t.Fatal("allowed repository ownership to change")
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER skillhub_membership_owner_insert`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO git_hub_memberships (list_id,repository_id) VALUES ((SELECT id FROM git_hub_lists WHERE list_key='list-a'),(SELECT id FROM git_hub_repositories WHERE repository_key='other-user'))`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := state.Open(t.Context(), path, key); err == nil {
		_ = reopened.Close()
		t.Fatal("opened database containing an invalid legacy membership")
	}
}

func TestExpiredObservationDoesNotExposePrivateData(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDirectory(t), "server.db")
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", "", token); err != nil {
		t.Fatal(err)
	}
	fixture := &catalogFixture{viewer: "viewer", lists: []fixtureList{{id: "list-a", repos: []map[string]any{fixtureRepo("private", "org/private", true)}}}}
	applyFixture(t, store, "alice", fixture)
	if err := store.SetListSubscription(t.Context(), "alice", "list-a", true); err != nil {
		t.Fatal(err)
	}
	applyFixture(t, store, "alice", fixture)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE git_hub_lists SET verified_at=?`, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	lists, err := store.GitHubLists(t.Context(), "alice")
	if err != nil || len(lists) != 0 {
		t.Fatalf("stale list metadata visible: %+v, %v", lists, err)
	}
	repos, err := store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repos) != 0 {
		t.Fatalf("stale private repository visible: %+v, %v", repos, err)
	}
	applyFixture(t, store, "alice", fixture)
	repos, err = store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repos) != 1 {
		t.Fatalf("fresh verification did not restore repository: %+v, %v", repos, err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE git_hub_lists SET verified_at=?`, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if lists, err := store.GitHubLists(t.Context(), "alice"); err != nil || len(lists) != 0 {
		t.Fatalf("future observation was accepted: %+v, %v", lists, err)
	}
}

func TestObservationMaximumAgeIsConfigurableAndBounded(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDirectory(t), "server.db")
	for _, invalid := range []time.Duration{-time.Second, 25 * time.Hour} {
		if store, err := state.OpenWithOptions(t.Context(), path, key, state.Options{ObservationMaxAge: invalid}); err == nil {
			_ = store.Close()
			t.Fatal("accepted invalid observation duration")
		}
	}
	store, err := state.OpenWithOptions(t.Context(), path, key, state.Options{ObservationMaxAge: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if second, err := state.OpenWithOptions(t.Context(), path, key, state.Options{ObservationMaxAge: time.Hour}); err == nil {
		_ = second.Close()
		t.Fatal("accepted inconsistent same-database observation policy")
	}
	second, err := state.OpenWithOptions(t.Context(), path, key, state.Options{ObservationMaxAge: 30 * time.Minute})
	if err != nil {
		t.Fatalf("same observation policy was rejected: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", "", token); err != nil {
		t.Fatal(err)
	}
	fixture := &catalogFixture{viewer: "viewer", lists: []fixtureList{{id: "list-a", repos: []map[string]any{}}}}
	applyFixture(t, store, "alice", fixture)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE git_hub_lists SET verified_at=?`, time.Now().Add(-45*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if lists, err := store.GitHubLists(t.Context(), "alice"); err != nil || len(lists) != 0 {
		t.Fatalf("custom maximum age ignored: %+v, %v", lists, err)
	}
}

func TestViewerSwitchDoesNotExposeOtherViewerHistory(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(privateDirectory(t), "server.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.PutCredential(t.Context(), "alice", "viewer-a", "", token)
	if err != nil {
		t.Fatal(err)
	}
	first := &catalogFixture{viewer: "viewer-a", lists: []fixtureList{{id: "same-id", repos: []map[string]any{fixtureRepo("same-repo", "a/private", true)}}}}
	applyFixture(t, store, "alice", first)
	if err := store.SetListSubscription(t.Context(), "alice", "same-id", true); err != nil {
		t.Fatal(err)
	}
	applyFixture(t, store, "alice", first)
	otherToken, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	generation, err = store.PutCredential(t.Context(), "alice", "viewer-b", generation, otherToken)
	if err != nil {
		t.Fatal(err)
	}
	lists, err := store.GitHubLists(t.Context(), "alice")
	if err != nil || len(lists) != 0 {
		t.Fatalf("viewer B saw viewer A lists: %+v, %v", lists, err)
	}
	second := &catalogFixture{viewer: "viewer-b", lists: []fixtureList{{id: "same-id", repos: []map[string]any{fixtureRepo("same-repo", "b/private", true)}}}}
	applyFixture(t, store, "alice", second)
	lists, err = store.GitHubLists(t.Context(), "alice")
	if err != nil || len(lists) != 1 || lists[0].Subscribed {
		t.Fatalf("viewer B inherited viewer A subscription: %+v, %v", lists, err)
	}
	backToken, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer-a", generation, backToken); err != nil {
		t.Fatal(err)
	}
	if repos, err := store.SubscribedRepositories(t.Context(), "alice"); err != nil || len(repos) != 0 {
		t.Fatalf("viewer A history visible before revalidation: %+v, %v", repos, err)
	}
	applyFixture(t, store, "alice", first)
	if repos, err := store.SubscribedRepositories(t.Context(), "alice"); err != nil || len(repos) != 1 || repos[0].NameWithOwner != "a/private" {
		t.Fatalf("viewer A data not restored after validation: %+v, %v", repos, err)
	}
}

func TestCatalogApplyFailureRollsBackAndCanRetry(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateDirectory(t), "server.db")
	store, err := state.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", "", token); err != nil {
		t.Fatal(err)
	}
	fixture := &catalogFixture{viewer: "viewer", lists: []fixtureList{{id: "list-a", repos: []map[string]any{fixtureRepo("repo-1", "org/one", false), fixtureRepo("repo-2", "org/two", true)}}}}
	applyFixture(t, store, "alice", fixture)
	if err := store.SetListSubscription(t.Context(), "alice", "list-a", true); err != nil {
		t.Fatal(err)
	}
	credential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.BeginListSync(t.Context(), "alice", credential.Generation)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ReadListSync(t.Context(), ticket, fixture.client(t, credential.Token))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_catalog BEFORE INSERT ON git_hub_memberships BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyListSync(t.Context(), result); err == nil {
		t.Fatal("ignored injected catalog failure")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM git_hub_repositories`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial repositories persisted: count=%d error=%v", count, err)
	}
	if lists, err := store.GitHubLists(t.Context(), "alice"); err != nil || len(lists) != 0 {
		t.Fatalf("failed sync exposed metadata: %+v, %v", lists, err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_catalog`); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyListSync(t.Context(), result); err != nil {
		t.Fatalf("rolled-back snapshot could not retry: %v", err)
	}
	if repos, err := store.SubscribedRepositories(t.Context(), "alice"); err != nil || len(repos) != 2 {
		t.Fatalf("retried sync incomplete: %+v, %v", repos, err)
	}
}
