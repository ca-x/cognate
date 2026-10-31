package restore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
)

const archiveTestAlpha = "---\nname: Alpha\ndescription: archive-private-description\n---\narchive-private-instructions\n"
const archiveTestAlphaHash = "51218b6579dee181f4dccfe06ce46efa3da8bbfd7195882adf0a8bacb1230b34"
const archiveTestBetaHash = "fc5664ced84ad753f2e64b1ea62881abc2a497281299dec8bce5f400b22401e9"

type archiveTestEntry struct {
	name string
	data []byte
	kind byte
}

func archiveTestEntries() []archiveTestEntry {
	return []archiveTestEntry{
		{name: "root/.claude/skills/alpha/SKILL.md", data: []byte(archiveTestAlpha)},
		{name: "root/.claude/skills/alpha/icon.bin", data: []byte{0, 255, 16, 32, 0, 254}},
		{name: "root/skills/beta/SKILL.md", data: []byte("Second skill\n")},
	}
}

func archiveTestTar(test *testing.T, entries []archiveTestEntry) []byte {
	test.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0600, Typeflag: kind}
		if kind == tar.TypeReg {
			header.Size = int64(len(entry.data))
		} else if kind == tar.TypeSymlink {
			header.Linkname = "../../archive-private-outside"
		}
		if err := archive.WriteHeader(header); err != nil {
			test.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := archive.Write(entry.data); err != nil {
				test.Fatal(err)
			}
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	return output.Bytes()
}

func archiveTestExpected(data []byte) archiveExpectation {
	digest := sha256.Sum256(data)
	expected := archiveExpectation{hash: hex.EncodeToString(digest[:]), bytes: int64(len(data))}
	for range 3 {
		expected.repositories = append(expected.repositories, archiveRepositoryExpectation{skillCount: 2, skills: map[string]string{
			".claude/skills/alpha": archiveTestAlphaHash,
			"skills/beta":          archiveTestBetaHash,
		}})
	}
	return expected
}

func archiveTestLimits() archiveLimits {
	return archiveLimits{bytes: repository.MaxArchiveBytes, references: 1000000}
}

func archiveTestAssert(test *testing.T, ctx context.Context, data []byte, expected archiveExpectation, limits archiveLimits, want error) {
	test.Helper()
	original := bytes.Clone(data)
	declaration := expected
	declaration.repositories = slices.Clone(expected.repositories)
	for index := range declaration.repositories {
		declaration.repositories[index].skills = maps.Clone(expected.repositories[index].skills)
	}
	got := verifyArchive(ctx, data, expected, limits)
	if !bytes.Equal(data, original) || !reflect.DeepEqual(expected, declaration) {
		test.Fatal("borrowed archive or declaration changed")
	}
	if got != want {
		test.Fatalf("archive decision: got %v, want %v", got, want)
	}
}

func TestVerifyArchiveSharedBinary(test *testing.T) {
	data := archiveTestTar(test, archiveTestEntries())
	expected := archiveTestExpected(data)
	archiveTestAssert(test, test.Context(), data, expected, archiveTestLimits(), nil)
	entries := append(archiveTestEntries(), archiveTestEntry{name: "root/unrelated-link", kind: tar.TypeSymlink})
	data = archiveTestTar(test, entries)
	archiveTestAssert(test, test.Context(), data, archiveTestExpected(data), archiveTestLimits(), nil)
}

func TestVerifyArchiveDeclarationRefusals(test *testing.T) {
	for _, name := range []string{"size", "hash", "hash-short", "hash-uppercase", "hash-nonhex", "last-count", "last-cardinality", "last-missing", "last-extra", "last-path", "last-hash", "last-hash-uppercase", "empty-repositories", "nil-skills", "zero-count", "negative-count", "zero-bytes", "negative-bytes"} {
		test.Run(name, func(test *testing.T) {
			data := archiveTestTar(test, archiveTestEntries())
			expected := archiveTestExpected(data)
			last := &expected.repositories[len(expected.repositories)-1]
			switch name {
			case "size":
				expected.bytes--
			case "hash":
				expected.hash = strings.Repeat("0", 64)
			case "hash-short":
				expected.hash = expected.hash[:63]
			case "hash-uppercase":
				expected.hash = strings.ToUpper(expected.hash)
			case "hash-nonhex":
				expected.hash = strings.Repeat("z", 64)
			case "last-count":
				last.skillCount = 3
				last.skills["extra"] = archiveTestBetaHash
			case "last-cardinality":
				last.skillCount = 1
			case "last-missing":
				delete(last.skills, "skills/beta")
				last.skillCount = 1
			case "last-extra":
				last.skills["extra"] = archiveTestBetaHash
			case "last-path":
				delete(last.skills, "skills/beta")
				last.skills["skills/elsewhere"] = archiveTestBetaHash
			case "last-hash":
				last.skills["skills/beta"] = strings.Repeat("a", 64)
			case "last-hash-uppercase":
				last.skills["skills/beta"] = strings.ToUpper(archiveTestBetaHash)
			case "empty-repositories":
				expected.repositories = nil
			case "nil-skills":
				last.skills = nil
			case "zero-count":
				last.skillCount = 0
			case "negative-count":
				last.skillCount = -1
			case "zero-bytes":
				expected.bytes = 0
			case "negative-bytes":
				expected.bytes = -1
			}
			archiveTestAssert(test, test.Context(), data, expected, archiveTestLimits(), errArchiveInvalid)
		})
	}
}

func TestVerifyArchiveBinaryAndDocumentBinding(test *testing.T) {
	for _, index := range []int{0, 1} {
		test.Run(fmt.Sprint(index), func(test *testing.T) {
			entries := archiveTestEntries()
			entries[index].data = append(bytes.Clone(entries[index].data), 'x')
			data := archiveTestTar(test, entries)
			archiveTestAssert(test, test.Context(), data, archiveTestExpected(data), archiveTestLimits(), errArchiveInvalid)
		})
	}
}

func TestVerifyArchiveLimits(test *testing.T) {
	for _, name := range []string{"exact", "byte-budget", "reference-budget", "zero-byte-limit", "large-byte-limit", "zero-reference-limit", "large-reference-limit", "expected-oversize", "data-oversize", "empty-data"} {
		test.Run(name, func(test *testing.T) {
			data := archiveTestTar(test, archiveTestEntries())
			expected := archiveTestExpected(data)
			limits := archiveLimits{bytes: int64(len(data)), references: 9}
			var want error
			switch name {
			case "byte-budget":
				limits.bytes--
				want = errArchiveBudget
			case "reference-budget":
				limits.references--
				want = errArchiveBudget
			case "zero-byte-limit":
				limits.bytes = 0
				want = errArchiveInvalid
			case "large-byte-limit":
				limits.bytes = repository.MaxArchiveBytes + 1
				want = errArchiveInvalid
			case "zero-reference-limit":
				limits.references = 0
				want = errArchiveInvalid
			case "large-reference-limit":
				limits.references = 1000001
				want = errArchiveInvalid
			case "expected-oversize":
				limits = archiveTestLimits()
				expected.bytes = repository.MaxArchiveBytes + 1
				expected.hash = "bad hash must not precede budget"
				want = errArchiveBudget
			case "data-oversize":
				limits = archiveTestLimits()
				data = make([]byte, repository.MaxArchiveBytes+1)
				expected.hash = "bad hash must not precede budget"
				want = errArchiveBudget
			case "empty-data":
				data = nil
				want = errArchiveInvalid
			}
			archiveTestAssert(test, test.Context(), data, expected, limits, want)
		})
	}
}

func TestVerifyArchiveMillionReferences(test *testing.T) {
	data := archiveTestTar(test, archiveTestEntries()[2:])
	digest := sha256.Sum256(data)
	repositoryExpected := archiveRepositoryExpectation{skillCount: 1, skills: map[string]string{"skills/beta": archiveTestBetaHash}}
	expected := archiveExpectation{hash: hex.EncodeToString(digest[:]), bytes: int64(len(data)), repositories: make([]archiveRepositoryExpectation, 500001)}
	for index := range expected.repositories {
		expected.repositories[index] = repositoryExpected
	}
	limits := archiveTestLimits()
	before := bytes.Clone(data)
	atLimit := expected
	atLimit.repositories = expected.repositories[:500000]
	if err := verifyArchive(test.Context(), data, atLimit, limits); err != nil {
		test.Fatal("exact 1m repo+skill references refused", err)
	}
	if err := verifyArchive(test.Context(), data, expected, limits); err != errArchiveBudget {
		test.Fatal("excess combined references admitted or wrong error", err)
	}
	expected.repositories = make([]archiveRepositoryExpectation, 1000001)
	if err := verifyArchive(test.Context(), data, expected, limits); err != errArchiveBudget {
		test.Fatal("repository count not budgeted before rows", err)
	}
	if !bytes.Equal(before, data) {
		test.Fatal("borrowed archive changed at large reference boundary")
	}
	for _, row := range atLimit.repositories {
		if row.skillCount != 1 || len(row.skills) != 1 || row.skills["skills/beta"] != archiveTestBetaHash {
			test.Fatal("borrowed shared declarations changed")
		}
	}
}

func TestVerifyArchiveUnsafeInputs(test *testing.T) {
	for _, name := range []string{"bad-gzip", "truncated-gzip", "traversal", "symlink-skill", "symlink-resource", "oversize-document", "no-skills"} {
		test.Run(name, func(test *testing.T) {
			entries := archiveTestEntries()
			want := errArchiveInvalid
			switch name {
			case "traversal":
				entries = append(entries, archiveTestEntry{name: "root/../archive-private-escape", data: []byte("archive-private-body")})
			case "symlink-skill":
				entries[0] = archiveTestEntry{name: entries[0].name, kind: tar.TypeSymlink}
			case "symlink-resource":
				entries[1] = archiveTestEntry{name: entries[1].name, kind: tar.TypeSymlink}
			case "oversize-document":
				entries[0].data = bytes.Repeat([]byte{'x'}, 1<<20+1)
				want = errArchiveBudget
			case "no-skills":
				entries = []archiveTestEntry{{name: "root/README.md", data: []byte("archive-private-body")}}
			}
			data := archiveTestTar(test, entries)
			if name == "bad-gzip" {
				data = []byte("archive-private-not-gzip")
			} else if name == "truncated-gzip" {
				data = data[:len(data)-4]
			}
			archiveTestAssert(test, test.Context(), data, archiveTestExpected(data), archiveTestLimits(), want)
		})
	}
}

type archiveTestCancelContext struct {
	context.Context
	cancel context.CancelFunc
	calls  int
	at     int
}

func (ctx *archiveTestCancelContext) Err() error {
	ctx.calls++
	if ctx.calls == ctx.at {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestVerifyArchiveCancellation(test *testing.T) {
	data := archiveTestTar(test, archiveTestEntries())
	expected := archiveTestExpected(data)
	archiveTestAssert(test, nil, data, expected, archiveTestLimits(), errArchiveInvalid)
	ctx, cancel := context.WithCancelCause(test.Context())
	cancel(errors.New("archive-private-cancellation-cause"))
	expected.hash = "bad hash must not precede cancellation"
	archiveTestAssert(test, ctx, data, expected, archiveTestLimits(), context.Canceled)
	expected = archiveTestExpected(data)
	deadline, expire := context.WithDeadline(test.Context(), time.Unix(1, 0))
	defer expire()
	archiveTestAssert(test, deadline, data, expected, archiveTestLimits(), context.DeadlineExceeded)
	observed := &archiveTestCancelContext{Context: test.Context()}
	archiveTestAssert(test, observed, data, expected, archiveTestLimits(), nil)
	for checkpoint := 1; checkpoint <= observed.calls; checkpoint++ {
		base, cancel := context.WithCancel(test.Context())
		staged := &archiveTestCancelContext{Context: base, cancel: cancel, at: checkpoint}
		archiveTestAssert(test, staged, data, expected, archiveTestLimits(), context.Canceled)
		cancel()
	}
	test.Logf("synchronous cancellation at %d observed Err checkpoints", observed.calls)
}

func TestVerifyArchiveErrorRedaction(test *testing.T) {
	for _, data := range [][]byte{[]byte("archive-private-not-gzip"), archiveTestTar(test, []archiveTestEntry{{name: "root/../archive-private-path", data: []byte("archive-private-body")}})} {
		err := verifyArchive(test.Context(), data, archiveTestExpected(data), archiveTestLimits())
		if err != errArchiveInvalid {
			test.Fatal("parser failure was not a bare category", err)
		}
		encoded, encodeError := json.Marshal(err.Error())
		if encodeError != nil {
			test.Fatal(encodeError)
		}
		var logging strings.Builder
		slog.New(slog.NewJSONHandler(&logging, nil)).Error("refusal", "error", err)
		output := fmt.Sprintf("%v %+v %#v %s %s", err, err, err, encoded, logging.String())
		for _, private := range []string{"archive-private", "gzip", "tar header", "invalid archive path", archiveTestAlphaHash} {
			if strings.Contains(output, private) {
				test.Fatal("parser/body/path leaked through error formatting")
			}
		}
	}
}
