package configuration

import (
	"bytes"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
)

const geminiStoredNative = `{"version":1,"variant":"gemini-provider","provider":{"authMode":"native-login"},"secrets":{}}`

func TestGeminiEnvelopeStrictStoredShape(test *testing.T) {
	for _, scenario := range []struct {
		name string
		raw  string
		want error
	}{
		{"empty", "", ErrInvalid},
		{"root-null", `null`, ErrInvalid},
		{"trailing", geminiStoredNative + `{}`, ErrInvalid},
		{"missing-version", `{"variant":"gemini-provider","provider":{"authMode":"native-login"},"secrets":{}}`, ErrInvalid},
		{"version-null", strings.Replace(geminiStoredNative, `"version":1`, `"version":null`, 1), ErrInvalid},
		{"duplicate-version", strings.Replace(geminiStoredNative, `"version":1`, `"version":1,"version":1`, 1), ErrInvalid},
		{"version-fraction", strings.Replace(geminiStoredNative, `"version":1`, `"version":1.1`, 1), ErrInvalid},
		{"future-version", strings.Replace(geminiStoredNative, `"version":1`, `"version":2`, 1), ErrUnsupported},
		{"future-variant", strings.Replace(geminiStoredNative, `gemini-provider`, `future-provider`, 1), ErrUnsupported},
		{"missing-secrets", `{"version":1,"variant":"gemini-provider","provider":{"authMode":"native-login"}}`, ErrInvalid},
		{"secrets-null", strings.Replace(geminiStoredNative, `"secrets":{}`, `"secrets":null`, 1), ErrInvalid},
		{"key-null", strings.Replace(geminiStoredNative, `"secrets":{}`, `"secrets":{"api_key":null}`, 1), ErrInvalid},
		{"missing-auth", strings.Replace(geminiStoredNative, `"authMode":"native-login"`, ``, 1), ErrInvalid},
		{"unknown-auth", strings.Replace(geminiStoredNative, `native-login`, `vertex`, 1), ErrUnsupported},
		{"auth-null", strings.Replace(geminiStoredNative, `"native-login"`, `null`, 1), ErrInvalid},
		{"auth-object", strings.Replace(geminiStoredNative, `"native-login"`, `{}`, 1), ErrInvalid},
		{"optional-null", strings.Replace(geminiStoredNative, `"authMode":"native-login"`, `"authMode":"native-login","envModel":null`, 1), ErrInvalid},
		{"duplicate-model", strings.Replace(geminiStoredNative, `"authMode":"native-login"`, `"authMode":"native-login","envModel":"a","envModel":"b"`, 1), ErrInvalid},
		{"empty-model", strings.Replace(geminiStoredNative, `"authMode":"native-login"`, `"authMode":"native-login","envModel":""`, 1), ErrInvalid},
		{"native-key-residue", strings.Replace(geminiStoredNative, `"secrets":{}`, `"secrets":{"api_key":"synthetic-residue"}`, 1), ErrInvalid},
		{"native-endpoint", strings.Replace(geminiStoredNative, `"authMode":"native-login"`, `"authMode":"native-login","endpoint":"https://example.invalid"`, 1), ErrInvalid},
		{"invalid-utf8", strings.Replace(geminiStoredNative, `native-login`, "bad\xff", 1), ErrInvalid},
		{"deep-unknown", strings.Replace(geminiStoredNative, `"secrets":{}`, `"secrets":{},"future":`+strings.Repeat(`[`, 17)+`0`+strings.Repeat(`]`, 17), 1), ErrInvalid},
		{"duplicate-unknown", strings.Replace(geminiStoredNative, `"secrets":{}`, `"secrets":{},"future":{"x":1,"x":2}`, 1), ErrInvalid},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			provider, secret, extras, err := decodeGeminiEnvelope(state.NewConfigurationPayload([]byte(scenario.raw)))
			defer secret.Clear()
			if !errors.Is(err, scenario.want) || !reflect.DeepEqual(provider, GeminiProvider{}) || len(secretBytes(secret)) != 0 || extras {
				test.Fatal("malformed stored envelope did not fail with a zero projection")
			}
		})
	}
	for _, field := range []string{`"future":null`, `"future":{"private-future-key":"synthetic-private-extra"}`, `"future":[1,true,null,{"x":"y"}]`} {
		raw := strings.TrimSuffix(geminiStoredNative, "}") + "," + field + "}"
		provider, secret, extras, err := decodeGeminiEnvelope(state.NewConfigurationPayload([]byte(raw)))
		secret.Clear()
		if err != nil || !extras || provider.AuthMode != agentconfig.GeminiNativeLogin {
			test.Fatal("legal unknown stored members were not preserved read-only")
		}
	}
}

func TestGeminiEnvelopeBytesBoundsAndProjection(test *testing.T) {
	provider := GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, KeyTransport: new(agentconfig.GeminiNativeDefault), EnvModel: new("env-A"), SettingsModel: new("settings-B")}
	secret := NewSecret([]byte("synthetic #=$\" key"))
	defer secret.Clear()
	payload, err := encodeGeminiEnvelope(provider, secret)
	want := `{"version":1,"variant":"gemini-provider","provider":{"authMode":"api-key","keyTransport":"native-default","envModel":"env-A","settingsModel":"settings-B"},"secrets":{"api_key":"synthetic #=$\" key"}}`
	if err != nil || !bytes.Equal(payload.Reveal(), []byte(want)) {
		test.Fatal("Gemini v1 envelope bytes differ from the fixed literal")
	}
	decoded, owned, extras, err := decodeGeminiEnvelope(payload)
	defer owned.Clear()
	if err != nil || extras || !reflect.DeepEqual(provider, decoded) || !bytes.Equal(secretBytes(secret), secretBytes(owned)) {
		test.Fatal("stored secret or independent model round-trip failed")
	}
	owned.Clear()
	if len(secretBytes(secret)) == 0 {
		test.Fatal("decoded credential aliases caller-owned input")
	}
	prefix := strings.TrimSuffix(geminiStoredNative, "}") + `,"future":"`
	raw := prefix + strings.Repeat("x", state.MaxConfigurationPayloadBytes-len(prefix)-2) + `"}`
	if len(raw) != state.MaxConfigurationPayloadBytes {
		test.Fatal("envelope limit fixture is not exact")
	}
	_, atLimit, extras, err := decodeGeminiEnvelope(state.NewConfigurationPayload([]byte(raw)))
	atLimit.Clear()
	if err != nil || !extras {
		test.Fatal("legal maximum stored envelope failed")
	}
	if _, secret, _, err := decodeGeminiEnvelope(state.NewConfigurationPayload([]byte(raw + " "))); !errors.Is(err, ErrInvalid) {
		secret.Clear()
		test.Fatal("envelope maximum plus one was accepted")
	}
}

func TestGeminiStoredExtrasAreReadOnly(test *testing.T) {
	for _, location := range []string{"top", "provider", "secrets"} {
		test.Run(location, func(test *testing.T) {
			store, _, _ := geminiStore(test)
			raw := geminiStoredNative
			member := `"private-future-key":{"value":"synthetic-private-extra"}`
			switch location {
			case "top":
				raw = strings.TrimSuffix(raw, "}") + "," + member + "}"
			case "provider":
				raw = strings.Replace(raw, `"authMode":"native-login"`, `"authMode":"native-login",`+member, 1)
			case "secrets":
				raw = strings.Replace(raw, `"secrets":{}`, `"secrets":{`+member+`}`, 1)
			}
			asset, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "gemini", Kind: state.ConfigurationKindProvider, Name: "Future", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(raw))})
			if err != nil {
				test.Fatal("stored extras fixture failed")
			}
			service := New(store)
			view, err := service.Edit(test.Context(), "alice", asset.ID)
			encoded, marshalErr := json.Marshal(view)
			if err != nil || marshalErr != nil || !view.HasPreservedExtras || view.GeminiProvider == nil || bytes.Contains(encoded, []byte("private-future-key")) || bytes.Contains(encoded, []byte("synthetic-private-extra")) {
				test.Fatal("unknown member names or values escaped the read-only projection")
			}
			if updated, err := service.Update(test.Context(), "alice", asset.ID, 1, geminiDraft(agentconfig.GeminiNativeLogin), nil); !errors.Is(err, ErrUnsupported) || updated.Metadata.ID != "" {
				test.Fatal("typed update overwrote preserved extras")
			}
			if copied, err := service.Copy(test.Context(), "alice", asset.ID, 1, "Copy"); !errors.Is(err, ErrUnsupported) || copied.Metadata.ID != "" {
				test.Fatal("typed copy lost preserved extras")
			}
			if desired, err := service.GeminiProviderDesired(test.Context(), "alice", asset.ID, 1); !errors.Is(err, ErrUnsupported) || !reflect.DeepEqual(desired, agentconfig.GeminiProviderDesired{}) {
				test.Fatal("preserved extras generated desired")
			}
			metadata, payload, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", asset.ID)
			if err != nil || metadata.Revision != 1 || !bytes.Equal(payload.Reveal(), []byte(raw)) {
				test.Fatal("denied typed operations changed the stored envelope")
			}
			if err := service.Delete(test.Context(), "alice", asset.ID, 1); err != nil {
				test.Fatal("owner/CAS delete was coupled to typed support")
			}
		})
	}
}

func TestGeminiEncryptedAADBinding(test *testing.T) {
	store, path, _ := geminiStore(test)
	service := New(store)
	first, err := service.Create(test.Context(), "alice", geminiDraft(agentconfig.GeminiAPIKeyAuth), geminiReplacement(test, "synthetic-aad-gemini"))
	if err != nil {
		test.Fatal("AAD source fixture failed")
	}
	second, err := service.Create(test.Context(), "alice", geminiDraft(agentconfig.GeminiAPIKeyAuth), nil)
	if err != nil {
		test.Fatal("AAD target fixture failed")
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		test.Fatal("synthetic SQLite oracle failed")
	}
	defer database.Close()
	var ciphertext []byte
	if database.QueryRowContext(test.Context(), "SELECT ciphertext FROM configuration_assets WHERE public_id = ?", first.Metadata.ID).Scan(&ciphertext) != nil {
		test.Fatal("encrypted source oracle failed")
	}
	if _, err := database.ExecContext(test.Context(), "UPDATE configuration_assets SET ciphertext = ? WHERE public_id = ?", ciphertext, second.Metadata.ID); err != nil {
		test.Fatal("synthetic AAD mutation failed")
	}
	if _, err := service.Edit(test.Context(), "alice", second.Metadata.ID); !errors.Is(err, ErrUnavailable) {
		test.Fatal("foreign ciphertext passed AAD identity binding")
	}
	if _, err := service.Update(test.Context(), "alice", second.Metadata.ID, 1, geminiDraft(agentconfig.GeminiAPIKeyAuth), nil); !errors.Is(err, ErrUnavailable) {
		test.Fatal("typed update overwrote a corrupted payload")
	}
	if _, err := service.Copy(test.Context(), "alice", second.Metadata.ID, 1, "copy"); !errors.Is(err, ErrUnavailable) {
		test.Fatal("typed copy accepted a corrupted payload")
	}
	if service.Delete(test.Context(), "alice", second.Metadata.ID, 1) != nil {
		test.Fatal("metadata-only delete attempted decryption")
	}
}
