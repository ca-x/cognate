package state

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/configurationasset"
	"github.com/ca-x/cognate/internal/server/ent/pack"
	"github.com/ca-x/cognate/internal/server/ent/packconfigurationattachment"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

func packConfigurationStores(test *testing.T) (*Store, *Store, Pack, ConfigurationAsset) {
	test.Helper()
	store, peer := refreshStores(test)
	repository := refreshCollect(test, store, "org/pack-configuration", refreshSkill("skills/example", "a"))
	entry := refreshPack(test, store, PackSelection{SkillID: repository.Repository.Skills[0].ID})
	draft := configurationStorageDraft()
	draft.AgentID = "claude"
	asset, err := store.CreateConfigurationAsset(test.Context(), "alice", draft)
	if err != nil {
		test.Fatal(err)
	}
	return store, peer, entry, asset
}

func packConfigurationReferenceFor(asset ConfigurationAsset) []PackConfigurationReference {
	return []PackConfigurationReference{{AssetID: asset.ID, AssetRevision: asset.Revision, TargetID: asset.AgentID + ".provider.user"}}
}

func packConfigurationHold(after bool) (ent.Hook, <-chan struct{}, func()) {
	reached, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var held atomic.Bool
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if held.Swap(true) {
				return next.Mutate(ctx, mutation)
			}
			var value ent.Value
			var err error
			if after {
				value, err = next.Mutate(ctx, mutation)
			}
			close(reached)
			select {
			case <-resume:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if after {
				return value, err
			}
			return next.Mutate(ctx, mutation)
		})
	}, reached, func() { once.Do(func() { close(resume) }) }
}

func TestPackConfigurationTwoStoreSchedules(test *testing.T) {
	for _, scenario := range []string{"add-delete", "delete-add", "add-update", "update-add", "replace-replace", "skills-replace", "replace-skills"} {
		test.Run(scenario, func(test *testing.T) {
			store, peer, entry, asset := packConfigurationStores(test)
			refs := packConfigurationReferenceFor(asset)
			packRevision, configurationRevision := int64(1), int64(1)
			if strings.Contains(scenario, "skills") {
				if _, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 1, 1, refs); err != nil {
					test.Fatal(err)
				}
				packRevision, configurationRevision = 2, 2
			}
			ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
			defer cancel()
			firstHook, firstReached, releaseFirst := packConfigurationHold(true)
			secondHook, secondReached, releaseSecond := packConfigurationHold(false)
			defer releaseFirst()
			defer releaseSecond()
			attach := func(target *Store, references []PackConfigurationReference) func() error {
				return func() error {
					result, err := target.ReplacePackConfigurationAttachments(ctx, "alice", entry.ID, packRevision, configurationRevision, references)
					if err != nil && !reflect.DeepEqual(result, PackConfigurationSnapshot{}) {
						return errors.New("failed contender published a snapshot")
					}
					return err
				}
			}
			deleteAsset := func(target *Store) func() error {
				return func() error { return target.DeleteConfigurationAsset(ctx, "alice", asset.ID, 1) }
			}
			updateAsset := func(target *Store) func() error {
				return func() error {
					draft := configurationStorageDraft()
					draft.AgentID, draft.Name = "claude", "Changed"
					_, err := target.UpdateConfigurationAsset(ctx, "alice", asset.ID, 1, draft)
					return err
				}
			}
			skills := func(target *Store) func() error {
				return func() error {
					_, err := target.UpdatePack(ctx, "alice", entry.ID, packRevision, PackDraft{Name: "Skills updated", Items: []PackSelection{{SkillID: entry.Items[0].SkillID}}})
					return err
				}
			}
			first, second := attach(store, refs), attach(peer, nil)
			var expectedSecond error = ErrPackConflict
			switch scenario {
			case "delete-add", "update-add":
				store.state.client.ConfigurationAsset.Use(firstHook)
				peer.state.client.Pack.Use(secondHook)
				second = attach(peer, refs)
				if scenario == "delete-add" {
					first, expectedSecond = deleteAsset(store), ErrConfigurationNotFound
				} else {
					first, expectedSecond = updateAsset(store), ErrConfigurationConflict
				}
			case "add-delete", "add-update":
				store.state.client.Pack.Use(firstHook)
				peer.state.client.ConfigurationAsset.Use(secondHook)
				if scenario == "add-delete" {
					second, expectedSecond = deleteAsset(peer), ErrConfigurationInUse
				} else {
					second, expectedSecond = updateAsset(peer), nil
				}
			default:
				store.state.client.Pack.Use(firstHook)
				peer.state.client.Pack.Use(secondHook)
				if scenario == "skills-replace" {
					first = skills(store)
				} else if scenario == "replace-skills" {
					second = skills(peer)
				}
			}
			firstDone, secondDone := make(chan error, 1), make(chan error, 1)
			var workers sync.WaitGroup
			defer func() { cancel(); releaseFirst(); releaseSecond(); workers.Wait() }()
			workers.Go(func() { firstDone <- first() })
			select {
			case <-firstReached:
			case err := <-firstDone:
				test.Fatalf("first operation missed held SQL write: %v", err)
			case <-ctx.Done():
				test.Fatal("first SQL write did not arrive")
			}
			workers.Go(func() { secondDone <- second() })
			select {
			case <-secondReached:
			case err := <-secondDone:
				test.Fatalf("contender missed transaction barrier: %v", err)
			case <-ctx.Done():
				test.Fatal("contender did not reach SQL mutation")
			}
			releaseFirst()
			if err := <-firstDone; err != nil {
				test.Fatalf("first writer failed: %v", err)
			}
			releaseSecond()
			if err := <-secondDone; !errors.Is(err, expectedSecond) {
				test.Fatalf("wrong serialized contender result: got %v want %v", err, expectedSecond)
			}
			current, err := peer.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
			if err != nil {
				test.Fatal(err)
			}
			expectedCount := 1
			if scenario == "delete-add" || scenario == "update-add" {
				expectedCount = 0
			}
			if len(current.Entries) != expectedCount {
				test.Fatal("competition left dangling/lost/partial references")
			}
			if scenario == "add-update" && (current.Entries[0].AssetRevision != 1 || current.Entries[0].State != PackConfigurationStaleAsset) {
				test.Fatal("concurrent asset update followed pin or hid stale state")
			}
			if expectedCount == 1 {
				if err := peer.DeleteConfigurationAsset(test.Context(), "alice", asset.ID, 1); scenario != "add-update" && !errors.Is(err, ErrConfigurationInUse) {
					test.Fatalf("competition left unprotected reference: %v", err)
				}
			}
		})
	}
}

func TestPackConfigurationRollbackOwnerClosureAndOverflow(test *testing.T) {
	store, peer, entry, asset := packConfigurationStores(test)
	refs := packConfigurationReferenceFor(asset)
	initial, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 1, 1, refs)
	if err != nil {
		test.Fatal(err)
	}
	foreignDraft := configurationStorageDraft()
	foreignDraft.AgentID = "claude"
	foreign, err := store.CreateConfigurationAsset(test.Context(), "bob", foreignDraft)
	if err != nil {
		test.Fatal(err)
	}
	invalid := append(refs, packConfigurationReferenceFor(foreign)...)
	if result, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 2, 2, invalid); !errors.Is(err, ErrConfigurationNotFound) || !reflect.DeepEqual(result, PackConfigurationSnapshot{}) {
		test.Fatalf("cross-owner group was partially accepted: %v", err)
	}
	unchanged, err := peer.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if err != nil || !reflect.DeepEqual(unchanged, initial) {
		test.Fatal("transaction rollback did not restore revisions/reference group")
	}
	if err := store.state.client.Pack.Update().Where(pack.PublicIDEQ(entry.ID)).SetConfigurationRevision(math.MaxInt64).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if _, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 2, math.MaxInt64, refs); !errors.Is(err, ErrPackConflict) {
		test.Fatal("stored configuration revision overflow accepted")
	}
	owner, err := store.state.client.User.Query().Where(user.UserKeyEQ("bob")).Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	foreignRow, err := store.state.client.ConfigurationAsset.Query().Where(configurationasset.PublicIDEQ(foreign.ID)).Only(test.Context())
	if err != nil || foreignRow.UserID != owner.ID {
		test.Fatal("foreign fixture unavailable")
	}
	if _, err := store.state.client.PackConfigurationAttachment.Update().Where(packconfigurationattachment.PublicIDEQ(initial.Entries[0].ID)).SetAssetRevision(2).Save(test.Context()); err != nil {
		test.Fatal(err)
	}
	stale, err := peer.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if err != nil || stale.Entries[0].State != PackConfigurationStaleAsset {
		test.Fatal("future pin was treated as current")
	}
	row, err := store.state.client.PackConfigurationAttachment.Query().Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	transaction, err := store.state.client.Tx(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.PackConfigurationAttachment.Delete().Where(packconfigurationattachment.IDEQ(row.ID)).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := transaction.PackConfigurationAttachment.Create().SetPublicID(row.PublicID).SetPackID(row.PackID).SetConfigurationAssetID(foreignRow.ID).SetAssetRevision(1).SetTargetID("claude.provider.user").SetOrdinal(0).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		test.Fatal(err)
	}
	result, err := peer.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if !errors.Is(err, ErrConfigurationUnavailable) || !reflect.DeepEqual(result, PackConfigurationSnapshot{}) {
		test.Fatalf("FK presence was mistaken for same-owner closure: %v", err)
	}
}

func TestPackConfigurationCommitUnknownReturnsZeroWithoutRetry(test *testing.T) {
	store, peer, entry, asset := packConfigurationStores(test)
	var commits atomic.Int64
	store.state.client.Pack.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			transaction, err := mutation.(*ent.PackMutation).Tx()
			if err != nil {
				return nil, err
			}
			transaction.OnCommit(func(next ent.Committer) ent.Committer {
				return ent.CommitFunc(func(ctx context.Context, transaction *ent.Tx) error {
					commits.Add(1)
					if err := next.Commit(ctx, transaction); err != nil {
						return err
					}
					return errors.New("synthetic commit response loss")
				})
			})
			return next.Mutate(ctx, mutation)
		})
	})
	result, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 1, 1, packConfigurationReferenceFor(asset))
	if !errors.Is(err, ErrConfigurationUnavailable) || !reflect.DeepEqual(result, PackConfigurationSnapshot{}) || commits.Load() != 1 {
		test.Fatal("unknown commit published success or retried")
	}
	current, err := peer.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if err != nil || current.PackRevision != 2 || current.ConfigurationRevision != 2 || len(current.Entries) != 1 {
		test.Fatal("commit response loss lost committed state")
	}
}

func TestPackConfigurationCreateFailureRestoresPreviousGroup(test *testing.T) {
	store, peer, entry, asset := packConfigurationStores(test)
	initial, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 1, 1, packConfigurationReferenceFor(asset))
	if err != nil {
		test.Fatal(err)
	}
	draft := configurationStorageDraft()
	draft.AgentID = "claude"
	other, err := store.CreateConfigurationAsset(test.Context(), "alice", draft)
	if err != nil {
		test.Fatal(err)
	}
	var inserts atomic.Int64
	store.state.client.PackConfigurationAttachment.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if mutation.Op().Is(ent.OpCreate) && inserts.Add(1) == 2 {
				return nil, errors.New("synthetic second attachment failure")
			}
			return next.Mutate(ctx, mutation)
		})
	})
	refs := append(packConfigurationReferenceFor(other), packConfigurationReferenceFor(asset)...)
	result, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 2, 2, refs)
	if !errors.Is(err, ErrConfigurationUnavailable) || !reflect.DeepEqual(result, PackConfigurationSnapshot{}) || inserts.Load() != 2 {
		test.Fatalf("failed bulk group published data or missed fault: %v", err)
	}
	current, err := peer.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if err != nil || !reflect.DeepEqual(current, initial) {
		test.Fatal("failed bulk replacement lost previous group/order/IDs/revisions")
	}
}

func TestPackConfigurationDigestGolden(test *testing.T) {
	snapshot := PackConfigurationSnapshot{PackID: "P", PackRevision: 2, ConfigurationRevision: 3, Entries: []PackConfigurationEntry{{ID: "A", AssetID: "C", AssetRevision: 4, TargetID: "claude.provider.user", AgentID: "claude", Kind: ConfigurationKindProvider, SchemaVersion: 1}}}
	expected, err := hex.DecodeString("7931e5247ca9e7e853ac870e18c58ffb059aa2ead12b35a916830b7cd7d09043")
	if err != nil {
		test.Fatal(err)
	}
	digest := packConfigurationDigest(snapshot)
	if !reflect.DeepEqual(digest[:], expected) {
		test.Fatal("snapshot digest canonical framing changed")
	}
	snapshot.Entries[0].State = PackConfigurationStaleAsset
	snapshot.Entries[0].NativeAvailability = "synthetic-other-availability"
	if packConfigurationDigest(snapshot) != digest {
		test.Fatal("dynamic state was added to snapshot binding")
	}
}

func TestPackConfigurationCanceledClosedAndStorageFailure(test *testing.T) {
	store, peer, entry, asset := packConfigurationStores(test)
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	result, err := store.ReplacePackConfigurationAttachments(ctx, "alice", entry.ID, 1, 1, packConfigurationReferenceFor(asset))
	if !errors.Is(err, ErrConfigurationUnavailable) || !reflect.DeepEqual(result, PackConfigurationSnapshot{}) {
		test.Fatal("canceled replacement returned metadata")
	}
	current, err := peer.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if err != nil || current.PackRevision != 1 || current.ConfigurationRevision != 1 || len(current.Entries) != 0 {
		test.Fatal("canceled replacement changed storage")
	}
	store.state.client.PackConfigurationAttachment.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			return nil, errors.New("synthetic SQL error private body")
		})
	})
	result, err = store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 1, 1, packConfigurationReferenceFor(asset))
	if !errors.Is(err, ErrConfigurationUnavailable) || !reflect.DeepEqual(result, PackConfigurationSnapshot{}) || strings.Contains(err.Error(), "private") {
		test.Fatal("storage error leaked details or snapshot")
	}
	current, err = peer.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if err != nil || current.PackRevision != 1 || current.ConfigurationRevision != 1 {
		test.Fatal("storage failure failed atomic rollback")
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	result, err = store.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if !errors.Is(err, ErrConfigurationUnavailable) || !reflect.DeepEqual(result, PackConfigurationSnapshot{}) {
		test.Fatal("closed store read returned metadata")
	}
}

func TestPackConfigurationLegacySchemaMigration(test *testing.T) {
	store, path, key := configurationStorageFixture(test)
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	for _, statement := range []string{
		"DROP TABLE pack_configuration_attachments",
		"ALTER TABLE packs DROP COLUMN configuration_revision",
		"INSERT INTO packs (public_id,user_id,name,description,revision,item_count,created_at,updated_at) SELECT 'LEGACYPACKAAAAAAAAAAAAAAAA',id,'Legacy pack','',7,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP FROM users WHERE user_key='alice'",
	} {
		if _, err := database.ExecContext(test.Context(), statement); err != nil {
			test.Fatal(err)
		}
	}
	reopened, err := Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	row, err := reopened.state.client.Pack.Query().Where(pack.NameEQ("Legacy pack")).Only(test.Context())
	if err != nil || row.Revision != 7 || row.ConfigurationRevision != 1 {
		test.Fatalf("legacy SQLite migration failed backfill: %v", err)
	}
	if exists, err := reopened.state.client.PackConfigurationAttachment.Query().Exist(test.Context()); err != nil || exists {
		test.Fatal("migration invented attachments or omitted new table")
	}
}
