package github

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

func (flow *DeviceFlow) Refresh(ctx context.Context, token Token) (Token, error) {
	if err := ctx.Err(); err != nil {
		return Token{}, err
	}
	if token.clientID == "" || token.clientID != flow.clientID || token.validity == nil || token.refreshGrant == nil {
		return Token{}, fmt.Errorf("GitHub refresh credential is unavailable for this client")
	}
	if !token.refreshExpiresAt.IsZero() && !flow.now().Before(token.refreshExpiresAt) {
		return Token{}, ErrRefreshReauthorizationRequired
	}
	if err := token.validity.lockUntil(ctx, token.refreshExpiresAt, flow.now, ErrRefreshReauthorizationRequired); err != nil {
		return Token{}, err
	}
	if err := ctx.Err(); err != nil {
		token.validity.unlock()
		return Token{}, err
	}
	if !token.refreshExpiresAt.IsZero() && !flow.now().Before(token.refreshExpiresAt) {
		token.validity.unlock()
		return Token{}, ErrRefreshReauthorizationRequired
	}
	grant := token.refreshGrant
	grant.mutex.Lock()
	if grant.started || grant.value == "" {
		grant.mutex.Unlock()
		token.validity.unlock()
		return Token{}, ErrRefreshOutcomeUnknown
	}
	grant.started = true
	credential := grant.value
	grant.value = ""
	token.validity.invalidated.Store(true)
	grant.mutex.Unlock()
	token.validity.unlock()
	form := url.Values{"client_id": {flow.clientID}, "grant_type": {"refresh_token"}, "refresh_token": {credential}}
	started := flow.now()
	var response tokenResponse
	if err := flow.post(ctx, accessTokenURL, form, &response); err != nil {
		return Token{}, errors.Join(ErrRefreshOutcomeUnknown, err)
	}
	if response.Error == "bad_refresh_token" {
		return Token{}, ErrRefreshReauthorizationRequired
	}
	if response.Error != "" || response.Scope.null || response.RefreshToken == "" || response.ExpiresIn == nil || response.RefreshExpiresIn == nil {
		return Token{}, ErrRefreshOutcomeUnknown
	}
	scopes := token.scopes
	if response.Scope.present {
		scopes = parseScopes(response.Scope.value)
	}
	updated, err := tokenFromResponse(response, started, flow.clientID, scopes)
	if err != nil || updated.value == token.value || response.RefreshToken == credential {
		return Token{}, ErrRefreshOutcomeUnknown
	}
	return updated, nil
}
