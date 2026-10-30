package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
)

func newSelectionAdmissionFixture(test *testing.T) (*ImportHandler, *importer.SelectionService, []string) {
	test.Helper()
	authentication, store := testServiceAndStore(test)
	cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "objects"))
	if err != nil {
		test.Fatal("archive cache unavailable")
	}
	collection, err := library.NewService(store, cache, repository.NewArchiveFetcher(&http.Client{Transport: graphTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("guard must not fetch") })}))
	if err != nil {
		test.Fatal("library unavailable")
	}
	test.Cleanup(func() { _ = collection.Close() })
	selection, err := importer.NewSelectionService(store, collection, authentication, "https://cognate.example.test")
	if err != nil {
		test.Fatal("selection service unavailable")
	}
	handler, err := NewImportHandlerWithSelection(authentication, collection, selection)
	if err != nil {
		test.Fatal("selection handler unavailable")
	}
	test.Cleanup(func() { _ = handler.Close() })
	var sessions []string
	for _, username := range []string{"alice", "bob", "charlie"} {
		password := []byte("Synthetic guard-only passphrase")
		if _, err := authentication.Register(test.Context(), username, password); err != nil {
			test.Fatal("fixture registration failed")
		}
		session, err := authentication.Login(test.Context(), username, password)
		clear(password)
		if err != nil {
			test.Fatal("fixture login failed")
		}
		sessions = append(sessions, session.Reveal())
	}
	return handler, selection, sessions
}

func TestImportSelectionGuardAdmissionBeforeBody(test *testing.T) {
	handler, selection, sessions := newSelectionAdmissionFixture(test)
	for _, path := range []string{importv1connect.ImportServicePreviewImportSelectionProcedure, importv1connect.ImportServiceCommitImportSelectionProcedure, importv1connect.ImportServiceGetImportCommitStatusProcedure} {
		body := &importUnreadBody{}
		request := httptest.NewRequest(http.MethodPost, path, body)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.Guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { test.Error("unauthorized request reached decoder") })).ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || body.reads != 0 {
			test.Fatalf("selection authentication boundary: status=%d reads=%d", response.Code, body.reads)
		}
	}
	first, err := selection.Begin(test.Context(), importer.SelectionPreviewMethod, sessions[0])
	if err != nil {
		test.Fatal("first heavy admission failed")
	}
	defer first.Release()
	second, err := selection.Begin(test.Context(), importer.SelectionCommitMethod, sessions[1])
	if err != nil {
		test.Fatal("second heavy admission failed")
	}
	defer second.Release()
	for _, session := range []string{sessions[0], sessions[2]} {
		body := &importUnreadBody{}
		request := httptest.NewRequest(http.MethodPost, importv1connect.ImportServiceCommitImportSelectionProcedure, body)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+session)
		response := httptest.NewRecorder()
		handler.Guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { test.Error("full heavy lane reached decoder") })).ServeHTTP(response, request)
		if response.Code != http.StatusTooManyRequests || body.reads != 0 {
			test.Fatal("heavy capacity did not reject before body")
		}
	}
	observer, err := selection.Begin(test.Context(), importer.SelectionStatusMethod, sessions[0])
	if err != nil {
		test.Fatal("observer must remain available during heavy work")
	}
	defer observer.Release()
	body := &importUnreadBody{}
	request := httptest.NewRequest(http.MethodPost, importv1connect.ImportServiceGetImportCommitStatusProcedure, body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+sessions[2])
	response := httptest.NewRecorder()
	handler.Guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { test.Error("full observer lane reached decoder") })).ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || body.reads != 0 {
		test.Fatal("observer capacity did not reject before body")
	}
}

func TestImportSelectionGuardRetainsLeaseThroughHandler(test *testing.T) {
	handler, selection, sessions := newSelectionAdmissionFixture(test)
	server := httptest.NewTLSServer(handler.Guard(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			test.Error("admitted body could not be read")
		}
		if call, err := selection.Begin(request.Context(), importer.SelectionPreviewMethod, sessions[0]); !errors.Is(err, importer.ErrSelectionBusy) {
			if call != nil {
				call.Release()
			}
			test.Error("heavy lease was released before handler returned")
		}
		observer, err := selection.Begin(request.Context(), importer.SelectionStatusMethod, sessions[0])
		if err != nil {
			test.Error("separate observer was blocked by heavy lease")
		} else {
			observer.Release()
		}
		writer.WriteHeader(http.StatusNoContent)
	})))
	test.Cleanup(server.Close)
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, server.URL+importv1connect.ImportServicePreviewImportSelectionProcedure, strings.NewReader("{}"))
	if err != nil {
		test.Fatal("request construction failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+sessions[0])
	response, err := server.Client().Do(request)
	if err != nil {
		test.Fatal("guard request failed")
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		test.Fatal("selected guard did not reach admitted handler")
	}
	call, err := selection.Begin(test.Context(), importer.SelectionPreviewMethod, sessions[0])
	if err != nil {
		test.Fatal("handler did not release completed request lease")
	}
	call.Release()
}
