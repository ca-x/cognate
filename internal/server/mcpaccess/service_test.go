package mcpaccess_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/state"
)

func setup(test *testing.T) (*state.Store, *mcpaccess.Service, string) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "server.db")
	store, err := state.Open(test.Context(), path, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	for _, owner := range []string{"alice", "bob"} {
		if err := store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
	}
	service, err := mcpaccess.NewService(store)
	if err != nil {
		test.Fatal(err)
	}
	return store, service, path
}

func metadata() state.MCPMetadata {
	return state.MCPMetadata{Name: "Editor", Scopes: []string{state.MCPCatalogRead}, ExpiresAt: time.Now().Add(time.Hour)}
}

func TestCredentialMetadataValidationAndOwnerIsolation(test *testing.T) {
	_, service, _ := setup(test)
	for _, scopes := range [][]string{nil, {}, {""}, {"*"}, {"publish:write"}, {"categories:write"}, {state.MCPCatalogRead, state.MCPCatalogRead}, {state.MCPCatalogRead, "catalog:write "}} {
		input := metadata()
		input.Scopes = scopes
		if _, secret, err := service.Create(test.Context(), "alice", input); !errors.Is(err, state.ErrMCPInvalid) || secret.Reveal() != "" {
			test.Fatalf("invalid scope accepted: %v", scopes)
		}
	}
	for _, expiry := range []time.Time{{}, time.Now().Add(-time.Second), time.Now().Add(state.MaxMCPValidity + time.Hour)} {
		input := metadata()
		input.ExpiresAt = expiry
		if _, _, err := service.Create(test.Context(), "alice", input); !errors.Is(err, state.ErrMCPInvalid) {
			test.Fatal("invalid expiry accepted")
		}
	}
	entry, secret, err := service.Create(test.Context(), "alice", metadata())
	if err != nil {
		test.Fatal(err)
	}
	if _, err := service.Get(test.Context(), "bob", entry.ID); !errors.Is(err, state.ErrMCPNotFound) {
		test.Fatal("cross-owner metadata returned")
	}
	if entries, err := service.List(test.Context(), "bob"); err != nil || len(entries) != 0 {
		test.Fatal("cross-owner credential list returned")
	}
	if _, err := service.Update(test.Context(), "bob", entry.ID, 1, metadata()); !errors.Is(err, state.ErrMCPNotFound) {
		test.Fatal("cross-owner metadata updated")
	}
	if _, _, err := service.Rotate(test.Context(), "bob", entry.ID, 1); !errors.Is(err, state.ErrMCPNotFound) {
		test.Fatal("cross-owner token rotated")
	}
	if _, err := service.Revoke(test.Context(), "bob", entry.ID, 1); !errors.Is(err, state.ErrMCPNotFound) {
		test.Fatal("cross-owner token revoked")
	}
	if _, err := service.ListAudit(test.Context(), "bob", entry.ID); !errors.Is(err, state.ErrMCPNotFound) {
		test.Fatal("cross-owner audit returned")
	}
	if _, err := service.Authorize(test.Context(), secret.Reveal(), "publish:write"); !errors.Is(err, state.ErrMCPForbidden) {
		test.Fatal("unknown scope authorized")
	}
}

func TestCredentialRotationCASAndImmediateScopeReduction(test *testing.T) {
	_, service, _ := setup(test)
	entry, original, err := service.Create(test.Context(), "alice", metadata())
	if err != nil {
		test.Fatal(err)
	}
	input := metadata()
	input.Name, input.Scopes = "Renamed", []string{state.MCPPacksRead}
	updated, err := service.Update(test.Context(), "alice", entry.ID, entry.Revision, input)
	if err != nil || updated.Revision != 2 {
		test.Fatalf("update failed: %v", err)
	}
	if _, err := service.Authorize(test.Context(), original.Reveal(), state.MCPCatalogRead); !errors.Is(err, state.ErrMCPForbidden) {
		test.Fatal("removed scope retained authority")
	}
	type outcome struct {
		entry  state.MCPCredential
		secret mcpaccess.Secret
		err    error
	}
	results := make(chan outcome, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			entry, secret, err := service.Rotate(test.Context(), "alice", entry.ID, updated.Revision)
			results <- outcome{entry: entry, secret: secret, err: err}
		})
	}
	workers.Wait()
	close(results)
	successes := 0
	var winner outcome
	for result := range results {
		if result.err == nil {
			successes++
			winner = result
		} else if !errors.Is(result.err, state.ErrMCPConflict) {
			test.Fatalf("rotation race: %v", result.err)
		}
	}
	if successes != 1 || winner.entry.Revision != 3 || winner.secret.Reveal() == original.Reveal() {
		test.Fatal("rotation was not atomic compare-and-swap")
	}
	if _, err := service.Authorize(test.Context(), original.Reveal(), ""); !errors.Is(err, state.ErrMCPUnauthorized) {
		test.Fatal("rotated token still valid")
	}
	if _, err := service.Authorize(test.Context(), winner.secret.Reveal(), state.MCPPacksRead); err != nil {
		test.Fatal(err)
	}
	revoked, err := service.Revoke(test.Context(), "alice", entry.ID, winner.entry.Revision)
	if err != nil || revoked.RevokedAt == nil {
		test.Fatalf("revoke: %v", err)
	}
	if _, err := service.Authorize(test.Context(), winner.secret.Reveal(), ""); !errors.Is(err, state.ErrMCPUnauthorized) {
		test.Fatal("revoked token still valid")
	}
	if _, _, err := service.Rotate(test.Context(), "alice", entry.ID, revoked.Revision); !errors.Is(err, state.ErrMCPConflict) {
		test.Fatal("revoked token reactivated by rotation")
	}
}

func TestCredentialSecretsNeverAppearInFormattingMetadataLogsOrStorage(test *testing.T) {
	store, service, path := setup(test)
	entry, secret, err := service.Create(test.Context(), "alice", metadata())
	if err != nil {
		test.Fatal(err)
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("safe", "credential", secret)
	encoded, err := json.Marshal(secret)
	if err != nil {
		test.Fatal(err)
	}
	entries, _ := service.List(test.Context(), "alice")
	listed, _ := json.Marshal(entries)
	for _, formatted := range []string{fmt.Sprintf("%v %+v %#v %s %q %x", secret, secret, secret, secret, secret, secret), string(encoded), string(listed), log.String()} {
		if strings.Contains(formatted, secret.Reveal()) {
			test.Fatal("credential secret escaped redaction")
		}
	}
	if err := service.Audit(test.Context(), entry.ID, "catalog_get", "/etc/passwd", "success"); !errors.Is(err, state.ErrMCPInvalid) {
		test.Fatal("arbitrary audit resource accepted")
	}
	if err := service.Audit(test.Context(), entry.ID, secret.Reveal(), "", "success"); !errors.Is(err, state.ErrMCPInvalid) {
		test.Fatal("arbitrary audit action accepted")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(path + suffix)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			test.Fatal(err)
		}
		if bytes.Contains(data, []byte(secret.Reveal())) {
			test.Fatal("credential plaintext persisted")
		}
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := state.Open(test.Context(), path, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	restored, _ := mcpaccess.NewService(reopened)
	if grant, err := restored.Authorize(test.Context(), secret.Reveal(), state.MCPCatalogRead); err != nil || grant.Owner != "alice" {
		test.Fatal("hash-only credential did not survive restart")
	}
}

func TestCredentialAndAuditCapacity(test *testing.T) {
	store, service, _ := setup(test)
	entry, _, err := service.Create(test.Context(), "alice", metadata())
	if err != nil {
		test.Fatal(err)
	}
	for range state.MaxMCPAuditEntries + 2 {
		if err := service.Audit(test.Context(), entry.ID, "catalog_list", "", "success"); err != nil {
			test.Fatal(err)
		}
	}
	audits, err := service.ListAudit(test.Context(), "alice", entry.ID)
	if err != nil || len(audits) != state.MaxMCPAuditEntries {
		test.Fatalf("audit retention is not bounded: %d, %v", len(audits), err)
	}
	for range state.MaxMCPTokens - 1 {
		if _, _, err := service.Create(test.Context(), "alice", metadata()); err != nil {
			test.Fatal(err)
		}
	}
	if _, _, err := service.Create(test.Context(), "alice", metadata()); !errors.Is(err, state.ErrMCPLimit) {
		test.Fatal("credential capacity not enforced")
	}
	entries, err := store.ListMCPCredentials(context.Background(), "alice")
	if err != nil || len(entries) != state.MaxMCPTokens {
		test.Fatal("capacity rejection left partial credential")
	}
}
