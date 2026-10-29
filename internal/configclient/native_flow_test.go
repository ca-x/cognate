package configclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"google.golang.org/protobuf/proto"

	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/nativeconsumer"
	"github.com/ca-x/cognate/internal/server/state"
)

type nativeClientFixture struct {
	server        *httptest.Server
	store         *state.Store
	auth          *auth.Service
	domain        *configuration.Service
	service       *nativeconsumer.Service
	client        *NativeClient
	owner, bearer string
}

func newNativeClientFixture(test *testing.T, h2 bool, filter func(http.Handler) http.Handler) *nativeClientFixture {
	test.Helper()
	master := bytes.Repeat([]byte{91}, 32)
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "native.db"), master)
	if err != nil {
		test.Fatal(err)
	}
	ownerAuth, err := auth.NewService(store, master)
	if err != nil {
		test.Fatal(err)
	}
	token, owner, err := ownerAuth.LoginOIDC(test.Context(), "https://synthetic.identity", "client-owner", "", "")
	if err != nil {
		test.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "https://" + server.Listener.Addr().String()
	service, err := nativeconsumer.New(store, ownerAuth, master, origin)
	clear(master)
	if err != nil {
		test.Fatal(err)
	}
	handler := http.Handler(httpapi.NewWithOptions(ownerAuth, httpapi.Options{PublicOrigin: origin, NativeConfiguration: service}))
	if filter != nil {
		handler = filter(handler)
	}
	server.Config.Handler = handler
	server.EnableHTTP2 = h2
	server.StartTLS()
	if err := service.Start(test.Context()); err != nil {
		test.Fatal(err)
	}
	settings := server.Client().Transport.(*http.Transport).Clone()
	settings.ForceAttemptHTTP2 = h2
	client, err := NewNativeClient(origin, settings)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { client.Close(); service.Close(); server.Close(); _ = store.Close() })
	return &nativeClientFixture{server: server, store: store, auth: ownerAuth, domain: configuration.New(store), service: service, client: client, owner: owner, bearer: token.Reveal()}
}

func TestNativeClientRealBootstrap(test *testing.T) {
	fixture := newNativeClientFixture(test, false, nil)
	flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer)
	if err != nil || flow == nil {
		test.Fatalf("real TLS/SQLite bootstrap failed: %v", err)
	}
}

const nativeTestSecret = "synthetic-A-secret-selected-2718281828"

func (fixture *nativeClientFixture) asset(test *testing.T, mode configuration.AuthMode) configuration.EditView {
	test.Helper()
	var mutations []configuration.SecretMutation
	if mode != configuration.NativeLogin {
		slot := configuration.APIKeySlot
		if mode == configuration.AuthToken {
			slot = configuration.AuthTokenSlot
		}
		mutations = []configuration.SecretMutation{{Slot: slot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte(nativeTestSecret))}}
	}
	asset, err := fixture.domain.Create(test.Context(), fixture.owner, configuration.Draft{Name: "synthetic client", Provider: configuration.ClaudeProvider{AuthMode: mode}}, mutations)
	if err != nil {
		test.Fatal(err)
	}
	return asset
}

func (fixture *nativeClientFixture) prepare(test *testing.T, mode configuration.AuthMode) (*NativeFlow, NativePreparation) {
	test.Helper()
	asset := fixture.asset(test, mode)
	flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	prepared, err := flow.Prepare(test.Context(), NativeSelection{AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: "selected-profile", SelectionRevision: 3})
	if err != nil {
		test.Fatal(err)
	}
	return flow, prepared
}

func nativeTestApproval(prepared NativePreparation) NativeApproval {
	return NativeApproval{PreparationID: prepared.PreparationID, OperationID: prepared.OperationID, Commitment: sha256.Sum256([]byte("synthetic immutable B plan commitment")), IntentExpiresAt: prepared.PreparationExpiresAt.Add(-time.Second), Slots: prepared.RequiredSlots, ConfirmationRevision: 4}
}

func TestNativeClientRealSixRPC(test *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, mode := range []configuration.AuthMode{configuration.APIKey, configuration.AuthToken, configuration.NativeLogin} {
			test.Run(string(mode)+map[bool]string{true: "-h2", false: "-h1"}[h2], func(test *testing.T) {
				var mutex sync.Mutex
				calls := make(map[string]int)
				connections := make(map[string]bool)
				protocol := 0
				credentials := make(map[string]string)
				fixture := newNativeClientFixture(test, h2, func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
						mutex.Lock()
						calls[request.URL.Path]++
						credentials[request.URL.Path] = request.Header.Get("Authorization")
						protocol = request.ProtoMajor
						connections[request.RemoteAddr] = true
						mutex.Unlock()
						next.ServeHTTP(writer, request)
					})
				})
				flow, prepared := fixture.prepare(test, mode)
				if prepared.PreparedAt.IsZero() || prepared.PreparationExpiresAt.After(prepared.NativeSessionExpiresAt) || prepared.AssetRevision != 1 || prepared.Adapter.DefinitionDigest != nativeClaudeDigest || len(prepared.Patch.Operations) != 7 {
					test.Fatal("unsafe/incomplete preparation metadata")
				}
				prepared.Adapter.Manifest.Capabilities[0] = "mutated"
				if flow.safePreparation().Adapter.Manifest.Capabilities[0] != agentconfig.ClaudeProviderEnvV1 {
					test.Fatal("pin view aliases flow")
				}
				required, err := flow.Authorize(test.Context(), nativeTestApproval(prepared))
				if err != nil {
					test.Fatal(err)
				}
				if required != (mode != configuration.NativeLogin) {
					test.Fatal("native login fabricated grant")
				}
				if required {
					material, err := flow.Consume(test.Context())
					if err != nil {
						test.Fatal(err)
					}
					owned := material.CloneBytes()
					if string(owned) != nativeTestSecret {
						test.Fatal("selected slot missing")
					}
					clear(owned)
					material.Destroy()
					if len(material.CloneBytes()) != 0 {
						test.Fatal("Destroy retained material")
					}
					if _, err := flow.Consume(test.Context()); !errors.Is(err, ErrNativeInvalid) {
						test.Fatal("consume replay allowed")
					}
				} else {
					if _, err := flow.Consume(test.Context()); !errors.Is(err, ErrNativeInvalid) {
						test.Fatal("login consumed fake material")
					}
				}
				status, err := flow.Status(test.Context())
				if err != nil {
					test.Fatal(err)
				}
				if required && status.State != NativeConsumed || !required && status.State != NativeNotIssued {
					test.Fatal("wrong durable status")
				}
				mutex.Lock()
				defer mutex.Unlock()
				if protocol != map[bool]int{true: 2, false: 1}[h2] {
					test.Fatal("did not exercise intended actual HTTP protocol")
				}
				if len(connections) != 1 {
					test.Fatal("sequential RPCs did not retain a single healthy TLS connection")
				}
				for path, count := range calls {
					if count != 1 {
						test.Fatal("RPC retried")
					}
					bootstrap := strings.HasSuffix(path, "BeginNativeConsumer") || strings.HasSuffix(path, "BindNativeConsumer")
					if bootstrap && credentials[path] != "Bearer "+fixture.bearer || !bootstrap && !strings.HasPrefix(credentials[path], "Bearer cn1.") {
						test.Fatal("owner/native bearer boundary violated")
					}
				}
				if len(calls) != map[bool]int{true: 6, false: 5}[required] {
					test.Fatal("did not exercise actual six RPC")
				}
			})
		}
	}
}

func TestNativeClientExplicitConsentAndRevision(test *testing.T) {
	var authorizeCalls, consumeCalls atomic.Int64
	fixture := newNativeClientFixture(test, false, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if strings.HasSuffix(request.URL.Path, "AuthorizeNativeMaterialization") {
				authorizeCalls.Add(1)
			}
			if strings.HasSuffix(request.URL.Path, "ConsumeNativeMaterialization") {
				consumeCalls.Add(1)
			}
			next.ServeHTTP(writer, request)
		})
	})
	flow, prepared := fixture.prepare(test, configuration.APIKey)
	for _, mutate := range []func(*NativeApproval){
		func(approval *NativeApproval) { approval.ConfirmationRevision = 0 },
		func(approval *NativeApproval) { approval.Slots = nil },
		func(approval *NativeApproval) {
			approval.Slots = []agentconfig.ClaudeSecretSlot{agentconfig.ClaudeAuthTokenSecret}
		},
		func(approval *NativeApproval) { approval.OperationID[0] ^= 1 },
		func(approval *NativeApproval) { approval.PreparationID = "other" },
		func(approval *NativeApproval) { approval.Commitment = [32]byte{} },
		func(approval *NativeApproval) {
			approval.IntentExpiresAt = prepared.PreparationExpiresAt.Add(time.Second)
		},
		func(approval *NativeApproval) { approval.PolicyID = "policy"; approval.PolicyRevision = 1 },
	} {
		approval := nativeTestApproval(prepared)
		mutate(&approval)
		if _, err := flow.Authorize(test.Context(), approval); !errors.Is(err, ErrNativeInvalid) {
			test.Fatal("invalid consent accepted")
		}
		if flow.authorizeAttempted {
			test.Fatal("invalid local consent issued RPC")
		}
	}
	asset, err := fixture.domain.Edit(test.Context(), fixture.owner, prepared.AssetID)
	if authorizeCalls.Load() != 0 || consumeCalls.Load() != 0 {
		test.Fatal("invalid explicit slot/confirmation reached sensitive RPC")
	}
	if err != nil {
		test.Fatal(err)
	}
	_, err = fixture.domain.Update(test.Context(), fixture.owner, prepared.AssetID, asset.Metadata.Revision, configuration.Draft{Name: "changed", Provider: asset.Provider}, nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := flow.Authorize(test.Context(), nativeTestApproval(prepared)); !errors.Is(err, ErrNativeUnknown) {
		test.Fatal("stale asset authorized")
	}
	if _, err := flow.Consume(test.Context()); !errors.Is(err, ErrNativeInvalid) {
		test.Fatal("denied authorization consumed")
	}
	if authorizeCalls.Load() != 1 || consumeCalls.Load() != 0 {
		test.Fatal("stale revision retried or consumed")
	}
}

type nativeClientTruncate struct{ http.ResponseWriter }

func (writer nativeClientTruncate) Unwrap() http.ResponseWriter { return writer.ResponseWriter }
func (writer nativeClientTruncate) Write(data []byte) (int, error) {
	_, err := writer.ResponseWriter.Write(data[:len(data)/2])
	return len(data), err
}

func TestNativeClientLostConsumeStatusOnly(test *testing.T) {
	for _, lost := range []string{"consume", "authorize"} {
		test.Run(lost, func(test *testing.T) {
			var mutex sync.Mutex
			counts := make(map[string]int)
			fixture := newNativeClientFixture(test, false, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					mutex.Lock()
					counts[request.URL.Path]++
					mutex.Unlock()
					if lost == "consume" && strings.HasSuffix(request.URL.Path, "ConsumeNativeMaterialization") || lost == "authorize" && strings.HasSuffix(request.URL.Path, "AuthorizeNativeMaterialization") {
						next.ServeHTTP(nativeClientTruncate{writer}, request)
						return
					}
					next.ServeHTTP(writer, request)
				})
			})
			flow, prepared := fixture.prepare(test, configuration.AuthToken)
			_, err := flow.Authorize(test.Context(), nativeTestApproval(prepared))
			if lost == "authorize" {
				if !errors.Is(err, ErrNativeUnknown) {
					test.Fatal("lost authorization not unknown")
				}
				if _, err := flow.Authorize(test.Context(), nativeTestApproval(prepared)); !errors.Is(err, ErrNativeInvalid) {
					test.Fatal("authorization replay")
				}
			} else {
				if err != nil {
					test.Fatal(err)
				}
				material, err := flow.Consume(test.Context())
				if !errors.Is(err, ErrNativeUnknown) || len(material.CloneBytes()) != 0 {
					test.Fatal("partial material escaped")
				}
				if _, err := flow.Consume(test.Context()); !errors.Is(err, ErrNativeInvalid) {
					test.Fatal("consume replay")
				}
			}
			status, err := flow.Status(test.Context())
			if err != nil {
				test.Fatal(err)
			}
			expected := NativeConsumed
			if lost == "authorize" {
				expected = NativeIssued
			}
			if status.State != expected {
				test.Fatal("durable status incorrect")
			}
			mutex.Lock()
			defer mutex.Unlock()
			for _, count := range counts {
				if count != 1 {
					test.Fatal("replayed RPC after unknown")
				}
			}
		})
	}
}

type nativeCaptureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
	actual http.ResponseWriter
}

func (writer *nativeCaptureWriter) Unwrap() http.ResponseWriter { return writer.actual }
func (writer *nativeCaptureWriter) Flush()                      {}
func (writer *nativeCaptureWriter) Header() http.Header         { return writer.header }
func (writer *nativeCaptureWriter) WriteHeader(status int)      { writer.status = status }
func (writer *nativeCaptureWriter) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.status = 200
	}
	return writer.body.Write(data)
}

func nativeMutatingFilter(method string, mutate func([]byte) []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if !strings.HasSuffix(request.URL.Path, method) {
				next.ServeHTTP(writer, request)
				return
			}
			capture := &nativeCaptureWriter{header: make(http.Header), actual: writer}
			next.ServeHTTP(capture, request)
			for name, values := range capture.header {
				for _, value := range values {
					writer.Header().Add(name, value)
				}
			}
			writer.Header().Del("Content-Length")
			writer.WriteHeader(capture.status)
			data := mutate(capture.body.Bytes())
			_, _ = writer.Write(data)
			clear(data)
			clear(capture.body.Bytes())
		})
	}
}

func TestNativeClientPrepareBindingMutations(test *testing.T) {
	mutations := map[string]func(*nativev1.PrepareNativeApplyResponse){
		"owner": func(response *nativev1.PrepareNativeApplyResponse) { response.Binding.OwnerId = "other-owner" },
		"origin": func(response *nativev1.PrepareNativeApplyResponse) {
			response.Binding.ServerOrigin = "https://other.invalid"
		},
		"session":   func(response *nativev1.PrepareNativeApplyResponse) { response.Binding.NativeSessionBinding[0] ^= 1 },
		"key":       func(response *nativev1.PrepareNativeApplyResponse) { response.Binding.ConsumerKeySha256[0] ^= 1 },
		"operation": func(response *nativev1.PrepareNativeApplyResponse) { response.Binding.OperationId[0] ^= 1 },
		"asset": func(response *nativev1.PrepareNativeApplyResponse) {
			response.Binding.AssetId = strings.Repeat("A", 26)
		},
		"revision":  func(response *nativev1.PrepareNativeApplyResponse) { response.Binding.AssetRevision++ },
		"profile":   func(response *nativev1.PrepareNativeApplyResponse) { response.Binding.ProfileId = "other-profile" },
		"selection": func(response *nativev1.PrepareNativeApplyResponse) { response.Binding.SelectionRevision++ },
		"pin": func(response *nativev1.PrepareNativeApplyResponse) {
			response.Binding.Adapter.DefinitionDigest = strings.Repeat("0", 64)
		},
		"capability": func(response *nativev1.PrepareNativeApplyResponse) {
			response.Binding.Adapter.RequiredCapabilities = []string{"claude.provider-env.v1"}
		},
		"expiry": func(response *nativev1.PrepareNativeApplyResponse) {
			response.Binding.PrepareExpiresAtUnixMs = time.Now().Add(-time.Second).UnixMilli()
		},
		"future": func(response *nativev1.PrepareNativeApplyResponse) {
			response.Binding.PreparedAtUnixMs = time.Now().Add(time.Hour).UnixMilli()
		},
		"patch": func(response *nativev1.PrepareNativeApplyResponse) { response.Binding.PatchSha256[0] ^= 1 },
		"slot": func(response *nativev1.PrepareNativeApplyResponse) {
			response.Binding.RequiredSlots = []nativev1.SecretSlot{2}
		},
	}
	for name, mutate := range mutations {
		test.Run(name, func(test *testing.T) {
			fixture := newNativeClientFixture(test, false, nativeMutatingFilter("PrepareNativeApply", func(data []byte) []byte {
				response := new(nativev1.PrepareNativeApplyResponse)
				if proto.Unmarshal(data, response) != nil {
					test.Error("fixture decode failed")
				}
				mutate(response)
				encoded, _ := proto.Marshal(response)
				return encoded
			}))
			asset := fixture.asset(test, configuration.APIKey)
			flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer)
			if err != nil {
				test.Fatal(err)
			}
			prepared, err := flow.Prepare(test.Context(), NativeSelection{AssetID: asset.Metadata.ID, AssetRevision: 1, ProfileID: "profile", SelectionRevision: 1})
			if !errors.Is(err, ErrNativeInvalid) || prepared.PreparationID != "" {
				test.Fatal("untrusted preparation escaped")
			}
		})
	}
}

func TestNativeClientCallerAndCloseDrain(test *testing.T) {
	for _, closeMode := range []string{"caller", "flow", "client", "caller-h2", "flow-h2", "client-h2"} {
		test.Run(closeMode, func(test *testing.T) {
			mode, h2 := strings.CutSuffix(closeMode, "-h2")
			entered, drained := make(chan struct{}), make(chan struct{})
			fixture := newNativeClientFixture(test, h2, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if strings.HasSuffix(request.URL.Path, "PrepareNativeApply") {
						_, _ = io.Copy(io.Discard, request.Body)
						close(entered)
						<-request.Context().Done()
						close(drained)
						return
					}
					next.ServeHTTP(writer, request)
				})
			})
			asset := fixture.asset(test, configuration.APIKey)
			flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer)
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := flow.Prepare(ctx, NativeSelection{AssetID: asset.Metadata.ID, AssetRevision: 1, ProfileID: "profile", SelectionRevision: 1})
				done <- err
			}()
			<-entered
			switch mode {
			case "caller":
				cancel()
			case "flow":
				flow.Close()
			case "client":
				fixture.client.Close()
			}
			select {
			case err := <-done:
				if !errors.Is(err, ErrNativeCanceled) {
					test.Fatal("cancellation leaked raw error")
				}
			case <-time.After(3 * time.Second):
				test.Fatal("Close did not join actual operation")
			}
			select {
			case <-drained:
			case <-time.After(3 * time.Second):
				test.Fatal("network request not canceled/drained")
			}
		})
	}
}

func TestNativeClientBootstrapResponseBindings(test *testing.T) {
	for _, scenario := range []string{"owner", "origin", "challenge-expiry", "session-expiry", "bind-binding", "bind-expiry"} {
		test.Run(scenario, func(test *testing.T) {
			method := "BeginNativeConsumer"
			if strings.HasPrefix(scenario, "bind-") {
				method = "BindNativeConsumer"
			}
			fixture := newNativeClientFixture(test, false, nativeMutatingFilter(method, func(data []byte) []byte {
				if method == "BeginNativeConsumer" {
					response := new(nativev1.BeginNativeConsumerResponse)
					if proto.Unmarshal(data, response) != nil {
						test.Error("fixture response decode")
						return nil
					}
					switch scenario {
					case "owner":
						response.OwnerId = "other-owner"
					case "origin":
						response.ServerOrigin = "https://other.invalid"
					case "challenge-expiry":
						response.ChallengeExpiresAtUnixMs = time.Now().Add(2 * time.Minute).UnixMilli()
					case "session-expiry":
						response.NativeSessionExpiresAtUnixMs = time.Now().Add(time.Hour).UnixMilli()
					}
					encoded, _ := proto.Marshal(response)
					return encoded
				}
				response := new(nativev1.BindNativeConsumerResponse)
				if proto.Unmarshal(data, response) != nil {
					test.Error("fixture response decode")
					return nil
				}
				defer nativeClear(response)
				if scenario == "bind-binding" {
					response.NativeSessionBinding[0] ^= 1
				} else {
					response.ExpiresAtUnixMs--
				}
				encoded, _ := proto.Marshal(response)
				return encoded
			}))
			if flow, err := fixture.client.OpenConsumer(test.Context(), fixture.owner, fixture.bearer); !errors.Is(err, ErrNativeInvalid) || flow != nil {
				test.Fatal("unbound bootstrap accepted")
			}
		})
	}
}

func TestNativeClientAuthorizeConsumeStatusResponseBindings(test *testing.T) {
	for _, scenario := range []string{"authorize-commitment", "authorize-preparation", "authorize-expiry", "authorize-grant-shape", "consume-commitment", "consume-grant", "consume-slot", "consume-time", "consume-extra-slot", "status-grant", "status-expiry", "status-consumed-time"} {
		test.Run(scenario, func(test *testing.T) {
			method := "AuthorizeNativeMaterialization"
			if strings.HasPrefix(scenario, "consume-") {
				method = "ConsumeNativeMaterialization"
			}
			if strings.HasPrefix(scenario, "status-") {
				method = "GetNativeMaterializationStatus"
			}
			fixture := newNativeClientFixture(test, false, nativeMutatingFilter(method, func(data []byte) []byte {
				var message proto.Message
				switch method {
				case "AuthorizeNativeMaterialization":
					response := new(nativev1.AuthorizeNativeMaterializationResponse)
					if proto.Unmarshal(data, response) != nil {
						test.Error("fixture decode")
						return nil
					}
					defer nativeClear(response)
					message = response
					switch scenario {
					case "authorize-commitment":
						response.Binding.IntentCommitment[0] ^= 1
					case "authorize-preparation":
						response.Binding.Preparation.SelectionRevision++
					case "authorize-expiry":
						response.ExpiresAtUnixMs = time.Now().Add(2 * time.Minute).UnixMilli()
					case "authorize-grant-shape":
						response.GrantToken = nil
					}
				case "ConsumeNativeMaterialization":
					response := new(nativev1.ConsumeNativeMaterializationResponse)
					if proto.Unmarshal(data, response) != nil {
						test.Error("fixture decode")
						return nil
					}
					defer nativeClear(response)
					message = response
					switch scenario {
					case "consume-commitment":
						response.Binding.IntentCommitment[0] ^= 1
					case "consume-grant":
						response.GrantId = "other-grant"
					case "consume-slot":
						response.SlotValues[0].Slot = 2
					case "consume-time":
						response.ConsumedAtUnixMs = time.Now().Add(time.Hour).UnixMilli()
					case "consume-extra-slot":
						response.SlotValues = append(response.SlotValues, proto.Clone(response.SlotValues[0]).(*nativev1.SlotValue))
					}
				case "GetNativeMaterializationStatus":
					response := new(nativev1.GetNativeMaterializationStatusResponse)
					if proto.Unmarshal(data, response) != nil {
						test.Error("fixture decode")
						return nil
					}
					message = response
					switch scenario {
					case "status-grant":
						response.GrantId = "other-grant"
					case "status-expiry":
						response.ExpiresAtUnixMs++
					case "status-consumed-time":
						response.ConsumedAtUnixMs = 1
					}
				}
				encoded, _ := proto.Marshal(message)
				return encoded
			}))
			flow, prepared := fixture.prepare(test, configuration.APIKey)
			_, err := flow.Authorize(test.Context(), nativeTestApproval(prepared))
			if method == "AuthorizeNativeMaterialization" {
				if !errors.Is(err, ErrNativeUnknown) {
					test.Fatal("unbound grant accepted")
				}
				return
			}
			if err != nil {
				test.Fatal(err)
			}
			material, err := flow.Consume(test.Context())
			defer material.Destroy()
			if method == "ConsumeNativeMaterialization" {
				if !errors.Is(err, ErrNativeUnknown) || len(material.CloneBytes()) != 0 {
					test.Fatal("unbound plaintext escaped")
				}
				return
			}
			if err != nil {
				test.Fatal(err)
			}
			if _, err := flow.Status(test.Context()); !errors.Is(err, ErrNativeInvalid) {
				test.Fatal("unbound durable status accepted")
			}
		})
	}
}

func TestNativeClientWrongPurposeProofRejected(test *testing.T) {
	fixture := newNativeClientFixture(test, false, nil)
	flow, prepared := fixture.prepare(test, configuration.APIKey)
	request := &nativev1.GetNativeMaterializationStatusRequest{PrepareId: prepared.PreparationID, OperationId: prepared.OperationID[:], ClientNonce: nativeNonce()}
	prepare := &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: prepared.OwnerID, AssetId: prepared.AssetID, ExpectedAssetRevision: 1, AgentId: "claude", SchemaVersion: 1, Adapter: nativeAdapterBinding(), Target: 1, Scope: 1, ProfileId: prepared.ProfileID, SelectionRevision: prepared.SelectionRevision, OperationId: prepared.OperationID[:], ClientNonce: request.ClientNonce}
	var err error
	request.ProofSignature, err = flow.sign(PrepareTranscript(flow.consumer, prepare))
	if err != nil {
		test.Fatal(err)
	}
	ctx, done, err := fixture.client.operation(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer done()
	if err := flow.call(ctx, "GetNativeMaterializationStatus", request, new(nativev1.GetNativeMaterializationStatusResponse)); !errors.Is(err, ErrNativeDenied) {
		test.Fatal("cross-purpose signature accepted")
	}
}

type nativeWaiterContext struct {
	context.Context
	started chan struct{}
	once    sync.Once
}

func (caller *nativeWaiterContext) Err() error {
	err := caller.Context.Err()
	caller.once.Do(func() { close(caller.started) })
	return err
}

type nativeHeldCall struct {
	fixture                   *nativeClientFixture
	flow                      *NativeFlow
	prepared                  NativePreparation
	entered, release, drained chan struct{}
	releaseOnce               sync.Once
	statusCalls               atomic.Int64
	result                    chan error
}

func newNativeHeldCall(test *testing.T, h2 bool, stage string) *nativeHeldCall {
	test.Helper()
	held := &nativeHeldCall{entered: make(chan struct{}), release: make(chan struct{}), drained: make(chan struct{}), result: make(chan error, 1)}
	held.fixture = newNativeClientFixture(test, h2, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if strings.HasSuffix(request.URL.Path, "GetNativeMaterializationStatus") {
				held.statusCalls.Add(1)
			}
			if !strings.HasSuffix(request.URL.Path, stage) {
				next.ServeHTTP(writer, request)
				return
			}
			data, err := io.ReadAll(request.Body)
			_ = request.Body.Close()
			defer clear(data)
			if err != nil {
				test.Error("actual holder request body failed")
				return
			}
			request.Body = io.NopCloser(bytes.NewReader(data))
			defer close(held.drained)
			close(held.entered)
			select {
			case <-held.release:
				next.ServeHTTP(writer, request)
			case <-request.Context().Done():
			}
		})
	})
	if stage == "PrepareNativeApply" {
		asset := held.fixture.asset(test, configuration.APIKey)
		var err error
		held.flow, err = held.fixture.client.OpenConsumer(test.Context(), held.fixture.owner, held.fixture.bearer)
		if err != nil {
			test.Fatal(err)
		}
		go func() {
			prepared, err := held.flow.Prepare(test.Context(), NativeSelection{AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: "profile", SelectionRevision: 1})
			held.prepared = prepared
			held.result <- err
		}()
	} else {
		held.flow, held.prepared = held.fixture.prepare(test, configuration.APIKey)
		go func() {
			_, err := held.flow.Authorize(test.Context(), nativeTestApproval(held.prepared))
			held.result <- err
		}()
	}
	<-held.entered
	return held
}

func (held *nativeHeldCall) unblock() { held.releaseOnce.Do(func() { close(held.release) }) }

func TestNativeClientQueuedCancellationIsolatesHolder(test *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, stage := range []string{"PrepareNativeApply", "AuthorizeNativeMaterialization"} {
			for _, mode := range []string{"already-canceled", "canceled-while-waiting"} {
				test.Run(map[bool]string{false: "h1", true: "h2"}[h2]+"/"+stage+"/"+mode, func(test *testing.T) {
					held := newNativeHeldCall(test, h2, stage)
					defer held.unblock()
					ctx, cancel := context.WithCancel(test.Context())
					defer cancel()
					caller := &nativeWaiterContext{Context: ctx, started: make(chan struct{})}
					if mode == "already-canceled" {
						cancel()
					}
					waiter := make(chan error, 1)
					go func() { _, err := held.flow.Status(caller); waiter <- err }()
					<-caller.started
					returned := false
					if mode == "canceled-while-waiting" {
						select {
						case <-waiter:
							returned = true
							test.Error("uncanceled waiter bypassed in-flight serial holder")
						case <-time.After(50 * time.Millisecond):
						}
						cancel()
					}
					if !returned {
						select {
						case err := <-waiter:
							returned = true
							if !errors.Is(err, ErrNativeCanceled) {
								test.Error("queued caller returned wrong fixed cancellation category")
							}
						case <-time.After(time.Second):
							test.Error("queued cancellation waited for another actual TLS operation")
						}
					}
					select {
					case <-held.drained:
						test.Error("queued cancellation canceled the holder request")
					default:
					}
					if held.statusCalls.Load() != 0 {
						test.Error("canceled queued caller issued a Status RPC")
					}
					held.unblock()
					var holderErr error
					select {
					case holderErr = <-held.result:
					case <-time.After(3 * time.Second):
						test.Error("holder failed to complete after release")
						held.flow.Close()
						holderErr = <-held.result
					}
					if !returned {
						if err := <-waiter; !errors.Is(err, ErrNativeCanceled) {
							test.Error("late canceled waiter returned wrong category")
						}
					}
					select {
					case <-held.drained:
					case <-time.After(3 * time.Second):
						test.Error("real holder did not drain")
					}
					if holderErr != nil {
						test.Error("queued cancellation disrupted successful real holder")
						return
					}
					if stage == "PrepareNativeApply" {
						if _, err := held.flow.Authorize(test.Context(), nativeTestApproval(held.prepared)); err != nil {
							test.Error("holder preparation could not authorize")
							return
						}
					}
					material, err := held.flow.Consume(test.Context())
					defer material.Destroy()
					if err != nil || string(material.CloneBytes()) != nativeTestSecret {
						test.Error("holder did not retain exact real materialization")
						return
					}
					status, err := held.flow.Status(test.Context())
					if err != nil || status.State != NativeConsumed {
						test.Error("holder final durable state changed")
					}
				})
			}
		}
	}
}

func TestNativeClientQueuedCloseDrainsAllCallers(test *testing.T) {
	for _, h2 := range []bool{false, true} {
		test.Run(map[bool]string{false: "h1", true: "h2"}[h2], func(test *testing.T) {
			held := newNativeHeldCall(test, h2, "AuthorizeNativeMaterialization")
			defer held.unblock()
			const count = 8
			waiters := make(chan error, count)
			for range count {
				caller := &nativeWaiterContext{Context: test.Context(), started: make(chan struct{})}
				go func() { _, err := held.flow.Status(caller); waiters <- err }()
				<-caller.started
			}
			closed := make(chan struct{}, 4)
			for range 2 {
				go func() { held.flow.Close(); closed <- struct{}{} }()
				go func() { held.fixture.client.Close(); closed <- struct{}{} }()
			}
			for range 4 {
				select {
				case <-closed:
				case <-time.After(3 * time.Second):
					test.Error("concurrent Close failed to join admitted work")
					held.unblock()
					held.flow.Close()
				}
			}
			if err := <-held.result; !errors.Is(err, ErrNativeUnknown) {
				test.Error("closed active authorization lost unknown category")
			}
			for range count {
				if err := <-waiters; !errors.Is(err, ErrNativeCanceled) {
					test.Error("Close failed to cancel queued caller")
				}
			}
			select {
			case <-held.drained:
			case <-time.After(3 * time.Second):
				test.Error("Close returned without actual server cancellation/drain")
			}
			if held.statusCalls.Load() != 0 {
				test.Error("Close let queued callers issue network operations")
			}
			if _, err := held.flow.Status(test.Context()); !errors.Is(err, ErrNativeCanceled) {
				test.Error("closed flow readmitted work")
			}
		})
	}
}
