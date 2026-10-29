package scheduler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/ca-x/cognate/internal/server/state"
)

func initialListsFixture(test *testing.T, storeManaged, cacheManaged bool, foreign bool) (*fixture, string) {
	test.Helper()
	if storeManaged || cacheManaged {
		if err := maintenance.CheckPlatform(test.Context()); errors.Is(err, maintenance.ErrUnsupported) {
			test.Skip("managed instance unsupported")
		}
	}
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), state.Options{MaintenanceManaged: storeManaged})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	cacheParent := directory
	if foreign {
		cacheParent = test.TempDir()
		if err := os.Chmod(cacheParent, 0o700); err != nil {
			test.Fatal(err)
		}
	}
	cache, err := library.OpenArchiveCacheWithOptions(test.Context(), filepath.Join(cacheParent, "archives"), library.CacheOptions{MaintenanceManaged: cacheManaged})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = cache.Close() })
	fixture := &fixture{store: store, cache: cache, updatedAt: "2026-01-01T00:00:00Z"}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		response, err := fixture.respond(request)
		if err != nil {
			http.Error(writer, "synthetic Lists failure", http.StatusBadRequest)
			return
		}
		defer response.Body.Close()
		writer.WriteHeader(response.StatusCode)
		_, _ = io.Copy(writer, response.Body)
	}))
	test.Cleanup(server.Close)
	fixture.client = &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.URL.Scheme, clone.URL.Host = "http", strings.TrimPrefix(server.URL, "http://")
		return server.Client().Transport.RoundTrip(clone)
	})}
	fixture.addUser(test, "alice")
	return fixture, directory
}

func initialListsWait(test *testing.T, signal <-chan struct{}) {
	test.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		test.Fatal("initial Lists barrier was not reached")
	}
}

func initialListsResult(test *testing.T, result <-chan error) error {
	test.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		test.Fatal("initial Lists operation did not drain")
		return nil
	}
}

func TestManagedInitialListsPair(test *testing.T) {
	for _, pairing := range []struct {
		name                                      string
		store, cache, foreign, replaced, rejected bool
	}{
		{name: "default"},
		{name: "same-physical-peer", store: true, cache: true},
		{name: "store-only", store: true, rejected: true},
		{name: "cache-only", cache: true, rejected: true},
		{name: "foreign-root", store: true, cache: true, foreign: true, rejected: true},
		{name: "replaced-cache", store: true, cache: true, replaced: true, rejected: true},
	} {
		test.Run(pairing.name, func(test *testing.T) {
			fixture, directory := initialListsFixture(test, pairing.store, pairing.cache, pairing.foreign)
			if pairing.replaced {
				if err := os.Rename(filepath.Join(directory, "archives"), filepath.Join(directory, "old-archives")); err != nil {
					test.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(directory, "archives"), 0o700); err != nil {
					test.Fatal(err)
				}
			}
			snapshot := archiveFixture(test)
			worker, err := New(fixture.store, fixture.cache, fetchFunc(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) { return snapshot, nil }), fixture.client, Options{})
			if worker != nil {
				defer worker.Close()
			}
			if pairing.rejected {
				if err == nil || worker != nil {
					test.Fatal("scheduler accepted invalid maintenance peers")
				}
			} else if err != nil {
				test.Fatal(err)
			}
			if err := fixture.store.CreateUser(test.Context(), "still-owned"); err != nil {
				test.Fatalf("constructor closed caller Store: %v", err)
			}
			if !pairing.replaced {
				if err := fixture.cache.Put(test.Context(), snapshot.Hash, snapshot.Data); err != nil {
					test.Fatalf("constructor closed caller cache: %v", err)
				}
			}
		})
	}
}

func TestManagedInitialListsNetworkOutsideScope(test *testing.T) {
	fixture, directory := initialListsFixture(test, true, true, false)
	snapshot := archiveFixture(test)
	entered, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(entered)
		select {
		case <-release:
			_, _ = writer.Write(snapshot.Data)
		case <-request.Context().Done():
		}
	}))
	test.Cleanup(server.Close)
	client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "codeload.github.com" || request.Header.Get("Authorization") != "" {
			return nil, errors.New("unexpected archive fixture request")
		}
		clone := request.Clone(request.Context())
		clone.URL.Scheme, clone.URL.Host = "http", strings.TrimPrefix(server.URL, "http://")
		return server.Client().Transport.RoundTrip(clone)
	})}
	worker := newScheduler(test, fixture, repository.NewArchiveFetcher(client), Options{MaxCollections: 1})
	instance, err := maintenance.Join(test.Context(), directory)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = instance.Close() })
	finished := make(chan error, 1)
	go func() { finished <- worker.RunOnce(ctx) }()
	initialListsWait(test, entered)
	bounded, stop := context.WithTimeout(test.Context(), time.Second)
	defer stop()
	exclusive, err := instance.Quiesce(bounded)
	if err != nil {
		cancel()
		close(release)
		_ = initialListsResult(test, finished)
		test.Fatalf("network held maintenance lease: %v", err)
	}
	defer exclusive.Release()
	close(release)
	if err := initialListsResult(test, finished); err == nil {
		test.Fatal("publication entered during maintenance Exclusive")
	}
	entries, err := fixture.store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil || len(entries) != 0 {
		test.Fatal("network response during Exclusive changed repository authority")
	}
	archives, err := filepath.Glob(filepath.Join(directory, "archives", "*.tar.gz"))
	if err != nil || len(archives) != 0 {
		test.Fatal("rejected publication created an archive")
	}
	stages, err := filepath.Glob(filepath.Join(directory, "archives", ".stage-*"))
	if err != nil || len(stages) != 0 {
		test.Fatal("rejected publication created a stage")
	}
}

func TestManagedInitialListsRoundTrip(test *testing.T) {
	for _, managed := range []bool{false, true} {
		name := "default"
		if managed {
			name = "managed"
		}
		test.Run(name, func(test *testing.T) {
			fixture, directory := initialListsFixture(test, managed, managed, false)
			snapshot := archiveFixture(test)
			fetches := 0
			worker := newScheduler(test, fixture, fetchFunc(func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
				fetches++
				return snapshot, nil
			}), Options{})
			for range 2 {
				if err := worker.RunOnce(test.Context()); err != nil {
					test.Fatal(err)
				}
			}
			entries, err := fixture.store.ListCollectedRepositories(test.Context(), "alice")
			if err != nil || len(entries) != 2 || fetches != 2 {
				test.Fatalf("wrong collection/fetch counts: %d/%d %v", len(entries), fetches, err)
			}
			for _, entry := range entries {
				if entry.Manual || entry.ArchiveHash != snapshot.Hash {
					test.Fatal("changed Lists collection semantics")
				}
				if err := fixture.cache.Verify(test.Context(), entry.ArchiveHash); err != nil {
					test.Fatal(err)
				}
			}
			initialListsProcess(test, directory, managed, managed)
			if err := worker.Close(); err != nil {
				test.Fatal(err)
			}
			if err := fixture.store.Close(); err != nil {
				test.Fatal(err)
			}
			if managed {
				initialListsProcess(test, directory, true, true)
			}
			if err := fixture.cache.Close(); err != nil {
				test.Fatal(err)
			}
			initialListsProcess(test, directory, managed, false)
		})
	}
}

func initialListsProcess(test *testing.T, directory string, managed, blocked bool) {
	test.Helper()
	command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestManagedInitialListsProcess$")
	mode := "default"
	if managed {
		mode = "managed"
	}
	want := "open"
	if blocked {
		want = "blocked"
	}
	command.Env = append(os.Environ(), "COGNATE_INITIAL_LISTS_PROBE="+directory, "COGNATE_INITIAL_LISTS_MODE="+mode, "COGNATE_INITIAL_LISTS_EXPECT="+want)
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("cooperating process failed: %v %s", err, output)
	}
}

func TestManagedInitialListsProcess(test *testing.T) {
	directory := os.Getenv("COGNATE_INITIAL_LISTS_PROBE")
	if directory == "" {
		return
	}
	budget := 3 * time.Second
	if os.Getenv("COGNATE_INITIAL_LISTS_EXPECT") == "blocked" {
		budget = 250 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(test.Context(), budget)
	defer cancel()
	store, err := state.OpenWithOptions(ctx, filepath.Join(directory, "server.db"), make([]byte, 32), state.Options{MaintenanceManaged: os.Getenv("COGNATE_INITIAL_LISTS_MODE") == "managed"})
	if store != nil {
		defer store.Close()
	}
	if os.Getenv("COGNATE_INITIAL_LISTS_EXPECT") == "blocked" {
		if store != nil || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, maintenance.ErrLock) {
			test.Fatalf("managed process lock not retained: %v", err)
		}
	} else if err != nil {
		test.Fatal(err)
	}
}

func TestManagedInitialListsClose(test *testing.T) {
	fixture, _ := initialListsFixture(test, true, true, false)
	entered, exited := make(chan struct{}), make(chan struct{})
	worker := newScheduler(test, fixture, fetchFunc(func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	}), Options{MaxCollections: 1})
	finished := make(chan error, 1)
	go func() { finished <- worker.RunOnce(test.Context()) }()
	initialListsWait(test, entered)
	if err := worker.RunOnce(test.Context()); !errors.Is(err, ErrBusy) {
		test.Fatalf("active scheduler did not stay busy: %v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- worker.Close() }()
	if err := initialListsResult(test, closed); err != nil {
		test.Fatal(err)
	}
	initialListsWait(test, exited)
	if err := initialListsResult(test, finished); !errors.Is(err, context.Canceled) {
		test.Fatalf("close cancellation changed: %v", err)
	}
	if err := worker.RunOnce(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatalf("closed scheduler ran again: %v", err)
	}
	if err := worker.Close(); err != nil {
		test.Fatal(err)
	}
	entries, err := fixture.store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil || len(entries) != 0 {
		test.Fatal("closed fetch published authority")
	}
}
