package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/labstack/echo/v4"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestConfigurationOrderHTTP(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			directory := test.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			key := bytes.Repeat([]byte{67}, 32)
			store, err := state.Open(test.Context(), filepath.Join(directory, "state.db"), key)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = store.Close() })
			service, err := auth.NewService(store, key)
			if err != nil {
				test.Fatal(err)
			}
			owner, session := mcpSession(test, service, "order-owner")
			_, foreign := mcpSession(test, service, "order-foreign")
			endpoint := httptest.NewTLSServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
			test.Cleanup(endpoint.Close)
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			const secret = "synthetic-order-credential-never-in-metadata"
			first, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: codexConfigurationDraft(), Secrets: []*configurationv1.SecretMutation{{SlotId: "codex.provider-key", Action: &configurationv1.SecretMutation_Replace{Replace: secret}}}}, session))
			if err != nil {
				test.Fatal(err)
			}
			second, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: first.Msg.View.Asset.Id, ExpectedSourceRevision: 1, Name: "copied metadata no key"}, session))
			if err != nil {
				test.Fatal(err)
			}
			if len(second.Msg.View.Secrets) != 1 || second.Msg.View.Secrets[0].Present {
				test.Fatal("Copy carried credential into new asset")
			}
			get := &configurationv1.GetAssetOrderRequest{AgentId: "codex", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER}
			before, err := client.GetAssetOrder(test.Context(), configurationRequest(get, session))
			if err != nil || before.Msg.Snapshot.Revision != 2 || len(before.Msg.Snapshot.Assets) != 2 || before.Msg.Snapshot.Assets[0].Id != first.Msg.View.Asset.Id {
				test.Fatalf("real TLS ordered metadata: %v", err)
			}
			replace := &configurationv1.ReplaceAssetOrderRequest{AgentId: "codex", Kind: get.Kind, ExpectedRevision: 2, AssetIds: []string{second.Msg.View.Asset.Id, first.Msg.View.Asset.Id}}
			after, err := client.ReplaceAssetOrder(test.Context(), configurationRequest(replace, session))
			if err != nil || after.Msg.Snapshot.Revision != 3 || !proto.Equal(after.Msg.Snapshot.Assets[0], second.Msg.View.Asset) || !proto.Equal(after.Msg.Snapshot.Assets[1], first.Msg.View.Asset) {
				test.Fatalf("real TLS reorder changed asset metadata: %v", err)
			}
			encoded, err := proto.Marshal(after.Msg)
			if err != nil || bytes.Contains(encoded, []byte(secret)) || bytes.Contains(encoded, []byte("codex.provider-key")) {
				test.Fatal("order snapshot exposed credential material or inventory")
			}
			if _, err := client.ReplaceAssetOrder(test.Context(), configurationRequest(replace, session)); connect.CodeOf(err) != connect.CodeAborted {
				test.Fatalf("stale CAS: %v", err)
			}
			if _, err := client.ReplaceAssetOrder(test.Context(), configurationRequest(replace, foreign)); connect.CodeOf(err) != connect.CodeAborted {
				test.Fatalf("foreign owner IDs accepted: %v", err)
			}
			if _, err := client.GetAssetOrder(test.Context(), configurationRequest(get, "")); connect.CodeOf(err) != connect.CodeUnauthenticated {
				test.Fatalf("anonymous read: %v", err)
			}
			foreignList, err := client.GetAssetOrder(test.Context(), configurationRequest(get, foreign))
			if err != nil || len(foreignList.Msg.Snapshot.Assets) != 0 {
				test.Fatalf("foreign metadata leak: %v", err)
			}
			list, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
			if err != nil || !proto.Equal(list.Msg.Assets[0], second.Msg.View.Asset) {
				test.Fatalf("legacy List lost order: %v", err)
			}
			durable, err := store.ConfigurationAssetOrder(test.Context(), owner, "codex", state.ConfigurationKindProvider)
			if err != nil || durable.Revision != 3 || durable.Assets[0].ID != replace.AssetIds[0] {
				test.Fatalf("wire not actually persisted: %v", err)
			}
			canceled, cancel := context.WithCancel(test.Context())
			cancel()
			if _, err := client.ReplaceAssetOrder(canceled, configurationRequest(replace, session)); connect.CodeOf(err) != connect.CodeCanceled {
				test.Fatalf("canceled wire accepted: %v", err)
			}
			badOrigin := configurationRequest(get, session)
			badOrigin.Header().Set("Origin", "https://foreign.example.test")
			if _, err := client.GetAssetOrder(test.Context(), badOrigin); err == nil {
				test.Fatal("cross-origin order read accepted")
			}
			if err := service.Logout(test.Context(), session); err != nil {
				test.Fatal(err)
			}
			if _, err := client.GetAssetOrder(test.Context(), configurationRequest(get, session)); connect.CodeOf(err) != connect.CodeUnauthenticated {
				test.Fatalf("revoked session accepted: %v", err)
			}
		})
	}
}

type configurationOrderLossTransport struct {
	base     http.RoundTripper
	armed    *atomic.Bool
	attempts *atomic.Int64
}

func (transport configurationOrderLossTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if request.URL.Path == configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure {
		transport.attempts.Add(1)
		if response.StatusCode == http.StatusOK && transport.armed.Swap(false) {
			_, readError := io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if readError != nil {
				return nil, readError
			}
			return nil, errors.New("synthetic committed order response lost")
		}
	}
	return response, nil
}

func TestConfigurationOrderHTTPUnknownNoReplay(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			store, owner, token, server := configurationOrderHTTPFixture(test)
			var armed atomic.Bool
			var attempts atomic.Int64
			httpClient := server.Client()
			httpClient.Transport = configurationOrderLossTransport{base: httpClient.Transport, armed: &armed, attempts: &attempts}
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(httpClient, server.URL, options...)
			first, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: codexConfigurationDraft()}, token))
			if err != nil {
				test.Fatal(err)
			}
			second, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: first.Msg.View.Asset.Id, ExpectedSourceRevision: 1, Name: "copy"}, token))
			if err != nil {
				test.Fatal(err)
			}
			request := &configurationv1.ReplaceAssetOrderRequest{AgentId: "codex", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, ExpectedRevision: 2, AssetIds: []string{second.Msg.View.Asset.Id, first.Msg.View.Asset.Id}}
			armed.Store(true)
			result, err := client.ReplaceAssetOrder(test.Context(), configurationRequest(request, token))
			if err == nil || result != nil || attempts.Load() != 1 || armed.Load() {
				test.Fatalf("lost committed receipt reported success/replayed: attempts%d error%v", attempts.Load(), err)
			}
			durable, err := store.ConfigurationAssetOrder(test.Context(), owner, "codex", state.ConfigurationKindProvider)
			if err != nil || durable.Revision != 3 || durable.Assets[0].ID != request.AssetIds[0] {
				test.Fatalf("loss was not after actual commit: %+v %v", durable, err)
			}
			read, err := client.GetAssetOrder(test.Context(), configurationRequest(&configurationv1.GetAssetOrderRequest{AgentId: "codex", Kind: request.Kind}, token))
			if err != nil || read.Msg.Snapshot.Revision != 3 || read.Msg.Snapshot.Assets[0].Id != request.AssetIds[0] || attempts.Load() != 1 {
				test.Fatalf("Unknown must reconcile by read, not replay: %v", err)
			}
		})
	}
}

func configurationOrderHTTPFixture(test *testing.T) (*state.Store, string, string, *httptest.Server) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{86}, 32)
	store, err := state.Open(test.Context(), filepath.Join(directory, "order.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	service, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	owner, token := mcpSession(test, service, "order-wire-owner")
	server := httptest.NewTLSServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
	test.Cleanup(server.Close)
	return store, owner, token, server
}

func TestConfigurationOrderStrictWire(test *testing.T) {
	store, owner, token, server := configurationOrderHTTPFixture(test)
	valid := `{"agentId":"codex","kind":1,"expectedRevision":"0","assetIds":[]}`
	cases := []struct {
		name, encoding string
		data           []byte
	}{
		{"null-ids", "json", []byte(strings.Replace(valid, `"assetIds":[]`, `"assetIds":null`, 1))},
		{"null-revision", "json", []byte(strings.Replace(valid, `"expectedRevision":"0"`, `"expectedRevision":null`, 1))},
		{"null-kind", "json", []byte(strings.Replace(valid, `"kind":1`, `"kind":null`, 1))},
		{"unknown", "json", []byte(strings.Replace(valid, `{`, `{"unknown":1,`, 1))},
		{"duplicate-alias", "json", []byte(strings.Replace(valid, `{`, `{"agent_id":"codex",`, 1))},
		{"null-item", "json", []byte(strings.Replace(valid, `"assetIds":[]`, `"assetIds":[null]`, 1))},
		{"wrong-ids-type", "json", []byte(strings.Replace(valid, `"assetIds":[]`, `"assetIds":{}`, 1))},
		{"unknown-enum", "json", []byte(strings.Replace(valid, `"kind":1`, `"kind":77`, 1))},
		{"negative-revision", "json", []byte(strings.Replace(valid, `"expectedRevision":"0"`, `"expectedRevision":"-1"`, 1))},
		{"unknown-field", "proto", protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)},
		{"wrong-string-wire", "proto", protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 1)},
		{"wrong-ids-wire", "proto", protowire.AppendVarint(protowire.AppendTag(nil, 4, protowire.VarintType), 1)},
		{"duplicate-scalar", "proto", []byte{10, 5, 'c', 'o', 'd', 'e', 'x', 10, 5, 'c', 'o', 'd', 'e', 'x', 16, 1}},
	}
	for _, entry := range cases {
		test.Run(entry.name, func(test *testing.T) {
			request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, server.URL+configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure, bytes.NewReader(entry.data))
			if err != nil {
				test.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("Content-Type", "application/"+entry.encoding)
			request.Header.Set("Connect-Protocol-Version", "1")
			response, err := server.Client().Do(request)
			if err != nil {
				test.Fatal(err)
			}
			body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusBadRequest || bytes.Contains(body, []byte(token)) {
				test.Fatalf("malformed order must reject without token echo: status%d read%v", response.StatusCode, err)
			}
		})
	}
	read, err := store.ConfigurationAssetOrder(test.Context(), owner, "codex", state.ConfigurationKindProvider)
	if err != nil || read.Revision != 0 || len(read.Assets) != 0 {
		test.Fatalf("bad request mutated virtual partition: %+v %v", read, err)
	}
}

func TestConfigurationOrderFullPartitionTLS(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			store, owner, token, server := configurationOrderHTTPFixture(test)
			for range state.MaxConfigurationAssets {
				_, err := store.CreateConfigurationAsset(test.Context(), owner, state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "readonly capacity row", SchemaVersion: 65535, Payload: state.NewConfigurationPayload([]byte(`{}`))})
				if err != nil {
					test.Fatal("create complete partition fixture", err)
				}
			}
			before, err := store.ConfigurationAssetOrder(test.Context(), owner, "codex", state.ConfigurationKindProvider)
			if err != nil || len(before.Assets) != 250 || before.Revision != 250 {
				test.Fatal("fixture must have 250 actual Store rows")
			}
			ids := make([]string, 0, len(before.Assets))
			for _, asset := range before.Assets {
				ids = append(ids, asset.ID)
			}
			slices.Reverse(ids)
			request := &configurationv1.ReplaceAssetOrderRequest{AgentId: "codex", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, ExpectedRevision: before.Revision, AssetIds: ids}
			var options []connect.ClientOption
			encoded, err := proto.Marshal(request)
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
				encoded, err = protojson.Marshal(request)
			}
			if err != nil || len(encoded) <= 4096 || len(encoded) >= 128<<10 {
				test.Fatal("full-partition witness must exceed generic budget but fit configuration budget")
			}
			client := configurationv1connect.NewConfigurationServiceClient(server.Client(), server.URL, options...)
			response, err := client.ReplaceAssetOrder(test.Context(), configurationRequest(request, token))
			if err != nil {
				test.Fatalf("complete 250-row TLS permutation rejected: %s", connect.CodeOf(err))
			}
			if response.Msg.Snapshot.AgentId != request.AgentId || response.Msg.Snapshot.Kind != request.Kind || response.Msg.Snapshot.Revision != before.Revision+1 || len(response.Msg.Snapshot.Assets) != 250 {
				test.Fatal("incomplete partition acknowledgment")
			}
			for index, asset := range response.Msg.Snapshot.Assets {
				expected, err := configurationAsset(before.Assets[249-index])
				if err != nil || !proto.Equal(asset, expected) {
					test.Fatal("full acknowledgment changed asset metadata or order")
				}
			}
			read, err := client.GetAssetOrder(test.Context(), configurationRequest(&configurationv1.GetAssetOrderRequest{AgentId: request.AgentId, Kind: request.Kind}, token))
			if err != nil || !proto.Equal(read.Msg.Snapshot, response.Msg.Snapshot) {
				test.Fatal("full TLS read-back mismatch")
			}
			durable, err := store.ConfigurationAssetOrder(test.Context(), owner, "codex", state.ConfigurationKindProvider)
			if err != nil || durable.Revision != before.Revision+1 || len(durable.Assets) != 250 {
				test.Fatal("full TLS permutation was not durably committed")
			}
			for index, asset := range durable.Assets {
				if asset.ID != ids[index] || asset.Revision != before.Assets[249-index].Revision || asset.Name != before.Assets[249-index].Name || asset.CreatedAt != before.Assets[249-index].CreatedAt || asset.UpdatedAt != before.Assets[249-index].UpdatedAt {
					test.Fatal("durable full order changed content metadata")
				}
			}
		})
	}
}

type configurationOrderBudgetBody struct {
	reader io.Reader
	reads  int
}

func (body *configurationOrderBudgetBody) Read(data []byte) (int, error) {
	body.reads++
	return body.reader.Read(data)
}

func (*configurationOrderBudgetBody) Close() error { return nil }

func TestConfigurationOrderGuardBudgetScope(test *testing.T) {
	cases := []struct {
		name  string
		path  string
		limit int
	}{
		{"order-get", configurationv1connect.ConfigurationServiceGetAssetOrderProcedure, 128 << 10},
		{"order-replace", configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure, 128 << 10},
		{"legacy-list", configurationv1connect.ConfigurationServiceListAssetsProcedure, 128 << 10},
		{"legacy-get", configurationv1connect.ConfigurationServiceGetAssetProcedure, 128 << 10},
		{"legacy-read", configurationv1connect.ConfigurationServiceReadDraftProcedure, 128 << 10},
		{"legacy-create", configurationv1connect.ConfigurationServiceCreateAssetProcedure, 128 << 10},
		{"legacy-update", configurationv1connect.ConfigurationServiceUpdateAssetProcedure, 128 << 10},
		{"legacy-copy", configurationv1connect.ConfigurationServiceCopyAssetProcedure, 128 << 10},
		{"legacy-delete", configurationv1connect.ConfigurationServiceDeleteAssetProcedure, 128 << 10},
		{"suffix", configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure + "Suffix", 4096},
		{"child", configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure + "/child", 4096},
		{"get-suffix", configurationv1connect.ConfigurationServiceGetAssetOrderProcedure + "Suffix", 4096},
		{"prefix", "/other" + configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure, 4096},
		{"case", strings.Replace(configurationv1connect.ConfigurationServiceGetAssetOrderProcedure, "GetAssetOrder", "getAssetOrder", 1), 4096},
		{"auth-session", authv1connect.AuthServiceGetSessionProcedure, 4096},
		{"auth-login", authv1connect.AuthServiceLoginProcedure, 4096},
	}
	for _, entry := range cases {
		test.Run(entry.name, func(test *testing.T) {
			for _, size := range []int{entry.limit, entry.limit + 1} {
				server := echo.New()
				server.Use(guardRequests(""))
				body := &configurationOrderBudgetBody{reader: bytes.NewReader(bytes.Repeat([]byte{'x'}, size))}
				server.POST(entry.path, func(ctx echo.Context) error {
					if body.reads != 0 {
						test.Fatal("outer guard eagerly consumed body")
					}
					_, err := io.ReadAll(ctx.Request().Body)
					if size == entry.limit {
						if err != nil {
							test.Fatal("exact route budget rejected", err)
						}
					} else if _, ok := errors.AsType[*http.MaxBytesError](err); !ok {
						test.Fatal("route budget+1 was not rejected")
					}
					return ctx.NoContent(http.StatusNoContent)
				})
				request := httptest.NewRequest(http.MethodPost, "http://localhost"+entry.path, nil)
				request.Body = body
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)
				if response.Code != http.StatusNoContent || body.reads == 0 {
					test.Fatal("guard budget probe did not reach bounded reader")
				}
			}
		})
	}
	for _, path := range []string{configurationv1connect.ConfigurationServiceGetAssetOrderProcedure, configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure, authv1connect.AuthServiceLoginProcedure} {
		server := echo.New()
		server.Use(guardRequests(""))
		body := &configurationOrderBudgetBody{reader: strings.NewReader(strings.Repeat("x", (128<<10)+1))}
		server.POST(path, func(echo.Context) error { test.Fatal("cross-origin reached handler"); return nil })
		request := httptest.NewRequest(http.MethodPost, "http://localhost"+path, nil)
		request.Body = body
		request.Header.Set("Origin", "https://foreign.example.test")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || body.reads != 0 {
			test.Fatal("cross-origin rejection read a lazy body")
		}
	}
}

func TestConfigurationOrderHTTPBodyBoundaries(test *testing.T) {
	store, owner, token, server := configurationOrderHTTPFixture(test)
	for _, entry := range []struct{ path, body string }{
		{configurationv1connect.ConfigurationServiceGetAssetOrderProcedure, `{"agentId":"codex","kind":1}`},
		{configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure, `{"agentId":"codex","kind":1,"expectedRevision":"0","assetIds":[]}`},
	} {
		exact := []byte(entry.body + strings.Repeat(" ", (128<<10)-len(entry.body)))
		status, _ := configurationRaw(test, server, token, entry.path, "application/json", exact)
		if status != http.StatusOK {
			test.Fatalf("exact 128KiB JSON rejected: status%d", status)
		}
		for _, encoding := range []string{"json", "proto"} {
			data := bytes.Repeat([]byte{'x'}, (128<<10)+1)
			if encoding == "json" {
				data = append(exact, ' ')
			}
			status, body := configurationRaw(test, server, token, entry.path, "application/"+encoding, data)
			if status < 400 || status >= 500 || len(body) > 4096 || !strings.Contains(body, "resource_exhausted") {
				test.Fatalf("over-128KiB must reject boundedly: encoding%s status%d", encoding, status)
			}
		}
		for _, path := range []string{entry.path + "Suffix", entry.path + "/child"} {
			status, body := configurationRaw(test, server, token, path, "application/json", []byte(entry.body+strings.Repeat(" ", 5000)))
			if status < 400 || status >= 500 || len(body) > 4096 {
				test.Fatal("near-path was admitted as an order method")
			}
		}
	}
	current, err := store.ConfigurationAssetOrder(test.Context(), owner, "codex", state.ConfigurationKindProvider)
	if err != nil || current.Revision != 0 || len(current.Assets) != 0 {
		test.Fatal("body/near-path rejections changed the virtual partition")
	}
}
