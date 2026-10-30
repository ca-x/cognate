package geminiconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

type countingContext struct {
	context.Context
	calls, cancelAt int
}

func (ctx *countingContext) Err() error {
	ctx.calls++
	if ctx.cancelAt > 0 && ctx.calls >= ctx.cancelAt {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func tokenJSON(last int) []byte {
	var body bytes.Buffer
	body.WriteString(`{"a":[`)
	for index := range 8 {
		if index > 0 {
			body.WriteByte(',')
		}
		count := 4096
		if index == 7 {
			count = last
		}
		body.WriteByte('[')
		body.WriteString(strings.Repeat("0,", count-1))
		body.WriteByte('0')
		body.WriteByte(']')
	}
	body.WriteString(`]}`)
	return body.Bytes()
}

func TestGeminiCodecLimitsAndCancellation(test *testing.T) {
	for _, size := range []int{1, 8192} {
		secret, err := NewSelectedSecret(agentconfig.GeminiAPIKeySecret, bytes.Repeat([]byte{'x'}, size))
		if err != nil {
			test.Fatal("legal secret bound rejected")
		}
		secret.Destroy()
	}
	for _, raw := range [][]byte{nil, bytes.Repeat([]byte{'x'}, 8193), {'x', 0}, {'\r'}, {'\n'}, {0xff}} {
		if _, err := NewSelectedSecret(agentconfig.GeminiAPIKeySecret, raw); !errors.Is(err, ErrInvalidSecret) {
			test.Fatal("invalid secret admitted")
		}
	}
	for _, size := range []int{MaxImageBytes, MaxImageBytes + 1} {
		image, err := NewImage(Present, make([]byte, size))
		if (err == nil) != (size == MaxImageBytes) {
			test.Fatal("image byte bound failed")
		}
		image.Destroy()
	}
	first, _ := NewImage(Present, make([]byte, MaxImageBytes))
	defer first.Destroy()
	for _, size := range []int{MaxPairBytes - MaxImageBytes, MaxPairBytes - MaxImageBytes + 1} {
		second, _ := NewImage(Present, make([]byte, size))
		pair, err := NewImages(first, second)
		second.Destroy()
		if (err == nil) != (size == MaxPairBytes-MaxImageBytes) {
			test.Fatal("pair byte bound failed")
		}
		pair.Destroy()
	}
	if _, err := NewImage(Missing, []byte("not missing")); err == nil {
		test.Fatal("missing silently swallowed body")
	}
	for _, count := range []int{4096, 4097} {
		for _, members := range []bool{false, true} {
			var body bytes.Buffer
			for index := range count {
				if members {
					fmt.Fprintf(&body, "K%d=x\n", index)
				} else {
					body.WriteString("#\n")
				}
			}
			parsed, err := readEnv(test.Context(), body.Bytes())
			if (err == nil) != (count == 4096) || err != nil && !errors.Is(err, ErrCodecLimit) {
				test.Fatal("env count limit failed")
			}
			if parsed != nil {
				parsed.destroy()
			}
		}
	}
	for _, count := range []int{512, 513} {
		var body bytes.Buffer
		body.WriteByte('{')
		for index := range count {
			if index > 0 {
				body.WriteByte(',')
			}
			fmt.Fprintf(&body, "\"K%d\":0", index)
		}
		body.WriteByte('}')
		parsed, err := readJSON(test.Context(), body.Bytes())
		if (err == nil) != (count == 512) || err != nil && !errors.Is(err, ErrCodecLimit) {
			test.Fatal("JSON member bound failed")
		}
		if parsed != nil {
			parsed.destroy()
		}
	}
	for _, count := range []int{4096, 4097} {
		body := []byte(`{"a":[` + strings.Repeat("0,", count-1) + "0]}")
		parsed, err := readJSON(test.Context(), body)
		if (err == nil) != (count == 4096) || err != nil && !errors.Is(err, ErrCodecLimit) {
			test.Fatal("JSON array bound failed")
		}
		if parsed != nil {
			parsed.destroy()
		}
	}
	for _, count := range []int{31, 32} {
		body := []byte(`{"a":` + strings.Repeat("[", count) + "0" + strings.Repeat("]", count) + "}")
		parsed, err := readJSON(test.Context(), body)
		if (err == nil) != (count == 31) || err != nil && !errors.Is(err, ErrCodecLimit) {
			test.Fatal("JSON depth bound failed")
		}
		if parsed != nil {
			parsed.destroy()
		}
	}
	for _, count := range []int{4075, 4076} {
		parsed, err := readJSON(test.Context(), tokenJSON(count))
		if (err == nil) != (count == 4075) || err != nil && !errors.Is(err, ErrCodecLimit) {
			test.Fatal("JSON token bound failed")
		}
		if parsed != nil {
			parsed.destroy()
		}
	}
	for _, size := range []int{MaxImageBytes, MaxImageBytes + 1} {
		body := []byte(`{"a":"` + strings.Repeat("x", size-8) + `"}`)
		parsed, err := readJSON(test.Context(), body)
		if (err == nil) != (size == MaxImageBytes) || err != nil && !errors.Is(err, ErrCodecLimit) {
			test.Fatal("parser byte bound failed")
		}
		if parsed != nil {
			parsed.destroy()
		}
	}
	for _, size := range []int{256, 257} {
		value := declaration(agentconfig.GeminiAPIKeyAuth)
		value.EnvModel = new(strings.Repeat("A", size))
		if (validateDeclaration(value) == nil) != (size == 256) {
			test.Fatal("model bound failed")
		}
	}
	for _, size := range []int{2048, 2049} {
		value := declaration(agentconfig.GeminiAPIKeyAuth)
		prefix := "https://example.invalid/"
		value.Endpoint = new(prefix + strings.Repeat("x", size-len(prefix)))
		if (validateDeclaration(value) == nil) != (size == 2048) {
			test.Fatal("endpoint bound failed")
		}
	}
	images := syntheticImages(test, "", `{}`)
	defer images.Destroy()
	resolution := syntheticResolution(test, images)
	defer resolution.Destroy()
	value := declaration(agentconfig.GeminiAPIKeyAuth)
	countCheck := &countingContext{Context: test.Context()}
	plan, err := checkProvider(countCheck, images, value, resolution, syntheticProfile())
	if err != nil {
		test.Fatal("baseline check for cancel oracle failed")
	}
	defer plan.Destroy()
	for _, at := range []int{1, 2, countCheck.calls / 2, countCheck.calls - 1, countCheck.calls} {
		result, err := checkProvider(&countingContext{Context: test.Context(), cancelAt: at}, images, value, resolution, syntheticProfile())
		if !errors.Is(err, context.Canceled) || result.get[planData]() != nil {
			test.Fatal("check mid or return cancel failed")
		}
	}
	secret, _ := NewSelectedSecret(agentconfig.GeminiAPIKeySecret, []byte(privacyMarker))
	defer secret.Destroy()
	countRender := &countingContext{Context: test.Context()}
	candidate, err := RenderProvider(countRender, plan, []SelectedSecret{secret})
	if err != nil {
		test.Fatal("baseline render for cancel oracle failed")
	}
	candidate.Destroy()
	for _, at := range []int{1, 2, countRender.calls / 2, countRender.calls - 1, countRender.calls} {
		result, err := RenderProvider(&countingContext{Context: test.Context(), cancelAt: at}, plan, []SelectedSecret{secret})
		if !errors.Is(err, context.Canceled) || result.get[candidateData]() != nil {
			test.Fatal("render mid or return cancel failed")
		}
	}
	privateCause := errors.New(privacyMarker)
	canceled, cancel := context.WithCancelCause(test.Context())
	cancel(privateCause)
	if result, err := CheckProvider(canceled, images, value, resolution); !errors.Is(err, context.Canceled) || errors.Is(err, privateCause) || strings.Contains(fmt.Sprint(err), privacyMarker) || result.get[planData]() != nil {
		test.Fatal("private cancellation cause escaped")
	}
	if result, err := CheckProvider(&countingContext{Context: test.Context(), cancelAt: 2}, images, value, resolution); !errors.Is(err, context.Canceled) || result.get[planData]() != nil {
		test.Fatal("production Check lacks publication fresh context gate")
	}
	for _, raw := range []string{"GEMINI_MODEL=good\u00a0", "GEMINI_MODEL=\ufeffgood"} {
		parsed, err := readEnv(test.Context(), []byte(raw))
		if err == nil {
			parsed.destroy()
			test.Fatal("ambiguous Unicode env whitespace admitted")
		}
	}
	for _, raw := range []string{`{"model/name":"safe"}`, `{"security/auth/selectedType":"oauth-personal"}`} {
		bad := syntheticImages(test, "", raw)
		facts := syntheticResolution(test, bad)
		result, err := checkProvider(test.Context(), bad, value, facts, syntheticProfile())
		facts.Destroy()
		bad.Destroy()
		if err == nil || result.get[planData]() != nil {
			test.Fatal("JSON path-name collision bypassed unknown gate")
		}
	}
}

func syntheticProfile() nativeProfile {
	return nativeProfile{id: "synthetic.only.v1", dialect: "dotenv17-jsonc-v1", sourceDigest: sha256.Sum256([]byte("synthetic-not-a-consumer-profile")), ignoredEnv: []string{"FOREIGN"}, ignoredJSON: []string{"/ui", "/mcpServers", "/permissions", "/model/other", "/security/auth/note"}}
}

func syntheticImages(test *testing.T, env, settings string) LocalImages {
	test.Helper()
	first, err := NewImage(Present, []byte(env))
	if err != nil {
		test.Fatal("env image rejected")
	}
	defer first.Destroy()
	second, err := NewImage(Present, []byte(settings))
	if err != nil {
		test.Fatal("settings image rejected")
	}
	defer second.Destroy()
	images, err := NewImages(first, second)
	if err != nil {
		test.Fatal("pair rejected")
	}
	return images
}

func syntheticResolution(test *testing.T, images LocalImages, cleanup ...Field) LocalResolution {
	test.Helper()
	data := images.get[imagesData]()
	env, err := readEnv(test.Context(), data.images[0].get[imageData]().body)
	if err != nil {
		test.Fatal("synthetic env invalid")
	}
	defer env.destroy()
	settingsBody := data.images[1].get[imageData]().body
	if data.images[1].Presence() == Missing {
		settingsBody = []byte(`{}`)
	}
	settings, err := readJSON(test.Context(), settingsBody)
	if err != nil {
		test.Fatal("synthetic settings invalid")
	}
	defer settings.destroy()
	facts := make([]FieldFact, 0, len(fields))
	for _, field := range fields {
		present := false
		switch field {
		case SettingsModelField:
			parent := settings.root.member("model")
			present = parent != nil && parent.member("name") != nil
		case AuthModeField:
			parent := settings.root.member("security")
			if parent != nil {
				parent = parent.member("auth")
			}
			present = parent != nil && parent.member("selectedType") != nil
		default:
			present = env.find(string(field)) != nil
		}
		fact := FieldFact{Field: field, Ownership: Absent, Cleanup: Preserve}
		if present {
			fact.Ownership = Managed
			for _, remove := range cleanup {
				if remove == field {
					fact.Cleanup = Clear
				}
			}
		}
		facts = append(facts, fact)
	}
	resolution, err := NewResolution(syntheticProfile().id, CompleteEffective, facts, [32]byte{1})
	if err != nil {
		test.Fatal("synthetic facts rejected")
	}
	return resolution
}

func TestGeminiProductionProfileGate(test *testing.T) {
	images := syntheticImages(test, "", `{}`)
	defer images.Destroy()
	resolution := syntheticResolution(test, images)
	defer resolution.Destroy()
	for _, id := range []string{"synthetic.only.v1", "gemini-v0.61.0", "bb523741c7429a44d03e964bc124c7c92df59d5f"} {
		resolution.get[resolutionData]().profileID = id
		plan, err := CheckProvider(test.Context(), images, declaration(agentconfig.GeminiAPIKeyAuth), resolution)
		if !errors.Is(err, ErrUnsupportedNativeProfile) || plan.get[planData]() != nil {
			test.Fatal("source identity or synthetic facts bypassed production gate")
		}
	}
	raw, err := os.ReadFile("testdata/native-profile-v0.61.0/source.json")
	if err != nil {
		test.Fatal("source provenance fixture missing")
	}
	var source struct {
		ConsumerCommit              string `json:"consumerCommit"`
		ConsumerBuilt               bool   `json:"consumerBuilt"`
		ConsumerExecuted            bool   `json:"consumerExecuted"`
		Qualified                   bool   `json:"qualified"`
		ProductionProfileRegistered bool   `json:"productionProfileRegistered"`
	}
	if json.Unmarshal(raw, &source) != nil || source.ConsumerCommit != "bb523741c7429a44d03e964bc124c7c92df59d5f" || source.ConsumerBuilt || source.ConsumerExecuted || source.Qualified || source.ProductionProfileRegistered {
		test.Fatal("source-only provenance falsely claims qualification")
	}
	resolution.get[resolutionData]().profileID = source.ConsumerCommit
	if result, err := CheckProvider(test.Context(), images, declaration(agentconfig.GeminiNativeLogin), resolution); !errors.Is(err, ErrUnsupportedNativeProfile) || result.get[planData]() != nil {
		test.Fatal("fixed official source identity became production capability")
	}
}

func TestGeminiCancellationCauseIsPrivate(test *testing.T) {
	images := syntheticImages(test, "", `{}`)
	defer images.Destroy()
	facts := syntheticResolution(test, images)
	defer facts.Destroy()
	plan, err := checkProvider(test.Context(), images, declaration(agentconfig.GeminiAPIKeyAuth), facts, syntheticProfile())
	if err != nil {
		test.Fatal("cancel control failed")
	}
	defer plan.Destroy()
	secret, _ := NewSelectedSecret(agentconfig.GeminiAPIKeySecret, []byte(privacyMarker))
	defer secret.Destroy()
	ctx, cancel := context.WithCancelCause(test.Context())
	cause := errors.New(privacyMarker)
	cancel(cause)
	_, first := CheckProvider(ctx, images, declaration(agentconfig.GeminiAPIKeyAuth), facts)
	_, second := checkProvider(ctx, images, declaration(agentconfig.GeminiAPIKeyAuth), facts, syntheticProfile())
	result, third := RenderProvider(ctx, plan, []SelectedSecret{secret})
	if result.get[candidateData]() != nil {
		test.Fatal("canceled render returned candidate")
	}
	for _, err := range []error{first, second, third} {
		if !errors.Is(err, context.Canceled) || errors.Is(err, cause) || strings.Contains(fmt.Sprint(err), privacyMarker) {
			test.Fatal("private cause escaped fixed cancellation classification")
		}
	}
}

func TestGeminiLocalInfluencesAndOutputBudget(test *testing.T) {
	for _, name := range deniedEnv {
		images := syntheticImages(test, name+"=x", `{}`)
		facts := syntheticResolution(test, images)
		profile := syntheticProfile()
		profile.ignoredEnv = append(profile.ignoredEnv, name)
		result, err := checkProvider(test.Context(), images, declaration(agentconfig.GeminiAPIKeyAuth), facts, profile)
		facts.Destroy()
		images.Destroy()
		if !errors.Is(err, ErrUnresolvedLocalAuth) || result.get[planData]() != nil {
			test.Fatal("known influencing env accepted as inert")
		}
	}
	for _, raw := range []string{`{"security":{"auth":{"useExternal":false}}}`, `{"security":{"auth":{"enforcedType":"gemini-api-key"}}}`, `{"modelConfigs":{}}`, `{"unknown":true}`, `{"model":null}`} {
		images := syntheticImages(test, "", raw)
		facts := syntheticResolution(test, images)
		result, err := checkProvider(test.Context(), images, declaration(agentconfig.GeminiAPIKeyAuth), facts, syntheticProfile())
		facts.Destroy()
		images.Destroy()
		if err == nil || result.get[planData]() != nil {
			test.Fatal("unresolved settings or wrong parent accepted")
		}
	}
	largeEnv := "#" + strings.Repeat("x", 190*1024-1)
	largeSettings := `{"ui":"` + strings.Repeat("x", 190*1024-9) + `"}`
	images := syntheticImages(test, largeEnv, largeSettings)
	facts := syntheticResolution(test, images)
	if result, err := checkProvider(test.Context(), images, declaration(agentconfig.GeminiAPIKeyAuth), facts, syntheticProfile()); !errors.Is(err, ErrCodecLimit) || result.get[planData]() != nil {
		test.Fatal("worst-case pair output not reserved before material")
	}
	facts.Destroy()
	images.Destroy()
	prefix, suffix := `{"ui":"`, `","security":{"auth":{"selectedType":"oauth-personal"}}}`
	settings := prefix + strings.Repeat("x", MaxPairBytes/2-len(prefix)-len(suffix)) + suffix
	images = syntheticImages(test, "#"+strings.Repeat("x", MaxPairBytes/2-1), settings)
	facts = syntheticResolution(test, images)
	plan, err := checkProvider(test.Context(), images, declaration(agentconfig.GeminiNativeLogin), facts, syntheticProfile())
	facts.Destroy()
	images.Destroy()
	if err != nil {
		test.Fatal("exact pair output budget rejected")
	}
	defer plan.Destroy()
	candidate, err := RenderProvider(test.Context(), plan, nil)
	if err != nil {
		test.Fatal("exact pair output failed")
	}
	defer candidate.Destroy()
	first, second := candidate.CopyImage(EnvRole), candidate.CopyImage(SettingsRole)
	defer first.Destroy()
	defer second.Destroy()
	if len(first.CopyBytes())+len(second.CopyBytes()) != MaxPairBytes {
		test.Fatal("exact pair output silently truncated")
	}
}
