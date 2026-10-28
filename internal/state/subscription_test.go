package state_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/ca-x/cognate/internal/state"
)

func openSubscriptionStore(test *testing.T) *state.Store {
	test.Helper()
	store, err := state.Open(test.Context(), filepath.Join(test.TempDir(), "client.db"))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	return store
}

func createSubscription(test *testing.T, store *state.Store, pack string) state.Subscription {
	test.Helper()
	result, err := store.CreateSubscription(test.Context(), state.SubscriptionDraft{ID: uuid.New().String(), Server: "https://hub.example", PackID: pack, Name: "Example", SealedCredential: bytes.Repeat([]byte{42}, 64)})
	if err != nil {
		test.Fatal(err)
	}
	return result
}

func subscriptionSnapshot(record state.Subscription, hash string) state.SubscriptionSnapshot {
	return state.SubscriptionSnapshot{ID: record.ID, ExpectedVersion: record.Version, Revision: 1, ManifestHash: strings.Repeat("b", 64), Name: record.Name, Skills: []state.SubscriptionSkill{{Slug: "demo", DesiredHash: hash}}}
}

func TestSubscriptionStageDeduplicatesMembershipAndIsAtomic(test *testing.T) {
	store := openSubscriptionStore(test)
	first := createSubscription(test, store, uuid.New().String())
	second := createSubscription(test, store, uuid.New().String())
	hash := strings.Repeat("a", 64)
	updates := []state.SubscriptionSnapshot{subscriptionSnapshot(first, hash), subscriptionSnapshot(second, hash)}
	if err := store.StageSubscriptionSync(test.Context(), updates); err != nil {
		test.Fatal(err)
	}
	records, err := store.Subscriptions(test.Context())
	if err != nil || len(records) != 2 {
		test.Fatalf("expected two memberships, got count=%d error=%v", len(records), err)
	}
	for _, record := range records {
		if record.Version != 2 || record.AppliedRevision != 0 || record.DesiredRevision != 1 || len(record.Skills) != 1 || record.Skills[0].DesiredHash != hash || record.Skills[0].AppliedHash != "" {
			test.Fatalf("unexpected pending metadata: %+v", record)
		}
	}
	updates[0].ExpectedVersion++
	updates[1].ExpectedVersion++
	updates[1].Skills[0].DesiredHash = strings.Repeat("c", 64)
	if err := store.StageSubscriptionSync(test.Context(), updates); !errors.Is(err, state.ErrSubscriptionConflict) {
		test.Fatalf("conflicting hashes did not reject atomically: %v", err)
	}
	after, err := store.Subscriptions(test.Context())
	if err != nil || after[0].Version != 2 || after[1].Version != 2 {
		test.Fatalf("failed stage changed versions: %v", err)
	}
	if err := store.StageSubscriptionSync(test.Context(), updates[:1]); !errors.Is(err, state.ErrSubscriptionConflict) {
		test.Fatalf("partial snapshot accepted: %v", err)
	}
}

func TestSubscriptionAppliedStateRequiresInstalledContentAndCAS(test *testing.T) {
	store := openSubscriptionStore(test)
	record := createSubscription(test, store, uuid.New().String())
	hash := strings.Repeat("a", 64)
	if err := store.StageSubscriptionSync(test.Context(), []state.SubscriptionSnapshot{subscriptionSnapshot(record, hash)}); err != nil {
		test.Fatal(err)
	}
	versions := map[string]int64{record.ID: 2}
	if err := store.CompleteSubscriptionSync(test.Context(), versions); err == nil {
		test.Fatal("missing installation advanced applied revision")
	}
	if err := store.Create(test.Context(), state.Installation{Slug: "demo", ContentHash: hash}); err != nil {
		test.Fatal(err)
	}
	if err := store.CompleteSubscriptionSync(test.Context(), versions); err != nil {
		test.Fatal(err)
	}
	records, err := store.Subscriptions(test.Context())
	if err != nil || len(records) != 1 || records[0].AppliedRevision != 1 || records[0].Version != 3 || records[0].Skills[0].AppliedHash != hash {
		test.Fatalf("successful content not recorded: %v", err)
	}
	if err := store.CompleteSubscriptionSync(test.Context(), versions); !errors.Is(err, state.ErrSubscriptionConflict) {
		test.Fatalf("stale completion accepted: %v", err)
	}
}

func TestSubscriptionRemovalKeepsUnmanagedInstallation(test *testing.T) {
	store := openSubscriptionStore(test)
	record := createSubscription(test, store, uuid.New().String())
	hash := strings.Repeat("a", 64)
	if err := store.StageSubscriptionSync(test.Context(), []state.SubscriptionSnapshot{subscriptionSnapshot(record, hash)}); err != nil {
		test.Fatal(err)
	}
	if err := store.Create(test.Context(), state.Installation{Slug: "demo", ContentHash: hash}); err != nil {
		test.Fatal(err)
	}
	if err := store.CompleteSubscriptionSync(test.Context(), map[string]int64{record.ID: 2}); err != nil {
		test.Fatal(err)
	}
	if err := store.DisableSubscription(test.Context(), record.ID, 3); err != nil {
		test.Fatal(err)
	}
	if err := store.StageSubscriptionSync(test.Context(), []state.SubscriptionSnapshot{{ID: record.ID, ExpectedVersion: 4}}); err != nil {
		test.Fatal(err)
	}
	if err := store.CompleteSubscriptionSync(test.Context(), map[string]int64{record.ID: 5}); err != nil {
		test.Fatal(err)
	}
	records, err := store.Subscriptions(test.Context())
	if err != nil || len(records) != 0 {
		test.Fatalf("removed subscription remains: %v", err)
	}
	installed, err := store.Get(test.Context(), "demo")
	if err != nil || installed.SubscriptionManaged {
		test.Fatalf("unmanaged matching skill was changed: %v", err)
	}
}

func TestSubscriptionCredentialIsNotIncludedInPublicMetadata(test *testing.T) {
	store := openSubscriptionStore(test)
	record := createSubscription(test, store, uuid.New().String())
	if _, err := store.CreateSubscription(test.Context(), state.SubscriptionDraft{ID: uuid.New().String(), Server: record.Server, PackID: record.PackID, Name: "Duplicate", SealedCredential: bytes.Repeat([]byte{43}, 64)}); !errors.Is(err, state.ErrSubscriptionConflict) {
		test.Fatalf("duplicate pack subscription accepted: %v", err)
	}
	called := false
	if err := store.WithSubscriptionCredential(test.Context(), record.ID, func(data []byte) error {
		called = true
		if !bytes.Equal(data, bytes.Repeat([]byte{42}, 64)) {
			test.Fatal("ciphertext changed")
		}
		return nil
	}); err != nil || !called {
		test.Fatalf("credential callback failed: %v", err)
	}
	if err := store.RotateSubscriptionCredential(test.Context(), record.ID, 1, bytes.Repeat([]byte{44}, 64)); err != nil {
		test.Fatal(err)
	}
	if err := store.DisableSubscription(test.Context(), record.ID, 1); !errors.Is(err, state.ErrSubscriptionConflict) {
		test.Fatalf("stale mutation accepted after rotation: %v", err)
	}
}
