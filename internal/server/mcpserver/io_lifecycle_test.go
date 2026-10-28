package mcpserver_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/server/mcpaccess"
	"github.com/ca-x/skillhub/internal/server/mcpserver"
	"github.com/ca-x/skillhub/internal/server/state"
)

func stalledBody(test *testing.T, address string, secret mcpaccess.Secret) net.Conn {
	test.Helper()
	host := strings.TrimPrefix(address, "http://")
	connection, err := net.DialTimeout("tcp", host, time.Second)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = connection.Close() })
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		test.Fatal(err)
	}
	if _, err := fmt.Fprintf(connection, "POST /mcp HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: 20\r\n\r\n{", host, secret.Reveal()); err != nil {
		test.Fatal("write stalled HTTP request")
	}
	return connection
}

func TestHTTPStalledBodyIsCanceledAndWaitedByClose(test *testing.T) {
	fixture := newFixture(test, nil)
	endpoint, server := fixture.endpoint(test, mcpserver.Options{MaxConcurrent: 1})
	_, secret := fixture.token(test, "alice", state.MCPCatalogRead)
	connection := stalledBody(test, server.URL, secret)
	ctx, cancel := context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	for {
		request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+mcpserver.Path, nil)
		request.Header.Set("Authorization", "Bearer "+secret.Reveal())
		response, err := server.Client().Do(request)
		if err != nil {
			test.Fatal("stalled body did not occupy the bounded request slot")
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusServiceUnavailable {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := endpoint.Close(ctx); err != nil {
		test.Fatalf("Close did not unblock real HTTP body I/O: %v", err)
	}
	if _, err := io.ReadAll(connection); err != nil {
		test.Fatal("HTTP connection survived endpoint Close")
	}
}

func TestHTTPStalledBodyIsBoundedByRequestDeadline(test *testing.T) {
	fixture := newFixture(test, nil)
	_, server := fixture.endpoint(test, mcpserver.Options{RequestTimeout: 75 * time.Millisecond})
	_, secret := fixture.token(test, "alice", state.MCPCatalogRead)
	connection := stalledBody(test, server.URL, secret)
	if _, err := io.ReadAll(connection); err != nil {
		test.Fatal("stalled body outlived the request deadline")
	}
}
