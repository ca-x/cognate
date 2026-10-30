package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type importTicketFixture struct {
	store      *Store
	credential ImportOwnerCredential
	digest     [32]byte
	now        time.Time
	path       string
	key        []byte
}

func newImportTicketFixture(test *testing.T) *importTicketFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	fixture := &importTicketFixture{path: filepath.Join(directory, "imports.db"), key: bytes.Repeat([]byte{67}, 32), now: time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC), digest: sha256.Sum256([]byte("synthetic-session-digest"))}
	store, err := Open(test.Context(), fixture.path, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	fixture.store = store
	test.Cleanup(func() { _ = fixture.store.Close() })
	store.state.now = func() time.Time { return fixture.now }
	if _, err := store.CreateOIDCSession(test.Context(), OIDCSessionPlan{Issuer: "https://issuer.invalid", Subject: "alice", TokenHash: fixture.digest[:], IssuedAt: fixture.now.Add(-time.Minute), ExpiresAt: fixture.now.Add(time.Hour)}); err != nil {
		test.Fatal(err)
	}
	fixture.credential, err = store.ImportCredentialForSession(test.Context(), fixture.digest)
	if err != nil {
		test.Fatal(err)
	}
	return fixture
}

func importTestClaims() ImportTicketClaims {
	return ImportTicketClaims{SourceProfile: ImportSourceCCSwitch24, SourceDigest: sha256.Sum256([]byte("source")), ProjectionDigest: sha256.Sum256([]byte("projection")), AdapterDigest: sha256.Sum256([]byte("adapter-and-host-rule")), ResolutionDigest: sha256.Sum256([]byte("resolution")), OptionsDigest: sha256.Sum256([]byte("options"))}
}

func TestImportTicketBindingAndBounds(test *testing.T) {
	fixture := newImportTicketFixture(test)
	claims := importTestClaims()
	sealed, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", claims)
	if err != nil || len(sealed) != 290 || len(base64.StdEncoding.EncodeToString(sealed)) != 388 {
		test.Fatal("ticket shape invalid")
	}
	ticket, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed)
	actual, present := ticket.Claims()
	if err != nil || !present || actual != claims || !ticket.ExpiresAt().Equal(fixture.now.Add(5*time.Minute)) {
		test.Fatal("ticket claims or expiry changed")
	}
	for index := range sealed {
		tampered := bytes.Clone(sealed)
		tampered[index] ^= 1
		if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", tampered); err == nil {
			test.Fatalf("tampered byte%d accepted", index)
		}
	}
	for _, raw := range [][]byte{nil, sealed[:289], append(bytes.Clone(sealed), 0)} {
		if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", raw); !errors.Is(err, ErrImportInvalid) {
			test.Fatal("ticket length not rejected")
		}
	}
	for _, origin := range []string{"", "http://cognate.invalid", "https://cognate.invalid/", "https://user:synthetic@cognate.invalid", "https://COGNATE.invalid", "https://cognate.invalid?query=1"} {
		if _, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, origin, claims); !errors.Is(err, ErrImportPrecondition) {
			test.Fatal("noncanonical origin accepted")
		}
	}
	if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://other.invalid", sealed); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("different origin accepted")
	}
	if _, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "http://127.0.0.1:8888", claims); err != nil {
		test.Fatal("explicit loopback origin rejected")
	}
	fixture.now = fixture.now.Add(-time.Millisecond)
	if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("clock rollback accepted")
	}
	fixture.now = fixture.now.Add(5*time.Minute + time.Millisecond)
	if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("expiry boundary accepted")
	}
	fixture.now = fixture.now.Add(-time.Millisecond)
	if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed); err != nil {
		test.Fatal("unexpired ticket rejected")
	}
	if rows, err := fixture.store.state.client.ImportReceipt.Query().Count(test.Context()); err != nil || rows != 0 {
		test.Fatal("ticket persisted a receipt")
	}
}

func TestImportTicketSessionRestartAndPrivacy(test *testing.T) {
	fixture := newImportTicketFixture(test)
	sealed, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.ImportCredentialForSession(test.Context(), [32]byte{}); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("unobserved session authorized")
	}
	bobHash := sha256.Sum256([]byte("other-synthetic-session"))
	if _, err := fixture.store.CreateOIDCSession(test.Context(), OIDCSessionPlan{Issuer: "https://issuer.invalid", Subject: "bob", TokenHash: bobHash[:], IssuedAt: fixture.now.Add(-time.Minute), ExpiresAt: fixture.now.Add(time.Hour)}); err != nil {
		test.Fatal(err)
	}
	bob, err := fixture.store.ImportCredentialForSession(test.Context(), bobHash)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.OpenImportTicket(test.Context(), bob, "https://cognate.invalid", sealed); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("cross owner ticket accepted")
	}
	if err := fixture.store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), fixture.path, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	fixture.store = reopened
	reopened.state.now = func() time.Time { return fixture.now }
	ticket, err := reopened.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed)
	if err != nil {
		test.Fatal("same-key restart rejected valid ticket")
	}
	for _, value := range []any{fixture.credential, ticket} {
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != `"[redacted import state]"` {
			test.Fatal("opaque JSON exposed")
		}
		for _, rendered := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value), fmt.Sprintf("%s", value)} {
			if rendered != "[redacted import state]" || strings.Contains(rendered, fixture.credential.Owner()) {
				test.Fatal("opaque formatting exposed")
			}
		}
	}
	if err := reopened.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
		test.Fatal(err)
	}
	if _, err := reopened.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("revoked session ticket accepted")
	}
	canceled, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := reopened.ImportCredentialForSession(canceled, bobHash); !errors.Is(err, context.Canceled) {
		test.Fatal("cancellation ignored")
	}
}

func TestImportTicketAuthenticatedPlaintextAndPurpose(test *testing.T) {
	fixture := newImportTicketFixture(test)
	origin := "https://cognate.invalid"
	sealed, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, origin, importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	plaintext, err := fixture.store.state.configurationAEAD.Open(nil, nil, sealed[4:], importTicketAAD(fixture.store.state.keyID, fixture.credential.Owner(), origin))
	if err != nil {
		test.Fatal(err)
	}
	defer clear(plaintext)
	for _, mutate := range []func([]byte){
		func(data []byte) { data[0] = 2 },
		func(data []byte) { data[1] = 0 },
		func(data []byte) { data[1] = 99 },
		func(data []byte) { binary.BigEndian.PutUint64(data[2:10], 0) },
		func(data []byte) {
			binary.BigEndian.PutUint64(data[10:18], uint64(fixture.now.Add(time.Minute).UnixMilli()))
		},
		func(data []byte) { data[34] ^= 1 },
		func(data []byte) { data[66] ^= 1 },
	} {
		altered := bytes.Clone(plaintext)
		mutate(altered)
		sealed := fixture.store.state.configurationAEAD.Seal([]byte("CIP1"), nil, altered, importTicketAAD(fixture.store.state.keyID, fixture.credential.Owner(), origin))
		if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, origin, sealed); !errors.Is(err, ErrImportPrecondition) {
			test.Fatal("invalid authenticated ticket claims accepted")
		}
	}
	if _, err := fixture.store.state.configurationAEAD.Open(nil, nil, sealed[4:], []byte("cognate/configuration-assets/v1/payload")); err == nil {
		test.Fatal("ticket accepted for another AEAD purpose")
	}
}

func TestImportTicketRejectIncompleteClaims(test *testing.T) {
	fixture := newImportTicketFixture(test)
	for _, mutate := range []func(*ImportTicketClaims){
		func(claims *ImportTicketClaims) { claims.SourceProfile = 0 },
		func(claims *ImportTicketClaims) { claims.SourceProfile = 99 },
		func(claims *ImportTicketClaims) { claims.SourceDigest = [32]byte{} },
		func(claims *ImportTicketClaims) { claims.ProjectionDigest = [32]byte{} },
		func(claims *ImportTicketClaims) { claims.AdapterDigest = [32]byte{} },
		func(claims *ImportTicketClaims) { claims.ResolutionDigest = [32]byte{} },
		func(claims *ImportTicketClaims) { claims.OptionsDigest = [32]byte{} },
	} {
		claims := importTestClaims()
		mutate(&claims)
		if _, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", claims); !errors.Is(err, ErrImportInvalid) {
			test.Error("incomplete claims accepted")
		}
	}
}

func TestImportTicketKeyAndSessionExpiry(test *testing.T) {
	fixture := newImportTicketFixture(test)
	origin := "https://cognate.invalid"
	sealed, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, origin, importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	if err := fixture.store.Close(); err != nil {
		test.Fatal(err)
	}
	if unexpected, err := Open(test.Context(), fixture.path, bytes.Repeat([]byte{68}, 32)); err == nil {
		_ = unexpected.Close()
		test.Fatal("database accepted a different master key")
	}
	rekeyed, err := Open(test.Context(), fixture.path, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	fixture.store = rekeyed
	rekeyed.state.now = func() time.Time { return fixture.now }
	current, err := rekeyed.ImportCredentialForSession(test.Context(), fixture.digest)
	if err != nil {
		test.Fatal(err)
	}
	correctAEAD := rekeyed.state.configurationAEAD
	rekeyed.state.configurationAEAD, err = newConfigurationAEAD(bytes.Repeat([]byte{68}, 32))
	if err != nil {
		test.Fatal(err)
	}
	if _, err := rekeyed.OpenImportTicket(test.Context(), current, origin, sealed); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("different key accepted old ticket")
	}
	rekeyed.state.configurationAEAD = correctAEAD
	otherKey := current.box.value
	otherKey.keyID = "different-key"
	if _, err := rekeyed.OpenImportTicket(test.Context(), ImportOwnerCredential{importWrap(otherKey)}, origin, sealed); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("credential from other key accepted")
	}
	sealed, err = rekeyed.SealImportTicket(test.Context(), current, origin, importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	fixture.now = fixture.now.Add(time.Hour)
	if _, err := rekeyed.OpenImportTicket(test.Context(), current, origin, sealed); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("expired session accepted ticket")
	}
	if _, err := rekeyed.ImportCredentialForSession(test.Context(), fixture.digest); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("expired session produced credential")
	}
}
