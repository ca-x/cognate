package geminiconfig

import (
	"bytes"
	"testing"
)

func TestGeminiSettingsPreservation(test *testing.T) {
	before := []byte("{\r\n// heading\r\n\"foreign\":9007199254740993999,\r\n\"model\":{\"other\":\"\\u0041\",\"name\":\"old\"},\r\n\"security\":{\"auth\":{\"selectedType\":\"oauth-personal\",\"note\":\"keep\"}},\r\n\"mcpServers\":{\"foreign\":{\"unknown\":[true,null]}}\r\n}")
	document, err := readJSON(test.Context(), before)
	if err != nil {
		test.Fatal("legal commented settings rejected")
	}
	defer document.destroy()
	after, err := rewriteSettings(test.Context(), document, []jsonChange{{path: []string{"model", "name"}, value: []byte(`"new"`)}, {path: []string{"security", "auth", "selectedType"}, value: []byte(`"gemini-api-key"`)}})
	want := bytes.ReplaceAll(before, []byte(`"old"`), []byte(`"new"`))
	want = bytes.ReplaceAll(want, []byte(`"oauth-personal"`), []byte(`"gemini-api-key"`))
	if err != nil || !bytes.Equal(after, want) {
		test.Fatal("JSONC edit changed unrelated raw bytes")
	}
	if !bytes.Equal(before, document.raw) {
		test.Fatal("JSONC edit mutated input")
	}
	for _, raw := range []string{`{"model":{},"\u006dodel":{}}`, `{"model":{"name":"one","name":"two"}}`, `{"model":{},}`, `[]`, `null`, `{"broken":`, `/* unfinished`, `{"string":"unterminated}`} {
		if parsed, err := readJSON(test.Context(), []byte(raw)); err == nil {
			parsed.destroy()
			test.Fatal("invalid or duplicate settings accepted")
		}
	}
	for _, raw := range []string{`{}`, "{/*before*/}", `{"model":{"other":1}}`, `{"security":{},"keep":true}`} {
		parsed, err := readJSON(test.Context(), []byte(raw))
		if err != nil {
			test.Fatal("insertion base rejected")
		}
		result, err := rewriteSettings(test.Context(), parsed, []jsonChange{{path: []string{"security", "auth", "selectedType"}, value: []byte(`"oauth-personal"`)}})
		if err != nil {
			test.Fatal("nested insertion failed")
		}
		actual, err := readJSON(test.Context(), result)
		if err != nil || actual.root.member("security").member("auth").member("selectedType").text != "oauth-personal" {
			test.Fatal("inserted selector differs")
		}
		actual.destroy()
		parsed.destroy()
	}
	for _, raw := range []string{`{"model":null}`, `{"model":[]}`, `{"model":false}`} {
		parsed, err := readJSON(test.Context(), []byte(raw))
		if err != nil {
			test.Fatal("shape fixture invalid")
		}
		if result, err := rewriteSettings(test.Context(), parsed, []jsonChange{{path: []string{"model", "name"}, value: []byte(`"new"`)}}); err == nil || len(result) != 0 {
			test.Fatal("malformed parent repaired")
		}
		parsed.destroy()
	}
	for _, raw := range []string{`{"name":"old",/*keep*/"other":1}`, `{"other":1,/*keep*/"name":"old"}`, `{/*keep*/"name":"old"/*tail*/}`} {
		parsed, _ := readJSON(test.Context(), []byte(raw))
		result, err := rewriteSettings(test.Context(), parsed, []jsonChange{{path: []string{"name"}, remove: true}})
		if err != nil || !bytes.Contains(result, []byte("/*keep*/")) {
			test.Fatal("removal lost foreign comment")
		}
		actual, err := readJSON(test.Context(), result)
		if err != nil || actual.root.member("name") != nil {
			test.Fatal("managed leaf removal invalid")
		}
		actual.destroy()
		parsed.destroy()
	}
}
