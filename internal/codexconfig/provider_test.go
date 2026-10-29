package codexconfig

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func syntheticInputs(test *testing.T) (LocalImages, LocalResolution) {
	test.Helper()
	fixture := goldenBundle(test)
	config, _ := NewImage(Present, []byte(fixture.Config))
	auth, _ := NewImage(Present, []byte(fixture.Auth))
	catalog, _ := NewImage(Present, []byte(fixture.Catalog))
	images, err := NewImages(config, auth, catalog)
	if err != nil {
		test.Fatal("bundle capture")
	}
	return images, syntheticResolution(test, Absent, Managed, nil, File, KeyringNotObserved, CredentialLogin)
}
func syntheticPlan(test *testing.T) ProviderPlan {
	test.Helper()
	images, resolution := syntheticInputs(test)
	plan, err := checkProvider(test.Context(), images, goldenDeclaration(test), resolution, syntheticProfile())
	images.Destroy()
	resolution.Destroy()
	if err != nil {
		test.Fatal("complete synthetic Check failed")
	}
	return plan
}
func syntheticSecret(test *testing.T) SelectedSecret {
	test.Helper()
	secret, err := NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte(goldenBundle(test).Secret))
	if err != nil {
		test.Fatal("secret fixture")
	}
	return secret
}

func TestCodexProviderBundle(test *testing.T) {
	images, resolution := syntheticInputs(test)
	if plan, err := CheckProvider(test.Context(), images, goldenDeclaration(test), resolution); !errors.Is(err, ErrUnsupportedNativeProfile) || plan.get[planData]() != nil {
		test.Fatal("production synthetic profile bypass")
	}
	plan := syntheticPlan(test)
	secret := syntheticSecret(test)
	candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
	if err != nil {
		test.Fatal("complete synthetic Render failed")
	}
	secret.Destroy()
	plan.Destroy()
	fixture := goldenBundle(test)
	if candidate.Action(ConfigRole) != Write || candidate.Action(CatalogRole) != Write || candidate.Action(AuthRole) != Keep {
		test.Fatal("artifact actions")
	}
	if !bytes.Equal(candidate.CopyImage(AuthRole).CopyBytes(), []byte(fixture.Auth)) || !bytes.Equal(candidate.CopyImage(CatalogRole).CopyBytes(), []byte(fixture.ExpectedCatalog)) {
		test.Fatal("whole bundle raw/golden mismatch")
	}
	config := candidate.CopyImage(ConfigRole).CopyBytes()
	if !bytes.Equal(config, []byte(fixture.ExpectedConfig)) {
		test.Fatal("complete nine-field config golden mismatch")
	}
	if bytes.Count(config, []byte(fixture.Secret)) != 1 || bytes.Contains(candidate.CopyImage(AuthRole).CopyBytes(), []byte(fixture.Secret)) || bytes.Contains(candidate.CopyImage(CatalogRole).CopyBytes(), []byte(fixture.Secret)) {
		test.Fatal("secret multiplicity/artifact leak")
	}
	copyImage := candidate.CopyImage(ConfigRole)
	candidate.Destroy()
	if !bytes.Equal(copyImage.CopyBytes(), config) {
		test.Fatal("candidate copy depends on destroyed source")
	}
	if candidate.CopyImage(ConfigRole).Presence() != PresenceInvalid {
		test.Fatal("destroy did not invalidate")
	}
}

func bindingBytes(test *testing.T, plan ProviderPlan) []byte {
	test.Helper()
	binding := plan.Binding()
	raw, err := json.Marshal(struct {
		Baselines   [3]BaselineBinding
		Declaration agentconfig.CodexProviderDeclarationV1
		Observation ObservationBinding
	}{binding.CopyBaselines(), binding.CopyDeclaration(), binding.CopyObservation()}, json.Deterministic(true))
	if err != nil {
		test.Fatal("trusted binding copy")
	}
	return raw
}
func TestCodexProviderBindingChanges(test *testing.T) {
	original := syntheticPlan(test)
	baseline := bindingBytes(test, original)
	fixture := goldenBundle(test)
	for index, value := range original.Binding().CopyBaselines() {
		if hex.EncodeToString(value.Digest[:]) != fixture.ExpectedBaselineDigests[index] {
			test.Fatal("independent baseline digest golden mismatch")
		}
	}
	if !reflect.DeepEqual(original.Preview(), fixture.ExpectedPreview) {
		test.Fatal("complete safe preview golden mismatch")
	}
	for _, role := range []Role{ConfigRole, AuthRole, CatalogRole} {
		images, resolution := syntheticInputs(test)
		values := images.get[imagesData]().images
		changed, _ := NewImage(Present, append(values[int(role)-1].CopyBytes(), '\n'))
		values[int(role)-1] = changed
		modified, _ := NewImages(values[0], values[1], values[2])
		plan, err := checkProvider(test.Context(), modified, goldenDeclaration(test), resolution, syntheticProfile())
		if err != nil || bytes.Equal(baseline, bindingBytes(test, plan)) {
			test.Fatal("baseline binding omitted")
		}
		if role == ConfigRole {
			value := plan.Binding().CopyBaselines()[0]
			if hex.EncodeToString(value.Digest[:]) != fixture.ConfigNewlineDigest {
				test.Fatal("binding change fixture mismatch")
			}
		}
	}
	for _, change := range []func(*agentconfig.CodexProviderDeclarationV1){
		func(value *agentconfig.CodexProviderDeclarationV1) { value.Endpoint = "https://other.invalid" },
		func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].SupportsParallelToolCalls = new(true)
		},
		func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].SupportsParallelToolCalls = nil
		},
		func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].InputModalities = new([]agentconfig.CodexModality{"text", "image"})
		},
		func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].ReasoningLevels = new([]agentconfig.CodexReasoningEffort{"max", "none", "high"})
		},
	} {
		images, resolution := syntheticInputs(test)
		declaration := goldenDeclaration(test)
		change(&declaration)
		plan, err := checkProvider(test.Context(), images, declaration, resolution, syntheticProfile())
		if err != nil || bytes.Equal(baseline, bindingBytes(test, plan)) {
			test.Fatal("declaration presence/order binding omitted")
		}
	}
	for _, variant := range []string{"witness", "template", "profile-metadata", "policy-result"} {
		images, resolution := syntheticInputs(test)
		profile := syntheticProfile()
		switch variant {
		case "witness":
			resolution.get[resolutionData]().witness[0] = 2
		case "template":
			profile.modelDefaults = append(profile.modelDefaults, ' ')
		case "profile-metadata":
			profile.ignoredRoot = append(profile.ignoredRoot, "ignored-only-in-test")
		case "policy-result":
			resolution.get[resolutionData]().auth.fileLogin = NoCredentialLogin
		}
		plan, err := checkProvider(test.Context(), images, goldenDeclaration(test), resolution, profile)
		if err != nil || bytes.Equal(baseline, bindingBytes(test, plan)) {
			test.Fatal("observation/profile/policy binding omitted")
		}
	}
	copyDeclaration := original.Binding().CopyDeclaration()
	*copyDeclaration.Catalog.Models[0].ContextWindow = 1
	copyObservation := original.Binding().CopyObservation()
	copyObservation.ProfileID = "modified"
	if !bytes.Equal(baseline, bindingBytes(test, original)) {
		test.Fatal("binding copy alias")
	}
}

func TestCodexRenderSecretExactness(test *testing.T) {
	plan := syntheticPlan(test)
	secret := syntheticSecret(test)
	for _, selected := range [][]SelectedSecret{nil, {SelectedSecret{}}, {secret, secret}} {
		candidate, err := RenderProvider(test.Context(), plan, selected)
		if !errors.Is(err, ErrInvalidSecret) || candidate.get[candidateData]() != nil {
			test.Fatal("material exact set violated")
		}
	}
	secret.Destroy()
	if _, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret}); !errors.Is(err, ErrInvalidSecret) {
		test.Fatal("destroyed secret reused")
	}
}

func TestCodexProviderZeroOnFailure(test *testing.T) {
	images, resolution := syntheticInputs(test)
	broken, _ := NewImage(Present, []byte(`{"models":`))
	values := images.get[imagesData]().images
	invalid, _ := NewImages(values[0], values[1], broken)
	if plan, err := checkProvider(test.Context(), invalid, goldenDeclaration(test), resolution, syntheticProfile()); err == nil || plan.get[planData]() != nil {
		test.Fatal("partial config plan after catalog failure")
	}
	large, _ := NewImage(Present, []byte("note='"+strings.Repeat("x", MaxImageBytes-7000)+"'\n"))
	largeInputs, _ := NewImages(large, values[1], values[2])
	plan, err := checkProvider(test.Context(), largeInputs, goldenDeclaration(test), resolution, syntheticProfile())
	if err != nil {
		test.Fatal("large bounded Check fixture")
	}
	secret, _ := NewSelectedSecret(agentconfig.CodexProviderKeySecret, bytes.Repeat([]byte("x"), 8192))
	if candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret}); !errors.Is(err, ErrCodecLimit) || candidate.get[candidateData]() != nil {
		test.Fatal("partial bundle after output limit")
	}
}

func TestCodexProviderCancelAndConcurrentCopies(test *testing.T) {
	images, resolution := syntheticInputs(test)
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if plan, err := checkProvider(ctx, images, goldenDeclaration(test), resolution, syntheticProfile()); !errors.Is(err, context.Canceled) || plan.get[planData]() != nil {
		test.Fatal("Check cancellation")
	}
	plan := syntheticPlan(test)
	secret := syntheticSecret(test)
	if candidate, err := RenderProvider(ctx, plan, []SelectedSecret{secret}); !errors.Is(err, context.Canceled) || candidate.get[candidateData]() != nil {
		test.Fatal("Render cancellation")
	}
	var workers sync.WaitGroup
	results := make(chan bool, 8)
	for range 8 {
		workers.Go(func() {
			candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
			if err != nil {
				results <- false
				return
			}
			copyImage := candidate.CopyImage(ConfigRole)
			candidate.Destroy()
			results <- bytes.Contains(copyImage.CopyBytes(), []byte(goldenBundle(test).Secret))
		})
	}
	workers.Wait()
	close(results)
	completed := 0
	for valid := range results {
		if !valid {
			test.Fatal("concurrent Render aliases state")
		}
		completed++
	}
	if completed != 8 {
		test.Fatal("missing concurrent completion")
	}
	for _, value := range []any{plan, plan.Binding()} {
		assertOpaque(test, value)
		assertOpaque(test, struct{ hidden any }{value})
	}
}

type cancelAtContext struct {
	context.Context
	cancel context.CancelFunc
	at     int64
	calls  atomic.Int64
}

func (value *cancelAtContext) Err() error {
	if value.calls.Add(1) == value.at {
		value.cancel()
	}
	return value.Context.Err()
}

func TestCodexProviderLateCancellation(test *testing.T) {
	images, resolution := syntheticInputs(test)
	base, cancel := context.WithCancel(test.Context())
	defer cancel()
	counter := &cancelAtContext{Context: base, cancel: cancel, at: -1}
	if _, err := checkProvider(counter, images, goldenDeclaration(test), resolution, syntheticProfile()); err != nil {
		test.Fatal("counting Check")
	}
	for _, phase := range []int64{2, 20, counter.calls.Load()} {
		base, cancel := context.WithCancel(test.Context())
		ctx := &cancelAtContext{Context: base, cancel: cancel, at: phase}
		plan, err := checkProvider(ctx, images, goldenDeclaration(test), resolution, syntheticProfile())
		cancel()
		if !errors.Is(err, context.Canceled) || plan.get[planData]() != nil {
			test.Fatal("late Check cancellation returned plan")
		}
	}
	plan := syntheticPlan(test)
	secret := syntheticSecret(test)
	base, cancel = context.WithCancel(test.Context())
	defer cancel()
	counter = &cancelAtContext{Context: base, cancel: cancel, at: -1}
	if _, err := RenderProvider(counter, plan, []SelectedSecret{secret}); err != nil {
		test.Fatal("counting Render")
	}
	for _, phase := range []int64{2, 20, counter.calls.Load()} {
		base, cancel := context.WithCancel(test.Context())
		ctx := &cancelAtContext{Context: base, cancel: cancel, at: phase}
		candidate, err := RenderProvider(ctx, plan, []SelectedSecret{secret})
		cancel()
		if !errors.Is(err, context.Canceled) || candidate.get[candidateData]() != nil {
			test.Fatal("late Render cancellation returned bundle")
		}
	}
}

func TestCodexProviderRetainedEffort(test *testing.T) {
	declaration := goldenDeclaration(test)
	declaration.ReasoningEffort = nil
	declaration.Catalog.Models[0].ReasoningLevels = nil
	declaration.Catalog.Models[0].DefaultReasoningLevel = nil
	config, _ := NewImage(Present, []byte("model_reasoning_effort='none'\n"))
	auth, _ := NewImage(Missing, nil)
	catalog, _ := NewImage(Present, []byte(`{"models":[{"slug":"Case/model:1","description":"old","supported_reasoning_levels":[{"effort":"none","description":"old"}],"default_reasoning_level":"none"}]}`))
	images, _ := NewImages(config, auth, catalog)
	resolution := syntheticResolution(test, Absent, Managed, []string{"Case/model:1"}, File, KeyringNotObserved, NoCredentialLogin)
	plan, err := checkProvider(test.Context(), images, declaration, resolution, syntheticProfile())
	if err != nil {
		test.Fatal("compatible retained effort rejected despite owned catalog proof")
	}
	candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{syntheticSecret(test)})
	if err != nil || !bytes.Contains(candidate.CopyImage(ConfigRole).CopyBytes(), []byte("model_reasoning_effort='none'")) {
		test.Fatal("retained effort changed")
	}
}

func TestCodexProviderAbsenceAndCapture(test *testing.T) {
	declaration := goldenDeclaration(test)
	declaration.Catalog = nil
	declaration.ReasoningEffort = nil
	config, _ := NewImage(Present, []byte("model_catalog_json='foreign.json'\n"))
	missing, _ := NewImage(Missing, nil)
	unread, _ := NewImage(NotObserved, nil)
	images, _ := NewImages(config, missing, unread)
	resolution := syntheticResolution(test, Absent, Foreign, nil, File, KeyringNotObserved, NoCredentialLogin)
	plan, err := checkProvider(test.Context(), images, declaration, resolution, syntheticProfile())
	if err != nil {
		test.Fatal("unselected foreign catalog was followed")
	}
	declaration.Model = "caller-changed"
	images.Destroy()
	resolution.Destroy()
	secret := syntheticSecret(test)
	candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
	if err != nil || candidate.Action(CatalogRole) != Keep || candidate.Action(AuthRole) != Keep || candidate.CopyImage(CatalogRole).Presence() != NotObserved || candidate.CopyImage(AuthRole).Presence() != Missing {
		test.Fatal("absence contract")
	}
	if !bytes.Contains(candidate.CopyImage(ConfigRole).CopyBytes(), []byte("model_catalog_json='foreign.json'")) || candidate.Binding().CopyDeclaration().Model != "Case/model:1" {
		test.Fatal("unselected pointer/captured declaration changed")
	}
	preview := plan.Preview()
	preview[0].Field = 0
	if plan.Preview()[0].Field == 0 {
		test.Fatal("preview aliases plan")
	}
	for _, value := range []any{plan, plan.Binding(), candidate, candidate.Binding()} {
		assertOpaque(test, value)
		assertOpaque(test, struct{ hidden any }{value})
	}
	var text bytes.Buffer
	encoded, _ := json.Marshal(plan.Preview())
	text.Write(encoded)
	if bytes.Contains(text.Bytes(), []byte("Case/model:1")) || bytes.Contains(text.Bytes(), []byte("foreign.json")) {
		test.Fatal("preview contains input values")
	}
}

func goldenDeclaration(test *testing.T) agentconfig.CodexProviderDeclarationV1 {
	test.Helper()
	raw, err := os.ReadFile("testdata/a-v1-golden.json")
	if err != nil {
		test.Fatal("golden unavailable")
	}
	var fixture struct {
		SourceCommit string                                 `json:"sourceCommit"`
		SourceBlob   string                                 `json:"sourceBlob"`
		Declaration  agentconfig.CodexProviderDeclarationV1 `json:"declaration"`
	}
	if json.Unmarshal(raw, &fixture, json.RejectUnknownMembers(true)) != nil || fixture.SourceBlob != "6004a34d6c5923725354cdd0e606614cf405b341" || fixture.SourceCommit != "c7d8ab60c2f7ae6d2c4b18b1163b72fe65d59dc8" {
		test.Fatal("golden identity")
	}
	return fixture.Declaration
}

func TestCodexADeclarationParity(test *testing.T) {
	declaration := goldenDeclaration(test)
	if validateDeclaration(declaration) != nil {
		test.Fatal("full A golden rejected")
	}
	cloned := cloneDeclaration(declaration)
	if !reflect.DeepEqual(cloned, declaration) {
		test.Fatal("A presence/order copy")
	}
	cloned.Catalog.Models[0].InputModalities = new([]agentconfig.CodexModality{"text"})
	if reflect.DeepEqual(cloned, declaration) {
		test.Fatal("A clone alias")
	}
	changes := map[string]func(*agentconfig.CodexProviderDeclarationV1){
		"version":  func(value *agentconfig.CodexProviderDeclarationV1) { value.APIVersion = 2 },
		"target":   func(value *agentconfig.CodexProviderDeclarationV1) { value.Target = "other" },
		"provider": func(value *agentconfig.CodexProviderDeclarationV1) { value.ProviderID = "foreign" },
		"wire":     func(value *agentconfig.CodexProviderDeclarationV1) { value.WireAPI = "chat" },
		"slot": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.RequiredSlots = append(value.RequiredSlots, value.CredentialReference)
		},
		"reference":       func(value *agentconfig.CodexProviderDeclarationV1) { value.CredentialReference = "other" },
		"policy":          func(value *agentconfig.CodexProviderDeclarationV1) { value.LocalAuthPolicy = "other" },
		"http":            func(value *agentconfig.CodexProviderDeclarationV1) { value.Endpoint = "http://codec.invalid" },
		"credentials":     func(value *agentconfig.CodexProviderDeclarationV1) { value.Endpoint = "https://user@codec.invalid" },
		"port":            func(value *agentconfig.CodexProviderDeclarationV1) { value.Endpoint = "https://codec.invalid:65536" },
		"encoded-control": func(value *agentconfig.CodexProviderDeclarationV1) { value.Endpoint = "https://codec.invalid/%0a" },
		"empty-query":     func(value *agentconfig.CodexProviderDeclarationV1) { value.Endpoint += "?" },
		"model":           func(value *agentconfig.CodexProviderDeclarationV1) { value.Model = " model" },
		"effort": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.ReasoningEffort = new(agentconfig.CodexReasoningEffort("alien"))
		},
		"empty-catalog": func(value *agentconfig.CodexProviderDeclarationV1) { value.Catalog.Models = nil },
		"duplicate": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models = append(value.Catalog.Models, value.Catalog.Models[0])
		},
		"missing-primary": func(value *agentconfig.CodexProviderDeclarationV1) { value.Catalog.Models[0].Model = "other" },
		"context-zero": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].ContextWindow = new(uint64(0))
		},
		"context-over": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].ContextWindow = new(uint64(2147483648))
		},
		"display-space": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].DisplayName = new(" leading")
		},
		"instructions-control": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].BaseInstructions = new("bad\x00")
		},
		"modalities-duplicate": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].InputModalities = new([]agentconfig.CodexModality{"text", "text"})
		},
		"levels-duplicate": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].ReasoningLevels = new([]agentconfig.CodexReasoningEffort{"high", "high"})
		},
		"default-without-levels": func(value *agentconfig.CodexProviderDeclarationV1) { value.Catalog.Models[0].ReasoningLevels = nil },
		"effort-incompatible": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.ReasoningEffort = new(agentconfig.CodexReasoningEffort("minimal"))
		},
	}
	for name, change := range changes {
		test.Run(name, func(test *testing.T) {
			value := goldenDeclaration(test)
			change(&value)
			if !errors.Is(validateDeclaration(value), ErrInvalidDeclaration) {
				test.Fatal("invalid A accepted")
			}
		})
	}
	declaration.Catalog = nil
	declaration.ReasoningEffort = nil
	if validateDeclaration(declaration) != nil {
		test.Fatal("minimal A rejected")
	}
}

func TestCodexInputPresenceAndClones(test *testing.T) {
	body := []byte("raw")
	image, err := NewImage(Present, body)
	if err != nil {
		test.Fatal("capture")
	}
	body[0] = 'X'
	copyBody := image.CopyBytes()
	copyBody[0] = 'Y'
	if !bytes.Equal(image.CopyBytes(), []byte("raw")) {
		test.Fatal("input/result alias")
	}
	missing, _ := NewImage(Missing, nil)
	unobserved, _ := NewImage(NotObserved, nil)
	images, err := NewImages(image, unobserved, missing)
	if err != nil {
		test.Fatal("images")
	}
	image.Destroy()
	if !bytes.Equal(images.CopyImage(ConfigRole).CopyBytes(), []byte("raw")) {
		test.Fatal("bundle aliases input")
	}
	if images.CopyImage(AuthRole).Presence() != NotObserved {
		test.Fatal("absence collapsed")
	}
	for _, presence := range []Presence{PresenceInvalid, Missing, NotObserved} {
		if _, err := NewImage(presence, []byte("bad")); !errors.Is(err, ErrInvalidLocalImage) {
			test.Fatal("invalid presence")
		}
	}
	if _, err := NewImages(unobserved, missing, missing); err == nil {
		test.Fatal("unobserved config")
	}
	if _, err := NewImage(Present, bytes.Repeat([]byte("x"), MaxImageBytes+1)); !errors.Is(err, ErrCodecLimit) {
		test.Fatal("image bound")
	}
	if _, err := NewImage(Present, nil); err != nil {
		test.Fatal("present empty TOML capture")
	}
}

func TestCodexSelectedSecret(test *testing.T) {
	for _, size := range []int{1, 8192} {
		body := bytes.Repeat([]byte("x"), size)
		secret, err := NewSelectedSecret(agentconfig.CodexProviderKeySecret, body)
		if err != nil {
			test.Fatal("valid secret rejected")
		}
		secret.Destroy()
		if body[0] != 'x' {
			test.Fatal("caller material modified")
		}
	}
	for _, body := range [][]byte{nil, bytes.Repeat([]byte("x"), 8193), {0xff}, []byte("bad\x00"), []byte("bad\r"), []byte("bad\n")} {
		if _, err := NewSelectedSecret(agentconfig.CodexProviderKeySecret, body); !errors.Is(err, ErrInvalidSecret) {
			test.Fatal("invalid secret accepted")
		}
	}
	if _, err := NewSelectedSecret("other", []byte("valid")); !errors.Is(err, ErrInvalidSecret) {
		test.Fatal("wrong slot")
	}
	if _, err := NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte(" exact ")); err != nil {
		test.Fatal("secret trimmed or rejected")
	}
}

func TestCodexADeclarationBounds(test *testing.T) {
	declaration := goldenDeclaration(test)
	for _, field := range []string{"endpoint", "display", "instructions", "model"} {
		value := cloneDeclaration(declaration)
		switch field {
		case "endpoint":
			value.Endpoint = "https://codec.invalid/" + strings.Repeat("x", 2048)
		case "display":
			value.Catalog.Models[0].DisplayName = new(strings.Repeat("x", 257))
		case "instructions":
			value.Catalog.Models[0].BaseInstructions = new(strings.Repeat("x", 2049))
		case "model":
			value.Model = strings.Repeat("x", 129)
		}
		if validateDeclaration(value) == nil {
			test.Fatal("A scalar limit ignored")
		}
	}
	declaration.Catalog.Models = make([]agentconfig.CodexCatalogModelDesired, 33)
	if validateDeclaration(declaration) == nil {
		test.Fatal("model count ignored")
	}
	declaration = goldenDeclaration(test)
	prefix := "https://codec.invalid/"
	declaration.Endpoint = prefix + strings.Repeat("x", 2048-len(prefix))
	declaration.Model = strings.Repeat("m", 128)
	declaration.Catalog.Models[0].Model = declaration.Model
	declaration.Catalog.Models[0].DisplayName = new(strings.Repeat("D", 256))
	declaration.Catalog.Models[0].BaseInstructions = new(strings.Repeat("I", 2048))
	if validateDeclaration(declaration) != nil {
		test.Fatal("valid A scalar boundaries rejected")
	}
	for index := 1; index < 16; index++ {
		model := declaration.Catalog.Models[0]
		model.Model = "different-" + strings.Repeat("m", index)
		declaration.Catalog.Models = append(declaration.Catalog.Models, model)
	}
	if validateDeclaration(declaration) == nil {
		test.Fatal("cumulative typed catalog limit ignored")
	}
}

func TestCodexProviderCaptureAndIndependentDestroy(test *testing.T) {
	images, resolution := syntheticInputs(test)
	declaration := goldenDeclaration(test)
	profile := syntheticProfile()
	first, err := checkProvider(test.Context(), images, declaration, resolution, profile)
	if err != nil {
		test.Fatal("capture first")
	}
	second, err := checkProvider(test.Context(), images, declaration, resolution, profile)
	if err != nil {
		test.Fatal("capture second")
	}
	*declaration.Catalog.Models[0].ContextWindow = 1
	(*declaration.Catalog.Models[0].ReasoningLevels)[0] = "low"
	*declaration.Catalog.Models[0].BaseInstructions = "caller changed"
	declaration.RequiredSlots[0] = "wrong"
	resolution.get[resolutionData]().witness[0] = 9
	profile.modelDefaults[0] = 'x'
	profile.descriptions["none"] = "caller changed"
	first.Destroy()
	images.Destroy()
	resolution.Destroy()
	candidate, err := RenderProvider(test.Context(), second, []SelectedSecret{syntheticSecret(test)})
	if err != nil || !bytes.Equal(candidate.CopyImage(CatalogRole).CopyBytes(), []byte(goldenBundle(test).ExpectedCatalog)) {
		test.Fatal("captured caller or other plan aliases")
	}
	binding := second.Binding()
	binding.Destroy()
	if second.Binding().CopyObservation().Witness[0] != 1 {
		test.Fatal("binding destroy aliases plan")
	}
	if len(second.Preview()) < 10 || !second.Preview()[8].SecretPlaceholder {
		test.Fatal("safe preview missing exact secret placeholder")
	}
}

func TestCodexProviderAuthBranches(test *testing.T) {
	for _, fixture := range []struct {
		store    AuthStore
		keyring  KeyringState
		login    LoginState
		presence Presence
		want     bool
	}{
		{File, KeyringNotObserved, CredentialLogin, Present, true}, {File, KeyringNotObserved, NoCredentialLogin, Missing, false},
		{Keyring, FoundCredentialLogin, LoginNotObserved, NotObserved, true}, {Keyring, KeyringMissing, LoginNotObserved, NotObserved, false},
		{Auto, FoundCredentialLogin, LoginUnknown, NotObserved, true}, {Auto, KeyringMissing, NoCredentialLogin, Missing, false},
	} {
		images, _ := syntheticInputs(test)
		values := images.get[imagesData]().images
		var body []byte
		if fixture.presence == Present {
			body = []byte(`{"opaque":"not inspected for login"}`)
		}
		auth, _ := NewImage(fixture.presence, body)
		images, _ = NewImages(values[0], auth, values[2])
		profile := syntheticProfile()
		profile.defaultStore = fixture.store
		resolution := syntheticResolution(test, Absent, Managed, nil, fixture.store, fixture.keyring, fixture.login)
		plan, err := checkProvider(test.Context(), images, goldenDeclaration(test), resolution, profile)
		if err != nil || plan.Binding().CopyObservation().RequiresOpenAIAuth != fixture.want {
			test.Fatal("whole bundle auth branch")
		}
		candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{syntheticSecret(test)})
		if err != nil || candidate.CopyImage(AuthRole).Presence() != fixture.presence || !bytes.Equal(candidate.CopyImage(AuthRole).CopyBytes(), body) || candidate.Action(AuthRole) != Keep {
			test.Fatal("auth branch changed baseline")
		}
	}
}
