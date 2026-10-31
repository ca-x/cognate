//go:build linux

package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestSnapshotResourceDomains(test *testing.T) {
	if os.Getenv("COGNATE_SNAPSHOT_RESOURCE_TEST") == "1" {
		limits := LocalSnapshotLimits{MaxDBBytes: 8 << 20, MaxInventoryBytes: 4 << 20, MaxRecords: 100000, MaxStagingBytes: 64 << 20, SQLiteHeapBytes: 32 << 20, WorkerAddressSpaceBytes: 16 << 30, WorkerCPUSeconds: 10, MaxWALBytes: 64 << 20, MaxSHMBytes: 1 << 20, DrainTimeout: time.Second, OperationTimeout: 10 * time.Second}
		var response snapshotWorkerResponse
		if snapshotInstallControls(limits, &response) != nil {
			test.Fatal("resource controls failed before source")
		}
		for _, expected := range []struct {
			resource int
			value    uint64
		}{{unix.RLIMIT_CORE, 0}, {unix.RLIMIT_AS, 16 << 30}, {unix.RLIMIT_CPU, 10}, {unix.RLIMIT_FSIZE, 8 << 20}} {
			var actual unix.Rlimit
			if unix.Getrlimit(expected.resource, &actual) != nil || actual.Cur != expected.value || actual.Max != expected.value {
				test.Fatal("actual kernel resource limit differs")
			}
		}
		file, err := os.CreateTemp(test.TempDir(), "mapping-")
		if err != nil {
			test.Fatal(err)
		}
		defer file.Close()
		if err = file.Truncate(4096); err != nil {
			test.Fatal(err)
		}
		mapping, err := unix.Mmap(int(file.Fd()), 0, 16<<30, unix.PROT_READ, unix.MAP_PRIVATE)
		if err == nil {
			_ = unix.Munmap(mapping)
			test.Fatal("kernel admitted mapping beyond absolute AS cap")
		}
		if err != unix.ENOMEM {
			test.Fatal("mapping refusal was not AS exhaustion")
		}
		tls := libc.NewTLS()
		defer tls.Close()
		if sqlite3.Xsqlite3_hard_heap_limit64(tls, -1) != 32<<20 {
			test.Fatal("heap limit readback differs")
		}
		allocation := sqlite3.Xsqlite3_malloc64(tls, 32<<20+1)
		if allocation != 0 {
			sqlite3.Xsqlite3_free(tls, allocation)
			test.Fatal("SQLite allocation exceeded heap cap")
		}
		return
	}
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSnapshotResourceDomains$", "-test.v")
	command.Env = append(os.Environ(), "COGNATE_SNAPSHOT_RESOURCE_TEST=1")
	output, err := command.CombinedOutput()
	if err != nil {
		test.Fatalf("real resource child rejected: %s", output)
	}
}

func TestSnapshotTask0TrustedCatalog(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "source.sqlite")
	store, err := Open(test.Context(), path, bytes.Repeat([]byte{91}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err = store.CreateUser(test.Context(), "snapshot-task0-owner"); err != nil {
		test.Fatal(err)
	}
	pool, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String())
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	rows, err := pool.QueryContext(test.Context(), "SELECT type,name,tbl_name,sql FROM main.sqlite_schema ORDER BY type COLLATE BINARY,name COLLATE BINARY")
	if err != nil {
		test.Fatal(err)
	}
	hash := sha256.New()
	hash.Write([]byte("CGB2SC01"))
	count := 0
	for rows.Next() {
		var fields [4]sql.NullString
		if err = rows.Scan(&fields[0], &fields[1], &fields[2], &fields[3]); err != nil {
			test.Fatal(err)
		}
		for index, field := range fields {
			if index < 3 && !field.Valid {
				test.Fatal("trusted catalog key is NULL")
			}
		}
		hash.Write(snapshotCatalogTestRecord(fields))
		count++
	}
	if err = rows.Err(); err != nil {
		test.Fatal(err)
	}
	if err = rows.Close(); err != nil {
		test.Fatal(err)
	}
	hash.Write([]byte{255})
	var encodedCount [4]byte
	binary.BigEndian.PutUint32(encodedCount[:], uint32(count))
	hash.Write(encodedCount[:])
	if fmt.Sprintf("%x", hash.Sum(nil)) != "89fb8b48f9321c2777b75e5fb552d34938ae7bb9985874292847ee99116e0129" || count != 92 {
		test.Fatal("independent complete catalog golden differs")
	}
	applicationHash, err := snapshotSchema(test.Context(), pool)
	if err != nil || fmt.Sprintf("%x", applicationHash) != "34738287b1235358fc73bbefed6fabdde0968bf8340dee784fc0a9b448bc9235" {
		test.Fatal("complete census changed original application fingerprint")
	}
	test.Logf("TRUSTED_COMPLETE_CATALOG=%x ENTRIES=%d APPLICATION=%x", hash.Sum(nil), count, applicationHash)
	info, err := os.Stat(path + "-wal")
	if err != nil || info.Size() <= 32 {
		test.Fatal("fixture lacks committed real WAL")
	}
}

func TestSnapshotUnknownSchemaRefusal(test *testing.T) {
	for _, name := range []string{"unapproved_review", "sqliteevil", "sqliteXreview", "SQLITEXreview", "sqlite", "sqlite%review", "sqlite-review"} {
		test.Run(name, func(test *testing.T) {
			store, path := snapshotTestStore(test)
			database, err := sql.Open("sqlite3", snapshotAddress(path, "rw"))
			if err != nil {
				test.Fatal(err)
			}
			defer func() {
				if database.Close() != nil {
					test.Error("schema fixture close failed")
				}
			}()
			if _, err = store.WithMaintenanceWriter(test.Context(), func(ctx context.Context) (bool, error) {
				_, failure := database.ExecContext(ctx, fmt.Sprintf("CREATE TABLE \"%s\"(value INTEGER); INSERT INTO \"%s\" VALUES(73)", name, name))
				return failure == nil, failure
			}); err != nil {
				test.Fatal("legal unknown schema fixture failed")
			}
			var found int
			if database.QueryRowContext(test.Context(), "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=? COLLATE BINARY", name).Scan(&found) != nil || found != 1 {
				test.Fatal("unfiltered exact-name census lost unknown schema")
			}
			called := false
			summary, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error {
				called = true
				return nil
			})
			if !errors.Is(err, ErrLocalSnapshotInvalid) || called || summary.get != nil {
				test.Fatal("unknown schema reached snapshot callback or summary")
			}
		})
	}
}

func TestSnapshotSQLiteInternalPrefix(test *testing.T) {
	store, path := snapshotTestStore(test)
	database, err := sql.Open("sqlite3", snapshotAddress(path, "rw"))
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	for _, name := range []string{"sqlite_review", "SQLITE_REVIEW"} {
		if _, err := database.ExecContext(test.Context(), fmt.Sprintf("CREATE TABLE \"%s\"(value INTEGER)", name)); err == nil {
			test.Fatal("SQLite accepted a reserved internal object name")
		}
	}
	summary, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil })
	if err != nil || summary.get == nil {
		test.Fatal("reserved-name refusal changed trusted baseline admission")
	}
}

func TestSnapshotPersistentCatalogRefusal(test *testing.T) {
	for _, scenario := range []struct {
		name, mutation, object, payload string
		parserRefusal                   bool
		rows                            int
	}{
		{name: "literal", mutation: "CREATE TABLE sqlite_review(value INTEGER); INSERT INTO sqlite_review VALUES(73)", object: "sqlite_review", payload: "SELECT count(*) FROM sqlite_review WHERE value=73", rows: 93},
		{name: "uppercase", mutation: "CREATE TABLE SQLITE_REVIEW(value INTEGER); INSERT INTO SQLITE_REVIEW VALUES(73)", object: "SQLITE_REVIEW", payload: "SELECT count(*) FROM SQLITE_REVIEW WHERE value=73", rows: 93},
		{name: "fake-autoindex", mutation: "CREATE TABLE sqlite_autoindex_review_1(value INTEGER); INSERT INTO sqlite_autoindex_review_1 VALUES(73)", object: "sqlite_autoindex_review_1", payload: "SELECT count(*) FROM sqlite_autoindex_review_1 WHERE value=73", rows: 93},
		{name: "rename-application-same-count", mutation: "ALTER TABLE users RENAME TO sqlite_renamed_application", object: "sqlite_renamed_application", payload: "SELECT count(*) FROM sqlite_renamed_application WHERE user_key='snapshot-private-owner'", rows: 92},
		{name: "known-sequence-definition", mutation: "UPDATE sqlite_schema SET sql='CREATE TABLE sqlite_sequence(name,seq,review)' WHERE name='sqlite_sequence'", object: "sqlite_sequence", payload: "SELECT count(*) FROM users WHERE user_key='snapshot-private-owner'", rows: 92},
		{name: "known-index-type-readable", mutation: "UPDATE sqlite_schema SET type='table' WHERE name='sqlite_autoindex_category_skills_1'", object: "sqlite_autoindex_category_skills_1", payload: "SELECT count(*) FROM users WHERE user_key='snapshot-private-owner'", rows: 92},
		{name: "known-index-owner-readable", mutation: "UPDATE sqlite_schema SET tbl_name='users' WHERE name='sqlite_autoindex_category_skills_1'", object: "sqlite_autoindex_category_skills_1", payload: "SELECT count(*) FROM users WHERE user_key='snapshot-private-owner'", rows: 92},
		{name: "invalid-sequence-sql-parser", mutation: "UPDATE sqlite_schema SET sql='not sql' WHERE name='sqlite_sequence'", object: "sqlite_sequence", parserRefusal: true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			store, path := snapshotTestStore(test)
			database, err := sql.Open("sqlite3", snapshotAddress(path, "rw"))
			if err != nil {
				test.Fatal(err)
			}
			database.SetMaxOpenConns(1)
			_, mutateError := store.WithMaintenanceWriter(test.Context(), func(ctx context.Context) (bool, error) {
				_, failure := database.ExecContext(ctx, "PRAGMA writable_schema=ON; "+scenario.mutation+"; PRAGMA writable_schema=OFF")
				return failure == nil, failure
			})
			closeError := database.Close()
			if mutateError != nil || closeError != nil {
				test.Fatal("persistent mutation setup failed")
			}
			fresh, err := sql.Open("sqlite3", snapshotAddress(path, "ro"))
			if err != nil {
				test.Fatal(err)
			}
			var objects, total, payload int
			queryError := fresh.QueryRowContext(test.Context(), "SELECT count(*) FROM sqlite_schema WHERE name=? COLLATE BINARY", scenario.object).Scan(&objects)
			if scenario.parserRefusal {
				if queryError == nil {
					_ = fresh.Close()
					test.Fatal("expected parser refusal was not observed")
				}
				test.Log("PARSER_EARLY_REFUSAL_NOT_CENSUS_BRANCH")
			} else {
				if queryError != nil || objects != 1 || fresh.QueryRowContext(test.Context(), "SELECT count(*) FROM sqlite_schema").Scan(&total) != nil || total != scenario.rows || fresh.QueryRowContext(test.Context(), scenario.payload).Scan(&payload) != nil || payload != 1 {
					_ = fresh.Close()
					test.Fatal("fresh read-only connection did not confirm persistent schema and payload")
				}
				test.Log("FRESH_READABLE_PERSISTENT_SCHEMA_AND_PAYLOAD")
			}
			if fresh.Close() != nil {
				test.Fatal("fresh census close failed")
			}
			called := false
			summary, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { called = true; return nil })
			refused := errors.Is(err, ErrLocalSnapshotInvalid)
			if scenario.parserRefusal {
				refused = refused || errors.Is(err, ErrLocalSnapshotBudget)
				test.Logf("PARSER_EARLY_RESULT invalid=%t budget=%t callback=%t summary=%t", errors.Is(err, ErrLocalSnapshotInvalid), errors.Is(err, ErrLocalSnapshotBudget), called, summary.get != nil)
			}
			if !refused || called || summary.get != nil {
				test.Fatal("persistent unknown catalog reached snapshot callback or summary")
			}
		})
	}
}

func snapshotCatalogTestRecord(fields [4]sql.NullString) []byte {
	var output bytes.Buffer
	output.WriteByte(1)
	for _, field := range fields {
		if !field.Valid {
			output.WriteByte(0)
			continue
		}
		output.WriteByte(1)
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(field.String)))
		output.Write(length[:])
		output.WriteString(field.String)
	}
	return output.Bytes()
}

func snapshotCatalogTestPool(test *testing.T) *sql.DB {
	test.Helper()
	pool, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		test.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	test.Cleanup(func() {
		if pool.Close() != nil {
			test.Error("catalog rowset pool close failed")
		}
	})
	if _, err := pool.ExecContext(test.Context(), "CREATE TABLE review_catalog(type,name,tbl_name,sql)"); err != nil {
		test.Fatal(err)
	}
	return pool
}

func TestSnapshotCatalogNullGolden(test *testing.T) {
	for _, scenario := range []struct {
		name, expected string
		value          any
	}{
		{name: "null", expected: "4347423253433031010100000005696e64657801000000076578616d706c6501000000057461626c6500ff00000001"},
		{name: "empty-text", value: "", expected: "4347423253433031010100000005696e64657801000000076578616d706c6501000000057461626c650100000000ff00000001"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			pool := snapshotCatalogTestPool(test)
			rows, err := pool.QueryContext(test.Context(), "SELECT 'index','example','table',?", scenario.value)
			if err != nil {
				test.Fatal(err)
			}
			if !rows.Next() {
				_ = rows.Close()
				test.Fatal("literal SQL cell missing")
			}
			var fields [4]sql.NullString
			scanError := rows.Scan(&fields[0], &fields[1], &fields[2], &fields[3])
			closeError := rows.Close()
			if scanError != nil || closeError != nil {
				test.Fatal("literal SQL cell read failed")
			}
			encoded := append([]byte("CGB2SC01"), snapshotCatalogTestRecord(fields)...)
			encoded = append(encoded, 255, 0, 0, 0, 1)
			if fmt.Sprintf("%x", encoded) != scenario.expected {
				test.Fatal("NULL and empty text lost independent transcript identity")
			}
		})
	}
}

func TestSnapshotCatalogCells(test *testing.T) {
	for _, scenario := range []struct {
		name        string
		column      int
		value       any
		valid, null bool
	}{
		{name: "genuine-null", column: 3, valid: true, null: true},
		{name: "empty-text", column: 3, value: "", valid: true},
		{name: "definition", column: 3, value: "definition", valid: true},
		{name: "sql-exact", column: 3, value: strings.Repeat("a", 65536), valid: true},
		{name: "sql-overlong-projected-null", column: 3, value: strings.Repeat("a", 65537)},
		{name: "sql-blob-projected-null", column: 3, value: []byte("definition")},
		{name: "sql-integer-projected-null", column: 3, value: 73},
		{name: "type-exact", column: 0, value: strings.Repeat("a", 64), valid: true, null: true},
		{name: "type-overlong", column: 0, value: strings.Repeat("a", 65)},
		{name: "type-empty", column: 0, value: ""},
		{name: "type-integer", column: 0, value: 73},
		{name: "name-exact", column: 1, value: strings.Repeat("a", 256), valid: true, null: true},
		{name: "name-overlong", column: 1, value: strings.Repeat("a", 257)},
		{name: "name-null", column: 1},
		{name: "name-multibyte-exact", column: 1, value: strings.Repeat("é", 128), valid: true, null: true},
		{name: "name-multibyte-overlong", column: 1, value: strings.Repeat("é", 129)},
		{name: "table-exact", column: 2, value: strings.Repeat("a", 256), valid: true, null: true},
		{name: "table-overlong", column: 2, value: strings.Repeat("a", 257)},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			pool := snapshotCatalogTestPool(test)
			fields := [4]any{"index", "example", "owner", nil}
			fields[scenario.column] = scenario.value
			if _, err := pool.ExecContext(test.Context(), "INSERT INTO review_catalog VALUES(?,?,?,?)", fields[:]...); err != nil {
				test.Fatal(err)
			}
			rows, err := pool.QueryContext(test.Context(), "SELECT "+snapshotCatalogColumns+" FROM review_catalog")
			if err != nil {
				test.Fatal(err)
			}
			if !rows.Next() {
				_ = rows.Close()
				test.Fatal("projected catalog cell missing")
			}
			row, scanError := snapshotScanCatalogRow(rows)
			closeError := rows.Close()
			if closeError != nil || (scanError == nil) != scenario.valid || scenario.valid && row.definitionNull != scenario.null || !scenario.valid && (!errors.Is(scanError, ErrLocalSnapshotInvalid) || row != (snapshotCatalogRow{})) {
				test.Fatal("bounded SQL cell state lost NULL/type/byte-length distinction")
			}
		})
	}
}

func snapshotCatalogCleanRowset(test *testing.T) *sql.DB {
	test.Helper()
	_, path := snapshotTestStore(test)
	source, err := sql.Open("sqlite3", snapshotAddress(path, "ro"))
	if err != nil {
		test.Fatal(err)
	}
	defer source.Close()
	rows, err := source.QueryContext(test.Context(), "SELECT type,name,tbl_name,sql FROM main.sqlite_schema ORDER BY type COLLATE BINARY,name COLLATE BINARY")
	if err != nil {
		test.Fatal(err)
	}
	defer rows.Close()
	pool := snapshotCatalogTestPool(test)
	count := 0
	for rows.Next() {
		var fields [4]any
		if err := rows.Scan(&fields[0], &fields[1], &fields[2], &fields[3]); err != nil {
			test.Fatal(err)
		}
		if _, err := pool.ExecContext(test.Context(), "INSERT INTO review_catalog VALUES(?,?,?,?)", fields[:]...); err != nil {
			test.Fatal(err)
		}
		count++
	}
	if rows.Err() != nil || count != 92 {
		test.Fatal("clean unfiltered rowset fixture differs")
	}
	return pool
}

func TestSnapshotCatalogClosedRowsets(test *testing.T) {
	for _, scenario := range []struct {
		name, mutation string
		arguments      []any
		valid          bool
	}{
		{name: "complete-clean", valid: true},
		{name: "known-index-type", mutation: "UPDATE review_catalog SET type='table' WHERE name='sqlite_autoindex_category_skills_1'"},
		{name: "known-index-owner", mutation: "UPDATE review_catalog SET tbl_name='users' WHERE name='sqlite_autoindex_category_skills_1'"},
		{name: "known-index-empty-text", mutation: "UPDATE review_catalog SET sql='' WHERE name='sqlite_autoindex_category_skills_1'"},
		{name: "known-index-text", mutation: "UPDATE review_catalog SET sql='CREATE INDEX review ON category_skills(skill_id)' WHERE name='sqlite_autoindex_category_skills_1'"},
		{name: "known-index-overlong-projected-null", mutation: "UPDATE review_catalog SET sql=? WHERE name='sqlite_autoindex_category_skills_1'", arguments: []any{strings.Repeat("a", 65537)}},
		{name: "known-index-blob-projected-null", mutation: "UPDATE review_catalog SET sql=CAST('definition' AS BLOB) WHERE name='sqlite_autoindex_category_skills_1'"},
		{name: "known-sequence-null", mutation: "UPDATE review_catalog SET sql=NULL WHERE name='sqlite_sequence'"},
		{name: "known-sequence-empty", mutation: "UPDATE review_catalog SET sql='' WHERE name='sqlite_sequence'"},
		{name: "known-sequence-definition", mutation: "UPDATE review_catalog SET sql='CREATE TABLE sqlite_sequence(name,seq,review)' WHERE name='sqlite_sequence'"},
		{name: "case-changed-name", mutation: "UPDATE review_catalog SET name='SQLITE_sequence' WHERE name='sqlite_sequence'"},
		{name: "missing-internal", mutation: "DELETE FROM review_catalog WHERE name='sqlite_sequence'"},
		{name: "duplicate-internal", mutation: "INSERT INTO review_catalog SELECT * FROM review_catalog WHERE name='sqlite_sequence'"},
		{name: "same-count-missing-and-duplicate", mutation: "DELETE FROM review_catalog WHERE name='sqlite_sequence'; INSERT INTO review_catalog SELECT * FROM review_catalog WHERE name='sqlite_autoindex_category_skills_1'"},
		{name: "missing-application", mutation: "DELETE FROM review_catalog WHERE name='users'"},
		{name: "null-application", mutation: "UPDATE review_catalog SET sql=NULL WHERE name='users'"},
		{name: "extra-fake-internal", mutation: "INSERT INTO review_catalog VALUES('table','sqlite_review','sqlite_review','CREATE TABLE sqlite_review(value INTEGER)')"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			pool := snapshotCatalogCleanRowset(test)
			if scenario.mutation != "" {
				if _, err := pool.ExecContext(test.Context(), scenario.mutation, scenario.arguments...); err != nil {
					test.Fatal("real SQL rowset mutation failed")
				}
			}
			rows, err := pool.QueryContext(test.Context(), "SELECT "+snapshotCatalogColumns+" FROM review_catalog ORDER BY type COLLATE BINARY,name COLLATE BINARY")
			if err != nil {
				test.Fatal(err)
			}
			fingerprint, err := snapshotReadCatalog(test.Context(), rows)
			if scenario.valid {
				if err != nil || fmt.Sprintf("%x", fingerprint) != "34738287b1235358fc73bbefed6fabdde0968bf8340dee784fc0a9b448bc9235" {
					test.Fatal("complete clean SQL rowset refused")
				}
			} else if !errors.Is(err, ErrLocalSnapshotInvalid) || fingerprint != ([32]byte{}) {
				test.Fatal("closed SQL census admitted an unknown or incomplete tuple set")
			}
		})
	}
}

func TestSnapshotCatalogCancellationAndOrder(test *testing.T) {
	for _, canceled := range []bool{false, true} {
		test.Run(fmt.Sprintf("canceled-%t", canceled), func(test *testing.T) {
			pool := snapshotCatalogCleanRowset(test)
			rows, err := pool.QueryContext(test.Context(), "SELECT "+snapshotCatalogColumns+" FROM review_catalog ORDER BY type COLLATE BINARY DESC,name COLLATE BINARY DESC")
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			if canceled {
				cancel()
			}
			fingerprint, err := snapshotReadCatalog(ctx, rows)
			expected := ErrLocalSnapshotInvalid
			if canceled {
				expected = context.Canceled
			}
			if !errors.Is(err, expected) || fingerprint != ([32]byte{}) || pool.PingContext(test.Context()) != nil {
				test.Fatal("failed catalog retained rows, minted fingerprint or lost cancellation/order")
			}
		})
	}
}
