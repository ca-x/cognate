package codexconfig

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json/v2"
	"strings"
)

//go:embed testdata/native-0.166.0/source.json
var native0166EffectiveSource string

//go:embed testdata/native-0.166.0/models.json
var native0166EffectiveModels string

//go:embed testdata/native-0.166.0/prompt.md
var native0166EffectivePrompt string

//go:embed testdata/native-0.166.0/LICENSE
var native0166EffectiveLicense string

const native0166MetadataSHA256 = "3ec7b2918972c29caff70df24af52aaba05282902b3e8040dadd1c7dabfee905"

type effectiveModelSource uint8

const (
	effectiveFallback effectiveModelSource = iota + 1
	effectiveBundled
	effectiveCatalog
)

type selectedModelFacts struct {
	source        effectiveModelSource
	matchedSlug   string
	lite          bool
	efforts       []string
	defaultEffort *string
}

func validate0166Metadata(ctx context.Context, source, models, prompt, license []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(source) > 1<<20 || len(models) > 1<<20 || len(prompt) > 1<<20 || len(license) > 1<<20 {
		return ErrUnsupportedNativeProfile
	}
	binding := sha256.Sum256(source)
	if hex.EncodeToString(binding[:]) != native0166MetadataSHA256 {
		return ErrUnsupportedNativeProfile
	}
	var manifest struct {
		ModelsSHA256  string
		PromptSHA256  string
		LicenseSHA256 string
	}
	if json.Unmarshal(source, &manifest) != nil {
		return ErrUnsupportedNativeProfile
	}
	for _, entry := range []struct {
		body   []byte
		wanted string
	}{{models, manifest.ModelsSHA256}, {prompt, manifest.PromptSHA256}, {license, manifest.LicenseSHA256}} {
		if err := ctx.Err(); err != nil {
			return err
		}
		digest := sha256.Sum256(entry.body)
		if hex.EncodeToString(digest[:]) != entry.wanted {
			return ErrUnsupportedNativeProfile
		}
	}
	return ctx.Err()
}

func validateEffective0166Profile(ctx context.Context, profile nativeProfile) error {
	if profile.id != Native0166EffectiveProfileID || profile.modelSemantics != sha256.Sum256([]byte(native0166EffectiveSource)) {
		return ErrUnsupportedNativeProfile
	}
	return validate0166Metadata(ctx, []byte(native0166EffectiveSource), []byte(native0166EffectiveModels), []byte(native0166EffectivePrompt), []byte(native0166EffectiveLicense))
}

func bundled0166Models(ctx context.Context) ([]selectedModelFacts, error) {
	var catalog struct {
		Models []struct {
			Slug                     string  `json:"slug"`
			Lite                     bool    `json:"use_responses_lite"`
			Default                  *string `json:"default_reasoning_level"`
			SupportedReasoningLevels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if json.Unmarshal([]byte(native0166EffectiveModels), &catalog) != nil || len(catalog.Models) != 12 {
		return nil, ErrUnsupportedNativeProfile
	}
	models := make([]selectedModelFacts, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		facts := selectedModelFacts{source: effectiveBundled, matchedSlug: model.Slug, lite: model.Lite, defaultEffort: model.Default}
		for _, level := range model.SupportedReasoningLevels {
			facts.efforts = append(facts.efforts, level.Effort)
		}
		models = append(models, facts)
	}
	return models, ctx.Err()
}

func longest0166Prefix(selected string, models []selectedModelFacts) (selectedModelFacts, bool) {
	var best selectedModelFacts
	found := false
	for _, model := range models {
		if strings.HasPrefix(selected, model.matchedSlug) && (!found || len(model.matchedSlug) > len(best.matchedSlug)) {
			best, found = model, true
		}
	}
	return best, found
}

func find0166Bundled(selected string, models []selectedModelFacts) (selectedModelFacts, bool) {
	if model, found := longest0166Prefix(selected, models); found {
		return model, true
	}
	namespace, suffix, exists := strings.Cut(selected, "/")
	if !exists || namespace == "" || strings.Contains(suffix, "/") {
		return selectedModelFacts{}, false
	}
	for _, character := range []byte(namespace) {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return selectedModelFacts{}, false
		}
	}
	return longest0166Prefix(suffix, models)
}

func effective0166InstructionsPresent(model *jsonNode) bool {
	legacy := model.member("base_instructions")
	if legacy != nil && legacy.kind != 'n' && legacy.kind != '"' {
		return false
	}
	if messages := model.member("model_messages"); messages != nil && messages.kind != 'n' {
		if messages.kind != '{' {
			return false
		}
		if canonical := messages.member("instructions_template"); canonical != nil && canonical.kind != 'n' {
			return canonical.kind == '"'
		}
	}
	return legacy != nil && legacy.kind == '"'
}

func resolveEffective0166(ctx context.Context, profile nativeProfile, selected string, explicit bool, catalog []byte) (selectedModelFacts, error) {
	if err := ctx.Err(); err != nil {
		return selectedModelFacts{}, err
	}
	if err := validateEffective0166Profile(ctx, profile); err != nil {
		return selectedModelFacts{}, err
	}
	fallback := selectedModelFacts{source: effectiveFallback, matchedSlug: selected}
	if !explicit {
		models, err := bundled0166Models(ctx)
		if err != nil {
			return selectedModelFacts{}, err
		}
		if found, exists := find0166Bundled(selected, models); exists {
			return found, ctx.Err()
		}
		return fallback, ctx.Err()
	}
	document, err := readJSON(ctx, catalog)
	if err != nil {
		return selectedModelFacts{}, err
	}
	models := document.root.member("models")
	if models == nil || models.kind != '[' || len(models.elements) == 0 {
		return selectedModelFacts{}, ErrOwnershipConflict
	}
	for _, model := range models.elements {
		if err := ctx.Err(); err != nil {
			return selectedModelFacts{}, err
		}
		slug := model.member("slug")
		if model.kind != '{' || slug == nil || slug.kind != '"' {
			return selectedModelFacts{}, ErrOwnershipConflict
		}
		if slug.text != selected {
			continue
		}
		if !effective0166InstructionsPresent(model) {
			return selectedModelFacts{}, ErrUnsupportedNativeProfile
		}
		facts := selectedModelFacts{source: effectiveCatalog, matchedSlug: slug.text}
		if lite := model.member("use_responses_lite"); lite != nil {
			if lite.kind != 't' && lite.kind != 'f' {
				return selectedModelFacts{}, ErrOwnershipConflict
			}
			facts.lite = lite.kind == 't'
		}
		if levels := model.member("supported_reasoning_levels"); levels != nil {
			if levels.kind != '[' {
				return selectedModelFacts{}, ErrOwnershipConflict
			}
			for _, level := range levels.elements {
				effort := level.member("effort")
				if level.kind != '{' || effort == nil || effort.kind != '"' {
					return selectedModelFacts{}, ErrOwnershipConflict
				}
				facts.efforts = append(facts.efforts, effort.text)
			}
		}
		if value := model.member("default_reasoning_level"); value != nil && value.kind != 'n' {
			if value.kind != '"' {
				return selectedModelFacts{}, ErrOwnershipConflict
			}
			facts.defaultEffort = new(value.text)
		}
		return facts, ctx.Err()
	}
	return fallback, ctx.Err()
}
