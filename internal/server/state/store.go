package state

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/filelock"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/user"
	_ "github.com/lib-x/entsqlite"
)

type Store struct{ state *storeState }

type storeState struct {
	client            *ent.Client
	aead              cipher.AEAD
	keyID             string
	observationMaxAge time.Duration
	now               func() time.Time
}

type Options struct {
	ObservationMaxAge time.Duration
}

func (Store) String() string   { return "[redacted server state]" }
func (Store) GoString() string { return "[redacted server state]" }

func (store *Store) MatchesKey(key []byte) bool {
	if store == nil || store.state == nil || len(key) != 32 {
		return false
	}
	fingerprint := sha256.Sum256(key)
	return subtle.ConstantTimeCompare([]byte(store.state.keyID), []byte(base64.RawURLEncoding.EncodeToString(fingerprint[:]))) == 1
}

func Open(ctx context.Context, path string, key []byte) (*Store, error) {
	return OpenWithOptions(ctx, path, key, Options{})
}

func OpenWithOptions(ctx context.Context, path string, key []byte, options Options) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.ObservationMaxAge == 0 {
		options.ObservationMaxAge = time.Hour
	}
	if options.ObservationMaxAge < 0 || options.ObservationMaxAge > 24*time.Hour {
		return nil, fmt.Errorf("GitHub observation maximum age must be at most 24 hours")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("server credential encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize server credential encryption")
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, fmt.Errorf("initialize server credential encryption")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) == "." || filepath.Dir(path) == path {
		return nil, fmt.Errorf("server database path must be absolute and clean")
	}
	if err := validateServerDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("resolve server data directory: %w", err)
	}
	if err := validateServerDirectory(parent); err != nil {
		return nil, err
	}
	path = filepath.Join(parent, filepath.Base(path))
	lock, err := filelock.Acquire(ctx, path+".schema.lock")
	if err != nil {
		return nil, fmt.Errorf("lock server schema: %w", err)
	}
	defer lock.Close()
	if err := prepareDatabaseFile(path); err != nil {
		return nil, err
	}
	parameters := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(10000)", "synchronous(NORMAL)"} {
		parameters.Add("_pragma", pragma)
	}
	filePath := filepath.ToSlash(path)
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	address := url.URL{Scheme: "file", Path: filePath, RawQuery: parameters.Encode()}
	client, err := ent.Open("sqlite3", address.String())
	if err != nil {
		return nil, fmt.Errorf("open server database: %w", err)
	}
	if err := client.Schema.Create(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize server database: %w", err)
	}
	fingerprint := sha256.Sum256(key)
	store := &Store{state: &storeState{client: client, aead: aead, keyID: base64.RawURLEncoding.EncodeToString(fingerprint[:]), observationMaxAge: options.ObservationMaxAge, now: time.Now}}
	if err := store.verifyDatabaseKey(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	if err := ensureCatalogIntegrity(ctx, address.String()); err != nil {
		_ = client.Close()
		return nil, err
	}
	return store, nil
}

func prepareDatabaseFile(path string) error {
	exists, err := inspectServerFile(path)
	if err != nil {
		return fmt.Errorf("inspect server database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		sidecarExists, err := inspectServerFile(path + suffix)
		if err != nil {
			return fmt.Errorf("inspect server database sidecar: %w", err)
		}
		if sidecarExists && !exists {
			return fmt.Errorf("server database has an orphaned sidecar")
		}
	}
	if exists {
		return nil
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if err := file.Close(); err != nil {
			return err
		}
		exists, err := inspectServerFile(path)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("server database disappeared during creation")
		}
		return nil
	}
	if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create server database: %w", err)
	}
	exists, err = inspectServerFile(path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("server database disappeared during creation")
	}
	return nil
}

func (store *Store) Close() error {
	if store == nil || store.state == nil {
		return nil
	}
	return store.state.client.Close()
}

func (store *Store) CreateUser(ctx context.Context, userKey string) error {
	if err := validateIdentifier(userKey); err != nil {
		return err
	}
	if err := store.state.client.User.Create().SetUserKey(userKey).Exec(ctx); err != nil {
		return fmt.Errorf("create server user: %w", err)
	}
	return nil
}

func validateIdentifier(value string) error {
	if value == "" || len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("invalid server identifier")
	}
	return nil
}

func findUser(ctx context.Context, client *ent.Client, userKey string) (*ent.User, error) {
	entry, err := client.User.Query().Where(user.UserKeyEQ(userKey)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("find server user: %w", err)
	}
	return entry, nil
}
