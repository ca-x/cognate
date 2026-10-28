package auth

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/server/state"
	"golang.org/x/crypto/argon2"
)

var (
	ErrAccountExists      = errors.New("local account already exists")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInvalidSession     = errors.New("invalid or expired session")
	ErrInvalidInput       = errors.New("invalid username or password format")
	ErrBusy               = errors.New("authentication capacity exhausted")
)

const (
	passwordMemoryKiB = 19 * 1024
	passwordRounds    = 2
	passwordSaltSize  = 16
	passwordHashSize  = 32
	sessionLifetime   = 24 * time.Hour
)

type Service struct {
	store     *state.Store
	signing   []byte
	dummyHash []byte
	slots     chan struct{}
	now       func() time.Time
}

type SessionToken struct {
	value     string
	expiresAt time.Time
}

func (Service) String() string                  { return "[redacted authentication service]" }
func (Service) GoString() string                { return "[redacted authentication service]" }
func (SessionToken) String() string             { return "[redacted session]" }
func (SessionToken) GoString() string           { return "[redacted session]" }
func (token SessionToken) Reveal() string       { return token.value }
func (token SessionToken) ExpiresAt() time.Time { return token.expiresAt }

func NewService(store *state.Store, masterKey []byte) (*Service, error) {
	if !store.MatchesKey(masterKey) {
		return nil, fmt.Errorf("authentication requires server state and a 32-byte master key")
	}
	signing, err := hkdf.Key(sha256.New, masterKey, nil, "skillhub/session-signing/v1", 32)
	if err != nil {
		return nil, fmt.Errorf("derive session signing key")
	}
	dummy := make([]byte, 32)
	if _, err := rand.Read(dummy); err != nil {
		return nil, fmt.Errorf("initialize authentication")
	}
	defer clear(dummy)
	dummyHash, err := hashPassword(dummy)
	if err != nil {
		return nil, err
	}
	return &Service{store: store, signing: signing, dummyHash: dummyHash, slots: make(chan struct{}, 2), now: time.Now}, nil
}

func (service *Service) Register(ctx context.Context, username string, password []byte) (string, error) {
	normalized, err := normalizeUsername(username)
	if err != nil || !validPassword(password) {
		return "", ErrInvalidInput
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
	if err := ctx.Err(); err != nil {
		return "", err
	}
	user, err := service.store.CreatePasswordAccount(ctx, normalized, hash)
	if errors.Is(err, state.ErrAccountConflict) {
		return "", ErrAccountExists
	}
	return user, err
}

func (service *Service) Login(ctx context.Context, username string, password []byte) (SessionToken, error) {
	normalized, err := normalizeUsername(username)
	if err != nil || !validPassword(password) {
		return SessionToken{}, ErrInvalidCredentials
	}
	if err := service.acquire(ctx); err != nil {
		return SessionToken{}, err
	}
	defer service.release()
	account, err := service.store.PasswordAccount(ctx, normalized)
	missing := errors.Is(err, state.ErrAccountNotFound)
	if err != nil && !missing {
		return SessionToken{}, err
	}
	hash := account.PasswordHash
	if missing {
		hash = service.dummyHash
	} else {
		defer clear(account.PasswordHash)
	}
	valid := verifyPassword(password, hash)
	if err := ctx.Err(); err != nil {
		return SessionToken{}, err
	}
	if missing || !valid {
		return SessionToken{}, ErrInvalidCredentials
	}
	token, digest, err := service.newSession()
	if err != nil {
		return SessionToken{}, err
	}
	now := service.now().UTC()
	token.expiresAt = now.Add(sessionLifetime)
	if err := service.store.CreatePasswordSession(ctx, normalized, hash, digest[:], now, token.expiresAt); err != nil {
		if errors.Is(err, state.ErrAccountNotFound) {
			return SessionToken{}, ErrInvalidCredentials
		}
		return SessionToken{}, err
	}
	return token, nil
}

func (service *Service) Authenticate(ctx context.Context, value string) (string, error) {
	digest, err := service.sessionDigest(value)
	if err != nil {
		return "", ErrInvalidSession
	}
	user, expiresAt, err := service.store.SessionUser(ctx, digest[:], service.now().UTC())
	if errors.Is(err, state.ErrSessionInvalid) {
		return "", ErrInvalidSession
	}
	if err == nil && !service.now().UTC().Before(expiresAt) {
		return "", ErrInvalidSession
	}
	return user, err
}

func (service *Service) LoginOIDC(ctx context.Context, issuer, subject, bindUser, binding string) (SessionToken, string, error) {
	if err := ctx.Err(); err != nil {
		return SessionToken{}, "", err
	}
	token, digest, err := service.newSession()
	if err != nil {
		return SessionToken{}, "", err
	}
	issuedAt := service.now().UTC()
	token.expiresAt = issuedAt.Add(sessionLifetime)
	var bindingHash []byte
	if bindUser != "" {
		proof, err := service.sessionDigest(binding)
		if err != nil {
			return SessionToken{}, "", ErrInvalidSession
		}
		bindingHash = proof[:]
	}
	user, err := service.store.CreateOIDCSession(ctx, state.OIDCSessionPlan{Issuer: issuer, Subject: subject, BindUser: bindUser, BindSessionHash: bindingHash, TokenHash: digest[:], IssuedAt: issuedAt, ExpiresAt: token.expiresAt})
	if err != nil {
		return SessionToken{}, "", err
	}
	return token, user, nil
}

func (service *Service) Logout(ctx context.Context, value string) error {
	digest, err := service.sessionDigest(value)
	if err != nil {
		return ErrInvalidSession
	}
	return service.store.RevokeSession(ctx, digest[:])
}

func (service *Service) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case service.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			service.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrBusy
	}
}

func (service *Service) release() { <-service.slots }

func normalizeUsername(value string) (string, error) {
	if len(value) < 3 || len(value) > 64 {
		return "", fmt.Errorf("invalid username")
	}
	for _, character := range value {
		if character != '-' && character != '_' && character != '.' && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return "", fmt.Errorf("invalid username")
		}
	}
	return strings.ToLower(value), nil
}

func validPassword(password []byte) bool { return len(password) >= 8 && len(password) <= 1024 }

func hashPassword(password []byte) ([]byte, error) {
	salt := make([]byte, passwordSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate password salt")
	}
	digest := argon2.IDKey(password, salt, passwordRounds, passwordMemoryKiB, 1, passwordHashSize)
	defer clear(digest)
	result := make([]byte, 1, 1+passwordSaltSize+passwordHashSize)
	result[0] = 1
	result = append(result, salt...)
	result = append(result, digest...)
	return result, nil
}

func verifyPassword(password, record []byte) bool {
	if len(record) != 1+passwordSaltSize+passwordHashSize || record[0] != 1 {
		return false
	}
	digest := argon2.IDKey(password, record[1:1+passwordSaltSize], passwordRounds, passwordMemoryKiB, 1, passwordHashSize)
	defer clear(digest)
	return hmac.Equal(digest, record[1+passwordSaltSize:])
}

func (service *Service) newSession() (SessionToken, [32]byte, error) {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return SessionToken{}, [32]byte{}, fmt.Errorf("generate session")
	}
	mac := hmac.New(sha256.New, service.signing)
	_, _ = mac.Write(nonce[:])
	value := base64.RawURLEncoding.EncodeToString(nonce[:]) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return SessionToken{value: value}, sha256.Sum256([]byte(value)), nil
}

func (service *Service) sessionDigest(value string) ([32]byte, error) {
	if len(value) != 87 {
		return [32]byte{}, ErrInvalidSession
	}
	encodedNonce, encodedMAC, ok := strings.Cut(value, ".")
	if !ok || len(encodedNonce) != 43 || len(encodedMAC) != 43 {
		return [32]byte{}, ErrInvalidSession
	}
	nonce, err := base64.RawURLEncoding.DecodeString(encodedNonce)
	if err != nil || len(nonce) != 32 || base64.RawURLEncoding.EncodeToString(nonce) != encodedNonce {
		return [32]byte{}, ErrInvalidSession
	}
	signature, err := base64.RawURLEncoding.DecodeString(encodedMAC)
	if err != nil || len(signature) != 32 || base64.RawURLEncoding.EncodeToString(signature) != encodedMAC {
		return [32]byte{}, ErrInvalidSession
	}
	mac := hmac.New(sha256.New, service.signing)
	_, _ = mac.Write(nonce)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return [32]byte{}, ErrInvalidSession
	}
	return sha256.Sum256([]byte(value)), nil
}
