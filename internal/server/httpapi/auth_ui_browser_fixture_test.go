package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/cognate/internal/server/oidclogin"
	"github.com/ca-x/cognate/web"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type authUIFixture struct {
	public, control *httptest.Server
	capability      string
	password        string
	owner           string
	cancel          context.CancelFunc
	done            context.Context
	mutex           sync.Mutex
	mode            string
	requests        []map[string]any
}

func newAuthUIFixture(test *testing.T) *authUIFixture {
	test.Helper()
	identity, store := testServiceAndStore(test)
	ctx, cancel := context.WithCancel(test.Context())
	test.Cleanup(cancel)
	fixture := &authUIFixture{capability: rand.Text(), password: rand.Text(), cancel: cancel, done: ctx, mode: "disabled"}
	var err error
	fixture.owner, err = identity.Register(ctx, "auth-fixture", []byte(fixture.password))
	if err != nil {
		test.Fatal(err)
	}
	fixture.public = httptest.NewUnstartedServer(nil)
	origin := "https://" + fixture.public.Listener.Addr().String()
	var issuer *httptest.Server
	issuer = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.MarshalWrite(writer, map[string]any{"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize", "token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/keys", "id_token_signing_alg_values_supported": []string{"ES256"}})
		case "/authorize":
			target, parseErr := url.Parse(request.URL.Query().Get("redirect_uri"))
			if parseErr != nil || target.String() != origin+oidclogin.CallbackPath {
				http.Error(writer, "invalid fixture redirect", http.StatusBadRequest)
				return
			}
			parameters := url.Values{"error": {"access_denied"}, "state": {request.URL.Query().Get("state")}}
			target.RawQuery = parameters.Encode()
			http.Redirect(writer, request, target.String(), http.StatusSeeOther)
		default:
			http.Error(writer, "fixture does not issue tokens", http.StatusNotImplemented)
		}
	}))
	test.Cleanup(issuer.Close)
	manager, err := oidclogin.New(ctx, identity, oidclogin.Config{Issuer: issuer.URL, ClientID: "auth-ui-fixture", PublicOrigin: origin})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(manager.Close)
	disabled := NewWithOptions(identity, Options{PublicOrigin: origin, Store: store, WebUI: web.Handler()})
	enabled := NewWithOptions(identity, Options{PublicOrigin: origin, Store: store, WebUI: web.Handler(), OIDC: manager})
	fixture.public.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		method := strings.TrimPrefix(request.URL.Path, "/skillhub.auth.v1.AuthService/")
		fixture.mutex.Lock()
		mode := fixture.mode
		fixture.mutex.Unlock()
		if strings.HasPrefix(request.URL.Path, "/skillhub.auth.v1.AuthService/") {
			receipt := map[string]any{"method": method}
			if method == "StartOIDCLogin" {
				data, readErr := io.ReadAll(io.LimitReader(request.Body, 4097))
				if readErr != nil || len(data) > 4096 {
					http.Error(writer, "fixture read failed", http.StatusBadRequest)
					return
				}
				request.Body = io.NopCloser(bytes.NewReader(data))
				input := &authv1.StartOIDCLoginRequest{}
				decode := proto.Unmarshal
				if strings.Contains(request.Header.Get("Content-Type"), "json") {
					decode = protojson.Unmarshal
				}
				if err := decode(data, input); err != nil {
					http.Error(writer, "fixture decode failed", http.StatusBadRequest)
					return
				}
				receipt["linkCurrentAccount"] = input.LinkCurrentAccount
			}
			fixture.mutex.Lock()
			fixture.requests = append(fixture.requests, receipt)
			fixture.mutex.Unlock()
			if mode == "options-slow" && method == "GetLoginOptions" || mode == "login-slow" && method == "Login" {
				select {
				case <-time.After(2 * time.Second):
				case <-request.Context().Done():
					return
				}
			}
			if mode == "options-error" && method == "GetLoginOptions" || mode == "oidc-error" && method == "StartOIDCLogin" || mode == "session-error" && method == "GetSession" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(writer, `{"code":"unavailable","message":"synthetic auth fixture failure"}`)
				return
			}
		}
		if mode == "disabled" {
			disabled.ServeHTTP(writer, request)
		} else {
			enabled.ServeHTTP(writer, request)
		}
	})
	fixture.public.StartTLS()
	test.Cleanup(fixture.public.Close)
	fixture.control = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(fixture.capability)) != 1 || request.Header.Get("Origin") != "" || request.URL.RawQuery != "" {
			http.Error(writer, "fixture capability required", http.StatusForbidden)
			return
		}
		fixture.mutex.Lock()
		defer fixture.mutex.Unlock()
		switch request.Method + " " + request.URL.Path {
		case "GET /state":
			_ = json.MarshalWrite(writer, map[string]any{"requests": fixture.requests, "owner": fixture.owner})
		case "POST /mode":
			data, readErr := io.ReadAll(http.MaxBytesReader(writer, request.Body, 64))
			mode := string(data)
			if readErr != nil || !authUIValidMode(mode) {
				http.Error(writer, "unknown fixture mode", http.StatusBadRequest)
				return
			}
			fixture.mode = mode
		case "POST /stop":
			cancel()
		default:
			http.NotFound(writer, request)
		}
	}))
	test.Cleanup(fixture.control.Close)
	return fixture
}

func authUIValidMode(mode string) bool {
	switch mode {
	case "disabled", "enabled", "options-slow", "options-error", "oidc-error", "login-slow", "session-error":
		return true
	default:
		return false
	}
}

func TestAuthUIBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_AUTH_UI_BROWSER_DIR")
	if directory == "" {
		test.Skip("isolated auth UI browser fixture only")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	fixture := newAuthUIFixture(test)
	file, err := root.OpenFile("ready.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		test.Fatal(err)
	}
	err = json.MarshalWrite(file, map[string]any{"url": fixture.public.URL, "controlURL": fixture.control.URL, "capability": fixture.capability, "password": fixture.password})
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		test.Fatal("fixture readiness write failed")
	}
	<-fixture.done.Done()
}

func TestAuthUIFixtureTLSAndControls(test *testing.T) {
	fixture := newAuthUIFixture(test)
	client := authv1connect.NewAuthServiceClient(fixture.public.Client(), fixture.public.URL)
	options, err := client.GetLoginOptions(test.Context(), connect.NewRequest(&authv1.GetLoginOptionsRequest{}))
	if err != nil || options.Msg.OidcEnabled {
		test.Fatalf("disabled options: %v", err)
	}
	fixture.mutex.Lock()
	fixture.mode = "enabled"
	fixture.mutex.Unlock()
	options, err = client.GetLoginOptions(test.Context(), connect.NewRequest(&authv1.GetLoginOptionsRequest{}))
	if err != nil || !options.Msg.OidcEnabled {
		test.Fatalf("enabled options: %v", err)
	}
	response, err := http.Get(fixture.control.URL + "/state")
	if err != nil {
		test.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		test.Fatal("fixture control accepted missing capability")
	}
}
