package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"github.com/labstack/echo/v4"
)

type selectionGuardBody struct {
	reads int
}

func (body *selectionGuardBody) Read(buffer []byte) (int, error) {
	body.reads++
	clear(buffer)
	return len(buffer), nil
}

func TestImportSelectionOuterGuardWireLimits(test *testing.T) {
	for _, example := range []struct {
		name      string
		procedure string
		limit     int64
	}{
		{"preview", importv1connect.ImportServicePreviewImportSelectionProcedure, 24 << 20},
		{"commit", importv1connect.ImportServiceCommitImportSelectionProcedure, 24 << 20},
		{"status", importv1connect.ImportServiceGetImportCommitStatusProcedure, 64 << 10},
		{"legacy-library", importv1connect.ImportServicePreviewLibraryExportProcedure, 2 << 20},
		{"legacy-snapshot", importv1connect.ImportServicePreviewCCSwitchSnapshotProcedure, 24 << 20},
		{"unknown", "/unknown", 4096},
		{"selection-suffix", importv1connect.ImportServicePreviewImportSelectionProcedure + "Extra", 4096},
		{"selection-child", importv1connect.ImportServiceCommitImportSelectionProcedure + "/child", 4096},
	} {
		test.Run(example.name, func(test *testing.T) {
			for _, extra := range []int64{0, 1} {
				body := &selectionGuardBody{}
				request := httptest.NewRequest(http.MethodPost, "http://localhost"+example.procedure, io.LimitReader(body, example.limit+extra))
				response := httptest.NewRecorder()
				server := echo.New()
				called := false
				guard := guardRequests("")(func(ctx echo.Context) error {
					called = true
					if body.reads != 0 {
						test.Fatal("outer guard read the body before downstream authentication")
					}
					count, err := io.Copy(io.Discard, ctx.Request().Body)
					if count != example.limit {
						test.Fatalf("wrong wire limit: got %d, want %d", count, example.limit)
					}
					if extra == 0 && err != nil {
						test.Fatalf("exact-limit body rejected: %v", err)
					}
					if extra != 0 {
						failure, ok := errors.AsType[*http.MaxBytesError](err)
						if !ok || failure.Limit != example.limit {
							test.Fatal("over-limit body did not preserve the exact wire boundary")
						}
					}
					return nil
				})
				if err := guard(server.NewContext(request, response)); err != nil || !called {
					test.Fatalf("outer guard failed: %v", err)
				}
				if response.Header().Get("Cache-Control") != "no-store" {
					test.Fatal("outer guard lost the private response policy")
				}
			}
		})
	}
}

func TestImportSelectionOuterGuardRejectsOriginWithoutReading(test *testing.T) {
	body := &selectionGuardBody{}
	request := httptest.NewRequest(http.MethodPost, "http://localhost"+importv1connect.ImportServiceCommitImportSelectionProcedure, body)
	request.Header.Set("Origin", "https://untrusted.example")
	server := echo.New()
	called := false
	guard := guardRequests("")(func(ctx echo.Context) error {
		called = true
		return nil
	})
	err := guard(server.NewContext(request, httptest.NewRecorder()))
	failure, ok := errors.AsType[*echo.HTTPError](err)
	if !ok || failure.Code != http.StatusForbidden || called || body.reads != 0 {
		test.Fatal("origin rejection read a body or reached the downstream handler")
	}
}
