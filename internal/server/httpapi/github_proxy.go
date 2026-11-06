package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"connectrpc.com/connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/internal/server/githubproxysettings"
)

type GitHubProxySettingsBackend interface {
	Get(context.Context) (githubproxysettings.Snapshot, error)
	Update(context.Context, int64, string) (githubproxysettings.Snapshot, error)
}

type githubProxyManagementHandler struct {
	auth    *authHandler
	backend GitHubProxySettingsBackend
	admins  []string
}

func (handler *githubProxyManagementHandler) administrator(ctx context.Context, header http.Header) error {
	owner, err := handler.auth.authenticate(ctx, header)
	if err != nil {
		return err
	}
	if !slices.Contains(handler.admins, owner) {
		return connect.NewError(connect.CodePermissionDenied, errors.New("GitHub proxy administration requires an authorized account"))
	}
	return nil
}

func (handler *githubProxyManagementHandler) GetGitHubProxySettings(ctx context.Context, request *connect.Request[managementv1.GetGitHubProxySettingsRequest]) (*connect.Response[managementv1.GetGitHubProxySettingsResponse], error) {
	if err := handler.administrator(ctx, request.Header()); err != nil {
		return nil, err
	}
	snapshot, err := handler.backend.Get(ctx)
	if err != nil {
		return nil, githubProxyManagementError(ctx, err)
	}
	response := connect.NewResponse(&managementv1.GetGitHubProxySettingsResponse{Settings: githubProxyView(snapshot)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (handler *githubProxyManagementHandler) UpdateGitHubProxySettings(ctx context.Context, request *connect.Request[managementv1.UpdateGitHubProxySettingsRequest]) (*connect.Response[managementv1.UpdateGitHubProxySettingsResponse], error) {
	defer func() { request.Msg.ProxyUrl = "" }()
	if err := handler.administrator(ctx, request.Header()); err != nil {
		return nil, err
	}
	snapshot, err := handler.backend.Update(ctx, request.Msg.ExpectedRevision, request.Msg.ProxyUrl)
	if err != nil {
		return nil, githubProxyManagementError(ctx, err)
	}
	response := connect.NewResponse(&managementv1.UpdateGitHubProxySettingsResponse{Settings: githubProxyView(snapshot)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func githubProxyView(snapshot githubproxysettings.Snapshot) *managementv1.GitHubProxySettings {
	return &managementv1.GitHubProxySettings{Revision: snapshot.Revision, Configured: snapshot.Configured, Endpoint: snapshot.Endpoint}
}

func githubProxyManagementError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	if errors.Is(err, githubproxysettings.ErrInvalid) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid GitHub forward proxy settings"))
	}
	if errors.Is(err, githubproxysettings.ErrConflict) {
		return connect.NewError(connect.CodeAborted, errors.New("GitHub proxy settings changed; reload before saving"))
	}
	return connect.NewError(connect.CodeUnavailable, errors.New("GitHub proxy settings unavailable; reload to check the saved state"))
}
