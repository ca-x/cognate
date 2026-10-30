package httpapi

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestImportSelectionDescriptorAdditive(test *testing.T) {
	file := importv1.File_skillhub_import_v1_import_proto
	service := file.Services().ByName("ImportService")
	for _, name := range []protoreflect.Name{"PreviewImportSelection", "CommitImportSelection", "GetImportCommitStatus"} {
		if service.Methods().ByName(name) == nil {
			test.Errorf("missing method %s", name)
		}
	}
	legacy := map[protoreflect.Name][]string{
		"PreviewLibraryExportRequest":     {"format", "export_data"},
		"RepositoryCandidate":             {"repository", "branch", "existing_repository_id", "source_enabled"},
		"PreviewLibraryExportResponse":    {"repositories", "referenced_skill_count"},
		"PreviewCCSwitchSnapshotRequest":  {"snapshot_data"},
		"PreviewCCSwitchSnapshotResponse": {"repositories"},
	}
	for name, names := range legacy {
		message := file.Messages().ByName(name)
		if message == nil || message.Fields().Len() != len(names) {
			test.Fatalf("legacy shape changed: %s", name)
		}
		for index, fieldName := range names {
			field := message.Fields().ByNumber(protoreflect.FieldNumber(index + 1))
			if field == nil || string(field.Name()) != fieldName {
				test.Fatalf("legacy tag changed: %s", name)
			}
		}
	}
	if !file.Messages().ByName("RepositoryCandidate").Fields().ByNumber(4).HasPresence() {
		test.Fatal("source_enabled lost presence")
	}
	for _, name := range []protoreflect.Name{"PreviewLibraryExport", "PreviewCCSwitchSnapshot"} {
		method := service.Methods().ByName(name)
		if method == nil || string(method.Input().Name()) != string(name)+"Request" || string(method.Output().Name()) != string(name)+"Response" || method.IsStreamingClient() || method.IsStreamingServer() {
			test.Fatalf("legacy method changed: %s", name)
		}
	}
	for _, name := range []protoreflect.Name{"PreviewImportSelectionRequest", "CommitImportSelectionRequest", "GetImportCommitStatusRequest", "ImportReceipt", "ImportCandidateOptions"} {
		if file.Messages().ByName(name) == nil {
			test.Errorf("missing selection message %s", name)
		}
	}
}

func TestImportSelectionEnumLedger(test *testing.T) {
	file := importv1.File_skillhub_import_v1_import_proto
	for name, values := range map[protoreflect.Name]string{
		"ImportSelectionSource":       "UNSPECIFIED CCSWITCH_SCHEMA24 MAGPIE_LIBRARY_JSON",
		"ImportCandidateStatus":       "UNSPECIFIED READY NEEDS_CHOICE NEEDS_REPOSITORY_IMPORT UNRESOLVED UNSUPPORTED_AGENT UNSUPPORTED_KIND UNSUPPORTED_FIELD NAME_CONFLICT",
		"ImportNoticeCode":            "UNSPECIFIED CREDENTIALS_OMITTED METADATA_EXCLUDED OTHER_SOURCE_DATA_EXCLUDED SOURCE_CONTENT_NOT_COMPARED UNKNOWN_SOURCE_SECTION_EXCLUDED",
		"ImportReceiptPhase":          "UNSPECIFIED RESERVED COLLECTING COMPLETED PARTIAL FAILED",
		"ImportCommitObservation":     "UNSPECIFIED NOT_OBSERVED IN_PROGRESS UNKNOWN TERMINAL",
		"ImportRepositoryOutcomeCode": "UNSPECIFIED COLLECTED ALREADY_COLLECTED NOT_ATTEMPTED CANCELLED AUTHORIZATION_LOST SOURCE_REJECTED CAPACITY_EXHAUSTED UNKNOWN",
	} {
		enum := file.Enums().ByName(name)
		if enum == nil {
			test.Fatalf("missing enum %s", name)
		}
		names := strings.Fields(values)
		if enum.Values().Len() != len(names) {
			test.Errorf("enum value count changed %s", name)
		}
		for index, suffix := range names {
			value := enum.Values().ByNumber(protoreflect.EnumNumber(index))
			if value == nil || !strings.HasSuffix(string(value.Name()), "_"+suffix) {
				test.Errorf("enum number changed %s:%d", name, index)
			}
		}
	}
}

func TestImportSelectionGeneratedClientUnavailable(test *testing.T) {
	fixture := newImportFixture(test)
	for _, jsonProtocol := range []bool{false, true} {
		var options []connect.ClientOption
		if jsonProtocol {
			options = append(options, connect.WithProtoJSON())
		}
		client := importv1connect.NewImportServiceClient(fixture.server.Client(), fixture.server.URL, options...)
		for _, authorized := range []bool{false, true} {
			preview := connect.NewRequest(&importv1.PreviewImportSelectionRequest{})
			commit := connect.NewRequest(&importv1.CommitImportSelectionRequest{})
			status := connect.NewRequest(&importv1.GetImportCommitStatusRequest{})
			if authorized {
				for _, header := range []http.Header{preview.Header(), commit.Header(), status.Header()} {
					header.Set("Authorization", "Bearer "+fixture.sessions[0])
				}
			}
			_, previewErr := client.PreviewImportSelection(test.Context(), preview)
			_, commitErr := client.CommitImportSelection(test.Context(), commit)
			_, statusErr := client.GetImportCommitStatus(test.Context(), status)
			expected := connect.CodeUnauthenticated
			if authorized {
				expected = connect.CodeUnimplemented
			}
			for _, err := range []error{previewErr, commitErr, statusErr} {
				if connect.CodeOf(err) != expected {
					test.Errorf("json=%t auth=%t got=%v want=%v", jsonProtocol, authorized, connect.CodeOf(err), expected)
				}
			}
		}
	}
}

func TestImportSelectionLegacyCodecUnchanged(test *testing.T) {
	legacy := new(importv1.PreviewLibraryExportRequest)
	if err := (importCodec{name: "json"}).Unmarshal([]byte(`{"format":1,"exportData":"e30=","futureField":{"nested":true}}`), legacy); err != nil || legacy.Format != importv1.ExportFormat_EXPORT_FORMAT_MAGPIE_LIBRARY_JSON || string(legacy.ExportData) != "{}" {
		test.Fatal("legacy unknown JSON behavior changed")
	}
	if err := (importCodec{name: "json"}).Unmarshal([]byte(`{"format":null,"exportData":null}`), legacy); err != nil {
		test.Fatal("legacy null behavior changed")
	}
}

func TestImportSelectionFieldLedger(test *testing.T) {
	file := importv1.File_skillhub_import_v1_import_proto
	ledger := map[protoreflect.Name]string{
		"PreviewImportSelectionRequest":  "source source_data options",
		"ImportCandidateOptions":         "candidate_id name claude_auth_mode gemini_auth_mode gemini_key_transport skill_binding",
		"PreviewImportSelectionResponse": "ticket expires_ms repositories skills configurations notices",
		"ImportRepositoryCandidate":      "id hint status",
		"ImportSkillCandidate":           "id safe_name repository_candidate_id status binding notices",
		"SkillBinding":                   "repository_id archive_hash skill_id logical_path content_hash repository branch",
		"ImportConfigurationCandidate":   "id known_agent_id kind status draft notices credential_slots safe_name",
		"ImportCredentialSlot":           "slot_id present",
		"ImportNotice":                   "id code count",
		"CommitImportSelectionRequest":   "ticket source source_data options operation_id repository_ids skill_ids configuration_ids ack_notice_ids",
		"CommitImportSelectionResponse":  "receipt",
		"GetImportCommitStatusRequest":   "operation_id",
		"GetImportCommitStatusResponse":  "observation receipt",
		"ImportReceipt":                  "operation_id revision phase repository_outcomes selected_skill_bindings created_assets notices",
		"ImportRepositoryOutcome":        "candidate_id code repository_id",
	}
	for name, fields := range ledger {
		message := file.Messages().ByName(name)
		if message == nil {
			test.Errorf("missing message %s", name)
			continue
		}
		names := strings.Fields(fields)
		if message.Fields().Len() != len(names) {
			test.Errorf("field count mismatch %s", name)
		}
		for index, fieldName := range names {
			field := message.Fields().ByNumber(protoreflect.FieldNumber(index + 1))
			if field == nil || string(field.Name()) != fieldName {
				test.Errorf("field ledger mismatch %s tag%d", name, index+1)
			}
		}
	}
	options := file.Messages().ByName("ImportCandidateOptions")
	if options == nil {
		return
	}
	for _, number := range []protoreflect.FieldNumber{2, 3, 4, 5, 6} {
		if !options.Fields().ByNumber(number).HasPresence() {
			test.Errorf("option tag%d lost presence", number)
		}
	}
	claude, gemini := options.Fields().ByNumber(3), options.Fields().ByNumber(4)
	if claude.ContainingOneof() == nil || claude.ContainingOneof() != gemini.ContainingOneof() || claude.ContainingOneof().IsSynthetic() {
		test.Fatal("auth choices are not one exclusive union")
	}
	for name, numbers := range map[protoreflect.Name][]protoreflect.FieldNumber{
		"PreviewImportSelectionRequest": {2}, "CommitImportSelectionRequest": {1, 3}, "PreviewImportSelectionResponse": {1},
	} {
		for _, number := range numbers {
			options := file.Messages().ByName(name).Fields().ByNumber(number).Options().(*descriptorpb.FieldOptions)
			if !options.GetDebugRedact() {
				test.Errorf("sensitive request field not redacted %s:%d", name, number)
			}
		}
	}
}

func TestImportSelectionLegacyUnknownRetention(test *testing.T) {
	descriptor := protodesc.ToFileDescriptorProto(importv1.File_skillhub_import_v1_import_proto)
	descriptor.Dependency = nil
	descriptor.MessageType = descriptor.MessageType[:5]
	descriptor.EnumType = descriptor.EnumType[:1]
	descriptor.Service[0].Method = descriptor.Service[0].Method[:2]
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(descriptor)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(encoded)) != "17d717703bd4d3947e3b3c7167631fd4c62f5723ac6bfc37765ee9fd3743bfcd" {
		test.Fatal("complete legacy descriptor changed")
	}
	legacy, err := protodesc.NewFile(descriptor, nil)
	if err != nil {
		test.Fatal("legacy descriptor cannot be loaded")
	}
	request := dynamicpb.NewMessage(legacy.Messages().ByName("PreviewCCSwitchSnapshotRequest"))
	wire := protowire.AppendTag(nil, 1, protowire.BytesType)
	wire = protowire.AppendBytes(wire, []byte("synthetic snapshot"))
	wire = protowire.AppendTag(wire, 99, protowire.BytesType)
	wire = protowire.AppendString(wire, "future synthetic field")
	if err := proto.Unmarshal(wire, request); err != nil {
		test.Fatal("legacy dynamic decode failed")
	}
	actual, err := proto.Marshal(request)
	if err != nil || !bytes.Equal(actual, wire) {
		test.Fatal("legacy unknown binary field was not retained")
	}
}

func TestImportSelectionUnavailableBeforeBody(test *testing.T) {
	fixture := newImportFixture(test)
	for _, method := range []string{"PreviewImportSelection", "CommitImportSelection", "GetImportCommitStatus"} {
		for _, authorized := range []bool{false, true} {
			body := &importUnreadBody{}
			request := httptest.NewRequest(http.MethodPost, "/skillhub.import.v1.ImportService/"+method, body)
			request.Header.Set("Content-Type", "application/json")
			if authorized {
				request.Header.Set("Authorization", "Bearer "+fixture.sessions[0])
			}
			response := httptest.NewRecorder()
			fixture.imports.Guard(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				test.Error("unimplemented selection reached decoder")
			})).ServeHTTP(response, request)
			expected := http.StatusUnauthorized
			if authorized {
				expected = http.StatusNotImplemented
			}
			if response.Code != expected || body.reads != 0 {
				test.Errorf("%s auth=%t status=%d reads=%d", method, authorized, response.Code, body.reads)
			}
		}
	}
}
