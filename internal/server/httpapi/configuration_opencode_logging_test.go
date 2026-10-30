package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/logging"
	"github.com/ca-x/cognate/internal/server/state"
	"modernc.org/sqlite"
)

func TestOpenCodeConfigurationLoggingPrivacyAndUnknown(test *testing.T) {
	for _, logFormat := range []string{"text", "json"} {
		test.Run(logFormat, func(test *testing.T) {
			captured := configurationCaptureLogs(test, logFormat)
			for _, encoding := range []string{"proto", "json"} {
				test.Run(encoding, func(test *testing.T) {
					accounts, store := testServiceAndStore(test)
					owner, session := mcpSession(test, accounts, "opencode-logging")
					directory := test.TempDir()
					if os.Chmod(directory, 0o700) != nil {
						test.Fatal("private logger fixture")
					}
					manager, err := logging.Open(test.Context(), directory)
					if err != nil {
						test.Fatal("actual production logging manager failed")
					}
					defer manager.Close()
					responses := &configurationPrivacyBuffer{}
					endpoint := configurationPrivacyServer(test, NewWithOptions(accounts, Options{Configuration: configuration.New(store), Logging: manager}), captured, responses)
					defer endpoint.Close()
					var options []connect.ClientOption
					if encoding == "json" {
						options = append(options, connect.WithProtoJSON())
					}
					client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
					private := []string{session, "synthetic-opencode-private-key", "synthetic-opencode-private-header", "synthetic-opencode-private-error", "private-opencode-endpoint.invalid"}
					draft := openCodeConfigurationDraft("v1")
					draft.GetOpencodeProvider().GetV1().Options.BaseUrl = new("https://" + private[4] + "/v1")
					created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "opencode.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: private[1]}}, {SlotId: "opencode.value.01", Action: &configurationv1.SecretMutation_Replace{Replace: private[2]}}}}, session))
					if err != nil {
						test.Fatal("logged OpenCode create failed")
					}
					id := created.Msg.View.Asset.Id
					metadata, payload, err := store.ReadConfigurationAssetVersion(test.Context(), owner, id)
					if err != nil || metadata.Revision != 1 || !bytes.Contains(payload.Reveal(), []byte(private[1])) || !bytes.Contains(payload.Reveal(), []byte(private[2])) {
						test.Fatal("real SQLite did not persist both encrypted credentials")
					}
					if _, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session)); err != nil {
						test.Fatal(err)
					}
					if _, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: id}, session)); err != nil {
						test.Fatal(err)
					}
					if _, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, session)); err != nil {
						test.Fatal(err)
					}
					if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, session)); err != nil {
						test.Fatal(err)
					}
					copied, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 2, Name: "copy"}, session))
					if err != nil || copied.Msg.View.Secrets[0].Present || copied.Msg.View.Secrets[1].Present {
						test.Fatal("logged copy retained key")
					}
					if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: copied.Msg.View.Asset.Id, ExpectedRevision: 1}, session)); err != nil {
						test.Fatal(err)
					}
					for _, operation := range []string{"create", "copy", "update", "delete"} {
						before, err := store.ListConfigurationAssets(test.Context(), owner)
						if err != nil {
							test.Fatal(err)
						}
						fault := &codexReceiptStore{Store: store, private: strings.Join(private, "|")}
						faultEndpoint := configurationPrivacyServer(test, NewWithOptions(accounts, Options{Configuration: configuration.New(fault), Logging: manager}), captured, responses)
						faultClient := configurationv1connect.NewConfigurationServiceClient(faultEndpoint.Client(), faultEndpoint.URL, options...)
						var failure error
						expectedCount := len(before)
						switch operation {
						case "create":
							_, failure = faultClient.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, session))
							expectedCount++
						case "copy":
							_, failure = faultClient.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 2, Name: "unknown copy"}, session))
							expectedCount++
						case "update":
							_, failure = faultClient.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 2, Draft: draft}, session))
						case "delete":
							_, failure = faultClient.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: id, ExpectedRevision: 3}, session))
							expectedCount--
						}
						faultEndpoint.Close()
						if connect.CodeOf(failure) != connect.CodeInternal || fault.calls.Load() != 1 {
							test.Fatal("unknown durable receipt returned success/replayed")
						}
						for _, marker := range private {
							if strings.Contains(failure.Error(), marker) {
								test.Fatal("durable unknown leaked private input")
							}
						}
						after, err := store.ListConfigurationAssets(test.Context(), owner)
						if err != nil || len(after) != expectedCount {
							test.Fatal("unknown receipt misrepresented durable side effects")
						}
					}
					endpoint.Close()
					if manager.Close() != nil {
						test.Fatal("real logger did not close")
					}
					segments, err := filepath.Glob(filepath.Join(directory, "logs", "segment-*.jsonl"))
					if err != nil || len(segments) == 0 {
						test.Fatal("actual named logger had no records")
					}
					var records []byte
					for _, segment := range segments {
						data, err := os.ReadFile(segment)
						if err != nil {
							test.Fatal(err)
						}
						records = append(records, data...)
					}
					if bytes.Count(records, []byte(`"msg":"request_completed"`)) < 11 {
						test.Fatal("real logger not exercised by complete RPC/unknown sequence")
					}
					for _, marker := range private {
						if bytes.Contains(records, []byte(marker)) || captured.contains(marker) {
							test.Fatal("OpenCode private value appeared in actual/std/slog/HTTP logs")
						}
					}
					for _, marker := range private[:4] {
						if responses.contains(marker) {
							test.Fatal("HTTP response contained credentials/private error")
						}
					}
				})
			}
		})
	}
}

func TestOpenCodeConfigurationCancelDrainAndTail(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			accounts, store := testServiceAndStore(test)
			owner, session := mcpSession(test, accounts, "opencode-cancel")
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			blocked := &codexAdmissionStore{Store: store, entered: make(chan context.Context, 16), exited: make(chan error, 16), release: release}
			endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(blocked)}))
			defer endpoint.Close()
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			results := make(chan error, 8)
			var workers sync.WaitGroup
			for range 8 {
				workers.Go(func() {
					_, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: openCodeConfigurationDraft("v2")}, session))
					results <- err
				})
			}
			for range 8 {
				serverCtx := codexAwait(test, blocked.entered)
				deadline, ok := serverCtx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 20*time.Second {
					test.Fatal("missing server timeout")
				}
			}
			if _, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: openCodeConfigurationDraft("v2")}, session)); connect.CodeOf(err) != connect.CodeResourceExhausted {
				test.Fatal("ninth admission was not rejected")
			}
			cancel()
			workers.Wait()
			for range 8 {
				if connect.CodeOf(codexAwait(test, results)) != connect.CodeCanceled || !errors.Is(codexAwait(test, blocked.exited), context.Canceled) {
					test.Fatal("cancel did not join actual server Store entry")
				}
			}
			rows, err := store.ListConfigurationAssets(test.Context(), owner)
			if err != nil || len(rows) != 0 {
				test.Fatal("precommit cancellation persisted")
			}
			once.Do(func() { close(release) })
			if _, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: openCodeConfigurationDraft("v2")}, session)); err != nil {
				test.Fatal("cancel failed to release admission capacity")
			}
		})
	}
}

func TestOpenCodeConfigurationSQLiteInTransactionCancel(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			directory := test.TempDir()
			if os.Chmod(directory, 0o700) != nil {
				test.Fatal("private SQLite fixture directory")
			}
			path := filepath.Join(directory, "cancel.db")
			key := []byte(strings.Repeat("q", 32))
			address := url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/"), RawQuery: url.Values{"_pragma": {"busy_timeout(10000)"}}.Encode()}
			database, err := sql.Open("sqlite3", address.String())
			if err != nil {
				test.Fatal("open pure-Go SQL driver")
			}
			defer database.Close()
			registrar, ok := database.Driver().(interface {
				RegisterScalarFunction(string, int32, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error)) error
			})
			if !ok {
				test.Fatal("actual SQLite driver does not expose scalar registration")
			}
			name := "http_opencode_cancel_" + strings.ToLower(rand.Text())
			entered, release := make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			if registrar.RegisterScalarFunction(name, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
				entered <- struct{}{}
				<-release
				return nil, nil
			}) != nil {
				test.Fatal("register real SQLite transaction barrier")
			}
			store, err := state.Open(test.Context(), path, key)
			if err != nil {
				test.Fatal("open real Store")
			}
			defer store.Close()
			service, err := auth.NewService(store, key)
			if err != nil {
				test.Fatal("open auth")
			}
			owner, session := mcpSession(test, service, "opencode-tx-cancel")
			if _, err := database.ExecContext(test.Context(), "CREATE TRIGGER http_opencode_cancel BEFORE INSERT ON configuration_assets BEGIN SELECT "+name+"(); END"); err != nil {
				test.Fatal("create real insert barrier")
			}
			witness := &codexAdmissionStore{Store: store, entered: make(chan context.Context, 1), exited: make(chan error, 1)}
			endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(witness)}))
			defer func() { once.Do(func() { close(release) }); endpoint.Close() }()
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: openCodeConfigurationDraft("v2")}, session))
				result <- err
			}()
			serverCtx := codexAwait(test, witness.entered)
			codexAwait(test, entered)
			cancel()
			codexAwait(test, serverCtx.Done())
			once.Do(func() { close(release) })
			if connect.CodeOf(codexAwait(test, result)) != connect.CodeCanceled {
				test.Fatal("HTTP cancellation did not reject result")
			}
			if codexAwait(test, witness.exited) == nil {
				test.Fatal("in-flight real Store transaction reported success after cancellation")
			}
			entries, err := store.ListConfigurationAssets(test.Context(), owner)
			if err != nil || len(entries) != 0 {
				test.Fatal("canceled in-flight transaction persisted an asset")
			}
			if _, err := database.ExecContext(test.Context(), "DROP TRIGGER http_opencode_cancel"); err != nil {
				test.Fatal("release transaction fixture")
			}
		})
	}
}

func TestOpenCodeConfigurationAfterCommitCancellation(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			accounts, store := testServiceAndStore(test)
			owner, session := mcpSession(test, accounts, "opencode-after-commit")
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			probe := &geminiAfterCommitStore{Store: store, cancel: cancel, committed: make(chan struct{}, 1), exited: make(chan error, 1)}
			endpoint := httptest.NewServer(NewWithOptions(accounts, Options{Configuration: configuration.New(probe)}))
			defer endpoint.Close()
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			result := make(chan error, 1)
			go func() {
				_, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: openCodeConfigurationDraft("v2"), Secrets: []*configurationv1.SecretMutation{{SlotId: "opencode.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-postcommit-key"}}}}, session))
				result <- err
			}()
			codexAwait(test, probe.committed)
			if connect.CodeOf(codexAwait(test, result)) != connect.CodeCanceled || !errors.Is(codexAwait(test, probe.exited), context.Canceled) || probe.calls.Load() != 1 {
				test.Fatal("post-commit cancellation returned success, replayed, or left its worker live")
			}
			entries, err := store.ListConfigurationAssets(test.Context(), owner)
			if err != nil || len(entries) != 1 || entries[0].Revision != 1 {
				test.Fatal("post-commit cancellation erased or replayed a committed row")
			}
			view, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: entries[0].ID}, session))
			if err != nil || !view.Msg.View.Secrets[0].Present || view.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE {
				test.Fatal("new authenticated read did not resolve the real committed state")
			}
		})
	}
}
