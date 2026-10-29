package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/native/v1/configurationnativev1connect"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/nativeconsumer"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

const nativeServicePath = "/skillhub.configuration.native.v1.NativeConfigurationService/"

type nativeConfigurationHandler struct {
	auth    *authHandler
	service *nativeconsumer.Service
}

func nativeConfigurationTransport(handler *nativeConfigurationHandler) (string, http.Handler) {
	path, rpc := configurationnativev1connect.NewNativeConfigurationServiceHandler(handler,
		connect.WithReadMaxBytes(nativeMaxWireBytes), connect.WithSendMaxBytes(nativeMaxWireBytes),
		connect.WithCodec(nativeConfigurationCodec{}), connect.WithCompression("gzip", nil, nil),
		connect.WithRequireConnectProtocolHeader(),
		connect.WithConditionalHandlerOptions(func(spec connect.Spec) []connect.HandlerOption {
			if nativeBootstrapProcedure(spec.Procedure) {
				return []connect.HandlerOption{connect.WithReadMaxBytes(nativeBootstrapMaxBytes)}
			}
			return nil
		}),
	)
	return path, rpc
}

type nativeResponseWriter struct {
	http.ResponseWriter
	request *http.Request
	status  int
	payload []byte
	failed  bool
}

func (writer *nativeResponseWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }
func (writer *nativeResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	if status >= 400 && writer.request.ProtoMajor == 1 {
		writer.request.Close = true
		writer.Header().Set("Connection", "close")
	}
	writer.status = status
}
func (writer *nativeResponseWriter) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	if writer.failed || len(data) > nativeMaxWireBytes-len(writer.payload) {
		writer.failed = true
		return 0, state.ErrNativeUnavailable
	}
	writer.payload = append(writer.payload, data...)
	return len(data), nil
}

func (writer *nativeResponseWriter) flush() error {
	defer func() { clear(writer.payload); writer.payload = nil }()
	if writer.failed {
		clear(writer.payload)
		writer.payload = nil
		writer.status, writer.failed = 0, false
		nativeWriteError(writer, connect.NewError(connect.CodeUnavailable, errors.New("native service unavailable")))
	}
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	writer.Header().Del("Transfer-Encoding")
	writer.Header().Set("Content-Length", strconv.Itoa(len(writer.payload)))
	writer.ResponseWriter.WriteHeader(writer.status)
	if len(writer.payload) > 0 {
		if _, err := writer.ResponseWriter.Write(writer.payload); err != nil {
			return err
		}
	}
	return http.NewResponseController(writer).Flush()
}

func nativeConfigurationRequests(service *nativeconsumer.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		response := &nativeResponseWriter{ResponseWriter: writer, request: request}
		writer = response
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Frame-Options", "DENY")
		err := service.Call(request.Context(), func(ctx context.Context) error {
			controller := http.NewResponseController(writer)
			deadline, _ := ctx.Deadline()
			if controller.SetReadDeadline(deadline) != nil || controller.SetWriteDeadline(deadline) != nil {
				return state.ErrNativeUnavailable
			}
			body := request.Body
			drained := make(chan struct{})
			stop := context.AfterFunc(ctx, func() {
				_ = controller.SetReadDeadline(time.Now())
				_ = controller.SetWriteDeadline(time.Now())
				_ = body.Close()
				close(drained)
			})
			defer func() {
				_ = response.flush()
				if !stop() {
					<-drained
				}
				_ = controller.SetReadDeadline(time.Now())
				_ = body.Close()
				_ = controller.SetReadDeadline(time.Time{})
			}()
			if err := nativeRequestAllowed(request); err != nil {
				nativeWriteError(writer, nativeError(ctx, err))
				return nil
			}
			request = request.Clone(ctx)
			request.Header.Del("Accept-Encoding")
			maximum := int64(nativeMaxWireBytes)
			if nativeBootstrapProcedure(request.URL.Path) {
				maximum = nativeBootstrapMaxBytes
			}
			request.Body = nativeRequestBody{ReadCloser: http.MaxBytesReader(writer, body, maximum), ctx: ctx}
			next.ServeHTTP(writer, request)
			return nil
		})
		if err != nil {
			failure := nativeError(request.Context(), err)
			controller := http.NewResponseController(writer)
			_ = controller.SetReadDeadline(time.Now())
			_ = request.Body.Close()
			_ = controller.SetWriteDeadline(time.Now().Add(20 * time.Second))
			nativeWriteError(writer, failure)
			_ = response.flush()
		}
	})
}

func nativeBootstrapProcedure(path string) bool {
	return path == configurationnativev1connect.NativeConfigurationServiceBeginNativeConsumerProcedure || path == configurationnativev1connect.NativeConfigurationServiceBindNativeConsumerProcedure
}

func nativeRequestAllowed(request *http.Request) error {
	if request.TLS == nil {
		return connect.NewError(connect.CodePermissionDenied, errors.New("native TLS required"))
	}
	for name := range request.Header {
		lower := strings.ToLower(name)
		if lower == "origin" || strings.HasPrefix(lower, "sec-fetch-") || lower == "cookie" {
			return connect.NewError(connect.CodePermissionDenied, errors.New("native request denied"))
		}
	}
	invalid := func() error {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid native request"))
	}
	if request.Method != http.MethodPost || request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.RawPath != "" {
		return invalid()
	}
	if values := request.Header.Values("Content-Type"); len(values) != 1 || values[0] != "application/proto" {
		return invalid()
	}
	if values := request.Header.Values("Connect-Protocol-Version"); len(values) != 1 || values[0] != "1" {
		return invalid()
	}
	for _, name := range []string{"Content-Encoding", "Connect-Content-Encoding", "Grpc-Encoding"} {
		if len(request.Header.Values(name)) != 0 {
			return invalid()
		}
	}
	if values := request.Header.Values("Connect-Timeout-Ms"); len(values) > 0 {
		if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 8 || strings.Trim(values[0], "0123456789") != "" {
			return invalid()
		}
		if _, err := strconv.ParseUint(values[0], 10, 32); err != nil {
			return invalid()
		}
	}
	bytes := 0
	for name, values := range request.Header {
		for _, value := range values {
			bytes += len(name) + len(value) + 4
			if bytes > 16<<10 {
				return connect.NewError(connect.CodeResourceExhausted, errors.New("native request limit exceeded"))
			}
		}
	}
	return nil
}

type nativeRequestBody struct {
	io.ReadCloser
	ctx context.Context
}

func (body nativeRequestBody) Read(destination []byte) (int, error) {
	count, err := body.ReadCloser.Read(destination)
	if err == nil || errors.Is(err, io.EOF) {
		return count, err
	}
	if body.ctx.Err() != nil {
		return count, nativeError(body.ctx, body.ctx.Err())
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return count, connect.NewError(connect.CodeResourceExhausted, errors.New("native request limit exceeded"))
	}
	return count, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid native request body"))
}

func nativeError(ctx context.Context, err error) *connect.Error {
	if errors.Is(err, state.ErrNativeOutcomeUnknown) {
		return connect.NewError(connect.CodeUnavailable, errors.New("materialization outcome unknown"))
	}
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled.(*connect.Error)
	}
	if failure, ok := errors.AsType[*connect.Error](err); ok {
		messages := map[connect.Code]string{
			connect.CodeInvalidArgument:    "invalid native request",
			connect.CodeUnauthenticated:    "invalid native authorization",
			connect.CodePermissionDenied:   "native request denied",
			connect.CodeFailedPrecondition: "native operation precondition failed",
			connect.CodeNotFound:           "native resource not found",
			connect.CodeAborted:            "native revision changed",
			connect.CodeResourceExhausted:  "native capacity exhausted",
			connect.CodeUnavailable:        "native service unavailable",
			connect.CodeCanceled:           "request canceled",
			connect.CodeDeadlineExceeded:   "request deadline exceeded",
		}
		if message, exists := messages[failure.Code()]; exists {
			return connect.NewError(failure.Code(), errors.New(message))
		}
		return connect.NewError(connect.CodeUnavailable, errors.New("native service unavailable"))
	}
	for _, entry := range []struct {
		err     error
		code    connect.Code
		message string
	}{
		{state.ErrNativeUnauthorized, connect.CodeUnauthenticated, "invalid native authorization"},
		{state.ErrNativeInvalid, connect.CodeInvalidArgument, "invalid native request"},
		{state.ErrNativeQuota, connect.CodeResourceExhausted, "native capacity exhausted"},
		{state.ErrNativePrecondition, connect.CodeFailedPrecondition, "native operation precondition failed"},
		{state.ErrConfigurationNotFound, connect.CodeNotFound, "native resource not found"},
		{configuration.ErrNotFound, connect.CodeNotFound, "native resource not found"},
		{state.ErrConfigurationConflict, connect.CodeAborted, "native revision changed"},
		{configuration.ErrConflict, connect.CodeAborted, "native revision changed"},
		{configuration.ErrUnsupported, connect.CodeFailedPrecondition, "native operation precondition failed"},
		{configuration.ErrInvalid, connect.CodeInvalidArgument, "invalid native request"},
		{configuration.ErrLimit, connect.CodeResourceExhausted, "native capacity exhausted"},
	} {
		if errors.Is(err, entry.err) {
			return connect.NewError(entry.code, errors.New(entry.message))
		}
	}
	return connect.NewError(connect.CodeUnavailable, errors.New("native service unavailable"))
}

func nativeWriteError(writer http.ResponseWriter, failure *connect.Error) {
	status := http.StatusBadRequest
	switch failure.Code() {
	case connect.CodeUnauthenticated:
		status = http.StatusUnauthorized
	case connect.CodePermissionDenied:
		status = http.StatusForbidden
	case connect.CodeResourceExhausted:
		status = http.StatusTooManyRequests
	case connect.CodeCanceled:
		status = 499
	case connect.CodeDeadlineExceeded:
		status = http.StatusGatewayTimeout
	case connect.CodeUnavailable:
		status = http.StatusServiceUnavailable
	}
	data, _ := json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{failure.Code().String(), failure.Message()})
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = writer.Write(data)
}

func (handler *nativeConfigurationHandler) credential(ctx context.Context, header http.Header, bootstrap bool) (string, error) {
	raw := bearer(header)
	if raw == "" || len(raw) > 128 {
		return "", state.ErrNativeUnauthorized
	}
	if bootstrap {
		return raw, nil
	}
	if !strings.HasPrefix(raw, "cn1.") {
		if _, err := handler.auth.authenticate(ctx, header); err != nil {
			return "", err
		}
		return "", connect.NewError(connect.CodePermissionDenied, errors.New("native credential required"))
	}
	if _, err := nativesessionauth.ParseNativeBearer(raw); err != nil {
		return "", state.ErrNativeUnauthorized
	}
	return raw, nil
}

func (handler *nativeConfigurationHandler) reply[Message any](ctx context.Context, message *Message, err error) (*connect.Response[Message], error) {
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if sensitive, ok := any(message).(proto.Message); ok && sensitive.ProtoReflect().IsValid() {
			nativeClearWire(sensitive)
		}
		return nil, nativeError(ctx, err)
	}
	return connect.NewResponse(message), nil
}

func (handler *nativeConfigurationHandler) BeginNativeConsumer(ctx context.Context, request *connect.Request[nativev1.BeginNativeConsumerRequest]) (*connect.Response[nativev1.BeginNativeConsumerResponse], error) {
	raw, err := handler.credential(ctx, request.Header(), true)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	value := request.Msg
	snapshot, err := handler.service.Begin(ctx, raw, state.NativeConsumerStart{ExpectedOwner: value.ExpectedOwnerId, Origin: value.ExpectedServerOrigin, Purpose: state.NativeConfigurationApply, OperationID: value.OperationId, PublicKey: value.ConsumerPublicKey, ClientNonce: value.ClientNonce})
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	fields, err := snapshot.BindFields()
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	return handler.reply(ctx, &nativev1.BeginNativeConsumerResponse{ChallengeId: fields.ChallengeID, Challenge: fields.Challenge[:], NativeSessionBinding: fields.NativeSessionBinding[:], OwnerId: fields.OwnerID, ServerOrigin: fields.ServerOrigin, ChallengeExpiresAtUnixMs: fields.ChallengeExpiresAtUnixMS, NativeSessionExpiresAtUnixMs: fields.NativeSessionExpiresAtUnixMS}, nil)
}

func (handler *nativeConfigurationHandler) BindNativeConsumer(ctx context.Context, request *connect.Request[nativev1.BindNativeConsumerRequest]) (*connect.Response[nativev1.BindNativeConsumerResponse], error) {
	raw, err := handler.credential(ctx, request.Header(), true)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	issued, binding, err := handler.service.Bind(ctx, raw, request.Msg.ChallengeId, request.Msg.ProofSignature)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	token, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(issued.Reveal(), "cn1."))
	if err != nil || len(token) != 32 {
		clear(token)
		return nil, nativeError(ctx, state.ErrNativeUnavailable)
	}
	return handler.reply(ctx, &nativev1.BindNativeConsumerResponse{NativeSessionToken: token, NativeSessionBinding: binding[:], ExpiresAtUnixMs: issued.ExpiresAt().UnixMilli()}, nil)
}

func (handler *nativeConfigurationHandler) PrepareNativeApply(ctx context.Context, request *connect.Request[nativev1.PrepareNativeApplyRequest]) (*connect.Response[nativev1.PrepareNativeApplyResponse], error) {
	raw, err := handler.credential(ctx, request.Header(), false)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	value := request.Msg
	input := nativesessionauth.PrepareRequest{OwnerID: value.ExpectedOwnerId, AssetID: value.AssetId, AssetRevision: value.ExpectedAssetRevision, AgentID: value.AgentId, SchemaVersion: value.SchemaVersion, Adapter: nativeAdapterFields(value.Adapter), Target: uint32(value.Target), Scope: uint32(value.Scope), ProfileID: value.ProfileId, SelectionRevision: value.SelectionRevision, OperationID: [16]byte(value.OperationId), Nonce: [32]byte(value.ClientNonce)}
	prepared, err := handler.service.Prepare(ctx, raw, input, value.ProofSignature)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	patch := &nativev1.ClaudeProviderPatch{Operations: make([]*nativev1.PatchOperation, 7)}
	for index, operation := range prepared.Patch() {
		entry := &nativev1.PatchOperation{Slot: nativev1.EnvSlot(index + 1)}
		switch operation.Action {
		case 1:
			entry.Action = &nativev1.PatchOperation_SetLiteral{SetLiteral: &nativev1.SetLiteral{Value: operation.Literal}}
		case 2:
			entry.Action = &nativev1.PatchOperation_SetSelectedSecret{SetSelectedSecret: &nativev1.SetSelectedSecret{Slot: nativev1.SecretSlot(operation.SecretSlot)}}
		case 3:
			entry.Action = &nativev1.PatchOperation_Remove{Remove: &nativev1.RemoveEnv{}}
		default:
			return nil, nativeError(ctx, state.ErrNativeUnavailable)
		}
		patch.Operations[index] = entry
	}
	challenge := prepared.Challenge()
	return handler.reply(ctx, &nativev1.PrepareNativeApplyResponse{Binding: nativeWirePreparation(prepared.Binding()), Patch: patch, MaterializationRequired: prepared.Binding().RequiredSlot != 0, AuthorizeChallenge: challenge[:]}, nil)
}

func (handler *nativeConfigurationHandler) AuthorizeNativeMaterialization(ctx context.Context, request *connect.Request[nativev1.AuthorizeNativeMaterializationRequest]) (*connect.Response[nativev1.AuthorizeNativeMaterializationResponse], error) {
	raw, err := handler.credential(ctx, request.Header(), false)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	value := request.Msg
	input := nativesessionauth.AuthorizeRequest{PrepareID: value.PrepareId, OperationID: [16]byte(value.OperationId), Commitment: [32]byte(value.IntentCommitment), Nonce: [32]byte(value.ClientNonce), IntentExpiresMS: value.IntentExpiresAtUnixMs}
	if len(value.RequestedSlots) == 1 {
		input.RequiredSlot = uint32(value.RequestedSlots[0])
	}
	switch consent := value.Authorization.(type) {
	case *nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation:
		input.Consent = nativesessionauth.ConsentFields{Kind: 1, Revision: consent.ExplicitConfirmation.ConfirmationRevision}
	case *nativev1.AuthorizeNativeMaterializationRequest_AutoPolicy:
		input.Consent = nativesessionauth.ConsentFields{Kind: 2, Revision: consent.AutoPolicy.PolicyRevision, PolicyID: string(consent.AutoPolicy.PolicyId)}
	default:
		return nil, nativeError(ctx, state.ErrNativeInvalid)
	}
	issued, err := handler.service.Authorize(ctx, raw, input, value.ProofSignature)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	defer issued.Destroy()
	response := &nativev1.AuthorizeNativeMaterializationResponse{Binding: nativeWireApply(issued.Binding()), MaterializationRequired: issued.Binding().Preparation.RequiredSlot != 0}
	if issued.ID() != "" {
		challenge := issued.Challenge()
		response.GrantId, response.GrantToken, response.ConsumeChallenge, response.ExpiresAtUnixMs = issued.ID(), issued.Reveal(), challenge[:], issued.ExpiresMS()
	}
	return handler.reply(ctx, response, nil)
}

func (handler *nativeConfigurationHandler) ConsumeNativeMaterialization(ctx context.Context, request *connect.Request[nativev1.ConsumeNativeMaterializationRequest]) (*connect.Response[nativev1.ConsumeNativeMaterializationResponse], error) {
	defer nativeClearWire(request.Msg)
	raw, err := handler.credential(ctx, request.Header(), false)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	value := request.Msg
	token, err := nativesessionauth.ParseGrantToken(value.GrantToken)
	if err != nil {
		return nil, nativeError(ctx, state.ErrNativeInvalid)
	}
	materialization, err := handler.service.Consume(ctx, raw, nativesessionauth.ConsumeRequest{PrepareID: value.PrepareId, GrantID: value.GrantId, Token: token, OperationID: [16]byte(value.OperationId), Commitment: [32]byte(value.IntentCommitment), Nonce: [32]byte(value.ClientNonce)}, value.ProofSignature)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	defer materialization.Destroy()
	encoded := materialization.Reveal()
	defer clear(encoded)
	response := new(nativev1.ConsumeNativeMaterializationResponse)
	if err := (nativeConfigurationCodec{}).Unmarshal(encoded, response); err != nil {
		return nil, nativeError(ctx, state.ErrNativeOutcomeUnknown)
	}
	return handler.reply(ctx, response, nil)
}

func (handler *nativeConfigurationHandler) GetNativeMaterializationStatus(ctx context.Context, request *connect.Request[nativev1.GetNativeMaterializationStatusRequest]) (*connect.Response[nativev1.GetNativeMaterializationStatusResponse], error) {
	raw, err := handler.credential(ctx, request.Header(), false)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	value := request.Msg
	status, err := handler.service.Status(ctx, raw, nativesessionauth.StatusRequest{PrepareID: value.PrepareId, GrantID: value.GrantId, OperationID: [16]byte(value.OperationId), Nonce: [32]byte(value.ClientNonce)}, value.ProofSignature)
	return handler.reply(ctx, &nativev1.GetNativeMaterializationStatusResponse{State: nativev1.GrantState(status.State), GrantId: status.GrantID, ExpiresAtUnixMs: status.ExpiresMS, ConsumedAtUnixMs: status.ConsumedMS}, err)
}

func nativeAdapterFields(value *nativev1.AdapterBinding) nativesessionauth.AdapterFields {
	return nativesessionauth.AdapterFields{APIVersion: value.ApiVersion, PluginID: value.PluginId, PluginVersion: value.PluginVersion, DefinitionDigest: value.DefinitionDigest, Capability: value.RequiredCapabilities[0]}
}

func nativeWirePreparation(value nativesessionauth.PreparationFields) *nativev1.PreparationBinding {
	consumer, request := value.Consumer, value.Request
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	var slots []nativev1.SecretSlot
	if value.RequiredSlot != 0 {
		slots = []nativev1.SecretSlot{nativev1.SecretSlot(value.RequiredSlot)}
	}
	return &nativev1.PreparationBinding{OwnerId: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationId: consumer.OperationID[:], AssetId: request.AssetID, AssetRevision: request.AssetRevision, AgentId: request.AgentID, AssetKind: 1, SchemaVersion: request.SchemaVersion, Adapter: &nativev1.AdapterBinding{ApiVersion: request.Adapter.APIVersion, PluginId: request.Adapter.PluginID, PluginVersion: request.Adapter.PluginVersion, DefinitionDigest: request.Adapter.DefinitionDigest, RequiredCapabilities: []string{request.Adapter.Capability}}, Target: nativev1.Target(request.Target), Scope: nativev1.Scope(request.Scope), ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, RequiredSlots: slots, PrepareExpiresAtUnixMs: value.ExpiresMS, ConsumerKeySha256: keyHash[:], PrepareId: value.PrepareID, PreparedAtUnixMs: value.PreparedMS, PatchSha256: value.PatchSHA256[:]}
}

func nativeWireApply(value nativesessionauth.ApplyFields) *nativev1.ApplyBinding {
	return &nativev1.ApplyBinding{Preparation: nativeWirePreparation(value.Preparation), IntentCommitment: value.Commitment[:], IntentExpiresAtUnixMs: value.IntentExpiresMS}
}
