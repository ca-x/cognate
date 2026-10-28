package github

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	deviceCodeURL  = "https://github.com/login/device/code"
	accessTokenURL = "https://github.com/login/oauth/access_token"
	maxOAuthBody   = 64 << 10
)

var (
	ErrDeviceExpired                  = errors.New("GitHub device authorization expired")
	ErrDeviceDenied                   = errors.New("GitHub device authorization denied")
	ErrTokenExpired                   = errors.New("GitHub access token expired")
	ErrTokenInvalidated               = errors.New("GitHub access token may have been invalidated by refresh")
	ErrSnapshotStale                  = errors.New("GitHub list snapshot was superseded by another synchronization")
	ErrRefreshOutcomeUnknown          = errors.New("GitHub refresh outcome is unknown; do not retry the old credential")
	ErrRefreshReauthorizationRequired = errors.New("GitHub authorization must be renewed")
	errOAuthTimeout                   = errors.New("GitHub authorization request timed out")
)

type DeviceFlow struct {
	clientID string
	client   *http.Client
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

type DeviceAuthorization struct {
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
}

type DeviceSession struct {
	state *deviceSessionState
}

type deviceSessionState struct {
	flow      *DeviceFlow
	code      string
	expiresAt time.Time
	interval  time.Duration
	mutex     sync.Mutex
	waiting   bool
	completed bool
}

type Token struct {
	value            string
	scopes           []string
	expiresAt        time.Time
	clientID         string
	validity         *tokenValidity
	refreshGrant     *refreshGrantState
	refreshExpiresAt time.Time
}

type tokenValidity struct {
	invalidated atomic.Bool
	gate        chan struct{}
	generation  uint64
}

func newTokenValidity() *tokenValidity {
	validity := &tokenValidity{gate: make(chan struct{}, 1)}
	validity.gate <- struct{}{}
	return validity
}

func (validity *tokenValidity) lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-validity.gate:
		return nil
	}
}

func (validity *tokenValidity) unlock() { validity.gate <- struct{}{} }

func (validity *tokenValidity) lockUntil(ctx context.Context, expiresAt time.Time, now func() time.Time, expired error) error {
	lockContext := ctx
	if !expiresAt.IsZero() {
		remaining := expiresAt.Sub(now())
		if remaining <= 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return expired
		}
		var cancel context.CancelFunc
		lockContext, cancel = context.WithTimeout(ctx, remaining)
		defer cancel()
	}
	if err := validity.lock(lockContext); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !expiresAt.IsZero() && errors.Is(lockContext.Err(), context.DeadlineExceeded) {
			return expired
		}
		return err
	}
	if ctx.Err() != nil {
		validity.unlock()
		return ctx.Err()
	}
	if !expiresAt.IsZero() && (lockContext.Err() != nil || !now().Before(expiresAt)) {
		validity.unlock()
		return expired
	}
	return nil
}

type refreshGrantState struct {
	mutex   sync.Mutex
	value   string
	started bool
}

func NewToken(value string, scopes []string) (Token, error) {
	if len(value) == 0 || len(value) > 4096 {
		return Token{}, fmt.Errorf("invalid GitHub access token")
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return Token{}, fmt.Errorf("invalid GitHub access token")
		}
	}
	return Token{value: value, scopes: append([]string(nil), scopes...), validity: newTokenValidity()}, nil
}

func (token Token) Reveal() string { return token.value }

func (token Token) Scopes() []string { return append([]string(nil), token.scopes...) }

func (token Token) ClientID() string { return token.clientID }

func (token Token) AccessOnly() Token {
	token.refreshGrant = nil
	token.refreshExpiresAt = time.Time{}
	return token
}

func (token Token) ExpiresAt() (time.Time, bool) { return token.expiresAt, !token.expiresAt.IsZero() }

func (token Token) RefreshExpiresAt() (time.Time, bool) {
	return token.refreshExpiresAt, !token.refreshExpiresAt.IsZero()
}

func (token Token) RefreshToken() (Token, bool) {
	if token.refreshGrant == nil {
		return Token{}, false
	}
	token.refreshGrant.mutex.Lock()
	defer token.refreshGrant.mutex.Unlock()
	if token.refreshGrant.started || token.refreshGrant.value == "" {
		return Token{}, false
	}
	return Token{value: token.refreshGrant.value, validity: newTokenValidity()}, true
}

func (Token) String() string { return "[redacted token]" }

func (Token) GoString() string { return "[redacted token]" }

func (DeviceSession) String() string { return "[redacted device session]" }

func (DeviceSession) GoString() string { return "[redacted device session]" }

func NewDeviceFlow(clientID string, client *http.Client) (*DeviceFlow, error) {
	if clientID == "" || len(clientID) > 256 || strings.TrimSpace(clientID) != clientID || strings.ContainsAny(clientID, "\r\n\x00") {
		return nil, fmt.Errorf("invalid GitHub OAuth client ID")
	}
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copy.Timeout == 0 {
		copy.Timeout = 30 * time.Second
	}
	return &DeviceFlow{clientID: clientID, client: &copy, now: time.Now, sleep: sleepContext}, nil
}

type deviceResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int64  `json:"expires_in"`
	Interval        int64  `json:"interval"`
}

func (flow *DeviceFlow) Start(ctx context.Context, requestUserScope bool) (DeviceAuthorization, *DeviceSession, error) {
	if err := ctx.Err(); err != nil {
		return DeviceAuthorization{}, nil, err
	}
	form := url.Values{"client_id": {flow.clientID}}
	if requestUserScope {
		form.Set("scope", "user")
	}
	started := flow.now()
	var response deviceResponse
	if err := flow.post(ctx, deviceCodeURL, form, &response); err != nil {
		return DeviceAuthorization{}, nil, err
	}
	if response.DeviceCode == "" || len(response.DeviceCode) > 4096 || !validUserCode(response.UserCode) || response.VerificationURI != "https://github.com/login/device" || response.ExpiresIn <= 0 || response.ExpiresIn > 86400 || response.Interval < 0 || response.Interval > 86400 {
		return DeviceAuthorization{}, nil, fmt.Errorf("invalid GitHub device authorization response")
	}
	if response.Interval == 0 {
		response.Interval = 5
	}
	expires := started.Add(time.Duration(response.ExpiresIn) * time.Second)
	if !flow.now().Before(expires) {
		return DeviceAuthorization{}, nil, ErrDeviceExpired
	}
	return DeviceAuthorization{UserCode: response.UserCode, VerificationURI: response.VerificationURI, ExpiresAt: expires}, &DeviceSession{state: &deviceSessionState{flow: flow, code: response.DeviceCode, expiresAt: expires, interval: time.Duration(response.Interval) * time.Second}}, nil
}

func validUserCode(code string) bool {
	if len(code) == 0 || len(code) > 64 {
		return false
	}
	for _, character := range code {
		if character != '-' && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

type tokenResponse struct {
	AccessToken      string     `json:"access_token"`
	TokenType        string     `json:"token_type"`
	Scope            oauthScope `json:"scope"`
	Error            string     `json:"error"`
	Interval         *int64     `json:"interval"`
	ExpiresIn        *int64     `json:"expires_in"`
	RefreshToken     string     `json:"refresh_token"`
	RefreshExpiresIn *int64     `json:"refresh_token_expires_in"`
}

type oauthScope struct {
	present bool
	null    bool
	value   string
}

func (scope *oauthScope) UnmarshalJSON(data []byte) error {
	scope.present = true
	if string(data) == "null" {
		scope.null = true
		return nil
	}
	return json.Unmarshal(data, &scope.value)
}

func (session *DeviceSession) Wait(ctx context.Context) (Token, error) {
	if err := ctx.Err(); err != nil {
		return Token{}, err
	}
	if session == nil || session.state == nil {
		return Token{}, fmt.Errorf("device authorization is unavailable")
	}
	state := session.state
	state.mutex.Lock()
	if state.waiting || state.completed {
		state.mutex.Unlock()
		return Token{}, fmt.Errorf("device authorization is already in use or complete")
	}
	state.waiting = true
	state.mutex.Unlock()
	defer func() {
		state.mutex.Lock()
		state.waiting = false
		state.mutex.Unlock()
	}()
	for {
		remaining := state.expiresAt.Sub(state.flow.now())
		if remaining <= 0 {
			state.complete()
			return Token{}, ErrDeviceExpired
		}
		if err := state.flow.sleep(ctx, min(state.interval, remaining)); err != nil {
			return Token{}, err
		}
		remaining = state.expiresAt.Sub(state.flow.now())
		if remaining <= 0 {
			state.complete()
			return Token{}, ErrDeviceExpired
		}
		form := url.Values{"client_id": {state.flow.clientID}, "device_code": {state.code}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}
		var response tokenResponse
		requestStarted := state.flow.now()
		pollContext, cancel := context.WithTimeout(ctx, remaining)
		err := state.flow.post(pollContext, accessTokenURL, form, &response)
		pollErr := pollContext.Err()
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return Token{}, ctx.Err()
			}
			if errors.Is(pollErr, context.DeadlineExceeded) {
				state.complete()
				return Token{}, ErrDeviceExpired
			}
			if errors.Is(err, errOAuthTimeout) {
				state.interval = min(state.interval*2, 24*time.Hour)
			}
			return Token{}, err
		}
		switch response.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			state.interval += 5 * time.Second
			if response.Interval != nil {
				if *response.Interval <= 0 || *response.Interval > 86400 {
					state.complete()
					return Token{}, fmt.Errorf("invalid GitHub polling interval")
				}
				state.interval = max(state.interval, time.Duration(*response.Interval)*time.Second)
			}
			continue
		case "expired_token":
			state.complete()
			return Token{}, ErrDeviceExpired
		case "access_denied":
			state.complete()
			return Token{}, ErrDeviceDenied
		case "":
		default:
			state.complete()
			return Token{}, fmt.Errorf("GitHub device authorization failed")
		}
		if !strings.EqualFold(response.TokenType, "bearer") {
			state.complete()
			return Token{}, fmt.Errorf("invalid GitHub access token response")
		}
		var scopes []string
		if response.Scope.null {
			state.complete()
			return Token{}, fmt.Errorf("invalid GitHub authorization scope")
		}
		if response.Scope.present {
			scopes = parseScopes(response.Scope.value)
		}
		token, err := tokenFromResponse(response, requestStarted, state.flow.clientID, scopes)
		if err != nil {
			state.complete()
			return Token{}, err
		}
		state.complete()
		return token, nil
	}
}

func parseScopes(value string) []string {
	var scopes []string
	for scope := range strings.SplitSeq(value, ",") {
		if scope = strings.TrimSpace(scope); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

func tokenFromResponse(response tokenResponse, started time.Time, clientID string, scopes []string) (Token, error) {
	token, err := NewToken(response.AccessToken, scopes)
	if err != nil || !strings.EqualFold(response.TokenType, "bearer") {
		return Token{}, fmt.Errorf("invalid GitHub access token response")
	}
	token.clientID = clientID
	if response.ExpiresIn != nil {
		if *response.ExpiresIn <= 0 || *response.ExpiresIn > 316224000 {
			return Token{}, fmt.Errorf("invalid GitHub access token expiration")
		}
		token.expiresAt = started.Add(time.Duration(*response.ExpiresIn) * time.Second)
	}
	if response.RefreshToken != "" {
		if _, err := NewToken(response.RefreshToken, nil); err != nil {
			return Token{}, fmt.Errorf("invalid GitHub refresh credential")
		}
		token.refreshGrant = &refreshGrantState{value: response.RefreshToken}
	}
	if response.RefreshExpiresIn != nil {
		if token.refreshGrant == nil || *response.RefreshExpiresIn <= 0 || *response.RefreshExpiresIn > 316224000 {
			return Token{}, fmt.Errorf("invalid GitHub refresh credential expiration")
		}
		token.refreshExpiresAt = started.Add(time.Duration(*response.RefreshExpiresIn) * time.Second)
	}
	return token, nil
}

func (state *deviceSessionState) complete() {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.completed = true
	state.code = ""
}

func (flow *DeviceFlow) post(ctx context.Context, endpoint string, form url.Values, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("create GitHub authorization request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "Cognate")
	response, err := flow.client.Do(request)
	if err != nil {
		return oauthTransportError(ctx, err, "GitHub authorization request failed")
	}
	defer response.Body.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub authorization returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthBody+1))
	if err != nil {
		return oauthTransportError(ctx, err, "read GitHub authorization response")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(body) > maxOAuthBody || json.Unmarshal(body, destination) != nil {
		return fmt.Errorf("invalid GitHub authorization response")
	}
	return nil
}

func oauthTransportError(ctx context.Context, err error, message string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() {
		return errOAuthTimeout
	}
	return errors.New(message)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
