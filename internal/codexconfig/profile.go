package codexconfig

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"maps"
	"slices"

	"github.com/ca-x/cognate/internal/agentconfig"
)

type nativeProfile struct {
	id                 string
	dialect            string
	defaultStore       AuthStore
	parallel           bool
	legacyInstructions bool
	relativeCatalog    bool
	bearerPrecedence   bool
	rootDefaults       []byte
	modelDefaults      []byte
	requiredRoot       []string
	requiredModel      []string
	ignoredRoot        []string
	ignoredProvider    []string
	ignoredCatalogRoot []string
	ignoredModel       []string
	efforts            []agentconfig.CodexReasoningEffort
	descriptions       map[agentconfig.CodexReasoningEffort]string
}

func approvedProfile(id string) (nativeProfile, bool) {
	for _, profile := range [...]nativeProfile{} {
		if profile.id == id {
			return profile, true
		}
	}
	return nativeProfile{}, false
}

func validateProfile(ctx context.Context, profile nativeProfile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validModelID(profile.id) || profile.dialect != "toml-1.1" || profile.defaultStore < File || profile.defaultStore > Auto || !profile.parallel || !profile.legacyInstructions || !profile.relativeCatalog || !profile.bearerPrecedence {
		return ErrUnsupportedNativeProfile
	}
	if len(profile.rootDefaults)+len(profile.modelDefaults) > 64<<10 {
		return ErrCodecLimit
	}
	for _, names := range [][]string{profile.requiredRoot, profile.requiredModel, profile.ignoredRoot, profile.ignoredProvider, profile.ignoredCatalogRoot, profile.ignoredModel} {
		if len(names) > 512 {
			return ErrCodecLimit
		}
		for index, name := range names {
			if !validModelID(name) || slices.Contains(names[:index], name) {
				return ErrUnsupportedNativeProfile
			}
		}
	}
	if !slices.Contains(profile.requiredRoot, "models") || !slices.Contains(profile.requiredModel, "slug") || len(profile.efforts) < 1 || len(profile.efforts) > 7 || len(profile.descriptions) != len(profile.efforts) {
		return ErrUnsupportedNativeProfile
	}
	for index, effort := range profile.efforts {
		if !validEffort(effort) || slices.Contains(profile.efforts[:index], effort) || !validLiteral(profile.descriptions[effort], 2048) {
			return ErrUnsupportedNativeProfile
		}
	}
	for _, raw := range [][]byte{profile.rootDefaults, profile.modelDefaults} {
		if _, err := readJSON(ctx, raw); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return ErrUnsupportedNativeProfile
		}
	}
	return ctx.Err()
}

func resolveLocalAuth(ctx context.Context, document *tomlDocument, image LocalImage, local resolutionData, profile nativeProfile) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if local.scope != CompleteEffective || local.witness == [32]byte{} || local.profileID != profile.id {
		return false, ErrUnresolvedLocalAuth
	}
	if image.Presence() == PresenceInvalid {
		return false, ErrInvalidLocalImage
	}
	if image.Presence() == Present {
		if _, err := readJSON(ctx, image.get[imageData]().body); err != nil {
			return false, err
		}
	}
	facts := local.auth
	store := profile.defaultStore
	if value, exists := document.lookup("cli_auth_credentials_store"); exists {
		name, ok := value.(string)
		if !ok {
			return false, ErrUnresolvedLocalAuth
		}
		switch name {
		case "file":
			store = File
		case "keyring":
			store = Keyring
		case "auto":
			store = Auto
		case "ephemeral":
			store = Ephemeral
		default:
			return false, ErrUnresolvedLocalAuth
		}
	}
	if store != facts.store {
		return false, ErrUnresolvedLocalAuth
	}
	if store == Keyring || store == Auto {
		switch facts.keyring {
		case FoundCredentialLogin:
			return true, ctx.Err()
		case KeyringMissing:
			if store == Keyring {
				return false, ctx.Err()
			}
		default:
			return false, ErrUnresolvedLocalAuth
		}
	} else if store != File {
		return false, ErrUnresolvedLocalAuth
	}
	if image.Presence() == NotObserved {
		return false, ErrUnresolvedLocalAuth
	}
	switch facts.fileLogin {
	case CredentialLogin:
		if image.Presence() != Present {
			return false, ErrUnresolvedLocalAuth
		}
		return true, ctx.Err()
	case NoCredentialLogin:
		return false, ctx.Err()
	default:
		return false, ErrUnresolvedLocalAuth
	}
}

func validateLocal(ctx context.Context, document *tomlDocument, catalog LocalImage, declaration agentconfig.CodexProviderDeclarationV1, local resolutionData, profile nativeProfile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if document.closed {
		return ErrUnsupportedRepresentation
	}
	owners := local.ownership
	provider, exists := document.lookup("model_providers", "cognate")
	if exists && owners.provider != Managed || !exists && owners.provider != Absent {
		return ErrOwnershipConflict
	}
	if exists {
		object, ok := provider.(map[string]any)
		if !ok {
			return ErrUnsupportedRepresentation
		}
		for name := range object {
			if !slices.Contains([]string{"name", "base_url", "wire_api", "requires_openai_auth", "experimental_bearer_token"}, name) && !slices.Contains(profile.ignoredProvider, name) {
				return ErrOwnershipConflict
			}
			if slices.Contains([]string{"auth", "env_key", "headers", "command", "http_headers", "env_http_headers"}, name) {
				return ErrOwnershipConflict
			}
		}
	}
	for name := range document.semantic {
		if slices.Contains([]string{"model_provider", "model", "model_reasoning_effort", "model_catalog_json", "model_providers", "cli_auth_credentials_store"}, name) {
			continue
		}
		if !slices.Contains(profile.ignoredRoot, name) {
			return ErrOwnershipConflict
		}
	}
	for _, name := range []string{"model_provider", "model", "model_reasoning_effort", "model_catalog_json"} {
		if value, exists := document.lookup(name); exists {
			if _, ok := value.(string); !ok {
				return ErrUnsupportedRepresentation
			}
		}
	}
	if declaration.ReasoningEffort != nil && !slices.Contains(profile.efforts, *declaration.ReasoningEffort) {
		return ErrUnsupportedNativeProfile
	}
	if declaration.ReasoningEffort == nil {
		if value, exists := document.lookup("model_reasoning_effort"); exists {
			effort := agentconfig.CodexReasoningEffort(value.(string))
			if declaration.Catalog == nil || !slices.Contains(profile.efforts, effort) {
				return ErrOwnershipConflict
			}
		}
	}
	if declaration.Catalog != nil {
		if pointer, exists := document.lookup("model_catalog_json"); exists && pointer != "cognate-model-catalog.json" {
			return ErrOwnershipConflict
		}
		if catalog.Presence() == Missing && owners.catalog != Absent || catalog.Presence() == Present && owners.catalog != Managed || catalog.Presence() == NotObserved || catalog.Presence() == PresenceInvalid {
			return ErrOwnershipConflict
		}
	}
	return ctx.Err()
}

func profileTemplateDigest(profile nativeProfile) [32]byte {
	value := append(slices.Clone(profile.rootDefaults), 0)
	value = append(value, profile.modelDefaults...)
	return sha256.Sum256(value)
}

func (profile nativeProfile) clone() nativeProfile {
	profile.rootDefaults = slices.Clone(profile.rootDefaults)
	profile.modelDefaults = slices.Clone(profile.modelDefaults)
	profile.requiredRoot = slices.Clone(profile.requiredRoot)
	profile.requiredModel = slices.Clone(profile.requiredModel)
	profile.ignoredRoot = slices.Clone(profile.ignoredRoot)
	profile.ignoredProvider = slices.Clone(profile.ignoredProvider)
	profile.ignoredCatalogRoot = slices.Clone(profile.ignoredCatalogRoot)
	profile.ignoredModel = slices.Clone(profile.ignoredModel)
	profile.efforts = slices.Clone(profile.efforts)
	profile.descriptions = maps.Clone(profile.descriptions)
	return profile
}

func profileDigest(profile nativeProfile) [32]byte {
	raw, _ := json.Marshal(struct {
		ID           string
		Dialect      string
		DefaultStore AuthStore
		Flags        [4]bool
		Template     [32]byte
		Required     [2][]string
		Ignored      [4][]string
		Efforts      []agentconfig.CodexReasoningEffort
		Descriptions map[agentconfig.CodexReasoningEffort]string
	}{profile.id, profile.dialect, profile.defaultStore, [4]bool{profile.parallel, profile.legacyInstructions, profile.relativeCatalog, profile.bearerPrecedence}, profileTemplateDigest(profile),
		[2][]string{profile.requiredRoot, profile.requiredModel}, [4][]string{profile.ignoredRoot, profile.ignoredProvider, profile.ignoredCatalogRoot, profile.ignoredModel}, profile.efforts, profile.descriptions}, json.Deterministic(true))
	return sha256.Sum256(raw)
}
