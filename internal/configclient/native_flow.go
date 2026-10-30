package configclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"google.golang.org/protobuf/proto"
)

type NativeSelection struct {
	AssetID           string
	AssetRevision     int64
	ProfileID         string
	SelectionRevision uint64
}

type NativePreparation struct {
	Origin, OwnerID, ProfileID, AssetID, PreparationID       string
	SchemaVersion                                            uint32
	AssetRevision                                            int64
	SelectionRevision                                        uint64
	OperationID                                              [16]byte
	Adapter                                                  agentconfig.Descriptor
	RequiredCapabilities                                     []agentconfig.Capability
	Patch                                                    agentconfig.ClaudeProviderPatchV1
	RequiredSlots                                            []agentconfig.ClaudeSecretSlot
	PreparedAt, PreparationExpiresAt, NativeSessionExpiresAt time.Time
}

type NativeApproval struct {
	PreparationID        string
	OperationID          [16]byte
	Commitment           [32]byte
	IntentExpiresAt      time.Time
	Slots                []agentconfig.ClaudeSecretSlot
	ConfirmationRevision uint64
	PolicyID             string
	PolicyRevision       uint64
}

type NativeGrantStatus string

const (
	NativeNotIssued NativeGrantStatus = "not-issued"
	NativeIssued    NativeGrantStatus = "issued"
	NativeConsumed  NativeGrantStatus = "consumed"
	NativeExpired   NativeGrantStatus = "expired"
	NativeRetired   NativeGrantStatus = "retired"
)

type NativeStatus struct {
	State                 NativeGrantStatus
	ExpiresAt, ConsumedAt time.Time
}

type NativeFlow struct {
	nativeFlowOpaque
	*nativeFlowPayload
	admission chan struct{}
	workers   sync.WaitGroup
	cleanup   sync.Once
	closed    bool
	mutex     sync.Mutex
	client    *NativeClient
	ctx       context.Context
	cancel    context.CancelFunc
	key       nativeSecret
	bearer    nativeSecret
	grant     nativeSecret
}

type nativeFlowPayload struct{ *nativeFlowSensitive }
type nativeFlowSensitive struct {
	agent                                string
	codex                                *codexFlowState
	codexAdmission                       codexAdmission
	consumer                             ConsumerBinding
	expires                              time.Time
	prepared                             *nativev1.PrepareNativeApplyResponse
	apply                                *nativev1.ApplyBinding
	grantID                              string
	consumeChallenge                     []byte
	grantExpiry                          int64
	authorizeAttempted, consumeAttempted bool
}

type nativeFlowOpaque struct{}

func (nativeFlowOpaque) String() string   { return "[redacted native flow]" }
func (nativeFlowOpaque) GoString() string { return "[redacted native flow]" }
func (nativeFlowOpaque) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted native flow]")
}
func (nativeFlowOpaque) MarshalJSON() ([]byte, error) { return []byte(`"[redacted native flow]"`), nil }
func (nativeFlowOpaque) MarshalText() ([]byte, error) { return []byte("[redacted native flow]"), nil }
func (nativeFlowOpaque) LogValue() slog.Value         { return slog.StringValue("[redacted native flow]") }

func (flow *NativeFlow) destroy() {
	flow.mutex.Lock()
	flow.closed = true
	flow.cancel()
	flow.mutex.Unlock()
	flow.workers.Wait()
	flow.cleanup.Do(func() {
		flow.key.Destroy()
		flow.bearer.Destroy()
		flow.grant.Destroy()
		flow.codex.destroy()
		clear(flow.consumer.NativeSessionBinding)
		clear(flow.consumer.OperationID)
		clear(flow.consumer.ConsumerKeySHA256)
		flow.consumer = ConsumerBinding{}
		flow.codexAdmission = nil
	})
}

func (flow *NativeFlow) Close() { flow.cancel(); flow.destroy() }

func (client *NativeClient) OpenConsumer(caller context.Context, owner, ownerBearer string) (*NativeFlow, error) {
	if !validOwner(owner) || len(ownerBearer) == 0 || len(ownerBearer) > 8192 || strings.ContainsAny(ownerBearer, "\r\n\x00") {
		return nil, ErrNativeInvalid
	}
	ctx, done, err := client.operation(caller)
	if err != nil {
		return nil, err
	}
	defer done()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, ErrNativeUnavailable
	}
	defer clear(private)
	operation, nonce := make([]byte, 16), make([]byte, 32)
	rand.Read(operation)
	rand.Read(nonce)
	begin := &nativev1.BeginNativeConsumerRequest{Purpose: 1, OperationId: operation, ConsumerPublicKey: public, ClientNonce: nonce, ExpectedOwnerId: owner, ExpectedServerOrigin: client.origin}
	challenge := new(nativev1.BeginNativeConsumerResponse)
	if err := client.call(ctx, "BeginNativeConsumer", ownerBearer, begin, challenge, true); err != nil {
		return nil, err
	}
	now := time.Now()
	if challenge.OwnerId != owner || challenge.ServerOrigin != client.origin || challenge.ChallengeExpiresAtUnixMs <= now.UnixMilli() || challenge.ChallengeExpiresAtUnixMs > now.Add(time.Minute).UnixMilli() || challenge.NativeSessionExpiresAtUnixMs > now.Add(5*time.Minute).UnixMilli() {
		return nil, ErrNativeInvalid
	}
	transcript, err := BindTranscript(begin, challenge)
	if err != nil {
		return nil, ErrNativeInvalid
	}
	signature, err := SignNativeProof(private, transcript)
	if err != nil {
		return nil, ErrNativeInvalid
	}
	bind := &nativev1.BindNativeConsumerRequest{ChallengeId: challenge.ChallengeId, ProofSignature: signature.CopyBytes()}
	bound := new(nativev1.BindNativeConsumerResponse)
	defer nativeClear(bound)
	if err := client.call(ctx, "BindNativeConsumer", ownerBearer, bind, bound, true); err != nil {
		return nil, err
	}
	if !bytes.Equal(bound.NativeSessionBinding, challenge.NativeSessionBinding) || bound.ExpiresAtUnixMs != challenge.NativeSessionExpiresAtUnixMs || bound.ExpiresAtUnixMs <= time.Now().UnixMilli() {
		return nil, ErrNativeInvalid
	}
	bearer := "cn1." + base64.RawURLEncoding.EncodeToString(bound.NativeSessionToken)
	keyHash := sha256.Sum256(public)
	flowCtx, cancel := context.WithCancel(client.ctx)
	flow := &NativeFlow{admission: make(chan struct{}, 1), client: client, ctx: flowCtx, cancel: cancel, key: newNativeSecret(private), bearer: newNativeSecret([]byte(bearer)), nativeFlowPayload: &nativeFlowPayload{nativeFlowSensitive: &nativeFlowSensitive{consumer: ConsumerBinding{ServerOrigin: client.origin, OwnerID: owner, NativeSessionBinding: bytes.Clone(bound.NativeSessionBinding), OperationID: bytes.Clone(operation), ConsumerKeySHA256: bytes.Clone(keyHash[:])}, expires: time.UnixMilli(bound.ExpiresAtUnixMs)}}}
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if ctx.Err() != nil || caller.Err() != nil || client.closed {
		flow.destroy()
		return nil, ErrNativeCanceled
	}
	client.flows = append(client.flows, flow)
	return flow, nil
}

func (flow *NativeFlow) start(caller context.Context) (context.Context, func(), error) {
	ctx, finish, err := flow.client.operation(caller)
	if err != nil {
		return nil, nil, err
	}
	flow.mutex.Lock()
	if flow.closed {
		flow.mutex.Unlock()
		finish()
		return nil, nil, ErrNativeCanceled
	}
	flow.workers.Add(1)
	flow.mutex.Unlock()
	operation, cancel := context.WithCancel(ctx)
	joined := make(chan struct{})
	stop := context.AfterFunc(flow.ctx, func() { cancel(); close(joined) })
	acquired := false
	done := func() {
		cancel()
		if !stop() {
			<-joined
		}
		if acquired {
			<-flow.admission
		}
		finish()
		flow.workers.Done()
	}
	select {
	case flow.admission <- struct{}{}:
		acquired = true
	case <-operation.Done():
		done()
		return nil, nil, ErrNativeCanceled
	}
	if operation.Err() != nil || caller.Err() != nil || flow.ctx.Err() != nil || !time.Now().Before(flow.expires) {
		done()
		return nil, nil, ErrNativeCanceled
	}
	return operation, done, nil
}

func (flow *NativeFlow) sign(transcript Transcript, err error) ([]byte, error) {
	if err != nil {
		return nil, ErrNativeInvalid
	}
	key := flow.key.CloneBytes()
	defer clear(key)
	signature, err := SignNativeProof(ed25519.PrivateKey(key), transcript)
	if err != nil {
		return nil, ErrNativeInvalid
	}
	return signature.CopyBytes(), nil
}

func (flow *NativeFlow) call(ctx context.Context, method string, request, response proto.Message) error {
	bearer := flow.bearer.CloneBytes()
	defer clear(bearer)
	return flow.client.call(ctx, method, string(bearer), request, response, false)
}

func nativeNonce() []byte { nonce := make([]byte, 32); rand.Read(nonce); return nonce }

func (flow *NativeFlow) Prepare(caller context.Context, selection NativeSelection) (NativePreparation, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return NativePreparation{}, err
	}
	defer done()
	if flow.prepared != nil {
		return NativePreparation{}, ErrNativeInvalid
	}
	if flow.agent != "" && flow.agent != "claude" {
		return NativePreparation{}, ErrNativeInvalid
	}
	request := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: flow.consumer.OwnerID, AssetId: selection.AssetID, ExpectedAssetRevision: selection.AssetRevision, AgentId: "claude", SchemaVersion: 1, Adapter: nativeAdapterBinding(), Target: 1, Scope: 1, ProfileId: selection.ProfileID, SelectionRevision: selection.SelectionRevision, OperationId: bytes.Clone(flow.consumer.OperationID), ClientNonce: nativeNonce()}
	request.ProofSignature, err = flow.sign(PrepareTranscript(flow.consumer, request))
	if err != nil {
		return NativePreparation{}, err
	}
	response := new(nativev1.PrepareNativeApplyResponse)
	flow.agent = "claude"
	started := time.Now()
	if err := flow.call(ctx, "PrepareNativeApply", request, response); err != nil {
		return NativePreparation{}, err
	}
	binding := response.Binding
	if binding.OwnerId != request.ExpectedOwnerId || binding.ServerOrigin != flow.consumer.ServerOrigin || !bytes.Equal(binding.NativeSessionBinding, flow.consumer.NativeSessionBinding) || !bytes.Equal(binding.ConsumerKeySha256, flow.consumer.ConsumerKeySHA256) || !bytes.Equal(binding.OperationId, request.OperationId) || binding.AssetId != request.AssetId || binding.AssetRevision != request.ExpectedAssetRevision || binding.AgentId != request.AgentId || binding.SchemaVersion != request.SchemaVersion || !proto.Equal(binding.Adapter, request.Adapter) || binding.Target != request.Target || binding.Scope != request.Scope || binding.ProfileId != request.ProfileId || binding.SelectionRevision != request.SelectionRevision || binding.PrepareExpiresAtUnixMs > flow.expires.UnixMilli() || binding.PrepareExpiresAtUnixMs <= time.Now().UnixMilli() || binding.PreparedAtUnixMs < started.UnixMilli() || binding.PreparedAtUnixMs > time.Now().UnixMilli() {
		return NativePreparation{}, ErrNativeInvalid
	}
	if ctx.Err() != nil || caller.Err() != nil {
		return NativePreparation{}, ErrNativeCanceled
	}
	flow.prepared = response
	return flow.safePreparation(), nil
}

func nativeSafeSlots(slots []nativev1.SecretSlot) []agentconfig.ClaudeSecretSlot {
	result := make([]agentconfig.ClaudeSecretSlot, 0, len(slots))
	for _, slot := range slots {
		if slot == 1 {
			result = append(result, agentconfig.ClaudeAPIKeySecret)
		} else {
			result = append(result, agentconfig.ClaudeAuthTokenSecret)
		}
	}
	return result
}

func (flow *NativeFlow) safePreparation() NativePreparation {
	binding := flow.prepared.Binding
	result := NativePreparation{Origin: binding.ServerOrigin, OwnerID: binding.OwnerId, ProfileID: binding.ProfileId, AssetID: binding.AssetId, PreparationID: binding.PrepareId, SchemaVersion: binding.SchemaVersion, AssetRevision: binding.AssetRevision, SelectionRevision: binding.SelectionRevision, Adapter: NativeAdapterPin(), RequiredCapabilities: []agentconfig.Capability{agentconfig.ClaudeProviderSwitchV1}, Patch: nativeSafePatch(flow.prepared.Patch), RequiredSlots: nativeSafeSlots(binding.RequiredSlots), PreparedAt: time.UnixMilli(binding.PreparedAtUnixMs), PreparationExpiresAt: time.UnixMilli(binding.PrepareExpiresAtUnixMs), NativeSessionExpiresAt: flow.expires}
	copy(result.OperationID[:], binding.OperationId)
	return result
}

func (flow *NativeFlow) Authorize(caller context.Context, approval NativeApproval) (bool, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return false, err
	}
	defer done()
	if flow.prepared == nil || flow.authorizeAttempted {
		return false, ErrNativeInvalid
	}
	binding := flow.prepared.Binding
	if approval.PreparationID != binding.PrepareId || !bytes.Equal(approval.OperationID[:], binding.OperationId) || approval.Commitment == [32]byte{} || !slices.Equal(approval.Slots, nativeSafeSlots(binding.RequiredSlots)) || approval.IntentExpiresAt.UnixMilli() <= time.Now().UnixMilli() || approval.IntentExpiresAt.After(time.Now().Add(5*time.Minute)) {
		return false, ErrNativeInvalid
	}
	request := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: binding.PrepareId, OperationId: bytes.Clone(binding.OperationId), IntentCommitment: bytes.Clone(approval.Commitment[:]), RequestedSlots: slices.Clone(binding.RequiredSlots), ClientNonce: nativeNonce(), IntentExpiresAtUnixMs: approval.IntentExpiresAt.UnixMilli()}
	if approval.ConfirmationRevision > 0 && approval.PolicyID == "" && approval.PolicyRevision == 0 {
		request.Authorization = &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: approval.ConfirmationRevision}}
	} else if approval.ConfirmationRevision == 0 && approval.PolicyRevision > 0 && opaqueID(approval.PolicyID) {
		request.Authorization = &nativev1.AuthorizeNativeMaterializationRequest_AutoPolicy{AutoPolicy: &nativev1.AutoPolicy{PolicyId: []byte(approval.PolicyID), PolicyRevision: approval.PolicyRevision}}
	} else {
		return false, ErrNativeInvalid
	}
	request.ProofSignature, err = flow.sign(AuthorizeTranscript(binding, flow.prepared.AuthorizeChallenge, request))
	if err != nil {
		return false, err
	}
	flow.authorizeAttempted = true
	expected := &nativev1.ApplyBinding{Preparation: proto.Clone(binding).(*nativev1.PreparationBinding), IntentCommitment: bytes.Clone(request.IntentCommitment), IntentExpiresAtUnixMs: request.IntentExpiresAtUnixMs}
	flow.apply = expected
	response := new(nativev1.AuthorizeNativeMaterializationResponse)
	defer nativeClear(response)
	if err := flow.call(ctx, "AuthorizeNativeMaterialization", request, response); err != nil {
		return false, ErrNativeUnknown
	}
	if !proto.Equal(response.Binding, expected) || response.MaterializationRequired != flow.prepared.MaterializationRequired || response.MaterializationRequired && (response.ExpiresAtUnixMs > time.Now().Add(time.Minute).UnixMilli() || response.ExpiresAtUnixMs > flow.expires.UnixMilli() || response.ExpiresAtUnixMs <= time.Now().UnixMilli()) {
		return false, ErrNativeUnknown
	}
	if ctx.Err() != nil || caller.Err() != nil {
		return false, ErrNativeUnknown
	}
	if response.MaterializationRequired {
		flow.grant = newNativeSecret(response.GrantToken)
		flow.grantID = response.GrantId
		flow.consumeChallenge = bytes.Clone(response.ConsumeChallenge)
		flow.grantExpiry = response.ExpiresAtUnixMs
	}
	return response.MaterializationRequired, nil
}

func (flow *NativeFlow) Consume(caller context.Context) (NativeMaterial, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return NativeMaterial{}, err
	}
	defer done()
	if flow.apply == nil || flow.grantID == "" || flow.consumeAttempted || time.Now().UnixMilli() >= flow.grantExpiry {
		return NativeMaterial{}, ErrNativeInvalid
	}
	request := &nativev1.ConsumeNativeMaterializationRequest{PrepareId: flow.prepared.Binding.PrepareId, GrantId: flow.grantID, GrantToken: flow.grant.CloneBytes(), OperationId: bytes.Clone(flow.consumer.OperationID), IntentCommitment: bytes.Clone(flow.apply.IntentCommitment), ClientNonce: nativeNonce()}
	defer nativeClear(request)
	request.ProofSignature, err = flow.sign(ConsumeTranscript(flow.apply, flow.prepared.AuthorizeChallenge, flow.consumeChallenge, flow.grantExpiry, request))
	if err != nil {
		return NativeMaterial{}, err
	}
	flow.consumeAttempted = true
	defer flow.grant.Destroy()
	response := new(nativev1.ConsumeNativeMaterializationResponse)
	defer nativeClear(response)
	started := time.Now()
	if err := flow.call(ctx, "ConsumeNativeMaterialization", request, response); err != nil {
		return NativeMaterial{}, ErrNativeUnknown
	}
	if !proto.Equal(response.Binding, flow.apply) || response.GrantId != flow.grantID || response.ConsumedAtUnixMs < started.UnixMilli() || response.ConsumedAtUnixMs > time.Now().UnixMilli() || response.ConsumedAtUnixMs >= flow.grantExpiry || ctx.Err() != nil || caller.Err() != nil {
		return NativeMaterial{}, ErrNativeUnknown
	}
	material := NativeMaterial{newNativeSecret(response.SlotValues[0].Plaintext)}
	if ctx.Err() != nil || caller.Err() != nil {
		material.Destroy()
		return NativeMaterial{}, ErrNativeUnknown
	}
	return material, nil
}

func (flow *NativeFlow) Status(caller context.Context) (NativeStatus, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return NativeStatus{}, err
	}
	defer done()
	if flow.prepared == nil || !flow.authorizeAttempted {
		return NativeStatus{}, ErrNativeInvalid
	}
	request := &nativev1.GetNativeMaterializationStatusRequest{PrepareId: flow.prepared.Binding.PrepareId, GrantId: flow.grantID, OperationId: bytes.Clone(flow.consumer.OperationID), ClientNonce: nativeNonce()}
	request.ProofSignature, err = flow.sign(StatusTranscript(flow.prepared.Binding, flow.prepared.AuthorizeChallenge, request))
	if err != nil {
		return NativeStatus{}, err
	}
	response := new(nativev1.GetNativeMaterializationStatusResponse)
	if err := flow.call(ctx, "GetNativeMaterializationStatus", request, response); err != nil {
		return NativeStatus{}, err
	}
	if flow.grantID != "" && response.GrantId != flow.grantID || response.ExpiresAtUnixMs > flow.apply.IntentExpiresAtUnixMs || response.ExpiresAtUnixMs > flow.expires.UnixMilli() || flow.grantExpiry != 0 && response.ExpiresAtUnixMs != flow.grantExpiry || response.ConsumedAtUnixMs > time.Now().UnixMilli() || response.ConsumedAtUnixMs != 0 && (response.ConsumedAtUnixMs < flow.prepared.Binding.PreparedAtUnixMs || response.ConsumedAtUnixMs >= response.ExpiresAtUnixMs) || ctx.Err() != nil || caller.Err() != nil {
		return NativeStatus{}, ErrNativeInvalid
	}
	states := []NativeGrantStatus{"", NativeNotIssued, NativeIssued, NativeConsumed, NativeExpired, NativeRetired}
	status := NativeStatus{State: states[int(response.State)]}
	if response.ExpiresAtUnixMs > 0 {
		status.ExpiresAt = time.UnixMilli(response.ExpiresAtUnixMs)
	}
	if response.ConsumedAtUnixMs > 0 {
		status.ConsumedAt = time.UnixMilli(response.ConsumedAtUnixMs)
	}
	return status, nil
}
