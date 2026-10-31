package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/ca-x/cognate/internal/server/state"
)

func (service *Service) BootstrapAdministrator(ctx context.Context, username, passwordFile string) (string, error) {
	normalized, err := normalizeUsername(username)
	if err != nil {
		return "", ErrInvalidInput
	}
	userKey, err := service.store.BootstrapPasswordAdministrator(ctx, normalized, nil)
	if !errors.Is(err, state.ErrBootstrapRequired) {
		return userKey, err
	}
	password, err := state.ReadPrivateFile(passwordFile, 1024)
	defer clear(password)
	if err != nil || !validPassword(password) {
		return "", fmt.Errorf("initial administrator password must be a private file containing 8 to 1024 bytes")
	}
	if err := service.acquire(ctx); err != nil {
		return "", err
	}
	defer service.release()
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}
	defer clear(hash)
	return service.store.BootstrapPasswordAdministrator(ctx, normalized, hash)
}
