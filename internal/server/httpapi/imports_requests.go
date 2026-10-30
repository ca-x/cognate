package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"github.com/ca-x/cognate/internal/server/importer"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const importMaxWireBytes = 2 << 20
const importSnapshotMaxWireBytes = 24 << 20
const importResponseGrace = time.Second

type importOwnerKey struct{}

func importSelectionMethod(procedure string) (importer.SelectionMethod, bool) {
	switch procedure {
	case importv1connect.ImportServicePreviewImportSelectionProcedure:
		return importer.SelectionPreviewMethod, true
	case importv1connect.ImportServiceCommitImportSelectionProcedure:
		return importer.SelectionCommitMethod, true
	case importv1connect.ImportServiceGetImportCommitStatusProcedure:
		return importer.SelectionStatusMethod, true
	default:
		return 0, false
	}
}

func importLimits(procedure string) (int64, time.Duration) {
	switch procedure {
	case importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure, importv1connect.ImportServicePreviewImportSelectionProcedure:
		return importSnapshotMaxWireBytes, 15 * time.Second
	case importv1connect.ImportServiceCommitImportSelectionProcedure:
		return importSnapshotMaxWireBytes, 120 * time.Second
	case importv1connect.ImportServiceGetImportCommitStatusProcedure:
		return 64 << 10, 5 * time.Second
	default:
		return importMaxWireBytes, 5 * time.Second
	}
}

func (handler *ImportHandler) admitImport(request *http.Request) (context.Context, func(), error) {
	handler.mutex.Lock()
	closed := handler.closed
	handler.mutex.Unlock()
	if closed {
		return nil, nil, connect.NewError(connect.CodeUnavailable, errors.New("import preview is shutting down"))
	}
	if method, selected := importSelectionMethod(request.URL.Path); selected && handler.selection != nil {
		call, err := handler.selection.Begin(request.Context(), method, bearer(request.Header))
		if err != nil {
			return nil, nil, importSelectionError(request.Context(), err)
		}
		admission := &importSelectionAdmission{call: call}
		return context.WithValue(call.Context(), importSelectionCallKey{}, admission), func() {
			clear(admission.ticket)
			admission.ticket = nil
			call.Release()
		}, nil
	}
	_, timeout := importLimits(request.URL.Path)
	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	owner, err := handler.auth.authenticate(ctx, request.Header)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if request.URL.Path != importv1connect.ImportServicePreviewLibraryExportProcedure && request.URL.Path != importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure {
		cancel()
		return nil, nil, connect.NewError(connect.CodeUnimplemented, errors.New("import method is not available"))
	}
	handler.mutex.Lock()
	if handler.closed {
		err = connect.NewError(connect.CodeUnavailable, errors.New("import preview is shutting down"))
	} else if _, exists := handler.owners[owner]; exists || len(handler.owners) >= 2 {
		err = connect.NewError(connect.CodeResourceExhausted, errors.New("import preview capacity exhausted"))
	} else {
		handler.owners[owner] = cancel
		handler.active.Add(1)
	}
	handler.mutex.Unlock()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return context.WithValue(ctx, importOwnerKey{}, owner), func() {
		handler.mutex.Lock()
		delete(handler.owners, owner)
		handler.mutex.Unlock()
		handler.active.Done()
		cancel()
	}, nil
}

func (handler *ImportHandler) Guard(next http.Handler) http.Handler {
	errorWriter := connect.NewErrorWriter()
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		rejected := false
		reject := func(err error) {
			rejected = true
			if request.ProtoMajor == 1 {
				writer.Header().Set("Connection", "close")
				_ = http.NewResponseController(writer).SetReadDeadline(time.Now())
			}
			_ = http.NewResponseController(writer).SetWriteDeadline(time.Now().Add(importResponseGrace))
			_ = errorWriter.Write(writer, request, err)
		}
		ctx, release, err := handler.admitImport(request)
		if err != nil {
			reject(err)
			return
		}
		defer release()
		maxBytes, _ := importLimits(request.URL.Path)
		for _, name := range []string{"Content-Encoding", "Grpc-Encoding", "Connect-Content-Encoding"} {
			values := request.Header.Values(name)
			if len(values) > 1 || len(values) == 1 && !strings.EqualFold(strings.TrimSpace(values[0]), "identity") {
				reject(connect.NewError(connect.CodeInvalidArgument, errors.New("export requires identity encoding")))
				return
			}
		}
		if request.ContentLength > maxBytes {
			reject(connect.NewError(connect.CodeResourceExhausted, errors.New("export size limit exceeded")))
			return
		}
		controller := http.NewResponseController(writer)
		deadline, _ := ctx.Deadline()
		if err := controller.SetReadDeadline(deadline); err != nil {
			reject(connect.NewError(connect.CodeUnavailable, errors.New("import preview transport unavailable")))
			return
		}
		if err := controller.SetWriteDeadline(deadline.Add(importResponseGrace)); err != nil {
			reject(connect.NewError(connect.CodeUnavailable, errors.New("import preview transport unavailable")))
			return
		}
		interrupted := make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			_ = controller.SetReadDeadline(time.Now())
			_ = controller.SetWriteDeadline(time.Now().Add(importResponseGrace))
			close(interrupted)
		})
		defer func() {
			if !stop() {
				<-interrupted
			}
			if request.ProtoMajor == 1 && rejected {
				_ = controller.SetReadDeadline(time.Now())
			} else {
				_ = controller.SetReadDeadline(time.Time{})
			}
		}()
		data, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxBytes))
		defer clear(data)
		if err != nil || ctx.Err() != nil {
			if canceled := contextError(ctx, ctx.Err()); canceled != nil {
				err = canceled
			} else if timeout, ok := errors.AsType[net.Error](err); ok && timeout.Timeout() {
				err = connect.NewError(connect.CodeDeadlineExceeded, errors.New("request deadline exceeded"))
			} else if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				err = connect.NewError(connect.CodeResourceExhausted, errors.New("export size limit exceeded"))
			} else {
				err = connect.NewError(connect.CodeInvalidArgument, errors.New("invalid export request"))
			}
			reject(err)
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(data))
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

type importCodec struct{ name string }

func (codec importCodec) Name() string { return codec.name }

func (codec importCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, errors.New("invalid export response")
	}
	maximum := importSelectionResponseLimit(message)
	if maximum != 0 && codec.name != "json" && proto.Size(message) > maximum {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("import response size limit exceeded"))
	}
	var data []byte
	var err error
	if codec.name == "json" {
		data, err = protojson.Marshal(message)
	} else {
		data, err = proto.Marshal(message)
	}
	if maximum != 0 && err != nil {
		clear(data)
		return nil, connect.NewError(connect.CodeInternal, errors.New("import response unavailable"))
	}
	if maximum != 0 && len(data) > maximum {
		clear(data)
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("import response size limit exceeded"))
	}
	return data, err
}

func importSelectionResponseLimit(message proto.Message) int {
	switch message.(type) {
	case *importv1.PreviewImportSelectionResponse, *importv1.CommitImportSelectionResponse:
		return 2 << 20
	case *importv1.GetImportCommitStatusResponse:
		return 128 << 10
	default:
		return 0
	}
}

func (codec importCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	if !ok {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid export request"))
	}
	switch message.(type) {
	case *importv1.PreviewImportSelectionRequest, *importv1.CommitImportSelectionRequest, *importv1.GetImportCommitStatusRequest:
		return importSelectionUnmarshal(data, message, codec.name)
	}
	var err error
	if codec.name == "json" {
		err = (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, message)
	} else {
		err = proto.Unmarshal(data, message)
	}
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid export request"))
	}
	return nil
}
