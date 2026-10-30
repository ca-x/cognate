package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/ca-x/cognate/internal/server/state"
)

func TestImportAuthenticationCurrentSession(test *testing.T) {
	service, _ := testService(test)
	password := []byte("synthetic-import-password")
	owner, err := service.Register(test.Context(), "import-user", password)
	if err != nil {
		test.Fatal(err)
	}
	token, err := service.Login(test.Context(), "import-user", password)
	if err != nil {
		test.Fatal(err)
	}
	credential, err := service.AuthenticateImport(test.Context(), token.Reveal())
	if err != nil || credential.Owner() != owner {
		test.Fatal("valid import owner not authenticated")
	}
	for _, bearer := range []string{"", "shp1.synthetic", "shm1.synthetic", "cn1.synthetic", token.Reveal() + "x"} {
		if _, err := service.AuthenticateImport(test.Context(), bearer); !errors.Is(err, ErrInvalidSession) {
			test.Fatal("invalid import bearer accepted")
		}
	}
	if err := service.Logout(test.Context(), token.Reveal()); err != nil {
		test.Fatal(err)
	}
	if _, err := service.AuthenticateImport(test.Context(), token.Reveal()); !errors.Is(err, state.ErrImportUnauthorized) {
		test.Fatal("revoked import bearer accepted")
	}
	digest := sha256.Sum256([]byte("synthetic-test-claim"))
	claims := state.ImportTicketClaims{SourceProfile: state.ImportSourceCCSwitch24, SourceDigest: digest, ProjectionDigest: digest, AdapterDigest: digest, ResolutionDigest: digest, OptionsDigest: digest}
	if _, err := service.store.SealImportTicket(test.Context(), credential, "https://cognate.invalid", claims); !errors.Is(err, state.ErrImportUnauthorized) {
		test.Fatal("cached credential survived revocation")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := service.AuthenticateImport(ctx, token.Reveal()); !errors.Is(err, context.Canceled) {
		test.Fatal("import auth ignored cancellation")
	}
}
