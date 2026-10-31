package nativeconfig

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/server"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestCodexV2LocalActionProfile(test *testing.T) {
	fixture := newCodexFixture(test, "")
	fixture.request.Apply = true
	result, err := fixture.apply(test.Context(), codexConsent())
	if err != nil {
		test.Fatal("v2 local-label source apply", codexFailure(err))
	}
	for _, apply := range []bool{false, true} {
		var reviewed bool
		ui := codexConsent()
		ui.Preview = func(_ context.Context, view CodexPreview) error {
			reviewed = true
			if view.NativeProfileID != codexconfig.Native0166EffectiveProfileID {
				test.Fatal("local review mislabeled the recorded v2 source")
			}
			return nil
		}
		restored, err := fixture.client.RestoreCodex(test.Context(), result.Local.OperationID, apply, ui)
		if err != nil || !reviewed || restored.Preview.NativeProfileID != codexconfig.Native0166EffectiveProfileID || restored.Preview.OperationID == result.Local.OperationID {
			test.Fatal("restore label not bound to input/source identity", codexFailure(err))
		}
	}
	if fixture.authorizes.Load() != 1 || fixture.consumes.Load() != 1 || fixture.consumed(test) != 1 {
		test.Fatal("local identity read caused remote consumption")
	}
}

func TestCodexV2ActualLifecycle(test *testing.T) {
	for _, mode := range []string{"generated", "canonical", "canonical-empty", "legacy-empty", "both-absent", "lite"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newCodexFixture(test, "")
			provider := &configuration.CodexProvider{AuthMode: "provider-key", Endpoint: "https://127.0.0.1:18443/v1", Model: "synthetic-model", ReasoningEffort: new(agentconfig.CodexReasoningEffort("high"))}
			instruction := "SYNTHETIC D2 GENERATED INSTRUCTIONS"
			var retained []byte
			if mode == "generated" {
				provider.Catalog = &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "synthetic-model", BaseInstructions: new(instruction), SupportsParallelToolCalls: new(true), ReasoningLevels: new([]agentconfig.CodexReasoningEffort{"high"})}}}
			} else {
				model := map[string]any{"slug": "synthetic-model", "display_name": "Synthetic retained", "supported_reasoning_levels": []any{map[string]any{"effort": "high", "description": "Synthetic high"}}, "shell_type": "unified_exec", "visibility": "list", "supported_in_api": true, "priority": 0, "support_verbosity": false, "truncation_policy": map[string]any{"mode": "bytes", "limit": 10000}, "experimental_supported_tools": []any{}, "unknown-retained": map[string]any{"value": "UNCHANGED"}}
				switch mode {
				case "canonical":
					instruction = "SYNTHETIC D2 CANONICAL INSTRUCTIONS"
					model["model_messages"] = map[string]any{"instructions_template": instruction}
					model["base_instructions"] = "SYNTHETIC LEGACY SHADOW MUST NOT WIN"
				case "canonical-empty":
					instruction = ""
					model["model_messages"] = map[string]any{"instructions_template": ""}
					model["base_instructions"] = "SYNTHETIC LEGACY SHADOW MUST NOT WIN"
				case "legacy-empty":
					instruction = ""
					model["base_instructions"] = ""
				case "lite":
					model["base_instructions"] = "SYNTHETIC LITE"
					model["use_responses_lite"] = true
				}
				var err error
				retained, err = json.Marshal(map[string]any{"models": []any{model}, "unknown-root": "UNCHANGED"}, json.Deterministic(true))
				if err != nil || os.WriteFile(filepath.Join(fixture.home, ".codex", "cognate-model-catalog.json"), retained, 0o600) != nil {
					test.Fatal("retained catalog fixture")
				}
				if os.WriteFile(filepath.Join(fixture.home, ".codex", "config.toml"), []byte("model_catalog_json=\"cognate-model-catalog.json\"\n"), 0o600) != nil {
					test.Fatal("retained config fixture")
				}
			}
			asset, err := fixture.domain.Create(test.Context(), fixture.request.OwnerID, configuration.Draft{Name: "d2-controlled", CodexProvider: provider}, []configuration.SecretMutation{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("SYNTHETIC_Q3_KEY"))}})
			if err != nil {
				test.Fatal("controlled asset creation")
			}
			fixture.request.AssetID, fixture.request.AssetRevision = asset.Metadata.ID, asset.Metadata.Revision
			authPath := filepath.Join(fixture.home, ".codex", "auth.json")
			authBefore, err := os.ReadFile(authPath)
			if err != nil {
				test.Fatal("auth baseline")
			}
			defer clear(authBefore)
			preview, err := fixture.apply(test.Context(), CodexInteraction{})
			if mode == "both-absent" || mode == "lite" {
				if err == nil || preview.Preview.OperationID != "" || fixture.authorizes.Load() != 0 || fixture.consumes.Load() != 0 || fixture.consumed(test) != 0 {
					test.Fatal("unsupported effective instructions/lite reached secret admission")
				}
				return
			}
			if err != nil || preview.Preview.NativeProfileID != codexconfig.Native0166EffectiveProfileID || fixture.authorizes.Load() != 0 || fixture.consumes.Load() != 0 {
				test.Fatalf("effective v2 preview not presecret: invalid=%t unsupported=%t attention=%t conflict=%t requests=%d authorize=%d consume=%d profilematch=%t", errors.Is(err, configapply.ErrCodexInvalid), errors.Is(err, configapply.ErrCodexUnsupported), errors.Is(err, configapply.ErrCodexAttention), errors.Is(err, configapply.ErrCodexConflict), fixture.requests.Load(), fixture.authorizes.Load(), fixture.consumes.Load(), preview.Preview.NativeProfileID == codexconfig.Native0166EffectiveProfileID)
			}
			fixture.request.Apply = true
			result, err := fixture.apply(test.Context(), codexConsent())
			if err != nil || result.Local.State != configapply.CodexCompleted || result.Preview.NativeProfileID != codexconfig.Native0166EffectiveProfileID || fixture.authorizes.Load() != 1 || fixture.consumes.Load() != 1 || fixture.consumed(test) != 1 {
				test.Fatal("controlled effective v2 apply did not commit exactly once", codexFailure(err))
			}
			authAfter, err := os.ReadFile(authPath)
			defer clear(authAfter)
			if err != nil || !bytes.Equal(authBefore, authAfter) {
				test.Fatal("effective v2 changed local auth bytes")
			}
			catalog, err := os.ReadFile(filepath.Join(fixture.home, ".codex", "cognate-model-catalog.json"))
			if err != nil || retained != nil && !bytes.Equal(retained, catalog) {
				test.Fatal("nil catalog changed retained unknown bytes")
			}
			if directory := os.Getenv("COGNATE_D2_EXPORT"); directory != "" {
				output := filepath.Join(directory, mode)
				if !filepath.IsAbs(directory) || os.Mkdir(output, 0o700) != nil {
					test.Fatal("private consumer export directory")
				}
				for _, name := range []string{"config.toml", "auth.json", "cognate-model-catalog.json"} {
					body, err := os.ReadFile(filepath.Join(fixture.home, ".codex", name))
					if err != nil || os.WriteFile(filepath.Join(output, name), body, 0o600) != nil {
						test.Fatal("private physical-role export")
					}
					clear(body)
				}
				if os.WriteFile(filepath.Join(output, "expected-instructions.txt"), []byte(instruction), 0o600) != nil {
					test.Fatal("independent literal consumer expectation")
				}
			}
		})
	}
}

func TestCodexDefaultQualificationZeroIO(test *testing.T) {
	if _, err := configapply.QualifiedCodexNativeProfile(); err == nil {
		if os.Getenv("COGNATE_Q3_NATIVE") == "" {
			test.Fatal("unexpected qualification in normal build")
		}
		return
	}
	root := test.TempDir()
	home, stateRoot := filepath.Join(root, "missing-home"), filepath.Join(root, "missing-state")
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { calls.Add(1); writer.WriteHeader(500) }))
	defer server.Close()
	settings := server.Client().Transport.(*http.Transport).Clone()
	settings.TLSClientConfig = &tls.Config{RootCAs: settings.TLSClientConfig.RootCAs.Clone()}
	remote, err := configclient.NewNativeClient(server.URL, settings)
	if err != nil {
		test.Fatal("closed-profile TLS fixture")
	}
	defer remote.Close()
	client, err := New(home, stateRoot, server.URL, bytes.Repeat([]byte{71}, 32), remote)
	if err != nil {
		test.Fatal("client metadata constructor")
	}
	defer client.Close()
	_, err = client.ApplyCodex(context.Background(), CodexApplyRequest{ApplyRequest: ApplyRequest{OwnerID: "owner", AssetID: "asset", ProfileID: "c0", AssetRevision: 1}, Executable: "/not-read", Directory: "/not-read"}, "synthetic-session", CodexInteraction{})
	if !errors.Is(err, configapply.ErrCodexUnsupported) {
		test.Fatal("unqualified profile was not rejected before I/O")
	}
	if calls.Load() != 0 {
		test.Fatal("unqualified profile performed RPC")
	}
	for _, directory := range []string{home, stateRoot} {
		if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("unqualified entry touched filesystem")
		}
	}
}

func TestCodexPrivateContextAndRedaction(test *testing.T) {
	client, err := New("/synthetic-home", "/synthetic-state", "", bytes.Repeat([]byte{9}, 32), nil)
	if err != nil {
		test.Fatal("client metadata")
	}
	defer client.Close()
	type contextKey struct{}
	caller, cancel := context.WithCancel(context.WithValue(test.Context(), contextKey{}, "CANARY_PRIVATE"))
	ctx, done, err := client.beginCodex(caller)
	if err != nil {
		test.Fatal("private begin")
	}
	release := sync.OnceFunc(done)
	defer release()
	if ctx.Value(contextKey{}) != nil || httptrace.ContextClientTrace(ctx) != nil {
		test.Fatal("caller values inherited")
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 2*time.Minute {
		test.Fatal("unbounded private context")
	}
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		test.Fatal("caller cancellation bridge")
	}
	release()
	short, stop := context.WithTimeout(test.Context(), 100*time.Millisecond)
	defer stop()
	ctx, done, err = client.beginCodex(short)
	if err != nil {
		test.Fatal("short begin")
	}
	defer done()
	parent, _ := short.Deadline()
	bounded, _ := ctx.Deadline()
	if bounded.After(parent) {
		test.Fatal("caller deadline extended")
	}
	values := []any{CodexApplyRequest{OwnerID: "CANARY_PRIVATE", Executable: "CANARY_PRIVATE"}, CodexPreview{OperationID: "CANARY_PRIVATE"}, CodexChange{Count: 778877}, CodexLocalResult{OperationID: "CANARY_PRIVATE"}, CodexResult{Preview: CodexPreview{AssetID: "CANARY_PRIVATE"}}, CodexApproval{}, CodexInteraction{}}
	for _, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v", "%x", "%d"} {
			text := fmt.Sprintf(format, value)
			if strings.Contains(text, "CANARY_PRIVATE") || strings.Contains(text, "778877") || strings.Contains(text, "43414e415259") {
				test.Fatal("private representation leaked")
			}
		}
		data, err := json.Marshal(map[string]any{"nested": []any{value}})
		if err != nil || bytes.Contains(data, []byte("CANARY_PRIVATE")) || bytes.Contains(data, []byte("778877")) {
			test.Fatal("nested JSON privacy")
		}
		var output bytes.Buffer
		slog.New(slog.NewJSONHandler(&output, nil)).Info("safe", "nested", value)
		if bytes.Contains(output.Bytes(), []byte("CANARY_PRIVATE")) {
			test.Fatal("structured log privacy")
		}
	}
	if codexFailure(fmt.Errorf("CANARY_PRIVATE: %w", ErrDenied)) != ErrDenied || strings.Contains(codexFailure(errors.New("CANARY_PRIVATE")).Error(), "CANARY_PRIVATE") {
		test.Fatal("raw error escaped")
	}
}

type codexFixture struct {
	client                                             *Client
	remote                                             *configclient.NativeClient
	store                                              *state.Store
	auth                                               *auth.Service
	domain                                             *configuration.Service
	request                                            CodexApplyRequest
	session, home, stateRoot, database                 string
	certificate                                        string
	authorizes, consumes, statuses, requests, business atomic.Int32
	entered, drained                                   chan struct{}
}

func newCodexFixture(test *testing.T, mode string) *codexFixture {
	test.Helper()
	executable := os.Getenv("COGNATE_Q3_NATIVE")
	if executable == "" {
		test.Skip("explicit pinned ELF and private compiler overlay required; no native qualification claimed")
	}
	if _, err := configapply.QualifiedCodexNativeProfile(); err != nil {
		test.Fatal("dedicated native mode must not become Unsupported/skip")
	}
	root := test.TempDir()
	if os.Chmod(root, 0o700) != nil {
		test.Fatal("private root")
	}
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate, pair := seed.Certificate(), seed.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	seed.Close()
	if err != nil {
		test.Fatal("TLS key")
	}
	defer clear(key)
	master := bytes.Repeat([]byte{91}, 32)
	defer clear(master)
	for name, body := range map[string][]byte{"master.key": bytes.Clone(master), "tls.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), "tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})} {
		if os.WriteFile(filepath.Join(root, name), body, 0o600) != nil {
			test.Fatal("private TLS fixture")
		}
		clear(body)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal("loopback listener")
	}
	backend := "https://" + listener.Addr().String()
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 10 * time.Second}
	fixture := &codexFixture{entered: make(chan struct{}), drained: make(chan struct{})}
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.requests.Add(1)
		authorize := strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization")
		consume := strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization")
		if authorize {
			fixture.authorizes.Add(1)
		}
		if consume {
			fixture.consumes.Add(1)
		}
		if strings.HasSuffix(request.URL.Path, "/GetNativeMaterializationStatus") {
			fixture.statuses.Add(1)
		}
		forward := request.Clone(request.Context())
		forward.URL = request.URL.Clone()
		destination, _ := url.Parse(backend)
		forward.URL.Scheme = destination.Scheme
		forward.URL.Host = destination.Host
		forward.Host = destination.Host
		forward.RequestURI = ""
		response, err := transport.RoundTrip(forward)
		if err != nil {
			writer.WriteHeader(503)
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		defer clear(body)
		if err != nil || len(body) > 2<<20 {
			writer.WriteHeader(503)
			return
		}
		lose := mode == "lost-authorize" && authorize || (mode == "lost-consume" || mode == "close-consume" || mode == "cancel-consume") && consume
		if lose {
			if response.StatusCode != 200 {
				test.Error("fault boundary requires actual successful server transaction")
			}
			close(fixture.entered)
			defer close(fixture.drained)
			if mode == "close-consume" || mode == "cancel-consume" {
				<-request.Context().Done()
			}
			writer.Header().Set("Content-Type", "application/proto")
			writer.WriteHeader(200)
			return
		}
		for name, values := range response.Header {
			for _, value := range values {
				writer.Header().Add(name, value)
			}
		}
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(body)
	}))
	proxy.StartTLS()
	ctx, cancel := context.WithCancel(test.Context())
	app, err := server.Open(ctx, server.Config{DataDirectory: filepath.Join(root, "data"), MasterKeyFile: filepath.Join(root, "master.key"), TLSCertificate: filepath.Join(root, "tls.pem"), TLSKey: filepath.Join(root, "tls.key"), PublicOrigin: proxy.URL, SourceRefreshInterval: new(time.Duration(0))})
	if err != nil {
		cancel()
		proxy.Close()
		listener.Close()
		test.Fatal("actual App open")
	}
	done := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() { done <- app.Serve(ctx, listener) })
	test.Cleanup(func() {
		cancel()
		if <-done != nil {
			test.Error("App serve join")
		}
		workers.Wait()
		if app.Close() != nil {
			test.Error("App close")
		}
		proxy.Close()
		transport.CloseIdleConnections()
	})
	fixture.database = filepath.Join(root, "data", "server.db")
	fixture.certificate = filepath.Join(root, "tls.pem")
	fixture.store, err = state.Open(test.Context(), fixture.database, master)
	if err != nil {
		test.Fatal("second physical Store")
	}
	test.Cleanup(func() {
		if fixture.store.Close() != nil {
			test.Error("Store close")
		}
	})
	fixture.auth, err = auth.NewService(fixture.store, master)
	if err != nil {
		test.Fatal("actual auth")
	}
	token, owner, err := fixture.auth.LoginOIDC(test.Context(), "https://synthetic.identity", "codex-q3-owner", "", "")
	if err != nil {
		test.Fatal("synthetic owner session")
	}
	fixture.session = token.Reveal()
	fixture.domain = configuration.New(fixture.store)
	provider := &configuration.CodexProvider{AuthMode: "provider-key", Endpoint: "https://synthetic-provider.invalid/v1", Model: "synthetic-model", Catalog: &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "synthetic-model", DisplayName: new("Synthetic model"), ContextWindow: new(uint64(32768)), SupportsParallelToolCalls: new(true), InputModalities: new([]agentconfig.CodexModality{"text"}), BaseInstructions: new("SYNTHETIC INSTRUCTIONS"), ReasoningLevels: new([]agentconfig.CodexReasoningEffort{"low", "high"}), DefaultReasoningLevel: new(agentconfig.CodexReasoningEffort("low"))}}}}
	asset, err := fixture.domain.Create(test.Context(), owner, configuration.Draft{Name: "synthetic-q3", CodexProvider: provider}, []configuration.SecretMutation{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("SYNTHETIC_Q3_KEY"))}})
	if err != nil {
		test.Fatal("real encrypted asset")
	}
	fixture.home, fixture.stateRoot = filepath.Join(root, "home"), filepath.Join(root, "state")
	workspace := filepath.Join(root, "workspace")
	for _, directory := range []string{fixture.home, fixture.stateRoot, workspace, filepath.Join(fixture.home, ".codex")} {
		if os.Mkdir(directory, 0o700) != nil {
			test.Fatal("synthetic directories")
		}
	}
	identity := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"synthetic@example.invalid"}`)) + ".synthetic"
	login, err := json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]any{"id_token": identity, "access_token": "SYNTHETIC_ACCESS", "refresh_token": "SYNTHETIC_REFRESH", "account_id": "synthetic-account"}, "last_refresh": time.Now().Add(-8*24*time.Hour + 2*time.Minute).UTC().Format(time.RFC3339Nano)})
	if err != nil || os.WriteFile(filepath.Join(fixture.home, ".codex", "auth.json"), login, 0o600) != nil {
		test.Fatal("fresh synthetic File OAuth")
	}
	clear(login)
	settings := proxy.Client().Transport.(*http.Transport).Clone()
	settings.TLSClientConfig = &tls.Config{RootCAs: settings.TLSClientConfig.RootCAs.Clone()}
	fixture.remote, err = configclient.NewNativeClient(proxy.URL, settings)
	if err != nil {
		test.Fatal("native TLS client")
	}
	test.Cleanup(fixture.remote.Close)
	fixture.client, err = New(fixture.home, fixture.stateRoot, proxy.URL, bytes.Repeat([]byte{0x67}, 32), fixture.remote)
	if err != nil {
		test.Fatal("native orchestration client")
	}
	test.Cleanup(func() {
		if fixture.client.Close() != nil {
			test.Error("client close")
		}
	})
	fixture.request = CodexApplyRequest{OwnerID: owner, AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: "synthetic-c0", Executable: executable, Directory: workspace}
	return fixture
}

func codexConsent() CodexInteraction {
	return CodexInteraction{Approve: func(_ context.Context, _ CodexPreview, slots []agentconfig.CodexSecretSlot) (CodexApproval, error) {
		return CodexApproval{Confirmed: true, ApprovedSlots: slots}, nil
	}}
}

func TestCodexActualCrashChild(test *testing.T) {
	if os.Getenv("COGNATE_Q3_CHILD") != "1" {
		test.Skip("private child process only")
	}
	certificate, err := os.ReadFile(os.Getenv("COGNATE_Q3_CERT"))
	if err != nil {
		test.Fatal("synthetic CA read")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certificate) {
		test.Fatal("synthetic CA")
	}
	remote, err := configclient.NewNativeClient(os.Getenv("COGNATE_Q3_ORIGIN"), &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}})
	if err != nil {
		test.Fatal("child TLS client")
	}
	defer remote.Close()
	client, err := New(os.Getenv("COGNATE_Q3_HOME"), os.Getenv("COGNATE_Q3_STATE"), os.Getenv("COGNATE_Q3_ORIGIN"), bytes.Repeat([]byte{0x67}, 32), remote)
	if err != nil {
		test.Fatal("child native client")
	}
	defer client.Close()
	request := CodexApplyRequest{OwnerID: os.Getenv("COGNATE_Q3_OWNER"), AssetID: os.Getenv("COGNATE_Q3_ASSET"), AssetRevision: 1, ProfileID: "synthetic-c0", Apply: true, Executable: os.Getenv("COGNATE_Q3_NATIVE"), Directory: os.Getenv("COGNATE_Q3_DIRECTORY")}
	ui := codexConsent()
	ui.Preview = func(_ context.Context, view CodexPreview) error {
		return os.WriteFile(os.Getenv("COGNATE_Q3_MARKER"), []byte(view.OperationID), 0o600)
	}
	_, err = client.ApplyCodex(test.Context(), request, os.Getenv("COGNATE_Q3_SESSION"), ui)
	test.Fatalf("expected real durable crash boundary; got safeclass=%v", codexFailure(err))
}

func TestCodexActualCrashRecovery(test *testing.T) {
	if os.Getenv("COGNATE_Q3_CRASH_OVERLAY") != "1" {
		test.Skip("explicit compiler fault overlay required for child crash oracle")
	}
	for _, boundary := range []string{"candidates-durable", "entry-published-2"} {
		test.Run(boundary, func(test *testing.T) {
			fixture := newCodexFixture(test, "")
			marker := filepath.Join(fixture.stateRoot, "child-operation")
			ctx, cancel := context.WithTimeout(test.Context(), 45*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexActualCrashChild$", "-test.count=1", "-test.timeout=40s")
			child.Env = []string{"HOME=" + fixture.home, "TMPDIR=" + os.TempDir(), "PATH=/usr/bin:/bin", "COGNATE_Q3_CHILD=1", "COGNATE_Q3_NATIVE=" + fixture.request.Executable, "COGNATE_Q3_HOME=" + fixture.home, "COGNATE_Q3_STATE=" + fixture.stateRoot, "COGNATE_Q3_ORIGIN=" + fixture.client.private().origin, "COGNATE_Q3_OWNER=" + fixture.request.OwnerID, "COGNATE_Q3_ASSET=" + fixture.request.AssetID, "COGNATE_Q3_SESSION=" + fixture.session, "COGNATE_Q3_DIRECTORY=" + fixture.request.Directory, "COGNATE_Q3_CERT=" + fixture.certificate, "COGNATE_Q3_MARKER=" + marker, "COGNATE_Q3_CRASH=" + boundary}
			var output bytes.Buffer
			child.Stdout = &output
			child.Stderr = &output
			err := child.Run()
			clear(output.Bytes())
			failure, ok := errors.AsType[*exec.ExitError](err)
			if !ok || failure.ExitCode() != 73 {
				test.Fatal("compiled durable boundary did not terminate real child at expected checkpoint")
			}
			operation, err := os.ReadFile(marker)
			if err != nil || len(operation) != 32 {
				test.Fatal("child operation identity")
			}
			status, err := fixture.client.StatusCodex(test.Context(), string(operation))
			if err != nil || status.State == configapply.CodexCompleted || status.State == configapply.CodexRestored {
				test.Fatal("partial/candidate crash falsely completed")
			}
			if boundary == "entry-published-2" {
				if _, err := os.Stat(filepath.Join(fixture.home, ".codex", "cognate-model-catalog.json")); err != nil {
					test.Fatal("actual partial catalog not published")
				}
				if _, err := os.Stat(filepath.Join(fixture.home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("actual partial config unexpectedly published")
				}
			}
			if _, err := fixture.client.RecoverCodex(test.Context(), string(operation), false, CodexInteraction{}); err != nil {
				test.Fatal("recovery preview")
			}
			refused, err := fixture.client.RecoverCodex(test.Context(), string(operation), true, CodexInteraction{})
			if err == nil || refused.Local.State == configapply.CodexCompleted {
				test.Fatal("recovery without fresh confirmation")
			}
			recovered, err := fixture.client.RecoverCodex(test.Context(), string(operation), true, codexConsent())
			if err != nil || recovered.Local.State != configapply.CodexCompleted || recovered.Local.SelectionRevision != 1 {
				test.Fatalf("actual recovery failed safeclass=%v state=%d", codexFailure(err), recovered.Local.State)
			}
			if fixture.authorizes.Load() != 1 || fixture.consumes.Load() != 1 || fixture.consumed(test) != 1 {
				test.Fatal("restart recovery replayed remote materialization")
			}
		})
	}
}

func TestCodexActualNPlusOneAndClosePreview(test *testing.T) {
	test.Run("selection-winner", func(test *testing.T) {
		fixture := newCodexFixture(test, "")
		fixture.request.Apply = true
		first, err := fixture.apply(test.Context(), codexConsent())
		if err != nil {
			test.Fatal("first managed apply")
		}
		second, err := fixture.apply(test.Context(), codexConsent())
		if err != nil || second.Local.SelectionRevision != 2 {
			test.Fatal("new managed selection did not win")
		}
		before, err := os.ReadFile(filepath.Join(fixture.home, ".codex", "config.toml"))
		if err != nil {
			test.Fatal("winner config")
		}
		defer clear(before)
		if _, err := fixture.client.RestoreCodex(test.Context(), first.Local.OperationID, true, codexConsent()); err == nil {
			test.Fatal("old N overwrote winning N+1")
		}
		after, _ := os.ReadFile(filepath.Join(fixture.home, ".codex", "config.toml"))
		defer clear(after)
		if !bytes.Equal(before, after) || fixture.consumed(test) != 2 || fixture.consumes.Load() != 2 || fixture.authorizes.Load() != 2 {
			test.Fatal("stale restore changed winner or consumed remotely")
		}
	})
	test.Run("preview-close", func(test *testing.T) {
		fixture := newCodexFixture(test, "")
		fixture.request.Apply = true
		entered := make(chan struct{})
		finished := make(chan error, 1)
		var workers sync.WaitGroup
		workers.Go(func() {
			_, err := fixture.apply(test.Context(), CodexInteraction{Preview: func(ctx context.Context, _ CodexPreview) error { close(entered); <-ctx.Done(); return ctx.Err() }, Approve: codexConsent().Approve})
			finished <- err
		})
		defer workers.Wait()
		select {
		case <-entered:
		case <-time.After(15 * time.Second):
			test.Fatal("human review not entered")
		}
		if _, err := fixture.client.StatusCodex(test.Context(), "unused"); !errors.Is(err, ErrBusy) {
			test.Fatal("shared client admission not busy")
		}
		if fixture.client.Close() != nil {
			test.Fatal("Close")
		}
		select {
		case err := <-finished:
			if err == nil {
				test.Fatal("closed human review proceeded")
			}
		case <-time.After(time.Second):
			test.Fatal("Close did not join review")
		}
		if fixture.authorizes.Load() != 0 || fixture.consumes.Load() != 0 || fixture.consumed(test) != 0 {
			test.Fatal("Close authorized material")
		}
	})
}
func (fixture *codexFixture) apply(ctx context.Context, ui CodexInteraction) (CodexResult, error) {
	fixture.business.Add(1)
	return fixture.client.ApplyCodex(ctx, fixture.request, fixture.session, ui)
}
func (fixture *codexFixture) consumed(test *testing.T) int {
	test.Helper()
	database, err := sql.Open("sqlite3", "file:"+fixture.database+"?mode=ro")
	if err != nil {
		test.Fatal("receipt database")
	}
	defer database.Close()
	var count int
	if database.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM native_materialization_grants WHERE consumed_ms>0").Scan(&count) != nil {
		test.Fatal("actual consume receipt")
	}
	return count
}

func TestCodexActualLifecycle(test *testing.T) {
	fixture := newCodexFixture(test, "")
	authPath := filepath.Join(fixture.home, ".codex", "auth.json")
	authBefore, _ := os.ReadFile(authPath)
	defer clear(authBefore)
	var traces atomic.Int32
	caller := httptrace.WithClientTrace(test.Context(), &httptrace.ClientTrace{WroteHeaderField: func(string, []string) { traces.Add(1) }})
	preview, err := fixture.apply(caller, CodexInteraction{})
	if err != nil || preview.Preview.OperationID == "" || preview.Preview.NativeProfileID == "" || preview.Preview.C0ProfileID != fixture.request.ProfileID || !preview.Preview.AuthKept || !slices.Equal(preview.Preview.RequiredSlots, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}) || fixture.authorizes.Load() != 0 || fixture.consumes.Load() != 0 || fixture.consumed(test) != 0 {
		test.Fatalf("default preview failed safeclass=%v", codexFailure(err))
	}
	for _, name := range []string{"config.toml", "cognate-model-catalog.json"} {
		if _, err := os.Stat(filepath.Join(fixture.home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("preview target write")
		}
	}
	fixture.request.Apply = true
	result, err := fixture.apply(caller, codexConsent())
	if err != nil || result.Local.State != configapply.CodexCompleted || result.Local.SelectionRevision != 1 || result.RemoteState != configclient.NativeConsumed || fixture.authorizes.Load() != 1 || fixture.consumes.Load() != 1 || fixture.consumed(test) != 1 || fixture.business.Load() != 2 || traces.Load() != 0 {
		test.Fatalf("actual native apply refused safeclass=%v state=%d", codexFailure(err), result.Local.State)
	}
	config, err := os.ReadFile(filepath.Join(fixture.home, ".codex", "config.toml"))
	defer clear(config)
	if err != nil || !bytes.Contains(config, []byte("SYNTHETIC_Q3_KEY")) {
		test.Fatal("actual config not materialized")
	}
	catalog, err := os.ReadFile(filepath.Join(fixture.home, ".codex", "cognate-model-catalog.json"))
	defer clear(catalog)
	if err != nil || !bytes.Contains(catalog, []byte("SYNTHETIC INSTRUCTIONS")) {
		test.Fatal("actual catalog not materialized")
	}
	if fixture.client.Close() != nil {
		test.Fatal("client restart close")
	}
	fixture.client, err = New(fixture.home, fixture.stateRoot, fixture.client.private().origin, bytes.Repeat([]byte{0x67}, 32), fixture.remote)
	if err != nil {
		test.Fatal("restart client")
	}
	test.Cleanup(func() { fixture.client.Close() })
	status, err := fixture.client.StatusCodex(caller, result.Local.OperationID)
	if err != nil || status.State != configapply.CodexCompleted {
		test.Fatal("restart status")
	}
	restorePreview, err := fixture.client.RestoreCodex(caller, result.Local.OperationID, false, CodexInteraction{})
	if err != nil || restorePreview.Preview.OperationID == result.Local.OperationID {
		test.Fatal("fresh restore plan")
	}
	restored, err := fixture.client.RestoreCodex(caller, result.Local.OperationID, true, codexConsent())
	if err != nil || restored.Local.State != configapply.CodexRestored || restored.Local.SelectionRevision != 2 {
		test.Fatalf("first RestoreMissing refused safeclass=%v state=%d", codexFailure(err), restored.Local.State)
	}
	for _, name := range []string{"config.toml", "cognate-model-catalog.json"} {
		if _, err := os.Stat(filepath.Join(fixture.home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("restore did not recover missing")
		}
	}
	authAfter, _ := os.ReadFile(authPath)
	defer clear(authAfter)
	if !bytes.Equal(authBefore, authAfter) || fixture.authorizes.Load() != 1 || fixture.consumes.Load() != 1 || fixture.consumed(test) != 1 {
		test.Fatal("auth Keep or restore zero-consume violated")
	}
}

func TestCodexActualRefusalsAndCallbackIsolation(test *testing.T) {
	for _, mode := range []string{"unapproved", "mutated-slots", "owner", "revision", "origin", "stale-after-review", "revoked-after-review", "foreign-auth", "mutated-view"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newCodexFixture(test, "")
			fixture.request.Apply = true
			ui := codexConsent()
			switch mode {
			case "unapproved":
				ui = CodexInteraction{}
			case "mutated-slots":
				ui.Approve = func(_ context.Context, view CodexPreview, slots []agentconfig.CodexSecretSlot) (CodexApproval, error) {
					view.RequiredSlots[0] = "foreign"
					slots[0] = "foreign"
					return CodexApproval{Confirmed: true, ApprovedSlots: slots}, nil
				}
			case "owner":
				fixture.request.OwnerID = "00000000-0000-0000-0000-000000000001"
			case "revision":
				fixture.request.AssetRevision++
			case "origin":
				fixture.client.private().origin = "https://different.synthetic.invalid"
			case "stale-after-review":
				ui.Preview = func(ctx context.Context, _ CodexPreview) error {
					view, err := fixture.domain.Edit(ctx, fixture.request.OwnerID, fixture.request.AssetID)
					if err != nil {
						test.Fatal("actual asset read")
					}
					_, err = fixture.domain.Update(ctx, fixture.request.OwnerID, fixture.request.AssetID, view.Metadata.Revision, configuration.Draft{Name: "changed-after-preview", CodexProvider: view.CodexProvider}, nil)
					if err != nil {
						test.Fatal("actual asset CAS mutation")
					}
					return nil
				}
			case "revoked-after-review":
				ui.Preview = func(ctx context.Context, _ CodexPreview) error {
					if fixture.auth.Logout(ctx, fixture.session) != nil {
						test.Fatal("actual logout")
					}
					return nil
				}
			case "foreign-auth":
				ui.Preview = func(_ context.Context, _ CodexPreview) error {
					if os.WriteFile(filepath.Join(fixture.home, ".codex", "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"SYNTHETIC_FOREIGN"}`), 0o600) != nil {
						test.Fatal("foreign auth writer")
					}
					return nil
				}
			case "mutated-view":
				ui.Preview = func(_ context.Context, view CodexPreview) error {
					view.OperationID = "foreign"
					view.AssetRevision = 999
					view.RequiredSlots[0] = "foreign"
					if len(view.Changes) > 0 {
						view.Changes[0].Action = 0
					}
					return nil
				}
			}
			result, err := fixture.apply(test.Context(), ui)
			if mode == "mutated-view" {
				if err != nil || result.Local.State != configapply.CodexCompleted {
					test.Fatal("callback view mutated D authority")
				}
				return
			}
			wantAuthorize := int32(0)
			wantUnknown := false
			if mode == "stale-after-review" || mode == "revoked-after-review" {
				wantAuthorize = 1
				wantUnknown = true
			}
			if err == nil || result.RemoteUnknown != wantUnknown || wantUnknown && !errors.Is(err, configclient.ErrNativeUnknown) || fixture.statuses.Load() > 1 || fixture.authorizes.Load() != wantAuthorize || fixture.consumes.Load() != 0 || fixture.consumed(test) != 0 {
				test.Fatal("invalid input/approval crossed consume boundary")
			}
		})
	}
}

func TestCodexActualUnknownAndStop(test *testing.T) {
	for _, mode := range []string{"lost-authorize", "lost-consume", "close-consume", "cancel-consume"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newCodexFixture(test, mode)
			fixture.request.Apply = true
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			type response struct {
				result CodexResult
				err    error
			}
			completed := make(chan response, 1)
			var workers sync.WaitGroup
			workers.Go(func() { result, err := fixture.apply(ctx, codexConsent()); completed <- response{result, err} })
			defer workers.Wait()
			select {
			case <-fixture.entered:
			case <-time.After(15 * time.Second):
				test.Fatal("actual fault boundary not reached")
			}
			if mode == "close-consume" {
				if fixture.client.Close() != nil {
					test.Fatal("Close")
				}
			} else if mode == "cancel-consume" {
				cancel()
			}
			var outcome response
			select {
			case outcome = <-completed:
			case <-time.After(15 * time.Second):
				test.Fatal("cancel/Close failed to join")
			}
			select {
			case <-fixture.drained:
			case <-time.After(time.Second):
				test.Fatal("actual handler failed to drain")
			}
			want := int32(1)
			if mode == "lost-authorize" {
				want = 0
			}
			if !errors.Is(outcome.err, configclient.ErrNativeUnknown) || !outcome.result.RemoteUnknown || fixture.business.Load() != 1 || fixture.authorizes.Load() != 1 || fixture.consumes.Load() != want || fixture.consumed(test) != int(want) || fixture.statuses.Load() > 1 {
				test.Fatalf("unknown/replay boundary changed safeclass=%v", codexFailure(outcome.err))
			}
			if mode == "close-consume" {
				return
			}
			operation := outcome.result.Preview.OperationID
			previewUI := CodexInteraction{Preview: func(_ context.Context, view CodexPreview) error {
				if view.OperationID != operation {
					test.Fatal("stop operation mismatch")
				}
				return nil
			}}
			if mode == "lost-authorize" {
				if _, err := fixture.client.AbandonCodex(test.Context(), operation, false, previewUI); err != nil {
					test.Fatal("safe abandon preview")
				}
				stopped, err := fixture.client.AbandonCodex(test.Context(), operation, true, codexConsent())
				if err != nil || stopped.Local.State != configapply.CodexAbandonedBeforeWrite {
					test.Fatal("safe abandon after unknown authorize")
				}
			} else {
				if _, err := fixture.client.AbandonCodex(test.Context(), operation, true, codexConsent()); err == nil {
					test.Fatal("consumed operation falsely abandoned")
				}
				stopped, err := fixture.client.RetireCodexUnpublished(test.Context(), operation, true, codexConsent())
				if err != nil || stopped.Local.State != configapply.CodexRetiredUnpublished || !stopped.Local.RemoteOutcomeUnknown {
					test.Fatal("unknown unpublished retirement lost truth")
				}
			}
			if fixture.authorizes.Load() != 1 || fixture.consumes.Load() != want || fixture.consumed(test) != int(want) {
				test.Fatal("local stop replayed remote materialization")
			}
		})
	}
}
