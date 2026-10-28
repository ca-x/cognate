package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMCPLegacyNumericZoneIsRepairedAtOpen(test *testing.T) {
	store, path, key := mcpTimezoneStore(test)
	now := time.Now().UTC().Truncate(time.Second)
	digest := sha256.Sum256([]byte("synthetic numeric timezone legacy fixture"))
	created, err := store.CreateMCPCredential(test.Context(), "alice", digest[:], MCPMetadata{Name: "Legacy numeric", Scopes: []string{MCPCatalogRead}, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	zone := time.FixedZone("+0545", 5*3600+45*60)
	legacyCreated := now.Add(-time.Minute).In(zone).String() + " m=+0.012060093"
	legacyExpiry := now.Add(time.Hour).In(zone).String()
	if _, err := database.ExecContext(test.Context(), "UPDATE mcp_credentials SET created_at=?, expires_at=? WHERE public_id=?", legacyCreated, legacyExpiry, created.ID); err != nil {
		test.Fatal(err)
	}
	for range 2 {
		reopened, err := Open(test.Context(), path, key)
		if err != nil {
			test.Fatal(err)
		}
		metadata, metadataErr := reopened.MCPCredential(test.Context(), "alice", created.ID)
		grant, authorizeErr := reopened.AuthorizeMCP(test.Context(), digest[:], MCPCatalogRead)
		if metadataErr != nil || authorizeErr != nil || grant.CredentialID != created.ID || metadata.Revision != created.Revision || !metadata.CreatedAt.Equal(now.Add(-time.Minute)) || !metadata.ExpiresAt.Equal(now.Add(time.Hour)) {
			_ = reopened.Close()
			test.Fatal("valid legacy numeric-zone credential did not survive repair and restart")
		}
		if _, err := reopened.AuthorizeMCP(test.Context(), digest[:], MCPPacksWrite); !errors.Is(err, ErrMCPForbidden) {
			_ = reopened.Close()
			test.Fatal("timestamp repair expanded scopes")
		}
		if err := reopened.Close(); err != nil {
			test.Fatal(err)
		}
	}
}

func TestMCPTimeCompatibilityPreservesRevocationAndNanos(test *testing.T) {
	store, path, key := mcpTimezoneStore(test)
	now := time.Now().UTC()
	digest := sha256.Sum256([]byte("synthetic revoked numeric-zone fixture"))
	entry, err := store.CreateMCPCredential(test.Context(), "alice", digest[:], MCPMetadata{Name: "Revoked", Scopes: []string{MCPCatalogRead}, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.RevokeMCPCredential(test.Context(), "alice", entry.ID, entry.Revision); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	zone := time.FixedZone("-0330", -(3*3600 + 30*60))
	if _, err := database.ExecContext(test.Context(), "UPDATE mcp_credentials SET created_at=?, expires_at=?, revoked_at=? WHERE public_id=?", now.Add(-time.Minute).In(zone).String(), now.Add(time.Hour).In(zone).String(), now.In(zone).String(), entry.ID); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	metadata, err := reopened.MCPCredential(test.Context(), "alice", entry.ID)
	if err != nil || metadata.RevokedAt == nil || !metadata.RevokedAt.Equal(now) || metadata.Revision != entry.Revision+1 || !metadata.ExpiresAt.Equal(now.Add(time.Hour)) {
		test.Fatal("compatibility repair changed validity, revocation or revision")
	}
	if _, err := reopened.AuthorizeMCP(test.Context(), digest[:], MCPCatalogRead); !errors.Is(err, ErrMCPUnauthorized) {
		test.Fatal("repair restored revoked authority")
	}
}

func TestMCPAuditNumericZoneCompatibility(test *testing.T) {
	store, path, key := mcpTimezoneStore(test)
	now := time.Now().In(time.FixedZone("+0545", 5*3600+45*60))
	store.state.now = func() time.Time { return now }
	digest := sha256.Sum256([]byte("synthetic numeric-zone audit compatibility"))
	entry, err := store.CreateMCPCredential(test.Context(), "alice", digest[:], MCPMetadata{Name: "Audit", Scopes: []string{MCPCatalogRead}, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	if err := store.RecordMCPAudit(test.Context(), entry.ID, "catalog_list", "", "success"); err != nil {
		test.Fatal(err)
	}
	audits, err := store.ListMCPAudit(test.Context(), "alice", entry.ID)
	if err != nil || len(audits) != 1 || !audits[0].CreatedAt.Equal(now) {
		test.Fatal("new numeric-zone audit cannot be read")
	}
	if _, offset := audits[0].CreatedAt.Zone(); offset != 0 {
		test.Fatal("new audit timestamp is not UTC")
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(test.Context(), "UPDATE mcp_audits SET created_at=?", now.String()+" m=+0.012060093"); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	reopened.state.now = func() time.Time { return now.Add(time.Second) }
	if err := reopened.RecordMCPAudit(test.Context(), entry.ID, "catalog_get", "", "success"); err != nil {
		test.Fatal("repaired old audit prevented another append")
	}
	audits, err = reopened.ListMCPAudit(test.Context(), "alice", entry.ID)
	if err != nil || len(audits) != 2 || audits[1].Action != "catalog_list" || audits[1].Outcome != "success" || !audits[1].CreatedAt.Equal(now) {
		test.Fatal("legacy audit contents or precision changed during repair")
	}
	if _, err := reopened.ListMCPAudit(test.Context(), "bob", entry.ID); !errors.Is(err, ErrMCPNotFound) {
		test.Fatal("timestamp repair expanded audit ownership")
	}
}

func TestMCPTimeCompatibilityBatchRollbackAndIdempotence(test *testing.T) {
	store, path, key := mcpTimezoneStore(test)
	now := time.Now().UTC().Truncate(time.Second)
	zone := time.FixedZone("+0545", 5*3600+45*60)
	for _, owner := range []string{"alice", "bob", "carol"} {
		if owner != "alice" {
			if err := store.CreateUser(test.Context(), owner); err != nil {
				test.Fatal(err)
			}
		}
		for index := range 90 {
			digest := sha256.Sum256([]byte(fmt.Sprintf("synthetic legacy batch %s %d", owner, index)))
			entry, err := store.CreateMCPCredential(test.Context(), owner, digest[:], MCPMetadata{Name: "Batch", Scopes: []string{MCPCatalogRead}, ExpiresAt: now.Add(time.Hour)})
			if err != nil {
				test.Fatal(err)
			}
			if err := store.RecordMCPAudit(test.Context(), entry.ID, "catalog_list", "", "success"); err != nil {
				test.Fatal(err)
			}
		}
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	legacyCreated := now.Add(-time.Minute).In(zone).String()
	if _, err := database.ExecContext(test.Context(), "UPDATE mcp_credentials SET created_at=?, expires_at=?", legacyCreated, now.Add(time.Hour).In(zone).String()); err != nil {
		test.Fatal(err)
	}
	if _, err := database.ExecContext(test.Context(), "UPDATE mcp_audits SET created_at=?", legacyCreated); err != nil {
		test.Fatal(err)
	}
	if _, err := database.ExecContext(test.Context(), "UPDATE mcp_credentials SET expires_at=? WHERE id=2", "invalid-time-synthetic"); err != nil {
		test.Fatal(err)
	}
	if reopened, err := Open(test.Context(), path, key); err == nil {
		_ = reopened.Close()
		test.Fatal("unrecoverable timestamp did not fail closed")
	} else if strings.Contains(err.Error(), "invalid-time-synthetic") {
		test.Fatal("compatibility error disclosed raw input")
	}
	var createdAt string
	if err := database.QueryRowContext(test.Context(), "SELECT CAST(created_at AS TEXT) FROM mcp_credentials WHERE id=1").Scan(&createdAt); err != nil || createdAt != legacyCreated {
		test.Fatal("failed batch did not roll back its earlier timestamp changes")
	}
	if _, err := database.ExecContext(test.Context(), "UPDATE mcp_credentials SET expires_at=? WHERE id=2", now.Add(time.Hour).In(zone).String()); err != nil {
		test.Fatal(err)
	}
	for range 2 {
		reopened, err := Open(test.Context(), path, key)
		if err != nil {
			test.Fatal(err)
		}
		for _, owner := range []string{"alice", "bob", "carol"} {
			entries, err := reopened.ListMCPCredentials(test.Context(), owner)
			if err != nil || len(entries) != 90 {
				_ = reopened.Close()
				test.Fatal("bounded compatibility batches lost owner metadata")
			}
			audits, err := reopened.ListMCPAudit(test.Context(), owner, entries[0].ID)
			if err != nil || len(audits) != 1 || !audits[0].CreatedAt.Equal(now.Add(-time.Minute)) {
				_ = reopened.Close()
				test.Fatal("bounded audit batches lost their timestamp")
			}
		}
		if err := reopened.Close(); err != nil {
			test.Fatal(err)
		}
	}
	if _, err := database.ExecContext(test.Context(), "CREATE TRIGGER prevent_mcp_timestamp_rewrite BEFORE UPDATE ON mcp_credentials BEGIN SELECT RAISE(ABORT, 'unexpected rewrite'); END"); err != nil {
		test.Fatal(err)
	}
	if _, err := database.ExecContext(test.Context(), "CREATE TRIGGER prevent_mcp_audit_timestamp_rewrite BEFORE UPDATE ON mcp_audits BEGIN SELECT RAISE(ABORT, 'unexpected audit rewrite'); END"); err != nil {
		test.Fatal(err)
	}
	if err := normalizeMCPTimestamps(test.Context(), path); err != nil {
		test.Fatal("already canonical timestamps were rewritten")
	}
	canceled, cancel := context.WithCancel(test.Context())
	cancel()
	if err := normalizeMCPTimestamps(canceled, path); !errors.Is(err, context.Canceled) {
		test.Fatal("compatibility scan did not preserve cancellation")
	}
}

func TestParseMCPTimeCompatibilityBounds(test *testing.T) {
	for _, invalid := range []string{"", strings.Repeat("0", 257), "not-a-date", "2030-01-01 10:30:00 +9999 +9999", "2030-01-01 10:30:00 +0545 +0545 m=NaN", "2030-01-01 10:30:00 +0545 +0545 m=+Inf", "2030-01-01 10:30:00 +0545 +0545 extra"} {
		if _, err := parseMCPTimestamp(invalid); err == nil {
			test.Fatal("invalid or over-budget legacy timestamp accepted")
		}
	}
}
