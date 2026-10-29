package codexconfig

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestCodexJSONRawAndLimits(test *testing.T) {
	raw := []byte("{ \"unknown\" : {\"big\":9007199254740993123456789,\"escaped\":\"\\u0061\"}, \"owned\" : false }")
	document, err := readJSON(test.Context(), raw)
	if err != nil {
		test.Fatal("valid JSON rejected")
	}
	output, _, err := rewriteJSONMembers(test.Context(), raw, document.root, []jsonUpdate{{name: "owned", value: []byte("true")}})
	if err != nil || !bytes.Equal(output, bytes.Replace(raw, []byte("false"), []byte("true"), 1)) {
		test.Fatal("JSON raw preservation")
	}
	for _, input := range []string{``, `[]`, `null`, `{} {}`, `{"x":1,"x":2}`, `{"x":{"a":1,"\u0061":2}}`, `{"x":[{"a":1,"a":2}]}`, "{\"x\":\"\xff\"}"} {
		if _, err := readJSON(test.Context(), []byte(input)); !errors.Is(err, ErrInvalidLocalImage) {
			test.Fatal("invalid JSON accepted")
		}
	}
	for _, input := range []string{"{\"x\":" + strings.Repeat("[", 32) + "0" + strings.Repeat("]", 32) + "}", "{\"x\":[" + strings.Repeat("0,", 4096) + "0]}"} {
		if _, err := readJSON(test.Context(), []byte(input)); !errors.Is(err, ErrCodecLimit) {
			test.Fatal("JSON structure limit")
		}
	}
	if _, err := readJSON(test.Context(), []byte("{\"x\":"+strings.Repeat("[", 31)+"0"+strings.Repeat("]", 31)+"}")); err != nil {
		test.Fatal("JSON depth boundary")
	}
}

func TestCodexJSONExactBudgets(test *testing.T) {
	for _, count := range []int{512, 513} {
		var members []string
		for index := range count {
			members = append(members, `"key`+strconv.Itoa(index)+`":0`)
		}
		_, err := readJSON(test.Context(), []byte("{"+strings.Join(members, ",")+"}"))
		if count == 512 && err != nil || count == 513 && !errors.Is(err, ErrCodecLimit) {
			test.Fatal("object member boundary")
		}
	}
	for _, extra := range []int{1, 2} {
		body := `{"x":[` + strings.Repeat(`[0,0,0,0,0,0],`, 4095) + "[" + strings.TrimSuffix(strings.Repeat("0,", extra), ",") + "]]}"
		_, err := readJSON(test.Context(), []byte(body))
		if extra == 1 && err != nil || extra == 2 && !errors.Is(err, ErrCodecLimit) {
			test.Fatal("JSON token boundary")
		}
	}
	maximum := []byte(`{"x":"` + strings.Repeat("x", MaxImageBytes-8) + `"}`)
	if len(maximum) != MaxImageBytes {
		test.Fatal("fixture byte size")
	}
	if _, err := readJSON(test.Context(), maximum); err != nil {
		test.Fatal("JSON byte boundary")
	}
	if _, err := readJSON(test.Context(), append(maximum, ' ')); !errors.Is(err, ErrCodecLimit) {
		test.Fatal("JSON byte overflow")
	}
}

func FuzzCodexJSON(fuzz *testing.F) {
	for _, seed := range []string{`{}`, `{"x":{"a":1,"\u0061":2}}`, `{"x":[1,{},[false]]}`, `{"x":9007199254740993123456789}`} {
		fuzz.Add([]byte(seed))
	}
	fuzz.Fuzz(func(test *testing.T, raw []byte) {
		before := bytes.Clone(raw)
		document, err := readJSON(test.Context(), raw)
		if !bytes.Equal(raw, before) {
			test.Fatal("JSON parser mutated input")
		}
		if err == nil && (document.root.start < 0 || document.root.end > len(raw)) {
			test.Fatal("JSON invalid spans")
		}
		if err != nil && !errors.Is(err, ErrInvalidLocalImage) && !errors.Is(err, ErrCodecLimit) {
			test.Fatal("unsafe JSON parser error")
		}
	})
}

func TestCodexAuthPreserved(test *testing.T) {
	raw := []byte("{\"tokens\":{\"access_token\":\"SYNTHETIC_PRIVATE_CANARY\"},\"OPENAI_API_KEY\":null,\"unknown\":9007199254740993123456789}")
	image, _ := NewImage(Present, raw)
	document, _ := indexTOML(test.Context(), nil)
	resolution := syntheticResolution(test, Absent, Absent, nil, File, KeyringNotObserved, CredentialLogin)
	if _, err := resolveLocalAuth(test.Context(), document, image, *resolution.get[resolutionData](), syntheticProfile()); err != nil {
		test.Fatal("file login observation rejected")
	}
	if !bytes.Equal(image.CopyBytes(), raw) || image.Presence() != Present {
		test.Fatal("auth changed")
	}
	for _, presence := range []Presence{Missing, NotObserved} {
		value, _ := NewImage(presence, nil)
		if value.Presence() != presence {
			test.Fatal("auth absence collapsed")
		}
	}
}
