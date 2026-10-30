package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestConditionalPublishAndAdapterRead(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	file, ref := sealedFile(test, 1<<20)
	result, err := target.PutSealed(test.Context(), ref, file)
	if err != nil || result != VerifiedNew {
		test.Fatalf("publish result=%v error=%v", result, err)
	}
	result, err = target.PutSealed(test.Context(), ref, file)
	if err != nil || result != VerifiedExisting {
		test.Fatalf("existing result=%v error=%v", result, err)
	}
	verified, err := target.VerifyExact(test.Context(), ref)
	if err != nil || verified != Verified {
		test.Fatalf("verify result=%v error=%v", verified, err)
	}
	var puts, heads, ranges int
	for _, call := range fixture.snapshot() {
		if call.method == "PUT" {
			puts++
			if call.condition != "*" {
				test.Fatal("unconditional publication")
			}
		}
		if call.method == "HEAD" {
			heads++
		}
		if call.rangeHeader != "" {
			ranges++
		}
	}
	if puts != 2 || heads != 3 || ranges != 6 {
		test.Fatalf("actual paths puts=%d HEAD=%d ranges=%d", puts, heads, ranges)
	}
}

func TestSealedBoundsAndBorrowedSource(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	for _, size := range []int64{1, 1 << 20, (64 << 20) + 4096} {
		test.Run(fmt.Sprint(size), func(test *testing.T) {
			file, ref := sealedFile(test, size)
			before, _ := file.Seek(0, io.SeekCurrent)
			literalDigest := ref.read().digest
			if size == 1 && hex.EncodeToString(literalDigest[:]) != "ca358758f6d27e6cf45272937977a748fd88391db679ceda7dc7bf1f005ee879" {
				test.Fatal("literal independent SHA mismatch")
			}
			attempt, _ := NewAttempt([16]byte{1}, [16]byte{2}, [16]byte{byte(size%251 + 1)})
			data := ref.read()
			ref, _ = NewObjectRef(attempt, DataExtent, 0, size, data.digest)
			calls := len(fixture.snapshot())
			result, err := target.PutSealed(test.Context(), ref, file)
			if err != nil || result != VerifiedNew {
				test.Fatalf("max extent: %v %v", result, err)
			}
			after, _ := file.Seek(0, io.SeekCurrent)
			if before != after {
				test.Fatal("borrowed file offset changed")
			}
			if _, err := file.Stat(); err != nil {
				test.Fatal("borrowed file closed")
			}
			puts := 0
			for _, call := range fixture.snapshot()[calls:] {
				if strings.Contains(call.query, "uploads") || strings.Contains(call.query, "uploadId") {
					test.Fatal("multipart path used")
				}
				if call.method == "PUT" {
					puts++
					if call.condition != "*" {
						test.Fatal("missing condition")
					}
				}
			}
			if puts != 1 {
				test.Fatal("not exactly one PUT")
			}
			test.Logf("actual TLS ciphertext_bytes=%d conditional_puts=%d multipart=0 borrowed_offset_preserved=true", size, puts)
		})
	}
	file, ref := sealedFile(test, 8)
	data := ref.read()
	wrong, _ := NewObjectRef(Attempt{read: func() attemptData { return data.attempt }}, DataExtent, 0, data.size, sha256.Sum256([]byte("wrong")))
	before := len(fixture.snapshot())
	if _, err := target.PutSealed(test.Context(), wrong, file); !errors.Is(err, ErrInvalid) {
		test.Fatal("bad local hash accepted")
	}
	if len(fixture.snapshot()) != before {
		test.Fatal("invalid hash reached network")
	}
	if _, err := NewObjectRef(Attempt{read: func() attemptData { return data.attempt }}, DataExtent, 0, MaxCiphertextBytes+1, data.digest); !errors.Is(err, ErrInvalid) {
		test.Fatal("oversized ref")
	}
}

func TestMissingAndHostileResponses(test *testing.T) {
	for _, row := range []struct {
		name    string
		status  int
		code    string
		missing bool
	}{
		{"typed-missing", 404, "NoSuchKey", true}, {"generic404", 404, "NotFound", false}, {"bucket404", 404, "NoSuchBucket", false}, {"denied", 403, "AccessDenied", false}, {"malformed", 404, "", false},
	} {
		test.Run(row.name, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, true)
			_, ref := sealedFile(test, 8)
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				if row.code == "" {
					writer.WriteHeader(row.status)
					io.WriteString(writer, "not xml")
				} else {
					fixtureError(writer, row.status, row.code)
				}
				return true
			}
			result, err := target.VerifyExact(test.Context(), ref)
			if row.missing {
				if result != Missing || err != nil {
					test.Fatalf("explicit Missing failed: %v %v", result, err)
				}
			} else if result == Missing || result == Verified || err == nil {
				test.Fatalf("unsafe negative: %v %v", result, err)
			}
			for _, call := range fixture.snapshot() {
				if call.method != "GET" {
					test.Fatal("fallback used")
				}
			}
		})
	}
	for _, mode := range []string{"head403", "head-length", "head-time", "get-range", "get-size", "get-encoding", "short", "digest", "error-cap", "list-cap"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, true)
			file, ref := sealedFile(test, 1024)
			if result, err := target.PutSealed(test.Context(), ref, file); err != nil || result != VerifiedNew {
				test.Fatal(result, err)
			}
			fixture.mutex.Lock()
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				full := request.Method == "GET" && request.Header.Get("Range") != "bytes=0-0"
				switch {
				case mode == "head403" && request.Method == "HEAD":
					fixtureError(writer, 403, "AccessDenied")
					return true
				case (mode == "head-length" || mode == "head-time") && request.Method == "HEAD":
					if mode != "head-time" {
						writer.Header().Set("Last-Modified", "Tue, 14 Nov 2023 22:13:20 GMT")
					}
					if mode != "head-length" {
						writer.Header().Set("Content-Length", "1024")
					}
					writer.WriteHeader(200)
					return true
				case mode == "error-cap" && request.Method == "GET":
					writer.WriteHeader(500)
					io.WriteString(writer, strings.Repeat("x", 128<<10))
					return true
				case mode == "list-cap" && request.URL.Query().Get("list-type") == "2":
					writer.Header().Set("Content-Type", "application/xml")
					io.WriteString(writer, strings.Repeat("x", 512<<10))
					return true
				case full && strings.HasPrefix(mode, "get-"):
					writer.Header().Set("Content-Length", "1024")
					writer.Header().Set("Content-Range", "bytes 0-1023/1024")
					if mode == "get-range" {
						writer.Header().Set("Content-Range", "bytes 1-1024/1025")
					}
					if mode == "get-size" {
						writer.Header().Set("Content-Length", "1025")
					}
					if mode == "get-encoding" {
						writer.Header().Set("Content-Encoding", "gzip")
					}
					writer.WriteHeader(206)
					io.WriteString(writer, strings.Repeat("x", 1024))
					return true
				case full && (mode == "short" || mode == "digest"):
					writer.Header().Set("Content-Length", "1024")
					writer.Header().Set("Content-Range", "bytes 0-1023/1024")
					writer.WriteHeader(206)
					size := 1024
					if mode == "short" {
						size = 8
					}
					io.WriteString(writer, strings.Repeat("x", size))
					return true
				}
				return false
			}
			fixture.mutex.Unlock()
			if mode == "list-cap" {
				attempt := Attempt{read: func() attemptData { return ref.read().attempt }}
				if _, err := target.ListAttemptPage(test.Context(), attempt, Cursor{}); err == nil {
					test.Fatal("oversized list accepted")
				}
				return
			}
			result, err := target.VerifyExact(test.Context(), ref)
			if result == Verified || result == Missing {
				test.Fatalf("hostile response accepted: %v %v", result, err)
			}
			for _, call := range fixture.snapshot() {
				if strings.Contains(call.query, "list-type") {
					test.Fatal("Afero fallback List dispatched")
				}
			}
		})
	}
}

func TestConditionalRaceConflictAndUnknown(test *testing.T) {
	fixture := newFixture(test)
	first := fixture.target(test, true)
	second := fixture.target(test, true)
	file, ref := sealedFile(test, 128)
	results := make(chan PutResult, 2)
	for _, target := range []*S3Target{first, second} {
		go func() { result, _ := target.PutSealed(test.Context(), ref, file); results <- result }()
	}
	left, right := <-results, <-results
	if !((left == VerifiedNew && right == VerifiedExisting) || (right == VerifiedNew && left == VerifiedExisting)) {
		test.Fatalf("race outcomes %v %v", left, right)
	}
	data := ref.read()
	different, _ := NewObjectRef(Attempt{read: func() attemptData { return data.attempt }}, data.kind, data.ordinal, data.size, sha256.Sum256([]byte("different")))
	result, err := first.VerifyExact(test.Context(), different)
	if err != nil || result != VerifyConflict {
		test.Fatalf("collision %v %v", result, err)
	}
	fixture.mutex.Lock()
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		if request.Method == "PUT" {
			fixtureError(writer, 500, "InternalError")
			return true
		}
		return false
	}
	fixture.mutex.Unlock()
	before := len(fixture.snapshot())
	outcome, _ := first.PutSealed(test.Context(), ref, file)
	if outcome != PutUnknown {
		test.Fatal("ambiguous PUT not Unknown")
	}
	if result, err := second.VerifyExact(test.Context(), ref); result != Verified || err != nil {
		test.Fatal("read-only reconciliation failed", result, err)
	}
	puts := 0
	for _, call := range fixture.snapshot()[before:] {
		if call.method == "PUT" {
			puts++
		}
	}
	if puts != 1 {
		test.Fatal("mutation replay")
	}
}

func TestScopedListAndConditionalDelete(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	file, ref := sealedFile(test, 64)
	attempt := Attempt{read: func() attemptData { return ref.read().attempt }}
	page, err := target.ListAttemptPage(test.Context(), attempt, Cursor{})
	if err != nil || len(page.Objects()) != 0 {
		test.Fatal("empty list", err)
	}
	if result, err := target.PutSealed(test.Context(), ref, file); result != VerifiedNew || err != nil {
		test.Fatal(result, err)
	}
	page, err = target.ListAttemptPage(test.Context(), attempt, Cursor{})
	if err != nil || len(page.Objects()) != 1 || !page.Next().IsZero() {
		test.Fatal("scoped list", err)
	}
	permit, verified, err := target.InspectForDelete(test.Context(), ref)
	if err != nil || verified != Verified {
		test.Fatal(verified, err)
	}
	foreign := fixture.target(test, true)
	before := len(fixture.snapshot())
	if _, err := foreign.DeleteExact(test.Context(), permit); !errors.Is(err, ErrInvalid) {
		test.Fatal("foreign permit")
	}
	if len(fixture.snapshot()) != before {
		test.Fatal("foreign permit made request")
	}
	if result, err := target.DeleteExact(test.Context(), permit); result != AbsentVerified || err != nil {
		test.Fatal("conditional delete", result, err)
	}
	for _, call := range fixture.snapshot() {
		if call.method == "DELETE" && call.match == "" {
			test.Fatal("unconditional delete")
		}
		if strings.Contains(call.query, "list-type") && !strings.Contains(call.query, "prefix=backups%2F010000") {
			test.Fatal("prefix widened")
		}
	}
	verified, err = target.VerifyExact(test.Context(), ref)
	if verified != Missing || err != nil {
		test.Fatal("absence", verified, err)
	}
	disabled := fixture.target(test, false)
	if _, err := disabled.DeleteExact(test.Context(), DeletePermit{}); !errors.Is(err, ErrInvalid) {
		test.Fatal("disabled delete")
	}
}

func TestOpaquePrivacyAndInputIsolation(test *testing.T) {
	access, secret, token := []byte("PRIVATE-ACCESS"), []byte("PRIVATE-SECRET"), []byte("PRIVATE-TOKEN")
	credentials, err := NewCredentials(access, secret, token)
	if err != nil {
		test.Fatal(err)
	}
	clear(access)
	clear(secret)
	clear(token)
	if string(credentials.read().secret) != "PRIVATE-SECRET" {
		test.Fatal("credentials aliased")
	}
	options, err := NewS3Options("https://example.invalid", "test", "synthetic", "root", [16]byte{1}, credentials, false)
	if err != nil {
		test.Fatal(err)
	}
	for _, value := range []any{credentials, options, []any{credentials, options}, map[string]any{"credentials": credentials, "options": options}, struct {
		Credentials Credentials
		Options     S3Options
	}{credentials, options}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			text := fmt.Sprintf(verb, value)
			if strings.Contains(text, "PRIVATE-") {
				test.Fatal("fmt leaked")
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil || strings.Contains(string(encoded), "PRIVATE-") {
			test.Fatal("JSON leaked")
		}
		var logs strings.Builder
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("synthetic", "value", value)
		if strings.Contains(logs.String(), "PRIVATE-") {
			test.Fatal("slog leaked")
		}
	}
	for _, root := range []string{"", "/root", "a/../b", "a//b", "a%2fb", "a\\b", "a/", "é"} {
		if _, err := NewS3Options("https://example.invalid", "test", "synthetic", root, [16]byte{1}, credentials, false); !errors.Is(err, ErrInvalid) {
			test.Fatal("noncanonical root", root)
		}
	}
	for _, endpoint := range []string{"http://example.invalid", "https://user:pass@example.invalid", "https://example.invalid/", "https://example.invalid?q=1", "https://example.invalid#fragment"} {
		if _, err := NewS3Options(endpoint, "test", "synthetic", "root", [16]byte{1}, credentials, false); !errors.Is(err, ErrInvalid) {
			test.Fatal("bad endpoint")
		}
	}
	first, _ := NewS3Target(options)
	second, _ := NewS3Target(options)
	first.Close()
	if string(second.options.read().credentials.read().secret) != "PRIVATE-SECRET" {
		test.Fatal("Close corrupted independent credentials")
	}
	second.Close()
}

func TestPreCancelledAndForeignInstance(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, false)
	file, ref := sealedFile(test, 1)
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := target.PutSealed(ctx, ref, file); !errors.Is(err, context.Canceled) {
		test.Fatal("cancellation", err)
	}
	attempt, _ := NewAttempt([16]byte{9}, [16]byte{2}, [16]byte{3})
	wrong, _ := NewObjectRef(attempt, DataExtent, 0, 1, ref.read().digest)
	if _, err := target.VerifyExact(test.Context(), wrong); !errors.Is(err, ErrInvalid) {
		test.Fatal("cross instance", err)
	}
	if len(fixture.snapshot()) != 0 {
		test.Fatal("pre-admission network")
	}
	if _, err := os.Stat(file.Name()); err != nil {
		test.Fatal("source removed")
	}
}

func TestListAdversariesAndPagination(test *testing.T) {
	for _, mode := range []string{"fifty", "fiftyone", "duplicate", "foreign", "bad-kind", "nil-size", "nil-time", "empty-etag", "bad-token", "same-token", "nil-truncated", "keycount", "pagination"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, true)
			_, ref := sealedFile(test, 16)
			data := ref.read()
			attempt := Attempt{read: func() attemptData { return data.attempt }}
			prefix := attemptPrefix(target.options.read(), data.attempt)
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				count := 1
				switch mode {
				case "fifty":
					count = 50
				case "fiftyone":
					count = 51
				case "duplicate":
					count = 2
				}
				writer.Header().Set("Content-Type", "application/xml")
				io.WriteString(writer, "<ListBucketResult>")
				truncated := "false"
				if mode == "bad-token" || mode == "same-token" || mode == "pagination" {
					truncated = "true"
				}
				if mode != "nil-truncated" {
					fmt.Fprintf(writer, "<IsTruncated>%s</IsTruncated>", truncated)
				}
				keyCount := count
				if mode == "keycount" {
					keyCount = 2
				}
				fmt.Fprintf(writer, "<KeyCount>%d</KeyCount>", keyCount)
				for index := range count {
					objectKey := fmt.Sprintf("%sdata/%08x", prefix, index)
					if mode == "duplicate" {
						objectKey = prefix + "data/00000000"
					}
					if mode == "foreign" {
						objectKey = "foreign/data/00000000"
					}
					if mode == "bad-kind" {
						objectKey = prefix + "arbitrary/00000000"
					}
					fmt.Fprintf(writer, "<Contents><Key>%s</Key>", objectKey)
					if mode != "nil-size" {
						io.WriteString(writer, "<Size>16</Size>")
					}
					if mode != "nil-time" {
						io.WriteString(writer, "<LastModified>2023-11-14T22:13:20Z</LastModified>")
					}
					if mode == "empty-etag" {
						io.WriteString(writer, "<ETag></ETag>")
					} else {
						io.WriteString(writer, "<ETag>opaque</ETag>")
					}
					io.WriteString(writer, "</Contents>")
				}
				switch mode {
				case "bad-token":
					fmt.Fprintf(writer, "<NextContinuationToken>%s</NextContinuationToken>", strings.Repeat("x", 4097))
				case "same-token":
					io.WriteString(writer, "<NextContinuationToken>same</NextContinuationToken>")
				case "pagination":
					io.WriteString(writer, "<NextContinuationToken>next</NextContinuationToken>")
				}
				io.WriteString(writer, "</ListBucketResult>")
				return true
			}
			cursor := Cursor{}
			if mode == "same-token" {
				previous := cursorData{target, data.attempt, "same", 1}
				cursor = Cursor{read: func() cursorData { return previous }}
			}
			page, err := target.ListAttemptPage(test.Context(), attempt, cursor)
			if mode == "fifty" {
				if err != nil || len(page.Objects()) != 50 {
					test.Fatal("legal maximum", err)
				}
				return
			}
			if mode == "pagination" {
				if err != nil || page.Next().IsZero() {
					test.Fatal("cursor missing", err)
				}
				before := len(fixture.snapshot())
				peer := fixture.target(test, true)
				if _, err := peer.ListAttemptPage(test.Context(), attempt, page.Next()); !errors.Is(err, ErrInvalid) {
					test.Fatal("cursor target binding")
				}
				if len(fixture.snapshot()) != before {
					test.Fatal("foreign cursor dispatched")
				}
				return
			}
			if err == nil || len(page.Objects()) != 0 || !page.Next().IsZero() {
				test.Fatalf("malformed list returned data: %s err=%v", mode, err)
			}
		})
	}
}

func TestDeleteConflictAndUncertainOutcomes(test *testing.T) {
	for _, mode := range []string{"conflict", "lost", "still-present", "denied-confirm", "disabled"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, mode != "disabled")
			file, ref := sealedFile(test, 32)
			if result, err := target.PutSealed(test.Context(), ref, file); result != VerifiedNew || err != nil {
				test.Fatal(result, err)
			}
			permit, result, err := target.InspectForDelete(test.Context(), ref)
			if result != Verified || err != nil {
				test.Fatal(result, err)
			}
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				if request.Method == "DELETE" {
					switch mode {
					case "conflict":
						fixtureError(writer, 412, "PreconditionFailed")
					case "lost":
						fixture.mutex.Lock()
						delete(fixture.objects, key)
						fixture.mutex.Unlock()
						connection, _, err := writer.(http.Hijacker).Hijack()
						if err != nil {
							panic(err)
						}
						connection.Close()
					case "still-present", "denied-confirm":
						writer.WriteHeader(204)
					default:
						return false
					}
					return true
				}
				if mode == "denied-confirm" && request.Method == "GET" {
					fixtureError(writer, 403, "AccessDenied")
					return true
				}
				return false
			}
			before := len(fixture.snapshot())
			deleted, err := target.DeleteExact(test.Context(), permit)
			if mode == "disabled" {
				if !errors.Is(err, ErrInvalid) || len(fixture.snapshot()) != before {
					test.Fatal("disabled prune dispatched")
				}
				return
			}
			if mode == "conflict" {
				if deleted != DeleteConflict {
					test.Fatal("delete conflict", deleted, err)
				}
			} else if deleted != DeleteUnknown {
				test.Fatal("uncertain deletion reported absent", deleted, err)
			}
			deletes := 0
			for _, call := range fixture.snapshot()[before:] {
				if call.method == "DELETE" {
					deletes++
				}
			}
			if deletes != 1 {
				test.Fatal("delete replay")
			}
		})
	}
}

func TestListOmissionDoesNotProveMissing(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	file, ref := sealedFile(test, 16)
	if result, err := target.PutSealed(test.Context(), ref, file); result != VerifiedNew || err != nil {
		test.Fatal(result, err)
	}
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		if request.URL.Query().Get("list-type") == "2" {
			io.WriteString(writer, "<ListBucketResult><IsTruncated>false</IsTruncated><KeyCount>0</KeyCount></ListBucketResult>")
			return true
		}
		return false
	}
	attempt := Attempt{read: func() attemptData { return ref.read().attempt }}
	page, err := target.ListAttemptPage(test.Context(), attempt, Cursor{})
	if err != nil || len(page.Objects()) != 0 {
		test.Fatal(err)
	}
	if result, err := target.VerifyExact(test.Context(), ref); result != Verified || err != nil {
		test.Fatal("List absence overrode exact read", result, err)
	}
}

func TestNewPutCorruptReadbackIsUnknown(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	file, ref := sealedFile(test, 32)
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		if request.Method == "GET" && request.Header.Get("Range") != "bytes=0-0" {
			writer.Header().Set("Content-Length", "32")
			writer.Header().Set("Content-Range", "bytes 0-31/32")
			writer.WriteHeader(206)
			io.WriteString(writer, strings.Repeat("x", 32))
			return true
		}
		return false
	}
	if result, _ := target.PutSealed(test.Context(), ref, file); result != PutUnknown {
		test.Fatalf("new PUT unverifiable readback must remain Unknown: %v", result)
	}
}

func TestPreexistingDifferentCiphertextConflicts(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, true)
	file, ref := sealedFile(test, 32)
	if result, err := target.PutSealed(test.Context(), ref, file); result != VerifiedNew || err != nil {
		test.Fatal(result, err)
	}
	replacement := []byte(strings.Repeat("z", 32))
	if _, err := file.WriteAt(replacement, 0); err != nil {
		test.Fatal(err)
	}
	data := ref.read()
	other, err := NewObjectRef(Attempt{read: func() attemptData { return data.attempt }}, data.kind, data.ordinal, data.size, sha256.Sum256(replacement))
	if err != nil {
		test.Fatal(err)
	}
	if result, err := target.PutSealed(test.Context(), other, file); result != PutConflict || err != nil {
		test.Fatal("immutable collision", result, err)
	}
}
