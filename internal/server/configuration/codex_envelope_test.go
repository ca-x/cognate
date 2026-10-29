package configuration_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

const codexEnvelopeFixture = `{"version":1,"variant":"codex-provider","provider":{"authMode":"provider-key","endpoint":"https://provider.example/v1","model":"model-test","catalog":{"models":[{"model":"model-test","supportsParallelToolCalls":false}]}},"secrets":{}}`

func putCodexEnvelope(test *testing.T, store *state.Store, raw string) state.ConfigurationAsset {
	test.Helper()
	metadata, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "Stored Codex", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(raw))})
	if err != nil {
		test.Fatal(err)
	}
	return metadata
}

func TestCodexProviderStoredExtras(test *testing.T) {
	for name, raw := range map[string]string{
		"root":     strings.Replace(codexEnvelopeFixture, `"version":1`, `"future":null,"version":1`, 1),
		"provider": strings.Replace(codexEnvelopeFixture, `"authMode":`, `"future":{"private":"synthetic-hidden"},"authMode":`, 1),
		"catalog":  strings.Replace(codexEnvelopeFixture, `"models":`, `"future":[null,1],"models":`, 1),
		"model":    strings.Replace(codexEnvelopeFixture, `"supportsParallelToolCalls":false`, `"supportsParallelToolCalls":false,"future":null`, 1),
		"secrets":  strings.Replace(codexEnvelopeFixture, `"secrets":{}`, `"secrets":{"future":"synthetic-hidden"}`, 1),
	} {
		test.Run(name, func(test *testing.T) {
			store, _, _ := fixture(test)
			metadata := putCodexEnvelope(test, store, raw)
			service := configuration.New(store)
			view, err := service.Edit(test.Context(), "alice", metadata.ID)
			if err != nil || !view.HasPreservedExtras || view.CodexProvider == nil || view.CodexProvider.Catalog.Models[0].SupportsParallelToolCalls == nil || *view.CodexProvider.Catalog.Models[0].SupportsParallelToolCalls {
				test.Fatal("valid unknown data must permit only a complete typed read-only view")
			}
			if _, err := service.Update(test.Context(), "alice", metadata.ID, 1, codexDraft(), nil); !errors.Is(err, configuration.ErrUnsupported) {
				test.Fatal("update must not discard unknown fields")
			}
			if _, err := service.Copy(test.Context(), "alice", metadata.ID, 1, "Unsafe copy"); !errors.Is(err, configuration.ErrUnsupported) {
				test.Fatal("copy must not discard unknown fields")
			}
			after, payload, err := store.ReadConfigurationAssetVersion(test.Context(), "alice", metadata.ID)
			if err != nil {
				test.Fatal(err)
			}
			encoded := payload.Reveal()
			defer clear(encoded)
			if after.Revision != 1 || !bytes.Equal(encoded, []byte(raw)) {
				test.Fatal("refused writes changed source payload or revision")
			}
			assets, err := service.List(test.Context(), "alice")
			if err != nil || len(assets) != 1 {
				test.Fatal("refused copy must not allocate another asset")
			}
			if err := service.Delete(test.Context(), "alice", metadata.ID, 1); err != nil {
				test.Fatal("owner must still be able to explicitly delete unknown-containing asset")
			}
		})
	}
}

func TestCodexProviderStoredInvalid(test *testing.T) {
	for name, raw := range map[string]string{
		"empty":                         `{}`,
		"known-null":                    strings.Replace(codexEnvelopeFixture, `"supportsParallelToolCalls":false`, `"supportsParallelToolCalls":null`, 1),
		"provider-null":                 strings.Replace(codexEnvelopeFixture, `"authMode":"provider-key"`, `"authMode":null`, 1),
		"wrong-type":                    strings.Replace(codexEnvelopeFixture, `"supportsParallelToolCalls":false`, `"supportsParallelToolCalls":"false"`, 1),
		"duplicate-known":               strings.Replace(codexEnvelopeFixture, `"version":1`, `"version":1,"version":1`, 1),
		"duplicate-unknown":             strings.Replace(codexEnvelopeFixture, `"version":1`, `"future":{"key":1,"key":2},"version":1`, 1),
		"unknown-does-not-mask-invalid": strings.Replace(codexEnvelopeFixture, `"version":1`, `"future":null,"version":1`, 1) + `{}`,
		"null-model":                    strings.Replace(codexEnvelopeFixture, `{"model":"model-test","supportsParallelToolCalls":false}`, `null`, 1),
		"unknown-enum":                  strings.Replace(codexEnvelopeFixture, `provider-key`, `native-login`, 1),
		"depth":                         strings.Replace(codexEnvelopeFixture, `"version":1`, `"future":`+strings.Repeat(`[`, 20)+`0`+strings.Repeat(`]`, 20)+`,"version":1`, 1),
	} {
		test.Run(name, func(test *testing.T) {
			store, _, _ := fixture(test)
			metadata := putCodexEnvelope(test, store, raw)
			if view, err := configuration.New(store).Edit(test.Context(), "alice", metadata.ID); !errors.Is(err, configuration.ErrInvalid) || view.Metadata.ID != "" || view.CodexProvider != nil {
				test.Fatal("malformed stored Codex provider must be rejected")
			}
		})
	}
}

func TestCodexProviderEnvelopeByteBoundary(test *testing.T) {
	store, _, _ := fixture(test)
	raw := strings.TrimSuffix(codexEnvelopeFixture, "}") + `,"future":""}`
	raw = strings.Replace(raw, `"future":""`, `"future":"`+strings.Repeat("x", state.MaxConfigurationPayloadBytes-len(raw))+`"`, 1)
	if len(raw) != state.MaxConfigurationPayloadBytes {
		test.Fatal("payload boundary fixture must encode to the exact payload quota")
	}
	metadata := putCodexEnvelope(test, store, raw)
	view, err := configuration.New(store).Edit(test.Context(), "alice", metadata.ID)
	if err != nil || !view.HasPreservedExtras {
		test.Fatal("exact-budget valid stored extras must be readable without lossy writes")
	}
	over := strings.Replace(raw, `"future":"`, `"future":"x`, 1)
	if _, err := store.CreateConfigurationAsset(test.Context(), "alice", state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "Over-budget Codex", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(over))}); !errors.Is(err, state.ErrConfigurationInvalid) {
		test.Fatal("one-byte-over encrypted payload must be rejected")
	}
}

func TestCodexProviderEnvelopeIdentityTamper(test *testing.T) {
	store, path, _ := fixture(test)
	service := configuration.New(store)
	source, err := service.Create(test.Context(), "alice", codexDraft(), codexReplacement("synthetic-encrypted-only-key"))
	if err != nil {
		test.Fatal(err)
	}
	target, err := service.Create(test.Context(), "alice", codexDraft(), nil)
	if err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	var encrypted []byte
	if err := database.QueryRowContext(test.Context(), "SELECT ciphertext FROM configuration_assets WHERE public_id = ?", source.Metadata.ID).Scan(&encrypted); err != nil {
		test.Fatal(err)
	}
	defer clear(encrypted)
	if bytes.Contains(encrypted, []byte("synthetic-encrypted-only-key")) || bytes.Contains(encrypted, []byte("provider.example")) {
		test.Fatal("configuration payload must be encrypted at rest")
	}
	if _, err := database.ExecContext(test.Context(), "UPDATE configuration_assets SET ciphertext = ? WHERE public_id = ?", encrypted, target.Metadata.ID); err != nil {
		test.Fatal(err)
	}
	if view, err := service.Edit(test.Context(), "alice", target.Metadata.ID); !errors.Is(err, configuration.ErrUnavailable) || view.Metadata.ID != "" || view.CodexProvider != nil {
		test.Fatal("valid ciphertext from another asset must fail its AAD identity binding")
	}
	if _, err := service.Update(test.Context(), "alice", target.Metadata.ID, 1, codexDraft(), nil); !errors.Is(err, configuration.ErrUnavailable) {
		test.Fatal("tampered identity must not be overwritten by a typed update")
	}
	if _, err := service.Copy(test.Context(), "alice", target.Metadata.ID, 1, "Tampered copy"); !errors.Is(err, configuration.ErrUnavailable) {
		test.Fatal("tampered identity must not be copied")
	}
}
