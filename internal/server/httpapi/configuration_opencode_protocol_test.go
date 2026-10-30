package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestOpenCodeConfigurationStrictSubtree(test *testing.T) {
	for _, format := range []string{"v1", "v2"} {
		test.Run(format, func(test *testing.T) {
			provider := openCodeConfigurationDraft(format).GetOpencodeProvider()
			data, err := protojson.Marshal(provider)
			if err != nil || !configurationOpenCodeJSONValid(data, provider.ProtoReflect().Descriptor()) {
				test.Fatal("valid OpenCode JSON wrappers rejected")
			}
			data, err = proto.Marshal(provider)
			if err != nil || !configurationOpenCodeWireValid(data, provider.ProtoReflect().Descriptor()) {
				test.Fatal("valid OpenCode protobuf wrappers rejected")
			}
		})
	}
	provider := &configurationv1.OpenCodeProvider{}
	for _, data := range []string{
		`{"providerId":"valid","v1":null}`,
		`{"providerId":"valid","v1":{},"v2":{}}`,
		`{"providerId":"valid","provider_id":"shadow","v1":{}}`,
		`{"providerId":"valid","v1":{"models":{"entries":{"x":null}}}}`,
		`{"providerId":"valid","v1":{"options":{"headers":{"entries":{"x":"opencode.value.01","x":"opencode.value.02"}}}}}`,
		`{"providerId":"valid","v1":{"options":{"baseUrl":"https://safe.invalid","base_url":"https://shadow.invalid"}}}`,
		`{"providerId":"valid","v1":{"options":{"future":true}}}`,
		`{"providerId":"valid","v2":{"body":{"entries":{"key":{"literal":"private-sentinel"}}}}}`,
		`{"providerId":"valid","v2":{"body":{"entries":{"key":{"null":false}}}}}`,
		`{"providerId":"valid","v2":{"body":{"entries":{"key":{"boolean":false,"number":"0"}}}}}`,
		`{"providerId":"valid","v2":{"settings":{"timeout":{"disabled":true}}}}`,
	} {
		if configurationOpenCodeJSONValid([]byte(data), provider.ProtoReflect().Descriptor()) {
			test.Fatal("strict subtree accepted malformed or private input")
		}
	}
}

func TestOpenCodeConfigurationRequestBudgetAndOrigin(test *testing.T) {
	accounts, store := testServiceAndStore(test)
	owner, session := mcpSession(test, accounts, "opencode-budget-origin")
	endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
	defer endpoint.Close()
	data, err := protojson.Marshal(&configurationv1.CreateAssetRequest{Draft: openCodeConfigurationDraft("v2")})
	if err != nil {
		test.Fatal(err)
	}
	data = append(data, bytes.Repeat([]byte(" "), configurationMaxRequestBytes-len(data))...)
	status, _ := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", data)
	if status != http.StatusOK {
		test.Fatal("exact common request budget rejected valid bounded OpenCode subtree")
	}
	status, _ = configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", append(data, ' '))
	if status < 400 || status >= 500 {
		test.Fatal("OpenCode widened common request budget")
	}
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, endpoint.URL+configurationv1connect.ConfigurationServiceCreateAssetProcedure, bytes.NewReader(data))
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Origin", "https://foreign.invalid")
	response, err := endpoint.Client().Do(request)
	if err != nil {
		test.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		test.Fatal("cross-origin OpenCode mutation accepted")
	}
	rows, err := store.ListConfigurationAssets(test.Context(), owner)
	if err != nil || len(rows) != 1 {
		test.Fatal("over-budget or cross-origin mutation reached persistence")
	}
}

func TestOpenCodeConfigurationStrictHTTP(test *testing.T) {
	accounts, store := testServiceAndStore(test)
	owner, session := mcpSession(test, accounts, "opencode-strict-http")
	endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
	defer endpoint.Close()
	for _, data := range []string{
		`{"draft":{"name":"fixture","opencodeProvider":null}}`,
		`{"draft":{"name":"fixture","opencodeProvider":{"providerId":"synthetic","v1":{}},"claudeProvider":null}}`,
		`{"draft":{"name":"fixture","claudeProvider":null,"opencodeProvider":{"providerId":"synthetic","v1":{}}}}`,
		`{"draft":{"name":"fixture","opencodeProvider":{"providerId":"synthetic","v1":{}},"codexProvider":null}}`,
		`{"draft":{"name":"fixture","geminiProvider":null,"opencodeProvider":{"providerId":"synthetic","v1":{}}}}`,
		`{"draft":{"name":"fixture","opencodeProvider":{"providerId":"synthetic","v1":{},"v2":{}}}}`,
		`{"draft":{"name":"fixture","opencodeProvider":{"providerId":"synthetic","v1":{"models":{"entries":{"model":null}}}}}}`,
		`{"draft":{"name":"fixture","opencodeProvider":{"providerId":"synthetic","v1":{"options":{"baseUrl":"https://private.invalid","base_url":"https://shadow.invalid"}}}}}`,
		`{"draft":{"name":"fixture","opencodeProvider":{"providerId":"synthetic","v2":{"body":{"entries":{"private":{"literal":"private-sentinel"}}}}}}}`,
		`{"draft":{"name":"fixture","opencodeProvider":{"providerId":"synthetic","v2":{"body":{"entries":{"private":{"null":false}}}}}}}`,
		`{"draft":{"name":"fixture","opencodeProvider":{"providerId":"synthetic","v2":{"body":{"entries":{"private":{"boolean":false,"number":"0"}}}}}}}`,
		`{"draft":{"name":"fixture","opencodeProvider":{"providerId":"synthetic","v1":{"options":{"headers":{"entries":{"x-private":"opencode.value.01","x-private":"opencode.value.02"}}}}}}}`,
		`{"draft":{"name":"fixture","opencodeProvider":{"providerId":"synthetic","v1":{"future":"private-sentinel"}}}}`,
	} {
		status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", []byte(data))
		if status != http.StatusBadRequest || strings.Contains(body, "private-sentinel") || strings.Contains(body, "private.invalid") {
			test.Fatal("strict HTTP JSON admitted ambiguous/private tree or leaked error input")
		}
	}
	draft := openCodeConfigurationDraft("v2")
	valid, err := proto.Marshal(draft.GetOpencodeProvider())
	if err != nil {
		test.Fatal(err)
	}
	field := func(number protowire.Number, value []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), value)
	}
	boolean := protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 0)
	entry := append(field(1, []byte("duplicate")), field(2, boolean)...)
	duplicated := append(field(1, []byte("synthetic")), field(3, field(7, append(field(1, entry), field(1, entry)...)))...)
	provider := func(data []byte) []byte { return field(1, append(field(1, []byte("fixture")), field(5, data)...)) }
	for _, data := range [][]byte{
		provider(duplicated),
		provider(append(bytes.Clone(valid), field(99, nil)...)),
		provider(append(bytes.Clone(valid), field(2, nil)...)),
		provider(append(bytes.Clone(valid), field(1, []byte("shadow"))...)),
		field(1, append(append(field(1, []byte("fixture")), field(5, valid)...), field(2, nil)...)),
	} {
		status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/proto", data)
		if status != http.StatusBadRequest || strings.Contains(body, "shadow") {
			test.Fatal("strict HTTP protobuf admitted mixed/unknown/duplicate tree")
		}
	}
	rows, err := store.ListConfigurationAssets(test.Context(), owner)
	if err != nil || len(rows) != 0 {
		test.Fatal("rejected wire input reached persistence")
	}
}

func TestOpenCodeConfigurationRecursionBudgetIsolation(test *testing.T) {
	for _, jsonFormat := range []bool{false, true} {
		request := &configurationv1.ListAssetsRequest{}
		data, err := proto.Marshal(request)
		if jsonFormat {
			data, err = protojson.Marshal(request)
		}
		if err != nil || configurationOpenCodeRecursionLimit(data, request.ProtoReflect().Descriptor(), jsonFormat) != 16 {
			test.Fatal("non-Draft RPC inherited OpenCode recursion budget")
		}
	}
	for _, draft := range []*configurationv1.Draft{configurationDraft("Claude"), codexConfigurationDraft(), geminiConfigurationDraft(), openCodeConfigurationDraft("v2")} {
		for _, request := range []proto.Message{&configurationv1.CreateAssetRequest{Draft: draft}, &configurationv1.UpdateAssetRequest{Id: "id", ExpectedRevision: 1, Draft: draft}} {
			for _, jsonFormat := range []bool{false, true} {
				data, err := proto.Marshal(request)
				if jsonFormat {
					data, err = protojson.Marshal(request)
				}
				if err != nil {
					test.Fatal(err)
				}
				limit := configurationOpenCodeRecursionLimit(data, request.ProtoReflect().Descriptor(), jsonFormat)
				expected := 16
				if draft.GetOpencodeProvider() != nil {
					expected = 96
				}
				if limit != expected {
					test.Fatal("OpenCode wrapper budget escaped its exact mutation branch")
				}
			}
		}
	}
	for _, data := range []string{
		`{"draft":{"name":"opencodeProvider","claudeProvider":{"authMode":2,"models":null}}}`,
		`{"draft":{"name":"opencode_provider","claudeProvider":{"authMode":2,"endpoint":null}}}`,
	} {
		var message configurationv1.CreateAssetRequest
		if err := (configurationCodec{name: "json"}).Unmarshal([]byte(data), &message); err != nil || message.GetDraft().GetClaudeProvider() == nil {
			test.Fatal("old Claude null compatibility changed")
		}
		if configurationOpenCodeRecursionLimit([]byte(data), message.ProtoReflect().Descriptor(), true) != 16 {
			test.Fatal("string content selected elevated recursion")
		}
	}
	for _, data := range []string{
		`{"draft":{"name":"Codex","codexProvider":{"authMode":1,"endpoint":null}}}`,
		`{"draft":{"name":"Gemini","geminiProvider":{"authMode":1,"endpoint":null}}}`,
	} {
		var message configurationv1.CreateAssetRequest
		if err := (configurationCodec{name: "json"}).Unmarshal([]byte(data), &message); err == nil {
			test.Fatal("old Codex/Gemini strict null policy relaxed")
		}
	}
}

func TestOpenCodeConfigurationStrictWire(test *testing.T) {
	field := func(number protowire.Number, value []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), value)
	}
	boolean := protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 0)
	entry := func(key string, value []byte, reversed bool) []byte {
		keyField, valueField := field(1, []byte(key)), field(2, value)
		if reversed {
			return append(valueField, keyField...)
		}
		return append(keyField, valueField...)
	}
	provider := func(entries []byte) []byte {
		return append(field(1, []byte("synthetic")), field(3, field(7, entries))...)
	}
	descriptor := (&configurationv1.OpenCodeProvider{}).ProtoReflect().Descriptor()
	first := field(1, entry("first", boolean, false))
	second := field(1, entry("second", boolean, true))
	valid := provider(append(bytes.Clone(first), second...))
	if !configurationOpenCodeWireValid(valid, descriptor) {
		test.Fatal("distinct map entries or reversed key/value order rejected")
	}
	for name, data := range map[string][]byte{
		"duplicate-map-key":   provider(append(bytes.Clone(first), first...)),
		"missing-map-key":     provider(field(1, field(2, boolean))),
		"missing-map-value":   provider(field(1, field(1, []byte("missing")))),
		"duplicate-entry-key": provider(field(1, append(entry("first", boolean, false), field(1, []byte("shadow"))...))),
		"unknown-entry-field": provider(field(1, append(entry("first", boolean, false), field(3, nil)...))),
		"multiple-oneof":      append(bytes.Clone(valid), field(2, nil)...),
		"duplicate-scalar":    append(bytes.Clone(valid), field(1, []byte("shadow"))...),
		"unknown-field":       append(bytes.Clone(valid), field(99, nil)...),
		"invalid-utf8":        append(field(1, []byte{0xff}), field(3, nil)...),
		"wrong-scalar-type":   append(protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 0), field(3, nil)...),
		"literal-secret":      provider(field(1, entry("private", field(4, []byte("private-sentinel")), false))),
		"false-null":          provider(field(1, entry("invalid", protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 0), false))),
		"nonboolean-varint":   provider(field(1, entry("invalid", protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 2), false))),
		"empty-value-oneof":   provider(field(1, entry("invalid", nil, false))),
		"empty-format-oneof":  field(1, []byte("synthetic")),
		"oversize":            append(field(1, bytes.Repeat([]byte("x"), 96<<10)), field(3, nil)...),
	} {
		test.Run(name, func(test *testing.T) {
			if configurationOpenCodeWireValid(data, descriptor) {
				test.Fatal("strict wire accepted malformed/ambiguous/secret input")
			}
		})
	}
}
