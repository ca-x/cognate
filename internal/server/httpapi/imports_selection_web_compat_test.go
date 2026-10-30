package httpapi

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

func TestImportSelectionIndependentSkillRepository(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		test.Run(encoding, func(test *testing.T) {
			directory := test.TempDir()
			if err := os.Chmod(directory, 0700); err != nil {
				test.Fatal("private synthetic directory unavailable")
			}
			key := bytes.Repeat([]byte{75}, 32)
			defer clear(key)
			store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), key)
			if err != nil {
				test.Fatal("synthetic store unavailable")
			}
			test.Cleanup(func() {
				if err := store.Close(); err != nil {
					test.Error("store close")
				}
			})
			service, err := auth.NewService(store, key)
			if err != nil {
				test.Fatal("synthetic authentication unavailable")
			}
			password := []byte("Synthetic selection compatibility password")
			defer clear(password)
			owner, err := service.Register(test.Context(), "compat-owner", password)
			if err != nil {
				test.Fatal("synthetic owner unavailable")
			}
			other, err := service.Register(test.Context(), "compat-other", password)
			if err != nil {
				test.Fatal("synthetic other owner unavailable")
			}
			token, err := service.Login(test.Context(), "compat-owner", password)
			if err != nil {
				test.Fatal("synthetic session unavailable")
			}
			cache, err := library.OpenArchiveCache(filepath.Join(directory, "archives"))
			if err != nil {
				test.Fatal("archive cache unavailable")
			}
			test.Cleanup(func() { _ = cache.Close() })
			fetcher, downloads := importRoundtripFetcher(test)
			content, err := library.NewService(store, cache, fetcher)
			if err != nil {
				test.Fatal("real library unavailable")
			}
			test.Cleanup(func() { _ = content.Close() })
			collected, err := content.Collect(test.Context(), owner, "example/existing", "")
			if err != nil {
				test.Fatal("owner seed unavailable")
			}
			existing, err := store.CollectedRepository(test.Context(), owner, collected.ID)
			if err != nil || len(existing.Skills) != 1 {
				test.Fatal("owner seed skill unavailable")
			}
			foreign, err := content.Collect(test.Context(), other, "example/existing", "")
			if err != nil {
				test.Fatal("foreign seed unavailable")
			}
			foreign, err = store.CollectedRepository(test.Context(), other, foreign.ID)
			if err != nil || len(foreign.Skills) != 1 {
				test.Fatal("foreign seed skill unavailable")
			}
			const origin = "https://compat.example.test"
			selection, err := importer.NewSelectionService(store, content, service, origin)
			if err != nil {
				test.Fatal("real selection unavailable")
			}
			handler, err := NewImportHandlerWithSelection(service, content, selection)
			if err != nil {
				_ = selection.Close()
				test.Fatal("real handler unavailable")
			}
			test.Cleanup(func() {
				if err := handler.Close(); err != nil {
					test.Error("handler drain")
				}
			})
			server := httptest.NewTLSServer(NewWithOptions(service, Options{PublicOrigin: origin, Store: store, Library: content, Imports: handler}))
			test.Cleanup(server.Close)
			interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
				return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
					request.Header().Set("Authorization", "Bearer "+token.Reveal())
					request.Header().Set("Origin", origin)
					return next(ctx, request)
				}
			})
			options := []connect.ClientOption{connect.WithInterceptors(interceptor)}
			if encoding == "json" {
				options = append(options, connect.WithProtoJSON())
			}
			client := importv1connect.NewImportServiceClient(server.Client(), server.URL, options...)
			raw := importRoundtripSnapshot(test)
			defer clear(raw)
			input := &importv1.PreviewImportSelectionRequest{Source: importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24, SourceData: raw}
			beforeDownloads := downloads.Load()
			initial, err := client.PreviewImportSelection(test.Context(), connect.NewRequest(input))
			if err != nil || len(initial.Msg.Repositories) != 1 || len(initial.Msg.Skills) != 1 {
				test.Fatal("real initial preview unavailable")
			}
			candidate := initial.Msg.Skills[0]
			if candidate.RepositoryCandidateId != "" || candidate.Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_UNRESOLVED || candidate.Binding != nil {
				test.Fatal("independent source skill must be unresolved with no repository association")
			}
			skill := existing.Skills[0]
			binding := &importv1.SkillBinding{RepositoryId: existing.ID, ArchiveHash: existing.ArchiveHash, SkillId: skill.ID, LogicalPath: skill.Path, ContentHash: skill.ContentHash, Repository: existing.NameWithOwner, Branch: existing.Branch}
			foreignSkill := foreign.Skills[0]
			foreignBinding := &importv1.SkillBinding{RepositoryId: foreign.ID, ArchiveHash: foreign.ArchiveHash, SkillId: foreignSkill.ID, LogicalPath: foreignSkill.Path, ContentHash: foreignSkill.ContentHash, Repository: foreign.NameWithOwner, Branch: foreign.Branch}
			badHash := proto.CloneOf(binding)
			badHash.ContentHash = "not-a-content-hash"
			wrongPin := proto.CloneOf(binding)
			wrongPin.ContentHash = string(bytes.Repeat([]byte{'a'}, 64))
			for _, invalid := range []*importv1.SkillBinding{foreignBinding, badHash, wrongPin} {
				request := proto.CloneOf(input)
				request.Options = []*importv1.ImportCandidateOptions{{CandidateId: candidate.Id, SkillBinding: invalid}}
				response, err := client.PreviewImportSelection(test.Context(), connect.NewRequest(request))
				if err == nil && response.Msg.Skills[0].Status == importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY {
					test.Fatal("invalid or foreign binding became Ready")
				}
			}
			input.Options = []*importv1.ImportCandidateOptions{{CandidateId: candidate.Id, SkillBinding: binding}}
			ready, err := client.PreviewImportSelection(test.Context(), connect.NewRequest(input))
			if err != nil || len(ready.Msg.Skills) != 1 {
				test.Fatal("explicit binding preview unavailable")
			}
			candidate = ready.Msg.Skills[0]
			if candidate.RepositoryCandidateId != "" || candidate.Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY || !proto.Equal(candidate.Binding, binding) {
				test.Fatal("explicit seven-pin binding lost or association invented")
			}
			commit := &importv1.CommitImportSelectionRequest{Ticket: slices.Clone(ready.Msg.Ticket), Source: input.Source, SourceData: raw, Options: input.Options, SkillIds: []string{candidate.Id}, OperationId: bytes.Repeat([]byte{77}, 16)}
			for _, notice := range ready.Msg.Notices {
				commit.AckNoticeIds = append(commit.AckNoticeIds, notice.Id)
			}
			for _, entry := range ready.Msg.Skills {
				for _, notice := range entry.Notices {
					commit.AckNoticeIds = append(commit.AckNoticeIds, notice.Id)
				}
			}
			for _, entry := range ready.Msg.Configurations {
				for _, notice := range entry.Notices {
					commit.AckNoticeIds = append(commit.AckNoticeIds, notice.Id)
				}
			}
			withoutAck := proto.CloneOf(commit)
			withoutAck.AckNoticeIds = nil
			if _, err := client.CommitImportSelection(test.Context(), connect.NewRequest(withoutAck)); err == nil {
				test.Fatal("missing acknowledgement committed")
			}
			response, err := client.CommitImportSelection(test.Context(), connect.NewRequest(commit))
			if err != nil || response.Msg.Receipt == nil {
				test.Fatal("skill-only commit unavailable")
			}
			receipt := response.Msg.Receipt
			if receipt.Phase != importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED || len(receipt.CreatedAssets) != 0 || len(receipt.RepositoryOutcomes) != 0 || len(receipt.SelectedSkillBindings) != 1 || !proto.Equal(receipt.SelectedSkillBindings[0], binding) {
				test.Fatal("skill-only receipt changed selection or seven pins")
			}
			status, err := client.GetImportCommitStatus(test.Context(), connect.NewRequest(&importv1.GetImportCommitStatusRequest{OperationId: commit.OperationId}))
			if err != nil || !proto.Equal(status.Msg.Receipt, receipt) {
				test.Fatal("durable receipt changed")
			}
			repositories, err := store.ListCollectedRepositories(test.Context(), owner)
			if err != nil || len(repositories) != 1 || repositories[0].ID != existing.ID || downloads.Load() != beforeDownloads {
				test.Fatal("preview or skill-only commit implicitly collected")
			}
			assets, err := store.ListConfigurationAssets(test.Context(), owner)
			if err != nil || len(assets) != 0 {
				test.Fatal("skill-only commit created assets")
			}
			if err := service.Logout(test.Context(), token.Reveal()); err != nil {
				test.Fatal("synthetic revocation unavailable")
			}
			revoked := proto.CloneOf(commit)
			revoked.OperationId = bytes.Repeat([]byte{78}, 16)
			if _, err := client.CommitImportSelection(test.Context(), connect.NewRequest(revoked)); connect.CodeOf(err) != connect.CodeUnauthenticated {
				test.Fatal("revoked owner session committed")
			}
			test.Log("REAL_TLS_SQLITE_EMPTY_ASSOCIATION_EXPLICIT_BINDING_SEVEN_PINS_ZERO_COLLECT")
		})
	}
}
