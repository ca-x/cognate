package configuration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/agentadapter"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

func wirePrepareRequest(request nativesessionauth.PrepareRequest) *nativev1.PrepareNativeApplyRequest {
	return &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: request.OwnerID, AssetId: request.AssetID, ExpectedAssetRevision: request.AssetRevision, AgentId: request.AgentID, SchemaVersion: request.SchemaVersion, Adapter: wireAdapter(request.Adapter), Target: nativev1.Target(request.Target), Scope: nativev1.Scope(request.Scope), ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, OperationId: request.OperationID[:], ClientNonce: request.Nonce[:]}
}

type nativeOrderFixture struct {
	domain             *Service
	native             *NativeService
	consumer           *auth.NativeConsumerService
	owner              string
	asset              EditView
	path               string
	bearer             string
	key                ed25519.PrivateKey
	prepare            nativesessionauth.PrepareRequest
	prepareSignature   []byte
	prepared           state.NativePrepared
	authorize          nativesessionauth.AuthorizeRequest
	authorizeSignature []byte
	grant              auth.IssuedNativeGrant
	consume            nativesessionauth.ConsumeRequest
	consumeSignature   []byte
}

type nativeOrderReadProbe struct {
	Store
	reads  int
	before func(context.Context)
}

func (probe *nativeOrderReadProbe) ReadConfigurationAssetVersion(ctx context.Context, owner, id string) (state.ConfigurationAsset, state.ConfigurationPayload, error) {
	probe.reads++
	if probe.before != nil {
		probe.before(ctx)
	}
	return probe.Store.ReadConfigurationAssetVersion(ctx, owner, id)
}

func newNativeOrderFixture(test *testing.T, mode AuthMode) *nativeOrderFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	master := bytes.Repeat([]byte{93}, 32)
	store, err := state.Open(test.Context(), filepath.Join(directory, "order.db"), master)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	owner, err := auth.NewService(store, master)
	if err != nil {
		test.Fatal(err)
	}
	parent, ownerID, err := owner.LoginOIDC(test.Context(), "https://synthetic.identity", "order-subject", "", "")
	if err != nil {
		test.Fatal(err)
	}
	consumer, err := auth.NewNativeConsumerService(owner, master, "https://cognate.example")
	clear(master)
	if err != nil {
		test.Fatal(err)
	}
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal(err)
	}
	native, err := NewNativeService(store, consumer, registry)
	if err != nil {
		test.Fatal(err)
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		test.Fatal(err)
	}
	operation := [16]byte{11}
	begin := &nativev1.BeginNativeConsumerRequest{Purpose: 1, OperationId: operation[:], ConsumerPublicKey: public, ClientNonce: bytes.Repeat([]byte{4}, 32), ExpectedOwnerId: ownerID, ExpectedServerOrigin: "https://cognate.example"}
	challenge, err := consumer.Begin(test.Context(), parent.Reveal(), state.NativeConsumerStart{ExpectedOwner: ownerID, Origin: begin.ExpectedServerOrigin, Purpose: state.NativeConfigurationApply, OperationID: operation[:], PublicKey: public, ClientNonce: begin.ClientNonce})
	if err != nil {
		test.Fatal(err)
	}
	fields, err := challenge.BindFields()
	if err != nil {
		test.Fatal(err)
	}
	transcript, err := configclient.BindTranscript(begin, &nativev1.BeginNativeConsumerResponse{ChallengeId: fields.ChallengeID, Challenge: fields.Challenge[:], NativeSessionBinding: fields.NativeSessionBinding[:], OwnerId: ownerID, ServerOrigin: begin.ExpectedServerOrigin, ChallengeExpiresAtUnixMs: fields.ChallengeExpiresAtUnixMS, NativeSessionExpiresAtUnixMs: fields.NativeSessionExpiresAtUnixMS})
	if err != nil {
		test.Fatal(err)
	}
	issued, err := consumer.Bind(test.Context(), parent.Reveal(), fields.ChallengeID, ed25519.Sign(key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal(err)
	}
	domain := New(store)
	slot, required := APIKeySlot, uint32(1)
	if mode == AuthToken {
		slot, required = AuthTokenSlot, 2
	}
	asset, err := domain.Create(test.Context(), ownerID, Draft{Name: "order fixture", Provider: ClaudeProvider{AuthMode: mode}}, []SecretMutation{{Slot: slot, Action: Replace, Replacement: NewSecret([]byte("synthetic-order-secret"))}})
	if err != nil {
		test.Fatal(err)
	}
	var adapter nativesessionauth.AdapterFields
	for _, descriptor := range registry.List() {
		if descriptor.Manifest.PluginID == "builtin.claude" {
			adapter = nativesessionauth.AdapterFields{APIVersion: 1, PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Capability: "claude.provider-switch.v1"}
		}
	}
	prepare := nativesessionauth.PrepareRequest{OwnerID: ownerID, AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, AgentID: "claude", SchemaVersion: 1, Adapter: adapter, Target: 1, Scope: 1, ProfileID: "profile", SelectionRevision: 1, OperationID: operation}
	transcript, err = configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: ownerID, ServerOrigin: begin.ExpectedServerOrigin, NativeSessionBinding: fields.NativeSessionBinding[:], OperationID: operation[:], ConsumerKeySHA256: consumerKeyHash(public)}, wirePrepareRequest(prepare))
	if err != nil {
		test.Fatal(err)
	}
	fixture := &nativeOrderFixture{domain: domain, native: native, consumer: consumer, owner: ownerID, asset: asset, path: filepath.Join(directory, "order.db"), bearer: issued.Reveal(), key: key, prepare: prepare, prepareSignature: ed25519.Sign(key, transcript.CopyBytes())}
	fixture.authorize = nativesessionauth.AuthorizeRequest{OperationID: operation, RequiredSlot: required, Consent: nativesessionauth.ConsentFields{Kind: 1, Revision: 1}}
	return fixture
}

func (fixture *nativeOrderFixture) advance(test *testing.T, stage string) {
	test.Helper()
	if stage == "prepare" {
		return
	}
	prepared, err := fixture.native.Prepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
	if err != nil {
		test.Fatal(err)
	}
	fixture.prepared = prepared
	fixture.authorize.PrepareID, fixture.authorize.IntentExpiresMS = prepared.Binding().PrepareID, prepared.Binding().ExpiresMS
	challenge := prepared.Challenge()
	transcript, err := configclient.AuthorizeTranscript(wirePreparation(prepared.Binding()), challenge[:], &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: fixture.authorize.PrepareID, OperationId: fixture.prepare.OperationID[:], IntentCommitment: fixture.authorize.Commitment[:], RequestedSlots: []nativev1.SecretSlot{nativev1.SecretSlot(fixture.authorize.RequiredSlot)}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: fixture.authorize.Nonce[:], IntentExpiresAtUnixMs: fixture.authorize.IntentExpiresMS})
	if err != nil {
		test.Fatal(err)
	}
	fixture.authorizeSignature = ed25519.Sign(fixture.key, transcript.CopyBytes())
	if stage == "authorize" {
		return
	}
	grant, err := fixture.native.Authorize(test.Context(), fixture.bearer, fixture.authorize, fixture.authorizeSignature)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(grant.Destroy)
	fixture.grant = grant
	fixture.consume = nativesessionauth.ConsumeRequest{PrepareID: fixture.authorize.PrepareID, GrantID: grant.ID(), OperationID: fixture.prepare.OperationID, Commitment: fixture.authorize.Commitment}
	fixture.signConsume(test, grant.Reveal())
}

func (fixture *nativeOrderFixture) signConsume(test *testing.T, token []byte) {
	test.Helper()
	var err error
	fixture.consume.Token, err = nativesessionauth.ParseGrantToken(token)
	if err != nil {
		test.Fatal(err)
	}
	authorizeChallenge, consumeChallenge := fixture.prepared.Challenge(), fixture.grant.Challenge()
	transcript, err := configclient.ConsumeTranscript(wireApply(fixture.grant.Binding()), authorizeChallenge[:], consumeChallenge[:], fixture.grant.ExpiresMS(), &nativev1.ConsumeNativeMaterializationRequest{PrepareId: fixture.consume.PrepareID, GrantId: fixture.consume.GrantID, GrantToken: token, OperationId: fixture.consume.OperationID[:], IntentCommitment: fixture.consume.Commitment[:], ClientNonce: fixture.consume.Nonce[:]})
	if err != nil {
		test.Fatal(err)
	}
	fixture.consumeSignature = ed25519.Sign(fixture.key, transcript.CopyBytes())
}

func TestNativeConfigurationStageProofBeforeAssetPreflight(test *testing.T) {
	for _, mode := range []AuthMode{APIKey, AuthToken} {
		for _, stage := range []string{"prepare", "authorize", "consume-proof", "consume-token"} {
			test.Run(string(mode)+"/"+stage, func(test *testing.T) {
				fixture := newNativeOrderFixture(test, mode)
				fixture.advance(test, stage)
				probe := &nativeOrderReadProbe{Store: fixture.native.domain.store}
				fixture.native.domain = New(probe)
				badSignature := make([]byte, ed25519.SignatureSize)
				if stage == "consume-token" {
					fixture.signConsume(test, bytes.Repeat([]byte{79}, 32))
					badSignature = fixture.consumeSignature
				}
				check := func() {
					test.Helper()
					var err error
					switch stage {
					case "prepare":
						_, err = fixture.native.Prepare(test.Context(), fixture.bearer, fixture.prepare, badSignature)
					case "authorize":
						_, err = fixture.native.Authorize(test.Context(), fixture.bearer, fixture.authorize, badSignature)
					default:
						response, failure := fixture.native.Consume(test.Context(), fixture.bearer, fixture.consume, badSignature)
						if len(response.Reveal()) != 0 {
							test.Fatal("bad capability returned materialization")
						}
						err = failure
					}
					if !errors.Is(err, state.ErrNativeUnauthorized) {
						test.Fatalf("invalid stage evidence observed business state: %v", err)
					}
					if probe.reads != 0 {
						test.Fatal("invalid stage evidence reached asset/plugin preflight")
					}
				}
				check()
				if _, err := fixture.domain.Update(test.Context(), fixture.owner, fixture.asset.Metadata.ID, fixture.asset.Metadata.Revision, Draft{Name: "changed", Provider: fixture.asset.Provider}, []SecretMutation{{Slot: APIKeySlot, Action: Keep}, {Slot: AuthTokenSlot, Action: Keep}}); err != nil {
					test.Fatal(err)
				}
				check()
			})
		}
	}
}

func TestNativeConfigurationValidEvidenceKeepsStaleRevisionConflict(test *testing.T) {
	for _, mode := range []AuthMode{APIKey, AuthToken} {
		for _, stage := range []string{"prepare", "authorize", "consume"} {
			test.Run(string(mode)+"/"+stage, func(test *testing.T) {
				fixture := newNativeOrderFixture(test, mode)
				fixture.advance(test, stage)
				if _, err := fixture.domain.Update(test.Context(), fixture.owner, fixture.asset.Metadata.ID, fixture.asset.Metadata.Revision, Draft{Name: "changed", Provider: fixture.asset.Provider}, nil); err != nil {
					test.Fatal(err)
				}
				var err error
				switch stage {
				case "prepare":
					_, err = fixture.native.Prepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
				case "authorize":
					_, err = fixture.native.Authorize(test.Context(), fixture.bearer, fixture.authorize, fixture.authorizeSignature)
				default:
					_, err = fixture.native.Consume(test.Context(), fixture.bearer, fixture.consume, fixture.consumeSignature)
				}
				if !errors.Is(err, state.ErrConfigurationConflict) {
					test.Fatalf("valid evidence lost real revision conflict: %v", err)
				}
			})
		}
	}
}

func TestNativeConfigurationTerminalCapabilityBeforeBusinessPreflight(test *testing.T) {
	for _, stage := range []string{"authorize", "consume"} {
		test.Run(stage, func(test *testing.T) {
			fixture := newNativeOrderFixture(test, APIKey)
			fixture.advance(test, "consume")
			if stage == "consume" {
				response, err := fixture.native.Consume(test.Context(), fixture.bearer, fixture.consume, fixture.consumeSignature)
				if err != nil || len(response.Reveal()) == 0 {
					test.Fatal("actual initial consume failed")
				}
				response.Destroy()
			}
			probe := &nativeOrderReadProbe{Store: fixture.native.domain.store}
			fixture.native.domain = New(probe)
			for range 2 {
				var err error
				if stage == "consume" {
					_, err = fixture.native.Consume(test.Context(), fixture.bearer, fixture.consume, fixture.consumeSignature)
				} else {
					_, err = fixture.native.Authorize(test.Context(), fixture.bearer, fixture.authorize, fixture.authorizeSignature)
				}
				if !errors.Is(err, state.ErrNativePrecondition) || probe.reads != 0 {
					test.Fatal("terminal capability reached asset/plugin state")
				}
				updated, err := fixture.domain.Update(test.Context(), fixture.owner, fixture.asset.Metadata.ID, fixture.asset.Metadata.Revision, Draft{Name: "changed", Provider: fixture.asset.Provider}, nil)
				if err != nil {
					test.Fatal(err)
				}
				fixture.asset = updated
			}
		})
	}
}

func TestNativeConfigurationPreflightReleasesWriterBeforeBusinessPlanning(test *testing.T) {
	for _, stage := range []string{"prepare", "authorize", "consume"} {
		test.Run(stage, func(test *testing.T) {
			fixture := newNativeOrderFixture(test, APIKey)
			fixture.advance(test, stage)
			database, err := sql.Open("sqlite3", fixture.path)
			if err != nil {
				test.Fatal(err)
			}
			defer database.Close()
			probe := &nativeOrderReadProbe{Store: fixture.native.domain.store, before: func(ctx context.Context) {
				connection, err := database.Conn(ctx)
				if err != nil {
					test.Fatal(err)
				}
				defer connection.Close()
				if _, err := connection.ExecContext(ctx, "PRAGMA busy_timeout = 100"); err != nil {
					test.Fatal(err)
				}
				if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
					test.Fatal("preflight retained a writer during business/plugin planning")
				}
				if _, err := connection.ExecContext(ctx, "ROLLBACK"); err != nil {
					test.Fatal(err)
				}
			}}
			fixture.native.domain = New(probe)
			switch stage {
			case "prepare":
				_, err = fixture.native.Prepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
			case "authorize":
				_, err = fixture.native.Authorize(test.Context(), fixture.bearer, fixture.authorize, fixture.authorizeSignature)
			default:
				response, failure := fixture.native.Consume(test.Context(), fixture.bearer, fixture.consume, fixture.consumeSignature)
				response.Destroy()
				err = failure
			}
			if err != nil || probe.reads != 1 {
				test.Fatalf("real stage did not plan outside preflight transaction: %v", err)
			}
		})
	}
}

func TestNativeConfigurationActualSecretSlotsOnce(test *testing.T) {
	var captured bytes.Buffer
	oldWriter, oldLogger := log.Writer(), slog.Default()
	oldFlags, oldPrefix := log.Flags(), log.Prefix()
	log.SetOutput(&captured)
	slog.SetDefault(slog.New(slog.NewTextHandler(&captured, nil)))
	test.Cleanup(func() {
		slog.SetDefault(oldLogger)
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	})
	for _, scenario := range []struct {
		mode    AuthMode
		replace bool
	}{{APIKey, false}, {AuthToken, false}, {APIKey, true}, {AuthToken, true}} {
		name := string(scenario.mode)
		if scenario.replace {
			name += "-replaced-revision"
		}
		test.Run(name, func(test *testing.T) {
			mode := scenario.mode
			directory := test.TempDir()
			if err := os.Chmod(directory, 0700); err != nil {
				test.Fatal(err)
			}
			master := bytes.Repeat([]byte{81}, 32)
			store, err := state.Open(test.Context(), filepath.Join(directory, "native.db"), master)
			if err != nil {
				test.Fatal(err)
			}
			defer store.Close()
			owner, _ := auth.NewService(store, master)
			parent, ownerID, err := owner.LoginOIDC(test.Context(), "https://identity.example", "synthetic-subject", "", "")
			if err != nil {
				test.Fatal(err)
			}
			consumer, _ := auth.NewNativeConsumerService(owner, master, "https://cognate.example")
			registry, _ := agentadapter.Builtins()
			service, err := NewNativeService(store, consumer, registry)
			if err != nil {
				test.Fatal(err)
			}
			var operation [16]byte
			_, _ = rand.Read(operation[:])
			key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32))
			start := state.NativeConsumerStart{ExpectedOwner: ownerID, Origin: "https://cognate.example", Purpose: state.NativeConfigurationApply, OperationID: operation[:], PublicKey: key.Public().(ed25519.PublicKey), ClientNonce: bytes.Repeat([]byte{2}, 32)}
			challenge, err := consumer.Begin(test.Context(), parent.Reveal(), start)
			if err != nil {
				test.Fatal(err)
			}
			fields, _ := challenge.BindFields()
			bindTranscript, err := configclient.BindTranscript(&nativev1.BeginNativeConsumerRequest{Purpose: 1, ExpectedOwnerId: ownerID, ExpectedServerOrigin: start.Origin, OperationId: start.OperationID, ConsumerPublicKey: start.PublicKey, ClientNonce: start.ClientNonce}, &nativev1.BeginNativeConsumerResponse{OwnerId: ownerID, ServerOrigin: start.Origin, ChallengeId: fields.ChallengeID, Challenge: fields.Challenge[:], NativeSessionBinding: fields.NativeSessionBinding[:], ChallengeExpiresAtUnixMs: fields.ChallengeExpiresAtUnixMS, NativeSessionExpiresAtUnixMs: fields.NativeSessionExpiresAtUnixMS})
			if err != nil {
				test.Fatal(err)
			}
			issued, err := consumer.Bind(test.Context(), parent.Reveal(), fields.ChallengeID, ed25519.Sign(key, bindTranscript.CopyBytes()))
			if err != nil {
				test.Fatal(err)
			}
			slot, wireSlot := APIKeySlot, uint32(1)
			if mode == AuthToken {
				slot, wireSlot = AuthTokenSlot, 2
			}
			sentinel := "synthetic-native-slot-secret-" + string(mode)
			domain := New(store)
			asset, err := domain.Create(test.Context(), ownerID, Draft{Name: "synthetic", Provider: ClaudeProvider{AuthMode: mode, Models: ClaudeModels{Default: new("synthetic-model")}}}, []SecretMutation{{Slot: slot, Action: Replace, Replacement: NewSecret([]byte(sentinel))}})
			if err != nil {
				test.Fatal(err)
			}
			adapter := registry.List()[0]
			for _, descriptor := range registry.List() {
				if descriptor.Manifest.PluginID == "builtin.claude" {
					adapter = descriptor
				}
			}
			request := nativesessionauth.PrepareRequest{OwnerID: ownerID, AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, AgentID: "claude", SchemaVersion: 1, Adapter: nativesessionauth.AdapterFields{APIVersion: 1, PluginID: adapter.Manifest.PluginID, PluginVersion: adapter.Manifest.PluginVersion, DefinitionDigest: adapter.DefinitionDigest, Capability: "claude.provider-switch.v1"}, Target: 1, Scope: 1, ProfileID: "profile", SelectionRevision: 1, OperationID: operation}
			wireRequest := wirePrepareRequest(request)
			consumerBinding := configclient.ConsumerBinding{OwnerID: ownerID, ServerOrigin: start.Origin, NativeSessionBinding: fields.NativeSessionBinding[:], OperationID: operation[:], ConsumerKeySHA256: consumerKeyHash(start.PublicKey)}
			transcript, err := configclient.PrepareTranscript(consumerBinding, wireRequest)
			if err != nil {
				test.Fatal(err)
			}
			prepared, err := service.Prepare(test.Context(), issued.Reveal(), request, ed25519.Sign(key, transcript.CopyBytes()))
			if err != nil {
				test.Fatal(err)
			}
			binding := wirePreparation(prepared.Binding())
			authorize := nativesessionauth.AuthorizeRequest{PrepareID: prepared.Binding().PrepareID, OperationID: operation, RequiredSlot: wireSlot, Consent: nativesessionauth.ConsentFields{Kind: 1, Revision: 1}, IntentExpiresMS: prepared.Binding().ExpiresMS}
			challengeBytes := prepared.Challenge()
			transcript, err = configclient.AuthorizeTranscript(binding, challengeBytes[:], &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: authorize.PrepareID, OperationId: operation[:], IntentCommitment: authorize.Commitment[:], RequestedSlots: []nativev1.SecretSlot{nativev1.SecretSlot(wireSlot)}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 1}}, ClientNonce: authorize.Nonce[:], IntentExpiresAtUnixMs: authorize.IntentExpiresMS})
			if err != nil {
				test.Fatal(err)
			}
			authorizeSignature := ed25519.Sign(key, transcript.CopyBytes())
			grant, err := service.Authorize(test.Context(), issued.Reveal(), authorize, authorizeSignature)
			if err != nil || len(grant.Reveal()) != 32 {
				test.Fatalf("no grant: %v", err)
			}
			defer grant.Destroy()
			if replayed, err := service.Authorize(test.Context(), issued.Reveal(), authorize, authorizeSignature); err == nil || len(replayed.Reveal()) != 0 {
				test.Fatal("confirmed authorization reissued its raw grant")
			}
			tokenBytes := grant.Reveal()
			parsed, _ := nativesessionauth.ParseGrantToken(tokenBytes)
			consume := nativesessionauth.ConsumeRequest{PrepareID: authorize.PrepareID, GrantID: grant.ID(), Token: parsed, OperationID: operation, Commitment: authorize.Commitment}
			consumeChallenge := grant.Challenge()
			transcript, err = configclient.ConsumeTranscript(wireApply(grant.Binding()), challengeBytes[:], consumeChallenge[:], grant.ExpiresMS(), &nativev1.ConsumeNativeMaterializationRequest{PrepareId: consume.PrepareID, GrantId: consume.GrantID, GrantToken: tokenBytes, OperationId: operation[:], IntentCommitment: consume.Commitment[:], ClientNonce: consume.Nonce[:]})
			if err != nil {
				test.Fatal(err)
			}
			signature := ed25519.Sign(key, transcript.CopyBytes())
			if scenario.replace {
				updated, err := domain.Update(test.Context(), ownerID, asset.Metadata.ID, asset.Metadata.Revision, Draft{Name: "synthetic", Provider: asset.Provider}, []SecretMutation{{Slot: slot, Action: Replace, Replacement: NewSecret([]byte("synthetic-new-credential-version"))}})
				if err != nil || updated.Metadata.Revision != asset.Metadata.Revision+1 {
					test.Fatal("typed replacement did not advance shared envelope CAS")
				}
				response, err := service.Consume(test.Context(), issued.Reveal(), consume, signature)
				if !errors.Is(err, state.ErrConfigurationConflict) || len(response.Reveal()) != 0 {
					test.Fatal("old grant consumed a new typed credential version")
				}
				return
			}
			response, err := service.Consume(test.Context(), issued.Reveal(), consume, signature)
			if err != nil {
				test.Fatal(err)
			}
			var decoded nativev1.ConsumeNativeMaterializationResponse
			if err := proto.Unmarshal(response.Reveal(), &decoded); err != nil {
				test.Fatal(err)
			}
			if len(decoded.SlotValues) != 1 || uint32(decoded.SlotValues[0].Slot) != wireSlot || string(decoded.SlotValues[0].Plaintext) != sentinel {
				test.Fatal("real selected secret not returned precisely")
			}
			if replay, err := service.Consume(test.Context(), issued.Reveal(), consume, signature); err == nil || len(replay.Reveal()) != 0 {
				test.Fatal("secret replayed")
			}
			statusRequest := nativesessionauth.StatusRequest{PrepareID: consume.PrepareID, OperationID: operation}
			transcript, err = configclient.StatusTranscript(binding, challengeBytes[:], &nativev1.GetNativeMaterializationStatusRequest{PrepareId: consume.PrepareID, OperationId: operation[:], ClientNonce: statusRequest.Nonce[:]})
			if err != nil {
				test.Fatal(err)
			}
			status, err := service.Status(test.Context(), issued.Reveal(), statusRequest, ed25519.Sign(key, transcript.CopyBytes()))
			if err != nil || status.State != 3 || status.GrantID != grant.ID() {
				test.Fatal("safe status did not observe durable one-time consumption")
			}
			for _, name := range []string{"native.db", "native.db-wal"} {
				data, err := os.ReadFile(filepath.Join(directory, name))
				if err != nil && !os.IsNotExist(err) {
					test.Fatal(err)
				}
				if bytes.Contains(data, []byte(sentinel)) || bytes.Contains(data, tokenBytes) {
					test.Fatal("secret/token persisted outside encrypted asset")
				}
			}
			if strings.Contains(captured.String(), sentinel) || strings.Contains(captured.String(), parent.Reveal()) || strings.Contains(captured.String(), issued.Reveal()) {
				test.Fatal("native flow logged sensitive values")
			}
			response.Destroy()
			grant.Destroy()
		})
	}
}
