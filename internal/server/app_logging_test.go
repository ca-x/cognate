package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/gen/skillhub/management/v1/managementv1connect"
	"github.com/ca-x/cognate/internal/server"
)

func loggingServerConfig(test *testing.T) server.Config {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal("prepare private server fixture")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		test.Fatal("generate isolated server key")
	}
	defer clear(key)
	keyFile := filepath.Join(directory, "master.key")
	if err := os.WriteFile(keyFile, key, 0o600); err != nil {
		test.Fatal("write isolated server key")
	}
	return server.Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: keyFile, SourceRefreshInterval: new(time.Duration(0))}
}

func loggingEndpoint(test *testing.T, app *server.App) (string, func()) {
	test.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal("listen on isolated server endpoint")
	}
	ctx, cancel := context.WithCancel(test.Context())
	finished := make(chan error, 1)
	go func() { finished <- app.Serve(ctx, listener) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				test.Error("isolated logging server did not shut down cleanly")
			}
		case <-time.After(5 * time.Second):
			_ = listener.Close()
			test.Error("isolated logging server did not drain")
		}
	})
	test.Cleanup(stop)
	return "http://" + listener.Addr().String(), stop
}

func TestServerLogSettingsSurviveRestartWithExplicitAdministrator(test *testing.T) {
	config := loggingServerConfig(test)
	app, err := server.Open(test.Context(), config)
	if err != nil {
		test.Fatal("open bootstrap server")
	}
	endpoint, stop := loggingEndpoint(test, app)
	client := &http.Client{Timeout: 3 * time.Second}
	accounts := authv1connect.NewAuthServiceClient(client, endpoint)
	registered, err := accounts.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "administrator", Password: "synthetic-admin-account-password"}))
	if err != nil {
		test.Fatal("create isolated administrator account")
	}
	login, err := accounts.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "administrator", Password: "synthetic-admin-account-password"}))
	if err != nil {
		test.Fatal("login isolated administrator account")
	}
	logs := managementv1connect.NewLogManagementServiceClient(client, endpoint)
	get := func() *connect.Request[managementv1.GetLogSettingsRequest] {
		request := connect.NewRequest(&managementv1.GetLogSettingsRequest{})
		request.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
		return request
	}
	if _, err := logs.GetLogSettings(test.Context(), get()); connect.CodeOf(err) != connect.CodePermissionDenied {
		test.Fatal("first registered account obtained implicit administrator access")
	}
	stop()
	if err := app.Close(); err != nil {
		test.Fatal("close bootstrap server")
	}
	config.AdminUserIDs = []string{registered.Msg.UserId}
	app, err = server.Open(test.Context(), config)
	if err != nil {
		test.Fatal("open explicitly authorized server")
	}
	endpoint, stop = loggingEndpoint(test, app)
	logs = managementv1connect.NewLogManagementServiceClient(client, endpoint)
	current, err := logs.GetLogSettings(test.Context(), get())
	if err != nil || current.Msg.Settings == nil {
		test.Fatal("explicit administrator could not read settings")
	}
	request := connect.NewRequest(&managementv1.UpdateLogSettingsRequest{ExpectedRevision: current.Msg.Settings.Revision, Settings: &managementv1.LogSettings{Level: "warn", MaxFileMib: 1, MaxBackups: 2, RetentionDays: 3}})
	request.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	updated, err := logs.UpdateLogSettings(test.Context(), request)
	if err != nil || updated.Msg.Settings == nil {
		test.Fatal("explicit administrator could not persist settings")
	}
	stop()
	if err := app.Close(); err != nil {
		test.Fatal("close configured server")
	}
	app, err = server.Open(test.Context(), config)
	if err != nil {
		test.Fatal("reopen configured server")
	}
	test.Cleanup(func() { _ = app.Close() })
	endpoint, stop = loggingEndpoint(test, app)
	defer stop()
	logs = managementv1connect.NewLogManagementServiceClient(client, endpoint)
	restored, err := logs.GetLogSettings(test.Context(), get())
	if err != nil || restored.Msg.Settings == nil || restored.Msg.Settings.Level != "warn" || restored.Msg.Settings.MaxFileMib != 1 || restored.Msg.Settings.MaxBackups != 2 || restored.Msg.Settings.RetentionDays != 3 || restored.Msg.Settings.Revision != updated.Msg.Settings.Revision {
		test.Fatal("administrator settings did not survive actual App restart")
	}
}

func TestServerRejectsInvalidAdministratorIDsBeforeFilesystem(test *testing.T) {
	for _, ids := range [][]string{{""}, {"synthetic-private-invalid-id"}, {"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000001"}, make([]string, 33)} {
		config := loggingServerConfig(test)
		config.AdminUserIDs = ids
		if app, err := server.Open(test.Context(), config); err == nil {
			_ = app.Close()
			test.Fatal("invalid global administrator configuration was accepted")
		} else if strings.Contains(err.Error(), "synthetic-private-invalid-id") {
			test.Fatal("invalid administrator configuration was reflected")
		}
		if _, err := os.Stat(config.DataDirectory); !os.IsNotExist(err) {
			test.Fatal("invalid administrator configuration touched the server data directory")
		}
	}
}

func TestServerWritesPrivateOperationalLogs(test *testing.T) {
	config := loggingServerConfig(test)
	app, err := server.Open(test.Context(), config)
	if err != nil {
		test.Fatal("open isolated logging server")
	}
	client, stop := serve(test, app)
	closed := false
	test.Cleanup(func() {
		if !closed {
			stop()
			_ = app.Close()
		}
	})
	private := "synthetic-server-private-password"
	if _, err := client.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "alice", Password: private})); err != nil {
		test.Fatal("register isolated account")
	}
	login, err := client.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "alice", Password: private}))
	if err != nil {
		test.Fatal("login isolated account")
	}
	stop()
	if err := app.Close(); err != nil {
		test.Fatal("close isolated logging server")
	}
	closed = true
	files, err := os.ReadDir(filepath.Join(config.DataDirectory, "logs"))
	if err != nil {
		test.Fatal("server did not create its private logging directory")
	}
	events := make(map[string]int)
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(config.DataDirectory, "logs", file.Name()))
		if err != nil {
			test.Fatal("read isolated server log")
		}
		if bytes.Contains(data, []byte(private)) || bytes.Contains(data, []byte(login.Msg.SessionToken)) {
			test.Fatal("server logs exposed a synthetic credential")
		}
		for line := range bytes.SplitSeq(data, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var record struct {
				Message string `json:"msg"`
			}
			if err := json.Unmarshal(line, &record); err != nil {
				test.Fatal("server log contains an invalid JSON record")
			}
			events[record.Message]++
		}
	}
	if events["server_started"] != 1 || events["server_stopped"] != 1 || events["request_completed"] < 2 {
		test.Fatal("server lifecycle and actual HTTP requests were not logged")
	}
}
