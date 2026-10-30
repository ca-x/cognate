package importer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
)

type selectionServiceFixture struct {
	store          *state.Store
	authentication *auth.Service
	content        *library.Service
	service        *SelectionService
	bearer         string
	owner          string
	counts         *selectionHTTPCounts
	path           string
}

type selectionHTTPCounts struct {
	mutex sync.Mutex
	paths []string
	hook  func(http.ResponseWriter, *http.Request) bool
}

func (counts *selectionHTTPCounts) size() int {
	counts.mutex.Lock()
	defer counts.mutex.Unlock()
	return len(counts.paths)
}

type selectionTransport func(*http.Request) (*http.Response, error)

func (transport selectionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func selectionTLSFetcher(test *testing.T) (*repository.ArchiveFetcher, *selectionHTTPCounts) {
	test.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	contents := []byte("---\nname: review-guide\ndescription: synthetic review\n---\nSynthetic skill.\n")
	if err := archive.WriteHeader(&tar.Header{Name: "source/skills/review/SKILL.md", Mode: 0o644, Size: int64(len(contents))}); err != nil {
		test.Fatal(err)
	}
	if _, err := archive.Write(contents); err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	data := bytes.Clone(buffer.Bytes())
	counts := new(selectionHTTPCounts)
	server := httptest.NewTLSServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
		counts.mutex.Lock()
		counts.paths = append(counts.paths, request.URL.Path)
		hook := counts.hook
		counts.mutex.Unlock()
		if hook != nil && hook(output, request) {
			return
		}
		if request.Host == "api.github.com" {
			if strings.Contains(request.URL.Path, "/tarball") {
				output.Header().Set("Location", "https://codeload.github.com/example/private/tar.gz/HEAD?cap=synthetic-capability")
				output.WriteHeader(302)
				return
			}
			_, _ = io.WriteString(output, `{"id":42,"node_id":"SYNTHETIC-NODE","full_name":"example/private","private":true,"default_branch":"main","permissions":{"pull":true}}`)
			return
		}
		_, _ = output.Write(data)
	}))
	test.Cleanup(server.Close)
	endpoint, _ := url.Parse(server.URL)
	transport := selectionTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.github.com" && request.URL.Host != "codeload.github.com" {
			return nil, errors.New("fixture forbids external HTTP")
		}
		clone := request.Clone(request.Context())
		clone.URL = request.URL.Clone()
		clone.Host = request.URL.Host
		clone.URL.Scheme = endpoint.Scheme
		clone.URL.Host = endpoint.Host
		return server.Client().Transport.RoundTrip(clone)
	})
	return repository.NewArchiveFetcher(&http.Client{Transport: transport}), counts
}

func newSelectionServiceFixture(test *testing.T) selectionServiceFixture {
	test.Helper()
	key := bytes.Repeat([]byte{81}, 32)
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "objects"))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = cache.Close() })
	fetcher, counts := selectionTLSFetcher(test)
	content, err := library.NewService(store, cache, fetcher)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = content.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	owner, err := authentication.Register(test.Context(), "import-owner", []byte("synthetic-password-only"))
	if err != nil {
		test.Fatal(err)
	}
	token, err := authentication.Login(test.Context(), "import-owner", []byte("synthetic-password-only"))
	if err != nil {
		test.Fatal(err)
	}
	service, err := NewSelectionService(store, content, authentication, "https://cognate.invalid")
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = service.Close() })
	return selectionServiceFixture{store, authentication, content, service, token.Reveal(), owner, counts, filepath.Join(directory, "server.db")}
}

func selectionServiceSnapshot(test *testing.T, statements string) []byte {
	test.Helper()
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
	if _, err := connection.ExecContext(test.Context(), statements); err != nil {
		test.Fatal(err)
	}
	var raw []byte
	err = connection.Raw(func(driver any) error {
		serializer, ok := driver.(interface{ Serialize() ([]byte, error) })
		if !ok {
			return errors.New("SQLite serializer unavailable")
		}
		var err error
		raw, err = serializer.Serialize()
		return err
	})
	if err != nil {
		test.Fatal(err)
	}
	return raw
}

func selectionServiceSource(test *testing.T, existing state.CollectedRepository) ([]byte, SelectionOptions) {
	test.Helper()
	statements, err := os.ReadFile("testdata/selection/ccswitch-schema24.sql")
	if err != nil {
		test.Fatal(err)
	}
	raw := selectionServiceSnapshot(test, string(statements)+`UPDATE skills SET repo_name='existing';`)
	projection, err := ProjectSelection(test.Context(), SelectionCCSwitch24, raw)
	if err != nil {
		test.Fatal(err)
	}
	defer projection.Destroy()
	view, err := projection.View()
	if err != nil {
		test.Fatal(err)
	}
	options := SelectionOptions{}
	for _, candidate := range view.Configurations {
		if candidate.AgentID == "claude" {
			options.Candidates = append(options.Candidates, SelectionCandidateOptions{CandidateID: candidate.ID, ClaudeAuthMode: new(configuration.APIKey)})
		}
		if candidate.AgentID == "gemini" {
			options.Candidates = append(options.Candidates, SelectionCandidateOptions{CandidateID: candidate.ID, GeminiKeyTransport: new(agentconfig.GeminiBearer)})
		}
	}
	if len(existing.Skills) != 1 {
		test.Fatal("real fixture did not scan a skill")
	}
	skill := existing.Skills[0]
	options.Candidates = append(options.Candidates, SelectionCandidateOptions{CandidateID: view.Skills[0].ID, SkillBinding: &SelectionSkillBinding{Repository: existing.NameWithOwner, Branch: existing.Branch, RepositoryID: existing.ID, ArchiveHash: existing.ArchiveHash, SkillID: skill.ID, Path: skill.Path, ContentHash: skill.ContentHash}})
	return raw, options
}

func selectionServicePreview(test *testing.T, fixture selectionServiceFixture, source SelectionSource, raw []byte, options SelectionOptions) SelectionPreview {
	test.Helper()
	call, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	defer call.Release()
	preview, err := fixture.service.Preview(call, source, raw, options)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(preview.Destroy)
	return preview
}

func selectionServiceCommit(test *testing.T, fixture selectionServiceFixture, input SelectionCommitInput) (state.ImportReceiptView, error) {
	test.Helper()
	request, err := NewSelectionCommitRequest(input)
	if err != nil {
		return state.ImportReceiptView{}, err
	}
	defer request.Destroy()
	call, err := fixture.service.Begin(test.Context(), SelectionCommitMethod, fixture.bearer)
	if err != nil {
		return state.ImportReceiptView{}, err
	}
	defer call.Release()
	return fixture.service.Commit(call, request)
}

func TestSelectionCommitSevenCombinations(test *testing.T) {
	for mask := range 8 {
		test.Run(fmt.Sprint(mask), func(test *testing.T) {
			fixture := newSelectionServiceFixture(test)
			existing, err := fixture.content.Collect(test.Context(), fixture.owner, "example/existing", "")
			if err != nil {
				test.Fatal(err)
			}
			existing, err = fixture.store.CollectedRepository(test.Context(), fixture.owner, existing.ID)
			if err != nil {
				test.Fatal(err)
			}
			raw, options := selectionServiceSource(test, existing)
			preview := selectionServicePreview(test, fixture, SelectionCCSwitch24, raw, options)
			view, err := preview.View()
			if err != nil {
				test.Fatal(err)
			}
			input := SelectionCommitInput{Ticket: preview.RevealTicket(), Source: SelectionCCSwitch24, SourceData: raw, Options: options, OperationID: [16]byte{byte(mask + 1)}}
			if mask&1 != 0 {
				input.RepositoryIDs = []string{view.Repositories[0].ID}
			}
			if mask&2 != 0 {
				if view.Skills[0].Status != SelectionCandidateReady {
					test.Fatal("existing exact binding not ready", view.Skills[0].Status)
				}
				input.SkillIDs = []string{view.Skills[0].ID}
			}
			if mask&4 != 0 {
				for _, candidate := range view.Configurations {
					if candidate.Status == SelectionCandidateReady {
						input.ConfigurationIDs = append(input.ConfigurationIDs, candidate.ID)
					}
				}
				if len(input.ConfigurationIDs) != 3 {
					test.Fatal("typed providers missing")
				}
			}
			for _, notice := range selectionAllNotices(view) {
				input.AckNoticeIDs = append(input.AckNoticeIDs, notice.ID)
			}
			before := fixture.counts.size()
			receipt, err := selectionServiceCommit(test, fixture, input)
			if mask == 0 {
				if err == nil {
					test.Fatal("000 accepted")
				}
				credential, err := fixture.authentication.AuthenticateImport(test.Context(), fixture.bearer)
				if err != nil {
					test.Fatal(err)
				}
				_, found, err := fixture.store.ReadImportReceipt(test.Context(), credential, input.OperationID)
				if err != nil || found {
					test.Fatal("000 wrote receipt")
				}
				return
			}
			if err != nil || receipt.Phase != state.ImportCompleted {
				test.Fatal("actual selection commit failed", err, receipt.Phase)
			}
			if len(receipt.CreatedAssets) != len(input.ConfigurationIDs) || len(receipt.SelectedSkills) != len(input.SkillIDs) || len(receipt.RepositoryOutcomes) != len(input.RepositoryIDs) {
				test.Fatal("selection was not exact")
			}
			if mask&1 == 0 && fixture.counts.size() != before {
				test.Fatal("unselected repository fetched or skill-only Collect invoked")
			}
			if mask&1 != 0 && fixture.counts.size() != before+1 {
				test.Fatal("selected repository did not fetch once")
			}
			assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
			if err != nil || len(assets) != len(input.ConfigurationIDs) {
				test.Fatal("asset batch missing", err)
			}
			for _, asset := range assets {
				edit, err := configuration.New(fixture.store).Edit(test.Context(), fixture.owner, asset.ID)
				if err != nil {
					test.Fatal(err)
				}
				var expected *configuration.Draft
				for _, candidate := range view.Configurations {
					if candidate.KnownAgentID == asset.AgentID {
						expected = candidate.Draft
					}
				}
				actual := configuration.Draft{Name: edit.Metadata.Name, Provider: edit.Provider, CodexProvider: edit.CodexProvider, GeminiProvider: edit.GeminiProvider}
				if expected == nil || !reflect.DeepEqual(actual, *expected) {
					test.Fatal("real envelope/readback changed typed fields")
				}
				for _, slot := range edit.Secrets {
					if slot.Present {
						test.Fatal("source credential migrated into asset")
					}
				}
			}
			duplicate, err := selectionServiceCommit(test, fixture, input)
			if err != nil || duplicate.Revision != receipt.Revision || fixture.counts.size() != before+len(input.RepositoryIDs) {
				test.Fatal("duplicate executed again", err)
			}
			for _, path := range []string{fixture.path, fixture.path + "-wal"} {
				encoded, err := os.ReadFile(path)
				if err == nil && bytes.Contains(encoded, []byte("SYNTHETIC-PRIVATE-MARKER")) {
					test.Fatal("source secret persisted as plaintext")
				}
				clear(encoded)
			}
		})
	}
}

func selectionServiceFullInput(test *testing.T, fixture selectionServiceFixture) (SelectionCommitInput, SelectionPreviewView) {
	test.Helper()
	existing, err := fixture.content.Collect(test.Context(), fixture.owner, "example/existing", "")
	if err != nil {
		test.Fatal(err)
	}
	existing, err = fixture.store.CollectedRepository(test.Context(), fixture.owner, existing.ID)
	if err != nil {
		test.Fatal(err)
	}
	raw, options := selectionServiceSource(test, existing)
	preview := selectionServicePreview(test, fixture, SelectionCCSwitch24, raw, options)
	view, err := preview.View()
	if err != nil {
		test.Fatal(err)
	}
	input := SelectionCommitInput{Ticket: preview.RevealTicket(), Source: SelectionCCSwitch24, SourceData: raw, Options: options, OperationID: [16]byte{77}, RepositoryIDs: []string{view.Repositories[0].ID}, SkillIDs: []string{view.Skills[0].ID}}
	for _, candidate := range view.Configurations {
		if candidate.Status == SelectionCandidateReady {
			input.ConfigurationIDs = append(input.ConfigurationIDs, candidate.ID)
		}
	}
	for _, notice := range selectionAllNotices(view) {
		input.AckNoticeIDs = append(input.AckNoticeIDs, notice.ID)
	}
	return input, view
}

func selectionAssertNoReservation(test *testing.T, fixture selectionServiceFixture, operation [16]byte) {
	test.Helper()
	credential, err := fixture.authentication.AuthenticateImport(test.Context(), fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	_, found, err := fixture.store.ReadImportReceipt(test.Context(), credential, operation)
	if err != nil || found {
		test.Fatal("stale request reserved a receipt", err)
	}
}

func TestSelectionCommitStaleBeforeReservation(test *testing.T) {
	for _, variant := range []string{"raw", "options", "ticket", "source", "ack", "id", "duplicate", "name-conflict", "pins", "binding", "cross-owner", "cross-origin"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newSelectionServiceFixture(test)
			input, view := selectionServiceFullInput(test, fixture)
			before := fixture.counts.size()
			switch variant {
			case "raw":
				input.SourceData = bytes.Clone(input.SourceData)
				input.SourceData[len(input.SourceData)-1] ^= 1
			case "options":
				input.Options.Candidates[0].Name = new("Different exact name")
			case "ticket":
				input.Ticket[100] ^= 1
			case "source":
				input.Source = SelectionMagpie
			case "ack":
				input.AckNoticeIDs = input.AckNoticeIDs[1:]
			case "id":
				input.RepositoryIDs = []string{strings.Repeat("f", 64)}
			case "duplicate":
				input.SkillIDs = append(input.SkillIDs, input.SkillIDs[0])
			case "name-conflict":
				for _, candidate := range view.Configurations {
					if candidate.Draft != nil {
						if _, err := configuration.New(fixture.store).Create(test.Context(), fixture.owner, *candidate.Draft, nil); err != nil {
							test.Fatal(err)
						}
						break
					}
				}
			case "pins":
				bound := input.Options.Candidates[len(input.Options.Candidates)-1].SkillBinding
				_, err := fixture.store.CollectRepository(test.Context(), fixture.owner, repository.Reference{NameWithOwner: bound.Repository, Branch: bound.Branch}, strings.Repeat("e", 64), nil)
				if err == nil {
					test.Fatal("invalid pin fixture unexpectedly accepted")
				}
				bound.ArchiveHash = strings.Repeat("e", 64)
			case "binding":
				input.Options.Candidates[len(input.Options.Candidates)-1].SkillBinding.Path = "review"
			case "cross-owner":
				if _, err := fixture.authentication.Register(test.Context(), "other-owner", []byte("synthetic-other-password")); err != nil {
					test.Fatal(err)
				}
				token, err := fixture.authentication.Login(test.Context(), "other-owner", []byte("synthetic-other-password"))
				if err != nil {
					test.Fatal(err)
				}
				fixture.bearer = token.Reveal()
			case "cross-origin":
				other, err := NewSelectionService(fixture.store, fixture.content, fixture.authentication, "https://other.invalid")
				if err != nil {
					test.Fatal(err)
				}
				defer other.Close()
				fixture.service = other
			}
			if _, err := selectionServiceCommit(test, fixture, input); err == nil {
				test.Fatal("stale/foreign input accepted")
			}
			selectionAssertNoReservation(test, fixture, input.OperationID)
			if fixture.counts.size() != before {
				test.Fatal("stale selection downloaded a repository")
			}
		})
	}
}

func TestSelectionCommitReceiptIdempotencyAndReopen(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	input, _ := selectionServiceFullInput(test, fixture)
	first, err := selectionServiceCommit(test, fixture, input)
	if err != nil {
		test.Fatal(err)
	}
	before := fixture.counts.size()
	changed := input
	changed.AckNoticeIDs = slicesCloneStrings(input.AckNoticeIDs)
	changed.AckNoticeIDs[0] = strings.Repeat("f", 64)
	if _, err := selectionServiceCommit(test, fixture, changed); !errors.Is(err, state.ErrImportConflict) {
		test.Fatal("same op different input did not conflict", err)
	}
	changed = input
	changed.OperationID = [16]byte{78}
	if _, err := selectionServiceCommit(test, fixture, changed); err == nil {
		test.Fatal("ticket replayed for new operation")
	}
	if fixture.counts.size() != before {
		test.Fatal("receipt replay downloaded")
	}
	if err := fixture.service.Close(); err != nil {
		test.Fatal(err)
	}
	if err := fixture.content.Close(); err != nil {
		test.Fatal(err)
	}
	if err := fixture.store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := state.Open(test.Context(), fixture.path, bytes.Repeat([]byte{81}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	authentication, err := auth.NewService(reopened, bytes.Repeat([]byte{81}, 32))
	if err != nil {
		test.Fatal(err)
	}
	cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "new-empty-cache"))
	if err != nil {
		test.Fatal(err)
	}
	defer cache.Close()
	fetcher, _ := selectionTLSFetcher(test)
	content, err := library.NewService(reopened, cache, fetcher)
	if err != nil {
		test.Fatal(err)
	}
	defer content.Close()
	service, err := NewSelectionService(reopened, content, authentication, "https://cognate.invalid")
	if err != nil {
		test.Fatal(err)
	}
	defer service.Close()
	fixture.store, fixture.content, fixture.authentication, fixture.service = reopened, content, authentication, service
	duplicate, err := selectionServiceCommit(test, fixture, input)
	if err != nil || !reflect.DeepEqual(first, duplicate) {
		test.Fatal("recreated controller replayed or changed receipt", err)
	}
}

func slicesCloneStrings(values []string) []string { return append([]string(nil), values...) }

func TestSelectionServiceLiveStatusAndCloseJoin(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	input, _ := selectionServiceFullInput(test, fixture)
	entered := make(chan struct{})
	stopped := make(chan struct{})
	fixture.counts.mutex.Lock()
	fixture.counts.hook = func(output http.ResponseWriter, request *http.Request) bool {
		close(entered)
		<-request.Context().Done()
		close(stopped)
		return true
	}
	fixture.counts.mutex.Unlock()
	call, err := fixture.service.Begin(test.Context(), SelectionCommitMethod, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	request, err := NewSelectionCommitRequest(input)
	if err != nil {
		call.Release()
		test.Fatal(err)
	}
	defer request.Destroy()
	result := make(chan error, 1)
	go func() { _, err := fixture.service.Commit(call, request); call.Release(); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		call.Release()
		test.Fatal("real HTTP did not start")
	}
	observer, err := fixture.service.Begin(test.Context(), SelectionStatusMethod, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	observation, err := fixture.service.Status(observer, input.OperationID)
	observer.Release()
	if err != nil || observation.Observation != SelectionInProgress || observation.Receipt == nil {
		test.Fatal("bounded live Status not reachable", err)
	}
	closed := make(chan struct{})
	go func() { _ = fixture.service.Close(); close(closed) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) && !errors.Is(err, state.ErrImportOutcomeUnknown) {
			test.Fatal("Close lost cancellation", err)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("Close did not cancel/join actual fetch")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		test.Fatal("Close did not join handler release")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		test.Fatal("TLS server not released")
	}
	other, err := NewSelectionService(fixture.store, fixture.content, fixture.authentication, "https://cognate.invalid")
	if err != nil {
		test.Fatal(err)
	}
	defer other.Close()
	observer, err = other.Begin(test.Context(), SelectionStatusMethod, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	defer observer.Release()
	observation, err = other.Status(observer, input.OperationID)
	if err != nil || observation.Observation != SelectionUnknown {
		test.Fatal("nonterminal restart became resumable/terminal", err)
	}
	assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
	if err != nil || len(assets) != 0 {
		test.Fatal("cancel committed assets", err)
	}
}

func TestSelectionServiceOpaqueOwnershipAndPrivacy(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	raw := []byte(`{"skills":[],"unknown":{"secret":"SYNTHETIC-RAW-SELECTION-MARKER"}}`)
	preview := selectionServicePreview(test, fixture, SelectionMagpie, raw, SelectionOptions{})
	view, err := preview.View()
	if err != nil {
		test.Fatal(err)
	}
	if len(view.Notices) != 1 || view.Notices[0].Code != state.ImportUnknownSourceSectionExcluded {
		test.Fatal("unknown provenance notice lost")
	}
	input := SelectionCommitInput{Ticket: preview.RevealTicket(), Source: SelectionMagpie, SourceData: raw, OperationID: [16]byte{4}, RepositoryIDs: []string{strings.Repeat("a", 64)}}
	request, err := NewSelectionCommitRequest(input)
	if err != nil {
		test.Fatal(err)
	}
	defer request.Destroy()
	clone := request.Clone()
	defer clone.Destroy()
	clear(raw)
	clear(input.Ticket)
	previewClone := preview.Clone()
	defer previewClone.Destroy()
	preview.Destroy()
	if _, err := preview.View(); err == nil {
		test.Fatal("destroyed preview readable")
	}
	if len(previewClone.RevealTicket()) != 290 {
		test.Fatal("preview clone aliased destroyed ticket")
	}
	for _, value := range []any{request, clone, previewClone, struct{ private SelectionCommitRequest }{clone}, struct{ private any }{&clone}, request.owned} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			text := fmt.Sprintf(verb, value)
			if strings.Contains(text, "SYNTHETIC-RAW-SELECTION-MARKER") || strings.Contains(text, "83 89 78 84 72 69 84 73 67") {
				test.Fatal("opaque private nesting leaked source")
			}
		}
		encoded, err := json.Marshal(value)
		if err == nil && bytes.Contains(encoded, []byte("SYNTHETIC-RAW-SELECTION-MARKER")) {
			test.Fatal("opaque JSON leaked source")
		}
		var buffer bytes.Buffer
		slog.New(slog.NewJSONHandler(&buffer, nil)).Info("selection", "value", value)
		if strings.Contains(buffer.String(), "SYNTHETIC-RAW-SELECTION-MARKER") {
			test.Fatal("slog leaked source")
		}
	}
	request.Destroy()
	if clone.owned == nil || !bytes.Contains(clone.owned.access().SourceData, []byte("SYNTHETIC-RAW-SELECTION-MARKER")) {
		test.Fatal("request clone aliased caller or Destroy")
	}
}

func TestSelectionCommitPrivateS1PartialAndUnauthorized(test *testing.T) {
	for _, variant := range []string{"partial", "logout", "credential-replaced"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newSelectionServiceFixture(test)
			token, err := github.NewToken("SYNTHETIC-I4-GITHUB-TOKEN", nil)
			if err != nil {
				test.Fatal(err)
			}
			generation, err := fixture.store.PutCredential(test.Context(), fixture.owner, "synthetic-viewer", "", token)
			if err != nil {
				test.Fatal(err)
			}
			raw := []byte(`{"skills":[{"name":"private","source":{"kind":"github","repo":"example/private","path":"skills/review"}},{"name":"limited","source":{"kind":"github","repo":"example/limited","path":"skills/review"}}]}`)
			preview := selectionServicePreview(test, fixture, SelectionMagpie, raw, SelectionOptions{})
			view, err := preview.View()
			if err != nil {
				test.Fatal(err)
			}
			input := SelectionCommitInput{Ticket: preview.RevealTicket(), Source: SelectionMagpie, SourceData: raw, OperationID: [16]byte{88}}
			for _, candidate := range view.Repositories {
				input.RepositoryIDs = append(input.RepositoryIDs, candidate.ID)
			}
			for _, notice := range selectionAllNotices(view) {
				input.AckNoticeIDs = append(input.AckNoticeIDs, notice.ID)
			}
			fixture.counts.mutex.Lock()
			fixture.counts.hook = func(output http.ResponseWriter, request *http.Request) bool {
				if request.Host == "codeload.github.com" {
					if request.Header.Get("Authorization") != "" {
						test.Error("private bearer crossed to codeload")
					}
					if variant == "logout" {
						if err := fixture.authentication.Logout(test.Context(), fixture.bearer); err != nil {
							test.Error(err)
						}
					}
					if variant == "credential-replaced" {
						replacement, err := github.NewToken("SYNTHETIC-I4-REPLACEMENT", nil)
						if err != nil {
							test.Error(err)
							return true
						}
						if _, err := fixture.store.PutCredential(test.Context(), fixture.owner, "synthetic-viewer", generation, replacement); err != nil {
							test.Error(err)
						}
					}
				}
				if strings.Contains(request.URL.Path, "limited") {
					output.Header().Set("Retry-After", "2")
					output.WriteHeader(429)
					return true
				}
				return false
			}
			fixture.counts.mutex.Unlock()
			receipt, err := selectionServiceCommit(test, fixture, input)
			if variant == "partial" {
				if err != nil || receipt.Phase != state.ImportPartial || len(receipt.RepositoryOutcomes) != 2 || receipt.RepositoryOutcomes[0].Code != state.ImportRepositoryCollected || receipt.RepositoryOutcomes[1].Code != state.ImportRepositorySourceRejected {
					test.Fatal("private two-hop partial contract failed", err, receipt.Phase)
				}
				entries, err := fixture.store.ListCollectedRepositories(test.Context(), fixture.owner)
				if err != nil || len(entries) != 1 {
					test.Fatal("successful private repository was not preserved", err)
				}
			} else {
				if err == nil {
					test.Fatal("revocation ignored")
				}
				assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
				if err != nil || len(assets) != 0 {
					test.Fatal("revoked operation created assets", err)
				}
			}
			if strings.Contains(fmt.Sprint(err), "SYNTHETIC-I4") {
				test.Fatal("private upstream error leaked credential")
			}
		})
	}
}

func TestSelectionCommitFinalCASAndFailureReceipt(test *testing.T) {
	for _, variant := range []string{"name", "pins"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newSelectionServiceFixture(test)
			input, view := selectionServiceFullInput(test, fixture)
			peer, err := state.Open(test.Context(), fixture.path, bytes.Repeat([]byte{81}, 32))
			if err != nil {
				test.Fatal(err)
			}
			defer peer.Close()
			fixture.counts.mutex.Lock()
			fixture.counts.hook = func(output http.ResponseWriter, request *http.Request) bool {
				if variant == "name" {
					for _, candidate := range view.Configurations {
						if candidate.Draft != nil {
							if _, err := configuration.New(peer).Create(test.Context(), fixture.owner, *candidate.Draft, nil); err != nil {
								test.Error(err)
							}
							break
						}
					}
				} else {
					binding := view.Skills[0].Binding
					ticket, err := peer.BeginRepositorySourceRefresh(test.Context(), fixture.owner, binding.RepositoryID)
					if err != nil {
						test.Error(err)
						return false
					}
					_, err = peer.ApplySourceRefresh(test.Context(), ticket, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Changed", Description: "synthetic replacement", Path: binding.LogicalPath}, ContentHash: strings.Repeat("c", 64)}})
					if err != nil {
						test.Error(err)
					}
				}
				return false
			}
			fixture.counts.mutex.Unlock()
			receipt, err := selectionServiceCommit(test, fixture, input)
			if err != nil || receipt.Phase != state.ImportPartial || len(receipt.CreatedAssets) != 0 || len(receipt.SelectedSkills) != 0 || len(receipt.RepositoryOutcomes) != 1 || receipt.RepositoryOutcomes[0].Code != state.ImportRepositoryCollected {
				test.Fatal("known rollback did not retain only actual repository effects", err, receipt.Phase)
			}
			if receipt.Revision != 3 {
				test.Fatal("failure finalization exceeded one receipt update", receipt.Revision)
			}
			assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
			expected := 0
			if variant == "name" {
				expected = 1
			}
			if err != nil || len(assets) != expected {
				test.Fatal("atomic asset batch partially committed", err)
			}
			entries, err := fixture.store.ListCollectedRepositories(test.Context(), fixture.owner)
			if err != nil || len(entries) != 2 {
				test.Fatal("collected repository rolled back", err)
			}
		})
	}
}

func TestSelectionServiceExpiredTicketAndExistingOnly(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	raw := []byte(`{"skills":[{"name":"new","source":{"kind":"github","repo":"example/source","path":"skills/review"}}]}`)
	preview := selectionServicePreview(test, fixture, SelectionMagpie, raw, SelectionOptions{})
	view, err := preview.View()
	if err != nil {
		test.Fatal(err)
	}
	input := SelectionCommitInput{Ticket: selectionTicketAt(test, fixture, preview.RevealTicket(), time.Now().Add(-10*time.Minute)), Source: SelectionMagpie, SourceData: raw, OperationID: [16]byte{91}, RepositoryIDs: []string{view.Repositories[0].ID}}
	before := fixture.counts.size()
	if _, err := selectionServiceCommit(test, fixture, input); !errors.Is(err, state.ErrImportPrecondition) {
		test.Fatal("validly encrypted expired ticket accepted for new write", err)
	}
	selectionAssertNoReservation(test, fixture, input.OperationID)
	credential, err := fixture.authentication.AuthenticateImport(test.Context(), fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	valid, err := fixture.store.OpenImportTicket(test.Context(), credential, "https://cognate.invalid", preview.RevealTicket())
	if err != nil {
		test.Fatal(err)
	}
	digest, err := selectionRequestDigest(fixture.owner, "https://cognate.invalid", input)
	if err != nil {
		test.Fatal(err)
	}
	reservation, err := fixture.store.ReserveImportReceipt(test.Context(), credential, "https://cognate.invalid", valid, input.OperationID, digest, state.ImportSelectionPlan{RepositoryIDs: input.RepositoryIDs})
	if err != nil {
		test.Fatal(err)
	}
	first, err := fixture.store.FinishImportSelection(test.Context(), credential, reservation.Execution, state.ImportFinishSelection{Phase: state.ImportFailed, RepositoryOutcomes: reservation.Receipt.RepositoryOutcomes})
	if err != nil {
		test.Fatal(err)
	}
	duplicate, err := selectionServiceCommit(test, fixture, input)
	if err != nil || first.Phase != duplicate.Phase || first.Revision != duplicate.Revision || first.OperationID != duplicate.OperationID || !reflect.DeepEqual(first.RepositoryOutcomes, duplicate.RepositoryOutcomes) || len(duplicate.CreatedAssets)+len(duplicate.SelectedSkills)+len(duplicate.Notices) != 0 {
		test.Fatal("expired existing op reopened ticket/executed", err)
	}
	future := input
	future.OperationID = [16]byte{92}
	future.Ticket = selectionTicketAt(test, fixture, preview.RevealTicket(), time.Now().Add(time.Hour))
	if _, err := selectionServiceCommit(test, fixture, future); !errors.Is(err, state.ErrImportPrecondition) {
		test.Fatal("future-issued/clock-rollback ticket accepted", err)
	}
	selectionAssertNoReservation(test, fixture, future.OperationID)
	if fixture.counts.size() != before {
		test.Fatal("expired/future/existing operation fetched")
	}
}

func selectionTicketAt(test *testing.T, fixture selectionServiceFixture, sealed []byte, issued time.Time) []byte {
	test.Helper()
	master := bytes.Repeat([]byte{81}, 32)
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
	for _, value := range []string{base64.RawURLEncoding.EncodeToString(fingerprint[:]), fixture.owner, "https://cognate.invalid"} {
		associated = binary.BigEndian.AppendUint64(associated, uint64(len(value)))
		associated = append(associated, value...)
	}
	plaintext, err := encryption.Open(nil, nil, sealed[4:], associated)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(plaintext)
	binary.BigEndian.PutUint64(plaintext[2:10], uint64(issued.UnixMilli()))
	binary.BigEndian.PutUint64(plaintext[10:18], uint64(issued.Add(5*time.Minute).UnixMilli()))
	return encryption.Seal([]byte("CIP1"), nil, plaintext, associated)
}

func TestSelectionCanonicalLiteralVectors(test *testing.T) {
	digest := sha256.Sum256([]byte(`{}`))
	view := SelectionView{Source: SelectionMagpie, SourceDigest: hex.EncodeToString(digest[:]), Repositories: []SelectionRepository{{ID: strings.Repeat("a", 64), Repository: "example/source", SourceEnabled: new(false)}}}
	claude := configuration.Draft{Name: "Claude", Provider: configuration.ClaudeProvider{Endpoint: new(""), AuthMode: configuration.APIKey, Models: configuration.ClaudeModels{Default: new("default")}}}
	modalities := []agentconfig.CodexModality{}
	levels := []agentconfig.CodexReasoningEffort{"high", "none"}
	codex := configuration.Draft{Name: "Codex", CodexProvider: &configuration.CodexProvider{AuthMode: agentconfig.CodexProviderKeyAuth, Endpoint: "https://codex.example/v1", Model: "m", ReasoningEffort: new(agentconfig.CodexReasoningEffort("low")), Catalog: &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "m", DisplayName: new("display"), ContextWindow: new(^uint64(0)), SupportsParallelToolCalls: new(false), InputModalities: &modalities, BaseInstructions: new("  instructions\r\n"), ReasoningLevels: &levels, DefaultReasoningLevel: new(agentconfig.CodexReasoningEffort("none"))}}}}}
	gemini := configuration.Draft{Name: "Gemini", GeminiProvider: &configuration.GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, Endpoint: new("https://gemini.example/v1"), KeyTransport: new(agentconfig.GeminiBearer), EnvModel: new("env"), SettingsModel: new("settings")}}
	for index, draft := range []*configuration.Draft{&claude, &codex, &gemini} {
		agent := []string{"claude", "codex", "gemini"}[index]
		slot := []string{"claude.api-key", "codex.provider-key", "gemini.api-key"}[index]
		view.Configurations = append(view.Configurations, SelectionConfiguration{ID: strings.Repeat(string(rune('b'+index)), 64), AgentID: agent, Kind: "provider", Name: draft.Name, Status: SelectionReady, Draft: draft, Slots: []SelectionSlot{{Slot: slot, Present: true}}})
	}
	projection, err := selectionProjectionDigest(view)
	if err != nil || hex.EncodeToString(projection[:]) != "920badd52c0d0b26ada19f815c2a2ea649ccf0a46fe4d9bba06b67b2677e4259" {
		test.Fatal("independent Python projection transcript mismatch", err, hex.EncodeToString(projection[:]))
	}
	options, err := selectionOptionsDigest(SelectionOptions{})
	if err != nil || hex.EncodeToString(options[:]) != "a1d1b9171e07c4bcbc48190467dc264e4879dc470a5d304e429d2974fc883d53" {
		test.Fatal("options literal mismatch")
	}
	input := SelectionCommitInput{Ticket: bytes.Repeat([]byte{7}, 290), Source: SelectionMagpie, SourceData: []byte(`{}`), OperationID: [16]byte{1}, RepositoryIDs: []string{strings.Repeat("a", 64)}}
	request, err := selectionRequestDigest("owner", "https://cognate.invalid", input)
	if err != nil || hex.EncodeToString(request[:]) != "79f02059a3c8376fd638b16e1d0dbcf4c5b46032968620c8195dc9691a82b781" {
		test.Fatal("request literal mismatch")
	}
	resolution, err := selectionResolutionDigest("owner", selectionResolution{})
	if err != nil || hex.EncodeToString(resolution[:]) != "d9444cf7c0450531b31422136685d16a0dda19d6082286e548f0bf8ed728f255" {
		test.Fatal("resolution literal mismatch")
	}
	for _, variant := range []string{"false-absent", "array-absent", "order", "model-swap", "max-value"} {
		model := &codex.CodexProvider.Catalog.Models[0]
		saved := *model
		switch variant {
		case "false-absent":
			model.SupportsParallelToolCalls = nil
		case "array-absent":
			model.InputModalities = nil
		case "order":
			reverse := []agentconfig.CodexReasoningEffort{"none", "high"}
			model.ReasoningLevels = &reverse
		case "model-swap":
			gemini.GeminiProvider.EnvModel, gemini.GeminiProvider.SettingsModel = gemini.GeminiProvider.SettingsModel, gemini.GeminiProvider.EnvModel
		case "max-value":
			model.ContextWindow = new(uint64(0))
		}
		changed, err := selectionProjectionDigest(view)
		if err != nil || changed == projection {
			test.Fatal("typed presence/order collapsed", variant)
		}
		*model = saved
		if variant == "model-swap" {
			gemini.GeminiProvider.EnvModel, gemini.GeminiProvider.SettingsModel = gemini.GeminiProvider.SettingsModel, gemini.GeminiProvider.EnvModel
		}
	}
	invalid := codex
	invalid.Provider.AuthMode = configuration.APIKey
	transcript := selectionTranscriptFor("invalid")
	transcript.draft(&invalid)
	if _, err := transcript.result(); err == nil {
		test.Fatal("ambiguous union silently hashed")
	}
}

func TestSelectionCommitActualSQLiteUnknownNoReplay(test *testing.T) {
	for _, variant := range []string{"checkpoint", "final"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newSelectionServiceFixture(test)
			input, _ := selectionServiceFullInput(test, fixture)
			database, err := sql.Open("sqlite3", fixture.path+"?_fk=1")
			if err != nil {
				test.Fatal(err)
			}
			defer database.Close()
			statement := `CREATE TRIGGER i4_unknown BEFORE UPDATE ON import_receipts WHEN NEW.phase='collecting' BEGIN SELECT RAISE(ROLLBACK,'SYNTHETIC-PRIVATE-SQLITE-ERROR'); END;`
			if variant == "final" {
				statement = `CREATE TRIGGER i4_unknown BEFORE INSERT ON configuration_assets BEGIN SELECT RAISE(ROLLBACK,'SYNTHETIC-PRIVATE-SQLITE-ERROR'); END;`
			}
			if _, err := database.ExecContext(test.Context(), statement); err != nil {
				test.Fatal(err)
			}
			_, err = selectionServiceCommit(test, fixture, input)
			if !errors.Is(err, state.ErrImportOutcomeUnknown) {
				test.Fatal("SQLite rollback uncertainty was not Unknown", err)
			}
			if strings.Contains(fmt.Sprint(err), "SYNTHETIC-PRIVATE") {
				test.Fatal("raw SQL diagnostic leaked")
			}
			before := fixture.counts.size()
			if _, err := database.ExecContext(test.Context(), `DROP TRIGGER i4_unknown`); err != nil {
				test.Fatal(err)
			}
			credential, err := fixture.authentication.AuthenticateImport(test.Context(), fixture.bearer)
			if err != nil {
				test.Fatal(err)
			}
			known, found, err := fixture.store.ReadImportReceipt(test.Context(), credential, input.OperationID)
			if err != nil || !found || selectionTerminal(known.Phase) {
				test.Fatal("Unknown was automatically failure-finalized", err)
			}
			if variant == "checkpoint" && known.Revision != 1 || variant == "final" && known.Revision != 2 {
				test.Fatal("Unknown performed a later receipt write", known.Revision)
			}
			duplicate, err := selectionServiceCommit(test, fixture, input)
			if err != nil || duplicate.Revision != known.Revision || duplicate.Phase != known.Phase || fixture.counts.size() != before {
				test.Fatal("Unknown duplicate resumed", err)
			}
			observer, err := fixture.service.Begin(test.Context(), SelectionStatusMethod, fixture.bearer)
			if err != nil {
				test.Fatal(err)
			}
			observation, err := fixture.service.Status(observer, input.OperationID)
			observer.Release()
			if err != nil || observation.Observation != SelectionUnknown {
				test.Fatal("Unknown receipt falsely claimed terminal/live", err)
			}
			assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
			if err != nil || len(assets) != 0 {
				test.Fatal("uncertain transaction partially wrote asset batch", err)
			}
			entries, err := fixture.store.ListCollectedRepositories(test.Context(), fixture.owner)
			if err != nil || len(entries) != 2 {
				test.Fatal("prior actual repository effect was deleted", err)
			}
		})
	}
}

func TestSelectionServiceAdmissionBoundsAndBodyLifecycle(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	bearers := []string{fixture.bearer}
	for _, username := range []string{"owner-two", "owner-three"} {
		if _, err := fixture.authentication.Register(test.Context(), username, []byte("synthetic-password-other")); err != nil {
			test.Fatal(err)
		}
		token, err := fixture.authentication.Login(test.Context(), username, []byte("synthetic-password-other"))
		if err != nil {
			test.Fatal(err)
		}
		bearers = append(bearers, token.Reveal())
	}
	first, err := fixture.service.Begin(test.Context(), SelectionCommitMethod, bearers[0])
	if err != nil {
		test.Fatal(err)
	}
	defer first.Release()
	second, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, bearers[1])
	if err != nil {
		test.Fatal(err)
	}
	defer second.Release()
	if _, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, bearers[2]); !errors.Is(err, ErrSelectionBusy) {
		test.Fatal("global heavy bound bypassed", err)
	}
	if _, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, "invalid"); !errors.Is(err, ErrSelectionBusy) {
		test.Fatal("auth occurred before global admission", err)
	}
	observer, err := fixture.service.Begin(test.Context(), SelectionStatusMethod, bearers[2])
	if err != nil {
		test.Fatal(err)
	}
	defer observer.Release()
	if _, err := fixture.service.Begin(test.Context(), SelectionStatusMethod, bearers[1]); !errors.Is(err, ErrSelectionBusy) {
		test.Fatal("observer global bound bypassed", err)
	}
	second.Release()
	observer.Release()
	closed := make(chan struct{})
	go func() { _ = fixture.service.Close(); close(closed) }()
	select {
	case <-first.Context().Done():
	case <-time.After(time.Second):
		test.Fatal("Close failed to cancel pre-body permit")
	}
	select {
	case <-closed:
		test.Fatal("Close returned before handler/body cleanup")
	default:
	}
	first.Release()
	select {
	case <-closed:
	case <-time.After(time.Second):
		test.Fatal("Close did not join pre-body permit")
	}
	if fixture.counts.size() != 0 {
		test.Fatal("admission/body lifecycle performed network")
	}
}

func TestSelectionServicePermitAndInputBounds(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	other, err := NewSelectionService(fixture.store, fixture.content, fixture.authentication, "https://cognate.invalid")
	if err != nil {
		test.Fatal(err)
	}
	defer other.Close()
	call, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	defer call.Release()
	if _, err := other.Preview(call, SelectionMagpie, []byte(`{"skills":[]}`), SelectionOptions{}); !errors.Is(err, ErrSelectionInvalid) {
		test.Fatal("foreign permit accepted")
	}
	if _, err := fixture.service.Status(call, [16]byte{1}); !errors.Is(err, ErrSelectionInvalid) {
		test.Fatal("wrong method permit accepted")
	}
	if _, err := fixture.service.Preview(call, SelectionMagpie, make([]byte, MaxMagpieExportBytes+1), SelectionOptions{}); !errors.Is(err, ErrSelectionLimit) {
		test.Fatal("oversized Magpie accepted", err)
	}
	if _, err := fixture.service.Preview(call, SelectionMagpie, []byte(`{"skills":[]}`), SelectionOptions{}); !errors.Is(err, ErrSelectionInvalid) {
		test.Fatal("permit re-entered")
	}
	if _, err := fixture.service.Preview(new(SelectionCall), SelectionMagpie, []byte(`{}`), SelectionOptions{}); !errors.Is(err, ErrSelectionInvalid) {
		test.Fatal("zero permit accepted")
	}
	if _, err := NewSelectionCommitRequest(SelectionCommitInput{Ticket: make([]byte, 290), Source: SelectionMagpie, SourceData: []byte(`{}`), OperationID: [16]byte{1}, RepositoryIDs: []string{strings.Repeat("a", 64)}, Options: SelectionOptions{Candidates: make([]SelectionCandidateOptions, 161)}}); err == nil {
		test.Fatal("oversize options accepted")
	}
	if fixture.counts.size() != 0 {
		test.Fatal("rejected permit downloaded")
	}
}

func TestSelectionCommitMagpieExactPathNoCollect(test *testing.T) {
	for _, variant := range []string{"exact", "missing-path", "basename", "missing-repository", "duplicate-binding"} {
		test.Run(variant, func(test *testing.T) {
			fixture := newSelectionServiceFixture(test)
			if variant != "missing-repository" {
				if _, err := fixture.content.Collect(test.Context(), fixture.owner, "example/existing", ""); err != nil {
					test.Fatal(err)
				}
			}
			source := `{"kind":"github","repo":"example/existing","path":"skills/review"}`
			if variant == "missing-path" {
				source = `{"kind":"github","repo":"example/existing"}`
			}
			if variant == "basename" {
				source = `{"kind":"github","repo":"example/existing","path":"review"}`
			}
			raw := []byte(`{"skills":[{"name":"first","source":` + source + `}]}`)
			if variant == "duplicate-binding" {
				raw = []byte(`{"skills":[{"name":"first","source":` + source + `},{"name":"second","source":` + source + `}]}`)
			}
			before := fixture.counts.size()
			preview := selectionServicePreview(test, fixture, SelectionMagpie, raw, SelectionOptions{})
			view, err := preview.View()
			if err != nil {
				test.Fatal(err)
			}
			input := SelectionCommitInput{Ticket: preview.RevealTicket(), Source: SelectionMagpie, SourceData: raw, OperationID: [16]byte{89}}
			for _, candidate := range view.Skills {
				input.SkillIDs = append(input.SkillIDs, candidate.ID)
			}
			for _, notice := range selectionAllNotices(view) {
				input.AckNoticeIDs = append(input.AckNoticeIDs, notice.ID)
			}
			receipt, err := selectionServiceCommit(test, fixture, input)
			if variant == "exact" {
				if err != nil || receipt.Phase != state.ImportCompleted || len(receipt.SelectedSkills) != 1 || receipt.SelectedSkills[0].LogicalPath != "skills/review" {
					test.Fatal("actual exact-path skill-only selection failed", err)
				}
			} else {
				if err == nil {
					test.Fatal("guessed/duplicate/foreign skill binding accepted")
				}
				selectionAssertNoReservation(test, fixture, input.OperationID)
				expected := SelectionCandidateUnresolved
				if variant == "missing-repository" {
					expected = SelectionCandidateNeedsRepositoryImport
				}
				if variant != "duplicate-binding" && view.Skills[0].Status != expected {
					test.Fatal("wrong missing/path status", view.Skills[0].Status)
				}
			}
			if fixture.counts.size() != before {
				test.Fatal("preview or skill-only import called Collect/network")
			}
		})
	}
}

func TestSelectionServiceUnknownPrecedenceAndCancellation(test *testing.T) {
	ctx, cancel := context.WithCancelCause(test.Context())
	cancel(errors.New("SYNTHETIC-PRIVATE-CANCEL-CAUSE"))
	if err := selectionServiceError(ctx, errors.Join(state.ErrImportOutcomeUnknown, ctx.Err())); !errors.Is(err, state.ErrImportOutcomeUnknown) {
		test.Fatal("cancellation hid unknown commit")
	}
	fixture := newSelectionServiceFixture(test)
	if _, err := fixture.service.Begin(ctx, SelectionPreviewMethod, fixture.bearer); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "PRIVATE") {
		test.Fatal("cancel bypassed/redaction lost", err)
	}
	call, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	defer call.Release()
	call.cancel()
	if _, err := fixture.service.Preview(call, SelectionMagpie, []byte(`{"skills":[]}`), SelectionOptions{}); !errors.Is(err, context.Canceled) {
		test.Fatal("cancelled permit executed", err)
	}
}

func TestSelectionServiceStatusStorageFailureAndOrigin(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	unconfigured, err := NewSelectionService(fixture.store, fixture.content, fixture.authentication, "")
	if err != nil {
		test.Fatal("invalid origin broke legacy service startup", err)
	}
	defer unconfigured.Close()
	if _, err := unconfigured.Begin(test.Context(), SelectionStatusMethod, fixture.bearer); !errors.Is(err, state.ErrImportPrecondition) {
		test.Fatal("unconfigured origin admitted a new method", err)
	}
	call, err := fixture.service.Begin(test.Context(), SelectionStatusMethod, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	defer call.Release()
	if err := fixture.store.Close(); err != nil {
		test.Fatal(err)
	}
	observation, err := fixture.service.Status(call, [16]byte{1})
	if err == nil || observation.Observation == SelectionNotObserved {
		test.Fatal("storage failure masqueraded as NotObserved")
	}
}

func TestSelectionCanonicalCompiledHostIdentity(test *testing.T) {
	host, err := selectionHostDigest()
	if err != nil || hex.EncodeToString(host[:]) != "a9d3abd187595386d6dfeb934e9d27354034e8fbc8144d7861fb0224c4271a31" {
		test.Fatal("actual compiled ten-file host bytes changed", err)
	}
	for _, fixture := range []struct {
		source        SelectionSource
		module, bound string
	}{
		{SelectionCCSwitch24, "5d50fb218db1cca7de85b0d2dd30c9634a07da3549a22801321068e769073380", "3bbdc87e850cdedc692fdd93d944cd97309e48516ae0013cfe3f165f5bc293d0"},
		{SelectionMagpie, "009724f358a2c537b342b4ce8f0967eaddd8b95907ff2f07c5039a0b6cc67596", "03d4e25887b78c883de0b5c0816d12295434d3f975179a52e9be82ce6b199e89"},
	} {
		module, err := SelectionAdapterDigest(fixture.source)
		if err != nil || module != fixture.module {
			test.Fatal("fixed actual module digest changed", err)
		}
		bound, err := selectionBoundAdapterDigest(fixture.source)
		if err != nil || hex.EncodeToString(bound[:]) != fixture.bound {
			test.Fatal("adapter failed independent module+Go-source literal", err)
		}
	}
}

func TestSelectionCanonicalBindingMatchesEmbeddedBytes(test *testing.T) {
	paths := []string{"ccswitch.go", "magpie.go", "skills.go", "snapshot.go", "selection.go", "selection_snapshot.go", "source_claude.go", "source_codex.go", "source_gemini.go", "selection_service.go"}
	independent := sha256.New()
	writeLength := func(value []byte) {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(value)))
		_, _ = independent.Write(size[:])
		_, _ = independent.Write(value)
	}
	writeLength([]byte("cognate/import-selection/v1/go-host-source"))
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], 10)
	_, _ = independent.Write(count[:])
	for _, path := range paths {
		data, err := selectionHostSources.ReadFile(path)
		if err != nil {
			test.Fatal(err)
		}
		writeLength([]byte(path))
		writeLength(data)
	}
	host := independent.Sum(nil)
	for _, source := range []SelectionSource{SelectionCCSwitch24, SelectionMagpie} {
		module := magpieModule
		if source == SelectionCCSwitch24 {
			module = ccSwitchModule
		}
		moduleDigest := sha256.Sum256([]byte("cognate/import-selection/schema24/v1\x00" + string(source) + "\x00" + module))
		independent.Reset()
		writeLength([]byte("cognate/import-selection/v1/adapter-host"))
		writeLength([]byte(source))
		_, _ = independent.Write(moduleDigest[:])
		_, _ = independent.Write(host)
		expected := independent.Sum(nil)
		actual, err := selectionBoundAdapterDigest(source)
		if err != nil || !bytes.Equal(expected, actual[:]) {
			test.Fatal("actual host source was omitted from adapter binding", err)
		}
	}
}

func TestSelectionServiceAdmissionAndClose(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	if _, err := NewSelectionService(nil, fixture.content, fixture.authentication, "https://cognate.invalid"); err == nil {
		test.Fatal("nil store admitted")
	}
	if _, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, "invalid"); err == nil {
		test.Fatal("invalid session admitted")
	}
	first, err := fixture.service.Begin(test.Context(), SelectionCommitMethod, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	defer first.Release()
	if unexpected, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, fixture.bearer); err == nil {
		unexpected.Release()
		test.Fatal("same-owner heavy slot admitted twice")
	}
	observer, err := fixture.service.Begin(test.Context(), SelectionStatusMethod, fixture.bearer)
	if err != nil {
		test.Fatal("bounded observer unavailable during heavy call", err)
	}
	if unexpected, err := fixture.service.Begin(test.Context(), SelectionStatusMethod, fixture.bearer); err == nil {
		unexpected.Release()
		test.Fatal("observer capacity bypass")
	}
	observer.Release()
	first.Release()
	first.Release()
	if err := fixture.service.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, fixture.bearer); err == nil {
		test.Fatal("closed service admitted work")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := fixture.service.Begin(ctx, SelectionPreviewMethod, fixture.bearer); !errors.Is(err, context.Canceled) {
		test.Fatal("cancellation lost", err)
	}
}

func TestSelectionServicePreviewAndObservation(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	call, err := fixture.service.Begin(test.Context(), SelectionPreviewMethod, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	defer call.Release()
	preview, err := fixture.service.Preview(call, SelectionMagpie, []byte(`{"skills":[]}`), SelectionOptions{})
	call.Release()
	if err != nil {
		test.Fatal("real moejs preview failed", err)
	}
	defer preview.Destroy()
	view, err := preview.View()
	if err != nil || len(preview.RevealTicket()) != 290 || view.ExpiresAt.IsZero() {
		test.Fatal("missing bounded preview/ticket")
	}
	observer, err := fixture.service.Begin(test.Context(), SelectionStatusMethod, fixture.bearer)
	if err != nil {
		test.Fatal(err)
	}
	defer observer.Release()
	observation, err := fixture.service.Status(observer, [16]byte{1})
	if err != nil || observation.Observation != SelectionNotObserved || observation.Receipt != nil {
		test.Fatal("unseen operation not reported accurately", err)
	}
}
