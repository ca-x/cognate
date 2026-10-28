package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type configurationFaultStore struct {
	*state.Store
	entries []state.ConfigurationAsset
	err     error
}

func (store *configurationFaultStore) ListConfigurationAssets(context.Context, string) ([]state.ConfigurationAsset, error) {
	return store.entries, store.err
}

func TestConfigurationErrorClassificationDoesNotEchoFields(test *testing.T) {
	service, store := testServiceAndStore(test)
	_, session := mcpSession(test, service, "configuration-failure")
	for _, failure := range []struct {
		name string
		err  error
		code connect.Code
	}{
		{"invalid", fmt.Errorf("private-field-sentinel: %w", state.ErrConfigurationInvalid), connect.CodeInvalidArgument},
		{"missing", state.ErrConfigurationNotFound, connect.CodeNotFound},
		{"conflict", state.ErrConfigurationConflict, connect.CodeAborted},
		{"capacity", state.ErrConfigurationLimit, connect.CodeResourceExhausted},
		{"unavailable", errors.New("private-field-sentinel"), connect.CodeInternal},
		{"canceled", context.Canceled, connect.CodeCanceled},
		{"deadline", context.DeadlineExceeded, connect.CodeDeadlineExceeded},
	} {
		test.Run(failure.name, func(test *testing.T) {
			endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(&configurationFaultStore{Store: store, err: failure.err})}))
			test.Cleanup(endpoint.Close)
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
			_, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
			if connect.CodeOf(err) != failure.code || strings.Contains(err.Error(), "private-field-sentinel") {
				test.Fatalf("failure mapping: %s", connect.CodeOf(err))
			}
		})
	}
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(&configurationFaultStore{Store: store, entries: []state.ConfigurationAsset{{ID: strings.Repeat("A", 26), AgentID: "claude", Kind: "future-kind", SchemaVersion: 1, Name: "Private name", Revision: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}}})}))
	test.Cleanup(endpoint.Close)
	client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
	_, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
	if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "Private name") || strings.Contains(err.Error(), "future-kind") {
		test.Fatal("unknown storage kind did not fail closed")
	}
}
