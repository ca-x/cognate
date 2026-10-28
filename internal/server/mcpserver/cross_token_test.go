package mcpserver_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/github"
	"github.com/ca-x/skillhub/internal/server/auth"
	"github.com/ca-x/skillhub/internal/server/mcpserver"
	"github.com/ca-x/skillhub/internal/server/packs"
	"github.com/ca-x/skillhub/internal/server/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSDKRejectsKnownSessionPackAndGitHubTokens(test *testing.T) {
	fixture := newFixture(test, nil)
	_, server := fixture.endpoint(test, mcpserver.Options{})
	_, mcpSecret := fixture.token(test, "alice", state.MCPCatalogRead)
	authentication, err := auth.NewService(fixture.store, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	password := []byte("Known-session-test-password")
	if _, err := authentication.Register(test.Context(), "web-user", password); err != nil {
		test.Fatal(err)
	}
	session, err := authentication.Login(test.Context(), "web-user", password)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := authentication.Authenticate(test.Context(), session.Reveal()); err != nil {
		test.Fatal("fixture Web session is not valid")
	}
	collected, err := fixture.library.Collect(test.Context(), "alice", "org/shared", "")
	if err != nil {
		test.Fatal(err)
	}
	details, err := fixture.store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	pack, err := fixture.store.CreatePack(test.Context(), "alice", state.PackDraft{Name: "Shared", Items: []state.PackSelection{{SkillID: details.Skills[0].ID}}})
	if err != nil {
		test.Fatal(err)
	}
	distribution, err := packs.NewService(fixture.store, fixture.library, make([]byte, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer distribution.Close()
	share, err := distribution.CreateShare(test.Context(), "alice", pack.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	githubToken, err := github.NewToken("ghp_"+strings.Repeat("a", 36), []string{"repo"})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.PutCredential(test.Context(), "alice", "github-viewer", "", githubToken); err != nil {
		test.Fatal(err)
	}
	for _, token := range []string{session.Reveal(), share.Token.Reveal(), githubToken.Reveal(), "", "smcp1_" + strings.Repeat("a", 43)} {
		client := mcp.NewClient(&mcp.Implementation{Name: "wrong-token-client", Version: "1"}, nil)
		connected, err := client.Connect(test.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL + mcpserver.Path, HTTPClient: &http.Client{Transport: &bearerTransport{token: token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if err == nil {
			_ = connected.Close()
			test.Fatal("SDK authenticated with a credential from another token domain")
		}
		request, _ := http.NewRequestWithContext(test.Context(), "POST", server.URL+mcpserver.Path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(request)
		if err != nil {
			test.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized || token != "" && bytes.Contains(data, []byte(token)) {
			test.Fatal("cross-token rejection disclosed credential or accepted request")
		}
	}
	if _, err := authentication.Authenticate(test.Context(), mcpSecret.Reveal()); !errors.Is(err, auth.ErrInvalidSession) {
		test.Fatal("MCP credential authorized Web session")
	}
	if _, err := distribution.Manifest(test.Context(), mcpSecret.Reveal()); !errors.Is(err, state.ErrPackShareUnavailable) {
		test.Fatal("MCP credential authorized Pack distribution")
	}
}
