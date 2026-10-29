package configuration

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"

	"github.com/ca-x/cognate/internal/server/state"
)

type codexStoredSecrets struct {
	ProviderKey *string `json:"provider_key,omitzero"`
}

type codexEnvelope struct {
	Version  *int                `json:"version"`
	Variant  *string             `json:"variant"`
	Provider *CodexProvider      `json:"provider"`
	Secrets  *codexStoredSecrets `json:"secrets"`
}

func encodeCodexEnvelope(provider CodexProvider, secret Secret) (state.ConfigurationPayload, error) {
	if err := validateCodexProvider(provider, len(secret.data) > 0); err != nil {
		return state.ConfigurationPayload{}, err
	}
	stored := &codexStoredSecrets{}
	if len(secret.data) > 0 {
		if !validSecret(secret) {
			return state.ConfigurationPayload{}, ErrInvalid
		}
		stored.ProviderKey = new(string(secret.data))
	}
	encoded, err := json.Marshal(codexEnvelope{Version: new(1), Variant: new("codex-provider"), Provider: &provider, Secrets: stored})
	defer clear(encoded)
	if err != nil || len(encoded) > state.MaxConfigurationPayloadBytes {
		return state.ConfigurationPayload{}, ErrInvalid
	}
	return state.NewConfigurationPayload(encoded), nil
}

func decodeCodexEnvelope(payload state.ConfigurationPayload) (CodexProvider, Secret, bool, error) {
	encoded := payload.Reveal()
	defer clear(encoded)
	if len(encoded) < 1 || len(encoded) > state.MaxConfigurationPayloadBytes {
		return CodexProvider{}, Secret{}, false, ErrInvalid
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(encoded))
	model := codexShape{"model": nil, "displayName": nil, "contextWindow": nil, "supportsParallelToolCalls": nil, "inputModalities": {"*": nil}, "baseInstructions": nil, "reasoningLevels": {"*": nil}, "defaultReasoningLevel": nil}
	shape := codexShape{"version": nil, "variant": nil, "provider": {"authMode": nil, "endpoint": nil, "model": nil, "reasoningEffort": nil, "catalog": {"models": {"*": model}}}, "secrets": {"provider_key": nil}}
	extras, err := scanCodexValue(decoder, shape, true, 0)
	if err != nil {
		return CodexProvider{}, Secret{}, false, ErrInvalid
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return CodexProvider{}, Secret{}, false, ErrInvalid
	}
	var decoded codexEnvelope
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Version == nil || decoded.Variant == nil || decoded.Provider == nil || decoded.Secrets == nil {
		return CodexProvider{}, Secret{}, false, ErrInvalid
	}
	if *decoded.Version != 1 || *decoded.Variant != "codex-provider" {
		return CodexProvider{}, Secret{}, false, ErrUnsupported
	}
	secret := Secret{}
	if decoded.Secrets.ProviderKey != nil {
		secret = NewSecret([]byte(*decoded.Secrets.ProviderKey))
		if !validSecret(secret) {
			clear(secret.data)
			return CodexProvider{}, Secret{}, false, ErrInvalid
		}
	}
	if err := validateCodexProvider(*decoded.Provider, len(secret.data) > 0); err != nil {
		clear(secret.data)
		return CodexProvider{}, Secret{}, false, err
	}
	return *cloneCodexProvider(*decoded.Provider), secret, extras, nil
}

type codexShape map[string]codexShape

func scanCodexValue(decoder *jsontext.Decoder, shape codexShape, known bool, depth int) (bool, error) {
	if depth > 16 {
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
			nested, err := scanCodexValue(decoder, child, known && exists, depth+1)
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
			child, err := scanCodexValue(decoder, shape["*"], known, depth+1)
			if err != nil {
				return false, err
			}
			extras = extras || child
		}
		if _, err := decoder.ReadToken(); err != nil {
			return false, ErrInvalid
		}
	}
	return extras, nil
}
