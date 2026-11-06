package githubproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testProxyUser     = "synthetic-proxy-user"
	testProxyPassword = "synthetic-proxy-password"
	testGitHubToken   = "Bearer synthetic-github-token"
)

type requestObservation struct {
	method             string
	uri                string
	host               string
	authorization      string
	proxyAuthorization string
}

func observe(request *http.Request) requestObservation {
	return requestObservation{
		method:             request.Method,
		uri:                request.RequestURI,
		host:               request.Host,
		authorization:      request.Header.Get("Authorization"),
		proxyAuthorization: request.Header.Get("Proxy-Authorization"),
	}
}

func TestHTTPForwardProxyAndHTTPSConnect(test *testing.T) {
	for _, proxyTLS := range []bool{false, true} {
		for _, originTLS := range []bool{false, true} {
			name := fmt.Sprintf("proxy_TLS_%t/origin_TLS_%t", proxyTLS, originTLS)
			test.Run(name, func(test *testing.T) {
				originRequests := make(chan requestObservation, 1)
				origin := newLocalServer(test, originTLS, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					originRequests <- observe(request)
					_, _ = io.WriteString(writer, "forwarded")
				}))
				proxy, proxyRequests := newHTTPProxy(test, origin, proxyTLS)
				proxyURL, err := url.Parse(proxy.URL)
				if err != nil {
					test.Fatal(err)
				}
				proxyURL.User = url.UserPassword(testProxyUser, testProxyPassword)
				transport, err := NewTransport(proxyURL.String())
				if err != nil {
					test.Fatal(err)
				}
				test.Cleanup(transport.CloseIdleConnections)
				trustLocalCertificates(transport, origin, proxy)
				client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
				target := origin.URL + "/repos/example/project?ref=main"
				request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, target, nil)
				if err != nil {
					test.Fatal(err)
				}
				if originTLS {
					request.Header.Set("Authorization", testGitHubToken)
				}
				originalURL := request.URL.Clone()
				originalHost := request.Host
				response, err := client.Do(request)
				if err != nil {
					test.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil || string(body) != "forwarded" || response.StatusCode != http.StatusOK {
					test.Fatalf("unexpected response: status=%d body=%q error=%v", response.StatusCode, body, err)
				}
				if request.URL.String() != originalURL.String() || request.Host != originalHost || request.Header.Get("Proxy-Authorization") != "" {
					test.Fatal("transport changes the caller's request URL, host or proxy authentication")
				}
				proxyRequest := receiveObservation(test, proxyRequests)
				originRequest := receiveObservation(test, originRequests)
				if proxyRequest.proxyAuthorization != "Basic "+base64.StdEncoding.EncodeToString([]byte(testProxyUser+":"+testProxyPassword)) {
					test.Fatal("proxy did not receive its own authentication")
				}
				if originRequest.host != originalHost || originRequest.uri != "/repos/example/project?ref=main" || originRequest.proxyAuthorization != "" {
					test.Fatal("origin received an altered target or proxy credentials")
				}
				if originTLS {
					if proxyRequest.method != http.MethodConnect || proxyRequest.host != originalHost || proxyRequest.uri != originalHost || proxyRequest.authorization != "" || originRequest.authorization != testGitHubToken {
						test.Fatal("HTTPS CONNECT exposes the GitHub token or changes the target")
					}
				} else if proxyRequest.method != http.MethodGet || proxyRequest.uri != target || proxyRequest.host != originalHost {
					test.Fatal("HTTP forward proxy did not receive the original absolute request target")
				}
				assertRedacted(test, transport, proxyURL.Host, testProxyUser, testProxyPassword)
			})
		}
	}
}

func TestTransportVerifiesTLS(test *testing.T) {
	for _, proxyTLS := range []bool{false, true} {
		test.Run(fmt.Sprintf("proxy_TLS_%t", proxyTLS), func(test *testing.T) {
			originRequests := make(chan requestObservation, 1)
			origin := newLocalServer(test, true, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				originRequests <- observe(request)
				writer.WriteHeader(http.StatusOK)
			}))
			proxy, _ := newHTTPProxy(test, origin, proxyTLS)
			transport, err := NewTransport(proxy.URL)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, origin.URL+"/repos", nil)
			if err != nil {
				test.Fatal(err)
			}
			request.Header.Set("Authorization", testGitHubToken)
			response, err := client.Do(request)
			if response != nil || !errors.Is(err, ErrRequestFailed) {
				test.Fatalf("untrusted TLS endpoint was not rejected: %v", err)
			}
			assertRedacted(test, err, proxy.Listener.Addr().String(), testGitHubToken)
			select {
			case <-originRequests:
				test.Fatal("untrusted origin received the GitHub token")
			default:
			}
		})
	}
}

func TestProxyConnectRejectionIsRedacted(test *testing.T) {
	const reason = "private-proxy.example synthetic-proxy-user synthetic-proxy-password"
	proxy := newLocalServer(test, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect || request.Header.Get("Authorization") != "" {
			test.Error("CONNECT sends an unexpected method or origin authorization")
		}
		connection, buffered, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			test.Error(err)
			return
		}
		defer connection.Close()
		_, _ = buffered.WriteString("HTTP/1.1 407 " + reason + "\r\nContent-Length: 0\r\n\r\n")
		_ = buffered.Flush()
	}))
	transport, err := NewTransport(proxy.URL)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(transport.CloseIdleConnections)
	request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, "https://127.0.0.1:9/repos", nil)
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Authorization", testGitHubToken)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if response != nil || !errors.Is(err, ErrRequestFailed) {
		test.Fatalf("CONNECT rejection was not returned as a sanitized error: %v", err)
	}
	assertRedacted(test, err, proxy.Listener.Addr().String(), reason, testProxyUser, testProxyPassword, testGitHubToken)
}

func TestTransportCancellation(test *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		test.Run(scheme, func(test *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			proxy := newLocalServer(test, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				close(entered)
				select {
				case <-request.Context().Done():
				case <-release:
				}
			}))
			test.Cleanup(func() { close(release) })
			transport, err := NewTransport(proxy.URL)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(transport.CloseIdleConnections)
			ctx, cancel := context.WithCancelCause(test.Context())
			test.Cleanup(func() { cancel(nil) })
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://127.0.0.1:9/repos", nil)
			if err != nil {
				test.Fatal(err)
			}
			result := make(chan error, 1)
			var requests sync.WaitGroup
			requests.Go(func() {
				client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
				response, err := client.Do(request)
				if response != nil {
					_ = response.Body.Close()
				}
				result <- err
			})
			defer func() {
				cancel(nil)
				requests.Wait()
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				test.Fatal("request did not reach the controlled proxy")
			}
			cancel(errors.New(testProxyPassword))
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					test.Fatalf("request did not preserve cancellation: %v", err)
				}
				assertRedacted(test, err, proxy.Listener.Addr().String(), testProxyPassword)
			case <-time.After(5 * time.Second):
				test.Fatal("canceled request did not return promptly")
			}
		})
	}
}

func TestTransportExpiredDeadline(test *testing.T) {
	transport, err := NewTransport("http://127.0.0.1:9")
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(transport.CloseIdleConnections)
	ctx, cancel := context.WithDeadlineCause(test.Context(), time.Now().Add(-time.Second), errors.New(testProxyPassword))
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://127.0.0.1:9/repos", nil)
	if err != nil {
		test.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if response != nil || !errors.Is(err, context.DeadlineExceeded) {
		test.Fatalf("expired request did not preserve its deadline error: %v", err)
	}
	assertRedacted(test, err, testProxyPassword)
}

func TestTransportLeavesRedirectPolicyToClient(test *testing.T) {
	originRequests := make(chan requestObservation, 1)
	origin := newLocalServer(test, true, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		originRequests <- observe(request)
		writer.Header().Set("Location", "https://127.0.0.1:9/third-party")
		writer.WriteHeader(http.StatusFound)
	}))
	proxy, proxyRequests := newHTTPProxy(test, origin, false)
	transport, err := NewTransport(proxy.URL)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(transport.CloseIdleConnections)
	trustLocalCertificates(transport, origin)
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, origin.URL+"/repos", nil)
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Authorization", testGitHubToken)
	response, err := client.Do(request)
	if err != nil {
		test.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusFound {
		test.Fatal("transport consumes or follows the redirect")
	}
	if receiveObservation(test, originRequests).authorization != testGitHubToken || receiveObservation(test, proxyRequests).authorization != "" {
		test.Fatal("authorization was not confined to the intended TLS origin")
	}
	select {
	case <-proxyRequests:
		test.Fatal("transport made an additional third-party request")
	default:
	}
}

func TestSOCKS5ForwardProxy(test *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		for _, authenticated := range []bool{false, true} {
			for _, originTLS := range []bool{false, true} {
				test.Run(fmt.Sprintf("%s/auth_%t/origin_TLS_%t", scheme, authenticated, originTLS), func(test *testing.T) {
					originRequests := make(chan requestObservation, 1)
					origin := newLocalServer(test, originTLS, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
						originRequests <- observe(request)
						_, _ = io.WriteString(writer, "socks-forwarded")
					}))
					_, port, err := net.SplitHostPort(origin.Listener.Addr().String())
					if err != nil {
						test.Fatal(err)
					}
					host := "github.synthetic.invalid"
					targetScheme := "http"
					if originTLS {
						if len(origin.Certificate().DNSNames) == 0 {
							test.Fatal("local TLS certificate has no DNS name")
						}
						host = origin.Certificate().DNSNames[0]
						targetScheme = "https"
					}
					target := net.JoinHostPort(host, port)
					proxyAddress := newSOCKS5Proxy(test, origin.Listener.Addr().String(), target, authenticated)
					proxyURL := &url.URL{Scheme: scheme, Host: proxyAddress}
					if authenticated {
						proxyURL.User = url.UserPassword(testProxyUser, testProxyPassword)
					}
					transport, err := NewTransport(proxyURL.String())
					if err != nil {
						test.Fatal(err)
					}
					test.Cleanup(transport.CloseIdleConnections)
					trustLocalCertificates(transport, origin)
					request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, targetScheme+"://"+target+"/repos?ref=main", nil)
					if err != nil {
						test.Fatal(err)
					}
					if originTLS {
						request.Header.Set("Authorization", testGitHubToken)
					}
					client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
					response, err := client.Do(request)
					if err != nil {
						test.Fatal(err)
					}
					body, err := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if err != nil || string(body) != "socks-forwarded" {
						test.Fatalf("SOCKS5 response: %q error=%v", body, err)
					}
					seen := receiveObservation(test, originRequests)
					if seen.host != target || seen.uri != "/repos?ref=main" || seen.proxyAuthorization != "" {
						test.Fatal("SOCKS5 changes the target or leaks proxy credentials to the origin")
					}
					if originTLS && seen.authorization != testGitHubToken {
						test.Fatal("SOCKS5 did not deliver authorization to the intended TLS origin")
					}
				})
			}
		}
	}
}

func newLocalServer(test *testing.T, secure bool, handler http.Handler) *httptest.Server {
	test.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	if secure {
		server.StartTLS()
	} else {
		server.Start()
	}
	test.Cleanup(server.Close)
	return server
}

func trustLocalCertificates(transport *Transport, servers ...*httptest.Server) {
	pool := x509.NewCertPool()
	for _, server := range servers {
		if server.TLS != nil {
			pool.AddCert(server.Certificate())
		}
	}
	transport.base.TLSClientConfig = &tls.Config{RootCAs: pool}
}

func receiveObservation(test *testing.T, observations <-chan requestObservation) requestObservation {
	test.Helper()
	select {
	case observation := <-observations:
		return observation
	case <-time.After(5 * time.Second):
		test.Fatal("controlled server did not observe a request")
		return requestObservation{}
	}
}

func newHTTPProxy(test *testing.T, origin *httptest.Server, secure bool) (*httptest.Server, <-chan requestObservation) {
	test.Helper()
	observations := make(chan requestObservation, 4)
	var tunnels sync.WaitGroup
	direct := &http.Transport{}
	test.Cleanup(direct.CloseIdleConnections)
	proxy := newLocalServer(test, secure, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observations <- observe(request)
		if request.Host != origin.Listener.Addr().String() {
			test.Error("controlled proxy refused a non-local target")
			http.Error(writer, "target refused", http.StatusBadGateway)
			return
		}
		if request.Method == http.MethodConnect {
			tunnels.Add(1)
			defer tunnels.Done()
			upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(request.Context(), "tcp", origin.Listener.Addr().String())
			if err != nil {
				http.Error(writer, "local upstream unavailable", http.StatusBadGateway)
				return
			}
			defer upstream.Close()
			connection, buffered, err := writer.(http.Hijacker).Hijack()
			if err != nil {
				test.Error(err)
				return
			}
			defer connection.Close()
			if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
				return
			}
			if err := buffered.Flush(); err != nil {
				return
			}
			pipeConnections(connection, buffered, upstream)
			return
		}
		if request.URL.Scheme != "http" || request.URL.Host != origin.Listener.Addr().String() {
			test.Error("controlled proxy refused an invalid absolute target")
			http.Error(writer, "target refused", http.StatusBadGateway)
			return
		}
		forward := request.Clone(request.Context())
		forward.RequestURI = ""
		forward.Header.Del("Proxy-Authorization")
		response, err := direct.RoundTrip(forward)
		if err != nil {
			http.Error(writer, "local upstream unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		writer.WriteHeader(response.StatusCode)
		_, _ = io.Copy(writer, response.Body)
	}))
	test.Cleanup(tunnels.Wait)
	return proxy, observations
}

func pipeConnections(connection net.Conn, reader io.Reader, upstream net.Conn) {
	var transfers sync.WaitGroup
	transfers.Go(func() {
		_, _ = io.Copy(upstream, reader)
		_ = upstream.Close()
	})
	_, _ = io.Copy(connection, upstream)
	_ = connection.Close()
	transfers.Wait()
}

func newSOCKS5Proxy(test *testing.T, upstreamAddress, target string, authenticated bool) string {
	test.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
		if err := socksHandshake(connection, target, authenticated); err != nil {
			finished <- err
			return
		}
		upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(test.Context(), "tcp", upstreamAddress)
		if err != nil {
			finished <- err
			return
		}
		defer upstream.Close()
		if _, err := connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
			finished <- err
			return
		}
		pipeConnections(connection, connection, upstream)
		finished <- nil
	}()
	test.Cleanup(func() {
		_ = listener.Close()
		select {
		case err := <-finished:
			if err != nil && !errors.Is(err, net.ErrClosed) {
				test.Errorf("controlled SOCKS5 proxy: %v", err)
			}
		case <-time.After(5 * time.Second):
			test.Error("controlled SOCKS5 proxy did not stop")
		}
	})
	return listener.Addr().String()
}

func socksHandshake(connection net.Conn, target string, authenticated bool) error {
	reader := bufio.NewReader(connection)
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if header[0] != 5 {
		return errors.New("unexpected SOCKS version")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	method := byte(0)
	if authenticated {
		method = 2
	}
	if !strings.ContainsRune(string(methods), rune(method)) {
		return errors.New("required SOCKS authentication method not offered")
	}
	if _, err := connection.Write([]byte{5, method}); err != nil {
		return err
	}
	if authenticated {
		if _, err := io.ReadFull(reader, header); err != nil {
			return err
		}
		username := make([]byte, int(header[1]))
		if _, err := io.ReadFull(reader, username); err != nil {
			return err
		}
		length, err := reader.ReadByte()
		if err != nil {
			return err
		}
		password := make([]byte, int(length))
		if _, err := io.ReadFull(reader, password); err != nil {
			return err
		}
		if header[0] != 1 || string(username) != testProxyUser || string(password) != testProxyPassword {
			return errors.New("unexpected SOCKS proxy authentication")
		}
		if _, err := connection.Write([]byte{1, 0}); err != nil {
			return err
		}
	}
	command := make([]byte, 5)
	if _, err := io.ReadFull(reader, command); err != nil {
		return err
	}
	if command[0] != 5 || command[1] != 1 || command[2] != 0 || command[3] != 3 {
		return errors.New("SOCKS target was not passed to the proxy for remote DNS")
	}
	hostAndPort := make([]byte, int(command[4])+2)
	if _, err := io.ReadFull(reader, hostAndPort); err != nil {
		return err
	}
	host := string(hostAndPort[:len(hostAndPort)-2])
	port := strconv.Itoa(int(binary.BigEndian.Uint16(hostAndPort[len(hostAndPort)-2:])))
	if net.JoinHostPort(host, port) != target {
		return errors.New("unexpected SOCKS target")
	}
	return nil
}
