package state_test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/state"
)

func TestOperationIntentPersistsWithTargets(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "client.db")
	canonical := filepath.Join(root, "canonical")
	agent := filepath.Join(root, "agent")
	for _, directory := range []string{canonical, agent} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	canonical, err := filepath.EvalSymlinks(canonical)
	if err != nil {
		t.Fatal(err)
	}
	agent, err = filepath.EvalSymlinks(agent)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{
		ID: "operation-1", Action: "install", Slug: "demo", ContentHash: "hash",
		Targets: []state.OperationTarget{
			{Path: filepath.Join(agent, "demo"), Kind: "symlink"},
			{Path: filepath.Join(canonical, "demo"), Kind: "canonical"},
		},
	}
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	got, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "planned" || !reflect.DeepEqual(got.Plan, plan) {
		t.Fatalf("got %+v, want planned %+v", got, plan)
	}
}

func TestOperationIntentCanPlanMissingAgentDirectories(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	path := filepath.Join(root, ".agents", "skills", "demo")
	plan := state.OperationPlan{ID: "new-directory", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: path, Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatalf("could not plan a missing parent: %v", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.GetOperation(t.Context(), plan.ID)
	if err != nil || operation.Plan.Targets[0].Path != filepath.Join(resolvedRoot, ".agents", "skills", "demo") {
		t.Fatalf("unexpected plan: %+v, %v", operation, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planning created an installation directory: %v", err)
	}
}

func TestOperationIntentPersistsAndReservesStageName(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "first", Action: "install", Slug: "first", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "first"), Kind: "canonical", StageName: ".skillhub-stage-fixed"}}}
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
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
	if err != nil || len(operation.Plan.Targets) != 1 || operation.Plan.Targets[0].StageName != ".skillhub-stage-fixed" {
		t.Fatalf("stage name did not survive reopen: %+v, %v", operation, err)
	}
	plan.ID, plan.Slug = "second", "second"
	plan.Targets[0].Path = filepath.Join(root, "second")
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("accepted a stage already reserved by another operation")
	}
	if _, err := os.Stat(filepath.Join(root, ".skillhub-stage-fixed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planning created a stage: %v", err)
	}
}

func TestOperationIntentRejectsInvalidOrOverlappingStage(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for index, target := range []state.OperationTarget{
		{Path: filepath.Join(root, "one"), Kind: "canonical", StageName: "../escape"},
		{Path: filepath.Join(root, "two"), Kind: "canonical", StageName: ".skillhub-stage-\x00bad"},
		{Path: filepath.Join(root, ".skillhub-stage-same"), Kind: "canonical", StageName: ".skillhub-stage-same"},
	} {
		id := fmt.Sprintf("invalid-stage-%d", index)
		plan := state.OperationPlan{ID: id, Action: "install", Slug: id, ContentHash: "hash", Targets: []state.OperationTarget{target}}
		if err := store.BeginOperation(t.Context(), plan); err == nil {
			t.Errorf("accepted invalid or overlapping stage %q", target.StageName)
		}
		if _, err := store.GetOperation(t.Context(), id); err == nil {
			t.Errorf("recorded invalid operation %q", id)
		}
	}
}

func TestOperationIntentStageCannotBeAnotherTarget(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first := state.OperationPlan{ID: "first", Action: "install", Slug: "first", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "first"), Kind: "canonical", StageName: ".skillhub-stage-fixed"}}}
	if err := store.BeginOperation(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second := state.OperationPlan{ID: "second", Action: "install", Slug: "second", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, ".skillhub-stage-fixed"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), second); err == nil {
		t.Fatal("accepted a target reserved as another operation's stage")
	}
}

func TestOperationIntentRejectsAliasedMissingTargetDirectories(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "existing")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	plan := state.OperationPlan{ID: "alias-missing", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{
		{Path: filepath.Join(parent, "missing", "demo"), Kind: "canonical"},
		{Path: filepath.Join(alias, "missing", "demo"), Kind: "symlink"},
	}}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("expected aliased missing targets to conflict")
	}
	if _, err := os.Stat(filepath.Join(parent, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planning modified target directories: %v", err)
	}
}

func TestOperationIntentRejectsDanglingParentSymlink(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "missing"), alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	plan := state.OperationPlan{ID: "dangling", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(alias, "skills", "demo"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("expected dangling parent symlink rejection")
	}
}

func TestOperationIntentRejectsConflictsAtomically(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	path := filepath.Join(root, "demo")
	plan := state.OperationPlan{ID: "duplicate", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{
		{Path: path, Kind: "canonical"}, {Path: path, Kind: "symlink"},
	}}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("expected duplicate target rejection")
	}
	if _, err := store.GetOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("failed intent must not leave an operation")
	}
	plan.Targets = plan.Targets[:1]
	if err := store.BeginOperation(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("expected duplicate operation ID rejection")
	}
}

func TestPendingOperationsSurviveReopen(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"second", "first"} {
		plan := state.OperationPlan{ID: id, Action: "install", Slug: id, ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, id), Kind: "canonical"}}}
		if err := store.BeginOperation(t.Context(), plan); err != nil {
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
	operations, err := store.PendingOperations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 2 || operations[0].Plan.ID != "second" || operations[1].Plan.ID != "first" || len(operations[0].Plan.Targets) != 1 || operations[0].Status != "planned" {
		t.Fatalf("unexpected pending operations: %+v", operations)
	}
}

func TestOperationIntentRejectsAliasedTargets(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "target")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	plan := state.OperationPlan{ID: "alias", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{
		{Path: filepath.Join(parent, "demo"), Kind: "canonical"}, {Path: filepath.Join(alias, "demo"), Kind: "symlink"},
	}}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("expected aliased target rejection")
	}
	if _, err := store.GetOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("failed intent must not leave an operation")
	}
}

func TestOperationIntentRejectsInvalidKindWithoutPartialRecord(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	plan := state.OperationPlan{ID: "invalid", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{
		{Path: filepath.Join(root, "one"), Kind: "canonical"}, {Path: filepath.Join(root, "two"), Kind: "unknown"},
	}}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("expected invalid target rejection")
	}
	if _, err := store.GetOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("failed intent must not leave an operation")
	}
}

func TestOperationIntentRejectsNULPath(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	plan := state.OperationPlan{ID: "nul-path", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "invalid\x00name"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("expected NUL path rejection")
	}
	if _, err := store.GetOperation(t.Context(), plan.ID); err == nil {
		t.Fatal("failed intent must not leave an operation")
	}
}

func TestOperationIntentRejectsFilesystemRoot(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	volumeRoot := filepath.VolumeName(root) + string(filepath.Separator)
	plan := state.OperationPlan{ID: "root", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: volumeRoot, Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("expected filesystem root rejection")
	}
}

func TestOperationIntentRejectsUnsafeSlugs(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for index, slug := range []string{"", ".", "..", "../escape", "nested/name", `nested\name`, "bad\x00name"} {
		id := fmt.Sprintf("invalid-slug-%d", index)
		plan := state.OperationPlan{ID: id, Action: "install", Slug: slug, ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, id), Kind: "canonical"}}}
		if err := store.BeginOperation(t.Context(), plan); err == nil {
			t.Errorf("accepted unsafe slug %q", slug)
		}
	}
}

func TestPendingOperationsReserveSlugAndTarget(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first := state.OperationPlan{ID: "first", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "one"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	for _, conflict := range []state.OperationPlan{
		{ID: "same-slug", Action: "update", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "two"), Kind: "canonical"}}},
		{ID: "same-target", Action: "install", Slug: "other", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "one"), Kind: "canonical"}}},
	} {
		if err := store.BeginOperation(t.Context(), conflict); err == nil {
			t.Errorf("accepted conflicting operation %q", conflict.ID)
		}
		if _, err := store.GetOperation(t.Context(), conflict.ID); err == nil {
			t.Errorf("conflicting operation %q was partially recorded", conflict.ID)
		}
	}
	if _, err := store.GetOperation(t.Context(), first.ID); err != nil {
		t.Fatalf("original operation changed: %v", err)
	}
}

func TestPendingOperationsRejectOverlappingTargetTrees(t *testing.T) {
	for _, firstIsParent := range []bool{true, false} {
		t.Run(fmt.Sprint(firstIsParent), func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "skills")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			firstPath, secondPath := parent, filepath.Join(parent, "demo")
			if !firstIsParent {
				firstPath, secondPath = secondPath, firstPath
			}
			first := state.OperationPlan{ID: "first", Action: "install", Slug: "first", ContentHash: "hash", Targets: []state.OperationTarget{{Path: firstPath, Kind: "canonical"}}}
			if err := store.BeginOperation(t.Context(), first); err != nil {
				t.Fatal(err)
			}
			second := state.OperationPlan{ID: "second", Action: "install", Slug: "second", ContentHash: "hash", Targets: []state.OperationTarget{{Path: secondPath, Kind: "canonical"}}}
			if err := store.BeginOperation(t.Context(), second); err == nil {
				t.Fatal("accepted overlapping target trees")
			}
		})
	}
}

func TestConcurrentConflictingOperationsHaveOneWinner(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "skills")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "client.db")
	first, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	plans := []state.OperationPlan{
		{ID: "first", Action: "install", Slug: "first", ContentHash: "hash", Targets: []state.OperationTarget{{Path: parent, Kind: "canonical"}}},
		{ID: "second", Action: "install", Slug: "second", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(parent, "demo"), Kind: "canonical"}}},
	}
	stores := []*state.Store{first, second}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for index := range 2 {
		workers.Go(func() {
			<-start
			results <- stores[index].BeginOperation(t.Context(), plans[index])
		})
	}
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
		t.Fatalf("got %d successful operations, want one", succeeded)
	}
	operations, err := first.PendingOperations(t.Context())
	if err != nil || len(operations) != 1 {
		t.Fatalf("unexpected pending operations: %+v, %v", operations, err)
	}
}

func TestPendingOperationClaimsRestoredAfterSchemaUpgrade(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "old", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical"}}}
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
	if _, err := database.ExecContext(t.Context(), `DROP TABLE operation_claims`); err != nil {
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
	plan.ID = "new"
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("old pending operation did not reserve its resources")
	}
}

func TestConflictingLegacyOperationClaimsFailWithoutPartialClaims(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "old", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical"}}}
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
	for _, statement := range []string{
		`DROP TABLE operation_claims`,
		`INSERT INTO operations (id, operation_key, action, slug, content_hash, status) VALUES (100, 'other', 'install', 'demo', 'hash', 'planned')`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "other")
	if _, err := database.ExecContext(t.Context(), `INSERT INTO operation_targets (operation_id, position, path, path_key, kind) VALUES (100, 0, ?, ?, 'canonical')`, path, path); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err := state.Open(t.Context(), databasePath); err == nil {
		_ = store.Close()
		t.Fatal("expected conflicting old intents to require reconciliation")
	}
	database, err = sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var count int
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM operation_claims`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("claim restoration was not rolled back: count=%d, error=%v", count, err)
	}
}

func TestInvalidLegacyOperationPathBlocksClaimMigration(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "old", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "demo"), Kind: "canonical"}}}
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
	if _, err := database.ExecContext(t.Context(), `DROP TABLE operation_claims`); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(root, "bad\x00name")
	if _, err := database.ExecContext(t.Context(), `UPDATE operation_targets SET path = ?, path_key = ?`, invalid, invalid); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err := state.Open(t.Context(), databasePath); err == nil {
		_ = store.Close()
		t.Fatal("expected invalid legacy operation path to block migration")
	}
	database, err = sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var got string
	if err := database.QueryRowContext(t.Context(), `SELECT path FROM operation_targets`).Scan(&got); err != nil || got != invalid {
		t.Fatalf("legacy operation was modified: %q, %v", got, err)
	}
}

func TestOperationRejectsConflictFromLegacyWriter(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "client.db")
	store, err := state.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `INSERT INTO operations (id, operation_key, action, slug, content_hash, status) VALUES (100, 'legacy', 'install', 'demo', 'hash', 'planned')`); err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(resolvedRoot, "old")
	key := path
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	if _, err := database.ExecContext(t.Context(), `INSERT INTO operation_targets (operation_id, position, path, path_key, kind) VALUES (100, 0, ?, ?, 'canonical')`, path, key); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	plan := state.OperationPlan{ID: "new", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(root, "new"), Kind: "canonical"}}}
	if err := store.BeginOperation(t.Context(), plan); err == nil {
		t.Fatal("expected existing legacy intent to reserve its slug")
	}
	if _, err := store.GetOperation(t.Context(), "new"); err == nil {
		t.Fatal("conflicting operation was partially recorded")
	}
}

func TestOperationIntentSurvivesProcessExit(t *testing.T) {
	if database := os.Getenv("SKILLHUB_TEST_CRASH_DATABASE"); database != "" {
		store, err := state.Open(t.Context(), database)
		if err != nil {
			t.Fatal(err)
		}
		plan := state.OperationPlan{ID: "interrupted", Action: "install", Slug: "demo", ContentHash: "hash", Targets: []state.OperationTarget{{Path: filepath.Join(filepath.Dir(database), "demo"), Kind: "canonical"}}}
		if err := store.BeginOperation(t.Context(), plan); err != nil {
			t.Fatal(err)
		}
		os.Exit(37)
	}
	database := filepath.Join(t.TempDir(), "client.db")
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestOperationIntentSurvivesProcessExit$")
	command.Env = append(os.Environ(), "SKILLHUB_TEST_CRASH_DATABASE="+database)
	output, err := command.CombinedOutput()
	exitError, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exitError.ExitCode() != 37 {
		t.Fatalf("child did not exit after writing intent: %v: %s", err, output)
	}
	store, err := state.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	operations, err := store.PendingOperations(t.Context())
	if err != nil || len(operations) != 1 || operations[0].Plan.ID != "interrupted" || operations[0].Status != "planned" {
		t.Fatalf("intent not recovered: %+v, %v", operations, err)
	}
}
