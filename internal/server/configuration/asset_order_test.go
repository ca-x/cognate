package configuration_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestConfigurationOrderService(test *testing.T) {
	store, _, _ := fixture(test)
	service := configuration.New(store)
	first, err := service.Create(test.Context(), "alice", draft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	second, err := service.Copy(test.Context(), "alice", first.Metadata.ID, 1, "real Copy without credentials")
	if err != nil {
		test.Fatal(err)
	}
	before, err := service.GetAssetOrder(test.Context(), "alice", "claude", state.ConfigurationKindProvider)
	if err != nil || before.Revision != 2 || !reflect.DeepEqual(before.Assets, []state.ConfigurationAsset{first.Metadata, second.Metadata}) {
		test.Fatalf("domain Copy did not append ordered membership: %+v %v", before, err)
	}
	request := state.ConfigurationOrderRequest{AgentID: "claude", Kind: state.ConfigurationKindProvider, ExpectedRevision: 2, IDs: []string{second.Metadata.ID, first.Metadata.ID}}
	after, err := service.ReplaceAssetOrder(test.Context(), "alice", request)
	if err != nil || after.Revision != 3 || after.Assets[0] != second.Metadata {
		test.Fatalf("domain reorder failed: %+v %v", after, err)
	}
	if result, err := service.ReplaceAssetOrder(test.Context(), "alice", request); !errors.Is(err, configuration.ErrConflict) || !reflect.DeepEqual(result, state.ConfigurationOrderSnapshot{}) {
		test.Fatalf("domain stale CAS: %+v %v", result, err)
	}
	legacy := configuration.New(struct{ configuration.Store }{store})
	if result, err := legacy.GetAssetOrder(test.Context(), "alice", "claude", state.ConfigurationKindProvider); !errors.Is(err, configuration.ErrUnavailable) || !reflect.DeepEqual(result, state.ConfigurationOrderSnapshot{}) {
		test.Fatalf("legacy Store faked order: %+v %v", result, err)
	}
}

type orderBoundaryStore struct {
	*state.Store
	change func(*state.ConfigurationOrderSnapshot)
}

func (store orderBoundaryStore) ConfigurationAssetOrder(ctx context.Context, owner, agent string, kind state.ConfigurationKind) (state.ConfigurationOrderSnapshot, error) {
	snapshot, err := store.Store.ConfigurationAssetOrder(ctx, owner, agent, kind)
	if err == nil {
		store.change(&snapshot)
	}
	return snapshot, err
}

func (store orderBoundaryStore) ReplaceConfigurationAssetOrder(ctx context.Context, owner string, request state.ConfigurationOrderRequest) (state.ConfigurationOrderSnapshot, error) {
	snapshot, err := store.Store.ReplaceConfigurationAssetOrder(ctx, owner, request)
	if err == nil {
		store.change(&snapshot)
	}
	return snapshot, err
}

func TestConfigurationOrderServiceRejectsUntrustedSnapshot(test *testing.T) {
	for _, mutation := range []struct {
		name   string
		change func(*state.ConfigurationOrderSnapshot)
	}{
		{"wrong-partition", func(snapshot *state.ConfigurationOrderSnapshot) { snapshot.AgentID = "gemini" }},
		{"negative-revision", func(snapshot *state.ConfigurationOrderSnapshot) { snapshot.Revision = -1 }},
		{"duplicate-member", func(snapshot *state.ConfigurationOrderSnapshot) { snapshot.Assets[1] = snapshot.Assets[0] }},
		{"out-of-order-ack", func(snapshot *state.ConfigurationOrderSnapshot) {
			snapshot.Assets[0], snapshot.Assets[1] = snapshot.Assets[1], snapshot.Assets[0]
		}},
	} {
		test.Run(mutation.name, func(test *testing.T) {
			store, _, _ := fixture(test)
			first, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "metadata", SchemaVersion: 65535, Payload: state.NewConfigurationPayload([]byte("readonly stored payload"))})
			if err != nil {
				test.Fatal(err)
			}
			second, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "metadata2", SchemaVersion: 65535, Payload: state.NewConfigurationPayload([]byte("readonly payload2"))})
			if err != nil {
				test.Fatal(err)
			}
			service := configuration.New(orderBoundaryStore{Store: store, change: mutation.change})
			result, err := service.ReplaceAssetOrder(test.Context(), "alice", state.ConfigurationOrderRequest{AgentID: "codex", Kind: state.ConfigurationKindProvider, ExpectedRevision: 2, IDs: []string{second.ID, first.ID}})
			if !errors.Is(err, configuration.ErrUnavailable) || !reflect.DeepEqual(result, state.ConfigurationOrderSnapshot{}) {
				test.Fatalf("untrusted order acknowledgment accepted: %+v %v", result, err)
			}
			durable, err := store.ConfigurationAssetOrder(test.Context(), "alice", "codex", state.ConfigurationKindProvider)
			if err != nil || durable.Revision != 3 || durable.Assets[0].ID != second.ID {
				test.Fatalf("invalid ack is not a rollback claim: %+v %v", durable, err)
			}
		})
	}
}
