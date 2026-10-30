package state

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/configurationorder"
	"github.com/ca-x/cognate/internal/server/ent/user"
	"github.com/ca-x/cognate/internal/server/maintenance"
)

func TestConfigurationOrderLegacyAndCiphertext(test *testing.T) {
	store, _, _ := configurationStorageFixture(test)
	first, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
	if err != nil {
		test.Fatal(err)
	}
	second, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
	if err != nil {
		test.Fatal(err)
	}
	before := configurationStoredRow(test, store, first.ID)
	if _, err := store.state.client.ConfigurationOrder.Delete().Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	legacy, err := store.ConfigurationAssetOrder(test.Context(), "alice", "codex", ConfigurationKindProvider)
	if err != nil || legacy.Revision != 0 || len(legacy.Assets) != 2 || legacy.Assets[0].ID != second.ID {
		test.Fatalf("legacy fallback: %+v %v", legacy, err)
	}
	count, err := store.state.client.ConfigurationOrder.Query().Count(test.Context())
	if err != nil || count != 0 {
		test.Fatalf("read backfilled: %d %v", count, err)
	}
	got, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", ConfigurationOrderRequest{AgentID: "codex", Kind: ConfigurationKindProvider, IDs: []string{first.ID, second.ID}})
	if err != nil || got.Revision != 1 {
		test.Fatalf("legacy materialization: %+v %v", got, err)
	}
	after := configurationStoredRow(test, store, first.ID)
	if before.ID != after.ID || before.PublicID != after.PublicID || before.UserID != after.UserID || before.AgentID != after.AgentID || before.Kind != after.Kind || before.Name != after.Name || before.SchemaVersion != after.SchemaVersion || before.Revision != after.Revision || !before.CreatedAt.Equal(after.CreatedAt) || !before.UpdatedAt.Equal(after.UpdatedAt) || !bytes.Equal(before.Ciphertext, after.Ciphertext) {
		test.Fatal("order modified asset row, ciphertext or timestamps")
	}
	account, err := findUser(test.Context(), store.state.client, "alice")
	if err != nil {
		test.Fatal(err)
	}
	partition, err := configurationOrderRow(test.Context(), store.state.client, account.ID, "codex", ConfigurationKindProvider)
	if err != nil {
		test.Fatal(err)
	}
	if err := store.state.client.ConfigurationOrder.UpdateOneID(partition.ID).SetRevision(math.MaxInt64).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	result, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", ConfigurationOrderRequest{AgentID: "codex", Kind: ConfigurationKindProvider, ExpectedRevision: math.MaxInt64, IDs: []string{second.ID, first.ID}})
	if !errors.Is(err, ErrConfigurationLimit) || !reflect.DeepEqual(result, ConfigurationOrderSnapshot{}) {
		test.Fatalf("overflow accepted: %+v %v", result, err)
	}
	if _, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft()); !errors.Is(err, ErrConfigurationLimit) {
		test.Fatalf("overflow membership did not rollback asset: %v", err)
	}
	if count, err := store.state.client.ConfigurationAsset.Query().Count(test.Context()); err != nil || count != 2 {
		test.Fatalf("overflow leaked asset: %d %v", count, err)
	}
}

func TestConfigurationOrderCommitUnknownAndCancelTail(test *testing.T) {
	for _, mode := range []string{"unknown", "cancel-tail"} {
		test.Run(mode, func(test *testing.T) {
			store, path, key := configurationStorageFixture(test)
			first, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
			if err != nil {
				test.Fatal(err)
			}
			second, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
			if err != nil {
				test.Fatal(err)
			}
			peer, err := Open(test.Context(), path, key)
			if err != nil {
				test.Fatal(err)
			}
			defer peer.Close()
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			var once sync.Once
			var commits atomic.Int64
			store.state.client.ConfigurationOrder.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					transaction, err := mutation.(*ent.ConfigurationOrderMutation).Tx()
					if err != nil {
						return nil, err
					}
					once.Do(func() {
						transaction.OnCommit(func(next ent.Committer) ent.Committer {
							return ent.CommitFunc(func(ctx context.Context, transaction *ent.Tx) error {
								commits.Add(1)
								if err := next.Commit(ctx, transaction); err != nil {
									return err
								}
								if mode == "cancel-tail" {
									cancel()
									return nil
								}
								return errors.New("synthetic committed response loss")
							})
						})
					})
					return next.Mutate(ctx, mutation)
				})
			})
			result, err := store.ReplaceConfigurationAssetOrder(ctx, "alice", ConfigurationOrderRequest{AgentID: "codex", Kind: ConfigurationKindProvider, ExpectedRevision: 2, IDs: []string{second.ID, first.ID}})
			if !errors.Is(err, ErrConfigurationUnavailable) || !reflect.DeepEqual(result, ConfigurationOrderSnapshot{}) || commits.Load() != 1 {
				test.Fatalf("unknown/tail returned success or replayed: %+v %v commits%d", result, err, commits.Load())
			}
			durable, err := peer.ConfigurationAssetOrder(test.Context(), "alice", "codex", ConfigurationKindProvider)
			if err != nil || durable.Revision != 3 || durable.Assets[0].ID != second.ID {
				test.Fatalf("unknown re-read lost actual commit: %+v %v", durable, err)
			}
		})
	}
}

func TestConfigurationOrderCapacityAndCorruptMembership(test *testing.T) {
	store, _, _ := configurationStorageFixture(test)
	ids := make([]string, 0, MaxConfigurationAssets)
	for range MaxConfigurationAssets {
		asset, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
		if err != nil {
			test.Fatal(err)
		}
		ids = append(ids, asset.ID)
	}
	if _, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft()); !errors.Is(err, ErrConfigurationLimit) {
		test.Fatalf("capacity exceeded: %v", err)
	}
	for left, right := 0, len(ids)-1; left < right; left, right = left+1, right-1 {
		ids[left], ids[right] = ids[right], ids[left]
	}
	got, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", ConfigurationOrderRequest{AgentID: "codex", Kind: ConfigurationKindProvider, ExpectedRevision: 250, IDs: ids})
	if err != nil || got.Revision != 251 || len(got.Assets) != 250 || got.Assets[0].ID != ids[0] {
		test.Fatalf("full bounded permutation: %+v %v", got, err)
	}
	got.Assets[0].Name = "caller modified snapshot"
	read, err := store.ConfigurationAssetOrder(test.Context(), "alice", "codex", ConfigurationKindProvider)
	if err != nil || read.Assets[0].Name == got.Assets[0].Name {
		test.Fatalf("snapshot alias escaped: %v", err)
	}
	if _, err := store.state.client.ConfigurationOrderEntry.Delete().Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	failed, err := store.ConfigurationAssetOrder(test.Context(), "alice", "codex", ConfigurationKindProvider)
	if !errors.Is(err, ErrConfigurationUnavailable) || !reflect.DeepEqual(failed, ConfigurationOrderSnapshot{}) {
		test.Fatalf("corrupt persisted order silently repaired/truncated: %+v %v", failed, err)
	}
}

func TestConfigurationOrderImportHookAndOwnerCascade(test *testing.T) {
	store, _, _ := configurationStorageFixture(test)
	err := store.catalogTransaction(test.Context(), func(tx *ent.Tx) error {
		account, err := findUser(test.Context(), tx.Client(), "alice")
		if err != nil {
			return err
		}
		for range 2 {
			if _, err := store.createConfigurationAssetTx(test.Context(), tx, account, configurationStorageDraft()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		test.Fatal(err)
	}
	order, err := store.ConfigurationAssetOrder(test.Context(), "alice", "codex", ConfigurationKindProvider)
	if err != nil || order.Revision != 2 || len(order.Assets) != 2 {
		test.Fatalf("import's central insertion helper bypassed membership: %+v %v", order, err)
	}
	for _, asset := range order.Assets {
		if err := store.DeleteConfigurationAsset(test.Context(), "alice", asset.ID, 1); err != nil {
			test.Fatal(err)
		}
	}
	account, err := findUser(test.Context(), store.state.client, "alice")
	if err != nil {
		test.Fatal(err)
	}
	if err := store.state.client.User.DeleteOneID(account.ID).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if count, err := store.state.client.ConfigurationOrder.Query().Where(configurationorder.UserIDEQ(account.ID)).Count(test.Context()); err != nil || count != 0 {
		test.Fatalf("owner cascade left order tombstone: %d %v", count, err)
	}
	if exists, err := store.state.client.User.Query().Where(user.UserKeyEQ("bob")).Exist(test.Context()); err != nil || !exists {
		test.Fatalf("cascade touched other owner: %v", err)
	}
}

func TestConfigurationOrderManagedCloseDrain(test *testing.T) {
	store, _, _ := managedStoreFixture(test)
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	first, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
	if err != nil {
		test.Fatal(err)
	}
	second, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
	if err != nil {
		test.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	store.state.client.ConfigurationOrder.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			select {
			case <-entered:
			default:
				close(entered)
				<-release
			}
			return next.Mutate(ctx, mutation)
		})
	})
	done := make(chan error, 1)
	go func() {
		_, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", ConfigurationOrderRequest{AgentID: "codex", Kind: ConfigurationKindProvider, ExpectedRevision: 2, IDs: []string{second.ID, first.ID}})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		test.Fatal("order SQL barrier not entered")
	}
	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		_, releaseWriter, err := store.enterWriter(test.Context())
		if errors.Is(err, maintenance.ErrClosed) {
			break
		}
		if err == nil {
			releaseWriter()
		}
		select {
		case <-deadline.C:
			test.Fatal("Close did not reject admission")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case err := <-closed:
		test.Fatalf("Close escaped live order writer: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			test.Fatalf("leased writer failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		test.Fatal("order writer did not drain")
	}
	select {
	case err := <-closed:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		test.Fatal("Close did not join writer")
	}
	if result, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", ConfigurationOrderRequest{AgentID: "codex", Kind: ConfigurationKindProvider}); err == nil || !reflect.DeepEqual(result, ConfigurationOrderSnapshot{}) {
		test.Fatalf("closed Store admitted order writer: %+v %v", result, err)
	}
}

func TestConfigurationOrderSourceGuard(test *testing.T) {
	store, peer, base, _, _ := sourceStarFixture(test)
	first, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
	if err != nil {
		test.Fatal(err)
	}
	second, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
	if err != nil {
		test.Fatal(err)
	}
	ticket := sourceRefreshTicketFor(test, store, base.Repository.ID)
	if err := peer.state.client.GitHubCredential.Update().SetGeneration("source-order-stale-generation").Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	ctx := context.WithValue(test.Context(), sourceRefreshContextKey{}, ticket)
	result, err := store.ReplaceConfigurationAssetOrder(ctx, "alice", ConfigurationOrderRequest{AgentID: "codex", Kind: ConfigurationKindProvider, ExpectedRevision: 2, IDs: []string{second.ID, first.ID}})
	if !errors.Is(err, ErrConfigurationUnavailable) || !reflect.DeepEqual(result, ConfigurationOrderSnapshot{}) {
		test.Fatalf("stale source guard published order: %+v %v", result, err)
	}
	durable, err := peer.ConfigurationAssetOrder(test.Context(), "alice", "codex", ConfigurationKindProvider)
	if err != nil || durable.Revision != 2 || durable.Assets[0].ID != first.ID {
		test.Fatalf("source rejection changed order: %+v %v", durable, err)
	}
}

func TestConfigurationOrderConcurrentContentAndMembership(test *testing.T) {
	for _, mode := range []string{"content", "create", "delete"} {
		test.Run(mode, func(test *testing.T) {
			store, path, key := configurationStorageFixture(test)
			first, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
			if err != nil {
				test.Fatal(err)
			}
			second, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft())
			if err != nil {
				test.Fatal(err)
			}
			peer, err := Open(test.Context(), path, key)
			if err != nil {
				test.Fatal(err)
			}
			defer peer.Close()
			hook, reached, resume := packConfigurationHold(false)
			defer resume()
			store.state.client.ConfigurationOrder.Use(hook)
			type outcome struct {
				result ConfigurationOrderSnapshot
				err    error
			}
			finished := make(chan outcome, 1)
			go func() {
				result, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", ConfigurationOrderRequest{AgentID: "codex", Kind: ConfigurationKindProvider, ExpectedRevision: 2, IDs: []string{second.ID, first.ID}})
				finished <- outcome{result, err}
			}()
			select {
			case <-reached:
			case <-time.After(3 * time.Second):
				test.Fatal("order did not reach real SQL fence")
			}
			switch mode {
			case "content":
				draft := configurationStorageDraft()
				draft.Name = "concurrent content edit"
				if _, err := peer.UpdateConfigurationAsset(test.Context(), "alice", first.ID, 1, draft); err != nil {
					test.Fatal(err)
				}
			case "create":
				if _, err := peer.CreateConfigurationAsset(test.Context(), "alice", configurationStorageDraft()); err != nil {
					test.Fatal(err)
				}
			case "delete":
				if err := peer.DeleteConfigurationAsset(test.Context(), "alice", first.ID, 1); err != nil {
					test.Fatal(err)
				}
			}
			resume()
			var result outcome
			select {
			case result = <-finished:
			case <-time.After(3 * time.Second):
				test.Fatal("SQL retry did not join")
			}
			if mode == "content" {
				if result.err != nil || result.result.Revision != 3 || result.result.Assets[1].Revision != 2 || result.result.Assets[1].Name != "concurrent content edit" {
					test.Fatalf("content edit lost/mixed with reorder: %+v %v", result.result, result.err)
				}
			} else if !errors.Is(result.err, ErrConfigurationConflict) || !reflect.DeepEqual(result.result, ConfigurationOrderSnapshot{}) {
				test.Fatalf("membership race did not invalidate CAS: %+v %v", result.result, result.err)
			}
		})
	}
}
