package codexconfig

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func alwaysOnProfile() nativeProfile {
	profile := syntheticProfile()
	profile.parallel = parallelAlwaysOn
	return profile
}

func TestCodexParallelAlwaysOnRejectsEveryFalse(test *testing.T) {
	for _, position := range []int{0, 1} {
		test.Run([]string{"selected", "unselected"}[position], func(test *testing.T) {
			images, resolution := syntheticInputs(test)
			defer images.Destroy()
			defer resolution.Destroy()
			declaration := goldenDeclaration(test)
			declaration.Catalog.Models[0].SupportsParallelToolCalls = new(true)
			declaration.Catalog.Models = append(declaration.Catalog.Models, agentconfig.CodexCatalogModelDesired{Model: "unselected", SupportsParallelToolCalls: new(true)})
			declaration.Catalog.Models[position].SupportsParallelToolCalls = new(false)
			original := cloneDeclaration(declaration)
			plan, err := checkProvider(test.Context(), images, declaration, resolution, alwaysOnProfile())
			defer plan.Destroy()
			if !errors.Is(err, ErrUnsupportedNativeProfile) || plan.get[planData]() != nil || !reflect.DeepEqual(declaration, original) {
				test.Fatal("AlwaysOn accepted false, returned a plan, or changed the declaration")
			}
		})
	}
}

func TestCodexParallelAlwaysOnPreservesPresence(test *testing.T) {
	var bindings [][]byte
	for _, value := range []*bool{nil, new(true)} {
		images, resolution := syntheticInputs(test)
		declaration := goldenDeclaration(test)
		declaration.Catalog.Models[0].SupportsParallelToolCalls = value
		plan, err := checkProvider(test.Context(), images, declaration, resolution, alwaysOnProfile())
		images.Destroy()
		resolution.Destroy()
		if err != nil {
			test.Fatal("supported AlwaysOn declaration rejected")
		}
		captured := plan.Binding()
		if !reflect.DeepEqual(captured.CopyDeclaration(), declaration) {
			test.Fatal("AlwaysOn collapsed optional declaration presence")
		}
		captured.Destroy()
		bindings = append(bindings, bindingBytes(test, plan))
		for _, preview := range plan.Preview() {
			if preview.Field == FieldParallel && preview.Change != ChangeNativeAlwaysOn {
				test.Fatal("preview pretended an ineffective catalog field was written")
			}
		}
		secret := syntheticSecret(test)
		candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
		secret.Destroy()
		plan.Destroy()
		if err != nil {
			test.Fatal("supported AlwaysOn render failed")
		}
		image := candidate.CopyImage(CatalogRole)
		if bytes.Contains(image.CopyBytes(), []byte("supports_parallel_tool_calls")) {
			test.Fatal("AlwaysOn manufactured an ineffective catalog control")
		}
		image.Destroy()
		candidate.Destroy()
	}
	if bytes.Equal(bindings[0], bindings[1]) {
		test.Fatal("absence and explicit true share a binding")
	}
}

func TestCodexParallelAlwaysOnExistingCatalog(test *testing.T) {
	declaration := goldenDeclaration(test)
	declaration.ReasoningEffort = nil
	declaration.Catalog.Models = []agentconfig.CodexCatalogModelDesired{{Model: "Case/model:1"}}
	for _, literal := range []string{"false", "null", "0", `"true"`, "true"} {
		image, err := NewImage(Present, []byte(`{"models":[{ "slug":"Case/model:1", "description":"keep", "supports_parallel_tool_calls" : `+literal+`, "opaque":9007199254740993123456789}]}`))
		if err != nil {
			test.Fatal("catalog fixture")
		}
		output, _, _, err := rewriteCatalog(test.Context(), image, declaration, ownershipData{catalog: Managed, models: []string{"Case/model:1"}}, alwaysOnProfile())
		image.Destroy()
		if literal == "true" {
			if err != nil || !bytes.Contains(output, []byte(`"supports_parallel_tool_calls" : true`)) || !bytes.Contains(output, []byte(`"opaque":9007199254740993123456789`)) {
				test.Fatal("existing inert true or unrelated bytes were not preserved")
			}
		} else if err == nil || len(output) != 0 {
			test.Fatal("AlwaysOn accepted an existing false or malformed legacy field")
		}
	}
}

func TestCodexParallelAlwaysOnTemplateAndMode(test *testing.T) {
	for _, mode := range []parallelMode{parallelUnverified, parallelMode(3), parallelMode(255)} {
		profile := syntheticProfile()
		profile.parallel = mode
		if !errors.Is(validateProfile(test.Context(), profile), ErrUnsupportedNativeProfile) {
			test.Fatal("unknown parallel capability accepted")
		}
	}
	for _, literal := range []string{"true", "false"} {
		profile := alwaysOnProfile()
		profile.modelDefaults = []byte(`{"description":"keep","supports_parallel_tool_calls":` + literal + `}`)
		if !errors.Is(validateProfile(test.Context(), profile), ErrUnsupportedNativeProfile) {
			test.Fatal("AlwaysOn template manufactured an ineffective native field")
		}
	}
	if profileDigest(syntheticProfile()) == profileDigest(alwaysOnProfile()) {
		test.Fatal("capability mode did not change profile identity")
	}
}

func TestCodexParallelAlwaysOnKeptAndRemovedCatalog(test *testing.T) {
	for _, presence := range []Presence{Missing, NotObserved, Present} {
		config, _ := NewImage(Present, []byte("model_catalog_json='cognate-model-catalog.json'\n"))
		auth, _ := NewImage(Missing, nil)
		var raw []byte
		if presence == Present {
			raw = []byte(`{"models":[{"slug":"Case/model:1","description":"keep","supports_parallel_tool_calls":false}]}`)
		}
		catalog, _ := NewImage(presence, raw)
		images, _ := NewImages(config, auth, catalog)
		resolution := syntheticResolution(test, Absent, Managed, []string{"Case/model:1"}, File, KeyringNotObserved, NoCredentialLogin)
		declaration := goldenDeclaration(test)
		declaration.Catalog = nil
		plan, err := checkProvider(test.Context(), images, declaration, resolution, alwaysOnProfile())
		if err == nil || plan.get[planData]() != nil {
			test.Fatal("kept catalog bypassed AlwaysOn observation or false rejection")
		}
		plan.Destroy()
		images.Destroy()
		resolution.Destroy()
		config.Destroy()
		auth.Destroy()
		catalog.Destroy()
	}
	image, _ := NewImage(Present, []byte(`{"models":[{"slug":"old","description":"keep","supports_parallel_tool_calls":false}]}`))
	defer image.Destroy()
	declaration := goldenDeclaration(test)
	declaration.Catalog.Models[0].SupportsParallelToolCalls = new(true)
	if output, _, _, err := rewriteCatalog(test.Context(), image, declaration, ownershipData{catalog: Managed, models: []string{"old"}}, alwaysOnProfile()); err == nil || len(output) != 0 {
		test.Fatal("removing an existing model silently erased its false declaration")
	}
}

func TestCodexParallelAlwaysOnRejectsLite(test *testing.T) {
	for _, literal := range []string{"true", `"false"`, "null", "false"} {
		profile := alwaysOnProfile()
		profile.ignoredModel = append(profile.ignoredModel, "use_responses_lite")
		config, _ := NewImage(Missing, nil)
		auth, _ := NewImage(Missing, nil)
		catalog, _ := NewImage(Present, []byte(`{"models":[{"slug":"Case/model:1","description":"keep","use_responses_lite":`+literal+`}]}`))
		images, _ := NewImages(config, auth, catalog)
		resolution := syntheticResolution(test, Absent, Managed, []string{"Case/model:1"}, File, KeyringNotObserved, NoCredentialLogin)
		declaration := goldenDeclaration(test)
		declaration.Catalog.Models[0].SupportsParallelToolCalls = new(true)
		plan, err := checkProvider(test.Context(), images, declaration, resolution, profile)
		if literal == "false" {
			if err != nil {
				test.Fatal("explicit Full Responses mode rejected")
			}
		} else if err == nil || plan.get[planData]() != nil {
			test.Fatal("Lite or ambiguous mode accepted as Full Responses")
		}
		plan.Destroy()
		images.Destroy()
		config.Destroy()
		auth.Destroy()
		catalog.Destroy()
		resolution.Destroy()
		profile.modelDefaults = []byte(`{"description":"keep","use_responses_lite":` + literal + `}`)
		if (validateProfile(test.Context(), profile) == nil) != (literal == "false") {
			test.Fatal("template Lite mode was not validated")
		}
	}
}

func TestCodexParallelAlwaysOnCancellationAndProductionGate(test *testing.T) {
	images, resolution := syntheticInputs(test)
	defer images.Destroy()
	defer resolution.Destroy()
	declaration := goldenDeclaration(test)
	declaration.Catalog.Models[0].SupportsParallelToolCalls = new(true)
	ctx, cancel := context.WithCancelCause(test.Context())
	cancel(errors.New("private-parallel-cause"))
	plan, err := checkProvider(ctx, images, declaration, resolution, alwaysOnProfile())
	if !errors.Is(err, context.Canceled) || plan.get[planData]() != nil || err.Error() != context.Canceled.Error() {
		test.Fatal("parallel mapping lost cancellation or exposed its cause")
	}
	plan, err = CheckProvider(test.Context(), images, declaration, resolution)
	if !errors.Is(err, ErrUnsupportedNativeProfile) || plan.get[planData]() != nil {
		test.Fatal("synthetic capability enabled production profile")
	}
}
