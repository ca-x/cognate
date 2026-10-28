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
