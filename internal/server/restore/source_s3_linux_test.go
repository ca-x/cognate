package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/backup"
	"golang.org/x/sys/unix"
)

type sourceS3Fixture struct {
	options backup.S3Options
	attempt backup.Attempt
	limits  backup.SealLimits
	key     []byte
	parent  *os.File
	objects map[string][]byte
	payload map[string][]byte
	mutex   sync.Mutex
	calls   []string
	hook    func(http.ResponseWriter, *http.Request) bool
}

func sourceS3Parent(test *testing.T) *os.File {
	test.Helper()
	name := test.TempDir()
	if err := os.Chmod(name, 0700); err != nil {
		test.Fatal(err)
	}
	file, err := os.Open(name)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = file.Close() })
	return file
}

func sourceS3TestFixture(test *testing.T) *sourceS3Fixture {
	test.Helper()
	fixture := &sourceS3Fixture{key: bytes.Repeat([]byte{13}, 32), parent: sourceS3Parent(test), objects: make(map[string][]byte), payload: map[string][]byte{"database": bytes.Repeat([]byte("synthetic-database-\x00\xff-private-body"), 4000), "inventory": []byte("synthetic-inventory-private-body\n")}}
	fixture.limits = backup.SealLimits{TotalPlainBytes: 4 << 20, TotalCipherBytes: 4 << 20, StagingBytes: 8 << 20, InputStagingBytes: 256 << 10, MaxObjects: 8, MaxInventoryRecords: 8, OperationTimeout: 5 * time.Minute}
	key, err := backup.ImportRecoveryKey([16]byte{9}, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	defer key.Destroy()
	engine, err := backup.NewSealEngine(sourceS3Parent(test), key, fixture.limits)
	if err != nil {
		test.Fatal("producer engine", err)
	}
	defer engine.Close()
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer attempt.Close()
	defer bootstrap.Close()
	readArtifact := func(artifact backup.SealedFile) []byte {
		var data []byte
		if err := artifact.WithFile(test.Context(), func(file *os.File) error {
			var err error
			data, err = io.ReadAll(file)
			return err
		}); err != nil {
			test.Fatal(err)
		}
		return data
	}
	boot := readArtifact(bootstrap)
	var attemptID [16]byte
	copy(attemptID[:], boot[48:64])
	fixture.attempt, err = backup.NewAttempt([16]byte{1}, [16]byte{2}, attemptID)
	if err != nil {
		test.Fatal(err)
	}
	prefix := fmt.Sprintf("/synthetic/backups/%x/backups/%x/%x/", [16]byte{1}, [16]byte{2}, attemptID)
	fixture.objects[prefix+"bootstrap/00000000"] = boot
	var records []backup.SealExtentRecord
	for ordinal, kind := range []string{"database", "inventory"} {
		payload := fixture.payload[kind]
		digest := sha256.Sum256(payload)
		artifact, err := engine.SealExtentSource(test.Context(), attempt, uint32(ordinal), int64(len(payload)), digest, func(_ context.Context, output io.Writer) error { _, err := output.Write(payload); return err })
		if err != nil {
			test.Fatal("producer extent", err)
		}
		data := readArtifact(artifact)
		defer artifact.Close()
		cipherHash := sha256.Sum256(data)
		records = append(records, backup.SealExtentRecord{Kind: "extent", Ordinal: uint32(ordinal), FileKind: kind, FileID: kind, PlaintextBytes: int64(len(payload)), PlaintextSHA256: hex.EncodeToString(digest[:]), CiphertextBytes: int64(len(data)), CiphertextSHA256: hex.EncodeToString(cipherHash[:])})
		fixture.objects[prefix+fmt.Sprintf("data/%08x", ordinal)] = data
	}
	page, err := engine.SealPage(test.Context(), attempt, 1, 2, func(yield func(backup.SealExtentRecord, error) bool) {
		for _, record := range records {
			if !yield(record, nil) {
				return
			}
		}
	})
	if err != nil {
		test.Fatal("producer page", err)
	}
	fixture.objects[prefix+"page/00000001"] = readArtifact(page)
	defer page.Close()
	databaseHash, inventoryHash := sha256.Sum256(fixture.payload["database"]), sha256.Sum256(fixture.payload["inventory"])
	root, err := engine.SealRoot(test.Context(), attempt, backup.SealMetadata{AppVersion: "s3-source-test", SchemaSHA256: hex.EncodeToString(databaseHash[:]), CutID: "01000000000000000000000000000000", MasterKeyID: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Database: backup.SealFileDescription{Bytes: int64(len(fixture.payload["database"])), SHA256: hex.EncodeToString(databaseHash[:]), Extents: 1}, Inventory: backup.SealInventoryDescription{Bytes: int64(len(fixture.payload["inventory"])), SHA256: hex.EncodeToString(inventoryHash[:]), Extents: 1, Records: 1, Owners: 1}, ClosureSHA256: hex.EncodeToString(inventoryHash[:]), ExternalConfigNames: []string{"MASTER_KEY_FILE"}})
	if err != nil {
		test.Fatal("producer root", err)
	}
	fixture.objects[prefix+"page/00000000"] = readArtifact(root)
	defer root.Close()
	complete, err := engine.SealComplete(test.Context(), attempt)
	if err != nil {
		test.Fatal("producer complete", err)
	}
	fixture.objects[prefix+"complete/00000000"] = readArtifact(complete)
	defer complete.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(fixture.serve))
	test.Cleanup(server.Close)
	certificate := filepath.Join(test.TempDir(), "synthetic-ca.pem")
	if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		test.Fatal(err)
	}
	test.Setenv("SSL_CERT_FILE", certificate)
	test.Setenv("SSL_CERT_DIR", test.TempDir())
	credentials, err := backup.NewCredentials([]byte("synthetic-access"), []byte("synthetic-private-secret"), nil)
	if err != nil {
		test.Fatal(err)
	}
	fixture.options, err = backup.NewS3Options(server.URL, "local-test-1", "synthetic", "backups", [16]byte{1}, credentials, false)
	if err != nil {
		test.Fatal(err)
	}
	return fixture
}

func (fixture *sourceS3Fixture) serve(writer http.ResponseWriter, request *http.Request) {
	fixture.mutex.Lock()
	fixture.calls = append(fixture.calls, request.Method+" "+request.URL.Path+" "+request.Header.Get("Range"))
	hook := fixture.hook
	fixture.mutex.Unlock()
	if hook != nil && hook(writer, request) {
		return
	}
	data, found := fixture.objects[request.URL.Path]
	if !found {
		writer.WriteHeader(404)
		_, _ = io.WriteString(writer, "<Error><Code>NoSuchKey</Code><Message>source-private-error</Message></Error>")
		return
	}
	if request.Method != http.MethodHead && request.Method != http.MethodGet {
		writer.WriteHeader(405)
		return
	}
	writer.Header().Set("Last-Modified", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
	writer.Header().Set("ETag", `"opaque-not-authentication"`)
	start, end := 0, len(data)-1
	if header := request.Header.Get("Range"); header != "" {
		first, last, ok := strings.Cut(strings.TrimPrefix(header, "bytes="), "-")
		var firstError, lastError error
		start, firstError = strconv.Atoi(first)
		end, lastError = strconv.Atoi(last)
		if !ok || firstError != nil || lastError != nil || start < 0 || end < start || end >= len(data) {
			writer.WriteHeader(416)
			return
		}
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
	}
	writer.Header().Set("Content-Length", strconv.Itoa(end-start+1))
	if request.Header.Get("Range") != "" {
		writer.WriteHeader(206)
	}
	if request.Method == http.MethodGet {
		_, _ = writer.Write(data[start : end+1])
	}
}

func (fixture *sourceS3Fixture) source() CandidateSource {
	return S3CandidateSource(fixture.options, fixture.parent, fixture.attempt, [16]byte{9}, fixture.key, fixture.limits)
}

func TestS3CandidateSourceAuthenticatedTLS(test *testing.T) {
	fixture := sourceS3TestFixture(test)
	keyBefore := bytes.Clone(fixture.key)
	var retained backup.CryptoVerifiedBundle
	called := 0
	err := fixture.source()(test.Context(), func(ctx context.Context, bundle backup.CryptoVerifiedBundle) error {
		called++
		retained = bundle
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 2*time.Minute {
			test.Fatal("restore callback inherited head 120s deadline")
		}
		entries, err := os.ReadDir(fixture.parent.Name())
		if err != nil || len(entries) != 2 {
			test.Fatal("head scope not cleaned before consumer", err, len(entries))
		}
		if _, err := bundle.Metadata(ctx); err != nil {
			return err
		}
		for _, kind := range []string{"database", "inventory"} {
			var output bytes.Buffer
			count, err := bundle.CopyFile(ctx, kind, kind, &output)
			if err != nil || count != int64(len(fixture.payload[kind])) || !bytes.Equal(output.Bytes(), fixture.payload[kind]) {
				test.Fatal("authenticated payload mismatch", kind, err)
			}
		}
		return nil
	})
	if err != nil || called != 1 {
		test.Fatalf("real TLS source refused: error=%v callback=%d", err, called)
	}
	if _, err := retained.Metadata(test.Context()); !errors.Is(err, backup.ErrClosed) {
		test.Fatal("bundle escaped Source lifetime", err)
	}
	if !bytes.Equal(keyBefore, fixture.key) {
		test.Fatal("borrowed recovery bytes modified")
	}
	if _, err := fixture.parent.Stat(); err != nil {
		test.Fatal("borrowed parent closed", err)
	}
	entries, err := os.ReadDir(fixture.parent.Name())
	if err != nil || len(entries) != 0 {
		test.Fatal("Source cleanup left temporary objects", err)
	}
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	if len(fixture.calls) != 16 {
		test.Fatal("unexpected TLS protocol request count", len(fixture.calls))
	}
	for _, call := range fixture.calls {
		if !strings.HasPrefix(call, "GET ") && !strings.HasPrefix(call, "HEAD ") {
			test.Fatal("Source mutated or listed remote objects")
		}
	}
	test.Log("actual TLS head=4 root/page/data=12 authenticated callback=1 outer deadline retained=true Source cleanup=true")
}

func sourceS3AssertFailure(test *testing.T, fixture *sourceS3Fixture, source CandidateSource, ctx context.Context, want error) {
	test.Helper()
	called := false
	err := source(ctx, func(context.Context, backup.CryptoVerifiedBundle) error { called = true; return nil })
	if err == nil || called || want != nil && !errors.Is(err, want) {
		test.Fatal("unsafe source admitted or wrong category", err, called, want)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), "private") || strings.Contains(fmt.Sprint(err), fixture.parent.Name()) {
		test.Fatal("source error leaked parser/body/path/cause")
	}
	if _, err := fixture.parent.Stat(); err != nil {
		test.Fatal("failure closed borrowed parent", err)
	}
}

func TestS3CandidateSourceRejectsUntrustedObjects(test *testing.T) {
	for _, scenario := range []string{"bootstrap", "complete", "root", "page", "last-data", "missing-data", "short-data", "wrong-attempt", "wrong-key", "zero-key", "zero-id", "short-key", "input-budget", "copy-budget", "plain-budget", "object-budget"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := sourceS3TestFixture(test)
			suffix := map[string]string{"bootstrap": "bootstrap/00000000", "complete": "complete/00000000", "root": "page/00000000", "page": "page/00000001", "last-data": "data/00000001", "missing-data": "data/00000001", "short-data": "data/00000001"}[scenario]
			for name, data := range fixture.objects {
				if suffix == "" || !strings.HasSuffix(name, suffix) {
					continue
				}
				switch scenario {
				case "missing-data":
					delete(fixture.objects, name)
				case "short-data":
					fixture.objects[name] = data[:len(data)-1]
				default:
					data[len(data)-1] ^= 1
				}
			}
			identity := [16]byte{9}
			switch scenario {
			case "wrong-attempt":
				fixture.attempt, _ = backup.NewAttempt([16]byte{1}, [16]byte{2}, [16]byte{99})
			case "wrong-key":
				fixture.key = bytes.Repeat([]byte{14}, 32)
			case "zero-key":
				fixture.key = make([]byte, 32)
			case "zero-id":
				identity = [16]byte{}
			case "short-key":
				fixture.key = []byte{13}
			case "input-budget":
				fixture.limits.InputStagingBytes = 32
			case "copy-budget":
				fixture.limits.InputStagingBytes = 16384
			case "plain-budget":
				fixture.limits.TotalPlainBytes = 32
			case "object-budget":
				fixture.limits.MaxObjects = 1
			}
			sourceS3AssertFailure(test, fixture, S3CandidateSource(fixture.options, fixture.parent, fixture.attempt, identity, fixture.key, fixture.limits), test.Context(), nil)
		})
	}
}

type sourceS3TargetWrapper struct {
	sourceS3Target
	head  func(context.Context, *os.File, backup.Attempt, func(context.Context, *os.File, *os.File) error) error
	close func() error
	copy  func(context.Context, backup.ObjectRef, io.Writer) (backup.VerifyResult, error)
}

func (target sourceS3TargetWrapper) WithRestoreHead(ctx context.Context, parent *os.File, attempt backup.Attempt, consume func(context.Context, *os.File, *os.File) error) error {
	if target.head != nil {
		return target.head(ctx, parent, attempt, consume)
	}
	return target.sourceS3Target.WithRestoreHead(ctx, parent, attempt, consume)
}
func (target sourceS3TargetWrapper) Close() error {
	if target.close != nil {
		return target.close()
	}
	return target.sourceS3Target.Close()
}
func (target sourceS3TargetWrapper) CopyExact(ctx context.Context, ref backup.ObjectRef, output io.Writer) (backup.VerifyResult, error) {
	if target.copy != nil {
		return target.copy(ctx, ref, output)
	}
	return target.sourceS3Target.CopyExact(ctx, ref, output)
}

func TestS3CandidateSourceCompleteCleanupResult(test *testing.T) {
	for _, scenario := range []string{"bundle", "session", "engine", "key", "target", "input-close", "input-read-close", "parent-close", "unlink", "sync", "file-sync", "head-cleanup"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := sourceS3TestFixture(test)
			operations := defaultSourceS3Ops()
			var destroyed backup.RecoveryKey
			var closedEngine backup.SealEngine
			operations.destroy = func(key backup.RecoveryKey) error {
				destroyed = key
				err := key.Destroy()
				if scenario == "key" {
					return errors.Join(err, syscall.EIO)
				}
				return err
			}
			operations.closeEngine = func(engine backup.SealEngine) error {
				closedEngine = engine
				err := engine.Close()
				if scenario == "engine" {
					return errors.Join(err, syscall.EIO)
				}
				return err
			}
			if scenario == "bundle" {
				operations.closeBundle = func(bundle backup.CryptoVerifiedBundle) error { return errors.Join(bundle.Close(), syscall.EIO) }
			}
			if scenario == "session" {
				operations.closeSession = func(session backup.OpenSession) error { return errors.Join(session.Close(), syscall.EIO) }
			}
			operations.target = func(options backup.S3Options) (sourceS3Target, error) {
				target, err := backup.NewS3Target(options)
				return sourceS3TargetWrapper{sourceS3Target: target, close: func() error {
					err := target.Close()
					if scenario == "target" {
						return errors.Join(err, syscall.EIO)
					}
					return err
				}, head: func(ctx context.Context, parent *os.File, attempt backup.Attempt, consume func(context.Context, *os.File, *os.File) error) error {
					err := target.WithRestoreHead(ctx, parent, attempt, consume)
					if scenario == "head-cleanup" && err == nil {
						return backup.ErrUnavailable
					}
					return err
				}}, err
			}
			operations.files.close = func(file *os.File) error {
				info, _ := file.Stat()
				flags, _ := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
				err := file.Close()
				if info != nil && (scenario == "parent-close" && info.IsDir() || strings.HasPrefix(filepath.Base(file.Name()), "restore-s3-input-") && (scenario == "input-close" && flags&unix.O_ACCMODE == unix.O_RDWR || scenario == "input-read-close" && flags&unix.O_ACCMODE == unix.O_RDONLY)) {
					return errors.Join(err, syscall.EIO)
				}
				return err
			}
			if scenario == "unlink" {
				operations.files.unlink = func(*os.File, string) error { return syscall.EIO }
			}
			if scenario == "sync" || scenario == "file-sync" {
				operations.files.sync = func(file *os.File) error {
					info, _ := file.Stat()
					if info != nil && (scenario == "sync" && info.IsDir() || scenario == "file-sync" && !info.IsDir()) {
						return syscall.EIO
					}
					return file.Sync()
				}
			}
			called := false
			var retained backup.CryptoVerifiedBundle
			err := s3CandidateSource(fixture.options, fixture.parent, fixture.attempt, [16]byte{9}, fixture.key, fixture.limits, operations)(test.Context(), func(_ context.Context, bundle backup.CryptoVerifiedBundle) error {
				called = true
				retained = bundle
				return nil
			})
			want := ErrCandidateUncertain
			if scenario == "sync" || scenario == "file-sync" || scenario == "unlink" {
				want = ErrCandidateCleanup
			}
			if !errors.Is(err, want) || called != (scenario != "head-cleanup" && scenario != "input-read-close" && scenario != "file-sync") {
				test.Fatal("complete Source cleanup result lost", scenario, err, called)
			}
			fixture.mutex.Lock()
			requests := len(fixture.calls)
			fixture.mutex.Unlock()
			if scenario == "head-cleanup" && requests != 4 || (scenario == "input-read-close" || scenario == "file-sync") && requests != 7 {
				test.Fatal("Source continued downloading after scope failure", scenario, requests)
			}
			if called {
				if _, err := retained.Metadata(test.Context()); !errors.Is(err, backup.ErrClosed) {
					test.Fatal("late failure left bundle usable", err)
				}
			}
			if _, _, err := closedEngine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2}); !errors.Is(err, backup.ErrClosed) {
				test.Fatal("engine cleanup skipped", err)
			}
			if engine, err := backup.NewSealEngine(fixture.parent, destroyed, fixture.limits); !errors.Is(err, backup.ErrClosed) {
				_ = engine.Close()
				test.Fatal("recovery key not destroyed", err)
			}
			if !bytes.Equal(fixture.key, bytes.Repeat([]byte{13}, 32)) {
				test.Fatal("borrowed key zeroed instead of imported copy")
			}
		})
	}
}

func TestS3CandidateSourceCancellation(test *testing.T) {
	for _, scenario := range []string{"before", "head", "copy", "callback", "deadline"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := sourceS3TestFixture(test)
			ctx, cancel := context.WithCancelCause(test.Context())
			defer cancel(nil)
			if scenario == "before" {
				cancel(errors.New("source-private-cancel-cause"))
			}
			if scenario == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer stop()
			}
			fixture.hook = func(_ http.ResponseWriter, request *http.Request) bool {
				if scenario == "head" && strings.Contains(request.URL.Path, "/bootstrap/") || scenario == "copy" && strings.Contains(request.URL.Path, "/page/00000000") {
					cancel(errors.New("source-private-cancel-cause"))
				}
				return false
			}
			called := false
			err := fixture.source()(ctx, func(context.Context, backup.CryptoVerifiedBundle) error {
				called = true
				cancel(errors.New("source-private-cancel-cause"))
				return nil
			})
			want := context.Canceled
			if scenario == "deadline" {
				want = context.DeadlineExceeded
			}
			if err != want || called != (scenario == "callback") {
				test.Fatal("cancellation category or callback barrier lost", err, called)
			}
			entries, err := os.ReadDir(fixture.parent.Name())
			if err != nil || len(entries) != 0 {
				test.Fatal("cancellation did not drain owned files", err)
			}
		})
	}
}

func TestS3CandidateSourceSingleInputWindow(test *testing.T) {
	fixture := sourceS3TestFixture(test)
	fixture.limits.StagingBytes = fixture.limits.InputStagingBytes + int64(len(fixture.payload["database"])+len(fixture.payload["inventory"])) + 65536
	operations := defaultSourceS3Ops()
	inHead := false
	operations.target = func(options backup.S3Options) (sourceS3Target, error) {
		target, err := backup.NewS3Target(options)
		return sourceS3TargetWrapper{sourceS3Target: target, head: func(ctx context.Context, parent *os.File, attempt backup.Attempt, consume func(context.Context, *os.File, *os.File) error) error {
			inHead = true
			defer func() { inHead = false }()
			return target.WithRestoreHead(ctx, parent, attempt, consume)
		}, copy: func(ctx context.Context, ref backup.ObjectRef, output io.Writer) (backup.VerifyResult, error) {
			if inHead {
				test.Error("CopyExact occupied a restore-head slot")
			}
			return target.CopyExact(ctx, ref, output)
		}}, err
	}
	var identity os.FileInfo
	var checks int
	fixture.hook = func(_ http.ResponseWriter, request *http.Request) bool {
		fixture.mutex.Lock()
		defer fixture.mutex.Unlock()
		if request.Method != http.MethodGet || !strings.Contains(request.URL.Path, "/data/") {
			return false
		}
		entries, err := os.ReadDir(fixture.parent.Name())
		if err != nil {
			test.Error(err)
			return false
		}
		seen := 0
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "restore-s3-input-") {
				info, err := entry.Info()
				if err != nil || !candidatePrivate(info, false) || info.Size() > fixture.limits.InputStagingBytes {
					test.Error("unbounded or non-private ciphertext staging")
				}
				if identity != nil && !os.SameFile(identity, info) {
					test.Error("ciphertext staging did not reuse one inode")
				}
				identity = info
				seen++
			}
		}
		if seen != 1 {
			test.Error("not exactly one ciphertext input window", seen)
		}
		checks++
		return false
	}
	if err := s3CandidateSource(fixture.options, fixture.parent, fixture.attempt, [16]byte{9}, fixture.key, fixture.limits, operations)(test.Context(), func(context.Context, backup.CryptoVerifiedBundle) error { return nil }); err != nil || checks != 4 {
		test.Fatal("reserved input window double charged or not bounded", err, checks)
	}
}

type sourceS3HeadContext struct {
	context.Context
	cancel  context.CancelFunc
	checks  int
	trigger int
}

func (ctx *sourceS3HeadContext) Err() error {
	ctx.checks++
	if ctx.checks == ctx.trigger {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestS3CandidateSourceHeadContextBarrier(test *testing.T) {
	for _, trigger := range []int{1, 2} {
		test.Run(fmt.Sprint(trigger), func(test *testing.T) {
			fixture := sourceS3TestFixture(test)
			operations := defaultSourceS3Ops()
			var head *sourceS3HeadContext
			copies := 0
			operations.target = func(options backup.S3Options) (sourceS3Target, error) {
				target, err := backup.NewS3Target(options)
				return sourceS3TargetWrapper{sourceS3Target: target, head: func(ctx context.Context, parent *os.File, attempt backup.Attempt, consume func(context.Context, *os.File, *os.File) error) error {
					return target.WithRestoreHead(ctx, parent, attempt, func(headContext context.Context, bootstrap, complete *os.File) error {
						scoped, cancel := context.WithCancel(headContext)
						defer cancel()
						head = &sourceS3HeadContext{Context: scoped, cancel: cancel, trigger: trigger}
						return consume(head, bootstrap, complete)
					})
				}, copy: func(ctx context.Context, ref backup.ObjectRef, output io.Writer) (backup.VerifyResult, error) {
					copies++
					return target.CopyExact(ctx, ref, output)
				}}, err
			}
			sourceS3AssertFailure(test, fixture, s3CandidateSource(fixture.options, fixture.parent, fixture.attempt, [16]byte{9}, fixture.key, fixture.limits, operations), test.Context(), nil)
			if head == nil || head.checks != trigger || copies != 0 {
				test.Fatal("head pre/post checks missing or downloading inside failed head scope", head, copies)
			}
		})
	}
}

func TestS3CandidateSourceUnknownOverridesCancellation(test *testing.T) {
	fixture := sourceS3TestFixture(test)
	ctx, cancel := context.WithCancelCause(test.Context())
	defer cancel(nil)
	operations := defaultSourceS3Ops()
	operations.closeBundle = func(bundle backup.CryptoVerifiedBundle) error { return errors.Join(bundle.Close(), syscall.EIO) }
	err := s3CandidateSource(fixture.options, fixture.parent, fixture.attempt, [16]byte{9}, fixture.key, fixture.limits, operations)(ctx, func(context.Context, backup.CryptoVerifiedBundle) error {
		cancel(errors.New("source-private-cancel-cause"))
		return errors.New("source-private-consumer-error")
	})
	if err != ErrCandidateUncertain {
		test.Fatal("close uncertainty demoted by cancellation/consumer failure", err)
	}
}

func TestS3CandidateSourcePrivateInputIdentity(test *testing.T) {
	test.Run("readonly-borrow", func(test *testing.T) {
		input, err := newSourceS3Input(sourceS3Parent(test), defaultSourceS3Ops().files)
		if err != nil {
			test.Fatal(err)
		}
		err = input.accept(test.Context(), 32, func(output io.Writer) (backup.VerifyResult, error) {
			_, err := output.Write([]byte("untrusted-ciphertext"))
			return backup.Verified, err
		}, func(file *os.File) error {
			flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
			if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
				test.Fatal("AcceptFile did not receive read-only FD", flags, err)
			}
			if _, err := file.Write([]byte("forbidden")); err == nil {
				test.Fatal("read-only input writable")
			}
			return nil
		})
		if err := errors.Join(err, input.cleanup()); err != nil {
			test.Fatal(err)
		}
	})
	for _, scenario := range []string{"parent-mode", "parent-symlink", "file-mode", "file-hardlink", "file-replacement"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := sourceS3TestFixture(test)
			if scenario == "parent-mode" {
				if err := os.Chmod(fixture.parent.Name(), 0755); err != nil {
					test.Fatal(err)
				}
			}
			if scenario == "parent-symlink" {
				alias := filepath.Join(test.TempDir(), "alias")
				if err := os.Symlink(fixture.parent.Name(), alias); err != nil {
					test.Fatal(err)
				}
				parent, err := os.Open(alias)
				if err != nil {
					test.Fatal(err)
				}
				defer parent.Close()
				fixture.parent = parent
			}
			var replacement string
			var mutated bool
			fixture.hook = func(_ http.ResponseWriter, request *http.Request) bool {
				if mutated || !strings.Contains(request.URL.Path, "/page/00000000") {
					return false
				}
				entries, err := os.ReadDir(fixture.parent.Name())
				if err != nil {
					test.Error(err)
					return false
				}
				for _, entry := range entries {
					if !strings.HasPrefix(entry.Name(), "restore-s3-input-") {
						continue
					}
					name := filepath.Join(fixture.parent.Name(), entry.Name())
					switch scenario {
					case "file-mode":
						err = os.Chmod(name, 0644)
					case "file-hardlink":
						err = os.Link(name, name+"-alias")
					case "file-replacement":
						err = os.Rename(name, name+"-displaced")
						if err == nil {
							err = os.WriteFile(name, []byte("foreign-private-sentinel"), 0600)
							replacement = name
						}
					}
					if err != nil {
						test.Error(err)
					}
					mutated = true
				}
				return false
			}
			sourceS3AssertFailure(test, fixture, fixture.source(), test.Context(), nil)
			if replacement != "" {
				data, err := os.ReadFile(replacement)
				if err != nil || string(data) != "foreign-private-sentinel" {
					test.Fatal("cleanup deleted or modified replacement inode", err)
				}
			}
		})
	}
}

func TestS3CandidateSourceInvalidAdmission(test *testing.T) {
	fixture := sourceS3TestFixture(test)
	if err := fixture.source()(nil, func(context.Context, backup.CryptoVerifiedBundle) error { test.Fatal("nil ctx callback"); return nil }); err != ErrCandidateInvalid {
		test.Fatal("nil context admitted", err)
	}
	if err := fixture.source()(test.Context(), nil); err != ErrCandidateInvalid {
		test.Fatal("nil consumer admitted", err)
	}
	for _, scenario := range []string{"nil-parent", "zero-options", "zero-attempt", "invalid-limits"} {
		test.Run(scenario, func(test *testing.T) {
			parent, options, expected, limits := fixture.parent, fixture.options, fixture.attempt, fixture.limits
			switch scenario {
			case "nil-parent":
				parent = nil
			case "zero-options":
				options = backup.S3Options{}
			case "zero-attempt":
				expected = backup.Attempt{}
			case "invalid-limits":
				limits.MaxObjects = 0
			}
			sourceS3AssertFailure(test, fixture, S3CandidateSource(options, parent, expected, [16]byte{9}, fixture.key, limits), test.Context(), nil)
		})
	}
}
