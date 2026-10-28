package state_test

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/state"
)

func TestStageIdentityPersistsAndCannotBeReplaced(t *testing.T) {
	directory := t.TempDir()
	database := filepath.Join(directory, "client.db")
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "install", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{
		{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-demo"},
		{Path: filepath.Join(directory, "projection"), Kind: "symlink"},
	}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	identity := "linux-dev-ino-v1:7:42"
	if err := store.RecordStageIdentity(t.Context(), plan.ID, 0, identity); err == nil {
		t.Fatal("recorded identity for planned operation")
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "other", "linux-dev-ino-v1:07:42", "linux-dev-ino-v1:7:0"} {
		if err := store.RecordStageIdentity(t.Context(), plan.ID, 0, invalid); err == nil {
			t.Fatalf("accepted identity %q", invalid)
		}
	}
	if err := store.RecordStageIdentity(t.Context(), plan.ID, 1, identity); err == nil {
		t.Fatal("recorded identity for target without a stage")
	}
	for range 2 {
		if err := store.RecordStageIdentity(t.Context(), plan.ID, 0, identity); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordStageIdentity(t.Context(), plan.ID, 0, "linux-dev-ino-v1:7:43"); err == nil {
		t.Fatal("replaced recorded identity")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || len(entry.Plan.Targets) != 2 || entry.Plan.Targets[0].StageIdentity != identity {
		t.Fatalf("operation = %+v, error = %v", entry, err)
	}
	if err := store.MarkOperationNeedsAttention(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStageIdentity(t.Context(), plan.ID, 0, identity); err == nil {
		t.Fatal("recorded identity for operation needing attention")
	}
}

func TestOpenRejectsPendingTargetPositionGap(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "client.db")
	store, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "gap", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-demo"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `UPDATE operation_targets SET position = 3`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := state.Open(t.Context(), databasePath); err == nil {
		_ = reopened.Close()
		t.Fatal("accepted pending operation with target position gap")
	}
}

func TestOpenRejectsInvalidPendingStageIdentity(t *testing.T) {
	for _, identity := range []string{"invalid", "linux-dev-ino-v1:7:42"} {
		t.Run(identity, func(t *testing.T) {
			directory := t.TempDir()
			databasePath := filepath.Join(directory, "client.db")
			store, err := state.Open(t.Context(), databasePath)
			if err != nil {
				t.Fatal(err)
			}
			plan := state.OperationPlan{ID: "planned", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-demo"}}}
			if err := store.BeginOperation(t.Context(), plan); err != nil {
				t.Fatal(err)
			}
			if identity == "invalid" {
				if err := store.StartOperation(t.Context(), plan.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			database, err := sql.Open("sqlite3", databasePath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.ExecContext(t.Context(), `UPDATE operation_targets SET stage_identity = ?`, identity); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if reopened, err := state.Open(t.Context(), databasePath); err == nil {
				_ = reopened.Close()
				t.Fatal("accepted invalid pending stage identity")
			}
		})
	}
}

func TestStageIdentityFailureDoesNotChangeJournal(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "client.db")
	store, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "failure", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-demo"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `CREATE TRIGGER reject_identity BEFORE UPDATE OF stage_identity ON operation_targets BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.RecordStageIdentity(t.Context(), plan.ID, 0, "linux-dev-ino-v1:7:42"); err == nil {
		t.Fatal("injected write failure did not fail")
	}
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Status != "active" || entry.Plan.Targets[0].StageIdentity != "" {
		t.Fatalf("operation = %+v, error = %v", entry, err)
	}
	plan.ID = "conflict"
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("failed write released pending claims")
	}
}

func TestConcurrentStageIdentityHasOneWinner(t *testing.T) {
	directory := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	plan := state.OperationPlan{ID: "race", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-race"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, identity := range []string{"linux-dev-ino-v1:7:42", "linux-dev-ino-v1:7:43"} {
		workers.Go(func() { <-start; results <- store.RecordStageIdentity(t.Context(), plan.ID, 0, identity) })
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("got %d identity winners", winners)
	}
}
