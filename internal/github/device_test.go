package github

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type timeoutError struct{ secret string }

func (failure timeoutError) Error() string { return failure.secret }
func (timeoutError) Timeout() bool         { return true }
func (timeoutError) Temporary() bool       { return true }

type timeoutBody struct{}

func (timeoutBody) Read([]byte) (int, error) { return 0, timeoutError{secret: rand.Text()} }
func (timeoutBody) Close() error             { return nil }

type cancelingBody struct {
	reader *strings.Reader
	cancel context.CancelFunc
}

func (body *cancelingBody) Read(data []byte) (int, error) {
	count, err := body.reader.Read(data)
	if errors.Is(err, io.EOF) {
		body.cancel()
	}
	return count, err
}

func (*cancelingBody) Close() error { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonResponse(status int, value any) *http.Response {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(encoded)))}
}

func TestDeviceFlowPollsWithSlowDownAndRedactsSecrets(t *testing.T) {
	deviceSecret, accessSecret := rand.Text(), rand.Text()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var waits []time.Duration
	var polls int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.Header.Get("Accept") != "application/json" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("client_id") != "client-id" {
			t.Fatal("missing client id")
		}
		switch request.URL.String() {
		case deviceCodeURL:
			if request.Form.Get("scope") != "user" {
				t.Fatal("explicit user scope request was omitted")
			}
			return jsonResponse(http.StatusOK, map[string]any{"device_code": deviceSecret, "user_code": "ABCD-EFGH", "verification_uri": "https://github.com/login/device", "expires_in": 120, "interval": 2}), nil
		case accessTokenURL:
			if request.Form.Get("device_code") != deviceSecret || request.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Fatal("invalid device grant")
			}
			polls++
			switch polls {
			case 1:
				return jsonResponse(http.StatusOK, map[string]any{"error": "authorization_pending"}), nil
			case 2:
				return jsonResponse(http.StatusOK, map[string]any{"error": "slow_down"}), nil
			default:
				return jsonResponse(http.StatusOK, map[string]any{"access_token": accessSecret, "token_type": "bearer", "scope": "user"}), nil
			}
		default:
			t.Fatalf("unexpected endpoint: %s", request.URL)
			return nil, nil
		}
	})}
	flow, err := NewDeviceFlow("client-id", client)
	if err != nil {
		t.Fatal(err)
	}
	flow.now = func() time.Time { return now }
	flow.sleep = func(ctx context.Context, duration time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		waits = append(waits, duration)
		now = now.Add(duration)
		return nil
	}
	info, session, err := flow.Start(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if info.UserCode != "ABCD-EFGH" || info.VerificationURI != "https://github.com/login/device" || !info.ExpiresAt.Equal(now.Add(120*time.Second)) {
		t.Fatalf("authorization = %+v", info)
	}
	for _, text := range []string{fmt.Sprintf("%v", session), fmt.Sprintf("%+v", session), fmt.Sprintf("%#v", session), fmt.Sprintf("%+v", *session), fmt.Sprintf("%#v", *session)} {
		if strings.Contains(text, deviceSecret) {
			t.Fatal("device secret leaked through formatting")
		}
	}
	token, err := session.Wait(t.Context())
	if err != nil || token.Reveal() != accessSecret {
		t.Fatalf("authorization failed: %v", err)
	}
	if len(waits) != 3 || waits[0] != 2*time.Second || waits[1] != 2*time.Second || waits[2] != 7*time.Second {
		t.Fatalf("poll intervals = %v", waits)
	}
	for _, text := range []string{fmt.Sprintf("%v", token), fmt.Sprintf("%+v", token), fmt.Sprintf("%#v", token)} {
		if strings.Contains(text, accessSecret) {
			t.Fatal("access token leaked through formatting")
		}
	}
	if _, err := session.Wait(t.Context()); err == nil {
		t.Fatal("reused completed session")
	}
}

func TestDeviceFlowRejectsUnsafeVerificationURLAndErrorsDoNotLeak(t *testing.T) {
	secret := rand.Text()
	for _, response := range []map[string]any{
		{"device_code": secret, "user_code": "ABCD", "verification_uri": "https://example.com/login/device", "expires_in": 900, "interval": 5},
		{"device_code": secret, "user_code": "ABCD", "verification_uri": "https://github.com/login/device", "expires_in": -1, "interval": 5},
		{"error": secret, "error_description": secret},
	} {
		flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, response), nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = flow.Start(t.Context(), false)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("unsafe response error: %v", err)
		}
	}
}

func TestDeviceFlowHonorsCancellationAndRejectsRedirect(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusFound, nil)
		response.Header.Set("Location", "https://example.com/collect")
		return response, nil
	})}
	flow, err := NewDeviceFlow("client-id", client)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := flow.Start(t.Context(), false); err == nil {
		t.Fatal("accepted redirect")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := flow.Start(ctx, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled, got %v", err)
	}
}

func TestDeviceFlowCancellationTakesPriorityDuringResponse(t *testing.T) {
	for _, stage := range []string{"status", "body"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if stage == "status" {
					cancel()
					return jsonResponse(http.StatusForbidden, nil), nil
				}
				response := jsonResponse(http.StatusOK, map[string]any{"device_code": rand.Text(), "user_code": "ABCD", "verification_uri": "https://github.com/login/device", "expires_in": 30})
				content, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				response.Body = &cancelingBody{reader: strings.NewReader(string(content)), cancel: cancel}
				return response, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := flow.Start(ctx, false); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled response = %v", err)
			}
		})
	}
}

func TestDeviceFlowUsesConservativeExpiryAndRedactsTransportErrors(t *testing.T) {
	secret := rand.Text()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := now
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == deviceCodeURL {
			now = now.Add(10 * time.Second)
			return jsonResponse(http.StatusOK, map[string]any{"device_code": secret, "user_code": "ABCD", "verification_uri": "https://github.com/login/device", "expires_in": 30, "interval": 1}), nil
		}
		return nil, errors.New(secret)
	})})
	if err != nil {
		t.Fatal(err)
	}
	flow.now = func() time.Time { return now }
	flow.sleep = func(ctx context.Context, duration time.Duration) error { now = now.Add(duration); return ctx.Err() }
	info, session, err := flow.Start(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ExpiresAt.Equal(start.Add(30 * time.Second)) {
		t.Fatalf("expiry = %v, want %v", info.ExpiresAt, start.Add(30*time.Second))
	}
	_, err = session.Wait(t.Context())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("transport error leaked secret: %v", err)
	}
}

func TestDeviceFlowRejectsAlreadyExpiredStartResponse(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		now = now.Add(31 * time.Second)
		return jsonResponse(http.StatusOK, map[string]any{"device_code": rand.Text(), "user_code": "ABCD", "verification_uri": "https://github.com/login/device", "expires_in": 30, "interval": 5}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	flow.now = func() time.Time { return now }
	if _, session, err := flow.Start(t.Context(), false); !errors.Is(err, ErrDeviceExpired) || session != nil {
		t.Fatalf("expired authorization session = %v, error = %v", session, err)
	}
}

func TestDeviceFlowBoundsPollingRequestByExpiry(t *testing.T) {
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})})
	if err != nil {
		t.Fatal(err)
	}
	session := &DeviceSession{state: &deviceSessionState{flow: flow, code: rand.Text(), expiresAt: time.Now().Add(50 * time.Millisecond), interval: time.Millisecond}}
	_, err = session.Wait(t.Context())
	if !errors.Is(err, ErrDeviceExpired) {
		t.Fatalf("expected expired, got %v", err)
	}
}

func TestDeviceFlowHandlesExpiringTokenAndRefreshSecret(t *testing.T) {
	access, refresh := rand.Text(), rand.Text()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"access_token": access, "token_type": "bearer", "expires_in": 120, "refresh_token": refresh, "refresh_token_expires_in": 600}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	flow.now = func() time.Time { return now }
	flow.sleep = func(ctx context.Context, duration time.Duration) error { now = now.Add(duration); return ctx.Err() }
	session := &DeviceSession{state: &deviceSessionState{flow: flow, code: rand.Text(), expiresAt: now.Add(time.Minute), interval: time.Second}}
	token, err := session.Wait(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	expires, ok := token.ExpiresAt()
	if !ok || !expires.Equal(now.Add(120*time.Second)) {
		t.Fatalf("access expiration = %v, present = %t", expires, ok)
	}
	refreshToken, ok := token.RefreshToken()
	if !ok || refreshToken.Reveal() != refresh {
		t.Fatal("refresh credential was discarded")
	}
	refreshExpiry, ok := token.RefreshExpiresAt()
	if !ok || !refreshExpiry.Equal(now.Add(600*time.Second)) {
		t.Fatalf("refresh expiration = %v, present = %t", refreshExpiry, ok)
	}
	for _, value := range []any{token, refreshToken} {
		for _, text := range []string{fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(text, access) || strings.Contains(text, refresh) {
				t.Fatal("credential leaked through formatting")
			}
		}
	}
}

func TestDeviceFlowRespectsTimeoutAndServerInterval(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var waits []time.Duration
	var calls int
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			return nil, timeoutError{secret: rand.Text()}
		case 2:
			return jsonResponse(http.StatusOK, map[string]any{"error": "slow_down", "interval": 30}), nil
		default:
			return jsonResponse(http.StatusOK, map[string]any{"error": "access_denied"}), nil
		}
	})})
	if err != nil {
		t.Fatal(err)
	}
	flow.now = func() time.Time { return now }
	flow.sleep = func(ctx context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		now = now.Add(duration)
		return ctx.Err()
	}
	session := &DeviceSession{state: &deviceSessionState{flow: flow, code: rand.Text(), expiresAt: now.Add(3 * time.Minute), interval: 2 * time.Second}}
	if _, err := session.Wait(t.Context()); err == nil {
		t.Fatal("expected request timeout")
	}
	if _, err := session.Wait(t.Context()); !errors.Is(err, ErrDeviceDenied) {
		t.Fatalf("expected denial, got %v", err)
	}
	if len(waits) != 3 || waits[0] != 2*time.Second || waits[1] != 4*time.Second || waits[2] != 30*time.Second {
		t.Fatalf("poll waits = %v", waits)
	}
}

func TestDeviceFlowBacksOffOnResponseReadTimeout(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: timeoutBody{}}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	flow.now = func() time.Time { return now }
	flow.sleep = func(ctx context.Context, duration time.Duration) error { now = now.Add(duration); return ctx.Err() }
	session := &DeviceSession{state: &deviceSessionState{flow: flow, code: rand.Text(), expiresAt: now.Add(time.Minute), interval: 2 * time.Second}}
	if _, err := session.Wait(t.Context()); !errors.Is(err, errOAuthTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
	if session.state.interval != 4*time.Second {
		t.Fatalf("timeout retry interval = %v", session.state.interval)
	}
}

func TestDeviceFlowStopsAfterTerminalProtocolError(t *testing.T) {
	var calls int
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusOK, map[string]any{"error": "incorrect_device_code"}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	flow.now = func() time.Time { return now }
	flow.sleep = func(ctx context.Context, duration time.Duration) error { now = now.Add(duration); return ctx.Err() }
	session := &DeviceSession{state: &deviceSessionState{flow: flow, code: rand.Text(), expiresAt: now.Add(time.Minute), interval: time.Second}}
	if _, err := session.Wait(t.Context()); err == nil {
		t.Fatal("accepted terminal error")
	}
	if _, err := session.Wait(t.Context()); err == nil || calls != 1 {
		t.Fatalf("terminal session retried: calls=%d error=%v", calls, err)
	}
}

func TestDeviceFlowRejectsConcurrentWaitAndAllowsCancellation(t *testing.T) {
	started := make(chan struct{})
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})})
	if err != nil {
		t.Fatal(err)
	}
	session := &DeviceSession{state: &deviceSessionState{flow: flow, code: rand.Text(), expiresAt: time.Now().Add(time.Minute), interval: time.Millisecond}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := session.Wait(ctx)
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("poll did not start")
	}
	if _, err := session.Wait(t.Context()); err == nil {
		t.Fatal("accepted concurrent poller")
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled poll error = %v", err)
	}
}
