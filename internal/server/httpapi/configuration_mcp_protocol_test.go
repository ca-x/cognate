package httpapi

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/descriptorpb"
	"net/http"
	"strings"
	"testing"

	mcpv1 "github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1/mcpv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func TestMCPConfigurationProtocolDescriptor(test *testing.T) {
	file := mcpv1.File_skillhub_configuration_mcp_v1_mcp_proto
	expected := map[string][]string{
		"Document":            {"servers#1#skillhub.configuration.mcp.v1.Server#repeated##false"},
		"Server":              {"id#1#string#optional##false", "name#2#string#optional##false", "enabled#3#bool#optional##true", "stdio#4#skillhub.configuration.mcp.v1.Stdio#optional#connection#false", "http#5#skillhub.configuration.mcp.v1.Remote#optional#connection#false", "sse#6#skillhub.configuration.mcp.v1.Remote#optional#connection#false"},
		"Stdio":               {"command#1#string#optional##false", "args#2#skillhub.configuration.mcp.v1.Args#optional##false", "env#3#skillhub.configuration.mcp.v1.NamedSlots#optional##false"},
		"Args":                {"values#1#skillhub.configuration.mcp.v1.Arg#repeated##false"},
		"Arg":                 {"id#1#string#optional##false", "slot_ref#2#string#optional##false"},
		"NamedSlots":          {"values#1#skillhub.configuration.mcp.v1.NamedSlot#repeated##false"},
		"NamedSlot":           {"name#1#string#optional##false", "slot_ref#2#string#optional##false"},
		"Remote":              {"url#1#string#optional##false", "headers#2#skillhub.configuration.mcp.v1.NamedSlots#optional##false"},
		"Draft":               {"name#1#string#optional##false", "agent_id#2#string#optional##false", "document#3#skillhub.configuration.mcp.v1.Document#optional##false"},
		"SlotStatus":          {"slot_id#1#string#optional##false", "present#2#bool#optional##true"},
		"Keep":                {},
		"Clear":               {"confirmed#1#bool#optional##true"},
		"SecretMutation":      {"slot_id#1#string#optional##false", "keep#2#skillhub.configuration.mcp.v1.Keep#optional#action#false", "replace#3#bytes#optional#action#false", "clear#4#skillhub.configuration.mcp.v1.Clear#optional#action#false"},
		"View":                {"asset#1#skillhub.configuration.v1.Asset#optional##false", "draft#2#skillhub.configuration.mcp.v1.Draft#optional##false", "secrets#3#skillhub.configuration.mcp.v1.SlotStatus#repeated##false", "readiness#4#skillhub.configuration.mcp.v1.Readiness#optional##false", "has_preserved_extras#5#bool#optional##false"},
		"Operation":           {"server_id#1#string#optional##false", "native_name#2#string#optional##false", "action#3#skillhub.configuration.mcp.v1.Action#optional##false", "desired#4#skillhub.configuration.mcp.v1.Server#optional##false", "requires_managed_proof#5#bool#optional##false"},
		"ListAssetsRequest":   {},
		"ListAssetsResponse":  {"assets#1#skillhub.configuration.v1.Asset#repeated##false"},
		"GetAssetRequest":     {"id#1#string#optional##false"},
		"GetAssetResponse":    {"asset#1#skillhub.configuration.v1.Asset#optional##false"},
		"ReadDraftRequest":    {"id#1#string#optional##false"},
		"ReadDraftResponse":   {"view#1#skillhub.configuration.mcp.v1.View#optional##false"},
		"CreateAssetRequest":  {"draft#1#skillhub.configuration.mcp.v1.Draft#optional##false", "secrets#2#skillhub.configuration.mcp.v1.SecretMutation#repeated##false"},
		"CreateAssetResponse": {"view#1#skillhub.configuration.mcp.v1.View#optional##false"},
		"UpdateAssetRequest":  {"id#1#string#optional##false", "expected_revision#2#int64#optional##false", "draft#3#skillhub.configuration.mcp.v1.Draft#optional##false", "secrets#4#skillhub.configuration.mcp.v1.SecretMutation#repeated##false"},
		"UpdateAssetResponse": {"view#1#skillhub.configuration.mcp.v1.View#optional##false"},
		"CopyAssetRequest":    {"id#1#string#optional##false", "expected_source_revision#2#int64#optional##false", "name#3#string#optional##false"},
		"CopyAssetResponse":   {"view#1#skillhub.configuration.mcp.v1.View#optional##false"},
		"DeleteAssetRequest":  {"id#1#string#optional##false", "expected_revision#2#int64#optional##false"},
		"DeleteAssetResponse": {},
		"PreviewRequest":      {"id#1#string#optional##false", "expected_revision#2#int64#optional##false", "draft#3#skillhub.configuration.mcp.v1.Draft#optional##false", "secrets#4#skillhub.configuration.mcp.v1.SecretMutation#repeated##false"},
		"PreviewResponse":     {"operations#1#skillhub.configuration.mcp.v1.Operation#repeated##false", "can_apply#2#bool#optional##false", "applied#3#bool#optional##false", "warnings#4#skillhub.configuration.mcp.v1.Warning#repeated##false", "readiness#5#skillhub.configuration.mcp.v1.Readiness#optional##false"},
	}
	if file.Messages().Len() != 31 || file.Enums().Len() != 3 || file.Services().Len() != 1 {
		test.Fatal("MCP schema closure changed")
	}
	for name, fields := range expected {
		message := file.Messages().ByName(protoreflect.Name(name))
		if message == nil || message.Fields().Len() != len(fields) {
			test.Fatalf("message %s field count", name)
		}
		for index, signature := range fields {
			field := message.Fields().Get(index)
			kind := field.Kind().String()
			if field.Message() != nil {
				kind = string(field.Message().FullName())
			}
			if field.Enum() != nil {
				kind = string(field.Enum().FullName())
			}
			group := ""
			if oneof := field.ContainingOneof(); oneof != nil && !oneof.IsSynthetic() {
				group = string(oneof.Name())
			}
			actual := fmt.Sprintf("%s#%d#%s#%s#%s#%t", field.Name(), field.Number(), kind, field.Cardinality(), group, field.HasOptionalKeyword())
			if actual != signature {
				test.Fatalf("%s: expected %s got %s", name, signature, actual)
			}
		}
	}
	methods := []string{"ListAssets", "GetAsset", "ReadDraft", "CreateAsset", "UpdateAsset", "CopyAsset", "DeleteAsset", "Preview"}
	service := file.Services().Get(0)
	if service.FullName() != "skillhub.configuration.mcp.v1.MCPConfigurationService" || service.Methods().Len() != 8 {
		test.Fatal("service closure")
	}
	for index, name := range methods {
		method := service.Methods().Get(index)
		if string(method.Name()) != name || string(method.Input().Name()) != name+"Request" || string(method.Output().Name()) != name+"Response" || method.IsStreamingClient() || method.IsStreamingServer() {
			test.Fatal("method contract", name)
		}
	}
	for index, values := range [][]string{{"READINESS_UNSPECIFIED", "READINESS_COMPLETE", "READINESS_NEEDS_CREDENTIAL"}, {"ACTION_UNSPECIFIED", "ACTION_UPSERT_MANAGED", "ACTION_DISABLE_MANAGED"}, {"WARNING_UNSPECIFIED", "WARNING_INSECURE_HTTP", "WARNING_NATIVE_NOT_QUALIFIED"}} {
		enumeration := file.Enums().Get(index)
		if enumeration.Values().Len() != len(values) {
			test.Fatal("enum cardinality")
		}
		for number, name := range values {
			value := enumeration.Values().Get(number)
			if string(value.Name()) != name || int(value.Number()) != number {
				test.Fatal("enum contract")
			}
		}
	}
	if configurationv1.File_skillhub_configuration_v1_configuration_proto.Services().Get(0).Methods().Len() != 9 {
		test.Fatal("delivered R1 descriptor changed")
	}
	mutation := file.Messages().ByName("SecretMutation").Fields().ByName("replace")
	if !mutation.Options().(*descriptorpb.FieldOptions).GetDebugRedact() {
		test.Fatal("replacement debug redaction missing")
	}
	warnings := file.Messages().ByName("PreviewResponse").Fields().ByName("warnings")
	if !warnings.IsList() || !warnings.IsPacked() {
		test.Fatal("approved warnings cardinality/packing")
	}
	protoregistry.GlobalFiles.RangeFiles(func(known protoreflect.FileDescriptor) bool {
		if strings.HasPrefix(known.Path(), "skillhub/") {
			encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(protodesc.ToFileDescriptorProto(known))
			if err != nil {
				test.Fatal(err)
			}
			test.Logf("DESCRIPTOR %s %x", known.Path(), sha256.Sum256(encoded))
		}
		return true
	})
}

func mcpWireBytes(number protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), value)
}

func TestMCPConfigurationScalarScanner(test *testing.T) {
	for _, size := range []int{4096, 4097} {
		message := &mcpv1.GetAssetRequest{Id: strings.Repeat("s", size)}
		data, err := protojson.Marshal(message)
		if err != nil {
			test.Fatal(err)
		}
		if mcpConfigurationJSONValid(data, message.ProtoReflect().Descriptor(), 0) != (size <= 4096) {
			test.Errorf("JSON scalar bound before generated decode: size=%d", size)
		}
	}
	for _, size := range []int{0, 8192, 8193} {
		message := &mcpv1.CreateAssetRequest{Secrets: []*mcpv1.SecretMutation{{SlotId: "arg/s/a", Action: &mcpv1.SecretMutation_Replace{Replace: bytes.Repeat([]byte{'x'}, size)}}}}
		data, err := protojson.Marshal(message)
		if err != nil {
			test.Fatal(err)
		}
		if mcpConfigurationJSONValid(data, message.ProtoReflect().Descriptor(), 0) != (size <= 8192) {
			test.Errorf("JSON bytes bound before generated decode: size=%d", size)
		}
	}
}

func TestMCPConfigurationStrictProtocol(test *testing.T) {
	fixture := newMCPConfigurationFixture(test)
	good := &mcpv1.CreateAssetRequest{Draft: mcpConfigurationDraft()}
	text, err := protojson.Marshal(good)
	if err != nil {
		test.Fatal(err)
	}
	binary, err := proto.Marshal(good)
	if err != nil {
		test.Fatal(err)
	}
	server, err := proto.Marshal(&mcpv1.Server{Id: "s", Name: "s", Enabled: new(false), Connection: &mcpv1.Server_Stdio{Stdio: &mcpv1.Stdio{Command: "cmd"}}})
	if err != nil {
		test.Fatal(err)
	}
	remote, _ := proto.Marshal(&mcpv1.Remote{Url: "https://example.test"})
	mixed := append(bytes.Clone(server), mcpWireBytes(5, remote)...)
	boolBad := bytes.Replace(server, []byte{24, 0}, []byte{24, 2}, 1)
	wrap := func(value []byte) []byte {
		draft := append(mcpWireBytes(1, []byte("MCP")), mcpWireBytes(2, []byte("claude"))...)
		draft = append(draft, mcpWireBytes(3, mcpWireBytes(1, value))...)
		return mcpWireBytes(1, draft)
	}
	cases := []struct {
		name, encoding string
		body           []byte
	}{
		{"unknown-json", "json", []byte(`{"draft":null,"leak":"synthetic-mcp-header-secret"}`)},
		{"null-required", "json", []byte(`{"draft":null}`)},
		{"null-array", "json", []byte(`{"draft":{"name":"x","agentId":"claude","document":{"servers":null}}}`)},
		{"null-item", "json", []byte(`{"draft":{"name":"x","agentId":"claude","document":{"servers":[null]}}}`)},
		{"mixed-json", "json", []byte(`{"draft":{"name":"x","agentId":"claude","document":{"servers":[{"id":"s","name":"s","enabled":false,"stdio":{"command":"cmd"},"http":{"url":"https://example.test"}}]}}}`)},
		{"missing-enabled", "json", []byte(`{"draft":{"name":"x","agentId":"claude","document":{"servers":[{"id":"s","name":"s","stdio":{"command":"cmd"}}]}}}`)},
		{"alias-duplicate", "json", bytes.Replace(text, []byte(`"agentId"`), []byte(`"agent_id":"claude","agentId"`), 1)},
		{"duplicate-draft", "json", append(append([]byte(`{"draft":{},`), text[1:len(text)-1]...), '}')},
		{"null-mutation", "json", []byte(`{"draft":{"name":"x","agentId":"claude","document":{}},"secrets":[null]}`)},
		{"false-clear", "json", []byte(`{"draft":{"name":"x","agentId":"claude","document":{}},"secrets":[{"slotId":"arg/s/a","clear":{"confirmed":false}}]}`)},
		{"missing-clear", "json", []byte(`{"draft":{"name":"x","agentId":"claude","document":{}},"secrets":[{"slotId":"arg/s/a","clear":{}}]}`)},
		{"mixed-action", "json", []byte(`{"draft":{"name":"x","agentId":"claude","document":{}},"secrets":[{"slotId":"arg/s/a","keep":{},"replace":""}]}`)},
		{"trailing", "json", append(bytes.Clone(text), []byte(` {}`)...)},
		{"duplicate-wire", "proto", append(bytes.Clone(binary), binary...)},
		{"unknown-wire", "proto", protowire.AppendVarint(protowire.AppendTag(bytes.Clone(binary), 99, protowire.VarintType), 1)},
		{"wrong-wire", "proto", []byte{8, 1}},
		{"group-wire", "proto", []byte{11, 12}},
		{"mixed-wire", "proto", wrap(mixed)},
		{"bool-two", "proto", wrap(boolBad)},
		{"malformed-length", "proto", []byte{10, 255}},
	}
	for _, entry := range cases {
		test.Run(entry.name, func(test *testing.T) {
			status, body := configurationRaw(test, fixture.server, fixture.token, mcpv1connect.MCPConfigurationServiceCreateAssetProcedure, "application/"+entry.encoding, entry.body)
			if status != http.StatusBadRequest || len(body) > 4096 || strings.Contains(body, "synthetic-mcp-header-secret") || strings.Contains(body, fixture.token) {
				test.Fatalf("invalid request not safely rejected: %s HTTP%d", entry.name, status)
			}
		})
	}
	assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
	if err != nil || len(assets) != 0 {
		test.Fatal("invalid protocol mutated Store", err)
	}
	for _, wire := range [][]byte{{34, 1, 2}, {32, 2}} {
		var decoded mcpv1.PreviewResponse
		if err := proto.Unmarshal(wire, &decoded); err != nil || len(decoded.Warnings) != 1 || decoded.Warnings[0] != mcpv1.Warning_WARNING_NATIVE_NOT_QUALIFIED {
			test.Fatal("packed/unpacked warning compatibility", err)
		}
	}
}

func TestMCPConfigurationProtocolPresence(test *testing.T) {
	for _, transport := range []string{"stdio", "http", "sse"} {
		server := &mcpv1.Server{Id: "s", Name: "s", Enabled: new(false)}
		switch transport {
		case "stdio":
			server.Connection = &mcpv1.Server_Stdio{Stdio: &mcpv1.Stdio{Command: "cmd", Args: &mcpv1.Args{}, Env: &mcpv1.NamedSlots{}}}
		case "http":
			server.Connection = &mcpv1.Server_Http{Http: &mcpv1.Remote{Url: "https://example.test", Headers: &mcpv1.NamedSlots{}}}
		case "sse":
			server.Connection = &mcpv1.Server_Sse{Sse: &mcpv1.Remote{Url: "https://example.test", Headers: &mcpv1.NamedSlots{}}}
		}
		for _, encoding := range []string{"json", "proto"} {
			var data []byte
			var err error
			if encoding == "json" {
				data, err = protojson.Marshal(server)
			} else {
				data, err = proto.Marshal(server)
			}
			if err != nil {
				test.Fatal(err)
			}
			decoded := new(mcpv1.Server)
			if encoding == "json" {
				err = protojson.Unmarshal(data, decoded)
			} else {
				err = proto.Unmarshal(data, decoded)
			}
			if err != nil || !proto.Equal(server, decoded) || decoded.Enabled == nil || *decoded.Enabled {
				test.Fatal("false/oneof/wrapper presence", transport, encoding)
			}
		}
	}
	mutation := &mcpv1.SecretMutation{SlotId: "arg/s/a", Action: &mcpv1.SecretMutation_Replace{Replace: []byte{}}}
	for _, encoding := range []string{"json", "proto"} {
		var data []byte
		var err error
		if encoding == "json" {
			data, err = protojson.Marshal(mutation)
		} else {
			data, err = proto.Marshal(mutation)
		}
		if err != nil {
			test.Fatal(err)
		}
		decoded := new(mcpv1.SecretMutation)
		if encoding == "json" {
			err = protojson.Unmarshal(data, decoded)
		} else {
			err = proto.Unmarshal(data, decoded)
		}
		if _, ok := decoded.Action.(*mcpv1.SecretMutation_Replace); err != nil || !ok {
			test.Fatal("present-empty replacement lost")
		}
	}
}
