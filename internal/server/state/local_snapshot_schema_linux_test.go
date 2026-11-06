//go:build linux

package state

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"modernc.org/sqlite"
)

func TestSnapshotSchemaVersionGoldens(test *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "repository-sources"
		if legacy {
			name = "0.6.2"
		}
		test.Run(name, func(test *testing.T) {
			_, path := snapshotTestStore(test)
			pool, err := sql.Open("sqlite3", snapshotAddress(path, "rw"))
			if err != nil {
				test.Fatal(err)
			}
			defer pool.Close()
			if legacy {
				if _, err := pool.ExecContext(test.Context(), "DROP TABLE repository_sources"); err != nil {
					test.Fatal(err)
				}
			}
			rows, err := pool.QueryContext(test.Context(), "SELECT type,name,tbl_name,sql FROM main.sqlite_schema ORDER BY type COLLATE BINARY,name COLLATE BINARY")
			if err != nil {
				test.Fatal(err)
			}
			complete, application, normalized := sha256.New(), sha256.New(), sha256.New()
			complete.Write([]byte("CGB2SC01"))
			count, applications, catalogBytes, applicationBytes, definitionBytes := 0, 0, 0, 0, 0
			for rows.Next() {
				var fields [4]sql.NullString
				if err := rows.Scan(&fields[0], &fields[1], &fields[2], &fields[3]); err != nil {
					test.Fatal(err)
				}
				record := snapshotCatalogTestRecord(fields)
				complete.Write(record)
				catalogBytes += len(record)
				count++
				if fields[1].String == "sqlite_sequence" || fields[1].String == "sqlite_autoindex_category_skills_1" {
					continue
				}
				applications++
				definitionBytes += len(fields[3].String)
				for index, field := range fields {
					if !field.Valid {
						test.Fatal("application catalog has a NULL field")
					}
					var length [4]byte
					binary.BigEndian.PutUint32(length[:], uint32(len(field.String)))
					application.Write(length[:])
					application.Write([]byte(field.String))
					applicationBytes += len(length) + len(field.String)
					value := field.String
					if index == 3 {
						value = strings.Join(strings.Fields(value), " ")
					}
					binary.BigEndian.PutUint32(length[:], uint32(len(value)))
					normalized.Write(length[:])
					normalized.Write([]byte(value))
				}
			}
			if err := rows.Err(); err != nil {
				test.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				test.Fatal(err)
			}
			complete.Write([]byte{255})
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(count))
			complete.Write(length[:])
			catalogBytes += len("CGB2SC01") + 1 + len(length)
			actualApplication := hex.EncodeToString(application.Sum(nil))
			actualNormalized := hex.EncodeToString(normalized.Sum(nil))
			actualComplete := hex.EncodeToString(complete.Sum(nil))
			test.Logf("APPLICATION=%s NORMALIZED=%s COMPLETE=%s ENTRIES=%d APPLICATIONS=%d CATALOG_BYTES=%d APPLICATION_BYTES=%d DEFINITION_BYTES=%d", actualApplication, actualNormalized, actualComplete, count, applications, catalogBytes, applicationBytes, definitionBytes)
			expectedApplication := "90ed016f16f332f11000262d81c01687a8668c0f1c4c3bfab830d632f468e74d"
			expectedNormalized := "a823acaa8121faf623a22263331ca8d4629b40951bc0905d5b2c753dd251ffcb"
			expectedComplete := "503b5c98bd43d02ef6730caa4c0a61294e53f93b1d0cb7d45432f3ac9e299c62"
			expectedCount, expectedApplications := 95, 93
			if legacy {
				expectedApplication = "34738287b1235358fc73bbefed6fabdde0968bf8340dee784fc0a9b448bc9235"
				expectedNormalized = "7e56e095b6943f47835a6a019ad99655b601224afa02726f2270312a5a404cfc"
				expectedComplete = "89fb8b48f9321c2777b75e5fb552d34938ae7bb9985874292847ee99116e0129"
				expectedCount, expectedApplications = 92, 90
			}
			if actualApplication != expectedApplication || actualNormalized != expectedNormalized || actualComplete != expectedComplete || count != expectedCount || applications != expectedApplications {
				test.Fatal("independent schema version golden differs")
			}
			expectedCatalogBytes, expectedSchemaBytes := 28335, 47773
			if legacy {
				expectedCatalogBytes, expectedSchemaBytes = 27485, 45749
			}
			if catalogBytes != expectedCatalogBytes || uint64(catalogBytes) > snapshotSourcePlanningCatalogBytes {
				test.Fatal("schema catalog planning budget differs")
			}
			observed, err := snapshotSchema(test.Context(), pool)
			if err != nil || hex.EncodeToString(observed[:]) != actualApplication {
				test.Fatal("exact known schema refused", err)
			}
			resourcePool, err := sql.Open("sqlite3", snapshotAddress(path, "ro"))
			if err != nil {
				test.Fatal(err)
			}
			defer resourcePool.Close()
			resourcePool.SetMaxOpenConns(1)
			var owners int
			if err := resourcePool.QueryRowContext(test.Context(), "SELECT count(*) FROM users").Scan(&owners); err != nil {
				test.Fatal(err)
			}
			connection, err := resourcePool.Conn(test.Context())
			if err != nil {
				test.Fatal(err)
			}
			var schemaBytes int
			statusError := connection.Raw(func(raw any) error {
				provider, ok := raw.(interface {
					Status(sqlite.DBStatusOp, bool) (int, int, error)
				})
				if !ok {
					return fmt.Errorf("driver lacks schema resource status")
				}
				var err error
				schemaBytes, _, err = provider.Status(sqlite.DBStatusSchemaUsed, false)
				return err
			})
			closeError := connection.Close()
			if statusError != nil || closeError != nil {
				test.Fatal("schema resource status failed", statusError, closeError)
			}
			if schemaBytes != expectedSchemaBytes || uint64(schemaBytes) > snapshotSourcePlanningParsedSchemaBytes || uint64(catalogBytes+schemaBytes) > snapshotSourcePlanningSchemaBytes {
				test.Fatal("parsed schema planning budget differs")
			}
			test.Logf("SQLITE_DBSTATUS_SCHEMA_USED=%d PLANNING_SCHEMA_BYTES=%d", schemaBytes, snapshotSourcePlanningSchemaBytes)
		})
	}
}
