package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"math"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const mcpConfigurationMaxBytes = 2 << 20

type mcpConfigurationCodec struct{ name string }

func (codec mcpConfigurationCodec) Name() string { return codec.name }

func (codec mcpConfigurationCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, mcpConfigurationError(nil, nil)
	}
	var data []byte
	var err error
	if codec.name == "json" {
		data, err = protojson.Marshal(message)
	} else {
		data, err = proto.Marshal(message)
	}
	if err != nil {
		return nil, mcpConfigurationError(nil, nil)
	}
	if len(data) > mcpConfigurationMaxBytes {
		clear(data)
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("MCP response capacity exhausted"))
	}
	return data, nil
}

func (codec mcpConfigurationCodec) Unmarshal(data []byte, value any) error {
	invalid := func() error {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid MCP configuration request"))
	}
	message, ok := value.(proto.Message)
	if !ok || len(data) > mcpConfigurationMaxBytes {
		return invalid()
	}
	descriptor := message.ProtoReflect().Descriptor()
	if string(descriptor.ParentFile().Package()) != "skillhub.configuration.mcp.v1" || !strings.HasSuffix(string(descriptor.Name()), "Request") {
		return invalid()
	}
	fresh := message.ProtoReflect().Type().New().Interface()
	var err error
	if codec.name == "json" {
		if !mcpConfigurationJSONValid(data, descriptor, 0) {
			return invalid()
		}
		err = (protojson.UnmarshalOptions{RecursionLimit: 24}).Unmarshal(data, fresh)
	} else {
		if !mcpConfigurationWireValid(data, descriptor, 0) {
			return invalid()
		}
		err = (proto.UnmarshalOptions{RecursionLimit: 12}).Unmarshal(data, fresh)
	}
	if err != nil {
		mcpConfigurationWipe(fresh.ProtoReflect())
		return invalid()
	}
	proto.Reset(message)
	proto.Merge(message, fresh)
	mcpConfigurationWipe(fresh.ProtoReflect())
	return nil
}

func mcpConfigurationWipe(message protoreflect.Message) {
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		wipe := func(item protoreflect.Value) {
			if field.Kind() == protoreflect.BytesKind {
				clear(item.Bytes())
			} else if field.Message() != nil {
				mcpConfigurationWipe(item.Message())
			}
		}
		if field.IsList() {
			for index := range value.List().Len() {
				wipe(value.List().Get(index))
			}
		} else {
			wipe(value)
		}
		return true
	})
}

func mcpConfigurationListLimit(field protoreflect.FieldDescriptor) int {
	switch field.ContainingMessage().Name() {
	case "Document":
		return 32
	case "Args", "NamedSlots":
		return 64
	default:
		return 1024
	}
}

func mcpConfigurationWireValid(data []byte, descriptor protoreflect.MessageDescriptor, depth int) bool {
	if depth > 12 {
		return false
	}
	seen := map[protoreflect.FieldNumber]int{}
	oneofs := map[protoreflect.FullName]bool{}
	for len(data) != 0 {
		number, wire, size := protowire.ConsumeTag(data)
		if size < 0 {
			return false
		}
		field := descriptor.Fields().ByNumber(number)
		if field == nil || !field.IsList() && seen[number] != 0 {
			return false
		}
		seen[number]++
		if field.IsList() && seen[number] > mcpConfigurationListLimit(field) {
			return false
		}
		if oneof := field.ContainingOneof(); oneof != nil {
			if oneofs[oneof.FullName()] {
				return false
			}
			oneofs[oneof.FullName()] = true
		}
		data = data[size:]
		switch field.Kind() {
		case protoreflect.StringKind, protoreflect.BytesKind, protoreflect.MessageKind:
			if wire != protowire.BytesType {
				return false
			}
			var nested []byte
			nested, size = protowire.ConsumeBytes(data)
			if size < 0 || field.Message() != nil && !mcpConfigurationWireValid(nested, field.Message(), depth+1) {
				return false
			}
			if field.Kind() == protoreflect.StringKind && len(nested) > 4096 || field.Kind() == protoreflect.BytesKind && len(nested) > 8192 {
				return false
			}
		case protoreflect.BoolKind, protoreflect.Int64Kind:
			if wire != protowire.VarintType {
				return false
			}
			var value uint64
			value, size = protowire.ConsumeVarint(data)
			if size < 0 || value > math.MaxInt64 || field.Kind() == protoreflect.BoolKind && value > 1 {
				return false
			}
		default:
			return false
		}
		data = data[size:]
	}
	return true
}

func mcpConfigurationJSONValid(data []byte, descriptor protoreflect.MessageDescriptor, depth int) bool {
	if depth > 12 {
		return false
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	if decoder.PeekKind() != '{' {
		return false
	}
	if _, err := decoder.ReadToken(); err != nil {
		return false
	}
	seen := map[protoreflect.FieldNumber]bool{}
	oneofs := map[protoreflect.FullName]bool{}
	for decoder.PeekKind() != '}' {
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
		value, err := decoder.ReadValue()
		if err != nil || value.Kind() == 'n' {
			return false
		}
		if field.IsList() {
			if value.Kind() != '[' {
				return false
			}
			array := jsontext.NewDecoder(bytes.NewReader(value))
			if _, err := array.ReadToken(); err != nil {
				return false
			}
			count := 0
			for array.PeekKind() != ']' {
				item, err := array.ReadValue()
				count++
				if err != nil || count > mcpConfigurationListLimit(field) || !mcpConfigurationJSONValue(item, field, depth+1) {
					return false
				}
			}
			if _, err := array.ReadToken(); err != nil {
				return false
			}
		} else if !mcpConfigurationJSONValue(value, field, depth+1) {
			return false
		}
	}
	if _, err := decoder.ReadToken(); err != nil {
		return false
	}
	_, err := decoder.ReadToken()
	return errors.Is(err, io.EOF)
}

func mcpConfigurationJSONValue(value jsontext.Value, field protoreflect.FieldDescriptor, depth int) bool {
	switch field.Kind() {
	case protoreflect.MessageKind:
		return mcpConfigurationJSONValid(value, field.Message(), depth)
	case protoreflect.StringKind, protoreflect.BytesKind:
		if value.Kind() != '"' {
			return false
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			return false
		}
		if field.Kind() == protoreflect.StringKind {
			return len(text) <= 4096
		}
		if len(text) > base64.StdEncoding.EncodedLen(8192) {
			return false
		}
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			decoded, err := encoding.DecodeString(text)
			size := len(decoded)
			clear(decoded)
			if err == nil {
				return size <= 8192
			}
		}
		return false
	case protoreflect.BoolKind:
		return value.Kind() == 't' || value.Kind() == 'f'
	case protoreflect.Int64Kind:
		return value.Kind() == '0' || value.Kind() == '"'
	default:
		return false
	}
}
