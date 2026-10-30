package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
)

const configurationMaxRequestBytes = 128 << 10
const configurationMaxResponseBytes = 1 << 20

func configurationTransport(service configurationv1connect.ConfigurationServiceHandler) (string, http.Handler) {
	return configurationv1connect.NewConfigurationServiceHandler(service,
		connect.WithReadMaxBytes(configurationMaxRequestBytes),
		connect.WithSendMaxBytes(configurationMaxResponseBytes),
		connect.WithCodec(configurationCodec{name: "json"}),
		connect.WithCodec(configurationCodec{name: "proto"}),
	)
}

type configurationHandler struct {
	auth    *authHandler
	service *configuration.Service
	once    sync.Once
	slots   chan struct{}
}

func (handler *configurationHandler) ListAssets(ctx context.Context, request *connect.Request[configurationv1.ListAssetsRequest]) (*connect.Response[configurationv1.ListAssetsResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*configurationv1.ListAssetsResponse, error) {
		entries, err := handler.service.List(ctx, owner)
		if err != nil {
			return nil, err
		}
		response := &configurationv1.ListAssetsResponse{Assets: make([]*configurationv1.Asset, 0, len(entries))}
		for _, entry := range entries {
			asset, err := configurationAsset(entry)
			if err != nil {
				return nil, err
			}
			response.Assets = append(response.Assets, asset)
		}
		return response, nil
	})
}

func (handler *configurationHandler) GetAsset(ctx context.Context, request *connect.Request[configurationv1.GetAssetRequest]) (*connect.Response[configurationv1.GetAssetResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*configurationv1.GetAssetResponse, error) {
		entry, err := handler.service.Get(ctx, owner, request.Msg.Id)
		if err != nil {
			return nil, err
		}
		asset, err := configurationAsset(entry)
		return &configurationv1.GetAssetResponse{Asset: asset}, err
	})
}

func (handler *configurationHandler) ReadDraft(ctx context.Context, request *connect.Request[configurationv1.ReadDraftRequest]) (*connect.Response[configurationv1.ReadDraftResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*configurationv1.ReadDraftResponse, error) {
		entry, err := handler.service.Edit(ctx, owner, request.Msg.Id)
		if err != nil {
			return nil, err
		}
		view, err := configurationView(entry)
		return &configurationv1.ReadDraftResponse{View: view}, err
	})
}

func (handler *configurationHandler) CreateAsset(ctx context.Context, request *connect.Request[configurationv1.CreateAssetRequest]) (*connect.Response[configurationv1.CreateAssetResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*configurationv1.CreateAssetResponse, error) {
		draft, secrets, err := configurationInput(request.Msg.Draft, request.Msg.Secrets)
		if err != nil {
			return nil, err
		}
		defer configurationClearSecrets(secrets)
		entry, err := handler.service.Create(ctx, owner, draft, secrets)
		if err != nil {
			return nil, err
		}
		view, err := configurationView(entry)
		return &configurationv1.CreateAssetResponse{View: view}, err
	})
}

func (handler *configurationHandler) UpdateAsset(ctx context.Context, request *connect.Request[configurationv1.UpdateAssetRequest]) (*connect.Response[configurationv1.UpdateAssetResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*configurationv1.UpdateAssetResponse, error) {
		draft, secrets, err := configurationInput(request.Msg.Draft, request.Msg.Secrets)
		if err != nil {
			return nil, err
		}
		defer configurationClearSecrets(secrets)
		entry, err := handler.service.Update(ctx, owner, request.Msg.Id, request.Msg.ExpectedRevision, draft, secrets)
		if err != nil {
			return nil, err
		}
		view, err := configurationView(entry)
		return &configurationv1.UpdateAssetResponse{View: view}, err
	})
}

func (handler *configurationHandler) CopyAsset(ctx context.Context, request *connect.Request[configurationv1.CopyAssetRequest]) (*connect.Response[configurationv1.CopyAssetResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*configurationv1.CopyAssetResponse, error) {
		entry, err := handler.service.Copy(ctx, owner, request.Msg.SourceId, request.Msg.ExpectedSourceRevision, request.Msg.Name)
		if err != nil {
			return nil, err
		}
		view, err := configurationView(entry)
		return &configurationv1.CopyAssetResponse{View: view}, err
	})
}

func (handler *configurationHandler) DeleteAsset(ctx context.Context, request *connect.Request[configurationv1.DeleteAssetRequest]) (*connect.Response[configurationv1.DeleteAssetResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*configurationv1.DeleteAssetResponse, error) {
		err := handler.service.Delete(ctx, owner, request.Msg.Id, request.Msg.ExpectedRevision)
		return &configurationv1.DeleteAssetResponse{}, err
	})
}

func (handler *configurationHandler) localCall[Response any](ctx context.Context, header http.Header, operation func(context.Context, string) (*Response, error)) (*connect.Response[Response], error) {
	handler.once.Do(func() { handler.slots = make(chan struct{}, 8) })
	if err := contextError(ctx, ctx.Err()); err != nil {
		return nil, err
	}
	select {
	case handler.slots <- struct{}{}:
		defer func() { <-handler.slots }()
	default:
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("configuration capacity exhausted"))
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	owner, err := handler.auth.authenticate(ctx, header)
	if err != nil {
		return nil, err
	}
	response, err := operation(ctx, owner)
	if err != nil {
		return nil, configurationError(ctx, err)
	}
	if err := contextError(ctx, ctx.Err()); err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func configurationError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	for _, mapping := range []struct {
		err  error
		code connect.Code
	}{
		{configuration.ErrInvalid, connect.CodeInvalidArgument},
		{configuration.ErrUnsupported, connect.CodeFailedPrecondition},
		{configuration.ErrNotFound, connect.CodeNotFound},
		{configuration.ErrConflict, connect.CodeAborted},
		{configuration.ErrLimit, connect.CodeResourceExhausted},
	} {
		if errors.Is(err, mapping.err) {
			return connect.NewError(mapping.code, mapping.err)
		}
	}
	return connect.NewError(connect.CodeInternal, configuration.ErrUnavailable)
}
