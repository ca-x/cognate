package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/web"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type openCodeBrowserFixture struct {
	ctx            context.Context
	cancel         context.CancelFunc
	store          *state.Store
	authentication *auth.Service
	domain         *configuration.Service
	public         *httptest.Server
	control        *httptest.Server
	faults         *geminiBrowserFaults
	password       string
	capability     string
	owner          string
	root           *os.Root
	home           string
	baseline       []byte
	databasePath   string
	once           sync.Once
	done           chan struct{}
	closed         bool
	copyMutex      sync.Mutex
	corruptCopy    bool
	routes         map[string]int
	jsonWrites     int
	otherWrites    int
	corruptReplies int
}

func newOpenCodeBrowserFixture(test *testing.T, root *os.Root) *openCodeBrowserFixture {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 240*time.Second)
	fixture := &openCodeBrowserFixture{ctx: ctx, cancel: cancel, root: root, password: rand.Text(), capability: rand.Text(), done: make(chan struct{}), faults: &geminiBrowserFaults{changed: make(chan struct{})}, routes: make(map[string]int)}
	test.Cleanup(fixture.Close)
	directory := test.TempDir()
	if os.Chmod(directory, 0700) != nil {
		test.Fatal("private database fixture unavailable")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		test.Fatal("synthetic key unavailable")
	}
	defer clear(key)
	var err error
	fixture.store, err = state.Open(ctx, filepath.Join(directory, "fixture.db"), key)
	fixture.databasePath = filepath.Join(directory, "fixture.db")
	if err != nil {
		test.Fatal("physical store unavailable")
	}
	fixture.authentication, err = auth.NewService(fixture.store, key)
	if err != nil {
		test.Fatal("real authentication unavailable")
	}
	for _, name := range []string{"opencode-browser-alice", "opencode-browser-bob"} {
		owner, registerErr := fixture.authentication.Register(ctx, name, []byte(fixture.password))
		if registerErr != nil {
			test.Fatal("synthetic registration failed")
		}
		if name == "opencode-browser-alice" {
			fixture.owner = owner
		}
	}
	fixture.domain = configuration.New(geminiBrowserCommitStore{Store: fixture.store, faults: fixture.faults})
	fixture.public = httptest.NewUnstartedServer(nil)
	origin := "https://" + fixture.public.Listener.Addr().String()
	fixture.public.Config.Handler = fixture.copyResponseGuard(fixture.faults.wrap(ctx, NewWithOptions(fixture.authentication, Options{PublicOrigin: origin, Configuration: fixture.domain, WebUI: web.Handler()})))
	fixture.public.Config.ReadHeaderTimeout = 5 * time.Second
	fixture.public.StartTLS()
	fixture.home = test.TempDir()
	test.Setenv("HOME", fixture.home)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(fixture.home, "config"))
	test.Setenv("XDG_DATA_HOME", filepath.Join(fixture.home, "data"))
	test.Setenv("XDG_STATE_HOME", filepath.Join(fixture.home, "state"))
	fixture.baseline = []byte("synthetic foreign OpenCode configuration, must remain unchanged")
	for _, path := range []string{"opencode.json", "config/opencode/opencode.json", "data/opencode/auth.json"} {
		if os.MkdirAll(filepath.Dir(filepath.Join(fixture.home, path)), 0700) != nil || os.WriteFile(filepath.Join(fixture.home, path), fixture.baseline, 0600) != nil {
			test.Fatal("native tripwire unavailable")
		}
	}
	fixture.control = httptest.NewUnstartedServer(http.HandlerFunc(fixture.serveControl))
	fixture.control.Config.ReadHeaderTimeout = 5 * time.Second
	fixture.control.Config.ReadTimeout = 5 * time.Second
	fixture.control.Config.WriteTimeout = 5 * time.Second
	fixture.control.Start()
	if root != nil {
		if fixture.writePrivate("expectations.json", openCodeBrowserExpectations(test)) != nil {
			test.Fatal("private boundary expectations unavailable")
		}
		public, _ := json.Marshal(map[string]any{"url": fixture.public.URL, "controlURL": fixture.control.URL, "ready": true})
		private, _ := json.Marshal(map[string]any{"url": fixture.public.URL, "controlURL": fixture.control.URL, "capability": fixture.capability, "username": "opencode-browser-alice", "otherUsername": "opencode-browser-bob", "password": fixture.password, "replacement": "SYNTHETIC-OC-WEB-REPLACEMENT"})
		defer clear(private)
		if fixture.writePrivate("public-ready.json", public) != nil || fixture.writePrivate("ready.json", private) != nil {
			test.Fatal("private readiness unavailable")
		}
	}
	return fixture
}

func (fixture *openCodeBrowserFixture) writePrivate(name string, data []byte) error {
	file, err := fixture.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Close())
}

func (fixture *openCodeBrowserFixture) copyResponseGuard(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.copyMutex.Lock()
		if strings.HasPrefix(request.URL.Path, "/skillhub.configuration.v1.ConfigurationService/") {
			fixture.routes[request.URL.Path]++
			if strings.HasSuffix(request.URL.Path, "CreateAsset") || strings.HasSuffix(request.URL.Path, "UpdateAsset") || strings.HasSuffix(request.URL.Path, "CopyAsset") || strings.HasSuffix(request.URL.Path, "DeleteAsset") {
				if request.Header.Get("Content-Type") == "application/json" {
					fixture.jsonWrites++
				} else {
					fixture.otherWrites++
				}
			}
		}
		corrupt := fixture.corruptCopy && request.URL.Path == configurationv1connect.ConfigurationServiceCopyAssetProcedure
		if corrupt {
			fixture.corruptCopy = false
		}
		fixture.copyMutex.Unlock()
		if !corrupt {
			handler.ServeHTTP(writer, request)
			return
		}
		recorder := httptest.NewRecorder()
		request = request.Clone(request.Context())
		request.Header.Del("Accept-Encoding")
		handler.ServeHTTP(recorder, request)
		defer clear(recorder.Body.Bytes())
		if recorder.Code != http.StatusOK {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		message := new(configurationv1.CopyAssetResponse)
		if protojson.Unmarshal(recorder.Body.Bytes(), message) != nil || message.View == nil || len(message.View.Secrets) == 0 {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		for _, slot := range message.View.Secrets {
			slot.Present = true
		}
		message.View.CredentialReadiness = configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE
		data, err := protojson.Marshal(message)
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		for name, values := range recorder.Header() {
			if !strings.EqualFold(name, "Content-Length") {
				writer.Header()[name] = values
			}
		}
		fixture.copyMutex.Lock()
		fixture.corruptReplies++
		fixture.copyMutex.Unlock()
		_, _ = writer.Write(data)
	})
}

func (fixture *openCodeBrowserFixture) sideEffectsZero() bool {
	files := 0
	err := filepath.WalkDir(fixture.home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, fixture.baseline) {
			return errors.New("native tripwire changed")
		}
		files++
		return nil
	})
	return err == nil && files == 3
}

func (fixture *openCodeBrowserFixture) Close() {
	fixture.once.Do(func() {
		fixture.cancel()
		fixture.faults.releaseHeld()
		if fixture.public != nil {
			fixture.public.CloseClientConnections()
			fixture.public.Close()
		}
		if fixture.control != nil {
			fixture.control.CloseClientConnections()
			fixture.control.Close()
		}
		fixture.faults.workers.Wait()
		fixture.closed = fixture.store != nil && fixture.store.Close() == nil && fixture.sideEffectsZero()
		if fixture.root != nil {
			counters, _ := fixture.faults.snapshot()
			data, _ := json.Marshal(map[string]any{"closed": fixture.closed, "active": counters.Active, "commits": counters.Commits, "sideEffectsZero": fixture.sideEffectsZero()})
			if fixture.writePrivate("stopped.json", data) != nil {
				fixture.closed = false
			}
			_ = fixture.root.Close()
		}
		fixture.password = ""
		fixture.capability = ""
		close(fixture.done)
	})
}

func (fixture *openCodeBrowserFixture) serveControl(writer http.ResponseWriter, request *http.Request) {
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
		assets, err := fixture.store.ListConfigurationAssets(request.Context(), fixture.owner)
		if err != nil {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		views := make([]map[string]any, 0, len(assets))
		for _, asset := range assets {
			view, err := fixture.domain.Edit(request.Context(), fixture.owner, asset.ID)
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			entry := map[string]any{"id": asset.ID, "name": asset.Name, "revision": asset.Revision, "agent": asset.AgentID, "slots": view.Secrets, "readiness": view.Readiness, "extras": view.HasPreservedExtras}
			statuses := make([]map[string]any, 0, len(view.Secrets))
			for _, slot := range view.Secrets {
				statuses = append(statuses, map[string]any{"slotId": string(slot.Slot), "present": slot.Present})
			}
			entry["slots"] = statuses
			_, payload, payloadErr := fixture.store.ReadConfigurationAssetVersion(request.Context(), fixture.owner, asset.ID)
			if payloadErr != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			raw := payload.Reveal()
			var envelope struct {
				Secrets map[string]string `json:"secrets"`
			}
			parseErr := json.Unmarshal(raw, &envelope)
			clear(raw)
			if parseErr != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			entry["expectedSecretEqual"] = envelope.Secrets["opencode.api-key"] == "SYNTHETIC-OC-WEB-REPLACEMENT"
			if view.GrokBuildProvider != nil {
				entry["grokProvider"] = view.GrokBuildProvider
				entry["expectedGrokSecretEqual"] = envelope.Secrets["api_key"] == "SYNTHETIC-OC-WEB-REPLACEMENT"
			}
			clear(envelope.Secrets)
			if view.OpenCodeProvider != nil {
				entry["format"] = view.OpenCodeProvider.Format
				entry["providerID"] = view.OpenCodeProvider.ProviderID
				entry["treeEqual"] = reflect.DeepEqual(*view.OpenCodeProvider, openCodeBrowserFullProvider(view.OpenCodeProvider.Format))
				if view.OpenCodeProvider.Format == "v2" {
					expected := openCodeBrowserFullProvider("v2")
					model := (*expected.V2.Models)["alpha"]
					model.Cost = &agentconfig.OpenCodeCosts{Single: &agentconfig.OpenCodeCost{Input: new("0"), Output: new("0.25")}}
					(*expected.V2.Models)["alpha"] = model
					entry["singleCostEqual"] = reflect.DeepEqual(*view.OpenCodeProvider, expected)
				}
			}
			views = append(views, entry)
		}
		counters, _ := fixture.faults.snapshot()
		plaintextAbsent := true
		for _, suffix := range []string{"", "-wal"} {
			data, readErr := os.ReadFile(fixture.databasePath + suffix)
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				plaintextAbsent = false
			}
			if bytes.Contains(data, []byte("SYNTHETIC-OC-WEB-REPLACEMENT")) {
				plaintextAbsent = false
			}
			clear(data)
		}
		fixture.copyMutex.Lock()
		routes, _ := json.Marshal(fixture.routes)
		jsonWrites, otherWrites, corruptReplies := fixture.jsonWrites, fixture.otherWrites, fixture.corruptReplies
		fixture.copyMutex.Unlock()
		data, err := json.Marshal(map[string]any{"assets": views, "counters": counters, "sideEffectsZero": fixture.sideEffectsZero(), "plaintextAbsent": plaintextAbsent, "routes": jsontext.Value(routes), "jsonWrites": jsonWrites, "otherWrites": otherWrites, "corruptReplies": corruptReplies})
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = writer.Write(data)
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/command" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, 4097))
	defer clear(data)
	var command struct {
		Action  string `json:"action"`
		AssetID string `json:"assetID"`
	}
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &command, json.RejectUnknownMembers(true)) != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	stop := false
	switch command.Action {
	case "stop":
		if command.AssetID != "" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		stop = true
	case "release":
		fixture.faults.releaseHeld()
	case "corrupt-copy":
		fixture.copyMutex.Lock()
		fixture.corruptCopy = true
		fixture.copyMutex.Unlock()
	case "hold-before", "drop-after-commit":
		if command.AssetID != "" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		fixture.faults.mutex.Lock()
		defer fixture.faults.mutex.Unlock()
		if fixture.faults.busy || fixture.faults.next != "" {
			writer.WriteHeader(http.StatusConflict)
			return
		}
		fixture.faults.next = "hold-before"
		if command.Action == "drop-after-commit" {
			fixture.faults.next = "drop"
		}
		fixture.faults.notify()
	case "external-update":
		view, err := fixture.domain.Edit(request.Context(), fixture.owner, command.AssetID)
		if err != nil || view.OpenCodeProvider == nil && view.GrokBuildProvider == nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_, err = fixture.domain.Update(request.Context(), fixture.owner, command.AssetID, view.Metadata.Revision, configuration.Draft{Name: view.Metadata.Name + " external", OpenCodeProvider: view.OpenCodeProvider, GrokBuildProvider: view.GrokBuildProvider}, nil)
		if err != nil {
			writer.WriteHeader(http.StatusConflict)
			return
		}
	case "seed-preserved-extras":
		metadata, payload, err := fixture.store.ReadConfigurationAssetVersion(request.Context(), fixture.owner, command.AssetID)
		if err != nil || metadata.AgentID != "opencode" && metadata.AgentID != "grokbuild" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		raw := payload.Reveal()
		defer clear(raw)
		if len(raw) == 0 || raw[len(raw)-1] != '}' {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		merged := append(bytes.Clone(raw[:len(raw)-1]), []byte(`,"future":null}`)...)
		defer clear(merged)
		_, err = fixture.store.UpdateConfigurationAsset(request.Context(), fixture.owner, metadata.ID, metadata.Revision, state.ConfigurationAssetDraft{AgentID: metadata.AgentID, Kind: metadata.Kind, Name: metadata.Name, SchemaVersion: metadata.SchemaVersion, Payload: state.NewConfigurationPayload(merged)})
		if err != nil {
			writer.WriteHeader(http.StatusConflict)
			return
		}
	case "seed-boundaries":
		deep := agentconfig.OpenCodeValue{Null: new(true)}
		for range 11 {
			deep = agentconfig.OpenCodeValue{Array: new([]agentconfig.OpenCodeValue{deep})}
		}
		models := make(map[string]agentconfig.OpenCodeV1Model)
		for index := range 64 {
			models[fmt.Sprintf("m%02d", index)] = agentconfig.OpenCodeV1Model{}
		}
		large := make(map[string]agentconfig.OpenCodeValue)
		for index := range 250 {
			large[fmt.Sprintf("k%03d", index)+strings.Repeat("x", 236)] = agentconfig.OpenCodeValue{Null: new(true)}
		}
		headers := make(map[string]string)
		for index := range 7 {
			headers[fmt.Sprintf("X-Large-%d", index)] = fmt.Sprintf("opencode.value.%02d", index+1)
		}
		providers := map[string]agentconfig.OpenCodeProvider{
			"Boundary depth12":  {ProviderID: "boundary-depth", Format: "v1", V1: &agentconfig.OpenCodeV1{Models: new(map[string]agentconfig.OpenCodeV1Model{"model": {Thinking: &deep}})}},
			"Boundary models64": {ProviderID: "boundary-models", Format: "v1", V1: &agentconfig.OpenCodeV1{Models: &models}},
			"Boundary wire96":   {ProviderID: "boundary-wire", Format: "v2", V2: &agentconfig.OpenCodeV2{Body: &large, Headers: &headers}},
		}
		for name, provider := range providers {
			if _, err := fixture.domain.Create(request.Context(), fixture.owner, configuration.Draft{Name: name, OpenCodeProvider: &provider}, nil); err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
	default:
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	_, _ = writer.Write([]byte(`{"ok":true}`))
	if stop {
		_ = http.NewResponseController(writer).Flush()
		fixture.cancel()
	}
}

func openCodeBrowserFullProvider(format string) agentconfig.OpenCodeProvider {
	boolean := func(value bool) agentconfig.OpenCodeValue { return agentconfig.OpenCodeValue{Bool: new(value)} }
	number := func(value string) agentconfig.OpenCodeValue { return agentconfig.OpenCodeValue{Number: new(value)} }
	reference := func(value string) agentconfig.OpenCodeValue { return agentconfig.OpenCodeValue{Reference: new(value)} }
	null := agentconfig.OpenCodeValue{Null: new(true)}
	empty := func() map[string]agentconfig.OpenCodeValue { return map[string]agentconfig.OpenCodeValue{} }
	provider := agentconfig.OpenCodeProvider{ProviderID: "web-full", Format: format}
	if format == "v1" {
		provider.V1 = &agentconfig.OpenCodeV1{NPM: new("@ai-sdk/openai-compatible"), Name: new("Provider One"), Options: &agentconfig.OpenCodeV1Options{BaseURL: new("https://synthetic.invalid/custom"), APIKey: new("opencode.api-key"), Headers: new(map[string]string{"X-Trace": "opencode.value.01"}), Extensions: map[string]agentconfig.OpenCodeValue{"flag": null}}, Models: new(map[string]agentconfig.OpenCodeV1Model{"alpha": {Name: new("Alpha"), Limit: &agentconfig.OpenCodeLimit{Context: new("8192"), Output: new("0"), Extensions: map[string]agentconfig.OpenCodeValue{"flag": boolean(false)}}, Options: new(map[string]agentconfig.OpenCodeValue{"switch": boolean(false)}), Modalities: &agentconfig.OpenCodeModalities{Input: new([]string{}), Output: new([]string{"text"}), Extensions: map[string]agentconfig.OpenCodeValue{"number": number("0")}}, Thinking: new(boolean(false)), Cost: new(number("0")), Variants: new(map[string]agentconfig.OpenCodeValue{"fast": {Object: new(empty())}}), Extensions: map[string]agentconfig.OpenCodeValue{"proof": reference("opencode.value.02")}}}), Extensions: map[string]agentconfig.OpenCodeValue{"object": {Object: new(map[string]agentconfig.OpenCodeValue{"array": {Array: new([]agentconfig.OpenCodeValue{null, boolean(false), number("0.5"), reference("opencode.value.03")})}})}, "__proto__": number("0"), "value": boolean(true)}}
	} else {
		provider.V2 = &agentconfig.OpenCodeV2{Package: new("@ai-sdk/openai-compatible"), Name: new("Provider Two"), Canonical: new("vendor"), Env: new([]string{"API_TOKEN", "REGION"}), Settings: &agentconfig.OpenCodeSettings{Timeout: &agentconfig.OpenCodeTimeout{Disabled: new(false)}, ChunkTimeout: new("0"), Compaction: &agentconfig.OpenCodeCompaction{Type: "native", Extensions: map[string]agentconfig.OpenCodeValue{"flag": boolean(false)}}, Transport: new("websocket"), Extensions: empty()}, Headers: new(map[string]string{"X-API": "opencode.api-key"}), Body: new(map[string]agentconfig.OpenCodeValue{"apiKey": reference("opencode.value.01")}), Models: new(map[string]agentconfig.OpenCodeV2Model{"alpha": {ModelID: new("remote"), Family: new("family"), Name: new("Alpha"), Package: new("@ai-sdk/openai-compatible"), Disabled: new(false), Settings: &agentconfig.OpenCodeModelSettings{Compaction: &agentconfig.OpenCodeCompaction{Type: "summary", Extensions: empty()}, Extensions: empty()}, Headers: new(map[string]string{}), Body: new(empty()), Compatibility: &agentconfig.OpenCodeCompatibility{ReasoningField: new(reference("opencode.value.02")), RequireReasoning: new(false), MaxTokensField: new("max_tokens"), RequireFinishReason: new(false), RequireAssistantAfterTool: new(false), SupportsPromptCacheKey: new(false), Extensions: map[string]agentconfig.OpenCodeValue{"flag": null}}, Capabilities: &agentconfig.OpenCodeCapabilities{Tools: new(false), Input: new([]string{"text", "image"}), Output: new([]string{}), Extensions: map[string]agentconfig.OpenCodeValue{"number": number("0")}}, Limit: &agentconfig.OpenCodeLimit{Context: new("0"), Input: new("4096"), Output: new("0"), Extensions: map[string]agentconfig.OpenCodeValue{"flag": boolean(true)}}, Cost: &agentconfig.OpenCodeCosts{Tiers: new([]agentconfig.OpenCodeCost{{Input: new("0"), Output: new("0.25"), Cache: &agentconfig.OpenCodeCacheCost{Read: new("0"), Write: new("0"), Extensions: map[string]agentconfig.OpenCodeValue{"flag": null}}, Tier: &agentconfig.OpenCodeCostTier{Type: "context", Size: new("8192"), Extensions: empty()}, Extensions: map[string]agentconfig.OpenCodeValue{"flag": boolean(false)}}, {Input: new("1"), Output: new("2")}})}, Variants: new([]agentconfig.OpenCodeVariant{{ID: "fast", Settings: &agentconfig.OpenCodeModelSettings{Compaction: &agentconfig.OpenCodeCompaction{Type: "native"}, Extensions: empty()}, Headers: new(map[string]string{"X-Mode": "opencode.value.03"}), Body: new(map[string]agentconfig.OpenCodeValue{"flag": boolean(false)}), Extensions: empty()}, {ID: "slow"}}), Extensions: map[string]agentconfig.OpenCodeValue{"named": reference("opencode.value.04")}}}), Extensions: map[string]agentconfig.OpenCodeValue{"nullable": null}}
	}
	return provider
}

func TestOpenCodeBrowserFullVectors(test *testing.T) {
	for _, format := range []string{"v1", "v2"} {
		provider := openCodeBrowserFullProvider(format)
		if err := agentconfig.ValidateOpenCodeProvider(provider); err != nil {
			test.Fatal("independent full browser vector is not supported")
		}
	}
}

func openCodeBrowserExpectations(test *testing.T) []byte {
	test.Helper()
	providers := make(map[string]agentconfig.OpenCodeProvider)
	for _, format := range []string{"v1", "v2"} {
		providers["full-"+format] = openCodeBrowserFullProvider(format)
	}
	deep := agentconfig.OpenCodeValue{Null: new(true)}
	for range 11 {
		deep = agentconfig.OpenCodeValue{Array: new([]agentconfig.OpenCodeValue{deep})}
	}
	providers["depth12"] = agentconfig.OpenCodeProvider{ProviderID: "depth12", Format: "v1", V1: &agentconfig.OpenCodeV1{Models: new(map[string]agentconfig.OpenCodeV1Model{"model": {Thinking: &deep}})}}
	nodes := make([]agentconfig.OpenCodeValue, 2046)
	for index := range nodes {
		nodes[index] = agentconfig.OpenCodeValue{Null: new(true)}
	}
	providers["nodes2048"] = agentconfig.OpenCodeProvider{ProviderID: "nodes2048", Format: "v2", V2: &agentconfig.OpenCodeV2{Body: new(map[string]agentconfig.OpenCodeValue{"many": {Array: &nodes}})}}
	models := make(map[string]agentconfig.OpenCodeV1Model)
	for index := range 64 {
		models[fmt.Sprintf("m%02d", index)] = agentconfig.OpenCodeV1Model{}
	}
	providers["models64"] = agentconfig.OpenCodeProvider{ProviderID: "models64", Format: "v1", V1: &agentconfig.OpenCodeV1{Models: &models}}
	variants := make([]agentconfig.OpenCodeVariant, 128)
	for index := range variants {
		variants[index] = agentconfig.OpenCodeVariant{ID: fmt.Sprintf("v%03d", index)}
	}
	providers["variants128"] = agentconfig.OpenCodeProvider{ProviderID: "variants128", Format: "v2", V2: &agentconfig.OpenCodeV2{Models: new(map[string]agentconfig.OpenCodeV2Model{"model": {Variants: &variants}})}}
	headers := make(map[string]string)
	for index := range 64 {
		slot := "opencode.api-key"
		if index != 0 {
			slot = fmt.Sprintf("opencode.value.%02d", index)
		}
		headers[fmt.Sprintf("X-H-%02d", index)] = slot
	}
	providers["headers64-slots64"] = agentconfig.OpenCodeProvider{ProviderID: "headers64", Format: "v2", V2: &agentconfig.OpenCodeV2{Headers: &headers}}
	extensions := make(map[string]agentconfig.OpenCodeValue)
	for index := range 256 {
		extensions[fmt.Sprintf("e%03d", index)] = agentconfig.OpenCodeValue{Null: new(true)}
	}
	providers["extensions256"] = agentconfig.OpenCodeProvider{ProviderID: "extensions256", Format: "v2", V2: &agentconfig.OpenCodeV2{Extensions: extensions}}
	large := make(map[string]agentconfig.OpenCodeValue)
	for index := range 250 {
		large[fmt.Sprintf("k%03d", index)+strings.Repeat("x", 236)] = agentconfig.OpenCodeValue{Null: new(true)}
	}
	providers["large-native"] = agentconfig.OpenCodeProvider{ProviderID: "large", Format: "v2", V2: &agentconfig.OpenCodeV2{Body: &large}}
	result := make(map[string]any)
	for name, provider := range providers {
		if err := agentconfig.ValidateOpenCodeProvider(provider); err != nil {
			test.Fatalf("independent boundary vector rejected: %s", name)
		}
		native, err := json.Marshal(provider)
		wire, wireErr := protojson.Marshal(configurationOpenCodeProviderWire(&provider))
		if err != nil || wireErr != nil {
			test.Fatal("boundary serialization failed")
		}
		result[name] = map[string]any{"provider": jsontext.Value(wire), "nativeBytes": len(native)}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		test.Fatal("boundary expectations serialization failed")
	}
	return encoded
}

func TestOpenCodeBrowserBoundaryVectors(test *testing.T) {
	if len(openCodeBrowserExpectations(test)) == 0 {
		test.Fatal("missing boundary vectors")
	}
}

func TestOpenCodeAssetsBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_OPENCODE_BROWSER_DIR")
	if directory == "" {
		test.Skip("isolated browser entrypoint; mechanical tests execute separately")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal("browser directory not private")
	}
	fixture := newOpenCodeBrowserFixture(test, root)
	<-fixture.ctx.Done()
	fixture.Close()
	if !fixture.closed || errors.Is(fixture.ctx.Err(), context.DeadlineExceeded) {
		test.Fatal("fixture did not stop and join normally")
	}
}

func TestGrokBrowserObserver(test *testing.T) {
	fixture := newOpenCodeBrowserFixture(test, nil)
	session, err := fixture.authentication.Login(test.Context(), "opencode-browser-alice", []byte(fixture.password))
	if err != nil {
		test.Fatal("Grok observer login failed")
	}
	client := configurationv1connect.NewConfigurationServiceClient(fixture.public.Client(), fixture.public.URL, connect.WithProtoJSON())
	draft := &configurationv1.Draft{Name: "Grok observer", Payload: &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: &configurationv1.GrokBuildProvider{Mode: configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL, Model: &configurationv1.GrokBuildModel{Profile: "local", UpstreamModel: "remote", Endpoint: "https://example.invalid/v1", Name: "Model", ApiBackend: "responses", ContextWindow: 9007199254740991, EnvKey: new("GROK_REF")}}}}
	_, err = client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "grokbuild.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "SYNTHETIC-OC-WEB-REPLACEMENT"}}}}, session.Reveal()))
	if err != nil {
		test.Fatal("Grok observer write failed")
	}
	request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, fixture.control.URL+"/state", nil)
	if err != nil {
		test.Fatal("Grok observer request failed")
	}
	request.Header.Set("X-Fixture-Capability", fixture.capability)
	response, err := fixture.control.Client().Do(request)
	if err != nil {
		test.Fatal("Grok observer unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	var observed struct {
		Assets []struct {
			Provider *configuration.GrokBuildProvider `json:"grokProvider"`
			KeyEqual bool                             `json:"expectedGrokSecretEqual"`
		} `json:"assets"`
	}
	if err != nil || response.StatusCode != 200 || len(data) > 65536 || bytes.Contains(data, []byte("SYNTHETIC-OC-WEB-REPLACEMENT")) || json.Unmarshal(data, &observed) != nil || len(observed.Assets) != 1 || !observed.Assets[0].KeyEqual || observed.Assets[0].Provider == nil || observed.Assets[0].Provider.Model == nil || observed.Assets[0].Provider.Model.ContextWindow != 9007199254740991 {
		test.Fatal("Grok observer lost exact model/key truth or exposed plaintext")
	}
}

func TestOpenCodeBrowserHarnessGuardsAndDrain(test *testing.T) {
	fixture := newOpenCodeBrowserFixture(test, nil)
	for _, scenario := range []struct {
		path, capability, origin string
		status                   int
	}{{"/state", "", "", 401}, {"/state", fixture.capability, "https://foreign.invalid", 403}, {"/unknown", fixture.capability, "", 404}, {"/state?extra=1", fixture.capability, "", 403}} {
		request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, fixture.control.URL+scenario.path, nil)
		if err != nil {
			test.Fatal("fixture request setup failed")
		}
		request.Header.Set("X-Fixture-Capability", scenario.capability)
		request.Header.Set("Origin", scenario.origin)
		response, err := fixture.control.Client().Do(request)
		if err != nil {
			test.Fatal("fixture control unavailable")
		}
		_ = response.Body.Close()
		if response.StatusCode != scenario.status || response.Header.Get("Cache-Control") != "no-store" {
			test.Fatal("control boundary accepted invalid request")
		}
	}
	for _, body := range []string{`{"action":"unknown"}`, `{"action":"stop","extra":true}`, strings.Repeat("x", 4097)} {
		request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.control.URL+"/command", strings.NewReader(body))
		if err != nil {
			test.Fatal("invalid-control request setup failed")
		}
		request.Header.Set("X-Fixture-Capability", fixture.capability)
		response, err := fixture.control.Client().Do(request)
		if err != nil {
			test.Fatal("invalid-control request failed")
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			test.Fatal("control accepted invalid command or budget")
		}
	}
	fixture.Close()
	if !fixture.closed {
		test.Fatal("real SQL close or tripwire failed")
	}
	select {
	case <-fixture.done:
	default:
		test.Fatal("close returned without join")
	}
}

func TestOpenCodeBrowserCopyReplyFault(test *testing.T) {
	fixture := newOpenCodeBrowserFixture(test, nil)
	session, err := fixture.authentication.Login(test.Context(), "opencode-browser-alice", []byte(fixture.password))
	if err != nil {
		test.Fatal("real session unavailable")
	}
	provider := openCodeBrowserFullProvider("v1")
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, configuration.Draft{Name: "Source", OpenCodeProvider: &provider}, nil)
	if err != nil {
		test.Fatal("real source asset unavailable")
	}
	fixture.copyMutex.Lock()
	fixture.corruptCopy = true
	fixture.copyMutex.Unlock()
	client := configurationv1connect.NewConfigurationServiceClient(fixture.public.Client(), fixture.public.URL, connect.WithProtoJSON())
	request := configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Metadata.ID, ExpectedSourceRevision: 1, Name: "Reply fault copy"}, session.Reveal())
	request.Header().Set("Accept-Encoding", "gzip")
	response, err := client.CopyAsset(test.Context(), request)
	if err != nil || response.Msg.View == nil || len(response.Msg.View.Secrets) == 0 || response.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE {
		test.Fatal("copy fault did not produce a decodable successful typed response")
	}
	for _, slot := range response.Msg.View.Secrets {
		if !slot.Present {
			test.Fatal("copy fault was not an all-present structural response")
		}
	}
	actual, err := fixture.domain.Edit(test.Context(), fixture.owner, response.Msg.View.Asset.Id)
	if err != nil || len(actual.Secrets) == 0 || actual.Readiness != configuration.NeedsCredential {
		test.Fatal("copy fault changed actual credential ownership")
	}
	for _, slot := range actual.Secrets {
		if slot.Present {
			test.Fatal("copy fault changed real database credential presence")
		}
	}
	fixture.copyMutex.Lock()
	defer fixture.copyMutex.Unlock()
	if fixture.corruptReplies != 1 {
		test.Fatal("copy fault missed response publication")
	}
}

func TestOpenCodeBrowserSecurityAndFullRPC(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			fixture := newOpenCodeBrowserFixture(test, nil)
			alice, err := fixture.authentication.Login(test.Context(), "opencode-browser-alice", []byte(fixture.password))
			if err != nil {
				test.Fatal("first real session unavailable")
			}
			bob, err := fixture.authentication.Login(test.Context(), "opencode-browser-bob", []byte(fixture.password))
			if err != nil {
				test.Fatal("second real session unavailable")
			}
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(fixture.public.Client(), fixture.public.URL, options...)
			if _, err := client.ListAssets(test.Context(), connect.NewRequest(&configurationv1.ListAssetsRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
				test.Fatal("unauthenticated metadata route accepted")
			}
			for _, format := range []string{"v1", "v2"} {
				provider := openCodeBrowserFullProvider(format)
				draft := &configurationv1.Draft{Name: "Full " + format, Payload: &configurationv1.Draft_OpencodeProvider{OpencodeProvider: configurationOpenCodeProviderWire(&provider)}}
				mutation := &configurationv1.SecretMutation{SlotId: "opencode.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "SYNTHETIC-OC-WEB-REPLACEMENT"}}
				request := configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: []*configurationv1.SecretMutation{mutation}}, alice.Reveal())
				request.Header().Set("Origin", "https://foreign.invalid")
				if _, err := client.CreateAsset(test.Context(), request); connect.CodeOf(err) != connect.CodePermissionDenied {
					test.Fatal("foreign origin write accepted")
				}
				created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: []*configurationv1.SecretMutation{mutation}}, alice.Reveal()))
				if err != nil || !proto.Equal(created.Msg.View.Draft, draft) {
					test.Fatal("full typed real create failed")
				}
				asset := created.Msg.View.Asset
				if _, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: asset.Id}, bob.Reveal())); connect.CodeOf(err) != connect.CodeNotFound {
					test.Fatal("other owner read accepted")
				}
				if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: draft}, bob.Reveal())); connect.CodeOf(err) != connect.CodeNotFound {
					test.Fatal("other owner write accepted")
				}
				if _, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: 1, Name: "Denied"}, bob.Reveal())); connect.CodeOf(err) != connect.CodeNotFound {
					test.Fatal("other owner copy accepted")
				}
				if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: asset.Id, ExpectedRevision: 1}, bob.Reveal())); connect.CodeOf(err) != connect.CodeNotFound {
					test.Fatal("other owner deletion accepted")
				}
				updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: draft}, alice.Reveal()))
				if err != nil || updated.Msg.View.Asset.Revision != 2 {
					test.Fatal("full same-CAS update failed")
				}
				if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: draft}, alice.Reveal())); connect.CodeOf(err) != connect.CodeAborted {
					test.Fatal("stale real CAS accepted")
				}
				copied, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: 2, Name: "Full copy"}, alice.Reveal()))
				if err != nil || !proto.Equal(copied.Msg.View.Draft.GetOpencodeProvider(), draft.GetOpencodeProvider()) || len(copied.Msg.View.Secrets) == 0 {
					test.Fatal("full real copy failed")
				}
				for _, slot := range copied.Msg.View.Secrets {
					if slot.Present {
						test.Fatal("full copy retained actual key")
					}
				}
				encoded, _ := protojson.Marshal(updated.Msg.View)
				if bytes.Contains(encoded, []byte("SYNTHETIC-OC-WEB-REPLACEMENT")) {
					test.Fatal("wire view contained secret")
				}
			}
		})
	}
}

func TestOpenCodeBrowserFixtureRPC(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			fixture := newOpenCodeBrowserFixture(test, nil)
			session, err := fixture.authentication.Login(test.Context(), "opencode-browser-alice", []byte(fixture.password))
			if err != nil {
				test.Fatal("real login failed")
			}
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(fixture.public.Client(), fixture.public.URL, options...)
			for _, format := range []string{"v1", "v2"} {
				draft := openCodeConfigurationDraft(format)
				created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, session.Reveal()))
				if err != nil {
					test.Fatal("real OpenCode create failed")
				}
				asset := created.Msg.View.Asset
				listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session.Reveal()))
				if err != nil || len(listed.Msg.Assets) != 1 {
					test.Fatal("real list failed")
				}
				got, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: asset.Id}, session.Reveal()))
				if err != nil || got.Msg.Asset.Id != asset.Id {
					test.Fatal("real metadata get failed")
				}
				read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: asset.Id}, session.Reveal()))
				if err != nil || !proto.Equal(read.Msg.View.Draft, draft) {
					test.Fatal("real draft roundtrip failed")
				}
				updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: draft}, session.Reveal()))
				if err != nil || updated.Msg.View.Asset.Revision != 2 {
					test.Fatal("real update failed")
				}
				copy, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: 2, Name: "No credentials copy"}, session.Reveal()))
				if err != nil || !proto.Equal(copy.Msg.View.Draft.GetOpencodeProvider(), draft.GetOpencodeProvider()) {
					test.Fatal("real copy lost provider tree")
				}
				for _, slot := range copy.Msg.View.Secrets {
					if slot.Present {
						test.Fatal("copy returned credential presence")
					}
				}
				for _, target := range []struct {
					id       string
					revision int64
				}{{asset.Id, 2}, {copy.Msg.View.Asset.Id, 1}} {
					if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: target.id, ExpectedRevision: target.revision}, session.Reveal())); err != nil {
						test.Fatal("real delete failed")
					}
				}
			}
		})
	}
}
