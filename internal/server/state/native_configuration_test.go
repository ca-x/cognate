package state

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
	"github.com/ca-x/cognate/internal/server/ent/nativeconsumerchallenge"
	"github.com/ca-x/cognate/internal/server/ent/nativeconsumersession"
	"github.com/ca-x/cognate/internal/server/ent/session"
)

type nativeConfigurationFlow struct {
	fixture       nativeFixture
	credential    NativeConsumerCredential
	bearer        nativesessionauth.ParsedNativeBearer
	verifier      *nativesessionauth.Verifier
	key           ed25519.PrivateKey
	request       nativesessionauth.PrepareRequest
	prepared      NativePrepared
	authorization NativeAuthorization
	token         []byte
	validator     NativePrepareValidator
	proof         nativesessionauth.VerifiedApplyProof
	consume       nativesessionauth.ConsumeRequest
}

func nativeFlowWire(binding nativesessionauth.PreparationFields) *nativev1.PreparationBinding {
	consumer, request := binding.Consumer, binding.Request
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	var slots []nativev1.SecretSlot
	if binding.RequiredSlot != 0 {
		slots = []nativev1.SecretSlot{nativev1.SecretSlot(binding.RequiredSlot)}
	}
	return &nativev1.PreparationBinding{OwnerId: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationId: consumer.OperationID[:], AssetId: request.AssetID, AssetRevision: request.AssetRevision, AgentId: request.AgentID, AssetKind: 1, SchemaVersion: request.SchemaVersion, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: request.Adapter.PluginID, PluginVersion: request.Adapter.PluginVersion, DefinitionDigest: request.Adapter.DefinitionDigest, RequiredCapabilities: []string{request.Adapter.Capability}}, Target: 1, Scope: 1, ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, RequiredSlots: slots, PrepareExpiresAtUnixMs: binding.ExpiresMS, ConsumerKeySha256: keyHash[:], PrepareId: binding.PrepareID, PreparedAtUnixMs: binding.PreparedMS, PatchSha256: binding.PatchSHA256[:]}
}

func newNativeConfigurationFlow(test *testing.T) nativeConfigurationFlow {
	test.Helper()
	fixture := newNativeFixture(test)
	parent, ownerProof := seamParent(test, fixture, "alice", 7)
	fixture.parent = parent
	fixture.digest, _ = ownerProof.TokenDigest()
	challenge, key := seamChallenge(test, fixture, parent)
	fields, _ := challenge.BindFields()
	verifier, _ := nativesessionauth.NewVerifier(fixture.key)
	verifiedBind, err := verifier.VerifyBind(ownerProof, fields, seamSignature(fields, key))
	if err != nil {
		test.Fatal(err)
	}
	bindProof, err := fixture.store.BindingProofFromVerified(parent, challenge, verifiedBind)
	if err != nil {
		test.Fatal(err)
	}
	bearer, _ := nativesessionauth.ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{31}, 32)))
	credential, _ := fixture.store.NativeCredentialFromBearer(bearer)
	if _, err := fixture.store.BindNativeConsumer(test.Context(), parent, NativeConsumerBind{ChallengeID: fields.ChallengeID, TokenHash: credential, Proof: bindProof}); err != nil {
		test.Fatal(err)
	}
	asset, err := fixture.store.CreateConfigurationAsset(test.Context(), "alice", ConfigurationAssetDraft{AgentID: "claude", Kind: ConfigurationKindProvider, Name: "synthetic", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic-state-secret-6d0"))})
	if err != nil {
		test.Fatal(err)
	}
	adapter := nativesessionauth.AdapterFields{APIVersion: 1, PluginID: "builtin.claude", PluginVersion: "1.1.0", DefinitionDigest: string(bytes.Repeat([]byte{'a'}, 64)), Capability: "claude.provider-switch.v1"}
	request := nativesessionauth.PrepareRequest{OwnerID: "alice", AssetID: asset.ID, AssetRevision: 1, AgentID: "claude", SchemaVersion: 1, Adapter: adapter, Target: 1, Scope: 1, ProfileID: "profile", SelectionRevision: 1, OperationID: fields.OperationID}
	consumer := nativesessionauth.ConsumerFields{OwnerID: "alice", ServerOrigin: fields.ServerOrigin, SessionBinding: fields.NativeSessionBinding, OperationID: fields.OperationID, PublicKey: fields.ConsumerPublicKey}
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	wire := nativeFlowWire(nativesessionauth.PreparationFields{Consumer: consumer, Request: request})
	transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: "alice", ServerOrigin: fields.ServerOrigin, NativeSessionBinding: fields.NativeSessionBinding[:], OperationID: fields.OperationID[:], ConsumerKeySHA256: keyHash[:]}, &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: "alice", AssetId: asset.ID, ExpectedAssetRevision: 1, AgentId: "claude", SchemaVersion: 1, Adapter: wire.Adapter, Target: 1, Scope: 1, ProfileId: "profile", SelectionRevision: 1, OperationId: fields.OperationID[:], ClientNonce: request.Nonce[:]})
	if err != nil {
		test.Fatal(err)
	}
	proof, err := verifier.VerifyPrepare(bearer, nativesessionauth.PrepareFields{Consumer: consumer, Request: request}, ed25519.Sign(key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal(err)
	}
	var patch nativesessionauth.PatchFields
	for index := range patch {
		patch[index].Action = 3
	}
	patch[0] = nativesessionauth.PatchOperation{Action: 2, SecretSlot: 1}
	validate := func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativePreparedData, error) {
		return NativePreparedData{Adapter: adapter, Patch: patch, RequiredSlot: 1}, nil
	}
	prepared, err := fixture.store.PrepareNativeConfigurationApply(test.Context(), credential, fields.ServerOrigin, request, proof, validate)
	if err != nil {
		test.Fatal(err)
	}
	flow := nativeConfigurationFlow{fixture: fixture, credential: credential, bearer: bearer, verifier: verifier, key: key, request: request, prepared: prepared, validator: validate}
	return flow
}

func (flow *nativeConfigurationFlow) authorize(test *testing.T) {
	test.Helper()
	request, proof := flow.authorizeEvidence(test)
	flow.token = bytes.Repeat([]byte{19}, 32)
	parsed, _ := nativesessionauth.ParseGrantToken(flow.token)
	var err error
	flow.authorization, err = flow.fixture.store.AuthorizeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), request, proof, parsed, flow.validator)
	if err != nil {
		test.Fatal(err)
	}
	flow.consume = nativesessionauth.ConsumeRequest{PrepareID: request.PrepareID, GrantID: flow.authorization.ID(), Token: parsed, OperationID: request.OperationID, Commitment: request.Commitment}
	apply := flow.authorization.Binding()
	consumeChallenge := flow.authorization.Challenge()
	challenge := flow.prepared.Challenge()
	transcript, err := configclient.ConsumeTranscript(&nativev1.ApplyBinding{Preparation: nativeFlowWire(apply.Preparation), IntentCommitment: apply.Commitment[:], IntentExpiresAtUnixMs: apply.IntentExpiresMS}, challenge[:], consumeChallenge[:], flow.authorization.ExpiresMS(), &nativev1.ConsumeNativeMaterializationRequest{PrepareId: request.PrepareID, GrantId: flow.consume.GrantID, GrantToken: flow.token, OperationId: request.OperationID[:], IntentCommitment: request.Commitment[:], ClientNonce: flow.consume.Nonce[:]})
	if err != nil {
		test.Fatal(err)
	}
	flow.proof, err = flow.verifier.VerifyConsume(flow.bearer, nativesessionauth.ConsumeFields{Apply: apply, AuthorizeChallenge: challenge, ConsumeChallenge: consumeChallenge, GrantExpiresMS: flow.authorization.ExpiresMS(), Request: flow.consume}, ed25519.Sign(flow.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal(err)
	}
}

func (flow nativeConfigurationFlow) authorizeEvidence(test *testing.T) (nativesessionauth.AuthorizeRequest, nativesessionauth.VerifiedApplyProof) {
	test.Helper()
	request := nativesessionauth.AuthorizeRequest{PrepareID: flow.prepared.Binding().PrepareID, OperationID: flow.request.OperationID, RequiredSlot: 1, Consent: nativesessionauth.ConsentFields{Kind: 1, Revision: 1}, IntentExpiresMS: flow.prepared.Binding().ExpiresMS}
	challenge := flow.prepared.Challenge()
	transcript, err := configclient.AuthorizeTranscript(nativeFlowWire(flow.prepared.Binding()), challenge[:], &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: request.PrepareID, OperationId: request.OperationID[:], IntentCommitment: request.Commitment[:], RequestedSlots: []nativev1.SecretSlot{1}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: request.Nonce[:], IntentExpiresAtUnixMs: request.IntentExpiresMS})
	if err != nil {
		test.Fatal(err)
	}
	proof, err := flow.verifier.VerifyAuthorize(flow.bearer, nativesessionauth.AuthorizeFields{Preparation: flow.prepared.Binding(), Challenge: challenge, Request: request}, ed25519.Sign(flow.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal(err)
	}
	return request, proof
}

func TestNativeConfigurationAuthorizeQuotaRollbackAndRealUnknown(test *testing.T) {
	for _, mode := range []string{"quota", "unknown"} {
		test.Run(mode, func(test *testing.T) {
			flow := newNativeConfigurationFlow(test)
			request, proof := flow.authorizeEvidence(test)
			token, _ := nativesessionauth.ParseGrantToken(bytes.Repeat([]byte{19}, 32))
			budget := nativeDefaultBudget()
			commits := 0
			if mode == "quota" {
				budget.tables[3][0].rows = 0
			} else {
				flow.fixture.store.state.client.NativeMaterializationGrant.Use(func(next ent.Mutator) ent.Mutator {
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
								return errors.New("synthetic-secret-grant-commit-failure")
							})
						})
						return next.Mutate(ctx, mutation)
					})
				})
			}
			grant, err := flow.fixture.store.authorizeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), request, proof, token, flow.validator, budget)
			if grant.ID() != "" {
				test.Fatal("failed authorization returned a grant")
			}
			count, readErr := flow.fixture.store.state.client.NativeMaterializationGrant.Query().Count(test.Context())
			if readErr != nil {
				test.Fatal(readErr)
			}
			if mode == "quota" {
				if !errors.Is(err, ErrNativeQuota) || count != 0 {
					test.Fatal("quota left partial grant")
				}
				row, readErr := flow.fixture.store.state.client.NativeApplyPreparation.Query().Only(test.Context())
				if readErr != nil || row.Phase != "patch-ready" {
					test.Fatal("quota bound preparation without grant")
				}
				flow.authorize(test)
			} else {
				if !errors.Is(err, ErrNativeOutcomeUnknown) || commits != 1 || count != 1 {
					test.Fatal("unknown oracle must COMMIT exactly once without returning authority")
				}
				if _, err := flow.fixture.store.AuthorizeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), request, proof, token, flow.validator); !errors.Is(err, ErrNativePrecondition) {
					test.Fatal("unknown grant was reissued")
				}
			}
		})
	}
}

func TestNativeConfigurationFreshIdentityAfterWriterWait(test *testing.T) {
	flow := newNativeConfigurationFlow(test)
	request, proof := flow.authorizeEvidence(test)
	token, _ := nativesessionauth.ParseGrantToken(bytes.Repeat([]byte{19}, 32))
	flow.fixture.store.state.client.NativeConsumerSession.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, mutation)
			flow.fixture.clock.Add(int64(6 * time.Minute))
			return value, err
		})
	})
	calls := 0
	validate := func(ctx context.Context, metadata ConfigurationAsset, payload ConfigurationPayload) (NativePreparedData, error) {
		calls++
		return flow.validator(ctx, metadata, payload)
	}
	result, err := flow.fixture.store.AuthorizeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), request, proof, token, validate)
	if !errors.Is(err, ErrNativeUnauthorized) || result.ID() != "" || calls != 0 {
		test.Fatalf("stale pre-writer instant admitted expired identity to callback: calls=%d err=%v", calls, err)
	}
}

func (flow nativeConfigurationFlow) requestOrigin() string {
	return flow.prepared.Binding().Consumer.ServerOrigin
}

func nativeStateMaterializer(_ context.Context, _ ConfigurationAsset, payload ConfigurationPayload, _ nativesessionauth.ApplyFields, _ string, _ int64) (NativeMaterialization, error) {
	plaintext := payload.Reveal()
	defer clear(plaintext)
	return NewNativeMaterialization(plaintext)
}

func TestNativeConfigurationActualCommitUnknownAndWithheld(test *testing.T) {
	for _, mode := range []string{"unknown", "cancel-after"} {
		test.Run(mode, func(test *testing.T) {
			flow := newNativeConfigurationFlow(test)
			flow.authorize(test)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			commits := 0
			flow.fixture.store.state.client.NativeMaterializationGrant.Use(func(next ent.Mutator) ent.Mutator {
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
							if mode == "cancel-after" {
								cancel()
								return nil
							}
							return errors.New("synthetic-secret-commit-error")
						})
					})
					return next.Mutate(ctx, mutation)
				})
			})
			response, err := flow.fixture.store.ConsumeNativeConfigurationGrant(ctx, flow.credential, flow.requestOrigin(), flow.consume, flow.proof, nativeStateMaterializer)
			if len(response.Reveal()) != 0 || commits != 1 {
				test.Fatal("unconfirmed/canceled commit published or retried")
			}
			if mode == "unknown" && !errors.Is(err, ErrNativeOutcomeUnknown) || mode == "cancel-after" && !errors.Is(err, context.Canceled) {
				test.Fatalf("wrong postcommit classification: %v", err)
			}
			grant, readErr := flow.fixture.store.state.client.NativeMaterializationGrant.Query().Only(test.Context())
			if readErr != nil || grant.ConsumedMs == 0 {
				test.Fatal("oracle did not actually COMMIT consumed state")
			}
			if replay, err := flow.fixture.store.ConsumeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof, nativeStateMaterializer); err == nil || len(replay.Reveal()) != 0 {
				test.Fatal("durable unknown was replayed")
			}
		})
	}
}

func TestNativeConfigurationConsumeAtomicFailureBoundaries(test *testing.T) {
	for _, mode := range []string{"revision", "delete", "revoke", "parent-expiry", "callback-fail", "cancel", "callback-expiry", "expired-clock-rollback"} {
		test.Run(mode, func(test *testing.T) {
			flow := newNativeConfigurationFlow(test)
			flow.authorize(test)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			materialize := nativeStateMaterializer
			calls := 0
			switch mode {
			case "revision":
				_, err := flow.fixture.store.UpdateConfigurationAsset(test.Context(), "alice", flow.request.AssetID, 1, ConfigurationAssetDraft{AgentID: "claude", Kind: ConfigurationKindProvider, Name: "new credential", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic-new-secret"))})
				if err != nil {
					test.Fatal(err)
				}
			case "delete":
				if err := flow.fixture.store.DeleteConfigurationAsset(test.Context(), "alice", flow.request.AssetID, 1); err != nil {
					test.Fatal(err)
				}
			case "revoke":
				if err := flow.fixture.store.RevokeSession(test.Context(), flow.fixture.digest[:]); err != nil {
					test.Fatal(err)
				}
			case "parent-expiry":
				database, err := sql.Open("sqlite3", flow.fixture.path)
				if err != nil {
					test.Fatal(err)
				}
				_, err = database.ExecContext(test.Context(), "UPDATE sessions SET expires_at=? WHERE token_hash=?", flow.fixture.store.state.now().Add(-time.Second), flow.fixture.digest[:])
				database.Close()
				if err != nil {
					test.Fatal(err)
				}
			case "callback-fail", "cancel", "callback-expiry":
				materialize = func(ctx context.Context, metadata ConfigurationAsset, payload ConfigurationPayload, binding nativesessionauth.ApplyFields, id string, consumed int64) (NativeMaterialization, error) {
					calls++
					switch mode {
					case "callback-fail":
						return NativeMaterialization{}, errors.New("synthetic-secret-in-callback-error")
					case "cancel":
						cancel()
					case "callback-expiry":
						flow.fixture.clock.Add(int64(time.Minute))
					}
					return nativeStateMaterializer(ctx, metadata, payload, binding, id, consumed)
				}
			case "expired-clock-rollback":
				flow.fixture.clock.Add(int64(time.Minute))
			}
			response, err := flow.fixture.store.ConsumeNativeConfigurationGrant(ctx, flow.credential, flow.requestOrigin(), flow.consume, flow.proof, materialize)
			if err == nil || len(response.Reveal()) != 0 {
				test.Fatal("failed authorization returned secret")
			}
			if mode == "revision" && !errors.Is(err, ErrConfigurationConflict) {
				test.Fatalf("old credential revision not rejected: %v", err)
			}
			if (mode == "revoke" || mode == "parent-expiry") && !errors.Is(err, ErrNativeUnauthorized) {
				test.Fatalf("revoked exact parent not rejected: %v", err)
			}
			if calls == 0 && (mode == "cancel" || mode == "callback-fail" || mode == "callback-expiry") {
				test.Fatal("callback failure seam not reached")
			}
			if mode != "revoke" {
				grant, readErr := flow.fixture.store.state.client.NativeMaterializationGrant.Query().Only(test.Context())
				if readErr != nil || grant.ConsumedMs != 0 {
					test.Fatal("failed consume committed consumed state")
				}
			}
			if mode == "expired-clock-rollback" || mode == "callback-expiry" {
				flow.fixture.clock.Add(-int64(time.Minute))
				response, err = flow.fixture.store.ConsumeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof, nativeStateMaterializer)
				if err == nil || len(response.Reveal()) != 0 {
					test.Fatal("persisted expiry resurrected after clock rollback")
				}
			}
		})
	}
}

func TestNativeConfigurationParentRevokeKeepsOtherChains(test *testing.T) {
	fixture := newNativeFixture(test)
	_, firstPrep, _ := nativeSyntheticPair(test, fixture)
	other := fixture
	other.parent, other.digest = nativeTestParent(test, fixture.store, "bob", fixture.store.state.now().Add(time.Hour))
	_, otherPrep, otherGrant := nativeSyntheticPair(test, other)
	if err := fixture.store.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
		test.Fatal(err)
	}
	if err := fixture.store.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
		test.Fatal(err)
	}
	if exists, _ := fixture.store.state.client.NativeApplyPreparation.Query().Where(nativeapplypreparation.IDEQ(firstPrep.ID)).Exist(test.Context()); exists {
		test.Fatal("actual parent DELETE left preparation")
	}
	if count, _ := fixture.store.state.client.NativeApplyPreparation.Query().Count(test.Context()); count != 1 {
		test.Fatal("other owner preparation was deleted")
	}
	grant, err := fixture.store.state.client.NativeMaterializationGrant.Query().Only(test.Context())
	if err != nil || grant.ID != otherGrant.ID || grant.PrepareID != otherPrep.ID {
		test.Fatal("other owner's complete consumed chain did not survive")
	}
	if _, _, err := fixture.store.SessionUser(test.Context(), other.digest[:], fixture.store.state.now()); err != nil {
		test.Fatal("other owner parent revoked")
	}
}

func TestNativeConfigurationConcurrentConsumptionAndWriterOrdering(test *testing.T) {
	flow := newNativeConfigurationFlow(test)
	flow.authorize(test)
	peer := nativePeer(test, flow.fixture)
	ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		response, err := flow.fixture.store.ConsumeNativeConfigurationGrant(ctx, flow.credential, flow.requestOrigin(), flow.consume, flow.proof, func(ctx context.Context, metadata ConfigurationAsset, payload ConfigurationPayload, binding nativesessionauth.ApplyFields, id string, consumed int64) (NativeMaterialization, error) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return NativeMaterialization{}, ctx.Err()
			}
			return nativeStateMaterializer(ctx, metadata, payload, binding, id, consumed)
		})
		if err == nil && string(response.Reveal()) != "synthetic-state-secret-6d0" {
			err = errors.New("wrong revision materialized")
		}
		response.Destroy()
		first <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		test.Fatal("consume did not reach real write transaction")
	}
	database, err := sql.Open("sqlite3", flow.fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err := database.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		test.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "UPDATE native_consumer_sessions SET gate_version=gate_version+1"); err == nil {
		test.Fatal("materializer is outside the actual SQLite writer gate")
	}
	second := make(chan error, 1)
	go func() {
		response, err := peer.ConsumeNativeConfigurationGrant(ctx, flow.credential, flow.requestOrigin(), flow.consume, flow.proof, nativeStateMaterializer)
		if len(response.Reveal()) != 0 {
			second <- errors.New("second consumer received secret")
			return
		}
		second <- err
	}()
	close(release)
	if err := <-first; err != nil {
		test.Fatal(err)
	}
	if err := <-second; !errors.Is(err, ErrNativePrecondition) {
		test.Fatalf("parallel consume not single-use: %v", err)
	}
	if err := peer.RevokeSession(ctx, flow.fixture.digest[:]); err != nil {
		test.Fatal(err)
	}
	if count, _ := peer.state.client.NativeMaterializationGrant.Query().Count(ctx); count != 0 {
		test.Fatal("logout failed after committed consume")
	}
}

func TestNativeConfigurationNineCascadeConstraints(test *testing.T) {
	fixture := newNativeFixture(test)
	database, err := sql.Open("sqlite3", fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	for table, expected := range map[string]map[string]string{
		"native_consumer_challenges":    {"parent_session_id": "sessions", "user_id": "users"},
		"native_consumer_sessions":      {"parent_session_id": "sessions", "user_id": "users"},
		"native_apply_preparations":     {"native_session_id": "native_consumer_sessions", "user_id": "users"},
		"native_materialization_grants": {"native_session_id": "native_consumer_sessions", "user_id": "users", "prepare_id": "native_apply_preparations"},
	} {
		rows, err := database.QueryContext(test.Context(), "PRAGMA foreign_key_list("+table+")")
		if err != nil {
			test.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var id, sequence int
			var target, from, to, update, deletion, match string
			if err := rows.Scan(&id, &sequence, &target, &from, &to, &update, &deletion, &match); err != nil {
				test.Fatal(err)
			}
			if deletion != "CASCADE" || expected[from] != target || to != "id" {
				test.Fatalf("%s has non-cascading native FK", table)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			test.Fatal(err)
		}
		rows.Close()
		if count != len(expected) {
			test.Fatalf("%s: expected %d physical CASCADE FKs, got %d", table, len(expected), count)
		}
	}
	rows, err := database.QueryContext(test.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		test.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		test.Fatal("native foreign key integrity failure")
	}
}

func TestNativeConfigurationOwnerDeleteRollbackLegacyOrder(test *testing.T) {
	fixture := newNativeFixture(test)
	nativeSyntheticPair(test, fixture)
	other := fixture
	other.parent, other.digest = nativeTestParent(test, fixture.store, "bob", fixture.store.state.now().Add(time.Hour))
	nativeSyntheticPair(test, other)
	for _, rollback := range []bool{true, false} {
		_, err := fixture.store.nativeTransaction(test.Context(), func(tx *ent.Tx) (struct{}, func(time.Time) error, error) {
			owner, err := findUser(test.Context(), tx.Client(), "alice")
			if err != nil {
				return struct{}{}, nil, err
			}
			if _, err := tx.Session.Delete().Where(session.UserIDEQ(owner.ID)).Exec(test.Context()); err != nil {
				return struct{}{}, nil, err
			}
			if err := tx.User.DeleteOneID(owner.ID).Exec(test.Context()); err != nil {
				return struct{}{}, nil, err
			}
			if rollback {
				return struct{}{}, nil, errors.New("synthetic owner-delete rollback")
			}
			return struct{}{}, nil, nil
		})
		want := 1
		if rollback {
			want = 2
			if err == nil {
				test.Fatal("rollback test unexpectedly committed")
			}
		} else if err != nil {
			test.Fatal(err)
		}
		if count, _ := fixture.store.state.client.NativeApplyPreparation.Query().Count(test.Context()); count != want {
			test.Fatal("owner delete/rollback damaged preparation chains")
		}
		if count, _ := fixture.store.state.client.NativeMaterializationGrant.Query().Count(test.Context()); count != want {
			test.Fatal("owner delete/rollback damaged consumed grants")
		}
		if _, _, err := fixture.store.SessionUser(test.Context(), other.digest[:], fixture.store.state.now()); err != nil {
			test.Fatal("owner deletion crossed ownership boundary")
		}
	}
}

func TestNativeConfigurationRequestDigestIntegrity(test *testing.T) {
	flow := newNativeConfigurationFlow(test)
	database, err := sql.Open("sqlite3", flow.fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(test.Context(), "UPDATE native_apply_preparations SET request_digest=zeroblob(32)"); err != nil {
		test.Fatal(err)
	}
	if _, err := flow.fixture.store.NativeConfigurationSnapshot(test.Context(), flow.credential, flow.requestOrigin(), flow.request.OperationID, flow.prepared.Binding().PrepareID); !errors.Is(err, ErrNativeUnavailable) {
		test.Fatal("corrupted request digest accepted as authoritative snapshot")
	}
}

func TestNativeConfigurationCorruptChargeIsUnavailable(test *testing.T) {
	flow := newNativeConfigurationFlow(test)
	database, err := sql.Open("sqlite3", flow.fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(test.Context(), "UPDATE native_consumer_sessions SET charge=charge-1"); err != nil {
		test.Fatal(err)
	}
	if _, err := flow.fixture.store.NativeConfigurationSnapshot(test.Context(), flow.credential, flow.requestOrigin(), flow.request.OperationID, ""); !errors.Is(err, ErrNativeUnavailable) {
		test.Fatalf("corrupt accounting misclassified as credentials: %v", err)
	}
}

func TestNativeConfigurationPrepareDifferentNonceCannotRenew(test *testing.T) {
	flow := newNativeConfigurationFlow(test)
	request := flow.request
	request.Nonce[0]++
	consumer := flow.prepared.Binding().Consumer
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	wire := nativeFlowWire(flow.prepared.Binding())
	transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: keyHash[:]}, &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: request.OwnerID, AssetId: request.AssetID, ExpectedAssetRevision: request.AssetRevision, AgentId: request.AgentID, SchemaVersion: request.SchemaVersion, Adapter: wire.Adapter, Target: 1, Scope: 1, ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, OperationId: request.OperationID[:], ClientNonce: request.Nonce[:]})
	if err != nil {
		test.Fatal(err)
	}
	proof, err := flow.verifier.VerifyPrepare(flow.bearer, nativesessionauth.PrepareFields{Consumer: consumer, Request: request}, ed25519.Sign(flow.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal(err)
	}
	flow.fixture.clock.Add(int64(time.Millisecond))
	if result, err := flow.fixture.store.PrepareNativeConfigurationApply(test.Context(), flow.credential, flow.requestOrigin(), request, proof, flow.validator); !errors.Is(err, ErrConfigurationConflict) || result.Binding().PrepareID != "" {
		test.Fatal("new nonce replaced or renewed existing preparation")
	}
	if count, _ := flow.fixture.store.state.client.NativeApplyPreparation.Query().Count(test.Context()); count != 1 {
		test.Fatal("same operation gained a second preparation")
	}
}

func TestNativeConfigurationGrantBindingIntegrity(test *testing.T) {
	flow := newNativeConfigurationFlow(test)
	flow.authorize(test)
	database, err := sql.Open("sqlite3", flow.fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(test.Context(), "UPDATE native_materialization_grants SET binding_digest=zeroblob(32)"); err != nil {
		test.Fatal(err)
	}
	if _, err := flow.fixture.store.NativeConfigurationSnapshot(test.Context(), flow.credential, flow.requestOrigin(), flow.request.OperationID, flow.prepared.Binding().PrepareID); !errors.Is(err, ErrNativeUnavailable) {
		test.Fatal("corrupted grant binding accepted as authoritative snapshot")
	}
}

func TestNativeConfigurationOpaqueMaterializationPrivacy(test *testing.T) {
	flow := newNativeConfigurationFlow(test)
	flow.authorize(test)
	sentinel := []byte("synthetic-private-materialization-6d0")
	response, err := NewNativeMaterialization(sentinel)
	if err != nil || !bytes.Equal(response.Reveal(), sentinel) {
		test.Fatal("privacy fixture must hold real bytes")
	}
	snapshot, err := flow.fixture.store.NativeConfigurationSnapshot(test.Context(), flow.credential, flow.requestOrigin(), flow.request.OperationID, flow.prepared.Binding().PrepareID)
	if err != nil {
		test.Fatal(err)
	}
	for _, value := range []any{response, flow.prepared, flow.authorization, snapshot, flow.proof, flow.consume.Token} {
		for _, container := range []any{value, &value, struct{ hidden any }{value}, map[string]any{"value": value}, []any{value}} {
			for _, verb := range "vTtbcdoOqxXUeEfFgGspw" {
				for _, prefix := range []string{"%", "%+", "%#", "%020.10"} {
					rendered := fmt.Sprintf(prefix+string(verb), container)
					if strings.Contains(rendered, string(sentinel)) || strings.Contains(rendered, fmt.Sprintf("%x", sentinel)) || strings.Contains(rendered, fmt.Sprint(sentinel)) {
						test.Fatal("opaque materialization fmt leaked")
					}
				}
			}
			for _, json := range []bool{false, true} {
				var output bytes.Buffer
				var handler slog.Handler = slog.NewTextHandler(&output, nil)
				if json {
					handler = slog.NewJSONHandler(&output, nil)
				}
				slog.New(handler).Info("synthetic", slog.Any("value", container))
				if strings.Contains(output.String(), string(sentinel)) || strings.Contains(output.String(), fmt.Sprintf("%x", sentinel)) {
					test.Fatal("real slog materialization leak")
				}
			}
		}
	}
	response.Destroy()
	if len(response.Reveal()) != 0 {
		test.Fatal("owned scratch remains after Destroy")
	}
}

func nativeSyntheticPair(test *testing.T, fixture nativeFixture) (*ent.NativeConsumerSession, *ent.NativeApplyPreparation, *ent.NativeMaterializationGrant) {
	test.Helper()
	parent, err := fixture.store.state.client.Session.Query().WithUser().Where(session.TokenHashEQ(fixture.digest[:])).Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	start := nativeTestStart()
	start.ExpectedOwner = parent.Edges.User.UserKey
	_, _ = rand.Read(start.OperationID)
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, start)
	if err != nil {
		test.Fatal(err)
	}
	input := nativeTestBind(challenge, fixture.digest)
	var tokenHash [32]byte
	_, _ = rand.Read(tokenHash[:])
	credential := NativeConsumerCredential{nativeOpaque: nativeWrap(tokenHash)}
	input.TokenHash = credential
	if _, err := fixture.store.BindNativeConsumer(test.Context(), fixture.parent, input); err != nil {
		test.Fatal(err)
	}
	child, err := fixture.store.state.client.NativeConsumerSession.Query().Where(nativeconsumersession.TokenHashEQ(credential.box.value[:])).Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	publicID := rand.Text()
	ciphertext := bytes.Repeat([]byte{17}, 512)
	prepCharge := int64(512 + len(publicID) + 16 + 32 + 32 + len(ciphertext) + len("bound-with-grant"))
	prep, err := fixture.store.state.client.NativeApplyPreparation.Create().SetPublicID(publicID).SetUserID(child.UserID).SetNativeSessionID(child.ID).SetOperationID(child.OperationID).SetRequestDigest(bytes.Repeat([]byte{21}, 32)).SetAuthorizeChallenge(bytes.Repeat([]byte{22}, 32)).SetCiphertext(ciphertext).SetPhase("bound-with-grant").SetIssuedMs(child.IssuedMs).SetExpiresMs(child.ExpiresMs).SetCharge(prepCharge).Save(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	publicID = rand.Text()
	grant, err := fixture.store.state.client.NativeMaterializationGrant.Create().SetPublicID(publicID).SetUserID(child.UserID).SetNativeSessionID(child.ID).SetPrepareID(prep.ID).SetTokenHash([]byte(rand.Text() + "123456")).SetConsumeChallenge(bytes.Repeat([]byte{23}, 32)).SetBindingDigest(bytes.Repeat([]byte{24}, 32)).SetIssuedMs(child.IssuedMs).SetExpiresMs(child.IssuedMs + 60000).SetConsumedMs(child.IssuedMs + 1).SetCharge(int64(512 + len(publicID) + 96)).Save(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.state.client.NativeConsumerChallenge.Delete().Where(nativeconsumerchallenge.UsedEQ(true)).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	return child, prep, grant
}

func TestNativeConfigurationBoundedGCAndRollback(test *testing.T) {
	fixture := newNativeFixture(test)
	for group := range 4 {
		current := fixture
		current.parent, current.digest = nativeTestParent(test, fixture.store, fmt.Sprintf("gc-owner-%d", group), fixture.store.state.now().Add(time.Hour))
		for range 16 {
			_, prepare, _ := nativeSyntheticPair(test, current)
			prepare.Ciphertext = bytes.Repeat([]byte{11}, 15000)
			prepare.Charge = nativePreparationCharge(prepare)
			if err := fixture.store.state.client.NativeApplyPreparation.UpdateOneID(prepare.ID).SetCiphertext(prepare.Ciphertext).SetCharge(prepare.Charge).Exec(test.Context()); err != nil {
				test.Fatal(err)
			}
		}
	}
	fixture.clock.Add(int64(6 * time.Minute))
	first, err := fixture.store.PruneNativeConsumers(test.Context())
	if err != nil || first.Roots <= 0 || first.Roots >= 64 || first.Rows != first.Roots*3 || first.Bytes > 1<<20 {
		test.Fatalf("GC failed distinct byte bound: %+v %v", first, err)
	}
	second, err := fixture.store.PruneNativeConsumers(test.Context())
	if err != nil || first.Roots+second.Roots != 64 || first.Rows+second.Rows != 192 {
		test.Fatalf("bounded GC lost complete roots: %+v %v", second, err)
	}
	if count, _ := fixture.store.state.client.NativeMaterializationGrant.Query().Count(test.Context()); count != 0 {
		test.Fatal("GC left orphan grants")
	}
	fixture.clock.Add(-int64(6 * time.Minute))
	_, prepare, _ := nativeSyntheticPair(test, fixture)
	_, err = fixture.store.nativeTransaction(test.Context(), func(tx *ent.Tx) (struct{}, func(time.Time) error, error) {
		if err := tx.NativeApplyPreparation.DeleteOneID(prepare.ID).Exec(test.Context()); err != nil {
			return struct{}{}, nil, err
		}
		return struct{}{}, nil, errors.New("synthetic rollback")
	})
	if err == nil {
		test.Fatal("test rollback committed")
	}
	if count, _ := fixture.store.state.client.NativeMaterializationGrant.Query().Count(test.Context()); count != 1 {
		test.Fatal("rollback did not restore cascade closure")
	}
}

func TestNativeConfigurationAllQuotaScopesAndPostState(test *testing.T) {
	for table := 2; table < 4; table++ {
		for scope := range 3 {
			for _, byteLimit := range []bool{false, true} {
				test.Run(fmt.Sprintf("table-%d-scope-%d-bytes-%t", table, scope, byteLimit), func(test *testing.T) {
					fixture := newNativeFixture(test)
					child, prepare, grant := nativeSyntheticPair(test, fixture)
					id, charge := prepare.ID, prepare.Charge
					if table == 3 {
						id, charge = grant.ID, grant.Charge
					}
					budget := nativeDefaultBudget()
					if byteLimit {
						budget.tables[table][scope].bytes = charge - 1
					} else {
						budget.tables[table][scope].rows = 0
					}
					_, err := fixture.store.nativeTransaction(test.Context(), func(tx *ent.Tx) (struct{}, func(time.Time) error, error) {
						return struct{}{}, nil, nativeCheckPostStateQuota(test.Context(), tx, []nativeQuotaChange{{id: id, post: nativeUsageRow{table, child.ParentSessionID, child.UserID, charge}}}, budget)
					})
					if !errors.Is(err, ErrNativeQuota) {
						test.Fatalf("new-table hard gate bypassed: %v", err)
					}
				})
			}
		}
	}
	for scope := range 3 {
		for _, byteLimit := range []bool{false, true} {
			test.Run(string(rune('A'+scope))+map[bool]string{true: "bytes", false: "rows"}[byteLimit], func(test *testing.T) {
				fixture := newNativeFixture(test)
				child, prepare, grant := nativeSyntheticPair(test, fixture)
				budget := nativeDefaultBudget()
				if byteLimit {
					budget.totals[scope].bytes = child.Charge + prepare.Charge + grant.Charge + 511
				} else {
					budget.totals[scope].rows = 3
				}
				_, err := fixture.store.nativeTransaction(test.Context(), func(tx *ent.Tx) (struct{}, func(time.Time) error, error) {
					if _, err := nativeParent(test.Context(), tx, fixture.parent, fixture.store.state.now()); err != nil {
						return struct{}{}, nil, err
					}
					return struct{}{}, nil, nativeCheckQuota(test.Context(), tx, nativeUsageRow{0, child.ParentSessionID, child.UserID, 512}, budget)
				})
				if !errors.Is(err, ErrNativeQuota) {
					test.Fatalf("four-table scope cap bypassed: %v", err)
				}
			})
		}
	}
	fixture := newNativeFixture(test)
	child, prepare, grant := nativeSyntheticPair(test, fixture)
	budget := nativeDefaultBudget()
	budget.totals[0].bytes = child.Charge + prepare.Charge + grant.Charge + 1024
	_, err := fixture.store.nativeTransaction(test.Context(), func(tx *ent.Tx) (struct{}, func(time.Time) error, error) {
		return struct{}{}, nil, nativeCheckPostStateQuota(test.Context(), tx, []nativeQuotaChange{
			{id: prepare.ID, post: nativeUsageRow{2, child.ParentSessionID, child.UserID, prepare.Charge + 513}},
			{post: nativeUsageRow{3, child.ParentSessionID, child.UserID, 512}},
		}, budget)
	})
	if !errors.Is(err, ErrNativeQuota) {
		test.Fatalf("replacement + insertion not charged together: %v", err)
	}
	budget.totals[0].bytes++
	_, err = fixture.store.nativeTransaction(test.Context(), func(tx *ent.Tx) (struct{}, func(time.Time) error, error) {
		return struct{}{}, nil, nativeCheckPostStateQuota(test.Context(), tx, []nativeQuotaChange{
			{id: prepare.ID, post: nativeUsageRow{2, child.ParentSessionID, child.UserID, prepare.Charge + 513}},
			{post: nativeUsageRow{3, child.ParentSessionID, child.UserID, 512}},
		}, budget)
	})
	if err != nil {
		test.Fatalf("exact post-state byte boundary rejected: %v", err)
	}
}

func TestNativeConfigurationMultiStoreLastSlotIncludesTerminalRows(test *testing.T) {
	fixture := newNativeFixture(test)
	nativeSyntheticPair(test, fixture)
	peer := nativePeer(test, fixture)
	budget := nativeDefaultBudget()
	budget.totals[0].rows = 4
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, store := range []*Store{fixture.store, peer} {
		workers.Go(func() {
			<-start
			_, err := store.beginNativeConsumer(test.Context(), fixture.parent, nativeTestStart(), budget)
			results <- err
		})
	}
	close(start)
	workers.Wait()
	accepted, exhausted := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrNativeQuota) {
			exhausted++
		} else {
			test.Fatalf("unexpected last-slot result: %v", err)
		}
	}
	if accepted != 1 || exhausted != 1 {
		test.Fatal("multi-Store cumulative last slot was not serialized")
	}
}

func TestNativeConfigurationBeginBindCountAllFourTables(test *testing.T) {
	fixture := newNativeFixture(test)
	nativeSyntheticPair(test, fixture)
	budget := nativeDefaultBudget()
	budget.totals[0].rows = 3
	if _, err := fixture.store.beginNativeConsumer(test.Context(), fixture.parent, nativeTestStart(), budget); !errors.Is(err, ErrNativeQuota) {
		test.Fatalf("Begin omitted durable preparation/grant: %v", err)
	}
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart())
	if err != nil {
		test.Fatal(err)
	}
	budget.totals[0].rows = 4
	if _, err := fixture.store.bindNativeConsumer(test.Context(), fixture.parent, nativeTestBind(challenge, fixture.digest), budget); !errors.Is(err, ErrNativeQuota) {
		test.Fatalf("Bind omitted durable preparation/grant: %v", err)
	}
	row, err := fixture.store.state.client.NativeConsumerChallenge.Query().Where(nativeconsumerchallenge.PublicIDEQ(challenge.ID())).Only(test.Context())
	if err != nil || row.Used {
		test.Fatal("quota failure consumed challenge")
	}
}

func TestNativeConfigurationGCCountsDistinctCascadeAndRealRevoke(test *testing.T) {
	fixture := newNativeFixture(test)
	nativeSyntheticPair(test, fixture)
	fixture.clock.Add(int64(6 * time.Minute))
	result, err := fixture.store.PruneNativeConsumers(test.Context())
	if err != nil || result.Roots != 1 || result.Rows != 3 || result.Bytes < 2000 {
		test.Fatalf("GC omitted/doubled cascade rows: %+v, %v", result, err)
	}
	if count, _ := fixture.store.state.client.NativeMaterializationGrant.Query().Count(test.Context()); count != 0 {
		test.Fatal("grant survives expired child")
	}
	fixture.clock.Add(-int64(6 * time.Minute))
	nativeSyntheticPair(test, fixture)
	other, otherDigest := nativeTestParent(test, fixture.store, "bob", fixture.store.state.now().Add(time.Hour))
	_ = other
	if err := fixture.store.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
		test.Fatal(err)
	}
	if err := fixture.store.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
		test.Fatal(err)
	}
	if count, _ := fixture.store.state.client.NativeApplyPreparation.Query().Count(test.Context()); count != 0 {
		test.Fatal("preparation survives actual parent DELETE")
	}
	if count, _ := fixture.store.state.client.NativeMaterializationGrant.Query().Count(test.Context()); count != 0 {
		test.Fatal("consumed grant blocked actual parent DELETE")
	}
	if _, _, err := fixture.store.SessionUser(context.Background(), otherDigest[:], fixture.store.state.now()); err != nil {
		test.Fatal("other owner was revoked")
	}
}
