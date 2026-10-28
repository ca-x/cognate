package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ca-x/skillhub/internal/server/mcpaccess"
	"github.com/ca-x/skillhub/internal/server/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	Path            = "/mcp"
	MaxRequestBytes = 64 << 10
	MaxResultBytes  = 1 << 20
)

var (
	ErrClosed   = errors.New("MCP server is closed")
	ErrBusy     = errors.New("MCP capacity exhausted")
	ErrTooLarge = errors.New("MCP result exceeds size limit; request a smaller page")
)

type RepositoryLibrary interface {
	List(context.Context, string) ([]state.CollectedRepository, error)
	Get(context.Context, string, string) (state.CollectedRepository, error)
	Collect(context.Context, string, string, string) (state.CollectedRepository, error)
}

type requestContextKey struct{}

type Options struct {
	Access         *mcpaccess.Service
	Store          *state.Store
	Library        RepositoryLibrary
	PublicOrigin   string
	MaxConcurrent  int
	RequestTimeout time.Duration
}

type Server struct {
	access    *mcpaccess.Service
	store     *state.Store
	library   RepositoryLibrary
	handler   *mcp.StreamableHTTPHandler
	origin    *url.URL
	timeout   time.Duration
	lifetime  context.Context
	cancel    context.CancelFunc
	mutex     sync.Mutex
	closed    bool
	active    sync.WaitGroup
	httpSlots chan struct{}
	toolSlots chan struct{}
	closeOnce sync.Once
	drained   chan struct{}
}

func New(options Options) (*Server, error) {
	if options.Access == nil || options.Store == nil || options.Library == nil {
		return nil, errors.New("MCP requires credentials, storage, and repository library")
	}
	if options.MaxConcurrent == 0 {
		options.MaxConcurrent = 8
	}
	if options.RequestTimeout == 0 {
		options.RequestTimeout = 20 * time.Second
	}
	if options.MaxConcurrent < 1 || options.MaxConcurrent > 64 || options.RequestTimeout <= 0 || options.RequestTimeout > time.Minute {
		return nil, errors.New("invalid MCP bounds")
	}
	var origin *url.URL
	if options.PublicOrigin != "" {
		parsed, err := url.Parse(options.PublicOrigin)
		if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
			return nil, errors.New("invalid MCP public origin")
		}
		origin = parsed
	}
	lifetime, cancel := context.WithCancel(context.Background())
	server := &Server{access: options.Access, store: options.Store, library: options.Library, origin: origin, timeout: options.RequestTimeout, lifetime: lifetime, cancel: cancel, httpSlots: make(chan struct{}, options.MaxConcurrent), toolSlots: make(chan struct{}, options.MaxConcurrent), drained: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sdk := mcp.NewServer(&mcp.Implementation{Name: "skillhub", Version: "1"}, &mcp.ServerOptions{Logger: logger, Capabilities: &mcp.ServerCapabilities{}})
	server.addTools(sdk)
	server.handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return sdk }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: MaxRequestBytes, Logger: logger, PropagateRequestCancellation: true, DisableLocalhostProtection: origin != nil})
	return server, nil
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	if request.URL.Path != Path || request.URL.RawQuery != "" || !server.allowedOrigin(request) {
		http.Error(writer, "MCP request not permitted", http.StatusForbidden)
		return
	}
	ctx, done, err := server.begin(request.Context(), server.httpSlots)
	if err != nil {
		http.Error(writer, "MCP unavailable", http.StatusServiceUnavailable)
		return
	}
	defer done()
	control := http.NewResponseController(writer)
	deadline, _ := ctx.Deadline()
	_ = control.SetReadDeadline(deadline)
	_ = control.SetWriteDeadline(deadline)
	originalBody := request.Body
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(callbackDone)
		_ = control.SetReadDeadline(time.Now())
		_ = control.SetWriteDeadline(time.Now())
		if originalBody != nil {
			_ = originalBody.Close()
		}
	})
	defer func() {
		if !stop() {
			<-callbackDone
		}
		_ = control.SetReadDeadline(time.Time{})
		_ = control.SetWriteDeadline(time.Time{})
	}()
	if _, err := server.access.Authorize(ctx, bearer(request.Header), ""); err != nil {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(writer, "invalid MCP credential", http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "MCP requires POST", http.StatusMethodNotAllowed)
		return
	}
	if request.Body == nil {
		http.Error(writer, "MCP request body required", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, MaxRequestBytes))
	if err != nil {
		status := http.StatusBadRequest
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(writer, "MCP request body rejected", status)
		return
	}
	request = request.Clone(context.WithValue(ctx, requestContextKey{}, ctx))
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.Header.Del("Mcp-Session-Id")
	response := &boundedResponse{header: make(http.Header)}
	server.handler.ServeHTTP(response, request)
	if response.overflow {
		http.Error(writer, "MCP result exceeds size limit", http.StatusInternalServerError)
		return
	}
	if ctx.Err() != nil {
		http.Error(writer, "MCP request canceled", http.StatusGatewayTimeout)
		return
	}
	for name, values := range response.header {
		if !strings.EqualFold(name, "Mcp-Session-Id") {
			writer.Header()[name] = values
		}
	}
	writer.WriteHeader(response.statusCode())
	_, _ = writer.Write(response.body.Bytes())
}

func (server *Server) allowedOrigin(request *http.Request) bool {
	expected := ""
	if server.origin != nil {
		if !strings.EqualFold(request.Host, server.origin.Host) {
			return false
		}
		expected = server.origin.String()
	} else {
		host := request.Host
		if hostname, _, err := net.SplitHostPort(host); err == nil {
			host = hostname
		}
		host = strings.Trim(host, "[]")
		address, err := netip.ParseAddr(host)
		if !strings.EqualFold(host, "localhost") && (err != nil || !address.IsLoopback()) {
			return false
		}
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		expected = scheme + "://" + request.Host
	}
	origins := request.Header.Values("Origin")
	return len(origins) == 0 || len(origins) == 1 && origins[0] == expected
}

func (server *Server) begin(ctx context.Context, slots chan struct{}) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	server.mutex.Lock()
	defer server.mutex.Unlock()
	if server.closed {
		return nil, nil, ErrClosed
	}
	select {
	case slots <- struct{}{}:
	default:
		return nil, nil, ErrBusy
	}
	server.active.Add(1)
	ctx, cancel := context.WithTimeout(ctx, server.timeout)
	stop := context.AfterFunc(server.lifetime, cancel)
	return ctx, func() { stop(); cancel(); <-slots; server.active.Done() }, nil
}

func (server *Server) Close(ctx context.Context) error {
	server.closeOnce.Do(func() {
		server.mutex.Lock()
		server.closed = true
		server.cancel()
		server.mutex.Unlock()
		go func() { server.active.Wait(); close(server.drained) }()
	})
	select {
	case <-server.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

type boundedResponse struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	overflow bool
}

func (response *boundedResponse) Header() http.Header { return response.header }

func (response *boundedResponse) WriteHeader(status int) {
	if response.status == 0 {
		response.status = status
	}
}

func (response *boundedResponse) Write(data []byte) (int, error) {
	if response.body.Len()+len(data) > MaxResultBytes {
		response.overflow = true
		return 0, ErrTooLarge
	}
	response.WriteHeader(http.StatusOK)
	return response.body.Write(data)
}

func (response *boundedResponse) statusCode() int {
	if response.status == 0 {
		return http.StatusOK
	}
	return response.status
}
