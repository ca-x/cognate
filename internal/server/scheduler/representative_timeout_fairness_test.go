package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
)

func TestRepresentativeTimeoutDoesNotStarveNextRepository(t *testing.T) {
	fixture := newFixture(t)
	snapshot := archiveFixture(t)
	var attempted []string
	fetcher := fetchFunc(func(ctx context.Context, reference repository.Reference) (*repository.ArchiveSnapshot, error) {
		attempted = append(attempted, reference.NameWithOwner)
		if reference.NameWithOwner == "org/skill-0" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return snapshot, nil
	})
	worker := newScheduler(t, fixture, fetcher, Options{Timeout: 200 * time.Millisecond, MaxCollections: 1})
	if err := worker.RunOnce(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first archive must exhaust the run deadline: %v", err)
	}
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	worker = newScheduler(t, fixture, fetcher, Options{Timeout: 200 * time.Millisecond, MaxCollections: 1})
	secondErr := worker.RunOnce(t.Context())
	if len(attempted) != 2 || attempted[0] != "org/skill-0" || attempted[1] != "org/skill-2" {
		t.Fatalf("deadline-exhausting archive starved the next repository across restart: attempted=%v, second error=%v", attempted, secondErr)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	collected, err := fixture.store.ListCollectedRepositories(t.Context(), "alice")
	if err != nil || len(collected) != 1 || collected[0].NameWithOwner != "org/skill-2" {
		t.Fatalf("next public repository was not collected: count=%d, error=%v", len(collected), err)
	}
}
