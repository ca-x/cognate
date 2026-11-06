package githubproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestNewTransportValidURL(test *testing.T) {
	for _, rawURL := range []string{
		"http://localhost",
		"http://127.0.0.1:1",
		"https://proxy.example:65535/",
		"socks5://localhost:1080",
		"socks5h://localhost",
		"http://[::1]",
		"https://[::1]:443/",
		"socks5://[fe80::1%25lo]:1080",
		"HTTP://proxy.example:8080",
		"http://proxy-user:proxy-password@localhost:8080/",
		"https://user%40example:p%3A%2F%3F%23%25@localhost",
		"socks5h://user@localhost:1080",
		"http://" + strings.Repeat("u", maxURLLength-len("http://@localhost")) + "@localhost",
	} {
		test.Run(rawURL[:min(len(rawURL), 80)], func(test *testing.T) {
			transport, err := NewTransport(rawURL)
			if err != nil {
				test.Fatalf("NewTransport: %v", err)
			}
			test.Cleanup(transport.CloseIdleConnections)
			request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, "https://api.github.test/repos", nil)
			if err != nil {
				test.Fatal(err)
			}
			proxyURL, err := transport.base.Proxy(request)
			if err != nil {
				test.Fatal(err)
			}
			expected, err := url.Parse(rawURL)
			if err != nil {
				test.Fatal(err)
			}
			if !reflect.DeepEqual(proxyURL, expected) {
				test.Fatal("transport does not retain the configured proxy")
			}
			assertRedacted(test, transport, "proxy-user", "proxy-password", "proxy.example", "user@example")
		})
	}
}

func TestNewTransportInvalidURL(test *testing.T) {
	cases := map[string]string{
		"whitespace":          " ",
		"leading space":       " http://localhost:8080",
		"trailing space":      "http://localhost:8080 ",
		"missing scheme":      "localhost:8080",
		"scheme relative":     "//localhost:8080",
		"unsupported scheme":  "ftp://localhost:8080",
		"mirror path":         "https://ghproxy.example/https://github.com/example/repo",
		"missing authority":   "http:///",
		"missing host":        "http://",
		"port without host":   "http://:8080",
		"userinfo only":       "http://proxy-user:proxy-password@",
		"opaque URL":          "http:localhost:8080",
		"port zero":           "http://localhost:0",
		"port out of range":   "http://localhost:65536",
		"port overflow":       "http://localhost:9999999999999999999999999",
		"negative port":       "http://localhost:-1",
		"positive port sign":  "http://localhost:+1",
		"nonnumeric port":     "http://localhost:abc",
		"empty port":          "http://localhost:",
		"empty IPv6 port":     "http://[::1]:",
		"unbracketed IPv6":    "http://::1",
		"bad IPv6":            "http://[not-an-IP]:8080",
		"IPv4 brackets":       "http://[127.0.0.1]:8080",
		"missing bracket":     "http://[::1:8080",
		"multiple ports":      "http://localhost:80:90",
		"path":                "http://localhost/path",
		"double slash path":   "http://localhost//",
		"encoded path":        "http://localhost/%2f",
		"dot path":            "http://localhost/.",
		"query":               "http://localhost?secret=proxy-password",
		"empty query":         "http://localhost?",
		"fragment":            "http://localhost#proxy-password",
		"empty fragment":      "http://localhost#",
		"bad escape":          "http://proxy-user:proxy-password%zz@localhost:8080",
		"encoded hostname":    "http://local%68ost:8080",
		"newline":             "http://localhost:8080\n",
		"tab":                 "http://local\thost:8080",
		"null":                "http://localhost\x00:8080",
		"delete":              "http://localhost\x7f:8080",
		"Unicode control":     "http://localhost\u0085:8080",
		"Unicode space":       "http://localhost\u00a0:8080",
		"escaped username LF": "http://proxy%0auser:proxy-password@localhost",
		"escaped password CR": "http://proxy-user:proxy%0dpassword@localhost",
		"escaped password C1": "http://proxy-user:proxy%C2%85password@localhost",
		"invalid UTF-8 user":  "http://proxy%ffuser:password@localhost",
		"invalid UTF-8 pass":  "http://proxy-user:password%ff@localhost",
		"IPv6 zone control":   "http://[fe80::1%25lo%0a]:8080",
		"IPv6 zone space":     "http://[fe80::1%25lo%20]:8080",
		"backslash":           "http://localhost\\evil:8080",
		"invalid UTF-8":       "http://localhost\xff:8080",
		"too long":            "http://" + strings.Repeat("u", maxURLLength) + "@localhost",
	}
	for name, rawURL := range cases {
		test.Run(name, func(test *testing.T) {
			transport, err := NewTransport(rawURL)
			if transport != nil || !errors.Is(err, ErrInvalidURL) {
				test.Fatalf("expected no transport and ErrInvalidURL; got transport=%v error=%v", transport, err)
			}
			assertRedacted(test, err, "localhost", "proxy-user", "proxy-password")
			assertRedacted(test, fmt.Errorf("proxy setup: %w", err), "localhost", "proxy-user", "proxy-password")
			if len(rawURL) > 1 {
				assertRedacted(test, err, rawURL)
			}
			if errors.Unwrap(err) != nil {
				test.Fatal("invalid URL error retains an underlying error")
			}
		})
	}
}

func TestNewTransportClonesDefault(test *testing.T) {
	original := http.DefaultTransport.(*http.Transport)
	before := original.Clone()
	for _, rawURL := range []string{"", "http://proxy-user:proxy-password@localhost:8080"} {
		transport, err := NewTransport(rawURL)
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(transport.CloseIdleConnections)
		if transport.base == original {
			test.Fatal("transport aliases the global default")
		}
		if transport.base.DialContext == nil || reflect.ValueOf(transport.base.DialContext).Pointer() != reflect.ValueOf(before.DialContext).Pointer() {
			test.Fatal("default context-aware dialer is not preserved")
		}
		if transport.base.TLSClientConfig != nil && transport.base.TLSClientConfig.InsecureSkipVerify {
			test.Fatal("transport disables TLS verification")
		}
		if transport.base.TLSHandshakeTimeout != before.TLSHandshakeTimeout || transport.base.IdleConnTimeout != before.IdleConnTimeout || transport.base.ForceAttemptHTTP2 != before.ForceAttemptHTTP2 || transport.base.MaxIdleConns != before.MaxIdleConns {
			test.Fatal("default transport settings are not preserved")
		}
		if rawURL == "" && reflect.ValueOf(transport.base.Proxy).Pointer() != reflect.ValueOf(before.Proxy).Pointer() {
			test.Fatal("empty configuration changes environment proxy handling")
		}
		transport.base.Proxy = nil
		transport.base.MaxIdleConns = 7
		transport.base.TLSClientConfig = &tls.Config{ServerName: "changed.example"}
	}
	if http.DefaultTransport != original || reflect.ValueOf(original.Proxy).Pointer() != reflect.ValueOf(before.Proxy).Pointer() || !reflect.DeepEqual(original.TLSClientConfig, before.TLSClientConfig) || original.MaxIdleConns != before.MaxIdleConns {
		test.Fatal("constructing or mutating the transport changes the global default")
	}
}

func TestNewTransportOpaqueDefault(test *testing.T) {
	original := http.DefaultTransport
	var observed *http.Request
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("synthetic response"))}
	opaque := &opaqueDefaultTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
		observed = request
		return response, nil
	}}
	http.DefaultTransport = opaque
	test.Cleanup(func() { http.DefaultTransport = original })
	transport, err := NewTransport("")
	if err != nil {
		test.Fatal(err)
	}
	request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, "https://api.github.test/repos?ref=synthetic", nil)
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer synthetic-token")
	actual, err := transport.RoundTrip(request)
	if err != nil || actual != response || observed != request || opaque.requests != 1 {
		test.Fatalf("empty settings did not delegate to the caller-owned default: %v", err)
	}
	if err := actual.Body.Close(); err != nil {
		test.Fatal(err)
	}
	transport.CloseIdleConnections()
	transport.CloseIdleConnections()
	if transport.base != nil || http.DefaultTransport != opaque || opaque.idleCloses != 0 || opaque.closes != 0 {
		test.Fatal("wrapper modified or closed the caller-owned default transport")
	}
	if request.URL.String() != "https://api.github.test/repos?ref=synthetic" || request.Host != "api.github.test" || request.Header.Get("Authorization") != "Bearer synthetic-token" || request.Header.Get("Proxy-Authorization") != "" {
		test.Fatal("opaque delegation changed the caller's request")
	}
	assertRedacted(test, transport, "synthetic-token", "api.github.test")
}

func TestNewTransportOpaqueDefaultRejectsExplicitProxy(test *testing.T) {
	original := http.DefaultTransport
	opaque := &opaqueDefaultTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		test.Fatal("explicit proxy configuration was silently delegated")
		return nil, nil
	}}
	http.DefaultTransport = opaque
	test.Cleanup(func() { http.DefaultTransport = original })
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		transport, err := NewTransport(scheme + "://proxy-user:proxy-password@proxy.private.invalid:8080")
		if transport != nil || !errors.Is(err, ErrDefaultTransport) {
			test.Fatalf("explicit proxy was not rejected for an opaque default: %v", err)
		}
		assertRedacted(test, err, "proxy-user", "proxy-password", "proxy.private.invalid")
	}
	if http.DefaultTransport != opaque || opaque.requests != 0 || opaque.idleCloses != 0 || opaque.closes != 0 {
		test.Fatal("rejected configuration changed the caller-owned default")
	}
}

func TestOpaqueDefaultRedactsRuntimeErrors(test *testing.T) {
	const secret = "http://proxy-user:proxy-password@proxy.private.invalid:8080"
	for _, fixture := range []struct {
		name    string
		cause   error
		want    error
		timeout bool
	}{
		{name: "failure", cause: errors.New(secret), want: ErrRequestFailed},
		{name: "timeout", cause: &net.DNSError{Err: secret, Name: secret, IsTimeout: true}, want: ErrRequestFailed, timeout: true},
		{name: "cancel", cause: fmt.Errorf("%s: %w", secret, context.Canceled), want: context.Canceled},
		{name: "deadline", cause: fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), want: context.DeadlineExceeded, timeout: true},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			original := http.DefaultTransport
			opaque := &opaqueDefaultTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				return nil, fixture.cause
			}}
			http.DefaultTransport = opaque
			test.Cleanup(func() { http.DefaultTransport = original })
			transport, err := NewTransport("")
			if err != nil {
				test.Fatal(err)
			}
			request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, "https://api.github.test/repos", nil)
			if err != nil {
				test.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if response != nil || !errors.Is(err, fixture.want) {
				test.Fatalf("opaque runtime error lost its classification: %v", err)
			}
			networkError, ok := errors.AsType[net.Error](err)
			if ok && networkError.Timeout() != fixture.timeout || fixture.timeout && !ok {
				test.Fatal("opaque runtime error lost its timeout classification")
			}
			assertRedacted(test, err, secret, "proxy-user", "proxy-password", "proxy.private.invalid")
			assertRedacted(test, fmt.Errorf("request: %w", err), secret, "proxy-user", "proxy-password", "proxy.private.invalid")
			transport.CloseIdleConnections()
			if opaque.idleCloses != 0 || opaque.closes != 0 {
				test.Fatal("runtime failure closed the caller-owned default")
			}
		})
	}
}

type opaqueDefaultTransport struct {
	roundTrip  func(*http.Request) (*http.Response, error)
	requests   int
	idleCloses int
	closes     int
}

func (transport *opaqueDefaultTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests++
	return transport.roundTrip(request)
}

func (transport *opaqueDefaultTransport) CloseIdleConnections() { transport.idleCloses++ }

func (transport *opaqueDefaultTransport) Close() error {
	transport.closes++
	return nil
}

func TestNewTransportPreservesEnvironmentProxy(test *testing.T) {
	for name, value := range map[string]string{
		"HTTP_PROXY":     "http://127.0.0.1:3128",
		"HTTPS_PROXY":    "http://127.0.0.1:3129",
		"NO_PROXY":       "bypass.example",
		"http_proxy":     "",
		"https_proxy":    "",
		"no_proxy":       "",
		"REQUEST_METHOD": "",
	} {
		test.Setenv(name, value)
	}
	for _, fixture := range []struct {
		target string
		proxy  string
	}{
		{target: "http://api.github.test/repos", proxy: "http://127.0.0.1:3128"},
		{target: "https://api.github.test/repos", proxy: "http://127.0.0.1:3129"},
		{target: "https://bypass.example/repos"},
		{target: "http://127.0.0.1/repos"},
	} {
		request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, fixture.target, nil)
		if err != nil {
			test.Fatal(err)
		}
		transport, err := NewTransport("")
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(transport.CloseIdleConnections)
		proxyURL, err := transport.base.Proxy(request)
		if err != nil {
			test.Fatal(err)
		}
		got := ""
		if proxyURL != nil {
			got = proxyURL.String()
		}
		if got != fixture.proxy {
			test.Fatal("empty proxy configuration does not honor the controlled environment")
		}
		explicit, err := NewTransport("socks5h://localhost:1080")
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(explicit.CloseIdleConnections)
		proxyURL, err = explicit.base.Proxy(request)
		if err != nil || proxyURL == nil || proxyURL.Scheme != "socks5h" || proxyURL.Host != "localhost:1080" {
			test.Fatal("explicit proxy configuration is overridden by the environment")
		}
	}
}

func TestTransportRedactsRuntimeErrors(test *testing.T) {
	const secret = "http://proxy-user:proxy-password@proxy.private.invalid:8080"
	for _, fixture := range []struct {
		name    string
		cause   error
		want    error
		timeout bool
	}{
		{name: "dial", cause: errors.New(secret), want: ErrRequestFailed},
		{name: "timeout", cause: &net.DNSError{Err: secret, Name: secret, IsTimeout: true}, want: ErrRequestFailed, timeout: true},
		{name: "cancel", cause: fmt.Errorf("%s: %w", secret, context.Canceled), want: context.Canceled},
		{name: "deadline", cause: fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), want: context.DeadlineExceeded, timeout: true},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			transport, err := NewTransport(secret)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(transport.CloseIdleConnections)
			transport.base.DialContext = func(context.Context, string, string) (net.Conn, error) {
				return nil, fixture.cause
			}
			request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, "https://api.github.test/repos", nil)
			if err != nil {
				test.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if response != nil || !errors.Is(err, fixture.want) {
				test.Fatalf("unexpected runtime error: %v", err)
			}
			networkError, ok := errors.AsType[net.Error](err)
			if ok && networkError.Timeout() != fixture.timeout || fixture.timeout && !ok {
				test.Fatal("timeout classification changed")
			}
			for current := err; current != nil; current = errors.Unwrap(current) {
				assertRedacted(test, current, secret, "proxy-user", "proxy-password", "proxy.private.invalid")
			}
			assertRedacted(test, fmt.Errorf("request: %w", err), secret, "proxy-user", "proxy-password", "proxy.private.invalid")
			assertRedacted(test, transport, secret, "proxy-user", "proxy-password", "proxy.private.invalid")
		})
	}
}

func assertRedacted(test *testing.T, value any, secrets ...string) {
	test.Helper()
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		printed := fmt.Sprintf(format, value)
		for _, secret := range secrets {
			if secret != "" && strings.Contains(printed, secret) {
				test.Fatalf("format %s reveals sensitive input", format)
			}
		}
	}
}
