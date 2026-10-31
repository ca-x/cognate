//go:build linux

package state

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/ent"
)

type restoreFixtureData struct {
	parent, database *os.File
	key              []byte
	metadata         LocalSnapshotMetadata
}

func restoreFixture(test *testing.T, assets, relations int) restoreFixtureData {
	test.Helper()
	store, _ := snapshotTestStore(test)
	if relations > 0 {
		snapshotSeedRelations(test, store, relations)
	} else if err := store.CreateUser(test.Context(), "snapshot-second-owner"); err != nil {
		test.Fatal(err)
	}
	owners := []string{"snapshot-private-owner", "snapshot-second-owner"}
	for _, owner := range owners {
		token, err := github.NewToken("restore-private-token-sentinel", []string{"repo"})
		if err != nil {
			test.Fatal(err)
		}
		if _, err = store.PutCredential(test.Context(), owner, "restore-viewer", "", token); err != nil {
			test.Fatal(err)
		}
	}
	for index := range assets {
		_, err := store.CreateConfigurationAsset(test.Context(), owners[index%2], ConfigurationAssetDraft{AgentID: "codex", Kind: ConfigurationKindProvider, Name: fmt.Sprint("restore-asset-", index), SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("restore-private-config-sentinel"))})
		if err != nil {
			test.Fatal(err)
		}
	}
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "server.db")
	output, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		test.Fatal(err)
	}
	summary, snapshotError := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
		_, err := view.CopyDatabase(ctx, output)
		return err
	})
	if err := errors.Join(snapshotError, output.Sync(), output.Close(), store.Close()); err != nil {
		test.Fatal(err)
	}
	parent, err := os.Open(directory)
	if err != nil {
		test.Fatal(err)
	}
	database, err := os.Open(path)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = database.Close(); _ = parent.Close() })
	return restoreFixtureData{parent: parent, database: database, key: bytes.Repeat([]byte{91}, 32), metadata: summary.Metadata()}
}

func restoreMutation(test *testing.T, fixture restoreFixtureData, statements ...string) {
	test.Helper()
	address := url.URL{Scheme: "file", Path: filepath.Join(fixture.parent.Name(), "server.db"), RawQuery: "mode=rw&_pragma=journal_mode(DELETE)&_pragma=foreign_keys(0)"}
	pool, err := sql.Open("sqlite3", address.String())
	if err != nil {
		test.Fatal(err)
	}
	for _, statement := range statements {
		if _, err := pool.ExecContext(test.Context(), statement); err != nil {
			_ = pool.Close()
			test.Fatal(err)
		}
	}
	if err := pool.Close(); err != nil {
		test.Fatal(err)
	}
}

func restoreFingerprint(test *testing.T, fixture restoreFixtureData) [32]byte {
	test.Helper()
	data, err := os.ReadFile(filepath.Join(fixture.parent.Name(), "server.db"))
	if err != nil {
		test.Fatal(err)
	}
	return sha256.Sum256(data)
}

func restoreRefused(test *testing.T, fixture restoreFixtureData, expected error) {
	test.Helper()
	before := restoreFingerprint(test, fixture)
	evidence, err := InspectRestoreDatabase(test.Context(), fixture.parent, fixture.database, fixture.key, snapshotTestLimits())
	if err == nil || evidence.Valid() || evidence != (RestoreDatabaseEvidence{}) || expected != nil && !errors.Is(err, expected) {
		test.Fatalf("candidate admitted or wrong refusal: %v", err)
	}
	if after := restoreFingerprint(test, fixture); after != before {
		test.Fatal("inspection mutated candidate")
	}
	if _, err := fixture.parent.Stat(); err != nil {
		test.Fatal("borrowed parent closed")
	}
	if _, err := fixture.database.ReadAt(make([]byte, 16), 0); err != nil {
		test.Fatal("borrowed database closed")
	}
}

func restoreDomainDecision(test *testing.T, fixture restoreFixtureData, valid bool) {
	test.Helper()
	before := restoreFingerprint(test, fixture)
	evidence, err := InspectRestoreDatabase(test.Context(), fixture.parent, fixture.database, fixture.key, snapshotTestLimits())
	if restoreFingerprint(test, fixture) != before {
		test.Fatal("domain inspection mutated candidate")
	}
	if _, statError := fixture.parent.Stat(); statError != nil {
		test.Fatal("borrowed parent closed")
	}
	if _, readError := fixture.database.ReadAt(make([]byte, 16), 0); readError != nil {
		test.Fatal("borrowed database closed")
	}
	if valid {
		if err != nil || !evidence.Valid() {
			test.Fatalf("positive domain control refused: %v", err)
		}
		test.Log("positive domain control admitted; database unchanged; borrowed FDs retained")
	} else {
		if !errors.Is(err, ErrRestoreInvalid) || evidence != (RestoreDatabaseEvidence{}) {
			test.Fatalf("unsafe domain admitted or wrong refusal: error=%v Valid=%v; database unchanged; borrowed FDs retained", err, evidence.Valid())
		}
		test.Log("unsafe domain refused with zero evidence; database unchanged; borrowed FDs retained")
	}
}

func restoreDomainFixtureCheck(test *testing.T, fixture restoreFixtureData, retainedRow string) {
	test.Helper()
	pool, err := sql.Open("sqlite3", snapshotAddress(fixture.database.Name(), "ro")+"&immutable=1")
	if err != nil {
		test.Fatal(err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			test.Fatal(err)
		}
	}()
	schema, err := snapshotSchema(test.Context(), pool)
	if err != nil || schema != fixture.metadata.SchemaFingerprint() {
		test.Fatal("domain fixture changed schema", err)
	}
	var integrity string
	if err := pool.QueryRowContext(test.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		test.Fatal("domain fixture integrity", integrity, err)
	}
	rows, err := pool.QueryContext(test.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		test.Fatal(err)
	}
	invalid := rows.Next()
	if err := errors.Join(rows.Err(), rows.Close()); err != nil || invalid {
		test.Fatal("domain fixture has invalid foreign keys", err)
	}
	var present int
	if err := pool.QueryRowContext(test.Context(), "SELECT count(*) FROM "+retainedRow).Scan(&present); err != nil || present != 1 {
		test.Fatal("retained domain row absent", present, err)
	}
	test.Log("fixture schema unchanged; integrity=ok; FK=clean; retained row: " + retainedRow)
}

func TestRestoreDatabaseRejectHiddenCiphertextDomains(test *testing.T) {
	for _, table := range []string{"git_hub_credentials", "configuration_assets"} {
		for _, identity := range []int{0, -1} {
			test.Run(fmt.Sprintf("%s/id=%d", table, identity), func(test *testing.T) {
				fixture := restoreFixture(test, 2, 0)
				restoreDomainDecision(test, fixture, true)
				restoreMutation(test, fixture, "UPDATE "+table+" SET ciphertext=x'00' WHERE id=(SELECT max(id) FROM "+table+")")
				restoreDomainDecision(test, fixture, false)
				if table == "configuration_assets" {
					restoreMutation(test, fixture, fmt.Sprintf("UPDATE configuration_order_entries SET asset_id=%d WHERE asset_id=(SELECT max(id) FROM configuration_assets)", identity))
				}
				restoreMutation(test, fixture, fmt.Sprintf("UPDATE %s SET id=%d WHERE id=(SELECT max(id) FROM %s)", table, identity, table))
				restoreDomainFixtureCheck(test, fixture, fmt.Sprintf("%s WHERE id=%d AND ciphertext=x'00'", table, identity))
				restoreDomainDecision(test, fixture, false)
			})
		}
	}
}

func TestRestoreDatabaseRejectHiddenReferenceDomains(test *testing.T) {
	for _, identity := range []int{0, -1} {
		test.Run(fmt.Sprintf("empty-pack/id=%d", identity), func(test *testing.T) {
			fixture := restoreFixture(test, 2, 2)
			restoreMutation(test, fixture, "DELETE FROM pack_items WHERE pack_id=1", "UPDATE packs SET item_count=0 WHERE id=1")
			restoreDomainDecision(test, fixture, true)
			restoreMutation(test, fixture, "UPDATE packs SET revision=0 WHERE id=1")
			restoreDomainDecision(test, fixture, false)
			restoreMutation(test, fixture, fmt.Sprintf("UPDATE packs SET id=%d WHERE id=1", identity))
			restoreDomainFixtureCheck(test, fixture, fmt.Sprintf("packs WHERE id=%d AND revision=0 AND item_count=0", identity))
			restoreDomainDecision(test, fixture, false)
		})
		test.Run(fmt.Sprintf("category-and-link/id=%d", identity), func(test *testing.T) {
			fixture := restoreFixture(test, 2, 2)
			restoreDomainDecision(test, fixture, true)
			restoreMutation(test, fixture, "UPDATE categories SET name='',normalized_name='',revision=0 WHERE id=1")
			restoreDomainDecision(test, fixture, false)
			restoreMutation(test, fixture, fmt.Sprintf("UPDATE category_skills SET category_id=%d WHERE category_id=1", identity), fmt.Sprintf("UPDATE categories SET id=%d WHERE id=1", identity))
			restoreDomainFixtureCheck(test, fixture, fmt.Sprintf("categories WHERE id=%d AND name='' AND normalized_name='' AND revision=0", identity))
			restoreDomainFixtureCheck(test, fixture, fmt.Sprintf("category_skills WHERE category_id=%d AND collected_skill_id=1", identity))
			restoreDomainDecision(test, fixture, false)
		})
	}
}

func TestRestoreDatabasePositiveIDDomains(test *testing.T) {
	for _, domain := range []struct {
		table string
		moves []string
	}{
		{"users", []string{"INSERT INTO users(id,user_key) VALUES (%d,'restore-unreferenced-owner')"}},
		{"git_hub_credentials", nil},
		{"configuration_assets", []string{"UPDATE configuration_order_entries SET asset_id=%d WHERE asset_id=1"}},
		{"collected_repositories", []string{"UPDATE collected_skills SET repository_id=%d WHERE repository_id=1"}},
		{"collected_skills", []string{"UPDATE pack_items SET skill_id=%d WHERE skill_id=1", "UPDATE category_skills SET collected_skill_id=%d WHERE collected_skill_id=1"}},
		{"packs", []string{"UPDATE pack_items SET pack_id=%d WHERE pack_id=1"}},
		{"pack_items", nil},
		{"categories", []string{"UPDATE category_skills SET category_id=%d WHERE category_id=1"}},
	} {
		for _, identity := range []int64{0, -1} {
			test.Run(fmt.Sprintf("%s/id=%d", domain.table, identity), func(test *testing.T) {
				fixture := restoreFixture(test, 2, 2)
				restoreDomainDecision(test, fixture, true)
				var statements []string
				for _, move := range domain.moves {
					statements = append(statements, fmt.Sprintf(move, identity))
				}
				if domain.table != "users" {
					statements = append(statements, fmt.Sprintf("UPDATE %s SET id=%d WHERE id=1", domain.table, identity))
				}
				restoreMutation(test, fixture, statements...)
				restoreDomainFixtureCheck(test, fixture, fmt.Sprintf("%s WHERE id=%d", domain.table, identity))
				restoreDomainDecision(test, fixture, false)
			})
		}
	}
}

func TestRestoreDatabaseIntegerDomainBoundaries(test *testing.T) {
	for _, identity := range []int64{-1 << 63, 1<<63 - 1} {
		test.Run(fmt.Sprint(identity), func(test *testing.T) {
			fixture := restoreFixture(test, 2, 0)
			restoreMutation(test, fixture, fmt.Sprintf("UPDATE git_hub_credentials SET id=%d WHERE id=1", identity))
			restoreDomainFixtureCheck(test, fixture, fmt.Sprintf("git_hub_credentials WHERE id=%d", identity))
			restoreDomainDecision(test, fixture, identity > 0)
		})
	}
}

func TestRestoreDatabaseValidReadOnly(test *testing.T) {
	for _, relations := range []int{0, 2, 130} {
		test.Run(fmt.Sprint(relations), func(test *testing.T) {
			fixture := restoreFixture(test, 2, relations)
			before := restoreFingerprint(test, fixture)
			key := bytes.Clone(fixture.key)
			evidence, err := InspectRestoreDatabase(test.Context(), fixture.parent, fixture.database, fixture.key, snapshotTestLimits())
			if err != nil || !evidence.Valid() {
				test.Fatalf("valid candidate refused: %v", err)
			}
			expectedBytes, expectedHash := fixture.metadata.Database()
			if size, digest := evidence.Database(); size != expectedBytes || digest != expectedHash {
				test.Fatal("database identity differs")
			}
			if size, digest, records := evidence.References(); records != uint64(relations*4) || size < 0 || digest == ([32]byte{}) {
				test.Fatal("canonical reference evidence differs")
			}
			evidenceBytes, evidenceHash, evidenceRecords := evidence.References()
			if size, digest, records := fixture.metadata.References(); size != evidenceBytes || digest != evidenceHash || records != evidenceRecords {
				test.Fatal("canonical snapshot reference identity differs")
			}
			if evidence.SchemaFingerprint() != fixture.metadata.SchemaFingerprint() || evidence.KeyIdentity() != fixture.metadata.KeyIdentity() || evidence.ObservationMaxAge() != time.Hour {
				test.Fatal("schema, key or policy differs")
			}
			if repositories, skills, packs, items := evidence.ReferenceCounts(); repositories != uint64(relations) || skills != repositories || packs != repositories || items != repositories {
				test.Fatal("reference kinds omitted")
			}
			if restoreFingerprint(test, fixture) != before || !bytes.Equal(key, fixture.key) {
				test.Fatal("borrowed data changed")
			}
			entries, err := os.ReadDir(fixture.parent.Name())
			if err != nil || len(entries) != 1 || entries[0].Name() != "server.db" {
				test.Fatal("inspection created files")
			}
		})
	}
}

func TestRestoreDatabaseControlMessages(test *testing.T) {
	request := restoreDatabaseRequest{Nonce: [16]byte{1}, Parent: restoreDatabaseIdentity{Device: 1, Inode: 1}, Database: restoreDatabaseIdentity{Device: 1, Inode: 2}, Limits: snapshotTestLimits()}
	var encoded bytes.Buffer
	if err := restoreWriteMessage(&encoded, request); err != nil {
		test.Fatal("request encoding failed", err)
	}
	var decoded restoreDatabaseRequest
	if err := restoreReadMessage(&encoded, 4096, &decoded); err != nil || decoded != request {
		test.Fatal("request round trip failed", err)
	}
}

func TestRestoreDatabaseControlRejections(test *testing.T) {
	request := restoreDatabaseRequest{Nonce: [16]byte{1}, Parent: restoreDatabaseIdentity{Device: 1, Inode: 1}, Database: restoreDatabaseIdentity{Device: 1, Inode: 2}, Limits: snapshotTestLimits()}
	encoded, err := restoreMarshalMessage(request)
	if err != nil {
		test.Fatal(err)
	}
	for name, payload := range map[string][]byte{
		"unknown":   append([]byte(`{"Unknown":1,`), encoded[1:]...),
		"duplicate": append([]byte(`{"Nonce":[1],`), encoded[1:]...),
		"trailing":  append(bytes.Clone(encoded), '\n', 'x'),
		"oversize":  bytes.Repeat([]byte(" "), 4097),
		"short":     encoded[:len(encoded)-1],
	} {
		test.Run(name, func(test *testing.T) {
			var decoded restoreDatabaseRequest
			if restoreReadMessage(bytes.NewReader(payload), 4096, &decoded) == nil {
				test.Fatal("invalid control message admitted")
			}
		})
	}
}

func TestRestoreDatabaseRunningCancellationAndCloseFailure(test *testing.T) {
	for _, mode := range []string{"cancel", "short-response", "final-close", "cancel-and-close"} {
		test.Run(mode, func(test *testing.T) {
			fixture := restoreFixture(test, 2, 2)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			closes := make(map[*os.File]int)
			workerOK := false
			operations := restoreDatabaseIO{
				closeFile: func(file *os.File) error {
					closes[file]++
					err := file.Close()
					if strings.Contains(mode, "close") && file.Name() == fixture.database.Name() {
						return syscall.EIO
					}
					return err
				},
				readMessage: func(reader io.Reader, maximum int64, output any) error {
					if strings.Contains(mode, "cancel") {
						cancel()
					}
					if mode == "short-response" {
						_, _ = io.CopyN(io.Discard, reader, 5)
						return ErrRestoreInvalid
					}
					err := restoreReadMessage(reader, maximum, output)
					workerOK = err == nil && output.(*restoreDatabaseResponse).Status == "ok"
					return err
				},
			}
			evidence, err := inspectRestoreDatabaseWithIO(ctx, fixture.parent, fixture.database, fixture.key, snapshotTestLimits(), operations)
			expected := ErrRestoreInvalid
			if mode == "cancel" {
				expected = context.Canceled
			}
			if strings.Contains(mode, "close") {
				expected = ErrRestoreUncertain
			}
			if !errors.Is(err, expected) || evidence != (RestoreDatabaseEvidence{}) {
				test.Fatal("late failure returned evidence or lost error priority", err)
			}
			if mode == "final-close" && !workerOK {
				test.Fatal("final close test did not reach a verified worker result")
			}
			if len(closes) < 9 {
				test.Fatal("owned worker descriptors did not drain")
			}
			for _, count := range closes {
				if count != 1 {
					test.Fatal("descriptor close retried")
				}
			}
			if _, err := fixture.parent.Stat(); err != nil {
				test.Fatal("borrowed parent closed")
			}
			if _, err := fixture.database.ReadAt(make([]byte, 16), 0); err != nil {
				test.Fatal("borrowed database closed")
			}
		})
	}
}

func TestRestoreDatabaseNoMigration(test *testing.T) {
	for name, statement := range map[string]string{
		"unknown-table":        "CREATE TABLE unapproved_restore(value TEXT)",
		"sqlite-prefix":        "CREATE TABLE sqlitefaux_restore(value TEXT)",
		"unknown-trigger":      "CREATE TRIGGER unapproved_restore AFTER INSERT ON users BEGIN SELECT 1; END",
		"new-column":           "ALTER TABLE users ADD COLUMN unapproved_restore TEXT",
		"missing-column":       "ALTER TABLE configuration_assets DROP COLUMN updated_at",
		"missing-key-proof":    "DELETE FROM server_metadata WHERE name='credential-key-v1'",
		"missing-policy-proof": "DELETE FROM server_metadata WHERE name='catalog-observation-policy-v1'",
		"bad-key-proof":        "UPDATE server_metadata SET key_proof=x'00' WHERE name='credential-key-v1'",
	} {
		test.Run(name, func(test *testing.T) {
			fixture := restoreFixture(test, 2, 2)
			restoreMutation(test, fixture, statement)
			restoreRefused(test, fixture, nil)
			entries, err := os.ReadDir(fixture.parent.Name())
			if err != nil || len(entries) != 1 {
				test.Fatal("refusal migrated or created sidecars")
			}
		})
	}
}

func TestRestoreDatabaseOriginalKey(test *testing.T) {
	fixture := restoreFixture(test, 2, 2)
	for _, key := range [][]byte{nil, bytes.Repeat([]byte{91}, 31), bytes.Repeat([]byte{91}, 33), bytes.Repeat([]byte{13}, 32)} {
		fixture.key = key
		restoreRefused(test, fixture, ErrRestoreKey)
	}
}

func TestRestoreDatabaseAllRetainedCiphertexts(test *testing.T) {
	for name, statement := range map[string]string{
		"last-github":           "UPDATE git_hub_credentials SET ciphertext=x'0100' WHERE id=(SELECT max(id) FROM git_hub_credentials)",
		"last-config":           "UPDATE configuration_assets SET ciphertext=x'00' WHERE id=(SELECT max(id) FROM configuration_assets)",
		"config-revision-aad":   "UPDATE configuration_assets SET revision=revision+1 WHERE id=(SELECT max(id) FROM configuration_assets)",
		"github-generation-aad": "UPDATE git_hub_credentials SET generation='changed-generation' WHERE id=(SELECT max(id) FROM git_hub_credentials)",
	} {
		test.Run(name, func(test *testing.T) {
			fixture := restoreFixture(test, 129, 2)
			if _, err := InspectRestoreDatabase(test.Context(), fixture.parent, fixture.database, fixture.key, snapshotTestLimits()); err != nil {
				test.Fatal("pre-tamper positive control", err)
			}
			restoreMutation(test, fixture, statement)
			restoreRefused(test, fixture, nil)
		})
	}
}

func TestRestoreDatabaseReferences(test *testing.T) {
	for name, statement := range map[string]string{
		"orphan":           "UPDATE pack_items SET skill_id=999999 WHERE id=1",
		"cross-owner-pack": "UPDATE pack_items SET skill_id=2 WHERE id=1",
		"count":            "UPDATE collected_repositories SET skill_count=2 WHERE id=1",
		"path":             "UPDATE collected_skills SET path='../escape' WHERE id=1",
		"category-owner":   "UPDATE categories SET user_id=2 WHERE id=1",
	} {
		test.Run(name, func(test *testing.T) {
			fixture := restoreFixture(test, 2, 2)
			restoreMutation(test, fixture, statement)
			restoreRefused(test, fixture, ErrRestoreInvalid)
		})
	}
}

func TestRestoreDatabasePolicy(test *testing.T) {
	for _, policy := range []string{fmt.Sprint(int64(2 * time.Hour)), "0", "01", "-1", fmt.Sprint(int64(25 * time.Hour))} {
		test.Run(policy, func(test *testing.T) {
			fixture := restoreFixture(test, 2, 0)
			block, err := aes.NewCipher(fixture.key)
			if err != nil {
				test.Fatal(err)
			}
			aead, err := cipher.NewGCMWithRandomNonce(block)
			if err != nil {
				test.Fatal(err)
			}
			proof := aead.Seal(nil, nil, []byte(policy), []byte(observationMetadataName+":"+fixture.metadata.KeyIdentity()))
			restoreMutation(test, fixture, "UPDATE server_metadata SET key_proof=x'"+hex.EncodeToString(proof)+"' WHERE name='catalog-observation-policy-v1'")
			if policy == fmt.Sprint(int64(2*time.Hour)) {
				evidence, err := InspectRestoreDatabase(test.Context(), fixture.parent, fixture.database, fixture.key, snapshotTestLimits())
				if err != nil || !evidence.Valid() || evidence.ObservationMaxAge() != 2*time.Hour {
					test.Fatal("nondefault policy lost", err)
				}
			} else {
				restoreRefused(test, fixture, nil)
			}
		})
	}
}

func TestRestoreDatabaseEmptyRetainedConfiguration(test *testing.T) {
	fixture := restoreFixture(test, 2, 0)
	pool, err := sql.Open("sqlite3", snapshotAddress(filepath.Join(fixture.parent.Name(), "server.db"), "ro")+"&immutable=1")
	if err != nil {
		test.Fatal(err)
	}
	var owner string
	var entry ent.ConfigurationAsset
	err = pool.QueryRowContext(test.Context(), "SELECT c.id,u.user_key,c.user_id,c.public_id,c.agent_id,c.kind,c.name,c.schema_version,c.revision FROM configuration_assets c JOIN users u ON u.id=c.user_id ORDER BY c.id DESC LIMIT 1").Scan(&entry.ID, &owner, &entry.UserID, &entry.PublicID, &entry.AgentID, &entry.Kind, &entry.Name, &entry.SchemaVersion, &entry.Revision)
	if err := errors.Join(err, pool.Close()); err != nil {
		test.Fatal(err)
	}
	aead, err := newConfigurationAEAD(fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	ciphertext := aead.Seal(nil, nil, nil, configurationAAD(owner, &entry))
	restoreMutation(test, fixture, "UPDATE configuration_assets SET ciphertext=x'"+hex.EncodeToString(ciphertext)+"' WHERE id="+fmt.Sprint(entry.ID))
	restoreRefused(test, fixture, ErrRestoreInvalid)
}

func TestRestoreDatabaseRejectsLiveWAL(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{91}, 32)
	path := filepath.Join(directory, "server.db")
	store, err := Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateUser(test.Context(), "restore-live-rejection-owner"); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		test.Fatal("fixture must be a live WAL database")
	}
	parent, err := os.Open(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer parent.Close()
	database, err := os.Open(path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	evidence, err := InspectRestoreDatabase(test.Context(), parent, database, key, snapshotTestLimits())
	if !errors.Is(err, ErrRestoreInvalid) || evidence.Valid() {
		test.Fatal("live WAL database admitted", err)
	}
	if err := store.CreateUser(test.Context(), "live-store-still-owns-state"); err != nil {
		test.Fatal("inspector disrupted the live owner", err)
	}
}

func TestRestoreDatabasePrivateIdentity(test *testing.T) {
	for _, mode := range []string{"public-file", "public-parent", "hardlink", "symlink", "replacement", "writable-fd", "wal", "shm", "journal"} {
		test.Run(mode, func(test *testing.T) {
			fixture := restoreFixture(test, 2, 0)
			path := filepath.Join(fixture.parent.Name(), "server.db")
			switch mode {
			case "public-file":
				if err := os.Chmod(path, 0644); err != nil {
					test.Fatal(err)
				}
			case "public-parent":
				if err := os.Chmod(fixture.parent.Name(), 0755); err != nil {
					test.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".alias"); err != nil {
					test.Fatal(err)
				}
			case "symlink", "replacement":
				if err := os.Rename(path, path+".original"); err != nil {
					test.Fatal(err)
				}
				if mode == "symlink" {
					if err := os.Symlink("server.db.original", path); err != nil {
						test.Fatal(err)
					}
				} else {
					if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
						test.Fatal(err)
					}
				}
			case "writable-fd":
				writable, err := os.OpenFile(path, os.O_RDWR, 0)
				if err != nil {
					test.Fatal(err)
				}
				defer writable.Close()
				fixture.database = writable
			default:
				if err := os.WriteFile(path+"-"+mode, nil, 0600); err != nil {
					test.Fatal(err)
				}
			}
			restoreRefused(test, fixture, ErrRestoreInvalid)
		})
	}
}

func TestRestoreDatabaseFailureOwnership(test *testing.T) {
	fixture := restoreFixture(test, 2, 2)
	for _, field := range []string{"database", "records", "inventory", "heap", "address-space", "staging"} {
		test.Run(field, func(test *testing.T) {
			limits := snapshotTestLimits()
			switch field {
			case "database":
				limits.MaxDBBytes = 1
			case "records":
				limits.MaxRecords = 1
			case "inventory":
				limits.MaxInventoryBytes = 1
			case "heap":
				limits.SQLiteHeapBytes = 65536
			case "address-space":
				limits.WorkerAddressSpaceBytes = limits.SQLiteHeapBytes + 1
			case "staging":
				limits.MaxStagingBytes = 1
			}
			evidence, err := InspectRestoreDatabase(test.Context(), fixture.parent, fixture.database, fixture.key, limits)
			if err == nil || evidence != (RestoreDatabaseEvidence{}) {
				test.Fatal("budget admitted")
			}
		})
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	evidence, err := InspectRestoreDatabase(ctx, fixture.parent, fixture.database, fixture.key, snapshotTestLimits())
	if !errors.Is(err, context.Canceled) || evidence.Valid() {
		test.Fatal("cancellation admitted", err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		test.Fatal(err)
	}
	var logging bytes.Buffer
	slog.New(slog.NewJSONHandler(&logging, nil)).Info("evidence", "value", evidence)
	diagnostics := fmt.Sprintf("%#v %v %s %s", evidence, ErrRestoreInvalid, encoded, logging.String())
	for _, forbidden := range []string{fixture.parent.Name(), "restore-private-config-sentinel", "restore-private-token-sentinel"} {
		if strings.Contains(diagnostics, forbidden) {
			test.Fatal("private data leaked")
		}
	}
	if _, err := fixture.database.Seek(0, io.SeekCurrent); err != nil {
		test.Fatal("borrowed FD closed")
	}
}
