package server

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/skillhub/gen/skillhub/auth/v1"
	"github.com/ca-x/skillhub/gen/skillhub/auth/v1/authv1connect"
	mcpv1 "github.com/ca-x/skillhub/gen/skillhub/mcp/v1"
	"github.com/ca-x/skillhub/gen/skillhub/mcp/v1/mcpv1connect"
	"github.com/ca-x/skillhub/internal/server/mcpserver"
	"github.com/ca-x/skillhub/internal/server/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type appMCPTransport struct{ token string }

func (transport appMCPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+transport.token)
	return http.DefaultTransport.RoundTrip(request)
}

func TestAppConnectsMCPManagementAndOfficialSDK(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(directory, "master.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	clear(key)
	app, err := Open(t.Context(), Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: keyPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	endpoint := httptest.NewServer(app.handler)
	t.Cleanup(endpoint.Close)
	client := endpoint.Client()
	client.Timeout = 5 * time.Second
	accounts := authv1connect.NewAuthServiceClient(client, endpoint.URL)
	password := rand.Text()
	if _, err := accounts.Register(t.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "mcp-owner", Password: password})); err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(t.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "mcp-owner", Password: password}))
	if err != nil {
		t.Fatal(err)
	}
	management := mcpv1connect.NewMCPManagementServiceClient(client, endpoint.URL)
	create := connect.NewRequest(&mcpv1.CreateCredentialRequest{Spec: &mcpv1.CredentialSpec{Name: "App integration", Scopes: []string{state.MCPCatalogRead}, ExpiresAtUnix: time.Now().Add(time.Hour).Unix()}})
	if _, err := management.CreateCredential(t.Context(), create); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous MCP management must be denied: %s", connect.CodeOf(err))
	}
	create.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	created, err := management.CreateCredential(t.Context(), create)
	if err != nil {
		t.Fatalf("MCP management is not connected to App: %s", connect.CodeOf(err))
	}
	if created.Msg.Credential == nil || created.Msg.Token == "" {
		t.Fatal("MCP credential was not issued")
	}
	sdk := mcp.NewClient(&mcp.Implementation{Name: "app-integration", Version: "1"}, nil)
	session, err := sdk.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint.URL + mcpserver.Path, HTTPClient: &http.Client{Transport: appMCPTransport{token: created.Msg.Token}, Timeout: 5 * time.Second}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal("official MCP SDK could not connect to App")
	}
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) == 0 {
		t.Fatal("official MCP SDK could not discover tools")
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "catalog_list", Arguments: mcpserver.PageInput{}})
	if err != nil || result.IsError {
		t.Fatal("authorized MCP catalog read failed through App")
	}
	revoke := connect.NewRequest(&mcpv1.RevokeCredentialRequest{Id: created.Msg.Credential.Id, ExpectedRevision: created.Msg.Credential.Revision})
	revoke.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	if _, err := management.RevokeCredential(t.Context(), revoke); err != nil {
		t.Fatalf("MCP revoke failed: %s", connect.CodeOf(err))
	}
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "catalog_list", Arguments: mcpserver.PageInput{}}); err == nil {
		t.Fatal("same MCP SDK client retained revoked authority")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint.URL+mcpserver.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("closed MCP endpoint did not finish its response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("App.Close left MCP available: %d", response.StatusCode)
	}
}
