package repository

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/cognate/internal/github"
)

func TestPrivateMetadataRealHTTP(test *testing.T) {
	for _, private := range []bool{false, true} {
		test.Run(fmt.Sprintf("private=%t", private), func(test *testing.T) {
			token := privateToken(test)
			var requests atomic.Int64
			fetcher := privateHTTPFetcher(test, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Method != http.MethodGet || request.Host != "api.github.com" || request.URL.RequestURI() != "/repos/acme/tools" {
					test.Error("metadata request escaped the fixed repository endpoint")
				}
				if request.Header.Get("Authorization") != "Bearer "+token.Reveal() || request.Header.Get("Accept-Encoding") != "identity" || request.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
					test.Error("metadata request omitted its authorized API headers")
				}
				if request.Header.Get("Cookie") != "" || request.Header.Get("Referer") != "" || request.Header.Get("Proxy-Authorization") != "" {
					test.Error("metadata inherited sensitive client state")
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(writer, `{"node_id":"R_synthetic","full_name":"Acme/Tools","private":%t,"temp_clone_token":"synthetic-response-secret"}`, private)
			}), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				test.Error("metadata discovery downloaded an archive")
			}))
			metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools", Branch: "feature/nested"}, token, func(context.Context) error { return nil })
			if err != nil || metadata.NameWithOwner != "acme/tools" || metadata.NodeID != "R_synthetic" || metadata.Private != private || requests.Load() != 1 {
				test.Fatal("authorized metadata did not preserve canonical repository identity and explicit visibility")
			}
		})
	}
}

func TestPrivateMetadataRejectsAmbiguousIdentity(test *testing.T) {
	for name, body := range map[string]string{
		"missing-private": `{"node_id":"R_synthetic","full_name":"acme/tools"}`,
		"null-private":    `{"node_id":"R_synthetic","full_name":"acme/tools","private":null}`,
		"wrong-repo":      `{"node_id":"R_synthetic","full_name":"other/tools","private":true}`,
		"url-alias":       `{"node_id":"R_synthetic","full_name":"https://github.com/acme/tools","private":true}`,
		"git-alias":       `{"node_id":"R_synthetic","full_name":"acme/tools.git","private":true}`,
		"trailing-slash":  `{"node_id":"R_synthetic","full_name":"acme/tools/","private":true}`,
		"trimmed-alias":   `{"node_id":"R_synthetic","full_name":" acme/tools ","private":true}`,
		"empty-node":      `{"node_id":"","full_name":"acme/tools","private":true}`,
		"control-node":    `{"node_id":"R_\nsynthetic","full_name":"acme/tools","private":true}`,
		"nonascii-node":   `{"node_id":"R_技能","full_name":"acme/tools","private":true}`,
		"duplicate":       `{"node_id":"R_synthetic","full_name":"acme/tools","private":false,"private":true}`,
		"trailing":        `{"node_id":"R_synthetic","full_name":"acme/tools","private":true}{}`,
		"oversize-node":   `{"node_id":"` + strings.Repeat("R", 257) + `","full_name":"acme/tools","private":true}`,
	} {
		test.Run(name, func(test *testing.T) {
			fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})})
			metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, privateToken(test), func(context.Context) error { return nil })
			if !errors.Is(err, ErrArchiveUnavailable) || metadata != (GitHubMetadata{}) {
				test.Fatal("ambiguous or cross-repository metadata was accepted")
			}
			privateErrorIsRedacted(test, err)
		})
	}
}

func TestPrivateMetadataAuthorizationCheckpoints(test *testing.T) {
	for _, deniedAt := range []int64{1, 2, 3} {
		test.Run(fmt.Sprintf("check=%d", deniedAt), func(test *testing.T) {
			var requests, checks atomic.Int64
			fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
				requests.Add(1)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"node_id":"R_synthetic","full_name":"acme/tools","private":true}`))}, nil
			})})
			metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, privateToken(test), func(context.Context) error {
				if checks.Add(1) == deniedAt {
					return errors.New("synthetic-check-secret")
				}
				return nil
			})
			if !errors.Is(err, ErrPrivateArchiveUnauthorized) || metadata != (GitHubMetadata{}) || deniedAt == 1 && requests.Load() != 0 {
				test.Fatal("metadata escaped an authorization checkpoint")
			}
			privateErrorIsRedacted(test, err)
		})
	}
}

func TestPrivateMetadataRequiresAuthorityBeforeParsing(test *testing.T) {
	var requests atomic.Int64
	fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("synthetic-response-secret"))}, nil
	})})
	if metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, privateToken(test), nil); !errors.Is(err, ErrPrivateArchiveUnauthorized) || metadata != (GitHubMetadata{}) || requests.Load() != 0 {
		test.Fatal("nil metadata authority was accepted")
	}
	if metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, github.Token{}, func(context.Context) error { return nil }); !errors.Is(err, ErrPrivateArchiveUnauthorized) || metadata != (GitHubMetadata{}) || requests.Load() != 0 {
		test.Fatal("empty metadata credential was sent")
	}
	checks := 0
	metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, privateToken(test), func(context.Context) error {
		checks++
		if checks == 2 {
			return errors.New("synthetic-check-secret")
		}
		return nil
	})
	if !errors.Is(err, ErrPrivateArchiveUnauthorized) || metadata != (GitHubMetadata{}) || requests.Load() != 1 {
		test.Fatal("metadata parsing preceded its post-read authorization check")
	}
	privateErrorIsRedacted(test, err)
}

type metadataBody struct {
	reader io.Reader
	closed atomic.Int64
	read   atomic.Int64
}

func (body *metadataBody) Read(destination []byte) (int, error) {
	count, err := body.reader.Read(destination)
	body.read.Add(int64(count))
	return count, err
}

func (body *metadataBody) Close() error {
	body.closed.Add(1)
	return nil
}

func TestPrivateMetadataLimitsAndStatuses(test *testing.T) {
	const limit = 64 << 10
	valid := `{"node_id":"R_synthetic","full_name":"acme/tools","private":true}`
	for _, scenario := range []struct {
		name   string
		status int
		length int64
		body   string
		want   error
	}{
		{name: "boundary", status: 200, length: -1, body: valid + strings.Repeat(" ", limit-len(valid))},
		{name: "declared-overflow", status: 200, length: limit + 1, body: valid, want: ErrArchiveTooLarge},
		{name: "stream-overflow", status: 200, length: -1, body: valid + strings.Repeat(" ", limit+1-len(valid)), want: ErrArchiveTooLarge},
		{name: "redirect", status: 302, body: "synthetic-response-secret", want: ErrArchiveUnavailable},
		{name: "unauthorized", status: 401, body: "synthetic-response-secret", want: ErrArchiveUnavailable},
		{name: "forbidden", status: 403, body: "synthetic-response-secret", want: ErrArchiveUnavailable},
		{name: "missing", status: 404, body: "synthetic-response-secret", want: ErrArchiveUnavailable},
		{name: "upstream", status: 500, body: "synthetic-response-secret", want: ErrArchiveUnavailable},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			body := &metadataBody{reader: strings.NewReader(scenario.body)}
			requests := 0
			fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: scenario.status, ContentLength: scenario.length, Header: http.Header{"Location": {"https://codeload.github.com/acme/tools?synthetic-capability"}}, Body: body}, nil
			})})
			metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, privateToken(test), func(context.Context) error { return nil })
			if !errors.Is(err, scenario.want) || requests != 1 || body.closed.Load() != 1 || body.read.Load() > limit+1 {
				test.Fatal("metadata limits, fixed status handling or response closure failed")
			}
			if scenario.want != nil && metadata != (GitHubMetadata{}) || scenario.want == nil && metadata.NodeID != "R_synthetic" {
				test.Fatal("metadata response did not match its bounded success state")
			}
			if scenario.status != 200 && body.read.Load() != 0 {
				test.Fatal("metadata error response body was consumed")
			}
			privateErrorIsRedacted(test, err)
		})
	}
}

func TestPrivateMetadataSharesCooldownAndFinalAdmission(test *testing.T) {
	for _, status := range []int{429, 403} {
		test.Run(fmt.Sprintf("status=%d", status), func(test *testing.T) {
			requests := 0
			fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"30"}}, Body: io.NopCloser(strings.NewReader("synthetic-response-secret"))}, nil
			})})
			metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, privateToken(test), func(context.Context) error { return nil })
			if !errors.Is(err, ErrArchiveRateLimited) || metadata != (GitHubMetadata{}) {
				test.Fatal("metadata did not observe the shared retry window")
			}
			if _, err := fetcher.Fetch(test.Context(), Reference{NameWithOwner: "acme/tools"}); !errors.Is(err, ErrArchiveRateLimited) || requests != 1 {
				test.Fatal("metadata retry window did not stop public archive transport")
			}
		})
	}
	requests, checks := 0, 0
	fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"30"}}, Body: http.NoBody}, nil
	})})
	metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, privateToken(test), func(ctx context.Context) error {
		checks++
		if checks == 1 {
			if _, err := fetcher.Fetch(ctx, Reference{NameWithOwner: "acme/tools"}); !errors.Is(err, ErrArchiveRateLimited) {
				test.Error("synthetic public rate limit was not reached")
			}
		}
		return nil
	})
	if !errors.Is(err, ErrArchiveRateLimited) || metadata != (GitHubMetadata{}) || requests != 1 {
		test.Fatal("metadata ignored a public cooldown established during authorization")
	}
}

func TestPrivateMetadataDeadlineAndTransportPrivacy(test *testing.T) {
	for _, budget := range []time.Duration{20 * time.Second, 4 * time.Second} {
		test.Run(budget.String(), func(test *testing.T) {
			synctest.Test(test, func(test *testing.T) {
				started := time.Now()
				token := privateToken(test)
				if budget < 20*time.Second {
					material, err := json.Marshal(map[string]any{"version": 1, "access": "synthetic-private-bearer", "expires_at": started.Add(budget)})
					if err != nil {
						test.Fatal("encode synthetic metadata credential expiry")
					}
					token, err = github.DecodeStoredToken(material)
					clear(material)
					if err != nil {
						test.Fatal("decode synthetic metadata credential expiry")
					}
				}
				fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(request *http.Request) (*http.Response, error) {
					<-request.Context().Done()
					return nil, request.Context().Err()
				})})
				metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, token, func(context.Context) error { return nil })
				if !errors.Is(err, context.DeadlineExceeded) || metadata != (GitHubMetadata{}) || time.Since(started) != budget {
					test.Fatal("metadata ignored its fetcher or credential deadline")
				}
			})
		})
	}
	fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "GET", URL: "https://api.github.com/?synthetic-capability", Err: errors.New("synthetic-transport-secret")}
	})})
	metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, privateToken(test), func(context.Context) error { return nil })
	privateErrorIsRedacted(test, err)
	if !errors.Is(err, ErrArchiveUnavailable) || metadata != (GitHubMetadata{}) {
		test.Fatal("metadata exposed a transport failure instead of its fixed unavailable error")
	}
}

type metadataErrorReader struct{}

func (metadataErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic-response-secret")
}

func TestPrivateMetadataBodyErrorsAndCanceledInput(test *testing.T) {
	body := &metadataBody{reader: metadataErrorReader{}}
	requests := 0
	fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
	})})
	metadata, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, privateToken(test), func(context.Context) error { return nil })
	privateErrorIsRedacted(test, err)
	if !errors.Is(err, ErrArchiveUnavailable) || metadata != (GitHubMetadata{}) || body.closed.Load() != 1 {
		test.Fatal("metadata leaked a body failure or failed to close its response")
	}
	ctx, cancel := context.WithCancelCause(test.Context())
	cancel(errors.New("synthetic-check-secret"))
	metadata, err = fetcher.FetchMetadata(ctx, Reference{NameWithOwner: "acme/tools"}, privateToken(test), func(context.Context) error { return nil })
	if !errors.Is(err, context.Canceled) || metadata != (GitHubMetadata{}) || requests != 1 {
		test.Fatal("canceled metadata input reached transport or returned its cancellation cause")
	}
	privateErrorIsRedacted(test, err)
	for _, reference := range []Reference{{NameWithOwner: "Acme/tools"}, {NameWithOwner: "acme/tools?synthetic-capability"}, {NameWithOwner: "acme/tools", Branch: "HEAD"}} {
		metadata, err := fetcher.FetchMetadata(test.Context(), reference, privateToken(test), func(context.Context) error { return nil })
		if !errors.Is(err, ErrInvalidReference) || metadata != (GitHubMetadata{}) || requests != 1 {
			test.Fatal("noncanonical metadata reference reached transport")
		}
		privateErrorIsRedacted(test, err)
	}
}

func TestPrivateMetadataAuthorizationBudget(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		started := time.Now()
		fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"node_id":"R_synthetic","full_name":"acme/tools","private":true}`))}, nil
		})})
		_, err := fetcher.FetchMetadata(test.Context(), Reference{NameWithOwner: "acme/tools"}, privateToken(test), func(ctx context.Context) error {
			deadline, exists := ctx.Deadline()
			if !exists || deadline.Sub(started) != 20*time.Second {
				test.Error("metadata authorization callback escaped the total fetch budget")
			}
			return nil
		})
		if err != nil {
			test.Fatal("bounded metadata authorization did not complete")
		}
	})
}
