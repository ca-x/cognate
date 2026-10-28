package agentconfig

type Target string
type Format string
type Capability string

const (
	APIVersion                          = 1
	ClaudeSettings           Target     = "claude.settings"
	CodexConfig              Target     = "codex.config"
	GeminiSettings           Target     = "gemini.settings"
	JSON                     Format     = "json"
	TOML                     Format     = "toml"
	ClaudeProviderEnvV1      Capability = "claude.provider-env.v1"
	ClaudeProviderSwitchV1   Capability = "claude.provider-switch.v1"
	MaxConfigBytes                      = 256 << 10
	MaxModuleBytes                      = 64 << 10
	MaxDefinitionBytes                  = 256 << 10
	MaxRequestBytes                     = 512 << 10
	MaxEnvironmentBytes                 = 64 << 10
	MaxEnvironmentEntries               = 128
	MaxEnvironmentValueBytes            = 8 << 10
	MaxDefinitions                      = 32
)

type Manifest struct {
	APIVersion    int          `json:"apiVersion"`
	PluginID      string       `json:"pluginID"`
	PluginVersion string       `json:"pluginVersion"`
	AgentID       string       `json:"agentID"`
	Target        Target       `json:"target"`
	Format        Format       `json:"format"`
	Capabilities  []Capability `json:"capabilities"`
}

type Descriptor struct {
	Manifest         Manifest `json:"manifest"`
	DefinitionDigest string   `json:"definitionDigest"`
}

type RenderRequest struct {
	APIVersion       int               `json:"apiVersion"`
	PluginID         string            `json:"pluginID"`
	PluginVersion    string            `json:"pluginVersion"`
	DefinitionDigest string            `json:"definitionDigest"`
	Target           Target            `json:"target"`
	Capabilities     []Capability      `json:"capabilities"`
	Base             []byte            `json:"base"`
	ProviderEnv      map[string]string `json:"providerEnv"`
}

type RenderResult struct {
	Target     Target `json:"target"`
	BaseSHA256 string `json:"baseSHA256"`
	NewSHA256  string `json:"newSHA256"`
	Content    []byte `json:"content"`
}

type ClaudeAuthMode string
type ClaudeEnvSlot string
type ClaudeSecretSlot string
type ClaudePatchAction string

const (
	ClaudeNativeLogin   ClaudeAuthMode = "native-login"
	ClaudeAPIKeyAuth    ClaudeAuthMode = "api-key"
	ClaudeAuthTokenAuth ClaudeAuthMode = "auth-token"

	ClaudeEnvAPIKey      ClaudeEnvSlot = "ANTHROPIC_API_KEY"
	ClaudeEnvAuthToken   ClaudeEnvSlot = "ANTHROPIC_AUTH_TOKEN"
	ClaudeEnvBaseURL     ClaudeEnvSlot = "ANTHROPIC_BASE_URL"
	ClaudeEnvModel       ClaudeEnvSlot = "ANTHROPIC_MODEL"
	ClaudeEnvHaikuModel  ClaudeEnvSlot = "ANTHROPIC_DEFAULT_HAIKU_MODEL"
	ClaudeEnvSonnetModel ClaudeEnvSlot = "ANTHROPIC_DEFAULT_SONNET_MODEL"
	ClaudeEnvOpusModel   ClaudeEnvSlot = "ANTHROPIC_DEFAULT_OPUS_MODEL"

	ClaudeAPIKeySecret    ClaudeSecretSlot = "claude.api-key"
	ClaudeAuthTokenSecret ClaudeSecretSlot = "claude.auth-token"

	ClaudeSetLiteral        ClaudePatchAction = "set-literal"
	ClaudeSetSelectedSecret ClaudePatchAction = "set-selected-secret"
	ClaudeRemove            ClaudePatchAction = "remove"
)

type ClaudeModelsDesired struct {
	Default *string `json:"default,omitzero"`
	Haiku   *string `json:"haiku,omitzero"`
	Sonnet  *string `json:"sonnet,omitzero"`
	Opus    *string `json:"opus,omitzero"`
}

type ClaudeProviderDesired struct {
	AuthMode         ClaudeAuthMode      `json:"authMode"`
	Endpoint         *string             `json:"endpoint,omitzero"`
	Models           ClaudeModelsDesired `json:"models"`
	APIKeyPresent    bool                `json:"apiKeyPresent"`
	AuthTokenPresent bool                `json:"authTokenPresent"`
}

type ClaudeProviderPatchRequest struct {
	APIVersion       int                   `json:"apiVersion"`
	PluginID         string                `json:"pluginID"`
	PluginVersion    string                `json:"pluginVersion"`
	DefinitionDigest string                `json:"definitionDigest"`
	Target           Target                `json:"target"`
	Capabilities     []Capability          `json:"capabilities"`
	Desired          ClaudeProviderDesired `json:"desired"`
}

type ClaudeProviderPatchOperation struct {
	Slot      ClaudeEnvSlot     `json:"slot"`
	Action    ClaudePatchAction `json:"action"`
	Literal   *string           `json:"literal,omitzero"`
	Reference *ClaudeSecretSlot `json:"reference,omitzero"`
}

type ClaudeProviderPatchV1 struct {
	Target     Target                         `json:"target"`
	Operations []ClaudeProviderPatchOperation `json:"operations"`
}
