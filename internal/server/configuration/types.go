package configuration

import (
	"context"
	"errors"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

var (
	ErrInvalid     = errors.New("invalid typed configuration")
	ErrUnsupported = errors.New("unsupported typed configuration")
	ErrNotFound    = errors.New("configuration not found")
	ErrConflict    = errors.New("configuration revision changed")
	ErrInUse       = errors.New("configuration asset in use")
	ErrLimit       = errors.New("configuration capacity exhausted")
	ErrUnavailable = errors.New("configuration unavailable")
)

type AuthMode string

const (
	NativeLogin AuthMode = "native-login"
	APIKey      AuthMode = "api-key"
	AuthToken   AuthMode = "auth-token"
)

type ClaudeModels struct {
	Default *string `json:"default,omitzero"`
	Haiku   *string `json:"haiku,omitzero"`
	Sonnet  *string `json:"sonnet,omitzero"`
	Opus    *string `json:"opus,omitzero"`
}

type ClaudeProvider struct {
	Endpoint *string      `json:"endpoint,omitzero"`
	AuthMode AuthMode     `json:"auth_mode"`
	Models   ClaudeModels `json:"models"`
}

type Draft struct {
	Name             string                        `json:"name"`
	Provider         ClaudeProvider                `json:"provider,omitzero"`
	CodexProvider    *CodexProvider                `json:"codex_provider,omitzero"`
	GeminiProvider   *GeminiProvider               `json:"gemini_provider,omitzero"`
	OpenCodeProvider *agentconfig.OpenCodeProvider `json:"opencode_provider,omitzero"`
}

type GeminiProvider struct {
	AuthMode      agentconfig.GeminiAuthMode      `json:"authMode"`
	Endpoint      *string                         `json:"endpoint,omitzero"`
	KeyTransport  *agentconfig.GeminiKeyTransport `json:"keyTransport,omitzero"`
	EnvModel      *string                         `json:"envModel,omitzero"`
	SettingsModel *string                         `json:"settingsModel,omitzero"`
}

type CodexProvider struct {
	AuthMode        agentconfig.CodexAuthMode         `json:"authMode"`
	Endpoint        string                            `json:"endpoint"`
	Model           string                            `json:"model"`
	ReasoningEffort *agentconfig.CodexReasoningEffort `json:"reasoningEffort,omitzero"`
	Catalog         *agentconfig.CodexCatalogDesired  `json:"catalog,omitzero"`
}

type Slot string

const (
	APIKeySlot           Slot = "claude.api-key"
	AuthTokenSlot        Slot = "claude.auth-token"
	CodexProviderKeySlot Slot = "codex.provider-key"
	GeminiAPIKeySlot     Slot = "gemini.api-key"
)

type SecretAction string

const (
	Keep    SecretAction = "keep"
	Replace SecretAction = "replace"
	Clear   SecretAction = "clear"
)

type SecretMutation struct {
	Slot        Slot         `json:"slot"`
	Action      SecretAction `json:"action"`
	Replacement Secret       `json:"replacement"`
}

type SecretStatus struct {
	Slot    Slot `json:"slot"`
	Present bool `json:"present"`
}

type Readiness string

const (
	CredentialComplete Readiness = "credential-complete"
	NeedsCredential    Readiness = "needs-credential"
)

type EditView struct {
	Metadata           state.ConfigurationAsset      `json:"metadata"`
	Provider           ClaudeProvider                `json:"provider,omitzero"`
	Secrets            []SecretStatus                `json:"secrets"`
	Readiness          Readiness                     `json:"readiness"`
	CodexProvider      *CodexProvider                `json:"codex_provider,omitzero"`
	GeminiProvider     *GeminiProvider               `json:"gemini_provider,omitzero"`
	OpenCodeProvider   *agentconfig.OpenCodeProvider `json:"opencode_provider,omitzero"`
	HasPreservedExtras bool                          `json:"has_preserved_extras"`
}

type Store interface {
	CreateConfigurationAsset(context.Context, string, state.ConfigurationAssetDraft) (state.ConfigurationAsset, error)
	ListConfigurationAssets(context.Context, string) ([]state.ConfigurationAsset, error)
	ConfigurationAsset(context.Context, string, string) (state.ConfigurationAsset, error)
	ReadConfigurationAssetVersion(context.Context, string, string) (state.ConfigurationAsset, state.ConfigurationPayload, error)
	UpdateConfigurationAsset(context.Context, string, string, int64, state.ConfigurationAssetDraft) (state.ConfigurationAsset, error)
	DeleteConfigurationAsset(context.Context, string, string, int64) error
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }
