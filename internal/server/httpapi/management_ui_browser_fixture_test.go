package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/gen/skillhub/management/v1/managementv1connect"
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/mcp/v1"
	"github.com/ca-x/cognate/gen/skillhub/mcp/v1/mcpv1connect"
	"github.com/ca-x/cognate/internal/server/logging"
	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/web"
)

type managementUIFixture struct {
	public, control *httptest.Server
	capability      string
	owner           string
	access          *mcpaccess.Service
	logs            *logging.Manager
	cancel          context.CancelFunc
	done            context.Context
	mutex           sync.Mutex
	rpc, mode       string
	counts          map[string]int
}

func newManagementUIFixture(test *testing.T) *managementUIFixture {
	test.Helper()
	authentication, store := testServiceAndStore(test)
	owner, err := authentication.Register(test.Context(), "management-admin", []byte("Synthetic-browser-only-42"))
	if err != nil {
		test.Fatal("synthetic administrator unavailable")
	}
	if _, err := authentication.Register(test.Context(), "management-member", []byte("Synthetic-browser-only-42")); err != nil {
		test.Fatal("synthetic member unavailable")
	}
	access, err := mcpaccess.NewService(store)
	if err != nil {
		test.Fatal("credential domain unavailable")
	}
	manager, _ := testLogManager(test)
	ctx, cancel := context.WithCancel(test.Context())
	test.Cleanup(cancel)
	fixture := &managementUIFixture{capability: rand.Text(), owner: owner, access: access, logs: manager, cancel: cancel, done: ctx, counts: make(map[string]int)}
	fixture.public = httptest.NewUnstartedServer(nil)
	origin := "https://" + fixture.public.Listener.Addr().String()
	handler := NewWithOptions(authentication, Options{Store: store, MCPAccess: access, Logging: manager, AdminUserIDs: []string{owner}, PublicOrigin: origin, WebUI: web.Handler()})
	fixture.public.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/_fixture/authorize" {
			token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
			if _, err := access.Authorize(request.Context(), token, request.Header.Get("X-Fixture-Scope")); err != nil {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if !strings.Contains(request.URL.Path, "MCPManagementService/") && !strings.Contains(request.URL.Path, "LogManagementService/") {
			handler.ServeHTTP(writer, request)
			return
		}
		_, method, _ := strings.CutLast(request.URL.Path, "/")
		fixture.mutex.Lock()
		fixture.counts[method]++
		mode := ""
		if fixture.rpc == method {
			mode = fixture.mode
		}
		fixture.mutex.Unlock()
		if mode == "slow" {
			select {
			case <-time.After(600 * time.Millisecond):
			case <-request.Context().Done():
				return
			}
		}
		if mode == "error" {
			managementUIError(writer, "unavailable")
			return
		}
		if mode == "conflict" && method == "UpdateLogSettings" {
			current, err := manager.Snapshot(request.Context())
			if err == nil {
				_, err = manager.Update(request.Context(), current.Settings.Revision, current.Settings)
			}
			if err != nil {
				managementUIError(writer, "internal")
				return
			}
		}
		if mode == "drop" {
			recorded := httptest.NewRecorder()
			handler.ServeHTTP(recorded, request)
			if recorded.Code < 200 || recorded.Code >= 300 {
				managementUIError(writer, "internal")
				return
			}
			managementUIError(writer, "unknown")
			return
		}
		handler.ServeHTTP(writer, request)
	})
	fixture.public.Config.ReadHeaderTimeout = 5 * time.Second
	fixture.public.StartTLS()
	test.Cleanup(fixture.public.Close)
	fixture.control = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(fixture.capability)) != 1 {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.Header.Get("Origin") != "" || request.URL.RawQuery != "" {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /state":
			fixture.mutex.Lock()
			defer fixture.mutex.Unlock()
			_ = json.MarshalWrite(writer, map[string]any{"counts": fixture.counts})
		case "POST /mode":
			var input struct{ RPC, Mode string }
			if json.UnmarshalRead(http.MaxBytesReader(writer, request.Body, 256), &input) != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if input.Mode != "" && input.Mode != "error" && input.Mode != "slow" && input.Mode != "drop" && input.Mode != "conflict" {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			fixture.mutex.Lock()
			fixture.rpc, fixture.mode = input.RPC, input.Mode
			fixture.mutex.Unlock()
		case "POST /seed":
			for _, name := range []string{"Fixture expired credential", "Fixture revoked credential"} {
				expiry := time.Now().Add(time.Hour)
				if strings.Contains(name, "expired") {
					expiry = time.Now().Add(time.Second)
				}
				entry, _, err := access.Create(request.Context(), owner, state.MCPMetadata{Name: name, Scopes: []string{state.MCPCatalogRead}, ExpiresAt: expiry})
				if err == nil && strings.Contains(name, "revoked") {
					_, err = access.Revoke(request.Context(), owner, entry.ID, entry.Revision)
				}
				if err != nil {
					writer.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
		case "POST /health-failure":
			if manager.Close() != nil {
				writer.WriteHeader(http.StatusInternalServerError)
			}
		case "POST /health-degraded":
			_ = manager.Record(logging.Event{})
		case "POST /bump-token":
			var input struct{ Name string }
			if json.UnmarshalRead(http.MaxBytesReader(writer, request.Body, 256), &input) != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			entries, err := access.List(request.Context(), owner)
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			for _, entry := range entries {
				if entry.Name == input.Name {
					if _, err := access.Update(request.Context(), owner, entry.ID, entry.Revision, state.MCPMetadata{Name: entry.Name, Scopes: entry.Scopes, ExpiresAt: entry.ExpiresAt}); err != nil {
						writer.WriteHeader(http.StatusInternalServerError)
					}
					return
				}
			}
			writer.WriteHeader(http.StatusNotFound)
		case "POST /stop":
			cancel()
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	test.Cleanup(fixture.control.Close)
	return fixture
}

func managementUIError(writer http.ResponseWriter, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusServiceUnavailable)
	_ = json.MarshalWrite(writer, map[string]string{"code": code, "message": "Synthetic fixture fault"})
}

func TestManagementUIBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_MANAGEMENT_UI_DIR")
	if directory == "" {
		test.Skip("isolated management browser fixture")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal("private fixture directory required")
	}
	defer root.Close()
	fixture := newManagementUIFixture(test)
	ready, err := json.Marshal(map[string]string{"url": fixture.public.URL, "controlURL": fixture.control.URL, "capability": fixture.capability})
	if err != nil {
		test.Fatal("fixture readiness unavailable")
	}
	file, err := root.OpenFile("ready.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		test.Fatal("fixture readiness file unavailable")
	}
	_, written := file.Write(ready)
	closed := file.Close()
	if written != nil || closed != nil {
		test.Fatal("fixture readiness incomplete")
	}
	select {
	case <-fixture.done.Done():
	case <-time.After(8 * time.Minute):
		test.Fatal("fixture stop receipt timeout")
	}
	if test.Context().Err() != nil {
		test.Fatal("fixture canceled without stop")
	}
	test.Log("MANAGEMENT_UI_STOP_RECEIPT")
}

func TestManagementUIFixtureAuthorizationCASAndSecrets(test *testing.T) {
	fixture := newManagementUIFixture(test)
	client := mcpv1connect.NewMCPManagementServiceClient(fixture.public.Client(), fixture.public.URL)
	service := fixture.public.Client()
	login := func(username string) string {
		request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.public.URL+"/skillhub.auth.v1.AuthService/Login", strings.NewReader(`{"username":"`+username+`","password":"Synthetic-browser-only-42"}`))
		if err != nil {
			test.Fatal("synthetic login request unavailable")
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := service.Do(request)
		if err != nil {
			test.Fatal("synthetic login failed")
		}
		defer response.Body.Close()
		var result struct {
			SessionToken string `json:"sessionToken"`
		}
		if json.UnmarshalRead(response.Body, &result) != nil || result.SessionToken == "" {
			test.Fatal("synthetic session missing")
		}
		return result.SessionToken
	}
	admin, member := login("management-admin"), login("management-member")
	created, err := client.CreateCredential(test.Context(), mcpRequest(&mcpv1.CreateCredentialRequest{Spec: &mcpv1.CredentialSpec{Name: "Synthetic authorization check", Scopes: []string{state.MCPCatalogRead}, ExpiresAtUnix: time.Now().Add(time.Hour).Unix()}}, admin))
	if err != nil || created.Msg.Token == "" {
		test.Fatal("real credential creation failed")
	}
	secret := created.Msg.Token
	created.Msg.Token = ""
	entry := created.Msg.Credential
	if _, err := fixture.access.Authorize(test.Context(), secret, state.MCPCatalogRead); err != nil {
		test.Fatal("read grant unavailable")
	}
	if _, err := fixture.access.Authorize(test.Context(), secret, state.MCPCatalogWrite); err == nil {
		test.Fatal("read token acquired write access")
	}
	if _, err := client.GetCredential(test.Context(), mcpRequest(&mcpv1.GetCredentialRequest{Id: entry.Id}, member)); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("foreign credential exposed")
	}
	rotated, err := client.RotateCredential(test.Context(), mcpRequest(&mcpv1.RotateCredentialRequest{Id: entry.Id, ExpectedRevision: entry.Revision}, admin))
	if err != nil || rotated.Msg.Token == "" {
		test.Fatal("real credential rotation failed")
	}
	if _, err := fixture.access.Authorize(test.Context(), secret, state.MCPCatalogRead); err == nil {
		test.Fatal("rotation retained old grant")
	}
	if _, err := client.RevokeCredential(test.Context(), mcpRequest(&mcpv1.RevokeCredentialRequest{Id: entry.Id, ExpectedRevision: entry.Revision}, admin)); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("stale CAS accepted")
	}
	if _, err := client.RevokeCredential(test.Context(), mcpRequest(&mcpv1.RevokeCredentialRequest{Id: entry.Id, ExpectedRevision: rotated.Msg.Credential.Revision}, admin)); err != nil {
		test.Fatal("real revocation failed")
	}
	if _, err := fixture.access.Authorize(test.Context(), rotated.Msg.Token, state.MCPCatalogRead); err == nil {
		test.Fatal("revocation retained new grant")
	}
	rotated.Msg.Token = ""
	expiring, err := client.CreateCredential(test.Context(), mcpRequest(&mcpv1.CreateCredentialRequest{Spec: &mcpv1.CredentialSpec{Name: "Synthetic expiry", Scopes: []string{state.MCPCatalogRead}, ExpiresAtUnix: time.Now().Add(time.Second).Unix()}}, admin))
	if err != nil {
		test.Fatal("expiry fixture creation failed")
	}
	timer := time.NewTimer(time.Until(time.Unix(expiring.Msg.Credential.ExpiresAtUnix, 0)))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-test.Context().Done():
		test.Fatal("expiry fixture canceled")
	}
	if _, err := fixture.access.Authorize(test.Context(), expiring.Msg.Token, state.MCPCatalogRead); err == nil {
		test.Fatal("expired credential retained access")
	}
	expiring.Msg.Token = ""
	logs := managementv1connect.NewLogManagementServiceClient(service, fixture.public.URL)
	if _, err := logs.GetLogSettings(test.Context(), mcpRequest(&managementv1.GetLogSettingsRequest{}, member)); connect.CodeOf(err) != connect.CodePermissionDenied {
		test.Fatal("ordinary member acquired global log access")
	}
	_ = fixture.logs.Record(logging.Event{})
	snapshot, err := logs.GetLogSettings(test.Context(), mcpRequest(&managementv1.GetLogSettingsRequest{}, admin))
	if err != nil || !snapshot.Msg.Health.Degraded || snapshot.Msg.Health.DroppedRecords != 1 {
		test.Fatal("real logger rejection was not observable")
	}
	response, err := service.Get(fixture.public.URL + "/mcp-settings")
	if err != nil {
		test.Fatal("embedded production web unavailable")
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(data), "/assets/") {
		test.Fatal("production assets missing")
	}
	unauthorized, err := http.Get(fixture.control.URL + "/state")
	if err != nil {
		test.Fatal("fixture guard unavailable")
	}
	defer unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		test.Fatal("fixture capability guard bypassed")
	}
}
