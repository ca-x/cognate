package importclient_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"github.com/ca-x/cognate/internal/importclient"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type selectionRPC struct {
	importv1connect.UnimplementedImportServiceHandler
	preview       *importv1.PreviewImportSelectionResponse
	commits       atomic.Int32
	statuses      atomic.Int32
	last          *importv1.CommitImportSelectionRequest
	lost          bool
	previews      atomic.Int32
	mutate        func(*importv1.ImportReceipt)
	started       chan struct{}
	release       chan struct{}
	previewBudget atomic.Int64
	commitBudget  atomic.Int64
	statusBudget  atomic.Int64
}

func selectionFixture(test *testing.T) (*importclient.SelectionClient, *selectionRPC, string) {
	test.Helper()
	fixture := &selectionRPC{preview: &importv1.PreviewImportSelectionResponse{
		Ticket: bytes.Repeat([]byte{7}, 290), ExpiresMs: time.Now().Add(4 * time.Minute).UnixMilli(),
		Repositories: []*importv1.ImportRepositoryCandidate{{Id: strings.Repeat("a", 64), Status: importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY, Hint: &importv1.RepositoryCandidate{Repository: "example/toolkit"}}},
		Skills: []*importv1.ImportSkillCandidate{{Id: strings.Repeat("b", 64), SafeName: "tool", RepositoryCandidateId: strings.Repeat("a", 64), Status: importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY,
			Binding: &importv1.SkillBinding{RepositoryId: "repository", ArchiveHash: strings.Repeat("d", 64), SkillId: "skill", LogicalPath: "tool/SKILL.md", ContentHash: strings.Repeat("e", 64), Repository: "example/toolkit", Branch: "main"}}},
		Notices:        []*importv1.ImportNotice{{Id: strings.Repeat("f", 64), Code: importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_CREDENTIALS_OMITTED, Count: 1}},
		Configurations: []*importv1.ImportConfigurationCandidate{{Id: strings.Repeat("c", 64), KnownAgentId: "gemini", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, Status: importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY, SafeName: "Gemini profile", Draft: &configurationv1.Draft{Name: "Gemini profile", Payload: &configurationv1.Draft_GeminiProvider{GeminiProvider: &configurationv1.GeminiProvider{AuthMode: configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_NATIVE_LOGIN}}}, CredentialSlots: []*importv1.ImportCredentialSlot{{SlotId: "gemini.api-key"}, {SlotId: "gemini.legacy-api-key"}}}},
	}}
	mux := http.NewServeMux()
	mux.Handle(importv1connect.NewImportServiceHandler(fixture))
	server := httptest.NewTLSServer(mux)
	test.Cleanup(server.Close)
	client, err := importclient.NewSelectionClient(server.URL, session, server.Client())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(client.Close)
	return client, fixture, exportFile(test, []byte(`{"skills":[]}`))
}

func (fixture *selectionRPC) PreviewImportSelection(_ context.Context, request *connect.Request[importv1.PreviewImportSelectionRequest]) (*connect.Response[importv1.PreviewImportSelectionResponse], error) {
	budget, _ := strconv.ParseInt(request.Header().Get("Connect-Timeout-Ms"), 10, 64)
	fixture.previewBudget.Store(budget)
	fixture.previews.Add(1)
	if request.Header().Get("Authorization") != "Bearer "+session || len(request.Msg.SourceData) == 0 {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("fixture"))
	}
	return connect.NewResponse(proto.Clone(fixture.preview).(*importv1.PreviewImportSelectionResponse)), nil
}

func (fixture *selectionRPC) CommitImportSelection(_ context.Context, request *connect.Request[importv1.CommitImportSelectionRequest]) (*connect.Response[importv1.CommitImportSelectionResponse], error) {
	budget, _ := strconv.ParseInt(request.Header().Get("Connect-Timeout-Ms"), 10, 64)
	fixture.commitBudget.Store(budget)
	fixture.commits.Add(1)
	fixture.last = proto.Clone(request.Msg).(*importv1.CommitImportSelectionRequest)
	if fixture.started != nil {
		close(fixture.started)
		<-fixture.release
	}
	if fixture.lost {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("private-upstream-secret"))
	}
	return connect.NewResponse(&importv1.CommitImportSelectionResponse{Receipt: fixture.receipt(request.Msg.OperationId)}), nil
}

func (fixture *selectionRPC) receipt(operation []byte) *importv1.ImportReceipt {
	receipt := &importv1.ImportReceipt{OperationId: bytes.Clone(operation), Revision: 2, Phase: importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED, Notices: fixture.preview.Notices}
	for _, id := range fixture.last.GetRepositoryIds() {
		receipt.RepositoryOutcomes = append(receipt.RepositoryOutcomes, &importv1.ImportRepositoryOutcome{CandidateId: id, Code: importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_COLLECTED, RepositoryId: new("repository")})
	}
	for range fixture.last.GetSkillIds() {
		receipt.SelectedSkillBindings = append(receipt.SelectedSkillBindings, proto.Clone(fixture.preview.Skills[0].Binding).(*importv1.SkillBinding))
	}
	for range fixture.last.GetConfigurationIds() {
		receipt.CreatedAssets = append(receipt.CreatedAssets, &configurationv1.Asset{Id: "asset", AgentId: "gemini", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, Name: "Gemini profile", SchemaVersion: 1, Revision: 1})
	}
	if fixture.mutate != nil {
		fixture.mutate(receipt)
	}
	return receipt
}

func (fixture *selectionRPC) GetImportCommitStatus(_ context.Context, request *connect.Request[importv1.GetImportCommitStatusRequest]) (*connect.Response[importv1.GetImportCommitStatusResponse], error) {
	budget, _ := strconv.ParseInt(request.Header().Get("Connect-Timeout-Ms"), 10, 64)
	fixture.statusBudget.Store(budget)
	fixture.statuses.Add(1)
	receipt := fixture.receipt(request.Msg.OperationId)
	observation := importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_TERMINAL
	if receipt.Phase < 3 {
		observation = importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_IN_PROGRESS
	}
	return connect.NewResponse(&importv1.GetImportCommitStatusResponse{Observation: observation, Receipt: receipt}), nil
}

func TestSelectionOneShotAndStatus(test *testing.T) {
	for _, lost := range []bool{false, true} {
		test.Run(map[bool]string{false: "receipt", true: "lost"}[lost], func(test *testing.T) {
			client, fixture, path := selectionFixture(test)
			fixture.lost = lost
			preview, err := client.PreviewFile(test.Context(), importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON, path, "", nil)
			if err != nil {
				test.Fatalf("preview rejected: %v", err)
			}
			defer preview.Close()
			view := preview.View()
			if len(view.Ticket) != 0 || len(view.Repositories) != 1 || preview.SHA256() == "" {
				test.Fatal("unsafe or empty preview view")
			}
			choice := importclient.SelectionChoice{Repositories: []int{1}, Skills: []int{1}, AcknowledgedNotices: []string{view.Notices[0].Id}}
			operation, err := client.BeginSelection(preview, choice)
			if err != nil {
				test.Fatal(err)
			}
			defer operation.Close()
			if decoded, err := hex.DecodeString(operation.ID()); err != nil || len(decoded) != 16 {
				test.Fatal("missing pre-dispatch operation identity")
			}
			view.Repositories[0].Id = strings.Repeat("0", 64)
			choice.Repositories[0] = 999
			result, err := operation.CommitFile(test.Context(), path)
			if lost {
				if !errors.Is(err, importclient.ErrSelectionUnknown) || strings.Contains(err.Error(), "private-upstream-secret") {
					test.Fatal("lost reply not safely unknown")
				}
			} else if err != nil || result.Repositories != 1 || result.Skills != 1 {
				test.Fatalf("bad commit result: %+v %v", result, err)
			}
			if fixture.last.RepositoryIds[0] != strings.Repeat("a", 64) || len(fixture.last.SourceData) == 0 {
				test.Fatal("caller mutated commit")
			}
			if _, err := operation.CommitFile(test.Context(), path); err == nil || fixture.commits.Load() != 1 {
				test.Fatal("commit retried")
			}
			status, err := client.Status(test.Context(), operation.ID())
			if err != nil || status.Phase != importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED || fixture.statuses.Load() != 1 || fixture.commits.Load() != 1 {
				test.Fatalf("status did not recover without replay: %+v %v", status, err)
			}
		})
	}
}

func TestSelectionExplicitChoiceAndPin(test *testing.T) {
	client, fixture, path := selectionFixture(test)
	preview, err := client.PreviewFile(test.Context(), importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON, path, "", nil)
	if err != nil {
		test.Fatalf("preview rejected: %v", err)
	}
	defer preview.Close()
	for _, choice := range []importclient.SelectionChoice{{}, {Repositories: []int{1}}, {Repositories: []int{1, 1}, AcknowledgedNotices: []string{strings.Repeat("f", 64)}}, {Skills: []int{2}, AcknowledgedNotices: []string{strings.Repeat("f", 64)}}} {
		if _, err := client.BeginSelection(preview, choice); err == nil {
			test.Fatal("invalid choice accepted")
		}
	}
	if fixture.commits.Load() != 0 {
		test.Fatal("validation wrote")
	}
	if _, err := client.PreviewFile(test.Context(), importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON, path, strings.Repeat("0", 64), nil); err == nil {
		test.Fatal("mismatched source pin accepted")
	}
}

func TestSelectionSevenCombinations(test *testing.T) {
	for combination := 1; combination <= 7; combination++ {
		test.Run(string(rune('0'+combination)), func(test *testing.T) {
			client, fixture, path := selectionFixture(test)
			preview, err := client.PreviewFile(test.Context(), importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON, path, "", nil)
			if err != nil {
				test.Fatal(err)
			}
			defer preview.Close()
			choice := importclient.SelectionChoice{AcknowledgedNotices: []string{strings.Repeat("f", 64)}}
			if combination&1 != 0 {
				choice.Repositories = []int{1}
			}
			if combination&2 != 0 {
				choice.Skills = []int{1}
			}
			if combination&4 != 0 {
				choice.Configurations = []int{1}
			}
			operation, err := client.BeginSelection(preview, choice)
			if err != nil {
				test.Fatal(err)
			}
			defer operation.Close()
			result, err := operation.CommitFile(test.Context(), path)
			if err != nil || !result.SelectionVerified || result.Repositories != len(choice.Repositories) || result.Skills != len(choice.Skills) || result.Configurations != len(choice.Configurations) || fixture.commits.Load() != 1 {
				test.Fatalf("wrong selected transaction: %+v %v", result, err)
			}
			if _, err := client.BeginSelection(preview, choice); err == nil {
				test.Fatal("preview used for a second operation")
			}
		})
	}
}

func TestSelectionRejectsMalformedPreview(test *testing.T) {
	cases := map[string]func(*importv1.PreviewImportSelectionResponse){
		"ticket": func(view *importv1.PreviewImportSelectionResponse) { view.Ticket = view.Ticket[:289] },
		"expired": func(view *importv1.PreviewImportSelectionResponse) {
			view.ExpiresMs = time.Now().Add(-time.Minute).UnixMilli()
		},
		"future": func(view *importv1.PreviewImportSelectionResponse) {
			view.ExpiresMs = time.Now().Add(6 * time.Minute).UnixMilli()
		},
		"duplicate-id": func(view *importv1.PreviewImportSelectionResponse) { view.Skills[0].Id = view.Repositories[0].Id },
		"status":       func(view *importv1.PreviewImportSelectionResponse) { view.Skills[0].Status = 99 },
		"binding":      func(view *importv1.PreviewImportSelectionResponse) { view.Skills[0].Binding.LogicalPath = "../foreign" },
		"unknown-field": func(view *importv1.PreviewImportSelectionResponse) {
			view.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
		},
		"missing-slot": func(view *importv1.PreviewImportSelectionResponse) { view.Configurations[0].CredentialSlots = nil },
		"unknown-slot": func(view *importv1.PreviewImportSelectionResponse) {
			view.Configurations[0].CredentialSlots[0].SlotId = "synthetic-private-slot"
		},
		"missing-draft":  func(view *importv1.PreviewImportSelectionResponse) { view.Configurations[0].Draft = nil },
		"unknown-notice": func(view *importv1.PreviewImportSelectionResponse) { view.Notices[0].Code = 99 },
	}
	for name, mutate := range cases {
		test.Run(name, func(test *testing.T) {
			client, fixture, path := selectionFixture(test)
			mutate(fixture.preview)
			if _, err := client.PreviewFile(test.Context(), importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON, path, "", nil); err == nil {
				test.Fatal("malformed preview accepted")
			}
			if fixture.commits.Load() != 0 {
				test.Fatal("malformed preview wrote")
			}
		})
	}
}

func TestSelectionPinsFileOwnerAndExpiry(test *testing.T) {
	client, fixture, path := selectionFixture(test)
	preview, err := client.PreviewFile(test.Context(), 2, path, "", nil)
	if err != nil {
		test.Fatal(err)
	}
	defer preview.Close()
	other, _, _ := selectionFixture(test)
	choice := importclient.SelectionChoice{Repositories: []int{1}, AcknowledgedNotices: []string{strings.Repeat("f", 64)}}
	if _, err := other.BeginSelection(preview, choice); err == nil {
		test.Fatal("foreign client consumed preview")
	}
	operation, err := client.BeginSelection(preview, choice)
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	if os.WriteFile(path, []byte("different snapshot"), 0o600) != nil {
		test.Fatal("fixture file mutation failed")
	}
	if _, err := operation.CommitFile(test.Context(), path); err == nil || fixture.commits.Load() != 0 {
		test.Fatal("changed file dispatched")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := operation.CommitFile(ctx, path); !errors.Is(err, context.Canceled) || fixture.commits.Load() != 0 {
		test.Fatal("pre-cancel dispatched")
	}
	client.Close()
	if _, err := client.Status(test.Context(), operation.ID()); err == nil {
		test.Fatal("closed client requested status")
	}
	var zero importclient.SelectionClient
	zero.Close()
	if _, err := zero.PreviewFile(test.Context(), 2, path, "", nil); err == nil {
		test.Fatal("zero client accepted")
	}
}

func TestSelectionReceiptIdentityAndRegression(test *testing.T) {
	cases := map[string]func(*importv1.ImportReceipt){
		"wrong-operation": func(receipt *importv1.ImportReceipt) { receipt.OperationId = bytes.Repeat([]byte{1}, 16) },
		"wrong-repository": func(receipt *importv1.ImportReceipt) {
			receipt.RepositoryOutcomes[0].CandidateId = strings.Repeat("0", 64)
		},
		"missing-outcome": func(receipt *importv1.ImportReceipt) { receipt.RepositoryOutcomes = nil },
		"false-completed": func(receipt *importv1.ImportReceipt) {
			receipt.RepositoryOutcomes[0].Code = importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_UNKNOWN
			receipt.RepositoryOutcomes[0].RepositoryId = nil
		},
		"extra-assets": func(receipt *importv1.ImportReceipt) {
			receipt.CreatedAssets = []*configurationv1.Asset{{Id: "other", AgentId: "gemini", Kind: 1, Name: "Gemini profile", SchemaVersion: 1, Revision: 1}}
		},
	}
	for name, mutate := range cases {
		test.Run(name, func(test *testing.T) {
			client, fixture, path := selectionFixture(test)
			preview, err := client.PreviewFile(test.Context(), 2, path, "", nil)
			if err != nil {
				test.Fatal(err)
			}
			defer preview.Close()
			operation, err := client.BeginSelection(preview, importclient.SelectionChoice{Repositories: []int{1}, AcknowledgedNotices: []string{strings.Repeat("f", 64)}})
			if err != nil {
				test.Fatal(err)
			}
			defer operation.Close()
			fixture.mutate = mutate
			if _, err := operation.CommitFile(test.Context(), path); !errors.Is(err, importclient.ErrSelectionUnknown) {
				test.Fatal("unbound receipt accepted")
			}
			if fixture.commits.Load() != 1 {
				test.Fatal("unexpected commit count")
			}
			fixture.mutate = nil
			if result, err := client.Status(test.Context(), operation.ID()); err != nil || !result.SelectionVerified {
				test.Fatal("status did not recover bound receipt")
			}
			fixture.mutate = func(receipt *importv1.ImportReceipt) { receipt.Revision = 1 }
			if _, err := client.Status(test.Context(), operation.ID()); err == nil {
				test.Fatal("receipt revision regressed")
			}
			fixture.mutate = func(receipt *importv1.ImportReceipt) {
				receipt.Revision = 3
				receipt.Phase = importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COLLECTING
			}
			if _, err := client.Status(test.Context(), operation.ID()); err == nil {
				test.Fatal("terminal receipt changed back to collecting")
			}
		})
	}
}

func TestSelectionPrivateFormattingAndOptions(test *testing.T) {
	client, fixture, path := selectionFixture(test)
	preview, err := client.PreviewFile(test.Context(), 2, path, "", nil)
	if err != nil {
		test.Fatal(err)
	}
	defer preview.Close()
	operation, err := client.BeginSelection(preview, importclient.SelectionChoice{Repositories: []int{1}, AcknowledgedNotices: []string{strings.Repeat("f", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	for _, value := range []any{client, preview, operation, []any{client, preview, operation}} {
		encoded, err := json.Marshal(value)
		if err != nil {
			test.Fatal(err)
		}
		for _, formatted := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value), string(encoded)} {
			if strings.Contains(formatted, session) || strings.Contains(formatted, "Gemini profile") || strings.Contains(formatted, path) {
				test.Fatal("private state formatting leaked")
			}
		}
	}
	for _, invalid := range []string{`{"options":[],"secret":"value"}`, `{"sourceData":"c2VjcmV0"}`, `{"options":[{"candidateId":"bad","apiKey":"secret"}]}`, `{"options":[],"options":[]}`} {
		optionsPath := exportFile(test, []byte(invalid))
		if _, err := importclient.ReadSelectionOptions(test.Context(), optionsPath); err == nil {
			test.Fatal("unknown or duplicate options accepted")
		}
	}
	if fixture.commits.Load() != 0 {
		test.Fatal("formatting or option parse wrote")
	}
}

func TestSelectionPrivateValueContainers(test *testing.T) {
	client, fixture, path := selectionFixture(test)
	checkSelectionPrivateContainers(test, reflect.ValueOf(client).Elem().Interface(), []string{session})
	fixture.preview.Ticket = bytes.Repeat([]byte("sensitive!"), 29)
	preview, err := client.PreviewFile(test.Context(), 2, path, "", nil)
	if err != nil {
		test.Fatal(err)
	}
	defer preview.Close()
	operation, err := client.BeginSelection(preview, importclient.SelectionChoice{Repositories: []int{1}, AcknowledgedNotices: []string{strings.Repeat("f", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	materials := []string{session, "Gemini profile", path, string(fixture.preview.Ticket), fmt.Sprintf("%v", fixture.preview.Ticket), fmt.Sprintf("%#v", fixture.preview.Ticket)}
	checkSelectionPrivateContainers(test, reflect.ValueOf(preview).Elem().Interface(), materials)
	checkSelectionPrivateContainers(test, reflect.ValueOf(operation).Elem().Interface(), materials)
	client.Close()
	checkSelectionPrivateContainers(test, reflect.ValueOf(client).Elem().Interface(), materials)
	if fixture.commits.Load() != 0 {
		test.Fatal("private formatting or close dispatched an import")
	}
}

func TestSelectionZeroOpaqueHandles(test *testing.T) {
	var client importclient.SelectionClient
	var preview importclient.SelectionPreview
	var operation importclient.SelectionOperation
	if _, err := client.PreviewFile(test.Context(), 2, "missing", "", nil); err == nil {
		test.Fatal("zero client accepted a preview")
	}
	if _, err := client.Status(test.Context(), strings.Repeat("a", 32)); err == nil {
		test.Fatal("zero client accepted status")
	}
	if _, err := client.BeginSelection(&preview, importclient.SelectionChoice{}); err == nil {
		test.Fatal("zero client accepted selection")
	}
	if preview.View() != nil || preview.SHA256() != "" || operation.ID() != "" {
		test.Fatal("zero handle exposed state")
	}
	if _, err := operation.CommitFile(test.Context(), "missing"); err == nil {
		test.Fatal("zero operation accepted commit")
	}
	client.Close()
	preview.Close()
	operation.Close()
}

func checkSelectionPrivateContainers(test *testing.T, value any, materials []string) {
	test.Helper()
	for _, container := range []any{value, struct{ hidden any }{value}, struct{ hidden []any }{[]any{value}}, struct{ hidden map[string]any }{map[string]any{"opaque": value}}} {
		var outputs []string
		for _, format := range []string{"%v", "%+v", "%#v", "%q", "%s"} {
			outputs = append(outputs, fmt.Sprintf(format, container))
		}
		for _, jsonOutput := range []bool{false, true} {
			var output bytes.Buffer
			var handler slog.Handler = slog.NewTextHandler(&output, nil)
			if jsonOutput {
				handler = slog.NewJSONHandler(&output, nil)
			}
			slog.New(handler).Info("private selection", "state", container)
			outputs = append(outputs, output.String())
		}
		for _, output := range outputs {
			for _, material := range materials {
				if strings.Contains(output, material) {
					test.Fatal("private selection container disclosed owned material")
				}
			}
		}
	}
}

func TestSelectionSuppressesCallerHTTPTrace(test *testing.T) {
	client, _, path := selectionFixture(test)
	var observed atomic.Bool
	ctx := httptrace.WithClientTrace(test.Context(), &httptrace.ClientTrace{WroteHeaderField: func(name string, _ []string) {
		if strings.EqualFold(name, "Authorization") {
			observed.Store(true)
		}
	}})
	preview, err := client.PreviewFile(ctx, 2, path, "", nil)
	if err != nil {
		test.Fatal(err)
	}
	defer preview.Close()
	if observed.Load() {
		test.Fatal("caller HTTP trace observed the private authorization header")
	}
}

func TestSelectionSkillWithoutSourceRepository(test *testing.T) {
	for _, status := range []importv1.ImportCandidateStatus{1, 3, 4} {
		test.Run(status.String(), func(test *testing.T) {
			client, fixture, path := selectionFixture(test)
			fixture.preview.Skills[0].RepositoryCandidateId = ""
			fixture.preview.Skills[0].Status = status
			if status != 1 {
				fixture.preview.Skills[0].Binding = nil
			}
			preview, err := client.PreviewFile(test.Context(), 2, path, "", nil)
			if err != nil {
				test.Fatal("independent skill source repository rejected")
			}
			preview.Close()
			fixture.preview.Skills[0].RepositoryCandidateId = strings.Repeat("9", 64)
			if _, err := client.PreviewFile(test.Context(), 2, path, "", nil); err == nil {
				test.Fatal("nonempty foreign repository candidate accepted")
			}
		})
	}
}

func TestSelectionStatusDuringCommit(test *testing.T) {
	client, fixture, path := selectionFixture(test)
	fixture.started = make(chan struct{})
	fixture.release = make(chan struct{})
	preview, err := client.PreviewFile(test.Context(), 2, path, "", nil)
	if err != nil {
		test.Fatal(err)
	}
	defer preview.Close()
	operation, err := client.BeginSelection(preview, importclient.SelectionChoice{Repositories: []int{1}, AcknowledgedNotices: []string{strings.Repeat("f", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	commitDone := make(chan error, 1)
	go func() { _, err := operation.CommitFile(test.Context(), path); commitDone <- err }()
	<-fixture.started
	statusDone := make(chan error, 1)
	go func() {
		result, err := client.Status(test.Context(), operation.ID())
		if err == nil && !result.SelectionVerified {
			err = errors.New("status lost selection binding")
		}
		statusDone <- err
	}()
	var statusErr error
	waited := false
	select {
	case statusErr = <-statusDone:
	case <-time.After(250 * time.Millisecond):
		waited = true
	}
	close(fixture.release)
	commitErr := <-commitDone
	if waited {
		statusErr = <-statusDone
	}
	if waited || statusErr != nil || commitErr != nil || fixture.commits.Load() != 1 || fixture.statuses.Load() != 1 {
		test.Fatalf("observer waited for held Commit or failed: blocked=%t status=%v commit=%v", waited, statusErr, commitErr)
	}
}

func TestSelectionProcedureBudgets(test *testing.T) {
	client, fixture, path := selectionFixture(test)
	for _, entry := range []struct {
		source importv1.ImportSelectionSource
		budget int64
	}{{2, 5000}, {1, 15000}} {
		preview, err := client.PreviewFile(test.Context(), entry.source, path, "", nil)
		if err != nil {
			test.Fatal(err)
		}
		if actual := fixture.previewBudget.Load(); actual > entry.budget || actual < entry.budget-3000 {
			test.Fatalf("wrong preview budget: %d", actual)
		}
		preview.Close()
	}
	preview, err := client.PreviewFile(test.Context(), 2, path, "", nil)
	if err != nil {
		test.Fatal(err)
	}
	defer preview.Close()
	operation, err := client.BeginSelection(preview, importclient.SelectionChoice{Repositories: []int{1}, AcknowledgedNotices: []string{strings.Repeat("f", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	if _, err := operation.CommitFile(test.Context(), path); err != nil {
		test.Fatal(err)
	}
	if actual := fixture.commitBudget.Load(); actual > 120000 || actual < 117000 {
		test.Fatalf("wrong commit budget: %d", actual)
	}
	if _, err := client.Status(test.Context(), operation.ID()); err != nil {
		test.Fatal(err)
	}
	if actual := fixture.statusBudget.Load(); actual > 5000 || actual < 2000 {
		test.Fatalf("wrong status budget: %d", actual)
	}
}
