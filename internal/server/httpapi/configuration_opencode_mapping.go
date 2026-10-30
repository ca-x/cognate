package httpapi

import (
	"maps"
	"slices"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type configurationOpenCodeMapper struct {
	invalid bool
	nodes   int
}

func configurationOpenCodeInput(draft *configurationv1.Draft, mutations []*configurationv1.SecretMutation) (configuration.Draft, []configuration.SecretMutation, error) {
	if draft == nil || len(mutations) > 64 {
		return configuration.Draft{}, nil, configuration.ErrInvalid
	}
	branch, ok := draft.Payload.(*configurationv1.Draft_OpencodeProvider)
	if !ok || branch == nil || branch.OpencodeProvider == nil {
		return configuration.Draft{}, nil, configuration.ErrInvalid
	}
	mapper := &configurationOpenCodeMapper{}
	provider := mapper.provider(branch.OpencodeProvider)
	if mapper.invalid || provider == nil {
		return configuration.Draft{}, nil, configuration.ErrInvalid
	}
	if err := agentconfig.ValidateOpenCodeProvider(*provider); err != nil {
		return configuration.Draft{}, nil, configuration.ErrUnsupported
	}
	for _, mutation := range mutations {
		if mutation == nil || !agentconfig.ValidOpenCodeSlot(mutation.SlotId) {
			return configuration.Draft{}, nil, configuration.ErrInvalid
		}
	}
	secrets, err := configurationSecretInput(mutations)
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	return configuration.Draft{Name: draft.Name, OpenCodeProvider: provider}, secrets, nil
}

func configurationOpenCodeView(entry configuration.EditView) (*configurationv1.DraftView, error) {
	invalid := func() (*configurationv1.DraftView, error) { return nil, configuration.ErrUnavailable }
	if entry.Metadata.AgentID != "opencode" || entry.Metadata.Kind != state.ConfigurationKindProvider || entry.Metadata.SchemaVersion != 1 || entry.Metadata.ID == "" || entry.Metadata.Name == "" || entry.Metadata.Revision < 1 || entry.OpenCodeProvider == nil || entry.CodexProvider != nil || entry.GeminiProvider != nil || entry.Provider != (configuration.ClaudeProvider{}) {
		return invalid()
	}
	provider, err := agentconfig.CloneOpenCodeProvider(*entry.OpenCodeProvider)
	if err != nil {
		return invalid()
	}
	slots, err := agentconfig.OpenCodeRequiredSlots(provider)
	if err != nil || len(slots) != len(entry.Secrets) {
		return invalid()
	}
	complete := true
	statuses := make([]*configurationv1.SecretStatus, 0, len(slots))
	for index, slot := range slots {
		status := entry.Secrets[index]
		if string(status.Slot) != slot {
			return invalid()
		}
		complete = complete && status.Present
		statuses = append(statuses, &configurationv1.SecretStatus{SlotId: slot, Present: status.Present})
	}
	readiness := configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL
	if complete {
		if entry.Readiness != configuration.CredentialComplete {
			return invalid()
		}
		readiness = configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE
	} else if entry.Readiness != configuration.NeedsCredential {
		return invalid()
	}
	asset, err := configurationAsset(entry.Metadata)
	if err != nil {
		return nil, err
	}
	return &configurationv1.DraftView{Asset: asset, Draft: &configurationv1.Draft{Name: asset.Name, Payload: &configurationv1.Draft_OpencodeProvider{OpencodeProvider: configurationOpenCodeProviderWire(&provider)}}, Secrets: statuses, CredentialReadiness: readiness, HasPreservedExtras: entry.HasPreservedExtras}, nil
}

func (mapper *configurationOpenCodeMapper) provider(source *configurationv1.OpenCodeProvider) *agentconfig.OpenCodeProvider {
	if source == nil {
		mapper.invalid = true
		return nil
	}
	result := &agentconfig.OpenCodeProvider{ProviderID: source.ProviderId}
	switch branch := source.Format.(type) {
	case *configurationv1.OpenCodeProvider_V1:
		if branch == nil || branch.V1 == nil {
			mapper.invalid = true
			return nil
		}
		result.Format, result.V1 = "v1", mapper.v1(branch.V1)
	case *configurationv1.OpenCodeProvider_V2:
		if branch == nil || branch.V2 == nil {
			mapper.invalid = true
			return nil
		}
		result.Format, result.V2 = "v2", mapper.v2(branch.V2)
	default:
		mapper.invalid = true
	}
	return result
}

func configurationOpenCodeProviderWire(source *agentconfig.OpenCodeProvider) *configurationv1.OpenCodeProvider {
	result := &configurationv1.OpenCodeProvider{ProviderId: source.ProviderID}
	if source.V1 != nil {
		result.Format = &configurationv1.OpenCodeProvider_V1{V1: configurationOpenCodeV1Wire(source.V1)}
	} else {
		result.Format = &configurationv1.OpenCodeProvider_V2{V2: configurationOpenCodeV2Wire(source.V2)}
	}
	return result
}

func (mapper *configurationOpenCodeMapper) optionalValue(source *configurationv1.OpenCodeValue) *agentconfig.OpenCodeValue {
	if source == nil {
		return nil
	}
	return mapper.value(source, 1)
}

func (mapper *configurationOpenCodeMapper) value(source *configurationv1.OpenCodeValue, depth int) *agentconfig.OpenCodeValue {
	mapper.nodes++
	if source == nil || mapper.nodes > 2048 || depth > 12 {
		mapper.invalid = true
		return nil
	}
	result := &agentconfig.OpenCodeValue{}
	switch branch := source.Value.(type) {
	case *configurationv1.OpenCodeValue_Null:
		if branch == nil || !branch.Null {
			mapper.invalid = true
			return nil
		}
		result.Null = new(true)
	case *configurationv1.OpenCodeValue_Boolean:
		if branch == nil {
			mapper.invalid = true
			return nil
		}
		result.Bool = new(branch.Boolean)
	case *configurationv1.OpenCodeValue_Number:
		if branch == nil {
			mapper.invalid = true
			return nil
		}
		result.Number = new(branch.Number)
	case *configurationv1.OpenCodeValue_SecretReference:
		if branch == nil {
			mapper.invalid = true
			return nil
		}
		result.Reference = new(branch.SecretReference)
	case *configurationv1.OpenCodeValue_Array:
		if branch == nil || branch.Array == nil || len(branch.Array.Values) > 2048 {
			mapper.invalid = true
			return nil
		}
		values := make([]agentconfig.OpenCodeValue, 0, len(branch.Array.Values))
		for _, item := range branch.Array.Values {
			value := mapper.value(item, depth+1)
			if value == nil {
				return nil
			}
			values = append(values, *value)
		}
		result.Array = &values
	case *configurationv1.OpenCodeValue_Object:
		if branch == nil || branch.Object == nil {
			mapper.invalid = true
			return nil
		}
		result.Object = mapper.object(branch.Object, depth+1)
	default:
		mapper.invalid = true
	}
	return result
}

func configurationOpenCodeValueWire(source *agentconfig.OpenCodeValue) *configurationv1.OpenCodeValue {
	if source == nil {
		return nil
	}
	result := &configurationv1.OpenCodeValue{}
	switch {
	case source.Null != nil:
		result.Value = &configurationv1.OpenCodeValue_Null{Null: *source.Null}
	case source.Bool != nil:
		result.Value = &configurationv1.OpenCodeValue_Boolean{Boolean: *source.Bool}
	case source.Number != nil:
		result.Value = &configurationv1.OpenCodeValue_Number{Number: *source.Number}
	case source.Reference != nil:
		result.Value = &configurationv1.OpenCodeValue_SecretReference{SecretReference: *source.Reference}
	case source.Array != nil:
		values := make([]*configurationv1.OpenCodeValue, 0, len(*source.Array))
		for _, value := range *source.Array {
			values = append(values, configurationOpenCodeValueWire(&value))
		}
		result.Value = &configurationv1.OpenCodeValue_Array{Array: &configurationv1.OpenCodeArray{Values: values}}
	case source.Object != nil:
		result.Value = &configurationv1.OpenCodeValue_Object{Object: configurationOpenCodeObjectWire(source.Object)}
	}
	return result
}

func (mapper *configurationOpenCodeMapper) object(source *configurationv1.OpenCodeObject, depth int) *map[string]agentconfig.OpenCodeValue {
	if source == nil {
		return nil
	}
	if len(source.Entries) > 2048 {
		mapper.invalid = true
		return nil
	}
	result := make(map[string]agentconfig.OpenCodeValue, len(source.Entries))
	for key, item := range source.Entries {
		value := mapper.value(item, depth)
		if value == nil {
			return nil
		}
		result[key] = *value
	}
	return &result
}

func configurationOpenCodeObjectWire(source *map[string]agentconfig.OpenCodeValue) *configurationv1.OpenCodeObject {
	if source == nil {
		return nil
	}
	result := &configurationv1.OpenCodeObject{Entries: make(map[string]*configurationv1.OpenCodeValue, len(*source))}
	for key, item := range *source {
		result.Entries[key] = configurationOpenCodeValueWire(&item)
	}
	return result
}

func (mapper *configurationOpenCodeMapper) extensions(source *configurationv1.OpenCodeObject) map[string]agentconfig.OpenCodeValue {
	values := mapper.object(source, 1)
	if values == nil {
		return nil
	}
	return *values
}

func configurationOpenCodeExtensionsWire(source map[string]agentconfig.OpenCodeValue) *configurationv1.OpenCodeObject {
	if source == nil {
		return nil
	}
	return configurationOpenCodeObjectWire(&source)
}

func (mapper *configurationOpenCodeMapper) strings(source *configurationv1.OpenCodeStrings) *[]string {
	if source == nil {
		return nil
	}
	if len(source.Values) > 256 {
		mapper.invalid = true
		return nil
	}
	return new(append(make([]string, 0, len(source.Values)), source.Values...))
}

func configurationOpenCodeStringsWire(source *[]string) *configurationv1.OpenCodeStrings {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeStrings{Values: slices.Clone(*source)}
}

func (mapper *configurationOpenCodeMapper) headers(source *configurationv1.OpenCodeHeaders) *map[string]string {
	if source == nil {
		return nil
	}
	if len(source.Entries) > 64 {
		mapper.invalid = true
		return nil
	}
	result := make(map[string]string, len(source.Entries))
	maps.Copy(result, source.Entries)
	return &result
}

func configurationOpenCodeHeadersWire(source *map[string]string) *configurationv1.OpenCodeHeaders {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeHeaders{Entries: maps.Clone(*source)}
}

func (mapper *configurationOpenCodeMapper) timeout(source *configurationv1.OpenCodeTimeout) *agentconfig.OpenCodeTimeout {
	if source == nil {
		return nil
	}
	result := &agentconfig.OpenCodeTimeout{}
	switch branch := source.Value.(type) {
	case *configurationv1.OpenCodeTimeout_Number:
		if branch == nil {
			mapper.invalid = true
			return nil
		}
		result.Number = new(branch.Number)
	case *configurationv1.OpenCodeTimeout_Disabled:
		if branch == nil || branch.Disabled {
			mapper.invalid = true
			return nil
		}
		result.Disabled = new(false)
	default:
		mapper.invalid = true
	}
	return result
}

func configurationOpenCodeTimeoutWire(source *agentconfig.OpenCodeTimeout) *configurationv1.OpenCodeTimeout {
	if source == nil {
		return nil
	}
	result := &configurationv1.OpenCodeTimeout{}
	if source.Number != nil {
		result.Value = &configurationv1.OpenCodeTimeout_Number{Number: *source.Number}
	} else {
		result.Value = &configurationv1.OpenCodeTimeout_Disabled{Disabled: *source.Disabled}
	}
	return result
}

func (mapper *configurationOpenCodeMapper) costs(source *configurationv1.OpenCodeCosts) *agentconfig.OpenCodeCosts {
	if source == nil {
		return nil
	}
	result := &agentconfig.OpenCodeCosts{}
	switch branch := source.Shape.(type) {
	case *configurationv1.OpenCodeCosts_Single:
		if branch == nil || branch.Single == nil {
			mapper.invalid = true
			return nil
		}
		result.Single = mapper.cost(branch.Single)
	case *configurationv1.OpenCodeCosts_Tiers:
		if branch == nil || branch.Tiers == nil || len(branch.Tiers.Values) > 128 {
			mapper.invalid = true
			return nil
		}
		values := make([]agentconfig.OpenCodeCost, 0, len(branch.Tiers.Values))
		for _, item := range branch.Tiers.Values {
			if item == nil {
				mapper.invalid = true
				return nil
			}
			values = append(values, *mapper.cost(item))
		}
		result.Tiers = &values
	default:
		mapper.invalid = true
	}
	return result
}

func configurationOpenCodeCostsWire(source *agentconfig.OpenCodeCosts) *configurationv1.OpenCodeCosts {
	if source == nil {
		return nil
	}
	result := &configurationv1.OpenCodeCosts{}
	if source.Single != nil {
		result.Shape = &configurationv1.OpenCodeCosts_Single{Single: configurationOpenCodeCostWire(source.Single)}
	} else {
		values := make([]*configurationv1.OpenCodeCost, 0, len(*source.Tiers))
		for _, item := range *source.Tiers {
			values = append(values, configurationOpenCodeCostWire(&item))
		}
		result.Shape = &configurationv1.OpenCodeCosts_Tiers{Tiers: &configurationv1.OpenCodeCostList{Values: values}}
	}
	return result
}

func (mapper *configurationOpenCodeMapper) v1(source *configurationv1.OpenCodeV1) *agentconfig.OpenCodeV1 {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeV1{
		NPM:        configurationClonePointer(source.Npm),
		Name:       configurationClonePointer(source.Name),
		Options:    mapper.v1Options(source.Options),
		Models:     mapper.models1(source.Models),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeV1Wire(source *agentconfig.OpenCodeV1) *configurationv1.OpenCodeV1 {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeV1{
		Npm:        configurationClonePointer(source.NPM),
		Name:       configurationClonePointer(source.Name),
		Options:    configurationOpenCodeV1OptionsWire(source.Options),
		Models:     configurationOpenCodeModels1Wire(source.Models),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) v1Options(source *configurationv1.OpenCodeV1Options) *agentconfig.OpenCodeV1Options {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeV1Options{
		BaseURL:    configurationClonePointer(source.BaseUrl),
		APIKey:     configurationClonePointer(source.ApiKey),
		Headers:    mapper.headers(source.Headers),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeV1OptionsWire(source *agentconfig.OpenCodeV1Options) *configurationv1.OpenCodeV1Options {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeV1Options{
		BaseUrl:    configurationClonePointer(source.BaseURL),
		ApiKey:     configurationClonePointer(source.APIKey),
		Headers:    configurationOpenCodeHeadersWire(source.Headers),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) v1Model(source *configurationv1.OpenCodeV1Model) *agentconfig.OpenCodeV1Model {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeV1Model{
		Name:       configurationClonePointer(source.Name),
		Limit:      mapper.limit(source.Limit),
		Options:    mapper.object(source.Options, 1),
		Modalities: mapper.modalities(source.Modalities),
		Thinking:   mapper.optionalValue(source.Thinking),
		Cost:       mapper.optionalValue(source.Cost),
		Variants:   mapper.object(source.Variants, 1),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeV1ModelWire(source *agentconfig.OpenCodeV1Model) *configurationv1.OpenCodeV1Model {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeV1Model{
		Name:       configurationClonePointer(source.Name),
		Limit:      configurationOpenCodeLimitWire(source.Limit),
		Options:    configurationOpenCodeObjectWire(source.Options),
		Modalities: configurationOpenCodeModalitiesWire(source.Modalities),
		Thinking:   configurationOpenCodeValueWire(source.Thinking),
		Cost:       configurationOpenCodeValueWire(source.Cost),
		Variants:   configurationOpenCodeObjectWire(source.Variants),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) limit(source *configurationv1.OpenCodeLimit) *agentconfig.OpenCodeLimit {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeLimit{
		Context:    configurationClonePointer(source.Context),
		Input:      configurationClonePointer(source.Input),
		Output:     configurationClonePointer(source.Output),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeLimitWire(source *agentconfig.OpenCodeLimit) *configurationv1.OpenCodeLimit {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeLimit{
		Context:    configurationClonePointer(source.Context),
		Input:      configurationClonePointer(source.Input),
		Output:     configurationClonePointer(source.Output),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) modalities(source *configurationv1.OpenCodeModalities) *agentconfig.OpenCodeModalities {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeModalities{
		Input:      mapper.strings(source.Input),
		Output:     mapper.strings(source.Output),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeModalitiesWire(source *agentconfig.OpenCodeModalities) *configurationv1.OpenCodeModalities {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeModalities{
		Input:      configurationOpenCodeStringsWire(source.Input),
		Output:     configurationOpenCodeStringsWire(source.Output),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) v2(source *configurationv1.OpenCodeV2) *agentconfig.OpenCodeV2 {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeV2{
		Package:    configurationClonePointer(source.Package),
		Name:       configurationClonePointer(source.Name),
		Canonical:  configurationClonePointer(source.Canonical),
		Env:        mapper.strings(source.Env),
		Settings:   mapper.settings(source.Settings),
		Headers:    mapper.headers(source.Headers),
		Body:       mapper.object(source.Body, 1),
		Models:     mapper.models2(source.Models),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeV2Wire(source *agentconfig.OpenCodeV2) *configurationv1.OpenCodeV2 {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeV2{
		Package:    configurationClonePointer(source.Package),
		Name:       configurationClonePointer(source.Name),
		Canonical:  configurationClonePointer(source.Canonical),
		Env:        configurationOpenCodeStringsWire(source.Env),
		Settings:   configurationOpenCodeSettingsWire(source.Settings),
		Headers:    configurationOpenCodeHeadersWire(source.Headers),
		Body:       configurationOpenCodeObjectWire(source.Body),
		Models:     configurationOpenCodeModels2Wire(source.Models),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) settings(source *configurationv1.OpenCodeSettings) *agentconfig.OpenCodeSettings {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeSettings{
		Timeout:      mapper.timeout(source.Timeout),
		ChunkTimeout: configurationClonePointer(source.ChunkTimeout),
		Compaction:   mapper.compaction(source.Compaction),
		Transport:    configurationClonePointer(source.Transport),
		Extensions:   mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeSettingsWire(source *agentconfig.OpenCodeSettings) *configurationv1.OpenCodeSettings {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeSettings{
		Timeout:      configurationOpenCodeTimeoutWire(source.Timeout),
		ChunkTimeout: configurationClonePointer(source.ChunkTimeout),
		Compaction:   configurationOpenCodeCompactionWire(source.Compaction),
		Transport:    configurationClonePointer(source.Transport),
		Extensions:   configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) compaction(source *configurationv1.OpenCodeCompaction) *agentconfig.OpenCodeCompaction {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeCompaction{
		Type:       source.Type,
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeCompactionWire(source *agentconfig.OpenCodeCompaction) *configurationv1.OpenCodeCompaction {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeCompaction{
		Type:       source.Type,
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) modelSettings(source *configurationv1.OpenCodeModelSettings) *agentconfig.OpenCodeModelSettings {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeModelSettings{
		Compaction: mapper.compaction(source.Compaction),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeModelSettingsWire(source *agentconfig.OpenCodeModelSettings) *configurationv1.OpenCodeModelSettings {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeModelSettings{
		Compaction: configurationOpenCodeCompactionWire(source.Compaction),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) v2Model(source *configurationv1.OpenCodeV2Model) *agentconfig.OpenCodeV2Model {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeV2Model{
		ModelID:       configurationClonePointer(source.ModelId),
		Family:        configurationClonePointer(source.Family),
		Name:          configurationClonePointer(source.Name),
		Package:       configurationClonePointer(source.Package),
		Disabled:      configurationClonePointer(source.Disabled),
		Settings:      mapper.modelSettings(source.Settings),
		Headers:       mapper.headers(source.Headers),
		Body:          mapper.object(source.Body, 1),
		Compatibility: mapper.compatibility(source.Compatibility),
		Capabilities:  mapper.capabilities(source.Capabilities),
		Limit:         mapper.limit(source.Limit),
		Cost:          mapper.costs(source.Cost),
		Variants:      mapper.variants(source.Variants),
		Extensions:    mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeV2ModelWire(source *agentconfig.OpenCodeV2Model) *configurationv1.OpenCodeV2Model {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeV2Model{
		ModelId:       configurationClonePointer(source.ModelID),
		Family:        configurationClonePointer(source.Family),
		Name:          configurationClonePointer(source.Name),
		Package:       configurationClonePointer(source.Package),
		Disabled:      configurationClonePointer(source.Disabled),
		Settings:      configurationOpenCodeModelSettingsWire(source.Settings),
		Headers:       configurationOpenCodeHeadersWire(source.Headers),
		Body:          configurationOpenCodeObjectWire(source.Body),
		Compatibility: configurationOpenCodeCompatibilityWire(source.Compatibility),
		Capabilities:  configurationOpenCodeCapabilitiesWire(source.Capabilities),
		Limit:         configurationOpenCodeLimitWire(source.Limit),
		Cost:          configurationOpenCodeCostsWire(source.Cost),
		Variants:      configurationOpenCodeVariantsWire(source.Variants),
		Extensions:    configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) variant(source *configurationv1.OpenCodeVariant) *agentconfig.OpenCodeVariant {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeVariant{
		ID:         source.Id,
		Settings:   mapper.modelSettings(source.Settings),
		Headers:    mapper.headers(source.Headers),
		Body:       mapper.object(source.Body, 1),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeVariantWire(source *agentconfig.OpenCodeVariant) *configurationv1.OpenCodeVariant {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeVariant{
		Id:         source.ID,
		Settings:   configurationOpenCodeModelSettingsWire(source.Settings),
		Headers:    configurationOpenCodeHeadersWire(source.Headers),
		Body:       configurationOpenCodeObjectWire(source.Body),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) compatibility(source *configurationv1.OpenCodeCompatibility) *agentconfig.OpenCodeCompatibility {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeCompatibility{
		ReasoningField:            mapper.optionalValue(source.ReasoningField),
		RequireReasoning:          configurationClonePointer(source.RequireReasoning),
		MaxTokensField:            configurationClonePointer(source.MaxTokensField),
		RequireFinishReason:       configurationClonePointer(source.RequireFinishReason),
		RequireAssistantAfterTool: configurationClonePointer(source.RequireAssistantAfterTool),
		SupportsPromptCacheKey:    configurationClonePointer(source.SupportsPromptCacheKey),
		Extensions:                mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeCompatibilityWire(source *agentconfig.OpenCodeCompatibility) *configurationv1.OpenCodeCompatibility {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeCompatibility{
		ReasoningField:            configurationOpenCodeValueWire(source.ReasoningField),
		RequireReasoning:          configurationClonePointer(source.RequireReasoning),
		MaxTokensField:            configurationClonePointer(source.MaxTokensField),
		RequireFinishReason:       configurationClonePointer(source.RequireFinishReason),
		RequireAssistantAfterTool: configurationClonePointer(source.RequireAssistantAfterTool),
		SupportsPromptCacheKey:    configurationClonePointer(source.SupportsPromptCacheKey),
		Extensions:                configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) capabilities(source *configurationv1.OpenCodeCapabilities) *agentconfig.OpenCodeCapabilities {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeCapabilities{
		Tools:      configurationClonePointer(source.Tools),
		Input:      mapper.strings(source.Input),
		Output:     mapper.strings(source.Output),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeCapabilitiesWire(source *agentconfig.OpenCodeCapabilities) *configurationv1.OpenCodeCapabilities {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeCapabilities{
		Tools:      configurationClonePointer(source.Tools),
		Input:      configurationOpenCodeStringsWire(source.Input),
		Output:     configurationOpenCodeStringsWire(source.Output),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) cost(source *configurationv1.OpenCodeCost) *agentconfig.OpenCodeCost {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeCost{
		Input:      configurationClonePointer(source.Input),
		Output:     configurationClonePointer(source.Output),
		Cache:      mapper.cacheCost(source.Cache),
		Tier:       mapper.costTier(source.Tier),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeCostWire(source *agentconfig.OpenCodeCost) *configurationv1.OpenCodeCost {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeCost{
		Input:      configurationClonePointer(source.Input),
		Output:     configurationClonePointer(source.Output),
		Cache:      configurationOpenCodeCacheCostWire(source.Cache),
		Tier:       configurationOpenCodeCostTierWire(source.Tier),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) cacheCost(source *configurationv1.OpenCodeCacheCost) *agentconfig.OpenCodeCacheCost {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeCacheCost{
		Read:       configurationClonePointer(source.Read),
		Write:      configurationClonePointer(source.Write),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeCacheCostWire(source *agentconfig.OpenCodeCacheCost) *configurationv1.OpenCodeCacheCost {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeCacheCost{
		Read:       configurationClonePointer(source.Read),
		Write:      configurationClonePointer(source.Write),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) costTier(source *configurationv1.OpenCodeCostTier) *agentconfig.OpenCodeCostTier {
	if source == nil {
		return nil
	}
	return &agentconfig.OpenCodeCostTier{
		Type:       source.Type,
		Size:       configurationClonePointer(source.Size),
		Extensions: mapper.extensions(source.Extensions),
	}
}

func configurationOpenCodeCostTierWire(source *agentconfig.OpenCodeCostTier) *configurationv1.OpenCodeCostTier {
	if source == nil {
		return nil
	}
	return &configurationv1.OpenCodeCostTier{
		Type:       source.Type,
		Size:       configurationClonePointer(source.Size),
		Extensions: configurationOpenCodeExtensionsWire(source.Extensions),
	}
}

func (mapper *configurationOpenCodeMapper) models1(source *configurationv1.OpenCodeV1Models) *map[string]agentconfig.OpenCodeV1Model {
	if source == nil {
		return nil
	}
	if len(source.Entries) > 64 {
		mapper.invalid = true
		return nil
	}
	result := make(map[string]agentconfig.OpenCodeV1Model, len(source.Entries))
	for key, item := range source.Entries {
		if item == nil {
			mapper.invalid = true
			return nil
		}
		result[key] = *mapper.v1Model(item)
	}
	return &result
}

func configurationOpenCodeModels1Wire(source *map[string]agentconfig.OpenCodeV1Model) *configurationv1.OpenCodeV1Models {
	if source == nil {
		return nil
	}
	result := &configurationv1.OpenCodeV1Models{Entries: make(map[string]*configurationv1.OpenCodeV1Model, len(*source))}
	for key, item := range *source {
		result.Entries[key] = configurationOpenCodeV1ModelWire(&item)
	}
	return result
}

func (mapper *configurationOpenCodeMapper) models2(source *configurationv1.OpenCodeV2Models) *map[string]agentconfig.OpenCodeV2Model {
	if source == nil {
		return nil
	}
	if len(source.Entries) > 64 {
		mapper.invalid = true
		return nil
	}
	result := make(map[string]agentconfig.OpenCodeV2Model, len(source.Entries))
	for key, item := range source.Entries {
		if item == nil {
			mapper.invalid = true
			return nil
		}
		result[key] = *mapper.v2Model(item)
	}
	return &result
}

func configurationOpenCodeModels2Wire(source *map[string]agentconfig.OpenCodeV2Model) *configurationv1.OpenCodeV2Models {
	if source == nil {
		return nil
	}
	result := &configurationv1.OpenCodeV2Models{Entries: make(map[string]*configurationv1.OpenCodeV2Model, len(*source))}
	for key, item := range *source {
		result.Entries[key] = configurationOpenCodeV2ModelWire(&item)
	}
	return result
}

func (mapper *configurationOpenCodeMapper) variants(source *configurationv1.OpenCodeVariants) *[]agentconfig.OpenCodeVariant {
	if source == nil {
		return nil
	}
	if len(source.Values) > 128 {
		mapper.invalid = true
		return nil
	}
	result := make([]agentconfig.OpenCodeVariant, len(source.Values))
	for key, item := range source.Values {
		if item == nil {
			mapper.invalid = true
			return nil
		}
		result[key] = *mapper.variant(item)
	}
	return &result
}

func configurationOpenCodeVariantsWire(source *[]agentconfig.OpenCodeVariant) *configurationv1.OpenCodeVariants {
	if source == nil {
		return nil
	}
	result := &configurationv1.OpenCodeVariants{Values: make([]*configurationv1.OpenCodeVariant, len(*source))}
	for key, item := range *source {
		result.Values[key] = configurationOpenCodeVariantWire(&item)
	}
	return result
}
