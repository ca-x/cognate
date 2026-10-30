package httpapi

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"io"
	"math"
	"unicode/utf8"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const importSelectionCodecDepth = 16
const importSelectionCodecValues = 65536

func importSelectionUnmarshal(data []byte, message proto.Message, encoding string) error {
	invalid := func() error {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid export request"))
	}
	maximum := importSnapshotMaxWireBytes
	if _, status := message.(*importv1.GetImportCommitStatusRequest); status {
		maximum = 64 << 10
	}
	if !message.ProtoReflect().IsValid() || len(data) > maximum {
		return invalid()
	}
	descriptor := message.ProtoReflect().Descriptor()
	remaining := importSelectionCodecValues
	temporary := message.ProtoReflect().New().Interface()
	if encoding == "json" {
		decoder := jsontext.NewDecoder(bytes.NewReader(data))
		if !importSelectionJSONMessage(decoder, descriptor, 0, &remaining) {
			return invalid()
		}
		if _, err := decoder.ReadToken(); err != io.EOF {
			return invalid()
		}
		if (protojson.UnmarshalOptions{RecursionLimit: importSelectionCodecDepth}).Unmarshal(data, temporary) != nil || !importSelectionEnumsValid(temporary.ProtoReflect()) {
			return invalid()
		}
	} else {
		if !importSelectionWireValid(data, descriptor, 0, &remaining) {
			return invalid()
		}
		if (proto.UnmarshalOptions{RecursionLimit: importSelectionCodecDepth}).Unmarshal(data, temporary) != nil {
			return invalid()
		}
	}
	if !importSelectionOptionsValid(temporary) {
		return invalid()
	}
	proto.Reset(message)
	proto.Merge(message, temporary)
	return nil
}

func importSelectionOptionsValid(message proto.Message) bool {
	var options []*importv1.ImportCandidateOptions
	switch request := message.(type) {
	case *importv1.PreviewImportSelectionRequest:
		options = request.Options
	case *importv1.CommitImportSelectionRequest:
		options = request.Options
	}
	for _, option := range options {
		if option.SkillBinding != nil && (option.Name != nil || option.Auth != nil || option.GeminiKeyTransport != nil) {
			return false
		}
		if _, claude := option.Auth.(*importv1.ImportCandidateOptions_ClaudeAuthMode); claude && option.GeminiKeyTransport != nil {
			return false
		}
	}
	return true
}

func importSelectionJSONMessage(decoder *jsontext.Decoder, descriptor protoreflect.MessageDescriptor, depth int, remaining *int) bool {
	if depth >= importSelectionCodecDepth {
		return false
	}
	token, err := decoder.ReadToken()
	if err != nil || token.Kind() != '{' {
		return false
	}
	seen := make(map[protoreflect.FieldNumber]bool)
	oneofs := make(map[protoreflect.FullName]bool)
	for decoder.PeekKind() != '}' {
		*remaining--
		if *remaining < 0 {
			return false
		}
		name, err := decoder.ReadToken()
		if err != nil || name.Kind() != '"' {
			return false
		}
		field := descriptor.Fields().ByJSONName(name.String())
		if field == nil {
			field = descriptor.Fields().ByName(protoreflect.Name(name.String()))
		}
		if field == nil || seen[field.Number()] {
			return false
		}
		seen[field.Number()] = true
		if oneof := field.ContainingOneof(); oneof != nil {
			if oneofs[oneof.FullName()] {
				return false
			}
			oneofs[oneof.FullName()] = true
		}
		if field.IsList() {
			start, err := decoder.ReadToken()
			if err != nil || start.Kind() != '[' {
				return false
			}
			for decoder.PeekKind() != ']' {
				*remaining--
				if *remaining < 0 || !importSelectionJSONValue(decoder, field, depth, remaining) {
					return false
				}
			}
			if _, err := decoder.ReadToken(); err != nil {
				return false
			}
		} else if !importSelectionJSONValue(decoder, field, depth, remaining) {
			return false
		}
	}
	_, err = decoder.ReadToken()
	return err == nil
}

func importSelectionJSONValue(decoder *jsontext.Decoder, field protoreflect.FieldDescriptor, depth int, remaining *int) bool {
	if field.Kind() == protoreflect.MessageKind {
		return importSelectionJSONMessage(decoder, field.Message(), depth+1, remaining)
	}
	token, err := decoder.ReadToken()
	if err != nil {
		return false
	}
	switch token.Kind() {
	case '"', '0', 't', 'f':
		return true
	default:
		return false
	}
}

func importSelectionEnumsValid(message protoreflect.Message) bool {
	valid := true
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		check := func(value protoreflect.Value) bool {
			if field.Kind() == protoreflect.MessageKind {
				return importSelectionEnumsValid(value.Message())
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
			valid = check(value)
		}
		return valid
	})
	return valid
}

func importSelectionWireValid(data []byte, descriptor protoreflect.MessageDescriptor, depth int, remaining *int) bool {
	if depth >= importSelectionCodecDepth {
		return false
	}
	seen := make(map[protoreflect.FieldNumber]bool)
	oneofs := make(map[protoreflect.FullName]bool)
	for len(data) > 0 {
		*remaining--
		if *remaining < 0 {
			return false
		}
		number, wireType, consumed := protowire.ConsumeTag(data)
		if consumed < 0 || number > protowire.MaxValidNumber {
			return false
		}
		field := descriptor.Fields().ByNumber(number)
		if field == nil || !field.IsList() && seen[number] {
			return false
		}
		seen[number] = true
		if oneof := field.ContainingOneof(); oneof != nil {
			if oneofs[oneof.FullName()] {
				return false
			}
			oneofs[oneof.FullName()] = true
		}
		data = data[consumed:]
		if wireType == protowire.BytesType {
			value, consumed := protowire.ConsumeBytes(data)
			if consumed < 0 {
				return false
			}
			switch field.Kind() {
			case protoreflect.MessageKind:
				if !importSelectionWireValid(value, field.Message(), depth+1, remaining) {
					return false
				}
			case protoreflect.StringKind:
				if !utf8.Valid(value) {
					return false
				}
			case protoreflect.BytesKind:
			default:
				if !field.IsList() {
					return false
				}
				for len(value) > 0 {
					*remaining--
					count := importSelectionScalar(value, importSelectionScalarWire(field.Kind()), field)
					if *remaining < 0 || count < 0 {
						return false
					}
					value = value[count:]
				}
			}
			data = data[consumed:]
		} else {
			consumed := importSelectionScalar(data, wireType, field)
			if consumed < 0 {
				return false
			}
			data = data[consumed:]
		}
	}
	return true
}

func importSelectionScalarWire(kind protoreflect.Kind) protowire.Type {
	switch kind {
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return protowire.Fixed32Type
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return protowire.Fixed64Type
	case protoreflect.BoolKind, protoreflect.EnumKind, protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind:
		return protowire.VarintType
	default:
		return -1
	}
}

func importSelectionScalar(data []byte, wireType protowire.Type, field protoreflect.FieldDescriptor) int {
	if wireType != importSelectionScalarWire(field.Kind()) {
		return -1
	}
	switch wireType {
	case protowire.Fixed32Type:
		_, consumed := protowire.ConsumeFixed32(data)
		return consumed
	case protowire.Fixed64Type:
		_, consumed := protowire.ConsumeFixed64(data)
		return consumed
	case protowire.VarintType:
		value, consumed := protowire.ConsumeVarint(data)
		if consumed < 0 {
			return -1
		}
		switch field.Kind() {
		case protoreflect.BoolKind:
			if value > 1 {
				return -1
			}
		case protoreflect.Uint32Kind, protoreflect.Sint32Kind:
			if value > math.MaxUint32 {
				return -1
			}
		case protoreflect.Int32Kind, protoreflect.EnumKind:
			if value > math.MaxInt32 && value < math.MaxUint64-math.MaxInt32 {
				return -1
			}
			if field.Enum() != nil && field.Enum().Values().ByNumber(protoreflect.EnumNumber(int32(value))) == nil {
				return -1
			}
		}
		return consumed
	default:
		return -1
	}
}
