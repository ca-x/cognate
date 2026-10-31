package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

const npxHTTPPrivateMarker = "SYNTHETIC-NPX-PRIVATE-MARKER"
const npxHTTPOrigin = "https://npx.example.test"

type npxSelectionHTTPFixture struct {
	store     *state.Store
	content   *library.Service
	server    *httptest.Server
	directory string
	owners    [2]string
	sessions  [2]auth.SessionToken
	downloads *atomic.Int64
	observed  [3]atomic.Int64
}

func newNPXSelectionHTTPFixture(test *testing.T) *npxSelectionHTTPFixture {
	test.Helper()
	fixture := &npxSelectionHTTPFixture{directory: test.TempDir()}
	if err := os.Chmod(fixture.directory, 0o700); err != nil {
		test.Fatal("private npx fixture root unavailable")
	}
	key := bytes.Repeat([]byte{96}, 32)
	defer clear(key)
	store, err := state.Open(test.Context(), filepath.Join(fixture.directory, "server.db"), key)
	if err != nil {
		test.Fatal("real npx SQLite store unavailable")
	}
	fixture.store = store
	test.Cleanup(func() {
		if err := store.Close(); err != nil {
			test.Error("npx store close failed")
		}
	})
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal("real npx authentication unavailable")
	}
	password := []byte("Synthetic npx HTTP passphrase only")
	defer clear(password)
	for index, username := range []string{"npx-owner", "npx-other"} {
		fixture.owners[index], err = authentication.Register(test.Context(), username, password)
		if err != nil {
			test.Fatal("real npx owner registration failed")
		}
		fixture.sessions[index], err = authentication.Login(test.Context(), username, password)
		if err != nil {
			test.Fatal("real npx owner login failed")
		}
	}
	cache, err := library.OpenArchiveCache(filepath.Join(fixture.directory, "archives"))
	if err != nil {
		test.Fatal("real npx archive cache unavailable")
	}
	test.Cleanup(func() { _ = cache.Close() })
	fetcher, downloads := importRoundtripFetcher(test)
	fixture.downloads = downloads
	fixture.content, err = library.NewService(store, cache, fetcher)
	if err != nil {
		test.Fatal("real npx library unavailable")
	}
	test.Cleanup(func() { _ = fixture.content.Close() })
	selection, err := importer.NewSelectionService(store, fixture.content, authentication, npxHTTPOrigin)
	if err != nil {
		test.Fatal("real npx selection service unavailable")
	}
	handler, err := NewImportHandlerWithSelection(authentication, fixture.content, selection)
	if err != nil {
		_ = selection.Close()
		test.Fatal("real npx import handler unavailable")
	}
	test.Cleanup(func() {
		if err := handler.Close(); err != nil {
			test.Error("npx import handler drain failed")
		}
	})
	fixture.server = httptest.NewTLSServer(NewWithOptions(authentication, Options{PublicOrigin: npxHTTPOrigin, Store: store, Library: fixture.content, Imports: handler}))
	test.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *npxSelectionHTTPFixture) client(test *testing.T, encoding, session string) importv1connect.ImportServiceClient {
	test.Helper()
	base := fixture.server.Client().Transport
	transport := importRoundtripTransport(func(request *http.Request) (*http.Response, error) {
		procedures := []string{importv1connect.ImportServicePreviewImportSelectionProcedure, importv1connect.ImportServiceCommitImportSelectionProcedure, importv1connect.ImportServiceGetImportCommitStatusProcedure}
		index := slices.Index(procedures, request.URL.Path)
		if index < 0 || request.URL.Scheme != "https" || request.URL.Host != fixture.server.Listener.Addr().String() || request.Header.Get("Content-Type") != "application/"+encoding {
			return nil, errors.New("npx fixture forbids unexpected RPC or encoding")
		}
		fixture.observed[index].Add(1)
		response, err := base.RoundTrip(request)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || len(body) > 2<<20 {
			return nil, errors.New("npx response unavailable or over budget")
		}
		if bytes.Contains(body, []byte(npxHTTPPrivateMarker)) {
			test.Error("npx HTTP response disclosed private source metadata")
		}
		for _, token := range fixture.sessions {
			if bytes.Contains(body, []byte(token.Reveal())) {
				test.Error("npx HTTP response disclosed an owner session")
			}
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		return response, nil
	})
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
			request.Header().Set("Origin", npxHTTPOrigin)
			if session != "" {
				request.Header().Set("Authorization", "Bearer "+session)
			}
			return next(ctx, request)
		}
	})
	options := []connect.ClientOption{connect.WithInterceptors(interceptor)}
	if encoding == "json" {
		options = append(options, connect.WithProtoJSON())
	}
	return importv1connect.NewImportServiceClient(&http.Client{Transport: transport, Timeout: 10 * time.Second}, fixture.server.URL, options...)
}

func npxSelectionHTTPSource(skillPath string) []byte {
	return fmt.Appendf(nil, `{"version":3,"skills":{"existing-review":{"sourceType":"github","source":"example/existing","ref":"main","skillPath":%q,"skillFolderHash":"1111111111111111111111111111111111111111"},"new-review":{"source_type":"github","source":"example/toolkit","sourceUrl":"https://github.com/example/toolkit","ref":"main","skillPath":%q},"excluded":{"sourceType":"gitlab","source":"outside/project"}},"dismissed":{},"private":"%s"}`, skillPath, skillPath, npxHTTPPrivateMarker)
}

func npxSelectionHTTPAck(view *importv1.PreviewImportSelectionResponse) []string {
	var identifiers []string
	for _, notice := range view.Notices {
		identifiers = append(identifiers, notice.Id)
	}
	for _, candidate := range view.Skills {
		for _, notice := range candidate.Notices {
			identifiers = append(identifiers, notice.Id)
		}
	}
	return identifiers
}

func TestImportSelectionNPXHTTPRoundtrip(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		for _, shape := range []struct{ name, path string }{{"directory", "skills/review"}, {"file", "skills/review/SKILL.md"}} {
			test.Run(encoding+"/"+shape.name, func(test *testing.T) {
				fixture := newNPXSelectionHTTPFixture(test)
				owner := fixture.owners[0]
				existing, err := fixture.content.Collect(test.Context(), owner, "example/existing", "main")
				if err != nil {
					test.Fatal("real existing repository collection failed")
				}
				existing, err = fixture.store.CollectedRepository(test.Context(), owner, existing.ID)
				if err != nil || len(existing.Skills) != 1 || existing.Skills[0].Path != "skills/review" {
					test.Fatal("real archive did not scan the expected skill directory")
				}
				rpc := fixture.client(test, encoding, fixture.sessions[0].Reveal())
				raw := npxSelectionHTTPSource(shape.path)
				defer clear(raw)
				source := importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_NPX_SKILLS_LOCK
				beforeDownloads := fixture.downloads.Load()
				initial, err := rpc.PreviewImportSelection(test.Context(), connect.NewRequest(&importv1.PreviewImportSelectionRequest{Source: source, SourceData: bytes.Clone(raw)}))
				if err != nil {
					test.Fatalf("real npx preview failed: %s", connect.CodeOf(err))
				}
				if len(initial.Msg.Repositories) != 2 || len(initial.Msg.Skills) != 2 || len(initial.Msg.Configurations) != 0 || len(initial.Msg.Ticket) != state.ImportTicketBytes {
					test.Fatal("npx preview lost GitHub candidates or included excluded configuration")
				}
				codes := make(map[importv1.ImportNoticeCode]bool)
				for _, notice := range initial.Msg.Notices {
					codes[notice.Code] = notice.Count > 0
				}
				var skillID, repositoryID string
				for _, candidate := range initial.Msg.Skills {
					for _, notice := range candidate.Notices {
						codes[notice.Code] = notice.Count > 0
					}
					if candidate.Binding != nil || candidate.Status == importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY {
						test.Fatal("npx lock provenance implicitly bound owner content")
					}
					if candidate.SafeName == "existing-review" {
						skillID = candidate.Id
					}
				}
				for _, candidate := range initial.Msg.Repositories {
					if candidate.Hint.Repository == "example/toolkit" {
						repositoryID = candidate.Id
					}
				}
				if skillID == "" || repositoryID == "" {
					test.Fatal("npx explicit selection candidates missing")
				}
				for _, code := range []importv1.ImportNoticeCode{importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_METADATA_EXCLUDED, importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_OTHER_SOURCE_DATA_EXCLUDED, importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_UNKNOWN_SOURCE_SECTION_EXCLUDED} {
					if !codes[code] {
						test.Fatalf("npx preview omitted required notice %s", code)
					}
				}
				skill := existing.Skills[0]
				binding := &importv1.SkillBinding{RepositoryId: existing.ID, ArchiveHash: existing.ArchiveHash, SkillId: skill.ID, LogicalPath: skill.Path, ContentHash: skill.ContentHash, Repository: existing.NameWithOwner, Branch: existing.Branch}
				options := []*importv1.ImportCandidateOptions{{CandidateId: skillID, SkillBinding: binding}}
				preview, err := rpc.PreviewImportSelection(test.Context(), connect.NewRequest(&importv1.PreviewImportSelectionRequest{Source: source, SourceData: bytes.Clone(raw), Options: options}))
				if err != nil {
					test.Fatalf("real npx explicit binding preview failed: %s", connect.CodeOf(err))
				}
				bound := false
				contentNotCompared := false
				for _, candidate := range preview.Msg.Skills {
					if candidate.Id == skillID {
						bound = candidate.Status == importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY && proto.Equal(candidate.Binding, binding)
						for _, notice := range candidate.Notices {
							if notice.Code == importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_SOURCE_CONTENT_NOT_COMPARED && notice.Count > 0 {
								contentNotCompared = true
							}
						}
					}
				}
				if !bound || !contentNotCompared || fixture.downloads.Load() != beforeDownloads {
					test.Fatal("npx binding, content acknowledgment or side-effect-free preview contract changed")
				}
				input := &importv1.CommitImportSelectionRequest{Ticket: bytes.Clone(preview.Msg.Ticket), Source: source, SourceData: bytes.Clone(raw), Options: options, OperationId: bytes.Repeat([]byte{97}, 16), RepositoryIds: []string{repositoryID}, SkillIds: []string{skillID}}
				statusInput := &importv1.GetImportCommitStatusRequest{OperationId: bytes.Clone(input.OperationId)}
				if response, err := rpc.CommitImportSelection(test.Context(), connect.NewRequest(proto.CloneOf(input))); connect.CodeOf(err) != connect.CodeFailedPrecondition || response != nil {
					test.Fatalf("npx missing-ack commit: code=%s response=%t", connect.CodeOf(err), response != nil)
				}
				before, err := rpc.GetImportCommitStatus(test.Context(), connect.NewRequest(proto.CloneOf(statusInput)))
				if err != nil || before.Msg.Observation != importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_NOT_OBSERVED || before.Msg.Receipt != nil || fixture.downloads.Load() != beforeDownloads {
					test.Fatal("rejected npx commit or read-only status reserved an operation")
				}
				input.AckNoticeIds = npxSelectionHTTPAck(preview.Msg)
				if len(input.AckNoticeIds) == 0 {
					test.Fatal("npx preview omitted required exclusion acknowledgments")
				}
				changed := proto.CloneOf(input)
				changed.SourceData = append(changed.SourceData, ' ')
				if response, err := rpc.CommitImportSelection(test.Context(), connect.NewRequest(changed)); connect.CodeOf(err) != connect.CodeInvalidArgument || response != nil {
					test.Fatalf("npx source-drift commit: code=%s response=%t", connect.CodeOf(err), response != nil)
				}
				commit, err := rpc.CommitImportSelection(test.Context(), connect.NewRequest(proto.CloneOf(input)))
				if err != nil {
					test.Fatalf("real npx commit failed: %s", connect.CodeOf(err))
				}
				receipt := commit.Msg.Receipt
				if receipt == nil || receipt.Phase != importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED || receipt.Revision < 1 || !bytes.Equal(receipt.OperationId, input.OperationId) || len(receipt.RepositoryOutcomes) != 1 || len(receipt.SelectedSkillBindings) != 1 || len(receipt.CreatedAssets) != 0 || !proto.Equal(receipt.SelectedSkillBindings[0], binding) {
					test.Fatal("npx receipt changed the exact repository and skill selection")
				}
				outcome := receipt.RepositoryOutcomes[0]
				collected, err := fixture.store.CollectedRepository(test.Context(), owner, outcome.GetRepositoryId())
				if err != nil || outcome.Code != importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_COLLECTED || outcome.CandidateId != repositoryID || collected.ID == existing.ID || collected.NameWithOwner != "example/toolkit" || collected.Branch != "main" || len(collected.Skills) != 1 || collected.Skills[0].Path != "skills/review" {
					test.Fatal("npx repository receipt did not resolve to the actual SQLite archive inventory")
				}
				for range 2 {
					status, err := rpc.GetImportCommitStatus(test.Context(), connect.NewRequest(proto.CloneOf(statusInput)))
					if err != nil || status.Msg.Observation != importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_TERMINAL || !proto.Equal(status.Msg.Receipt, receipt) {
						test.Fatal("npx read-only status changed the completed receipt")
					}
					replay, err := rpc.CommitImportSelection(test.Context(), connect.NewRequest(proto.CloneOf(input)))
					if err != nil || !proto.Equal(replay.Msg.Receipt, receipt) {
						test.Fatal("npx idempotent replay changed the completed receipt")
					}
				}
				other := fixture.client(test, encoding, fixture.sessions[1].Reveal())
				foreignPreview, err := other.PreviewImportSelection(test.Context(), connect.NewRequest(&importv1.PreviewImportSelectionRequest{Source: source, SourceData: bytes.Clone(raw)}))
				if err != nil {
					test.Fatal("independent npx owner preview failed")
				}
				for _, candidate := range foreignPreview.Msg.Repositories {
					if candidate.Hint.ExistingRepositoryId != "" {
						test.Fatal("npx preview leaked another owner's repository identity")
					}
				}
				for _, candidate := range foreignPreview.Msg.Skills {
					if candidate.Binding != nil {
						test.Fatal("npx preview leaked another owner's skill binding")
					}
				}
				if response, err := other.CommitImportSelection(test.Context(), connect.NewRequest(proto.CloneOf(input))); connect.CodeOf(err) != connect.CodeFailedPrecondition || response != nil {
					test.Fatal("another owner consumed the npx ticket or operation")
				}
				foreign, err := other.GetImportCommitStatus(test.Context(), connect.NewRequest(proto.CloneOf(statusInput)))
				if err != nil || foreign.Msg.Observation != importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_NOT_OBSERVED || foreign.Msg.Receipt != nil {
					test.Fatal("npx operation existence or receipt leaked to another owner")
				}
				absent, err := other.GetImportCommitStatus(test.Context(), connect.NewRequest(&importv1.GetImportCommitStatusRequest{OperationId: bytes.Repeat([]byte{98}, 16)}))
				if err != nil || !proto.Equal(absent.Msg, foreign.Msg) {
					test.Fatal("another owner could distinguish npx operation existence")
				}
				if fixture.downloads.Load() != beforeDownloads+1 {
					test.Fatal("npx rejected input, replay, foreign owner or status caused extra downloads")
				}
				for index, expected := range []int{2, 0} {
					repositories, err := fixture.store.ListCollectedRepositories(test.Context(), fixture.owners[index])
					if err != nil || len(repositories) != expected {
						test.Fatal("npx selection or foreign replay changed the actual repository inventory")
					}
					assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owners[index])
					if err != nil || len(assets) != 0 {
						test.Fatal("npx selection imported configuration assets")
					}
				}
				for _, suffix := range []string{"", "-wal"} {
					data, err := os.ReadFile(filepath.Join(fixture.directory, "server.db") + suffix)
					if err != nil && !errors.Is(err, os.ErrNotExist) {
						test.Fatal("npx database privacy scan failed")
					}
					if bytes.Contains(data, []byte(npxHTTPPrivateMarker)) {
						test.Fatal("npx raw private source metadata persisted in SQLite")
					}
					clear(data)
				}
				for _, observed := range []*atomic.Int64{&fixture.observed[0], &fixture.observed[1], &fixture.observed[2]} {
					if observed.Load() == 0 {
						test.Fatal("npx real TLS client did not traverse all three RPCs")
					}
				}
			})
		}
	}
}

func TestImportSelectionNPXHTTPInvalidSourceAndAuthentication(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			fixture := newNPXSelectionHTTPFixture(test)
			rpc := fixture.client(test, encoding, fixture.sessions[0].Reveal())
			source := importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_NPX_SKILLS_LOCK
			for _, invalid := range []struct {
				name, raw string
				code      connect.Code
			}{
				{"schema-version", `{"version":2,"skills":{}}`, connect.CodeFailedPrecondition},
				{"skills-array", `{"version":3,"skills":[]}`, connect.CodeInvalidArgument},
				{"duplicate-key", `{"version":3,"version":3,"skills":{}}`, connect.CodeInvalidArgument},
				{"truncated", `{"version":3,"skills":{`, connect.CodeInvalidArgument},
				{"type-conflict", `{"version":3,"skills":{"review":{"source":"example/toolkit","sourceType":"github","source_type":"gitlab"}}}`, connect.CodeInvalidArgument},
				{"path-traversal", `{"version":3,"skills":{"review":{"source":"example/toolkit","sourceType":"github","skillPath":"../skills/review"}}}`, connect.CodeInvalidArgument},
			} {
				test.Run(invalid.name, func(test *testing.T) {
					response, err := rpc.PreviewImportSelection(test.Context(), connect.NewRequest(&importv1.PreviewImportSelectionRequest{Source: source, SourceData: []byte(invalid.raw)}))
					if connect.CodeOf(err) != invalid.code || response != nil {
						test.Fatalf("invalid npx source: code=%s response=%t", connect.CodeOf(err), response != nil)
					}
				})
			}
			raw := npxSelectionHTTPSource("skills/review")
			defer clear(raw)
			operation := bytes.Repeat([]byte{99}, 16)
			for _, unauthorized := range []struct{ name, token string }{{"unsigned", ""}, {"forged-session", strings.Repeat("U", 43) + "." + strings.Repeat("V", 43)}} {
				test.Run(unauthorized.name, func(test *testing.T) {
					client := fixture.client(test, encoding, unauthorized.token)
					preview, previewErr := client.PreviewImportSelection(test.Context(), connect.NewRequest(&importv1.PreviewImportSelectionRequest{Source: source, SourceData: bytes.Clone(raw)}))
					commit, commitErr := client.CommitImportSelection(test.Context(), connect.NewRequest(&importv1.CommitImportSelectionRequest{Source: source, SourceData: bytes.Clone(raw), Ticket: bytes.Repeat([]byte{1}, state.ImportTicketBytes), OperationId: bytes.Clone(operation)}))
					status, statusErr := client.GetImportCommitStatus(test.Context(), connect.NewRequest(&importv1.GetImportCommitStatusRequest{OperationId: bytes.Clone(operation)}))
					if preview != nil || commit != nil || status != nil {
						test.Fatal("unsigned npx request returned private business data")
					}
					for _, err := range []error{previewErr, commitErr, statusErr} {
						if connect.CodeOf(err) != connect.CodeUnauthenticated {
							test.Fatalf("unsigned npx request: code=%s", connect.CodeOf(err))
						}
					}
					response, err := client.PreviewImportSelection(test.Context(), connect.NewRequest(&importv1.PreviewImportSelectionRequest{Source: source, SourceData: []byte(`{"version":2,"skills":{}}`)}))
					if connect.CodeOf(err) != connect.CodeUnauthenticated || response != nil {
						test.Fatal("npx schema parsing ran ahead of authentication")
					}
				})
			}
			status, err := rpc.GetImportCommitStatus(test.Context(), connect.NewRequest(&importv1.GetImportCommitStatusRequest{OperationId: bytes.Clone(operation)}))
			if err != nil || status.Msg.Observation != importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_NOT_OBSERVED || status.Msg.Receipt != nil || fixture.downloads.Load() != 0 {
				test.Fatal("invalid or unsigned npx requests reserved an operation or downloaded content")
			}
			for _, owner := range fixture.owners {
				repositories, err := fixture.store.ListCollectedRepositories(test.Context(), owner)
				if err != nil || len(repositories) != 0 {
					test.Fatal("invalid or unsigned npx requests persisted repositories")
				}
				assets, err := fixture.store.ListConfigurationAssets(test.Context(), owner)
				if err != nil || len(assets) != 0 {
					test.Fatal("invalid or unsigned npx requests persisted configuration assets")
				}
			}
		})
	}
}
