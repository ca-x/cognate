package auth

import (
	"bytes"
	"context"
	"crypto/rand"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
)

type issuedNativeGrant struct {
	raw           []byte
	authorization state.NativeAuthorization
}
type IssuedNativeGrant struct {
	nativeAuthOpaque[issuedNativeGrant]
}

func (value IssuedNativeGrant) Reveal() []byte {
	if !value.present() {
		return nil
	}
	return bytes.Clone(value.box.value.raw)
}
func (value IssuedNativeGrant) Binding() nativesessionauth.ApplyFields {
	if !value.present() {
		return nativesessionauth.ApplyFields{}
	}
	return value.box.value.authorization.Binding()
}
func (value IssuedNativeGrant) ID() string {
	if !value.present() {
		return ""
	}
	return value.box.value.authorization.ID()
}
func (value IssuedNativeGrant) Challenge() [32]byte {
	if !value.present() {
		return [32]byte{}
	}
	return value.box.value.authorization.Challenge()
}
func (value IssuedNativeGrant) ExpiresMS() int64 {
	if !value.present() {
		return 0
	}
	return value.box.value.authorization.ExpiresMS()
}
func (value IssuedNativeGrant) Destroy() {
	if value.present() {
		clear(value.box.value.raw)
		value.box.value.raw = nil
	}
}

func (service *NativeConsumerService) Authorize(ctx context.Context, raw string, request nativesessionauth.AuthorizeRequest, signature []byte, validate state.NativePrepareValidator) (IssuedNativeGrant, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return IssuedNativeGrant{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return IssuedNativeGrant{}, err
	}
	proof, err := config.verifier.VerifyAuthorize(parsed, snapshot.AuthorizeFields(request), signature)
	if err != nil {
		return IssuedNativeGrant{}, state.ErrNativeUnauthorized
	}
	return nativeIssueGrant(ctx, func(token nativesessionauth.ParsedGrantToken) (state.NativeAuthorization, error) {
		return config.owner.store.AuthorizeNativeConfigurationGrant(ctx, credential, config.origin, request, proof, token, validate)
	})
}

func nativeIssueGrant(ctx context.Context, issue func(nativesessionauth.ParsedGrantToken) (state.NativeAuthorization, error)) (IssuedNativeGrant, error) {
	var scratch [32]byte
	defer clear(scratch[:])
	_, _ = rand.Read(scratch[:])
	token, err := nativesessionauth.ParseGrantToken(scratch[:])
	if err != nil {
		return IssuedNativeGrant{}, state.ErrNativeUnavailable
	}
	result, err := issue(token)
	if err != nil {
		return IssuedNativeGrant{}, err
	}
	if result.Binding().Preparation.PrepareID == "" {
		return IssuedNativeGrant{}, state.ErrNativeUnavailable
	}
	var grant []byte
	if result.ID() != "" {
		grant = bytes.Clone(scratch[:])
	}
	if err := ctx.Err(); err != nil {
		clear(grant)
		return IssuedNativeGrant{}, err
	}
	return IssuedNativeGrant{nativeAuthOpaque: nativeAuthWrap(issuedNativeGrant{raw: grant, authorization: result})}, nil
}

func (service *NativeConsumerService) Status(ctx context.Context, raw string, request nativesessionauth.StatusRequest, signature []byte) (state.NativeGrantStatus, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeGrantStatus{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return state.NativeGrantStatus{}, err
	}
	proof, err := config.verifier.VerifyStatus(parsed, snapshot.StatusFields(request), signature)
	if err != nil {
		return state.NativeGrantStatus{}, state.ErrNativeUnauthorized
	}
	return config.owner.store.NativeConfigurationGrantStatus(ctx, credential, config.origin, request, proof)
}

func (service *NativeConsumerService) ConfigurationSnapshot(ctx context.Context, raw string, operation [16]byte, prepareID string) (state.NativeApplySnapshot, error) {
	credential, _, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeApplySnapshot{}, err
	}
	config := service.box.value
	return config.owner.store.NativeConfigurationSnapshot(ctx, credential, config.origin, operation, prepareID)
}

func (service *NativeConsumerService) PreflightPrepare(ctx context.Context, raw string, request nativesessionauth.PrepareRequest, signature []byte) (state.NativeApplySnapshot, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeApplySnapshot{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, "")
	if err != nil {
		return state.NativeApplySnapshot{}, err
	}
	proof, err := config.verifier.VerifyPrepare(parsed, nativesessionauth.PrepareFields{Consumer: snapshot.Consumer(), Request: request}, signature)
	if err != nil {
		return state.NativeApplySnapshot{}, state.ErrNativeUnauthorized
	}
	return config.owner.store.PreflightNativeConfigurationPrepare(ctx, credential, config.origin, request, proof)
}

func (service *NativeConsumerService) PreflightAuthorize(ctx context.Context, raw string, request nativesessionauth.AuthorizeRequest, signature []byte) (state.NativeApplySnapshot, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeApplySnapshot{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return state.NativeApplySnapshot{}, err
	}
	proof, err := config.verifier.VerifyAuthorize(parsed, snapshot.AuthorizeFields(request), signature)
	if err != nil {
		return state.NativeApplySnapshot{}, state.ErrNativeUnauthorized
	}
	return config.owner.store.PreflightNativeConfigurationAuthorize(ctx, credential, config.origin, request, proof)
}

func (service *NativeConsumerService) PreflightConsume(ctx context.Context, raw string, request nativesessionauth.ConsumeRequest, signature []byte) (state.NativeApplySnapshot, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeApplySnapshot{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return state.NativeApplySnapshot{}, err
	}
	proof, err := config.verifier.VerifyConsume(parsed, snapshot.ConsumeFields(request), signature)
	if err != nil {
		return state.NativeApplySnapshot{}, state.ErrNativeUnauthorized
	}
	return config.owner.store.PreflightNativeConfigurationConsume(ctx, credential, config.origin, request, proof)
}

func (service *NativeConsumerService) OwnsConfigurationStore(store *state.Store) bool {
	return service != nil && service.present() && service.box.value.owner.store == store
}

func (service *NativeConsumerService) Consume(ctx context.Context, raw string, request nativesessionauth.ConsumeRequest, signature []byte, materialize state.NativeMaterializer) (state.NativeMaterialization, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, request.PrepareID)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	proof, err := config.verifier.VerifyConsume(parsed, snapshot.ConsumeFields(request), signature)
	if err != nil {
		return state.NativeMaterialization{}, state.ErrNativeUnauthorized
	}
	response, err := config.owner.store.ConsumeNativeConfigurationGrant(ctx, credential, config.origin, request, proof, materialize)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	if err := ctx.Err(); err != nil {
		response.Destroy()
		return state.NativeMaterialization{}, err
	}
	return response, nil
}

func (service *NativeConsumerService) configurationCredential(ctx context.Context, raw string) (state.NativeConsumerCredential, nativesessionauth.ParsedNativeBearer, error) {
	if err := ctx.Err(); err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.ParsedNativeBearer{}, err
	}
	if service == nil || !service.present() {
		return state.NativeConsumerCredential{}, nativesessionauth.ParsedNativeBearer{}, state.ErrNativeUnauthorized
	}
	parsed, err := nativesessionauth.ParseNativeBearer(raw)
	if err != nil {
		return state.NativeConsumerCredential{}, nativesessionauth.ParsedNativeBearer{}, state.ErrNativeUnauthorized
	}
	credential, err := service.box.value.owner.store.NativeCredentialFromBearer(parsed)
	return credential, parsed, err
}

func (service *NativeConsumerService) Prepare(ctx context.Context, raw string, request nativesessionauth.PrepareRequest, signature []byte, validate state.NativePrepareValidator) (state.NativePrepared, error) {
	credential, parsed, err := service.configurationCredential(ctx, raw)
	if err != nil {
		return state.NativePrepared{}, err
	}
	config := service.box.value
	snapshot, err := config.owner.store.NativeConfigurationSnapshot(ctx, credential, config.origin, request.OperationID, "")
	if err != nil {
		return state.NativePrepared{}, err
	}
	proof, err := config.verifier.VerifyPrepare(parsed, nativesessionauth.PrepareFields{Consumer: snapshot.Consumer(), Request: request}, signature)
	if err != nil {
		return state.NativePrepared{}, state.ErrNativeUnauthorized
	}
	prepared, err := config.owner.store.PrepareNativeConfigurationApply(ctx, credential, config.origin, request, proof, validate)
	if err != nil {
		return state.NativePrepared{}, err
	}
	if err := ctx.Err(); err != nil {
		return state.NativePrepared{}, err
	}
	return prepared, nil
}
