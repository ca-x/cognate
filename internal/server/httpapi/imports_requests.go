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
	"github.com/ca-x/skillhub/gen/skillhub/import/v1/importv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const importMaxWireBytes = 2 << 20
const importSnapshotMaxWireBytes = 24 << 20
const importResponseGrace = time.Second

type importOwnerKey struct{}

func importLimits(procedure string) (int64, time.Duration) {
	if procedure == importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure {
		return importSnapshotMaxWireBytes, 15 * time.Second
	}
	return importMaxWireBytes, 5 * time.Second
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
		handler.mutex.Lock()
		closed := handler.closed
		handler.mutex.Unlock()
		if closed {
			reject(connect.NewError(connect.CodeUnavailable, errors.New("import preview is shutting down")))
			return
		}
		maxBytes, timeout := importLimits(request.URL.Path)
		ctx, cancel := context.WithTimeout(request.Context(), timeout)
		defer cancel()
		owner, err := handler.auth.authenticate(ctx, request.Header)
		if err != nil {
			reject(err)
			return
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
			reject(err)
			return
		}
		defer func() {
			handler.mutex.Lock()
			delete(handler.owners, owner)
			handler.mutex.Unlock()
			handler.active.Done()
		}()
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
		ctx = context.WithValue(ctx, importOwnerKey{}, owner)
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
	if codec.name == "json" {
		return protojson.Marshal(message)
	}
	return proto.Marshal(message)
}

func (codec importCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	if !ok {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid export request"))
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
