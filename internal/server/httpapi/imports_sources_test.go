package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	libraryv1 "github.com/ca-x/cognate/gen/skillhub/library/v1"
	"github.com/ca-x/cognate/gen/skillhub/library/v1/libraryv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

type sourceOfflineFetcher struct{ requests atomic.Int32 }

func (fetcher *sourceOfflineFetcher) Fetch(context.Context, repository.Reference) (*repository.ArchiveSnapshot, error) {
	fetcher.requests.Add(1)
	return nil, repository.ErrArchiveUnavailable
}

func TestMagpieSourceReferenceHTTPRoundTrip(test *testing.T) {
	raw := []byte(`{"skills":[{"name":"Review","source":{"kind":"github","repo":"example/review","path":"skills/review"}},{"name":"Root","source":{"kind":"github","repo":"example/root","path":"."}},{"name":"Unspecified","source":{"kind":"github","repo":"example/review"}}]}`)
	if source := os.Getenv("COGNATE_MAGPIE_LIBRARY_FIXTURE"); source != "" {
		file, err := os.Open(source)
		if err != nil {
			test.Fatal("open private source fixture")
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			file.Close()
			test.Fatal("invalid private source fixture")
		}
		raw, err = io.ReadAll(io.LimitReader(file, (1<<20)+1))
		file.Close()
		if err != nil || len(raw) > 1<<20 {
			test.Fatal("read private source fixture")
		}
	}
	defer clear(raw)
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		test.Fatal(err)
	}
	defer clear(key)
	path := filepath.Join(directory, "state.db")
	store, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal("open isolated store")
	}
	defer store.Close()
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	owner, err := authentication.Register(test.Context(), "source-owner", []byte("synthetic-source-password"))
	if err != nil {
		test.Fatal(err)
	}
	session, err := authentication.Login(test.Context(), "source-owner", []byte("synthetic-source-password"))
	if err != nil {
		test.Fatal(err)
	}
	cache, err := library.OpenArchiveCache(filepath.Join(directory, "archives"))
	if err != nil {
		test.Fatal(err)
	}
	defer cache.Close()
	fetcher := new(sourceOfflineFetcher)
	content, err := library.NewService(store, cache, fetcher)
	if err != nil {
		test.Fatal(err)
	}
	defer content.Close()
	endpoint := httptest.NewUnstartedServer(nil)
	selection, err := importer.NewSelectionService(store, content, authentication, "https://"+endpoint.Listener.Addr().String())
	if err != nil {
		test.Fatal(err)
	}
	imports, err := NewImportHandlerWithSelection(authentication, content, selection)
	if err != nil {
		test.Fatal(err)
	}
	defer imports.Close()
	endpoint.Config.Handler = NewWithOptions(authentication, Options{Store: store, Library: content, Imports: imports})
	endpoint.StartTLS()
	defer endpoint.Close()
	client := importv1connect.NewImportServiceClient(endpoint.Client(), endpoint.URL)
	libraryClient := libraryv1connect.NewLibraryServiceClient(endpoint.Client(), endpoint.URL)
	preview, err := client.PreviewImportSelection(test.Context(), mcpRequest(&importv1.PreviewImportSelectionRequest{Source: importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON, SourceData: raw, RegisterSources: true}, session.Reveal()))
	if err != nil {
		test.Fatal("private export preview failed")
	}
	if len(preview.Msg.Skills) == 0 || len(preview.Msg.Repositories) == 0 {
		test.Fatal("preview lost source candidates")
	}
	input := &importv1.CommitImportSelectionRequest{Ticket: preview.Msg.Ticket, Source: importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON, SourceData: raw, RegisterSources: true, OperationId: bytes.Repeat([]byte{17}, 16)}
	for _, candidate := range preview.Msg.Repositories {
		input.RepositoryIds = append(input.RepositoryIds, candidate.Id)
	}
	for _, candidate := range preview.Msg.Skills {
		if candidate.Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_SOURCE_REFERENCE_READY || candidate.Binding != nil || candidate.SourceReference == nil {
			test.Fatal("cold library skill was not selectable as an unfetched reference")
		}
		input.SkillIds = append(input.SkillIds, candidate.Id)
		for _, notice := range candidate.Notices {
			input.AckNoticeIds = append(input.AckNoticeIds, notice.Id)
		}
	}
	for _, candidate := range preview.Msg.Configurations {
		for _, notice := range candidate.Notices {
			input.AckNoticeIds = append(input.AckNoticeIds, notice.Id)
		}
	}
	global := make([]string, 0, len(preview.Msg.Notices))
	for _, notice := range preview.Msg.Notices {
		global = append(global, notice.Id)
	}
	input.AckNoticeIds = append(global, input.AckNoticeIds...)
	committed, err := client.CommitImportSelection(test.Context(), mcpRequest(input, session.Reveal()))
	if err != nil || committed.Msg.Receipt.Phase != importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED || len(committed.Msg.Receipt.SelectedSkillSources) != len(input.SkillIds) || len(committed.Msg.Receipt.SelectedSkillBindings) != 0 {
		test.Fatal("source reference HTTP commit did not complete")
	}
	for _, outcome := range committed.Msg.Receipt.RepositoryOutcomes {
		if outcome.Code != importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_REGISTERED {
			test.Fatal("HTTP receipt misrepresented registration as collected content")
		}
	}
	listed, err := libraryClient.ListRepositorySources(test.Context(), mcpRequest(&libraryv1.ListRepositorySourcesRequest{}, session.Reveal()))
	if err != nil || len(listed.Msg.Sources) != len(input.RepositoryIds) {
		test.Fatal("registered sources were not visible through HTTP")
	}
	references := 0
	for _, source := range listed.Msg.Sources {
		references += len(source.Skills)
		if source.CollectedRepositoryId != "" {
			test.Fatal("registered source was falsely marked verified")
		}
	}
	if references != len(input.SkillIds) || fetcher.requests.Load() != 0 {
		test.Fatal("import lost references or attempted a network download")
	}
	endpoint.Close()
	if err := imports.Close(); err != nil {
		test.Fatal(err)
	}
	if err := content.Close(); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal("reopen source store")
	}
	defer reopened.Close()
	saved, err := reopened.ListRepositorySources(test.Context(), owner)
	if err != nil || len(saved) != len(input.RepositoryIds) {
		test.Fatal("source records did not survive restart")
	}
	count := 0
	for _, source := range saved {
		count += len(source.Skills)
	}
	if count != references {
		test.Fatal("skill source records did not survive restart")
	}
	test.Logf("repositories=%d skill_references=%d network_requests=%d restart_persistence=passed", len(saved), count, fetcher.requests.Load())
}
