package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"github.com/ca-x/cognate/internal/importclient"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

func TestSelectionClientRealStore(test *testing.T) {
	for mask := 1; mask <= 7; mask++ {
		for _, lost := range []bool{false, true} {
			test.Run(fmt.Sprintf("%03b/lost=%t", mask, lost), func(test *testing.T) { selectionClientRealStore(test, mask, lost) })
		}
	}
}

func selectionClientRealStore(test *testing.T, mask int, lost bool) {
	directory := test.TempDir()
	if os.Chmod(directory, 0o700) != nil {
		test.Fatal("private fixture directory unavailable")
	}
	key := bytes.Repeat([]byte{91}, 32)
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		test.Fatal("store unavailable")
	}
	test.Cleanup(func() {
		if store.Close() != nil {
			test.Error("store close failed")
		}
	})
	identity, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal("auth unavailable")
	}
	password := []byte("Synthetic client roundtrip passphrase")
	defer clear(password)
	owner, err := identity.Register(test.Context(), "selection-client-owner", password)
	if err != nil {
		test.Fatal("owner register failed")
	}
	session, err := identity.Login(test.Context(), "selection-client-owner", password)
	if err != nil {
		test.Fatal("owner login failed")
	}
	cache, err := library.OpenArchiveCache(filepath.Join(directory, "archives"))
	if err != nil {
		test.Fatal("cache unavailable")
	}
	test.Cleanup(func() { _ = cache.Close() })
	fetcher, downloads := importRoundtripFetcher(test)
	content, err := library.NewService(store, cache, fetcher)
	if err != nil {
		test.Fatal("library unavailable")
	}
	test.Cleanup(func() { _ = content.Close() })
	existing, err := content.Collect(test.Context(), owner, "example/existing", "")
	if err != nil {
		test.Fatal("existing content unavailable")
	}
	existing, err = store.CollectedRepository(test.Context(), owner, existing.ID)
	if err != nil || len(existing.Skills) != 1 {
		test.Fatal("existing skill inventory missing")
	}
	const origin = "https://cognate.example.test"
	selection, err := importer.NewSelectionService(store, content, identity, origin)
	if err != nil {
		test.Fatal("selection service unavailable")
	}
	handler, err := NewImportHandlerWithSelection(identity, content, selection)
	if err != nil {
		_ = selection.Close()
		test.Fatal("handler unavailable")
	}
	test.Cleanup(func() {
		if handler.Close() != nil {
			test.Error("handler close failed")
		}
	})
	server := httptest.NewTLSServer(NewWithOptions(identity, Options{PublicOrigin: origin, Store: store, Library: content, Imports: handler}))
	test.Cleanup(server.Close)
	var commits, statuses atomic.Int32
	transport := server.Client().Transport
	settings := &http.Client{Transport: importRoundtripTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != server.Listener.Addr().String() || request.URL.Scheme != "https" || request.Header.Get("Content-Type") != "application/proto" {
			return nil, errors.New("unexpected client RPC origin or encoding")
		}
		if request.URL.Path == importv1connect.ImportServiceCommitImportSelectionProcedure {
			commits.Add(1)
		}
		if request.URL.Path == importv1connect.ImportServiceGetImportCommitStatusProcedure {
			statuses.Add(1)
		}
		response, err := transport.RoundTrip(request)
		if err == nil && lost && request.URL.Path == importv1connect.ImportServiceCommitImportSelectionProcedure {
			_ = response.Body.Close()
			return nil, errors.New("synthetic lost receipt private marker")
		}
		return response, err
	})}
	client, err := importclient.NewSelectionClient(server.URL, session.Reveal(), settings)
	if err != nil {
		test.Fatal("client unavailable")
	}
	test.Cleanup(client.Close)
	raw := importRoundtripSnapshot(test)
	path := filepath.Join(directory, "snapshot.db")
	if os.WriteFile(path, raw, 0o600) != nil {
		test.Fatal("source file unavailable")
	}
	source := importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24
	initial, err := client.PreviewFile(test.Context(), source, path, "", nil)
	if err != nil {
		test.Fatalf("real client initial preview rejected: %v", err)
	}
	hash := initial.SHA256()
	view := initial.View()
	var options []*importv1.ImportCandidateOptions
	for _, candidate := range view.Configurations {
		switch candidate.KnownAgentId {
		case "claude":
			options = append(options, &importv1.ImportCandidateOptions{CandidateId: candidate.Id, Auth: &importv1.ImportCandidateOptions_ClaudeAuthMode{ClaudeAuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY}})
		case "gemini":
			options = append(options, &importv1.ImportCandidateOptions{CandidateId: candidate.Id, GeminiKeyTransport: new(configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_BEARER)})
		}
	}
	skill := existing.Skills[0]
	binding := &importv1.SkillBinding{RepositoryId: existing.ID, ArchiveHash: existing.ArchiveHash, SkillId: skill.ID, LogicalPath: skill.Path, ContentHash: skill.ContentHash, Repository: existing.NameWithOwner, Branch: existing.Branch}
	options = append(options, &importv1.ImportCandidateOptions{CandidateId: view.Skills[0].Id, SkillBinding: binding})
	initial.Close()
	preview, err := client.PreviewFile(test.Context(), source, path, hash, options)
	if err != nil {
		test.Fatalf("real client explicit choice rejected: %v", err)
	}
	defer preview.Close()
	view = preview.View()
	choice := importclient.SelectionChoice{}
	if mask&1 != 0 {
		choice.Repositories = []int{1}
	}
	if mask&2 != 0 {
		choice.Skills = []int{1}
	}
	for index, candidate := range view.Configurations {
		if mask&4 != 0 && candidate.Status == importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY {
			choice.Configurations = append(choice.Configurations, index+1)
		}
	}
	for _, notice := range selectionNoticesForClient(view) {
		choice.AcknowledgedNotices = append(choice.AcknowledgedNotices, notice.Id)
	}
	operation, err := client.BeginSelection(preview, choice)
	if err != nil {
		test.Fatalf("real client selection rejected: %v", err)
	}
	defer operation.Close()
	before := downloads.Load()
	result, err := operation.CommitFile(test.Context(), path)
	if lost {
		if !errors.Is(err, importclient.ErrSelectionUnknown) {
			test.Fatal("lost real receipt did not remain unknown")
		}
	} else if err != nil || !result.SelectionVerified {
		test.Fatalf("real commit rejected: %v", err)
	}
	result, err = client.Status(test.Context(), operation.ID())
	if err != nil || !result.SelectionVerified || result.Phase != importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED || result.Repositories != len(choice.Repositories) || result.Skills != len(choice.Skills) || result.Configurations != len(choice.Configurations) {
		test.Fatalf("real Status not exact: %+v %v", result, err)
	}
	if _, err := operation.CommitFile(test.Context(), path); err == nil || commits.Load() != 1 || statuses.Load() != 1 {
		test.Fatal("client replayed commit")
	}
	if downloads.Load() != before+int64(len(choice.Repositories)) {
		test.Fatal("unselected resource or status downloaded content")
	}
	assets, err := store.ListConfigurationAssets(test.Context(), owner)
	if err != nil || len(assets) != len(choice.Configurations) {
		test.Fatal("actual asset set differs")
	}
	for _, asset := range assets {
		edit, err := configuration.New(store).Edit(test.Context(), owner, asset.ID)
		if err != nil {
			test.Fatal("actual asset readback unavailable")
		}
		actual, err := configurationView(edit)
		if err != nil {
			test.Fatal("actual typed conversion unavailable")
		}
		var expected *configurationv1.Draft
		for _, candidate := range view.Configurations {
			if candidate.KnownAgentId == asset.AgentID {
				expected = candidate.Draft
			}
		}
		if expected == nil || !proto.Equal(actual.Draft, expected) {
			test.Fatal("client import lost typed fields")
		}
		for _, slot := range edit.Secrets {
			if slot.Present {
				test.Fatal("source secret migrated without authorization")
			}
		}
	}
	repositories, err := store.ListCollectedRepositories(test.Context(), owner)
	if err != nil || len(repositories) != 1+len(choice.Repositories) {
		test.Fatal("actual repository set differs")
	}
	for _, entry := range repositories {
		if entry.ID != existing.ID && entry.NameWithOwner != view.Repositories[0].Hint.Repository {
			test.Fatal("receipt points to the wrong actual repository")
		}
	}
}

func selectionNoticesForClient(view *importv1.PreviewImportSelectionResponse) []*importv1.ImportNotice {
	result := append([]*importv1.ImportNotice{}, view.Notices...)
	for _, entry := range view.Skills {
		result = append(result, entry.Notices...)
	}
	for _, entry := range view.Configurations {
		result = append(result, entry.Notices...)
	}
	return result
}
