package state

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
)

type codexStateFlow struct {
	base           nativeConfigurationFlow
	consumer       nativesessionauth.ConsumerFields
	request        nativesessionauth.CodexPrepareRequest
	data           NativeCodexPreparedData
	validator      NativeCodexPrepareValidator
	prepareProof   nativesessionauth.VerifiedApplyProof
	prepared       NativeCodexPrepared
	local          nativesessionauth.CodexLocalBinding
	authorize      nativesessionauth.CodexAuthorizeRequest
	authorizeProof nativesessionauth.VerifiedApplyProof
	grant          NativeCodexAuthorization
	consume        nativesessionauth.ConsumeRequest
	consumeProof   nativesessionauth.VerifiedApplyProof
	token          []byte
}

func newCodexStateFlow(test *testing.T) *codexStateFlow {
	test.Helper()
	base := newNativeConfigurationFlow(test)
	if _, err := base.fixture.store.state.client.NativeApplyPreparation.Delete().Exec(test.Context()); err != nil {
		test.Fatal("cannot reset unrelated preparation")
	}
	return newCodexStateFlowWithBase(test, base)
}

func newCodexStateFlowWithBase(test *testing.T, base nativeConfigurationFlow) *codexStateFlow {
	test.Helper()
	asset, err := base.fixture.store.CreateConfigurationAsset(test.Context(), "alice", ConfigurationAssetDraft{AgentID: "codex", Kind: ConfigurationKindProvider, Name: "synthetic-codex", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic-c2-slot-key"))})
	if err != nil {
		test.Fatal("cannot create encrypted asset")
	}
	snapshot, err := base.fixture.store.NativeCodexConfigurationSnapshot(test.Context(), base.credential, base.requestOrigin(), base.request.OperationID, "")
	if err != nil {
		test.Fatal("cannot capture actual consumer")
	}
	consumer := snapshot.Consumer()
	request := nativesessionauth.CodexPrepareRequest{PrepareRequest: base.request, ContractVersion: 1}
	request.AssetID = asset.ID
	request.AgentID = "codex"
	request.Target = 2
	request.Nonce = [32]byte(bytes.Repeat([]byte{6}, 32))
	request.Adapter = nativesessionauth.AdapterFields{APIVersion: 1, PluginID: "builtin.codex", PluginVersion: "1.1.0", DefinitionDigest: "f45d146803f9f4f818a5d17ea1cc536b27bc77f49eb059c10c2f305912af02b4", Capability: "codex.provider-plan.v1"}
	encoded, err := os.ReadFile("../../configclient/testdata/native-codex-proof-v1.json")
	var literal struct {
		Declaration agentconfig.CodexProviderDeclarationV1 `json:"declaration"`
	}
	if err != nil || json.Unmarshal(encoded, &literal) != nil {
		test.Fatal("cannot load declaration literal")
	}
	data, err := NewNativeCodexPreparedData(request.Adapter, literal.Declaration)
	if err != nil {
		test.Fatal("typed declaration rejected")
	}
	flow := &codexStateFlow{base: base, consumer: consumer, request: request, data: data}
	flow.validator = func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativeCodexPreparedData, error) {
		return flow.data, nil
	}
	flow.prepareProof = flow.prepareEvidence(test, request)
	return flow
}

func (flow *codexStateFlow) prepareEvidence(test *testing.T, request nativesessionauth.CodexPrepareRequest) nativesessionauth.VerifiedApplyProof {
	test.Helper()
	consumer := flow.consumer
	hash := sha256.Sum256(consumer.PublicKey[:])
	wire := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: request.OwnerID, AssetId: request.AssetID, ExpectedAssetRevision: request.AssetRevision, AgentId: "codex", SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: request.Adapter.PluginID, PluginVersion: request.Adapter.PluginVersion, DefinitionDigest: request.Adapter.DefinitionDigest, RequiredCapabilities: []string{request.Adapter.Capability}}, Target: 2, Scope: 1, ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, OperationId: request.OperationID[:], ClientNonce: request.Nonce[:], CodexContractVersion: 1}
	transcript, err := configclient.CodexPrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: hash[:]}, wire)
	if err != nil {
		test.Fatal("client prepare builder rejected")
	}
	proof, err := flow.base.verifier.VerifyCodexPrepare(flow.base.bearer, nativesessionauth.CodexPrepareFields{Consumer: consumer, Request: request}, ed25519.Sign(flow.base.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal("real prepare proof failed")
	}
	return proof
}
func (flow *codexStateFlow) prepare(test *testing.T) {
	test.Helper()
	var err error
	flow.prepared, err = flow.base.fixture.store.PrepareNativeCodexApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.request, flow.prepareProof, flow.validator)
	if err != nil {
		test.Fatal("state prepare failed")
	}
	golden, consumer := codexRecordFixture(test)
	literal, _ := hex.DecodeString(golden.Compact["bound"].Hex)
	projection, err := nativeDecodeCodexRecord(literal, consumer)
	if err != nil {
		test.Fatal("cannot load synthetic projection")
	}
	prepare := flow.prepared.Binding()
	flow.local, err = nativesessionauth.NewCodexLocalBinding([32]byte(prepare.CopyWire().Codex.DeclarationSha256), projection.apply.LocalBinding().CopyWire())
	if err != nil {
		test.Fatal("cannot bind synthetic local projection to actual complete declaration")
	}
	expiry := prepare.CopyWire().PrepareExpiresAtUnixMs
	commitment, err := configclient.HashCodexIntent(prepare.CopyWire(), flow.local.CopyWire(), 1, expiry)
	if err != nil {
		test.Fatal("client intent failed")
	}
	flow.authorize = nativesessionauth.CodexAuthorizeRequest{AuthorizeRequest: nativesessionauth.AuthorizeRequest{PrepareID: prepare.CopyWire().PrepareId, OperationID: flow.request.OperationID, Commitment: commitment, RequiredSlot: 3, Consent: nativesessionauth.ConsentFields{Kind: 1, Revision: 1}, Nonce: [32]byte(bytes.Repeat([]byte{9}, 32)), IntentExpiresMS: expiry}, Local: flow.local}
	challenge := flow.prepared.Challenge()
	wire := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: flow.authorize.PrepareID, OperationId: flow.authorize.OperationID[:], IntentCommitment: commitment[:], RequestedSlots: []nativev1.SecretSlot{3}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: flow.authorize.Nonce[:], IntentExpiresAtUnixMs: expiry, CodexLocalBinding: flow.local.CopyWire()}
	transcript, err := configclient.CodexAuthorizeTranscript(prepare.CopyWire(), challenge[:], wire)
	if err != nil {
		test.Fatal("client authorize builder failed")
	}
	flow.authorizeProof, err = flow.base.verifier.VerifyCodexAuthorize(flow.base.bearer, nativesessionauth.CodexAuthorizeFields{Preparation: prepare, Local: flow.local, Challenge: challenge, Request: flow.authorize.AuthorizeRequest}, ed25519.Sign(flow.base.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal("real authorization proof failed")
	}
}
func (flow *codexStateFlow) issue(test *testing.T) {
	test.Helper()
	flow.token = bytes.Repeat([]byte{7}, 32)
	token, _ := nativesessionauth.ParseGrantToken(flow.token)
	var err error
	flow.grant, err = flow.base.fixture.store.AuthorizeNativeCodexGrant(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.authorize, flow.authorizeProof, token, flow.validator)
	if err != nil {
		test.Fatal("state authorize failed")
	}
	flow.consume = nativesessionauth.ConsumeRequest{PrepareID: flow.authorize.PrepareID, GrantID: flow.grant.ID(), Token: token, OperationID: flow.request.OperationID, Commitment: flow.authorize.Commitment, Nonce: [32]byte(bytes.Repeat([]byte{12}, 32))}
	authorize, consume := flow.prepared.Challenge(), flow.grant.Challenge()
	wire := &nativev1.ConsumeNativeMaterializationRequest{PrepareId: flow.consume.PrepareID, GrantId: flow.consume.GrantID, GrantToken: flow.token, OperationId: flow.consume.OperationID[:], IntentCommitment: flow.consume.Commitment[:], ClientNonce: flow.consume.Nonce[:]}
	transcript, err := configclient.CodexConsumeTranscript(flow.grant.Binding().CopyWire(), authorize[:], consume[:], flow.grant.ExpiresMS(), wire)
	if err != nil {
		test.Fatal("client consume builder failed")
	}
	flow.consumeProof, err = flow.base.verifier.VerifyCodexConsume(flow.base.bearer, nativesessionauth.CodexConsumeFields{Apply: flow.grant.Binding(), AuthorizeChallenge: authorize, ConsumeChallenge: consume, GrantExpiresMS: flow.grant.ExpiresMS(), Request: flow.consume}, ed25519.Sign(flow.base.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal("real consume proof failed")
	}
}
func codexStateMaterializer(_ context.Context, _ ConfigurationAsset, payload ConfigurationPayload, _ nativesessionauth.CodexApply, _ string, _ int64) (NativeMaterialization, error) {
	body := payload.Reveal()
	defer clear(body)
	return NewNativeMaterialization(body)
}

func TestCodexNativeStoreLifecycle(test *testing.T) {
	flow := newCodexStateFlow(test)
	flow.prepare(test)
	store := flow.base.fixture.store
	first := flow.prepared.Binding().CopyWire()
	duplicate, err := store.PrepareNativeCodexApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.request, flow.prepareProof, flow.validator)
	if err != nil || duplicate.Binding().CopyWire().PrepareId != first.PrepareId || duplicate.Challenge() != flow.prepared.Challenge() {
		test.Fatal("same original nonce was not idempotent")
	}
	different := flow.request
	different.Nonce[0] ^= 1
	if result, err := store.PrepareNativeCodexApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, different, flow.prepareEvidence(test, different), flow.validator); !errors.Is(err, ErrConfigurationConflict) || result.Binding().CopyWire() != nil {
		test.Fatal("different original nonce did not conflict")
	}
	flow.issue(test)
	if flow.grant.ID() == "" || flow.grant.ExpiresMS() > store.state.now().Add(time.Minute).UnixMilli() {
		test.Fatal("grant lifetime invalid")
	}
	result, err := store.ConsumeNativeCodexGrant(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof, codexStateMaterializer)
	if err != nil || !bytes.Equal(result.Reveal(), []byte("synthetic-c2-slot-key")) {
		test.Fatal("single-slot materialization failed")
	}
	result.Destroy()
	if repeated, err := store.ConsumeNativeCodexGrant(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof, codexStateMaterializer); !errors.Is(err, ErrNativePrecondition) || len(repeated.Reveal()) != 0 {
		test.Fatal("materialization replay succeeded")
	}
	request := nativesessionauth.StatusRequest{PrepareID: first.PrepareId, OperationID: flow.request.OperationID, Nonce: [32]byte(bytes.Repeat([]byte{13}, 32))}
	challenge := flow.prepared.Challenge()
	transcript, err := configclient.CodexStatusTranscript(first, challenge[:], &nativev1.GetNativeMaterializationStatusRequest{PrepareId: request.PrepareID, OperationId: request.OperationID[:], ClientNonce: request.Nonce[:]})
	if err != nil {
		test.Fatal("status builder failed")
	}
	proof, err := flow.base.verifier.VerifyCodexStatus(flow.base.bearer, nativesessionauth.CodexStatusFields{Preparation: flow.prepared.Binding(), Challenge: challenge, Request: request}, ed25519.Sign(flow.base.key, transcript.CopyBytes()))
	status, err := store.NativeCodexGrantStatus(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, request, proof)
	if err != nil || status.State != 3 || status.GrantID != flow.grant.ID() {
		test.Fatal("safe Status lost consumed fact")
	}
}

func TestCodexNativePreflightIsolation(test *testing.T) {
	flow := newCodexStateFlow(test)
	store := flow.base.fixture.store
	callbacks := 0
	validate := func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativeCodexPreparedData, error) {
		callbacks++
		return flow.data, nil
	}
	if result, err := store.PrepareNativeCodexApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.request, nativesessionauth.VerifiedApplyProof{}, validate); !errors.Is(err, ErrNativeUnauthorized) || result.Binding().CopyWire() != nil || callbacks != 0 {
		test.Fatal("invalid prepare proof reached asset validator")
	}
	if store.CreateUser(test.Context(), "bob") != nil {
		test.Fatal("cannot create second owner")
	}
	foreign, err := store.CreateConfigurationAsset(test.Context(), "bob", ConfigurationAssetDraft{AgentID: "codex", Kind: ConfigurationKindProvider, Name: "private-other-owner", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic-other-owner-key"))})
	if err != nil {
		test.Fatal("cannot create foreign encrypted asset")
	}
	foreignRequest := flow.request
	foreignRequest.AssetID = foreign.ID
	if result, err := store.PrepareNativeCodexApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, foreignRequest, nativesessionauth.VerifiedApplyProof{}, validate); !errors.Is(err, ErrNativeUnauthorized) || result.Binding().CopyWire() != nil || callbacks != 0 {
		test.Fatal("foreign asset existence influenced bad-proof result")
	}
	if result, err := store.PrepareNativeCodexApply(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, foreignRequest, flow.prepareEvidence(test, foreignRequest), validate); !errors.Is(err, ErrConfigurationNotFound) || result.Binding().CopyWire() != nil || callbacks != 0 {
		test.Fatal("owner scope reached another owner's validator")
	}
	flow.prepare(test)
	token, _ := nativesessionauth.ParseGrantToken(bytes.Repeat([]byte{7}, 32))
	if result, err := store.AuthorizeNativeCodexGrant(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.authorize, nativesessionauth.VerifiedApplyProof{}, token, validate); !errors.Is(err, ErrNativeUnauthorized) || result.ID() != "" || callbacks != 0 {
		test.Fatal("invalid authorize proof reached validator")
	}
	flow.issue(test)
	materialize := func(context.Context, ConfigurationAsset, ConfigurationPayload, nativesessionauth.CodexApply, string, int64) (NativeMaterialization, error) {
		callbacks++
		return NewNativeMaterialization([]byte("unsafe"))
	}
	if result, err := store.ConsumeNativeCodexGrant(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, nativesessionauth.VerifiedApplyProof{}, materialize); !errors.Is(err, ErrNativeUnauthorized) || len(result.Reveal()) != 0 || callbacks != 0 {
		test.Fatal("invalid consume proof reached materializer")
	}
	if snapshot, err := store.PreflightNativeCodexConsume(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof); err != nil || snapshot.Consumer() != flow.consumer {
		test.Fatal("invalid attempt burned legitimate grant")
	}
}

func TestCodexNativeCASAndRevocation(test *testing.T) {
	for _, stage := range []string{"authorize-cas", "consume-cas", "consume-revoke"} {
		test.Run(stage, func(test *testing.T) {
			flow := newCodexStateFlow(test)
			flow.prepare(test)
			store := flow.base.fixture.store
			if stage != "authorize-cas" {
				flow.issue(test)
			}
			if stage == "consume-revoke" {
				if store.RevokeSession(test.Context(), flow.base.fixture.digest[:]) != nil {
					test.Fatal("cannot revoke actual parent")
				}
			} else {
				_, err := store.UpdateConfigurationAsset(test.Context(), "alice", flow.request.AssetID, flow.request.AssetRevision, ConfigurationAssetDraft{AgentID: "codex", Kind: ConfigurationKindProvider, Name: "changed", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("different-new-revision-secret"))})
				if err != nil {
					test.Fatal("actual CAS setup failed")
				}
			}
			callbacks := 0
			if stage == "authorize-cas" {
				token, _ := nativesessionauth.ParseGrantToken(bytes.Repeat([]byte{7}, 32))
				result, err := store.AuthorizeNativeCodexGrant(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.authorize, flow.authorizeProof, token, func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativeCodexPreparedData, error) {
					callbacks++
					return flow.data, nil
				})
				if !errors.Is(err, ErrConfigurationConflict) || result.ID() != "" || callbacks != 0 {
					test.Fatal("authorize CAS failed to preserve revision before validator")
				}
			} else {
				result, err := store.ConsumeNativeCodexGrant(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof, func(context.Context, ConfigurationAsset, ConfigurationPayload, nativesessionauth.CodexApply, string, int64) (NativeMaterialization, error) {
					callbacks++
					return NewNativeMaterialization([]byte("unsafe"))
				})
				expected := ErrConfigurationConflict
				if stage == "consume-revoke" {
					expected = ErrNativeUnauthorized
				}
				if !errors.Is(err, expected) || len(result.Reveal()) != 0 || callbacks != 0 {
					test.Fatal("revoked/stale consumption reached materializer")
				}
			}
		})
	}
}

func TestCodexNativeUnknownCommit(test *testing.T) {
	for _, stage := range []string{"authorize", "consume", "consume-cancel"} {
		test.Run(stage, func(test *testing.T) {
			flow := newCodexStateFlow(test)
			flow.prepare(test)
			if stage != "authorize" {
				flow.issue(test)
			}
			callContext, cancel := context.WithCancel(test.Context())
			defer cancel()
			commits := 0
			flow.base.fixture.store.state.client.NativeMaterializationGrant.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					tx, err := mutation.(*ent.NativeMaterializationGrantMutation).Tx()
					if err != nil {
						return nil, err
					}
					tx.OnCommit(func(next ent.Committer) ent.Committer {
						return ent.CommitFunc(func(ctx context.Context, tx *ent.Tx) error {
							if err := next.Commit(ctx, tx); err != nil {
								return err
							}
							commits++
							if stage == "consume-cancel" {
								cancel()
								return nil
							}
							return errors.New("synthetic-private-lost-receipt")
						})
					})
					return next.Mutate(ctx, mutation)
				})
			})
			if stage == "authorize" {
				token, _ := nativesessionauth.ParseGrantToken(bytes.Repeat([]byte{7}, 32))
				result, err := flow.base.fixture.store.AuthorizeNativeCodexGrant(callContext, flow.base.credential, flow.consumer.ServerOrigin, flow.authorize, flow.authorizeProof, token, flow.validator)
				if err == nil || result.ID() != "" {
					test.Fatal("unknown authorize returned grant")
				}
			} else {
				result, err := flow.base.fixture.store.ConsumeNativeCodexGrant(callContext, flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof, codexStateMaterializer)
				if err == nil || len(result.Reveal()) != 0 {
					test.Fatal("unknown consume returned secret")
				}
			}
			grant, err := flow.base.fixture.store.state.client.NativeMaterializationGrant.Query().Only(test.Context())
			if err != nil || commits != 1 || stage != "authorize" && grant.ConsumedMs == 0 {
				test.Fatal("unknown-commit test did not actually commit exactly once")
			}
		})
	}
}

func TestCodexNativeQuotaPostState(test *testing.T) {
	flow := newCodexStateFlow(test)
	flow.prepare(test)
	store := flow.base.fixture.store
	row, err := store.state.client.NativeApplyPreparation.Query().Only(test.Context())
	if err != nil {
		test.Fatal("cannot read preparation")
	}
	apply, err := nativesessionauth.NewCodexApply(flow.prepared.Binding(), flow.local, flow.authorize.Consent.Revision, flow.authorize.IntentExpiresMS)
	if err != nil {
		test.Fatal("cannot construct expected bound record")
	}
	post := *row
	post.Phase = nativeapplypreparation.PhaseBoundWithGrant
	record := nativeCodexRecord{preparation: flow.prepared.Binding(), apply: apply}
	if store.nativeSealCodexPrepared(&post, flow.consumer, record) != nil || post.Charge <= row.Charge || post.Charge > 13096 {
		test.Fatal("actual bound AEAD charge invalid")
	}
	plaintext, _ := nativeEncodeCodexRecord(record)
	defer clear(plaintext)
	if len(post.Ciphertext) != len(plaintext)+28 || store.state.configurationAEAD.Overhead() != 28 || post.Charge != 512+int64(len(post.PublicID)+16+32+32+len(post.Ciphertext)+len(post.Phase)) {
		test.Fatal("actual AEAD/charge formula changed")
	}
	for _, scope := range []int{0, 1, 2} {
		test.Run([]string{"parent", "owner", "instance"}[scope], func(test *testing.T) {
			budget := nativeDefaultBudget()
			budget.tables[2][scope].bytes = row.Charge
			token, _ := nativesessionauth.ParseGrantToken(bytes.Repeat([]byte{7}, 32))
			result, err := store.authorizeNativeCodexGrant(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.authorize, flow.authorizeProof, token, flow.validator, budget)
			if !errors.Is(err, ErrNativeQuota) || result.ID() != "" {
				test.Fatal("bound preparation replacement bypassed post-state byte quota")
			}
			actual, err := store.state.client.NativeApplyPreparation.Get(test.Context(), row.ID)
			grants, countErr := store.state.client.NativeMaterializationGrant.Query().Count(test.Context())
			if err != nil || countErr != nil || grants != 0 || actual.Phase != row.Phase || actual.Charge != row.Charge || !bytes.Equal(actual.Ciphertext, row.Ciphertext) {
				test.Fatal("quota rejection partially committed authorization")
			}
		})
	}
	flow.issue(test)
	if _, err := store.ConsumeNativeCodexGrant(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof, codexStateMaterializer); err != nil {
		test.Fatal("terminal quota fixture failed")
	}
	_, err = store.nativeTransaction(test.Context(), func(tx *ent.Tx) (struct{}, func(time.Time) error, error) {
		inventory, err := nativeLoadInventory(test.Context(), tx)
		if err != nil {
			return struct{}{}, nil, err
		}
		if len(inventory.prepares) != 1 || len(inventory.grants) != 1 || inventory.grants[0].ConsumedMs == 0 {
			return struct{}{}, nil, ErrNativeUnavailable
		}
		return struct{}{}, nil, nil
	})
	if err != nil {
		test.Fatal("terminal rows were dropped from quota inventory")
	}
}

func TestCodexNativeLargeCatalogCompactBudget(test *testing.T) {
	flow := newCodexStateFlow(test)
	declaration := cloneNativeCodexDeclaration(flow.data.box.value.declaration)
	declaration.Model = "model-00"
	declaration.ReasoningEffort = nil
	declaration.Catalog = new(agentconfig.CodexCatalogDesired)
	for index := range 32 {
		declaration.Catalog.Models = append(declaration.Catalog.Models, agentconfig.CodexCatalogModelDesired{Model: fmt.Sprintf("model-%02d", index), BaseInstructions: new(strings.Repeat("synthetic-large-catalog-", 36)), SupportsParallelToolCalls: new(false)})
	}
	encoded, err := json.Marshal(declaration.Catalog)
	defer clear(encoded)
	if err != nil || len(encoded) < 24<<10 || len(encoded) > 32<<10 {
		test.Fatal("large valid catalog fixture is outside its intended range")
	}
	flow.data, err = NewNativeCodexPreparedData(flow.request.Adapter, declaration)
	if err != nil {
		test.Fatal("legal32model large catalog rejected")
	}
	flow.prepare(test)
	flow.issue(test)
	row, err := flow.base.fixture.store.state.client.NativeApplyPreparation.Query().Only(test.Context())
	if err != nil || row.Charge > 16<<10 || len(row.Ciphertext) > 16<<10 {
		test.Fatal("full catalog expanded authenticated compact beyond16KiB")
	}
	plaintext, err := flow.base.fixture.store.state.configurationAEAD.Open(nil, nil, row.Ciphertext, nativePreparedAAD(row, flow.consumer))
	defer clear(plaintext)
	if err != nil || len(plaintext) > 12396 || bytes.Contains(plaintext, []byte("synthetic-large-catalog-")) || len(row.Ciphertext) != len(plaintext)+28 {
		test.Fatal("compact persisted catalog or lost actualAEAD accounting")
	}
}

func TestCodexNativeTwoStoresLastQuotaSlot(test *testing.T) {
	first := newCodexStateFlow(test)
	first.prepare(test)
	fixture := first.base.fixture
	start := nativeTestStart()
	start.OperationID = bytes.Repeat([]byte{2}, 16)
	start.PublicKey = first.base.key.Public().(ed25519.PublicKey)
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, start)
	if err != nil {
		test.Fatal("cannot create second operation")
	}
	bind := nativeTestBind(challenge, fixture.digest)
	parsed, _ := nativesessionauth.ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)))
	bind.TokenHash, err = fixture.store.NativeCredentialFromBearer(parsed)
	if err != nil {
		test.Fatal("cannot derive actual second bearer credential")
	}
	if _, err := fixture.store.BindNativeConsumer(test.Context(), fixture.parent, bind); err != nil {
		test.Fatal("cannot bind second quota fixture")
	}
	base := first.base
	base.credential = bind.TokenHash
	base.bearer = parsed
	base.request.OperationID = [16]byte(start.OperationID)
	second := newCodexStateFlowWithBase(test, base)
	second.prepare(test)
	peer, err := Open(test.Context(), fixture.path, fixture.key)
	if err != nil {
		test.Fatal("second real Store open failed")
	}
	test.Cleanup(func() { _ = peer.Close() })
	peer.state.now = func() time.Time { return time.Unix(0, fixture.clock.Load()).UTC() }
	second.base.fixture.store = peer
	budget := nativeDefaultBudget()
	budget.tables[3][0].rows = 1
	budget.tables[3][1].rows = 1
	budget.tables[3][2].rows = 1
	startGate := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for index, flow := range []*codexStateFlow{first, second} {
		workers.Go(func() {
			<-startGate
			token, _ := nativesessionauth.ParseGrantToken(bytes.Repeat([]byte{byte(51 + index)}, 32))
			_, err := flow.base.fixture.store.authorizeNativeCodexGrant(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.authorize, flow.authorizeProof, token, flow.validator, budget)
			results <- err
		})
	}
	close(startGate)
	workers.Wait()
	close(results)
	successes, quotas := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrNativeQuota) {
			quotas++
		} else {
			test.Fatal("last-slot competition returned unexpected error")
		}
	}
	count, err := fixture.store.state.client.NativeMaterializationGrant.Query().Count(test.Context())
	if err != nil || successes != 1 || quotas != 1 || count != 1 {
		test.Fatal("two Stores admitted more than one last-slot grant")
	}
}
