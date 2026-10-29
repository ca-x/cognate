package httpapi

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/server/logging"
)

func observeRequests(manager *logging.Manager, next http.Handler) http.Handler {
	if manager == nil {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		route := logRoute(request.URL.Path)
		_ = manager.Record(logging.Event{Kind: logging.EventRequestStarted, Route: route})
		observed := &logResponseWriter{ResponseWriter: writer}
		completed := false
		defer func() {
			status := observed.status
			if !completed {
				status = http.StatusInternalServerError
			} else if status == 0 {
				status = http.StatusOK
			}
			_ = manager.Record(logging.Event{Kind: logging.EventRequestCompleted, Route: route, Status: status, Duration: time.Since(started)})
		}()
		next.ServeHTTP(observed, request)
		completed = true
	})
}

type logResponseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *logResponseWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *logResponseWriter) WriteHeader(status int) {
	if writer.status == 0 && (status >= 200 || status == http.StatusSwitchingProtocols) {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *logResponseWriter) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.ResponseWriter.Write(data)
}

func (writer *logResponseWriter) FlushError() error {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *logResponseWriter) Flush() { _ = writer.FlushError() }

func (writer *logResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(writer.ResponseWriter).Hijack()
}

func (writer *logResponseWriter) Push(target string, options *http.PushOptions) error {
	if pusher, ok := writer.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

func logRoute(path string) logging.Route {
	if path == "/healthz" {
		return logging.RouteHealth
	}
	if path == "/mcp" {
		return logging.RouteMCP
	}
	for _, prefix := range []struct {
		path  string
		route logging.Route
	}{
		{"/skillhub.auth.v1.", logging.RouteAuth},
		{"/skillhub.catalog.v1.PublicCatalogService/", logging.RouteCatalog},
		{"/skillhub.catalog.v1.", logging.RouteCatalog},
		{"/skillhub.library.v1.", logging.RouteLibrary},
		{"/skillhub.pack.v1.DistributionService/", logging.RouteDistribution},
		{"/skillhub.pack.v1.", logging.RoutePacks},
		{"/skillhub.github.v1.", logging.RouteGitHub},
		{"/skillhub.import.v1.", logging.RouteImport},
		{"/skillhub.mcp.v1.", logging.RouteMCP},
		{"/skillhub.configuration.native.v1.", logging.RouteNativeConfiguration},
		{"/skillhub.configuration.v1.", logging.RouteConfiguration},
		{"/skillhub.management.v1.", logging.RouteManagement},
	} {
		if strings.HasPrefix(path, prefix.path) {
			return prefix.route
		}
	}
	if path == "/" || strings.HasPrefix(path, "/assets/") {
		return logging.RouteWeb
	}
	return logging.RouteOther
}
