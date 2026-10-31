package state

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
)

func TestGitHubTransactionLockWaitCancellation(test *testing.T) {
	for _, credential := range []bool{false, true} {
		for _, cancellation := range []string{"deadline", "cancel"} {
			name := cancellation + "/empty"
			if credential {
				name = cancellation + "/ready"
			}
			test.Run(name, func(test *testing.T) {
				directory := test.TempDir()
				requireGitHubTransactionSuccess(test, "directory", os.Chmod(directory, 0o700))
				path := filepath.Join(directory, "cancellation.db")
				first, err := Open(test.Context(), path, make([]byte, 32))
				requireGitHubTransactionSuccess(test, "first", err)
				test.Cleanup(func() { _ = first.Close() })
				second, err := Open(test.Context(), path, make([]byte, 32))
				requireGitHubTransactionSuccess(test, "second", err)
				test.Cleanup(func() { _ = second.Close() })
				requireGitHubTransactionSuccess(test, "owner", first.CreateUser(test.Context(), "cancellation-owner"))
				if credential {
					token, err := github.NewToken(rand.Text(), nil)
					requireGitHubTransactionSuccess(test, "token", err)
					_, err = first.PutCredential(test.Context(), "cancellation-owner", "viewer", "", token)
					requireGitHubTransactionSuccess(test, "credential", err)
				}
				holder, err := first.state.client.Tx(test.Context())
				requireGitHubTransactionSuccess(test, "holder", err)
				defer func() { _ = holder.Rollback() }()
				requireGitHubTransactionSuccess(test, "write-lock", holder.GitHubCredential.Update().AddSyncSequence(0).Exec(test.Context()))
				var ctx context.Context
				var cancel context.CancelFunc
				expected := context.Canceled
				if cancellation == "deadline" {
					ctx, cancel = context.WithTimeout(test.Context(), 100*time.Millisecond)
					expected = context.DeadlineExceeded
				} else {
					ctx, cancel = context.WithCancel(test.Context())
					stop := time.AfterFunc(100*time.Millisecond, cancel)
					defer stop.Stop()
				}
				defer cancel()
				result := make(chan error, 1)
				go func() { _, err := second.GitHubCatalog(ctx, "cancellation-owner"); result <- err }()
				select {
				case err := <-result:
					if !errors.Is(err, expected) {
						test.Fatalf("lock admission did not preserve cancellation: %T", err)
					}
				case <-time.After(time.Second):
					requireGitHubTransactionSuccess(test, "release-holder", holder.Rollback())
					<-result
					test.Fatal("canceled catalog remained blocked on another connection's write lock")
				}
				requireGitHubTransactionSuccess(test, "release-holder", holder.Rollback())
				catalog, err := second.GitHubCatalog(test.Context(), "cancellation-owner")
				if credential {
					requireGitHubTransactionSuccess(test, "catalog-after-cancel", err)
					if catalog.ViewerID != "viewer" {
						test.Fatal("canceled lock admission changed the credential identity")
					}
				} else if !errors.Is(err, ErrCredentialNotFound) {
					test.Fatalf("canceled lock admission changed the missing-credential result: %T", err)
				}
			})
		}
	}
}
