package state_test

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
)

func packConfigurationFixture(test *testing.T) (*state.Store, state.Pack) {
	test.Helper()
	store, entry, _, _ := packConfigurationPersistentFixture(test)
	return store, entry
}

func packConfigurationPersistentFixture(test *testing.T) (*state.Store, state.Pack, string, []byte) {
	test.Helper()
	store, path, key := configurationFixture(test)
	reference, err := repository.ParseReference("org/attachments", "")
	if err != nil {
		test.Fatal(err)
	}
	collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("c", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Synthetic skill", Description: "Synthetic attachment fixture", Path: "skills/example"}, ContentHash: strings.Repeat("a", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	detail, err := store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	entry, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Attachment test", Items: []state.PackSelection{{SkillID: detail.Skills[0].ID}}})
	if err != nil {
		test.Fatal(err)
	}
	return store, entry, path, key
}

func packConfigurationAsset(test *testing.T, store *state.Store, owner, agent string) state.ConfigurationAsset {
	test.Helper()
	draft := configurationDraft()
	draft.AgentID = agent
	asset, err := store.CreateConfigurationAsset(test.Context(), owner, draft)
	if err != nil {
		test.Fatal(err)
	}
	return asset
}

func packConfigurationRef(asset state.ConfigurationAsset) state.PackConfigurationReference {
	return state.PackConfigurationReference{AssetID: asset.ID, AssetRevision: asset.Revision, TargetID: asset.AgentID + ".provider.user"}
}

func TestPackConfigurationLifecycleReopenLegacyUpdateAndStaleProtection(test *testing.T) {
	store, entry, path, key := packConfigurationPersistentFixture(test)
	claude := packConfigurationAsset(test, store, "alice", "claude")
	codex := packConfigurationAsset(test, store, "alice", "codex")
	refs := []state.PackConfigurationReference{packConfigurationRef(claude), packConfigurationRef(codex)}
	first, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 1, 1, refs)
	if err != nil || first.PackRevision != 2 || first.ConfigurationRevision != 2 || len(first.Entries) != 2 {
		test.Fatalf("atomic replacement failed: %+v %v", first, err)
	}
	for ordinal, item := range first.Entries {
		if len(item.ID) != 26 || item.PackConfigurationReference != refs[ordinal] || item.Ordinal != ordinal || item.State != state.PackConfigurationCurrent || item.NativeAvailability != state.PackConfigurationNativeUnavailable {
			test.Fatal("invalid reference metadata or invented native availability")
		}
	}
	reordered, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 2, 2, []state.PackConfigurationReference{refs[1], refs[0]})
	if err != nil || reordered.Entries[0].ID != first.Entries[1].ID || reordered.Entries[1].ID != first.Entries[0].ID || reordered.Digest == first.Digest {
		test.Fatalf("reorder lost stable IDs or digest binding: %v", err)
	}
	updated, err := store.UpdatePack(test.Context(), "alice", entry.ID, 3, state.PackDraft{Name: "Skills changed", Items: []state.PackSelection{{SkillID: entry.Items[0].SkillID, Slug: "renamed"}}})
	if err != nil || updated.Revision != 4 {
		test.Fatalf("legacy skill update failed: %v", err)
	}
	afterSkills, err := store.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if err != nil || afterSkills.ConfigurationRevision != 3 || afterSkills.PackRevision != 4 || !reflect.DeepEqual(afterSkills.Entries, reordered.Entries) || afterSkills.Digest == reordered.Digest {
		test.Fatalf("legacy UpdatePack dropped or retargeted attachments: %v", err)
	}
	draft := configurationDraft()
	draft.SchemaVersion = 2
	changed, err := store.UpdateConfigurationAsset(test.Context(), "alice", codex.ID, 1, draft)
	if err != nil || changed.Revision != 2 {
		test.Fatal(err)
	}
	stale, err := store.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if err != nil || stale.Entries[0].AssetRevision != 1 || stale.Entries[0].State != state.PackConfigurationStaleAsset || stale.Entries[1].State != state.PackConfigurationCurrent {
		test.Fatalf("asset update followed latest or hid stale pin: %v", err)
	}
	if err := store.DeleteConfigurationAsset(test.Context(), "alice", codex.ID, 2); !errors.Is(err, state.ErrConfigurationInUse) {
		test.Fatalf("stale reference failed delete protection: %v", err)
	}
	encoded, err := json.Marshal(stale)
	if err != nil {
		test.Fatal(err)
	}
	for _, output := range []string{string(encoded), fmt.Sprintf("%v %+v %#v", stale, stale, stale)} {
		for _, private := range []string{configurationSentinel, codex.Name, "ciphertext", "Payload"} {
			if strings.Contains(output, private) {
				test.Fatal("snapshot exposed private payload/name")
			}
		}
	}
	legacy, err := json.Marshal(updated)
	if err != nil || strings.Contains(string(legacy), codex.ID) || strings.Contains(string(legacy), first.Entries[0].ID) || strings.Contains(string(legacy), "Configuration") {
		test.Fatal("legacy Pack metadata acquired attachment fields")
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = reopened.Close() })
	persisted, err := reopened.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if err != nil || !reflect.DeepEqual(persisted, stale) {
		test.Fatalf("reopen changed exact metadata: %v", err)
	}
	if err := reopened.DeletePack(test.Context(), "alice", entry.ID, 4); err != nil {
		test.Fatalf("Pack deletion failed attachment cleanup: %v", err)
	}
	if _, err := reopened.ConfigurationAsset(test.Context(), "alice", claude.ID); err != nil {
		test.Fatal("Pack deletion cascaded configuration asset")
	}
	if err := reopened.DeleteConfigurationAsset(test.Context(), "alice", codex.ID, 2); err != nil {
		test.Fatalf("Pack deletion left references: %v", err)
	}
}

func TestPackConfigurationBoundsCASOwnerAndTargets(test *testing.T) {
	store, entry := packConfigurationFixture(test)
	asset := packConfigurationAsset(test, store, "alice", "claude")
	foreign := packConfigurationAsset(test, store, "bob", "claude")
	unknown := packConfigurationAsset(test, store, "alice", "future-agent")
	refs := []state.PackConfigurationReference{packConfigurationRef(asset)}
	initial, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 1, 1, refs)
	if err != nil {
		test.Fatal(err)
	}
	for _, scenario := range []struct {
		name                  string
		owner                 string
		packRevision          int64
		configurationRevision int64
		refs                  []state.PackConfigurationReference
		expected              error
	}{
		{"foreign-pack", "bob", 2, 2, refs, state.ErrPackNotFound},
		{"unknown-owner", "nobody", 2, 2, refs, state.ErrPackNotFound},
		{"stale-pack", "alice", 1, 2, refs, state.ErrPackConflict},
		{"stale-configuration", "alice", 2, 1, refs, state.ErrPackConflict},
		{"overflow-pack", "alice", math.MaxInt64, 2, refs, state.ErrPackConflict},
		{"overflow-configuration", "alice", 2, math.MaxInt64, refs, state.ErrPackConflict},
		{"zero-configuration", "alice", 2, 0, refs, state.ErrPackConflict},
		{"foreign-asset", "alice", 2, 2, []state.PackConfigurationReference{packConfigurationRef(foreign)}, state.ErrConfigurationNotFound},
		{"missing-asset", "alice", 2, 2, []state.PackConfigurationReference{{AssetID: strings.Repeat("A", 26), AssetRevision: 1, TargetID: "claude.provider.user"}}, state.ErrConfigurationNotFound},
		{"stale-asset", "alice", 2, 2, []state.PackConfigurationReference{{AssetID: asset.ID, AssetRevision: 2, TargetID: "claude.provider.user"}}, state.ErrConfigurationConflict},
		{"zero-asset", "alice", 2, 2, []state.PackConfigurationReference{{AssetID: asset.ID, AssetRevision: 0, TargetID: "claude.provider.user"}}, state.ErrPackInvalid},
		{"wrong-agent-target", "alice", 2, 2, []state.PackConfigurationReference{{AssetID: asset.ID, AssetRevision: 1, TargetID: "codex.provider.user"}}, state.ErrPackConfigurationUnsupported},
		{"path-target", "alice", 2, 2, []state.PackConfigurationReference{{AssetID: asset.ID, AssetRevision: 1, TargetID: "../../settings"}}, state.ErrPackConfigurationUnsupported},
		{"unknown-agent", "alice", 2, 2, []state.PackConfigurationReference{packConfigurationRef(unknown)}, state.ErrPackConfigurationUnsupported},
		{"duplicate", "alice", 2, 2, []state.PackConfigurationReference{refs[0], refs[0]}, state.ErrPackInvalid},
		{"duplicate-different-pin", "alice", 2, 2, []state.PackConfigurationReference{refs[0], {AssetID: asset.ID, AssetRevision: 2, TargetID: refs[0].TargetID}}, state.ErrPackInvalid},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			result, err := store.ReplacePackConfigurationAttachments(test.Context(), scenario.owner, entry.ID, scenario.packRevision, scenario.configurationRevision, scenario.refs)
			if !errors.Is(err, scenario.expected) || !reflect.DeepEqual(result, state.PackConfigurationSnapshot{}) {
				test.Fatalf("unsafe replacement result or wrong classification: %v", err)
			}
			current, err := store.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
			if err != nil || !reflect.DeepEqual(current, initial) {
				test.Fatalf("failed group changed snapshot/revisions: %v", err)
			}
		})
	}
	for _, owner := range []string{"bob", "nobody"} {
		result, err := store.PackConfigurationAttachments(test.Context(), owner, entry.ID)
		if !errors.Is(err, state.ErrPackNotFound) || !reflect.DeepEqual(result, state.PackConfigurationSnapshot{}) {
			test.Fatalf("foreign read returned attachment presence: %v", err)
		}
	}
	ctx := state.WithMCPAuthorization(test.Context(), state.MCPGrant{}, state.MCPPacksWrite)
	if result, err := store.PackConfigurationAttachments(ctx, "alice", entry.ID); !errors.Is(err, state.ErrMCPForbidden) || !reflect.DeepEqual(result, state.PackConfigurationSnapshot{}) {
		test.Fatalf("MCP attachment read not denied: %v", err)
	}
	if result, err := store.ReplacePackConfigurationAttachments(ctx, "alice", entry.ID, 2, 2, nil); !errors.Is(err, state.ErrMCPForbidden) || !reflect.DeepEqual(result, state.PackConfigurationSnapshot{}) {
		test.Fatalf("MCP attachment write not denied: %v", err)
	}
	all := make([]state.PackConfigurationReference, 0, 17)
	for range 17 {
		all = append(all, packConfigurationRef(packConfigurationAsset(test, store, "alice", "claude")))
	}
	if _, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 2, 2, all); !errors.Is(err, state.ErrPackLimit) {
		test.Fatalf("17 attachments accepted: %v", err)
	}
	full, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 2, 2, all[:16])
	if err != nil || len(full.Entries) != 16 {
		test.Fatalf("16 valid attachments refused: %v", err)
	}
	empty, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 3, 3, nil)
	if err != nil || len(empty.Entries) != 0 || empty.PackRevision != 4 || empty.ConfigurationRevision != 4 {
		test.Fatalf("explicit empty replacement failed: %v", err)
	}
}

func TestPackConfigurationEmptySnapshot(test *testing.T) {
	store, entry := packConfigurationFixture(test)
	snapshot, err := store.PackConfigurationAttachments(test.Context(), "alice", entry.ID)
	if err != nil || snapshot.PackID != entry.ID || snapshot.PackRevision != 1 || snapshot.ConfigurationRevision != 1 || len(snapshot.Entries) != 0 || snapshot.Digest == ([32]byte{}) {
		test.Fatalf("empty owner snapshot unavailable or invalid: %+v %v", snapshot, err)
	}
}

func TestPackConfigurationFiniteTargetsAreNotNativeCapabilities(test *testing.T) {
	store, entry := packConfigurationFixture(test)
	var refs []state.PackConfigurationReference
	for _, agent := range []string{"claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"} {
		refs = append(refs, packConfigurationRef(packConfigurationAsset(test, store, "alice", agent)))
	}
	snapshot, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 1, 1, refs)
	if err != nil || len(snapshot.Entries) != 14 {
		test.Fatalf("finite fourteen-target registration incomplete: %v", err)
	}
	for ordinal, item := range snapshot.Entries {
		if item.PackConfigurationReference != refs[ordinal] || item.NativeAvailability != state.PackConfigurationNativeUnavailable || item.State != state.PackConfigurationCurrent {
			test.Fatal("target registration invented native capability or reordered metadata")
		}
	}
	for _, scenario := range []struct {
		kind   state.ConfigurationKind
		schema int
	}{
		{state.ConfigurationKindProvider, 2},
		{state.ConfigurationKindMCP, 1},
		{state.ConfigurationKindPrompt, 1},
		{state.ConfigurationKindAgentSettings, 1},
		{state.ConfigurationKindWorkspace, 1},
	} {
		draft := configurationDraft()
		draft.Kind, draft.SchemaVersion = scenario.kind, scenario.schema
		asset, err := store.CreateConfigurationAsset(test.Context(), "alice", draft)
		if err != nil {
			test.Fatal(err)
		}
		if result, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", entry.ID, 2, 2, []state.PackConfigurationReference{packConfigurationRef(asset)}); !errors.Is(err, state.ErrPackConfigurationUnsupported) || !reflect.DeepEqual(result, state.PackConfigurationSnapshot{}) {
			test.Fatalf("unregistered schema/kind accepted: %v", err)
		}
	}
}

func TestPackConfigurationMultiplePacksAndExplicitRebind(test *testing.T) {
	store, first := packConfigurationFixture(test)
	second, err := store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Second pack", Items: []state.PackSelection{{SkillID: first.Items[0].SkillID}}})
	if err != nil {
		test.Fatal(err)
	}
	asset := packConfigurationAsset(test, store, "alice", "claude")
	firstSnapshot, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", first.ID, 1, 1, []state.PackConfigurationReference{packConfigurationRef(asset)})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", second.ID, 1, 1, []state.PackConfigurationReference{packConfigurationRef(asset)}); err != nil {
		test.Fatal(err)
	}
	draft := configurationDraft()
	draft.AgentID = "claude"
	updated, err := store.UpdateConfigurationAsset(test.Context(), "alice", asset.ID, 1, draft)
	if err != nil {
		test.Fatal(err)
	}
	rebound, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", first.ID, 2, 2, []state.PackConfigurationReference{packConfigurationRef(updated)})
	if err != nil || rebound.Entries[0].ID != firstSnapshot.Entries[0].ID || rebound.Entries[0].AssetRevision != 2 || rebound.Entries[0].State != state.PackConfigurationCurrent {
		test.Fatalf("explicit rebind lost reference identity or stayed stale: %v", err)
	}
	if _, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", first.ID, 3, 3, nil); err != nil {
		test.Fatal(err)
	}
	if err := store.DeleteConfigurationAsset(test.Context(), "alice", asset.ID, 2); !errors.Is(err, state.ErrConfigurationInUse) {
		test.Fatalf("detaching one Pack lost other Pack's stale protection: %v", err)
	}
	if _, err := store.ReplacePackConfigurationAttachments(test.Context(), "alice", second.ID, 2, 2, nil); err != nil {
		test.Fatal(err)
	}
	if err := store.DeleteConfigurationAsset(test.Context(), "alice", asset.ID, 2); err != nil {
		test.Fatalf("fully unreferenced asset remained undeletable: %v", err)
	}
}
