package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/session"
)

const ImportTicketBytes = 290
const importTicketLifetimeMS = 300000
const importTicketPurpose = "cognate/import-selection/v1/ticket"

var (
	ErrImportUnauthorized = errors.New("invalid import authorization")
	ErrImportInvalid      = errors.New("invalid import request")
	ErrImportPrecondition = errors.New("import precondition failed")
	ErrImportUnavailable  = errors.New("import storage unavailable")
)

type importBox[Value any] struct{ value Value }
type importOpaque[Value any] struct{ box *importBox[Value] }

func importWrap[Value any](value Value) importOpaque[Value] {
	return importOpaque[Value]{box: &importBox[Value]{value: value}}
}

func (importOpaque[Value]) String() string   { return "[redacted import state]" }
func (importOpaque[Value]) GoString() string { return "[redacted import state]" }
func (importOpaque[Value]) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted import state]")
}
func (importOpaque[Value]) LogValue() slog.Value { return slog.StringValue("[redacted import state]") }
func (importOpaque[Value]) MarshalText() ([]byte, error) {
	return []byte("[redacted import state]"), nil
}
func (importOpaque[Value]) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted import state]"`), nil
}

type importOwnerCredential struct {
	owner  string
	digest [32]byte
	keyID  string
}

type ImportOwnerCredential struct {
	importOpaque[importOwnerCredential]
}

func (credential ImportOwnerCredential) Owner() string {
	if credential.box == nil {
		return ""
	}
	return credential.box.value.owner
}

type ImportSourceProfile uint8

const (
	ImportSourceCCSwitch24 ImportSourceProfile = 1
	ImportSourceMagpie     ImportSourceProfile = 2
)

type ImportTicketClaims struct {
	SourceProfile    ImportSourceProfile
	SourceDigest     [32]byte
	ProjectionDigest [32]byte
	AdapterDigest    [32]byte
	ResolutionDigest [32]byte
	OptionsDigest    [32]byte
}

type importTicket struct {
	claims       ImportTicketClaims
	id           [16]byte
	issuedMS     int64
	expiresMS    int64
	ownerDigest  [32]byte
	originDigest [32]byte
}

type ImportTicket struct{ importOpaque[importTicket] }

func (ticket ImportTicket) Claims() (ImportTicketClaims, bool) {
	if ticket.box == nil {
		return ImportTicketClaims{}, false
	}
	return ticket.box.value.claims, true
}

func (ticket ImportTicket) ExpiresAt() time.Time {
	if ticket.box == nil {
		return time.Time{}
	}
	return time.UnixMilli(ticket.box.value.expiresMS)
}

func (store *Store) ImportCredentialForSession(ctx context.Context, digest [32]byte) (ImportOwnerCredential, error) {
	if err := ctx.Err(); err != nil {
		return ImportOwnerCredential{}, err
	}
	if store == nil || store.state == nil {
		return ImportOwnerCredential{}, ErrImportUnauthorized
	}
	owner, _, err := store.SessionUser(ctx, digest[:], store.state.now().UTC())
	if err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return ImportOwnerCredential{}, canceled
		}
		if errors.Is(err, ErrSessionInvalid) {
			return ImportOwnerCredential{}, ErrImportUnauthorized
		}
		return ImportOwnerCredential{}, ErrImportUnavailable
	}
	return ImportOwnerCredential{importWrap(importOwnerCredential{owner: owner, digest: digest, keyID: store.state.keyID})}, nil
}

func (store *Store) importOwner(ctx context.Context, sessions *ent.SessionClient, credential ImportOwnerCredential) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if credential.box == nil || credential.box.value.keyID != store.state.keyID {
		return "", ErrImportUnauthorized
	}
	value := credential.box.value
	entry, err := sessions.Query().Where(session.TokenHashEQ(value.digest[:])).WithUser().Only(ctx)
	if err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return "", canceled
		}
		if ent.IsNotFound(err) {
			return "", ErrImportUnauthorized
		}
		return "", ErrImportUnavailable
	}
	instant := store.state.now().UTC()
	if entry.Edges.User == nil || entry.Edges.User.UserKey != value.owner || instant.Before(entry.IssuedAt) || !instant.Before(entry.ExpiresAt) {
		return "", ErrImportUnauthorized
	}
	return value.owner, nil
}

func importTicketAAD(keyID, owner, origin string) []byte {
	result := []byte(importTicketPurpose)
	for _, field := range []string{keyID, owner, origin} {
		result = binary.BigEndian.AppendUint64(result, uint64(len(field)))
		result = append(result, field...)
	}
	return result
}

func importProfileValid(profile ImportSourceProfile) bool {
	return profile == ImportSourceCCSwitch24 || profile == ImportSourceMagpie
}

func importClaimsValid(claims ImportTicketClaims) bool {
	if !importProfileValid(claims.SourceProfile) {
		return false
	}
	for _, digest := range [][32]byte{claims.SourceDigest, claims.ProjectionDigest, claims.AdapterDigest, claims.ResolutionDigest, claims.OptionsDigest} {
		if digest == [32]byte{} {
			return false
		}
	}
	return true
}

func (store *Store) SealImportTicket(ctx context.Context, credential ImportOwnerCredential, origin string, claims ImportTicketClaims) ([]byte, error) {
	if !nativesessionauth.ValidServerOrigin(origin) {
		return nil, ErrImportPrecondition
	}
	if !importClaimsValid(claims) {
		return nil, ErrImportInvalid
	}
	if store == nil || store.state == nil {
		return nil, ErrImportUnauthorized
	}
	owner, err := store.importOwner(ctx, store.state.client.Session, credential)
	if err != nil {
		return nil, err
	}
	issuedMS := store.state.now().UTC().UnixMilli()
	if issuedMS <= 0 || issuedMS > math.MaxInt64-importTicketLifetimeMS {
		return nil, ErrImportPrecondition
	}
	var identifier [16]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		return nil, ErrImportUnavailable
	}
	plaintext := []byte{1, byte(claims.SourceProfile)}
	plaintext = binary.BigEndian.AppendUint64(plaintext, uint64(issuedMS))
	plaintext = binary.BigEndian.AppendUint64(plaintext, uint64(issuedMS+importTicketLifetimeMS))
	plaintext = append(plaintext, identifier[:]...)
	ownerDigest, originDigest := sha256.Sum256([]byte(owner)), sha256.Sum256([]byte(origin))
	for _, digest := range [][32]byte{ownerDigest, originDigest, claims.SourceDigest, claims.ProjectionDigest, claims.AdapterDigest, claims.ResolutionDigest, claims.OptionsDigest} {
		plaintext = append(plaintext, digest[:]...)
	}
	defer clear(plaintext)
	sealed := store.state.configurationAEAD.Seal([]byte("CIP1"), nil, plaintext, importTicketAAD(store.state.keyID, owner, origin))
	if err := ctx.Err(); err != nil {
		clear(sealed)
		return nil, err
	}
	if len(sealed) != ImportTicketBytes {
		clear(sealed)
		return nil, ErrImportUnavailable
	}
	return sealed, nil
}

func (store *Store) OpenImportTicket(ctx context.Context, credential ImportOwnerCredential, origin string, sealed []byte) (ImportTicket, error) {
	if !nativesessionauth.ValidServerOrigin(origin) {
		return ImportTicket{}, ErrImportPrecondition
	}
	if len(sealed) != ImportTicketBytes || string(sealed[:4]) != "CIP1" {
		return ImportTicket{}, ErrImportInvalid
	}
	if store == nil || store.state == nil {
		return ImportTicket{}, ErrImportUnauthorized
	}
	owner, err := store.importOwner(ctx, store.state.client.Session, credential)
	if err != nil {
		return ImportTicket{}, err
	}
	plaintext, err := store.state.configurationAEAD.Open(nil, nil, sealed[4:], importTicketAAD(store.state.keyID, owner, origin))
	if err != nil {
		return ImportTicket{}, ErrImportPrecondition
	}
	defer clear(plaintext)
	if len(plaintext) != 258 || plaintext[0] != 1 || !importProfileValid(ImportSourceProfile(plaintext[1])) {
		return ImportTicket{}, ErrImportPrecondition
	}
	issuedMS, expiresMS := binary.BigEndian.Uint64(plaintext[2:10]), binary.BigEndian.Uint64(plaintext[10:18])
	now := store.state.now().UTC().UnixMilli()
	if issuedMS == 0 || issuedMS > math.MaxInt64-importTicketLifetimeMS || expiresMS != issuedMS+importTicketLifetimeMS || now < 0 || uint64(now) < issuedMS || uint64(now) >= expiresMS {
		return ImportTicket{}, ErrImportPrecondition
	}
	ownerDigest, originDigest := sha256.Sum256([]byte(owner)), sha256.Sum256([]byte(origin))
	if subtle.ConstantTimeCompare(plaintext[34:66], ownerDigest[:]) != 1 || subtle.ConstantTimeCompare(plaintext[66:98], originDigest[:]) != 1 {
		return ImportTicket{}, ErrImportPrecondition
	}
	value := importTicket{issuedMS: int64(issuedMS), expiresMS: int64(expiresMS), id: [16]byte(plaintext[18:34]), ownerDigest: ownerDigest, originDigest: originDigest}
	value.claims = ImportTicketClaims{SourceProfile: ImportSourceProfile(plaintext[1]), SourceDigest: [32]byte(plaintext[98:130]), ProjectionDigest: [32]byte(plaintext[130:162]), AdapterDigest: [32]byte(plaintext[162:194]), ResolutionDigest: [32]byte(plaintext[194:226]), OptionsDigest: [32]byte(plaintext[226:258])}
	if !importClaimsValid(value.claims) {
		return ImportTicket{}, ErrImportPrecondition
	}
	if err := ctx.Err(); err != nil {
		return ImportTicket{}, err
	}
	return ImportTicket{importWrap(value)}, nil
}
