package httpapi

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/server/configuration"
	"google.golang.org/protobuf/proto"
)

const codexHTTPSecret = "synthetic-C2-provider-key-exact-bytes"

type codexHTTPFlow struct{ *nativeHTTPFlow }

func codexHTTPAsset(test *testing.T, fixture *nativeHTTPFixture) configuration.EditView {
	test.Helper()
	provider := &configuration.CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://synthetic-c2-provider.invalid/v1", Model: "alpha", ReasoningEffort: new(agentconfig.CodexReasoningEffort("high")), Catalog: &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "alpha", DisplayName: new("Alpha"), ContextWindow: new(uint64(131072)), SupportsParallelToolCalls: new(false), InputModalities: new([]agentconfig.CodexModality{"image", "text"}), BaseInstructions: new("Keep\r\nspaces\t"), ReasoningLevels: new([]agentconfig.CodexReasoningEffort{"high", "none"}), DefaultReasoningLevel: new(agentconfig.CodexReasoningEffort("high"))}, {Model: "beta"}}}}
	asset, err := fixture.domain.Create(test.Context(), fixture.ownerID, configuration.Draft{Name: "synthetic-Codex-C2", CodexProvider: provider}, []configuration.SecretMutation{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte(codexHTTPSecret))}})
	if err != nil {
		test.Fatal("cannot create real encrypted Codex asset")
	}
	return asset
}
func (flow *codexHTTPFlow) prepare(test *testing.T, asset configuration.EditView) {
	test.Helper()
	request := nativeCodexPrepareWire()
	request.ExpectedOwnerId = flow.consumer.OwnerID
	request.AssetId = asset.Metadata.ID
	request.ExpectedAssetRevision = asset.Metadata.Revision
	request.OperationId = bytes.Clone(flow.operation)
	request.ProofSignature = nil
	transcript, err := configclient.CodexPrepareTranscript(flow.consumer, request)
	if err != nil {
		test.Fatal("Codex prepare proof builder failed")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	response, err := flow.fixture.client.PrepareNativeApply(test.Context(), flow.fixture.request(request, flow.bearer))
	if err != nil {
		test.Fatalf("real Codex Prepare failed: %s", connect.CodeOf(err))
	}
	if configclient.ValidateCodexPreparedDeclaration(response.Msg) != nil || response.Msg.Patch != nil || !response.Msg.MaterializationRequired || response.Header().Get("Cache-Control") != "no-store" {
		test.Fatal("invalid real Codex preparation response")
	}
	flow.prepared = response.Msg
}
func codexHTTPProjection() *nativev1.CodexLocalBindingV1 {
	return &nativev1.CodexLocalBindingV1{BindingVersion: 1, NativeProfileId: "synthetic-protocol-not-D", NativeProfileSha256: bytes.Repeat([]byte{1}, 32), TemplateSha256: bytes.Repeat([]byte{2}, 32), EffectiveScope: 1, ProviderOwnership: 1, CatalogOwnership: 1, AuthStore: 2, KeyringState: 1, FileLoginState: 4, LocalAuthPolicy: "preserve-local-login-v1", RequiresOpenaiAuth: new(true), ObservationWitness: bytes.Repeat([]byte{3}, 32), Baselines: []*nativev1.CodexRoleBaseline{{Role: 1, State: &nativev1.CodexRoleBaseline_Missing{Missing: new(nativev1.CodexMissing)}}, {Role: 2, State: &nativev1.CodexRoleBaseline_NotObserved{NotObserved: new(nativev1.CodexNotObserved)}}, {Role: 3, State: &nativev1.CodexRoleBaseline_Missing{Missing: new(nativev1.CodexMissing)}}}}
}
func (flow *codexHTTPFlow) authorizeRequest(test *testing.T) *connect.Request[nativev1.AuthorizeNativeMaterializationRequest] {
	test.Helper()
	local := codexHTTPProjection()
	expiry := flow.prepared.Binding.PrepareExpiresAtUnixMs
	commitment, err := configclient.HashCodexIntent(flow.prepared.Binding, local, 7, expiry)
	if err != nil {
		test.Fatal("client complete Codex intent failed")
	}
	request := &nativev1.AuthorizeNativeMaterializationRequest{PrepareId: flow.prepared.Binding.PrepareId, OperationId: bytes.Clone(flow.operation), IntentCommitment: commitment[:], RequestedSlots: []nativev1.SecretSlot{3}, Authorization: &nativev1.AuthorizeNativeMaterializationRequest_ExplicitConfirmation{ExplicitConfirmation: &nativev1.ExplicitConfirmation{ConfirmationRevision: 7}}, ClientNonce: bytes.Repeat([]byte{9}, 32), IntentExpiresAtUnixMs: expiry, CodexLocalBinding: local}
	transcript, err := configclient.CodexAuthorizeTranscript(flow.prepared.Binding, flow.prepared.AuthorizeChallenge, request)
	if err != nil {
		test.Fatal("client authorize proof failed")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	return flow.fixture.request(request, flow.bearer)
}
func (flow *codexHTTPFlow) authorize(test *testing.T) {
	test.Helper()
	response, err := flow.fixture.client.AuthorizeNativeMaterialization(test.Context(), flow.authorizeRequest(test))
	if err != nil {
		test.Fatalf("real Codex Authorize failed: %s", connect.CodeOf(err))
	}
	if len(response.Msg.GrantToken) != 32 || !response.Msg.MaterializationRequired || response.Msg.Binding.GetCodexLocalBinding() == nil {
		test.Fatal("incomplete Codex grant")
	}
	flow.grant = response.Msg
}
func (flow *codexHTTPFlow) consumeRequest(test *testing.T) *connect.Request[nativev1.ConsumeNativeMaterializationRequest] {
	test.Helper()
	request := &nativev1.ConsumeNativeMaterializationRequest{PrepareId: flow.prepared.Binding.PrepareId, GrantId: flow.grant.GrantId, GrantToken: bytes.Clone(flow.grant.GrantToken), OperationId: bytes.Clone(flow.operation), IntentCommitment: bytes.Clone(flow.grant.Binding.IntentCommitment), ClientNonce: bytes.Repeat([]byte{12}, 32)}
	transcript, err := configclient.CodexConsumeTranscript(flow.grant.Binding, flow.prepared.AuthorizeChallenge, flow.grant.ConsumeChallenge, flow.grant.ExpiresAtUnixMs, request)
	if err != nil {
		test.Fatal("Codex consume proof failed")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	return flow.fixture.request(request, flow.bearer)
}
func (flow *codexHTTPFlow) status(test *testing.T) *nativev1.GetNativeMaterializationStatusResponse {
	test.Helper()
	request := &nativev1.GetNativeMaterializationStatusRequest{PrepareId: flow.prepared.Binding.PrepareId, OperationId: bytes.Clone(flow.operation), ClientNonce: bytes.Repeat([]byte{13}, 32)}
	transcript, err := configclient.CodexStatusTranscript(flow.prepared.Binding, flow.prepared.AuthorizeChallenge, request)
	if err != nil {
		test.Fatal("Codex status proof failed")
	}
	request.ProofSignature = ed25519.Sign(flow.key, transcript.CopyBytes())
	response, err := flow.fixture.client.GetNativeMaterializationStatus(test.Context(), flow.fixture.request(request, flow.bearer))
	if err != nil {
		test.Fatal("real safe Codex Status failed")
	}
	return response.Msg
}

func TestCodexNativeTLSLifecycle(test *testing.T) {
	fixture := newNativeHTTPFixture(test)
	asset := codexHTTPAsset(test, fixture)
	flow := &codexHTTPFlow{fixture.bootstrap(test)}
	flow.prepare(test, asset)
	declaration := flow.prepared.CodexDeclaration
	if declaration.Catalog.Models[0].SupportsParallelToolCalls == nil || *declaration.Catalog.Models[0].SupportsParallelToolCalls || declaration.Catalog.Models[0].InputModalities.Values[0] != 2 || declaration.Catalog.Models[0].BaseInstructions == nil || *declaration.Catalog.Models[0].BaseInstructions != "Keep\r\nspaces\t" {
		test.Fatal("real A/catalog optional false, order or raw bytes lost")
	}
	if flow.status(test).State != 1 {
		test.Fatal("prepared Status should be not-issued")
	}
	flow.authorize(test)
	if flow.status(test).State != 2 {
		test.Fatal("authorized Status should be issued")
	}
	response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test))
	if err != nil {
		test.Fatalf("real Codex Consume failed: %s", connect.CodeOf(err))
	}
	if len(response.Msg.SlotValues) != 1 || response.Msg.SlotValues[0].Slot != 3 || !bytes.Equal(response.Msg.SlotValues[0].Plaintext, []byte(codexHTTPSecret)) || !proto.Equal(response.Msg.Binding, flow.grant.Binding) {
		test.Fatal("single-slot exact bytes/binding mismatch")
	}
	if flow.status(test).State != 3 {
		test.Fatal("consumed fact missing")
	}
	if result, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test)); err == nil || result != nil {
		test.Fatal("Codex materialization replay succeeded")
	}
}

func TestCodexNativeHTTPPrivacy(test *testing.T) {
	var captured bytes.Buffer
	originalLog, originalSlog := log.Writer(), slog.Default()
	log.SetOutput(&captured)
	slog.SetDefault(slog.New(slog.NewTextHandler(&captured, nil)))
	test.Cleanup(func() { log.SetOutput(originalLog); slog.SetDefault(originalSlog) })
	fixture := newNativeHTTPFixture(test)
	asset := codexHTTPAsset(test, fixture)
	flow := &codexHTTPFlow{fixture.bootstrap(test)}
	flow.prepare(test, asset)
	flow.authorize(test)
	proof := flow.consumeRequest(test).Msg.ProofSignature
	secrets := [][]byte{[]byte(codexHTTPSecret), []byte(fixture.parent), []byte(flow.bearer), bytes.Clone(flow.grant.GrantToken), proof, flow.grant.Binding.IntentCommitment, flow.prepared.Binding.Codex.DeclarationSha256}
	for _, body := range [][]byte{[]byte(codexHTTPSecret), bytes.Repeat([]byte{'X'}, nativeMaxWireBytes+1)} {
		request, _ := http.NewRequestWithContext(test.Context(), http.MethodPost, fixture.endpoint.URL+nativeServicePath+"ConsumeNativeMaterialization", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/proto")
		request.Header.Set("Connect-Protocol-Version", "1")
		request.Header.Set("Authorization", "Bearer "+flow.bearer)
		response, err := fixture.endpoint.Client().Do(request)
		if err != nil {
			test.Fatal("privacy negative request failed setup")
		}
		encoded, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode < 400 || response.Header.Get("Cache-Control") != "no-store" {
			test.Fatal("privacy negative did not actually fail")
		}
		for _, secret := range secrets {
			if bytes.Contains(encoded, secret) {
				test.Fatal("error response leaked sensitive bytes")
			}
		}
	}
	response, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test))
	if err != nil || !bytes.Equal(response.Msg.SlotValues[0].Plaintext, []byte(codexHTTPSecret)) {
		test.Fatal("privacy cannot be faked by suppressing valid key")
	}
	for _, secret := range secrets {
		for _, representation := range []string{string(secret), hex.EncodeToString(secret), base64.StdEncoding.EncodeToString(secret), base64.RawURLEncoding.EncodeToString(secret), fmt.Sprint(secret)} {
			if strings.Contains(captured.String(), representation) {
				test.Fatal("stdlog/slog leaked Codex protocol/material bytes")
			}
		}
	}
}

func TestCodexNativeLostReceiptOnlyStatus(test *testing.T) {
	for _, stage := range []string{"AuthorizeNativeMaterialization", "ConsumeNativeMaterialization"} {
		test.Run(stage, func(test *testing.T) {
			var armed atomic.Bool
			fixture := newNativeHTTPFixture(test, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if strings.HasSuffix(request.URL.Path, "/"+stage) && armed.CompareAndSwap(true, false) {
						next.ServeHTTP(nativeLostResponseWriter{ResponseWriter: writer, truncate: true}, request)
						return
					}
					next.ServeHTTP(writer, request)
				})
			})
			asset := codexHTTPAsset(test, fixture)
			flow := &codexHTTPFlow{fixture.bootstrap(test)}
			flow.prepare(test, asset)
			if stage == "ConsumeNativeMaterialization" {
				flow.authorize(test)
			}
			armed.Store(true)
			if stage == "AuthorizeNativeMaterialization" {
				result, err := fixture.client.AuthorizeNativeMaterialization(test.Context(), flow.authorizeRequest(test))
				if err == nil || result != nil {
					test.Fatal("lost grant receipt pretended success")
				}
				if flow.status(test).State != 2 {
					test.Fatal("lost Authorize did not actually COMMIT")
				}
			} else {
				result, err := fixture.client.ConsumeNativeMaterialization(test.Context(), flow.consumeRequest(test))
				if err == nil || result != nil {
					test.Fatal("lost consume receipt pretended success")
				}
				if flow.status(test).State != 3 {
					test.Fatal("lost Consume did not actually COMMIT")
				}
			}
		})
	}
}

func TestCodexNativeTransportLifetime(test *testing.T) {
	TestNativeHTTPEightRealBlockedBodiesCancelAndDrain(test)
}
