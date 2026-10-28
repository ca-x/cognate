package search

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

var ErrRateLimited = errors.New("skills.sh search rate limit exceeded")

const maxResponseBytes = 1 << 20

type Result struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	SkillID  string `json:"skillId"`
	Name     string `json:"name"`
	Installs int64  `json:"installs"`
}

type Client struct{ http *http.Client }

func NewClient(transport http.RoundTripper) *Client {
	return &Client{http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (client *Client) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if len(query) > 512 || !utf8.ValidString(query) || len(utf16.Encode([]rune(query))) < 2 || strings.ContainsFunc(query, unicode.IsControl) || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("invalid search query or limit")
	}
	endpoint := url.URL{Scheme: "https", Host: "skills.sh", Path: "/api/search"}
	parameters := url.Values{"q": {query}, "limit": {strconv.Itoa(limit)}}
	endpoint.RawQuery = parameters.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create search request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("skills.sh search request failed")
	}
	defer response.Body.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skills.sh search returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("read skills.sh search response")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("skills.sh search response exceeds size limit")
	}
	var payload struct {
		Skills []struct {
			ID       string `json:"id"`
			Source   string `json:"source"`
			SkillID  string `json:"skillId"`
			Name     string `json:"name"`
			Installs *int64 `json:"installs"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Skills == nil || len(payload.Skills) > limit {
		return nil, fmt.Errorf("invalid skills.sh search response")
	}
	results := make([]Result, 0, len(payload.Skills))
	for _, result := range payload.Skills {
		if result.ID == "" || result.Source == "" || result.SkillID == "" || result.Name == "" || result.Installs == nil || *result.Installs < 0 {
			return nil, fmt.Errorf("invalid skills.sh search result")
		}
		results = append(results, Result{ID: result.ID, Source: result.Source, SkillID: result.SkillID, Name: result.Name, Installs: *result.Installs})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
