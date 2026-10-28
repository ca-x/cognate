package httpapi

import (
	"errors"
	"math"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type configurationCodec struct{ name string }

func (codec configurationCodec) Name() string { return codec.name }

func (codec configurationCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, errors.New("invalid configuration response")
	}
	var data []byte
	var err error
	if codec.name == "json" {
		data, err = protojson.Marshal(message)
	} else {
		data, err = proto.Marshal(message)
	}
	if err != nil {
		return nil, errors.New("invalid configuration response")
	}
	if len(data) > configurationMaxResponseBytes {
		return nil, errors.New("configuration response size limit exceeded")
	}
	return data, nil
}

func (codec configurationCodec) Unmarshal(data []byte, value any) error {
	invalid := func() error {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid configuration request"))
	}
	message, ok := value.(proto.Message)
	if !ok {
		return invalid()
	}
	var err error
	if codec.name == "json" {
		err = (protojson.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(data, message)
	} else {
		if !configurationWireValid(data, message.ProtoReflect().Descriptor(), 0) {
			return invalid()
		}
		err = (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(data, message)
	}
	if err != nil || !configurationMessageValid(message.ProtoReflect()) {
		return invalid()
	}
	return nil
}

func configurationMessageValid(message protoreflect.Message) bool {
	valid := len(message.GetUnknown()) == 0
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		check := func(value protoreflect.Value) bool {
			if field.Message() != nil {
				return configurationMessageValid(value.Message())
			}
			return field.Enum() == nil || field.Enum().Values().ByNumber(value.Enum()) != nil
		}
		if field.IsList() {
			for index := range value.List().Len() {
				if !check(value.List().Get(index)) {
					valid = false
					return false
				}
			}
		} else {
			valid = valid && check(value)
		}
		return valid
	})
	return valid
}

func configurationWireValid(data []byte, descriptor protoreflect.MessageDescriptor, depth int) bool {
	if depth > 16 {
		return false
	}
	seen := make(map[protoreflect.FieldNumber]bool)
	oneofs := make(map[protoreflect.FullName]bool)
	for len(data) != 0 {
		number, wireType, tagBytes := protowire.ConsumeTag(data)
		if tagBytes < 0 {
			return false
		}
		field := descriptor.Fields().ByNumber(number)
		if field == nil || (!field.IsList() && seen[number]) {
			return false
		}
		seen[number] = true
		if oneof := field.ContainingOneof(); oneof != nil {
			if oneofs[oneof.FullName()] {
				return false
			}
			oneofs[oneof.FullName()] = true
		}
		data = data[tagBytes:]
		valueBytes := protowire.ConsumeFieldValue(number, wireType, data)
		if valueBytes < 0 {
			return false
		}
		if field.Message() != nil {
			if wireType != protowire.BytesType {
				return false
			}
			nested, consumed := protowire.ConsumeBytes(data)
			if consumed < 0 || !configurationWireValid(nested, field.Message(), depth+1) {
				return false
			}
		} else if field.Enum() != nil {
			if wireType != protowire.VarintType {
				return false
			}
			value, consumed := protowire.ConsumeVarint(data)
			if consumed < 0 || value > math.MaxInt32 || field.Enum().Values().ByNumber(protoreflect.EnumNumber(value)) == nil {
				return false
			}
		}
		data = data[valueBytes:]
	}
	return true
}
