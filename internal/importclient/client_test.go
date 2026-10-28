package importclient_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/skillhub/gen/skillhub/import/v1"
	"github.com/ca-x/skillhub/gen/skillhub/import/v1/importv1connect"
	libraryv1 "github.com/ca-x/skillhub/gen/skillhub/library/v1"
	"github.com/ca-x/skillhub/gen/skillhub/library/v1/libraryv1connect"
	"github.com/ca-x/skillhub/internal/importclient"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

var session = strings.Repeat("A", 43) + "." + strings.Repeat("B", 43)

type controlledRPC struct {
	importv1connect.UnimplementedImportServiceHandler
	libraryv1connect.UnimplementedLibraryServiceHandler
	preview func(context.Context, *connect.Request[importv1.PreviewLibraryExportRequest]) (*importv1.PreviewLibraryExportResponse, error)
	collect func(context.Context, *connect.Request[libraryv1.CollectRepositoryRequest]) (*libraryv1.CollectRepositoryResponse, error)
	calls   atomic.Int64
}

func (fixture *controlledRPC) PreviewLibraryExport(ctx context.Context, request *connect.Request[importv1.PreviewLibraryExportRequest]) (*connect.Response[importv1.PreviewLibraryExportResponse], error) {
	if fixture.preview != nil {
		response, err := fixture.preview(ctx, request)
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(response), nil
	}
	return connect.NewResponse(validPreview()), nil
}

func (fixture *controlledRPC) CollectRepository(ctx context.Context, request *connect.Request[libraryv1.CollectRepositoryRequest]) (*connect.Response[libraryv1.CollectRepositoryResponse], error) {
	fixture.calls.Add(1)
	if fixture.collect != nil {
		response, err := fixture.collect(ctx, request)
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(response), nil
	}
	return connect.NewResponse(&libraryv1.CollectRepositoryResponse{Repository: &libraryv1.Repository{NameWithOwner: "example/toolkit", ArchiveHash: strings.Repeat("a", 64), SkillCount: 1}}), nil
}

func validPreview() *importv1.PreviewLibraryExportResponse {
	return &importv1.PreviewLibraryExportResponse{ReferencedSkillCount: 1, Repositories: []*importv1.RepositoryCandidate{{Repository: "example/toolkit"}}}
}

func exportFile(test *testing.T, data []byte) string {
	test.Helper()
	path := filepath.Join(test.TempDir(), "export.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		test.Fatal(err)
	}
	return path
}

func newClient(test *testing.T, fixture *controlledRPC) (*importclient.Client, string, string) {
	test.Helper()
	mux := http.NewServeMux()
	mux.Handle(importv1connect.NewImportServiceHandler(fixture))
	mux.Handle(libraryv1connect.NewLibraryServiceHandler(fixture))
	server := httptest.NewServer(mux)
	test.Cleanup(server.Close)
	client, err := importclient.New(server.URL, session, nil)
	if err != nil {
		test.Fatal(err)
	}
	return client, server.URL, exportFile(test, []byte(`{"skills":[]}`))
}

func TestOriginsAndSessionBoundaries(test *testing.T) {
	for _, origin := range []string{"", "http://github.com", "https://user:secret@example.org", "https://example.org/path", "https://example.org?secret=1", "https://example.org#secret", "https://example.org\\evil", "https://example.org:0", "https://example.org:65536", "https://example.org:", "https://example..org", "https://-example.org", "http://127.0.0.1.evil", "http://[fe80::1%25lo]", "https://example.org\n"} {
		if _, err := importclient.New(origin, session, nil); err == nil || strings.Contains(err.Error(), origin) && origin != "" {
			test.Fatalf("origin accepted or leaked: %q", origin)
		}
	}
	for _, origin := range []string{"https://example.org", "https://example.org/", "http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		if _, err := importclient.New(origin, session, nil); err != nil {
			test.Fatalf("valid origin: %q %v", origin, err)
		}
	}
	for _, token := range []string{"", "shp1.private", session + "\n", strings.Replace(session, "A", "\r", 1)} {
		if _, err := importclient.New("https://example.org", token, nil); err == nil {
			test.Fatal("invalid session accepted")
		}
	}
	if token, err := importclient.ReadSession(strings.NewReader("\n" + session + "\n")); err != nil || token != session {
		test.Fatal("bounded stdin rejected")
	}
}

func TestTLSClientAndCredentialRedaction(test *testing.T) {
	var seen atomic.Int64
	fixture := &controlledRPC{preview: func(_ context.Context, request *connect.Request[importv1.PreviewLibraryExportRequest]) (*importv1.PreviewLibraryExportResponse, error) {
		if request.Header().Get("Authorization") != "Bearer "+session {
			return nil, errors.New("missing authentication")
		}
		seen.Add(1)
		return validPreview(), nil
	}}
	path, handler := importv1connect.NewImportServiceHandler(fixture)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	client, err := importclient.New(server.URL, session, server.Client())
	if err != nil {
		test.Fatal(err)
	}
	if _, err := client.PreviewFile(test.Context(), exportFile(test, []byte(`{"skills":[]}`))); err != nil || seen.Load() != 1 {
		test.Fatalf("TLS failed: %v", err)
	}
	var logs bytes.Buffer
	slog.New(slog.NewTextHandler(&logs, nil)).Info("client", "value", client)
	formatted := fmt.Sprintf("%v %+v %#v %q", client, client, client, client)
	if strings.Contains(formatted+logs.String(), session) {
		test.Fatal("client formatting leaked credential")
	}
}

func TestRedirectsNeverForwardCredentials(test *testing.T) {
	var forwarded atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		forwarded.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer destination.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, destination.URL, status)
		}))
		client, err := importclient.New(server.URL, session, nil)
		if err != nil {
			test.Fatal(err)
		}
		_, err = client.PreviewFile(test.Context(), exportFile(test, []byte(`{"skills":[]}`)))
		server.Close()
		if err == nil || forwarded.Load() != 0 || strings.Contains(err.Error(), session) {
			test.Fatalf("redirect status %d", status)
		}
	}
}

func TestPreviewValidationHasZeroCollects(test *testing.T) {
	for _, kind := range []string{"count", "too-many", "duplicate", "url", "controls", "bidi", "branch", "uppercase", "head", "traversal"} {
		test.Run(kind, func(test *testing.T) {
			fixture := &controlledRPC{preview: func(context.Context, *connect.Request[importv1.PreviewLibraryExportRequest]) (*importv1.PreviewLibraryExportResponse, error) {
				response := validPreview()
				switch kind {
				case "count":
					response.ReferencedSkillCount = 0
				case "too-many":
					response.ReferencedSkillCount = 1001
				case "duplicate":
					response.ReferencedSkillCount = 2
					response.Repositories = append(response.Repositories, proto.Clone(response.Repositories[0]).(*importv1.RepositoryCandidate))
				case "url":
					response.Repositories[0].Repository = "https://secret@github.com/example/toolkit"
				case "controls":
					response.Repositories[0].Branch = "main\x1b[2J"
				case "bidi":
					response.Repositories[0].Branch = "main\u202e"
				case "branch":
					response.Repositories[0].Branch = "../secret"
				case "uppercase":
					response.Repositories[0].Repository = "Example/Toolkit"
				case "head":
					response.Repositories[0].Branch = "HEAD"
				case "traversal":
					response.Repositories[0].Repository = "../toolkit"
				}
				return response, nil
			}}
			client, _, path := newClient(test, fixture)
			if _, err := client.PreviewFile(test.Context(), path); err == nil || fixture.calls.Load() != 0 {
				test.Fatal("malicious preview accepted")
			}
		})
	}
}

func TestPreviewBindingAndImmutableSelection(test *testing.T) {
	fixture := &controlledRPC{}
	client, server, path := newClient(test, fixture)
	preview, err := client.PreviewFile(test.Context(), path)
	if err != nil {
		test.Fatal(err)
	}
	other, err := importclient.New(server, strings.Repeat("C", 43)+"."+strings.Repeat("D", 43), nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := other.ApplyFile(test.Context(), path, preview.SHA256(), preview, []int{1}); err == nil || fixture.calls.Load() != 0 {
		test.Fatal("different session reused preview")
	}
	candidates := preview.Candidates()
	candidates[0].Repository = "attacker/secret"
	if _, err := client.ApplyFile(test.Context(), path, preview.SHA256(), preview, []int{1}); err != nil {
		test.Fatalf("mutated public copy changed selection: %v", err)
	}
	if fixture.calls.Load() != 1 {
		test.Fatal("selection not sent")
	}
}

func TestChangedFileAndEmptySelectionHaveZeroCollects(test *testing.T) {
	fixture := &controlledRPC{}
	client, _, path := newClient(test, fixture)
	preview, err := client.PreviewFile(test.Context(), path)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := client.ApplyFile(test.Context(), path, preview.SHA256(), preview, nil); err == nil {
		test.Fatal("empty selection accepted")
	}
	if err := os.WriteFile(path, []byte(`{"skills":null}`), 0o600); err != nil {
		test.Fatal(err)
	}
	if _, err := client.ApplyFile(test.Context(), path, preview.SHA256(), preview, []int{1}); err == nil || fixture.calls.Load() != 0 {
		test.Fatal("changed export collected")
	}
}

func TestPreviewFileAtHashRejectsBeforeNetwork(test *testing.T) {
	var previews atomic.Int64
	fixture := &controlledRPC{preview: func(context.Context, *connect.Request[importv1.PreviewLibraryExportRequest]) (*importv1.PreviewLibraryExportResponse, error) {
		previews.Add(1)
		return validPreview(), nil
	}}
	client, _, path := newClient(test, fixture)
	for _, expected := range []string{"", "not-a-sha256", strings.Repeat("0", 64), strings.Repeat("G", 64)} {
		if _, err := client.PreviewFileAtHash(test.Context(), path, expected); err == nil {
			test.Fatal("unconfirmed hash accepted")
		}
	}
	if previews.Load() != 0 || fixture.calls.Load() != 0 {
		test.Fatal("unconfirmed hash sent to server")
	}
}

func TestPreviewFileAtHashUploadsPinnedBytesAndApplyRechecks(test *testing.T) {
	data := []byte(`{"skills":[]}`)
	wantHash := fmt.Sprintf("%x", sha256.Sum256(data))
	var previews atomic.Int64
	var path string
	fixture := &controlledRPC{preview: func(_ context.Context, request *connect.Request[importv1.PreviewLibraryExportRequest]) (*importv1.PreviewLibraryExportResponse, error) {
		previews.Add(1)
		if !bytes.Equal(request.Msg.ExportData, data) {
			return nil, errors.New("upload did not match confirmed bytes")
		}
		if err := os.WriteFile(path, []byte(`{"skills":null}`), 0o600); err != nil {
			return nil, err
		}
		return validPreview(), nil
	}}
	client, _, authorizedPath := newClient(test, fixture)
	path = authorizedPath
	preview, err := client.PreviewFileAtHash(test.Context(), path, wantHash)
	if err != nil || preview.SHA256() != wantHash || previews.Load() != 1 {
		test.Fatalf("pinned preview failed: %v", err)
	}
	if _, err := client.ApplyFile(test.Context(), path, wantHash, preview, []int{1}); err == nil || fixture.calls.Load() != 0 {
		test.Fatal("file changed during preview was collected")
	}
}

func TestOwnerHintsUseOnlyTheCurrentSession(test *testing.T) {
	fixture := &controlledRPC{preview: func(_ context.Context, request *connect.Request[importv1.PreviewLibraryExportRequest]) (*importv1.PreviewLibraryExportResponse, error) {
		response := validPreview()
		if request.Header().Get("Authorization") == "Bearer "+session {
			response.Repositories[0].ExistingRepositoryId = "PRIVATE_OWNER_ID"
		}
		return response, nil
	}}
	client, server, path := newClient(test, fixture)
	other, err := importclient.New(server, strings.Repeat("C", 43)+"."+strings.Repeat("D", 43), nil)
	if err != nil {
		test.Fatal(err)
	}
	for index, current := range []*importclient.Client{client, other} {
		preview, err := current.PreviewFile(test.Context(), path)
		if err != nil {
			test.Fatal(err)
		}
		if preview.Candidates()[0].Existing != (index == 0) {
			test.Fatal("owner-scoped hint mixed sessions")
		}
		if strings.Contains(fmt.Sprintf("%+v", preview.Candidates()), "PRIVATE_OWNER_ID") {
			test.Fatal("owner ID escaped safe candidates")
		}
	}
}

func TestInvalidCollectionResponseStopsWithUnknownOutcome(test *testing.T) {
	fixture := &controlledRPC{
		preview: func(context.Context, *connect.Request[importv1.PreviewLibraryExportRequest]) (*importv1.PreviewLibraryExportResponse, error) {
			return &importv1.PreviewLibraryExportResponse{ReferencedSkillCount: 2, Repositories: []*importv1.RepositoryCandidate{{Repository: "example/toolkit"}, {Repository: "example/second"}}}, nil
		},
		collect: func(context.Context, *connect.Request[libraryv1.CollectRepositoryRequest]) (*libraryv1.CollectRepositoryResponse, error) {
			return &libraryv1.CollectRepositoryResponse{Repository: &libraryv1.Repository{NameWithOwner: "PRIVATE_SECRET", ArchiveHash: strings.Repeat("a", 64), SkillCount: 1}}, nil
		},
	}
	client, _, path := newClient(test, fixture)
	preview, err := client.PreviewFile(test.Context(), path)
	if err != nil {
		test.Fatal(err)
	}
	result, err := client.ApplyFile(test.Context(), path, preview.SHA256(), preview, []int{1, 2})
	if !errors.Is(err, importclient.ErrIncomplete) || fixture.calls.Load() != 1 || result.Outcomes[0].Status != importclient.Unknown || result.Outcomes[1].Status != importclient.Unattempted {
		test.Fatalf("unsafe invalid-response result: %+v %v", result, err)
	}
	if strings.Contains(err.Error(), "PRIVATE_SECRET") {
		test.Fatal("invalid response exposed")
	}
}

func TestLostCollectionResponseDoesNotRetry(test *testing.T) {
	var calls atomic.Int64
	fixture := &controlledRPC{preview: func(context.Context, *connect.Request[importv1.PreviewLibraryExportRequest]) (*importv1.PreviewLibraryExportResponse, error) {
		return &importv1.PreviewLibraryExportResponse{ReferencedSkillCount: 2, Repositories: []*importv1.RepositoryCandidate{{Repository: "example/toolkit"}, {Repository: "example/second"}}}, nil
	}}
	previewPath, handler := importv1connect.NewImportServiceHandler(fixture)
	mux := http.NewServeMux()
	mux.Handle(previewPath, handler)
	mux.HandleFunc(libraryv1connect.LibraryServiceCollectRepositoryProcedure, func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		calls.Add(1)
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			test.Error(err)
			return
		}
		_ = connection.Close()
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := importclient.New(server.URL, session, nil)
	if err != nil {
		test.Fatal(err)
	}
	path := exportFile(test, []byte(`{"skills":[]}`))
	preview, err := client.PreviewFile(test.Context(), path)
	if err != nil {
		test.Fatal(err)
	}
	result, err := client.ApplyFile(test.Context(), path, preview.SHA256(), preview, []int{1, 2})
	if !errors.Is(err, importclient.ErrIncomplete) || calls.Load() != 1 || result.Outcomes[0].Status != importclient.Unknown || result.Outcomes[1].Status != importclient.Unattempted {
		test.Fatalf("lost-response result: %+v %v", result, err)
	}
}

func TestBoundedWireAndDecodedResponses(test *testing.T) {
	for _, kind := range []string{"wire", "protobuf"} {
		test.Run(kind, func(test *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/proto")
				data, err := proto.Marshal(validPreview())
				if err != nil {
					test.Error(err)
					return
				}
				if kind == "protobuf" {
					data = protowire.AppendTag(data, 100, protowire.BytesType)
					data = protowire.AppendBytes(data, bytes.Repeat([]byte("x"), (1<<20)+1))
				} else {
					data = bytes.Repeat([]byte("x"), 2<<20)
				}
				_, _ = writer.Write(data)
			}))
			defer server.Close()
			client, err := importclient.New(server.URL, session, nil)
			if err != nil {
				test.Fatal(err)
			}
			if _, err := client.PreviewFile(test.Context(), exportFile(test, []byte(`{"skills":[]}`))); err == nil {
				test.Fatal("unbounded response accepted")
			}
		})
	}
}

func TestOneInFlightAndCancellationBeforeDispatch(test *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	fixture := &controlledRPC{collect: func(ctx context.Context, _ *connect.Request[libraryv1.CollectRepositoryRequest]) (*libraryv1.CollectRepositoryResponse, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, connect.NewError(connect.CodeNotFound, errors.New("PRIVATE_SECRET"))
	}}
	client, _, path := newClient(test, fixture)
	preview, err := client.PreviewFile(test.Context(), path)
	if err != nil {
		test.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := client.ApplyFile(test.Context(), path, preview.SHA256(), preview, []int{1})
		finished <- err
	}()
	<-started
	if _, err := client.ApplyFile(test.Context(), path, preview.SHA256(), preview, []int{1}); connect.CodeOf(err) != connect.CodeResourceExhausted {
		test.Errorf("concurrent dispatch: %v", err)
	}
	close(release)
	if err := <-finished; !errors.Is(err, importclient.ErrIncomplete) || fixture.calls.Load() != 1 {
		test.Fatal("concurrent call escaped gate")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := client.ApplyFile(ctx, path, preview.SHA256(), preview, []int{1}); !errors.Is(err, context.Canceled) || fixture.calls.Load() != 1 {
		test.Fatal("pre-canceled call sent")
	}
}

func TestCallerTimeoutAndSafeServerErrors(test *testing.T) {
	fixture := &controlledRPC{preview: func(ctx context.Context, _ *connect.Request[importv1.PreviewLibraryExportRequest]) (*importv1.PreviewLibraryExportResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	path, handler := importv1connect.NewImportServiceHandler(fixture)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := importclient.New(server.URL, session, &http.Client{Timeout: 50 * time.Millisecond})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := client.PreviewFile(test.Context(), exportFile(test, []byte(`{"skills":[]}`))); err == nil {
		test.Fatal("timeout ignored")
	}
}

func TestExactExportByteBound(test *testing.T) {
	data := append([]byte(`{"skills":[]}`), bytes.Repeat([]byte(" "), importclient.MaxExportBytes-len(`{"skills":[]}`))...)
	wantHash := fmt.Sprintf("%x", sha256.Sum256(data))
	fixture := &controlledRPC{preview: func(_ context.Context, request *connect.Request[importv1.PreviewLibraryExportRequest]) (*importv1.PreviewLibraryExportResponse, error) {
		if !bytes.Equal(request.Msg.ExportData, data) {
			return nil, errors.New("upload changed")
		}
		return &importv1.PreviewLibraryExportResponse{}, nil
	}}
	client, _, _ := newClient(test, fixture)
	preview, err := client.PreviewFile(test.Context(), exportFile(test, data))
	if err != nil || preview.SHA256() != wantHash {
		test.Fatalf("exact bound: %v", err)
	}
}

func TestReadSessionIsBoundedAndDoesNotExposeReadErrors(test *testing.T) {
	reader := &countedReader{}
	if _, err := importclient.ReadSession(reader); err == nil || reader.read != 2049 {
		test.Fatalf("unbounded stdin: %d", reader.read)
	}
	if _, err := importclient.ReadSession(failingReader{}); err == nil || strings.Contains(err.Error(), "PRIVATE_SECRET") {
		test.Fatal("stdin error exposed")
	}
}

type countedReader struct{ read int }

func (reader *countedReader) Read(data []byte) (int, error) {
	clear(data)
	reader.read += len(data)
	return len(data), nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("PRIVATE_SECRET") }

var _ io.Reader = (*countedReader)(nil)
