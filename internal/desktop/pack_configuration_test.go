package desktop

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/v1/packv1connect"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/packattachments"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"

	"github.com/egoist/mygo/ui"
)

type packConfigurationFixture struct {
	service                                                      *Service
	origin, owner, session, foreignSession, pack, asset, unknown string
	mutex                                                        sync.Mutex
	requests                                                     map[string]int
	drop, hold, holdWrite                                        bool
	arrived                                                      chan struct{}
	release, canceled                                            chan struct{}
	stop                                                         func()
	certificateFile                                              string
}

func newPackConfigurationFixture(test *testing.T, seeds ...func(*serverstate.Store, string, string, string)) *packConfigurationFixture {
	test.Helper()
	root := test.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		test.Fatal(err)
	}
	directory := filepath.Join(root, "data")
	keyFile := filepath.Join(root, "master.key")
	key, err := serverstate.InitializeMasterKey(test.Context(), keyFile, directory)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(key)
	store, err := serverstate.Open(test.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	owner, err := authentication.Register(test.Context(), "pack-owner", []byte("synthetic-pack-password"))
	if err != nil {
		test.Fatal(err)
	}
	login, err := authentication.Login(test.Context(), "pack-owner", []byte("synthetic-pack-password"))
	if err != nil {
		test.Fatal(err)
	}
	if _, err := authentication.Register(test.Context(), "pack-foreign", []byte("synthetic-foreign-password")); err != nil {
		test.Fatal(err)
	}
	foreign, err := authentication.Login(test.Context(), "pack-foreign", []byte("synthetic-foreign-password"))
	if err != nil {
		test.Fatal(err)
	}
	ref, err := repository.ParseReference("fixture/pack", "")
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
	pack, err := store.CreatePack(test.Context(), owner, serverstate.PackDraft{Name: "Owner Pack", Items: []serverstate.PackSelection{{SkillID: detail.Skills[0].ID}}})
	if err != nil {
		test.Fatal(err)
	}
	secret := configuration.NewSecret([]byte("synthetic-pack-asset-secret"))
	defer secret.Clear()
	asset, err := configuration.New(store).Create(test.Context(), owner, configuration.Draft{Name: "Provider metadata", Provider: configuration.ClaudeProvider{AuthMode: configuration.APIKey}}, []configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Replace, Replacement: secret}})
	if err != nil {
		test.Fatal(err)
	}
	unknown, err := store.CreateConfigurationAsset(test.Context(), owner, serverstate.ConfigurationAssetDraft{AgentID: "future-agent", Kind: serverstate.ConfigurationKindMCP, Name: "Unknown metadata", SchemaVersion: 7, Payload: serverstate.NewConfigurationPayload([]byte(`{"secret":"must stay encrypted"}`))})
	if err != nil {
		test.Fatal(err)
	}
	for _, seed := range seeds {
		seed(store, owner, pack.ID, asset.Metadata.ID)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	certificateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		test.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &certificateKey.PublicKey, certificateKey)
	if err != nil {
		test.Fatal(err)
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(certificateKey)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(encodedKey)
	certFile, tlsKeyFile := filepath.Join(root, "cert.pem"), filepath.Join(root, "tls.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(tlsKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}), 0600); err != nil {
		test.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	origin := "https://" + listener.Addr().String()
	app, err := server.Open(test.Context(), server.Config{DataDirectory: directory, MasterKeyFile: keyFile, TLSCertificate: certFile, TLSKey: tlsKeyFile, PublicOrigin: origin, SourceRefreshInterval: new(time.Duration(0))})
	if err != nil {
		listener.Close()
		test.Fatal(err)
	}
	life, cancel := context.WithCancel(context.Background())
	serve := make(chan error, 1)
	go func() { serve <- app.Serve(life, listener) }()
	fixture := &packConfigurationFixture{owner: owner, session: login.Reveal(), foreignSession: foreign.Reveal(), pack: pack.ID, asset: asset.Metadata.ID, unknown: unknown.ID, requests: map[string]int{}, arrived: make(chan struct{}, 8), release: make(chan struct{}), canceled: make(chan struct{}, 8)}
	pool := x509.NewCertPool()
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		test.Fatal(err)
	}
	pool.AddCert(certificate)
	target, err := url.Parse(origin)
	if err != nil {
		test.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, Proxy: nil}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.ModifyResponse = func(response *http.Response) error {
		fixture.mutex.Lock()
		drop, holdWrite := fixture.drop, fixture.holdWrite
		fixture.mutex.Unlock()
		if holdWrite && strings.HasSuffix(response.Request.URL.Path, "/ReplaceAttachments") && response.StatusCode == 200 {
			fixture.arrived <- struct{}{}
			select {
			case <-response.Request.Context().Done():
				return response.Request.Context().Err()
			case <-fixture.release:
			}
		}
		if drop && strings.HasSuffix(response.Request.URL.Path, "/ReplaceAttachments") && response.StatusCode == 200 {
			if err := response.Body.Close(); err != nil {
				return err
			}
			response.Body = io.NopCloser(bytes.NewReader([]byte{255}))
			response.ContentLength = 1
			response.Header.Set("Content-Length", "1")
		}
		return nil
	}
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mutex.Lock()
		fixture.requests[filepath.Base(request.URL.Path)]++
		hold := fixture.hold
		fixture.mutex.Unlock()
		if hold && strings.HasSuffix(request.URL.Path, "/GetAttachments") {
			if _, err := io.Copy(io.Discard, io.LimitReader(request.Body, 128<<10)); err != nil {
				return
			}
			if err := request.Body.Close(); err != nil {
				return
			}
			select {
			case fixture.arrived <- struct{}{}:
			default:
			}
			select {
			case <-request.Context().Done():
				fixture.canceled <- struct{}{}
			case <-fixture.release:
			}
			return
		}
		proxy.ServeHTTP(writer, request)
	}))
	fixture.origin, fixture.service = endpoint.URL, &Service{Home: test.TempDir(), HTTP: endpoint.Client()}
	fixture.certificateFile = filepath.Join(root, "proxy-cert.pem")
	if err := os.WriteFile(fixture.certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: endpoint.Certificate().Raw}), 0600); err != nil {
		test.Fatal(err)
	}
	var once sync.Once
	fixture.stop = func() {
		once.Do(func() {
			close(fixture.release)
			endpoint.Close()
			transport.CloseIdleConnections()
			cancel()
			if err := app.Close(); err != nil {
				test.Error("App.Close", err)
			}
			select {
			case err := <-serve:
				if err != nil {
					test.Error("Serve join", err)
				}
			case <-time.After(10 * time.Second):
				test.Error("Serve failed to join")
			}
			probe, err := net.Listen("tcp", listener.Addr().String())
			if err != nil {
				test.Error("listener not released", err)
			} else if err := probe.Close(); err != nil {
				test.Error(err)
			}
		})
	}
	test.Cleanup(fixture.stop)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request, err := http.NewRequestWithContext(test.Context(), http.MethodGet, origin+"/healthz", nil)
	if err != nil {
		test.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		test.Fatal("server readiness", err)
	}
	_, readError := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	closeError := response.Body.Close()
	if readError != nil || closeError != nil || response.StatusCode != http.StatusOK {
		test.Fatal("server not ready", readError, closeError, response.StatusCode)
	}
	return fixture
}

func (fixture *packConfigurationFixture) count(method string) int {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	return fixture.requests[method]
}

func TestPackConfigurationLoadsOwnerMetadataWithoutSecretReads(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	model, queue := configurationModelFixture(test, fixture.service)
	model.SetOrigin(fixture.origin)
	model.session.set(fixture.session)
	model.Page = 1
	tester := ui.NewTester(model.View, 920, 760)
	if !tester.HasText("读取所有者附件") {
		test.Fatal("no owner-only load action")
	}
	tester.Scroll(600, 450, 0, 450)
	click(test, tester, "读取所有者附件")
	if !model.Busy {
		test.Fatal("owner load click did not start a task")
	}
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if !tester.HasText("Owner Pack") || !tester.HasText("Unknown metadata") || !tester.HasText(fixture.owner) {
		test.Fatalf("missing actual owner, pack or unknown-schema metadata: status=%s loaded=%t assets=%d pack=%t unknown=%t owner=%t", model.Status, model.packConfigurationState().Loaded, len(model.packConfigurationState().Catalog.Assets), tester.HasText("Owner Pack"), tester.HasText("Unknown metadata"), tester.HasText(fixture.owner))
	}
	if fixture.count("ReadDraft") != 0 || fixture.count("GetAsset") != 0 || fixture.count("ReplaceAttachments") != 0 {
		test.Fatal("read crossed metadata boundary")
	}
}

func packConfigurationLoadModel(test *testing.T, fixture *packConfigurationFixture) (*Model, chan func()) {
	test.Helper()
	model, queue := configurationModelFixture(test, fixture.service)
	model.SetOrigin(fixture.origin)
	model.session.set(fixture.session)
	model.Page = 1
	model.LoadPackConfigurations()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if !model.packConfigurationState().Loaded {
		test.Fatal(model.Status)
	}
	return model, queue
}

func packConfigurationPrepareModel(test *testing.T, model *Model, queue chan func()) {
	test.Helper()
	model.PreparePackConfigurations()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if model.packConfigurationState().Review == nil {
		test.Fatal("review unavailable: ", model.Status)
	}
}

func packConfigurationAddProvider(test *testing.T, model *Model) {
	test.Helper()
	state := model.packConfigurationState()
	model.BeginPackConfigurations()
	for index, asset := range state.Catalog.Assets {
		if packConfigurationEligible(asset) {
			state.AssetSelected = index
			state.add()
			return
		}
	}
	test.Fatal("missing provider fixture")
}

func TestPackConfigurationExactConfirmationAndClear(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	model, queue := packConfigurationLoadModel(test, fixture)
	packConfigurationAddProvider(test, model)
	packConfigurationPrepareModel(test, model, queue)
	state := model.packConfigurationState()
	state.Confirmation = "not the exact confirmation"
	model.SubmitPackConfigurations()
	if model.Busy || fixture.count("ReplaceAttachments") != 0 {
		test.Fatal("implicit confirmation wrote")
	}
	state.Confirmation = state.Review.Confirmation
	model.SubmitPackConfigurations()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Blocked || state.dirty() || len(state.Snapshot.Entries) != 1 || state.Snapshot.PackRevision != 2 || state.Snapshot.ConfigurationRevision != 2 || fixture.count("ReplaceAttachments") != 1 {
		test.Fatal("exact confirmation failed one full dual CAS", model.Status)
	}
	model.BeginPackConfigurations()
	state.Draft = []packattachments.Reference{}
	packConfigurationPrepareModel(test, model, queue)
	if len(state.Review.References) != 0 || len(state.Review.Before.Entries) != 1 {
		test.Fatal("clear review lost old/new sets")
	}
	state.Confirmation = state.Review.Confirmation
	model.SubmitPackConfigurations()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Blocked || len(state.Snapshot.Entries) != 0 || fixture.count("ReplaceAttachments") != 2 {
		test.Fatal("explicit clear not committed", model.Status)
	}
	assertNoCredentialFiles(test, fixture.service.Home, fixture.session, "never read", "must stay encrypted")
}

func TestPackConfigurationUnknownIsSingleWriteAndReadOnlyReconciliation(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	model, queue := packConfigurationLoadModel(test, fixture)
	packConfigurationAddProvider(test, model)
	packConfigurationPrepareModel(test, model, queue)
	state := model.packConfigurationState()
	oldDraft := slices.Clone(state.Draft)
	fixture.mutex.Lock()
	fixture.drop = true
	fixture.mutex.Unlock()
	state.Confirmation = state.Review.Confirmation
	model.SubmitPackConfigurations()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if !state.Blocked || state.Review != nil || !strings.Contains(model.Status, "Unknown") || fixture.count("ReplaceAttachments") != 1 {
		test.Fatal("committed invalid receipt was not Unknown/single-write", model.Status)
	}
	model.SubmitPackConfigurations()
	model.PreparePackConfigurations()
	if model.Busy || fixture.count("ReplaceAttachments") != 1 {
		test.Fatal("Unknown replayed")
	}
	model.LoadPackConfigurations()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if !state.Blocked || !state.Loaded || state.Editing || state.Snapshot.PackRevision != 2 || len(state.Snapshot.Entries) != 1 || !slices.Equal(state.Draft, oldDraft) {
		test.Fatal("reread did not preserve blocked draft and actual committed state", model.Status)
	}
	model.PreparePackConfigurations()
	if state.Review != nil || fixture.count("ReplaceAttachments") != 1 {
		test.Fatal("reread silently reauthorized")
	}
	model.BeginPackConfigurations()
	if state.Blocked || !state.Editing || state.dirty() {
		test.Fatal("fresh edit did not use current readonly baseline")
	}
}

func TestPackConfigurationStaleExplicitUpgradeAndUnknownSchema(test *testing.T) {
	fixture := newPackConfigurationFixture(test, func(store *serverstate.Store, owner, pack, asset string) {
		if _, err := store.ReplacePackConfigurationAttachments(test.Context(), owner, pack, 1, 1, []serverstate.PackConfigurationReference{{AssetID: asset, AssetRevision: 1, TargetID: "claude.provider.user"}}); err != nil {
			test.Fatal(err)
		}
		if _, err := store.UpdateConfigurationAsset(test.Context(), owner, asset, 1, serverstate.ConfigurationAssetDraft{AgentID: "claude", Kind: serverstate.ConfigurationKindProvider, Name: "Provider metadata", SchemaVersion: 1, Payload: serverstate.NewConfigurationPayload([]byte(`{"fixture":"new encrypted data"}`))}); err != nil {
			test.Fatal(err)
		}
	})
	model, queue := packConfigurationLoadModel(test, fixture)
	state := model.packConfigurationState()
	if state.Snapshot.Entries[0].State != 2 {
		test.Fatal("stale state absent")
	}
	model.BeginPackConfigurations()
	if state.Draft[0].ExpectedAssetRevision != 1 {
		test.Fatal("pin silently upgraded")
	}
	for index, asset := range state.Catalog.Assets {
		if asset.ID == fixture.unknown {
			state.AssetSelected = index
			state.add()
		}
	}
	if len(state.Draft) != 1 {
		test.Fatal("unknown schema became attachable")
	}
	state.Selected = 0
	state.upgrade()
	if state.Draft[0].ExpectedAssetRevision != 2 {
		test.Fatal("explicit upgrade absent")
	}
	packConfigurationPrepareModel(test, model, queue)
	if state.Review.Before.Entries[0].AssetRevision != 1 || state.Review.References[0].ExpectedAssetRevision != 2 {
		test.Fatal("review lost actual stale-to-current difference")
	}
	state.Confirmation = state.Review.Confirmation
	model.SubmitPackConfigurations()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Blocked || state.Snapshot.Entries[0].State != 1 || fixture.count("ReplaceAttachments") != 1 {
		test.Fatal("explicit upgrade failed", model.Status)
	}
	if fixture.count("ReadDraft") != 0 || fixture.count("GetAsset") != 0 {
		test.Fatal("metadata editor read secrets")
	}
}

func TestPackConfigurationDualCASConflictsAndMissingPack(test *testing.T) {
	for _, scenario := range []string{"configuration-and-pack", "pack-only", "missing-pack"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newPackConfigurationFixture(test)
			model, queue := packConfigurationLoadModel(test, fixture)
			packConfigurationAddProvider(test, model)
			packConfigurationPrepareModel(test, model, queue)
			state := model.packConfigurationState()
			if scenario == "configuration-and-pack" {
				other, err := fixture.service.OpenPackConfigurations(fixture.origin, fixture.session)
				if err != nil {
					test.Fatal(err)
				}
				defer other.Close()
				if _, err := other.prepare(test.Context(), state.Snapshot, []packattachments.Reference{}); err != nil {
					test.Fatal(err)
				}
				other.private().mutex.Lock()
				confirmation := other.private().operation.Confirmation()
				other.private().mutex.Unlock()
				if _, err := other.submit(test.Context(), confirmation); err != nil {
					test.Fatal(err)
				}
			} else {
				rpc := packv1connect.NewPackServiceClient(fixture.service.client(fixture.origin), fixture.origin, configurationOptions()...)
				if scenario == "missing-pack" {
					request := connect.NewRequest(&packv1.DeletePackRequest{PackId: fixture.pack, ExpectedRevision: 1})
					request.Header().Set("Authorization", "Bearer "+fixture.session)
					if _, err := rpc.DeletePack(test.Context(), request); err != nil {
						test.Fatal(err)
					}
				} else {
					get := connect.NewRequest(&packv1.GetPackRequest{PackId: fixture.pack})
					get.Header().Set("Authorization", "Bearer "+fixture.session)
					response, err := rpc.GetPack(test.Context(), get)
					if err != nil {
						test.Fatal(err)
					}
					items := make([]*packv1.PackSelection, 0, len(response.Msg.Pack.Items))
					for _, item := range response.Msg.Pack.Items {
						items = append(items, &packv1.PackSelection{SkillId: item.SkillId, Slug: item.Slug})
					}
					request := connect.NewRequest(&packv1.UpdatePackRequest{PackId: fixture.pack, ExpectedRevision: 1, Draft: &packv1.PackDraft{Name: "Renamed", Items: items}})
					request.Header().Set("Authorization", "Bearer "+fixture.session)
					if _, err := rpc.UpdatePack(test.Context(), request); err != nil {
						test.Fatal(err)
					}
				}
			}
			before := fixture.count("ReplaceAttachments")
			state.Confirmation = state.Review.Confirmation
			model.SubmitPackConfigurations()
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if !state.Blocked || state.Review != nil || fixture.count("ReplaceAttachments") != before || len(state.Draft) != 1 {
				test.Fatal("late conflict/delete replayed or discarded draft", model.Status)
			}
		})
	}
}

func TestPackConfigurationOwnerIdentityAndCloseDrain(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	foreign, err := fixture.service.OpenPackConfigurations(fixture.origin, fixture.foreignSession)
	if err != nil {
		test.Fatal(err)
	}
	defer foreign.Close()
	catalog, err := foreign.catalog(test.Context())
	if err != nil || len(catalog.Packs) != 0 {
		test.Fatal("cross-owner list exposed packs")
	}
	if _, err := foreign.get(test.Context(), fixture.pack); err == nil {
		test.Fatal("cross-owner snapshot exposed")
	}
	model, queue := packConfigurationLoadModel(test, fixture)
	packConfigurationAddProvider(test, model)
	draft := slices.Clone(model.packConfigurationState().Draft)
	model.Page = 0
	model.Page = 1
	if !slices.Equal(model.packConfigurationState().Draft, draft) {
		test.Fatal("navigation lost dirty draft")
	}
	fixture.mutex.Lock()
	fixture.hold = true
	fixture.mutex.Unlock()
	model.LoadPackConfigurations()
	select {
	case <-fixture.arrived:
	case <-time.After(5 * time.Second):
		test.Fatal("real HTTP read never entered")
	}
	model.Close()
	joined := make(chan struct{})
	go func() { model.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		test.Fatal("Close/Wait did not drain held HTTP")
	}
	select {
	case <-fixture.canceled:
	case <-time.After(5 * time.Second):
		test.Fatal("held HTTP server did not observe cancellation")
	}
	for len(queue) > 0 {
		(<-queue)()
	}
	if model.packConfiguration != nil || model.session.value() != "" || model.Busy {
		test.Fatal("closed model retained owner or accepted stale completion")
	}
	if fixture.count("ReplaceAttachments") != 0 {
		test.Fatal("close wrote")
	}
}

func TestPackConfigurationCancelReviewAndIdentitySwitch(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	model, queue := packConfigurationLoadModel(test, fixture)
	packConfigurationAddProvider(test, model)
	packConfigurationPrepareModel(test, model, queue)
	state := model.packConfigurationState()
	draft := slices.Clone(state.Draft)
	model.packConfigurationClearReview()
	if state.Review != nil || !slices.Equal(state.Draft, draft) || fixture.count("ReplaceAttachments") != 0 {
		test.Fatal("cancel lost draft or wrote")
	}
	packConfigurationPrepareModel(test, model, queue)
	model.SetOrigin(fixture.origin)
	model.Wait()
	if model.packConfiguration != nil || model.session.value() != "" {
		test.Fatal("identity switch retained private review")
	}
	if fixture.count("ReplaceAttachments") != 0 {
		test.Fatal("identity switch submitted")
	}
}

func TestPackConfigurationFourteenMetadataTargetsBoundsAndOrder(test *testing.T) {
	fixture := newPackConfigurationFixture(test, func(store *serverstate.Store, owner, _, _ string) {
		for index, agent := range []string{"claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp", "claude", "codex", "gemini"} {
			if _, err := store.CreateConfigurationAsset(test.Context(), owner, serverstate.ConfigurationAssetDraft{AgentID: agent, Kind: serverstate.ConfigurationKindProvider, Name: fmt.Sprintf("Metadata %02d", index), SchemaVersion: 1, Payload: serverstate.NewConfigurationPayload([]byte(`{}`))}); err != nil {
				test.Fatal(err)
			}
		}
	})
	model, queue := packConfigurationLoadModel(test, fixture)
	state := model.packConfigurationState()
	model.BeginPackConfigurations()
	for index, asset := range state.Catalog.Assets {
		if packConfigurationEligible(asset) {
			state.AssetSelected = index
			state.add()
		}
	}
	if len(state.Draft) != 16 {
		test.Fatal("16 limit narrowed or exceeded")
	}
	state.Selected = 0
	first := state.Draft[0]
	state.move(1)
	if state.Selected != 1 || state.Draft[1] != first {
		test.Fatal("reorder lost selection")
	}
	packConfigurationPrepareModel(test, model, queue)
	refs := slices.Clone(state.Review.References)
	state.Confirmation = state.Review.Confirmation
	model.SubmitPackConfigurations()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Blocked || !slices.Equal(packConfigurationReferences(state.Snapshot), refs) || fixture.count("ReplaceAttachments") != 1 {
		test.Fatal("full order/14 finite metadata targets failed", model.Status)
	}
}

func TestPackConfigurationConnectionCloseRejectsFurtherWork(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	connection, err := fixture.service.OpenPackConfigurations(fixture.origin, fixture.session)
	if err != nil {
		test.Fatal(err)
	}
	connection.Close()
	connection.Close()
	if _, err := connection.catalog(test.Context()); !errors.Is(err, packattachments.ErrClosed) {
		test.Fatal("closed connection accepted read")
	}
	if _, err := connection.get(test.Context(), fixture.pack); !errors.Is(err, packattachments.ErrClosed) {
		test.Fatal("closed connection accepted get")
	}
}

func TestPackConfigurationLateAssetConflictAndWriteCancellation(test *testing.T) {
	for _, scenario := range []string{"late-asset", "cancel-after-commit"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newPackConfigurationFixture(test)
			model, queue := packConfigurationLoadModel(test, fixture)
			packConfigurationAddProvider(test, model)
			packConfigurationPrepareModel(test, model, queue)
			state := model.packConfigurationState()
			if scenario == "late-asset" {
				identity, err := fixture.service.ConfigurationIdentity(test.Context(), fixture.origin, fixture.session)
				if err != nil {
					test.Fatal(err)
				}
				defer identity.clear()
				var asset configurationAsset
				for _, entry := range state.Catalog.Assets {
					if entry.ID == fixture.asset {
						asset = entry
					}
				}
				form := configurationForm{Asset: asset, Name: "Concurrent update", Mode: agentconfig.ClaudeAPIKeyAuth, Actions: [2]configurationSecretAction{configurationKeep, configurationKeep}}
				if _, err := fixture.service.SaveConfiguration(test.Context(), identity, form.edit()); err != nil {
					test.Fatal(err)
				}
			} else {
				fixture.mutex.Lock()
				fixture.holdWrite = true
				fixture.mutex.Unlock()
			}
			state.Confirmation = state.Review.Confirmation
			model.SubmitPackConfigurations()
			if scenario == "late-asset" {
				configurationPump(test, model, queue, func() bool { return !model.Busy })
				if !state.Blocked || !strings.Contains(model.Status, "冲突") {
					test.Fatal("late asset CAS did not block", model.Status)
				}
			} else {
				select {
				case <-fixture.arrived:
				case <-time.After(5 * time.Second):
					test.Fatal("actual committed mutation not observed")
				}
				model.Cancel()
				model.Wait()
				for len(queue) > 0 {
					(<-queue)()
				}
				if !state.Blocked || state.Loaded || state.Review != nil || len(state.Draft) != 1 || state.Connection != nil {
					test.Fatal("canceled write remained replayable or lost draft")
				}
			}
			if fixture.count("ReplaceAttachments") != 1 {
				test.Fatal("late refusal or canceled mutation replayed")
			}
			reader, err := fixture.service.OpenPackConfigurations(fixture.origin, fixture.session)
			if err != nil {
				test.Fatal(err)
			}
			defer reader.Close()
			after, err := reader.get(test.Context(), fixture.pack)
			if err != nil {
				test.Fatal(err)
			}
			expected := int64(1)
			if scenario == "cancel-after-commit" {
				expected = 2
			}
			if after.PackRevision != expected || after.ConfigurationRevision != expected {
				test.Fatal("actual SQLite outcome not preserved")
			}
			model.SubmitPackConfigurations()
			if fixture.count("ReplaceAttachments") != 1 {
				test.Fatal("consumed review replayed")
			}
		})
	}
}

func packConfigurationFocus(test *testing.T, tester *ui.Tester, label string) {
	test.Helper()
	for count := 0; count < 80; count++ {
		if tester.Focused(label) {
			test.Logf("focus %s tabs=%d", label, count)
			return
		}
		tester.Key(0, ui.KeyTab)
	}
	test.Fatal("keyboard control not reachable: ", label)
}

func TestPackConfigurationFrontDoorKeyboardAndModal(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	model, queue := configurationModelFixture(test, fixture.service)
	tester := ui.NewTester(model.View, 920, 760)
	packConfigurationFocus(test, tester, "服务地址")
	tester.Type(fixture.origin)
	packConfigurationFocus(test, tester, "账号")
	tester.Key(0, ui.KeyEnter)
	packConfigurationFocus(test, tester, "用户名")
	tester.Type("pack-owner")
	packConfigurationFocus(test, tester, "密码")
	tester.Type("synthetic-pack-password")
	packConfigurationFocus(test, tester, "登录")
	tester.Key(0, ui.KeyEnter)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if model.session.value() == "" {
		test.Fatal("real UI login failed: ", model.Status)
	}
	packConfigurationFocus(test, tester, "Pack 订阅")
	tester.Key(0, ui.KeyEnter)
	packConfigurationFocus(test, tester, "读取所有者附件")
	tester.Key(0, ui.KeyEnter)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	state := model.packConfigurationState()
	if !state.Loaded {
		test.Fatal("keyboard owner read failed: ", model.Status)
	}
	packConfigurationFocus(test, tester, "放弃旧草稿，重新编辑")
	tester.Key(0, ui.KeyEnter)
	packConfigurationFocus(test, tester, "Provider metadata")
	tester.Key(0, ui.KeyEnter)
	if state.AssetSelected < 0 || !packConfigurationEligible(state.Catalog.Assets[state.AssetSelected]) {
		test.Fatalf("keyboard metadata selection failed: selected=%d assets=%v", state.AssetSelected, state.Catalog.Assets)
	}
	packConfigurationFocus(test, tester, "添加所选资产引用")
	tester.Key(0, ui.KeyEnter)
	packConfigurationFocus(test, tester, "审阅附件替换")
	tester.Key(0, ui.KeyEnter)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if state.Review == nil {
		test.Fatal("keyboard review missing: ", model.Status)
	}
	packConfigurationFocus(test, tester, "本次附件确认")
	tester.Type(state.Review.Confirmation)
	packConfigurationFocus(test, tester, "确认并替换一次附件")
	tester.Key(0, ui.KeyEnter)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if state.Blocked || len(state.Snapshot.Entries) != 1 || fixture.count("ReplaceAttachments") != 1 {
		test.Fatal("keyboard final confirmation failed", model.Status)
	}
	model.BeginPackConfigurations()
	state.Draft = []packattachments.Reference{}
	tester.Frame()
	packConfigurationFocus(test, tester, "审阅附件替换")
	tester.Key(0, ui.KeyEnter)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	if state.Review != nil || !state.dirty() || fixture.count("ReplaceAttachments") != 1 {
		test.Fatal("Escape lost draft or submitted")
	}
	if !tester.Focused("审阅附件替换") {
		test.Fatal("Escape did not restore review-trigger focus")
	}
}

func TestPackConfigurationReviewBindsMetadataAndGeneration(test *testing.T) {
	for _, change := range []string{"owner", "snapshot", "draft", "generation", "expiry"} {
		test.Run(change, func(test *testing.T) {
			fixture := newPackConfigurationFixture(test)
			model, queue := packConfigurationLoadModel(test, fixture)
			packConfigurationAddProvider(test, model)
			packConfigurationPrepareModel(test, model, queue)
			state := model.packConfigurationState()
			state.Confirmation = state.Review.Confirmation
			switch change {
			case "owner":
				state.Review.OwnerID = "different-owner"
			case "snapshot":
				state.Snapshot.Digest[0] ^= 1
			case "draft":
				state.Draft[0].ExpectedAssetRevision++
			case "generation":
				model.generation++
			case "expiry":
				state.Review.Expires = time.Now().Add(-time.Second)
			}
			model.SubmitPackConfigurations()
			if model.Busy || !state.Blocked || state.Review != nil || fixture.count("ReplaceAttachments") != 0 {
				test.Fatal("changed review was reusable")
			}
		})
	}
}

func TestPackConfigurationAppearancePreservesDraftAndScroll(test *testing.T) {
	fixture := newPackConfigurationFixture(test)
	model, _ := packConfigurationLoadModel(test, fixture)
	packConfigurationAddProvider(test, model)
	state := model.packConfigurationState()
	draft := slices.Clone(state.Draft)
	for _, width := range []int{640, 920} {
		for _, dark := range []bool{false, true} {
			for _, scale := range []float32{1, 2} {
				tester := ui.NewTester(model.View, width, 760)
				tester.SetDark(dark)
				tester.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: scale})
				for attempts := 0; !nativeShellControlVisible(tester, "审阅附件替换", nativePages[1].heading); attempts++ {
					if attempts >= 128 {
						test.Fatal("attachment action unreachable by bounded real scroll")
					}
					title, _ := tester.Find(nativePages[1].heading)
					footer, _ := tester.Find("取消任务")
					bounds, _ := tester.Find("审阅附件替换")
					delta := float32(100)
					if bounds.Y < title.Y+title.H {
						delta = -100
					}
					tester.Scroll(float32(width)/2, (title.Y+title.H+footer.Y)/2, 0, delta)
					tester.Frame()
				}
				if !slices.Equal(state.Draft, draft) {
					test.Fatal("appearance change lost draft")
				}
				if directory := os.Getenv("COGNATE_PACK_UI_EVIDENCE"); directory != "" {
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("pack-width%d-dark%t-text%.0f.png", width, dark, scale)))
					if err != nil {
						test.Fatal(err)
					}
					if err := png.Encode(file, tester.Image()); err != nil {
						test.Fatal(err)
					}
					if err := file.Close(); err != nil {
						test.Fatal(err)
					}
				}
			}
		}
	}
}

func TestPackConfigurationNativeFrontDoor(test *testing.T) {
	driver, binary, directory := os.Getenv("COGNATE_PACK_NATIVE_DRIVER"), os.Getenv("COGNATE_PACK_NATIVE_BINARY"), os.Getenv("COGNATE_PACK_NATIVE_EVIDENCE")
	if driver == "" || binary == "" || directory == "" {
		test.Skip("explicit isolated ordinary-binary native frontdoor gate")
	}
	if !filepath.IsAbs(driver) || !filepath.IsAbs(binary) || !filepath.IsAbs(directory) {
		test.Fatal("native gate needs private absolute paths")
	}
	fixture := newPackConfigurationFixture(test)
	home, runtimeDirectory := test.TempDir(), test.TempDir()
	if err := os.Chmod(runtimeDirectory, 0700); err != nil {
		test.Fatal(err)
	}
	logFile, err := os.OpenFile(filepath.Join(directory, "native-driver.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 200*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/cage", "--", "/bin/sh", driver)
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "XDG_DATA_HOME=" + filepath.Join(home, ".local/share"), "XDG_RUNTIME_DIR=" + runtimeDirectory, "WLR_BACKENDS=headless", "WLR_RENDERER=pixman", "LIBGL_ALWAYS_SOFTWARE=1", "GALLIUM_DRIVER=llvmpipe", "LP_NUM_THREADS=2", "LANG=C.UTF-8", "SSL_CERT_FILE=" + fixture.certificateFile, "SSL_CERT_DIR=" + home, "PACK_ORIGIN=" + fixture.origin, "PACK_BINARY=" + binary, "PACK_EVIDENCE=" + directory}
	command.Stdout, command.Stderr = logFile, logFile
	runError := command.Run()
	if err := logFile.Close(); err != nil {
		test.Error(err)
	}
	if runError != nil {
		test.Fatal("ordinary native runner/Wait failed", runError)
	}
	connection, err := fixture.service.OpenPackConfigurations(fixture.origin, fixture.session)
	if err != nil {
		test.Fatal(err)
	}
	defer connection.Close()
	snapshot, err := connection.get(test.Context(), fixture.pack)
	if err != nil || len(snapshot.Entries) != 1 || snapshot.Entries[0].AssetID != fixture.asset || fixture.count("ReplaceAttachments") != 1 || fixture.count("ReadDraft") != 0 {
		test.Fatal("native frontdoor did not commit exactly one expected metadata set")
	}
	assertNoCredentialFiles(test, home, fixture.session, "never read", "must stay encrypted")
	test.Log("ordinary binary login → owner metadata → explicit one-write receipt; child Wait joined")
}

func TestPackConfigurationOwnerEntryPreservesSubscriptions(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
	model.Page = 1
	tester := ui.NewTester(model.View, 920, 760)
	if !tester.HasText("所有者配置附件") {
		test.Fatal("Pack page is missing the independent owner attachment editor")
	}
	if !tester.HasText("刷新订阅") || !tester.HasText("启用后台同步") {
		test.Fatal("owner editor replaced existing skills subscription controls")
	}
	if !tester.HasText("附件仅元数据，不授配置权限，不应用设备。") {
		test.Fatal("metadata editor omits the authorization and application boundary")
	}
}
