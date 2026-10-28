package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	packv1 "github.com/ca-x/skillhub/gen/skillhub/pack/v1"
	"github.com/ca-x/skillhub/internal/server/packs"
)

type distributionHandler struct{ service *packs.Service }

func (handler *distributionHandler) GetManifest(ctx context.Context, request *connect.Request[packv1.GetManifestRequest]) (*connect.Response[packv1.GetManifestResponse], error) {
	manifest, err := handler.service.Manifest(ctx, bearer(request.Header()))
	if err != nil {
		return nil, packError(ctx, err)
	}
	return connect.NewResponse(&packv1.GetManifestResponse{FormatVersion: 1, PackId: manifest.PackID, Name: manifest.Name, Description: manifest.Description, Revision: manifest.Revision, ManifestHash: manifest.Hash, Skills: packSkills(manifest.Skills)}), nil
}

func (handler *distributionHandler) DownloadSkill(ctx context.Context, request *connect.Request[packv1.DownloadSkillRequest], stream *connect.ServerStream[packv1.DownloadSkillResponse]) error {
	err := handler.service.Download(ctx, bearer(request.Header()), request.Msg.SkillId, request.Msg.ContentHash, request.Msg.PackRevision, func(chunk packs.Chunk) error {
		return stream.Send(&packv1.DownloadSkillResponse{Offset: chunk.Offset, Data: chunk.Data, Final: chunk.Final, TransferSha256: chunk.SHA256, ContentHash: chunk.ContentHash})
	})
	if err != nil {
		return packError(ctx, err)
	}
	return nil
}

type distributionLifetime interface {
	BeginRequest(context.Context) (context.Context, func(), error)
}

func distributionRequests(service distributionLifetime, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, done, err := service.BeginRequest(request.Context())
		if err != nil {
			http.Error(writer, "distribution unavailable", http.StatusServiceUnavailable)
			return
		}
		defer done()
		transport := &distributionTransport{controller: http.NewResponseController(writer), lifetime: ctx}
		defer transport.close()
		if err := transport.watch(ctx); err != nil {
			http.Error(writer, "distribution transport unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx = context.WithValue(ctx, distributionTransportKey{}, transport)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

type distributionTransportKey struct{}

type distributionTransport struct {
	controller *http.ResponseController
	lifetime   context.Context
	mutex      sync.Mutex
	watches    []distributionWatch
}

type distributionWatch struct {
	stop     func() bool
	finished chan struct{}
}

func (transport *distributionTransport) watch(ctx context.Context) error {
	if err := transport.applyDeadline(ctx); err != nil {
		return err
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = transport.applyDeadline(ctx)
		close(finished)
	})
	transport.watches = append(transport.watches, distributionWatch{stop: stop, finished: finished})
	return nil
}

func (transport *distributionTransport) applyDeadline(ctx context.Context) error {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	deadline, _ := ctx.Deadline()
	if transport.lifetime.Err() != nil || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		deadline = time.Now()
	} else if errors.Is(ctx.Err(), context.Canceled) {
		deadline, _ = transport.lifetime.Deadline()
	}
	if err := transport.controller.SetReadDeadline(deadline); err != nil {
		return err
	}
	return transport.controller.SetWriteDeadline(deadline)
}

func (transport *distributionTransport) close() {
	for _, watch := range transport.watches {
		if !watch.stop() {
			<-watch.finished
		}
	}
	_ = transport.controller.SetReadDeadline(time.Time{})
	_ = transport.controller.SetWriteDeadline(time.Time{})
}

type distributionDeadlines struct{}

func (distributionDeadlines) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		if err := bindDistributionDeadline(ctx); err != nil {
			return nil, err
		}
		return next(ctx, request)
	}
}

func (distributionDeadlines) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (distributionDeadlines) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, stream connect.StreamingHandlerConn) error {
		if err := bindDistributionDeadline(ctx); err != nil {
			return err
		}
		return next(ctx, stream)
	}
}

func bindDistributionDeadline(ctx context.Context) error {
	transport, ok := ctx.Value(distributionTransportKey{}).(*distributionTransport)
	if !ok {
		return connect.NewError(connect.CodeUnavailable, packs.ErrClosed)
	}
	if err := transport.watch(ctx); err != nil {
		return connect.NewError(connect.CodeUnavailable, packs.ErrClosed)
	}
	return nil
}
