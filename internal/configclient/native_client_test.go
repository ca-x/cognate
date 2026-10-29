package configclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/configuration"
)

func TestNativeClientRejectsOriginsAndAmbientHooks(test *testing.T) {
	for _, origin := range []string{"http://127.0.0.1:1234", "https://user:synthetic-secret@example.com", "https://example.com/", "https://example.com?key=synthetic-secret", "https://example.com#secret", "https://EXAMPLE.com", "https://example.com:0443", "https://example.com\\evil"} {
		client, err := NewNativeClient(origin, nil)
		if !errors.Is(err, ErrNativeInvalid) || client != nil {
			test.Fatal("unsafe origin accepted")
		}
	}
	var hooks atomic.Int64
	settings := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { hooks.Add(1); return nil, nil }, DialContext: func(context.Context, string, string) (net.Conn, error) {
		hooks.Add(1)
		return nil, errors.New("synthetic-private-hook")
	}, TLSClientConfig: &tls.Config{InsecureSkipVerify: true, VerifyConnection: func(tls.ConnectionState) error { hooks.Add(1); return nil }, KeyLogWriter: io.Discard}}
	client, err := NewNativeClient("https://127.0.0.1:1", settings)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	if client.transport.Proxy != nil || client.transport.TLSClientConfig.InsecureSkipVerify || client.transport.TLSClientConfig.VerifyConnection != nil || client.transport.TLSClientConfig.KeyLogWriter != nil || client.http.Jar != nil {
		test.Fatal("unsafe inherited transport configuration")
	}
	_, err = client.OpenConsumer(test.Context(), "owner", "synthetic-owner")
	if !errors.Is(err, ErrNativeUnavailable) || hooks.Load() != 0 {
		test.Fatal("arbitrary transport callback inherited")
	}
}

func TestNativeClientRedactionEveryVerbAndContainer(test *testing.T) {
	secret := []byte(nativeTestSecret)
	material := NativeMaterial{newNativeSecret(secret)}
	defer material.Destroy()
	encodings := []string{nativeTestSecret, hex.EncodeToString(secret), base64.StdEncoding.EncodeToString(secret), fmt.Sprint(secret), fmt.Sprintf("%#v", secret)}
	private := struct{ value NativeMaterial }{material}
	for _, value := range []any{material, &material, private, &private, []NativeMaterial{material}, map[string]NativeMaterial{"selected": material}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%t", "%f", "%e", "%g", "%b", "%o", "%U", "%c", "%p"} {
			output := fmt.Sprintf(verb, value)
			if verb != "%p" && strings.Contains(output, fmt.Sprintf(verb, secret)) {
				test.Fatal("fmt exposed per-verb byte representation")
			}
			for _, encoding := range encodings {
				if strings.Contains(output, encoding) {
					test.Fatal("fmt leaked material through private container")
				}
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil && strings.Contains(err.Error(), nativeTestSecret) {
			test.Fatal("JSON error leaked material")
		}
		switch value.(type) {
		case NativeMaterial, *NativeMaterial:
			if err != nil {
				test.Fatal("public material JSON failed")
			}
		}
		if bytes.Contains(encoded, secret) {
			test.Fatal("JSON leaked material")
		}
		var logs bytes.Buffer
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("synthetic", "value", value)
		slog.New(slog.NewTextHandler(&logs, nil)).Info("synthetic", "value", value)
		log.New(&logs, "", 0).Printf("%#v", value)
		for _, encoding := range encodings {
			if strings.Contains(logs.String(), encoding) {
				test.Fatal("log/slog leaked material")
			}
		}
	}
	clone := material.CloneBytes()
	clone[0] ^= 1
	if bytes.Equal(clone, material.CloneBytes()) {
		test.Fatal("CloneBytes aliases owned material")
	}
	clear(clone)
	copyMaterial := material
	independent := material.Clone()
	defer independent.Destroy()
	material.Destroy()
	if len(copyMaterial.CloneBytes()) != 0 {
		test.Fatal("Destroy did not invalidate wrapper aliases")
	}
	if string(independent.CloneBytes()) != nativeTestSecret {
		test.Fatal("independent clone destroyed through original")
	}
}

func TestNativeClientMasksTraceContextAndNoRedirect(test *testing.T) {
	var traced atomic.Int64
	fixture := newNativeClientFixture(test, false, nil)
	ctx := httptrace.WithClientTrace(test.Context(), &httptrace.ClientTrace{WroteHeaderField: func(string, []string) { traced.Add(1) }, GotConn: func(httptrace.GotConnInfo) { traced.Add(1) }})
	flow, err := fixture.client.OpenConsumer(ctx, fixture.owner, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	flow.Close()
	if traced.Load() != 0 {
		test.Fatal("caller tracing observed sensitive requests")
	}
	var redirected atomic.Int64
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { redirected.Add(1) }))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := NewNativeClient(redirect.URL, redirect.Client().Transport.(*http.Transport))
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	_, err = client.OpenConsumer(test.Context(), "owner", "synthetic-secret")
	if err == nil || redirected.Load() != 0 {
		test.Fatalf("redirect result category=%v targetCalls=%d", err, redirected.Load())
	}
}

func TestNativeClientRejectsUnauthenticatedTLS(test *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { test.Error("untrusted TLS request delivered") }))
	defer server.Close()
	client, err := NewNativeClient(server.URL, &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}})
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	_, err = client.OpenConsumer(test.Context(), "owner", "synthetic-secret")
	if !errors.Is(err, ErrNativeUnavailable) {
		test.Fatal("untrusted TLS accepted")
	}
}

func TestNativeClientBoundedAndPrivateErrors(test *testing.T) {
	for _, scenario := range []string{"oversize", "compressed", "cookie", "cache", "content-type", "error", "truncated"} {
		test.Run(scenario, func(test *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Cache-Control", "no-store")
				writer.Header().Set("Content-Type", "application/proto")
				switch scenario {
				case "compressed":
					writer.Header().Set("Content-Encoding", "gzip")
				case "cookie":
					writer.Header().Set("Set-Cookie", "session="+nativeTestSecret)
				case "cache":
					writer.Header().Del("Cache-Control")
				case "content-type":
					writer.Header().Set("Content-Type", "application/json")
				case "error":
					writer.WriteHeader(401)
				case "truncated":
					writer.Header().Set("Content-Length", "10000")
				}
				if scenario == "oversize" {
					_, _ = writer.Write(bytes.Repeat([]byte{1}, nativeBootstrapLimit+1))
				} else {
					_, _ = io.WriteString(writer, nativeTestSecret)
				}
			}))
			defer server.Close()
			client, err := NewNativeClient(server.URL, server.Client().Transport.(*http.Transport))
			if err != nil {
				test.Fatal(err)
			}
			defer client.Close()
			_, err = client.OpenConsumer(test.Context(), "owner", "synthetic-owner")
			if err == nil || strings.Contains(fmt.Sprint(err), nativeTestSecret) || strings.Contains(fmt.Sprint(err), server.URL) {
				test.Fatal("unsanitized native error")
			}
		})
	}
}

func TestNativeClientActualTwentySecondResponseBudget(test *testing.T) {
	entered, drained := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Type", "application/proto")
		writer.Header().Set("Content-Length", "32")
		_, _ = writer.Write([]byte{1})
		_ = http.NewResponseController(writer).Flush()
		close(entered)
		<-request.Context().Done()
		close(drained)
	}))
	defer server.Close()
	client, err := NewNativeClient(server.URL, server.Client().Transport.(*http.Transport))
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	started := time.Now()
	_, err = client.OpenConsumer(test.Context(), "owner", "synthetic-owner")
	if !errors.Is(err, ErrNativeCanceled) || time.Since(started) < 19*time.Second || time.Since(started) > 24*time.Second {
		test.Fatal("actual production response budget not enforced")
	}
	<-entered
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		test.Fatal("budget did not close actual I/O")
	}
}

func TestNativeClientParentRevocationAndOwnerIsolation(test *testing.T) {
	fixture := newNativeClientFixture(test, false, nil)
	if _, err := fixture.client.OpenConsumer(test.Context(), "other-owner", fixture.bearer); !errors.Is(err, ErrNativeDenied) {
		test.Fatal("cross-owner bootstrap succeeded")
	}
	flow, prepared := fixture.prepare(test, configuration.APIKey)
	if err := fixture.auth.Logout(test.Context(), fixture.bearer); err != nil {
		test.Fatal(err)
	}
	if _, err := flow.Authorize(test.Context(), nativeTestApproval(prepared)); !errors.Is(err, ErrNativeUnknown) {
		test.Fatal("revoked parent authorized")
	}
	if _, err := flow.Status(test.Context()); !errors.Is(err, ErrNativeDenied) {
		test.Fatal("revoked parent status allowed")
	}
}

func TestNativeClientRealFlowLogPrivacy(test *testing.T) {
	var output bytes.Buffer
	previousWriter, previousFlags, previousPrefix := log.Writer(), log.Flags(), log.Prefix()
	previousSlog := slog.Default()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer func() {
		slog.SetDefault(previousSlog)
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	}()
	fixture := newNativeClientFixture(test, false, nil)
	flow, prepared := fixture.prepare(test, configuration.APIKey)
	if _, err := flow.Authorize(test.Context(), nativeTestApproval(prepared)); err != nil {
		test.Fatal(err)
	}
	bearer, grant, key := flow.bearer.CloneBytes(), flow.grant.CloneBytes(), flow.key.CloneBytes()
	defer clear(bearer)
	defer clear(grant)
	defer clear(key)
	material, err := flow.Consume(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer material.Destroy()
	if _, err := flow.Status(test.Context()); err != nil {
		test.Fatal(err)
	}
	log.Printf("opaque=%#v flow=%#v", material, flow)
	slog.Info("opaque", "material", material)
	for _, sensitive := range [][]byte{[]byte(nativeTestSecret), []byte(fixture.bearer), bearer, grant, key} {
		for _, encoding := range []string{string(sensitive), hex.EncodeToString(sensitive), base64.StdEncoding.EncodeToString(sensitive), fmt.Sprint(sensitive)} {
			if encoding != "" && strings.Contains(output.String(), encoding) {
				test.Fatal("real client/server flow logged credentials")
			}
		}
	}
}
