package auth

import "testing"

func TestOIDCUnlinkOwnerPasswordAndSessionGuards(test *testing.T) {
	service, _ := testService(test)
	password := []byte("synthetic-unlink-password")
	owner, err := service.Register(test.Context(), "owner", password)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := service.Register(test.Context(), "other", password); err != nil {
		test.Fatal(err)
	}
	current, err := service.Login(test.Context(), "owner", password)
	if err != nil {
		test.Fatal(err)
	}
	other, err := service.Login(test.Context(), "other", password)
	if err != nil {
		test.Fatal(err)
	}
	linked, actual, err := service.LoginOIDC(test.Context(), "https://issuer.example", "subject", owner, current.Reveal())
	if err != nil || actual != owner {
		test.Fatal("binding failed")
	}
	bindings, canUnlink, err := service.OIDCBindings(test.Context(), current.Reveal())
	if err != nil || !canUnlink || len(bindings) != 1 {
		test.Fatal("binding list failed")
	}
	if err := service.UnlinkOIDC(test.Context(), other.Reveal(), bindings[0].ID, password); err == nil {
		test.Fatal("cross-owner unlink accepted")
	}
	if err := service.UnlinkOIDC(test.Context(), current.Reveal(), bindings[0].ID, []byte("wrong-password")); err == nil {
		test.Fatal("incorrect password accepted")
	}
	if err := service.UnlinkOIDC(test.Context(), current.Reveal(), bindings[0].ID, password); err != nil {
		test.Fatal("unlink failed")
	}
	if _, err := service.Authenticate(test.Context(), linked.Reveal()); err == nil {
		test.Fatal("old OIDC session survived unlink")
	}
	if actual, err := service.Authenticate(test.Context(), current.Reveal()); err != nil || actual != owner {
		test.Fatal("current session lost")
	}
	if _, err := service.Authenticate(test.Context(), other.Reveal()); err != nil {
		test.Fatal("other owner session revoked")
	}
	if _, err := service.Login(test.Context(), "owner", password); err != nil {
		test.Fatal("password login lost")
	}
	sole, _, err := service.LoginOIDC(test.Context(), "https://issuer.example", "oidc-only", "", "")
	if err != nil {
		test.Fatal("OIDC-only login failed")
	}
	bindings, canUnlink, err = service.OIDCBindings(test.Context(), sole.Reveal())
	if err != nil || canUnlink || len(bindings) != 1 {
		test.Fatal("OIDC-only lockout guard missing")
	}
	if err := service.UnlinkOIDC(test.Context(), sole.Reveal(), bindings[0].ID, password); err == nil {
		test.Fatal("only login method removed")
	}
}
