package configclient_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"

	configurationnativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/configclient"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"
)

func fixtureKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("cognate native proof synthetic known-answer v1"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func beginFixture() (*configurationnativev1.BeginNativeConsumerRequest, *configurationnativev1.BeginNativeConsumerResponse) {
	return &configurationnativev1.BeginNativeConsumerRequest{
		Purpose:     configurationnativev1.NativePurpose_NATIVE_PURPOSE_CONFIGURATION_APPLY,
		OperationId: bytes.Repeat([]byte{1}, 16), ConsumerPublicKey: fixtureKey().Public().(ed25519.PublicKey), ClientNonce: bytes.Repeat([]byte{3}, 32),
		ExpectedOwnerId: "owner-synthetic", ExpectedServerOrigin: "https://proof.example:8443",
	}, &configurationnativev1.BeginNativeConsumerResponse{
		ChallengeId: "challenge-synthetic", Challenge: bytes.Repeat([]byte{4}, 32), NativeSessionBinding: bytes.Repeat([]byte{5}, 32),
		OwnerId: "owner-synthetic", ServerOrigin: "https://proof.example:8443", ChallengeExpiresAtUnixMs: 1700000060000, NativeSessionExpiresAtUnixMs: 1700000900000,
	}
}

type proofFixture struct {
	context     configclient.ConsumerBinding
	preparation *configurationnativev1.PreparationBinding
	apply       *configurationnativev1.ApplyBinding
	patch       *configurationnativev1.ClaudeProviderPatch
	prepare     *configurationnativev1.PrepareNativeApplyRequest
	authorize   *configurationnativev1.AuthorizeNativeMaterializationRequest
	consume     *configurationnativev1.ConsumeNativeMaterializationRequest
	status      *configurationnativev1.GetNativeMaterializationStatusRequest
	challenge   []byte
}

func nativeFixture(test testing.TB) proofFixture {
	test.Helper()
	adapter := &configurationnativev1.AdapterBinding{ApiVersion: 1, PluginId: "builtin.claude", PluginVersion: "1.1.0", DefinitionDigest: strings.Repeat("a", 64), RequiredCapabilities: []string{"claude.provider-switch.v1"}}
	patch := &configurationnativev1.ClaudeProviderPatch{}
	for index := range 7 {
		patch.Operations = append(patch.Operations, &configurationnativev1.PatchOperation{Slot: configurationnativev1.EnvSlot(index + 1), Action: &configurationnativev1.PatchOperation_Remove{Remove: &configurationnativev1.RemoveEnv{}}})
	}
	patch.Operations[0].Action = &configurationnativev1.PatchOperation_SetSelectedSecret{SetSelectedSecret: &configurationnativev1.SetSelectedSecret{Slot: configurationnativev1.SecretSlot_SECRET_SLOT_CLAUDE_API_KEY}}
	for index, value := range map[int]string{2: "https://provider.invalid/v1", 3: "model-default", 5: "model-sonnet"} {
		patch.Operations[index].Action = &configurationnativev1.PatchOperation_SetLiteral{SetLiteral: &configurationnativev1.SetLiteral{Value: value}}
	}
	patchHash, err := configclient.HashPatch(configurationnativev1.Target_TARGET_CLAUDE_SETTINGS, patch)
	if err != nil {
		test.Fatal("valid fixture patch rejected")
	}
	keyHash := sha256.Sum256(fixtureKey().Public().(ed25519.PublicKey))
	preparation := &configurationnativev1.PreparationBinding{
		OwnerId: "owner-synthetic", ServerOrigin: "https://proof.example:8443", NativeSessionBinding: bytes.Repeat([]byte{5}, 32), OperationId: bytes.Repeat([]byte{1}, 16),
		AssetId: strings.Repeat("A", 26), AssetRevision: 7, AgentId: "claude", AssetKind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, SchemaVersion: 1,
		Adapter: adapter, Target: configurationnativev1.Target_TARGET_CLAUDE_SETTINGS, Scope: configurationnativev1.Scope_SCOPE_USER, ProfileId: "synthetic-profile", SelectionRevision: 9,
		RequiredSlots: []configurationnativev1.SecretSlot{configurationnativev1.SecretSlot_SECRET_SLOT_CLAUDE_API_KEY}, PrepareExpiresAtUnixMs: 1700000300000,
		ConsumerKeySha256: keyHash[:], PrepareId: "prepare-synthetic", PreparedAtUnixMs: 1700000000000, PatchSha256: patchHash[:],
	}
	apply := &configurationnativev1.ApplyBinding{Preparation: preparation, IntentCommitment: bytes.Repeat([]byte{7}, 32), IntentExpiresAtUnixMs: 1700000240000}
	return proofFixture{
		context:     configclient.ConsumerBinding{ServerOrigin: preparation.ServerOrigin, OwnerID: preparation.OwnerId, NativeSessionBinding: bytes.Clone(preparation.NativeSessionBinding), OperationID: bytes.Clone(preparation.OperationId), ConsumerKeySHA256: keyHash[:]},
		preparation: preparation, apply: apply, patch: patch, challenge: bytes.Repeat([]byte{11}, 32),
		prepare:   &configurationnativev1.PrepareNativeApplyRequest{ExpectedOwnerId: preparation.OwnerId, AssetId: preparation.AssetId, ExpectedAssetRevision: 7, AgentId: "claude", SchemaVersion: 1, Adapter: proto.Clone(adapter).(*configurationnativev1.AdapterBinding), Target: preparation.Target, Scope: preparation.Scope, ProfileId: preparation.ProfileId, SelectionRevision: 9, OperationId: bytes.Clone(preparation.OperationId), ClientNonce: bytes.Repeat([]byte{6}, 32)},
		authorize: &configurationnativev1.AuthorizeNativeMaterializationRequest{PrepareId: preparation.PrepareId, OperationId: bytes.Clone(preparation.OperationId), IntentCommitment: bytes.Clone(apply.IntentCommitment), RequestedSlots: []configurationnativev1.SecretSlot{configurationnativev1.SecretSlot_SECRET_SLOT_CLAUDE_API_KEY}, Authorization: &configurationnativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &configurationnativev1.ExplicitConfirmation{ConfirmationRevision: 11}}, ClientNonce: bytes.Repeat([]byte{6}, 32), IntentExpiresAtUnixMs: apply.IntentExpiresAtUnixMs},
		consume:   &configurationnativev1.ConsumeNativeMaterializationRequest{PrepareId: preparation.PrepareId, GrantId: "grant-synthetic", GrantToken: bytes.Repeat([]byte{9}, 32), OperationId: bytes.Clone(preparation.OperationId), IntentCommitment: bytes.Clone(apply.IntentCommitment), ClientNonce: bytes.Repeat([]byte{12}, 32)},
		status:    &configurationnativev1.GetNativeMaterializationStatusRequest{PrepareId: preparation.PrepareId, OperationId: bytes.Clone(preparation.OperationId), ClientNonce: bytes.Repeat([]byte{13}, 32)},
	}
}

func transcripts(test *testing.T, fixture proofFixture) map[string]configclient.Transcript {
	test.Helper()
	result := make(map[string]configclient.Transcript)
	add := func(name string, transcript configclient.Transcript, err error) {
		if err != nil {
			test.Fatalf("valid %s transcript rejected", name)
		}
		result[name] = transcript
	}
	begin, challenge := beginFixture()
	value, err := configclient.BindTranscript(begin, challenge)
	add("bind", value, err)
	value, err = configclient.PrepareTranscript(fixture.context, fixture.prepare)
	add("prepare", value, err)
	value, err = configclient.AuthorizeTranscript(fixture.preparation, fixture.challenge, fixture.authorize)
	add("authorize", value, err)
	auto := proto.Clone(fixture.authorize).(*configurationnativev1.AuthorizeNativeMaterializationRequest)
	auto.Authorization = &configurationnativev1.AuthorizeNativeMaterializationRequest_AutoPolicy{AutoPolicy: &configurationnativev1.AutoPolicy{PolicyId: bytes.Repeat([]byte{'P'}, 32), PolicyRevision: 13}}
	value, err = configclient.AuthorizeTranscript(fixture.preparation, fixture.challenge, auto)
	add("authorize-auto", value, err)
	value, err = configclient.ConsumeTranscript(fixture.apply, fixture.challenge, bytes.Repeat([]byte{10}, 32), 1700000060000, fixture.consume)
	add("consume", value, err)
	value, err = configclient.StatusTranscript(fixture.preparation, fixture.challenge, fixture.status)
	add("status", value, err)
	return result
}

func TestNativeKnownAnswerVectorsAndPurposeIsolation(test *testing.T) {
	var answer struct {
		Format  string            `json:"format"`
		Hashes  map[string]string `json:"hashes"`
		Vectors map[string]struct {
			Transcript string `json:"transcript"`
			Signature  string `json:"signature"`
		} `json:"vectors"`
	}
	data, err := os.ReadFile("testdata/native-proof-v1.json")
	if err != nil || json.Unmarshal(data, &answer, json.RejectUnknownMembers(true)) != nil || answer.Format != "cognate-native-proof-known-answer-v1" || len(answer.Vectors) != 6 {
		test.Fatal("invalid known-answer fixture")
	}
	fixture := nativeFixture(test)
	values := transcripts(test, fixture)
	for name, value := range values {
		test.Run(name, func(test *testing.T) {
			vector := answer.Vectors[name]
			want, err := hex.DecodeString(vector.Transcript)
			if err != nil || !bytes.Equal(value.CopyBytes(), want) {
				test.Fatal("canonical transcript differs from independent vector")
			}
			signature, err := configclient.SignNativeProof(fixtureKey(), value)
			if err != nil || hex.EncodeToString(signature.CopyBytes()) != vector.Signature {
				test.Fatal("signature differs from independent Ed25519 vector")
			}
			if configclient.VerifyNativeProof(fixtureKey().Public().(ed25519.PublicKey), value, signature.CopyBytes()) != nil {
				test.Fatal("valid vector signature rejected")
			}
			for otherName, other := range values {
				if otherName != name && configclient.VerifyNativeProof(fixtureKey().Public().(ed25519.PublicKey), other, signature.CopyBytes()) == nil {
					test.Fatal("proof replayed across purpose or consent")
				}
			}
			for index := range 64 {
				modified := signature.CopyBytes()
				modified[index] ^= 1
				if !errors.Is(configclient.VerifyNativeProof(fixtureKey().Public().(ed25519.PublicKey), value, modified), configclient.ErrNativeProofSignature) {
					test.Fatal("mutated signature accepted")
				}
			}
		})
	}
	patch, err := configclient.HashPatch(configurationnativev1.Target_TARGET_CLAUDE_SETTINGS, fixture.patch)
	if err != nil || hex.EncodeToString(patch[:]) != answer.Hashes["patch"] {
		test.Fatal("patch hash differs from independent vector")
	}
	preparation, err := configclient.HashPreparation(fixture.preparation)
	if err != nil || hex.EncodeToString(preparation[:]) != answer.Hashes["preparation"] {
		test.Fatal("preparation hash differs from independent vector")
	}
	apply, err := configclient.HashApply(fixture.apply)
	if err != nil || hex.EncodeToString(apply[:]) != answer.Hashes["apply"] || apply == preparation {
		test.Fatal("apply hash differs from independent vector or domain")
	}
}

func TestNativeEveryPresentFieldIsBound(test *testing.T) {
	fixture := nativeFixture(test)
	begin, challenge := beginFixture()
	auto := proto.Clone(fixture.authorize).(*configurationnativev1.AuthorizeNativeMaterializationRequest)
	auto.Authorization = &configurationnativev1.AuthorizeNativeMaterializationRequest_AutoPolicy{AutoPolicy: &configurationnativev1.AutoPolicy{PolicyId: bytes.Repeat([]byte{'P'}, 32), PolicyRevision: 13}}
	for name, scenario := range map[string]struct {
		message proto.Message
		build   func(proto.Message) (configclient.Transcript, error)
	}{
		"bind-begin": {begin, func(message proto.Message) (configclient.Transcript, error) {
			return configclient.BindTranscript(message.(*configurationnativev1.BeginNativeConsumerRequest), challenge)
		}},
		"bind-challenge": {challenge, func(message proto.Message) (configclient.Transcript, error) {
			return configclient.BindTranscript(begin, message.(*configurationnativev1.BeginNativeConsumerResponse))
		}},
		"prepare-request": {fixture.prepare, func(message proto.Message) (configclient.Transcript, error) {
			return configclient.PrepareTranscript(fixture.context, message.(*configurationnativev1.PrepareNativeApplyRequest))
		}},
		"authorize-preparation": {fixture.preparation, func(message proto.Message) (configclient.Transcript, error) {
			return configclient.AuthorizeTranscript(message.(*configurationnativev1.PreparationBinding), fixture.challenge, fixture.authorize)
		}},
		"authorize-request": {fixture.authorize, func(message proto.Message) (configclient.Transcript, error) {
			return configclient.AuthorizeTranscript(fixture.preparation, fixture.challenge, message.(*configurationnativev1.AuthorizeNativeMaterializationRequest))
		}},
		"authorize-auto": {auto, func(message proto.Message) (configclient.Transcript, error) {
			return configclient.AuthorizeTranscript(fixture.preparation, fixture.challenge, message.(*configurationnativev1.AuthorizeNativeMaterializationRequest))
		}},
		"consume-apply": {fixture.apply, func(message proto.Message) (configclient.Transcript, error) {
			return configclient.ConsumeTranscript(message.(*configurationnativev1.ApplyBinding), fixture.challenge, bytes.Repeat([]byte{10}, 32), 1700000060000, fixture.consume)
		}},
		"consume-request": {fixture.consume, func(message proto.Message) (configclient.Transcript, error) {
			return configclient.ConsumeTranscript(fixture.apply, fixture.challenge, bytes.Repeat([]byte{10}, 32), 1700000060000, message.(*configurationnativev1.ConsumeNativeMaterializationRequest))
		}},
		"status-request": {fixture.status, func(message proto.Message) (configclient.Transcript, error) {
			return configclient.StatusTranscript(fixture.preparation, fixture.challenge, message.(*configurationnativev1.GetNativeMaterializationStatusRequest))
		}},
	} {
		test.Run(name, func(test *testing.T) {
			original, err := scenario.build(scenario.message)
			if err != nil {
				test.Fatal("valid mutation baseline rejected")
			}
			signature, err := configclient.SignNativeProof(fixtureKey(), original)
			if err != nil {
				test.Fatal("valid baseline signing rejected")
			}
			root := proto.Clone(scenario.message)
			count := 0
			var walk func(protoreflect.Message)
			walk = func(message protoreflect.Message) {
				message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
					check := func(value protoreflect.Value, set func(protoreflect.Value)) {
						if field.Kind() == protoreflect.MessageKind {
							walk(value.Message())
							return
						}
						if field.Name() == "proof_signature" {
							return
						}
						var modified protoreflect.Value
						switch field.Kind() {
						case protoreflect.BytesKind:
							data := bytes.Clone(value.Bytes())
							data[0] ^= 1
							modified = protoreflect.ValueOfBytes(data)
						case protoreflect.StringKind:
							text := value.String()
							if strings.HasPrefix(text, "https://") {
								text = strings.Replace(text, "proof.example", "other.example", 1)
							} else {
								text = string(byte(text[0]+1)) + text[1:]
							}
							modified = protoreflect.ValueOfString(text)
						case protoreflect.EnumKind:
							modified = protoreflect.ValueOfEnum(value.Enum() + 1)
						case protoreflect.Uint32Kind:
							modified = protoreflect.ValueOfUint32(uint32(value.Uint() + 1))
						case protoreflect.Uint64Kind:
							modified = protoreflect.ValueOfUint64(value.Uint() + 1)
						case protoreflect.Int64Kind:
							modified = protoreflect.ValueOfInt64(value.Int() + 1)
						default:
							test.Fatal("uncovered mutation field kind")
						}
						set(modified)
						changed, err := scenario.build(root)
						if err == nil && configclient.VerifyNativeProof(fixtureKey().Public().(ed25519.PublicKey), changed, signature.CopyBytes()) == nil {
							test.Fatalf("field mutation was not bound: %s", field.FullName())
						}
						set(value)
						count++
					}
					if field.IsList() {
						list := value.List()
						for index := range list.Len() {
							check(list.Get(index), func(value protoreflect.Value) { list.Set(index, value) })
						}
					} else {
						check(value, func(value protoreflect.Value) { message.Set(field, value) })
					}
					return true
				})
			}
			walk(root.ProtoReflect())
			if count == 0 || !proto.Equal(root, scenario.message) {
				test.Fatal("mutation walk empty or failed to restore fixture")
			}
		})
	}
	baseline, err := configclient.PrepareTranscript(fixture.context, fixture.prepare)
	if err != nil {
		test.Fatal("valid context rejected")
	}
	for name, change := range map[string]func(*configclient.ConsumerBinding){
		"owner":  func(binding *configclient.ConsumerBinding) { binding.OwnerID += "x" },
		"origin": func(binding *configclient.ConsumerBinding) { binding.ServerOrigin = "https://other.example:8443" },
		"session": func(binding *configclient.ConsumerBinding) {
			binding.NativeSessionBinding = bytes.Repeat([]byte{99}, 32)
		},
		"operation": func(binding *configclient.ConsumerBinding) { binding.OperationID = bytes.Repeat([]byte{99}, 16) },
		"key":       func(binding *configclient.ConsumerBinding) { binding.ConsumerKeySHA256 = bytes.Repeat([]byte{99}, 32) },
	} {
		test.Run("context-"+name, func(test *testing.T) {
			changed := fixture.context
			change(&changed)
			result, err := configclient.PrepareTranscript(changed, fixture.prepare)
			if err == nil && bytes.Equal(result.CopyBytes(), baseline.CopyBytes()) {
				test.Fatal("context mutation not bound")
			}
		})
	}
}

func TestNativeStageAndPatchValidation(test *testing.T) {
	fixture := nativeFixture(test)
	response := &configurationnativev1.PrepareNativeApplyResponse{Binding: fixture.preparation, Patch: fixture.patch, MaterializationRequired: true, AuthorizeChallenge: fixture.challenge}
	if configclient.ValidatePreparedPatch(response) != nil {
		test.Fatal("valid prepared patch rejected")
	}
	for name, change := range map[string]func(*configurationnativev1.PrepareNativeApplyResponse){
		"hash": func(value *configurationnativev1.PrepareNativeApplyResponse) { value.Binding.PatchSha256[0] ^= 1 },
		"required-slot": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Binding.RequiredSlots[0] = configurationnativev1.SecretSlot_SECRET_SLOT_CLAUDE_AUTH_TOKEN
		},
		"materialization": func(value *configurationnativev1.PrepareNativeApplyResponse) { value.MaterializationRequired = false },
		"unknown-nested": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[0].GetSetSelectedSecret().ProtoReflect().SetUnknown([]byte{16, 1})
		},
		"duplicate-slot": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[1].Slot = value.Patch.Operations[0].Slot
		},
		"order": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[0], value.Patch.Operations[1] = value.Patch.Operations[1], value.Patch.Operations[0]
		},
		"unknown-enum": func(value *configurationnativev1.PrepareNativeApplyResponse) { value.Patch.Operations[0].Slot = 99 },
		"both-auth": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[1].Action = &configurationnativev1.PatchOperation_SetSelectedSecret{SetSelectedSecret: &configurationnativev1.SetSelectedSecret{Slot: configurationnativev1.SecretSlot_SECRET_SLOT_CLAUDE_AUTH_TOKEN}}
		},
		"literal-auth": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[0].Action = &configurationnativev1.PatchOperation_SetLiteral{SetLiteral: &configurationnativev1.SetLiteral{Value: "not-allowed"}}
		},
		"wrong-secret": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[0].GetSetSelectedSecret().Slot = configurationnativev1.SecretSlot_SECRET_SLOT_CLAUDE_AUTH_TOKEN
		},
		"missing-action": func(value *configurationnativev1.PrepareNativeApplyResponse) { value.Patch.Operations[0].Action = nil },
		"nil-op":         func(value *configurationnativev1.PrepareNativeApplyResponse) { value.Patch.Operations[0] = nil },
		"nil-action": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[0].Action = &configurationnativev1.PatchOperation_SetSelectedSecret{}
		},
		"utf8": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[3].GetSetLiteral().Value = string([]byte{255})
		},
		"model-limit": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[3].GetSetLiteral().Value = strings.Repeat("x", 257)
		},
		"endpoint-query": func(value *configurationnativev1.PrepareNativeApplyResponse) {
			value.Patch.Operations[2].GetSetLiteral().Value += "?"
		},
	} {
		test.Run(name, func(test *testing.T) {
			value := proto.Clone(response).(*configurationnativev1.PrepareNativeApplyResponse)
			change(value)
			if configclient.ValidatePreparedPatch(value) == nil {
				test.Fatal("invalid prepared patch accepted")
			}
		})
	}
	native := proto.Clone(response).(*configurationnativev1.PrepareNativeApplyResponse)
	native.Binding.RequiredSlots = nil
	native.MaterializationRequired = false
	for _, index := range []int{0, 2} {
		native.Patch.Operations[index].Action = &configurationnativev1.PatchOperation_Remove{Remove: &configurationnativev1.RemoveEnv{}}
	}
	hash, err := configclient.HashPatch(native.Binding.Target, native.Patch)
	if err != nil {
		test.Fatal("valid native-login patch rejected")
	}
	native.Binding.PatchSha256 = hash[:]
	if configclient.ValidatePreparedPatch(native) != nil {
		test.Fatal("native-login preparation rejected")
	}
	authorize := proto.Clone(fixture.authorize).(*configurationnativev1.AuthorizeNativeMaterializationRequest)
	authorize.RequestedSlots = nil
	if _, err := configclient.AuthorizeTranscript(native.Binding, native.AuthorizeChallenge, authorize); err != nil {
		test.Fatal("native-login second stage rejected")
	}
	apply := &configurationnativev1.ApplyBinding{Preparation: native.Binding, IntentCommitment: authorize.IntentCommitment, IntentExpiresAtUnixMs: authorize.IntentExpiresAtUnixMs}
	if _, err := configclient.ConsumeTranscript(apply, fixture.challenge, bytes.Repeat([]byte{10}, 32), 1700000060000, fixture.consume); !errors.Is(err, configclient.ErrInvalidNativeProof) {
		test.Fatal("native-login fabricated materialization accepted")
	}
	if _, err := configclient.HashApply(&configurationnativev1.ApplyBinding{Preparation: fixture.preparation}); !errors.Is(err, configclient.ErrInvalidNativeProof) {
		test.Fatal("preparation alone accepted as apply binding")
	}
	if _, err := configclient.StatusTranscript(fixture.preparation, fixture.challenge, fixture.status); err != nil {
		test.Fatal("pre-authorization status requires nonexistent commitment")
	}
}

func marshalProof(test *testing.T, message proto.Message) []byte {
	test.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		test.Fatal("synthetic wire marshal failed")
	}
	return data
}

func TestNativePolicyIDOpaqueContract(test *testing.T) {
	fixture := nativeFixture(test)
	for _, entry := range []struct {
		name  string
		value []byte
		valid bool
	}{
		{"one", []byte("A"), true},
		{"sixteen", bytes.Repeat([]byte{'p'}, 16), true},
		{"thirty-two", []byte(strings.Repeat("Aa09_-Zz", 4)), true},
		{"sixty-four", bytes.Repeat([]byte{'P'}, 64), true},
		{"empty", nil, false},
		{"sixty-five", bytes.Repeat([]byte{'p'}, 65), false},
		{"nul", make([]byte, 32), false},
		{"embedded-nul", append(bytes.Repeat([]byte{'p'}, 31), 0), false},
		{"cr", append(bytes.Repeat([]byte{'p'}, 31), '\r'), false},
		{"lf", append(bytes.Repeat([]byte{'p'}, 31), '\n'), false},
		{"crlf", append(bytes.Repeat([]byte{'p'}, 30), '\r', '\n'), false},
		{"non-ascii", []byte("é" + strings.Repeat("p", 30)), false},
		{"invalid-utf8", append(bytes.Repeat([]byte{'p'}, 31), 255), false},
		{"space", []byte(" " + strings.Repeat("p", 31)), false},
		{"slash", []byte("/" + strings.Repeat("p", 31)), false},
		{"dot", []byte("." + strings.Repeat("p", 31)), false},
		{"colon", []byte(":" + strings.Repeat("p", 31)), false},
		{"plus", []byte("+" + strings.Repeat("p", 31)), false},
		{"percent", []byte("%" + strings.Repeat("p", 31)), false},
	} {
		test.Run(entry.name, func(test *testing.T) {
			request := proto.Clone(fixture.authorize).(*configurationnativev1.AuthorizeNativeMaterializationRequest)
			request.Authorization = &configurationnativev1.AuthorizeNativeMaterializationRequest_AutoPolicy{AutoPolicy: &configurationnativev1.AutoPolicy{PolicyId: bytes.Clone(entry.value), PolicyRevision: 13}}
			transcript, err := configclient.AuthorizeTranscript(fixture.preparation, fixture.challenge, request)
			if entry.valid {
				if err != nil || len(transcript.CopyBytes()) == 0 || !bytes.Equal(request.GetAutoPolicy().PolicyId, entry.value) {
					test.Fatal("valid opaque policy ID rejected or changed")
				}
				signature, err := configclient.SignNativeProof(fixtureKey(), transcript)
				if err != nil || configclient.VerifyNativeProof(fixtureKey().Public().(ed25519.PublicKey), transcript, signature.CopyBytes()) != nil {
					test.Fatal("valid policy transcript failed sign/verify")
				}
			} else if !errors.Is(err, configclient.ErrInvalidNativeProof) || transcript.CopyBytes() != nil || !reflect.DeepEqual(transcript, configclient.Transcript{}) {
				test.Fatal("invalid policy ID accepted or nonzero transcript returned")
			}
			request.ProofSignature = bytes.Repeat([]byte{1}, 64)
			destination := &configurationnativev1.AuthorizeNativeMaterializationRequest{PrepareId: "untouched", IntentCommitment: []byte{42}}
			before := proto.Clone(destination)
			err = configclient.StrictUnmarshalNativeProof(marshalProof(test, request), destination)
			if entry.valid {
				if err != nil || !proto.Equal(request, destination) {
					test.Fatal("valid opaque policy ID wire rejected or changed")
				}
			} else if !errors.Is(err, configclient.ErrInvalidNativeProof) || !proto.Equal(before, destination) {
				test.Fatal("invalid policy wire accepted or destination changed")
			}
		})
	}
}

func TestNativeStrictWireRequiresSignatureButBuilderDoesNot(test *testing.T) {
	fixture := nativeFixture(test)
	if _, err := configclient.PrepareTranscript(fixture.context, fixture.prepare); err != nil {
		test.Fatal("unsigned typed input cannot construct signature transcript")
	}
	for _, message := range []proto.Message{fixture.prepare, fixture.authorize, fixture.consume, fixture.status} {
		if configclient.StrictUnmarshalNativeProof(marshalProof(test, message), message.ProtoReflect().New().Interface()) == nil {
			test.Fatal("unsigned proof-bearing wire request accepted")
		}
	}
}

func TestNativeLimitsUnknownsAndNilUnionFailClosed(test *testing.T) {
	begin, challenge := beginFixture()
	begin.ExpectedOwnerId, challenge.OwnerId = strings.Repeat("x", 512), strings.Repeat("x", 512)
	if _, err := configclient.BindTranscript(begin, challenge); err != nil {
		test.Fatal("owner boundary rejected")
	}
	begin.ExpectedOwnerId += "x"
	challenge.OwnerId += "x"
	value, err := configclient.BindTranscript(begin, challenge)
	if !errors.Is(err, configclient.ErrInvalidNativeProof) || value.CopyBytes() != nil {
		test.Fatal("oversized owner accepted or partial transcript returned")
	}
	for _, origin := range []string{"https://user@proof.example", "https://proof.example/", "https://proof.example?", "https://proof.example#", "http://proof.example", "https://PROOF.example", "https://proof.example:00080", "https://proof.example:65536", "https://proof.example:", "https://proof.example\\other", "https://proof.example\n", "https://proof.example/" + strings.Repeat("x", 2048)} {
		begin, challenge := beginFixture()
		begin.ExpectedServerOrigin, challenge.ServerOrigin = origin, origin
		if _, err := configclient.BindTranscript(begin, challenge); !errors.Is(err, configclient.ErrInvalidNativeProof) {
			test.Fatal("noncanonical or unsafe origin accepted")
		}
	}
	for _, origin := range []string{"https://proof.example", "https://[::1]:8443", "http://127.0.0.1:8181", "http://localhost:8181"} {
		begin, challenge := beginFixture()
		begin.ExpectedServerOrigin, challenge.ServerOrigin = origin, origin
		if _, err := configclient.BindTranscript(begin, challenge); err != nil {
			test.Fatal("syntactic origin rejected")
		}
	}
	fixture := nativeFixture(test)
	for name, change := range map[string]func(*configurationnativev1.PreparationBinding){
		"zero-target":   func(value *configurationnativev1.PreparationBinding) { value.Target = 0 },
		"unknown-scope": func(value *configurationnativev1.PreparationBinding) { value.Scope = 99 },
		"kind":          func(value *configurationnativev1.PreparationBinding) { value.AssetKind = 0 },
		"agent":         func(value *configurationnativev1.PreparationBinding) { value.AgentId = "codex" },
		"schema":        func(value *configurationnativev1.PreparationBinding) { value.SchemaVersion = 2 },
		"revision":      func(value *configurationnativev1.PreparationBinding) { value.AssetRevision = 0 },
		"selection":     func(value *configurationnativev1.PreparationBinding) { value.SelectionRevision = 0 },
		"profile":       func(value *configurationnativev1.PreparationBinding) { value.ProfileId = strings.Repeat("a", 65) },
		"path":          func(value *configurationnativev1.PreparationBinding) { value.ProfileId = "/not-a-profile" },
		"digest": func(value *configurationnativev1.PreparationBinding) {
			value.Adapter.DefinitionDigest = strings.Repeat("A", 64)
		},
		"capability": func(value *configurationnativev1.PreparationBinding) {
			value.Adapter.RequiredCapabilities = append(value.Adapter.RequiredCapabilities, "claude.provider-switch.v1")
		},
		"negative-time": func(value *configurationnativev1.PreparationBinding) { value.PreparedAtUnixMs = -1 },
		"ttl": func(value *configurationnativev1.PreparationBinding) {
			value.PrepareExpiresAtUnixMs = value.PreparedAtUnixMs + 300001
		},
		"time-order": func(value *configurationnativev1.PreparationBinding) {
			value.PrepareExpiresAtUnixMs = value.PreparedAtUnixMs
		},
		"duplicate-slots": func(value *configurationnativev1.PreparationBinding) {
			value.RequiredSlots = append(value.RequiredSlots, value.RequiredSlots[0])
		},
		"unknown-nested": func(value *configurationnativev1.PreparationBinding) {
			value.Adapter.ProtoReflect().SetUnknown(wireUint(100, 1))
		},
		"unknown-root": func(value *configurationnativev1.PreparationBinding) {
			value.ProtoReflect().SetUnknown(wireUint(100, 1))
		},
		"invalid-utf8": func(value *configurationnativev1.PreparationBinding) { value.OwnerId = string([]byte{255}) },
	} {
		test.Run(name, func(test *testing.T) {
			modified := proto.Clone(fixture.preparation).(*configurationnativev1.PreparationBinding)
			change(modified)
			hash, err := configclient.HashPreparation(modified)
			if !errors.Is(err, configclient.ErrInvalidNativeProof) || hash != ([32]byte{}) {
				test.Fatal("invalid preparation accepted or partial hash returned")
			}
		})
	}
	for _, length := range []int{0, 15, 17, 31, 33} {
		begin, challenge := beginFixture()
		begin.ClientNonce = make([]byte, length)
		if _, err := configclient.BindTranscript(begin, challenge); !errors.Is(err, configclient.ErrInvalidNativeProof) {
			test.Fatal("nonce length accepted")
		}
	}
	for _, message := range []proto.Message{fixture.preparation, fixture.prepare, fixture.authorize, fixture.apply} {
		modified := proto.Clone(message)
		modified.ProtoReflect().SetUnknown(wireBytes(100, make([]byte, 512<<10)))
		if configclient.StrictUnmarshalNativeProof(marshalProof(test, modified), message.ProtoReflect().New().Interface()) == nil {
			test.Fatal("message budget accepted")
		}
	}
	patch := proto.Clone(fixture.patch).(*configurationnativev1.ClaudeProviderPatch)
	var nilAction *configurationnativev1.PatchOperation_SetSelectedSecret
	patch.Operations[0].Action = nilAction
	if _, err := configclient.HashPatch(configurationnativev1.Target_TARGET_CLAUDE_SETTINGS, patch); !errors.Is(err, configclient.ErrInvalidNativeProof) {
		test.Fatal("typed nil union accepted")
	}
	if _, err := configclient.HashPreparation(nil); !errors.Is(err, configclient.ErrInvalidNativeProof) {
		test.Fatal("nil preparation accepted")
	}
	if _, err := configclient.HashApply(nil); !errors.Is(err, configclient.ErrInvalidNativeProof) {
		test.Fatal("nil apply accepted")
	}
	if _, err := configclient.HashPatch(0, fixture.patch); !errors.Is(err, configclient.ErrInvalidNativeProof) {
		test.Fatal("zero patch target accepted")
	}
}

func TestNativeExactStringsAndExternalChallengeArgumentsAreBound(test *testing.T) {
	begin, challenge := beginFixture()
	begin.ExpectedOwnerId, challenge.OwnerId = "owner-é", "owner-é"
	composed, err := configclient.BindTranscript(begin, challenge)
	if err != nil {
		test.Fatal("valid UTF-8 identity rejected")
	}
	begin.ExpectedOwnerId, challenge.OwnerId = "owner-e\u0301", "owner-e\u0301"
	decomposed, err := configclient.BindTranscript(begin, challenge)
	if err != nil || bytes.Equal(composed.CopyBytes(), decomposed.CopyBytes()) {
		test.Fatal("identity silently Unicode-normalized")
	}
	fixture := nativeFixture(test)
	values := transcripts(test, fixture)
	for name, build := range map[string]func() (configclient.Transcript, error){
		"authorize": func() (configclient.Transcript, error) {
			return configclient.AuthorizeTranscript(fixture.preparation, bytes.Repeat([]byte{99}, 32), fixture.authorize)
		},
		"consume-authorize": func() (configclient.Transcript, error) {
			return configclient.ConsumeTranscript(fixture.apply, bytes.Repeat([]byte{99}, 32), bytes.Repeat([]byte{10}, 32), 1700000060000, fixture.consume)
		},
		"consume-challenge": func() (configclient.Transcript, error) {
			return configclient.ConsumeTranscript(fixture.apply, fixture.challenge, bytes.Repeat([]byte{99}, 32), 1700000060000, fixture.consume)
		},
		"consume-expiry": func() (configclient.Transcript, error) {
			return configclient.ConsumeTranscript(fixture.apply, fixture.challenge, bytes.Repeat([]byte{10}, 32), 1700000060001, fixture.consume)
		},
		"status": func() (configclient.Transcript, error) {
			return configclient.StatusTranscript(fixture.preparation, bytes.Repeat([]byte{99}, 32), fixture.status)
		},
	} {
		test.Run(name, func(test *testing.T) {
			purpose := name
			if strings.HasPrefix(name, "consume-") {
				purpose = "consume"
			}
			signature, err := configclient.SignNativeProof(fixtureKey(), values[purpose])
			if err != nil {
				test.Fatal("baseline signing failed")
			}
			changed, err := build()
			if err != nil || configclient.VerifyNativeProof(fixtureKey().Public().(ed25519.PublicKey), changed, signature.CopyBytes()) == nil {
				test.Fatal("valid external argument mutation not bound")
			}
		})
	}
	for _, expiry := range []int64{-1, 0, fixture.preparation.PreparedAtUnixMs, fixture.apply.IntentExpiresAtUnixMs + 1} {
		if _, err := configclient.ConsumeTranscript(fixture.apply, fixture.challenge, bytes.Repeat([]byte{10}, 32), expiry, fixture.consume); !errors.Is(err, configclient.ErrInvalidNativeProof) {
			test.Fatal("invalid grant expiry accepted")
		}
	}
	for _, expiry := range []int64{-1, 0, fixture.preparation.PreparedAtUnixMs, fixture.preparation.PrepareExpiresAtUnixMs + 1} {
		request := proto.Clone(fixture.authorize).(*configurationnativev1.AuthorizeNativeMaterializationRequest)
		request.IntentExpiresAtUnixMs = expiry
		if _, err := configclient.AuthorizeTranscript(fixture.preparation, fixture.challenge, request); !errors.Is(err, configclient.ErrInvalidNativeProof) {
			test.Fatal("invalid intent expiry accepted")
		}
	}
	fixture.authorize.Authorization = nil
	if _, err := configclient.AuthorizeTranscript(fixture.preparation, fixture.challenge, fixture.authorize); !errors.Is(err, configclient.ErrInvalidNativeProof) {
		test.Fatal("missing consent accepted")
	}
	var nilPolicy *configurationnativev1.AuthorizeNativeMaterializationRequest_AutoPolicy
	fixture.authorize.Authorization = nilPolicy
	if _, err := configclient.AuthorizeTranscript(fixture.preparation, fixture.challenge, fixture.authorize); !errors.Is(err, configclient.ErrInvalidNativeProof) {
		test.Fatal("typed nil consent accepted")
	}
	fixture.status.GrantId = "grant-synthetic"
	changed, err := configclient.StatusTranscript(fixture.preparation, fixture.challenge, fixture.status)
	if err != nil || bytes.Equal(changed.CopyBytes(), values["status"].CopyBytes()) {
		test.Fatal("empty and nonempty grant ID encoded equally")
	}
}

func wireBytes(number protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), value)
}

func wireUint(number protowire.Number, value uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(nil, number, protowire.VarintType), value)
}

func TestNativeStrictWireAllowlistAndNoPartialDestination(test *testing.T) {
	fixture := nativeFixture(test)
	begin, challenge := beginFixture()
	response := &configurationnativev1.PrepareNativeApplyResponse{Binding: fixture.preparation, Patch: fixture.patch, MaterializationRequired: true, AuthorizeChallenge: fixture.challenge}
	for _, message := range []proto.Message{begin, challenge, &configurationnativev1.BindNativeConsumerRequest{ChallengeId: challenge.ChallengeId, ProofSignature: bytes.Repeat([]byte{1}, 64)}, fixture.preparation.Adapter, fixture.preparation, fixture.apply, fixture.patch, fixture.prepare, response, fixture.authorize, fixture.consume, fixture.status} {
		if field := message.ProtoReflect().Descriptor().Fields().ByName("proof_signature"); field != nil {
			message.ProtoReflect().Set(field, protoreflect.ValueOfBytes(bytes.Repeat([]byte{1}, 64)))
		}
		decoded := message.ProtoReflect().New().Interface()
		if configclient.StrictUnmarshalNativeProof(marshalProof(test, message), decoded) != nil || !proto.Equal(message, decoded) {
			test.Fatal("supported native wire failed exact roundtrip")
		}
	}
	for _, message := range []proto.Message{nil, (*configurationnativev1.PrepareNativeApplyRequest)(nil), &emptypb.Empty{}, &configurationv1.Asset{}, &configurationnativev1.ConsumeNativeMaterializationResponse{}, &configurationnativev1.BindNativeConsumerResponse{}, &configurationnativev1.AuthorizeNativeMaterializationResponse{}, &configurationnativev1.GetNativeMaterializationStatusResponse{}} {
		if !errors.Is(configclient.StrictUnmarshalNativeProof(nil, message), configclient.ErrInvalidNativeProof) {
			test.Fatal("typed nil, unsupported type, or sensitive response accepted")
		}
	}
	valid := marshalProof(test, fixture.prepare)
	nestedDuplicate := proto.Clone(fixture.prepare).(*configurationnativev1.PrepareNativeApplyRequest)
	nestedDuplicate.Adapter.ProtoReflect().SetUnknown(wireBytes(2, []byte(nestedDuplicate.Adapter.PluginId)))
	nestedUnknown := proto.Clone(fixture.prepare).(*configurationnativev1.PrepareNativeApplyRequest)
	nestedUnknown.Adapter.ProtoReflect().SetUnknown(wireUint(100, 1))
	for name, data := range map[string][]byte{
		"duplicate-singular":      append(bytes.Clone(valid), wireBytes(1, []byte(fixture.prepare.ExpectedOwnerId))...),
		"duplicate-signature":     append(append(bytes.Clone(valid), wireBytes(14, bytes.Repeat([]byte{1}, 64))...), wireBytes(14, bytes.Repeat([]byte{1}, 64))...),
		"nested-duplicate":        marshalProof(test, nestedDuplicate),
		"nested-unknown":          marshalProof(test, nestedUnknown),
		"unknown":                 append(bytes.Clone(valid), wireUint(100, 1)...),
		"reserved-old-commitment": append(bytes.Clone(valid), wireBytes(12, bytes.Repeat([]byte{1}, 32))...),
		"reserved-old-expiry":     append(bytes.Clone(valid), wireUint(15, 1700000300000)...),
		"bad-utf8":                append(bytes.Clone(valid), wireBytes(1, []byte{255})...),
		"nonminimal-tag":          append([]byte{valid[0] | 128, 0}, valid[1:]...),
		"wrong-wire-kind":         append(bytes.Clone(valid), wireUint(1, 1)...),
		"truncated":               valid[:len(valid)-1],
		"size-limit":              make([]byte, (512<<10)+1),
		"json":                    []byte(`{"expectedOwnerId":"synthetic"}`),
		"compressed":              []byte{31, 139, 8, 0},
	} {
		test.Run(name, func(test *testing.T) {
			destination := &configurationnativev1.PrepareNativeApplyRequest{ExpectedOwnerId: "untouched", ClientNonce: []byte{42}}
			before := proto.Clone(destination)
			if !errors.Is(configclient.StrictUnmarshalNativeProof(data, destination), configclient.ErrInvalidNativeProof) || !proto.Equal(before, destination) {
				test.Fatal("invalid wire accepted or destination partially changed")
			}
		})
	}
	authorizeRaw := marshalProof(test, fixture.authorize)
	auto := &configurationnativev1.AutoPolicy{PolicyId: bytes.Repeat([]byte{'Q'}, 32), PolicyRevision: 1}
	duplicateConsent := append(bytes.Clone(authorizeRaw), wireBytes(6, marshalProof(test, auto))...)
	if configclient.StrictUnmarshalNativeProof(duplicateConsent, &configurationnativev1.AuthorizeNativeMaterializationRequest{}) == nil {
		test.Fatal("duplicate oneof survived merge")
	}
	patchDuplicate := proto.Clone(fixture.patch).(*configurationnativev1.ClaudeProviderPatch)
	patchDuplicate.Operations[1].ProtoReflect().SetUnknown(wireBytes(4, nil))
	if configclient.StrictUnmarshalNativeProof(marshalProof(test, patchDuplicate), &configurationnativev1.ClaudeProviderPatch{}) == nil {
		test.Fatal("duplicate nested same oneof survived merge")
	}
	packed := proto.Clone(fixture.authorize).(*configurationnativev1.AuthorizeNativeMaterializationRequest)
	packed.RequestedSlots = nil
	for _, rawSlots := range [][]byte{{1, 1}, {2, 1}, {0}, {99}, {129, 0}} {
		data := append(marshalProof(test, packed), wireBytes(4, rawSlots)...)
		if configclient.StrictUnmarshalNativeProof(data, &configurationnativev1.AuthorizeNativeMaterializationRequest{}) == nil {
			test.Fatal("duplicate, unordered, zero, unknown, or nonminimal packed slots accepted")
		}
	}
	overflow := proto.Clone(fixture.prepare).(*configurationnativev1.PrepareNativeApplyRequest)
	overflow.SchemaVersion = 0
	if configclient.StrictUnmarshalNativeProof(append(marshalProof(test, overflow), wireUint(5, 1<<32|1)...), &configurationnativev1.PrepareNativeApplyRequest{}) == nil {
		test.Fatal("overflowing uint32 accepted")
	}
	response.MaterializationRequired = false
	if configclient.StrictUnmarshalNativeProof(append(marshalProof(test, response), wireUint(3, 2)...), &configurationnativev1.PrepareNativeApplyResponse{}) == nil {
		test.Fatal("noncanonical bool accepted")
	}
	if configclient.StrictUnmarshalNativeProof(make([]byte, 8193), &configurationnativev1.BeginNativeConsumerRequest{}) == nil {
		test.Fatal("bootstrap budget not enforced")
	}
}

func TestNativeOpaqueProofsCloneAndRedact(test *testing.T) {
	begin, challenge := beginFixture()
	transcript, err := configclient.BindTranscript(begin, challenge)
	if err != nil {
		test.Fatal("fixture transcript rejected")
	}
	before := transcript.CopyBytes()
	begin.ClientNonce[0] ^= 1
	challenge.Challenge[0] ^= 1
	revealed := transcript.CopyBytes()
	clear(revealed)
	if !bytes.Equal(before, transcript.CopyBytes()) {
		test.Fatal("transcript retained mutable input/output alias")
	}
	signature, err := configclient.SignNativeProof(fixtureKey(), transcript)
	if err != nil {
		test.Fatal("fixture signing failed")
	}
	signatureBefore := signature.CopyBytes()
	clear(signature.CopyBytes())
	if !bytes.Equal(signatureBefore, signature.CopyBytes()) {
		test.Fatal("signature reveal retained mutable alias")
	}
	for _, value := range []any{transcript, &transcript, signature, &signature, configclient.Transcript{}, configclient.Signature{}} {
		if value.(fmt.Stringer).String() != "[redacted native proof]" || value.(fmt.GoStringer).GoString() != "[redacted native proof]" {
			test.Fatal("explicit string methods did not redact")
		}
		var text, structured bytes.Buffer
		slog.New(slog.NewTextHandler(&text, nil)).Info("fixture", "proof", value)
		slog.New(slog.NewJSONHandler(&structured, nil)).Info("fixture", "proof", value)
		if !strings.Contains(text.String(), "[redacted native proof]") || !strings.Contains(structured.String(), "[redacted native proof]") {
			test.Fatal("direct slog did not redact")
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != `"[redacted native proof]"` {
			test.Fatal("direct JSON proof did not redact")
		}
		encoded, err = value.(interface{ MarshalText() ([]byte, error) }).MarshalText()
		if err != nil || string(encoded) != "[redacted native proof]" {
			test.Fatal("direct text proof did not redact")
		}
		for _, verb := range "vtsqdboxXOcUeEfFgGz" {
			for _, flags := range []string{"", "#", "+", "-", " ", "0", "#0+- "} {
				for _, dimensions := range []string{"", "64", ".1", "64.1"} {
					if fmt.Sprintf("%"+flags+dimensions+string(verb), value) != "[redacted native proof]" {
						test.Fatal("direct proof formatting not fixed-marker")
					}
				}
			}
		}
	}
	var fingerprints []string
	for _, raw := range [][]byte{before, signatureBefore} {
		for _, format := range []string{"%s", "%x", "%X", "%d", "%b", "%o", "%c", "%U", "%#v"} {
			encoded := fmt.Sprintf(format, raw)
			if format == "%#v" {
				encoded = strings.TrimSuffix(encoded[strings.IndexByte(encoded, '{')+1:], "}")
			} else if strings.HasPrefix(encoded, "[") {
				encoded = strings.TrimSuffix(strings.TrimPrefix(encoded, "["), "]")
			}
			fingerprints = append(fingerprints, encoded)
		}
	}
	for _, value := range []any{struct{ private configclient.Transcript }{transcript}, struct{ private configclient.Signature }{signature}, struct{ private []configclient.Transcript }{[]configclient.Transcript{transcript}}, struct {
		private map[string]configclient.Signature
	}{map[string]configclient.Signature{"value": signature}}, struct{ private *configclient.Transcript }{&transcript}, struct{ private *configclient.Signature }{}, struct{ private configclient.Transcript }{}} {
		assertSafe := func(output string) {
			test.Helper()
			for _, encoded := range fingerprints {
				if strings.Contains(output, encoded) {
					test.Fatal("private proof container exposed reversible material")
				}
			}
		}
		for _, verb := range "vtsqdboxXOcUeEfFgGzawTp" {
			for _, flags := range []string{"", "#", "+", "#0+- "} {
				for _, dimensions := range []string{"", "64", ".1", "64.1"} {
					assertSafe(fmt.Sprintf("%"+flags+dimensions+string(verb), value))
				}
			}
		}
		var text, structured bytes.Buffer
		slog.New(slog.NewTextHandler(&text, nil)).Info("fixture", "proof", value)
		slog.New(slog.NewJSONHandler(&structured, nil)).Info("fixture", "proof", value)
		assertSafe(text.String())
		assertSafe(structured.String())
		encoded, err := json.Marshal(value)
		if err != nil {
			assertSafe(err.Error())
		} else {
			assertSafe(string(encoded))
		}
	}
	if (configclient.Transcript{}).CopyBytes() != nil || (configclient.Signature{}).CopyBytes() != nil {
		test.Fatal("zero wrapper revealed bytes")
	}
	for _, key := range []ed25519.PrivateKey{nil, make([]byte, 63), make([]byte, 64), make([]byte, 65)} {
		result, err := configclient.SignNativeProof(key, transcript)
		if !errors.Is(err, configclient.ErrInvalidNativeProof) || result.CopyBytes() != nil {
			test.Fatal("invalid key accepted or partial signature returned")
		}
	}
	if _, err := configclient.SignNativeProof(fixtureKey(), configclient.Transcript{}); !errors.Is(err, configclient.ErrInvalidNativeProof) {
		test.Fatal("zero transcript signed")
	}
	for _, length := range []int{0, 31, 33} {
		if !errors.Is(configclient.VerifyNativeProof(make([]byte, length), transcript, signatureBefore), configclient.ErrInvalidNativeProof) {
			test.Fatal("invalid public key accepted")
		}
	}
	for _, length := range []int{0, 63, 65} {
		if !errors.Is(configclient.VerifyNativeProof(fixtureKey().Public().(ed25519.PublicKey), transcript, make([]byte, length)), configclient.ErrInvalidNativeProof) {
			test.Fatal("invalid signature length accepted")
		}
	}
	if !reflect.DeepEqual(transcript.CopyBytes(), before) {
		test.Fatal("verification changed transcript")
	}
}

func FuzzNativeStrictWire(test *testing.F) {
	begin, _ := beginFixture()
	fixture := nativeFixture(test)
	fixture.prepare.ProofSignature = bytes.Repeat([]byte{1}, 64)
	fixture.authorize.ProofSignature = bytes.Repeat([]byte{1}, 64)
	seeds := []proto.Message{begin, fixture.prepare, fixture.authorize, fixture.patch, fixture.apply}
	for index, message := range seeds {
		seed, err := proto.Marshal(message)
		if err != nil {
			test.Fatal("seed failed")
		}
		test.Add(append([]byte{byte(index)}, seed...))
	}
	test.Add([]byte{0, 10, 1, 255})
	test.Fuzz(func(test *testing.T, data []byte) {
		if len(data) == 0 || len(data) > maxFuzzNativeBytes+1 {
			return
		}
		destination := proto.Clone(seeds[int(data[0])%len(seeds)])
		before := proto.Clone(destination)
		input := bytes.Clone(data)
		err := configclient.StrictUnmarshalNativeProof(data[1:], destination)
		if !bytes.Equal(input, data) || err != nil && !proto.Equal(before, destination) {
			test.Fatal("strict decoder mutated input or left partial DTO")
		}
		if err == nil {
			canonical, err := proto.Marshal(destination)
			if err != nil || configclient.StrictUnmarshalNativeProof(canonical, destination.ProtoReflect().New().Interface()) != nil {
				test.Fatal("accepted wire failed canonical roundtrip")
			}
		}
	})
}

const maxFuzzNativeBytes = 512 << 10

func TestNativeBindTranscriptUsesVersionedPurpose(test *testing.T) {
	begin, challenge := beginFixture()
	transcript, err := configclient.BindTranscript(begin, challenge)
	if err != nil {
		test.Fatal("valid bind transcript rejected")
	}
	var prefix []byte
	for _, value := range []string{"cognate/native-config-proof/v1", "bind"} {
		prefix = binary.BigEndian.AppendUint32(prefix, uint32(len(value)))
		prefix = append(prefix, value...)
	}
	prefix = binary.BigEndian.AppendUint32(prefix, 1)
	if !bytes.HasPrefix(transcript.CopyBytes(), prefix) || len(transcript.CopyBytes()) <= len(prefix) {
		test.Fatal("bind transcript omitted versioned domain, purpose, or fields")
	}
}
