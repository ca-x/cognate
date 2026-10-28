package oidclogin_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/server/auth"
	"github.com/ca-x/skillhub/internal/server/oidclogin"
	"github.com/ca-x/skillhub/internal/server/state"
	"github.com/go-jose/go-jose/v4"
)

type issuerFixture struct {
	server       *httptest.Server
	challenge    string
	nonce        string
	subject      string
	badClaim     string
	tokenEntered chan struct{}
	keysEntered  chan struct{}
	keysCanceled chan struct{}
}

func newIssuer(t *testing.T) *issuerFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &issuerFixture{subject: "subject"}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		var response any
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			response = map[string]any{"issuer": fixture.server.URL, "authorization_endpoint": fixture.server.URL + "/authorize", "token_endpoint": fixture.server.URL + "/token", "jwks_uri": fixture.server.URL + "/keys", "id_token_signing_alg_values_supported": []string{"ES256"}}
		case "/authorize":
			parameters := request.URL.Query()
			fixture.nonce, fixture.challenge = parameters.Get("nonce"), parameters.Get("code_challenge")
			target, err := url.Parse(parameters.Get("redirect_uri"))
			if err != nil {
				http.Error(writer, "invalid redirect", http.StatusBadRequest)
				return
			}
			query := target.Query()
			query.Set("state", parameters.Get("state"))
			query.Set("code", "issued-code")
			target.RawQuery = query.Encode()
			http.Redirect(writer, request, target.String(), http.StatusSeeOther)
			return
		case "/keys":
			if fixture.keysEntered != nil {
				close(fixture.keysEntered)
				<-request.Context().Done()
				close(fixture.keysCanceled)
				return
			}
			response = jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Use: "sig", Algorithm: "ES256"}}}
		case "/token":
			if err := request.ParseForm(); err != nil {
				http.Error(writer, "invalid request", http.StatusBadRequest)
				return
			}
			if fixture.tokenEntered != nil {
				close(fixture.tokenEntered)
				<-request.Context().Done()
				return
			}
			challenge := sha256.Sum256([]byte(request.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(challenge[:]) != fixture.challenge || request.Form.Get("client_id") != "test-client" {
				http.Error(writer, "invalid PKCE", http.StatusBadRequest)
				return
			}
			claims := map[string]any{"iss": fixture.server.URL, "sub": fixture.subject, "aud": "test-client", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "nonce": fixture.nonce}
			switch fixture.badClaim {
			case "nonce":
				claims["nonce"] = "wrong"
			case "audience":
				claims["aud"] = "wrong"
			case "issuer":
				claims["iss"] = "https://wrong.example"
			case "expiry":
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			case "azp":
				claims["azp"] = "wrong"
			}
			payload, err := json.Marshal(claims)
			if err != nil {
				http.Error(writer, "fixture encoding failed", 500)
				return
			}
			message, err := signer.Sign(payload)
			if err != nil {
				http.Error(writer, "fixture signing failed", 500)
				return
			}
			encoded, err := message.CompactSerialize()
			if err != nil {
				http.Error(writer, "fixture signing failed", 500)
				return
			}
			if fixture.badClaim == "signature" {
				encoded += "x"
			}
			response = map[string]any{"access_token": rand.Text(), "token_type": "Bearer", "id_token": encoded}
		default:
			http.NotFound(writer, request)
			return
		}
		data, err := json.Marshal(response)
		if err != nil {
			http.Error(writer, "fixture encoding failed", 500)
			return
		}
		_, _ = writer.Write(data)
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func testAuth(t *testing.T) *auth.Service {
	t.Helper()
	service, _ := testServices(t)
	return service
}

func testServices(t *testing.T) (*auth.Service, *state.Store) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := auth.NewService(store, key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func begin(t *testing.T, manager *oidclogin.Manager, fixture *issuerFixture, binding string) (oidclogin.BeginResult, string) {
	t.Helper()
	result, err := manager.Begin(t.Context(), binding)
	if err != nil {
		t.Fatal(err)
	}
	address, err := url.Parse(result.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	fixture.nonce, fixture.challenge = address.Query().Get("nonce"), address.Query().Get("code_challenge")
	if fixture.nonce == "" || fixture.challenge == "" || address.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("OIDC request lacks nonce or PKCE")
	}
	return result, address.Query().Get("state")
}

func TestOIDCLoginUsesOneTimeBrowserBoundHandoff(t *testing.T) {
	fixture, service := newIssuer(t), testAuth(t)
	manager, err := oidclogin.New(t.Context(), service, oidclogin.Config{Issuer: fixture.server.URL, ClientID: "test-client", PublicOrigin: "http://127.0.0.1:18999"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	started, stateValue := begin(t, manager, fixture, "")
	if _, err := manager.Complete(t.Context(), stateValue, "wrong-browser", "issued-code"); !errors.Is(err, oidclogin.ErrLoginFailed) {
		t.Fatalf("browser binding: %v", err)
	}
	result, err := manager.Complete(t.Context(), stateValue, started.BrowserSecret, "issued-code")
	if err != nil || result == "" {
		t.Fatalf("OIDC completion: %v", err)
	}
	if _, err := manager.Complete(t.Context(), stateValue, started.BrowserSecret, "issued-code"); !errors.Is(err, oidclogin.ErrLoginFailed) {
		t.Fatalf("state replay: %v", err)
	}
	token, user, err := manager.Redeem(t.Context(), result)
	if err != nil || user == "" {
		t.Fatalf("handoff: %v", err)
	}
	if actual, err := service.Authenticate(t.Context(), token.Reveal()); err != nil || actual != user {
		t.Fatalf("OIDC session: %v", err)
	}
	if _, _, err := manager.Redeem(t.Context(), result); !errors.Is(err, oidclogin.ErrLoginFailed) {
		t.Fatalf("handoff replay: %v", err)
	}
}

func TestOIDCRejectsInvalidIdentityProof(t *testing.T) {
	for _, kind := range []string{"nonce", "audience", "issuer", "expiry", "signature", "azp"} {
		t.Run(kind, func(t *testing.T) {
			fixture, service := newIssuer(t), testAuth(t)
			fixture.badClaim = kind
			manager, err := oidclogin.New(t.Context(), service, oidclogin.Config{Issuer: fixture.server.URL, ClientID: "test-client", PublicOrigin: "http://127.0.0.1:18999"})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			started, stateValue := begin(t, manager, fixture, "")
			if _, err := manager.Complete(t.Context(), stateValue, started.BrowserSecret, "issued-code"); !errors.Is(err, oidclogin.ErrLoginFailed) {
				t.Fatalf("invalid proof accepted: %v", err)
			}
		})
	}
}

func TestOIDCBindingRechecksOriginalSession(t *testing.T) {
	fixture, service := newIssuer(t), testAuth(t)
	password := []byte(rand.Text())
	if _, err := service.Register(t.Context(), "alice", password); err != nil {
		t.Fatal(err)
	}
	session, err := service.Login(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := oidclogin.New(t.Context(), service, oidclogin.Config{Issuer: fixture.server.URL, ClientID: "test-client", PublicOrigin: "http://127.0.0.1:18999"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	started, stateValue := begin(t, manager, fixture, session.Reveal())
	if err := service.Logout(t.Context(), session.Reveal()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Complete(t.Context(), stateValue, started.BrowserSecret, "issued-code"); !errors.Is(err, oidclogin.ErrLoginFailed) {
		t.Fatalf("revoked binding session accepted: %v", err)
	}
}

func TestOIDCExplicitBindingKeepsTheCurrentUser(t *testing.T) {
	fixture, service := newIssuer(t), testAuth(t)
	password := []byte(rand.Text())
	user, err := service.Register(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Login(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := oidclogin.New(t.Context(), service, oidclogin.Config{Issuer: fixture.server.URL, ClientID: "test-client", PublicOrigin: "http://127.0.0.1:18999"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for _, binding := range []string{session.Reveal(), ""} {
		started, stateValue := begin(t, manager, fixture, binding)
		proof, err := manager.Complete(t.Context(), stateValue, started.BrowserSecret, "issued-code")
		if err != nil {
			t.Fatal(err)
		}
		_, actual, err := manager.Redeem(t.Context(), proof)
		if err != nil || actual != user {
			t.Fatalf("bound identity changed internal user: %v", err)
		}
	}
}

func TestOIDCCloseCancelsTokenExchange(t *testing.T) {
	fixture, service := newIssuer(t), testAuth(t)
	fixture.tokenEntered = make(chan struct{})
	manager, err := oidclogin.New(t.Context(), service, oidclogin.Config{Issuer: fixture.server.URL, ClientID: "test-client", PublicOrigin: "http://127.0.0.1:18999"})
	if err != nil {
		t.Fatal(err)
	}
	started, stateValue := begin(t, manager, fixture, "")
	finished := make(chan error, 1)
	go func() {
		_, err := manager.Complete(t.Context(), stateValue, started.BrowserSecret, "issued-code")
		finished <- err
	}()
	select {
	case <-fixture.tokenEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("token exchange did not start")
	}
	manager.Close()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("closed manager completed login")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manager did not cancel exchange")
	}
}

func TestOIDCCloseCancelsBackgroundJWKSRequest(t *testing.T) {
	fixture, service := newIssuer(t), testAuth(t)
	fixture.keysEntered, fixture.keysCanceled = make(chan struct{}), make(chan struct{})
	manager, err := oidclogin.New(t.Context(), service, oidclogin.Config{Issuer: fixture.server.URL, ClientID: "test-client", PublicOrigin: "http://127.0.0.1:18999"})
	if err != nil {
		t.Fatal(err)
	}
	started, stateValue := begin(t, manager, fixture, "")
	finished := make(chan error, 1)
	go func() {
		_, err := manager.Complete(t.Context(), stateValue, started.BrowserSecret, "issued-code")
		finished <- err
	}()
	select {
	case <-fixture.keysEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("JWKS request did not start")
	}
	manager.Close()
	if err := <-finished; err == nil {
		t.Fatal("closed manager completed login")
	}
	select {
	case <-fixture.keysCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("JWKS request survived shutdown")
	}
}
