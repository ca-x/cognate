package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestAuthRunClosedRoutes(test *testing.T) {
	many := append([]string{"run", "--server", "https://example.test", "--", "mcp-assets", "list"}, make([]string, 60)...)
	if _, _, _, err := authArguments(many); err == nil {
		test.Fatal("injected child argv budget not enforced")
	}
	for _, route := range [][]string{{"mcp-assets", "list"}, {"mcp-assets", "update"}, {"pack", "configurations", "replace"}, {"import", "status"}, {"config", "apply", "--target", "codex.config"}, {"config", "apply", "--target=claude.settings"}} {
		arguments := append([]string{"run", "--server", "https://example.test/", "--"}, route...)
		origin, got, native, err := authArguments(arguments)
		if err != nil || origin != "https://example.test" || len(got) != len(route) || native != (route[0] == "config") {
			test.Fatal("approved fixed route refused")
		}
	}
	for _, route := range [][]string{{"sh", "-c"}, {"serve", "x"}, {"auth", "run"}, {"grok-assets", "apply"}, {"config", "status"}, {"config", "apply"}, {"config", "apply", "--target=other"}, {"config", "apply", "--target=codex.config", "--target=codex.config"}, {"mcp-assets", "list", "--server=https://other.test"}, {"mcp-assets", "list", "--token-stdin=false"}, {"mcp-assets", "list", "--owner", "private"}, {"import", "select", "--", "--server"}, {"mcp-assets", "list", strings.Repeat("x", 32768)}} {
		if _, _, _, err := authArguments(append([]string{"run", "--server", "https://example.test", "--"}, route...)); err == nil {
			test.Fatal("unsafe route admitted")
		}
	}
	for _, origin := range []string{"http://public.example", "https://user:pass@example.test", "https://example.test/path", "https://example.test?", "https://example.test#", "https://example.test:0", "https://example.test:", "https://%65xample.test", "https://example.test\\x", "https://[fe80::1%25eth0]", "https://example.test\n"} {
		if _, err := authOrigin(origin); err == nil {
			test.Fatal("unsafe origin admitted")
		}
	}
	for _, origin := range []string{"http://127.0.0.1:123", "http://[::1]:123", "https://Example.TEST/"} {
		if _, err := authOrigin(origin); err != nil {
			test.Fatal("supported origin refused")
		}
	}
}

func TestAuthRunOriginLiteralLoopbackOnly(test *testing.T) {
	for _, origin := range []string{"http://localhost", "http://LOCALHOST", "http://LocalHost:123", "http://localhost.", "http://localhost.localdomain", "http://example.test", "http://[::]", "http://192.0.2.1"} {
		test.Run(origin, func(test *testing.T) {
			if _, err := authOrigin(origin); err == nil {
				test.Error("HTTP origin without literal loopback admitted")
			}
			if _, _, _, err := authArguments([]string{"run", "--server", origin, "--", "mcp-assets", "list"}); err == nil {
				test.Error("route admitted HTTP hostname before credential input")
			}
		})
	}
	for _, origin := range []string{"http://127.0.0.1", "http://127.19.23.1:123", "http://[::1]", "http://[::1]:123", "https://localhost", "https://LOCALHOST", "https://example.test"} {
		test.Run(origin, func(test *testing.T) {
			if _, err := authOrigin(origin); err != nil {
				test.Error("literal loopback HTTP or verified-TLS origin refused")
			}
			if _, _, _, err := authArguments([]string{"run", "--server", origin, "--", "mcp-assets", "list"}); err != nil {
				test.Error("literal loopback HTTP or TLS route refused")
			}
		})
	}
}

func authTestPrivate() *authPrivate {
	private := newAuthPrivate()
	private.material().username = []byte("synthetic-user")
	private.material().password = []byte("auth-private-canary-481763951824")
	return private
}

func TestAuthRunPrivateContainers(test *testing.T) {
	private := authTestPrivate()
	defer private.clear()
	private.material().token = []byte("auth-private-canary-481763951824")
	containers := []any{private, *private, private.material(), *private.material(), []any{private}, map[string]any{"private": private}, struct{ Private *authPrivate }{private}}
	var capture bytes.Buffer
	for _, container := range containers {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%X", "%d", "%q"} {
			_, _ = fmt.Fprintf(&capture, verb, container)
		}
		data, err := json.Marshal(container)
		if err != nil {
			test.Fatal("private JSON failed")
		}
		capture.Write(data)
		slog.New(slog.NewJSONHandler(&capture, nil)).Info("private", "value", container)
		slog.New(slog.NewTextHandler(&capture, nil)).Info("private", "value", container)
	}
	secret := []byte("auth-private-canary-481763951824")
	for _, sentinel := range []string{string(secret), hex.EncodeToString(secret), strings.ToUpper(hex.EncodeToString(secret)), base64.StdEncoding.EncodeToString(secret), fmt.Sprint(secret), fmt.Sprintf("%d", secret)} {
		if strings.Contains(capture.String(), sentinel) {
			test.Fatal("private carrier disclosed canary")
		}
	}
	password, token := private.material().password, private.material().token
	private.clear()
	for _, buffer := range [][]byte{password, token} {
		if !bytes.Equal(buffer, make([]byte, len(buffer))) {
			test.Fatal("owned secret not cleared")
		}
	}
}

func TestAuthRunStrictWire(test *testing.T) {
	private := authTestPrivate()
	defer private.clear()
	codec := authCodec{private: private}
	valid, _ := proto.Marshal(&authv1.GetSessionResponse{UserId: "owner"})
	for _, data := range [][]byte{append(bytes.Clone(valid), valid...), protowire.AppendString(protowire.AppendTag(nil, 9, protowire.BytesType), "hidden"), {10, 1, 255}, {10, 129, 0, 'x'}, {8, 1}, bytes.Repeat([]byte{0}, 4097)} {
		if codec.Unmarshal(data, &authv1.GetSessionResponse{}) == nil {
			test.Fatal("malformed auth wire accepted")
		}
	}
	if codec.Unmarshal(valid, &authv1.GetSessionResponse{}) != nil {
		test.Fatal("valid auth wire refused")
	}
}

func TestAuthRunCodecBorrowedBuffer(test *testing.T) {
	private := authTestPrivate()
	data, _ := proto.Marshal(&authv1.GetSessionResponse{UserId: "synthetic-owner"})
	before := bytes.Clone(data)
	if (authCodec{private: private}).Unmarshal(data, &authv1.GetSessionResponse{}) != nil {
		test.Fatal("valid wire refused")
	}
	private.clear()
	if !bytes.Equal(data, before) {
		test.Fatal("private clear mutated borrowed Connect decode buffer")
	}
}

func TestAuthRunTransportAndIdentity(test *testing.T) {
	for _, mode := range []string{"normal", "wrong-password", "bad-token", "expired", "too-future", "empty-owner", "control-owner", "duplicate", "unknown", "oversize", "redirect", "raw-error", "logout-failure"} {
		test.Run(mode, func(test *testing.T) {
			private := authTestPrivate()
			defer private.clear()
			var logins, identities, logouts, trace atomic.Int32
			token := strings.Repeat("A", 43) + "." + strings.Repeat("B", 43)
			service := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Cookie") != "" || request.Header.Get("Accept-Encoding") != "identity" {
					test.Error("ambient transport inherited")
				}
				var message proto.Message
				switch {
				case strings.HasSuffix(request.URL.Path, "/Login"):
					logins.Add(1)
					body, _ := io.ReadAll(io.LimitReader(request.Body, 4097))
					login := &authv1.LoginRequest{}
					if proto.Unmarshal(body, login) != nil || login.Username != "synthetic-user" || login.Password != "auth-private-canary-481763951824" {
						test.Error("typed credentials changed")
					}
					clear(body)
					login.Reset()
					if mode == "wrong-password" || mode == "raw-error" {
						http.Error(writer, "auth-private-canary-481763951824", 401)
						return
					}
					if mode == "redirect" {
						http.Redirect(writer, request, "http://127.0.0.1:1/private", 307)
						return
					}
					expires := time.Now().Add(24 * time.Hour).Unix()
					value := token
					if mode == "bad-token" {
						value = "invalid"
					}
					if mode == "expired" {
						expires = time.Now().Add(-time.Second).Unix()
					}
					if mode == "too-future" {
						expires = time.Now().Add(25 * time.Hour).Unix()
					}
					message = &authv1.LoginResponse{SessionToken: value, ExpiresAtUnix: expires}
				case strings.HasSuffix(request.URL.Path, "/GetSession"):
					identities.Add(1)
					if request.Header.Get("Authorization") != "Bearer "+token {
						test.Error("identity bearer missing")
					}
					owner := "synthetic-owner"
					if mode == "empty-owner" {
						owner = ""
					}
					if mode == "control-owner" {
						owner = "bad\nowner"
					}
					message = &authv1.GetSessionResponse{UserId: owner}
				case strings.HasSuffix(request.URL.Path, "/Logout"):
					logouts.Add(1)
					if mode == "logout-failure" {
						http.Error(writer, "private logout failure", 503)
						return
					}
					message = &authv1.LogoutResponse{}
				default:
					test.Error("unexpected auth procedure")
					writer.WriteHeader(404)
					return
				}
				data, _ := proto.Marshal(message)
				if strings.HasSuffix(request.URL.Path, "/Login") {
					if mode == "duplicate" {
						data = append(data, data...)
					}
					if mode == "unknown" {
						data = protowire.AppendVarint(protowire.AppendTag(data, 9, protowire.VarintType), 1)
					}
					if mode == "oversize" {
						data = bytes.Repeat([]byte{1}, 4097)
					}
				}
				writer.Header().Set("Content-Type", "application/proto")
				_, _ = writer.Write(data)
			}))
			defer service.Close()
			client := newAuthTransport(service.URL, private)
			defer client.close()
			ctx := httptrace.WithClientTrace(test.Context(), &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { trace.Add(1) }})
			err := client.login(ctx)
			if err == nil {
				err = client.identify(ctx)
			}
			want := mode == "normal" || mode == "logout-failure"
			if (err == nil) != want || logins.Load() != 1 || trace.Load() != 0 {
				test.Fatal("auth refusal, retry or context isolation mismatch")
			}
			if err != nil && strings.Contains(err.Error(), "canary") {
				test.Fatal("raw diagnostic exposed")
			}
			if len(private.material().token) != 0 {
				cleanup := client.logout(test.Context())
				if (cleanup == nil) != (mode != "logout-failure") || logouts.Load() != 1 {
					test.Fatal("cleanup attempt mismatch")
				}
			}
			if mode == "normal" && identities.Load() != 1 {
				test.Fatal("identity not checked")
			}
		})
	}
}

func TestAuthRunTransportCancel(test *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	service := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { close(entered); <-release }))
	defer service.Close()
	defer close(release)
	private := authTestPrivate()
	defer private.clear()
	client := newAuthTransport(service.URL, private)
	defer client.close()
	ctx, cancel := context.WithCancel(test.Context())
	done := make(chan error, 1)
	go func() { done <- client.login(ctx) }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			test.Fatal("cancel accepted")
		}
	case <-time.After(time.Second):
		test.Fatal("auth work not drained")
	}
}
