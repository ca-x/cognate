package importclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
)

func TestSelectionSourceFileBoundaries(test *testing.T) {
	for _, source := range []struct {
		id    int
		limit int
	}{{1, 16 << 20}, {2, 1 << 20}} {
		for _, size := range []int{0, 1, source.limit, source.limit + 1} {
			path := filepath.Join(test.TempDir(), "source")
			file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0o600)
			if err != nil {
				test.Fatal(err)
			}
			if err := errors.Join(file.Truncate(int64(size)), file.Close()); err != nil {
				test.Fatal(err)
			}
			data, hash, err := selectionReadFile(test.Context(), importv1.ImportSelectionSource(source.id), path, "")
			if size > 0 && size <= source.limit {
				if err != nil || len(data) != size || !validHash(hash) {
					test.Fatal("valid source boundary rejected")
				}
			} else if err == nil {
				test.Fatal("invalid source boundary accepted")
			}
			clear(data)
		}
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, _, err := selectionReadFile(ctx, 2, "unread", ""); !errors.Is(err, context.Canceled) {
		test.Fatal("canceled source read was attempted")
	}
}

type selectionLimitBody struct {
	remaining, read int
	closed          bool
}

func (body *selectionLimitBody) Read(buffer []byte) (int, error) {
	if body.remaining == 0 {
		return 0, io.EOF
	}
	count := min(len(buffer), body.remaining)
	clear(buffer[:count])
	body.remaining -= count
	body.read += count
	return count, nil
}
func (body *selectionLimitBody) Close() error { body.closed = true; return nil }

type selectionLimitTransport func(*http.Request) (*http.Response, error)

func (transport selectionLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestSelectionWireBodyLimitsBeforeAggregation(test *testing.T) {
	for _, entry := range []struct {
		procedure string
		limit     int
	}{{importv1connect.ImportServicePreviewImportSelectionProcedure, 2 << 20}, {importv1connect.ImportServiceGetImportCommitStatusProcedure, 128 << 10}} {
		for _, declared := range []int64{-1, int64(entry.limit + 1)} {
			body := &selectionLimitBody{remaining: 8 << 20}
			transport := selectionTransport{scheme: "https", host: "fixture.invalid", base: selectionLimitTransport(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("Accept-Encoding") != "identity" {
					test.Error("non-identity response requested")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: declared, Body: body}, nil
			})}
			request, err := http.NewRequest(http.MethodPost, "https://fixture.invalid"+entry.procedure, nil)
			if err != nil {
				test.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if declared >= 0 {
				if err == nil || body.read != 0 || !body.closed {
					test.Fatal("declared over-limit body read")
				}
				continue
			}
			if err != nil {
				test.Fatal(err)
			}
			data, readErr := io.ReadAll(response.Body)
			clear(data)
			_ = response.Body.Close()
			if readErr == nil || body.read > entry.limit+1 || !body.closed {
				test.Fatal("streamed over-limit body aggregated")
			}
		}
	}
	body := &selectionLimitBody{remaining: 8 << 20}
	transport := selectionTransport{scheme: "https", host: "fixture.invalid", base: selectionLimitTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Encoding": []string{"gzip"}}, Body: body}, nil
	})}
	request, _ := http.NewRequest(http.MethodPost, "https://fixture.invalid"+importv1connect.ImportServicePreviewImportSelectionProcedure, nil)
	if _, err := transport.RoundTrip(request); err == nil || body.read != 0 || !body.closed {
		test.Fatal("compressed response body admitted")
	}
	for _, path := range []string{"/other", importv1connect.ImportServicePreviewImportSelectionProcedure + "?secret=synthetic", importv1connect.ImportServicePreviewImportSelectionProcedure + "#fragment"} {
		request, _ := http.NewRequest(http.MethodPost, "https://fixture.invalid"+path, strings.NewReader(""))
		if _, err := transport.RoundTrip(request); err == nil {
			test.Fatal("unknown procedure or origin suffix accepted")
		}
	}
}
