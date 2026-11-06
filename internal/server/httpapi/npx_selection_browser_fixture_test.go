package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/web"
)

type npxSelectionRecorder struct {
	*httptest.ResponseRecorder
	transport http.ResponseWriter
}

func (recorder *npxSelectionRecorder) Unwrap() http.ResponseWriter {
	return recorder.transport
}

func TestNPXSelectionBrowserFixture(test *testing.T) {
	selectionBrowserFixture(test, os.Getenv("COGNATE_NPX_SELECTION_DIR"), false)
}

func TestOpenCodeSelectionBrowserFixture(test *testing.T) {
	selectionBrowserFixture(test, os.Getenv("COGNATE_OPENCODE_SELECTION_DIR"), true)
}

func selectionBrowserFixture(test *testing.T, directory string, opencode bool) {
	if directory == "" {
		test.Skip("isolated npx selection browser fixture")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	if opencode {
		data := importRoundtripSnapshot(test, `DELETE FROM providers; INSERT INTO providers VALUES('work','opencode','OpenCode imported','{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://opencode.example/v1","apiKey":"SYNTHETIC-PRIVATE-MARKER","headers":{"Authorization":"SYNTHETIC-PRIVATE-MARKER"}},"models":{"a":{"name":"Model A","limit":{"context":9007199254740991,"output":4096},"modalities":{"input":["text","image"],"output":["text"]}}}}','{"opencodeConfigFormat":"v1"}',NULL,NULL,0,0,NULL,NULL,NULL,0,0);`)
		file, err := root.OpenFile("source.db", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			test.Fatal(err)
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			test.Fatal("synthetic source write failed")
		}
	}
	authentication, store := testServiceAndStore(test)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	password, capability := rand.Text(), rand.Text()
	for _, name := range []string{"npx-owner", "npx-other"} {
		if _, err := authentication.Register(ctx, name, []byte(password)); err != nil {
			test.Fatal("register synthetic owner", err)
		}
	}
	archive := repositoriesUIArchive(test)
	var archiveRequests atomic.Int64
	fetcher := repository.NewArchiveFetcher(&http.Client{Transport: graphTransport(func(request *http.Request) (*http.Response, error) {
		archiveRequests.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})})
	cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "archives"))
	if err != nil {
		test.Fatal(err)
	}
	defer cache.Close()
	content, err := library.NewService(store, cache, fetcher)
	if err != nil {
		test.Fatal(err)
	}
	defer content.Close()
	public := httptest.NewUnstartedServer(nil)
	origin := "https://" + public.Listener.Addr().String()
	selection, err := importer.NewSelectionService(store, content, authentication, origin)
	if err != nil {
		test.Fatal(err)
	}
	imports, err := NewImportHandlerWithSelection(authentication, content, selection)
	if err != nil {
		selection.Close()
		test.Fatal(err)
	}
	defer imports.Close()
	handler := NewWithOptions(authentication, Options{Store: store, Library: content, Imports: imports, PublicOrigin: origin, WebUI: web.Handler()})
	var mutex sync.Mutex
	requests := make(map[string]int)
	lost := false
	public.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		requests[request.URL.Path]++
		drop := lost && request.URL.Path == "/skillhub.import.v1.ImportService/CommitImportSelection"
		mutex.Unlock()
		if drop {
			recorded := &npxSelectionRecorder{ResponseRecorder: httptest.NewRecorder(), transport: writer}
			handler.ServeHTTP(recorded, request)
			if recorded.Code != http.StatusOK {
				test.Errorf("lost-receipt request did not complete real commit: HTTP %d", recorded.Code)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(writer, `{"code":"unavailable","message":"synthetic lost receipt"}`)
			return
		}
		handler.ServeHTTP(writer, request)
	})
	public.Config.ReadHeaderTimeout = 5 * time.Second
	public.StartTLS()
	defer public.Close()
	control := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(capability)) != 1 || request.Header.Get("Origin") != "" || request.URL.RawQuery != "" {
			http.Error(writer, "control denied", http.StatusForbidden)
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		switch request.Method + " " + request.URL.Path {
		case "GET /state":
			requests["fixture.archive_requests"] = int(archiveRequests.Load())
			json.MarshalWrite(writer, requests)
		case "POST /lost":
			lost = true
		case "POST /normal":
			lost = false
		case "POST /stop":
			cancel()
		default:
			http.NotFound(writer, request)
		}
	}))
	defer control.Close()
	file, err := root.OpenFile("ready.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		test.Fatal(err)
	}
	err = json.MarshalWrite(file, map[string]string{"url": public.URL, "controlURL": control.URL, "password": password, "capability": capability})
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		test.Fatal("write private ready file", err, closeErr)
	}
	<-ctx.Done()
}
