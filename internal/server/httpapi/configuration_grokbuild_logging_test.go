package httpapi

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestGrokBuildConfigurationLoggingAndUnknownReceipt(test *testing.T) {
	for _, format := range []string{"text", "json"} {
		test.Run(format, func(test *testing.T) {
			logs := configurationCaptureLogs(test, format)
			for _, encoding := range []string{"proto", "json"} {
				test.Run(encoding, func(test *testing.T) {
					accounts, store := testServiceAndStore(test)
					owner, session := mcpSession(test, accounts, "grok-privacy")
					private := []string{session, "synthetic-grok-private-key", "synthetic-grok-private-error", "synthetic-grok-private-name"}
					test.Cleanup(func() {
						for _, value := range private {
							if logs.contains(value) {
								test.Error("private Grok data reached logs")
							}
						}
					})
					var options []connect.ClientOption
					if encoding == "json" {
						options = append(options, connect.WithProtoJSON())
					}
					draft := grokBuildConfigurationDraft(nil)
					draft.Name = private[3]
					responses := &configurationPrivacyBuffer{}
					endpoint := configurationPrivacyServer(test, NewWithOptions(accounts, Options{Configuration: configuration.New(store)}), logs, responses)
					client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
					created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "grokbuild.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: private[1]}}}}, session))
					if err != nil {
						test.Fatalf("private create: %s", connect.CodeOf(err))
					}
					id := created.Msg.View.Asset.Id
					configurationAssertStoredSecret(test, store, owner, id, 1, private[1])
					encoded, err := protojson.Marshal(created.Msg)
					if err != nil || strings.Contains(string(encoded), private[1]) || responses.contains(private[1]) || responses.contains(session) {
						test.Fatal("successful response disclosed credential")
					}
					for _, operation := range []string{"create", "copy", "update", "delete"} {
						test.Run(operation, func(test *testing.T) {
							before, err := store.ListConfigurationAssets(test.Context(), owner)
							if err != nil {
								test.Fatal("cannot read pre-operation inventory")
							}
							fault := &codexReceiptStore{Store: store, private: private[2]}
							faultResponses := &configurationPrivacyBuffer{}
							faultEndpoint := configurationPrivacyServer(test, NewWithOptions(accounts, Options{Configuration: configuration.New(fault)}), logs, faultResponses)
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
								test.Fatal("lost receipt returned success or replayed write")
							}
							for _, value := range private {
								if strings.Contains(callErr.Error(), value) || faultResponses.contains(value) {
									test.Fatal("lost receipt disclosed private context")
								}
							}
							after, err := store.ListConfigurationAssets(test.Context(), owner)
							if err != nil {
								test.Fatal("cannot read committed inventory")
							}
							switch operation {
							case "create", "copy":
								if len(after) != len(before)+1 {
									test.Fatal("lost receipt did not actually commit exactly once")
								}
							case "update":
								metadata, err := store.ConfigurationAsset(test.Context(), owner, id)
								if err != nil || metadata.Revision != 2 {
									test.Fatal("lost update did not commit revision")
								}
							case "delete":
								if len(after) != len(before)-1 {
									test.Fatal("lost delete did not commit")
								}
							}
						})
					}
				})
			}
			for _, control := range []string{"privacy-stdlog-capture-control", "privacy-slog-capture-control", "privacy-http-capture-control"} {
				if !logs.contains(control) {
					test.Fatal("privacy capture did not observe control")
				}
			}
		})
	}
}
