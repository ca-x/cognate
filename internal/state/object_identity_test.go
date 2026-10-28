package state_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/state"
)

func TestLegacyPublishedWithoutObjectIdentityIsNotCompleted(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	hash, identity := strings.Repeat("a", 64), "linux-dev-ino-v1:7:42"
	plan := state.OperationPlan{ID: "legacy", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical", StageName: ".skillhub-stage-legacy"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStageIdentity(t.Context(), plan.ID, 0, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkStageReady(t.Context(), plan.ID, 0, identity, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginPublish(t.Context(), plan.ID, 0, identity, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkPublished(t.Context(), plan.ID, 0, identity, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE operation_targets SET object_identity = ''`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CompleteInstallation(t.Context(), plan.ID); err == nil {
		t.Fatal("completed legacy operation without object identity")
	}
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Status != "active" || entry.Plan.Targets[0].ObjectIdentity != "" {
		t.Fatalf("entry = %+v, error = %v", entry, err)
	}
	plan.ID = "another"
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("released legacy operation claims")
	}
}

func TestOpenRejectsContradictoryObjectIdentity(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "bad", Action: "install", Slug: "demo", ContentHash: strings.Repeat("a", 64), Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical", StageName: ".skillhub-stage-bad"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE operation_targets SET object_identity = 'linux-dev-ino-v1:7:42'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := state.Open(t.Context(), database); err == nil {
		_ = reopened.Close()
		t.Fatal("accepted identity without published result")
	}
}
