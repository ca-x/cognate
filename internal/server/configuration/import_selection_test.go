package configuration_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestPrepareImportedDraftParity(test *testing.T) {
	store, _, _ := fixture(test)
	claude := draft()
	claude.Name = "Imported Claude"
	gemini := configuration.Draft{Name: "Imported Gemini", GeminiProvider: &configuration.GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, KeyTransport: new(agentconfig.GeminiBearer), EnvModel: new("env-model"), SettingsModel: new("settings-model")}}
	for _, input := range []configuration.Draft{claude, codexDraft(), gemini} {
		before, err := store.ListConfigurationAssets(test.Context(), "alice")
		if err != nil {
			test.Fatal(err)
		}
		prepared, err := configuration.PrepareImportedDraft(test.Context(), input)
		if err != nil || prepared.Name != input.Name || prepared.SchemaVersion != 1 || prepared.Kind != state.ConfigurationKindProvider {
			test.Fatal("valid source draft not prepared losslessly")
		}
		assets, err := store.ListConfigurationAssets(test.Context(), "alice")
		if err != nil {
			test.Fatal(err)
		}
		count := len(assets)
		if count != len(before) {
			test.Fatal("preparation wrote an asset")
		}
		created, err := configuration.New(store).Create(test.Context(), "alice", input, nil)
		if err != nil {
			test.Fatal(err)
		}
		stored, err := store.ReadConfigurationAsset(test.Context(), "alice", created.Metadata.ID)
		if err != nil || prepared.AgentID != created.Metadata.AgentID || !bytes.Equal(stored.Reveal(), prepared.Payload.Reveal()) {
			test.Fatal("import envelope differs from ordinary create")
		}
		if len(created.Secrets) == 0 {
			test.Fatal("credential metadata missing")
		}
		for _, secret := range created.Secrets {
			if secret.Present {
				test.Fatal("import implicitly created a secret")
			}
		}
		assets, err = store.ListConfigurationAssets(test.Context(), "alice")
		if err != nil || len(assets) != count+1 {
			test.Fatal("preparation wrote an asset")
		}
	}
	bad := claude
	bad.Name = " Imported Claude "
	if _, err := configuration.PrepareImportedDraft(test.Context(), bad); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatal("import silently normalized source name")
	}
	bad = claude
	bad.CodexProvider = codexDraft().CodexProvider
	if _, err := configuration.PrepareImportedDraft(test.Context(), bad); !errors.Is(err, configuration.ErrInvalid) {
		test.Fatal("mixed provider accepted")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := configuration.PrepareImportedDraft(ctx, claude); !errors.Is(err, context.Canceled) {
		test.Fatal("preparation ignored cancellation")
	}
}
