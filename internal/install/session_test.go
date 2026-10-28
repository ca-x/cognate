package install_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/objectid"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/ca-x/cognate/internal/state"
	"github.com/spf13/afero"
)

func TestSessionSerializesDatabaseAccess(t *testing.T) {
	directory := t.TempDir()
	first, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if second, err := install.OpenSession(ctx, directory); !errors.Is(err, context.DeadlineExceeded) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("expected lock timeout, got %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
}

func TestSessionRequiresPendingOperationsToBeResolved(t *testing.T) {
	directory := t.TempDir()
	first, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "first", Action: "install", Slug: "first", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "first"), Kind: "canonical"}}}
	if err := first.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	pending, err := second.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Plan.ID != "first" {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	plan.ID, plan.Slug = "second", "second"
	plan.Targets[0].Path = filepath.Join(directory, "second")
	if err := second.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("new operation accepted before recovery")
	}
	if err := second.CancelPlannedOperation(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	if err := second.BeginOperation(t.Context(), plan); err != nil {
		t.Fatalf("resolved operation still blocks new work: %v", err)
	}
}

func TestClosedSessionRejectsOperations(t *testing.T) {
	session, err := install.OpenSession(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.PendingOperations(t.Context()); err == nil {
		t.Fatal("closed session accepted operation")
	}
}

func TestSessionDoesNotStartAnOldPlanWithoutRecovery(t *testing.T) {
	directory := t.TempDir()
	first, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "old", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical"}}}
	if err := first.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.StartOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("started an old plan without recovery")
	}
	if err := second.CancelPlannedOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	plan.ID = "new"
	if err := second.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := second.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatalf("could not start plan created in this session: %v", err)
	}
}

func TestSessionBlocksWorkAfterInterruptedActiveOperation(t *testing.T) {
	directory := t.TempDir()
	first, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "old", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical"}}}
	if err := first.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := first.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.CancelPlannedOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("cancelled an active operation as unstarted")
	}
	if err := second.MarkOperationNeedsAttention(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	plan.ID, plan.Slug = "new", "other"
	plan.Targets[0].Path = filepath.Join(directory, "other")
	if err := second.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("started new work despite an unresolved operation")
	}
}

func TestRecoverInterruptedOperations(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "planned"
		if active {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			first, err := install.OpenSession(t.Context(), directory)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(directory, "demo")
			plan := state.OperationPlan{ID: "old", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: target, Kind: "canonical", StageName: ".skillhub-stage-demo"}}}
			if err := first.BeginOperation(t.Context(), plan); err != nil {
				t.Fatal(err)
			}
			if active {
				if err := first.StartOperation(t.Context(), plan.ID); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("preserve"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			second, err := install.OpenSession(t.Context(), directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = second.Close() })
			for range 2 {
				pending, err := second.RecoverInterruptedOperations(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if active {
					if len(pending) != 1 || pending[0].Status != "needs_attention" {
						t.Fatalf("pending = %+v, want needs_attention", pending)
					}
				} else if len(pending) != 0 {
					t.Fatalf("pending = %+v, want none", pending)
				}
			}
			if active {
				if content, err := os.ReadFile(target); err != nil || string(content) != "preserve" {
					t.Fatalf("target changed: %q, %v", content, err)
				}
			}
			plan.ID = "new"
			err = second.BeginOperation(t.Context(), plan)
			if active && err == nil || !active && err != nil {
				t.Fatalf("new operation error = %v; active = %v", err, active)
			}
		})
	}
}

func TestRecoveryCannotInterruptWorkInSameSession(t *testing.T) {
	for _, active := range []bool{false, true} {
		directory := t.TempDir()
		session, err := install.OpenSession(t.Context(), directory)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		plan := state.OperationPlan{ID: "live", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical"}}}
		if err := session.BeginOperation(t.Context(), plan); err != nil {
			t.Fatal(err)
		}
		if active {
			if err := session.StartOperation(t.Context(), plan.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := session.RecoverInterruptedOperations(t.Context()); err == nil {
			t.Fatal("recovery accepted work started in the same session")
		}
	}
}

func TestRecoverAfterProcessExit(t *testing.T) {
	if directory := os.Getenv("SKILLHUB_TEST_RECOVERY_DIRECTORY"); directory != "" {
		session, err := install.OpenSession(t.Context(), directory)
		if err != nil {
			t.Fatal(err)
		}
		plan := state.OperationPlan{ID: "interrupted", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-interrupted"}}}
		if err := session.BeginOperation(t.Context(), plan); err != nil {
			t.Fatal(err)
		}
		if err := session.StartOperation(t.Context(), plan.ID); err != nil {
			t.Fatal(err)
		}
		stage, err := skill.CreateStageAt(directory, plan.Targets[0].StageName)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage.Path(), "partial"), []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	directory := t.TempDir()
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRecoverAfterProcessExit$")
	command.Env = append(os.Environ(), "SKILLHUB_TEST_RECOVERY_DIRECTORY="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v: %s", err, output)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	pending, err := session.RecoverInterruptedOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	partial := filepath.Join(directory, ".skillhub-stage-interrupted", "partial")
	if content, err := os.ReadFile(partial); err != nil || string(content) != "preserve" {
		t.Fatalf("unidentified stage changed: %q, %v", content, err)
	}
}

func TestSessionRecordsStageIdentityBeforeCopy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stage identity is not supported on this platform")
	}
	directory := t.TempDir()
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "stage", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-demo"}}}
	if err := session.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateStage(t.Context(), plan.ID, 0); err == nil {
		t.Fatal("recorded a stage before starting the operation")
	}
	if err := session.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateStage(t.Context(), plan.ID, 1); err == nil {
		t.Fatal("created a nonexistent target")
	}
	stage, err := session.CreateStage(t.Context(), plan.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Plan.Targets[0].StageIdentity == "" {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	identity := pending[0].Plan.Targets[0].StageIdentity
	info, err := os.Stat(stage)
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity, err := objectid.FromFileInfo(info)
	if err != nil || identity != wantIdentity {
		t.Fatalf("recorded identity %q, want %q, error %v", identity, wantIdentity, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	session, err = install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.CreateStage(t.Context(), plan.ID, 0); err == nil {
		t.Fatal("created stage for an operation started by a previous session")
	}
	pending, err = session.RecoverInterruptedOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Plan.Targets[0].StageIdentity != identity || pending[0].Status != "needs_attention" {
		t.Fatalf("pending after recovery = %+v, error = %v", pending, err)
	}
}

func TestSessionCopiesVerifiedStageAndRecordsReadiness(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stage identity is not supported on this platform")
	}
	directory, source := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("original"), 0o600); err != nil {
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
	plan := state.OperationPlan{ID: "ready", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-ready"}}}
	if err := session.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := session.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := session.CopyStage(t.Context(), plan.ID, 0, source); err == nil {
		t.Fatal("copied without creating a stage")
	}
	stage, err := session.CreateStage(t.Context(), plan.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.CopyStage(t.Context(), plan.ID, 0, source); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(stage, "SKILL.md")); err != nil || string(content) != "original" {
		t.Fatalf("stage content = %q, error = %v", content, err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || !pending[0].Plan.Targets[0].StageReady {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.CopyStage(t.Context(), plan.ID, 0, source); err == nil {
		t.Fatal("closed session accepted stage copy")
	}
	session, err = install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	pending, err = session.RecoverInterruptedOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || !pending[0].Plan.Targets[0].StageReady {
		t.Fatalf("pending after restart = %+v, error = %v", pending, err)
	}
}

func TestSessionCopyRejectsChangedSource(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stage identity is not supported on this platform")
	}
	directory, source := t.TempDir(), t.TempDir()
	file := filepath.Join(source, "SKILL.md")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	plan := state.OperationPlan{ID: "changed", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-changed"}}}
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
	if err := session.CopyStage(t.Context(), plan.ID, 0, source); err == nil {
		t.Fatal("accepted changed source")
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || pending[0].Plan.Targets[0].StageReady {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if entries, err := os.ReadDir(stage); err != nil || len(entries) != 0 {
		t.Fatalf("unexpected stage contents: %v, %v", entries, err)
	}
}

func TestSessionPreservesCopyWhenReadinessWriteFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stage identity is not supported on this platform")
	}
	directory, source := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := database.ExecContext(t.Context(), `CREATE TRIGGER reject_ready BEFORE UPDATE OF stage_ready ON operation_targets BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	plan := state.OperationPlan{ID: "failure", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-copy-failure"}}}
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
	if err := session.CopyStage(t.Context(), plan.ID, 0, source); err == nil {
		t.Fatal("injected readiness write failure did not fail")
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || pending[0].Plan.Targets[0].StageReady {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if content, err := os.ReadFile(filepath.Join(stage, "SKILL.md")); err != nil || string(content) != "skill" {
		t.Fatalf("copied stage was not preserved: %q, %v", content, err)
	}
}

func TestStageReadinessSurvivesProcessExit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stage identity is not supported on this platform")
	}
	if directory := os.Getenv("SKILLHUB_TEST_READY_DIRECTORY"); directory != "" {
		source := filepath.Join(directory, "source")
		hash, err := skill.ContentHash(afero.NewOsFs(), source)
		if err != nil {
			t.Fatal(err)
		}
		session, err := install.OpenSession(t.Context(), directory)
		if err != nil {
			t.Fatal(err)
		}
		plan := state.OperationPlan{ID: "ready", Action: "install", Slug: "demo", ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-crash-ready"}}}
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
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStageReadinessSurvivesProcessExit$")
	command.Env = append(os.Environ(), "SKILLHUB_TEST_READY_DIRECTORY="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v: %s", err, output)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	pending, err := session.RecoverInterruptedOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || !pending[0].Plan.Targets[0].StageReady {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if content, err := os.ReadFile(filepath.Join(directory, ".skillhub-stage-crash-ready", "SKILL.md")); err != nil || string(content) != "skill" {
		t.Fatalf("stage not retained: %q, %v", content, err)
	}
}

func TestStageIdentitySurvivesProcessExit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stage identity is not supported on this platform")
	}
	if directory := os.Getenv("SKILLHUB_TEST_IDENTITY_DIRECTORY"); directory != "" {
		session, err := install.OpenSession(t.Context(), directory)
		if err != nil {
			t.Fatal(err)
		}
		plan := state.OperationPlan{ID: "interrupted", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-recorded"}}}
		if err := session.BeginOperation(t.Context(), plan); err != nil {
			t.Fatal(err)
		}
		if err := session.StartOperation(t.Context(), plan.ID); err != nil {
			t.Fatal(err)
		}
		_, err = session.CreateStage(t.Context(), plan.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	directory := t.TempDir()
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStageIdentitySurvivesProcessExit$")
	command.Env = append(os.Environ(), "SKILLHUB_TEST_IDENTITY_DIRECTORY="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v: %s", err, output)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	pending, err := session.RecoverInterruptedOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || pending[0].Plan.Targets[0].StageIdentity == "" {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if info, err := os.Stat(filepath.Join(directory, ".skillhub-stage-recorded")); err != nil || !info.IsDir() {
		t.Fatalf("stage not retained: %v", err)
	}
}

func TestSessionDoesNotClaimPreexistingStage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stage identity is not supported on this platform")
	}
	directory := t.TempDir()
	name := ".skillhub-stage-existing"
	stagePath := filepath.Join(directory, name)
	if err := os.Mkdir(stagePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagePath, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	plan := state.OperationPlan{ID: "existing", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: name}}}
	if err := session.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := session.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if stage, err := session.CreateStage(t.Context(), plan.ID, 0); err == nil || stage != "" {
		t.Fatalf("preexisting stage accepted: %v, %v", stage, err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || pending[0].Plan.Targets[0].StageIdentity != "" {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if content, err := os.ReadFile(filepath.Join(stagePath, "keep")); err != nil || string(content) != "keep" {
		t.Fatalf("preexisting stage changed: %q, %v", content, err)
	}
}

func TestSessionRetainsStageWhenIdentityWriteFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stage identity is not supported on this platform")
	}
	directory := t.TempDir()
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
	if _, err := database.ExecContext(t.Context(), `CREATE TRIGGER reject_identity BEFORE UPDATE OF stage_identity ON operation_targets BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	plan := state.OperationPlan{ID: "failure", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: ".skillhub-stage-failure"}}}
	if err := session.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := session.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	stage, err := session.CreateStage(t.Context(), plan.ID, 0)
	if err == nil || stage == "" {
		t.Fatalf("stage = %v, error = %v", stage, err)
	}
	if info, err := os.Stat(stage); err != nil || !info.IsDir() {
		t.Fatalf("stage not retained: %v", err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" || pending[0].Plan.Targets[0].StageIdentity != "" {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if _, err := session.CreateStage(t.Context(), plan.ID, 0); err == nil {
		t.Fatal("continued after failed stage creation")
	}
}

func TestSessionReportsUnconfirmedFailureStatus(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stage identity is not supported on this platform")
	}
	directory := t.TempDir()
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
	if _, err := database.ExecContext(t.Context(), `CREATE TRIGGER reject_attention BEFORE UPDATE OF status ON operations WHEN NEW.status = 'needs_attention' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	name := ".skillhub-stage-existing"
	if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "failure", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(directory, "demo"), Kind: "canonical", StageName: name}}}
	if err := session.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := session.StartOperation(t.Context(), plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateStage(t.Context(), plan.ID, 0); err == nil || !strings.Contains(err.Error(), "attention status was not recorded") {
		t.Fatalf("expected unconfirmed status error, got %v", err)
	}
	pending, err := session.PendingOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "active" {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
	if _, err := session.CreateStage(t.Context(), plan.ID, 0); err == nil {
		t.Fatal("continued with an unconfirmed failure status")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `DROP TRIGGER reject_attention`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	session, err = install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	pending, err = session.RecoverInterruptedOperations(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != "needs_attention" {
		t.Fatalf("recovered = %+v, error = %v", pending, err)
	}
}

func TestSessionLockWorksAcrossProcessesAndAliases(t *testing.T) {
	if directory := os.Getenv("SKILLHUB_TEST_SESSION_DIRECTORY"); directory != "" {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		if session, err := install.OpenSession(ctx, directory); !errors.Is(err, context.DeadlineExceeded) {
			if session != nil {
				_ = session.Close()
			}
			t.Fatalf("expected lock timeout, got %v", err)
		}
		return
	}
	directory := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(directory, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSessionLockWorksAcrossProcessesAndAliases$")
	command.Env = append(os.Environ(), "SKILLHUB_TEST_SESSION_DIRECTORY="+alias)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("other process did not respect installation lock: %v: %s", err, output)
	}
}

func TestSessionLockFollowsDatabaseIdentity(t *testing.T) {
	firstDirectory, secondDirectory := t.TempDir(), t.TempDir()
	first, err := install.OpenSession(t.Context(), firstDirectory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err := os.Symlink(filepath.Join(firstDirectory, "client.db"), filepath.Join(secondDirectory, "client.db")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if second, err := install.OpenSession(ctx, secondDirectory); !errors.Is(err, context.DeadlineExceeded) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("database alias bypassed installation lock: %v", err)
	}
}

func TestSessionOpenFailureReleasesLock(t *testing.T) {
	directory := t.TempDir()
	database := filepath.Join(directory, "client.db")
	if err := os.Mkdir(database, 0o700); err != nil {
		t.Fatal(err)
	}
	if session, err := install.OpenSession(t.Context(), directory); err == nil {
		_ = session.Close()
		t.Fatal("expected database open failure")
	}
	if err := os.Remove(database); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	session, err := install.OpenSession(ctx, directory)
	if err != nil {
		t.Fatalf("open failure leaked installation lock: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
}
