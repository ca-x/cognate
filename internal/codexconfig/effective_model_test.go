package codexconfig

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func TestEffective0166SelectedInstructionPrerequisite(test *testing.T) {
	var pinned struct {
		Models []map[string]jsontext.Value `json:"models"`
	}
	if err := json.Unmarshal([]byte(native0166EffectiveModels), &pinned); err != nil {
		test.Fatal("pinned instruction fixture unavailable")
	}
	var baseline map[string]jsontext.Value
	for _, model := range pinned.Models {
		var slug string
		if json.Unmarshal(model["slug"], &slug) == nil && slug == "gpt-5.5" {
			baseline = model
		}
	}
	if baseline == nil {
		test.Fatal("pinned Full model unavailable")
	}
	for _, scenario := range []struct {
		name, messages, legacy string
		refuse                 bool
	}{
		{name: "canonical-empty", messages: `{"instructions_template":""}`, legacy: `"LEGACY_MUST_NOT_OVERRIDE_EMPTY_CANONICAL"`},
		{name: "legacy-empty", legacy: `""`},
		{name: "both-absent", refuse: true},
		{name: "canonical-text-only", messages: `{"instructions_template":"CANONICAL"}`},
		{name: "canonical-empty-only", messages: `{"instructions_template":""}`},
		{name: "legacy-text-only", legacy: `"LEGACY"`},
		{name: "canonical-null-promotes-empty-legacy", messages: `{"instructions_template":null}`, legacy: `""`},
		{name: "messages-null-promotes-empty-legacy", messages: `null`, legacy: `""`},
		{name: "messages-empty-promotes-empty-legacy", messages: `{}`, legacy: `""`},
		{name: "both-null", messages: `null`, legacy: `null`, refuse: true},
		{name: "canonical-null-without-legacy", messages: `{"instructions_template":null}`, refuse: true},
		{name: "messages-empty-without-legacy", messages: `{}`, refuse: true},
		{name: "invalid-messages-type", messages: `[]`, legacy: `"LEGACY"`, refuse: true},
		{name: "invalid-canonical-type", messages: `{"instructions_template":123}`, legacy: `"LEGACY"`, refuse: true},
		{name: "invalid-legacy-type-with-canonical", messages: `{"instructions_template":""}`, legacy: `true`, refuse: true},
		{name: "invalid-legacy-type", legacy: `[]`, refuse: true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			model := make(map[string]jsontext.Value, len(baseline))
			for name, value := range baseline {
				model[name] = bytes.Clone(value)
			}
			delete(model, "model_messages")
			delete(model, "base_instructions")
			if scenario.messages != "" {
				model["model_messages"] = jsontext.Value(scenario.messages)
			}
			if scenario.legacy != "" {
				model["base_instructions"] = jsontext.Value(scenario.legacy)
			}
			catalog, err := json.Marshal(map[string]any{"models": []any{model}}, json.Deterministic(true))
			if err != nil {
				test.Fatal("instruction fixture encoding failed")
			}
			images, resolution := effective0166Fixture(test, Native0166EffectiveProfileID,
				"cli_auth_credentials_store='file'\nmodel_catalog_json='cognate-model-catalog.json'\n", catalog)
			plan, err := CheckProvider(test.Context(), images, effective0166Declaration("gpt-5.5"), resolution)
			defer plan.Destroy()
			if scenario.refuse {
				if !errors.Is(err, ErrUnsupportedNativeProfile) || plan.get[planData]() != nil {
					test.Fatal("selected instructions without a native string prerequisite admitted a plan before material")
				}
				return
			}
			if err != nil || plan.get[planData]() == nil {
				test.Fatal("legal canonical or promoted legacy string was refused")
			}
			selected, err := NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("SYNTHETIC_INSTRUCTION_BOUNDARY_KEY"))
			if err != nil {
				test.Fatal("synthetic selected fixture unavailable")
			}
			defer selected.Destroy()
			candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{selected})
			defer candidate.Destroy()
			if err != nil || candidate.get[candidateData]() == nil || candidate.Action(CatalogRole) != Keep || candidate.Action(AuthRole) != Keep {
				test.Fatal("legal instruction presence changed role Keep")
			}
			actual := candidate.CopyImage(CatalogRole)
			defer actual.Destroy()
			body := actual.CopyBytes()
			defer clear(body)
			if !bytes.Equal(body, catalog) {
				test.Fatal("instruction admission rewrote original foreign catalog bytes")
			}
		})
	}
}

func effective0166Fixture(test *testing.T, identifier, configText string, catalogBytes []byte) (LocalImages, LocalResolution) {
	test.Helper()
	config, err := NewImage(Present, []byte(configText))
	if err != nil {
		test.Fatal("config fixture")
	}
	test.Cleanup(config.Destroy)
	auth, err := NewImage(Missing, nil)
	if err != nil {
		test.Fatal("auth fixture")
	}
	test.Cleanup(auth.Destroy)
	presence, owner := Missing, Absent
	if catalogBytes != nil {
		presence, owner = Present, Foreign
	}
	catalog, err := NewImage(presence, catalogBytes)
	if err != nil {
		test.Fatal("catalog fixture")
	}
	test.Cleanup(catalog.Destroy)
	images, err := NewImages(config, auth, catalog)
	if err != nil {
		test.Fatal("images fixture")
	}
	test.Cleanup(images.Destroy)
	facts, err := NewAuthFacts(File, KeyringNotObserved, NoCredentialLogin)
	if err != nil {
		test.Fatal("auth facts fixture")
	}
	test.Cleanup(facts.Destroy)
	owners, err := NewOwnership(Absent, owner, nil)
	if err != nil {
		test.Fatal("owner fixture")
	}
	test.Cleanup(owners.Destroy)
	resolution, err := NewResolution(identifier, CompleteEffective, owners, facts, [32]byte{1})
	if err != nil {
		test.Fatal("resolution fixture")
	}
	test.Cleanup(resolution.Destroy)
	return images, resolution
}

func effective0166Declaration(model string) agentconfig.CodexProviderDeclarationV1 {
	return agentconfig.CodexProviderDeclarationV1{APIVersion: 1, Target: agentconfig.CodexConfig, ProviderID: agentconfig.CodexProviderID, WireAPI: agentconfig.CodexWireAPI, Endpoint: "https://127.0.0.1:18443/v1", Model: model, CredentialReference: agentconfig.CodexProviderKeySecret, RequiredSlots: []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, LocalAuthPolicy: agentconfig.CodexPreserveLocalLoginV1}
}

func TestEffective0166CompletePinnedProjection(test *testing.T) {
	var reference struct {
		Models []struct {
			Slug    string  `json:"slug"`
			Lite    bool    `json:"use_responses_lite"`
			Default *string `json:"default_reasoning_level"`
			Levels  []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if json.Unmarshal([]byte(native0166EffectiveModels), &reference) != nil || len(reference.Models) != 12 {
		test.Fatal("full pinned metadata unavailable")
	}
	for _, row := range reference.Models {
		for _, selected := range []string{row.Slug, row.Slug + "-dated", "custom/" + row.Slug, "custom/" + row.Slug + "-dated", row.Slug + "/one/two"} {
			facts, err := resolveEffective0166(test.Context(), native0166EffectiveProfile(), selected, false, nil)
			if err != nil || facts.source != effectiveBundled || facts.matchedSlug != row.Slug || facts.lite != row.Lite || len(facts.efforts) != len(row.Levels) || (facts.defaultEffort == nil) != (row.Default == nil) {
				test.Fatal("pinned row or native prefix/namespace resolution changed")
			}
			for index, level := range row.Levels {
				if facts.efforts[index] != level.Effort {
					test.Fatal("native-only level or order lost")
				}
			}
			if row.Default != nil && *facts.defaultEffort != *row.Default {
				test.Fatal("model default lost")
			}
		}
	}
	for _, selected := range []string{"unknown-model", "bad.ns/gpt-5.5", "/gpt-5.5", "custom/extra/gpt-5.5", "汉/gpt-5.5", "custom/unknown-model"} {
		facts, err := resolveEffective0166(test.Context(), native0166EffectiveProfile(), selected, false, nil)
		if err != nil || facts.source != effectiveFallback || facts.matchedSlug != selected || facts.lite || facts.defaultEffort != nil || len(facts.efforts) != 0 {
			test.Fatal("unknown source did not retain native fallback metadata")
		}
	}
	models := []selectedModelFacts{{matchedSlug: "a", lite: true}, {matchedSlug: "ab"}, {matchedSlug: "ab", lite: true}}
	matched, found := find0166Bundled("ab-dated", models)
	if !found || matched.matchedSlug != "ab" || matched.lite {
		test.Fatal("longest match/first equal-length source order changed")
	}
}

func TestEffective0166ExplicitExactAndFallback(test *testing.T) {
	const catalog = `{"future":{"large":9007199254740993},"models":[{"slug":"gpt-6-sol","use_responses_lite":false,"supported_reasoning_levels":[{"effort":"ultra","description":"future"}],"default_reasoning_level":"ultra","base_instructions":"","unknown":"keep"}]}`
	for _, selected := range []string{"gpt-6-sol", "gpt-6-sol-dated", "custom/gpt-6-sol", "gpt-5.5"} {
		facts, err := resolveEffective0166(test.Context(), native0166EffectiveProfile(), selected, true, []byte(catalog))
		if err != nil || facts.lite {
			test.Fatal("explicit catalog resolution failed")
		}
		if selected == "gpt-6-sol" {
			if facts.source != effectiveCatalog || facts.defaultEffort == nil || *facts.defaultEffort != "ultra" || !slices.Equal(facts.efforts, []string{"ultra"}) {
				test.Fatal("explicit exact source/default not retained")
			}
		} else if facts.source != effectiveFallback || facts.defaultEffort != nil || len(facts.efforts) != 0 {
			test.Fatal("explicit miss incorrectly used bundled/prefix metadata")
		}
	}
}

func TestEffective0166MetadataBoundedAndFailClosed(test *testing.T) {
	original := [][]byte{[]byte(native0166EffectiveSource), []byte(native0166EffectiveModels), []byte(native0166EffectivePrompt), []byte(native0166EffectiveLicense)}
	if validate0166Metadata(test.Context(), original[0], original[1], original[2], original[3]) != nil {
		test.Fatal("original metadata refused")
	}
	for index := range original {
		mutated := slices.Clone(original)
		mutated[index] = append(bytes.Clone(original[index]), ' ')
		if err := validate0166Metadata(test.Context(), mutated[0], mutated[1], mutated[2], mutated[3]); !errors.Is(err, ErrUnsupportedNativeProfile) {
			test.Fatal("modified metadata/prompt/license binding trusted")
		}
	}
	if err := validate0166Metadata(test.Context(), make([]byte, (1<<20)+1), nil, nil, nil); !errors.Is(err, ErrUnsupportedNativeProfile) {
		test.Fatal("static metadata bound increased")
	}
	ctx, cancel := context.WithCancelCause(test.Context())
	cancel(errors.New("PRIVATE_CANCEL_CAUSE"))
	err := validate0166Metadata(ctx, original[0], original[1], original[2], original[3])
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "PRIVATE_CANCEL_CAUSE") {
		test.Fatal("cancel lost or private cause leaked")
	}
	for _, profile := range []nativeProfile{func() nativeProfile {
		value := native0166EffectiveProfile()
		value.modelSemantics = [32]byte{}
		return value
	}(), func() nativeProfile {
		value := native0166EffectiveProfile()
		value.id = Native0166ProfileID
		return value
	}()} {
		if !errors.Is(validateProfile(test.Context(), profile), ErrUnsupportedNativeProfile) {
			test.Fatal("mixed legacy/effective identity accepted")
		}
	}
}

func TestEffective0166SevenEffortsAndOwnedSnapshots(test *testing.T) {
	const retained = "{\r\n \"future\":9007199254740993,\"models\":[{\"slug\":\"retained-model\",\"supported_reasoning_levels\":[{\"effort\":\"low\",\"description\":\"low\"}],\"default_reasoning_level\":\"low\",\"base_instructions\":\"\",\"foreign\":{\"text\":\"\\u4e2d\"}}]}\r\n"
	for _, kind := range []string{"full", "fallback", "retained"} {
		for _, effort := range []agentconfig.CodexReasoningEffort{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
			test.Run(kind+"/"+string(effort), func(test *testing.T) {
				config := "# KEEP_PREFIX\r\ncli_auth_credentials_store='file'\r\n# KEEP_SUFFIX\n"
				model := "gpt-5.5"
				var raw []byte
				if kind == "retained" {
					model = "retained-model"
					raw = []byte(retained)
					config += "model_catalog_json='cognate-model-catalog.json'\r\n"
				} else if kind == "fallback" {
					model = "unknown-model"
				}
				images, resolution := effective0166Fixture(test, Native0166EffectiveProfileID, config, raw)
				declaration := effective0166Declaration(model)
				declaration.ReasoningEffort = new(effort)
				plan, err := CheckProvider(test.Context(), images, declaration, resolution)
				if err != nil {
					test.Fatal("valid root effort refused by advertised-membership fence")
				}
				defer plan.Destroy()
				images.Destroy()
				resolution.Destroy()
				secret, err := NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("SYNTHETIC_SELECTED_KEY"))
				if err != nil {
					test.Fatal("selected fixture failed")
				}
				defer secret.Destroy()
				candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
				if err != nil {
					test.Fatal("owned plan did not survive caller snapshot destruction")
				}
				defer candidate.Destroy()
				output := candidate.CopyImage(ConfigRole)
				defer output.Destroy()
				body := output.CopyBytes()
				defer clear(body)
				document, err := indexTOML(test.Context(), body)
				if err != nil {
					test.Fatal("render invalid TOML")
				}
				actual, exists := document.lookup("model_reasoning_effort")
				if !exists || actual != string(effort) || !bytes.Contains(body, []byte("# KEEP_PREFIX\r\n")) || !bytes.Contains(body, []byte("# KEEP_SUFFIX\n")) || candidate.Action(CatalogRole) != Keep || candidate.Action(AuthRole) != Keep {
					test.Fatal("root value/presence or foreign bytes/actions changed")
				}
				catalog := candidate.CopyImage(CatalogRole)
				defer catalog.Destroy()
				catalogBody := catalog.CopyBytes()
				defer clear(catalogBody)
				if !bytes.Equal(catalogBody, raw) {
					test.Fatal("retained unknown JSON rewritten")
				}
			})
		}
	}
}

func TestEffective0166InheritedRootAndV1Isolation(test *testing.T) {
	const config = "cli_auth_credentials_store='file'\nmodel_reasoning_effort='low'\n"
	for _, identifier := range []string{Native0166ProfileID, Native0166EffectiveProfileID} {
		images, resolution := effective0166Fixture(test, identifier, config, nil)
		plan, err := CheckProvider(test.Context(), images, effective0166Declaration("gpt-5.5"), resolution)
		defer plan.Destroy()
		if identifier == Native0166ProfileID {
			if !errors.Is(err, ErrOwnershipConflict) {
				test.Fatal("legacy root-preservation admission semantics changed")
			}
		} else if err != nil || plan.get[planData]() == nil {
			test.Fatal("v2 valid retained root effort refused")
		}
	}
	images, resolution := effective0166Fixture(test, Native0166ProfileID, "cli_auth_credentials_store='file'\n", nil)
	plan, err := CheckProvider(test.Context(), images, effective0166Declaration("gpt-6-sol"), resolution)
	defer plan.Destroy()
	if err != nil || plan.get[planData]() == nil {
		test.Fatal("legacy v1 behavior silently changed under old identity")
	}
	for _, bad := range []string{"ultra", "Low", ""} {
		declaration := effective0166Declaration("gpt-5.5")
		declaration.ReasoningEffort = new(agentconfig.CodexReasoningEffort(bad))
		if _, err := CheckProvider(test.Context(), images, declaration, resolution); !errors.Is(err, ErrInvalidDeclaration) {
			test.Fatal("unknown asset root enum accepted")
		}
	}
}

func TestEffective0166PointerAndLocalConflict(test *testing.T) {
	const raw = `{"future":{"unknown":true},"models":[{"slug":"gpt-6-sol","supported_reasoning_levels":[],"base_instructions":""}]}`
	for _, explicit := range []bool{false, true} {
		config := "cli_auth_credentials_store='file'\n"
		if explicit {
			config += "model_catalog_json='cognate-model-catalog.json'\n"
		}
		images, resolution := effective0166Fixture(test, Native0166EffectiveProfileID, config, []byte(raw))
		plan, err := CheckProvider(test.Context(), images, effective0166Declaration("gpt-6-sol"), resolution)
		defer plan.Destroy()
		if explicit {
			if err != nil || plan.get[planData]() == nil {
				test.Fatal("explicit native-default Full metadata refused")
			}
		} else if !errors.Is(err, ErrUnsupportedNativeProfile) || plan.get[planData]() != nil {
			test.Fatal("unreferenced local artifact replaced actual bundled source")
		}
	}
	for _, raw := range []string{
		`{"models":[{"slug":"gpt-5.5","use_responses_lite":true}]}`,
		`{"models":[{"slug":"gpt-5.5","supports_parallel_tool_calls":false}]}`,
		`{"models":[{"slug":"gpt-5.5","use_responses_lite":"false"}]}`,
	} {
		images, resolution := effective0166Fixture(test, Native0166EffectiveProfileID, "cli_auth_credentials_store='file'\nmodel_catalog_json='cognate-model-catalog.json'\n", []byte(raw))
		plan, err := CheckProvider(test.Context(), images, effective0166Declaration("gpt-5.5"), resolution)
		defer plan.Destroy()
		if !errors.Is(err, ErrOwnershipConflict) || plan.get[planData]() != nil {
			test.Fatal("retained explicit ownership/parallel conflict weakened")
		}
	}
}

func TestEffective0166AbsentRootAndDestroyedPlan(test *testing.T) {
	images, resolution := effective0166Fixture(test, Native0166EffectiveProfileID, "cli_auth_credentials_store='file'\n", nil)
	plan, err := CheckProvider(test.Context(), images, effective0166Declaration("gpt-5.5"), resolution)
	if err != nil {
		test.Fatal("default model source refused")
	}
	defer plan.Destroy()
	secret, err := NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("SYNTHETIC_SELECTED_KEY"))
	if err != nil {
		test.Fatal("selected fixture")
	}
	defer secret.Destroy()
	candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
	if err != nil {
		test.Fatal("default model source render")
	}
	defer candidate.Destroy()
	image := candidate.CopyImage(ConfigRole)
	defer image.Destroy()
	body := image.CopyBytes()
	defer clear(body)
	document, err := indexTOML(test.Context(), body)
	if err != nil {
		test.Fatal("rendered TOML invalid")
	}
	if _, exists := document.lookup("model_reasoning_effort"); exists {
		test.Fatal("model default was materialized as a new root value")
	}
	if _, exists := document.lookup("model_catalog_json"); exists || candidate.Action(CatalogRole) != Keep {
		test.Fatal("absent catalog was normalized or filled")
	}
	plan.Destroy()
	result, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
	defer result.Destroy()
	if err == nil || result.get[candidateData]() != nil {
		test.Fatal("destroyed plan revived")
	}
}

func TestEffective0166FreshCancelNoPlanOrMaterial(test *testing.T) {
	for range 3 {
		images, resolution := effective0166Fixture(test, Native0166EffectiveProfileID, "cli_auth_credentials_store='file'\n", nil)
		ctx, cancel := context.WithCancelCause(test.Context())
		cancel(errors.New("PRIVATE_FRESH_CAUSE"))
		plan, err := CheckProvider(ctx, images, effective0166Declaration("gpt-5.5"), resolution)
		defer plan.Destroy()
		if !errors.Is(err, context.Canceled) || plan.get[planData]() != nil || strings.Contains(err.Error(), "PRIVATE_FRESH_CAUSE") {
			test.Fatal("fresh canceled invocation produced plan or leaked Cause")
		}
	}
}

func TestEffective0166LitePreplan(test *testing.T) {
	for _, login := range []string{"missing", "api", "oauth"} {
		for _, explicit := range []bool{false, true} {
			name := login + "/default"
			if explicit {
				name = login + "/low"
			}
			test.Run(name, func(test *testing.T) {
				config, err := NewImage(Present, []byte("cli_auth_credentials_store='file'\n# KEEP\n"))
				if err != nil {
					test.Fatal("config fixture unavailable")
				}
				defer config.Destroy()
				presence := Missing
				var authBytes []byte
				if login != "missing" {
					presence = Present
					authBytes = []byte(`{"OPENAI_API_KEY":"SYNTHETIC_OLD_API"}`)
					if login == "oauth" {
						token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":4102444800,"https://api.openai.com/auth":{"chatgpt_account_id":"fixture","chatgpt_user_id":"fixture-user","chatgpt_plan_type":"plus"}}`)) + ".synthetic"
						authBytes, err = json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]string{"id_token": token, "access_token": "SYNTHETIC_OLD_ACCESS", "refresh_token": "SYNTHETIC_OLD_REFRESH", "account_id": "fixture"}, "last_refresh": time.Now().UTC().Format(time.RFC3339Nano)})
						if err != nil {
							test.Fatal("auth fixture encoding failed")
						}
					}
				}
				defer clear(authBytes)
				auth, err := NewImage(presence, authBytes)
				if err != nil {
					test.Fatal("auth image unavailable")
				}
				defer auth.Destroy()
				catalog, err := NewImage(Missing, nil)
				if err != nil {
					test.Fatal("catalog image unavailable")
				}
				defer catalog.Destroy()
				images, err := NewImages(config, auth, catalog)
				if err != nil {
					test.Fatal("images unavailable")
				}
				defer images.Destroy()
				facts, _, err := InspectNative0166(test.Context(), config, auth, time.Now())
				if err != nil {
					test.Fatal("recognized auth fixture failed")
				}
				defer facts.Destroy()
				owners, err := NewOwnership(Absent, Absent, nil)
				if err != nil {
					test.Fatal("ownership unavailable")
				}
				defer owners.Destroy()
				resolution, err := NewResolution(Native0166EffectiveProfileID, CompleteEffective, owners, facts, [32]byte{1})
				if err != nil {
					test.Fatal("resolution unavailable")
				}
				defer resolution.Destroy()
				declaration := agentconfig.CodexProviderDeclarationV1{APIVersion: 1, Target: agentconfig.CodexConfig, ProviderID: agentconfig.CodexProviderID, WireAPI: agentconfig.CodexWireAPI, Endpoint: "https://127.0.0.1:18443/v1", Model: "gpt-6-sol", CredentialReference: agentconfig.CodexProviderKeySecret, RequiredSlots: []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, LocalAuthPolicy: agentconfig.CodexPreserveLocalLoginV1}
				if explicit {
					declaration.ReasoningEffort = new(agentconfig.CodexReasoningEffort("low"))
				}
				plan, err := CheckProvider(test.Context(), images, declaration, resolution)
				defer plan.Destroy()
				if !errors.Is(err, ErrUnsupportedNativeProfile) || plan.get[planData]() != nil {
					test.Fatal("effective Lite admitted before selected material; expected exact preplan refusal")
				}
			})
		}
	}
}
