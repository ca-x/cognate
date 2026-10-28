package sourcerefresh

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

type workerFetcher func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error)

func (fetch workerFetcher) Fetch(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
	return fetch(ctx, reference)
}

func workerFixture(test *testing.T, fetch workerFetcher) (*Worker, *state.Store, *library.Service) {
	test.Helper()
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
	for _, name := range []string{"org/first", "org/second"} {
		reference, _ := repository.ParseReference(name, "")
		if _, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("a", 64), []repository.ArchivedSkill{{Path: "skills/chosen", Name: "Chosen", Description: "Synthetic", ContentHash: strings.Repeat("b", 64)}}); err != nil {
			test.Fatal(err)
		}
	}
	cache, err := library.OpenArchiveCache(filepath.Join(directory, "cache"))
	if err != nil {
		test.Fatal(err)
	}
	service, err := library.NewService(store, cache, fetch)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	worker, err := New(store, service, Options{DataDirectory: directory, MaxOwners: 1})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = worker.Close() })
	return worker, store, service
}

func TestWorkerRotatesFailedSourcesWithoutCredentials(test *testing.T) {
	var requested []string
	worker, _, _ := workerFixture(test, func(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		requested = append(requested, reference.NameWithOwner)
		return nil, repository.ErrArchiveUnavailable
	})
	for range 2 {
		if err := worker.RunOnce(test.Context()); !errors.Is(err, repository.ErrArchiveUnavailable) {
			test.Fatalf("source was not attempted: %v", err)
		}
	}
	if len(requested) != 2 || requested[0] == requested[1] {
		test.Fatal("failed first source starved the next source")
	}
}

func TestWorkerFirstTickIsDelayed(test *testing.T) {
	worker, _, _ := workerFixture(test, func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
		test.Error("work ran before the first interval")
		return nil, repository.ErrArchiveUnavailable
	})
	synctest.Test(test, func(test *testing.T) {
		if err := worker.Start(test.Context()); err != nil {
			test.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(DefaultInterval - time.Second)
		synctest.Wait()
		if err := worker.Start(test.Context()); !errors.Is(err, ErrStarted) {
			test.Fatal("duplicate start accepted")
		}
		if err := worker.Close(); err != nil {
			test.Fatal(err)
		}
	})
}

func TestWorkerCloseCancelsDrainsAndSharedLockRejectsOverlap(test *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	worker, store, service := workerFixture(test, func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return nil, ctx.Err()
	})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	finished := make(chan error, 1)
	go func() { finished <- worker.RunOnce(test.Context()) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		test.Fatal("worker did not fetch")
	}
	if err := worker.RunOnce(test.Context()); !errors.Is(err, ErrBusy) {
		test.Fatal("overlapping work accepted")
	}
	peer, err := New(store, service, worker.options)
	if err != nil {
		test.Fatal(err)
	}
	defer peer.Close()
	if err := peer.RunOnce(test.Context()); !errors.Is(err, ErrBusy) {
		test.Fatal("shared data directory lock was bypassed")
	}
	closed := make(chan struct{})
	go func() { _ = worker.Close(); close(closed) }()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		test.Fatal("close did not cancel source")
	}
	select {
	case <-closed:
		test.Fatal("close returned before active fetch exited")
	default:
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		test.Fatal("close did not drain")
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		test.Fatal("cancellation not propagated")
	}
	if err := worker.RunOnce(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("work accepted after close")
	}
	if err := worker.Start(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("start accepted after close")
	}
}

func TestWorkerTimeoutAdvancesOnlyAttemptedSources(test *testing.T) {
	var requested []string
	worker, store, service := workerFixture(test, func(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		requested = append(requested, reference.NameWithOwner)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	options := worker.options
	options.Timeout = 20 * time.Millisecond
	bounded, err := New(store, service, options)
	if err != nil {
		test.Fatal(err)
	}
	defer bounded.Close()
	for range 2 {
		if err := bounded.RunOnce(test.Context()); !errors.Is(err, context.DeadlineExceeded) {
			test.Fatalf("batch did not time out: %v", err)
		}
	}
	if len(requested) != 2 || requested[0] == requested[1] {
		test.Fatal("timeout starved the next source")
	}
}

func TestWorkerPartialTimeoutDoesNotSkipRemainingOwners(test *testing.T) {
	var requested []string
	worker, store, service := workerFixture(test, func(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		requested = append(requested, reference.NameWithOwner)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	for _, owner := range []string{"bob", "carol"} {
		if err := store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
		if _, err := store.CollectRepository(test.Context(), owner, repository.Reference{NameWithOwner: "org/" + owner}, strings.Repeat("a", 64), []repository.ArchivedSkill{{Path: "skills/chosen", Name: "Chosen", Description: "Synthetic", ContentHash: strings.Repeat("b", 64)}}); err != nil {
			test.Fatal(err)
		}
	}
	options := worker.options
	options.Timeout, options.MaxOwners = 20*time.Millisecond, 3
	bounded, err := New(store, service, options)
	if err != nil {
		test.Fatal(err)
	}
	defer bounded.Close()
	for range 3 {
		if err := bounded.RunOnce(test.Context()); !errors.Is(err, context.DeadlineExceeded) {
			test.Fatalf("partial batch did not time out: %v", err)
		}
	}
	if len(requested) != 3 || requested[1] != "org/bob" || requested[2] != "org/carol" {
		test.Fatal("timeout skipped unattempted owners")
	}
}

func TestWorkerOptionBoundsAndCanceledCalls(test *testing.T) {
	worker, store, service := workerFixture(test, func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
		test.Fatal("canceled call fetched")
		return nil, nil
	})
	for _, change := range []func(*Options){
		func(options *Options) { options.Interval = -time.Second },
		func(options *Options) { options.Interval = time.Millisecond },
		func(options *Options) { options.Interval = 25 * time.Hour },
		func(options *Options) { options.Timeout = -time.Second },
		func(options *Options) { options.Timeout = 3 * time.Minute },
		func(options *Options) { options.MaxOwners = 17 },
		func(options *Options) { options.DataDirectory = "relative" },
	} {
		options := worker.options
		change(&options)
		if unexpected, err := New(store, service, options); err == nil {
			unexpected.Close()
			test.Fatal("invalid options accepted")
		}
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if err := worker.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		test.Fatal("canceled batch accepted")
	}
	if err := worker.Start(ctx); !errors.Is(err, context.Canceled) {
		test.Fatal("canceled start accepted")
	}
}
