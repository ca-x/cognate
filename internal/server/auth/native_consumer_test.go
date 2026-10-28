package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configurationnativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/state"
)

type nativeServiceFixture struct {
	owner   *Service
	native  *NativeConsumerService
	master  []byte
	path    string
	parent  SessionToken
	ownerID string
}

func newNativeServiceFixture(test *testing.T) nativeServiceFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "auth.db")
	store, err := state.Open(test.Context(), path, master)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	owner, err := NewService(store, master)
	if err != nil {
		test.Fatal(err)
	}
	parent, ownerID, err := owner.LoginOIDC(test.Context(), "https://identity.example", "synthetic-subject", "", "")
	if err != nil {
		test.Fatal(err)
	}
	native, err := NewNativeConsumerService(owner, master, "https://cognate.example")
	if err != nil {
		test.Fatal(err)
	}
	return nativeServiceFixture{owner: owner, native: native, master: master, path: path, parent: parent, ownerID: ownerID}
}

func nativeServiceStart(fixture nativeServiceFixture) (state.NativeConsumerStart, ed25519.PrivateKey) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	return state.NativeConsumerStart{ExpectedOwner: fixture.ownerID, Origin: "https://cognate.example", Purpose: state.NativeConfigurationApply,
		OperationID: bytes.Repeat([]byte{1}, 16), PublicKey: key.Public().(ed25519.PublicKey), ClientNonce: bytes.Repeat([]byte{2}, 32)}, key
}

func pureBind(test *testing.T, fields nativesessionauth.BindFields, key ed25519.PrivateKey) []byte {
	test.Helper()
	transcript := pureBindTranscript(test, fields)
	signature, err := configclient.SignNativeProof(key, transcript)
	if err != nil {
		test.Fatal(err)
	}
	return signature.CopyBytes()
}

func pureBindTranscript(test *testing.T, fields nativesessionauth.BindFields) configclient.Transcript {
	test.Helper()
	begin := &configurationnativev1.BeginNativeConsumerRequest{
		Purpose: configurationnativev1.NativePurpose(fields.Purpose), ExpectedOwnerId: fields.OwnerID, ExpectedServerOrigin: fields.ServerOrigin,
		OperationId: fields.OperationID[:], ConsumerPublicKey: fields.ConsumerPublicKey[:], ClientNonce: fields.ClientNonce[:],
	}
	challenge := &configurationnativev1.BeginNativeConsumerResponse{
		OwnerId: fields.OwnerID, ServerOrigin: fields.ServerOrigin, ChallengeId: fields.ChallengeID,
		Challenge: fields.Challenge[:], NativeSessionBinding: fields.NativeSessionBinding[:],
		ChallengeExpiresAtUnixMs: fields.ChallengeExpiresAtUnixMS, NativeSessionExpiresAtUnixMs: fields.NativeSessionExpiresAtUnixMS,
	}
	transcript, err := configclient.BindTranscript(begin, challenge)
	if err != nil {
		test.Fatal(err)
	}
	return transcript
}

func TestNativeFacadeRealOwnerLifecycle(test *testing.T) {
	fixture := newNativeServiceFixture(test)
	start, key := nativeServiceStart(fixture)
	snapshot, err := fixture.native.Begin(test.Context(), fixture.parent.Reveal(), start)
	if err != nil {
		test.Fatal(err)
	}
	fields, err := snapshot.BindFields()
	if err != nil {
		test.Fatal(err)
	}
	issued, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key))
	if err != nil || issued.Reveal() == "" || issued.Identity().Owner() != fixture.ownerID {
		test.Fatalf("real Bind failed: %v", err)
	}
	identity, err := fixture.native.Authenticate(test.Context(), issued.Reveal(), start.OperationID)
	if err != nil || identity.Owner() != fixture.ownerID {
		test.Fatalf("issued child unusable: %v", err)
	}
	if _, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key)); !errors.Is(err, state.ErrNativeUnauthorized) {
		test.Fatal("challenge replay accepted")
	}
	if err := fixture.owner.Logout(test.Context(), fixture.parent.Reveal()); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.native.Authenticate(test.Context(), issued.Reveal(), start.OperationID); !errors.Is(err, state.ErrNativeUnauthorized) {
		test.Fatal("real Logout did not revoke child")
	}
}

func TestNativeFacadeLocalLoginAndBoundaries(test *testing.T) {
	fixture := newNativeServiceFixture(test)
	password := []byte("synthetic-local-password")
	ownerID, err := fixture.owner.Register(test.Context(), "local-user", password)
	if err != nil {
		test.Fatal(err)
	}
	parent, err := fixture.owner.Login(test.Context(), "local-user", password)
	if err != nil {
		test.Fatal(err)
	}
	start, key := nativeServiceStart(fixture)
	start.ExpectedOwner = ownerID
	snapshot, err := fixture.native.Begin(test.Context(), parent.Reveal(), start)
	if err != nil {
		test.Fatal(err)
	}
	fields, _ := snapshot.BindFields()
	issued, err := fixture.native.Bind(test.Context(), parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key))
	if err != nil || issued.Identity().Owner() != ownerID || issued.ExpiresAt().UnixMilli() != fields.NativeSessionExpiresAtUnixMS {
		test.Fatal("local owner MAC path failed")
	}
	for _, raw := range []string{fixture.parent.Reveal(), "cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{10}, 32)), issued.Reveal() + "!"} {
		if _, err := fixture.native.Authenticate(test.Context(), raw, start.OperationID); !errors.Is(err, state.ErrNativeUnauthorized) {
			test.Fatal("unissued or invalid native bearer authorized")
		}
	}
	wrongOperation := bytes.Repeat([]byte{9}, 16)
	if _, err := fixture.native.Authenticate(test.Context(), issued.Reveal(), wrongOperation); !errors.Is(err, state.ErrNativeUnauthorized) {
		test.Fatal("operation substitution authorized")
	}
	for _, origin := range []string{"", "http://localhost", "http://127.0.0.1:8080", "https://EXAMPLE.com", "https://example.com/", "https://user@example.com", "https://example.com?"} {
		if _, err := NewNativeConsumerService(fixture.owner, fixture.master, origin); !errors.Is(err, state.ErrNativeInvalid) {
			test.Fatal("facade accepted unsupported trusted origin")
		}
	}
	if _, err := NewNativeConsumerService(fixture.owner, bytes.Repeat([]byte{99}, 32), "https://cognate.example"); !errors.Is(err, state.ErrNativeInvalid) {
		test.Fatal("wrong master initialized facade")
	}
	start.Origin = "https://other.example"
	if _, err := fixture.native.Begin(test.Context(), parent.Reveal(), start); !errors.Is(err, state.ErrNativeInvalid) {
		test.Fatal("origin substituted")
	}
}

func TestNativeFacadeOwnerSessionAndSignatureSubstitution(test *testing.T) {
	fixture := newNativeServiceFixture(test)
	start, key := nativeServiceStart(fixture)
	snapshot, err := fixture.native.Begin(test.Context(), fixture.parent.Reveal(), start)
	if err != nil {
		test.Fatal(err)
	}
	fields, _ := snapshot.BindFields()
	signature := pureBind(test, fields, key)
	second, _, err := fixture.owner.LoginOIDC(test.Context(), "https://identity.example", "synthetic-subject", "", "")
	if err != nil {
		test.Fatal(err)
	}
	foreign, _, err := fixture.owner.LoginOIDC(test.Context(), "https://identity.example", "other-subject", "", "")
	if err != nil {
		test.Fatal(err)
	}
	for _, raw := range []string{second.Reveal(), foreign.Reveal(), fixture.parent.Reveal()[:86] + "!", fixture.ownerID} {
		issued, err := fixture.native.Bind(test.Context(), raw, fields.ChallengeID, signature)
		if !errors.Is(err, state.ErrNativeUnauthorized) || issued.Reveal() != "" {
			test.Fatal("owner or exact parent substitution authorized")
		}
	}
	for _, wrong := range [][]byte{nil, bytes.Repeat([]byte{1}, 64), ed25519.Sign(key, []byte("unrelated signed message"))} {
		issued, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, wrong)
		if !errors.Is(err, state.ErrNativeUnauthorized) || issued.Reveal() != "" {
			test.Fatal("invalid proof returned token")
		}
	}
	if _, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, signature); err != nil {
		test.Fatal("invalid attempts consumed legitimate challenge")
	}
}

func TestNativeFacadeUnknownNeverIssuesOrReplays(test *testing.T) {
	for _, mode := range []string{"unknown-after-real-commit", "cancel-before-bind", "logout-before-bind"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newNativeServiceFixture(test)
			start, key := nativeServiceStart(fixture)
			snapshot, err := fixture.native.Begin(test.Context(), fixture.parent.Reveal(), start)
			if err != nil {
				test.Fatal(err)
			}
			fields, _ := snapshot.BindFields()
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			original := fixture.native.box.value.bind
			calls := 0
			fixture.native.box.value.bind = func(ctx context.Context, parent state.OwnerSessionCredential, input state.NativeConsumerBind) (state.NativeConsumerIdentity, error) {
				calls++
				if mode == "cancel-before-bind" {
					cancel()
				}
				if mode == "logout-before-bind" {
					if err := fixture.owner.Logout(ctx, fixture.parent.Reveal()); err != nil {
						return state.NativeConsumerIdentity{}, err
					}
				}
				identity, err := original(ctx, parent, input)
				if err == nil && mode == "unknown-after-real-commit" {
					return identity, errors.Join(state.ErrNativeOutcomeUnknown, errors.New("synthetic-private-error-must-not-echo"))
				}
				return identity, err
			}
			issued, err := fixture.native.Bind(ctx, fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key))
			if err == nil || issued.Reveal() != "" || issued.Identity().ID() != "" || !issued.ExpiresAt().IsZero() || calls != 1 {
				test.Fatal("uncertain or failed bind returned authority or retried")
			}
			if strings.Contains(err.Error(), "must-not-echo") {
				test.Fatal("bind error echoed sensitive downstream detail")
			}
			if mode == "unknown-after-real-commit" && !errors.Is(err, state.ErrNativeOutcomeUnknown) {
				test.Fatal("commit uncertainty hidden")
			}
			database, err := sql.Open("sqlite3", fixture.path)
			if err != nil {
				test.Fatal(err)
			}
			defer database.Close()
			var count int
			if err := database.QueryRowContext(test.Context(), "SELECT count(*) FROM native_consumer_sessions").Scan(&count); err != nil {
				test.Fatal(err)
			}
			want := 0
			if mode == "unknown-after-real-commit" {
				want = 1
				fixture.native.box.value.bind = original
				if result, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key)); !errors.Is(err, state.ErrNativeUnauthorized) || result.Reveal() != "" {
					test.Fatal("lost committed result replayed")
				}
			}
			if count != want {
				test.Fatal("facade fault did not preserve actual SQLite commit outcome")
			}
		})
	}
}

func TestNativeFacadeCancelAfterKnownCommit(test *testing.T) {
	testNativeFacadePublicationAfterCommit(test, false)
}

func TestNativeFacadeDeadlineAfterKnownCommit(test *testing.T) {
	testNativeFacadePublicationAfterCommit(test, true)
}

func testNativeFacadePublicationAfterCommit(test *testing.T, deadline bool) {
	test.Helper()
	fixture := newNativeServiceFixture(test)
	start, key := nativeServiceStart(fixture)
	snapshot, err := fixture.native.Begin(test.Context(), fixture.parent.Reveal(), start)
	if err != nil {
		test.Fatal(err)
	}
	fields, err := snapshot.BindFields()
	if err != nil {
		test.Fatal(err)
	}
	var ctx context.Context
	var cancel context.CancelFunc
	wantError := context.Canceled
	if deadline {
		ctx, cancel = context.WithTimeout(test.Context(), time.Second)
		wantError = context.DeadlineExceeded
	} else {
		ctx, cancel = context.WithCancel(test.Context())
	}
	defer cancel()
	original := fixture.native.box.value.bind
	calls := 0
	knownCommit := false
	fixture.native.box.value.bind = func(ctx context.Context, parent state.OwnerSessionCredential, input state.NativeConsumerBind) (state.NativeConsumerIdentity, error) {
		calls++
		identity, err := original(ctx, parent, input)
		if err == nil && identity.ID() != "" {
			knownCommit = true
			if deadline {
				<-ctx.Done()
			} else {
				cancel()
			}
		}
		return identity, err
	}
	issued, err := fixture.native.Bind(ctx, fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key))
	database, openErr := sql.Open("sqlite3", fixture.path)
	if openErr != nil {
		test.Fatal(openErr)
	}
	defer database.Close()
	var children int
	if queryErr := database.QueryRowContext(test.Context(), "SELECT count(*) FROM native_consumer_sessions").Scan(&children); queryErr != nil || children != 1 || !knownCommit || !errors.Is(ctx.Err(), wantError) || calls != 1 {
		test.Fatal("probe did not establish one real commit followed by cancellation")
	}
	if !errors.Is(err, wantError) || errors.Is(err, state.ErrNativeOutcomeUnknown) || issued.Reveal() != "" || issued.Identity().ID() != "" || !issued.ExpiresAt().IsZero() {
		test.Fatal("canceled publication returned authority or misclassified known commit")
	}
	fixture.native.box.value.bind = original
	if replay, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key)); !errors.Is(err, state.ErrNativeUnauthorized) || replay.Reveal() != "" {
		test.Fatal("canceled publication allowed committed challenge replay")
	}
	if queryErr := database.QueryRowContext(test.Context(), "SELECT count(*) FROM native_consumer_sessions").Scan(&children); queryErr != nil || children != 1 {
		test.Fatal("canceled publication deleted or duplicated committed child")
	}
}

func TestNativeBindCommittedProofFieldParity(test *testing.T) {
	fixture := newNativeServiceFixture(test)
	start, key := nativeServiceStart(fixture)
	snapshot, err := fixture.native.Begin(test.Context(), fixture.parent.Reveal(), start)
	if err != nil {
		test.Fatal(err)
	}
	base, _ := snapshot.BindFields()
	verifier, _ := nativesessionauth.NewVerifier(fixture.master)
	parent, _ := verifier.VerifyOwner(fixture.parent.Reveal())
	cases := []nativesessionauth.BindFields{base}
	for _, change := range []func(*nativesessionauth.BindFields){
		func(fields *nativesessionauth.BindFields) {
			fields.ServerOrigin = "https://" + strings.Repeat("a.", 1019) + "aa"
		},
		func(fields *nativesessionauth.BindFields) { fields.ServerOrigin = "http://127.0.0.1:8080" },
		func(fields *nativesessionauth.BindFields) { fields.ServerOrigin = "http://[::1]:65535" },
		func(fields *nativesessionauth.BindFields) { fields.ServerOrigin = "http://localhost" },
		func(fields *nativesessionauth.BindFields) { fields.ServerOrigin = "https://127.0.0.01" },
		func(fields *nativesessionauth.BindFields) { fields.OwnerID = strings.Repeat("界", 170) + "ab" },
		func(fields *nativesessionauth.BindFields) { fields.OwnerID = "a" },
		func(fields *nativesessionauth.BindFields) { fields.OperationID[15]++ },
		func(fields *nativesessionauth.BindFields) {
			other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32))
			fields.ConsumerPublicKey = [32]byte(other.Public().(ed25519.PublicKey))
		},
		func(fields *nativesessionauth.BindFields) { fields.ClientNonce[31]++ },
		func(fields *nativesessionauth.BindFields) { fields.ChallengeID = strings.Repeat("Z", 64) },
		func(fields *nativesessionauth.BindFields) { fields.ChallengeID = "x" },
		func(fields *nativesessionauth.BindFields) { fields.Challenge[31]++ },
		func(fields *nativesessionauth.BindFields) { fields.NativeSessionBinding[31]++ },
		func(fields *nativesessionauth.BindFields) { fields.ChallengeExpiresAtUnixMS-- },
		func(fields *nativesessionauth.BindFields) {
			fields.NativeSessionExpiresAtUnixMS = fields.ChallengeExpiresAtUnixMS
		},
		func(fields *nativesessionauth.BindFields) { fields.NativeSessionExpiresAtUnixMS++ },
	} {
		fields := base
		change(&fields)
		cases = append(cases, fields)
	}
	for _, fields := range cases {
		transcript := pureBindTranscript(test, fields)
		signer := key
		if fields.ConsumerPublicKey != base.ConsumerPublicKey {
			signer = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32))
		}
		proof, err := verifier.VerifyBind(parent, fields, pureBind(test, fields, signer))
		if err != nil {
			test.Fatal("bridge rejected b5 canonical signature")
		}
		var encoded bytes.Buffer
		for _, part := range [][]byte{[]byte("cognate/native-config-bind-evidence/v1"), transcript.CopyBytes()} {
			_ = binary.Write(&encoded, binary.BigEndian, uint32(len(part)))
			_, _ = encoded.Write(part)
		}
		want := sha256.Sum256(encoded.Bytes())
		got, err := nativesessionauth.BindFieldsDigest(fields)
		if err != nil || got != want {
			test.Fatal("bridge D differs from committed proof canonical bytes")
		}
		credential, err := fixture.owner.store.OwnerCredentialFromVerified(parent)
		if err != nil {
			test.Fatal(err)
		}
		if _, err := fixture.owner.store.BindingProofFromVerified(credential, snapshot, proof); fields == base && err != nil || fields != base && !errors.Is(err, state.ErrNativeUnauthorized) {
			test.Fatal("authoritative comparison disagrees with exact field parity")
		}
	}
}

func TestNativeFacadeNewWrapperPrivacy(test *testing.T) {
	fixture := newNativeServiceFixture(test)
	start, key := nativeServiceStart(fixture)
	snapshot, err := fixture.native.Begin(test.Context(), fixture.parent.Reveal(), start)
	if err != nil {
		test.Fatal(err)
	}
	fields, _ := snapshot.BindFields()
	issued, err := fixture.native.Bind(test.Context(), fixture.parent.Reveal(), fields.ChallengeID, pureBind(test, fields, key))
	if err != nil {
		test.Fatal(err)
	}
	secrets := []string{issued.Reveal(), fixture.parent.Reveal(), base64.RawURLEncoding.EncodeToString(fixture.owner.signing), fmt.Sprintf("%x", fixture.owner.signing), fmt.Sprintf("%X", fixture.owner.signing), fmt.Sprint(fixture.owner.signing)}
	for _, value := range []any{fixture.native, *fixture.native, issued, struct{ hidden IssuedNativeConsumer }{issued}, struct{ hidden NativeConsumerService }{*fixture.native}} {
		for _, container := range []any{value, &value, struct{ hidden any }{value}, []any{value}, map[string]any{"value": value}} {
			var outputs []string
			for _, verb := range "vTtbcdoOqxXUeEfFgGspw" {
				for _, flags := range []string{"", "+", "#", "020.10"} {
					outputs = append(outputs, fmt.Sprintf("%"+flags+string(verb), container))
				}
			}
			for _, json := range []bool{false, true} {
				var output bytes.Buffer
				var handler slog.Handler = slog.NewTextHandler(&output, nil)
				if json {
					handler = slog.NewJSONHandler(&output, nil)
				}
				slog.New(handler).Info("synthetic", slog.Any("value", container))
				outputs = append(outputs, output.String())
			}
			for _, output := range outputs {
				for _, secret := range secrets {
					if strings.Contains(output, secret) {
						test.Fatal("new service or credential leaked through default representation")
					}
				}
			}
		}
	}
	for _, file := range []string{fixture.path, fixture.path + "-wal"} {
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			test.Fatal(err)
		}
		if bytes.Contains(data, []byte(issued.Reveal())) || bytes.Contains(data, []byte(fixture.parent.Reveal())) {
			test.Fatal("plaintext bearer persisted")
		}
	}
}
