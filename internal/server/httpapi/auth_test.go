package httpapi

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/state"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	return New(testService(t))
}

func testService(t *testing.T) *auth.Service {
	t.Helper()
	service, _ := testServiceAndStore(t)
	return service
}

func testServiceAndStore(t *testing.T) (*auth.Service, *state.Store) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := auth.NewService(store, key)
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func TestAuthHTTP(t *testing.T) {
	server := httptest.NewServer(testHandler(t))
	t.Cleanup(server.Close)
	client := authv1connect.NewAuthServiceClient(server.Client(), server.URL)
	password := rand.Text()
	_, err := client.Register(t.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "!", Password: password}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid registration: %v", err)
	}
	registered, err := client.Register(t.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "Alice", Password: password}))
	if err != nil || registered.Msg.UserId == "" {
		t.Fatalf("register: %v", err)
	}
	if registered.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("registration response cacheable")
	}
	login, err := client.Login(t.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "alice", Password: password}))
	if err != nil {
		t.Fatal(err)
	}
	if login.Header().Get("Cache-Control") != "no-store" || login.Msg.ExpiresAtUnix <= time.Now().Unix() {
		t.Fatal("invalid login response")
	}
	request := connect.NewRequest(&authv1.GetSessionRequest{})
	request.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	request.Header().Set("X-Forwarded-User", "other")
	session, err := client.GetSession(t.Context(), request)
	if err != nil || session.Msg.UserId != registered.Msg.UserId {
		t.Fatalf("session: %v", err)
	}
	logout := connect.NewRequest(&authv1.LogoutRequest{})
	logout.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	if _, err := client.Logout(t.Context(), logout); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetSession(t.Context(), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("revoked token: %v", err)
	}
	request.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken+"!")
	if _, err := client.GetSession(t.Context(), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tampered token: %v", err)
	}
	request.Header().Del("Authorization")
	if _, err := client.GetSession(t.Context(), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("spoofed identity: %v", err)
	}
	_, err = client.Login(t.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "alice", Password: password + "wrong"}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated || strings.Contains(err.Error(), password) {
		t.Fatalf("invalid login: %v", err)
	}
}

func TestAuthHTTPBoundaries(t *testing.T) {
	server := httptest.NewServer(testHandler(t))
	t.Cleanup(server.Close)
	response, err := server.Client().Get(server.URL + authv1connect.AuthServiceLoginProcedure)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("GET accepted")
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("error response cacheable")
	}
	response, err = server.Client().Post(server.URL+authv1connect.AuthServiceLoginProcedure, "application/json", strings.NewReader(`{"username":"alice","password":"`+strings.Repeat("x", 64*1024)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("oversized request accepted")
	}
}

func TestCanceledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	handler := &authHandler{service: testService(t)}
	_, err := handler.Register(ctx, connect.NewRequest(&authv1.RegisterRequest{Username: "alice", Password: "valid-password"}))
	if connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("canceled request: %v", err)
	}
}

func TestAuthRejectsCrossOriginAndThrottlesRepeatedLogins(t *testing.T) {
	server := httptest.NewServer(testHandler(t))
	t.Cleanup(server.Close)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+authv1connect.AuthServiceLoginProcedure, strings.NewReader(`{"username":"alice","password":"password"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "https://untrusted.example")
	request.Host = "untrusted.example"
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin login status = %d", response.StatusCode)
	}
	client := authv1connect.NewAuthServiceClient(server.Client(), server.URL)
	throttled := false
	for range 11 {
		_, err := client.Login(t.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "!", Password: "password"}))
		if connect.CodeOf(err) == connect.CodeResourceExhausted {
			throttled = true
			break
		}
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("login code = %v", err)
		}
	}
	if !throttled {
		t.Fatal("repeated logins were not rate limited")
	}
}

func TestHostHeaderCannotSelectTrustedBrowserOrigin(t *testing.T) {
	server := httptest.NewServer(testHandler(t))
	t.Cleanup(server.Close)
	for _, origin := range []string{"", "http://attacker.example"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+authv1connect.AuthServiceRegisterProcedure, strings.NewReader(`{"username":"alice","password":"password"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "attacker.example"
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("untrusted host status = %d", response.StatusCode)
		}
	}
}

func TestHealthCheckAndGenericNotFound(t *testing.T) {
	server := httptest.NewServer(testHandler(t))
	t.Cleanup(server.Close)
	for path, status := range map[string]int{"/healthz": http.StatusOK, "/missing": http.StatusNotFound} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s status = %d, error = %v", path, response.StatusCode, err)
		}
		if response.Header.Get("X-Content-Type-Options") != "nosniff" || strings.Contains(string(data), "server.db") {
			t.Fatal("unsafe error response or missing security headers")
		}
	}
}
