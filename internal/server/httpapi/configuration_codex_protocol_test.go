package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const codexConfigurationJSON = `{"draft":{"name":"Protocol fixture","codexProvider":{"authMode":1,"endpoint":"https://api.example.test/v1","model":"model-b","reasoningEffort":3,"catalog":{"models":[{"model":"model-b","displayName":"Private model","contextWindow":"200000","supportsParallelToolCalls":false,"inputModalities":{"values":[2,1]},"baseInstructions":"private instructions","reasoningLevels":{"values":[5,3]},"defaultReasoningLevel":3},{"model":"model-a"}]}}},"secrets":[{"slotId":"codex.provider-key","replace":"protocol-synthetic-key"}]}`

func codexJSONEdit(test *testing.T, data []byte, path []string, change func([]byte) []byte) []byte {
	test.Helper()
	if len(path) == 0 {
		return change(bytes.Clone(data))
	}
	var encoded []byte
	var err error
	if len(data) > 0 && data[0] == '[' {
		var values []jsontext.Value
		if err := json.Unmarshal(data, &values); err != nil {
			test.Fatal("invalid array fixture")
		}
		index, err := strconv.Atoi(path[0])
		if err != nil || index >= len(values) {
			test.Fatal("invalid fixture index")
		}
		values[index] = codexJSONEdit(test, values[index], path[1:], change)
		encoded, err = json.Marshal(values, jsontext.AllowDuplicateNames(true))
	} else {
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(data, &fields); err != nil {
			test.Fatal("invalid object fixture")
		}
		value, exists := fields[path[0]]
		if !exists {
			test.Fatal("missing fixture field")
		}
		fields[path[0]] = codexJSONEdit(test, value, path[1:], change)
		encoded, err = json.Marshal(fields, jsontext.AllowDuplicateNames(true))
	}
	if err != nil {
		test.Fatal("cannot encode protocol fixture")
	}
	return encoded
}

func codexWireEdit(test *testing.T, data []byte, path []protowire.Number, change func([]byte) []byte) []byte {
	test.Helper()
	if len(path) == 0 {
		return change(bytes.Clone(data))
	}
	for offset := 0; offset < len(data); {
		number, kind, tagSize := protowire.ConsumeTag(data[offset:])
		if tagSize < 0 {
			test.Fatal("invalid fixture tag")
		}
		size := protowire.ConsumeFieldValue(number, kind, data[offset+tagSize:])
		if size < 0 {
			test.Fatal("invalid fixture value")
		}
		end := offset + tagSize + size
		if number == path[0] {
			value, consumed := protowire.ConsumeBytes(data[offset+tagSize:])
			if kind != protowire.BytesType || consumed < 0 {
				test.Fatal("fixture path not a message")
			}
			replaced := codexWireEdit(test, value, path[1:], change)
			result := bytes.Clone(data[:offset])
			result = protowire.AppendTag(result, number, kind)
			result = protowire.AppendBytes(result, replaced)
			return append(result, data[end:]...)
		}
		offset = end
	}
	test.Fatal("missing wire fixture path")
	return nil
}

func TestCodexConfigurationProtocol(test *testing.T) {
	service, store := testServiceAndStore(test)
	_, session := mcpSession(test, service, "codex-protocol")
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
	test.Cleanup(endpoint.Close)
	procedure := configurationv1connect.ConfigurationServiceCreateAssetProcedure
	check := func(test *testing.T, format string, data []byte, wanted int) {
		test.Helper()
		status, body := configurationRaw(test, endpoint, session, procedure, "application/"+format, data)
		if status != wanted {
			test.Fatalf("protocol status %d, wanted %d", status, wanted)
		}
		if wanted != http.StatusOK && (strings.Contains(body, "protocol-synthetic-key") || strings.Contains(body, "private instructions")) {
			test.Fatal("protocol error exposed private request")
		}
	}
	test.Run("json-positive", func(test *testing.T) { check(test, "json", []byte(codexConfigurationJSON), http.StatusOK) })
	provider := []string{"draft", "codexProvider"}
	model := []string{"draft", "codexProvider", "catalog", "models", "0"}
	paths := [][]string{nil, {"draft"}, provider, appendPath(provider, "catalog"), model, appendPath(model, "inputModalities"), appendPath(model, "reasoningLevels"), {"secrets", "0"}}
	for index, path := range paths {
		test.Run("unknown-"+strconv.Itoa(index), func(test *testing.T) {
			data := codexJSONEdit(test, []byte(codexConfigurationJSON), path, func(value []byte) []byte { return append(value[:len(value)-1], []byte(`,"untrustedField":null}`)...) })
			check(test, "json", data, http.StatusBadRequest)
		})
		test.Run("duplicate-"+strconv.Itoa(index), func(test *testing.T) {
			data := codexJSONEdit(test, []byte(codexConfigurationJSON), path, func(value []byte) []byte { return append(append(bytes.Clone(value[:len(value)-1]), ','), value[1:]...) })
			check(test, "json", data, http.StatusBadRequest)
		})
	}
	nullPaths := [][]string{provider, appendPath(provider, "authMode"), appendPath(provider, "endpoint"), appendPath(provider, "model"), appendPath(provider, "reasoningEffort"), appendPath(provider, "catalog"), appendPath(provider, "catalog", "models"), model}
	for _, name := range []string{"model", "displayName", "contextWindow", "supportsParallelToolCalls", "inputModalities", "baseInstructions", "reasoningLevels", "defaultReasoningLevel"} {
		nullPaths = append(nullPaths, appendPath(model, name))
	}
	nullPaths = append(nullPaths, appendPath(model, "inputModalities", "values"), appendPath(model, "inputModalities", "values", "0"), appendPath(model, "reasoningLevels", "values"), appendPath(model, "reasoningLevels", "values", "0"))
	for index, path := range nullPaths {
		for _, value := range []string{"null", `{"wrong":true}`} {
			test.Run("type-"+strconv.Itoa(index)+"-"+value, func(test *testing.T) {
				data := codexJSONEdit(test, []byte(codexConfigurationJSON), path, func([]byte) []byte { return []byte(value) })
				check(test, "json", data, http.StatusBadRequest)
			})
		}
	}
	for _, entry := range []struct {
		path  []string
		value string
	}{
		{appendPath(provider, "authMode"), `0`}, {appendPath(provider, "authMode"), `2`}, {appendPath(provider, "authMode"), `"official-account"`},
		{appendPath(provider, "reasoningEffort"), `0`}, {appendPath(provider, "reasoningEffort"), `99`},
		{appendPath(model, "contextWindow"), `"2147483648"`}, {appendPath(model, "contextWindow"), `"18446744073709551616"`}, {appendPath(model, "contextWindow"), `1.5`},
		{appendPath(model, "contextWindow"), `0`}, {appendPath(model, "supportsParallelToolCalls"), `"false"`},
		{appendPath(model, "inputModalities"), `{}`}, {appendPath(model, "reasoningLevels"), `{}`},
		{appendPath(model, "inputModalities", "values"), `[1,1]`}, {appendPath(model, "inputModalities", "values"), `[0]`}, {appendPath(model, "inputModalities", "values"), `[99]`},
		{appendPath(model, "reasoningLevels", "values"), `[3,3]`}, {appendPath(model, "reasoningLevels", "values"), `[0]`},
		{[]string{"secrets", "0", "slotId"}, `"claude.api-key"`}, {[]string{"secrets", "0", "replace"}, `""`},
	} {
		data := codexJSONEdit(test, []byte(codexConfigurationJSON), entry.path, func([]byte) []byte { return []byte(entry.value) })
		check(test, "json", data, http.StatusBadRequest)
	}
	check(test, "json", bytes.ReplaceAll([]byte(codexConfigurationJSON), []byte(`"codexProvider"`), []byte(`"codex_provider"`)), http.StatusOK)
	check(test, "json", []byte(`{"draft":{"name":"x","codex_provider":null}}`), http.StatusBadRequest)
	check(test, "json", []byte(`{"draft":{"name":"x","codexProvider":{},"codex_provider":{}}}`), http.StatusBadRequest)
	check(test, "json", []byte(`{"draft":{"name":"x","codexProvider":{},"claudeProvider":{"authMode":1}}}`), http.StatusBadRequest)
	check(test, "json", bytes.Replace([]byte(codexConfigurationJSON), []byte("Protocol fixture"), []byte{0xff}, 1), http.StatusBadRequest)
	check(test, "json", []byte(`{"draft":{"name":"Claude null compatibility","claudeProvider":{"authMode":1,"endpoint":null}}}`), http.StatusOK)
	encoded, err := proto.Marshal(&configurationv1.CreateAssetRequest{Draft: codexConfigurationDraft()})
	if err != nil {
		test.Fatal("marshal generated positive fixture")
	}
	check(test, "proto", encoded, http.StatusOK)
	unpacked := codexWireEdit(test, encoded, []protowire.Number{1, 3, 5, 1, 5}, func([]byte) []byte { return []byte{8, 2, 8, 1} })
	unpacked = codexWireEdit(test, unpacked, []protowire.Number{1, 3, 5, 1, 7}, func([]byte) []byte { return []byte{8, 5, 8, 3} })
	check(test, "proto", unpacked, http.StatusOK)
	for _, path := range [][]protowire.Number{nil, {1}, {1, 3}, {1, 3, 5}, {1, 3, 5, 1}, {1, 3, 5, 1, 5}, {1, 3, 5, 1, 7}} {
		unknown := codexWireEdit(test, encoded, path, func(value []byte) []byte {
			return protowire.AppendVarint(protowire.AppendTag(value, 99, protowire.VarintType), 1)
		})
		check(test, "proto", unknown, http.StatusBadRequest)
	}
	for _, packed := range [][]byte{{10, 1, 99}, {10, 1, 128}, {10, 1, 0}, {8, 99}, {8, 0}, {10, 2, 1, 1}, {}, {10, 0}} {
		mutated := codexWireEdit(test, encoded, []protowire.Number{1, 3, 5, 1, 5}, func([]byte) []byte { return packed })
		check(test, "proto", mutated, http.StatusBadRequest)
	}
	duplicate := codexWireEdit(test, encoded, []protowire.Number{1, 3}, func(value []byte) []byte {
		return protowire.AppendVarint(protowire.AppendTag(value, 1, protowire.VarintType), 1)
	})
	check(test, "proto", duplicate, http.StatusBadRequest)
	oneof := codexWireEdit(test, encoded, []protowire.Number{1}, func(value []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(value, 2, protowire.BytesType), []byte{16, 1})
	})
	check(test, "proto", oneof, http.StatusBadRequest)
	check(test, "proto", encoded[:len(encoded)-1], http.StatusBadRequest)
}

func appendPath(path []string, names ...string) []string {
	return append(append([]string{}, path...), names...)
}

const codexPreAssetDescriptor = "CpIfCi1za2lsbGh1Yi9jb25maWd1cmF0aW9uL3YxL2NvbmZpZ3VyYXRpb24ucHJvdG8SGXNraWxsaHViLmNvbmZpZ3VyYXRpb24udjEikwIKBUFzc2V0Eg4KAmlkGAEgASgJUgJpZBIZCghhZ2VudF9pZBgCIAEoCVIHYWdlbnRJZBI4CgRraW5kGAMgASgOMiQuc2tpbGxodWIuY29uZmlndXJhdGlvbi52MS5Bc3NldEtpbmRSBGtpbmQSEgoEbmFtZRgEIAEoCVIEbmFtZRIlCg5zY2hlbWFfdmVyc2lvbhgFIAEoDVINc2NoZW1hVmVyc2lvbhIaCghyZXZpc2lvbhgGIAEoA1IIcmV2aXNpb24SJgoPY3JlYXRlZF9hdF91bml4GAcgASgDUg1jcmVhdGVkQXRVbml4EiYKD3VwZGF0ZWRfYXRfdW5peBgIIAEoA1INdXBkYXRlZEF0VW5peCK5AQoMQ2xhdWRlTW9kZWxzEigKDWRlZmF1bHRfbW9kZWwYASABKAlIAFIMZGVmYXVsdE1vZGVsiAEBEhkKBWhhaWt1GAIgASgJSAFSBWhhaWt1iAEBEhsKBnNvbm5ldBgDIAEoCUgCUgZzb25uZXSIAQESFwoEb3B1cxgEIAEoCUgDUgRvcHVziAEBQhAKDl9kZWZhdWx0X21vZGVsQggKBl9oYWlrdUIJCgdfc29ubmV0QgcKBV9vcHVzIscBCg5DbGF1ZGVQcm92aWRlchIfCghlbmRwb2ludBgBIAEoCUgAUghlbmRwb2ludIgBARJGCglhdXRoX21vZGUYAiABKA4yKS5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkNsYXVkZUF1dGhNb2RlUghhdXRoTW9kZRI/CgZtb2RlbHMYAyABKAsyJy5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkNsYXVkZU1vZGVsc1IGbW9kZWxzQgsKCV9lbmRwb2ludCJ8CgVEcmFmdBISCgRuYW1lGAEgASgJUgRuYW1lElQKD2NsYXVkZV9wcm92aWRlchgCIAEoCzIpLnNraWxsaHViLmNvbmZpZ3VyYXRpb24udjEuQ2xhdWRlUHJvdmlkZXJIAFIOY2xhdWRlUHJvdmlkZXJCCQoHcGF5bG9hZCJBCgxTZWNyZXRTdGF0dXMSFwoHc2xvdF9pZBgBIAEoCVIGc2xvdElkEhgKB3ByZXNlbnQYAiABKAhSB3ByZXNlbnQiDAoKS2VlcFNlY3JldCINCgtDbGVhclNlY3JldCLRAQoOU2VjcmV0TXV0YXRpb24SFwoHc2xvdF9pZBgBIAEoCVIGc2xvdElkEjsKBGtlZXAYAiABKAsyJS5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLktlZXBTZWNyZXRIAFIEa2VlcBIfCgdyZXBsYWNlGAMgASgJQgOAAQFIAFIHcmVwbGFjZRI+CgVjbGVhchgEIAEoCzImLnNraWxsaHViLmNvbmZpZ3VyYXRpb24udjEuQ2xlYXJTZWNyZXRIAFIFY2xlYXJCCAoGYWN0aW9uItMCCglEcmFmdFZpZXcSNgoFYXNzZXQYASABKAsyIC5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkFzc2V0UgVhc3NldBI2CgVkcmFmdBgCIAEoCzIgLnNraWxsaHViLmNvbmZpZ3VyYXRpb24udjEuRHJhZnRSBWRyYWZ0EkEKB3NlY3JldHMYAyADKAsyJy5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLlNlY3JldFN0YXR1c1IHc2VjcmV0cxJhChRjcmVkZW50aWFsX3JlYWRpbmVzcxgEIAEoDjIuLnNraWxsaHViLmNvbmZpZ3VyYXRpb24udjEuQ3JlZGVudGlhbFJlYWRpbmVzc1ITY3JlZGVudGlhbFJlYWRpbmVzcxIwChRoYXNfcHJlc2VydmVkX2V4dHJhcxgFIAEoCFISaGFzUHJlc2VydmVkRXh0cmFzIhMKEUxpc3RBc3NldHNSZXF1ZXN0Ik4KEkxpc3RBc3NldHNSZXNwb25zZRI4CgZhc3NldHMYASADKAsyIC5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkFzc2V0UgZhc3NldHMiIQoPR2V0QXNzZXRSZXF1ZXN0Eg4KAmlkGAEgASgJUgJpZCJKChBHZXRBc3NldFJlc3BvbnNlEjYKBWFzc2V0GAEgASgLMiAuc2tpbGxodWIuY29uZmlndXJhdGlvbi52MS5Bc3NldFIFYXNzZXQiIgoQUmVhZERyYWZ0UmVxdWVzdBIOCgJpZBgBIAEoCVICaWQiTQoRUmVhZERyYWZ0UmVzcG9uc2USOAoEdmlldxgBIAEoCzIkLnNraWxsaHViLmNvbmZpZ3VyYXRpb24udjEuRHJhZnRWaWV3UgR2aWV3IpEBChJDcmVhdGVBc3NldFJlcXVlc3QSNgoFZHJhZnQYASABKAsyIC5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkRyYWZ0UgVkcmFmdBJDCgdzZWNyZXRzGAIgAygLMikuc2tpbGxodWIuY29uZmlndXJhdGlvbi52MS5TZWNyZXRNdXRhdGlvblIHc2VjcmV0cyJPChNDcmVhdGVBc3NldFJlc3BvbnNlEjgKBHZpZXcYASABKAsyJC5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkRyYWZ0Vmlld1IEdmlldyLOAQoSVXBkYXRlQXNzZXRSZXF1ZXN0Eg4KAmlkGAEgASgJUgJpZBIrChFleHBlY3RlZF9yZXZpc2lvbhgCIAEoA1IQZXhwZWN0ZWRSZXZpc2lvbhI2CgVkcmFmdBgDIAEoCzIgLnNraWxsaHViLmNvbmZpZ3VyYXRpb24udjEuRHJhZnRSBWRyYWZ0EkMKB3NlY3JldHMYBCADKAsyKS5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLlNlY3JldE11dGF0aW9uUgdzZWNyZXRzIk8KE1VwZGF0ZUFzc2V0UmVzcG9uc2USOAoEdmlldxgBIAEoCzIkLnNraWxsaHViLmNvbmZpZ3VyYXRpb24udjEuRHJhZnRWaWV3UgR2aWV3In0KEENvcHlBc3NldFJlcXVlc3QSGwoJc291cmNlX2lkGAEgASgJUghzb3VyY2VJZBI4ChhleHBlY3RlZF9zb3VyY2VfcmV2aXNpb24YAiABKANSFmV4cGVjdGVkU291cmNlUmV2aXNpb24SEgoEbmFtZRgDIAEoCVIEbmFtZSJNChFDb3B5QXNzZXRSZXNwb25zZRI4CgR2aWV3GAEgASgLMiQuc2tpbGxodWIuY29uZmlndXJhdGlvbi52MS5EcmFmdFZpZXdSBHZpZXciUQoSRGVsZXRlQXNzZXRSZXF1ZXN0Eg4KAmlkGAEgASgJUgJpZBIrChFleHBlY3RlZF9yZXZpc2lvbhgCIAEoA1IQZXhwZWN0ZWRSZXZpc2lvbiIVChNEZWxldGVBc3NldFJlc3BvbnNlKqQBCglBc3NldEtpbmQSGgoWQVNTRVRfS0lORF9VTlNQRUNJRklFRBAAEhcKE0FTU0VUX0tJTkRfUFJPVklERVIQARISCg5BU1NFVF9LSU5EX01DUBACEhUKEUFTU0VUX0tJTkRfUFJPTVBUEAMSHQoZQVNTRVRfS0lORF9BR0VOVF9TRVRUSU5HUxAEEhgKFEFTU0VUX0tJTkRfV09SS1NQQUNFEAUqlAEKDkNsYXVkZUF1dGhNb2RlEiAKHENMQVVERV9BVVRIX01PREVfVU5TUEVDSUZJRUQQABIhCh1DTEFVREVfQVVUSF9NT0RFX05BVElWRV9MT0dJThABEhwKGENMQVVERV9BVVRIX01PREVfQVBJX0tFWRACEh8KG0NMQVVERV9BVVRIX01PREVfQVVUSF9UT0tFThADKokBChNDcmVkZW50aWFsUmVhZGluZXNzEiQKIENSRURFTlRJQUxfUkVBRElORVNTX1VOU1BFQ0lGSUVEEAASIQodQ1JFREVOVElBTF9SRUFESU5FU1NfQ09NUExFVEUQARIpCiVDUkVERU5USUFMX1JFQURJTkVTU19ORUVEU19DUkVERU5USUFMEAIygAYKFENvbmZpZ3VyYXRpb25TZXJ2aWNlEmkKCkxpc3RBc3NldHMSLC5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkxpc3RBc3NldHNSZXF1ZXN0Gi0uc2tpbGxodWIuY29uZmlndXJhdGlvbi52MS5MaXN0QXNzZXRzUmVzcG9uc2USYwoIR2V0QXNzZXQSKi5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkdldEFzc2V0UmVxdWVzdBorLnNraWxsaHViLmNvbmZpZ3VyYXRpb24udjEuR2V0QXNzZXRSZXNwb25zZRJmCglSZWFkRHJhZnQSKy5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLlJlYWREcmFmdFJlcXVlc3QaLC5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLlJlYWREcmFmdFJlc3BvbnNlEmwKC0NyZWF0ZUFzc2V0Ei0uc2tpbGxodWIuY29uZmlndXJhdGlvbi52MS5DcmVhdGVBc3NldFJlcXVlc3QaLi5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkNyZWF0ZUFzc2V0UmVzcG9uc2USbAoLVXBkYXRlQXNzZXQSLS5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLlVwZGF0ZUFzc2V0UmVxdWVzdBouLnNraWxsaHViLmNvbmZpZ3VyYXRpb24udjEuVXBkYXRlQXNzZXRSZXNwb25zZRJmCglDb3B5QXNzZXQSKy5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkNvcHlBc3NldFJlcXVlc3QaLC5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkNvcHlBc3NldFJlc3BvbnNlEmwKC0RlbGV0ZUFzc2V0Ei0uc2tpbGxodWIuY29uZmlndXJhdGlvbi52MS5EZWxldGVBc3NldFJlcXVlc3QaLi5za2lsbGh1Yi5jb25maWd1cmF0aW9uLnYxLkRlbGV0ZUFzc2V0UmVzcG9uc2VCR1pFZ2l0aHViLmNvbS9jYS14L2NvZ25hdGUvZ2VuL3NraWxsaHViL2NvbmZpZ3VyYXRpb24vdjE7Y29uZmlndXJhdGlvbnYxYgZwcm90bzM="

func TestCodexConfigurationOldDescriptorCompatibility(test *testing.T) {
	data, err := base64.StdEncoding.DecodeString(codexPreAssetDescriptor)
	if err != nil {
		test.Fatal("decode pinned old descriptor")
	}
	set := &descriptorpb.FileDescriptorSet{}
	if proto.Unmarshal(data, set) != nil {
		test.Fatal("read pinned old descriptor")
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		test.Fatal("resolve pinned old descriptor")
	}
	message := func(name string) *dynamicpb.Message {
		descriptor, err := files.FindDescriptorByName(protoreflect.FullName("skillhub.configuration.v1." + name))
		if err != nil {
			test.Fatal("old descriptor missing message")
		}
		return dynamicpb.NewMessage(descriptor.(protoreflect.MessageDescriptor))
	}
	if message("Draft").Descriptor().Fields().ByNumber(3) != nil {
		test.Fatal("pinned descriptor is not pre-Codex")
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		format := strings.TrimPrefix(request.Header.Get("Content-Type"), "application/")
		data, err := io.ReadAll(io.LimitReader(request.Body, 128<<10))
		old := message("CreateAssetRequest")
		codec := configurationCodec{name: format}
		if err != nil || codec.Unmarshal(data, old) != nil {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, `{"code":"invalid_argument","message":"invalid configuration request"}`)
			return
		}
		response := message("CreateAssetResponse")
		view := response.Mutable(response.Descriptor().Fields().ByName("view")).Message()
		view.Set(view.Descriptor().Fields().ByName("draft"), old.Get(old.Descriptor().Fields().ByName("draft")))
		encoded, err := codec.Marshal(response)
		if err != nil {
			test.Error("legacy response encoding failed")
			writer.WriteHeader(500)
			return
		}
		writer.Header().Set("Content-Type", "application/"+format)
		_, _ = writer.Write(encoded)
	}))
	test.Cleanup(endpoint.Close)
	for _, options := range [][]connect.ClientOption{nil, {connect.WithProtoJSON()}} {
		client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
		draft := configurationDraft("Old descriptor Claude")
		response, err := client.CreateAsset(test.Context(), connect.NewRequest(&configurationv1.CreateAssetRequest{Draft: draft}))
		if err != nil || !proto.Equal(response.Msg.View.Draft, draft) {
			test.Fatal("new Claude client cannot communicate with fixed old descriptor server")
		}
		if _, err := client.CreateAsset(test.Context(), connect.NewRequest(&configurationv1.CreateAssetRequest{Draft: codexConfigurationDraft()})); connect.CodeOf(err) != connect.CodeInvalidArgument {
			test.Fatal("legacy server silently accepted new variant")
		}
	}
	encoded, _ := proto.Marshal(codexConfigurationDraft())
	oldDraft := message("Draft")
	if proto.Unmarshal(encoded, oldDraft) != nil || oldDraft.WhichOneof(oldDraft.Descriptor().Oneofs().ByName("payload")) != nil || len(oldDraft.GetUnknown()) == 0 {
		test.Fatal("old descriptor misinterpreted new branch as Claude")
	}
}

func TestCodexConfigurationProtocolNull(test *testing.T) {
	service, store := testServiceAndStore(test)
	_, session := mcpSession(test, service, "codex-null")
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Configuration: configuration.New(store)}))
	test.Cleanup(endpoint.Close)
	data := `{"draft":{"name":"Null fixture","codexProvider":{"authMode":1,"endpoint":"https://api.example.test/v1","model":"test","reasoningEffort":null}}}`
	status, body := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", []byte(data))
	if status != http.StatusBadRequest || strings.Contains(body, "Null fixture") {
		test.Fatalf("explicit Codex null accepted: status %d", status)
	}
}
