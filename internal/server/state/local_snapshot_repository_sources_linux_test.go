//go:build linux

package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
)

func snapshotSeedRepositorySource(test *testing.T, store *Store, owner int, publicID, name string, skills []repository.SkillSource) *ent.RepositorySource {
	test.Helper()
	entry, err := store.state.client.RepositorySource.Create().SetPublicID(publicID).SetUserID(owner).SetNameWithOwner(name).SetBranch("stable").SetSkills(skills).SetRegisteredAt(time.Unix(1700000000, 0).UTC()).Save(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	return entry
}

func TestLocalSnapshotSchemaVersionsRoundTrip(test *testing.T) {
	for _, legacy := range []bool{false, true} {
		test.Run(fmt.Sprintf("legacy-%t", legacy), func(test *testing.T) {
			store, sourcePath := snapshotTestStore(test)
			snapshotSeedRelations(test, store, 1)
			var expectedReferences bytes.Buffer
			baseline, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
				_, err := view.CopyReferences(ctx, &expectedReferences)
				return err
			})
			if err != nil {
				test.Fatal(err)
			}
			var entries []*ent.RepositorySource
			expectedSchema := "90ed016f16f332f11000262d81c01687a8668c0f1c4c3bfab830d632f468e74d"
			if legacy {
				pool, err := sql.Open("sqlite3", snapshotAddress(sourcePath, "rw"))
				if err != nil {
					test.Fatal(err)
				}
				_, mutationError := pool.ExecContext(test.Context(), "DROP TABLE repository_sources")
				if err := errors.Join(mutationError, pool.Close()); err != nil {
					test.Fatal(err)
				}
				expectedSchema = "34738287b1235358fc73bbefed6fabdde0968bf8340dee784fc0a9b448bc9235"
			} else {
				for _, owner := range []int{1, 2} {
					entries = append(entries, snapshotSeedRepositorySource(test, store, owner, fmt.Sprintf("source-%d", owner), "uncollected/source-only", []repository.SkillSource{{Name: "Unverified", Path: "skills/not-collected"}, {Name: "Root", Path: ""}}))
				}
			}
			directory := test.TempDir()
			if err := os.Chmod(directory, 0700); err != nil {
				test.Fatal(err)
			}
			outputPath := filepath.Join(directory, "server.db")
			output, err := os.OpenFile(outputPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				test.Fatal(err)
			}
			var references bytes.Buffer
			summary, snapshotError := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
				if _, err := view.CopyReferences(ctx, &references); err != nil {
					return err
				}
				_, err := view.CopyDatabase(ctx, output)
				return err
			})
			if err := errors.Join(snapshotError, output.Sync(), output.Close()); err != nil {
				test.Fatal(err)
			}
			if !bytes.Equal(expectedReferences.Bytes(), references.Bytes()) {
				test.Fatal("unverified sources entered archive reference closure")
			}
			if size, hash, records := summary.Metadata().References(); size != int64(references.Len()) || records != 4 || hash != sha256.Sum256(expectedReferences.Bytes()) {
				test.Fatal("source-only metadata changed closure counts or digest")
			}
			expectedSize, _, _ := baseline.Metadata().References()
			if expectedSize != int64(references.Len()) {
				test.Fatal("reference inventory changed")
			}
			parent, err := os.Open(directory)
			if err != nil {
				test.Fatal(err)
			}
			defer parent.Close()
			database, err := os.Open(outputPath)
			if err != nil {
				test.Fatal(err)
			}
			defer database.Close()
			fixture := restoreFixtureData{parent: parent, database: database, key: bytes.Repeat([]byte{91}, 32), metadata: summary.Metadata()}
			before := restoreFingerprint(test, fixture)
			limits := snapshotTestLimits()
			limits.MaxRecords = 6 + uint64(len(entries))*3
			evidence, err := InspectRestoreDatabase(test.Context(), parent, database, fixture.key, limits)
			if err != nil || !evidence.Valid() {
				test.Fatal("exact schema snapshot refused by real restore worker", err)
			}
			observed := evidence.SchemaFingerprint()
			if hex.EncodeToString(observed[:]) != expectedSchema || observed != summary.Metadata().SchemaFingerprint() || restoreFingerprint(test, fixture) != before {
				test.Fatal("restore changed candidate or schema identity")
			}
			limits.MaxRecords--
			if refused, err := InspectRestoreDatabase(test.Context(), parent, database, fixture.key, limits); !errors.Is(err, ErrRestoreBudget) || refused.Valid() {
				test.Fatal("restore did not account for metadata validation records", err)
			}
			pool, err := sql.Open("sqlite3", snapshotAddress(outputPath, "ro")+"&immutable=1")
			if err != nil {
				test.Fatal(err)
			}
			defer pool.Close()
			var tables int
			if err := pool.QueryRowContext(test.Context(), "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='repository_sources'").Scan(&tables); err != nil || legacy && tables != 0 || !legacy && tables != 1 {
				test.Fatal("backup did not preserve observed source schema", err)
			}
			for _, entry := range entries {
				var owner int
				var name, branch, encoded string
				var registered time.Time
				err := pool.QueryRowContext(test.Context(), "SELECT user_id,name_with_owner,branch,skills,registered_at FROM repository_sources WHERE public_id=?", entry.PublicID).Scan(&owner, &name, &branch, &encoded, &registered)
				var skills []repository.SkillSource
				if err != nil || json.Unmarshal([]byte(encoded), &skills) != nil || owner != entry.UserID || name != entry.NameWithOwner || branch != entry.Branch || !registered.Equal(entry.RegisteredAt) || !slices.Equal(skills, entry.Skills) {
					test.Fatal("real source metadata was lost or changed", err)
				}
			}
			test.Logf("SCHEMA=%s SOURCE_ROWS=%d CLOSURE_RECORDS=4 UNCHANGED=true", expectedSchema, len(entries))
		})
	}
}

func TestLocalSnapshotRepositorySourceQuotas(test *testing.T) {
	for _, kind := range []string{"repositories", "skills"} {
		test.Run(kind, func(test *testing.T) {
			store, path := snapshotTestStore(test)
			snapshotSeedRelations(test, store, 1)
			maximum, encoded := maxRepositorySources, "[]"
			var entries []repository.SkillSource
			if kind == "skills" {
				maximum = 2
				for index := range maxSkillSources / 2 {
					entries = append(entries, repository.SkillSource{Name: fmt.Sprintf("Skill%d", index), Path: fmt.Sprintf("skills/skill%d", index)})
				}
				data, err := json.Marshal(entries)
				if err != nil {
					test.Fatal(err)
				}
				encoded = string(data)
			}
			insert := fmt.Sprintf("WITH RECURSIVE seq(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM seq WHERE value<%d) INSERT INTO repository_sources(public_id,user_id,name_with_owner,branch,skills,registered_at) SELECT 'source-'||u.id||'-'||seq.value,u.id,'org/source'||seq.value,'stable','%s','2023-11-14 22:13:20+00:00' FROM users u,seq", maximum, encoded)
			pool, err := sql.Open("sqlite3", snapshotAddress(path, "rw"))
			if err != nil {
				test.Fatal(err)
			}
			defer pool.Close()
			if _, err := pool.ExecContext(test.Context(), insert); err != nil {
				test.Fatal(err)
			}
			if summary, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil }); err != nil || summary.get == nil {
				test.Fatal("independent owner quotas rejected at exact limit", err)
			}
			fixture := restoreFixture(test, 1, 0)
			restoreMutation(test, fixture, insert)
			if evidence, err := InspectRestoreDatabase(test.Context(), fixture.parent, fixture.database, fixture.key, snapshotTestLimits()); err != nil || !evidence.Valid() {
				test.Fatal("restore rejected independent owner quotas", err)
			}
			excess := "INSERT INTO repository_sources(public_id,user_id,name_with_owner,branch,skills,registered_at) VALUES('excess',1,'org/excess','stable','[]','2023-11-14 22:13:20+00:00')"
			if kind == "skills" {
				entries = append(entries, repository.SkillSource{Name: "Extra", Path: "skills/extra"})
				data, err := json.Marshal(entries)
				if err != nil {
					test.Fatal(err)
				}
				excess = "UPDATE repository_sources SET skills='" + string(data) + "' WHERE public_id='source-1-1'"
			}
			if _, err := pool.ExecContext(test.Context(), excess); err != nil {
				test.Fatal(err)
			}
			called := false
			if summary, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { called = true; return nil }); !errors.Is(err, ErrLocalSnapshotBudget) || called || summary.get != nil {
				test.Fatal("source quota overflow reached snapshot callback or summary", err)
			}
			restoreMutation(test, fixture, excess)
			restoreRefused(test, fixture, ErrRestoreBudget)
		})
	}
}

func TestLocalSnapshotRepositorySourceEscapedBoundary(test *testing.T) {
	store, path := snapshotTestStore(test)
	snapshotSeedRepositorySource(test, store, 1, "escaped-source", "org/source", []repository.SkillSource{})
	objects := make([]string, 0, maxSkillSources)
	for index := range maxSkillSources {
		logicalPath := fmt.Sprintf("%01024d", index)
		var escapedPath strings.Builder
		for _, character := range logicalPath {
			fmt.Fprintf(&escapedPath, `\u%04x`, character)
		}
		objects = append(objects, `{"name":"`+strings.Repeat(`\u0061`, 256)+`","path":"`+escapedPath.String()+`"}`)
	}
	encoded := "[" + strings.Join(objects, ",") + "]"
	if len(encoded) != snapshotSourceSkillsBytes-1 {
		test.Fatal("escaped source JSON resource bound differs")
	}
	pool, err := sql.Open("sqlite3", snapshotAddress(path, "rw"))
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.ExecContext(test.Context(), "UPDATE repository_sources SET skills=?", encoded); err != nil {
		test.Fatal(err)
	}
	limits := snapshotTestLimits()
	limits.MaxRecords = 1 + maxSkillSources
	if summary, err := store.WithLocalSnapshot(test.Context(), limits, func(context.Context, LocalSnapshotView) error { return nil }); err != nil || summary.get == nil {
		test.Fatal("maximum escaped source metadata refused within resource limits", err)
	}
	fixture := restoreFixture(test, 1, 0)
	restoreMutation(test, fixture, "INSERT INTO repository_sources(public_id,user_id,name_with_owner,branch,skills,registered_at) VALUES('escaped-source',1,'org/source','stable','"+encoded+"','2023-11-14 22:13:20+00:00')")
	if evidence, err := InspectRestoreDatabase(test.Context(), fixture.parent, fixture.database, fixture.key, snapshotTestLimits()); err != nil || !evidence.Valid() {
		test.Fatal("restore refused maximum escaped source metadata", err)
	}
	test.Logf("SOURCE_JSON_BYTES=%d SOURCE_SKILLS=%d SQLITE_HEAP_BYTES=%d", len(encoded), maxSkillSources, limits.SQLiteHeapBytes)
}

func TestLocalSnapshotRepositorySourceRefusal(test *testing.T) {
	for name, mutation := range map[string]string{
		"orphan-owner":        "user_id=999999",
		"zero-owner":          "user_id=0",
		"negative-owner":      "user_id=-1",
		"zero-id":             "id=0",
		"negative-id":         "id=-1",
		"noncanonical-name":   "name_with_owner='Org/Source'",
		"noncanonical-branch": "branch='HEAD'",
		"overlong-public-id":  "public_id=printf('%065d',0)",
		"overlong-branch":     "branch=printf('%0257d',0)",
		"overlong-skill-name": `skills='[{"name":"'||printf('%0257d',0)||'","path":""}]'`,
		"overlong-skill-path": `skills='[{"name":"Example","path":"'||printf('%01025d',0)||'"}]'`,
		"giant-name":          "name_with_owner=zeroblob(2097152)",
		"giant-time":          "registered_at=zeroblob(2097152)",
		"giant-json":          fmt.Sprintf("skills=zeroblob(%d)", snapshotSourceSkillsBytes+1),
		"malformed-json":      `skills='[{'`,
		"not-array":           `skills='{}'`,
		"null-array":          `skills='null'`,
		"null-element":        `skills='[null]'`,
		"missing-name":        `skills='[{"path":"skills/example"}]'`,
		"missing-path":        `skills='[{"name":"Example"}]'`,
		"null-name":           `skills='[{"name":null,"path":""}]'`,
		"null-path":           `skills='[{"name":"Example","path":null}]'`,
		"unknown-member":      `skills='[{"name":"Example","path":"","hash":"unverified"}]'`,
		"duplicate-member":    `skills='[{"name":"Example","path":"","path":"skills/example"}]'`,
		"wrong-type":          `skills='[{"name":1,"path":""}]'`,
		"blank-name":          `skills='[{"name":" ","path":""}]'`,
		"padded-name":         `skills='[{"name":" Example","path":""}]'`,
		"absolute-path":       `skills='[{"name":"Example","path":"/etc/passwd"}]'`,
		"traversal-path":      `skills='[{"name":"Example","path":"../private"}]'`,
		"unclean-path":        `skills='[{"name":"Example","path":"skills//example"}]'`,
		"windows-path":        `skills='[{"name":"Example","path":"C:/private"}]'`,
		"backslash-path":      `skills='[{"name":"Example","path":"skills\\example"}]'`,
		"control-path":        `skills='[{"name":"Example","path":"skills/\u0000example"}]'`,
		"duplicate-reference": `skills='[{"name":"Example","path":""},{"name":"Example","path":""}]'`,
		"trailing-json":       `skills='[]{}'`,
	} {
		test.Run(name, func(test *testing.T) {
			store, path := snapshotTestStore(test)
			snapshotSeedRepositorySource(test, store, 1, "source-1", "org/source", []repository.SkillSource{{Name: "Example", Path: "skills/example"}})
			pool, err := sql.Open("sqlite3", snapshotAddress(path, "rw", "foreign_keys(0)"))
			if err != nil {
				test.Fatal(err)
			}
			_, mutationError := pool.ExecContext(test.Context(), "UPDATE repository_sources SET "+mutation)
			if err := errors.Join(mutationError, pool.Close()); err != nil {
				test.Fatal(err)
			}
			called := false
			summary, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { called = true; return nil })
			if !errors.Is(err, ErrLocalSnapshotInvalid) || called || summary.get != nil {
				test.Fatal("unsafe source reached snapshot callback or summary", err)
			}
			fixture := restoreFixture(test, 1, 0)
			restoreMutation(test, fixture, `INSERT INTO repository_sources(public_id,user_id,name_with_owner,branch,skills,registered_at) VALUES('source-1',1,'org/source','stable','[{"name":"Example","path":"skills/example"}]','2023-11-14 22:13:20+00:00')`, "UPDATE repository_sources SET "+mutation)
			restoreRefused(test, fixture, ErrRestoreInvalid)
		})
	}
}
