package httpapi

import (
	"bytes"
	"encoding/json/jsontext"
	"math"
	"strings"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func selectionTestBytes(number protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), value)
}

func selectionTestVarint(number protowire.Number, value uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(nil, number, protowire.VarintType), value)
}

func selectionTestReject(test *testing.T, codec string, data []byte, destination proto.Message) {
	test.Helper()
	before := proto.Clone(destination)
	err := (importCodec{name: codec}).Unmarshal(data, destination)
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		test.Fatal("strict decode must reject invalid input with InvalidArgument")
	}
	if err.Error() != "invalid_argument: invalid export request" {
		test.Fatal("decode exposed nonconstant error")
	}
	if !proto.Equal(before, destination) {
		test.Fatal("failed decode changed destination")
	}
}

func TestImportSelectionCodecJSONStrict(test *testing.T) {
	cases := map[string]string{
		"unknown":                 `{"source":1,"PRIVATE-CANARY":true}`,
		"duplicate":               `{"source":1,"source":2}`,
		"alias_duplicate":         `{"sourceData":"YQ==","source_data":"Yg=="}`,
		"nested_unknown":          `{"options":[{"candidateId":"PRIVATE-CANARY","secret":true}]}`,
		"binding_unknown":         `{"options":[{"skillBinding":{"repositoryId":"PRIVATE-CANARY","secret":true}}]}`,
		"binding_alias":           `{"options":[{"skillBinding":{"repositoryId":"a","repository_id":"b"}}]}`,
		"duplicate_oneof":         `{"options":[{"claudeAuthMode":1,"geminiAuthMode":1}]}`,
		"duplicate_oneof_default": `{"options":[{"claudeAuthMode":0,"geminiAuthMode":0}]}`,
		"unknown_enum":            `{"source":777}`,
		"unknown_enum_name":       `{"source":"PRIVATE-CANARY"}`,
		"nested_enum":             `{"options":[{"geminiKeyTransport":777}]}`,
		"nested_oneof_enum":       `{"options":[{"geminiAuthMode":777}]}`,
		"root_null":               `null`,
		"source_null":             `{"source":null}`,
		"bytes_null":              `{"sourceData":null}`,
		"list_null":               `{"options":null}`,
		"element_null":            `{"options":[null]}`,
		"optional_null":           `{"options":[{"name":null}]}`,
		"oneof_null":              `{"options":[{"claudeAuthMode":null}]}`,
		"binding_null":            `{"options":[{"skillBinding":null}]}`,
		"binding_field_null":      `{"options":[{"skillBinding":{"branch":null}}]}`,
		"list_wrong_type":         `{"options":{}}`,
		"message_wrong_type":      `{"options":[[]]}`,
		"bytes_wrong_type":        `{"sourceData":1}`,
		"scalar_wrong_type":       `{"options":[{"candidateId":false}]}`,
		"invalid_base64":          `{"sourceData":"PRIVATE-CANARY!"}`,
		"trailing":                `{} {}`,
		"partial":                 `{"source":1,"options":[`,
		"deep_unknown":            `{"options":[{"skillBinding":{"unknown":` + strings.Repeat("[", 30) + `0` + strings.Repeat("]", 30) + `}}]}`,
	}
	for name, data := range cases {
		test.Run(name, func(test *testing.T) {
			selectionTestReject(test, "json", []byte(data), &importv1.PreviewImportSelectionRequest{SourceData: []byte("unchanged"), Options: []*importv1.ImportCandidateOptions{{CandidateId: "before"}}})
		})
	}
	for _, data := range []string{`{"ticket":null}`, `{"repositoryIds":[null]}`, `{"ackNoticeIds":null}`, `{"operationId":"YQ==","operation_id":"Yg=="}`} {
		selectionTestReject(test, "json", []byte(data), &importv1.CommitImportSelectionRequest{Ticket: []byte("unchanged")})
	}
	for _, data := range []string{`{"operationId":null}`, `{"unknown":"PRIVATE-CANARY"}`, `{"operationId":"YQ==","operation_id":"Yg=="}`} {
		selectionTestReject(test, "json", []byte(data), &importv1.GetImportCommitStatusRequest{OperationId: []byte("unchanged")})
	}
}

func TestImportSelectionCodecWireStrict(test *testing.T) {
	for _, fixture := range []struct {
		name       string
		options    protowire.Number
		newMessage func() proto.Message
	}{
		{"preview", 3, func() proto.Message { return &importv1.PreviewImportSelectionRequest{SourceData: []byte("unchanged")} }},
		{"commit", 4, func() proto.Message { return &importv1.CommitImportSelectionRequest{Ticket: []byte("unchanged")} }},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			for name, data := range map[string][]byte{
				"unknown":            selectionTestBytes(100, []byte("PRIVATE-CANARY")),
				"duplicate":          append(selectionTestBytes(2, nil), selectionTestBytes(2, nil)...),
				"nested_unknown":     selectionTestBytes(fixture.options, selectionTestBytes(100, []byte("PRIVATE-CANARY"))),
				"nested_duplicate":   selectionTestBytes(fixture.options, append(selectionTestBytes(1, []byte("a")), selectionTestBytes(1, []byte("b"))...)),
				"oneof":              selectionTestBytes(fixture.options, append(selectionTestVarint(3, 1), selectionTestVarint(4, 1)...)),
				"oneof_zero":         selectionTestBytes(fixture.options, append(selectionTestVarint(3, 0), selectionTestVarint(4, 0)...)),
				"enum_unknown":       selectionTestBytes(fixture.options, selectionTestVarint(5, 777)),
				"enum_overflow":      selectionTestBytes(fixture.options, selectionTestVarint(5, 1<<32|1)),
				"enum_wrong_wire":    selectionTestBytes(fixture.options, selectionTestBytes(5, []byte{1})),
				"string_wrong_wire":  selectionTestBytes(fixture.options, selectionTestVarint(1, 1)),
				"message_wrong_wire": selectionTestVarint(fixture.options, 1),
				"binding_unknown":    selectionTestBytes(fixture.options, selectionTestBytes(6, selectionTestVarint(100, 1))),
				"invalid_utf8":       selectionTestBytes(fixture.options, selectionTestBytes(1, []byte{255})),
				"truncated":          {0x12, 0x7f},
				"tag_zero":           {0},
				"illegal_wire":       {0x0e},
				"group":              {0x0b, 0x0c},
			} {
				test.Run(name, func(test *testing.T) { selectionTestReject(test, "proto", data, fixture.newMessage()) })
			}
		})
	}
	for _, data := range [][]byte{selectionTestVarint(1, 1), selectionTestVarint(100, 1), append(selectionTestBytes(1, []byte("a")), selectionTestBytes(1, []byte("b"))...)} {
		selectionTestReject(test, "proto", data, &importv1.GetImportCommitStatusRequest{OperationId: []byte("before")})
	}
}

func TestImportSelectionCodecPositiveAndReplacement(test *testing.T) {
	name := ""
	transport := configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_UNSPECIFIED
	messages := []proto.Message{
		&importv1.PreviewImportSelectionRequest{Source: importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24, SourceData: []byte("synthetic"), Options: []*importv1.ImportCandidateOptions{{CandidateId: "first", Name: &name, Auth: &importv1.ImportCandidateOptions_GeminiAuthMode{GeminiAuthMode: configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_UNSPECIFIED}, GeminiKeyTransport: &transport}, {CandidateId: "second", SkillBinding: &importv1.SkillBinding{RepositoryId: "repo", ArchiveHash: "archive", SkillId: "skill", LogicalPath: "SKILL.md", ContentHash: "content", Repository: "owner/repo", Branch: "main"}}}},
		&importv1.CommitImportSelectionRequest{Source: importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON, Ticket: bytes.Repeat([]byte{3}, 290), SourceData: []byte("synthetic"), OperationId: bytes.Repeat([]byte{4}, 16), RepositoryIds: []string{"second", "first"}, SkillIds: []string{"skill"}, ConfigurationIds: []string{"config"}, AckNoticeIds: []string{"notice"}},
		&importv1.GetImportCommitStatusRequest{OperationId: bytes.Repeat([]byte{5}, 16)},
	}
	for _, message := range messages {
		for _, codec := range []string{"json", "proto"} {
			test.Run(string(message.ProtoReflect().Descriptor().Name())+"/"+codec, func(test *testing.T) {
				var data []byte
				var err error
				if codec == "json" {
					data, err = protojson.Marshal(message)
				} else {
					data, err = proto.Marshal(message)
				}
				if err != nil {
					test.Fatal("positive fixture marshal failed")
				}
				destination := message.ProtoReflect().New().Interface()
				destination.ProtoReflect().SetUnknown(selectionTestVarint(100, 7))
				if err := (importCodec{name: codec}).Unmarshal(data, destination); err != nil || !proto.Equal(message, destination) {
					test.Fatal("valid decode lost presence/order/binding or did not replace destination")
				}
			})
		}
	}
	for _, data := range []string{`{}`, `{"source":0}`, `{"source_data":"","options":[]}`} {
		if err := (importCodec{name: "json"}).Unmarshal([]byte(data), &importv1.PreviewImportSelectionRequest{}); err != nil {
			test.Fatal("codec must not replace business validation of absent/zero values")
		}
	}
}

func TestImportSelectionCodecByteBounds(test *testing.T) {
	selectionTestReject(test, "json", []byte("{"+strings.Repeat(" ", 24<<20)+"}"), &importv1.PreviewImportSelectionRequest{SourceData: []byte("before")})
	selectionTestReject(test, "json", []byte("{"+strings.Repeat(" ", 64<<10)+"}"), &importv1.GetImportCommitStatusRequest{OperationId: []byte("before")})
	selectionTestReject(test, "proto", selectionTestBytes(1, make([]byte, 64<<10)), &importv1.GetImportCommitStatusRequest{OperationId: []byte("before")})
}

func TestImportSelectionCodecLegacyCompatibility(test *testing.T) {
	legacy := &importv1.PreviewLibraryExportRequest{}
	if err := (importCodec{name: "json"}).Unmarshal([]byte(`{"format":null,"exportData":null,"foreign":{"PRIVATE-CANARY":true}}`), legacy); err != nil {
		test.Fatal("legacy JSON unknown/null changed")
	}
	wire := selectionTestVarint(100, 1)
	if err := (importCodec{name: "proto"}).Unmarshal(wire, legacy); err != nil || !bytes.Equal(legacy.ProtoReflect().GetUnknown(), wire) {
		test.Fatal("legacy binary unknown retention changed")
	}
}

func TestImportSelectionCodecExactGoDispatch(test *testing.T) {
	descriptor := (&importv1.PreviewImportSelectionRequest{}).ProtoReflect().Descriptor()
	dynamic := dynamicpb.NewMessage(descriptor)
	if err := (importCodec{name: "json"}).Unmarshal([]byte(`{"source":777,"sourceData":null,"unknown":true}`), dynamic); err != nil || dynamic.Get(descriptor.Fields().ByNumber(1)).Enum() != 777 {
		test.Fatal("strict dispatch must not match descriptor or message name")
	}
	response := &importv1.PreviewImportSelectionResponse{}
	if err := (importCodec{name: "json"}).Unmarshal([]byte(`{"ticket":null,"unknown":true}`), response); err != nil {
		test.Fatal("request prerequisite changed response codec")
	}
	for _, message := range []proto.Message{(*importv1.PreviewImportSelectionRequest)(nil), (*importv1.CommitImportSelectionRequest)(nil), (*importv1.GetImportCommitStatusRequest)(nil)} {
		if err := (importCodec{name: "proto"}).Unmarshal(nil, message); err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			test.Fatal("typed nil request was not safely rejected")
		}
	}
}

func TestImportSelectionCodecTransactionalLateFailure(test *testing.T) {
	selectionTestReject(test, "json", []byte(`{"source":1,"ticket":"PRIVATE-CANARY!"}`), &importv1.CommitImportSelectionRequest{Ticket: []byte("before"), SourceData: []byte("before-source")})
	selectionTestReject(test, "json", []byte(`{"operationId":"PRIVATE-CANARY!"}`), &importv1.GetImportCommitStatusRequest{OperationId: []byte("before")})
	selectionTestReject(test, "json", []byte(`{"sourceData":"YQ==","options":[{"candidateId":false}]}`), &importv1.PreviewImportSelectionRequest{SourceData: []byte("before")})
	for _, data := range [][]byte{selectionTestVarint(1, 777), selectionTestVarint(1, 1<<32|1), selectionTestBytes(1, []byte{1})} {
		selectionTestReject(test, "proto", data, &importv1.PreviewImportSelectionRequest{SourceData: []byte("before")})
	}
	for _, data := range [][]byte{selectionTestVarint(2, 777), selectionTestVarint(2, 1<<32|1), selectionTestBytes(2, []byte{1})} {
		selectionTestReject(test, "proto", data, &importv1.CommitImportSelectionRequest{Ticket: []byte("before")})
	}
}

func FuzzImportSelectionCodecTransactional(fuzzer *testing.F) {
	fuzzer.Add([]byte(`{"options":[{"name":null}]}`), uint8(0))
	fuzzer.Add([]byte(`{"ticket":"PRIVATE-CANARY!"}`), uint8(1))
	fuzzer.Add([]byte(`{"operationId":null}`), uint8(2))
	fuzzer.Add(selectionTestBytes(3, selectionTestVarint(3, 1)), uint8(3))
	fuzzer.Add(selectionTestBytes(1, []byte{1, 2}), uint8(4))
	fuzzer.Add([]byte{0xff}, uint8(5))
	fuzzer.Fuzz(func(test *testing.T, data []byte, variant uint8) {
		var destination proto.Message
		switch variant % 3 {
		case 0:
			destination = &importv1.PreviewImportSelectionRequest{SourceData: []byte("before")}
		case 1:
			destination = &importv1.CommitImportSelectionRequest{Ticket: []byte("before")}
		default:
			destination = &importv1.GetImportCommitStatusRequest{OperationId: []byte("before")}
		}
		before := proto.Clone(destination)
		encoding := "json"
		if variant%6 >= 3 {
			encoding = "proto"
		}
		err := (importCodec{name: encoding}).Unmarshal(data, destination)
		if err != nil && (err.Error() != "invalid_argument: invalid export request" || !proto.Equal(before, destination)) {
			test.Fatal("rejected decode was not safe and transactional")
		}
	})
}

func TestImportSelectionCodecMixedOptionShape(test *testing.T) {
	for _, option := range []string{`{"skillBinding":{},"name":""}`, `{"skillBinding":{},"geminiAuthMode":0}`, `{"skillBinding":{},"claudeAuthMode":0}`, `{"skillBinding":{},"geminiKeyTransport":0}`, `{"claudeAuthMode":0,"geminiKeyTransport":0}`} {
		selectionTestReject(test, "json", []byte(`{"options":[`+option+`]}`), &importv1.PreviewImportSelectionRequest{SourceData: []byte("before")})
		selectionTestReject(test, "json", []byte(`{"options":[`+option+`]}`), &importv1.CommitImportSelectionRequest{Ticket: []byte("before")})
	}
	for _, option := range [][]byte{
		append(selectionTestBytes(6, nil), selectionTestBytes(2, nil)...),
		append(selectionTestBytes(6, nil), selectionTestVarint(4, 0)...),
		append(selectionTestBytes(6, nil), selectionTestVarint(3, 0)...),
		append(selectionTestBytes(6, nil), selectionTestVarint(5, 0)...),
		append(selectionTestVarint(3, 0), selectionTestVarint(5, 0)...),
	} {
		selectionTestReject(test, "proto", selectionTestBytes(3, option), &importv1.PreviewImportSelectionRequest{SourceData: []byte("before")})
		selectionTestReject(test, "proto", selectionTestBytes(4, option), &importv1.CommitImportSelectionRequest{Ticket: []byte("before")})
	}
}

func selectionTestDescriptor(test *testing.T, kind protoreflect.Kind, repeated bool) protoreflect.MessageDescriptor {
	test.Helper()
	label := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	if repeated {
		label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	}
	field := &descriptorpb.FieldDescriptorProto{Name: new("value"), Number: new(int32(1)), Type: new(descriptorpb.FieldDescriptorProto_Type(kind)), Label: &label}
	if kind == protoreflect.EnumKind {
		field.TypeName = new(".codecprobe.Choice")
	}
	if kind == protoreflect.MessageKind {
		field.TypeName = new(".codecprobe.Node")
	}
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("codec-probe.proto"), Package: new("codecprobe"), Syntax: new("proto3"),
		EnumType:    []*descriptorpb.EnumDescriptorProto{{Name: new("Choice"), Value: []*descriptorpb.EnumValueDescriptorProto{{Name: new("ZERO"), Number: new(int32(0))}, {Name: new("ONE"), Number: new(int32(1))}, {Name: new("NEGATIVE"), Number: new(int32(-1))}}}},
		MessageType: []*descriptorpb.DescriptorProto{{Name: new("Node"), Field: []*descriptorpb.FieldDescriptorProto{field}}},
	}, nil)
	if err != nil {
		test.Fatal("scalar test descriptor invalid")
	}
	return file.Messages().ByName("Node")
}

func selectionTestWireValidation(data []byte, descriptor protoreflect.MessageDescriptor) bool {
	remaining := importSelectionCodecValues
	return importSelectionWireValid(data, descriptor, 0, &remaining)
}

func TestImportSelectionCodecAllScalarWireKinds(test *testing.T) {
	for _, fixture := range []struct {
		kind    protoreflect.Kind
		wire    protowire.Type
		payload []byte
	}{
		{protoreflect.BoolKind, protowire.VarintType, []byte{1}},
		{protoreflect.EnumKind, protowire.VarintType, []byte{1}},
		{protoreflect.Int32Kind, protowire.VarintType, protowire.AppendVarint(nil, math.MaxUint64)},
		{protoreflect.Int64Kind, protowire.VarintType, protowire.AppendVarint(nil, math.MaxUint64)},
		{protoreflect.Uint32Kind, protowire.VarintType, protowire.AppendVarint(nil, math.MaxUint32)},
		{protoreflect.Uint64Kind, protowire.VarintType, protowire.AppendVarint(nil, math.MaxUint64)},
		{protoreflect.Sint32Kind, protowire.VarintType, protowire.AppendVarint(nil, math.MaxUint32)},
		{protoreflect.Sint64Kind, protowire.VarintType, protowire.AppendVarint(nil, math.MaxUint64)},
		{protoreflect.Fixed32Kind, protowire.Fixed32Type, protowire.AppendFixed32(nil, math.MaxUint32)},
		{protoreflect.Sfixed32Kind, protowire.Fixed32Type, protowire.AppendFixed32(nil, math.MaxUint32)},
		{protoreflect.FloatKind, protowire.Fixed32Type, protowire.AppendFixed32(nil, math.Float32bits(1.25))},
		{protoreflect.Fixed64Kind, protowire.Fixed64Type, protowire.AppendFixed64(nil, math.MaxUint64)},
		{protoreflect.Sfixed64Kind, protowire.Fixed64Type, protowire.AppendFixed64(nil, math.MaxUint64)},
		{protoreflect.DoubleKind, protowire.Fixed64Type, protowire.AppendFixed64(nil, math.Float64bits(1.25))},
		{protoreflect.StringKind, protowire.BytesType, protowire.AppendBytes(nil, []byte("synthetic"))},
		{protoreflect.BytesKind, protowire.BytesType, protowire.AppendBytes(nil, []byte{255})},
	} {
		test.Run(fixture.kind.String(), func(test *testing.T) {
			for _, repeated := range []bool{false, true} {
				descriptor := selectionTestDescriptor(test, fixture.kind, repeated)
				wire := append(protowire.AppendTag(nil, 1, fixture.wire), fixture.payload...)
				if !selectionTestWireValidation(wire, descriptor) {
					test.Fatal("legal scalar wire rejected")
				}
				if proto.Unmarshal(wire, dynamicpb.NewMessage(descriptor)) != nil {
					test.Fatal("legal scalar oracle disagrees with protobuf")
				}
				wrongType := protowire.VarintType
				wrongPayload := []byte{1}
				if fixture.wire == protowire.VarintType {
					wrongType = protowire.Fixed32Type
					wrongPayload = []byte{1, 0, 0, 0}
				}
				if selectionTestWireValidation(append(protowire.AppendTag(nil, 1, wrongType), wrongPayload...), descriptor) {
					test.Fatal("scalar wire mismatch accepted")
				}
				if selectionTestWireValidation(wire[:len(wire)-1], descriptor) {
					test.Fatal("truncated scalar accepted")
				}
				if repeated {
					if !selectionTestWireValidation(append(bytes.Clone(wire), wire...), descriptor) {
						test.Fatal("legal repeated occurrences rejected")
					}
					if fixture.wire != protowire.BytesType {
						packed := selectionTestBytes(1, append(bytes.Clone(fixture.payload), fixture.payload...))
						if !selectionTestWireValidation(packed, descriptor) {
							test.Fatal("packed scalar values rejected")
						}
					}
				} else if selectionTestWireValidation(append(bytes.Clone(wire), wire...), descriptor) {
					test.Fatal("duplicate singular accepted")
				}
			}
		})
	}
	for _, fixture := range []struct {
		kind  protoreflect.Kind
		value uint64
	}{
		{protoreflect.BoolKind, 2}, {protoreflect.Uint32Kind, 1 << 32}, {protoreflect.Sint32Kind, 1 << 32},
		{protoreflect.Int32Kind, 1 << 31}, {protoreflect.Int32Kind, 0xffffffff}, {protoreflect.Int32Kind, math.MaxUint64 - math.MaxInt32 - 1},
		{protoreflect.EnumKind, 777}, {protoreflect.EnumKind, 1<<32 | 1},
	} {
		for _, repeated := range []bool{false, true} {
			descriptor := selectionTestDescriptor(test, fixture.kind, repeated)
			if selectionTestWireValidation(selectionTestVarint(1, fixture.value), descriptor) {
				test.Fatal("scalar range truncation/unknown enum accepted")
			}
			if repeated && selectionTestWireValidation(selectionTestBytes(1, protowire.AppendVarint(nil, fixture.value)), descriptor) {
				test.Fatal("packed scalar range truncation/unknown enum accepted")
			}
		}
	}
	for _, kind := range []protoreflect.Kind{protoreflect.Int32Kind, protoreflect.EnumKind} {
		if !selectionTestWireValidation(selectionTestVarint(1, math.MaxUint64), selectionTestDescriptor(test, kind, false)) {
			test.Fatal("legal negative int32/known enum rejected")
		}
	}
	for _, fixture := range []struct {
		kind protoreflect.Kind
		data []byte
	}{
		{protoreflect.FloatKind, selectionTestBytes(1, []byte{1, 2, 3})},
		{protoreflect.DoubleKind, selectionTestBytes(1, []byte{1, 2, 3, 4, 5, 6, 7})},
		{protoreflect.BoolKind, selectionTestBytes(1, []byte{0x80})},
	} {
		if selectionTestWireValidation(fixture.data, selectionTestDescriptor(test, fixture.kind, true)) {
			test.Fatal("truncated packed scalar accepted")
		}
	}
}

func TestImportSelectionCodecDepthWorkAndBoundary(test *testing.T) {
	descriptor := selectionTestDescriptor(test, protoreflect.MessageKind, false)
	wire := []byte{}
	json := `{}`
	for range importSelectionCodecDepth - 1 {
		wire = selectionTestBytes(1, wire)
		json = `{"value":` + json + `}`
	}
	remaining := importSelectionCodecValues
	if !selectionTestWireValidation(wire, descriptor) || !importSelectionJSONMessage(jsontext.NewDecoder(strings.NewReader(json)), descriptor, 0, &remaining) {
		test.Fatal("legal depth rejected")
	}
	wire = selectionTestBytes(1, wire)
	json = `{"value":` + json + `}`
	remaining = importSelectionCodecValues
	if selectionTestWireValidation(wire, descriptor) || importSelectionJSONMessage(jsontext.NewDecoder(strings.NewReader(json)), descriptor, 0, &remaining) {
		test.Fatal("depth limit bypassed")
	}
	selectionTestReject(test, "json", []byte(`{"options":[`+strings.Repeat(`{},`, importSelectionCodecValues)+`{}]}`), &importv1.PreviewImportSelectionRequest{SourceData: []byte("before")})
	selectionTestReject(test, "proto", bytes.Repeat(selectionTestBytes(3, nil), importSelectionCodecValues+1), &importv1.PreviewImportSelectionRequest{SourceData: []byte("before")})
	for _, codec := range []string{"json", "proto"} {
		for _, message := range []proto.Message{&importv1.PreviewImportSelectionRequest{}, &importv1.CommitImportSelectionRequest{}} {
			var oversized []byte
			if codec == "json" {
				oversized = []byte("{" + strings.Repeat(" ", 24<<20) + "}")
			} else {
				oversized = selectionTestBytes(3, make([]byte, 24<<20))
			}
			selectionTestReject(test, codec, oversized, message)
		}
	}
	status := &importv1.GetImportCommitStatusRequest{}
	if err := (importCodec{name: "json"}).Unmarshal([]byte("{"+strings.Repeat(" ", (64<<10)-2)+"}"), status); err != nil {
		test.Fatal("exact byte-boundary rejected")
	}
}
