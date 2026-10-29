package configclient

import (
	"bytes"
	"errors"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func nativeCodecBind() *nativev1.BindNativeConsumerResponse {
	return &nativev1.BindNativeConsumerResponse{NativeSessionToken: bytes.Repeat([]byte{91}, 32), NativeSessionBinding: bytes.Repeat([]byte{92}, 32), ExpiresAtUnixMs: 100000}
}

func TestNativeClientStrictResponseCodec(test *testing.T) {
	valid, _ := proto.Marshal(nativeCodecBind())
	for name, data := range map[string][]byte{
		"unknown":      protowire.AppendVarint(protowire.AppendTag(bytes.Clone(valid), 99, protowire.VarintType), 1),
		"duplicate":    append(bytes.Clone(valid), valid...),
		"wrong-type":   protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 1),
		"noncanonical": {0x8a, 0x00, 32},
		"truncated":    valid[:len(valid)-1],
		"oversize":     bytes.Repeat([]byte{1}, nativeWireLimit+1),
	} {
		test.Run(name, func(test *testing.T) {
			target := new(nativev1.BindNativeConsumerResponse)
			if err := nativeDecode(data, target, nativeWireLimit); !errors.Is(err, ErrNativeInvalid) || len(target.NativeSessionToken) != 0 {
				test.Fatal("ambiguous wire/partial secret accepted")
			}
		})
	}
	target := new(nativev1.BindNativeConsumerResponse)
	if err := nativeDecode(valid, target, nativeWireLimit); err != nil {
		test.Fatal(err)
	}
	nativeClear(target)
	if !bytes.Equal(target.NativeSessionToken, make([]byte, 32)) {
		test.Fatal("decoded owned secret not cleared")
	}
}

func TestNativeClientStrictNestedAndEnum(test *testing.T) {
	fixture := newNativeClientFixture(test, false, nil)
	flow, _ := fixture.prepare(test, "api-key")
	valid, _ := proto.Marshal(flow.prepared)
	response := new(nativev1.PrepareNativeApplyResponse)
	if err := nativeDecode(valid, response, nativeWireLimit); err != nil {
		test.Fatal(err)
	}
	for _, mutate := range []func(*nativev1.PrepareNativeApplyResponse){
		func(value *nativev1.PrepareNativeApplyResponse) {
			value.Binding.Adapter.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x01})
		},
		func(value *nativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[0].GetSetSelectedSecret().ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x01})
		},
		func(value *nativev1.PrepareNativeApplyResponse) {
			value.Binding.RequiredSlots = []nativev1.SecretSlot{1, 1}
		},
		func(value *nativev1.PrepareNativeApplyResponse) {
			value.Binding.RequiredSlots = []nativev1.SecretSlot{99}
		},
	} {
		candidate := proto.Clone(response).(*nativev1.PrepareNativeApplyResponse)
		mutate(candidate)
		data, _ := proto.Marshal(candidate)
		if err := nativeDecode(data, new(nativev1.PrepareNativeApplyResponse), nativeWireLimit); !errors.Is(err, ErrNativeInvalid) {
			test.Fatal("nested unknown/duplicate slot/enum accepted")
		}
	}
	operation := flow.prepared.Patch.Operations[0]
	encoded, _ := proto.Marshal(operation)
	encoded = protowire.AppendBytes(protowire.AppendTag(encoded, 4, protowire.BytesType), nil)
	if strictProofWire(encoded, operation.ProtoReflect().Descriptor(), 0) {
		test.Fatal("duplicate patch oneof accepted")
	}
}

func TestNativeClientFieldAndBootstrapBudgets(test *testing.T) {
	data, _ := proto.Marshal(nativeCodecBind())
	if err := nativeDecode(data, new(nativev1.BindNativeConsumerResponse), len(data)-1); !errors.Is(err, ErrNativeInvalid) {
		test.Fatal("response byte budget not enforced")
	}
	budget := 0
	if nativeFieldBudget(data, nativeCodecBind().ProtoReflect().Descriptor(), &budget) {
		test.Fatal("field budget not enforced")
	}
	fixture := newNativeClientFixture(test, false, nativeMutatingFilter("PrepareNativeApply", func(data []byte) []byte { return append(data, bytes.Repeat([]byte{1}, nativeWireLimit)...) }))
	asset := fixture.asset(test, "api-key")
	flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := flow.Prepare(test.Context(), NativeSelection{AssetID: asset.Metadata.ID, AssetRevision: 1, ProfileID: "profile", SelectionRevision: 1}); !errors.Is(err, ErrNativeInvalid) {
		test.Fatal("512KiB wire budget not enforced")
	}
}
