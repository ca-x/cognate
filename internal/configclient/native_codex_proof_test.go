package configclient

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"os"
	"slices"
	"strings"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	publicv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type clientCodexOracle struct {
	Declaration agentconfig.CodexProviderDeclarationV1 `json:"declaration"`
	PublicKey   string                                 `json:"public_key"`
	Hashes      map[string]string                      `json:"hashes"`
	Negatives   map[string]string                      `json:"hash_negative_vectors"`
	Vectors     map[string]struct {
		Hex       string `json:"transcript_hex"`
		Signature string `json:"signature"`
	} `json:"vectors"`
	Local struct {
		Profile     string   `json:"nativeProfileID"`
		ProfileHash string   `json:"nativeProfileSHA256"`
		Template    string   `json:"templateSHA256"`
		Models      []string `json:"managedModels"`
		Witness     string   `json:"witnessSHA256"`
		Baselines   []struct {
			Role     uint32 `json:"role"`
			Presence int    `json:"presence"`
			Hash     string `json:"sha256"`
		} `json:"baselines"`
	} `json:"local_binding"`
}

func clientCodexFixture(test *testing.T) (clientCodexOracle, *nativev1.PrepareNativeApplyRequest, *nativev1.PrepareNativeApplyResponse, *nativev1.CodexLocalBindingV1) {
	test.Helper()
	encoded, err := os.ReadFile("testdata/native-codex-proof-v1.json")
	var oracle clientCodexOracle
	if err != nil || json.Unmarshal(encoded, &oracle) != nil {
		test.Fatal("cannot load independent literal")
	}
	decode := func(text string) []byte {
		decoded, err := hex.DecodeString(text)
		if err != nil {
			test.Fatal("bad literal hex")
		}
		return decoded
	}
	keyHash := sha256.Sum256(decode(oracle.PublicKey))
	adapter := &nativev1.AdapterBinding{ApiVersion: 1, PluginId: "builtin.codex", PluginVersion: "1.1.0", DefinitionDigest: "f45d146803f9f4f818a5d17ea1cc536b27bc77f49eb059c10c2f305912af02b4", RequiredCapabilities: []string{"codex.provider-plan.v1"}}
	request := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: "owner-synthetic", AssetId: string(bytes.Repeat([]byte{'A'}, 26)), ExpectedAssetRevision: 7, AgentId: "codex", SchemaVersion: 1, Adapter: adapter, Target: 2, Scope: 1, ProfileId: "profile-c2", SelectionRevision: 9, OperationId: bytes.Repeat([]byte{1}, 16), ClientNonce: bytes.Repeat([]byte{6}, 32), CodexContractVersion: 1}
	declaration := &nativev1.CodexDeclarationV1{ApiVersion: 1, Target: "codex.config", ProviderId: "cognate", WireApi: "responses", Endpoint: oracle.Declaration.Endpoint, Model: oracle.Declaration.Model, ReasoningEffort: new(publicv1.CodexReasoningEffort(5)), Catalog: &publicv1.CodexCatalog{Models: []*publicv1.CodexCatalogModel{{Model: "alpha", DisplayName: new("Alpha"), ContextWindow: new(uint64(131072)), SupportsParallelToolCalls: new(false), InputModalities: &publicv1.CodexModalities{Values: []publicv1.CodexModality{2, 1}}, BaseInstructions: new("Keep\nspaces\t"), ReasoningLevels: &publicv1.CodexReasoningLevels{Values: []publicv1.CodexReasoningEffort{5, 1}}, DefaultReasoningLevel: new(publicv1.CodexReasoningEffort(5))}, {Model: "beta"}}}, CredentialReference: "codex.provider-key", RequiredSlots: []nativev1.SecretSlot{3}, LocalAuthPolicy: "preserve-local-login-v1"}
	preparation := &nativev1.PreparationBinding{OwnerId: request.ExpectedOwnerId, ServerOrigin: "https://proof.example:8443", NativeSessionBinding: bytes.Repeat([]byte{5}, 32), OperationId: request.OperationId, AssetId: request.AssetId, AssetRevision: 7, AgentId: "codex", AssetKind: 1, SchemaVersion: 1, Adapter: adapter, Target: 2, Scope: 1, ProfileId: "profile-c2", SelectionRevision: 9, RequiredSlots: []nativev1.SecretSlot{3}, PrepareExpiresAtUnixMs: 1700000300000, ConsumerKeySha256: keyHash[:], PrepareId: "prepare-synthetic", PreparedAtUnixMs: 1700000000000, Codex: &nativev1.CodexPreparationV1{ContractVersion: 1, DeclarationSha256: decode(oracle.Hashes["declaration"])}}
	response := &nativev1.PrepareNativeApplyResponse{Binding: preparation, CodexDeclaration: declaration, AuthorizeChallenge: bytes.Repeat([]byte{11}, 32), MaterializationRequired: true}
	local := &nativev1.CodexLocalBindingV1{BindingVersion: 1, NativeProfileId: oracle.Local.Profile, NativeProfileSha256: decode(oracle.Local.ProfileHash), TemplateSha256: decode(oracle.Local.Template), EffectiveScope: 1, ProviderOwnership: 2, CatalogOwnership: 2, ManagedModels: oracle.Local.Models, AuthStore: 2, KeyringState: 1, FileLoginState: 4, LocalAuthPolicy: "preserve-local-login-v1", RequiresOpenaiAuth: new(true), ObservationWitness: decode(oracle.Local.Witness)}
	for _, baseline := range oracle.Local.Baselines {
		entry := &nativev1.CodexRoleBaseline{Role: nativev1.CodexRole(baseline.Role)}
		switch baseline.Presence {
		case 1:
			entry.State = &nativev1.CodexRoleBaseline_Missing{Missing: new(nativev1.CodexMissing)}
		case 2:
			entry.State = &nativev1.CodexRoleBaseline_Present{Present: &nativev1.CodexPresent{Sha256: decode(baseline.Hash)}}
		case 3:
			entry.State = &nativev1.CodexRoleBaseline_NotObserved{NotObserved: new(nativev1.CodexNotObserved)}
		}
		local.Baselines = append(local.Baselines, entry)
	}
	return oracle, request, response, local
}

func TestCodexNativeCanonicalGolden(test *testing.T) {
	oracle, request, response, local := clientCodexFixture(test)
	check := func(name string) func([32]byte, error) {
		return func(hash [32]byte, err error) {
			test.Helper()
			if err != nil || hex.EncodeToString(hash[:]) != oracle.Hashes[name] {
				test.Fatalf("literal %s mismatch", name)
			}
		}
	}
	check("declaration")(HashCodexDeclaration(response.CodexDeclaration))
	check("preparation")(HashCodexPreparation(response.Binding))
	check("local_binding")(HashCodexLocalBinding(response.Binding.Codex.DeclarationSha256, local))
	check("intent")(HashCodexIntent(response.Binding, local, 11, 1700000240000))
	commitment, _ := hex.DecodeString(oracle.Hashes["intent"])
	apply := &nativev1.ApplyBinding{Preparation: response.Binding, IntentCommitment: commitment, IntentExpiresAtUnixMs: 1700000240000, CodexLocalBinding: local}
	check("apply")(HashCodexApply(apply))
	if ValidateCodexPreparedDeclaration(response) != nil {
		test.Fatal("literal declaration not validated")
	}
	consumer := ConsumerBinding{OwnerID: response.Binding.OwnerId, ServerOrigin: response.Binding.ServerOrigin, OperationID: request.OperationId, NativeSessionBinding: response.Binding.NativeSessionBinding, ConsumerKeySHA256: response.Binding.ConsumerKeySha256}
	authorize := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: response.Binding.PrepareId, OperationId: request.OperationId, IntentCommitment: commitment, RequestedSlots: []nativev1.SecretSlot{3}, ClientNonce: request.ClientNonce, IntentExpiresAtUnixMs: apply.IntentExpiresAtUnixMs, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 11}}, CodexLocalBinding: local}
	consume := &nativev1.ConsumeNativeMaterializationRequest{PrepareId: response.Binding.PrepareId, GrantId: "grant-synthetic", GrantToken: bytes.Repeat([]byte{7}, 32), OperationId: request.OperationId, IntentCommitment: commitment, ClientNonce: bytes.Repeat([]byte{12}, 32)}
	status := &nativev1.GetNativeMaterializationStatusRequest{PrepareId: response.Binding.PrepareId, OperationId: request.OperationId, ClientNonce: bytes.Repeat([]byte{13}, 32)}
	begin := &nativev1.BeginNativeConsumerRequest{ExpectedOwnerId: consumer.OwnerID, ExpectedServerOrigin: consumer.ServerOrigin, Purpose: 1, OperationId: request.OperationId, ConsumerPublicKey: mustCodexHex(test, oracle.PublicKey), ClientNonce: bytes.Repeat([]byte{3}, 32)}
	challenge := &nativev1.BeginNativeConsumerResponse{ChallengeId: "challenge-synthetic", OwnerId: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, Challenge: bytes.Repeat([]byte{4}, 32), NativeSessionBinding: consumer.NativeSessionBinding, ChallengeExpiresAtUnixMs: 1700000060000, NativeSessionExpiresAtUnixMs: 1700000300000}
	checks := map[string]func() (Transcript, error){"bind": func() (Transcript, error) { return BindTranscript(begin, challenge) }, "prepare": func() (Transcript, error) { return CodexPrepareTranscript(consumer, request) }, "authorize": func() (Transcript, error) {
		return CodexAuthorizeTranscript(response.Binding, response.AuthorizeChallenge, authorize)
	}, "consume": func() (Transcript, error) {
		return CodexConsumeTranscript(apply, response.AuthorizeChallenge, bytes.Repeat([]byte{10}, 32), 1700000060000, consume)
	}, "status": func() (Transcript, error) {
		return CodexStatusTranscript(response.Binding, response.AuthorizeChallenge, status)
	}}
	seed := sha256.Sum256([]byte("cognate codex native consumption synthetic vector v1"))
	key := ed25519.NewKeyFromSeed(seed[:])
	defer clear(key)
	for purpose, build := range checks {
		transcript, err := build()
		if err != nil || hex.EncodeToString(transcript.CopyBytes()) != oracle.Vectors[purpose].Hex {
			test.Fatalf("literal %s transcript mismatch", purpose)
		}
		signature, err := SignNativeProof(key, transcript)
		if err != nil || hex.EncodeToString(signature.CopyBytes()) != oracle.Vectors[purpose].Signature {
			test.Fatal("literal signature mismatch")
		}
		if VerifyNativeProof(key.Public().(ed25519.PublicKey), transcript, signature.CopyBytes()) != nil {
			test.Fatal("Go Ed25519 failed")
		}
	}
	response.CodexDeclaration.Catalog.Models[0].SupportsParallelToolCalls = nil
	absent, err := HashCodexDeclaration(response.CodexDeclaration)
	if err != nil || hex.EncodeToString(absent[:]) != oracle.Negatives["parallel_absent_declaration"] {
		test.Fatal("optional false collapsed")
	}
}

func TestCodexNativeSchemaStrictAndIsolation(test *testing.T) {
	_, request, response, local := clientCodexFixture(test)
	request.ProofSignature = bytes.Repeat([]byte{17}, 64)
	if encoded, err := proto.Marshal(request); err != nil {
		test.Fatal("valid Codex fixture cannot marshal")
	} else {
		target := new(nativev1.PrepareNativeApplyRequest)
		if StrictUnmarshalNativeProof(encoded, target) != nil {
			test.Fatal("Codex strict roundtrip rejected")
		}
	}
	legacy := proto.Clone(request).(*nativev1.PrepareNativeApplyRequest)
	legacy.AgentId = "claude"
	legacy.Target = 1
	legacy.CodexContractVersion = 0
	legacy.Adapter = nativeAdapterBinding()
	encoded, err := proto.Marshal(legacy)
	if err != nil {
		test.Fatal("legacy fixture rejected")
	}
	zeroTag := protowire.AppendTag(bytes.Clone(encoded), 40, protowire.VarintType)
	zeroTag = protowire.AppendVarint(zeroTag, 0)
	if StrictUnmarshalNativeProof(zeroTag, new(nativev1.PrepareNativeApplyRequest)) == nil {
		test.Fatal("Claude accepted explicit zero Codex tag40")
	}
	legacy.CodexContractVersion = 1
	if _, err := PrepareTranscript(ConsumerBinding{OwnerID: legacy.ExpectedOwnerId, ServerOrigin: response.Binding.ServerOrigin, NativeSessionBinding: response.Binding.NativeSessionBinding, OperationID: legacy.OperationId, ConsumerKeySHA256: response.Binding.ConsumerKeySha256}, legacy); err == nil {
		test.Fatal("legacy transcript ignored Codex contract")
	}
	for name, message := range map[string]proto.Message{"declaration": response.CodexDeclaration, "local": local, "authorize-sensitive": new(nativev1.AuthorizeNativeMaterializationResponse), "consume-sensitive": new(nativev1.ConsumeNativeMaterializationResponse), "bind-sensitive": new(nativev1.BindNativeConsumerResponse)} {
		test.Run(name, func(test *testing.T) {
			encoded, _ := proto.Marshal(message)
			if StrictUnmarshalNativeProof(encoded, message) == nil {
				test.Fatal("public proof root allowlist expanded")
			}
		})
	}
	prepared, err := proto.Marshal(response)
	if err != nil {
		test.Fatal("fixture marshal failed")
	}
	prepared = protowire.AppendTag(prepared, 2, protowire.BytesType)
	prepared = protowire.AppendBytes(prepared, nil)
	if StrictUnmarshalNativeProof(prepared, new(nativev1.PrepareNativeApplyResponse)) == nil {
		test.Fatal("empty Claude payload accepted in Codex response")
	}
}

func mustCodexHex(test *testing.T, text string) []byte {
	test.Helper()
	decoded, err := hex.DecodeString(text)
	if err != nil {
		test.Fatal("bad fixture hex")
	}
	return decoded
}

func TestCodexNativeCanonicalValidation(test *testing.T) {
	for name, mutate := range map[string]func(*nativev1.CodexDeclarationV1){
		"slot":             func(value *nativev1.CodexDeclarationV1) { value.RequiredSlots[0] = 1 },
		"empty-catalog":    func(value *nativev1.CodexDeclarationV1) { value.Catalog.Models = nil },
		"duplicate-model":  func(value *nativev1.CodexDeclarationV1) { value.Catalog.Models[1].Model = "alpha" },
		"empty-modalities": func(value *nativev1.CodexDeclarationV1) { value.Catalog.Models[0].InputModalities.Values = nil },
		"invalid-default": func(value *nativev1.CodexDeclarationV1) {
			value.Catalog.Models[0].DefaultReasoningLevel = new(publicv1.CodexReasoningEffort(4))
		},
		"empty-none": func(value *nativev1.CodexDeclarationV1) {
			value.ReasoningEffort = new(publicv1.CodexReasoningEffort(0))
		},
		"unknown-nested": func(value *nativev1.CodexDeclarationV1) {
			value.Catalog.Models[0].InputModalities.ProtoReflect().SetUnknown([]byte{16, 0})
		},
		"bad-endpoint": func(value *nativev1.CodexDeclarationV1) { value.Endpoint = "https://provider.invalid/%00" },
		"bad-context": func(value *nativev1.CodexDeclarationV1) {
			value.Catalog.Models[0].ContextWindow = new(uint64(2147483648))
		},
		"bad-instructions": func(value *nativev1.CodexDeclarationV1) {
			value.Catalog.Models[0].BaseInstructions = new(strings.Repeat("x", 2049))
		},
	} {
		test.Run(name, func(test *testing.T) {
			_, _, response, _ := clientCodexFixture(test)
			mutate(response.CodexDeclaration)
			hash, err := HashCodexDeclaration(response.CodexDeclaration)
			if err == nil || hash != [32]byte{} {
				test.Fatal("invalid declaration accepted")
			}
		})
	}
	oracle, _, response, local := clientCodexFixture(test)
	slices.Reverse(response.CodexDeclaration.Catalog.Models)
	hash, err := HashCodexDeclaration(response.CodexDeclaration)
	if err != nil || hex.EncodeToString(hash[:]) != oracle.Negatives["catalog_reordered_declaration"] {
		test.Fatal("order not preserved")
	}
	local.Baselines[1].State = &nativev1.CodexRoleBaseline_Missing{Missing: new(nativev1.CodexMissing)}
	hash, err = HashCodexLocalBinding(response.Binding.Codex.DeclarationSha256, local)
	if err != nil || hex.EncodeToString(hash[:]) != oracle.Negatives["auth_missing_local_binding"] {
		test.Fatal("Missing collapsed with NotObserved")
	}
	for _, store := range []nativev1.CodexAuthStore{2, 3} {
		for _, login := range []nativev1.CodexFileLoginState{1, 2, 3, 4} {
			_, _, response, local = clientCodexFixture(test)
			local.AuthStore = store
			local.FileLoginState = login
			if login == 1 {
				local.Baselines[1].State = &nativev1.CodexRoleBaseline_Present{Present: &nativev1.CodexPresent{Sha256: bytes.Repeat([]byte{32}, 32)}}
			}
			if _, err := HashCodexLocalBinding(response.Binding.Codex.DeclarationSha256, local); err != nil {
				test.Fatal("unneeded file fact overrides known keyring login")
			}
		}
	}
}
