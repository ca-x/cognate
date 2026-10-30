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

func nativeCodexDesired(payload state.ConfigurationPayload) (agentconfig.CodexProviderDesired, error) {
	provider, secret, extras, err := decodeCodexEnvelope(payload)
	defer secret.Clear()
	if err != nil || extras || provider.AuthMode != agentconfig.CodexProviderKeyAuth {
		return agentconfig.CodexProviderDesired{}, state.ErrNativePrecondition
	}
	key := secretBytes(secret)
	if !nativeCodexSecretValid(key) {
		return agentconfig.CodexProviderDesired{}, state.ErrNativePrecondition
	}
	cloned := cloneCodexProvider(provider)
	return agentconfig.CodexProviderDesired{AuthMode: cloned.AuthMode, Endpoint: cloned.Endpoint, Model: cloned.Model, ReasoningEffort: cloned.ReasoningEffort, KeyPresent: true, Catalog: cloned.Catalog}, nil
}
func nativeCodexSecretValid(key []byte) bool {
	return len(key) > 0 && len(key) <= 8192 && utf8.Valid(key) && !bytes.ContainsAny(key, "\x00\r\n")
}

func (service *NativeService) nativeCodexPlan(ctx context.Context, request nativesessionauth.CodexPrepareRequest) (state.NativeCodexPrepareValidator, agentconfig.CodexProviderDeclarationV1, error) {
	metadata, payload, err := service.domain.loadEditable(ctx, request.OwnerID, request.AssetID)
	if err != nil {
		return nil, agentconfig.CodexProviderDeclarationV1{}, err
	}
	if metadata.Revision != request.AssetRevision {
		return nil, agentconfig.CodexProviderDeclarationV1{}, state.ErrConfigurationConflict
	}
	if metadata.AgentID != "codex" || request.AgentID != "codex" || metadata.SchemaVersion != 1 || metadata.Kind != state.ConfigurationKindProvider {
		return nil, agentconfig.CodexProviderDeclarationV1{}, state.ErrNativePrecondition
	}
	var trusted nativesessionauth.AdapterFields
	for _, descriptor := range service.registry.List() {
		if descriptor.Manifest.PluginID == "builtin.codex" {
			trusted = nativesessionauth.AdapterFields{APIVersion: uint32(descriptor.Manifest.APIVersion), PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Capability: "codex.provider-plan.v1"}
		}
	}
	if trusted != request.Adapter {
		return nil, agentconfig.CodexProviderDeclarationV1{}, state.ErrNativePrecondition
	}
	desired, err := nativeCodexDesired(payload)
	if err != nil {
		return nil, agentconfig.CodexProviderDeclarationV1{}, err
	}
	declaration, err := service.registry.PlanCodexProvider(ctx, agentconfig.CodexProviderPlanRequest{APIVersion: int(trusted.APIVersion), PluginID: trusted.PluginID, PluginVersion: trusted.PluginVersion, DefinitionDigest: trusted.DefinitionDigest, Target: agentconfig.CodexConfig, Capabilities: []agentconfig.Capability{agentconfig.CodexProviderPlanV1}, Desired: desired})
	if err != nil {
		if ctx.Err() != nil {
			return nil, agentconfig.CodexProviderDeclarationV1{}, ctx.Err()
		}
		return nil, agentconfig.CodexProviderDeclarationV1{}, state.ErrNativePrecondition
	}
	data, err := state.NewNativeCodexPreparedData(trusted, declaration)
	if err != nil {
		return nil, agentconfig.CodexProviderDeclarationV1{}, err
	}
	validate := func(ctx context.Context, current state.ConfigurationAsset, payload state.ConfigurationPayload) (state.NativeCodexPreparedData, error) {
		if err := ctx.Err(); err != nil {
			return state.NativeCodexPreparedData{}, err
		}
		actual, err := nativeCodexDesired(payload)
		if err != nil {
			return state.NativeCodexPreparedData{}, err
		}
		if current != metadata || !reflect.DeepEqual(actual, desired) {
			return state.NativeCodexPreparedData{}, state.ErrConfigurationConflict
		}
		return data, nil
	}
	return validate, declaration, nil
}

func (service *NativeService) PrepareCodex(ctx context.Context, bearer string, request nativesessionauth.CodexPrepareRequest, signature []byte) (state.NativeCodexPrepared, error) {
	if service == nil || service.consumer == nil {
		return state.NativeCodexPrepared{}, state.ErrNativeUnauthorized
	}
	if _, err := service.consumer.PreflightCodexPrepare(ctx, bearer, request, signature); err != nil {
		return state.NativeCodexPrepared{}, err
	}
	validate, _, err := service.nativeCodexPlan(ctx, request)
	if err != nil {
		return state.NativeCodexPrepared{}, err
	}
	return service.consumer.PrepareCodex(ctx, bearer, request, signature, validate)
}
func (service *NativeService) AuthorizeCodex(ctx context.Context, bearer string, request nativesessionauth.AuthorizeRequest, wire *nativev1.CodexLocalBindingV1, signature []byte) (auth.IssuedCodexNativeGrant, error) {
	if service == nil || service.consumer == nil {
		return auth.IssuedCodexNativeGrant{}, state.ErrNativeUnauthorized
	}
	snapshot, err := service.consumer.CodexConfigurationSnapshot(ctx, bearer, request.OperationID, request.PrepareID)
	if err != nil {
		return auth.IssuedCodexNativeGrant{}, err
	}
	preparation := snapshot.Preparation()
	local, err := nativesessionauth.NewCodexLocalBinding([32]byte(preparation.CopyWire().Codex.DeclarationSha256), wire)
	if err != nil {
		return auth.IssuedCodexNativeGrant{}, state.ErrNativeInvalid
	}
	input := nativesessionauth.CodexAuthorizeRequest{AuthorizeRequest: request, Local: local}
	if _, err := service.consumer.PreflightCodexAuthorize(ctx, bearer, input, signature); err != nil {
		return auth.IssuedCodexNativeGrant{}, err
	}
	validate, _, err := service.nativeCodexPlan(ctx, preparation.CopyRequest())
	if err != nil {
		return auth.IssuedCodexNativeGrant{}, err
	}
	return service.consumer.AuthorizeCodex(ctx, bearer, input, signature, validate)
}
func (service *NativeService) consumeCodex(ctx context.Context, bearer string, request nativesessionauth.ConsumeRequest, signature []byte) (state.NativeMaterialization, error) {
	snapshot, err := service.consumer.PreflightCodexConsume(ctx, bearer, request, signature)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	apply := snapshot.ConsumeFields(request).Apply
	validate, declaration, err := service.nativeCodexPlan(ctx, apply.Preparation().CopyRequest())
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	expected, err := nativesessionauth.HashCodexDeclaration(declaration)
	if err != nil {
		return state.NativeMaterialization{}, state.ErrNativePrecondition
	}
	materialize := func(ctx context.Context, metadata state.ConfigurationAsset, payload state.ConfigurationPayload, binding nativesessionauth.CodexApply, grantID string, consumedMS int64) (state.NativeMaterialization, error) {
		if _, err := validate(ctx, metadata, payload); err != nil {
			return state.NativeMaterialization{}, err
		}
		wire := binding.CopyWire()
		if wire == nil || !bytes.Equal(wire.Preparation.Codex.DeclarationSha256, expected[:]) || binding.Preparation().CopyRequest().Adapter != apply.Preparation().CopyRequest().Adapter {
			return state.NativeMaterialization{}, state.ErrNativePrecondition
		}
		provider, secret, extras, err := decodeCodexEnvelope(payload)
		defer secret.Clear()
		if err != nil || extras || provider.AuthMode != agentconfig.CodexProviderKeyAuth {
			return state.NativeMaterialization{}, state.ErrNativePrecondition
		}
		selected := secretBytes(secret)
		if !nativeCodexSecretValid(selected) {
			return state.NativeMaterialization{}, state.ErrNativePrecondition
		}
		encoded, err := proto.Marshal(&nativev1.ConsumeNativeMaterializationResponse{Binding: wire, SlotValues: []*nativev1.SlotValue{{Slot: 3, Plaintext: selected}}, GrantId: grantID, ConsumedAtUnixMs: consumedMS})
		defer clear(encoded)
		if err != nil {
			return state.NativeMaterialization{}, state.ErrNativeUnavailable
		}
		return state.NewNativeMaterialization(encoded)
	}
	return service.consumer.ConsumeCodex(ctx, bearer, request, signature, materialize)
}
