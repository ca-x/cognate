package configclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"slices"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"google.golang.org/protobuf/proto"
)

type GeminiNativePreparation struct {
	nativeFlowOpaque
	private *geminiPreparedValue
}
type geminiPreparedValue struct {
	response       *nativev1.PrepareNativeApplyResponse
	sessionExpires time.Time
}

func (value GeminiNativePreparation) CopyBinding() *nativev1.PreparationBinding {
	if value.private == nil {
		return nil
	}
	return proto.Clone(value.private.response.Binding).(*nativev1.PreparationBinding)
}
func (value GeminiNativePreparation) CopyDeclaration() agentconfig.GeminiProviderDeclarationV1 {
	if value.private == nil {
		return agentconfig.GeminiProviderDeclarationV1{}
	}
	declaration, _ := decodeGeminiDeclaration(value.private.response.GeminiDeclaration)
	return declaration
}
func (value GeminiNativePreparation) SessionExpiresAt() time.Time {
	if value.private == nil {
		return time.Time{}
	}
	return value.private.sessionExpires
}
func (value GeminiNativePreparation) Destroy() {
	if value.private != nil {
		proto.Reset(value.private.response)
		*value.private = geminiPreparedValue{}
	}
}
func (flow *NativeFlow) PrepareGemini(caller context.Context, selection NativeSelection) (GeminiNativePreparation, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return GeminiNativePreparation{}, err
	}
	defer done()
	if flow.agent != "" || flow.prepared != nil {
		return GeminiNativePreparation{}, ErrNativeInvalid
	}
	request := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: flow.consumer.OwnerID, AssetId: selection.AssetID, ExpectedAssetRevision: selection.AssetRevision, AgentId: "gemini", SchemaVersion: 1, Adapter: geminiNativeAdapterBinding(), Target: 3, Scope: 1, ProfileId: selection.ProfileID, SelectionRevision: selection.SelectionRevision, OperationId: bytes.Clone(flow.consumer.OperationID), ClientNonce: nativeNonce(), GeminiContractVersion: 1}
	request.ProofSignature, err = flow.sign(GeminiPrepareTranscript(flow.consumer, request))
	if err != nil {
		return GeminiNativePreparation{}, err
	}
	flow.agent = "gemini"
	response := new(nativev1.PrepareNativeApplyResponse)
	started := time.Now()
	if err := flow.call(ctx, "PrepareNativeApply", request, response); err != nil {
		return GeminiNativePreparation{}, err
	}
	if ValidateGeminiPreparedDeclaration(response) != nil {
		return GeminiNativePreparation{}, ErrNativeInvalid
	}
	binding := response.Binding
	if binding.OwnerId != request.ExpectedOwnerId || binding.ServerOrigin != flow.consumer.ServerOrigin || !bytes.Equal(binding.NativeSessionBinding, flow.consumer.NativeSessionBinding) || !bytes.Equal(binding.ConsumerKeySha256, flow.consumer.ConsumerKeySHA256) || !bytes.Equal(binding.OperationId, flow.consumer.OperationID) || binding.AssetId != request.AssetId || binding.AssetRevision != request.ExpectedAssetRevision || binding.ProfileId != request.ProfileId || binding.SelectionRevision != request.SelectionRevision || !proto.Equal(binding.Adapter, request.Adapter) || binding.PrepareExpiresAtUnixMs > flow.expires.UnixMilli() || binding.PrepareExpiresAtUnixMs <= time.Now().UnixMilli() || binding.PreparedAtUnixMs < started.UnixMilli() || binding.PreparedAtUnixMs > time.Now().UnixMilli() || ctx.Err() != nil || caller.Err() != nil {
		return GeminiNativePreparation{}, ErrNativeInvalid
	}
	flow.prepared = response
	return GeminiNativePreparation{private: &geminiPreparedValue{response: proto.Clone(response).(*nativev1.PrepareNativeApplyResponse), sessionExpires: flow.expires}}, nil
}

type geminiStatusTicketBody struct {
	Version         uint32
	Origin, Owner   string
	Operation       [16]byte
	Expires         int64
	Request, Bearer []byte
}

func (flow *NativeFlow) geminiStatusTicket() ([]byte, error) {
	request := &nativev1.GetNativeMaterializationStatusRequest{PrepareId: flow.prepared.Binding.PrepareId, OperationId: bytes.Clone(flow.consumer.OperationID), ClientNonce: nativeNonce()}
	var err error
	request.ProofSignature, err = flow.sign(GeminiStatusTranscript(flow.prepared.Binding, flow.prepared.AuthorizeChallenge, request))
	if err != nil {
		return nil, err
	}
	encoded, err := proto.Marshal(request)
	if err != nil {
		return nil, ErrNativeInvalid
	}
	bearer := flow.bearer.CloneBytes()
	defer clear(bearer)
	body, err := json.Marshal(geminiStatusTicketBody{Version: 1, Origin: flow.consumer.ServerOrigin, Owner: flow.consumer.OwnerID, Operation: [16]byte(flow.consumer.OperationID), Expires: flow.expires.UnixMilli(), Request: encoded, Bearer: bearer})
	if err != nil || len(body) > 8192 {
		clear(body)
		return nil, ErrNativeInvalid
	}
	return body, nil
}
func (flow *NativeFlow) AuthorizeGemini(caller context.Context, engine *configapply.GeminiEngine, plan configapply.GeminiPlan) error {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return err
	}
	defer done()
	if flow.agent != "gemini" || flow.prepared == nil || flow.authorizeAttempted || engine == nil {
		return ErrNativeInvalid
	}
	ticket, err := flow.geminiStatusTicket()
	if err != nil {
		return err
	}
	defer clear(ticket)
	if err := engine.BindGeminiStatusTicket(ctx, plan, ticket); err != nil {
		return err
	}
	constraints, err := engine.AdmitGeminiAuthorization(ctx, plan)
	if err != nil {
		return err
	}
	defer constraints.Destroy()
	local := constraints.CopyProjection()
	preparation := flow.prepared.Binding
	commitment, err := HashGeminiIntent(preparation, local, constraints.ConfirmationRevision(), constraints.ExpiresAt().UnixMilli())
	if err != nil || commitment != constraints.Commitment() || constraints.OperationID() != [16]byte(flow.consumer.OperationID) {
		return ErrNativeInvalid
	}
	request := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: preparation.PrepareId, OperationId: bytes.Clone(flow.consumer.OperationID), IntentCommitment: bytes.Clone(commitment[:]), RequestedSlots: slices.Clone(preparation.RequiredSlots), Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: constraints.ConfirmationRevision()}}, ClientNonce: nativeNonce(), IntentExpiresAtUnixMs: constraints.ExpiresAt().UnixMilli(), GeminiLocalBinding: local}
	request.ProofSignature, err = flow.sign(GeminiAuthorizeTranscript(preparation, flow.prepared.AuthorizeChallenge, request))
	if err != nil {
		return err
	}
	flow.apply = &nativev1.ApplyBinding{Preparation: proto.Clone(preparation).(*nativev1.PreparationBinding), IntentCommitment: bytes.Clone(commitment[:]), IntentExpiresAtUnixMs: request.IntentExpiresAtUnixMs, GeminiLocalBinding: proto.Clone(local).(*nativev1.GeminiLocalBindingV1)}
	flow.authorizeAttempted = true
	response := new(nativev1.AuthorizeNativeMaterializationResponse)
	defer nativeClear(response)
	if flow.call(ctx, "AuthorizeNativeMaterialization", request, response) != nil {
		return ErrNativeUnknown
	}
	if !proto.Equal(response.Binding, flow.apply) || response.MaterializationRequired != (len(preparation.RequiredSlots) == 1) || response.MaterializationRequired && (response.ExpiresAtUnixMs <= time.Now().UnixMilli() || response.ExpiresAtUnixMs > time.Now().Add(time.Minute).UnixMilli() || response.ExpiresAtUnixMs > flow.expires.UnixMilli()) || ctx.Err() != nil || caller.Err() != nil {
		return ErrNativeUnknown
	}
	if err := engine.RecordGeminiAuthorization(ctx, plan, response.Binding); err != nil {
		return ErrNativeUnknown
	}
	if response.MaterializationRequired {
		flow.grant = newNativeSecret(response.GrantToken)
		flow.grantID = response.GrantId
		flow.consumeChallenge = bytes.Clone(response.ConsumeChallenge)
		flow.grantExpiry = response.ExpiresAtUnixMs
	}
	return nil
}
func (flow *NativeFlow) ConsumeGemini(caller context.Context, engine *configapply.GeminiEngine, plan configapply.GeminiPlan) (NativeMaterial, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return NativeMaterial{}, err
	}
	defer done()
	if flow.agent != "gemini" || flow.prepared == nil || flow.apply == nil || flow.grantID == "" || flow.consumeAttempted || time.Now().UnixMilli() >= flow.grantExpiry {
		return NativeMaterial{}, ErrNativeInvalid
	}
	request := &nativev1.ConsumeNativeMaterializationRequest{PrepareId: flow.prepared.Binding.PrepareId, GrantId: flow.grantID, GrantToken: flow.grant.CloneBytes(), OperationId: bytes.Clone(flow.consumer.OperationID), IntentCommitment: bytes.Clone(flow.apply.IntentCommitment), ClientNonce: nativeNonce()}
	defer nativeClear(request)
	request.ProofSignature, err = flow.sign(GeminiConsumeTranscript(flow.apply, flow.prepared.AuthorizeChallenge, flow.consumeChallenge, flow.grantExpiry, request))
	if err != nil {
		return NativeMaterial{}, err
	}
	if err := engine.AdmitGeminiConsume(ctx, plan); err != nil {
		return NativeMaterial{}, err
	}
	flow.consumeAttempted = true
	defer flow.grant.Destroy()
	response := new(nativev1.ConsumeNativeMaterializationResponse)
	defer nativeClear(response)
	started := time.Now()
	if flow.call(ctx, "ConsumeNativeMaterialization", request, response) != nil {
		return NativeMaterial{}, ErrNativeUnknown
	}
	if !proto.Equal(response.Binding, flow.apply) || response.GrantId != flow.grantID || response.ConsumedAtUnixMs < started.UnixMilli() || response.ConsumedAtUnixMs > time.Now().UnixMilli() || response.ConsumedAtUnixMs >= flow.grantExpiry || ctx.Err() != nil || caller.Err() != nil {
		return NativeMaterial{}, ErrNativeUnknown
	}
	if err := engine.RecordGeminiConsumption(ctx, plan, response.Binding); err != nil {
		return NativeMaterial{}, ErrNativeUnknown
	}
	return NativeMaterial{newNativeSecret(response.SlotValues[0].Plaintext)}, nil
}
func (flow *NativeFlow) StatusGemini(caller context.Context) (NativeStatus, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return NativeStatus{}, err
	}
	defer done()
	if flow.agent != "gemini" || flow.prepared == nil {
		return NativeStatus{}, ErrNativeInvalid
	}
	request := &nativev1.GetNativeMaterializationStatusRequest{PrepareId: flow.prepared.Binding.PrepareId, GrantId: flow.grantID, OperationId: bytes.Clone(flow.consumer.OperationID), ClientNonce: nativeNonce()}
	request.ProofSignature, err = flow.sign(GeminiStatusTranscript(flow.prepared.Binding, flow.prepared.AuthorizeChallenge, request))
	if err != nil {
		return NativeStatus{}, err
	}
	response := new(nativev1.GetNativeMaterializationStatusResponse)
	if err := flow.call(ctx, "GetNativeMaterializationStatus", request, response); err != nil {
		return NativeStatus{}, err
	}
	return geminiStatusValue(response, flow.prepared.Binding.PreparedAtUnixMs, flow.expires.UnixMilli())
}
func geminiStatusValue(response *nativev1.GetNativeMaterializationStatusResponse, preparedMS, expiresMS int64) (NativeStatus, error) {
	if response.State < 1 || response.State > 5 || response.ExpiresAtUnixMs > expiresMS || response.ConsumedAtUnixMs > time.Now().UnixMilli() || response.ConsumedAtUnixMs != 0 && (response.ConsumedAtUnixMs < preparedMS || response.ConsumedAtUnixMs >= response.ExpiresAtUnixMs) {
		return NativeStatus{}, ErrNativeInvalid
	}
	values := []NativeGrantStatus{"", NativeNotIssued, NativeIssued, NativeConsumed, NativeExpired, NativeRetired}
	result := NativeStatus{State: values[int(response.State)]}
	if response.ExpiresAtUnixMs > 0 {
		result.ExpiresAt = time.UnixMilli(response.ExpiresAtUnixMs)
	}
	if response.ConsumedAtUnixMs > 0 {
		result.ConsumedAt = time.UnixMilli(response.ConsumedAtUnixMs)
	}
	return result, nil
}
func (client *NativeClient) StatusGeminiTicket(caller context.Context, ticket configapply.GeminiStatusTicket, owner string) (NativeStatus, error) {
	ctx, done, err := client.operation(caller)
	if err != nil {
		return NativeStatus{}, err
	}
	defer done()
	body := ticket.CopyBytes()
	defer clear(body)
	stored, request, err := parseGeminiStatusTicket(body, client.origin, owner, time.Now())
	if err != nil {
		return NativeStatus{}, err
	}
	defer clear(stored.Bearer)
	defer clear(stored.Request)
	response := new(nativev1.GetNativeMaterializationStatusResponse)
	if err := client.call(ctx, "GetNativeMaterializationStatus", string(stored.Bearer), request, response, false); err != nil {
		return NativeStatus{}, err
	}
	return geminiStatusValue(response, 0, stored.Expires)
}

func parseGeminiStatusTicket(body []byte, origin, owner string, now time.Time) (stored geminiStatusTicketBody, request *nativev1.GetNativeMaterializationStatusRequest, result error) {
	defer func() {
		if result != nil {
			clear(stored.Bearer)
			clear(stored.Request)
			stored = geminiStatusTicketBody{}
			request = nil
		}
	}()
	if len(body) == 0 || len(body) > 8192 || json.Unmarshal(body, &stored, json.RejectUnknownMembers(true)) != nil || stored.Version != 1 || stored.Origin != origin || stored.Owner != owner || stored.Expires <= now.UnixMilli() || len(stored.Bearer) != 47 || !bytes.HasPrefix(stored.Bearer, []byte("cn1.")) {
		return stored, nil, ErrNativeInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(stored.Bearer[4:]))
	defer clear(decoded)
	if err != nil || len(decoded) != 32 {
		return stored, nil, ErrNativeInvalid
	}
	request = new(nativev1.GetNativeMaterializationStatusRequest)
	if StrictUnmarshalNativeProof(stored.Request, request) != nil || [16]byte(request.OperationId) != stored.Operation || request.GrantId != "" {
		return stored, nil, ErrNativeInvalid
	}
	return stored, request, nil
}
