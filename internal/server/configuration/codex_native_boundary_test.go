package configuration

import (
	"crypto/ed25519"
	"database/sql"
	"errors"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
)

func TestCodexAssetNativeDenied(test *testing.T) {
	fixture := newNativeOrderFixture(test, APIKey)
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, Draft{Name: "Codex not native", CodexProvider: &CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://provider.example/v1", Model: "model-test"}}, []SecretMutation{{Slot: CodexProviderKeySlot, Action: Replace, Replacement: NewSecret([]byte("synthetic-codex-native-boundary"))}})
	if err != nil {
		test.Fatal(err)
	}
	if _, _, err := fixture.domain.load(test.Context(), fixture.owner, asset.Metadata.ID); !errors.Is(err, ErrUnsupported) {
		test.Fatal("Claude native loader must remain closed to Codex")
	}
	snapshot, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, fixture.prepare, fixture.prepareSignature)
	if err != nil {
		test.Fatal("positive control must prove valid native consumer and prepare signature")
	}
	consumer := snapshot.Consumer()
	request := fixture.prepare
	request.AssetID = asset.Metadata.ID
	request.AssetRevision = asset.Metadata.Revision
	transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: consumerKeyHash(consumer.PublicKey[:])}, wirePrepareRequest(request))
	if err != nil {
		test.Fatal(err)
	}
	signature := ed25519.Sign(fixture.key, transcript.CopyBytes())
	if _, err := fixture.consumer.PreflightPrepare(test.Context(), fixture.bearer, request, signature); err != nil {
		test.Fatal("Codex asset identifier must reach business gate with a valid proof")
	}
	if _, err := fixture.native.Prepare(test.Context(), fixture.bearer, request, signature); !errors.Is(err, ErrUnsupported) {
		test.Fatal("validly signed Claude request must not prepare a Codex asset")
	}
	database, err := sql.Open("sqlite", fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	for _, query := range []string{"SELECT count(*) FROM native_apply_preparations", "SELECT count(*) FROM native_materialization_grants"} {
		var count int
		if err := database.QueryRowContext(test.Context(), query).Scan(&count); err != nil || count != 0 {
			test.Fatal("denied Codex prepare must not allocate native authorization rows")
		}
	}
}
