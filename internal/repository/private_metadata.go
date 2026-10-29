package repository

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"

	"github.com/ca-x/cognate/internal/github"
)

const maxRepositoryMetadataBytes = 64 << 10

type GitHubMetadata struct {
	NodeID        string
	NameWithOwner string
	Private       bool
}

func (fetcher *ArchiveFetcher) FetchMetadata(ctx context.Context, reference Reference, token github.Token, check func(context.Context) error) (GitHubMetadata, error) {
	parsed, err := ParseReference(reference.NameWithOwner, reference.Branch)
	if err != nil || parsed != reference {
		return GitHubMetadata{}, ErrInvalidReference
	}
	ctx, cancel := context.WithTimeout(ctx, fetcher.client.Timeout)
	defer cancel()
	if expires, exists := token.ExpiresAt(); exists {
		var expire context.CancelFunc
		ctx, expire = context.WithDeadline(ctx, expires)
		defer expire()
	}
	if err := fetcher.cooldown.admit(ctx); err != nil {
		return GitHubMetadata{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+reference.NameWithOwner, nil)
	if err != nil {
		return GitHubMetadata{}, ErrInvalidReference
	}
	request.Header.Set("Authorization", "Bearer "+token.Reveal())
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "Cognate")
	if err := privateArchiveAuthorization(ctx, token, check); err != nil {
		return GitHubMetadata{}, err
	}
	if err := fetcher.cooldown.admit(ctx); err != nil {
		return GitHubMetadata{}, err
	}
	response, err := fetcher.client.Do(request)
	if err != nil {
		return GitHubMetadata{}, privateMetadataFailure(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusForbidden && len(response.Header.Values("Retry-After")) != 0 {
		fetcher.cooldown.observe(response.Header.Get("Retry-After"))
		return GitHubMetadata{}, ErrArchiveRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return GitHubMetadata{}, ErrArchiveUnavailable
	}
	if response.ContentLength > maxRepositoryMetadataBytes {
		return GitHubMetadata{}, ErrArchiveTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRepositoryMetadataBytes+1))
	defer clear(data)
	if err != nil {
		return GitHubMetadata{}, privateMetadataFailure(ctx)
	}
	if err := privateArchiveAuthorization(ctx, token, check); err != nil {
		return GitHubMetadata{}, err
	}
	if len(data) > maxRepositoryMetadataBytes {
		return GitHubMetadata{}, ErrArchiveTooLarge
	}
	metadata, err := decodeGitHubMetadata(data, reference)
	if err != nil {
		return GitHubMetadata{}, privateMetadataFailure(ctx)
	}
	if err := privateArchiveAuthorization(ctx, token, check); err != nil {
		return GitHubMetadata{}, err
	}
	return metadata, nil
}

func decodeGitHubMetadata(data []byte, reference Reference) (GitHubMetadata, error) {
	var document struct {
		NodeID   string `json:"node_id"`
		FullName string `json:"full_name"`
		Private  *bool  `json:"private"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return GitHubMetadata{}, ErrArchiveUnavailable
	}
	identity, err := ParseReference(document.FullName, reference.Branch)
	if err != nil || identity != reference || strings.ToLower(document.FullName) != reference.NameWithOwner || document.Private == nil || len(document.NodeID) == 0 || len(document.NodeID) > 256 || strings.ContainsFunc(document.NodeID, func(character rune) bool { return character < 0x21 || character > 0x7e }) {
		return GitHubMetadata{}, ErrArchiveUnavailable
	}
	return GitHubMetadata{NodeID: strings.Clone(document.NodeID), NameWithOwner: strings.Clone(identity.NameWithOwner), Private: *document.Private}, nil
}

func privateMetadataFailure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrArchiveUnavailable
}
