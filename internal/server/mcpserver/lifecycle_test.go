package mcpserver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSDKCloseCancelsWaitsAndRejectsNewWork(test *testing.T) {
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		test.Run(version, func(test *testing.T) { closeCancels(test, version) })
	}
}

func closeCancels(test *testing.T, version string) {
	test.Helper()
	started, exited := make(chan struct{}), make(chan struct{})
	fixture := newFixture(test, func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
		close(started)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	})
	endpoint, server := fixture.endpoint(test, mcpserver.Options{MaxConcurrent: 1})
	_, secret := fixture.token(test, "alice", state.MCPCatalogWrite)
	client, _ := sdkClientProtocol(test, server, secret, version)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = client.CallTool(test.Context(), &mcp.CallToolParams{Name: "catalog_collect", Arguments: mcpserver.CollectInput{Repository: "org/blocked"}})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		test.Fatal("real SDK tool did not start")
	}
	newRequest := func() *http.Request {
		request, err := http.NewRequestWithContext(test.Context(), "POST", server.URL+mcpserver.Path, strings.NewReader(`{}`))
		if err != nil {
			test.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+secret.Reveal())
		return request
	}
	response, err := server.Client().Do(newRequest())
	if err != nil {
		test.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		test.Fatalf("concurrent request not bounded: %d", response.StatusCode)
	}
	ctx, cancel := context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	if err := endpoint.Close(ctx); err != nil {
		test.Fatal(err)
	}
	select {
	case <-exited:
	default:
		test.Fatal("Close returned before tool dependency exited")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		test.Fatal("SDK request survived Close")
	}
	if err := endpoint.Close(ctx); err != nil {
		test.Fatal("repeated Close failed")
	}
	response, err = server.Client().Do(newRequest())
	if err != nil {
		test.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		test.Fatal("closed endpoint accepted new work")
	}
	entries, err := fixture.store.ListCollectedRepositories(test.Context(), "alice")
	if err != nil || len(entries) != 0 {
		test.Fatal("canceled operation mutated repository library")
	}
}

func TestSDKRequestDeadlineCancelsDependency(test *testing.T) {
	exited := make(chan struct{})
	fixture := newFixture(test, func(ctx context.Context, _ repository.Reference) (*repository.ArchiveSnapshot, error) {
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	})
	_, server := fixture.endpoint(test, mcpserver.Options{RequestTimeout: 75 * time.Millisecond})
	_, secret := fixture.token(test, "alice", state.MCPCatalogWrite)
	client, _ := sdkClient(test, server, secret)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = client.CallTool(test.Context(), &mcp.CallToolParams{Name: "catalog_collect", Arguments: mcpserver.CollectInput{Repository: "org/blocked"}})
	}()
	select {
	case <-exited:
	case <-time.After(time.Second):
		test.Fatal("request deadline did not cancel dependency")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		test.Fatal("request outlived its deadline")
	}
}

func TestCloseCallerDeadlineBoundsUncooperativeDependency(test *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	fixture := newFixture(test, func(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
		close(started)
		<-release
		return nil, errors.New("dependency stopped")
	})
	endpoint, server := fixture.endpoint(test, mcpserver.Options{})
	_, secret := fixture.token(test, "alice", state.MCPCatalogWrite)
	client, _ := sdkClient(test, server, secret)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = client.CallTool(test.Context(), &mcp.CallToolParams{Name: "catalog_collect", Arguments: mcpserver.CollectInput{Repository: "org/blocked"}})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		test.Fatal("tool did not start")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 20*time.Millisecond)
	defer cancel()
	err := endpoint.Close(ctx)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		test.Fatalf("Close ignored caller deadline: %v", err)
	}
	ctx, cancel = context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	if err := endpoint.Close(ctx); err != nil {
		test.Fatal(err)
	}
	<-finished
}

func TestSDKToolBoundsAndForbiddenInputs(test *testing.T) {
	fixture := newFixture(test, nil)
	_, server := fixture.endpoint(test, mcpserver.Options{})
	entry, secret := fixture.token(test, "alice", state.MCPCatalogRead, state.MCPCatalogWrite)
	client, _ := sdkClient(test, server, secret)
	for _, input := range []mcpserver.PageInput{{Limit: 101}, {Limit: -1}, {Offset: -1}, {Offset: 1001}} {
		if !call(test, client, "catalog_list", input).IsError {
			test.Fatal("out-of-bound page accepted")
		}
	}
	for _, repository := range []string{"file:///etc/passwd", "https://attacker.invalid/repo", "../../private", "https://github.com/org/repo"} {
		if !call(test, client, "catalog_collect", mcpserver.CollectInput{Repository: repository}).IsError {
			test.Fatal("noncanonical repository input accepted")
		}
	}
	for _, tool := range []string{"token_create", "token_rotate", "publish", "category_create", "execute", "fetch_url", "read_file", "share_get"} {
		if _, err := client.CallTool(test.Context(), &mcp.CallToolParams{Name: tool, Arguments: map[string]any{}}); err == nil {
			test.Fatalf("forbidden tool exists: %s", tool)
		}
	}
	audits, err := fixture.access.ListAudit(test.Context(), "alice", entry.ID)
	if err != nil {
		test.Fatal(err)
	}
	for _, audit := range audits {
		if strings.Contains(audit.Resource, "/") || strings.Contains(audit.Action, "attacker") {
			test.Fatal("request input entered audit metadata")
		}
	}
}

func TestHTTPChunkedBodyIsBounded(test *testing.T) {
	fixture := newFixture(test, nil)
	_, server := fixture.endpoint(test, mcpserver.Options{})
	_, secret := fixture.token(test, "alice", state.MCPCatalogRead)
	request, _ := http.NewRequestWithContext(test.Context(), "POST", server.URL+mcpserver.Path, io.NopCloser(strings.NewReader(strings.Repeat(" ", mcpserver.MaxRequestBytes+1))))
	request.ContentLength = -1
	request.Header.Set("Authorization", "Bearer "+secret.Reveal())
	response, err := server.Client().Do(request)
	if err != nil {
		test.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		test.Fatalf("chunked body bypassed bound: %d", response.StatusCode)
	}
}
