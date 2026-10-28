package server_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/cognate/internal/server"
)

const importPreviewPath = "/skillhub.import.v1.ImportService/PreviewLibraryExport"
const ccSwitchPreviewPath = "/skillhub.import.v1.ImportService/PreviewCCSwitchSnapshot"

type importAppFixture struct {
	app     *server.App
	address string
	session string
	client  *http.Client
}

func openImportApp(t *testing.T) importAppFixture {
	t.Helper()
	base := t.TempDir()
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(base, "master.key")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	clear(key)
	app, err := server.Open(t.Context(), server.Config{DataDirectory: filepath.Join(base, "data"), MasterKeyFile: keyPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() { finished <- app.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server shutdown timed out")
		}
	})
	client := &http.Client{Timeout: 9 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	authentication := authv1connect.NewAuthServiceClient(client, "http://"+listener.Addr().String())
	password := rand.Text()
	if _, err := authentication.Register(t.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "import-app", Password: password})); err != nil {
		t.Fatal(err)
	}
	login, err := authentication.Login(t.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "import-app", Password: password}))
	if err != nil {
		t.Fatal(err)
	}
	return importAppFixture{app: app, address: listener.Addr().String(), session: login.Msg.SessionToken, client: client}
}

func (fixture importAppFixture) post(t *testing.T, path, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+fixture.address+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	request.Header.Set("Authorization", "Bearer "+fixture.session)
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal("request failed")
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func magpieRequest(export string) string {
	return `{"format":"EXPORT_FORMAT_MAGPIE_LIBRARY_JSON","exportData":"` + base64.StdEncoding.EncodeToString([]byte(export)) + `"}`
}

func TestAppImportPreviewDoesNotCollect(t *testing.T) {
	fixture := openImportApp(t)
	body := magpieRequest(`{"skills":[{"name":"example","source":{"kind":"github","repo":"Example/Skills","ref":"HEAD"}}]}`)
	for range 2 {
		response := fixture.post(t, importPreviewPath, body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("preview status: %d", response.StatusCode)
		}
		var preview struct {
			Repositories []struct {
				Repository           string `json:"repository"`
				Branch               string `json:"branch"`
				ExistingRepositoryID string `json:"existingRepositoryId"`
			} `json:"repositories"`
			ReferencedSkillCount uint32 `json:"referencedSkillCount"`
		}
		if err := json.UnmarshalRead(response.Body, &preview); err != nil {
			t.Fatal("invalid preview response")
		}
		if len(preview.Repositories) != 1 || preview.ReferencedSkillCount != 1 || preview.Repositories[0].Repository != "example/skills" || preview.Repositories[0].Branch != "" || preview.Repositories[0].ExistingRepositoryID != "" {
			t.Fatal("preview did not preserve normalized reference-only semantics")
		}
	}
	response := fixture.post(t, "/skillhub.library.v1.LibraryService/ListRepositories", `{}`)
	var listing struct {
		Repositories []any `json:"repositories"`
	}
	if response.StatusCode != http.StatusOK || json.UnmarshalRead(response.Body, &listing) != nil || len(listing.Repositories) != 0 {
		t.Fatal("preview changed the repository library")
	}
}

func (fixture importAppFixture) slowUpload(t *testing.T) net.Conn {
	t.Helper()
	return fixture.slowUploadPath(t, importPreviewPath)
}

func (fixture importAppFixture) slowUploadPath(t *testing.T, path string) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp", fixture.address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := connection.SetDeadline(time.Now().Add(9 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nConnect-Protocol-Version: 1\r\nExpect: 100-continue\r\nContent-Length: 1024\r\n\r\n", path, fixture.address, fixture.session); err != nil {
		t.Fatal("could not start partial upload")
	}
	interim, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatal("could not observe partial upload admission")
	}
	_ = interim.Body.Close()
	if interim.StatusCode != http.StatusContinue {
		t.Fatalf("partial upload was rejected before body admission: %d", interim.StatusCode)
	}
	if _, err := io.WriteString(connection, "{"); err != nil {
		t.Fatal("could not send partial upload body")
	}
	return connection
}

func TestAppCCSwitchSnapshotSharedAdmissionAndClose(t *testing.T) {
	fixture := openImportApp(t)
	connection := fixture.slowUploadPath(t, ccSwitchPreviewPath)
	response := fixture.post(t, importPreviewPath, magpieRequest(`{"skills":[]}`))
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("cc-switch snapshot did not share the per-owner import slot: %d", response.StatusCode)
	}
	closed := make(chan error, 1)
	go func() { closed <- fixture.app.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close snapshot request: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot upload was not interrupted before store teardown")
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatal("closed snapshot upload did not produce a response")
	}
	defer response.Body.Close()
	if response.StatusCode < 400 {
		t.Fatal("closed snapshot upload unexpectedly succeeded")
	}
	response = fixture.post(t, ccSwitchPreviewPath, `{}`)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("closed snapshot handler accessed the store")
	}
}

func TestAppCCSwitchSnapshotSlowBodyDeadline(t *testing.T) {
	fixture := openImportApp(t)
	started := time.Now()
	connection := fixture.slowUploadPath(t, ccSwitchPreviewPath)
	if err := connection.SetDeadline(time.Now().Add(19 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("snapshot upload did not reach its server read deadline: %v", err)
	}
	defer response.Body.Close()
	elapsed := time.Since(started)
	if response.StatusCode < 400 || elapsed < 14*time.Second || elapsed > 17500*time.Millisecond {
		t.Fatalf("snapshot slow-body status=%d elapsed=%s", response.StatusCode, elapsed)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal("snapshot deadline response did not finish")
	}
	response = fixture.post(t, importPreviewPath, magpieRequest(`{"skills":[]}`))
	if response.StatusCode != http.StatusOK {
		t.Fatal("snapshot deadline did not release the shared owner slot")
	}
}

func TestAppImportPreviewSlowBodyDeadline(t *testing.T) {
	fixture := openImportApp(t)
	started := time.Now()
	connection := fixture.slowUpload(t)
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("upload deadline did not produce an HTTP rejection: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 400 || time.Since(started) > 7500*time.Millisecond {
		t.Fatalf("slow upload status=%d elapsed=%s", response.StatusCode, time.Since(started))
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal("could not finish deadline response")
	}
	response = fixture.post(t, importPreviewPath, magpieRequest(`{"skills":[]}`))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("slot not released after slow upload: %d", response.StatusCode)
	}
}

func TestAppImportCloseDrainsUploadBeforeStore(t *testing.T) {
	fixture := openImportApp(t)
	connection := fixture.slowUpload(t)
	response := fixture.post(t, importPreviewPath, magpieRequest(`{"skills":[]}`))
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("admitted partial upload did not retain its owner slot: %d", response.StatusCode)
	}
	finished := make(chan error, 1)
	go func() { finished <- fixture.app.Close() }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("close failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not interrupt and drain the admitted upload")
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("drained upload did not finish with an HTTP response: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 400 {
		t.Fatalf("closed upload unexpectedly succeeded: %d", response.StatusCode)
	}
	response = fixture.post(t, importPreviewPath, magpieRequest(`{"skills":[]}`))
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("closed preview did not reject before accessing the closed store: %d", response.StatusCode)
	}
}
