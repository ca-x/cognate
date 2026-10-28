package desktop

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	libraryv1 "github.com/ca-x/cognate/gen/skillhub/library/v1"
	"github.com/ca-x/cognate/gen/skillhub/library/v1/libraryv1connect"
	"github.com/egoist/mygo/ui"
)

type importHTTPFixture struct {
	importv1connect.UnimplementedImportServiceHandler
	libraryv1connect.UnimplementedLibraryServiceHandler
	session  string
	collects atomic.Int32
}

func (fixture *importHTTPFixture) PreviewLibraryExport(_ context.Context, request *connect.Request[importv1.PreviewLibraryExportRequest]) (*connect.Response[importv1.PreviewLibraryExportResponse], error) {
	if request.Header().Get("Authorization") != "Bearer "+fixture.session {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid fixture session"))
	}
	return connect.NewResponse(&importv1.PreviewLibraryExportResponse{ReferencedSkillCount: 2, Repositories: []*importv1.RepositoryCandidate{{Repository: "fixture/tools"}, {Repository: "fixture/not-selected"}}}), nil
}

func (fixture *importHTTPFixture) CollectRepository(_ context.Context, request *connect.Request[libraryv1.CollectRepositoryRequest]) (*connect.Response[libraryv1.CollectRepositoryResponse], error) {
	if request.Header().Get("Authorization") != "Bearer "+fixture.session {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid fixture session"))
	}
	fixture.collects.Add(1)
	return connect.NewResponse(&libraryv1.CollectRepositoryResponse{Repository: &libraryv1.Repository{NameWithOwner: "fixture/tools", ArchiveHash: strings.Repeat("a", 64), SkillCount: 1}}), nil
}

func TestNativeMagpiePreviewExplicitSelectionAndApply(test *testing.T) {
	fixture := &importHTTPFixture{session: strings.Repeat("A", 43) + "." + strings.Repeat("B", 43)}
	mux := http.NewServeMux()
	mux.Handle(importv1connect.NewImportServiceHandler(fixture))
	mux.Handle(libraryv1connect.NewLibraryServiceHandler(fixture))
	server := httptest.NewServer(mux)
	defer server.Close()
	path := filepath.Join(test.TempDir(), "magpie.json")
	if err := os.WriteFile(path, []byte(`{"skills":[]}`), 0600); err != nil {
		test.Fatal(err)
	}
	model, pump := modelFixture(test, &Service{Home: test.TempDir(), HTTP: server.Client()})
	model.SetOrigin(server.URL)
	model.session = fixture.session
	model.FilePath = path
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "Magpie 导入")
	click(test, tester, "预览文件")
	pump()
	tester.Frame()
	if len(model.Candidates) != 2 {
		test.Fatalf("preview failed: %s", model.Status)
	}
	model.Apply()
	pump()
	if fixture.collects.Load() != 0 {
		test.Fatal("unselected repositories were applied")
	}
	click(test, tester, "fixture/tools @ （已存在 false）")
	click(test, tester, "应用明确选择的仓库")
	pump()
	tester.Frame()
	if fixture.collects.Load() != 1 || len(model.ImportResults) != 1 || !strings.Contains(model.ImportResults[0], "collected") {
		test.Fatalf("explicit import failed: %s / %v", model.Status, model.ImportResults)
	}
	click(test, tester, "预览文件")
	pump()
	tester.Frame()
	click(test, tester, "fixture/tools @ （已存在 false）")
	if err := os.WriteFile(path, []byte(`{"skills":[{}]}`), 0600); err != nil {
		test.Fatal(err)
	}
	click(test, tester, "应用明确选择的仓库")
	pump()
	if fixture.collects.Load() != 1 {
		test.Fatal("changed file was applied using stale preview")
	}
}

func TestHTTPWorkCancellationAndRedirectGuard(test *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		close(started)
		select {
		case <-request.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	model, pump := modelFixture(test, &Service{Home: test.TempDir(), HTTP: server.Client()})
	model.SetOrigin(server.URL)
	model.Search(false)
	<-started
	model.Cancel()
	pump()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		test.Fatal("HTTP work was not canceled")
	}
	if model.Busy || len(model.Skills) > 0 {
		test.Fatal("canceled request affected UI")
	}

	var foreignRequests atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignRequests.Add(1) }))
	defer foreign.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, foreign.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	service := &Service{HTTP: redirect.Client()}
	if _, err := service.Login(test.Context(), redirect.URL, "fixture", "secret"); err == nil || foreignRequests.Load() != 0 {
		test.Fatal("login credentials followed cross-origin redirect")
	}
}
