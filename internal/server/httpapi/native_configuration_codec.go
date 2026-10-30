package httpapi

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const nativeMaxWireBytes = 512 << 10
const nativeBootstrapMaxBytes = 8 << 10

type nativeConfigurationCodec struct{}

func (nativeConfigurationCodec) Name() string { return "proto" }

func (nativeConfigurationCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, errors.New("invalid native response")
	}
	defer nativeClearWire(message)
	if !nativeMessageValid(message) {
		return nil, errors.New("invalid native response")
	}
	data, err := proto.Marshal(message)
	if err != nil || len(data) > nativeMaxWireBytes {
		clear(data)
		return nil, errors.New("invalid native response")
	}
	return data, nil
}

func (nativeConfigurationCodec) Unmarshal(data []byte, value any) error {
	invalid := func() error {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid native request"))
	}
	message, ok := value.(proto.Message)
	if !ok || !message.ProtoReflect().IsValid() || len(data) > nativeMaxWireBytes {
		return invalid()
	}
	remaining := 1024
	if !nativeWireValid(data, message.ProtoReflect().Descriptor(), 0, &remaining) {
		return invalid()
	}
	temporary := message.ProtoReflect().New().Interface()
	if (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(data, temporary) != nil || !nativeMessageValid(temporary) {
		nativeClearWire(temporary)
		return invalid()
	}
	proto.Reset(message)
	proto.Merge(message, temporary)
	nativeClearWire(temporary)
	return nil
}

func nativeVarint(data []byte) (uint64, int) {
	value, count := protowire.ConsumeVarint(data)
	if count < 0 || count != protowire.SizeVarint(value) {
		return 0, -1
	}
	return value, count
}

func nativeWireValid(data []byte, descriptor protoreflect.MessageDescriptor, depth int, remaining *int) bool {
	if depth >= 16 {
		return false
	}
	seen := make(map[protoreflect.FieldNumber]bool)
	oneofs := make(map[protoreflect.FullName]bool)
	for len(data) > 0 {
		*remaining--
		if *remaining < 0 {
			return false
		}
		tag, count := nativeVarint(data)
		if count < 0 || tag>>3 == 0 || tag>>3 > uint64(protowire.MaxValidNumber) {
			return false
		}
		field := descriptor.Fields().ByNumber(protoreflect.FieldNumber(tag >> 3))
		if field == nil || !field.IsList() && seen[field.Number()] {
			return false
		}
		seen[field.Number()] = true
		if oneof := field.ContainingOneof(); oneof != nil {
			if oneofs[oneof.FullName()] {
				return false
			}
			oneofs[oneof.FullName()] = true
		}
		data = data[count:]
		wireType := protowire.Type(tag & 7)
		if wireType == protowire.BytesType {
			if field.Kind() != protoreflect.StringKind && field.Kind() != protoreflect.BytesKind && field.Kind() != protoreflect.MessageKind && !field.IsPacked() {
				return false
			}
			length, prefix := nativeVarint(data)
			if prefix < 0 || length > uint64(len(data)-prefix) {
				return false
			}
			nested := data[prefix : prefix+int(length)]
			data = data[prefix+int(length):]
			switch {
			case field.Kind() == protoreflect.MessageKind:
				if !nativeWireValid(nested, field.Message(), depth+1, remaining) {
					return false
				}
			case field.IsPacked():
				if len(nested) == 0 {
					return false
				}
				for len(nested) > 0 {
					*remaining--
					if *remaining < 0 {
						return false
					}
					value, size := nativeVarint(nested)
					if size < 0 || !nativeScalarValid(field, value) {
						return false
					}
					nested = nested[size:]
				}
			case field.Kind() == protoreflect.StringKind && !utf8.Valid(nested):
				return false
			}
		} else {
			value, size := nativeVarint(data)
			if wireType != protowire.VarintType || size < 0 || !nativeScalarValid(field, value) {
				return false
			}
			data = data[size:]
		}
	}
	if descriptor.FullName() == "skillhub.configuration.native.v1.PreparationBinding" && seen[21] && seen[40] {
		return false
	}
	if descriptor.FullName() == "skillhub.configuration.native.v1.PrepareNativeApplyResponse" && seen[2] && seen[5] {
		return false
	}
	return true
}

func nativeScalarValid(field protoreflect.FieldDescriptor, value uint64) bool {
	if field.FullName() == "skillhub.configuration.native.v1.PrepareNativeApplyRequest.codex_contract_version" && value != 1 {
		return false
	}
	switch field.Kind() {
	case protoreflect.EnumKind:
		return value > 0 && value <= math.MaxInt32 && field.Enum().Values().ByNumber(protoreflect.EnumNumber(value)) != nil
	case protoreflect.Uint32Kind:
		return value <= math.MaxUint32
	case protoreflect.Uint64Kind:
		return true
	case protoreflect.Int64Kind:
		return value <= math.MaxInt64
	case protoreflect.BoolKind:
		return value <= 1
	default:
		return false
	}
}

func nativeIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, letter := range value {
		if !(letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z' || letter >= '0' && letter <= '9' || letter == '_' || letter == '-') {
			return false
		}
	}
	return true
}

func nativeOwnerValid(value string) bool {
	return len(value) > 0 && len(value) <= 512 && utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func nativeOriginValid(value string) bool {
	return strings.HasPrefix(value, "https://") && nativesessionauth.ValidServerOrigin(value)
}

func nativeAssetValid(value string) bool {
	return len(value) == 26 && strings.Trim(value, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") == ""
}

func nativeAdapterValid(value *nativev1.AdapterBinding) bool {
	return value != nil && value.ApiVersion > 0 && nativePluginIdentifier(value.PluginId) && nativePluginVersion(value.PluginVersion) && len(value.DefinitionDigest) == 64 && strings.Trim(value.DefinitionDigest, "0123456789abcdef") == "" && len(value.RequiredCapabilities) == 1 && value.RequiredCapabilities[0] == "claude.provider-switch.v1"
}

func nativePluginIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	return strings.Trim(value, "abcdefghijklmnopqrstuvwxyz0123456789.-") == ""
}

func nativePluginVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(value) > 32 || len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' || strings.Trim(part, "0123456789") != "" {
			return false
		}
	}
	return true
}

func nativeSlotsValid(slots []nativev1.SecretSlot) bool {
	return len(slots) == 0 || len(slots) == 1 && (slots[0] == 1 || slots[0] == 2)
}

func nativePreparationValid(value *nativev1.PreparationBinding) bool {
	if value != nil && value.Codex != nil {
		return false
	}
	return value != nil && nativeOwnerValid(value.OwnerId) && nativeOriginValid(value.ServerOrigin) && len(value.NativeSessionBinding) == 32 && len(value.OperationId) == 16 && nativeAssetValid(value.AssetId) && value.AssetRevision > 0 && value.AgentId == "claude" && value.AssetKind == 1 && value.SchemaVersion == 1 && nativeAdapterValid(value.Adapter) && value.Adapter.ApiVersion == 1 && value.Adapter.PluginId == "builtin.claude" && value.Adapter.PluginVersion == "1.1.0" && value.Target == 1 && value.Scope == 1 && nativeIdentifier(value.ProfileId) && value.SelectionRevision > 0 && nativeSlotsValid(value.RequiredSlots) && value.PreparedAtUnixMs > 0 && value.PrepareExpiresAtUnixMs > value.PreparedAtUnixMs && value.PrepareExpiresAtUnixMs-value.PreparedAtUnixMs <= 300000 && len(value.ConsumerKeySha256) == 32 && nativeIdentifier(value.PrepareId) && len(value.PatchSha256) == 32
}

func nativeApplyValid(value *nativev1.ApplyBinding) bool {
	if value != nil && value.CodexLocalBinding != nil {
		return false
	}
	return value != nil && nativePreparationValid(value.Preparation) && len(value.IntentCommitment) == 32 && value.IntentExpiresAtUnixMs > value.Preparation.PreparedAtUnixMs && value.IntentExpiresAtUnixMs <= value.Preparation.PrepareExpiresAtUnixMs
}

func nativeKnown(message protoreflect.Message) bool {
	if len(message.GetUnknown()) != 0 {
		return false
	}
	valid := true
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		check := func(value protoreflect.Value) bool {
			if field.Message() != nil {
				return nativeKnown(value.Message())
			}
			return field.Enum() == nil || value.Enum() > 0 && field.Enum().Values().ByNumber(value.Enum()) != nil
		}
		if field.IsList() {
			for index := range value.List().Len() {
				if !check(value.List().Get(index)) {
					valid = false
					break
				}
			}
		} else {
			valid = check(value)
		}
		return valid
	})
	return valid
}

func nativeMessageValid(message proto.Message) bool {
	if !message.ProtoReflect().IsValid() || !nativeKnown(message.ProtoReflect()) {
		return false
	}
	if handled, valid := nativeCodexMessageValid(message); handled {
		return valid
	}
	switch value := message.(type) {
	case *nativev1.BeginNativeConsumerRequest:
		return value.Purpose == 1 && len(value.OperationId) == 16 && len(value.ConsumerPublicKey) == 32 && len(value.ClientNonce) == 32 && nativeOwnerValid(value.ExpectedOwnerId) && nativeOriginValid(value.ExpectedServerOrigin)
	case *nativev1.BindNativeConsumerRequest:
		return nativeIdentifier(value.ChallengeId) && len(value.ProofSignature) == 64
	case *nativev1.PrepareNativeApplyRequest:
		return nativeOwnerValid(value.ExpectedOwnerId) && nativeAssetValid(value.AssetId) && value.ExpectedAssetRevision > 0 && len(value.AgentId) > 0 && len(value.AgentId) <= 64 && value.SchemaVersion > 0 && nativeAdapterValid(value.Adapter) && value.Target == 1 && value.Scope == 1 && nativeIdentifier(value.ProfileId) && value.SelectionRevision > 0 && len(value.OperationId) == 16 && len(value.ClientNonce) == 32 && len(value.ProofSignature) == 64
	case *nativev1.AuthorizeNativeMaterializationRequest:
		valid := nativeIdentifier(value.PrepareId) && len(value.OperationId) == 16 && len(value.IntentCommitment) == 32 && nativeSlotsValid(value.RequestedSlots) && len(value.ClientNonce) == 32 && len(value.ProofSignature) == 64 && value.IntentExpiresAtUnixMs > 0
		switch consent := value.Authorization.(type) {
		case *nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation:
			return valid && consent.ExplicitConfirmation != nil && consent.ExplicitConfirmation.ConfirmationRevision > 0
		case *nativev1.AuthorizeNativeMaterializationRequest_AutoPolicy:
			return valid && consent.AutoPolicy != nil && nativeIdentifier(string(consent.AutoPolicy.PolicyId)) && consent.AutoPolicy.PolicyRevision > 0
		default:
			return false
		}
	case *nativev1.ConsumeNativeMaterializationRequest:
		return nativeIdentifier(value.PrepareId) && nativeIdentifier(value.GrantId) && len(value.GrantToken) == 32 && len(value.OperationId) == 16 && len(value.IntentCommitment) == 32 && len(value.ClientNonce) == 32 && len(value.ProofSignature) == 64
	case *nativev1.GetNativeMaterializationStatusRequest:
		return nativeIdentifier(value.PrepareId) && (value.GrantId == "" || nativeIdentifier(value.GrantId)) && len(value.OperationId) == 16 && len(value.ClientNonce) == 32 && len(value.ProofSignature) == 64
	case *nativev1.BeginNativeConsumerResponse:
		return nativeIdentifier(value.ChallengeId) && len(value.Challenge) == 32 && len(value.NativeSessionBinding) == 32 && nativeOwnerValid(value.OwnerId) && nativeOriginValid(value.ServerOrigin) && value.ChallengeExpiresAtUnixMs > 0 && value.NativeSessionExpiresAtUnixMs >= value.ChallengeExpiresAtUnixMs
	case *nativev1.BindNativeConsumerResponse:
		return len(value.NativeSessionToken) == 32 && len(value.NativeSessionBinding) == 32 && value.ExpiresAtUnixMs > 0
	case *nativev1.PrepareNativeApplyResponse:
		if !nativePreparationValid(value.Binding) || len(value.AuthorizeChallenge) != 32 || value.MaterializationRequired != (len(value.Binding.RequiredSlots) == 1) {
			return false
		}
		patch, valid := nativePatchFields(value.Patch)
		digest, err := nativesessionauth.HashPatch(patch)
		return valid && err == nil && bytes.Equal(digest[:], value.Binding.PatchSha256)
	case *nativev1.AuthorizeNativeMaterializationResponse:
		if !nativeApplyValid(value.Binding) || value.MaterializationRequired != (len(value.Binding.Preparation.RequiredSlots) == 1) {
			return false
		}
		if !value.MaterializationRequired {
			return value.GrantId == "" && len(value.GrantToken) == 0 && len(value.ConsumeChallenge) == 0 && value.ExpiresAtUnixMs == 0
		}
		return nativeIdentifier(value.GrantId) && len(value.GrantToken) == 32 && len(value.ConsumeChallenge) == 32 && value.ExpiresAtUnixMs > value.Binding.Preparation.PreparedAtUnixMs && value.ExpiresAtUnixMs <= value.Binding.IntentExpiresAtUnixMs
	case *nativev1.ConsumeNativeMaterializationResponse:
		return nativeApplyValid(value.Binding) && len(value.Binding.Preparation.RequiredSlots) == 1 && len(value.SlotValues) == 1 && value.SlotValues[0] != nil && value.SlotValues[0].Slot == value.Binding.Preparation.RequiredSlots[0] && len(value.SlotValues[0].Plaintext) > 0 && len(value.SlotValues[0].Plaintext) <= nativeMaxWireBytes && nativeIdentifier(value.GrantId) && value.ConsumedAtUnixMs > 0
	case *nativev1.GetNativeMaterializationStatusResponse:
		if value.State == 1 {
			return value.GrantId == "" && value.ExpiresAtUnixMs == 0 && value.ConsumedAtUnixMs == 0
		}
		return value.State >= 2 && value.State <= 5 && nativeIdentifier(value.GrantId) && value.ExpiresAtUnixMs > 0 && (value.State == 3 && value.ConsumedAtUnixMs > 0 || value.State != 3 && value.ConsumedAtUnixMs == 0)
	default:
		return false
	}
}

func nativePatchFields(patch *nativev1.ClaudeProviderPatch) (nativesessionauth.PatchFields, bool) {
	var result nativesessionauth.PatchFields
	if patch == nil || len(patch.Operations) != 7 {
		return result, false
	}
	for index, operation := range patch.Operations {
		if operation == nil || operation.Slot != nativev1.EnvSlot(index+1) {
			return result, false
		}
		switch action := operation.Action.(type) {
		case *nativev1.PatchOperation_SetLiteral:
			if action.SetLiteral == nil {
				return result, false
			}
			result[index] = nativesessionauth.PatchOperation{Action: 1, Literal: action.SetLiteral.Value}
		case *nativev1.PatchOperation_SetSelectedSecret:
			if action.SetSelectedSecret == nil {
				return result, false
			}
			result[index] = nativesessionauth.PatchOperation{Action: 2, SecretSlot: uint32(action.SetSelectedSecret.Slot)}
		case *nativev1.PatchOperation_Remove:
			if action.Remove == nil {
				return result, false
			}
			result[index].Action = 3
		default:
			return result, false
		}
	}
	return result, true
}

func nativeClearWire(message proto.Message) {
	switch value := message.(type) {
	case *nativev1.BindNativeConsumerResponse:
		clear(value.NativeSessionToken)
	case *nativev1.AuthorizeNativeMaterializationResponse:
		clear(value.GrantToken)
	case *nativev1.ConsumeNativeMaterializationResponse:
		for _, slot := range value.SlotValues {
			if slot != nil {
				clear(slot.Plaintext)
			}
		}
	case *nativev1.ConsumeNativeMaterializationRequest:
		clear(value.GrantToken)
	}
}
