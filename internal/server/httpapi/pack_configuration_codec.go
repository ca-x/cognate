package httpapi

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"io"
	"math"
	"strconv"

	"connectrpc.com/connect"
	packconfigurationv1 "github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type packConfigurationCodec struct{ name string }

func (codec packConfigurationCodec) Name() string { return codec.name }

func (codec packConfigurationCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, errors.New("invalid pack configuration response")
	}
	var body []byte
	var err error
	if codec.name == "json" {
		body, err = protojson.Marshal(message)
	} else {
		body, err = proto.Marshal(message)
	}
	if err != nil {
		return nil, errors.New("invalid pack configuration response")
	}
	if len(body) > packConfigurationMaxBytes {
		clear(body)
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("pack configuration response limit exceeded"))
	}
	return body, nil
}

func (codec packConfigurationCodec) Unmarshal(body []byte, value any) error {
	invalid := func() error {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid pack configuration request"))
	}
	message, ok := value.(proto.Message)
	if !ok || len(body) > packConfigurationMaxBytes {
		return invalid()
	}
	switch message.(type) {
	case *packconfigurationv1.GetAttachmentsRequest, *packconfigurationv1.ReplaceAttachmentsRequest:
	default:
		return invalid()
	}
	fresh := message.ProtoReflect().Type().New().Interface()
	var err error
	if codec.name == "json" {
		if !packConfigurationJSONValid(body, fresh.ProtoReflect().Descriptor(), 0) {
			return invalid()
		}
		err = (protojson.UnmarshalOptions{RecursionLimit: 8}).Unmarshal(body, fresh)
	} else {
		if !packConfigurationWireValid(body, fresh.ProtoReflect().Descriptor(), 0) {
			return invalid()
		}
		err = (proto.UnmarshalOptions{RecursionLimit: 8}).Unmarshal(body, fresh)
	}
	if err != nil {
		return invalid()
	}
	proto.Reset(message)
	proto.Merge(message, fresh)
	return nil
}

func packConfigurationWireValid(body []byte, descriptor protoreflect.MessageDescriptor, depth int) bool {
	if depth > 4 {
		return false
	}
	seen := make(map[protoreflect.FieldNumber]int)
	for len(body) > 0 {
		number, wire, consumed := protowire.ConsumeTag(body)
		if consumed < 0 {
			return false
		}
		field := descriptor.Fields().ByNumber(number)
		if field == nil || !field.IsList() && seen[number] != 0 {
			return false
		}
		seen[number]++
		if field.IsList() && seen[number] > state.MaxPackConfigurationAttachments {
			return false
		}
		body = body[consumed:]
		var size int
		switch field.Kind() {
		case protoreflect.StringKind, protoreflect.MessageKind:
			if wire != protowire.BytesType {
				return false
			}
			var value []byte
			value, size = protowire.ConsumeBytes(body)
			if size < 0 || field.Message() != nil && !packConfigurationWireValid(value, field.Message(), depth+1) {
				return false
			}
		case protoreflect.Int64Kind:
			if wire != protowire.VarintType {
				return false
			}
			var value uint64
			value, size = protowire.ConsumeVarint(body)
			if size < 0 || value > math.MaxInt64 {
				return false
			}
		default:
			return false
		}
		body = body[size:]
	}
	return true
}

func packConfigurationJSONValid(body []byte, descriptor protoreflect.MessageDescriptor, depth int) bool {
	if depth > 4 {
		return false
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(body))
	if decoder.PeekKind() != '{' {
		return false
	}
	if _, err := decoder.ReadToken(); err != nil {
		return false
	}
	seen := make(map[protoreflect.FieldNumber]bool)
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
				if err != nil || count > state.MaxPackConfigurationAttachments || !packConfigurationJSONValid(item, field.Message(), depth+1) {
					return false
				}
			}
			if _, err := array.ReadToken(); err != nil {
				return false
			}
			continue
		}
		switch field.Kind() {
		case protoreflect.StringKind:
			if value.Kind() != '"' {
				return false
			}
		case protoreflect.Int64Kind:
			number := string(value)
			if value.Kind() == '"' {
				text := jsontext.NewDecoder(bytes.NewReader(value))
				token, err := text.ReadToken()
				if err != nil {
					return false
				}
				number = token.String()
			} else if value.Kind() != '0' {
				return false
			}
			parsed, err := strconv.ParseInt(number, 10, 64)
			if err != nil || parsed < 0 {
				return false
			}
		default:
			return false
		}
	}
	if _, err := decoder.ReadToken(); err != nil {
		return false
	}
	_, err := decoder.ReadToken()
	return err == io.EOF
}
