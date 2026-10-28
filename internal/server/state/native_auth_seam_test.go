package state

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
)

func TestNativeApplyDirectStateRejectsSignedCallerSnapshot(test *testing.T) {
	for _, mode := range []string{"key", "binding", "owner", "adapter", "revision", "commitment", "expiry", "authorize-challenge", "consume-challenge", "grant", "issuer", "bearer"} {
		test.Run(mode, func(test *testing.T) {
			flow := newNativeConfigurationFlow(test)
			flow.authorize(test)
			fields := nativesessionauth.ConsumeFields{Apply: flow.authorization.Binding(), AuthorizeChallenge: flow.prepared.Challenge(), ConsumeChallenge: flow.authorization.Challenge(), GrantExpiresMS: flow.authorization.ExpiresMS(), Request: flow.consume}
			key, verifier, bearer := flow.key, flow.verifier, flow.bearer
			switch mode {
			case "key":
				key = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{99}, 32))
				fields.Apply.Preparation.Consumer.PublicKey = [32]byte(key.Public().(ed25519.PublicKey))
			case "binding":
				fields.Apply.Preparation.Consumer.SessionBinding[0]++
			case "owner":
				fields.Apply.Preparation.Consumer.OwnerID = "bob"
				fields.Apply.Preparation.Request.OwnerID = "bob"
			case "adapter":
				fields.Apply.Preparation.Request.Adapter.DefinitionDigest = strings.Repeat("b", 64)
			case "revision":
				fields.Apply.Preparation.Request.AssetRevision++
			case "commitment":
				fields.Apply.Commitment[0]++
				fields.Request.Commitment = fields.Apply.Commitment
			case "expiry":
				fields.Apply.IntentExpiresMS--
			case "authorize-challenge":
				fields.AuthorizeChallenge[0]++
			case "consume-challenge":
				fields.ConsumeChallenge[0]++
			case "grant":
				fields.Request.GrantID += "x"
			case "issuer":
				verifier, _ = nativesessionauth.NewVerifier(bytes.Repeat([]byte{99}, 32))
			case "bearer":
				bearer, _ = nativesessionauth.ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{99}, 32)))
			}
			apply := fields.Apply
			transcript, err := configclient.ConsumeTranscript(&nativev1.ApplyBinding{Preparation: nativeFlowWire(apply.Preparation), IntentCommitment: apply.Commitment[:], IntentExpiresAtUnixMs: apply.IntentExpiresMS}, fields.AuthorizeChallenge[:], fields.ConsumeChallenge[:], fields.GrantExpiresMS, &nativev1.ConsumeNativeMaterializationRequest{PrepareId: fields.Request.PrepareID, GrantId: fields.Request.GrantID, GrantToken: flow.token, OperationId: fields.Request.OperationID[:], IntentCommitment: fields.Request.Commitment[:], ClientNonce: fields.Request.Nonce[:]})
			if err != nil {
				test.Fatal(err)
			}
			proof, err := verifier.VerifyConsume(bearer, fields, ed25519.Sign(key, transcript.CopyBytes()))
			if err != nil {
				test.Fatal("test must possess a real signature on valid alternative canonical fields")
			}
			if snapshot, err := flow.fixture.store.PreflightNativeConfigurationConsume(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, proof); !errors.Is(err, ErrNativeUnauthorized) || snapshot.present() {
				test.Fatal("signed caller snapshot bypassed authoritative preflight")
			}
			response, err := flow.fixture.store.ConsumeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, proof, nativeStateMaterializer)
			if !errors.Is(err, ErrNativeUnauthorized) || len(response.Reveal()) != 0 {
				test.Fatal("signed caller snapshot bypassed authoritative state")
			}
			if _, err := flow.fixture.store.PreflightNativeConfigurationConsume(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof); err != nil {
				test.Fatal("invalid preflight burned a legitimate grant")
			}
			response, err = flow.fixture.store.ConsumeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof, nativeStateMaterializer)
			if err != nil || string(response.Reveal()) != "synthetic-state-secret-6d0" {
				test.Fatal("rejected proof burned legitimate grant")
			}
			response.Destroy()
		})
	}
}

func TestNativeApplyPreflightRejectsZeroProofAndPreservesStickyExpiry(test *testing.T) {
	flow := newNativeConfigurationFlow(test)
	if _, err := flow.fixture.store.PreflightNativeConfigurationPrepare(test.Context(), flow.credential, flow.requestOrigin(), flow.request, nativesessionauth.VerifiedApplyProof{}); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("prepare preflight trusted credential without stage proof")
	}
	request, proof := flow.authorizeEvidence(test)
	if _, err := flow.fixture.store.PreflightNativeConfigurationAuthorize(test.Context(), flow.credential, flow.requestOrigin(), request, nativesessionauth.VerifiedApplyProof{}); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("authorize preflight trusted credential without stage proof")
	}
	if _, err := flow.fixture.store.PreflightNativeConfigurationAuthorize(test.Context(), flow.credential, flow.requestOrigin(), request, proof); err != nil {
		test.Fatal("valid authorization preflight failed")
	}
	flow.authorize(test)
	if _, err := flow.fixture.store.PreflightNativeConfigurationConsume(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, nativesessionauth.VerifiedApplyProof{}); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("consume preflight trusted credential without stage proof")
	}
	flow.fixture.clock.Store(time.UnixMilli(flow.authorization.ExpiresMS()).UnixNano())
	if _, err := flow.fixture.store.PreflightNativeConfigurationConsume(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof); !errors.Is(err, ErrNativePrecondition) {
		test.Fatal("expired preflight was not rejected")
	}
	flow.fixture.clock.Add(-int64(time.Second))
	if _, err := flow.fixture.store.PreflightNativeConfigurationConsume(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof); !errors.Is(err, ErrNativePrecondition) {
		test.Fatal("preflight expiry revived after clock rollback")
	}
	if response, err := flow.fixture.store.ConsumeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof, nativeStateMaterializer); !errors.Is(err, ErrNativePrecondition) || len(response.Reveal()) != 0 {
		test.Fatal("final transaction ignored preflight terminal expiry")
	}
}

func seamParent(test *testing.T, fixture nativeFixture, owner string, nonce byte) (OwnerSessionCredential, nativesessionauth.VerifiedOwnerSession) {
	test.Helper()
	user, err := findUser(test.Context(), fixture.store.state.client, owner)
	if ent.IsNotFound(err) {
		if err := fixture.store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
		user, err = findUser(test.Context(), fixture.store.state.client, owner)
	}
	if err != nil {
		test.Fatal(err)
	}
	signing, err := hkdf.Key(sha256.New, fixture.key, nil, "skillhub/session-signing/v1", 32)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(signing)
	rawNonce := bytes.Repeat([]byte{nonce}, 32)
	mac := hmac.New(sha256.New, signing)
	_, _ = mac.Write(rawNonce)
	raw := base64.RawURLEncoding.EncodeToString(rawNonce) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	digest := sha256.Sum256([]byte(raw))
	if _, err := fixture.store.state.client.Session.Create().SetUserID(user.ID).SetTokenHash(digest[:]).SetIssuedAt(fixture.store.state.now().Add(-time.Second)).SetExpiresAt(fixture.store.state.now().Add(time.Hour)).Save(test.Context()); err != nil {
		test.Fatal(err)
	}
	verifier, err := nativesessionauth.NewVerifier(fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	evidence, err := verifier.VerifyOwner(raw)
	if err != nil {
		test.Fatal(err)
	}
	credential, err := fixture.store.OwnerCredentialFromVerified(evidence)
	if err != nil {
		test.Fatal(err)
	}
	return credential, evidence
}

func TestNativeAuthSeamAuthoritativeFieldsAndExactParent(test *testing.T) {
	fixture := newNativeFixture(test)
	parent, evidence := seamParent(test, fixture, "alice", 8)
	otherParent, otherEvidence := seamParent(test, fixture, "alice", 9)
	snapshot, key := seamChallenge(test, fixture, parent)
	fields, _ := snapshot.BindFields()
	verifier, _ := nativesessionauth.NewVerifier(fixture.key)
	otherProof, err := verifier.VerifyBind(otherEvidence, fields, seamSignature(fields, key))
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.BindingProofFromVerified(otherParent, snapshot, otherProof); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("snapshot rebound to different exact parent before transaction")
	}
	if _, err := fixture.store.NativeBindChallenge(test.Context(), otherParent, fields.ChallengeID); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("other session read challenge")
	}
	for _, change := range []func(*nativesessionauth.BindFields){
		func(fields *nativesessionauth.BindFields) { fields.ServerOrigin = "https://other.example" },
		func(fields *nativesessionauth.BindFields) { fields.OwnerID = "bob" },
		func(fields *nativesessionauth.BindFields) { fields.OperationID[0]++ },
		func(fields *nativesessionauth.BindFields) { fields.ClientNonce[0]++ },
		func(fields *nativesessionauth.BindFields) { fields.ChallengeID += "x" },
		func(fields *nativesessionauth.BindFields) { fields.Challenge[0]++ },
		func(fields *nativesessionauth.BindFields) { fields.NativeSessionBinding[0]++ },
		func(fields *nativesessionauth.BindFields) { fields.ChallengeExpiresAtUnixMS-- },
		func(fields *nativesessionauth.BindFields) { fields.NativeSessionExpiresAtUnixMS++ },
		func(fields *nativesessionauth.BindFields) {
			other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32))
			fields.ConsumerPublicKey = [32]byte(other.Public().(ed25519.PublicKey))
		},
	} {
		altered := fields
		change(&altered)
		signer := key
		if altered.ConsumerPublicKey != fields.ConsumerPublicKey {
			signer = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32))
		}
		proof, err := verifier.VerifyBind(evidence, altered, seamSignature(altered, signer))
		if err != nil {
			test.Fatal("bridge rejected legitimately signed alternative fields")
		}
		if _, err := fixture.store.BindingProofFromVerified(parent, snapshot, proof); !errors.Is(err, ErrNativeUnauthorized) {
			test.Fatal("signed caller fields replaced authoritative snapshot")
		}
	}
	fields.ClientNonce[0]++
	fresh, _ := snapshot.BindFields()
	if fresh.ClientNonce == fields.ClientNonce {
		test.Fatal("accessor mutated private snapshot")
	}
	if _, err := fixture.store.BindingProofFromVerified(parent, NativeBindSnapshot{}, otherProof); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("zero snapshot accepted")
	}
	if _, err := fixture.store.BindingProofFromVerified(parent, snapshot, nativesessionauth.VerifiedBindProof{}); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("zero proof accepted")
	}
	if nativeTestCounts(test, fixture.store)[1] != 0 {
		test.Fatal("verification factory created child")
	}
}

func TestNativeAuthSeamKeyAnchorEvenWithMatchingDatabaseDigest(test *testing.T) {
	target := newNativeFixture(test)
	foreign := newNativeFixture(test)
	_, evidence := seamParent(test, foreign, "alice", 8)
	digest, _ := evidence.TokenDigest()
	user, err := findUser(test.Context(), target.store.state.client, "alice")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := target.store.state.client.Session.Create().SetUserID(user.ID).SetTokenHash(digest[:]).SetIssuedAt(target.store.state.now().Add(-time.Second)).SetExpiresAt(target.store.state.now().Add(time.Hour)).Save(test.Context()); err != nil {
		test.Fatal(err)
	}
	if _, err := target.store.OwnerCredentialFromVerified(evidence); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("foreign master accepted despite target keyID anchor")
	}
	if _, err := target.store.OwnerCredentialFromVerified(nativesessionauth.VerifiedOwnerSession{}); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("zero owner evidence accepted")
	}
	if _, err := target.store.NativeCredentialFromBearer(nativesessionauth.ParsedNativeBearer{}); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("zero native bearer accepted")
	}
}

func TestNativeAuthSeamFinalTransactionAndRealUnknown(test *testing.T) {
	for _, mode := range []string{"revoke", "expire", "cancel", "expire-at-commit", "replace-internal-issued", "commit-unknown"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newNativeFixture(test)
			parent, evidence := seamParent(test, fixture, "alice", 8)
			snapshot, key := seamChallenge(test, fixture, parent)
			fields, _ := snapshot.BindFields()
			verifier, _ := nativesessionauth.NewVerifier(fixture.key)
			verified, _ := verifier.VerifyBind(evidence, fields, seamSignature(fields, key))
			proof, err := fixture.store.BindingProofFromVerified(parent, snapshot, verified)
			if err != nil {
				test.Fatal(err)
			}
			parsed, _ := nativesessionauth.ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32)))
			credential, _ := fixture.store.NativeCredentialFromBearer(parsed)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			var commits atomic.Int64
			switch mode {
			case "revoke":
				peer := nativePeer(test, fixture)
				digest, _ := evidence.TokenDigest()
				if err := peer.RevokeSession(ctx, digest[:]); err != nil {
					test.Fatal(err)
				}
			case "expire":
				fixture.clock.Store(time.UnixMilli(fields.ChallengeExpiresAtUnixMS).UnixNano())
			case "cancel":
				cancel()
			case "expire-at-commit":
				fixture.store.state.client.NativeConsumerSession.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
						value, err := next.Mutate(ctx, mutation)
						fixture.clock.Store(time.UnixMilli(fields.ChallengeExpiresAtUnixMS).UnixNano())
						return value, err
					})
				})
			case "replace-internal-issued":
				database, err := sql.Open("sqlite3", fixture.path)
				if err != nil {
					test.Fatal(err)
				}
				defer database.Close()
				if _, err := database.ExecContext(ctx, "UPDATE native_consumer_challenges SET issued_ms = issued_ms + 1 WHERE public_id = ?", fields.ChallengeID); err != nil {
					test.Fatal(err)
				}
				fixture.clock.Add(int64(2 * time.Millisecond))
			case "commit-unknown":
				fixture.store.state.client.NativeConsumerSession.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
						tx, err := mutation.(*ent.NativeConsumerSessionMutation).Tx()
						if err != nil {
							return nil, err
						}
						tx.OnCommit(func(next ent.Committer) ent.Committer {
							return ent.CommitFunc(func(ctx context.Context, tx *ent.Tx) error {
								commits.Add(1)
								if err := next.Commit(ctx, tx); err != nil {
									return err
								}
								return errors.New("synthetic-secret-error-must-not-echo")
							})
						})
						return next.Mutate(ctx, mutation)
					})
				})
			}
			identity, err := fixture.store.BindNativeConsumer(ctx, parent, NativeConsumerBind{ChallengeID: fields.ChallengeID, Proof: proof, TokenHash: credential})
			if identity.ID() != "" {
				test.Fatal("failure returned child identity")
			}
			wantChildren := 0
			switch mode {
			case "commit-unknown":
				if !errors.Is(err, ErrNativeOutcomeUnknown) || commits.Load() != 1 || strings.Contains(err.Error(), "must-not-echo") {
					test.Fatal("unknown result leaked, retried or misclassified")
				}
				wantChildren = 1
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					test.Fatal("canceled Bind accepted")
				}
			default:
				if !errors.Is(err, ErrNativeUnauthorized) {
					test.Fatal("stale authority accepted")
				}
			}
			if nativeTestCounts(test, fixture.store)[1] != wantChildren {
				test.Fatal("real COMMIT/rollback reality mismatch")
			}
		})
	}
}

func TestNativeAuthSeamTwoStoresConsumeOnce(test *testing.T) {
	fixture := newNativeFixture(test)
	parent, evidence := seamParent(test, fixture, "alice", 8)
	first, key := seamChallenge(test, fixture, parent)
	fields, _ := first.BindFields()
	peer := nativePeer(test, fixture)
	second, err := peer.NativeBindChallenge(test.Context(), parent, fields.ChallengeID)
	if err != nil {
		test.Fatal(err)
	}
	verifier, _ := nativesessionauth.NewVerifier(fixture.key)
	verified, _ := verifier.VerifyBind(evidence, fields, seamSignature(fields, key))
	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var workers sync.WaitGroup
	for index, store := range []*Store{fixture.store, peer} {
		snapshot := []NativeBindSnapshot{first, second}[index]
		proof, err := store.BindingProofFromVerified(parent, snapshot, verified)
		if err != nil {
			test.Fatal(err)
		}
		parsed, _ := nativesessionauth.ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(index + 10)}, 32)))
		credential, _ := store.NativeCredentialFromBearer(parsed)
		workers.Go(func() {
			<-start
			_, err := store.BindNativeConsumer(test.Context(), parent, NativeConsumerBind{ChallengeID: fields.ChallengeID, Proof: proof, TokenHash: credential})
			errorsFound <- err
		})
	}
	close(start)
	workers.Wait()
	close(errorsFound)
	wins := 0
	for err := range errorsFound {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrNativeUnauthorized) {
			test.Fatalf("unexpected race failure: %v", err)
		}
	}
	if wins != 1 || nativeTestCounts(test, fixture.store)[1] != 1 {
		test.Fatal("two stores consumed challenge more than once")
	}
}

func TestNativeAuthSeamSnapshotDefaultPrivacy(test *testing.T) {
	fixture := newNativeFixture(test)
	parent, _ := seamParent(test, fixture, "alice", 8)
	snapshot, _ := seamChallenge(test, fixture, parent)
	secret := []byte("synthetic-native-snapshot-secret!")
	secret = secret[:32]
	copy(snapshot.box.value.Challenge.Challenge[:], secret)
	for _, value := range []any{snapshot, struct{ hidden NativeBindSnapshot }{snapshot}, struct{ hidden any }{snapshot}, []any{snapshot}} {
		for _, verb := range "vTtbcdoOqxXUeEfFgGspw" {
			for _, flags := range []string{"", "+", "#", "020.10"} {
				output := fmt.Sprintf("%"+flags+string(verb), value)
				if strings.Contains(output, string(secret)) || strings.Contains(output, fmt.Sprint(secret)) || strings.Contains(output, fmt.Sprintf("%x", secret)) || strings.Contains(output, fmt.Sprintf("%X", secret)) {
					test.Fatal("snapshot leaked via fmt")
				}
			}
		}
		for _, json := range []bool{false, true} {
			var output bytes.Buffer
			var handler slog.Handler = slog.NewTextHandler(&output, nil)
			if json {
				handler = slog.NewJSONHandler(&output, nil)
			}
			slog.New(handler).Info("synthetic", slog.Any("snapshot", value))
			if strings.Contains(output.String(), string(secret)) {
				test.Fatal("snapshot leaked via slog")
			}
		}
	}
}

func seamSignature(fields nativesessionauth.BindFields, key ed25519.PrivateKey) []byte {
	var encoded bytes.Buffer
	writeBlob := func(value []byte) {
		_ = binary.Write(&encoded, binary.BigEndian, uint32(len(value)))
		_, _ = encoded.Write(value)
	}
	writeBlob([]byte("cognate/native-config-proof/v1"))
	writeBlob([]byte("bind"))
	_ = binary.Write(&encoded, binary.BigEndian, uint32(1))
	writeBlob([]byte(fields.ServerOrigin))
	writeBlob([]byte(fields.OwnerID))
	_ = binary.Write(&encoded, binary.BigEndian, fields.Purpose)
	writeBlob(fields.OperationID[:])
	writeBlob(fields.ConsumerPublicKey[:])
	writeBlob(fields.ClientNonce[:])
	writeBlob([]byte(fields.ChallengeID))
	writeBlob(fields.Challenge[:])
	writeBlob(fields.NativeSessionBinding[:])
	_ = binary.Write(&encoded, binary.BigEndian, uint64(fields.ChallengeExpiresAtUnixMS))
	_ = binary.Write(&encoded, binary.BigEndian, uint64(fields.NativeSessionExpiresAtUnixMS))
	return ed25519.Sign(key, encoded.Bytes())
}

func seamChallenge(test *testing.T, fixture nativeFixture, parent OwnerSessionCredential) (NativeBindSnapshot, ed25519.PrivateKey) {
	test.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	start := nativeTestStart()
	start.PublicKey = key.Public().(ed25519.PublicKey)
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), parent, start)
	if err != nil {
		test.Fatal(err)
	}
	snapshot, err := fixture.store.NativeBindChallenge(test.Context(), parent, challenge.ID())
	if err != nil {
		test.Fatal(err)
	}
	return snapshot, key
}

func TestNativeAuthSeamVerifiedLifecycle(test *testing.T) {
	fixture := newNativeFixture(test)
	parent, evidence := seamParent(test, fixture, "alice", 8)
	snapshot, key := seamChallenge(test, fixture, parent)
	fields, err := snapshot.BindFields()
	if err != nil {
		test.Fatal(err)
	}
	verifier, _ := nativesessionauth.NewVerifier(fixture.key)
	verified, err := verifier.VerifyBind(evidence, fields, seamSignature(fields, key))
	if err != nil {
		test.Fatal(err)
	}
	proof, err := fixture.store.BindingProofFromVerified(parent, snapshot, verified)
	if err != nil {
		test.Fatal(err)
	}
	parsed, _ := nativesessionauth.ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32)))
	credential, err := fixture.store.NativeCredentialFromBearer(parsed)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.BindNativeConsumer(test.Context(), parent, NativeConsumerBind{ChallengeID: fields.ChallengeID, TokenHash: credential, Proof: proof}); err != nil {
		test.Fatal(err)
	}
	identity, err := fixture.store.AuthenticateNativeConsumer(test.Context(), credential, fields.ServerOrigin, fields.OperationID[:])
	if err != nil || identity.Owner() != "alice" {
		test.Fatal("verified bridge did not reach real child state")
	}
	digest, _ := evidence.TokenDigest()
	if err := fixture.store.RevokeSession(test.Context(), digest[:]); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.AuthenticateNativeConsumer(test.Context(), credential, fields.ServerOrigin, fields.OperationID[:]); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("real parent deletion left child authorized")
	}
}
