package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/sourcerefresh"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestAppSourceRefreshConfiguration(test *testing.T) {
	for _, scenario := range []struct {
		name           string
		interval       *time.Duration
		valid, enabled bool
	}{
		{"default", nil, true, true},
		{"disabled", new(time.Duration(0)), true, false},
		{"minimum", new(time.Second), true, true},
		{"maximum", new(24 * time.Hour), true, true},
		{"negative", new(-time.Second), false, false},
		{"too-short", new(time.Millisecond), false, false},
		{"too-long", new(24*time.Hour + time.Second), false, false},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			base := test.TempDir()
			if err := os.Chmod(base, 0o700); err != nil {
				test.Fatal(err)
			}
			key := filepath.Join(base, "master.key")
			if err := os.WriteFile(key, make([]byte, 32), 0o600); err != nil {
				test.Fatal(err)
			}
			app, err := Open(test.Context(), Config{DataDirectory: filepath.Join(base, "data"), MasterKeyFile: key, SourceRefreshInterval: scenario.interval})
			if err != nil {
				if scenario.valid {
					test.Fatal(err)
				}
				return
			}
			defer app.Close()
			if !scenario.valid {
				test.Fatal("invalid interval accepted")
			}
			if (app.sourceRefresh != nil) != scenario.enabled {
				test.Fatal("source worker enablement did not match configuration")
			}
		})
	}
}

type appRefreshFetcher func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error)

func (fetch appRefreshFetcher) Fetch(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
	return fetch(ctx, reference)
}

func TestAppSourceRefreshCancellationDrainsBeforeDependencies(test *testing.T) {
	for _, shutdown := range []string{"serve-cancel", "close"} {
		test.Run(shutdown, func(test *testing.T) {
			directory := test.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = store.Close() })
			if err := store.CreateUser(test.Context(), "alice"); err != nil {
				test.Fatal(err)
			}
			if _, err := store.CollectRepository(test.Context(), "alice", repository.Reference{NameWithOwner: "org/source"}, strings.Repeat("a", 64), []repository.ArchivedSkill{{Path: "skills/demo", Name: "Demo", Description: "Synthetic", ContentHash: strings.Repeat("b", 64)}}); err != nil {
				test.Fatal(err)
			}
			cache, err := library.OpenArchiveCache(filepath.Join(directory, "objects"))
			if err != nil {
				test.Fatal(err)
			}
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			collection, err := library.NewService(store, cache, appRefreshFetcher(func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release
				return nil, ctx.Err()
			}))
			if err != nil {
				test.Fatal(err)
			}
			defer collection.Close()
			worker, err := sourcerefresh.New(store, collection, sourcerefresh.Options{DataDirectory: directory, Interval: time.Second})
			if err != nil {
				test.Fatal(err)
			}
			defer worker.Close()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			app := &App{store: store, library: collection, sourceRefresh: worker, handler: http.NotFoundHandler(), allowHTTP: true}
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			finished := make(chan error, 1)
			if shutdown == "serve-cancel" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					test.Fatal(err)
				}
				go func() { finished <- app.Serve(ctx, listener) }()
			} else {
				if err := worker.Start(ctx); err != nil {
					test.Fatal(err)
				}
			}
			select {
			case <-entered:
			case <-time.After(4 * time.Second):
				test.Fatal("App did not start periodic refresh")
			}
			if shutdown == "serve-cancel" {
				cancel()
			} else {
				go func() { finished <- app.Close() }()
			}
			select {
			case <-canceled:
			case <-time.After(3 * time.Second):
				test.Fatal("App shutdown did not cancel source work")
			}
			select {
			case <-finished:
				test.Fatal("App returned before source exited")
			default:
			}
			if _, err := store.ListCollectedRepositories(test.Context(), "alice"); err != nil {
				test.Fatal("App closed storage before source drained")
			}
			close(release)
			select {
			case err := <-finished:
				if err != nil {
					test.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				test.Fatal("App did not finish after source exit")
			}
			if err := worker.RunOnce(test.Context()); !errors.Is(err, sourcerefresh.ErrClosed) {
				test.Fatal("App shutdown left worker usable")
			}
		})
	}
}
