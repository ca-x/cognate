package configclient

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/nativeconsumer"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestCodexAdmittedV2Identity(test *testing.T) {
	_, _, _, _, admission, _, _ := codexAdmittedFixture(test, nil)
	id, manifest, profile, template := codexconfig.Native0166EffectiveManifest()
	want := configapply.CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}
	if admission.CopyProfileIdentity() != want {
		test.Fatal("D admission is not exact effective-v2")
	}
	binding := admission.CopyProviderBinding()
	defer binding.Destroy()
	projection, err := codexAdmittedProjection(binding, want)
	if err != nil || projection == nil || projection.BindingVersion != 1 || projection.NativeProfileId != id || !bytes.Equal(projection.NativeProfileSha256, manifest[:]) || !bytes.Equal(projection.TemplateSha256, template[:]) {
		test.Fatal("C2 refuses exact v2 or changed wire version1")
	}
}

func TestCodexAdmittedV1MixedRefusal(test *testing.T) {
	var calls atomic.Int32
	_, flow, engine, plan, admission, _, _ := codexAdmittedFixture(test, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") || strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
				calls.Add(1)
			}
			next.ServeHTTP(writer, request)
		})
	})
	binding := admission.CopyProviderBinding()
	defer binding.Destroy()
	id, manifest, profile, template := codexconfig.Native0166Manifest()
	old := configapply.CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}
	identities := []configapply.CodexProfileIdentity{old, {}}
	for _, field := range []string{"id", "manifest", "profile", "template"} {
		mixed := admission.CopyProfileIdentity()
		switch field {
		case "id":
			mixed.ID = old.ID
		case "manifest":
			mixed.ManifestSHA256 = old.ManifestSHA256
		case "profile":
			mixed.ProfileDigest = old.ProfileDigest
		case "template":
			mixed.TemplateDigest[0] ^= 1
		}
		identities = append(identities, mixed)
	}
	for _, identity := range identities {
		if projection, err := codexAdmittedProjection(binding, identity); err == nil || projection != nil {
			test.Fatal("old/mixed/zero tuple acquired C2 projection authority")
		}
	}
	if status, err := engine.StatusCodex(test.Context(), plan.View().OperationID); err != nil || status.State != configapply.CodexPreviewed || calls.Load() != 0 || flow.codex.authorizeAttempted || flow.codex.consumeAttempted {
		test.Fatal("tuple refusal created intent or remote business request")
	}
}

func TestCodexAdmittedV2Lifecycle(test *testing.T) {
	var authorizes, consumes atomic.Int32
	fixture, flow, engine, plan, admission, _, _ := codexAdmittedFixture(test, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") {
				authorizes.Add(1)
			}
			if strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
				consumes.Add(1)
			}
			next.ServeHTTP(writer, request)
		})
	})
	want := admission.CopyProfileIdentity()
	if result := codexApplyAdmittedRound(test, flow, engine, admission); result.State != configapply.CodexCompleted || authorizes.Load() != 1 || consumes.Load() != 1 || codexCommittedConsumeCount(test, fixture) != 1 {
		test.Fatal("v2 real TLS/SQLite lifecycle not exactly once")
	}
	recorded, err := engine.RecordedCodexProfile(test.Context(), plan.View().OperationID)
	if err != nil || recorded != want {
		test.Fatal("durable v2 profile changed after publish")
	}
}

type codexAdmittedServer struct {
	*nativeClientFixture
	databasePath string
}

func newCodexAdmittedServer(test *testing.T, filter func(http.Handler) http.Handler) *codexAdmittedServer {
	test.Helper()
	master := bytes.Repeat([]byte{91}, 32)
	defer clear(master)
	directory := test.TempDir()
	if os.Chmod(directory, 0o700) != nil {
		test.Fatal("private SQLite directory")
	}
	databasePath := filepath.Join(directory, "admitted.db")
	store, err := state.Open(test.Context(), databasePath, master)
	if err != nil {
		test.Fatal("real SQLite")
	}
	ownerAuth, err := auth.NewService(store, master)
	if err != nil {
		test.Fatal("owner auth")
	}
	token, owner, err := ownerAuth.LoginOIDC(test.Context(), "https://synthetic.identity", "admitted-owner", "", "")
	if err != nil {
		test.Fatal("synthetic owner")
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "https://" + server.Listener.Addr().String()
	service, err := nativeconsumer.New(store, ownerAuth, master, origin)
	if err != nil {
		test.Fatal("native service")
	}
	handler := http.Handler(httpapi.NewWithOptions(ownerAuth, httpapi.Options{PublicOrigin: origin, NativeConfiguration: service}))
	if filter != nil {
		handler = filter(handler)
	}
	server.Config.Handler = handler
	server.StartTLS()
	if service.Start(test.Context()) != nil {
		test.Fatal("native lifecycle")
	}
	settings := server.Client().Transport.(*http.Transport).Clone()
	settings.TLSClientConfig = &tls.Config{RootCAs: settings.TLSClientConfig.RootCAs.Clone()}
	client, err := NewNativeClient(origin, settings)
	if err != nil {
		test.Fatal("TLS native client")
	}
	test.Cleanup(func() { client.Close(); service.Close(); server.Close(); _ = store.Close() })
	return &codexAdmittedServer{nativeClientFixture: &nativeClientFixture{server: server, store: store, auth: ownerAuth, domain: configuration.New(store), service: service, client: client, owner: owner, bearer: token.Reveal()}, databasePath: databasePath}
}

func codexCommittedConsumeCount(test *testing.T, fixture *codexAdmittedServer) int {
	test.Helper()
	database, err := sql.Open("sqlite3", "file:"+fixture.databasePath+"?mode=ro")
	if err != nil {
		test.Fatal("read-only receipt query")
	}
	defer database.Close()
	var count int
	if database.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM native_materialization_grants WHERE consumed_ms > 0").Scan(&count) != nil {
		test.Fatal("durable consume count")
	}
	return count
}

func codexAdmittedFixture(test *testing.T, filter func(http.Handler) http.Handler) (*codexAdmittedServer, *NativeFlow, *configapply.CodexEngine, configapply.CodexPlan, configapply.CodexAdmission, string, string) {
	test.Helper()
	executable := os.Getenv("COGNATE_Q2_NATIVE_FIXTURE")
	if executable == "" {
		test.Skip("explicit pinned native consumer fixture is required for real D integration")
	}
	if _, err := configapply.QualifiedCodexNativeProfile(); err != nil {
		test.Fatal("dedicated native mode requires exact private compiler qualification fixture or independently qualified production tuple")
	}
	fixture := newCodexAdmittedServer(test, filter)
	provider := &configuration.CodexProvider{AuthMode: "provider-key", Endpoint: "https://synthetic-provider.invalid/v1", Model: "synthetic-model", Catalog: &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "synthetic-model", DisplayName: new("Synthetic model"), ContextWindow: new(uint64(32768)), SupportsParallelToolCalls: new(true), InputModalities: new([]agentconfig.CodexModality{"text"}), BaseInstructions: new("SYNTHETIC INSTRUCTIONS"), ReasoningLevels: new([]agentconfig.CodexReasoningEffort{"low", "high"}), DefaultReasoningLevel: new(agentconfig.CodexReasoningEffort("low"))}}}}
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, configuration.Draft{Name: "private-admitted-fixture", CodexProvider: provider}, []configuration.SecretMutation{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("SYNTHETIC_ADMITTED_KEY"))}})
	if err != nil {
		test.Fatal("real encrypted asset creation refused")
	}
	root := test.TempDir()
	home, stateRoot, workspace := filepath.Join(root, "home"), filepath.Join(root, "state"), filepath.Join(root, "workspace")
	for _, path := range []string{home, stateRoot, workspace, filepath.Join(home, ".codex")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			test.Fatal("synthetic directories")
		}
	}
	identity := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"synthetic@example.invalid"}`)) + ".synthetic"
	auth, err := json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]any{"id_token": identity, "access_token": "SYNTHETIC_ACCESS", "refresh_token": "SYNTHETIC_REFRESH", "account_id": "synthetic-account"}, "last_refresh": time.Now().Add(-8*24*time.Hour + 2*time.Minute).UTC().Format(time.RFC3339Nano)})
	if err != nil || os.WriteFile(filepath.Join(home, ".codex", "auth.json"), auth, 0o600) != nil {
		test.Fatal("bounded synthetic auth fixture")
	}
	clear(auth)
	engine, err := configapply.OpenCodex(test.Context(), home, stateRoot, bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		test.Fatal("real physical D engine refused")
	}
	test.Cleanup(func() { _ = engine.Close() })
	current, err := engine.CurrentCodexSelection(test.Context(), fixture.server.URL, fixture.owner, "admitted-profile")
	if err != nil {
		test.Fatal("current selection")
	}
	defer current.Destroy()
	revision, err := current.NextSelectionRevision()
	if err != nil {
		test.Fatal("selection revision")
	}
	flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer)
	if err != nil {
		test.Fatal("real TLS bootstrap refused")
	}
	test.Cleanup(flow.Close)
	prepared, err := flow.PrepareCodex(test.Context(), NativeSelection{AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: "admitted-profile", SelectionRevision: revision})
	if err != nil {
		test.Fatal("real TLS/moejs prepare refused")
	}
	defer prepared.Destroy()
	selection, err := configapply.NewCodexSelection(prepared.CopyBinding(), prepared.CopyDeclaration(), prepared.SessionExpiresAt(), current)
	if err != nil {
		test.Fatal("real D selection refused")
	}
	defer selection.Destroy()
	observation, err := engine.ObserveCodex(test.Context(), selection, configapply.CodexLaunch{Executable: executable, Directory: workspace, Arguments: []string{"app-server"}})
	if err != nil {
		test.Fatal("real pinned native observation refused")
	}
	defer observation.Destroy()
	plan, err := engine.PreviewCodexObserved(test.Context(), selection, observation)
	if err != nil {
		test.Fatal("real D preview refused: dedicated native mode requires explicit private compiler qualification fixture or a separately qualified production profile")
	}
	test.Cleanup(plan.Destroy)
	admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
	if err != nil {
		test.Fatal("D managed confirm refused")
	}
	test.Cleanup(admission.Destroy)
	return fixture, flow, engine, plan, admission, home, stateRoot
}

func TestCodexAdmittedBridgeRealLifecycle(test *testing.T) {
	var authorizes, consumes atomic.Int32
	fixture, flow, engine, plan, admission, home, stateRoot := codexAdmittedFixture(test, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") {
				authorizes.Add(1)
			}
			if strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
				consumes.Add(1)
			}
			next.ServeHTTP(writer, request)
		})
	})
	constraints := admission.CopyAuthorizationConstraints()
	defer constraints.Destroy()
	authBefore, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		test.Fatal("auth baseline")
	}
	defer clear(authBefore)
	binding := admission.CopyProviderBinding()
	defer binding.Destroy()
	identity := admission.CopyProfileIdentity()
	for _, field := range []string{"id", "manifest", "profile", "template"} {
		foreign := identity
		switch field {
		case "id":
			foreign.ID = "foreign-profile"
		case "manifest":
			foreign.ManifestSHA256[0] ^= 1
		case "profile":
			foreign.ProfileDigest[0] ^= 1
		case "template":
			foreign.TemplateDigest[0] ^= 1
		}
		if projection, err := codexAdmittedProjection(binding, foreign); err == nil || projection != nil {
			test.Fatal("foreign full profile tuple acquired projection authority")
		}
	}
	approval, err := NewCodexNativeApproval(constraints.ConfirmationRevision(), constraints.ApprovedDeadline(), true, true)
	if err != nil {
		test.Fatal("D exact approval refused")
	}
	defer approval.Destroy()
	authorization, err := flow.AuthorizeCodexAdmitted(test.Context(), admission, approval)
	if err != nil || authorization.CopyBinding() == nil || authorizes.Load() != 1 {
		test.Fatal("real D admitted bridge did not authorize exactly once")
	}
	defer authorization.Destroy()
	material, err := flow.ConsumeCodex(test.Context())
	if err != nil || consumes.Load() != 1 {
		test.Fatal("real D production consume refused")
	}
	defer material.Destroy()
	if codexCommittedConsumeCount(test, fixture) != 1 {
		test.Fatal("successful consume CAS count differs from one")
	}
	data := material.CloneBytes()
	if !bytes.Equal(data, []byte("SYNTHETIC_ADMITTED_KEY")) {
		clear(data)
		test.Fatal("wrong native material")
	}
	selected, err := codexconfig.NewSelectedSecret(agentconfig.CodexProviderKeySecret, data)
	clear(data)
	if err != nil {
		test.Fatal("selected material refused")
	}
	defer selected.Destroy()
	candidate, err := engine.MaterializeCodex(test.Context(), admission, []codexconfig.SelectedSecret{selected})
	if err != nil {
		test.Fatal("same D plan did not materialize")
	}
	defer candidate.Destroy()
	result, err := engine.ApplyCodex(test.Context(), admission, candidate)
	if err != nil || result.State != configapply.CodexCompleted || result.SelectionRevision != 1 {
		test.Fatal("D publish failed")
	}
	if _, err := flow.ConsumeCodex(test.Context()); err == nil || consumes.Load() != 1 {
		test.Fatal("production consume replayed")
	}
	if err := engine.Close(); err != nil {
		test.Fatal("engine close")
	}
	reopened, err := configapply.OpenCodex(test.Context(), home, stateRoot, bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		test.Fatal("restart")
	}
	defer reopened.Close()
	status, err := reopened.StatusCodex(test.Context(), plan.View().OperationID)
	if err != nil || status.State != configapply.CodexCompleted {
		test.Fatal("durable applied state lost")
	}
	restore, err := reopened.PreviewCodexRestore(test.Context(), status.OperationID)
	if err != nil {
		test.Fatal("restore preview")
	}
	defer restore.Destroy()
	confirmed, err := restore.Confirm(test.Context(), true)
	if err != nil {
		test.Fatal("restore confirm")
	}
	defer confirmed.Destroy()
	result, err = reopened.RestoreCodex(test.Context(), confirmed)
	if err != nil || result.State != configapply.CodexRestored || result.SelectionRevision != 2 || authorizes.Load() != 1 || consumes.Load() != 1 {
		test.Fatalf("controlled restore changed state=%d revision=%d authorize=%d consume=%d conflict=%t attention=%t expired=%t", result.State, result.SelectionRevision, authorizes.Load(), consumes.Load(), errors.Is(err, configapply.ErrCodexConflict), errors.Is(err, configapply.ErrCodexAttention), errors.Is(err, configapply.ErrCodexExpired))
	}
	for _, name := range []string{"config.toml", "cognate-model-catalog.json"} {
		if _, err := os.Stat(filepath.Join(home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("first generated restore did not restore exact Missing Before")
		}
	}
	authAfter, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	defer clear(authAfter)
	if err != nil || !bytes.Equal(authBefore, authAfter) {
		test.Fatal("restore changed Keep auth bytes")
	}
}

func TestCodexAdmittedBridgeDurableIntentBeforeRPC(test *testing.T) {
	for _, mode := range []string{"consume", "abandon-first"} {
		test.Run(mode, func(test *testing.T) {
			var engine *configapply.CodexEngine
			var operation string
			var calls, badPhase atomic.Int32
			fixture, flow, actual, plan, admission, _, _ := codexAdmittedFixture(test, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					wanted := configapply.CodexLocalState(0)
					if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") {
						wanted = configapply.CodexAuthorizeAttempted
					}
					if strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
						wanted = configapply.CodexConsumeAttempted
					}
					if wanted != 0 {
						calls.Add(1)
						status, err := engine.StatusCodex(request.Context(), operation)
						if err != nil || status.State != wanted {
							badPhase.Add(1)
						}
					}
					next.ServeHTTP(writer, request)
				})
			})
			engine, operation = actual, plan.View().OperationID
			constraints := admission.CopyAuthorizationConstraints()
			defer constraints.Destroy()
			approval, err := NewCodexNativeApproval(constraints.ConfirmationRevision(), constraints.ApprovedDeadline(), true, true)
			if err != nil {
				test.Fatal("exact approval")
			}
			defer approval.Destroy()
			if mode == "abandon-first" {
				stop, err := engine.PreviewCodexAbandon(test.Context(), operation)
				if err != nil {
					test.Fatal("abandon preview")
				}
				defer stop.Destroy()
				confirmed, err := stop.Confirm(test.Context(), true)
				if err != nil {
					test.Fatal("abandon confirmation")
				}
				defer confirmed.Destroy()
				if _, err := engine.AbandonCodex(test.Context(), confirmed); err != nil {
					test.Fatal("abandon")
				}
			}
			grant, err := flow.AuthorizeCodexAdmitted(test.Context(), admission, approval)
			defer grant.Destroy()
			if mode == "abandon-first" {
				if err == nil || grant.CopyBinding() != nil {
					test.Fatal("late authorize after abandon")
				}
				material, err := flow.ConsumeCodex(test.Context())
				defer material.Destroy()
				if err == nil || calls.Load() != 0 || codexCommittedConsumeCount(test, fixture) != 0 {
					test.Fatal("abandon-first performed remote mutation")
				}
				return
			}
			if err != nil {
				test.Fatal("authorize")
			}
			material, err := flow.ConsumeCodex(test.Context())
			defer material.Destroy()
			if err != nil || calls.Load() != 2 || badPhase.Load() != 0 || codexCommittedConsumeCount(test, fixture) != 1 {
				test.Fatal("remote mutation preceded durable D attempted intent")
			}
			if _, err := engine.PreviewCodexAbandon(test.Context(), operation); err == nil {
				test.Fatal("durable consume intent acquired abandon proof")
			}
		})
	}
}

func TestCodexAdmittedBridgeUnknownCloseAndContext(test *testing.T) {
	for _, mode := range []string{"lost-authorize", "lost-consume", "close-consume", "retire-consume"} {
		test.Run(mode, func(test *testing.T) {
			var authorizes, consumes, traceCalls atomic.Int32
			entered, drained := make(chan struct{}), make(chan struct{})
			stage := "ConsumeNativeMaterialization"
			if mode == "lost-authorize" {
				stage = "AuthorizeNativeMaterialization"
			}
			fixture, flow, engine, plan, admission, _, _ := codexAdmittedFixture(test, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") {
						authorizes.Add(1)
					}
					if strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
						consumes.Add(1)
					}
					if !strings.HasSuffix(request.URL.Path, "/"+stage) {
						next.ServeHTTP(writer, request)
						return
					}
					capture := &nativeCaptureWriter{header: make(http.Header), actual: writer}
					next.ServeHTTP(capture, request)
					defer clear(capture.body.Bytes())
					defer close(drained)
					if capture.status != http.StatusOK {
						test.Error("expected actual COMMIT before uncertain response")
					}
					close(entered)
					if mode == "close-consume" || mode == "retire-consume" {
						<-request.Context().Done()
					}
					writer.Header().Set("Content-Type", "application/proto")
					writer.WriteHeader(http.StatusOK)
				})
			})
			constraints := admission.CopyAuthorizationConstraints()
			defer constraints.Destroy()
			approval, err := NewCodexNativeApproval(constraints.ConfirmationRevision(), constraints.ApprovedDeadline(), true, true)
			if err != nil {
				test.Fatal("approval")
			}
			defer approval.Destroy()
			caller := httptrace.WithClientTrace(test.Context(), &httptrace.ClientTrace{WroteHeaderField: func(string, []string) { traceCalls.Add(1) }})
			if mode != "lost-authorize" {
				grant, err := flow.AuthorizeCodexAdmitted(caller, admission, approval)
				defer grant.Destroy()
				if err != nil {
					test.Fatal("real admitted authorize")
				}
			}
			result := make(chan error, 1)
			var workers sync.WaitGroup
			workers.Go(func() {
				if mode == "lost-authorize" {
					grant, err := flow.AuthorizeCodexAdmitted(caller, admission, approval)
					defer grant.Destroy()
					if grant.CopyBinding() != nil {
						test.Error("unknown authorize returned authority")
					}
					result <- err
				} else {
					material, err := flow.ConsumeCodex(caller)
					defer material.Destroy()
					if len(material.CloneBytes()) != 0 {
						test.Error("uncertain consume returned material")
					}
					result <- err
				}
			})
			defer workers.Wait()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				test.Fatal("real server boundary not reached")
			}
			if mode == "retire-consume" {
				stop, err := engine.PreviewCodexRetireUnpublished(test.Context(), plan.View().OperationID)
				if err != nil {
					test.Fatal("concurrent retirement preview")
				}
				defer stop.Destroy()
				confirmed, err := stop.Confirm(test.Context(), true)
				if err != nil {
					test.Fatal("concurrent retirement confirm")
				}
				defer confirmed.Destroy()
				if _, err := engine.RetireCodexUnpublished(test.Context(), confirmed); err != nil {
					test.Fatal("concurrent retirement")
				}
			}
			if mode == "close-consume" || mode == "retire-consume" {
				flow.Close()
			}
			select {
			case err := <-result:
				if !errors.Is(err, ErrNativeUnknown) {
					test.Fatal("unknown outcome was not preserved")
				}
			case <-time.After(10 * time.Second):
				test.Fatal("unknown operation did not join")
			}
			select {
			case <-drained:
			case <-time.After(10 * time.Second):
				test.Fatal("handler failed to drain")
			}
			grant, _ := flow.AuthorizeCodexAdmitted(test.Context(), admission, approval)
			defer grant.Destroy()
			material, _ := flow.ConsumeCodex(test.Context())
			defer material.Destroy()
			wantConsumes := int32(1)
			if mode == "lost-authorize" {
				wantConsumes = 0
			}
			if authorizes.Load() != 1 || consumes.Load() != wantConsumes || traceCalls.Load() != 0 || codexCommittedConsumeCount(test, fixture) != int(wantConsumes) {
				test.Fatal("unknown replay, trace exposure, or durable CAS count changed")
			}
			if mode == "lost-authorize" {
				stop, err := engine.PreviewCodexAbandon(test.Context(), plan.View().OperationID)
				if err != nil {
					test.Fatal("unused grant did not permit safe local abandon")
				}
				defer stop.Destroy()
				confirmed, err := stop.Confirm(test.Context(), true)
				if err != nil {
					test.Fatal("abandon confirm")
				}
				defer confirmed.Destroy()
				if _, err := engine.AbandonCodex(test.Context(), confirmed); err != nil {
					test.Fatal("local abandon failed after unknown authorization")
				}
			}
		})
	}
}

func TestCodexAdmittedBridgeUnadmitted(test *testing.T) {
	var calls atomic.Int32
	fixture := newNativeClientFixture(test, false, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") || strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
				calls.Add(1)
			}
			next.ServeHTTP(writer, request)
		})
	})
	flow, _, approval := codexClientPrepared(test, fixture)
	if grant, err := flow.AuthorizeCodexAdmitted(test.Context(), configapply.CodexAdmission{}, approval); err == nil || grant.CopyBinding() != nil {
		test.Fatal("zero admission authorized")
	}
	if value, err := flow.ConsumeCodex(test.Context()); !errors.Is(err, ErrNativeInvalid) || len(value.CloneBytes()) != 0 {
		test.Fatal("zero admission consumed")
	}
	for _, raw := range []string{`{"LifecycleVersion":1,"ManagedModelsVersion":1,"ManagedModels":["forged-after"]}`, `{"profile":{"ID":"forged","ManifestSHA256":"caller"},"constraints":{"Revision":1,"DeadlineUnixMS":9999999999999}}`} {
		var forged configapply.CodexAdmission
		_ = json.Unmarshal([]byte(raw), &forged)
		if grant, err := flow.AuthorizeCodexAdmitted(test.Context(), forged, approval); err == nil || grant.CopyBinding() != nil || calls.Load() != 0 {
			test.Fatal("caller metadata created engine admission evidence")
		}
	}
}

func TestCodexAdmittedBridgeExactConstraints(test *testing.T) {
	for _, kind := range []string{"revision", "deadline"} {
		test.Run(kind, func(test *testing.T) {
			var calls atomic.Int32
			_, flow, engine, plan, admission, _, _ := codexAdmittedFixture(test, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") || strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
						calls.Add(1)
					}
					next.ServeHTTP(writer, request)
				})
			})
			constraints := admission.CopyAuthorizationConstraints()
			defer constraints.Destroy()
			revision, deadline := constraints.ConfirmationRevision(), constraints.ApprovedDeadline()
			if kind == "revision" {
				revision++
			} else {
				deadline = deadline.Add(time.Millisecond)
				if deadline.UnixMilli() > flow.codex.prepared.Binding.PrepareExpiresAtUnixMs || deadline.After(flow.expires) {
					test.Fatal("deadline-only mismatch not inside server/session ceilings")
				}
			}
			approval, err := NewCodexNativeApproval(revision, deadline, true, true)
			if err != nil {
				test.Fatal("scalar mismatch setup")
			}
			defer approval.Destroy()
			grant, err := flow.AuthorizeCodexAdmitted(test.Context(), admission, approval)
			defer grant.Destroy()
			if err == nil || grant.CopyBinding() != nil || calls.Load() != 0 || flow.codex.authorizeAttempted || flow.codex.consumeAttempted {
				test.Fatal("nonexact D constraints reached materialization RPC")
			}
			status, err := engine.StatusCodex(test.Context(), plan.View().OperationID)
			if err != nil || status.State != configapply.CodexPreviewed || status.RemoteOutcomeUnknown {
				test.Fatal("nonexact D constraints persisted attempted intent")
			}
		})
	}
}

func codexAdmittedNextRound(test *testing.T, fixture *codexAdmittedServer, engine *configapply.CodexEngine, home string, models []string) (*NativeFlow, configapply.CodexPlan, configapply.CodexAdmission) {
	test.Helper()
	var catalog *agentconfig.CodexCatalogDesired
	model := "synthetic-model"
	if models != nil {
		catalog = &agentconfig.CodexCatalogDesired{}
		model = models[0]
		for _, id := range models {
			catalog.Models = append(catalog.Models, agentconfig.CodexCatalogModelDesired{Model: id, SupportsParallelToolCalls: new(true), ContextWindow: new(uint64(32768))})
		}
	}
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, configuration.Draft{Name: "explicit-new-attempt", CodexProvider: &configuration.CodexProvider{AuthMode: "provider-key", Endpoint: "https://synthetic-provider.invalid/v1", Model: model, Catalog: catalog}}, []configuration.SecretMutation{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("SYNTHETIC_ADMITTED_KEY"))}})
	if err != nil {
		test.Fatal("fresh asset")
	}
	current, err := engine.CurrentCodexSelection(test.Context(), fixture.server.URL, fixture.owner, "admitted-profile")
	if err != nil {
		test.Fatal("fresh current")
	}
	defer current.Destroy()
	revision, err := current.NextSelectionRevision()
	if err != nil {
		test.Fatal("fresh counter")
	}
	flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer)
	if err != nil {
		test.Fatal("fresh child")
	}
	test.Cleanup(flow.Close)
	prepared, err := flow.PrepareCodex(test.Context(), NativeSelection{AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: "admitted-profile", SelectionRevision: revision})
	if err != nil {
		test.Fatal("fresh prepare")
	}
	defer prepared.Destroy()
	selection, err := configapply.NewCodexSelection(prepared.CopyBinding(), prepared.CopyDeclaration(), prepared.SessionExpiresAt(), current)
	if err != nil {
		test.Fatal("fresh selection")
	}
	defer selection.Destroy()
	observation, err := engine.ObserveCodex(test.Context(), selection, configapply.CodexLaunch{Executable: os.Getenv("COGNATE_Q2_NATIVE_FIXTURE"), Directory: filepath.Join(filepath.Dir(home), "workspace"), Arguments: []string{"app-server"}})
	if err != nil {
		test.Fatal("fresh physical observation")
	}
	defer observation.Destroy()
	plan, err := engine.PreviewCodexObserved(test.Context(), selection, observation)
	if err != nil {
		test.Fatal("fresh plan")
	}
	test.Cleanup(plan.Destroy)
	admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
	if err != nil {
		test.Fatal("fresh confirm")
	}
	test.Cleanup(admission.Destroy)
	return flow, plan, admission
}

func codexApplyAdmittedRound(test *testing.T, flow *NativeFlow, engine *configapply.CodexEngine, admission configapply.CodexAdmission) configapply.CodexResult {
	test.Helper()
	constraints := admission.CopyAuthorizationConstraints()
	defer constraints.Destroy()
	approval, err := NewCodexNativeApproval(constraints.ConfirmationRevision(), constraints.ApprovedDeadline(), true, true)
	if err != nil {
		test.Fatal("exact consent")
	}
	defer approval.Destroy()
	grant, err := flow.AuthorizeCodexAdmitted(test.Context(), admission, approval)
	if err != nil {
		test.Fatal("exact admitted authorization")
	}
	defer grant.Destroy()
	material, err := flow.ConsumeCodex(test.Context())
	if err != nil {
		test.Fatal("admitted consumption")
	}
	defer material.Destroy()
	data := material.CloneBytes()
	secret, err := codexconfig.NewSelectedSecret(agentconfig.CodexProviderKeySecret, data)
	clear(data)
	if err != nil {
		test.Fatal("secret")
	}
	defer secret.Destroy()
	candidate, err := engine.MaterializeCodex(test.Context(), admission, []codexconfig.SelectedSecret{secret})
	if err != nil {
		test.Fatal("exact plan render")
	}
	defer candidate.Destroy()
	result, err := engine.ApplyCodex(test.Context(), admission, candidate)
	if err != nil || result.State != configapply.CodexCompleted {
		test.Fatal("exact candidate publish")
	}
	return result
}

func TestCodexAdmittedBridgeSwitchPresentRestore(test *testing.T) {
	for _, mode := range []string{"present-restore", "nil-preserve", "retired-new-attempt"} {
		test.Run(mode, func(test *testing.T) {
			fixture, flow, engine, plan, admission, home, _ := codexAdmittedFixture(test, nil)
			if mode == "retired-new-attempt" {
				constraints := admission.CopyAuthorizationConstraints()
				defer constraints.Destroy()
				approval, _ := NewCodexNativeApproval(constraints.ConfirmationRevision(), constraints.ApprovedDeadline(), true, true)
				defer approval.Destroy()
				grant, err := flow.AuthorizeCodexAdmitted(test.Context(), admission, approval)
				if err != nil {
					test.Fatal("authorize")
				}
				defer grant.Destroy()
				material, err := flow.ConsumeCodex(test.Context())
				if err != nil {
					test.Fatal("consume")
				}
				material.Destroy()
				stop, err := engine.PreviewCodexRetireUnpublished(test.Context(), plan.View().OperationID)
				if err != nil {
					test.Fatal("retirement preview")
				}
				defer stop.Destroy()
				confirmed, err := stop.Confirm(test.Context(), true)
				if err != nil {
					test.Fatal("retirement consent")
				}
				defer confirmed.Destroy()
				if _, err := engine.RetireCodexUnpublished(test.Context(), confirmed); err != nil {
					test.Fatal("retirement")
				}
				nextFlow, _, nextAdmission := codexAdmittedNextRound(test, fixture, engine, home, []string{"second-model"})
				if result := codexApplyAdmittedRound(test, nextFlow, engine, nextAdmission); result.SelectionRevision != 1 {
					test.Fatal("retired prior attempt advanced selection")
				}
				if status, err := engine.StatusCodex(test.Context(), plan.View().OperationID); err != nil || status.State != configapply.CodexRetiredUnpublished || !status.RemoteOutcomeUnknown {
					test.Fatal("new attempt erased old unknown")
				}
				if _, err := os.Stat(filepath.Join(home, ".codex", "codex-"+plan.View().OperationID+".reserve")); err != nil {
					test.Fatal("new attempt dropped unknown reservation")
				}
				if codexCommittedConsumeCount(test, fixture) != 2 {
					test.Fatal("retired/new successful consume count")
				}
				return
			}
			if result := codexApplyAdmittedRound(test, flow, engine, admission); result.SelectionRevision != 1 {
				test.Fatal("first counter")
			}
			beforeConfig, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
			if err != nil {
				test.Fatal("first config")
			}
			defer clear(beforeConfig)
			beforeCatalog, err := os.ReadFile(filepath.Join(home, ".codex", "cognate-model-catalog.json"))
			if err != nil {
				test.Fatal("first catalog")
			}
			defer clear(beforeCatalog)
			nextFlow, nextPlan, nextAdmission := codexAdmittedNextRound(test, fixture, engine, home, []string{"second-model", "synthetic-model"})
			if result := codexApplyAdmittedRound(test, nextFlow, engine, nextAdmission); result.SelectionRevision != 2 {
				test.Fatal("second counter")
			}
			if _, err := engine.PreviewCodexRestore(test.Context(), plan.View().OperationID); !errors.Is(err, configapply.ErrCodexConflict) {
				test.Fatal("N+1 did not block stale restore")
			}
			if mode == "nil-preserve" {
				lastFlow, _, lastAdmission := codexAdmittedNextRound(test, fixture, engine, home, nil)
				if result := codexApplyAdmittedRound(test, lastFlow, engine, lastAdmission); result.SelectionRevision != 3 {
					test.Fatal("nil catalog lost owned model bookkeeping")
				}
				if codexCommittedConsumeCount(test, fixture) != 3 {
					test.Fatal("nil preserve consume count")
				}
				return
			}
			restore, err := engine.PreviewCodexRestore(test.Context(), nextPlan.View().OperationID)
			if err != nil {
				test.Fatal("present restore preview")
			}
			defer restore.Destroy()
			confirmed, err := restore.Confirm(test.Context(), true)
			if err != nil {
				test.Fatal("present restore consent")
			}
			defer confirmed.Destroy()
			result, err := engine.RestoreCodex(test.Context(), confirmed)
			if err != nil || result.State != configapply.CodexRestored || result.SelectionRevision != 3 {
				test.Fatal("present restore")
			}
			for name, want := range map[string][]byte{"config.toml": beforeConfig, "cognate-model-catalog.json": beforeCatalog} {
				actual, err := os.ReadFile(filepath.Join(home, ".codex", name))
				if err != nil || !bytes.Equal(actual, want) {
					test.Fatal("present restore did not preserve exact before bytes")
				}
				clear(actual)
			}
		})
	}
}
