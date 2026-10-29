package httpapi

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func codecBegin() *nativev1.BeginNativeConsumerRequest {
	return &nativev1.BeginNativeConsumerRequest{Purpose: 1, OperationId: bytes.Repeat([]byte{1}, 16), ConsumerPublicKey: bytes.Repeat([]byte{2}, 32), ClientNonce: bytes.Repeat([]byte{3}, 32), ExpectedOwnerId: "synthetic-owner", ExpectedServerOrigin: "https://cognate.example"}
}

func TestNativeProductionPathsDoNotLogWireRequests(test *testing.T) {
	for _, name := range []string{"native_configuration.go", "native_configuration_codec.go", "../nativeconsumer/service.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			test.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, data, 0)
		if err != nil {
			test.Fatal(err)
		}
		for _, imported := range file.Imports {
			path, _ := strconv.Unquote(imported.Path.Value)
			if path == "fmt" || path == "log" || path == "log/slog" || path == "net/http/httputil" {
				test.Fatal("native production path added a raw formatting/logging surface")
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if function, ok := call.Fun.(*ast.Ident); ok && (function.Name == "print" || function.Name == "println") {
					test.Error("native production path prints wire data")
				}
			}
			return true
		})
	}
}

func TestNativeErrorsDropUntrustedConnectMessagesAndDetails(test *testing.T) {
	const sentinel = "synthetic-untrusted-native-error-secret"
	for _, code := range []connect.Code{connect.CodeInvalidArgument, connect.CodeUnauthenticated, connect.CodePermissionDenied, connect.CodeFailedPrecondition, connect.CodeAborted, connect.CodeResourceExhausted, connect.CodeUnavailable} {
		failure := nativeError(test.Context(), connect.NewError(code, errors.New(sentinel)))
		if failure.Code() != code || strings.Contains(failure.Message(), sentinel) || len(failure.Details()) != 0 {
			test.Fatal("native transport forwarded an untrusted Connect error payload")
		}
	}
}

func TestNativeCodecStrictWireAndTransactionalDecode(test *testing.T) {
	valid, _ := proto.Marshal(codecBegin())
	field := func(number protowire.Number, value []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), value)
	}
	for name, data := range map[string][]byte{
		"unknown":      append(bytes.Clone(valid), field(99, []byte("synthetic-wire-sentinel"))...),
		"duplicate":    append(bytes.Clone(valid), field(5, []byte("other"))...),
		"wrong-wire":   append(bytes.Clone(valid), protowire.AppendVarint(protowire.AppendTag(nil, 5, protowire.VarintType), 1)...),
		"overlong-tag": append([]byte{0x88, 0}, valid[1:]...),
		"zero-enum":    append([]byte{8, 0}, valid[2:]...),
		"unknown-enum": append([]byte{8, 127}, valid[2:]...),
		"oversize":     bytes.Repeat([]byte{0}, (512<<10)+1),
	} {
		test.Run(name, func(test *testing.T) {
			destination := codecBegin()
			before := proto.Clone(destination)
			if err := (nativeConfigurationCodec{}).Unmarshal(data, destination); err == nil || !proto.Equal(destination, before) {
				test.Fatal("strict decode accepted ambiguous bytes or changed destination on error")
			}
		})
	}
	var destination nativev1.BeginNativeConsumerRequest
	if err := (nativeConfigurationCodec{}).Unmarshal(valid, &destination); err != nil || !proto.Equal(codecBegin(), &destination) {
		test.Fatal("canonical protobuf request rejected")
	}
}

func TestNativeCodecRejectsNestedUnknownAndDuplicateConsent(test *testing.T) {
	request := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: "prepare", OperationId: bytes.Repeat([]byte{1}, 16), IntentCommitment: bytes.Repeat([]byte{2}, 32), RequestedSlots: []nativev1.SecretSlot{1}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: bytes.Repeat([]byte{3}, 32), ProofSignature: bytes.Repeat([]byte{4}, 64), IntentExpiresAtUnixMs: 1000}
	valid, _ := proto.Marshal(request)
	for _, field := range [][]byte{
		protowire.AppendBytes(protowire.AppendTag(nil, 5, protowire.BytesType), []byte{8, 1}),
		protowire.AppendBytes(protowire.AppendTag(nil, 6, protowire.BytesType), []byte{10, 1, 'x', 16, 1}),
	} {
		var destination nativev1.AuthorizeNativeMaterializationRequest
		if err := (nativeConfigurationCodec{}).Unmarshal(append(bytes.Clone(valid), field...), &destination); err == nil {
			test.Fatal("duplicate singular or oneof accepted")
		}
	}
	request.GetExplicitConfirmation().ProtoReflect().SetUnknown([]byte{16, 1})
	nested, _ := proto.Marshal(request)
	var destination nativev1.AuthorizeNativeMaterializationRequest
	if err := (nativeConfigurationCodec{}).Unmarshal(nested, &destination); err == nil {
		test.Fatal("nested unknown accepted")
	}
	request.GetExplicitConfirmation().ProtoReflect().SetUnknown(nil)
	request.RequestedSlots = []nativev1.SecretSlot{1, 1}
	duplicateSlot, _ := proto.Marshal(request)
	if err := (nativeConfigurationCodec{}).Unmarshal(duplicateSlot, &destination); err == nil {
		test.Fatal("duplicate packed slots accepted")
	}
}

func TestNativeCodecNamedAdapterBounds(test *testing.T) {
	for _, scenario := range []struct{ name, plugin, version string }{
		{"invalid-id", "Plugin\nID", "1.1.0"},
		{"leading-zero-version", "builtin.claude", "01.1.0"},
		{"short-version", "builtin.claude", "1.1"},
		{"oversized-version", "builtin.claude", strings.Repeat("1", 33)},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			request := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: "owner", AssetId: strings.Repeat("A", 26), ExpectedAssetRevision: 1, AgentId: "claude", SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: scenario.plugin, PluginVersion: scenario.version, DefinitionDigest: strings.Repeat("a", 64), RequiredCapabilities: []string{"claude.provider-switch.v1"}}, Target: 1, Scope: 1, ProfileId: "profile", SelectionRevision: 1, OperationId: bytes.Repeat([]byte{1}, 16), ClientNonce: bytes.Repeat([]byte{2}, 32), ProofSignature: bytes.Repeat([]byte{3}, 64)}
			data, _ := proto.Marshal(request)
			var destination nativev1.PrepareNativeApplyRequest
			if err := (nativeConfigurationCodec{}).Unmarshal(data, &destination); err == nil {
				test.Fatal("named adapter wire grammar was not validated before service execution")
			}
		})
	}
}
