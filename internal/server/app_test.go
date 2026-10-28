package server_test

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/skillhub/gen/skillhub/auth/v1"
	"github.com/ca-x/skillhub/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/skillhub/internal/server"
)

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
