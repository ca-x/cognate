package publiccatalog

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ca-x/skillhub/internal/server/packs"
	"github.com/ca-x/skillhub/internal/server/state"
)

var (
	ErrClosed = errors.New("public catalog is closed")
	ErrBusy   = errors.New("public download capacity exhausted")
)

type Service struct {
	store     *state.Store
	source    packs.ArchiveSource
	lifetime  context.Context
	cancel    context.CancelFunc
	mutex     sync.Mutex
	closed    bool
	active    sync.WaitGroup
	closeOnce sync.Once
	slots     chan struct{}
}

func (*Service) String() string   { return "[redacted public catalog]" }
func (*Service) GoString() string { return "[redacted public catalog]" }

func NewService(store *state.Store, source packs.ArchiveSource) (*Service, error) {
	if store == nil || source == nil {
		return nil, errors.New("public catalog requires storage and a verified archive source")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{store: store, source: source, lifetime: ctx, cancel: cancel, slots: make(chan struct{}, 2)}, nil
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
	return ctx, sync.OnceFunc(func() { stop(); cancel(); service.active.Done() }), nil
}

func (service *Service) Close() error {
	service.closeOnce.Do(func() {
		service.mutex.Lock()
		service.closed = true
		service.cancel()
		service.mutex.Unlock()
		service.active.Wait()
	})
	return nil
}

func (service *Service) ListSkills(ctx context.Context, query state.PublicSkillQuery) (state.PublicSkillPage, error) {
	ctx, done, err := service.BeginRequest(ctx)
	if err != nil {
		return state.PublicSkillPage{}, err
	}
	defer done()
	return service.store.ListPublicSkills(ctx, query)
}

func (service *Service) ListCategories(ctx context.Context, limit uint32, cursor string) (state.PublicCategoryPage, error) {
	ctx, done, err := service.BeginRequest(ctx)
	if err != nil {
		return state.PublicCategoryPage{}, err
	}
	defer done()
	return service.store.ListPublicCategories(ctx, limit, cursor)
}

func (service *Service) GetSkill(ctx context.Context, id string) (state.PublicSkill, error) {
	ctx, done, err := service.BeginRequest(ctx)
	if err != nil {
		return state.PublicSkill{}, err
	}
	defer done()
	return service.store.PublicSkill(ctx, id)
}
