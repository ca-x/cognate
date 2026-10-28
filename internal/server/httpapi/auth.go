package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/skillhub/gen/skillhub/auth/v1"
	"github.com/ca-x/skillhub/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/skillhub/gen/skillhub/github/v1/githubv1connect"
	"github.com/ca-x/skillhub/gen/skillhub/import/v1/importv1connect"
	"github.com/ca-x/skillhub/gen/skillhub/library/v1/libraryv1connect"
	"github.com/ca-x/skillhub/gen/skillhub/mcp/v1/mcpv1connect"
	"github.com/ca-x/skillhub/gen/skillhub/pack/v1/packv1connect"
	"github.com/ca-x/skillhub/internal/server/auth"
	"github.com/ca-x/skillhub/internal/server/githubauth"
	"github.com/ca-x/skillhub/internal/server/library"
	"github.com/ca-x/skillhub/internal/server/mcpaccess"
	"github.com/ca-x/skillhub/internal/server/mcpserver"
	"github.com/ca-x/skillhub/internal/server/oidclogin"
	"github.com/ca-x/skillhub/internal/server/packs"
	"github.com/ca-x/skillhub/internal/server/publiccatalog"
	"github.com/ca-x/skillhub/internal/server/state"
	"github.com/labstack/echo/v4"
)

type authHandler struct {
	service *auth.Service
	oidc    *oidclogin.Manager
}

func New(service *auth.Service) http.Handler {
	return NewWithOptions(service, Options{})
}

type Options struct {
	PublicOrigin  string
	Store         *state.Store
	GitHubClient  *http.Client
	WebUI         http.Handler
	GitHubDevice  *githubauth.Service
	OIDC          *oidclogin.Manager
	Library       *library.Service
	Imports       *ImportHandler
	Packs         *packs.Service
	MCPAccess     *mcpaccess.Service
	MCP           *mcpserver.Server
	PublicCatalog *publiccatalog.Service
}

func NewWithOptions(service *auth.Service, options Options) http.Handler {
	if service == nil {
		panic("authentication service is required")
	}
	server := echo.New()
	server.HideBanner = true
	server.HidePort = true
	server.HTTPErrorHandler = func(err error, ctx echo.Context) {
		status := http.StatusInternalServerError
		if failure, ok := errors.AsType[*echo.HTTPError](err); ok && failure.Code >= 400 && failure.Code <= 599 {
			status = failure.Code
		}
		if !ctx.Response().Committed {
			_ = ctx.NoContent(status)
		}
	}
	server.Use(guardRequests(options.PublicOrigin))
	server.GET("/healthz", func(ctx echo.Context) error { return ctx.String(http.StatusOK, "ok\n") })
	authentication := &authHandler{service: service, oidc: options.OIDC}
	path, handler := authv1connect.NewAuthServiceHandler(authentication, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(4096))
	server.Any(path+"*", echo.WrapHandler(handler))
	if options.Store != nil {
		path, handler := githubv1connect.NewGitHubServiceHandler(&githubHandler{auth: authentication, store: options.Store, client: options.GitHubClient, device: options.GitHubDevice, slots: make(chan struct{}, 2)}, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(8<<20))
		server.Any(path+"*", echo.WrapHandler(handler))
	}
	if options.OIDC != nil {
		server.GET(oidclogin.CallbackPath, echo.WrapHandler(http.HandlerFunc(authentication.oidcCallback)))
	}
	if options.MCPAccess != nil {
		path, handler := mcpv1connect.NewMCPManagementServiceHandler(&mcpManagementHandler{auth: authentication, access: options.MCPAccess}, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(1<<20))
		server.Any(path+"*", echo.WrapHandler(handler))
	}
	if options.Library != nil {
		path, handler := libraryv1connect.NewLibraryServiceHandler(&libraryHandler{auth: authentication, service: options.Library}, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(8<<20))
		server.Any(path+"*", echo.WrapHandler(handler))
	}
	if options.Imports != nil {
		path, handler := importv1connect.NewImportServiceHandler(options.Imports, connect.WithReadMaxBytes(importMaxWireBytes), connect.WithSendMaxBytes(importMaxWireBytes), connect.WithCompression("gzip", nil, nil), connect.WithCodec(importCodec{name: "json"}), connect.WithCodec(importCodec{name: "proto"}))
		server.Any(path+"*", echo.WrapHandler(options.Imports.Guard(handler)))
	}
	if options.Packs != nil && options.Store != nil {
		path, handler := packv1connect.NewPackServiceHandler(&packHandler{auth: authentication, store: options.Store, service: options.Packs}, connect.WithReadMaxBytes(64<<10), connect.WithSendMaxBytes(8<<20))
		server.Any(path+"*", echo.WrapHandler(handler))
		path, handler = packv1connect.NewDistributionServiceHandler(&distributionHandler{service: options.Packs}, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(8<<20), connect.WithInterceptors(distributionDeadlines{}))
		server.Any(path+"*", echo.WrapHandler(distributionRequests(options.Packs, handler)))
	}
	registerCatalog(server, authentication, options)
	if options.WebUI != nil {
		server.Any("/*", echo.WrapHandler(options.WebUI))
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if options.MCP != nil && request.URL.Path == mcpserver.Path {
			options.MCP.ServeHTTP(writer, request)
			return
		}
		server.ServeHTTP(writer, request)
	})
}

func (handler *authHandler) Register(ctx context.Context, request *connect.Request[authv1.RegisterRequest]) (*connect.Response[authv1.RegisterResponse], error) {
	password := []byte(request.Msg.Password)
	defer clear(password)
	user, err := handler.service.Register(ctx, request.Msg.Username, password)
	if canceled := contextError(ctx, err); canceled != nil {
		return nil, canceled
	}
	if errors.Is(err, auth.ErrAccountExists) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("account already exists"))
	}
	if errors.Is(err, auth.ErrInvalidInput) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account could not be created"))
	}
	if errors.Is(err, auth.ErrBusy) {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("authentication capacity exhausted"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication unavailable"))
	}
	return connect.NewResponse(&authv1.RegisterResponse{UserId: user}), nil
}

func (handler *authHandler) Login(ctx context.Context, request *connect.Request[authv1.LoginRequest]) (*connect.Response[authv1.LoginResponse], error) {
	password := []byte(request.Msg.Password)
	defer clear(password)
	token, err := handler.service.Login(ctx, request.Msg.Username, password)
	if canceled := contextError(ctx, err); canceled != nil {
		return nil, canceled
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	}
	if errors.Is(err, auth.ErrBusy) {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("authentication capacity exhausted"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication unavailable"))
	}
	return connect.NewResponse(&authv1.LoginResponse{SessionToken: token.Reveal(), ExpiresAtUnix: token.ExpiresAt().Unix()}), nil
}

func (handler *authHandler) GetSession(ctx context.Context, request *connect.Request[authv1.GetSessionRequest]) (*connect.Response[authv1.GetSessionResponse], error) {
	user, err := handler.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv1.GetSessionResponse{UserId: user}), nil
}

func (handler *authHandler) Logout(ctx context.Context, request *connect.Request[authv1.LogoutRequest]) (*connect.Response[authv1.LogoutResponse], error) {
	if _, err := handler.authenticate(ctx, request.Header()); err != nil {
		return nil, err
	}
	if err := handler.service.Logout(ctx, bearer(request.Header())); err != nil {
		if canceled := contextError(ctx, err); canceled != nil {
			return nil, canceled
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("authentication unavailable"))
	}
	return connect.NewResponse(&authv1.LogoutResponse{}), nil
}

func (handler *authHandler) authenticate(ctx context.Context, header http.Header) (string, error) {
	user, err := handler.service.Authenticate(ctx, bearer(header))
	if canceled := contextError(ctx, err); canceled != nil {
		return "", canceled
	}
	if errors.Is(err, auth.ErrInvalidSession) {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, errors.New("authentication unavailable"))
	}
	return user, nil
}

func contextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, errors.New("request canceled"))
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("request deadline exceeded"))
	}
	return nil
}

func bearer(header http.Header) string {
	values := header.Values("Authorization")
	if len(values) != 1 {
		return ""
	}
	value, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok {
		return ""
	}
	return value
}
