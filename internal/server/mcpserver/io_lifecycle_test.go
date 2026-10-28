package mcpserver_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/state"
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

type observedHTTPBody struct {
	io.ReadCloser
	entered  func()
	finished func()
}

func (body *observedHTTPBody) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	if count > 0 {
		body.entered()
	}
	if err != nil {
		body.finished()
	}
	return count, err
}

func TestHTTPStalledBodyIsCanceledAndWaitedByClose(test *testing.T) {
	fixture := newFixture(test, nil)
	endpoint, err := mcpserver.New(mcpserver.Options{Access: fixture.access, Store: fixture.store, Library: fixture.library, MaxConcurrent: 1})
	if err != nil {
		test.Fatal(err)
	}
	entered, finished := make(chan struct{}), make(chan struct{})
	enter := sync.OnceFunc(func() { close(entered) })
	finish := sync.OnceFunc(func() { close(finished) })
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ContentLength == 20 {
			request.Body = &observedHTTPBody{ReadCloser: request.Body, entered: enter, finished: finish}
		}
		endpoint.ServeHTTP(writer, request)
	}))
	test.Cleanup(server.Close)
	test.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := endpoint.Close(ctx); err != nil {
			test.Error(err)
		}
	})
	_, secret := fixture.token(test, "alice", state.MCPCatalogRead)
	connection := stalledBody(test, server.URL, secret)
	select {
	case <-entered:
	case <-time.After(time.Second):
		test.Fatal("real HTTP body Read did not consume the partial request")
	}
	probeCtx, probeCancel := context.WithTimeout(test.Context(), time.Second)
	defer probeCancel()
	request, err := http.NewRequestWithContext(probeCtx, "POST", server.URL+mcpserver.Path, nil)
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret.Reveal())
	response, err := server.Client().Do(request)
	if err != nil {
		test.Fatal("probe failed after the real HTTP body Read entered")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		test.Fatalf("stalled HTTP body did not retain its request slot: %d", response.StatusCode)
	}
	ctx, cancel := context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	if err := endpoint.Close(ctx); err != nil {
		test.Fatalf("Close did not unblock real HTTP body I/O: %v", err)
	}
	select {
	case <-finished:
	default:
		test.Fatal("Close returned before the real HTTP body Read finished")
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
