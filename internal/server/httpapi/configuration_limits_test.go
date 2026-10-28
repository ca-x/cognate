package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type configurationBlockingStore struct {
	*state.Store
	entered chan time.Duration
	exited  chan struct{}
	release chan struct{}
}

type configurationOversizedResponse struct {
	configurationv1connect.UnimplementedConfigurationServiceHandler
}

func (configurationOversizedResponse) ListAssets(context.Context, *connect.Request[configurationv1.ListAssetsRequest]) (*connect.Response[configurationv1.ListAssetsResponse], error) {
	return connect.NewResponse(&configurationv1.ListAssetsResponse{Assets: []*configurationv1.Asset{{Name: strings.Repeat("x", (1<<20)+1)}}}), nil
}

func TestConfigurationResponseBudget(test *testing.T) {
	_, handler := configurationTransport(configurationOversizedResponse{})
	endpoint := httptest.NewServer(handler)
	test.Cleanup(endpoint.Close)
	for _, options := range [][]connect.ClientOption{nil, {connect.WithProtoJSON()}} {
		client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
		_, err := client.ListAssets(test.Context(), connect.NewRequest(&configurationv1.ListAssetsRequest{}))
		if err == nil {
			test.Fatal("oversized uncompressed response was accepted through gzip")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			test.Fatalf("1MiB response limit: %s", connect.CodeOf(err))
		}
	}
}

func (store *configurationBlockingStore) ListConfigurationAssets(ctx context.Context, owner string) ([]state.ConfigurationAsset, error) {
	deadline, _ := ctx.Deadline()
	store.entered <- time.Until(deadline)
	defer func() { store.exited <- struct{}{} }()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-store.release:
		return store.Store.ListConfigurationAssets(ctx, owner)
	}
}

func TestConfigurationEightSlotsCancellationAndDeadline(test *testing.T) {
	service, store := testServiceAndStore(test)
	_, session := mcpSession(test, service, "configuration-capacity")
	blocking := &configurationBlockingStore{Store: store, entered: make(chan time.Duration, 16), exited: make(chan struct{}, 16), release: make(chan struct{})}
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(blocking)}))
	test.Cleanup(endpoint.Close)
	client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	var workers sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		workers.Go(func() {
			_, err := client.ListAssets(ctx, configurationRequest(&configurationv1.ListAssetsRequest{}, session))
			results <- err
		})
	}
	for range 8 {
		select {
		case remaining := <-blocking.entered:
			if remaining <= 0 || remaining > 20*time.Second {
				test.Fatal("local operation has no bounded 20s deadline")
			}
		case <-time.After(5 * time.Second):
			test.Fatal("eight local operations did not enter")
		}
	}
	if _, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session)); connect.CodeOf(err) != connect.CodeResourceExhausted {
		test.Fatal("ninth local operation was not rejected")
	}
	cancel()
	workers.Wait()
	for range 8 {
		if err := <-results; connect.CodeOf(err) != connect.CodeCanceled {
			test.Fatalf("client cancellation: %s", connect.CodeOf(err))
		}
		select {
		case <-blocking.exited:
		case <-time.After(5 * time.Second):
			test.Fatal("HTTP cancellation did not reach the store")
		}
	}
	close(blocking.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
		if err == nil {
			break
		}
		if connect.CodeOf(err) != connect.CodeResourceExhausted || time.Now().After(deadline) {
			test.Fatal("canceled requests did not release capacity")
		}
	}
}

func TestConfigurationRequestBudgetsAndOrigin(test *testing.T) {
	service, store := testServiceAndStore(test)
	_, session := mcpSession(test, service, "configuration-budgets")
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
	test.Cleanup(endpoint.Close)
	procedures := []string{
		configurationv1connect.ConfigurationServiceListAssetsProcedure,
		configurationv1connect.ConfigurationServiceGetAssetProcedure,
		configurationv1connect.ConfigurationServiceReadDraftProcedure,
		configurationv1connect.ConfigurationServiceCreateAssetProcedure,
		configurationv1connect.ConfigurationServiceUpdateAssetProcedure,
		configurationv1connect.ConfigurationServiceCopyAssetProcedure,
		configurationv1connect.ConfigurationServiceDeleteAssetProcedure,
	}
	for _, procedure := range procedures {
		data := []byte(`{}` + strings.Repeat(" ", (128<<10)-1))
		status, body := configurationRaw(test, endpoint, session, procedure, "application/json", data)
		if status < 400 || status >= 500 || len(body) > 4096 {
			test.Fatalf("unbounded %s: status %d", procedure, status)
		}
	}
	status, _ := configurationRaw(test, endpoint, session, procedures[0], "application/json", []byte(`{}`+strings.Repeat(" ", (128<<10)-2)))
	if status != http.StatusOK {
		test.Fatalf("exact 128KiB request budget: status %d", status)
	}
	status, _ = configurationRaw(test, endpoint, session, procedures[0], "application/json", []byte(`{}`+strings.Repeat(" ", 5000)))
	if status != http.StatusOK {
		test.Fatal("configuration request inherited generic 4KiB Echo limit")
	}
	var compressed bytes.Buffer
	compressor := gzip.NewWriter(&compressed)
	if _, err := compressor.Write([]byte(`{}` + strings.Repeat(" ", 129<<10))); err != nil {
		test.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		test.Fatal(err)
	}
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, endpoint.URL+procedures[0], &compressed)
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Encoding", "gzip")
	request.Header.Set("Authorization", "Bearer "+session)
	response, err := endpoint.Client().Do(request)
	if err != nil {
		test.Fatal("compressed request failed")
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode < 400 || response.StatusCode >= 500 {
		test.Fatal("decompressed request budget bypassed")
	}
	request, err = http.NewRequestWithContext(test.Context(), http.MethodPost, endpoint.URL+procedures[0], strings.NewReader(`{}`))
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Origin", "https://foreign.invalid")
	response, err = endpoint.Client().Do(request)
	if err != nil {
		test.Fatal("origin fixture failed")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		test.Fatal("cross-origin configuration allowed")
	}
}
