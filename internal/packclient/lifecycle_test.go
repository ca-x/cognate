package packclient

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	packv1 "github.com/ca-x/skillhub/gen/skillhub/pack/v1"
	"github.com/ca-x/skillhub/gen/skillhub/pack/v1/packv1connect"
)

func TestServerErrorDiagnosticsRedactCredentialsAndPreserveCode(test *testing.T) {
	token := fixtureToken()
	fixture := distributionFixture{manifest: func(context.Context, *connect.Request[packv1.GetManifestRequest]) (*connect.Response[packv1.GetManifestResponse], error) {
		failure := connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("untrusted server echoed %s", token))
		failure.Meta().Set("Authorization", "Bearer "+token)
		return nil, failure
	}}
	_, handler := packv1connect.NewDistributionServiceHandler(fixture)
	server := httptest.NewTLSServer(handler)
	test.Cleanup(server.Close)
	client, err := New(server.URL, token, server.Client())
	if err != nil {
		test.Fatal(err)
	}
	_, err = client.Manifest(test.Context())
	if connect.CodeOf(err) != connect.CodeUnauthenticated || strings.Contains(fmt.Sprintf("%v %#v %+v", err, err, err), token) {
		test.Fatal("server error lost its code or disclosed credentials")
	}
	if failure, ok := errors.AsType[*connect.Error](err); !ok || len(failure.Meta()) != 0 {
		test.Fatal("untrusted error metadata escaped the client boundary")
	}
}

func TestClientHonorsShorterSuppliedTimeout(test *testing.T) {
	finished := make(chan struct{})
	fixture := distributionFixture{manifest: func(ctx context.Context, _ *connect.Request[packv1.GetManifestRequest]) (*connect.Response[packv1.GetManifestResponse], error) {
		defer close(finished)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	_, handler := packv1connect.NewDistributionServiceHandler(fixture)
	server := httptest.NewServer(handler)
	test.Cleanup(server.Close)
	supplied := server.Client()
	supplied.Timeout = 80 * time.Millisecond
	client, err := New(server.URL, fixtureToken(), supplied)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := client.Manifest(test.Context()); !errors.Is(err, context.DeadlineExceeded) {
		test.Fatalf("client timeout was not preserved: %v", err)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		test.Fatal("timed out request did not finish")
	}
}

func TestConcurrencyBudgetIsFailFastAndReusable(test *testing.T) {
	manifest, _ := fixtureManifest(test)
	started := make(chan struct{}, 2)
	var block atomic.Bool
	block.Store(true)
	fixture := distributionFixture{manifest: func(ctx context.Context, _ *connect.Request[packv1.GetManifestRequest]) (*connect.Response[packv1.GetManifestResponse], error) {
		if block.Load() {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return connect.NewResponse(manifest), nil
	}}
	_, handler := packv1connect.NewDistributionServiceHandler(fixture)
	server := httptest.NewServer(handler)
	test.Cleanup(server.Close)
	client, err := New(server.URL, fixtureToken(), server.Client())
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		workers.Go(func() {
			_, err := client.Manifest(ctx)
			results <- err
		})
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			test.Fatal("concurrent requests did not reach the server")
		}
	}
	if _, err := client.Manifest(test.Context()); connect.CodeOf(err) != connect.CodeResourceExhausted {
		test.Fatal("concurrency overflow was not rejected")
	}
	cancel()
	workers.Wait()
	for range 2 {
		if err := <-results; !errors.Is(err, context.Canceled) {
			test.Fatalf("canceled worker did not finish: %v", err)
		}
	}
	block.Store(false)
	if _, err := client.Manifest(test.Context()); err != nil {
		test.Fatalf("concurrency slot was not released: %v", err)
	}
}

func TestSnapshotConcurrentCloseIsIdempotent(test *testing.T) {
	manifest, entries := fixtureManifest(test)
	client := fixtureClient(test, manifest, fixtureFrames(fixtureArchive(test, entries), manifest.Skills[0].ContentHash), nil)
	snapshot, err := client.Download(test.Context(), manifest, manifest.Skills[0])
	if err != nil {
		test.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan error, 32)
	for range cap(results) {
		workers.Go(func() { results <- snapshot.Close() })
	}
	workers.Wait()
	for range cap(results) {
		if err := <-results; err != nil {
			test.Fatalf("concurrent cleanup failed: %v", err)
		}
	}
	var zero Snapshot
	if err := zero.Close(); err != nil || zero.Path() != "" {
		test.Fatal("zero snapshot is unsafe")
	}
	var missing *Snapshot
	if err := missing.Close(); err != nil || missing.Path() != "" {
		test.Fatal("nil snapshot is unsafe")
	}
}
