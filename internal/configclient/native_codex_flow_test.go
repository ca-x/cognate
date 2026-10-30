package configclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/codexconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestCodexNativeClientStrictAndMixedVersions(test *testing.T) {
	_, _, response, _ := clientCodexFixture(test)
	encoded, err := proto.Marshal(response)
	if err != nil {
		test.Fatal("cannot marshal typed fixture")
	}
	decoded := new(nativev1.PrepareNativeApplyResponse)
	if nativeDecode(encoded, decoded, nativeWireLimit) != nil || !proto.Equal(decoded, response) {
		test.Fatal("private client codec rejected complete Codex response")
	}
	for _, mutate := range []func(*nativev1.PrepareNativeApplyResponse){func(value *nativev1.PrepareNativeApplyResponse) { value.CodexDeclaration = nil }, func(value *nativev1.PrepareNativeApplyResponse) { value.Binding.Codex = nil }, func(value *nativev1.PrepareNativeApplyResponse) { value.Binding.Adapter.PluginVersion = "1.0.0" }, func(value *nativev1.PrepareNativeApplyResponse) { value.Patch = new(nativev1.ClaudeProviderPatch) }, func(value *nativev1.PrepareNativeApplyResponse) {
		value.CodexDeclaration.Catalog.Models[0].SupportsParallelToolCalls = nil
	}} {
		candidate := proto.Clone(response).(*nativev1.PrepareNativeApplyResponse)
		mutate(candidate)
		encoded, _ := proto.Marshal(candidate)
		before := proto.Clone(decoded)
		if nativeDecode(encoded, decoded, nativeWireLimit) == nil || !proto.Equal(before, decoded) {
			test.Fatal("mixed/old/incomplete response accepted or partially replaced result")
		}
	}
}

func codexClientPrepared(test *testing.T, fixture *nativeClientFixture) (*NativeFlow, CodexNativePreparation, CodexNativeApproval) {
	test.Helper()
	asset := codexClientAsset(test, fixture)
	flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer)
	if err != nil {
		test.Fatal("real consumer bootstrap failed")
	}
	test.Cleanup(flow.Close)
	prepared, err := flow.PrepareCodex(test.Context(), NativeSelection{AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: "selected-profile", SelectionRevision: 1})
	if err != nil {
		test.Fatal("real Codex prepare failed")
	}
	test.Cleanup(prepared.Destroy)
	approval, err := NewCodexNativeApproval(1, time.UnixMilli(prepared.CopyBinding().PrepareExpiresAtUnixMs).Add(-time.Second), true, true)
	if err != nil {
		test.Fatal("cannot construct explicit approval")
	}
	test.Cleanup(approval.Destroy)
	return flow, prepared, approval
}

func TestCodexNativeClientUnknownAndClose(test *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, stage := range []string{"AuthorizeNativeMaterialization", "ConsumeNativeMaterialization"} {
			for _, mode := range []string{"lost", "cancel", "flow-close", "client-close"} {
				test.Run(fmt.Sprintf("h2=%t/%s/%s", h2, stage, mode), func(test *testing.T) {
					var authorizes, consumes, statuses, intercepted atomic.Int32
					entered, drained := make(chan struct{}), make(chan struct{})
					fixture := newNativeClientFixture(test, h2, func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
							switch {
							case strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization"):
								authorizes.Add(1)
							case strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization"):
								consumes.Add(1)
							case strings.HasSuffix(request.URL.Path, "/GetNativeMaterializationStatus"):
								statuses.Add(1)
							}
							if !strings.HasSuffix(request.URL.Path, "/"+stage) {
								next.ServeHTTP(writer, request)
								return
							}
							if intercepted.Add(1) != 1 {
								writer.WriteHeader(http.StatusConflict)
								return
							}
							recorder := &nativeCaptureWriter{header: make(http.Header), actual: writer}
							next.ServeHTTP(recorder, request)
							defer clear(recorder.body.Bytes())
							defer close(drained)
							if recorder.status != http.StatusOK {
								test.Error("expected real successful COMMIT before lost receipt")
							}
							close(entered)
							if mode != "lost" {
								<-request.Context().Done()
							}
							writer.Header().Set("Content-Type", "application/proto")
							writer.WriteHeader(http.StatusOK)
						})
					})
					flow, _, approval := codexClientPrepared(test, fixture)
					_, _, _, local := clientCodexFixture(test)
					if stage == "ConsumeNativeMaterialization" {
						grant, err := flow.authorizeCodex(test.Context(), local, approval)
						defer grant.Destroy()
						if err != nil {
							test.Fatal("actual authorization failed before consume receipt test")
						}
					}
					ctx, cancel := context.WithCancel(test.Context())
					defer cancel()
					result := make(chan error, 1)
					go func() {
						if stage == "AuthorizeNativeMaterialization" {
							grant, err := flow.authorizeCodex(ctx, local, approval)
							defer grant.Destroy()
							if grant.CopyBinding() != nil || grant.ID() != "" {
								test.Error("unknown issuance returned authority")
							}
							result <- err
						} else {
							material, err := consumeCodexProtocolFixture(ctx, flow)
							defer material.Destroy()
							if len(material.CloneBytes()) != 0 {
								test.Error("unknown materialization returned secret")
							}
							result <- err
						}
					}()
					<-entered
					switch mode {
					case "cancel":
						cancel()
					case "flow-close":
						flow.Close()
					case "client-close":
						fixture.client.Close()
					}
					if err := <-result; !errors.Is(err, ErrNativeUnknown) {
						test.Fatal("lost/canceled postcommit request must remain unknown")
					}
					select {
					case <-drained:
					case <-time.After(3 * time.Second):
						test.Fatal("actual TLS handler failed to drain")
					}
					if _, err := flow.authorizeCodex(test.Context(), local, approval); err == nil {
						test.Fatal("authorization replayed after unknown/Close")
					}
					material, err := consumeCodexProtocolFixture(test.Context(), flow)
					defer material.Destroy()
					if err == nil || len(material.CloneBytes()) != 0 || authorizes.Load() != 1 || consumes.Load() != map[bool]int32{false: 0, true: 1}[stage == "ConsumeNativeMaterialization"] {
						test.Fatal("unknown request caused sensitive replay")
					}
					status, err := flow.StatusCodex(test.Context())
					if strings.HasSuffix(mode, "close") {
						if !errors.Is(err, ErrNativeCanceled) || statuses.Load() != 0 || len(flow.key.CloneBytes()) != 0 || len(flow.bearer.CloneBytes()) != 0 || len(flow.grant.CloneBytes()) != 0 || flow.codex.prepared != nil {
							test.Fatal("Close readmitted Status or retained owned credentials")
						}
					} else {
						expected := NativeIssued
						if stage == "ConsumeNativeMaterialization" {
							expected = NativeConsumed
						}
						if err != nil || status.State != expected || statuses.Load() != 1 {
							test.Fatal("safe Status failed to observe actual committed state")
						}
					}
				})
			}
		}
	}
}

func TestCodexNativeClientQueuedCancelAndDrain(test *testing.T) {
	for _, h2 := range []bool{false, true} {
		test.Run(fmt.Sprintf("h2=%t", h2), func(test *testing.T) {
			entered, release, drained := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var statuses atomic.Int32
			fixture := newNativeClientFixture(test, h2, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if strings.HasSuffix(request.URL.Path, "/GetNativeMaterializationStatus") {
						statuses.Add(1)
					}
					if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") {
						_, _ = io.Copy(io.Discard, request.Body)
						close(entered)
						defer close(drained)
						select {
						case <-release:
						case <-request.Context().Done():
						}
						return
					}
					next.ServeHTTP(writer, request)
				})
			})
			flow, _, approval := codexClientPrepared(test, fixture)
			_, _, _, local := clientCodexFixture(test)
			holder := make(chan error, 1)
			go func() { _, err := flow.authorizeCodex(test.Context(), local, approval); holder <- err }()
			<-entered
			ctx, cancel := context.WithCancel(test.Context())
			waiter := make(chan error, 1)
			go func() { _, err := flow.StatusCodex(ctx); waiter <- err }()
			cancel()
			if err := <-waiter; !errors.Is(err, ErrNativeCanceled) || statuses.Load() != 0 {
				test.Fatal("canceled queued caller reached the network")
			}
			select {
			case <-drained:
				test.Fatal("queued cancellation affected another live operation")
			default:
			}
			closed := make(chan struct{}, 4)
			for range 2 {
				go func() { flow.Close(); closed <- struct{}{} }()
				go func() { fixture.client.Close(); closed <- struct{}{} }()
			}
			for range 4 {
				select {
				case <-closed:
				case <-time.After(3 * time.Second):
					unblock()
					test.Fatal("concurrent Close failed to join actual HTTP operation")
				}
			}
			if err := <-holder; !errors.Is(err, ErrNativeUnknown) {
				test.Fatal("closed sensitive operation lost unknown classification")
			}
			<-drained
		})
	}
}

func TestCodexNativePrivateContainers(test *testing.T) {
	_, _, response, local := clientCodexFixture(test)
	response.Binding.OwnerId = "synthetic-private-codex-owner-do-not-log"
	response.AuthorizeChallenge = bytes.Repeat([]byte{177}, 32)
	prepared := CodexNativePreparation{codexClientOpaque: codexClientWrap(codexPreparedData{response: response, sessionExpires: time.UnixMilli(1700000200000)})}
	defer prepared.Destroy()
	approval, _ := NewCodexNativeApproval(12345678987654321, time.UnixMilli(1700000100000), true, true)
	defer approval.Destroy()
	authorization := CodexNativeAuthorization{codexClientOpaque: codexClientWrap(codexAuthorizationData{binding: &nativev1.ApplyBinding{Preparation: proto.Clone(response.Binding).(*nativev1.PreparationBinding), IntentCommitment: bytes.Repeat([]byte{177}, 32), CodexLocalBinding: local}, id: "synthetic-private-codex-grant-do-not-log"})}
	defer authorization.Destroy()
	needles := []string{response.Binding.OwnerId, authorization.ID(), "12345678987654321", "177 177 177", hex.EncodeToString(response.AuthorizeChallenge), "provider.example", "model-a"}
	var typedNil *CodexNativePreparation
	for _, root := range []any{prepared, &prepared, approval, &approval, authorization, &authorization, CodexNativePreparation{}, typedNil} {
		for _, value := range []any{root, struct{ hidden any }{root}, struct{ hidden []any }{[]any{root}}, []any{root}, map[string]any{"hidden": root}, struct{ hidden map[string]any }{map[string]any{"value": root}}} {
			check := func(output []byte) {
				for _, needle := range needles {
					if bytes.Contains(output, []byte(needle)) {
						test.Fatal("opaque Codex value leaked through logging/serialization")
					}
				}
			}
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%t", "%b", "%d", "%x", "%X", "%o", "%O", "%U", "%c", "%f", "%F", "%g", "%G", "%e", "%E", "%020x", "%.128s", "%#080.20v"} {
				check([]byte(fmt.Sprintf(verb, value)))
			}
			encoded, err := json.Marshal(value)
			check(encoded)
			if err != nil {
				check([]byte(err.Error()))
			}
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			logger.Info("synthetic", "value", value)
			check(logs.Bytes())
			logs.Reset()
			previous := log.Writer()
			log.SetOutput(&logs)
			log.Print(value)
			log.SetOutput(previous)
			check(logs.Bytes())
		}
	}
	text, _ := authorization.MarshalText()
	if string(text) != "[redacted Codex native]" {
		test.Fatal("text serialization is not fixed redaction")
	}
}

func TestCodexNativeClientWireBoundaries(test *testing.T) {
	_, _, response, _ := clientCodexFixture(test)
	encoded, _ := proto.Marshal(response)
	for name, bad := range map[string][]byte{"unknown": protowire.AppendBytes(protowire.AppendTag(bytes.Clone(encoded), 99, protowire.BytesType), nil), "duplicate": protowire.AppendBytes(protowire.AppendTag(bytes.Clone(encoded), 5, protowire.BytesType), nil), "overlong-tag": {0x8a, 0, 0}, "over-limit": bytes.Repeat([]byte{1}, nativeWireLimit+1)} {
		test.Run(name, func(test *testing.T) {
			target := proto.Clone(response).(*nativev1.PrepareNativeApplyResponse)
			before := proto.Clone(target)
			if nativeDecode(bad, target, nativeWireLimit) == nil || !proto.Equal(before, target) {
				test.Fatal("invalid wire accepted or destination partially changed")
			}
		})
	}
	descriptor := response.CodexDeclaration.Catalog.Models[0].InputModalities.ProtoReflect().Descriptor()
	for _, packed := range []bool{false, true} {
		var data []byte
		if packed {
			data = protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), []byte{2, 1})
		} else {
			data = []byte{8, 2, 8, 1}
		}
		if !strictProofWire(data, descriptor, 0) {
			test.Fatal("legal ordered packed/unpacked enum rejected")
		}
	}
	for _, data := range [][]byte{protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), bytes.Repeat([]byte{1}, 1024)), {10, 1, 0}, {10, 1, 99}, {8, 0x81, 0}} {
		if strictProofWire(data, descriptor, 0) {
			test.Fatal("packed count or enum/varint validation bypassed")
		}
	}
	if strictProofWire(nil, descriptor, 16) {
		test.Fatal("recursive depth limit bypassed")
	}
}

func TestCodexNativeClientMaterialKeyBounds(test *testing.T) {
	_, _, prepared, local := clientCodexFixture(test)
	commitment, err := HashCodexIntent(prepared.Binding, local, 1, prepared.Binding.PrepareExpiresAtUnixMs)
	if err != nil {
		test.Fatal("cannot construct bound response")
	}
	base := &nativev1.ConsumeNativeMaterializationResponse{Binding: &nativev1.ApplyBinding{Preparation: prepared.Binding, IntentCommitment: commitment[:], IntentExpiresAtUnixMs: prepared.Binding.PrepareExpiresAtUnixMs, CodexLocalBinding: local}, GrantId: "synthetic-bound-key", ConsumedAtUnixMs: prepared.Binding.PreparedAtUnixMs + 1}
	for _, pair := range []struct {
		body  []byte
		valid bool
	}{{[]byte("x"), true}, {bytes.Repeat([]byte("é"), 4096), true}, {nil, false}, {bytes.Repeat([]byte("x"), 8193), false}, {[]byte{0xff}, false}, {[]byte("x\x00y"), false}, {[]byte("x\ny"), false}, {[]byte("x\ry"), false}} {
		base.SlotValues = []*nativev1.SlotValue{{Slot: 3, Plaintext: pair.body}}
		encoded, _ := proto.Marshal(base)
		var target nativev1.ConsumeNativeMaterializationResponse
		err := nativeDecode(encoded, &target, nativeWireLimit)
		if (err == nil) != pair.valid || !pair.valid && len(target.SlotValues) != 0 {
			test.Fatal("material key UTF8/byte/control boundary changed or failure returned data")
		}
		nativeClear(&target)
		clear(encoded)
	}
}

func TestCodexNativeFlowPrivateContainers(test *testing.T) {
	flow := &NativeFlow{nativeFlowPayload: &nativeFlowPayload{nativeFlowSensitive: &nativeFlowSensitive{consumer: ConsumerBinding{OwnerID: "synthetic-codex-flow-private-owner", ServerOrigin: "https://private-flow.invalid", NativeSessionBinding: bytes.Repeat([]byte{77}, 32), OperationID: bytes.Repeat([]byte{88}, 16), ConsumerKeySHA256: bytes.Repeat([]byte{99}, 32)}, grantID: "synthetic-private-flow-grant", consumeChallenge: bytes.Repeat([]byte{111}, 32)}}}
	valueCopy := reflect.ValueOf(flow).Elem().Interface()
	values := []any{flow, valueCopy, struct{ hidden *NativeFlow }{flow}, struct{ hidden any }{valueCopy}, []*NativeFlow{flow}, []any{valueCopy}}
	for _, value := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%t", "%b", "%d", "%x", "%X", "%o", "%O", "%U", "%c", "%020x", "%.128s"} {
			output := fmt.Sprintf(verb, value)
			for _, needle := range []string{"synthetic-codex-flow-private-owner", "https://private-flow.invalid", "synthetic-private-flow-grant", "77 77 77", "111 111 111"} {
				if strings.Contains(output, needle) {
					test.Fatal("flow private-container formatting leaks protocol identity/challenge")
				}
			}
		}
	}
}

func codexClientAsset(test *testing.T, fixture *nativeClientFixture) configuration.EditView {
	test.Helper()
	oracle, _, _, _ := clientCodexFixture(test)
	declaration := oracle.Declaration
	provider := &configuration.CodexProvider{AuthMode: "provider-key", Endpoint: declaration.Endpoint, Model: declaration.Model, ReasoningEffort: declaration.ReasoningEffort, Catalog: declaration.Catalog}
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, configuration.Draft{Name: "synthetic-C2-client", CodexProvider: provider}, []configuration.SecretMutation{{Slot: configuration.CodexProviderKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-C2-client-slot-key"))}})
	if err != nil {
		test.Fatal("cannot create encrypted Codex client fixture")
	}
	return asset
}

func TestCodexNativeClientFlow(test *testing.T) {
	for _, h2 := range []bool{false, true} {
		test.Run(map[bool]string{false: "h1", true: "h2"}[h2], func(test *testing.T) {
			var authorizes, consumes, prepares atomic.Int32
			fixture := newNativeClientFixture(test, h2, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					switch {
					case strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization"):
						authorizes.Add(1)
					case strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization"):
						consumes.Add(1)
					case strings.HasSuffix(request.URL.Path, "/PrepareNativeApply"):
						prepares.Add(1)
					}
					next.ServeHTTP(writer, request)
				})
			})
			asset := codexClientAsset(test, fixture)
			flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer)
			if err != nil {
				test.Fatal("real TLS/SQLite bootstrap failed")
			}
			defer flow.Close()
			selection := NativeSelection{AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: "selected-c0-profile", SelectionRevision: 7}
			prepared, err := flow.PrepareCodex(test.Context(), selection)
			if err != nil {
				test.Fatal("real Codex client Prepare failed")
			}
			defer prepared.Destroy()
			oracle, _, _, local := clientCodexFixture(test)
			if !reflect.DeepEqual(prepared.CopyDeclaration(), oracle.Declaration) {
				test.Fatal("complete typed declaration changed over real TLS")
			}
			if status, err := flow.StatusCodex(test.Context()); err != nil || status.State != NativeNotIssued {
				test.Fatal("safe pre-authorization Status failed")
			}
			approval, err := NewCodexNativeApproval(9, time.UnixMilli(prepared.CopyBinding().PrepareExpiresAtUnixMs).Add(-time.Second), true, true)
			if err != nil {
				test.Fatal("explicit slot/final approval rejected")
			}
			defer approval.Destroy()
			if grant, err := flow.AuthorizeCodex(test.Context(), codexconfig.ProviderPlan{}, approval); !errors.Is(err, ErrNativeUnavailable) || grant.CopyBinding() != nil || authorizes.Load() != 0 || consumes.Load() != 0 || flow.codex.authorizeAttempted {
				test.Fatal("closed production D/Profile gate attempted authorization")
			}
			if _, err := flow.Prepare(test.Context(), selection); !errors.Is(err, ErrNativeInvalid) || prepares.Load() != 1 {
				test.Fatal("flow crossed from Codex to Claude")
			}
			cloned := approval.Clone()
			approval.Destroy()
			defer cloned.Destroy()
			grant, err := flow.authorizeCodex(test.Context(), local, cloned)
			if err != nil {
				test.Fatal("qualified synthetic-projection protocol core failed Authorize")
			}
			defer grant.Destroy()
			if authorizes.Load() != 1 {
				test.Fatal("authorization count differs from one")
			}
			copyGrant := grant.Clone()
			grant.Destroy()
			defer copyGrant.Destroy()
			binding := copyGrant.CopyBinding()
			binding.CodexLocalBinding.ManagedModels[0] = "changed"
			if proto.Equal(binding, flow.codex.apply) {
				test.Fatal("trusted authorization copy aliases flow")
			}
			if _, err := flow.authorizeCodex(test.Context(), local, cloned); !errors.Is(err, ErrNativeInvalid) || authorizes.Load() != 1 {
				test.Fatal("sensitive authorization replayed")
			}
			material, err := consumeCodexProtocolFixture(test.Context(), flow)
			if err != nil || !bytes.Equal(material.CloneBytes(), []byte("synthetic-C2-client-slot-key")) {
				test.Fatal("actual single-slot client materialization failed")
			}
			defer material.Destroy()
			if _, err := consumeCodexProtocolFixture(test.Context(), flow); !errors.Is(err, ErrNativeInvalid) || consumes.Load() != 1 {
				test.Fatal("client replayed consumed material")
			}
			if status, err := flow.StatusCodex(test.Context()); err != nil || status.State != NativeConsumed {
				test.Fatal("safe consumed Status failed")
			}
			copyPrepared := prepared.Clone()
			defer copyPrepared.Destroy()
			prepared.Destroy()
			if copyPrepared.CopyBinding() == nil || !reflect.DeepEqual(copyPrepared.CopyDeclaration(), oracle.Declaration) {
				test.Fatal("Destroy invalidated independently owned preparation clone")
			}
		})
	}
}

func consumeCodexProtocolFixture(caller context.Context, flow *NativeFlow) (NativeMaterial, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return NativeMaterial{}, err
	}
	defer done()
	return flow.consumeCore(ctx, caller)
}

func TestCodexNativePreflightClosed(test *testing.T) {
	var sensitive atomic.Int32
	fixture := newNativeClientFixture(test, false, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") || strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
				sensitive.Add(1)
			}
			next.ServeHTTP(writer, request)
		})
	})
	asset := codexClientAsset(test, fixture)
	flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer)
	if err != nil {
		test.Fatal("bootstrap failed")
	}
	defer flow.Close()
	prepared, err := flow.PrepareCodex(test.Context(), NativeSelection{AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: "profile", SelectionRevision: 1})
	if err != nil {
		test.Fatal("prepare failed")
	}
	defer prepared.Destroy()
	for _, choice := range []struct{ key, final bool }{{false, false}, {true, false}, {false, true}} {
		if approval, err := NewCodexNativeApproval(1, time.Now().Add(time.Minute), choice.key, choice.final); err == nil || approval.present() {
			test.Fatal("slot/final confirmation was inferred")
		}
	}
	approval, _ := NewCodexNativeApproval(1, time.Now().Add(time.Minute), true, true)
	defer approval.Destroy()
	for _, encoded := range []string{`{}`, `{"witness":"` + strings.Repeat("01", 32) + `","profileID":"synthetic-protocol-not-D"}`, `{"declaration":{"endpoint":"https://provider.invalid/v1","model":"alpha"},"baselines":[{"role":1,"missing":{}}],"templateDigest":"` + strings.Repeat("02", 32) + `","nativeProfileSha256":"` + strings.Repeat("03", 32) + `"}`} {
		var plan codexconfig.ProviderPlan
		_ = json.Unmarshal([]byte(encoded), &plan)
		if grant, err := flow.AuthorizeCodex(test.Context(), plan, approval); !errors.Is(err, ErrNativeUnavailable) || grant.CopyBinding() != nil || sensitive.Load() != 0 {
			test.Fatal("caller JSON or32B witness created production D authority")
		}
	}
	config, _ := codexconfig.NewImage(codexconfig.Missing, nil)
	auth, _ := codexconfig.NewImage(codexconfig.NotObserved, nil)
	catalog, _ := codexconfig.NewImage(codexconfig.Missing, nil)
	defer config.Destroy()
	defer auth.Destroy()
	defer catalog.Destroy()
	images, _ := codexconfig.NewImages(config, auth, catalog)
	defer images.Destroy()
	ownership, _ := codexconfig.NewOwnership(codexconfig.Absent, codexconfig.Absent, nil)
	defer ownership.Destroy()
	facts, _ := codexconfig.NewAuthFacts(codexconfig.Keyring, codexconfig.FoundCredentialLogin, codexconfig.LoginNotObserved)
	defer facts.Destroy()
	resolution, _ := codexconfig.NewResolution("synthetic-protocol-not-D", codexconfig.CompleteEffective, ownership, facts, [32]byte(bytes.Repeat([]byte{1}, 32)))
	defer resolution.Destroy()
	foreign, err := codexconfig.CheckProvider(test.Context(), images, prepared.CopyDeclaration(), resolution)
	defer foreign.Destroy()
	if !errors.Is(err, codexconfig.ErrUnsupportedNativeProfile) {
		test.Fatal("unqualified native profile unexpectedly produced a B plan")
	}
	if grant, err := flow.AuthorizeCodex(test.Context(), foreign, approval); !errors.Is(err, ErrNativeUnavailable) || grant.CopyBinding() != nil || sensitive.Load() != 0 {
		test.Fatal("unsupported B result created production admission")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if result, err := flow.AuthorizeCodex(ctx, codexconfig.ProviderPlan{}, approval); err == nil || result.CopyBinding() != nil {
		test.Fatal("canceled admission returned authorization")
	}
	if result, err := flow.ConsumeCodex(test.Context()); !errors.Is(err, ErrNativeInvalid) || len(result.CloneBytes()) != 0 {
		test.Fatal("unadmitted public flow consumed material")
	}
	if flow.codexAdmission != nil || flow.codex.authorizeAttempted || flow.codex.consumeAttempted || sensitive.Load() != 0 {
		test.Fatal("synthetic/caller inputs created a production admission source")
	}
}
