package httpapi

import (
	"bytes"
	"encoding/json/jsontext"
	"io"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/agentconfig"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type configurationOpenCodeScan struct {
	fields int
}

func configurationOpenCodeRecursionLimit(data []byte, descriptor protoreflect.MessageDescriptor, jsonFormat bool) int {
	switch descriptor.FullName() {
	case "skillhub.configuration.v1.CreateAssetRequest", "skillhub.configuration.v1.UpdateAssetRequest":
	default:
		return 16
	}
	draftField := descriptor.Fields().ByName("draft")
	draft, found := configurationOpenCodeEncodedField(data, descriptor, draftField, jsonFormat)
	if !found {
		return 16
	}
	providerField := draftField.Message().Fields().ByName("opencode_provider")
	if _, found := configurationOpenCodeEncodedField(draft, draftField.Message(), providerField, jsonFormat); found {
		return 96
	}
	return 16
}

func configurationOpenCodeEncodedField(data []byte, descriptor protoreflect.MessageDescriptor, wanted protoreflect.FieldDescriptor, jsonFormat bool) ([]byte, bool) {
	if wanted == nil {
		return nil, false
	}
	if jsonFormat {
		decoder := jsontext.NewDecoder(bytes.NewReader(data))
		if decoder.PeekKind() != '{' {
			return nil, false
		}
		if _, err := decoder.ReadToken(); err != nil {
			return nil, false
		}
		for decoder.PeekKind() != '}' {
			name, err := decoder.ReadToken()
			if err != nil || name.Kind() != '"' {
				return nil, false
			}
			field := descriptor.Fields().ByJSONName(name.String())
			if field == nil {
				field = descriptor.Fields().ByName(protoreflect.Name(name.String()))
			}
			value, err := decoder.ReadValue()
			if err != nil {
				return nil, false
			}
			if field == wanted {
				return value, true
			}
		}
		return nil, false
	}
	for len(data) != 0 {
		number, wireType, tagBytes := protowire.ConsumeTag(data)
		if tagBytes < 0 {
			return nil, false
		}
		data = data[tagBytes:]
		consumed := protowire.ConsumeFieldValue(number, wireType, data)
		if consumed < 0 {
			return nil, false
		}
		if number == wanted.Number() && wireType == protowire.BytesType {
			value, size := protowire.ConsumeBytes(data)
			return value, size >= 0
		}
		data = data[consumed:]
	}
	return nil, false
}

func configurationOpenCodeMessageValid(message protoreflect.Message) bool {
	scan := &configurationOpenCodeScan{}
	var visit func(protoreflect.Message, int) bool
	visit = func(message protoreflect.Message, depth int) bool {
		if !message.IsValid() || len(message.GetUnknown()) != 0 || !scan.count(depth) {
			return false
		}
		valid := true
		message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
			if !scan.count(depth) {
				valid = false
				return false
			}
			switch {
			case field.IsMap():
				value.Map().Range(func(_ protoreflect.MapKey, item protoreflect.Value) bool {
					valid = scan.count(depth) && (field.MapValue().Message() == nil || visit(item.Message(), depth+1))
					return valid
				})
			case field.IsList():
				for index := range value.List().Len() {
					if !scan.count(depth) || field.Message() != nil && !visit(value.List().Get(index).Message(), depth+1) {
						valid = false
						break
					}
				}
			case field.Message() != nil:
				valid = visit(value.Message(), depth+1)
			}
			return valid
		})
		return valid
	}
	return visit(message, 0)
}

func configurationOpenCodeJSONValid(data []byte, descriptor protoreflect.MessageDescriptor) bool {
	if len(data) > agentconfig.MaxOpenCodeRequestBytes {
		return false
	}
	scan := &configurationOpenCodeScan{}
	return scan.jsonMessage(data, descriptor, 0)
}

func configurationOpenCodeWireValid(data []byte, descriptor protoreflect.MessageDescriptor) bool {
	if len(data) > agentconfig.MaxOpenCodeRequestBytes {
		return false
	}
	scan := &configurationOpenCodeScan{}
	return scan.wireMessage(data, descriptor, 0)
}

func (scan *configurationOpenCodeScan) count(depth int) bool {
	scan.fields++
	return depth <= 64 && scan.fields <= 16384
}

func configurationOpenCodeOneofsValid(descriptor protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) bool {
	for index := range descriptor.Oneofs().Len() {
		oneof := descriptor.Oneofs().Get(index)
		if !oneof.IsSynthetic() && !seen[oneof.FullName()] {
			return false
		}
	}
	return true
}

func configurationOpenCodeBooleanValid(field protoreflect.FieldDescriptor, value bool) bool {
	switch field.FullName() {
	case "skillhub.configuration.v1.OpenCodeValue.null":
		return value
	case "skillhub.configuration.v1.OpenCodeTimeout.disabled":
		return !value
	default:
		return true
	}
}

func (scan *configurationOpenCodeScan) jsonMessage(data []byte, descriptor protoreflect.MessageDescriptor, depth int) bool {
	if !scan.count(depth) {
		return false
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	if decoder.PeekKind() != '{' {
		return false
	}
	if _, err := decoder.ReadToken(); err != nil {
		return false
	}
	seen := make(map[protoreflect.FieldNumber]bool)
	oneofs := make(map[protoreflect.FullName]bool)
	for decoder.PeekKind() != '}' {
		name, err := decoder.ReadToken()
		if err != nil || name.Kind() != '"' {
			return false
		}
		field := descriptor.Fields().ByJSONName(name.String())
		if field == nil {
			field = descriptor.Fields().ByName(protoreflect.Name(name.String()))
		}
		if field == nil || seen[field.Number()] || !scan.count(depth) {
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
		if err != nil || value.Kind() == 'n' || !scan.jsonField(value, field, depth+1) {
			return false
		}
	}
	if _, err := decoder.ReadToken(); err != nil {
		return false
	}
	_, err := decoder.ReadToken()
	return err == io.EOF && configurationOpenCodeOneofsValid(descriptor, oneofs)
}

func (scan *configurationOpenCodeScan) jsonField(data jsontext.Value, field protoreflect.FieldDescriptor, depth int) bool {
	if field.IsMap() {
		if data.Kind() != '{' {
			return false
		}
		decoder := jsontext.NewDecoder(bytes.NewReader(data))
		if _, err := decoder.ReadToken(); err != nil {
			return false
		}
		keys := make(map[string]bool)
		for decoder.PeekKind() != '}' {
			key, err := decoder.ReadToken()
			if err != nil || key.Kind() != '"' || keys[key.String()] || len(keys) >= 2048 || !scan.count(depth) {
				return false
			}
			keys[key.String()] = true
			value, err := decoder.ReadValue()
			if err != nil || !scan.jsonSingular(value, field.MapValue(), depth+1) {
				return false
			}
		}
		if _, err := decoder.ReadToken(); err != nil {
			return false
		}
		_, err := decoder.ReadToken()
		return err == io.EOF
	}
	if field.IsList() {
		if data.Kind() != '[' {
			return false
		}
		decoder := jsontext.NewDecoder(bytes.NewReader(data))
		if _, err := decoder.ReadToken(); err != nil {
			return false
		}
		count := 0
		for decoder.PeekKind() != ']' {
			count++
			value, err := decoder.ReadValue()
			if err != nil || count > 2048 || !scan.count(depth) || !scan.jsonSingular(value, field, depth+1) {
				return false
			}
		}
		if _, err := decoder.ReadToken(); err != nil {
			return false
		}
		_, err := decoder.ReadToken()
		return err == io.EOF
	}
	return scan.jsonSingular(data, field, depth)
}

func (scan *configurationOpenCodeScan) jsonSingular(data jsontext.Value, field protoreflect.FieldDescriptor, depth int) bool {
	if field.FullName() == "skillhub.configuration.v1.OpenCodeValue.literal" {
		return false
	}
	if field.Message() != nil {
		return scan.jsonMessage(data, field.Message(), depth)
	}
	switch field.Kind() {
	case protoreflect.StringKind:
		return data.Kind() == '"'
	case protoreflect.BoolKind:
		return (data.Kind() == 't' || data.Kind() == 'f') && configurationOpenCodeBooleanValid(field, data.Kind() == 't')
	default:
		return false
	}
}

func (scan *configurationOpenCodeScan) wireMessage(data []byte, descriptor protoreflect.MessageDescriptor, depth int) bool {
	if !scan.count(depth) {
		return false
	}
	seen := make(map[protoreflect.FieldNumber]bool)
	oneofs := make(map[protoreflect.FullName]bool)
	mapKeys := make(map[protoreflect.FieldNumber]map[string]bool)
	for len(data) != 0 {
		number, wireType, tagBytes := protowire.ConsumeTag(data)
		if tagBytes < 0 {
			return false
		}
		field := descriptor.Fields().ByNumber(number)
		if field == nil || seen[number] && !field.IsList() && !field.IsMap() || !scan.count(depth) {
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
		consumed := protowire.ConsumeFieldValue(number, wireType, data)
		if consumed < 0 {
			return false
		}
		value := data[:consumed]
		if field.IsMap() {
			if wireType != protowire.BytesType {
				return false
			}
			entry, size := protowire.ConsumeBytes(value)
			if size < 0 || !scan.wireMessage(entry, field.Message(), depth+1) {
				return false
			}
			key, ok := configurationOpenCodeMapKey(entry)
			if !ok {
				return false
			}
			if mapKeys[number] == nil {
				mapKeys[number] = make(map[string]bool)
			}
			if mapKeys[number][key] || len(mapKeys[number]) >= 2048 {
				return false
			}
			mapKeys[number][key] = true
		} else if !scan.wireSingular(value, wireType, field, depth+1) {
			return false
		}
		data = data[consumed:]
	}
	return configurationOpenCodeOneofsValid(descriptor, oneofs)
}

func configurationOpenCodeMapKey(data []byte) (string, bool) {
	key, keyFound, valueFound := "", false, false
	for len(data) != 0 {
		number, wireType, tagBytes := protowire.ConsumeTag(data)
		if tagBytes < 0 {
			return "", false
		}
		data = data[tagBytes:]
		consumed := protowire.ConsumeFieldValue(number, wireType, data)
		if consumed < 0 {
			return "", false
		}
		if number == 1 {
			value, size := protowire.ConsumeBytes(data)
			if size < 0 {
				return "", false
			}
			key, keyFound = string(value), true
		}
		valueFound = valueFound || number == 2
		data = data[consumed:]
	}
	return key, keyFound && valueFound
}

func (scan *configurationOpenCodeScan) wireSingular(data []byte, wireType protowire.Type, field protoreflect.FieldDescriptor, depth int) bool {
	if field.FullName() == "skillhub.configuration.v1.OpenCodeValue.literal" {
		return false
	}
	if field.Message() != nil {
		if wireType != protowire.BytesType {
			return false
		}
		value, consumed := protowire.ConsumeBytes(data)
		return consumed >= 0 && scan.wireMessage(value, field.Message(), depth)
	}
	switch field.Kind() {
	case protoreflect.StringKind:
		if wireType != protowire.BytesType {
			return false
		}
		value, consumed := protowire.ConsumeBytes(data)
		return consumed >= 0 && utf8.Valid(value)
	case protoreflect.BoolKind:
		if wireType != protowire.VarintType {
			return false
		}
		value, consumed := protowire.ConsumeVarint(data)
		return consumed >= 0 && value <= 1 && configurationOpenCodeBooleanValid(field, value == 1)
	default:
		return false
	}
}
