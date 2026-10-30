package state

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativesessionauth"
)

func TestCodexNativePreflightSnapshots(test *testing.T) {
	flow := newCodexStateFlow(test)
	flow.prepare(test)
	store := flow.base.fixture.store
	if snapshot, err := store.PreflightNativeCodexPrepare(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.request, flow.prepareProof); err != nil || snapshot.Consumer() != flow.consumer {
		test.Fatal("real prepare snapshot failed")
	}
	if snapshot, err := store.PreflightNativeCodexAuthorize(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.authorize, flow.authorizeProof); err != nil || snapshot.Consumer() != flow.consumer {
		test.Fatal("real authorize snapshot failed")
	}
	flow.issue(test)
	snapshot, err := store.PreflightNativeCodexConsume(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof)
	if err != nil || snapshot.Consumer() != flow.consumer {
		test.Fatal("real consume snapshot failed")
	}
	forged := flow.consume
	forged.Token, _ = nativesessionauth.ParseGrantToken(bytes.Repeat([]byte{44}, 32))
	authorize, consume := flow.prepared.Challenge(), flow.grant.Challenge()
	transcript, err := configclient.CodexConsumeTranscript(flow.grant.Binding().CopyWire(), authorize[:], consume[:], flow.grant.ExpiresMS(), &nativev1.ConsumeNativeMaterializationRequest{PrepareId: forged.PrepareID, GrantId: forged.GrantID, GrantToken: bytes.Repeat([]byte{44}, 32), OperationId: forged.OperationID[:], IntentCommitment: forged.Commitment[:], ClientNonce: forged.Nonce[:]})
	if err != nil {
		test.Fatal("alternative token transcript must be well formed")
	}
	proof, err := flow.base.verifier.VerifyCodexConsume(flow.base.bearer, snapshot.ConsumeFields(forged), ed25519.Sign(flow.base.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal("alternative token proof must cryptographically verify")
	}
	if result, err := store.PreflightNativeCodexConsume(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, forged, proof); !errors.Is(err, ErrNativeUnauthorized) || result.Consumer().OwnerID != "" {
		test.Fatal("valid signature bypassed stored grant-token check")
	}
	if _, err := store.PreflightNativeCodexConsume(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof); err != nil {
		test.Fatal("wrong token burned original grant")
	}
	for _, value := range []any{flow.prepared, flow.grant, flow.data, snapshot, struct{ hidden NativeCodexPrepared }{flow.prepared}, struct{ hidden NativeCodexAuthorization }{flow.grant}, struct{ hidden NativeCodexApplySnapshot }{snapshot}} {
		for _, verb := range []string{"%#v", "%t", "%d", "%s", "%q", "%x", "%b", "%+v", "%020x", "%.128s"} {
			output := fmt.Sprintf(verb, value)
			if strings.Contains(output, "alice") || strings.Contains(output, "synthetic-c2-slot-key") || strings.Contains(output, flow.request.Adapter.DefinitionDigest) || strings.Contains(output, "https://provider.invalid") {
				test.Fatal("state capability private-container leak")
			}
		}
	}
	copyDeclaration := flow.prepared.CopyDeclaration()
	copyDeclaration.Catalog.Models[0].SupportsParallelToolCalls = new(true)
	if *flow.prepared.CopyDeclaration().Catalog.Models[0].SupportsParallelToolCalls {
		test.Fatal("declaration copy aliases state")
	}
	flow.base.fixture.clock.Store(time.UnixMilli(flow.grant.ExpiresMS()).UnixNano())
	if _, err := store.PreflightNativeCodexConsume(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof); !errors.Is(err, ErrNativePrecondition) {
		test.Fatal("expired grant admitted")
	}
	flow.base.fixture.clock.Add(-int64(time.Second))
	if _, err := store.PreflightNativeCodexConsume(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.consume, flow.consumeProof); !errors.Is(err, ErrNativePrecondition) {
		test.Fatal("clock rollback revived sticky expired grant")
	}
}

func TestCodexNativeAuthenticatedRecordRouting(test *testing.T) {
	claude := newNativeConfigurationFlow(test)
	protocol, err := claude.fixture.store.NativeConfigurationProtocol(test.Context(), claude.credential, claude.requestOrigin(), claude.request.OperationID, claude.prepared.Binding().PrepareID)
	if err != nil || protocol != 1 {
		test.Fatal("existing Claude record no longer routes")
	}
	flow := newCodexStateFlow(test)
	flow.prepare(test)
	store := flow.base.fixture.store
	protocol, err = store.NativeConfigurationProtocol(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, flow.request.OperationID, flow.prepared.Binding().CopyWire().PrepareId)
	if err != nil || protocol != 2 {
		test.Fatal("Codex record failed authenticated routing")
	}
	operation := flow.request.OperationID
	operation[0] ^= 1
	if protocol, err := store.NativeConfigurationProtocol(test.Context(), flow.base.credential, flow.consumer.ServerOrigin, operation, flow.prepared.Binding().CopyWire().PrepareId); !errors.Is(err, ErrNativeUnauthorized) || protocol != 0 {
		test.Fatal("caller operation hint bypassed real child gate")
	}
	if protocol, err := store.NativeConfigurationProtocol(test.Context(), NativeConsumerCredential{}, flow.consumer.ServerOrigin, flow.request.OperationID, flow.prepared.Binding().CopyWire().PrepareId); !errors.Is(err, ErrNativeUnauthorized) || protocol != 0 {
		test.Fatal("unauthenticated record family leaked")
	}
}
