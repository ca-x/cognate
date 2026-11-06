package httpapi

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/gen/skillhub/management/v1/managementv1connect"
	"github.com/ca-x/cognate/internal/server/githubproxysettings"
)

func TestGitHubProxyManagementAuthorizationAndPersistence(test *testing.T) {
	service, _ := testServiceAndStore(test)
	adminID, session := mcpSession(test, service, "proxy-administrator")
	otherID, otherSession := mcpSession(test, service, "proxy-ordinary")
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{43}, 32)
	manager, err := githubproxysettings.Open(test.Context(), directory, key)
	if err != nil {
		test.Fatal(err)
	}
	defer manager.Close()
	admins := []string{adminID}
	endpoint := httptest.NewServer(NewWithOptions(service, Options{GitHubProxy: manager, AdminUserIDs: admins}))
	defer endpoint.Close()
	admins[0] = otherID
	client := managementv1connect.NewGitHubProxyManagementServiceClient(endpoint.Client(), endpoint.URL)
	for _, token := range []string{"", "invalid-session"} {
		if _, err := client.GetGitHubProxySettings(test.Context(), mcpRequest(&managementv1.GetGitHubProxySettingsRequest{}, token)); connect.CodeOf(err) != connect.CodeUnauthenticated {
			test.Fatal("anonymous caller reached GitHub proxy settings")
		}
	}
	if _, err := client.GetGitHubProxySettings(test.Context(), mcpRequest(&managementv1.GetGitHubProxySettingsRequest{}, otherSession)); connect.CodeOf(err) != connect.CodePermissionDenied {
		test.Fatal("ordinary account reached administrator settings")
	}
	if _, err := client.UpdateGitHubProxySettings(test.Context(), mcpRequest(&managementv1.UpdateGitHubProxySettingsRequest{ExpectedRevision: 1, ProxyUrl: "http://127.0.0.1:7890"}, otherSession)); connect.CodeOf(err) != connect.CodePermissionDenied {
		test.Fatal("ordinary account changed the instance proxy")
	}
	initial, err := client.GetGitHubProxySettings(test.Context(), mcpRequest(&managementv1.GetGitHubProxySettingsRequest{}, session))
	if err != nil || initial.Msg.Settings.Revision != 1 || initial.Msg.Settings.Configured || initial.Msg.Settings.Endpoint != "" || initial.Header().Get("Cache-Control") != "no-store" {
		test.Fatal("default proxy policy unavailable")
	}
	const secret = "SYNTHETIC-PROXY-PRIVATE-43"
	updated, err := client.UpdateGitHubProxySettings(test.Context(), mcpRequest(&managementv1.UpdateGitHubProxySettingsRequest{ExpectedRevision: 1, ProxyUrl: "socks5h://synthetic-user:" + secret + "@127.0.0.1:7890"}, session))
	if err != nil || !updated.Msg.Settings.Configured || updated.Msg.Settings.Endpoint != "socks5h://127.0.0.1:7890" || updated.Msg.Settings.Revision != 2 {
		test.Fatal("administrator proxy update failed")
	}
	if strings.Contains(fmt.Sprint(updated.Msg), secret) || strings.Contains(fmt.Sprint(updated.Msg), "synthetic-user") {
		test.Fatal("proxy update echoed credentials")
	}
	sealed, err := os.ReadFile(filepath.Join(directory, "network", "github-proxy.enc"))
	if err != nil || bytes.Contains(sealed, []byte(secret)) || bytes.Contains(sealed, []byte("synthetic-user")) {
		test.Fatal("proxy credentials were not encrypted at rest")
	}
	if _, err := client.UpdateGitHubProxySettings(test.Context(), mcpRequest(&managementv1.UpdateGitHubProxySettingsRequest{ExpectedRevision: 1}, session)); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("stale proxy update was accepted")
	}
	if _, err := client.UpdateGitHubProxySettings(test.Context(), mcpRequest(&managementv1.UpdateGitHubProxySettingsRequest{ExpectedRevision: 2, ProxyUrl: "https://synthetic-user:" + secret + "@127.0.0.1:7890/download"}, session)); connect.CodeOf(err) != connect.CodeInvalidArgument || strings.Contains(fmt.Sprint(err), secret) {
		test.Fatal("invalid proxy path accepted or exposed a credential")
	}
	endpoint.Close()
	if err := manager.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := githubproxysettings.Open(test.Context(), directory, key)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	saved, err := reopened.Get(test.Context())
	if err != nil || saved.Revision != 2 || !saved.Configured || saved.Endpoint != updated.Msg.Settings.Endpoint {
		test.Fatal("administrator proxy policy did not survive restart")
	}
	reset, err := reopened.Update(test.Context(), 2, "")
	if err != nil || reset.Configured || reset.Endpoint != "" || reset.Revision != 3 {
		test.Fatal("restoring the environment proxy policy failed")
	}
}
