package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
)

func mcpTimezoneStore(test *testing.T) (*Store, string, []byte) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	path, key := filepath.Join(directory, "server.db"), make([]byte, 32)
	store, err := Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	return store, path, key
}

func TestMCPCredentialsUseInstantsAcrossTimezones(test *testing.T) {
	instant := time.Date(2030, 1, 1, 2, 30, 0, 0, time.UTC)
	for _, location := range []*time.Location{time.FixedZone("CST", 8*3600), time.FixedZone("PDT", -7*3600), time.FixedZone("NPT", 5*3600+45*60)} {
		test.Run(location.String(), func(test *testing.T) {
			store, path, key := mcpTimezoneStore(test)
			store.state.now = func() time.Time { return instant.In(location) }
			digest := sha256.Sum256([]byte("synthetic MCP timezone fixture"))
			entry, err := store.CreateMCPCredential(test.Context(), "alice", digest[:], MCPMetadata{Name: "Timezone", Scopes: []string{MCPCatalogRead}, ExpiresAt: instant.Add(time.Hour)})
			if err != nil {
				test.Fatal(err)
			}
			if _, offset := entry.CreatedAt.Zone(); offset != 0 {
				test.Fatal("new creation timestamp is not canonical UTC")
			}
			updated, err := store.UpdateMCPCredential(test.Context(), "alice", entry.ID, entry.Revision, MCPMetadata{Name: "Timezone", Scopes: []string{MCPCatalogRead}, ExpiresAt: instant.Add(time.Hour).In(location)})
			if err != nil {
				test.Fatal(err)
			}
			if _, offset := updated.ExpiresAt.Zone(); offset != 0 {
				test.Fatal("updated expiry timestamp is not canonical UTC")
			}
			grant, err := store.AuthorizeMCP(test.Context(), digest[:], MCPCatalogRead)
			if err != nil || grant.CredentialID != entry.ID || grant.Owner != "alice" {
				test.Fatal("valid credential rejected by process timezone")
			}
			if err := store.Close(); err != nil {
				test.Fatal(err)
			}
			reopened, err := Open(test.Context(), path, key)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = reopened.Close() })
			reopened.state.now = func() time.Time { return instant.Add(time.Minute).UTC() }
			if _, err := reopened.AuthorizeMCP(test.Context(), digest[:], MCPCatalogRead); err != nil {
				test.Fatal("timezone change across restart invalidated credential")
			}
			reopened.state.now = func() time.Time { return instant.Add(time.Minute).In(location) }
			revoked, err := reopened.RevokeMCPCredential(test.Context(), "alice", entry.ID, updated.Revision)
			if err != nil || revoked.RevokedAt == nil {
				test.Fatal("timezone credential revocation failed")
			}
			if _, offset := revoked.RevokedAt.Zone(); offset != 0 {
				test.Fatal("revocation timestamp is not canonical UTC")
			}
			if _, err := reopened.AuthorizeMCP(test.Context(), digest[:], MCPCatalogRead); !errors.Is(err, ErrMCPUnauthorized) {
				test.Fatal("revoked timezone credential retained authority")
			}
		})
	}
}

func TestMCPLegacyOffsetTimesRemainValidWithoutMigration(test *testing.T) {
	instant := time.Date(2030, 1, 1, 2, 30, 0, 0, time.UTC)
	locations := []*time.Location{time.UTC, time.FixedZone("CST", 8*3600), time.FixedZone("PDT", -7*3600)}
	for _, createdLocation := range locations {
		for _, expiryLocation := range locations {
			test.Run(createdLocation.String()+"/"+expiryLocation.String(), func(test *testing.T) {
				store, _, _ := mcpTimezoneStore(test)
				owner, err := findUser(test.Context(), store.state.client, "alice")
				if err != nil {
					test.Fatal(err)
				}
				digest := sha256.Sum256([]byte("synthetic legacy offset credential"))
				createdAt, expiresAt := instant.Add(-time.Hour), instant.Add(time.Hour)
				entry, err := store.state.client.MCPCredential.Create().SetUserID(owner.ID).SetPublicID(rand.Text()).SetName("Legacy offset").SetScopes([]string{MCPCatalogRead}).SetTokenHash(digest[:]).SetCreatedAt(createdAt.In(createdLocation)).SetExpiresAt(expiresAt.In(expiryLocation)).Save(test.Context())
				if err != nil {
					test.Fatal(err)
				}
				if _, err := store.state.client.MCPCredential.Get(test.Context(), entry.ID); err != nil {
					test.Fatalf("legacy time fixture could not be read: %v", err)
				}
				for _, clockLocation := range locations {
					for _, boundary := range []struct {
						name  string
						now   time.Time
						valid bool
					}{
						{name: "before-created", now: createdAt.Add(-time.Nanosecond)},
						{name: "at-created", now: createdAt, valid: true},
						{name: "active", now: instant, valid: true},
						{name: "before-expiry", now: expiresAt.Add(-time.Nanosecond), valid: true},
						{name: "at-expiry", now: expiresAt},
						{name: "after-expiry", now: expiresAt.Add(time.Nanosecond)},
					} {
						test.Run(clockLocation.String()+"/"+boundary.name, func(test *testing.T) {
							store.state.now = func() time.Time { return boundary.now.In(clockLocation) }
							grant, err := store.AuthorizeMCP(test.Context(), digest[:], MCPCatalogRead)
							if boundary.valid {
								if err != nil || grant.CredentialID != entry.PublicID || !grant.ExpiresAt.Equal(expiresAt) {
									test.Fatalf("legacy offset credential lost valid authority: %v", err)
								}
							} else if !errors.Is(err, ErrMCPUnauthorized) || grant.Owner != "" || grant.proof != nil {
								test.Fatal("credential received authority outside validity interval")
							}
						})
					}
				}
			})
		}
	}
}

func TestMCPAuthorizationRechecksCreationAfterClockRollback(test *testing.T) {
	store, _, _ := mcpTimezoneStore(test)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	createdAt := now
	store.state.now = func() time.Time { return now }
	digest := sha256.Sum256([]byte("synthetic MCP clock rollback fixture"))
	if _, err := store.CreateMCPCredential(test.Context(), "alice", digest[:], MCPMetadata{Name: "Rollback", Scopes: []string{MCPCatalogRead}, ExpiresAt: now.Add(time.Hour)}); err != nil {
		test.Fatal(err)
	}
	store.state.client.User.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			now = createdAt.Add(-time.Nanosecond)
			return value, err
		})
	}))
	grant, err := store.AuthorizeMCP(test.Context(), digest[:], MCPCatalogRead)
	if !errors.Is(err, ErrMCPUnauthorized) || grant.Owner != "" || grant.proof != nil {
		test.Fatal("clock rollback during owner lookup retained authority")
	}
}
