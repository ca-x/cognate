package auth

import (
	"context"

	"github.com/ca-x/cognate/internal/server/state"
)

func (service *Service) OIDCBindings(ctx context.Context, value string) ([]state.OIDCBinding, bool, error) {
	owner, err := service.Authenticate(ctx, value)
	if err != nil {
		return nil, false, err
	}
	bindings, err := service.store.OIDCBindings(ctx, owner)
	if err != nil {
		return nil, false, err
	}
	account, localErr := service.store.LocalAccountForUser(ctx, owner)
	clear(account.PasswordHash)
	return bindings, localErr == nil, nil
}

func (service *Service) UnlinkOIDC(ctx context.Context, value string, identityID int, password []byte) error {
	owner, err := service.Authenticate(ctx, value)
	if err != nil {
		return err
	}
	if !validPassword(password) || identityID <= 0 {
		return ErrInvalidCredentials
	}
	if err := service.acquire(ctx); err != nil {
		return err
	}
	defer service.release()
	account, err := service.store.LocalAccountForUser(ctx, owner)
	if err != nil {
		return ErrInvalidCredentials
	}
	defer clear(account.PasswordHash)
	if !verifyPassword(password, account.PasswordHash) {
		return ErrInvalidCredentials
	}
	digest, err := service.sessionDigest(value)
	if err != nil {
		return ErrInvalidSession
	}
	return service.store.UnlinkOIDC(ctx, owner, digest[:], account.PasswordHash, identityID)
}
