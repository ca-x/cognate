package state

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib-x/entsqlite"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"modernc.org/sqlite/vfs"
)

const snapshotCapabilityPaddingBytes = 1024 * 16384

type snapshotCapabilityFixture struct {
	database *sql.DB
	source   *sql.Conn
	path     string
	staging  string
}

func snapshotCapabilityDirectory(test *testing.T) string {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	return directory
}

func snapshotCapabilityAddress(path string, readonly bool) string {
	parameters := url.Values{}
	if readonly {
		parameters.Set("mode", "ro")
		parameters.Set("immutable", "1")
	} else {
		for _, pragma := range []string{"journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(1)", "wal_autocheckpoint(0)", "busy_timeout(1000)", "cache_size(-256)"} {
			parameters.Add("_pragma", pragma)
		}
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: parameters.Encode()}).String()
}

func snapshotCapabilityReserve(directory, name string) (string, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || filepath.Base(name) != name || !filepath.IsLocal(name) {
		return "", fmt.Errorf("snapshot fixture requires a private directory and local basename")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return filepath.Join(directory, name), nil
}

func snapshotCapabilityOpen(test *testing.T) snapshotCapabilityFixture {
	test.Helper()
	fixture := snapshotCapabilityFixture{staging: snapshotCapabilityDirectory(test)}
	var err error
	fixture.path, err = snapshotCapabilityReserve(snapshotCapabilityDirectory(test), "source.db")
	if err != nil {
		test.Fatal(err)
	}
	fixture.database, err = sql.Open("sqlite3", snapshotCapabilityAddress(fixture.path, false))
	if err != nil {
		test.Fatal(err)
	}
	fixture.database.SetMaxOpenConns(3)
	test.Cleanup(func() {
		if err := fixture.database.Close(); err != nil {
			test.Error(err)
		}
	})
	fixture.source, err = fixture.database.Conn(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if err := fixture.source.Close(); err != nil {
			test.Error(err)
		}
	})
	for _, statement := range []string{
		"CREATE TABLE snapshot_owner (id TEXT PRIMARY KEY)",
		"CREATE TABLE snapshot_asset (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES snapshot_owner(id), revision INTEGER NOT NULL, payload TEXT NOT NULL)",
		"CREATE TABLE snapshot_padding (id INTEGER PRIMARY KEY, payload BLOB NOT NULL)",
		"INSERT INTO snapshot_owner VALUES ('alice'), ('bob')",
		"INSERT INTO snapshot_asset VALUES ('alpha', 'alice', 0, 'synthetic-a'), ('beta', 'bob', 0, 'synthetic-b')",
		"WITH RECURSIVE sequence(value) AS (VALUES(1) UNION ALL SELECT value + 1 FROM sequence WHERE value < 1024) INSERT INTO snapshot_padding SELECT value, zeroblob(16384) FROM sequence",
		"PRAGMA wal_checkpoint(TRUNCATE)",
		"UPDATE snapshot_asset SET revision = 1",
	} {
		if _, err := fixture.source.ExecContext(test.Context(), statement); err != nil {
			test.Fatal(err)
		}
	}
	info, err := os.Stat(fixture.path + "-wal")
	if err != nil || info.Size() <= 32 {
		test.Fatalf("fixture needs an uncheckpointed committed WAL: stat=%v error=%v", info, err)
	}
	var version, journal string
	var synchronous int
	if err := fixture.source.QueryRowContext(test.Context(), "SELECT sqlite_version()").Scan(&version); err != nil {
		test.Fatal(err)
	}
	if err := fixture.source.QueryRowContext(test.Context(), "PRAGMA journal_mode").Scan(&journal); err != nil {
		test.Fatal(err)
	}
	if err := fixture.source.QueryRowContext(test.Context(), "PRAGMA synchronous").Scan(&synchronous); err != nil {
		test.Fatal(err)
	}
	if journal != "wal" || synchronous != 1 {
		test.Fatalf("unexpected source settings: journal=%s synchronous=%d", journal, synchronous)
	}
	test.Logf("driver=entsqlite/sqlite3 sqlite=%s Go=%s OS=%s/%s journal=%s synchronous=%d padding_bytes=%d", version, runtime.Version(), runtime.GOOS, runtime.GOARCH, journal, synchronous, snapshotCapabilityPaddingBytes)
	return fixture
}

func snapshotCapabilityCheck(ctx context.Context, path string) error {
	return snapshotCapabilityInspect(ctx, path, snapshotCapabilityAddress(path, true))
}

func snapshotCapabilityInspect(ctx context.Context, path, address string) error {
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !parent.IsDir() || parent.Mode().Perm() != 0o700 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("snapshot fixture is not a private regular file")
	}
	database, err := sql.Open("sqlite3", address)
	if err != nil {
		return err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	var integrity string
	if err := database.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("snapshot integrity failed")
	}
	violations, err := database.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := violations.Next()
	if err := errors.Join(violations.Err(), violations.Close()); err != nil {
		return err
	}
	if invalid {
		return fmt.Errorf("snapshot foreign key violation")
	}
	var owners, tables int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM snapshot_owner WHERE id IN ('alice', 'bob')").Scan(&owners); err != nil {
		return err
	}
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type = 'table'").Scan(&tables); err != nil {
		return err
	}
	if owners != 2 || tables != 3 {
		return fmt.Errorf("snapshot synthetic schema or owner mismatch")
	}
	rows, err := database.QueryContext(ctx, "SELECT owner_id, id, revision, payload FROM snapshot_asset ORDER BY owner_id, id")
	if err != nil {
		return err
	}
	digest := sha256.New()
	for rows.Next() {
		var owner, identifier, payload string
		var revision int
		if err := rows.Scan(&owner, &identifier, &revision, &payload); err != nil {
			_ = rows.Close()
			return err
		}
		fmt.Fprintf(digest, "%s|%s|%d|%s\n", owner, identifier, revision, payload)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	var count, bytes int
	if err := database.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(length(payload)), 0) FROM snapshot_padding").Scan(&count, &bytes); err != nil {
		return err
	}
	fmt.Fprintf(digest, "padding|%d|%d\n", count, bytes)
	expected := sha256.Sum256([]byte("alice|alpha|1|synthetic-a\nbob|beta|1|synthetic-b\npadding|1024|16777216\n"))
	if string(digest.Sum(nil)) != string(expected[:]) {
		return fmt.Errorf("snapshot committed data hash mismatch")
	}
	_, writeErr := database.ExecContext(ctx, "CREATE TABLE forbidden_initialization (id INTEGER)")
	if failure, ok := errors.AsType[*sqlite.Error](writeErr); !ok || failure.Code()&0xff != sqlite3.SQLITE_READONLY {
		return fmt.Errorf("snapshot inspection did not enforce read-only schema access: %w", writeErr)
	}
	return database.Close()
}

func snapshotCapabilitySync(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func TestSQLiteSnapshotCapabilityWAL(test *testing.T) {
	fixture := snapshotCapabilityOpen(test)
	mainBefore, err := os.ReadFile(fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	walBefore, err := os.ReadFile(fixture.path + "-wal")
	if err != nil {
		test.Fatal(err)
	}
	sourceAddress := (&url.URL{Scheme: "file", Path: filepath.ToSlash(fixture.path), RawQuery: "mode=ro"}).String()
	if err := snapshotCapabilityInspect(test.Context(), fixture.path, sourceAddress); err != nil {
		test.Fatalf("live WAL source read-only inspection: %v", err)
	}
	mainAfter, err := os.ReadFile(fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	walAfter, err := os.ReadFile(fixture.path + "-wal")
	if err != nil {
		test.Fatal(err)
	}
	if sha256.Sum256(mainBefore) != sha256.Sum256(mainAfter) || sha256.Sum256(walBefore) != sha256.Sum256(walAfter) {
		test.Fatal("live source read-only inspection modified main DB or WAL bytes")
	}
	missing := filepath.Join(fixture.staging, "missing.db")
	if err := snapshotCapabilityCheck(test.Context(), missing); err == nil {
		test.Fatal("oracle accepted a missing snapshot")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("read-only inspection created the missing snapshot")
	}
	readonly, err := sql.Open("sqlite3", snapshotCapabilityAddress(missing, true))
	if err != nil {
		test.Fatal(err)
	}
	openErr := readonly.PingContext(test.Context())
	if err := readonly.Close(); err != nil {
		test.Fatal(err)
	}
	if openErr == nil {
		test.Fatal("read-only driver opened a nonexistent snapshot")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("read-only driver created a nonexistent snapshot")
	}
	mainOnly, err := os.ReadFile(fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	wrong := filepath.Join(fixture.staging, "wrong-main-only.db")
	if err := os.WriteFile(wrong, mainOnly, 0o600); err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilityCheck(test.Context(), wrong); err == nil || !strings.Contains(err.Error(), "committed data hash mismatch") {
		test.Fatalf("oracle must reject omitted WAL commits, got %v", err)
	}
	test.Log("negative oracle: missing snapshot and online main-only copy rejected")
	target, err := snapshotCapabilityReserve(fixture.staging, "snapshot'quoted.db")
	if err != nil {
		test.Fatal(err)
	}
	pending, err := fixture.database.BeginTx(test.Context(), nil)
	if err != nil {
		test.Fatal(err)
	}
	defer func() {
		if err := pending.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			test.Error(err)
		}
	}()
	if _, err := pending.ExecContext(test.Context(), "UPDATE snapshot_asset SET revision = 2"); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.source.ExecContext(test.Context(), "VACUUM main INTO ?", target); err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilitySync(target); err != nil {
		test.Fatal(err)
	}
	before, err := os.ReadFile(target)
	if err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilityCheck(test.Context(), target); err != nil {
		test.Fatalf("real WAL snapshot round-trip: %v", err)
	}
	if err := pending.Commit(); err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilityCheck(test.Context(), target); err != nil {
		test.Fatalf("snapshot changed with later source commit: %v", err)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		test.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		test.Fatal("read-only validation modified the snapshot bytes")
	}
	entries, err := os.ReadDir(fixture.staging)
	if err != nil {
		test.Fatal(err)
	}
	if len(entries) != 2 {
		test.Fatal("read-only validation created an unexpected file or sidecar")
	}
	for _, path := range []string{fixture.path, fixture.path + "-wal", fixture.path + "-shm", target} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			test.Fatalf("synthetic database permissions: stat=%v error=%v", info, err)
		}
	}
	if err := os.Truncate(target, 128); err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilityCheck(test.Context(), target); err == nil {
		test.Fatal("oracle accepted a truncated snapshot")
	}
}

func TestSQLiteSnapshotCapabilityTargets(test *testing.T) {
	fixture := snapshotCapabilityOpen(test)
	test.Run("nonempty", func(test *testing.T) {
		target, err := snapshotCapabilityReserve(fixture.staging, "occupied.db")
		if err != nil {
			test.Fatal(err)
		}
		original := []byte("synthetic occupied file")
		if err := os.WriteFile(target, original, 0o600); err != nil {
			test.Fatal(err)
		}
		if _, err := fixture.source.ExecContext(test.Context(), "VACUUM main INTO ?", target); err == nil {
			test.Fatal("VACUUM accepted a nonempty target")
		}
		remaining, err := os.ReadFile(target)
		if err != nil || string(remaining) != string(original) {
			test.Fatal("failed VACUUM changed the occupied target")
		}
		if err := snapshotCapabilityCheck(test.Context(), target); err == nil {
			test.Fatal("oracle accepted a failed occupied target")
		}
	})
	for _, target := range []string{fixture.staging, filepath.Join(fixture.staging, "absent", "snapshot.db")} {
		if _, err := fixture.source.ExecContext(test.Context(), "VACUUM main INTO ?", target); err == nil {
			test.Fatal("VACUUM accepted an unwritable target")
		}
		if err := snapshotCapabilityCheck(test.Context(), target); err == nil {
			test.Fatal("oracle accepted an unwritable target")
		}
	}
	test.Run("private_reservation", func(test *testing.T) {
		outside := snapshotCapabilityDirectory(test)
		victim := filepath.Join(outside, "untouched.db")
		if err := os.WriteFile(victim, []byte("synthetic untouched file"), 0o600); err != nil {
			test.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(fixture.staging, "link.db")); err != nil {
			test.Fatal(err)
		}
		if _, err := snapshotCapabilityReserve(fixture.staging, "link.db"); err == nil {
			test.Fatal("reservation accepted a symbolic link")
		}
		parentLink := filepath.Join(outside, "linked-directory")
		if err := os.Symlink(fixture.staging, parentLink); err != nil {
			test.Fatal(err)
		}
		if _, err := snapshotCapabilityReserve(parentLink, "snapshot.db"); err == nil {
			test.Fatal("reservation accepted a symbolic-link parent")
		}
		untrusted := snapshotCapabilityDirectory(test)
		if err := os.Chmod(untrusted, 0o777); err != nil {
			test.Fatal(err)
		}
		if _, err := snapshotCapabilityReserve(untrusted, "snapshot.db"); err == nil {
			test.Fatal("reservation accepted an untrusted directory")
		}
		if _, err := snapshotCapabilityReserve(fixture.staging, "../escape.db"); err == nil {
			test.Fatal("reservation accepted a nonlocal name")
		}
		remaining, err := os.ReadFile(victim)
		if err != nil || string(remaining) != "synthetic untouched file" {
			test.Fatal("rejected reservation changed the symlink target")
		}
		if err := os.WriteFile(victim, nil, 0o600); err != nil {
			test.Fatal(err)
		}
		link := filepath.Join(fixture.staging, "link.db")
		if _, err := fixture.source.ExecContext(test.Context(), "VACUUM main INTO ?", link); err != nil {
			test.Fatal(err)
		}
		if err := snapshotCapabilitySync(victim); err != nil {
			test.Fatal(err)
		}
		if err := snapshotCapabilityCheck(test.Context(), victim); err != nil {
			test.Fatal("raw driver symlink control did not produce real snapshot data")
		}
		if err := snapshotCapabilityCheck(test.Context(), link); err == nil {
			test.Fatal("oracle accepted a symbolic link despite valid snapshot bytes")
		}
		unsafeTarget := filepath.Join(untrusted, "unsafe.db")
		if err := os.WriteFile(unsafeTarget, nil, 0o600); err != nil {
			test.Fatal(err)
		}
		if _, err := fixture.source.ExecContext(test.Context(), "VACUUM main INTO ?", unsafeTarget); err != nil {
			test.Fatal(err)
		}
		if err := snapshotCapabilityCheck(test.Context(), unsafeTarget); err == nil {
			test.Fatal("oracle accepted an untrusted parent despite successful SQL")
		}
		test.Log("raw SQLite follows symlinks and accepts untrusted parents; fixture host guards, not VACUUM success, enforce these boundaries")
	})
}

type snapshotCapabilityReadFS struct {
	fs.FS
	target string
	armed  atomic.Bool
	block  func() error
}

type snapshotCapabilityReadFile struct {
	fs.File
	seeker io.Seeker
	owner  *snapshotCapabilityReadFS
}

func (filesystem *snapshotCapabilityReadFS) Open(name string) (fs.File, error) {
	file, err := filesystem.FS.Open(name)
	if err != nil {
		return nil, err
	}
	seeker, ok := file.(io.Seeker)
	if !ok {
		return nil, errors.Join(fmt.Errorf("synthetic source must support seeking"), file.Close())
	}
	return &snapshotCapabilityReadFile{File: file, seeker: seeker, owner: filesystem}, nil
}

func (file *snapshotCapabilityReadFile) Seek(offset int64, whence int) (int64, error) {
	return file.seeker.Seek(offset, whence)
}

func (file *snapshotCapabilityReadFile) Read(buffer []byte) (int, error) {
	if file.owner.armed.Load() {
		info, err := os.Stat(file.owner.target)
		if err == nil && info.Size() > 0 && info.Size() < snapshotCapabilityPaddingBytes && file.owner.armed.CompareAndSwap(true, false) {
			if err := file.owner.block(); err != nil {
				return 0, err
			}
		}
	}
	return file.File.Read(buffer)
}

func snapshotCapabilityStandalone(test *testing.T, fixture snapshotCapabilityFixture) string {
	test.Helper()
	target, err := snapshotCapabilityReserve(snapshotCapabilityDirectory(test), "gate-source.db")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.source.ExecContext(test.Context(), "VACUUM main INTO ?", target); err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilitySync(target); err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilityCheck(test.Context(), target); err != nil {
		test.Fatal(err)
	}
	return target
}

func snapshotCapabilityGatedConnection(test *testing.T, source, target string, block func() error) *sql.Conn {
	test.Helper()
	filesystem := &snapshotCapabilityReadFS{FS: os.DirFS(filepath.Dir(source)), target: target, block: block}
	name, registered, err := vfs.New(filesystem)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if err := registered.Close(); err != nil {
			test.Error(err)
		}
	})
	parameters := url.Values{"mode": {"ro"}, "immutable": {"1"}, "vfs": {name}, "_pragma": {"cache_size(-256)"}}
	address := "file:" + filepath.Base(source) + "?" + parameters.Encode()
	database, err := sql.Open("sqlite3", address)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if err := database.Close(); err != nil {
			test.Error(err)
		}
	})
	connection, err := database.Conn(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if err := connection.Close(); err != nil {
			test.Error(err)
		}
	})
	var count int
	if err := connection.QueryRowContext(test.Context(), "SELECT count(*) FROM snapshot_asset").Scan(&count); err != nil || count != 2 {
		test.Fatalf("gated source warmup: count=%d error=%v", count, err)
	}
	filesystem.armed.Store(true)
	return connection
}

func snapshotCapabilityNativeTarget(path string) string {
	name := "unix"
	if runtime.GOOS == "windows" {
		name = "win32"
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: url.Values{"vfs": {name}}.Encode()}).String()
}

func snapshotCapabilityVacuum(ctx context.Context, connection *sql.Conn, target string, ready func(func() error)) error {
	return connection.Raw(func(raw any) error {
		executor, executable := raw.(driver.ExecerContext)
		validator, validatable := raw.(driver.Validator)
		if !executable || !validatable || !validator.IsValid() {
			return fmt.Errorf("snapshot driver must expose a healthy context executor and validator")
		}
		ready(func() error {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			for validator.IsValid() {
				select {
				case <-timer.C:
					return fmt.Errorf("driver did not acknowledge its interrupt while the real read was held")
				default:
					runtime.Gosched()
				}
			}
			return nil
		})
		_, err := executor.ExecContext(ctx, "VACUUM main INTO ?", []driver.NamedValue{{Ordinal: 1, Value: snapshotCapabilityNativeTarget(target)}})
		return err
	})
}

func TestSQLiteSnapshotCapabilityCancel(test *testing.T) {
	fixture := snapshotCapabilityOpen(test)
	preTarget, err := snapshotCapabilityReserve(fixture.staging, "precanceled.db")
	if err != nil {
		test.Fatal(err)
	}
	preCtx, preCancel := context.WithCancel(test.Context())
	preCancel()
	if _, err := fixture.source.ExecContext(preCtx, "VACUUM main INTO ?", preTarget); !errors.Is(err, context.Canceled) {
		test.Fatalf("pre-canceled snapshot: %v", err)
	}
	if err := snapshotCapabilityCheck(test.Context(), preTarget); err == nil {
		test.Fatal("oracle accepted a pre-canceled snapshot")
	}
	target, err := snapshotCapabilityReserve(fixture.staging, "canceled.db")
	if err != nil {
		test.Fatal(err)
	}
	source := snapshotCapabilityStandalone(test, fixture)
	test.Run("cancel_before_gate", func(test *testing.T) {
		target, err := snapshotCapabilityReserve(snapshotCapabilityDirectory(test), "before-gate.db")
		if err != nil {
			test.Fatal(err)
		}
		var reached atomic.Bool
		connection := snapshotCapabilityGatedConnection(test, source, target, func() error {
			reached.Store(true)
			return nil
		})
		ctx, cancel := context.WithCancel(test.Context())
		cancel()
		if _, err := connection.ExecContext(ctx, "VACUUM main INTO ?", snapshotCapabilityNativeTarget(target)); !errors.Is(err, context.Canceled) {
			test.Fatalf("pre-gate cancellation: %v", err)
		}
		if reached.Load() {
			test.Fatal("pre-gate cancellation entered the read barrier")
		}
		if err := snapshotCapabilityCheck(test.Context(), target); err == nil {
			test.Fatal("oracle accepted a pre-gate canceled snapshot")
		}
	})
	entered, release := make(chan struct{}), make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	defer resume()
	var waitInterrupt func() error
	var barrierErr error
	connection := snapshotCapabilityGatedConnection(test, source, target, func() error {
		close(entered)
		<-release
		barrierErr = waitInterrupt()
		return barrierErr
	})
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		err := snapshotCapabilityVacuum(ctx, connection, target, func(wait func() error) { waitInterrupt = wait })
		finished <- err
	}()
	select {
	case <-entered:
		if err := ctx.Err(); err != nil {
			test.Fatalf("context ended before real VACUUM read barrier: %v", err)
		}
	case err := <-finished:
		test.Fatalf("VACUUM returned without its in-flight read barrier: %v", err)
	case <-ctx.Done():
		test.Fatal("VACUUM did not enter its read barrier within ten seconds")
	}
	cancel()
	resume()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			test.Fatalf("in-flight canceled snapshot: %v", err)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("canceled SQL did not drain within five seconds")
	}
	if barrierErr != nil {
		test.Fatal(barrierErr)
	}
	if err := snapshotCapabilityCheck(test.Context(), target); err == nil {
		test.Fatal("oracle accepted an in-flight canceled snapshot")
	}
	var count int
	if err := fixture.database.QueryRowContext(test.Context(), "SELECT count(*) FROM snapshot_asset WHERE revision = 1").Scan(&count); err != nil || count != 2 {
		test.Fatalf("cancellation affected another connection: count=%d error=%v", count, err)
	}
	var healthy int
	if err := connection.QueryRowContext(test.Context(), "SELECT 1").Scan(&healthy); err != nil || healthy != 1 {
		test.Fatalf("dedicated connection did not recover after cancellation: %v", err)
	}
	retry, err := snapshotCapabilityReserve(fixture.staging, "retry.db")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := connection.ExecContext(test.Context(), "VACUUM main INTO ?", snapshotCapabilityNativeTarget(retry)); err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilitySync(retry); err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilityCheck(test.Context(), retry); err != nil {
		test.Fatalf("fresh retry after cancellation failed: %v", err)
	}
	test.Run("release_before_late_cancel", func(test *testing.T) {
		target, err := snapshotCapabilityReserve(snapshotCapabilityDirectory(test), "released.db")
		if err != nil {
			test.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		resume := sync.OnceFunc(func() { close(release) })
		defer resume()
		connection := snapshotCapabilityGatedConnection(test, source, target, func() error {
			close(entered)
			<-release
			return nil
		})
		ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
		defer cancel()
		finished := make(chan error, 1)
		go func() {
			_, err := connection.ExecContext(ctx, "VACUUM main INTO ?", snapshotCapabilityNativeTarget(target))
			finished <- err
		}()
		select {
		case <-entered:
		case err := <-finished:
			test.Fatalf("release control did not enter its barrier: %v", err)
		case <-ctx.Done():
			test.Fatal("release control did not reach its barrier within ten seconds")
		}
		resume()
		select {
		case err := <-finished:
			if err != nil {
				test.Fatal(err)
			}
		case <-ctx.Done():
			test.Fatal("released VACUUM did not complete within ten seconds")
		}
		cancel()
		if err := snapshotCapabilitySync(target); err != nil {
			test.Fatal(err)
		}
		if err := snapshotCapabilityCheck(test.Context(), target); err != nil {
			test.Fatalf("release before late cancellation did not preserve a complete snapshot: %v", err)
		}
		var healthy int
		if err := connection.QueryRowContext(test.Context(), "SELECT 1").Scan(&healthy); err != nil || healthy != 1 {
			test.Fatalf("late cancellation affected a later statement: %v", err)
		}
	})
}

func TestSQLiteSnapshotCapabilityInterruptedProcess(test *testing.T) {
	if os.Getenv("COGNATE_SQLITE_CAPABILITY_CHILD") == "1" {
		target := os.Getenv("COGNATE_SQLITE_CAPABILITY_TARGET")
		ctx, cancel := context.WithTimeout(test.Context(), 7*time.Second)
		defer cancel()
		var operation byte
		var waitInterrupt func() error
		var barrierErr error
		connection := snapshotCapabilityGatedConnection(test, os.Getenv("COGNATE_SQLITE_CAPABILITY_SOURCE"), target, func() error {
			if _, err := fmt.Fprintln(os.Stdout, "VACUUM_READ_BLOCKED"); err != nil {
				return err
			}
			var acknowledgment [1]byte
			if _, err := io.ReadFull(os.Stdin, acknowledgment[:]); err != nil {
				return err
			}
			operation = acknowledgment[0]
			switch operation {
			case 'C':
				cancel()
				barrierErr = waitInterrupt()
				return barrierErr
			case 'R':
			default:
				return fmt.Errorf("unexpected child barrier control")
			}
			return nil
		})
		err := snapshotCapabilityVacuum(ctx, connection, target, func(wait func() error) { waitInterrupt = wait })
		if barrierErr != nil {
			test.Fatal(barrierErr)
		}
		if operation == 'C' {
			if !errors.Is(err, context.Canceled) {
				test.Fatalf("child in-flight cancellation: %v", err)
			}
			if err := snapshotCapabilityCheck(test.Context(), target); err == nil {
				test.Fatal("oracle accepted a canceled child snapshot")
			}
		} else {
			if err != nil {
				test.Fatal(err)
			}
			if operation != 'R' {
				test.Fatal("child VACUUM completed without its read barrier")
			}
			if err := snapshotCapabilitySync(target); err != nil {
				test.Fatal(err)
			}
			if err := snapshotCapabilityCheck(test.Context(), target); err != nil {
				test.Fatal(err)
			}
		}
		var healthy int
		if err := connection.QueryRowContext(test.Context(), "SELECT 1").Scan(&healthy); err != nil || healthy != 1 {
			test.Fatalf("child connection did not recover: %v", err)
		}
		if _, err := fmt.Fprintf(os.Stdout, "VACUUM_CLEAN_%c\n", operation); err != nil {
			test.Fatal(err)
		}
		return
	}
	fixture := snapshotCapabilityOpen(test)
	source := snapshotCapabilityStandalone(test, fixture)
	for _, operation := range []string{"kill", "cancel", "release"} {
		test.Run(operation, func(test *testing.T) { snapshotCapabilityChild(test, source, operation) })
	}
	valid, err := snapshotCapabilityReserve(fixture.staging, "after-crash.db")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.source.ExecContext(test.Context(), "VACUUM main INTO ?", valid); err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilitySync(valid); err != nil {
		test.Fatal(err)
	}
	if err := snapshotCapabilityCheck(test.Context(), valid); err != nil {
		test.Fatalf("source did not survive child interruption and cleanup: %v", err)
	}
}

func snapshotCapabilityChild(test *testing.T, source, operation string) {
	test.Helper()
	directory := snapshotCapabilityDirectory(test)
	target, err := snapshotCapabilityReserve(directory, "child.db")
	if err != nil {
		test.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestSQLiteSnapshotCapabilityInterruptedProcess$", "-test.timeout=8s")
	command.Env = append(os.Environ(), "COGNATE_SQLITE_CAPABILITY_CHILD=1", "COGNATE_SQLITE_CAPABILITY_SOURCE="+source, "COGNATE_SQLITE_CAPABILITY_TARGET="+target)
	control, err := command.StdinPipe()
	if err != nil {
		test.Fatal(err)
	}
	defer control.Close()
	output, err := command.StdoutPipe()
	if err != nil {
		test.Fatal(err)
	}
	if err := command.Start(); err != nil {
		test.Fatal(err)
	}
	finished := make(chan error, 1)
	entered := make(chan error, 1)
	completion := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(output)
		message, err := reader.ReadString('\n')
		if err == nil && message != "VACUUM_READ_BLOCKED\n" {
			err = fmt.Errorf("unexpected child read-barrier acknowledgment")
		}
		entered <- err
		remaining, readErr := io.ReadAll(reader)
		completion <- string(remaining)
		finished <- errors.Join(readErr, command.Wait())
	}()
	joined := false
	defer func() {
		if !joined {
			cancel()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				test.Error("child cleanup did not join within five seconds")
			}
		}
		for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
			if err := os.Remove(target + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				test.Error(err)
			}
		}
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			test.Errorf("child left unexpected temporary files: count=%d error=%v", len(entries), err)
		}
	}()
	select {
	case err := <-entered:
		if err != nil {
			test.Fatalf("child did not reach the real VACUUM read barrier: %v", err)
		}
	case err := <-finished:
		joined = true
		test.Fatalf("child returned without its in-flight read barrier: %v", err)
	case <-ctx.Done():
		test.Fatal("child did not enter its read barrier within ten seconds")
	}
	if operation == "kill" {
		if err := command.Process.Kill(); err != nil {
			test.Fatal(err)
		}
	} else {
		controlByte := byte('C')
		if operation == "release" {
			controlByte = 'R'
		}
		if _, err := control.Write([]byte{controlByte}); err != nil {
			test.Fatal(err)
		}
	}
	select {
	case err := <-finished:
		joined = true
		if operation == "kill" && err == nil {
			test.Fatal("killed child exited successfully")
		}
		if operation != "kill" && err != nil {
			test.Fatalf("controlled child failed to exit cleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("interrupted child did not drain within five seconds")
	}
	if command.ProcessState == nil {
		test.Fatal("child process was not reaped")
	}
	message := <-completion
	if operation != "kill" {
		expected := "VACUUM_CLEAN_C\n"
		if operation == "release" {
			expected = "VACUUM_CLEAN_R\n"
		}
		if !strings.HasPrefix(message, expected) {
			test.Fatal("child did not acknowledge the requested completion mode")
		}
	}
	inspectionErr := snapshotCapabilityCheck(test.Context(), target)
	if operation == "release" && inspectionErr != nil {
		test.Fatalf("released child did not create a complete snapshot: %v", inspectionErr)
	}
	if operation != "release" && inspectionErr == nil {
		test.Fatal("restart inspection accepted an interrupted child snapshot")
	}
}
