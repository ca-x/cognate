package repository

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/github"
)

var ErrPrivateArchiveUnauthorized = errors.New("private GitHub archive authorization unavailable")

func (fetcher *ArchiveFetcher) FetchPrivate(ctx context.Context, reference Reference, token github.Token, check func(context.Context) error) (*ArchiveSnapshot, error) {
	parsed, err := ParseReference(reference.NameWithOwner, reference.Branch)
	if err != nil || parsed != reference {
		return nil, ErrInvalidReference
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if expires, exists := token.ExpiresAt(); exists {
		var expire context.CancelFunc
		ctx, expire = context.WithDeadline(ctx, expires)
		defer expire()
	}
	endpoint := "https://api.github.com/repos/" + reference.NameWithOwner + "/tarball"
	if reference.Branch != "" {
		endpoint += "/" + url.PathEscape(reference.Branch)
	}
	for hop := range 2 {
		if err := fetcher.cooldown.admit(ctx); err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, ErrArchiveUnavailable
		}
		request.Header.Set("Accept-Encoding", "identity")
		request.Header.Set("User-Agent", "Cognate")
		if hop == 0 {
			request.Header.Set("Accept", "application/vnd.github+json")
			request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
			request.Header.Set("Authorization", "Bearer "+token.Reveal())
		}
		if err := privateArchiveAuthorization(ctx, token, check); err != nil {
			return nil, err
		}
		if err := fetcher.cooldown.admit(ctx); err != nil {
			return nil, err
		}
		response, err := fetcher.client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrArchiveUnavailable
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusForbidden && len(response.Header.Values("Retry-After")) != 0 {
			fetcher.cooldown.observe(response.Header.Get("Retry-After"))
			_ = response.Body.Close()
			return nil, ErrArchiveRateLimited
		}
		if hop == 0 {
			locations := response.Header.Values("Location")
			_ = response.Body.Close()
			if response.StatusCode != http.StatusFound || len(locations) != 1 || !privateArchiveLocation(locations[0]) {
				return nil, ErrArchiveUnavailable
			}
			endpoint = locations[0]
			continue
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, ErrArchiveUnavailable
		}
		if response.ContentLength > MaxArchiveBytes {
			return nil, ErrArchiveTooLarge
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, MaxArchiveBytes+1))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrArchiveUnavailable
		}
		if err := privateArchiveAuthorization(ctx, token, check); err != nil {
			return nil, err
		}
		if len(data) > MaxArchiveBytes {
			return nil, ErrArchiveTooLarge
		}
		snapshot, err := inspectArchive(ctx, data)
		if err != nil {
			return nil, err
		}
		if err := privateArchiveAuthorization(ctx, token, check); err != nil {
			return nil, err
		}
		return snapshot, nil
	}
	return nil, ErrArchiveUnavailable
}

func privateArchiveAuthorization(ctx context.Context, token github.Token, check func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if check == nil || token.Reveal() == "" {
		return ErrPrivateArchiveUnauthorized
	}
	if err := check(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrPrivateArchiveUnauthorized
	}
	return ctx.Err()
}

func privateArchiveLocation(location string) bool {
	if location == "" || len(location) > 8<<10 || strings.Contains(location, "#") || strings.ContainsFunc(location, func(character rune) bool { return character < 0x21 || character > 0x7e }) {
		return false
	}
	parsed, err := url.Parse(location)
	return err == nil && parsed.Scheme == "https" && parsed.Opaque == "" && parsed.User == nil && (parsed.Host == "codeload.github.com" || parsed.Host == "codeload.github.com:443") && parsed.Fragment == ""
}
