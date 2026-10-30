package httpapi

import (
	"bytes"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	packconfigurationv1 "github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1/packconfigurationv1connect"
	"github.com/labstack/echo/v4"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestPackConfigurationStrictCodec(test *testing.T) {
	for _, format := range []string{"json", "proto"} {
		codec := packConfigurationCodec{name: format}
		valid := &packconfigurationv1.ReplaceAttachmentsRequest{PackId: "opaque-pack", ExpectedPackRevision: 9007199254740993, ExpectedConfigurationRevision: math.MaxInt64, References: []*packconfigurationv1.AttachmentReference{{AssetId: "first", ExpectedAssetRevision: math.MaxInt64, TargetId: "claude.provider.user"}, {AssetId: "second", ExpectedAssetRevision: 2, TargetId: "codex.provider.user"}}}
		encoded, err := codec.Marshal(valid)
		if err != nil {
			test.Fatal(err)
		}
		var decoded packconfigurationv1.ReplaceAttachmentsRequest
		if err := codec.Unmarshal(encoded, &decoded); err != nil || !proto.Equal(&decoded, valid) {
			test.Fatal("lossless int64/order roundtrip")
		}
		if err := codec.Unmarshal(encoded, &configurationv1.DeleteAssetRequest{}); err == nil {
			test.Fatal("new codec hijacked old request")
		}
		if _, err := codec.Marshal(&packconfigurationv1.GetAttachmentsResponse{Snapshot: &packconfigurationv1.AttachmentSnapshot{PackId: strings.Repeat("x", 64<<10)}}); err == nil {
			test.Fatal("response overflow")
		}
		if err := codec.Unmarshal(bytes.Repeat([]byte{' '}, (64<<10)+1), &decoded); err == nil {
			test.Fatal("request overflow")
		}
	}
	codec := packConfigurationCodec{name: "json"}
	for _, input := range []string{
		`null`, `[]`, `{} {}`, `{"packId":null}`, `{"packId":"p","packId":"q"}`,
		`{"packId":"p","pack_id":"q"}`, `{"unknown-canary":"secret"}`,
		`{"references":null}`, `{"references":[null]}`, `{"references":[{"assetId":null}]}`,
		`{"references":[{"assetId":"a","asset_id":"b"}]}`, `{"references":[{"unknown-canary":"private"}]}`,
		`{"references":[{"expectedAssetRevision":{}}]}`, `{"expectedPackRevision":1.5}`,
		`{"expectedPackRevision":1e3}`, `{"expectedPackRevision":"1e3"}`, `{"expectedPackRevision":true}`,
		`{"expectedPackRevision":"9223372036854775808"}`, `{"expectedPackRevision":-1}`,
		`{"references":{}}`, `{"references":[{"targetId":9}]}`,
	} {
		var message packconfigurationv1.ReplaceAttachmentsRequest
		if err := codec.Unmarshal([]byte(input), &message); connect.CodeOf(err) != connect.CodeInvalidArgument {
			test.Fatal("strict JSON request accepted")
		}
		if !proto.Equal(&message, &packconfigurationv1.ReplaceAttachmentsRequest{}) {
			test.Fatal("failed decode returned partial request")
		}
	}
	for _, text := range []string{`{"expectedPackRevision":9007199254740993}`, `{"expectedPackRevision":"9007199254740993"}`} {
		var message packconfigurationv1.ReplaceAttachmentsRequest
		if err := codec.Unmarshal([]byte(text), &message); err != nil || message.ExpectedPackRevision != 9007199254740993 {
			test.Fatal("JSON int64 precision lost")
		}
	}
	codec = packConfigurationCodec{name: "proto"}
	unknown := protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)
	wrong := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 1)
	duplicate := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), "first")
	duplicate = protowire.AppendString(protowire.AppendTag(duplicate, 1, protowire.BytesType), "second")
	duplicateNumber := protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 1)
	duplicateNumber = protowire.AppendVarint(protowire.AppendTag(duplicateNumber, 2, protowire.VarintType), 2)
	negative := protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), math.MaxUint64)
	wrongNumber := protowire.AppendString(protowire.AppendTag(nil, 2, protowire.BytesType), "1")
	nestedUnknown := protowire.AppendBytes(protowire.AppendTag(nil, 4, protowire.BytesType), unknown)
	var tooMany []byte
	for range 17 {
		tooMany = protowire.AppendBytes(protowire.AppendTag(tooMany, 4, protowire.BytesType), nil)
	}
	for _, body := range [][]byte{unknown, wrong, duplicate, duplicateNumber, negative, wrongNumber, nestedUnknown, tooMany, {0xff}, {0x22, 0xff}} {
		var message packconfigurationv1.ReplaceAttachmentsRequest
		if err := codec.Unmarshal(body, &message); connect.CodeOf(err) != connect.CodeInvalidArgument {
			test.Fatal("strict wire request accepted")
		}
	}
}

func TestPackConfigurationOuterGuardKeepsNearAndLegacyLimits(test *testing.T) {
	for _, path := range []string{
		packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure,
		packconfigurationv1connect.PackConfigurationServiceReplaceAttachmentsProcedure,
		packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure + "Extra",
		"/skillhub.pack.v1.PackService/GetPack",
	} {
		request := httptest.NewRequest(http.MethodPost, "https://localhost"+path, bytes.NewReader(bytes.Repeat([]byte{'x'}, 5000)))
		response := httptest.NewRecorder()
		server := echo.New()
		ctx := server.NewContext(request, response)
		read := false
		err := guardRequests("")(func(ctx echo.Context) error {
			body, err := io.ReadAll(ctx.Request().Body)
			read = true
			if packConfigurationProcedure(path) {
				if err != nil || len(body) != 5000 {
					test.Fatal("new method retained old 4096 outer limit")
				}
			} else if err == nil {
				test.Fatal("near/legacy path inherited 64KiB allowance")
			}
			return nil
		})(ctx)
		if err != nil || !read {
			test.Fatal("outer guard oracle not exercised")
		}
	}
}

func TestPackConfigurationTLSStrictProtocolAndOuterLimits(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	for _, procedure := range []string{packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, packconfigurationv1connect.PackConfigurationServiceReplaceAttachmentsProcedure} {
		for _, encoding := range []string{"json", "proto"} {
			body := []byte(`{"unknown-private-canary":"do-not-echo"}`)
			if encoding == "proto" {
				body = protowire.AppendBytes(protowire.AppendTag(nil, 99, protowire.BytesType), []byte("unknown-private-canary"))
			}
			status, output := configurationRaw(test, fixture.endpoint, fixture.session, procedure, "application/"+encoding, body)
			if status != http.StatusBadRequest || strings.Contains(output, "canary") {
				test.Fatal("unknown fields crossed real TLS decoder")
			}
		}
	}
	body := []byte(`{"packId":"missing"}`)
	body = append(body, bytes.Repeat([]byte{' '}, (64<<10)-len(body))...)
	status, _ := configurationRaw(test, fixture.endpoint, fixture.session, packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, "application/json", body)
	if status != http.StatusNotFound {
		test.Fatal("exact 64KiB limit shadowed by old Echo 4096 guard")
	}
	body = append(body, ' ')
	status, _ = configurationRaw(test, fixture.endpoint, fixture.session, packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, "application/json", body)
	if status != http.StatusTooManyRequests {
		test.Fatal("64KiB+1 was not resource-exhausted")
	}
	for _, path := range []string{packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure + "/extra", packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure + "Extra"} {
		status, _ := configurationRaw(test, fixture.endpoint, fixture.session, path, "application/json", []byte(`{"packId":"missing"}`))
		if status != http.StatusNotImplemented {
			test.Fatal("near path became an attachment RPC")
		}
	}
}

func TestPackConfigurationWireLedgerDescriptor(test *testing.T) {
	descriptor := packconfigurationv1.File_skillhub_pack_configuration_v1_configuration_proto
	service := descriptor.Services().Get(0)
	if service.FullName() != "skillhub.pack.configuration.v1.PackConfigurationService" || service.Methods().Len() != 2 {
		test.Fatal("wire service drift")
	}
	for name, fields := range map[string][]string{
		"GetAttachmentsRequest":     {"pack_id"},
		"AttachmentReference":       {"asset_id", "expected_asset_revision", "target_id"},
		"ReplaceAttachmentsRequest": {"pack_id", "expected_pack_revision", "expected_configuration_revision", "references"},
		"AttachmentEntry":           {"attachment_id", "asset_id", "asset_revision", "agent_id", "kind", "schema_version", "target_id", "ordinal", "state"},
		"AttachmentSnapshot":        {"pack_id", "pack_revision", "configuration_revision", "entries", "snapshot_digest"},
	} {
		message := descriptor.Messages().ByName(protoreflect.Name(name))
		if message == nil || message.Fields().Len() != len(fields) {
			test.Fatal("wire fields drift")
		}
		for ordinal, field := range fields {
			if message.Fields().ByNumber(protoreflect.FieldNumber(ordinal+1)).Name() != protoreflect.Name(field) {
				test.Fatal("nested tag changed")
			}
		}
	}
}
