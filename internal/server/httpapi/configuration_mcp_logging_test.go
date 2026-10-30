package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1/mcpv1connect"
	"github.com/ca-x/cognate/internal/server/logging"
)

func TestMCPConfigurationLoggingPrivacy(test *testing.T) {
	for _, format := range []string{"text", "json"} {
		test.Run(format, func(test *testing.T) {
			logs := configurationCaptureLogs(test, format)
			for _, encoding := range []string{"proto", "json"} {
				test.Run(encoding, func(test *testing.T) {
					fixture := newMCPConfigurationFixture(test)
					directory := test.TempDir()
					if err := os.Chmod(directory, 0700); err != nil {
						test.Fatal(err)
					}
					manager, err := logging.Open(test.Context(), directory)
					if err != nil {
						test.Fatal(err)
					}
					defer manager.Close()
					responses := new(configurationPrivacyBuffer)
					endpoint := httptest.NewTLSServer(NewWithOptions(fixture.auth, Options{MCPConfiguration: fixture.handler, Logging: manager}))
					defer endpoint.Close()
					httpClient := endpoint.Client()
					httpClient.Transport = configurationPrivacyTransport{base: httpClient.Transport, responses: responses}
					options := []connect.ClientOption{}
					if encoding == "json" {
						options = append(options, connect.WithProtoJSON())
					}
					client := mcpv1connect.NewMCPConfigurationServiceClient(httpClient, endpoint.URL, options...)
					created, err := client.CreateAsset(test.Context(), configurationRequest(&mcpv1.CreateAssetRequest{Draft: mcpConfigurationDraft(), Secrets: mcpConfigurationReplacements()}, fixture.token))
					if err != nil {
						test.Fatal(err)
					}
					id := created.Msg.View.Asset.Id
					if _, err := client.ListAssets(test.Context(), configurationRequest(&mcpv1.ListAssetsRequest{}, fixture.token)); err != nil {
						test.Fatal(err)
					}
					if _, err := client.GetAsset(test.Context(), configurationRequest(&mcpv1.GetAssetRequest{Id: id}, fixture.token)); err != nil {
						test.Fatal(err)
					}
					if _, err := client.ReadDraft(test.Context(), configurationRequest(&mcpv1.ReadDraftRequest{Id: id}, fixture.token)); err != nil {
						test.Fatal(err)
					}
					if _, err := client.Preview(test.Context(), configurationRequest(&mcpv1.PreviewRequest{Id: id, ExpectedRevision: 1, Draft: mcpConfigurationDraft()}, fixture.token)); err != nil {
						test.Fatal(err)
					}
					if _, err := client.UpdateAsset(test.Context(), configurationRequest(&mcpv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: mcpConfigurationDraft()}, fixture.token)); err != nil {
						test.Fatal(err)
					}
					copied, err := client.CopyAsset(test.Context(), configurationRequest(&mcpv1.CopyAssetRequest{Id: id, ExpectedSourceRevision: 2, Name: "copy without credentials"}, fixture.token))
					if err != nil {
						test.Fatal(err)
					}
					if _, err := client.DeleteAsset(test.Context(), configurationRequest(&mcpv1.DeleteAssetRequest{Id: copied.Msg.View.Asset.Id, ExpectedRevision: 1}, fixture.token)); err != nil {
						test.Fatal(err)
					}
					if _, err := client.UpdateAsset(test.Context(), configurationRequest(&mcpv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: mcpConfigurationDraft(), Secrets: mcpConfigurationReplacements()}, fixture.token)); connect.CodeOf(err) != connect.CodeAborted {
						test.Fatal("stale privacy gate", err)
					}
					if _, err := client.ReadDraft(test.Context(), configurationRequest(&mcpv1.ReadDraftRequest{Id: id}, fixture.foreign)); connect.CodeOf(err) != connect.CodeNotFound {
						test.Fatal("foreign privacy gate", err)
					}
					ctx, cancel := context.WithCancel(test.Context())
					cancel()
					if _, err := client.CreateAsset(ctx, configurationRequest(&mcpv1.CreateAssetRequest{Draft: mcpConfigurationDraft(), Secrets: mcpConfigurationReplacements()}, fixture.token)); err == nil {
						test.Fatal("canceled call succeeded")
					}
					status, _ := configurationRaw(test, endpoint, fixture.token, mcpv1connect.MCPConfigurationServiceCreateAssetProcedure, "application/json", []byte(`{"draft":null,"sentinel":"synthetic-mcp-header-secret"}`))
					if status < 400 {
						test.Fatal("malformed privacy gate")
					}
					if err := manager.Close(); err != nil {
						test.Fatal(err)
					}
					var recorded []byte
					entries, err := os.ReadDir(filepath.Join(directory, "logs"))
					if err != nil {
						test.Fatal(err)
					}
					for _, entry := range entries {
						if strings.HasSuffix(entry.Name(), ".jsonl") {
							data, err := os.ReadFile(filepath.Join(directory, "logs", entry.Name()))
							if err != nil {
								test.Fatal(err)
							}
							recorded = append(recorded, data...)
						}
					}
					if !bytes.Contains(recorded, []byte("request_completed")) {
						test.Fatal("actual production logger did not record requests")
					}
					for _, secret := range []string{"synthetic-mcp-arg-secret", "synthetic-mcp-header-secret", fixture.token} {
						for _, value := range []string{secret, base64.StdEncoding.EncodeToString([]byte(secret))} {
							if logs.contains(value) || responses.contains(value) || bytes.Contains(recorded, []byte(value)) {
								test.Fatal("secret escaped into response/real logger")
							}
						}
					}
				})
			}
		})
	}
}

type mcpDropReceipt struct{ base http.RoundTripper }

func (transport mcpDropReceipt) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return nil, io.ErrUnexpectedEOF
}

func TestMCPConfigurationCommitReceiptUncertainty(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	base := fixture.server.Client()
	client := mcpv1connect.NewMCPConfigurationServiceClient(&http.Client{Transport: mcpDropReceipt{base: base.Transport}}, fixture.server.URL)
	_, err := client.CreateAsset(test.Context(), configurationRequest(&mcpv1.CreateAssetRequest{Draft: mcpConfigurationDraft(), Secrets: mcpConfigurationReplacements()}, fixture.token))
	if err == nil {
		test.Fatal("receipt drop control did not fail")
	}
	assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
	if err != nil || len(assets) != 1 || assets[0].Revision != 1 {
		test.Fatal("uncertain result rolled back or replayed committed creation", err)
	}
}
