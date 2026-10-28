package install_test

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/ca-x/skillhub/internal/install"
	"github.com/ca-x/skillhub/internal/skill"
	"github.com/ca-x/skillhub/internal/state"
	"github.com/spf13/afero"
)

func publishedWithProjection(t *testing.T) (*install.Session, string, string) {
	return publishedWithProjectionTrigger(t, "")
}

func publishedWithProjectionTrigger(t *testing.T, trigger string) (*install.Session, string, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	root, source := t.TempDir(), t.TempDir()
	if trigger != "" {
		database := filepath.Join(root, "client.db")
		store, err := state.Open(t.Context(), database)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite3", database)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(t.Context(), trigger); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return publishedProjectionInDirectory(t, root, source)
}

func publishedProjectionInDirectory(t *testing.T, root, source string) (*install.Session, string, string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	canonical, projection := filepath.Join(root, "canonical"), filepath.Join(root, "agent")
	plan := state.OperationPlan{ID: "install", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: projection, Kind: "symlink"}, {Path: canonical, Kind: "canonical", StageName: ".skillhub-stage-demo"}}}
	if err := session.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := session.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateStage(t.Context(), plan.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := session.CopyStage(t.Context(), plan.ID, 1, source); err != nil {
		t.Fatal(err)
	}
	if err := session.PublishStage(t.Context(), plan.ID, 1); err != nil {
		t.Fatal(err)
	}
	return session, canonical, projection
}

func TestSessionProjectionSurvivesProcessExit(t *testing.T) {
	if root := os.Getenv("SKILLHUB_PROJECTION_TEST_ROOT"); root != "" {
		source := filepath.Join(root, "source")
		if err := os.Mkdir(source, 0o700); err != nil {
			t.Fatal(err)
		}
		session, _, _ := publishedProjectionInDirectory(t, root, source)
		if err := session.PublishProjection(t.Context(), "install", 0); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("SKILLHUB_PROJECTION_TEST_COMPLETE") == "yes" {
			if err := session.CompleteInstallation(t.Context(), "install"); err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(0)
	}
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	for _, complete := range []string{"no", "yes"} {
		t.Run(complete, func(t *testing.T) {
			root := t.TempDir()
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSessionProjectionSurvivesProcessExit$")
			command.Env = append(os.Environ(), "SKILLHUB_PROJECTION_TEST_ROOT="+root, "SKILLHUB_PROJECTION_TEST_COMPLETE="+complete)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("child failed: %v: %s", err, output)
			}
			link, err := os.Readlink(filepath.Join(root, "agent"))
			if err != nil || link != filepath.Join(root, "canonical") {
				t.Fatalf("link = %q, error = %v", link, err)
			}
			session, err := install.OpenSession(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				pending, err := session.RecoverInterruptedOperations(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if complete == "no" && (len(pending) != 1 || pending[0].Status != "needs_attention" || !pending[0].Plan.Targets[0].Published) {
					t.Fatalf("pending = %+v", pending)
				}
				if complete == "yes" && len(pending) != 0 {
					t.Fatalf("pending = %+v", pending)
				}
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			if complete == "yes" {
				store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				record, err := store.Get(t.Context(), "demo")
				if err != nil || len(record.Projections) != 2 {
					t.Fatalf("record = %+v, error = %v", record, err)
				}
			}
		})
	}
}

func TestSessionProjectionRejectsChangedCanonical(t *testing.T) {
	session, canonical, projection := publishedWithProjection(t)
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := session.PublishProjection(t.Context(), "install", 0); err == nil {
		t.Fatal("accepted changed canonical content")
	}
	if _, err := os.Lstat(projection); !os.IsNotExist(err) {
		t.Fatalf("projection exists: %v", err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestSessionCompletesMultipleProjections(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	root, source := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "multi", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{
		{Path: filepath.Join(root, "claude"), Kind: "symlink"},
		{Path: filepath.Join(root, "canonical"), Kind: "canonical", StageName: ".skillhub-stage-multi"},
		{Path: filepath.Join(root, "codex"), Kind: "symlink"},
	}}
	session, err := install.OpenSession(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := session.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateStage(t.Context(), plan.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := session.CopyStage(t.Context(), plan.ID, 1, source); err != nil {
		t.Fatal(err)
	}
	if err := session.PublishStage(t.Context(), plan.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := session.PublishProjection(t.Context(), plan.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteInstallation(t.Context(), plan.ID); err == nil {
		t.Fatal("completed before second projection")
	}
	if err := session.PublishProjection(t.Context(), plan.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteInstallation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"claude", "codex"} {
		data, err := os.ReadFile(filepath.Join(root, target, "SKILL.md"))
		if err != nil || string(data) != "skill" {
			t.Fatalf("%s: data = %q, error = %v", target, data, err)
		}
	}
}

func TestSessionClosesCompletedOperationHandles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	previous := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previous)
	root, source := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for index := range 12 {
		slug := fmt.Sprintf("demo-%d", index)
		plan := state.OperationPlan{ID: slug, Action: "install", Slug: slug, ContentHash: hash, Targets: []state.OperationTarget{
			{Path: filepath.Join(root, slug), Kind: "canonical", StageName: ".skillhub-stage-" + slug},
			{Path: filepath.Join(root, "link-"+slug), Kind: "symlink"},
		}}
		if err := session.BeginOperation(t.Context(), plan); err != nil {
			t.Fatal(err)
		}
		if err := session.StartOperation(t.Context(), plan.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := session.CreateStage(t.Context(), plan.ID, 0); err != nil {
			t.Fatal(err)
		}
		if err := session.CopyStage(t.Context(), plan.ID, 0, source); err != nil {
			t.Fatal(err)
		}
		if err := session.PublishStage(t.Context(), plan.ID, 0); err != nil {
			t.Fatal(err)
		}
		if err := session.PublishProjection(t.Context(), plan.ID, 1); err != nil {
			t.Fatal(err)
		}
		if err := session.CompleteInstallation(t.Context(), plan.ID); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) > len(before)+3 {
		t.Fatalf("descriptors grew from %d to %d after completed operations", len(before), len(after))
	}
}

func TestSessionProjectionDatabaseFailuresPreserveEvidence(t *testing.T) {
	for _, column := range []string{"publish_intent", "published", "object_identity"} {
		afterCreate := column != "publish_intent"
		t.Run(column, func(t *testing.T) {
			trigger := `CREATE TRIGGER reject_projection BEFORE UPDATE OF ` + column + ` ON operation_targets WHEN NEW.kind = 'symlink' BEGIN SELECT RAISE(ABORT, 'injected'); END`
			session, canonical, projection := publishedWithProjectionTrigger(t, trigger)
			if err := session.PublishProjection(t.Context(), "install", 0); err == nil {
				t.Fatal("projection unexpectedly succeeded")
			}
			if afterCreate {
				link, err := os.Readlink(projection)
				if err != nil || link != canonical {
					t.Fatalf("link = %q, error = %v", link, err)
				}
			} else if _, err := os.Lstat(projection); !os.IsNotExist(err) {
				t.Fatalf("unexpected projection: %v", err)
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := install.OpenSession(t.Context(), filepath.Dir(canonical))
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			for range 2 {
				pending, err := reopened.RecoverInterruptedOperations(t.Context())
				if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || pending[0].Plan.Targets[0].PublishIntent != afterCreate || pending[0].Plan.Targets[0].Published || pending[0].Plan.Targets[0].ObjectIdentity != "" {
					t.Fatalf("pending = %+v, error = %v", pending, err)
				}
			}
		})
	}
}

func TestSessionPublishesProjectionAndCompletes(t *testing.T) {
	session, canonical, projection := publishedWithProjection(t)
	if err := session.CompleteInstallation(t.Context(), "install"); err == nil {
		t.Fatal("completed before projection")
	}
	if err := session.PublishProjection(t.Context(), "install", 0); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(projection)
	if err != nil || link != canonical {
		t.Fatalf("link = %q, error = %v", link, err)
	}
	if err := session.CompleteInstallation(t.Context(), "install"); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(filepath.Dir(canonical), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.Get(t.Context(), "demo")
	if err != nil || len(record.Projections) != 2 {
		t.Fatalf("record = %+v, error = %v", record, err)
	}
}

func TestSessionProjectionConflictPreservesExisting(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			session, canonical, projection := publishedWithProjection(t)
			switch kind {
			case "file":
				if err := os.WriteFile(projection, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(projection, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(canonical, projection); err != nil {
					t.Fatal(err)
				}
			case "dangling":
				if err := os.Symlink(projection+"-absent", projection); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(projection)
			if err != nil {
				t.Fatal(err)
			}
			if err := session.PublishProjection(t.Context(), "install", 0); err == nil {
				t.Fatal("overwrote existing projection")
			}
			after, err := os.Lstat(projection)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("projection changed: %v", err)
			}
			pending, err := session.PendingOperations(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" {
				t.Fatalf("pending = %+v, error = %v", pending, err)
			}
		})
	}
}

func TestSessionRejectsReplacedProjection(t *testing.T) {
	session, canonical, projection := publishedWithProjection(t)
	if err := session.PublishProjection(t.Context(), "install", 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(projection, projection+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canonical, projection); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteInstallation(t.Context(), "install"); err == nil {
		t.Fatal("completed after projection replacement")
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}
