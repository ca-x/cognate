package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/v2"
	"fmt"
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
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	"github.com/ca-x/cognate/web"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type publicCatalogBrowserFixture struct {
	public, control *httptest.Server
	capability      string
	cancel          context.CancelFunc
	done            context.Context
	mutex           sync.Mutex
	mode            string
	requests        []map[string]any
}

type publicCatalogBrowserRecorder struct {
	*httptest.ResponseRecorder
	transport http.ResponseWriter
}

func (recorder *publicCatalogBrowserRecorder) Unwrap() http.ResponseWriter {
	return recorder.transport
}

func publicCatalogBrowserArchive(test *testing.T, private bool) []byte {
	test.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	names := []string{"Change review", "API contracts", "Release notes", "Test planning", "多语言文档审阅与可访问性检查", "Incident handoff", "Dependency review", "Query planning", "Migration checklist"}
	count := 27
	if private {
		count = 1
		names = []string{"PRIVATE-FIXTURE-CATALOG"}
	}
	for index := range count {
		name := names[index%len(names)]
		if index >= len(names) {
			name = fmt.Sprintf("%s %02d", name, index)
		}
		description := "Review changes against the stated requirements. Record evidence, identify risks, and keep the final decision with the maintainer."
		if index == 4 {
			description = "逐项审阅中文与 English 文档，检查术语、键盘入口和窄屏布局。保留原始证据，不把公开发布等同于安全审查。"
		}
		if index == 5 {
			description = ""
		}
		contents := []byte(fmt.Sprintf("---\nname: %s\ndescription: %s\n---\nSynthetic browser fixture only. Review before use.\n", name, description))
		if err := archive.WriteHeader(&tar.Header{Name: fmt.Sprintf("repo-main/skills/fixture-%02d/SKILL.md", index), Mode: 0o644, Size: int64(len(contents))}); err != nil {
			test.Fatal(err)
		}
		if _, err := archive.Write(contents); err != nil {
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

func newPublicCatalogBrowserFixture(test *testing.T) *publicCatalogBrowserFixture {
	test.Helper()
	authentication, store := testServiceAndStore(test)
	owner, err := authentication.Register(test.Context(), "public-catalog-fixture", []byte(rand.Text()))
	if err != nil {
		test.Fatal(err)
	}
	archives := map[bool][]byte{false: publicCatalogBrowserArchive(test, false), true: publicCatalogBrowserArchive(test, true)}
	upstream := &http.Client{Transport: graphTransport(func(request *http.Request) (*http.Response, error) {
		data := archives[strings.Contains(request.URL.Path, "private")]
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "archives"))
	if err != nil {
		test.Fatal(err)
	}
	collection, err := library.NewService(store, cache, repository.NewArchiveFetcher(upstream))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = collection.Close() })
	collected, err := collection.Collect(test.Context(), owner, "catalog-fixture/review-tools", "main")
	if err != nil {
		test.Fatal(err)
	}
	collected, err = collection.Get(test.Context(), owner, collected.ID)
	if err != nil || len(collected.Skills) != 27 {
		test.Fatalf("fixture skills unavailable: %v, count %d", err, len(collected.Skills))
	}
	assigned := make(map[string][]string)
	for index := range 66 {
		name := fmt.Sprintf("Fixture category %02d", index)
		if index == 0 {
			name = "Engineering"
		}
		category, createErr := store.CreateCategory(test.Context(), owner, name)
		if createErr != nil {
			test.Fatal(createErr)
		}
		skillID := collected.Skills[index%26].ID
		assigned[skillID] = append(assigned[skillID], category.ID)
		ids := assigned[skillID]
		if _, err := store.SetSkillCategories(test.Context(), owner, skillID, ids); err != nil {
			test.Fatal(err)
		}
	}
	if _, err := store.SetPublication(test.Context(), owner, collected.ID, 1, true, true); err != nil {
		test.Fatal(err)
	}
	private, err := collection.Collect(test.Context(), owner, "catalog-fixture/private", "main")
	if err != nil {
		test.Fatal(err)
	}
	private, err = collection.Get(test.Context(), owner, private.ID)
	if err != nil || len(private.Skills) != 1 {
		test.Fatalf("private fixture unavailable: %v", err)
	}
	privateCategory, err := store.CreateCategory(test.Context(), owner, "PRIVATE-FIXTURE-CATEGORY")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.SetSkillCategories(test.Context(), owner, private.Skills[0].ID, []string{privateCategory.ID}); err != nil {
		test.Fatal(err)
	}
	domain, err := publiccatalog.NewService(store, collection)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = domain.Close() })
	ctx, cancel := context.WithCancel(test.Context())
	fixture := &publicCatalogBrowserFixture{capability: rand.Text(), cancel: cancel, done: ctx, mode: "normal"}
	test.Cleanup(cancel)
	fixture.public = httptest.NewUnstartedServer(nil)
	origin := "https://" + fixture.public.Listener.Addr().String()
	handler := NewWithOptions(authentication, Options{Store: store, PublicOrigin: origin, PublicCatalog: domain, WebUI: web.Handler()})
	fixture.public.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/skillhub.catalog.v1.PublicCatalogService/") {
			handler.ServeHTTP(writer, request)
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(request.Body, 8193))
		if readErr != nil || len(data) > 8192 {
			http.Error(writer, "fixture request limit", http.StatusBadRequest)
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(data))
		var input, output proto.Message
		switch {
		case strings.HasSuffix(request.URL.Path, "/ListSkills"):
			input, output = &catalogv1.ListSkillsRequest{}, &catalogv1.ListSkillsResponse{}
		case strings.HasSuffix(request.URL.Path, "/ListCategories"):
			input, output = &catalogv1.PublicCatalogServiceListCategoriesRequest{}, &catalogv1.PublicCatalogServiceListCategoriesResponse{}
		case strings.HasSuffix(request.URL.Path, "/GetSkill"):
			input = &catalogv1.GetSkillRequest{}
		}
		decode, encode := proto.Unmarshal, proto.Marshal
		if strings.Contains(request.Header.Get("Content-Type"), "json") {
			decode, encode = protojson.Unmarshal, protojson.Marshal
		}
		var body map[string]any
		if input != nil && decode(data, input) == nil {
			encoded, _ := protojson.Marshal(input)
			_ = json.Unmarshal(encoded, &body)
		}
		fixture.mutex.Lock()
		mode := fixture.mode
		fixture.requests = append(fixture.requests, map[string]any{"path": request.URL.Path, "body": body, "authorization": request.Header.Get("Authorization") != ""})
		fixture.mutex.Unlock()
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
			_, _ = io.WriteString(writer, `{"code":"unavailable","message":"synthetic fixture failure"}`)
			return
		}
		if mode == "repeat" && output != nil && body["cursor"] != nil && body["cursor"] != "" {
			request.Header.Del("Accept-Encoding")
			recorded := &publicCatalogBrowserRecorder{ResponseRecorder: httptest.NewRecorder(), transport: writer}
			handler.ServeHTTP(recorded, request)
			responseData := recorded.Body.Bytes()
			if recorded.Code == 200 && decode(recorded.Body.Bytes(), output) == nil {
				switch response := output.(type) {
				case *catalogv1.ListSkillsResponse:
					response.NextCursor, _ = body["cursor"].(string)
				case *catalogv1.PublicCatalogServiceListCategoriesResponse:
					response.NextCursor, _ = body["cursor"].(string)
				}
				if encoded, err := encode(output); err == nil {
					responseData = encoded
				}
			}
			for name, values := range recorded.Header() {
				if name != "Content-Length" {
					writer.Header()[name] = values
				}
			}
			writer.WriteHeader(recorded.Code)
			_, _ = writer.Write(responseData)
			return
		}
		handler.ServeHTTP(writer, request)
	})
	fixture.public.Config.ReadHeaderTimeout = 5 * time.Second
	fixture.public.StartTLS()
	test.Cleanup(fixture.public.Close)
	fixture.control = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(fixture.capability)) != 1 {
			http.Error(writer, "fixture capability required", http.StatusUnauthorized)
			return
		}
		if request.Header.Get("Origin") != "" || request.URL.RawQuery != "" {
			http.Error(writer, "fixture control provenance rejected", http.StatusForbidden)
			return
		}
		fixture.mutex.Lock()
		defer fixture.mutex.Unlock()
		switch request.Method + " " + request.URL.Path {
		case "GET /state":
			_ = json.MarshalWrite(writer, map[string]any{"requests": fixture.requests, "mode": fixture.mode})
		case "POST /mode":
			data, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 128))
			if err != nil || (string(data) != "normal" && string(data) != "slow" && string(data) != "error" && string(data) != "repeat") {
				http.Error(writer, "unknown fixture mode", http.StatusBadRequest)
				return
			}
			fixture.mode = string(data)
		case "POST /stop":
			cancel()
		default:
			http.NotFound(writer, request)
		}
	}))
	test.Cleanup(fixture.control.Close)
	return fixture
}

func TestPublicCatalogBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_PUBLIC_CATALOG_BROWSER_DIR")
	if directory == "" {
		test.Skip("isolated public catalog browser fixture")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	fixture := newPublicCatalogBrowserFixture(test)
	data, err := json.Marshal(map[string]any{"url": fixture.public.URL, "controlURL": fixture.control.URL, "capability": fixture.capability})
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

func TestPublicCatalogBrowserFixtureDataAndGuards(test *testing.T) {
	fixture := newPublicCatalogBrowserFixture(test)
	client := catalogv1connect.NewPublicCatalogServiceClient(fixture.public.Client(), fixture.public.URL)
	page, err := client.ListSkills(test.Context(), connect.NewRequest(&catalogv1.ListSkillsRequest{Limit: 24}))
	if err != nil || len(page.Msg.Skills) != 24 || page.Msg.NextCursor == "" {
		test.Fatalf("real public pagination unavailable: %v", err)
	}
	for _, entry := range page.Msg.Skills {
		if strings.Contains(entry.Name, "PRIVATE") || len(entry.ContentHash) != 64 {
			test.Fatal("private fixture or invalid content hash in public results")
		}
	}
	last, err := client.ListSkills(test.Context(), connect.NewRequest(&catalogv1.ListSkillsRequest{Limit: 24, Cursor: page.Msg.NextCursor}))
	if err != nil || len(last.Msg.Skills) != 3 || last.Msg.NextCursor != "" {
		test.Fatalf("real final public page unavailable: %v", err)
	}
	categories, err := client.ListCategories(test.Context(), connect.NewRequest(&catalogv1.PublicCatalogServiceListCategoriesRequest{Limit: 64}))
	if err != nil || len(categories.Msg.Categories) != 64 || categories.Msg.NextCursor == "" {
		test.Fatalf("public category pagination unavailable: %v", err)
	}
	for _, category := range categories.Msg.Categories {
		if category.SkillCount != 1 || strings.Contains(category.Name, "PRIVATE") {
			test.Fatal("public category counts include unpublished fixture data")
		}
	}
	categoryEnd, err := client.ListCategories(test.Context(), connect.NewRequest(&catalogv1.PublicCatalogServiceListCategoriesRequest{Limit: 64, Cursor: categories.Msg.NextCursor}))
	if err != nil || len(categoryEnd.Msg.Categories) != 2 || categoryEnd.Msg.NextCursor != "" {
		test.Fatalf("public category final page unavailable: %v", err)
	}
	fixture.mutex.Lock()
	fixture.mode = "repeat"
	fixture.mutex.Unlock()
	for _, options := range [][]connect.ClientOption{nil, {connect.WithProtoJSON()}} {
		client := catalogv1connect.NewPublicCatalogServiceClient(fixture.public.Client(), fixture.public.URL, options...)
		repeated, err := client.ListSkills(test.Context(), connect.NewRequest(&catalogv1.ListSkillsRequest{Limit: 24, Cursor: page.Msg.NextCursor}))
		if err != nil || repeated.Msg.NextCursor != page.Msg.NextCursor || len(repeated.Msg.Skills) != 3 {
			test.Fatalf("repeat-cursor fixture must preserve real rows for binary and JSON: %v", err)
		}
	}
	response, err := fixture.control.Client().Get(fixture.control.URL + "/state")
	if err != nil {
		test.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		test.Fatal("fixture control accepted a request without its capability")
	}
}
