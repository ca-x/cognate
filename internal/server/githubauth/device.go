package githubauth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ca-x/skillhub/internal/github"
	"github.com/ca-x/skillhub/internal/server/state"
)

var (
	ErrNotFound   = errors.New("device authorization not found")
	ErrInProgress = errors.New("device authorization already in progress")
	ErrBusy       = errors.New("device authorization capacity exhausted")
	ErrClosed     = errors.New("device authorization service closed")
)

type State uint8

const (
	Pending State = iota + 1
	Complete
	Denied
	Expired
	Canceled
	Failed
	Superseded
)

type Authorization struct {
	ID              string
	State           State
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
	Generation      string
	ViewerID        string
}

func (Authorization) String() string   { return "[redacted device authorization]" }
func (Authorization) GoString() string { return "[redacted device authorization]" }

type attempt struct {
	mutex    sync.Mutex
	result   Authorization
	state    atomic.Uint32
	cancel   context.CancelFunc
	retireAt time.Time
}

type Service struct {
	mutex    sync.Mutex
	closed   bool
	attempts map[string]*attempt
	workers  sync.WaitGroup
	store    CredentialStore
	flow     *github.DeviceFlow
	client   *http.Client
}

type CredentialStore interface {
	Credential(context.Context, string) (state.Credential, error)
	PutCredential(context.Context, string, string, string, github.Token) (string, error)
}

func (*Service) String() string   { return "[redacted device authorization service]" }
func (*Service) GoString() string { return "[redacted device authorization service]" }

func NewService(store CredentialStore, clientID string, client *http.Client) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("server state is required")
	}
	flow, err := github.NewDeviceFlow(clientID, client)
	if err != nil {
		return nil, err
	}
	return &Service{store: store, flow: flow, client: client, attempts: make(map[string]*attempt)}, nil
}

func (service *Service) Start(ctx context.Context, user, expectedGeneration string, private bool) (Authorization, error) {
	if err := ctx.Err(); err != nil {
		return Authorization{}, err
	}
	service.mutex.Lock()
	if service.closed {
		service.mutex.Unlock()
		return Authorization{}, ErrClosed
	}
	for user, previous := range service.attempts {
		if time.Now().After(previous.retireAt) {
			previous.cancel()
			delete(service.attempts, user)
		}
	}
	if previous := service.attempts[user]; previous != nil {
		pending := previous.currentState() == Pending
		if pending {
			service.mutex.Unlock()
			return Authorization{}, ErrInProgress
		}
		delete(service.attempts, user)
	}
	if len(service.attempts) >= 64 {
		service.evictTerminal()
		if len(service.attempts) >= 64 {
			service.mutex.Unlock()
			return Authorization{}, ErrBusy
		}
	}
	flowCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	current := &attempt{result: Authorization{ID: rand.Text(), ExpiresAt: time.Now().Add(15 * time.Minute)}, cancel: cancel, retireAt: time.Now().Add(20 * time.Minute)}
	current.state.Store(uint32(Pending))
	service.attempts[user] = current
	service.workers.Add(1)
	service.mutex.Unlock()
	defer service.workers.Done()
	startCtx, cancelStart := context.WithTimeout(ctx, 10*time.Second)
	stop := context.AfterFunc(flowCtx, cancelStart)
	defer stop()
	defer cancelStart()
	credential, err := service.store.Credential(startCtx, user)
	if err != nil && !errors.Is(err, state.ErrCredentialNotFound) {
		service.finish(current, err)
		return Authorization{}, err
	}
	if credential.Generation != expectedGeneration {
		service.finish(current, state.ErrCredentialConflict)
		return Authorization{}, state.ErrCredentialConflict
	}
	authorization, session, err := service.flow.Start(startCtx, private)
	if err != nil {
		service.finish(current, err)
		if ctx.Err() != nil {
			return Authorization{}, ctx.Err()
		}
		return Authorization{}, fmt.Errorf("GitHub device authorization could not start")
	}
	current.mutex.Lock()
	defer current.mutex.Unlock()
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if service.closed || ctx.Err() != nil || flowCtx.Err() != nil || current.currentState() != Pending {
		current.state.Store(uint32(Canceled))
		cancel()
		if ctx.Err() != nil {
			return Authorization{}, ctx.Err()
		}
		return Authorization{}, ErrClosed
	}
	current.result.UserCode = authorization.UserCode
	current.result.VerificationURI = authorization.VerificationURI
	if authorization.ExpiresAt.Before(current.result.ExpiresAt) {
		current.result.ExpiresAt = authorization.ExpiresAt
	}
	service.workers.Go(func() { service.poll(flowCtx, current, session, user, expectedGeneration) })
	return publicResult(current.result, current.currentState()), nil
}

func (service *Service) evictTerminal() {
	for user, current := range service.attempts {
		terminal := current.currentState() != Pending
		if terminal {
			current.cancel()
			delete(service.attempts, user)
			return
		}
	}
}

func (service *Service) find(user, id string) (*attempt, error) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if service.closed {
		return nil, ErrClosed
	}
	current := service.attempts[user]
	if current == nil || id != "" && current.result.ID != id || time.Now().After(current.retireAt) {
		return nil, ErrNotFound
	}
	return current, nil
}

func (service *Service) Get(user, id string) (Authorization, error) {
	current, err := service.find(user, id)
	if err != nil {
		return Authorization{}, err
	}
	current.mutex.Lock()
	defer current.mutex.Unlock()
	if current.currentState() == Pending && !time.Now().Before(current.result.ExpiresAt) {
		current.state.Store(uint32(Expired))
		current.cancel()
	}
	return publicResult(current.result, current.currentState()), nil
}

func (service *Service) Cancel(user, id string) (Authorization, error) {
	current, err := service.find(user, id)
	if err != nil {
		return Authorization{}, err
	}
	current.cancel()
	current.mutex.Lock()
	defer current.mutex.Unlock()
	if current.currentState() == Pending {
		current.state.Store(uint32(Canceled))
		current.cancel()
	}
	return publicResult(current.result, current.currentState()), nil
}

func (current *attempt) currentState() State { return State(current.state.Load()) }

func publicResult(result Authorization, status State) Authorization {
	result.State = status
	if result.State != Pending {
		result.UserCode = ""
		result.VerificationURI = ""
	}
	return result
}

func (service *Service) poll(ctx context.Context, current *attempt, session *github.DeviceSession, user, expectedGeneration string) {
	defer current.cancel()
	token, err := session.Wait(ctx)
	if err != nil {
		service.finish(current, err)
		return
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	client, err := github.NewListsClient(token, service.client)
	if err != nil {
		service.finish(current, err)
		return
	}
	snapshot, err := client.Lists(verifyCtx)
	if err != nil {
		service.finish(current, err)
		return
	}
	current.mutex.Lock()
	defer current.mutex.Unlock()
	if current.currentState() != Pending {
		return
	}
	if ctx.Err() != nil || !time.Now().Before(current.result.ExpiresAt) {
		current.state.Store(uint32(Expired))
		return
	}
	generation, err := service.store.PutCredential(verifyCtx, user, snapshot.ViewerID, expectedGeneration, token)
	if err != nil {
		current.state.Store(uint32(resultState(err)))
		return
	}
	current.result.Generation, current.result.ViewerID = generation, snapshot.ViewerID
	current.state.Store(uint32(Complete))
}

func (service *Service) finish(current *attempt, err error) {
	current.mutex.Lock()
	defer current.mutex.Unlock()
	if current.currentState() == Pending {
		current.state.Store(uint32(resultState(err)))
	}
	current.cancel()
}

func resultState(err error) State {
	switch {
	case errors.Is(err, context.Canceled):
		return Canceled
	case errors.Is(err, github.ErrDeviceExpired), errors.Is(err, context.DeadlineExceeded):
		return Expired
	case errors.Is(err, github.ErrDeviceDenied):
		return Denied
	case errors.Is(err, state.ErrCredentialConflict):
		return Superseded
	default:
		return Failed
	}
}

func (service *Service) Close() {
	service.mutex.Lock()
	service.closed = true
	for current := range maps.Values(service.attempts) {
		current.cancel()
	}
	service.mutex.Unlock()
	service.workers.Wait()
}
