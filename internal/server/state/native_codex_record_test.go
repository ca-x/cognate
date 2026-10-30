package state

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeapplypreparation"
	"github.com/ca-x/cognate/internal/server/ent/nativeconsumersession"
)

type codexRecordGolden struct {
	PublicKey string `json:"public_key"`
	Compact   map[string]struct {
		Hex   string `json:"hex"`
		Bytes int    `json:"bytes"`
		Hash  string `json:"sha256"`
	} `json:"compact"`
}

func codexRecordFixture(test *testing.T) (codexRecordGolden, nativesessionauth.ConsumerFields) {
	test.Helper()
	encoded, err := os.ReadFile("../../configclient/testdata/native-codex-proof-v1.json")
	var golden codexRecordGolden
	if err != nil || json.Unmarshal(encoded, &golden) != nil {
		test.Fatal("cannot load compact literal")
	}
	public, err := hex.DecodeString(golden.PublicKey)
	if err != nil || len(public) != 32 {
		test.Fatal("invalid synthetic public key")
	}
	return golden, nativesessionauth.ConsumerFields{OwnerID: "owner-synthetic", ServerOrigin: "https://proof.example:8443", SessionBinding: [32]byte(bytes.Repeat([]byte{5}, 32)), OperationID: [16]byte(bytes.Repeat([]byte{1}, 16)), PublicKey: [32]byte(public)}
}

func TestCodexNativeCompactGolden(test *testing.T) {
	golden, consumer := codexRecordFixture(test)
	for name, vector := range golden.Compact {
		test.Run(name, func(test *testing.T) {
			plaintext, err := hex.DecodeString(vector.Hex)
			if err != nil {
				test.Fatal("invalid literal hex")
			}
			record, err := nativeDecodeCodexRecord(plaintext, consumer)
			if err != nil {
				test.Fatal("literal compact rejected")
			}
			actual, err := nativeEncodeCodexRecord(record)
			hash := sha256.Sum256(actual)
			if err != nil || len(actual) != vector.Bytes || hex.EncodeToString(hash[:]) != vector.Hash || !bytes.Equal(actual, plaintext) {
				test.Fatal("compact literal byte mismatch")
			}
			nonce := byte(6)
			if name == "unbound_nonce22" || name == "bound_nonce22" {
				nonce = 34
			}
			if record.preparation.CopyRequest().Nonce != [32]byte(bytes.Repeat([]byte{nonce}, 32)) {
				test.Fatal("authenticated original Prepare nonce lost")
			}
			actual[0] ^= 1
			second, _ := nativeEncodeCodexRecord(record)
			if !bytes.Equal(second, plaintext) {
				test.Fatal("canonical copy aliases record")
			}
			for _, bad := range [][]byte{plaintext[:len(plaintext)-1], append(bytes.Clone(plaintext), 0), append([]byte("CX1\x00"), plaintext[4:]...)} {
				result, err := nativeDecodeCodexRecord(bad, consumer)
				if err == nil || result.preparation.CopyWire() != nil {
					test.Fatal("damaged compact accepted")
				}
			}
			nonceOffset := 12 + int(binary.BigEndian.Uint32(plaintext[8:12]))
			for _, size := range []uint32{0, 31, 33, 0xffffffff} {
				bad := bytes.Clone(plaintext)
				binary.BigEndian.PutUint32(bad[nonceOffset:nonceOffset+4], size)
				if _, err := nativeDecodeCodexRecord(bad, consumer); err == nil {
					test.Fatal("non-exact authenticated Prepare nonce length accepted")
				}
			}
			foreign := consumer
			foreign.PublicKey[0] ^= 1
			if _, err := nativeDecodeCodexRecord(plaintext, foreign); err == nil {
				test.Fatal("foreign authenticated consumer accepted")
			}
		})
	}
}

func codexStoredRecord(test *testing.T, bound bool) (nativeConfigurationFlow, *ent.NativeApplyPreparation, nativeCodexRecord) {
	test.Helper()
	flow := newNativeConfigurationFlow(test)
	if _, err := flow.fixture.store.state.client.NativeApplyPreparation.Delete().Exec(test.Context()); err != nil {
		test.Fatal("cannot remove unrelated fixture preparation")
	}
	consumer := flow.prepared.Binding().Consumer
	asset, err := flow.fixture.store.CreateConfigurationAsset(test.Context(), consumer.OwnerID, ConfigurationAssetDraft{AgentID: "codex", Kind: ConfigurationKindProvider, Name: "synthetic", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic-codec-record-payload"))})
	if err != nil {
		test.Fatal("cannot create synthetic Codex asset")
	}
	request := nativesessionauth.CodexPrepareRequest{PrepareRequest: flow.request, ContractVersion: 1}
	request.AssetID = asset.ID
	request.AgentID = "codex"
	request.Target = 2
	request.Adapter = nativesessionauth.AdapterFields{APIVersion: 1, PluginID: "builtin.codex", PluginVersion: "1.1.0", DefinitionDigest: "f45d146803f9f4f818a5d17ea1cc536b27bc77f49eb059c10c2f305912af02b4", Capability: "codex.provider-plan.v1"}
	request.Nonce = [32]byte(bytes.Repeat([]byte{6}, 32))
	golden, original := codexRecordFixture(test)
	literal, _ := hex.DecodeString(golden.Compact["bound"].Hex)
	projected, err := nativeDecodeCodexRecord(literal, original)
	if err != nil {
		test.Fatal("cannot load literal binding")
	}
	now := flow.fixture.store.state.now().UTC()
	digest := projected.apply.LocalBinding().DeclarationSHA256()
	preparation, err := nativesessionauth.NewCodexPreparation(consumer, request, "BBBBBBBBBBBBBBBBBBBBBBBBBB", now.UnixMilli(), flow.prepared.Binding().ExpiresMS, digest)
	if err != nil {
		test.Fatal("cannot build actual-consumer preparation")
	}
	record := nativeCodexRecord{preparation: preparation}
	phase := nativeapplypreparation.PhasePatchReady
	if bound {
		record.apply, err = nativesessionauth.NewCodexApply(preparation, projected.apply.LocalBinding(), 1, now.Add(time.Minute).UnixMilli())
		if err != nil {
			test.Fatal("cannot build bound compact")
		}
		phase = nativeapplypreparation.PhaseBoundWithGrant
	}
	stage, err := nativesessionauth.CodexPrepareFieldsDigest(nativesessionauth.CodexPrepareFields{Consumer: consumer, Request: request})
	child, err := flow.fixture.store.state.client.NativeConsumerSession.Query().Only(test.Context())
	if err != nil {
		test.Fatal("cannot read bound native child")
	}
	row := &ent.NativeApplyPreparation{PublicID: "BBBBBBBBBBBBBBBBBBBBBBBBBB", UserID: child.UserID, NativeSessionID: child.ID, OperationID: bytes.Clone(consumer.OperationID[:]), RequestDigest: bytes.Clone(stage[:]), AuthorizeChallenge: bytes.Repeat([]byte{11}, 32), Phase: phase, IssuedMs: now.UnixMilli(), ExpiresMs: preparation.CopyWire().PrepareExpiresAtUnixMs}
	if err != nil || flow.fixture.store.nativeSealCodexPrepared(row, consumer, record) != nil {
		test.Fatal("cannot seal compact")
	}
	if len(row.Ciphertext) != lenMustCodexRecord(test, record)+flow.fixture.store.state.configurationAEAD.Overhead() || row.Charge != nativePreparationCharge(row) || row.Charge > 16<<10 {
		test.Fatal("actual AEAD/row charge mismatch")
	}
	stored, err := flow.fixture.store.state.client.NativeApplyPreparation.Create().SetPublicID(row.PublicID).SetUserID(row.UserID).SetNativeSessionID(row.NativeSessionID).SetOperationID(row.OperationID).SetRequestDigest(row.RequestDigest).SetAuthorizeChallenge(row.AuthorizeChallenge).SetCiphertext(row.Ciphertext).SetPhase(row.Phase).SetIssuedMs(row.IssuedMs).SetExpiresMs(row.ExpiresMs).SetCharge(row.Charge).Save(test.Context())
	if err != nil {
		test.Fatal("cannot persist sealed compact")
	}
	return flow, stored, record
}

func lenMustCodexRecord(test *testing.T, record nativeCodexRecord) int {
	test.Helper()
	encoded, err := nativeEncodeCodexRecord(record)
	defer clear(encoded)
	if err != nil {
		test.Fatal("record unexpectedly invalid")
	}
	return len(encoded)
}

func codexLoadTestRecord(ctx context.Context, store *Store, flow nativeConfigurationFlow, id string) (nativeCodexRecord, error) {
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (nativeCodexRecord, func(time.Time) error, error) {
		child, parent, gate, err := nativeConsumerGate(ctx, tx, flow.credential, flow.requestOrigin(), flow.request.OperationID, store.state.now)
		if err != nil {
			return nativeCodexRecord{}, nil, err
		}
		_, record, err := store.nativeLoadCodexPrepared(ctx, tx, child, nativeConsumerFields(child, parent), id)
		return record, gate, err
	})
}

func TestCodexNativePrepareNonceRestart(test *testing.T) {
	for _, bound := range []bool{false, true} {
		test.Run(map[bool]string{false: "unbound", true: "bound"}[bound], func(test *testing.T) {
			flow, row, original := codexStoredRecord(test, bound)
			if err := flow.fixture.store.Close(); err != nil {
				test.Fatal("close failed")
			}
			reopened, err := Open(test.Context(), flow.fixture.path, flow.fixture.key)
			if err != nil {
				test.Fatal("real SQLite restart failed")
			}
			test.Cleanup(func() { _ = reopened.Close() })
			reopened.state.now = func() time.Time { return time.Unix(0, flow.fixture.clock.Load()).UTC() }
			loaded, err := codexLoadTestRecord(test.Context(), reopened, flow, row.PublicID)
			if err != nil || loaded.preparation.CopyRequest() != original.preparation.CopyRequest() {
				test.Fatal("restart lost original Prepare input snapshot")
			}
			request := loaded.preparation.CopyRequest()
			digest, err := nativesessionauth.CodexPrepareFieldsDigest(nativesessionauth.CodexPrepareFields{Consumer: loaded.preparation.CopyConsumer(), Request: request})
			if err != nil || !bytes.Equal(digest[:], row.RequestDigest) {
				test.Fatal("restart cannot reconstruct original stage evidence")
			}
			request.Nonce[0] ^= 1
			different, _ := nativesessionauth.CodexPrepareFieldsDigest(nativesessionauth.CodexPrepareFields{Consumer: loaded.preparation.CopyConsumer(), Request: request})
			if bytes.Equal(different[:], row.RequestDigest) {
				test.Fatal("different nonce collapsed to same stage")
			}
		})
	}
}

func TestCodexNativeRequestDigestTamper(test *testing.T) {
	for _, bound := range []bool{false, true} {
		test.Run(map[bool]string{false: "unbound", true: "bound"}[bound], func(test *testing.T) {
			flow, row, _ := codexStoredRecord(test, bound)
			if _, err := codexLoadTestRecord(test.Context(), flow.fixture.store, flow, row.PublicID); err != nil {
				test.Fatal("positive load failed")
			}
			tampered := bytes.Clone(row.RequestDigest)
			tampered[0] ^= 1
			database, err := sql.Open("sqlite3", flow.fixture.path)
			if err != nil {
				test.Fatal("cannot open isolated tamper fixture")
			}
			defer database.Close()
			if _, err := database.ExecContext(test.Context(), "UPDATE native_apply_preparations SET request_digest=? WHERE id=?", tampered, row.ID); err != nil {
				test.Fatal("tamper setup failed")
			}
			child, err := flow.fixture.store.state.client.NativeConsumerSession.Query().Only(test.Context())
			if err != nil {
				test.Fatal("cannot capture child gate")
			}
			result, err := codexLoadTestRecord(test.Context(), flow.fixture.store, flow, row.PublicID)
			if !errors.Is(err, ErrNativeUnavailable) || result.preparation.CopyWire() != nil {
				test.Fatal("corrupted external row digest not rejected as Unavailable")
			}
			after, err := flow.fixture.store.state.client.NativeApplyPreparation.Get(test.Context(), row.ID)
			if err != nil || !bytes.Equal(after.RequestDigest, tampered) || !bytes.Equal(after.Ciphertext, row.Ciphertext) || after.Charge != row.Charge {
				test.Fatal("corrupt row was silently repaired or changed")
			}
			updated, err := flow.fixture.store.state.client.NativeConsumerSession.Query().Where(nativeconsumersession.IDEQ(child.ID)).Only(test.Context())
			if err != nil || updated.GateVersion != child.GateVersion {
				test.Fatal("failed record read committed gate mutation")
			}
		})
	}
}

type codexRecordChildInput struct {
	Path        string
	Key         []byte
	Origin      string
	Operation   [16]byte
	PrepareID   string
	Now         int64
	Bound       bool
	Declaration agentconfig.CodexProviderDeclarationV1
}

func TestCodexNativePrepareNonceProcessRestart(test *testing.T) {
	for _, bound := range []bool{false, true} {
		test.Run(map[bool]string{false: "unbound", true: "bound"}[bound], func(test *testing.T) {
			flow := newCodexStateFlow(test)
			flow.prepare(test)
			if bound {
				flow.issue(test)
			}
			input := codexRecordChildInput{Path: flow.base.fixture.path, Key: bytes.Clone(flow.base.fixture.key), Origin: flow.consumer.ServerOrigin, Operation: flow.request.OperationID, PrepareID: flow.prepared.Binding().CopyWire().PrepareId, Now: flow.base.fixture.clock.Load(), Bound: bound, Declaration: flow.prepared.CopyDeclaration()}
			encoded, err := json.Marshal(input)
			if err != nil {
				test.Fatal("cannot encode synthetic restart fixture")
			}
			defer clear(encoded)
			path := filepath.Join(test.TempDir(), "synthetic-child.json")
			if os.WriteFile(path, encoded, 0o600) != nil {
				test.Fatal("cannot create private child fixture")
			}
			if flow.base.fixture.store.Close() != nil {
				test.Fatal("cannot close prior SQLite writer")
			}
			ctx, cancel := context.WithTimeout(test.Context(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeCodexRecordChild$", "-test.count=1", "-test.v")
			command.Env = append(os.Environ(), "COGNATE_C2_RECORD_CHILD="+path, "CGO_ENABLED=0")
			output, err := command.CombinedOutput()
			if err != nil || !bytes.Contains(output, []byte("--- PASS: TestNativeCodexRecordChild")) || bytes.Contains(output, []byte("SKIP")) {
				test.Fatal("real process restart failed or did not run child oracle")
			}
		})
	}
}

func TestNativeCodexRecordChild(test *testing.T) {
	path := os.Getenv("COGNATE_C2_RECORD_CHILD")
	if path == "" {
		test.Skip("subprocess entry point, exercised by Codex process-restart oracle")
	}
	encoded, err := os.ReadFile(path)
	defer clear(encoded)
	var input codexRecordChildInput
	if err != nil || json.Unmarshal(encoded, &input) != nil {
		test.Fatal("cannot load synthetic child fixture")
	}
	verifier, err := nativesessionauth.NewVerifier(input.Key)
	if err != nil {
		test.Fatal("cannot create child verifier")
	}
	store, err := Open(test.Context(), input.Path, input.Key)
	clear(input.Key)
	if err != nil {
		test.Fatal("child cannot reopen real SQLite")
	}
	defer store.Close()
	store.state.now = func() time.Time { return time.Unix(0, input.Now).UTC() }
	bearer, _ := nativesessionauth.ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{31}, 32)))
	credential, err := store.NativeCredentialFromBearer(bearer)
	if err != nil {
		test.Fatal("native bearer failed")
	}
	snapshot, err := store.NativeCodexConfigurationSnapshot(test.Context(), credential, input.Origin, input.Operation, input.PrepareID)
	if err != nil || snapshot.Preparation().CopyRequest().Nonce != [32]byte(bytes.Repeat([]byte{6}, 32)) {
		test.Fatal("new process lost authenticated original nonce")
	}
	if (snapshot.box.value.record.apply.CopyWire() != nil) != input.Bound {
		test.Fatal("restart changed bound state")
	}
	request := snapshot.Preparation().CopyRequest()
	data, err := NewNativeCodexPreparedData(request.Adapter, input.Declaration)
	if err != nil {
		test.Fatal("child declaration invalid")
	}
	validate := func(context.Context, ConfigurationAsset, ConfigurationPayload) (NativeCodexPreparedData, error) {
		return data, nil
	}
	base := nativeConfigurationFlow{fixture: nativeFixture{store: store}, credential: credential, bearer: bearer, key: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))}
	base.verifier = verifier
	flow := codexStateFlow{base: base, consumer: snapshot.Consumer(), request: request}
	proof := flow.prepareEvidence(test, request)
	replayed, err := store.PrepareNativeCodexApply(test.Context(), credential, input.Origin, request, proof, validate)
	if err != nil || replayed.Binding().CopyWire().PrepareId != input.PrepareID {
		test.Fatal("same original nonce not idempotent after process restart")
	}
	request.Nonce[0] ^= 1
	if result, err := store.PrepareNativeCodexApply(test.Context(), credential, input.Origin, request, flow.prepareEvidence(test, request), validate); !errors.Is(err, ErrConfigurationConflict) || result.Binding().CopyWire() != nil {
		test.Fatal("different original nonce accepted after process restart")
	}
}
