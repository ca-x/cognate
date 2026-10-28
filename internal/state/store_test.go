package state_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/filelock"
	"github.com/ca-x/skillhub/internal/state"
)

func TestOpenMigratesPreviousNonemptyProjectionSchema(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "legacy.db")
	parent := filepath.Join(root, "agent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE installed_skills (id INTEGER PRIMARY KEY, slug TEXT NOT NULL UNIQUE, content_hash TEXT NOT NULL)`,
		`CREATE TABLE projections (id INTEGER PRIMARY KEY, path TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, installed_skill_projections INTEGER NOT NULL REFERENCES installed_skills(id))`,
		`INSERT INTO installed_skills (id, slug, content_hash) VALUES (1, 'legacy', 'hash')`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(alias, "legacy")
	if _, err := database.ExecContext(t.Context(), `INSERT INTO projections (path, kind, installed_skill_projections) VALUES (?, 'symlink', 1)`, path); err != nil {
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
	got, err := store.Get(t.Context(), "legacy")
	canonicalParent, resolveErr := filepath.EvalSymlinks(parent)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err != nil || len(got.Projections) != 1 || got.Projections[0].Path != filepath.Join(canonicalParent, "legacy") {
		t.Fatalf("legacy installation not preserved: %+v, %v", got, err)
	}
	if got.Projections[0].Identity != "" {
		t.Fatal("legacy projection was automatically assigned an identity")
	}
}

func TestConcurrentOpenMigratesLegacyDatabase(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "legacy.db")
	createLegacyDatabase(t, path, filepath.Join(root, "skill"))
	start := make(chan struct{})
	results := make(chan error, 12)
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			<-start
			store, err := state.Open(t.Context(), path)
			if err == nil {
				err = store.Close()
			}
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent open failed: %v", err)
		}
	}
}

func createLegacyDatabase(t *testing.T, path, projection string) {
	t.Helper()
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE installed_skills (id INTEGER PRIMARY KEY, slug TEXT NOT NULL UNIQUE, content_hash TEXT NOT NULL)`,
		`CREATE TABLE projections (id INTEGER PRIMARY KEY, path TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, installed_skill_projections INTEGER NOT NULL REFERENCES installed_skills(id))`,
		`INSERT INTO installed_skills (id, slug, content_hash) VALUES (1, 'legacy', 'hash')`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(t.Context(), `INSERT INTO projections (path, kind, installed_skill_projections) VALUES (?, 'symlink', 1)`, projection); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentProcessesOpenLegacyDatabase(t *testing.T) {
	if path := os.Getenv("SKILLHUB_TEST_LEGACY_DATABASE"); path != "" {
		store, err := state.Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	path := filepath.Join(root, "legacy.db")
	createLegacyDatabase(t, path, filepath.Join(root, "skill"))
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	start := make(chan struct{})
	results := make(chan error, 6)
	var workers sync.WaitGroup
	for index := range 6 {
		workers.Go(func() {
			<-start
			databasePath := path
			if index%2 == 1 {
				databasePath = filepath.Join(alias, "legacy.db")
			}
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestConcurrentProcessesOpenLegacyDatabase$")
			command.Env = append(os.Environ(), "SKILLHUB_TEST_LEGACY_DATABASE="+databasePath)
			output, err := command.CombinedOutput()
			if err != nil {
				results <- fmt.Errorf("child failed: %w: %s", err, output)
				return
			}
			results <- nil
		})
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent open failed: %v", err)
		}
	}
}

func TestOpenHonorsCancellationWhileSchemaLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.db")
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := filelock.Acquire(t.Context(), filepath.Join(resolvedParent, filepath.Base(path))+".schema.lock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if store, err := state.Open(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		if store != nil {
			_ = store.Close()
		}
		t.Fatalf("got %v, want deadline exceeded", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database created while lock was held: %v", err)
	}
}

func TestConcurrentProcessesCreateDatabase(t *testing.T) {
	if path := os.Getenv("SKILLHUB_TEST_NEW_DATABASE"); path != "" {
		store, err := state.Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "client.db")
	start := make(chan struct{})
	results := make(chan error, 6)
	var workers sync.WaitGroup
	for range 6 {
		workers.Go(func() {
			<-start
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestConcurrentProcessesCreateDatabase$")
			command.Env = append(os.Environ(), "SKILLHUB_TEST_NEW_DATABASE="+path)
			output, err := command.CombinedOutput()
			if err != nil {
				results <- fmt.Errorf("child failed: %w: %s", err, output)
				return
			}
			results <- nil
		})
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestOpenRejectsHardLinkedDatabase(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "legacy.db")
	createLegacyDatabase(t, path, filepath.Join(root, "skill"))
	alias := filepath.Join(root, "alias.db")
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	for _, databasePath := range []string{path, alias} {
		store, err := state.Open(t.Context(), databasePath)
		if err == nil {
			_ = store.Close()
			t.Errorf("accepted hard-linked database %q", databasePath)
		}
	}
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var count int
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_table_info('projections') WHERE name = 'path_key'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("database schema changed: count=%d error=%v", count, err)
	}
}

func TestLegacyAliasConflictRollsBackMigration(t *testing.T) {
	root := t.TempDir()
	parent, alias := filepath.Join(root, "agent"), filepath.Join(root, "alias")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	path := filepath.Join(root, "legacy.db")
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE installed_skills (id INTEGER PRIMARY KEY, slug TEXT NOT NULL UNIQUE, content_hash TEXT NOT NULL)`,
		`CREATE TABLE projections (id INTEGER PRIMARY KEY, path TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, installed_skill_projections INTEGER NOT NULL REFERENCES installed_skills(id))`,
		`INSERT INTO installed_skills (id, slug, content_hash) VALUES (1, 'legacy', 'hash')`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, projection := range []string{filepath.Join(parent, "demo"), filepath.Join(alias, "demo")} {
		if _, err := database.ExecContext(t.Context(), `INSERT INTO projections (path, kind, installed_skill_projections) VALUES (?, 'symlink', 1)`, projection); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err := state.Open(t.Context(), path); err == nil {
		_ = store.Close()
		t.Fatal("expected aliased legacy records to require reconciliation")
	}
	database, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var count int
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM projections`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("legacy data changed: count=%d, error=%v", count, err)
	}
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_table_info('projections') WHERE name = 'path_key'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration was not rolled back: count=%d, error=%v", count, err)
	}
}

func TestInstallationRecordsPersistAndRejectConflicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client #.db")
	store, err := state.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	record := state.Installation{
		Slug: "demo", ContentHash: "hash",
		Projections: []state.Projection{{Path: filepath.Join(t.TempDir(), ".claude", "skills", "demo"), Kind: "symlink"}},
	}
	if err := os.MkdirAll(filepath.Dir(record.Projections[0].Path), 0o700); err != nil {
		t.Fatal(err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(record.Projections[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	record.Projections[0].Path = filepath.Join(parent, filepath.Base(record.Projections[0].Path))
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(t.Context(), record); err == nil {
		t.Fatal("duplicate slug should fail")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	got, err := store.Get(t.Context(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, record) {
		t.Fatalf("got %+v, want %+v", got, record)
	}
}

func TestProjectionAliasesCannotClaimSamePath(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "agent")
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
	first := state.Installation{Slug: "first", ContentHash: "one", Projections: []state.Projection{{Path: filepath.Join(parent, "demo"), Kind: "symlink"}}}
	second := state.Installation{Slug: "second", ContentHash: "two", Projections: []state.Projection{{Path: filepath.Join(alias, "demo"), Kind: "symlink"}}}
	if err := store.Create(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(t.Context(), second); err == nil {
		t.Fatal("aliased projection must not acquire a second owner")
	}
}

func TestProjectionRecordDoesNotFollowRetargetedParentAlias(t *testing.T) {
	root := t.TempDir()
	firstParent, secondParent := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, parent := range []string{firstParent, secondParent} {
		if err := os.Mkdir(parent, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(firstParent, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store, err := state.Open(t.Context(), filepath.Join(root, "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first := state.Installation{Slug: "first", ContentHash: "one", Projections: []state.Projection{{Path: filepath.Join(alias, "demo"), Kind: "symlink"}}}
	if err := store.Create(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secondParent, alias); err != nil {
		t.Fatal(err)
	}
	second := state.Installation{Slug: "second", ContentHash: "two", Projections: []state.Projection{{Path: filepath.Join(secondParent, "demo"), Kind: "symlink"}}}
	if err := store.Create(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	resolvedParent, err := filepath.EvalSymlinks(firstParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projections) != 1 || got.Projections[0].Path != filepath.Join(resolvedParent, "demo") {
		t.Fatalf("stored path followed changed alias: %+v", got.Projections)
	}
}

func TestConcurrentDuplicateInstallationHasOneWinner(t *testing.T) {
	store, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			results <- store.Create(t.Context(), state.Installation{Slug: "shared", ContentHash: "same"})
		})
	}
	workers.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("got %d successful writes, want 1", succeeded)
	}
	if _, err := store.Get(t.Context(), "shared"); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionConflictRollsBackInstallation(t *testing.T) {
	store, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	projection := state.Projection{Path: filepath.Join(t.TempDir(), "agent", "skills", "demo"), Kind: "symlink"}
	if err := os.MkdirAll(filepath.Dir(projection.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(t.Context(), state.Installation{Slug: "first", ContentHash: "one", Projections: []state.Projection{projection}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(t.Context(), state.Installation{Slug: "second", ContentHash: "two", Projections: []state.Projection{projection}}); err == nil {
		t.Fatal("expected projection conflict")
	}
	if _, err := store.Get(t.Context(), "second"); err == nil {
		t.Fatal("failed transaction must not leave an installation")
	}
	if _, err := store.Get(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
}

func TestCreateRejectsUncleanProjectionPath(t *testing.T) {
	store, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	path := filepath.Join(t.TempDir(), "skills") + string(filepath.Separator) + ".." + string(filepath.Separator) + "skills" + string(filepath.Separator) + "demo"
	err = store.Create(t.Context(), state.Installation{
		Slug: "demo", ContentHash: "hash", Projections: []state.Projection{{Path: path, Kind: "symlink"}},
	})
	if err == nil {
		t.Fatal("expected unclean projection path rejection")
	}
}
