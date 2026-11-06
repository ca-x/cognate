package desktop

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	libraryv1 "github.com/ca-x/cognate/gen/skillhub/library/v1"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

type selectionHTTPTransport func(*http.Request) (*http.Response, error)

func (transport selectionHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type selectionFixture struct {
	service                 *Service
	server                  *httptest.Server
	store                   *serverstate.Store
	library                 *library.Service
	auth                    *auth.Service
	owner, session, foreign string
	mutex                   sync.Mutex
	requests                map[string]int
	dropCommit              bool
	intercept               func(http.ResponseWriter, *http.Request) bool
	responseEdit            func(string, []byte) []byte
	upstream                http.RoundTripper
	downloads               atomic.Int32
}

func newSelectionFixture(test *testing.T) *selectionFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{37}, 32)
	defer clear(key)
	store, err := serverstate.Open(test.Context(), filepath.Join(directory, "selection.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if err := store.Close(); err != nil {
			test.Error(err)
		}
	})
	accounts, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	owner, err := accounts.Register(test.Context(), "selection-owner", []byte("synthetic-selection-password"))
	if err != nil {
		test.Fatal(err)
	}
	issued, err := accounts.Login(test.Context(), "selection-owner", []byte("synthetic-selection-password"))
	if err != nil {
		test.Fatal(err)
	}
	if _, err := accounts.Register(test.Context(), "selection-foreign", []byte("synthetic-foreign-password")); err != nil {
		test.Fatal(err)
	}
	foreign, err := accounts.Login(test.Context(), "selection-foreign", []byte("synthetic-foreign-password"))
	if err != nil {
		test.Fatal(err)
	}
	fixture := &selectionFixture{store: store, auth: accounts, owner: owner, session: issued.Reveal(), foreign: foreign.Reveal(), requests: make(map[string]int)}
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for name, value := range map[string]string{"source/skills/review/SKILL.md": "---\nname: review-guide\ndescription: synthetic guide\n---\nFull content.\n", "source/skills/review/.hidden": "synthetic-hidden", "source/skills/review/data.bin": "\x00\x01\xff"} {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(value))}); err != nil {
			test.Fatal(err)
		}
		if _, err := archive.Write([]byte(value)); err != nil {
			test.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.downloads.Add(1)
		if request.Host != "codeload.github.com" {
			test.Error("unexpected upstream host")
			writer.WriteHeader(400)
			return
		}
		_, _ = writer.Write(buffer.Bytes())
	}))
	test.Cleanup(upstream.Close)
	endpoint, err := url.Parse(upstream.URL)
	if err != nil {
		test.Fatal(err)
	}
	transport := selectionHTTPTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "codeload.github.com" {
			return nil, errors.New("synthetic upstream only")
		}
		clone := request.Clone(request.Context())
		clone.URL = request.URL.Clone()
		clone.Host = request.URL.Host
		clone.URL.Scheme, clone.URL.Host = endpoint.Scheme, endpoint.Host
		return upstream.Client().Transport.RoundTrip(clone)
	})
	fixture.upstream = transport
	cache, err := library.OpenArchiveCache(filepath.Join(directory, "archives"))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if err := cache.Close(); err != nil {
			test.Error(err)
		}
	})
	content, err := library.NewService(store, cache, repository.NewArchiveFetcher(&http.Client{Transport: transport, Timeout: 5 * time.Second}))
	if err != nil {
		test.Fatal(err)
	}
	fixture.library = content
	test.Cleanup(func() {
		if err := content.Close(); err != nil {
			test.Error(err)
		}
	})
	fixture.server = httptest.NewUnstartedServer(nil)
	origin := "https://" + fixture.server.Listener.Addr().String()
	selection, err := importer.NewSelectionService(store, content, accounts, origin)
	if err != nil {
		test.Fatal(err)
	}
	imports, err := httpapi.NewImportHandlerWithSelection(accounts, content, selection)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if err := imports.Close(); err != nil {
			test.Error(err)
		}
	})
	app := httpapi.NewWithOptions(accounts, httpapi.Options{Store: store, Library: content, Imports: imports, PublicOrigin: origin})
	fixture.server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mutex.Lock()
		fixture.requests[request.URL.Path]++
		intercept := fixture.intercept
		fixture.mutex.Unlock()
		if intercept != nil && intercept(writer, request) {
			return
		}
		app.ServeHTTP(writer, request)
	})
	fixture.server.StartTLS()
	test.Cleanup(fixture.server.Close)
	client := fixture.server.Client()
	base := client.Transport
	client.Transport = selectionHTTPTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != fixture.server.Listener.Addr().String() {
			return nil, errors.New("synthetic RPC only")
		}
		response, err := base.RoundTrip(request)
		if err != nil {
			return response, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || len(body) > 2<<20 {
			return nil, errors.New("bounded synthetic response")
		}
		if bytes.Contains(body, []byte("SYNTHETIC-PRIVATE-MARKER")) || bytes.Contains(body, []byte(fixture.session)) {
			test.Error("source or bearer escaped RPC response")
		}
		fixture.mutex.Lock()
		drop := fixture.dropCommit && strings.HasSuffix(request.URL.Path, "/CommitImportSelection")
		edit := fixture.responseEdit
		fixture.mutex.Unlock()
		if drop {
			return nil, errors.New("synthetic lost committed response")
		}
		if edit != nil && response.Header.Get("Content-Encoding") == "gzip" {
			reader, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				return nil, errors.New("synthetic gzip unavailable")
			}
			body, err = io.ReadAll(io.LimitReader(reader, (2<<20)+1))
			closeErr := reader.Close()
			if err != nil || closeErr != nil || len(body) > 2<<20 {
				return nil, errors.New("synthetic gzip over budget")
			}
			response.Header.Del("Content-Encoding")
		}
		if edit != nil {
			body = edit(request.URL.Path, body)
			response.ContentLength = int64(len(body))
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		return response, nil
	})
	fixture.service = &Service{Home: test.TempDir(), HTTP: client}
	return fixture
}

func (fixture *selectionFixture) count(method string) int {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	total := 0
	for path, count := range fixture.requests {
		if method == "" || strings.HasSuffix(path, "/"+method) {
			total += count
		}
	}
	return total
}

func TestSelectionNonterminalCommitKeepsReadOnlyBarrier(test *testing.T) {
	fixture := newSelectionFixture(test)
	model, pump := selectionLoadModel(test, fixture, 3)
	state := model.selectionState()
	selectionApproveRepositories(test, model)
	fixture.mutex.Lock()
	fixture.responseEdit = func(path string, body []byte) []byte {
		if !strings.HasSuffix(path, "/CommitImportSelection") {
			return body
		}
		var response importv1.CommitImportSelectionResponse
		if err := proto.Unmarshal(body, &response); err != nil {
			test.Error(err)
			return body
		}
		response.Receipt.Phase = importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COLLECTING
		response.Receipt.Revision = 2
		for _, outcome := range response.Receipt.RepositoryOutcomes {
			outcome.Code = importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_NOT_ATTEMPTED
			outcome.RepositoryId = nil
		}
		encoded, err := proto.Marshal(&response)
		if err != nil {
			test.Error(err)
			return body
		}
		return encoded
	}
	fixture.mutex.Unlock()
	model.CommitSelection()
	pump()
	if state.Result.Phase != 2 || state.Result.Observation != 2 || len(model.selectionBarriers) != 1 {
		test.Fatal("nonterminal valid receipt released the single-write barrier", state.Result)
	}
	state.UploadApproved = true
	model.PreviewSelection()
	pump()
	if fixture.count("PreviewImportSelection") != 1 {
		test.Fatal("nonterminal receipt allowed a fresh write preview")
	}
	model.QuerySelectionStatus()
	pump()
	if state.Result.Phase != 3 || len(model.selectionBarriers) != 0 {
		test.Fatal("explicit terminal status did not reconcile actual committed state")
	}
}

func TestSelectionRealExpiredSealedTicketCannotWrite(test *testing.T) {
	fixture := newSelectionFixture(test)
	fixture.mutex.Lock()
	fixture.responseEdit = func(path string, body []byte) []byte {
		if !strings.HasSuffix(path, "/PreviewImportSelection") {
			return body
		}
		var response importv1.PreviewImportSelectionResponse
		if err := proto.Unmarshal(body, &response); err != nil {
			test.Error(err)
			return body
		}
		response.Ticket = selectionExpiredTicket(test, fixture, response.Ticket)
		encoded, err := proto.Marshal(&response)
		if err != nil {
			test.Error(err)
			return body
		}
		return encoded
	}
	fixture.mutex.Unlock()
	model, pump := selectionLoadModel(test, fixture, 3)
	state := model.selectionState()
	selectionApproveRepositories(test, model)
	model.CommitSelection()
	pump()
	if fixture.count("CommitImportSelection") != 1 || fixture.downloads.Load() != 0 || len(model.selectionBarriers) != 1 {
		test.Fatal("authentic expired ticket wrote or discarded uncertain operation")
	}
	id, err := hex.DecodeString(state.OperationID)
	if err != nil {
		test.Fatal(err)
	}
	credential, err := fixture.auth.AuthenticateImport(test.Context(), fixture.session)
	if err != nil {
		test.Fatal(err)
	}
	var operation [16]byte
	copy(operation[:], id)
	if _, found, err := fixture.store.ReadImportReceipt(test.Context(), credential, operation); err != nil || found {
		test.Fatal("expired ticket reserved execution")
	}
	model.QuerySelectionStatus()
	pump()
	if state.Result.Observation != 1 || len(model.selectionBarriers) != 1 {
		test.Fatal("NotObserved was interpreted as future nonexecution")
	}
}

func selectionExpiredTicket(test *testing.T, fixture *selectionFixture, sealed []byte) []byte {
	test.Helper()
	master := bytes.Repeat([]byte{37}, 32)
	defer clear(master)
	fingerprint := sha256.Sum256(master)
	key, err := hkdf.Key(sha256.New, master, nil, "cognate/configuration-assets/v1", 32)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		test.Fatal(err)
	}
	encryption, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		test.Fatal(err)
	}
	associated := []byte("cognate/import-selection/v1/ticket")
	for _, value := range []string{base64.RawURLEncoding.EncodeToString(fingerprint[:]), fixture.owner, fixture.server.URL} {
		associated = binary.BigEndian.AppendUint64(associated, uint64(len(value)))
		associated = append(associated, value...)
	}
	plaintext, err := encryption.Open(nil, nil, sealed[4:], associated)
	if err != nil {
		test.Fatal("synthetic ticket authentication failed")
	}
	defer clear(plaintext)
	issued := time.Now().Add(-10 * time.Minute)
	binary.BigEndian.PutUint64(plaintext[2:10], uint64(issued.UnixMilli()))
	binary.BigEndian.PutUint64(plaintext[10:18], uint64(issued.Add(5*time.Minute).UnixMilli()))
	return encryption.Seal([]byte("CIP1"), nil, plaintext, associated)
}

func TestSelectionStaleRepositoryMetadataNeverBecomesBinding(test *testing.T) {
	fixture := newSelectionFixture(test)
	if _, err := fixture.library.Collect(test.Context(), fixture.owner, "example/toolkit", ""); err != nil {
		test.Fatal(err)
	}
	model, pump := selectionLoadModel(test, fixture, 3)
	fixture.mutex.Lock()
	fixture.responseEdit = func(path string, body []byte) []byte {
		if !strings.HasSuffix(path, "/GetRepository") {
			return body
		}
		var response libraryv1.GetRepositoryResponse
		if err := proto.Unmarshal(body, &response); err != nil {
			test.Error(err)
			return body
		}
		response.Repository.ArchiveHash = strings.Repeat("f", 64)
		encoded, err := proto.Marshal(&response)
		if err != nil {
			test.Error(err)
			return body
		}
		return encoded
	}
	fixture.mutex.Unlock()
	model.LoadSelectionBindings(0)
	pump()
	state := model.selectionState()
	if len(state.Bindings) != 0 || fixture.count("ListRepositories") != 1 || fixture.count("GetRepository") != 1 {
		test.Fatal("mixed catalog revisions yielded a content binding")
	}
	model.BindSelection(0)
	model.ChooseSelection(1, 0, true)
	if len(state.Choice.Skills) != 0 || fixture.count("CommitImportSelection") != 0 {
		test.Fatal("failed repository read fell back to guessed skill identity")
	}
}

func selectionExport(test *testing.T, source importv1.ImportSelectionSource, extraSQL ...string) string {
	test.Helper()
	var data []byte
	switch source {
	case importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24:
		schema, err := os.ReadFile("../server/importer/testdata/selection/ccswitch-schema24.sql")
		if err != nil {
			test.Fatal(err)
		}
		database, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			test.Fatal(err)
		}
		defer database.Close()
		connection, err := database.Conn(test.Context())
		if err != nil {
			test.Fatal(err)
		}
		defer connection.Close()
		if _, err := connection.ExecContext(test.Context(), string(schema)+strings.Join(extraSQL, "\n")); err != nil {
			test.Fatal(err)
		}
		if err := connection.Raw(func(driver any) error {
			serializer, ok := driver.(interface{ Serialize() ([]byte, error) })
			if !ok {
				return errors.New("serializer unavailable")
			}
			var err error
			data, err = serializer.Serialize()
			return err
		}); err != nil {
			test.Fatal(err)
		}
	case importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON:
		var err error
		data, err = os.ReadFile("../server/importer/testdata/selection/magpie.json")
		if err != nil {
			test.Fatal(err)
		}
	case importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_NPX_SKILLS_LOCK:
		data = []byte(`{"version":3,"skills":{"review-guide":{"sourceType":"github","source":"example/toolkit","skillPath":"skills/review","skillFolderHash":"1111111111111111111111111111111111111111"},"excluded":{"sourceType":"local","source":"/SYNTHETIC-PRIVATE-MARKER"}}}`)
	default:
		test.Fatal("unsupported fixture source")
	}
	defer clear(data)
	path := filepath.Join(test.TempDir(), "explicit-export")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		test.Fatal(err)
	}
	return path
}

func TestSelectionAuthorizationAndThreeSourcesPreview(test *testing.T) {
	for _, source := range []importv1.ImportSelectionSource{1, 2, 3} {
		test.Run(source.String(), func(test *testing.T) {
			fixture := newSelectionFixture(test)
			model, pump := modelFixture(test, fixture.service)
			model.SetOrigin(fixture.server.URL)
			model.session.set(fixture.session)
			state := model.selectionState()
			state.Path, state.Source = selectionExport(test, source), source
			model.PreviewSelection()
			pump()
			if fixture.count("") != 0 {
				test.Fatal("unapproved file triggered identity or upload")
			}
			state.UploadApproved = true
			model.PreviewSelection()
			pump()
			if state.Preview == nil || state.View == nil || fixture.count("PreviewImportSelection") != 1 || fixture.count("GetSession") == 0 {
				test.Fatal("real authorized preview failed", model.Status)
			}
			if len(state.Choice.Repositories)+len(state.Choice.Skills)+len(state.Choice.Configurations) != 0 || fixture.downloads.Load() != 0 || fixture.count("CommitImportSelection") != 0 {
				test.Fatal("preview selected or mutated assets")
			}
		})
	}
}

func selectionMakeReady(test *testing.T, model *Model, pump func(), source importv1.ImportSelectionSource) {
	test.Helper()
	state := model.selectionState()
	if source == 1 {
		for index := range state.Configurations {
			form := &state.Configurations[index]
			if form.Agent == "claude" {
				form.Claude = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY
			}
			if form.Agent == "gemini" {
				form.Gemini = configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_API_KEY
				form.Transport = configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_NATIVE_DEFAULT
			}
		}
		model.SelectionOptionsChanged()
		model.PreviewSelection()
		pump()
	}
	if state.Preview == nil {
		test.Fatal("ready options failed", model.Status)
	}
	model.LoadSelectionBindings(0)
	pump()
	if len(state.Bindings) != 1 {
		test.Fatal("same-repository complete metadata missing", model.Status)
	}
	model.BindSelection(0)
	if source != 2 {
		model.PreviewSelection()
		pump()
	}
	if state.Preview == nil || state.View.Skills[0].Status != 1 {
		test.Fatal("skill not ready after explicit content selection", model.Status)
	}
}

func TestSelectionRealAllSupportedIndependentAndMixedChoices(test *testing.T) {
	for _, source := range []importv1.ImportSelectionSource{1, 2, 3} {
		maximum := 3
		if source == 1 {
			maximum = 7
		}
		for mask := 1; mask <= maximum; mask++ {
			test.Run(fmt.Sprintf("source%d-mask%d", source, mask), func(test *testing.T) {
				fixture := newSelectionFixture(test)
				if _, err := fixture.library.Collect(test.Context(), fixture.owner, "example/toolkit", ""); err != nil {
					test.Fatal(err)
				}
				model, pump := selectionLoadModel(test, fixture, source)
				selectionMakeReady(test, model, pump, source)
				state := model.selectionState()
				wantRepos, wantSkills, wantConfigurations := 0, 0, 0
				if mask&1 != 0 {
					model.ChooseSelection(0, 0, true)
					wantRepos = 1
				}
				if mask&2 != 0 {
					model.ChooseSelection(1, 0, true)
					wantSkills = 1
				}
				if mask&4 != 0 {
					for index, candidate := range state.View.Configurations {
						if candidate.Status == 1 {
							model.ChooseSelection(2, index, true)
							wantConfigurations++
						}
					}
				}
				for index := range state.Notices {
					state.Notices[index].Checked = true
				}
				model.ReviewSelection()
				if state.Review == nil {
					test.Fatal("real selection review rejected", model.Status)
				}
				state.FinalApproved = true
				before := fixture.downloads.Load()
				model.CommitSelection()
				pump()
				if state.Result.Phase != 3 || state.Result.Repositories != wantRepos || state.Result.Skills != wantSkills || state.Result.Configurations != wantConfigurations || fixture.count("CommitImportSelection") != 1 {
					test.Fatal("actual receipt differs from explicit selection", state.Result, model.Status)
				}
				if fixture.downloads.Load() != before {
					test.Fatal("already collected references caused downloads")
				}
				assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
				if err != nil || len(assets) != wantConfigurations {
					test.Fatal("real private asset count differs")
				}
				for _, asset := range assets {
					edit, err := configuration.New(fixture.store).Edit(test.Context(), fixture.owner, asset.ID)
					if err != nil {
						test.Fatal(err)
					}
					for _, secret := range edit.Secrets {
						if secret.Present {
							test.Fatal("source credentials migrated without authorization")
						}
					}
				}
				foreign, err := fixture.store.ListConfigurationAssets(test.Context(), "foreign-owner")
				if err != nil || len(foreign) != 0 {
					test.Fatal("private assets escaped current owner")
				}
			})
		}
	}
}

func TestSelectionMissingRepositoryExplicitCollectThenRepreview(test *testing.T) {
	fixture := newSelectionFixture(test)
	model, pump := selectionLoadModel(test, fixture, 3)
	state := model.selectionState()
	model.LoadSelectionBindings(0)
	pump()
	if len(state.Bindings) != 0 || state.View.Skills[0].Status != 3 {
		test.Fatal("uncollected repository got an invented content pin")
	}
	selectionApproveRepositories(test, model)
	model.CommitSelection()
	pump()
	if state.Result.Phase != 3 || fixture.downloads.Load() != 1 {
		test.Fatal("explicit initial repository collection failed", model.Status)
	}
	state.UploadApproved = true
	model.PreviewSelection()
	pump()
	model.LoadSelectionBindings(0)
	pump()
	if len(state.Bindings) != 1 {
		test.Fatal("collected repository not visible after repreview")
	}
	model.BindSelection(0)
	model.PreviewSelection()
	pump()
	model.ChooseSelection(1, 0, true)
	for index := range state.Notices {
		state.Notices[index].Checked = true
	}
	model.ReviewSelection()
	state.FinalApproved = true
	model.CommitSelection()
	pump()
	if state.Result.Phase != 3 || state.Result.Skills != 1 || state.Result.Repositories != 0 || fixture.downloads.Load() != 1 || fixture.count("CommitImportSelection") != 2 {
		test.Fatal("second skill-only operation fetched or failed", model.Status)
	}
}

func TestSelectionExplicitFileLimitsAndUnsafeInputs(test *testing.T) {
	for _, scenario := range []string{"missing", "public", "symlink", "oversized", "snapshot-not-sqlite", "invalid-source"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newSelectionFixture(test)
			model, pump := modelFixture(test, fixture.service)
			model.SetOrigin(fixture.server.URL)
			model.session.set(fixture.session)
			state := model.selectionState()
			state.Source, state.Path, state.UploadApproved = 3, selectionExport(test, 3), true
			switch scenario {
			case "missing":
				state.Path += ".missing"
			case "public":
				if err := os.Chmod(state.Path, 0o644); err != nil {
					test.Fatal(err)
				}
			case "symlink":
				link := state.Path + ".link"
				if err := os.Symlink(state.Path, link); err != nil {
					test.Fatal(err)
				}
				state.Path = link
			case "oversized":
				if err := os.WriteFile(state.Path, bytes.Repeat([]byte{' '}, (1<<20)+1), 0o600); err != nil {
					test.Fatal(err)
				}
			case "snapshot-not-sqlite":
				state.Source = 1
			case "invalid-source":
				state.Source = 99
			}
			model.PreviewSelection()
			pump()
			if state.Preview != nil || fixture.downloads.Load() != 0 || fixture.count("CommitImportSelection") != 0 {
				test.Fatal("unsafe source yielded a preview or mutation")
			}
			wantUploads := 0
			if scenario == "snapshot-not-sqlite" {
				wantUploads = 1
			}
			if fixture.count("PreviewImportSelection") != wantUploads {
				test.Fatal("local invalid file uploaded")
			}
		})
	}
}

func TestSelectionActualAppTLSAllSourcesWithServerOnlyMoejs(test *testing.T) {
	fixture := newSelectionFixture(test)
	previous := http.DefaultTransport
	http.DefaultTransport = fixture.upstream
	test.Cleanup(func() { http.DefaultTransport = previous })
	seed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := seed.Certificate()
	privateKey, err := x509.MarshalPKCS8PrivateKey(seed.TLS.Certificates[0].PrivateKey)
	seed.Close()
	if err != nil {
		test.Fatal(err)
	}
	defer clear(privateKey)
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	master, certFile, keyFile := filepath.Join(directory, "master"), filepath.Join(directory, "cert.pem"), filepath.Join(directory, "tls.key")
	for path, data := range map[string][]byte{master: bytes.Repeat([]byte{83}, 32), certFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), keyFile: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey})} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			test.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = listener.Close() })
	origin := "https://" + listener.Addr().String()
	app, err := server.Open(test.Context(), server.Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: master, TLSCertificate: certFile, TLSKey: keyFile, PublicOrigin: origin, SourceRefreshInterval: new(time.Duration(0))})
	if err != nil {
		test.Fatal("real App open failed", err)
	}
	life, cancel := context.WithCancel(test.Context())
	served := make(chan error, 1)
	go func() { served <- app.Serve(life, listener) }()
	test.Cleanup(func() {
		cancel()
		select {
		case err := <-served:
			if err != nil {
				test.Error("actual App Serve failed", err)
			}
		case <-time.After(10 * time.Second):
			test.Error("actual App Serve did not join")
		}
		if err := app.Close(); err != nil {
			test.Error("actual App Close failed", err)
		}
	})
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}}
	test.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	authentication := authv1connect.NewAuthServiceClient(client, origin)
	for _, source := range []importv1.ImportSelectionSource{1, 2, 3} {
		test.Run(fmt.Sprintf("source%d", source), func(test *testing.T) {
			username := fmt.Sprintf("actual-app-source-%d", source)
			if _, err := authentication.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: username, Password: "synthetic-app-password"})); err != nil {
				test.Fatal(err)
			}
			service := &Service{Home: test.TempDir(), HTTP: client}
			model, pump := modelFixture(test, service)
			model.SetOrigin(origin)
			model.Username = username
			model.password.set("synthetic-app-password")
			model.Login()
			pump()
			if model.session.value() == "" {
				test.Fatal("actual App login failed", model.Status)
			}
			state := model.selectionState()
			state.Source, state.Path, state.UploadApproved = source, selectionExport(test, source), true
			model.PreviewSelection()
			pump()
			if state.Preview == nil {
				test.Fatal("actual App preview failed", model.Status)
			}
			selectionApproveRepositories(test, model)
			model.CommitSelection()
			pump()
			if state.Result.Phase != 3 {
				test.Fatal("actual App collection failed", model.Status)
			}
			state.UploadApproved = true
			model.PreviewSelection()
			pump()
			selectionMakeReady(test, model, pump, source)
			model.ChooseSelection(1, 0, true)
			want := 0
			if source == 1 {
				for index, candidate := range state.View.Configurations {
					if candidate.Status == 1 {
						model.ChooseSelection(2, index, true)
						want++
					}
				}
			}
			for index := range state.Notices {
				state.Notices[index].Checked = true
			}
			model.ReviewSelection()
			state.FinalApproved = true
			model.CommitSelection()
			pump()
			if state.Result.Phase != 3 || state.Result.Skills != 1 || state.Result.Repositories != 0 || state.Result.Configurations != want {
				test.Fatal("actual App mixed content/assets receipt failed", model.Status, state.Result)
			}
			model.QuerySelectionStatus()
			pump()
			if state.Result.Observation != 4 || state.Result.Phase != 3 {
				test.Fatal("actual App read-only status failed")
			}
			configurations := configurationv1connect.NewConfigurationServiceClient(client, origin)
			request := connect.NewRequest(&configurationv1.ListAssetsRequest{})
			request.Header().Set("Authorization", "Bearer "+model.session.value())
			assets, err := configurations.ListAssets(test.Context(), request)
			if err != nil || len(assets.Msg.Assets) != want {
				test.Fatal("actual App SQLite asset readback failed")
			}
			for _, asset := range assets.Msg.Assets {
				request := connect.NewRequest(&configurationv1.ReadDraftRequest{Id: asset.Id})
				request.Header().Set("Authorization", "Bearer "+model.session.value())
				draft, err := configurations.ReadDraft(test.Context(), request)
				if err != nil {
					test.Fatal("actual App draft readback failed", err)
				}
				for _, secret := range draft.Msg.View.Secrets {
					if secret.Present {
						test.Fatal("actual App imported source credential")
					}
				}
			}
		})
	}
	if fixture.downloads.Load() != 3 {
		test.Fatal("actual App did not perform exactly the three explicit source collections")
	}
}
