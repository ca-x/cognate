package configuration_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type responseStore struct {
	configuration.Store
	err    error
	change func(*state.ConfigurationAsset)
	list   []state.ConfigurationAsset
}

func (store responseStore) CreateConfigurationAsset(ctx context.Context, owner string, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	if store.err != nil {
		return state.ConfigurationAsset{}, store.err
	}
	metadata, err := store.Store.CreateConfigurationAsset(ctx, owner, draft)
	if store.change != nil {
		store.change(&metadata)
	}
	return metadata, err
}

func (store responseStore) UpdateConfigurationAsset(ctx context.Context, owner, id string, revision int64, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	if store.err != nil {
		return state.ConfigurationAsset{}, store.err
	}
	metadata, err := store.Store.UpdateConfigurationAsset(ctx, owner, id, revision, draft)
	if store.change != nil {
		store.change(&metadata)
	}
	return metadata, err
}

func (store responseStore) ConfigurationAsset(ctx context.Context, owner, id string) (state.ConfigurationAsset, error) {
	if store.err != nil {
		return state.ConfigurationAsset{}, store.err
	}
	metadata, err := store.Store.ConfigurationAsset(ctx, owner, id)
	if store.change != nil {
		store.change(&metadata)
	}
	return metadata, err
}

func (store responseStore) ListConfigurationAssets(ctx context.Context, owner string) ([]state.ConfigurationAsset, error) {
	if store.err != nil {
		return nil, store.err
	}
	if store.list != nil {
		return store.list, nil
	}
	metadata, err := store.Store.ListConfigurationAssets(ctx, owner)
	for index := range metadata {
		if store.change != nil {
			store.change(&metadata[index])
		}
	}
	return metadata, err
}

func (store responseStore) DeleteConfigurationAsset(ctx context.Context, owner, id string, revision int64) error {
	if store.err != nil {
		return store.err
	}
	return store.Store.DeleteConfigurationAsset(ctx, owner, id, revision)
}

func TestMetadataStructureFailsClosed(test *testing.T) {
	store, _, _ := fixture(test)
	created, err := configuration.New(store).Create(test.Context(), "alice", draft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	for _, change := range []func(*state.ConfigurationAsset){
		func(entry *state.ConfigurationAsset) { entry.ID = "bad" },
		func(entry *state.ConfigurationAsset) { entry.ID = strings.Repeat("0", 26) },
		func(entry *state.ConfigurationAsset) { entry.Revision = 0 },
		func(entry *state.ConfigurationAsset) { entry.Name = " bad " },
		func(entry *state.ConfigurationAsset) { entry.SchemaVersion = 0 },
		func(entry *state.ConfigurationAsset) { entry.SchemaVersion = 65536 },
		func(entry *state.ConfigurationAsset) { entry.AgentID = "" },
		func(entry *state.ConfigurationAsset) { entry.AgentID = "Bad" },
		func(entry *state.ConfigurationAsset) { entry.AgentID = "bad/agent" },
		func(entry *state.ConfigurationAsset) { entry.Kind = "arbitrary" },
	} {
		service := configuration.New(responseStore{Store: store, change: change})
		if got, err := service.Get(test.Context(), "alice", created.Metadata.ID); !errors.Is(err, configuration.ErrUnavailable) || got.ID != "" {
			test.Fatalf("bad metadata get: %v", err)
		}
		if got, err := service.List(test.Context(), "alice"); !errors.Is(err, configuration.ErrUnavailable) || len(got) != 0 {
			test.Fatalf("bad metadata list: %v", err)
		}
	}
	for _, entries := range [][]state.ConfigurationAsset{{created.Metadata, created.Metadata}, make([]state.ConfigurationAsset, state.MaxConfigurationAssets+1)} {
		if got, err := configuration.New(responseStore{Store: store, list: entries}).List(test.Context(), "alice"); !errors.Is(err, configuration.ErrUnavailable) || len(got) != 0 {
			test.Fatalf("bad metadata list size/duplicate: %v", err)
		}
	}
}

func TestUnexpectedMutationResponseDoesNotClaimSuccess(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(responseStore{Store: store, change: func(entry *state.ConfigurationAsset) { entry.Revision = 999 }})
	if view, err := service.Create(test.Context(), "alice", draft(), replacement(sentinel)); !errors.Is(err, configuration.ErrUnavailable) || view.Metadata.ID != "" {
		test.Fatalf("unexpected create response: %v", err)
	}
	created, err := configuration.New(store).Create(test.Context(), "alice", draft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	if view, err := service.Update(test.Context(), "alice", created.Metadata.ID, 1, draft(), replacement(sentinel)); !errors.Is(err, configuration.ErrUnavailable) || view.Metadata.ID != "" {
		test.Fatalf("unexpected update response: %v", err)
	}
	limited := configuration.New(responseStore{Store: store, err: state.ErrConfigurationLimit})
	if _, err := limited.Create(test.Context(), "alice", draft(), nil); !errors.Is(err, configuration.ErrLimit) {
		test.Fatalf("create limit: %v", err)
	}
	if _, err := limited.Update(test.Context(), "alice", created.Metadata.ID, 2, draft(), nil); !errors.Is(err, configuration.ErrLimit) {
		test.Fatalf("update limit: %v", err)
	}
}

func TestRevisionAndUnknownTypeGuards(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	created, err := service.Create(test.Context(), "alice", draft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	for _, revision := range []int64{0, -1, math.MaxInt64} {
		if _, err := service.Update(test.Context(), "alice", created.Metadata.ID, revision, draft(), nil); !errors.Is(err, configuration.ErrInvalid) {
			test.Fatalf("revision boundary: %v", err)
		}
	}
	if _, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 0, "Copy"); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatalf("copy revision: %v", err)
	}
	if err := service.Delete(test.Context(), "alice", created.Metadata.ID, 0); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatalf("delete revision: %v", err)
	}
	if _, err := service.Copy(test.Context(), "alice", created.Metadata.ID, 1, ""); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatalf("copy name: %v", err)
	}
	for _, entry := range []state.ConfigurationAssetDraft{
		{AgentID: "claude", Kind: state.ConfigurationKindProvider, Name: "Future schema", SchemaVersion: 2, Payload: state.NewConfigurationPayload([]byte(`{}`))},
		{AgentID: "claude", Kind: state.ConfigurationKindMCP, Name: "Other kind", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(`{}`))},
	} {
		metadata, err := store.CreateConfigurationAsset(test.Context(), "alice", entry)
		if err != nil {
			test.Fatal(err)
		}
		if _, err := service.Get(test.Context(), "alice", metadata.ID); err != nil {
			test.Fatalf("valid unknown metadata: %v", err)
		}
		if _, err := service.Edit(test.Context(), "alice", metadata.ID); !errors.Is(err, configuration.ErrUnsupported) {
			test.Fatalf("unknown typed capability: %v", err)
		}
		if err := service.Delete(test.Context(), "alice", metadata.ID, 1); err != nil {
			test.Fatal(err)
		}
	}
	if _, err := configuration.New(nil).Create(test.Context(), "alice", draft(), nil); !errors.Is(err, configuration.ErrUnavailable) {
		test.Fatalf("nil store: %v", err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Create(test.Context(), "alice", draft(), nil); !errors.Is(err, configuration.ErrUnavailable) {
		test.Fatalf("closed store: %v", err)
	}
}
