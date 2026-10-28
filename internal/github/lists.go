package github

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json/v2"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	graphQLURL        = "https://api.github.com/graphql"
	maxGraphQLBody    = 2 << 20
	maxLists          = 1024
	maxRepositories   = 100000
	listMetadataQuery = `query CognateLists($listCursor: String) {
  viewer { id lists(first: 100, after: $listCursor) {
    pageInfo { hasNextPage endCursor }
    nodes { id name slug isPrivate updatedAt lastAddedAt }
  } }
}`
	listItemsQuery = `query CognateListItems($id: ID!, $itemCursor: String) {
  node(id: $id) { __typename ... on UserList {
    id name slug isPrivate updatedAt lastAddedAt user { id }
    items(first: 100, after: $itemCursor) {
      totalCount pageInfo { hasNextPage endCursor }
      nodes { __typename ... on Repository { id databaseId nameWithOwner isPrivate } }
    }
  } }
}`
)

type ListsClient struct {
	client *http.Client
	token  Token
}

func (ListsClient) String() string { return "[redacted GitHub lists client]" }

func (ListsClient) GoString() string { return "[redacted GitHub lists client]" }

func (client *ListsClient) MatchesToken(token Token) bool {
	return client != nil && subtle.ConstantTimeCompare([]byte(client.token.value), []byte(token.value)) == 1 && client.token.clientID == token.clientID && client.token.expiresAt.Equal(token.expiresAt) && slices.Equal(client.token.scopes, token.scopes)
}

type ListsSnapshot struct {
	ViewerID   string
	Lists      []List
	validity   *tokenValidity
	generation uint64
	observedAt time.Time
}

func (snapshot ListsSnapshot) ObservedAt() time.Time { return snapshot.observedAt }

type List struct {
	ID             string
	Name           string
	Slug           string
	Private        bool
	UpdatedAt      time.Time
	LastAddedAt    time.Time
	Repositories   []ListRepository
	ItemsUnchanged bool
}

type ListRepository struct {
	ID            string
	DatabaseID    *int64
	NameWithOwner string
	Private       bool
}

func NewListsClient(token Token, client *http.Client) (*ListsClient, error) {
	if _, err := NewToken(token.value, nil); err != nil {
		return nil, err
	}
	if token.validity == nil {
		return nil, fmt.Errorf("GitHub access token state is unavailable")
	}
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copy.Timeout == 0 {
		copy.Timeout = 30 * time.Second
	}
	token.refreshGrant = nil
	token.refreshExpiresAt = time.Time{}
	return &ListsClient{client: &copy, token: token}, nil
}

type pageInfo struct {
	HasNextPage *bool   `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

type listNode struct {
	ID          *string    `json:"id"`
	Name        *string    `json:"name"`
	Slug        *string    `json:"slug"`
	Private     *bool      `json:"isPrivate"`
	UpdatedAt   *time.Time `json:"updatedAt"`
	LastAddedAt *time.Time `json:"lastAddedAt"`
}

type listPage struct {
	Data *struct {
		Viewer *struct {
			ID    *string `json:"id"`
			Lists *struct {
				PageInfo *pageInfo   `json:"pageInfo"`
				Nodes    []*listNode `json:"nodes"`
			} `json:"lists"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []any `json:"errors"`
}

type itemPage struct {
	Data *struct {
		Node *struct {
			Type string `json:"__typename"`
			listNode
			User *struct {
				ID *string `json:"id"`
			} `json:"user"`
			Items *struct {
				TotalCount *int      `json:"totalCount"`
				PageInfo   *pageInfo `json:"pageInfo"`
				Nodes      []*struct {
					Type          string  `json:"__typename"`
					ID            *string `json:"id"`
					DatabaseID    *int64  `json:"databaseId"`
					NameWithOwner *string `json:"nameWithOwner"`
					Private       *bool   `json:"isPrivate"`
				} `json:"nodes"`
			} `json:"items"`
		} `json:"node"`
	} `json:"data"`
	Errors []any `json:"errors"`
}

func (client *ListsClient) Lists(ctx context.Context) (ListsSnapshot, error) {
	return client.lists(ctx, nil, true)
}

func (client *ListsClient) ListsSelected(ctx context.Context, selected map[string]List) (ListsSnapshot, error) {
	return client.lists(ctx, selected, false)
}

func (client *ListsClient) lists(ctx context.Context, selected map[string]List, all bool) (ListsSnapshot, error) {
	observedAt := time.Now().UTC()
	generation, err := client.beginSnapshot(ctx)
	if err != nil {
		return ListsSnapshot{}, err
	}
	snapshot, err := client.listMetadata(ctx)
	if err != nil {
		return ListsSnapshot{}, err
	}
	remaining := maxRepositories
	for index := range snapshot.Lists {
		previous, enabled := selected[snapshot.Lists[index].ID]
		if !all && !enabled {
			continue
		}
		if !all && sameList(previous, snapshot.Lists[index]) {
			snapshot.Lists[index].ItemsUnchanged = true
			continue
		}
		items, err := client.listItems(ctx, snapshot.ViewerID, snapshot.Lists[index], remaining)
		if err != nil {
			return ListsSnapshot{}, err
		}
		snapshot.Lists[index].Repositories = items
		remaining -= len(items)
	}
	current, err := client.listMetadata(ctx)
	if err != nil {
		return ListsSnapshot{}, err
	}
	if current.ViewerID != snapshot.ViewerID || len(current.Lists) != len(snapshot.Lists) {
		return ListsSnapshot{}, fmt.Errorf("GitHub list set changed during synchronization")
	}
	byID := make(map[string]List, len(current.Lists))
	for _, item := range current.Lists {
		byID[item.ID] = item
	}
	for _, item := range snapshot.Lists {
		other, exists := byID[item.ID]
		if !exists || !sameList(item, other) {
			return ListsSnapshot{}, fmt.Errorf("GitHub list set changed during synchronization")
		}
	}
	snapshot.validity = client.token.validity
	snapshot.generation = generation
	snapshot.observedAt = observedAt
	return snapshot, nil
}

func (client *ListsClient) beginSnapshot(ctx context.Context) (uint64, error) {
	validity := client.token.validity
	if err := validity.lockUntil(ctx, client.token.expiresAt, time.Now, ErrTokenExpired); err != nil {
		return 0, err
	}
	defer validity.unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if validity.invalidated.Load() {
		return 0, ErrTokenInvalidated
	}
	if !client.token.expiresAt.IsZero() && !time.Now().Before(client.token.expiresAt) {
		return 0, ErrTokenExpired
	}
	if validity.generation == math.MaxUint64 {
		return 0, fmt.Errorf("GitHub synchronization generation exhausted")
	}
	validity.generation++
	return validity.generation, nil
}

func (client *ListsClient) CommitSnapshot(ctx context.Context, snapshot ListsSnapshot, commit func(context.Context, ListsSnapshot) error) error {
	if snapshot.validity == nil || snapshot.validity != client.token.validity || snapshot.ViewerID == "" || commit == nil {
		return fmt.Errorf("GitHub list snapshot does not belong to this credential")
	}
	if err := snapshot.validity.lockUntil(ctx, client.token.expiresAt, time.Now, ErrTokenExpired); err != nil {
		return err
	}
	defer snapshot.validity.unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot.validity.invalidated.Load() {
		return ErrTokenInvalidated
	}
	if snapshot.generation == 0 || snapshot.generation != snapshot.validity.generation {
		return ErrSnapshotStale
	}
	if !client.token.expiresAt.IsZero() && !time.Now().Before(client.token.expiresAt) {
		return ErrTokenExpired
	}
	commitContext := ctx
	if !client.token.expiresAt.IsZero() {
		var cancel context.CancelFunc
		commitContext, cancel = context.WithDeadline(ctx, client.token.expiresAt)
		defer cancel()
	}
	return commit(commitContext, snapshot)
}

func (client *ListsClient) listMetadata(ctx context.Context) (ListsSnapshot, error) {
	var snapshot ListsSnapshot
	var cursor *string
	seenCursors, seenIDs, seenSlugs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for {
		var response listPage
		if err := client.query(ctx, listMetadataQuery, map[string]any{"listCursor": cursor}, &response); err != nil {
			return ListsSnapshot{}, err
		}
		if len(response.Errors) != 0 || response.Data == nil || response.Data.Viewer == nil || response.Data.Viewer.ID == nil || !validOpaqueID(*response.Data.Viewer.ID) || response.Data.Viewer.Lists == nil || response.Data.Viewer.Lists.Nodes == nil {
			return ListsSnapshot{}, fmt.Errorf("GitHub lists response is incomplete")
		}
		viewer := response.Data.Viewer
		if len(viewer.Lists.Nodes) > 100 || len(viewer.Lists.Nodes) == 0 && viewer.Lists.PageInfo != nil && viewer.Lists.PageInfo.HasNextPage != nil && *viewer.Lists.PageInfo.HasNextPage {
			return ListsSnapshot{}, fmt.Errorf("GitHub list page is invalid")
		}
		if snapshot.ViewerID != "" && snapshot.ViewerID != *viewer.ID {
			return ListsSnapshot{}, fmt.Errorf("GitHub viewer changed during pagination")
		}
		snapshot.ViewerID = *viewer.ID
		for _, node := range viewer.Lists.Nodes {
			item, err := parseListNode(node)
			if err != nil || seenIDs[item.ID] || seenSlugs[item.Slug] {
				return ListsSnapshot{}, fmt.Errorf("GitHub returned an invalid or duplicate list")
			}
			seenIDs[item.ID], seenSlugs[item.Slug] = true, true
			snapshot.Lists = append(snapshot.Lists, item)
			if len(snapshot.Lists) > maxLists {
				return ListsSnapshot{}, fmt.Errorf("GitHub list limit exceeded")
			}
		}
		next, more, err := nextCursor(viewer.Lists.PageInfo, seenCursors)
		if err != nil {
			return ListsSnapshot{}, err
		}
		if !more {
			break
		}
		cursor = next
	}
	return snapshot, nil
}

func (client *ListsClient) listItems(ctx context.Context, viewer string, expected List, limit int) ([]ListRepository, error) {
	var items []ListRepository
	var cursor *string
	seenCursors, seenIDs, seenNames, seenDatabaseIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[int64]bool{}
	var total = -1
	for {
		var response itemPage
		if err := client.query(ctx, listItemsQuery, map[string]any{"id": expected.ID, "itemCursor": cursor}, &response); err != nil {
			return nil, err
		}
		if len(response.Errors) != 0 || response.Data == nil || response.Data.Node == nil || response.Data.Node.Type != "UserList" || response.Data.Node.User == nil || response.Data.Node.User.ID == nil || *response.Data.Node.User.ID != viewer || response.Data.Node.Items == nil || response.Data.Node.Items.Nodes == nil {
			return nil, fmt.Errorf("GitHub list is unavailable or incomplete")
		}
		node := response.Data.Node
		if len(node.Items.Nodes) > 100 || len(node.Items.Nodes) == 0 && node.Items.PageInfo != nil && node.Items.PageInfo.HasNextPage != nil && *node.Items.PageInfo.HasNextPage {
			return nil, fmt.Errorf("GitHub item page is invalid")
		}
		observed, err := parseListNode(&node.listNode)
		if err != nil || !sameList(expected, observed) {
			return nil, fmt.Errorf("GitHub list changed during pagination")
		}
		if node.Items.TotalCount == nil || *node.Items.TotalCount < 0 || *node.Items.TotalCount > limit || total >= 0 && total != *node.Items.TotalCount {
			return nil, fmt.Errorf("GitHub list item count is invalid or changed")
		}
		total = *node.Items.TotalCount
		for _, repository := range node.Items.Nodes {
			if repository == nil || repository.Type != "Repository" || repository.ID == nil || !validOpaqueID(*repository.ID) || repository.DatabaseID != nil && (*repository.DatabaseID <= 0 || seenDatabaseIDs[*repository.DatabaseID]) || repository.NameWithOwner == nil || !validRepositoryName(*repository.NameWithOwner) || repository.Private == nil || seenIDs[*repository.ID] || seenNames[strings.ToLower(*repository.NameWithOwner)] {
				return nil, fmt.Errorf("GitHub returned an invalid or duplicate repository")
			}
			seenIDs[*repository.ID], seenNames[strings.ToLower(*repository.NameWithOwner)] = true, true
			if repository.DatabaseID != nil {
				seenDatabaseIDs[*repository.DatabaseID] = true
			}
			items = append(items, ListRepository{ID: *repository.ID, DatabaseID: repository.DatabaseID, NameWithOwner: *repository.NameWithOwner, Private: *repository.Private})
			if len(items) > total {
				return nil, fmt.Errorf("GitHub list item count changed")
			}
		}
		next, more, err := nextCursor(node.Items.PageInfo, seenCursors)
		if err != nil {
			return nil, err
		}
		if !more {
			if len(items) != total {
				return nil, fmt.Errorf("GitHub list item count changed")
			}
			return items, nil
		}
		cursor = next
	}
}

func parseListNode(node *listNode) (List, error) {
	if node == nil || node.ID == nil || !validOpaqueID(*node.ID) || node.Name == nil || node.Slug == nil || *node.Slug == "" || node.Private == nil || node.UpdatedAt == nil || node.UpdatedAt.IsZero() || node.LastAddedAt == nil || node.LastAddedAt.IsZero() {
		return List{}, fmt.Errorf("invalid list metadata")
	}
	return List{ID: *node.ID, Name: *node.Name, Slug: *node.Slug, Private: *node.Private, UpdatedAt: *node.UpdatedAt, LastAddedAt: *node.LastAddedAt}, nil
}

func sameList(first, second List) bool {
	if first.ID != second.ID || first.Name != second.Name || first.Slug != second.Slug || first.Private != second.Private || !first.UpdatedAt.Equal(second.UpdatedAt) {
		return false
	}
	return first.LastAddedAt.Equal(second.LastAddedAt)
}

func validOpaqueID(value string) bool {
	return value != "" && len(value) <= 1024 && !strings.ContainsAny(value, "\r\n\x00")
}

func validRepositoryName(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 255 || part == "." || part == ".." {
			return false
		}
		for _, character := range part {
			if character != '-' && character != '_' && character != '.' && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

func nextCursor(page *pageInfo, seen map[string]bool) (*string, bool, error) {
	if page == nil || page.HasNextPage == nil {
		return nil, false, fmt.Errorf("GitHub pagination metadata is incomplete")
	}
	if !*page.HasNextPage {
		return nil, false, nil
	}
	if page.EndCursor == nil || !validOpaqueID(*page.EndCursor) || seen[*page.EndCursor] {
		return nil, false, fmt.Errorf("GitHub pagination cursor is invalid or repeated")
	}
	seen[*page.EndCursor] = true
	return page.EndCursor, true, nil
}

func (client *ListsClient) query(ctx context.Context, query string, variables map[string]any, destination any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := client.token.validity.lockUntil(ctx, client.token.expiresAt, time.Now, ErrTokenExpired); err != nil {
		return err
	}
	defer client.token.validity.unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !client.token.expiresAt.IsZero() && !time.Now().Before(client.token.expiresAt) {
		return ErrTokenExpired
	}
	if client.token.validity != nil && client.token.validity.invalidated.Load() {
		return ErrTokenInvalidated
	}
	payload, err := json.Marshal(struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}{Query: query, Variables: variables})
	if err != nil {
		return fmt.Errorf("encode GitHub GraphQL request")
	}
	requestContext := ctx
	if !client.token.expiresAt.IsZero() {
		var cancel context.CancelFunc
		requestContext, cancel = context.WithDeadline(ctx, client.token.expiresAt)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, graphQLURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create GitHub GraphQL request")
	}
	request.Header.Set("Authorization", "Bearer "+client.token.Reveal())
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Cognate")
	response, err := client.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if requestContext.Err() != nil && !client.token.expiresAt.IsZero() && !time.Now().Before(client.token.expiresAt) {
			return ErrTokenExpired
		}
		return fmt.Errorf("GitHub GraphQL request failed")
	}
	defer response.Body.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if requestContext.Err() != nil && !client.token.expiresAt.IsZero() && !time.Now().Before(client.token.expiresAt) {
		return ErrTokenExpired
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub GraphQL returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxGraphQLBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if requestContext.Err() != nil && !client.token.expiresAt.IsZero() && !time.Now().Before(client.token.expiresAt) {
			return ErrTokenExpired
		}
		return fmt.Errorf("read GitHub GraphQL response")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !client.token.expiresAt.IsZero() && !time.Now().Before(client.token.expiresAt) {
		return ErrTokenExpired
	}
	if len(body) > maxGraphQLBody || json.Unmarshal(body, destination) != nil {
		return fmt.Errorf("invalid GitHub GraphQL response")
	}
	return nil
}
