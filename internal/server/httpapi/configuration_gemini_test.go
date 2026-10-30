package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
	"modernc.org/sqlite"
)

func geminiConfigurationDraft() *configurationv1.Draft {
	return &configurationv1.Draft{Name: "Gemini fixture", Payload: &configurationv1.Draft_GeminiProvider{GeminiProvider: &configurationv1.GeminiProvider{AuthMode: configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_NATIVE_LOGIN}}}
}

func geminiAPIConfigurationDraft() *configurationv1.Draft {
	return &configurationv1.Draft{Name: "Gemini API fixture", Payload: &configurationv1.Draft_GeminiProvider{GeminiProvider: &configurationv1.GeminiProvider{
		AuthMode: configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_API_KEY, KeyTransport: new(configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_BEARER), Endpoint: new("https://private-gemini.example.test/v1"), EnvModel: new("private-gemini-env"), SettingsModel: new("private-gemini-settings"),
	}}}
}

func TestGeminiConfigurationMappingIsolation(test *testing.T) {
	wire := geminiAPIConfigurationDraft()
	input, owned, err := configurationInput(wire, nil)
	defer configurationClearSecrets(owned)
	if err != nil || input.GeminiProvider == nil {
		test.Fatal("valid Gemini input rejected")
	}
	provider := input.GeminiProvider
	if provider.Endpoint == wire.GetGeminiProvider().Endpoint || provider.EnvModel == wire.GetGeminiProvider().EnvModel || provider.SettingsModel == wire.GetGeminiProvider().SettingsModel {
		test.Fatal("input mapper retained caller optional pointers")
	}
	*wire.GetGeminiProvider().Endpoint = "https://changed.example.test"
	*wire.GetGeminiProvider().EnvModel = "changed-env"
	*wire.GetGeminiProvider().SettingsModel = "changed-settings"
	*wire.GetGeminiProvider().KeyTransport = configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_NATIVE_DEFAULT
	if *provider.Endpoint != "https://private-gemini.example.test/v1" || *provider.EnvModel != "private-gemini-env" || *provider.SettingsModel != "private-gemini-settings" || *provider.KeyTransport != agentconfig.GeminiBearer {
		test.Fatal("caller mutation altered mapped Gemini provider")
	}
	entry := configuration.EditView{Metadata: state.ConfigurationAsset{ID: "gemini-map", AgentID: "gemini", Kind: state.ConfigurationKindProvider, SchemaVersion: 1, Name: "Gemini map", Revision: 1}, GeminiProvider: provider, Secrets: []configuration.SecretStatus{{Slot: configuration.GeminiAPIKeySlot, Present: true}}, Readiness: configuration.CredentialComplete, HasPreservedExtras: true}
	view, err := configurationView(entry)
	if err != nil || !view.HasPreservedExtras || !view.Secrets[0].Present {
		test.Fatal("valid Gemini view lost status or preserved-extras marker")
	}
	projected := view.Draft.GetGeminiProvider()
	*projected.Endpoint = "https://view-changed.example.test"
	*projected.EnvModel = "view-env"
	*projected.SettingsModel = "view-settings"
	*projected.KeyTransport = configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_NATIVE_DEFAULT
	if *provider.Endpoint != "https://private-gemini.example.test/v1" || *provider.EnvModel != "private-gemini-env" || *provider.SettingsModel != "private-gemini-settings" || *provider.KeyTransport != agentconfig.GeminiBearer {
		test.Fatal("view mutation altered domain provider")
	}
	for index, change := range []func(*configuration.EditView){
		func(entry *configuration.EditView) { entry.Metadata.AgentID = "claude-code" },
		func(entry *configuration.EditView) { entry.Metadata.Kind = state.ConfigurationKindPrompt },
		func(entry *configuration.EditView) { entry.Metadata.SchemaVersion = 2 },
		func(entry *configuration.EditView) { entry.Metadata.ID = "" },
		func(entry *configuration.EditView) { entry.Metadata.Name = "" },
		func(entry *configuration.EditView) { entry.Metadata.Revision = 0 },
		func(entry *configuration.EditView) { entry.GeminiProvider = nil },
		func(entry *configuration.EditView) { entry.CodexProvider = &configuration.CodexProvider{} },
		func(entry *configuration.EditView) { entry.Provider.AuthMode = configuration.NativeLogin },
		func(entry *configuration.EditView) { entry.Secrets = nil },
		func(entry *configuration.EditView) {
			entry.Secrets = append(entry.Secrets, configuration.SecretStatus{Slot: configuration.GeminiAPIKeySlot})
		},
		func(entry *configuration.EditView) {
			entry.Secrets = []configuration.SecretStatus{{Slot: configuration.APIKeySlot, Present: true}}
		},
		func(entry *configuration.EditView) { entry.Readiness = configuration.NeedsCredential },
		func(entry *configuration.EditView) {
			entry.Secrets = []configuration.SecretStatus{{Slot: configuration.GeminiAPIKeySlot}}
		},
	} {
		malformed := entry
		change(&malformed)
		if result, err := configurationView(malformed); !errors.Is(err, configuration.ErrUnavailable) || result != nil {
			test.Errorf("malformed domain view became usable: case %d", index)
		}
	}
	for index, draft := range []*configurationv1.Draft{
		nil,
		{},
		{Payload: (*configurationv1.Draft_GeminiProvider)(nil)},
		{Payload: &configurationv1.Draft_GeminiProvider{}},
		{Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: &configurationv1.ClaudeProvider{}}},
	} {
		if _, owned, err := configurationGeminiInput(draft, nil); !errors.Is(err, configuration.ErrInvalid) || owned != nil {
			configurationClearSecrets(owned)
			test.Errorf("invalid Gemini payload mapped: case %d", index)
		}
	}
}

func TestGeminiConfigurationConcurrentCAS(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			accounts, store := testServiceAndStore(test)
			owner, session := mcpSession(test, accounts, "gemini-http-cas")
			endpoint := httptest.NewServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
			defer endpoint.Close()
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiAPIConfigurationDraft()}, session))
			if err != nil {
				test.Fatal("create actual Gemini CAS fixture")
			}
			type result struct {
				index int
				view  *configurationv1.DraftView
				err   error
			}
			start := make(chan struct{})
			results := make(chan result, 2)
			keys := []string{"synthetic-gemini-cas-left", "synthetic-gemini-cas-right"}
			models := []string{"gemini-cas-left", "gemini-cas-right"}
			var workers sync.WaitGroup
			for index := range 2 {
				workers.Go(func() {
					<-start
					draft := geminiAPIConfigurationDraft()
					draft.GetGeminiProvider().EnvModel = new(models[index])
					response, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: created.Msg.View.Asset.Id, ExpectedRevision: 1, Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: keys[index]}}}}, session))
					entry := result{index: index, err: err}
					if response != nil {
						entry.view = response.Msg.View
					}
					results <- entry
				})
			}
			close(start)
			workers.Wait()
			wins, conflicts, winner := 0, 0, -1
			for range 2 {
				entry := codexAwait(test, results)
				if entry.err == nil && entry.view != nil && entry.view.Asset.Revision == 2 {
					wins++
					winner = entry.index
				} else if connect.CodeOf(entry.err) == connect.CodeAborted && entry.view == nil {
					conflicts++
				} else {
					test.Fatalf("Gemini CAS writer: %s", connect.CodeOf(entry.err))
				}
			}
			if wins != 1 || conflicts != 1 {
				test.Fatal("same-revision Gemini writers did not have exactly one winner")
			}
			read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: created.Msg.View.Asset.Id}, session))
			if err != nil || read.Msg.View.Asset.Revision != 2 || read.Msg.View.Draft.GetGeminiProvider().GetEnvModel() != models[winner] {
				test.Fatal("Gemini CAS rebased or combined different snapshots")
			}
			configurationAssertStoredSecret(test, store, owner, created.Msg.View.Asset.Id, 2, keys[winner])
		})
	}
}

type geminiAfterCommitStore struct {
	*state.Store
	cancel    context.CancelFunc
	calls     atomic.Int32
	committed chan struct{}
	exited    chan error
}

func (store *geminiAfterCommitStore) CreateConfigurationAsset(ctx context.Context, owner string, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	store.calls.Add(1)
	entry, err := store.Store.CreateConfigurationAsset(ctx, owner, draft)
	if err != nil {
		store.exited <- err
		return state.ConfigurationAsset{}, err
	}
	store.committed <- struct{}{}
	store.cancel()
	select {
	case <-ctx.Done():
		store.exited <- ctx.Err()
		return entry, nil
	case <-time.After(5 * time.Second):
		store.exited <- errors.New("fixture cancellation was not observed")
		return state.ConfigurationAsset{}, errors.New("fixture cancellation was not observed")
	}
}

func TestGeminiConfigurationAfterCommitCancellation(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			accounts, store := testServiceAndStore(test)
			owner, session := mcpSession(test, accounts, "gemini-after-commit")
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
				_, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiAPIConfigurationDraft(), Secrets: []*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-postcommit-key"}}}}, session))
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

func TestGeminiConfigurationLimitsCancelAndUnknown(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			service, store := testServiceAndStore(test)
			owner, session := mcpSession(test, service, "gemini-admission")
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			blocking := &codexAdmissionStore{Store: store, entered: make(chan context.Context, 16), exited: make(chan error, 16), release: release}
			endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(blocking)}))
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
					_, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiConfigurationDraft()}, session))
					results <- err
				})
			}
			for range 8 {
				incoming := codexAwait(test, blocking.entered)
				deadline, ok := incoming.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 20*time.Second {
					test.Fatal("missing 20s server bound")
				}
			}
			ninthCtx, stopNinth := context.WithTimeout(test.Context(), time.Second)
			defer stopNinth()
			if _, err := client.CreateAsset(ninthCtx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiConfigurationDraft()}, session)); connect.CodeOf(err) != connect.CodeResourceExhausted {
				test.Fatal("ninth Gemini write was not immediately rejected")
			}
			cancel()
			workers.Wait()
			for range 8 {
				if connect.CodeOf(codexAwait(test, results)) != connect.CodeCanceled || !errors.Is(codexAwait(test, blocking.exited), context.Canceled) {
					test.Fatal("cancellation did not drain server write")
				}
			}
			entries, err := store.ListConfigurationAssets(test.Context(), owner)
			if err != nil || len(entries) != 0 {
				test.Fatal("canceled writes committed")
			}
			once.Do(func() { close(release) })
			_, err = client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiConfigurationDraft()}, session))
			if err != nil {
				test.Fatalf("capacity did not recover after cancellation: %s", connect.CodeOf(err))
			}
		})
	}
	test.Run("json-budget", func(test *testing.T) {
		service, store := testServiceAndStore(test)
		_, session := mcpSession(test, service, "gemini-size")
		endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
		defer endpoint.Close()
		data := []byte(geminiConfigurationJSON + strings.Repeat(" ", (128<<10)-len(geminiConfigurationJSON)))
		status, _ := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", data)
		if status != http.StatusOK {
			test.Fatal("exact 128KiB Gemini JSON request failed")
		}
		status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", append(data, ' '))
		if status < 400 || status >= 500 || strings.Contains(body, "synthetic-gemini-protocol-key") {
			test.Fatal("oversize Gemini request escaped bounded private rejection")
		}
		var compressed bytes.Buffer
		compressor := gzip.NewWriter(&compressed)
		if _, err := compressor.Write(append(data, ' ')); err != nil {
			test.Fatal("compress oversize fixture")
		}
		if compressor.Close() != nil {
			test.Fatal("finish gzip fixture")
		}
		request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, endpoint.URL+configurationv1connect.ConfigurationServiceCreateAssetProcedure, &compressed)
		if err != nil {
			test.Fatal("construct compressed fixture")
		}
		request.Header.Set("Authorization", "Bearer "+session)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Content-Encoding", "gzip")
		response, err := endpoint.Client().Do(request)
		if err != nil {
			test.Fatal("compressed HTTP request failed")
		}
		defer response.Body.Close()
		bodyBytes, err := io.ReadAll(io.LimitReader(response.Body, 4097))
		if err != nil || response.StatusCode < 400 || response.StatusCode >= 500 || len(bodyBytes) > 4096 || bytes.Contains(bodyBytes, []byte("synthetic-gemini-protocol-key")) {
			test.Fatal("gzip bypassed decompressed request budget or private error bound")
		}
	})
}

func TestGeminiConfigurationSQLiteInTransactionCancel(test *testing.T) {
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
			name := "http_gemini_cancel_" + strings.ToLower(rand.Text())
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
			owner, session := mcpSession(test, service, "gemini-tx-cancel")
			if _, err := database.ExecContext(test.Context(), "CREATE TRIGGER http_gemini_cancel BEFORE INSERT ON configuration_assets BEGIN SELECT "+name+"(); END"); err != nil {
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
				_, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiConfigurationDraft()}, session))
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
			if _, err := database.ExecContext(test.Context(), "DROP TRIGGER http_gemini_cancel"); err != nil {
				test.Fatal("release transaction fixture")
			}
		})
	}
}

type geminiOversizeResponse struct {
	configurationv1connect.UnimplementedConfigurationServiceHandler
}

func (geminiOversizeResponse) ReadDraft(context.Context, *connect.Request[configurationv1.ReadDraftRequest]) (*connect.Response[configurationv1.ReadDraftResponse], error) {
	draft := geminiConfigurationDraft()
	draft.GetGeminiProvider().EnvModel = new(strings.Repeat("private-gemini-response-marker", 1<<16))
	return connect.NewResponse(&configurationv1.ReadDraftResponse{View: &configurationv1.DraftView{Draft: draft}}), nil
}

func TestGeminiConfigurationResponseBudget(test *testing.T) {
	_, handler := configurationTransport(geminiOversizeResponse{})
	endpoint := httptest.NewServer(handler)
	defer endpoint.Close()
	for _, options := range [][]connect.ClientOption{nil, {connect.WithProtoJSON()}} {
		client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
		_, err := client.ReadDraft(test.Context(), connect.NewRequest(&configurationv1.ReadDraftRequest{}))
		if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "private-gemini-response-marker") {
			test.Fatal("Gemini response bypassed uncompressed limit or exposed private body")
		}
	}
}

func TestGeminiConfigurationOwnerLifecycleAndCAS(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			directory := test.TempDir()
			if err := os.Chmod(directory, 0700); err != nil {
				test.Fatal("private fixture setup failed")
			}
			key := bytes.Repeat([]byte{42}, 32)
			store, err := state.Open(test.Context(), filepath.Join(directory, "gemini.db"), key)
			if err != nil {
				test.Fatal("synthetic store setup failed")
			}
			test.Cleanup(func() { _ = store.Close() })
			accounts, err := auth.NewService(store, key)
			if err != nil {
				test.Fatal("synthetic auth setup failed")
			}
			_, session := mcpSession(test, accounts, "gemini-owner")
			_, foreign := mcpSession(test, accounts, "gemini-foreign")
			server := httptest.NewServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
			test.Cleanup(server.Close)
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(server.Client(), server.URL, options...)
			draft := &configurationv1.Draft{Name: "Gemini fixture", Payload: &configurationv1.Draft_GeminiProvider{GeminiProvider: &configurationv1.GeminiProvider{AuthMode: configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_NATIVE_LOGIN}}}
			created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, session))
			if err != nil {
				test.Fatalf("Gemini native-login create rejected: %s", connect.CodeOf(err))
			}
			view := created.Msg.View
			if view == nil || view.Asset == nil || view.Asset.AgentId != "gemini" || view.Asset.SchemaVersion != 1 || !proto.Equal(view.Draft, draft) || view.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE || view.HasPreservedExtras {
				test.Fatal("native-login typed view lost its declared semantics")
			}
			id := view.Asset.Id
			read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, session))
			if err != nil || !proto.Equal(read.Msg.View, view) {
				test.Fatal("typed draft did not round trip")
			}
			metadata, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: id}, session))
			if err != nil || !proto.Equal(metadata.Msg.Asset, view.Asset) {
				test.Fatal("metadata read lost its identity")
			}
			listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
			if err != nil || len(listed.Msg.Assets) != 1 || listed.Msg.Assets[0].Id != id {
				test.Fatal("owner list lost its private asset")
			}
			for _, operation := range []func(string) error{
				func(token string) error {
					_, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: id}, token))
					return err
				},
				func(token string) error {
					_, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, token))
					return err
				},
				func(token string) error {
					_, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, token))
					return err
				},
				func(token string) error {
					_, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 1, Name: "Unauthorized copy"}, token))
					return err
				},
				func(token string) error {
					_, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: id, ExpectedRevision: 1}, token))
					return err
				},
			} {
				if connect.CodeOf(operation(foreign)) != connect.CodeNotFound || connect.CodeOf(operation("")) != connect.CodeUnauthenticated {
					test.Fatal("foreign or anonymous caller crossed the owner boundary")
				}
			}
			foreignList, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, foreign))
			if err != nil || len(foreignList.Msg.Assets) != 0 {
				test.Fatal("foreign list disclosed an asset")
			}
			apiDraft := &configurationv1.Draft{Name: "Gemini API fixture", Payload: &configurationv1.Draft_GeminiProvider{GeminiProvider: &configurationv1.GeminiProvider{
				AuthMode: configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_API_KEY, KeyTransport: new(configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_NATIVE_DEFAULT), EnvModel: new("env-model"), SettingsModel: new("settings-model"),
			}}}
			changed, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: apiDraft}, session))
			if err != nil || changed.Msg.View.Asset.Revision != 2 || !proto.Equal(changed.Msg.View.Draft, apiDraft) || changed.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL || len(changed.Msg.View.Secrets) != 1 || changed.Msg.View.Secrets[0].Present {
				test.Fatal("API-key mode silently filled a credential or lost independent model values")
			}
			const syntheticKey = "synthetic-gemini-http-credential-19"
			complete, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 2, Draft: apiDraft, Secrets: []*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: syntheticKey}}}}, session))
			if err != nil || complete.Msg.View.Asset.Revision != 3 || !complete.Msg.View.Secrets[0].Present || complete.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE {
				test.Fatal("credential replacement did not produce a complete private asset")
			}
			encoded, err := proto.Marshal(complete.Msg.View)
			if err != nil || bytes.Contains(encoded, []byte(syntheticKey)) {
				test.Fatal("a read view exposed replacement material")
			}
			_, err = client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, session))
			if connect.CodeOf(err) != connect.CodeAborted {
				test.Fatal("stale update did not reject its revision")
			}
			_, err = client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 1, Name: "Stale copy"}, session))
			if connect.CodeOf(err) != connect.CodeAborted {
				test.Fatal("stale copy did not reject its source revision")
			}
			copied, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 3, Name: "Copy without credential"}, session))
			if err != nil || copied.Msg.View.Asset.Id == id || copied.Msg.View.Asset.Revision != 1 || copied.Msg.View.Secrets[0].Present || copied.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL || !proto.Equal(copied.Msg.View.Draft.GetGeminiProvider(), apiDraft.GetGeminiProvider()) {
				test.Fatal("copy inherited a credential or lost its typed provider")
			}
			_, err = client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 3, Draft: draft}, session))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				test.Fatal("native-login transition silently removed a stored credential")
			}
			unchanged, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, session))
			if err != nil || unchanged.Msg.View.Asset.Revision != 3 || !unchanged.Msg.View.Secrets[0].Present || !proto.Equal(unchanged.Msg.View.Draft, apiDraft) {
				test.Fatal("refused transition changed stored state")
			}
			cleared, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 3, Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}}}}, session))
			if err != nil || cleared.Msg.View.Asset.Revision != 4 || cleared.Msg.View.Secrets[0].Present || cleared.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE || !proto.Equal(cleared.Msg.View.Draft, draft) {
				test.Fatal("explicit same-CAS CLEAR did not complete the native-login transition")
			}
			server.Close()
			if err := store.Close(); err != nil {
				test.Fatal("close actual SQLite before restart")
			}
			store, err = state.Open(test.Context(), filepath.Join(directory, "gemini.db"), key)
			if err != nil {
				test.Fatal("reopen actual SQLite after restart")
			}
			accounts, err = auth.NewService(store, key)
			if err != nil {
				test.Fatal("reopen authentication after restart")
			}
			server = httptest.NewServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
			test.Cleanup(server.Close)
			client = configurationv1connect.NewConfigurationServiceClient(server.Client(), server.URL, options...)
			for _, expected := range []*configurationv1.DraftView{cleared.Msg.View, copied.Msg.View} {
				restored, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: expected.Asset.Id}, session))
				if err != nil || !proto.Equal(restored.Msg.View, expected) {
					test.Fatal("real server restart changed a stored Gemini view or session")
				}
			}
			for _, asset := range []*configurationv1.Asset{cleared.Msg.View.Asset, copied.Msg.View.Asset} {
				if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: asset.Id, ExpectedRevision: asset.Revision}, session)); err != nil {
					test.Fatal("owner delete failed")
				}
			}
			listed, err = client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
			if err != nil || len(listed.Msg.Assets) != 0 {
				test.Fatal("delete left private assets visible")
			}
		})
	}
}

func TestGeminiConfigurationStoredExtrasAndFutureMetadata(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			accounts, store := testServiceAndStore(test)
			owner, session := mcpSession(test, accounts, "gemini-http-extras")
			endpoint := httptest.NewServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
			defer endpoint.Close()
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiAPIConfigurationDraft()}, session))
			if err != nil {
				test.Fatal("create Gemini extras fixture")
			}
			id := created.Msg.View.Asset.Id
			metadata, payload, err := store.ReadConfigurationAssetVersion(test.Context(), owner, id)
			if err != nil {
				test.Fatal("read encrypted Gemini fixture")
			}
			data := payload.Reveal()
			defer clear(data)
			withExtras := append(bytes.Clone(data[:len(data)-1]), []byte(`,"future":"private-gemini-extra"}`)...)
			defer clear(withExtras)
			_, err = store.UpdateConfigurationAsset(test.Context(), owner, id, 1, state.ConfigurationAssetDraft{AgentID: metadata.AgentID, Kind: metadata.Kind, Name: metadata.Name, SchemaVersion: metadata.SchemaVersion, Payload: state.NewConfigurationPayload(withExtras)})
			if err != nil {
				test.Fatal("persist encrypted Gemini unknown field")
			}
			read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, session))
			if err != nil || !read.Msg.View.HasPreservedExtras || !proto.Equal(read.Msg.View.Draft, geminiAPIConfigurationDraft()) {
				test.Fatal("Gemini read lost preserved-extras projection")
			}
			encoded, err := proto.Marshal(read.Msg)
			if err != nil || bytes.Contains(encoded, []byte("private-gemini-extra")) {
				test.Fatal("Gemini read exposed unknown stored data")
			}
			if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 2, Draft: geminiAPIConfigurationDraft()}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("Gemini update discarded stored extras")
			}
			if _, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 2, Name: "Forbidden Gemini extras copy"}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("Gemini copy discarded stored extras")
			}
			metadata, payload, err = store.ReadConfigurationAssetVersion(test.Context(), owner, id)
			actual := payload.Reveal()
			defer clear(actual)
			if err != nil || metadata.Revision != 2 || !bytes.Equal(actual, withExtras) {
				test.Fatal("refused Gemini operation altered source bytes")
			}
			for _, agent := range []string{"gemini", "future-agent"} {
				future, err := store.CreateConfigurationAsset(test.Context(), owner, state.ConfigurationAssetDraft{AgentID: agent, Kind: state.ConfigurationKindProvider, Name: "Future metadata", SchemaVersion: 2, Payload: state.NewConfigurationPayload([]byte(`{}`))})
				if err != nil {
					test.Fatal("persist future metadata")
				}
				metadata, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: future.ID}, session))
				if err != nil || metadata.Msg.Asset.AgentId != agent || metadata.Msg.Asset.SchemaVersion != 2 {
					test.Fatal("future metadata cannot be browsed")
				}
				if _, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: future.ID}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
					test.Fatal("future metadata became an editable Gemini draft")
				}
				if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: future.ID, ExpectedRevision: 1}, session)); err != nil {
					test.Fatal("future metadata cannot be deleted with owner CAS")
				}
			}
			if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: id, ExpectedRevision: 2}, session)); err != nil {
				test.Fatal("preserved-extras asset cannot be deleted with owner CAS")
			}
		})
	}
}
