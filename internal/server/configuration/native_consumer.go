package configuration

import (
	"context"
	"crypto/sha256"
	"reflect"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/agentadapter"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

type NativeService struct {
	domain   *Service
	consumer *auth.NativeConsumerService
	registry *agentadapter.Registry
}

func NewNativeService(store *state.Store, consumer *auth.NativeConsumerService, registry *agentadapter.Registry) (*NativeService, error) {
	if store == nil || consumer == nil || !consumer.OwnsConfigurationStore(store) || registry == nil {
		return nil, state.ErrNativeInvalid
	}
	return &NativeService{domain: New(store), consumer: consumer, registry: registry}, nil
}

func nativeDesired(payload state.ConfigurationPayload) (agentconfig.ClaudeProviderDesired, uint32, error) {
	provider, secrets, err := decodeEnvelope(payload)
	if err != nil {
		return agentconfig.ClaudeProviderDesired{}, 0, state.ErrNativePrecondition
	}
	defer secrets.apiKey.Clear()
	defer secrets.authToken.Clear()
	var slot uint32
	switch provider.AuthMode {
	case APIKey:
		if len(secretBytes(secrets.apiKey)) == 0 {
			return agentconfig.ClaudeProviderDesired{}, 0, state.ErrNativePrecondition
		}
		slot = 1
	case AuthToken:
		if len(secretBytes(secrets.authToken)) == 0 {
			return agentconfig.ClaudeProviderDesired{}, 0, state.ErrNativePrecondition
		}
		slot = 2
	case NativeLogin:
	default:
		return agentconfig.ClaudeProviderDesired{}, 0, state.ErrNativePrecondition
	}
	return agentconfig.ClaudeProviderDesired{AuthMode: agentconfig.ClaudeAuthMode(provider.AuthMode), Endpoint: provider.Endpoint, Models: agentconfig.ClaudeModelsDesired{Default: provider.Models.Default, Haiku: provider.Models.Haiku, Sonnet: provider.Models.Sonnet, Opus: provider.Models.Opus}, APIKeyPresent: slot == 1, AuthTokenPresent: slot == 2}, slot, nil
}

func (service *NativeService) nativePlan(ctx context.Context, request nativesessionauth.PrepareRequest) (state.NativePrepareValidator, error) {
	metadata, payload, err := service.domain.load(ctx, request.OwnerID, request.AssetID)
	if err != nil {
		return nil, err
	}
	if metadata.Revision != request.AssetRevision {
		return nil, state.ErrConfigurationConflict
	}
	if metadata.AgentID != request.AgentID || metadata.SchemaVersion != int(request.SchemaVersion) || metadata.Kind != state.ConfigurationKindProvider {
		return nil, state.ErrNativePrecondition
	}
	var trusted nativesessionauth.AdapterFields
	for _, descriptor := range service.registry.List() {
		if descriptor.Manifest.PluginID == "builtin.claude" {
			trusted = nativesessionauth.AdapterFields{APIVersion: uint32(descriptor.Manifest.APIVersion), PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Capability: "claude.provider-switch.v1"}
		}
	}
	if trusted != request.Adapter {
		return nil, state.ErrNativePrecondition
	}
	desired, slot, err := nativeDesired(payload)
	if err != nil {
		return nil, err
	}
	plan, err := service.registry.PlanClaudeProvider(ctx, agentconfig.ClaudeProviderPatchRequest{APIVersion: int(trusted.APIVersion), PluginID: trusted.PluginID, PluginVersion: trusted.PluginVersion, DefinitionDigest: trusted.DefinitionDigest, Target: agentconfig.ClaudeSettings, Capabilities: []agentconfig.Capability{agentconfig.ClaudeProviderSwitchV1}, Desired: desired})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, state.ErrNativePrecondition
	}
	var patch nativesessionauth.PatchFields
	if len(plan.Operations) != 7 {
		return nil, state.ErrNativePrecondition
	}
	slots := [...]agentconfig.ClaudeEnvSlot{agentconfig.ClaudeEnvAPIKey, agentconfig.ClaudeEnvAuthToken, agentconfig.ClaudeEnvBaseURL, agentconfig.ClaudeEnvModel, agentconfig.ClaudeEnvHaikuModel, agentconfig.ClaudeEnvSonnetModel, agentconfig.ClaudeEnvOpusModel}
	for index, operation := range plan.Operations {
		if operation.Slot != slots[index] {
			return nil, state.ErrNativePrecondition
		}
		switch operation.Action {
		case agentconfig.ClaudeSetLiteral:
			if operation.Literal == nil {
				return nil, state.ErrNativePrecondition
			}
			patch[index] = nativesessionauth.PatchOperation{Action: 1, Literal: *operation.Literal}
		case agentconfig.ClaudeSetSelectedSecret:
			if operation.Reference == nil {
				return nil, state.ErrNativePrecondition
			}
			switch *operation.Reference {
			case agentconfig.ClaudeAPIKeySecret:
				patch[index] = nativesessionauth.PatchOperation{Action: 2, SecretSlot: 1}
			case agentconfig.ClaudeAuthTokenSecret:
				patch[index] = nativesessionauth.PatchOperation{Action: 2, SecretSlot: 2}
			default:
				return nil, state.ErrNativePrecondition
			}
		case agentconfig.ClaudeRemove:
			patch[index].Action = 3
		default:
			return nil, state.ErrNativePrecondition
		}
	}
	if _, err := nativesessionauth.HashPatch(patch); err != nil {
		return nil, state.ErrNativePrecondition
	}
	return func(ctx context.Context, current state.ConfigurationAsset, payload state.ConfigurationPayload) (state.NativePreparedData, error) {
		if err := ctx.Err(); err != nil {
			return state.NativePreparedData{}, err
		}
		actual, actualSlot, err := nativeDesired(payload)
		if err != nil {
			return state.NativePreparedData{}, err
		}
		if current != metadata || actualSlot != slot || !reflect.DeepEqual(actual, desired) {
			return state.NativePreparedData{}, state.ErrConfigurationConflict
		}
		return state.NativePreparedData{Adapter: trusted, Patch: patch, RequiredSlot: slot}, nil
	}, nil
}

func (service *NativeService) Prepare(ctx context.Context, bearer string, request nativesessionauth.PrepareRequest, signature []byte) (state.NativePrepared, error) {
	if _, err := service.consumer.PreflightPrepare(ctx, bearer, request, signature); err != nil {
		return state.NativePrepared{}, err
	}
	validate, err := service.nativePlan(ctx, request)
	if err != nil {
		return state.NativePrepared{}, err
	}
	return service.consumer.Prepare(ctx, bearer, request, signature, validate)
}

func (service *NativeService) Authorize(ctx context.Context, bearer string, request nativesessionauth.AuthorizeRequest, signature []byte) (auth.IssuedNativeGrant, error) {
	snapshot, err := service.consumer.PreflightAuthorize(ctx, bearer, request, signature)
	if err != nil {
		return auth.IssuedNativeGrant{}, err
	}
	validate, err := service.nativePlan(ctx, snapshot.AuthorizeFields(request).Preparation.Request)
	if err != nil {
		return auth.IssuedNativeGrant{}, err
	}
	return service.consumer.Authorize(ctx, bearer, request, signature, validate)
}

func (service *NativeService) Consume(ctx context.Context, bearer string, request nativesessionauth.ConsumeRequest, signature []byte) (state.NativeMaterialization, error) {
	snapshot, err := service.consumer.PreflightConsume(ctx, bearer, request, signature)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	apply := snapshot.ConsumeFields(request).Apply
	validate, err := service.nativePlan(ctx, apply.Preparation.Request)
	if err != nil {
		return state.NativeMaterialization{}, err
	}
	materialize := func(ctx context.Context, metadata state.ConfigurationAsset, payload state.ConfigurationPayload, binding nativesessionauth.ApplyFields, grantID string, consumedMS int64) (state.NativeMaterialization, error) {
		data, err := validate(ctx, metadata, payload)
		if err != nil {
			return state.NativeMaterialization{}, err
		}
		patchHash, err := nativesessionauth.HashPatch(data.Patch)
		if err != nil || data.RequiredSlot != binding.Preparation.RequiredSlot || patchHash != binding.Preparation.PatchSHA256 || data.Adapter != binding.Preparation.Request.Adapter {
			return state.NativeMaterialization{}, state.ErrNativePrecondition
		}
		_, secrets, err := decodeEnvelope(payload)
		if err != nil {
			return state.NativeMaterialization{}, state.ErrNativePrecondition
		}
		defer secrets.apiKey.Clear()
		defer secrets.authToken.Clear()
		var selected []byte
		switch binding.Preparation.RequiredSlot {
		case 1:
			selected = secretBytes(secrets.apiKey)
		case 2:
			selected = secretBytes(secrets.authToken)
		default:
			return state.NativeMaterialization{}, state.ErrNativePrecondition
		}
		if len(selected) == 0 {
			return state.NativeMaterialization{}, state.ErrNativePrecondition
		}
		encoded, err := proto.Marshal(&nativev1.ConsumeNativeMaterializationResponse{Binding: wireApply(binding), SlotValues: []*nativev1.SlotValue{{Slot: nativev1.SecretSlot(binding.Preparation.RequiredSlot), Plaintext: selected}}, GrantId: grantID, ConsumedAtUnixMs: consumedMS})
		defer clear(encoded)
		if err != nil {
			return state.NativeMaterialization{}, state.ErrNativeUnavailable
		}
		return state.NewNativeMaterialization(encoded)
	}
	return service.consumer.Consume(ctx, bearer, request, signature, materialize)
}

func (service *NativeService) Status(ctx context.Context, bearer string, request nativesessionauth.StatusRequest, signature []byte) (state.NativeGrantStatus, error) {
	return service.consumer.Status(ctx, bearer, request, signature)
}

func consumerKeyHash(publicKey []byte) []byte { digest := sha256.Sum256(publicKey); return digest[:] }

func wireAdapter(adapter nativesessionauth.AdapterFields) *nativev1.AdapterBinding {
	return &nativev1.AdapterBinding{ApiVersion: adapter.APIVersion, PluginId: adapter.PluginID, PluginVersion: adapter.PluginVersion, DefinitionDigest: adapter.DefinitionDigest, RequiredCapabilities: []string{adapter.Capability}}
}

func wirePreparation(binding nativesessionauth.PreparationFields) *nativev1.PreparationBinding {
	request, consumer := binding.Request, binding.Consumer
	var slots []nativev1.SecretSlot
	if binding.RequiredSlot != 0 {
		slots = []nativev1.SecretSlot{nativev1.SecretSlot(binding.RequiredSlot)}
	}
	return &nativev1.PreparationBinding{OwnerId: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationId: consumer.OperationID[:], AssetId: request.AssetID, AssetRevision: request.AssetRevision, AgentId: request.AgentID, AssetKind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, SchemaVersion: request.SchemaVersion, Adapter: wireAdapter(request.Adapter), Target: nativev1.Target(request.Target), Scope: nativev1.Scope(request.Scope), ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, RequiredSlots: slots, PrepareExpiresAtUnixMs: binding.ExpiresMS, ConsumerKeySha256: consumerKeyHash(consumer.PublicKey[:]), PrepareId: binding.PrepareID, PreparedAtUnixMs: binding.PreparedMS, PatchSha256: binding.PatchSHA256[:]}
}

func wireApply(binding nativesessionauth.ApplyFields) *nativev1.ApplyBinding {
	return &nativev1.ApplyBinding{Preparation: wirePreparation(binding.Preparation), IntentCommitment: binding.Commitment[:], IntentExpiresAtUnixMs: binding.IntentExpiresMS}
}
