package auth

import (
	"bytes"
	"context"
	"crypto/rand"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
)

type issuedCodexNativeGrant struct {
	raw           []byte
	authorization state.NativeCodexAuthorization
}
type IssuedCodexNativeGrant struct {
	nativeAuthOpaque[issuedCodexNativeGrant]
}

func (value IssuedCodexNativeGrant) Reveal() []byte {
	if !value.present() {
		return nil
	}
	return bytes.Clone(value.box.value.raw)
}
func (value IssuedCodexNativeGrant) Binding() nativesessionauth.CodexApply {
	if !value.present() {
		return nativesessionauth.CodexApply{}
	}
	return value.box.value.authorization.Binding()
}
func (value IssuedCodexNativeGrant) ID() string {
	if !value.present() {
		return ""
	}
	return value.box.value.authorization.ID()
}
func (value IssuedCodexNativeGrant) Challenge() [32]byte {
	if !value.present() {
		return [32]byte{}
	}
	return value.box.value.authorization.Challenge()
}
func (value IssuedCodexNativeGrant) ExpiresMS() int64 {
	if !value.present() {
		return 0
	}
	return value.box.value.authorization.ExpiresMS()
}
func (value IssuedCodexNativeGrant) Destroy() {
	if value.present() {
		clear(value.box.value.raw)
		value.box.value.raw = nil
	}
}

func (service *NativeConsumerService) CodexConfigurationSnapshot(ctx context.Context, raw string, operation [16]byte, prepareID string) (state.NativeCodexApplySnapshot, error) {
	credential, _, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeCodexApplySnapshot{}, err
	}
	config := service.box.value
	return config.owner.store.NativeCodexConfigurationSnapshot(ctx, credential, config.origin, operation, prepareID)
}
func (service *NativeConsumerService) ConfigurationProtocol(ctx context.Context, raw string, operation [16]byte, prepareID string) (uint32, error) {
	credential, _, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return 0, err
	}
	config := service.box.value
	return config.owner.store.NativeConfigurationProtocol(ctx, credential, config.origin, operation, prepareID)
}

func (service *NativeConsumerService) codexPrepareEvidence(ctx context.Context, raw string, request nativesessionauth.CodexPrepareRequest, signature []byte) (state.NativeConsumerCredential, nativesessionauth.VerifiedApplyProof, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeCodexConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, "")
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	proof, err := config.verifier.VerifyCodexPrepare(parsed, nativesessionauth.CodexPrepareFields{Consumer: snapshot.Consumer(), Request: request}, signature)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, state.ErrNativeUnauthorized
	}
	return credential, proof, nil
}
func (service *NativeConsumerService) PrepareCodex(ctx context.Context, raw string, request nativesessionauth.CodexPrepareRequest, signature []byte, validate state.NativeCodexPrepareValidator) (state.NativeCodexPrepared, error) {
	credential, proof, err := service.codexPrepareEvidence(ctx, raw, request, signature)
	if err != nil {
		return state.NativeCodexPrepared{}, err
	}
	config := service.box.value
	prepared, err := config.owner.store.PrepareNativeCodexApply(ctx, credential, config.origin, request, proof, validate)
	if err != nil {
		return state.NativeCodexPrepared{}, err
	}
	if err := ctx.Err(); err != nil {
		return state.NativeCodexPrepared{}, err
	}
	return prepared, nil
}
func (service *NativeConsumerService) PreflightCodexPrepare(ctx context.Context, raw string, request nativesessionauth.CodexPrepareRequest, signature []byte) (state.NativeCodexApplySnapshot, error) {
	credential, proof, err := service.codexPrepareEvidence(ctx, raw, request, signature)
	if err != nil {
		return state.NativeCodexApplySnapshot{}, err
	}
	config := service.box.value
	return config.owner.store.PreflightNativeCodexPrepare(ctx, credential, config.origin, request, proof)
}
func (service *NativeConsumerService) codexAuthorizeEvidence(ctx context.Context, raw string, request nativesessionauth.CodexAuthorizeRequest, signature []byte) (state.NativeConsumerCredential, nativesessionauth.VerifiedApplyProof, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeCodexConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	proof, err := config.verifier.VerifyCodexAuthorize(parsed, snapshot.AuthorizeFields(request), signature)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, state.ErrNativeUnauthorized
	}
	return credential, proof, nil
}
func (service *NativeConsumerService) PreflightCodexAuthorize(ctx context.Context, raw string, request nativesessionauth.CodexAuthorizeRequest, signature []byte) (state.NativeCodexApplySnapshot, error) {
	credential, proof, err := service.codexAuthorizeEvidence(ctx, raw, request, signature)
	if err != nil {
		return state.NativeCodexApplySnapshot{}, err
	}
	config := service.box.value
	return config.owner.store.PreflightNativeCodexAuthorize(ctx, credential, config.origin, request, proof)
}
func (service *NativeConsumerService) AuthorizeCodex(ctx context.Context, raw string, request nativesessionauth.CodexAuthorizeRequest, signature []byte, validate state.NativeCodexPrepareValidator) (IssuedCodexNativeGrant, error) {
	credential, proof, err := service.codexAuthorizeEvidence(ctx, raw, request, signature)
	if err != nil {
		return IssuedCodexNativeGrant{}, err
	}
	config := service.box.value
	return nativeIssueCodexGrant(ctx, func(token nativesessionauth.ParsedGrantToken) (state.NativeCodexAuthorization, error) {
		return config.owner.store.AuthorizeNativeCodexGrant(ctx, credential, config.origin, request, proof, token, validate)
	})
}

func nativeIssueCodexGrant(ctx context.Context, issue func(nativesessionauth.ParsedGrantToken) (state.NativeCodexAuthorization, error)) (IssuedCodexNativeGrant, error) {
	var scratch [32]byte
	defer clear(scratch[:])
	_, _ = rand.Read(scratch[:])
	token, err := nativesessionauth.ParseGrantToken(scratch[:])
	if err != nil {
		return IssuedCodexNativeGrant{}, state.ErrNativeUnavailable
	}
	result, err := issue(token)
	if err != nil {
		return IssuedCodexNativeGrant{}, err
	}
	if result.ID() == "" || result.Binding().CopyWire() == nil {
		return IssuedCodexNativeGrant{}, state.ErrNativeUnavailable
	}
	if err := ctx.Err(); err != nil {
		return IssuedCodexNativeGrant{}, err
	}
	return IssuedCodexNativeGrant{nativeAuthOpaque: nativeAuthWrap(issuedCodexNativeGrant{raw: bytes.Clone(scratch[:]), authorization: result})}, nil
}
func (service *NativeConsumerService) codexConsumeEvidence(ctx context.Context, raw string, request nativesessionauth.ConsumeRequest, signature []byte) (state.NativeConsumerCredential, nativesessionauth.VerifiedApplyProof, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeCodexConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, err
	}
	proof, err := config.verifier.VerifyCodexConsume(parsed, snapshot.ConsumeFields(request), signature)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.VerifiedApplyProof{}, state.ErrNativeUnauthorized
	}
	return credential, proof, nil
}
func (service *NativeConsumerService) PreflightCodexConsume(ctx context.Context, raw string, request nativesessionauth.ConsumeRequest, signature []byte) (state.NativeCodexApplySnapshot, error) {
	credential, proof, err := service.codexConsumeEvidence(ctx, raw, request, signature)
	if err != nil {
		return state.NativeCodexApplySnapshot{}, err
	}
	config := service.box.value
	return config.owner.store.PreflightNativeCodexConsume(ctx, credential, config.origin, request, proof)
}
func (service *NativeConsumerService) ConsumeCodex(ctx context.Context, raw string, request nativesessionauth.ConsumeRequest, signature []byte, materialize state.NativeCodexMaterializer) (state.NativeMaterialization, error) {
	credential, proof, err := service.codexConsumeEvidence(ctx, raw, request, signature)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	config := service.box.value
	result, err := config.owner.store.ConsumeNativeCodexGrant(ctx, credential, config.origin, request, proof, materialize)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	if err := ctx.Err(); err != nil {
		result.Destroy()
		return state.NativeMaterialization{}, err
	}
	return result, nil
}
func (service *NativeConsumerService) StatusCodex(ctx context.Context, raw string, request nativesessionauth.StatusRequest, signature []byte) (state.NativeGrantStatus, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeGrantStatus{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeCodexConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return state.NativeGrantStatus{}, err
	}
	proof, err := config.verifier.VerifyCodexStatus(parsed, snapshot.StatusFields(request), signature)
	if err != nil {
		return state.NativeGrantStatus{}, state.ErrNativeUnauthorized
	}
	return config.owner.store.NativeCodexGrantStatus(ctx, credential, config.origin, request, proof)
}
