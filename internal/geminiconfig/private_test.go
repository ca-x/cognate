package geminiconfig

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

const privacyMarker = "SYNTHETIC_GB0_PRIVATE_981723"

func assertPrivate(test *testing.T, value any) {
	test.Helper()
	formats := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%t", "%b", "%d", "%o", "%O", "%U", "%c", "%e", "%E", "%f", "%F", "%g", "%G", "%p", "%T", "%020v", "%.30s", "%#20x", "%q|%v"}
	leaks := []string{privacyMarker, fmt.Sprint([]byte(privacyMarker)), fmt.Sprintf("%#v", []byte(privacyMarker)), fmt.Sprintf("%x", privacyMarker), fmt.Sprintf("%X", privacyMarker)}
	for _, format := range []string{"%t", "%d", "%b", "%o", "%O", "%U", "%c", "%e", "%E", "%f", "%F", "%g", "%G"} {
		leaks = append(leaks, fmt.Sprintf(format, []byte(privacyMarker)))
	}
	check := func(body string) {
		for _, leak := range leaks {
			if strings.Contains(body, leak) {
				test.Fatal("opaque value leaked private sentinel")
			}
		}
	}
	for _, format := range formats {
		check(fmt.Sprintf(format, value))
	}
	body, err := json.Marshal(value)
	if err != nil {
		if _, marshaler := value.(interface{ MarshalJSON() ([]byte, error) }); marshaler {
			test.Fatal("private JSON encoding failed")
		}
		check(err.Error())
	} else {
		check(string(body))
	}
	if marshaler, ok := value.(interface{ MarshalText() ([]byte, error) }); ok {
		body, err := marshaler.MarshalText()
		if err != nil {
			test.Fatal("private text encoding failed")
		}
		check(string(body))
	}
	var buffer bytes.Buffer
	log.New(&buffer, "", 0).Print(value)
	check(buffer.String())
	for _, handler := range []slog.Handler{slog.NewTextHandler(&buffer, nil), slog.NewJSONHandler(&buffer, nil)} {
		buffer.Reset()
		slog.New(handler).Info("privacy", "value", value)
		check(buffer.String())
	}
}

func TestGeminiAllOpaqueContainers(test *testing.T) {
	images := syntheticImages(test, "GEMINI_API_KEY='"+privacyMarker+"'\n", `{"ui":"`+privacyMarker+`"}`)
	defer images.Destroy()
	facts := syntheticResolution(test, images)
	defer facts.Destroy()
	value := declaration(agentconfig.GeminiAPIKeyAuth)
	value.Endpoint = new("https://" + privacyMarker + ".invalid/")
	plan, err := checkProvider(test.Context(), images, value, facts, syntheticProfile())
	if err != nil {
		test.Fatal("private pair fixture failed")
	}
	defer plan.Destroy()
	secret, _ := NewSelectedSecret(agentconfig.GeminiAPIKeySecret, []byte(privacyMarker))
	defer secret.Destroy()
	candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
	if err != nil {
		test.Fatal("private candidate fixture failed")
	}
	defer candidate.Destroy()
	binding := plan.Binding()
	defer binding.Destroy()
	image := images.CopyImage(EnvRole)
	defer image.Destroy()
	for _, value := range []any{image, images, facts, secret, plan, binding, candidate} {
		for _, container := range []any{value, &value, []any{value}, map[string]any{"entry": value}, struct{ hidden any }{value}, &struct{ hidden any }{value}, struct{ hidden struct{ nested any } }{struct{ nested any }{value}}} {
			assertPrivate(test, container)
		}
	}
}

func TestGeminiCodecOpaqueOwnership(test *testing.T) {
	input := []byte(privacyMarker)
	secret, err := NewSelectedSecret(agentconfig.GeminiAPIKeySecret, input)
	if err != nil {
		test.Fatal("synthetic material rejected")
	}
	type privateBox struct{ credential SelectedSecret }
	for _, value := range []any{secret, &secret, []SelectedSecret{secret}, map[string]SelectedSecret{"entry": secret}, privateBox{secret}, &privateBox{secret}, struct{ box privateBox }{privateBox{secret}}, func() SelectedSecret { return secret }} {
		assertPrivate(test, value)
	}
	clear(input)
	if !bytes.Equal(secret.get[secretData]().body, []byte(privacyMarker)) {
		test.Fatal("constructor aliases caller material")
	}
	alias := secret
	secret.Destroy()
	secret.Destroy()
	if alias.get[secretData]() != nil {
		test.Fatal("destroyed handle retains material")
	}
	assertPrivate(test, secret)
}
