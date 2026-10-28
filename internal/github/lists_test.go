package github

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type graphQuery struct {
	Query     string `json:"query"`
	Variables struct {
		ListCursor *string `json:"listCursor"`
		ItemCursor *string `json:"itemCursor"`
		ID         string  `json:"id"`
	} `json:"variables"`
}

func TestListsFetchesEveryListAndEveryItemPage(t *testing.T) {
	secret := rand.Text()
	token, err := NewToken(secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.String() != graphQLURL || request.Header.Get("Authorization") != "Bearer "+secret {
			t.Fatal("incorrect GraphQL request")
		}
		var query graphQuery
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(query.Query, "lists(first: 100") {
			if query.Variables.ListCursor == nil {
				return listPageResponse("first", "list-next", true), nil
			}
			if *query.Variables.ListCursor != "list-next" {
				t.Fatal("incorrect list cursor")
			}
			return listPageResponse("second", "list-end", false), nil
		}
		if !strings.Contains(query.Query, "node(id: $id)") || !strings.Contains(query.Query, "items(first: 100") {
			t.Fatal("item query does not use per-list node")
		}
		switch query.Variables.ID {
		case "id-first":
			if query.Variables.ItemCursor == nil {
				items := make([]map[string]any, 0, 100)
				for index := range 100 {
					items = append(items, map[string]any{"__typename": "Repository", "id": fmt.Sprintf("repo-%d", index), "nameWithOwner": fmt.Sprintf("owner/repo-%d", index), "databaseId": index + 1})
				}
				return itemPageResponse("first", "item-next", true, 101, items), nil
			}
			if *query.Variables.ItemCursor != "item-next" {
				t.Fatal("incorrect item cursor")
			}
			return itemPageResponse("first", "", false, 101, []map[string]any{{"__typename": "Repository", "id": "repo-last", "nameWithOwner": "owner/last", "databaseId": 101}}), nil
		case "id-second":
			return itemPageResponse("second", "", false, 1, []map[string]any{{"__typename": "Repository", "id": "repo-second", "nameWithOwner": "other/skill", "databaseId": 102}}), nil
		default:
			t.Fatal("incorrect list ID")
			return nil, nil
		}
	})})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Lists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{fmt.Sprintf("%v", client), fmt.Sprintf("%+v", client), fmt.Sprintf("%#v", client), fmt.Sprintf("%+v", *client), fmt.Sprintf("%#v", *client)} {
		if strings.Contains(text, secret) {
			t.Fatal("lists client formatting leaked token")
		}
	}
	lists := snapshot.Lists
	if snapshot.ViewerID != "viewer-id" || len(lists) != 2 || lists[0].Slug != "first" || len(lists[0].Repositories) != 101 || lists[0].Repositories[100].NameWithOwner != "owner/last" || lists[1].Slug != "second" || len(lists[1].Repositories) != 1 || calls != 7 {
		t.Fatalf("unexpected list data; count = %d, calls = %d", len(lists), calls)
	}
}

func TestListsRejectsChangedMetadataAfterFetchingItems(t *testing.T) {
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var listsCalls int
	client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var query graphQuery
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			t.Fatal(err)
		}
		if query.Variables.ID != "" {
			return itemPageResponse("first", "", false, 0, []map[string]any{}), nil
		}
		listsCalls++
		if listsCalls == 1 {
			return listPageResponse("first", "", false), nil
		}
		return listPageResponse("changed", "", false), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Lists(t.Context())
	if err == nil || len(snapshot.Lists) != 0 || snapshot.ViewerID != "" {
		t.Fatalf("accepted changed list set: %+v, %v", snapshot, err)
	}
}

func listMetadata(slug string) map[string]any {
	return map[string]any{"id": "id-" + slug, "name": slug, "slug": slug, "isPrivate": false, "updatedAt": "2026-01-01T00:00:00Z", "lastAddedAt": "2026-01-01T00:00:00Z"}
}

func listPageResponse(slug, cursor string, more bool) *http.Response {
	return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer-id", "lists": map[string]any{"pageInfo": map[string]any{"endCursor": cursor, "hasNextPage": more}, "nodes": []any{listMetadata(slug)}}}}})
}

func itemPageResponse(slug, cursor string, more bool, total int, items []map[string]any) *http.Response {
	for _, item := range items {
		if _, exists := item["isPrivate"]; !exists {
			item["isPrivate"] = false
		}
	}
	node := listMetadata(slug)
	node["__typename"] = "UserList"
	node["user"] = map[string]any{"id": "viewer-id"}
	node["items"] = map[string]any{"totalCount": total, "pageInfo": map[string]any{"endCursor": cursor, "hasNextPage": more}, "nodes": items}
	return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"node": node}})
}

func TestListsRejectsPartialResultsAndRedactsErrors(t *testing.T) {
	secret := rand.Text()
	token, err := NewToken(secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"graphql-error", "cursor-cycle", "changed-list", "bad-repository", "null-privacy", "null-lists", "null-items", "null-last-added", "empty-list-page", "empty-item-page", "duplicate-database-id", "transport"} {
		t.Run(mode, func(t *testing.T) {
			var itemCalls int
			var listCalls int
			client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				var query graphQuery
				if err := json.UnmarshalRead(request.Body, &query); err != nil {
					t.Fatal(err)
				}
				if mode == "graphql-error" {
					return jsonResponse(http.StatusOK, map[string]any{"errors": []any{map[string]any{"message": secret}}}), nil
				}
				if mode == "transport" {
					return nil, errors.New(secret)
				}
				if query.Variables.ID == "" {
					listCalls++
					if mode == "null-last-added" {
						metadata := listMetadata("first")
						metadata["lastAddedAt"] = nil
						return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer-id", "lists": map[string]any{"pageInfo": map[string]any{"endCursor": nil, "hasNextPage": false}, "nodes": []any{metadata}}}}}), nil
					}
					if mode == "null-lists" {
						return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer-id", "lists": map[string]any{"pageInfo": map[string]any{"endCursor": nil, "hasNextPage": false}, "nodes": nil}}}}), nil
					}
					if mode == "empty-list-page" {
						if listCalls > 1 {
							t.Fatal("retried empty list page")
						}
						return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer-id", "lists": map[string]any{"pageInfo": map[string]any{"endCursor": "next", "hasNextPage": true}, "nodes": []any{}}}}}), nil
					}
					return listPageResponse("first", "end", false), nil
				}
				itemCalls++
				switch mode {
				case "null-privacy":
					return itemPageResponse("first", "", false, 1, []map[string]any{{"__typename": "Repository", "id": "repo", "nameWithOwner": "owner/repo", "databaseId": 1, "isPrivate": nil}}), nil
				case "empty-item-page":
					if itemCalls > 1 {
						t.Fatal("retried empty item page")
					}
					return itemPageResponse("first", "next", true, 1, []map[string]any{}), nil
				case "null-items":
					node := listMetadata("first")
					node["__typename"] = "UserList"
					node["user"] = map[string]any{"id": "viewer-id"}
					node["items"] = map[string]any{"totalCount": 0, "pageInfo": map[string]any{"endCursor": nil, "hasNextPage": false}, "nodes": nil}
					return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"node": node}}), nil
				case "duplicate-database-id":
					return itemPageResponse("first", "", false, 2, []map[string]any{{"__typename": "Repository", "id": "first", "nameWithOwner": "owner/first", "databaseId": 7}, {"__typename": "Repository", "id": "second", "nameWithOwner": "owner/second", "databaseId": 7}}), nil
				case "cursor-cycle":
					return itemPageResponse("first", "repeated", true, 2, []map[string]any{{"__typename": "Repository", "id": fmt.Sprint(itemCalls), "nameWithOwner": fmt.Sprintf("owner/repo-%d", itemCalls), "databaseId": itemCalls}}), nil
				case "changed-list":
					slug := "first"
					if itemCalls > 1 {
						slug = "other"
					}
					return itemPageResponse(slug, "item-next", true, 2, []map[string]any{{"__typename": "Repository", "id": fmt.Sprint(itemCalls), "nameWithOwner": fmt.Sprintf("owner/repo-%d", itemCalls), "databaseId": itemCalls}}), nil
				default:
					return itemPageResponse("first", "", false, 1, []map[string]any{{"__typename": "Issue", "id": "bad", "nameWithOwner": "bad", "databaseId": 1}}), nil
				}
			})})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := client.Lists(context.Background())
			if err == nil || len(snapshot.Lists) != 0 || snapshot.ViewerID != "" || strings.Contains(err.Error(), secret) {
				t.Fatalf("unexpected partial result or error: %v", err)
			}
		})
	}
}

func TestListsAcceptsRepositoryWithoutNumericDatabaseID(t *testing.T) {
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var query graphQuery
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			t.Fatal(err)
		}
		if query.Variables.ID == "" {
			return listPageResponse("first", "", false), nil
		}
		return itemPageResponse("first", "", false, 1, []map[string]any{{"__typename": "Repository", "id": "node-id", "nameWithOwner": "owner/repo", "databaseId": nil}}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Lists(t.Context())
	if err != nil || len(snapshot.Lists) != 1 || len(snapshot.Lists[0].Repositories) != 1 || snapshot.Lists[0].Repositories[0].DatabaseID != nil {
		t.Fatalf("nullable database ID: %+v, %v", snapshot, err)
	}
}

func TestListsSupportsEmptyViewerAndEmptyList(t *testing.T) {
	for _, emptyList := range []bool{false, true} {
		t.Run(fmt.Sprint(emptyList), func(t *testing.T) {
			token, err := NewToken(rand.Text(), nil)
			if err != nil {
				t.Fatal(err)
			}
			client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				var query graphQuery
				if err := json.UnmarshalRead(request.Body, &query); err != nil {
					t.Fatal(err)
				}
				if query.Variables.ID != "" {
					return itemPageResponse("first", "", false, 0, []map[string]any{}), nil
				}
				if emptyList {
					return listPageResponse("first", "", false), nil
				}
				return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer-id", "lists": map[string]any{"pageInfo": map[string]any{"endCursor": nil, "hasNextPage": false}, "nodes": []any{}}}}}), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := client.Lists(t.Context())
			if err != nil || snapshot.ViewerID != "viewer-id" || len(snapshot.Lists) != intFromBool(emptyList) {
				t.Fatalf("snapshot = %+v, error = %v", snapshot, err)
			}
		})
	}
}

func TestListsDoesNotSendExpiredTokenOrRetainRefreshCredential(t *testing.T) {
	access, refresh := rand.Text(), rand.Text()
	token, err := NewToken(access, nil)
	if err != nil {
		t.Fatal(err)
	}
	token.expiresAt = time.Now().Add(-time.Second)
	token.refreshGrant = &refreshGrantState{value: refresh}
	client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("sent an expired credential")
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if client.token.refreshGrant != nil {
		t.Fatal("lists client retained an unnecessary refresh credential")
	}
	if _, err := client.Lists(t.Context()); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected expired token error, got %v", err)
	}
}

func TestSnapshotCommitRejectsRotatedCredentialAndDifferentAccount(t *testing.T) {
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	token.clientID, token.refreshGrant = "client-id", &refreshGrantState{value: rand.Text()}
	listClient, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer-id", "lists": map[string]any{"pageInfo": map[string]any{"endCursor": nil, "hasNextPage": false}, "nodes": []any{}}}}}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := listClient.Lists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	otherClient, err := NewListsClient(other, nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	commit := func(context.Context, ListsSnapshot) error { called = true; return nil }
	if err := otherClient.CommitSnapshot(t.Context(), snapshot, commit); err == nil || called {
		t.Fatal("accepted snapshot for a different credential")
	}
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"access_token": rand.Text(), "refresh_token": rand.Text(), "token_type": "bearer", "expires_in": 100, "refresh_token_expires_in": 1000}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flow.Refresh(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if err := listClient.CommitSnapshot(t.Context(), snapshot, commit); !errors.Is(err, ErrTokenInvalidated) || called {
		t.Fatalf("accepted snapshot after refresh: %v", err)
	}
}

func TestSnapshotCommitSerializesRefresh(t *testing.T) {
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	token.clientID, token.refreshGrant = "client-id", &refreshGrantState{value: rand.Text()}
	client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer-id", "lists": map[string]any{"pageInfo": map[string]any{"endCursor": nil, "hasNextPage": false}, "nodes": []any{}}}}}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Lists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"access_token": rand.Text(), "refresh_token": rand.Text(), "token_type": "bearer", "expires_in": 100, "refresh_token_expires_in": 1000}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	started, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- client.CommitSnapshot(t.Context(), snapshot, func(context.Context, ListsSnapshot) error {
			close(started)
			<-finish
			return nil
		})
	}()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := flow.Refresh(ctx, token); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("refresh should wait for commit: %v", err)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := flow.Refresh(t.Context(), token); err != nil {
		t.Fatalf("refresh after commit: %v", err)
	}
}

func TestSnapshotCommitRejectsOlderConcurrentSync(t *testing.T) {
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer-id", "lists": map[string]any{"pageInfo": map[string]any{"endCursor": nil, "hasNextPage": false}, "nodes": []any{}}}}}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := client.Lists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	later, err := client.Lists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	commit := func(context.Context, ListsSnapshot) error { called = true; return nil }
	if err := client.CommitSnapshot(t.Context(), earlier, commit); !errors.Is(err, ErrSnapshotStale) || called {
		t.Fatalf("older snapshot was accepted: %v", err)
	}
	if err := client.CommitSnapshot(t.Context(), later, commit); err != nil || !called {
		t.Fatalf("latest snapshot was rejected: %v", err)
	}
}

func TestFailedNewSynchronizationStillSupersedesOlderSnapshot(t *testing.T) {
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls > 2 {
			return jsonResponse(http.StatusServiceUnavailable, nil), nil
		}
		return jsonResponse(http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "viewer-id", "lists": map[string]any{"pageInfo": map[string]any{"endCursor": nil, "hasNextPage": false}, "nodes": []any{}}}}}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := client.Lists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Lists(t.Context()); err == nil {
		t.Fatal("expected newer synchronization to fail")
	}
	called := false
	if err := client.CommitSnapshot(t.Context(), earlier, func(context.Context, ListsSnapshot) error { called = true; return nil }); !errors.Is(err, ErrSnapshotStale) || called {
		t.Fatalf("earlier snapshot accepted after failed newer sync: %v", err)
	}
}

func TestTokenExpiryBoundsGraphQLRequestAndSnapshotCommit(t *testing.T) {
	for _, operation := range []string{"request", "commit"} {
		t.Run(operation, func(t *testing.T) {
			token, err := NewToken(rand.Text(), nil)
			if err != nil {
				t.Fatal(err)
			}
			token.expiresAt = time.Now().Add(50 * time.Millisecond)
			client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			})})
			if err != nil {
				t.Fatal(err)
			}
			if operation == "request" {
				if _, err := client.Lists(t.Context()); !errors.Is(err, ErrTokenExpired) {
					t.Fatalf("expired request = %v", err)
				}
				return
			}
			generation, err := client.beginSnapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			snapshot := ListsSnapshot{ViewerID: "viewer-id", validity: token.validity, generation: generation}
			err = client.CommitSnapshot(t.Context(), snapshot, func(ctx context.Context, _ ListsSnapshot) error {
				<-ctx.Done()
				return ctx.Err()
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expired commit = %v", err)
			}
		})
	}
}

func TestListsCancellationTakesPriorityOverHTTPError(t *testing.T) {
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		cancel()
		return jsonResponse(http.StatusForbidden, nil), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Lists(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request = %v", err)
	}
}

func TestGateWaitStopsAtCredentialExpiry(t *testing.T) {
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := token.validity.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer token.validity.unlock()
	token.expiresAt = time.Now().Add(50 * time.Millisecond)
	client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("request sent after expiry")
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Lists(t.Context()); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired gate wait = %v", err)
	}
}

func intFromBool(value bool) int {
	if value {
		return 1
	}
	return 0
}
