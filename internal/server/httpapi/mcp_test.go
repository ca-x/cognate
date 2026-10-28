package httpapi

import (
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/skillhub/gen/skillhub/auth/v1"
	"github.com/ca-x/skillhub/gen/skillhub/auth/v1/authv1connect"
	mcpv1 "github.com/ca-x/skillhub/gen/skillhub/mcp/v1"
	"github.com/ca-x/skillhub/gen/skillhub/mcp/v1/mcpv1connect"
	"github.com/ca-x/skillhub/internal/server/auth"
	"github.com/ca-x/skillhub/internal/server/mcpaccess"
	"github.com/ca-x/skillhub/internal/server/state"
	"google.golang.org/protobuf/encoding/protojson"
)

func mcpRequest[Message any](message *Message, token string) *connect.Request[Message] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+token)
	return request
}

func mcpSession(test *testing.T, service *auth.Service, username string) (string, string) {
	test.Helper()
	password := rand.Text()
	owner, err := service.Register(test.Context(), username, []byte(password))
	if err != nil {
		test.Fatal(err)
	}
	session, err := service.Login(test.Context(), username, []byte(password))
	if err != nil {
		test.Fatal(err)
	}
	return owner, session.Reveal()
}

func TestMCPManagementSessionOwnerAndOneTimeSecrets(test *testing.T) {
	service, store := testServiceAndStore(test)
	owner, session := mcpSession(test, service, "alice")
	_, foreignSession := mcpSession(test, service, "bob")
	access, err := mcpaccess.NewService(store)
	if err != nil {
		test.Fatal(err)
	}
	server := httptest.NewServer(NewWithOptions(service, Options{MCPAccess: access}))
	defer server.Close()
	client := mcpv1connect.NewMCPManagementServiceClient(server.Client(), server.URL)
	spec := &mcpv1.CredentialSpec{Name: "Desktop AI", Scopes: []string{state.MCPCatalogRead}, ExpiresAtUnix: time.Now().Add(time.Hour).Unix()}
	if _, err := client.CreateCredential(test.Context(), connect.NewRequest(&mcpv1.CreateCredentialRequest{Spec: spec})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatalf("anonymous management: %v", err)
	}
	for _, scopes := range [][]string{nil, {"publish:write"}, {"categories:write"}, {""}} {
		_, err := client.CreateCredential(test.Context(), mcpRequest(&mcpv1.CreateCredentialRequest{Spec: &mcpv1.CredentialSpec{Name: "Invalid", Scopes: scopes, ExpiresAtUnix: spec.ExpiresAtUnix}}, session))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			test.Fatal("invalid scope accepted by management RPC")
		}
	}
	created, err := client.CreateCredential(test.Context(), mcpRequest(&mcpv1.CreateCredentialRequest{Spec: spec}, session))
	if err != nil {
		test.Fatal(err)
	}
	credential, raw := created.Msg.Credential, created.Msg.Token
	if credential == nil || credential.Id == "" || !strings.HasPrefix(raw, "smcp1_") || created.Header().Get("Cache-Control") != "no-store" {
		test.Fatal("credential creation did not return one-time secret safely")
	}
	if _, err := client.CreateCredential(test.Context(), mcpRequest(&mcpv1.CreateCredentialRequest{Spec: spec}, raw)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("MCP Bearer substituted for local management session")
	}
	authClient := authv1connect.NewAuthServiceClient(server.Client(), server.URL)
	if _, err := authClient.GetSession(test.Context(), mcpRequest(&authv1.GetSessionRequest{}, raw)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("MCP Bearer substituted for Web session")
	}
	if _, err := client.GetCredential(test.Context(), mcpRequest(&mcpv1.GetCredentialRequest{Id: credential.Id}, foreignSession)); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("foreign credential metadata returned")
	}
	if _, err := client.RotateCredential(test.Context(), mcpRequest(&mcpv1.RotateCredentialRequest{Id: credential.Id, ExpectedRevision: 1}, foreignSession)); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("foreign credential rotated")
	}
	if _, err := client.UpdateCredential(test.Context(), mcpRequest(&mcpv1.UpdateCredentialRequest{Id: credential.Id, ExpectedRevision: 1, Spec: spec}, foreignSession)); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("foreign credential updated")
	}
	if _, err := client.RevokeCredential(test.Context(), mcpRequest(&mcpv1.RevokeCredentialRequest{Id: credential.Id, ExpectedRevision: 1}, foreignSession)); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("foreign credential revoked")
	}
	if _, err := client.ListAudit(test.Context(), mcpRequest(&mcpv1.ListAuditRequest{CredentialId: credential.Id}, foreignSession)); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("foreign credential audit returned")
	}
	foreign, err := client.ListCredentials(test.Context(), mcpRequest(&mcpv1.ListCredentialsRequest{}, foreignSession))
	if err != nil || len(foreign.Msg.Credentials) != 0 {
		test.Fatal("foreign credential list leaked")
	}
	listed, err := client.ListCredentials(test.Context(), mcpRequest(&mcpv1.ListCredentialsRequest{}, session))
	if err != nil || len(listed.Msg.Credentials) != 1 {
		test.Fatal("owner credential list unavailable")
	}
	jsonList, err := protojson.Marshal(listed.Msg)
	if err != nil || strings.Contains(string(jsonList), raw) || strings.Contains(strings.ToLower(string(jsonList)), "tokenhash") {
		test.Fatal("list returned secret material")
	}
	spec.Name, spec.Scopes = "Renamed", []string{state.MCPPacksRead}
	updated, err := client.UpdateCredential(test.Context(), mcpRequest(&mcpv1.UpdateCredentialRequest{Id: credential.Id, ExpectedRevision: 1, Spec: spec}, session))
	if err != nil || updated.Msg.Credential.Revision != 2 || updated.Msg.Credential.Name != "Renamed" {
		test.Fatalf("metadata update: %v", err)
	}
	if _, err := access.Authorize(test.Context(), raw, state.MCPCatalogRead); !errors.Is(err, state.ErrMCPForbidden) {
		test.Fatal("management scope reduction was not immediate")
	}
	if _, err := client.RotateCredential(test.Context(), mcpRequest(&mcpv1.RotateCredentialRequest{Id: credential.Id, ExpectedRevision: 1}, session)); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("stale revision rotated credential")
	}
	rotated, err := client.RotateCredential(test.Context(), mcpRequest(&mcpv1.RotateCredentialRequest{Id: credential.Id, ExpectedRevision: 2}, session))
	if err != nil || rotated.Msg.Token == "" || rotated.Msg.Token == raw || rotated.Msg.Credential.Revision != 3 {
		test.Fatalf("rotation: %v", err)
	}
	if _, err := access.Authorize(test.Context(), raw, ""); !errors.Is(err, state.ErrMCPUnauthorized) {
		test.Fatal("old credential survived management rotation")
	}
	if grant, err := access.Authorize(test.Context(), rotated.Msg.Token, state.MCPPacksRead); err != nil || grant.Owner != owner {
		test.Fatal("new credential not owner bound")
	}
	revoked, err := client.RevokeCredential(test.Context(), mcpRequest(&mcpv1.RevokeCredentialRequest{Id: credential.Id, ExpectedRevision: 3}, session))
	if err != nil || revoked.Msg.Credential.RevokedAtUnix == 0 {
		test.Fatalf("revocation: %v", err)
	}
	if _, err := access.Authorize(test.Context(), rotated.Msg.Token, ""); !errors.Is(err, state.ErrMCPUnauthorized) {
		test.Fatal("revoked credential retained authority")
	}
	audit, err := client.ListAudit(test.Context(), mcpRequest(&mcpv1.ListAuditRequest{CredentialId: credential.Id}, session))
	if err != nil || len(audit.Msg.Entries) != 4 {
		test.Fatalf("audit metadata: %v", err)
	}
	encoded, _ := protojson.Marshal(audit.Msg)
	if strings.Contains(string(encoded), raw) || strings.Contains(string(encoded), rotated.Msg.Token) || strings.Contains(string(encoded), spec.Name) {
		test.Fatal("sensitive request metadata entered audit")
	}
}

func TestMCPManagementOriginAndBodyBounds(test *testing.T) {
	service, store := testServiceAndStore(test)
	_, session := mcpSession(test, service, "alice")
	access, _ := mcpaccess.NewService(store)
	server := httptest.NewServer(NewWithOptions(service, Options{MCPAccess: access}))
	defer server.Close()
	client := mcpv1connect.NewMCPManagementServiceClient(server.Client(), server.URL)
	request := mcpRequest(&mcpv1.ListCredentialsRequest{}, session)
	request.Header().Set("Origin", "https://attacker.invalid")
	if _, err := client.ListCredentials(test.Context(), request); err == nil {
		test.Fatal("cross-origin credential management accepted")
	}
	request.Header().Del("Origin")
	request.Header().Add("Authorization", "Bearer "+session)
	if _, err := client.ListCredentials(test.Context(), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("multiple Authorization headers accepted")
	}
	body := strings.NewReader(`{"spec":{"name":"` + strings.Repeat("a", 5000) + `"}}`)
	httpRequest, _ := http.NewRequestWithContext(test.Context(), "POST", server.URL+mcpv1connect.MCPManagementServiceCreateCredentialProcedure, body)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+session)
	response, err := server.Client().Do(httpRequest)
	if err != nil {
		test.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		test.Fatal("oversized management request accepted")
	}
}
