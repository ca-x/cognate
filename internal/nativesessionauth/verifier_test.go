package nativesessionauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func ownerToken(test *testing.T, master []byte) string {
	test.Helper()
	key, err := hkdf.Key(sha256.New, master, nil, "skillhub/session-signing/v1", 32)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(key)
	nonce := bytes.Repeat([]byte{7}, 32)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(nonce)
	return base64.RawURLEncoding.EncodeToString(nonce) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifierRejectsInvalidMACAndIssuer(test *testing.T) {
	master := bytes.Repeat([]byte{5}, 32)
	verifier, _ := NewVerifier(master)
	raw := ownerToken(test, master)
	parts := strings.Split(raw, ".")
	mac, _ := base64.RawURLEncoding.DecodeString(parts[1])
	mac[0]++
	for _, candidate := range []string{"", "alice", strings.Repeat("a", 64), raw + "=", raw[:86], parts[0] + "." + base64.RawURLEncoding.EncodeToString(mac), ownerToken(test, bytes.Repeat([]byte{6}, 32))} {
		if _, err := verifier.VerifyOwner(candidate); !errors.Is(err, ErrInvalidEvidence) {
			test.Fatal("invalid bearer passed MAC gate")
		}
	}
	noncanonical := raw[:42] + "d" + raw[43:]
	if _, err := verifier.VerifyOwner(noncanonical); !errors.Is(err, ErrInvalidEvidence) {
		test.Fatal("noncanonical base64 passed MAC gate")
	}
	other, _ := NewVerifier(bytes.Repeat([]byte{6}, 32))
	foreign, _ := other.VerifyOwner(ownerToken(test, bytes.Repeat([]byte{6}, 32)))
	fields, key := bindFixture()
	signature := ed25519.Sign(key, independentBind(fields, "bind"))
	if _, err := verifier.VerifyBind(foreign, fields, signature); !errors.Is(err, ErrInvalidEvidence) {
		test.Fatal("foreign issuer parent accepted")
	}
	if _, err := verifier.VerifyBind(VerifiedOwnerSession{}, fields, signature); !errors.Is(err, ErrInvalidEvidence) {
		test.Fatal("zero parent accepted")
	}
	for _, empty := range []*Verifier{nil, {}, new(Verifier)} {
		if _, err := empty.VerifyOwner(raw); !errors.Is(err, ErrInvalidEvidence) {
			test.Fatal("uninitialized verifier accepted")
		}
	}
	for _, size := range []int{0, 31, 33} {
		if _, err := NewVerifier(make([]byte, size)); !errors.Is(err, ErrInvalidEvidence) {
			test.Fatal("invalid master size accepted")
		}
	}
}

func TestNativeBearerFrozenDigest(test *testing.T) {
	var nonce [32]byte
	for index := range nonce {
		nonce[index] = byte(index)
	}
	raw := "cn1." + base64.RawURLEncoding.EncodeToString(nonce[:])
	parsed, err := ParseNativeBearer(raw)
	digest, present := parsed.TokenDigest()
	if err != nil || !present || fmt.Sprintf("%x", digest) != "e25522493793f1df04b40d38242d006747b5583e5082080e41ccd040847d6ca0" {
		test.Fatal("native digest differs from independent frozen vector")
	}
	for _, candidate := range []string{"", raw + "=", raw[:46], "cn2." + raw[4:], raw[4:], "cn1." + strings.Repeat("!", 43), raw[:46] + "9"} {
		if _, err := ParseNativeBearer(candidate); !errors.Is(err, ErrInvalidEvidence) {
			test.Fatal("invalid native bearer parsed")
		}
	}
	if _, present := (ParsedNativeBearer{}).TokenDigest(); present {
		test.Fatal("zero bearer has evidence")
	}
}

func TestBridgeBindValidationBounds(test *testing.T) {
	verifier, _ := NewVerifier(bytes.Repeat([]byte{5}, 32))
	parent, _ := verifier.VerifyOwner(ownerToken(test, bytes.Repeat([]byte{5}, 32)))
	fields, key := bindFixture()
	for _, change := range []func(*BindFields){
		func(fields *BindFields) { fields.Purpose = 0 },
		func(fields *BindFields) { fields.Purpose = 2 },
		func(fields *BindFields) { fields.OwnerID = "" },
		func(fields *BindFields) { fields.OwnerID = strings.Repeat("a", 513) },
		func(fields *BindFields) { fields.OwnerID = "a\x00b" },
		func(fields *BindFields) { fields.OwnerID = "\xff" },
		func(fields *BindFields) { fields.ChallengeID = "" },
		func(fields *BindFields) { fields.ChallengeID = strings.Repeat("a", 65) },
		func(fields *BindFields) { fields.ChallengeID = "../bad" },
		func(fields *BindFields) { fields.ChallengeExpiresAtUnixMS = 0 },
		func(fields *BindFields) { fields.ChallengeExpiresAtUnixMS = -1 },
		func(fields *BindFields) { fields.NativeSessionExpiresAtUnixMS = fields.ChallengeExpiresAtUnixMS - 1 },
	} {
		invalid := fields
		change(&invalid)
		if _, err := verifier.VerifyBind(parent, invalid, ed25519.Sign(key, independentBind(invalid, "bind"))); !errors.Is(err, ErrInvalidEvidence) {
			test.Fatal("correct signature bypassed bounded field policy")
		}
		if _, err := BindFieldsDigest(invalid); !errors.Is(err, ErrInvalidEvidence) {
			test.Fatal("digest accepted invalid fields")
		}
	}
	for _, origin := range []string{"", "https://example.com/", "https://EXAMPLE.com", "http://example.com", "https://user@example.com", "https://example.com?", "https://example.com#", "https://example.com:01", "https://example.com:0", "https://example.com:65536", "https://[fe80::1%25zone]", "https://bad_host", "https://example.com\\evil", "https://example.com\n", strings.Repeat("x", 2049)} {
		invalid := fields
		invalid.ServerOrigin = origin
		if _, err := verifier.VerifyBind(parent, invalid, ed25519.Sign(key, independentBind(invalid, "bind"))); !errors.Is(err, ErrInvalidEvidence) {
			test.Fatal("noncanonical origin accepted")
		}
	}
	for _, size := range []int{0, 63, 65, 4096} {
		if _, err := verifier.VerifyBind(parent, fields, make([]byte, size)); !errors.Is(err, ErrInvalidEvidence) {
			test.Fatal("invalid signature size accepted")
		}
	}
}

func TestBridgeConcurrentImmutableEvidence(test *testing.T) {
	master := bytes.Repeat([]byte{5}, 32)
	verifier, _ := NewVerifier(master)
	raw := ownerToken(test, master)
	fields, key := bindFixture()
	signature := ed25519.Sign(key, independentBind(fields, "bind"))
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 16 {
				parent, err := verifier.VerifyOwner(raw)
				if err != nil {
					test.Error("concurrent MAC verification failed")
					return
				}
				if _, err := verifier.VerifyBind(parent, fields, signature); err != nil {
					test.Error("concurrent Bind verification failed")
				}
			}
		})
	}
	workers.Wait()
}

func TestBridgeAllVerbAndSlogPrivacy(test *testing.T) {
	master := bytes.Repeat([]byte{5}, 32)
	verifier, _ := NewVerifier(master)
	parent, _ := verifier.VerifyOwner(ownerToken(test, master))
	fields, key := bindFixture()
	proof, _ := verifier.VerifyBind(parent, fields, ed25519.Sign(key, independentBind(fields, "bind")))
	parsed, _ := ParseNativeBearer("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	values := []any{verifier, *verifier, parent, proof, parsed, struct{ hidden Verifier }{*verifier}, struct{ hidden VerifiedOwnerSession }{parent}, struct{ hidden VerifiedBindProof }{proof}, struct{ hidden ParsedNativeBearer }{parsed}}
	secrets := [][]byte{verifier.payload.value.signing[:], parent.payload.value.digest[:], proof.payload.value.digest[:], parsed.payload.value[:]}
	for _, value := range values {
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
					for _, encoded := range []string{string(secret), fmt.Sprintf("%x", secret), fmt.Sprintf("%X", secret), fmt.Sprint(secret), base64.RawURLEncoding.EncodeToString(secret)} {
						if strings.Contains(output, encoded) {
							test.Fatal("default representation leaked native evidence")
						}
					}
				}
			}
		}
	}
}

func bindFixture() (BindFields, ed25519.PrivateKey) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	return BindFields{
		ServerOrigin: "https://cognate.example", OwnerID: "alice", Purpose: 1,
		OperationID: [16]byte{1}, ConsumerPublicKey: [32]byte(key.Public().(ed25519.PublicKey)),
		ClientNonce: [32]byte{2}, ChallengeID: "challenge_1", Challenge: [32]byte{3},
		NativeSessionBinding: [32]byte{4}, ChallengeExpiresAtUnixMS: 1900000060000,
		NativeSessionExpiresAtUnixMS: 1900000300000,
	}, key
}

func independentBind(fields BindFields, purpose string) []byte {
	var encoded bytes.Buffer
	writeBlob := func(value []byte) {
		_ = binary.Write(&encoded, binary.BigEndian, uint32(len(value)))
		_, _ = encoded.Write(value)
	}
	writeBlob([]byte("cognate/native-config-proof/v1"))
	writeBlob([]byte(purpose))
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
	return encoded.Bytes()
}

func TestVerifierOwnerAndCanonicalBinding(test *testing.T) {
	master := bytes.Repeat([]byte{5}, 32)
	verifier, err := NewVerifier(master)
	if err != nil {
		test.Fatal(err)
	}
	parent, err := verifier.VerifyOwner(ownerToken(test, master))
	if err != nil {
		test.Fatal(err)
	}
	fingerprint := sha256.Sum256(master)
	keyID := base64.RawURLEncoding.EncodeToString(fingerprint[:])
	digest, present := parent.TokenDigest()
	if !present || digest != sha256.Sum256([]byte(ownerToken(test, master))) || !parent.MatchesKeyID(keyID) {
		test.Fatal("MAC evidence lost exact digest or issuer")
	}
	fields, key := bindFixture()
	signature := ed25519.Sign(key, independentBind(fields, "bind"))
	proof, err := verifier.VerifyBind(parent, fields, signature)
	if err != nil {
		test.Fatal(err)
	}
	bindDigest, err := BindFieldsDigest(fields)
	if err != nil || !proof.MatchesBinding(keyID, digest, bindDigest) {
		test.Fatal("canonical signed binding rejected")
	}
	for _, message := range [][]byte{[]byte("unrelated but validly signed text"), independentBind(fields, "prepare"), independentBind(fields, "consume")} {
		if _, err := verifier.VerifyBind(parent, fields, ed25519.Sign(key, message)); !errors.Is(err, ErrInvalidEvidence) {
			test.Fatal("arbitrary signed transcript rebound to authoritative fields")
		}
	}
	other := fields
	other.Challenge[0]++
	if _, err := verifier.VerifyBind(parent, fields, ed25519.Sign(key, independentBind(other, "bind"))); !errors.Is(err, ErrInvalidEvidence) {
		test.Fatal("signature over other fields rebound to expected fields")
	}
	wrongKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32))
	if _, err := verifier.VerifyBind(parent, fields, ed25519.Sign(wrongKey, independentBind(fields, "bind"))); !errors.Is(err, ErrInvalidEvidence) {
		test.Fatal("signature from another consumer key accepted")
	}
}
