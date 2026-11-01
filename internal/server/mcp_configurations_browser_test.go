package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/x509"
	"encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1/mcpv1connect"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type mcpBrowserFixture struct {
	app                     *App
	public, control         *httptest.Server
	client                  mcpv1connect.MCPConfigurationServiceClient
	owner, session, foreign string
	password, capability    string
	mutex                   sync.Mutex
	mode, procedure         string
	requests                map[string]int
	stages                  []string
	active                  atomic.Int64
	stopped                 chan struct{}
	stop                    func()
	stopReceived            atomic.Bool
	finish                  func() error
}

func mcpBrowserRequest[Message any](message *Message, session string) *connect.Request[Message] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+session)
	return request
}

func newMCPBrowserFixture(test *testing.T) *mcpBrowserFixture {
	test.Helper()
	root, err := os.MkdirTemp("", "cognate-mcp-app-")
	if err != nil {
		test.Fatal("private fixture root unavailable")
	}
	test.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			test.Error("fixture root cleanup failed")
		}
	})
	fixture := &mcpBrowserFixture{capability: rand.Text(), password: rand.Text(), requests: make(map[string]int), stopped: make(chan struct{})}
	fixture.stop = sync.OnceFunc(func() { close(fixture.stopped) })
	fixture.public = httptest.NewUnstartedServer(nil)
	fixture.public.StartTLS()
	test.Cleanup(fixture.public.Close)
	pair := fixture.public.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		test.Fatal("synthetic TLS key unavailable")
	}
	certificatePath, keyPath := filepath.Join(root, "tls.crt"), filepath.Join(root, "tls.key")
	if err := errors.Join(os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]}), 0600), os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600)); err != nil {
		test.Fatal("synthetic TLS files unavailable")
	}
	clear(key)
	ctx, cancel := context.WithCancel(test.Context())
	app, err := openApp(ctx, Config{DataDirectory: filepath.Join(root, "data"), MasterKeyFile: filepath.Join(root, "master.key"), InitializeMasterKey: true, TLSCertificate: certificatePath, TLSKey: keyPath, PublicOrigin: fixture.public.URL, SourceRefreshInterval: new(time.Duration(0))}, appRuntimeHooks{observe: func(stage string) {
		fixture.mutex.Lock()
		fixture.stages = append(fixture.stages, stage)
		fixture.mutex.Unlock()
	}})
	if err != nil {
		cancel()
		test.Fatal("real App initialization failed", err)
	}
	fixture.app = app
	var workers sync.WaitGroup
	var serveError error
	var transport *http.Transport
	fixture.finish = sync.OnceValue(func() error {
		fixture.public.Close()
		if fixture.control != nil {
			fixture.control.Close()
		}
		cancel()
		closeError := app.Close()
		workers.Wait()
		if transport != nil {
			transport.CloseIdleConnections()
		}
		fixture.mutex.Lock()
		defer fixture.mutex.Unlock()
		if fixture.active.Load() != 0 || !slices.Contains(fixture.stages, "serve-tail") || !slices.Contains(fixture.stages, "logs-closed") || !slices.Contains(fixture.stages, "lease-closed") {
			return errors.Join(closeError, serveError, errors.New("fixture lifecycle did not drain"))
		}
		if slices.Index(fixture.stages, "serve-tail") > slices.Index(fixture.stages, "store-closed") || slices.Index(fixture.stages, "logs-closed") > slices.Index(fixture.stages, "lease-closed") {
			return errors.New("fixture resource release ordering failed")
		}
		return errors.Join(closeError, serveError)
	})
	test.Cleanup(func() {
		if err := fixture.finish(); err != nil {
			test.Error("real App/Serve/worker cleanup failed", err)
		}
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal("fixture listener unavailable")
	}
	workers.Go(func() { serveError = app.Serve(ctx, listener) })
	backend := "https://" + listener.Addr().String()
	target, _ := url.Parse(backend)
	transport = fixture.public.Client().Transport.(*http.Transport).Clone()
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.ModifyResponse = func(response *http.Response) error {
		body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		closeError := response.Body.Close()
		if err != nil || closeError != nil || len(body) > 2<<20 {
			return errors.New("bounded fixture response failed")
		}
		for _, canary := range []string{"WEB-MCP-STORED-CANARY", "WEB-MCP-NEW-CANARY"} {
			if bytes.Contains(body, []byte(canary)) {
				test.Error("secret leaked in real RPC response")
				return errors.New("fixture privacy guard")
			}
		}
		fixture.mutex.Lock()
		lost := fixture.mode == "lost" && strings.HasSuffix(response.Request.URL.Path, "/"+fixture.procedure)
		fixture.mutex.Unlock()
		if lost {
			if response.StatusCode != http.StatusOK {
				test.Error("lost reply did not follow real successful handler")
			}
			return errors.New("synthetic reply lost after commit")
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(writer, `{"code":"unavailable","message":"synthetic transport failure"}`)
	}
	fixture.public.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.active.Add(1)
		defer fixture.active.Add(-1)
		fixture.mutex.Lock()
		fixture.requests[request.URL.Path]++
		mode, procedure := fixture.mode, fixture.procedure
		fixture.mutex.Unlock()
		if mode == "slow" && strings.HasSuffix(request.URL.Path, "/"+procedure) {
			select {
			case <-time.After(2 * time.Second):
			case <-request.Context().Done():
				return
			}
		}
		proxy.ServeHTTP(writer, request)
	})
	master, err := state.ReadMasterKey(filepath.Join(root, "master.key"))
	if err != nil {
		test.Fatal("synthetic authentication key unavailable")
	}
	defer clear(master)
	authentication, err := auth.NewService(app.store, master)
	if err != nil {
		test.Fatal("synthetic authentication unavailable")
	}
	for _, name := range []string{"mcp-owner", "mcp-foreign"} {
		owner, err := authentication.Register(ctx, name, []byte(fixture.password))
		if err != nil {
			test.Fatal("synthetic account unavailable")
		}
		session, err := authentication.Login(ctx, name, []byte(fixture.password))
		if err != nil {
			test.Fatal("synthetic session unavailable")
		}
		if name == "mcp-owner" {
			fixture.owner, fixture.session = owner, session.Reveal()
		} else {
			fixture.foreign = session.Reveal()
		}
	}
	fixture.client = mcpv1connect.NewMCPConfigurationServiceClient(&http.Client{Transport: transport, Timeout: 10 * time.Second}, backend)
	for _, seed := range []struct{ name, session string }{{"Seed private MCP", fixture.session}, {"Foreign private MCP", fixture.foreign}} {
		_, err := fixture.client.CreateAsset(ctx, mcpBrowserRequest(&mcpv1.CreateAssetRequest{Draft: &mcpv1.Draft{Name: seed.name, AgentId: "claude", Document: &mcpv1.Document{Servers: []*mcpv1.Server{{Id: "local", Name: "local", Enabled: new(true), Connection: &mcpv1.Server_Stdio{Stdio: &mcpv1.Stdio{Command: "synthetic-not-executed", Env: &mcpv1.NamedSlots{Values: []*mcpv1.NamedSlot{{Name: "TOKEN", SlotRef: "env/local/TOKEN"}}}}}}}}}, Secrets: []*mcpv1.SecretMutation{{SlotId: "env/local/TOKEN", Action: &mcpv1.SecretMutation_Replace{Replace: []byte("WEB-MCP-STORED-CANARY")}}}}, seed.session))
		if err != nil {
			test.Fatal("real MCP seed failed", connect.CodeOf(err))
		}
	}
	if _, err := app.store.CreateConfigurationAsset(ctx, fixture.owner, state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindMCP, Name: "Unknown private MCP", SchemaVersion: 65535, Payload: state.NewConfigurationPayload([]byte(`{"future":true}`))}); err != nil {
		test.Fatal("unknown schema seed failed")
	}
	fixture.control = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(fixture.capability)) != 1 || request.Header.Get("Origin") != "" || request.URL.RawQuery != "" {
			http.Error(writer, "control denied", 403)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /login":
			_ = json.MarshalWrite(writer, map[string]string{"username": "mcp-owner", "foreign": "mcp-foreign", "password": fixture.password})
		case "GET /state":
			assets, err := configuration.NewMCP(app.store).List(ctx, fixture.owner)
			if err != nil {
				http.Error(writer, "state unavailable", 500)
				return
			}
			rows := make([]map[string]any, 0, len(assets))
			for _, asset := range assets {
				row := map[string]any{"id": asset.ID, "name": asset.Name, "revision": asset.Revision, "schema": asset.SchemaVersion}
				if asset.SchemaVersion == 1 {
					view, err := configuration.NewMCP(app.store).ReadDraft(ctx, fixture.owner, asset.ID)
					if err != nil {
						http.Error(writer, "draft unavailable", 500)
						return
					}
					row["declaration"], row["slots"] = view.Draft.Document, view.Secrets
				}
				rows = append(rows, row)
			}
			fixture.mutex.Lock()
			defer fixture.mutex.Unlock()
			_ = json.MarshalWrite(writer, map[string]any{"assets": rows, "requests": fixture.requests})
		case "POST /mode":
			var input struct{ Mode, Procedure string }
			if json.UnmarshalRead(http.MaxBytesReader(writer, request.Body, 512), &input, json.RejectUnknownMembers(true)) != nil || !slices.Contains([]string{"normal", "lost", "slow"}, input.Mode) || input.Mode != "normal" && !slices.Contains([]string{"ReadDraft", "Preview", "CreateAsset", "UpdateAsset", "CopyAsset", "DeleteAsset"}, input.Procedure) {
				http.Error(writer, "invalid mode", 400)
				return
			}
			fixture.mutex.Lock()
			fixture.mode, fixture.procedure = input.Mode, input.Procedure
			fixture.mutex.Unlock()
		case "POST /external":
			var input struct{ ID string }
			if json.UnmarshalRead(http.MaxBytesReader(writer, request.Body, 512), &input) != nil {
				http.Error(writer, "invalid input", 400)
				return
			}
			view, err := fixture.client.ReadDraft(ctx, mcpBrowserRequest(&mcpv1.ReadDraftRequest{Id: input.ID}, fixture.session))
			if err == nil {
				view.Msg.View.Draft.Name += " external"
				_, err = fixture.client.UpdateAsset(ctx, mcpBrowserRequest(&mcpv1.UpdateAssetRequest{Id: input.ID, ExpectedRevision: view.Msg.View.Asset.Revision, Draft: view.Msg.View.Draft}, fixture.session))
			}
			if err != nil {
				http.Error(writer, "external CAS failed", 500)
			}
		case "GET /privacy":
			checked, leaked := 0, false
			err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkError error) error {
				if walkError != nil {
					return walkError
				}
				if entry.IsDir() {
					return nil
				}
				info, err := entry.Info()
				if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
					return errors.New("privacy probe requires bounded regular fixture files")
				}
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				checked++
				leaked = leaked || bytes.Contains(content, []byte("WEB-MCP-STORED-CANARY")) || bytes.Contains(content, []byte("WEB-MCP-NEW-CANARY"))
				clear(content)
				return nil
			})
			if err != nil {
				http.Error(writer, "privacy probe failed", 500)
				return
			}
			_ = json.MarshalWrite(writer, map[string]any{"checkedPrivateFiles": checked, "plaintextCanaryFound": leaked})
		case "POST /stop":
			fixture.stopReceived.Store(true)
			_, _ = io.WriteString(writer, "stop received")
			fixture.stop()
		default:
			http.NotFound(writer, request)
		}
	}))
	return fixture
}

func TestMCPConfigurationsBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_MCP_CONFIGURATIONS_UI_DIR")
	if directory == "" {
		test.Skip("isolated real App browser entrypoint")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !filepath.IsAbs(directory) {
		test.Fatal("readiness requires a private directory")
	}
	fixture := newMCPBrowserFixture(test)
	file, err := os.OpenFile(filepath.Join(directory, "ready.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		test.Fatal("readiness unavailable")
	}
	err = json.MarshalWrite(file, map[string]string{"url": fixture.public.URL, "controlURL": fixture.control.URL, "capability": fixture.capability})
	if errors.Join(err, file.Close()) != nil {
		test.Fatal("readiness not written")
	}
	select {
	case <-fixture.stopped:
	case <-time.After(12 * time.Minute):
		test.Fatal("explicit stop was not received")
	}
	if !fixture.stopReceived.Load() {
		test.Fatal("stop receipt absent")
	}
	if err := fixture.finish(); err != nil {
		test.Fatal("real App/Serve/worker join failed", err)
	}
	test.Log("MCP_CONFIGURATIONS_STOP_RECEIVED APP_CLOSE_OK SERVE_WORKERS_JOINED")
}

func TestMCPConfigurationsBrowserGuards(test *testing.T) {
	fixture := newMCPBrowserFixture(test)
	ctx := test.Context()
	assets, err := fixture.client.ListAssets(ctx, mcpBrowserRequest(&mcpv1.ListAssetsRequest{}, fixture.session))
	if err != nil || len(assets.Msg.Assets) != 2 {
		test.Fatal("owner list did not isolate assets")
	}
	var id string
	var draft *mcpv1.Draft
	for _, asset := range assets.Msg.Assets {
		if asset.SchemaVersion == 1 {
			id = asset.Id
		}
	}
	view, err := fixture.client.ReadDraft(ctx, mcpBrowserRequest(&mcpv1.ReadDraftRequest{Id: id}, fixture.session))
	if err != nil {
		test.Fatal("guard sanitized draft unavailable")
	}
	draft = view.Msg.View.Draft
	if _, err := fixture.client.ListAssets(ctx, connect.NewRequest(&mcpv1.ListAssetsRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("anonymous asset access")
	}
	checks := []func() error{
		func() error {
			_, err := fixture.client.UpdateAsset(ctx, mcpBrowserRequest(&mcpv1.UpdateAssetRequest{Id: id, ExpectedRevision: 1, Draft: draft}, fixture.foreign))
			return err
		},
		func() error {
			_, err := fixture.client.Preview(ctx, mcpBrowserRequest(&mcpv1.PreviewRequest{Id: id, ExpectedRevision: 1, Draft: draft}, fixture.foreign))
			return err
		},
		func() error {
			_, err := fixture.client.GetAsset(ctx, mcpBrowserRequest(&mcpv1.GetAssetRequest{Id: id}, fixture.foreign))
			return err
		},
		func() error {
			_, err := fixture.client.ReadDraft(ctx, mcpBrowserRequest(&mcpv1.ReadDraftRequest{Id: id}, fixture.foreign))
			return err
		},
		func() error {
			_, err := fixture.client.CopyAsset(ctx, mcpBrowserRequest(&mcpv1.CopyAssetRequest{Id: id, ExpectedSourceRevision: 1, Name: "foreign copy"}, fixture.foreign))
			return err
		},
		func() error {
			_, err := fixture.client.DeleteAsset(ctx, mcpBrowserRequest(&mcpv1.DeleteAssetRequest{Id: id, ExpectedRevision: 1}, fixture.foreign))
			return err
		},
	}
	for index, check := range checks {
		if code := connect.CodeOf(check()); code != connect.CodeNotFound {
			test.Fatal("cross-owner operation was not denied", index, code)
		}
	}
	response, err := http.Get(fixture.control.URL + "/state")
	if err != nil {
		test.Fatal("control probe unavailable")
	}
	if err := response.Body.Close(); err != nil || response.StatusCode != http.StatusForbidden {
		test.Fatal("control capability guard failed")
	}
	if err := fixture.finish(); err != nil {
		test.Fatal(fmt.Errorf("fixture guards cleanup: %w", err))
	}
}
