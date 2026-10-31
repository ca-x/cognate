package state

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/ent"
	"modernc.org/sqlite"
)

type githubTransactionTestKey struct{}

type githubTransactionTransport func(*http.Request) (*http.Response, error)

func (transport githubTransactionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func requireGitHubTransactionSuccess(test *testing.T, phase string, err error) {
	test.Helper()
	if err == nil {
		return
	}
	if failure, valid := errors.AsType[*sqlite.Error](err); valid {
		test.Fatalf("phase=%s type=sqlite code=%d", phase, failure.Code())
	}
	test.Fatalf("phase=%s type=%T", phase, err)
}

func TestGitHubTransactionsDoNotUpgradeStaleSnapshots(test *testing.T) {
	for _, phase := range []string{"catalog", "credential", "begin", "claim", "commit", "subscription"} {
		test.Run(phase, func(test *testing.T) {
			directory := test.TempDir()
			requireGitHubTransactionSuccess(test, "directory", os.Chmod(directory, 0o700))
			store, err := Open(test.Context(), filepath.Join(directory, "github.db"), make([]byte, 32))
			requireGitHubTransactionSuccess(test, "open", err)
			test.Cleanup(func() { _ = store.Close() })
			requireGitHubTransactionSuccess(test, "owner", store.CreateUser(test.Context(), "alice"))
			token, err := github.NewToken(rand.Text(), nil)
			requireGitHubTransactionSuccess(test, "token", err)
			generation, err := store.PutCredential(test.Context(), "alice", "viewer", "", token)
			requireGitHubTransactionSuccess(test, "credential-setup", err)
			owner, err := findUser(test.Context(), store.state.client, "alice")
			requireGitHubTransactionSuccess(test, "owner-setup", err)
			now := time.Now().UTC()
			requireGitHubTransactionSuccess(test, "list-setup", store.state.client.GitHubList.Create().SetUserID(owner.ID).SetViewerID("viewer").SetListKey("list").SetCredentialGeneration(generation).SetName("list").SetSlug("list").SetPrivate(false).SetUpdatedAt(now).SetLastAddedAt(now).SetVerifiedAt(now).SetVisible(true).Exec(test.Context()))
			client, err := github.NewListsClient(token, &http.Client{Transport: githubTransactionTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"viewer":{"id":"viewer","lists":{"totalCount":0,"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}`))}, nil
			})})
			requireGitHubTransactionSuccess(test, "client-setup", err)
			var ticket ListSyncTicket
			var result ListSyncResult
			if phase == "claim" || phase == "commit" {
				ticket, err = store.BeginListSync(test.Context(), "alice", generation)
				requireGitHubTransactionSuccess(test, "ticket-setup", err)
			}
			if phase == "commit" {
				result, err = store.ReadListSync(test.Context(), ticket, client)
				requireGitHubTransactionSuccess(test, "snapshot-setup", err)
			}
			var written, interleaved atomic.Bool
			store.state.client.GitHubCredential.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					value, err := next.Mutate(ctx, mutation)
					if err == nil && ctx.Value(githubTransactionTestKey{}) != nil {
						written.Store(true)
					}
					return value, err
				})
			})
			store.state.client.User.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
					value, err := next.Query(ctx, query)
					if err == nil && ctx.Value(githubTransactionTestKey{}) != nil && !written.Load() && interleaved.CompareAndSwap(false, true) {
						requireGitHubTransactionSuccess(test, "interleaved-writer", store.CreateUser(test.Context(), "unrelated-writer"))
					}
					return value, err
				})
			}))
			ctx := context.WithValue(test.Context(), githubTransactionTestKey{}, true)
			switch phase {
			case "catalog":
				_, err = store.GitHubCatalog(ctx, "alice")
			case "credential":
				_, err = store.PutCredential(ctx, "alice", "viewer", generation, token)
			case "begin":
				_, err = store.BeginListSync(ctx, "alice", generation)
			case "claim":
				err = store.claimListRead(ctx, ticket, "fetch", client)
			case "commit":
				err = store.ApplyListSync(ctx, result)
			case "subscription":
				err = store.SetListSubscription(ctx, "alice", "list", true)
			}
			test.Logf("phase=%s read_before_writer=%t", phase, interleaved.Load())
			requireGitHubTransactionSuccess(test, phase, err)
			if !written.Load() {
				test.Fatal("operation did not acquire its transaction write lock")
			}
		})
	}
}

func TestGitHubTransactionsAcrossConnections(test *testing.T) {
	directory := test.TempDir()
	requireGitHubTransactionSuccess(test, "directory", os.Chmod(directory, 0o700))
	path := filepath.Join(directory, "github.db")
	key := make([]byte, 32)
	first, err := Open(test.Context(), path, key)
	requireGitHubTransactionSuccess(test, "first-open", err)
	test.Cleanup(func() { _ = first.Close() })
	second, err := Open(test.Context(), path, key)
	requireGitHubTransactionSuccess(test, "second-open", err)
	test.Cleanup(func() { _ = second.Close() })
	requireGitHubTransactionSuccess(test, "owner", first.CreateUser(test.Context(), "alice"))
	token, err := github.NewToken(rand.Text(), nil)
	requireGitHubTransactionSuccess(test, "token", err)
	generation, err := first.PutCredential(test.Context(), "alice", "viewer", "", token)
	requireGitHubTransactionSuccess(test, "credential", err)
	for range 30 {
		start := make(chan struct{})
		var group sync.WaitGroup
		var catalogError, syncError error
		var snapshot CatalogSnapshot
		group.Go(func() {
			<-start
			snapshot, catalogError = first.GitHubCatalog(test.Context(), "alice")
		})
		group.Go(func() {
			<-start
			_, syncError = second.BeginListSync(test.Context(), "alice", generation)
		})
		close(start)
		group.Wait()
		requireGitHubTransactionSuccess(test, "concurrent-catalog", catalogError)
		requireGitHubTransactionSuccess(test, "concurrent-begin", syncError)
		if snapshot.Generation != generation || snapshot.ViewerID != "viewer" {
			test.Fatal("concurrent catalog changed credential identity")
		}
	}
}
