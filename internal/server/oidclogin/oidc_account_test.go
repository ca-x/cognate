package oidclogin_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/oidclogin"
)

func TestAuthenticatedOIDCLoginBindsWithoutExplicitFlag(test *testing.T) {
	fixture, service := newIssuer(test), testAuth(test)
	owner, err := service.Register(test.Context(), "existing-user", []byte("synthetic-password-123"))
	if err != nil {
		test.Fatal(err)
	}
	session, err := service.Login(test.Context(), "existing-user", []byte("synthetic-password-123"))
	if err != nil {
		test.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	manager, err := oidclogin.New(test.Context(), service, oidclogin.Config{Issuer: fixture.server.URL, ClientID: "test-client", PublicOrigin: "https://" + server.Listener.Addr().String()})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(manager.Close)
	server.Config.Handler = httpapi.NewWithOptions(service, httpapi.Options{OIDC: manager})
	server.StartTLS()
	test.Cleanup(server.Close)
	rpc := authv1connect.NewAuthServiceClient(server.Client(), server.URL)
	request := connect.NewRequest(&authv1.StartOIDCLoginRequest{})
	request.Header().Set("Authorization", "Bearer "+session.Reveal())
	started, err := rpc.StartOIDCLogin(test.Context(), request)
	if err != nil {
		test.Fatal(err)
	}
	cookie, err := http.ParseSetCookie(started.Header().Get("Set-Cookie"))
	if err != nil {
		test.Fatal(err)
	}
	address, err := url.Parse(started.Msg.AuthorizationUrl)
	if err != nil {
		test.Fatal(err)
	}
	fixture.nonce, fixture.challenge = address.Query().Get("nonce"), address.Query().Get("code_challenge")
	result, err := manager.Complete(test.Context(), address.Query().Get("state"), cookie.Value, "issued-code")
	if err != nil {
		test.Fatal(err)
	}
	_, actual, err := manager.Redeem(test.Context(), result)
	if err != nil || actual != owner {
		test.Fatal("authenticated OIDC login created or selected a different user")
	}
	listed := connect.NewRequest(&authv1.GetOIDCBindingsRequest{})
	listed.Header().Set("Authorization", "Bearer "+session.Reveal())
	bindings, err := rpc.GetOIDCBindings(test.Context(), listed)
	if err != nil || !bindings.Msg.CanUnlink || len(bindings.Msg.Bindings) != 1 {
		test.Fatal("linked identity was not listed for its owner")
	}
	unlink := connect.NewRequest(&authv1.UnlinkOIDCRequest{IdentityId: bindings.Msg.Bindings[0].IdentityId, Password: "wrong-password"})
	unlink.Header().Set("Authorization", "Bearer "+session.Reveal())
	if _, err := rpc.UnlinkOIDC(test.Context(), unlink); connect.CodeOf(err) != connect.CodeInvalidArgument {
		test.Fatal("unlink accepted an invalid password")
	}
	unlink.Msg.Password = "synthetic-password-123"
	if _, err := rpc.UnlinkOIDC(test.Context(), unlink); err != nil {
		test.Fatal("password-confirmed unlink failed")
	}
	bindings, err = rpc.GetOIDCBindings(test.Context(), listed)
	if err != nil || len(bindings.Msg.Bindings) != 0 {
		test.Fatal("unlinked identity remained or current session was revoked")
	}
	if _, err := service.Login(test.Context(), "existing-user", []byte("synthetic-password-123")); err != nil {
		test.Fatal("unlink removed the password login")
	}
}
