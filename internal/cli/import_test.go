package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/skillhub/gen/skillhub/import/v1"
	"github.com/ca-x/skillhub/gen/skillhub/import/v1/importv1connect"
	libraryv1 "github.com/ca-x/skillhub/gen/skillhub/library/v1"
	"github.com/ca-x/skillhub/gen/skillhub/library/v1/libraryv1connect"
	"github.com/ca-x/skillhub/internal/cli"
)

var importSession = strings.Repeat("A", 43) + "." + strings.Repeat("B", 43)

type importRPC struct {
	importv1connect.UnimplementedImportServiceHandler
	libraryv1connect.UnimplementedLibraryServiceHandler
	mutex         sync.Mutex
	data          []byte
	previews      int
	branches      []string
	codes         []connect.Code
	beforeCollect func(context.Context)
}

func (fixture *importRPC) PreviewLibraryExport(_ context.Context, request *connect.Request[importv1.PreviewLibraryExportRequest]) (*connect.Response[importv1.PreviewLibraryExportResponse], error) {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	if request.Header().Get("Authorization") != "Bearer "+importSession {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("PRIVATE_SERVER_SECRET"))
	}
	if request.Msg.Format != importv1.ExportFormat_EXPORT_FORMAT_MAGPIE_LIBRARY_JSON || !bytes.Equal(request.Msg.ExportData, fixture.data) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("PRIVATE_SERVER_SECRET"))
	}
	fixture.previews++
	return connect.NewResponse(&importv1.PreviewLibraryExportResponse{ReferencedSkillCount: 7, Repositories: []*importv1.RepositoryCandidate{
		{Repository: "example/toolkit", ExistingRepositoryId: "PRIVATE_OWNER_ID"},
		{Repository: "example/toolkit", Branch: "release/v2"},
		{Repository: "example/toolkit", Branch: "head"},
	}}), nil
}

func (fixture *importRPC) CollectRepository(ctx context.Context, request *connect.Request[libraryv1.CollectRepositoryRequest]) (*connect.Response[libraryv1.CollectRepositoryResponse], error) {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	if request.Header().Get("Authorization") != "Bearer "+importSession || request.Msg.Repository != "example/toolkit" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("PRIVATE_SERVER_SECRET"))
	}
	fixture.branches = append(fixture.branches, request.Msg.Branch)
	if fixture.beforeCollect != nil {
		fixture.beforeCollect(ctx)
	}
	if len(fixture.codes) >= len(fixture.branches) {
		if code := fixture.codes[len(fixture.branches)-1]; code != 0 {
			return nil, connect.NewError(code, errors.New("PRIVATE_SERVER_SECRET"))
		}
	}
	return connect.NewResponse(&libraryv1.CollectRepositoryResponse{Repository: &libraryv1.Repository{
		Id: "PRIVATE_OWNER_ID", NameWithOwner: request.Msg.Repository, Branch: request.Msg.Branch,
		ArchiveHash: strings.Repeat("a", 64), SkillCount: 1, Manual: true,
	}}), nil
}

func newImportRPC(test *testing.T) (*importRPC, *httptest.Server, string, string) {
	test.Helper()
	data, err := os.ReadFile("../server/importer/testdata/magpie/library.json")
	if err != nil {
		test.Fatal(err)
	}
	fixture := &importRPC{data: data}
	mux := http.NewServeMux()
	mux.Handle(importv1connect.NewImportServiceHandler(fixture))
	mux.Handle(libraryv1connect.NewLibraryServiceHandler(fixture))
	server := httptest.NewServer(mux)
	test.Cleanup(server.Close)
	path := filepath.Join(test.TempDir(), "private-export.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		test.Fatal(err)
	}
	return fixture, server, path, fmt.Sprintf("%x", sha256.Sum256(data))
}

func runImport(ctx context.Context, server, path, token string, output io.Writer, extra ...string) error {
	command := cli.New()
	command.SetIn(strings.NewReader(token + "\n"))
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs(append([]string{"import", "magpie", "--server", server, "--file", path, "--token-stdin"}, extra...))
	return command.ExecuteContext(ctx)
}

func assertImportPrivate(test *testing.T, text, path string) {
	test.Helper()
	for _, secret := range []string{importSession, "MAGPIE_FIXTURE_SECRET", "MAGPIE_FIXTURE_PRIVATE", "PRIVATE_SERVER_SECRET", "PRIVATE_OWNER_ID", path, "review-guide", "do-not-execute"} {
		if strings.Contains(text, secret) {
			test.Fatalf("private value exposed: %q", secret)
		}
	}
}

func TestImportMagpiePreviewAndOrderedApply(test *testing.T) {
	fixture, server, path, hash := newImportRPC(test)
	home := test.TempDir()
	test.Setenv("HOME", home)
	var output bytes.Buffer
	if err := runImport(test.Context(), server.URL, path, importSession, &output); err != nil {
		test.Fatal(err)
	}
	if fixture.previews != 1 || len(fixture.branches) != 0 || !strings.Contains(output.String(), hash) || !strings.Contains(output.String(), "REFERENCES 7") {
		test.Fatalf("preview: %s", &output)
	}
	assertImportPrivate(test, output.String(), path)
	output.Reset()
	if err := runImport(test.Context(), server.URL, path, importSession, &output, "--apply", "--expected-sha256", hash, "--select", "3,1", "--select", "2"); err != nil {
		test.Fatal(err)
	}
	if !slices.Equal(fixture.branches, []string{"head", "", "release/v2"}) {
		test.Fatalf("order/existing validation: %v", fixture.branches)
	}
	if strings.Count(output.String(), "collected") != 3 {
		test.Fatalf("apply: %s", &output)
	}
	assertImportPrivate(test, output.String(), path)
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		test.Fatal("import created client state")
	}
}

type importMutatingWriter struct {
	bytes.Buffer
	mutate func()
}

func (writer *importMutatingWriter) Write(data []byte) (int, error) {
	if writer.mutate != nil {
		writer.mutate()
		writer.mutate = nil
	}
	return writer.Buffer.Write(data)
}

func TestImportMagpieHashAndSelectionHaveZeroWrites(test *testing.T) {
	for _, extra := range [][]string{
		{"--apply"}, {"--apply", "--select", "1"}, {"--select", "1"},
		{"--apply", "--expected-sha256", strings.Repeat("0", 64), "--select", "1"},
	} {
		test.Run(strings.Join(extra, " "), func(test *testing.T) {
			fixture, server, path, _ := newImportRPC(test)
			var output bytes.Buffer
			if err := runImport(test.Context(), server.URL, path, importSession, &output, extra...); err == nil {
				test.Fatal("unsafe apply accepted")
			}
			if len(fixture.branches) != 0 {
				test.Fatal("collected without pinned selection")
			}
		})
	}
	for _, selection := range []string{"", "0", "4", "1,1", "-1"} {
		fixture, server, path, hash := newImportRPC(test)
		if err := runImport(test.Context(), server.URL, path, importSession, io.Discard, "--apply", "--expected-sha256", hash, "--select", selection); err == nil {
			test.Fatalf("accepted selection %q", selection)
		}
		if len(fixture.branches) != 0 {
			test.Fatal("invalid selection collected")
		}
	}
	fixture, server, path, hash := newImportRPC(test)
	writer := &importMutatingWriter{mutate: func() {
		if err := os.WriteFile(path, []byte(`{"skills":[]}`), 0o600); err != nil {
			test.Fatal(err)
		}
	}}
	if err := runImport(test.Context(), server.URL, path, importSession, writer, "--apply", "--expected-sha256", hash, "--select", "1"); err == nil {
		test.Fatal("changed file accepted")
	}
	if fixture.previews != 1 || len(fixture.branches) != 0 {
		test.Fatal("changed file collected")
	}
}

func TestImportMagpieApplyHashMismatchDoesNotUpload(test *testing.T) {
	for _, kind := range []string{"stale-preview", "wrong-hash", "malformed-hash"} {
		test.Run(kind, func(test *testing.T) {
			fixture, server, path, expected := newImportRPC(test)
			switch kind {
			case "stale-preview":
				fixture.data = bytes.ReplaceAll(fixture.data, []byte("MAGPIE_FIXTURE_SECRET"), []byte("UNREVIEWED_EXPORT_SECRET"))
				if err := os.WriteFile(path, fixture.data, 0o600); err != nil {
					test.Fatal(err)
				}
			case "wrong-hash":
				expected = strings.Repeat("0", 64)
			case "malformed-hash":
				expected = "not-a-sha256"
			}
			var output bytes.Buffer
			err := runImport(test.Context(), server.URL, path, importSession, &output, "--apply", "--expected-sha256", expected, "--select", "1")
			if err == nil {
				test.Fatal("unconfirmed export accepted")
			}
			if fixture.previews != 0 || len(fixture.branches) != 0 {
				test.Fatalf("unconfirmed export reached server: previews=%d collects=%d", fixture.previews, len(fixture.branches))
			}
			assertImportPrivate(test, err.Error()+output.String(), path)
			if strings.Contains(err.Error()+output.String(), "UNREVIEWED_EXPORT_SECRET") {
				test.Fatal("unconfirmed export exposed")
			}
		})
	}
}

func TestImportMagpieFailureClassification(test *testing.T) {
	for _, code := range []connect.Code{connect.CodeInvalidArgument, connect.CodeNotFound, connect.CodeFailedPrecondition, connect.CodeDataLoss, connect.CodeResourceExhausted, connect.CodePermissionDenied, connect.CodeUnauthenticated, connect.CodeCanceled, connect.CodeDeadlineExceeded, connect.CodeUnavailable, connect.CodeInternal, connect.CodeUnknown} {
		test.Run(code.String(), func(test *testing.T) {
			fixture, server, path, hash := newImportRPC(test)
			fixture.codes = []connect.Code{0, code}
			var output bytes.Buffer
			err := runImport(test.Context(), server.URL, path, importSession, &output, "--apply", "--expected-sha256", hash, "--select", "1,2,3")
			if err == nil {
				test.Fatal("partial failure returned success")
			}
			want := 2
			if slices.Contains([]connect.Code{connect.CodeInvalidArgument, connect.CodeNotFound, connect.CodeFailedPrecondition, connect.CodeDataLoss}, code) {
				want = 3
			}
			if len(fixture.branches) != want {
				test.Fatalf("calls=%d want=%d", len(fixture.branches), want)
			}
			if want == 2 && (!strings.Contains(output.String(), "unknown") || !strings.Contains(output.String(), "unattempted")) {
				test.Fatalf("lost uncertain result: %s", &output)
			}
			if (code == connect.CodeCanceled || code == connect.CodeDeadlineExceeded) && !strings.Contains(output.String(), "unknown "+code.String()) {
				test.Fatalf("lost cancellation reason: %s", &output)
			}
			if !strings.Contains(output.String(), "collected") {
				test.Fatal("completed result lost")
			}
			assertImportPrivate(test, err.Error()+output.String(), path)
		})
	}
}

func TestImportMagpieCanceledInFlight(test *testing.T) {
	fixture, server, path, hash := newImportRPC(test)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	fixture.beforeCollect = func(requestContext context.Context) { cancel(); <-requestContext.Done() }
	var output bytes.Buffer
	err := runImport(ctx, server.URL, path, importSession, &output, "--apply", "--expected-sha256", hash, "--select", "1,2,3")
	if !errors.Is(err, context.Canceled) {
		test.Fatalf("cancellation lost: %v", err)
	}
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	if len(fixture.branches) != 1 || strings.Count(output.String(), "unattempted") != 2 || !strings.Contains(output.String(), "unknown") {
		test.Fatalf("cancellation results: %s", &output)
	}
}

func TestImportMagpieInputBoundaries(test *testing.T) {
	for _, kind := range []string{"empty", "oversize", "directory", "symlink", "parent-symlink", "missing", "bad-session", "long-session"} {
		test.Run(kind, func(test *testing.T) {
			fixture, server, path, _ := newImportRPC(test)
			token := importSession
			switch kind {
			case "empty":
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					test.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), (1<<20)+1), 0o600); err != nil {
					test.Fatal(err)
				}
			case "directory":
				path = filepath.Dir(path)
			case "missing":
				path += ".missing"
			case "bad-session":
				token = "shp1.not-a-session"
			case "long-session":
				token = strings.Repeat("x", 2049)
			case "symlink", "parent-symlink":
				link := filepath.Join(test.TempDir(), "link")
				target := path
				if kind == "parent-symlink" {
					target = filepath.Dir(path)
				}
				if err := os.Symlink(target, link); err != nil {
					test.Skip(err)
				}
				path = link
				if kind == "parent-symlink" {
					path = filepath.Join(link, "private-export.json")
				}
			}
			var output bytes.Buffer
			err := runImport(test.Context(), server.URL, path, token, &output)
			if err == nil || fixture.previews != 0 || len(fixture.branches) != 0 {
				test.Fatal("invalid input reached RPC")
			}
			assertImportPrivate(test, err.Error()+output.String(), path)
		})
	}
}

func TestImportMagpieExitCode(test *testing.T) {
	if os.Getenv("SKILLHUB_IMPORT_EXIT_HELPER") == "1" {
		os.Args = append([]string{os.Args[0]}, strings.Split(os.Getenv("SKILLHUB_IMPORT_ARGS"), "\n")...)
		cli.Execute()
		return
	}
	fixture, server, path, hash := newImportRPC(test)
	for _, failure := range []bool{false, true} {
		fixture.codes = nil
		fixture.branches = nil
		if failure {
			fixture.codes = []connect.Code{connect.CodeNotFound}
		}
		args := []string{"import", "magpie", "--server", server.URL, "--file", path, "--token-stdin", "--apply", "--expected-sha256", hash, "--select", "1"}
		process := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestImportMagpieExitCode$")
		process.Env = append(os.Environ(), "SKILLHUB_IMPORT_EXIT_HELPER=1", "SKILLHUB_IMPORT_ARGS="+strings.Join(args, "\n"))
		process.Stdin = strings.NewReader(importSession)
		output, err := process.CombinedOutput()
		if failure {
			if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
				test.Fatalf("exit=%v", err)
			}
		} else if err != nil {
			test.Fatal(err)
		}
		assertImportPrivate(test, string(output), path)
	}
}
