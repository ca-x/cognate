package state_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/github"
	"github.com/ca-x/skillhub/internal/server/ent"
	"github.com/ca-x/skillhub/internal/server/state"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (function transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestListSyncGenerationSurvivesAnotherProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("server storage security is unsupported on Windows")
	}
	if path := os.Getenv("SKILLHUB_SERVER_SYNC_FIXTURE"); path != "" {
		key := make([]byte, 32)
		if _, err := io.ReadFull(os.Stdin, key); err != nil {
			t.Fatal(err)
		}
		store, err := state.Open(t.Context(), path, key)
		if err != nil {
			t.Fatal(err)
		}
		credential, err := store.Credential(t.Context(), "alice")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.BeginListSync(t.Context(), "alice", credential.Generation); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
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
	generation, err := store.PutCredential(t.Context(), "alice", "viewer", "", token)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.BeginListSync(t.Context(), "alice", generation)
	if err != nil {
		t.Fatal(err)
	}
	result := readSnapshot(t, store, ticket, credential.Token)
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestListSyncGenerationSurvivesAnotherProcess$")
	command.Env = append(os.Environ(), "SKILLHUB_SERVER_SYNC_FIXTURE="+path)
	command.Stdin = bytes.NewReader(key)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child process failed: %v: %s", err, output)
	}
	called := false
	err = store.CommitListSync(t.Context(), result, func(context.Context, *ent.Tx, github.ListsSnapshot) error { called = true; return nil })
	if !errors.Is(err, state.ErrSyncSuperseded) || called {
		t.Fatalf("other process did not supersede synchronization: %v", err)
	}
}

func emptyClient(t *testing.T, token github.Token) *github.ListsClient {
	t.Helper()
	client, err := github.NewListsClient(token, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"viewer":{"id":"viewer","lists":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}`))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func readSnapshot(t *testing.T, store *state.Store, ticket state.ListSyncTicket, token github.Token) state.ListSyncResult {
	t.Helper()
	result, err := store.ReadListSync(t.Context(), ticket, emptyClient(t, token))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestListSyncCASRejectsSupersededOrReauthorizedWork(t *testing.T) {
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
	generation, err := store.PutCredential(t.Context(), "alice", "viewer", "", token)
	if err != nil {
		t.Fatal(err)
	}
	firstCredential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	firstTicket, err := store.BeginListSync(t.Context(), "alice", generation)
	if err != nil {
		t.Fatal(err)
	}
	firstResult := readSnapshot(t, store, firstTicket, firstCredential.Token)
	secondCredential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	secondTicket, err := store.BeginListSync(t.Context(), "alice", generation)
	if err != nil {
		t.Fatal(err)
	}
	secondResult := readSnapshot(t, store, secondTicket, secondCredential.Token)
	called := false
	apply := func(ctx context.Context, tx *ent.Tx, snapshot github.ListsSnapshot) error {
		called = true
		return tx.User.Create().SetUserKey("applied").Exec(ctx)
	}
	if err := store.CommitListSync(t.Context(), firstResult, apply); !errors.Is(err, state.ErrSyncSuperseded) || called {
		t.Fatalf("old sync committed: %v", err)
	}
	if err := store.CommitListSync(t.Context(), secondResult, apply); err != nil || !called {
		t.Fatalf("current sync failed: %v", err)
	}
	called = false
	if err := store.CommitListSync(t.Context(), secondResult, apply); !errors.Is(err, state.ErrSyncSuperseded) || called {
		t.Fatalf("replayed synchronization committed: %v", err)
	}
	thirdTicket, err := store.BeginListSync(t.Context(), "alice", generation)
	if err != nil {
		t.Fatal(err)
	}
	thirdCredential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	thirdResult := readSnapshot(t, store, thirdTicket, thirdCredential.Token)
	if _, err := store.PutCredential(t.Context(), "alice", "viewer", generation, token); err != nil {
		t.Fatal(err)
	}
	called = false
	if err := store.CommitListSync(t.Context(), thirdResult, apply); !errors.Is(err, state.ErrSyncSuperseded) || called {
		t.Fatalf("reauthorized sync committed: %v", err)
	}
}

func TestListSyncCommitRollsBackApplication(t *testing.T) {
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
	generation, err := store.PutCredential(t.Context(), "alice", "viewer", "", token)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.BeginListSync(t.Context(), "alice", generation)
	if err != nil {
		t.Fatal(err)
	}
	result := readSnapshot(t, store, ticket, credential.Token)
	injected := errors.New("injected transaction failure")
	err = store.CommitListSync(t.Context(), result, func(ctx context.Context, tx *ent.Tx, _ github.ListsSnapshot) error {
		if err := tx.User.Create().SetUserKey("must-rollback").Exec(ctx); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected failure: %v", err)
	}
	if err := store.CommitListSync(t.Context(), result, func(ctx context.Context, tx *ent.Tx, _ github.ListsSnapshot) error {
		return tx.User.Create().SetUserKey("must-rollback").Exec(ctx)
	}); err != nil {
		t.Fatalf("rolled-back sync could not be retried: %v", err)
	}
	if err := store.CreateUser(t.Context(), "must-rollback"); err == nil {
		t.Fatal("successful sync did not commit application")
	}
}

func TestListSyncRejectsClientFromDifferentCredential(t *testing.T) {
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
	credential, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.PutCredential(t.Context(), "alice", "viewer", "", credential)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.BeginListSync(t.Context(), "alice", generation)
	if err != nil {
		t.Fatal(err)
	}
	other, err := github.NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := emptyClient(t, other)
	_, err = store.ReadListSync(t.Context(), ticket, client)
	if !errors.Is(err, state.ErrSyncSuperseded) {
		t.Fatalf("accepted different credential: %v", err)
	}
	forged, err := github.NewToken(credential.Reveal(), []string{"repo"})
	if err != nil {
		t.Fatal(err)
	}
	client = emptyClient(t, forged)
	_, err = store.ReadListSync(t.Context(), ticket, client)
	if !errors.Is(err, state.ErrSyncSuperseded) {
		t.Fatalf("accepted forged credential metadata: %v", err)
	}
}

func TestListSyncTicketAllowsOnlyOneSnapshotRead(t *testing.T) {
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
	generation, err := store.PutCredential(t.Context(), "alice", "viewer", "", token)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.BeginListSync(t.Context(), "alice", generation)
	if err != nil {
		t.Fatal(err)
	}
	client := emptyClient(t, credential.Token)
	if _, err := store.ReadListSync(t.Context(), ticket, client); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadListSync(t.Context(), ticket, client); !errors.Is(err, state.ErrSyncSuperseded) {
		t.Fatalf("reused sync ticket for a second read: %v", err)
	}
}

func TestListSyncReadFailureRequiresNewTicket(t *testing.T) {
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
	generation, err := store.PutCredential(t.Context(), "alice", "viewer", "", token)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.Credential(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := store.BeginListSync(t.Context(), "alice", generation)
	if err != nil {
		t.Fatal(err)
	}
	var requests int
	client, err := github.NewListsClient(credential.Token, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"viewer":{"id":"viewer","lists":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}`))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadListSync(t.Context(), ticket, client); err == nil {
		t.Fatal("accepted failed GitHub read")
	}
	if _, err := store.ReadListSync(t.Context(), ticket, client); !errors.Is(err, state.ErrSyncSuperseded) || requests != 1 {
		t.Fatalf("reused failed read ticket: requests=%d error=%v", requests, err)
	}
	newTicket, err := store.BeginListSync(t.Context(), "alice", generation)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ReadListSync(t.Context(), newTicket, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitListSync(t.Context(), result, func(context.Context, *ent.Tx, github.ListsSnapshot) error { return nil }); err != nil {
		t.Fatalf("new sync did not complete: %v", err)
	}
}
