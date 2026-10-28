package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
)

func TestSchedulerCanceledAttemptRotatesAfterRestart(test *testing.T) {
	fixture := newFixture(test)
	snapshot := archiveFixture(test)
	entered := make(chan struct{})
	var attempted []string
	fetcher := fetchFunc(func(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		attempted = append(attempted, reference.NameWithOwner)
		if len(attempted) == 1 {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return snapshot, nil
	})
	worker := newScheduler(test, fixture, fetcher, Options{MaxCollections: 1})
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- worker.RunOnce(ctx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		test.Fatal("first repository download did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			test.Fatalf("cancelled attempt did not report cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("cancelled download did not exit")
	}
	collected, err := fixture.store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil || len(collected) != 0 {
		test.Fatalf("cancelled attempt was marked collected: count=%d, error=%v", len(collected), err)
	}
	if err := worker.Close(); err != nil {
		test.Fatal(err)
	}
	worker = newScheduler(test, fixture, fetcher, Options{MaxCollections: 1})
	if err := worker.RunOnce(test.Context()); err != nil {
		test.Fatal(err)
	}
	if len(attempted) != 2 || attempted[0] != "org/skill-0" || attempted[1] != "org/skill-2" {
		test.Fatalf("cancelled attempt starved the next source after restart: %v", attempted)
	}
	collected, err = fixture.store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil || len(collected) != 1 || collected[0].NameWithOwner != "org/skill-2" {
		test.Fatalf("next public repository was not collected: count=%d, error=%v", len(collected), err)
	}
}
