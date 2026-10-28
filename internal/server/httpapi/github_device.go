package httpapi

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	githubv1 "github.com/ca-x/skillhub/gen/skillhub/github/v1"
	"github.com/ca-x/skillhub/internal/server/githubauth"
)

func (handler *githubHandler) StartDeviceAuthorization(ctx context.Context, request *connect.Request[githubv1.StartDeviceAuthorizationRequest]) (*connect.Response[githubv1.StartDeviceAuthorizationResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if handler.device == nil {
		return nil, deviceNotConfigured()
	}
	authorization, err := handler.device.Start(ctx, user, request.Msg.ExpectedGeneration, request.Msg.RequestUserScope)
	if err != nil {
		return nil, deviceError(ctx, err)
	}
	return connect.NewResponse(&githubv1.StartDeviceAuthorizationResponse{Authorization: deviceMessage(authorization)}), nil
}

func (handler *githubHandler) GetDeviceAuthorization(ctx context.Context, request *connect.Request[githubv1.GetDeviceAuthorizationRequest]) (*connect.Response[githubv1.GetDeviceAuthorizationResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if handler.device == nil {
		return nil, deviceNotConfigured()
	}
	authorization, err := handler.device.Get(user, request.Msg.AuthorizationId)
	if err != nil {
		return nil, deviceError(ctx, err)
	}
	return connect.NewResponse(&githubv1.GetDeviceAuthorizationResponse{Authorization: deviceMessage(authorization)}), nil
}

func (handler *githubHandler) CancelDeviceAuthorization(ctx context.Context, request *connect.Request[githubv1.CancelDeviceAuthorizationRequest]) (*connect.Response[githubv1.CancelDeviceAuthorizationResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if handler.device == nil {
		return nil, deviceNotConfigured()
	}
	authorization, err := handler.device.Cancel(user, request.Msg.AuthorizationId)
	if err != nil {
		return nil, deviceError(ctx, err)
	}
	return connect.NewResponse(&githubv1.CancelDeviceAuthorizationResponse{Authorization: deviceMessage(authorization)}), nil
}

func deviceNotConfigured() error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New("GitHub device flow is not configured"))
}

func deviceError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	switch {
	case errors.Is(err, githubauth.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("device authorization not found"))
	case errors.Is(err, githubauth.ErrInProgress):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("device authorization already in progress"))
	case errors.Is(err, githubauth.ErrBusy):
		return connect.NewError(connect.CodeResourceExhausted, errors.New("device authorization capacity exhausted"))
	case errors.Is(err, githubauth.ErrClosed):
		return connect.NewError(connect.CodeUnavailable, errors.New("device authorization unavailable"))
	}
	return githubError(ctx, err)
}

func deviceMessage(value githubauth.Authorization) *githubv1.DeviceAuthorization {
	return &githubv1.DeviceAuthorization{Id: value.ID, State: githubv1.DeviceAuthorizationState(value.State), UserCode: value.UserCode, VerificationUri: value.VerificationURI, ExpiresAtUnix: value.ExpiresAt.Unix(), CredentialGeneration: value.Generation, ViewerId: value.ViewerID}
}
