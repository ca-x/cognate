package importer

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
)

func parseOpenCodeSelection(ctx context.Context, id, name string, raw []byte) (configuration.Draft, []SelectionSlot, error) {
	if err := ctx.Err(); err != nil {
		return configuration.Draft{}, nil, err
	}
	root, err := sourceObject(raw)
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	defer clearSourceObject(root)
	if !sourceFields(root, "npm", "name", "options", "models") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	provider := agentconfig.OpenCodeV1{}
	if openCodeSourceStrings(root, map[string]**string{"npm": &provider.NPM, "name": &provider.Name}) != nil {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	slots := []SelectionSlot{}
	if raw, present := root["options"]; present {
		options, err := sourceObject(raw)
		if err != nil {
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
		defer clearSourceObject(options)
		if !sourceFields(options, "baseURL", "apiKey", "headers") {
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
		provider.Options = &agentconfig.OpenCodeV1Options{}
		if openCodeSourceStrings(options, map[string]**string{"baseURL": &provider.Options.BaseURL}) != nil {
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
		if value, present := options["apiKey"]; present {
			key, err := sourceString(value)
			if err != nil {
				return configuration.Draft{}, nil, ErrSelectionUnsupported
			}
			provider.Options.APIKey = new("opencode.api-key")
			slots = append(slots, SelectionSlot{Slot: "opencode.api-key", Present: key != ""})
		}
		if raw, present := options["headers"]; present {
			headers, err := sourceObject(raw)
			if err != nil || len(headers) > 63 {
				clearSourceObject(headers)
				return configuration.Draft{}, nil, ErrSelectionUnsupported
			}
			defer clearSourceObject(headers)
			provider.Options.Headers = new(make(map[string]string, len(headers)))
			for index, header := range slices.Sorted(maps.Keys(headers)) {
				value, err := sourceString(headers[header])
				if err != nil {
					return configuration.Draft{}, nil, ErrSelectionUnsupported
				}
				slot := fmt.Sprintf("opencode.value.%02d", index+1)
				(*provider.Options.Headers)[header] = slot
				slots = append(slots, SelectionSlot{Slot: slot, Present: value != ""})
			}
		}
	}
	if raw, present := root["models"]; present {
		models, err := sourceObject(raw)
		if err != nil || len(models) > 64 {
			clearSourceObject(models)
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
		defer clearSourceObject(models)
		provider.Models = new(make(map[string]agentconfig.OpenCodeV1Model, len(models)))
		for id, raw := range models {
			model, err := openCodeSourceModel(raw)
			if err != nil {
				return configuration.Draft{}, nil, err
			}
			(*provider.Models)[id] = model
		}
	}
	draft := configuration.Draft{Name: name, OpenCodeProvider: &agentconfig.OpenCodeProvider{ProviderID: id, Format: "v1", V1: &provider}}
	if _, err := configuration.PrepareImportedDraft(ctx, draft); err != nil {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	return draft, slots, ctx.Err()
}

func openCodeSourceStrings(object map[string]jsontext.Value, fields map[string]**string) error {
	for field, target := range fields {
		if raw, present := object[field]; present {
			value, err := sourceString(raw)
			if err != nil {
				return ErrSelectionUnsupported
			}
			*target = new(value)
		}
	}
	return nil
}

func openCodeSourceModel(raw []byte) (agentconfig.OpenCodeV1Model, error) {
	model := agentconfig.OpenCodeV1Model{}
	object, err := sourceObject(raw)
	if err != nil {
		return model, ErrSelectionUnsupported
	}
	defer clearSourceObject(object)
	if !sourceFields(object, "name", "limit", "modalities") || openCodeSourceStrings(object, map[string]**string{"name": &model.Name}) != nil {
		return model, ErrSelectionUnsupported
	}
	if raw, present := object["limit"]; present {
		limit, err := sourceObject(raw)
		if err != nil {
			return model, ErrSelectionUnsupported
		}
		defer clearSourceObject(limit)
		if !sourceFields(limit, "context", "input", "output") {
			return model, ErrSelectionUnsupported
		}
		model.Limit = &agentconfig.OpenCodeLimit{}
		for field, target := range map[string]**string{"context": &model.Limit.Context, "input": &model.Limit.Input, "output": &model.Limit.Output} {
			if raw, present := limit[field]; present {
				value := string(raw)
				if _, err := strconv.ParseUint(value, 10, 64); err != nil {
					return model, ErrSelectionUnsupported
				}
				*target = new(value)
			}
		}
	}
	if raw, present := object["modalities"]; present {
		modalities, err := sourceObject(raw)
		if err != nil {
			return model, ErrSelectionUnsupported
		}
		defer clearSourceObject(modalities)
		if !sourceFields(modalities, "input", "output") {
			return model, ErrSelectionUnsupported
		}
		model.Modalities = &agentconfig.OpenCodeModalities{}
		for field, target := range map[string]**[]string{"input": &model.Modalities.Input, "output": &model.Modalities.Output} {
			if raw, present := modalities[field]; present {
				var values []string
				if json.Unmarshal(raw, &values) != nil || values == nil {
					return model, ErrSelectionUnsupported
				}
				*target = new(values)
			}
		}
	}
	return model, nil
}
