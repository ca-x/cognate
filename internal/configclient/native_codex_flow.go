package configclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"time"
	"unicode/utf8"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type codexClientBox[Value any] struct{ value *Value }
type codexClientOpaque[Value any] struct{ box *codexClientBox[Value] }

func codexClientWrap[Value any](value Value) codexClientOpaque[Value] {
	return codexClientOpaque[Value]{box: &codexClientBox[Value]{value: new(value)}}
}
func (value codexClientOpaque[Value]) present() bool {
	return value.box != nil && value.box.value != nil
}
func (codexClientOpaque[Value]) String() string   { return "[redacted Codex native]" }
func (codexClientOpaque[Value]) GoString() string { return "[redacted Codex native]" }
func (codexClientOpaque[Value]) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted Codex native]")
}
func (codexClientOpaque[Value]) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted Codex native]"`), nil
}
func (codexClientOpaque[Value]) MarshalText() ([]byte, error) {
	return []byte("[redacted Codex native]"), nil
}
func (codexClientOpaque[Value]) LogValue() slog.Value {
	return slog.StringValue("[redacted Codex native]")
}

type codexPreparedData struct {
	response       *nativev1.PrepareNativeApplyResponse
	sessionExpires time.Time
}
type codexApprovalData struct {
	revision uint64
	expires  time.Time
}
type codexAuthorizationData struct {
	binding *nativev1.ApplyBinding
	id      string
	expires time.Time
}
type CodexNativePreparation struct {
	codexClientOpaque[codexPreparedData]
}
type CodexNativeApproval struct {
	codexClientOpaque[codexApprovalData]
}
type CodexNativeAuthorization struct {
	codexClientOpaque[codexAuthorizationData]
}

func NewCodexNativeApproval(confirmationRevision uint64, intentExpiresAt time.Time, providerKeyApproved, finalApproved bool) (CodexNativeApproval, error) {
	if confirmationRevision == 0 || intentExpiresAt.UnixMilli() <= 0 || !providerKeyApproved || !finalApproved {
		return CodexNativeApproval{}, ErrNativeInvalid
	}
	return CodexNativeApproval{codexClientOpaque: codexClientWrap(codexApprovalData{revision: confirmationRevision, expires: intentExpiresAt})}, nil
}
func (value CodexNativePreparation) CopyBinding() *nativev1.PreparationBinding {
	if !value.present() {
		return nil
	}
	return proto.Clone(value.box.value.response.Binding).(*nativev1.PreparationBinding)
}
func (value CodexNativePreparation) CopyDeclaration() agentconfig.CodexProviderDeclarationV1 {
	if !value.present() {
		return agentconfig.CodexProviderDeclarationV1{}
	}
	declaration, _ := codexDeclaration(value.box.value.response.CodexDeclaration)
	return declaration
}
func (value CodexNativePreparation) SessionExpiresAt() time.Time {
	if !value.present() {
		return time.Time{}
	}
	return value.box.value.sessionExpires
}
func (value CodexNativePreparation) Clone() CodexNativePreparation {
	if !value.present() {
		return CodexNativePreparation{}
	}
	return CodexNativePreparation{codexClientOpaque: codexClientWrap(codexPreparedData{response: proto.Clone(value.box.value.response).(*nativev1.PrepareNativeApplyResponse), sessionExpires: value.box.value.sessionExpires})}
}
func (value CodexNativeApproval) Clone() CodexNativeApproval {
	if !value.present() {
		return CodexNativeApproval{}
	}
	return CodexNativeApproval{codexClientOpaque: codexClientWrap(*value.box.value)}
}
func (value CodexNativeAuthorization) CopyBinding() *nativev1.ApplyBinding {
	if !value.present() {
		return nil
	}
	return proto.Clone(value.box.value.binding).(*nativev1.ApplyBinding)
}
func (value CodexNativeAuthorization) ID() string {
	if !value.present() {
		return ""
	}
	return value.box.value.id
}
func (value CodexNativeAuthorization) ExpiresAt() time.Time {
	if !value.present() {
		return time.Time{}
	}
	return value.box.value.expires
}
func (value CodexNativeAuthorization) Clone() CodexNativeAuthorization {
	if !value.present() {
		return CodexNativeAuthorization{}
	}
	return CodexNativeAuthorization{codexClientOpaque: codexClientWrap(codexAuthorizationData{binding: value.CopyBinding(), id: value.box.value.id, expires: value.box.value.expires})}
}
func (value CodexNativePreparation) Destroy() {
	if value.present() {
		codexClearWire(value.box.value.response)
		value.box.value = nil
	}
}
func (value CodexNativeApproval) Destroy() {
	if value.present() {
		*value.box.value = codexApprovalData{}
		value.box.value = nil
	}
}
func (value CodexNativeAuthorization) Destroy() {
	if value.present() {
		codexClearWire(value.box.value.binding)
		value.box.value = nil
	}
}

type codexAdmission func(context.Context, codexconfig.ProviderPlan) (*nativev1.CodexLocalBindingV1, error)
type codexFlowState struct {
	prepared                             *nativev1.PrepareNativeApplyResponse
	apply                                *nativev1.ApplyBinding
	grantID                              string
	consumeChallenge                     []byte
	grantExpiry                          int64
	authorizeAttempted, consumeAttempted bool
}

func codexClearWire(message proto.Message) {
	if message == nil || !message.ProtoReflect().IsValid() {
		return
	}
	message.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		wipe := func(value protoreflect.Value) {
			if field.Kind() == protoreflect.BytesKind {
				clear(value.Bytes())
			} else if field.Kind() == protoreflect.MessageKind {
				codexClearWire(value.Message().Interface())
			}
		}
		if field.IsList() {
			for index := range value.List().Len() {
				wipe(value.List().Get(index))
			}
		} else {
			wipe(value)
		}
		return true
	})
	proto.Reset(message)
}
func (value *codexFlowState) destroy() {
	if value == nil {
		return
	}
	codexClearWire(value.prepared)
	codexClearWire(value.apply)
	clear(value.consumeChallenge)
	*value = codexFlowState{}
}

func codexClientResponseValid(message proto.Message) (bool, bool) {
	if message == nil || !message.ProtoReflect().IsValid() {
		return false, false
	}
	switch value := message.(type) {
	case *nativev1.PrepareNativeApplyResponse:
		if value.CodexDeclaration != nil || value.Binding.GetTarget() == 2 {
			return true, ValidateCodexPreparedDeclaration(value) == nil
		}
	case *nativev1.AuthorizeNativeMaterializationResponse:
		if value.Binding.GetPreparation().GetTarget() == 2 || value.Binding.GetCodexLocalBinding() != nil {
			_, err := HashCodexApply(value.Binding)
			return true, knownMessage(value) && err == nil && value.MaterializationRequired && opaqueID(value.GrantId) && len(value.GrantToken) == 32 && len(value.ConsumeChallenge) == 32 && value.ExpiresAtUnixMs > value.Binding.Preparation.PreparedAtUnixMs && value.ExpiresAtUnixMs <= value.Binding.IntentExpiresAtUnixMs
		}
	case *nativev1.ConsumeNativeMaterializationResponse:
		if value.Binding.GetPreparation().GetTarget() == 2 || value.Binding.GetCodexLocalBinding() != nil {
			_, err := HashCodexApply(value.Binding)
			if !knownMessage(value) || err != nil || len(value.SlotValues) != 1 || value.SlotValues[0] == nil || value.SlotValues[0].Slot != 3 || !opaqueID(value.GrantId) {
				return true, false
			}
			body := value.SlotValues[0].Plaintext
			return true, len(body) > 0 && len(body) <= 8192 && utf8.Valid(body) && !bytes.ContainsAny(body, "\x00\r\n") && value.ConsumedAtUnixMs >= value.Binding.Preparation.PreparedAtUnixMs && value.ConsumedAtUnixMs < value.Binding.IntentExpiresAtUnixMs
		}
	}
	return false, false
}

func (flow *NativeFlow) PrepareCodex(caller context.Context, selection NativeSelection) (CodexNativePreparation, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return CodexNativePreparation{}, err
	}
	defer done()
	if flow.agent != "" && flow.agent != "codex" || flow.codex != nil && flow.codex.prepared != nil {
		return CodexNativePreparation{}, ErrNativeInvalid
	}
	request := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: flow.consumer.OwnerID, AssetId: selection.AssetID, ExpectedAssetRevision: selection.AssetRevision, AgentId: "codex", SchemaVersion: 1, Adapter: codexNativeAdapterBinding(), Target: 2, Scope: 1, ProfileId: selection.ProfileID, SelectionRevision: selection.SelectionRevision, OperationId: bytes.Clone(flow.consumer.OperationID), ClientNonce: nativeNonce(), CodexContractVersion: 1}
	request.ProofSignature, err = flow.sign(CodexPrepareTranscript(flow.consumer, request))
	if err != nil {
		return CodexNativePreparation{}, err
	}
	flow.agent = "codex"
	response := new(nativev1.PrepareNativeApplyResponse)
	owned := false
	defer func() {
		if !owned {
			codexClearWire(response)
		}
	}()
	started := time.Now()
	if err := flow.call(ctx, "PrepareNativeApply", request, response); err != nil {
		return CodexNativePreparation{}, err
	}
	binding := response.Binding
	if binding.OwnerId != request.ExpectedOwnerId || binding.ServerOrigin != flow.consumer.ServerOrigin || !bytes.Equal(binding.NativeSessionBinding, flow.consumer.NativeSessionBinding) || !bytes.Equal(binding.ConsumerKeySha256, flow.consumer.ConsumerKeySHA256) || !bytes.Equal(binding.OperationId, request.OperationId) || binding.AssetId != request.AssetId || binding.AssetRevision != request.ExpectedAssetRevision || binding.AgentId != "codex" || binding.SchemaVersion != request.SchemaVersion || !proto.Equal(binding.Adapter, request.Adapter) || binding.Target != 2 || binding.Scope != 1 || binding.ProfileId != request.ProfileId || binding.SelectionRevision != request.SelectionRevision || binding.PrepareExpiresAtUnixMs > flow.expires.UnixMilli() || binding.PrepareExpiresAtUnixMs <= time.Now().UnixMilli() || binding.PreparedAtUnixMs < started.UnixMilli() || binding.PreparedAtUnixMs > time.Now().UnixMilli() {
		return CodexNativePreparation{}, ErrNativeInvalid
	}
	if ctx.Err() != nil || caller.Err() != nil {
		return CodexNativePreparation{}, ErrNativeCanceled
	}
	flow.codex = &codexFlowState{prepared: response}
	owned = true
	return CodexNativePreparation{codexClientOpaque: codexClientWrap(codexPreparedData{response: proto.Clone(response).(*nativev1.PrepareNativeApplyResponse), sessionExpires: flow.expires})}, nil
}

func codexProjectionMatchesBinding(prepared *nativev1.PrepareNativeApplyResponse, binding codexconfig.ProviderBinding, projection *nativev1.CodexLocalBindingV1) bool {
	if !validCodexLocal(projection) {
		return false
	}
	declaration, valid := codexDeclaration(prepared.CodexDeclaration)
	if !valid || !reflect.DeepEqual(declaration, binding.CopyDeclaration()) {
		return false
	}
	observation := binding.CopyObservation()
	if observation.ProfileID != projection.NativeProfileId || observation.TemplateDigest != [32]byte(projection.TemplateSha256) || observation.ProfileDigest == [32]byte{} || observation.Policy != agentconfig.CodexPreserveLocalLoginV1 || observation.RequiresOpenAIAuth != *projection.RequiresOpenaiAuth || observation.Witness != [32]byte(projection.ObservationWitness) || observation.Scope != codexconfig.CompleteEffective || !slices.Equal(observation.ManagedModels, projection.ManagedModels) {
		return false
	}
	ownership := func(value codexconfig.Ownership) nativev1.CodexOwnership {
		switch value {
		case codexconfig.Absent:
			return 1
		case codexconfig.Managed:
			return 2
		case codexconfig.Foreign:
			return 3
		default:
			return 4
		}
	}
	if ownership(observation.ProviderOwnership) != projection.ProviderOwnership || ownership(observation.CatalogOwnership) != projection.CatalogOwnership {
		return false
	}
	var store nativev1.CodexAuthStore
	switch observation.AuthStore {
	case codexconfig.File:
		store = 1
	case codexconfig.Keyring:
		store = 2
	case codexconfig.Auto:
		store = 3
	case codexconfig.Ephemeral:
		store = 4
	default:
		return false
	}
	var keyring nativev1.CodexKeyringState
	switch observation.Keyring {
	case codexconfig.FoundCredentialLogin:
		keyring = 1
	case codexconfig.KeyringMissing:
		keyring = 2
	case codexconfig.KeyringUnknown:
		keyring = 3
	case codexconfig.KeyringNotObserved:
		keyring = 4
	default:
		return false
	}
	var login nativev1.CodexFileLoginState
	switch observation.FileLogin {
	case codexconfig.CredentialLogin:
		login = 1
	case codexconfig.NoCredentialLogin:
		login = 2
	case codexconfig.LoginUnknown:
		login = 3
	case codexconfig.LoginNotObserved:
		login = 4
	default:
		return false
	}
	if projection.AuthStore != store || projection.KeyringState != keyring || projection.FileLoginState != login {
		return false
	}
	for index, baseline := range binding.CopyBaselines() {
		var role nativev1.CodexRole
		switch baseline.Role {
		case codexconfig.ConfigRole:
			role = 1
		case codexconfig.AuthRole:
			role = 2
		case codexconfig.CatalogRole:
			role = 3
		default:
			return false
		}
		wire := projection.Baselines[index]
		if role != nativev1.CodexRole(index+1) || wire.Role != role {
			return false
		}
		switch baseline.Presence {
		case codexconfig.Missing:
			if wire.GetMissing() == nil || baseline.Digest != [32]byte{} {
				return false
			}
		case codexconfig.Present:
			if wire.GetPresent() == nil || baseline.Digest != [32]byte(wire.GetPresent().Sha256) {
				return false
			}
		case codexconfig.NotObserved:
			if wire.GetNotObserved() == nil || baseline.Digest != [32]byte{} {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (flow *NativeFlow) AuthorizeCodex(caller context.Context, plan codexconfig.ProviderPlan, approval CodexNativeApproval) (CodexNativeAuthorization, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return CodexNativeAuthorization{}, err
	}
	defer done()
	if flow.agent != "codex" || flow.codex == nil || flow.codex.prepared == nil {
		return CodexNativeAuthorization{}, ErrNativeInvalid
	}
	if flow.codexAdmission == nil {
		return CodexNativeAuthorization{}, ErrNativeUnavailable
	}
	projection, err := flow.codexAdmission(ctx, plan)
	if err != nil {
		return CodexNativeAuthorization{}, ErrNativeUnavailable
	}
	binding := plan.Binding()
	defer binding.Destroy()
	if !codexProjectionMatchesBinding(flow.codex.prepared, binding, projection) {
		return CodexNativeAuthorization{}, ErrNativeInvalid
	}
	return flow.authorizeCodexStarted(ctx, caller, projection, approval)
}

func (flow *NativeFlow) authorizeCodex(caller context.Context, projection *nativev1.CodexLocalBindingV1, approval CodexNativeApproval) (CodexNativeAuthorization, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return CodexNativeAuthorization{}, err
	}
	defer done()
	return flow.authorizeCodexStarted(ctx, caller, projection, approval)
}
func (flow *NativeFlow) authorizeCodexStarted(ctx, caller context.Context, projection *nativev1.CodexLocalBindingV1, approval CodexNativeApproval) (CodexNativeAuthorization, error) {
	if flow.agent != "codex" || flow.codex == nil || flow.codex.prepared == nil || flow.codex.authorizeAttempted || !approval.present() || !validCodexLocal(projection) {
		return CodexNativeAuthorization{}, ErrNativeInvalid
	}
	state := flow.codex
	consent := approval.box.value
	preparation := state.prepared.Binding
	if consent.expires.UnixMilli() <= time.Now().UnixMilli() || consent.expires.UnixMilli() > preparation.PrepareExpiresAtUnixMs || consent.expires.After(flow.expires) {
		return CodexNativeAuthorization{}, ErrNativeInvalid
	}
	local := proto.Clone(projection).(*nativev1.CodexLocalBindingV1)
	if state.prepared.CodexDeclaration.Catalog != nil && (local.Baselines[2].GetNotObserved() != nil || local.CatalogOwnership == 3 || local.Baselines[2].GetMissing() != nil && local.CatalogOwnership != 1 || local.Baselines[2].GetPresent() != nil && local.CatalogOwnership != 2) {
		return CodexNativeAuthorization{}, ErrNativeInvalid
	}
	commitment, err := HashCodexIntent(preparation, local, consent.revision, consent.expires.UnixMilli())
	if err != nil {
		return CodexNativeAuthorization{}, ErrNativeInvalid
	}
	request := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: preparation.PrepareId, OperationId: bytes.Clone(preparation.OperationId), IntentCommitment: bytes.Clone(commitment[:]), RequestedSlots: []nativev1.SecretSlot{3}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: consent.revision}}, ClientNonce: nativeNonce(), IntentExpiresAtUnixMs: consent.expires.UnixMilli(), CodexLocalBinding: local}
	request.ProofSignature, err = flow.sign(CodexAuthorizeTranscript(preparation, state.prepared.AuthorizeChallenge, request))
	if err != nil {
		return CodexNativeAuthorization{}, err
	}
	expected := &nativev1.ApplyBinding{Preparation: proto.Clone(preparation).(*nativev1.PreparationBinding), IntentCommitment: bytes.Clone(request.IntentCommitment), IntentExpiresAtUnixMs: request.IntentExpiresAtUnixMs, CodexLocalBinding: proto.Clone(local).(*nativev1.CodexLocalBindingV1)}
	state.authorizeAttempted = true
	state.apply = expected
	response := new(nativev1.AuthorizeNativeMaterializationResponse)
	defer nativeClear(response)
	if flow.call(ctx, "AuthorizeNativeMaterialization", request, response) != nil {
		return CodexNativeAuthorization{}, ErrNativeUnknown
	}
	if !proto.Equal(response.Binding, expected) || !response.MaterializationRequired || response.ExpiresAtUnixMs > time.Now().Add(time.Minute).UnixMilli() || response.ExpiresAtUnixMs > flow.expires.UnixMilli() || response.ExpiresAtUnixMs <= time.Now().UnixMilli() || ctx.Err() != nil || caller.Err() != nil {
		return CodexNativeAuthorization{}, ErrNativeUnknown
	}
	flow.grant = newNativeSecret(response.GrantToken)
	state.grantID = response.GrantId
	state.consumeChallenge = bytes.Clone(response.ConsumeChallenge)
	state.grantExpiry = response.ExpiresAtUnixMs
	return CodexNativeAuthorization{codexClientOpaque: codexClientWrap(codexAuthorizationData{binding: proto.Clone(expected).(*nativev1.ApplyBinding), id: state.grantID, expires: time.UnixMilli(state.grantExpiry)})}, nil
}

func (flow *NativeFlow) ConsumeCodex(caller context.Context) (NativeMaterial, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return NativeMaterial{}, err
	}
	defer done()
	if flow.agent != "codex" || flow.codex == nil || flow.codex.apply == nil || flow.codex.grantID == "" || flow.codex.consumeAttempted || time.Now().UnixMilli() >= flow.codex.grantExpiry {
		return NativeMaterial{}, ErrNativeInvalid
	}
	state := flow.codex
	request := &nativev1.ConsumeNativeMaterializationRequest{PrepareId: state.prepared.Binding.PrepareId, GrantId: state.grantID, GrantToken: flow.grant.CloneBytes(), OperationId: bytes.Clone(flow.consumer.OperationID), IntentCommitment: bytes.Clone(state.apply.IntentCommitment), ClientNonce: nativeNonce()}
	defer nativeClear(request)
	request.ProofSignature, err = flow.sign(CodexConsumeTranscript(state.apply, state.prepared.AuthorizeChallenge, state.consumeChallenge, state.grantExpiry, request))
	if err != nil {
		return NativeMaterial{}, err
	}
	state.consumeAttempted = true
	defer flow.grant.Destroy()
	response := new(nativev1.ConsumeNativeMaterializationResponse)
	defer nativeClear(response)
	started := time.Now()
	if flow.call(ctx, "ConsumeNativeMaterialization", request, response) != nil {
		return NativeMaterial{}, ErrNativeUnknown
	}
	if !proto.Equal(response.Binding, state.apply) || response.GrantId != state.grantID || response.ConsumedAtUnixMs < started.UnixMilli() || response.ConsumedAtUnixMs > time.Now().UnixMilli() || response.ConsumedAtUnixMs >= state.grantExpiry || ctx.Err() != nil || caller.Err() != nil {
		return NativeMaterial{}, ErrNativeUnknown
	}
	material := NativeMaterial{newNativeSecret(response.SlotValues[0].Plaintext)}
	if ctx.Err() != nil || caller.Err() != nil {
		material.Destroy()
		return NativeMaterial{}, ErrNativeUnknown
	}
	return material, nil
}
func (flow *NativeFlow) StatusCodex(caller context.Context) (NativeStatus, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return NativeStatus{}, err
	}
	defer done()
	if flow.agent != "codex" || flow.codex == nil || flow.codex.prepared == nil {
		return NativeStatus{}, ErrNativeInvalid
	}
	state := flow.codex
	request := &nativev1.GetNativeMaterializationStatusRequest{PrepareId: state.prepared.Binding.PrepareId, GrantId: state.grantID, OperationId: bytes.Clone(flow.consumer.OperationID), ClientNonce: nativeNonce()}
	request.ProofSignature, err = flow.sign(CodexStatusTranscript(state.prepared.Binding, state.prepared.AuthorizeChallenge, request))
	if err != nil {
		return NativeStatus{}, err
	}
	response := new(nativev1.GetNativeMaterializationStatusResponse)
	if err := flow.call(ctx, "GetNativeMaterializationStatus", request, response); err != nil {
		return NativeStatus{}, err
	}
	expiry := state.prepared.Binding.PrepareExpiresAtUnixMs
	if state.apply != nil {
		expiry = min(expiry, state.apply.IntentExpiresAtUnixMs)
	}
	if state.grantID != "" && response.GrantId != state.grantID || response.ExpiresAtUnixMs > expiry || response.ExpiresAtUnixMs > flow.expires.UnixMilli() || state.grantExpiry != 0 && response.ExpiresAtUnixMs != state.grantExpiry || response.ConsumedAtUnixMs > time.Now().UnixMilli() || response.ConsumedAtUnixMs != 0 && (response.ConsumedAtUnixMs < state.prepared.Binding.PreparedAtUnixMs || response.ConsumedAtUnixMs >= response.ExpiresAtUnixMs) || ctx.Err() != nil || caller.Err() != nil {
		return NativeStatus{}, ErrNativeInvalid
	}
	states := []NativeGrantStatus{"", NativeNotIssued, NativeIssued, NativeConsumed, NativeExpired, NativeRetired}
	result := NativeStatus{State: states[int(response.State)]}
	if response.ExpiresAtUnixMs > 0 {
		result.ExpiresAt = time.UnixMilli(response.ExpiresAtUnixMs)
	}
	if response.ConsumedAtUnixMs > 0 {
		result.ConsumedAt = time.UnixMilli(response.ConsumedAtUnixMs)
	}
	return result, nil
}
