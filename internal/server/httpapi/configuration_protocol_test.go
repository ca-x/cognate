package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func configurationRaw(test *testing.T, endpoint *httptest.Server, session, procedure, contentType string, data []byte) (int, string) {
	test.Helper()
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, endpoint.URL+procedure, bytes.NewReader(data))
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Connect-Protocol-Version", "1")
	response, err := endpoint.Client().Do(request)
	if err != nil {
		test.Fatal("configuration HTTP request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		test.Fatal("configuration HTTP response failed")
	}
	return response.StatusCode, string(body)
}

func TestConfigurationStrictProtocol(test *testing.T) {
	service, store := testServiceAndStore(test)
	_, session := mcpSession(test, service, "configuration-protocol")
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
	test.Cleanup(endpoint.Close)
	const sentinel = "private-protocol-sentinel"
	for _, procedure := range []string{
		configurationv1connect.ConfigurationServiceListAssetsProcedure,
		configurationv1connect.ConfigurationServiceGetAssetProcedure,
		configurationv1connect.ConfigurationServiceReadDraftProcedure,
		configurationv1connect.ConfigurationServiceCreateAssetProcedure,
		configurationv1connect.ConfigurationServiceUpdateAssetProcedure,
		configurationv1connect.ConfigurationServiceCopyAssetProcedure,
		configurationv1connect.ConfigurationServiceDeleteAssetProcedure,
	} {
		for _, format := range []string{"json", "proto"} {
			data := []byte(`{"private-protocol-sentinel":true}`)
			if format == "proto" {
				data = protowire.AppendTag(nil, 99, protowire.VarintType)
				data = protowire.AppendVarint(data, 1)
			}
			status, body := configurationRaw(test, endpoint, session, procedure, "application/"+format, data)
			if status != http.StatusBadRequest || strings.Contains(body, sentinel) {
				test.Fatalf("procedure accepted unknown fields: %s", procedure)
			}
		}
	}
	for _, data := range []string{
		`{"draft":{"name":"Private","claudeProvider":{"authMode":2}},"private-protocol-sentinel":true}`,
		`{"draft":{"name":"Private","claudeProvider":{"authMode":2,"private-protocol-sentinel":true}}}`,
		`{"draft":{"name":"Private","claudeProvider":{"authMode":99}}}`,
		`{"draft":{"name":"Private","claudeProvider":{"authMode":"private-protocol-sentinel"}}}`,
		`{"draft":{"name":"Private","futureProvider":{"private-protocol-sentinel":true}}}`,
		`{"draft":{"name":"Private","claudeProvider":{"authMode":2}},"secrets":[{"slotId":"claude.api-key","futureAction":"private-protocol-sentinel"}]}`,
		`{"draft":{"name":"Private","claudeProvider":{"authMode":2}},"secrets":[{"slotId":"claude.api-key","keep":{},"replace":"private-protocol-sentinel"}]}`,
	} {
		status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", []byte(data))
		if status != http.StatusBadRequest || strings.Contains(body, sentinel) {
			test.Fatalf("strict JSON failed closed: status %d", status)
		}
	}
	unknown := protowire.AppendTag(nil, 99, protowire.BytesType)
	unknown = protowire.AppendString(unknown, sentinel)
	for _, location := range []string{"request", "draft", "provider", "models", "mutation", "keep", "clear", "enum"} {
		test.Run(location, func(test *testing.T) {
			message := &configurationv1.CreateAssetRequest{Draft: configurationDraft("Private"), Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}}}}
			switch location {
			case "request":
				message.ProtoReflect().SetUnknown(unknown)
			case "draft":
				message.Draft.ProtoReflect().SetUnknown(unknown)
			case "provider":
				message.Draft.GetClaudeProvider().ProtoReflect().SetUnknown(unknown)
			case "models":
				message.Draft.GetClaudeProvider().Models = &configurationv1.ClaudeModels{}
				message.Draft.GetClaudeProvider().Models.ProtoReflect().SetUnknown(unknown)
			case "mutation":
				message.Secrets[0].ProtoReflect().SetUnknown(unknown)
			case "keep":
				keep := &configurationv1.KeepSecret{}
				keep.ProtoReflect().SetUnknown(unknown)
				message.Secrets[0].Action = &configurationv1.SecretMutation_Keep{Keep: keep}
			case "clear":
				message.Secrets[0].GetClear().ProtoReflect().SetUnknown(unknown)
			case "enum":
				message.Draft.GetClaudeProvider().AuthMode = 99
			}
			data, err := proto.Marshal(message)
			if err != nil {
				test.Fatal("cannot encode protocol fixture")
			}
			status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/proto", data)
			if status != http.StatusBadRequest || strings.Contains(body, sentinel) {
				test.Fatalf("strict binary failed closed: status %d", status)
			}
		})
	}
	keep := &configurationv1.KeepSecret{}
	keep.ProtoReflect().SetUnknown(unknown)
	shadowed, err := proto.Marshal(&configurationv1.SecretMutation{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Keep{Keep: keep}})
	if err != nil {
		test.Fatal("cannot encode shadowed oneof fixture")
	}
	shadowed = protowire.AppendTag(shadowed, 3, protowire.BytesType)
	shadowed = protowire.AppendString(shadowed, sentinel)
	data, err := proto.Marshal(&configurationv1.CreateAssetRequest{Draft: configurationDraft("Shadowed oneof")})
	if err != nil {
		test.Fatal("cannot encode shadowed request fixture")
	}
	data = protowire.AppendTag(data, 2, protowire.BytesType)
	data = protowire.AppendBytes(data, shadowed)
	status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/proto", data)
	if status != http.StatusBadRequest || strings.Contains(body, sentinel) {
		test.Fatal("overwritten binary oneof hid unknown nested fields")
	}
	client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
	listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
	if err != nil || len(listed.Msg.Assets) != 0 {
		test.Fatal("rejected protocol mutated assets")
	}
}
