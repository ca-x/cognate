package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/v2"
	"errors"
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
	githubv1 "github.com/ca-x/cognate/gen/skillhub/github/v1"
	"github.com/ca-x/cognate/gen/skillhub/github/v1/githubv1connect"
	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/githubauth"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/web"
	"google.golang.org/protobuf/encoding/protojson"
)

type sourcesBrowserFixture struct {
	public, control *httptest.Server
	store           *state.Store
	owner, database string
	password, token string
	capability      string
	session         string
	cancel          context.CancelFunc
	done            context.Context
	mutex           sync.Mutex
	mode            string
	count           int
	requests        []string
	failures        []string
	failureEvents   []sourcesBrowserFailure
	stage           string
	validated       int
	wireSafe        bool
}

type sourcesBrowserFailure struct {
	Sequence int    `json:"sequence"`
	Stage    string `json:"stage"`
	Mode     string `json:"mode"`
	Method   string `json:"method"`
	Code     string `json:"code"`
}

func newSourcesBrowserFixture(test *testing.T) *sourcesBrowserFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "sources.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	done, cancel := context.WithCancel(test.Context())
	test.Cleanup(cancel)
	fixture := &sourcesBrowserFixture{store: store, database: directory, password: rand.Text(), token: rand.Text(), capability: rand.Text(), done: done, cancel: cancel, mode: "normal", stage: "startup", count: 28, wireSafe: true}
	fixture.owner, err = authentication.Register(test.Context(), "sources-fixture", []byte(fixture.password))
	if err != nil {
		test.Fatal(err)
	}
	session, err := authentication.Login(test.Context(), "sources-fixture", []byte(fixture.password))
	if err != nil {
		test.Fatal(err)
	}
	fixture.session = session.Reveal()
	upstream := &http.Client{Transport: graphTransport(fixture.upstream)}
	device, err := githubauth.NewService(store, "synthetic-sources-client", upstream)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(device.Close)
	fixture.public = httptest.NewUnstartedServer(nil)
	origin := "https://" + fixture.public.Listener.Addr().String()
	handler := NewWithOptions(authentication, Options{Store: store, GitHubClient: upstream, GitHubDevice: device, PublicOrigin: origin, WebUI: web.Handler()})
	fixture.public.Config.Handler = fixture.wrap(handler)
	fixture.public.Config.ReadHeaderTimeout = 5 * time.Second
	fixture.public.StartTLS()
	test.Cleanup(fixture.public.Close)
	fixture.control = httptest.NewServer(http.HandlerFunc(fixture.controls))
	test.Cleanup(fixture.control.Close)
	return fixture
}

func (fixture *sourcesBrowserFixture) upstream(request *http.Request) (*http.Response, error) {
	fixture.mutex.Lock()
	mode, count := fixture.mode, fixture.count
	fixture.mutex.Unlock()
	var payload any
	switch request.URL.String() {
	case "https://github.com/login/device/code":
		payload = map[string]any{"device_code": rand.Text(), "user_code": "TEST-ONLY", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 60}
	case "https://github.com/login/oauth/access_token":
		payload = map[string]any{"error": "authorization_pending"}
	case "https://api.github.com/graphql":
		if request.Header.Get("Authorization") != "Bearer "+fixture.token || mode == "upstream-error" {
			return nil, errors.New("synthetic upstream unavailable")
		}
		fixture.mutex.Lock()
		fixture.validated++
		fixture.mutex.Unlock()
		var query struct {
			Variables struct {
				ID string `json:"id"`
			} `json:"variables"`
		}
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			return nil, errors.New("synthetic query invalid")
		}
		page := map[string]any{"hasNextPage": false, "endCursor": nil}
		metadata := func(index int) map[string]any {
			names := []string{"Engineering tools", "Design systems · 设计与可访问性", "Research and long-term reference collections for engineering teams", "UnbrokenNameABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"}
			return map[string]any{"id": fmt.Sprintf("list-%02d", index), "name": fmt.Sprintf("%s %02d", names[index%len(names)], index), "slug": fmt.Sprintf("fixture-reference-collection-%02d", index), "isPrivate": index%4 == 1, "updatedAt": "2026-10-31T00:00:00Z", "lastAddedAt": "2026-10-31T00:00:00Z"}
		}
		if query.Variables.ID == "" {
			if mode == "slow-upstream" {
				select {
				case <-time.After(1500 * time.Millisecond):
				case <-request.Context().Done():
					return nil, request.Context().Err()
				}
			}
			nodes := make([]any, 0, count)
			for index := range count {
				nodes = append(nodes, metadata(index))
			}
			payload = map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "synthetic-github-viewer", "lists": map[string]any{"pageInfo": page, "nodes": nodes}}}}
		} else {
			var index int
			if _, err := fmt.Sscanf(query.Variables.ID, "list-%02d", &index); err != nil {
				return nil, errors.New("synthetic list invalid")
			}
			node := metadata(index)
			node["__typename"] = "UserList"
			node["user"] = map[string]any{"id": "synthetic-github-viewer"}
			node["items"] = map[string]any{"totalCount": 1, "pageInfo": page, "nodes": []any{map[string]any{"__typename": "Repository", "id": fmt.Sprintf("repo-%02d", index), "nameWithOwner": fmt.Sprintf("synthetic/tools-%02d", index), "databaseId": index + 1, "isPrivate": false}}}
			payload = map[string]any{"data": map[string]any{"node": node}}
		}
	default:
		return nil, errors.New("external network forbidden in fixture")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
}

func (fixture *sourcesBrowserFixture) wrap(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/skillhub.") {
			handler.ServeHTTP(writer, request)
			return
		}
		_, method, _ := strings.CutLast(request.URL.Path, "/")
		fixture.mutex.Lock()
		mode, stage := fixture.mode, fixture.stage
		if mode == "internal-once" {
			fixture.mode = "normal"
		}
		fixture.requests = append(fixture.requests, method)
		fixture.mutex.Unlock()
		if mode == "slow" && method == "GetCatalog" {
			select {
			case <-time.After(2 * time.Second):
			case <-request.Context().Done():
				return
			}
		}
		if mode == "internal-once" || mode == "error" && method == "GetCatalog" {
			code, status := "unavailable", http.StatusServiceUnavailable
			if mode == "internal-once" {
				code, status = "internal", http.StatusInternalServerError
			}
			fixture.recordFailure(stage, mode, method, code)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(status)
			_ = json.MarshalWrite(writer, map[string]string{"code": code, "message": "synthetic fixture failure"})
			return
		}
		request.Header.Del("Accept-Encoding")
		recorded := httptest.NewRecorder()
		handler.ServeHTTP(recorded, request)
		data := recorded.Body.Bytes()
		if mode == "short-expiry" && method == "GetCatalog" && recorded.Code == http.StatusOK {
			var response githubv1.GetCatalogResponse
			if protojson.Unmarshal(data, &response) == nil && response.Catalog != nil && response.Catalog.Connected {
				response.Catalog.ValidUntilUnix = time.Now().Add(3 * time.Second).Unix()
				data, _ = protojson.Marshal(&response)
			}
		}
		if mode == "absent" && method == "GetCatalog" && recorded.Code == http.StatusOK {
			data = []byte(`{}`)
		}
		if recorded.Code >= http.StatusBadRequest {
			var failure struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(data, &failure) != nil || failure.Code == "" {
				failure.Code = "unknown"
			}
			fixture.recordFailure(stage, mode, method, failure.Code)
		}
		fixture.mutex.Lock()
		fixture.wireSafe = fixture.wireSafe && !bytes.Contains(data, []byte(fixture.token))
		fixture.mutex.Unlock()
		for name, values := range recorded.Header() {
			if name != "Content-Length" {
				writer.Header()[name] = values
			}
		}
		writer.WriteHeader(recorded.Code)
		_, _ = writer.Write(data)
	})
}

func (fixture *sourcesBrowserFixture) recordFailure(stage, mode, method, code string) {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	fixture.failures = append(fixture.failures, method+":"+code)
	fixture.failureEvents = append(fixture.failureEvents, sourcesBrowserFailure{Sequence: len(fixture.failureEvents) + 1, Stage: stage, Mode: mode, Method: method, Code: code})
}

func (fixture *sourcesBrowserFixture) controls(writer http.ResponseWriter, request *http.Request) {
	if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(fixture.capability)) != 1 || request.Header.Get("Origin") != "" || request.URL.RawQuery != "" {
		http.Error(writer, "fixture control rejected", http.StatusForbidden)
		return
	}
	switch request.Method + " " + request.URL.Path {
	case "GET /state":
		plaintextAbsent := true
		for _, name := range []string{"sources.db", "sources.db-wal", "sources.db-shm"} {
			data, err := os.ReadFile(filepath.Join(fixture.database, name))
			if err == nil && bytes.Contains(data, []byte(fixture.token)) {
				plaintextAbsent = false
			}
		}
		fixture.mutex.Lock()
		defer fixture.mutex.Unlock()
		_ = json.MarshalWrite(writer, map[string]any{"requests": fixture.requests, "failures": fixture.failures, "failureEvents": fixture.failureEvents, "validated": fixture.validated, "wireSafe": fixture.wireSafe, "plaintextAbsent": plaintextAbsent})
	case "POST /stage":
		data, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 64))
		if err != nil || len(data) == 0 || strings.Trim(string(data), "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			http.Error(writer, "invalid stage", http.StatusBadRequest)
			return
		}
		fixture.mutex.Lock()
		fixture.stage = string(data)
		fixture.mutex.Unlock()
	case "POST /mode":
		data, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 64))
		mode := string(data)
		if err != nil || !map[string]bool{"normal": true, "slow": true, "slow-upstream": true, "error": true, "upstream-error": true, "short-expiry": true, "absent": true, "empty": true, "long": true, "internal-once": true}[mode] {
			http.Error(writer, "unknown mode", http.StatusBadRequest)
			return
		}
		fixture.mutex.Lock()
		if mode == "empty" {
			fixture.count, fixture.mode = 0, "normal"
		} else if mode == "long" {
			fixture.count, fixture.mode = 28, "normal"
		} else {
			fixture.mode = mode
		}
		fixture.mutex.Unlock()
	case "POST /expire", "POST /rotate":
		credential, err := fixture.store.Credential(request.Context(), fixture.owner)
		if err != nil {
			http.Error(writer, "no credential", http.StatusConflict)
			return
		}
		expires := time.Now().Add(time.Hour)
		if request.URL.Path == "/expire" {
			expires = time.Now().Add(-time.Second)
		}
		material, err := json.Marshal(map[string]any{"version": 1, "access": fixture.token, "expires_at": expires})
		if err != nil {
			http.Error(writer, "expiry encoding failed", http.StatusInternalServerError)
			return
		}
		defer clear(material)
		token, err := github.DecodeStoredToken(material)
		if err == nil {
			_, err = fixture.store.PutCredential(request.Context(), fixture.owner, "synthetic-github-viewer", credential.Generation, token)
		}
		if err != nil {
			http.Error(writer, "expiry failed", http.StatusInternalServerError)
		}
	case "POST /stop":
		fixture.cancel()
	default:
		http.NotFound(writer, request)
	}
}

func TestSourcesBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_SOURCES_BROWSER_DIR")
	if directory == "" {
		test.Skip("isolated sources browser fixture")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	fixture := newSourcesBrowserFixture(test)
	data, err := json.Marshal(map[string]any{"url": fixture.public.URL, "controlURL": fixture.control.URL, "capability": fixture.capability, "username": "sources-fixture", "password": fixture.password, "token": fixture.token})
	if err != nil {
		test.Fatal("fixture readiness encoding failed")
	}
	file, err := root.OpenFile("ready.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		test.Fatal("fixture readiness write failed")
	}
	if err := file.Close(); err != nil {
		test.Fatal(err)
	}
	<-fixture.done.Done()
}

func TestSourcesBrowserFixtureRealConnectAndGuards(test *testing.T) {
	fixture := newSourcesBrowserFixture(test)
	client := githubv1connect.NewGitHubServiceClient(fixture.public.Client(), fixture.public.URL)
	request := connect.NewRequest(&githubv1.ConnectTokenRequest{AccessToken: fixture.token})
	request.Header().Set("Authorization", "Bearer "+fixture.session)
	connected, err := client.ConnectToken(test.Context(), request)
	if err != nil || connected.Msg.CredentialGeneration == "" {
		test.Fatal("real token validation failed")
	}
	if _, err := client.ConnectToken(test.Context(), request); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("generation guard missing")
	}
	syncRequest := connect.NewRequest(&githubv1.SyncRequest{})
	syncRequest.Header().Set("Authorization", "Bearer "+fixture.session)
	result, err := client.Sync(test.Context(), syncRequest)
	if err != nil || len(result.Msg.GetCatalog().GetLists()) != 28 {
		test.Fatal("real SQLite list sync failed")
	}
	selection := connect.NewRequest(&githubv1.SetListSubscriptionRequest{ListId: "list-00", Subscribed: true})
	selection.Header().Set("Authorization", "Bearer "+fixture.session)
	if _, err := client.SetListSubscription(test.Context(), selection); err != nil {
		test.Fatal("fixture subscription failed")
	}
	for _, count := range []int{28, 0, 28} {
		fixture.mutex.Lock()
		fixture.count = count
		fixture.mutex.Unlock()
		if _, err := client.Sync(test.Context(), syncRequest); err != nil {
			test.Fatalf("fixture collection transition code: %s", connect.CodeOf(err))
		}
	}
	response, err := fixture.control.Client().Get(fixture.control.URL + "/state")
	if err != nil {
		test.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		test.Fatal("control capability guard missing")
	}
	expire := httptest.NewRequest(http.MethodPost, "/expire", nil)
	expire.Header.Set("X-Fixture-Capability", fixture.capability)
	recorded := httptest.NewRecorder()
	fixture.controls(recorded, expire)
	if recorded.Code != http.StatusOK {
		test.Fatal("fixture expiry failed")
	}
	catalogRequest := connect.NewRequest(&githubv1.GetCatalogRequest{})
	catalogRequest.Header().Set("Authorization", "Bearer "+fixture.session)
	catalog, err := client.GetCatalog(test.Context(), catalogRequest)
	if err != nil || !catalog.Msg.GetCatalog().GetAuthorizationRequired() || len(catalog.Msg.Catalog.Lists) != 0 {
		test.Fatal("expired credential did not hide lists")
	}
	replacement := connect.NewRequest(&githubv1.ConnectTokenRequest{AccessToken: fixture.token, ExpectedGeneration: catalog.Msg.Catalog.CredentialGeneration})
	replacement.Header().Set("Authorization", "Bearer "+fixture.session)
	if _, err := client.ConnectToken(test.Context(), replacement); err != nil {
		test.Fatalf("expired replacement code: %s", connect.CodeOf(err))
	}
	result, err = client.Sync(test.Context(), syncRequest)
	if err != nil || len(result.Msg.GetCatalog().GetLists()) != 28 {
		test.Fatalf("replacement sync code: %s", connect.CodeOf(err))
	}
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	if !fixture.wireSafe || fixture.validated == 0 {
		test.Fatal("unsafe fixture responses or missing upstream validation")
	}
}
