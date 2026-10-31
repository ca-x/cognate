package state

import "testing"

func TestImportTicketNPXProfileRoundTrip(test *testing.T) {
	fixture := newImportTicketFixture(test)
	for _, profile := range []ImportSourceProfile{1, 2, 3} {
		claims := importTestClaims()
		claims.SourceProfile = profile
		sealed, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", claims)
		if err != nil || len(sealed) != ImportTicketBytes {
			test.Fatal("supported source profile could not be sealed without changing ticket size")
		}
		ticket, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed)
		actual, present := ticket.Claims()
		if err != nil || !present || actual != claims {
			test.Fatal("sealed source profile identity was not preserved")
		}
	}
}
