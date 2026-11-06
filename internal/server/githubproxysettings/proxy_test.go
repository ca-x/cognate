package githubproxysettings

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	fixtureVariable = "COGNATE_GITHUB_PROXY_TEST_FIXTURE"
	localToken      = "Bearer synthetic-github-token"
)

func TestManagerLocalProxyLifecycle(test *testing.T) {
	fixture := isolatedTLSFixture(test)
	if fixture == "" {
		return
	}
	certificate, err := tls.LoadX509KeyPair(filepath.Join(fixture, "certificate.pem"), filepath.Join(fixture, "key.pem"))
	if err != nil {
		test.Fatal(err)
	}
	var received atomic.Int64
	var routes sync.Map
	cancelEntered := make(chan struct{})
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		route, exists := routes.Load(request.RemoteAddr)
		if !exists {
			test.Error("origin did not observe a controlled proxy connection")
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		writer.Header().Set("X-Synthetic-Proxy", route.(connectionRoute).proxy.server.URL)
		if request.Header.Get("Authorization") != localToken || request.Header.Get("Proxy-Authorization") != "" {
			test.Error("origin did not receive only its own authorization")
		}
		if request.URL.Path == "/cancel" {
			close(cancelEntered)
			<-request.Context().Done()
			return
		}
		received.Add(1)
		_, _ = io.WriteString(writer, "local-github-response")
	}))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
	origin.Config.ErrorLog = log.New(io.Discard, "", 0)
	origin.StartTLS()
	test.Cleanup(origin.Close)
	first := newConnectProxy(test, origin.Listener.Addr().String(), &routes)
	second := newConnectProxy(test, origin.Listener.Addr().String(), &routes)
	firstURL := localProxyURL(test, first.server.URL, "synthetic-proxy-user", "synthetic-proxy-password")
	secondURL := localProxyURL(test, second.server.URL, "synthetic-proxy-user-second", "synthetic-proxy-password-second")
	for name, value := range map[string]string{
		"HTTP_PROXY": firstURL, "HTTPS_PROXY": firstURL, "NO_PROXY": "",
		"http_proxy": "", "https_proxy": "", "no_proxy": "", "REQUEST_METHOD": "",
	} {
		test.Setenv(name, value)
	}
	defaultTransport := http.DefaultTransport
	directory := testDirectory(test)
	manager, err := Open(test.Context(), directory, testMaster())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = manager.Close() })
	if _, err := manager.Update(test.Context(), 1, firstURL); err != nil {
		test.Fatal(err)
	}
	held, err := localRequest(test.Context(), manager, "api.github.com", "/repos")
	if err != nil {
		test.Fatal(err)
	}
	old := manager.current
	updated, err := manager.Update(test.Context(), 2, secondURL)
	if err != nil || updated.Endpoint != second.server.URL || updated.Revision != 3 {
		test.Fatalf("hot update: %+v, %v", updated, err)
	}
	manager.mutex.Lock()
	oldStillLeased := old.retired && old.active == 1
	manager.mutex.Unlock()
	if !oldStillLeased {
		test.Fatal("hot update did not preserve an in-flight old response")
	}
	if err := consumeLocalResponse(held); err != nil {
		test.Fatal(err)
	}
	select {
	case <-first.closed:
	case <-time.After(5 * time.Second):
		test.Fatal("retired proxy connection was not closed after the response completed")
	}
	response, err := localRequest(test.Context(), manager, "codeload.github.com", "/archive")
	if err != nil {
		test.Fatal(err)
	}
	if err := consumeLocalResponse(response); err != nil {
		test.Fatal(err)
	}
	if first.requests.Load() != 1 || second.requests.Load() != 1 {
		test.Fatal("requests did not move from the first proxy to the second")
	}
	assertProxyAuthorization(test, first.observed, "synthetic-proxy-user", "synthetic-proxy-password")
	assertProxyAuthorization(test, second.observed, "synthetic-proxy-user-second", "synthetic-proxy-password-second")
	assertPrivate(test, manager)
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			for range 3 {
				response, err := localRequest(test.Context(), manager, "github.com", "/repos")
				if err == nil {
					err = consumeLocalResponse(response)
				}
				if err != nil {
					test.Errorf("concurrent local proxy request: %v", err)
				}
			}
		})
	}
	for index := range 4 {
		nextURL := firstURL
		if index%2 != 0 {
			nextURL = secondURL
		}
		updated, err = manager.Update(test.Context(), updated.Revision, nextURL)
		if err != nil {
			test.Error(err)
		}
	}
	workers.Wait()
	if received.Load() != 38 {
		test.Fatalf("concurrent requests were lost: received=%d", received.Load())
	}
	if err := manager.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), directory, testMaster())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = reopened.Close() })
	if snapshot, err := reopened.Get(test.Context()); err != nil || snapshot != updated {
		test.Fatal("restart did not restore the active proxy revision")
	}
	response, err = localRequest(test.Context(), reopened, "api.github.com", "/repos")
	if err != nil {
		test.Fatal(err)
	}
	if err := consumeLocalResponse(response); err != nil || response.Header.Get("X-Synthetic-Proxy") != second.server.URL {
		test.Fatalf("restart did not restore actual proxy routing: %v", err)
	}
	reset, err := reopened.Update(test.Context(), updated.Revision, "")
	if err != nil || reset.Configured || reset.Endpoint != "" {
		test.Fatalf("environment reset: %+v, %v", reset, err)
	}
	response, err = localRequest(test.Context(), reopened, "github.com", "/repos")
	if err != nil {
		test.Fatal(err)
	}
	if err := consumeLocalResponse(response); err != nil || response.Header.Get("X-Synthetic-Proxy") != first.server.URL {
		test.Fatalf("empty setting did not route through the environment proxy: %v", err)
	}
	ctx, cancel := context.WithCancelCause(test.Context())
	defer cancel(nil)
	result := make(chan error, 1)
	go func() {
		response, err := localRequest(ctx, reopened, "api.github.com", "/cancel")
		if response != nil {
			_ = response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-cancelEntered:
	case <-time.After(5 * time.Second):
		test.Fatal("controlled cancellation request did not reach the local TLS origin")
	}
	cancel(errors.New("synthetic-proxy-password"))
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			test.Fatalf("local proxy cancellation: %v", err)
		}
		assertPrivate(test, err)
	case <-time.After(5 * time.Second):
		test.Fatal("local proxy cancellation did not return promptly")
	}
	if http.DefaultTransport != defaultTransport {
		test.Fatal("manager replaced the global HTTP transport")
	}
}

func localRequest(ctx context.Context, manager *Manager, host, path string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", localToken)
	return manager.RoundTrip(request)
}

func consumeLocalResponse(response *http.Response) error {
	contents, readError := io.ReadAll(response.Body)
	closeError := response.Body.Close()
	if readError != nil || closeError != nil {
		return errors.New("cannot consume local response")
	}
	if response.StatusCode != http.StatusOK || string(contents) != "local-github-response" {
		return errors.New("unexpected local response")
	}
	return nil
}

func localProxyURL(test *testing.T, address, username, password string) string {
	test.Helper()
	proxyURL, err := url.Parse(address)
	if err != nil {
		test.Fatal(err)
	}
	proxyURL.User = url.UserPassword(username, password)
	return proxyURL.String()
}

type proxyObservation struct {
	target             string
	authorization      string
	proxyAuthorization string
}

type connectProxy struct {
	server   *httptest.Server
	requests atomic.Int64
	observed chan proxyObservation
	closed   chan struct{}
	tunnels  sync.WaitGroup
}

type connectionRoute struct {
	proxy  *connectProxy
	target string
}

func newConnectProxy(test *testing.T, upstreamAddress string, routes *sync.Map) *connectProxy {
	test.Helper()
	proxy := &connectProxy{observed: make(chan proxyObservation, 128), closed: make(chan struct{}, 128)}
	proxy.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch strings.ToLower(request.Host) {
		case "api.github.com:443", "codeload.github.com:443", "github.com:443":
		default:
			test.Error("controlled proxy refused an unexpected destination")
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		if request.Method != http.MethodConnect {
			test.Error("controlled proxy received a non-CONNECT request")
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		proxy.tunnels.Add(1)
		defer proxy.tunnels.Done()
		proxy.requests.Add(1)
		proxy.observed <- proxyObservation{target: request.Host, authorization: request.Header.Get("Authorization"), proxyAuthorization: request.Header.Get("Proxy-Authorization")}
		upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(request.Context(), "tcp", upstreamAddress)
		if err != nil {
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		peer := upstream.LocalAddr().String()
		routes.Store(peer, connectionRoute{proxy: proxy, target: request.Host})
		defer routes.Delete(peer)
		connection, buffered, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			test.Error(err)
			return
		}
		defer connection.Close()
		defer func() { proxy.closed <- struct{}{} }()
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if err := buffered.Flush(); err != nil {
			return
		}
		var transfers sync.WaitGroup
		transfers.Go(func() {
			_, _ = io.Copy(upstream, buffered)
			_ = upstream.Close()
		})
		_, _ = io.Copy(connection, upstream)
		_ = connection.Close()
		transfers.Wait()
	}))
	test.Cleanup(proxy.server.Close)
	test.Cleanup(proxy.tunnels.Wait)
	return proxy
}

func isolatedTLSFixture(test *testing.T) string {
	test.Helper()
	if fixture := os.Getenv(fixtureVariable); fixture != "" {
		return fixture
	}
	fixture := makeTLSFixture(test)
	command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^"+test.Name()+"$", "-test.count=1", "-test.timeout=45s")
	command.Env = []string{
		fixtureVariable + "=" + fixture,
		"SSL_CERT_FILE=" + filepath.Join(fixture, "certificate.pem"),
		"SSL_CERT_DIR=" + filepath.Join(fixture, "roots"),
	}
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("isolated local proxy verification: %v\n%s", err, output)
	}
	return ""
}

func assertProxyAuthorization(test *testing.T, observations <-chan proxyObservation, username, password string) {
	test.Helper()
	select {
	case observed := <-observations:
		if observed.authorization != "" || observed.proxyAuthorization != "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)) {
			test.Fatal("CONNECT leaked origin authorization or lost proxy authentication")
		}
	case <-time.After(5 * time.Second):
		test.Fatal("controlled proxy did not observe CONNECT")
	}
}

func makeTLSFixture(test *testing.T) string {
	test.Helper()
	directory := testDirectory(test)
	if err := os.Mkdir(filepath.Join(directory, "roots"), 0o700); err != nil {
		test.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		test.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "synthetic GitHub proxy fixture"},
		DNSNames:              []string{"api.github.com", "codeload.github.com", "github.com"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		test.Fatal(err)
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "certificate.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0o600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}), 0o600); err != nil {
		test.Fatal(err)
	}
	clear(encodedKey)
	return directory
}
