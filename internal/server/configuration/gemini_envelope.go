package configuration

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

type geminiStoredSecrets struct {
	APIKey *string `json:"api_key,omitzero"`
}

type geminiEnvelope struct {
	Version  *int                 `json:"version"`
	Variant  *string              `json:"variant"`
	Provider *GeminiProvider      `json:"provider"`
	Secrets  *geminiStoredSecrets `json:"secrets"`
}

func encodeGeminiEnvelope(provider GeminiProvider, secret Secret) (state.ConfigurationPayload, error) {
	if err := validateGeminiProvider(provider); err != nil {
		return state.ConfigurationPayload{}, err
	}
	present := len(secretBytes(secret)) != 0
	if present && (provider.AuthMode == agentconfig.GeminiNativeLogin || !validSecret(secret)) {
		return state.ConfigurationPayload{}, ErrInvalid
	}
	stored := &geminiStoredSecrets{}
	if present {
		stored.APIKey = new(string(secretBytes(secret)))
	}
	encoded, err := json.Marshal(geminiEnvelope{Version: new(1), Variant: new("gemini-provider"), Provider: &provider, Secrets: stored})
	defer clear(encoded)
	if err != nil || len(encoded) < 1 || len(encoded) > state.MaxConfigurationPayloadBytes {
		return state.ConfigurationPayload{}, ErrInvalid
	}
	return state.NewConfigurationPayload(encoded), nil
}

func decodeGeminiEnvelope(payload state.ConfigurationPayload) (GeminiProvider, Secret, bool, error) {
	encoded := payload.Reveal()
	defer clear(encoded)
	if len(encoded) < 1 || len(encoded) > state.MaxConfigurationPayloadBytes {
		return GeminiProvider{}, Secret{}, false, ErrInvalid
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(encoded))
	shape := codexShape{"version": nil, "variant": nil, "provider": {"authMode": nil, "endpoint": nil, "keyTransport": nil, "envModel": nil, "settingsModel": nil}, "secrets": {"api_key": nil}}
	extras, err := scanCodexValue(decoder, shape, true, 0)
	if err != nil {
		return GeminiProvider{}, Secret{}, false, ErrInvalid
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return GeminiProvider{}, Secret{}, false, ErrInvalid
	}
	var decoded geminiEnvelope
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Version == nil || decoded.Variant == nil || decoded.Provider == nil || decoded.Secrets == nil {
		return GeminiProvider{}, Secret{}, false, ErrInvalid
	}
	if *decoded.Version != 1 || *decoded.Variant != "gemini-provider" {
		return GeminiProvider{}, Secret{}, false, ErrUnsupported
	}
	if decoded.Provider.AuthMode != "" && decoded.Provider.AuthMode != agentconfig.GeminiNativeLogin && decoded.Provider.AuthMode != agentconfig.GeminiAPIKeyAuth {
		return GeminiProvider{}, Secret{}, false, ErrUnsupported
	}
	if err := validateGeminiProvider(*decoded.Provider); err != nil {
		return GeminiProvider{}, Secret{}, false, err
	}
	var secret Secret
	if decoded.Secrets.APIKey != nil {
		temporary := []byte(*decoded.Secrets.APIKey)
		secret = NewSecret(temporary)
		clear(temporary)
		if !validSecret(secret) || decoded.Provider.AuthMode == agentconfig.GeminiNativeLogin {
			secret.Clear()
			return GeminiProvider{}, Secret{}, false, ErrInvalid
		}
	}
	return *cloneGeminiProvider(*decoded.Provider), secret, extras, nil
}
