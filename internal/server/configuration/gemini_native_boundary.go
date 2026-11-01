package configuration

import (
	"bytes"
	"context"
	"reflect"
	"unicode/utf8"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

func nativeGeminiDesired(payload state.ConfigurationPayload) (agentconfig.GeminiProviderDesired, error) {
	provider, secret, extras, err := decodeGeminiEnvelope(payload)
	defer secret.Clear()
	if err != nil || extras {
		return agentconfig.GeminiProviderDesired{}, state.ErrNativePrecondition
	}
	if provider.AuthMode == agentconfig.GeminiAPIKeyAuth && !nativeGeminiSecretValid(secretBytes(secret)) {
		return agentconfig.GeminiProviderDesired{}, state.ErrNativePrecondition
	}
	cloned := cloneGeminiProvider(provider)
	return agentconfig.GeminiProviderDesired{AuthMode: cloned.AuthMode, Endpoint: cloned.Endpoint, KeyTransport: cloned.KeyTransport, EnvModel: cloned.EnvModel, SettingsModel: cloned.SettingsModel, KeyPresent: provider.AuthMode == agentconfig.GeminiAPIKeyAuth}, nil
}
func nativeGeminiSecretValid(key []byte) bool {
	return len(key) > 0 && len(key) <= 8192 && utf8.Valid(key) && !bytes.ContainsAny(key, "\x00\r\n")
}

func (service *NativeService) nativeGeminiPlan(ctx context.Context, request nativesessionauth.GeminiPrepareRequest) (state.NativeGeminiPrepareValidator, agentconfig.GeminiProviderDeclarationV1, error) {
	metadata, payload, err := service.domain.loadEditable(ctx, request.OwnerID, request.AssetID)
	if err != nil {
		return nil, agentconfig.GeminiProviderDeclarationV1{}, err
	}
	if metadata.Revision != request.AssetRevision {
		return nil, agentconfig.GeminiProviderDeclarationV1{}, state.ErrConfigurationConflict
	}
	if metadata.AgentID != "gemini" || request.AgentID != "gemini" || metadata.SchemaVersion != 1 || metadata.Kind != state.ConfigurationKindProvider {
		return nil, agentconfig.GeminiProviderDeclarationV1{}, state.ErrNativePrecondition
	}
	var trusted nativesessionauth.AdapterFields
	for _, descriptor := range service.registry.List() {
		if descriptor.Manifest.PluginID == "builtin.gemini" {
			trusted = nativesessionauth.AdapterFields{APIVersion: uint32(descriptor.Manifest.APIVersion), PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Capability: "gemini.provider-plan.v1"}
		}
	}
	if trusted != request.Adapter {
		return nil, agentconfig.GeminiProviderDeclarationV1{}, state.ErrNativePrecondition
	}
	desired, err := nativeGeminiDesired(payload)
	if err != nil {
		return nil, agentconfig.GeminiProviderDeclarationV1{}, err
	}
	declaration, err := service.registry.PlanGeminiProvider(ctx, agentconfig.GeminiProviderPlanRequest{APIVersion: int(trusted.APIVersion), PluginID: trusted.PluginID, PluginVersion: trusted.PluginVersion, DefinitionDigest: trusted.DefinitionDigest, Target: agentconfig.GeminiSettings, Capabilities: []agentconfig.Capability{agentconfig.GeminiProviderPlanV1}, Desired: desired})
	if err != nil {
		if ctx.Err() != nil {
			return nil, agentconfig.GeminiProviderDeclarationV1{}, ctx.Err()
		}
		return nil, agentconfig.GeminiProviderDeclarationV1{}, state.ErrNativePrecondition
	}
	data, err := state.NewNativeGeminiPreparedData(trusted, declaration)
	if err != nil {
		return nil, agentconfig.GeminiProviderDeclarationV1{}, err
	}
	validate := func(ctx context.Context, current state.ConfigurationAsset, payload state.ConfigurationPayload) (state.NativeGeminiPreparedData, error) {
		if err := ctx.Err(); err != nil {
			return state.NativeGeminiPreparedData{}, err
		}
		actual, err := nativeGeminiDesired(payload)
		if err != nil {
			return state.NativeGeminiPreparedData{}, err
		}
		if current != metadata || !reflect.DeepEqual(actual, desired) {
			return state.NativeGeminiPreparedData{}, state.ErrConfigurationConflict
		}
		return data, nil
	}
	return validate, declaration, nil
}

func (service *NativeService) PrepareGemini(ctx context.Context, bearer string, request nativesessionauth.GeminiPrepareRequest, signature []byte) (state.NativeGeminiPrepared, error) {
	if service == nil || service.consumer == nil {
		return state.NativeGeminiPrepared{}, state.ErrNativeUnauthorized
	}
	if _, err := service.consumer.PreflightGeminiPrepare(ctx, bearer, request, signature); err != nil {
		return state.NativeGeminiPrepared{}, err
	}
	validate, _, err := service.nativeGeminiPlan(ctx, request)
	if err != nil {
		return state.NativeGeminiPrepared{}, err
	}
	return service.consumer.PrepareGemini(ctx, bearer, request, signature, validate)
}
func (service *NativeService) AuthorizeGemini(ctx context.Context, bearer string, request nativesessionauth.AuthorizeRequest, wire *nativev1.GeminiLocalBindingV1, signature []byte) (auth.IssuedGeminiNativeGrant, error) {
	if service == nil || service.consumer == nil {
		return auth.IssuedGeminiNativeGrant{}, state.ErrNativeUnauthorized
	}
	snapshot, err := service.consumer.GeminiConfigurationSnapshot(ctx, bearer, request.OperationID, request.PrepareID)
	if err != nil {
		return auth.IssuedGeminiNativeGrant{}, err
	}
	preparation := snapshot.Preparation()
	local, err := nativesessionauth.NewGeminiLocalBinding([32]byte(preparation.CopyWire().Gemini.DeclarationSha256), wire)
	if err != nil {
		return auth.IssuedGeminiNativeGrant{}, state.ErrNativeInvalid
	}
	input := nativesessionauth.GeminiAuthorizeRequest{AuthorizeRequest: request, Local: local}
	if _, err := service.consumer.PreflightGeminiAuthorize(ctx, bearer, input, signature); err != nil {
		return auth.IssuedGeminiNativeGrant{}, err
	}
	validate, _, err := service.nativeGeminiPlan(ctx, preparation.CopyRequest())
	if err != nil {
		return auth.IssuedGeminiNativeGrant{}, err
	}
	return service.consumer.AuthorizeGemini(ctx, bearer, input, signature, validate)
}
func (service *NativeService) consumeGemini(ctx context.Context, bearer string, request nativesessionauth.ConsumeRequest, signature []byte) (state.NativeMaterialization, error) {
	snapshot, err := service.consumer.PreflightGeminiConsume(ctx, bearer, request, signature)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	apply := snapshot.ConsumeFields(request).Apply
	validate, declaration, err := service.nativeGeminiPlan(ctx, apply.Preparation().CopyRequest())
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	expected, err := nativesessionauth.HashGeminiDeclaration(declaration)
	if err != nil {
		return state.NativeMaterialization{}, state.ErrNativePrecondition
	}
	materialize := func(ctx context.Context, metadata state.ConfigurationAsset, payload state.ConfigurationPayload, binding nativesessionauth.GeminiApply, grantID string, consumedMS int64) (state.NativeMaterialization, error) {
		if _, err := validate(ctx, metadata, payload); err != nil {
			return state.NativeMaterialization{}, err
		}
		wire := binding.CopyWire()
		if wire == nil || !bytes.Equal(wire.Preparation.Gemini.DeclarationSha256, expected[:]) || binding.Preparation().CopyRequest().Adapter != apply.Preparation().CopyRequest().Adapter {
			return state.NativeMaterialization{}, state.ErrNativePrecondition
		}
		provider, secret, extras, err := decodeGeminiEnvelope(payload)
		defer secret.Clear()
		if err != nil || extras || provider.AuthMode != agentconfig.GeminiAPIKeyAuth {
			return state.NativeMaterialization{}, state.ErrNativePrecondition
		}
		selected := secretBytes(secret)
		if !nativeGeminiSecretValid(selected) {
			return state.NativeMaterialization{}, state.ErrNativePrecondition
		}
		encoded, err := proto.Marshal(&nativev1.ConsumeNativeMaterializationResponse{Binding: wire, SlotValues: []*nativev1.SlotValue{{Slot: 4, Plaintext: selected}}, GrantId: grantID, ConsumedAtUnixMs: consumedMS})
		defer clear(encoded)
		if err != nil {
			return state.NativeMaterialization{}, state.ErrNativeUnavailable
		}
		return state.NewNativeMaterialization(encoded)
	}
	return service.consumer.ConsumeGemini(ctx, bearer, request, signature, materialize)
}
