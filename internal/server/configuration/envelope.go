package configuration

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"

	"github.com/ca-x/cognate/internal/server/state"
)

type storedSecrets struct {
	APIKey    *string `json:"api_key,omitzero"`
	AuthToken *string `json:"auth_token,omitzero"`
}

func decodeEnvelope(payload state.ConfigurationPayload) (ClaudeProvider, credentials, error) {
	encoded := payload.Reveal()
	defer clear(encoded)
	if len(encoded) < 1 || len(encoded) > state.MaxConfigurationPayloadBytes {
		return ClaudeProvider{}, credentials{}, ErrInvalid
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(encoded))
	for count := 0; ; count++ {
		token, err := decoder.ReadToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || count > 128 || token.Kind() == 'n' {
			return ClaudeProvider{}, credentials{}, ErrInvalid
		}
	}
	var decoded struct {
		Version  *int    `json:"version"`
		Variant  *string `json:"variant"`
		Provider *struct {
			Endpoint *string       `json:"endpoint"`
			AuthMode *AuthMode     `json:"auth_mode"`
			Models   *ClaudeModels `json:"models"`
		} `json:"provider"`
		Secrets *storedSecrets `json:"secrets"`
	}
	if err := json.Unmarshal(encoded, &decoded, json.RejectUnknownMembers(true)); err != nil || decoded.Version == nil || decoded.Variant == nil || decoded.Provider == nil || decoded.Secrets == nil {
		return ClaudeProvider{}, credentials{}, ErrInvalid
	}
	if *decoded.Version != 1 || *decoded.Variant != "claude-provider" {
		return ClaudeProvider{}, credentials{}, ErrUnsupported
	}
	if decoded.Provider.AuthMode == nil || decoded.Provider.Models == nil {
		return ClaudeProvider{}, credentials{}, ErrInvalid
	}
	provider := ClaudeProvider{Endpoint: decoded.Provider.Endpoint, AuthMode: *decoded.Provider.AuthMode, Models: *decoded.Provider.Models}
	if err := validateProvider(provider); err != nil {
		return ClaudeProvider{}, credentials{}, err
	}
	secrets := credentials{}
	if decoded.Secrets.APIKey != nil {
		secrets.apiKey = NewSecret([]byte(*decoded.Secrets.APIKey))
		if !validSecret(secrets.apiKey) {
			secrets.clear()
			return ClaudeProvider{}, credentials{}, ErrInvalid
		}
	}
	if decoded.Secrets.AuthToken != nil {
		secrets.authToken = NewSecret([]byte(*decoded.Secrets.AuthToken))
		if !validSecret(secrets.authToken) {
			secrets.clear()
			return ClaudeProvider{}, credentials{}, ErrInvalid
		}
	}
	if err := checkCredentials(provider, secrets); err != nil {
		secrets.clear()
		return ClaudeProvider{}, credentials{}, err
	}
	return provider, secrets, nil
}

type envelope struct {
	Version  int            `json:"version"`
	Variant  string         `json:"variant"`
	Provider ClaudeProvider `json:"provider"`
	Secrets  storedSecrets  `json:"secrets"`
}

func encodeEnvelope(provider ClaudeProvider, secrets credentials) (state.ConfigurationPayload, error) {
	stored := storedSecrets{}
	if len(secretBytes(secrets.apiKey)) > 0 {
		stored.APIKey = new(string(secretBytes(secrets.apiKey)))
	}
	if len(secretBytes(secrets.authToken)) > 0 {
		stored.AuthToken = new(string(secretBytes(secrets.authToken)))
	}
	encoded, err := json.Marshal(envelope{Version: 1, Variant: "claude-provider", Provider: provider, Secrets: stored})
	if err != nil || len(encoded) > state.MaxConfigurationPayloadBytes {
		return state.ConfigurationPayload{}, ErrInvalid
	}
	defer clear(encoded)
	return state.NewConfigurationPayload(encoded), nil
}
