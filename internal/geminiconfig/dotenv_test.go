package geminiconfig

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"os"
	"testing"
)

func TestGeminiDotenvFixedVectors(test *testing.T) {
	raw, err := os.ReadFile("testdata/native-profile-v0.61.0/vectors.json")
	if err != nil {
		test.Fatal("literal vectors missing")
	}
	var vectors struct {
		Dotenv []struct {
			Raw   string `json:"raw"`
			Value string `json:"value"`
		} `json:"dotenv"`
		SettingsBefore string `json:"settingsBefore"`
		SettingsAfter  string `json:"settingsAfter"`
	}
	if json.Unmarshal(raw, &vectors) != nil || len(vectors.Dotenv) != 6 {
		test.Fatal("literal vector fixture invalid")
	}
	for _, vector := range vectors.Dotenv {
		parsed, err := readEnv(test.Context(), []byte(vector.Raw))
		if err != nil || parsed.find("A") == nil || !bytes.Equal(parsed.find("A").value, []byte(vector.Value)) {
			test.Fatal("independent literal parse vector differs")
		}
		parsed.destroy()
	}
	parsed, err := readJSON(test.Context(), []byte(vectors.SettingsBefore))
	if err != nil {
		test.Fatal("settings vector invalid")
	}
	defer parsed.destroy()
	actual, err := rewriteSettings(test.Context(), parsed, []jsonChange{{path: []string{"model", "name"}, value: []byte(`"new"`)}})
	if err != nil || !bytes.Equal(actual, []byte(vectors.SettingsAfter)) {
		test.Fatal("independent settings literal vector differs")
	}
}

func TestGeminiDotenvUnicodeLineSeparators(test *testing.T) {
	for _, separator := range []string{"\u2028", "\u2029"} {
		parsed, err := readEnv(test.Context(), []byte("# comment"+separator+"GEMINI_API_KEY=hidden"))
		if err == nil {
			parsed.destroy()
			test.Fatal("JS line separator concealed an active assignment")
		}
		if encoded, err := encodeEnv([]byte("key" + separator + "value")); err == nil || len(encoded) != 0 {
			test.Fatal("unsupported JS line separator encoded into material")
		}
	}
}

func TestGeminiDotenvPreservation(test *testing.T) {
	before := []byte("# heading\r\nexport GEMINI_MODEL = 'old'  # keep\r\nFOREIGN=unknown=unchanged\r\n# tail")
	document, err := readEnv(test.Context(), before)
	if err != nil {
		test.Fatal("legal synthetic dotenv rejected")
	}
	defer document.destroy()
	after, err := rewriteEnv(test.Context(), document, []envUpdate{{name: "GEMINI_MODEL", value: []byte("New/Model:1")}})
	want := []byte("# heading\r\nexport GEMINI_MODEL = 'New/Model:1'  # keep\r\nFOREIGN=unknown=unchanged\r\n# tail")
	if err != nil || !bytes.Equal(after, want) {
		test.Fatal("dotenv span preservation failed")
	}
	if !bytes.Equal(before, document.raw) {
		test.Fatal("dotenv rewrite mutated input")
	}
	for _, value := range []string{"plain", " space ", "x#y$z=1", `a'"b`, "a`b'c", `literal\n\r`, `trailing\`, "密钥 空格", "'\"`#all"} {
		encoded, err := encodeEnv([]byte(value))
		if err != nil {
			if value != "'\"`#all" || !errors.Is(err, ErrUnsupportedRepresentation) {
				test.Fatal("representable literal rejected")
			}
			continue
		}
		raw := append([]byte("GEMINI_API_KEY="), encoded...)
		parsed, err := readEnv(test.Context(), raw)
		if err != nil || parsed.find("GEMINI_API_KEY") == nil || !bytes.Equal(parsed.find("GEMINI_API_KEY").value, []byte(value)) {
			test.Fatal("dotenv literal changed bytes")
		}
		parsed.destroy()
	}
	for _, raw := range []string{"A=one\nA=two", "export A=one\nA=two", "A='first\nsecond'", "A='unterminated", "A='a' garbage", "A:\tvalue", "not an assignment", "A=x\rB=y", "A=x\x00"} {
		if document, err := readEnv(test.Context(), []byte(raw)); err == nil {
			document.destroy()
			test.Fatal("unsupported or ambiguous dotenv accepted")
		}
	}
	removed, err := rewriteEnv(test.Context(), document, []envUpdate{{name: "GEMINI_MODEL", remove: true}})
	if err != nil || !bytes.Equal(removed, []byte("# heading\r\n# keep\r\nFOREIGN=unknown=unchanged\r\n# tail")) {
		test.Fatal("managed removal lost comments or foreign lines")
	}
	for _, raw := range []string{"", "# no newline", "FOREIGN=value\n", "FOREIGN=value\r\n"} {
		document, err := readEnv(test.Context(), []byte(raw))
		if err != nil {
			test.Fatal("insertion base rejected")
		}
		result, err := rewriteEnv(test.Context(), document, []envUpdate{{name: "GEMINI_MODEL", value: []byte("new")}})
		if err != nil {
			test.Fatal("bounded insertion failed")
		}
		parsed, err := readEnv(test.Context(), result)
		if err != nil || parsed.find("GEMINI_MODEL") == nil {
			test.Fatal("inserted dotenv not valid")
		}
		parsed.destroy()
		document.destroy()
	}
}

func TestGeminiDotenvTrailingBackslashBoundaries(test *testing.T) {
	for _, sample := range []struct {
		name, value string
		quote       byte
	}{
		{"single", `synthetic-tail\`, '\''},
		{"backtick", `synthetic'quoted\`, '`'},
		{"double", "synthetic'`quoted\\", '"'},
	} {
		for _, foreign := range []bool{false, true} {
			test.Run(sample.name+map[bool]string{false: "-comment", true: "-foreign"}[foreign], func(test *testing.T) {
				next := "# preserved " + string(sample.quote)
				if foreign {
					next = "FOREIGN=value" + string(sample.quote)
				}
				before := []byte("GEMINI_API_KEY='old'\n" + next + "\nGEMINI_MODEL='model.keep'\n")
				document, err := readEnv(test.Context(), before)
				if err != nil {
					test.Fatal("boundary fixture rejected")
				}
				defer document.destroy()
				after, err := rewriteEnv(test.Context(), document, []envUpdate{{name: "GEMINI_API_KEY", value: []byte(sample.value)}})
				defer clear(after)
				want := []byte("GEMINI_API_KEY=" + sample.value + "\n" + next + "\nGEMINI_MODEL='model.keep'\n")
				if err != nil || !bytes.Equal(after, want) {
					test.Fatal("whole document needs literal unquoted trailing backslash")
				}
				if !bytes.Equal(document.raw, before) {
					test.Fatal("boundary rewrite changed baseline")
				}
				settings := `{"security":{"auth":{"selectedType":"gemini-api-key"}}}`
				images := syntheticImages(test, string(before), settings)
				defer images.Destroy()
				resolution := syntheticResolution(test, images)
				defer resolution.Destroy()
				plan, err := checkProvider(test.Context(), images, declaration("api-key"), resolution, syntheticProfile())
				if err != nil {
					test.Fatal("whole document check rejected literal material fixture")
				}
				defer plan.Destroy()
				secret, err := NewSelectedSecret("gemini.api-key", []byte(sample.value))
				if err != nil {
					test.Fatal("whole document material fixture rejected")
				}
				defer secret.Destroy()
				candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
				if err != nil || candidate.get[candidateData]() == nil {
					test.Fatal("representable trailing backslash needs complete candidate")
				}
				defer candidate.Destroy()
				image := candidate.CopyImage(EnvRole)
				defer image.Destroy()
				body := image.CopyBytes()
				defer clear(body)
				if !bytes.Equal(body, want) {
					test.Fatal("whole document render changed material or foreign bytes")
				}
			})
		}
	}
}

func TestGeminiDotenvInputCrossLineQuotes(test *testing.T) {
	for _, quote := range []byte{'\'', '`', '"'} {
		for _, newline := range []string{"\n", "\r\n"} {
			for _, following := range []string{"# preserved ", "FOREIGN=value"} {
				before := []byte("GEMINI_API_KEY=" + string(quote) + "tail\\" + string(quote) + newline + following + string(quote) + newline)
				parsed, err := readEnv(test.Context(), before)
				if parsed != nil {
					parsed.destroy()
				}
				if !errors.Is(err, ErrUnsupportedRepresentation) || parsed != nil {
					test.Fatal("cross line quoted input accepted")
				}
			}
		}
		before := []byte("GEMINI_API_KEY=" + string(quote) + "tail\\" + string(quote))
		parsed, err := readEnv(test.Context(), before)
		if err != nil || parsed.find("GEMINI_API_KEY") == nil || !bytes.Equal(parsed.find("GEMINI_API_KEY").value, []byte(`tail\`)) {
			test.Fatal("single line backtracking control changed")
		}
		parsed.destroy()
	}
}

func TestGeminiDotenvForeignQuoteInsertion(test *testing.T) {
	for _, quote := range []byte{'\'', '`', '"'} {
		before := []byte("FOREIGN=" + string(quote) + "tail\\" + string(quote))
		document, err := readEnv(test.Context(), before)
		if err != nil {
			test.Fatal("single line foreign fixture rejected")
		}
		var later []byte
		later = append(later, "\n# preserved "...)
		later = append(later, quote)
		later = append(later, '\n')
		after, err := applyEdits(test.Context(), document.raw, []rawEdit{{start: len(before), end: len(before), value: later}})
		if err != nil {
			test.Fatal("bounded edit fixture rejected")
		}
		parsed, err := readEnv(test.Context(), after)
		if parsed != nil {
			parsed.destroy()
		}
		clear(after)
		document.destroy()
		if !errors.Is(err, ErrUnsupportedRepresentation) || parsed != nil {
			test.Fatal("later bytes changed previously complete foreign value")
		}
	}
}

func TestGeminiDotenvTailFailureWithholdsWholePair(test *testing.T) {
	images := syntheticImages(test, "GEMINI_API_KEY='old'\nFOREIGN=keep'\n", `{"security":{"auth":{"selectedType":"gemini-api-key"}},"model":{"name":"old.settings"}}`)
	defer images.Destroy()
	resolution := syntheticResolution(test, images)
	defer resolution.Destroy()
	want := declaration("api-key")
	want.SettingsModel = new("new.settings")
	plan, err := checkProvider(test.Context(), images, want, resolution, syntheticProfile())
	if err != nil {
		test.Fatal("whole pair boundary fixture rejected")
	}
	defer plan.Destroy()
	for _, value := range []string{" space-tail\\", "hash#tail\\", "'quoted-tail\\"} {
		secret, err := NewSelectedSecret("gemini.api-key", []byte(value))
		if err != nil {
			test.Fatal("synthetic tail material rejected")
		}
		candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
		secret.Destroy()
		if !errors.Is(err, ErrUnsupportedRepresentation) || candidate.get[candidateData]() != nil {
			candidate.Destroy()
			test.Fatal("unrepresentable tail published pair")
		}
	}
}
