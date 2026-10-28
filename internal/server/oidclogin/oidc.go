package oidclogin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ca-x/skillhub/internal/server/auth"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const CallbackPath = "/auth/oidc/callback"

var (
	ErrLoginFailed = errors.New("OpenID Connect login failed")
	ErrBusy        = errors.New("OpenID Connect capacity exhausted")
)

type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	PublicOrigin string
}

func (Config) String() string   { return "[redacted OpenID Connect configuration]" }
func (Config) GoString() string { return "[redacted OpenID Connect configuration]" }

type BeginResult struct {
	AuthorizationURL string
	BrowserSecret    string
}

func (BeginResult) String() string   { return "[redacted OpenID Connect transaction]" }
func (BeginResult) GoString() string { return "[redacted OpenID Connect transaction]" }

type transaction struct {
	proof    [32]byte
	nonce    string
	verifier string
	binding  string
	user     string
	expires  time.Time
}

type loginResult struct {
	token   auth.SessionToken
	user    string
	expires time.Time
	ready   bool
}

type Manager struct {
	mutex    sync.Mutex
	closed   bool
	workers  sync.WaitGroup
	calls    map[string]context.CancelFunc
	pending  map[string]transaction
	results  map[string]loginResult
	config   *oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
	auth     *auth.Service
	issuer   string
	secure   bool
	network  *limitedTransport
}

func (*Manager) String() string   { return "[redacted OpenID Connect manager]" }
func (*Manager) GoString() string { return "[redacted OpenID Connect manager]" }

type limitedBody struct {
	io.Reader
	io.Closer
	release func()
}

func (body limitedBody) Close() error {
	defer body.release()
	return body.Closer.Close()
}

type limitedTransport struct {
	base      http.RoundTripper
	mutex     sync.Mutex
	closed    bool
	workers   sync.WaitGroup
	interrupt func(context.CancelFunc) func() bool
	cancel    context.CancelFunc
}

func (transport *limitedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mutex.Lock()
	if transport.closed {
		transport.mutex.Unlock()
		return nil, context.Canceled
	}
	transport.workers.Add(1)
	transport.mutex.Unlock()
	ctx, cancel := context.WithCancel(request.Context())
	stop := transport.interrupt(cancel)
	release := sync.OnceFunc(func() { stop(); cancel(); transport.workers.Done() })
	response, err := transport.base.RoundTrip(request.WithContext(ctx))
	if err != nil {
		release()
		return nil, err
	}
	response.Body = limitedBody{Reader: io.LimitReader(response.Body, 1<<20), Closer: response.Body, release: release}
	return response, err
}

func (transport *limitedTransport) Close() {
	transport.mutex.Lock()
	transport.closed = true
	transport.cancel()
	transport.mutex.Unlock()
	transport.workers.Wait()
}

func endpoint(raw string, allowHTTP bool) (*url.URL, error) {
	address, err := url.Parse(raw)
	if err != nil || address.Host == "" || address.User != nil || address.Fragment != "" {
		return nil, fmt.Errorf("invalid OpenID Connect URL")
	}
	if address.Scheme == "https" {
		return address, nil
	}
	if address.Scheme == "http" && allowHTTP {
		host := address.Hostname()
		parsed, err := netip.ParseAddr(host)
		if strings.EqualFold(host, "localhost") || err == nil && parsed.IsLoopback() {
			return address, nil
		}
	}
	return nil, fmt.Errorf("OpenID Connect requires HTTPS; HTTP is limited to loopback development")
}

func New(ctx context.Context, service *auth.Service, config Config) (*Manager, error) {
	if service == nil || len(config.ClientID) == 0 || len(config.ClientID) > 256 || strings.ContainsAny(config.ClientID, "\x00\r\n") {
		return nil, fmt.Errorf("invalid OpenID Connect configuration")
	}
	issuer, err := endpoint(config.Issuer, true)
	if err != nil || issuer.RawQuery != "" || issuer.ForceQuery {
		return nil, fmt.Errorf("invalid OpenID Connect issuer")
	}
	origin, err := endpoint(config.PublicOrigin, true)
	if err != nil || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery {
		return nil, fmt.Errorf("OpenID Connect requires an explicit public origin")
	}
	networkCtx, cancelNetwork := context.WithCancel(context.Background())
	network := &limitedTransport{base: http.DefaultTransport, cancel: cancelNetwork, interrupt: func(cancel context.CancelFunc) func() bool { return context.AfterFunc(networkCtx, cancel) }}
	initialized := false
	defer func() {
		if !initialized {
			network.Close()
		}
	}()
	client := &http.Client{Timeout: 10 * time.Second, Transport: network, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("OpenID Connect discovery failed")
	}
	var metadata struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("invalid OpenID Connect discovery")
	}
	providerEndpoint := provider.Endpoint()
	for _, address := range []string{providerEndpoint.AuthURL, providerEndpoint.TokenURL, metadata.JWKSURI} {
		if _, err := endpoint(address, issuer.Scheme == "http"); err != nil {
			return nil, err
		}
	}
	if config.ClientSecret == "" {
		providerEndpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	oauthConfig := &oauth2.Config{ClientID: config.ClientID, ClientSecret: config.ClientSecret, RedirectURL: config.PublicOrigin + CallbackPath, Scopes: []string{oidc.ScopeOpenID}, Endpoint: providerEndpoint}
	verifier := provider.VerifierContext(oidc.ClientContext(context.Background(), client), &oidc.Config{ClientID: config.ClientID, SupportedSigningAlgs: []string{"RS256", "ES256", "ES384", "ES512", "EdDSA"}})
	initialized = true
	return &Manager{calls: make(map[string]context.CancelFunc), pending: make(map[string]transaction), results: make(map[string]loginResult), config: oauthConfig, verifier: verifier, client: client, auth: service, issuer: config.Issuer, secure: origin.Scheme == "https", network: network}, nil
}

func (manager *Manager) SecureCookies() bool { return manager.secure }

func (manager *Manager) enter(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if manager.closed {
		return nil, nil, ErrLoginFailed
	}
	if len(manager.calls) >= 32 {
		return nil, nil, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	id := rand.Text()
	manager.calls[id] = cancel
	manager.workers.Add(1)
	return ctx, func() {
		cancel()
		manager.mutex.Lock()
		delete(manager.calls, id)
		manager.mutex.Unlock()
		manager.workers.Done()
	}, nil
}

func (manager *Manager) prune() {
	now := time.Now()
	for id, item := range manager.pending {
		if !now.Before(item.expires) {
			delete(manager.pending, id)
		}
	}
	for id, item := range manager.results {
		if !now.Before(item.expires) {
			delete(manager.results, id)
		}
	}
}

func (manager *Manager) Begin(ctx context.Context, binding string) (BeginResult, error) {
	ctx, done, err := manager.enter(ctx)
	if err != nil {
		return BeginResult{}, err
	}
	defer done()
	var user string
	if binding != "" {
		user, err = manager.auth.Authenticate(ctx, binding)
		if err != nil {
			return BeginResult{}, ErrLoginFailed
		}
	}
	stateValue, browser, nonce, verifier := rand.Text(), rand.Text(), rand.Text(), oauth2.GenerateVerifier()
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	manager.prune()
	if manager.closed || ctx.Err() != nil {
		return BeginResult{}, ErrLoginFailed
	}
	if len(manager.pending) >= 128 {
		return BeginResult{}, ErrBusy
	}
	manager.pending[stateValue] = transaction{proof: sha256.Sum256([]byte(browser)), nonce: nonce, verifier: verifier, binding: binding, user: user, expires: time.Now().Add(5 * time.Minute)}
	return BeginResult{AuthorizationURL: manager.config.AuthCodeURL(stateValue, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), BrowserSecret: browser}, nil
}

func (manager *Manager) consume(stateValue, browser string) (transaction, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	manager.prune()
	item, found := manager.pending[stateValue]
	proof := sha256.Sum256([]byte(browser))
	if !found || browser == "" || subtle.ConstantTimeCompare(item.proof[:], proof[:]) != 1 {
		return transaction{}, ErrLoginFailed
	}
	delete(manager.pending, stateValue)
	return item, nil
}

func (manager *Manager) Complete(ctx context.Context, stateValue, browser, code string) (string, error) {
	ctx, done, err := manager.enter(ctx)
	if err != nil {
		return "", err
	}
	defer done()
	if len(stateValue) > 128 || len(browser) > 128 || len(code) == 0 || len(code) > 4096 {
		return "", ErrLoginFailed
	}
	item, err := manager.consume(stateValue, browser)
	if err != nil {
		return "", err
	}
	token, err := manager.config.Exchange(context.WithValue(ctx, oauth2.HTTPClient, manager.client), code, oauth2.VerifierOption(item.verifier))
	if err != nil {
		return "", ErrLoginFailed
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || len(raw) > 64<<10 {
		return "", ErrLoginFailed
	}
	identity, err := manager.verifier.Verify(ctx, raw)
	if err != nil || identity.Issuer != manager.issuer || identity.Nonce != item.nonce || identity.Subject == "" || identity.IssuedAt.IsZero() || identity.IssuedAt.After(time.Now().Add(time.Minute)) || !time.Now().Before(identity.Expiry) {
		return "", ErrLoginFailed
	}
	var claims struct {
		AuthorizedParty string `json:"azp"`
	}
	if err := identity.Claims(&claims); err != nil || (len(identity.Audience) > 1 || claims.AuthorizedParty != "") && claims.AuthorizedParty != manager.config.ClientID {
		return "", ErrLoginFailed
	}
	if identity.AccessTokenHash != "" && identity.VerifyAccessToken(token.AccessToken) != nil {
		return "", ErrLoginFailed
	}
	if item.binding != "" {
		currentUser, err := manager.auth.Authenticate(ctx, item.binding)
		if err != nil || currentUser != item.user {
			return "", ErrLoginFailed
		}
	}
	resultID := rand.Text()
	manager.mutex.Lock()
	manager.prune()
	if manager.closed || len(manager.results) >= 128 {
		manager.mutex.Unlock()
		return "", ErrBusy
	}
	manager.results[resultID] = loginResult{expires: time.Now().Add(time.Minute)}
	manager.mutex.Unlock()
	local, user, err := manager.auth.LoginOIDC(ctx, identity.Issuer, identity.Subject, item.user, item.binding)
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if err != nil || manager.closed || ctx.Err() != nil {
		delete(manager.results, resultID)
		return "", ErrLoginFailed
	}
	manager.results[resultID] = loginResult{token: local, user: user, expires: time.Now().Add(time.Minute), ready: true}
	return resultID, nil
}

func (manager *Manager) Redeem(ctx context.Context, proof string) (auth.SessionToken, string, error) {
	ctx, done, err := manager.enter(ctx)
	if err != nil {
		return auth.SessionToken{}, "", err
	}
	defer done()
	manager.mutex.Lock()
	manager.prune()
	result, found := manager.results[proof]
	if found && result.ready {
		delete(manager.results, proof)
	}
	manager.mutex.Unlock()
	if !found || !result.ready {
		return auth.SessionToken{}, "", ErrLoginFailed
	}
	if _, err := manager.auth.Authenticate(ctx, result.token.Reveal()); err != nil {
		return auth.SessionToken{}, "", ErrLoginFailed
	}
	return result.token, result.user, nil
}

func (manager *Manager) Close() {
	manager.mutex.Lock()
	manager.closed = true
	for cancel := range maps.Values(manager.calls) {
		cancel()
	}
	manager.mutex.Unlock()
	manager.network.Close()
	manager.workers.Wait()
	manager.mutex.Lock()
	clear(manager.pending)
	clear(manager.results)
	manager.mutex.Unlock()
}
