package agentconfig

type GeminiAuthMode string
type GeminiKeyTransport string
type GeminiSecretSlot string
type GeminiLocalAuthPolicy string

const (
	GeminiNativeLogin          GeminiAuthMode        = "native-login"
	GeminiAPIKeyAuth           GeminiAuthMode        = "api-key"
	GeminiNativeDefault        GeminiKeyTransport    = "native-default"
	GeminiBearer               GeminiKeyTransport    = "bearer"
	GeminiAPIKeySecret         GeminiSecretSlot      = "gemini.api-key"
	GeminiPreserveLocalLoginV1 GeminiLocalAuthPolicy = "preserve-local-login-v1"
	GeminiProviderPlanV1       Capability            = "gemini.provider-plan.v1"
	GeminiPlannerPluginVersion                       = "1.1.0"
	MaxGeminiDesiredBytes                            = 8 << 10
	MaxGeminiRequestBytes                            = 16 << 10
	MaxGeminiDeclarationBytes                        = 16 << 10
)

type GeminiProviderDesired struct {
	AuthMode      GeminiAuthMode      `json:"authMode"`
	Endpoint      *string             `json:"endpoint,omitzero"`
	KeyTransport  *GeminiKeyTransport `json:"keyTransport,omitzero"`
	EnvModel      *string             `json:"envModel,omitzero"`
	SettingsModel *string             `json:"settingsModel,omitzero"`
	KeyPresent    bool                `json:"keyPresent"`
}

type GeminiProviderPlanRequest struct {
	APIVersion       int                   `json:"apiVersion"`
	PluginID         string                `json:"pluginID"`
	PluginVersion    string                `json:"pluginVersion"`
	DefinitionDigest string                `json:"definitionDigest"`
	Target           Target                `json:"target"`
	Capabilities     []Capability          `json:"capabilities"`
	Desired          GeminiProviderDesired `json:"desired"`
}

type GeminiProviderDeclarationV1 struct {
	Version             int                   `json:"version"`
	Target              Target                `json:"target"`
	AuthMode            GeminiAuthMode        `json:"authMode"`
	Endpoint            *string               `json:"endpoint,omitzero"`
	KeyTransport        *GeminiKeyTransport   `json:"keyTransport,omitzero"`
	EnvModel            *string               `json:"envModel,omitzero"`
	SettingsModel       *string               `json:"settingsModel,omitzero"`
	SelectedType        string                `json:"selectedType"`
	CredentialReference *GeminiSecretSlot     `json:"credentialReference,omitzero"`
	RequiredSlots       []GeminiSecretSlot    `json:"requiredSlots"`
	LocalAuthPolicy     GeminiLocalAuthPolicy `json:"localAuthPolicy"`
}
