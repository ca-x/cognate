package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1/mcpv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type MCPConfigurationHandler struct {
	auth   *authHandler
	store  *state.Store
	mutex  sync.Mutex
	closed bool
	active map[*mcpConfigurationRequest]context.CancelFunc
	joined sync.WaitGroup
}

type mcpConfigurationRequest struct {
	store   *mcpRequestStore
	service *configuration.MCPService
}

type mcpConfigurationContextKey struct{}

func NewMCPConfigurationHandler(authentication *auth.Service, store *state.Store) (*MCPConfigurationHandler, error) {
	if authentication == nil || store == nil {
		return nil, errors.New("MCP configuration requires authentication and storage")
	}
	return &MCPConfigurationHandler{auth: &authHandler{service: authentication}, store: store, active: make(map[*mcpConfigurationRequest]context.CancelFunc)}, nil
}

func (handler *MCPConfigurationHandler) Close() error {
	handler.mutex.Lock()
	handler.closed = true
	for _, cancel := range handler.active {
		cancel()
	}
	handler.mutex.Unlock()
	handler.joined.Wait()
	return nil
}

func (handler *MCPConfigurationHandler) begin(ctx context.Context) (context.Context, *mcpConfigurationRequest, func(), error) {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	if handler.closed {
		return nil, nil, nil, connect.NewError(connect.CodeUnavailable, errors.New("MCP configuration closed"))
	}
	if len(handler.active) >= 8 {
		return nil, nil, nil, connect.NewError(connect.CodeResourceExhausted, errors.New("MCP configuration capacity exhausted"))
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	request := new(mcpConfigurationRequest)
	handler.active[request] = cancel
	handler.joined.Add(1)
	return ctx, request, func() {
		cancel()
		handler.mutex.Lock()
		delete(handler.active, request)
		handler.mutex.Unlock()
		handler.joined.Done()
	}, nil
}

func mcpConfigurationProcedure(path string) bool {
	switch path {
	case mcpv1connect.MCPConfigurationServiceListAssetsProcedure,
		mcpv1connect.MCPConfigurationServiceGetAssetProcedure,
		mcpv1connect.MCPConfigurationServiceReadDraftProcedure,
		mcpv1connect.MCPConfigurationServiceCreateAssetProcedure,
		mcpv1connect.MCPConfigurationServiceUpdateAssetProcedure,
		mcpv1connect.MCPConfigurationServiceCopyAssetProcedure,
		mcpv1connect.MCPConfigurationServiceDeleteAssetProcedure,
		mcpv1connect.MCPConfigurationServicePreviewProcedure:
		return true
	default:
		return false
	}
}

func (handler *MCPConfigurationHandler) transport() (string, http.Handler) {
	path, transport := mcpv1connect.NewMCPConfigurationServiceHandler(handler,
		connect.WithReadMaxBytes(mcpConfigurationMaxBytes), connect.WithSendMaxBytes(mcpConfigurationMaxBytes),
		connect.WithCodec(mcpConfigurationCodec{name: "proto"}), connect.WithCodec(mcpConfigurationCodec{name: "json"}))
	return path, handler.requests(transport)
}

func (handler *MCPConfigurationHandler) requests(next http.Handler) http.Handler {
	errorWriter := connect.NewErrorWriter()
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		controller := http.NewResponseController(writer)
		rejected := false
		reject := func(err error) {
			rejected = true
			if request.ProtoMajor == 1 {
				writer.Header().Set("Connection", "close")
			}
			_ = controller.SetReadDeadline(time.Now())
			_ = controller.SetWriteDeadline(time.Now().Add(time.Second))
			_ = errorWriter.Write(writer, request, err)
			_ = controller.Flush()
		}
		if !mcpConfigurationProcedure(request.URL.Path) || request.Method != http.MethodPost {
			reject(connect.NewError(connect.CodeUnimplemented, errors.New("unknown MCP configuration method")))
			return
		}
		ctx, admitted, release, err := handler.begin(request.Context())
		if err != nil {
			reject(err)
			return
		}
		defer release()
		owner, err := handler.auth.authenticate(ctx, request.Header)
		if err != nil {
			reject(mcpConfigurationAuthError(ctx, err))
			return
		}
		for _, name := range []string{"Content-Encoding", "Grpc-Encoding", "Connect-Content-Encoding"} {
			values := request.Header.Values(name)
			if len(values) != 0 && (len(values) != 1 || values[0] != "identity") {
				reject(connect.NewError(connect.CodeInvalidArgument, errors.New("MCP configuration requires identity encoding")))
				return
			}
		}
		if request.ContentLength > mcpConfigurationMaxBytes {
			reject(connect.NewError(connect.CodeResourceExhausted, errors.New("MCP configuration capacity exhausted")))
			return
		}
		deadline, _ := ctx.Deadline()
		if controller.SetReadDeadline(deadline) != nil || controller.SetWriteDeadline(deadline.Add(time.Second)) != nil {
			reject(connect.NewError(connect.CodeUnavailable, errors.New("MCP configuration transport unavailable")))
			return
		}
		drained := make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			_ = controller.SetReadDeadline(time.Now())
			_ = controller.SetWriteDeadline(time.Now().Add(time.Second))
			close(drained)
		})
		defer func() {
			if !stop() {
				<-drained
			}
			if !rejected || request.ProtoMajor != 1 {
				_ = controller.SetReadDeadline(time.Time{})
			}
			if !rejected && ctx.Err() == nil {
				_ = controller.SetWriteDeadline(time.Time{})
			}
		}()
		body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, mcpConfigurationMaxBytes))
		defer clear(body)
		if err != nil || ctx.Err() != nil {
			failure := mcpConfigurationError(ctx, ctx.Err())
			if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() && !time.Now().Before(deadline) {
				failure = connect.NewError(connect.CodeDeadlineExceeded, errors.New("MCP configuration deadline exceeded"))
			} else if ctx.Err() == nil {
				switch {
				case err == nil:
				case errors.Is(err, context.Canceled):
					failure = mcpConfigurationError(ctx, context.Canceled)
				default:
					if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
						failure = connect.NewError(connect.CodeResourceExhausted, errors.New("MCP configuration capacity exhausted"))
					} else if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() {
						failure = connect.NewError(connect.CodeDeadlineExceeded, errors.New("MCP configuration deadline exceeded"))
					} else {
						failure = connect.NewError(connect.CodeInvalidArgument, errors.New("invalid MCP configuration body"))
					}
				}
			}
			reject(failure)
			return
		}
		headers := request.Header.Clone()
		defer clear(headers)
		proxy := &mcpRequestStore{store: handler.store, owner: owner}
		proxy.authorize = func(ctx context.Context) error {
			current, err := handler.auth.authenticate(ctx, headers)
			if err != nil {
				if connect.CodeOf(err) == connect.CodeUnauthenticated {
					proxy.failure.Store(1)
				} else {
					proxy.failure.Store(2)
				}
				return err
			}
			if current != owner {
				proxy.failure.Store(1)
				return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid MCP owner session"))
			}
			return nil
		}
		admitted.store, admitted.service = proxy, configuration.NewMCP(proxy)
		request = request.Clone(context.WithValue(ctx, mcpConfigurationContextKey{}, admitted))
		request.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(writer, request)
		_ = controller.Flush()
	})
}

func mcpConfigurationCall[Output any](ctx context.Context, operation func(*configuration.MCPService, string) (*Output, error)) (*connect.Response[Output], error) {
	admitted, _ := ctx.Value(mcpConfigurationContextKey{}).(*mcpConfigurationRequest)
	if admitted == nil || admitted.service == nil || admitted.store == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid MCP owner session"))
	}
	if err := ctx.Err(); err != nil {
		return nil, mcpConfigurationError(ctx, err)
	}
	message, err := operation(admitted.service, admitted.store.owner)
	if ctx.Err() != nil {
		return nil, mcpConfigurationError(ctx, ctx.Err())
	}
	if tail := admitted.store.authorize(ctx); tail != nil || admitted.store.failure.Load() != 0 {
		if admitted.store.failure.Load() == 1 {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid MCP owner session"))
		}
		return nil, mcpConfigurationError(ctx, tail)
	}
	if err != nil || message == nil {
		return nil, mcpConfigurationError(ctx, err)
	}
	if ctx.Err() != nil {
		return nil, mcpConfigurationError(ctx, ctx.Err())
	}
	return connect.NewResponse(message), nil
}

func (handler *MCPConfigurationHandler) ListAssets(ctx context.Context, request *connect.Request[mcpv1.ListAssetsRequest]) (*connect.Response[mcpv1.ListAssetsResponse], error) {
	return mcpConfigurationCall(ctx, func(service *configuration.MCPService, owner string) (*mcpv1.ListAssetsResponse, error) {
		assets, err := service.List(ctx, owner)
		if err != nil {
			return nil, err
		}
		result := &mcpv1.ListAssetsResponse{Assets: []*configurationv1.Asset{}}
		for _, asset := range assets {
			metadata, err := configurationAsset(asset)
			if err != nil {
				return nil, err
			}
			result.Assets = append(result.Assets, metadata)
		}
		return result, nil
	})
}

func (handler *MCPConfigurationHandler) GetAsset(ctx context.Context, request *connect.Request[mcpv1.GetAssetRequest]) (*connect.Response[mcpv1.GetAssetResponse], error) {
	return mcpConfigurationCall(ctx, func(service *configuration.MCPService, owner string) (*mcpv1.GetAssetResponse, error) {
		asset, err := service.Get(ctx, owner, request.Msg.Id)
		if err != nil {
			return nil, err
		}
		metadata, err := configurationAsset(asset)
		return &mcpv1.GetAssetResponse{Asset: metadata}, err
	})
}

func (handler *MCPConfigurationHandler) ReadDraft(ctx context.Context, request *connect.Request[mcpv1.ReadDraftRequest]) (*connect.Response[mcpv1.ReadDraftResponse], error) {
	return mcpConfigurationCall(ctx, func(service *configuration.MCPService, owner string) (*mcpv1.ReadDraftResponse, error) {
		view, err := service.ReadDraft(ctx, owner, request.Msg.Id)
		if err != nil {
			return nil, err
		}
		if view.Metadata.SchemaVersion != 1 || view.Metadata.AgentID != "claude" || view.HasPreservedExtras {
			return nil, configuration.ErrUnsupported
		}
		mapped, err := mcpConfigurationView(view)
		return &mcpv1.ReadDraftResponse{View: mapped}, err
	})
}

func (handler *MCPConfigurationHandler) CreateAsset(ctx context.Context, request *connect.Request[mcpv1.CreateAssetRequest]) (*connect.Response[mcpv1.CreateAssetResponse], error) {
	defer mcpConfigurationWipe(request.Msg.ProtoReflect())
	return mcpConfigurationCall(ctx, func(service *configuration.MCPService, owner string) (*mcpv1.CreateAssetResponse, error) {
		draft, mutations, err := mcpConfigurationInput(request.Msg.Draft, request.Msg.Secrets)
		defer mcpConfigurationClear(mutations)
		if err != nil {
			return nil, err
		}
		view, err := service.Create(ctx, owner, draft, mutations)
		if err != nil {
			return nil, err
		}
		mapped, err := mcpConfigurationView(view)
		return &mcpv1.CreateAssetResponse{View: mapped}, err
	})
}

func (handler *MCPConfigurationHandler) UpdateAsset(ctx context.Context, request *connect.Request[mcpv1.UpdateAssetRequest]) (*connect.Response[mcpv1.UpdateAssetResponse], error) {
	defer mcpConfigurationWipe(request.Msg.ProtoReflect())
	return mcpConfigurationCall(ctx, func(service *configuration.MCPService, owner string) (*mcpv1.UpdateAssetResponse, error) {
		draft, mutations, err := mcpConfigurationInput(request.Msg.Draft, request.Msg.Secrets)
		defer mcpConfigurationClear(mutations)
		if err != nil {
			return nil, err
		}
		view, err := service.Update(ctx, owner, request.Msg.Id, request.Msg.ExpectedRevision, draft, mutations)
		if err != nil {
			return nil, err
		}
		mapped, err := mcpConfigurationView(view)
		return &mcpv1.UpdateAssetResponse{View: mapped}, err
	})
}

func (handler *MCPConfigurationHandler) CopyAsset(ctx context.Context, request *connect.Request[mcpv1.CopyAssetRequest]) (*connect.Response[mcpv1.CopyAssetResponse], error) {
	return mcpConfigurationCall(ctx, func(service *configuration.MCPService, owner string) (*mcpv1.CopyAssetResponse, error) {
		view, err := service.Copy(ctx, owner, request.Msg.Id, request.Msg.ExpectedSourceRevision, request.Msg.Name)
		if err != nil {
			return nil, err
		}
		mapped, err := mcpConfigurationView(view)
		return &mcpv1.CopyAssetResponse{View: mapped}, err
	})
}

func (handler *MCPConfigurationHandler) DeleteAsset(ctx context.Context, request *connect.Request[mcpv1.DeleteAssetRequest]) (*connect.Response[mcpv1.DeleteAssetResponse], error) {
	return mcpConfigurationCall(ctx, func(service *configuration.MCPService, owner string) (*mcpv1.DeleteAssetResponse, error) {
		err := service.Delete(ctx, owner, request.Msg.Id, request.Msg.ExpectedRevision)
		return &mcpv1.DeleteAssetResponse{}, err
	})
}

func (handler *MCPConfigurationHandler) Preview(ctx context.Context, request *connect.Request[mcpv1.PreviewRequest]) (*connect.Response[mcpv1.PreviewResponse], error) {
	defer mcpConfigurationWipe(request.Msg.ProtoReflect())
	return mcpConfigurationCall(ctx, func(service *configuration.MCPService, owner string) (*mcpv1.PreviewResponse, error) {
		draft, mutations, err := mcpConfigurationInput(request.Msg.Draft, request.Msg.Secrets)
		defer mcpConfigurationClear(mutations)
		if err != nil {
			return nil, err
		}
		preview, err := service.Preview(ctx, owner, request.Msg.Id, request.Msg.ExpectedRevision, draft, mutations)
		if err != nil {
			return nil, err
		}
		return mcpConfigurationPreview(preview)
	})
}

func mcpConfigurationAuthError(ctx context.Context, err error) error {
	if failure := contextError(ctx, err); failure != nil {
		return failure
	}
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated, connect.CodeCanceled, connect.CodeDeadlineExceeded:
		return err
	default:
		return connect.NewError(connect.CodeUnavailable, errors.New("MCP configuration unavailable"))
	}
}

func mcpConfigurationError(ctx context.Context, err error) error {
	if ctx != nil {
		if failure := contextError(ctx, err); failure != nil {
			return failure
		}
	}
	for _, entry := range []struct {
		cause error
		code  connect.Code
	}{
		{configuration.ErrInvalid, connect.CodeInvalidArgument},
		{configuration.ErrUnsupported, connect.CodeFailedPrecondition},
		{configuration.ErrNotFound, connect.CodeNotFound},
		{configuration.ErrConflict, connect.CodeAborted},
		{configuration.ErrLimit, connect.CodeResourceExhausted},
		{configuration.ErrInUse, connect.CodeFailedPrecondition},
	} {
		if errors.Is(err, entry.cause) {
			return connect.NewError(entry.code, entry.cause)
		}
	}
	return connect.NewError(connect.CodeUnavailable, errors.New("MCP configuration unavailable"))
}
