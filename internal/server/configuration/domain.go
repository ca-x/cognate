package configuration

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/server/state"
)

type credentials struct {
	apiKey    Secret
	authToken Secret
}

func (service *Service) load(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	if err := service.ready(ctx); err != nil {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, err
	}
	metadata, payload, err := service.store.ReadConfigurationAssetVersion(ctx, owner, id)
	if err := failure(ctx, err); err != nil {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, err
	}
	if metadata.ID != id {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, ErrUnavailable
	}
	if err := metadataSupported(metadata); err != nil {
		return state.ConfigurationAsset{}, state.ConfigurationPayload{}, err
	}
	return metadata, payload, nil
}

func (service *Service) Edit(ctx context.Context, owner, id string) (EditView, error) {
	metadata, payload, err := service.loadEditable(ctx, owner, id)
	if err != nil {
		return EditView{}, err
	}
	if metadata.AgentID == "gemini" {
		return geminiEdit(ctx, metadata, payload)
	}
	if metadata.AgentID == "codex" {
		return codexEdit(metadata, payload)
	}
	provider, secrets, err := decodeEnvelope(payload)
	if err != nil {
		return EditView{}, err
	}
	defer secrets.clear()
	return editView(metadata, provider, secrets), nil
}

func (service *Service) Update(ctx context.Context, owner, id string, expectedRevision int64, input Draft, mutations []SecretMutation) (EditView, error) {
	if expectedRevision < 1 || expectedRevision == math.MaxInt64 {
		return EditView{}, ErrInvalid
	}
	metadata, payload, err := service.loadEditable(ctx, owner, id)
	if err != nil {
		return EditView{}, err
	}
	if metadata.Revision != expectedRevision {
		return EditView{}, ErrConflict
	}
	if metadata.AgentID == "gemini" {
		return service.updateGemini(ctx, owner, metadata, payload, input, mutations)
	}
	if metadata.AgentID == "codex" {
		return service.updateCodex(ctx, owner, metadata, payload, input, mutations)
	}
	if input.CodexProvider != nil || input.GeminiProvider != nil {
		return EditView{}, ErrInvalid
	}
	_, current, err := decodeEnvelope(payload)
	if err != nil {
		return EditView{}, err
	}
	defer current.clear()
	input, err = normalizeDraft(input)
	if err != nil {
		return EditView{}, err
	}
	secrets, err := mergeSecrets(current, mutations, false)
	if err != nil {
		return EditView{}, err
	}
	defer secrets.clear()
	if err := checkCredentials(input.Provider, secrets); err != nil {
		return EditView{}, err
	}
	encoded, err := encodeEnvelope(input.Provider, secrets)
	if err != nil {
		return EditView{}, err
	}
	updated, err := service.store.UpdateConfigurationAsset(ctx, owner, id, expectedRevision, state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindProvider, Name: input.Name, SchemaVersion: 1, Payload: encoded})
	if err := failure(ctx, err); err != nil {
		return EditView{}, err
	}
	if metadataSupported(updated) != nil || updated.ID != id || updated.Revision != expectedRevision+1 || updated.Name != input.Name {
		return EditView{}, ErrUnavailable
	}
	return editView(updated, input.Provider, secrets), nil
}

func (service *Service) Copy(ctx context.Context, owner, id string, expectedSourceRevision int64, name string) (EditView, error) {
	if expectedSourceRevision < 1 {
		return EditView{}, ErrInvalid
	}
	metadata, payload, err := service.loadEditable(ctx, owner, id)
	if err != nil {
		return EditView{}, err
	}
	if metadata.Revision != expectedSourceRevision {
		return EditView{}, ErrConflict
	}
	if metadata.AgentID == "gemini" {
		view, err := geminiEdit(ctx, metadata, payload)
		if err != nil {
			return EditView{}, err
		}
		if view.HasPreservedExtras {
			return EditView{}, ErrUnsupported
		}
		return service.Create(ctx, owner, Draft{Name: name, GeminiProvider: view.GeminiProvider}, nil)
	}
	if metadata.AgentID == "codex" {
		view, err := codexEdit(metadata, payload)
		if err != nil {
			return EditView{}, err
		}
		if view.HasPreservedExtras {
			return EditView{}, ErrUnsupported
		}
		return service.Create(ctx, owner, Draft{Name: name, CodexProvider: view.CodexProvider}, nil)
	}
	provider, secrets, err := decodeEnvelope(payload)
	if err != nil {
		return EditView{}, err
	}
	defer secrets.clear()
	input, err := normalizeDraft(Draft{Name: name, Provider: provider})
	if err != nil {
		return EditView{}, err
	}
	return service.create(ctx, owner, input, credentials{})
}

func (service *Service) Get(ctx context.Context, owner, id string) (state.ConfigurationAsset, error) {
	if err := service.ready(ctx); err != nil {
		return state.ConfigurationAsset{}, err
	}
	metadata, err := service.store.ConfigurationAsset(ctx, owner, id)
	if err := failure(ctx, err); err != nil {
		return state.ConfigurationAsset{}, err
	}
	if metadata.ID != id {
		return state.ConfigurationAsset{}, ErrUnavailable
	}
	if err := validateMetadata(metadata); err != nil {
		return state.ConfigurationAsset{}, err
	}
	return metadata, nil
}

func (service *Service) List(ctx context.Context, owner string) ([]state.ConfigurationAsset, error) {
	if err := service.ready(ctx); err != nil {
		return nil, err
	}
	metadata, err := service.store.ListConfigurationAssets(ctx, owner)
	if err := failure(ctx, err); err != nil {
		return nil, err
	}
	if len(metadata) > state.MaxConfigurationAssets {
		return nil, ErrUnavailable
	}
	result := make([]state.ConfigurationAsset, 0, len(metadata))
	seen := make(map[string]bool, len(metadata))
	for _, entry := range metadata {
		err := validateMetadata(entry)
		if err != nil {
			return nil, err
		}
		if seen[entry.ID] {
			return nil, ErrUnavailable
		}
		seen[entry.ID] = true
		result = append(result, entry)
	}
	return result, nil
}

func (service *Service) Delete(ctx context.Context, owner, id string, expectedRevision int64) error {
	if expectedRevision < 1 {
		return ErrInvalid
	}
	metadata, err := service.Get(ctx, owner, id)
	if err != nil {
		return err
	}
	if metadata.Revision != expectedRevision {
		return ErrConflict
	}
	return failure(ctx, service.store.DeleteConfigurationAsset(ctx, owner, id, expectedRevision))
}

func (service *Service) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if service == nil || service.store == nil {
		return ErrUnavailable
	}
	return nil
}

func failure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	for _, known := range []struct{ source, target error }{
		{context.Canceled, context.Canceled}, {context.DeadlineExceeded, context.DeadlineExceeded},
		{state.ErrConfigurationInvalid, ErrInvalid}, {state.ErrConfigurationNotFound, ErrNotFound},
		{state.ErrConfigurationConflict, ErrConflict}, {state.ErrConfigurationLimit, ErrLimit},
	} {
		if errors.Is(err, known.source) {
			return known.target
		}
	}
	return ErrUnavailable
}

func normalizeDraft(input Draft) (Draft, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !validText(input.Name, 128) {
		return Draft{}, ErrInvalid
	}
	if input.GeminiProvider != nil {
		if input.CodexProvider != nil || input.Provider != (ClaudeProvider{}) {
			return Draft{}, ErrInvalid
		}
		if err := validateGeminiProvider(*input.GeminiProvider); err != nil {
			return Draft{}, err
		}
		input.GeminiProvider = cloneGeminiProvider(*input.GeminiProvider)
		return input, nil
	}
	if input.CodexProvider != nil {
		if input.Provider != (ClaudeProvider{}) {
			return Draft{}, ErrInvalid
		}
		if err := validateCodexProvider(*input.CodexProvider, false); err != nil {
			return Draft{}, err
		}
		input.CodexProvider = cloneCodexProvider(*input.CodexProvider)
		return input, nil
	}
	if err := validateProvider(input.Provider); err != nil {
		return Draft{}, err
	}
	input.Provider = cloneProvider(input.Provider)
	return input, nil
}

func validText(value string, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func validateProvider(provider ClaudeProvider) error {
	switch provider.AuthMode {
	case NativeLogin, APIKey, AuthToken:
	default:
		return ErrInvalid
	}
	if provider.Endpoint != nil {
		value := *provider.Endpoint
		endpoint, err := url.Parse(value)
		if !validText(value, 2048) || err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.Hostname() == "" || endpoint.User != nil || strings.ContainsAny(value, "?#") || provider.AuthMode == NativeLogin {
			return ErrInvalid
		}
	}
	for _, model := range []*string{provider.Models.Default, provider.Models.Haiku, provider.Models.Sonnet, provider.Models.Opus} {
		if model != nil && !validText(*model, 256) {
			return ErrInvalid
		}
	}
	return nil
}

func cloneProvider(provider ClaudeProvider) ClaudeProvider {
	clone := func(value *string) *string {
		if value == nil {
			return nil
		}
		return new(*value)
	}
	provider.Endpoint = clone(provider.Endpoint)
	provider.Models = ClaudeModels{Default: clone(provider.Models.Default), Haiku: clone(provider.Models.Haiku), Sonnet: clone(provider.Models.Sonnet), Opus: clone(provider.Models.Opus)}
	return provider
}

func mergeSecrets(current credentials, mutations []SecretMutation, creating bool) (_ credentials, finalErr error) {
	current = credentials{apiKey: cloneSecret(current.apiKey), authToken: cloneSecret(current.authToken)}
	defer func() {
		if finalErr != nil {
			current.clear()
		}
	}()
	seen := make(map[Slot]bool, 2)
	for _, mutation := range mutations {
		if seen[mutation.Slot] {
			return credentials{}, ErrInvalid
		}
		seen[mutation.Slot] = true
		var target *Secret
		switch mutation.Slot {
		case APIKeySlot:
			target = &current.apiKey
		case AuthTokenSlot:
			target = &current.authToken
		default:
			return credentials{}, ErrInvalid
		}
		if mutation.Action != Replace && len(secretBytes(mutation.Replacement)) != 0 {
			return credentials{}, ErrInvalid
		}
		switch mutation.Action {
		case Keep:
			if creating {
				return credentials{}, ErrInvalid
			}
		case Clear:
			target.Clear()
		case Replace:
			if !validSecret(mutation.Replacement) {
				return credentials{}, ErrInvalid
			}
			target.Clear()
			*target = cloneSecret(mutation.Replacement)
		default:
			return credentials{}, ErrInvalid
		}
	}
	return current, nil
}

func validSecret(secret Secret) bool {
	return len(secretBytes(secret)) > 0 && len(secretBytes(secret)) <= 8192 && utf8.Valid(secretBytes(secret)) && !strings.ContainsAny(string(secretBytes(secret)), "\x00\r\n")
}

func checkCredentials(provider ClaudeProvider, secrets credentials) error {
	switch provider.AuthMode {
	case NativeLogin:
		if len(secretBytes(secrets.apiKey)) != 0 || len(secretBytes(secrets.authToken)) != 0 {
			return ErrInvalid
		}
	case APIKey:
		if len(secretBytes(secrets.authToken)) != 0 {
			return ErrInvalid
		}
	case AuthToken:
		if len(secretBytes(secrets.apiKey)) != 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func editView(metadata state.ConfigurationAsset, provider ClaudeProvider, secrets credentials) EditView {
	view := EditView{Metadata: metadata, Provider: cloneProvider(provider), Secrets: []SecretStatus{{Slot: APIKeySlot, Present: len(secretBytes(secrets.apiKey)) > 0}, {Slot: AuthTokenSlot, Present: len(secretBytes(secrets.authToken)) > 0}}, Readiness: CredentialComplete}
	if provider.AuthMode == APIKey && !view.Secrets[0].Present || provider.AuthMode == AuthToken && !view.Secrets[1].Present {
		view.Readiness = NeedsCredential
	}
	return view
}

func metadataSupported(metadata state.ConfigurationAsset) error {
	if err := validateMetadata(metadata); err != nil {
		return err
	}
	if metadata.AgentID != "claude" || metadata.Kind != state.ConfigurationKindProvider || metadata.SchemaVersion != 1 {
		return ErrUnsupported
	}
	return nil
}

func validateMetadata(metadata state.ConfigurationAsset) error {
	if len(metadata.ID) != 26 || metadata.Revision < 1 || metadata.SchemaVersion < 1 || metadata.SchemaVersion > 65535 || !validText(metadata.Name, 128) || metadata.Name != strings.TrimSpace(metadata.Name) || metadata.CreatedAt.IsZero() || metadata.UpdatedAt.IsZero() {
		return ErrUnavailable
	}
	for _, letter := range metadata.ID {
		if !(letter >= 'A' && letter <= 'Z' || letter >= '2' && letter <= '7') {
			return ErrUnavailable
		}
	}
	if len(metadata.AgentID) < 1 || len(metadata.AgentID) > 64 || metadata.AgentID[0] < 'a' || metadata.AgentID[0] > 'z' {
		return ErrUnavailable
	}
	for _, letter := range metadata.AgentID {
		if !(letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' || letter == '-') {
			return ErrUnavailable
		}
	}
	switch metadata.Kind {
	case state.ConfigurationKindProvider, state.ConfigurationKindMCP, state.ConfigurationKindPrompt, state.ConfigurationKindAgentSettings, state.ConfigurationKindWorkspace:
	default:
		return ErrUnavailable
	}
	return nil
}

func (service *Service) Create(ctx context.Context, owner string, input Draft, mutations []SecretMutation) (EditView, error) {
	if err := service.ready(ctx); err != nil {
		return EditView{}, err
	}
	input, err := normalizeDraft(input)
	if err != nil {
		return EditView{}, err
	}
	if input.GeminiProvider != nil {
		secret, err := mergeGeminiSecret(Secret{}, mutations, true)
		if err != nil {
			return EditView{}, err
		}
		return service.createGemini(ctx, owner, input, secret)
	}
	if input.CodexProvider != nil {
		secret, err := mergeCodexSecret(Secret{}, mutations, true)
		if err != nil {
			return EditView{}, err
		}
		return service.createCodex(ctx, owner, input, secret)
	}
	secrets, err := mergeSecrets(credentials{}, mutations, true)
	if err != nil {
		return EditView{}, err
	}
	return service.create(ctx, owner, input, secrets)
}

func (service *Service) create(ctx context.Context, owner string, input Draft, secrets credentials) (EditView, error) {
	defer secrets.clear()
	if err := checkCredentials(input.Provider, secrets); err != nil {
		return EditView{}, err
	}
	payload, err := encodeEnvelope(input.Provider, secrets)
	if err != nil {
		return EditView{}, err
	}
	metadata, err := service.store.CreateConfigurationAsset(ctx, owner, state.ConfigurationAssetDraft{AgentID: "claude", Kind: state.ConfigurationKindProvider, Name: input.Name, SchemaVersion: 1, Payload: payload})
	if err := failure(ctx, err); err != nil {
		return EditView{}, err
	}
	if metadataSupported(metadata) != nil || metadata.Revision != 1 || metadata.Name != input.Name {
		return EditView{}, ErrUnavailable
	}
	return editView(metadata, input.Provider, secrets), nil
}
