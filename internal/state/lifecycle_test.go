package state_test

import (
	"database/sql"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/state"
)

func TestPreviousJournalSchemaCanCancelPendingOperation(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "client.db")
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE operations (id INTEGER PRIMARY KEY, operation_key TEXT NOT NULL UNIQUE, action TEXT NOT NULL, slug TEXT NOT NULL, content_hash TEXT NOT NULL, status TEXT NOT NULL)`,
		`CREATE TABLE operation_targets (id INTEGER PRIMARY KEY, position INTEGER NOT NULL, path TEXT NOT NULL, path_key TEXT NOT NULL, kind TEXT NOT NULL, operation_id INTEGER NOT NULL REFERENCES operations(id))`,
		`INSERT INTO operations (id, operation_key, action, slug, content_hash, status) VALUES (1, 'old', 'install', 'demo', 'hash', 'planned')`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(resolvedRoot, "demo")
	key := path
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	if _, err := database.ExecContext(t.Context(), `INSERT INTO operation_targets (position, path, path_key, kind, operation_id) VALUES (0, ?, ?, 'canonical', 1)`, path, key); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.CancelPlannedOperation(t.Context(), "old"); err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "new", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: path, Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatalf("claims not released after migrating old journal: %v", err)
	}
}

func TestCancelRollsBackStatusWhenClaimReleaseFails(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	plan := state.OperationPlan{ID: "first", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical", StageName: ".skillhub-stage-first"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `CREATE TRIGGER reject_claim_delete BEFORE DELETE ON operation_claims BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelPlannedOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("expected claim release failure")
	}
	operation, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || operation.Status != "planned" {
		t.Fatalf("operation = %+v, error = %v", operation, err)
	}
	plan.ID = "second"
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("failed cancellation released claims")
	}
}

func TestCancelPlannedOperationReleasesClaims(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "first", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical", StageName: ".skillhub-stage-first"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelPlannedOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelPlannedOperation(t.Context(), plan.ID); err != nil {
		t.Fatalf("repeated cancellation failed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	operation, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || operation.Status != "cancelled" {
		t.Fatalf("operation = %+v, error = %v", operation, err)
	}
	pending, err := store.PendingOperations(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	plan.ID = "second"
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatalf("cancelled operation still reserves resources: %v", err)
	}
}

func TestAttentionStateRetainsClaimsAcrossRestart(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "first", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkOperationNeedsAttention(t.Context(), plan.ID); err == nil {
		t.Fatal("unstarted operation should not become active failure")
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.MarkOperationNeedsAttention(t.Context(), plan.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	operation, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || operation.Status != "needs_attention" {
		t.Fatalf("operation = %+v, error = %v", operation, err)
	}
	plan.ID = "second"
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("operation needing attention lost its claims")
	}
}

func TestStartedOperationCannotBeCancelledAsUnstarted(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	plan := state.OperationPlan{ID: "first", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelPlannedOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("cancelled a started operation")
	}
	plan.ID = "second"
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("started operation lost its claims")
	}
}

func TestStartAndCancelRaceHasOneWinner(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	plan := state.OperationPlan{ID: "race", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Go(func() { <-start; results <- store.StartOperation(t.Context(), plan.ID) })
	workers.Go(func() { <-start; results <- store.CancelPlannedOperation(t.Context(), plan.ID) })
	close(start)
	workers.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("got %d winners, want one", succeeded)
	}
	operation, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || (operation.Status != "active" && operation.Status != "cancelled") {
		t.Fatalf("operation = %+v, error = %v", operation, err)
	}
}
