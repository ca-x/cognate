package state_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/state"
)

func TestCompleteInstallationAtomic(t *testing.T) {
	for _, failure := range []string{"", "installation", "status", "claims"} {
		t.Run(failure, func(t *testing.T) {
			directory := t.TempDir()
			databasePath := filepath.Join(directory, "client.db")
			store, err := state.Open(t.Context(), databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			hash := strings.Repeat("a", 64)
			identity := "linux-dev-ino-v1:7:42"
			plan := state.OperationPlan{ID: "first", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-first"}}}
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
			if failure != "" {
				triggers := map[string]string{
					"installation": `CREATE TRIGGER fail_completion BEFORE INSERT ON installed_skills BEGIN SELECT RAISE(ABORT, 'injected'); END`,
					"status":       `CREATE TRIGGER fail_completion BEFORE UPDATE OF status ON operations WHEN NEW.status = 'completed' BEGIN SELECT RAISE(ABORT, 'injected'); END`,
					"claims":       `CREATE TRIGGER fail_completion BEFORE DELETE ON operation_claims BEGIN SELECT RAISE(ABORT, 'injected'); END`,
				}
				db, err := sql.Open("sqlite3", databasePath)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(t.Context(), triggers[failure]); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = store.CompleteInstallation(t.Context(), plan.ID)
			if (err != nil) != (failure != "") {
				t.Fatalf("completion error = %v", err)
			}
			entry, err := store.GetOperation(t.Context(), plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			record, recordErr := store.Get(t.Context(), plan.Slug)
			if failure == "" {
				if entry.Status != "completed" || recordErr != nil || record.ContentHash != hash || len(record.Projections) != 1 || record.Projections[0].Path != plan.Targets[0].Path || record.Projections[0].Kind != "canonical" {
					t.Fatalf("entry = %+v, record = %+v, error = %v", entry, record, recordErr)
				}
			} else if entry.Status != "active" || recordErr == nil {
				t.Fatalf("partial transaction: entry = %+v, record error = %v", entry, recordErr)
			}
			db, err := sql.Open("sqlite3", databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var claims int
			if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM operation_claims`).Scan(&claims); err != nil {
				t.Fatal(err)
			}
			if (claims == 0) != (failure == "") {
				t.Fatalf("claims = %d", claims)
			}
		})
	}
}

func TestInstallationAndPendingClaimsConflict(t *testing.T) {
	for _, order := range []string{"installed-first", "pending-first"} {
		for _, relation := range []string{"slug", "same", "ancestor", "descendant"} {
			t.Run(order+"/"+relation, func(t *testing.T) {
				root := t.TempDir()
				base := filepath.Join(root, "existing")
				if err := os.Mkdir(base, 0o700); err != nil {
					t.Fatal(err)
				}
				installedPath, pendingPath := base, base
				installedSlug, pendingSlug := "old", "new"
				switch relation {
				case "slug":
					pendingSlug, pendingPath = installedSlug, filepath.Join(root, "other")
				case "ancestor":
					pendingPath = filepath.Join(base, "child")
				case "descendant":
					installedPath = filepath.Join(base, "child")
				}
				store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				record := state.Installation{Slug: installedSlug, ContentHash: "hash", Projections: []state.Projection{{Path: installedPath, Kind: "canonical"}}}
				plan := state.OperationPlan{ID: "pending", Action: "install", Slug: pendingSlug, ContentHash: "hash", Targets: []state.OperationTarget{{Path: pendingPath, Kind: "canonical"}}}
				if order == "installed-first" {
					if err := store.Create(t.Context(), record); err != nil {
						t.Fatal(err)
					}
					if err := store.BeginOperation(t.Context(), plan); err == nil {
						t.Fatal("accepted conflicting plan")
					}
				} else {
					if err := store.BeginOperation(t.Context(), plan); err != nil {
						t.Fatal(err)
					}
					if err := store.Create(t.Context(), record); err == nil {
						t.Fatal("accepted conflicting installation")
					}
				}
			})
		}
	}
}

func TestCompleteInstallationRejectsUnpublishedTargets(t *testing.T) {
	directory := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hash, identity := strings.Repeat("a", 64), "linux-dev-ino-v1:7:42"
	plan := state.OperationPlan{ID: "first", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-first"}, {Path: filepath.Join(directory, "agent-demo"), Kind: "symlink"}}}
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
	if err := store.CompleteInstallation(t.Context(), plan.ID); err == nil {
		t.Fatal("completed while an agent target is unpublished")
	}
	entry, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || entry.Status != "active" {
		t.Fatalf("entry = %+v, error = %v", entry, err)
	}
	if _, err := store.Get(t.Context(), plan.Slug); err == nil {
		t.Fatal("recorded incomplete installation")
	}
	plan.ID = "second"
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("released claims for incomplete installation")
	}
}
