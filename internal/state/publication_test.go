package state_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/state"
)

func TestPublicationProgressRequiresReadyIdentityAndHash(t *testing.T) {
	directory := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hash := strings.Repeat("a", 64)
	identity := "linux-dev-ino-v1:7:42"
	plan := state.OperationPlan{ID: "publish", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-publish"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStageIdentity(t.Context(), plan.ID, 0, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginPublish(t.Context(), plan.ID, 0, identity, hash); err == nil {
		t.Fatal("published stage before it was ready")
	}
	if err := store.MarkPublished(t.Context(), plan.ID, 0, identity, hash); err == nil {
		t.Fatal("published without an intent")
	}
	if err := store.MarkStageReady(t.Context(), plan.ID, 0, identity, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginPublish(t.Context(), plan.ID, 0, identity, strings.Repeat("b", 64)); err == nil {
		t.Fatal("accepted a different content hash")
	}
	if err := store.BeginPublish(t.Context(), plan.ID, 0, identity, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginPublish(t.Context(), plan.ID, 0, identity, hash); err == nil {
		t.Fatal("accepted duplicate publish intent")
	}
	if err := store.MarkPublished(t.Context(), plan.ID, 0, "linux-dev-ino-v1:7:43", hash); err == nil {
		t.Fatal("accepted a different stage identity")
	}
	if err := store.MarkPublished(t.Context(), plan.ID, 0, identity, hash); err != nil {
		t.Fatal(err)
	}
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || !entry.Plan.Targets[0].PublishIntent || !entry.Plan.Targets[0].Published {
		t.Fatalf("operation = %+v, error = %v", entry, err)
	}
}

func TestOpenRejectsPublishedWithoutIntent(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "client.db")
	store, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "bad", Action: "install", Slug: "demo", ContentHash: strings.Repeat("a", 64), Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-bad"}}}
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
	if _, err := database.ExecContext(t.Context(), `UPDATE operation_targets SET published = 1`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := state.Open(t.Context(), databasePath); err == nil {
		_ = reopened.Close()
		t.Fatal("accepted published target without intent")
	}
}
