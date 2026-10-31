package backup

import (
	"errors"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func TestConditionalPutWithoutAPIError(test *testing.T) {
	fixture := newFixture(test)
	target := fixture.target(test, false)
	file, ref := sealedFile(test, 8)
	if result, err := target.PutSealed(test.Context(), ref, file); err != nil || result != VerifiedNew {
		test.Fatal("initial conditional publication failed")
	}
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, _ string) bool {
		if request.Method != http.MethodPut {
			return false
		}
		writer.Header().Set("Content-Type", "application/xml")
		writer.WriteHeader(http.StatusPreconditionFailed)
		_, _ = io.WriteString(writer, "<Error><Code>")
		return true
	}
	operation, err := target.begin(test.Context(), "put", 4)
	if err != nil {
		test.Fatal("cannot start SDK shape probe")
	}
	data, err := operation.bind(ref)
	if err != nil {
		operation.end()
		test.Fatal("cannot bind SDK shape probe")
	}
	options := target.options.read()
	_, wireError := operation.client("PutObject").PutObject(operation.ctx, &s3.PutObjectInput{Bucket: new(options.bucket), Key: new(operation.key), ContentLength: new(data.size), IfNoneMatch: new("*"), Body: io.NewSectionReader(file, 0, data.size)})
	operation.end()
	_, typedAPIError := errors.AsType[smithy.APIError](wireError)
	response, responseError := errors.AsType[*smithyhttp.ResponseError](wireError)
	if wireError == nil || typedAPIError || !responseError || response.HTTPStatusCode() != http.StatusPreconditionFailed {
		test.Fatal("fixture did not reproduce HTTP 412 without an APIError")
	}
	before := len(fixture.snapshot())
	result, err := target.PutSealed(test.Context(), ref, file)
	if err != nil || result != VerifiedExisting {
		test.Fatalf("exact existing object rejected after untyped 412: result=%d error=%v", result, err)
	}
	puts, reads := 0, 0
	for _, call := range fixture.snapshot()[before:] {
		if call.method == http.MethodPut {
			puts++
			if call.condition != "*" {
				test.Fatal("conditional publication was weakened")
			}
		} else if call.method == http.MethodGet || call.method == http.MethodHead {
			reads++
		}
	}
	if puts != 1 || reads == 0 {
		test.Fatal("untyped 412 was retried or accepted without exact readback", puts, reads)
	}
}

func TestConditionalPutStatusDoesNotAuthorizeExisting(test *testing.T) {
	for _, scenario := range []struct {
		name     string
		status   int
		missing  bool
		changed  bool
		denied   bool
		result   PutResult
		readback bool
	}{
		{name: "different-ciphertext", status: 412, changed: true, result: PutConflict, readback: true},
		{name: "missing-object", status: 412, missing: true, result: PutUnknown, readback: true},
		{name: "readback-denied", status: 412, denied: true, result: PutUnknown, readback: true},
		{name: "conflict-is-not-precondition", status: 409, result: PutUnknown},
		{name: "server-error-is-not-precondition", status: 500, result: PutUnknown},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, false)
			file, ref := sealedFile(test, 8)
			if !scenario.missing {
				if result, err := target.PutSealed(test.Context(), ref, file); err != nil || result != VerifiedNew {
					test.Fatal("initial conditional publication failed")
				}
			}
			if scenario.changed {
				fixture.mutex.Lock()
				object := fixture.objects[objectKey(target.options.read(), ref.read())]
				fixture.mutex.Unlock()
				if err := os.WriteFile(object.path, []byte("changed!"), 0o600); err != nil {
					test.Fatal(err)
				}
			}
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, _ string) bool {
				if request.Method == http.MethodPut {
					writer.Header().Set("Content-Type", "application/xml")
					writer.WriteHeader(scenario.status)
					_, _ = io.WriteString(writer, "<Error><Code>")
					return true
				}
				if scenario.denied {
					fixtureError(writer, http.StatusForbidden, "AccessDenied")
					return true
				}
				return false
			}
			before := len(fixture.snapshot())
			result, err := target.PutSealed(test.Context(), ref, file)
			if result != scenario.result || (err != nil) != (scenario.result == PutUnknown) {
				test.Fatalf("unsafe conditional outcome: result=%d error=%v", result, err)
			}
			puts, reads := 0, 0
			for _, call := range fixture.snapshot()[before:] {
				if call.method == http.MethodPut {
					puts++
					if call.condition != "*" {
						test.Fatal("conditional publication was weakened")
					}
				} else if call.method == http.MethodGet || call.method == http.MethodHead {
					reads++
				}
			}
			if puts != 1 || (reads != 0) != scenario.readback {
				test.Fatal("unexpected retry or readback", puts, reads)
			}
		})
	}
}

func TestConditionalDeleteWithoutAPIError(test *testing.T) {
	for _, scenario := range []struct {
		name   string
		status int
		result DeleteResult
	}{
		{name: "precondition", status: http.StatusPreconditionFailed, result: DeleteConflict},
		{name: "conflict-is-not-precondition", status: http.StatusConflict, result: DeleteUnknown},
		{name: "server-error-is-not-precondition", status: http.StatusInternalServerError, result: DeleteUnknown},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			fixture := newFixture(test)
			target := fixture.target(test, true)
			file, ref := sealedFile(test, 8)
			if result, err := target.PutSealed(test.Context(), ref, file); err != nil || result != VerifiedNew {
				test.Fatal("initial conditional publication failed")
			}
			permit, verified, err := target.InspectForDelete(test.Context(), ref)
			if err != nil || verified != Verified {
				test.Fatal("delete inspection failed")
			}
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, _ string) bool {
				if request.Method != http.MethodDelete {
					return false
				}
				writer.Header().Set("Content-Type", "application/xml")
				writer.WriteHeader(scenario.status)
				_, _ = io.WriteString(writer, "<Error><Code>")
				return true
			}
			before := len(fixture.snapshot())
			result, err := target.DeleteExact(test.Context(), permit)
			if result != scenario.result || (err != nil) != (scenario.result == DeleteUnknown) {
				test.Fatalf("unsafe conditional deletion: result=%d error=%v", result, err)
			}
			calls := fixture.snapshot()[before:]
			if len(calls) != 1 || calls[0].method != http.MethodDelete || calls[0].match == "" {
				test.Fatal("conditional deletion was retried or weakened")
			}
			if verified, err := target.VerifyExact(test.Context(), ref); err != nil || verified != Verified {
				test.Fatal("failed deletion changed the existing object")
			}
		})
	}
}
