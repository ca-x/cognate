package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ca-x/cognate/web"
)

func TestEmbeddedUIAndDeepLink(t *testing.T) {
	for _, path := range []string{"/", "/repositories", "/account", "/packs", "/categories", "/explore", "/explore/skill-ID_123", "/mcp-settings", "/configuration"} {
		response := httptest.NewRecorder()
		web.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="root"`) || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("UI unavailable on %s: %d", path, response.Code)
		}
		if !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || strings.Contains(response.Header().Get("Content-Security-Policy"), "unsafe-") {
			t.Fatal("missing UI content security policy")
		}
	}
	for _, path := range []string{"/assets/missing.js", "/skillhub.missing.v1.Service/Method", "/../../go.mod", "/explore/", "/explore/a/b", "/explore/..", "/explore/has%20space", "/explore/id%2Fother", "/categories/private", "/mcp", "/configuration/", "/configuration/private", "/configuration.json"} {
		response := httptest.NewRecorder()
		web.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code == http.StatusOK || strings.Contains(response.Body.String(), "module github.com") {
			t.Fatalf("unexpected resource accessible: %s", path)
		}
	}
}

func TestConfigurationHeadDeepLink(test *testing.T) {
	response := httptest.NewRecorder()
	web.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/configuration", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
		test.Fatal("configuration HEAD does not serve the embedded application metadata")
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || strings.Contains(response.Header().Get("Content-Security-Policy"), "unsafe-") {
		test.Fatal("configuration HEAD weakened the content security policy")
	}
}
