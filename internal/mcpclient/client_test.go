package mcpclient_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"connectrpc.com/connect"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1/mcpv1connect"
	"github.com/ca-x/cognate/internal/mcpclient"
	"github.com/ca-x/cognate/internal/server"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/encoding/protojson"
)

const actualMCPDraft = `{"name":"tools","agentId":"claude","document":{"servers":[{"id":"stdio","name":"stdio","enabled":true,"stdio":{"command":"never-executed-synthetic-command","args":{"values":[{"id":"a","slotRef":"arg/stdio/a"}]},"env":{"values":[{"name":"TOKEN","slotRef":"env/stdio/TOKEN"}]}}},{"id":"http","name":"http","enabled":true,"http":{"url":"https://example.test/mcp","headers":{"values":[{"name":"Authorization","slotRef":"header/http/Authorization"}]}}},{"id":"sse","name":"sse","enabled":false,"sse":{"url":"https://example.test/events","headers":{}}}]}}`

func actualMCPInput(test *testing.T, declaration, mutations string) *mcpclient.Input {
	test.Helper()
	actions := ""
	if mutations != "" {
		actions = inputFile(test, "private-actions.json", mutations)
	}
	input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "draft.json", declaration), actions)
	if err != nil {
		test.Fatal("actual input refused")
	}
	test.Cleanup(input.Close)
	return input
}

func actualMCPAudit(test *testing.T, fixture *actualMCPAppFixture) *sql.DB {
	test.Helper()
	database, err := sql.Open("sqlite3", filepath.Join(fixture.directory, "data", "server.db"))
	if err != nil {
		test.Fatal("physical SQLite audit unavailable")
	}
	database.SetMaxOpenConns(1)
	test.Cleanup(func() {
		if database.Close() != nil {
			test.Error("SQLite audit close failed")
		}
	})
	for _, statement := range []string{
		`CREATE TABLE mcp_cli_test_audit(operation TEXT NOT NULL, revision INTEGER NOT NULL)`,
		`CREATE TRIGGER mcp_cli_test_insert AFTER INSERT ON configuration_assets WHEN NEW.kind = 'mcp' BEGIN INSERT INTO mcp_cli_test_audit VALUES ('create',NEW.revision); END`,
		`CREATE TRIGGER mcp_cli_test_update AFTER UPDATE ON configuration_assets WHEN NEW.kind = 'mcp' AND NEW.revision > OLD.revision BEGIN INSERT INTO mcp_cli_test_audit VALUES ('update',NEW.revision); END`,
		`CREATE TRIGGER mcp_cli_test_delete AFTER DELETE ON configuration_assets WHEN OLD.kind = 'mcp' BEGIN INSERT INTO mcp_cli_test_audit VALUES ('delete',OLD.revision); END`,
	} {
		if _, err := database.ExecContext(test.Context(), statement); err != nil {
			test.Fatal("SQLite commit audit setup failed")
		}
	}
	return database
}

func actualMCPCount(test *testing.T, database *sql.DB, operation string) int {
	test.Helper()
	var count int
	if database.QueryRowContext(test.Context(), `SELECT count(*) FROM mcp_cli_test_audit WHERE operation=?`, operation).Scan(&count) != nil {
		test.Fatal("SQLite audit read failed")
	}
	return count
}

func TestMCPClientActualTLSOwnerAndReadonly(test *testing.T) {
	fixture := newActualMCPAppFixture(test)
	database := actualMCPAudit(test, fixture)
	client, err := mcpclient.New(fixture.origin, fixture.session, fixture.client)
	if err != nil {
		test.Fatal("actual owner client unavailable")
	}
	defer client.Close()
	input := actualMCPInput(test, `{"name":"tools","agentId":"claude","document":{}}`, "")
	operation, err := client.Prepare(test.Context(), mcpclient.Change{Action: "create"}, input)
	if err != nil {
		test.Fatal("actual owner prepare failed")
	}
	defer operation.Close()
	created, err := operation.Submit(test.Context())
	if err != nil {
		test.Fatal("actual owner create failed")
	}
	store, err := state.Open(test.Context(), filepath.Join(fixture.directory, "data", "server.db"), fixture.master)
	if err != nil {
		test.Fatal("second actual Store unavailable")
	}
	authentication, err := auth.NewService(store, fixture.master)
	if err != nil {
		_ = store.Close()
		test.Fatal("second owner issuer unavailable")
	}
	token, _, err := authentication.LoginOIDC(test.Context(), "https://synthetic.identity", "mcp-foreign-owner", "", "")
	if store.Close() != nil || err != nil {
		test.Fatal("second genuine owner session unavailable")
	}
	foreign, err := mcpclient.New(fixture.origin, token.Reveal(), fixture.client)
	if err != nil {
		test.Fatal("foreign session client unavailable")
	}
	defer foreign.Close()
	items, err := foreign.List(test.Context())
	if err != nil || len(items) != 0 {
		test.Fatal("foreign metadata disclosed")
	}
	if _, err := foreign.Get(test.Context(), created.ID); err == nil {
		test.Fatal("foreign Get admitted")
	}
	if _, err := foreign.Read(test.Context(), created.ID); err == nil {
		test.Fatal("foreign Read admitted")
	}
	for _, action := range []string{"copy", "delete"} {
		change := mcpclient.Change{Action: action, ID: created.ID, Revision: 1}
		if action == "copy" {
			change.Name = "foreign-copy"
		}
		if _, err := foreign.Prepare(test.Context(), change, nil); err == nil {
			test.Fatal("foreign mutation preparation admitted")
		}
	}
	if _, err := database.ExecContext(test.Context(), `UPDATE configuration_assets SET schema_version=2 WHERE public_id=?`, created.ID); err != nil {
		test.Fatal("synthetic future-schema fixture unavailable")
	}
	metadata, err := client.Get(test.Context(), created.ID)
	if err != nil || metadata.Editable() || metadata.Schema != 2 {
		test.Fatal("future-schema metadata hidden or editable")
	}
	view, err := client.Read(test.Context(), created.ID)
	var output bytes.Buffer
	if err != nil || view.WriteSummary(test.Context(), &output) != nil || !strings.Contains(output.String(), "readonly=true") {
		test.Fatal("future-schema read not metadata-only")
	}
	if _, err := client.Prepare(test.Context(), mcpclient.Change{Action: "copy", ID: created.ID, Revision: 1, Name: "copy"}, nil); !errors.Is(err, mcpclient.ErrInvalid) {
		test.Fatal("future-schema copy admitted")
	}
	if _, err := client.Prepare(test.Context(), mcpclient.Change{Action: "update", ID: created.ID, Revision: 1}, input); !errors.Is(err, mcpclient.ErrInvalid) {
		test.Fatal("future-schema update admitted")
	}
	if actualMCPCount(test, database, "create") != 1 || actualMCPCount(test, database, "update") != 0 || actualMCPCount(test, database, "delete") != 0 {
		test.Fatal("owner/readonly controls mutated durable assets")
	}
}

func TestMCPClientActualTLSUnknownAndDrain(test *testing.T) {
	for _, mode := range []string{"drop", "cancel", "close"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newActualMCPAppFixture(test)
			database := actualMCPAudit(test, fixture)
			var calls atomic.Int32
			entered := make(chan struct{})
			transport := controlledTransport(func(request *http.Request) (*http.Response, error) {
				mutation := strings.HasSuffix(request.URL.Path, "/CreateAsset")
				if mutation {
					calls.Add(1)
				}
				response, err := fixture.client.Transport.RoundTrip(request)
				if err == nil && mutation && response.StatusCode == 200 {
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
					if mode != "drop" {
						close(entered)
						<-request.Context().Done()
					}
					return nil, errors.New("synthetic private committed response loss")
				}
				return response, err
			})
			client, err := mcpclient.New(fixture.origin, fixture.session, &http.Client{Transport: transport})
			if err != nil {
				test.Fatal("actual fault client unavailable")
			}
			defer client.Close()
			input := actualMCPInput(test, `{"name":"tools","agentId":"claude","document":{}}`, "")
			operation, err := client.Prepare(test.Context(), mcpclient.Change{Action: "create"}, input)
			if err != nil {
				test.Fatal("actual fault prepare failed")
			}
			defer operation.Close()
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			result := make(chan error, 1)
			var worker sync.WaitGroup
			worker.Go(func() { _, err := operation.Submit(ctx); result <- err })
			if mode != "drop" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					cancel()
					worker.Wait()
					test.Fatal("actual commit was not reached")
				}
				if mode == "cancel" {
					cancel()
				} else {
					client.Close()
				}
			}
			err = <-result
			worker.Wait()
			if !errors.Is(err, mcpclient.ErrUnknown) || calls.Load() != 1 || actualMCPCount(test, database, "create") != 1 {
				test.Fatal("actual committed loss/drain not Unknown single commit")
			}
			if mode != "close" {
				if _, err := operation.Submit(test.Context()); !errors.Is(err, mcpclient.ErrUnknown) {
					test.Fatal("actual lost operation replay admitted")
				}
				if _, err := client.Prepare(test.Context(), mcpclient.Change{Action: "create"}, input); !errors.Is(err, mcpclient.ErrUnknown) {
					test.Fatal("actual unknown client mutation admitted")
				}
				items, err := client.List(test.Context())
				if err != nil || len(items) != 1 {
					test.Fatal("actual reconciliation read unavailable")
				}
			}
			if calls.Load() != 1 || actualMCPCount(test, database, "create") != 1 {
				test.Fatal("actual business mutation replay occurred")
			}
			test.Log("genuine committed response loss: generated transport=1 successful SQLite CAS=1; worker/callbacks joined; no replay")
		})
	}
}

func TestMCPClientActualTLSLifecycle(test *testing.T) {
	fixture := newActualMCPAppFixture(test)
	database := actualMCPAudit(test, fixture)
	client, err := mcpclient.New(fixture.origin, fixture.session, fixture.client)
	if err != nil {
		test.Fatal("real client unavailable")
	}
	defer client.Close()
	input := actualMCPInput(test, actualMCPDraft, `[{"slotId":"arg/stdio/a","replace":"c3ludGhldGljLW1jcC1hcmctc2VjcmV0"},{"slotId":"env/stdio/TOKEN","replace":""},{"slotId":"header/http/Authorization","replace":"c3ludGhldGljLW1jcC1oZWFkZXItc2VjcmV0"}]`)
	preview, err := client.Preview(test.Context(), "", 0, input)
	if err != nil {
		test.Fatal("real preview failed")
	}
	var logical, review, safe bytes.Buffer
	if preview.WriteSummary(test.Context(), &logical) != nil || !strings.Contains(logical.String(), "can_apply=false") || actualMCPCount(test, database, "create") != 0 {
		test.Fatal("preview wrote or misrepresented native application")
	}
	prepared, err := client.Prepare(test.Context(), mcpclient.Change{Action: "create"}, input)
	if err != nil {
		test.Fatal("real create prepare failed")
	}
	defer prepared.Close()
	if prepared.WriteSummary(test.Context(), &review) != nil {
		test.Fatal("real complete review failed")
	}
	created, err := prepared.Submit(test.Context())
	if err != nil || created.Revision != 1 || actualMCPCount(test, database, "create") != 1 {
		test.Fatal("real create acknowledgement or SQLite count failed")
	}
	if _, err := prepared.Submit(test.Context()); !errors.Is(err, mcpclient.ErrUnknown) || actualMCPCount(test, database, "create") != 1 {
		test.Fatal("create replayed")
	}
	items, err := client.List(test.Context())
	if err != nil || len(items) != 1 || items[0] != created {
		test.Fatal("real list mismatch")
	}
	metadata, err := client.Get(test.Context(), created.ID)
	if err != nil || metadata != created {
		test.Fatal("real get mismatch")
	}
	view, err := client.Read(test.Context(), created.ID)
	if err != nil || view.WriteSummary(test.Context(), &safe) != nil {
		test.Fatal("real read failed")
	}
	raw := mcpv1connect.NewMCPConfigurationServiceClient(fixture.client, fixture.origin)
	readRequest := connect.NewRequest(&mcpv1.ReadDraftRequest{Id: created.ID})
	readRequest.Header().Set("Authorization", "Bearer "+fixture.session)
	read, err := raw.ReadDraft(test.Context(), readRequest)
	expected := &mcpv1.Draft{}
	if protojson.Unmarshal([]byte(actualMCPDraft), expected) != nil || err != nil || !proto.Equal(read.Msg.View.Draft, expected) || len(read.Msg.View.Secrets) != 3 {
		test.Fatal("transport/order/false/wrapper presence lost")
	}
	for _, slot := range read.Msg.View.Secrets {
		if slot.Present == nil || !*slot.Present {
			test.Fatal("present-empty replacement lost")
		}
	}
	changed := strings.Replace(actualMCPDraft, `"enabled":true`, `"enabled":false`, 1)
	updateInput := actualMCPInput(test, changed, `[{"slotId":"arg/stdio/a","keep":{}},{"slotId":"env/stdio/TOKEN","replace":""},{"slotId":"header/http/Authorization","clear":{"confirmed":true}}]`)
	first, err := client.Prepare(test.Context(), mcpclient.Change{Action: "update", ID: created.ID, Revision: 1}, updateInput)
	if err != nil {
		test.Fatal("real update prepare failed")
	}
	defer first.Close()
	stale, err := client.Prepare(test.Context(), mcpclient.Change{Action: "update", ID: created.ID, Revision: 1}, updateInput)
	if err != nil {
		test.Fatal("concurrent real CAS prepare failed")
	}
	defer stale.Close()
	updated, err := first.Submit(test.Context())
	if err != nil || updated.Revision != 2 {
		test.Fatal("real update failed")
	}
	if _, err := stale.Submit(test.Context()); !errors.Is(err, mcpclient.ErrConflict) || actualMCPCount(test, database, "update") != 1 {
		test.Fatal("stale CAS mutated or misclassified")
	}
	copyOperation, err := client.Prepare(test.Context(), mcpclient.Change{Action: "copy", ID: created.ID, Revision: 2, Name: "copied"}, nil)
	if err != nil {
		test.Fatal("real copy prepare failed")
	}
	defer copyOperation.Close()
	copied, err := copyOperation.Submit(test.Context())
	if err != nil || copied.ID == created.ID || copied.Revision != 1 {
		test.Fatal("real copy failed")
	}
	readRequest.Msg.Id = copied.ID
	copyView, err := raw.ReadDraft(test.Context(), readRequest)
	if err != nil {
		test.Fatal("real copied draft unavailable")
	}
	for _, slot := range copyView.Msg.View.Secrets {
		if slot.Present == nil || *slot.Present {
			test.Fatal("copy carried credential")
		}
	}
	empty := actualMCPInput(test, `{"name":"tools","agentId":"claude","document":{}}`, "")
	if _, err := client.Prepare(test.Context(), mcpclient.Change{Action: "update", ID: created.ID, Revision: 2}, empty); !errors.Is(err, mcpclient.ErrInvalid) || actualMCPCount(test, database, "update") != 1 {
		test.Fatal("old-only omission changed DB")
	}
	cleared := actualMCPInput(test, `{"name":"tools","agentId":"claude","document":{}}`, `[{"slotId":"arg/stdio/a","clear":{"confirmed":true}},{"slotId":"env/stdio/TOKEN","clear":{"confirmed":true}},{"slotId":"header/http/Authorization","clear":{"confirmed":true}}]`)
	clearOperation, err := client.Prepare(test.Context(), mcpclient.Change{Action: "update", ID: created.ID, Revision: 2}, cleared)
	if err != nil {
		test.Fatal("real union CLEAR prepare failed")
	}
	defer clearOperation.Close()
	clearedMetadata, err := clearOperation.Submit(test.Context())
	if err != nil || clearedMetadata.Revision != 3 || actualMCPCount(test, database, "update") != 2 {
		test.Fatal("real union CLEAR failed")
	}
	for _, item := range []mcpclient.Metadata{copied, clearedMetadata} {
		operation, err := client.Prepare(test.Context(), mcpclient.Change{Action: "delete", ID: item.ID, Revision: item.Revision}, nil)
		if err != nil {
			test.Fatal("real delete prepare failed")
		}
		_, err = operation.Submit(test.Context())
		operation.Close()
		if err != nil {
			test.Fatal("real delete failed")
		}
	}
	if actualMCPCount(test, database, "create") != 2 || actualMCPCount(test, database, "delete") != 2 {
		test.Fatal("physical commit counts mismatch")
	}
	for _, output := range []string{review.String(), safe.String(), logical.String()} {
		for _, canary := range []string{"synthetic-mcp-arg-secret", "synthetic-mcp-header-secret", "c3ludGhldGljLW1jcC1hcmctc2VjcmV0", fixture.session} {
			if strings.Contains(output, canary) {
				test.Fatal("secret leaked to public review/logical output")
			}
		}
	}
	client.Close()
	fixture.stop()
	reopened, err := state.Open(test.Context(), filepath.Join(fixture.directory, "data", "server.db"), fixture.master)
	if err != nil {
		test.Fatal("lifecycle reopen failed")
	}
	defer func() {
		if reopened.Close() != nil {
			test.Error("reopened Store close failed")
		}
	}()
	persisted, err := reopened.ListConfigurationAssets(test.Context(), fixture.owner)
	if err != nil || len(persisted) != 0 {
		test.Fatal("real lifecycle deletes did not persist")
	}
	test.Log("real App/TLS/auth/SQLite/moejs eight RPCs; committed create=2 update=2 delete=2; Preview writes=0")
}

type actualMCPAppFixture struct {
	client    *http.Client
	origin    string
	directory string
	master    []byte
	owner     string
	session   string
	stop      func()
}

func newActualMCPAppFixture(test *testing.T) *actualMCPAppFixture {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 30*time.Second)
	test.Cleanup(cancel)
	directory := test.TempDir()
	if os.Chmod(directory, 0700) != nil {
		test.Fatal("private App fixture directory unavailable")
	}
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := seed.Certificate()
	privateKey, err := x509.MarshalPKCS8PrivateKey(seed.TLS.Certificates[0].PrivateKey)
	seed.Close()
	if err != nil {
		test.Fatal("synthetic TLS fixture key unavailable")
	}
	defer clear(privateKey)
	master := bytes.Repeat([]byte{83}, 32)
	fixture := &actualMCPAppFixture{directory: directory, master: master}
	test.Cleanup(func() { clear(master); fixture.session = ""; fixture.owner = "" })
	for name, contents := range map[string][]byte{
		"master.key": master,
		"tls.pem":    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}),
		"tls.key":    pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}),
	} {
		if os.WriteFile(filepath.Join(directory, name), contents, 0600) != nil {
			test.Fatal("synthetic private fixture write failed")
		}
		if name != "master.key" {
			clear(contents)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal("private loopback listener unavailable")
	}
	test.Cleanup(func() { _ = listener.Close() })
	fixture.origin = "https://" + listener.Addr().String()
	application, err := server.Open(ctx, server.Config{
		DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: filepath.Join(directory, "master.key"),
		TLSCertificate: filepath.Join(directory, "tls.pem"), TLSKey: filepath.Join(directory, "tls.key"),
		PublicOrigin: fixture.origin, SourceRefreshInterval: new(time.Duration(0)),
	})
	if err != nil {
		test.Fatal("real App fixture open failed")
	}
	finished := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() { finished <- application.Serve(ctx, listener) })
	var stopOnce sync.Once
	fixture.stop = func() {
		stopOnce.Do(func() {
			cancel()
			if <-finished != nil {
				test.Error("real App serve did not stop cleanly")
			}
			workers.Wait()
			if application.Close() != nil {
				test.Error("real App close failed")
			}
		})
	}
	test.Cleanup(fixture.stop)
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, Proxy: nil, DisableCompression: true, MaxConnsPerHost: 1, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 5 * time.Second}
	fixture.client = &http.Client{Transport: transport, Timeout: 5 * time.Second}
	test.Cleanup(transport.CloseIdleConnections)
	authentication := authv1connect.NewAuthServiceClient(fixture.client, fixture.origin)
	registered, err := authentication.Register(ctx, connect.NewRequest(&authv1.RegisterRequest{Username: "mcp-cli-fixture", Password: "synthetic-mcp-cli-fixture-password"}))
	if err != nil || registered == nil || registered.Msg == nil {
		test.Fatal("real TLS App registration failed")
	}
	login, err := authentication.Login(ctx, connect.NewRequest(&authv1.LoginRequest{Username: "mcp-cli-fixture", Password: "synthetic-mcp-cli-fixture-password"}))
	if err != nil || login == nil || login.Msg == nil || login.Msg.SessionToken == "" {
		test.Fatal("real TLS App session issue failed")
	}
	fixture.owner, fixture.session = registered.Msg.UserId, login.Msg.SessionToken
	return fixture
}

func TestMCPActualTLSAppFixtureAuthenticationOnly(test *testing.T) {
	fixture := newActualMCPAppFixture(test)
	client, err := mcpclient.New(fixture.origin, fixture.session, fixture.client)
	if err != nil {
		test.Fatal("actual TLS client setup failed")
	}
	defer client.Close()
	if client.Authenticate(test.Context()) != nil {
		test.Fatal("actual TLS App authentication failed")
	}
	client.Close()
	fixture.stop()
	store, err := state.Open(test.Context(), filepath.Join(fixture.directory, "data", "server.db"), fixture.master)
	if err != nil {
		test.Fatal("physical SQLite reopen failed")
	}
	defer func() {
		if store.Close() != nil {
			test.Error("reopened SQLite close failed")
		}
	}()
	authentication, err := auth.NewService(store, fixture.master)
	if err != nil {
		test.Fatal("reopened authentication unavailable")
	}
	owner, err := authentication.Authenticate(test.Context(), fixture.session)
	if err != nil || owner != fixture.owner {
		test.Fatal("actual session did not persist in physical SQLite")
	}
	assets, err := store.ListConfigurationAssets(test.Context(), owner)
	if err != nil || len(assets) != 0 {
		test.Fatal("authentication-only fixture unexpectedly wrote configuration assets")
	}
}

func TestMCPActualTLSAppFixtureRevocationOnly(test *testing.T) {
	fixture := newActualMCPAppFixture(test)
	client, err := mcpclient.New(fixture.origin, fixture.session, fixture.client)
	if err != nil {
		test.Fatal("actual TLS client setup failed")
	}
	defer client.Close()
	if client.Authenticate(test.Context()) != nil {
		test.Fatal("actual TLS App authentication failed")
	}
	authentication := authv1connect.NewAuthServiceClient(fixture.client, fixture.origin)
	request := connect.NewRequest(&authv1.LogoutRequest{})
	request.Header().Set("Authorization", "Bearer "+fixture.session)
	if _, err := authentication.Logout(test.Context(), request); err != nil {
		test.Fatal("actual TLS App logout failed")
	}
	if !errors.Is(client.Authenticate(test.Context()), mcpclient.ErrDenied) {
		test.Fatal("revoked real session remained authorized")
	}
	if _, err := client.Prepare(test.Context(), mcpclient.Change{Action: "delete", ID: strings.Repeat("A", 26), Revision: 1}, nil); !errors.Is(err, mcpclient.ErrDenied) {
		test.Fatal("revoked real session passed preparation authentication")
	}
	client.Close()
	fixture.stop()
	store, err := state.Open(test.Context(), filepath.Join(fixture.directory, "data", "server.db"), fixture.master)
	if err != nil {
		test.Fatal("physical SQLite reopen failed")
	}
	defer func() {
		if store.Close() != nil {
			test.Error("reopened SQLite close failed")
		}
	}()
	persisted, err := auth.NewService(store, fixture.master)
	if err != nil {
		test.Fatal("reopened authentication unavailable")
	}
	if _, err := persisted.Authenticate(test.Context(), fixture.session); !errors.Is(err, auth.ErrInvalidSession) {
		test.Fatal("session revocation did not persist in physical SQLite")
	}
	assets, err := store.ListConfigurationAssets(test.Context(), fixture.owner)
	if err != nil || len(assets) != 0 {
		test.Fatal("revocation-only fixture unexpectedly wrote configuration assets")
	}
}

func inputFile(test *testing.T, name, content string) string {
	test.Helper()
	path := filepath.Join(test.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		test.Fatal("fixture write failed")
	}
	return path
}

func TestMCPGeneratedUnknownBlocksReplayButAllowsReads(test *testing.T) {
	var mutations atomic.Int32
	id := strings.Repeat("A", 26)
	client, err := mcpclient.New("https://example.test", strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), &http.Client{Transport: controlledTransport(func(request *http.Request) (*http.Response, error) {
		var message proto.Message
		switch request.URL.Path {
		case "/skillhub.auth.v1.AuthService/GetSession":
			message = &authv1.GetSessionResponse{UserId: "synthetic-owner"}
		case "/skillhub.configuration.mcp.v1.MCPConfigurationService/GetAsset":
			message = &mcpv1.GetAssetResponse{Asset: &configurationv1.Asset{Id: id, AgentId: "claude", Name: "tools", Kind: configurationv1.AssetKind_ASSET_KIND_MCP, SchemaVersion: 1, Revision: 1, CreatedAtUnix: 1, UpdatedAtUnix: 1}}
		case "/skillhub.configuration.mcp.v1.MCPConfigurationService/DeleteAsset":
			mutations.Add(1)
			return nil, errors.New("synthetic-upstream-secret-canary")
		default:
			return nil, errors.New("synthetic refusal")
		}
		data, _ := proto.Marshal(message)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/proto"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})})
	if err != nil {
		test.Fatal("setup failed")
	}
	defer client.Close()
	change := mcpclient.Change{Action: "delete", ID: id, Revision: 1}
	operation, err := client.Prepare(test.Context(), change, nil)
	if err != nil {
		test.Fatal("prepare failed")
	}
	defer operation.Close()
	if _, err := operation.Submit(test.Context()); !errors.Is(err, mcpclient.ErrUnknown) {
		test.Fatal("transport uncertainty not blocked")
	}
	if _, err := operation.Submit(test.Context()); !errors.Is(err, mcpclient.ErrUnknown) {
		test.Fatal("same operation replay admitted")
	}
	if _, err := client.Prepare(test.Context(), change, nil); !errors.Is(err, mcpclient.ErrUnknown) {
		test.Fatal("new mutation admitted after uncertainty")
	}
	if _, err := client.Get(test.Context(), id); err != nil {
		test.Fatal("read reconciliation was blocked")
	}
	if mutations.Load() != 1 {
		test.Fatal("application replay occurred")
	}
}

func TestMCPGeneratedStrictResponseWire(test *testing.T) {
	base := &mcpv1.ListAssetsResponse{Assets: []*configurationv1.Asset{{Id: strings.Repeat("A", 26), AgentId: "claude", Name: "tools", Kind: configurationv1.AssetKind_ASSET_KIND_MCP, SchemaVersion: 1, Revision: 1, CreatedAtUnix: 1, UpdatedAtUnix: 1}}}
	data, _ := proto.Marshal(base)
	unknown := append(append([]byte{}, data...), protowire.AppendTag(nil, 99, protowire.VarintType)...)
	unknown = protowire.AppendVarint(unknown, 1)
	asset, _ := proto.Marshal(base.Assets[0])
	duplicate := append(append([]byte{}, asset...), protowire.AppendTag(nil, 6, protowire.VarintType)...)
	duplicate = protowire.AppendVarint(duplicate, 1)
	duplicate = protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), duplicate)
	for _, encoded := range [][]byte{unknown, duplicate, {0x8a, 0x00, 0x00}} {
		client, err := mcpclient.New("https://example.test", strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), &http.Client{Transport: controlledTransport(func(request *http.Request) (*http.Response, error) {
			response := encoded
			if strings.Contains(request.URL.Path, "AuthService") {
				response, _ = proto.Marshal(&authv1.GetSessionResponse{UserId: "synthetic-owner"})
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/proto"}}, Body: io.NopCloser(bytes.NewReader(response))}, nil
		})})
		if err != nil {
			test.Fatal("setup failed")
		}
		if _, err := client.List(test.Context()); err == nil {
			client.Close()
			test.Fatal("malformed/unknown wire accepted")
		}
		client.Close()
	}
}

func TestMCPGeneratedMappingOnlyEightMethods(test *testing.T) {
	id := strings.Repeat("A", 26)
	copyID := strings.Repeat("B", 26)
	asset := func(id, name string, revision int64) *configurationv1.Asset {
		return &configurationv1.Asset{Id: id, Name: name, AgentId: "claude", Kind: configurationv1.AssetKind_ASSET_KIND_MCP, SchemaVersion: 1, Revision: revision, CreatedAtUnix: 1, UpdatedAtUnix: 2}
	}
	declaration := &mcpv1.Draft{Name: "tools", AgentId: "claude", Document: &mcpv1.Document{Servers: []*mcpv1.Server{{Id: "tools", Name: "tools", Enabled: new(false), Connection: &mcpv1.Server_Stdio{Stdio: &mcpv1.Stdio{Command: "tool", Args: &mcpv1.Args{}, Env: &mcpv1.NamedSlots{}}}}}}}
	view := func(id, name string, revision int64) *mcpv1.View {
		copyDraft := proto.Clone(declaration).(*mcpv1.Draft)
		copyDraft.Name = name
		return &mcpv1.View{Asset: asset(id, name, revision), Draft: copyDraft, Readiness: mcpv1.Readiness_READINESS_COMPLETE}
	}
	counts := map[string]int{}
	client, err := mcpclient.New("https://example.test", strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), &http.Client{Transport: controlledTransport(func(request *http.Request) (*http.Response, error) {
		method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		counts[method]++
		var result proto.Message
		switch method {
		case "GetSession":
			result = &authv1.GetSessionResponse{UserId: "synthetic-owner"}
		case "ListAssets":
			result = &mcpv1.ListAssetsResponse{Assets: []*configurationv1.Asset{asset(id, "tools", 1)}}
		case "GetAsset":
			result = &mcpv1.GetAssetResponse{Asset: asset(id, "tools", 1)}
		case "ReadDraft":
			result = &mcpv1.ReadDraftResponse{View: view(id, "tools", 1)}
		case "Preview":
			data, _ := io.ReadAll(request.Body)
			var message mcpv1.PreviewRequest
			if proto.Unmarshal(data, &message) != nil || !proto.Equal(message.Draft, declaration) {
				test.Error("typed preview dropped false/empty/order")
			}
			result = &mcpv1.PreviewResponse{Operations: []*mcpv1.Operation{{ServerId: "tools", NativeName: "tools", Action: mcpv1.Action_ACTION_DISABLE_MANAGED, RequiresManagedProof: true}}, Readiness: mcpv1.Readiness_READINESS_COMPLETE, Warnings: []mcpv1.Warning{mcpv1.Warning_WARNING_NATIVE_NOT_QUALIFIED}}
		case "CreateAsset":
			result = &mcpv1.CreateAssetResponse{View: view(id, "tools", 1)}
		case "UpdateAsset":
			result = &mcpv1.UpdateAssetResponse{View: view(id, "tools", 2)}
		case "CopyAsset":
			result = &mcpv1.CopyAssetResponse{View: view(copyID, "copied", 1)}
		case "DeleteAsset":
			result = &mcpv1.DeleteAssetResponse{}
		default:
			test.Error("non-ledger method invoked")
			return nil, errors.New("synthetic refusal")
		}
		data, err := proto.Marshal(result)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/proto"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
	})})
	if err != nil {
		test.Fatal("mapping client setup failed")
	}
	defer client.Close()
	input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "draft", `{"name":"tools","agentId":"claude","document":{"servers":[{"id":"tools","name":"tools","enabled":false,"stdio":{"command":"tool","args":{},"env":{}}}]}}`), "")
	if err != nil {
		test.Fatal("mapping input setup failed")
	}
	defer input.Close()
	if items, err := client.List(test.Context()); err != nil || len(items) != 1 {
		test.Fatal("typed List failed")
	}
	if item, err := client.Get(test.Context(), id); err != nil || item.ID != id {
		test.Fatal("typed Get failed")
	}
	if _, err := client.Read(test.Context(), id); err != nil {
		test.Fatal("typed Read failed")
	}
	if _, err := client.Preview(test.Context(), "", 0, input); err != nil {
		test.Fatal("typed Preview failed")
	}
	for _, change := range []mcpclient.Change{{Action: "create"}, {Action: "update", ID: id, Revision: 1}, {Action: "copy", ID: id, Revision: 1, Name: "copied"}, {Action: "delete", ID: id, Revision: 1}} {
		var selected *mcpclient.Input
		if change.Action == "create" || change.Action == "update" {
			selected = input
		}
		operation, err := client.Prepare(test.Context(), change, selected)
		if err != nil {
			test.Fatal("typed operation prepare failed")
		}
		var summary bytes.Buffer
		if operation.WriteSummary(test.Context(), &summary) != nil || operation.Confirmation() == "" {
			test.Fatal("typed review failed")
		}
		if _, err := operation.Submit(test.Context()); err != nil {
			test.Fatal("typed mutation failed")
		}
		if _, err := operation.Submit(test.Context()); !errors.Is(err, mcpclient.ErrUnknown) {
			test.Fatal("same mutation admitted twice")
		}
		operation.Close()
	}
	for _, method := range []string{"ListAssets", "GetAsset", "ReadDraft", "Preview", "CreateAsset", "UpdateAsset", "CopyAsset", "DeleteAsset"} {
		if counts[method] < 1 {
			test.Fatal("generated mapping method never invoked")
		}
	}
	for _, method := range []string{"CreateAsset", "UpdateAsset", "CopyAsset", "DeleteAsset"} {
		if counts[method] != 1 {
			test.Fatal("mutation mapping was replayed")
		}
	}
}

type controlledTransport func(*http.Request) (*http.Response, error)

func (transport controlledTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestMCPClientCloseDrainsAndRejectsConcurrentUse(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		entered, cleanup, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		client, err := mcpclient.New("https://example.test", strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), &http.Client{Transport: controlledTransport(func(request *http.Request) (*http.Response, error) {
			close(entered)
			<-request.Context().Done()
			close(cleanup)
			<-release
			return nil, request.Context().Err()
		})})
		if err != nil {
			test.Fatal("client construction failed")
		}
		defer client.Close()
		releaseHeld := sync.OnceFunc(func() { close(release) })
		defer releaseHeld()
		done := make(chan error, 1)
		go func() { done <- client.Authenticate(test.Context()) }()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			test.Fatal("request did not enter")
		}
		if !errors.Is(client.Authenticate(test.Context()), mcpclient.ErrBusy) {
			test.Fatal("overlapping request admitted")
		}
		go func() { client.Close(); close(closed) }()
		select {
		case <-cleanup:
		case <-time.After(3 * time.Second):
			test.Fatal("Close did not cancel active request")
		}
		synctest.Wait()
		select {
		case <-closed:
			test.Fatal("Close returned before transport drained")
		default:
		}
		releaseHeld()
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			test.Fatal("Close failed to drain")
		}
		if !errors.Is(<-done, context.Canceled) {
			test.Fatal("cancellation sentinel lost")
		}
	})
}

func TestMCPInputBoundsAndReplacementStates(test *testing.T) {
	declaration := `{"name":"tools","agentId":"claude","document":{"servers":[{"id":"tools","name":"tools","enabled":false,"stdio":{"command":"tool","args":{"values":[{"id":"first","slotRef":"arg/tools/first"}]}}}]}}`
	for _, action := range []string{`"keep":{}`, `"replace":""`, `"clear":{"confirmed":true}`} {
		input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "draft", declaration), inputFile(test, "secrets", `[{"slotId":"arg/tools/first",`+action+`}]`))
		if err != nil {
			test.Fatal("valid explicit action refused")
		}
		input.Close()
	}
	for _, content := range []string{
		`[{"slotId":"arg/tools/first","replace":"` + strings.Repeat("YQ==", 8193) + `"}]`,
		`[{"slotId":"arg/tools/first","replace":"AA=="}]`,
		`[{"slotId":"arg/tools/first","replace":"/w=="}]`,
		`[{"slotId":"wrong/tools/first","keep":{}}]`,
		`[{"slotId":"arg/tools/first","keep":{}},{"slotId":"arg/tools/first","clear":{"confirmed":true}}]`,
	} {
		if input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "draft", declaration), inputFile(test, "secrets", content)); err == nil {
			input.Close()
			test.Fatal("invalid secret action accepted")
		}
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := mcpclient.LoadInput(ctx, "not-a-real-path", ""); !errors.Is(err, context.Canceled) {
		test.Fatal("pre-cancel sentinel lost")
	}
	oversize := inputFile(test, "oversize", strings.Repeat(" ", 512<<10)+declaration)
	if input, err := mcpclient.LoadInput(test.Context(), oversize, ""); err == nil {
		input.Close()
		test.Fatal("oversize declaration accepted")
	}
	linkedDir := filepath.Join(test.TempDir(), "directory")
	actualDir := test.TempDir()
	if os.WriteFile(filepath.Join(actualDir, "draft"), []byte(declaration), 0600) != nil || os.Symlink(actualDir, linkedDir) != nil {
		test.Fatal("directory symlink setup failed")
	}
	if input, err := mcpclient.LoadInput(test.Context(), filepath.Join(linkedDir, "draft"), ""); err == nil {
		input.Close()
		test.Fatal("followed intermediate symlink")
	}
}

func TestMCPInputLegalCapacityAndOneOver(test *testing.T) {
	var servers []string
	for index := range 32 {
		servers = append(servers, fmt.Sprintf(`{"id":"s%d","name":"s%d","enabled":false,"stdio":{"command":"tool","args":{},"env":{}}}`, index, index))
	}
	for _, count := range []int{32, 33} {
		entries := strings.Join(servers, ",")
		if count == 33 {
			entries += `,{"id":"overflow","name":"overflow","enabled":true,"sse":{"url":"https://example.test/events"}}`
		}
		input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "draft", `{"name":"capacity","agentId":"claude","document":{"servers":[`+entries+`]}}`), "")
		if count == 32 {
			if err != nil {
				test.Fatal("legal 32-server declaration refused")
			}
			input.Close()
		} else if err == nil {
			input.Close()
			test.Fatal("33-server declaration accepted")
		}
	}
	var envs []string
	for index := range 64 {
		envs = append(envs, fmt.Sprintf(`{"name":"KEY_%d","slotRef":"env/tools/KEY_%d"}`, index, index))
	}
	for _, count := range []int{64, 65} {
		entries := strings.Join(envs, ",")
		if count == 65 {
			entries += `,{"name":"OVERFLOW","slotRef":"env/tools/OVERFLOW"}`
		}
		input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "draft", `{"name":"capacity","agentId":"claude","document":{"servers":[{"id":"tools","name":"tools","enabled":true,"stdio":{"command":"tool","env":{"values":[`+entries+`]}}}]}}`), "")
		if count == 64 {
			if err != nil {
				test.Fatal("legal 64-env declaration refused")
			}
			input.Close()
		} else if err == nil {
			input.Close()
			test.Fatal("65-env declaration accepted")
		}
	}
}

func TestMCPCarriersHideSyntheticValues(test *testing.T) {
	canary := "synthetic-MCP-private-9f5a"
	encoded := base64.StdEncoding.EncodeToString([]byte(canary))
	declaration := inputFile(test, "draft", `{"name":"tools","agentId":"claude","document":{"servers":[{"id":"tools","name":"tools","enabled":true,"stdio":{"command":"tool","env":{"values":[{"name":"TOKEN","slotRef":"env/tools/TOKEN"}]}}}]}}`)
	input, err := mcpclient.LoadInput(test.Context(), declaration, inputFile(test, "secrets", `[{"slotId":"env/tools/TOKEN","replace":"`+encoded+`"}]`))
	if err != nil {
		test.Fatal("synthetic private fixture refused")
	}
	defer input.Close()
	client, err := mcpclient.New("https://example.test", strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), nil)
	if err != nil {
		test.Fatal("private client setup failed")
	}
	defer client.Close()
	for _, carrier := range []any{input, client, []any{input, client}, map[string]any{"input": input, "client": client}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x", "%X"} {
			text := fmt.Sprintf(format, carrier)
			for _, forbidden := range []string{canary, encoded, hex.EncodeToString([]byte(canary)), fmt.Sprint([]byte(canary))} {
				if strings.Contains(text, forbidden) {
					test.Fatal("fmt private canary escaped")
				}
			}
		}
		data, err := json.Marshal(carrier)
		if err != nil || bytes.Contains(data, []byte(canary)) || bytes.Contains(data, []byte(encoded)) {
			test.Fatal("JSON private canary escaped")
		}
		for _, jsonLog := range []bool{false, true} {
			var output bytes.Buffer
			var handler slog.Handler = slog.NewTextHandler(&output, nil)
			if jsonLog {
				handler = slog.NewJSONHandler(&output, nil)
			}
			slog.New(handler).Info("private-test", "value", carrier)
			if strings.Contains(output.String(), canary) || strings.Contains(output.String(), encoded) {
				test.Fatal("slog private canary escaped")
			}
		}
	}
}

func TestMCPReplacementExactBudgets(test *testing.T) {
	declaration := `{"name":"tools","agentId":"claude","document":{"servers":[{"id":"tools","name":"tools","enabled":true,"stdio":{"command":"tool","env":{"values":[{"name":"TOKEN","slotRef":"env/tools/TOKEN"}]}}}]}}`
	for _, size := range []int{8192, 8193} {
		encoded := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("a"), size))
		input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "draft", declaration), inputFile(test, "secrets", `[{"slotId":"env/tools/TOKEN","replace":"`+encoded+`"}]`))
		if size == 8192 {
			if err != nil {
				test.Fatal("legal per-value boundary refused")
			}
			input.Close()
		} else if err == nil {
			input.Close()
			test.Fatal("per-value overflow accepted")
		}
	}
}

func TestMCPOldNewUnion1024Boundary(test *testing.T) {
	makeDraft := func(prefix string) string {
		var servers []string
		for serverIndex := range 32 {
			var slots []string
			for slotIndex := range 16 {
				slots = append(slots, fmt.Sprintf(`{"name":"KEY_%d","slotRef":"env/%s%d/KEY_%d"}`, slotIndex, prefix, serverIndex, slotIndex))
			}
			servers = append(servers, fmt.Sprintf(`{"id":"%s%d","name":"%s%d","enabled":false,"stdio":{"command":"tool","env":{"values":[%s]}}}`, prefix, serverIndex, prefix, serverIndex, strings.Join(slots, ",")))
		}
		return `{"name":"capacity","agentId":"claude","document":{"servers":[` + strings.Join(servers, ",") + `]}}`
	}
	old, err := mcpclient.LoadInput(test.Context(), inputFile(test, "old", makeDraft("old")), "")
	if err != nil {
		test.Fatal("legal old 512-ref document refused")
	}
	defer old.Close()
	var actions []string
	for serverIndex := range 32 {
		for slotIndex := range 16 {
			actions = append(actions, fmt.Sprintf(`{"slotId":"env/old%d/KEY_%d","clear":{"confirmed":true}}`, serverIndex, slotIndex))
			actions = append(actions, fmt.Sprintf(`{"slotId":"env/new%d/KEY_%d","replace":""}`, serverIndex, slotIndex))
		}
	}
	input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "new", makeDraft("new")), inputFile(test, "mutations", "["+strings.Join(actions, ",")+"]"))
	if err != nil {
		test.Fatal("legal 1024-action input refused")
	}
	defer input.Close()
	if input.ValidateUpdate(old) != nil {
		test.Fatal("legal 1024 old/new union refused")
	}
	actions = append(actions, `{"slotId":"env/stranger/EXTRA","clear":{"confirmed":true}}`)
	if invalid, err := mcpclient.LoadInput(test.Context(), inputFile(test, "new", makeDraft("new")), inputFile(test, "mutations", "["+strings.Join(actions, ",")+"]")); err == nil {
		invalid.Close()
		test.Fatal("1025 actions accepted")
	}
}

func TestMCPOldOnlyClearIsRequired(test *testing.T) {
	oldDocument := `{"name":"tools","agentId":"claude","document":{"servers":[{"id":"tools","name":"tools","enabled":true,"stdio":{"command":"tool","args":{"values":[{"id":"first","slotRef":"arg/tools/first"}]},"env":{"values":[{"name":"TOKEN","slotRef":"env/tools/TOKEN"}]}}},{"id":"remote","name":"remote","enabled":false,"http":{"url":"https://example.test/mcp","headers":{"values":[{"name":"X-Key","slotRef":"header/remote/X-Key"}]}}}]}}`
	old, err := mcpclient.LoadInput(test.Context(), inputFile(test, "old", oldDocument), "")
	if err != nil {
		test.Fatal("valid previous declaration refused")
	}
	defer old.Close()
	newDocument := `{"name":"tools","agentId":"claude","document":{"servers":[{"id":"tools","name":"tools","enabled":false,"sse":{"url":"https://example.test/events"}}]}}`
	for _, actions := range []string{`[]`, `[{"slotId":"arg/tools/first","keep":{}}]`, `[{"slotId":"env/tools/TOKEN","replace":""}]`, `[{"slotId":"header/foreign/X-Key","clear":{"confirmed":true}}]`} {
		input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "new", newDocument), inputFile(test, "actions", actions))
		if err != nil {
			test.Fatal("old-only structural action fixture refused before union validation")
		}
		if input.ValidateUpdate(old) == nil {
			input.Close()
			test.Fatal("removed secret reference lacked its explicit confirmed CLEAR")
		}
		input.Close()
	}
	actions := `[{"slotId":"arg/tools/first","clear":{"confirmed":true}},{"slotId":"env/tools/TOKEN","clear":{"confirmed":true}},{"slotId":"header/remote/X-Key","clear":{"confirmed":true}}]`
	input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "new", newDocument), inputFile(test, "actions", actions))
	if err != nil {
		test.Fatal("valid removal actions refused")
	}
	defer input.Close()
	if input.ValidateUpdate(old) != nil {
		test.Fatal("complete old-only CLEAR set refused")
	}
	var summary bytes.Buffer
	if input.WriteSummary(test.Context(), &summary) != nil || !strings.Contains(summary.String(), `"sse"`) {
		test.Fatal("union validation corrupted desired transport")
	}
	if input.ValidateUpdate(nil) == nil {
		test.Fatal("foreign CLEAR accepted for a new declaration")
	}
}

func TestMCPInputSnapshotAndTerminalProjection(test *testing.T) {
	path := inputFile(test, "declaration", `{"name":"tools","agentId":"claude","document":{"servers":[{"id":"tools","name":"tools","enabled":true,"stdio":{"command":"tool\u202evalue"}}]}}`)
	input, err := mcpclient.LoadInput(test.Context(), path, "")
	if err != nil {
		test.Fatal("valid declaration rejected")
	}
	defer input.Close()
	if os.WriteFile(path, []byte(`{"name":"changed"}`), 0600) != nil {
		test.Fatal("snapshot fixture write failed")
	}
	var summary bytes.Buffer
	if input.WriteSummary(test.Context(), &summary) != nil {
		test.Fatal("summary failed")
	}
	if strings.Contains(summary.String(), "changed") || strings.ContainsRune(summary.String(), '\u202e') {
		test.Fatal("snapshot mutated or terminal bidi escaped projection missing")
	}
	if !strings.Contains(summary.String(), `\u202e`) {
		test.Fatal("exact quoted declaration lost bidi character")
	}
}

func TestMCPTransportRefusesRedirectAndOversize(test *testing.T) {
	var destination atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destination.Add(1) }))
	defer target.Close()
	for _, mode := range []string{"redirect", "encoding", "oversize"} {
		test.Run(mode, func(test *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch mode {
				case "redirect":
					http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
				case "encoding":
					writer.Header().Set("Content-Encoding", "gzip")
					writer.WriteHeader(200)
				case "oversize":
					writer.Header().Set("Content-Type", "application/proto")
					_, _ = io.WriteString(writer, strings.Repeat("x", 4097))
				}
			}))
			defer server.Close()
			client, err := mcpclient.New(server.URL, strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), nil)
			if err != nil {
				test.Fatal("client setup failed")
			}
			defer client.Close()
			if client.Authenticate(test.Context()) == nil {
				test.Fatal("invalid response accepted")
			}
		})
	}
	if destination.Load() != 0 {
		test.Fatal("credentials redirected to another origin")
	}
}

func TestMCPInputStrictAndPrivate(test *testing.T) {
	valid := `{"name":"tools","agentId":"claude","document":{"servers":[]}}`
	for _, content := range []string{
		`{"name":"tools","name":"other","agentId":"claude","document":{}}`,
		`{"name":"tools","agentId":"claude","document":null}`,
		`{"name":"tools","agentId":"claude","document":{"servers":[null]}}`,
		`{"name":"tools","agentId":"claude","document":{},"extra":true}`,
		`{"name":"tools","agent_id":"claude","document":{}}`,
		`{"name":"tools","agentId":"claude","document":{}} {}`,
		`{"name":"tools","agentId":"claude","document":{"servers":[{"id":"a","name":"a","stdio":{"command":"tool"}}]}}`,
	} {
		if input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "invalid", content), ""); err == nil {
			input.Close()
			test.Fatal("malformed declaration accepted")
		}
	}
	for _, content := range []string{`null`, `[null]`, `[{"slotId":"arg/a/b","replace":"Zg"}]`, `[{"slotId":"arg/a/b","replace":"Zh=="}]`, `[{"slotId":"arg/a/b","keep":{},"replace":""}]`, `[{"slotId":"arg/a/b","clear":{"confirmed":false}}]`} {
		if input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "draft", valid), inputFile(test, "secrets", content)); err == nil {
			input.Close()
			test.Fatal("malformed mutation accepted")
		}
	}
	input, err := mcpclient.LoadInput(test.Context(), inputFile(test, "draft", valid), "")
	if err != nil {
		test.Fatal("valid empty document refused")
	}
	defer input.Close()
	for _, carrier := range []any{input, any(input), []any{input}, map[string]any{"input": input}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
			if strings.Contains(fmt.Sprintf(format, carrier), "tools") {
				test.Fatal("fmt exposed private declaration")
			}
		}
		data, err := json.Marshal(carrier)
		if err != nil || bytes.Contains(data, []byte("tools")) {
			test.Fatal("JSON exposed private declaration")
		}
		var output bytes.Buffer
		slog.New(slog.NewJSONHandler(&output, nil)).Info("test", "input", carrier)
		if strings.Contains(output.String(), "tools") {
			test.Fatal("slog exposed private declaration")
		}
	}
	linked := filepath.Join(test.TempDir(), "link")
	if err := os.Symlink(inputFile(test, "draft", valid), linked); err != nil {
		test.Fatal("symlink setup failed")
	}
	if loaded, err := mcpclient.LoadInput(test.Context(), linked, ""); err == nil {
		loaded.Close()
		test.Fatal("followed symlink")
	}
	loose := inputFile(test, "secrets", `[]`)
	if os.Chmod(loose, 0644) != nil {
		test.Fatal("mode setup failed")
	}
	if loaded, err := mcpclient.LoadInput(test.Context(), inputFile(test, "draft", valid), loose); err == nil {
		loaded.Close()
		test.Fatal("public secrets file accepted")
	}
}

func TestMCPTransportIsolation(test *testing.T) {
	var arrivals, traces atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		arrivals.Add(1)
		if request.URL.Path != "/skillhub.auth.v1.AuthService/GetSession" || request.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 43)+"."+strings.Repeat("b", 43) {
			test.Error("wrong controlled auth envelope")
		}
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := mcpclient.New(server.URL, strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), server.Client())
	if err != nil {
		test.Fatal("valid origin rejected")
	}
	defer client.Close()
	ctx := httptrace.WithClientTrace(test.Context(), &httptrace.ClientTrace{WroteHeaderField: func(string, []string) { traces.Add(1) }})
	if err := client.Authenticate(ctx); err == nil {
		test.Fatal("unavailable service accepted")
	}
	if arrivals.Load() != 1 || traces.Load() != 0 {
		test.Fatal("request was replayed or inherited trace callback")
	}
	client.Close()
	if err := client.Authenticate(test.Context()); !errors.Is(err, mcpclient.ErrClosed) {
		test.Fatal("closed client admitted request")
	}
	for _, origin := range []string{"http://example.test", server.URL + "/path", server.URL + "?x=1", "https://user:pass@example.test"} {
		if client, err := mcpclient.New(origin, strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), nil); err == nil {
			client.Close()
			test.Fatal("unsafe origin accepted")
		}
	}
}

func TestMCPInputPresenceOrderAndUnion(test *testing.T) {
	declaration := inputFile(test, "declaration.json", `{"name":"tools","agentId":"claude","document":{"servers":[{"id":"tools","name":"tools","enabled":false,"stdio":{"command":"tool","args":{"values":[]},"env":{"values":[{"name":"API_KEY","slotRef":"env/tools/API_KEY"}]}}},{"id":"remote","name":"remote","enabled":true,"http":{"url":"https://example.test/mcp","headers":{}}},{"id":"legacy","name":"legacy","enabled":true,"sse":{"url":"http://127.0.0.1/events"}}]}}`)
	secrets := inputFile(test, "mutations.json", `[{"slotId":"env/tools/API_KEY","replace":"c3ludGhldGljLWNhbmFyeQ=="}]`)
	input, err := mcpclient.LoadInput(test.Context(), declaration, secrets)
	if err != nil {
		test.Fatal("valid full declaration rejected")
	}
	defer input.Close()
	var summary bytes.Buffer
	if err := input.WriteSummary(test.Context(), &summary); err != nil {
		test.Fatal("summary failed")
	}
	text := summary.String()
	for _, expected := range []string{`"enabled":false`, `"args":{"values":[]}`, `"headers":{"values":[]}`, "env/tools/API_KEY", "REPLACE"} {
		if !strings.Contains(text, expected) {
			test.Fatalf("summary missing required presence marker %q", expected)
		}
	}
	if strings.Contains(text, "synthetic-canary") || strings.Contains(text, "c3ludGhldGlj") {
		test.Fatal("summary leaked replacement")
	}
	if !(strings.Index(text, `"id":"tools"`) < strings.Index(text, `"id":"remote"`) && strings.Index(text, `"id":"remote"`) < strings.Index(text, `"id":"legacy"`)) {
		test.Fatal("server order changed")
	}
	input.Close()
	if !errors.Is(input.WriteSummary(context.Background(), &summary), mcpclient.ErrClosed) {
		test.Fatal("closed input remained usable")
	}
}
