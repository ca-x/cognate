package github

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"time"
)

type storedToken struct {
	Version          int       `json:"version"`
	Access           string    `json:"access"`
	Refresh          string    `json:"refresh,omitempty"`
	ClientID         string    `json:"client_id,omitempty"`
	Scopes           []string  `json:"scopes,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitzero"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitzero"`
}

func (token Token) WithStorageMaterial(ctx context.Context, consume func([]byte) error) error {
	if token.validity == nil || consume == nil {
		return fmt.Errorf("GitHub token storage is unavailable")
	}
	if err := token.validity.lock(ctx); err != nil {
		return err
	}
	defer token.validity.unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if token.validity.invalidated.Load() {
		return ErrTokenInvalidated
	}
	material := storedToken{Version: 1, Access: token.value, ClientID: token.clientID, Scopes: token.scopes, ExpiresAt: token.expiresAt, RefreshExpiresAt: token.refreshExpiresAt}
	if token.refreshGrant != nil {
		token.refreshGrant.mutex.Lock()
		if token.refreshGrant.started || token.refreshGrant.value == "" {
			token.refreshGrant.mutex.Unlock()
			return ErrRefreshOutcomeUnknown
		}
		material.Refresh = token.refreshGrant.value
		token.refreshGrant.mutex.Unlock()
	}
	encoded, err := json.Marshal(material)
	if err != nil || len(encoded) > 64<<10 {
		return fmt.Errorf("encode GitHub credential for storage")
	}
	defer clear(encoded)
	return consume(encoded)
}

func DecodeStoredToken(data []byte) (Token, error) {
	if len(data) == 0 || len(data) > 64<<10 {
		return Token{}, fmt.Errorf("invalid stored GitHub credential")
	}
	var material storedToken
	if err := json.Unmarshal(data, &material, json.RejectUnknownMembers(true)); err != nil || material.Version != 1 {
		return Token{}, fmt.Errorf("invalid stored GitHub credential")
	}
	token, err := NewToken(material.Access, material.Scopes)
	if err != nil || material.Refresh == "" && !material.RefreshExpiresAt.IsZero() {
		return Token{}, fmt.Errorf("invalid stored GitHub credential")
	}
	if material.Refresh != "" {
		if _, err := NewToken(material.Refresh, nil); err != nil {
			return Token{}, fmt.Errorf("invalid stored GitHub credential")
		}
		if _, err := NewDeviceFlow(material.ClientID, nil); err != nil {
			return Token{}, fmt.Errorf("invalid stored GitHub credential")
		}
		token.refreshGrant = &refreshGrantState{value: material.Refresh}
	}
	token.clientID = material.ClientID
	token.expiresAt = material.ExpiresAt
	token.refreshExpiresAt = material.RefreshExpiresAt
	return token, nil
}
