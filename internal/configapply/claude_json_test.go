package configapply_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configapply"
)

func nativePatch() agentconfig.ClaudeProviderPatchV1 {
	return agentconfig.ClaudeProviderPatchV1{Target: agentconfig.ClaudeSettings, Operations: []agentconfig.ClaudeProviderPatchOperation{
		{Slot: agentconfig.ClaudeEnvAPIKey, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvAuthToken, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvBaseURL, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvModel, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvHaikuModel, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvSonnetModel, Action: agentconfig.ClaudeRemove},
		{Slot: agentconfig.ClaudeEnvOpusModel, Action: agentconfig.ClaudeRemove},
	}}
}

func selectedPatch() agentconfig.ClaudeProviderPatchV1 {
	patch := nativePatch()
	patch.Operations[0].Action, patch.Operations[0].Reference = agentconfig.ClaudeSetSelectedSecret, new(agentconfig.ClaudeAPIKeySecret)
	for index, value := range []string{"https://provider.invalid/v1", "Default", "Haiku", "Sonnet", "Opus"} {
		patch.Operations[index+2].Action, patch.Operations[index+2].Literal = agentconfig.ClaudeSetLiteral, new(value)
	}
	return patch
}

func selectedCredentials(test *testing.T) map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial {
	test.Helper()
	secret, err := configapply.NewSecretMaterial([]byte("synthetic-credential-claude-local-only"))
	if err != nil {
		test.Fatal("valid synthetic credential rejected")
	}
	return map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret}
}

func TestClaudeNativeLoginPreservesRawTokens(test *testing.T) {
	base := []byte(" \r\n" + `{"x":[900719925474099312345,"\u00e9"],"e\u006ev":{ "KEEP" : {"v":1e+004}, "ANTHROPIC_API_KEY":"old", "ANTHROPIC_MODEL":"old" }}` + "\n")
	want := []byte(" \r\n" + `{"x":[900719925474099312345,"\u00e9"],"e\u006ev":{ "KEEP" : {"v":1e+004} }}` + "\n")
	before := bytes.Clone(base)
	image, err := configapply.RewriteClaudeSettings(base, nativePatch(), nil)
	if err != nil || !bytes.Equal(image.CopyBytes(), want) || !bytes.Equal(base, before) {
		test.Fatal("native-login did not remove managed fields while retaining exact unrelated tokens")
	}
}

func TestClaudeSwitchSevenSlotsAndRoundTrip(test *testing.T) {
	base := []byte(`{"env":{"KEEP":[1,900719925474099312345],"ANTHROPIC_AUTH_TOKEN":"old"},"unknown":"\u00e9"}`)
	credentials := selectedCredentials(test)
	image, err := configapply.RewriteClaudeSettings(base, selectedPatch(), credentials)
	want := `{"env":{"KEEP":[1,900719925474099312345],"ANTHROPIC_API_KEY":"synthetic-credential-claude-local-only","ANTHROPIC_BASE_URL":"https://provider.invalid/v1","ANTHROPIC_MODEL":"Default","ANTHROPIC_DEFAULT_HAIKU_MODEL":"Haiku","ANTHROPIC_DEFAULT_SONNET_MODEL":"Sonnet","ANTHROPIC_DEFAULT_OPUS_MODEL":"Opus"},"unknown":"\u00e9"}`
	if err != nil || !bytes.Equal(image.CopyBytes(), []byte(want)) {
		test.Fatal("seven-slot switch failed or changed unrelated bytes")
	}
	second, err := configapply.RewriteClaudeSettings(image.CopyBytes(), selectedPatch(), credentials)
	if err != nil || !bytes.Equal(image.CopyBytes(), second.CopyBytes()) {
		test.Fatal("repeated in-memory rewrite was not byte stable")
	}
	restored, err := configapply.RewriteClaudeSettings(image.CopyBytes(), nativePatch(), nil)
	if err != nil || !bytes.Equal(restored.CopyBytes(), []byte(`{"env":{"KEEP":[1,900719925474099312345]},"unknown":"\u00e9"}`)) {
		test.Fatal("native-login did not remove all seven overrides")
	}
}

func assertRewriteFailure(test *testing.T, base []byte, patch agentconfig.ClaudeProviderPatchV1, credentials map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial, expected error) {
	test.Helper()
	before := bytes.Clone(base)
	image, err := configapply.RewriteClaudeSettings(base, patch, credentials)
	if !errors.Is(err, expected) || image.CopyBytes() != nil || !bytes.Equal(base, before) {
		test.Fatal("rewrite failure classification, zero result, or input isolation violated")
	}
}

func TestClaudeRejectsInvalidSettingsWithoutPartialOutput(test *testing.T) {
	for index, base := range []string{
		"", " ", "null", "[]", `{"env":null}`, `{"env":[]}`, `{"env":true}`,
		`{"env":{"ANTHROPIC_MODEL":null}}`, `{"env":{"ANTHROPIC_AUTH_TOKEN":42}}`,
		`{"a":1,"a":2}`, `{"env":{},"e\u006ev":{}}`,
		`{"x":{"deep":[{"a":1,"\u0061":2}]}}`,
		`{"env":{"KEEP":0,"\u004bEEP":1}}`,
		`{"env":{}} {}`, `{"x":0}tail`, `{"env":{},}`, "{\"x\":\"\xff\"}",
		`{"x":"\ud800"}`, `{"env":{"KEEP":"\udfff"}}`, `{"env":{"KEEP":1,}}`,
		`{/* JSONC */"env":{}}`, `{"x":[[[}`, `{"env":{"KEEP":01}}`,
	} {
		test.Run(fmt.Sprintf("case-%d", index), func(test *testing.T) {
			assertRewriteFailure(test, []byte(base), nativePatch(), nil, configapply.ErrInvalidSettings)
		})
	}
}

func TestClaudePatchIndependentValidation(test *testing.T) {
	for _, invalid := range []struct {
		name   string
		mutate func(*agentconfig.ClaudeProviderPatchV1)
	}{
		{"target", func(patch *agentconfig.ClaudeProviderPatchV1) { patch.Target = agentconfig.CodexConfig }},
		{"missing", func(patch *agentconfig.ClaudeProviderPatchV1) { patch.Operations = patch.Operations[:6] }},
		{"extra", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations = append(patch.Operations, patch.Operations[0])
		}},
		{"duplicate", func(patch *agentconfig.ClaudeProviderPatchV1) { patch.Operations[6].Slot = patch.Operations[5].Slot }},
		{"order", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[3], patch.Operations[4] = patch.Operations[4], patch.Operations[3]
		}},
		{"unknown-slot", func(patch *agentconfig.ClaudeProviderPatchV1) { patch.Operations[6].Slot = "OTHER" }},
		{"unknown-action", func(patch *agentconfig.ClaudeProviderPatchV1) { patch.Operations[0].Action = "execute" }},
		{"literal-auth", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[0].Action, patch.Operations[0].Literal = agentconfig.ClaudeSetLiteral, new("private")
		}},
		{"remove-empty-literal", func(patch *agentconfig.ClaudeProviderPatchV1) { patch.Operations[0].Literal = new("") }},
		{"remove-empty-reference", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[0].Reference = new(agentconfig.ClaudeSecretSlot(""))
		}},
		{"missing-literal", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[3].Action = agentconfig.ClaudeSetLiteral
		}},
		{"present-empty-literal", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[3].Action, patch.Operations[3].Literal = agentconfig.ClaudeSetLiteral, new("")
		}},
		{"literal-with-reference", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[3].Action, patch.Operations[3].Literal, patch.Operations[3].Reference = agentconfig.ClaudeSetLiteral, new("Model"), new(agentconfig.ClaudeAPIKeySecret)
		}},
		{"missing-reference", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[0].Action = agentconfig.ClaudeSetSelectedSecret
		}},
		{"wrong-reference", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[0].Action, patch.Operations[0].Reference = agentconfig.ClaudeSetSelectedSecret, new(agentconfig.ClaudeAuthTokenSecret)
		}},
		{"secret-with-literal", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[0].Action, patch.Operations[0].Reference, patch.Operations[0].Literal = agentconfig.ClaudeSetSelectedSecret, new(agentconfig.ClaudeAPIKeySecret), new("private")
		}},
		{"model-reference", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[3].Action, patch.Operations[3].Reference = agentconfig.ClaudeSetSelectedSecret, new(agentconfig.ClaudeAPIKeySecret)
		}},
		{"native-endpoint", func(patch *agentconfig.ClaudeProviderPatchV1) {
			patch.Operations[2].Action, patch.Operations[2].Literal = agentconfig.ClaudeSetLiteral, new("https://provider.invalid")
		}},
	} {
		test.Run(invalid.name, func(test *testing.T) {
			patch := nativePatch()
			invalid.mutate(&patch)
			assertRewriteFailure(test, []byte(`{"env":{"KEEP":"private"}}`), patch, nil, configapply.ErrInvalidPatch)
		})
	}
	for _, value := range []string{"", "relative", "http:opaque", "https://", "ftp://provider.invalid", "https://user:private@provider.invalid", "https://provider.invalid?", "https://provider.invalid#", "https://provider.invalid\n", "https://provider.invalid/" + strings.Repeat("x", 2048), "https://provider.invalid/\x7f", "\xff"} {
		patch := selectedPatch()
		patch.Operations[2].Literal = new(value)
		assertRewriteFailure(test, []byte(`{}`), patch, selectedCredentials(test), configapply.ErrInvalidPatch)
	}
	for _, value := range []string{"", strings.Repeat("é", 129), "bad\n", "bad\u0085", "\xff"} {
		patch := selectedPatch()
		patch.Operations[3].Literal = new(value)
		assertRewriteFailure(test, []byte(`{}`), patch, selectedCredentials(test), configapply.ErrInvalidPatch)
	}
}

func TestClaudeExactCredentialConsumption(test *testing.T) {
	credentials := selectedCredentials(test)
	assertRewriteFailure(test, []byte(`{}`), selectedPatch(), nil, configapply.ErrInvalidSecret)
	assertRewriteFailure(test, []byte(`{}`), nativePatch(), credentials, configapply.ErrInvalidSecret)
	for _, slot := range []agentconfig.ClaudeSecretSlot{agentconfig.ClaudeAuthTokenSecret, "unknown"} {
		credentials := selectedCredentials(test)
		credentials[slot] = credentials[agentconfig.ClaudeAPIKeySecret]
		assertRewriteFailure(test, []byte(`{}`), selectedPatch(), credentials, configapply.ErrInvalidSecret)
	}
	assertRewriteFailure(test, []byte(`{}`), selectedPatch(), map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial{agentconfig.ClaudeAPIKeySecret: {}}, configapply.ErrInvalidSecret)
	patch := selectedPatch()
	patch.Operations[1].Action, patch.Operations[1].Reference = agentconfig.ClaudeSetSelectedSecret, new(agentconfig.ClaudeAuthTokenSecret)
	credentials[agentconfig.ClaudeAuthTokenSecret] = credentials[agentconfig.ClaudeAPIKeySecret]
	assertRewriteFailure(test, []byte(`{}`), patch, credentials, configapply.ErrInvalidPatch)
	patch = nativePatch()
	patch.Operations[1].Action, patch.Operations[1].Reference = agentconfig.ClaudeSetSelectedSecret, new(agentconfig.ClaudeAuthTokenSecret)
	image, err := configapply.RewriteClaudeSettings([]byte(`{"env":{"ANTHROPIC_API_KEY":"old"}}`), patch, map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial{agentconfig.ClaudeAuthTokenSecret: credentials[agentconfig.ClaudeAuthTokenSecret]})
	if err != nil || !bytes.Equal(image.CopyBytes(), []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"synthetic-credential-claude-local-only"}}`)) {
		test.Fatal("auth-token switch failed to remove old API key")
	}
}

func TestClaudeRawSpanDeletionAndInsertion(test *testing.T) {
	for _, example := range []struct{ base, want string }{
		{" \n{\t}\r\n", " \n{\t}\r\n"},
		{`{"env": { "ANTHROPIC_API_KEY" : "old" }}`, `{"env": {  }}`},
		{`{"env":{"ANTHROPIC_API_KEY":"old", "KEEP":true}}`, `{"env":{"KEEP":true}}`},
		{`{"env":{"A":1, "ANTHROPIC_MODEL":"old", "B":2}}`, `{"env":{"A":1, "B":2}}`},
		{`{"env":{"ANTHROPIC_API_\u004bEY":"old","KEEP":{"nested":[null,false]}}}`, `{"env":{"KEEP":{"nested":[null,false]}}}`},
	} {
		image, err := configapply.RewriteClaudeSettings([]byte(example.base), nativePatch(), nil)
		if err != nil || !bytes.Equal(image.CopyBytes(), []byte(example.want)) {
			test.Fatal("raw-span deletion modified unrelated tokens or failed")
		}
	}
	patch := nativePatch()
	patch.Operations[4].Action, patch.Operations[4].Literal = agentconfig.ClaudeSetLiteral, new("Haiku")
	image, err := configapply.RewriteClaudeSettings([]byte(" {\r\n\t\"x\" : 1e+04\r\n} \n"), patch, nil)
	if err != nil || !bytes.Equal(image.CopyBytes(), []byte(" {\r\n\t\"x\" : 1e+04,\"env\":{\"ANTHROPIC_DEFAULT_HAIKU_MODEL\":\"Haiku\"}\r\n} \n")) {
		test.Fatal("env insertion did not preserve raw root tokens")
	}
}

func numberedMembers(count int) string {
	var output strings.Builder
	for index := range count {
		if index != 0 {
			output.WriteByte(',')
		}
		fmt.Fprintf(&output, `"k%d":0`, index)
	}
	return output.String()
}

func TestClaudeInputAndFinalBudgets(test *testing.T) {
	credentials := selectedCredentials(test)
	accept := func(base string, patch agentconfig.ClaudeProviderPatchV1, credentials map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial) {
		test.Helper()
		image, err := configapply.RewriteClaudeSettings([]byte(base), patch, credentials)
		if err != nil || !jsontext.Value(image.CopyBytes()).IsValid() {
			test.Fatal("valid exact-boundary image rejected")
		}
	}
	accept("{"+numberedMembers(512)+"}", nativePatch(), nil)
	assertRewriteFailure(test, []byte("{"+numberedMembers(513)+"}"), nativePatch(), nil, configapply.ErrSettingsLimit)
	accept("{"+numberedMembers(511)+"}", selectedPatch(), credentials)
	assertRewriteFailure(test, []byte("{"+numberedMembers(512)+"}"), selectedPatch(), credentials, configapply.ErrSettingsLimit)
	accept(`{"env":{},`+numberedMembers(511)+"}", selectedPatch(), credentials)
	accept(`{"env":{`+numberedMembers(128)+"}}", nativePatch(), nil)
	assertRewriteFailure(test, []byte(`{"env":{`+numberedMembers(129)+"}}"), nativePatch(), nil, configapply.ErrSettingsLimit)
	accept(`{"env":{`+numberedMembers(122)+"}}", selectedPatch(), credentials)
	assertRewriteFailure(test, []byte(`{"env":{`+numberedMembers(123)+"}}"), selectedPatch(), credentials, configapply.ErrSettingsLimit)
	accept(`{"env":{"KEEP":"`+strings.Repeat(`\u0061`, 8192)+`"}}`, nativePatch(), nil)
	assertRewriteFailure(test, []byte(`{"env":{"KEEP":"`+strings.Repeat(`\u0061`, 8193)+`"}}`), nativePatch(), nil, configapply.ErrSettingsLimit)
	accept(`{"env":{"KEEP":[`+strings.Repeat(" ", 8190)+"]}}", nativePatch(), nil)
	assertRewriteFailure(test, []byte(`{"env":{"KEEP":[`+strings.Repeat(" ", 8191)+"]}}"), nativePatch(), nil, configapply.ErrSettingsLimit)
	for _, size := range []int{agentconfig.MaxEnvironmentBytes, agentconfig.MaxEnvironmentBytes + 1} {
		base := `{"env":{` + strings.Repeat(" ", size-2) + "}}"
		if size == agentconfig.MaxEnvironmentBytes {
			accept(base, nativePatch(), nil)
			assertRewriteFailure(test, []byte(base), selectedPatch(), credentials, configapply.ErrSettingsLimit)
		} else {
			assertRewriteFailure(test, []byte(base), nativePatch(), nil, configapply.ErrSettingsLimit)
		}
	}
	wantEnv := `{"ANTHROPIC_API_KEY":"synthetic-credential-claude-local-only","ANTHROPIC_BASE_URL":"https://provider.invalid/v1","ANTHROPIC_MODEL":"Default","ANTHROPIC_DEFAULT_HAIKU_MODEL":"Haiku","ANTHROPIC_DEFAULT_SONNET_MODEL":"Sonnet","ANTHROPIC_DEFAULT_OPUS_MODEL":"Opus"}`
	accept(`{"env":{`+strings.Repeat(" ", agentconfig.MaxEnvironmentBytes-len(wantEnv))+"}}", selectedPatch(), credentials)
	assertRewriteFailure(test, []byte(`{"env":{`+strings.Repeat(" ", agentconfig.MaxEnvironmentBytes-len(wantEnv)+1)+"}}"), selectedPatch(), credentials, configapply.ErrSettingsLimit)
	accept(strings.Repeat(" ", agentconfig.MaxConfigBytes-2)+"{}", nativePatch(), nil)
	assertRewriteFailure(test, []byte(strings.Repeat(" ", agentconfig.MaxConfigBytes-1)+"{}"), nativePatch(), nil, configapply.ErrSettingsLimit)
	finalRootSize := len(`{"env":` + wantEnv + "}")
	accept(strings.Repeat(" ", agentconfig.MaxConfigBytes-finalRootSize)+"{}", selectedPatch(), credentials)
	assertRewriteFailure(test, []byte(strings.Repeat(" ", agentconfig.MaxConfigBytes-finalRootSize+1)+"{}"), selectedPatch(), credentials, configapply.ErrSettingsLimit)
	patch := selectedPatch()
	prefix := "https://provider.invalid/"
	patch.Operations[2].Literal, patch.Operations[3].Literal = new(prefix+strings.Repeat("x", 2048-len(prefix))), new(strings.Repeat("é", 128))
	accept("{}", patch, credentials)
}

func TestClaudeWrappersRedactAndIsolateCopies(test *testing.T) {
	for _, invalid := range [][]byte{nil, {}, {255}, []byte("bad\x00"), []byte("bad\r"), []byte("bad\n"), bytes.Repeat([]byte{'x'}, 8193)} {
		material, err := configapply.NewSecretMaterial(invalid)
		if !errors.Is(err, configapply.ErrInvalidSecret) || material.CopyBytes() != nil {
			test.Fatal("invalid secret accepted or returned partial bytes")
		}
	}
	for _, valid := range [][]byte{[]byte(" "), bytes.Repeat([]byte{'x'}, 8192), []byte("tab\tallowed")} {
		material, err := configapply.NewSecretMaterial(valid)
		if err != nil || !bytes.Equal(material.CopyBytes(), valid) {
			test.Fatal("valid secret changed or rejected")
		}
	}
	input := []byte("synthetic-private-wrapper-sentinel")
	material, err := configapply.NewSecretMaterial(input)
	if err != nil {
		test.Fatal("valid secret rejected")
	}
	input[0] ^= 1
	if string(material.CopyBytes()) != "synthetic-private-wrapper-sentinel" {
		test.Fatal("constructor retained input alias")
	}
	base, patch := []byte(`{"unknown":"unchanged"}`), selectedPatch()
	credentials := map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial{agentconfig.ClaudeAPIKeySecret: material}
	image, err := configapply.RewriteClaudeSettings(base, patch, credentials)
	if err != nil {
		test.Fatal("valid image rejected")
	}
	before := image.CopyBytes()
	base[0] ^= 1
	*patch.Operations[3].Literal = "changed"
	delete(credentials, agentconfig.ClaudeAPIKeySecret)
	revealed := image.CopyBytes()
	revealed[0] ^= 1
	secretCopy := material.CopyBytes()
	secretCopy[0] ^= 1
	if !bytes.Equal(before, image.CopyBytes()) || string(material.CopyBytes()) != "synthetic-private-wrapper-sentinel" {
		test.Fatal("returned wrapper retained a mutable external alias")
	}
	for _, value := range []any{material, &material, image, &image, struct{ Image configapply.SettingsImage }{image}} {
		for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
			if strings.Contains(fmt.Sprintf(format, value), "synthetic-private-wrapper-sentinel") {
				test.Fatal("default formatting exposed material")
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil || bytes.Contains(encoded, []byte("synthetic-private-wrapper-sentinel")) {
			test.Fatal("default JSON exposed material")
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("fixture", "value", value)
		if bytes.Contains(log.Bytes(), []byte("synthetic-private-wrapper-sentinel")) {
			test.Fatal("default slog exposed material")
		}
	}
	for _, value := range []interface{ MarshalText() ([]byte, error) }{material, &material, image, &image} {
		encoded, err := value.MarshalText()
		if err != nil || bytes.Contains(encoded, []byte("synthetic-private-wrapper-sentinel")) {
			test.Fatal("default text exposed material")
		}
	}
	if reflect.DeepEqual(image, configapply.SettingsImage{}) {
		test.Fatal("valid rewrite returned zero image")
	}
}

func TestClaudeWrappersFormatFixedMarkers(test *testing.T) {
	materials := []configapply.SecretMaterial{{}}
	images := []configapply.SettingsImage{{}}
	for _, size := range []int{1, 37, 8192} {
		material, err := configapply.NewSecretMaterial(bytes.Repeat([]byte{'K'}, size))
		if err != nil {
			test.Fatal("synthetic material rejected")
		}
		image, err := configapply.RewriteClaudeSettings([]byte(`{}`), selectedPatch(), map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial{agentconfig.ClaudeAPIKeySecret: material})
		if err != nil {
			test.Fatal("synthetic image rejected")
		}
		materials = append(materials, material)
		images = append(images, image)
	}
	for _, wrapper := range []string{"secret", "image"} {
		for _, shape := range []string{"value", "pointer", "struct", "typed-struct", "pointer-field", "nested", "slice", "array", "map"} {
			for _, verb := range "vtsqdboxXOcUeEfFgGz" {
				test.Run(wrapper+"/"+shape+"/"+string(verb), func(test *testing.T) {
					for _, flags := range []string{"", "#", "+", "-", " ", "0", "#0+- "} {
						for _, dimensions := range []string{"", "1", "64", ".0", ".1", ".64", "64.1", "1.64", "*.*"} {
							format := "%" + flags + dimensions + string(verb)
							var baseline string
							for index, material := range materials {
								image := images[index]
								var value any = material
								var pointer any = &material
								marker := "[redacted secret material]"
								if wrapper == "image" {
									value, pointer, marker = image, &image, "[redacted settings image]"
								}
								switch shape {
								case "pointer":
									value = pointer
								case "struct":
									value = struct{ Value any }{value}
								case "typed-struct":
									value = struct {
										Value   configapply.SecretMaterial
										Pointer *configapply.SecretMaterial
									}{material, &material}
									if wrapper == "image" {
										value = struct {
											Value   configapply.SettingsImage
											Pointer *configapply.SettingsImage
										}{image, &image}
									}
								case "pointer-field":
									value = &struct{ Value any }{pointer}
								case "nested":
									value = struct{ Values []any }{[]any{struct{ Value any }{value}, pointer}}
								case "slice":
									value = []any{value, pointer}
								case "array":
									value = [2]any{value, pointer}
								case "map":
									value = map[string]any{"value": value, "pointer": pointer}
								}
								arguments := []any{value}
								if dimensions == "*.*" {
									arguments = []any{64, 1, value}
								}
								output := fmt.Sprintf(format, arguments...)
								if !strings.Contains(output, marker) {
									test.Fatal("formatting did not preserve fixed redaction marker")
								}
								if (shape == "value" || shape == "pointer") && output != marker {
									test.Fatal("direct formatting emitted more than the fixed marker")
								}
								if index == 0 {
									baseline = output
								} else if output != baseline {
									test.Fatal("formatting depended on private payload or its length")
								}
							}
						}
					}
				})
			}
		}
	}
}

func TestClaudeWrappersFormatTypeAndAddress(test *testing.T) {
	material, err := configapply.NewSecretMaterial([]byte("synthetic-format-only"))
	if err != nil {
		test.Fatal("synthetic material rejected")
	}
	image, err := configapply.RewriteClaudeSettings([]byte(`{}`), selectedPatch(), map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial{agentconfig.ClaudeAPIKeySecret: material})
	if err != nil {
		test.Fatal("synthetic image rejected")
	}
	for _, fixture := range []struct {
		value    any
		pointer  any
		typeName string
	}{
		{material, &material, "configapply.SecretMaterial"},
		{image, &image, "configapply.SettingsImage"},
	} {
		for _, flags := range []string{"", "#", "+", "-", " ", "0"} {
			for _, dimensions := range []string{"", "64", ".1", "64.1"} {
				format := "%" + flags + dimensions
				if fmt.Sprintf(format+"T", fixture.value) != fmt.Sprintf(format+"s", fixture.typeName) || fmt.Sprintf(format+"T", fixture.pointer) != fmt.Sprintf(format+"s", "*"+fixture.typeName) {
					test.Fatal("type formatting emitted more than type metadata")
				}
				if fmt.Sprintf(format+"p", fixture.pointer) != fmt.Sprintf(format+"p", reflect.ValueOf(fixture.pointer).UnsafePointer()) {
					test.Fatal("address formatting emitted more than wrapper address metadata")
				}
			}
		}
	}
}

func TestClaudePrivateContainersDoNotExposePayload(test *testing.T) {
	for _, size := range []int{0, 37, 128, 8192} {
		var material configapply.SecretMaterial
		var image configapply.SettingsImage
		if size > 0 {
			var err error
			material, err = configapply.NewSecretMaterial(bytes.Repeat([]byte{'K'}, size))
			if err != nil {
				test.Fatal("synthetic material rejected")
			}
			image, err = configapply.RewriteClaudeSettings([]byte(`{}`), selectedPatch(), map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial{agentconfig.ClaudeAPIKeySecret: material})
			if err != nil {
				test.Fatal("synthetic image rejected")
			}
		}
		var fingerprints []string
		for _, payload := range [][]byte{material.CopyBytes(), image.CopyBytes()} {
			if len(payload) == 0 {
				continue
			}
			for _, format := range []string{"%s", "%q", "%x", "%X", "%v", "%#v", "%d", "%b", "%o", "%O", "%c", "%U"} {
				encoded := fmt.Sprintf(format, payload)
				if format == "%#v" {
					encoded = strings.TrimSuffix(encoded[strings.IndexByte(encoded, '{')+1:], "}")
				} else if strings.HasPrefix(encoded, "[") {
					encoded = strings.TrimSuffix(strings.TrimPrefix(encoded, "["), "]")
				}
				fingerprints = append(fingerprints, encoded)
			}
		}
		for name, private := range map[string]any{
			"secret":       struct{ private configapply.SecretMaterial }{material},
			"image":        struct{ private configapply.SettingsImage }{image},
			"secret-slice": struct{ private []configapply.SecretMaterial }{[]configapply.SecretMaterial{material}},
			"image-slice":  struct{ private []configapply.SettingsImage }{[]configapply.SettingsImage{image}},
			"secret-array": struct{ private [1]configapply.SecretMaterial }{[1]configapply.SecretMaterial{material}},
			"image-array":  struct{ private [1]configapply.SettingsImage }{[1]configapply.SettingsImage{image}},
			"secret-map": struct {
				private map[string]configapply.SecretMaterial
			}{map[string]configapply.SecretMaterial{"value": material}},
			"image-map": struct {
				private map[string]configapply.SettingsImage
			}{map[string]configapply.SettingsImage{"value": image}},
			"secret-pointer":     struct{ private *configapply.SecretMaterial }{&material},
			"image-pointer":      struct{ private *configapply.SettingsImage }{&image},
			"nil-secret-pointer": struct{ private *configapply.SecretMaterial }{},
			"nil-image-pointer":  struct{ private *configapply.SettingsImage }{},
		} {
			for shape, container := range map[string]any{
				"value":             private,
				"pointer":           &private,
				"private-interface": struct{ private any }{private},
				"private-struct":    struct{ private struct{ Public any } }{struct{ Public any }{private}},
				"private-slice":     struct{ private []any }{[]any{private}},
				"private-array":     struct{ private [1]any }{[1]any{private}},
				"private-map":       struct{ private map[string]any }{map[string]any{"value": private}},
				"public-slice":      []any{private},
			} {
				test.Run(fmt.Sprintf("size-case-%d/%s/%s", size, name, shape), func(test *testing.T) {
					activeFormat := ""
					assertOpaque := func(output string) {
						test.Helper()
						for _, fingerprint := range fingerprints {
							if strings.Contains(output, fingerprint) {
								test.Fatalf("private container exposed reversible payload bytes through %s", activeFormat)
							}
						}
						if strings.Contains(output, "[]uint8") || strings.Contains(output, "[]byte") {
							test.Fatal("private container exposed byte storage")
						}
					}
					for _, verb := range "vtsqdboxXOcUeEfFgGzawTp" {
						for _, flags := range []string{"", "#", "+", "-", " ", "0", "#0+- "} {
							for _, dimensions := range []string{"", "64", ".1", "64.1"} {
								activeFormat = "%" + flags + dimensions + string(verb)
								assertOpaque(fmt.Sprintf(activeFormat, container))
							}
						}
					}
					for _, handler := range []func(*bytes.Buffer) slog.Handler{
						func(output *bytes.Buffer) slog.Handler { return slog.NewTextHandler(output, nil) },
						func(output *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(output, nil) },
					} {
						var output bytes.Buffer
						slog.New(handler(&output)).Info("fixture", "value", container)
						assertOpaque(output.String())
					}
				})
			}
		}
	}
}

func TestClaudeZeroWrappersAndCopiedValues(test *testing.T) {
	var material configapply.SecretMaterial
	var image configapply.SettingsImage
	if material.CopyBytes() != nil || image.CopyBytes() != nil {
		test.Fatal("zero wrapper returned payload")
	}
	assertRewriteFailure(test, []byte(`{}`), selectedPatch(), map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial{agentconfig.ClaudeAPIKeySecret: material}, configapply.ErrInvalidSecret)
	material, err := configapply.NewSecretMaterial([]byte("synthetic-copy-only"))
	if err != nil {
		test.Fatal("synthetic material rejected")
	}
	image, err = configapply.RewriteClaudeSettings([]byte(`{}`), selectedPatch(), map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial{agentconfig.ClaudeAPIKeySecret: material})
	if err != nil {
		test.Fatal("synthetic image rejected")
	}
	materialCopy, imageCopy := material, image
	materialBytes, imageBytes := materialCopy.CopyBytes(), imageCopy.CopyBytes()
	clear(materialBytes)
	clear(imageBytes)
	if !bytes.Equal(material.CopyBytes(), []byte("synthetic-copy-only")) || !bytes.Equal(materialCopy.CopyBytes(), material.CopyBytes()) || !bytes.Equal(imageCopy.CopyBytes(), image.CopyBytes()) || len(image.CopyBytes()) == 0 || image.CopyBytes()[0] != '{' {
		test.Fatal("copied wrapper reveal changed shared immutable payload")
	}
}

func TestClaudeQuotedMaterialAndFailurePreserveInputs(test *testing.T) {
	input := []byte(" \t\"\\é ")
	material, err := configapply.NewSecretMaterial(input)
	if err != nil {
		test.Fatal("valid quoted material rejected")
	}
	credentials := map[agentconfig.ClaudeSecretSlot]configapply.SecretMaterial{agentconfig.ClaudeAPIKeySecret: material}
	patch, originalPatch := selectedPatch(), selectedPatch()
	assertRewriteFailure(test, []byte(`{"invalid":`), patch, credentials, configapply.ErrInvalidSettings)
	if !reflect.DeepEqual(patch, originalPatch) || len(credentials) != 1 || !bytes.Equal(credentials[agentconfig.ClaudeAPIKeySecret].CopyBytes(), input) {
		test.Fatal("failure modified patch or consumed caller credential bytes")
	}
	image, err := configapply.RewriteClaudeSettings([]byte(`{}`), patch, credentials)
	var decoded struct {
		Environment map[string]string `json:"env"`
	}
	if err != nil || json.Unmarshal(image.CopyBytes(), &decoded) != nil || !bytes.Equal([]byte(decoded.Environment["ANTHROPIC_API_KEY"]), input) {
		test.Fatal("material quoting, UTF-8, or whitespace preservation failed")
	}
}

func FuzzClaudeNativeRewrite(test *testing.F) {
	for _, seed := range []string{
		`{}`, `{"env":{}}`, `{"env":{"ANTHROPIC_API_KEY":"old","KEEP":[1,900719925474099312345]}}`,
		"{\r\n\t\"env\":{ \"KEEP\" : {\"n\":1e+004}, \"ANTHROPIC_MODEL\":\"old\" }}\n",
		`{"env":{"ANTHROPIC_API_\u004bEY":"old"}}`, `{"env":{"x":0,"\u0078":1}}`, `{"broken":`,
	} {
		test.Add([]byte(seed))
	}
	test.Fuzz(func(test *testing.T, base []byte) {
		before := bytes.Clone(base)
		image, err := configapply.RewriteClaudeSettings(base, nativePatch(), nil)
		if !bytes.Equal(base, before) {
			test.Fatal("rewrite modified input")
		}
		if err != nil {
			if image.CopyBytes() != nil || !(errors.Is(err, configapply.ErrInvalidSettings) || errors.Is(err, configapply.ErrSettingsLimit)) {
				test.Fatal("rewrite returned partial image or uncontrolled error")
			}
			return
		}
		output := image.CopyBytes()
		if len(output) > agentconfig.MaxConfigBytes || !jsontext.Value(output).IsValid() {
			test.Fatal("rewrite generated invalid or unbounded JSON")
		}
		var root map[string]jsontext.Value
		if json.Unmarshal(output, &root) != nil || root == nil {
			test.Fatal("rewrite generated a non-object")
		}
		var original map[string]jsontext.Value
		if json.Unmarshal(base, &original) != nil {
			test.Fatal("rewrite accepted invalid base")
		}
		for name, value := range original {
			if name != "env" && !bytes.Equal(value, root[name]) {
				test.Fatal("rewrite changed an unrelated root value")
			}
		}
		if raw, exists := root["env"]; exists {
			var environment map[string]jsontext.Value
			if json.Unmarshal(raw, &environment) != nil || environment == nil {
				test.Fatal("rewrite generated a non-object environment")
			}
			for _, operation := range nativePatch().Operations {
				if _, exists := environment[string(operation.Slot)]; exists {
					test.Fatal("native-login retained a managed override")
				}
			}
			var beforeEnvironment map[string]jsontext.Value
			if json.Unmarshal(original["env"], &beforeEnvironment) != nil {
				test.Fatal("native-login created a missing environment")
			}
			for _, operation := range nativePatch().Operations {
				delete(beforeEnvironment, string(operation.Slot))
			}
			if !reflect.DeepEqual(beforeEnvironment, environment) {
				test.Fatal("rewrite changed or discarded unrelated environment values")
			}
		}
		second, err := configapply.RewriteClaudeSettings(output, nativePatch(), nil)
		if err != nil || !bytes.Equal(second.CopyBytes(), output) {
			test.Fatal("rewrite was not idempotent")
		}
	})
}

func BenchmarkClaudeNativeRewrite(benchmark *testing.B) {
	base := []byte(`{"env":{"KEEP":{"number":900719925474099312345},"ANTHROPIC_API_KEY":"synthetic-old"},"format":"\u00e9"}`)
	patch := nativePatch()
	benchmark.ReportAllocs()
	for benchmark.Loop() {
		if _, err := configapply.RewriteClaudeSettings(base, patch, nil); err != nil {
			benchmark.Fatal("valid bounded rewrite failed")
		}
	}
}
