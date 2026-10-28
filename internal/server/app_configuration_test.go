package server

import (
	"crypto/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestAppConfigurationSavesWithoutApplyingAndSurvivesRestart(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	home := filepath.Join(directory, "isolated-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		test.Fatal(err)
	}
	test.Setenv("HOME", home)
	keyPath := filepath.Join(directory, "master.key")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		test.Fatal(err)
	}
	clear(key)
	config := Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: keyPath}
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = app.Close() })
	endpoint := httptest.NewServer(app.handler)
	test.Cleanup(endpoint.Close)
	accounts := authv1connect.NewAuthServiceClient(endpoint.Client(), endpoint.URL)
	password := rand.Text()
	if _, err := accounts.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "configuration-app", Password: password})); err != nil {
		test.Fatal("register failed")
	}
	login, err := accounts.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "configuration-app", Password: password}))
	if err != nil {
		test.Fatal("login failed")
	}
	client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
	if _, err := client.ListAssets(test.Context(), connect.NewRequest(&configurationv1.ListAssetsRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatalf("anonymous App configuration: %s", connect.CodeOf(err))
	}
	const secret = "app-configuration-private-sentinel"
	request := connect.NewRequest(&configurationv1.CreateAssetRequest{Draft: &configurationv1.Draft{Name: "Saved, not applied", Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: &configurationv1.ClaudeProvider{AuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY}}}, Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: secret}}}})
	request.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	created, err := client.CreateAsset(test.Context(), request)
	if err != nil {
		test.Fatalf("App configuration create: %s", connect.CodeOf(err))
	}
	encoded, err := protojson.Marshal(created.Msg)
	if err != nil || strings.Contains(string(encoded), secret) {
		test.Fatal("App returned secret")
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		test.Fatal("saving configuration wrote to the client home")
	}
	endpoint.Close()
	if err := app.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = reopened.Close() })
	endpoint = httptest.NewServer(reopened.handler)
	test.Cleanup(endpoint.Close)
	client = configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
	read := connect.NewRequest(&configurationv1.ReadDraftRequest{Id: created.Msg.View.Asset.Id})
	read.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	result, err := client.ReadDraft(test.Context(), read)
	if err != nil || result.Msg.View.Asset.Revision != 1 || !result.Msg.View.Secrets[0].Present {
		test.Fatal("App lost persisted configuration or local session")
	}
	encoded, err = protojson.Marshal(result.Msg)
	if err != nil || strings.Contains(string(encoded), secret) {
		test.Fatal("restart read returned secret")
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		test.Fatal("restart applied configuration")
	}
}
