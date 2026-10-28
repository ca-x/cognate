package oidclogin_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/oidclogin"
	"github.com/ca-x/cognate/web"
)

func TestOIDCBrowserFixture(t *testing.T) {
	address := os.Getenv("SKILLHUB_OIDC_BROWSER_FIXTURE")
	if address == "" {
		t.Skip("manual browser protocol acceptance fixture")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if tcp, ok := listener.Addr().(*net.TCPAddr); !ok || !tcp.IP.IsLoopback() {
		_ = listener.Close()
		t.Fatal("browser fixture must use loopback")
	}
	fixture := newIssuer(t)
	service, store := testServices(t)
	manager, err := oidclogin.New(t.Context(), service, oidclogin.Config{Issuer: fixture.server.URL, ClientID: "test-client", PublicOrigin: "http://" + listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	handler := httpapi.NewWithOptions(service, httpapi.Options{Store: store, OIDC: manager, WebUI: web.Handler()})
	finished := make(chan struct{})
	var finish sync.Once
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/__test/finish" && request.Method == http.MethodPost {
			finish.Do(func() { close(finished) })
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		handler.ServeHTTP(writer, request)
	}))
	_ = server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	t.Log("browser fixture ready at", server.URL)
	select {
	case <-finished:
	case <-time.After(3 * time.Minute):
		t.Fatal("browser fixture was not completed")
	}
}
