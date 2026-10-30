package configuration

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func mcpFixture(test *testing.T) (*state.Store, *MCPService, string, []byte) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "mcp.db")
	key := bytes.Repeat([]byte{47}, 32)
	store, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	for _, owner := range []string{"alice", "bob"} {
		if err := store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
	}
	return store, NewMCP(store), path, key
}

func mcpCiphertext(test *testing.T, path, id string) []byte {
	test.Helper()
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal("sqlite open failed")
	}
	defer database.Close()
	var ciphertext []byte
	if err := database.QueryRowContext(test.Context(), "SELECT ciphertext FROM configuration_assets WHERE public_id = ?", id).Scan(&ciphertext); err != nil {
		test.Fatal("ciphertext query failed")
	}
	return ciphertext
}

func TestMCPQueryUnsupportedPreservesAsset(test *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		test.Run(transport, func(test *testing.T) {
			store, service, path, _ := mcpFixture(test)
			created, err := service.Create(test.Context(), "alice", mcpDraft(), mcpReplacements())
			if err != nil {
				test.Fatal("create fixture failed")
			}
			before, err := store.ListConfigurationAssets(test.Context(), "alice")
			if err != nil {
				test.Fatal("read fixture metadata failed")
			}
			ciphertext := mcpCiphertext(test, path, created.Metadata.ID)
			remote := &agentconfig.MCPRemote{URL: "https://mcp.example.invalid/rpc?tenant=synthetic"}
			server := agentconfig.MCPServer{ID: "remote", Name: "remote", Enabled: new(true)}
			if transport == "http" {
				server.HTTP = remote
			} else {
				server.SSE = remote
			}
			draft := MCPDraft{AgentID: "claude", Name: "query", Document: agentconfig.MCPDocumentV1{Servers: []agentconfig.MCPServer{server}}}
			assertUnchanged := func() {
				test.Helper()
				after, err := store.ListConfigurationAssets(test.Context(), "alice")
				if err != nil || !reflect.DeepEqual(before, after) || !bytes.Equal(ciphertext, mcpCiphertext(test, path, created.Metadata.ID)) {
					test.Fatal("unsupported query mutated SQLite metadata, revision or ciphertext")
				}
			}
			view, err := service.Create(test.Context(), "alice", draft, nil)
			if !errors.Is(err, ErrUnsupported) || !reflect.DeepEqual(view, MCPView{}) {
				test.Error("create query must return Unsupported and zero view")
			}
			assertUnchanged()
			view, err = service.Update(test.Context(), "alice", created.Metadata.ID, created.Metadata.Revision, draft, nil)
			if !errors.Is(err, ErrUnsupported) || !reflect.DeepEqual(view, MCPView{}) {
				test.Error("update query must return Unsupported and zero view")
			}
			assertUnchanged()
			for _, assetID := range []string{"", created.Metadata.ID} {
				revision := int64(0)
				if assetID != "" {
					revision = created.Metadata.Revision
				}
				preview, err := service.Preview(test.Context(), "alice", assetID, revision, draft, nil)
				if !errors.Is(err, ErrUnsupported) || !reflect.DeepEqual(preview, MCPPreview{}) {
					test.Error("preview query must return Unsupported and zero preview")
				}
				assertUnchanged()
			}
		})
	}
}

func TestMCPAssetLifecycleAndPreview(test *testing.T) {
	store, service, path, key := mcpFixture(test)
	view, err := service.Create(test.Context(), "alice", mcpDraft(), mcpReplacements())
	if err != nil {
		test.Fatal(err)
	}
	id := view.Metadata.ID
	for _, operation := range []func() error{
		func() error { _, err := service.Get(test.Context(), "bob", id); return err },
		func() error { _, err := service.ReadDraft(test.Context(), "bob", id); return err },
		func() error { _, err := service.Update(test.Context(), "bob", id, 1, mcpDraft(), nil); return err },
		func() error { _, err := service.Copy(test.Context(), "bob", id, 1, "copy"); return err },
		func() error { return service.Delete(test.Context(), "bob", id, 1) },
	} {
		if err := operation(); !errors.Is(err, ErrNotFound) {
			test.Fatal("cross owner boundary bypass")
		}
	}
	draft := mcpDraft()
	*draft.Document.Servers[0].Enabled = false
	changed, err := service.Update(test.Context(), "alice", id, 1, draft, nil)
	if err != nil || changed.Metadata.Revision != 2 || !changed.Secrets[0].Present || *changed.Draft.Document.Servers[0].Enabled {
		test.Fatal("disable failed to preserve slots")
	}
	copied, err := service.Copy(test.Context(), "alice", id, 2, "copy")
	if err != nil || copied.Metadata.ID == id || copied.Readiness != NeedsCredential || copied.Secrets[0].Present || copied.Secrets[1].Present {
		test.Fatal("copy contained source secret")
	}
	preview, err := service.Preview(test.Context(), "alice", id, 2, draft, nil)
	if err != nil || preview.CanApply || preview.Applied || len(preview.Declaration.Operations) != 1 || preview.Declaration.Operations[0].Action != "disable-managed-server" {
		test.Fatal("preview misrepresented logical declaration")
	}
	if _, err := service.Update(test.Context(), "alice", id, 1, draft, nil); !errors.Is(err, ErrConflict) {
		test.Fatal("stale update accepted")
	}
	reopened, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	reread, err := NewMCP(reopened).ReadDraft(test.Context(), "alice", id)
	if err != nil || reread.Metadata.Revision != 2 || !reread.Secrets[0].Present {
		test.Fatal("second store reopen lost asset")
	}
	listed, err := service.List(test.Context(), "alice")
	if err != nil || len(listed) != 2 {
		test.Fatal("MCP list failed")
	}
	if err := service.Delete(test.Context(), "alice", id, 2); err != nil {
		test.Fatal(err)
	}
	if _, err := store.ConfigurationAsset(test.Context(), "alice", id); !errors.Is(err, state.ErrConfigurationNotFound) {
		test.Fatal("delete not durable")
	}
}

func TestMCPAssetRemovedSlotsAtomicCAS(test *testing.T) {
	for _, scenario := range []string{"arg", "env", "header", "transport-http", "transport-sse", "server"} {
		test.Run(scenario, func(test *testing.T) {
			store, service, path, key := mcpFixture(test)
			draft := mcpDraft()
			draft.Document.Servers = append(draft.Document.Servers, agentconfig.MCPServer{ID: "remote", Name: "remote", Enabled: new(true), HTTP: &agentconfig.MCPRemote{URL: "https://example.invalid/mcp", Headers: &agentconfig.MCPNamedSlots{Values: []agentconfig.MCPNamedSlot{{Name: "X-Token", ValueRef: "header/remote/X-Token"}}}}})
			replacements := append(mcpReplacements(), MCPSecretMutation{Slot: "header/remote/X-Token", Action: Replace, Replacement: NewMCPSecret([]byte("synthetic-header"))})
			created, err := service.Create(test.Context(), "alice", draft, replacements)
			if err != nil {
				test.Fatal(err)
			}
			id := created.Metadata.ID
			before := mcpCiphertext(test, path, id)
			removed := []string{}
			switch scenario {
			case "arg":
				draft.Document.Servers[0].Stdio.Args = nil
				removed = []string{"arg/local/first"}
			case "env":
				draft.Document.Servers[0].Stdio.Env = nil
				removed = []string{"env/local/TOKEN"}
			case "header":
				draft.Document.Servers[1].HTTP.Headers = nil
				removed = []string{"header/remote/X-Token"}
			case "transport-http":
				draft.Document.Servers[0].Stdio = nil
				draft.Document.Servers[0].HTTP = &agentconfig.MCPRemote{URL: "https://example.invalid"}
				removed = []string{"arg/local/first", "env/local/TOKEN"}
			case "transport-sse":
				draft.Document.Servers[0].Stdio = nil
				draft.Document.Servers[0].SSE = &agentconfig.MCPRemote{URL: "http://localhost/sse"}
				removed = []string{"arg/local/first", "env/local/TOKEN"}
			case "server":
				draft.Document.Servers = draft.Document.Servers[1:]
				removed = []string{"arg/local/first", "env/local/TOKEN"}
			}
			for _, bad := range [][]MCPSecretMutation{nil, {{Slot: removed[0], Action: Clear}}, {{Slot: removed[0], Action: Keep}}, {{Slot: removed[0], Action: Replace, Replacement: NewMCPSecret([]byte("bad"))}}, {{Slot: "arg/foreign/first", Action: Clear, Confirmed: true}}} {
				zero, err := service.Update(test.Context(), "alice", id, 1, draft, bad)
				if !errors.Is(err, ErrInvalid) || zero.Metadata.ID != "" {
					test.Fatal("removed refs accepted without explicit clear")
				}
				current, err := store.ConfigurationAsset(test.Context(), "alice", id)
				if err != nil || current != created.Metadata || !bytes.Equal(before, mcpCiphertext(test, path, id)) {
					test.Fatal("rejected update mutated durable state")
				}
			}
			clears := make([]MCPSecretMutation, 0, len(removed))
			for _, ref := range removed {
				clears = append(clears, MCPSecretMutation{Slot: ref, Action: Clear, Confirmed: true})
			}
			updated, err := service.Update(test.Context(), "alice", id, 1, draft, clears)
			if err != nil || updated.Metadata.Revision != 2 {
				test.Fatalf("confirmed union transition rejected: %v", err)
			}
			reopened, err := state.Open(test.Context(), path, key)
			if err != nil {
				test.Fatal(err)
			}
			defer reopened.Close()
			_, payload, err := reopened.ReadConfigurationAssetVersion(test.Context(), "alice", id)
			if err != nil {
				test.Fatal(err)
			}
			envelope, err := decodeMCPEnvelope(payload)
			if err != nil {
				test.Fatal(err)
			}
			defer envelope.Close()
			for _, ref := range removed {
				if _, exists := envelope.data().slots[ref]; exists {
					test.Fatal("removed secret remained stored")
				}
			}
			for _, status := range updated.Secrets {
				if !status.Present {
					test.Fatal("unrelated slot lost")
				}
			}
		})
	}
}

func TestMCPAssetCASConcurrent(test *testing.T) {
	_, service, _, _ := mcpFixture(test)
	view, err := service.Create(test.Context(), "alice", mcpDraft(), mcpReplacements())
	if err != nil {
		test.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		workers.Go(func() {
			_, err := service.Update(test.Context(), "alice", view.Metadata.ID, 1, mcpDraft(), nil)
			results <- err
		})
	}
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			test.Fatal("unexpected concurrent result")
		}
	}
	if successes != 1 || conflicts != 1 {
		test.Fatal("CAS did not serialize competing updates")
	}
}

type mcpFailureStore struct {
	Store
	creates     int
	updates     int
	fault       error
	afterCreate bool
}

func (store *mcpFailureStore) CreateConfigurationAsset(ctx context.Context, owner string, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	store.creates++
	if store.afterCreate {
		_, _ = store.Store.CreateConfigurationAsset(ctx, owner, draft)
	}
	return state.ConfigurationAsset{}, store.fault
}
func (store *mcpFailureStore) UpdateConfigurationAsset(ctx context.Context, owner, id string, revision int64, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	store.updates++
	return state.ConfigurationAsset{}, store.fault
}

func TestMCPAssetUnknownNoReplay(test *testing.T) {
	store, _, _, _ := mcpFixture(test)
	wrapper := &mcpFailureStore{Store: store, fault: errors.New("private store diagnostic must not escape"), afterCreate: true}
	service := NewMCP(wrapper)
	view, err := service.Create(test.Context(), "alice", mcpDraft(), mcpReplacements())
	if !errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(view, MCPView{}) || wrapper.creates != 1 {
		test.Fatal("unknown create repeated or leaked result")
	}
	listed, err := service.List(test.Context(), "alice")
	if err != nil || len(listed) != 1 {
		test.Fatal("committed unknown cannot be inspected")
	}
	_, err = service.Update(test.Context(), "alice", listed[0].ID, 1, mcpDraft(), nil)
	if !errors.Is(err, ErrUnavailable) || wrapper.updates != 1 {
		test.Fatal("unknown update replayed")
	}
}

func TestMCPAssetUnknownEnvelopeReadOnly(test *testing.T) {
	store, service, path, _ := mcpFixture(test)
	created, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindMCP, Name: "future", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(`{"version":1,"document":{"servers":[]},"slots":{},"future":false}`))})
	if err != nil {
		test.Fatal(err)
	}
	before := mcpCiphertext(test, path, created.ID)
	view, err := service.ReadDraft(test.Context(), "alice", created.ID)
	if err != nil || !view.HasPreservedExtras {
		test.Fatal("future extras not read-only")
	}
	if _, err := service.Copy(test.Context(), "alice", created.ID, 1, "copy"); !errors.Is(err, ErrUnsupported) {
		test.Fatal("copy stripped future extras")
	}
	if _, err := service.Update(test.Context(), "alice", created.ID, 1, mcpDraft(), nil); !errors.Is(err, ErrUnsupported) {
		test.Fatal("edit stripped future extras")
	}
	if !bytes.Equal(before, mcpCiphertext(test, path, created.ID)) {
		test.Fatal("unknown envelope overwritten")
	}
	if err := service.Delete(test.Context(), "alice", created.ID, 1); err != nil {
		test.Fatal("owner cannot explicitly delete unknown")
	}
}
func mcpDraft() MCPDraft {
	return MCPDraft{AgentID: "claude", Name: "MCP synthetic", Document: agentconfig.MCPDocumentV1{Servers: []agentconfig.MCPServer{{ID: "local", Name: "local", Enabled: new(true), Stdio: &agentconfig.MCPStdio{Command: "node", Args: &agentconfig.MCPArgs{Values: []agentconfig.MCPArg{{ID: "first", ValueRef: "arg/local/first"}}}, Env: &agentconfig.MCPNamedSlots{Values: []agentconfig.MCPNamedSlot{{Name: "TOKEN", ValueRef: "env/local/TOKEN"}}}}}}}}
}
func mcpReplacements() []MCPSecretMutation {
	return []MCPSecretMutation{{Slot: "arg/local/first", Action: Replace, Replacement: NewMCPSecret([]byte("MCP-PRIVATE-CANARY-638124"))}, {Slot: "env/local/TOKEN", Action: Replace, Replacement: NewMCPSecret([]byte{})}}
}

func TestMCPAssetCreateRead(test *testing.T) {
	_, service, _, _ := mcpFixture(test)
	view, err := service.Create(test.Context(), "alice", mcpDraft(), mcpReplacements())
	if err != nil {
		test.Fatalf("real MCP create failed: %v", err)
	}
	if view.Metadata.Kind != state.ConfigurationKindMCP || view.Metadata.Revision != 1 || view.Readiness != CredentialComplete || len(view.Secrets) != 2 || !view.Secrets[1].Present {
		test.Fatal("metadata or empty-present lost")
	}
	loaded, err := service.ReadDraft(test.Context(), "alice", view.Metadata.ID)
	if err != nil || loaded.Metadata != view.Metadata || !loaded.Secrets[0].Present {
		test.Fatal("read draft failed")
	}
}
