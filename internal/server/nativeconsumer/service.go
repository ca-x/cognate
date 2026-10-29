package nativeconsumer

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/agentadapter"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type Service struct {
	consumer *auth.NativeConsumerService
	domain   *configuration.NativeService
	store    *state.Store
	verifier *nativesessionauth.Verifier
	origin   string
	prune    func(context.Context) (state.NativePruneResult, error)
	mutex    sync.Mutex
	lifetime context.Context
	cancel   context.CancelFunc
	closed   bool
	workers  sync.WaitGroup
	slots    chan struct{}
}

func New(store *state.Store, owner *auth.Service, master []byte, origin string) (*Service, error) {
	if !strings.HasPrefix(origin, "https://") || !nativesessionauth.ValidServerOrigin(origin) {
		return nil, state.ErrNativeInvalid
	}
	consumer, err := auth.NewNativeConsumerService(owner, master, origin)
	if err != nil {
		return nil, err
	}
	registry, err := agentadapter.Builtins()
	if err != nil {
		return nil, state.ErrNativeUnavailable
	}
	domain, err := configuration.NewNativeService(store, consumer, registry)
	if err != nil {
		return nil, err
	}
	verifier, err := nativesessionauth.NewVerifier(master)
	if err != nil {
		return nil, state.ErrNativeInvalid
	}
	return &Service{consumer: consumer, domain: domain, store: store, verifier: verifier, origin: origin, prune: store.PruneNativeConsumers, slots: make(chan struct{}, 8)}, nil
}

func (service *Service) Origin() string { return service.origin }

func (service *Service) Start(ctx context.Context) error {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if service.closed || service.cancel != nil {
		return state.ErrNativeUnavailable
	}
	service.lifetime, service.cancel = context.WithCancel(ctx)
	service.workers.Go(func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if service.lifetime.Err() != nil {
				return
			}
			_, _ = service.prune(service.lifetime)
			select {
			case <-service.lifetime.Done():
				return
			case <-ticker.C:
			}
		}
	})
	return nil
}

func (service *Service) Close() {
	service.mutex.Lock()
	service.closed = true
	if service.cancel != nil {
		service.cancel()
	}
	service.mutex.Unlock()
	service.workers.Wait()
}

func (service *Service) Call(ctx context.Context, operation func(context.Context) error) error {
	service.mutex.Lock()
	if service.closed || service.lifetime == nil || service.lifetime.Err() != nil {
		service.mutex.Unlock()
		return state.ErrNativeUnavailable
	}
	select {
	case service.slots <- struct{}{}:
	default:
		service.mutex.Unlock()
		return state.ErrNativeQuota
	}
	service.workers.Add(1)
	lifetime := service.lifetime
	service.mutex.Unlock()
	defer service.workers.Done()
	defer func() { <-service.slots }()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	stop := context.AfterFunc(lifetime, cancel)
	defer cancel()
	defer stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation(ctx)
}

func (service *Service) Begin(ctx context.Context, bearer string, start state.NativeConsumerStart) (state.NativeBindSnapshot, error) {
	return service.consumer.Begin(ctx, bearer, start)
}

func (service *Service) Bind(ctx context.Context, bearer, challenge string, signature []byte) (auth.IssuedNativeConsumer, [32]byte, error) {
	evidence, err := service.verifier.VerifyOwner(bearer)
	if err != nil {
		return auth.IssuedNativeConsumer{}, [32]byte{}, state.ErrNativeUnauthorized
	}
	parent, err := service.store.OwnerCredentialFromVerified(evidence)
	if err != nil {
		return auth.IssuedNativeConsumer{}, [32]byte{}, err
	}
	snapshot, err := service.store.NativeBindChallenge(ctx, parent, challenge)
	if err != nil {
		return auth.IssuedNativeConsumer{}, [32]byte{}, err
	}
	fields, err := snapshot.BindFields()
	if err != nil {
		return auth.IssuedNativeConsumer{}, [32]byte{}, err
	}
	issued, err := service.consumer.Bind(ctx, bearer, challenge, signature)
	if err != nil {
		return auth.IssuedNativeConsumer{}, [32]byte{}, err
	}
	return issued, fields.NativeSessionBinding, nil
}

func (service *Service) Prepare(ctx context.Context, bearer string, request nativesessionauth.PrepareRequest, signature []byte) (state.NativePrepared, error) {
	return service.domain.Prepare(ctx, bearer, request, signature)
}

func (service *Service) Authorize(ctx context.Context, bearer string, request nativesessionauth.AuthorizeRequest, signature []byte) (auth.IssuedNativeGrant, error) {
	return service.domain.Authorize(ctx, bearer, request, signature)
}

func (service *Service) Consume(ctx context.Context, bearer string, request nativesessionauth.ConsumeRequest, signature []byte) (state.NativeMaterialization, error) {
	return service.domain.Consume(ctx, bearer, request, signature)
}

func (service *Service) Status(ctx context.Context, bearer string, request nativesessionauth.StatusRequest, signature []byte) (state.NativeGrantStatus, error) {
	return service.domain.Status(ctx, bearer, request, signature)
}
