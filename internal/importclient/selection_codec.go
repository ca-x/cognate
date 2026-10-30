package importclient

import (
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"math"
)

type selectionCodec struct{}

func (selectionCodec) Name() string { return "proto" }
func (selectionCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, errSelection
	}
	data, err := proto.Marshal(message)
	limit := 24 << 20
	if _, status := message.(*importv1.GetImportCommitStatusRequest); status {
		limit = 64 << 10
	}
	if err != nil || len(data) > limit {
		return nil, errSelection
	}
	return data, nil
}
func (selectionCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	budget := 2 << 20
	limit := 2 << 20
	if _, status := message.(*importv1.GetImportCommitStatusResponse); status {
		limit = 128 << 10
	}
	if !ok || len(data) > limit || !selectionWire(data, message.ProtoReflect().Descriptor(), 0, &budget) {
		return errPreview
	}
	if err := (proto.UnmarshalOptions{RecursionLimit: 32}).Unmarshal(data, message); err != nil || !selectionKnown(message.ProtoReflect(), 0) {
		return errPreview
	}
	return nil
}
func selectionWire(data []byte, descriptor protoreflect.MessageDescriptor, depth int, budget *int) bool {
	if depth > 32 {
		return false
	}
	seen := make(map[protoreflect.FieldNumber]bool)
	oneofs := make(map[protoreflect.FullName]bool)
	for len(data) > 0 {
		*budget--
		if *budget < 0 {
			return false
		}
		number, kind, prefix := protowire.ConsumeTag(data)
		if prefix < 0 || prefix != protowire.SizeVarint(uint64(number)<<3|uint64(kind)) {
			return false
		}
		field := descriptor.Fields().ByNumber(protoreflect.FieldNumber(number))
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
		data = data[prefix:]
		if kind == protowire.BytesType {
			value, size := protowire.ConsumeBytes(data)
			if size < 0 || size-len(value) != protowire.SizeVarint(uint64(len(value))) {
				return false
			}
			if field.Message() != nil {
				if !selectionWire(value, field.Message(), depth+1, budget) {
					return false
				}
			} else if field.Kind() != protoreflect.StringKind && field.Kind() != protoreflect.BytesKind {
				if !field.IsPacked() {
					return false
				}
				for len(value) > 0 {
					if selectionScalarWire(field) != protowire.VarintType {
						return false
					}
					encoded, count := protowire.ConsumeVarint(value)
					if !selectionVarint(field, encoded, count) {
						return false
					}
					value = value[count:]
				}
			}
			data = data[size:]
		} else {
			if field.Message() != nil || field.Kind() == protoreflect.StringKind || field.Kind() == protoreflect.BytesKind {
				return false
			}
			size := protowire.ConsumeFieldValue(number, kind, data)
			if size < 0 || kind != selectionScalarWire(field) {
				return false
			}
			if kind == protowire.VarintType {
				value, count := protowire.ConsumeVarint(data)
				if !selectionVarint(field, value, count) {
					return false
				}
			}
			data = data[size:]
		}
	}
	return true
}

func selectionScalarWire(field protoreflect.FieldDescriptor) protowire.Type {
	switch field.Kind() {
	case protoreflect.DoubleKind, protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return protowire.Fixed64Type
	case protoreflect.FloatKind, protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		return protowire.Fixed32Type
	default:
		return protowire.VarintType
	}
}

func selectionVarint(field protoreflect.FieldDescriptor, value uint64, count int) bool {
	if count <= 0 || count != protowire.SizeVarint(value) {
		return false
	}
	switch field.Kind() {
	case protoreflect.BoolKind:
		return value <= 1
	case protoreflect.EnumKind:
		return value <= math.MaxInt32 && field.Enum().Values().ByNumber(protoreflect.EnumNumber(value)) != nil
	case protoreflect.Uint32Kind, protoreflect.Sint32Kind:
		return value <= math.MaxUint32
	case protoreflect.Int32Kind:
		return value <= math.MaxInt32 || value >= math.MaxUint64-math.MaxInt32
	}
	return true
}
