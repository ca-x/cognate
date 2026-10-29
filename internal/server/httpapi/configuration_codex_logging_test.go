package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/encoding/protojson"
)

type codexReceiptStore struct {
	*state.Store
	calls   atomic.Int32
	private string
}

func (store *codexReceiptStore) CreateConfigurationAsset(ctx context.Context, owner string, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	store.calls.Add(1)
	entry, err := store.Store.CreateConfigurationAsset(ctx, owner, draft)
	if err != nil {
		return entry, err
	}
	return entry, errors.New(store.private)
}

func (store *codexReceiptStore) UpdateConfigurationAsset(ctx context.Context, owner, id string, revision int64, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	store.calls.Add(1)
	entry, err := store.Store.UpdateConfigurationAsset(ctx, owner, id, revision, draft)
	if err != nil {
		return entry, err
	}
	return entry, errors.New(store.private)
}

func (store *codexReceiptStore) DeleteConfigurationAsset(ctx context.Context, owner, id string, revision int64) error {
	store.calls.Add(1)
	if err := store.Store.DeleteConfigurationAsset(ctx, owner, id, revision); err != nil {
		return err
	}
	return errors.New(store.private)
}

func TestCodexConfigurationLoggingAndUnknownReceipt(test *testing.T) {
	for _, format := range []string{"text", "json"} {
		test.Run(format, func(test *testing.T) {
			logs := configurationCaptureLogs(test, format)
			for _, encoding := range []string{"proto", "json"} {
				test.Run(encoding, func(test *testing.T) {
					service, store := testServiceAndStore(test)
					owner, session := mcpSession(test, service, "codex-private-logs")
					private := []string{session, "codex-logging-replacement", "codex-logging-base-instructions", "codex-untrusted-internal-error", "codex-invalid-session"}
					test.Cleanup(func() {
						for _, value := range private {
							if logs.contains(value) {
								test.Error("HTTP logged private Codex data")
							}
						}
					})
					var options []connect.ClientOption
					if encoding == "json" {
						options = append(options, connect.WithProtoJSON())
					}
					draft := codexConfigurationDraft()
					draft.GetCodexProvider().Catalog.Models[0].BaseInstructions = new(private[2])
					responses := &configurationPrivacyBuffer{}
					endpoint := configurationPrivacyServer(test, NewWithOptions(service, Options{Configuration: configuration.New(store)}), logs, responses)
					client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
					created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "codex.provider-key", Action: &configurationv1.SecretMutation_Replace{Replace: private[1]}}}}, session))
					if err != nil {
						test.Fatalf("private create: %s", connect.CodeOf(err))
					}
					id := created.Msg.View.Asset.Id
					codexAssertStoredKey(test, store, owner, id, private[1])
					encoded, _ := protojson.Marshal(created.Msg)
					if strings.Contains(string(encoded), private[1]) || responses.contains(private[1]) || responses.contains(session) {
						test.Fatal("successful response disclosed credential")
					}
					_, err = client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, private[4]))
					if connect.CodeOf(err) != connect.CodeUnauthenticated {
						test.Fatal("invalid session accepted")
					}
					for _, operation := range []string{"create", "copy", "update", "delete"} {
						test.Run(operation, func(test *testing.T) {
							before, err := store.ListConfigurationAssets(test.Context(), owner)
							if err != nil {
								test.Fatal("read real pre-operation inventory")
							}
							fault := &codexReceiptStore{Store: store, private: private[3]}
							faultEndpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(fault)}))
							test.Cleanup(faultEndpoint.Close)
							faultClient := configurationv1connect.NewConfigurationServiceClient(faultEndpoint.Client(), faultEndpoint.URL, options...)
							var callErr error
							switch operation {
							case "create":
								_, callErr = faultClient.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, session))
							case "copy":
								_, callErr = faultClient.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 1, Name: "Receipt copy"}, session))
							case "update":
								_, callErr = faultClient.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, session))
							case "delete":
								_, callErr = faultClient.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: id, ExpectedRevision: 2}, session))
							}
							if connect.CodeOf(callErr) != connect.CodeInternal || fault.calls.Load() != 1 {
								test.Fatal("unknown receipt produced success or write replay")
							}
							for _, value := range private {
								if strings.Contains(callErr.Error(), value) {
									test.Fatal("unknown receipt echoed private context")
								}
							}
							after, err := store.ListConfigurationAssets(test.Context(), owner)
							if err != nil {
								test.Fatal("read real committed inventory")
							}
							switch operation {
							case "create", "copy":
								if len(after) != len(before)+1 {
									test.Fatal("lost-receipt fixture did not really commit")
								}
							case "update":
								metadata, err := store.ConfigurationAsset(test.Context(), owner, id)
								if err != nil || metadata.Revision != 2 {
									test.Fatal("lost update receipt did not commit revision")
								}
							case "delete":
								if len(after) != len(before)-1 {
									test.Fatal("lost delete receipt did not commit")
								}
							}
						})
					}
					status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", []byte(strings.Replace(codexConfigurationJSON, `"replace":"protocol-synthetic-key"`, `"replace":"`+private[1]+`","untrusted":"`+private[3]+`"`, 1)))
					if status != 400 {
						test.Fatal("private malformed request accepted")
					}
					for _, value := range private {
						if strings.Contains(body, value) {
							test.Fatal("malformed response leaked private value")
						}
					}
				})
			}
			for _, control := range []string{"privacy-stdlog-capture-control", "privacy-slog-capture-control", "privacy-http-capture-control"} {
				if !logs.contains(control) {
					test.Fatal("privacy capture control missing")
				}
			}
		})
	}
}
