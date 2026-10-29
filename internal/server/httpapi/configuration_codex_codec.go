package httpapi

import (
	"bytes"
	"encoding/json/jsontext"
	"io"
	"math"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func configurationEnumWireValid(data []byte, wireType protowire.Type, field protoreflect.FieldDescriptor) bool {
	if wireType == protowire.BytesType && field.IsList() {
		packed, consumed := protowire.ConsumeBytes(data)
		if consumed < 0 {
			return false
		}
		data = packed
	} else if wireType != protowire.VarintType {
		return false
	}
	for len(data) > 0 {
		value, consumed := protowire.ConsumeVarint(data)
		if consumed < 0 || value > math.MaxInt32 || field.Enum().Values().ByNumber(protoreflect.EnumNumber(value)) == nil {
			return false
		}
		data = data[consumed:]
	}
	return true
}

func configurationCodexJSONValid(data []byte, descriptor protoreflect.MessageDescriptor, codex bool, depth int) bool {
	if depth > 16 {
		return false
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	if decoder.PeekKind() == 'n' {
		return !codex
	}
	if decoder.PeekKind() != '{' {
		return false
	}
	if _, err := decoder.ReadToken(); err != nil {
		return false
	}
	for decoder.PeekKind() != '}' {
		name, err := decoder.ReadToken()
		if err != nil || name.Kind() != '"' {
			return false
		}
		field := descriptor.Fields().ByJSONName(name.String())
		if field == nil {
			field = descriptor.Fields().ByName(protoreflect.Name(name.String()))
		}
		if field == nil {
			return false
		}
		value, err := decoder.ReadValue()
		if err != nil {
			return false
		}
		inside := codex || field.FullName() == "skillhub.configuration.v1.Draft.codex_provider"
		if value.Kind() == 'n' {
			if inside {
				return false
			}
			continue
		}
		if field.IsList() {
			if value.Kind() != '[' {
				return false
			}
			array := jsontext.NewDecoder(bytes.NewReader(value))
			if _, err := array.ReadToken(); err != nil {
				return false
			}
			for array.PeekKind() != ']' {
				item, err := array.ReadValue()
				if err != nil || inside && item.Kind() == 'n' {
					return false
				}
				if field.Message() != nil && !configurationCodexJSONValid(item, field.Message(), inside, depth+1) {
					return false
				}
			}
		} else if field.Message() != nil && !configurationCodexJSONValid(value, field.Message(), inside, depth+1) {
			return false
		}
	}
	if _, err := decoder.ReadToken(); err != nil {
		return false
	}
	_, err := decoder.ReadToken()
	return err == io.EOF
}
