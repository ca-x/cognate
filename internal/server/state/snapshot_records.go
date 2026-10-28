package state

import (
	"context"
	"fmt"

	"github.com/ca-x/skillhub/internal/github"
	"github.com/ca-x/skillhub/internal/server/ent"
	"github.com/ca-x/skillhub/internal/server/ent/githublist"
	"github.com/ca-x/skillhub/internal/server/ent/githubrepository"
)

func snapshotRecords(ctx context.Context, tx *ent.Tx, ticket ListSyncTicket, snapshot github.ListsSnapshot) (map[string]*ent.GitHubList, map[string]*ent.GitHubRepository, error) {
	listIDs := make([]string, 0, len(snapshot.Lists))
	repositoryIDs := make([]string, 0)
	for _, list := range snapshot.Lists {
		listIDs = append(listIDs, list.ID)
		for _, repository := range list.Repositories {
			repositoryIDs = append(repositoryIDs, repository.ID)
		}
	}
	lists := make(map[string]*ent.GitHubList, len(listIDs))
	for start := 0; start < len(listIDs); start += 500 {
		end := min(start+500, len(listIDs))
		entries, err := tx.GitHubList.Query().Where(githublist.UserIDEQ(ticket.userID), githublist.ViewerIDEQ(ticket.viewerID), githublist.ListKeyIn(listIDs[start:end]...)).All(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("read observed GitHub lists: %w", err)
		}
		for _, entry := range entries {
			lists[entry.ListKey] = entry
		}
	}
	repositories := make(map[string]*ent.GitHubRepository, len(repositoryIDs))
	for start := 0; start < len(repositoryIDs); start += 500 {
		end := min(start+500, len(repositoryIDs))
		entries, err := tx.GitHubRepository.Query().Where(githubrepository.UserIDEQ(ticket.userID), githubrepository.ViewerIDEQ(ticket.viewerID), githubrepository.RepositoryKeyIn(repositoryIDs[start:end]...)).All(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("read observed GitHub repositories: %w", err)
		}
		for _, entry := range entries {
			repositories[entry.RepositoryKey] = entry
		}
	}
	return lists, repositories, nil
}
