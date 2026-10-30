package httpapi

import (
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

func TestGrokBuildConfigurationStrictJSON(test *testing.T) {
	const valid = `{"draft":{"name":"Protocol Grok","grokbuildProvider":{"mode":"GROK_BUILD_MODE_CUSTOM_MODEL","model":{"profile":"Test","upstreamModel":"grok-test","endpoint":"https://synthetic.invalid/v1","name":"Test provider","apiBackend":"responses","contextWindow":"500000"}}}}`
	cases := []struct{ name, body string }{
		{"null-provider", strings.Replace(valid, `{"mode":"GROK_BUILD_MODE_CUSTOM_MODEL","model":{"profile":"Test","upstreamModel":"grok-test","endpoint":"https://synthetic.invalid/v1","name":"Test provider","apiBackend":"responses","contextWindow":"500000"}}`, `null`, 1)},
		{"null-model", `{"draft":{"name":"Test","grokbuildProvider":{"mode":2,"model":null}}}`},
		{"missing-model", `{"draft":{"name":"Test","grokbuildProvider":{"mode":2}}}`},
		{"unknown-enum", strings.Replace(valid, `"GROK_BUILD_MODE_CUSTOM_MODEL"`, `99`, 1)},
		{"unspecified-enum", strings.Replace(valid, `"GROK_BUILD_MODE_CUSTOM_MODEL"`, `0`, 1)},
		{"native-with-model", strings.Replace(valid, `"GROK_BUILD_MODE_CUSTOM_MODEL"`, `1`, 1)},
		{"duplicate-mode", strings.Replace(valid, `"mode":`, `"mode":2,"mode":`, 1)},
		{"duplicate-alias", strings.Replace(valid, `"upstreamModel":`, `"upstream_model":"grok-test","upstreamModel":`, 1)},
		{"spoof-key-status", strings.Replace(valid, `"profile":"Test"`, `"profile":"Test","keyPresent":true`, 1)},
		{"null-env", strings.Replace(valid, `"profile":"Test"`, `"profile":"Test","envKey":null`, 1)},
		{"empty-env", strings.Replace(valid, `"profile":"Test"`, `"profile":"Test","envKey":""`, 1)},
		{"bad-env", strings.Replace(valid, `"profile":"Test"`, `"profile":"Test","envKey":"1BAD"`, 1)},
		{"context-zero", strings.Replace(valid, `"500000"`, `"0"`, 1)},
		{"context-overflow", strings.Replace(valid, `"500000"`, `"9007199254740992"`, 1)},
		{"insecure-endpoint", strings.Replace(valid, `https://`, `http://`, 1)},
		{"unknown-field", strings.Replace(valid, `"profile":"Test"`, `"profile":"Test","unexpected":{}`, 1)},
		{"two-oneofs", strings.Replace(valid, `"name":"Protocol Grok"`, `"name":"Protocol Grok","claudeProvider":{}`, 1)},
		{"trailing-object", valid + `{}`},
		{"foreign-slot", strings.TrimSuffix(valid, `}`) + `,"secrets":[{"slotId":"codex.provider-key","replace":"synthetic-private"}]}`},
		{"duplicate-slot", strings.TrimSuffix(valid, `}`) + `,"secrets":[{"slotId":"grokbuild.api-key","replace":"synthetic-private"},{"slotId":"grokbuild.api-key","keep":{}}]}`},
		{"body-limit", strings.Replace(valid, `Protocol Grok`, strings.Repeat("x", 128<<10), 1)},
	}
	for _, candidate := range cases {
		test.Run(candidate.name, func(test *testing.T) {
			accounts, store := testServiceAndStore(test)
			owner, session := mcpSession(test, accounts, "grok-protocol")
			endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
			defer endpoint.Close()
			status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", []byte(candidate.body))
			if status == http.StatusOK || status >= 500 || strings.Contains(body, "synthetic-private") {
				test.Fatalf("invalid wire request accepted, crashed or disclosed credential: status %d", status)
			}
			entries, err := store.ListConfigurationAssets(test.Context(), owner)
			if err != nil || len(entries) != 0 {
				test.Fatal("wire refusal wrote actual durable state")
			}
		})
	}
	for _, snake := range []bool{false, true} {
		accounts, store := testServiceAndStore(test)
		owner, session := mcpSession(test, accounts, "grok-alias-positive")
		endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
		body := valid
		if snake {
			body = strings.NewReplacer("grokbuildProvider", "grokbuild_provider", "upstreamModel", "upstream_model", "apiBackend", "api_backend", "contextWindow", "context_window").Replace(body)
		}
		status, _ := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", []byte(body))
		endpoint.Close()
		entries, err := store.ListConfigurationAssets(test.Context(), owner)
		if status != http.StatusOK || err != nil || len(entries) != 1 {
			test.Fatal("single canonical protobuf field spelling must create one durable asset")
		}
	}
}

func TestGrokBuildConfigurationStrictBinary(test *testing.T) {
	valid, err := proto.Marshal(&configurationv1.CreateAssetRequest{Draft: grokBuildConfigurationDraft(nil)})
	if err != nil {
		test.Fatal("binary fixture encode failed")
	}
	duplicate := append(append([]byte{}, valid...), valid...)
	unknown := protowire.AppendTag(append([]byte{}, valid...), 99, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	unknownMode := grokBuildConfigurationDraft(nil)
	unknownMode.GetGrokbuildProvider().Mode = 99
	unknownEnum, err := proto.Marshal(&configurationv1.CreateAssetRequest{Draft: unknownMode})
	if err != nil {
		test.Fatal("unknown enum fixture encode failed")
	}
	for name, body := range map[string][]byte{"duplicate-draft": duplicate, "unknown-field": unknown, "unknown-enum": unknownEnum, "truncated": valid[:len(valid)-1]} {
		test.Run(name, func(test *testing.T) {
			accounts, store := testServiceAndStore(test)
			owner, session := mcpSession(test, accounts, "grok-binary-protocol")
			endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
			defer endpoint.Close()
			status, _ := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/proto", body)
			entries, err := store.ListConfigurationAssets(test.Context(), owner)
			if status != http.StatusBadRequest || err != nil || len(entries) != 0 {
				test.Fatalf("malformed binary wrote state or wrong refusal status: %d", status)
			}
		})
	}
}
