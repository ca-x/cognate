package subscription

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/packclient"
	"github.com/ca-x/cognate/internal/state"
)

type Manager struct {
	mutex   sync.Mutex
	life    context.Context
	cancel  context.CancelFunc
	store   *state.Store
	install *install.Session
	client  *http.Client
	home    string
	key     []byte
}

func (*Manager) String() string       { return "[redacted subscription manager]" }
func (*Manager) GoString() string     { return "[redacted subscription manager]" }
func (*Manager) LogValue() slog.Value { return slog.StringValue("[redacted subscription manager]") }
func (*Manager) Format(destination fmt.State, _ rune) {
	_, _ = io.WriteString(destination, "[redacted subscription manager]")
}

func Open(ctx context.Context, home, directory string, key []byte, client *http.Client) (*Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(key) != 32 || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return nil, fmt.Errorf("invalid client home or credential key")
	}
	derived, err := deriveCredentialKey(key)
	if err != nil {
		return nil, err
	}
	session, err := install.OpenSession(ctx, directory)
	if err != nil {
		clear(derived)
		return nil, err
	}
	store, err := state.Open(ctx, filepath.Join(directory, "client.db"))
	if err != nil {
		clear(derived)
		return nil, errors.Join(err, session.Close())
	}
	life, cancel := context.WithCancel(ctx)
	manager := &Manager{life: life, cancel: cancel, store: store, install: session, client: client, home: home, key: derived}
	if err := manager.verifyCredentials(ctx); err != nil {
		return nil, errors.Join(err, manager.Close())
	}
	return manager, nil
}

func (manager *Manager) Close() error {
	manager.cancel()
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if manager.store == nil {
		return nil
	}
	err := errors.Join(manager.store.Close(), manager.install.Close())
	manager.store, manager.install = nil, nil
	clear(manager.key)
	manager.key = nil
	return err
}

func (manager *Manager) requestContext(ctx context.Context) (context.Context, func(), error) {
	if manager.store == nil || manager.life.Err() != nil {
		return nil, nil, fmt.Errorf("subscription manager is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	request, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(manager.life, cancel)
	return request, func() { stop(); cancel() }, nil
}

func (manager *Manager) List(ctx context.Context) ([]state.Subscription, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	ctx, done, err := manager.requestContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	return manager.store.Subscriptions(ctx)
}

func (manager *Manager) Subscribe(ctx context.Context, server, credential string) (state.Subscription, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	ctx, done, err := manager.requestContext(ctx)
	if err != nil {
		return state.Subscription{}, err
	}
	defer done()
	client, err := packclient.New(server, credential, manager.client)
	if err != nil {
		return state.Subscription{}, err
	}
	manifest, err := client.Manifest(ctx)
	if err != nil {
		return state.Subscription{}, err
	}
	origin, _ := url.Parse(server)
	origin.Path, origin.Host = "", strings.ToLower(origin.Host)
	server = origin.String()
	records, err := manager.store.Subscriptions(ctx)
	if err != nil {
		return state.Subscription{}, err
	}
	id := rand.Text()
	var existing *state.Subscription
	for _, record := range records {
		if record.Server == server && record.PackID == manifest.PackId {
			existing, id = new(record), record.ID
			break
		}
	}
	sealed, err := manager.sealCredential(id, server, manifest.PackId, []byte(credential))
	if err != nil {
		return state.Subscription{}, err
	}
	defer clear(sealed)
	if existing == nil {
		return manager.store.CreateSubscription(ctx, state.SubscriptionDraft{ID: id, Server: server, PackID: manifest.PackId, Name: manifest.Name, SealedCredential: sealed})
	}
	if err := manager.store.RotateSubscriptionCredential(ctx, existing.ID, existing.Version, sealed); err != nil {
		return state.Subscription{}, err
	}
	existing.Version++
	existing.Enabled = true
	return *existing, nil
}

func (manager *Manager) Unsubscribe(ctx context.Context, id string) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	ctx, done, err := manager.requestContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	records, err := manager.store.Subscriptions(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.ID == id {
			return manager.store.DisableSubscription(ctx, id, record.Version)
		}
	}
	return fmt.Errorf("subscription not found")
}
