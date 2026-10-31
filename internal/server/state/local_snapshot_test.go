//go:build linux

package state

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/maintenance"
)

func snapshotSeedRelations(test *testing.T, store *Store, count int) {
	test.Helper()
	if err := store.CreateUser(test.Context(), "snapshot-second-owner"); err != nil {
		test.Fatal(err)
	}
	owners, err := store.state.client.User.Query().Order(ent.Asc("id")).All(test.Context())
	if err != nil || len(owners) != 2 {
		test.Fatal("owner fixture failed")
	}
	if _, err = store.WithMaintenanceWriter(test.Context(), func(ctx context.Context) (bool, error) {
		failure := func() error {
			transaction, err := store.state.client.Tx(ctx)
			if err != nil {
				return err
			}
			defer transaction.Rollback()
			for index := range count {
				owner := owners[index%2].ID
				repository, err := transaction.CollectedRepository.Create().SetPublicID(fmt.Sprintf("snapshot-repository-%d", index)).SetUserID(owner).SetNameWithOwner(fmt.Sprintf("org/repository%d", index)).SetBranch("").SetArchiveHash(fmt.Sprintf("%064x", index+1)).SetSkillCount(1).SetCollectedAt(time.Unix(1700000000, 0)).Save(ctx)
				if err != nil {
					return err
				}
				selected, err := transaction.CollectedSkill.Create().SetPublicID(fmt.Sprintf("snapshot-skill-%d", index)).SetRepositoryID(repository.ID).SetPath("skills/example").SetName("Example").SetDescription("bounded skill").SetContentHash(strings.Repeat("a", 64)).Save(ctx)
				if err != nil {
					return err
				}
				pack, err := transaction.Pack.Create().SetPublicID(fmt.Sprintf("snapshot-pack-%d", index)).SetUserID(owner).SetName("Pack").SetItemCount(1).SetCreatedAt(time.Unix(1700000000, 0)).SetUpdatedAt(time.Unix(1700000000, 0)).Save(ctx)
				if err != nil {
					return err
				}
				if err = transaction.PackItem.Create().SetPackID(pack.ID).SetSkillID(selected.ID).SetSlug("example").Exec(ctx); err != nil {
					return err
				}
				category, err := transaction.Category.Create().SetPublicID(fmt.Sprintf("snapshot-category-%d", index)).SetUserID(owner).SetName(fmt.Sprintf("category%d", index)).SetNormalizedName(fmt.Sprintf("category%d", index)).AddSkillIDs(selected.ID).Save(ctx)
				if err != nil || category == nil {
					return ErrLocalSnapshotInvalid
				}
			}
			return transaction.Commit()
		}()
		return failure == nil, failure
	}); err != nil {
		test.Fatal(err)
	}
}

func TestLocalSnapshotAllOwnersAndIndependentRestore(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("snapshot worker runtime is Linux-only")
	}
	store, path := snapshotTestStore(test)
	snapshotSeedRelations(test, store, 130)
	if _, err := store.CreateConfigurationAsset(test.Context(), "snapshot-private-owner", ConfigurationAssetDraft{AgentID: "codex", Kind: ConfigurationKindProvider, Name: "snapshot private config", SchemaVersion: 1, Payload: NewConfigurationPayload(bytes.Repeat([]byte("snapshot-private-config-sentinel"), 4096))}); err != nil {
		test.Fatal(err)
	}
	configuration, err := store.state.client.ConfigurationAsset.Query().Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	uncommitted, err := store.state.client.Tx(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	if err = uncommitted.User.Create().SetUserKey("not-committed-at-cut").Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	destination, err := os.CreateTemp(test.TempDir(), "restored-")
	if err != nil {
		test.Fatal(err)
	}
	destinationName := destination.Name()
	var referenceBytes bytes.Buffer
	var savedView LocalSnapshotView
	var savedContext context.Context
	var metadata LocalSnapshotMetadata
	seen := map[string]int{}
	summary, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
		savedView, savedContext = view, ctx
		var err error
		metadata, err = view.Metadata(ctx)
		if err != nil {
			return err
		}
		var cursor LocalSnapshotCursor
		for {
			page, err := view.NextReferences(ctx, cursor, 17)
			if err != nil {
				return err
			}
			for _, row := range page.Rows() {
				seen[row.Kind()]++
			}
			if page.Done() {
				break
			}
			cursor = page.Cursor()
		}
		if _, err = view.CopyReferences(ctx, &referenceBytes); err != nil {
			return err
		}
		if _, err = view.CopyDatabase(ctx, destination); err != nil {
			return err
		}
		return nil
	})
	if rollback := uncommitted.Rollback(); rollback != nil {
		test.Fatal(rollback)
	}
	if err != nil || summary.get == nil {
		test.Fatal("complete snapshot refused", err)
	}
	if err = destination.Sync(); err != nil {
		test.Fatal(err)
	}
	if err = destination.Close(); err != nil {
		test.Fatal(err)
	}
	for _, kind := range []string{"repository", "skill", "pack", "pack_item"} {
		if seen[kind] != 130 {
			test.Fatal("owner/kind/page omitted", kind, seen[kind])
		}
	}
	_, referenceHash, records := metadata.References()
	if records != 520 || referenceHash != sha256.Sum256(referenceBytes.Bytes()) {
		test.Fatal("reference receipt differs")
	}
	if _, err = savedView.Metadata(savedContext); err == nil {
		test.Fatal("expired view admitted")
	}
	if err = store.Close(); err != nil {
		test.Fatal(err)
	}
	if err = os.Rename(filepath.Dir(path), filepath.Dir(path)+"-offline"); err != nil {
		test.Fatal(err)
	}
	defer os.Rename(filepath.Dir(path)+"-offline", filepath.Dir(path))
	pool, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: destinationName, RawQuery: "mode=ro&immutable=1"}).String())
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	for _, table := range []string{"collected_repositories", "collected_skills", "packs", "pack_items", "categories", "category_skills"} {
		var count int
		if pool.QueryRowContext(test.Context(), "SELECT count(*) FROM "+table).Scan(&count) != nil || count != 130 {
			test.Fatal("independent restore omitted rows", table)
		}
	}
	var users int
	if pool.QueryRowContext(test.Context(), "SELECT count(*) FROM users").Scan(&users) != nil || users != 2 {
		test.Fatal("WAL cut included uncommitted or omitted committed rows")
	}
	var stored []byte
	if pool.QueryRowContext(test.Context(), "SELECT ciphertext FROM configuration_assets").Scan(&stored) != nil || !bytes.Equal(stored, configuration.Ciphertext) {
		test.Fatal("large encrypted field changed or disappeared")
	}
	var proof []byte
	var keyID string
	if pool.QueryRowContext(test.Context(), "SELECT key_id,key_proof FROM server_metadata WHERE name='credential-key-v1'").Scan(&keyID, &proof) != nil {
		test.Fatal("missing independent key proof")
	}
	digest := sha256.Sum256(bytes.Repeat([]byte{91}, 32))
	if keyID != base64.RawURLEncoding.EncodeToString(digest[:]) {
		test.Fatal("key identity changed")
	}
	block, _ := aes.NewCipher(bytes.Repeat([]byte{91}, 32))
	aead, _ := cipher.NewGCM(block)
	if len(proof) < 28 {
		test.Fatal("short key proof")
	}
	plain, err := aead.Open(nil, proof[:12], proof[12:], []byte("credential-key-v1:"+keyID))
	if err != nil || string(plain) != "credential-key-v1" {
		test.Fatal("independent proof authentication failed")
	}
	var integrity string
	if pool.QueryRowContext(test.Context(), "PRAGMA integrity_check").Scan(&integrity) != nil || integrity != "ok" {
		test.Fatal("independent SQL integrity failed")
	}
}

func TestLocalSnapshotPrivateCarriers(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("snapshot worker runtime is Linux-only")
	}
	store, _ := snapshotTestStore(test)
	snapshotSeedRelations(test, store, 1)
	_, err := store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
		metadata, err := view.Metadata(ctx)
		if err != nil {
			return err
		}
		page, err := view.NextReferences(ctx, LocalSnapshotCursor{}, 128)
		if err != nil {
			return err
		}
		values := []any{view, metadata, page, page.Cursor(), page.Rows(), ctx}
		for _, value := range values {
			for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
				text := fmt.Sprintf(format, struct{ hidden any }{value})
				for _, secret := range []string{"snapshot-private-owner", "snapshot-repository", "org/repository", "snapshot-stage", "key_proof"} {
					if strings.Contains(text, secret) {
						return ErrLocalSnapshotInvalid
					}
				}
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				if _, isContext := value.(context.Context); isContext {
					continue
				}
				return err
			}
			if bytes.Contains(encoded, []byte("org/repository")) {
				return ErrLocalSnapshotInvalid
			}
			var logs bytes.Buffer
			slog.New(slog.NewJSONHandler(&logs, nil)).Info("snapshot", "value", value)
			if strings.Contains(logs.String(), "org/repository") {
				return ErrLocalSnapshotInvalid
			}
		}
		rows := page.Rows()
		rows[0] = LocalSnapshotReference{}
		if page.Rows()[0].get == nil {
			return ErrLocalSnapshotInvalid
		}
		return nil
	})
	if err != nil {
		test.Fatal("private carrier regression", err)
	}
}

func TestLocalSnapshotInvalidAndDefaultModes(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("snapshot worker runtime is Linux-only")
	}
	store, path := snapshotTestStore(test)
	for _, limits := range []LocalSnapshotLimits{{}, func() LocalSnapshotLimits {
		value := snapshotTestLimits()
		value.WorkerAddressSpaceBytes = -1
		return value
	}()} {
		if result, err := store.WithLocalSnapshot(test.Context(), limits, func(context.Context, LocalSnapshotView) error { return nil }); err == nil || result.get != nil {
			test.Fatal("invalid limits admitted")
		}
	}
	plain, err := Open(test.Context(), filepath.Join(filepath.Dir(path), "unmanaged.sqlite"), bytes.Repeat([]byte{91}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer plain.Close()
	if result, err := plain.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil }); err == nil || result.get != nil {
		test.Fatal("unmanaged store silently enrolled")
	}
	_, err = store.WithMaintenanceWriter(test.Context(), func(ctx context.Context) (bool, error) {
		_, err := store.WithLocalSnapshot(ctx, snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil })
		return false, err
	})
	if !errors.Is(err, maintenance.ErrLease) {
		test.Fatal("writer context admitted snapshot", err)
	}
	var result LocalSnapshotSummary
	result, err = store.WithLocalSnapshot(test.Context(), snapshotTestLimits(), func(ctx context.Context, view LocalSnapshotView) error {
		_, err := store.WithLocalSnapshot(ctx, snapshotTestLimits(), func(context.Context, LocalSnapshotView) error { return nil })
		if !errors.Is(err, maintenance.ErrLease) {
			return ErrLocalSnapshotInvalid
		}
		if _, err = view.CopyDatabase(ctx, io.Discard); err != nil {
			return err
		}
		return nil
	})
	if err != nil || result.get == nil {
		test.Fatal("recursive admission boundary failed", err)
	}
}
