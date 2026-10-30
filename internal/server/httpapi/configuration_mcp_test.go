package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1/mcpv1connect"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/labstack/echo/v4"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type mcpAuthenticationBody struct {
	io.ReadCloser
	reads *atomic.Uint64
}

func (body mcpAuthenticationBody) Read(data []byte) (int, error) {
	body.reads.Add(1)
	return body.ReadCloser.Read(data)
}

func TestMCPConfigurationAuthenticationUnavailable(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			fixture := newMCPConfigurationFixture(test)
			var reads atomic.Uint64
			base := NewWithOptions(fixture.auth, Options{MCPConfiguration: fixture.handler})
			endpoint := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				request.Body = mcpAuthenticationBody{ReadCloser: request.Body, reads: &reads}
				base.ServeHTTP(writer, request)
			}))
			defer endpoint.Close()
			options := []connect.ClientOption{}
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := mcpv1connect.NewMCPConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			if err := fixture.store.Close(); err != nil {
				test.Fatal("close synthetic authentication store")
			}
			_, err := client.ListAssets(test.Context(), configurationRequest(&mcpv1.ListAssetsRequest{}, fixture.token))
			if connect.CodeOf(err) != connect.CodeUnavailable {
				test.Errorf("authentication storage failure: got %s, want unavailable", connect.CodeOf(err))
			}
			if reads.Load() != 0 {
				test.Error("authentication refusal read request body")
			}
		})
	}
}

func TestMCPConfigurationAuthenticationClassification(test *testing.T) {
	canceled, cancel := context.WithCancel(test.Context())
	cancel()
	expired, expire := context.WithDeadline(test.Context(), time.Unix(0, 0))
	defer expire()
	denied := connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	unavailable := connect.NewError(connect.CodeInternal, errors.New("authentication unavailable"))
	for _, scenario := range []struct {
		name string
		ctx  context.Context
		err  error
		code connect.Code
	}{
		{"denied", test.Context(), denied, connect.CodeUnauthenticated},
		{"storage", test.Context(), unavailable, connect.CodeUnavailable},
		{"private-storage", test.Context(), errors.New("synthetic-private-auth-storage"), connect.CodeUnavailable},
		{"domain-storage", test.Context(), configuration.ErrInvalid, connect.CodeUnavailable},
		{"canceled-error", test.Context(), context.Canceled, connect.CodeCanceled},
		{"expired-error", test.Context(), context.DeadlineExceeded, connect.CodeDeadlineExceeded},
		{"canceled-code", test.Context(), connect.NewError(connect.CodeCanceled, errors.New("request canceled")), connect.CodeCanceled},
		{"expired-code", test.Context(), connect.NewError(connect.CodeDeadlineExceeded, errors.New("request deadline exceeded")), connect.CodeDeadlineExceeded},
		{"canceled-denied", canceled, denied, connect.CodeCanceled},
		{"expired-denied", expired, denied, connect.CodeDeadlineExceeded},
		{"canceled-storage", canceled, unavailable, connect.CodeCanceled},
		{"expired-storage", expired, unavailable, connect.CodeDeadlineExceeded},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			err := mcpConfigurationAuthError(scenario.ctx, scenario.err)
			if connect.CodeOf(err) != scenario.code {
				test.Errorf("authentication category: got %s, want %s", connect.CodeOf(err), scenario.code)
			}
			if strings.Contains(err.Error(), "synthetic-private-auth-storage") {
				test.Error("authentication category exposed infrastructure details")
			}
		})
	}
}

func TestMCPConfigurationMount(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	status, _ := configurationRaw(test, fixture.server, fixture.token, mcpv1connect.MCPConfigurationServiceListAssetsProcedure, "application/json", []byte(`{}`))
	if status != http.StatusOK {
		test.Fatalf("MCP owner ListAssets must mount: got HTTP %d", status)
	}
}

func TestMCPConfigurationGuardBudgets(test *testing.T) {
	paths := []string{
		mcpv1connect.MCPConfigurationServiceListAssetsProcedure,
		mcpv1connect.MCPConfigurationServiceGetAssetProcedure,
		mcpv1connect.MCPConfigurationServiceReadDraftProcedure,
		mcpv1connect.MCPConfigurationServiceCreateAssetProcedure,
		mcpv1connect.MCPConfigurationServiceUpdateAssetProcedure,
		mcpv1connect.MCPConfigurationServiceCopyAssetProcedure,
		mcpv1connect.MCPConfigurationServiceDeleteAssetProcedure,
		mcpv1connect.MCPConfigurationServicePreviewProcedure,
	}
	limits := make(map[string]int)
	for _, path := range paths {
		limits[path] = 2 << 20
		limits[path+"Suffix"] = 4096
		limits[path+"/child"] = 4096
		limits["/other"+path] = 4096
		limits[strings.ToLower(path)] = 4096
	}
	limits[configurationv1connect.ConfigurationServiceCreateAssetProcedure] = 128 << 10
	limits["/other.Service/Call"] = 4096
	for path, limit := range limits {
		test.Run(path, func(test *testing.T) {
			for _, size := range []int{limit, limit + 1} {
				server := echo.New()
				server.Use(guardRequests(""))
				body := &configurationOrderBudgetBody{reader: bytes.NewReader(bytes.Repeat([]byte{'x'}, size))}
				server.POST(path, func(ctx echo.Context) error {
					if body.reads != 0 {
						test.Fatal("guard consumed lazy body")
					}
					_, err := io.ReadAll(ctx.Request().Body)
					if size == limit && err != nil {
						test.Fatal("exact budget rejected", err)
					}
					if size > limit {
						if _, ok := errors.AsType[*http.MaxBytesError](err); !ok {
							test.Fatal("budget+1 not rejected")
						}
					}
					return ctx.NoContent(http.StatusNoContent)
				})
				request := httptest.NewRequest(http.MethodPost, "http://localhost"+path, nil)
				request.Body = body
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)
				if response.Code != http.StatusNoContent || body.reads == 0 {
					test.Fatal("bounded body not reached")
				}
			}
		})
	}
}

type mcpConfigurationFixture struct {
	directory             string
	store                 *state.Store
	auth                  *auth.Service
	owner, token, foreign string
	handler               *MCPConfigurationHandler
	server                *httptest.Server
}

func newMCPConfigurationFixture(test *testing.T) *mcpConfigurationFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{83}, 32)
	store, err := state.Open(test.Context(), filepath.Join(directory, "state.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	owner, token := mcpSession(test, authentication, "mcp-configuration-owner")
	_, foreign := mcpSession(test, authentication, "mcp-configuration-foreign")
	handler, err := NewMCPConfigurationHandler(authentication, store)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = handler.Close() })
	server := httptest.NewTLSServer(NewWithOptions(authentication, Options{MCPConfiguration: handler}))
	test.Cleanup(server.Close)
	return &mcpConfigurationFixture{directory: directory, store: store, auth: authentication, owner: owner, token: token, foreign: foreign, handler: handler, server: server}
}

func (fixture *mcpConfigurationFixture) client(encoding string) mcpv1connect.MCPConfigurationServiceClient {
	options := []connect.ClientOption{}
	if encoding == "json" {
		options = append(options, connect.WithProtoJSON())
	}
	return mcpv1connect.NewMCPConfigurationServiceClient(fixture.server.Client(), fixture.server.URL, options...)
}

func mcpConfigurationDraft() *mcpv1.Draft {
	return &mcpv1.Draft{Name: "MCP connections", AgentId: "claude", Document: &mcpv1.Document{Servers: []*mcpv1.Server{
		{Id: "stdio", Name: "stdio", Enabled: new(true), Connection: &mcpv1.Server_Stdio{Stdio: &mcpv1.Stdio{Command: "mcp-run", Args: &mcpv1.Args{Values: []*mcpv1.Arg{{Id: "a", SlotRef: "arg/stdio/a"}}}, Env: &mcpv1.NamedSlots{Values: []*mcpv1.NamedSlot{{Name: "TOKEN", SlotRef: "env/stdio/TOKEN"}}}}}},
		{Id: "http", Name: "http", Enabled: new(true), Connection: &mcpv1.Server_Http{Http: &mcpv1.Remote{Url: "https://example.test/mcp", Headers: &mcpv1.NamedSlots{Values: []*mcpv1.NamedSlot{{Name: "Authorization", SlotRef: "header/http/Authorization"}}}}}},
		{Id: "sse", Name: "sse", Enabled: new(false), Connection: &mcpv1.Server_Sse{Sse: &mcpv1.Remote{Url: "https://example.test/events", Headers: &mcpv1.NamedSlots{}}}},
	}}}
}

func mcpConfigurationReplacements() []*mcpv1.SecretMutation {
	return []*mcpv1.SecretMutation{
		{SlotId: "arg/stdio/a", Action: &mcpv1.SecretMutation_Replace{Replace: []byte("synthetic-mcp-arg-secret")}},
		{SlotId: "env/stdio/TOKEN", Action: &mcpv1.SecretMutation_Replace{Replace: []byte{}}},
		{SlotId: "header/http/Authorization", Action: &mcpv1.SecretMutation_Replace{Replace: []byte("synthetic-mcp-header-secret")}},
	}
}

func TestMCPConfigurationTLSLifecycle(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			fixture := newMCPConfigurationFixture(test)
			client := fixture.client(encoding)
			var created *mcpv1.View
			test.Run("CreateAsset", func(test *testing.T) {
				response, err := client.CreateAsset(test.Context(), configurationRequest(&mcpv1.CreateAssetRequest{Draft: mcpConfigurationDraft(), Secrets: mcpConfigurationReplacements()}, fixture.token))
				if err != nil {
					test.Fatal(err)
				}
				created = response.Msg.View
				if created.Asset.Revision != 1 || created.Readiness != mcpv1.Readiness_READINESS_COMPLETE || len(created.Secrets) != 3 {
					test.Fatal("incomplete create acknowledgment")
				}
				for _, slot := range created.Secrets {
					if slot.Present == nil || !*slot.Present {
						test.Fatal("present-empty credential lost")
					}
				}
			})
			if created == nil {
				test.Fatal("create prerequisite failed")
			}
			id := created.Asset.Id
			test.Run("ListAssets", func(test *testing.T) {
				response, err := client.ListAssets(test.Context(), configurationRequest(&mcpv1.ListAssetsRequest{}, fixture.token))
				if err != nil || len(response.Msg.Assets) != 1 || !proto.Equal(response.Msg.Assets[0], created.Asset) {
					test.Fatalf("metadata list: %v", err)
				}
			})
			test.Run("GetAsset", func(test *testing.T) {
				response, err := client.GetAsset(test.Context(), configurationRequest(&mcpv1.GetAssetRequest{Id: id}, fixture.token))
				if err != nil || !proto.Equal(response.Msg.Asset, created.Asset) {
					test.Fatalf("get metadata: %v", err)
				}
			})
			test.Run("ReadDraft", func(test *testing.T) {
				response, err := client.ReadDraft(test.Context(), configurationRequest(&mcpv1.ReadDraftRequest{Id: id}, fixture.token))
				if err != nil || !proto.Equal(response.Msg.View, created) {
					test.Fatalf("safe draft roundtrip: %v", err)
				}
			})
			test.Run("Preview", func(test *testing.T) {
				response, err := client.Preview(test.Context(), configurationRequest(&mcpv1.PreviewRequest{Id: id, ExpectedRevision: 1, Draft: mcpConfigurationDraft()}, fixture.token))
				if err != nil || len(response.Msg.Operations) != 3 || response.Msg.CanApply || response.Msg.Applied {
					test.Fatalf("logic-only preview: %v", err)
				}
				if response.Msg.Operations[0].Desired == nil || response.Msg.Operations[2].Desired != nil || !response.Msg.Operations[2].RequiresManagedProof {
					test.Fatal("managed-only operation shape")
				}
				metadata, err := fixture.store.ConfigurationAsset(test.Context(), fixture.owner, id)
				if err != nil || metadata.Revision != 1 {
					test.Fatal("preview wrote database")
				}
			})
			test.Run("OwnerCAS", func(test *testing.T) {
				if _, err := client.GetAsset(test.Context(), configurationRequest(&mcpv1.GetAssetRequest{Id: id}, fixture.foreign)); connect.CodeOf(err) != connect.CodeNotFound {
					test.Fatalf("foreign asset: %v", err)
				}
				if _, err := client.ListAssets(test.Context(), configurationRequest(&mcpv1.ListAssetsRequest{}, "")); connect.CodeOf(err) != connect.CodeUnauthenticated {
					test.Fatalf("anonymous list: %v", err)
				}
				response, err := client.ListAssets(test.Context(), configurationRequest(&mcpv1.ListAssetsRequest{}, fixture.foreign))
				if err != nil || len(response.Msg.Assets) != 0 {
					test.Fatalf("foreign metadata: %v", err)
				}
			})
			test.Run("UpdateAsset", func(test *testing.T) {
				draft := mcpConfigurationDraft()
				draft.Document.Servers[0].Enabled = new(false)
				response, err := client.UpdateAsset(test.Context(), configurationRequest(&mcpv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, fixture.token))
				if err != nil || response.Msg.View.Asset.Revision != 2 || response.Msg.View.Draft.Document.Servers[0].Enabled == nil || *response.Msg.View.Draft.Document.Servers[0].Enabled {
					test.Fatalf("explicit false CAS: %v", err)
				}
				if _, err := client.UpdateAsset(test.Context(), configurationRequest(&mcpv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, fixture.token)); connect.CodeOf(err) != connect.CodeAborted {
					test.Fatalf("stale CAS: %v", err)
				}
			})
			var copyID string
			test.Run("CopyAsset", func(test *testing.T) {
				response, err := client.CopyAsset(test.Context(), configurationRequest(&mcpv1.CopyAssetRequest{Id: id, ExpectedSourceRevision: 2, Name: "copied connections"}, fixture.token))
				if err != nil {
					test.Fatal(err)
				}
				copyID = response.Msg.View.Asset.Id
				if response.Msg.View.Readiness != mcpv1.Readiness_READINESS_NEEDS_CREDENTIAL || copyID == id {
					test.Fatal("copy readiness/identity")
				}
				for _, slot := range response.Msg.View.Secrets {
					if slot.Present == nil || *slot.Present {
						test.Fatal("copy carried secret")
					}
				}
				original, err := client.ReadDraft(test.Context(), configurationRequest(&mcpv1.ReadDraftRequest{Id: id}, fixture.token))
				if err != nil {
					test.Fatal(err)
				}
				for _, slot := range original.Msg.View.Secrets {
					if !slot.GetPresent() {
						test.Fatal("copy modified source")
					}
				}
			})
			test.Run("DeleteAsset", func(test *testing.T) {
				if copyID == "" {
					test.Fatal("copy prerequisite")
				}
				if _, err := client.DeleteAsset(test.Context(), configurationRequest(&mcpv1.DeleteAssetRequest{Id: copyID, ExpectedRevision: 1}, fixture.token)); err != nil {
					test.Fatal(err)
				}
				if _, err := client.ReadDraft(test.Context(), configurationRequest(&mcpv1.ReadDraftRequest{Id: copyID}, fixture.token)); connect.CodeOf(err) != connect.CodeNotFound {
					test.Fatalf("deleted asset still readable: %v", err)
				}
			})
			test.Run("UnionClear", func(test *testing.T) {
				draft := mcpConfigurationDraft()
				draft.Document.Servers = nil
				if _, err := client.UpdateAsset(test.Context(), configurationRequest(&mcpv1.UpdateAssetRequest{Id: id, ExpectedRevision: 2, Draft: draft}, fixture.token)); connect.CodeOf(err) != connect.CodeInvalidArgument {
					test.Fatalf("old-only slots silently discarded: %v", err)
				}
				mutations := []*mcpv1.SecretMutation{}
				for _, slot := range created.Secrets {
					mutations = append(mutations, &mcpv1.SecretMutation{SlotId: slot.SlotId, Action: &mcpv1.SecretMutation_Clear{Clear: &mcpv1.Clear{Confirmed: new(true)}}})
				}
				response, err := client.UpdateAsset(test.Context(), configurationRequest(&mcpv1.UpdateAssetRequest{Id: id, ExpectedRevision: 2, Draft: draft, Secrets: mutations}, fixture.token))
				if err != nil || response.Msg.View.Asset.Revision != 3 || len(response.Msg.View.Secrets) != 0 {
					test.Fatalf("confirmed removal: %v", err)
				}
			})
		})
	}
}

func mcpMaximumDraft(test *testing.T, prefix string) (*mcpv1.Draft, []string) {
	test.Helper()
	document := agentconfig.MCPDocumentV1{Servers: []agentconfig.MCPServer{}}
	refs := []string{}
	for index := range 32 {
		id := fmt.Sprintf("%s%02d", prefix, index)
		server := agentconfig.MCPServer{ID: id, Name: id, Enabled: new(false), Stdio: &agentconfig.MCPStdio{Command: "cmd", Args: &agentconfig.MCPArgs{Values: []agentconfig.MCPArg{}}}}
		for arg := range 16 {
			argID := fmt.Sprintf("a%02d", arg)
			ref := "arg/" + id + "/" + argID
			refs = append(refs, ref)
			server.Stdio.Args.Values = append(server.Stdio.Args.Values, agentconfig.MCPArg{ID: argID, ValueRef: ref})
		}
		document.Servers = append(document.Servers, server)
	}
	for index := range document.Servers {
		encoded, err := json.Marshal(document)
		if err != nil {
			test.Fatal(err)
		}
		extra := min((64<<10)-len(encoded), 4096-len(document.Servers[index].Stdio.Command))
		if extra > 0 {
			document.Servers[index].Stdio.Command += strings.Repeat("c", extra)
		}
	}
	encoded, _ := json.Marshal(document)
	if len(encoded) != 64<<10 {
		test.Fatal("max document not exact")
	}
	if slots, err := agentconfig.ValidateMCPDocument(document); err != nil || len(slots) != 512 {
		test.Fatal("invalid independent boundary fixture", err)
	}
	result := &mcpv1.Draft{Name: strings.Repeat("n", 128), AgentId: "claude", Document: &mcpv1.Document{Servers: []*mcpv1.Server{}}}
	for _, server := range document.Servers {
		args := &mcpv1.Args{Values: []*mcpv1.Arg{}}
		for _, arg := range server.Stdio.Args.Values {
			args.Values = append(args.Values, &mcpv1.Arg{Id: arg.ID, SlotRef: arg.ValueRef})
		}
		result.Document.Servers = append(result.Document.Servers, &mcpv1.Server{Id: server.ID, Name: server.Name, Enabled: new(false), Connection: &mcpv1.Server_Stdio{Stdio: &mcpv1.Stdio{Command: server.Stdio.Command, Args: args}}})
	}
	return result, refs
}

func TestMCPConfigurationExtremeTLS(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			fixture := newMCPConfigurationFixture(test)
			client := fixture.client(encoding)
			old, oldRefs := mcpMaximumDraft(test, "o")
			created, err := client.CreateAsset(test.Context(), configurationRequest(&mcpv1.CreateAssetRequest{Draft: old}, fixture.token))
			if err != nil {
				test.Fatal("old maximum draft", err)
			}
			draft, refs := mcpMaximumDraft(test, "n")
			mutations := []*mcpv1.SecretMutation{}
			for _, ref := range oldRefs {
				mutations = append(mutations, &mcpv1.SecretMutation{SlotId: ref, Action: &mcpv1.SecretMutation_Clear{Clear: &mcpv1.Clear{Confirmed: new(true)}}})
			}
			for index, ref := range refs {
				value := []byte{}
				if index < 16 {
					value = bytes.Repeat([]byte{'s'}, 8192)
				}
				mutations = append(mutations, &mcpv1.SecretMutation{SlotId: ref, Action: &mcpv1.SecretMutation_Replace{Replace: value}})
			}
			request := &mcpv1.UpdateAssetRequest{Id: created.Msg.View.Asset.Id, ExpectedRevision: 1, Draft: draft, Secrets: mutations}
			binary, _ := proto.Marshal(request)
			text, _ := protojson.Marshal(request)
			if len(text) <= 128<<10 || len(text) >= 2<<20 || len(binary) <= 128<<10 || len(binary) >= 2<<20 || len(mutations) != 1024 {
				test.Fatal("did not exercise raw capacity")
			}
			response, err := client.UpdateAsset(test.Context(), configurationRequest(request, fixture.token))
			if err != nil {
				test.Fatal("maximum semantic combination", err)
			}
			if response.Msg.View.Asset.Revision != 2 || len(response.Msg.View.Secrets) != 512 || response.Msg.View.Readiness != mcpv1.Readiness_READINESS_COMPLETE {
				test.Fatal("max acknowledgment truncated")
			}
			read, err := client.ReadDraft(test.Context(), configurationRequest(&mcpv1.ReadDraftRequest{Id: request.Id}, fixture.token))
			if err != nil || !proto.Equal(response.Msg.View, read.Msg.View) {
				test.Fatal("max response roundtrip", err)
			}
			metadata, payload, err := fixture.store.ReadConfigurationAssetVersion(test.Context(), fixture.owner, request.Id)
			if err != nil {
				test.Fatal(err)
			}
			raw := payload.Reveal()
			defer clear(raw)
			var envelope struct {
				Slots    map[string][]byte `json:"slots"`
				Version  int               `json:"version"`
				Document jsontext.Value    `json:"document"`
			}
			_ = metadata
			if err := json.Unmarshal(raw, &envelope); err != nil {
				test.Fatal(err)
			}
			total := 0
			for _, value := range envelope.Slots {
				total += len(value)
				clear(value)
			}
			if total != 128<<10 || len(envelope.Slots) != 512 {
				test.Fatal("real encrypted inventory did not contain exact bytes")
			}
		})
	}
}

func TestMCPConfigurationStoreRevocation(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	proxy := &mcpRequestStore{store: fixture.store, owner: fixture.owner, authorize: func(ctx context.Context) error { _, err := fixture.auth.Authenticate(ctx, fixture.token); return err }}
	if _, err := proxy.ListConfigurationAssets(test.Context(), fixture.owner); err != nil {
		test.Fatal(err)
	}
	if err := fixture.auth.Logout(test.Context(), fixture.token); err != nil {
		test.Fatal(err)
	}
	for _, operation := range []func() error{
		func() error { _, err := proxy.ListConfigurationAssets(test.Context(), fixture.owner); return err },
		func() error { _, err := proxy.ConfigurationAsset(test.Context(), fixture.owner, "invalid"); return err },
		func() error {
			_, _, err := proxy.ReadConfigurationAssetVersion(test.Context(), fixture.owner, "invalid")
			return err
		},
		func() error {
			_, err := proxy.CreateConfigurationAsset(test.Context(), fixture.owner, state.ConfigurationAssetDraft{})
			return err
		},
		func() error {
			_, err := proxy.UpdateConfigurationAsset(test.Context(), fixture.owner, "invalid", 1, state.ConfigurationAssetDraft{})
			return err
		},
		func() error { return proxy.DeleteConfigurationAsset(test.Context(), fixture.owner, "invalid", 1) },
	} {
		if err := operation(); !errors.Is(err, auth.ErrInvalidSession) {
			test.Fatalf("revoked session reached Store: %v", err)
		}
	}
}

type mcpUnreadBody struct{ reads int }

func (body *mcpUnreadBody) Read([]byte) (int, error) {
	body.reads++
	return 0, errors.New("body must not be read")
}
func (*mcpUnreadBody) Close() error { return nil }

func TestMCPConfigurationPrebodySession(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	access, err := mcpaccess.NewService(fixture.store)
	if err != nil {
		test.Fatal(err)
	}
	_, f10, err := access.Create(test.Context(), fixture.owner, state.MCPMetadata{Name: "synthetic F10", Scopes: []string{state.MCPCatalogRead}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range []struct {
		name, token, origin string
		closed              bool
	}{
		{name: "anonymous"}, {name: "invalid-session", token: "synthetic-not-a-session"}, {name: "F10-is-not-session", token: f10.Reveal()}, {name: "foreign-origin", token: fixture.token, origin: "https://foreign.example.test"}, {name: "closed", token: fixture.token, closed: true},
	} {
		test.Run(entry.name, func(test *testing.T) {
			if entry.closed {
				_ = fixture.handler.Close()
			}
			body := new(mcpUnreadBody)
			request := httptest.NewRequest(http.MethodPost, "https://localhost"+mcpv1connect.MCPConfigurationServiceCreateAssetProcedure, nil)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Connect-Protocol-Version", "1")
			request.Body = body
			if entry.token != "" {
				request.Header.Set("Authorization", "Bearer "+entry.token)
			}
			if entry.origin != "" {
				request.Header.Set("Origin", entry.origin)
			}
			response := httptest.NewRecorder()
			fixture.server.Config.Handler.ServeHTTP(response, request)
			if response.Code < 400 || body.reads != 0 {
				test.Fatal("prebody rejection consumed body", response.Code, body.reads)
			}
		})
	}
}

func TestMCPConfigurationHealthyKeepalive(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	client := fixture.client("json")
	for index := range 3 {
		observed, reused := false, false
		ctx := httptrace.WithClientTrace(test.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { observed = true; reused = info.Reused }})
		if _, err := client.ListAssets(ctx, configurationRequest(&mcpv1.ListAssetsRequest{}, fixture.token)); err != nil {
			test.Fatal("healthy EOF request", err)
		}
		if !observed || index > 0 && !reused {
			test.Fatal("healthy EOF lost keepalive", index)
		}
	}
}

func TestMCPConfigurationLegacyIsolation(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	endpoint := httptest.NewTLSServer(NewWithOptions(fixture.auth, Options{Configuration: configuration.New(fixture.store)}))
	defer endpoint.Close()
	for _, encoding := range []string{"proto", "json"} {
		message := &mcpv1.CreateAssetRequest{Draft: mcpConfigurationDraft()}
		var data []byte
		var err error
		if encoding == "json" {
			data, err = protojson.Marshal(message)
		} else {
			data, err = proto.Marshal(message)
		}
		if err != nil {
			test.Fatal(err)
		}
		status, _ := configurationRaw(test, endpoint, fixture.token, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/"+encoding, data)
		if status < 400 {
			test.Fatal("old provider endpoint interpreted MCP as provider", encoding, status)
		}
	}
	status, _ := configurationRaw(test, endpoint, fixture.token, mcpv1connect.MCPConfigurationServiceListAssetsProcedure, "application/json", []byte(`{}`))
	if status != http.StatusNotFound {
		test.Fatal("nil MCP option unexpectedly mounted", status)
	}
	assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
	if err != nil || len(assets) != 0 {
		test.Fatal("legacy confusion created asset", err)
	}
}

func TestMCPConfigurationHTTPBodyBoundaries(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	exact := append([]byte(`{}`), bytes.Repeat([]byte{' '}, (2<<20)-2)...)
	status, _ := configurationRaw(test, fixture.server, fixture.token, mcpv1connect.MCPConfigurationServiceListAssetsProcedure, "application/json", exact)
	if status != 200 {
		test.Fatal("exact 2MiB canonical-plus-padding list rejected", status)
	}
	for _, encoding := range []string{"json", "proto"} {
		data := bytes.Repeat([]byte{'x'}, (2<<20)+1)
		if encoding == "json" {
			data = append(exact, ' ')
		}
		request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.server.URL+mcpv1connect.MCPConfigurationServiceListAssetsProcedure, io.NopCloser(bytes.NewReader(data)))
		if err != nil {
			test.Fatal(err)
		}
		request.ContentLength = -1
		request.Header.Set("Content-Type", "application/"+encoding)
		request.Header.Set("Connect-Protocol-Version", "1")
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		response, err := fixture.server.Client().Do(request)
		if err != nil {
			test.Fatal(err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
		response.Body.Close()
		if err != nil || response.StatusCode < 400 || len(body) > 4096 || !bytes.Contains(body, []byte("resource_exhausted")) {
			test.Fatal("raw+1 not rejected boundedly", response.StatusCode, err)
		}
	}
}

func TestMCPConfigurationCancellationAndClose(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	results := make(chan error, 8)
	writers := []*io.PipeWriter{}
	written := make(chan struct{}, 8)
	for range 8 {
		reader, writer := io.Pipe()
		writers = append(writers, writer)
		go func() { _, _ = writer.Write([]byte("{")); written <- struct{}{} }()
		go func() {
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, fixture.server.URL+mcpv1connect.MCPConfigurationServiceCreateAssetProcedure, reader)
			if err != nil {
				results <- err
				return
			}
			request.Header.Set("Authorization", "Bearer "+fixture.token)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Connect-Protocol-Version", "1")
			response, err := fixture.server.Client().Do(request)
			if err == nil {
				_, err = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode < 400 {
					err = errors.New("slow incomplete request succeeded")
				}
			}
			results <- err
		}()
	}
	defer func() {
		for _, writer := range writers {
			_ = writer.Close()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		fixture.handler.mutex.Lock()
		active := len(fixture.handler.active)
		fixture.handler.mutex.Unlock()
		if active == 8 {
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("did not establish eight actual slow-body requests", active)
		}
		time.Sleep(time.Millisecond)
	}
	status, body := configurationRaw(test, fixture.server, fixture.token, mcpv1connect.MCPConfigurationServiceListAssetsProcedure, "application/json", []byte(`{}`))
	if status < 400 || !strings.Contains(body, "resource_exhausted") {
		test.Fatal("ninth request did not reject immediately", status)
	}
	closed := make(chan error, 1)
	go func() { closed <- fixture.handler.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		test.Fatal("Close failed to cancel and join active body reads")
	}
	for _, writer := range writers {
		_ = writer.Close()
	}
	for range 8 {
		select {
		case <-written:
		case <-time.After(3 * time.Second):
			test.Fatal("body writer did not join")
		}
		select {
		case <-results:
		case <-time.After(3 * time.Second):
			test.Fatal("client/transport did not join")
		}
	}
	fixture.handler.mutex.Lock()
	remaining := len(fixture.handler.active)
	fixture.handler.mutex.Unlock()
	if remaining != 0 {
		test.Fatal("Close returned with active requests")
	}
	status, _ = configurationRaw(test, fixture.server, fixture.token, mcpv1connect.MCPConfigurationServiceListAssetsProcedure, "application/json", []byte(`{}`))
	if status != 503 {
		test.Fatal("closed handler readmitted request", status)
	}
}

func TestMCPConfigurationFullCapacity(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	ids := make(map[string]bool)
	for index := range 250 {
		asset, err := fixture.store.CreateConfigurationAsset(test.Context(), fixture.owner, state.ConfigurationAssetDraft{
			AgentID: "claude", Kind: state.ConfigurationKindMCP, Name: fmt.Sprintf("future-%03d", index), SchemaVersion: 99,
			Payload: state.NewConfigurationPayload([]byte(`{"future":true}`)),
		})
		if err != nil {
			test.Fatal("real quota fixture", err)
		}
		ids[asset.ID] = true
	}
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			client := fixture.client(encoding)
			response, err := client.ListAssets(test.Context(), configurationRequest(&mcpv1.ListAssetsRequest{}, fixture.token))
			if err != nil || len(response.Msg.Assets) != 250 {
				test.Fatal("full list truncated", err)
			}
			seen := make(map[string]bool)
			for _, asset := range response.Msg.Assets {
				if !ids[asset.Id] || seen[asset.Id] || asset.SchemaVersion != 99 {
					test.Fatal("metadata identity/schema lost")
				}
				seen[asset.Id] = true
			}
			id := response.Msg.Assets[0].Id
			if _, err := client.GetAsset(test.Context(), configurationRequest(&mcpv1.GetAssetRequest{Id: id}, fixture.token)); err != nil {
				test.Fatal("future metadata must remain readable", err)
			}
			if _, err := client.ReadDraft(test.Context(), configurationRequest(&mcpv1.ReadDraftRequest{Id: id}, fixture.token)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("future envelope must remain read-only", err)
			}
			if _, err := client.CreateAsset(test.Context(), configurationRequest(&mcpv1.CreateAssetRequest{Draft: mcpConfigurationDraft()}, fixture.token)); connect.CodeOf(err) != connect.CodeResourceExhausted {
				test.Fatal("quota+1 not rejected", err)
			}
		})
	}
}

func TestMCPConfigurationUnknownExtrasReadOnly(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			fixture := newMCPConfigurationFixture(test)
			raw := []byte(`{"version":1,"document":{"servers":[]},"slots":{},"future":"synthetic-private-unknown-extra"}`)
			asset, err := fixture.store.CreateConfigurationAsset(test.Context(), fixture.owner, state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindMCP, Name: "future", SchemaVersion: 1, Payload: state.NewConfigurationPayload(raw)})
			if err != nil {
				test.Fatal(err)
			}
			client := fixture.client(encoding)
			if _, err := client.ReadDraft(test.Context(), configurationRequest(&mcpv1.ReadDraftRequest{Id: asset.ID}, fixture.token)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("future extras must remain metadata-only", err)
			}
			metadataResponse, err := client.GetAsset(test.Context(), configurationRequest(&mcpv1.GetAssetRequest{Id: asset.ID}, fixture.token))
			if err != nil {
				test.Fatal("future metadata unavailable", err)
			}
			encoded, err := protojson.Marshal(metadataResponse.Msg)
			if err != nil || bytes.Contains(encoded, []byte("synthetic-private-unknown-extra")) {
				test.Fatal("private future extras escaped")
			}
			if _, err := client.UpdateAsset(test.Context(), configurationRequest(&mcpv1.UpdateAssetRequest{Id: asset.ID, ExpectedRevision: 1, Draft: mcpConfigurationDraft()}, fixture.token)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("update stripped unknown extras", err)
			}
			if _, err := client.CopyAsset(test.Context(), configurationRequest(&mcpv1.CopyAssetRequest{Id: asset.ID, ExpectedSourceRevision: 1, Name: "copy"}, fixture.token)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("copy stripped unknown extras", err)
			}
			metadata, payload, err := fixture.store.ReadConfigurationAssetVersion(test.Context(), fixture.owner, asset.ID)
			if err != nil || metadata.Revision != 1 || !bytes.Equal(payload.Reveal(), raw) {
				test.Fatal("readonly envelope changed", err)
			}
		})
	}
}

func TestMCPConfigurationRemovalKinds(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		for _, kind := range []string{"arg", "env", "header", "server", "transport"} {
			test.Run(encoding+"/"+kind, func(test *testing.T) {
				fixture := newMCPConfigurationFixture(test)
				client := fixture.client(encoding)
				created, err := client.CreateAsset(test.Context(), configurationRequest(&mcpv1.CreateAssetRequest{Draft: mcpConfigurationDraft(), Secrets: mcpConfigurationReplacements()}, fixture.token))
				if err != nil {
					test.Fatal(err)
				}
				draft := mcpConfigurationDraft()
				ref := "arg/stdio/a"
				switch kind {
				case "arg":
					draft.Document.Servers[0].GetStdio().Args = &mcpv1.Args{}
				case "env":
					draft.Document.Servers[0].GetStdio().Env = &mcpv1.NamedSlots{}
					ref = "env/stdio/TOKEN"
				case "header":
					draft.Document.Servers[1].GetHttp().Headers = &mcpv1.NamedSlots{}
					ref = "header/http/Authorization"
				case "server":
					draft.Document.Servers = append(draft.Document.Servers[:1], draft.Document.Servers[2:]...)
					ref = "header/http/Authorization"
				case "transport":
					draft.Document.Servers[1].Connection = &mcpv1.Server_Stdio{Stdio: &mcpv1.Stdio{Command: "cmd"}}
					ref = "header/http/Authorization"
				}
				id := created.Msg.View.Asset.Id
				if _, err := client.UpdateAsset(test.Context(), configurationRequest(&mcpv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, fixture.token)); connect.CodeOf(err) != connect.CodeInvalidArgument {
					test.Fatal("removal bypassed explicit clear", err)
				}
				response, err := client.UpdateAsset(test.Context(), configurationRequest(&mcpv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft, Secrets: []*mcpv1.SecretMutation{{SlotId: ref, Action: &mcpv1.SecretMutation_Clear{Clear: &mcpv1.Clear{Confirmed: new(true)}}}}}, fixture.token))
				if err != nil || response.Msg.View.Asset.Revision != 2 || len(response.Msg.View.Secrets) != 2 {
					test.Fatal("explicit union removal failed", err)
				}
				for _, slot := range response.Msg.View.Secrets {
					if slot.SlotId == ref {
						test.Fatal("removed secret retained")
					}
				}
			})
		}
	}
}

func TestMCPConfigurationTwoStoresReopen(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	other, err := state.Open(test.Context(), filepath.Join(fixture.directory, "state.db"), bytes.Repeat([]byte{83}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer other.Close()
	created, err := fixture.client("proto").CreateAsset(test.Context(), configurationRequest(&mcpv1.CreateAssetRequest{Draft: mcpConfigurationDraft(), Secrets: mcpConfigurationReplacements()}, fixture.token))
	if err != nil {
		test.Fatal(err)
	}
	metadata, payload, err := fixture.store.ReadConfigurationAssetVersion(test.Context(), fixture.owner, created.Msg.View.Asset.Id)
	if err != nil {
		test.Fatal(err)
	}
	results := make(chan error, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, store := range []*state.Store{fixture.store, other} {
		workers.Go(func() {
			<-start
			_, err := store.UpdateConfigurationAsset(test.Context(), fixture.owner, metadata.ID, 1, state.ConfigurationAssetDraft{
				AgentID: metadata.AgentID, Kind: metadata.Kind, Name: metadata.Name, SchemaVersion: metadata.SchemaVersion, Payload: payload,
			})
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, state.ErrConfigurationConflict) {
			conflicts++
		} else {
			test.Fatal("two Store CAS unexpected error", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		test.Fatal("two Store CAS lost", successes, conflicts)
	}
	if err := other.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := state.Open(test.Context(), filepath.Join(fixture.directory, "state.db"), bytes.Repeat([]byte{83}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	view, err := configuration.NewMCP(reopened).ReadDraft(test.Context(), fixture.owner, metadata.ID)
	if err != nil || view.Metadata.Revision != 2 || len(view.Secrets) != 3 {
		test.Fatal("reopened encrypted view", err)
	}
}

func TestMCPConfigurationResponseTail(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	for _, mode := range []string{"revoked", "canceled", "latched"} {
		test.Run(mode, func(test *testing.T) {
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			proxy := &mcpRequestStore{store: fixture.store, owner: fixture.owner, authorize: func(context.Context) error { return nil }}
			admitted := &mcpConfigurationRequest{store: proxy, service: configuration.NewMCP(proxy)}
			ctx = context.WithValue(ctx, mcpConfigurationContextKey{}, admitted)
			response, err := mcpConfigurationCall(ctx, func(*configuration.MCPService, string) (*mcpv1.ListAssetsResponse, error) {
				switch mode {
				case "revoked":
					proxy.authorize = func(context.Context) error { proxy.failure.Store(1); return errors.New("private-tail-sentinel") }
				case "latched":
					proxy.failure.Store(1)
				case "canceled":
					cancel()
				}
				return &mcpv1.ListAssetsResponse{}, nil
			})
			if response != nil || err == nil || strings.Contains(err.Error(), "private-tail-sentinel") {
				test.Fatal("tail gate released response/error detail")
			}
		})
	}
}

func TestMCPConfigurationHTTP2CancelAndRevoke(test *testing.T) {
	for _, mode := range []string{"cancel", "revoke"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newMCPConfigurationFixture(test)
			endpoint := httptest.NewUnstartedServer(NewWithOptions(fixture.auth, Options{MCPConfiguration: fixture.handler}))
			endpoint.EnableHTTP2 = true
			endpoint.StartTLS()
			defer endpoint.Close()
			data, err := protojson.Marshal(&mcpv1.CreateAssetRequest{Draft: mcpConfigurationDraft()})
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(test.Context(), 8*time.Second)
			defer cancel()
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL+mcpv1connect.MCPConfigurationServiceCreateAssetProcedure, reader)
			if err != nil {
				test.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+fixture.token)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Connect-Protocol-Version", "1")
			result := make(chan error, 1)
			go func() {
				response, err := endpoint.Client().Do(request)
				if err == nil {
					body, readErr := io.ReadAll(response.Body)
					response.Body.Close()
					if response.ProtoMajor != 2 || response.StatusCode != http.StatusUnauthorized || !bytes.Contains(body, []byte("unauthenticated")) {
						err = fmt.Errorf("HTTP2 revoked response status=%d proto=%d", response.StatusCode, response.ProtoMajor)
					} else {
						err = readErr
					}
				}
				result <- err
			}()
			if _, err := writer.Write(data[:1]); err != nil {
				test.Fatal(err)
			}
			mcpWaitActive(test, fixture.handler, 1, 2*time.Second)
			if mode == "cancel" {
				cancel()
			} else {
				if err := fixture.auth.Logout(test.Context(), fixture.token); err != nil {
					test.Fatal(err)
				}
				if _, err := writer.Write(data[1:]); err != nil {
					test.Fatal(err)
				}
				_ = writer.Close()
			}
			select {
			case err := <-result:
				if mode == "cancel" && err == nil || mode == "revoke" && err != nil {
					test.Fatal("HTTP2 cancellation/revocation outcome", err)
				}
			case <-time.After(3 * time.Second):
				test.Fatal("HTTP2 client did not join")
			}
			mcpWaitActive(test, fixture.handler, 0, 3*time.Second)
			assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
			if err != nil || len(assets) != 0 {
				test.Fatal("revoked/canceled body wrote database", err)
			}
		})
	}
}

func mcpWaitActive(test *testing.T, handler *MCPConfigurationHandler, expected int, limit time.Duration) {
	test.Helper()
	deadline := time.Now().Add(limit)
	for {
		handler.mutex.Lock()
		active := len(handler.active)
		handler.mutex.Unlock()
		if active == expected {
			return
		}
		if time.Now().After(deadline) {
			test.Fatalf("active requests=%d expected=%d", active, expected)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMCPConfigurationActualReadDeadline(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	ctx, cancel := context.WithTimeout(test.Context(), 27*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fixture.server.URL+mcpv1connect.MCPConfigurationServiceCreateAssetProcedure, reader)
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	result := make(chan error, 1)
	started := time.Now()
	go func() {
		response, err := fixture.server.Client().Do(request)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusGatewayTimeout || !bytes.Contains(body, []byte("deadline_exceeded")) {
				err = fmt.Errorf("real deadline outcome HTTP %d", response.StatusCode)
			} else {
				err = readErr
			}
		}
		result <- err
	}()
	if _, err := writer.Write([]byte("{")); err != nil {
		test.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(25 * time.Second):
		test.Fatal("production20s body deadline did not terminate")
	}
	if elapsed := time.Since(started); elapsed < 19*time.Second || elapsed > 25*time.Second {
		test.Fatal("not actual production deadline", elapsed)
	}
	mcpWaitActive(test, fixture.handler, 0, time.Second)
	assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
	if err != nil || len(assets) != 0 {
		test.Fatal("incomplete body wrote database", err)
	}
}

type mcpSmallWriteListener struct{ net.Listener }

func (listener mcpSmallWriteListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcp, ok := connection.(*net.TCPConn); ok {
		if err := tcp.SetWriteBuffer(1024); err != nil {
			_ = connection.Close()
			return nil, err
		}
	}
	return connection, nil
}

func TestMCPConfigurationActualWriteDeadline(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	for index := range 250 {
		_, err := fixture.store.CreateConfigurationAsset(test.Context(), fixture.owner, state.ConfigurationAssetDraft{
			AgentID: "claude", Kind: state.ConfigurationKindMCP, Name: fmt.Sprintf("%03d", index) + strings.Repeat("n", 125), SchemaVersion: 99, Payload: state.NewConfigurationPayload([]byte(`{"future":true}`)),
		})
		if err != nil {
			test.Fatal(err)
		}
	}
	endpoint := httptest.NewUnstartedServer(NewWithOptions(fixture.auth, Options{MCPConfiguration: fixture.handler}))
	endpoint.Listener = mcpSmallWriteListener{Listener: endpoint.Listener}
	endpoint.StartTLS()
	defer endpoint.Close()
	connection, err := net.DialTimeout("tcp", endpoint.Listener.Addr().String(), 3*time.Second)
	if err != nil {
		test.Fatal(err)
	}
	defer connection.Close()
	if err := connection.(*net.TCPConn).SetReadBuffer(1024); err != nil {
		test.Fatal(err)
	}
	secured := tls.Client(connection, &tls.Config{InsecureSkipVerify: true})
	if err := secured.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		test.Fatal(err)
	}
	if err := secured.HandshakeContext(test.Context()); err != nil {
		test.Fatal(err)
	}
	started := time.Now()
	if _, err := fmt.Fprintf(secured, "POST %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nConnect-Protocol-Version: 1\r\nContent-Length: 2\r\n\r\n{}", mcpv1connect.MCPConfigurationServiceListAssetsProcedure, endpoint.Listener.Addr(), fixture.token); err != nil {
		test.Fatal(err)
	}
	mcpWaitActive(test, fixture.handler, 1, 3*time.Second)
	mcpWaitActive(test, fixture.handler, 0, 28*time.Second)
	if elapsed := time.Since(started); elapsed < 19*time.Second || elapsed > 28*time.Second {
		test.Fatal("slow TLS sink did not exercise actual write budget", elapsed)
	}
	if err := fixture.handler.Close(); err != nil {
		test.Fatal(err)
	}
}

func TestMCPConfigurationSemanticBoundaries(test *testing.T) {
	cases := []struct {
		name   string
		change func(*mcpv1.PreviewRequest)
	}{
		{"servers33", func(request *mcpv1.PreviewRequest) {
			request.Draft.Document.Servers = nil
			for index := range 33 {
				request.Draft.Document.Servers = append(request.Draft.Document.Servers, &mcpv1.Server{Id: fmt.Sprintf("s%d", index), Name: fmt.Sprintf("s%d", index), Enabled: new(false), Connection: &mcpv1.Server_Stdio{Stdio: &mcpv1.Stdio{Command: "cmd"}}})
			}
		}},
		{"args65", func(request *mcpv1.PreviewRequest) {
			values := []*mcpv1.Arg{}
			for index := range 65 {
				values = append(values, &mcpv1.Arg{Id: fmt.Sprintf("a%d", index), SlotRef: fmt.Sprintf("arg/stdio/a%d", index)})
			}
			request.Draft.Document.Servers[0].GetStdio().Args.Values = values
		}},
		{"env65", func(request *mcpv1.PreviewRequest) {
			values := []*mcpv1.NamedSlot{}
			for index := range 65 {
				values = append(values, &mcpv1.NamedSlot{Name: fmt.Sprintf("E%d", index), SlotRef: fmt.Sprintf("env/stdio/E%d", index)})
			}
			request.Draft.Document.Servers[0].GetStdio().Env.Values = values
		}},
		{"headers33", func(request *mcpv1.PreviewRequest) {
			values := []*mcpv1.NamedSlot{}
			for index := range 33 {
				values = append(values, &mcpv1.NamedSlot{Name: fmt.Sprintf("X-%d", index), SlotRef: fmt.Sprintf("header/http/X-%d", index)})
			}
			request.Draft.Document.Servers[1].GetHttp().Headers.Values = values
		}},
		{"command4097", func(request *mcpv1.PreviewRequest) {
			request.Draft.Document.Servers[0].GetStdio().Command = strings.Repeat("c", 4097)
		}},
		{"url4097", func(request *mcpv1.PreviewRequest) {
			request.Draft.Document.Servers[1].GetHttp().Url = "https://example.test/" + strings.Repeat("p", 4097-len("https://example.test/"))
		}},
		{"ref129", func(request *mcpv1.PreviewRequest) {
			request.Draft.Document.Servers[0].GetStdio().Args.Values[0].SlotRef = strings.Repeat("r", 129)
		}},
		{"wrong-purpose", func(request *mcpv1.PreviewRequest) {
			request.Draft.Document.Servers[0].GetStdio().Args.Values[0].SlotRef = "env/stdio/TOKEN"
		}},
		{"duplicate-slot", func(request *mcpv1.PreviewRequest) {
			request.Secrets = []*mcpv1.SecretMutation{mcpConfigurationReplacements()[0], mcpConfigurationReplacements()[0]}
		}},
		{"unknown-slot", func(request *mcpv1.PreviewRequest) {
			request.Secrets = []*mcpv1.SecretMutation{{SlotId: "arg/stdio/missing", Action: &mcpv1.SecretMutation_Replace{Replace: []byte("synthetic")}}}
		}},
		{"replace8193", func(request *mcpv1.PreviewRequest) {
			request.Secrets = []*mcpv1.SecretMutation{{SlotId: "arg/stdio/a", Action: &mcpv1.SecretMutation_Replace{Replace: bytes.Repeat([]byte{'x'}, 8193)}}}
		}},
		{"mutations1025", func(request *mcpv1.PreviewRequest) {
			for range 1025 {
				request.Secrets = append(request.Secrets, &mcpv1.SecretMutation{SlotId: "arg/stdio/a", Action: &mcpv1.SecretMutation_Keep{Keep: &mcpv1.Keep{}}})
			}
		}},
		{"header-crlf", func(request *mcpv1.PreviewRequest) {
			request.Secrets = []*mcpv1.SecretMutation{{SlotId: "header/http/Authorization", Action: &mcpv1.SecretMutation_Replace{Replace: []byte("value\r\nother")}}}
		}},
		{"secret-nul", func(request *mcpv1.PreviewRequest) {
			request.Secrets = []*mcpv1.SecretMutation{{SlotId: "arg/stdio/a", Action: &mcpv1.SecretMutation_Replace{Replace: []byte{'a', 0}}}}
		}},
		{"unsupported-target", func(request *mcpv1.PreviewRequest) { request.Draft.AgentId = "codex" }},
	}
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			fixture := newMCPConfigurationFixture(test)
			client := fixture.client(encoding)
			for _, entry := range cases {
				test.Run(entry.name, func(test *testing.T) {
					request := &mcpv1.PreviewRequest{Draft: mcpConfigurationDraft()}
					entry.change(request)
					response, err := client.Preview(test.Context(), configurationRequest(request, fixture.token))
					if response != nil || err == nil || connect.CodeOf(err) == connect.CodeUnavailable {
						test.Fatal("semantic invalid/overlimit must reject specifically", err)
					}
				})
			}
			for _, mode := range []string{"document65537", "slots513", "total-secret131073"} {
				test.Run(mode, func(test *testing.T) {
					draft, refs := mcpMaximumDraft(test, "s")
					mutations := []*mcpv1.SecretMutation{}
					switch mode {
					case "document65537":
						for _, server := range draft.Document.Servers {
							if len(server.GetStdio().Command) < 4096 {
								server.GetStdio().Command += "c"
								break
							}
						}
					case "slots513":
						draft.Document.Servers[31].GetStdio().Args.Values = append(draft.Document.Servers[31].GetStdio().Args.Values, &mcpv1.Arg{Id: "extra", SlotRef: "arg/s31/extra"})
					case "total-secret131073":
						for index := range 17 {
							size := 8192
							if index == 16 {
								size = 1
							}
							mutations = append(mutations, &mcpv1.SecretMutation{SlotId: refs[index], Action: &mcpv1.SecretMutation_Replace{Replace: bytes.Repeat([]byte{'x'}, size)}})
						}
					}
					if _, err := client.Preview(test.Context(), configurationRequest(&mcpv1.PreviewRequest{Draft: draft, Secrets: mutations}, fixture.token)); err == nil || connect.CodeOf(err) == connect.CodeUnavailable {
						test.Fatal("combined semantic+1 accepted or misclassified", err)
					}
				})
			}
			assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
			if err != nil || len(assets) != 0 {
				test.Fatal("invalid previews wrote database", err)
			}
		})
	}
}
