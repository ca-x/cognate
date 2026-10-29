package codexconfig

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestCodexTOMLRepresentations(test *testing.T) {
	for name, input := range map[string]string{
		"explicit":      "keep = 9223372036854775807\n[model_providers.cognate]\nbase_url = 'https://old.invalid' # tail\n",
		"quoted":        "[\"model_providers\".\"cog\\u006eate\"]\n\"base_url\" = 'https://old.invalid' # tail\n",
		"dotted":        "model_providers.cognate.base_url = 'https://old.invalid' # tail\n",
		"quoted-dotted": "\"model_providers\".\"cog\\u006eate\".base_url = 'https://old.invalid' # tail\n",
		"empty":         "",
	} {
		test.Run(name, func(test *testing.T) {
			document, err := indexTOML(test.Context(), []byte(input))
			if err != nil {
				test.Fatal("supported TOML rejected")
			}
			output, _, err := rewriteTOML(test.Context(), document, goldenDeclaration(test), true, []byte("new\"\\\t\x01key"))
			if err != nil {
				test.Fatal("supported TOML conversion failed")
			}
			var decoded map[string]any
			if toml.Unmarshal(output, &decoded) != nil || decoded["model_provider"] != "cognate" {
				test.Fatal("root semantic mismatch")
			}
			provider := decoded["model_providers"].(map[string]any)["cognate"].(map[string]any)
			if provider["experimental_bearer_token"] != "new\"\\\t\x01key" || provider["requires_openai_auth"] != true || provider["base_url"] != "https://codec.invalid:443/v1" {
				test.Fatal("provider semantic mismatch")
			}
			if strings.Contains(input, "# tail") && !bytes.Contains(output, []byte(" # tail\n")) {
				test.Fatal("tail comment changed")
			}
			if strings.Contains(input, "9223372036854775807") && !bytes.Contains(output, []byte("keep = 9223372036854775807\n")) {
				test.Fatal("unknown TOML normalized")
			}
		})
	}
}

func TestCodexTOMLExactBudgets(test *testing.T) {
	for _, count := range []int{32765, 32766} {
		raw := []byte("x=[" + strings.Repeat("0,", count-1) + "0]")
		_, err := indexTOML(test.Context(), raw)
		if count == 32765 && err != nil || count == 32766 && !errors.Is(err, ErrCodecLimit) {
			test.Fatal("TOML AST node boundary")
		}
	}
	for _, depth := range []int{32, 33} {
		for _, raw := range []string{strings.Repeat("part.", depth-1) + "leaf=0", "x=" + strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)} {
			_, err := indexTOML(test.Context(), []byte(raw))
			if depth == 32 && err != nil || depth == 33 && !errors.Is(err, ErrCodecLimit) {
				test.Fatal("TOML depth/key boundary")
			}
		}
	}
	if _, err := applyEdits(test.Context(), nil, make([]rawEdit, 512)); err != nil {
		test.Fatal("edit boundary")
	}
	if _, err := applyEdits(test.Context(), []byte("abc"), []rawEdit{{start: 0, end: 2}, {start: 1, end: 3}}); !errors.Is(err, ErrUnsupportedRepresentation) {
		test.Fatal("overlapping edits accepted")
	}
}

func FuzzCodexTOML(fuzz *testing.F) {
	for _, seed := range []string{"", "x=1\nx=2", "[model_providers.cognate]\nbase_url='https://old.invalid'", "x={a=[1,2]}"} {
		fuzz.Add([]byte(seed))
	}
	fuzz.Fuzz(func(test *testing.T, raw []byte) {
		before := bytes.Clone(raw)
		_, err := indexTOML(test.Context(), raw)
		if !bytes.Equal(raw, before) {
			test.Fatal("TOML parser mutated input")
		}
		if err != nil && !errors.Is(err, ErrInvalidLocalImage) && !errors.Is(err, ErrCodecLimit) {
			test.Fatal("unsafe TOML parser error")
		}
	})
}

func BenchmarkCodexBoundedParsers(benchmark *testing.B) {
	for _, name := range []string{"toml-valid-max", "toml-invalid-max", "json-valid-max", "json-invalid-max"} {
		benchmark.Run(name, func(benchmark *testing.B) {
			var raw []byte
			if strings.HasPrefix(name, "toml") {
				raw = []byte("note='" + strings.Repeat("x", MaxImageBytes-7) + "'")
			} else {
				raw = []byte(`{"x":"` + strings.Repeat("x", MaxImageBytes-8) + `"}`)
			}
			if strings.Contains(name, "invalid") {
				raw[len(raw)-1] = 'x'
			}
			benchmark.ReportAllocs()
			benchmark.SetBytes(int64(len(raw)))
			for benchmark.Loop() {
				if strings.HasPrefix(name, "toml") {
					_, _ = indexTOML(benchmark.Context(), raw)
				} else {
					_, _ = readJSON(benchmark.Context(), raw)
				}
			}
		})
	}
}

func TestCodexTOMLLayoutAnchors(test *testing.T) {
	input := []byte("# root\r\nkeep = 'raw'\r\n# provider docs\r\n[other]\r\nkey = 'OLD_PRIVATE_CANARY'\r\n# eof docs")
	document, err := indexTOML(test.Context(), input)
	if err != nil {
		test.Fatal("layout fixture")
	}
	output, _, err := rewriteTOML(test.Context(), document, goldenDeclaration(test), false, []byte("new"))
	if err != nil {
		test.Fatal("layout conversion")
	}
	var decoded map[string]any
	if toml.Unmarshal(output, &decoded) != nil || decoded["model"] != "Case/model:1" {
		test.Fatal("root inserted in last section")
	}
	if bytes.Index(output, []byte("model_provider =")) > bytes.Index(output, []byte("# provider docs")) {
		test.Fatal("root comment anchor")
	}
	if !bytes.Contains(output, []byte("# provider docs\r\n[other]\r\nkey = 'OLD_PRIVATE_CANARY'\r\n")) || !bytes.HasSuffix(output, []byte("# eof docs")) {
		test.Fatal("untouched layout changed")
	}
	if bytes.Contains(bytes.ReplaceAll(output, []byte("\r\n"), nil), []byte("\n")) {
		test.Fatal("EOL normalized")
	}
	for _, fixture := range []string{"# eof", "keep = 1", "\"model_providers.cognate\" = 'unrelated'\n"} {
		document, err := indexTOML(test.Context(), []byte(fixture))
		if err != nil {
			test.Fatal("legal layout")
		}
		if _, _, err := rewriteTOML(test.Context(), document, goldenDeclaration(test), false, []byte("new")); err != nil {
			test.Fatal("legal layout failed")
		}
	}
}

func TestCodexTOMLSemanticValidation(test *testing.T) {
	for _, input := range []string{"model='a'\nmodel='b'", "a.b=1\na=2", "[x]\n[x]", "x = {bad}", "x='unterminated", "x = \x00"} {
		if _, err := indexTOML(test.Context(), []byte(input)); !errors.Is(err, ErrInvalidLocalImage) {
			test.Fatal("mature semantic rejection missing")
		}
	}
	for _, input := range []string{"model_providers = { cognate = { name = 'x' } }", "[model_providers]\ncognate={name='x'}", "[[model_providers.cognate]]\nname='x'", "model_providers=1"} {
		document, err := indexTOML(test.Context(), []byte(input))
		if err == nil {
			_, _, err = rewriteTOML(test.Context(), document, goldenDeclaration(test), false, []byte("new"))
		}
		if !errors.Is(err, ErrUnsupportedRepresentation) {
			test.Fatal("owned closed representation accepted")
		}
	}
	for _, input := range []string{strings.Repeat("# comment\n", 4097), "x=" + strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33)} {
		if _, err := indexTOML(test.Context(), []byte(input)); !errors.Is(err, ErrCodecLimit) {
			test.Fatal("TOML structural limit")
		}
	}
	if _, err := indexTOML(test.Context(), []byte(strings.Repeat("# comment\n", 4096))); err != nil {
		test.Fatal("expression boundary")
	}
	if _, err := applyEdits(test.Context(), nil, make([]rawEdit, maxEdits+1)); !errors.Is(err, ErrCodecLimit) {
		test.Fatal("edit bound")
	}
}
