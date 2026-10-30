package httpapi

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func openCodeConfigurationDraft(format string) *configurationv1.Draft {
	provider := &configurationv1.OpenCodeProvider{ProviderId: "synthetic-provider"}
	if format == "v1" {
		provider.Format = &configurationv1.OpenCodeProvider_V1{V1: &configurationv1.OpenCodeV1{Npm: new("@ai-sdk/openai-compatible"), Options: &configurationv1.OpenCodeV1Options{BaseUrl: new("https://synthetic.invalid/a%2Fb"), ApiKey: new("opencode.api-key"), Headers: &configurationv1.OpenCodeHeaders{Entries: map[string]string{"x-synthetic": "opencode.value.01"}}}, Models: &configurationv1.OpenCodeV1Models{}}}
	} else {
		provider.Format = &configurationv1.OpenCodeProvider_V2{V2: &configurationv1.OpenCodeV2{Package: new("@ai-sdk/openai-compatible"), Env: &configurationv1.OpenCodeStrings{}, Settings: &configurationv1.OpenCodeSettings{Timeout: &configurationv1.OpenCodeTimeout{Value: &configurationv1.OpenCodeTimeout_Disabled{Disabled: false}}}, Body: &configurationv1.OpenCodeObject{Entries: map[string]*configurationv1.OpenCodeValue{"apiKey": {Value: &configurationv1.OpenCodeValue_SecretReference{SecretReference: "opencode.api-key"}}}}, Models: &configurationv1.OpenCodeV2Models{}}}
	}
	return &configurationv1.Draft{Name: "OpenCode fixture", Payload: &configurationv1.Draft_OpencodeProvider{OpencodeProvider: provider}}
}

func openCodeConfigurationOptionalDrafts() map[string]*configurationv1.Draft {
	v1 := func(thinking, cost *configurationv1.OpenCodeValue) *configurationv1.Draft {
		return &configurationv1.Draft{Name: "Optional presence", Payload: &configurationv1.Draft_OpencodeProvider{OpencodeProvider: &configurationv1.OpenCodeProvider{ProviderId: "optional-provider", Format: &configurationv1.OpenCodeProvider_V1{V1: &configurationv1.OpenCodeV1{Models: &configurationv1.OpenCodeV1Models{Entries: map[string]*configurationv1.OpenCodeV1Model{"model": {Thinking: thinking, Cost: cost}}}}}}}}
	}
	v2 := func(compatibility *configurationv1.OpenCodeCompatibility) *configurationv1.Draft {
		return &configurationv1.Draft{Name: "Optional presence", Payload: &configurationv1.Draft_OpencodeProvider{OpencodeProvider: &configurationv1.OpenCodeProvider{ProviderId: "optional-provider", Format: &configurationv1.OpenCodeProvider_V2{V2: &configurationv1.OpenCodeV2{Models: &configurationv1.OpenCodeV2Models{Entries: map[string]*configurationv1.OpenCodeV2Model{"model": {Compatibility: compatibility}}}}}}}}
	}
	return map[string]*configurationv1.Draft{
		"v1-both-absent":     v1(nil, nil),
		"v1-thinking-absent": v1(nil, &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Number{Number: "0"}}),
		"v1-cost-absent":     v1(&configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Boolean{Boolean: false}}, nil),
		"v1-both-present":    v1(&configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Boolean{Boolean: false}}, &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Number{Number: "0"}}),
		"v2-compat-absent":   v2(nil),
		"v2-compat-empty":    v2(&configurationv1.OpenCodeCompatibility{}),
		"v2-compat-false":    v2(&configurationv1.OpenCodeCompatibility{RequireReasoning: new(false)}),
		"v2-compat-ref":      v2(&configurationv1.OpenCodeCompatibility{ReasoningField: &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_SecretReference{SecretReference: "opencode.value.01"}}}),
	}
}

func TestOpenCodeConfigurationOptionalPresenceSevenRPC(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			for name, draft := range openCodeConfigurationOptionalDrafts() {
				test.Run(name, func(test *testing.T) {
					input, secrets, err := configurationInput(draft, nil)
					configurationClearSecrets(secrets)
					if err != nil || input.OpenCodeProvider == nil || !proto.Equal(configurationOpenCodeProviderWire(input.OpenCodeProvider), draft.GetOpencodeProvider()) {
						test.Fatal("optional omission or present false/zero changed in mapper roundtrip")
					}
					accounts, store := testServiceAndStore(test)
					_, session := mcpSession(test, accounts, "opencode-optional-owner")
					endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
					defer endpoint.Close()
					var options []connect.ClientOption
					if encoding == "json" {
						options = append(options, connect.WithProtoJSON())
					}
					client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
					created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, session))
					if err != nil || !proto.Equal(created.Msg.View.Draft, draft) {
						test.Fatal("optional presence failed actual TLS/SQLite create")
					}
					asset := created.Msg.View.Asset
					listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
					if err != nil || len(listed.Msg.Assets) != 1 || listed.Msg.Assets[0].Id != asset.Id {
						test.Fatal("optional presence create did not persist one asset")
					}
					got, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: asset.Id}, session))
					if err != nil || got.Msg.Asset.Id != asset.Id {
						test.Fatal("optional presence get failed")
					}
					read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: asset.Id}, session))
					if err != nil || !proto.Equal(read.Msg.View.Draft, draft) {
						test.Fatal("optional omission or present false/zero changed after SQLite read")
					}
					updatedDraft := proto.Clone(draft).(*configurationv1.Draft)
					updatedDraft.Name = "Updated optional presence"
					updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: updatedDraft}, session))
					if err != nil || updated.Msg.View.Asset.Revision != 2 || !proto.Equal(updated.Msg.View.Draft, updatedDraft) {
						test.Fatal("optional presence CAS update failed")
					}
					if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: draft}, session)); connect.CodeOf(err) != connect.CodeAborted {
						test.Fatal("optional presence bypassed revision CAS")
					}
					copyDraft := proto.Clone(updatedDraft).(*configurationv1.Draft)
					copyDraft.Name = "Copied optional presence"
					copied, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: 2, Name: copyDraft.Name}, session))
					if err != nil || !proto.Equal(copied.Msg.View.Draft, copyDraft) {
						test.Fatal("optional omission or present false/zero changed after copy")
					}
					for _, status := range copied.Msg.View.Secrets {
						if status.Present {
							test.Fatal("optional presence copy invented credentials")
						}
					}
					for _, target := range []struct {
						id       string
						revision int64
					}{{asset.Id, 2}, {copied.Msg.View.Asset.Id, 1}} {
						if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: target.id, ExpectedRevision: target.revision}, session)); err != nil {
							test.Fatal("optional presence delete failed")
						}
					}
					listed, err = client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
					if err != nil || len(listed.Msg.Assets) != 0 {
						test.Fatal("optional presence deletion left assets")
					}
				})
			}
		})
	}
}

func TestOpenCodeConfigurationOptionalPresentEmptyRejected(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			for _, field := range []string{"thinking", "cost", "reasoning-field", "map-value", "array-value"} {
				test.Run(field, func(test *testing.T) {
					drafts := openCodeConfigurationOptionalDrafts()
					draft := drafts["v1-both-absent"]
					model := draft.GetOpencodeProvider().GetV1().Models.Entries["model"]
					switch field {
					case "thinking":
						model.Thinking = &configurationv1.OpenCodeValue{}
					case "cost":
						model.Cost = &configurationv1.OpenCodeValue{}
					case "reasoning-field":
						draft = drafts["v2-compat-empty"]
						draft.GetOpencodeProvider().GetV2().Models.Entries["model"].Compatibility.ReasoningField = &configurationv1.OpenCodeValue{}
					case "map-value":
						model.Options = &configurationv1.OpenCodeObject{Entries: map[string]*configurationv1.OpenCodeValue{"invalid": nil}}
					case "array-value":
						model.Thinking = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Array{Array: &configurationv1.OpenCodeArray{Values: []*configurationv1.OpenCodeValue{nil}}}}
					}
					input, secrets, err := configurationInput(draft, nil)
					configurationClearSecrets(secrets)
					if err == nil || input != (configuration.Draft{}) || secrets != nil {
						test.Fatal("present empty or required nil value yielded usable mapper output")
					}
					accounts, store := testServiceAndStore(test)
					_, session := mcpSession(test, accounts, "opencode-required-value-owner")
					endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
					defer endpoint.Close()
					var options []connect.ClientOption
					if encoding == "json" {
						options = append(options, connect.WithProtoJSON())
					}
					client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
					if _, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, session)); connect.CodeOf(err) != connect.CodeInvalidArgument {
						test.Fatal("actual HTTP accepted a present empty or required nil value")
					}
					listed, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
					if err != nil || len(listed.Msg.Assets) != 0 {
						test.Fatal("invalid value rejection wrote an asset")
					}
				})
			}
		})
	}
}

func TestOpenCodeConfigurationConcurrentCASAndClear(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			accounts, store := testServiceAndStore(test)
			owner, session := mcpSession(test, accounts, "opencode-cas")
			endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
			defer endpoint.Close()
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: openCodeConfigurationDraft("v1"), Secrets: []*configurationv1.SecretMutation{{SlotId: "opencode.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-initial-key"}}, {SlotId: "opencode.value.01", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-initial-header"}}}}, session))
			if err != nil {
				test.Fatal(err)
			}
			type result struct {
				index int
				err   error
			}
			results := make(chan result, 2)
			start := make(chan struct{})
			var workers sync.WaitGroup
			for index := range 2 {
				workers.Go(func() {
					<-start
					draft := openCodeConfigurationDraft("v1")
					draft.Name = "winner-" + strconv.Itoa(index)
					_, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: created.Msg.View.Asset.Id, ExpectedRevision: 1, Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "opencode.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-winning-key-" + strconv.Itoa(index)}}}}, session))
					results <- result{index: index, err: err}
				})
			}
			close(start)
			workers.Wait()
			winner, successes, conflicts := -1, 0, 0
			for range 2 {
				result := <-results
				if result.err == nil {
					winner = result.index
					successes++
				} else if connect.CodeOf(result.err) == connect.CodeAborted {
					conflicts++
				}
			}
			if successes != 1 || conflicts != 1 {
				test.Fatal("real typed CAS did not yield one winner/one conflict")
			}
			id := created.Msg.View.Asset.Id
			read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, session))
			if err != nil || read.Msg.View.Asset.Revision != 2 || read.Msg.View.Draft.Name != "winner-"+strconv.Itoa(winner) {
				test.Fatal("CAS stored a mixed name/revision")
			}
			_, payload, err := store.ReadConfigurationAssetVersion(test.Context(), owner, id)
			data := payload.Reveal()
			if err != nil || !bytes.Contains(data, []byte("synthetic-winning-key-"+strconv.Itoa(winner))) || bytes.Contains(data, []byte("synthetic-winning-key-"+strconv.Itoa(1-winner))) {
				test.Fatal("CAS mixed credential values")
			}
			clear(data)
			draft := openCodeConfigurationDraft("v1")
			draft.GetOpencodeProvider().GetV1().Options.Headers = nil
			if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 2, Draft: draft}, session)); connect.CodeOf(err) != connect.CodeInvalidArgument {
				test.Fatal("live reference removed without explicit CLEAR")
			}
			updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 2, Draft: draft, Secrets: []*configurationv1.SecretMutation{{SlotId: "opencode.value.01", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}}}}, session))
			if err != nil || updated.Msg.View.Asset.Revision != 3 || len(updated.Msg.View.Secrets) != 1 || !updated.Msg.View.Secrets[0].Present {
				test.Fatal("explicit CLEAR lost kept key or slot projection")
			}
			_, payload, err = store.ReadConfigurationAssetVersion(test.Context(), owner, id)
			data = payload.Reveal()
			defer clear(data)
			if err != nil || bytes.Contains(data, []byte("synthetic-initial-header")) {
				test.Fatal("removed reference's credential persisted")
			}
		})
	}
}

func TestOpenCodeConfigurationStoredFutureReadOnly(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			accounts, store := testServiceAndStore(test)
			owner, session := mcpSession(test, accounts, "opencode-future")
			endpoint := httptest.NewServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
			defer endpoint.Close()
			var options []connect.ClientOption
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
			created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: openCodeConfigurationDraft("v2")}, session))
			if err != nil {
				test.Fatal(err)
			}
			metadata, payload, err := store.ReadConfigurationAssetVersion(test.Context(), owner, created.Msg.View.Asset.Id)
			if err != nil {
				test.Fatal(err)
			}
			known := payload.Reveal()
			defer clear(known)
			future := append([]byte(`{"future":{"private":"unknown-opencode-sentinel"},`), known[1:]...)
			defer clear(future)
			changed, err := store.UpdateConfigurationAsset(test.Context(), owner, metadata.ID, 1, state.ConfigurationAssetDraft{AgentID: metadata.AgentID, Kind: metadata.Kind, Name: metadata.Name, SchemaVersion: metadata.SchemaVersion, Payload: state.NewConfigurationPayload(future)})
			if err != nil {
				test.Fatal(err)
			}
			read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: metadata.ID}, session))
			if err != nil || !read.Msg.View.HasPreservedExtras || !proto.Equal(read.Msg.View.Draft, created.Msg.View.Draft) {
				test.Fatal("future stored field destroyed known read projection")
			}
			encoded, err := protojson.Marshal(read.Msg)
			if err != nil || bytes.Contains(encoded, []byte("unknown-opencode-sentinel")) {
				test.Fatal("unknown private stored field escaped read")
			}
			if _, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: metadata.ID, ExpectedRevision: changed.Revision, Draft: read.Msg.View.Draft}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("future field was overwritten by typed update")
			}
			if _, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: metadata.ID, ExpectedSourceRevision: changed.Revision, Name: "future copy"}, session)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				test.Fatal("future private tree copied")
			}
			metadata, payload, err = store.ReadConfigurationAssetVersion(test.Context(), owner, metadata.ID)
			unchanged := payload.Reveal()
			defer clear(unchanged)
			if err != nil || metadata.Revision != changed.Revision || !bytes.Equal(unchanged, future) {
				test.Fatal("read-only rejection changed stored bytes/revision")
			}
			if _, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: metadata.ID}, session)); err != nil {
				test.Fatal(err)
			}
			if _, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session)); err != nil {
				test.Fatal(err)
			}
			if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: metadata.ID, ExpectedRevision: metadata.Revision}, session)); err != nil {
				test.Fatal("future metadata could not be deleted")
			}
		})
	}
}

func openCodeConfigurationFullDraft(format string) *configurationv1.Draft {
	draft := openCodeConfigurationDraft(format)
	number := func(value string) *configurationv1.OpenCodeValue {
		return &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Number{Number: value}}
	}
	ref := func() *configurationv1.OpenCodeValue {
		return &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_SecretReference{SecretReference: "opencode.value.01"}}
	}
	object := func() *configurationv1.OpenCodeObject {
		return &configurationv1.OpenCodeObject{Entries: map[string]*configurationv1.OpenCodeValue{
			"nullable": {Value: &configurationv1.OpenCodeValue_Null{Null: true}}, "boolean": {Value: &configurationv1.OpenCodeValue_Boolean{Boolean: false}}, "number": number("0"), "secret": ref(), "emptyArray": {Value: &configurationv1.OpenCodeValue_Array{Array: &configurationv1.OpenCodeArray{}}}, "emptyObject": {Value: &configurationv1.OpenCodeValue_Object{Object: &configurationv1.OpenCodeObject{}}},
		}}
	}
	limit := func() *configurationv1.OpenCodeLimit {
		return &configurationv1.OpenCodeLimit{Context: new("200000"), Output: new("0"), Extensions: object()}
	}
	compaction := func() *configurationv1.OpenCodeCompaction {
		return &configurationv1.OpenCodeCompaction{Type: "summary", Extensions: object()}
	}
	modelSettings := func() *configurationv1.OpenCodeModelSettings {
		return &configurationv1.OpenCodeModelSettings{Compaction: compaction(), Extensions: object()}
	}
	if format == "v1" {
		provider := draft.GetOpencodeProvider().GetV1()
		provider.Name = new("Synthetic provider")
		provider.Extensions = object()
		provider.Options.Extensions = object()
		provider.Models.Entries = map[string]*configurationv1.OpenCodeV1Model{"synthetic-model": {Name: new("Synthetic model"), Limit: limit(), Options: object(), Modalities: &configurationv1.OpenCodeModalities{Input: &configurationv1.OpenCodeStrings{Values: []string{"text", "image"}}, Output: &configurationv1.OpenCodeStrings{Values: []string{"text"}}, Extensions: object()}, Thinking: ref(), Cost: number("0.5"), Variants: object(), Extensions: object()}}
		provider.Models.Entries["synthetic-model"].Variants = &configurationv1.OpenCodeObject{Entries: map[string]*configurationv1.OpenCodeValue{"synthetic-variant": {Value: &configurationv1.OpenCodeValue_Object{Object: object()}}}}
	} else {
		provider := draft.GetOpencodeProvider().GetV2()
		provider.Name, provider.Canonical = new("Synthetic provider"), new("synthetic")
		provider.Env = &configurationv1.OpenCodeStrings{Values: []string{"SYNTHETIC_API_KEY"}}
		provider.Settings = &configurationv1.OpenCodeSettings{Timeout: &configurationv1.OpenCodeTimeout{Value: &configurationv1.OpenCodeTimeout_Number{Number: "0"}}, ChunkTimeout: new("0"), Compaction: compaction(), Transport: new("http"), Extensions: object()}
		provider.Headers = &configurationv1.OpenCodeHeaders{Entries: map[string]string{"x-synthetic": "opencode.value.01"}}
		provider.Body, provider.Extensions = object(), object()
		limits := limit()
		limits.Input = new("1000")
		provider.Models.Entries = map[string]*configurationv1.OpenCodeV2Model{"synthetic-model": {ModelId: new("model-id"), Family: new("synthetic-family"), Name: new("Synthetic model"), Package: new("@ai-sdk/openai-compatible"), Disabled: new(false), Settings: modelSettings(), Headers: &configurationv1.OpenCodeHeaders{}, Body: &configurationv1.OpenCodeObject{}, Compatibility: &configurationv1.OpenCodeCompatibility{ReasoningField: ref(), RequireReasoning: new(false), MaxTokensField: new("max_tokens"), RequireFinishReason: new(false), RequireAssistantAfterTool: new(false), SupportsPromptCacheKey: new(false), Extensions: object()}, Capabilities: &configurationv1.OpenCodeCapabilities{Tools: new(false), Input: &configurationv1.OpenCodeStrings{Values: []string{"text"}}, Output: &configurationv1.OpenCodeStrings{}, Extensions: object()}, Limit: limits, Cost: &configurationv1.OpenCodeCosts{Shape: &configurationv1.OpenCodeCosts_Single{Single: &configurationv1.OpenCodeCost{Input: new("0"), Output: new("0.5"), Cache: &configurationv1.OpenCodeCacheCost{Read: new("0"), Write: new("0"), Extensions: object()}, Tier: &configurationv1.OpenCodeCostTier{Type: "context", Size: new("0"), Extensions: object()}, Extensions: object()}}}, Variants: &configurationv1.OpenCodeVariants{Values: []*configurationv1.OpenCodeVariant{{Id: "synthetic-variant", Settings: modelSettings(), Headers: &configurationv1.OpenCodeHeaders{}, Body: object(), Extensions: object()}}}, Extensions: object()}}
	}
	return draft
}

func TestOpenCodeConfigurationFullFieldRoundtrip(test *testing.T) {
	for _, format := range []string{"v1", "v2"} {
		test.Run(format, func(test *testing.T) {
			draft := openCodeConfigurationFullDraft(format)
			input, owned, err := configurationInput(draft, nil)
			configurationClearSecrets(owned)
			if err != nil {
				test.Fatalf("full typed native fields rejected: %v", err)
			}
			wire := configurationOpenCodeProviderWire(input.OpenCodeProvider)
			if !proto.Equal(wire, draft.GetOpencodeProvider()) {
				test.Fatal("full typed field/optional/empty collection/false/zero roundtrip lost information")
			}
			for _, encode := range []func(proto.Message) ([]byte, error){proto.Marshal, protojson.Marshal} {
				data, err := encode(wire)
				if err != nil {
					test.Fatal(err)
				}
				valid := configurationOpenCodeWireValid(data, wire.ProtoReflect().Descriptor())
				if len(data) > 0 && data[0] == '{' {
					valid = configurationOpenCodeJSONValid(data, wire.ProtoReflect().Descriptor())
				}
				if !valid {
					test.Fatal("full native fields rejected by strict subtree")
				}
			}
		})
	}
}

func TestOpenCodeConfigurationTLSFullFields(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			for _, format := range []string{"v1", "v2"} {
				test.Run(format, func(test *testing.T) {
					accounts, store := testServiceAndStore(test)
					_, session := mcpSession(test, accounts, "opencode-tls-fields")
					endpoint := httptest.NewTLSServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
					defer endpoint.Close()
					var options []connect.ClientOption
					if encoding == "json" {
						options = append(options, connect.WithProtoJSON())
					}
					client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
					for _, draft := range []*configurationv1.Draft{openCodeConfigurationFullDraft(format), openCodeConfigurationDraft(format)} {
						if format == "v2" {
							value := &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Boolean{Boolean: false}}
							for range 10 {
								value = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Array{Array: &configurationv1.OpenCodeArray{Values: []*configurationv1.OpenCodeValue{value}}}}
							}
							draft.GetOpencodeProvider().GetV2().Body.Entries["depth12"] = value
						}
						created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, session))
						if err != nil || !proto.Equal(created.Msg.View.Draft, draft) {
							test.Fatalf("TLS full-field/empty/deep Create roundtrip failed: %s", connect.CodeOf(err))
						}
						read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: created.Msg.View.Asset.Id}, session))
						if err != nil || !proto.Equal(read.Msg.View.Draft, draft) {
							test.Fatal("TLS persisted fields differ on read")
						}
					}
				})
			}
		})
	}
}

func TestOpenCodeConfigurationWrapperDepth(test *testing.T) {
	for _, depth := range []int{2, 8, 12, 13} {
		test.Run(strconv.Itoa(depth), func(test *testing.T) {
			draft := openCodeConfigurationDraft("v2")
			value := &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Boolean{Boolean: false}}
			for range depth - 2 {
				value = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Array{Array: &configurationv1.OpenCodeArray{Values: []*configurationv1.OpenCodeValue{value}}}}
			}
			draft.GetOpencodeProvider().GetV2().Body = &configurationv1.OpenCodeObject{Entries: map[string]*configurationv1.OpenCodeValue{"nested": value}}
			input, owned, err := configurationInput(draft, nil)
			configurationClearSecrets(owned)
			if depth > 12 {
				if err == nil || input.OpenCodeProvider != nil {
					test.Fatal("native value depth budget relaxed")
				}
				return
			}
			if err != nil || input.OpenCodeProvider == nil {
				test.Fatal("legal native depth rejected as wrapper depth")
			}
			data, err := proto.Marshal(draft.GetOpencodeProvider())
			if err != nil || !configurationOpenCodeWireValid(data, draft.GetOpencodeProvider().ProtoReflect().Descriptor()) {
				test.Fatal("legal depth rejected by wire wrappers")
			}
			data, err = protojson.Marshal(draft.GetOpencodeProvider())
			if err != nil || !configurationOpenCodeJSONValid(data, draft.GetOpencodeProvider().ProtoReflect().Descriptor()) {
				test.Fatal("legal depth rejected by JSON wrappers")
			}
		})
	}
}

func TestOpenCodeConfigurationSevenRPC(test *testing.T) {
	for _, encoding := range []string{"json", "proto"} {
		test.Run(encoding, func(test *testing.T) {
			for _, format := range []string{"v1", "v2"} {
				test.Run(format, func(test *testing.T) {
					accounts, store := testServiceAndStore(test)
					_, session := mcpSession(test, accounts, "opencode-http-owner")
					_, foreign := mcpSession(test, accounts, "opencode-http-foreign")
					endpoint := httptest.NewServer(NewWithOptions(accounts, Options{Configuration: configuration.New(store)}))
					test.Cleanup(endpoint.Close)
					var options []connect.ClientOption
					if encoding == "json" {
						options = append(options, connect.WithProtoJSON())
					}
					client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
					draft := openCodeConfigurationDraft(format)
					mutations := []*configurationv1.SecretMutation{{SlotId: "opencode.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-opencode-key-sentinel"}}}
					if format == "v1" {
						mutations = append(mutations, &configurationv1.SecretMutation{SlotId: "opencode.value.01", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-opencode-header-sentinel"}})
					}
					created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft, Secrets: mutations}, session))
					if err != nil || created.Msg.View.Asset.AgentId != "opencode" || created.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE {
						test.Fatalf("valid authenticated OpenCode create rejected: %s", connect.CodeOf(err))
					}
					asset := created.Msg.View.Asset
					for _, status := range created.Msg.View.Secrets {
						if !status.Present {
							test.Fatal("replacement did not persist presence")
						}
					}
					assertPrivate := func(message proto.Message) {
						test.Helper()
						data, err := protojson.Marshal(message)
						if err != nil || bytes.Contains(data, []byte("sentinel")) {
							test.Fatal("response exposed secret replacement")
						}
					}
					assertPrivate(created.Msg)
					if _, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: draft}, "")); connect.CodeOf(err) != connect.CodeUnauthenticated {
						test.Fatal("anonymous create accepted")
					}
					for _, token := range []string{session, foreign} {
						list, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, token))
						expected := 0
						if token == session {
							expected = 1
						}
						if err != nil || len(list.Msg.Assets) != expected {
							test.Fatal("owner-isolated list failed")
						}
						assertPrivate(list.Msg)
					}
					operations := []struct {
						name string
						call func(string) error
					}{
						{"get", func(token string) error {
							_, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: asset.Id}, token))
							return err
						}},
						{"read", func(token string) error {
							_, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: asset.Id}, token))
							return err
						}},
						{"update", func(token string) error {
							_, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: 1, Draft: draft}, token))
							return err
						}},
						{"copy", func(token string) error {
							_, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: 1, Name: "foreign copy"}, token))
							return err
						}},
						{"delete", func(token string) error {
							_, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: asset.Id, ExpectedRevision: 1}, token))
							return err
						}},
					}
					for _, operation := range operations {
						if connect.CodeOf(operation.call("")) != connect.CodeUnauthenticated || connect.CodeOf(operation.call(foreign)) != connect.CodeNotFound {
							test.Fatalf("%s failed owner/session isolation", operation.name)
						}
					}
					read, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: asset.Id}, session))
					if err != nil || !proto.Equal(read.Msg.View.Draft, draft) {
						test.Fatal("typed read lost native wrapper presence")
					}
					assertPrivate(read.Msg)
					get, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: asset.Id}, session))
					if err != nil || get.Msg.Asset.Id != asset.Id {
						test.Fatal("owner get failed")
					}
					copied, err := client.CopyAsset(test.Context(), configurationRequest(&configurationv1.CopyAssetRequest{SourceId: asset.Id, ExpectedSourceRevision: 1, Name: "No credentials"}, session))
					if err != nil || copied.Msg.View.CredentialReadiness != configurationv1.CredentialReadiness_CREDENTIAL_READINESS_NEEDS_CREDENTIAL {
						test.Fatal("copy inherited credential readiness")
					}
					for _, status := range copied.Msg.View.Secrets {
						if status.Present {
							test.Fatal("copy inherited key material")
						}
					}
					assertPrivate(copied.Msg)
					for index, action := range []string{"keep", "replace", "clear"} {
						var mutation *configurationv1.SecretMutation
						switch action {
						case "keep":
							mutation = &configurationv1.SecretMutation{SlotId: "opencode.api-key", Action: &configurationv1.SecretMutation_Keep{Keep: &configurationv1.KeepSecret{}}}
						case "replace":
							mutation = &configurationv1.SecretMutation{SlotId: "opencode.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-opencode-replaced-sentinel"}}
						case "clear":
							mutation = &configurationv1.SecretMutation{SlotId: "opencode.api-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}}
						}
						updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: asset.Id, ExpectedRevision: int64(index + 1), Draft: draft, Secrets: []*configurationv1.SecretMutation{mutation}}, session))
						if err != nil || updated.Msg.View.Asset.Revision != int64(index+2) || updated.Msg.View.Secrets[0].Present != (index < 2) {
							test.Fatal("CAS secret action failed")
						}
						assertPrivate(updated.Msg)
					}
					for _, operation := range operations[2:] {
						if connect.CodeOf(operation.call(session)) != connect.CodeAborted {
							test.Fatalf("stale %s accepted", operation.name)
						}
					}
					if _, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: asset.Id, ExpectedRevision: 4}, session)); err != nil {
						test.Fatal("owner delete failed")
					}
					if _, err := client.GetAsset(test.Context(), configurationRequest(&configurationv1.GetAssetRequest{Id: asset.Id}, session)); connect.CodeOf(err) != connect.CodeNotFound || err != nil && strings.Contains(err.Error(), "sentinel") {
						test.Fatal("delete retained metadata or leaked secret")
					}
					if err := accounts.Logout(test.Context(), session); err != nil {
						test.Fatal(err)
					}
					if _, err := client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session)); connect.CodeOf(err) != connect.CodeUnauthenticated {
						test.Fatal("revoked session accepted")
					}
				})
			}
		})
	}
}

func TestOpenCodeConfigurationMappingSeam(test *testing.T) {
	for _, format := range []string{"v1", "v2"} {
		test.Run(format, func(test *testing.T) {
			draft := openCodeConfigurationDraft(format)
			input, owned, err := configurationOpenCodeInput(draft, nil)
			defer configurationClearSecrets(owned)
			if err != nil || input.OpenCodeProvider == nil || input.OpenCodeProvider.Format != format {
				test.Fatal("valid OpenCode typed input rejected")
			}
			statuses := []configuration.SecretStatus{{Slot: "opencode.api-key"}}
			if format == "v1" {
				statuses = append(statuses, configuration.SecretStatus{Slot: "opencode.value.01"})
			}
			entry := configuration.EditView{Metadata: state.ConfigurationAsset{ID: "synthetic-id", AgentID: "opencode", Kind: state.ConfigurationKindProvider, SchemaVersion: 1, Name: draft.Name, Revision: 1}, OpenCodeProvider: input.OpenCodeProvider, Readiness: configuration.NeedsCredential, Secrets: statuses}
			view, err := configurationOpenCodeView(entry)
			if err != nil || view == nil || view.Draft.GetOpencodeProvider() == nil {
				test.Fatal("valid OpenCode view rejected")
			}
			roundtrip, secrets, err := configurationOpenCodeInput(view.Draft, nil)
			configurationClearSecrets(secrets)
			if err != nil || !reflect.DeepEqual(roundtrip.OpenCodeProvider, input.OpenCodeProvider) {
				test.Fatal("typed wrapper presence changed on roundtrip")
			}
			draft.GetOpencodeProvider().ProviderId = "changed"
			view.Draft.GetOpencodeProvider().ProviderId = "view-changed"
			if input.OpenCodeProvider.ProviderID != "synthetic-provider" {
				test.Fatal("mapper retained caller aliases")
			}
			if format == "v1" {
				*draft.GetOpencodeProvider().GetV1().Options.BaseUrl = "https://changed.invalid"
				*view.Draft.GetOpencodeProvider().GetV1().Options.BaseUrl = "https://changed-view.invalid"
				draft.GetOpencodeProvider().GetV1().Options.Headers.Entries["x-synthetic"] = "opencode.value.02"
				view.Draft.GetOpencodeProvider().GetV1().Options.Headers.Entries["x-synthetic"] = "opencode.value.03"
				if *input.OpenCodeProvider.V1.Options.BaseURL != "https://synthetic.invalid/a%2Fb" || (*input.OpenCodeProvider.V1.Options.Headers)["x-synthetic"] != "opencode.value.01" {
					test.Fatal("optional pointer/map alias crossed mapper")
				}
			} else {
				view.Draft.GetOpencodeProvider().GetV2().Body.Entries["apiKey"].Value = &configurationv1.OpenCodeValue_Number{Number: "0"}
				draft.GetOpencodeProvider().GetV2().Settings.Timeout.Value = &configurationv1.OpenCodeTimeout_Number{Number: "10"}
				if input.OpenCodeProvider.V2.Settings.Timeout.Disabled == nil || (*input.OpenCodeProvider.V2.Body)["apiKey"].Reference == nil {
					test.Fatal("recursive oneof alias crossed mapper")
				}
			}
			entry.Provider.AuthMode = configuration.NativeLogin
			if view, err := configurationOpenCodeView(entry); !errors.Is(err, configuration.ErrUnavailable) || view != nil {
				test.Fatal("mixed view was projected")
			}
		})
	}
}

func TestOpenCodeConfigurationMappingRejectsMalformed(test *testing.T) {
	for name, change := range map[string]func(*configurationv1.OpenCodeV2){
		"nil-map-value": func(provider *configurationv1.OpenCodeV2) {
			provider.Models.Entries = map[string]*configurationv1.OpenCodeV2Model{"model": nil}
		},
		"model-budget": func(provider *configurationv1.OpenCodeV2) {
			provider.Models.Entries = make(map[string]*configurationv1.OpenCodeV2Model)
			for index := range 65 {
				provider.Models.Entries["model-"+strconv.Itoa(index)] = &configurationv1.OpenCodeV2Model{}
			}
		},
		"variant-budget": func(provider *configurationv1.OpenCodeV2) {
			provider.Models.Entries = map[string]*configurationv1.OpenCodeV2Model{"model": {Variants: &configurationv1.OpenCodeVariants{Values: make([]*configurationv1.OpenCodeVariant, 129)}}}
		},
		"header-budget": func(provider *configurationv1.OpenCodeV2) {
			provider.Headers = &configurationv1.OpenCodeHeaders{Entries: make(map[string]string)}
			for index := range 65 {
				provider.Headers.Entries["x-"+strconv.Itoa(index)] = "opencode.api-key"
			}
		},
		"empty-value": func(provider *configurationv1.OpenCodeV2) {
			provider.Body.Entries["invalid"] = &configurationv1.OpenCodeValue{}
		},
		"nil-value": func(provider *configurationv1.OpenCodeV2) { provider.Body.Entries["invalid"] = nil },
		"typed-nil-value": func(provider *configurationv1.OpenCodeV2) {
			provider.Body.Entries["invalid"] = &configurationv1.OpenCodeValue{Value: (*configurationv1.OpenCodeValue_Number)(nil)}
		},
		"null-false": func(provider *configurationv1.OpenCodeV2) {
			provider.Body.Entries["invalid"] = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Null{Null: false}}
		},
		"literal": func(provider *configurationv1.OpenCodeV2) {
			provider.Body.Entries["invalid"] = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Literal{Literal: "private-sentinel"}}
		},
		"foreign-slot": func(provider *configurationv1.OpenCodeV2) {
			provider.Body.Entries["invalid"] = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_SecretReference{SecretReference: "claude.api-key"}}
		},
		"noncanonical-number": func(provider *configurationv1.OpenCodeV2) {
			provider.Body.Entries["invalid"] = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Number{Number: "1e0"}}
		},
		"unsafe-integer": func(provider *configurationv1.OpenCodeV2) {
			provider.Body.Entries["invalid"] = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Number{Number: "9007199254740992"}}
		},
		"empty-timeout": func(provider *configurationv1.OpenCodeV2) {
			provider.Settings.Timeout = &configurationv1.OpenCodeTimeout{}
		},
		"wrong-cost-shape": func(provider *configurationv1.OpenCodeV2) {
			provider.Models.Entries = map[string]*configurationv1.OpenCodeV2Model{"model": {Cost: &configurationv1.OpenCodeCosts{}}}
		},
		"node-budget": func(provider *configurationv1.OpenCodeV2) {
			values := make([]*configurationv1.OpenCodeValue, 2049)
			for index := range values {
				values[index] = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Boolean{Boolean: false}}
			}
			provider.Body.Entries["invalid"] = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Array{Array: &configurationv1.OpenCodeArray{Values: values}}}
		},
		"byte-budget": func(provider *configurationv1.OpenCodeV2) {
			provider.Body.Entries = make(map[string]*configurationv1.OpenCodeValue)
			for index := range 512 {
				provider.Body.Entries[strings.Repeat("x", 250)+strconv.Itoa(index)] = &configurationv1.OpenCodeValue{Value: &configurationv1.OpenCodeValue_Number{Number: "0"}}
			}
		},
	} {
		test.Run(name, func(test *testing.T) {
			draft := openCodeConfigurationDraft("v2")
			change(draft.GetOpencodeProvider().GetV2())
			input, owned, err := configurationInput(draft, []*configurationv1.SecretMutation{{SlotId: "opencode.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-key-sentinel"}}})
			defer configurationClearSecrets(owned)
			if err == nil || input != (configuration.Draft{}) || owned != nil || strings.Contains(err.Error(), "sentinel") {
				test.Fatal("malformed native tree yielded usable output or secret")
			}
		})
	}
	for _, costs := range []*configurationv1.OpenCodeCosts{
		{Shape: &configurationv1.OpenCodeCosts_Tiers{Tiers: &configurationv1.OpenCodeCostList{}}},
		{Shape: &configurationv1.OpenCodeCosts_Tiers{Tiers: &configurationv1.OpenCodeCostList{Values: []*configurationv1.OpenCodeCost{{Input: new("0"), Output: new("0")}}}}},
	} {
		draft := openCodeConfigurationDraft("v2")
		draft.GetOpencodeProvider().GetV2().Models.Entries = map[string]*configurationv1.OpenCodeV2Model{"model": {Cost: costs, Variants: &configurationv1.OpenCodeVariants{}}}
		input, owned, err := configurationInput(draft, nil)
		configurationClearSecrets(owned)
		if err != nil || input.OpenCodeProvider == nil || !proto.Equal(configurationOpenCodeProviderWire(input.OpenCodeProvider), draft.GetOpencodeProvider()) {
			test.Fatal("present empty/list costs or variants lost")
		}
	}
	provider := openCodeConfigurationDraft("v2")
	input, _, err := configurationInput(provider, nil)
	if err != nil || agentconfig.ValidateOpenCodeProvider(*input.OpenCodeProvider) != nil {
		test.Fatal("valid control rejected alongside malformed trees")
	}
}
