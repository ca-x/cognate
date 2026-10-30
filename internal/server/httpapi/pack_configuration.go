package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	packconfigurationv1 "github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1/packconfigurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/state"
)

const packConfigurationMaxBytes = 64 << 10

type packConfigurationOwnerKey struct{}

type packConfigurationHandler struct {
	auth    *authHandler
	store   *state.Store
	service *packs.Service
	slots   chan struct{}
}

func packConfigurationTransport(handler *packConfigurationHandler) (string, http.Handler) {
	handler.slots = make(chan struct{}, 8)
	path, transport := packconfigurationv1connect.NewPackConfigurationServiceHandler(handler,
		connect.WithReadMaxBytes(packConfigurationMaxBytes), connect.WithSendMaxBytes(packConfigurationMaxBytes),
		connect.WithCodec(packConfigurationCodec{name: "proto"}), connect.WithCodec(packConfigurationCodec{name: "json"}))
	return path, handler.requests(transport)
}

func packConfigurationProcedure(path string) bool {
	return path == packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure || path == packconfigurationv1connect.PackConfigurationServiceReplaceAttachmentsProcedure
}

func (handler *packConfigurationHandler) requests(next http.Handler) http.Handler {
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
		}
		if !packConfigurationProcedure(request.URL.Path) {
			reject(connect.NewError(connect.CodeUnimplemented, errors.New("unknown pack configuration method")))
			return
		}
		select {
		case handler.slots <- struct{}{}:
			defer func() { <-handler.slots }()
		default:
			reject(connect.NewError(connect.CodeResourceExhausted, errors.New("pack configuration capacity exhausted")))
			return
		}
		ctx, release, err := handler.service.BeginRequest(request.Context())
		if err != nil {
			reject(packConfigurationError(request.Context(), err))
			return
		}
		defer release()
		owner, err := handler.auth.authenticate(ctx, request.Header)
		if err != nil {
			reject(err)
			return
		}
		if request.Method != http.MethodPost {
			reject(connect.NewError(connect.CodeUnimplemented, errors.New("pack configuration requires POST")))
			return
		}
		for _, name := range []string{"Content-Encoding", "Grpc-Encoding", "Connect-Content-Encoding"} {
			values := request.Header.Values(name)
			if len(values) != 0 && (len(values) != 1 || values[0] != "identity") {
				reject(connect.NewError(connect.CodeInvalidArgument, errors.New("pack configuration requires identity encoding")))
				return
			}
		}
		if request.ContentLength > packConfigurationMaxBytes {
			reject(connect.NewError(connect.CodeResourceExhausted, errors.New("pack configuration size limit exceeded")))
			return
		}
		deadline, _ := ctx.Deadline()
		if controller.SetReadDeadline(deadline) != nil || controller.SetWriteDeadline(deadline.Add(time.Second)) != nil {
			reject(connect.NewError(connect.CodeUnavailable, errors.New("pack configuration transport unavailable")))
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
			if rejected && request.ProtoMajor == 1 {
				_ = controller.SetReadDeadline(time.Now())
			} else {
				_ = controller.SetReadDeadline(time.Time{})
			}
		}()
		body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, packConfigurationMaxBytes))
		defer clear(body)
		if err != nil || ctx.Err() != nil {
			failure := contextError(ctx, ctx.Err())
			if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() && !time.Now().Before(deadline) {
				failure = connect.NewError(connect.CodeDeadlineExceeded, errors.New("pack configuration deadline exceeded"))
			}
			if failure == nil {
				if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
					failure = connect.NewError(connect.CodeResourceExhausted, errors.New("pack configuration size limit exceeded"))
				} else if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() {
					failure = connect.NewError(connect.CodeDeadlineExceeded, errors.New("pack configuration deadline exceeded"))
				} else {
					failure = connect.NewError(connect.CodeInvalidArgument, errors.New("invalid pack configuration request"))
				}
			}
			reject(failure)
			return
		}
		request = request.Clone(context.WithValue(ctx, packConfigurationOwnerKey{}, owner))
		request.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(writer, request)
	})
}

func (handler *packConfigurationHandler) owner(ctx context.Context, header http.Header) (string, error) {
	admitted, _ := ctx.Value(packConfigurationOwnerKey{}).(string)
	if admitted == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("invalid owner session"))
	}
	owner, err := handler.auth.authenticate(ctx, header)
	if err != nil {
		return "", err
	}
	if owner != admitted {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("invalid owner session"))
	}
	return owner, nil
}

func (handler *packConfigurationHandler) GetAttachments(ctx context.Context, request *connect.Request[packconfigurationv1.GetAttachmentsRequest]) (*connect.Response[packconfigurationv1.GetAttachmentsResponse], error) {
	owner, err := handler.owner(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	snapshot, err := handler.store.PackConfigurationAttachments(ctx, owner, request.Msg.PackId)
	if err != nil {
		return nil, packConfigurationError(ctx, err)
	}
	message, err := packConfigurationSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&packconfigurationv1.GetAttachmentsResponse{Snapshot: message}), nil
}

func (handler *packConfigurationHandler) ReplaceAttachments(ctx context.Context, request *connect.Request[packconfigurationv1.ReplaceAttachmentsRequest]) (*connect.Response[packconfigurationv1.ReplaceAttachmentsResponse], error) {
	owner, err := handler.owner(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if len(request.Msg.References) > state.MaxPackConfigurationAttachments {
		return nil, packConfigurationError(ctx, state.ErrPackLimit)
	}
	refs := make([]state.PackConfigurationReference, 0, len(request.Msg.References))
	for _, ref := range request.Msg.References {
		if ref == nil {
			return nil, packConfigurationError(ctx, state.ErrPackInvalid)
		}
		refs = append(refs, state.PackConfigurationReference{AssetID: ref.AssetId, AssetRevision: ref.ExpectedAssetRevision, TargetID: ref.TargetId})
	}
	snapshot, err := handler.store.ReplacePackConfigurationAttachments(ctx, owner, request.Msg.PackId, request.Msg.ExpectedPackRevision, request.Msg.ExpectedConfigurationRevision, refs)
	if err != nil {
		return nil, packConfigurationError(ctx, err)
	}
	message, err := packConfigurationSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&packconfigurationv1.ReplaceAttachmentsResponse{Snapshot: message}), nil
}

func packConfigurationSnapshot(snapshot state.PackConfigurationSnapshot) (*packconfigurationv1.AttachmentSnapshot, error) {
	result := &packconfigurationv1.AttachmentSnapshot{PackId: snapshot.PackID, PackRevision: snapshot.PackRevision, ConfigurationRevision: snapshot.ConfigurationRevision, SnapshotDigest: bytes.Clone(snapshot.Digest[:])}
	for _, entry := range snapshot.Entries {
		asset, err := configurationAsset(state.ConfigurationAsset{Kind: entry.Kind, SchemaVersion: entry.SchemaVersion})
		if err != nil {
			return nil, configurationError(context.Background(), configuration.ErrUnavailable)
		}
		var status packconfigurationv1.AttachmentState
		switch entry.State {
		case state.PackConfigurationCurrent:
			status = packconfigurationv1.AttachmentState_ATTACHMENT_STATE_CURRENT
		case state.PackConfigurationStaleAsset:
			status = packconfigurationv1.AttachmentState_ATTACHMENT_STATE_STALE
		case state.PackConfigurationUnsupported:
			status = packconfigurationv1.AttachmentState_ATTACHMENT_STATE_UNSUPPORTED
		default:
			return nil, configurationError(context.Background(), configuration.ErrUnavailable)
		}
		result.Entries = append(result.Entries, &packconfigurationv1.AttachmentEntry{AttachmentId: entry.ID, AssetId: entry.AssetID, AssetRevision: entry.AssetRevision, AgentId: entry.AgentID, Kind: asset.Kind, SchemaVersion: asset.SchemaVersion, TargetId: entry.TargetID, Ordinal: uint32(entry.Ordinal), State: status})
	}
	return result, nil
}

func packConfigurationError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	for _, mapping := range []struct {
		source error
		code   connect.Code
	}{
		{state.ErrPackNotFound, connect.CodeNotFound}, {state.ErrConfigurationNotFound, connect.CodeNotFound},
		{state.ErrPackInvalid, connect.CodeInvalidArgument}, {state.ErrConfigurationInvalid, connect.CodeInvalidArgument},
		{state.ErrPackConflict, connect.CodeAborted}, {state.ErrConfigurationConflict, connect.CodeAborted},
		{state.ErrPackLimit, connect.CodeResourceExhausted}, {state.ErrConfigurationLimit, connect.CodeResourceExhausted},
		{state.ErrPackConfigurationUnsupported, connect.CodeFailedPrecondition}, {state.ErrMCPForbidden, connect.CodePermissionDenied},
		{packs.ErrClosed, connect.CodeUnavailable},
	} {
		if errors.Is(err, mapping.source) {
			return connect.NewError(mapping.code, errors.New("pack configuration request refused"))
		}
	}
	return connect.NewError(connect.CodeUnavailable, errors.New("pack configuration unavailable"))
}
