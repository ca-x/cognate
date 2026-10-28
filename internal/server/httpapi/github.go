package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	githubv1 "github.com/ca-x/skillhub/gen/skillhub/github/v1"
	"github.com/ca-x/skillhub/internal/github"
	"github.com/ca-x/skillhub/internal/server/githubauth"
	"github.com/ca-x/skillhub/internal/server/state"
)

type githubHandler struct {
	auth   *authHandler
	store  *state.Store
	client *http.Client
	device *githubauth.Service
	slots  chan struct{}
}

func (handler *githubHandler) ConnectToken(ctx context.Context, request *connect.Request[githubv1.ConnectTokenRequest]) (*connect.Response[githubv1.ConnectTokenResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	token, err := github.NewToken(request.Msg.AccessToken, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid GitHub token"))
	}
	ctx, done, err := handler.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	client, err := github.NewListsClient(token, handler.client)
	if err != nil {
		return nil, githubError(ctx, err)
	}
	snapshot, err := client.Lists(ctx)
	if err != nil {
		if canceled := contextError(ctx, err); canceled != nil {
			return nil, canceled
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("GitHub authorization or list verification failed"))
	}
	generation, err := handler.store.PutCredential(ctx, user, snapshot.ViewerID, request.Msg.ExpectedGeneration, token)
	if err != nil {
		return nil, githubError(ctx, err)
	}
	return connect.NewResponse(&githubv1.ConnectTokenResponse{CredentialGeneration: generation, ViewerId: snapshot.ViewerID}), nil
}

func (handler *githubHandler) GetCatalog(ctx context.Context, request *connect.Request[githubv1.GetCatalogRequest]) (*connect.Response[githubv1.GetCatalogResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	catalog, err := handler.catalog(ctx, user)
	if err != nil {
		return nil, githubError(ctx, err)
	}
	return connect.NewResponse(&githubv1.GetCatalogResponse{Catalog: catalog}), nil
}

func (handler *githubHandler) Sync(ctx context.Context, request *connect.Request[githubv1.SyncRequest]) (*connect.Response[githubv1.SyncResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	ctx, done, err := handler.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	credential, err := handler.store.Credential(ctx, user)
	if err != nil {
		return nil, githubError(ctx, err)
	}
	client, err := github.NewListsClient(credential.Token, handler.client)
	if err != nil {
		return nil, githubError(ctx, err)
	}
	ticket, err := handler.store.BeginListSync(ctx, user, credential.Generation)
	if err != nil {
		return nil, githubError(ctx, err)
	}
	result, err := handler.store.ReadListSync(ctx, ticket, client)
	if err != nil {
		if canceled := contextError(ctx, err); canceled != nil {
			return nil, canceled
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("GitHub synchronization failed; retry synchronization"))
	}
	if err := handler.store.ApplyListSync(ctx, result); err != nil {
		return nil, githubError(ctx, err)
	}
	catalog, err := handler.catalog(ctx, user)
	if err != nil {
		return nil, githubError(ctx, err)
	}
	return connect.NewResponse(&githubv1.SyncResponse{Catalog: catalog}), nil
}

func (handler *githubHandler) SetListSubscription(ctx context.Context, request *connect.Request[githubv1.SetListSubscriptionRequest]) (*connect.Response[githubv1.SetListSubscriptionResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	id := request.Msg.ListId
	if len(id) == 0 || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n") {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid list identifier"))
	}
	if err := handler.store.SetListSubscription(ctx, user, id, request.Msg.Subscribed); err != nil {
		return nil, githubError(ctx, err)
	}
	return connect.NewResponse(&githubv1.SetListSubscriptionResponse{}), nil
}

func (handler *githubHandler) begin(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, contextError(ctx, err)
	}
	select {
	case handler.slots <- struct{}{}:
		ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		return ctx, func() { cancel(); <-handler.slots }, nil
	default:
		return nil, nil, connect.NewError(connect.CodeResourceExhausted, errors.New("GitHub synchronization capacity exhausted"))
	}
}

func (handler *githubHandler) catalog(ctx context.Context, user string) (*githubv1.Catalog, error) {
	snapshot, err := handler.store.GitHubCatalog(ctx, user)
	if errors.Is(err, state.ErrCredentialNotFound) {
		return &githubv1.Catalog{DeviceFlowEnabled: handler.device != nil}, nil
	}
	if errors.Is(err, state.ErrCredentialUnavailable) {
		credential, lookupErr := handler.store.Credential(ctx, user)
		if lookupErr != nil {
			return nil, lookupErr
		}
		return &githubv1.Catalog{ViewerId: credential.ViewerID, CredentialGeneration: credential.Generation, DeviceFlowEnabled: handler.device != nil, AuthorizationRequired: true}, nil
	}
	if err != nil {
		return nil, err
	}
	catalog := &githubv1.Catalog{Connected: true, ViewerId: snapshot.ViewerID, CredentialGeneration: snapshot.Generation, ValidUntilUnix: snapshot.ValidUntil.Unix(), DeviceFlowEnabled: handler.device != nil, Lists: make([]*githubv1.List, 0, len(snapshot.Lists)), Repositories: make([]*githubv1.Repository, 0, len(snapshot.Repositories))}
	for _, list := range snapshot.Lists {
		catalog.Lists = append(catalog.Lists, &githubv1.List{Id: list.ID, Name: list.Name, Slug: list.Slug, Private: list.Private, Subscribed: list.Subscribed, UpdatedAtUnix: list.UpdatedAt.Unix(), LastAddedAtUnix: list.LastAddedAt.Unix(), VerifiedAtUnix: list.VerifiedAt.Unix()})
	}
	for _, repo := range snapshot.Repositories {
		catalog.Repositories = append(catalog.Repositories, &githubv1.Repository{Id: repo.ID, NameWithOwner: repo.NameWithOwner, DatabaseId: repo.DatabaseID, Private: repo.Private})
	}
	return catalog, nil
}

func githubError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	if errors.Is(err, state.ErrCredentialConflict) || errors.Is(err, state.ErrSyncSuperseded) {
		return connect.NewError(connect.CodeAborted, errors.New("GitHub state changed; reload and retry"))
	}
	if errors.Is(err, state.ErrCredentialNotFound) || errors.Is(err, state.ErrCredentialUnavailable) || errors.Is(err, github.ErrTokenExpired) || errors.Is(err, github.ErrTokenInvalidated) {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("connect GitHub before continuing"))
	}
	if errors.Is(err, state.ErrObservationExpired) {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("synchronize GitHub lists before continuing"))
	}
	return connect.NewError(connect.CodeInternal, errors.New("GitHub catalog unavailable"))
}
