package grokclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

type relayFixture struct {
	app           *appFixture
	server        *httptest.Server
	settings      *http.Transport
	mutex         sync.Mutex
	counts        map[string]int
	lose          string
	gate          string
	arrived       chan struct{}
	release       chan struct{}
	drained       chan struct{}
	once          sync.Once
	bodiesWithKey int
}

func newRelay(test *testing.T) *relayFixture {
	test.Helper()
	relay := &relayFixture{app: newAppFixture(test), counts: map[string]int{}}
	relay.server = httptest.NewTLSServer(http.HandlerFunc(relay.handle))
	test.Cleanup(relay.server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(relay.server.Certificate())
	relay.settings = &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}}
	return relay
}
func (relay *relayFixture) handle(writer http.ResponseWriter, request *http.Request) {
	data, err := io.ReadAll(io.LimitReader(request.Body, (128<<10)+1))
	if err != nil || len(data) > 128<<10 {
		writer.WriteHeader(400)
		return
	}
	defer clear(data)
	relay.mutex.Lock()
	relay.counts[request.URL.Path]++
	if bytes.Contains(data, []byte("synthetic-G1-key")) {
		relay.bodiesWithKey++
	}
	gate, arrived, release, drained := relay.gate, relay.arrived, relay.release, relay.drained
	lose := relay.lose
	relay.mutex.Unlock()
	if gate == request.URL.Path {
		relay.once.Do(func() { close(arrived) })
		defer close(drained)
		select {
		case <-release:
		case <-request.Context().Done():
			return
		}
	}
	forward, err := http.NewRequestWithContext(request.Context(), http.MethodPost, relay.app.origin+request.URL.Path, bytes.NewReader(data))
	if err != nil {
		writer.WriteHeader(500)
		return
	}
	forward.Header = request.Header.Clone()
	response, err := relay.app.http.Do(forward)
	if err != nil {
		writer.WriteHeader(502)
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		writer.WriteHeader(502)
		return
	}
	defer clear(body)
	for name, values := range response.Header {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	if lose == request.URL.Path && response.StatusCode == 200 {
		writer.Header().Set("Content-Length", "1000000")
		writer.WriteHeader(200)
		_, _ = writer.Write(body[:len(body)/2])
		return
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = writer.Write(body)
}
func (relay *relayFixture) client(test *testing.T) *Client {
	test.Helper()
	client, err := New(relay.server.URL, relay.app.session, relay.settings)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(client.Close)
	return client
}
func (relay *relayFixture) count(path string) int {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	return relay.counts[path]
}
func (relay *relayFixture) hold(path string) {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	relay.gate = path
	relay.arrived = make(chan struct{})
	relay.release = make(chan struct{})
	relay.drained = make(chan struct{})
}
func await(test *testing.T, signal <-chan struct{}) {
	test.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		test.Fatal("bounded fixture event missing")
	}
}

func TestCommittedLossNeverReplays(test *testing.T) {
	for _, action := range []Action{Create, Update, Copy, Delete, ReplaceOrder} {
		test.Run(actionLabel(action), func(test *testing.T) {
			relay := newRelay(test)
			client := relay.client(test)
			source := submitChange(test, client, Change{Action: Create}, fixtureInput(test, customJSON, ReplaceKey))
			other := submitChange(test, client, Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation))
			change := Change{Action: action, ID: source.Metadata.ID, ExpectedRevision: 1}
			var input *Input
			switch action {
			case Create:
				change = Change{Action: Create}
				input = fixtureInput(test, customJSON, ReplaceKey)
			case Update:
				input = fixtureInput(test, customJSON, ClearKey)
			case Copy:
				change.Name = "Unknown copy"
			case ReplaceOrder:
				before, err := client.GetOrder(test.Context())
				if err != nil {
					test.Fatal(err)
				}
				change = Change{Action: ReplaceOrder, ExpectedOrderRevision: before.Revision, OrderIDs: []string{other.Metadata.ID, source.Metadata.ID}}
			}
			operation, err := client.Prepare(test.Context(), change, input)
			if err != nil {
				test.Fatal(err)
			}
			defer operation.Close()
			second, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation))
			if err != nil {
				test.Fatal(err)
			}
			defer second.Close()
			method := map[Action]string{Create: "CreateAsset", Update: "UpdateAsset", Copy: "CopyAsset", Delete: "DeleteAsset", ReplaceOrder: "ReplaceAssetOrder"}[action]
			path := servicePrefix + method
			beforeHTTP := relay.count(path)
			relay.mutex.Lock()
			relay.lose = path
			relay.mutex.Unlock()
			business := 0
			business++
			result, err := operation.Submit(test.Context())
			if !errors.Is(err, ErrUnknown) || !reflectZeroResult(result) || business != 1 || relay.count(path) != beforeHTTP+1 {
				test.Fatal("committed loss was not terminal Unknown")
			}
			if _, err := operation.Submit(test.Context()); !errors.Is(err, ErrClosed) {
				test.Fatal("same operation replayed")
			}
			if _, err := second.Submit(test.Context()); !errors.Is(err, ErrUnknown) {
				test.Fatal("prepared second write bypassed Unknown block")
			}
			if _, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation)); !errors.Is(err, ErrUnknown) {
				test.Fatal("fresh write bypassed Unknown block")
			}
			if relay.count(path) != beforeHTTP+1 {
				test.Fatal("mutation HTTP repeated")
			}
			assets, err := client.List(test.Context())
			if err != nil {
				test.Fatal("Unknown blocked read-only reconciliation", err)
			}
			switch action {
			case Create, Copy:
				if len(assets) != 3 {
					test.Fatal("postcommit lost create/copy was mistaken for rollback")
				}
				for _, asset := range assets {
					if asset.ID != source.Metadata.ID && asset.ID != other.Metadata.ID {
						expected := wireDraft()
						key := " synthetic-G1-key\t"
						if action == Copy {
							expected.Name = "Unknown copy"
							key = ""
						}
						assertStored(test, relay.app, asset.ID, 1, key, expected)
					}
				}
			case Delete:
				if len(assets) != 1 || assets[0].ID != other.Metadata.ID {
					test.Fatal("postcommit delete not durable")
				}
			case Update:
				metadata, err := relay.app.peer.ConfigurationAsset(test.Context(), relay.app.owner, source.Metadata.ID)
				if err != nil || metadata.Revision != 2 {
					test.Fatal("postcommit update not exactly revision2")
				}
				assertStored(test, relay.app, source.Metadata.ID, 2, "", wireDraft())
			case ReplaceOrder:
				order, err := relay.app.peer.ConfigurationAssetOrder(test.Context(), relay.app.owner, "grokbuild", state.ConfigurationKindProvider)
				if err != nil || order.Revision != change.ExpectedOrderRevision+1 || order.Assets[0].ID != other.Metadata.ID || order.Assets[1].ID != source.Metadata.ID {
					test.Fatal("postcommit order not durable")
				}
			}
			test.Logf("ATTEMPTS action=%s initial-submit=%d mutation-http=%d asset-durable-delta=1 blocked-replays=true", actionLabel(action), business, relay.count(path)-beforeHTTP)
		})
	}
}
func reflectZeroResult(value Result) bool {
	return value.Metadata == nil && value.Order == nil && !value.Deleted
}

func TestPrepareNoSecretUploadAndCAS(test *testing.T) {
	relay := newRelay(test)
	client := relay.client(test)
	input := fixtureInput(test, customJSON, ReplaceKey)
	operation, err := client.Prepare(test.Context(), Change{Action: Create}, input)
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	relay.mutex.Lock()
	uploads := relay.bodiesWithKey
	relay.mutex.Unlock()
	if uploads != 0 || relay.count(servicePrefix+"CreateAsset") != 0 {
		test.Fatal("Prepare uploaded replacement or mutated")
	}
	result, err := operation.Submit(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	id := result.Metadata.ID
	stale, err := client.Prepare(test.Context(), Change{Action: Update, ID: id, ExpectedRevision: 1}, fixtureInput(test, customJSON, ClearKey))
	if err != nil {
		test.Fatal(err)
	}
	defer stale.Close()
	peerDraft := configuration.Draft{Name: "peer winner", GrokBuildProvider: &configuration.GrokBuildProvider{Mode: "native-login"}}
	if _, err := configuration.New(relay.app.peer).Update(test.Context(), relay.app.owner, id, 1, peerDraft, []configuration.SecretMutation{{Slot: configuration.GrokBuildAPIKeySlot, Action: configuration.Clear}}); err != nil {
		test.Fatal("real second Store update", err)
	}
	before := relay.count(servicePrefix + "UpdateAsset")
	if result, err := stale.Submit(test.Context()); !errors.Is(err, ErrConflict) || !reflectZeroResult(result) {
		test.Fatal("prepared CAS was rebased or bypassed")
	}
	metadata, err := relay.app.peer.ConfigurationAsset(test.Context(), relay.app.owner, id)
	if err != nil || metadata.Revision != 2 || metadata.Name != "peer winner" || relay.count(servicePrefix+"UpdateAsset") != before+1 {
		test.Fatal("stale mutation changed actual winner")
	}
}

func TestContextIsolationBusyAndClose(test *testing.T) {
	relay := newRelay(test)
	client := relay.client(test)
	var traced atomic.Int32
	ctx := httptrace.WithClientTrace(test.Context(), &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { traced.Add(1) }})
	if _, err := client.GetOrder(ctx); err != nil {
		test.Fatal(err)
	}
	if traced.Load() != 0 {
		test.Fatal("caller httptrace/context values reached privileged transport")
	}
	relay.hold(authProcedure)
	done := make(chan error, 1)
	go func() { _, err := client.List(test.Context()); done <- err }()
	await(test, relay.arrived)
	if _, err := client.GetOrder(test.Context()); !errors.Is(err, ErrBusy) {
		test.Fatal("concurrent use queued or admitted")
	}
	closed := make(chan struct{})
	go func() { client.Close(); close(closed) }()
	await(test, closed)
	await(test, relay.drained)
	if err := <-done; !errors.Is(err, context.Canceled) {
		test.Fatal("Close did not cancel/join actual HTTP")
	}
	if _, err := client.List(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("closed client admitted")
	}
}

func TestRevocationAndDeadlineAdmission(test *testing.T) {
	test.Run("logout", func(test *testing.T) {
		fixture := newAppFixture(test)
		client := fixtureClient(test, fixture)
		operation, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, customJSON, ReplaceKey))
		if err != nil {
			test.Fatal(err)
		}
		defer operation.Close()
		auth := authv1connect.NewAuthServiceClient(fixture.http, fixture.origin)
		if _, err := auth.Logout(test.Context(), fixtureRequest(fixture, &authv1.LogoutRequest{})); err != nil {
			test.Fatal("actual logout")
		}
		if _, err := operation.Submit(test.Context()); !errors.Is(err, ErrDenied) {
			test.Fatal("revoked session admitted")
		}
		if assets, err := fixture.peer.ListConfigurationAssets(test.Context(), fixture.owner); err != nil || len(assets) != 0 {
			test.Fatal("revoked session wrote")
		}
	})
	test.Run("slow-auth-crosses-fixed-expiry", func(test *testing.T) {
		relay := newRelay(test)
		client := relay.client(test)
		operation, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, customJSON, ReplaceKey))
		if err != nil {
			test.Fatal(err)
		}
		defer operation.Close()
		operation.state().expires = time.Now().Add(time.Second)
		relay.hold(authProcedure)
		done := make(chan error, 1)
		go func() { _, err := operation.Submit(test.Context()); done <- err }()
		await(test, relay.arrived)
		time.Sleep(max(time.Until(operation.state().expires)+20*time.Millisecond, time.Millisecond))
		close(relay.release)
		if err := <-done; !errors.Is(err, ErrExpired) {
			test.Fatal("slow auth renewed expired review")
		}
		await(test, relay.drained)
		if relay.count(servicePrefix+"CreateAsset") != 0 {
			test.Fatal("expired review entered mutation")
		}
	})
}

func TestOwnerScopeAndPendingCapacity(test *testing.T) {
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	created := submitChange(test, client, Change{Action: Create}, fixtureInput(test, customJSON, ReplaceKey))
	auth := authv1connect.NewAuthServiceClient(fixture.http, fixture.origin)
	_, err := auth.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "grok-g1-other", Password: "synthetic-other-password"}))
	if err != nil {
		test.Fatal("second owner fixture")
	}
	login, err := auth.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "grok-g1-other", Password: "synthetic-other-password"}))
	if err != nil {
		test.Fatal(err)
	}
	other, err := New(fixture.origin, login.Msg.SessionToken, fixture.settings)
	if err != nil {
		test.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Read(test.Context(), created.Metadata.ID); !errors.Is(err, ErrNotFound) {
		test.Fatal("foreign owner read exposed asset")
	}
	if _, err := other.Prepare(test.Context(), Change{Action: Delete, ID: created.Metadata.ID, ExpectedRevision: 1}, nil); !errors.Is(err, ErrNotFound) {
		test.Fatal("foreign owner delete admitted")
	}
	var operations []*Operation
	for range 8 {
		operation, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation))
		if err != nil {
			test.Fatal(err)
		}
		operations = append(operations, operation)
	}
	if _, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation)); !errors.Is(err, ErrLimit) {
		test.Fatal("unbounded prepared state")
	}
	for _, operation := range operations {
		operation.Close()
	}
	operation, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation))
	if err != nil {
		test.Fatal("Close did not free owned pending state")
	}
	operation.Close()
	if _, err := New(fixture.origin, strings.Repeat("x", 43), fixture.settings); !errors.Is(err, ErrInvalid) {
		test.Fatal("share/non-session token accepted")
	}
}

type waitingReview struct{ arrived, finished chan struct{} }

func (writer *waitingReview) Write([]byte) (int, error) { return 0, ErrInvalid }
func (writer *waitingReview) WriteContext(ctx context.Context, _ []byte) (int, error) {
	close(writer.arrived)
	<-ctx.Done()
	close(writer.finished)
	return 0, ctx.Err()
}

func TestCloseDrainsReview(test *testing.T) {
	for _, kind := range []string{"operation", "view"} {
		test.Run(kind, func(test *testing.T) {
			fixture := newAppFixture(test)
			client := fixtureClient(test, fixture)
			created := submitChange(test, client, Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation))
			var write func(context.Context, io.Writer) error
			var closeReview func()
			if kind == "operation" {
				operation, err := client.Prepare(test.Context(), Change{Action: Delete, ID: created.Metadata.ID, ExpectedRevision: 1}, nil)
				if err != nil {
					test.Fatal(err)
				}
				write = operation.WriteSummary
				closeReview = operation.Close
			} else {
				view, err := client.Read(test.Context(), created.Metadata.ID)
				if err != nil {
					test.Fatal(err)
				}
				write = view.WriteSummary
				closeReview = view.Close
			}
			ctx, cancel := context.WithCancel(test.Context())
			writer := &waitingReview{arrived: make(chan struct{}), finished: make(chan struct{})}
			done := make(chan error, 1)
			go func() { done <- write(ctx, writer) }()
			defer func() { cancel(); <-done }()
			await(test, writer.arrived)
			closeReview()
			select {
			case <-writer.finished:
			default:
				test.Fatal("Close returned before review cancellation and drain")
			}
		})
	}
}

func TestSubmitReleasesOwnedMaterial(test *testing.T) {
	for _, cancelCall := range []bool{false, true} {
		test.Run(map[bool]string{false: "success", true: "canceled-auth"}[cancelCall], func(test *testing.T) {
			relay := newRelay(test)
			client := relay.client(test)
			operation, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, customJSON, ReplaceKey))
			if err != nil {
				test.Fatal(err)
			}
			defer operation.Close()
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			if cancelCall {
				relay.hold(authProcedure)
				done := make(chan error, 1)
				go func() { _, err := operation.Submit(ctx); done <- err }()
				await(test, relay.arrived)
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					test.Fatal("caller cancellation lost")
				}
				await(test, relay.drained)
				if relay.count(servicePrefix+"CreateAsset") != 0 {
					test.Fatal("pre-entry cancellation mutated")
				}
			} else {
				if _, err := operation.Submit(ctx); err != nil {
					test.Fatal(err)
				}
			}
			owned := operation.state().input.state()
			owned.mutex.Lock()
			closed := owned.closed
			remaining := len(owned.key)
			owned.mutex.Unlock()
			if !closed || remaining != 0 {
				test.Fatal("used operation retained replacement material after return")
			}
		})
	}
}

func TestSubmitFixedExpiryIsTransportDeadline(test *testing.T) {
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	operation, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, customJSON, ReplaceKey))
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	expires := time.Now().Add(5 * time.Second)
	operation.state().expires = expires
	base := client.state().http.Transport
	var seen, violation atomic.Bool
	client.state().http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		seen.Store(true)
		if deadline, ok := request.Context().Deadline(); !ok || deadline.After(expires) {
			violation.Store(true)
		}
		return base.RoundTrip(request)
	})
	if _, err := operation.Submit(test.Context()); err != nil {
		test.Fatal(err)
	}
	if !seen.Load() || violation.Load() {
		test.Fatal("fixed review cutoff was not bound to actual transport context")
	}
}

func TestOperationCloseCancelsActualAuth(test *testing.T) {
	relay := newRelay(test)
	client := relay.client(test)
	operation, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, customJSON, ReplaceKey))
	if err != nil {
		test.Fatal(err)
	}
	relay.hold(authProcedure)
	done := make(chan error, 1)
	go func() { _, err := operation.Submit(test.Context()); done <- err }()
	await(test, relay.arrived)
	operation.Close()
	await(test, relay.drained)
	if err := <-done; !errors.Is(err, context.Canceled) {
		test.Fatal("operation Close did not cancel/join actual auth")
	}
	if relay.count(servicePrefix+"CreateAsset") != 0 {
		test.Fatal("closed operation mutated")
	}
}

func TestCopyAndOrderDoNotRebase(test *testing.T) {
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	first := submitChange(test, client, Change{Action: Create}, fixtureInput(test, customJSON, ReplaceKey))
	second := submitChange(test, client, Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation))
	copyOperation, err := client.Prepare(test.Context(), Change{Action: Copy, ID: first.Metadata.ID, ExpectedRevision: 1, Name: "stale copy"}, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer copyOperation.Close()
	order, err := client.GetOrder(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	orderOperation, err := client.Prepare(test.Context(), Change{Action: ReplaceOrder, ExpectedOrderRevision: order.Revision, OrderIDs: []string{second.Metadata.ID, first.Metadata.ID}}, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer orderOperation.Close()
	if _, err := configuration.New(fixture.peer).Update(test.Context(), fixture.owner, first.Metadata.ID, 1, configuration.Draft{Name: "peer", GrokBuildProvider: &configuration.GrokBuildProvider{Mode: "native-login"}}, []configuration.SecretMutation{{Slot: configuration.GrokBuildAPIKeySlot, Action: configuration.Clear}}); err != nil {
		test.Fatal(err)
	}
	if _, err := copyOperation.Submit(test.Context()); !errors.Is(err, ErrConflict) {
		test.Fatal("copy silently rebased")
	}
	third := submitChange(test, client, Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation))
	if _, err := orderOperation.Submit(test.Context()); !errors.Is(err, ErrConflict) {
		test.Fatal("order silently dropped new member")
	}
	actual, err := client.GetOrder(test.Context())
	if err != nil || len(actual.Assets) != 3 || actual.Assets[2].ID != third.Metadata.ID {
		test.Fatal("stale order changed winning membership")
	}
}
