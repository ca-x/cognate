package desktop

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/cognate/internal/mcpclient"
	"github.com/ca-x/cognate/internal/server"
	serverstate "github.com/ca-x/cognate/internal/server/state"
)

type mcpDesktopFixture struct {
	service       *Service
	store         *serverstate.Store
	database      *sql.DB
	origin, owner string
	session       string
	mutex         sync.Mutex
	requests      map[string]int
	drop          string
	intercept     func(http.ResponseWriter, *http.Request) bool
}

func newMCPDesktopFixture(test *testing.T) *mcpDesktopFixture {
	test.Helper()
	directory := test.TempDir()
	if os.Chmod(directory, 0700) != nil {
		test.Fatal("private fixture directory")
	}
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate, pair := seed.Certificate(), seed.TLS.Certificates[0]
	privateKey, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	seed.Close()
	if err != nil {
		test.Fatal(err)
	}
	defer clear(privateKey)
	master := bytes.Repeat([]byte{74}, 32)
	defer clear(master)
	for name, body := range map[string][]byte{"master.key": bytes.Clone(master), "tls.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), "tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey})} {
		if os.WriteFile(filepath.Join(directory, name), body, 0600) != nil {
			test.Fatal("fixture write")
		}
		clear(body)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = listener.Close() })
	backend, _ := url.Parse("https://" + listener.Addr().String())
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 10 * time.Second}
	test.Cleanup(transport.CloseIdleConnections)
	fixture := &mcpDesktopFixture{requests: map[string]int{}}
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mutex.Lock()
		fixture.requests[request.URL.Path]++
		drop, intercept := fixture.drop, fixture.intercept
		fixture.mutex.Unlock()
		if intercept != nil && intercept(writer, request) {
			return
		}
		forward := request.Clone(request.Context())
		forward.URL = request.URL.Clone()
		forward.URL.Scheme, forward.URL.Host, forward.Host, forward.RequestURI = backend.Scheme, backend.Host, backend.Host, ""
		response, err := transport.RoundTrip(forward)
		if err != nil {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		defer clear(body)
		if err != nil || len(body) > 2<<20 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if drop != "" && strings.HasSuffix(request.URL.Path, "/"+drop) {
			if response.StatusCode != http.StatusOK {
				test.Error("drop must follow actual successful transaction")
			}
			connection, _, err := writer.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
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
	test.Cleanup(proxy.Close)
	fixture.origin = proxy.URL
	ctx, cancel := context.WithCancel(test.Context())
	application, err := server.Open(ctx, server.Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: filepath.Join(directory, "master.key"), TLSCertificate: filepath.Join(directory, "tls.pem"), TLSKey: filepath.Join(directory, "tls.key"), PublicOrigin: fixture.origin, SourceRefreshInterval: new(time.Duration(0))})
	if err != nil {
		cancel()
		test.Fatal(err)
	}
	finished := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() { finished <- application.Serve(ctx, listener) })
	test.Cleanup(func() {
		cancel()
		if <-finished != nil {
			test.Error("App serve join")
		}
		workers.Wait()
		if application.Close() != nil {
			test.Error("App close")
		}
	})
	fixture.service = &Service{Home: test.TempDir(), HTTP: proxy.Client()}
	fixture.store, err = serverstate.Open(test.Context(), filepath.Join(directory, "data", "server.db"), master)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if fixture.store.Close() != nil {
			test.Error("fixture Store close")
		}
	})
	fixture.database, err = sql.Open("sqlite3", filepath.Join(directory, "data", "server.db"))
	if err != nil {
		test.Fatal(err)
	}
	fixture.database.SetMaxOpenConns(1)
	test.Cleanup(func() { _ = fixture.database.Close() })
	for _, statement := range []string{
		`CREATE TABLE mcp_desktop_audit(operation TEXT NOT NULL, revision INTEGER NOT NULL)`,
		`CREATE TRIGGER mcp_desktop_insert AFTER INSERT ON configuration_assets WHEN NEW.kind = 'mcp' BEGIN INSERT INTO mcp_desktop_audit VALUES ('create',NEW.revision); END`,
		`CREATE TRIGGER mcp_desktop_update AFTER UPDATE ON configuration_assets WHEN NEW.kind = 'mcp' AND NEW.revision > OLD.revision BEGIN INSERT INTO mcp_desktop_audit VALUES ('update',NEW.revision); END`,
		`CREATE TRIGGER mcp_desktop_delete AFTER DELETE ON configuration_assets WHEN OLD.kind = 'mcp' BEGIN INSERT INTO mcp_desktop_audit VALUES ('delete',OLD.revision); END`,
	} {
		if _, err := fixture.database.ExecContext(test.Context(), statement); err != nil {
			test.Fatal("physical commit audit setup", err)
		}
	}
	authentication := authv1connect.NewAuthServiceClient(proxy.Client(), fixture.origin)
	registered, err := authentication.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "synthetic-mcp-desktop", Password: "synthetic-private-password"}))
	if err != nil {
		test.Fatal("actual TLS registration", err)
	}
	login, err := authentication.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "synthetic-mcp-desktop", Password: "synthetic-private-password"}))
	if err != nil {
		test.Fatal("actual TLS login", err)
	}
	fixture.owner, fixture.session = registered.Msg.UserId, login.Msg.SessionToken
	test.Cleanup(func() { fixture.session = "" })
	return fixture
}

func (fixture *mcpDesktopFixture) count(method string) int {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	var count int
	for name, value := range fixture.requests {
		if strings.HasSuffix(name, "/"+method) {
			count += value
		}
	}
	return count
}

func (fixture *mcpDesktopFixture) commits(test *testing.T, operation string) int {
	test.Helper()
	var count int
	if err := fixture.database.QueryRowContext(test.Context(), `SELECT count(*) FROM mcp_desktop_audit WHERE operation=?`, operation).Scan(&count); err != nil {
		test.Fatal("physical commit count", err)
	}
	return count
}

func mcpDesktopModel(test *testing.T, fixture *mcpDesktopFixture) *mcpAssetsModel {
	test.Helper()
	identity, err := fixture.service.ConfigurationIdentity(test.Context(), fixture.origin, fixture.session)
	if err != nil {
		test.Fatal(err)
	}
	defer identity.clear()
	model, err := fixture.service.openMCPAssets(test.Context(), identity)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(model.Close)
	return model
}

func mcpDesktopDraft() mcpclient.Draft {
	return mcpclient.Draft{Name: "Synthetic native MCP", AgentID: "claude", Servers: []mcpclient.Server{{ID: "tools", Name: "tools", Stdio: &mcpclient.Stdio{Command: "never-executed", Args: &mcpclient.Arguments{Values: []mcpclient.Argument{{ID: "first", SlotRef: "arg/tools/first"}}}, Env: &mcpclient.NamedSlots{Values: []mcpclient.NamedSlot{}}}}, {ID: "remote", Name: "remote", Enabled: true, HTTP: &mcpclient.Remote{URL: "https://synthetic.invalid/mcp", Headers: &mcpclient.NamedSlots{Values: []mcpclient.NamedSlot{{Name: "Authorization", SlotRef: "header/remote/Authorization"}}}}}, {ID: "events", Name: "events", SSE: &mcpclient.Remote{URL: "https://synthetic.invalid/events"}}}}
}

func mcpDesktopCreate(test *testing.T, model *mcpAssetsModel) mcpclient.Metadata {
	test.Helper()
	input, err := mcpclient.NewInput(test.Context(), mcpDesktopDraft(), []mcpclient.SecretEdit{{SlotID: "arg/tools/first", Action: mcpclient.Replace, Replacement: []byte("synthetic-mcp-argument")}, {SlotID: "header/remote/Authorization", Action: mcpclient.Replace, Replacement: []byte("synthetic-mcp-header")}})
	if err != nil {
		test.Fatal(err)
	}
	defer input.Close()
	review, err := model.Prepare(test.Context(), mcpclient.Change{Action: "create"}, input)
	if err != nil || len(review.Slots) != 2 || strings.Contains(review.Summary, "synthetic-mcp-header") || !strings.Contains(review.Summary, "REPLACE") {
		test.Fatal("complete redacted review", err)
	}
	created, err := model.Submit(test.Context(), review.Confirmation)
	if err != nil {
		test.Fatal(err)
	}
	return created
}

func TestMCPDesktopActualReadOnlyAndOwnerIsolation(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	model := mcpDesktopModel(test, fixture)
	for _, schema := range []int{1, 2} {
		created, err := fixture.store.CreateConfigurationAsset(test.Context(), fixture.owner, serverstate.ConfigurationAssetDraft{AgentID: "claude", Kind: serverstate.ConfigurationKindMCP, Name: "future", SchemaVersion: schema, Payload: serverstate.NewConfigurationPayload([]byte(`{"version":1,"document":{"servers":[]},"slots":{},"future":false}`))})
		if err != nil {
			test.Fatal(err)
		}
		var before, after []byte
		if err := fixture.database.QueryRowContext(test.Context(), `SELECT ciphertext FROM configuration_assets WHERE public_id=?`, created.ID).Scan(&before); err != nil {
			test.Fatal(err)
		}
		view, err := model.Read(test.Context(), created.ID)
		if err != nil || !view.ReadOnly || view.Metadata.ID != created.ID || len(view.Draft.Servers) != 0 {
			test.Fatal("unknown fields/schema not read-only metadata", err)
		}
		for _, action := range []string{"copy", "delete", "update"} {
			if _, err := model.Prepare(test.Context(), mcpclient.Change{Action: action, ID: created.ID, Revision: 1, Name: "must-not-copy"}, nil); err == nil {
				test.Fatal("unverified declaration mutation prepared")
			}
		}
		if err := fixture.database.QueryRowContext(test.Context(), `SELECT ciphertext FROM configuration_assets WHERE public_id=?`, created.ID).Scan(&after); err != nil || !bytes.Equal(before, after) {
			test.Fatal("read-only ciphertext changed")
		}
	}
	authentication := authv1connect.NewAuthServiceClient(fixture.service.HTTP, fixture.origin)
	if _, err := authentication.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "synthetic-other-owner", Password: "synthetic-other-password"})); err != nil {
		test.Fatal(err)
	}
	login, err := authentication.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "synthetic-other-owner", Password: "synthetic-other-password"}))
	if err != nil {
		test.Fatal(err)
	}
	identity, err := fixture.service.ConfigurationIdentity(test.Context(), fixture.origin, login.Msg.SessionToken)
	if err != nil {
		test.Fatal(err)
	}
	defer identity.clear()
	foreign, err := fixture.service.openMCPAssets(test.Context(), identity)
	if err != nil {
		test.Fatal(err)
	}
	defer foreign.Close()
	list, err := foreign.List(test.Context())
	if err != nil || len(list) != 0 {
		test.Fatal("foreign owner listed private assets", err)
	}
	owned, err := model.List(test.Context())
	if err != nil || len(owned) != 2 {
		test.Fatal(err)
	}
	if _, err := foreign.Read(test.Context(), owned[0].ID); err == nil {
		test.Fatal("foreign read admitted")
	}
	if fixture.commits(test, "create") != 2 || fixture.commits(test, "update") != 0 || fixture.commits(test, "delete") != 0 {
		test.Fatal("read-only or foreign operations wrote")
	}
}

func TestMCPDesktopActualTLSCRUDAndCAS(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	model := mcpDesktopModel(test, fixture)
	created := mcpDesktopCreate(test, model)
	view, err := model.Read(test.Context(), created.ID)
	if err != nil || view.ReadOnly || view.NeedsCredential || len(view.Draft.Servers) != 3 || view.Draft.Servers[0].Enabled || view.Draft.Servers[0].Stdio.Env == nil || view.Draft.Servers[2].SSE.Headers != nil {
		test.Fatal("complete three-transport/presence roundtrip", err)
	}
	view.Draft.Name = "Edited"
	input, err := mcpclient.NewInput(test.Context(), view.Draft, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer input.Close()
	review, err := model.Prepare(test.Context(), mcpclient.Change{Action: "update", ID: created.ID, Revision: 1}, input)
	if err != nil || fixture.count("UpdateAsset") != 0 {
		test.Fatal("prepare wrote", err)
	}
	if _, err := model.Submit(test.Context(), "wrong"); !errors.Is(err, mcpclient.ErrInvalid) {
		test.Fatal("wrong confirmation admitted")
	}
	updated, err := model.Submit(test.Context(), review.Confirmation)
	if err != nil || updated.Revision != 2 {
		test.Fatal(err)
	}
	if _, err := model.Prepare(test.Context(), mcpclient.Change{Action: "update", ID: created.ID, Revision: 1}, input); !errors.Is(err, mcpclient.ErrConflict) {
		test.Fatal("stale CAS accepted")
	}
	review, err = model.Prepare(test.Context(), mcpclient.Change{Action: "copy", ID: created.ID, Revision: 2, Name: "No credentials"}, nil)
	if err != nil {
		test.Fatal(err)
	}
	copied, err := model.Submit(test.Context(), review.Confirmation)
	if err != nil {
		test.Fatal(err)
	}
	view, err = model.Read(test.Context(), copied.ID)
	if err != nil || !view.NeedsCredential {
		test.Fatal("copy credential state", err)
	}
	for _, slot := range view.Secrets {
		if slot.Present {
			test.Fatal("copy included a secret")
		}
	}
	review, err = model.Prepare(test.Context(), mcpclient.Change{Action: "delete", ID: copied.ID, Revision: 1}, nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := model.Submit(test.Context(), review.Confirmation); err != nil {
		test.Fatal(err)
	}
	if _, err := model.Submit(test.Context(), review.Confirmation); !errors.Is(err, mcpclient.ErrClosed) || fixture.count("DeleteAsset") != 1 {
		test.Fatal("review replayed")
	}
	assets, err := model.List(test.Context())
	if err != nil || len(assets) != 1 || assets[0].Revision != 2 || fixture.count("BeginNativeConsumer") != 0 {
		test.Fatal("unexpected final assets/native operation", err)
	}
}

func TestMCPDesktopActualUnknownNeverReplays(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	model := mcpDesktopModel(test, fixture)
	created := mcpDesktopCreate(test, model)
	review, err := model.Prepare(test.Context(), mcpclient.Change{Action: "copy", ID: created.ID, Revision: 1, Name: "Lost receipt"}, nil)
	if err != nil {
		test.Fatal(err)
	}
	fixture.mutex.Lock()
	fixture.drop = "CopyAsset"
	fixture.mutex.Unlock()
	if _, err := model.Submit(test.Context(), review.Confirmation); !errors.Is(err, mcpclient.ErrUnknown) {
		test.Fatal("committed lost response not Unknown", err)
	}
	if _, err := model.Prepare(test.Context(), mcpclient.Change{Action: "delete", ID: created.ID, Revision: 1}, nil); !errors.Is(err, mcpclient.ErrUnknown) {
		test.Fatal("Unknown reopened mutation gate", err)
	}
	assets, err := model.List(test.Context())
	if err != nil || len(assets) != 2 || fixture.count("CopyAsset") != 1 {
		test.Fatal("actual commit or no-replay proof", err)
	}
	for _, text := range []string{fmt.Sprintf("%+v", model), fmt.Sprintf("%#v", model)} {
		if strings.Contains(text, fixture.session) || strings.Contains(text, "synthetic-mcp-header") {
			test.Fatal("model formatting exposed private data")
		}
	}
}

func TestMCPDesktopActualLateCASConflict(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	first, second := mcpDesktopModel(test, fixture), mcpDesktopModel(test, fixture)
	created := mcpDesktopCreate(test, first)
	view, err := first.Read(test.Context(), created.ID)
	if err != nil {
		test.Fatal(err)
	}
	view.Draft.Name = "Reviewed at revision one"
	input, err := mcpclient.NewInput(test.Context(), view.Draft, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer input.Close()
	change := mcpclient.Change{Action: "update", ID: created.ID, Revision: 1}
	old, err := first.Prepare(test.Context(), change, input)
	if err != nil {
		test.Fatal(err)
	}
	newer, err := second.Prepare(test.Context(), change, input)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := second.Submit(test.Context(), newer.Confirmation); err != nil {
		test.Fatal(err)
	}
	if _, err := first.Submit(test.Context(), old.Confirmation); !errors.Is(err, mcpclient.ErrConflict) {
		test.Fatal("late CAS overwrote newer revision", err)
	}
	if _, err := first.Submit(test.Context(), old.Confirmation); !errors.Is(err, mcpclient.ErrClosed) || fixture.count("UpdateAsset") != 2 || fixture.commits(test, "update") != 1 {
		test.Fatal("late conflict was replayed or changed physical CAS count", err)
	}
}
