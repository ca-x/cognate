package httpapi

import (
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
)

func TestGeminiConfigurationLoggingAndUnknownReceipt(test *testing.T) {
	for _, format := range []string{"text", "json"} {
		test.Run(format, func(test *testing.T) {
			logs := configurationCaptureLogs(test, format)
			for _, encoding := range []string{"proto", "json"} {
				test.Run(encoding, func(test *testing.T) {
					accounts, store := testServiceAndStore(test)
					owner, session := mcpSession(test, accounts, "gemini-private-logs")
					private := []string{session, "synthetic-gemini-log-key", "synthetic-gemini-internal-error", "synthetic-gemini-invalid-session", "private-gemini.example.test", "private-gemini-env", "private-gemini-settings"}
					test.Cleanup(func() {
						for _, value := range private {
							if logs.contains(value) {
								test.Error("HTTP logged private Gemini data")
							}
						}
					})
					var options []connect.ClientOption
					if encoding == "json" {
						options = append(options, connect.WithProtoJSON())
					}
					responses := &configurationPrivacyBuffer{}
					endpoint := configurationPrivacyServer(test, NewWithOptions(accounts, Options{Configuration: configuration.New(store)}), logs, responses)
					client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
					created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiAPIConfigurationDraft(), Secrets: []*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: private[1]}}}}, session))
					if err != nil {
						test.Fatalf("private Gemini create: %s", connect.CodeOf(err))
					}
					id := created.Msg.View.Asset.Id
					configurationAssertStoredSecret(test, store, owner, id, 1, private[1])
					if responses.contains(private[1]) || responses.contains(session) {
						test.Fatal("successful Gemini response disclosed credentials")
					}
					_, err = client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, private[3]))
					if connect.CodeOf(err) != connect.CodeUnauthenticated {
						test.Fatal("invalid Gemini session accepted")
					}
					for _, operation := range []string{"create", "copy", "update", "delete"} {
						test.Run(operation, func(test *testing.T) {
							before, err := store.ListConfigurationAssets(test.Context(), owner)
							if err != nil {
								test.Fatal("read pre-operation inventory")
							}
							fault := &codexReceiptStore{Store: store, private: strings.Join(private, "|")}
							faultResponses := &configurationPrivacyBuffer{}
							faultEndpoint := configurationPrivacyServer(test, NewWithOptions(accounts, Options{Configuration: configuration.New(fault)}), logs, faultResponses)
							faultClient := configurationv1connect.NewConfigurationServiceClient(faultEndpoint.Client(), faultEndpoint.URL, options...)
							var callErr error
							switch operation {
							case "create":
								_, callErr = faultClient.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiAPIConfigurationDraft()}, session))
							case "copy":
								_, callErr = faultClient.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 1, Name: "Gemini unknown receipt"}, session))
							case "update":
								_, callErr = faultClient.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: geminiAPIConfigurationDraft(), Secrets: []*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Keep{Keep: &configurationv1.KeepSecret{}}}}}, session))
							case "delete":
								_, callErr = faultClient.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: id, ExpectedRevision: 2}, session))
							}
							if connect.CodeOf(callErr) != connect.CodeInternal || fault.calls.Load() != 1 {
								test.Fatal("unknown Gemini receipt returned success or replayed a write")
							}
							for _, value := range private {
								if strings.Contains(callErr.Error(), value) || faultResponses.contains(value) {
									test.Fatal("unknown Gemini receipt disclosed private context")
								}
							}
							after, err := store.ListConfigurationAssets(test.Context(), owner)
							if err != nil {
								test.Fatal("read post-operation inventory")
							}
							switch operation {
							case "create", "copy":
								if len(after) != len(before)+1 {
									test.Fatal("lost receipt did not really commit a new row")
								}
							case "update":
								configurationAssertStoredSecret(test, store, owner, id, 2, private[1])
							case "delete":
								if len(after) != len(before)-1 {
									test.Fatal("lost delete receipt did not really commit")
								}
								if _, err := store.ConfigurationAsset(test.Context(), owner, id); err == nil {
									test.Fatal("deleted Gemini row remained readable")
								}
							}
						})
					}
					fresh, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiAPIConfigurationDraft(), Secrets: []*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: private[1]}}}}, session))
					if err != nil {
						test.Fatal("create after unknown receipt failed")
					}
					for index, action := range []*configurationv1.SecretMutation{
						{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Keep{Keep: &configurationv1.KeepSecret{}}},
						{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}},
					} {
						updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: fresh.Msg.View.Asset.Id, ExpectedRevision: int64(index + 1), Draft: geminiAPIConfigurationDraft(), Secrets: []*configurationv1.SecretMutation{action}}, session))
						if err != nil || updated.Msg.View.Secrets[0].Present != (index == 0) {
							test.Fatal("successful Gemini KEEP/CLEAR lost its status")
						}
						wanted := ""
						if index == 0 {
							wanted = private[1]
						}
						configurationAssertStoredSecret(test, store, owner, fresh.Msg.View.Asset.Id, int64(index+2), wanted)
					}
					malformed := strings.Replace(geminiConfigurationJSON, `"replace":"synthetic-gemini-protocol-key"`, `"replace":"`+private[1]+`","untrusted":"`+private[2]+`"`, 1)
					status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", []byte(malformed))
					if status != 400 {
						test.Fatal("private malformed Gemini request accepted")
					}
					for _, value := range private {
						if strings.Contains(body, value) {
							test.Fatal("malformed Gemini response disclosed private data")
						}
					}
					if responses.contains(private[1]) || responses.contains(session) {
						test.Fatal("Gemini response stream disclosed credentials")
					}
					unsafeDTO := (&configurationv1.CreateAssetRequest{Draft: geminiAPIConfigurationDraft(), Secrets: []*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: private[1]}}}}).String()
					if !strings.Contains(unsafeDTO, private[1]) || !strings.Contains(unsafeDTO, private[4]) {
						test.Fatal("raw generated DTO sensitivity positive witness missing")
					}
				})
			}
			for _, witness := range []string{"privacy-stdlog-capture-control", "privacy-slog-capture-control", "privacy-http-capture-control"} {
				if !logs.contains(witness) {
					test.Fatal("privacy capture positive witness missing")
				}
			}
		})
	}
}

func TestConfigurationHTTPMutationOwnership(test *testing.T) {
	input := []*configurationv1.SecretMutation{
		{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-http-owned-key"}},
		{SlotId: "claude.auth-token", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-http-owned-auth"}},
	}
	owned, err := configurationSecretInput(input)
	if err != nil || len(owned) != 2 {
		test.Fatal("valid replacements failed to map")
	}
	for _, mutation := range owned {
		if reflect.ValueOf(mutation.Replacement).IsZero() {
			test.Fatal("positive owned replacement witness missing")
		}
	}
	configurationClearSecrets(owned)
	configurationClearSecrets(owned)
	configurationClearSecrets(nil)
	for _, mutation := range owned {
		if !reflect.ValueOf(mutation.Replacement).IsZero() {
			test.Error("owned replacement handle survived cleanup")
		}
	}
	if input[0].GetReplace() != "synthetic-http-owned-key" || input[1].GetReplace() != "synthetic-http-owned-auth" {
		test.Fatal("cleanup mutated the caller's immutable wire input")
	}
}

func TestGeminiConfigurationFailureLoggingPrivacy(test *testing.T) {
	for _, format := range []string{"text", "json"} {
		test.Run(format, func(test *testing.T) {
			logs := configurationCaptureLogs(test, format)
			test.Run("admission-and-request-budget", TestGeminiConfigurationLimitsCancelAndUnknown)
			test.Run("response-budget", TestGeminiConfigurationResponseBudget)
			test.Run("before-commit-cancellation", TestGeminiConfigurationSQLiteInTransactionCancel)
			test.Run("after-commit-cancellation", TestGeminiConfigurationAfterCommitCancellation)
			for _, marker := range []string{"synthetic-gemini-protocol-key", "synthetic-postcommit-key", "private-gemini-response-marker", "private-gemini.example.test", "private-gemini-env", "private-gemini-settings", "Bearer "} {
				if logs.contains(marker) {
					test.Error("Gemini rejection or cancellation logged private data")
				}
			}
		})
	}
}

func TestConfigurationMutationNilActions(test *testing.T) {
	for index, malformed := range []*configurationv1.SecretMutation{
		nil,
		{SlotId: "gemini.api-key"},
		{SlotId: "gemini.api-key", Action: (*configurationv1.SecretMutation_Keep)(nil)},
		{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Keep{}},
		{SlotId: "gemini.api-key", Action: (*configurationv1.SecretMutation_Clear)(nil)},
		{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Clear{}},
		{SlotId: "gemini.api-key", Action: (*configurationv1.SecretMutation_Replace)(nil)},
	} {
		func() {
			defer func() {
				if recover() != nil {
					test.Errorf("malformed action panicked: case %d", index)
				}
			}()
			result, err := configurationSecretInput([]*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-before-invalid"}}, malformed})
			if err == nil || result != nil {
				test.Errorf("malformed action returned an owned result: case %d", index)
			}
		}()
	}
}
