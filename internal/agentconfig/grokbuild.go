package agentconfig

type GrokBuildMode string
type GrokBuildSecretSlot string

const (
	GrokBuildConfig               Target              = "grokbuild.config"
	GrokBuildNativeLogin          GrokBuildMode       = "native-login"
	GrokBuildCustomModel          GrokBuildMode       = "custom-model"
	GrokBuildAPIKeySecret         GrokBuildSecretSlot = "grokbuild.api-key"
	GrokBuildProviderPlanV1       Capability          = "grokbuild.provider-plan.v1"
	GrokBuildPlannerPluginVersion                     = "1.0.0"
	MaxGrokBuildDesiredBytes                          = 8 << 10
	MaxGrokBuildRequestBytes                          = 16 << 10
	MaxGrokBuildDeclarationBytes                      = 16 << 10
	MaxGrokBuildContextWindow     int64               = 1<<53 - 1
)

type GrokBuildModelDesired struct {
	Profile       string  `json:"profile"`
	UpstreamModel string  `json:"upstreamModel"`
	Endpoint      string  `json:"endpoint"`
	Name          string  `json:"name"`
	APIBackend    string  `json:"apiBackend"`
	ContextWindow int64   `json:"contextWindow"`
	EnvKey        *string `json:"envKey,omitzero"`
	KeyPresent    bool    `json:"keyPresent"`
}

type GrokBuildProviderDesired struct {
	Mode  GrokBuildMode          `json:"mode"`
	Model *GrokBuildModelDesired `json:"model,omitzero"`
}

type GrokBuildProviderPlanRequest struct {
	APIVersion       int                      `json:"apiVersion"`
	PluginID         string                   `json:"pluginID"`
	PluginVersion    string                   `json:"pluginVersion"`
	DefinitionDigest string                   `json:"definitionDigest"`
	Target           Target                   `json:"target"`
	Capabilities     []Capability             `json:"capabilities"`
	Desired          GrokBuildProviderDesired `json:"desired"`
}

type GrokBuildProviderDeclarationV1 struct {
	Version             int                    `json:"version"`
	Target              Target                 `json:"target"`
	Mode                GrokBuildMode          `json:"mode"`
	Model               *GrokBuildModelDesired `json:"model,omitzero"`
	CredentialReference *GrokBuildSecretSlot   `json:"credentialReference,omitzero"`
	RequiredSlots       []GrokBuildSecretSlot  `json:"requiredSlots"`
	LocalAuthPolicy     string                 `json:"localAuthPolicy"`
}
