package state

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
)

func TestMCPAuthorizationChecksExpiryAfterCredentialAndUserQueries(test *testing.T) {
	for _, queryName := range []string{"credential", "user"} {
		test.Run(queryName, func(test *testing.T) {
			for _, boundary := range []struct {
				name   string
				offset time.Duration
			}{
				{name: "before", offset: -time.Nanosecond},
				{name: "equal"},
				{name: "after", offset: time.Nanosecond},
			} {
				test.Run(boundary.name, func(test *testing.T) {
					for _, scope := range []string{"", MCPCatalogRead} {
						test.Run("scope="+scope, func(test *testing.T) {
							directory := test.TempDir()
							if err := os.Chmod(directory, 0o700); err != nil {
								test.Fatal(err)
							}
							store, err := Open(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32))
							if err != nil {
								test.Fatal(err)
							}
							test.Cleanup(func() { _ = store.Close() })
							now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
							store.state.now = func() time.Time { return now }
							if err := store.CreateUser(test.Context(), "alice"); err != nil {
								test.Fatal(err)
							}
							expiry := now.Add(time.Minute)
							digest := sha256.Sum256([]byte("MCP authorization boundary fixture"))
							entry, err := store.CreateMCPCredential(test.Context(), "alice", digest[:], MCPMetadata{Name: "Boundary", Scopes: []string{MCPCatalogRead}, ExpiresAt: expiry})
							if err != nil {
								test.Fatal(err)
							}
							if _, err := store.AuthorizeMCP(test.Context(), digest[:], scope); err != nil {
								test.Fatalf("unexpired control failed: %v", err)
							}
							advanced := false
							interceptor := ent.InterceptFunc(func(next ent.Querier) ent.Querier {
								return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
									value, err := next.Query(ctx, query)
									if err == nil {
										now = expiry.Add(boundary.offset)
										advanced = true
									}
									return value, err
								})
							})
							if queryName == "credential" {
								store.state.client.MCPCredential.Intercept(interceptor)
							} else {
								store.state.client.User.Intercept(interceptor)
							}
							grant, err := store.AuthorizeMCP(test.Context(), digest[:], scope)
							if !advanced {
								test.Fatal("authorization did not finish the selected query")
							}
							if boundary.offset < 0 {
								if err != nil || grant.CredentialID != entry.ID || grant.Owner != "alice" || len(grant.Scopes) != 1 || grant.Scopes[0] != MCPCatalogRead || !grant.ExpiresAt.Equal(expiry) {
									test.Fatalf("strictly unexpired credential lost its owner/scope grant: %v", err)
								}
							} else if !errors.Is(err, ErrMCPUnauthorized) || grant.CredentialID != "" || grant.Owner != "" || len(grant.Scopes) != 0 || !grant.ExpiresAt.IsZero() {
								test.Fatalf("authorization at or after expiry returned authority: %v", err)
							}
						})
					}
				})
			}
		})
	}
}
