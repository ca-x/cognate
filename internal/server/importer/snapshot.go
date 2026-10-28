package importer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const MaxCCSwitchSnapshotBytes = 16 << 20

func PreviewCCSwitchSnapshot(ctx context.Context, data []byte) (RepositoryPreview, error) {
	if err := ctx.Err(); err != nil {
		return RepositoryPreview{}, err
	}
	if !validCCSwitchSnapshot(data) {
		return RepositoryPreview{}, errors.New("invalid cc-switch snapshot")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return RepositoryPreview{}, databaseError(ctx)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	connection, err := database.Conn(ctx)
	if err != nil {
		return RepositoryPreview{}, databaseError(ctx)
	}
	defer connection.Close()
	if err := prepareCCSwitchSnapshot(ctx, connection, data); err != nil {
		return RepositoryPreview{}, databaseError(ctx)
	}
	var integrity string
	if err := connection.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&integrity); err != nil || integrity != "ok" {
		return RepositoryPreview{}, databaseError(ctx)
	}
	var kind string
	if err := connection.QueryRowContext(ctx, `SELECT type FROM pragma_table_list('skill_repos') WHERE schema = 'main'`).Scan(&kind); err != nil || kind != "table" {
		return RepositoryPreview{}, databaseError(ctx)
	}
	var generated int
	if err := connection.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_xinfo('skill_repos') WHERE name IN ('owner','name','branch','enabled') AND hidden != 0`).Scan(&generated); err != nil || generated != 0 {
		return RepositoryPreview{}, databaseError(ctx)
	}
	input, err := readCCSwitchRepositoryRows(ctx, connection)
	if err != nil {
		return RepositoryPreview{}, err
	}
	return transformCCSwitchRepositories(ctx, input)
}

func validCCSwitchSnapshot(data []byte) bool {
	if len(data) < 512 || len(data) > MaxCCSwitchSnapshotBytes || !bytes.Equal(data[:16], []byte("SQLite format 3\x00")) || data[18] != 1 || data[19] != 1 {
		return false
	}
	pageSize := int(binary.BigEndian.Uint16(data[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	if pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 || len(data)%pageSize != 0 {
		return false
	}
	return uint64(binary.BigEndian.Uint32(data[28:32])) == uint64(len(data)/pageSize) && bytes.Equal(data[24:28], data[92:96])
}

func prepareCCSwitchSnapshot(ctx context.Context, connection *sql.Conn, data []byte) error {
	for _, limit := range []struct{ identifier, value int }{
		{sqlite3.SQLITE_LIMIT_LENGTH, 1 << 20},
		{sqlite3.SQLITE_LIMIT_SQL_LENGTH, 64 << 10},
		{sqlite3.SQLITE_LIMIT_COLUMN, 128},
		{sqlite3.SQLITE_LIMIT_EXPR_DEPTH, 64},
		{sqlite3.SQLITE_LIMIT_COMPOUND_SELECT, 16},
		{sqlite3.SQLITE_LIMIT_ATTACHED, 0},
		{sqlite3.SQLITE_LIMIT_TRIGGER_DEPTH, 0},
		{sqlite3.SQLITE_LIMIT_WORKER_THREADS, 0},
	} {
		if _, err := sqlite.Limit(connection, limit.identifier, limit.value); err != nil {
			return err
		}
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA query_only=ON; PRAGMA trusted_schema=OFF; PRAGMA temp_store=MEMORY; PRAGMA cache_size=-1024;`); err != nil {
		return err
	}
	if err := connection.Raw(func(driverConnection any) error {
		deserializer, ok := driverConnection.(interface{ Deserialize([]byte) error })
		if !ok {
			return errors.New("snapshot deserialization unavailable")
		}
		return deserializer.Deserialize(data)
	}); err != nil {
		return err
	}
	_, err := connection.ExecContext(ctx, `PRAGMA query_only=ON; PRAGMA trusted_schema=OFF; PRAGMA temp_store=MEMORY; PRAGMA cache_size=-1024;`)
	return err
}
