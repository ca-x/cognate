//go:build linux

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	libraryv1 "github.com/ca-x/cognate/gen/skillhub/library/v1"
	"github.com/ca-x/cognate/gen/skillhub/library/v1/libraryv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var selectionCommandSession = strings.Repeat("S", 43) + "." + strings.Repeat("T", 43)

type selectionCommandRPC struct {
	importv1connect.UnimplementedImportServiceHandler
	libraryv1connect.UnimplementedLibraryServiceHandler
	preview           *importv1.PreviewImportSelectionResponse
	previews          atomic.Int32
	commits           atomic.Int32
	statuses          atomic.Int32
	legacy            atomic.Int32
	mutex             sync.Mutex
	lastPreview       *importv1.PreviewImportSelectionRequest
	lastCommit        *importv1.CommitImportSelectionRequest
	lost              bool
	previewError      bool
	hangPreview       bool
	statusObservation importv1.ImportCommitObservation
	entered           chan struct{}
	drained           chan struct{}
	server            *httptest.Server
	path              string
	hash              string
	home              string
}

func newSelectionCommandRPC(test *testing.T) *selectionCommandRPC {
	test.Helper()
	fixture := &selectionCommandRPC{entered: make(chan struct{}, 1), drained: make(chan struct{}, 1), home: test.TempDir()}
	for name, value := range map[string]string{"HOME": fixture.home, "XDG_CONFIG_HOME": filepath.Join(fixture.home, "config"), "XDG_DATA_HOME": filepath.Join(fixture.home, "data"), "XDG_CACHE_HOME": filepath.Join(fixture.home, "cache")} {
		test.Setenv(name, value)
	}
	fixture.preview = &importv1.PreviewImportSelectionResponse{
		Ticket: bytes.Repeat([]byte{0x73}, 290), ExpiresMs: time.Now().Add(4 * time.Minute).UnixMilli(),
		Repositories: []*importv1.ImportRepositoryCandidate{{Id: strings.Repeat("a", 64), Status: importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY, Hint: &importv1.RepositoryCandidate{Repository: "example/toolkit", Branch: "main"}}},
		Skills: []*importv1.ImportSkillCandidate{{Id: strings.Repeat("b", 64), SafeName: "selected-guide", RepositoryCandidateId: strings.Repeat("a", 64), Status: importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY,
			Binding: &importv1.SkillBinding{RepositoryId: "repository-pin", ArchiveHash: strings.Repeat("d", 64), SkillId: "skill-pin", LogicalPath: "guides/exact/SKILL.md", ContentHash: strings.Repeat("e", 64), Repository: "example/toolkit", Branch: "main"}}},
		Configurations: []*importv1.ImportConfigurationCandidate{{Id: strings.Repeat("c", 64), KnownAgentId: "gemini", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, Status: importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY, SafeName: "Gemini selected",
			Draft:           &configurationv1.Draft{Name: "Gemini selected", Payload: &configurationv1.Draft_GeminiProvider{GeminiProvider: &configurationv1.GeminiProvider{AuthMode: configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_NATIVE_LOGIN}}},
			CredentialSlots: []*importv1.ImportCredentialSlot{{SlotId: "gemini.api-key", Present: true}, {SlotId: "gemini.legacy-api-key", Present: false}}}},
		Notices: []*importv1.ImportNotice{{Id: strings.Repeat("f", 64), Code: importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_CREDENTIALS_OMITTED, Count: 1}, {Id: strings.Repeat("9", 64), Code: importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_SOURCE_CONTENT_NOT_COMPARED, Count: 1}},
	}
	data := []byte(`{"skills":[],"private":"SYNTHETIC_SOURCE_CREDENTIAL"}`)
	fixture.path = filepath.Join(test.TempDir(), "private-authorized-export.json")
	if err := os.WriteFile(fixture.path, data, 0o600); err != nil {
		test.Fatal("cannot create synthetic export")
	}
	fixture.hash = fmt.Sprintf("%x", sha256.Sum256(data))
	mux := http.NewServeMux()
	mux.Handle(importv1connect.NewImportServiceHandler(fixture))
	mux.Handle(libraryv1connect.NewLibraryServiceHandler(fixture))
	fixture.server = httptest.NewServer(mux)
	test.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *selectionCommandRPC) PreviewImportSelection(ctx context.Context, request *connect.Request[importv1.PreviewImportSelectionRequest]) (*connect.Response[importv1.PreviewImportSelectionResponse], error) {
	fixture.previews.Add(1)
	if request.Header().Get("Authorization") != "Bearer "+selectionCommandSession {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("SYNTHETIC_SERVER_CREDENTIAL"))
	}
	fixture.mutex.Lock()
	fixture.lastPreview = proto.Clone(request.Msg).(*importv1.PreviewImportSelectionRequest)
	fixture.mutex.Unlock()
	if fixture.hangPreview {
		fixture.entered <- struct{}{}
		<-ctx.Done()
		fixture.drained <- struct{}{}
		return nil, connect.NewError(connect.CodeCanceled, errors.New("SYNTHETIC_SERVER_CREDENTIAL"))
	}
	if fixture.previewError {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("SYNTHETIC_SERVER_CREDENTIAL"))
	}
	return connect.NewResponse(proto.Clone(fixture.preview).(*importv1.PreviewImportSelectionResponse)), nil
}

func (fixture *selectionCommandRPC) CommitImportSelection(_ context.Context, request *connect.Request[importv1.CommitImportSelectionRequest]) (*connect.Response[importv1.CommitImportSelectionResponse], error) {
	fixture.commits.Add(1)
	if request.Header().Get("Authorization") != "Bearer "+selectionCommandSession {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("SYNTHETIC_SERVER_CREDENTIAL"))
	}
	fixture.mutex.Lock()
	fixture.lastCommit = proto.Clone(request.Msg).(*importv1.CommitImportSelectionRequest)
	fixture.mutex.Unlock()
	if fixture.lost {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("SYNTHETIC_SERVER_CREDENTIAL"))
	}
	return connect.NewResponse(&importv1.CommitImportSelectionResponse{Receipt: fixture.receipt(request.Msg.OperationId)}), nil
}

func (fixture *selectionCommandRPC) receipt(operation []byte) *importv1.ImportReceipt {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	receipt := &importv1.ImportReceipt{OperationId: bytes.Clone(operation), Revision: 2, Phase: importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED, Notices: fixture.preview.Notices}
	if fixture.lastCommit == nil {
		return receipt
	}
	for _, candidate := range fixture.lastCommit.RepositoryIds {
		receipt.RepositoryOutcomes = append(receipt.RepositoryOutcomes, &importv1.ImportRepositoryOutcome{CandidateId: candidate, Code: importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_COLLECTED, RepositoryId: new("repository-pin")})
	}
	for range fixture.lastCommit.SkillIds {
		receipt.SelectedSkillBindings = append(receipt.SelectedSkillBindings, proto.Clone(fixture.preview.Skills[0].Binding).(*importv1.SkillBinding))
	}
	for range fixture.lastCommit.ConfigurationIds {
		receipt.CreatedAssets = append(receipt.CreatedAssets, &configurationv1.Asset{Id: "asset-pin", AgentId: "gemini", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, Name: "Gemini selected", SchemaVersion: 1, Revision: 1})
	}
	return receipt
}

func (fixture *selectionCommandRPC) GetImportCommitStatus(_ context.Context, request *connect.Request[importv1.GetImportCommitStatusRequest]) (*connect.Response[importv1.GetImportCommitStatusResponse], error) {
	fixture.statuses.Add(1)
	if request.Header().Get("Authorization") != "Bearer "+selectionCommandSession {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("SYNTHETIC_SERVER_CREDENTIAL"))
	}
	if fixture.statusObservation == importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_NOT_OBSERVED {
		return connect.NewResponse(&importv1.GetImportCommitStatusResponse{Observation: fixture.statusObservation}), nil
	}
	return connect.NewResponse(&importv1.GetImportCommitStatusResponse{Observation: importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_TERMINAL, Receipt: fixture.receipt(request.Msg.OperationId)}), nil
}

func (fixture *selectionCommandRPC) CollectRepository(context.Context, *connect.Request[libraryv1.CollectRepositoryRequest]) (*connect.Response[libraryv1.CollectRepositoryResponse], error) {
	fixture.legacy.Add(1)
	return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("legacy collect must not run"))
}

func (fixture *selectionCommandRPC) arguments(extra ...string) []string {
	return append([]string{"import", "select", "--server", fixture.server.URL, "--file", fixture.path, "--source", "magpie", "--token-stdin"}, extra...)
}

func (fixture *selectionCommandRPC) applyArguments() []string {
	return fixture.arguments("--apply", "--expected-sha256", fixture.hash, "--repositories", "1", "--skills", "1", "--configurations", "1", "--ack-notice", strings.Repeat("f", 64), "--ack-notice", strings.Repeat("9", 64))
}

func selectionCommandRun(ctx context.Context, arguments []string, input io.Reader, output io.Writer) error {
	command := New()
	command.SetArgs(arguments)
	command.SetIn(input)
	command.SetOut(output)
	command.SetErr(output)
	return command.ExecuteContext(ctx)
}

func selectionCommandPrivate(test *testing.T, fixture *selectionCommandRPC, output string) {
	test.Helper()
	for _, secret := range []string{selectionCommandSession, fixture.path, fixture.home, "SYNTHETIC_SOURCE_CREDENTIAL", "SYNTHETIC_SERVER_CREDENTIAL", base64.StdEncoding.EncodeToString(fixture.preview.Ticket)} {
		if strings.Contains(output, secret) {
			test.Error("command output exposed a private value")
		}
	}
}

func TestImportSelectionDefaultPreview(test *testing.T) {
	fixture := newSelectionCommandRPC(test)
	var output bytes.Buffer
	if err := selectionCommandRun(test.Context(), fixture.arguments(), strings.NewReader(selectionCommandSession), &output); err != nil {
		test.Fatal("default selection preview failed")
	}
	if fixture.previews.Load() != 1 || fixture.commits.Load() != 0 || fixture.statuses.Load() != 0 || fixture.legacy.Load() != 0 {
		test.Fatal("default preview dispatched a write or wrong RPC")
	}
	for _, text := range []string{fixture.hash, "REPOSITORY 1", "SKILL 1", "CONFIGURATION 1", "gemini.api-key present=true omitted=true", "gemini.legacy-api-key present=false omitted=true", "SOURCE_CONTENT_NOT_COMPARED"} {
		if !strings.Contains(output.String(), text) {
			test.Error("preview omitted required safe category or credential presence")
		}
	}
	selectionCommandPrivate(test, fixture, output.String())
	fixture.mutex.Lock()
	request := fixture.lastPreview
	fixture.mutex.Unlock()
	if request == nil || request.Source != importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON || fmt.Sprintf("%x", sha256.Sum256(request.SourceData)) != fixture.hash || len(request.Options) != 0 {
		test.Fatal("preview changed the source snapshot or selected default options")
	}
	entries, err := os.ReadDir(fixture.home)
	if err != nil || len(entries) != 0 {
		test.Fatal("preview wrote client configuration or browser-like state")
	}
}

func TestImportSelectionShowsExactSkillBinding(test *testing.T) {
	fixture := newSelectionCommandRPC(test)
	var output bytes.Buffer
	if err := selectionCommandRun(test.Context(), fixture.arguments(), strings.NewReader(selectionCommandSession), &output); err != nil {
		test.Fatal("preview failed before binding review")
	}
	binding := fixture.preview.Skills[0].Binding
	for _, field := range []struct{ name, value string }{{"repository_id", binding.RepositoryId}, {"archive_hash", binding.ArchiveHash}, {"skill_id", binding.SkillId}, {"logical_path", binding.LogicalPath}, {"content_hash", binding.ContentHash}, {"repository", binding.Repository}, {"branch", binding.Branch}} {
		if !strings.Contains(output.String(), field.value) {
			test.Errorf("displayed selection omits exact skill binding field %s", field.name)
		}
	}
	if fixture.commits.Load() != 0 {
		test.Fatal("review-only binding probe wrote")
	}
}

func TestImportSelectionShowsCandidateIDsForOptions(test *testing.T) {
	fixture := newSelectionCommandRPC(test)
	var output bytes.Buffer
	if err := selectionCommandRun(test.Context(), fixture.arguments(), strings.NewReader(selectionCommandSession), &output); err != nil {
		test.Fatal("preview failed before option candidate identification")
	}
	for _, candidate := range []struct{ kind, id string }{{"repository", fixture.preview.Repositories[0].Id}, {"skill", fixture.preview.Skills[0].Id}, {"configuration", fixture.preview.Configurations[0].Id}} {
		if !strings.Contains(output.String(), candidate.id) {
			test.Errorf("preview omits %s candidate ID needed for an options file", candidate.kind)
		}
	}
}

func TestImportSelectionFullOptionsFile(test *testing.T) {
	fixture := newSelectionCommandRPC(test)
	options := []*importv1.ImportCandidateOptions{{CandidateId: strings.Repeat("b", 64), SkillBinding: proto.Clone(fixture.preview.Skills[0].Binding).(*importv1.SkillBinding)}, {CandidateId: strings.Repeat("c", 64), Name: new("Gemini selected"), Auth: &importv1.ImportCandidateOptions_GeminiAuthMode{GeminiAuthMode: configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_NATIVE_LOGIN}, GeminiKeyTransport: new(configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_NATIVE_DEFAULT)}}
	data, err := protojson.Marshal(&importv1.PreviewImportSelectionRequest{Options: options})
	if err != nil {
		test.Fatal("cannot encode complete options")
	}
	path := filepath.Join(test.TempDir(), "private-options.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		test.Fatal("cannot write synthetic options")
	}
	var output bytes.Buffer
	if err := selectionCommandRun(test.Context(), fixture.arguments("--options-file", path), strings.NewReader(selectionCommandSession), &output); err != nil {
		test.Fatal("complete ProtoJSON options rejected")
	}
	fixture.mutex.Lock()
	request := fixture.lastPreview
	fixture.mutex.Unlock()
	if request == nil || !proto.Equal(&importv1.PreviewImportSelectionRequest{Options: request.Options}, &importv1.PreviewImportSelectionRequest{Options: options}) {
		test.Fatal("command dropped binding, optional name, explicit auth or transport")
	}
	if strings.Contains(output.String(), path) || fixture.commits.Load() != 0 {
		test.Fatal("options exposed a private path or wrote by default")
	}
	master, slave := nativeTestPTY(test)
	command, capture, cancel := selectionCommandChild(test, fixture, append(fixture.applyArguments(), "--options-file", path), slave, false, false)
	defer cancel()
	var operation string
	select {
	case operation = <-capture.operation:
	case <-time.After(3 * time.Second):
		cancel()
		_ = command.Wait()
		test.Fatal("options apply did not publish operation")
	}
	selectionCommandPrompt(test, master, operation)
	_, _ = master.Write([]byte("import " + operation + "\n"))
	if err := command.Wait(); err != nil || fixture.commits.Load() != 1 {
		test.Fatal("complete options did not survive explicit TTY commit")
	}
	fixture.mutex.Lock()
	committed := fixture.lastCommit
	fixture.mutex.Unlock()
	if committed == nil || !proto.Equal(&importv1.PreviewImportSelectionRequest{Options: committed.Options}, &importv1.PreviewImportSelectionRequest{Options: options}) {
		test.Fatal("commit discarded complete options from its exact preview")
	}
}

func TestImportSelectionInvalidFlagsAndErrorsArePrivate(test *testing.T) {
	for _, extra := range [][]string{{"--apply"}, {"--skills", "1"}, {"--expected-sha256", strings.Repeat("0", 64)}, {"--ack-notice", strings.Repeat("f", 64)}, {"--source", "private-invalid-source"}, {"--unexpected-private-flag"}, {"--apply", "--expected-sha256", strings.Repeat("0", 64), "--skills", "1"}} {
		fixture := newSelectionCommandRPC(test)
		var output bytes.Buffer
		err := selectionCommandRun(test.Context(), fixture.arguments(extra...), strings.NewReader(selectionCommandSession), &output)
		if err == nil || fixture.commits.Load() != 0 || fixture.legacy.Load() != 0 {
			test.Error("invalid flags authorized a write")
		}
		if err != nil {
			selectionCommandPrivate(test, fixture, err.Error()+output.String())
		}
	}
	fixture := newSelectionCommandRPC(test)
	fixture.previewError = true
	var output bytes.Buffer
	err := selectionCommandRun(test.Context(), fixture.arguments(), strings.NewReader(selectionCommandSession), &output)
	if err == nil || fixture.previews.Load() != 1 || fixture.commits.Load() != 0 {
		test.Fatal("upstream rejection not preserved")
	}
	selectionCommandPrivate(test, fixture, err.Error()+output.String())
	for _, flag := range []string{"--file", "--options-file"} {
		fixture := newSelectionCommandRPC(test)
		var output bytes.Buffer
		err := selectionCommandRun(test.Context(), fixture.arguments(flag, fixture.path+".missing"), strings.NewReader(selectionCommandSession), &output)
		if err == nil || fixture.commits.Load() != 0 {
			test.Error("missing private input was accepted")
		}
		if err != nil {
			selectionCommandPrivate(test, fixture, output.String()+err.Error())
		}
	}
}

func TestImportSelectionStatusOnlyNotObservedNeverUploads(test *testing.T) {
	fixture := newSelectionCommandRPC(test)
	fixture.statusObservation = importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_NOT_OBSERVED
	for _, operation := range []string{strings.Repeat("1", 32), strings.Repeat("0", 32), "not-an-operation", strings.Repeat("A", 32)} {
		var output bytes.Buffer
		err := selectionCommandRun(test.Context(), []string{"import", "status", "--server", fixture.server.URL, "--operation-id", operation, "--token-stdin"}, strings.NewReader(selectionCommandSession), &output)
		if operation == strings.Repeat("1", 32) && (err == nil || !strings.Contains(output.String(), "NOT_OBSERVED")) {
			test.Error("NotObserved was hidden or treated as completion")
		}
		if operation != strings.Repeat("1", 32) && err == nil {
			test.Error("invalid status identity accepted")
		}
		if err != nil {
			selectionCommandPrivate(test, fixture, err.Error()+output.String())
		}
	}
	if fixture.statuses.Load() != 1 || fixture.previews.Load() != 0 || fixture.commits.Load() != 0 || fixture.legacy.Load() != 0 {
		test.Fatal("status uploaded a source, retried, or accepted invalid identities")
	}
}

func TestImportSelectionOutputFailureAndCancellationNeverWrite(test *testing.T) {
	for _, mode := range []string{"unsupported-writer", "closed-output", "full-output", "input-timeout", "request-cancel"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newSelectionCommandRPC(test)
			limit := 250 * time.Millisecond
			if mode == "request-cancel" {
				limit = 3 * time.Second
			}
			ctx, cancel := context.WithTimeout(test.Context(), limit)
			defer cancel()
			var output io.Writer = &bytes.Buffer{}
			var input io.Reader = strings.NewReader(selectionCommandSession)
			if mode == "unsupported-writer" {
				output = io.Discard
			}
			if mode == "closed-output" {
				reader, writer, err := os.Pipe()
				if err != nil {
					test.Fatal("pipe unavailable")
				}
				_ = reader.Close()
				defer writer.Close()
				output = writer
			}
			if mode == "full-output" {
				_, writer, _ := fullWatchPipe(test)
				output = writer
			}
			if mode == "input-timeout" {
				reader, writer, err := os.Pipe()
				if err != nil {
					test.Fatal("pipe unavailable")
				}
				defer reader.Close()
				defer writer.Close()
				input = reader
			}
			if mode == "request-cancel" {
				fixture.hangPreview = true
			}
			var cancellation sync.WaitGroup
			triggered := make(chan bool, 1)
			if mode == "request-cancel" {
				cancellation.Go(func() {
					select {
					case <-fixture.entered:
						triggered <- true
						cancel()
					case <-ctx.Done():
						triggered <- false
					}
				})
			}
			operationErr := selectionCommandRun(ctx, fixture.applyArguments(), input, output)
			cancel()
			cancellation.Wait()
			if operationErr == nil {
				test.Error("I/O failure or canceled request succeeded")
			}
			if fixture.commits.Load() != 0 || fixture.legacy.Load() != 0 {
				test.Fatal("output failure or cancellation wrote")
			}
			if mode == "request-cancel" {
				if !<-triggered {
					test.Fatal("cancellation did not reach an actual loopback handler")
				}
				select {
				case <-fixture.drained:
				case <-time.After(2 * time.Second):
					test.Fatal("canceled HTTP handler did not drain")
				}
			}
		})
	}
}

func TestImportSelectionProcess(test *testing.T) {
	if os.Getenv("COGNATE_SELECTION_TEST_PROCESS") != "1" {
		return
	}
	var arguments []string
	if err := json.Unmarshal([]byte(os.Getenv("COGNATE_SELECTION_TEST_ARGS")), &arguments); err != nil {
		os.Exit(25)
	}
	duration := 4 * time.Second
	if os.Getenv("COGNATE_SELECTION_TEST_TIMEOUT") == "short" {
		duration = 350 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(test.Context(), duration)
	defer cancel()
	if err := selectionCommandRun(ctx, arguments, os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "SELECTION_COMMAND_FAILED")
		os.Exit(23)
	}
	os.Exit(0)
}

type selectionCommandCapture struct {
	mutex     sync.Mutex
	data      bytes.Buffer
	operation chan string
	once      sync.Once
}

func (capture *selectionCommandCapture) Write(data []byte) (int, error) {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	if capture.data.Len()+len(data) > (4<<20)+4096 {
		return 0, io.ErrShortBuffer
	}
	count, err := capture.data.Write(data)
	if match := regexp.MustCompile(`(?m)^OPERATION ([0-9a-f]{32})\r?$`).FindStringSubmatch(capture.data.String()); len(match) == 2 {
		capture.once.Do(func() { capture.operation <- match[1] })
	}
	return count, err
}

func (capture *selectionCommandCapture) text() string {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	return capture.data.String()
}

func selectionCommandChild(test *testing.T, fixture *selectionCommandRPC, arguments []string, terminal *os.File, short, stdinYes bool) (*exec.Cmd, *selectionCommandCapture, context.CancelFunc) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 6*time.Second)
	data, err := json.Marshal(arguments)
	if err != nil {
		test.Fatal("cannot encode child arguments")
	}
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestImportSelectionProcess$", "-test.count=1")
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + fixture.home, "XDG_CONFIG_HOME=" + filepath.Join(fixture.home, "config"), "XDG_DATA_HOME=" + filepath.Join(fixture.home, "data"), "XDG_CACHE_HOME=" + filepath.Join(fixture.home, "cache"), "TMPDIR=" + test.TempDir(), "COGNATE_SELECTION_TEST_PROCESS=1", "COGNATE_SELECTION_TEST_ARGS=" + string(data)}
	if short {
		command.Env = append(command.Env, "COGNATE_SELECTION_TEST_TIMEOUT=short")
	}
	command.Stdin = strings.NewReader(selectionCommandSession)
	if stdinYes {
		command.Stdin = strings.NewReader(selectionCommandSession + "\nyes\n")
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if terminal != nil {
		command.ExtraFiles = []*os.File{terminal}
		command.SysProcAttr.Setctty = true
		command.SysProcAttr.Ctty = 3
	}
	command.WaitDelay = time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		return command.Process.Kill()
	}
	capture := &selectionCommandCapture{operation: make(chan string, 1)}
	command.Stdout, command.Stderr = capture, capture
	if err := command.Start(); err != nil {
		cancel()
		test.Fatal("cannot start bounded selection child")
	}
	test.Cleanup(func() {
		cancel()
		if command.ProcessState == nil {
			_ = command.Wait()
		}
	})
	return command, capture, cancel
}

func selectionCommandPrompt(test *testing.T, master *os.File, operation string) string {
	test.Helper()
	if err := master.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		test.Fatal("PTY deadline unavailable")
	}
	var text strings.Builder
	for text.Len() < 4096 {
		buffer := make([]byte, 512)
		count, err := master.Read(buffer)
		if count > 0 {
			text.Write(buffer[:count])
		}
		if strings.Contains(text.String(), "确认请输入 import "+operation) {
			return text.String()
		}
		if err != nil {
			test.Fatal("exact controlling-TTY prompt missing")
		}
	}
	test.Fatal("unbounded confirmation prompt")
	return ""
}

func TestImportSelectionTTYReviewHasBindingAndChoices(test *testing.T) {
	fixture := newSelectionCommandRPC(test)
	master, slave := nativeTestPTY(test)
	command, capture, cancel := selectionCommandChild(test, fixture, fixture.applyArguments(), slave, false, false)
	defer cancel()
	var operation string
	select {
	case operation = <-capture.operation:
	case <-time.After(3 * time.Second):
		cancel()
		_ = command.Wait()
		test.Fatal("operation missing before TTY review")
	}
	text := selectionCommandPrompt(test, master, operation)
	binding := fixture.preview.Skills[0].Binding
	for _, field := range []struct{ name, value string }{{"repository_id", binding.RepositoryId}, {"archive_hash", binding.ArchiveHash}, {"skill_id", binding.SkillId}, {"logical_path", binding.LogicalPath}, {"content_hash", binding.ContentHash}, {"repository", binding.Repository}, {"branch", binding.Branch}, {"repository selection", "repositories=[1]"}, {"skill selection", "skills=[1]"}, {"configuration selection", "configurations=[1]"}} {
		if !strings.Contains(text, field.value) {
			test.Errorf("controlling-TTY approval lacks reviewed %s while stdout is redirected", field.name)
		}
	}
	_, _ = master.Write([]byte("no\n"))
	if err := command.Wait(); err == nil || fixture.commits.Load() != 0 {
		test.Fatal("declined incomplete review committed")
	}
}

func TestImportSelectionTTYConsentUnknownAndReadOnlyStatus(test *testing.T) {
	for _, mode := range []string{"confirm", "yes", "wrong-operation", "no-tty", "stdin-yes", "unknown", "timeout", "kill"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newSelectionCommandRPC(test)
			fixture.lost = mode == "unknown"
			var master, slave *os.File
			if mode != "no-tty" && mode != "stdin-yes" {
				master, slave = nativeTestPTY(test)
			}
			command, capture, cancel := selectionCommandChild(test, fixture, fixture.applyArguments(), slave, mode == "timeout", mode == "stdin-yes")
			defer cancel()
			var operation string
			if master != nil {
				select {
				case operation = <-capture.operation:
				case <-time.After(3 * time.Second):
					cancel()
					_ = command.Wait()
					test.Fatal("operation was not published before consent")
				}
				selectionCommandPrompt(test, master, operation)
				if fixture.commits.Load() != 0 {
					cancel()
					_ = command.Wait()
					test.Fatal("commit preceded exact TTY confirmation")
				}
				switch mode {
				case "confirm", "unknown":
					_, _ = master.Write([]byte("import " + operation + "\n"))
				case "yes":
					_, _ = master.Write([]byte("yes\n"))
				case "wrong-operation":
					_, _ = master.Write([]byte("import " + strings.Repeat("0", 32) + "\n"))
				case "kill":
					cancel()
				}
			}
			err := command.Wait()
			committed := mode == "confirm" || mode == "unknown"
			if (fixture.commits.Load() == 1) != committed || fixture.commits.Load() > 1 || fixture.legacy.Load() != 0 {
				test.Fatal("TTY approval boundary or one-shot commit violated")
			}
			if (err == nil) != (mode == "confirm") {
				test.Error("unexpected child terminal result")
			}
			if command.ProcessState == nil || !command.ProcessState.Exited() && mode != "kill" {
				test.Error("child was not joined")
			}
			selectionCommandPrivate(test, fixture, capture.text())
			if committed {
				fixture.mutex.Lock()
				request := fixture.lastCommit
				fixture.mutex.Unlock()
				decoded, decodeErr := hex.DecodeString(operation)
				if decodeErr != nil || !bytes.Equal(decoded, request.OperationId) || !slices.Equal(request.RepositoryIds, []string{strings.Repeat("a", 64)}) || !slices.Equal(request.SkillIds, []string{strings.Repeat("b", 64)}) || !slices.Equal(request.ConfigurationIds, []string{strings.Repeat("c", 64)}) {
					test.Fatal("displayed operation or explicit selection changed on commit")
				}
			}
			if mode == "unknown" {
				before := fixture.previews.Load()
				var output bytes.Buffer
				arguments := []string{"import", "status", "--server", fixture.server.URL, "--operation-id", operation, "--token-stdin"}
				if err := selectionCommandRun(test.Context(), arguments, strings.NewReader(selectionCommandSession), &output); err != nil {
					test.Fatal("status did not recover independently")
				}
				if fixture.commits.Load() != 1 || fixture.previews.Load() != before || fixture.statuses.Load() != 1 {
					test.Fatal("status replayed source or commit")
				}
				if !strings.Contains(output.String(), "SELECTION_VERIFIED false") || !strings.Contains(output.String(), "不表示本机安装或应用") {
					test.Error("status overclaimed replay verification or installation")
				}
				selectionCommandPrivate(test, fixture, output.String())
			}
		})
	}
}
