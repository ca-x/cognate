package configuration

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"reflect"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

func openCodeFullProvider(format string) agentconfig.OpenCodeProvider {
	object := map[string]agentconfig.OpenCodeValue{"nested": {Array: new([]agentconfig.OpenCodeValue{{Null: new(true)}, {Bool: new(false)}, {Number: new("0.125")}, {Reference: new("opencode.value.01")}})}}
	provider := agentconfig.OpenCodeProvider{ProviderID: "Vendor_1", Format: format}
	if format == "v1" {
		provider.V1 = &agentconfig.OpenCodeV1{
			NPM: new("@ai-sdk/openai-compatible"), Name: new("Provider name"), Extensions: map[string]agentconfig.OpenCodeValue{"custom": {Bool: new(false)}},
			Options: &agentconfig.OpenCodeV1Options{BaseURL: new("https://synthetic.invalid:443/a%2Fb"), APIKey: new("opencode.api-key"), Headers: new(map[string]string{"X-Custom": "opencode.value.01"}), Extensions: object},
			Models: new(map[string]agentconfig.OpenCodeV1Model{"model-1": {
				Name: new("Model name"), Limit: &agentconfig.OpenCodeLimit{Context: new("0"), Output: new("8192"), Extensions: object},
				Options: &object, Modalities: &agentconfig.OpenCodeModalities{Input: new([]string{"text", "image"}), Output: new([]string{}), Extensions: object}, Thinking: &agentconfig.OpenCodeValue{Bool: new(false)},
				Cost:     &agentconfig.OpenCodeValue{Object: new(map[string]agentconfig.OpenCodeValue{"input": {Number: new("0.125")}, "output": {Number: new("0")}})},
				Variants: new(map[string]agentconfig.OpenCodeValue{"reasoning": {Object: &object}}), Extensions: object,
			}}),
		}
		return provider
	}
	provider.V2 = &agentconfig.OpenCodeV2{
		Package: new("@ai-sdk/openai-compatible"), Name: new("Provider name"), Canonical: new("vendor"), Env: new([]string{"SYNTHETIC_API_KEY"}),
		Settings: &agentconfig.OpenCodeSettings{Timeout: &agentconfig.OpenCodeTimeout{Disabled: new(false)}, ChunkTimeout: new("0.5"), Compaction: &agentconfig.OpenCodeCompaction{Type: "summary", Extensions: object}, Transport: new("websocket"), Extensions: object},
		Headers:  new(map[string]string{"Authorization": "opencode.api-key"}), Body: &object, Extensions: object,
		Models: new(map[string]agentconfig.OpenCodeV2Model{"model-1": {
			ModelID: new("upstream/model:1"), Family: new("model-family"), Name: new("Model name"), Package: new("sdk"), Disabled: new(false),
			Settings: &agentconfig.OpenCodeModelSettings{Compaction: &agentconfig.OpenCodeCompaction{Type: "native", Extensions: object}, Extensions: object}, Headers: new(map[string]string{"X-Model": "opencode.value.01"}), Body: &object,
			Compatibility: &agentconfig.OpenCodeCompatibility{ReasoningField: &agentconfig.OpenCodeValue{Reference: new("opencode.value.01")}, RequireReasoning: new(false), MaxTokensField: new("max_tokens"), RequireFinishReason: new(false), RequireAssistantAfterTool: new(true), SupportsPromptCacheKey: new(false), Extensions: object},
			Capabilities:  &agentconfig.OpenCodeCapabilities{Tools: new(false), Input: new([]string{"text"}), Output: new([]string{}), Extensions: object},
			Limit:         &agentconfig.OpenCodeLimit{Context: new("1"), Input: new("0"), Output: new("8192"), Extensions: object},
			Cost:          &agentconfig.OpenCodeCosts{Tiers: new([]agentconfig.OpenCodeCost{{Input: new("0.125"), Output: new("0"), Cache: &agentconfig.OpenCodeCacheCost{Read: new("0"), Write: new("0.25"), Extensions: object}, Tier: &agentconfig.OpenCodeCostTier{Type: "context", Size: new("8192"), Extensions: object}, Extensions: object}})},
			Variants:      new([]agentconfig.OpenCodeVariant{{ID: "reasoning", Settings: &agentconfig.OpenCodeModelSettings{Compaction: &agentconfig.OpenCodeCompaction{Type: "native", Extensions: object}, Extensions: object}, Headers: new(map[string]string{"X-Variant": "opencode.value.01"}), Body: &object, Extensions: object}}), Extensions: object,
		}}),
	}
	return provider
}

func TestOpenCodeEnvelopeFullDeclaration(test *testing.T) {
	for _, format := range []string{"v1", "v2"} {
		test.Run(format, func(test *testing.T) {
			provider := openCodeFullProvider(format)
			secret := NewSecret([]byte("synthetic-full-envelope-private"))
			defer secret.Clear()
			secrets := openCodeCredentials{"opencode.api-key": secret, "opencode.value.01": cloneSecret(secret)}
			defer secrets.clear()
			payload, err := encodeOpenCodeEnvelope(provider, secrets)
			if err != nil {
				test.Fatal("full declaration encode failed")
			}
			decoded, owned, extras, err := decodeOpenCodeEnvelope(payload)
			defer owned.clear()
			if err != nil || extras || !reflect.DeepEqual(decoded, provider) || string(secretBytes(owned["opencode.value.01"])) != "synthetic-full-envelope-private" {
				test.Fatal("full declaration lost a nested field")
			}
			store, _, _ := geminiStore(test)
			asset, err := New(store).Create(test.Context(), "alice", Draft{Name: "full", OpenCodeProvider: &provider}, nil)
			if err != nil {
				test.Fatal("full real Store declaration failed")
			}
			copy, err := New(store).Copy(test.Context(), "alice", asset.Metadata.ID, 1, "full-copy")
			if err != nil || !reflect.DeepEqual(copy.OpenCodeProvider, &provider) {
				test.Fatal("copy dropped nested fields")
			}
		})
	}
}

func TestOpenCodeEnvelopeUnknownExtrasReadOnly(test *testing.T) {
	for name, alter := range map[string]func([]byte) []byte{
		"envelope": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"version":1`), []byte(`"future":{"private":"synthetic-future-private"},"version":1`), 1)
		},
		"provider": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"providerID"`), []byte(`"future":{"private":"synthetic-future-private"},"providerID"`), 1)
		},
		"nested-model": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"modelID"`), []byte(`"future":{"private":"synthetic-future-private"},"modelID"`), 1)
		},
		"nested-value": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"number":"0.125"`), []byte(`"future":{"private":"synthetic-future-private"},"number":"0.125"`), 1)
		},
		"future-secret": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"secrets":{}`), []byte(`"secrets":{"opencode.future":{"private":"synthetic-future-private"}}`), 1)
		},
	} {
		test.Run(name, func(test *testing.T) {
			store, _, _ := geminiStore(test)
			provider := openCodeFullProvider("v2")
			payload, err := encodeOpenCodeEnvelope(provider, nil)
			if err != nil {
				test.Fatal("future fixture encode failed")
			}
			raw := payload.Reveal()
			defer clear(raw)
			future := alter(raw)
			defer clear(future)
			if bytes.Equal(raw, future) {
				test.Fatal("future fixture did not mutate")
			}
			asset, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "opencode", Kind: state.ConfigurationKindProvider, Name: "future", SchemaVersion: 1, Payload: state.NewConfigurationPayload(future)})
			if err != nil {
				test.Fatal("future fixture persist failed")
			}
			observed := &geminiObservedStore{Store: store}
			service := New(observed)
			view, err := service.Edit(test.Context(), "alice", asset.ID)
			if err != nil || !view.HasPreservedExtras || !reflect.DeepEqual(view.OpenCodeProvider, &provider) {
				test.Fatal("future known projection or extras flag incorrect")
			}
			encoded, err := json.Marshal(view)
			if err != nil || bytes.Contains(encoded, []byte("synthetic-future-private")) {
				test.Fatal("unknown extras escaped private envelope")
			}
			if _, err := service.Update(test.Context(), "alice", asset.ID, 1, Draft{Name: "overwrite", OpenCodeProvider: &provider}, nil); !errors.Is(err, ErrUnsupported) {
				test.Fatal("unknown extras rewritten by update")
			}
			if _, err := service.Copy(test.Context(), "alice", asset.ID, 1, "copy"); !errors.Is(err, ErrUnsupported) {
				test.Fatal("unknown extras silently omitted by copy")
			}
			if _, err := service.OpenCodeProviderDesired(test.Context(), "alice", asset.ID, 1); !errors.Is(err, ErrUnsupported) {
				test.Fatal("unknown extras planned")
			}
			if observed.creates+observed.updates != 0 {
				test.Fatal("unknown extras attempted business write")
			}
			_, after, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", asset.ID)
			if err != nil {
				test.Fatal("future durable oracle failed")
			}
			afterRaw := after.Reveal()
			defer clear(afterRaw)
			if !bytes.Equal(afterRaw, future) {
				test.Fatal("unknown stored bytes drifted")
			}
			if _, err := service.Get(test.Context(), "alice", asset.ID); err != nil {
				test.Fatal("metadata blocked by future payload")
			}
			if err := service.Delete(test.Context(), "alice", asset.ID, 1); err != nil {
				test.Fatal("future payload cannot be deleted")
			}
		})
	}
}

func TestOpenCodeEnvelopeKnownNullAndDuplicate(test *testing.T) {
	provider := openCodeFullProvider("v2")
	payload, err := encodeOpenCodeEnvelope(provider, nil)
	if err != nil {
		test.Fatal("invalid-fixture encode failed")
	}
	raw := payload.Reveal()
	defer clear(raw)
	for name, pair := range map[string][2]string{
		"known-null":      {`"name":"Provider name"`, `"name":null`},
		"nested-null":     {`"disabled":false`, `"disabled":null`},
		"union-null":      {`"null":true`, `"null":null`},
		"duplicate-field": {`"version":1`, `"version":1,"version":1`},
		"duplicate-map":   {`"model-1":{`, `"model-1":{},"model-1":{`},
		"secret-raw-null": {`"secrets":{}`, `"secrets":{"opencode.api-key":null}`},
		"literal-secret":  {`"reference":"opencode.value.01"`, `"literal":"synthetic-private-value"`},
		"bad-number":      {`"number":"0.125"`, `"number":"9007199254740993"`},
	} {
		test.Run(name, func(test *testing.T) {
			altered := bytes.Replace(raw, []byte(pair[0]), []byte(pair[1]), 1)
			defer clear(altered)
			if bytes.Equal(raw, altered) {
				test.Fatal("invalid-fixture did not mutate")
			}
			_, secrets, _, err := decodeOpenCodeEnvelope(state.NewConfigurationPayload(altered))
			defer secrets.clear()
			if err == nil || len(secrets) != 0 {
				test.Fatal("invalid stored member accepted or error retained secrets")
			}
		})
	}
}

func TestOpenCodeEnvelopeSecretFailureClearsOwned(test *testing.T) {
	provider := openCodeFullProvider("v1")
	raw, err := json.Marshal(openCodeEnvelope{Version: new(1), Variant: new("opencode-provider"), Provider: &provider, Secrets: new(map[string]jsontext.Value{"opencode.api-key": jsontext.Value(`"valid-private"`), "opencode.value.01": jsontext.Value(`"invalid\nprivate"`)})})
	defer clear(raw)
	if err != nil {
		test.Fatal("owned-secret fixture failed")
	}
	_, owned, _, err := decodeOpenCodeEnvelope(state.NewConfigurationPayload(raw))
	if err == nil || len(owned) != 0 {
		test.Fatal("decode failure retained owned credentials")
	}
}

func TestOpenCodeEnvelopeOptionalShapes(test *testing.T) {
	providers := []agentconfig.OpenCodeProvider{
		{ProviderID: "vendor", Format: "v1", V1: &agentconfig.OpenCodeV1{Options: &agentconfig.OpenCodeV1Options{Headers: new(map[string]string{})}, Models: new(map[string]agentconfig.OpenCodeV1Model{})}},
		{ProviderID: "vendor", Format: "v1", V1: &agentconfig.OpenCodeV1{Models: new(map[string]agentconfig.OpenCodeV1Model{"model": {Thinking: &agentconfig.OpenCodeValue{Bool: new(false)}, Options: new(map[string]agentconfig.OpenCodeValue{}), Cost: &agentconfig.OpenCodeValue{Object: new(map[string]agentconfig.OpenCodeValue{})}, Variants: new(map[string]agentconfig.OpenCodeValue{})}})}},
		{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{Env: new([]string{}), Settings: &agentconfig.OpenCodeSettings{Timeout: &agentconfig.OpenCodeTimeout{Number: new("0")}, ChunkTimeout: new("0")}, Headers: new(map[string]string{}), Body: new(map[string]agentconfig.OpenCodeValue{}), Models: new(map[string]agentconfig.OpenCodeV2Model{})}},
		{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{Models: new(map[string]agentconfig.OpenCodeV2Model{"model": {Disabled: new(false), Settings: &agentconfig.OpenCodeModelSettings{}, Compatibility: &agentconfig.OpenCodeCompatibility{}, Capabilities: &agentconfig.OpenCodeCapabilities{Tools: new(false), Input: new([]string{}), Output: new([]string{})}, Cost: &agentconfig.OpenCodeCosts{Single: &agentconfig.OpenCodeCost{Input: new("0"), Output: new("0")}}, Variants: new([]agentconfig.OpenCodeVariant{})}})}},
		{ProviderID: "vendor", Format: "v2", V2: &agentconfig.OpenCodeV2{Models: new(map[string]agentconfig.OpenCodeV2Model{"model": {Cost: &agentconfig.OpenCodeCosts{Tiers: new([]agentconfig.OpenCodeCost{})}}})}},
	}
	for _, provider := range providers {
		payload, err := encodeOpenCodeEnvelope(provider, nil)
		if err != nil {
			test.Fatal("valid optional-shape encode failed")
		}
		decoded, secrets, extras, err := decodeOpenCodeEnvelope(payload)
		defer secrets.clear()
		if err != nil || extras || !reflect.DeepEqual(decoded, provider) {
			test.Fatal("optional absent/empty/zero/false/cost union not preserved")
		}
	}
}
