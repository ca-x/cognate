package desktop

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativeconfig"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/nativeconsumer"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"github.com/gofrs/flock"
	"google.golang.org/protobuf/proto"
)

type configurationFixture struct {
	service                        *Service
	server                         *httptest.Server
	store                          *serverstate.Store
	auth                           *auth.Service
	domain                         *configuration.Service
	owner, session, foreignSession string
	mutex                          sync.Mutex
	requests                       map[string]int
	intercept                      func(http.ResponseWriter, *http.Request) bool
}

func newConfigurationFixture(test *testing.T) *configurationFixture {
	test.Helper()
	home, directory := test.TempDir(), test.TempDir()
	test.Setenv("HOME", home)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	test.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{29}, 32)
	defer clear(key)
	store, err := serverstate.Open(test.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		test.Fatal("open synthetic server")
	}
	test.Cleanup(func() { _ = store.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal("create synthetic authentication")
	}
	owner, err := authentication.Register(test.Context(), "native-config-owner", []byte("synthetic-password-123"))
	if err != nil {
		test.Fatal("register synthetic owner")
	}
	issued, err := authentication.Login(test.Context(), "native-config-owner", []byte("synthetic-password-123"))
	if err != nil {
		test.Fatal("login synthetic owner")
	}
	if _, err := authentication.Register(test.Context(), "native-config-foreign", []byte("synthetic-password-456")); err != nil {
		test.Fatal("register foreign owner")
	}
	foreign, err := authentication.Login(test.Context(), "native-config-foreign", []byte("synthetic-password-456"))
	if err != nil {
		test.Fatal("login foreign owner")
	}
	fixture := &configurationFixture{store: store, auth: authentication, domain: configuration.New(store), owner: owner, session: issued.Reveal(), foreignSession: foreign.Reveal(), requests: make(map[string]int)}
	fixture.server = httptest.NewUnstartedServer(nil)
	origin := "https://" + fixture.server.Listener.Addr().String()
	native, err := nativeconsumer.New(store, authentication, key, origin)
	if err != nil {
		test.Fatal("synthetic native service")
	}
	if err := native.Start(test.Context()); err != nil {
		test.Fatal("start synthetic native service")
	}
	test.Cleanup(native.Close)
	handler := httpapi.NewWithOptions(authentication, httpapi.Options{Configuration: fixture.domain, PublicOrigin: origin, NativeConfiguration: native})
	fixture.server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mutex.Lock()
		fixture.requests[request.URL.Path]++
		intercept := fixture.intercept
		fixture.mutex.Unlock()
		if intercept != nil && intercept(writer, request) {
			return
		}
		handler.ServeHTTP(writer, request)
	})
	fixture.server.StartTLS()
	test.Cleanup(fixture.server.Close)
	fixture.service = &Service{Home: home, HTTP: fixture.server.Client()}
	return fixture
}

func (fixture *configurationFixture) count(method string) int {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	count := 0
	for path, value := range fixture.requests {
		if method == "" || strings.HasSuffix(path, "/"+method) {
			count += value
		}
	}
	return count
}

func (fixture *configurationFixture) setIntercept(intercept func(http.ResponseWriter, *http.Request) bool) {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	fixture.intercept = intercept
}

func configurationTestIdentity(test *testing.T, fixture *configurationFixture) configurationIdentity {
	test.Helper()
	identity, err := fixture.service.ConfigurationIdentity(test.Context(), fixture.server.URL, fixture.session)
	if err != nil {
		test.Fatal("synthetic identity failed")
	}
	return identity
}

func TestNativeConfigurationMetadataAndCapacity(test *testing.T) {
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	for index := range 250 {
		agent, schema, kind := "codex", 1, serverstate.ConfigurationKindProvider
		if index%3 == 1 {
			agent, schema = "claude", 2
		}
		if index%3 == 2 {
			agent, kind = "future-agent", serverstate.ConfigurationKindMCP
		}
		_, err := fixture.store.CreateConfigurationAsset(test.Context(), fixture.owner, serverstate.ConfigurationAssetDraft{AgentID: agent, Kind: kind, Name: fmt.Sprintf("Metadata %03d", index), SchemaVersion: schema, Payload: serverstate.NewConfigurationPayload([]byte(`{"future":true}`))})
		if err != nil {
			test.Fatal("seed bounded mixed metadata")
		}
	}
	listed, err := fixture.service.ConfigurationAssets(test.Context(), identity)
	if err != nil || len(listed) != 250 || fixture.count("ReadDraft") != 0 {
		test.Fatal("mixed metadata failed or eagerly decoded drafts")
	}
	for _, asset := range listed {
		if asset.Editable {
			test.Fatal("unsupported metadata became editable")
		}
	}
	before := fixture.count("")
	if _, err := fixture.service.ConfigurationDraft(test.Context(), identity, listed[0].ID); !errors.Is(err, errConfigurationUnsupported) {
		test.Fatal("unsupported draft was not rejected")
	}
	if fixture.count("") != before+1 || fixture.count("ReadDraft") != 0 {
		test.Fatal("unsupported capability performed draft/native access")
	}
	if _, err := fixture.service.CopyConfiguration(test.Context(), identity, listed[0], "Unsupported copy"); !errors.Is(err, errConfigurationUnsupported) || fixture.count("") != before+1 {
		test.Fatal("unsupported metadata copy reached typed domain")
	}
	if _, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(configurationAsset{}, "Overflow")); !errors.Is(err, errConfigurationLimit) {
		test.Fatal("real per-owner quota was not preserved")
	}
	if err := fixture.service.DeleteConfiguration(test.Context(), identity, listed[0]); err != nil {
		test.Fatal("metadata-only delete attempted typed decode")
	}
}

func TestNativeConfigurationPresenceAndCAS(test *testing.T) {
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	provider := agentconfig.ClaudeProviderDesired{AuthMode: agentconfig.ClaudeAPIKeyAuth, Endpoint: new("https://api.invalid/v1"), Models: agentconfig.ClaudeModelsDesired{Default: new("default"), Haiku: new("haiku"), Sonnet: new("sonnet"), Opus: new("opus")}}
	edit := newConfigurationEdit(configurationAsset{}, "All fields", provider, [2]configurationSecretEdit{{Action: configurationReplace, Replacement: []byte("synthetic-api-secret")}, {Action: configurationClear}})
	secretBacking := edit.private().Secrets[0].Replacement
	created, err := fixture.service.SaveConfiguration(test.Context(), identity, edit)
	if err != nil || created.private().Provider.Endpoint == nil || *created.private().Provider.Models.Opus != "opus" {
		test.Fatal("present provider fields were lost")
	}
	if !bytes.Equal(secretBacking, make([]byte, len(secretBacking))) || len(edit.private().Secrets[0].Replacement) != 0 {
		test.Fatal("submitted replacement buffer was not cleared")
	}
	for index := range 5 {
		bad := cloneConfigurationProvider(provider)
		switch index {
		case 0:
			bad.Endpoint = new("")
		case 1:
			bad.Models.Default = new("")
		case 2:
			bad.Models.Haiku = new("")
		case 3:
			bad.Models.Sonnet = new("")
		case 4:
			bad.Models.Opus = new("")
		}
		before := fixture.count("")
		input := newConfigurationEdit(created.Asset, "Empty is present", bad, [2]configurationSecretEdit{{Action: configurationKeep}, {Action: configurationKeep}})
		if _, err := fixture.service.SaveConfiguration(test.Context(), identity, input); !errors.Is(err, errConfigurationInvalid) || fixture.count("") != before {
			test.Fatal("present-empty value was silently removed or sent")
		}
	}
	keep := [2]configurationSecretEdit{{Action: configurationKeep}, {Action: configurationKeep}}
	provider.Endpoint, provider.Models = nil, agentconfig.ClaudeModelsDesired{}
	updated, err := fixture.service.SaveConfiguration(test.Context(), identity, newConfigurationEdit(created.Asset, "Absent fields", provider, keep))
	if err != nil || updated.private().Provider.Endpoint != nil || updated.private().Provider.Models.Default != nil {
		test.Fatal("absent provider fields were synthesized")
	}
	payload, err := fixture.store.ReadConfigurationAsset(test.Context(), fixture.owner, updated.Asset.ID)
	if err != nil {
		test.Fatal("read encrypted synthetic fixture")
	}
	encoded := payload.Reveal()
	defer clear(encoded)
	var stored struct {
		Secrets struct {
			APIKey    string `json:"api_key"`
			AuthToken string `json:"auth_token"`
		} `json:"secrets"`
	}
	if json.Unmarshal(encoded, &stored) != nil || stored.Secrets.APIKey != "synthetic-api-secret" {
		test.Fatal("KEEP did not retain the actual encrypted credential")
	}
	provider.AuthMode = agentconfig.ClaudeAuthTokenAuth
	if _, err := fixture.service.SaveConfiguration(test.Context(), identity, newConfigurationEdit(updated.Asset, "No implicit clearing", provider, [2]configurationSecretEdit{{Action: configurationKeep}, {Action: configurationReplace, Replacement: []byte("synthetic-token-secret")}})); !errors.Is(err, errConfigurationInvalid) {
		test.Fatal("mode switch silently cleared the incompatible stored secret")
	}
	switched, err := fixture.service.SaveConfiguration(test.Context(), identity, newConfigurationEdit(updated.Asset, "Explicit switch", provider, [2]configurationSecretEdit{{Action: configurationClear}, {Action: configurationReplace, Replacement: []byte("synthetic-token-secret")}}))
	if err != nil || switched.private().Provider.APIKeyPresent || !switched.private().Provider.AuthTokenPresent {
		test.Fatal("explicit slot switch failed")
	}
	for _, secrets := range [][2]configurationSecretEdit{
		{{Action: configurationKeep}, {Action: configurationClear}},
		{{Action: configurationReplace}, {Action: configurationClear}},
	} {
		before := fixture.count("")
		if _, err := fixture.service.SaveConfiguration(test.Context(), identity, newConfigurationEdit(configurationAsset{}, "Invalid new credentials", provider, secrets)); !errors.Is(err, errConfigurationInvalid) || fixture.count("") != before {
			test.Fatal("new KEEP or empty REPLACE reached server")
		}
	}
	provider.AuthMode = agentconfig.ClaudeNativeLogin
	provider.Endpoint = new("https://api.invalid")
	if _, err := fixture.service.SaveConfiguration(test.Context(), identity, newConfigurationEdit(switched.Asset, "Invalid native endpoint", provider, keep)); !errors.Is(err, errConfigurationInvalid) {
		test.Fatal("native-login endpoint was silently discarded")
	}
}

func TestNativeConfigurationInFlightConflictAndRevocation(test *testing.T) {
	for _, revoke := range []bool{false, true} {
		test.Run(fmt.Sprintf("revoke=%t", revoke), func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			identity := configurationTestIdentity(test, fixture)
			created, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(configurationAsset{}, "Before barrier"))
			if err != nil {
				test.Fatal("seed barrier asset")
			}
			entered, release := make(chan struct{}), make(chan struct{})
			test.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
				if strings.HasSuffix(request.URL.Path, "/UpdateAsset") {
					close(entered)
					select {
					case <-release:
					case <-request.Context().Done():
					}
				}
				return false
			})
			result := make(chan error, 1)
			go func() {
				_, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(created.Asset, "Must not win"))
				result <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				test.Fatal("real update did not reach TLS barrier")
			}
			var expected error
			if revoke {
				if err := fixture.auth.Logout(test.Context(), fixture.session); err != nil {
					test.Fatal("revoke real parent session")
				}
				expected = errConfigurationIdentity
			} else {
				if _, err := fixture.domain.Update(test.Context(), fixture.owner, created.Asset.ID, 1, configuration.Draft{Name: "Authoritative concurrent edit", Provider: configuration.ClaudeProvider{AuthMode: configuration.APIKey}}, nil); err != nil {
					test.Fatal("commit authoritative concurrent update")
				}
				expected = errConfigurationConflict
			}
			before, err := fixture.store.ReadConfigurationAsset(test.Context(), fixture.owner, created.Asset.ID)
			if err != nil {
				test.Fatal("capture authoritative envelope")
			}
			close(release)
			select {
			case err := <-result:
				if !errors.Is(err, expected) {
					test.Fatal("wrong held-update classification")
				}
			case <-time.After(3 * time.Second):
				test.Fatal("held update did not drain")
			}
			after, err := fixture.store.ReadConfigurationAsset(test.Context(), fixture.owner, created.Asset.ID)
			previous, current := before.Reveal(), after.Reveal()
			defer clear(previous)
			defer clear(current)
			if err != nil || !bytes.Equal(previous, current) {
				test.Fatal("failed update overwrote authoritative encrypted envelope")
			}
		})
	}
}

func configurationWriteResponse(writer http.ResponseWriter, message proto.Message, compressed bool) {
	data, err := proto.Marshal(message)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/proto")
	if compressed {
		writer.Header().Set("Content-Encoding", "gzip")
		archive := gzip.NewWriter(writer)
		_, _ = archive.Write(data)
		_ = archive.Close()
	} else {
		_, _ = writer.Write(data)
	}
}

func TestNativeConfigurationResponseBoundsAndCapabilities(test *testing.T) {
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	valid := &configurationv1.Asset{Id: strings.Repeat("A", 26), AgentId: "claude", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, Name: "Safe", SchemaVersion: 1, Revision: 1, CreatedAtUnix: 1, UpdatedAtUnix: 1}
	for _, scenario := range []string{"unknown-kind", "bad-name", "nested-unknown", "duplicate", "over-count", "oversize", "gzip-oversize"} {
		test.Run(scenario, func(test *testing.T) {
			entry := proto.Clone(valid).(*configurationv1.Asset)
			response := &configurationv1.ListAssetsResponse{Assets: []*configurationv1.Asset{entry}}
			switch scenario {
			case "unknown-kind":
				entry.Kind = 99
			case "bad-name":
				entry.Name = "unsafe\nmetadata"
			case "nested-unknown":
				entry.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "duplicate":
				response.Assets = append(response.Assets, proto.Clone(entry).(*configurationv1.Asset))
			case "over-count":
				for range 250 {
					response.Assets = append(response.Assets, proto.Clone(entry).(*configurationv1.Asset))
				}
			case "oversize", "gzip-oversize":
				entry.Name = strings.Repeat("synthetic-response-body", 60000)
			}
			fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
				configurationWriteResponse(writer, response, scenario == "gzip-oversize")
				return true
			})
			expected := errConfigurationInvalid
			if scenario == "oversize" || scenario == "gzip-oversize" {
				expected = errConfigurationLimit
			}
			if assets, err := fixture.service.ConfigurationAssets(test.Context(), identity); !errors.Is(err, expected) || len(assets) != 0 {
				test.Fatal("malformed or unbounded metadata was accepted")
			}
		})
	}
	fixture.setIntercept(nil)
	before := fixture.count("")
	edit := configurationTestEdit(configurationAsset{}, "Oversized request")
	edit.private().Secrets[0].Replacement = bytes.Repeat([]byte{'x'}, 129<<10)
	if _, err := fixture.service.SaveConfiguration(test.Context(), identity, edit); !errors.Is(err, errConfigurationInvalid) || fixture.count("") != before {
		test.Fatal("oversized replacement reached network")
	}
}

func TestNativeConfigurationDraftValidation(test *testing.T) {
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	created, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(configurationAsset{}, "Draft validation"))
	if err != nil {
		test.Fatal("seed draft validation")
	}
	for _, scenario := range []string{"missing-payload", "unknown-mode", "unknown-slot", "duplicate-slot", "unknown-field", "empty-presence", "wrong-readiness", "preserved-extras"} {
		test.Run(scenario, func(test *testing.T) {
			view := &configurationv1.DraftView{
				Asset:               &configurationv1.Asset{Id: created.Asset.ID, AgentId: "claude", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, Name: created.Asset.Name, SchemaVersion: 1, Revision: 1, CreatedAtUnix: 1, UpdatedAtUnix: 1},
				Draft:               &configurationv1.Draft{Name: created.Asset.Name, Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: &configurationv1.ClaudeProvider{AuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY, Models: &configurationv1.ClaudeModels{}}}},
				Secrets:             []*configurationv1.SecretStatus{{SlotId: "claude.api-key", Present: true}, {SlotId: "claude.auth-token"}},
				CredentialReadiness: configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE,
			}
			switch scenario {
			case "missing-payload":
				view.Draft.Payload = nil
			case "unknown-mode":
				view.Draft.GetClaudeProvider().AuthMode = 99
			case "unknown-slot":
				view.Secrets[0].SlotId = "future-slot"
			case "duplicate-slot":
				view.Secrets[1].SlotId = view.Secrets[0].SlotId
			case "unknown-field":
				view.Draft.GetClaudeProvider().Models.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "empty-presence":
				view.Draft.GetClaudeProvider().Endpoint = new("")
			case "wrong-readiness":
				view.CredentialReadiness = configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL
			case "preserved-extras":
				view.HasPreservedExtras = true
			}
			fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
				if !strings.HasSuffix(request.URL.Path, "/ReadDraft") {
					return false
				}
				configurationWriteResponse(writer, &configurationv1.ReadDraftResponse{View: view}, false)
				return true
			})
			read, err := fixture.service.ConfigurationDraft(test.Context(), identity, created.Asset.ID)
			if scenario == "preserved-extras" {
				if err != nil || !read.private().PreservedExtras {
					test.Fatal("wire extras presence was discarded")
				}
			} else if !errors.Is(err, errConfigurationInvalid) || read.private != nil {
				test.Fatal("invalid typed draft response was exposed")
			}
		})
	}
}

func TestNativeConfigurationSerializedSendBudget(test *testing.T) {
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		_, _ = io.Copy(io.Discard, request.Body)
		configurationWriteResponse(writer, &configurationv1.CreateAssetResponse{}, false)
		return true
	})
	for _, jsonWire := range []bool{false, true} {
		test.Run(fmt.Sprintf("json=%t", jsonWire), func(test *testing.T) {
			options := configurationOptions()
			if jsonWire {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(fixture.service.client(identity.Origin), identity.Origin, options...)
			name := strings.Repeat("x", 129<<10)
			if jsonWire {
				name = strings.Repeat(`"`, 70<<10)
			}
			request := connect.NewRequest(&configurationv1.CreateAssetRequest{Draft: &configurationv1.Draft{Name: name}})
			if err := identity.authorize(request.Header()); err != nil {
				test.Fatal("authorize bounded transport fixture")
			}
			before := fixture.count("")
			_, err := client.CreateAsset(test.Context(), request)
			test.Logf("send budget code=%s request delta=%d", connect.CodeOf(err), fixture.count("")-before)
			if connect.CodeOf(err) != connect.CodeResourceExhausted {
				test.Fatal("local serialized protobuf/escaped JSON send budget was not enforced")
			}
		})
	}
}

func TestNativeConfigurationRequestDeadline(test *testing.T) {
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	entered, drained := make(chan struct{}), make(chan struct{})
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		writer.Header().Set("Content-Type", "application/proto")
		writer.WriteHeader(http.StatusOK)
		_ = http.NewResponseController(writer).Flush()
		close(entered)
		<-request.Context().Done()
		close(drained)
		return true
	})
	started := time.Now()
	result := make(chan error, 1)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	go func() { _, err := fixture.service.ConfigurationAssets(ctx, identity); result <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		test.Fatal("configured deadline did not reach real body read")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) < 19*time.Second {
			test.Fatal("actual 20-second RPC budget was shortened or misclassified")
		}
	case <-time.After(25 * time.Second):
		test.Fatal("configured 20-second RPC body read did not drain")
	}
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		test.Fatal("configured timeout left a real server handler running")
	}
}

func TestNativeConfigurationCancellationAndTransport(test *testing.T) {
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	before := fixture.count("")
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := fixture.service.ConfigurationAssets(ctx, identity); !errors.Is(err, context.Canceled) || fixture.count("") != before {
		test.Fatal("pre-canceled request reached network")
	}
	entered, drained := make(chan struct{}), make(chan struct{})
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		if !strings.HasSuffix(request.URL.Path, "/ListAssets") {
			return false
		}
		writer.Header().Set("Content-Type", "application/proto")
		writer.WriteHeader(http.StatusOK)
		_ = http.NewResponseController(writer).Flush()
		close(entered)
		<-request.Context().Done()
		close(drained)
		return true
	})
	ctx, cancel = context.WithCancel(test.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := fixture.service.ConfigurationAssets(ctx, identity); result <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		test.Fatal("body-read cancellation did not reach real server")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			test.Fatal("body cancellation lost classification")
		}
	case <-time.After(3 * time.Second):
		test.Fatal("body-read worker was not joined")
	}
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		test.Fatal("cancellation retained real HTTP handler")
	}
	var foreignCalls atomic.Int32
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignCalls.Add(1) }))
	defer foreign.Close()
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		http.Redirect(writer, request, foreign.URL+"/synthetic-private-location", http.StatusTemporaryRedirect)
		return true
	})
	if _, err := fixture.service.ConfigurationAssets(test.Context(), identity); err == nil || foreignCalls.Load() != 0 {
		test.Fatal("redirect crossed origin")
	}
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		if request.Header.Get("Cookie") != "" {
			test.Error("configuration client sent cookie jar credentials")
		}
		configurationWriteResponse(writer, &configurationv1.ListAssetsResponse{}, false)
		return true
	})
	jar, _ := cookiejar.New(nil)
	origin, _ := url.Parse(fixture.server.URL)
	jar.SetCookies(origin, []*http.Cookie{{Name: "unrelated-session", Value: "synthetic-cookie"}})
	fixture.service.HTTP.Jar = jar
	if _, err := fixture.service.ConfigurationAssets(test.Context(), identity); err != nil {
		test.Fatal("cookie-free request failed")
	}
	identity.clear()
	before = fixture.count("")
	if _, err := fixture.service.ConfigurationAssets(test.Context(), identity); !errors.Is(err, errConfigurationIdentity) || fixture.count("") != before {
		test.Fatal("cleared identity retained authority")
	}
}

func TestNativeConfigurationServicePrivacy(test *testing.T) {
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	secret := "synthetic-opaque-replacement-sentinel"
	edit := newConfigurationEdit(configurationAsset{}, "Privacy", agentconfig.ClaudeProviderDesired{AuthMode: agentconfig.ClaudeAPIKeyAuth}, [2]configurationSecretEdit{{Action: configurationReplace, Replacement: []byte(secret)}, {Action: configurationClear}})
	var output bytes.Buffer
	previousWriter, previousFlags, previousPrefix, previousSlog := log.Writer(), log.Flags(), log.Prefix(), slog.Default()
	log.SetOutput(&output)
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer func() {
		slog.SetDefault(previousSlog)
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	}()
	for _, value := range []any{identity, edit, []configurationEdit{edit}, map[string]any{"private": struct {
		identity configurationIdentity
		edit     configurationEdit
	}{identity, edit}}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%t", "%d", "%f", "%p"} {
			_, _ = fmt.Fprintf(&output, verb, value)
		}
		log.Print(value)
		slog.Info("synthetic privacy capture", "value", value)
		encoded, _ := json.Marshal(value)
		_, _ = output.Write(encoded)
	}
	created, err := fixture.service.SaveConfiguration(test.Context(), identity, edit)
	if err != nil {
		test.Fatal("privacy real create failed")
	}
	if _, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(created.Asset, "Privacy update")); err != nil {
		test.Fatal("privacy real update failed")
	}
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		http.Error(writer, secret+fixture.session+" https://private.invalid/secret", http.StatusForbidden)
		return true
	})
	_, failure := fixture.service.ConfigurationAssets(test.Context(), identity)
	_, _ = fmt.Fprint(&output, failure)
	if failure == nil || strings.Contains(output.String(), secret) || strings.Contains(output.String(), fixture.session) || strings.Contains(output.String(), "https://private.invalid/secret") {
		test.Fatal("sensitive HTTP input or raw error reached formatted/logged output")
	}
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	for path := range fixture.requests {
		if strings.Contains(path, "native") {
			test.Fatal("privacy editing invoked native authorization")
		}
	}
}

func configurationTestEdit(asset configurationAsset, name string) configurationEdit {
	return newConfigurationEdit(asset, name, agentconfig.ClaudeProviderDesired{AuthMode: agentconfig.ClaudeAPIKeyAuth}, [2]configurationSecretEdit{
		{Action: configurationReplace, Replacement: []byte("synthetic-native-configuration-replacement")},
		{Action: configurationClear},
	})
}

func TestNativeConfigurationKeyAdmissionBeforeIdentity(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("Linux runtime admission; other platforms compile separately")
	}
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	draft, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(configurationAsset{}, "Admission"))
	if err != nil {
		test.Fatal("create admission asset")
	}
	before := fixture.count("")
	_, err = fixture.service.ApplyConfiguration(test.Context(), identity, draft.Asset, "default", true, nativeconfig.Interaction{})
	if !errors.Is(err, clientkey.ErrInitializationRequired) || fixture.count("") != before {
		test.Fatal("missing key reached fresh identity/native transport")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	_, err = fixture.service.ApplyConfiguration(ctx, identity, draft.Asset, "default", false, nativeconfig.Interaction{})
	if !errors.Is(err, context.Canceled) || fixture.count("") != before {
		test.Fatal("canceled admission reached network")
	}
	if err := clientkey.Initialize(test.Context(), fixture.service.Home); err != nil {
		test.Fatal("explicit synthetic key init")
	}
	wrong := identity
	wrong.OwnerID = "wrong-owner"
	_, err = fixture.service.ApplyConfiguration(test.Context(), wrong, draft.Asset, "default", false, nativeconfig.Interaction{})
	if !errors.Is(err, errConfigurationIdentity) || fixture.count("BeginNativeConsumer") != 0 {
		test.Fatal("fresh identity mismatch opened native consumer")
	}
	if _, err := os.Stat(filepath.Join(fixture.service.Home, ".local", "share", "skillhub", "client.db")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("failed admission opened business database")
	}
}

func TestNativeConfigurationKeySentinelsAndReleasedLocks(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("real Linux key admission")
	}
	for _, mode := range []string{"missing", "recovery", "unavailable", "released"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			identity := configurationTestIdentity(test, fixture)
			draft, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(configurationAsset{}, "Key boundaries"))
			if err != nil {
				test.Fatal("save key fixture")
			}
			if mode != "missing" {
				if err := clientkey.Initialize(test.Context(), fixture.service.Home); err != nil {
					test.Fatal("init key fixture")
				}
			}
			keyPath := filepath.Join(fixture.service.Home, ".config", "skillhub", "client.key")
			if mode == "recovery" {
				if err := os.Remove(keyPath); err != nil {
					test.Fatal(err)
				}
			}
			if mode == "unavailable" {
				if err := os.Chmod(keyPath, 0644); err != nil {
					test.Fatal(err)
				}
			}
			before := fixture.count("")
			var checked atomic.Bool
			fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
				if strings.HasSuffix(request.URL.Path, "/GetSession") {
					for _, relative := range []string{".config/skillhub/client-key.init.lock", ".local/share/skillhub/client.db.install.lock"} {
						probe := flock.New(filepath.Join(fixture.service.Home, relative))
						acquired, err := probe.TryLock()
						_ = probe.Close()
						if err != nil || !acquired {
							test.Error("fresh GetSession began under physical key/install admission lock")
						}
					}
					checked.Store(true)
				}
				return false
			})
			_, err = fixture.service.ApplyConfiguration(test.Context(), identity, draft.Asset, "default", false, nativeconfig.Interaction{})
			if mode == "released" {
				if !checked.Load() {
					test.Fatal("successful key never reached fresh identity")
				}
				return
			}
			expected := clientkey.ErrInitializationRequired
			if mode == "recovery" {
				expected = clientkey.ErrRecoveryRequired
			}
			if mode == "unavailable" {
				expected = clientkey.ErrUnavailable
			}
			if !errors.Is(err, expected) || fixture.count("") != before || checked.Load() {
				test.Fatal("failed admission reached network or lost exact safe sentinel")
			}
		})
	}
}

func TestNativeConfigurationCanceledPhysicalLockWait(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("real Linux install lock")
	}
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	draft, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(configurationAsset{}, "Locked wait"))
	if err != nil {
		test.Fatal("lock fixture asset")
	}
	if err := clientkey.Initialize(test.Context(), fixture.service.Home); err != nil {
		test.Fatal("lock fixture key")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestClientKeyEntrypointsSessionChild$")
	command.Env = []string{"COGNATE_E2_SESSION_CHILD=1", "COGNATE_E2_STATE=" + filepath.Join(fixture.service.Home, ".local/share/skillhub"), "HOME=" + fixture.service.Home, "TMPDIR=" + test.TempDir()}
	input, err := command.StdinPipe()
	if err != nil {
		test.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		test.Fatal(err)
	}
	if err := command.Start(); err != nil {
		test.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = command.Process.Kill(); _ = command.Wait() }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "session-locked\n" {
		test.Fatal("actual child lock not acquired")
	}
	before := fixture.count("")
	operation, cancelOperation := context.WithCancel(test.Context())
	result := make(chan error, 1)
	go func() {
		_, err := fixture.service.ApplyConfiguration(operation, identity, draft.Asset, "default", false, nativeconfig.Interaction{})
		result <- err
	}()
	defer cancelOperation()
	locked := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		probe := flock.New(filepath.Join(fixture.service.Home, ".config/skillhub/client-key.init.lock"))
		acquired, err := probe.TryLock()
		_ = probe.Close()
		if err == nil && !acquired {
			locked = true
			break
		}
		runtime.Gosched()
	}
	if !locked {
		test.Fatal("service did not actually wait under admission lock ordering")
	}
	cancelOperation()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			test.Fatal("lock wait lost cancellation")
		}
	case <-ctx.Done():
		test.Fatal("lock waiter did not drain")
	}
	if fixture.count("") != before {
		test.Fatal("canceled lock wait reached fresh identity/native network")
	}
	_ = input.Close()
	if err := command.Wait(); err != nil {
		test.Fatal("lock child did not close")
	}
}

func TestNativeConfigurationRealApplyAndRestore(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("real Linux publisher")
	}
	for _, mode := range []agentconfig.ClaudeAuthMode{agentconfig.ClaudeAPIKeyAuth, agentconfig.ClaudeAuthTokenAuth, agentconfig.ClaudeNativeLogin} {
		test.Run(string(mode), func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			identity := configurationTestIdentity(test, fixture)
			secrets := [2]configurationSecretEdit{{Action: configurationClear}, {Action: configurationClear}}
			if mode == agentconfig.ClaudeAPIKeyAuth {
				secrets[0] = configurationSecretEdit{Action: configurationReplace, Replacement: []byte("synthetic-apply-secret")}
			}
			if mode == agentconfig.ClaudeAuthTokenAuth {
				secrets[1] = configurationSecretEdit{Action: configurationReplace, Replacement: []byte("synthetic-apply-secret")}
			}
			draft, err := fixture.service.SaveConfiguration(test.Context(), identity, newConfigurationEdit(configurationAsset{}, "Apply fixture", agentconfig.ClaudeProviderDesired{AuthMode: mode}, secrets))
			if err != nil {
				test.Fatal("save real asset")
			}
			if err := clientkey.Initialize(test.Context(), fixture.service.Home); err != nil {
				test.Fatal("initialize synthetic key")
			}
			if err := os.Mkdir(filepath.Join(fixture.service.Home, ".claude"), 0700); err != nil {
				test.Fatal(err)
			}
			target := filepath.Join(fixture.service.Home, ".claude", "settings.json")
			original := []byte(`{"env":{"UNOWNED":"keep"}}`)
			if err := os.WriteFile(target, original, 0600); err != nil {
				test.Fatal(err)
			}
			previewCount, approveCount := 0, 0
			interaction := nativeconfig.Interaction{Preview: func(ctx context.Context, plan configapply.SafePlan) error {
				previewCount++
				if !configurationPlanValid(plan) {
					return errConfigurationInvalid
				}
				return nil
			}, Approve: func(ctx context.Context, plan configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot) (nativeconfig.Approval, error) {
				approveCount++
				return nativeconfig.Approval{Confirmed: true, ApprovedSlots: slots}, nil
			}}
			preview, err := fixture.service.ApplyConfiguration(test.Context(), identity, draft.Asset, "default", false, interaction)
			if err != nil || preview.Plan.OperationID == "" || approveCount != 0 || fixture.count("AuthorizeNativeMaterialization") != 0 || fixture.count("ConsumeNativeMaterialization") != 0 {
				test.Fatal("preview did not remain preview")
			}
			if data, err := os.ReadFile(target); err != nil || !bytes.Equal(data, original) {
				test.Fatal("preview changed target")
			}
			outcome, err := fixture.service.ApplyConfiguration(test.Context(), identity, draft.Asset, "default", true, interaction)
			if err != nil || outcome.Local.Status != "AppliedOnDisk" || outcome.Plan.OperationID == preview.Plan.OperationID || approveCount != 1 || previewCount != 2 {
				test.Fatalf("real exact apply failed (class %s)", configurationMessage(err))
			}
			if fixture.count("BeginNativeConsumer") != 2 || fixture.count("BindNativeConsumer") != 2 || fixture.count("PrepareNativeApply") != 2 {
				test.Fatal("apply bypassed real RPC handshake")
			}
			expectedConsume := 1
			if mode == agentconfig.ClaudeNativeLogin {
				expectedConsume = 0
			}
			if fixture.count("ConsumeNativeMaterialization") != expectedConsume {
				test.Fatal("unexpected secret consume")
			}
			data, err := os.ReadFile(target)
			if err != nil || !bytes.Contains(data, []byte("UNOWNED")) || mode != agentconfig.ClaudeNativeLogin && !bytes.Contains(data, []byte("synthetic-apply-secret")) {
				test.Fatal("real target content not preserved/applied")
			}
			requests := fixture.count("")
			status, err := fixture.service.ConfigurationStatus(test.Context(), outcome.Local.OperationID)
			if err != nil || status.Local.Status != "AppliedOnDisk" {
				test.Fatal("offline status failed")
			}
			restored, err := fixture.service.RestoreConfiguration(test.Context(), outcome.Local.OperationID, true, interaction)
			if err != nil || restored.Local.Status != "Restored" || fixture.count("") != requests {
				test.Fatal("offline restore required network or failed")
			}
			if data, err := os.ReadFile(target); err != nil || !bytes.Equal(data, original) {
				test.Fatal("restore did not preserve original baseline")
			}
		})
	}
}

func TestNativeConfigurationLostConsumeNoReplay(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("real Linux publisher")
	}
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	draft, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(configurationAsset{}, "Lost consume"))
	if err != nil {
		test.Fatal("save fixture")
	}
	if err := clientkey.Initialize(test.Context(), fixture.service.Home); err != nil {
		test.Fatal("init fixture")
	}
	if err := os.Mkdir(filepath.Join(fixture.service.Home, ".claude"), 0700); err != nil {
		test.Fatal(err)
	}
	handler := fixture.server.Config.Handler
	fixture.server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
			handler.ServeHTTP(configurationLostWriter{writer}, request)
			connection, _, err := writer.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}
		handler.ServeHTTP(writer, request)
	})
	outcome, err := fixture.service.ApplyConfiguration(test.Context(), identity, draft.Asset, "default", true, nativeconfig.Interaction{Approve: func(ctx context.Context, plan configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot) (nativeconfig.Approval, error) {
		return nativeconfig.Approval{Confirmed: true, ApprovedSlots: slots}, nil
	}})
	if !errors.Is(err, configclient.ErrNativeUnknown) || !outcome.RemoteUnknown || outcome.RemoteState != configclient.NativeConsumed || fixture.count("ConsumeNativeMaterialization") != 1 || fixture.count("GetNativeMaterializationStatus") != 1 || outcome.Local.Status == "AppliedOnDisk" {
		test.Fatalf("lost consume: unknownErr=%t unknown=%t consumed=%t calls=%d statusCalls=%d class=%s", errors.Is(err, configclient.ErrNativeUnknown), outcome.RemoteUnknown, outcome.RemoteState == configclient.NativeConsumed, fixture.count("ConsumeNativeMaterialization"), fixture.count("GetNativeMaterializationStatus"), configurationMessage(err))
	}
}

type configurationLostWriter struct{ http.ResponseWriter }

func (writer configurationLostWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }
func (configurationLostWriter) Write(data []byte) (int, error)     { return len(data), nil }
func (configurationLostWriter) WriteHeader(int)                    {}
func (configurationLostWriter) Flush()                             {}

func TestNativeConfigurationAssetService(test *testing.T) {
	fixture := newConfigurationFixture(test)
	identity, err := fixture.service.ConfigurationIdentity(test.Context(), fixture.server.URL, fixture.session)
	if err != nil || identity.OwnerID != fixture.owner {
		test.Fatal("real GetSession did not bind the authenticated owner")
	}
	created, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(configurationAsset{}, "Native primary"))
	if err != nil || created.Asset.Revision != 1 || !created.Asset.Editable || created.private == nil || !created.private().Provider.APIKeyPresent {
		test.Fatal("real typed create failed")
	}
	listed, err := fixture.service.ConfigurationAssets(test.Context(), identity)
	if err != nil || len(listed) != 1 || listed[0].ID != created.Asset.ID {
		test.Fatal("real metadata list failed")
	}
	read, err := fixture.service.ConfigurationDraft(test.Context(), identity, created.Asset.ID)
	if err != nil || read.Asset.Revision != 1 || read.private().NeedsCredential {
		test.Fatal("real draft read failed")
	}
	updated, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(read.Asset, "Native edited"))
	if err != nil || updated.Asset.Revision != 2 {
		test.Fatal("real typed update failed")
	}
	if _, err := fixture.service.SaveConfiguration(test.Context(), identity, configurationTestEdit(read.Asset, "Stale")); !errors.Is(err, errConfigurationConflict) {
		test.Fatal("stale revision did not preserve CAS")
	}
	copy, err := fixture.service.CopyConfiguration(test.Context(), identity, updated.Asset, "Copy without credentials")
	if err != nil || copy.private().Provider.APIKeyPresent || copy.private().Provider.AuthTokenPresent || !copy.private().NeedsCredential {
		test.Fatal("copy carried a credential")
	}
	foreign, err := fixture.service.ConfigurationIdentity(test.Context(), fixture.server.URL, fixture.foreignSession)
	if err != nil {
		test.Fatal("foreign identity failed")
	}
	if _, err := fixture.service.ConfigurationDraft(test.Context(), foreign, created.Asset.ID); !errors.Is(err, errConfigurationNotFound) {
		test.Fatal("cross-owner draft leaked")
	}
	if err := fixture.service.DeleteConfiguration(test.Context(), identity, updated.Asset); err != nil {
		test.Fatal("owner delete failed")
	}
	if _, err := fixture.service.ConfigurationDraft(test.Context(), identity, created.Asset.ID); !errors.Is(err, errConfigurationNotFound) {
		test.Fatal("deleted asset remained readable")
	}
	if entries, err := os.ReadDir(fixture.service.Home); err != nil || len(entries) != 0 {
		test.Fatal("remote-only editing created local state or key")
	}
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	for path := range fixture.requests {
		if len(path) >= len("/skillhub.configuration.native") && path[:len("/skillhub.configuration.native")] == "/skillhub.configuration.native" {
			test.Fatal("saving invoked native authorization")
		}
	}
}
