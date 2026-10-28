package httpapi

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/skillhub/gen/skillhub/import/v1"
	"github.com/ca-x/skillhub/gen/skillhub/import/v1/importv1connect"
	"github.com/ca-x/skillhub/internal/server/importer"
	"google.golang.org/protobuf/encoding/protojson"
)

func snapshotRPCFixture(test *testing.T, extra string) []byte {
	test.Helper()
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	connection, err := database.Conn(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(test.Context(), `CREATE TABLE skill_repos(owner TEXT, name TEXT, branch TEXT, enabled INTEGER); INSERT INTO skill_repos VALUES ('Example','Toolkit','HEAD',1),('example','disabled','head',0); CREATE TABLE providers(secret TEXT); INSERT INTO providers VALUES ('SNAPSHOT-SECRET-SENTINEL');`+extra); err != nil {
		test.Fatal(err)
	}
	var data []byte
	if err := connection.Raw(func(driverConnection any) error {
		serializer, ok := driverConnection.(interface{ Serialize() ([]byte, error) })
		if !ok {
			return errors.New("serialization unavailable")
		}
		data, err = serializer.Serialize()
		return err
	}); err != nil {
		test.Fatal(err)
	}
	return data
}

func TestImportCCSwitchSnapshotOwnerAndNoCollection(test *testing.T) {
	fixture := newImportFixture(test)
	entry, err := fixture.collection.Collect(test.Context(), fixture.owners[0], "example/toolkit", "")
	if err != nil {
		test.Fatal(err)
	}
	before := fixture.fetches.Load()
	data := snapshotRPCFixture(test, "")
	for index, session := range fixture.sessions[:2] {
		request := connect.NewRequest(&importv1.PreviewCCSwitchSnapshotRequest{SnapshotData: bytes.Clone(data)})
		request.Header().Set("Authorization", "Bearer "+session)
		response, err := fixture.client.PreviewCCSwitchSnapshot(test.Context(), request)
		if err != nil || len(response.Msg.Repositories) != 2 {
			test.Fatalf("snapshot RPC preview unavailable: %v", err)
		}
		for _, candidate := range response.Msg.Repositories {
			if candidate.SourceEnabled == nil || candidate.Branch != "" {
				test.Fatal("source enabled or default branch metadata missing")
			}
			if candidate.Repository == "example/toolkit" && (index == 0 && candidate.ExistingRepositoryId != entry.ID || index == 1 && candidate.ExistingRepositoryId != "" || !candidate.GetSourceEnabled()) {
				test.Fatal("existing repository hint crossed owner or lost enabled state")
			}
			if candidate.Repository == "example/disabled" && candidate.GetSourceEnabled() {
				test.Fatal("disabled source was silently enabled")
			}
		}
	}
	if fixture.fetches.Load() != before {
		test.Fatal("snapshot preview fetched repository content")
	}
	entries, err := fixture.collection.List(test.Context(), fixture.owners[1])
	if err != nil || len(entries) != 0 {
		test.Fatal("snapshot preview collected repository records")
	}
}

func TestImportCCSwitchSnapshotLimitsAndSafeErrors(test *testing.T) {
	fixture := newImportFixture(test)
	large := snapshotRPCFixture(test, `CREATE TABLE padding(value BLOB); WITH RECURSIVE records(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM records WHERE value<2400) INSERT INTO padding SELECT zeroblob(1024) FROM records;`)
	if len(large) <= 2<<20 {
		test.Fatal("fixture does not exercise separate snapshot wire limit")
	}
	request := connect.NewRequest(&importv1.PreviewCCSwitchSnapshotRequest{SnapshotData: large})
	request.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
	if _, err := fixture.client.PreviewCCSwitchSnapshot(test.Context(), request); err != nil {
		test.Fatalf("bounded snapshot larger than Magpie transport was rejected: %v", err)
	}
	for _, testCase := range []struct {
		data []byte
		code connect.Code
	}{
		{nil, connect.CodeInvalidArgument},
		{[]byte("SNAPSHOT-SECRET-SENTINEL"), connect.CodeFailedPrecondition},
		{make([]byte, importer.MaxCCSwitchSnapshotBytes+1), connect.CodeResourceExhausted},
	} {
		request := connect.NewRequest(&importv1.PreviewCCSwitchSnapshotRequest{SnapshotData: testCase.data})
		request.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
		_, err := fixture.client.PreviewCCSwitchSnapshot(test.Context(), request)
		if connect.CodeOf(err) != testCase.code || strings.Contains(err.Error(), "SNAPSHOT-SECRET-SENTINEL") {
			test.Fatalf("unsafe snapshot rejection code: %v", connect.CodeOf(err))
		}
	}
	magpie := connect.NewRequest(&importv1.PreviewLibraryExportRequest{Format: importv1.ExportFormat_EXPORT_FORMAT_MAGPIE_LIBRARY_JSON, ExportData: make([]byte, (1<<20)+1)})
	magpie.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
	if _, err := fixture.client.PreviewLibraryExport(test.Context(), magpie); connect.CodeOf(err) != connect.CodeResourceExhausted {
		test.Fatal("snapshot upload weakened Magpie raw byte limit")
	}
}

func TestImportCCSwitchSnapshotRejectsBeforeRead(test *testing.T) {
	fixture := newImportFixture(test)
	for _, session := range []string{"", "invalid-session"} {
		body := &importUnreadBody{}
		request := httptest.NewRequest(http.MethodPost, importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure, body)
		request.Host = "127.0.0.1"
		request.Header.Set("Content-Type", "application/json")
		if session != "" {
			request.Header.Set("Authorization", "Bearer "+session)
		}
		response := httptest.NewRecorder()
		fixture.server.Config.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || body.reads != 0 {
			test.Fatalf("snapshot pre-read authentication status=%d reads=%d", response.Code, body.reads)
		}
	}
	response, err := fixture.server.Client().Post(fixture.server.URL+importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure, "application/json", io.NopCloser(strings.NewReader(`{}`)))
	if err != nil {
		test.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		test.Fatal("anonymous snapshot preview was accepted")
	}
}

func TestImportCCSwitchSnapshotEncodingsAndWireBounds(test *testing.T) {
	fixture := newImportFixture(test)
	data := snapshotRPCFixture(test, "")
	for _, options := range [][]connect.ClientOption{{}, {connect.WithGRPC()}, {connect.WithGRPCWeb()}, {connect.WithProtoJSON()}} {
		client := importv1connect.NewImportServiceClient(fixture.server.Client(), fixture.server.URL, options...)
		request := connect.NewRequest(&importv1.PreviewCCSwitchSnapshotRequest{SnapshotData: bytes.Clone(data)})
		request.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
		response, err := client.PreviewCCSwitchSnapshot(test.Context(), request)
		if err != nil || len(response.Msg.Repositories) != 2 {
			test.Fatalf("snapshot protocol rejected identity request: %v", err)
		}
	}
	for _, header := range []string{"Content-Encoding", "Grpc-Encoding", "Connect-Content-Encoding"} {
		body := &importUnreadBody{}
		request := httptest.NewRequest(http.MethodPost, importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure, body)
		request.Host = "127.0.0.1"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+fixture.sessions[0])
		request.Header.Set(header, "gzip")
		response := httptest.NewRecorder()
		fixture.server.Config.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || body.reads != 0 {
			test.Fatal("snapshot compressed request was read before rejection")
		}
	}
	body := &importUnreadBody{}
	request := httptest.NewRequest(http.MethodPost, importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure, body)
	request.Host = "127.0.0.1"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+fixture.sessions[0])
	request.ContentLength = importSnapshotMaxWireBytes + 1
	response := httptest.NewRecorder()
	fixture.server.Config.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || body.reads != 0 {
		test.Fatal("oversized snapshot wire body was read before rejection")
	}
	encoded, err := protojson.Marshal(&importv1.PreviewCCSwitchSnapshotRequest{SnapshotData: bytes.Clone(data)})
	if err != nil {
		test.Fatal(err)
	}
	jsonRequest, err := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.server.URL+importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure, bytes.NewReader(encoded))
	if err != nil {
		test.Fatal(err)
	}
	jsonRequest.Header.Set("Authorization", "Bearer "+fixture.sessions[0])
	jsonRequest.Header.Set("Content-Type", "application/json")
	jsonRequest.Header.Set("Connect-Protocol-Version", "1")
	jsonResponse, err := fixture.server.Client().Do(jsonRequest)
	if err != nil {
		test.Fatal(err)
	}
	defer jsonResponse.Body.Close()
	result, err := io.ReadAll(jsonResponse.Body)
	if err != nil || jsonResponse.StatusCode != http.StatusOK || bytes.Contains(result, []byte("SNAPSHOT-SECRET-SENTINEL")) || !bytes.Contains(result, []byte(`"sourceEnabled":false`)) {
		test.Fatal("snapshot JSON response lost disabled state or exposed unselected secret")
	}
}

type importResponseObserver struct {
	http.ResponseWriter
	entered chan struct{}
	once    sync.Once
}

func (writer *importResponseObserver) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *importResponseObserver) Write(data []byte) (int, error) {
	writer.once.Do(func() { close(writer.entered) })
	return writer.ResponseWriter.Write(data)
}

func TestImportSnapshotCloseUnblocksResponseWrite(test *testing.T) {
	fixture := newImportFixture(test)
	data := snapshotRPCFixture(test, `DELETE FROM skill_repos; WITH RECURSIVE records(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM records WHERE value<1000) INSERT INTO skill_repos SELECT 'example','candidate-'||printf('%0180d',value),'branch-'||printf('%0180d',value),1 FROM records;`)
	previewRequest := connect.NewRequest(&importv1.PreviewCCSwitchSnapshotRequest{SnapshotData: data})
	previewRequest.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
	preview, err := fixture.client.PreviewCCSwitchSnapshot(test.Context(), previewRequest)
	if err != nil || len(preview.Msg.Repositories) != 1000 {
		test.Fatalf("large bounded snapshot fixture was not valid: %v", err)
	}
	entered := make(chan struct{})
	finished := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(finished)
		fixture.server.Config.Handler.ServeHTTP(&importResponseObserver{ResponseWriter: writer, entered: entered}, request)
	}))
	server.Config.ConnState = func(connection net.Conn, state http.ConnState) {
		if state == http.StateNew {
			socket, ok := connection.(*net.TCPConn)
			if !ok || socket.SetWriteBuffer(4<<10) != nil {
				test.Error("response fixture could not bound socket write buffer")
			}
		}
	}
	server.Start()
	test.Cleanup(server.Close)
	address, err := net.ResolveTCPAddr("tcp", server.Listener.Addr().String())
	if err != nil {
		test.Fatal(err)
	}
	connection, err := net.DialTCP("tcp", nil, address)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = connection.Close() })
	if err := connection.SetReadBuffer(1024); err != nil {
		test.Fatal(err)
	}
	encoded, err := protojson.Marshal(&importv1.PreviewCCSwitchSnapshotRequest{SnapshotData: data})
	if err != nil {
		test.Fatal(err)
	}
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, server.URL+importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure, bytes.NewReader(encoded))
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+fixture.sessions[0])
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	if err := request.Write(connection); err != nil {
		test.Fatal("could not send bounded snapshot fixture")
	}
	importAwait(test, entered)
	closed := make(chan error, 1)
	go func() { closed <- fixture.imports.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		test.Fatal("Close did not interrupt the stalled preview response writer")
	}
	importAwait(test, finished)
}

func TestImportSnapshotAndMagpieOverHTTP2(test *testing.T) {
	fixture := newImportFixture(test)
	var protocol atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		protocol.Store(int32(request.ProtoMajor))
		fixture.server.Config.Handler.ServeHTTP(writer, request)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	test.Cleanup(server.Close)
	client := importv1connect.NewImportServiceClient(server.Client(), server.URL, connect.WithGRPC())
	data := snapshotRPCFixture(test, "")
	for range 2 {
		snapshot := connect.NewRequest(&importv1.PreviewCCSwitchSnapshotRequest{SnapshotData: bytes.Clone(data)})
		snapshot.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
		response, err := client.PreviewCCSwitchSnapshot(test.Context(), snapshot)
		if err != nil || len(response.Msg.Repositories) != 2 || protocol.Load() != 2 {
			test.Fatalf("snapshot HTTP/2 gRPC preview: %v protocol=%d", err, protocol.Load())
		}
		magpie := connect.NewRequest(&importv1.PreviewLibraryExportRequest{Format: importv1.ExportFormat_EXPORT_FORMAT_MAGPIE_LIBRARY_JSON, ExportData: []byte(`{"skills":[]}`)})
		magpie.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
		if _, err := client.PreviewLibraryExport(test.Context(), magpie); err != nil || protocol.Load() != 2 {
			test.Fatalf("Magpie HTTP/2 gRPC preview: %v protocol=%d", err, protocol.Load())
		}
	}
}
