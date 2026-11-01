package auth

import (
	"bytes"
	"context"
	"crypto/rand"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
)

type issuedGeminiNativeGrant struct {
	raw           []byte
	authorization state.NativeGeminiAuthorization
}
type IssuedGeminiNativeGrant struct {
	nativeAuthOpaque[issuedGeminiNativeGrant]
}

func (value IssuedGeminiNativeGrant) Reveal() []byte {
	if !value.present() {
		return nil
	}
	return bytes.Clone(value.box.value.raw)
}
func (value IssuedGeminiNativeGrant) Binding() nativesessionauth.GeminiApply {
	if !value.present() {
		return nativesessionauth.GeminiApply{}
	}
	return value.box.value.authorization.Binding()
}
func (value IssuedGeminiNativeGrant) ID() string {
	if !value.present() {
		return ""
	}
	return value.box.value.authorization.ID()
}
func (value IssuedGeminiNativeGrant) Challenge() [32]byte {
	if !value.present() {
		return [32]byte{}
	}
	return value.box.value.authorization.Challenge()
}
func (value IssuedGeminiNativeGrant) ExpiresMS() int64 {
	if !value.present() {
		return 0
	}
	return value.box.value.authorization.ExpiresMS()
}
func (value IssuedGeminiNativeGrant) Destroy() {
	if value.present() {
		clear(value.box.value.raw)
		value.box.value.raw = nil
	}
}

func (service *NativeConsumerService) GeminiConfigurationSnapshot(ctx context.Context, raw string, operation [16]byte, prepareID string) (state.NativeGeminiApplySnapshot, error) {
	credential, _, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeGeminiApplySnapshot{}, err
	}
	config := service.box.value
	return config.owner.store.NativeGeminiConfigurationSnapshot(ctx, credential, config.origin, operation, prepareID)
}
func (service *NativeConsumerService) geminiPrepareEvidence(ctx context.Context, raw string, request nativesessionauth.GeminiPrepareRequest, signature []byte) (state.NativeConsumerCredential, nativesessionauth.VerifiedApplyProof, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeGeminiConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, "")
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	proof, err := config.verifier.VerifyGeminiPrepare(parsed, nativesessionauth.GeminiPrepareFields{Consumer: snapshot.Consumer(), Request: request}, signature)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, state.ErrNativeUnauthorized
	}
	return credential, proof, nil
}
func (service *NativeConsumerService) PrepareGemini(ctx context.Context, raw string, request nativesessionauth.GeminiPrepareRequest, signature []byte, validate state.NativeGeminiPrepareValidator) (state.NativeGeminiPrepared, error) {
	credential, proof, err := service.geminiPrepareEvidence(ctx, raw, request, signature)
	if err != nil {
		return state.NativeGeminiPrepared{}, err
	}
	config := service.box.value
	prepared, err := config.owner.store.PrepareNativeGeminiApply(ctx, credential, config.origin, request, proof, validate)
	if err != nil {
		return state.NativeGeminiPrepared{}, err
	}
	if err := ctx.Err(); err != nil {
		return state.NativeGeminiPrepared{}, err
	}
	return prepared, nil
}
func (service *NativeConsumerService) PreflightGeminiPrepare(ctx context.Context, raw string, request nativesessionauth.GeminiPrepareRequest, signature []byte) (state.NativeGeminiApplySnapshot, error) {
	credential, proof, err := service.geminiPrepareEvidence(ctx, raw, request, signature)
	if err != nil {
		return state.NativeGeminiApplySnapshot{}, err
	}
	config := service.box.value
	return config.owner.store.PreflightNativeGeminiPrepare(ctx, credential, config.origin, request, proof)
}
func (service *NativeConsumerService) geminiAuthorizeEvidence(ctx context.Context, raw string, request nativesessionauth.GeminiAuthorizeRequest, signature []byte) (state.NativeConsumerCredential, nativesessionauth.VerifiedApplyProof, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeGeminiConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	proof, err := config.verifier.VerifyGeminiAuthorize(parsed, snapshot.AuthorizeFields(request), signature)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, state.ErrNativeUnauthorized
	}
	return credential, proof, nil
}
func (service *NativeConsumerService) PreflightGeminiAuthorize(ctx context.Context, raw string, request nativesessionauth.GeminiAuthorizeRequest, signature []byte) (state.NativeGeminiApplySnapshot, error) {
	credential, proof, err := service.geminiAuthorizeEvidence(ctx, raw, request, signature)
	if err != nil {
		return state.NativeGeminiApplySnapshot{}, err
	}
	config := service.box.value
	return config.owner.store.PreflightNativeGeminiAuthorize(ctx, credential, config.origin, request, proof)
}
func (service *NativeConsumerService) AuthorizeGemini(ctx context.Context, raw string, request nativesessionauth.GeminiAuthorizeRequest, signature []byte, validate state.NativeGeminiPrepareValidator) (IssuedGeminiNativeGrant, error) {
	credential, proof, err := service.geminiAuthorizeEvidence(ctx, raw, request, signature)
	if err != nil {
		return IssuedGeminiNativeGrant{}, err
	}
	config := service.box.value
	return nativeIssueGeminiGrant(ctx, func(token nativesessionauth.ParsedGrantToken) (state.NativeGeminiAuthorization, error) {
		return config.owner.store.AuthorizeNativeGeminiGrant(ctx, credential, config.origin, request, proof, token, validate)
	})
}

func nativeIssueGeminiGrant(ctx context.Context, issue func(nativesessionauth.ParsedGrantToken) (state.NativeGeminiAuthorization, error)) (IssuedGeminiNativeGrant, error) {
	var scratch [32]byte
	defer clear(scratch[:])
	_, _ = rand.Read(scratch[:])
	token, err := nativesessionauth.ParseGrantToken(scratch[:])
	if err != nil {
		return IssuedGeminiNativeGrant{}, state.ErrNativeUnavailable
	}
	result, err := issue(token)
	if err != nil {
		return IssuedGeminiNativeGrant{}, err
	}
	if result.Binding().CopyWire() == nil || (len(result.Binding().CopyWire().Preparation.RequiredSlots) == 1) != (result.ID() != "") {
		return IssuedGeminiNativeGrant{}, state.ErrNativeUnavailable
	}
	if err := ctx.Err(); err != nil {
		return IssuedGeminiNativeGrant{}, err
	}
	var raw []byte
	if result.ID() != "" {
		raw = bytes.Clone(scratch[:])
	}
	return IssuedGeminiNativeGrant{nativeAuthOpaque: nativeAuthWrap(issuedGeminiNativeGrant{raw: raw, authorization: result})}, nil
}
func (service *NativeConsumerService) geminiConsumeEvidence(ctx context.Context, raw string, request nativesessionauth.ConsumeRequest, signature []byte) (state.NativeConsumerCredential, nativesessionauth.VerifiedApplyProof, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeGeminiConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	proof, err := config.verifier.VerifyGeminiConsume(parsed, snapshot.ConsumeFields(request), signature)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, state.ErrNativeUnauthorized
	}
	return credential, proof, nil
}
func (service *NativeConsumerService) PreflightGeminiConsume(ctx context.Context, raw string, request nativesessionauth.ConsumeRequest, signature []byte) (state.NativeGeminiApplySnapshot, error) {
	credential, proof, err := service.geminiConsumeEvidence(ctx, raw, request, signature)
	if err != nil {
		return state.NativeGeminiApplySnapshot{}, err
	}
	config := service.box.value
	return config.owner.store.PreflightNativeGeminiConsume(ctx, credential, config.origin, request, proof)
}
func (service *NativeConsumerService) ConsumeGemini(ctx context.Context, raw string, request nativesessionauth.ConsumeRequest, signature []byte, materialize state.NativeGeminiMaterializer) (state.NativeMaterialization, error) {
	credential, proof, err := service.geminiConsumeEvidence(ctx, raw, request, signature)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	config := service.box.value
	result, err := config.owner.store.ConsumeNativeGeminiGrant(ctx, credential, config.origin, request, proof, materialize)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	if err := ctx.Err(); err != nil {
		result.Destroy()
		return state.NativeMaterialization{}, err
	}
	return result, nil
}
func (service *NativeConsumerService) StatusGemini(ctx context.Context, raw string, request nativesessionauth.StatusRequest, signature []byte) (state.NativeGrantStatus, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeGrantStatus{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeGeminiConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return state.NativeGrantStatus{}, err
	}
	proof, err := config.verifier.VerifyGeminiStatus(parsed, snapshot.StatusFields(request), signature)
	if err != nil {
		return state.NativeGrantStatus{}, state.ErrNativeUnauthorized
	}
	return config.owner.store.NativeGeminiGrantStatus(ctx, credential, config.origin, request, proof)
}
