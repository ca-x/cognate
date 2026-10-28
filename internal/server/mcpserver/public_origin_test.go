package mcpserver_test

import (
	"net/http"
	"testing"

	"github.com/ca-x/skillhub/internal/server/mcpserver"
	"github.com/ca-x/skillhub/internal/server/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type originTransport struct {
	next   http.RoundTripper
	host   string
	origin string
}

func (transport originTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Host = transport.host
	request.Header.Set("Origin", transport.origin)
	return transport.next.RoundTrip(request)
}

func TestSDKConfiguredPublicOriginThroughLoopbackProxy(test *testing.T) {
	fixture := newFixture(test, nil)
	_, server := fixture.endpoint(test, mcpserver.Options{PublicOrigin: "https://skillhub.example"})
	_, secret := fixture.token(test, "alice", state.MCPCatalogRead)
	for _, scenario := range []struct {
		host    string
		origin  string
		allowed bool
	}{
		{"skillhub.example", "https://skillhub.example", true},
		{"attacker.invalid", "https://skillhub.example", false},
		{"skillhub.example", "https://attacker.invalid", false},
	} {
		client := mcp.NewClient(&mcp.Implementation{Name: "proxy-client", Version: "1"}, nil)
		transport := originTransport{next: &bearerTransport{token: secret.Reveal()}, host: scenario.host, origin: scenario.origin}
		session, err := client.Connect(test.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL + mcpserver.Path, HTTPClient: &http.Client{Transport: transport}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if !scenario.allowed {
			if err == nil {
				_ = session.Close()
				test.Fatal("forged public Host/Origin accepted")
			}
			continue
		}
		if err != nil {
			test.Fatalf("configured public Host rejected through a loopback proxy: %v", err)
		}
		decode[mcpserver.CatalogPage](test, call(test, session, "catalog_list", mcpserver.PageInput{}))
		_ = session.Close()
	}
}
