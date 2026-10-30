package agentconfig

import (
	"encoding/json/v2"
	"errors"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	OpenCodeConfig               Target     = "opencode.config"
	OpenCodeProviderPlanV1       Capability = "opencode.provider-plan.v1"
	OpenCodePlannerPluginVersion            = "1.0.0"
	MaxOpenCodeDesiredBytes                 = 64 << 10
	MaxOpenCodeRequestBytes                 = 96 << 10
	MaxOpenCodeDeclarationBytes             = 96 << 10
)

var ErrOpenCodeUnsupported = errors.New("unsupported OpenCode definition")

type OpenCodeValue struct {
	Null      *bool                     `json:"null,omitzero"`
	Bool      *bool                     `json:"bool,omitzero"`
	Number    *string                   `json:"number,omitzero"`
	Literal   *string                   `json:"literal,omitzero"`
	Array     *[]OpenCodeValue          `json:"array,omitzero"`
	Object    *map[string]OpenCodeValue `json:"object,omitzero"`
	Reference *string                   `json:"reference,omitzero"`
}

type OpenCodeProvider struct {
	ProviderID string      `json:"providerID"`
	Format     string      `json:"format"`
	V1         *OpenCodeV1 `json:"v1,omitzero"`
	V2         *OpenCodeV2 `json:"v2,omitzero"`
}

type OpenCodeV1 struct {
	NPM        *string                     `json:"npm,omitzero"`
	Name       *string                     `json:"name,omitzero"`
	Options    *OpenCodeV1Options          `json:"options,omitzero"`
	Models     *map[string]OpenCodeV1Model `json:"models,omitzero"`
	Extensions map[string]OpenCodeValue    `json:"extensions,omitzero"`
}

type OpenCodeV1Options struct {
	BaseURL    *string                  `json:"baseURL,omitzero"`
	APIKey     *string                  `json:"apiKey,omitzero"`
	Headers    *map[string]string       `json:"headers,omitzero"`
	Extensions map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeV1Model struct {
	Name       *string                   `json:"name,omitzero"`
	Limit      *OpenCodeLimit            `json:"limit,omitzero"`
	Options    *map[string]OpenCodeValue `json:"options,omitzero"`
	Modalities *OpenCodeModalities       `json:"modalities,omitzero"`
	Thinking   *OpenCodeValue            `json:"thinking,omitzero"`
	Cost       *OpenCodeValue            `json:"cost,omitzero"`
	Variants   *map[string]OpenCodeValue `json:"variants,omitzero"`
	Extensions map[string]OpenCodeValue  `json:"extensions,omitzero"`
}

type OpenCodeLimit struct {
	Context    *string                  `json:"context,omitzero"`
	Input      *string                  `json:"input,omitzero"`
	Output     *string                  `json:"output,omitzero"`
	Extensions map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeModalities struct {
	Input      *[]string                `json:"input,omitzero"`
	Output     *[]string                `json:"output,omitzero"`
	Extensions map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeV2 struct {
	Package    *string                     `json:"package,omitzero"`
	Name       *string                     `json:"name,omitzero"`
	Canonical  *string                     `json:"canonical,omitzero"`
	Env        *[]string                   `json:"env,omitzero"`
	Settings   *OpenCodeSettings           `json:"settings,omitzero"`
	Headers    *map[string]string          `json:"headers,omitzero"`
	Body       *map[string]OpenCodeValue   `json:"body,omitzero"`
	Models     *map[string]OpenCodeV2Model `json:"models,omitzero"`
	Extensions map[string]OpenCodeValue    `json:"extensions,omitzero"`
}

type OpenCodeSettings struct {
	Timeout      *OpenCodeTimeout         `json:"timeout,omitzero"`
	ChunkTimeout *string                  `json:"chunkTimeout,omitzero"`
	Compaction   *OpenCodeCompaction      `json:"compaction,omitzero"`
	Transport    *string                  `json:"transport,omitzero"`
	Extensions   map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeTimeout struct {
	Number   *string `json:"number,omitzero"`
	Disabled *bool   `json:"disabled,omitzero"`
}

type OpenCodeCompaction struct {
	Type       string                   `json:"type"`
	Extensions map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeModelSettings struct {
	Compaction *OpenCodeCompaction      `json:"compaction,omitzero"`
	Extensions map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeV2Model struct {
	ModelID       *string                   `json:"modelID,omitzero"`
	Family        *string                   `json:"family,omitzero"`
	Name          *string                   `json:"name,omitzero"`
	Package       *string                   `json:"package,omitzero"`
	Disabled      *bool                     `json:"disabled,omitzero"`
	Settings      *OpenCodeModelSettings    `json:"settings,omitzero"`
	Headers       *map[string]string        `json:"headers,omitzero"`
	Body          *map[string]OpenCodeValue `json:"body,omitzero"`
	Compatibility *OpenCodeCompatibility    `json:"compatibility,omitzero"`
	Capabilities  *OpenCodeCapabilities     `json:"capabilities,omitzero"`
	Limit         *OpenCodeLimit            `json:"limit,omitzero"`
	Cost          *OpenCodeCosts            `json:"cost,omitzero"`
	Variants      *[]OpenCodeVariant        `json:"variants,omitzero"`
	Extensions    map[string]OpenCodeValue  `json:"extensions,omitzero"`
}

type OpenCodeVariant struct {
	ID         string                    `json:"id"`
	Settings   *OpenCodeModelSettings    `json:"settings,omitzero"`
	Headers    *map[string]string        `json:"headers,omitzero"`
	Body       *map[string]OpenCodeValue `json:"body,omitzero"`
	Extensions map[string]OpenCodeValue  `json:"extensions,omitzero"`
}

type OpenCodeCompatibility struct {
	ReasoningField            *OpenCodeValue           `json:"reasoningField,omitzero"`
	RequireReasoning          *bool                    `json:"requireReasoning,omitzero"`
	MaxTokensField            *string                  `json:"maxTokensField,omitzero"`
	RequireFinishReason       *bool                    `json:"requireFinishReason,omitzero"`
	RequireAssistantAfterTool *bool                    `json:"requireAssistantAfterTool,omitzero"`
	SupportsPromptCacheKey    *bool                    `json:"supportsPromptCacheKey,omitzero"`
	Extensions                map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeCapabilities struct {
	Tools      *bool                    `json:"tools,omitzero"`
	Input      *[]string                `json:"input,omitzero"`
	Output     *[]string                `json:"output,omitzero"`
	Extensions map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeCosts struct {
	Single *OpenCodeCost   `json:"single,omitzero"`
	Tiers  *[]OpenCodeCost `json:"tiers,omitzero"`
}

type OpenCodeCost struct {
	Input      *string                  `json:"input,omitzero"`
	Output     *string                  `json:"output,omitzero"`
	Cache      *OpenCodeCacheCost       `json:"cache,omitzero"`
	Tier       *OpenCodeCostTier        `json:"tier,omitzero"`
	Extensions map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeCacheCost struct {
	Read       *string                  `json:"read,omitzero"`
	Write      *string                  `json:"write,omitzero"`
	Extensions map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeCostTier struct {
	Type       string                   `json:"type"`
	Size       *string                  `json:"size"`
	Extensions map[string]OpenCodeValue `json:"extensions,omitzero"`
}

type OpenCodeSlotStatus struct {
	Slot    string `json:"slot"`
	Present bool   `json:"present"`
}

type OpenCodeProviderDesired struct {
	Provider OpenCodeProvider     `json:"provider"`
	Slots    []OpenCodeSlotStatus `json:"slots"`
}

type OpenCodeProviderPlanRequest struct {
	APIVersion       int                     `json:"apiVersion"`
	PluginID         string                  `json:"pluginID"`
	PluginVersion    string                  `json:"pluginVersion"`
	DefinitionDigest string                  `json:"definitionDigest"`
	Target           Target                  `json:"target"`
	Capabilities     []Capability            `json:"capabilities"`
	Desired          OpenCodeProviderDesired `json:"desired"`
}

type OpenCodePlanV1 struct {
	Version       int      `json:"version"`
	Target        Target   `json:"target"`
	Mode          string   `json:"mode"`
	Format        string   `json:"format"`
	Container     string   `json:"container"`
	ProviderID    string   `json:"providerID"`
	DefinitionRef string   `json:"definitionRef"`
	RequiredSlots []string `json:"requiredSlots"`
}

type OpenCodeProviderDeclarationV1 struct {
	Plan     OpenCodePlanV1   `json:"plan"`
	Provider OpenCodeProvider `json:"provider"`
}

func ValidateOpenCodeProvider(provider OpenCodeProvider) error {
	_, err := OpenCodeRequiredSlots(provider)
	return err
}

func OpenCodeRequiredSlots(provider OpenCodeProvider) ([]string, error) {
	if !openCodeID(provider.ProviderID) || !(provider.Format == "v1" && provider.V1 != nil && provider.V2 == nil || provider.Format == "v2" && provider.V2 != nil && provider.V1 == nil) {
		return nil, ErrOpenCodeUnsupported
	}
	validator := openCodeValidator{references: make(map[string]bool)}
	valid := false
	if provider.V1 != nil {
		valid = validator.v1(*provider.V1)
	} else {
		valid = validator.v2(*provider.V2)
	}
	if !valid {
		return nil, ErrOpenCodeUnsupported
	}
	encoded, err := json.Marshal(provider)
	if err != nil || len(encoded) > MaxOpenCodeDesiredBytes {
		return nil, ErrOpenCodeUnsupported
	}
	result := make([]string, 0, len(validator.references))
	for slot := range validator.references {
		result = append(result, slot)
	}
	slices.Sort(result)
	return result, nil
}

func CloneOpenCodeProvider(provider OpenCodeProvider) (OpenCodeProvider, error) {
	if err := ValidateOpenCodeProvider(provider); err != nil {
		return OpenCodeProvider{}, err
	}
	encoded, err := json.Marshal(provider)
	var result OpenCodeProvider
	if err != nil || json.Unmarshal(encoded, &result, json.RejectUnknownMembers(true)) != nil {
		return OpenCodeProvider{}, ErrOpenCodeUnsupported
	}
	return result, nil
}

func ValidOpenCodeSlot(slot string) bool {
	if slot == "opencode.api-key" {
		return true
	}
	if len(slot) != len("opencode.value.01") || !strings.HasPrefix(slot, "opencode.value.") {
		return false
	}
	return slot >= "opencode.value.01" && slot <= "opencode.value.63" && slot[len(slot)-1] >= '0' && slot[len(slot)-1] <= '9'
}

type openCodeValidator struct {
	nodes      int
	headers    int
	variants   int
	extensions int
	references map[string]bool
}

func openCodeText(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func openCodeOptional(value *string) bool { return value == nil || openCodeText(*value, 256) }

func openCodePackage(value *string) bool {
	if value == nil {
		return true
	}
	if len(*value) == 0 || len(*value) > 256 {
		return false
	}
	parts := strings.Split(*value, "/")
	if strings.HasPrefix(*value, "@") {
		if len(parts) != 2 {
			return false
		}
		parts[0] = strings.TrimPrefix(parts[0], "@")
	} else if len(parts) != 1 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, character := range part {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.') {
				return false
			}
		}
	}
	return true
}

func openCodeNumber(value string, integer, nonnegative bool) bool {
	if len(value) == 0 || len(value) > 8192 {
		return false
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || strconv.FormatFloat(number, 'f', -1, 64) != value {
		return false
	}
	if math.Trunc(number) == number && math.Abs(number) > 9007199254740991 {
		return false
	}
	return (!integer || math.Trunc(number) == number) && (!nonnegative || number >= 0)
}

func (validator *openCodeValidator) reference(value string) bool {
	if !ValidOpenCodeSlot(value) {
		return false
	}
	validator.references[value] = true
	return len(validator.references) <= 64
}

func (validator *openCodeValidator) value(value OpenCodeValue, depth int) bool {
	validator.nodes++
	if validator.nodes > 2048 || depth > 12 {
		return false
	}
	count := 0
	for _, present := range []bool{value.Null != nil, value.Bool != nil, value.Number != nil, value.Literal != nil, value.Array != nil, value.Object != nil, value.Reference != nil} {
		if present {
			count++
		}
	}
	if count != 1 || value.Literal != nil {
		return false
	}
	switch {
	case value.Null != nil:
		return *value.Null
	case value.Bool != nil:
		return true
	case value.Number != nil:
		return openCodeNumber(*value.Number, false, false)
	case value.Reference != nil:
		return validator.reference(*value.Reference)
	case value.Array != nil:
		if *value.Array == nil || len(*value.Array) > 2048 {
			return false
		}
		for _, child := range *value.Array {
			if !validator.value(child, depth+1) {
				return false
			}
		}
	case value.Object != nil:
		if *value.Object == nil || len(*value.Object) > 2048 {
			return false
		}
		for key, child := range *value.Object {
			if !openCodeText(key, 256) || !validator.value(child, depth+1) {
				return false
			}
		}
	}
	return true
}

func (validator *openCodeValidator) fields(values map[string]OpenCodeValue, reserved ...string) bool {
	validator.extensions += len(values)
	if validator.extensions > 256 {
		return false
	}
	for key, value := range values {
		if !openCodeText(key, 256) || slices.Contains(reserved, key) || !validator.value(value, 1) {
			return false
		}
	}
	return true
}

func (validator *openCodeValidator) object(values *map[string]OpenCodeValue) bool {
	return values == nil || *values != nil && validator.value(OpenCodeValue{Object: values}, 1)
}

func (validator *openCodeValidator) optionalValue(value *OpenCodeValue) bool {
	return value == nil || validator.value(*value, 1)
}

func (validator *openCodeValidator) header(values *map[string]string) bool {
	if values == nil {
		return true
	}
	if *values == nil {
		return false
	}
	validator.headers += len(*values)
	if validator.headers > 64 {
		return false
	}
	seen := make(map[string]bool)
	for key, reference := range *values {
		if len(key) == 0 || len(key) > 128 || seen[strings.ToLower(key)] || !validator.reference(reference) {
			return false
		}
		for _, character := range key {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", character)) {
				return false
			}
		}
		seen[strings.ToLower(key)] = true
	}
	return true
}

func openCodeStrings(values *[]string, identifiers bool) bool {
	if values == nil {
		return true
	}
	if *values == nil || len(*values) > 256 {
		return false
	}
	for _, value := range *values {
		if !openCodeText(value, 256) {
			return false
		}
		if identifiers {
			for index, character := range value {
				if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character == '_' || index > 0 && character >= '0' && character <= '9') {
					return false
				}
			}
		}
	}
	return true
}

func (validator *openCodeValidator) limit(limit *OpenCodeLimit, v1 bool) bool {
	if limit == nil {
		return true
	}
	if v1 && limit.Input != nil {
		return false
	}
	for _, value := range []*string{limit.Context, limit.Input, limit.Output} {
		if value != nil && !openCodeNumber(*value, true, v1) {
			return false
		}
	}
	return validator.fields(limit.Extensions, "context", "input", "output")
}

func openCodeEndpoint(value *string) bool {
	if value == nil {
		return true
	}
	if !openCodeText(*value, 2048) || strings.ContainsAny(*value, "?#\\") {
		return false
	}
	endpoint, err := url.Parse(*value)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.ContainsRune(endpoint.Path, '\\') || strings.ContainsFunc(endpoint.Path, unicode.IsControl) || !utf8.ValidString(endpoint.Path) {
		return false
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
}

func (validator *openCodeValidator) v1(provider OpenCodeV1) bool {
	if !openCodePackage(provider.NPM) || !openCodeOptional(provider.Name) || !validator.fields(provider.Extensions, "npm", "name", "options", "models") {
		return false
	}
	if options := provider.Options; options != nil {
		if !openCodeEndpoint(options.BaseURL) || options.APIKey != nil && !validator.reference(*options.APIKey) || !validator.header(options.Headers) || !validator.fields(options.Extensions, "baseURL", "apiKey", "headers") {
			return false
		}
	}
	if provider.Models == nil {
		return true
	}
	if *provider.Models == nil || len(*provider.Models) > 64 {
		return false
	}
	for id, model := range *provider.Models {
		if !openCodeID(id) || !openCodeOptional(model.Name) || !validator.limit(model.Limit, true) || !validator.object(model.Options) || !validator.optionalValue(model.Thinking) || !validator.optionalValue(model.Cost) || !validator.fields(model.Extensions, "name", "limit", "options", "modalities", "thinking", "cost", "variants") {
			return false
		}
		if modalities := model.Modalities; modalities != nil {
			if !openCodeStrings(modalities.Input, false) || !openCodeStrings(modalities.Output, false) || !validator.fields(modalities.Extensions, "input", "output") {
				return false
			}
		}
		if model.Variants != nil {
			if *model.Variants == nil {
				return false
			}
			validator.variants += len(*model.Variants)
			if validator.variants > 128 {
				return false
			}
			for id, variant := range *model.Variants {
				if !openCodeID(id) || variant.Object == nil || !validator.value(variant, 1) {
					return false
				}
			}
		}
	}
	return true
}

func (validator *openCodeValidator) compaction(compaction *OpenCodeCompaction) bool {
	return compaction == nil || (compaction.Type == "summary" || compaction.Type == "native") && validator.fields(compaction.Extensions, "type")
}

func (validator *openCodeValidator) modelSettings(settings *OpenCodeModelSettings) bool {
	return settings == nil || validator.compaction(settings.Compaction) && validator.fields(settings.Extensions, "compaction")
}

func (validator *openCodeValidator) settings(settings *OpenCodeSettings) bool {
	if settings == nil {
		return true
	}
	if timeout := settings.Timeout; timeout != nil {
		if (timeout.Number == nil) == (timeout.Disabled == nil) || timeout.Number != nil && !openCodeNumber(*timeout.Number, false, false) || timeout.Disabled != nil && *timeout.Disabled {
			return false
		}
	}
	return (settings.ChunkTimeout == nil || openCodeNumber(*settings.ChunkTimeout, false, false)) && (settings.Transport == nil || *settings.Transport == "http" || *settings.Transport == "websocket") && validator.compaction(settings.Compaction) && validator.fields(settings.Extensions, "timeout", "chunkTimeout", "compaction", "transport")
}

func (validator *openCodeValidator) cost(cost OpenCodeCost) bool {
	if cost.Input == nil || cost.Output == nil || !openCodeNumber(*cost.Input, false, false) || !openCodeNumber(*cost.Output, false, false) || !validator.fields(cost.Extensions, "input", "output", "cache", "tier") {
		return false
	}
	if cache := cost.Cache; cache != nil {
		if cache.Read != nil && !openCodeNumber(*cache.Read, false, false) || cache.Write != nil && !openCodeNumber(*cache.Write, false, false) || !validator.fields(cache.Extensions, "read", "write") {
			return false
		}
	}
	if tier := cost.Tier; tier != nil {
		if tier.Type != "context" || tier.Size == nil || !openCodeNumber(*tier.Size, true, false) || !validator.fields(tier.Extensions, "type", "size") {
			return false
		}
	}
	return true
}

func (validator *openCodeValidator) model(model OpenCodeV2Model) bool {
	if !openCodeOptional(model.ModelID) || !openCodeOptional(model.Family) || !openCodeOptional(model.Name) || !openCodePackage(model.Package) || !validator.modelSettings(model.Settings) || !validator.header(model.Headers) || !validator.object(model.Body) || !validator.limit(model.Limit, false) || !validator.fields(model.Extensions, "modelID", "family", "name", "package", "disabled", "settings", "headers", "body", "compatibility", "capabilities", "limit", "cost", "variants") {
		return false
	}
	if compatibility := model.Compatibility; compatibility != nil {
		if compatibility.ReasoningField != nil && (compatibility.ReasoningField.Reference == nil || !validator.value(*compatibility.ReasoningField, 1)) || compatibility.MaxTokensField != nil && *compatibility.MaxTokensField != "max_completion_tokens" && *compatibility.MaxTokensField != "max_tokens" || !validator.fields(compatibility.Extensions, "reasoningField", "requireReasoning", "maxTokensField", "requireFinishReason", "requireAssistantAfterTool", "supportsPromptCacheKey") {
			return false
		}
	}
	if capabilities := model.Capabilities; capabilities != nil {
		if capabilities.Tools == nil || capabilities.Input == nil || capabilities.Output == nil || !openCodeStrings(capabilities.Input, false) || !openCodeStrings(capabilities.Output, false) || !validator.fields(capabilities.Extensions, "tools", "input", "output") {
			return false
		}
	}
	if costs := model.Cost; costs != nil {
		if (costs.Single == nil) == (costs.Tiers == nil) {
			return false
		}
		if costs.Single != nil && !validator.cost(*costs.Single) {
			return false
		}
		if costs.Tiers != nil {
			if *costs.Tiers == nil || len(*costs.Tiers) > 128 {
				return false
			}
			for _, cost := range *costs.Tiers {
				if !validator.cost(cost) {
					return false
				}
			}
		}
	}
	if model.Variants != nil {
		if *model.Variants == nil {
			return false
		}
		validator.variants += len(*model.Variants)
		if validator.variants > 128 {
			return false
		}
		seen := make(map[string]bool)
		for _, variant := range *model.Variants {
			if !openCodeID(variant.ID) || seen[variant.ID] || !validator.modelSettings(variant.Settings) || !validator.header(variant.Headers) || !validator.object(variant.Body) || !validator.fields(variant.Extensions, "id", "settings", "headers", "body") {
				return false
			}
			seen[variant.ID] = true
		}
	}
	return true
}

func (validator *openCodeValidator) v2(provider OpenCodeV2) bool {
	if !openCodePackage(provider.Package) || !openCodeOptional(provider.Name) || !openCodeOptional(provider.Canonical) || !openCodeStrings(provider.Env, true) || !validator.settings(provider.Settings) || !validator.header(provider.Headers) || !validator.object(provider.Body) || !validator.fields(provider.Extensions, "package", "name", "canonical", "env", "settings", "headers", "body", "models") {
		return false
	}
	if provider.Models == nil {
		return true
	}
	if *provider.Models == nil || len(*provider.Models) > 64 {
		return false
	}
	for id, model := range *provider.Models {
		if !openCodeID(id) || !validator.model(model) {
			return false
		}
	}
	return true
}

func openCodeID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, character := range []byte(value) {
		alphanumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
		if !alphanumeric && (index == 0 || character != '.' && character != '_' && character != '-') {
			return false
		}
	}
	return true
}
