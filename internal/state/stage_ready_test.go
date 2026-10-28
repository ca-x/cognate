package state_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/state"
)

func TestStageReadyRequiresActiveIdentityAndPlannedHash(t *testing.T) {
	directory := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hash := strings.Repeat("a", 64)
	identity := "linux-dev-ino-v1:7:42"
	plan := state.OperationPlan{ID: "ready", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-ready"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkStageReady(t.Context(), plan.ID, 0, identity, hash); err == nil {
		t.Fatal("marked an unstarted stage ready")
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkStageReady(t.Context(), plan.ID, 0, identity, hash); err == nil {
		t.Fatal("marked a stage without recorded identity ready")
	}
	if err := store.RecordStageIdentity(t.Context(), plan.ID, 0, identity); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []struct{ identity, hash string }{
		{"linux-dev-ino-v1:7:43", hash},
		{identity, strings.Repeat("b", 64)},
		{identity, "invalid"},
	} {
		if err := store.MarkStageReady(t.Context(), plan.ID, 0, attempt.identity, attempt.hash); err == nil {
			t.Fatal("accepted mismatched identity or hash")
		}
	}
	if err := store.MarkStageReady(t.Context(), plan.ID, 0, identity, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkStageReady(t.Context(), plan.ID, 0, identity, hash); err == nil {
		t.Fatal("accepted repeated ready transition")
	}
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || !entry.Plan.Targets[0].StageReady || entry.Plan.Targets[0].StageIdentity != identity {
		t.Fatalf("operation = %+v, error = %v", entry, err)
	}
}

func TestOpenRejectsReadyStageWithoutIdentity(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "client.db")
	store, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "corrupt", Action: "install", Slug: "demo", ContentHash: strings.Repeat("a", 64), Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-ready"}}}
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
	if _, err := database.ExecContext(t.Context(), `UPDATE operation_targets SET stage_ready = 1`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := state.Open(t.Context(), databasePath); err == nil {
		_ = reopened.Close()
		t.Fatal("accepted ready stage without identity")
	}
}
