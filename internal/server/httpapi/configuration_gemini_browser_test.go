package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/web"
)

type geminiBrowserFaults struct {
	mutex          sync.Mutex
	requests       int
	writes         int
	commits        int
	storeCalls     int
	controlCommits int
	active         int
	next           string
	busy           bool
	stage          string
	gate           chan struct{}
	changed        chan struct{}
	workers        sync.WaitGroup
}

type geminiBrowserCall struct {
	mode      string
	committed bool
}
type geminiBrowserCallKey struct{}

type geminiBrowserCommitStore struct {
	configuration.Store
	faults *geminiBrowserFaults
}

func (faults *geminiBrowserFaults) notify() {
	close(faults.changed)
	faults.changed = make(chan struct{})
}

func (faults *geminiBrowserFaults) before(ctx context.Context) error {
	call, _ := ctx.Value(geminiBrowserCallKey{}).(*geminiBrowserCall)
	if call != nil && call.mode == "hold-before" {
		if err := faults.wait(ctx, "before"); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	faults.mutex.Lock()
	faults.storeCalls++
	faults.notify()
	faults.mutex.Unlock()
	return nil
}

func (faults *geminiBrowserFaults) committed(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	call, _ := ctx.Value(geminiBrowserCallKey{}).(*geminiBrowserCall)
	faults.mutex.Lock()
	faults.commits++
	if call != nil {
		call.committed = true
	} else {
		faults.controlCommits++
	}
	faults.notify()
	faults.mutex.Unlock()
	if call != nil && call.mode == "error" {
		return errors.New("fixture response withheld after commit")
	}
	return nil
}

func (store geminiBrowserCommitStore) CreateConfigurationAsset(ctx context.Context, owner string, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	if err := store.faults.before(ctx); err != nil {
		return state.ConfigurationAsset{}, err
	}
	metadata, err := store.Store.CreateConfigurationAsset(ctx, owner, draft)
	if err := store.faults.committed(ctx, err); err != nil {
		return state.ConfigurationAsset{}, err
	}
	return metadata, nil
}

func (store geminiBrowserCommitStore) UpdateConfigurationAsset(ctx context.Context, owner, id string, revision int64, draft state.ConfigurationAssetDraft) (state.ConfigurationAsset, error) {
	if err := store.faults.before(ctx); err != nil {
		return state.ConfigurationAsset{}, err
	}
	metadata, err := store.Store.UpdateConfigurationAsset(ctx, owner, id, revision, draft)
	if err := store.faults.committed(ctx, err); err != nil {
		return state.ConfigurationAsset{}, err
	}
	return metadata, nil
}

func (store geminiBrowserCommitStore) DeleteConfigurationAsset(ctx context.Context, owner, id string, revision int64) error {
	if err := store.faults.before(ctx); err != nil {
		return err
	}
	return store.faults.committed(ctx, store.Store.DeleteConfigurationAsset(ctx, owner, id, revision))
}

func (faults *geminiBrowserFaults) releaseHeld() {
	faults.mutex.Lock()
	defer faults.mutex.Unlock()
	if faults.gate != nil {
		close(faults.gate)
		faults.gate = nil
	}
	faults.stage = ""
	faults.notify()
}

func (faults *geminiBrowserFaults) wait(ctx context.Context, stage string) error {
	gate := make(chan struct{})
	faults.mutex.Lock()
	faults.gate = gate
	faults.stage = stage
	faults.notify()
	faults.mutex.Unlock()
	select {
	case <-gate:
	case <-ctx.Done():
		faults.releaseHeld()
		return ctx.Err()
	}
	return ctx.Err()
}

func (faults *geminiBrowserFaults) wrap(ctx context.Context, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestContext, cancel := context.WithCancel(request.Context())
		stop := context.AfterFunc(ctx, cancel)
		defer func() { stop(); cancel() }()
		call := &geminiBrowserCall{}
		faults.mutex.Lock()
		if ctx.Err() != nil {
			faults.mutex.Unlock()
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		faults.workers.Add(1)
		faults.active++
		faults.requests++
		switch request.URL.Path {
		case configurationv1connect.ConfigurationServiceCreateAssetProcedure, configurationv1connect.ConfigurationServiceUpdateAssetProcedure, configurationv1connect.ConfigurationServiceCopyAssetProcedure, configurationv1connect.ConfigurationServiceDeleteAssetProcedure:
			faults.writes++
			call.mode, faults.next = faults.next, ""
			if call.mode != "" {
				faults.busy = true
			}
		}
		faults.notify()
		faults.mutex.Unlock()
		defer func() {
			faults.mutex.Lock()
			faults.active--
			if call.mode != "" {
				faults.busy = false
			}
			faults.notify()
			faults.mutex.Unlock()
			faults.workers.Done()
		}()
		request = request.WithContext(context.WithValue(requestContext, geminiBrowserCallKey{}, call))
		writer.Header().Set("Cache-Control", "no-store")
		if call.mode == "" {
			handler.ServeHTTP(writer, request)
			return
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		defer clear(recorder.Body.Bytes())
		if call.mode == "hold" && call.committed && recorder.Code == http.StatusOK {
			if faults.wait(requestContext, "after") != nil {
				return
			}
		}
		if requestContext.Err() != nil {
			return
		}
		for name, values := range recorder.Header() {
			writer.Header()[name] = values
		}
		if call.mode == "drop" && call.committed && recorder.Code == http.StatusOK {
			writer.Header().Set("Content-Length", "1048576")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write(recorder.Body.Bytes()[:min(8, recorder.Body.Len())])
			_ = http.NewResponseController(writer).Flush()
			if hijacker, ok := writer.(http.Hijacker); ok {
				connection, _, err := hijacker.Hijack()
				if err == nil {
					_ = connection.Close()
				}
			}
			return
		}
		writer.WriteHeader(recorder.Code)
		_, _ = writer.Write(recorder.Body.Bytes())
	})
}

type geminiBrowserRealm struct {
	store          *state.Store
	authentication *auth.Service
	domain         *configuration.Service
	public         *httptest.Server
	owners         map[string]string
}

type geminiBrowserFixture struct {
	ctx               context.Context
	cancel            context.CancelFunc
	faults            *geminiBrowserFaults
	realm             *geminiBrowserRealm
	alternate         *geminiBrowserRealm
	control           *httptest.Server
	capability        string
	password          string
	closeOnce         sync.Once
	done              chan struct{}
	home              string
	baseline          map[string][]byte
	trap              *geminiBrowserNetworkTrap
	originalTransport http.RoundTripper
	expectedKey       []byte
	readyRoot         *os.Root
	readyFiles        []string
	closeOK           bool
}

type geminiBrowserNetworkTrap struct {
	base    *http.Transport
	allowed map[string]bool
	blocked atomic.Int32
}

func (trap *geminiBrowserNetworkTrap) RoundTrip(request *http.Request) (*http.Response, error) {
	if !trap.allowed[request.URL.Scheme+"://"+request.URL.Host] {
		trap.blocked.Add(1)
		return nil, errors.New("fixture forbids external network")
	}
	return trap.base.RoundTrip(request)
}

func (trap *geminiBrowserNetworkTrap) CloseIdleConnections() { trap.base.CloseIdleConnections() }

type geminiBrowserCounters struct {
	Requests       int    `json:"requests"`
	Writes         int    `json:"writes"`
	StoreCalls     int    `json:"storeCalls"`
	Commits        int    `json:"commits"`
	ControlCommits int    `json:"controlCommits"`
	Active         int    `json:"active"`
	Held           bool   `json:"held"`
	Stage          string `json:"stage"`
}

func (faults *geminiBrowserFaults) snapshot() (geminiBrowserCounters, <-chan struct{}) {
	faults.mutex.Lock()
	defer faults.mutex.Unlock()
	return geminiBrowserCounters{Requests: faults.requests, Writes: faults.writes, StoreCalls: faults.storeCalls, Commits: faults.commits, ControlCommits: faults.controlCommits, Active: faults.active, Held: faults.stage != "", Stage: faults.stage}, faults.changed
}

func newGeminiBrowserFixture(test *testing.T) *geminiBrowserFixture {
	return newGeminiBrowserFixtureWithRoot(test, nil)
}

func newGeminiBrowserFixtureWithRoot(test *testing.T, readyRoot *os.Root) *geminiBrowserFixture {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 45*time.Minute)
	fixture := &geminiBrowserFixture{ctx: ctx, cancel: cancel, faults: &geminiBrowserFaults{changed: make(chan struct{})}, capability: rand.Text(), password: rand.Text(), done: make(chan struct{}), readyRoot: readyRoot, closeOK: true}
	initialized := make(chan struct{})
	defer close(initialized)
	go fixture.shutdown(initialized)
	test.Cleanup(fixture.Close)
	fixture.expectedKey = []byte(rand.Text())
	fixture.realm = fixture.newRealm(test)
	fixture.alternate = fixture.newRealm(test)
	fixture.control = httptest.NewUnstartedServer(http.HandlerFunc(fixture.controlRequest))
	fixture.control.Config.ReadHeaderTimeout = 5 * time.Second
	fixture.control.Config.ReadTimeout = 5 * time.Second
	fixture.control.Config.WriteTimeout = 5 * time.Second
	fixture.control.Start()
	fixture.home = test.TempDir()
	fixture.baseline = map[string][]byte{".gemini/.env": []byte("foreign-env"), ".gemini/settings.json": []byte(`{"foreign":true}`), "current": []byte("current-baseline"), "selection.json": []byte("selection-baseline"), "Pack/manifest.json": []byte(`{"baseline":true}`)}
	for name, data := range fixture.baseline {
		path := filepath.Join(fixture.home, name)
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, data, 0600) != nil {
			test.Fatal("synthetic native baseline setup failed")
		}
	}
	test.Setenv("HOME", fixture.home)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(fixture.home, ".config"))
	test.Setenv("XDG_DATA_HOME", filepath.Join(fixture.home, ".local/share"))
	fixture.originalTransport = http.DefaultTransport
	original, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		test.Fatal("fixture requires an isolated standard HTTP transport")
	}
	transport := original.Clone()
	transport.Proxy = nil
	fixture.trap = &geminiBrowserNetworkTrap{base: transport, allowed: map[string]bool{fixture.realm.public.URL: true, fixture.alternate.public.URL: true, fixture.control.URL: true}}
	http.DefaultTransport = fixture.trap
	fixture.realm.public.Client().Transport = fixture.trap
	fixture.alternate.public.Client().Transport = fixture.trap
	fixture.control.Client().Transport = fixture.trap
	if readyRoot != nil {
		if err := fixture.writeReadiness(); err != nil {
			test.Fatal("private fixture readiness setup failed")
		}
	}
	return fixture
}

func (fixture *geminiBrowserFixture) shutdown(initialized <-chan struct{}) {
	<-fixture.ctx.Done()
	<-initialized
	fixture.closeOnce.Do(func() {
		defer close(fixture.done)
		fixture.faults.releaseHeld()
		for _, realm := range []*geminiBrowserRealm{fixture.realm, fixture.alternate} {
			if realm != nil {
				realm.public.CloseClientConnections()
				realm.public.Close()
			}
		}
		if fixture.control != nil {
			fixture.control.CloseClientConnections()
			fixture.control.Close()
		}
		fixture.faults.workers.Wait()
		for _, realm := range []*geminiBrowserRealm{fixture.realm, fixture.alternate} {
			if realm != nil && realm.store.Close() != nil {
				fixture.closeOK = false
			}
		}
		if fixture.trap != nil {
			http.DefaultTransport = fixture.originalTransport
			fixture.trap.CloseIdleConnections()
		}
		if fixture.readyRoot != nil {
			for _, name := range fixture.readyFiles {
				if fixture.readyRoot.Remove(name) != nil {
					fixture.closeOK = false
				}
			}
			snapshot, _ := fixture.faults.snapshot()
			closed, err := json.Marshal(struct {
				geminiBrowserCounters
				Closed          bool `json:"closed"`
				SideEffectsZero bool `json:"sideEffectsZero"`
			}{snapshot, fixture.closeOK, fixture.trap != nil && fixture.sideEffectsZero()})
			if err != nil || fixture.writePrivate("stopped.json", closed) != nil {
				fixture.closeOK = false
			}
			clear(closed)
			if fixture.readyRoot.Close() != nil {
				fixture.closeOK = false
			}
		}
		clear(fixture.expectedKey)
		fixture.password, fixture.capability = "", ""
	})
}

func (fixture *geminiBrowserFixture) newRealm(test *testing.T) *geminiBrowserRealm {
	test.Helper()
	directory := test.TempDir()
	if os.Chmod(directory, 0700) != nil {
		test.Fatal("private fixture directory setup failed")
	}
	master := make([]byte, 32)
	defer clear(master)
	if _, err := rand.Read(master); err != nil {
		test.Fatal("synthetic fixture key setup failed")
	}
	store, err := state.Open(fixture.ctx, filepath.Join(directory, "fixture.db"), master)
	if err != nil {
		test.Fatal("synthetic fixture database setup failed")
	}
	complete := false
	defer func() {
		if !complete {
			_ = store.Close()
		}
	}()
	authentication, err := auth.NewService(store, master)
	clear(master)
	if err != nil {
		test.Fatal("synthetic fixture authentication setup failed")
	}
	realm := &geminiBrowserRealm{store: store, authentication: authentication, domain: configuration.New(geminiBrowserCommitStore{Store: store, faults: fixture.faults}), owners: make(map[string]string)}
	for _, name := range []string{"gemini-browser-alice", "gemini-browser-bob"} {
		owner, err := authentication.Register(fixture.ctx, name, []byte(fixture.password))
		if err != nil {
			test.Fatal("synthetic browser registration failed")
		}
		realm.owners[name] = owner
	}
	realm.public = httptest.NewUnstartedServer(nil)
	origin := "http://" + realm.public.Listener.Addr().String()
	realm.public.Config.Handler = fixture.faults.wrap(fixture.ctx, NewWithOptions(authentication, Options{PublicOrigin: origin, Configuration: realm.domain, WebUI: web.Handler()}))
	realm.public.Config.ReadHeaderTimeout = 5 * time.Second
	realm.public.Start()
	complete = true
	return realm
}

func geminiBrowserOpenRoot(directory string) (*os.Root, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return nil, errors.New("fixture directory must be private")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.New("fixture directory unavailable")
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, errors.New("fixture directory changed")
	}
	return root, nil
}

func (fixture *geminiBrowserFixture) writePrivate(name string, encoded []byte) error {
	file, err := fixture.readyRoot.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("fixture private file unavailable")
	}
	fixture.readyFiles = append(fixture.readyFiles, name)
	_, err = file.Write(encoded)
	closed := file.Close()
	if err != nil || closed != nil {
		return errors.New("fixture private file incomplete")
	}
	return nil
}

func (fixture *geminiBrowserFixture) writeReadiness() error {
	public, err := json.Marshal(struct {
		URL             string `json:"url"`
		ControlURL      string `json:"controlURL"`
		AlternateURL    string `json:"alternateURL"`
		Ready           bool   `json:"ready"`
		LifetimeSeconds int    `json:"lifetimeSeconds"`
	}{fixture.realm.public.URL, fixture.control.URL, fixture.alternate.public.URL, true, 2700})
	if err != nil || fixture.writePrivate("public-ready.json", public) != nil {
		return errors.New("fixture public readiness unavailable")
	}
	private, err := json.Marshal(struct {
		URL        string                 `json:"url"`
		ControlURL string                 `json:"controlURL"`
		Capability string                 `json:"capability"`
		Bootstrap  geminiBrowserBootstrap `json:"bootstrap"`
	}{fixture.realm.public.URL, fixture.control.URL, fixture.capability, geminiBrowserBootstrap{
		Owners: []geminiBrowserAccount{{Username: "gemini-browser-alice", Password: fixture.password}, {Username: "gemini-browser-bob", Password: fixture.password}},
		APIKey: string(fixture.expectedKey),
	}})
	defer clear(private)
	if err != nil {
		return errors.New("fixture private readiness unavailable")
	}
	return fixture.writePrivate("ready.json", private)
}

type geminiBrowserAccount struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type geminiBrowserBootstrap struct {
	Owners []geminiBrowserAccount `json:"owners"`
	APIKey string                 `json:"apiKey"`
}

func TestGeminiAssetsBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_GEMINI_BROWSER_DIR")
	if directory == "" {
		test.Skip("isolated browser fixture entrypoint only; mechanical tests run separately")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal("browser control directory is not private")
	}
	fixture := newGeminiBrowserFixtureWithRoot(test, root)
	<-fixture.done
	if !fixture.closeOK || errors.Is(fixture.ctx.Err(), context.DeadlineExceeded) {
		test.Fatal("browser fixture did not stop and drain normally")
	}
}

func (fixture *geminiBrowserFixture) Close() {
	fixture.cancel()
	<-fixture.done
}

func (fixture *geminiBrowserFixture) controlRequest(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(fixture.capability)) != 1 {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	if request.Header.Get("Origin") != "" || request.Header.Get("Sec-Fetch-Site") != "" || request.URL.RawQuery != "" {
		writer.WriteHeader(http.StatusForbidden)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/state" {
		receipt, err := fixture.receipt()
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		encoded, err := json.Marshal(receipt)
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = writer.Write(encoded)
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/command" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	encoded, err := io.ReadAll(io.LimitReader(request.Body, 4097))
	defer clear(encoded)
	if err != nil || len(encoded) > 4096 {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	var command struct {
		Action  string `json:"action"`
		AssetID string `json:"assetID"`
	}
	if json.Unmarshal(encoded, &command, json.RejectUnknownMembers(true)) != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if command.Action != "mutate" && command.Action != "extras" && command.AssetID != "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	stop := false
	switch command.Action {
	case "hold-next-write", "drop-next-write", "hold-before-next-write", "error-next-write":
		mode := strings.TrimSuffix(command.Action, "-next-write")
		fixture.faults.mutex.Lock()
		if fixture.faults.next != "" || fixture.faults.busy {
			fixture.faults.mutex.Unlock()
			writer.WriteHeader(http.StatusConflict)
			return
		}
		fixture.faults.next = mode
		fixture.faults.notify()
		fixture.faults.mutex.Unlock()
	case "release":
		fixture.faults.releaseHeld()
	case "mutate", "extras":
		if len(command.AssetID) != 26 {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		owner := fixture.realm.owners["gemini-browser-alice"]
		metadata, payload, err := fixture.realm.store.ReadConfigurationAssetVersion(fixture.ctx, owner, command.AssetID)
		if err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if command.Action == "mutate" {
			view, err := fixture.realm.domain.Edit(fixture.ctx, owner, metadata.ID)
			if err != nil || view.HasPreservedExtras {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			_, err = fixture.realm.domain.Update(fixture.ctx, owner, metadata.ID, metadata.Revision, configuration.Draft{Name: "Externally revised", Provider: view.Provider, CodexProvider: view.CodexProvider, GeminiProvider: view.GeminiProvider}, nil)
			if err != nil {
				writer.WriteHeader(http.StatusConflict)
				return
			}
		} else {
			raw := payload.Reveal()
			defer clear(raw)
			if len(raw) == 0 || raw[len(raw)-1] != '}' {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			merged := append(bytes.Clone(raw[:len(raw)-1]), []byte(`,"future":null}`)...)
			defer clear(merged)
			wrapped := geminiBrowserCommitStore{Store: fixture.realm.store, faults: fixture.faults}
			_, err = wrapped.UpdateConfigurationAsset(fixture.ctx, owner, metadata.ID, metadata.Revision, state.ConfigurationAssetDraft{AgentID: metadata.AgentID, Kind: metadata.Kind, Name: metadata.Name, SchemaVersion: metadata.SchemaVersion, Payload: state.NewConfigurationPayload(merged)})
			if err != nil {
				writer.WriteHeader(http.StatusConflict)
				return
			}
		}
	case "stop":
		stop = true
	default:
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	response := []byte(`{"ok":true}`)
	writer.Header().Set("Content-Length", "11")
	_, _ = writer.Write(response)
	if stop {
		_ = http.NewResponseController(writer).Flush()
		fixture.cancel()
	}
}

type geminiBrowserAssetReceipt struct {
	ID                     string                       `json:"id"`
	Name                   string                       `json:"name"`
	Revision               int64                        `json:"revision"`
	Agent                  string                       `json:"agent"`
	Auth                   string                       `json:"auth"`
	Readiness              configuration.Readiness      `json:"readiness"`
	Extras                 bool                         `json:"extras"`
	Secrets                []configuration.SecretStatus `json:"secrets"`
	FullFixtureMatches     bool                         `json:"fullFixtureMatches"`
	MinimalFixtureMatches  bool                         `json:"minimalFixtureMatches"`
	NativeFixtureMatches   bool                         `json:"nativeFixtureMatches"`
	ExpectedProviderEqual  bool                         `json:"expectedProviderEqual"`
	SlotPresent            bool                         `json:"slotPresent"`
	ExpectedSecretEqual    bool                         `json:"expectedSecretEqual"`
	SecretEmpty            bool                         `json:"secretEmpty"`
	SecretMatchesSynthetic bool                         `json:"secretMatchesSynthetic"`
	EnvModelPresent        bool                         `json:"envModelPresent"`
	SettingsModelPresent   bool                         `json:"settingsModelPresent"`
}

type geminiBrowserReceipt struct {
	geminiBrowserCounters
	Alice            []geminiBrowserAssetReceipt            `json:"gemini-browser-alice"`
	Bob              []geminiBrowserAssetReceipt            `json:"gemini-browser-bob"`
	Alternate        map[string][]geminiBrowserAssetReceipt `json:"alternate"`
	AlternateURL     string                                 `json:"alternateURL"`
	ProviderRequests int32                                  `json:"providerRequests"`
	SideEffectsZero  bool                                   `json:"sideEffectsZero"`
}

type geminiBrowserVersionStore struct {
	configuration.Store
	owner    string
	metadata state.ConfigurationAsset
	payload  state.ConfigurationPayload
}

func (store geminiBrowserVersionStore) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	if err := ctx.Err(); err != nil {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, err
	}
	if owner != store.owner || id != store.metadata.ID {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, state.ErrConfigurationNotFound
	}
	return store.metadata, store.payload, nil
}

func geminiBrowserFullProvider() *configuration.GeminiProvider {
	return &configuration.GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, Endpoint: new("https://provider.example/v1"), KeyTransport: new(agentconfig.GeminiBearer), EnvModel: new("model-test"), SettingsModel: new("settings-model-test")}
}

func (fixture *geminiBrowserFixture) assetReceipts(realm *geminiBrowserRealm, owner string) ([]geminiBrowserAssetReceipt, error) {
	assets, err := realm.domain.List(fixture.ctx, owner)
	if err != nil {
		return nil, err
	}
	result := make([]geminiBrowserAssetReceipt, 0, len(assets))
	for _, asset := range assets {
		metadata, payload, err := realm.store.ReadConfigurationAssetVersion(fixture.ctx, owner, asset.ID)
		if err != nil {
			return nil, err
		}
		view, err := configuration.New(geminiBrowserVersionStore{Store: realm.store, owner: owner, metadata: metadata, payload: payload}).Edit(fixture.ctx, owner, asset.ID)
		if err != nil {
			return nil, err
		}
		receipt := geminiBrowserAssetReceipt{ID: metadata.ID, Name: metadata.Name, Revision: metadata.Revision, Agent: metadata.AgentID, Readiness: view.Readiness, Extras: view.HasPreservedExtras, Secrets: view.Secrets}
		for _, status := range view.Secrets {
			if status.Slot == configuration.GeminiAPIKeySlot {
				receipt.SlotPresent = status.Present
			}
		}
		if view.GeminiProvider != nil {
			receipt.Auth = string(view.GeminiProvider.AuthMode)
			receipt.FullFixtureMatches = reflect.DeepEqual(view.GeminiProvider, geminiBrowserFullProvider())
			receipt.MinimalFixtureMatches = reflect.DeepEqual(view.GeminiProvider, &configuration.GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, KeyTransport: new(agentconfig.GeminiNativeDefault)})
			receipt.NativeFixtureMatches = reflect.DeepEqual(view.GeminiProvider, &configuration.GeminiProvider{AuthMode: agentconfig.GeminiNativeLogin})
			receipt.ExpectedProviderEqual = receipt.FullFixtureMatches
			receipt.EnvModelPresent = view.GeminiProvider.EnvModel != nil
			receipt.SettingsModelPresent = view.GeminiProvider.SettingsModel != nil
		}
		raw := payload.Reveal()
		var private struct {
			Secrets map[string]jsontext.Value `json:"secrets"`
		}
		err = json.Unmarshal(raw, &private)
		clear(raw)
		if err != nil {
			return nil, errors.New("fixture envelope receipt unavailable")
		}
		receipt.SecretEmpty = len(private.Secrets) == 0
		var value string
		if encoded, exists := private.Secrets["api_key"]; exists && json.Unmarshal(encoded, &value) == nil {
			receipt.SecretMatchesSynthetic = subtle.ConstantTimeCompare([]byte(value), fixture.expectedKey) == 1
		}
		receipt.ExpectedSecretEqual = receipt.SecretMatchesSynthetic
		value = ""
		for _, encoded := range private.Secrets {
			clear(encoded)
		}
		result = append(result, receipt)
	}
	return result, nil
}

func (fixture *geminiBrowserFixture) sideEffectsZero() bool {
	if len(fixture.baseline) == 0 || fixture.trap.blocked.Load() != 0 {
		return false
	}
	actual := make(map[string][]byte)
	err := filepath.WalkDir(fixture.home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name, err := filepath.Rel(fixture.home, path)
		if err != nil {
			return err
		}
		actual[name], err = os.ReadFile(path)
		return err
	})
	return err == nil && reflect.DeepEqual(fixture.baseline, actual)
}

func (fixture *geminiBrowserFixture) receipt() (geminiBrowserReceipt, error) {
	receipt := geminiBrowserReceipt{AlternateURL: fixture.alternate.public.URL, Alternate: make(map[string][]geminiBrowserAssetReceipt), ProviderRequests: fixture.trap.blocked.Load(), SideEffectsZero: fixture.sideEffectsZero()}
	var err error
	if receipt.Alice, err = fixture.assetReceipts(fixture.realm, fixture.realm.owners["gemini-browser-alice"]); err != nil {
		return geminiBrowserReceipt{}, err
	}
	if receipt.Bob, err = fixture.assetReceipts(fixture.realm, fixture.realm.owners["gemini-browser-bob"]); err != nil {
		return geminiBrowserReceipt{}, err
	}
	for name, owner := range fixture.alternate.owners {
		if receipt.Alternate[name], err = fixture.assetReceipts(fixture.alternate, owner); err != nil {
			return geminiBrowserReceipt{}, err
		}
	}
	receipt.geminiBrowserCounters, _ = fixture.faults.snapshot()
	return receipt, nil
}

func TestGeminiBrowserHarnessGuardsAndDrain(test *testing.T) {
	test.Run("commit-witness", geminiBrowserBasicWitness)
	for _, codec := range []string{"proto", "json"} {
		test.Run("seven-rpc/"+codec, func(test *testing.T) { geminiBrowserRPCWitness(test, codec) })
	}
	for _, stage := range []string{"before", "after"} {
		for _, outcome := range []string{"release", "cancel", "stop"} {
			test.Run(stage+"/"+outcome, func(test *testing.T) { geminiBrowserPhase(test, stage, outcome) })
		}
	}
	for _, mode := range []string{"drop-next-write", "error-next-write"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newGeminiBrowserFixture(test)
			client, token := geminiBrowserClient(test, fixture.realm, fixture.password)
			geminiBrowserCommand(test, fixture, mode, "", http.StatusOK)
			_, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiBrowserDraft("Unknown outcome")}, token))
			if err == nil {
				test.Fatal("postcommit fault returned a successful receipt")
			}
			failure, typed := errors.AsType[*connect.Error](err)
			if mode == "error-next-write" && (!typed || failure.Code() != connect.CodeInternal || failure.Message() != configuration.ErrUnavailable.Error()) {
				test.Fatal("postcommit Store error did not match the existing sanitized internal contract")
			}
			geminiBrowserWait(test, fixture, func(state geminiBrowserCounters) bool { return state.Active == 0 })
			snapshot, _ := fixture.faults.snapshot()
			assets, err := fixture.realm.domain.List(test.Context(), fixture.realm.owners["gemini-browser-alice"])
			if err != nil || len(assets) != 1 || snapshot.Writes != 1 || snapshot.StoreCalls != 1 || snapshot.Commits != 1 {
				test.Fatal("fault did not follow exactly one actual SQLite commit")
			}
		})
	}
	test.Run("rejected-write-never-held", func(test *testing.T) {
		fixture := newGeminiBrowserFixture(test)
		client, token := geminiBrowserClient(test, fixture.realm, fixture.password)
		geminiBrowserCommand(test, fixture, "hold-next-write", "", http.StatusOK)
		_, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{}, token))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			test.Fatal("invalid real mutation was masked by a fixture fault")
		}
		geminiBrowserWait(test, fixture, func(state geminiBrowserCounters) bool { return state.Active == 0 })
		snapshot, _ := fixture.faults.snapshot()
		if snapshot.Held || snapshot.Commits != 0 || snapshot.StoreCalls != 0 || snapshot.Writes != 1 {
			test.Fatal("uncommitted request fabricated a hold or commit witness")
		}
	})
	test.Run("owner-realm-origin", func(test *testing.T) {
		fixture := newGeminiBrowserFixture(test)
		client, token := geminiBrowserClient(test, fixture.realm, fixture.password)
		asset, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiBrowserDraft("Owner bound")}, token))
		if err != nil {
			test.Fatal("owner fixture create failed")
		}
		login := authv1connect.NewAuthServiceClient(fixture.realm.public.Client(), fixture.realm.public.URL)
		bob, err := login.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "gemini-browser-bob", Password: fixture.password}))
		if err != nil {
			test.Fatal("second synthetic owner login failed")
		}
		if _, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: asset.Msg.View.Asset.Id}, bob.Msg.SessionToken)); connect.CodeOf(err) != connect.CodeNotFound {
			test.Fatal("fixture crossed owner isolation")
		}
		alternate := configurationv1connect.NewConfigurationServiceClient(fixture.alternate.public.Client(), fixture.alternate.public.URL)
		if _, err := alternate.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, token)); connect.CodeOf(err) != connect.CodeUnauthenticated {
			test.Fatal("fixture accepted another realm's session")
		}
		request := configurationRequest(&configurationv1.ListAssetsRequest{}, token)
		request.Header().Set("Origin", fixture.alternate.public.URL)
		if _, err := client.ListAssets(test.Context(), request); connect.CodeOf(err) != connect.CodePermissionDenied {
			test.Fatal("fixture trusted a foreign explicit origin")
		}
	})
	test.Run("control-boundaries", geminiBrowserControlBoundaries)
	test.Run("private-readiness", geminiBrowserPrivateReadiness)
	test.Run("readiness-no-overwrite", geminiBrowserNoOverwrite)
	test.Run("gemini-receipts-cas-extras", geminiBrowserGeminiReceipts)
	test.Run("side-effect-tripwires", geminiBrowserSideEffectTripwires)
}

func geminiBrowserBasicWitness(test *testing.T) {
	fixture := newGeminiBrowserFixture(test)
	response, err := fixture.control.Client().Get(fixture.control.URL + "/state")
	if err != nil {
		test.Fatal("unauthorized control probe failed")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("Cache-Control") != "no-store" {
		test.Fatal("fixture control accepted an unauthenticated observer")
	}
	session, err := fixture.realm.authentication.Login(test.Context(), "gemini-browser-alice", []byte(fixture.password))
	if err != nil {
		test.Fatal("synthetic browser login failed")
	}
	client := configurationv1connect.NewConfigurationServiceClient(fixture.realm.public.Client(), fixture.realm.public.URL)
	created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiBrowserDraft("Fixture witness")}, session.Reveal()))
	if err != nil || created.Msg.View.Asset.Revision != 1 {
		test.Fatal("real fixture HTTP create failed")
	}
	assets, err := fixture.realm.domain.List(test.Context(), fixture.realm.owners["gemini-browser-alice"])
	if err != nil || len(assets) != 1 {
		test.Fatal("real SQLite commit witness is absent")
	}
	request, _ := http.NewRequestWithContext(test.Context(), http.MethodGet, fixture.control.URL+"/state", nil)
	request.Header.Set("X-Fixture-Capability", fixture.capability)
	stateResponse, err := fixture.control.Client().Do(request)
	if err != nil {
		test.Fatal("private receipt request failed")
	}
	encoded, _ := io.ReadAll(stateResponse.Body)
	_ = stateResponse.Body.Close()
	var receipt struct {
		Commits int `json:"commits"`
	}
	if json.Unmarshal(encoded, &receipt) != nil || receipt.Commits != 1 {
		test.Fatal("fixture commit counter did not witness the successful real Store commit")
	}
	fixture.Close()
	select {
	case <-fixture.done:
	default:
		test.Fatal("normal fixture close did not join")
	}
}

func geminiBrowserClient(test *testing.T, realm *geminiBrowserRealm, password string) (configurationv1connect.ConfigurationServiceClient, string) {
	test.Helper()
	authentication := authv1connect.NewAuthServiceClient(realm.public.Client(), realm.public.URL)
	login, err := authentication.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "gemini-browser-alice", Password: password}))
	if err != nil {
		test.Fatal("real HTTP fixture login failed")
	}
	return configurationv1connect.NewConfigurationServiceClient(realm.public.Client(), realm.public.URL), login.Msg.SessionToken
}

func geminiBrowserCommand(test *testing.T, fixture *geminiBrowserFixture, action, id string, expectedStatus int) {
	test.Helper()
	encoded, err := json.Marshal(struct {
		Action  string `json:"action"`
		AssetID string `json:"assetID,omitzero"`
	}{action, id})
	if err != nil {
		test.Fatal("private command encoding failed")
	}
	defer clear(encoded)
	request, _ := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.control.URL+"/command", bytes.NewReader(encoded))
	request.Header.Set("X-Fixture-Capability", fixture.capability)
	response, err := fixture.control.Client().Do(request)
	if err != nil {
		test.Fatal("private fixture command transport failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || response.StatusCode != expectedStatus || response.Header.Get("Cache-Control") != "no-store" {
		test.Fatal("private fixture command status or cache boundary failed")
	}
	if expectedStatus == http.StatusOK && !bytes.Equal(data, []byte(`{"ok":true}`)) {
		test.Fatal("fixture command acknowledgment was incomplete")
	}
}

func geminiBrowserWait(test *testing.T, fixture *geminiBrowserFixture, predicate func(geminiBrowserCounters) bool) geminiBrowserCounters {
	test.Helper()
	for {
		snapshot, changed := fixture.faults.snapshot()
		if predicate(snapshot) {
			return snapshot
		}
		select {
		case <-changed:
		case <-test.Context().Done():
			test.Fatal("fixture state barrier did not complete")
		}
	}
}

func geminiBrowserPhase(test *testing.T, stage, outcome string) {
	test.Helper()
	fixture := newGeminiBrowserFixture(test)
	client, token := geminiBrowserClient(test, fixture.realm, fixture.password)
	action := "hold-next-write"
	if stage == "before" {
		action = "hold-before-next-write"
	}
	geminiBrowserCommand(test, fixture, action, "", http.StatusOK)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	result := make(chan error, 1)
	var worker sync.WaitGroup
	worker.Go(func() {
		_, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiBrowserDraft("Held fixture")}, token))
		result <- err
	})
	before := geminiBrowserWait(test, fixture, func(state geminiBrowserCounters) bool { return state.Held })
	want := 1
	if stage == "before" {
		want = 0
	}
	if before.Stage != stage || before.Commits != want || before.StoreCalls != want || before.Writes != 1 {
		test.Fatal("fixture gate was on the wrong side of the actual commit")
	}
	assets, err := fixture.realm.domain.List(test.Context(), fixture.realm.owners["gemini-browser-alice"])
	if err != nil || len(assets) != want {
		test.Fatal("gate counters disagree with real persistent rows")
	}
	geminiBrowserCommand(test, fixture, "drop-next-write", "", http.StatusConflict)
	switch outcome {
	case "release":
		geminiBrowserCommand(test, fixture, "release", "", http.StatusOK)
	case "cancel":
		cancel()
	case "stop":
		geminiBrowserCommand(test, fixture, "stop", "", http.StatusOK)
	}
	worker.Wait()
	resultErr := <-result
	if outcome == "release" && resultErr != nil || outcome != "release" && resultErr == nil {
		test.Fatal("fixture hold/release/cancel outcome was incorrect")
	}
	if outcome == "cancel" && connect.CodeOf(resultErr) != connect.CodeCanceled {
		test.Fatal("caller abort lost its canceled classification")
	}
	geminiBrowserWait(test, fixture, func(state geminiBrowserCounters) bool { return state.Active == 0 && !state.Held })
	fixture.Close()
	after, _ := fixture.faults.snapshot()
	if outcome == "release" {
		want = 1
	}
	if after.Commits != want || after.StoreCalls != want {
		test.Fatal("close replayed a write or erased a committed outcome")
	}
	select {
	case <-fixture.done:
	default:
		test.Fatal("fixture close returned without joining the actual worker")
	}
}

func geminiBrowserControlBoundaries(test *testing.T) {
	test.Helper()
	fixture := newGeminiBrowserFixture(test)
	for _, scenario := range []struct {
		method, path, body, origin string
		status                     int
	}{
		{http.MethodGet, "/state?extra=1", "", "", http.StatusForbidden},
		{http.MethodGet, "/state", "", fixture.realm.public.URL, http.StatusForbidden},
		{http.MethodGet, "/unknown", "", "", http.StatusNotFound},
		{http.MethodPost, "/command", `{"action":"stop","unknown":true}`, "", http.StatusBadRequest},
		{http.MethodPost, "/command", `{"action":"stop","action":"release"}`, "", http.StatusBadRequest},
		{http.MethodPost, "/command", strings.Repeat("x", 4097), "", http.StatusBadRequest},
		{http.MethodPost, "/command", `{"action":"hold-next-write","assetID":"not-allowed"}`, "", http.StatusBadRequest},
	} {
		request, _ := http.NewRequestWithContext(test.Context(), scenario.method, fixture.control.URL+scenario.path, strings.NewReader(scenario.body))
		request.Header.Set("X-Fixture-Capability", fixture.capability)
		request.Header.Set("Origin", scenario.origin)
		response, err := fixture.control.Client().Do(request)
		if err != nil {
			test.Fatal("control validation probe failed")
		}
		_ = response.Body.Close()
		if response.StatusCode != scenario.status || response.Header.Get("Cache-Control") != "no-store" {
			test.Fatal("control boundary accepted an invalid request")
		}
	}
	geminiBrowserCommand(test, fixture, "stop", "", http.StatusOK)
	fixture.Close()
}

func geminiBrowserPrivateReadiness(test *testing.T) {
	directory := test.TempDir()
	if os.Chmod(directory, 0755) != nil {
		test.Fatal("fixture permission setup failed")
	}
	if root, err := geminiBrowserOpenRoot(directory); err == nil {
		_ = root.Close()
		test.Fatal("fixture accepted a non-private directory")
	}
	if os.Chmod(directory, 0700) != nil {
		test.Fatal("fixture permission setup failed")
	}
	link := filepath.Join(test.TempDir(), "link")
	if os.Symlink(directory, link) != nil {
		test.Fatal("fixture symlink probe setup failed")
	}
	if root, err := geminiBrowserOpenRoot(link); err == nil {
		_ = root.Close()
		test.Fatal("fixture accepted a symlink control directory")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal("private fixture directory rejected")
	}
	fixture := newGeminiBrowserFixtureWithRoot(test, root)
	for _, name := range []string{"public-ready.json", "ready.json"} {
		info, err := root.Stat(name)
		if err != nil || info.Mode().Perm() != 0600 {
			test.Fatal("fixture readiness permissions were not private")
		}
	}
	public, err := root.ReadFile("public-ready.json")
	if err != nil || bytes.Contains(public, []byte(fixture.capability)) || bytes.Contains(public, []byte(fixture.password)) || bytes.Contains(public, fixture.expectedKey) {
		test.Fatal("public readiness exposed private fixture material")
	}
	var readiness struct {
		URL             string `json:"url"`
		ControlURL      string `json:"controlURL"`
		AlternateURL    string `json:"alternateURL"`
		Ready           bool   `json:"ready"`
		LifetimeSeconds int    `json:"lifetimeSeconds"`
	}
	if json.Unmarshal(public, &readiness) != nil || !readiness.Ready || readiness.LifetimeSeconds != 2700 || readiness.URL != fixture.realm.public.URL || readiness.ControlURL != fixture.control.URL || readiness.AlternateURL != fixture.alternate.public.URL {
		test.Fatal("public fixture readiness did not describe the actual listeners")
	}
	if bytes.Contains(public, []byte("bootstrap")) || bytes.Contains(public, []byte("capability")) || bytes.Contains(public, []byte("password")) {
		test.Fatal("public readiness contained private bootstrap fields")
	}
	private, err := root.ReadFile("ready.json")
	defer clear(private)
	var bootstrap struct {
		URL        string                 `json:"url"`
		ControlURL string                 `json:"controlURL"`
		Capability string                 `json:"capability"`
		Bootstrap  geminiBrowserBootstrap `json:"bootstrap"`
	}
	if err != nil || json.Unmarshal(private, &bootstrap, json.RejectUnknownMembers(true)) != nil || bootstrap.URL != fixture.realm.public.URL || bootstrap.ControlURL != fixture.control.URL || bootstrap.Capability != fixture.capability || len(bootstrap.Bootstrap.Owners) != 2 || bootstrap.Bootstrap.Owners[0].Username != "gemini-browser-alice" || bootstrap.Bootstrap.Owners[1].Username != "gemini-browser-bob" || bootstrap.Bootstrap.Owners[0].Password != fixture.password || bootstrap.Bootstrap.Owners[1].Password != fixture.password || bootstrap.Bootstrap.APIKey != string(fixture.expectedKey) {
		test.Fatal("private bootstrap did not match the synthetic fixture")
	}
	fixture.Close()
	for _, name := range []string{"public-ready.json", "ready.json"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("private readiness survived actual fixture shutdown")
		}
	}
	stopped, err := os.ReadFile(filepath.Join(directory, "stopped.json"))
	var receipt struct {
		Closed          bool `json:"closed"`
		SideEffectsZero bool `json:"sideEffectsZero"`
		Active          int  `json:"active"`
		Held            bool `json:"held"`
	}
	if err != nil || json.Unmarshal(stopped, &receipt) != nil || !receipt.Closed || !receipt.SideEffectsZero || receipt.Active != 0 || receipt.Held || !fixture.closeOK {
		test.Fatal("fixture stop receipt did not witness complete drain and close")
	}
}

func geminiBrowserNoOverwrite(test *testing.T) {
	directory := test.TempDir()
	if os.Chmod(directory, 0700) != nil {
		test.Fatal("private fixture setup failed")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal("private fixture root unavailable")
	}
	fixture := newGeminiBrowserFixture(test)
	fixture.readyRoot = root
	path := filepath.Join(directory, "ready.json")
	original := []byte("preexisting-private-file")
	if os.WriteFile(path, original, 0600) != nil || fixture.writeReadiness() == nil {
		test.Fatal("fixture overwrote an existing readiness file")
	}
	fixture.Close()
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, original) || !fixture.closeOK {
		test.Fatal("fixture cleanup changed an unowned readiness file")
	}
}

func geminiBrowserGeminiReceipts(test *testing.T) {
	fixture := newGeminiBrowserFixture(test)
	owner := fixture.realm.owners["gemini-browser-alice"]
	secret := configuration.NewSecret(fixture.expectedKey)
	defer secret.Clear()
	asset, err := fixture.realm.domain.Create(test.Context(), owner, configuration.Draft{Name: "Synthetic Gemini", GeminiProvider: geminiBrowserFullProvider()}, []configuration.SecretMutation{{Slot: configuration.GeminiAPIKeySlot, Action: configuration.Replace, Replacement: secret}})
	if err != nil {
		test.Fatal("real Gemini domain fixture setup failed")
	}
	receipt, err := fixture.receipt()
	if err != nil || len(receipt.Alice) != 1 || len(receipt.Bob) != 0 || len(receipt.Alternate["gemini-browser-alice"]) != 0 || len(receipt.Alternate["gemini-browser-bob"]) != 0 {
		test.Fatal("fixture receipt mixed owners or realms")
	}
	view := receipt.Alice[0]
	if view.ID != asset.Metadata.ID || view.Revision != 1 || view.Agent != "gemini" || view.Auth != "api-key" || !view.ExpectedProviderEqual || !view.ExpectedSecretEqual || !view.SlotPresent || !view.EnvModelPresent || !view.SettingsModelPresent || view.Extras || view.SecretEmpty || !receipt.SideEffectsZero || receipt.Commits != 1 || receipt.ControlCommits != 1 || receipt.Writes != 0 {
		test.Fatal("Gemini receipt did not reflect authoritative typed data and real commit")
	}
	encoded, err := json.Marshal(receipt)
	if err != nil || bytes.Contains(encoded, fixture.expectedKey) || bytes.Contains(encoded, []byte(fixture.password)) || bytes.Contains(encoded, []byte(fixture.capability)) {
		test.Fatal("safe fixture receipt exposed private material")
	}
	geminiBrowserCommand(test, fixture, "mutate", asset.Metadata.ID, http.StatusOK)
	if _, err := fixture.realm.domain.Update(test.Context(), owner, asset.Metadata.ID, 1, configuration.Draft{Name: "Stale write", GeminiProvider: geminiBrowserFullProvider()}, nil); !errors.Is(err, configuration.ErrConflict) {
		test.Fatal("controlled external commit did not create a real CAS conflict")
	}
	geminiBrowserCommand(test, fixture, "extras", asset.Metadata.ID, http.StatusOK)
	receipt, err = fixture.receipt()
	if err != nil || len(receipt.Alice) != 1 || receipt.Alice[0].Revision != 3 || receipt.Alice[0].Name != "Externally revised" || !receipt.Alice[0].Extras || !receipt.Alice[0].ExpectedSecretEqual || receipt.Commits != 3 || receipt.ControlCommits != 3 || receipt.StoreCalls != 3 || receipt.Writes != 0 || !receipt.SideEffectsZero {
		test.Fatal("controlled CAS or unknown extras receipt was inaccurate")
	}
	geminiBrowserCommand(test, fixture, "mutate", asset.Metadata.ID, http.StatusBadRequest)
	geminiBrowserCommand(test, fixture, "extras", strings.Repeat("x", 26), http.StatusBadRequest)
	geminiBrowserCommand(test, fixture, "stop", "", http.StatusOK)
	fixture.Close()
}

func geminiBrowserSideEffectTripwires(test *testing.T) {
	fixture := newGeminiBrowserFixture(test)
	if !fixture.sideEffectsZero() {
		test.Fatal("fixture baseline was not clean")
	}
	path := filepath.Join(fixture.home, ".gemini/.env")
	if os.WriteFile(path, []byte("unexpected synthetic change"), 0600) != nil || fixture.sideEffectsZero() {
		test.Fatal("fixture did not detect a changed native baseline")
	}
	if os.WriteFile(path, fixture.baseline[".gemini/.env"], 0600) != nil || !fixture.sideEffectsZero() {
		test.Fatal("synthetic baseline restoration failed")
	}
	var reached atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		reached.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer foreign.Close()
	request, _ := http.NewRequestWithContext(test.Context(), http.MethodGet, foreign.URL, nil)
	response, err := http.DefaultClient.Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || reached.Load() != 0 || fixture.trap.blocked.Load() != 1 || fixture.sideEffectsZero() {
		test.Fatal("fixture outbound-network tripwire failed")
	}
}

func geminiBrowserDraft(name string) *configurationv1.Draft {
	return &configurationv1.Draft{Name: name, Payload: &configurationv1.Draft_GeminiProvider{GeminiProvider: &configurationv1.GeminiProvider{
		AuthMode: configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_API_KEY, Endpoint: new("https://provider.example/v1"), KeyTransport: new(configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_BEARER), EnvModel: new("model-test"), SettingsModel: new("settings-model-test"),
	}}}
}

func geminiBrowserRPCWitness(test *testing.T, codec string) {
	fixture := newGeminiBrowserFixture(test)
	_, token := geminiBrowserClient(test, fixture.realm, fixture.password)
	var options []connect.ClientOption
	if codec == "json" {
		options = append(options, connect.WithProtoJSON())
	}
	client := configurationv1connect.NewConfigurationServiceClient(fixture.realm.public.Client(), fixture.realm.public.URL, options...)
	created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: geminiBrowserDraft("RPC Gemini"), Secrets: []*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: string(fixture.expectedKey)}}}}, token))
	if err != nil || created.Msg.View.Asset.AgentId != "gemini" || created.Msg.View.Draft.GetGeminiProvider() == nil || len(created.Msg.View.Secrets) != 1 || !created.Msg.View.Secrets[0].Present {
		test.Fatal("real Gemini fixture HTTP create did not preserve the typed branch or slot")
	}
	id := created.Msg.View.Asset.Id
	listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, token))
	if err != nil || len(listed.Msg.Assets) != 1 || listed.Msg.Assets[0].Id != id {
		test.Fatal("real Gemini fixture HTTP list failed")
	}
	if _, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: id}, token)); err != nil {
		test.Fatal("real Gemini fixture HTTP metadata get failed")
	}
	read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, token))
	if err != nil || read.Msg.View.Draft.GetGeminiProvider() == nil || read.Msg.View.Asset.Revision != 1 {
		test.Fatal("real Gemini fixture HTTP draft read failed")
	}
	updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: geminiBrowserDraft("RPC Gemini updated"), Secrets: []*configurationv1.SecretMutation{{SlotId: "gemini.api-key", Action: &configurationv1.SecretMutation_Keep{Keep: &configurationv1.KeepSecret{}}}}}, token))
	if err != nil || updated.Msg.View.Asset.Revision != 2 || !updated.Msg.View.Secrets[0].Present {
		test.Fatal("real Gemini fixture HTTP KEEP failed")
	}
	copied, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 2, Name: "RPC copy"}, token))
	if err != nil || copied.Msg.View.Asset.Id == id || copied.Msg.View.Asset.Revision != 1 || copied.Msg.View.Secrets[0].Present || copied.Msg.View.Draft.GetGeminiProvider() == nil {
		test.Fatal("real Gemini fixture HTTP copy leaked a key or lost its typed branch")
	}
	receipt, err := fixture.receipt()
	if err != nil || len(receipt.Alice) != 2 {
		test.Fatal("real Gemini fixture Store receipts unavailable")
	}
	for _, asset := range receipt.Alice {
		if !asset.ExpectedProviderEqual || asset.ID == id && (!asset.ExpectedSecretEqual || !asset.SlotPresent) || asset.ID != id && (!asset.SecretEmpty || asset.SlotPresent) {
			test.Fatal("real Gemini HTTP save/copy disagreed with the authoritative Store receipt")
		}
	}
	if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: copied.Msg.View.Asset.Id, ExpectedRevision: 1}, token)); err != nil {
		test.Fatal("real Gemini fixture HTTP delete failed")
	}
	geminiBrowserWait(test, fixture, func(state geminiBrowserCounters) bool { return state.Active == 0 })
	receipt, err = fixture.receipt()
	if err != nil || receipt.Writes != 4 || receipt.StoreCalls != 4 || receipt.Commits != 4 || receipt.ControlCommits != 0 || len(receipt.Alice) != 1 || !receipt.SideEffectsZero || receipt.ProviderRequests != 0 {
		test.Fatal("seven real Gemini RPCs disagreed with mutation counters or side-effect guards")
	}
}
