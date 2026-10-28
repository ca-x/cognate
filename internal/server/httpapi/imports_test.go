package httpapi

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	libraryv1 "github.com/ca-x/cognate/gen/skillhub/library/v1"
	"github.com/ca-x/cognate/gen/skillhub/library/v1/libraryv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	"google.golang.org/protobuf/proto"
)

type importFixture struct {
	authentication *auth.Service
	collection     *library.Service
	imports        *ImportHandler
	server         *httptest.Server
	client         importv1connect.ImportServiceClient
	sessions       []string
	owners         []string
	fetches        atomic.Int64
	cachePath      string
	fetch          graphTransport
}

type importUnreadBody struct{ reads int }

func (body *importUnreadBody) Read(data []byte) (int, error) { body.reads++; return 0, io.EOF }
func (body *importUnreadBody) Close() error                  { return nil }

func TestImportPreviewRPCOwnerIsolation(test *testing.T) {
	fixture := newImportFixture(test)
	entry, err := fixture.collection.Collect(test.Context(), fixture.owners[0], "Example/Toolkit", "HEAD")
	if err != nil {
		test.Fatal(err)
	}
	data := []byte(`{"skills":[{"name":"one","source":{"kind":"github","repo":"EXAMPLE/TOOLKIT","ref":"HEAD"}}]}`)
	for index, session := range fixture.sessions[:2] {
		response, err := fixture.client.PreviewLibraryExport(test.Context(), importRequest(data, session))
		if err != nil {
			test.Fatal(err)
		}
		expected := ""
		if index == 0 {
			expected = entry.ID
		}
		if len(response.Msg.Repositories) != 1 || response.Msg.Repositories[0].ExistingRepositoryId != expected {
			test.Fatalf("owner hint: %v", response.Msg)
		}
	}
	if err := fixture.authentication.Logout(test.Context(), fixture.sessions[0]); err != nil {
		test.Fatal(err)
	}
	for _, session := range []string{"", fixture.sessions[0], "shp1.pack-token", "shm1.mcp-token"} {
		body := &importUnreadBody{}
		request := httptest.NewRequest(http.MethodPost, importv1connect.ImportServicePreviewLibraryExportProcedure, body)
		request.Host = "127.0.0.1"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+session)
		response := httptest.NewRecorder()
		fixture.server.Config.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || body.reads != 0 {
			test.Fatalf("predecode auth: status=%d reads=%d body=%s", response.Code, body.reads, response.Body)
		}
	}
}

func TestImportPreviewRPCBoundsAndFormats(test *testing.T) {
	fixture := newImportFixture(test)
	for _, testcase := range []struct {
		name   string
		data   []byte
		format importv1.ExportFormat
		code   connect.Code
	}{
		{"one MiB", []byte(`{"skills":[]}` + strings.Repeat(" ", importer.MaxMagpieExportBytes-len(`{"skills":[]}`))), importv1.ExportFormat_EXPORT_FORMAT_MAGPIE_LIBRARY_JSON, 0},
		{"over one MiB", []byte(`{"skills":[]}` + strings.Repeat(" ", importer.MaxMagpieExportBytes+1-len(`{"skills":[]}`))), importv1.ExportFormat_EXPORT_FORMAT_MAGPIE_LIBRARY_JSON, connect.CodeResourceExhausted},
		{"empty", nil, importv1.ExportFormat_EXPORT_FORMAT_MAGPIE_LIBRARY_JSON, connect.CodeInvalidArgument},
		{"unspecified", []byte(`{"skills":[]}`), 0, connect.CodeInvalidArgument},
		{"unknown", []byte(`{"skills":[]}`), 99, connect.CodeInvalidArgument},
		{"malformed", []byte(`{"secret":"IMPORT_PRIVATE_SENTINEL"`), 1, connect.CodeFailedPrecondition},
		{"null", []byte(`{"skills":null}`), 1, 0},
		{"missing skills", []byte(`{}`), 1, connect.CodeFailedPrecondition},
	} {
		test.Run(testcase.name, func(test *testing.T) {
			request := importRequest(testcase.data, fixture.sessions[0])
			request.Msg.Format = testcase.format
			_, err := fixture.client.PreviewLibraryExport(test.Context(), request)
			if testcase.code == 0 && err != nil || testcase.code != 0 && connect.CodeOf(err) != testcase.code {
				test.Fatalf("code=%v want=%v err=%v", connect.CodeOf(err), testcase.code, err)
			}
		})
	}
	base := `{"format":1,"exportData":"` + base64.StdEncoding.EncodeToString([]byte(`{"skills":[]}`)) + `"}`
	for _, unknownLength := range []bool{false, true} {
		for _, size := range []int{2 << 20, (2 << 20) + 1} {
			request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.server.URL+importv1connect.ImportServicePreviewLibraryExportProcedure, strings.NewReader(base+strings.Repeat(" ", size-len(base))))
			if err != nil {
				test.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+fixture.sessions[0])
			request.Header.Set("Content-Type", "application/json")
			if unknownLength {
				request.ContentLength = -1
			}
			response, err := fixture.server.Client().Do(request)
			if err != nil {
				test.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				test.Fatal(err)
			}
			want := http.StatusOK
			if size > 2<<20 {
				want = http.StatusTooManyRequests
			}
			if response.StatusCode != want {
				test.Fatalf("wire size=%d chunked=%v status=%d body=%s", size, unknownLength, response.StatusCode, body)
			}
		}
	}
	for _, header := range []string{"Content-Encoding", "Grpc-Encoding", "Connect-Content-Encoding"} {
		body := &importUnreadBody{}
		request := httptest.NewRequest(http.MethodPost, importv1connect.ImportServicePreviewLibraryExportProcedure, body)
		request.Host = "127.0.0.1"
		request.Header.Set("Authorization", "Bearer "+fixture.sessions[0])
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(header, "gzip")
		response := httptest.NewRecorder()
		fixture.server.Config.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || body.reads != 0 {
			test.Fatalf("compressed %s status=%d reads=%d", header, response.Code, body.reads)
		}
	}
	skills := make([]map[string]string, 1001)
	for index := range skills {
		skills[index] = map[string]string{"name": "skill-" + strings.Repeat("a", index/26) + string(rune('a'+index%26))}
	}
	data, err := json.Marshal(map[string]any{"skills": skills})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.client.PreviewLibraryExport(test.Context(), importRequest(data, fixture.sessions[0])); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		test.Fatalf("1001 references: %v", err)
	}
}

type importObservedBody struct {
	io.ReadCloser
	entered chan struct{}
	once    sync.Once
}

func (body *importObservedBody) Read(data []byte) (int, error) {
	body.once.Do(func() { close(body.entered) })
	return body.ReadCloser.Read(data)
}

func importAwait(test *testing.T, signal <-chan struct{}) {
	test.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		test.Fatal("request barrier timed out")
	}
}

func TestImportPreviewAdmissionAndClose(test *testing.T) {
	fixture := newImportFixture(test)
	entered := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	finished := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	guarded := fixture.server.Config.Handler
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if marker := request.Header.Get("X-Import-Barrier"); marker != "" {
			index := int(marker[0] - '0')
			defer close(finished[index])
			request.Body = &importObservedBody{ReadCloser: request.Body, entered: entered[index]}
		}
		guarded.ServeHTTP(writer, request)
	}))
	test.Cleanup(server.Close)
	start := func(index int) net.Conn {
		test.Helper()
		connection, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() { _ = connection.Close() })
		if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			test.Fatal(err)
		}
		_, err = fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nX-Import-Barrier: %d\r\nContent-Length: 1024\r\n\r\n{", importv1connect.ImportServicePreviewLibraryExportProcedure, server.Listener.Addr(), fixture.sessions[index], index)
		if err != nil {
			test.Fatal(err)
		}
		importAwait(test, entered[index])
		return connection
	}
	first := start(0)
	second := start(1)
	for _, session := range []string{fixture.sessions[0], fixture.sessions[2]} {
		body := &importUnreadBody{}
		request := httptest.NewRequest(http.MethodPost, importv1connect.ImportServicePreviewLibraryExportProcedure, body)
		request.Host = "127.0.0.1"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+session)
		response := httptest.NewRecorder()
		guarded.ServeHTTP(response, request)
		if response.Code != http.StatusTooManyRequests || body.reads != 0 {
			test.Fatalf("admission status=%d reads=%d", response.Code, body.reads)
		}
	}
	_ = first.Close()
	importAwait(test, finished[0])
	third := start(2)
	closed := make(chan struct{})
	go func() { _ = fixture.imports.Close(); close(closed) }()
	importAwait(test, closed)
	importAwait(test, finished[1])
	importAwait(test, finished[2])
	for _, connection := range []net.Conn{second, third} {
		response, err := http.ReadResponse(bufio.NewReader(connection), nil)
		if err != nil {
			test.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 499 || !bytes.Contains(data, []byte(`"code":"canceled"`)) {
			test.Fatalf("close response status=%d body=%s", response.StatusCode, data)
		}
	}
	if err := fixture.imports.Close(); err != nil {
		test.Fatal(err)
	}
	body := &importUnreadBody{}
	request := httptest.NewRequest(http.MethodPost, "/", body)
	response := httptest.NewRecorder()
	fixture.imports.Guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { test.Error("closed handler entered") })).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || body.reads != 0 {
		test.Fatalf("after close status=%d reads=%d", response.Code, body.reads)
	}
}

func TestImportPreviewRejectedSlowBodyClosesConnection(test *testing.T) {
	fixture := newImportFixture(test)
	closed := make(chan struct{})
	server := httptest.NewUnstartedServer(fixture.server.Config.Handler)
	server.Config.ConnState = func(connection net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			close(closed)
		}
	}
	server.Start()
	test.Cleanup(server.Close)
	connection, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		test.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		test.Fatal(err)
	}
	_, err = fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: 1024\r\n\r\n{", importv1connect.ImportServicePreviewLibraryExportProcedure, server.Listener.Addr())
	if err != nil {
		test.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		test.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		test.Fatalf("unauthorized slow body status=%d", response.StatusCode)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	importAwait(test, closed)
}

func newImportFixture(test *testing.T) *importFixture {
	test.Helper()
	authentication, store := testServiceAndStore(test)
	fixture := &importFixture{authentication: authentication, cachePath: filepath.Join(test.TempDir(), "objects")}
	cache, err := library.OpenArchiveCache(fixture.cachePath)
	if err != nil {
		test.Fatal(err)
	}
	archive := libraryArchive(test, "---\nname: Example\ndescription: A useful skill\n---\nInstructions")
	upstream := &http.Client{Transport: graphTransport(func(request *http.Request) (*http.Response, error) {
		fixture.fetches.Add(1)
		if fixture.fetch != nil {
			return fixture.fetch(request)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}
	fixture.collection, err = library.NewService(store, cache, repository.NewArchiveFetcher(upstream))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = fixture.collection.Close() })
	fixture.imports, err = NewImportHandler(authentication, fixture.collection)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = fixture.imports.Close() })
	for _, username := range []string{"alice", "bob", "charlie"} {
		password := []byte(rand.Text())
		owner, err := authentication.Register(test.Context(), username, password)
		if err != nil {
			test.Fatal(err)
		}
		session, err := authentication.Login(test.Context(), username, password)
		if err != nil {
			test.Fatal(err)
		}
		fixture.owners = append(fixture.owners, owner)
		fixture.sessions = append(fixture.sessions, session.Reveal())
	}
	fixture.server = httptest.NewServer(NewWithOptions(authentication, Options{Library: fixture.collection, Imports: fixture.imports}))
	test.Cleanup(fixture.server.Close)
	fixture.client = importv1connect.NewImportServiceClient(fixture.server.Client(), fixture.server.URL)
	return fixture
}

type importActionBody struct {
	io.ReadCloser
	action func()
	once   sync.Once
}

func (body *importActionBody) Read(data []byte) (int, error) {
	body.once.Do(body.action)
	return body.ReadCloser.Read(data)
}

func TestImportPreviewRPCReauthAfterRead(test *testing.T) {
	for _, mode := range []string{"revoked", "owner changed"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newImportFixture(test)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				request.Body = &importActionBody{ReadCloser: request.Body, action: func() {
					if mode == "revoked" {
						if err := fixture.authentication.Logout(request.Context(), fixture.sessions[0]); err != nil {
							test.Error(err)
						}
					} else {
						request.Header.Set("Authorization", "Bearer "+fixture.sessions[1])
					}
				}}
				fixture.server.Config.Handler.ServeHTTP(writer, request)
			}))
			test.Cleanup(server.Close)
			client := importv1connect.NewImportServiceClient(server.Client(), server.URL)
			if _, err := client.PreviewLibraryExport(test.Context(), importRequest([]byte(`{"skills":[]}`), fixture.sessions[0])); connect.CodeOf(err) != connect.CodeUnauthenticated {
				test.Fatalf("session changed during upload: %v", err)
			}
		})
	}
}

func TestImportPreviewRPCSourceSemanticsAndRedaction(test *testing.T) {
	fixture := newImportFixture(test)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	test.Cleanup(func() { log.SetOutput(previous) })
	fixture.server.Config.ErrorLog = log.New(&logs, "", 0)
	data, err := os.ReadFile("../importer/testdata/magpie/library.json")
	if err != nil {
		test.Fatal(err)
	}
	response, err := fixture.client.PreviewLibraryExport(test.Context(), importRequest(data, fixture.sessions[0]))
	if err != nil {
		test.Fatal(err)
	}
	for index, branch := range []string{"", "release/v2", "head"} {
		candidate := response.Msg.Repositories[index]
		if candidate.Repository != "example/toolkit" || candidate.Branch != branch {
			test.Fatalf("source semantics: %v", response.Msg)
		}
	}
	encoded, err := json.Marshal(response.Msg)
	if err != nil {
		test.Fatal(err)
	}
	for _, secret := range []string{"MAGPIE_FIXTURE_SECRET", "MAGPIE_FIXTURE_PRIVATE", "do-not-execute", "ignored-content-hash"} {
		if bytes.Contains(encoded, []byte(secret)) || strings.Contains(logs.String(), secret) {
			test.Fatal("preview leaked private source metadata")
		}
	}
	for _, body := range []string{
		`{"format":"IMPORT_PRIVATE_SENTINEL","exportData":""}`,
		`{"format":1,"exportData":"IMPORT_PRIVATE_SENTINEL!!"}`,
		`{"format":1,"exportData":"` + base64.StdEncoding.EncodeToString([]byte(`{"skills":[{"name":"one","source":{"kind":"github","repo":"org/repo","path":"/IMPORT_PRIVATE_SENTINEL"}}]}`)) + `"}`,
	} {
		request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.server.URL+importv1connect.ImportServicePreviewLibraryExportProcedure, strings.NewReader(body))
		if err != nil {
			test.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+fixture.sessions[0])
		request.Header.Set("Content-Type", "application/json")
		response, err := fixture.server.Client().Do(request)
		if err != nil {
			test.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			test.Fatal(err)
		}
		if response.StatusCode < 400 || bytes.Contains(data, []byte("IMPORT_PRIVATE_SENTINEL")) || strings.Contains(logs.String(), "IMPORT_PRIVATE_SENTINEL") {
			test.Fatalf("unsafe export error: status=%d", response.StatusCode)
		}
	}
	after, err := os.ReadFile("../importer/testdata/magpie/library.json")
	if err != nil || !bytes.Equal(data, after) {
		test.Fatal("source fixture changed")
	}
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.server.URL+libraryv1connect.LibraryServiceListRepositoriesProcedure, strings.NewReader(`{}`+strings.Repeat(" ", 4096)))
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+fixture.sessions[0])
	request.Header.Set("Content-Type", "application/json")
	ordinary, err := fixture.server.Client().Do(request)
	if err != nil {
		test.Fatal(err)
	}
	defer ordinary.Body.Close()
	if ordinary.StatusCode != http.StatusTooManyRequests {
		test.Fatalf("ordinary API limit changed: %d", ordinary.StatusCode)
	}
}

func TestImportPreviewRPCIdentityProtocols(test *testing.T) {
	fixture := newImportFixture(test)
	server := httptest.NewUnstartedServer(fixture.server.Config.Handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	test.Cleanup(server.Close)
	for _, protocol := range []struct {
		name   string
		option connect.ClientOption
	}{
		{"Connect JSON", connect.WithProtoJSON()}, {"gRPC", connect.WithGRPC()}, {"gRPC-Web", connect.WithGRPCWeb()},
	} {
		test.Run(protocol.name, func(test *testing.T) {
			client := importv1connect.NewImportServiceClient(server.Client(), server.URL, protocol.option)
			if _, err := client.PreviewLibraryExport(test.Context(), importRequest([]byte(`{"skills":[]}`), fixture.sessions[0])); err != nil {
				test.Fatalf("identity request: %v", err)
			}
			compressed := importv1connect.NewImportServiceClient(server.Client(), server.URL, protocol.option, connect.WithSendGzip(), connect.WithCompressMinBytes(0))
			if _, err := compressed.PreviewLibraryExport(test.Context(), importRequest([]byte(`{"skills":[]}`), fixture.sessions[0])); connect.CodeOf(err) != connect.CodeInvalidArgument {
				test.Fatalf("compressed request: %v", err)
			}
		})
	}
	for _, contentType := range []string{"application/grpc+proto", "application/grpc-web+proto"} {
		frame := make([]byte, 5)
		frame[0] = 1
		binary.BigEndian.PutUint32(frame[1:], uint32(len("IMPORT_PRIVATE_SENTINEL")))
		frame = append(frame, []byte("IMPORT_PRIVATE_SENTINEL")...)
		request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, server.URL+importv1connect.ImportServicePreviewLibraryExportProcedure, bytes.NewReader(frame))
		if err != nil {
			test.Fatal(err)
		}
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("Authorization", "Bearer "+fixture.sessions[0])
		response, err := server.Client().Do(request)
		if err != nil {
			test.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			test.Fatal(err)
		}
		status := response.Header.Get("Grpc-Status")
		if status == "" {
			status = response.Trailer.Get("Grpc-Status")
		}
		if status == "0" || status == "" && !bytes.Contains(data, []byte("grpc-status:")) || bytes.Contains(data, []byte("IMPORT_PRIVATE_SENTINEL")) {
			test.Fatalf("compressed flag accepted: header=%v trailer=%v body=%s", response.Header, response.Trailer, data)
		}
	}
}

func importRequest(data []byte, session string) *connect.Request[importv1.PreviewLibraryExportRequest] {
	request := connect.NewRequest(&importv1.PreviewLibraryExportRequest{Format: importv1.ExportFormat_EXPORT_FORMAT_MAGPIE_LIBRARY_JSON, ExportData: data})
	request.Header().Set("Authorization", "Bearer "+session)
	return request
}

func TestImportPreviewRPCNoCollection(test *testing.T) {
	fixture := newImportFixture(test)
	data, err := os.ReadFile("../importer/testdata/magpie/library.json")
	if err != nil {
		test.Fatal(err)
	}
	var previous *importv1.PreviewLibraryExportResponse
	for range 2 {
		response, err := fixture.client.PreviewLibraryExport(test.Context(), importRequest(data, fixture.sessions[0]))
		if err != nil {
			test.Fatalf("authorized preview: %v", err)
		}
		if len(response.Msg.Repositories) != 3 || response.Msg.ReferencedSkillCount != 7 {
			test.Fatalf("unexpected preview: %v", response.Msg)
		}
		if previous != nil && !proto.Equal(previous, response.Msg) {
			test.Fatal("preview replay changed results")
		}
		previous = response.Msg
	}
	entries, err := fixture.collection.List(test.Context(), fixture.owners[0])
	if err != nil || len(entries) != 0 || fixture.fetches.Load() != 0 {
		test.Fatalf("preview performed collection: entries=%d fetches=%d err=%v", len(entries), fixture.fetches.Load(), err)
	}
}

func (fixture *importFixture) collect(ctx context.Context, owner int, input, branch string) (*connect.Response[libraryv1.CollectRepositoryResponse], error) {
	client := libraryv1connect.NewLibraryServiceClient(fixture.server.Client(), fixture.server.URL)
	request := connect.NewRequest(&libraryv1.CollectRepositoryRequest{Repository: input, Branch: branch})
	request.Header().Set("Authorization", "Bearer "+fixture.sessions[owner])
	return client.CollectRepository(ctx, request)
}

func (fixture *importFixture) list(test *testing.T, owner int) []*libraryv1.Repository {
	test.Helper()
	client := libraryv1connect.NewLibraryServiceClient(fixture.server.Client(), fixture.server.URL)
	request := connect.NewRequest(&libraryv1.ListRepositoriesRequest{})
	request.Header().Set("Authorization", "Bearer "+fixture.sessions[owner])
	response, err := client.ListRepositories(test.Context(), request)
	if err != nil {
		test.Fatal(err)
	}
	return response.Msg.Repositories
}

func (fixture *importFixture) get(ctx context.Context, owner int, id string) (*connect.Response[libraryv1.GetRepositoryResponse], error) {
	client := libraryv1connect.NewLibraryServiceClient(fixture.server.Client(), fixture.server.URL)
	request := connect.NewRequest(&libraryv1.GetRepositoryRequest{RepositoryId: id})
	request.Header().Set("Authorization", "Bearer "+fixture.sessions[owner])
	return client.GetRepository(ctx, request)
}

func importArchiveResponse(data []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}
}

func TestImportFlowSelectedRepositoriesAndRetry(test *testing.T) {
	fixture := newImportFixture(test)
	good := libraryArchive(test, "---\nname: Selected\ndescription: Selected skill\n---\nInstructions")
	var empty bytes.Buffer
	compressed := gzip.NewWriter(&empty)
	archive := tar.NewWriter(compressed)
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	var unselected atomic.Int64
	fixture.fetch = func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "/unselected/"):
			unselected.Add(1)
			return importArchiveResponse(good), nil
		case strings.Contains(request.URL.Path, "/empty/"):
			return importArchiveResponse(empty.Bytes()), nil
		default:
			return importArchiveResponse(good), nil
		}
	}
	data := []byte(`{"skills":[{"name":"good","source":{"kind":"github","repo":"org/good"}},{"name":"empty","source":{"kind":"github","repo":"org/empty"}},{"name":"unselected","source":{"kind":"github","repo":"org/unselected"}}]}`)
	preview, err := fixture.client.PreviewLibraryExport(test.Context(), importRequest(data, fixture.sessions[0]))
	if err != nil {
		test.Fatal(err)
	}
	if len(preview.Msg.Repositories) != 3 || fixture.fetches.Load() != 0 {
		test.Fatal("preview fetched archives")
	}
	var collected *libraryv1.Repository
	for index, candidate := range preview.Msg.Repositories[:2] {
		response, err := fixture.collect(test.Context(), 0, candidate.Repository, candidate.Branch)
		if index == 1 {
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatalf("empty repository: %v", err)
			}
		} else {
			if err != nil {
				test.Fatal(err)
			}
			collected = response.Msg.Repository
		}
	}
	listed := fixture.list(test, 0)
	if len(listed) != 1 || listed[0].Id != collected.Id || unselected.Load() != 0 || fixture.fetches.Load() != 2 {
		test.Fatal("partial selection changed unselected repositories")
	}
	detail, err := fixture.get(test.Context(), 0, collected.Id)
	if err != nil || len(detail.Msg.Skills) != 1 || len(detail.Msg.Skills[0].ContentHash) != 64 {
		test.Fatalf("selected skill unavailable: %v", err)
	}
	retried, err := fixture.collect(test.Context(), 0, "ORG/GOOD", "HEAD")
	if err != nil || !proto.Equal(retried.Msg.Repository, collected) {
		test.Fatalf("retry changed snapshot: %v", err)
	}
	after, err := fixture.get(test.Context(), 0, collected.Id)
	if err != nil || !proto.Equal(after.Msg, detail.Msg) || len(fixture.list(test, 0)) != 1 || fixture.fetches.Load() != 2 {
		test.Fatalf("retry changed skill hash/count or fetched archive: %v", err)
	}
}

func TestImportFlowOwnerConflictAndUnknownResult(test *testing.T) {
	test.Run("concurrent natural key and owner isolation", func(test *testing.T) {
		fixture := newImportFixture(test)
		archive := libraryArchive(test, "---\nname: Concurrent\ndescription: Skill\n---\nInstructions")
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		fixture.fetch = func(request *http.Request) (*http.Response, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return importArchiveResponse(archive), nil
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		}
		type result struct {
			response *connect.Response[libraryv1.CollectRepositoryResponse]
			err      error
		}
		results := make(chan result, 2)
		var workers sync.WaitGroup
		for _, input := range []string{"ORG/RACE", "org/race"} {
			workers.Go(func() {
				response, err := fixture.collect(test.Context(), 0, input, "HEAD")
				results <- result{response, err}
			})
		}
		importAwait(test, entered)
		importAwait(test, entered)
		close(release)
		workers.Wait()
		first, second := <-results, <-results
		if first.err != nil || second.err != nil || !proto.Equal(first.response.Msg, second.response.Msg) || len(fixture.list(test, 0)) != 1 {
			test.Fatalf("concurrent collection: %v %v", first.err, second.err)
		}
		id := first.response.Msg.Repository.Id
		if len(fixture.list(test, 1)) != 0 {
			test.Fatal("cross-owner list visible")
		}
		if _, err := fixture.get(test.Context(), 1, id); connect.CodeOf(err) != connect.CodeNotFound {
			test.Fatalf("cross-owner detail: %v", err)
		}
		other, err := fixture.collect(test.Context(), 1, "org/race", "")
		if err != nil || other.Msg.Repository.Id == id || len(fixture.list(test, 1)) != 1 {
			test.Fatalf("owner natural key not isolated: %v", err)
		}
		detail, err := fixture.get(test.Context(), 0, id)
		if err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.cachePath, detail.Msg.Repository.ArchiveHash+".tar.gz"), []byte("corrupt"), 0o600); err != nil {
			test.Fatal(err)
		}
		if _, err := fixture.collect(test.Context(), 0, "org/race", ""); connect.CodeOf(err) != connect.CodeDataLoss {
			test.Fatalf("corrupt archive accepted: %v", err)
		}
		if _, err := fixture.get(test.Context(), 0, id); connect.CodeOf(err) != connect.CodeDataLoss {
			test.Fatalf("corrupt detail accepted: %v", err)
		}
	})
	test.Run("canceled before commit", func(test *testing.T) {
		fixture := newImportFixture(test)
		entered, exited := make(chan struct{}), make(chan struct{})
		var first atomic.Bool
		archive := libraryArchive(test, "---\nname: Retry\ndescription: Skill\n---\nInstructions")
		fixture.fetch = func(request *http.Request) (*http.Response, error) {
			if !first.Swap(true) {
				close(entered)
				<-request.Context().Done()
				close(exited)
				return nil, request.Context().Err()
			}
			return importArchiveResponse(archive), nil
		}
		ctx, cancel := context.WithCancel(test.Context())
		defer cancel()
		finished := make(chan error, 1)
		go func() { _, err := fixture.collect(ctx, 0, "org/canceled", ""); finished <- err }()
		importAwait(test, entered)
		cancel()
		if err := <-finished; connect.CodeOf(err) != connect.CodeCanceled {
			test.Fatalf("cancel before commit: %v", err)
		}
		importAwait(test, exited)
		if len(fixture.list(test, 0)) != 0 {
			test.Fatal("canceled fetch committed")
		}
		if _, err := fixture.collect(test.Context(), 0, "org/canceled", ""); err != nil {
			test.Fatal(err)
		}
		if len(fixture.list(test, 0)) != 1 {
			test.Fatal("retry did not converge")
		}
	})
	for _, mode := range []string{"lost response", "canceled after commit"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newImportFixture(test)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			transport := fixture.server.Client().Transport
			client := libraryv1connect.NewLibraryServiceClient(&http.Client{Transport: graphTransport(func(request *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(request)
				if err != nil {
					return nil, err
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if mode == "canceled after commit" {
					cancel()
					return nil, context.Canceled
				}
				return nil, io.ErrUnexpectedEOF
			})}, fixture.server.URL)
			request := connect.NewRequest(&libraryv1.CollectRepositoryRequest{Repository: "org/uncertain"})
			request.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
			if _, err := client.CollectRepository(ctx, request); err == nil {
				test.Fatal("lost response reported success")
			}
			listed := fixture.list(test, 0)
			if len(listed) != 1 {
				test.Fatal("response loss rolled back committed repository")
			}
			detail, err := fixture.get(test.Context(), 0, listed[0].Id)
			if err != nil {
				test.Fatal(err)
			}
			retry, err := fixture.collect(test.Context(), 0, "ORG/UNCERTAIN", "HEAD")
			if err != nil || retry.Msg.Repository.Id != listed[0].Id {
				test.Fatalf("uncertain retry: %v", err)
			}
			after, err := fixture.get(test.Context(), 0, listed[0].Id)
			if err != nil || !proto.Equal(detail.Msg, after.Msg) || len(fixture.list(test, 0)) != 1 || fixture.fetches.Load() != 1 {
				test.Fatalf("retry changed committed snapshot: %v", err)
			}
		})
	}
}
