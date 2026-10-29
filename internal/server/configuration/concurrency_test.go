package configuration_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type barrierStore struct {
	configuration.Store
	arrived        chan struct{}
	release        chan struct{}
	reads          atomic.Int32
	writes         atomic.Int32
	firstWrite     string
	firstWriteDone chan struct{}
}

func (store *barrierStore) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	metadata, payload, err := store.Store.ReadConfigurationAssetVersion(ctx, owner, id)
	store.reads.Add(1)
	store.arrived <- struct{}{}
	select {
	case <-store.release:
		return metadata, payload, err
	case <-ctx.Done():
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, ctx.Err()
	}
}

func (store *barrierStore) UpdateConfigurationAsset(ctx context.Context, owner, id string, revision int64, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	store.writes.Add(1)
	if store.firstWrite != "" {
		if draft.Name == store.firstWrite {
			defer close(store.firstWriteDone)
		} else {
			select {
			case <-store.firstWriteDone:
			case <-ctx.Done():
				return state.ConfigurationAsset{}, ctx.Err()
			}
		}
	}
	return store.Store.UpdateConfigurationAsset(ctx, owner, id, revision, draft)
}

func TestConcurrentUpdatesHaveOneWinnerAndNoKeepRebase(test *testing.T) {
	for _, schedule := range []struct{ name, firstWrite string }{
		{name: "contended"},
		{name: "keep-first", firstWrite: "Keep winner"},
		{name: "replace-first", firstWrite: "Replace winner"},
	} {
		test.Run(schedule.name, func(test *testing.T) {
			testConcurrentUpdates(test, schedule.firstWrite)
		})
	}
}

func testConcurrentUpdates(test *testing.T, firstWrite string) {
	test.Helper()
	store, _, _ := fixture(test)
	base := configuration.New(store)
	created, err := base.Create(test.Context(), "alice", draft(), replacement(sentinel))
	if err != nil {
		test.Fatal(err)
	}
	barrier := &barrierStore{Store: store, arrived: make(chan struct{}, 2), release: make(chan struct{}), firstWrite: firstWrite, firstWriteDone: make(chan struct{})}
	service := configuration.New(barrier)
	type result struct {
		name string
		view configuration.EditView
		err  error
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for _, name := range []string{"Keep winner", "Replace winner"} {
		workers.Go(func() {
			input := draft()
			input.Name = name
			var changes []configuration.SecretMutation
			if name == "Replace winner" {
				changes = replacement(sentinel + "-replacement")
			}
			view, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, input, changes)
			results <- result{name: name, view: view, err: err}
		})
	}
	for range 2 {
		select {
		case <-barrier.arrived:
		case <-test.Context().Done():
			test.Fatal("workers did not reach snapshot barrier")
		}
	}
	close(barrier.release)
	workers.Wait()
	close(results)
	winner := ""
	for result := range results {
		if result.err == nil {
			if winner != "" || result.view.Metadata.Revision != 2 {
				test.Fatal("multiple successful CAS updates")
			}
			winner = result.name
		} else if result.view.Metadata.ID != "" || (!errors.Is(result.err, configuration.ErrConflict) && (firstWrite != "" || !errors.Is(result.err, configuration.ErrUnavailable))) {
			test.Fatalf("loser must be a permitted concurrent failure and zero view: %v", result.err)
		}
	}
	if winner == "" || barrier.reads.Load() != 2 || barrier.writes.Load() != 2 {
		test.Fatal("missing winner or automatic retry")
	}
	if firstWrite != "" && winner != firstWrite {
		test.Fatal("the committed writer did not win its scheduled CAS")
	}
	expected := sentinel
	if winner == "Replace winner" {
		expected += "-replacement"
	}
	if storedSecret(test, store, created.Metadata.ID, configuration.APIKeySlot) != expected {
		test.Fatal("KEEP rebased onto another revision or loser changed secrets")
	}
	view, err := base.Edit(test.Context(), "alice", created.Metadata.ID)
	if err != nil || view.Metadata.Name != winner || view.Metadata.Revision != 2 {
		test.Fatalf("winner metadata: %v", err)
	}
}

type afterReadStore struct {
	configuration.Store
	after func()
	reads int
}

func (store *afterReadStore) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	metadata, payload, err := store.Store.ReadConfigurationAssetVersion(ctx, owner, id)
	store.reads++
	store.after()
	return metadata, payload, err
}

func TestReadAndCopyUseOneAtomicVersion(test *testing.T) {
	store, _, _ := fixture(test)
	base := configuration.New(store)
	created, err := base.Create(test.Context(), "alice", draft(), replacement(sentinel))
	if err != nil {
		test.Fatal(err)
	}
	changed := draft()
	changed.Provider.Models.Default = new("revision-two")
	wrapper := &afterReadStore{Store: store, after: func() {
		if _, err := base.Update(test.Context(), "alice", created.Metadata.ID, 1, changed, []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Clear}}); err != nil {
			test.Fatal(err)
		}
	}}
	view, err := configuration.New(wrapper).Edit(test.Context(), "alice", created.Metadata.ID)
	if err != nil || wrapper.reads != 1 || view.Metadata.Revision != 1 || *view.Provider.Models.Default != "claude-test" || !view.Secrets[0].Present {
		test.Fatalf("mixed edit snapshot: %v", err)
	}
	wrapper.reads = 0
	wrapper.after = func() {
		third := draft()
		third.Provider.Models.Default = new("revision-three")
		if _, err := base.Update(test.Context(), "alice", created.Metadata.ID, 2, third, replacement(sentinel+"-third")); err != nil {
			test.Fatal(err)
		}
	}
	copyView, err := configuration.New(wrapper).Copy(test.Context(), "alice", created.Metadata.ID, 2, "Snapshot copy")
	if err != nil || wrapper.reads != 1 || *copyView.Provider.Models.Default != "revision-two" || copyView.Secrets[0].Present || copyView.Readiness != configuration.NeedsCredential {
		test.Fatalf("copy mixed source revisions or leaked key: %v", err)
	}
	source, err := base.Edit(test.Context(), "alice", created.Metadata.ID)
	if err != nil || source.Metadata.Revision != 3 || *source.Provider.Models.Default != "revision-three" {
		test.Fatalf("copy changed source: %v", err)
	}
}

type metadataOnlyStore struct {
	configuration.Store
	reads int
}

func (store *metadataOnlyStore) ReadConfigurationAssetVersion(context.Context, string, string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	store.reads++
	return state.ConfigurationAsset{}, state.ConfigurationPayload{}, errors.New("payload access forbidden")
}

func TestMetadataAndDeleteNeverReadPayload(test *testing.T) {
	store, _, _ := fixture(test)
	metadata, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "future-agent", Kind: state.ConfigurationKindWorkspace, Name: "Future document", SchemaVersion: 65535, Payload: state.NewConfigurationPayload([]byte("deliberately not a Claude envelope"))})
	if err != nil {
		test.Fatal(err)
	}
	wrapper := &metadataOnlyStore{Store: store}
	service := configuration.New(wrapper)
	if _, err := service.List(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	if got, err := service.Get(test.Context(), "alice", metadata.ID); err != nil || got.SchemaVersion != 65535 {
		test.Fatalf("metadata-only get: %v", err)
	}
	if err := service.Delete(test.Context(), "alice", metadata.ID, 1); err != nil {
		test.Fatal(err)
	}
	if wrapper.reads != 0 {
		test.Fatal("metadata operation decrypted payload")
	}
}

func TestStaleCopyChecksRevisionBeforeEnvelope(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	created, err := service.Create(test.Context(), "alice", draft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	_, err = store.UpdateConfigurationAsset(test.Context(), "alice", created.Metadata.ID, 1, state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindProvider, Name: "Newer invalid envelope", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(`{}`))})
	if err != nil {
		test.Fatal(err)
	}
	if view, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 1, "Outdated copy"); !errors.Is(err, configuration.ErrConflict) || view.Metadata.ID != "" {
		test.Fatalf("expected source revision must precede envelope decoding: %v", err)
	}
	listed, err := store.ListConfigurationAssets(test.Context(), "alice")
	if err != nil || len(listed) != 1 {
		test.Fatal("stale copy created asset")
	}
}
