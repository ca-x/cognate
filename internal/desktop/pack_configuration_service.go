package desktop

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"reflect"
	"sync"
	"time"

	"connectrpc.com/connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/v1/packv1connect"
	"github.com/ca-x/cognate/internal/packattachments"
)

type packConfigurationBackend interface {
	OpenPackConfigurations(string, string) (*packConfigurationConnection, error)
}

type packConfigurationPack struct {
	ID, Name string
	Revision int64
}
type packConfigurationCatalog struct {
	packattachments.Identity
	Packs  []packConfigurationPack
	Assets []configurationAsset
}
type packConfigurationConnection struct {
	private func() *packConfigurationConnectionState
}
type packConfigurationConnectionState struct {
	mutex                  sync.Mutex
	closed                 bool
	workers                sync.WaitGroup
	life                   context.Context
	cancel                 context.CancelFunc
	client                 *packattachments.Client
	service                *Service
	transport              *http.Transport
	origin, session, owner string
	operation              *packattachments.Operation
}

func (service *Service) OpenPackConfigurations(origin, session string) (*packConfigurationConnection, error) {
	canonical, err := ValidateOrigin(origin)
	if err != nil || canonical != origin {
		return nil, packattachments.ErrInvalid
	}
	var settings *http.Transport
	if service.HTTP != nil && service.HTTP.Transport != nil {
		var ok bool
		settings, ok = service.HTTP.Transport.(*http.Transport)
		if !ok {
			return nil, packattachments.ErrInvalid
		}
	}
	client, err := packattachments.New(origin, session, settings)
	if err != nil {
		return nil, err
	}
	configuration := &tls.Config{MinVersion: tls.VersionTLS12}
	if settings != nil && settings.TLSClientConfig != nil && settings.TLSClientConfig.RootCAs != nil {
		configuration.RootCAs = settings.TLSClientConfig.RootCAs.Clone()
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSClientConfig: configuration, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 20 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 2, MaxConnsPerHost: 1, MaxResponseHeaderBytes: 4096, DisableCompression: true}
	life, cancel := context.WithCancel(context.Background())
	state := &packConfigurationConnectionState{life: life, cancel: cancel, client: client, service: &Service{HTTP: &http.Client{Transport: transport}}, transport: transport, origin: origin, session: session}
	return &packConfigurationConnection{private: func() *packConfigurationConnectionState { return state }}, nil
}

func (connection *packConfigurationConnection) start(caller context.Context) (context.Context, *packConfigurationConnectionState, func(), error) {
	state := connection.private()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed || state.life.Err() != nil {
		return nil, nil, nil, packattachments.ErrClosed
	}
	ctx, cancel := context.WithTimeout(caller, 60*time.Second)
	done := make(chan struct{})
	stop := context.AfterFunc(state.life, func() { cancel(); close(done) })
	if state.life.Err() != nil {
		cancel()
	}
	state.workers.Add(1)
	return ctx, state, func() {
		cancel()
		if !stop() {
			<-done
		}
		state.workers.Done()
	}, nil
}

func (connection *packConfigurationConnection) stop() { connection.private().cancel() }
func (connection *packConfigurationConnection) Close() {
	state := connection.private()
	state.mutex.Lock()
	state.closed = true
	state.cancel()
	state.mutex.Unlock()
	state.workers.Wait()
	state.client.Close()
	connection.closeReview()
	state.mutex.Lock()
	state.session, state.owner = "", ""
	state.mutex.Unlock()
	state.transport.CloseIdleConnections()
}

func (connection *packConfigurationConnection) closeReview() {
	state := connection.private()
	state.mutex.Lock()
	operation := state.operation
	state.operation = nil
	state.mutex.Unlock()
	if operation != nil {
		operation.Close()
	}
}

func (connection *packConfigurationConnection) catalog(caller context.Context) (packConfigurationCatalog, error) {
	ctx, state, release, err := connection.start(caller)
	if err != nil {
		return packConfigurationCatalog{}, err
	}
	defer release()
	identity, err := state.service.ConfigurationIdentity(ctx, state.origin, state.session)
	if err != nil {
		return packConfigurationCatalog{}, err
	}
	defer identity.clear()
	owner, err := state.client.Identity(ctx)
	if err != nil {
		return packConfigurationCatalog{}, err
	}
	if owner.OwnerID != identity.OwnerID || owner.Origin != identity.Origin {
		return packConfigurationCatalog{}, packattachments.ErrDenied
	}
	state.mutex.Lock()
	state.owner = owner.OwnerID
	state.mutex.Unlock()
	request := connect.NewRequest(&packv1.ListPacksRequest{})
	if err := identity.authorize(request.Header()); err != nil {
		return packConfigurationCatalog{}, err
	}
	rpc := packv1connect.NewPackServiceClient(state.service.client(state.origin), state.origin, configurationOptions()...)
	response, err := rpc.ListPacks(ctx, request)
	if err != nil {
		return packConfigurationCatalog{}, configurationFailure(ctx, err)
	}
	if response == nil || !configurationKnown(response.Msg) || len(response.Msg.Packs) > 100 {
		return packConfigurationCatalog{}, packattachments.ErrInvalid
	}
	catalog := packConfigurationCatalog{Identity: owner, Packs: make([]packConfigurationPack, 0, len(response.Msg.Packs))}
	seen := map[string]bool{}
	for _, pack := range response.Msg.Packs {
		if pack == nil || !configurationID(pack.Id) || !configurationText(pack.Name, 128) || pack.Revision < 1 || seen[pack.Id] {
			return packConfigurationCatalog{}, packattachments.ErrInvalid
		}
		seen[pack.Id] = true
		catalog.Packs = append(catalog.Packs, packConfigurationPack{ID: pack.Id, Name: pack.Name, Revision: pack.Revision})
	}
	catalog.Assets, err = state.service.ConfigurationAssets(ctx, identity)
	if err != nil {
		return packConfigurationCatalog{}, err
	}
	fresh, err := state.client.Identity(ctx)
	if err != nil {
		return packConfigurationCatalog{}, err
	}
	if fresh != owner {
		return packConfigurationCatalog{}, packattachments.ErrDenied
	}
	return catalog, ctx.Err()
}

func (connection *packConfigurationConnection) get(caller context.Context, pack string) (packattachments.Snapshot, error) {
	ctx, state, release, err := connection.start(caller)
	if err != nil {
		return packattachments.Snapshot{}, err
	}
	defer release()
	return state.client.Get(ctx, pack)
}

func (connection *packConfigurationConnection) prepare(caller context.Context, before packattachments.Snapshot, refs []packattachments.Reference) (packattachments.Review, error) {
	ctx, state, release, err := connection.start(caller)
	if err != nil {
		return packattachments.Review{}, err
	}
	defer release()
	connection.closeReview()
	operation, err := state.client.Prepare(ctx, before.PackID, before.PackRevision, before.ConfigurationRevision, refs)
	if err != nil {
		return packattachments.Review{}, err
	}
	review, err := operation.Review()
	if err != nil || !reflect.DeepEqual(review.Before, before) {
		operation.Close()
		return packattachments.Review{}, packattachments.ErrConflict
	}
	state.mutex.Lock()
	state.operation = operation
	state.mutex.Unlock()
	return review, ctx.Err()
}

func (connection *packConfigurationConnection) submit(caller context.Context, confirmation string) (packattachments.Snapshot, error) {
	ctx, state, release, err := connection.start(caller)
	if err != nil {
		return packattachments.Snapshot{}, err
	}
	defer release()
	state.mutex.Lock()
	operation := state.operation
	state.operation = nil
	state.mutex.Unlock()
	if operation == nil {
		return packattachments.Snapshot{}, packattachments.ErrClosed
	}
	defer operation.Close()
	review, err := operation.Review()
	if err != nil || confirmation != review.Confirmation {
		return packattachments.Snapshot{}, packattachments.ErrClosed
	}
	return operation.Submit(ctx)
}
