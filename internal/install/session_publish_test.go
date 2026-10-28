package install_test

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ca-x/skillhub/internal/install"
	"github.com/ca-x/skillhub/internal/skill"
	"github.com/ca-x/skillhub/internal/state"
	"github.com/spf13/afero"
)

func readyCanonicalStage(t *testing.T) (*install.Session, string, string) {
	return readyCanonicalStageWithTrigger(t, "")
}

func readyCanonicalStageWithTrigger(t *testing.T, trigger string) (*install.Session, string, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("canonical publication is not yet supported on this platform")
	}
	directory, source := t.TempDir(), t.TempDir()
	if trigger != "" {
		databasePath := filepath.Join(directory, "client.db")
		store, err := state.Open(t.Context(), databasePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		database, err := sql.Open("sqlite3", databasePath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.ExecContext(t.Context(), trigger); err != nil {
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	plan := state.OperationPlan{ID: "publish", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-publish"}}}
	if err := session.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := session.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	stage, err := session.CreateStage(t.Context(), plan.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.CopyStage(t.Context(), plan.ID, 0, source); err != nil {
		t.Fatal(err)
	}
	return session, stage, plan.Targets[0].Path
}

func TestSessionPublishTransactionFailuresPreserveEvidence(t *testing.T) {
	for _, column := range []string{"publish_intent", "published", "object_identity"} {
		afterRename := column != "publish_intent"
		t.Run(column, func(t *testing.T) {
			trigger := `CREATE TRIGGER reject_publication BEFORE UPDATE OF ` + column + ` ON operation_targets BEGIN SELECT RAISE(ABORT, 'injected failure'); END`
			session, stage, target := readyCanonicalStageWithTrigger(t, trigger)
			if err := session.PublishStage(t.Context(), "publish", 0); err == nil {
				t.Fatal("injected database failure did not fail")
			}
			pending, err := session.PendingOperations(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || pending[0].Plan.Targets[0].Published || pending[0].Plan.Targets[0].PublishIntent != afterRename || pending[0].Plan.Targets[0].ObjectIdentity != "" {
				t.Fatalf("pending = %+v, error = %v", pending, err)
			}
			present, absent := stage, target
			if afterRename {
				present, absent = target, stage
			}
			if content, err := os.ReadFile(filepath.Join(present, "SKILL.md")); err != nil || string(content) != "skill" {
				t.Fatalf("content was not preserved: %q, %v", content, err)
			}
			if _, err := os.Lstat(absent); !os.IsNotExist(err) {
				t.Fatalf("unexpected path %q: %v", absent, err)
			}
		})
	}
}

func TestPublishedStageSurvivesProcessExit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("canonical publication is not yet supported on this platform")
	}
	if directory := os.Getenv("SKILLHUB_TEST_PUBLISHED_DIRECTORY"); directory != "" {
		source := filepath.Join(directory, "source")
		hash, err := skill.ContentHash(afero.NewOsFs(), source)
		if err != nil {
			t.Fatal(err)
		}
		session, err := install.OpenSession(t.Context(), directory)
		if err != nil {
			t.Fatal(err)
		}
		plan := state.OperationPlan{ID: "published", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-published"}}}
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
		os.Exit(0)
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPublishedStageSurvivesProcessExit$")
	command.Env = append(os.Environ(), "SKILLHUB_TEST_PUBLISHED_DIRECTORY="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v: %s", err, output)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	for range 2 {
		pending, err := session.RecoverInterruptedOperations(t.Context())
		if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || !pending[0].Plan.Targets[0].PublishIntent || !pending[0].Plan.Targets[0].Published {
			t.Fatalf("pending = %+v, error = %v", pending, err)
		}
	}
	if content, err := os.ReadFile(filepath.Join(directory, "demo", "SKILL.md")); err != nil || string(content) != "skill" {
		t.Fatalf("target not retained: %q, %v", content, err)
	}
}

func TestSessionPublishesCanonicalStage(t *testing.T) {
	session, stage, target := readyCanonicalStage(t)
	if err := session.PublishStage(t.Context(), "publish", 0); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(target, "SKILL.md")); err != nil || string(content) != "skill" {
		t.Fatalf("target content = %q, error = %v", content, err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage remains: %v", err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || !pending[0].Plan.Targets[0].PublishIntent || !pending[0].Plan.Targets[0].Published {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestSessionCompleteRechecksPublishedContent(t *testing.T) {
	for _, modification := range []string{"none", "content", "replacement"} {
		t.Run(modification, func(t *testing.T) {
			session, _, target := readyCanonicalStage(t)
			if err := session.PublishStage(t.Context(), "publish", 0); err != nil {
				t.Fatal(err)
			}
			if modification == "content" {
				if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if modification == "replacement" {
				if err := os.Rename(target, target+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("skill"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := session.CompleteInstallation(t.Context(), "publish")
			if (err != nil) != (modification != "none") {
				t.Fatalf("completion error = %v", err)
			}
			pending, err := session.PendingOperations(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if modification != "none" {
				if len(pending) != 1 || pending[0].Status != "needs_attention" {
					t.Fatalf("pending = %+v", pending)
				}
			} else {
				if len(pending) != 0 {
					t.Fatalf("pending = %+v", pending)
				}
				if err := session.Close(); err != nil {
					t.Fatal(err)
				}
				store, err := state.Open(t.Context(), filepath.Join(filepath.Dir(target), "client.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				record, err := store.Get(t.Context(), "demo")
				if err != nil || len(record.Projections) != 1 || record.Projections[0].Path != target {
					t.Fatalf("record = %+v, error = %v", record, err)
				}
			}
		})
	}
}

func TestSessionPublishRejectsModifiedStage(t *testing.T) {
	for _, modification := range []string{"content", "git"} {
		t.Run(modification, func(t *testing.T) {
			session, stage, target := readyCanonicalStage(t)
			if modification == "content" {
				if err := os.WriteFile(filepath.Join(stage, "SKILL.md"), []byte("modified"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(filepath.Join(stage, ".git"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := session.PublishStage(t.Context(), "publish", 0); err == nil {
				t.Fatal("published a modified stage")
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("unexpected target: %v", err)
			}
			if _, err := os.Stat(stage); err != nil {
				t.Fatalf("stage not retained: %v", err)
			}
			pending, err := session.PendingOperations(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || pending[0].Plan.Targets[0].Published {
				t.Fatalf("pending = %+v, error = %v", pending, err)
			}
		})
	}
}

func TestSessionPublishDoesNotOverwriteExistingTarget(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			session, stage, target := readyCanonicalStage(t)
			switch kind {
			case "file":
				if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink", "dangling":
				linkTarget := filepath.Join(filepath.Dir(target), "missing")
				if kind == "symlink" {
					linkTarget = t.TempDir()
				}
				if err := os.Symlink(linkTarget, target); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			before, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			if err := session.PublishStage(t.Context(), "publish", 0); err == nil {
				t.Fatal("overwrote target")
			}
			after, err := os.Lstat(target)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("target changed: %v", err)
			}
			if _, err := os.Stat(stage); err != nil {
				t.Fatalf("stage not retained: %v", err)
			}
		})
	}
}
