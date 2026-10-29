package agentconfig

type CodexAuthMode string
type CodexReasoningEffort string
type CodexModality string
type CodexSecretSlot string
type CodexLocalAuthPolicy string

const (
	CodexProviderKeyAuth      CodexAuthMode        = "provider-key"
	CodexProviderKeySecret    CodexSecretSlot      = "codex.provider-key"
	CodexPreserveLocalLoginV1 CodexLocalAuthPolicy = "preserve-local-login-v1"
	CodexProviderPlanV1       Capability           = "codex.provider-plan.v1"
	CodexPlannerPluginVersion                      = "1.1.0"
	CodexProviderID                                = "cognate"
	CodexWireAPI                                   = "responses"
	MaxCodexDesiredBytes                           = 48 << 10
	MaxCodexCatalogBytes                           = 32 << 10
	MaxCodexRequestBytes                           = 64 << 10
	MaxCodexDeclarationBytes                       = 64 << 10
)

type CodexCatalogModelDesired struct {
	Model                     string                  `json:"model"`
	DisplayName               *string                 `json:"displayName,omitzero"`
	ContextWindow             *uint64                 `json:"contextWindow,omitzero"`
	SupportsParallelToolCalls *bool                   `json:"supportsParallelToolCalls,omitzero"`
	InputModalities           *[]CodexModality        `json:"inputModalities,omitzero"`
	BaseInstructions          *string                 `json:"baseInstructions,omitzero"`
	ReasoningLevels           *[]CodexReasoningEffort `json:"reasoningLevels,omitzero"`
	DefaultReasoningLevel     *CodexReasoningEffort   `json:"defaultReasoningLevel,omitzero"`
}

type CodexCatalogDesired struct {
	Models []CodexCatalogModelDesired `json:"models"`
}

type CodexProviderDesired struct {
	AuthMode        CodexAuthMode         `json:"authMode"`
	Endpoint        string                `json:"endpoint"`
	Model           string                `json:"model"`
	ReasoningEffort *CodexReasoningEffort `json:"reasoningEffort,omitzero"`
	KeyPresent      bool                  `json:"keyPresent"`
	Catalog         *CodexCatalogDesired  `json:"catalog,omitzero"`
}

type CodexProviderPlanRequest struct {
	APIVersion       int                  `json:"apiVersion"`
	PluginID         string               `json:"pluginID"`
	PluginVersion    string               `json:"pluginVersion"`
	DefinitionDigest string               `json:"definitionDigest"`
	Target           Target               `json:"target"`
	Capabilities     []Capability         `json:"capabilities"`
	Desired          CodexProviderDesired `json:"desired"`
}

type CodexProviderDeclarationV1 struct {
	APIVersion          int                   `json:"apiVersion"`
	Target              Target                `json:"target"`
	ProviderID          string                `json:"providerID"`
	WireAPI             string                `json:"wireAPI"`
	Endpoint            string                `json:"endpoint"`
	Model               string                `json:"model"`
	ReasoningEffort     *CodexReasoningEffort `json:"reasoningEffort,omitzero"`
	Catalog             *CodexCatalogDesired  `json:"catalog,omitzero"`
	CredentialReference CodexSecretSlot       `json:"credentialReference"`
	RequiredSlots       []CodexSecretSlot     `json:"requiredSlots"`
	LocalAuthPolicy     CodexLocalAuthPolicy  `json:"localAuthPolicy"`
}
