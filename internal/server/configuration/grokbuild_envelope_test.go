package configuration

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/state"
)

func TestGrokBuildEnvelopeShapes(test *testing.T) {
	for _, shape := range []string{"native", "missing-key", "key", "env", "both", "boundaries"} {
		test.Run(shape, func(test *testing.T) {
			provider := grokBuildDraft().GrokBuildProvider
			var secret Secret
			if shape == "native" {
				provider = &GrokBuildProvider{Mode: "native-login"}
			}
			if shape == "key" || shape == "both" || shape == "boundaries" {
				secret = NewSecret([]byte("synthetic-envelope-key"))
			}
			defer secret.Clear()
			if shape == "env" || shape == "both" {
				provider.Model.EnvKey = new("GROK_NAME")
			}
			if shape == "boundaries" {
				provider.Model.Profile = strings.Repeat("p", 128)
				provider.Model.UpstreamModel = strings.Repeat("u", 256)
				provider.Model.Name = strings.Repeat("n", 128)
				provider.Model.Endpoint = "https://e.invalid/" + strings.Repeat("x", 2030)
				provider.Model.EnvKey = new(strings.Repeat("X", 128))
				secret.Clear()
				secret = NewSecret([]byte(strings.Repeat("s", 8192)))
				defer secret.Clear()
			}
			payload, err := encodeGrokBuildEnvelope(*provider, secret)
			if err != nil {
				test.Fatal(err)
			}
			decoded, owned, extras, err := decodeGrokBuildEnvelope(payload)
			defer owned.Clear()
			if err != nil || extras || !reflect.DeepEqual(decoded, *provider) || !bytes.Equal(secretBytes(owned), secretBytes(secret)) {
				test.Fatal("full envelope parity failed")
			}
			if decoded.Model != nil && decoded.Model == provider.Model {
				test.Fatal("decoded model aliases caller")
			}
			owned.Clear()
			if shape == "key" && len(secretBytes(secret)) == 0 {
				test.Fatal("decode/clear invalidated caller secret")
			}
		})
	}
	cases := map[string]string{
		"known-null":       strings.Replace(grokBuildStoredDraft, `"profile":"local-profile"`, `"profile":null`, 1),
		"model-null":       `{"version":1,"variant":"grokbuild-provider","provider":{"mode":"native-login","model":null},"secrets":{}}`,
		"env-null":         strings.Replace(grokBuildStoredDraft, `"contextWindow":500000`, `"contextWindow":500000,"envKey":null`, 1),
		"missing-version":  strings.Replace(grokBuildStoredDraft, `"version":1,`, "", 1),
		"missing-provider": `{"version":1,"variant":"grokbuild-provider","secrets":{}}`,
		"missing-secrets":  strings.Replace(grokBuildStoredDraft, `,"secrets":{}`, "", 1),
		"duplicate":        strings.Replace(grokBuildStoredDraft, `"version":1`, `"version":1,"version":1`, 1),
		"wrong-type":       strings.Replace(grokBuildStoredDraft, `"contextWindow":500000`, `"contextWindow":"500000"`, 1),
		"fraction":         strings.Replace(grokBuildStoredDraft, `"contextWindow":500000`, `"contextWindow":1.5`, 1),
		"too-large":        strings.Replace(grokBuildStoredDraft, `"contextWindow":500000`, `"contextWindow":9007199254740992`, 1),
		"zero":             strings.Replace(grokBuildStoredDraft, `"contextWindow":500000`, `"contextWindow":0`, 1),
		"bad-env":          strings.Replace(grokBuildStoredDraft, `"contextWindow":500000`, `"contextWindow":1,"envKey":"1KEY"`, 1),
		"empty-key":        strings.Replace(grokBuildStoredDraft, `"secrets":{}`, `"secrets":{"api_key":""}`, 1),
		"key-null":         strings.Replace(grokBuildStoredDraft, `"secrets":{}`, `"secrets":{"api_key":null}`, 1),
		"trailing":         grokBuildStoredDraft + `{}`,
		"depth":            strings.Replace(grokBuildStoredDraft, `"secrets":{}`, `"extra":`+strings.Repeat("[", 18)+`0`+strings.Repeat("]", 18)+`,"secrets":{}`, 1),
		"overflow":         strings.Repeat(" ", state.MaxConfigurationPayloadBytes) + grokBuildStoredDraft,
	}
	for name, raw := range cases {
		test.Run(name, func(test *testing.T) {
			provider, secret, _, err := decodeGrokBuildEnvelope(state.NewConfigurationPayload([]byte(raw)))
			defer secret.Clear()
			if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(provider, GrokBuildProvider{}) || len(secretBytes(secret)) != 0 {
				test.Fatal("invalid envelope accepted or returned partial secret")
			}
		})
	}
	for _, raw := range []string{strings.Replace(grokBuildStoredDraft, `"version":1`, `"version":2`, 1), strings.Replace(grokBuildStoredDraft, `"grokbuild-provider"`, `"gemini-provider"`, 1), strings.Replace(grokBuildStoredDraft, `"custom-model"`, `"future"`, 1), strings.Replace(grokBuildStoredDraft, `"responses"`, `"future"`, 1)} {
		_, secret, _, err := decodeGrokBuildEnvelope(state.NewConfigurationPayload([]byte(raw)))
		secret.Clear()
		if !errors.Is(err, ErrUnsupported) {
			test.Fatal("future stored format was upgraded or misinterpreted")
		}
	}
}

func TestGrokBuildUnknownExtrasReadOnly(test *testing.T) {
	for _, location := range []string{"envelope", "provider", "model", "secrets", "keyPresent"} {
		test.Run(location, func(test *testing.T) {
			store, _, _ := geminiStore(test)
			raw := grokBuildStoredDraft
			switch location {
			case "envelope":
				raw = strings.Replace(raw, `"version":1`, `"future":{"secret":"synthetic-future-secret"},"version":1`, 1)
			case "provider":
				raw = strings.Replace(raw, `"mode":"custom-model"`, `"future":false,"mode":"custom-model"`, 1)
			case "model":
				raw = strings.Replace(raw, `"profile":"local-profile"`, `"future":[null,false,123],"profile":"local-profile"`, 1)
			case "secrets":
				raw = strings.Replace(raw, `"secrets":{}`, `"secrets":{"future.key":"synthetic-future-secret"}`, 1)
			case "keyPresent":
				raw = strings.Replace(raw, `"profile":"local-profile"`, `"keyPresent":true,"profile":"local-profile"`, 1)
			}
			asset, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "grokbuild", Kind: "provider", Name: "Future", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(raw))})
			if err != nil {
				test.Fatal(err)
			}
			service := New(store)
			view, err := service.Edit(test.Context(), "alice", asset.ID)
			if err != nil || !view.HasPreservedExtras || view.Secrets[0].Present {
				test.Fatal("unknown extras were treated as editable or as key presence")
			}
			for _, call := range []func() error{
				func() error {
					_, err := service.Update(test.Context(), "alice", asset.ID, 1, grokBuildDraft(), nil)
					return err
				},
				func() error { _, err := service.Copy(test.Context(), "alice", asset.ID, 1, "copy"); return err },
				func() error {
					_, err := service.GrokBuildProviderDesired(test.Context(), "alice", asset.ID, 1)
					return err
				},
			} {
				if !errors.Is(call(), ErrUnsupported) {
					test.Fatal("unknown extras bypassed read-only")
				}
			}
			metadata, payload, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", asset.ID)
			if err != nil || metadata != asset || string(payload.Reveal()) != raw {
				test.Fatal("unknown fields were rewritten/lost")
			}
			encoded, err := json.Marshal(view)
			if err != nil || bytes.Contains(encoded, []byte("synthetic-future-secret")) {
				test.Fatal("unknown secret leaked in view")
			}
			if _, err := service.GetAssetOrder(test.Context(), "alice", "grokbuild", "provider"); err != nil {
				test.Fatal("future extras broke metadata ordering")
			}
			if err := service.Delete(test.Context(), "alice", asset.ID, 1); err != nil {
				test.Fatal("read-only extras broke explicit metadata deletion")
			}
		})
	}
}

func TestGrokBuildPrivateOutput(test *testing.T) {
	canary := []byte("synthetic-grok-private-83de12")
	secret := NewSecret(canary)
	defer secret.Clear()
	clone := cloneSecret(secret)
	defer clone.Clear()
	values := []any{secret, &secret, SecretMutation{Slot: GrokBuildAPIKeySlot, Action: Replace, Replacement: secret}, struct {
		Label   string
		private Secret
	}{"synthetic", secret}, []Secret{secret}, map[string]Secret{"key": secret}, struct {
		Label string
		value any
	}{"synthetic", secret}}
	variants := []string{string(canary), hex.EncodeToString(canary), strings.ToUpper(hex.EncodeToString(canary)), fmt.Sprint(canary), fmt.Sprintf("%x", canary)}
	for _, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x", "%X"} {
			encoded := fmt.Sprintf(format, value)
			for _, variant := range variants {
				if strings.Contains(encoded, variant) {
					test.Fatal("private container formatting leaked canary")
				}
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			test.Fatal(err)
		}
		var logs bytes.Buffer
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("synthetic", "value", value)
		for _, variant := range variants {
			if strings.Contains(string(encoded), variant) || strings.Contains(logs.String(), variant) {
				test.Fatal("JSON/slog leaked canary")
			}
		}
	}
	secret.Clear()
	if len(secretBytes(secret)) != 0 || !bytes.Equal(secretBytes(clone), canary) {
		test.Fatal("owned clear/copy contract failed")
	}
}

func TestGrokBuildAADAndAtRest(test *testing.T) {
	store, path, key := geminiStore(test)
	service := New(store)
	input := grokBuildDraft()
	input.GrokBuildProvider.Model.Name = "synthetic-private-provider-91ab"
	created, err := service.Create(test.Context(), "alice", input, grokBuildReplacement(test, "synthetic-at-rest-key-28d1"))
	if err != nil {
		test.Fatal(err)
	}
	other, err := service.Create(test.Context(), "alice", input, nil)
	if err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	var encrypted []byte
	if err := database.QueryRowContext(test.Context(), "SELECT ciphertext FROM configuration_assets WHERE public_id=?", created.Metadata.ID).Scan(&encrypted); err != nil {
		test.Fatal(err)
	}
	for _, marker := range []string{"synthetic-at-rest-key-28d1", "synthetic-private-provider-91ab"} {
		if bytes.Contains(encrypted, []byte(marker)) {
			test.Fatal("plaintext in ciphertext")
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			data, err := os.ReadFile(path + suffix)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				test.Fatal(err)
			}
			if bytes.Contains(data, []byte(marker)) {
				test.Fatal("plaintext persisted in SQLite")
			}
		}
	}
	if _, err := database.ExecContext(test.Context(), "UPDATE configuration_assets SET ciphertext=? WHERE public_id=?", encrypted, other.Metadata.ID); err != nil {
		test.Fatal(err)
	}
	if view, err := service.Edit(test.Context(), "alice", other.Metadata.ID); !errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(view, EditView{}) {
		test.Fatal("cross-asset ciphertext accepted")
	}
	if _, err := database.ExecContext(test.Context(), "UPDATE configuration_assets SET name=? WHERE public_id=?", "tampered", created.Metadata.ID); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Edit(test.Context(), "alice", created.Metadata.ID); !errors.Is(err, ErrUnavailable) {
		test.Fatal("AAD name tamper accepted")
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	wrong := bytes.Clone(key)
	wrong[0] ^= 1
	if reopened, err := state.Open(test.Context(), path, wrong); err == nil {
		reopened.Close()
		test.Fatal("wrong master key reopened database")
	}
}
