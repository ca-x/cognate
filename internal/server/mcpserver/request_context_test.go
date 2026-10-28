package mcpserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type requestCancellationKey struct{}

func TestSDKOriginalHTTPRequestCancellationStopsTool(test *testing.T) {
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		test.Run(version, func(test *testing.T) {
			started := make(chan context.CancelFunc, 1)
			exited := make(chan error, 1)
			fixture := newFixture(test, func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
				cancel, _ := ctx.Value(requestCancellationKey{}).(context.CancelFunc)
				started <- cancel
				<-ctx.Done()
				exited <- ctx.Err()
				return nil, ctx.Err()
			})
			endpoint, err := mcpserver.New(mcpserver.Options{Access: fixture.access, Store: fixture.store, Library: fixture.library, RequestTimeout: 2 * time.Second})
			if err != nil {
				test.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				ctx, cancel := context.WithCancel(request.Context())
				defer cancel()
				ctx = context.WithValue(ctx, requestCancellationKey{}, cancel)
				endpoint.ServeHTTP(writer, request.WithContext(ctx))
			}))
			test.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := endpoint.Close(ctx); err != nil {
					test.Error(err)
				}
				server.Close()
			})
			_, secret := fixture.token(test, "alice", state.MCPCatalogWrite)
			client, _ := sdkClientProtocol(test, server, secret, version)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				_, _ = client.CallTool(test.Context(), &mcp.CallToolParams{Name: "catalog_collect", Arguments: mcpserver.CollectInput{Repository: "org/blocked"}})
			}()
			select {
			case cancel := <-started:
				if cancel == nil {
					test.Fatal("original request context value was lost")
				}
				cancel()
			case <-time.After(time.Second):
				test.Fatal("official SDK tool did not start")
			}
			select {
			case err := <-exited:
				if !errors.Is(err, context.Canceled) {
					test.Errorf("original request cancellation became %v", err)
				}
			case <-time.After(250 * time.Millisecond):
				test.Error("tool outlived original HTTP cancellation before its own deadline")
			}
			ctx, cancel := context.WithTimeout(test.Context(), time.Second)
			defer cancel()
			if err := endpoint.Close(ctx); err != nil {
				test.Fatal(err)
			}
			select {
			case <-finished:
			case <-ctx.Done():
				test.Fatal("official SDK call survived endpoint Close")
			}
		})
	}
}
