package search_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/search"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestSearchQueriesSkillsIndex(t *testing.T) {
	client := search.NewClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "skills.sh" || request.URL.Path != "/api/search" || request.URL.Query().Get("q") != "go & tools" || request.URL.Query().Get("limit") != "2" {
			t.Fatalf("unexpected request: %s", request.URL)
		}
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatal("unexpected credentials")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"query":"go & tools","skills":[{"id":"owner/repo/skill","source":"owner/repo","skillId":"skill","name":"Go skill","installs":42}],"count":1}`)), Request: request}, nil
	}))
	items, err := client.Search(t.Context(), "go & tools", 2)
	if err != nil || len(items) != 1 || items[0].Name != "Go skill" || items[0].Source != "owner/repo" || items[0].SkillID != "skill" || items[0].Installs != 42 {
		t.Fatalf("results = %+v, error = %v", items, err)
	}
}

func TestSearchTransportErrorsDoNotExposeQuery(t *testing.T) {
	client := search.NewClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("private-search-text sensitive upstream data")
	}))
	_, err := client.Search(t.Context(), "private-search-text", 1)
	if err == nil || strings.Contains(err.Error(), "private-search-text") || strings.Contains(err.Error(), "sensitive upstream data") {
		t.Fatalf("error = %v", err)
	}
}

func TestSearchRejectsInvalidAndUnboundedInputs(t *testing.T) {
	client := search.NewClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatal("invalid input made network request")
		return nil, nil
	}))
	for _, input := range []struct {
		query string
		limit int
	}{{"", 1}, {" ", 1}, {"x", 1}, {strings.Repeat("x", 513), 1}, {"go", 0}, {"go", 101}} {
		if _, err := client.Search(t.Context(), input.query, input.limit); err == nil {
			t.Fatalf("accepted input: %+v", input)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Search(ctx, "go", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search = %v", err)
	}
}

func TestSearchRejectsBadResponsesAndRedactsErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"http-error", http.StatusInternalServerError, "sensitive upstream data"},
		{"missing-skills", http.StatusOK, `{}`},
		{"invalid-json", http.StatusOK, `{`},
		{"oversized", http.StatusOK, strings.Repeat("x", 2<<20)},
		{"invalid-result", http.StatusOK, `{"skills":[{"name":"x","source":"x","skillId":"x","installs":-1}]}`},
		{"missing-count", http.StatusOK, `{"skills":[{"id":"x/y/z","name":"x","source":"x/y","skillId":"z"}]}`},
		{"null-count", http.StatusOK, `{"skills":[{"id":"x/y/z","name":"x","source":"x/y","skillId":"z","installs":null}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := search.NewClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body)), Request: request}, nil
			}))
			_, err := client.Search(t.Context(), "private-search-text", 2)
			if err == nil || strings.Contains(err.Error(), "private-search-text") || strings.Contains(err.Error(), "sensitive upstream data") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSearchAcceptsSingleNonBMPCharacter(t *testing.T) {
	client := search.NewClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"skills":[]}`)), Request: request}, nil
	}))
	if _, err := client.Search(t.Context(), "🚀", 1); err != nil {
		t.Fatalf("non-BMP search: %v", err)
	}
}

func TestSearchAcceptsZeroInstalls(t *testing.T) {
	client := search.NewClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"skills":[{"id":"x/y/z","source":"x/y","skillId":"z","name":"z","installs":0}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	results, err := client.Search(t.Context(), "go", 1)
	if err != nil || len(results) != 1 || results[0].Installs != 0 {
		t.Fatalf("zero installations = %+v, %v", results, err)
	}
}

func TestSearchCanceledBeforeResponseIsReturned(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	client := search.NewClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"skills":[]}`)), Request: request}, nil
	}))
	if _, err := client.Search(ctx, "go", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled response = %v", err)
	}
}

func TestSearchRateLimitAndRedirect(t *testing.T) {
	requests := 0
	client := search.NewClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		header := make(http.Header)
		header.Set("Location", "https://other.invalid/")
		status := http.StatusFound
		if requests == 1 {
			status = http.StatusTooManyRequests
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	}))
	if _, err := client.Search(t.Context(), "go", 1); !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("rate limit = %v", err)
	}
	if _, err := client.Search(t.Context(), "go", 1); err == nil {
		t.Fatal("accepted redirect")
	}
	if requests != 2 {
		t.Fatalf("redirect made extra request: %d", requests)
	}
}
