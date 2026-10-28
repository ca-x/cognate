package state

import (
	"context"
	"fmt"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/githublist"
)

func (store *Store) listWatermarks(ctx context.Context, ticket ListSyncTicket) (map[string]github.List, error) {
	entries, err := store.state.client.GitHubList.Query().Where(githublist.UserIDEQ(ticket.userID), githublist.ViewerIDEQ(ticket.viewerID), githublist.SubscribedEQ(true)).Limit(1025).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read selected GitHub list watermarks: %w", err)
	}
	if len(entries) > 1024 {
		return nil, fmt.Errorf("selected GitHub list limit exceeded")
	}
	selected := make(map[string]github.List, len(entries))
	for _, entry := range entries {
		selected[entry.ListKey] = github.List{}
		if store.reusableList(entry, ticket) {
			selected[entry.ListKey] = listWatermark(entry)
		}
	}
	return selected, nil
}

func (store *Store) reusableList(entry *ent.GitHubList, ticket ListSyncTicket) bool {
	if !entry.Subscribed || entry.CredentialGeneration != ticket.generation || entry.ItemsVerifiedAt == nil {
		return false
	}
	now := store.state.now()
	return !entry.ItemsVerifiedAt.After(now) && now.Before(entry.ItemsVerifiedAt.Add(store.state.observationMaxAge))
}

func listWatermark(entry *ent.GitHubList) github.List {
	return github.List{ID: entry.ListKey, Name: entry.Name, Slug: entry.Slug, Private: entry.Private, UpdatedAt: entry.UpdatedAt, LastAddedAt: entry.LastAddedAt}
}

func sameListWatermark(first, second github.List) bool {
	return first.ID == second.ID && first.Name == second.Name && first.Slug == second.Slug && first.Private == second.Private && first.UpdatedAt.Equal(second.UpdatedAt) && first.LastAddedAt.Equal(second.LastAddedAt)
}
