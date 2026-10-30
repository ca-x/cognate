package nativesessionauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"google.golang.org/protobuf/proto"
)

func TestCodexNativeSchemaBranches(test *testing.T) {
	request := new(nativev1.PrepareNativeApplyRequest)
	if request.ProtoReflect().Descriptor().Fields().ByNumber(40) == nil {
		test.Fatal("Codex contract version must have its distinct native tag40")
	}
	if nativev1.Target(2).Descriptor().Values().ByNumber(2) == nil || nativev1.SecretSlot(3).Descriptor().Values().ByNumber(3) == nil {
		test.Fatal("Codex target2 and single provider secret slot3 must be declared")
	}
}

type codexGolden struct {
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

func codexFixture(test *testing.T) (codexGolden, ConsumerFields, CodexPrepareRequest, *nativev1.CodexLocalBindingV1) {
	test.Helper()
	encoded, err := os.ReadFile("../configclient/testdata/native-codex-proof-v1.json")
	var fixture codexGolden
	if err != nil || json.Unmarshal(encoded, &fixture) != nil {
		test.Fatal("cannot load literal oracle")
	}
	decode := func(value string) []byte {
		decoded, err := hex.DecodeString(value)
		if err != nil {
			test.Fatal("invalid oracle hex")
		}
		return decoded
	}
	consumer := ConsumerFields{OwnerID: "owner-synthetic", ServerOrigin: "https://proof.example:8443", PublicKey: [32]byte(decode(fixture.PublicKey)), OperationID: [16]byte(bytes.Repeat([]byte{1}, 16)), SessionBinding: [32]byte(bytes.Repeat([]byte{5}, 32))}
	request := CodexPrepareRequest{PrepareRequest: PrepareRequest{OwnerID: consumer.OwnerID, AssetID: string(bytes.Repeat([]byte{'A'}, 26)), AgentID: "codex", ProfileID: "profile-c2", AssetRevision: 7, SchemaVersion: 1, Target: 2, Scope: 1, Adapter: AdapterFields{APIVersion: 1, PluginID: "builtin.codex", PluginVersion: "1.1.0", DefinitionDigest: "f45d146803f9f4f818a5d17ea1cc536b27bc77f49eb059c10c2f305912af02b4", Capability: "codex.provider-plan.v1"}, SelectionRevision: 9, OperationID: consumer.OperationID, Nonce: [32]byte(bytes.Repeat([]byte{6}, 32))}, ContractVersion: 1}
	wire := &nativev1.CodexLocalBindingV1{BindingVersion: 1, NativeProfileId: fixture.Local.Profile, NativeProfileSha256: decode(fixture.Local.ProfileHash), TemplateSha256: decode(fixture.Local.Template), EffectiveScope: 1, ProviderOwnership: 2, CatalogOwnership: 2, ManagedModels: slices.Clone(fixture.Local.Models), AuthStore: 2, KeyringState: 1, FileLoginState: 4, LocalAuthPolicy: "preserve-local-login-v1", RequiresOpenaiAuth: new(true), ObservationWitness: decode(fixture.Local.Witness)}
	for _, baseline := range fixture.Local.Baselines {
		entry := &nativev1.CodexRoleBaseline{Role: nativev1.CodexRole(baseline.Role)}
		switch baseline.Presence {
		case 1:
			entry.State = &nativev1.CodexRoleBaseline_Missing{Missing: new(nativev1.CodexMissing)}
		case 2:
			entry.State = &nativev1.CodexRoleBaseline_Present{Present: &nativev1.CodexPresent{Sha256: decode(baseline.Hash)}}
		case 3:
			entry.State = &nativev1.CodexRoleBaseline_NotObserved{NotObserved: new(nativev1.CodexNotObserved)}
		}
		wire.Baselines = append(wire.Baselines, entry)
	}
	return fixture, consumer, request, wire
}

func TestCodexNativeCanonicalGolden(test *testing.T) {
	fixture, consumer, request, wire := codexFixture(test)
	declarationHash, err := HashCodexDeclaration(fixture.Declaration)
	checkHash := func(name string) func([32]byte, error) {
		return func(actual [32]byte, err error) {
			test.Helper()
			if err != nil || hex.EncodeToString(actual[:]) != fixture.Hashes[name] {
				test.Fatalf("literal %s hash mismatch", name)
			}
		}
	}
	checkHash("declaration")(declarationHash, err)
	preparation, err := NewCodexPreparation(consumer, request, "prepare-synthetic", 1700000000000, 1700000300000, declarationHash)
	if err != nil {
		test.Fatal("preparation rejected")
	}
	local, err := NewCodexLocalBinding(declarationHash, wire)
	if err != nil {
		test.Fatal("binding rejected")
	}
	checkHash("preparation")(HashCodexPreparation(preparation))
	checkHash("local_binding")(HashCodexLocalBinding(local))
	checkHash("intent")(HashCodexIntent(preparation, local, 11, 1700000240000))
	apply, err := NewCodexApply(preparation, local, 11, 1700000240000)
	if err != nil {
		test.Fatal("apply rejected")
	}
	checkHash("apply")(HashCodexApply(preparation, apply.Commitment(), 1700000240000))
	challenge := [32]byte(bytes.Repeat([]byte{11}, 32))
	authorize := CodexAuthorizeFields{Preparation: preparation, Local: local, Challenge: challenge, Request: AuthorizeRequest{PrepareID: "prepare-synthetic", OperationID: consumer.OperationID, Commitment: apply.Commitment(), RequiredSlot: 3, Consent: ConsentFields{Kind: 1, Revision: 11}, Nonce: request.Nonce, IntentExpiresMS: 1700000240000}}
	token, _ := ParseGrantToken(bytes.Repeat([]byte{7}, 32))
	consume := CodexConsumeFields{Apply: apply, AuthorizeChallenge: challenge, ConsumeChallenge: [32]byte(bytes.Repeat([]byte{10}, 32)), GrantExpiresMS: 1700000060000, Request: ConsumeRequest{PrepareID: "prepare-synthetic", GrantID: "grant-synthetic", Token: token, OperationID: consumer.OperationID, Commitment: apply.Commitment(), Nonce: [32]byte(bytes.Repeat([]byte{12}, 32))}}
	status := CodexStatusFields{Preparation: preparation, Challenge: challenge, Request: StatusRequest{PrepareID: "prepare-synthetic", OperationID: consumer.OperationID, Nonce: [32]byte(bytes.Repeat([]byte{13}, 32))}}
	bind, bindErr := bindCanonical(BindFields{ServerOrigin: consumer.ServerOrigin, OwnerID: consumer.OwnerID, Purpose: 1, OperationID: consumer.OperationID, ConsumerPublicKey: consumer.PublicKey, ClientNonce: [32]byte(bytes.Repeat([]byte{3}, 32)), ChallengeID: "challenge-synthetic", Challenge: [32]byte(bytes.Repeat([]byte{4}, 32)), NativeSessionBinding: consumer.SessionBinding, ChallengeExpiresAtUnixMS: 1700000060000, NativeSessionExpiresAtUnixMS: 1700000300000})
	bindSignature, _ := hex.DecodeString(fixture.Vectors["bind"].Signature)
	if bindErr != nil || hex.EncodeToString(bind) != fixture.Vectors["bind"].Hex || !ed25519.Verify(consumer.PublicKey[:], bind, bindSignature) {
		test.Fatal("unchanged Bind literal mismatch")
	}
	encoded := map[string][]byte{}
	encoded["prepare"], err = codexPrepareCanonical(CodexPrepareFields{Consumer: consumer, Request: request})
	if err != nil {
		test.Fatal("prepare rejected")
	}
	encoded["authorize"], err = codexAuthorizeCanonical(authorize)
	if err != nil {
		test.Fatal("authorize rejected")
	}
	encoded["consume"], err = codexConsumeCanonical(consume)
	if err != nil {
		test.Fatal("consume rejected")
	}
	encoded["status"], err = codexStatusCanonical(status)
	if err != nil {
		test.Fatal("status rejected")
	}
	verifier, _ := NewVerifier(bytes.Repeat([]byte{33}, 32))
	bearer, _ := ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32)))
	for name, actual := range encoded {
		vector := fixture.Vectors[name]
		if hex.EncodeToString(actual) != vector.Hex {
			test.Fatalf("literal %s transcript mismatch", name)
		}
		signature, _ := hex.DecodeString(vector.Signature)
		if !ed25519.Verify(consumer.PublicKey[:], actual, signature) {
			test.Fatal("literal Ed25519 mismatch")
		}
		var proof VerifiedApplyProof
		switch name {
		case "prepare":
			proof, err = verifier.VerifyCodexPrepare(bearer, CodexPrepareFields{Consumer: consumer, Request: request}, signature)
		case "authorize":
			proof, err = verifier.VerifyCodexAuthorize(bearer, authorize, signature)
		case "consume":
			proof, err = verifier.VerifyCodexConsume(bearer, consume, signature)
		case "status":
			proof, err = verifier.VerifyCodexStatus(bearer, status, signature)
		}
		digest := sha256.Sum256(blob(blob(nil, []byte("cognate/native-codex-stage-evidence/v1")), actual))
		native, _ := bearer.TokenDigest()
		if err != nil || !proof.MatchesApply(verifier.payload.value.keyID, native, name, digest) {
			test.Fatal("real verifier stage domain mismatch")
		}
		signature[0] ^= 1
		if _, err := verifier.verifyCodex(bearer, name, consumer.PublicKey, actual, signature); err == nil {
			test.Fatal("modified signature accepted")
		}
	}
	fixture.Declaration.Catalog.Models[0].SupportsParallelToolCalls = nil
	absent, _ := HashCodexDeclaration(fixture.Declaration)
	if hex.EncodeToString(absent[:]) != fixture.Negatives["parallel_absent_declaration"] {
		test.Fatal("false presence collapsed")
	}
	fixture.Declaration.Catalog.Models[0].SupportsParallelToolCalls = new(false)
	slices.Reverse(fixture.Declaration.Catalog.Models)
	reordered, _ := HashCodexDeclaration(fixture.Declaration)
	if hex.EncodeToString(reordered[:]) != fixture.Negatives["catalog_reordered_declaration"] {
		test.Fatal("catalog order collapsed")
	}
	copyWire := local.CopyWire()
	copyWire.ManagedModels[0] = "changed"
	copyWire.Baselines[0].GetPresent().Sha256[0] ^= 1
	checkHash("local_binding")(HashCodexLocalBinding(local))
	copyPrep := preparation.CopyWire()
	copyPrep.Codex.DeclarationSha256[0] ^= 1
	checkHash("preparation")(HashCodexPreparation(preparation))
}

func TestCodexNativeLocalPolicyBoundary(test *testing.T) {
	fixture, _, _, wire := codexFixture(test)
	declarationHash, _ := HashCodexDeclaration(fixture.Declaration)
	for _, store := range []nativev1.CodexAuthStore{2, 3} {
		for _, fileLogin := range []nativev1.CodexFileLoginState{1, 2, 3, 4} {
			candidate := proto.Clone(wire).(*nativev1.CodexLocalBindingV1)
			candidate.AuthStore = store
			candidate.FileLoginState = fileLogin
			if fileLogin == 1 {
				candidate.Baselines[1].State = &nativev1.CodexRoleBaseline_Present{Present: &nativev1.CodexPresent{Sha256: bytes.Repeat([]byte{32}, 32)}}
			}
			if _, err := NewCodexLocalBinding(declarationHash, candidate); err != nil {
				test.Fatal("unused file observation rejected despite known keyring login")
			}
		}
	}
	profile := proto.Clone(wire).(*nativev1.CodexLocalBindingV1)
	profile.NativeProfileId = "signed.profile:" + strings.Repeat("a", 113)
	if len(profile.NativeProfileId) != 128 {
		test.Fatal("invalid boundary fixture")
	}
	if _, err := NewCodexLocalBinding(declarationHash, profile); err != nil {
		test.Fatal("legal 128-byte profile rejected")
	}
	mutations := map[string]func(*nativev1.CodexLocalBindingV1){
		"unknown-store":      func(value *nativev1.CodexLocalBindingV1) { value.AuthStore = 0 },
		"unknown-keyring":    func(value *nativev1.CodexLocalBindingV1) { value.AuthStore = 3; value.KeyringState = 3 },
		"foreign-provider":   func(value *nativev1.CodexLocalBindingV1) { value.ProviderOwnership = 3 },
		"missing-login-fact": func(value *nativev1.CodexLocalBindingV1) { value.RequiresOpenaiAuth = nil },
		"opposite-login":     func(value *nativev1.CodexLocalBindingV1) { value.RequiresOpenaiAuth = new(false) },
		"unknown-config": func(value *nativev1.CodexLocalBindingV1) {
			value.Baselines[0].State = &nativev1.CodexRoleBaseline_NotObserved{NotObserved: new(nativev1.CodexNotObserved)}
		},
		"short-baseline": func(value *nativev1.CodexLocalBindingV1) {
			value.Baselines[0].GetPresent().Sha256 = value.Baselines[0].GetPresent().Sha256[:31]
		},
		"role-order": func(value *nativev1.CodexLocalBindingV1) {
			value.Baselines[1], value.Baselines[2] = value.Baselines[2], value.Baselines[1]
		},
		"empty-witness":   func(value *nativev1.CodexLocalBindingV1) { clear(value.ObservationWitness) },
		"duplicate-model": func(value *nativev1.CodexLocalBindingV1) { value.ManagedModels[1] = value.ManagedModels[0] },
		"nil-union": func(value *nativev1.CodexLocalBindingV1) {
			value.Baselines[1].State = &nativev1.CodexRoleBaseline_NotObserved{}
		},
		"unknown-nested": func(value *nativev1.CodexLocalBindingV1) {
			value.Baselines[1].GetNotObserved().ProtoReflect().SetUnknown([]byte{8, 0})
		},
	}
	for name, mutate := range mutations {
		test.Run(name, func(test *testing.T) {
			candidate := proto.Clone(wire).(*nativev1.CodexLocalBindingV1)
			mutate(candidate)
			result, err := NewCodexLocalBinding(declarationHash, candidate)
			if err == nil || result.CopyWire() != nil {
				test.Fatal("invalid local binding accepted")
			}
		})
	}
}

func TestCodexNativeEveryLocalFactBound(test *testing.T) {
	fixture, consumer, request, wire := codexFixture(test)
	digest, _ := HashCodexDeclaration(fixture.Declaration)
	prepared, _ := NewCodexPreparation(consumer, request, "prepare-synthetic", 1700000000000, 1700000300000, digest)
	local, _ := NewCodexLocalBinding(digest, wire)
	original, _ := HashCodexLocalBinding(local)
	intent, _ := HashCodexIntent(prepared, local, 11, 1700000240000)
	for name, mutate := range map[string]func(*nativev1.CodexLocalBindingV1){
		"profile":         func(value *nativev1.CodexLocalBindingV1) { value.NativeProfileId += "-different" },
		"manifest":        func(value *nativev1.CodexLocalBindingV1) { value.NativeProfileSha256[0] ^= 1 },
		"template":        func(value *nativev1.CodexLocalBindingV1) { value.TemplateSha256[0] ^= 1 },
		"witness":         func(value *nativev1.CodexLocalBindingV1) { value.ObservationWitness[0] ^= 1 },
		"config-baseline": func(value *nativev1.CodexLocalBindingV1) { value.Baselines[0].GetPresent().Sha256[0] ^= 1 },
		"auth-baseline": func(value *nativev1.CodexLocalBindingV1) {
			value.Baselines[1].State = &nativev1.CodexRoleBaseline_Missing{Missing: new(nativev1.CodexMissing)}
		},
		"ownership":     func(value *nativev1.CodexLocalBindingV1) { value.ProviderOwnership = 1 },
		"managed-order": func(value *nativev1.CodexLocalBindingV1) { slices.Reverse(value.ManagedModels) },
	} {
		test.Run(name, func(test *testing.T) {
			candidate := proto.Clone(wire).(*nativev1.CodexLocalBindingV1)
			mutate(candidate)
			changed, err := NewCodexLocalBinding(digest, candidate)
			if err != nil {
				test.Fatal("valid binding variation rejected")
			}
			bound, err := HashCodexLocalBinding(changed)
			if err != nil || bound == original {
				test.Fatal("complete local fact omitted from HB")
			}
			actual, err := HashCodexIntent(prepared, changed, 11, 1700000240000)
			if err != nil || actual == intent {
				test.Fatal("complete local fact omitted from HI")
			}
		})
	}
	apply, _ := NewCodexApply(prepared, local, 11, 1700000240000)
	requestAuthorize := AuthorizeRequest{PrepareID: "prepare-synthetic", OperationID: consumer.OperationID, Commitment: apply.Commitment(), RequiredSlot: 3, Consent: ConsentFields{Kind: 1, Revision: 11}, IntentExpiresMS: 1700000240000}
	fields := CodexAuthorizeFields{Preparation: prepared, Local: local, Request: requestAuthorize}
	if _, err := CodexAuthorizeFieldsDigest(fields); err != nil {
		test.Fatal("complete authorize positive control failed")
	}
	fields.Request.Commitment[0] ^= 1
	if value, err := CodexAuthorizeFieldsDigest(fields); err == nil || value != [32]byte{} {
		test.Fatal("caller HI was accepted without independent recomputation")
	}
}

func TestCodexNativePrivateContainers(test *testing.T) {
	fixture, consumer, request, wire := codexFixture(test)
	digest, _ := HashCodexDeclaration(fixture.Declaration)
	preparation, _ := NewCodexPreparation(consumer, request, "prepare-synthetic", 1700000000000, 1700000300000, digest)
	local, _ := NewCodexLocalBinding(digest, wire)
	apply, _ := NewCodexApply(preparation, local, 11, 1700000240000)
	values := []any{preparation, &preparation, local, &local, apply, &apply, struct{ hidden CodexPreparation }{preparation}, struct{ hidden CodexLocalBinding }{local}, struct{ hidden CodexApply }{apply}, []CodexApply{apply}, map[string]CodexLocalBinding{"hidden": local}}
	needles := []string{consumer.OwnerID, consumer.ServerOrigin, request.ProfileID, "synthetic-codec-profile-v1", hex.EncodeToString(digest[:]), "117 86 4 218", "1700000300000"}
	for index, value := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%b", "%t", "%f", "%e", "%E", "%g", "%G", "%o", "%O", "%c", "%U", "%020x", "%.80s", "%+q", "%#08v"} {
			output := fmt.Sprintf(verb, value)
			for _, needle := range needles {
				if strings.Contains(output, needle) {
					test.Fatal("opaque private-container formatting leaks")
				}
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil && index < 6 {
			test.Fatal("redacted JSON failed")
		}
		if err != nil {
			for _, needle := range needles {
				if strings.Contains(err.Error(), needle) {
					test.Fatal("JSON error leaks")
				}
			}
		}
		for _, needle := range needles {
			if bytes.Contains(encoded, []byte(needle)) {
				test.Fatal("opaque JSON leaks")
			}
		}
		var output bytes.Buffer
		slog.New(slog.NewTextHandler(&output, nil)).Info("safe", "value", value)
		for _, needle := range needles {
			if strings.Contains(output.String(), needle) {
				test.Fatal("opaque slog leaks")
			}
		}
	}
}

func TestCodexNativeCanonicalValidation(test *testing.T) {
	mutations := map[string]func(*agentconfig.CodexProviderDeclarationV1){
		"api": func(value *agentconfig.CodexProviderDeclarationV1) { value.APIVersion = 2 },
		"slot": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.RequiredSlots = []agentconfig.CodexSecretSlot{"claude.api-key"}
		},
		"zero-slot": func(value *agentconfig.CodexProviderDeclarationV1) { value.RequiredSlots = nil },
		"duplicate-slot": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.RequiredSlots = append(value.RequiredSlots, value.RequiredSlots[0])
		},
		"reference": func(value *agentconfig.CodexProviderDeclarationV1) { value.CredentialReference = "different" },
		"policy":    func(value *agentconfig.CodexProviderDeclarationV1) { value.LocalAuthPolicy = "resolved-false" },
		"endpoint-user": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Endpoint = "https://secret@provider.invalid"
		},
		"endpoint-port":    func(value *agentconfig.CodexProviderDeclarationV1) { value.Endpoint = "https://provider.invalid:65536" },
		"endpoint-query":   func(value *agentconfig.CodexProviderDeclarationV1) { value.Endpoint += "?" },
		"endpoint-control": func(value *agentconfig.CodexProviderDeclarationV1) { value.Endpoint = "https://provider.invalid/%00" },
		"empty-catalog":    func(value *agentconfig.CodexProviderDeclarationV1) { value.Catalog.Models = nil },
		"duplicate-model":  func(value *agentconfig.CodexProviderDeclarationV1) { value.Catalog.Models[1].Model = "alpha" },
		"invalid-model":    func(value *agentconfig.CodexProviderDeclarationV1) { value.Model = " alpha" },
		"absent-selected":  func(value *agentconfig.CodexProviderDeclarationV1) { value.Model = "gamma" },
		"null-modalities": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].InputModalities = new([]agentconfig.CodexModality(nil))
		},
		"duplicate-modalities": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].InputModalities = new([]agentconfig.CodexModality{"text", "text"})
		},
		"bad-effort": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.ReasoningEffort = new(agentconfig.CodexReasoningEffort(""))
		},
		"unlisted-effort": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.ReasoningEffort = new(agentconfig.CodexReasoningEffort("medium"))
		},
		"default-without-levels": func(value *agentconfig.CodexProviderDeclarationV1) { value.Catalog.Models[0].ReasoningLevels = nil },
		"zero-context": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].ContextWindow = new(uint64(0))
		},
		"huge-context": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].ContextWindow = new(uint64(2147483648))
		},
		"empty-instructions": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].BaseInstructions = new("")
		},
		"bad-instructions": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models[0].BaseInstructions = new("bad\x00")
		},
		"model-count": func(value *agentconfig.CodexProviderDeclarationV1) {
			value.Catalog.Models = make([]agentconfig.CodexCatalogModelDesired, 33)
		},
		"catalog-total": func(value *agentconfig.CodexProviderDeclarationV1) {
			for index := range 20 {
				value.Catalog.Models = append(value.Catalog.Models, agentconfig.CodexCatalogModelDesired{Model: fmt.Sprintf("model%d", index), BaseInstructions: new(strings.Repeat("x", 2048))})
			}
		},
	}
	for name, mutate := range mutations {
		test.Run(name, func(test *testing.T) {
			fixture, _, _, _ := codexFixture(test)
			mutate(&fixture.Declaration)
			hash, err := HashCodexDeclaration(fixture.Declaration)
			if err == nil || hash != [32]byte{} {
				test.Fatal("invalid A declaration accepted")
			}
		})
	}
	fixture, _, _, _ := codexFixture(test)
	fixture.Declaration.Catalog.Models[0].BaseInstructions = new(" \r\n\t ")
	if _, err := HashCodexDeclaration(fixture.Declaration); err != nil {
		test.Fatal("legal raw instruction whitespace rejected")
	}
}
