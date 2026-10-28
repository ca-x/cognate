package packs

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/ca-x/skillhub/internal/server/state"
)

var (
	ErrClosed = errors.New("pack distribution is closed")
	ErrBusy   = errors.New("pack distribution capacity exhausted")
)

type ArchiveSource interface {
	WriteSkillArchive(context.Context, string, string, string, io.Writer) error
}

type Service struct {
	store     *state.Store
	source    ArchiveSource
	signing   []byte
	now       func() time.Time
	lifetime  context.Context
	cancel    context.CancelFunc
	mutex     sync.Mutex
	closed    bool
	active    sync.WaitGroup
	closeOnce sync.Once
	slots     chan struct{}
}

func (*Service) String() string   { return "[redacted pack service]" }
func (*Service) GoString() string { return "[redacted pack service]" }

func NewService(store *state.Store, source ArchiveSource, masterKey []byte) (*Service, error) {
	if store == nil || source == nil || !store.MatchesKey(masterKey) {
		return nil, errors.New("pack distribution requires matching storage, archive source, and master key")
	}
	signing, err := hkdf.Key(sha256.New, masterKey, nil, "skillhub/pack-share-signing/v1", 32)
	if err != nil {
		return nil, errors.New("derive pack signing key")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{store: store, source: source, signing: signing, now: time.Now, lifetime: ctx, cancel: cancel, slots: make(chan struct{}, 2)}, nil
}

func (service *Service) BeginRequest(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	service.mutex.Lock()
	if service.closed {
		service.mutex.Unlock()
		return nil, nil, ErrClosed
	}
	service.active.Add(1)
	service.mutex.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	stop := context.AfterFunc(service.lifetime, cancel)
	return ctx, func() { stop(); cancel(); service.active.Done() }, nil
}

func (service *Service) Close() {
	service.closeOnce.Do(func() {
		service.mutex.Lock()
		service.closed = true
		service.cancel()
		service.mutex.Unlock()
		service.active.Wait()
		clear(service.signing)
	})
}
