package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	githubv1 "github.com/ca-x/skillhub/gen/skillhub/github/v1"
	"github.com/ca-x/skillhub/gen/skillhub/github/v1/githubv1connect"
)

type graphTransport func(*http.Request) (*http.Response, error)

func (transport graphTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestGitHubAPIConnectSelectSyncAndUserIsolation(t *testing.T) {
	service, store := testServiceAndStore(t)
	var calls atomic.Int64
	tokenValue := rand.Text()
	upstream := &http.Client{Transport: graphTransport(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.String() != "https://api.github.com/graphql" || request.Header.Get("Authorization") != "Bearer "+tokenValue {
			return nil, fmt.Errorf("unexpected upstream request")
		}
		var query struct {
			Variables struct {
				ID string `json:"id"`
			} `json:"variables"`
		}
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			return nil, err
		}
		metadata := map[string]any{"id": "list-one", "name": "Skills", "slug": "skills", "isPrivate": true, "updatedAt": "2026-01-01T00:00:00Z", "lastAddedAt": "2026-01-01T00:00:00Z"}
		page := map[string]any{"hasNextPage": false, "endCursor": nil}
		var payload any
		if query.Variables.ID == "" {
			payload = map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "github-viewer", "lists": map[string]any{"pageInfo": page, "nodes": []any{metadata}}}}}
		} else {
			metadata["__typename"] = "UserList"
			metadata["user"] = map[string]any{"id": "github-viewer"}
			metadata["items"] = map[string]any{"totalCount": 1, "pageInfo": page, "nodes": []any{map[string]any{"__typename": "Repository", "id": "repo-one", "nameWithOwner": "org/skills", "databaseId": 123, "isPrivate": true}}}
			payload = map[string]any{"data": map[string]any{"node": metadata}}
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	server := httptest.NewServer(NewWithOptions(service, Options{Store: store, GitHubClient: upstream}))
	t.Cleanup(server.Close)
	client := githubv1connect.NewGitHubServiceClient(server.Client(), server.URL)
	if _, err := client.Sync(t.Context(), connect.NewRequest(&githubv1.SyncRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated || calls.Load() != 0 {
		t.Fatalf("unauthenticated sync: %v", err)
	}
	sessions := make([]string, 0, 2)
	for _, name := range []string{"alice", "bob"} {
		password := []byte(rand.Text())
		if _, err := service.Register(t.Context(), name, password); err != nil {
			t.Fatal(err)
		}
		token, err := service.Login(t.Context(), name, password)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, token.Reveal())
	}
	connectRequest := connect.NewRequest(&githubv1.ConnectTokenRequest{AccessToken: tokenValue})
	connectRequest.Header().Set("Authorization", "Bearer "+sessions[0])
	connected, err := client.ConnectToken(t.Context(), connectRequest)
	if err != nil || connected.Msg.ViewerId != "github-viewer" || connected.Msg.CredentialGeneration == "" {
		t.Fatalf("connect: %v", err)
	}
	if _, err := client.ConnectToken(t.Context(), connectRequest); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale credential replacement: %v", err)
	}
	syncRequest := connect.NewRequest(&githubv1.SyncRequest{})
	syncRequest.Header().Set("Authorization", "Bearer "+sessions[0])
	result, err := client.Sync(t.Context(), syncRequest)
	if err != nil || len(result.Msg.GetCatalog().GetLists()) != 1 || len(result.Msg.GetCatalog().GetRepositories()) != 0 {
		t.Fatalf("discover: %v", err)
	}
	if deadline := result.Msg.Catalog.ValidUntilUnix; deadline <= time.Now().Unix() || deadline > result.Msg.Catalog.Lists[0].VerifiedAtUnix+3600 {
		t.Fatal("catalog expiry does not bind returned observations")
	}
	selection := connect.NewRequest(&githubv1.SetListSubscriptionRequest{ListId: "list-one", Subscribed: true})
	selection.Header().Set("Authorization", "Bearer "+sessions[0])
	if _, err := client.SetListSubscription(t.Context(), selection); err != nil {
		t.Fatal(err)
	}
	result, err = client.Sync(t.Context(), syncRequest)
	if err != nil || len(result.Msg.GetCatalog().GetRepositories()) != 1 || !result.Msg.Catalog.Lists[0].Subscribed || result.Msg.Catalog.Repositories[0].NameWithOwner != "org/skills" {
		t.Fatalf("subscribed sync: %v", err)
	}
	other := connect.NewRequest(&githubv1.GetCatalogRequest{})
	other.Header().Set("Authorization", "Bearer "+sessions[1])
	other.Header().Set("X-User-Id", "alice")
	catalog, err := client.GetCatalog(t.Context(), other)
	if err != nil || catalog.Msg.GetCatalog().GetConnected() || len(catalog.Msg.GetCatalog().GetRepositories()) != 0 {
		t.Fatalf("other user catalog: %v", err)
	}
	selection.Header().Set("Authorization", "Bearer "+sessions[1])
	if _, err := client.SetListSubscription(t.Context(), selection); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("cross-user selection: %v", err)
	}
}

func TestGitHubAPIErrorsDoNotReturnUpstreamSecrets(t *testing.T) {
	service, store := testServiceAndStore(t)
	password := []byte(rand.Text())
	if _, err := service.Register(t.Context(), "alice", password); err != nil {
		t.Fatal(err)
	}
	session, err := service.Login(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	secret := rand.Text()
	upstream := &http.Client{Transport: graphTransport(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("upstream %s", secret) })}
	server := httptest.NewServer(NewWithOptions(service, Options{Store: store, GitHubClient: upstream}))
	t.Cleanup(server.Close)
	client := githubv1connect.NewGitHubServiceClient(server.Client(), server.URL)
	request := connect.NewRequest(&githubv1.ConnectTokenRequest{AccessToken: secret})
	request.Header().Set("Authorization", "Bearer "+session.Reveal())
	_, err = client.ConnectToken(t.Context(), request)
	if connect.CodeOf(err) != connect.CodeUnavailable || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe upstream error: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.GetCatalog(ctx, connect.NewRequest(&githubv1.GetCatalogRequest{})); connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("canceled request: %v", err)
	}
}
