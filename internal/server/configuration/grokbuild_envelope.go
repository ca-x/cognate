package configuration

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"

	"github.com/ca-x/cognate/internal/server/state"
)

func encodeGrokBuildEnvelope(provider GrokBuildProvider, secret Secret) (state.ConfigurationPayload, error) {
	if err := validateGrokBuildProvider(provider); err != nil {
		return state.ConfigurationPayload{}, err
	}
	present := len(secretBytes(secret)) != 0
	if present && (provider.Mode == "native-login" || !validSecret(secret)) {
		return state.ConfigurationPayload{}, ErrInvalid
	}
	encodedProvider, err := json.Marshal(provider)
	if err != nil {
		return state.ConfigurationPayload{}, ErrInvalid
	}
	var value []byte
	if present {
		value, err = json.Marshal(string(secretBytes(secret)))
		if err != nil {
			return state.ConfigurationPayload{}, ErrInvalid
		}
		defer clear(value)
	}
	encoded := make([]byte, 0, len(encodedProvider)+len(value)+128)
	encoded = append(encoded, `{"version":1,"variant":"grokbuild-provider","provider":`...)
	encoded = append(encoded, encodedProvider...)
	encoded = append(encoded, `,"secrets":{`...)
	if present {
		encoded = append(encoded, `"api_key":`...)
		encoded = append(encoded, value...)
	}
	encoded = append(encoded, '}', '}')
	defer clear(encoded)
	if len(encoded) > state.MaxConfigurationPayloadBytes {
		return state.ConfigurationPayload{}, ErrInvalid
	}
	return state.NewConfigurationPayload(encoded), nil
}

func decodeGrokBuildEnvelope(payload state.ConfigurationPayload) (GrokBuildProvider, Secret, bool, error) {
	encoded := payload.Reveal()
	defer clear(encoded)
	if len(encoded) == 0 || len(encoded) > state.MaxConfigurationPayloadBytes {
		return GrokBuildProvider{}, Secret{}, false, ErrInvalid
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(encoded))
	shape := codexShape{"version": nil, "variant": nil, "provider": {"mode": nil, "model": {"profile": nil, "upstreamModel": nil, "endpoint": nil, "name": nil, "apiBackend": nil, "contextWindow": nil, "envKey": nil}}, "secrets": {"api_key": nil}}
	extras, err := scanCodexValue(decoder, shape, true, 0)
	if err != nil {
		return GrokBuildProvider{}, Secret{}, false, ErrInvalid
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return GrokBuildProvider{}, Secret{}, false, ErrInvalid
	}
	var envelope struct {
		Version  *int                       `json:"version"`
		Variant  *string                    `json:"variant"`
		Provider *GrokBuildProvider         `json:"provider"`
		Secrets  *map[string]jsontext.Value `json:"secrets"`
	}
	defer func() {
		if envelope.Secrets != nil {
			for _, value := range *envelope.Secrets {
				clear(value)
			}
		}
	}()
	if json.Unmarshal(encoded, &envelope) != nil || envelope.Version == nil || envelope.Variant == nil || envelope.Provider == nil || envelope.Secrets == nil {
		return GrokBuildProvider{}, Secret{}, false, ErrInvalid
	}
	if *envelope.Version != 1 || *envelope.Variant != "grokbuild-provider" {
		return GrokBuildProvider{}, Secret{}, false, ErrUnsupported
	}
	if err := validateGrokBuildProvider(*envelope.Provider); err != nil {
		return GrokBuildProvider{}, Secret{}, false, err
	}
	var secret Secret
	if raw, exists := (*envelope.Secrets)["api_key"]; exists {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return GrokBuildProvider{}, Secret{}, false, ErrInvalid
		}
		temporary := []byte(value)
		secret = NewSecret(temporary)
		clear(temporary)
		if !validSecret(secret) || envelope.Provider.Mode == "native-login" {
			secret.Clear()
			return GrokBuildProvider{}, Secret{}, false, ErrInvalid
		}
	}
	return *cloneGrokBuildProvider(*envelope.Provider), secret, extras, nil
}
