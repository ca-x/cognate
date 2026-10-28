package repository

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestArchiveFetchHonorsRetryAfterSeconds(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		data := archiveFixture(test, archiveEntry{name: "repo-main/example/SKILL.md", body: "---\nname: Example\ndescription: Synthetic\n---\nInstructions"})
		var requests atomic.Int64
		fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
			if requests.Add(1) == 1 {
				return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"120"}}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
		})})
		if _, err := fetcher.Fetch(test.Context(), Reference{NameWithOwner: "org/one"}); !errors.Is(err, ErrArchiveRateLimited) {
			test.Fatal("initial rate limit was not reported safely")
		}
		if _, err := fetcher.Fetch(test.Context(), Reference{NameWithOwner: "org/two"}); !errors.Is(err, ErrArchiveRateLimited) || requests.Load() != 1 {
			test.Fatal("a second repository request bypassed the declared Retry-After window")
		}
		time.Sleep(119 * time.Second)
		if _, err := fetcher.Fetch(test.Context(), Reference{NameWithOwner: "org/two"}); !errors.Is(err, ErrArchiveRateLimited) || requests.Load() != 1 {
			test.Fatal("a request reached GitHub before the declared deadline")
		}
		time.Sleep(time.Second)
		snapshot, err := fetcher.Fetch(test.Context(), Reference{NameWithOwner: "org/two"})
		if err != nil || snapshot == nil || len(snapshot.Skills) != 1 || requests.Load() != 2 {
			test.Fatal("archive download did not resume successfully at the retry deadline")
		}
	})
}

func TestArchiveFetchAdmissionDoesNotSerializeNetwork(test *testing.T) {
	data := archiveFixture(test, archiveEntry{name: "repo-main/example/SKILL.md", body: "Instructions"})
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var group sync.WaitGroup
	defer group.Wait()
	defer close(release)
	ctx, cancel := context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-release
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})})
	for _, reference := range []Reference{{NameWithOwner: "org/one"}, {NameWithOwner: "org/two"}} {
		group.Go(func() { _, _ = fetcher.Fetch(test.Context(), reference) })
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			test.Fatal("rate admission serialized network IO instead of permitting both requests")
		}
	}
}

func TestArchiveFetchUnusableRetryAfterKeepsCallerPolicy(test *testing.T) {
	for _, header := range []string{"", "0", "-1", "+1", "1.5", "1e3", "not-a-date", "9999999999999999999999x", "Fri, 31 Dec 1999 23:59:59 GMT"} {
		test.Run(header, func(test *testing.T) {
			synctest.Test(test, func(test *testing.T) {
				data := archiveFixture(test, archiveEntry{name: "repo-main/example/SKILL.md", body: "Instructions"})
				var requests atomic.Int64
				fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
					status := http.StatusOK
					if requests.Add(1) == 1 {
						status = http.StatusTooManyRequests
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {header}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
				})})
				reference := Reference{NameWithOwner: "org/repo"}
				if _, err := fetcher.Fetch(test.Context(), reference); !errors.Is(err, ErrArchiveRateLimited) || header != "" && strings.Contains(err.Error(), header) {
					test.Fatal("a rate-limit error exposed response material or changed classification")
				}
				if snapshot, err := fetcher.Fetch(test.Context(), reference); err != nil || snapshot == nil || requests.Load() != 2 {
					test.Fatal("an unusable retry header imposed a new caller retry policy")
				}
			})
		})
	}
}

func TestArchiveFetchNumericRetryAfterOverflowFailsClosed(test *testing.T) {
	for _, header := range []string{"9223372037", "18446744073709551616", strings.Repeat("9", 128)} {
		test.Run(header, func(test *testing.T) {
			synctest.Test(test, func(test *testing.T) {
				data := archiveFixture(test, archiveEntry{name: "repo-main/example/SKILL.md", body: "Instructions"})
				var requests atomic.Int64
				client := &http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
					status := http.StatusOK
					if requests.Add(1) == 1 {
						status = http.StatusTooManyRequests
						return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {header}}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
				})}
				fetcher := NewArchiveFetcher(client)
				reference := Reference{NameWithOwner: "org/repo"}
				if _, err := fetcher.Fetch(test.Context(), reference); !errors.Is(err, ErrArchiveRateLimited) {
					test.Fatal("overflowing rate-limit response changed error classification")
				}
				if _, err := fetcher.Fetch(test.Context(), reference); !errors.Is(err, ErrArchiveRateLimited) || requests.Load() != 1 {
					test.Fatal("positive numeric Retry-After overflow failed open")
				}
				time.Sleep(400 * 24 * time.Hour)
				if _, err := fetcher.Fetch(test.Context(), reference); !errors.Is(err, ErrArchiveRateLimited) || requests.Load() != 1 {
					test.Fatal("an unrepresentable retry delay wrapped or expired early")
				}
				if snapshot, err := NewArchiveFetcher(client).Fetch(test.Context(), reference); err != nil || snapshot == nil || requests.Load() != 2 {
					test.Fatal("a fresh fetcher could not reset its own process-local rate state")
				}
			})
		})
	}
}

func TestArchiveFetchConcurrentRepliesNeverShortenCooldown(test *testing.T) {
	for _, scenario := range []struct {
		name      string
		status    int
		header    string
		remaining time.Duration
	}{
		{name: "shorter", status: http.StatusTooManyRequests, header: "60", remaining: 110 * time.Second},
		{name: "zero", status: http.StatusTooManyRequests, header: "0", remaining: 110 * time.Second},
		{name: "missing", status: http.StatusTooManyRequests, remaining: 110 * time.Second},
		{name: "malformed", status: http.StatusTooManyRequests, header: "not-a-date", remaining: 110 * time.Second},
		{name: "past-date", status: http.StatusTooManyRequests, header: "Thu, 01 Jan 1970 00:00:00 GMT", remaining: 110 * time.Second},
		{name: "successful-reply", status: http.StatusOK, remaining: 110 * time.Second},
		{name: "equal-delay-later-reply", status: http.StatusTooManyRequests, header: "120", remaining: 120 * time.Second},
		{name: "longer-later-reply", status: http.StatusTooManyRequests, header: "180", remaining: 180 * time.Second},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			synctest.Test(test, func(test *testing.T) {
				data := archiveFixture(test, archiveEntry{name: "repo-main/example/SKILL.md", body: "Instructions"})
				entered := make(chan struct{}, 2)
				first, second := make(chan struct{}), make(chan struct{})
				releaseFirst := sync.OnceFunc(func() { close(first) })
				releaseSecond := sync.OnceFunc(func() { close(second) })
				var group sync.WaitGroup
				defer group.Wait()
				defer releaseFirst()
				defer releaseSecond()
				var requests atomic.Int64
				fetcher := NewArchiveFetcher(&http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
					status, header := http.StatusOK, ""
					switch requests.Add(1) {
					case 1:
						entered <- struct{}{}
						<-first
						status, header = http.StatusTooManyRequests, "120"
					case 2:
						entered <- struct{}{}
						<-second
						status, header = scenario.status, scenario.header
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {header}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
				})})
				results := make(chan error, 2)
				for _, reference := range []Reference{{NameWithOwner: "org/one"}, {NameWithOwner: "org/two"}} {
					group.Go(func() {
						_, err := fetcher.Fetch(test.Context(), reference)
						results <- err
					})
				}
				for range 2 {
					select {
					case <-entered:
					case <-time.After(time.Second):
						test.Fatal("rate admission serialized already admitted network requests")
					}
				}
				releaseFirst()
				if err := <-results; !errors.Is(err, ErrArchiveRateLimited) {
					test.Fatal("the first admitted request did not report its real rate limit")
				}
				time.Sleep(10 * time.Second)
				releaseSecond()
				err := <-results
				if scenario.status == http.StatusOK && err != nil || scenario.status != http.StatusOK && !errors.Is(err, ErrArchiveRateLimited) {
					test.Fatal("an already admitted request was incorrectly canceled or misclassified")
				}
				time.Sleep(scenario.remaining - time.Second)
				if _, err := fetcher.Fetch(test.Context(), Reference{NameWithOwner: "org/three"}); !errors.Is(err, ErrArchiveRateLimited) || requests.Load() != 2 {
					test.Fatal("concurrent replies replaced the greatest absolute retry deadline")
				}
				time.Sleep(time.Second)
				if snapshot, err := fetcher.Fetch(test.Context(), Reference{NameWithOwner: "org/three"}); err != nil || snapshot == nil || requests.Load() != 3 {
					test.Fatal("the greatest concurrent retry deadline did not expire normally")
				}
			})
		})
	}
}

func TestArchiveFetchHonorsRetryAfterDateAndInstanceIsolation(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		data := archiveFixture(test, archiveEntry{name: "repo-main/example/SKILL.md", body: "Instructions"})
		var requests atomic.Int64
		retryAt := time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)
		client := &http.Client{Transport: archiveTransport(func(*http.Request) (*http.Response, error) {
			if requests.Add(1) == 1 {
				return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {retryAt}}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
		})}
		fetcher := NewArchiveFetcher(client)
		reference := Reference{NameWithOwner: "org/repo"}
		if _, err := fetcher.Fetch(test.Context(), reference); !errors.Is(err, ErrArchiveRateLimited) {
			test.Fatal("initial date-based rate limit was not reported")
		}
		canceled, cancel := context.WithCancel(test.Context())
		cancel()
		if _, err := fetcher.Fetch(canceled, reference); !errors.Is(err, context.Canceled) || requests.Load() != 1 {
			test.Fatal("cancellation did not take priority without starting a request")
		}
		if snapshot, err := NewArchiveFetcher(client).Fetch(test.Context(), reference); err != nil || snapshot == nil || requests.Load() != 2 {
			test.Fatal("one fetcher's cooldown blocked an independent instance")
		}
		time.Sleep(119 * time.Second)
		if _, err := fetcher.Fetch(test.Context(), reference); !errors.Is(err, ErrArchiveRateLimited) || requests.Load() != 2 {
			test.Fatal("HTTP-date Retry-After did not prevent an early network request")
		}
		time.Sleep(time.Second)
		if snapshot, err := fetcher.Fetch(test.Context(), reference); err != nil || snapshot == nil || len(snapshot.Skills) != 1 || requests.Load() != 3 {
			test.Fatal("date-based cooldown did not admit a real successful download at expiry")
		}
	})
}
