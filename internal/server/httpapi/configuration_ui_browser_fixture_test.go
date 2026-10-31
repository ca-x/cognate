package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/web"
)

type configurationUIFixture struct {
	public, control                               *httptest.Server
	capability, password, session, foreign, owner string
	done                                          context.Context
	cancel                                        context.CancelFunc
	mutex                                         sync.Mutex
	mode, procedure                               string
	requests                                      map[string]int
	store                                         *state.Store
}

func newConfigurationUIFixture(test *testing.T) *configurationUIFixture {
	test.Helper()
	authentication, store := testServiceAndStore(test)
	ctx, cancel := context.WithCancel(test.Context())
	fixture := &configurationUIFixture{capability: rand.Text(), password: rand.Text(), done: ctx, cancel: cancel, store: store, requests: make(map[string]int)}
	test.Cleanup(cancel)
	for _, name := range []string{"configuration-owner", "configuration-foreign"} {
		identity, err := authentication.Register(ctx, name, []byte(fixture.password))
		if err != nil {
			test.Fatal("synthetic account setup failed")
		}
		if name == "configuration-owner" {
			fixture.owner = identity
		}
	}
	owner, err := authentication.Login(ctx, "configuration-owner", []byte(fixture.password))
	if err != nil {
		test.Fatal("synthetic login failed")
	}
	fixture.session = owner.Reveal()
	foreign, err := authentication.Login(ctx, "configuration-foreign", []byte(fixture.password))
	if err != nil {
		test.Fatal("foreign login failed")
	}
	fixture.foreign = foreign.Reveal()
	fixture.public = httptest.NewUnstartedServer(nil)
	origin := "https://" + fixture.public.Listener.Addr().String()
	handler := NewWithOptions(authentication, Options{Configuration: configuration.New(store), PublicOrigin: origin, WebUI: web.Handler()})
	fixture.public.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mutex.Lock()
		fixture.requests[request.URL.Path]++
		mode, procedure := fixture.mode, fixture.procedure
		fixture.mutex.Unlock()
		if procedure != "" && strings.HasSuffix(request.URL.Path, "/"+procedure) {
			if mode == "slow" || mode == "slow-error" {
				select {
				case <-time.After(1200 * time.Millisecond):
				case <-request.Context().Done():
					return
				}
			}
			if mode == "lost" {
				recorded := httptest.NewRecorder()
				handler.ServeHTTP(recorded, request)
				if recorded.Code != http.StatusOK {
					test.Error("lost reply did not follow successful business handler")
				}
			}
			if mode == "error" || mode == "lost" || mode == "slow-error" {
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
	client := configurationv1connect.NewConfigurationServiceClient(fixture.public.Client(), fixture.public.URL)
	drafts := []*configurationv1.Draft{
		{Name: "Claude synthetic", Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: &configurationv1.ClaudeProvider{AuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_NATIVE_LOGIN}}},
		codexConfigurationDraft(), geminiConfigurationDraft(), openCodeConfigurationDraft("v2"), grokBuildNativeDraft(),
	}
	for index, draft := range drafts {
		draft.Name = []string{"Claude synthetic", "Codex synthetic", "Gemini synthetic", "OpenCode synthetic", "Grok synthetic"}[index]
		if _, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, fixture.session)); err != nil {
			test.Fatalf("provider seed failed: %s", connect.CodeOf(err))
		}
	}
	if _, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: &configurationv1.Draft{Name: "Claude second", Payload: drafts[0].Payload}}, fixture.session)); err != nil {
		test.Fatal("second asset setup failed")
	}
	if _, err := client.CreateAsset(ctx, configurationRequest(&configurationv1.CreateAssetRequest{Draft: drafts[0]}, fixture.foreign)); err != nil {
		test.Fatal("foreign seed failed")
	}
	if _, err := store.CreateConfigurationAsset(ctx, fixture.owner, state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindProvider, Name: "Unknown format synthetic", SchemaVersion: 65535, Payload: state.NewConfigurationPayload([]byte(`{"future":true}`))}); err != nil {
		test.Fatal("unknown asset seed failed")
	}
	fixture.control = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(fixture.capability)) != 1 || request.Header.Get("Origin") != "" || request.URL.RawQuery != "" {
			http.Error(writer, "control denied", 403)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /login":
			_ = json.MarshalWrite(writer, map[string]string{"username": "configuration-owner", "password": fixture.password})
		case "GET /state":
			assets, err := store.ListConfigurationAssets(ctx, fixture.owner)
			if err != nil {
				http.Error(writer, "state unavailable", 500)
				return
			}
			type receipt struct {
				ID, Name, Agent string
				Revision        int64
			}
			rows := make([]receipt, 0, len(assets))
			for _, asset := range assets {
				rows = append(rows, receipt{asset.ID, asset.Name, asset.AgentID, asset.Revision})
			}
			fixture.mutex.Lock()
			defer fixture.mutex.Unlock()
			_ = json.MarshalWrite(writer, map[string]any{"assets": rows, "requests": fixture.requests})
		case "POST /mode":
			var input struct{ Mode, Procedure string }
			if json.UnmarshalRead(http.MaxBytesReader(writer, request.Body, 512), &input, json.RejectUnknownMembers(true)) != nil || (input.Mode != "normal" && input.Mode != "error" && input.Mode != "slow" && input.Mode != "slow-error" && input.Mode != "lost") {
				http.Error(writer, "invalid mode", 400)
				return
			}
			fixture.mutex.Lock()
			fixture.mode, fixture.procedure = input.Mode, input.Procedure
			fixture.mutex.Unlock()
		case "POST /external":
			var input struct{ ID string }
			if json.UnmarshalRead(http.MaxBytesReader(writer, request.Body, 512), &input) != nil {
				http.Error(writer, "invalid asset", 400)
				return
			}
			view, err := client.ReadDraft(ctx, configurationRequest(&configurationv1.ReadDraftRequest{Id: input.ID}, fixture.session))
			if err != nil {
				http.Error(writer, "asset unavailable", 400)
				return
			}
			view.Msg.View.Draft.Name += " external"
			_, err = client.UpdateAsset(ctx, configurationRequest(&configurationv1.UpdateAssetRequest{Id: input.ID, ExpectedRevision: view.Msg.View.Asset.Revision, Draft: view.Msg.View.Draft}, fixture.session))
			if err != nil {
				http.Error(writer, "external update failed", 500)
			}
		case "POST /empty":
			listed, err := client.ListAssets(ctx, configurationRequest(&configurationv1.ListAssetsRequest{}, fixture.session))
			if err != nil {
				http.Error(writer, "asset list unavailable", 500)
				return
			}
			for _, asset := range listed.Msg.Assets {
				if _, err := client.DeleteAsset(ctx, configurationRequest(&configurationv1.DeleteAssetRequest{Id: asset.Id, ExpectedRevision: asset.Revision}, fixture.session)); err != nil {
					http.Error(writer, "asset deletion failed", 500)
					return
				}
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

func TestConfigurationUIBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_CONFIGURATION_UI_DIR")
	if directory == "" {
		test.Skip("isolated configuration UI entrypoint")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal("private readiness directory unavailable")
	}
	defer root.Close()
	fixture := newConfigurationUIFixture(test)
	file, err := root.OpenFile("ready.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		test.Fatal("readiness unavailable")
	}
	if err := json.MarshalWrite(file, map[string]string{"url": fixture.public.URL, "controlURL": fixture.control.URL, "capability": fixture.capability}); err != nil {
		test.Fatal("readiness write failed")
	}
	if err := file.Close(); err != nil {
		test.Fatal("readiness close failed")
	}
	select {
	case <-fixture.done.Done():
	case <-time.After(25 * time.Minute):
		test.Fatal("fixture timed out without stop")
	}
	fixture.public.Close()
	fixture.control.Close()
	if err := fixture.store.Close(); err != nil {
		test.Fatal("fixture store did not close cleanly")
	}
	test.Log("CONFIGURATION_UI_STOP_RECEIPT")
}

func TestConfigurationUIFixtureGuards(test *testing.T) {
	fixture := newConfigurationUIFixture(test)
	client := configurationv1connect.NewConfigurationServiceClient(fixture.public.Client(), fixture.public.URL)
	if _, err := client.ListAssets(test.Context(), connect.NewRequest(&configurationv1.ListAssetsRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("anonymous access allowed")
	}
	listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, fixture.foreign))
	if err != nil || len(listed.Msg.Assets) != 1 {
		test.Fatal("owner isolation failed")
	}
	for _, suffix := range []string{"/state", "/state?x=1"} {
		request, _ := http.NewRequestWithContext(test.Context(), http.MethodGet, fixture.control.URL+suffix, nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			test.Fatal("control probe failed")
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			test.Fatal("unguarded control")
		}
	}
	for _, probe := range []struct{ path, origin string }{{"/state?x=1", ""}, {"/state", "https://foreign.invalid"}} {
		request, _ := http.NewRequestWithContext(test.Context(), http.MethodGet, fixture.control.URL+probe.path, nil)
		request.Header.Set("X-Fixture-Capability", fixture.capability)
		request.Header.Set("Origin", probe.origin)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			test.Fatal("control provenance probe failed")
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			test.Fatal("cross-origin or query control allowed")
		}
	}
}

func TestConfigurationUIFixtureBusiness(test *testing.T) {
	fixture := newConfigurationUIFixture(test)
	client := configurationv1connect.NewConfigurationServiceClient(fixture.public.Client(), fixture.public.URL)
	listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, fixture.session))
	if err != nil || len(listed.Msg.Assets) != 7 {
		test.Fatal("real asset list unavailable")
	}
	var asset *configurationv1.Asset
	for _, entry := range listed.Msg.Assets {
		if entry.AgentId == "codex" {
			asset = entry
		}
	}
	if asset == nil {
		test.Fatal("Codex seed missing")
	}
	if _, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: asset.Id}, fixture.foreign)); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("foreign draft visible")
	}
	draft := codexConfigurationDraft()
	draft.Name = "Credential lifecycle synthetic"
	secret := rand.Text()
	replaced, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: asset.Revision, Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "codex.provider-key", Action: &configurationv1.SecretMutation_Replace{Replace: secret}}}}, fixture.session))
	if err != nil || !replaced.Msg.View.Secrets[0].Present {
		test.Fatal("replace failed")
	}
	kept, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: replaced.Msg.View.Asset.Revision, Draft: draft}, fixture.session))
	if err != nil || !kept.Msg.View.Secrets[0].Present {
		test.Fatal("keep failed")
	}
	if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: asset.Revision, Draft: draft}, fixture.session)); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("stale CAS accepted")
	}
	copied, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: kept.Msg.View.Asset.Revision, Name: "Credential-free synthetic"}, fixture.session))
	if err != nil || copied.Msg.View.Secrets[0].Present {
		test.Fatal("copy leaked credential presence")
	}
	cleared, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: kept.Msg.View.Asset.Revision, Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "codex.provider-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}}}}, fixture.session))
	if err != nil || cleared.Msg.View.Secrets[0].Present {
		test.Fatal("clear failed")
	}
}
