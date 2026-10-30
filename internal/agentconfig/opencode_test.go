package agentconfig_test

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func TestOpenCodeProviderUnion(test *testing.T) {
	for name, provider := range map[string]agentconfig.OpenCodeProvider{
		"missing":      {},
		"wrong-format": {ProviderID: "vendor", Format: "v3", V1: &agentconfig.OpenCodeV1{}},
		"mixed":        {ProviderID: "vendor", Format: "v1", V1: &agentconfig.OpenCodeV1{}, V2: &agentconfig.OpenCodeV2{}},
		"bad-id":       {ProviderID: "../private", Format: "v2", V2: &agentconfig.OpenCodeV2{}},
	} {
		test.Run(name, func(test *testing.T) {
			if !errors.Is(agentconfig.ValidateOpenCodeProvider(provider), agentconfig.ErrOpenCodeUnsupported) {
				test.Fatal("unsupported definition accepted")
			}
		})
	}
	for _, provider := range []agentconfig.OpenCodeProvider{
		{ProviderID: "vendor", Format: "v1", V1: &agentconfig.OpenCodeV1{}},
		{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{}},
	} {
		if err := agentconfig.ValidateOpenCodeProvider(provider); err != nil {
			test.Fatal("valid empty definition rejected")
		}
	}
}

func TestOpenCodeProviderCumulativeBudgets(test *testing.T) {
	for _, boundary := range []struct {
		name  string
		limit int
		make  func(int) agentconfig.OpenCodeProvider
	}{
		{"models", 64, func(count int) agentconfig.OpenCodeProvider {
			models := make(map[string]agentconfig.OpenCodeV2Model)
			for index := range count {
				models[fmt.Sprintf("model-%d", index)] = agentconfig.OpenCodeV2Model{}
			}
			return agentconfig.OpenCodeProvider{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{Models: &models}}
		}},
		{"headers", 64, func(count int) agentconfig.OpenCodeProvider {
			headers := make(map[string]string)
			for index := range count {
				headers[fmt.Sprintf("X-Header-%d", index)] = "opencode.api-key"
			}
			return agentconfig.OpenCodeProvider{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{Headers: &headers}}
		}},
		{"extensions", 256, func(count int) agentconfig.OpenCodeProvider {
			fields := make(map[string]agentconfig.OpenCodeValue)
			for index := range count {
				fields[fmt.Sprintf("field-%d", index)] = agentconfig.OpenCodeValue{Bool: new(false)}
			}
			return agentconfig.OpenCodeProvider{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{Extensions: fields}}
		}},
		{"variants", 128, func(count int) agentconfig.OpenCodeProvider {
			variants := make([]agentconfig.OpenCodeVariant, count)
			for index := range count {
				variants[index] = agentconfig.OpenCodeVariant{ID: fmt.Sprintf("variant-%d", index)}
			}
			models := map[string]agentconfig.OpenCodeV2Model{"model": {Variants: &variants}}
			return agentconfig.OpenCodeProvider{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{Models: &models}}
		}},
		{"value-nodes", 2046, func(count int) agentconfig.OpenCodeProvider {
			values := make([]agentconfig.OpenCodeValue, count)
			for index := range count {
				values[index] = agentconfig.OpenCodeValue{Bool: new(false)}
			}
			return openCodeValueProvider(agentconfig.OpenCodeValue{Array: &values})
		}},
		{"value-depth", 10, func(count int) agentconfig.OpenCodeProvider {
			value := agentconfig.OpenCodeValue{Null: new(true)}
			for range count {
				value = agentconfig.OpenCodeValue{Array: new([]agentconfig.OpenCodeValue{value})}
			}
			return openCodeValueProvider(value)
		}},
	} {
		test.Run(boundary.name, func(test *testing.T) {
			if err := agentconfig.ValidateOpenCodeProvider(boundary.make(boundary.limit)); err != nil {
				test.Fatal("exact budget rejected")
			}
			if err := agentconfig.ValidateOpenCodeProvider(boundary.make(boundary.limit + 1)); !errors.Is(err, agentconfig.ErrOpenCodeUnsupported) {
				test.Fatal("budget plus one accepted")
			}
		})
	}
	cyclic := make([]agentconfig.OpenCodeValue, 1)
	cyclic[0] = agentconfig.OpenCodeValue{Array: &cyclic}
	if err := agentconfig.ValidateOpenCodeProvider(openCodeValueProvider(cyclic[0])); !errors.Is(err, agentconfig.ErrOpenCodeUnsupported) {
		test.Fatal("cycle not rejected before serialization")
	}
}

func TestOpenCodeProviderNestedKnownFields(test *testing.T) {
	for name, model := range map[string]agentconfig.OpenCodeV2Model{
		"capabilities-missing-tools":   {Capabilities: &agentconfig.OpenCodeCapabilities{Input: new([]string{}), Output: new([]string{})}},
		"capabilities-missing-input":   {Capabilities: &agentconfig.OpenCodeCapabilities{Tools: new(false), Output: new([]string{})}},
		"cost-missing-input":           {Cost: &agentconfig.OpenCodeCosts{Single: &agentconfig.OpenCodeCost{Output: new("0")}}},
		"cost-mixed-shape":             {Cost: &agentconfig.OpenCodeCosts{Single: &agentconfig.OpenCodeCost{Input: new("0"), Output: new("0")}, Tiers: new([]agentconfig.OpenCodeCost{})}},
		"cost-missing-tier-size":       {Cost: &agentconfig.OpenCodeCosts{Single: &agentconfig.OpenCodeCost{Input: new("0"), Output: new("0"), Tier: &agentconfig.OpenCodeCostTier{Type: "context"}}}},
		"variant-missing-id":           {Variants: new([]agentconfig.OpenCodeVariant{{}})},
		"variant-duplicate-id":         {Variants: new([]agentconfig.OpenCodeVariant{{ID: "same"}, {ID: "same"}})},
		"compaction-invalid":           {Settings: &agentconfig.OpenCodeModelSettings{Compaction: &agentconfig.OpenCodeCompaction{Type: "invalid"}}},
		"compatibility-literal-secret": {Compatibility: &agentconfig.OpenCodeCompatibility{ReasoningField: &agentconfig.OpenCodeValue{Literal: new("synthetic-private")}}},
		"compatibility-invalid-enum":   {Compatibility: &agentconfig.OpenCodeCompatibility{MaxTokensField: new("unknown")}},
	} {
		test.Run(name, func(test *testing.T) {
			provider := agentconfig.OpenCodeProvider{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{Models: new(map[string]agentconfig.OpenCodeV2Model{"model": model})}}
			if err := agentconfig.ValidateOpenCodeProvider(provider); !errors.Is(err, agentconfig.ErrOpenCodeUnsupported) {
				test.Fatal("invalid nested known field accepted")
			}
		})
	}
	for _, settings := range []agentconfig.OpenCodeSettings{
		{Timeout: &agentconfig.OpenCodeTimeout{Disabled: new(true)}},
		{Timeout: &agentconfig.OpenCodeTimeout{}},
		{Timeout: &agentconfig.OpenCodeTimeout{Disabled: new(false), Number: new("1")}},
		{Transport: new("unsupported")},
	} {
		provider := agentconfig.OpenCodeProvider{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{Settings: &settings}}
		if err := agentconfig.ValidateOpenCodeProvider(provider); !errors.Is(err, agentconfig.ErrOpenCodeUnsupported) {
			test.Fatal("invalid provider settings accepted")
		}
	}
}

func TestOpenCodeProviderFinitePrecision(test *testing.T) {
	smallest := "0." + strings.Repeat("0", 323) + "5"
	if err := agentconfig.ValidateOpenCodeProvider(openCodeValueProvider(agentconfig.OpenCodeValue{Number: &smallest})); err != nil {
		test.Fatal("finite representable subnormal rejected")
	}
	for _, number := range []string{"9007199254740991.1", "9007199254740990.5", "0." + strings.Repeat("0", 324) + "1"} {
		if err := agentconfig.ValidateOpenCodeProvider(openCodeValueProvider(agentconfig.OpenCodeValue{Number: &number})); !errors.Is(err, agentconfig.ErrOpenCodeUnsupported) {
			test.Fatal("rounded or underflow number silently coerced")
		}
	}
}

func TestOpenCodeProviderV1OpenModelTypes(test *testing.T) {
	for _, model := range []string{
		`{"thinking":{"bool":false},"cost":{"object":{"input":{"number":"0.125"}}}}`,
		`{"thinking":{"object":{"budget":{"number":"1024"}}},"cost":{"array":[{"number":"0.125"},{"number":"0.5"}]}}`,
	} {
		encoded := []byte(`{"providerID":"vendor","format":"v1","v1":{"models":{"model":` + model + `}}}`)
		var provider agentconfig.OpenCodeProvider
		if err := json.Unmarshal(encoded, &provider); err != nil {
			test.Fatal("V1 flattened open model value lost its type")
		}
		if err := agentconfig.ValidateOpenCodeProvider(provider); err != nil {
			test.Fatal("safe typed V1 open model value rejected")
		}
		var before, after any
		if err := json.Unmarshal(encoded, &before); err != nil {
			test.Fatal("typed fixture invalid")
		}
		roundtrip, err := json.Marshal(provider)
		if err != nil || json.Unmarshal(roundtrip, &after) != nil {
			test.Fatal("typed model roundtrip failed")
		}
		want, _ := json.Marshal(before, json.Deterministic(true))
		got, _ := json.Marshal(after, json.Deterministic(true))
		if string(want) != string(got) {
			test.Fatal("V1 open model type changed")
		}
	}
}

func openCodeValueProvider(value agentconfig.OpenCodeValue) agentconfig.OpenCodeProvider {
	return agentconfig.OpenCodeProvider{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{Body: new(map[string]agentconfig.OpenCodeValue{"value": value})}}
}

func TestOpenCodeProviderOpenValues(test *testing.T) {
	for name, value := range map[string]agentconfig.OpenCodeValue{
		"empty":             {},
		"literal-secret":    {Literal: new("synthetic-sensitive")},
		"mixed":             {Bool: new(false), Number: new("1")},
		"null-false":        {Null: new(false)},
		"invalid-reference": {Reference: new("claude.api-key")},
		"slot-zero":         {Reference: new("opencode.value.00")},
		"slot-over":         {Reference: new("opencode.value.64")},
		"integer-rounded":   {Number: new("9007199254740993")},
		"noncanonical":      {Number: new("01")},
		"infinity":          {Number: new("Infinity")},
		"null-array":        {Array: new([]agentconfig.OpenCodeValue(nil))},
		"null-object":       {Object: new(map[string]agentconfig.OpenCodeValue(nil))},
		"nested-literal":    {Array: new([]agentconfig.OpenCodeValue{{Literal: new("private")}})},
	} {
		test.Run(name, func(test *testing.T) {
			if err := agentconfig.ValidateOpenCodeProvider(openCodeValueProvider(value)); !errors.Is(err, agentconfig.ErrOpenCodeUnsupported) {
				test.Fatal("unsupported open value accepted")
			}
		})
	}
	for _, value := range []agentconfig.OpenCodeValue{
		{Null: new(true)}, {Bool: new(false)}, {Number: new("0.1")}, {Number: new("9007199254740991")},
		{Reference: new("opencode.api-key")}, {Reference: new("opencode.value.63")},
		{Object: new(map[string]agentconfig.OpenCodeValue{})}, {Array: new([]agentconfig.OpenCodeValue{})},
	} {
		if err := agentconfig.ValidateOpenCodeProvider(openCodeValueProvider(value)); err != nil {
			test.Fatal("supported open value rejected")
		}
	}
}

func TestOpenCodeProviderKnownBounds(test *testing.T) {
	for name, mutate := range map[string]func(*agentconfig.OpenCodeV1){
		"url-decoded-control": func(provider *agentconfig.OpenCodeV1) {
			provider.Options = &agentconfig.OpenCodeV1Options{BaseURL: new("https://synthetic.invalid/%0a")}
		},
		"url-decoded-slash": func(provider *agentconfig.OpenCodeV1) {
			provider.Options = &agentconfig.OpenCodeV1Options{BaseURL: new("https://synthetic.invalid/%5c")}
		},
		"key-literal": func(provider *agentconfig.OpenCodeV1) {
			provider.Options = &agentconfig.OpenCodeV1Options{APIKey: new("synthetic-private")}
		},
		"headers-case-duplicate": func(provider *agentconfig.OpenCodeV1) {
			provider.Options = &agentconfig.OpenCodeV1Options{Headers: new(map[string]string{"X-Test": "opencode.value.01", "x-test": "opencode.value.02"})}
		},
		"headers-literal": func(provider *agentconfig.OpenCodeV1) {
			provider.Options = &agentconfig.OpenCodeV1Options{Headers: new(map[string]string{"Authorization": "private"})}
		},
		"header-token": func(provider *agentconfig.OpenCodeV1) {
			provider.Options = &agentconfig.OpenCodeV1Options{Headers: new(map[string]string{"bad key": "opencode.value.01"})}
		},
		"package-command": func(provider *agentconfig.OpenCodeV1) { provider.NPM = new("npm install malicious") },
		"package-url":     func(provider *agentconfig.OpenCodeV1) { provider.NPM = new("https://synthetic.invalid/sdk") },
		"name-control":    func(provider *agentconfig.OpenCodeV1) { provider.Name = new("private\n") },
		"negative-limit": func(provider *agentconfig.OpenCodeV1) {
			provider.Models = new(map[string]agentconfig.OpenCodeV1Model{"model": {Limit: &agentconfig.OpenCodeLimit{Context: new("-1")}}})
		},
		"input-v1": func(provider *agentconfig.OpenCodeV1) {
			provider.Models = new(map[string]agentconfig.OpenCodeV1Model{"model": {Limit: &agentconfig.OpenCodeLimit{Input: new("1")}}})
		},
		"extensions-shadow": func(provider *agentconfig.OpenCodeV1) {
			provider.Extensions = map[string]agentconfig.OpenCodeValue{"options": {Null: new(true)}}
		},
		"extension-key-long": func(provider *agentconfig.OpenCodeV1) {
			provider.Extensions = map[string]agentconfig.OpenCodeValue{strings.Repeat("x", 257): {Null: new(true)}}
		},
	} {
		test.Run(name, func(test *testing.T) {
			provider := agentconfig.OpenCodeProvider{ProviderID: "vendor", Format: "v1", V1: &agentconfig.OpenCodeV1{}}
			mutate(provider.V1)
			if err := agentconfig.ValidateOpenCodeProvider(provider); !errors.Is(err, agentconfig.ErrOpenCodeUnsupported) {
				test.Fatal("unsupported known value accepted")
			}
		})
	}
}
