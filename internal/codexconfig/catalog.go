package codexconfig

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"slices"

	"github.com/ca-x/cognate/internal/agentconfig"
)

var nativeModelFields = [...]string{"slug", "description", "display_name", "context_window", "max_context_window", "supports_parallel_tool_calls", "input_modalities", "base_instructions", "supported_reasoning_levels", "default_reasoning_level"}

func requiredJSON(object *jsonNode, names []string) error {
	for _, name := range names {
		if object.member(name) == nil {
			return ErrUnsupportedNativeProfile
		}
	}
	return nil
}

func validateNativeModel(raw []byte, model *jsonNode, profile nativeProfile) error {
	if model.kind != '{' {
		return ErrInvalidLocalImage
	}
	if err := requiredJSON(model, profile.requiredModel); err != nil {
		return err
	}
	for _, member := range model.members {
		if !slices.Contains(nativeModelFields[:], member.name) && !slices.Contains(profile.ignoredModel, member.name) {
			return ErrOwnershipConflict
		}
		if slices.Contains([]string{"model_messages", "auth", "env_key", "headers", "command"}, member.name) {
			return ErrOwnershipConflict
		}
		switch member.name {
		case "slug", "description", "display_name", "base_instructions", "default_reasoning_level":
			if member.value.kind != '"' {
				return ErrInvalidLocalImage
			}
		case "context_window", "max_context_window":
			var number uint64
			if member.value.kind != '0' || json.Unmarshal(raw[member.value.start:member.value.end], &number) != nil || number < 1 || number > 2147483647 {
				return ErrInvalidLocalImage
			}
		case "supports_parallel_tool_calls":
			if member.value.kind != 't' && member.value.kind != 'f' {
				return ErrInvalidLocalImage
			}
		case "input_modalities":
			if member.value.kind != '[' || len(member.value.elements) < 1 || len(member.value.elements) > 2 {
				return ErrInvalidLocalImage
			}
			var seen []string
			for _, item := range member.value.elements {
				if item.kind != '"' || item.text != "text" && item.text != "image" || slices.Contains(seen, item.text) {
					return ErrInvalidLocalImage
				}
				seen = append(seen, item.text)
			}
		case "supported_reasoning_levels":
			if member.value.kind != '[' || len(member.value.elements) < 1 || len(member.value.elements) > 7 {
				return ErrInvalidLocalImage
			}
			var seen []agentconfig.CodexReasoningEffort
			for _, item := range member.value.elements {
				if item.kind != '{' {
					return ErrInvalidLocalImage
				}
				effort, description := item.member("effort"), item.member("description")
				if effort == nil || effort.kind != '"' || description == nil || description.kind != '"' || len(item.members) != 2 {
					return ErrInvalidLocalImage
				}
				value := agentconfig.CodexReasoningEffort(effort.text)
				if !slices.Contains(profile.efforts, value) || slices.Contains(seen, value) {
					return ErrUnsupportedNativeProfile
				}
				seen = append(seen, value)
			}
		}
	}
	slug := model.member("slug")
	if slug == nil || !validModelID(slug.text) {
		return ErrInvalidLocalImage
	}
	if minimum, maximum := model.member("context_window"), model.member("max_context_window"); minimum != nil && maximum != nil && !bytes.Equal(raw[minimum.start:minimum.end], raw[maximum.start:maximum.end]) {
		return ErrOwnershipConflict
	}
	if value := model.member("default_reasoning_level"); value != nil {
		levels := model.member("supported_reasoning_levels")
		found := false
		if levels != nil {
			for _, item := range levels.elements {
				if effort := item.member("effort"); effort != nil && effort.text == value.text {
					found = true
				}
			}
		}
		if !found {
			return ErrOwnershipConflict
		}
	}
	return nil
}

func catalogUpdates(model agentconfig.CodexCatalogModelDesired, profile nativeProfile) ([]jsonUpdate, error) {
	updates := []jsonUpdate{}
	add := func(name string, value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return ErrInvalidDeclaration
		}
		updates = append(updates, jsonUpdate{name: name, value: raw})
		return nil
	}
	if err := add("slug", model.Model); err != nil {
		return nil, err
	}
	if model.DisplayName != nil {
		if err := add("display_name", *model.DisplayName); err != nil {
			return nil, err
		}
	}
	if model.ContextWindow != nil {
		if err := add("context_window", *model.ContextWindow); err != nil {
			return nil, err
		}
		if err := add("max_context_window", *model.ContextWindow); err != nil {
			return nil, err
		}
	}
	if model.SupportsParallelToolCalls != nil {
		if err := add("supports_parallel_tool_calls", *model.SupportsParallelToolCalls); err != nil {
			return nil, err
		}
	}
	if model.InputModalities != nil {
		if err := add("input_modalities", *model.InputModalities); err != nil {
			return nil, err
		}
	}
	if model.BaseInstructions != nil {
		if err := add("base_instructions", *model.BaseInstructions); err != nil {
			return nil, err
		}
	}
	if model.ReasoningLevels != nil {
		levels := []struct {
			Effort      agentconfig.CodexReasoningEffort `json:"effort"`
			Description string                           `json:"description"`
		}{}
		for _, effort := range *model.ReasoningLevels {
			description, exists := profile.descriptions[effort]
			if !exists || !slices.Contains(profile.efforts, effort) {
				return nil, ErrUnsupportedNativeProfile
			}
			levels = append(levels, struct {
				Effort      agentconfig.CodexReasoningEffort `json:"effort"`
				Description string                           `json:"description"`
			}{effort, description})
		}
		if err := add("supported_reasoning_levels", levels); err != nil {
			return nil, err
		}
	}
	if model.DefaultReasoningLevel != nil {
		if err := add("default_reasoning_level", *model.DefaultReasoningLevel); err != nil {
			return nil, err
		}
	}
	return updates, nil
}

func rewriteCatalog(ctx context.Context, image LocalImage, declaration agentconfig.CodexProviderDeclarationV1, owners ownershipData, profile nativeProfile) ([]byte, int, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, 0, err
	}
	if err := validateDeclaration(declaration); err != nil {
		return nil, 0, 0, err
	}
	if declaration.Catalog == nil {
		return nil, 0, 0, ErrInvalidDeclaration
	}
	if err := validateProfile(ctx, profile); err != nil {
		return nil, 0, 0, err
	}
	var raw []byte
	switch image.Presence() {
	case Missing:
		if owners.catalog != Absent || len(owners.models) != 0 {
			return nil, 0, 0, ErrOwnershipConflict
		}
		raw = profile.rootDefaults
	case Present:
		if owners.catalog != Managed {
			return nil, 0, 0, ErrOwnershipConflict
		}
		raw = image.get[imageData]().body
	default:
		return nil, 0, 0, ErrOwnershipConflict
	}
	document, err := readJSON(ctx, raw)
	if err != nil {
		return nil, 0, 0, err
	}
	if err := requiredJSON(document.root, profile.requiredRoot); err != nil {
		return nil, 0, 0, err
	}
	for _, member := range document.root.members {
		if member.name != "models" && !slices.Contains(profile.ignoredCatalogRoot, member.name) {
			return nil, 0, 0, ErrOwnershipConflict
		}
	}
	models := document.root.member("models")
	if models == nil || models.kind != '[' {
		return nil, 0, 0, ErrInvalidLocalImage
	}
	existing := make(map[string]*jsonNode, len(models.elements))
	for _, model := range models.elements {
		if err := validateNativeModel(raw, model, profile); err != nil {
			return nil, 0, 0, err
		}
		name := model.member("slug").text
		if existing[name] != nil || !slices.Contains(owners.models, name) {
			return nil, 0, 0, ErrOwnershipConflict
		}
		existing[name] = model
	}
	if len(existing) != len(owners.models) {
		return nil, 0, 0, ErrOwnershipConflict
	}
	array := []byte{'['}
	defer func() { clear(array) }()
	edits, retained := 0, 0
	for index, desired := range declaration.Catalog.Models {
		if err := ctx.Err(); err != nil {
			return nil, 0, 0, err
		}
		entry := profile.modelDefaults
		if old := existing[desired.Model]; old != nil {
			entry = raw[old.start:old.end]
			retained++
		}
		old, err := readJSON(ctx, entry)
		if err != nil {
			return nil, 0, 0, err
		}
		updates, err := catalogUpdates(desired, profile)
		if err != nil {
			return nil, 0, 0, err
		}
		rewritten, count, err := rewriteJSONMembers(ctx, entry, old.root, updates)
		if err != nil {
			return nil, 0, 0, err
		}
		checked, err := readJSON(ctx, rewritten)
		if err == nil {
			err = validateNativeModel(rewritten, checked.root, profile)
		}
		if err == nil && desired.Model == declaration.Model && declaration.ReasoningEffort != nil {
			if levels := checked.root.member("supported_reasoning_levels"); levels != nil {
				compatible := false
				for _, item := range levels.elements {
					if item.member("effort").text == string(*declaration.ReasoningEffort) {
						compatible = true
					}
				}
				if !compatible {
					err = ErrOwnershipConflict
				}
			}
		}
		if err != nil {
			clear(rewritten)
			return nil, 0, 0, err
		}
		edits += count + 1
		if edits > maxEdits {
			clear(rewritten)
			return nil, 0, 0, ErrCodecLimit
		}
		if index > 0 {
			array = append(array, ',')
		}
		if len(array)+len(rewritten)+1 > MaxImageBytes {
			clear(rewritten)
			return nil, 0, 0, ErrCodecLimit
		}
		array = append(array, rewritten...)
		clear(rewritten)
	}
	array = append(array, ']')
	output, count, err := rewriteJSONMembers(ctx, raw, document.root, []jsonUpdate{{name: "models", value: array}})
	if err != nil {
		return nil, 0, 0, err
	}
	if edits+count > maxEdits {
		clear(output)
		return nil, 0, 0, ErrCodecLimit
	}
	if err := ctx.Err(); err != nil {
		clear(output)
		return nil, 0, 0, err
	}
	return output, edits + count, len(existing) - retained, nil
}
