package state_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/ca-x/cognate/internal/server/state"
)

func TestConfigurationOrderLifecycle(test *testing.T) {
	store, path, key := configurationFixture(test)
	first, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	second, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	before, err := store.ConfigurationAssetOrder(test.Context(), "alice", "codex", state.ConfigurationKindProvider)
	if err != nil || before.Revision != 2 || !reflect.DeepEqual(before.Assets, []state.ConfigurationAsset{first, second}) {
		test.Fatalf("created assets must append with independent membership revision: %+v %v", before, err)
	}
	after, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", state.ConfigurationOrderRequest{AgentID: "codex", Kind: state.ConfigurationKindProvider, ExpectedRevision: 2, IDs: []string{second.ID, first.ID}})
	if err != nil || after.Revision != 3 || !reflect.DeepEqual(after.Assets, []state.ConfigurationAsset{second, first}) {
		test.Fatalf("reorder must preserve all content metadata: %+v %v", after, err)
	}
	listed, err := store.ListConfigurationAssets(test.Context(), "alice")
	if err != nil || !reflect.DeepEqual(listed, after.Assets) {
		test.Fatalf("legacy list ignored durable order: %+v %v", listed, err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.ConfigurationAssetOrder(test.Context(), "alice", "codex", state.ConfigurationKindProvider)
	if err != nil || !reflect.DeepEqual(got, after) {
		test.Fatalf("order lost on reopen: %+v %v", got, err)
	}
}

func TestConfigurationOrderMembershipAndOwner(test *testing.T) {
	store, _, _ := configurationFixture(test)
	empty, err := store.ConfigurationAssetOrder(test.Context(), "alice", "codex", state.ConfigurationKindProvider)
	if err != nil || empty.Revision != 0 || len(empty.Assets) != 0 {
		test.Fatalf("fresh partition: %+v %v", empty, err)
	}
	request := state.ConfigurationOrderRequest{AgentID: "codex", Kind: state.ConfigurationKindProvider}
	unchanged, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", request)
	if err != nil || !reflect.DeepEqual(unchanged, empty) {
		test.Fatalf("empty no-op: %+v %v", unchanged, err)
	}
	first, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	second, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	foreign, err := store.CreateConfigurationAsset(test.Context(), "bob", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	owned, err := store.ConfigurationAssetOrder(test.Context(), "bob", "codex", state.ConfigurationKindProvider)
	if err != nil || !reflect.DeepEqual(owned.Assets, []state.ConfigurationAsset{foreign}) {
		test.Fatalf("order owner isolation: %+v %v", owned, err)
	}
	for _, ids := range [][]string{{first.ID}, {first.ID, foreign.ID}, {first.ID, first.ID}} {
		request.ExpectedRevision, request.IDs = 2, ids
		result, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", request)
		if err == nil || !reflect.DeepEqual(result, state.ConfigurationOrderSnapshot{}) {
			test.Fatalf("invalid membership accepted: %+v %v", result, err)
		}
	}
	request.ExpectedRevision, request.IDs = 2, []string{first.ID, second.ID}
	unchanged, err = store.ReplaceConfigurationAssetOrder(test.Context(), "alice", request)
	if err != nil || unchanged.Revision != 2 {
		test.Fatalf("no-op advanced revision: %+v %v", unchanged, err)
	}
	draft := configurationDraft()
	draft.Name = "content edit without reordering"
	updated, err := store.UpdateConfigurationAsset(test.Context(), "alice", first.ID, 1, draft)
	if err != nil {
		test.Fatal(err)
	}
	snapshot, err := store.ConfigurationAssetOrder(test.Context(), "alice", "codex", state.ConfigurationKindProvider)
	if err != nil || snapshot.Revision != 2 || snapshot.Assets[0] != updated {
		test.Fatalf("content edit moved display revision: %+v %v", snapshot, err)
	}
	if err := store.DeleteConfigurationAsset(test.Context(), "alice", second.ID, 1); err != nil {
		test.Fatal(err)
	}
	if err := store.DeleteConfigurationAsset(test.Context(), "alice", first.ID, 2); err != nil {
		test.Fatal(err)
	}
	tombstone, err := store.ConfigurationAssetOrder(test.Context(), "alice", "codex", state.ConfigurationKindProvider)
	if err != nil || tombstone.Revision != 4 || len(tombstone.Assets) != 0 {
		test.Fatalf("lost empty tombstone: %+v %v", tombstone, err)
	}
	request.ExpectedRevision, request.IDs = 0, nil
	if _, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", request); !errors.Is(err, state.ErrConfigurationConflict) {
		test.Fatalf("empty ABA accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	result, err := store.ConfigurationAssetOrder(ctx, "alice", "codex", state.ConfigurationKindProvider)
	if err == nil || !reflect.DeepEqual(result, state.ConfigurationOrderSnapshot{}) {
		test.Fatalf("canceled read published metadata: %+v %v", result, err)
	}
}

func TestConfigurationOrderTwoStores(test *testing.T) {
	store, path, key := configurationFixture(test)
	first, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	second, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	third, err := store.CreateConfigurationAsset(test.Context(), "alice", configurationDraft())
	if err != nil {
		test.Fatal(err)
	}
	peer, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	defer peer.Close()
	start, results := make(chan struct{}), make(chan error, 2)
	var workers sync.WaitGroup
	for index, writer := range []*state.Store{store, peer} {
		workers.Go(func() {
			<-start
			ids := []string{third.ID, first.ID, second.ID}
			if index == 1 {
				ids = []string{second.ID, third.ID, first.ID}
			}
			_, err := writer.ReplaceConfigurationAssetOrder(test.Context(), "alice", state.ConfigurationOrderRequest{AgentID: "codex", Kind: state.ConfigurationKindProvider, ExpectedRevision: 3, IDs: ids})
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, state.ErrConfigurationConflict) && !errors.Is(err, state.ErrConfigurationUnavailable) {
			test.Fatalf("unexpected race error: %v", err)
		}
	}
	if winners != 1 {
		test.Fatalf("CAS winners=%d", winners)
	}
	got, err := peer.ConfigurationAssetOrder(test.Context(), "alice", "codex", state.ConfigurationKindProvider)
	if err != nil || got.Revision != 4 {
		test.Fatalf("durable winner lost: %+v %v", got, err)
	}
}

func TestConfigurationOrderAllPartitionsAndReadonly(test *testing.T) {
	store, _, _ := configurationFixture(test)
	for _, agent := range []string{"claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"} {
		for _, kind := range []state.ConfigurationKind{state.ConfigurationKindProvider, state.ConfigurationKindMCP, state.ConfigurationKindPrompt, state.ConfigurationKindAgentSettings, state.ConfigurationKindWorkspace} {
			draft := configurationDraft()
			draft.AgentID, draft.Kind, draft.SchemaVersion = agent, kind, 65535
			first, err := store.CreateConfigurationAsset(test.Context(), "alice", draft)
			if err != nil {
				test.Fatal(err)
			}
			second, err := store.CreateConfigurationAsset(test.Context(), "alice", draft)
			if err != nil {
				test.Fatal(err)
			}
			got, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", state.ConfigurationOrderRequest{AgentID: agent, Kind: kind, ExpectedRevision: 2, IDs: []string{second.ID, first.ID}})
			if err != nil || got.Revision != 3 || !reflect.DeepEqual(got.Assets, []state.ConfigurationAsset{second, first}) {
				test.Fatalf("partition %s/%s: %+v %v", agent, kind, got, err)
			}
		}
	}
	draft := configurationDraft()
	draft.AgentID = "future-agent"
	unknown, err := store.CreateConfigurationAsset(test.Context(), "alice", draft)
	if err != nil {
		test.Fatal(err)
	}
	listed, err := store.ListConfigurationAssets(test.Context(), "alice")
	if err != nil || len(listed) != 141 || !slices.ContainsFunc(listed, func(asset state.ConfigurationAsset) bool { return asset == unknown }) {
		test.Fatalf("unknown fallback or complete list lost: %d %v", len(listed), err)
	}
	if _, err := store.ConfigurationAssetOrder(test.Context(), "alice", "future-agent", draft.Kind); !errors.Is(err, state.ErrConfigurationInvalid) {
		test.Fatalf("unknown partition became writable: %v", err)
	}
}

func TestConfigurationOrderPackPinsUnchanged(test *testing.T) {
	store, pack := packConfigurationFixture(test)
	first := packConfigurationAsset(test, store, "alice", "codex")
	second := packConfigurationAsset(test, store, "alice", "codex")
	before, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", pack.ID, 1, 1, []state.PackConfigurationReference{packConfigurationRef(first)})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.ReplaceConfigurationAssetOrder(test.Context(), "alice", state.ConfigurationOrderRequest{AgentID: "codex", Kind: state.ConfigurationKindProvider, ExpectedRevision: 2, IDs: []string{second.ID, first.ID}}); err != nil {
		test.Fatal(err)
	}
	after, err := store.PackConfigurationAttachments(test.Context(), "alice", pack.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		test.Fatalf("display reorder changed Pack pins/revisions/digest: %+v %+v %v", before, after, err)
	}
	if err := store.DeleteConfigurationAsset(test.Context(), "alice", first.ID, 1); !errors.Is(err, state.ErrConfigurationInUse) {
		test.Fatalf("PC1 InUse weakened: %v", err)
	}
	order, err := store.ConfigurationAssetOrder(test.Context(), "alice", "codex", state.ConfigurationKindProvider)
	if err != nil || order.Revision != 3 || len(order.Assets) != 2 {
		test.Fatalf("failed InUse deletion changed order: %+v %v", order, err)
	}
}
