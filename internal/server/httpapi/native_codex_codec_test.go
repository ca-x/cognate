package httpapi

import (
	"bytes"
	"math"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	publicv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/configclient"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func nativeCodexPrepareWire() *nativev1.PrepareNativeApplyRequest {
	pin := configclient.CodexNativeAdapterPin()
	return &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: "synthetic-owner", AssetId: "AAAAAAAAAAAAAAAAAAAAAAAAAA", ExpectedAssetRevision: 1, AgentId: "codex", SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: pin.Manifest.PluginID, PluginVersion: pin.Manifest.PluginVersion, DefinitionDigest: pin.DefinitionDigest, RequiredCapabilities: []string{"codex.provider-plan.v1"}}, Target: 2, Scope: 1, ProfileId: "profile", SelectionRevision: 1, OperationId: bytes.Repeat([]byte{1}, 16), ClientNonce: bytes.Repeat([]byte{6}, 32), ProofSignature: bytes.Repeat([]byte{9}, 64), CodexContractVersion: 1}
}

func TestCodexNativeStrictNestedPresence(test *testing.T) {
	base := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: "synthetic-prepare", OperationId: bytes.Repeat([]byte{1}, 16), IntentCommitment: bytes.Repeat([]byte{2}, 32), RequestedSlots: []nativev1.SecretSlot{3}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: bytes.Repeat([]byte{3}, 32), ProofSignature: bytes.Repeat([]byte{4}, 64), IntentExpiresAtUnixMs: 1700000100000, CodexLocalBinding: codexHTTPProjection()}
	encoded, _ := proto.Marshal(base)
	if (nativeConfigurationCodec{}).Unmarshal(encoded, new(nativev1.AuthorizeNativeMaterializationRequest)) != nil {
		test.Fatal("positive complete local projection rejected")
	}
	for name, mutate := range map[string]func(*nativev1.AuthorizeNativeMaterializationRequest){
		"unknown-local": func(value *nativev1.AuthorizeNativeMaterializationRequest) {
			value.CodexLocalBinding.ProtoReflect().SetUnknown([]byte{0x80, 0x06, 0})
		},
		"duplicate-optional-false": func(value *nativev1.AuthorizeNativeMaterializationRequest) {
			value.CodexLocalBinding.ProtoReflect().SetUnknown([]byte{104, 0})
		},
		"duplicate-oneof": func(value *nativev1.AuthorizeNativeMaterializationRequest) {
			value.CodexLocalBinding.Baselines[0].ProtoReflect().SetUnknown([]byte{26, 34, 10, 32, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1})
		},
		"unknown-empty-union": func(value *nativev1.AuthorizeNativeMaterializationRequest) {
			value.CodexLocalBinding.Baselines[0].GetMissing().ProtoReflect().SetUnknown([]byte{8, 0})
		},
		"unknown-confirmation": func(value *nativev1.AuthorizeNativeMaterializationRequest) {
			value.GetExplicitConfirmation().ProtoReflect().SetUnknown([]byte{16, 0})
		},
		"auto-mixed": func(value *nativev1.AuthorizeNativeMaterializationRequest) {
			value.ProtoReflect().SetUnknown([]byte{50, 0})
		},
		"negative-expiry": func(value *nativev1.AuthorizeNativeMaterializationRequest) { value.IntentExpiresAtUnixMs = -1 },
		"wrong-slot": func(value *nativev1.AuthorizeNativeMaterializationRequest) {
			value.RequestedSlots = []nativev1.SecretSlot{1}
		},
		"duplicate-slot": func(value *nativev1.AuthorizeNativeMaterializationRequest) {
			value.RequestedSlots = []nativev1.SecretSlot{3, 3}
		},
		"unknown-role": func(value *nativev1.AuthorizeNativeMaterializationRequest) {
			value.CodexLocalBinding.Baselines[0].Role = 99
		},
		"absent-policy-result": func(value *nativev1.AuthorizeNativeMaterializationRequest) {
			value.CodexLocalBinding.RequiresOpenaiAuth = nil
		},
		"unknown-auth": func(value *nativev1.AuthorizeNativeMaterializationRequest) { value.CodexLocalBinding.AuthStore = 0 },
	} {
		test.Run(name, func(test *testing.T) {
			candidate := proto.Clone(base).(*nativev1.AuthorizeNativeMaterializationRequest)
			mutate(candidate)
			encoded, _ := proto.Marshal(candidate)
			target := proto.Clone(base).(*nativev1.AuthorizeNativeMaterializationRequest)
			if (nativeConfigurationCodec{}).Unmarshal(encoded, target) == nil || !proto.Equal(target, base) {
				test.Fatal("nested duplicate/unknown/mixed/presence accepted or destination changed")
			}
		})
	}
	for _, pair := range []struct {
		value uint64
		valid bool
	}{{math.MaxUint32, true}, {math.MaxUint32 + 1, false}} {
		field := new(nativev1.PrepareNativeApplyRequest).ProtoReflect().Descriptor().Fields().ByNumber(5)
		if nativeScalarValid(field, pair.value) != pair.valid {
			test.Fatal("uint32 overflow was folded")
		}
	}
	budget := 1024
	if nativeWireValid(nil, base.ProtoReflect().Descriptor(), 16, &budget) {
		test.Fatal("depth16 budget bypassed")
	}
}

func TestCodexNativeStrictWire(test *testing.T) {
	request := nativeCodexPrepareWire()
	encoded, err := proto.Marshal(request)
	if err != nil {
		test.Fatal("fixture marshal failed")
	}
	destination := new(nativev1.PrepareNativeApplyRequest)
	if (nativeConfigurationCodec{}).Unmarshal(encoded, destination) != nil || !proto.Equal(request, destination) {
		test.Fatal("valid typed Codex wire was rejected")
	}
	for _, extra := range [][]byte{protowire.AppendVarint(protowire.AppendTag(nil, 40, protowire.VarintType), 1), protowire.AppendBytes(protowire.AppendTag(nil, 99, protowire.BytesType), nil)} {
		bad := append(bytes.Clone(encoded), extra...)
		before := proto.Clone(destination)
		if (nativeConfigurationCodec{}).Unmarshal(bad, destination) == nil || !proto.Equal(before, destination) {
			test.Fatal("duplicate/unknown wire accepted or partially replaced destination")
		}
	}
	legacy := proto.Clone(request).(*nativev1.PrepareNativeApplyRequest)
	legacy.AgentId = "claude"
	legacy.Target = 1
	legacy.CodexContractVersion = 0
	legacy.Adapter.PluginId = "builtin.claude"
	legacy.Adapter.RequiredCapabilities = []string{"claude.provider-switch.v1"}
	encoded, _ = proto.Marshal(legacy)
	encoded = protowire.AppendVarint(protowire.AppendTag(encoded, 40, protowire.VarintType), 0)
	if (nativeConfigurationCodec{}).Unmarshal(encoded, new(nativev1.PrepareNativeApplyRequest)) == nil {
		test.Fatal("Claude explicit zero Codex member accepted")
	}
}

func TestCodexNativeStrictPackedBudget(test *testing.T) {
	descriptor := new(publicv1.CodexModalities).ProtoReflect().Descriptor()
	packed := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), []byte{2, 1})
	unpacked := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 2)
	unpacked = protowire.AppendVarint(protowire.AppendTag(unpacked, 1, protowire.VarintType), 1)
	for _, encoded := range [][]byte{packed, unpacked} {
		budget := 1024
		if !nativeWireValid(encoded, descriptor, 0, &budget) {
			test.Fatal("legal packed/unpacked typed modality list rejected")
		}
		var value publicv1.CodexModalities
		if proto.Unmarshal(encoded, &value) != nil || len(value.Values) != 2 || value.Values[0] != 2 || value.Values[1] != 1 {
			test.Fatal("modality order changed")
		}
	}
	oversize := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), bytes.Repeat([]byte{1}, 1025))
	budget := 1024
	if nativeWireValid(oversize, descriptor, 0, &budget) {
		test.Fatal("packed value count bypassed accumulated1024 budget")
	}
	for _, bad := range [][]byte{{0x08, 0x81, 0}, {0x0a, 1, 0}, {0x0a, 1, 99}, {0x12, 0}} {
		budget := 1024
		if nativeWireValid(bad, descriptor, 0, &budget) {
			test.Fatal("noncanonical/unknown enum or unknown member accepted")
		}
	}
}
