package importer_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/importer"
	"modernc.org/sqlite"
)

func ccSwitchSnapshot(test *testing.T, statements string) []byte {
	test.Helper()
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	connection, err := database.Conn(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(test.Context(), statements); err != nil {
		test.Fatal(err)
	}
	var data []byte
	if err := connection.Raw(func(driverConnection any) error {
		serializer, ok := driverConnection.(interface{ Serialize() ([]byte, error) })
		if !ok {
			return errors.New("SQLite serialization unavailable")
		}
		data, err = serializer.Serialize()
		return err
	}); err != nil {
		test.Fatal(err)
	}
	return data
}

const snapshotSchema = `CREATE TABLE skill_repos(owner TEXT, name TEXT, branch TEXT, enabled INTEGER);`

func TestCCSwitchSnapshotPreviewNoDiskOrSecrets(test *testing.T) {
	temporary := test.TempDir()
	test.Setenv("TMPDIR", temporary)
	data := ccSwitchSnapshot(test, snapshotSchema+`INSERT INTO skill_repos VALUES ('Example','One','HEAD',1),('example','two','head',0),('example','three','dev',1); CREATE TABLE providers(secret TEXT); INSERT INTO providers VALUES ('SNAPSHOT-SECRET-SENTINEL');`)
	before := bytes.Clone(data)
	for range 2 {
		preview, err := importer.PreviewCCSwitchSnapshot(test.Context(), data)
		if err != nil || !preview.Found || len(preview.Repositories) != 3 {
			test.Fatalf("snapshot preview missing expected repositories: %v", err)
		}
		if preview.Repositories[0].Branch != "" || !preview.Repositories[0].Enabled || preview.Repositories[2].Branch != "" || preview.Repositories[2].Enabled || preview.Repositories[1].Branch != "dev" {
			test.Fatal("snapshot lost HEAD normalization, source ordering or enabled state")
		}
	}
	if !bytes.Equal(data, before) {
		test.Fatal("snapshot preview modified caller bytes")
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		test.Fatal("snapshot preview created filesystem artifacts")
	}
}

func TestCCSwitchSnapshotRejectsMalformedAndWAL(test *testing.T) {
	valid := ccSwitchSnapshot(test, snapshotSchema)
	wal := bytes.Clone(valid)
	wal[18], wal[19] = 2, 2
	wrongPageCount := bytes.Clone(valid)
	binary.BigEndian.PutUint32(wrongPageCount[28:32], 0x7fffffff)
	for _, data := range [][]byte{nil, {}, []byte("SNAPSHOT-SECRET-SENTINEL"), valid[:100], append(bytes.Clone(valid), 0), wal, wrongPageCount, make([]byte, importer.MaxCCSwitchSnapshotBytes+1)} {
		preview, err := importer.PreviewCCSwitchSnapshot(test.Context(), data)
		if err == nil || preview.Found || len(preview.Repositories) != 0 || strings.Contains(err.Error(), "SNAPSHOT-SECRET-SENTINEL") {
			test.Fatal("invalid or non-self-contained snapshot was not safely rejected")
		}
	}
}

func TestCCSwitchSnapshotSchemaAndRowLimits(test *testing.T) {
	for _, statements := range []string{
		`CREATE TABLE unrelated(value TEXT);`,
		`CREATE VIEW skill_repos AS SELECT 'example' owner, 'one' name, '' branch, 1 enabled;`,
		`CREATE VIRTUAL TABLE skill_repos USING fts5(owner,name,branch,enabled);`,
		`CREATE TABLE skill_repos(owner TEXT, name TEXT, branch TEXT GENERATED ALWAYS AS ('HEAD') VIRTUAL, enabled INTEGER);`,
		snapshotSchema + `INSERT INTO skill_repos VALUES ('../escape','one','HEAD',1);`,
		snapshotSchema + `INSERT INTO skill_repos VALUES ('example','one','HEAD',2);`,
		snapshotSchema + `INSERT INTO skill_repos VALUES ('example','one',printf('%0300d',1),1);`,
		snapshotSchema + `WITH RECURSIVE records(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM records WHERE value<1001) INSERT INTO skill_repos SELECT 'example','repo-'||value,'HEAD',1 FROM records;`,
	} {
		data := ccSwitchSnapshot(test, statements)
		preview, err := importer.PreviewCCSwitchSnapshot(test.Context(), data)
		if err == nil || preview.Found || len(preview.Repositories) != 0 {
			test.Fatal("unsupported schema or invalid record was accepted")
		}
	}
}

func TestCCSwitchSnapshotEmptyAndCancellation(test *testing.T) {
	data := ccSwitchSnapshot(test, snapshotSchema)
	preview, err := importer.PreviewCCSwitchSnapshot(test.Context(), data)
	if err != nil || !preview.Found || len(preview.Repositories) != 0 {
		test.Fatalf("empty repository snapshot: %v", err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := importer.PreviewCCSwitchSnapshot(ctx, data); !errors.Is(err, context.Canceled) {
		test.Fatalf("canceled snapshot preview: %v", err)
	}
}

func TestCCSwitchSnapshotUsesCommittedWALBackup(test *testing.T) {
	directory := test.TempDir()
	sourcePath := filepath.Join(directory, "live.db")
	exportPath := filepath.Join(directory, "export.db")
	database, err := sql.Open("sqlite3", sourcePath)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(test.Context(), `PRAGMA journal_mode=WAL;`+snapshotSchema+`INSERT INTO skill_repos VALUES ('example','committed-wal','HEAD',1);`); err != nil {
		test.Fatal(err)
	}
	rawMain, err := os.ReadFile(sourcePath)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := importer.PreviewCCSwitchSnapshot(test.Context(), rawMain); err == nil {
		test.Fatal("raw WAL main file was treated as a complete export")
	}
	connection, err := database.Conn(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer connection.Close()
	if err := connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("SQLite backup unavailable")
		}
		backup, err := backuper.NewBackup(exportPath)
		if err != nil {
			return err
		}
		defer backup.Finish()
		if more, err := backup.Step(-1); err != nil {
			return err
		} else if more {
			return errors.New("backup did not complete")
		}
		return nil
	}); err != nil {
		test.Fatal(err)
	}
	exported, err := sql.Open("sqlite3", exportPath)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := exported.ExecContext(test.Context(), `PRAGMA journal_mode=DELETE`); err != nil {
		_ = exported.Close()
		test.Fatal(err)
	}
	if err := exported.Close(); err != nil {
		test.Fatal(err)
	}
	data, err := os.ReadFile(exportPath)
	if err != nil {
		test.Fatal(err)
	}
	preview, err := importer.PreviewCCSwitchSnapshot(test.Context(), data)
	if err != nil || !preview.Found || len(preview.Repositories) != 1 || preview.Repositories[0].Name != "committed-wal" {
		test.Fatalf("engine-produced backup lost committed WAL rows: %v; size=%d header=%x", err, len(data), data[16:100])
	}
}
