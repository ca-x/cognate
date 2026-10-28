package github

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRefreshRotatesCredentialsAndDisallowsReuse(t *testing.T) {
	oldAccess, oldRefresh, newAccess, newRefresh := rand.Text(), rand.Text(), rand.Text(), rand.Text()
	var requests int
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.String() != accessTokenURL || request.Method != http.MethodPost {
			t.Fatal("unexpected refresh endpoint")
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("client_id") != "client-id" || request.Form.Get("grant_type") != "refresh_token" || request.Form.Get("refresh_token") != oldRefresh || request.Form.Has("client_secret") {
			t.Fatal("incorrect device flow refresh grant")
		}
		return jsonResponse(http.StatusOK, map[string]any{"access_token": newAccess, "refresh_token": newRefresh, "token_type": "bearer", "scope": "read:user", "expires_in": 120, "refresh_token_expires_in": 600}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	flow.now = func() time.Time { return now }
	old, err := NewToken(oldAccess, []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	old.clientID, old.refreshGrant, old.refreshExpiresAt = "client-id", &refreshGrantState{value: oldRefresh}, now.Add(time.Hour)
	listClient, err := NewListsClient(old, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("used an access token after refresh")
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := flow.Refresh(t.Context(), old)
	if err != nil || updated.Reveal() != newAccess {
		t.Fatalf("refresh failed: %v", err)
	}
	refresh, exists := updated.RefreshToken()
	if !exists || refresh.Reveal() != newRefresh || len(updated.Scopes()) != 1 || updated.Scopes()[0] != "read:user" {
		t.Fatal("rotated credential or scopes missing")
	}
	if _, err := flow.Refresh(t.Context(), old); !errors.Is(err, ErrRefreshOutcomeUnknown) || requests != 1 {
		t.Fatalf("old refresh reused: requests=%d error=%v", requests, err)
	}
	if _, err := listClient.Lists(t.Context()); !errors.Is(err, ErrTokenInvalidated) {
		t.Fatalf("old list client accepted rotated token: %v", err)
	}
}

func TestRefreshTreatsLostResponseAsIrreversible(t *testing.T) {
	var requests int
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New(rand.Text())
	})})
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	token.clientID, token.refreshGrant = "client-id", &refreshGrantState{value: rand.Text()}
	if _, err := flow.Refresh(t.Context(), token); !errors.Is(err, ErrRefreshOutcomeUnknown) {
		t.Fatalf("expected uncertain refresh, got %v", err)
	}
	if _, err := flow.Refresh(t.Context(), token); !errors.Is(err, ErrRefreshOutcomeUnknown) || requests != 1 {
		t.Fatalf("retried uncertain refresh: requests=%d error=%v", requests, err)
	}
}

func TestRefreshRejectsExpiredOrWrongClientWithoutSending(t *testing.T) {
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("sent an invalid refresh grant")
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	token.clientID, token.refreshGrant = "other-client", &refreshGrantState{value: rand.Text()}
	if _, err := flow.Refresh(t.Context(), token); err == nil {
		t.Fatal("accepted different client")
	}
	token.clientID = "client-id"
	token.refreshExpiresAt = time.Now().Add(-time.Second)
	if _, err := flow.Refresh(t.Context(), token); !errors.Is(err, ErrRefreshReauthorizationRequired) {
		t.Fatalf("expected reauthorization, got %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := flow.Refresh(ctx, token); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestRefreshSerializesCopiesAndRejectsUncertainResponses(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		close(started)
		<-finish
		return jsonResponse(http.StatusOK, map[string]any{"access_token": rand.Text(), "token_type": "bearer"}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	token.clientID, token.refreshGrant = "client-id", &refreshGrantState{value: rand.Text()}
	result := make(chan error, 1)
	go func() {
		_, err := flow.Refresh(t.Context(), token)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh request did not start")
	}
	if _, err := flow.Refresh(t.Context(), token); !errors.Is(err, ErrRefreshOutcomeUnknown) {
		t.Fatalf("concurrent refresh error = %v", err)
	}
	close(finish)
	if err := <-result; !errors.Is(err, ErrRefreshOutcomeUnknown) {
		t.Fatalf("incomplete rotation response error = %v", err)
	}
}

func TestRefreshWaitsForInFlightRequestsAndAllowsCanceledWait(t *testing.T) {
	requestStarted := make(chan struct{})
	finishRequest := make(chan struct{})
	refreshStarted := make(chan struct{})
	oldAccess, oldRefresh := rand.Text(), rand.Text()
	token, err := NewToken(oldAccess, nil)
	if err != nil {
		t.Fatal(err)
	}
	token.clientID, token.refreshGrant = "client-id", &refreshGrantState{value: oldRefresh}
	listClient, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		close(requestStarted)
		<-finishRequest
		return jsonResponse(http.StatusServiceUnavailable, nil), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		close(refreshStarted)
		return jsonResponse(http.StatusOK, map[string]any{"access_token": rand.Text(), "refresh_token": rand.Text(), "token_type": "bearer", "expires_in": 100, "refresh_token_expires_in": 1000}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	listDone := make(chan error, 1)
	go func() { _, err := listClient.Lists(t.Context()); listDone <- err }()
	<-requestStarted
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := flow.Refresh(ctx, token); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting refresh cancellation = %v", err)
	}
	if _, ok := token.RefreshToken(); !ok {
		t.Fatal("canceled wait consumed the refresh grant")
	}
	refreshDone := make(chan error, 1)
	go func() { _, err := flow.Refresh(t.Context(), token); refreshDone <- err }()
	select {
	case <-refreshStarted:
		t.Fatal("refresh overlapped an in-flight request")
	case <-time.After(20 * time.Millisecond):
	}
	close(finishRequest)
	if err := <-listDone; err == nil {
		t.Fatal("expected list transport failure")
	}
	if err := <-refreshDone; err != nil {
		t.Fatalf("refresh after request: %v", err)
	}
}

func TestRefreshRespectsExplicitEmptyScope(t *testing.T) {
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"access_token": rand.Text(), "refresh_token": rand.Text(), "token_type": "bearer", "scope": "", "expires_in": 100, "refresh_token_expires_in": 1000}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewToken(rand.Text(), []string{"repo"})
	if err != nil {
		t.Fatal(err)
	}
	token.clientID, token.refreshGrant = "client-id", &refreshGrantState{value: rand.Text()}
	updated, err := flow.Refresh(t.Context(), token)
	if err != nil || len(updated.Scopes()) != 0 {
		t.Fatalf("explicit empty scope = %v, error = %v", updated.Scopes(), err)
	}
}

func TestRefreshRejectsNullScopeWithoutReusingGrant(t *testing.T) {
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"access_token": rand.Text(), "refresh_token": rand.Text(), "token_type": "bearer", "scope": nil, "expires_in": 100, "refresh_token_expires_in": 1000}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewToken(rand.Text(), []string{"repo"})
	if err != nil {
		t.Fatal(err)
	}
	token.clientID, token.refreshGrant = "client-id", &refreshGrantState{value: rand.Text()}
	if _, err := flow.Refresh(t.Context(), token); !errors.Is(err, ErrRefreshOutcomeUnknown) {
		t.Fatalf("null scope accepted: %v", err)
	}
	if _, ok := token.RefreshToken(); ok {
		t.Fatal("reused refresh grant after uncertain rotation")
	}
}

func TestRefreshGateWaitStopsAtRefreshExpiry(t *testing.T) {
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	token.clientID, token.refreshGrant = "client-id", &refreshGrantState{value: rand.Text()}
	token.refreshExpiresAt = time.Now().Add(50 * time.Millisecond)
	if err := token.validity.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer token.validity.unlock()
	flow, err := NewDeviceFlow("client-id", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("sent expired refresh grant")
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flow.Refresh(t.Context(), token); !errors.Is(err, ErrRefreshReauthorizationRequired) {
		t.Fatalf("expired gate wait = %v", err)
	}
	if _, ok := token.RefreshToken(); !ok {
		t.Fatal("waiting without sending consumed the grant")
	}
}
