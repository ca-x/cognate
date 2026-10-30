package configuration

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"strconv"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

type openCodeCredentials map[Slot]Secret

func (secrets openCodeCredentials) clear() {
	for slot, secret := range secrets {
		secret.Clear()
		delete(secrets, slot)
	}
}

type openCodeEnvelope struct {
	Version  *int                          `json:"version"`
	Variant  *string                       `json:"variant"`
	Provider *agentconfig.OpenCodeProvider `json:"provider"`
	Secrets  *map[string]jsontext.Value    `json:"secrets"`
}

func encodeOpenCodeEnvelope(provider agentconfig.OpenCodeProvider, secrets openCodeCredentials) (state.ConfigurationPayload, error) {
	required, err := agentconfig.OpenCodeRequiredSlots(provider)
	if err != nil {
		return state.ConfigurationPayload{}, ErrUnsupported
	}
	stored := make(map[string]string)
	allowed := make(map[Slot]bool, len(required))
	for _, slot := range required {
		allowed[Slot(slot)] = true
	}
	total := 0
	for slot, secret := range secrets {
		if !allowed[slot] || !validSecret(secret) {
			return state.ConfigurationPayload{}, ErrInvalid
		}
		total += len(secretBytes(secret))
		if total > 64<<10 {
			return state.ConfigurationPayload{}, ErrInvalid
		}
		stored[string(slot)] = string(secretBytes(secret))
	}
	encoded, err := json.Marshal(struct {
		Version  int                          `json:"version"`
		Variant  string                       `json:"variant"`
		Provider agentconfig.OpenCodeProvider `json:"provider"`
		Secrets  map[string]string            `json:"secrets"`
	}{1, "opencode-provider", provider, stored})
	defer clear(encoded)
	if err != nil || len(encoded) > state.MaxConfigurationPayloadBytes {
		return state.ConfigurationPayload{}, ErrInvalid
	}
	return state.NewConfigurationPayload(encoded), nil
}

func decodeOpenCodeEnvelope(payload state.ConfigurationPayload) (_ agentconfig.OpenCodeProvider, result openCodeCredentials, extras bool, finalErr error) {
	encoded := payload.Reveal()
	defer clear(encoded)
	if len(encoded) == 0 || len(encoded) > state.MaxConfigurationPayloadBytes {
		return agentconfig.OpenCodeProvider{}, nil, false, ErrInvalid
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(encoded))
	extras, err := scanOpenCodeStored(decoder, openCodeStoredShape(), true, 0)
	if err != nil {
		return agentconfig.OpenCodeProvider{}, nil, false, ErrInvalid
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return agentconfig.OpenCodeProvider{}, nil, false, ErrInvalid
	}
	var envelope openCodeEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil || envelope.Version == nil || envelope.Variant == nil || envelope.Provider == nil || envelope.Secrets == nil {
		return agentconfig.OpenCodeProvider{}, nil, false, ErrInvalid
	}
	if *envelope.Version != 1 || *envelope.Variant != "opencode-provider" {
		return agentconfig.OpenCodeProvider{}, nil, false, ErrUnsupported
	}
	required, err := agentconfig.OpenCodeRequiredSlots(*envelope.Provider)
	if err != nil {
		return agentconfig.OpenCodeProvider{}, nil, false, ErrUnsupported
	}
	allowed := make(map[string]bool, len(required))
	for _, slot := range required {
		allowed[slot] = true
	}
	result = make(openCodeCredentials)
	defer func() {
		if finalErr != nil {
			result.clear()
		}
	}()
	total := 0
	for slot, raw := range *envelope.Secrets {
		if !agentconfig.ValidOpenCodeSlot(slot) {
			extras = true
			continue
		}
		if !allowed[slot] {
			return agentconfig.OpenCodeProvider{}, result, false, ErrInvalid
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return agentconfig.OpenCodeProvider{}, result, false, ErrInvalid
		}
		temporary := []byte(value)
		secret := NewSecret(temporary)
		clear(temporary)
		result[Slot(slot)] = secret
		total += len(secretBytes(secret))
		if !validSecret(secret) || total > 64<<10 {
			return agentconfig.OpenCodeProvider{}, result, false, ErrInvalid
		}
	}
	provider, err := agentconfig.CloneOpenCodeProvider(*envelope.Provider)
	if err != nil {
		return agentconfig.OpenCodeProvider{}, result, false, ErrUnsupported
	}
	return provider, result, extras, nil
}

func scanOpenCodeStored(decoder *jsontext.Decoder, shape codexShape, known bool, depth int) (bool, error) {
	if depth > 64 {
		return false, ErrInvalid
	}
	token, err := decoder.ReadToken()
	if err != nil || known && token.Kind() == 'n' {
		return false, ErrInvalid
	}
	extras := false
	switch token.Kind() {
	case '{':
		for decoder.PeekKind() != '}' {
			name, err := decoder.ReadToken()
			if err != nil || name.Kind() != '"' {
				return false, ErrInvalid
			}
			child, exists := shape[name.String()]
			if !exists {
				child, exists = shape["*"]
			}
			nested, err := scanOpenCodeStored(decoder, child, known && exists, depth+1)
			if err != nil {
				return false, err
			}
			extras = extras || nested || known && !exists
		}
		if _, err := decoder.ReadToken(); err != nil {
			return false, ErrInvalid
		}
	case '[':
		for decoder.PeekKind() != ']' {
			nested, err := scanOpenCodeStored(decoder, shape["*"], known, depth+1)
			if err != nil {
				return false, err
			}
			extras = extras || nested
		}
		if _, err := decoder.ReadToken(); err != nil {
			return false, ErrInvalid
		}
	}
	return extras, nil
}

func openCodeStoredShape() codexShape {
	value := codexShape{"null": nil, "bool": nil, "number": nil, "literal": nil, "reference": nil}
	value["array"] = codexShape{"*": value}
	value["object"] = codexShape{"*": value}
	object := codexShape{"*": value}
	headers := codexShape{"*": nil}
	strings := codexShape{"*": nil}
	limit := codexShape{"context": nil, "input": nil, "output": nil, "extensions": object}
	compaction := codexShape{"type": nil, "extensions": object}
	modelSettings := codexShape{"compaction": compaction, "extensions": object}
	variant := codexShape{"id": nil, "settings": modelSettings, "headers": headers, "body": object, "extensions": object}
	cost := codexShape{"input": nil, "output": nil, "cache": {"read": nil, "write": nil, "extensions": object}, "tier": {"type": nil, "size": nil, "extensions": object}, "extensions": object}
	modelV2 := codexShape{
		"modelID": nil, "family": nil, "name": nil, "package": nil, "disabled": nil,
		"settings": modelSettings, "headers": headers, "body": object, "limit": limit,
		"compatibility": {"reasoningField": value, "requireReasoning": nil, "maxTokensField": nil, "requireFinishReason": nil, "requireAssistantAfterTool": nil, "supportsPromptCacheKey": nil, "extensions": object},
		"capabilities":  {"tools": nil, "input": strings, "output": strings, "extensions": object},
		"cost":          {"single": cost, "tiers": {"*": cost}}, "variants": {"*": variant}, "extensions": object,
	}
	modelV1 := codexShape{"name": nil, "limit": limit, "options": object, "modalities": {"input": strings, "output": strings, "extensions": object}, "thinking": value, "cost": value, "variants": object, "extensions": object}
	secrets := codexShape{"opencode.api-key": nil}
	for index := 1; index <= 63; index++ {
		suffix := strconv.Itoa(index)
		if index < 10 {
			suffix = "0" + suffix
		}
		secrets["opencode.value."+suffix] = nil
	}
	return codexShape{"version": nil, "variant": nil, "provider": {
		"providerID": nil, "format": nil,
		"v1": {"npm": nil, "name": nil, "options": {"baseURL": nil, "apiKey": nil, "headers": headers, "extensions": object}, "models": {"*": modelV1}, "extensions": object},
		"v2": {"package": nil, "name": nil, "canonical": nil, "env": strings, "settings": {"timeout": {"number": nil, "disabled": nil}, "chunkTimeout": nil, "compaction": compaction, "transport": nil, "extensions": object}, "headers": headers, "body": object, "models": {"*": modelV2}, "extensions": object},
	}, "secrets": secrets}
}
