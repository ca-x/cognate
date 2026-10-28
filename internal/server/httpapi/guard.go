package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/ca-x/skillhub/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/skillhub/gen/skillhub/import/v1/importv1connect"
	"github.com/ca-x/skillhub/gen/skillhub/pack/v1/packv1connect"
	"github.com/labstack/echo/v4"
)

type requestBudget struct {
	mutex  sync.Mutex
	tokens float64
	last   time.Time
}

func (budget *requestBudget) allow() bool {
	budget.mutex.Lock()
	defer budget.mutex.Unlock()
	now := time.Now()
	budget.tokens = min(10, budget.tokens+now.Sub(budget.last).Seconds()/2)
	budget.last = now
	if budget.tokens < 1 {
		return false
	}
	budget.tokens--
	return true
}

func guardRequests(publicOrigin string) echo.MiddlewareFunc {
	budget := &requestBudget{tokens: 10, last: time.Now()}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) (result error) {
			defer func() {
				if recover() != nil {
					result = echo.NewHTTPError(http.StatusInternalServerError)
				}
			}()
			header := ctx.Response().Header()
			header.Set("Cache-Control", "no-store")
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("Referrer-Policy", "no-referrer")
			header.Set("X-Frame-Options", "DENY")
			request := ctx.Request()
			if publicOrigin == "" && !loopbackHost(request.Host) {
				return echo.NewHTTPError(http.StatusForbidden)
			}
			if origins := request.Header.Values("Origin"); len(origins) != 0 {
				expected := publicOrigin
				if expected == "" {
					scheme := "http"
					if request.TLS != nil {
						scheme = "https"
					}
					expected = scheme + "://" + request.Host
				}
				if len(origins) != 1 || origins[0] != expected {
					return echo.NewHTTPError(http.StatusForbidden)
				}
			}
			if request.URL.Path == authv1connect.AuthServiceRegisterProcedure || request.URL.Path == authv1connect.AuthServiceLoginProcedure || request.URL.Path == authv1connect.AuthServiceStartOIDCLoginProcedure {
				if !budget.allow() {
					header.Set("Retry-After", "2")
					header.Set("Content-Type", "application/json")
					return ctx.String(http.StatusTooManyRequests, `{"code":"resource_exhausted","message":"authentication rate limit exceeded"}`)
				}
			}
			var maxBytes int64 = 4096
			if request.URL.Path == importv1connect.ImportServicePreviewLibraryExportProcedure {
				maxBytes = importMaxWireBytes
			}
			if request.URL.Path == importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure {
				maxBytes = importSnapshotMaxWireBytes
			}
			if request.URL.Path == packv1connect.PackServiceCreatePackProcedure || request.URL.Path == packv1connect.PackServiceUpdatePackProcedure {
				maxBytes = 64 << 10
			}
			request.Body = http.MaxBytesReader(ctx.Response(), request.Body, maxBytes)
			return next(ctx)
		}
	}
}

func loopbackHost(value string) bool {
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	if strings.EqualFold(value, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(value)
	return err == nil && address.IsLoopback()
}
