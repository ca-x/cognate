package importer

import (
	"database/sql"
	"testing"
)

func TestCCSwitchSnapshotEffectivePolicy(test *testing.T) {
	for _, policy := range []string{"", "PRAGMA default_cache_size=2147483647;"} {
		source, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() { source.Close() })
		sourceConnection, err := source.Conn(test.Context())
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() { sourceConnection.Close() })
		if _, err := sourceConnection.ExecContext(test.Context(), `CREATE TABLE skill_repos(owner TEXT, name TEXT, branch TEXT, enabled INTEGER);`+policy); err != nil {
			test.Fatal(err)
		}
		var data []byte
		if err := sourceConnection.Raw(func(connection any) error {
			var err error
			data, err = connection.(interface{ Serialize() ([]byte, error) }).Serialize()
			return err
		}); err != nil {
			test.Fatal(err)
		}
		target, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() { target.Close() })
		connection, err := target.Conn(test.Context())
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() { connection.Close() })
		if err := prepareCCSwitchSnapshot(test.Context(), connection, data); err != nil {
			test.Fatal(err)
		}
		for _, restriction := range []struct {
			pragma string
			want   int
		}{{"cache_size", -1024}, {"query_only", 1}, {"trusted_schema", 0}, {"temp_store", 2}} {
			var actual int
			if err := connection.QueryRowContext(test.Context(), "PRAGMA "+restriction.pragma).Scan(&actual); err != nil || actual != restriction.want {
				test.Errorf("effective %s = %d, want %d (query failed: %t)", restriction.pragma, actual, restriction.want, err != nil)
			}
		}
		if _, err := connection.ExecContext(test.Context(), `CREATE TABLE forbidden(value TEXT)`); err == nil {
			test.Fatal("snapshot remained writable")
		}
		if _, err := connection.ExecContext(test.Context(), `ATTACH ':memory:' AS forbidden`); err == nil {
			test.Fatal("snapshot allowed attached databases")
		}
	}
}
