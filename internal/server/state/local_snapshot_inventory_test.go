//go:build linux

package state

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"github.com/ca-x/cognate/internal/github"
	"net/url"
	"strings"
	"testing"
)

func TestSnapshotInventoryTypedRelations(test *testing.T) {
	store, path := snapshotTestStore(test)
	snapshotSeedRelations(test, store, 1)
	pool, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String())
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	for _, row := range []snapshotReference{
		{Kind: "repository", ID: 1, PublicID: "snapshot-repository-0", Owner: 1, Name: "org/repository0", Branch: "", ArchiveHash: strings.Repeat("0", 63) + "1", SkillCount: 1, Revision: 1},
		{Kind: "skill", ID: 1, PublicID: "snapshot-skill-0", Repository: 1, Path: "skills/example", ContentHash: strings.Repeat("a", 64), skillName: "Example", skillDescription: "bounded skill"},
		{Kind: "pack", ID: 1, PublicID: "snapshot-pack-0", Owner: 1, Revision: 1, ItemCount: 1},
		{Kind: "pack_item", ID: 1, Pack: 1, Skill: 1, Slug: "example"},
	} {
		if err = snapshotValidateReference(test.Context(), pool, row); err != nil {
			test.Fatal("typed relation failed", row.Kind, err)
		}
	}
	if err = snapshotValidateCategories(test.Context(), pool, 100); err != nil {
		test.Fatal("category relation failed", err)
	}
	var output bytes.Buffer
	counts, size, _, err := snapshotWriteInventory(test.Context(), pool, &output, snapshotTestLimits())
	if err != nil {
		test.Fatal("bounded actual SQL inventory failed", err)
	}
	if counts.total != 4 || size != int64(output.Len()) || bytes.Count(output.Bytes(), []byte{'\n'}) != 4 {
		test.Fatal("inventory counts differ")
	}
	var repositoryRow map[string]jsontext.Value
	if json.Unmarshal(bytes.Split(output.Bytes(), []byte{'\n'})[0], &repositoryRow) != nil || repositoryRow["branch"] == nil {
		test.Fatal("required branch absent")
	}
}

func TestSnapshotReferenceStrictWire(test *testing.T) {
	literal := `{"kind":"repository","id":1,"public_id":"snapshot-repository-0","owner":1,"name":"org/repository0","branch":"","archive_hash":"0000000000000000000000000000000000000000000000000000000000000001","skill_count":1,"revision":1}` + "\n"
	if _, _, err := snapshotReadRow(bufio.NewReaderSize(strings.NewReader(literal), 65536)); err != nil {
		test.Fatal("literal valid reference refused", err)
	}
	for name, input := range map[string]string{
		"missing":          strings.Replace(literal, `"branch":"",`, "", 1),
		"null":             strings.Replace(literal, `"branch":""`, `"branch":null`, 1),
		"duplicate":        strings.Replace(literal, `"id":1`, `"id":1,"id":1`, 1),
		"unknown":          strings.Replace(literal, `"id":1`, `"extra":0,"id":1`, 1),
		"wrong-type":       strings.Replace(literal, `"id":1`, `"id":"1"`, 1),
		"fraction":         strings.Replace(literal, `"id":1`, `"id":1.0`, 1),
		"trailing":         strings.TrimSuffix(literal, "\n") + "{}\n",
		"no-final-newline": strings.TrimSuffix(literal, "\n"),
		"too-large":        strings.Replace(literal, `"branch":""`, `"branch":"`+strings.Repeat("a", 65536)+`"`, 1),
	} {
		test.Run(name, func(test *testing.T) {
			if _, _, err := snapshotReadRow(bufio.NewReaderSize(strings.NewReader(input), 65536)); err == nil {
				test.Fatal("malformed reference admitted")
			}
		})
	}
}

func TestLocalSnapshotInvalidRelationsAndNoEnrollment(test *testing.T) {
	for name, statement := range map[string]string{
		"repo-count":           "UPDATE collected_repositories SET skill_count=2",
		"pack-count":           "UPDATE packs SET item_count=2",
		"cross-owner-item":     "UPDATE packs SET user_id=2",
		"cross-owner-category": "UPDATE categories SET user_id=2",
		"noncanonical-branch":  "UPDATE collected_repositories SET branch='HEAD'",
		"oversized-reference":  "UPDATE collected_skills SET path=printf('%01025d',0)",
		"missing-key-proof":    "DELETE FROM server_metadata WHERE name='credential-key-v1'",
		"missing-policy-proof": "DELETE FROM server_metadata WHERE name='catalog-observation-policy-v1'",
		"bad-key-proof":        "UPDATE server_metadata SET key_proof=zeroblob(64) WHERE name='credential-key-v1'",
		"bad-policy-proof":     "UPDATE server_metadata SET key_proof=zeroblob(64) WHERE name='catalog-observation-policy-v1'",
	} {
		test.Run(name, func(test *testing.T) {
			store, path := snapshotTestStore(test)
			snapshotSeedRelations(test, store, 1)
			pool, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path}).String())
			if err != nil {
				test.Fatal(err)
			}
			defer pool.Close()
			if _, err = pool.ExecContext(test.Context(), statement); err != nil {
				test.Fatal(err)
			}
			called := false
			result, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { called = true; return nil })
			if err == nil || called || result.get != nil {
				test.Fatal("invalid relation/proof admitted", name)
			}
			if strings.HasPrefix(name, "missing-") {
				var count int
				if pool.QueryRowContext(test.Context(), "SELECT count(*) FROM server_metadata").Scan(&count) != nil || count != 1 {
					test.Fatal("snapshot silently enrolled missing proof")
				}
			}
		})
	}
}

func TestLocalSnapshotRepresentativeCredentialAuthentication(test *testing.T) {
	store, _ := snapshotTestStore(test)
	token, err := github.NewToken("synthetic-snapshot-access", []string{"repo"})
	if err != nil {
		test.Fatal(err)
	}
	if _, err = store.PutCredential(test.Context(), "snapshot-private-owner", "viewer1", "", token); err != nil {
		test.Fatal(err)
	}
	_, err = store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil })
	if err != nil {
		test.Fatal("valid encrypted credential refused", err)
	}
	entry, err := store.state.client.GitHubCredential.Query().Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	entry.Ciphertext[len(entry.Ciphertext)-1] ^= 1
	if err = store.state.client.GitHubCredential.UpdateOneID(entry.ID).SetCiphertext(entry.Ciphertext).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	called := false
	result, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { called = true; return nil })
	if err == nil || called || result.get != nil {
		test.Fatal("unauthenticated representative credential minted candidate")
	}
}
