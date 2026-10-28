package oidclogin_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/skillhub/gen/skillhub/auth/v1"
	"github.com/ca-x/skillhub/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/skillhub/internal/server/httpapi"
	"github.com/ca-x/skillhub/internal/server/oidclogin"
)

func TestOIDCHTTPCallbackAndCookieHandoff(t *testing.T) {
	fixture, service := newIssuer(t), testAuth(t)
	server := httptest.NewUnstartedServer(nil)
	manager, err := oidclogin.New(t.Context(), service, oidclogin.Config{Issuer: fixture.server.URL, ClientID: "test-client", PublicOrigin: "https://" + server.Listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	server.Config.Handler = httpapi.NewWithOptions(service, httpapi.Options{OIDC: manager})
	server.StartTLS()
	t.Cleanup(server.Close)
	client := server.Client()
	client.Jar, err = cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	rpc := authv1connect.NewAuthServiceClient(client, server.URL)
	options, err := rpc.GetLoginOptions(t.Context(), connect.NewRequest(&authv1.GetLoginOptionsRequest{}))
	if err != nil || !options.Msg.OidcEnabled {
		t.Fatalf("OIDC options: %v", err)
	}
	if _, err := rpc.StartOIDCLogin(t.Context(), connect.NewRequest(&authv1.StartOIDCLoginRequest{LinkCurrentAccount: true})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous identity binding: %v", err)
	}
	started, err := rpc.StartOIDCLogin(t.Context(), connect.NewRequest(&authv1.StartOIDCLoginRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := http.ParseSetCookie(started.Header().Get("Set-Cookie"))
	if err != nil || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != oidclogin.CallbackPath || cookie.Domain != "" {
		t.Fatal("invalid OIDC transaction cookie attributes")
	}
	authorizationURL, err := url.Parse(started.Msg.AuthorizationUrl)
	if err != nil {
		t.Fatal(err)
	}
	fixture.nonce, fixture.challenge = authorizationURL.Query().Get("nonce"), authorizationURL.Query().Get("code_challenge")
	callback := server.URL + oidclogin.CallbackPath + "?" + url.Values{"state": {authorizationURL.Query().Get("state")}, "code": {"issued-code"}}.Encode()
	anonymous := *client
	anonymous.Jar = nil
	response, err := anonymous.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/?oidc=failed" {
		t.Fatal("callback accepted another browser")
	}
	response, err = client.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/?oidc=complete" {
		t.Fatal("callback did not return fixed token-free redirect")
	}
	for _, cookie := range response.Cookies() {
		if !cookie.HttpOnly || !cookie.Secure || cookie.Domain != "" {
			t.Fatal("OIDC callback set unsafe cookie")
		}
	}
	completed, err := rpc.CompleteOIDCLogin(t.Context(), connect.NewRequest(&authv1.CompleteOIDCLoginRequest{}))
	if err != nil || completed.Msg.UserId == "" {
		t.Fatalf("HTTP handoff: %v", err)
	}
	session := connect.NewRequest(&authv1.GetSessionRequest{})
	session.Header().Set("Authorization", "Bearer "+completed.Msg.SessionToken)
	identity, err := rpc.GetSession(t.Context(), session)
	if err != nil || identity.Msg.UserId != completed.Msg.UserId {
		t.Fatalf("HTTP OIDC session: %v", err)
	}
	if _, err := rpc.CompleteOIDCLogin(t.Context(), connect.NewRequest(&authv1.CompleteOIDCLoginRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("HTTP handoff replay: %v", err)
	}
}
