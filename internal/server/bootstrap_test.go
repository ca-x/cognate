package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/gen/skillhub/management/v1/managementv1connect"
	"github.com/ca-x/cognate/internal/server"
)

func TestAppBootstrapAdministratorAuthorizationAndRestart(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	keyFile := filepath.Join(directory, "master.key")
	passwordFile := filepath.Join(directory, "bootstrap.password")
	password := rand.Text()
	if err := os.WriteFile(keyFile, bytes.Repeat([]byte{79}, 32), 0o600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(passwordFile, []byte(password), 0o600); err != nil {
		test.Fatal(err)
	}
	config := server.Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: keyFile, BootstrapAdminUsername: "admin", BootstrapAdminPasswordFile: passwordFile}
	for round := range 2 {
		func() {
			app, err := server.Open(test.Context(), config)
			if err != nil {
				test.Fatal(err)
			}
			defer app.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithCancel(test.Context())
			finished := make(chan error, 1)
			go func() { finished <- app.Serve(ctx, listener) }()
			defer func() {
				cancel()
				select {
				case err := <-finished:
					if err != nil {
						test.Error(err)
					}
				case <-time.After(5 * time.Second):
					test.Error("bootstrap server failed to stop")
				}
			}()
			baseURL := "http://" + listener.Addr().String()
			client := &http.Client{Timeout: 5 * time.Second}
			authentication := authv1connect.NewAuthServiceClient(client, baseURL)
			login, err := authentication.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "admin", Password: password}))
			if err != nil {
				test.Fatal("administrator did not authenticate with original password")
			}
			management := managementv1connect.NewLogManagementServiceClient(client, baseURL)
			request := connect.NewRequest(&managementv1.GetLogSettingsRequest{})
			request.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
			if _, err := management.GetLogSettings(test.Context(), request); err != nil {
				test.Fatalf("bootstrap administrator was not authorized: %v", err)
			}
			if round == 0 {
				if _, err := authentication.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "ordinary-user", Password: rand.Text()})); err != nil {
					test.Fatal(err)
				}
				if err := os.Remove(passwordFile); err != nil {
					test.Fatal(err)
				}
			}
		}()
	}
}
