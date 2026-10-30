package codexconfig

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNative0166EffectiveTuple(test *testing.T) {
	for _, scenario := range []struct {
		manifest func() (string, [32]byte, [32]byte, [32]byte)
		id       string
		wanted   [3]string
	}{
		{Native0166Manifest, Native0166ProfileID, [3]string{"48e7d6b81c0fd2e223297af2109e2def5d02319bc9811fa4ceee6c6c06c1d1be", "e8907ff47efec51937f7157a5e1d5f90d1b1c3d9b9cb07e3c5b43fbf7cf2c1ee", "3dcb5a25097379f27a103690d56b0e182f557ef26da2753742f6147384dbe515"}},
		{Native0166EffectiveManifest, Native0166EffectiveProfileID, [3]string{"990ba16b7312c7cd41dc52f1c7a2dc4f2f916c4dbe52c25a2ef6dde90da586bb", "c044cdc06b1e4817e548ac71aa40ca63115578ac82d9ef76d4d6677ee5fb3161", "3dcb5a25097379f27a103690d56b0e182f557ef26da2753742f6147384dbe515"}},
	} {
		identifier, manifest, profile, template := scenario.manifest()
		if identifier != scenario.id || hex.EncodeToString(manifest[:]) != scenario.wanted[0] || hex.EncodeToString(profile[:]) != scenario.wanted[1] || hex.EncodeToString(template[:]) != scenario.wanted[2] {
			test.Fatal("profile tuple changed from approved deterministic identity proposal")
		}
	}
}

func TestNative0166LayerWitnessClosure(test *testing.T) {
	const original = `{"layers":[{"name":{"type":"user","file":"/synthetic/.codex/config.toml"},"config":{"model":"synthetic"},"version":"user-version"},{"name":{"type":"system","file":"/etc/codex/config.toml"},"config":{},"version":"system-version"}]}`
	config := native0166Image(test, "model=\"synthetic\"\n")
	witness, err := Native0166LayerWitness(test.Context(), []byte(original), config, "/synthetic/.codex/config.toml")
	if err != nil || witness == [32]byte{} {
		test.Fatal("exact user and empty system layers refused")
	}
	for _, scenario := range []struct {
		name string
		from string
		to   string
	}{
		{"project", `"type":"system"`, `"type":"project"`},
		{"session", `"type":"system"`, `"type":"sessionFlags"`},
		{"unknown", `"type":"system"`, `"type":"PRIVATE_CANARY"`},
		{"duplicate-kind", `"type":"system"`, `"type":"user"`},
		{"wrong-user-path", `"file":"/synthetic/.codex/config.toml"`, `"file":"/foreign/config.toml"`},
		{"selected-profile", `"type":"user"`, `"type":"user","profile":"foreign"`},
		{"disabled-layer", `"version":"user-version"`, `"version":"user-version","disabledReason":"disabled"`},
		{"semantic-mismatch", `"model":"synthetic"`, `"model":"different"`},
		{"nonempty-system", `"config":{}`, `"config":{"model_context_window":4096}`},
		{"unknown-layer-member", `"version":"system-version"`, `"version":"system-version","other":true`},
		{"duplicate-member", `"version":"system-version"`, `"version":"system-version","version":"duplicate"`},
		{"missing-system", `,{"name":{"type":"system","file":"/etc/codex/config.toml"},"config":{},"version":"system-version"}`, ``},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			raw := strings.Replace(original, scenario.from, scenario.to, 1)
			if raw == original {
				test.Fatal("closure negative fixture did not mutate")
			}
			actual, err := Native0166LayerWitness(test.Context(), []byte(raw), config, "/synthetic/.codex/config.toml")
			if !errors.Is(err, ErrUnresolvedLocalAuth) || actual != [32]byte{} || strings.Contains(err.Error(), "PRIVATE_CANARY") {
				test.Fatal("unknown native source closure trusted or leaked")
			}
		})
	}
	changed := strings.ReplaceAll(original, "synthetic", "changed")
	changed = strings.Replace(changed, `"version":"user-version"`, `"version":"after-version"`, 1)
	after := native0166Image(test, "model=\"changed\"\n")
	actual, err := Native0166LayerWitness(test.Context(), []byte(changed), after, "/changed/.codex/config.toml")
	if err != nil || actual != witness {
		test.Fatal("managed role change contaminated non-role witness")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := Native0166LayerWitness(ctx, []byte(original), config, "/synthetic/.codex/config.toml"); !errors.Is(err, context.Canceled) {
		test.Fatal("canceled layer validation continued")
	}
}

func TestNative0166LayerResolvesOnlyFixedRelativeCatalog(test *testing.T) {
	const response = `{"layers":[{"name":{"type":"user","file":"/synthetic/.codex/config.toml"},"config":{"model_catalog_json":"/synthetic/.codex/cognate-model-catalog.json"},"version":"user-version"},{"name":{"type":"system","file":"/etc/codex/config.toml"},"config":{},"version":"system-version"}]}`
	config := native0166Image(test, "model_catalog_json=\"cognate-model-catalog.json\"\n")
	if _, err := Native0166LayerWitness(test.Context(), []byte(response), config, "/synthetic/.codex/config.toml"); err != nil {
		test.Fatal("native fixed relative catalog resolution refused")
	}
	for _, wrong := range []string{"cognate-model-catalog.json", "/foreign/cognate-model-catalog.json", "/synthetic/.codex/other.json"} {
		raw := strings.Replace(response, "/synthetic/.codex/cognate-model-catalog.json", wrong, 1)
		if _, err := Native0166LayerWitness(test.Context(), []byte(raw), config, "/synthetic/.codex/config.toml"); !errors.Is(err, ErrUnresolvedLocalAuth) {
			test.Fatal("unresolved or foreign native catalog trusted")
		}
	}
}

func native0166Image(test *testing.T, raw string) LocalImage {
	test.Helper()
	presence := Present
	if raw == "" {
		presence = Missing
	}
	image, err := NewImage(presence, []byte(raw))
	if err != nil {
		test.Fatal("synthetic image setup failed")
	}
	test.Cleanup(image.Destroy)
	return image
}

func native0166OAuth(test *testing.T, now time.Time, access string) string {
	test.Helper()
	jwt := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"synthetic@example.invalid"}`)) + ".synthetic"
	raw, err := json.Marshal(map[string]any{
		"auth_mode": "chatgpt", "OPENAI_API_KEY": nil,
		"tokens":       map[string]any{"id_token": jwt, "access_token": access, "refresh_token": "SYNTHETIC_REFRESH", "account_id": "synthetic-account"},
		"last_refresh": now.Format(time.RFC3339Nano),
	})
	if err != nil {
		test.Fatal("synthetic auth setup failed")
	}
	return string(raw)
}

func TestNative0166FileGrammarAndFreshness(test *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for _, scenario := range []struct {
		name  string
		raw   string
		login LoginState
		valid bool
	}{
		{"missing", "", NoCredentialLogin, true},
		{"api", `{"OPENAI_API_KEY":"SYNTHETIC_API"}`, CredentialLogin, true},
		{"fresh-oauth", native0166OAuth(test, now.Add(-time.Hour), "SYNTHETIC_ACCESS"), CredentialLogin, true},
		{"stale-oauth", native0166OAuth(test, now.Add(-8*24*time.Hour), "SYNTHETIC_ACCESS"), LoginUnknown, false},
		{"future-oauth", native0166OAuth(test, now.Add(time.Minute), "SYNTHETIC_ACCESS"), LoginUnknown, false},
		{"unknown-native-success", `{"unrecognized_synthetic_auth":true}`, LoginUnknown, false},
		{"empty-object", `{}`, LoginUnknown, false},
		{"null-key", `{"OPENAI_API_KEY":null}`, LoginUnknown, false},
		{"mixed", `{"OPENAI_API_KEY":"SYNTHETIC_API","tokens":{}}`, LoginUnknown, false},
		{"duplicate", `{"OPENAI_API_KEY":"SYNTHETIC_API","OPENAI_API_KEY":"SECOND"}`, LoginUnknown, false},
		{"foreign", `{"OPENAI_API_KEY":"SYNTHETIC_API","other":"PRIVATE_CANARY"}`, LoginUnknown, false},
		{"wrong-mode", `{"auth_mode":"chatgpt","OPENAI_API_KEY":"SYNTHETIC_API"}`, LoginUnknown, false},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			auth := native0166Image(test, scenario.raw)
			before := auth.CopyBytes()
			facts, deadline, err := InspectNative0166(test.Context(), native0166Image(test, ""), auth, now)
			defer facts.Destroy()
			if !bytes.Equal(before, auth.CopyBytes()) {
				test.Fatal("File auth bytes changed")
			}
			if scenario.valid {
				data := facts.get[authData]()
				if err != nil || data == nil || data.store != File || data.fileLogin != scenario.login || !deadline.After(now) || deadline.After(now.Add(5*time.Minute)) {
					test.Fatal("recognized fresh File auth refused or not bounded")
				}
			} else if !errors.Is(err, ErrUnresolvedLocalAuth) || facts.get[authData]() != nil {
				test.Fatal("unknown or refresh-needed File auth trusted")
			}
			if err != nil && bytes.Contains([]byte(err.Error()), []byte("PRIVATE_CANARY")) {
				test.Fatal("private auth escaped through error")
			}
		})
	}
}

func TestNative0166JWTRefreshWindow(test *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for _, remaining := range []time.Duration{4 * time.Minute, 5 * time.Minute, 6 * time.Minute, time.Hour} {
		payload, _ := json.Marshal(map[string]int64{"exp": now.Add(remaining).Unix()})
		jwt := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".synthetic"
		facts, deadline, err := InspectNative0166(test.Context(), native0166Image(test, ""), native0166Image(test, native0166OAuth(test, now, jwt)), now)
		facts.Destroy()
		if remaining <= 5*time.Minute {
			if !errors.Is(err, ErrUnresolvedLocalAuth) {
				test.Fatal("native proactive refresh boundary ignored")
			}
		} else if err != nil || deadline.After(now.Add(remaining-5*time.Minute)) {
			test.Fatal("fresh JWT refused or expiry exceeded native refresh boundary")
		}
	}
}

func TestNative0166ConfigSourceRefusal(test *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for _, raw := range []string{`cli_auth_credentials_store="auto"`, `cli_auth_credentials_store="keyring"`, `profile="foreign"`, `model_context_window=4096`, "[profiles.foreign]\nmodel=\"other\"", `model_catalog_json="/private/foreign.json"`} {
		facts, _, err := InspectNative0166(test.Context(), native0166Image(test, raw), native0166Image(test, ""), now)
		facts.Destroy()
		if err == nil {
			test.Fatal("unknown effective source or unsupported auth store trusted")
		}
	}
}

func TestNative0166OptionalGrammarAndCancellation(test *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	base := native0166OAuth(test, now, "SYNTHETIC_ACCESS")
	for _, scenario := range []struct {
		name   string
		modify func(map[string]any)
		valid  bool
	}{
		{"null-account", func(raw map[string]any) { raw["tokens"].(map[string]any)["account_id"] = nil }, true},
		{"optional-owner", func(raw map[string]any) { raw["tokens"].(map[string]any)["login_owner_id"] = "synthetic-owner" }, true},
		{"optional-provenance", func(raw map[string]any) {
			raw["tokens"].(map[string]any)["oauth_provenance"] = map[string]any{"issuer": "https://synthetic.invalid", "client_id": "synthetic-client"}
		}, true},
		{"unknown-token-field", func(raw map[string]any) { raw["tokens"].(map[string]any)["foreign"] = "PRIVATE_CANARY" }, false},
		{"provenance-type", func(raw map[string]any) { raw["tokens"].(map[string]any)["oauth_provenance"] = "PRIVATE_CANARY" }, false},
		{"missing-refresh", func(raw map[string]any) { delete(raw["tokens"].(map[string]any), "refresh_token") }, false},
		{"missing-refresh-time", func(raw map[string]any) { delete(raw, "last_refresh") }, false},
		{"malformed-identity", func(raw map[string]any) { raw["tokens"].(map[string]any)["id_token"] = "not-jwt" }, false},
		{"malformed-access", func(raw map[string]any) { raw["tokens"].(map[string]any)["access_token"] = "malformed.jwt.value" }, false},
		{"mixed-agent-identity", func(raw map[string]any) { raw["agent_identity"] = "PRIVATE_CANARY" }, false},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			var object map[string]any
			if json.Unmarshal([]byte(base), &object) != nil {
				test.Fatal("synthetic fixture invalid")
			}
			scenario.modify(object)
			raw, err := json.Marshal(object)
			if err != nil {
				test.Fatal("synthetic fixture invalid")
			}
			facts, _, err := InspectNative0166(test.Context(), native0166Image(test, ""), native0166Image(test, string(raw)), now)
			facts.Destroy()
			if (err == nil) != scenario.valid {
				test.Fatal("File grammar classification differs")
			}
		})
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	facts, _, err := InspectNative0166(ctx, native0166Image(test, ""), native0166Image(test, ""), now)
	facts.Destroy()
	if !errors.Is(err, context.Canceled) {
		test.Fatal("canceled classification continued")
	}
}
