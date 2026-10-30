package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1/mcpv1connect"
	"github.com/ca-x/cognate/internal/server"
)

func TestAppMCPConfigurationClose(test *testing.T) {
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	keyPath := filepath.Join(directory, "master.key")
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte{91}, 32), 0600); err != nil {
		test.Fatal(err)
	}
	app, err := server.Open(test.Context(), server.Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: keyPath})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = app.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	life, cancel := context.WithCancel(test.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- app.Serve(life, listener) }()
	defer func() {
		cancel()
		select {
		case err := <-served:
			if err != nil {
				test.Error(err)
			}
		case <-time.After(3 * time.Second):
			test.Error("actual App Serve did not join")
		}
	}()
	baseURL := "http://" + listener.Addr().String()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	authentication := authv1connect.NewAuthServiceClient(httpClient, baseURL)
	if _, err := authentication.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "mcp-app-owner", Password: "synthetic-private-password-91"})); err != nil {
		test.Fatal(err)
	}
	login, err := authentication.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "mcp-app-owner", Password: "synthetic-private-password-91"}))
	if err != nil {
		test.Fatal(err)
	}
	client := mcpv1connect.NewMCPConfigurationServiceClient(httpClient, baseURL)
	request := connect.NewRequest(&mcpv1.ListAssetsRequest{})
	request.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	if response, err := client.ListAssets(test.Context(), request); err != nil || len(response.Msg.Assets) != 0 {
		test.Fatalf("actual App did not mount owner MCP: %v", err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	written := make(chan struct{})
	go func() { _, _ = writer.Write([]byte("{")); close(written) }()
	responseDone := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(test.Context(), http.MethodPost, baseURL+mcpv1connect.MCPConfigurationServiceCreateAssetProcedure, reader)
		if err != nil {
			responseDone <- err
			return
		}
		req.Header.Set("Authorization", "Bearer "+login.Msg.SessionToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Connect-Protocol-Version", "1")
		response, err := httpClient.Do(req)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode < 400 {
				err = errors.New("incomplete body succeeded")
			}
		}
		responseDone <- err
	}()
	select {
	case <-written:
	case <-time.After(2 * time.Second):
		test.Fatal("slow request writer did not start")
	}
	time.Sleep(100 * time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- app.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		test.Fatal("App.Close did not cancel/join MCP before closing database")
	}
	_ = writer.Close()
	select {
	case <-responseDone:
	case <-time.After(3 * time.Second):
		test.Fatal("actual App slow body did not join")
	}
	late := connect.NewRequest(&mcpv1.ListAssetsRequest{})
	late.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	if _, err := client.ListAssets(test.Context(), late); connect.CodeOf(err) != connect.CodeUnavailable {
		test.Fatalf("App closed handler admitted new request: %v", err)
	}
}

func TestServerPersistsAccountsAndSessionsAcrossRestart(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(base, "master.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	config := server.Config{DataDirectory: filepath.Join(base, "data"), MasterKeyFile: keyPath}
	app, err := server.Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	client, stop := serve(t, app)
	password := rand.Text()
	registered, err := client.Register(t.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "alice", Password: password}))
	if err != nil {
		t.Fatal(err)
	}
	login, err := client.Login(t.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "alice", Password: password}))
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	app, err = server.Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	client, stop = serve(t, app)
	defer stop()
	request := connect.NewRequest(&authv1.GetSessionRequest{})
	request.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	session, err := client.GetSession(t.Context(), request)
	if err != nil || session.Msg.UserId != registered.Msg.UserId {
		t.Fatalf("persisted session: %v", err)
	}
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	if wrong, err := server.Open(t.Context(), config); err == nil {
		_ = wrong.Close()
		t.Fatal("server opened existing database with a different master key")
	}
}

func serve(t *testing.T, app *server.App) (authv1connect.AuthServiceClient, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() { finished <- app.Serve(ctx, listener) }()
	client := authv1connect.NewAuthServiceClient(&http.Client{Timeout: 5 * time.Second}, "http://"+listener.Addr().String())
	return client, func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	}
}

func TestServerRejectsInvalidConfigurationAndCanceledStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := server.Open(ctx, server.Config{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled start: %v", err)
	}
	for _, config := range []server.Config{{}, {DataDirectory: "relative", MasterKeyFile: "/missing"}, {DataDirectory: t.TempDir(), MasterKeyFile: "/missing"}, {DataDirectory: t.TempDir(), TLSCertificate: "cert"}} {
		if app, err := server.Open(t.Context(), config); err == nil {
			_ = app.Close()
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestServerRejectsReadableTLSPrivateKey(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(base, "tls.key")
	if err := os.WriteFile(keyPath, []byte("not-a-real-private-key"), 0o644); err != nil {
		t.Fatal(err)
	}
	app, err := server.Open(t.Context(), server.Config{DataDirectory: filepath.Join(base, "data"), TLSCertificate: filepath.Join(base, "cert.pem"), TLSKey: keyPath})
	if err == nil {
		_ = app.Close()
		t.Fatal("accepted readable TLS private key")
	}
	if !strings.Contains(err.Error(), "TLS private key must be") {
		t.Fatalf("key permissions not checked before loading material: %v", err)
	}
}
