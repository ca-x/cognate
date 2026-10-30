package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/x509"
	"database/sql"
	"encoding/json/v2"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type orderBrowserFixture struct {
	ctx        context.Context
	cancel     context.CancelFunc
	app        *server.App
	store      *state.Store
	public     *httptest.Server
	control    *httptest.Server
	serveDone  chan error
	serving    bool
	owner      string
	session    string
	foreign    string
	capability string
	database   string
	baseline   map[string]state.ConfigurationAsset
	ciphertext map[string][]byte
	writes     atomic.Int64
	draftReads atomic.Int64
	lost       atomic.Bool
	once       sync.Once
	closeError error
}

func newOrderBrowserFixture(test *testing.T, exportDirectory string) *orderBrowserFixture {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 300*time.Second)
	fixture := &orderBrowserFixture{ctx: ctx, cancel: cancel, serveDone: make(chan error, 1), capability: rand.Text(), baseline: make(map[string]state.ConfigurationAsset), ciphertext: make(map[string][]byte)}
	test.Cleanup(func() {
		if fixture.Close() != nil {
			test.Error("order browser fixture did not drain")
		}
	})
	directory := test.TempDir()
	if os.Chmod(directory, 0o700) != nil {
		test.Fatal("private fixture directory unavailable")
	}
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := seed.TLS.Certificates[0]
	privateKey, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		test.Fatal("fixture TLS key unavailable")
	}
	certificatePath, keyPath := filepath.Join(directory, "certificate.pem"), filepath.Join(directory, "tls.key")
	if os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0o600) != nil || os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}), 0o600) != nil {
		test.Fatal("fixture TLS files unavailable")
	}
	clear(privateKey)
	transport := seed.Client().Transport.(*http.Transport).Clone()
	seed.Close()
	test.Cleanup(transport.CloseIdleConnections)
	masterKey := make([]byte, 32)
	if _, err := rand.Read(masterKey); err != nil {
		test.Fatal("fixture key unavailable")
	}
	defer clear(masterKey)
	masterPath := filepath.Join(directory, "master.key")
	if os.WriteFile(masterPath, masterKey, 0o600) != nil {
		test.Fatal("fixture key file unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal("fixture listener unavailable")
	}
	var proxy *httputil.ReverseProxy
	fixture.public = httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure {
			fixture.writes.Add(1)
		}
		if request.URL.Path == configurationv1connect.ConfigurationServiceReadDraftProcedure {
			fixture.draftReads.Add(1)
		}
		proxy.ServeHTTP(writer, request)
	}))
	origin := "https://" + fixture.public.Listener.Addr().String()
	fixture.app, err = server.Open(ctx, server.Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: masterPath, TLSCertificate: certificatePath, TLSKey: keyPath, PublicOrigin: origin, SourceRefreshInterval: new(time.Duration(0))})
	if err != nil {
		_ = listener.Close()
		test.Fatal("real App initialization failed")
	}
	fixture.database = filepath.Join(directory, "data", "server.db")
	fixture.store, err = state.Open(ctx, fixture.database, masterKey)
	if err != nil {
		_ = listener.Close()
		test.Fatal("second physical Store unavailable")
	}
	authentication, err := auth.NewService(fixture.store, masterKey)
	if err != nil {
		_ = listener.Close()
		test.Fatal("fixture authentication unavailable")
	}
	password := rand.Text()
	fixture.owner, err = authentication.Register(ctx, "order-user", []byte(password))
	if err != nil {
		_ = listener.Close()
		test.Fatal("fixture account unavailable")
	}
	session, err := authentication.Login(ctx, "order-user", []byte(password))
	if err != nil {
		_ = listener.Close()
		test.Fatal("fixture session unavailable")
	}
	fixture.session = session.Reveal()
	if _, err := authentication.Register(ctx, "order-other", []byte(password)); err != nil {
		_ = listener.Close()
		test.Fatal("foreign fixture account unavailable")
	}
	foreign, err := authentication.Login(ctx, "order-other", []byte(password))
	if err != nil {
		_ = listener.Close()
		test.Fatal("foreign fixture session unavailable")
	}
	fixture.foreign = foreign.Reveal()
	for _, name := range []string{"Alpha", "Beta"} {
		if _, err := configuration.New(fixture.store).Create(ctx, fixture.owner, configuration.Draft{Name: name, Provider: configuration.ClaudeProvider{AuthMode: configuration.NativeLogin}}, nil); err != nil {
			_ = listener.Close()
			test.Fatal("fixture asset unavailable")
		}
	}
	for _, entry := range []struct{ agent, name string }{{"claude", "Readonly"}, {"codex", "Other group"}} {
		payload := state.NewConfigurationPayload([]byte(`{"fixture":"private configuration body"}`))
		_, err := fixture.store.CreateConfigurationAsset(ctx, fixture.owner, state.ConfigurationAssetDraft{AgentID: entry.agent, Kind: state.ConfigurationKindProvider, Name: entry.name, SchemaVersion: 65535, Payload: payload})
		if err != nil {
			_ = listener.Close()
			test.Fatal("readonly fixture asset unavailable")
		}
	}
	assets, err := fixture.store.ListConfigurationAssets(ctx, fixture.owner)
	if err != nil {
		_ = listener.Close()
		test.Fatal("fixture baseline unavailable")
	}
	for _, asset := range assets {
		fixture.baseline[asset.ID] = asset
		fixture.ciphertext[asset.ID], err = fixture.readCiphertext(asset.ID)
		if err != nil {
			_ = listener.Close()
			test.Fatal("fixture ciphertext baseline unavailable")
		}
	}
	backend, _ := url.Parse("https://" + listener.Addr().String())
	proxy = httputil.NewSingleHostReverseProxy(backend)
	proxy.Transport = transport
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.Request.URL.Path == configurationv1connect.ConfigurationServiceReplaceAssetOrderProcedure && response.StatusCode == http.StatusOK && fixture.lost.CompareAndSwap(true, false) {
			response.ContentLength += 32
			response.Header.Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
		}
		return nil
	}
	fixture.serving = true
	go func() { fixture.serveDone <- fixture.app.Serve(ctx, listener) }()
	fixture.public.StartTLS()
	fixture.control = httptest.NewTLSServer(http.HandlerFunc(fixture.handleControl))
	if exportDirectory != "" {
		info, err := os.Lstat(exportDirectory)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
			test.Fatal("browser export directory must be private")
		}
		data, err := json.Marshal(map[string]string{"url": fixture.public.URL, "controlURL": fixture.control.URL, "username": "order-user", "password": password, "capability": fixture.capability})
		if err != nil || os.WriteFile(filepath.Join(exportDirectory, "ready.json"), data, 0o600) != nil {
			test.Fatal("private browser handoff unavailable")
		}
	}
	return fixture
}

func (fixture *orderBrowserFixture) readCiphertext(id string) ([]byte, error) {
	database, err := sql.Open("sqlite3", fixture.database)
	if err != nil {
		return nil, errors.New("ciphertext connection unavailable")
	}
	var ciphertext []byte
	err = database.QueryRowContext(fixture.ctx, "SELECT ciphertext FROM configuration_assets WHERE public_id = ?", id).Scan(&ciphertext)
	return ciphertext, errors.Join(err, database.Close())
}

func (fixture *orderBrowserFixture) handleControl(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Order-Fixture")), []byte(fixture.capability)) != 1 {
		writer.WriteHeader(http.StatusForbidden)
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/command" {
		if request.ContentLength < 0 || request.ContentLength > 1024 {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var input struct {
			Action string `json:"action"`
		}
		if json.UnmarshalRead(io.LimitReader(request.Body, 1025), &input, json.RejectUnknownMembers(true)) != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		switch input.Action {
		case "finish":
			writer.WriteHeader(http.StatusNoContent)
			fixture.cancel()
		case "lost":
			fixture.lost.Store(true)
			writer.WriteHeader(http.StatusNoContent)
		case "reverse":
			snapshot, err := fixture.store.ConfigurationAssetOrder(request.Context(), fixture.owner, "claude", state.ConfigurationKindProvider)
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			ids := make([]string, len(snapshot.Assets))
			for index, asset := range snapshot.Assets {
				ids[len(ids)-index-1] = asset.ID
			}
			if _, err := fixture.store.ReplaceConfigurationAssetOrder(request.Context(), fixture.owner, state.ConfigurationOrderRequest{AgentID: "claude", Kind: state.ConfigurationKindProvider, ExpectedRevision: snapshot.Revision, IDs: ids}); err != nil {
				writer.WriteHeader(http.StatusConflict)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			writer.WriteHeader(http.StatusBadRequest)
		}
		return
	}
	if request.Method != http.MethodGet || request.URL.Path != "/state" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	snapshot, err := fixture.store.ConfigurationAssetOrder(request.Context(), fixture.owner, "claude", state.ConfigurationKindProvider)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	assets, err := fixture.store.ListConfigurationAssets(request.Context(), fixture.owner)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	preserved := len(assets) == len(fixture.baseline)
	for _, asset := range assets {
		ciphertext, err := fixture.readCiphertext(asset.ID)
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		preserved = preserved && reflect.DeepEqual(asset, fixture.baseline[asset.ID]) && bytes.Equal(fixture.ciphertext[asset.ID], ciphertext)
	}
	names := make([]string, len(snapshot.Assets))
	for index, asset := range snapshot.Assets {
		names[index] = asset.Name
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.MarshalWrite(writer, map[string]any{"revision": snapshot.Revision, "names": names, "writes": fixture.writes.Load(), "draftReads": fixture.draftReads.Load(), "contentPreserved": preserved})
}

func (fixture *orderBrowserFixture) Close() error {
	fixture.once.Do(func() {
		fixture.cancel()
		if fixture.public != nil {
			fixture.public.Close()
		}
		if fixture.control != nil {
			fixture.control.Close()
		}
		if fixture.app != nil {
			if fixture.serving {
				fixture.closeError = errors.Join(fixture.closeError, <-fixture.serveDone)
			}
			fixture.closeError = errors.Join(fixture.closeError, fixture.app.Close())
		}
		if fixture.store != nil {
			fixture.closeError = errors.Join(fixture.closeError, fixture.store.Close())
		}
	})
	return fixture.closeError
}

func TestConfigurationOrderBrowserFixtureLifecycle(test *testing.T) {
	fixture := newOrderBrowserFixture(test, "")
	client := configurationv1connect.NewConfigurationServiceClient(fixture.public.Client(), fixture.public.URL)
	request := connect.NewRequest(&configurationv1.GetAssetOrderRequest{AgentId: "claude", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER})
	request.Header().Set("Authorization", "Bearer "+fixture.session)
	response, err := client.GetAssetOrder(test.Context(), request)
	if err != nil || response.Msg.Snapshot == nil || len(response.Msg.Snapshot.Assets) != 3 {
		test.Fatal("real App full group read failed")
	}
	foreign := connect.NewRequest(&configurationv1.ReplaceAssetOrderRequest{AgentId: "claude", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, ExpectedRevision: response.Msg.Snapshot.Revision, AssetIds: []string{response.Msg.Snapshot.Assets[0].Id}})
	foreign.Header().Set("Authorization", "Bearer "+fixture.foreign)
	if _, err := client.ReplaceAssetOrder(test.Context(), foreign); err == nil {
		test.Fatal("foreign owner reordered fixture assets")
	}
	if fixture.Close() != nil {
		test.Fatal("real App fixture did not close")
	}
}

func TestConfigurationOrderBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_ORDER_BROWSER_DIR")
	if directory == "" {
		test.Skip("isolated browser entrypoint; lifecycle test runs separately")
	}
	fixture := newOrderBrowserFixture(test, directory)
	<-fixture.ctx.Done()
	if errors.Is(fixture.ctx.Err(), context.DeadlineExceeded) {
		test.Error("browser fixture did not finish within its budget")
	}
	if fixture.Close() != nil {
		test.Fatal("browser fixture did not drain")
	}
}
