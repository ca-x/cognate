package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/gen/skillhub/catalog/v1/catalogv1connect"
	libraryv1 "github.com/ca-x/cognate/gen/skillhub/library/v1"
	"github.com/ca-x/cognate/gen/skillhub/library/v1/libraryv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	"github.com/ca-x/cognate/web"
)

type repositoriesUIFixture struct {
	public, control                                          *httptest.Server
	password, capability, foreignCategory, foreignRepository string
	cancel                                                   context.CancelFunc
	done                                                     context.Context
	mutex                                                    sync.Mutex
	mode, procedure                                          string
	requests                                                 map[string]int
	archivePath                                              string
}

func repositoriesUIArchive(test *testing.T) []byte {
	test.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range []struct{ path, name, description string }{
		{"skills/review", "Evidence based engineering review", "Review requirements, preserve evidence and verify keyboard accessibility before publishing any repository contents. 中文与 English 都应保持可读。"},
		{"skills/planning", "Implementation planning", "Plan a small, verifiable change. Keep ownership and publication boundaries explicit."},
	} {
		contents := "---\nname: " + entry.name + "\ndescription: " + entry.description + "\n---\nSynthetic fixture only.\n"
		if err := archive.WriteHeader(&tar.Header{Name: "repo-main/" + entry.path + "/SKILL.md", Mode: 0o644, Size: int64(len(contents))}); err != nil {
			test.Fatal(err)
		}
		if _, err := io.WriteString(archive, contents); err != nil {
			test.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		test.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		test.Fatal(err)
	}
	return buffer.Bytes()
}

func newRepositoriesUIFixture(test *testing.T) *repositoriesUIFixture {
	test.Helper()
	authentication, store := testServiceAndStore(test)
	ctx, cancel := context.WithCancel(test.Context())
	fixture := &repositoriesUIFixture{password: rand.Text(), capability: rand.Text(), cancel: cancel, done: ctx, requests: make(map[string]int)}
	test.Cleanup(cancel)
	for _, name := range []string{"task2-owner", "task2-foreign"} {
		if _, err := authentication.Register(ctx, name, []byte(fixture.password)); err != nil {
			test.Fatal(err)
		}
	}
	foreign, err := authentication.Login(ctx, "task2-foreign", []byte(fixture.password))
	if err != nil {
		test.Fatal(err)
	}
	archive := repositoriesUIArchive(test)
	upstream := &http.Client{Transport: graphTransport(func(request *http.Request) (*http.Response, error) {
		status, data := http.StatusOK, archive
		fixture.mutex.Lock()
		unavailable := fixture.mode == "error" && fixture.procedure == "upstream"
		fixture.mutex.Unlock()
		if unavailable {
			status, data = http.StatusServiceUnavailable, nil
		}
		if strings.Contains(request.URL.Path, "/missing/") {
			status, data = http.StatusNotFound, nil
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	archiveDirectory := filepath.Join(test.TempDir(), "archives")
	cache, err := library.OpenArchiveCache(archiveDirectory)
	if err != nil {
		test.Fatal(err)
	}
	collection, err := library.NewService(store, cache, repository.NewArchiveFetcher(upstream))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = collection.Close() })
	fixture.public = httptest.NewUnstartedServer(nil)
	origin := "https://" + fixture.public.Listener.Addr().String()
	publicDomain, err := publiccatalog.NewService(store, collection)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = publicDomain.Close() })
	handler := NewWithOptions(authentication, Options{Store: store, Library: collection, PublicCatalog: publicDomain, PublicOrigin: origin, WebUI: web.Handler()})
	fixture.public.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mutex.Lock()
		fixture.requests[request.URL.Path]++
		mode, procedure := fixture.mode, fixture.procedure
		fixture.mutex.Unlock()
		if procedure != "" && (procedure == "*" || strings.HasSuffix(request.URL.Path, "/"+procedure)) {
			if mode == "lost" {
				recorded := httptest.NewRecorder()
				handler.ServeHTTP(recorded, request)
				if recorded.Code != http.StatusOK {
					test.Error("lost-receipt fixture did not commit successfully")
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(writer, `{"code":"unavailable","message":"synthetic lost receipt after commit"}`)
				return
			}
			if mode == "slow" {
				select {
				case <-time.After(1500 * time.Millisecond):
				case <-request.Context().Done():
					return
				}
			}
			if mode == "error" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(writer, `{"code":"unavailable","message":"synthetic transport failure"}`)
				return
			}
		}
		handler.ServeHTTP(writer, request)
	})
	fixture.public.Config.ReadHeaderTimeout = 5 * time.Second
	fixture.public.StartTLS()
	test.Cleanup(fixture.public.Close)
	categoryClient := catalogv1connect.NewCategoryServiceClient(fixture.public.Client(), origin)
	foreignCategory, err := categoryClient.CreateCategory(ctx, mcpRequest(&catalogv1.CreateCategoryRequest{Name: "FOREIGN-ONLY"}, foreign.Reveal()))
	if err != nil {
		test.Fatal(err)
	}
	fixture.foreignCategory = foreignCategory.Msg.Category.Id
	libraryClient := libraryv1connect.NewLibraryServiceClient(fixture.public.Client(), origin)
	foreignRepository, err := libraryClient.CollectRepository(ctx, mcpRequest(&libraryv1.CollectRepositoryRequest{Repository: "fixture/foreign-only"}, foreign.Reveal()))
	if err != nil {
		test.Fatal(err)
	}
	fixture.foreignRepository = foreignRepository.Msg.Repository.Id
	fixture.archivePath = filepath.Join(archiveDirectory, foreignRepository.Msg.Repository.ArchiveHash+".tar.gz")
	fixture.control = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(fixture.capability)) != 1 || request.Header.Get("Origin") != "" || request.URL.RawQuery != "" {
			http.Error(writer, "control denied", http.StatusForbidden)
			return
		}
		fixture.mutex.Lock()
		defer fixture.mutex.Unlock()
		switch request.Method + " " + request.URL.Path {
		case "GET /state":
			_ = json.MarshalWrite(writer, fixture.requests)
		case "POST /mode":
			var input struct{ Mode, Procedure string }
			if err := json.UnmarshalRead(http.MaxBytesReader(writer, request.Body, 512), &input); err != nil || (input.Mode != "normal" && input.Mode != "error" && input.Mode != "slow" && input.Mode != "lost") {
				http.Error(writer, "invalid mode", 400)
				return
			}
			fixture.mode, fixture.procedure = input.Mode, input.Procedure
		case "POST /corrupt":
			if err := os.Remove(fixture.archivePath); err != nil {
				http.Error(writer, "fixture archive unavailable", 500)
			}
		case "POST /stop":
			cancel()
		default:
			http.NotFound(writer, request)
		}
	}))
	test.Cleanup(fixture.control.Close)
	return fixture
}

func TestRepositoriesUIBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_REPOSITORIES_UI_DIR")
	if directory == "" {
		test.Skip("isolated repositories browser fixture")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	fixture := newRepositoriesUIFixture(test)
	data, err := json.Marshal(map[string]any{"url": fixture.public.URL, "controlURL": fixture.control.URL, "password": fixture.password, "capability": fixture.capability, "foreignCategory": fixture.foreignCategory, "foreignRepository": fixture.foreignRepository})
	if err != nil {
		test.Fatal(err)
	}
	file, err := root.OpenFile("ready.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		test.Fatal(err)
	}
	if err := file.Close(); err != nil {
		test.Fatal(err)
	}
	<-fixture.done.Done()
}

func TestRepositoriesUIFixtureGuards(test *testing.T) {
	fixture := newRepositoriesUIFixture(test)
	client := libraryv1connect.NewLibraryServiceClient(fixture.public.Client(), fixture.public.URL)
	if _, err := client.ListRepositories(test.Context(), connect.NewRequest(&libraryv1.ListRepositoriesRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatalf("anonymous owner access: %v", err)
	}
	response, err := http.Get(fixture.control.URL + "/state")
	if err != nil {
		test.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		test.Fatal("unguarded fixture control")
	}
}
