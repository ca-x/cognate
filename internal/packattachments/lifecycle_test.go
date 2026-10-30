package packattachments

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
)

type noArchives struct{}

type corruptMutationReceipt struct {
	base http.RoundTripper
	body []byte
}

func (transport corruptMutationReceipt) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusOK || request.URL.Path != servicePrefix+"ReplaceAttachments" {
		return response, err
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(transport.body))
	response.ContentLength = int64(len(transport.body))
	response.Header.Del("Content-Length")
	return response, nil
}

func TestCommittedInvalidReceiptsAreUnknown(test *testing.T) {
	for _, body := range [][]byte{{0xff}, {}, bytes.Repeat([]byte{0xff}, 128<<10)} {
		test.Run(fmt.Sprintf("bytes-%d", len(body)), func(test *testing.T) {
			fixture := newFixture(test)
			client, err := New(fixture.origin, fixture.session, fixture.transport)
			if err != nil {
				test.Fatal(err)
			}
			defer client.Close()
			client.state().http.Transport = corruptMutationReceipt{base: client.state().http.Transport, body: body}
			operation, err := client.Prepare(test.Context(), fixture.pack, 1, 1, []Reference{{AssetID: fixture.asset, ExpectedAssetRevision: 1, TargetID: "claude.provider.user"}})
			if err != nil {
				test.Fatal(err)
			}
			defer operation.Close()
			if _, err := operation.Submit(test.Context()); !errors.Is(err, ErrUnknown) {
				test.Fatalf("committed invalid receipt must be Unknown: %v", err)
			}
			persisted, err := fixture.store.PackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack)
			if err != nil || persisted.PackRevision != 2 || persisted.ConfigurationRevision != 2 || len(persisted.Entries) != 1 || fixture.control.replace.Load() != 1 {
				test.Fatal("must follow exactly one real SQLite committed HTTP mutation")
			}
			if _, err := client.Get(test.Context(), fixture.pack); err != nil {
				test.Fatal("read-only reconciliation", err)
			}
			if next, err := client.Prepare(test.Context(), fixture.pack, 2, 2, []Reference{}); next != nil || !errors.Is(err, ErrUnknown) {
				test.Fatal("unconfirmed mutation must block fresh mutation")
			}
		})
	}
}

func (noArchives) WriteSkillArchive(context.Context, string, string, string, io.Writer) error {
	return nil
}

type fixture struct {
	store                               *state.Store
	authentication                      *auth.Service
	origin, session, owner, pack, asset string
	transport                           *http.Transport
	control                             *fixtureControl
	databasePath                        string
}

type fixtureControl struct {
	replace    atomic.Int32
	drop, wait atomic.Bool
	arrived    chan struct{}
	release    chan struct{}
	once       sync.Once
}
type discardedResponse struct {
	http.ResponseWriter
	headers http.Header
}

func (writer discardedResponse) Header() http.Header            { return writer.headers }
func (writer discardedResponse) WriteHeader(int)                {}
func (writer discardedResponse) Write(data []byte) (int, error) { return len(data), nil }
func (writer discardedResponse) Unwrap() http.ResponseWriter    { return writer.ResponseWriter }

func newFixture(test *testing.T) *fixture {
	test.Helper()
	key := bytes.Repeat([]byte{44}, 32)
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if store.Close() != nil {
			test.Error("fixture close")
		}
		clear(key)
	})
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	owner, err := authentication.Register(test.Context(), "attachment-owner", []byte("synthetic-owner-password"))
	if err != nil {
		test.Fatal(err)
	}
	login, err := authentication.Login(test.Context(), "attachment-owner", []byte("synthetic-owner-password"))
	if err != nil {
		test.Fatal(err)
	}
	ref, err := repository.ParseReference("org/attachments", "")
	if err != nil {
		test.Fatal(err)
	}
	repo, err := store.CollectRepository(test.Context(), owner, ref, strings.Repeat("a", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Synthetic", Description: "Only fixture", Path: "skills/example"}, ContentHash: strings.Repeat("b", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	detail, err := store.CollectedRepository(test.Context(), owner, repo.ID)
	if err != nil {
		test.Fatal(err)
	}
	pack, err := store.CreatePack(test.Context(), owner, state.PackDraft{Name: "Pack", Items: []state.PackSelection{{SkillID: detail.Skills[0].ID}}})
	if err != nil {
		test.Fatal(err)
	}
	asset, err := configuration.New(store).Create(test.Context(), owner, configuration.Draft{Name: "Private provider", Provider: configuration.ClaudeProvider{AuthMode: configuration.NativeLogin}}, nil)
	if err != nil {
		test.Fatal(err)
	}
	service, err := packs.NewService(store, noArchives{}, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(service.Close)
	control := &fixtureControl{arrived: make(chan struct{}), release: make(chan struct{})}
	handler := httpapi.NewWithOptions(authentication, httpapi.Options{Store: store, Packs: service, Configuration: configuration.New(store)})
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/GetAttachments") && control.wait.Load() {
			_, _ = io.Copy(io.Discard, request.Body)
			control.once.Do(func() { close(control.arrived) })
			select {
			case <-request.Context().Done():
			case <-control.release:
			case <-test.Context().Done():
			}
			return
		}
		if strings.HasSuffix(request.URL.Path, "/ReplaceAttachments") {
			control.replace.Add(1)
			if control.drop.Load() {
				handler.ServeHTTP(discardedResponse{ResponseWriter: writer, headers: make(http.Header)}, request)
				connection, _, err := http.NewResponseController(writer).Hijack()
				if err == nil {
					_ = connection.Close()
				}
				return
			}
		}
		handler.ServeHTTP(writer, request)
	}))
	test.Cleanup(endpoint.Close)
	pool := x509.NewCertPool()
	pool.AddCert(endpoint.Certificate())
	return &fixture{store: store, authentication: authentication, origin: endpoint.URL, owner: owner, session: login.Reveal(), pack: pack.ID, asset: asset.Metadata.ID, transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, control: control, databasePath: filepath.Join(directory, "server.db")}
}

func TestActualTLSOwnerGet(test *testing.T) {
	fixture := newFixture(test)
	client, err := New(fixture.origin, fixture.session, fixture.transport)
	if err != nil {
		test.Fatal("client creation")
	}
	defer client.Close()
	snapshot, err := client.Get(test.Context(), fixture.pack)
	if err != nil || snapshot.PackID != fixture.pack || snapshot.PackRevision != 1 || snapshot.ConfigurationRevision != 1 || len(snapshot.Entries) != 0 {
		test.Fatal("real TLS owner metadata Get refused")
	}
}

func TestActualTLSReplaceCASAndClear(test *testing.T) {
	fixture := newFixture(test)
	client, err := New(fixture.origin, fixture.session, fixture.transport)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	refs := []Reference{{AssetID: fixture.asset, ExpectedAssetRevision: 1, TargetID: "claude.provider.user"}}
	operation, err := client.Prepare(test.Context(), fixture.pack, 1, 1, refs)
	if err != nil {
		test.Fatal("real prepare refused")
	}
	defer operation.Close()
	refs[0].AssetID = strings.Repeat("A", 26)
	before, err := fixture.store.PackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack)
	if err != nil || before.PackRevision != 1 || len(before.Entries) != 0 {
		test.Fatal("preview wrote metadata")
	}
	var review bytes.Buffer
	if operation.WriteSummary(test.Context(), &review) != nil || !strings.Contains(review.String(), "NOT server-validated") || !strings.Contains(review.String(), "NativeUnavailable") {
		test.Fatal("review misrepresented readiness")
	}
	after, err := operation.Submit(test.Context())
	if err != nil || after.PackRevision != 2 || after.ConfigurationRevision != 2 || len(after.Entries) != 1 || after.Entries[0].AssetID != fixture.asset {
		test.Fatal("real dual-CAS replacement did not commit exact cloned refs")
	}
	persisted, err := fixture.store.PackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack)
	if err != nil || len(persisted.Entries) != 1 || persisted.Entries[0].ID != after.Entries[0].ID || persisted.Digest != after.Digest || persisted.Entries[0].AssetID != after.Entries[0].AssetID || persisted.Entries[0].TargetID != after.Entries[0].TargetID {
		test.Fatal("returned attachment identity is not the actual persisted entry")
	}
	if _, err := operation.Submit(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("replayed mutation")
	}
	if _, err := client.Prepare(test.Context(), fixture.pack, 1, 2, []Reference{}); !errors.Is(err, ErrConflict) {
		test.Fatal("stale pack CAS admitted")
	}
	if _, err := client.Prepare(test.Context(), fixture.pack, 2, 1, []Reference{}); !errors.Is(err, ErrConflict) {
		test.Fatal("stale config CAS admitted")
	}
	clearOperation, err := client.Prepare(test.Context(), fixture.pack, 2, 2, []Reference{})
	if err != nil {
		test.Fatal(err)
	}
	defer clearOperation.Close()
	cleared, err := clearOperation.Submit(test.Context())
	if err != nil || len(cleared.Entries) != 0 || cleared.PackRevision != 3 || cleared.ConfigurationRevision != 3 {
		test.Fatal("explicit empty did not clear")
	}
}

func TestActualTLSResponseLossNeverReplays(test *testing.T) {
	fixture := newFixture(test)
	audit, err := sql.Open("sqlite3", fixture.databasePath)
	if err != nil {
		test.Fatal(err)
	}
	defer audit.Close()
	for _, statement := range []string{`CREATE TABLE attachment_commit_audit(revision INTEGER NOT NULL)`, `CREATE TRIGGER attachment_committed AFTER UPDATE OF configuration_revision ON packs WHEN NEW.configuration_revision>OLD.configuration_revision BEGIN INSERT INTO attachment_commit_audit VALUES(NEW.configuration_revision); END`} {
		if _, err := audit.ExecContext(test.Context(), statement); err != nil {
			test.Fatal("audit trigger setup")
		}
	}
	client, err := New(fixture.origin, fixture.session, fixture.transport)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	operation, err := client.Prepare(test.Context(), fixture.pack, 1, 1, []Reference{{AssetID: fixture.asset, ExpectedAssetRevision: 1, TargetID: "claude.provider.user"}})
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	fixture.control.drop.Store(true)
	callerAttempts := 0
	submit := func() (Snapshot, error) { callerAttempts++; return operation.Submit(test.Context()) }
	if _, err := submit(); !errors.Is(err, ErrUnknown) {
		test.Fatal("lost committed response not Unknown")
	}
	if _, err := client.Prepare(test.Context(), fixture.pack, 2, 2, []Reference{}); !errors.Is(err, ErrUnknown) {
		test.Fatal("unknown client allowed another write")
	}
	if _, err := submit(); !errors.Is(err, ErrUnknown) {
		test.Fatal("same client replay accepted")
	}
	snapshot, err := client.Get(test.Context(), fixture.pack)
	if err != nil || snapshot.ConfigurationRevision != 2 || len(snapshot.Entries) != 1 {
		test.Fatal("unknown client could not reconcile read-only metadata")
	}
	var committed int
	if audit.QueryRowContext(test.Context(), `SELECT count(*) FROM attachment_commit_audit`).Scan(&committed) != nil || callerAttempts != 2 || fixture.control.replace.Load() != 1 || committed != 1 {
		test.Fatal("caller/HTTP/physical commit attempts diverged")
	}
}

func TestActualTLSCallerAndCloseCancelJoin(test *testing.T) {
	for _, closing := range []bool{false, true} {
		test.Run(map[bool]string{false: "caller", true: "close"}[closing], func(test *testing.T) {
			fixture := newFixture(test)
			client, err := New(fixture.origin, fixture.session, fixture.transport)
			if err != nil {
				test.Fatal(err)
			}
			defer client.Close()
			fixture.control.wait.Store(true)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			done := make(chan error, 1)
			var workers sync.WaitGroup
			workers.Go(func() { _, err := client.Get(ctx, fixture.pack); done <- err })
			defer func() { close(fixture.control.release); cancel(); client.Close(); workers.Wait() }()
			select {
			case <-fixture.control.arrived:
			case <-time.After(3 * time.Second):
				test.Fatal("actual HTTP request not entered")
			}
			if closing {
				client.Close()
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					test.Fatal("cancelled request succeeded")
				}
			case <-time.After(2 * time.Second):
				test.Fatal("cancel/Close did not join actual request")
			}
		})
	}
}

func TestActualTLSForeignOwnerAndKnownFailure(test *testing.T) {
	fixture := newFixture(test)
	client, err := New(fixture.origin, fixture.session, fixture.transport)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	bad, err := client.Prepare(test.Context(), fixture.pack, 1, 1, []Reference{{AssetID: fixture.asset, ExpectedAssetRevision: 2, TargetID: "claude.provider.user"}})
	if err != nil {
		test.Fatal("metadata preview falsely validates new refs")
	}
	defer bad.Close()
	if _, err := bad.Submit(test.Context()); !errors.Is(err, ErrConflict) {
		test.Fatal("asset stale not known conflict")
	}
	good, err := client.Prepare(test.Context(), fixture.pack, 1, 1, []Reference{})
	if err != nil {
		test.Fatal("known conflict poisoned client")
	}
	defer good.Close()
	if _, err := good.Submit(test.Context()); err != nil {
		test.Fatal("fresh explicit operation after known failure refused")
	}
	_, err = fixture.authentication.Register(test.Context(), "attachment-other", []byte("synthetic-other-password"))
	if err != nil {
		test.Fatal(err)
	}
	foreign, err := fixture.authentication.Login(test.Context(), "attachment-other", []byte("synthetic-other-password"))
	if err != nil {
		test.Fatal(err)
	}
	client.state().session = foreign.Reveal()
	if _, err := client.Get(test.Context(), fixture.pack); !errors.Is(err, ErrDenied) {
		test.Fatal("same client silently changed owner")
	}
}

func TestActualTLSOrderStaleAndPhysicalPeerCAS(test *testing.T) {
	fixture := newFixture(test)
	peer, err := state.Open(test.Context(), fixture.databasePath, bytes.Repeat([]byte{44}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer peer.Close()
	second, err := configuration.New(peer).Create(test.Context(), fixture.owner, configuration.Draft{Name: "Second", Provider: configuration.ClaudeProvider{AuthMode: configuration.NativeLogin}}, nil)
	if err != nil {
		test.Fatal(err)
	}
	client, err := New(fixture.origin, fixture.session, fixture.transport)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	refs := []Reference{{AssetID: second.Metadata.ID, ExpectedAssetRevision: 1, TargetID: "claude.provider.user"}, {AssetID: fixture.asset, ExpectedAssetRevision: 1, TargetID: "claude.provider.user"}}
	operation, err := client.Prepare(test.Context(), fixture.pack, 1, 1, refs)
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	after, err := operation.Submit(test.Context())
	if err != nil || len(after.Entries) != 2 || after.Entries[0].AssetID != second.Metadata.ID || after.Entries[1].AssetID != fixture.asset {
		test.Fatal("ordered references changed")
	}
	stalePlan, err := client.Prepare(test.Context(), fixture.pack, 2, 2, []Reference{})
	if err != nil {
		test.Fatal(err)
	}
	defer stalePlan.Close()
	if _, err := peer.ReplacePackConfigurationAttachments(test.Context(), fixture.owner, fixture.pack, 2, 2, []state.PackConfigurationReference{{AssetID: fixture.asset, AssetRevision: 1, TargetID: "claude.provider.user"}}); err != nil {
		test.Fatal("physical peer CAS")
	}
	if _, err := stalePlan.Submit(test.Context()); !errors.Is(err, ErrConflict) {
		test.Fatal("stale physical peer CAS did not invalidate preview")
	}
	if _, err := configuration.New(peer).Update(test.Context(), fixture.owner, fixture.asset, 1, configuration.Draft{Name: "Updated", Provider: configuration.ClaudeProvider{AuthMode: configuration.NativeLogin}}, nil); err != nil {
		test.Fatal(err)
	}
	snapshot, err := client.Get(test.Context(), fixture.pack)
	if err != nil || len(snapshot.Entries) != 1 || snapshot.Entries[0].State != 2 || snapshot.Entries[0].AssetRevision != 1 {
		test.Fatal("stale reference metadata hidden or guessed current")
	}
	unsupported, err := client.Prepare(test.Context(), fixture.pack, 3, 3, []Reference{{AssetID: fixture.asset, ExpectedAssetRevision: 2, TargetID: "unknown.target"}})
	if err != nil {
		test.Fatal("metadata-only preview claimed target validation")
	}
	defer unsupported.Close()
	if _, err := unsupported.Submit(test.Context()); !errors.Is(err, ErrTargetUnsupported) {
		test.Fatal("unsupported target mislabeled as CAS or unknown")
	}
}

type contextProbe struct {
	base     http.RoundTripper
	key      any
	observed atomic.Bool
	calls    atomic.Int32
}

func (probe *contextProbe) RoundTrip(request *http.Request) (*http.Response, error) {
	probe.calls.Add(1)
	if request.Context().Value(probe.key) != nil {
		probe.observed.Store(true)
	}
	return probe.base.RoundTrip(request)
}

func TestActualTLSContextPrivacyAndRevocation(test *testing.T) {
	fixture := newFixture(test)
	client, err := New(fixture.origin, fixture.session, fixture.transport)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	key := new(struct{})
	probe := &contextProbe{base: client.state().http.Transport, key: key}
	client.state().http.Transport = probe
	traced := atomic.Bool{}
	ctx := httptrace.WithClientTrace(context.WithValue(test.Context(), key, "SYNTHETIC_PRIVATE_CONTEXT"), &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { traced.Store(true) }})
	if _, err := client.Get(ctx, fixture.pack); err != nil || probe.calls.Load() != 2 || probe.observed.Load() || traced.Load() {
		test.Fatal("caller values or callbacks crossed independent network kernel")
	}
	operation, err := client.Prepare(test.Context(), fixture.pack, 1, 1, []Reference{})
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	if fixture.authentication.Logout(test.Context(), fixture.session) != nil {
		test.Fatal("logout fixture")
	}
	if _, err := operation.Submit(test.Context()); !errors.Is(err, ErrDenied) || fixture.control.replace.Load() != 0 {
		test.Fatal("revoked owner session reached write")
	}
}

func TestActualTLSSessionExpiryBeforeMutation(test *testing.T) {
	fixture := newFixture(test)
	client, err := New(fixture.origin, fixture.session, fixture.transport)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	operation, err := client.Prepare(test.Context(), fixture.pack, 1, 1, []Reference{})
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	database, err := sql.Open("sqlite3", fixture.databasePath)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(test.Context(), `UPDATE sessions SET expires_at=?`, time.Now().Add(-time.Hour)); err != nil {
		test.Fatal("expiry fixture")
	}
	if _, err := operation.Submit(test.Context()); !errors.Is(err, ErrDenied) || fixture.control.replace.Load() != 0 {
		test.Fatal("expired owner session reached mutation")
	}
}

func TestTransportAuthorityAndOrigin(test *testing.T) {
	fixture := newFixture(test)
	for _, origin := range []string{"http://example.invalid", "https://u:pw@example.invalid", "https://example.invalid/path", "https://example.invalid?secret=1", "https://example.invalid/#x", "http://127.0.0.1:0"} {
		if client, err := New(origin, fixture.session, nil); err == nil {
			client.Close()
			test.Fatal("unsafe origin accepted")
		}
	}
	if client, err := New(fixture.origin, fixture.session, &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}); err == nil {
		client.Close()
		test.Fatal("insecure TLS override accepted")
	}
	settings := fixture.transport.Clone()
	settings.Proxy = func(*http.Request) (*url.URL, error) {
		test.Error("supplied proxy policy executed")
		return nil, ErrInvalid
	}
	client, err := New(fixture.origin, fixture.session, settings)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	if client.state().transport.Proxy != nil || client.state().http.CheckRedirect == nil || client.state().transport.TLSClientConfig.InsecureSkipVerify {
		test.Fatal("transport inherited ambient policy")
	}
	if _, err := client.Get(test.Context(), fixture.pack); err != nil {
		test.Fatal("verified TLS positive failed")
	}
	for _, representation := range []string{fmt.Sprintf("%+v", client), fmt.Sprintf("%#v", client)} {
		if strings.Contains(representation, fixture.session) || strings.Contains(representation, fixture.origin) {
			test.Fatal("opaque client leaked private state")
		}
	}
}

func TestAllFourteenMetadataTargetsRemainUnavailable(test *testing.T) {
	fixture := newFixture(test)
	refs := make([]Reference, 0, 14)
	for _, agent := range []string{"claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"} {
		asset, err := fixture.store.CreateConfigurationAsset(test.Context(), fixture.owner, state.ConfigurationAssetDraft{AgentID: agent, Kind: state.ConfigurationKindProvider, Name: "Synthetic metadata", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte("SYNTHETIC_PRIVATE_BODY_NOT_A_CONSUMER_FIXTURE"))})
		if err != nil {
			test.Fatal("opaque metadata fixture")
		}
		refs = append(refs, Reference{AssetID: asset.ID, ExpectedAssetRevision: 1, TargetID: agent + ".provider.user"})
	}
	client, err := New(fixture.origin, fixture.session, fixture.transport)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	operation, err := client.Prepare(test.Context(), fixture.pack, 1, 1, refs)
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	var review bytes.Buffer
	if operation.WriteSummary(test.Context(), &review) != nil || strings.Count(review.String(), "NativeUnavailable") != 14 || strings.Contains(review.String(), "SYNTHETIC_PRIVATE_BODY") {
		test.Fatal("metadata preview leaked body or promised native support")
	}
	result, err := operation.Submit(test.Context())
	if err != nil || len(result.Entries) != 14 {
		test.Fatal("fourteen metadata references refused")
	}
	for index, entry := range result.Entries {
		if entry.AssetID != refs[index].AssetID || entry.TargetID != refs[index].TargetID {
			test.Fatal("fourteen target ordering changed")
		}
	}
	service, err := packs.NewService(fixture.store, noArchives{}, bytes.Repeat([]byte{44}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer service.Close()
	share, err := service.CreateShare(test.Context(), fixture.owner, fixture.pack, nil)
	if err != nil {
		test.Fatal("real share issue")
	}
	shared, err := New(fixture.origin, share.Token.Reveal(), fixture.transport)
	if err == nil {
		defer shared.Close()
		if _, err := shared.Get(test.Context(), fixture.pack); err == nil {
			test.Fatal("share token admitted as owner session")
		}
	}
}
