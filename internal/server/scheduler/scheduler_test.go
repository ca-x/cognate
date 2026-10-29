package scheduler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (transport transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type fetchFunc func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error)

func (fetch fetchFunc) Fetch(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
	return fetch(ctx, reference)
}

type fixture struct {
	store         *state.Store
	cache         *library.ArchiveCache
	client        *http.Client
	mutex         sync.Mutex
	itemCalls     int
	metaCalls     int
	updatedAt     string
	onItems       func(context.Context) error
	privateSource bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cache, err := library.OpenArchiveCache(filepath.Join(directory, "archives"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	fixture := &fixture{store: store, cache: cache, updatedAt: "2026-01-01T00:00:00Z"}
	fixture.client = &http.Client{Transport: transportFunc(fixture.respond)}
	fixture.addUser(t, "alice")
	return fixture
}

func (fixture *fixture) addUser(t *testing.T, user string) {
	t.Helper()
	if err := fixture.store.CreateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	token, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := fixture.store.PutCredential(t.Context(), user, "viewer", "", token)
	if err != nil {
		t.Fatal(err)
	}
	client, err := github.NewListsClient(token, fixture.client)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := fixture.store.BeginListSync(t.Context(), user, generation)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.store.ReadSelectedListSync(t.Context(), ticket, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ApplyListSync(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SetListSubscription(t.Context(), user, "selected", true); err != nil {
		t.Fatal(err)
	}
}

func (fixture *fixture) respond(request *http.Request) (*http.Response, error) {
	var query struct {
		Variables struct {
			ID string `json:"id"`
		} `json:"variables"`
	}
	if err := json.UnmarshalRead(request.Body, &query); err != nil {
		return nil, err
	}
	fixture.mutex.Lock()
	updatedAt, onItems := fixture.updatedAt, fixture.onItems
	if query.Variables.ID == "" {
		fixture.metaCalls++
	} else {
		fixture.itemCalls++
	}
	fixture.mutex.Unlock()
	metadata := func(id string) map[string]any {
		return map[string]any{"id": id, "name": id, "slug": id, "isPrivate": false, "updatedAt": updatedAt, "lastAddedAt": updatedAt}
	}
	page := map[string]any{"hasNextPage": false, "endCursor": nil}
	var response any
	if query.Variables.ID == "" {
		response = map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer", "lists": map[string]any{"pageInfo": page, "nodes": []any{metadata("selected"), metadata("ignored")}}}}}
	} else {
		if onItems != nil {
			if err := onItems(request.Context()); err != nil {
				return nil, err
			}
		}
		if query.Variables.ID != "selected" {
			return nil, errors.New("unselected list items requested")
		}
		nodes := []any{}
		for index, private := range []bool{false, true, false} {
			if private && !fixture.privateSource {
				continue
			}
			nodes = append(nodes, map[string]any{"__typename": "Repository", "id": fmt.Sprintf("repo-%d", index), "databaseId": index + 1, "nameWithOwner": fmt.Sprintf("Org/Skill-%d", index), "isPrivate": private})
		}
		node := metadata("selected")
		node["__typename"], node["user"] = "UserList", map[string]any{"id": "viewer"}
		node["items"] = map[string]any{"totalCount": len(nodes), "pageInfo": page, "nodes": nodes}
		response = map[string]any{"data": map[string]any{"node": node}}
	}
	body, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func archiveFixture(t *testing.T) *repository.ArchiveSnapshot {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	content := "---\nname: Example\ndescription: A scheduler fixture\n---\nExample\n"
	if err := archive.WriteHeader(&tar.Header{Name: "repo-main/SKILL.md", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(archive, content); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		t.Fatal(err)
	}
	fetcher := repository.NewArchiveFetcher(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(buffer.Bytes()))}, nil
	})})
	snapshot, err := fetcher.Fetch(t.Context(), repository.Reference{NameWithOwner: "org/skill"})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func newScheduler(t *testing.T, fixture *fixture, fetcher library.ArchiveFetcher, options Options) *Scheduler {
	t.Helper()
	scheduler, err := New(fixture.store, fixture.cache, fetcher, fixture.client, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Close() })
	return scheduler
}

func TestSchedulerPrivateNeverUsesPublicFetcherAndPublicSelectionIsImmutable(t *testing.T) {
	fixture := newFixture(t)
	fixture.privateSource = true
	snapshot := archiveFixture(t)
	var fetches atomic.Int64
	scheduler := newScheduler(t, fixture, fetchFunc(func(_ context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		if reference.NameWithOwner == "org/skill-1" {
			t.Fatal("private repository fetched")
		}
		fetches.Add(1)
		return snapshot, nil
	}), Options{MaxCollections: 1})
	for attempt := range 4 {
		err := scheduler.RunOnce(t.Context())
		if attempt == 1 {
			if !errors.Is(err, state.ErrSourceAccessUnavailable) {
				t.Fatalf("private capability absence did not fail closed: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		entries, err := fixture.store.ListCollectedRepositories(t.Context(), "alice")
		if err != nil || len(entries) != []int{1, 1, 2, 2}[attempt] {
			catalog, catalogErr := fixture.store.GitHubCatalog(t.Context(), "alice")
			t.Fatalf("public collection after run %d: count=%d, error=%v, lists=%d, repositories=%d, catalog error=%v", attempt+1, len(entries), err, len(catalog.Lists), len(catalog.Repositories), catalogErr)
		}
		for _, entry := range entries {
			if entry.Manual || entry.ArchiveHash != snapshot.Hash {
				t.Fatal("automatic immutable collection metadata is incorrect")
			}
		}
	}
	fixture.mutex.Lock()
	itemCalls := fixture.itemCalls
	fixture.mutex.Unlock()
	if fetches.Load() != 2 || itemCalls != 1 {
		t.Fatalf("expected two immutable fetches and one selected list read, got %d/%d", fetches.Load(), itemCalls)
	}
}

func TestSchedulerRejectsCredentialAndSelectionChangesDuringWork(t *testing.T) {
	for _, stage := range []string{"items", "archive"} {
		for _, change := range []string{"credential", "selection"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				fixture := newFixture(t)
				snapshot := archiveFixture(t)
				var once sync.Once
				mutate := func() error {
					var mutationError error
					once.Do(func() {
						if change == "selection" {
							for _, selected := range []bool{false, true} {
								if err := fixture.store.SetListSubscription(t.Context(), "alice", "selected", selected); err != nil {
									mutationError = err
									return
								}
							}
							return
						}
						credential, err := fixture.store.Credential(t.Context(), "alice")
						if err != nil {
							mutationError = err
							return
						}
						token, err := github.NewToken(rand.Text(), nil)
						if err != nil {
							mutationError = err
							return
						}
						_, mutationError = fixture.store.PutCredential(t.Context(), "alice", "viewer", credential.Generation, token)
					})
					return mutationError
				}
				if stage == "items" {
					fixture.onItems = func(context.Context) error { return mutate() }
				}
				scheduler := newScheduler(t, fixture, fetchFunc(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
					if stage == "archive" {
						if err := mutate(); err != nil {
							return nil, err
						}
					}
					return snapshot, nil
				}), Options{})
				if err := scheduler.RunOnce(t.Context()); !errors.Is(err, state.ErrSyncSuperseded) {
					t.Fatalf("stale work was not rejected: %v", err)
				}
				entries, err := fixture.store.ListCollectedRepositories(t.Context(), "alice")
				if err != nil || len(entries) != 0 {
					t.Fatalf("stale work collected repositories: %d, %v", len(entries), err)
				}
			})
		}
	}
}

func TestSchedulerCloseCancelsAndWaitsForActiveWork(t *testing.T) {
	fixture := newFixture(t)
	entered, exited := make(chan struct{}), make(chan struct{})
	scheduler := newScheduler(t, fixture, fetchFunc(func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	}), Options{Interval: time.Millisecond})
	if err := scheduler.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(t.Context()); !errors.Is(err, ErrStarted) {
		t.Fatalf("duplicate start: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("periodic worker did not start")
	}
	if err := scheduler.RunOnce(t.Context()); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlapping work was not bounded: %v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- scheduler.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel and join active work")
	}
	select {
	case <-exited:
	default:
		t.Fatal("Close returned before the fetch exited")
	}
	if err := scheduler.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.RunOnce(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed scheduler accepted work: %v", err)
	}
	if err := scheduler.Start(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed scheduler restarted: %v", err)
	}
}

func TestSchedulerRunTimeoutAndCallerCancellation(t *testing.T) {
	fixture := newFixture(t)
	var fetches atomic.Int64
	scheduler := newScheduler(t, fixture, fetchFunc(func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
		fetches.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}), Options{Timeout: 30 * time.Millisecond})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := scheduler.RunOnce(ctx); !errors.Is(err, context.Canceled) || fetches.Load() != 0 {
		t.Fatalf("caller cancellation: fetches=%d, error=%v", fetches.Load(), err)
	}
	if err := scheduler.RunOnce(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run timeout: %v", err)
	}
	entries, err := fixture.store.ListCollectedRepositories(t.Context(), "alice")
	if err != nil || len(entries) != 0 {
		t.Fatalf("timed-out fetch applied: %d, %v", len(entries), err)
	}
}

func TestSchedulerBoundsAndRotatesUsers(t *testing.T) {
	fixture := newFixture(t)
	fixture.addUser(t, "bob")
	snapshot := archiveFixture(t)
	scheduler := newScheduler(t, fixture, fetchFunc(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
		return snapshot, nil
	}), Options{MaxUsers: 1, MaxCollections: 1})
	for attempt := range 3 {
		if err := scheduler.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
		alice, aliceError := fixture.store.ListCollectedRepositories(t.Context(), "alice")
		bob, bobError := fixture.store.ListCollectedRepositories(t.Context(), "bob")
		if aliceError != nil || bobError != nil || len(alice) != 1+attempt/2 || len(bob) != min(attempt, 1) {
			t.Fatalf("user rotation run %d: alice=%d bob=%d errors=%v/%v", attempt+1, len(alice), len(bob), aliceError, bobError)
		}
	}
}

func TestSchedulerRunsPeriodicallyAndStopsWithParent(t *testing.T) {
	fixture := newFixture(t)
	snapshot := archiveFixture(t)
	scheduler := newScheduler(t, fixture, fetchFunc(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) { return snapshot, nil }), Options{Interval: time.Millisecond})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := scheduler.Start(ctx); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(5 * time.Second)
	for {
		fixture.mutex.Lock()
		calls := fixture.metaCalls
		fixture.mutex.Unlock()
		if calls >= 8 {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("periodic worker did not repeat")
		}
	}
	cancel()
	if err := scheduler.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerRejectsInvalidBounds(t *testing.T) {
	fixture := newFixture(t)
	fetcher := fetchFunc(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) { return nil, nil })
	for _, options := range []Options{{Interval: -1}, {Interval: 25 * time.Hour}, {Timeout: -1}, {Timeout: 11 * time.Minute}, {MaxUsers: -1}, {MaxUsers: 1001}, {MaxCollections: -1}, {MaxCollections: 1001}} {
		if scheduler, err := New(fixture.store, fixture.cache, fetcher, fixture.client, options); err == nil {
			_ = scheduler.Close()
			t.Fatalf("invalid scheduler bounds accepted: %+v", options)
		}
	}
}

func TestSchedulerCollectionCursorSurvivesWorkerRestartAndFailures(t *testing.T) {
	fixture := newFixture(t)
	snapshot := archiveFixture(t)
	failed := errors.New("fixture archive unavailable")
	var attempted []string
	fetcher := fetchFunc(func(_ context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		attempted = append(attempted, reference.NameWithOwner)
		if reference.NameWithOwner == "org/skill-0" {
			return nil, failed
		}
		return snapshot, nil
	})
	scheduler := newScheduler(t, fixture, fetcher, Options{MaxCollections: 1})
	if err := scheduler.RunOnce(t.Context()); !errors.Is(err, failed) {
		t.Fatalf("failed fetch did not report failure: %v", err)
	}
	if err := scheduler.Close(); err != nil {
		t.Fatal(err)
	}
	scheduler = newScheduler(t, fixture, fetcher, Options{MaxCollections: 1})
	if err := scheduler.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(attempted) != 2 || attempted[0] != "org/skill-0" || attempted[1] != "org/skill-2" {
		t.Fatalf("failed low-ID source starved the next batch: %v", attempted)
	}
}

func TestSchedulerBoundsCollectionByCredentialExpiry(t *testing.T) {
	fixture := newFixture(t)
	credential, err := fixture.store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	material, err := json.Marshal(map[string]any{"version": 1, "access": rand.Text(), "expires_at": time.Now().UTC().Add(250 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	token, err := github.DecodeStoredToken(material)
	clear(material)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.PutCredential(t.Context(), "alice", "viewer", credential.Generation, token); err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int64
	scheduler := newScheduler(t, fixture, fetchFunc(func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
		fetches.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}), Options{Timeout: 2 * time.Second})
	if err := scheduler.RunOnce(t.Context()); !errors.Is(err, context.DeadlineExceeded) || fetches.Load() != 1 {
		t.Fatalf("credential expiry did not bound active collection: fetches=%d, error=%v", fetches.Load(), err)
	}
	entries, err := fixture.store.ListCollectedRepositories(t.Context(), "alice")
	if err != nil || len(entries) != 0 {
		t.Fatalf("expired credential applied collection: %d, %v", len(entries), err)
	}
}

func TestSchedulerCloseCancelsGraphQLRead(t *testing.T) {
	fixture := newFixture(t)
	entered := make(chan struct{})
	fixture.onItems = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	var fetches atomic.Int64
	scheduler := newScheduler(t, fixture, fetchFunc(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
		fetches.Add(1)
		return nil, errors.New("unexpected archive fetch")
	}), Options{})
	if err := scheduler.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("GraphQL item read did not start")
	}
	finished := make(chan error, 1)
	go func() { finished <- scheduler.Close() }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel GraphQL read")
	}
	if fetches.Load() != 0 {
		t.Fatal("cancelled GraphQL read started collection")
	}
	repositories, err := fixture.store.SubscribedRepositories(t.Context(), "alice")
	if err != nil || len(repositories) != 0 {
		t.Fatalf("cancelled GraphQL read left members visible: %d, %v", len(repositories), err)
	}
}
