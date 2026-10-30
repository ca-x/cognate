package backup

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureObject struct {
	path, etag string
	size       int64
}
type wireCall struct{ method, key, rangeHeader, condition, match, query string }
type s3Fixture struct {
	server  *httptest.Server
	dir     string
	mutex   sync.Mutex
	objects map[string]fixtureObject
	calls   []wireCall
	hook    func(http.ResponseWriter, *http.Request, string) bool
}

func newFixture(test *testing.T) *s3Fixture {
	test.Helper()
	fixture := &s3Fixture{dir: test.TempDir(), objects: make(map[string]fixtureObject)}
	fixture.server = httptest.NewTLSServer(http.HandlerFunc(fixture.serve))
	test.Cleanup(fixture.server.Close)
	return fixture
}
func (fixture *s3Fixture) target(test *testing.T, prune bool) *S3Target {
	test.Helper()
	credentials, err := NewCredentials([]byte("synthetic-access"), []byte("SYNTHETIC-PRIVATE-SECRET"), []byte("SYNTHETIC-TOKEN"))
	if err != nil {
		test.Fatal(err)
	}
	options, err := NewS3Options(fixture.server.URL, "local-test-1", "synthetic", "backups", [16]byte{1}, credentials, prune)
	if err != nil {
		test.Fatal(err)
	}
	target, err := NewS3Target(options)
	if err != nil {
		test.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(fixture.server.Certificate())
	target.roots = roots
	test.Cleanup(func() {
		if err := target.Close(); err != nil {
			test.Error(err)
		}
	})
	return target
}
func (fixture *s3Fixture) snapshot() []wireCall {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	return slices.Clone(fixture.calls)
}
func fixtureError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/xml")
	writer.WriteHeader(status)
	fmt.Fprintf(writer, "<Error><Code>%s</Code><Message>synthetic</Message></Error>", code)
}
func (fixture *s3Fixture) serve(writer http.ResponseWriter, request *http.Request) {
	key := strings.TrimPrefix(request.URL.Path, "/synthetic/")
	fixture.mutex.Lock()
	fixture.calls = append(fixture.calls, wireCall{request.Method, key, request.Header.Get("Range"), request.Header.Get("If-None-Match"), request.Header.Get("If-Match"), request.URL.RawQuery})
	hook := fixture.hook
	fixture.mutex.Unlock()
	if hook != nil && hook(writer, request, key) {
		return
	}
	if request.URL.Query().Get("list-type") == "2" {
		fixture.list(writer, request)
		return
	}
	switch request.Method {
	case http.MethodPut:
		file, err := os.CreateTemp(fixture.dir, "object-")
		if err != nil {
			panic(err)
		}
		hash := sha256.New()
		size, copyErr := io.CopyBuffer(io.MultiWriter(file, hash), request.Body, make([]byte, 32<<10))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			fixtureError(writer, 500, "InternalError")
			return
		}
		object := fixtureObject{file.Name(), `"opaque-` + hex.EncodeToString(hash.Sum(nil)[:8]) + `"`, size}
		fixture.mutex.Lock()
		_, exists := fixture.objects[key]
		if !exists || request.Header.Get("If-None-Match") != "*" {
			fixture.objects[key] = object
		}
		fixture.mutex.Unlock()
		if exists && request.Header.Get("If-None-Match") == "*" {
			fixtureError(writer, 412, "PreconditionFailed")
			return
		}
		writer.Header().Set("ETag", object.etag)
		writer.WriteHeader(200)
	case http.MethodHead, http.MethodGet:
		fixture.mutex.Lock()
		object, exists := fixture.objects[key]
		fixture.mutex.Unlock()
		if !exists {
			fixtureError(writer, 404, "NoSuchKey")
			return
		}
		writer.Header().Set("Last-Modified", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
		writer.Header().Set("ETag", object.etag)
		start, end := int64(0), object.size-1
		ranged := request.Method == http.MethodGet && request.Header.Get("Range") != ""
		if ranged {
			value := strings.TrimPrefix(request.Header.Get("Range"), "bytes=")
			first, last, _ := strings.Cut(value, "-")
			start, _ = strconv.ParseInt(first, 10, 64)
			if last != "" {
				end, _ = strconv.ParseInt(last, 10, 64)
			}
			if start < 0 || start >= object.size || end < start || end >= object.size {
				fixtureError(writer, 416, "InvalidRange")
				return
			}
			writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, object.size))
		}
		writer.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		if ranged {
			writer.WriteHeader(206)
		}
		if request.Method == http.MethodHead {
			return
		}
		file, err := os.Open(object.path)
		if err != nil {
			panic(err)
		}
		defer file.Close()
		io.CopyBuffer(writer, io.NewSectionReader(file, start, end-start+1), make([]byte, 32<<10))
	case http.MethodDelete:
		fixture.mutex.Lock()
		object, exists := fixture.objects[key]
		if exists && request.Header.Get("If-Match") != "" && request.Header.Get("If-Match") != object.etag {
			fixture.mutex.Unlock()
			fixtureError(writer, 412, "PreconditionFailed")
			return
		}
		delete(fixture.objects, key)
		fixture.mutex.Unlock()
		writer.WriteHeader(204)
	default:
		fixtureError(writer, 400, "InvalidRequest")
	}
}
func (fixture *s3Fixture) list(writer http.ResponseWriter, request *http.Request) {
	type entry struct {
		Key          string
		Size         int64
		ETag         string
		LastModified string
	}
	response := struct {
		XMLName     xml.Name `xml:"ListBucketResult"`
		IsTruncated bool
		Contents    []entry
		KeyCount    int
	}{}
	prefix := request.URL.Query().Get("prefix")
	fixture.mutex.Lock()
	for key, object := range fixture.objects {
		if strings.HasPrefix(key, prefix) {
			response.Contents = append(response.Contents, entry{key, object.size, object.etag, "2023-11-14T22:13:20Z"})
		}
	}
	fixture.mutex.Unlock()
	slices.SortFunc(response.Contents, func(first, second entry) int { return strings.Compare(first.Key, second.Key) })
	response.KeyCount = len(response.Contents)
	writer.Header().Set("Content-Type", "application/xml")
	xml.NewEncoder(writer).Encode(response)
}
func sealedFile(test *testing.T, size int64) (*os.File, ObjectRef) {
	test.Helper()
	file, err := os.CreateTemp(test.TempDir(), "sealed-")
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { file.Close() })
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	for index := range buffer {
		buffer[index] = byte(index*31 + 7)
	}
	for remaining := size; remaining > 0; {
		count := min(remaining, int64(len(buffer)))
		if _, err := io.MultiWriter(file, hash).Write(buffer[:count]); err != nil {
			test.Fatal(err)
		}
		remaining -= count
	}
	attempt, err := NewAttempt([16]byte{1}, [16]byte{2}, [16]byte{3})
	if err != nil {
		test.Fatal(err)
	}
	ref, err := NewObjectRef(attempt, DataExtent, 0, size, [32]byte(hash.Sum(nil)))
	if err != nil {
		test.Fatal(err)
	}
	return file, ref
}
