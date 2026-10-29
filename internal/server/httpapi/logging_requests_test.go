package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/logging"
)

func requestLogRecords(test *testing.T, directory string) []map[string]any {
	test.Helper()
	files, err := os.ReadDir(filepath.Join(directory, "logs"))
	if err != nil {
		test.Fatal("read isolated log directory")
	}
	var records []map[string]any
	for _, entry := range files {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, "logs", entry.Name()))
		if err != nil {
			test.Fatal("read isolated log segment")
		}
		if bytes.Contains(data, []byte("synthetic-request-private")) {
			test.Fatal("request logging exposed synthetic private input")
		}
		for line := range bytes.SplitSeq(data, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var record map[string]any
			if err := json.Unmarshal(line, &record); err != nil {
				test.Fatal("request logging produced invalid JSON")
			}
			records = append(records, record)
		}
	}
	return records
}

func TestRequestLoggingStreamsAndExcludesPrivateInput(test *testing.T) {
	for _, http2 := range []bool{false, true} {
		test.Run(fmt.Sprintf("http2=%t", http2), func(test *testing.T) { testRequestLoggingStream(test, http2) })
	}
}

func testRequestLoggingStream(test *testing.T, http2 bool) {
	manager, directory := testLogManager(test)
	if err := manager.Record(logging.Event{Kind: logging.EventServerStarted}); err != nil {
		test.Fatal("write synthetic event control")
	}
	before := len(requestLogRecords(test, directory))
	if before == 0 {
		test.Fatal("log capture control did not produce a record")
	}
	release := make(chan struct{})
	endpoint := httptest.NewUnstartedServer(observeRequests(manager, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if (request.ProtoMajor == 2) != http2 {
			test.Error("logging fixture did not use the requested HTTP protocol")
		}
		if err := http.NewResponseController(writer).SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
			test.Error("logging wrapper lost the actual write deadline capability")
		}
		writer.Header().Set("Trailer", "X-Stream-Result")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(writer, "synthetic-request-private-body\n")
		if err := http.NewResponseController(writer).Flush(); err != nil {
			test.Error("logging wrapper lost streaming flush")
			return
		}
		select {
		case <-release:
		case <-request.Context().Done():
			return
		}
		writer.Header().Set("X-Stream-Result", "complete")
		_, _ = io.WriteString(writer, "tail\n")
	})))
	endpoint.EnableHTTP2 = http2
	endpoint.Config.ErrorLog = manager.HTTPErrorLog()
	endpoint.StartTLS()
	test.Cleanup(endpoint.Close)
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, endpoint.URL+"/synthetic-request-private-path?secret=synthetic-request-private-query", strings.NewReader("synthetic-request-private-input"))
	if err != nil {
		test.Fatal("prepare streaming request fixture")
	}
	request.Header.Set("Authorization", "Bearer synthetic-request-private-token")
	request.Header.Set("X-API-Key", "synthetic-request-private-key")
	client := endpoint.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Do(request)
	if err != nil {
		close(release)
		test.Fatal("streaming response was buffered or failed")
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	close(release)
	if err != nil || line != "synthetic-request-private-body\n" || response.StatusCode != http.StatusAccepted {
		_ = response.Body.Close()
		test.Fatal("logging changed streaming status or first chunk")
	}
	tail, readErr := io.ReadAll(reader)
	_ = response.Body.Close()
	endpoint.Close()
	if readErr != nil || string(tail) != "tail\n" || response.Trailer.Get("X-Stream-Result") != "complete" {
		test.Fatal("logging changed the stream tail or trailers")
	}
	after := requestLogRecords(test, directory)
	if len(after) <= before {
		test.Fatal("real HTTP request did not produce a private JSON log event")
	}
}

func TestRequestLoggingNativeSixRPCKeepaliveAndPrivacy(test *testing.T) {
	for _, mode := range []configuration.AuthMode{configuration.APIKey, configuration.AuthToken, configuration.NativeLogin} {
		test.Run(string(mode), func(test *testing.T) {
			manager, directory := testLogManager(test)
			fixture := newNativeHTTPFixture(test, func(next http.Handler) http.Handler { return observeRequests(manager, next) })
			slot := configuration.APIKeySlot
			if mode == configuration.AuthToken {
				slot = configuration.AuthTokenSlot
			}
			const private = "synthetic-request-private-native"
			var mutations []configuration.SecretMutation
			if mode != configuration.NativeLogin {
				mutations = []configuration.SecretMutation{{Slot: slot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte(private))}}
			}
			asset, err := fixture.domain.Create(test.Context(), fixture.ownerID, configuration.Draft{Name: "logging fixture", Provider: configuration.ClaudeProvider{AuthMode: mode}}, mutations)
			if err != nil {
				test.Fatal("create synthetic native logging fixture")
			}
			flow := fixture.bootstrap(test)
			flow.prepare(test, asset.Metadata.ID, asset.Metadata.Revision)
			flow.authorize(test)
			if mode != configuration.NativeLogin {
				response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test))
				if err != nil || len(response.Msg.SlotValues) != 1 || string(response.Msg.SlotValues[0].Plaintext) != private || flow.status(test).State != 3 {
					test.Fatal("logging changed one-time native material consumption")
				}
			} else if flow.grant.MaterializationRequired || len(flow.grant.GrantToken) != 0 || flow.status(test).State != 1 {
				test.Fatal("logging changed the native-login no-secret flow")
			}
			fixture.endpoint.Close()
			if len(requestLogRecords(test, directory)) < 5 {
				test.Fatal("native requests did not produce operational events")
			}
		})
	}
}

func TestRequestLoggingSanitizesTransportPanic(test *testing.T) {
	manager, directory := testLogManager(test)
	endpoint := httptest.NewUnstartedServer(observeRequests(manager, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("synthetic-request-private-panic")
	})))
	endpoint.Config.ErrorLog = manager.HTTPErrorLog()
	endpoint.Start()
	response, err := endpoint.Client().Get(endpoint.URL)
	if response != nil {
		_ = response.Body.Close()
	}
	endpoint.Close()
	if err == nil {
		test.Fatal("panic fixture unexpectedly returned a successful response")
	}
	events := requestLogRecords(test, directory)
	transport, completed := false, false
	for _, event := range events {
		transport = transport || event["msg"] == "transport_error"
		completed = completed || event["msg"] == "request_completed" && event["status"] == float64(500)
	}
	if !transport || !completed {
		test.Fatal("sanitized transport failure or panic request result was not logged")
	}
}

func TestRequestLoggingFailureDoesNotChangeBusinessResponse(test *testing.T) {
	manager, _ := testLogManager(test)
	if err := manager.Close(); err != nil {
		test.Fatal("close failure fixture logger")
	}
	next := observeRequests(manager, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Business", "preserved")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(writer, "authorized-business-rejection")
	}))
	recorder := httptest.NewRecorder()
	next.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusForbidden || recorder.Body.String() != "authorized-business-rejection" || recorder.Header().Get("X-Business") != "preserved" {
		test.Fatal("logging failure changed business authorization or response")
	}
}
