package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
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
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
	"modernc.org/sqlite"
)

func codexConfigurationDraft() *configurationv1.Draft {
	return &configurationv1.Draft{Name: "Codex fixture", Payload: &configurationv1.Draft_CodexProvider{CodexProvider: &configurationv1.CodexProvider{
		AuthMode: configurationv1.CodexAuthMode_CODEX_AUTH_MODE_PROVIDER_KEY, Endpoint: "https://api.example.test/v1", Model: "model-b",
		ReasoningEffort: new(configurationv1.CodexReasoningEffort_CODEX_REASONING_EFFORT_LOW),
		Catalog: &configurationv1.CodexCatalog{Models: []*configurationv1.CodexCatalogModel{
			{Model: "model-b", DisplayName: new("Private model"), ContextWindow: new(uint64(200000)), SupportsParallelToolCalls: new(false),
				InputModalities:  &configurationv1.CodexModalities{Values: []configurationv1.CodexModality{configurationv1.CodexModality_CODEX_MODALITY_IMAGE, configurationv1.CodexModality_CODEX_MODALITY_TEXT}},
				BaseInstructions: new("private instructions\nwith a tab\there"), ReasoningLevels: &configurationv1.CodexReasoningLevels{Values: []configurationv1.CodexReasoningEffort{configurationv1.CodexReasoningEffort_CODEX_REASONING_EFFORT_HIGH, configurationv1.CodexReasoningEffort_CODEX_REASONING_EFFORT_LOW}},
				DefaultReasoningLevel: new(configurationv1.CodexReasoningEffort_CODEX_REASONING_EFFORT_LOW)},
			{Model: "model-a"},
		}},
	}}}
}

func TestCodexConfigurationLifecycle(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			key := []byte(strings.Repeat("k", 32))
			directory := test.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				test.Fatal("private fixture directory")
			}
			path := filepath.Join(directory, "codex.db")
			store, err := state.Open(test.Context(), path, key)
			if err != nil {
				test.Fatal("open synthetic SQLite fixture")
			}
			test.Cleanup(func() { _ = store.Close() })
			service, err := auth.NewService(store, key)
			if err != nil {
				test.Fatal("open synthetic auth fixture")
			}
			owner, session := mcpSession(test, service, "codex-owner")
			_, foreign := mcpSession(test, service, "codex-foreign")
			endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
			test.Cleanup(endpoint.Close)
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			draft := codexConfigurationDraft()
			created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, session))
			if err != nil {
				test.Fatalf("Codex create: %s", connect.CodeOf(err))
			}
			view := created.Msg.View
			if !proto.Equal(view.Draft, draft) || view.Asset.AgentId != "codex" || len(view.Secrets) != 1 || view.Secrets[0].SlotId != "codex.provider-key" || view.Secrets[0].Present || view.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL {
				test.Fatal("Codex typed presence/order or canonical credential status lost")
			}
			id := view.Asset.Id
			operations := []struct {
				name string
				call func(string) error
			}{
				{"get", func(token string) error {
					_, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: id}, token))
					return err
				}},
				{"read", func(token string) error {
					_, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, token))
					return err
				}},
				{"update", func(token string) error {
					_, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, token))
					return err
				}},
				{"copy", func(token string) error {
					_, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 1, Name: "Forbidden"}, token))
					return err
				}},
				{"delete", func(token string) error {
					_, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: id, ExpectedRevision: 1}, token))
					return err
				}},
			}
			for _, operation := range operations {
				test.Run(operation.name, func(test *testing.T) {
					if connect.CodeOf(operation.call("")) != connect.CodeUnauthenticated || connect.CodeOf(operation.call(foreign)) != connect.CodeNotFound {
						test.Fatal("owner/session gate failed")
					}
				})
			}
			listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, foreign))
			if err != nil || len(listed.Msg.Assets) != 0 {
				test.Fatal("foreign list leaked metadata")
			}
			mutations := []*configurationv1.SecretMutation{
				{SlotId: "codex.provider-key", Action: &configurationv1.SecretMutation_Replace{Replace: "codex-only-synthetic-replacement"}},
				{SlotId: "codex.provider-key", Action: &configurationv1.SecretMutation_Keep{Keep: &configurationv1.KeepSecret{}}},
				nil,
				{SlotId: "codex.provider-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}},
			}
			for index, mutation := range mutations {
				var secrets []*configurationv1.SecretMutation
				if mutation != nil {
					secrets = []*configurationv1.SecretMutation{mutation}
				}
				updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: int64(index + 1), Draft: draft, Secrets: secrets}, session))
				if err != nil {
					test.Fatalf("update %d: %s", index, connect.CodeOf(err))
				}
				view = updated.Msg.View
				if !proto.Equal(view.Draft, draft) || view.Asset.Revision != int64(index+2) || len(view.Secrets) != 1 || view.Secrets[0].SlotId != "codex.provider-key" || view.Secrets[0].Present != (index < 3) {
					test.Fatal("typed update changed presence/order or credential semantics")
				}
				wanted := "codex-only-synthetic-replacement"
				if index == 3 {
					wanted = ""
				}
				codexAssertStoredKey(test, store, owner, id, wanted)
				if index == 1 {
					copied, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 3, Name: "Copy without key"}, session))
					if err != nil {
						test.Fatalf("copy: %s", connect.CodeOf(err))
					}
					if copied.Msg.View.Secrets[0].Present || copied.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL {
						test.Fatal("copy retained credential")
					}
					codexAssertStoredKey(test, store, owner, copied.Msg.View.Asset.Id, "")
				}
			}
			if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, session)); connect.CodeOf(err) != connect.CodeAborted {
				test.Fatal("stale update did not conflict")
			}
			if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: id, ExpectedRevision: 1}, session)); connect.CodeOf(err) != connect.CodeAborted {
				test.Fatal("stale delete did not conflict")
			}
			endpoint.Close()
			if err := store.Close(); err != nil {
				test.Fatal("close SQLite fixture")
			}
			store, err = state.Open(test.Context(), path, key)
			if err != nil {
				test.Fatal("reopen SQLite fixture")
			}
			service, err = auth.NewService(store, key)
			if err != nil {
				test.Fatal("reopen auth fixture")
			}
			reopened := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
			test.Cleanup(reopened.Close)
			client = configurationv1connect.NewConfigurationServiceClient(reopened.Client(), reopened.URL, options...)
			read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, session))
			if err != nil || !proto.Equal(read.Msg.View, view) {
				test.Fatal("SQLite reopen lost typed draft/revision/credential status")
			}
			if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: id, ExpectedRevision: 5}, session)); err != nil {
				test.Fatalf("delete: %s", connect.CodeOf(err))
			}
			if _, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, session)); connect.CodeOf(err) != connect.CodeNotFound {
				test.Fatal("deleted asset remained readable")
			}
		})
	}
}

func codexAssertStoredKey(test *testing.T, store *state.Store, owner, id, wanted string) {
	test.Helper()
	_, payload, err := store.ReadConfigurationAssetVersion(test.Context(), owner, id)
	if err != nil {
		test.Fatal("read encrypted synthetic payload")
	}
	data := payload.Reveal()
	defer clear(data)
	var stored struct {
		Secrets struct {
			ProviderKey *string `json:"provider_key"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		test.Fatal("decode synthetic encrypted fixture")
	}
	if wanted == "" && stored.Secrets.ProviderKey != nil || wanted != "" && (stored.Secrets.ProviderKey == nil || *stored.Secrets.ProviderKey != wanted) {
		test.Fatal("stored credential does not match mutation/copy contract")
	}
}

type codexAdmissionStore struct {
	*state.Store
	entered chan context.Context
	exited  chan error
	release <-chan struct{}
}

func (store *codexAdmissionStore) CreateConfigurationAsset(ctx context.Context, owner string, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	store.entered <- ctx
	if store.release != nil {
		select {
		case <-ctx.Done():
			store.exited <- ctx.Err()
			return state.ConfigurationAsset{}, ctx.Err()
		case <-store.release:
		}
	}
	entry, err := store.Store.CreateConfigurationAsset(ctx, owner, draft)
	store.exited <- err
	return entry, err
}

func codexAwait[Value any](test *testing.T, channel <-chan Value) Value {
	test.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		test.Fatal("bounded fixture event missing")
		var zero Value
		return zero
	}
}

func TestCodexConfigurationLimitsAndCancel(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			service, store := testServiceAndStore(test)
			owner, session := mcpSession(test, service, "codex-admission")
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
					_, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: codexConfigurationDraft()}, session))
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
			if _, err := client.CreateAsset(ninthCtx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: codexConfigurationDraft()}, session)); connect.CodeOf(err) != connect.CodeResourceExhausted {
				test.Fatal("ninth Codex write was not immediately rejected")
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
			_, err = client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: codexConfigurationDraft()}, session))
			if err != nil {
				test.Fatalf("capacity did not recover after cancellation: %s", connect.CodeOf(err))
			}
		})
	}
	test.Run("json-budget", func(test *testing.T) {
		service, store := testServiceAndStore(test)
		_, session := mcpSession(test, service, "codex-size")
		endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
		defer endpoint.Close()
		data := []byte(codexConfigurationJSON + strings.Repeat(" ", (128<<10)-len(codexConfigurationJSON)))
		status, _ := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", data)
		if status != http.StatusOK {
			test.Fatal("exact 128KiB Codex JSON request failed")
		}
		status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", append(data, ' '))
		if status < 400 || status >= 500 || strings.Contains(body, "protocol-synthetic-key") {
			test.Fatal("oversize Codex request escaped bounded private rejection")
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
		if err != nil || response.StatusCode < 400 || response.StatusCode >= 500 || len(bodyBytes) > 4096 || bytes.Contains(bodyBytes, []byte("protocol-synthetic-key")) {
			test.Fatal("gzip bypassed decompressed request budget or private error bound")
		}
	})
}

func TestCodexConfigurationConcurrentCAS(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			service, store := testServiceAndStore(test)
			_, session := mcpSession(test, service, "codex-http-cas")
			endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
			defer endpoint.Close()
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: codexConfigurationDraft()}, session))
			if err != nil {
				test.Fatal("create CAS fixture")
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			var workers sync.WaitGroup
			for range 2 {
				workers.Go(func() {
					<-start
					_, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: created.Msg.View.Asset.Id, ExpectedRevision: 1, Draft: codexConfigurationDraft()}, session))
					results <- err
				})
			}
			close(start)
			workers.Wait()
			wins, conflicts := 0, 0
			for range 2 {
				err := codexAwait(test, results)
				if err == nil {
					wins++
				} else if connect.CodeOf(err) == connect.CodeAborted {
					conflicts++
				} else {
					test.Fatalf("CAS writer: %s", connect.CodeOf(err))
				}
			}
			if wins != 1 || conflicts != 1 {
				test.Fatal("same-revision Codex writers did not have exactly one winner")
			}
			read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: created.Msg.View.Asset.Id}, session))
			if err != nil || read.Msg.View.Asset.Revision != 2 {
				test.Fatal("CAS revision drifted or rebased")
			}
		})
	}
}

func TestCodexConfigurationSQLiteInTransactionCancel(test *testing.T) {
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
			name := "http_cancel_" + strings.ToLower(rand.Text())
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
			owner, session := mcpSession(test, service, "codex-tx-cancel")
			if _, err := database.ExecContext(test.Context(), "CREATE TRIGGER http_cancel BEFORE INSERT ON configuration_assets BEGIN SELECT "+name+"(); END"); err != nil {
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
				_, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: codexConfigurationDraft()}, session))
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
			if _, err := database.ExecContext(test.Context(), "DROP TRIGGER http_cancel"); err != nil {
				test.Fatal("release transaction fixture")
			}
		})
	}
}

func TestCodexConfigurationCloneAndMetadata(test *testing.T) {
	draft := codexConfigurationDraft()
	input, _, err := configurationInput(draft, nil)
	if err != nil {
		test.Fatal("typed input rejected")
	}
	*input.CodexProvider.Catalog.Models[0].SupportsParallelToolCalls = true
	*input.CodexProvider.Catalog.Models[0].DisplayName = "changed"
	(*input.CodexProvider.Catalog.Models[0].InputModalities)[0] = "text"
	if *draft.GetCodexProvider().Catalog.Models[0].SupportsParallelToolCalls || *draft.GetCodexProvider().Catalog.Models[0].DisplayName != "Private model" || draft.GetCodexProvider().Catalog.Models[0].InputModalities.Values[0] != configurationv1.CodexModality_CODEX_MODALITY_IMAGE {
		test.Fatal("input aliases caller optional values or ordered arrays")
	}
	service, store := testServiceAndStore(test)
	owner, _ := mcpSession(test, service, "codex-clone")
	input, _, err = configurationInput(codexConfigurationDraft(), nil)
	if err != nil {
		test.Fatal("typed fixture rejected")
	}
	entry, err := configuration.New(store).Create(test.Context(), owner, input, nil)
	if err != nil {
		test.Fatal("real domain create failed")
	}
	view, err := configurationView(entry)
	if err != nil {
		test.Fatal("typed output rejected")
	}
	*view.Draft.GetCodexProvider().Catalog.Models[0].SupportsParallelToolCalls = true
	*view.Draft.GetCodexProvider().Catalog.Models[0].DisplayName = "changed"
	view.Draft.GetCodexProvider().Catalog.Models[0].ReasoningLevels.Values[0] = configurationv1.CodexReasoningEffort_CODEX_REASONING_EFFORT_LOW
	if *entry.CodexProvider.Catalog.Models[0].SupportsParallelToolCalls || *entry.CodexProvider.Catalog.Models[0].DisplayName != "Private model" || (*entry.CodexProvider.Catalog.Models[0].ReasoningLevels)[0] != "high" {
		test.Fatal("output aliases private domain optional values or ordered arrays")
	}
	for _, damage := range []func(*configuration.EditView){
		func(value *configuration.EditView) { value.Metadata.AgentID = "claude" },
		func(value *configuration.EditView) { value.Metadata.Kind = state.ConfigurationKindMCP },
		func(value *configuration.EditView) { value.Metadata.SchemaVersion = 2 },
		func(value *configuration.EditView) { value.CodexProvider = nil },
		func(value *configuration.EditView) { value.Readiness = configuration.CredentialComplete },
		func(value *configuration.EditView) { value.Secrets = nil },
		func(value *configuration.EditView) {
			value.Secrets = []configuration.SecretStatus{{Slot: configuration.APIKeySlot}}
		},
		func(value *configuration.EditView) {
			value.Secrets = append(value.Secrets, configuration.SecretStatus{Slot: configuration.CodexProviderKeySlot})
		},
	} {
		copy := entry
		damage(&copy)
		if result, err := configurationView(copy); !errors.Is(err, configuration.ErrUnavailable) || result != nil {
			test.Fatal("inconsistent variant metadata/status was exposed")
		}
	}
}

func TestCodexConfigurationStoredExtrasAndFutureMetadata(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			service, store := testServiceAndStore(test)
			owner, session := mcpSession(test, service, "codex-http-extras")
			endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
			defer endpoint.Close()
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: codexConfigurationDraft()}, session))
			if err != nil {
				test.Fatal("create extras fixture")
			}
			id := created.Msg.View.Asset.Id
			metadata, payload, err := store.ReadConfigurationAssetVersion(test.Context(), owner, id)
			if err != nil {
				test.Fatal("read actual encrypted fixture")
			}
			data := payload.Reveal()
			defer clear(data)
			withExtras := append(bytes.Clone(data[:len(data)-1]), []byte(`,"future":null}`)...)
			defer clear(withExtras)
			_, err = store.UpdateConfigurationAsset(test.Context(), owner, id, 1, state.ConfigurationAssetDraft{AgentID: metadata.AgentID, Kind: metadata.Kind, Name: metadata.Name, SchemaVersion: metadata.SchemaVersion, Payload: state.NewConfigurationPayload(withExtras)})
			if err != nil {
				test.Fatal("persist encrypted future field")
			}
			read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, session))
			if err != nil || !read.Msg.View.HasPreservedExtras || !proto.Equal(read.Msg.View.Draft, codexConfigurationDraft()) {
				test.Fatal("HTTP lost read-only preserved-extras projection")
			}
			if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 2, Draft: codexConfigurationDraft()}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("update discarded unknown stored fields")
			}
			if _, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 2, Name: "Forbidden extras copy"}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("copy silently discarded unknown stored fields")
			}
			metadata, payload, err = store.ReadConfigurationAssetVersion(test.Context(), owner, id)
			if err != nil || metadata.Revision != 2 || !bytes.Equal(payload.Reveal(), withExtras) {
				test.Fatal("rejected operation changed encrypted source projection")
			}
			future, err := store.CreateConfigurationAsset(test.Context(), owner, state.ConfigurationAssetDraft{AgentID: "future-agent", Kind: state.ConfigurationKindProvider, Name: "Future metadata only", SchemaVersion: 2, Payload: state.NewConfigurationPayload([]byte(`{}`))})
			if err != nil {
				test.Fatal("persist future metadata fixture")
			}
			listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
			if err != nil || len(listed.Msg.Assets) != 2 {
				test.Fatal("unknown-agent metadata broke listing")
			}
			if _, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: future.ID}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("unknown agent was guessed as supported typed draft")
			}
			if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: id, ExpectedRevision: 2}, session)); err != nil {
				test.Fatal("owner could not delete read-only extras asset")
			}
		})
	}
}

type codexOversizeResponse struct {
	configurationv1connect.UnimplementedConfigurationServiceHandler
}

func (codexOversizeResponse) ReadDraft(context.Context, *connect.Request[configurationv1.ReadDraftRequest]) (*connect.Response[configurationv1.ReadDraftResponse], error) {
	draft := codexConfigurationDraft()
	draft.GetCodexProvider().Catalog.Models[0].BaseInstructions = new(strings.Repeat("private-response-marker", (1<<20)/20))
	return connect.NewResponse(&configurationv1.ReadDraftResponse{View: &configurationv1.DraftView{Draft: draft}}), nil
}

func TestCodexConfigurationResponseBudget(test *testing.T) {
	_, handler := configurationTransport(codexOversizeResponse{})
	endpoint := httptest.NewServer(handler)
	defer endpoint.Close()
	for _, options := range [][]connect.ClientOption{nil, {connect.WithProtoJSON()}} {
		client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
		_, err := client.ReadDraft(test.Context(), connect.NewRequest(&configurationv1.ReadDraftRequest{}))
		if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "private-response-marker") {
			test.Fatal("Codex response bypassed uncompressed 1MiB limit or exposed private body")
		}
	}
}
