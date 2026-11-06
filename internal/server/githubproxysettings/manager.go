package githubproxysettings

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ca-x/cognate/internal/githubproxy"
	"github.com/ca-x/cognate/internal/server/state"
)

const (
	settingsName = "github-proxy.enc"
	lockName     = "github-proxy.lock"
	keyDomain    = "cognate/server-github-proxy/key/v1"
	settingsAAD  = "cognate/server-github-proxy/settings/v1\x00network/github-proxy.enc"
)

var (
	ErrInvalid     = errors.New("invalid GitHub proxy settings request")
	ErrConflict    = errors.New("GitHub proxy settings revision conflict")
	ErrUnavailable = errors.New("GitHub proxy settings unavailable")
	ErrUncertain   = fmt.Errorf("persistence uncertain; reopen required: %w", ErrUnavailable)
	ErrClosed      = fmt.Errorf("manager closed: %w", ErrUnavailable)
	ErrOwned       = fmt.Errorf("settings already in use: %w", ErrUnavailable)
	owners         sync.Map
)

type Snapshot struct {
	Revision   int64
	Configured bool
	Endpoint   string
}

type Manager struct {
	mutex       sync.Mutex
	operation   chan struct{}
	root        *os.Root
	identity    os.FileInfo
	owner       interface{ Close() error }
	ownerPath   string
	lockInfo    os.FileInfo
	aead        cipher.AEAD
	files       atomicFiles
	snapshot    Snapshot
	current     *generation
	durableHash [32]byte
	hasFile     bool
	uncertain   bool
	closed      bool
	closeError  error
}

type generation struct {
	transport *githubproxy.Transport
	active    int
	retired   bool
}

type settingsRecord struct {
	Version  int     `json:"version"`
	Revision int64   `json:"revision"`
	ProxyURL *string `json:"proxy_url"`
}

func (*Manager) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private GitHub proxy settings manager]")
}

func (*Manager) LogValue() slog.Value {
	return slog.StringValue("[private GitHub proxy settings manager]")
}

func (settingsRecord) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private GitHub proxy settings record]")
}

func (settingsRecord) LogValue() slog.Value {
	return slog.StringValue("[private GitHub proxy settings record]")
}

func Open(ctx context.Context, dataDirectory string, master []byte) (*Manager, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if len(master) != 32 || !filepath.IsAbs(dataDirectory) || filepath.Clean(dataDirectory) != dataDirectory {
		return nil, ErrInvalid
	}
	parent, err := state.OpenPrivateDirectory(dataDirectory)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err := parent.Close(); err != nil {
		return nil, ErrUnavailable
	}
	root, err := state.OpenPrivateDirectory(filepath.Join(dataDirectory, "network"))
	if err != nil {
		return nil, ErrUnavailable
	}
	manager := &Manager{root: root, ownerPath: root.Name(), operation: make(chan struct{}, 1)}
	if _, loaded := owners.LoadOrStore(manager.ownerPath, manager); loaded {
		_ = root.Close()
		return nil, ErrOwned
	}
	success := false
	defer func() {
		if !success {
			_ = manager.Close()
		}
	}()
	manager.identity, err = root.Stat(".")
	if err != nil {
		return nil, ErrUnavailable
	}
	if err := manager.acquireOwner(ctx); err != nil {
		return nil, err
	}
	key, err := hkdf.Key(sha256.New, master, nil, keyDomain, 32)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrUnavailable
	}
	manager.aead, err = cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, ErrUnavailable
	}
	manager.files = rootFiles{root: root}
	encoded, exists, err := manager.readPrivateSettings()
	if err != nil {
		return nil, ErrUnavailable
	}
	defer clear(encoded)
	record := settingsRecord{Version: 1, Revision: 1, ProxyURL: new("")}
	if exists {
		plaintext, err := manager.aead.Open(nil, nil, encoded, []byte(settingsAAD))
		if err != nil {
			return nil, ErrUnavailable
		}
		defer clear(plaintext)
		record = settingsRecord{}
		if err := json.Unmarshal(plaintext, &record, json.RejectUnknownMembers(true)); err != nil || record.Version != 1 || record.Revision < 1 || record.ProxyURL == nil {
			return nil, ErrUnavailable
		}
	}
	transport, snapshot, err := configuredTransport(record.Revision, *record.ProxyURL)
	if err != nil {
		return nil, ErrUnavailable
	}
	manager.current, manager.snapshot = &generation{transport: transport}, snapshot
	manager.hasFile, manager.durableHash = exists, sha256.Sum256(encoded)
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	success = true
	return manager, nil
}

func (manager *Manager) Get(ctx context.Context) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if err := manager.status(); err != nil {
		return Snapshot{}, err
	}
	return manager.snapshot, nil
}

func (manager *Manager) Update(ctx context.Context, expectedRevision int64, proxyURL string) (Snapshot, error) {
	if err := manager.acquire(ctx); err != nil {
		return Snapshot{}, err
	}
	defer manager.release()
	manager.mutex.Lock()
	if err := manager.status(); err != nil {
		manager.mutex.Unlock()
		return Snapshot{}, err
	}
	previous := manager.snapshot
	manager.mutex.Unlock()
	if expectedRevision != previous.Revision || previous.Revision == math.MaxInt64 {
		return Snapshot{}, ErrConflict
	}
	transport, snapshot, err := configuredTransport(previous.Revision+1, proxyURL)
	if err != nil {
		return Snapshot{}, err
	}
	installed := false
	defer func() {
		if !installed {
			transport.CloseIdleConnections()
		}
	}()
	encoded, exists, err := manager.readPrivateSettings()
	if err != nil || exists != manager.hasFile || sha256.Sum256(encoded) != manager.durableHash {
		clear(encoded)
		manager.failClosed()
		return Snapshot{}, ErrUncertain
	}
	clear(encoded)
	plaintext, err := json.Marshal(settingsRecord{Version: 1, Revision: snapshot.Revision, ProxyURL: new(proxyURL)})
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	defer clear(plaintext)
	sealed := manager.aead.Seal(nil, nil, plaintext, []byte(settingsAAD))
	defer clear(sealed)
	uncertain, err := writePrivateAtomic(ctx, manager.files, sealed)
	if err != nil {
		if uncertain {
			manager.failClosed()
		}
		return Snapshot{}, err
	}
	if err := contextError(ctx); err != nil {
		manager.failClosed()
		return Snapshot{}, errors.Join(ErrUncertain, err)
	}
	confirmed, exists, err := manager.readPrivateSettings()
	confirmedHash := sha256.Sum256(confirmed)
	clear(confirmed)
	if err != nil || !exists || confirmedHash != sha256.Sum256(sealed) {
		manager.failClosed()
		return Snapshot{}, ErrUncertain
	}
	if err := contextError(ctx); err != nil {
		manager.failClosed()
		return Snapshot{}, errors.Join(ErrUncertain, err)
	}
	manager.mutex.Lock()
	retired := manager.current
	retired.retired = true
	manager.current = &generation{transport: transport}
	manager.snapshot = snapshot
	manager.hasFile, manager.durableHash = true, sha256.Sum256(sealed)
	manager.mutex.Unlock()
	installed = true
	retired.transport.CloseIdleConnections()
	return snapshot, nil
}

func (manager *Manager) RoundTrip(request *http.Request) (*http.Response, error) {
	if !allowedRequest(request) {
		return rejectRequest(request, ErrInvalid)
	}
	if err := contextError(request.Context()); err != nil {
		return rejectRequest(request, err)
	}
	manager.mutex.Lock()
	if err := manager.status(); err != nil {
		manager.mutex.Unlock()
		return rejectRequest(request, err)
	}
	current := manager.current
	current.active++
	manager.mutex.Unlock()
	response, err := current.transport.RoundTrip(request)
	if err != nil {
		manager.finish(current)
		return nil, errors.Join(ErrUnavailable, err)
	}
	if response.Body == nil {
		manager.finish(current)
		return response, nil
	}
	response.Body = &leasedBody{ReadCloser: response.Body, ctx: request.Context(), release: func() { manager.finish(current) }}
	return response, nil
}

func (manager *Manager) finish(current *generation) {
	manager.mutex.Lock()
	current.active--
	closeIdle := current.retired && current.active == 0
	manager.mutex.Unlock()
	if closeIdle {
		current.transport.CloseIdleConnections()
	}
}

func (manager *Manager) CloseIdleConnections() {
	manager.mutex.Lock()
	current := manager.current
	manager.mutex.Unlock()
	if current != nil {
		current.transport.CloseIdleConnections()
	}
}

func (manager *Manager) Close() error {
	manager.operation <- struct{}{}
	defer manager.release()
	manager.mutex.Lock()
	if manager.closed {
		err := manager.closeError
		manager.mutex.Unlock()
		return err
	}
	manager.closed = true
	current := manager.current
	manager.current = nil
	if current != nil {
		current.retired = true
	}
	manager.aead = nil
	manager.mutex.Unlock()
	if current != nil {
		current.transport.CloseIdleConnections()
	}
	var closeError error
	if manager.root != nil {
		closeError = manager.root.Close()
	}
	if manager.owner != nil {
		closeError = errors.Join(closeError, manager.owner.Close())
	}
	if closeError != nil {
		manager.closeError = ErrUncertain
		return ErrUncertain
	}
	owners.CompareAndDelete(manager.ownerPath, manager)
	return nil
}

func (manager *Manager) status() error {
	if manager.closed {
		return ErrClosed
	}
	if manager.uncertain {
		return ErrUncertain
	}
	return nil
}

func (manager *Manager) failClosed() {
	manager.mutex.Lock()
	manager.uncertain = true
	current := manager.current
	if current != nil {
		current.retired = true
	}
	manager.mutex.Unlock()
	if current != nil {
		current.transport.CloseIdleConnections()
	}
}

func (manager *Manager) acquire(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	select {
	case manager.operation <- struct{}{}:
		if err := contextError(ctx); err != nil {
			manager.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return contextError(ctx)
	}
}

func (manager *Manager) release() { <-manager.operation }

func contextError(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.Join(ErrUnavailable, context.DeadlineExceeded)
		}
		return errors.Join(ErrUnavailable, context.Canceled)
	}
	return nil
}

func configuredTransport(revision int64, rawURL string) (*githubproxy.Transport, Snapshot, error) {
	transport, err := githubproxy.NewTransport(rawURL)
	if err != nil {
		if errors.Is(err, githubproxy.ErrInvalidURL) {
			return nil, Snapshot{}, ErrInvalid
		}
		return nil, Snapshot{}, ErrUnavailable
	}
	snapshot := Snapshot{Revision: revision, Configured: rawURL != ""}
	if rawURL != "" {
		address, err := url.Parse(rawURL)
		if err != nil {
			transport.CloseIdleConnections()
			return nil, Snapshot{}, ErrInvalid
		}
		snapshot.Endpoint = address.Scheme + "://" + address.Host
	}
	return transport, snapshot, nil
}

func allowedRequest(request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.Scheme != "https" || request.URL.Opaque != "" || request.URL.User != nil {
		return false
	}
	if request.Host != "" && !strings.EqualFold(request.Host, request.URL.Host) {
		return false
	}
	switch strings.ToLower(request.URL.Host) {
	case "api.github.com", "api.github.com:443", "codeload.github.com", "codeload.github.com:443", "github.com", "github.com:443":
		return true
	default:
		return false
	}
}

func rejectRequest(request *http.Request, err error) (*http.Response, error) {
	if request != nil && request.Body != nil {
		_ = request.Body.Close()
	}
	return nil, err
}

type leasedBody struct {
	io.ReadCloser
	ctx     context.Context
	once    sync.Once
	release func()
}

func (*leasedBody) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private GitHub response body]")
}

func (*leasedBody) LogValue() slog.Value {
	return slog.StringValue("[private GitHub response body]")
}

func (body *leasedBody) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	if err == nil {
		return count, nil
	}
	body.once.Do(body.release)
	if errors.Is(err, io.EOF) {
		return count, io.EOF
	}
	if err := contextError(body.ctx); err != nil {
		return count, err
	}
	if errors.Is(err, context.Canceled) {
		return count, errors.Join(ErrUnavailable, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return count, errors.Join(ErrUnavailable, context.DeadlineExceeded)
	}
	return count, errors.Join(ErrUnavailable, githubproxy.ErrRequestFailed)
}

func (body *leasedBody) Close() error {
	err := body.ReadCloser.Close()
	body.once.Do(body.release)
	if err != nil {
		return errors.Join(ErrUnavailable, githubproxy.ErrRequestFailed)
	}
	return nil
}
