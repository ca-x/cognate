package configclient

import (
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const nativeWireLimit = 512 << 10
const nativeBootstrapLimit = 8 << 10

func nativeEncode(message proto.Message, limit int) ([]byte, error) {
	if !validDecodedProof(message) {
		return nil, ErrNativeInvalid
	}
	data, err := proto.Marshal(message)
	if err != nil || len(data) > limit {
		clear(data)
		return nil, ErrNativeInvalid
	}
	return data, nil
}

func nativeDecode(data []byte, target proto.Message, limit int) error {
	if len(data) > limit || !strictProofWire(data, target.ProtoReflect().Descriptor(), 0) {
		return ErrNativeInvalid
	}
	budget := 1024
	if !nativeFieldBudget(data, target.ProtoReflect().Descriptor(), &budget) {
		return ErrNativeInvalid
	}
	temporary := target.ProtoReflect().New().Interface()
	defer nativeClear(temporary)
	if (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(data, temporary) != nil || !nativeResponseValid(temporary) {
		return ErrNativeInvalid
	}
	proto.Reset(target)
	proto.Merge(target, temporary)
	return nil
}

func nativeFieldBudget(data []byte, descriptor protoreflect.MessageDescriptor, remaining *int) bool {
	for len(data) > 0 {
		*remaining--
		if *remaining < 0 {
			return false
		}
		number, kind, prefix := protowire.ConsumeTag(data)
		if prefix < 0 {
			return false
		}
		data = data[prefix:]
		field := descriptor.Fields().ByNumber(protoreflect.FieldNumber(number))
		if field == nil {
			return false
		}
		if kind == protowire.BytesType {
			nested, size := protowire.ConsumeBytes(data)
			if size < 0 {
				return false
			}
			if field.Kind() == protoreflect.MessageKind && !nativeFieldBudget(nested, field.Message(), remaining) {
				return false
			}
			data = data[size:]
		} else {
			size := protowire.ConsumeFieldValue(number, kind, data)
			if size < 0 {
				return false
			}
			data = data[size:]
		}
	}
	return true
}

func nativeResponseValid(message proto.Message) bool {
	switch value := message.(type) {
	case *nativev1.BeginNativeConsumerResponse:
		return validChallenge(value)
	case *nativev1.BindNativeConsumerResponse:
		return knownMessage(value) && len(value.NativeSessionToken) == 32 && len(value.NativeSessionBinding) == 32 && value.ExpiresAtUnixMs > 0
	case *nativev1.PrepareNativeApplyResponse:
		return ValidatePreparedPatch(value) == nil
	case *nativev1.AuthorizeNativeMaterializationResponse:
		if !knownMessage(value) || !validApply(value.Binding) || value.MaterializationRequired != (len(value.Binding.Preparation.RequiredSlots) == 1) {
			return false
		}
		if !value.MaterializationRequired {
			return value.GrantId == "" && len(value.GrantToken) == 0 && len(value.ConsumeChallenge) == 0 && value.ExpiresAtUnixMs == 0
		}
		return opaqueID(value.GrantId) && len(value.GrantToken) == 32 && len(value.ConsumeChallenge) == 32 && value.ExpiresAtUnixMs > value.Binding.Preparation.PreparedAtUnixMs && value.ExpiresAtUnixMs <= value.Binding.IntentExpiresAtUnixMs
	case *nativev1.ConsumeNativeMaterializationResponse:
		return knownMessage(value) && validApply(value.Binding) && len(value.Binding.Preparation.RequiredSlots) == 1 && len(value.SlotValues) == 1 && knownMessage(value.SlotValues[0]) && value.SlotValues[0].Slot == value.Binding.Preparation.RequiredSlots[0] && len(value.SlotValues[0].Plaintext) > 0 && len(value.SlotValues[0].Plaintext) <= 64<<10 && opaqueID(value.GrantId) && value.ConsumedAtUnixMs > 0
	case *nativev1.GetNativeMaterializationStatusResponse:
		if !knownMessage(value) {
			return false
		}
		switch value.State {
		case nativev1.GrantState_GRANT_STATE_NOT_ISSUED:
			return value.GrantId == "" && value.ExpiresAtUnixMs == 0 && value.ConsumedAtUnixMs == 0
		case nativev1.GrantState_GRANT_STATE_ISSUED, nativev1.GrantState_GRANT_STATE_CONSUMED, nativev1.GrantState_GRANT_STATE_EXPIRED, nativev1.GrantState_GRANT_STATE_RETIRED:
			return opaqueID(value.GrantId) && value.ExpiresAtUnixMs > 0 && ((value.State == nativev1.GrantState_GRANT_STATE_CONSUMED && value.ConsumedAtUnixMs > 0) || (value.State != nativev1.GrantState_GRANT_STATE_CONSUMED && value.ConsumedAtUnixMs == 0))
		}
	}
	return false
}

func nativeClear(message proto.Message) {
	switch value := message.(type) {
	case *nativev1.BindNativeConsumerResponse:
		clear(value.NativeSessionToken)
	case *nativev1.AuthorizeNativeMaterializationResponse:
		clear(value.GrantToken)
	case *nativev1.ConsumeNativeMaterializationRequest:
		clear(value.GrantToken)
	case *nativev1.ConsumeNativeMaterializationResponse:
		for _, slot := range value.SlotValues {
			if slot != nil {
				clear(slot.Plaintext)
			}
		}
	}
}
