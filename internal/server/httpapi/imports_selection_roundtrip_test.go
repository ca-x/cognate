package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

type importRoundtripTransport func(*http.Request) (*http.Response, error)

func (transport importRoundtripTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func importRoundtripFetcher(test *testing.T) (*repository.ArchiveFetcher, *atomic.Int64) {
	test.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	content := []byte("---\nname: review-guide\ndescription: synthetic review\n---\nSynthetic skill.\n")
	if err := archive.WriteHeader(&tar.Header{Name: "source/skills/review/SKILL.md", Mode: 0o644, Size: int64(len(content))}); err != nil {
		test.Fatal("synthetic archive header failed")
	}
	if _, err := archive.Write(content); err != nil {
		test.Fatal("synthetic archive content failed")
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal("synthetic archive close failed")
	}
	count := new(atomic.Int64)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		count.Add(1)
		if request.Host != "codeload.github.com" || request.Method != http.MethodGet {
			test.Error("unexpected synthetic GitHub request")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = writer.Write(buffer.Bytes())
	}))
	test.Cleanup(upstream.Close)
	endpoint, err := url.Parse(upstream.URL)
	if err != nil {
		test.Fatal("synthetic upstream address failed")
	}
	transport := importRoundtripTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "codeload.github.com" {
			return nil, errors.New("roundtrip fixture forbids external HTTP")
		}
		clone := request.Clone(request.Context())
		clone.URL = request.URL.Clone()
		clone.Host = request.URL.Host
		clone.URL.Scheme, clone.URL.Host = endpoint.Scheme, endpoint.Host
		return upstream.Client().Transport.RoundTrip(clone)
	})
	return repository.NewArchiveFetcher(&http.Client{Transport: transport, Timeout: 5 * time.Second}), count
}

func importRoundtripSnapshot(test *testing.T, extraSQL ...string) []byte {
	test.Helper()
	schema, err := os.ReadFile("../importer/testdata/selection/ccswitch-schema24.sql")
	if err != nil {
		test.Fatal("synthetic source fixture unavailable")
	}
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		test.Fatal("synthetic source database unavailable")
	}
	test.Cleanup(func() { _ = database.Close() })
	connection, err := database.Conn(test.Context())
	if err != nil {
		test.Fatal("synthetic source connection unavailable")
	}
	defer connection.Close()
	if _, err := connection.ExecContext(test.Context(), string(schema)+`UPDATE skills SET repo_name='existing';`+strings.Join(extraSQL, "\n")); err != nil {
		test.Fatal("synthetic source schema failed")
	}
	var snapshot []byte
	err = connection.Raw(func(driver any) error {
		serializer, ok := driver.(interface{ Serialize() ([]byte, error) })
		if !ok {
			return errors.New("synthetic SQLite serializer unavailable")
		}
		var serializeErr error
		snapshot, serializeErr = serializer.Serialize()
		return serializeErr
	})
	if err != nil {
		test.Fatal("synthetic source snapshot failed")
	}
	test.Cleanup(func() { clear(snapshot) })
	return snapshot
}

func TestImportSelectionHTTPRoundtrip(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		for mask := range 8 {
			test.Run(fmt.Sprintf("%s/%03b", encoding, mask), func(test *testing.T) {
				importSelectionHTTPRoundtrip(test, encoding, mask)
			})
		}
	}
}

func importSelectionHTTPRoundtrip(test *testing.T, encoding string, mask int) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal("private fixture root unavailable")
	}
	key := bytes.Repeat([]byte{93}, 32)
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		test.Fatal("real store unavailable")
	}
	test.Cleanup(func() {
		if err := store.Close(); err != nil {
			test.Error("real store close failed")
		}
	})
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal("real authentication unavailable")
	}
	password := []byte("Synthetic roundtrip passphrase only")
	defer clear(password)
	owner, err := authentication.Register(test.Context(), "roundtrip-owner", password)
	if err != nil {
		test.Fatal("real registration failed")
	}
	session, err := authentication.Login(test.Context(), "roundtrip-owner", password)
	if err != nil {
		test.Fatal("real login failed")
	}
	cache, err := library.OpenArchiveCache(filepath.Join(directory, "archives"))
	if err != nil {
		test.Fatal("real archive cache unavailable")
	}
	test.Cleanup(func() { _ = cache.Close() })
	fetcher, downloads := importRoundtripFetcher(test)
	content, err := library.NewService(store, cache, fetcher)
	if err != nil {
		test.Fatal("real library unavailable")
	}
	test.Cleanup(func() { _ = content.Close() })
	existing, err := content.Collect(test.Context(), owner, "example/existing", "")
	if err != nil {
		test.Fatal("real existing repository collection failed")
	}
	existing, err = store.CollectedRepository(test.Context(), owner, existing.ID)
	if err != nil || len(existing.Skills) != 1 {
		test.Fatal("real skill inventory missing")
	}
	const origin = "https://cognate.example.test"
	selection, err := importer.NewSelectionService(store, content, authentication, origin)
	if err != nil {
		test.Fatal("real selection service unavailable")
	}
	handler, err := NewImportHandlerWithSelection(authentication, content, selection)
	if err != nil {
		_ = selection.Close()
		test.Fatal("real import handler unavailable")
	}
	test.Cleanup(func() {
		if err := handler.Close(); err != nil {
			test.Error("real import handler drain failed")
		}
	})
	server := httptest.NewTLSServer(NewWithOptions(authentication, Options{PublicOrigin: origin, Store: store, Library: content, Imports: handler}))
	test.Cleanup(server.Close)
	var observed [3]atomic.Int64
	client := server.Client()
	client.Timeout = 10 * time.Second
	transport := client.Transport
	client.Transport = importRoundtripTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != server.Listener.Addr().String() {
			return nil, errors.New("roundtrip fixture forbids nonlocal RPC")
		}
		procedures := []string{importv1connect.ImportServicePreviewImportSelectionProcedure, importv1connect.ImportServiceCommitImportSelectionProcedure, importv1connect.ImportServiceGetImportCommitStatusProcedure}
		index := slices.Index(procedures, request.URL.Path)
		if index < 0 || request.Header.Get("Content-Type") != "application/"+encoding {
			return nil, errors.New("unexpected roundtrip procedure or wire encoding")
		}
		observed[index].Add(1)
		response, err := transport.RoundTrip(request)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || len(body) > 2<<20 {
			return nil, errors.New("roundtrip response unavailable or over budget")
		}
		if bytes.Contains(body, []byte("SYNTHETIC-PRIVATE-MARKER")) || bytes.Contains(body, []byte(session.Reveal())) {
			test.Error("roundtrip response disclosed source credential or session")
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		return response, nil
	})
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Authorization", "Bearer "+session.Reveal())
			request.Header().Set("Origin", origin)
			return next(ctx, request)
		}
	})
	clientOptions := []connect.ClientOption{connect.WithInterceptors(interceptor)}
	if encoding == "json" {
		clientOptions = append(clientOptions, connect.WithProtoJSON())
	}
	rpc := importv1connect.NewImportServiceClient(client, server.URL, clientOptions...)
	raw := importRoundtripSnapshot(test)
	source := importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24
	prePreviewDownloads := downloads.Load()
	initial, err := rpc.PreviewImportSelection(test.Context(), connect.NewRequest(&importv1.PreviewImportSelectionRequest{Source: source, SourceData: bytes.Clone(raw)}))
	if err != nil {
		test.Fatalf("initial real HTTP preview failed: %s", connect.CodeOf(err))
	}
	if len(initial.Msg.Repositories) != 1 || len(initial.Msg.Skills) != 1 || len(initial.Msg.Configurations) != 5 {
		test.Fatalf("initial real HTTP candidate counts: repositories=%d skills=%d configurations=%d", len(initial.Msg.Repositories), len(initial.Msg.Skills), len(initial.Msg.Configurations))
	}
	var options []*importv1.ImportCandidateOptions
	for _, candidate := range initial.Msg.Configurations {
		switch candidate.KnownAgentId {
		case "claude":
			if candidate.Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_NEEDS_CHOICE {
				test.Fatal("Claude auth ambiguity did not require explicit choice")
			}
			options = append(options, &importv1.ImportCandidateOptions{CandidateId: candidate.Id, Auth: &importv1.ImportCandidateOptions_ClaudeAuthMode{ClaudeAuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY}})
		case "gemini":
			options = append(options, &importv1.ImportCandidateOptions{CandidateId: candidate.Id, GeminiKeyTransport: new(configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_BEARER)})
		case "codex":
		default:
			if candidate.Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_UNSUPPORTED_KIND || candidate.Draft != nil {
				test.Fatal("MCP/prompt fixture must remain explicitly unsupported")
			}
		}
	}
	skill := existing.Skills[0]
	binding := &importv1.SkillBinding{RepositoryId: existing.ID, ArchiveHash: existing.ArchiveHash, SkillId: skill.ID, LogicalPath: skill.Path, ContentHash: skill.ContentHash, Repository: existing.NameWithOwner, Branch: existing.Branch}
	options = append(options, &importv1.ImportCandidateOptions{CandidateId: initial.Msg.Skills[0].Id, SkillBinding: binding})
	preview, err := rpc.PreviewImportSelection(test.Context(), connect.NewRequest(&importv1.PreviewImportSelectionRequest{Source: source, SourceData: bytes.Clone(raw), Options: options}))
	if err != nil {
		test.Fatalf("explicit-choice real HTTP repreview failed: %s", connect.CodeOf(err))
	}
	view := preview.Msg
	if len(view.Ticket) != state.ImportTicketBytes || view.ExpiresMs <= time.Now().UnixMilli() || len(view.Repositories) != 1 || len(view.Skills) != 1 || len(view.Configurations) != 5 {
		test.Fatal("repreview ticket or candidate contract changed")
	}
	if downloads.Load() != prePreviewDownloads {
		test.Fatal("pure HTTP preview unexpectedly downloaded a repository")
	}
	previewAssets, err := store.ListConfigurationAssets(test.Context(), owner)
	if err != nil || len(previewAssets) != 0 {
		test.Fatal("pure HTTP preview unexpectedly wrote an asset")
	}
	if view.Repositories[0].Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY || view.Skills[0].Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY || !proto.Equal(view.Skills[0].Binding, binding) {
		test.Fatal("explicit existing skill binding or repository not ready")
	}
	input := &importv1.CommitImportSelectionRequest{Ticket: bytes.Clone(view.Ticket), Source: source, SourceData: bytes.Clone(raw), Options: options, OperationId: append([]byte{byte(mask + 1)}, make([]byte, 15)...)}
	if mask&1 != 0 {
		input.RepositoryIds = []string{view.Repositories[0].Id}
	}
	if mask&2 != 0 {
		input.SkillIds = []string{view.Skills[0].Id}
	}
	readyProviders := 0
	expectedSlots := map[string]bool{"claude.api-key": true, "claude.auth-token": true, "codex.provider-key": true, "gemini.api-key": true, "gemini.legacy-api-key": false}
	for _, candidate := range view.Configurations {
		if candidate.Status == importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_UNSUPPORTED_KIND && candidate.Draft == nil {
			continue
		}
		if candidate.Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY || candidate.Draft == nil {
			test.Fatal("explicit-choice typed provider not ready")
		}
		readyProviders++
		for _, slot := range candidate.CredentialSlots {
			present, known := expectedSlots[slot.SlotId]
			if !known || slot.Present != present {
				test.Fatal("source credential presence metadata changed")
			}
			delete(expectedSlots, slot.SlotId)
		}
		if mask&4 != 0 {
			input.ConfigurationIds = append(input.ConfigurationIds, candidate.Id)
		}
	}
	if readyProviders != 3 || len(expectedSlots) != 0 {
		test.Fatal("three typed providers or explicit slot presence were not preserved")
	}
	notices := slices.Clone(view.Notices)
	for _, candidate := range view.Skills {
		notices = append(notices, candidate.Notices...)
	}
	for _, candidate := range view.Configurations {
		notices = append(notices, candidate.Notices...)
	}
	for _, notice := range notices {
		input.AckNoticeIds = append(input.AckNoticeIds, notice.Id)
	}
	if len(view.Notices) == 0 || len(view.Skills[0].Notices) == 0 || len(input.AckNoticeIds) <= len(view.Notices)+len(view.Skills[0].Notices) {
		test.Fatal("fixture did not exercise global, skill and configuration notice ordering")
	}
	statusRequest := &importv1.GetImportCommitStatusRequest{OperationId: bytes.Clone(input.OperationId)}
	beforeStatus, err := rpc.GetImportCommitStatus(test.Context(), connect.NewRequest(statusRequest))
	if err != nil || beforeStatus.Msg.Observation != importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_NOT_OBSERVED || beforeStatus.Msg.Receipt != nil {
		test.Fatal("fresh operation unexpectedly observed")
	}
	beforeDownloads := downloads.Load()
	commit, err := rpc.CommitImportSelection(test.Context(), connect.NewRequest(proto.CloneOf(input)))
	if mask == 0 {
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			test.Fatal("000 did not reject with exact InvalidArgument")
		}
		after, err := rpc.GetImportCommitStatus(test.Context(), connect.NewRequest(statusRequest))
		if err != nil || after.Msg.Observation != importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_NOT_OBSERVED || after.Msg.Receipt != nil {
			test.Fatal("000 wrote a receipt")
		}
	} else {
		if err != nil || commit.Msg.Receipt == nil {
			test.Fatalf("selected real HTTP commit failed: %s", connect.CodeOf(err))
		}
		receipt := commit.Msg.Receipt
		if receipt.Phase != importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED || receipt.Revision < 1 || !bytes.Equal(receipt.OperationId, input.OperationId) || len(receipt.CreatedAssets) != len(input.ConfigurationIds) || len(receipt.SelectedSkillBindings) != len(input.SkillIds) || len(receipt.RepositoryOutcomes) != len(input.RepositoryIds) {
			test.Fatal("real HTTP receipt was not the exact completed selection")
		}
		if mask&2 != 0 && !proto.Equal(receipt.SelectedSkillBindings[0], binding) {
			test.Fatal("receipt changed one of the seven binding fields")
		}
		if mask&1 != 0 && (receipt.RepositoryOutcomes[0].CandidateId != input.RepositoryIds[0] || receipt.RepositoryOutcomes[0].Code != importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_COLLECTED || receipt.RepositoryOutcomes[0].GetRepositoryId() == "") {
			test.Fatal("real repository outcome missing")
		}
		if mask&1 != 0 {
			outcome := receipt.RepositoryOutcomes[0]
			candidate := view.Repositories[0]
			actualRepository, lookupErr := store.CollectedRepository(test.Context(), owner, outcome.GetRepositoryId())
			if lookupErr != nil || candidate.Hint == nil || outcome.CandidateId != candidate.Id || actualRepository.ID != outcome.GetRepositoryId() || actualRepository.ID == existing.ID || actualRepository.NameWithOwner != candidate.Hint.Repository || actualRepository.Branch != candidate.Hint.Branch {
				test.Fatal("receipt repository ID does not resolve to selected real DB repository")
			}
		}
		status, err := rpc.GetImportCommitStatus(test.Context(), connect.NewRequest(statusRequest))
		if err != nil || status.Msg.Observation != importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_TERMINAL || !proto.Equal(status.Msg.Receipt, receipt) {
			test.Fatal("real HTTP Status did not return the committed receipt")
		}
		replay, err := rpc.CommitImportSelection(test.Context(), connect.NewRequest(proto.CloneOf(input)))
		if err != nil || !proto.Equal(replay.Msg.Receipt, receipt) {
			test.Fatal("real HTTP replay did not return identical receipt")
		}
		afterReplay, err := rpc.GetImportCommitStatus(test.Context(), connect.NewRequest(statusRequest))
		if err != nil || afterReplay.Msg.Observation != importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_TERMINAL || !proto.Equal(afterReplay.Msg.Receipt, receipt) {
			test.Fatal("real HTTP replay changed persistent receipt")
		}
	}
	if downloads.Load() != beforeDownloads+int64(len(input.RepositoryIds)) {
		test.Fatal("unselected category, Status or replay caused extra download")
	}
	assets, err := store.ListConfigurationAssets(test.Context(), owner)
	if err != nil || len(assets) != len(input.ConfigurationIds) {
		test.Fatal("real database asset batch not exact")
	}
	for _, asset := range assets {
		edit, err := configuration.New(store).Edit(test.Context(), owner, asset.ID)
		if err != nil {
			test.Fatal("real encrypted asset readback failed")
		}
		converted, err := configurationView(edit)
		if err != nil {
			test.Fatal("real encrypted asset typed conversion failed")
		}
		var expected *configurationv1.Draft
		for _, candidate := range view.Configurations {
			if candidate.KnownAgentId == asset.AgentID {
				expected = candidate.Draft
			}
		}
		if expected == nil || !proto.Equal(converted.Draft, expected) {
			test.Fatal("real encrypted asset changed preview typed fields")
		}
		for _, slot := range edit.Secrets {
			if slot.Present {
				test.Fatal("source credential migrated to real asset")
			}
		}
	}
	repositories, err := store.ListCollectedRepositories(test.Context(), owner)
	if err != nil || len(repositories) != 1+len(input.RepositoryIds) {
		test.Fatal("real database repository selection not exact")
	}
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(filepath.Join(directory, "server.db") + suffix)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			test.Fatal("real database privacy scan failed")
		}
		if bytes.Contains(data, []byte("SYNTHETIC-PRIVATE-MARKER")) {
			test.Fatal("source plaintext leaked into persistent database")
		}
		clear(data)
	}
	if observed[0].Load() != 2 || observed[1].Load() != 1+int64(min(mask, 1)) || observed[2].Load() != 2+int64(min(mask, 1)) {
		test.Fatal("real HTTP did not traverse all three exact RPCs with the selected encoding")
	}
}
