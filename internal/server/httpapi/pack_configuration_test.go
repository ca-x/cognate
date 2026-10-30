package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	packconfigurationv1 "github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1/packconfigurationv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"google.golang.org/protobuf/proto"
)

type packConfigurationArchive struct{}

func (packConfigurationArchive) WriteSkillArchive(context.Context, string, string, string, io.Writer) error {
	return nil
}

func TestPackConfigurationInvalidReferencesRollbackAndQuota(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	foreignOwner, err := fixture.auth.Authenticate(test.Context(), fixture.foreign)
	if err != nil {
		test.Fatal(err)
	}
	foreign, err := configuration.New(fixture.store).Create(test.Context(), foreignOwner, configuration.Draft{Name: "Other owner", Provider: configuration.ClaudeProvider{AuthMode: configuration.NativeLogin}}, nil)
	if err != nil {
		test.Fatal(err)
	}
	client := packconfigurationv1connect.NewPackConfigurationServiceClient(fixture.endpoint.Client(), fixture.endpoint.URL)
	for _, attempt := range []struct {
		refs []*packconfigurationv1.AttachmentReference
		code connect.Code
	}{
		{[]*packconfigurationv1.AttachmentReference{{AssetId: foreign.Metadata.ID, ExpectedAssetRevision: 1, TargetId: "claude.provider.user"}}, connect.CodeNotFound},
		{[]*packconfigurationv1.AttachmentReference{{AssetId: strings.Repeat("A", 26), ExpectedAssetRevision: 1, TargetId: "claude.provider.user"}}, connect.CodeNotFound},
		{[]*packconfigurationv1.AttachmentReference{{AssetId: fixture.asset.ID, ExpectedAssetRevision: 1, TargetId: "arbitrary.path"}}, connect.CodeFailedPrecondition},
		{[]*packconfigurationv1.AttachmentReference{fixture.ref(), fixture.ref()}, connect.CodeInvalidArgument},
	} {
		response, err := client.ReplaceAttachments(test.Context(), configurationRequest(&packconfigurationv1.ReplaceAttachmentsRequest{PackId: fixture.pack.ID, ExpectedPackRevision: 1, ExpectedConfigurationRevision: 1, References: attempt.refs}, fixture.session))
		if connect.CodeOf(err) != attempt.code || response != nil {
			test.Fatal("invalid/foreign reference classification or partial response")
		}
		snapshot, err := fixture.store.PackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack.ID)
		if err != nil || snapshot.PackRevision != 1 || snapshot.ConfigurationRevision != 1 || len(snapshot.Entries) != 0 {
			test.Fatal("rejected replacement advanced revisions")
		}
	}
	refs := make([]*packconfigurationv1.AttachmentReference, 17)
	for index := range refs {
		refs[index] = fixture.ref()
	}
	if response, err := client.ReplaceAttachments(test.Context(), configurationRequest(&packconfigurationv1.ReplaceAttachmentsRequest{PackId: fixture.pack.ID, ExpectedPackRevision: 1, ExpectedConfigurationRevision: 1, References: refs}, fixture.session)); connect.CodeOf(err) != connect.CodeInvalidArgument || response != nil {
		test.Fatal("over-limit array passed strict request grammar")
	}
}

func TestPackConfigurationConcurrentCAS(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	client := packconfigurationv1connect.NewPackConfigurationServiceClient(fixture.endpoint.Client(), fixture.endpoint.URL)
	start := make(chan struct{})
	type outcome struct {
		response *connect.Response[packconfigurationv1.ReplaceAttachmentsResponse]
		err      error
	}
	results := make(chan outcome, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			response, err := client.ReplaceAttachments(test.Context(), configurationRequest(&packconfigurationv1.ReplaceAttachmentsRequest{PackId: fixture.pack.ID, ExpectedPackRevision: 1, ExpectedConfigurationRevision: 1, References: []*packconfigurationv1.AttachmentReference{fixture.ref()}}, fixture.session))
			results <- outcome{response: response, err: err}
		})
	}
	close(start)
	workers.Wait()
	winners, failures := 0, 0
	for range 2 {
		result := <-results
		if result.err == nil {
			winners++
			if result.response == nil || result.response.Msg.Snapshot.PackRevision != 2 || result.response.Msg.Snapshot.ConfigurationRevision != 2 {
				test.Fatal("winner view invalid")
			}
		} else {
			failures++
			if result.response != nil || connect.CodeOf(result.err) != connect.CodeAborted && connect.CodeOf(result.err) != connect.CodeUnavailable {
				test.Fatal("contended failure leaked snapshot or incorrect class")
			}
		}
	}
	snapshot, err := fixture.store.PackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack.ID)
	if err != nil || winners != 1 || failures != 1 || snapshot.PackRevision != 2 || snapshot.ConfigurationRevision != 2 || len(snapshot.Entries) != 1 || snapshot.Entries[0].AssetID != fixture.asset.ID {
		test.Fatal("CAS wrote multiple times or lost selected metadata")
	}
}

type packConfigurationDropResponse struct {
	base  http.RoundTripper
	calls atomic.Int32
}

func (transport *packConfigurationDropResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if request.URL.Path != packconfigurationv1connect.PackConfigurationServiceReplaceAttachmentsProcedure {
		return response, err
	}
	if err != nil {
		return response, err
	}
	transport.calls.Add(1)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return nil, errors.New("synthetic post-commit response loss")
}

func TestPackConfigurationCommittedResponseLossDoesNotReplay(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	transport := &packConfigurationDropResponse{base: fixture.endpoint.Client().Transport}
	client := packconfigurationv1connect.NewPackConfigurationServiceClient(&http.Client{Transport: transport}, fixture.endpoint.URL)
	response, err := client.ReplaceAttachments(test.Context(), configurationRequest(&packconfigurationv1.ReplaceAttachmentsRequest{PackId: fixture.pack.ID, ExpectedPackRevision: 1, ExpectedConfigurationRevision: 1, References: []*packconfigurationv1.AttachmentReference{fixture.ref()}}, fixture.session))
	if err == nil || response != nil || transport.calls.Load() != 1 {
		test.Fatal("response loss replayed or became success")
	}
	snapshot, err := fixture.store.PackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack.ID)
	if err != nil || snapshot.PackRevision != 2 || snapshot.ConfigurationRevision != 2 || len(snapshot.Entries) != 1 {
		test.Fatal("actual committed write missing or duplicated")
	}
}

func TestPackConfigurationFourteenTargetsRemainMetadataOnly(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	agents := []string{"claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"}
	var refs []*packconfigurationv1.AttachmentReference
	for _, agent := range agents {
		payload := state.NewConfigurationPayload([]byte("synthetic-opaque-not-a-native-capability"))
		asset, err := fixture.store.CreateConfigurationAsset(test.Context(), fixture.owner, state.ConfigurationAssetDraft{AgentID: agent, Kind: state.ConfigurationKindProvider, SchemaVersion: 1, Name: "Metadata only", Payload: payload})
		if err != nil {
			test.Fatal(err)
		}
		refs = append(refs, &packconfigurationv1.AttachmentReference{AssetId: asset.ID, ExpectedAssetRevision: 1, TargetId: agent + ".provider.user"})
	}
	client := packconfigurationv1connect.NewPackConfigurationServiceClient(fixture.endpoint.Client(), fixture.endpoint.URL)
	response, err := client.ReplaceAttachments(test.Context(), configurationRequest(&packconfigurationv1.ReplaceAttachmentsRequest{PackId: fixture.pack.ID, ExpectedPackRevision: 1, ExpectedConfigurationRevision: 1, References: refs}, fixture.session))
	if err != nil || len(response.Msg.Snapshot.Entries) != 14 {
		test.Fatal("fourteen metadata targets")
	}
	snapshot, err := fixture.store.PackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack.ID)
	if err != nil {
		test.Fatal(err)
	}
	for index, entry := range snapshot.Entries {
		if entry.AgentID != agents[index] || entry.NativeAvailability != state.PackConfigurationNativeUnavailable {
			test.Fatal("placeholder target was claimed as native-ready")
		}
	}
}

func TestPackConfigurationRealSlowBodyDeadline(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	connection := packConfigurationSlowConnection(test, fixture.endpoint, fixture.session, packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, `{"packId":"held"}`)
	if err := connection.SetDeadline(time.Now().Add(25 * time.Second)); err != nil {
		test.Fatal(err)
	}
	started := time.Now()
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		test.Fatal("real request deadline did not produce bounded refusal")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusGatewayTimeout {
		test.Fatalf("slow body deadline status=%d, want bounded deadline refusal", response.StatusCode)
	}
	if elapsed := time.Since(started); elapsed < 19*time.Second || elapsed > 24*time.Second {
		test.Fatal("slow body was not bounded by original 20s work deadline")
	}
}

type packConfigurationUnreadBody struct{ reads int }

func (body *packConfigurationUnreadBody) Read([]byte) (int, error) { body.reads++; return 0, io.EOF }
func (*packConfigurationUnreadBody) Close() error                  { return nil }

type packConfigurationObservedBody struct {
	io.ReadCloser
	entered chan struct{}
	started bool
}

type packConfigurationDeadlineParent struct {
	context.Context
	expires time.Time
}

func (ctx packConfigurationDeadlineParent) Deadline() (time.Time, bool) { return ctx.expires, true }

type packConfigurationTimeoutBody struct {
	io.ReadCloser
	cancel  context.CancelFunc
	expires time.Time
}

func (body *packConfigurationTimeoutBody) Read([]byte) (int, error) {
	time.Sleep(max(0, time.Until(body.expires)) + time.Millisecond)
	body.cancel()
	return 0, os.ErrDeadlineExceeded
}

func TestPackConfigurationReadDeadlineCancellationRace(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	handler := NewWithOptions(fixture.auth, Options{Store: fixture.store, Packs: fixture.service})
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		parent, cancel := context.WithCancel(request.Context())
		defer cancel()
		expires := time.Now().Add(100 * time.Millisecond)
		request = request.WithContext(packConfigurationDeadlineParent{Context: parent, expires: expires})
		request.Body = &packConfigurationTimeoutBody{ReadCloser: request.Body, cancel: cancel, expires: expires}
		handler.ServeHTTP(writer, request)
	}))
	test.Cleanup(endpoint.Close)
	status, _ := configurationRaw(test, endpoint, fixture.session, packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, "application/json", []byte(`{}`))
	if status != http.StatusGatewayTimeout {
		test.Fatalf("elapsed transport deadline mislabeled as caller cancellation: %d", status)
	}
}

func (body *packConfigurationObservedBody) Read(buffer []byte) (int, error) {
	if !body.started {
		body.started = true
		body.entered <- struct{}{}
	}
	return body.ReadCloser.Read(buffer)
}

func packConfigurationSlowConnection(test *testing.T, endpoint *httptest.Server, session, path, body string) net.Conn {
	test.Helper()
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", endpoint.Listener.Addr().String(), endpoint.Client().Transport.(*http.Transport).TLSClientConfig.Clone())
	if err != nil {
		test.Fatal("synthetic TLS dial")
	}
	test.Cleanup(func() { _ = connection.Close() })
	if err := connection.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		test.Fatal(err)
	}
	if _, err := fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nConnect-Protocol-Version: 1\r\nAuthorization: Bearer %s\r\nContent-Length: %d\r\n\r\n%s", path, endpoint.Listener.Addr().String(), session, len(body), body[:1]); err != nil {
		test.Fatal("synthetic partial body send")
	}
	return connection
}

func TestPackConfigurationAuthenticationAndExactPathsBeforeBody(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	handler := NewWithOptions(fixture.auth, Options{Store: fixture.store, Packs: fixture.service})
	for _, path := range []string{packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, packconfigurationv1connect.PackConfigurationServiceReplaceAttachmentsProcedure} {
		for _, token := range []string{"", "mcp1.invalid", "shp1.invalid", "private-session-canary"} {
			body := &packConfigurationUnreadBody{}
			request := httptest.NewRequest(http.MethodPost, "https://localhost"+path, body)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || body.reads != 0 || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "canary") {
				test.Fatal("auth-before-body or finite error boundary")
			}
		}
	}
	for _, path := range []string{packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure + "/extra", packconfigurationv1connect.PackConfigurationServiceReplaceAttachmentsProcedure + "Extra"} {
		body := &packConfigurationUnreadBody{}
		request := httptest.NewRequest(http.MethodPost, "https://localhost"+path, body)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+fixture.session)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotImplemented || body.reads != 0 {
			test.Fatal("near path was decoded")
		}
	}
	body := &packConfigurationUnreadBody{}
	request := httptest.NewRequest(http.MethodPost, "https://localhost"+packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, body)
	request.Header.Set("Origin", "https://foreign.invalid")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || body.reads != 0 {
		test.Fatal("origin guard read body")
	}
}

func TestPackConfigurationRegistrationAndEncodingBoundaries(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	for _, options := range []Options{{Store: fixture.store}, {Packs: fixture.service}} {
		endpoint := httptest.NewTLSServer(NewWithOptions(fixture.auth, options))
		status, _ := configurationRaw(test, endpoint, fixture.session, packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, "application/json", []byte(`{}`))
		endpoint.Close()
		if status != http.StatusNotFound {
			test.Fatal("attachment RPC registered without both Store and owned Packs lifetime")
		}
	}
	handler := NewWithOptions(fixture.auth, Options{Store: fixture.store, Packs: fixture.service})
	for _, encoding := range []string{"gzip", "unknown-canary"} {
		body := &packConfigurationUnreadBody{}
		request := httptest.NewRequest(http.MethodPost, "https://localhost"+packconfigurationv1connect.PackConfigurationServiceReplaceAttachmentsProcedure, body)
		request.Header.Set("Authorization", "Bearer "+fixture.session)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Content-Encoding", encoding)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || body.reads != 0 || strings.Contains(response.Body.String(), "canary") {
			test.Fatal("encoding parsed body or disclosed fields")
		}
	}
}

func TestPackConfigurationEightSlowBodiesAndCloseJoin(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	entered, finished := make(chan struct{}, 8), make(chan struct{}, 8)
	handler := NewWithOptions(fixture.auth, Options{Store: fixture.store, Packs: fixture.service})
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = &packConfigurationObservedBody{ReadCloser: request.Body, entered: entered}
		handler.ServeHTTP(writer, request)
		finished <- struct{}{}
	}))
	test.Cleanup(endpoint.Close)
	for index := range 8 {
		path := packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure
		if index%2 != 0 {
			path = packconfigurationv1connect.PackConfigurationServiceReplaceAttachmentsProcedure
		}
		packConfigurationSlowConnection(test, endpoint, fixture.session, path, `{"packId":"held"}`)
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			test.Fatal("slow body was not admitted")
		}
	}
	body := &packConfigurationUnreadBody{}
	request := httptest.NewRequest(http.MethodPost, "https://localhost"+packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || body.reads != 0 {
		test.Fatal("ninth slot was not rejected before authentication/body")
	}
	closed := make(chan struct{})
	go func() { fixture.service.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		test.Fatal("Packs.Close did not cancel and join real blocked network body")
	}
	for range 8 {
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			test.Fatal("body worker not joined")
		}
	}
	status, _ := configurationRaw(test, fixture.endpoint, fixture.session, packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, "application/json", []byte(`{}`))
	if status != http.StatusServiceUnavailable {
		test.Fatal("closed Pack service admitted new RPC")
	}
}

func TestPackConfigurationSessionRevokedDuringSlowBody(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	entered := make(chan struct{}, 1)
	handler := NewWithOptions(fixture.auth, Options{Store: fixture.store, Packs: fixture.service})
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = &packConfigurationObservedBody{ReadCloser: request.Body, entered: entered}
		handler.ServeHTTP(writer, request)
	}))
	test.Cleanup(endpoint.Close)
	body := fmt.Sprintf(`{"packId":%q,"expectedPackRevision":"1","expectedConfigurationRevision":"1","references":[]}`, fixture.pack.ID)
	connection := packConfigurationSlowConnection(test, endpoint, fixture.session, packconfigurationv1connect.PackConfigurationServiceReplaceAttachmentsProcedure, body)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		test.Fatal("authenticated slow body not reached")
	}
	if err := fixture.auth.Logout(test.Context(), fixture.session); err != nil {
		test.Fatal(err)
	}
	if _, err := io.WriteString(connection, body[1:]); err != nil {
		test.Fatal("complete slow body")
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		test.Fatal("read revocation response")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		test.Fatal("session revoked during body retained write authority")
	}
	snapshot, err := fixture.store.PackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack.ID)
	if err != nil || snapshot.PackRevision != 1 || snapshot.ConfigurationRevision != 1 || len(snapshot.Entries) != 0 {
		test.Fatal("revoked request wrote SQLite")
	}
}

func TestPackConfigurationTwentySecondLifecycleDeadline(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	handler := &packConfigurationHandler{auth: &authHandler{service: fixture.auth}, store: fixture.store, service: fixture.service, slots: make(chan struct{}, 8)}
	observed := make(chan time.Duration, 1)
	endpoint := httptest.NewTLSServer(handler.requests(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		deadline, ok := request.Context().Deadline()
		if !ok {
			observed <- -1
		} else {
			observed <- time.Until(deadline)
		}
		writer.WriteHeader(http.StatusNoContent)
	})))
	test.Cleanup(endpoint.Close)
	status, _ := configurationRaw(test, endpoint, fixture.session, packconfigurationv1connect.PackConfigurationServiceGetAttachmentsProcedure, "application/json", []byte(`{}`))
	if status != http.StatusNoContent {
		test.Fatal("deadline probe transport")
	}
	remaining := <-observed
	if remaining <= 0 || remaining > 20*time.Second {
		test.Fatal("request lifetime exceeds original 20s contract")
	}
}

type packConfigurationTestFixture struct {
	auth     *auth.Service
	store    *state.Store
	service  *packs.Service
	owner    string
	session  string
	foreign  string
	pack     state.Pack
	asset    state.ConfigurationAsset
	endpoint *httptest.Server
}

func newPackConfigurationFixture(test *testing.T) *packConfigurationTestFixture {
	test.Helper()
	root := test.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x63}, 32)
	store, err := state.Open(test.Context(), filepath.Join(root, "server.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	owner, session := mcpSession(test, authentication, "pc2-alice")
	_, foreign := mcpSession(test, authentication, "pc2-bob")
	reference, err := repository.ParseReference("org/pc2", "")
	if err != nil {
		test.Fatal(err)
	}
	collected, err := store.CollectRepository(test.Context(), owner, reference, strings.Repeat("a", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Skill", Description: "PC2 fixture", Path: "example"}, ContentHash: strings.Repeat("c", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	detail, err := store.CollectedRepository(test.Context(), owner, collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	entry, err := store.CreatePack(test.Context(), owner, state.PackDraft{Name: "Skills only", Items: []state.PackSelection{{SkillID: detail.Skills[0].ID}}})
	if err != nil {
		test.Fatal(err)
	}
	view, err := configuration.New(store).Create(test.Context(), owner, configuration.Draft{Name: "pc2-private-asset-name", Provider: configuration.ClaudeProvider{AuthMode: configuration.NativeLogin}}, nil)
	if err != nil {
		test.Fatal(err)
	}
	service, err := packs.NewService(store, packConfigurationArchive{}, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(service.Close)
	endpoint := httptest.NewTLSServer(NewWithOptions(authentication, Options{Store: store, Packs: service, Configuration: configuration.New(store)}))
	test.Cleanup(endpoint.Close)
	return &packConfigurationTestFixture{auth: authentication, store: store, service: service, owner: owner, session: session, foreign: foreign, pack: entry, asset: view.Metadata, endpoint: endpoint}
}

func (fixture *packConfigurationTestFixture) ref() *packconfigurationv1.AttachmentReference {
	return &packconfigurationv1.AttachmentReference{AssetId: fixture.asset.ID, ExpectedAssetRevision: 1, TargetId: "claude.provider.user"}
}

func TestPackConfigurationOwnerCASStaleAndLegacyIsolation(test *testing.T) {
	for _, format := range []string{"proto", "json"} {
		test.Run(format, func(test *testing.T) {
			fixture := newPackConfigurationFixture(test)
			var options []connect.ClientOption
			if format == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := packconfigurationv1connect.NewPackConfigurationServiceClient(fixture.endpoint.Client(), fixture.endpoint.URL, options...)
			get := func(token, id string) (*connect.Response[packconfigurationv1.GetAttachmentsResponse], error) {
				return client.GetAttachments(test.Context(), configurationRequest(&packconfigurationv1.GetAttachmentsRequest{PackId: id}, token))
			}
			initial, err := get(fixture.session, fixture.pack.ID)
			if err != nil || initial.Msg.Snapshot.PackRevision != 1 || initial.Msg.Snapshot.ConfigurationRevision != 1 || len(initial.Msg.Snapshot.Entries) != 0 || len(initial.Msg.Snapshot.SnapshotDigest) != 32 {
				test.Fatal("initial snapshot")
			}
			for _, token := range []string{"", "shp1.invalid", "mcp1.invalid"} {
				if _, err := get(token, fixture.pack.ID); connect.CodeOf(err) != connect.CodeUnauthenticated {
					test.Fatal("non-owner-session credential accepted")
				}
			}
			for _, id := range []string{fixture.pack.ID, strings.Repeat("A", 26)} {
				if response, err := get(fixture.foreign, id); connect.CodeOf(err) != connect.CodeNotFound || response != nil {
					test.Fatal("foreign/missing mismatch or response leak")
				}
			}
			replace := func(token string, packRevision, configurationRevision int64, refs ...*packconfigurationv1.AttachmentReference) (*connect.Response[packconfigurationv1.ReplaceAttachmentsResponse], error) {
				return client.ReplaceAttachments(test.Context(), configurationRequest(&packconfigurationv1.ReplaceAttachmentsRequest{PackId: fixture.pack.ID, ExpectedPackRevision: packRevision, ExpectedConfigurationRevision: configurationRevision, References: refs}, token))
			}
			if response, err := replace(fixture.foreign, 1, 1, fixture.ref()); connect.CodeOf(err) != connect.CodeNotFound || response != nil {
				test.Fatal("foreign replacement leaked or wrote")
			}
			first, err := replace(fixture.session, 1, 1, fixture.ref())
			if err != nil || first.Msg.Snapshot.PackRevision != 2 || first.Msg.Snapshot.ConfigurationRevision != 2 || len(first.Msg.Snapshot.Entries) != 1 || first.Msg.Snapshot.Entries[0].State != packconfigurationv1.AttachmentState_ATTACHMENT_STATE_CURRENT {
				test.Fatal("attachment replacement")
			}
			stored, err := fixture.store.PackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack.ID)
			if err != nil || !bytes.Equal(stored.Digest[:], first.Msg.Snapshot.SnapshotDigest) || stored.Entries[0].NativeAvailability != state.PackConfigurationNativeUnavailable {
				test.Fatal("wire/store digest mismatch or fake native readiness")
			}
			for _, pair := range [][2]int64{{1, 2}, {2, 1}} {
				if response, err := replace(fixture.session, pair[0], pair[1], fixture.ref()); connect.CodeOf(err) != connect.CodeAborted || response != nil {
					test.Fatal("two-dimensional CAS bypass")
				}
			}
			if _, err := fixture.store.UpdatePack(test.Context(), fixture.owner, fixture.pack.ID, 2, state.PackDraft{Name: "Skill-only edit", Items: []state.PackSelection{{SkillID: fixture.pack.Items[0].SkillID}}}); err != nil {
				test.Fatal(err)
			}
			if _, err := configuration.New(fixture.store).Update(test.Context(), fixture.owner, fixture.asset.ID, 1, configuration.Draft{Name: "Changed asset", Provider: configuration.ClaudeProvider{AuthMode: configuration.NativeLogin}}, nil); err != nil {
				test.Fatal(err)
			}
			stale, err := get(fixture.session, fixture.pack.ID)
			if err != nil || stale.Msg.Snapshot.PackRevision != 3 || stale.Msg.Snapshot.ConfigurationRevision != 2 || stale.Msg.Snapshot.Entries[0].AssetRevision != 1 || stale.Msg.Snapshot.Entries[0].State != packconfigurationv1.AttachmentState_ATTACHMENT_STATE_STALE || stale.Msg.Snapshot.Entries[0].AttachmentId != first.Msg.Snapshot.Entries[0].AttachmentId {
				test.Fatal("legacy update or stale pin contract")
			}
			if response, err := replace(fixture.session, 3, 2, fixture.ref()); connect.CodeOf(err) != connect.CodeAborted || response != nil {
				test.Fatal("stale asset reference accepted")
			}
			cleared, err := replace(fixture.session, 3, 2)
			if err != nil || len(cleared.Msg.Snapshot.Entries) != 0 || cleared.Msg.Snapshot.PackRevision != 4 {
				test.Fatal("explicit detach")
			}
		})
	}
}

func TestPackConfigurationFourProviderMetadataOrder(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	var references []*packconfigurationv1.AttachmentReference
	for _, draft := range []*configurationv1.Draft{configurationDraft("Claude"), codexConfigurationDraft(), geminiConfigurationDraft(), openCodeConfigurationDraft("v1")} {
		input, secrets, err := configurationInput(draft, nil)
		if err != nil {
			test.Fatal(err)
		}
		view, err := configuration.New(fixture.store).Create(test.Context(), fixture.owner, input, secrets)
		configurationClearSecrets(secrets)
		if err != nil {
			test.Fatal(err)
		}
		references = append(references, &packconfigurationv1.AttachmentReference{AssetId: view.Metadata.ID, ExpectedAssetRevision: 1, TargetId: view.Metadata.AgentID + ".provider.user"})
	}
	client := packconfigurationv1connect.NewPackConfigurationServiceClient(fixture.endpoint.Client(), fixture.endpoint.URL)
	first, err := client.ReplaceAttachments(test.Context(), configurationRequest(&packconfigurationv1.ReplaceAttachmentsRequest{PackId: fixture.pack.ID, ExpectedPackRevision: 1, ExpectedConfigurationRevision: 1, References: references}, fixture.session))
	if err != nil || len(first.Msg.Snapshot.Entries) != 4 {
		test.Fatal("four provider metadata")
	}
	for ordinal, entry := range first.Msg.Snapshot.Entries {
		if entry.AssetId != references[ordinal].AssetId || entry.Ordinal != uint32(ordinal) || entry.State != packconfigurationv1.AttachmentState_ATTACHMENT_STATE_CURRENT {
			test.Fatal("ordered metadata")
		}
	}
	second, err := client.ReplaceAttachments(test.Context(), configurationRequest(&packconfigurationv1.ReplaceAttachmentsRequest{PackId: fixture.pack.ID, ExpectedPackRevision: 2, ExpectedConfigurationRevision: 2, References: []*packconfigurationv1.AttachmentReference{references[3], references[0]}}, fixture.session))
	if err != nil || second.Msg.Snapshot.Entries[0].AttachmentId != first.Msg.Snapshot.Entries[3].AttachmentId || second.Msg.Snapshot.Entries[1].AttachmentId != first.Msg.Snapshot.Entries[0].AttachmentId {
		test.Fatal("reorder did not preserve IDs")
	}
}

func TestPackConfigurationDeleteInUseTypedDetail(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	if _, err := fixture.store.ReplacePackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack.ID, 1, 1, []state.PackConfigurationReference{{AssetID: fixture.asset.ID, AssetRevision: 1, TargetID: "claude.provider.user"}}); err != nil {
		test.Fatal(err)
	}
	for _, options := range [][]connect.ClientOption{nil, {connect.WithProtoJSON()}} {
		client := configurationv1connect.NewConfigurationServiceClient(fixture.endpoint.Client(), fixture.endpoint.URL, options...)
		_, err := client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: fixture.asset.ID, ExpectedRevision: 1}, fixture.session))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			test.Fatal("in-use classification lost between state/domain/HTTP")
		}
		failure, ok := err.(*connect.Error)
		if !ok || len(failure.Details()) != 1 {
			test.Fatal("missing finite detail")
		}
		message, err := failure.Details()[0].Value()
		detail, ok := message.(*packconfigurationv1.AttachmentFailure)
		if err != nil || !ok || detail.Reason != packconfigurationv1.AttachmentFailureReason_ATTACHMENT_FAILURE_REASON_ASSET_IN_USE {
			test.Fatal("invalid finite in-use reason")
		}
		if !proto.Equal(detail, &packconfigurationv1.AttachmentFailure{Reason: packconfigurationv1.AttachmentFailureReason_ATTACHMENT_FAILURE_REASON_ASSET_IN_USE}) {
			test.Fatal("in-use detail contains extra fields")
		}
		_, err = client.DeleteAsset(test.Context(), configurationRequest(&configurationv1.DeleteAssetRequest{Id: fixture.asset.ID, ExpectedRevision: 2}, fixture.session))
		if connect.CodeOf(err) != connect.CodeAborted {
			test.Fatal("stale delete bypassed revision precedence")
		}
	}
}

func TestPackConfigurationOwnerRouteExists(test *testing.T) {
	key := make([]byte, 32)
	root := test.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(root, "state.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	_, session := mcpSession(test, authentication, "pc2-owner")
	service, err := packs.NewService(store, packConfigurationArchive{}, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(service.Close)
	endpoint := httptest.NewTLSServer(NewWithOptions(authentication, Options{Store: store, Packs: service}))
	test.Cleanup(endpoint.Close)
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, endpoint.URL+"/skillhub.pack.configuration.v1.PackConfigurationService/GetAttachments", strings.NewReader(`{"packId":"missing"}`))
	if err != nil {
		test.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	request.Header.Set("Authorization", "Bearer "+session)
	response, err := endpoint.Client().Do(request)
	if err != nil {
		test.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `"code":"not_found"`) {
		test.Fatal("owner attachment endpoint absent or did not return finite owner-scoped NotFound")
	}
}
