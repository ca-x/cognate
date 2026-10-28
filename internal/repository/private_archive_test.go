package repository

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/cognate/internal/github"
)

func privateToken(test *testing.T) github.Token {
	test.Helper()
	token, err := github.NewToken("synthetic-private-bearer", nil)
	if err != nil {
		test.Fatal("create synthetic token")
	}
	return token
}

func TestPrivateArchiveRechecksCooldownAfterAuthorization(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		data := archiveFixture(test, archiveEntry{name: "root/SKILL.md", body: "Synthetic skill"})
		var apiRequests, archiveRequests atomic.Int64
		fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host == "api.github.com" {
				apiRequests.Add(1)
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://codeload.github.com/org/private/legacy.tar.gz/main?token=synthetic"}}, Body: http.NoBody}, nil
			}
			if request.URL.Path == "/org/public/tar.gz/HEAD" {
				return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"60"}}, Body: http.NoBody}, nil
			}
			archiveRequests.Add(1)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
		})})
		checks := 0
		snapshot, err := fetcher.FetchPrivate(test.Context(), Reference{NameWithOwner: "org/private"}, privateToken(test), func(ctx context.Context) error {
			checks++
			if checks == 2 {
				if _, err := fetcher.Fetch(ctx, Reference{NameWithOwner: "org/public"}); !errors.Is(err, ErrArchiveRateLimited) {
					test.Fatal("public request did not establish shared cooldown")
				}
			}
			return nil
		})
		if !errors.Is(err, ErrArchiveRateLimited) || snapshot != nil || apiRequests.Load() != 1 || archiveRequests.Load() != 0 {
			test.Fatal("private second hop ignored cooldown established during its authorization check")
		}
	})
}

func privateHTTPFetcher(test *testing.T, api, archive http.Handler) *ArchiveFetcher {
	test.Helper()
	apiServer := httptest.NewTLSServer(api)
	test.Cleanup(apiServer.Close)
	archiveServer := httptest.NewTLSServer(archive)
	test.Cleanup(archiveServer.Close)
	apiURL, _ := url.Parse(apiServer.URL)
	archiveURL, _ := url.Parse(archiveServer.URL)
	jar, err := cookiejar.New(nil)
	if err != nil {
		test.Fatal("create synthetic cookie jar")
	}
	for _, host := range []string{"api.github.com", "codeload.github.com"} {
		jar.SetCookies(&url.URL{Scheme: "https", Host: host}, []*http.Cookie{{Name: "synthetic-inherited-cookie", Value: "must-not-send"}})
	}
	return NewArchiveFetcher(&http.Client{Jar: jar, Transport: archiveTransport(func(request *http.Request) (*http.Response, error) {
		copy := request.Clone(request.Context())
		copy.URL = request.URL.Clone()
		copy.Host = request.URL.Host
		switch request.URL.Host {
		case "api.github.com":
			copy.URL.Scheme, copy.URL.Host = apiURL.Scheme, apiURL.Host
			return apiServer.Client().Transport.RoundTrip(copy)
		case "codeload.github.com", "codeload.github.com:443":
			copy.URL.Scheme, copy.URL.Host = archiveURL.Scheme, archiveURL.Host
			return archiveServer.Client().Transport.RoundTrip(copy)
		default:
			test.Error("request escaped fixed GitHub hosts")
			return nil, context.Canceled
		}
	})})
}

func TestPrivateArchiveRealHTTPAndBranch(test *testing.T) {
	for _, branch := range []string{"", "feature/nested+name"} {
		test.Run(branch, func(test *testing.T) {
			data := archiveFixture(test,
				archiveEntry{name: "repo-main/.claude/skills/demo/SKILL.md", body: "---\nname: Demo\ndescription: Private fixture\n---\nInstructions"},
				archiveEntry{name: "repo-main/.claude/skills/demo/.hidden.bin", body: "\x00\xffbinary"},
				archiveEntry{name: "repo-main/.claude/skills/demo/run", body: "run", mode: 0o755},
			)
			var checks, apiRequests, archiveRequests atomic.Int64
			fetcher := privateHTTPFetcher(test, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				apiRequests.Add(1)
				expected := "/repos/org/repo/tarball"
				if branch != "" {
					expected += "/feature%2Fnested+name"
				}
				if request.Method != http.MethodGet || request.RequestURI != expected || request.Host != "api.github.com" || request.Header.Get("Authorization") != "Bearer synthetic-private-bearer" || request.Header.Get("Cookie") != "" || checks.Load() < 1 {
					test.Error("API request violated identity, branch, authorization or check contract")
				}
				writer.Header().Set("Location", "https://codeload.github.com/org/repo/legacy.tar.gz/main?token=synthetic-capability%2B&signature=opaque")
				writer.Header().Set("Set-Cookie", "must-not-forward=synthetic-cookie; Domain=github.com")
				writer.WriteHeader(http.StatusFound)
			}), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				archiveRequests.Add(1)
				if request.RequestURI != "/org/repo/legacy.tar.gz/main?token=synthetic-capability%2B&signature=opaque" || request.Host != "codeload.github.com" || checks.Load() < 2 {
					test.Error("archive request lost its exact capability URL or authorization check")
				}
				for _, header := range []string{"Authorization", "Cookie", "Referer", "Proxy-Authorization"} {
					if request.Header.Get(header) != "" {
						test.Error("archive request leaked a sensitive header")
					}
				}
				_, _ = writer.Write(data)
			}))
			snapshot, err := fetcher.FetchPrivate(test.Context(), Reference{NameWithOwner: "org/repo", Branch: branch}, privateToken(test), func(context.Context) error {
				checks.Add(1)
				return nil
			})
			if err != nil || snapshot == nil || len(snapshot.Skills) != 1 || !bytes.Equal(snapshot.Data, data) || len(snapshot.Hash) != 64 || apiRequests.Load() != 1 || archiveRequests.Load() != 1 || checks.Load() < 3 {
				test.Fatal("private archive did not complete the authorized two-hop whole-archive fetch")
			}
		})
	}
}

func privateErrorIsRedacted(test *testing.T, err error) {
	test.Helper()
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Error("private archive failed", slog.Any("error", err), slog.Any("credential", privateToken(test)))
	formatted := fmt.Sprintf("%v %+v %#v %v %#v", err, err, err, privateToken(test), privateToken(test)) + logs.String()
	for _, secret := range []string{"synthetic-private-bearer", "synthetic-capability", "synthetic-response-secret", "synthetic-check-secret", "synthetic-transport-secret"} {
		if strings.Contains(formatted, secret) {
			test.Fatal("error formatting or structured logging exposed secret material")
		}
	}
}

func TestPrivateArchiveRejectsUnsafeLocations(test *testing.T) {
	for _, scenario := range []struct {
		name      string
		locations []string
	}{
		{name: "missing"},
		{name: "empty", locations: []string{""}},
		{name: "plaintext", locations: []string{"http://codeload.github.com/org/repo?token=synthetic-capability"}},
		{name: "other-host", locations: []string{"https://evil.invalid/org/repo?token=synthetic-capability"}},
		{name: "suffix-host", locations: []string{"https://codeload.github.com.evil.invalid/archive"}},
		{name: "subdomain", locations: []string{"https://private.codeload.github.com/archive"}},
		{name: "api-redirect", locations: []string{"https://api.github.com/repos/org/other/tarball"}},
		{name: "userinfo", locations: []string{"https://synthetic-capability@codeload.github.com/archive"}},
		{name: "port", locations: []string{"https://codeload.github.com:444/archive"}},
		{name: "host-dot", locations: []string{"https://codeload.github.com./archive"}},
		{name: "fragment", locations: []string{"https://codeload.github.com/archive#synthetic-capability"}},
		{name: "empty-fragment", locations: []string{"https://codeload.github.com/archive#"}},
		{name: "relative", locations: []string{"/org/repo/archive"}},
		{name: "scheme-relative", locations: []string{"//codeload.github.com/archive"}},
		{name: "opaque", locations: []string{"https:codeload.github.com/archive"}},
		{name: "whitespace", locations: []string{"https://codeload.github.com/invalid path"}},
		{name: "oversized", locations: []string{"https://codeload.github.com/archive?signature=" + strings.Repeat("x", 8<<10)}},
		{name: "ambiguous", locations: []string{"https://codeload.github.com/one", "https://codeload.github.com/two"}},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			var requests atomic.Int64
			fetcher := privateHTTPFetcher(test, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				for _, location := range scenario.locations {
					writer.Header().Add("Location", location)
				}
				writer.WriteHeader(http.StatusFound)
			}), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
				test.Error("unsafe Location reached a second network hop")
			}))
			snapshot, err := fetcher.FetchPrivate(test.Context(), Reference{NameWithOwner: "org/repo"}, privateToken(test), func(context.Context) error { return nil })
			if !errors.Is(err, ErrArchiveUnavailable) || snapshot != nil || requests.Load() != 1 {
				test.Fatal("unsafe Location was accepted or retried")
			}
			privateErrorIsRedacted(test, err)
		})
	}
}

func TestPrivateArchiveAuthorityCheckpoints(test *testing.T) {
	for deniedAt := 1; deniedAt <= 4; deniedAt++ {
		test.Run(fmt.Sprint(deniedAt), func(test *testing.T) {
			data := archiveFixture(test, archiveEntry{name: "root/SKILL.md", body: "Synthetic skill"})
			var apiRequests, archiveRequests atomic.Int64
			fetcher := privateHTTPFetcher(test, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				apiRequests.Add(1)
				writer.Header().Set("Location", "https://codeload.github.com/org/repo/archive?token=synthetic-capability")
				writer.WriteHeader(http.StatusFound)
			}), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				archiveRequests.Add(1)
				_, _ = writer.Write(data)
			}))
			checks := 0
			snapshot, err := fetcher.FetchPrivate(test.Context(), Reference{NameWithOwner: "org/repo"}, privateToken(test), func(context.Context) error {
				checks++
				if checks == deniedAt {
					return errors.New("synthetic-check-secret synthetic-private-bearer")
				}
				return nil
			})
			expectedAPI, expectedArchive := int64(1), int64(1)
			if deniedAt == 1 {
				expectedAPI = 0
			}
			if deniedAt <= 2 {
				expectedArchive = 0
			}
			if !errors.Is(err, ErrPrivateArchiveUnauthorized) || snapshot != nil || apiRequests.Load() != expectedAPI || archiveRequests.Load() != expectedArchive {
				test.Fatal("changed authority survived a fetch checkpoint")
			}
			privateErrorIsRedacted(test, err)
		})
	}
}

func TestPrivateArchiveRequiresCheckAndToken(test *testing.T) {
	var requests atomic.Int64
	fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("synthetic-transport-secret")
	})})
	for _, scenario := range []struct {
		name  string
		token github.Token
		check func(context.Context) error
	}{
		{name: "nil-check", token: privateToken(test)},
		{name: "empty-token", check: func(context.Context) error { return nil }},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			if snapshot, err := fetcher.FetchPrivate(test.Context(), Reference{NameWithOwner: "org/repo"}, scenario.token, scenario.check); !errors.Is(err, ErrPrivateArchiveUnauthorized) || snapshot != nil {
				test.Fatal("missing private authority was accepted")
			}
		})
	}
	for _, reference := range []Reference{{NameWithOwner: "Org/Repo"}, {NameWithOwner: "https://github.com/org/repo"}, {NameWithOwner: "org/repo", Branch: "../main"}, {NameWithOwner: "org/repo", Branch: "HEAD"}} {
		if snapshot, err := fetcher.FetchPrivate(test.Context(), reference, privateToken(test), func(context.Context) error { return nil }); !errors.Is(err, ErrInvalidReference) || snapshot != nil {
			test.Fatal("noncanonical or unsafe reference was accepted")
		}
	}
	if requests.Load() != 0 {
		test.Fatal("invalid input issued an HTTP request")
	}
}

func TestPrivateArchiveStatusErrorsNeverFallbackOrRedirect(test *testing.T) {
	for _, hop := range []string{"api", "archive"} {
		for _, status := range []int{http.StatusOK, http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
			if hop == "api" && status == http.StatusFound {
				continue
			}
			if hop == "archive" && status == http.StatusOK {
				continue
			}
			test.Run(fmt.Sprintf("%s-%d", hop, status), func(test *testing.T) {
				var apiRequests, archiveRequests atomic.Int64
				fetcher := privateHTTPFetcher(test, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					apiRequests.Add(1)
					writer.Header().Set("Location", "https://codeload.github.com/org/repo/archive?token=synthetic-capability")
					if hop == "api" {
						writer.WriteHeader(status)
					} else {
						writer.WriteHeader(http.StatusFound)
					}
					_, _ = io.WriteString(writer, "synthetic-response-secret")
				}), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					archiveRequests.Add(1)
					writer.Header().Set("Location", "https://codeload.github.com/another?token=synthetic-capability")
					writer.WriteHeader(status)
					_, _ = io.WriteString(writer, "synthetic-response-secret")
				}))
				snapshot, err := fetcher.FetchPrivate(test.Context(), Reference{NameWithOwner: "org/repo"}, privateToken(test), func(context.Context) error { return nil })
				expectedArchive := int64(0)
				if hop == "archive" {
					expectedArchive = 1
				}
				if !errors.Is(err, ErrArchiveUnavailable) || snapshot != nil || apiRequests.Load() != 1 || archiveRequests.Load() != expectedArchive {
					test.Fatal("upstream error followed a redirect, retried, or fell back to public")
				}
				privateErrorIsRedacted(test, err)
			})
		}
	}
}

func TestPrivateArchiveTransportErrorsAreRedacted(test *testing.T) {
	for failedHop := range 2 {
		test.Run(fmt.Sprint(failedHop), func(test *testing.T) {
			requests := 0
			fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
				requests++
				if requests == failedHop+1 {
					return nil, &url.Error{Op: "Get", URL: "https://codeload.github.com/archive?token=synthetic-capability", Err: errors.New("synthetic-transport-secret synthetic-private-bearer")}
				}
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://codeload.github.com/archive?token=synthetic-capability"}}, Body: http.NoBody}, nil
			})})
			snapshot, err := fetcher.FetchPrivate(test.Context(), Reference{NameWithOwner: "org/repo"}, privateToken(test), func(context.Context) error { return nil })
			if !errors.Is(err, ErrArchiveUnavailable) || snapshot != nil || requests != failedHop+1 {
				test.Fatal("transport error was not returned once as a fixed safe failure")
			}
			privateErrorIsRedacted(test, err)
		})
	}
}

func TestPrivateArchiveCancellationAtEachHop(test *testing.T) {
	for _, stage := range []string{"before-api", "between-hops", "reading"} {
		test.Run(stage, func(test *testing.T) {
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			var apiRequests, archiveRequests atomic.Int64
			fetcher := privateHTTPFetcher(test, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				apiRequests.Add(1)
				writer.Header().Set("Location", "https://codeload.github.com/archive?token=synthetic-capability")
				writer.WriteHeader(http.StatusFound)
			}), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				archiveRequests.Add(1)
				writer.WriteHeader(http.StatusOK)
				writer.(http.Flusher).Flush()
				cancel()
				<-request.Context().Done()
			}))
			checks := 0
			if stage == "before-api" {
				cancel()
			}
			snapshot, err := fetcher.FetchPrivate(ctx, Reference{NameWithOwner: "org/repo"}, privateToken(test), func(context.Context) error {
				checks++
				if stage == "between-hops" && checks == 2 {
					cancel()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || snapshot != nil || stage == "before-api" && apiRequests.Load() != 0 || stage != "reading" && archiveRequests.Load() != 0 {
				test.Fatal("cancellation issued a later request or returned archive data")
			}
		})
	}
}

func TestPrivateArchiveSharedDeadlineAndTokenExpiry(test *testing.T) {
	for _, scenario := range []struct {
		name   string
		budget time.Duration
	}{
		{name: "total-budget", budget: 20 * time.Second},
		{name: "token-expiry", budget: 5 * time.Second},
		{name: "caller-budget", budget: 4 * time.Second},
		{name: "expired-token"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			synctest.Test(test, func(test *testing.T) {
				started := time.Now()
				ctx := test.Context()
				token := privateToken(test)
				if scenario.name == "token-expiry" || scenario.name == "expired-token" {
					material, err := json.Marshal(map[string]any{"version": 1, "access": "synthetic-private-bearer", "expires_at": started.Add(scenario.budget)})
					if err != nil {
						test.Fatal("encode synthetic expiry")
					}
					token, err = github.DecodeStoredToken(material)
					if err != nil {
						test.Fatal("decode synthetic expiry")
					}
				}
				if scenario.name == "caller-budget" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, scenario.budget)
					defer cancel()
				}
				requests := 0
				fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					deadline, exists := request.Context().Deadline()
					if !exists || !deadline.Equal(started.Add(scenario.budget)) {
						test.Error("hops did not share the earliest authorization/request deadline")
					}
					delay := 12 * time.Second
					if scenario.budget < 20*time.Second {
						delay = 3 * time.Second
					}
					select {
					case <-request.Context().Done():
						return nil, request.Context().Err()
					case <-time.After(delay):
						return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://codeload.github.com/archive?token=synthetic-capability"}}, Body: http.NoBody}, nil
					}
				})})
				snapshot, err := fetcher.FetchPrivate(ctx, Reference{NameWithOwner: "org/repo"}, token, func(context.Context) error { return nil })
				if !errors.Is(err, context.DeadlineExceeded) || snapshot != nil || time.Since(started) != scenario.budget || scenario.budget == 0 && requests != 0 || scenario.budget != 0 && requests != 2 {
					test.Fatal("private fetch exceeded the shared deadline or fetched with an expired token")
				}
			})
		})
	}
}

func TestPrivateArchiveLimitsAndQueryNegativeControls(test *testing.T) {
	for _, scenario := range []string{"missing-query", "advertised-size", "streamed-size", "unsafe-tar", "large-skill", "invalid-gzip"} {
		test.Run(scenario, func(test *testing.T) {
			data := archiveFixture(test, archiveEntry{name: "root/SKILL.md", body: "Synthetic skill"})
			if scenario == "unsafe-tar" {
				data = archiveFixture(test, archiveEntry{name: "root/../../SKILL.md", body: "synthetic-response-secret"})
			}
			if scenario == "large-skill" {
				data = archiveFixture(test, archiveEntry{name: "root/SKILL.md", body: strings.Repeat("x", maxSkillDocument+1)})
			}
			if scenario == "invalid-gzip" {
				data = []byte("synthetic-response-secret")
			}
			fetcher := privateHTTPFetcher(test, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				location := "https://codeload.github.com/archive?token=synthetic-capability"
				if scenario == "missing-query" {
					location = "https://codeload.github.com/archive"
				}
				writer.Header().Set("Location", location)
				writer.WriteHeader(http.StatusFound)
			}), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch scenario {
				case "missing-query":
					if request.URL.RawQuery == "" {
						writer.WriteHeader(http.StatusForbidden)
						return
					}
				case "advertised-size":
					writer.Header().Set("Content-Length", fmt.Sprint(MaxArchiveBytes+1))
					writer.WriteHeader(http.StatusOK)
					return
				case "streamed-size":
					writer.WriteHeader(http.StatusOK)
					writer.(http.Flusher).Flush()
					_, _ = io.CopyN(writer, strings.NewReader(strings.Repeat("x", MaxArchiveBytes+1)), MaxArchiveBytes+1)
					return
				}
				_, _ = writer.Write(data)
			}))
			expected := ErrArchiveInvalid
			if scenario == "missing-query" {
				expected = ErrArchiveUnavailable
			} else if scenario == "advertised-size" || scenario == "streamed-size" || scenario == "large-skill" {
				expected = ErrArchiveTooLarge
			}
			snapshot, err := fetcher.FetchPrivate(test.Context(), Reference{NameWithOwner: "org/repo"}, privateToken(test), func(context.Context) error { return nil })
			if !errors.Is(err, expected) || snapshot != nil {
				test.Fatal("archive/query negative control bypassed the existing validation limits")
			}
			privateErrorIsRedacted(test, err)
		})
	}
}

func TestPrivateArchiveCooldownSharedWithPublic(test *testing.T) {
	for _, source := range []string{"public", "api-429", "api-403", "archive-429", "archive-403"} {
		test.Run(source, func(test *testing.T) {
			synctest.Test(test, func(test *testing.T) {
				data := archiveFixture(test, archiveEntry{name: "root/SKILL.md", body: "Synthetic skill"})
				limited := true
				requests := 0
				fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					atAPI := request.URL.Host == "api.github.com"
					if limited && (source == "public" || strings.HasPrefix(source, "api-") == atAPI) {
						status := http.StatusTooManyRequests
						if strings.HasSuffix(source, "403") {
							status = http.StatusForbidden
						}
						return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"60"}}, Body: http.NoBody}, nil
					}
					if atAPI {
						return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://codeload.github.com/archive?token=synthetic-capability"}}, Body: http.NoBody}, nil
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
				})})
				reference := Reference{NameWithOwner: "org/repo"}
				private := func() (*ArchiveSnapshot, error) {
					return fetcher.FetchPrivate(test.Context(), reference, privateToken(test), func(context.Context) error { return nil })
				}
				var err error
				if source == "public" {
					_, err = fetcher.Fetch(test.Context(), reference)
				} else {
					_, err = private()
				}
				if !errors.Is(err, ErrArchiveRateLimited) {
					test.Fatal("private/public upstream rate limit was not classified")
				}
				limited = false
				before := requests
				for _, attempt := range []func() (*ArchiveSnapshot, error){private, func() (*ArchiveSnapshot, error) {
					return fetcher.Fetch(test.Context(), Reference{NameWithOwner: "org/other"})
				}} {
					if snapshot, err := attempt(); !errors.Is(err, ErrArchiveRateLimited) || snapshot != nil || requests != before {
						test.Fatal("shared cooldown allowed a public/private request")
					}
				}
				time.Sleep(59 * time.Second)
				if _, err := private(); !errors.Is(err, ErrArchiveRateLimited) || requests != before {
					test.Fatal("private request resumed before Retry-After")
				}
				time.Sleep(time.Second)
				if snapshot, err := private(); err != nil || snapshot == nil || requests != before+2 {
					test.Fatal("private archive did not resume with a fresh two-hop request at the deadline")
				}
			})
		})
	}
}

func TestPrivateArchiveConcurrentPublicAndPrivateEntry(test *testing.T) {
	data := archiveFixture(test, archiveEntry{name: "root/SKILL.md", body: "Synthetic skill"})
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	releaseRequests := sync.OnceFunc(func() { close(release) })
	var group sync.WaitGroup
	defer group.Wait()
	defer releaseRequests()
	ctx, cancel := context.WithTimeout(test.Context(), 3*time.Second)
	defer cancel()
	fetcher := privateHTTPFetcher(test, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Location", "https://codeload.github.com/archive?token=synthetic-capability")
		writer.WriteHeader(http.StatusFound)
	}), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-request.Context().Done():
			return
		}
		_, _ = writer.Write(data)
	}))
	results := make(chan error, 2)
	group.Go(func() {
		_, err := fetcher.FetchPrivate(ctx, Reference{NameWithOwner: "org/private"}, privateToken(test), func(context.Context) error { return nil })
		results <- err
	})
	group.Go(func() {
		_, err := fetcher.Fetch(ctx, Reference{NameWithOwner: "org/public"})
		results <- err
	})
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			test.Fatal("private/public entry held a shared lock across HTTP")
		}
	}
	releaseRequests()
	for range 2 {
		if err := <-results; err != nil {
			test.Fatal("concurrent private/public fetch failed")
		}
	}
}

func TestPrivateArchiveBodyErrorsAndCancelCauseAreRedacted(test *testing.T) {
	for _, mode := range []string{"read-error", "cancel-cause"} {
		test.Run(mode, func(test *testing.T) {
			ctx, cancel := context.WithCancelCause(test.Context())
			defer cancel(nil)
			fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "api.github.com" {
					return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://codeload.github.com:443/archive?token=synthetic-capability"}}, Body: http.NoBody}, nil
				}
				reader, writer := io.Pipe()
				_ = writer.CloseWithError(errors.New("synthetic-response-secret synthetic-capability"))
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: reader}, nil
			})})
			snapshot, err := fetcher.FetchPrivate(ctx, Reference{NameWithOwner: "org/repo"}, privateToken(test), func(context.Context) error {
				if mode == "cancel-cause" {
					cancel(errors.New("synthetic-check-secret"))
				}
				return nil
			})
			expected := ErrArchiveUnavailable
			if mode == "cancel-cause" {
				expected = context.Canceled
			}
			if !errors.Is(err, expected) || snapshot != nil {
				test.Fatal("body error or cancellation cause escaped its safe error classification")
			}
			privateErrorIsRedacted(test, err)
		})
	}
}

func TestPrivateArchiveConcurrentRateRepliesNeverShortenPublicCooldown(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		apiEntered, publicEntered := make(chan struct{}), make(chan struct{})
		privateReply, publicReply := make(chan struct{}), make(chan struct{})
		releasePrivate := sync.OnceFunc(func() { close(privateReply) })
		releasePublic := sync.OnceFunc(func() { close(publicReply) })
		var group sync.WaitGroup
		defer group.Wait()
		defer releasePrivate()
		defer releasePublic()
		var requests atomic.Int64
		fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(request *http.Request) (*http.Response, error) {
			requests.Add(1)
			header := "30"
			if request.URL.Host == "api.github.com" {
				close(apiEntered)
				<-privateReply
				header = "120"
			} else {
				close(publicEntered)
				<-publicReply
			}
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {header}}, Body: http.NoBody}, nil
		})})
		results := make(chan error, 2)
		group.Go(func() {
			_, err := fetcher.FetchPrivate(test.Context(), Reference{NameWithOwner: "org/private"}, privateToken(test), func(context.Context) error { return nil })
			results <- err
		})
		group.Go(func() {
			_, err := fetcher.Fetch(test.Context(), Reference{NameWithOwner: "org/public"})
			results <- err
		})
		<-apiEntered
		<-publicEntered
		releasePrivate()
		if err := <-results; !errors.Is(err, ErrArchiveRateLimited) {
			test.Fatal("private response did not establish cooldown")
		}
		time.Sleep(10 * time.Second)
		releasePublic()
		if err := <-results; !errors.Is(err, ErrArchiveRateLimited) {
			test.Fatal("later public response was not classified as rate limited")
		}
		time.Sleep(109 * time.Second)
		if _, err := fetcher.Fetch(test.Context(), Reference{NameWithOwner: "org/next"}); !errors.Is(err, ErrArchiveRateLimited) || requests.Load() != 2 {
			test.Fatal("later public reply shortened the private response's shared cooldown")
		}
	})
}

func TestPrivateArchiveRealHTTPRateLimitHeaders(test *testing.T) {
	for _, hop := range []string{"api", "archive"} {
		for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
			test.Run(fmt.Sprintf("%s-%d", hop, status), func(test *testing.T) {
				var requests atomic.Int64
				fetcher := privateHTTPFetcher(test, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					requests.Add(1)
					if hop == "api" {
						writer.Header().Set("Retry-After", "60")
						writer.WriteHeader(status)
						return
					}
					writer.Header().Set("Location", "https://codeload.github.com:443/archive?token=synthetic-capability")
					writer.WriteHeader(http.StatusFound)
				}), http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					requests.Add(1)
					if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
						test.Error("codeload rate-limit request received API secrets")
					}
					writer.Header().Set("Retry-After", "60")
					writer.WriteHeader(status)
				}))
				snapshot, err := fetcher.FetchPrivate(test.Context(), Reference{NameWithOwner: "org/repo"}, privateToken(test), func(context.Context) error { return nil })
				if !errors.Is(err, ErrArchiveRateLimited) || snapshot != nil {
					test.Fatal("real HTTP rate-limit header was not classified")
				}
				before := requests.Load()
				if _, err := fetcher.Fetch(test.Context(), Reference{NameWithOwner: "org/public"}); !errors.Is(err, ErrArchiveRateLimited) || requests.Load() != before {
					test.Fatal("private real HTTP rate limit did not block public network entry")
				}
			})
		}
	}
}
