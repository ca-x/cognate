package githubauth_test

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/skillhub/internal/github"
	"github.com/ca-x/skillhub/internal/server/githubauth"
	"github.com/ca-x/skillhub/internal/server/state"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (transport transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func fixtureStore(t *testing.T) *state.Store {
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
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, user := range []string{"alice", "bob"} {
		if err := store.CreateUser(t.Context(), user); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func fixtureClient(t *testing.T, accessToken string, polls *atomic.Int64, outcome string) *http.Client {
	t.Helper()
	deviceCode := rand.Text()
	return &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		var value any
		switch request.URL.String() {
		case "https://github.com/login/device/code":
			value = map[string]any{"device_code": deviceCode, "user_code": "AAAA-BBBB", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 1}
		case "https://github.com/login/oauth/access_token":
			polls.Add(1)
			if err := request.ParseForm(); err != nil {
				return nil, err
			}
			if request.Form.Get("device_code") != deviceCode {
				return nil, errors.New("incorrect device code")
			}
			if outcome != "" {
				value = map[string]any{"error": outcome}
			} else {
				value = map[string]any{"access_token": accessToken, "token_type": "bearer", "scope": ""}
			}
		case "https://api.github.com/graphql":
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				return nil, errors.New("incorrect access token")
			}
			value = map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "github-viewer", "lists": map[string]any{"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}, "nodes": []any{}}}}}
		default:
			return nil, errors.New("unexpected URL")
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
}

func TestDeviceAuthorizationStoresTokenOnlyForInitiatingUser(t *testing.T) {
	store := fixtureStore(t)
	synctest.Test(t, func(t *testing.T) {
		access := rand.Text()
		var polls atomic.Int64
		service, err := githubauth.NewService(store, "test-client", fixtureClient(t, access, &polls, ""))
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		authorization, err := service.Start(t.Context(), "alice", "", false)
		if err != nil || authorization.ID == "" || authorization.UserCode != "AAAA-BBBB" || authorization.State != githubauth.Pending {
			t.Fatalf("start: %v", err)
		}
		if _, err := service.Get("bob", authorization.ID); !errors.Is(err, githubauth.ErrNotFound) {
			t.Fatalf("other user lookup: %v", err)
		}
		if _, err := service.Start(t.Context(), "alice", "", false); !errors.Is(err, githubauth.ErrInProgress) {
			t.Fatalf("duplicate flow: %v", err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		completed, err := service.Get("alice", authorization.ID)
		if err != nil || completed.State != githubauth.Complete || completed.Generation == "" || completed.ViewerID != "github-viewer" || completed.UserCode != "" {
			t.Fatalf("completion: state %v, error %v", completed.State, err)
		}
		credential, err := store.Credential(t.Context(), "alice")
		if err != nil || credential.Token.Reveal() != access || credential.Generation != completed.Generation {
			t.Fatalf("stored credential mismatch: %v", err)
		}
		if _, err := store.Credential(t.Context(), "bob"); !errors.Is(err, state.ErrCredentialNotFound) {
			t.Fatalf("other user received credential: %v", err)
		}
		if polls.Load() != 1 {
			t.Fatal("unexpected repeated token polling")
		}
	})
}

func TestDeviceCancellationAndTerminalStates(t *testing.T) {
	for _, outcome := range []string{"cancel", "access_denied", "expired_token", "unknown_error"} {
		t.Run(outcome, func(t *testing.T) {
			store := fixtureStore(t)
			synctest.Test(t, func(t *testing.T) {
				var polls atomic.Int64
				service, err := githubauth.NewService(store, "test-client", fixtureClient(t, rand.Text(), &polls, outcome))
				if err != nil {
					t.Fatal(err)
				}
				defer service.Close()
				authorization, err := service.Start(t.Context(), "alice", "", false)
				if err != nil {
					t.Fatal(err)
				}
				want := githubauth.Failed
				switch outcome {
				case "cancel":
					want = githubauth.Canceled
					if _, err := service.Cancel("bob", authorization.ID); !errors.Is(err, githubauth.ErrNotFound) {
						t.Fatalf("other user cancellation: %v", err)
					}
					if _, err := service.Cancel("alice", authorization.ID); err != nil {
						t.Fatal(err)
					}
				case "access_denied":
					want = githubauth.Denied
				case "expired_token":
					want = githubauth.Expired
				}
				time.Sleep(2 * time.Second)
				synctest.Wait()
				current, err := service.Get("alice", authorization.ID)
				if err != nil || current.State != want {
					t.Fatalf("terminal state = %v, error = %v", current.State, err)
				}
				if outcome == "cancel" && polls.Load() != 0 {
					t.Fatal("canceled authorization contacted token endpoint")
				}
				if _, err := store.Credential(t.Context(), "alice"); !errors.Is(err, state.ErrCredentialNotFound) {
					t.Fatalf("failed authorization stored token: %v", err)
				}
			})
		})
	}
}

func TestLateDeviceAuthorizationDoesNotReplaceNewerCredential(t *testing.T) {
	store := fixtureStore(t)
	synctest.Test(t, func(t *testing.T) {
		var polls atomic.Int64
		service, err := githubauth.NewService(store, "test-client", fixtureClient(t, rand.Text(), &polls, ""))
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		authorization, err := service.Start(t.Context(), "alice", "", false)
		if err != nil {
			t.Fatal(err)
		}
		newer, err := github.NewToken(rand.Text(), nil)
		if err != nil {
			t.Fatal(err)
		}
		generation, err := store.PutCredential(t.Context(), "alice", "newer-viewer", "", newer)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		current, err := service.Get("alice", authorization.ID)
		if err != nil || current.State != githubauth.Superseded {
			t.Fatalf("superseded flow: %v", err)
		}
		credential, err := store.Credential(t.Context(), "alice")
		if err != nil || credential.Generation != generation || credential.Token.Reveal() != newer.Reveal() {
			t.Fatal("late authorization overwrote newer credential")
		}
	})
}

func TestCloseCancelsAnInFlightAuthorizationStart(t *testing.T) {
	store := fixtureStore(t)
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		upstream := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			close(started)
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}
		service, err := githubauth.NewService(store, "test-client", upstream)
		if err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() { _, err := service.Start(t.Context(), "alice", "", false); finished <- err }()
		<-started
		service.Close()
		if err := <-finished; err == nil {
			t.Fatal("start succeeded after shutdown")
		}
		service.Close()
		if _, err := service.Get("alice", ""); !errors.Is(err, githubauth.ErrClosed) {
			t.Fatalf("service remained open: %v", err)
		}
	})
}

func TestTerminalAuthorizationsDoNotExhaustCapacity(t *testing.T) {
	store := fixtureStore(t)
	var polls atomic.Int64
	service, err := githubauth.NewService(store, "test-client", fixtureClient(t, rand.Text(), &polls, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for index := range 64 {
		user := fmt.Sprintf("terminal-user-%d", index)
		if err := store.CreateUser(t.Context(), user); err != nil {
			t.Fatal(err)
		}
		authorization, err := service.Start(t.Context(), user, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Cancel(user, authorization.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Start(t.Context(), "bob", "", false); err != nil {
		t.Fatalf("terminal tasks exhausted authorization capacity: %v", err)
	}
}

type blockedCredentialStore struct{ entered chan struct{} }

func (store blockedCredentialStore) Credential(ctx context.Context, _ string) (state.Credential, error) {
	close(store.entered)
	<-ctx.Done()
	return state.Credential{}, ctx.Err()
}

func (blockedCredentialStore) PutCredential(context.Context, string, string, string, github.Token) (string, error) {
	return "", errors.New("unexpected credential write")
}

func TestCloseWaitsForAndCancelsCredentialRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := blockedCredentialStore{entered: make(chan struct{})}
		service, err := githubauth.NewService(store, "test-client", nil)
		if err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() { _, err := service.Start(t.Context(), "alice", "", false); finished <- err }()
		<-store.entered
		service.Close()
		if err := <-finished; err == nil {
			t.Fatal("credential read outlived shutdown")
		}
	})
}

type blockedCommitStore struct {
	*state.Store
	entered chan struct{}
	once    sync.Once
}

func (store *blockedCommitStore) PutCredential(ctx context.Context, _, _, _ string, _ github.Token) (string, error) {
	store.once.Do(func() { close(store.entered) })
	<-ctx.Done()
	return "", ctx.Err()
}

func TestCapacityCheckDoesNotBlockShutdownCancellation(t *testing.T) {
	base := fixtureStore(t)
	synctest.Test(t, func(t *testing.T) {
		store := &blockedCommitStore{Store: base, entered: make(chan struct{})}
		var polls atomic.Int64
		service, err := githubauth.NewService(store, "test-client", fixtureClient(t, rand.Text(), &polls, ""))
		if err != nil {
			t.Fatal(err)
		}
		for index := range 64 {
			user := fmt.Sprintf("active-user-%d", index)
			if err := base.CreateUser(t.Context(), user); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Start(t.Context(), user, "", false); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(time.Second)
		<-store.entered
		synctest.Wait()
		startedAt := time.Now()
		capacity := make(chan error, 1)
		go func() { _, err := service.Start(t.Context(), "bob", "", false); capacity <- err }()
		synctest.Wait()
		service.Close()
		<-capacity
		if time.Since(startedAt) > time.Second {
			t.Fatal("capacity scan prevented shutdown from canceling credential writes")
		}
	})
}

func TestCancelInterruptsCredentialWriteBeforeWaitingForItsResult(t *testing.T) {
	base := fixtureStore(t)
	synctest.Test(t, func(t *testing.T) {
		store := &blockedCommitStore{Store: base, entered: make(chan struct{})}
		var polls atomic.Int64
		service, err := githubauth.NewService(store, "test-client", fixtureClient(t, rand.Text(), &polls, ""))
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		authorization, err := service.Start(t.Context(), "alice", "", false)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		<-store.entered
		startedAt := time.Now()
		canceled, err := service.Cancel("alice", authorization.ID)
		if err != nil || canceled.State != githubauth.Canceled || time.Since(startedAt) > time.Second {
			t.Fatalf("credential write cancellation: %v", err)
		}
	})
}
