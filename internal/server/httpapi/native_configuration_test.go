package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/native/v1/configurationnativev1connect"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/agentadapter"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/nativeconsumer"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type nativeHTTPFixture struct {
	store                         *state.Store
	owner                         *auth.Service
	domain                        *configuration.Service
	service                       *nativeconsumer.Service
	endpoint                      *httptest.Server
	client                        configurationnativev1connect.NativeConfigurationServiceClient
	parent, ownerID, origin, path string
	adapter                       *nativev1.AdapterBinding
}

func newNativeHTTPFixture(test *testing.T, filters ...func(http.Handler) http.Handler) *nativeHTTPFixture {
	return newNativeHTTPFixtureWithListener(test, nil, filters...)
}

func newNativeHTTPFixtureWithListener(test *testing.T, decorate func(net.Listener) net.Listener, filters ...func(http.Handler) http.Handler) *nativeHTTPFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	master := bytes.Repeat([]byte{97}, 32)
	path := filepath.Join(directory, "transport.db")
	store, err := state.Open(test.Context(), path, master)
	if err != nil {
		test.Fatal(err)
	}
	owner, err := auth.NewService(store, master)
	if err != nil {
		test.Fatal(err)
	}
	parent, ownerID, err := owner.LoginOIDC(test.Context(), "https://synthetic.identity", "owner", "", "")
	if err != nil {
		test.Fatal(err)
	}
	endpoint := httptest.NewUnstartedServer(nil)
	if decorate != nil {
		endpoint.Listener = decorate(endpoint.Listener)
	}
	origin := "https://" + endpoint.Listener.Addr().String()
	service, err := nativeconsumer.New(store, owner, master, origin)
	clear(master)
	if err != nil {
		test.Fatal(err)
	}
	endpoint.Config.Handler = NewWithOptions(owner, Options{PublicOrigin: origin, NativeConfiguration: service})
	for _, filter := range filters {
		endpoint.Config.Handler = filter(endpoint.Config.Handler)
	}
	endpoint.StartTLS()
	if err := service.Start(test.Context()); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { service.Close(); endpoint.Close(); _ = store.Close() })
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	var adapter *nativev1.AdapterBinding
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.PluginID == "builtin.claude" {
			adapter = &nativev1.AdapterBinding{ApiVersion: 1, PluginId: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, RequiredCapabilities: []string{"claude.provider-switch.v1"}}
		}
	}
	return &nativeHTTPFixture{store: store, owner: owner, domain: configuration.New(store), service: service, endpoint: endpoint, client: configurationnativev1connect.NewNativeConfigurationServiceClient(endpoint.Client(), endpoint.URL), parent: parent.Reveal(), ownerID: ownerID, origin: origin, adapter: adapter, path: path}
}

func (fixture *nativeHTTPFixture) asset(test *testing.T) configuration.EditView {
	test.Helper()
	asset, err := fixture.domain.Create(test.Context(), fixture.ownerID, configuration.Draft{Name: "native fixture", Provider: configuration.ClaudeProvider{AuthMode: configuration.APIKey}}, []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-native-http-private-sentinel"))}})
	if err != nil {
		test.Fatal(err)
	}
	return asset
}

func TestNativeHTTPTransportRejectionAndNoStore(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	encoded, _ := proto.Marshal(codecBegin())
	for _, scenario := range []struct{ name, method, query, contentType, header, value string }{
		{name: "GET", method: "GET"}, {name: "query", query: "?message=synthetic-query-sentinel"},
		{name: "JSON", contentType: "application/json"}, {name: "gRPC", contentType: "application/grpc+proto"},
		{name: "gRPC-Web", contentType: "application/grpc-web+proto"}, {name: "stream", contentType: "application/connect+proto"},
		{name: "compressed", header: "Content-Encoding", value: "gzip"}, {name: "identity-encoding", header: "Content-Encoding", value: "identity"},
		{name: "Origin", header: "Origin", value: fixture.origin}, {name: "Sec-Fetch", header: "Sec-Fetch-Site", value: "none"},
		{name: "Cookie", header: "Cookie", value: "session=synthetic-cookie-sentinel"},
		{name: "version", header: "Connect-Protocol-Version", value: "synthetic-header-sentinel"},
		{name: "bad-timeout", header: "Connect-Timeout-Ms", value: "synthetic-header-sentinel"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			method, contentType := scenario.method, scenario.contentType
			if method == "" {
				method = http.MethodPost
			}
			if contentType == "" {
				contentType = "application/proto"
			}
			request, _ := http.NewRequestWithContext(test.Context(), method, fixture.endpoint.URL+nativeServicePath+"PrepareNativeApply"+scenario.query, bytes.NewReader(encoded))
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("Connect-Protocol-Version", "1")
			if scenario.header != "" {
				request.Header.Set(scenario.header, scenario.value)
			}
			response, err := fixture.endpoint.Client().Do(request)
			if err != nil {
				test.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode < 400 || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Access-Control-Allow-Origin") != "" || response.Header.Get("Set-Cookie") != "" || bytes.Contains(body, []byte("synthetic-")) {
				test.Fatal("native transport allowed forbidden request, cached error, emitted CORS/cookie, or echoed fields")
			}
		})
	}
	plain := httptest.NewServer(NewWithOptions(fixture.owner, Options{PublicOrigin: fixture.origin, NativeConfiguration: fixture.service}))
	defer plain.Close()
	request, _ := http.NewRequestWithContext(test.Context(), http.MethodPost, plain.URL+nativeServicePath+"BeginNativeConsumer", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/proto")
	request.Header.Set("Connect-Protocol-Version", "1")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("Forwarded", "proto=https")
	response, err := plain.Client().Do(request)
	if err != nil {
		test.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 || response.Header.Get("Cache-Control") != "no-store" {
		test.Fatal("forwarded headers forged real TLS")
	}
}

type nativeAdmissionObservedBody struct {
	io.ReadCloser
	once     sync.Once
	observed chan<- struct{}
}

func (body *nativeAdmissionObservedBody) Read(data []byte) (int, error) {
	count, err := body.ReadCloser.Read(data)
	if count > 0 {
		body.once.Do(func() { body.observed <- struct{}{} })
	}
	return count, err
}

func TestNativeHTTPEightRealBlockedBodiesCancelAndDrain(test *testing.T) {
	observed := make(chan struct{}, 8)
	fixture := newNativeHTTPFixture(test, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			request.Body = &nativeAdmissionObservedBody{ReadCloser: request.Body, observed: observed}
			next.ServeHTTP(writer, request)
		})
	})
	var connections []net.Conn
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	transport := fixture.endpoint.Client().Transport.(*http.Transport)
	for range 8 {
		connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", fixture.endpoint.Listener.Addr().String(), transport.TLSClientConfig.Clone())
		if err != nil {
			test.Fatal(err)
		}
		connections = append(connections, connection)
		_, err = fmt.Fprintf(connection, "POST %sPrepareNativeApply HTTP/1.1\r\nHost: synthetic.invalid\r\nContent-Type: application/proto\r\nConnect-Protocol-Version: 1\r\nContent-Length: 1000\r\n\r\nX", nativeServicePath)
		if err != nil {
			test.Fatal(err)
		}
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for range 8 {
		select {
		case <-observed:
		case <-deadline.C:
			test.Fatal("eight actual request bodies did not start under admission")
		}
	}
	request, _ := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.endpoint.URL+nativeServicePath+"PrepareNativeApply", strings.NewReader("X"))
	request.Header.Set("Content-Type", "application/proto")
	request.Header.Set("Connect-Protocol-Version", "1")
	response, err := fixture.endpoint.Client().Do(request)
	if err != nil {
		test.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 429 || response.Header.Get("Cache-Control") != "no-store" {
		test.Fatal("ninth body bypassed full-request capacity")
	}
	closed := make(chan struct{})
	go func() { fixture.service.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		test.Fatal("cancellation failed to unblock and drain actual body reads")
	}
	if err := fixture.service.Call(test.Context(), func(context.Context) error { test.Error("post-close call ran"); return nil }); !errors.Is(err, state.ErrNativeUnavailable) {
		test.Fatal("shutdown admission remained open")
	}
}

type nativeLostResponseWriter struct {
	http.ResponseWriter
	truncate bool
}

func (writer nativeLostResponseWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }
func (writer nativeLostResponseWriter) Write(data []byte) (int, error) {
	if writer.truncate {
		_, _ = writer.ResponseWriter.Write(data[:len(data)/2])
		_ = http.NewResponseController(writer.ResponseWriter).Flush()
	}
	connection, _, err := http.NewResponseController(writer.ResponseWriter).Hijack()
	if err == nil {
		_ = connection.Close()
	}
	return 0, io.ErrClosedPipe
}

func TestNativeHTTPPostCommitLostAndTruncatedNeverReplay(test *testing.T) {
	for _, truncate := range []bool{false, true} {
		test.Run(fmt.Sprintf("truncate-%t", truncate), func(test *testing.T) {
			var armed atomic.Bool
			fixture := newNativeHTTPFixture(test, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") && armed.CompareAndSwap(true, false) {
						next.ServeHTTP(nativeLostResponseWriter{ResponseWriter: writer, truncate: truncate}, request)
						return
					}
					next.ServeHTTP(writer, request)
				})
			})
			asset := fixture.asset(test)
			flow := fixture.bootstrap(test)
			flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
			flow.authorize(test)
			armed.Store(true)
			response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test))
			if err == nil || response != nil {
				test.Fatal("damaged response pretended complete materialization")
			}
			if flow.status(test).State != 3 {
				test.Fatal("real COMMIT was rolled back or consume did not precede response failure")
			}
			if response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test)); err == nil || response != nil {
				test.Fatal("lost committed secret was replayed")
			}
		})
	}
}

func TestNativeHTTPCommittedBootstrapAndGrantLossNeverReissue(test *testing.T) {
	for _, stage := range []string{"BindNativeConsumer", "AuthorizeNativeMaterialization"} {
		test.Run(stage, func(test *testing.T) {
			var armed atomic.Bool
			fixture := newNativeHTTPFixture(test, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if strings.HasSuffix(request.URL.Path, "/"+stage) && armed.CompareAndSwap(true, false) {
						next.ServeHTTP(nativeLostResponseWriter{ResponseWriter: writer}, request)
						return
					}
					next.ServeHTTP(writer, request)
				})
			})
			if stage == "BindNativeConsumer" {
				public, key, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					test.Fatal(err)
				}
				begin := codecBegin()
				begin.ExpectedOwnerId, begin.ExpectedServerOrigin, begin.ConsumerPublicKey = fixture.ownerID, fixture.origin, public
				challenge, err := fixture.client.BeginNativeConsumer(test.Context(), fixture.request(begin, fixture.parent))
				if err != nil {
					test.Fatal("Begin before loss failed")
				}
				transcript, err := configclient.BindTranscript(begin, challenge.Msg)
				if err != nil {
					test.Fatal("Bind transcript before loss failed")
				}
				request := fixture.request(&nativev1.BindNativeConsumerRequest{ChallengeId: challenge.Msg.ChallengeId, ProofSignature: ed25519.Sign(key, transcript.CopyBytes())}, fixture.parent)
				armed.Store(true)
				if response, err := fixture.client.BindNativeConsumer(test.Context(), request); err == nil || response != nil {
					test.Fatal("lost Bind response returned a token")
				}
				if response, err := fixture.client.BindNativeConsumer(test.Context(), request); err == nil || response != nil {
					test.Fatal("committed Bind reissued a lost token")
				}
				database, err := sql.Open("sqlite3", fixture.path)
				if err != nil {
					test.Fatal(err)
				}
				defer database.Close()
				var children int
				if err := database.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM native_consumer_sessions").Scan(&children); err != nil || children != 1 {
					test.Fatal("lost Bind did not really commit exactly once")
				}
				return
			}
			asset := fixture.asset(test)
			flow := fixture.bootstrap(test)
			flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
			request := flow.authorizeRequest(test)
			armed.Store(true)
			if response, err := fixture.client.AuthorizeNativeMaterialization(test.Context(), request); err == nil || response != nil {
				test.Fatal("lost authorization response returned token")
			}
			if status := flow.status(test); status.State != 2 || status.GrantId == "" {
				test.Fatal("lost Authorize was not durably issued")
			}
			if response, err := fixture.client.AuthorizeNativeMaterialization(test.Context(), request); connect.CodeOf(err) != connect.CodeFailedPrecondition || response != nil {
				test.Fatal("committed authorization reissued lost grant")
			}
		})
	}
}

func TestNativeHTTPRealGCStartupCascadesExpiredChainAndKeepsLiveState(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	asset := fixture.asset(test)
	expired, live := fixture.bootstrap(test), fixture.bootstrap(test)
	for _, flow := range []*nativeHTTPFlow{expired, live} {
		flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
		flow.authorize(test)
	}
	response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), expired.consumeRequest(test))
	if err != nil || len(response.Msg.SlotValues) != 1 {
		test.Fatal("GC fixture lacks real consumed grant")
	}
	fixture.service.Close()
	database, err := sql.Open("sqlite3", fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	var child int
	if err := database.QueryRowContext(test.Context(), "SELECT id FROM native_consumer_sessions WHERE session_binding = ?", expired.consumer.NativeSessionBinding).Scan(&child); err != nil {
		test.Fatal(err)
	}
	transaction, err := database.BeginTx(test.Context(), nil)
	if err != nil {
		test.Fatal(err)
	}
	for _, statement := range []string{
		"UPDATE native_consumer_sessions SET issued_ms = issued_ms - 600000, expires_ms = expires_ms - 600000 WHERE id = ?",
		"UPDATE native_apply_preparations SET issued_ms = issued_ms - 600000, expires_ms = expires_ms - 600000 WHERE native_session_id = ?",
		"UPDATE native_materialization_grants SET issued_ms = issued_ms - 600000, expires_ms = expires_ms - 600000, consumed_ms = consumed_ms - 600000 WHERE native_session_id = ?",
	} {
		if result, err := transaction.ExecContext(test.Context(), statement, child); err != nil {
			_ = transaction.Rollback()
			test.Fatal(err)
		} else if count, _ := result.RowsAffected(); count != 1 {
			_ = transaction.Rollback()
			test.Fatal("GC fixture did not age one complete chain")
		}
	}
	if err := transaction.Commit(); err != nil {
		test.Fatal(err)
	}
	worker, err := nativeconsumer.New(fixture.store, fixture.owner, bytes.Repeat([]byte{97}, 32), fixture.origin)
	if err != nil {
		test.Fatal(err)
	}
	defer worker.Close()
	if err := worker.Start(test.Context()); err != nil {
		test.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var children, preparations, grants, challenges int
		for _, item := range []struct {
			table string
			count *int
		}{{"native_consumer_sessions", &children}, {"native_apply_preparations", &preparations}, {"native_materialization_grants", &grants}, {"native_consumer_challenges", &challenges}} {
			if err := database.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM "+item.table).Scan(item.count); err != nil {
				test.Fatal(err)
			}
		}
		if children == 1 && preparations == 1 && grants == 1 && challenges == 0 {
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("trusted startup did not really prune used challenges and expired four-table cascade")
		}
		time.Sleep(time.Millisecond)
	}
	request := live.statusRequest(test).Msg
	status, err := worker.Status(test.Context(), live.bearer, nativesessionauth.StatusRequest{PrepareID: request.PrepareId, GrantID: request.GrantId, OperationID: [16]byte(request.OperationId), Nonce: [32]byte(request.ClientNonce)}, request.ProofSignature)
	if err != nil || status.State != 2 {
		test.Fatal("GC damaged a live sibling's actual grant authority")
	}
	worker.Close()
	if _, err := database.ExecContext(test.Context(), "BEGIN IMMEDIATE"); err != nil {
		test.Fatal("closed GC retained database writer")
	}
	if _, err := database.ExecContext(test.Context(), "ROLLBACK"); err != nil {
		test.Fatal(err)
	}
}

type nativeFailingReader struct{}

func (nativeFailingReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic-private-native-body-error")
}

func TestNativeHTTPPrivacyOnRealSuccessCodecAndErrorPaths(test *testing.T) {
	var captured bytes.Buffer
	oldLog, oldSlog, flags, prefix := log.Writer(), slog.Default(), log.Flags(), log.Prefix()
	log.SetOutput(&captured)
	slog.SetDefault(slog.New(slog.NewTextHandler(&captured, nil)))
	test.Cleanup(func() { slog.SetDefault(oldSlog); log.SetOutput(oldLog); log.SetFlags(flags); log.SetPrefix(prefix) })
	fixture := newNativeHTTPFixture(test, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("X-Synthetic-Read-Failure") == "1" {
				_ = request.Body.Close()
				request.Body = io.NopCloser(nativeFailingReader{})
			}
			next.ServeHTTP(writer, request)
		})
	})
	asset := fixture.asset(test)
	flow := fixture.bootstrap(test)
	flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
	flow.authorize(test)
	proof := flow.consumeRequest(test).Msg.ProofSignature
	secrets := [][]byte{[]byte("synthetic-native-http-private-sentinel"), []byte(fixture.parent), []byte(flow.bearer), flow.grant.GrantToken, proof}
	for _, scenario := range []struct {
		path string
		data []byte
	}{
		{"ConsumeNativeMaterialization", []byte("synthetic-native-http-private-sentinel")},
		{"ConsumeNativeMaterialization", append([]byte{0xfa, 7, 32}, flow.grant.GrantToken...)},
		{"PrepareNativeApply", bytes.Repeat([]byte{'X'}, nativeMaxWireBytes+1)},
		{"BeginNativeConsumer", bytes.Repeat([]byte{'X'}, nativeBootstrapMaxBytes+1)},
	} {
		request, _ := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.endpoint.URL+nativeServicePath+scenario.path, bytes.NewReader(scenario.data))
		request.Header.Set("Content-Type", "application/proto")
		request.Header.Set("Connect-Protocol-Version", "1")
		request.Header.Set("Authorization", "Bearer "+flow.bearer)
		response, err := fixture.endpoint.Client().Do(request)
		if err != nil {
			test.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode < 400 || response.Header.Get("Cache-Control") != "no-store" {
			test.Fatal("privacy fixture was not an actual failure")
		}
		for _, secret := range secrets {
			if bytes.Contains(data, secret) {
				test.Fatal("native error echoed sensitive material")
			}
		}
	}
	failedRead, _ := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.endpoint.URL+nativeServicePath+"PrepareNativeApply", strings.NewReader("X"))
	failedRead.Header.Set("Content-Type", "application/proto")
	failedRead.Header.Set("Connect-Protocol-Version", "1")
	failedRead.Header.Set("X-Synthetic-Read-Failure", "1")
	readResponse, err := fixture.endpoint.Client().Do(failedRead)
	if err != nil {
		test.Fatal(err)
	}
	readError, _ := io.ReadAll(readResponse.Body)
	readResponse.Body.Close()
	if readResponse.StatusCode < 400 || readResponse.Header.Get("Cache-Control") != "no-store" || bytes.Contains(readError, []byte("synthetic-private-native-body-error")) {
		test.Fatal("native body-read error echoed its raw cause")
	}
	response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test))
	if err != nil || string(response.Msg.SlotValues[0].Plaintext) != string(secrets[0]) {
		test.Fatal("privacy was faked by removing functional plaintext")
	}
	if flow.status(test).State != 3 {
		test.Fatal("safe status failed")
	}
	if strings.Contains(captured.String(), "synthetic-private-native-body-error") {
		test.Fatal("native body-read path logged raw cause")
	}
	for _, secret := range secrets {
		for _, representation := range []string{string(secret), hex.EncodeToString(secret), base64.StdEncoding.EncodeToString(secret), base64.RawURLEncoding.EncodeToString(secret), fmt.Sprint(secret)} {
			if strings.Contains(captured.String(), representation) {
				test.Fatal("native path logged sensitive material")
			}
		}
	}
	database, err := sql.Open("sqlite3", fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	var consumed int
	if err := database.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM native_materialization_grants WHERE consumed_ms > 0").Scan(&consumed); err != nil || consumed != 1 {
		test.Fatal("privacy fixture did not really commit consumption")
	}
}

func TestNativeHTTPResponseSlotsFailClosedAndClearOwnedBytes(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	asset := fixture.asset(test)
	flow := fixture.bootstrap(test)
	flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
	flow.authorize(test)
	response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test))
	if err != nil {
		test.Fatal("response validator needs actual materialization")
	}
	for _, scenario := range []string{"duplicate", "other-slot", "empty", "unknown-nested", "unbound", "oversize"} {
		test.Run(scenario, func(test *testing.T) {
			message := proto.Clone(response.Msg).(*nativev1.ConsumeNativeMaterializationResponse)
			switch scenario {
			case "duplicate":
				message.SlotValues = append(message.SlotValues, proto.Clone(message.SlotValues[0]).(*nativev1.SlotValue))
			case "other-slot":
				message.SlotValues[0].Slot = 2
			case "empty":
				message.SlotValues[0].Plaintext = nil
			case "unknown-nested":
				message.SlotValues[0].ProtoReflect().SetUnknown([]byte{24, 1})
			case "unbound":
				message.Binding = nil
			case "oversize":
				message.SlotValues[0].Plaintext = bytes.Repeat([]byte{'X'}, nativeMaxWireBytes)
			}
			encoded, err := (nativeConfigurationCodec{}).Marshal(message)
			if err == nil || len(encoded) != 0 {
				test.Fatal("invalid materialization response escaped strict slot boundary")
			}
			for _, slot := range message.SlotValues {
				if bytes.Count(slot.Plaintext, []byte{0}) != len(slot.Plaintext) {
					test.Fatal("failed response retained owned plaintext bytes")
				}
			}
		})
	}
	message := proto.Clone(response.Msg).(*nativev1.ConsumeNativeMaterializationResponse)
	encoded, err := (nativeConfigurationCodec{}).Marshal(message)
	if err != nil || !bytes.Contains(encoded, []byte("synthetic-native-http-private-sentinel")) {
		test.Fatal("successful response codec replaced actual secret with redaction")
	}
	if bytes.Count(message.SlotValues[0].Plaintext, []byte{0}) != len(message.SlotValues[0].Plaintext) {
		test.Fatal("successful encoding retained owned raw DTO plaintext")
	}
	clear(encoded)
}

func TestNativeHTTPStrictNestedWireRejectsBeforeGrantMutation(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	asset := fixture.asset(test)
	flow := fixture.bootstrap(test)
	flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
	request := flow.authorizeRequest(test).Msg
	valid, _ := proto.Marshal(request)
	field := func(number protowire.Number, data []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), data)
	}
	nested := proto.Clone(request).(*nativev1.AuthorizeNativeMaterializationRequest)
	nested.GetExplicitConfirmation().ProtoReflect().SetUnknown([]byte{16, 1})
	unknownNested, _ := proto.Marshal(nested)
	duplicateSlots := proto.Clone(request).(*nativev1.AuthorizeNativeMaterializationRequest)
	duplicateSlots.RequestedSlots = append(duplicateSlots.RequestedSlots, duplicateSlots.RequestedSlots...)
	packed, _ := proto.Marshal(duplicateSlots)
	for _, data := range [][]byte{
		append(bytes.Clone(valid), field(99, []byte("synthetic-private-wire-field"))...),
		append(bytes.Clone(valid), field(1, []byte(request.PrepareId))...),
		append(bytes.Clone(valid), field(5, []byte{8, 1})...),
		unknownNested, packed,
	} {
		httpRequest, _ := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.endpoint.URL+nativeServicePath+"AuthorizeNativeMaterialization", bytes.NewReader(data))
		httpRequest.Header.Set("Content-Type", "application/proto")
		httpRequest.Header.Set("Connect-Protocol-Version", "1")
		httpRequest.Header.Set("Authorization", "Bearer "+flow.bearer)
		response, err := fixture.endpoint.Client().Do(httpRequest)
		if err != nil {
			test.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 400 || response.Header.Get("Cache-Control") != "no-store" || !bytes.Contains(body, []byte("invalid_argument")) || bytes.Contains(body, []byte("synthetic-private-wire-field")) {
			test.Fatal("ambiguous nested proto reached grant mutation or echoed wire fields")
		}
	}
	flow.authorize(test)
	if flow.status(test).State != 2 {
		test.Fatal("strict rejection burned or mutated the valid authorization")
	}
}

func TestNativeHTTPStorageFailureIsUnavailableNotBadCredential(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	asset := fixture.asset(test)
	flow := fixture.bootstrap(test)
	prepare := flow.prepareRequest(test, asset.Metadata.ID, asset.Metadata.Revision)
	if err := fixture.store.Close(); err != nil {
		test.Fatal(err)
	}
	begin := codecBegin()
	begin.ExpectedOwnerId, begin.ExpectedServerOrigin = fixture.ownerID, fixture.origin
	if _, err := fixture.client.BeginNativeConsumer(test.Context(), fixture.request(begin, fixture.parent)); connect.CodeOf(err) != connect.CodeUnavailable {
		test.Error("storage failure was disguised as an invalid bootstrap credential")
	}
	prepare.Header().Set("Authorization", "Bearer "+fixture.parent)
	if _, err := fixture.client.PrepareNativeApply(test.Context(), prepare); connect.CodeOf(err) != connect.CodeUnavailable {
		test.Error("storage failure was disguised as an invalid owner credential")
	}
	prepare.Header().Set("Authorization", "Bearer "+flow.bearer)
	if _, err := fixture.client.PrepareNativeApply(test.Context(), prepare); connect.CodeOf(err) != connect.CodeUnavailable {
		test.Error("native storage failure lost Unavailable classification")
	}
}

func (fixture *nativeHTTPFixture) request[Message any](message *Message, bearer string) *connect.Request[Message] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+bearer)
	return request
}

type nativeHTTPFlow struct {
	fixture   *nativeHTTPFixture
	key       ed25519.PrivateKey
	operation []byte
	consumer  configclient.ConsumerBinding
	bearer    string
	prepared  *nativev1.PrepareNativeApplyResponse
	grant     *nativev1.AuthorizeNativeMaterializationResponse
}

func (fixture *nativeHTTPFixture) bootstrap(test *testing.T) *nativeHTTPFlow {
	test.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		test.Fatal(err)
	}
	operation := make([]byte, 16)
	_, _ = rand.Read(operation)
	begin := &nativev1.BeginNativeConsumerRequest{Purpose: 1, OperationId: operation, ConsumerPublicKey: public, ClientNonce: bytes.Repeat([]byte{2}, 32), ExpectedOwnerId: fixture.ownerID, ExpectedServerOrigin: fixture.origin}
	challenge, err := fixture.client.BeginNativeConsumer(test.Context(), fixture.request(begin, fixture.parent))
	if err != nil {
		test.Fatalf("Begin: %s", connect.CodeOf(err))
	}
	transcript, err := configclient.BindTranscript(begin, challenge.Msg)
	if err != nil {
		test.Fatal("invalid real Begin response")
	}
	issued, err := fixture.client.BindNativeConsumer(test.Context(), fixture.request(&nativev1.BindNativeConsumerRequest{ChallengeId: challenge.Msg.ChallengeId, ProofSignature: ed25519.Sign(private, transcript.CopyBytes())}, fixture.parent))
	if err != nil || len(issued.Msg.NativeSessionToken) != 32 {
		test.Fatal("Bind did not issue native token")
	}
	if issued.Header().Get("Cache-Control") != "no-store" {
		test.Fatal("sensitive bootstrap response cached")
	}
	hash := sha256.Sum256(public)
	return &nativeHTTPFlow{fixture: fixture, key: private, operation: operation, bearer: "cn1." + base64.RawURLEncoding.EncodeToString(issued.Msg.NativeSessionToken), consumer: configclient.ConsumerBinding{OwnerID: fixture.ownerID, ServerOrigin: fixture.origin, NativeSessionBinding: issued.Msg.NativeSessionBinding, OperationID: operation, ConsumerKeySHA256: hash[:]}}
}

func (flow *nativeHTTPFlow) prepare(test *testing.T, id string, revision int64) {
	test.Helper()
	prepared, err := flow.fixture.client.PrepareNativeApply(test.Context(), flow.prepareRequest(test, id, revision))
	if err != nil {
		test.Fatalf("Prepare: %s", connect.CodeOf(err))
	}
	if configclient.ValidatePreparedPatch(prepared.Msg) != nil {
		test.Fatal("invalid actual moejs patch")
	}
	flow.prepared = prepared.Msg
}

func (flow *nativeHTTPFlow) prepareRequest(test *testing.T, id string, revision int64) *connect.Request[nativev1.PrepareNativeApplyRequest] {
	test.Helper()
	fixture := flow.fixture
	request := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: fixture.ownerID, AssetId: id, ExpectedAssetRevision: revision, AgentId: "claude", SchemaVersion: 1, Adapter: fixture.adapter, Target: 1, Scope: 1, ProfileId: "profile", SelectionRevision: 1, OperationId: flow.operation, ClientNonce: bytes.Repeat([]byte{3}, 32)}
	transcript, err := configclient.PrepareTranscript(flow.consumer, request)
	if err != nil {
		test.Fatal("prepare proof input")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	return fixture.request(request, flow.bearer)
}

func (flow *nativeHTTPFlow) authorize(test *testing.T) {
	test.Helper()
	grant, err := flow.fixture.client.AuthorizeNativeMaterialization(test.Context(), flow.authorizeRequest(test))
	if err != nil {
		test.Fatalf("Authorize: %s", connect.CodeOf(err))
	}
	flow.grant = grant.Msg
}

func (flow *nativeHTTPFlow) authorizeRequest(test *testing.T) *connect.Request[nativev1.AuthorizeNativeMaterializationRequest] {
	test.Helper()
	request := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: flow.prepared.Binding.PrepareId, OperationId: flow.operation, IntentCommitment: bytes.Repeat([]byte{4}, 32), RequestedSlots: flow.prepared.Binding.RequiredSlots, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: bytes.Repeat([]byte{5}, 32), IntentExpiresAtUnixMs: flow.prepared.Binding.PrepareExpiresAtUnixMs}
	transcript, err := configclient.AuthorizeTranscript(flow.prepared.Binding, flow.prepared.AuthorizeChallenge, request)
	if err != nil {
		test.Fatal("authorize proof input")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	return flow.fixture.request(request, flow.bearer)
}

func (flow *nativeHTTPFlow) consumeRequest(test *testing.T) *connect.Request[nativev1.ConsumeNativeMaterializationRequest] {
	test.Helper()
	request := &nativev1.ConsumeNativeMaterializationRequest{PrepareId: flow.prepared.Binding.PrepareId, GrantId: flow.grant.GrantId, GrantToken: flow.grant.GrantToken, OperationId: flow.operation, IntentCommitment: flow.grant.Binding.IntentCommitment, ClientNonce: bytes.Repeat([]byte{6}, 32)}
	transcript, err := configclient.ConsumeTranscript(flow.grant.Binding, flow.prepared.AuthorizeChallenge, flow.grant.ConsumeChallenge, flow.grant.ExpiresAtUnixMs, request)
	if err != nil {
		test.Fatal("consume proof input")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	return flow.fixture.request(request, flow.bearer)
}

func (flow *nativeHTTPFlow) status(test *testing.T) *nativev1.GetNativeMaterializationStatusResponse {
	test.Helper()
	status, err := flow.fixture.client.GetNativeMaterializationStatus(test.Context(), flow.statusRequest(test))
	if err != nil {
		test.Fatalf("Status: %s", connect.CodeOf(err))
	}
	return status.Msg
}

func (flow *nativeHTTPFlow) statusRequest(test *testing.T) *connect.Request[nativev1.GetNativeMaterializationStatusRequest] {
	test.Helper()
	request := &nativev1.GetNativeMaterializationStatusRequest{PrepareId: flow.prepared.Binding.PrepareId, OperationId: flow.operation, ClientNonce: bytes.Repeat([]byte{7}, 32)}
	transcript, err := configclient.StatusTranscript(flow.prepared.Binding, flow.prepared.AuthorizeChallenge, request)
	if err != nil {
		test.Fatal("status proof input")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	return flow.fixture.request(request, flow.bearer)
}

func TestNativeHTTPOwnerNativeProofIsolationAndRevocation(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	asset := fixture.asset(test)
	flow := fixture.bootstrap(test)
	flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
	flow.authorize(test)
	for _, credential := range []string{"", "cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{90}, 32)), fixture.parent} {
		expected := connect.CodeUnauthenticated
		if credential == fixture.parent {
			expected = connect.CodePermissionDenied
		}
		prepare := flow.prepareRequest(test, asset.Metadata.ID, asset.Metadata.Revision)
		prepare.Header().Set("Authorization", "Bearer "+credential)
		if _, err := fixture.client.PrepareNativeApply(test.Context(), prepare); connect.CodeOf(err) != expected {
			test.Fatal("Prepare accepted non-native credential")
		}
		authorize := flow.authorizeRequest(test)
		authorize.Header().Set("Authorization", "Bearer "+credential)
		if _, err := fixture.client.AuthorizeNativeMaterialization(test.Context(), authorize); connect.CodeOf(err) != expected {
			test.Fatal("Authorize accepted non-native credential")
		}
		consume := flow.consumeRequest(test)
		consume.Header().Set("Authorization", "Bearer "+credential)
		if _, err := fixture.client.ConsumeNativeMaterialization(test.Context(), consume); connect.CodeOf(err) != expected {
			test.Fatal("Consume accepted non-native credential")
		}
		status := flow.statusRequest(test)
		status.Header().Set("Authorization", "Bearer "+credential)
		if _, err := fixture.client.GetNativeMaterializationStatus(test.Context(), status); connect.CodeOf(err) != expected {
			test.Fatal("Status accepted non-native credential")
		}
	}
	for _, mutation := range []string{"signature", "token", "duplicate-bearer"} {
		request := flow.consumeRequest(test)
		request.Msg = proto.Clone(request.Msg).(*nativev1.ConsumeNativeMaterializationRequest)
		switch mutation {
		case "signature":
			request.Msg.ProofSignature[0] ^= 1
		case "token":
			request.Msg.GrantToken[0] ^= 1
		case "duplicate-bearer":
			request.Header().Add("Authorization", "Bearer "+fixture.parent)
		}
		if _, err := fixture.client.ConsumeNativeMaterialization(test.Context(), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
			test.Fatal("bad proof/token/bearer not rejected uniformly")
		}
	}
	if flow.status(test).State != 2 {
		test.Fatal("failed authorization consumed grant")
	}
	_, otherID, err := fixture.owner.LoginOIDC(test.Context(), "https://synthetic.identity", "other", "", "")
	if err != nil {
		test.Fatal(err)
	}
	other, err := fixture.domain.Create(test.Context(), otherID, configuration.Draft{Name: "other owner", Provider: configuration.ClaudeProvider{AuthMode: configuration.NativeLogin}}, nil)
	if err != nil {
		test.Fatal(err)
	}
	for _, id := range []string{other.Metadata.ID, strings.Repeat("A", 26)} {
		if _, err := fixture.client.PrepareNativeApply(test.Context(), flow.prepareRequest(test, id, 1)); connect.CodeOf(err) != connect.CodeNotFound {
			test.Fatal("cross-owner/nonexistent assets were distinguishable")
		}
	}
	if err := fixture.owner.Logout(test.Context(), fixture.parent); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("revoked parent still authorized consume")
	}
	if _, err := fixture.client.GetNativeMaterializationStatus(test.Context(), flow.statusRequest(test)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("revoked identity could query status")
	}
}

func TestNativeHTTPAssetCASAndTypedKeepClear(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	asset := fixture.asset(test)
	flow := fixture.bootstrap(test)
	flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
	flow.authorize(test)
	updated, err := fixture.domain.Update(test.Context(), fixture.ownerID, asset.Metadata.ID, asset.Metadata.Revision, configuration.Draft{Name: "kept", Provider: asset.Provider}, []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Keep}})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test)); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("old grant ignored envelope CAS change")
	}
	kept := fixture.bootstrap(test)
	kept.prepare(test, asset.Metadata.ID, updated.Metadata.Revision)
	kept.authorize(test)
	response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), kept.consumeRequest(test))
	if err != nil || len(response.Msg.SlotValues) != 1 || string(response.Msg.SlotValues[0].Plaintext) != "synthetic-native-http-private-sentinel" {
		test.Fatal("KEEP lost actual secret")
	}
	cleared, err := fixture.domain.Update(test.Context(), fixture.ownerID, asset.Metadata.ID, updated.Metadata.Revision, configuration.Draft{Name: "cleared", Provider: updated.Provider}, []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Clear}})
	if err != nil {
		test.Fatal(err)
	}
	missing := fixture.bootstrap(test)
	if _, err := fixture.client.PrepareNativeApply(test.Context(), missing.prepareRequest(test, asset.Metadata.ID, cleared.Metadata.Revision)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		test.Fatal("cleared credential materialized old secret")
	}
}

func TestNativeHTTPPossessionIsNotOfficialClientAttestation(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	flow := fixture.bootstrap(test)
	if !strings.HasPrefix(flow.bearer, "cn1.") {
		test.Fatal("owner-controlled arbitrary key could not bootstrap; not the frozen possession contract")
	}
	begin := codecBegin()
	begin.ExpectedOwnerId, begin.ExpectedServerOrigin = fixture.ownerID, fixture.origin
	if _, err := fixture.client.BeginNativeConsumer(test.Context(), fixture.request(begin, flow.bearer)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("native bearer bootstrapped owner authority")
	}
	request := fixture.request(begin, fixture.parent)
	request.Header().Set("X-Forwarded-Host", "attacker.invalid")
	request.Header().Set("Forwarded", "host=attacker.invalid;proto=http")
	challenge, err := fixture.client.BeginNativeConsumer(test.Context(), request)
	if err != nil || challenge.Msg.ServerOrigin != fixture.origin {
		test.Fatal("untrusted forwarded identity changed canonical origin")
	}
}

func TestNativeHTTPContextCancellationReleasesActualBodyRead(test *testing.T) {
	fixture := newNativeHTTPFixture(test, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			ctx, cancel := context.WithTimeout(request.Context(), 50*time.Millisecond)
			defer cancel()
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	})
	transport := fixture.endpoint.Client().Transport.(*http.Transport)
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", fixture.endpoint.Listener.Addr().String(), transport.TLSClientConfig.Clone())
	if err != nil {
		test.Fatal(err)
	}
	defer connection.Close()
	_, err = fmt.Fprintf(connection, "POST %sPrepareNativeApply HTTP/1.1\r\nHost: synthetic.invalid\r\nContent-Type: application/proto\r\nConnect-Protocol-Version: 1\r\nContent-Length: 1000\r\n\r\nX", nativeServicePath)
	if err != nil {
		test.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadAll(connection)
	if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() {
		test.Fatal("context timeout left real body read detached")
	}
	closed := make(chan struct{})
	go func() { fixture.service.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		test.Fatal("canceled body did not actually drain")
	}
}

type nativeObservedBody struct {
	io.ReadCloser
	closed chan struct{}
	once   sync.Once
}

func (body *nativeObservedBody) Close() error {
	err := body.ReadCloser.Close()
	body.once.Do(func() { close(body.closed) })
	return err
}

func TestNativeHTTPRejectedSlowBodyIsActuallyClosedBeforeReturn(test *testing.T) {
	for _, scenario := range []string{"JSON", "Origin", "rate-limit"} {
		test.Run(scenario, func(test *testing.T) {
			closed := make(chan struct{})
			fixture := newNativeHTTPFixture(test, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if request.Header.Get("X-Synthetic-Observe") == "1" {
						request.Body = &nativeObservedBody{ReadCloser: request.Body, closed: closed}
					}
					next.ServeHTTP(writer, request)
				})
			})
			method, contentType, extra := "PrepareNativeApply", "application/json", ""
			if scenario == "Origin" {
				contentType, extra = "application/proto", "Origin: https://untrusted.invalid\r\n"
			}
			if scenario == "rate-limit" {
				method, contentType = "BeginNativeConsumer", "application/proto"
				for range 10 {
					request, _ := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.endpoint.URL+nativeServicePath+method, strings.NewReader("X"))
					request.Header.Set("Content-Type", contentType)
					request.Header.Set("Connect-Protocol-Version", "1")
					response, err := fixture.endpoint.Client().Do(request)
					if err != nil {
						test.Fatal(err)
					}
					response.Body.Close()
				}
			}
			transport := fixture.endpoint.Client().Transport.(*http.Transport)
			connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", fixture.endpoint.Listener.Addr().String(), transport.TLSClientConfig.Clone())
			if err != nil {
				test.Fatal(err)
			}
			defer connection.Close()
			_, err = fmt.Fprintf(connection, "POST %s%s HTTP/1.1\r\nHost: synthetic.invalid\r\nContent-Type: %s\r\nConnect-Protocol-Version: 1\r\nX-Synthetic-Observe: 1\r\n%sContent-Length: 1000\r\n\r\nX", nativeServicePath, method, contentType, extra)
			if err != nil {
				test.Fatal(err)
			}
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				test.Fatal("rejected request left actual Body.Close blocked outside the native request budget")
			}
		})
	}
}

func TestNativeHTTPRealSixRPCSecretSlots(test *testing.T) {
	for _, mode := range []configuration.AuthMode{configuration.APIKey, configuration.AuthToken, configuration.NativeLogin} {
		test.Run(string(mode), func(test *testing.T) {
			fixture := newNativeHTTPFixture(test)
			slot, expected := configuration.APIKeySlot, nativev1.SecretSlot(1)
			if mode == configuration.AuthToken {
				slot, expected = configuration.AuthTokenSlot, 2
			}
			const sentinel = "synthetic-native-http-selected-secret"
			var mutations []configuration.SecretMutation
			if mode != configuration.NativeLogin {
				mutations = []configuration.SecretMutation{{Slot: slot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte(sentinel))}}
			}
			asset, err := fixture.domain.Create(test.Context(), fixture.ownerID, configuration.Draft{Name: "native HTTP", Provider: configuration.ClaudeProvider{AuthMode: mode}}, mutations)
			if err != nil {
				test.Fatal(err)
			}
			flow := fixture.bootstrap(test)
			flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
			flow.authorize(test)
			if mode == configuration.NativeLogin {
				if flow.grant.MaterializationRequired || len(flow.grant.GrantToken) != 0 || flow.grant.GrantId != "" || flow.grant.ExpiresAtUnixMs != 0 || flow.grant.Binding == nil || flow.status(test).State != 1 {
					test.Fatal("native-login minted a fake grant or lost intent binding")
				}
				return
			}
			request := flow.consumeRequest(test)
			response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), request)
			if err != nil {
				test.Fatalf("Consume: %s", connect.CodeOf(err))
			}
			if len(response.Msg.SlotValues) != 1 || response.Msg.SlotValues[0].Slot != expected || string(response.Msg.SlotValues[0].Plaintext) != sentinel || response.Header().Get("Cache-Control") != "no-store" {
				test.Fatal("real selected slot not returned exactly once through native transport")
			}
			if _, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("consumed secret replayed")
			}
			if flow.status(test).State != 3 {
				test.Fatal("Status did not reflect durable consumption")
			}
		})
	}
}

type nativeSocketWrite struct {
	deadline int64
	done     chan struct{}
	frames   []string
}

type nativeObservedTCP struct {
	*net.TCPConn
	armed    *atomic.Bool
	deadline atomic.Int64
	writes   chan *nativeSocketWrite
}

func (connection *nativeObservedTCP) SetWriteDeadline(deadline time.Time) error {
	value := int64(0)
	if !deadline.IsZero() {
		value = deadline.UnixNano()
	}
	connection.deadline.Store(value)
	return connection.TCPConn.SetWriteDeadline(deadline)
}

func (connection *nativeObservedTCP) Write(data []byte) (int, error) {
	if !connection.armed.Load() {
		return connection.TCPConn.Write(data)
	}
	event := &nativeSocketWrite{deadline: connection.deadline.Load(), done: make(chan struct{})}
	var callers [32]uintptr
	frames := runtime.CallersFrames(callers[:runtime.Callers(1, callers[:])])
	for {
		frame, more := frames.Next()
		event.frames = append(event.frames, frame.Function)
		if !more {
			break
		}
	}
	select {
	case connection.writes <- event:
	default:
	}
	count, err := connection.TCPConn.Write(data)
	close(event.done)
	return count, err
}

type nativeObservedListener struct {
	net.Listener
	armed  *atomic.Bool
	writes chan *nativeSocketWrite
}

func (listener nativeObservedListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tcp := connection.(*net.TCPConn)
	if err := tcp.SetWriteBuffer(1024); err != nil {
		_ = tcp.Close()
		return nil, err
	}
	return &nativeObservedTCP{TCPConn: tcp, armed: listener.armed, writes: listener.writes}, nil
}

func nativeBlockedStatusWrite(test *testing.T) (*nativeHTTPFixture, *nativeSocketWrite, net.Conn) {
	test.Helper()
	fixture := newNativeHTTPFixture(test)
	asset := fixture.asset(test)
	flow := fixture.bootstrap(test)
	flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
	flow.authorize(test)
	encoded, err := proto.Marshal(flow.statusRequest(test).Msg)
	if err != nil {
		test.Fatal(err)
	}
	var armed atomic.Bool
	writes := make(chan *nativeSocketWrite, 1024)
	endpoint := httptest.NewUnstartedServer(NewWithOptions(fixture.owner, Options{PublicOrigin: fixture.origin, NativeConfiguration: fixture.service}))
	endpoint.Listener = nativeObservedListener{Listener: endpoint.Listener, armed: &armed, writes: writes}
	endpoint.StartTLS()
	test.Cleanup(endpoint.Close)
	raw, err := net.DialTimeout("tcp", endpoint.Listener.Addr().String(), time.Second)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = raw.Close() })
	if err := raw.(*net.TCPConn).SetReadBuffer(1024); err != nil {
		test.Fatal(err)
	}
	config := endpoint.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	config.ServerName = "127.0.0.1"
	connection := tls.Client(raw, config)
	if err := connection.HandshakeContext(test.Context()); err != nil {
		test.Fatal(err)
	}
	armed.Store(true)
	var pipeline bytes.Buffer
	for range 128 {
		fmt.Fprintf(&pipeline, "POST %sGetNativeMaterializationStatus HTTP/1.1\r\nHost: synthetic.invalid\r\nContent-Type: application/proto\r\nConnect-Protocol-Version: 1\r\nAuthorization: Bearer %s\r\nContent-Length: %d\r\n\r\n", nativeServicePath, flow.bearer, len(encoded))
		pipeline.Write(encoded)
	}
	if _, err := connection.Write(pipeline.Bytes()); err != nil {
		test.Fatal(err)
	}
	timeout := time.NewTimer(8 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-writes:
			application := false
			for _, frame := range event.frames {
				if frame == "net/http.(*response).finishRequest" || frame == "net/http.(*response).FlushError" {
					application = true
				}
			}
			if !application {
				continue
			}
			select {
			case <-event.done:
			case <-time.After(100 * time.Millisecond):
				return fixture, event, raw
			}
		case <-timeout.C:
			test.Fatal("real small-buffer TLS fixture did not reach a blocked application write")
		}
	}
}

func TestNativeHTTPProductionTwentySecondWriteBudget(test *testing.T) {
	_, event, _ := nativeBlockedStatusWrite(test)
	if event.deadline == 0 || time.Until(time.Unix(0, event.deadline)) > 20*time.Second {
		test.Error("application write lost its real production twenty-second deadline")
	}
	select {
	case <-event.done:
	case <-time.After(21 * time.Second):
		test.Error("actual TLS application write survived production budget plus one second")
	}
}

func TestNativeHTTPCloseDrainsActualResponseWrite(test *testing.T) {
	fixture, event, raw := nativeBlockedStatusWrite(test)
	entered, release := make(chan struct{}, 7), make(chan struct{})
	var holders sync.WaitGroup
	for range 7 {
		holders.Go(func() {
			_ = fixture.service.Call(test.Context(), func(context.Context) error {
				entered <- struct{}{}
				<-release
				return nil
			})
		})
	}
	for range 7 {
		<-entered
	}
	if err := fixture.service.Call(test.Context(), func(context.Context) error { return nil }); !errors.Is(err, state.ErrNativeQuota) {
		test.Error("actual blocked response write escaped the eight-slot admission count")
	}
	close(release)
	holders.Wait()
	closed := make(chan struct{})
	go func() { fixture.service.Close(); close(closed) }()
	defer func() { _ = raw.Close(); <-closed }()
	select {
	case <-closed:
		select {
		case <-event.done:
		default:
			test.Error("coordinator Close returned before actual response write drained")
		}
	case <-time.After(2 * time.Second):
		test.Error("coordinator cancellation did not interrupt actual TLS response write")
	}
}

func nativeSlowTLSRequest(test *testing.T, fixture *nativeHTTPFixture, bearer string) *http.Response {
	test.Helper()
	config := fixture.endpoint.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", fixture.endpoint.Listener.Addr().String(), config)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = connection.Close() })
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(connection, "POST %sGetNativeMaterializationStatus HTTP/1.1\r\nHost: synthetic.invalid\r\nContent-Type: application/proto\r\nConnect-Protocol-Version: 1\r\nAuthorization: Bearer %s\r\nContent-Length: 100\r\n\r\n", nativeServicePath, bearer); err != nil {
		test.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func nativeAssertRejection(test *testing.T, response *http.Response, status int, code string) {
	test.Helper()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		test.Fatal(err)
	}
	var rejection struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &rejection); err != nil {
		test.Fatal("unparseable bounded native rejection")
	}
	if response.StatusCode != status || rejection.Code != code || response.Header.Get("Cache-Control") != "no-store" {
		test.Errorf("cleanup changed original rejection: status=%d code=%s", response.StatusCode, rejection.Code)
	}
}

func TestNativeHTTPEightContinueSlowNinthKeepsQuota(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	flow := fixture.bootstrap(test)
	config := fixture.endpoint.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	for range 8 {
		connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", fixture.endpoint.Listener.Addr().String(), config)
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() { _ = connection.Close() })
		_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := fmt.Fprintf(connection, "POST %sPrepareNativeApply HTTP/1.1\r\nHost: synthetic.invalid\r\nContent-Type: application/proto\r\nConnect-Protocol-Version: 1\r\nContent-Length: 1000\r\nExpect: 100-continue\r\n\r\n", nativeServicePath); err != nil {
			test.Fatal(err)
		}
		response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
		if err != nil || response.StatusCode != 100 {
			test.Fatal("real Body.Read did not reach 100-continue admission barrier")
		}
		_ = response.Body.Close()
	}
	if err := fixture.service.Call(test.Context(), func(context.Context) error { return nil }); !errors.Is(err, state.ErrNativeQuota) {
		test.Fatal("eight real reads did not hold eight slots")
	}
	nativeAssertRejection(test, nativeSlowTLSRequest(test, fixture, flow.bearer), 429, "resource_exhausted")
	fixture.service.Close()
}

func TestNativeHTTPClosedSlowBodyKeepsUnavailable(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	flow := fixture.bootstrap(test)
	fixture.service.Close()
	nativeAssertRejection(test, nativeSlowTLSRequest(test, fixture, flow.bearer), 503, "unavailable")
}

func TestNativeHTTPAlreadyCanceledCallerRemainsCanceled(test *testing.T) {
	fixture := newNativeHTTPFixture(test, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			ctx, cancel := context.WithCancel(request.Context())
			cancel()
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	})
	nativeAssertRejection(test, nativeSlowTLSRequest(test, fixture, "synthetic-unused"), 499, "canceled")
}

func TestNativeHTTPRealH1KeepAliveAndH2SixRPC(test *testing.T) {
	for _, protocol := range []int{1, 2} {
		test.Run(fmt.Sprintf("HTTP%d", protocol), func(test *testing.T) {
			fixture := newNativeHTTPFixture(test)
			var calls atomic.Int32
			var reused atomic.Int32
			handler := NewWithOptions(fixture.owner, Options{PublicOrigin: fixture.origin, NativeConfiguration: fixture.service})
			endpoint := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.TLS == nil || request.ProtoMajor != protocol {
					test.Error("actual TLS/HTTP negotiation mismatch")
				}
				calls.Add(1)
				handler.ServeHTTP(writer, request)
			}))
			endpoint.EnableHTTP2 = protocol == 2
			endpoint.StartTLS()
			defer endpoint.Close()
			client := endpoint.Client()
			client.Transport.(*http.Transport).MaxConnsPerHost = 1
			fixture.client = configurationnativev1connect.NewNativeConfigurationServiceClient(client, endpoint.URL)
			trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
				if info.Reused {
					reused.Add(1)
				}
			}}
			client.Transport = nativeTraceTransport{RoundTripper: client.Transport, trace: trace}
			asset := fixture.asset(test)
			flow := fixture.bootstrap(test)
			flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
			flow.authorize(test)
			response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test))
			if err != nil || string(response.Msg.SlotValues[0].Plaintext) != "synthetic-native-http-private-sentinel" {
				test.Fatal("real keepalive/h2 materialization failed")
			}
			if flow.status(test).State != 3 || calls.Load() != 6 || reused.Load() != 5 {
				test.Fatal("six real RPCs did not complete on one reusable TLS connection")
			}
		})
	}
}

type nativeTraceTransport struct {
	http.RoundTripper
	trace *httptrace.ClientTrace
}

func (transport nativeTraceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport.RoundTripper.RoundTrip(request.WithContext(httptrace.WithClientTrace(request.Context(), transport.trace)))
}

type nativeEOFProbe struct {
	test        *testing.T
	background  chan struct{}
	finished    chan struct{}
	started     sync.Once
	armed       atomic.Bool
	interrupted atomic.Bool
	request     atomic.Pointer[context.Context]
	accepts     atomic.Int32
	calls       atomic.Int32
}

type nativeEOFListener struct {
	net.Listener
	probe *nativeEOFProbe
}

func (listener nativeEOFListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	listener.probe.accepts.Add(1)
	return nativeEOFConn{Conn: connection, probe: listener.probe}, nil
}

type nativeEOFConn struct {
	net.Conn
	probe *nativeEOFProbe
}

func nativeEOFStackIncludes(name string) bool {
	var callers [40]uintptr
	count := runtime.Callers(2, callers[:])
	frames := runtime.CallersFrames(callers[:count])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, name) {
			return true
		}
		if !more {
			return false
		}
	}
}

func (connection nativeEOFConn) Read(destination []byte) (int, error) {
	if connection.probe.armed.Load() && nativeEOFStackIncludes("net/http.(*connReader).backgroundRead") {
		connection.probe.started.Do(func() { close(connection.probe.background) })
	}
	return connection.Conn.Read(destination)
}

func (connection nativeEOFConn) SetReadDeadline(deadline time.Time) error {
	probe := connection.probe
	if probe.armed.Load() && !deadline.IsZero() && !deadline.After(time.Now()) && nativeEOFStackIncludes("httpapi.nativeConfigurationRequests") && probe.interrupted.CompareAndSwap(false, true) {
		err := connection.Conn.SetReadDeadline(deadline)
		select {
		case <-(*probe.request.Load()).Done():
			probe.test.Log("healthy EOF cleanup canceled the real H1 connection context")
		case <-time.After(3 * time.Second):
			probe.test.Error("expired-deadline regression probe did not observe its actual read interruption")
		}
		return err
	}
	return connection.Conn.SetReadDeadline(deadline)
}

type nativeEOFBody struct {
	io.ReadCloser
	probe *nativeEOFProbe
}

func (body nativeEOFBody) Read(destination []byte) (int, error) {
	count, err := body.ReadCloser.Read(destination)
	if errors.Is(err, io.EOF) {
		select {
		case <-body.probe.background:
		case <-time.After(3 * time.Second):
			body.probe.test.Error("real H1 EOF background reader did not enter")
		}
	}
	return count, err
}

type nativeEOFTransport struct {
	http.RoundTripper
	probe   *nativeEOFProbe
	chunked bool
}

func (transport nativeEOFTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.chunked {
		request = request.Clone(request.Context())
		request.ContentLength = -1
		request.TransferEncoding = []string{"chunked"}
	}
	response, err := transport.RoundTripper.RoundTrip(request)
	if err == nil && strings.HasSuffix(request.URL.Path, "/BindNativeConsumer") {
		select {
		case <-transport.probe.finished:
		case <-time.After(4 * time.Second):
			transport.probe.test.Error("real H1 Bind handler did not finish cleanup")
		}
	}
	return response, err
}

func TestNativeHTTPHealthyEOFBackgroundReadKeepsConnection(test *testing.T) {
	for _, chunked := range []bool{false, true} {
		for _, mode := range []configuration.AuthMode{configuration.APIKey, configuration.AuthToken, configuration.NativeLogin} {
			test.Run(fmt.Sprintf("chunked=%t/%s", chunked, mode), func(test *testing.T) {
				probe := &nativeEOFProbe{test: test, background: make(chan struct{}), finished: make(chan struct{})}
				fixture := newNativeHTTPFixtureWithListener(test, func(listener net.Listener) net.Listener {
					return nativeEOFListener{Listener: listener, probe: probe}
				}, func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
						if request.TLS == nil || request.ProtoMajor != 1 || request.Context().Err() != nil {
							test.Error("healthy TLS H1 request arrived with canceled connection context")
						}
						probe.calls.Add(1)
						if strings.HasSuffix(request.URL.Path, "/BindNativeConsumer") {
							ctx := request.Context()
							probe.request.Store(&ctx)
							probe.armed.Store(true)
							request.Body = nativeEOFBody{ReadCloser: request.Body, probe: probe}
							defer close(probe.finished)
						}
						next.ServeHTTP(writer, request)
					})
				})
				fixture.endpoint.Client().Transport = nativeEOFTransport{RoundTripper: fixture.endpoint.Client().Transport, probe: probe, chunked: chunked}
				var mutations []configuration.SecretMutation
				if mode != configuration.NativeLogin {
					slot := configuration.APIKeySlot
					if mode == configuration.AuthToken {
						slot = configuration.AuthTokenSlot
					}
					mutations = []configuration.SecretMutation{{Slot: slot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-eof-selected-slot"))}}
				}
				asset, err := fixture.domain.Create(test.Context(), fixture.ownerID, configuration.Draft{Name: "healthy EOF", Provider: configuration.ClaudeProvider{AuthMode: mode}}, mutations)
				if err != nil {
					test.Fatal(err)
				}
				flow := fixture.bootstrap(test)
				flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
				flow.authorize(test)
				calls, status := int32(5), nativev1.GrantState(1)
				if mode != configuration.NativeLogin {
					response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test))
					if err != nil || len(response.Msg.SlotValues) != 1 || string(response.Msg.SlotValues[0].Plaintext) != "synthetic-eof-selected-slot" {
						test.Fatal("healthy EOF connection did not deliver the real selected secret")
					}
					calls, status = 6, 3
				} else if flow.grant.MaterializationRequired || len(flow.grant.GrantToken) != 0 {
					test.Fatal("native-login minted a secret grant")
				}
				if flow.status(test).State != status || probe.calls.Load() != calls || probe.accepts.Load() != 1 {
					test.Fatalf("healthy EOF lost actual connection reuse: accepts=%d calls=%d", probe.accepts.Load(), probe.calls.Load())
				}
				test.Logf("healthy TLS H1 EOF completed with accepts=%d calls=%d", probe.accepts.Load(), probe.calls.Load())
			})
		}
	}
}
