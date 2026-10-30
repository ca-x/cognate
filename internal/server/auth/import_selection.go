package auth

import (
	"context"

	"github.com/ca-x/cognate/internal/server/state"
)

func (service *Service) AuthenticateImport(ctx context.Context, bearer string) (state.ImportOwnerCredential, error) {
	if err := ctx.Err(); err != nil {
		return state.ImportOwnerCredential{}, err
	}
	if service == nil {
		return state.ImportOwnerCredential{}, ErrInvalidSession
	}
	digest, err := service.sessionDigest(bearer)
	if err != nil {
		return state.ImportOwnerCredential{}, ErrInvalidSession
	}
	return service.store.ImportCredentialForSession(ctx, digest)
}
