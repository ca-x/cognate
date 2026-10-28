package state_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/state"
)

func TestProjectionPublicationRequiresPublishedCanonical(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hash, identity := strings.Repeat("a", 64), "linux-dev-ino-v1:7:42"
	plan := state.OperationPlan{ID: "install", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical", StageName: ".skillhub-stage-demo"}, {Path: filepath.Join(root, "agent-demo"), Kind: "symlink"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginProjection(t.Context(), plan.ID, 1); err == nil {
		t.Fatal("accepted projection before canonical publication")
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
	if err := store.MarkProjectionPublished(t.Context(), plan.ID, 1, "linux-dev-ino-v1:7:43"); err == nil {
		t.Fatal("accepted result without intent")
	}
	if err := store.BeginProjection(t.Context(), plan.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginProjection(t.Context(), plan.ID, 1); err == nil {
		t.Fatal("accepted duplicate intent")
	}
	if err := store.MarkProjectionPublished(t.Context(), plan.ID, 1, "linux-dev-ino-v1:7:43"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkProjectionPublished(t.Context(), plan.ID, 1, "linux-dev-ino-v1:7:43"); err == nil {
		t.Fatal("accepted duplicate result")
	}
	db, err := sql.Open("sqlite3", filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_projection BEFORE INSERT ON projections WHEN NEW.kind = 'symlink' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInstallation(t.Context(), plan.ID); err == nil {
		t.Fatal("completed with a failed projection record")
	}
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Status != "active" {
		t.Fatalf("entry = %+v, error = %v", entry, err)
	}
	if _, err := store.Get(t.Context(), plan.Slug); err == nil {
		t.Fatal("partial installation persisted")
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER reject_projection`); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInstallation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	record, err := store.Get(t.Context(), plan.Slug)
	if err != nil || len(record.Projections) != 2 {
		t.Fatalf("record = %+v, error = %v", record, err)
	}
}
