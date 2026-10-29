package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/native/v1/configurationnativev1connect"
)

func nativeAppConfig(test *testing.T) (Config, *x509.CertPool) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	master := filepath.Join(directory, "master.key")
	if err := os.WriteFile(master, bytes.Repeat([]byte{79}, 32), 0600); err != nil {
		test.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		test.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(37), Subject: pkix.Name{CommonName: "synthetic native TLS"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		test.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		test.Fatal(err)
	}
	certificatePath, keyPath := filepath.Join(directory, "tls.crt"), filepath.Join(directory, "tls.key")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded})
	if err := os.WriteFile(certificatePath, certificate, 0600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		test.Fatal(err)
	}
	clear(key)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certificate)
	zero := time.Duration(0)
	return Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: master, TLSCertificate: certificatePath, TLSKey: keyPath, SourceRefreshInterval: &zero}, pool
}

func TestAppNativeDisabledWithoutCanonicalOriginAndLocalTLS(test *testing.T) {
	for _, scenario := range []struct {
		origin   string
		localTLS bool
	}{{"", true}, {"https://cognate.example", false}, {"http://127.0.0.1", true}, {"https://COGNATE.example", true}} {
		config, _ := nativeAppConfig(test)
		config.PublicOrigin = scenario.origin
		if !scenario.localTLS {
			config.TLSCertificate, config.TLSKey = "", ""
			config.AllowHTTP = true
		}
		app, err := Open(test.Context(), config)
		if err != nil {
			test.Fatal(err)
		}
		if app.native != nil {
			test.Fatal("native enabled without canonical HTTPS and local TLS; proxy HTTP was trusted")
		}
		if err := app.Close(); err != nil {
			test.Fatal(err)
		}
	}
}

func TestAppNativeRealTLSServeAndLifecycle(test *testing.T) {
	config, roots := nativeAppConfig(test)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	config.PublicOrigin = "https://" + listener.Addr().String()
	app, err := Open(test.Context(), config)
	if err != nil {
		listener.Close()
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = app.Close() })
	if app.native == nil {
		test.Fatal("approved native TLS deployment was not wired")
	}
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- app.Serve(ctx, listener) }()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	accounts := authv1connect.NewAuthServiceClient(client, config.PublicOrigin)
	if _, err := accounts.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "native-app", Password: "synthetic-native-app-password"})); err != nil {
		test.Fatal("real TLS register failed")
	}
	login, err := accounts.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "native-app", Password: "synthetic-native-app-password"}))
	if err != nil {
		test.Fatal("real TLS login failed")
	}
	session, err := accounts.GetSession(test.Context(), func() *connect.Request[authv1.GetSessionRequest] {
		request := connect.NewRequest(&authv1.GetSessionRequest{})
		request.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
		return request
	}())
	if err != nil {
		test.Fatal("real TLS owner session failed")
	}
	consumer := configurationnativev1connect.NewNativeConfigurationServiceClient(client, config.PublicOrigin)
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	request := connect.NewRequest(&nativev1.BeginNativeConsumerRequest{Purpose: 1, OperationId: bytes.Repeat([]byte{1}, 16), ConsumerPublicKey: public, ClientNonce: bytes.Repeat([]byte{2}, 32), ExpectedOwnerId: session.Msg.UserId, ExpectedServerOrigin: config.PublicOrigin})
	request.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	challenge, err := consumer.BeginNativeConsumer(test.Context(), request)
	if err != nil || challenge.Msg.ServerOrigin != config.PublicOrigin {
		test.Fatal("App did not route actual native bootstrap")
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("App native shutdown did not cancel and drain")
	}
	if err := app.native.Call(test.Context(), func(context.Context) error { test.Error("admitted after Serve stopped"); return nil }); err == nil {
		test.Fatal("native admission survived Serve")
	}
}
