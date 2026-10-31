package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sourceAttempt(test *testing.T) (SealEngine, SealAttempt, SealedFile) {
	test.Helper()
	engine, _, _ := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = bootstrap.Close() })
	return engine, attempt, bootstrap
}

func sourceStagePrivate(test *testing.T, engine SealEngine, sentinels ...[]byte) {
	test.Helper()
	root := engine.get().directory.root.Name()
	entries, err := os.ReadDir(root)
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			test.Fatal(err)
		}
		for _, sentinel := range sentinels {
			if bytes.Contains(content, sentinel) {
				test.Fatal("private stage contains a plaintext or recovery-key sentinel")
			}
		}
	}
}

func TestSealExtentSourceRoundTrip(test *testing.T) {
	engine, attempt, bootstrap := sourceAttempt(test)
	database := bytes.Repeat([]byte("SQLite-snapshot-master-key-secret-sentinel:"), 26000)
	inventory := []byte("inventory-source-secret-sentinel")
	archive := []byte("archive-source-secret-sentinel")
	archiveHash := sha256.Sum256(archive)
	archiveID := hex.EncodeToString(archiveHash[:])
	databaseHash, inventoryHash := sha256.Sum256(database), sha256.Sum256(inventory)
	var artifacts []SealedFile
	var records []SealExtentRecord
	for ordinal, payload := range [][]byte{database, inventory, archive} {
		digest := sha256.Sum256(payload)
		var escaped io.Writer
		artifact, err := engine.SealExtentSource(test.Context(), attempt, uint32(ordinal), int64(len(payload)), digest, func(ctx context.Context, output io.Writer) error {
			escaped = output
			for offset := 0; offset < len(payload); offset += 32768 {
				if err := ctx.Err(); err != nil {
					return err
				}
				if _, err := output.Write(payload[offset:min(offset+32768, len(payload))]); err != nil {
					return err
				}
			}
			sourceStagePrivate(test, engine, database[:42], inventory, archive, bytes.Repeat([]byte{13}, 32))
			return nil
		})
		if err != nil {
			test.Fatal("stream source refused", err)
		}
		test.Cleanup(func() { _ = artifact.Close() })
		if count, err := escaped.Write([]byte("late-secret-write")); count != 0 || !errors.Is(err, ErrClosed) {
			test.Fatal("callback writer escaped its lifetime", count, err)
		}
		if err = artifact.WithFile(test.Context(), func(input *os.File) error {
			encoded, err := io.ReadAll(input)
			if err != nil {
				return err
			}
			header, err := parseSealHeader(encoded[:sealHeaderSize])
			if err != nil {
				return err
			}
			var legacy bytes.Buffer
			if _, err = sealEncrypt(test.Context(), header, attempt.get().dek[:], bytes.NewReader(payload), &legacy); err != nil {
				return err
			}
			if !bytes.Equal(encoded, legacy.Bytes()) {
				return errors.New("writer source differs from existing EncryptReader format")
			}
			return nil
		}); err != nil {
			test.Fatal(err)
		}
		kind := []string{"database", "inventory", "archive"}[ordinal]
		id := kind
		if kind == "archive" {
			id = archiveID
		}
		ref := artifact.Ref().read()
		records = append(records, SealExtentRecord{Kind: "extent", Ordinal: uint32(ordinal), FileKind: kind, FileID: id, PlaintextBytes: int64(len(payload)), PlaintextSHA256: hex.EncodeToString(digest[:]), CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])})
		artifacts = append(artifacts, artifact)
	}
	page, err := engine.SealPage(test.Context(), attempt, 1, uint32(len(records)), func(yield func(SealExtentRecord, error) bool) {
		for _, record := range records {
			if !yield(record, nil) {
				return
			}
		}
	})
	if err != nil {
		test.Fatal(err)
	}
	defer page.Close()
	if artifact, err := engine.SealExtentSource(test.Context(), attempt, 3, 1, sha256.Sum256([]byte{1}), func(context.Context, io.Writer) error {
		test.Error("source producer entered after the manifest phase")
		return nil
	}); err == nil || artifact.get != nil {
		test.Fatal("source extent admitted after the manifest phase")
	}
	databaseHex, inventoryHex := hex.EncodeToString(databaseHash[:]), hex.EncodeToString(inventoryHash[:])
	root, err := engine.SealRoot(test.Context(), attempt, SealMetadata{AppVersion: "source-test", SchemaSHA256: databaseHex, CutID: "01000000000000000000000000000000", MasterKeyID: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Database: SealFileDescription{Bytes: int64(len(database)), SHA256: databaseHex, Extents: 1}, Inventory: SealInventoryDescription{Bytes: int64(len(inventory)), SHA256: inventoryHex, Extents: 1, Records: 1, Owners: 1}, Archives: 1, ClosureSHA256: inventoryHex, ExternalConfigNames: []string{"MASTER_KEY_FILE"}})
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	complete, err := engine.SealComplete(test.Context(), attempt)
	if err != nil {
		test.Fatal(err)
	}
	defer complete.Close()
	sourceStagePrivate(test, engine, database[:42], inventory, archive, bytes.Repeat([]byte{13}, 32))
	var session OpenSession
	err = bootstrap.WithFile(test.Context(), func(bootstrapFile *os.File) error {
		return complete.WithFile(test.Context(), func(completeFile *os.File) error {
			var openErr error
			session, openErr = engine.BeginOpen(test.Context(), bootstrapFile, completeFile, Attempt{read: func() attemptData { return attempt.get().identity }})
			return openErr
		})
	})
	if err != nil {
		test.Fatal(err)
	}
	defer session.Close()
	for _, artifact := range append([]SealedFile{root, page}, artifacts...) {
		next, err := session.NextRef(test.Context())
		if err != nil || next.read == nil || next.read() != artifact.Ref().read() {
			test.Fatal("legacy open session returned a different object", err)
		}
		if err = artifact.WithFile(test.Context(), func(input *os.File) error { return session.AcceptFile(test.Context(), input) }); err != nil {
			test.Fatal("legacy open session refused source ciphertext", err)
		}
	}
	bundle, err := session.Finish(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer bundle.Close()
	for kind, payload := range map[string][]byte{"database": database, "inventory": inventory, "archive": archive} {
		var output bytes.Buffer
		id := kind
		if kind == "archive" {
			id = archiveID
		}
		count, err := bundle.CopyFile(test.Context(), kind, id, &output)
		if err != nil || count != int64(len(payload)) || !bytes.Equal(output.Bytes(), payload) {
			test.Fatal("source plaintext did not survive the existing authenticated open path", err)
		}
	}
}

func TestSealExtentSourceFailures(test *testing.T) {
	payload := []byte("source-failure-master-key-secret-sentinel")
	for _, mode := range []string{"short", "overrun", "late-overrun", "hash", "producer", "producer-after-write", "cancel", "ignored-cancel"} {
		test.Run(mode, func(test *testing.T) {
			engine, attempt, _ := sourceAttempt(test)
			state, data := engine.get(), attempt.get()
			plain, cipher, disk, objects := state.plain, state.cipher, state.disk, state.objects
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			var escaped io.Writer
			artifact, err := engine.SealExtentSource(ctx, attempt, 0, int64(len(payload)), sha256.Sum256(payload), func(ctx context.Context, output io.Writer) error {
				escaped = output
				switch mode {
				case "short":
					_, _ = output.Write(payload[:len(payload)-1])
				case "overrun":
					if count, err := output.Write(append(bytes.Clone(payload), 1)); count != 0 || err == nil {
						test.Error("oversized write was not refused atomically")
					}
					_, _ = output.Write(payload)
				case "late-overrun":
					_, _ = output.Write(payload)
					_, _ = output.Write([]byte{1})
				case "hash":
					_, _ = output.Write(bytes.Repeat([]byte{1}, len(payload)))
				case "producer":
					return fmt.Errorf("%s: %w", payload, ErrInvalid)
				case "producer-after-write":
					_, _ = output.Write(payload)
					return errors.New(string(payload))
				case "cancel":
					_, _ = output.Write(payload)
					cancel()
				case "ignored-cancel":
					cancel()
					if count, err := output.Write(payload); count != 0 || !errors.Is(err, context.Canceled) {
						test.Error("canceled producer could continue writing")
					}
				}
				return nil
			})
			if artifact.get != nil || err == nil {
				test.Fatal("failed source produced an artifact")
			}
			if strings.Contains(mode, "cancel") {
				if !errors.Is(err, context.Canceled) {
					test.Fatal("cancellation was lost", err)
				}
			} else if !errors.Is(err, ErrInvalid) {
				test.Fatal("producer error was not sanitized", err)
			}
			if errors.Unwrap(err) != nil || strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), string(payload)) {
				test.Fatal("source error exposed the producer's data")
			}
			if count, err := escaped.Write(payload); count != 0 || !errors.Is(err, ErrClosed) {
				test.Fatal("failed source writer escaped", count, err)
			}
			if !data.poison || data.entries != 0 || data.dataCount != 0 || data.locations[0][0] != 0 {
				test.Fatal("failed source was recorded or attempt was not poisoned")
			}
			info, statErr := data.catalog.Stat()
			if statErr != nil || info.Size() != 0 {
				test.Fatal("failed source wrote a catalog record", statErr)
			}
			if state.plain != plain || state.cipher != cipher || state.disk != disk || state.objects != objects {
				test.Fatal("failed source did not release its budget")
			}
			entries, readErr := os.ReadDir(state.directory.root.Name())
			if readErr != nil || len(entries) != 2 {
				test.Fatal("failed source left a private artifact", readErr)
			}
			for _, ordinal := range []uint32{0, 1} {
				if file, err := engine.SealExtentSource(test.Context(), attempt, ordinal, int64(len(payload)), sha256.Sum256(payload), func(context.Context, io.Writer) error {
					test.Error("poisoned attempt invoked a producer")
					return nil
				}); err == nil || file.get != nil {
					test.Fatal("failed or poisoned ordinal reused")
				}
			}
			sourceStagePrivate(test, engine, payload, bytes.Repeat([]byte{13}, 32))
		})
	}
}

func TestSealExtentSourceLengthAndQuota(test *testing.T) {
	for _, length := range []int64{-1, 0, sealExtentLimit + 1, 9} {
		test.Run(fmt.Sprint(length), func(test *testing.T) {
			engine, attempt, _ := sourceAttempt(test)
			engine.get().limits.InputStagingBytes = 8
			producer := func(context.Context, io.Writer) error {
				test.Error("invalid length invoked a producer")
				return nil
			}
			if file, err := engine.SealExtentSource(test.Context(), attempt, 0, length, [32]byte{}, producer); !errors.Is(err, ErrSealBudget) || file.get != nil {
				test.Fatal("unbounded input admitted", err)
			}
			payload := []byte("bounded")
			valid := func(_ context.Context, output io.Writer) error { _, err := output.Write(payload); return err }
			if file, err := engine.SealExtentSource(test.Context(), attempt, 0, int64(len(payload)), sha256.Sum256(payload), valid); err == nil || file.get != nil {
				test.Fatal("pre-input ordinal reused")
			}
			file, err := engine.SealExtentSource(test.Context(), attempt, 1, int64(len(payload)), sha256.Sum256(payload), valid)
			if err != nil {
				test.Fatal("invalid pre-input length poisoned an unburned slot", err)
			}
			defer file.Close()
		})
	}
	for _, quota := range []string{"plain", "cipher", "disk", "objects"} {
		test.Run(quota, func(test *testing.T) {
			engine, attempt, _ := sourceAttempt(test)
			state := engine.get()
			cipherLength, _ := sealCipherSize(1)
			switch quota {
			case "plain":
				state.limits.TotalPlainBytes = state.plain
			case "cipher":
				state.limits.TotalCipherBytes = state.cipher + cipherLength - 1
			case "disk":
				state.limits.StagingBytes = state.disk + cipherLength - 1
			case "objects":
				charge, err := state.reserve(0, 0, 0, state.limits.MaxObjects)
				if err != nil {
					test.Fatal(err)
				}
				defer charge.release()
			}
			plain, cipher, disk, objects := state.plain, state.cipher, state.disk, state.objects
			if file, err := engine.SealExtentSource(test.Context(), attempt, 0, 1, sha256.Sum256([]byte{1}), func(context.Context, io.Writer) error {
				test.Error("exhausted budget invoked a producer")
				return nil
			}); !errors.Is(err, ErrSealBudget) || file.get != nil {
				test.Fatal("quota exhaustion admitted", err)
			}
			if !attempt.get().poison || attempt.get().entries != 0 || state.plain != plain || state.cipher != cipher || state.disk != disk || state.objects != objects {
				test.Fatal("quota refusal changed budget or did not poison the attempt")
			}
		})
	}
}

func TestSealExtentSourceBoundaries(test *testing.T) {
	for _, length := range []int64{1, sealFrame - sealTrailerSize, sealFrame - sealTrailerSize + 1, sealFrame - 1, sealFrame, sealFrame + 1, sealExtentLimit} {
		test.Run(fmt.Sprint(length), func(test *testing.T) {
			engine, attempt, _ := sourceAttempt(test)
			engine.get().limits.InputStagingBytes = length
			chunk := bytes.Repeat([]byte{42}, 32768)
			digest := sha256.New()
			for remaining := length; remaining > 0; remaining -= min(remaining, int64(len(chunk))) {
				_, _ = digest.Write(chunk[:min(remaining, int64(len(chunk)))])
			}
			var expected [32]byte
			copy(expected[:], digest.Sum(nil))
			artifact, err := engine.SealExtentSource(test.Context(), attempt, 0, length, expected, func(_ context.Context, output io.Writer) error {
				for remaining := length; remaining > 0; remaining -= min(remaining, int64(len(chunk))) {
					if _, err := output.Write(chunk[:min(remaining, int64(len(chunk)))]); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				test.Fatal(err)
			}
			defer artifact.Close()
			if err = artifact.WithFile(test.Context(), func(input *os.File) error {
				var encoded [sealHeaderSize]byte
				if _, err := io.ReadFull(input, encoded[:]); err != nil {
					return err
				}
				header, err := parseSealHeader(encoded[:])
				if err != nil {
					return err
				}
				actual, err := sealDecrypt(test.Context(), header, attempt.get().dek[:], input, io.Discard)
				if err != nil || actual != expected || header.length != length {
					return ErrInvalid
				}
				return nil
			}); err != nil {
				test.Fatal("boundary ciphertext did not authenticate", err)
			}
		})
	}
}

func TestSealExtentSourceWriterRedaction(test *testing.T) {
	engine, attempt, _ := sourceAttempt(test)
	payload := []byte("writer-formatter-secret-sentinel")
	var escaped io.Writer
	artifact, err := engine.SealExtentSource(test.Context(), attempt, 0, int64(len(payload)), sha256.Sum256(payload), func(_ context.Context, output io.Writer) error {
		escaped = output
		_, err := output.Write(payload)
		var logs bytes.Buffer
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("writer", "source", output)
		encoded, jsonErr := json.Marshal(output)
		formatted := fmt.Sprintf("%v %+v %#v %s", output, output, output, output)
		if jsonErr != nil || string(encoded) != "{}" || !strings.Contains(formatted, "[private backup source writer]") || !strings.Contains(logs.String(), "[private backup source writer]") || strings.Contains(formatted+logs.String(), string(payload)) {
			test.Error("writer's default representations expose its state")
		}
		if _, ok := output.(io.Closer); ok {
			test.Error("producer can close the encryption stream")
		}
		return err
	})
	if err != nil {
		test.Fatal(err)
	}
	defer artifact.Close()
	var joined sync.WaitGroup
	for range 8 {
		joined.Go(func() {
			if count, err := escaped.Write(payload); count != 0 || !errors.Is(err, ErrClosed) {
				test.Error("asynchronously escaped writer remained usable")
			}
		})
	}
	joined.Wait()
}

func TestSealExtentSourceCloseDrain(test *testing.T) {
	for _, scope := range []string{"attempt", "engine", "key"} {
		test.Run(scope, func(test *testing.T) {
			engine, key, _ := sealFixture(test)
			attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
			if err != nil {
				test.Fatal(err)
			}
			defer bootstrap.Close()
			payload := []byte("draining-source-secret-sentinel")
			entered, canceled := make(chan struct{}, 2), make(chan struct{}, 2)
			release := make(chan struct{})
			var unblock sync.Once
			defer unblock.Do(func() { close(release) })
			results := make(chan error, 2)
			for ordinal := range uint32(2) {
				go func() {
					file, err := engine.SealExtentSource(test.Context(), attempt, ordinal, int64(len(payload)), sha256.Sum256(payload), func(ctx context.Context, output io.Writer) error {
						if _, err := output.Write(payload); err != nil {
							return err
						}
						entered <- struct{}{}
						<-ctx.Done()
						if count, err := output.Write(payload); count != 0 || !errors.Is(err, context.Canceled) {
							test.Error("closing scope did not revoke producer writes")
						}
						canceled <- struct{}{}
						<-release
						return nil
					})
					if file.get != nil {
						_ = file.Close()
						test.Error("closing scope exposed an artifact")
					}
					results <- err
				}()
			}
			for range 2 {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					test.Fatal("two synchronous producers did not enter")
				}
			}
			if file, err := engine.SealExtentSource(test.Context(), attempt, 2, 1, [32]byte{}, func(context.Context, io.Writer) error {
				test.Error("third producer entered an occupied engine")
				return nil
			}); !errors.Is(err, ErrBusy) || file.get != nil {
				test.Fatal("existing two-operation limit was bypassed", err)
			}
			closed := make(chan error, 1)
			go func() {
				switch scope {
				case "attempt":
					closed <- attempt.Close()
				case "engine":
					closed <- engine.Close()
				case "key":
					closed <- key.Destroy()
				}
			}()
			for range 2 {
				select {
				case <-canceled:
				case <-time.After(5 * time.Second):
					test.Fatal("close did not cancel both producer contexts")
				}
			}
			select {
			case <-closed:
				test.Fatal("close returned before its producers drained")
			default:
			}
			unblock.Do(func() { close(release) })
			for range 2 {
				select {
				case err := <-results:
					if !errors.Is(err, context.Canceled) {
						test.Fatal("closed producer succeeded or lost cancellation", err)
					}
				case <-time.After(5 * time.Second):
					test.Fatal("producer did not drain")
				}
			}
			select {
			case err := <-closed:
				if err != nil {
					test.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				test.Fatal("close did not join producers")
			}
			if attempt.get().entries != 0 || attempt.get().dataCount != 0 || attempt.get().active != 0 || engine.get().active != 0 {
				test.Fatal("close left active producers or admitted records")
			}
		})
	}
}

type sourceFlushSink struct {
	calls  int
	mode   string
	cancel context.CancelFunc
}

func (sink *sourceFlushSink) Write(payload []byte) (int, error) {
	sink.calls++
	if sink.calls == 2 {
		switch sink.mode {
		case "error":
			return 0, errors.New("flush-secret-sentinel")
		case "short":
			return len(payload) - 1, nil
		case "cancel":
			sink.cancel()
		}
	}
	return len(payload), nil
}

func TestSealExtentSourceFinalFrameFailure(test *testing.T) {
	for _, mode := range []string{"error", "short", "cancel"} {
		test.Run(mode, func(test *testing.T) {
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			sink := &sourceFlushSink{mode: mode, cancel: cancel}
			payload := []byte("buffered-secret-sentinel")
			header := sealHeader{purpose: 1, attempt: attemptData{instance: [16]byte{1}, backup: [16]byte{2}, attempt: [16]byte{3}}, length: int64(len(payload))}
			var escaped io.Writer
			err := sealEncryptSource(ctx, header, bytes.Repeat([]byte{13}, 32), sha256.Sum256(payload), func(_ context.Context, output io.Writer) error {
				escaped = output
				_, err := output.Write(payload)
				return err
			}, sink)
			if sink.calls != 2 || err == nil || strings.Contains(err.Error(), "secret-sentinel") {
				test.Fatal("final buffered frame failure was ignored or leaked data", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) || mode != "cancel" && !errors.Is(err, ErrInvalid) {
				test.Fatal("final frame returned an unsafe error", err)
			}
			if count, err := escaped.Write(payload); count != 0 || !errors.Is(err, ErrClosed) {
				test.Fatal("writer remained usable during finalization")
			}
		})
	}
}

func TestSealExtentSourcePhysicalFailures(test *testing.T) {
	for _, mode := range []string{"sync", "directory-sync", "close-write", "close-read", "late-cancel", "cleanup"} {
		test.Run(mode, func(test *testing.T) {
			engine, attempt, _ := sourceAttempt(test)
			state := engine.get()
			plain, cipher, disk, objects := state.plain, state.cipher, state.disk, state.objects
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			originalClose, originalSync := state.ops.close, state.ops.sync
			defer func() { state.ops.close, state.ops.sync = originalClose, originalSync }()
			closes, directorySyncs := 0, 0
			state.ops.close = func(file *os.File) error {
				err := originalClose(file)
				if strings.HasSuffix(file.Name(), "-1-00000000") {
					closes++
					if mode == "close-write" && closes == 1 || mode == "close-read" && closes == 2 {
						return errors.New("close-secret-sentinel")
					}
					if mode == "late-cancel" && closes == 2 {
						cancel()
					}
				}
				return err
			}
			state.ops.sync = func(file *os.File) error {
				if mode == "sync" && strings.HasSuffix(file.Name(), "-1-00000000") {
					return errors.New("sync-secret-sentinel")
				}
				if file == state.directory.root {
					directorySyncs++
					if mode == "cleanup" || mode == "directory-sync" && directorySyncs == 1 {
						return errors.New("directory-secret-sentinel")
					}
				}
				return originalSync(file)
			}
			payload := []byte("physical-source-secret-sentinel")
			file, err := engine.SealExtentSource(ctx, attempt, 0, int64(len(payload)), sha256.Sum256(payload), func(_ context.Context, output io.Writer) error {
				_, err := output.Write(payload)
				if mode == "cleanup" {
					return errors.New("producer-secret-sentinel")
				}
				return err
			})
			expected := ErrInvalid
			if strings.HasPrefix(mode, "close-") {
				expected = ErrSealUncertain
			} else if mode == "cleanup" {
				expected = ErrSealCleanup
			} else if mode == "late-cancel" {
				expected = context.Canceled
			}
			if file.get != nil || !errors.Is(err, expected) || strings.Contains(fmt.Sprint(err), "secret-sentinel") {
				test.Fatal("physical source failure was not preserved safely", err)
			}
			if attempt.get().entries != 0 || attempt.get().dataCount != 0 || !attempt.get().poison {
				test.Fatal("physical source failure admitted a record or did not poison")
			}
			if expected == ErrSealUncertain || expected == ErrSealCleanup {
				if !state.uncertain || state.disk <= disk || state.cipher <= cipher || state.plain <= plain || state.objects <= objects {
					test.Fatal("uncertain cleanup released its live charge")
				}
			} else if state.plain != plain || state.cipher != cipher || state.disk != disk || state.objects != objects {
				test.Fatal("certain cleanup did not release its charge")
			}
		})
	}
}

func TestSealExtentSourceOrdinalSharedWithFile(test *testing.T) {
	for _, streamFirst := range []bool{true, false} {
		test.Run(fmt.Sprint(streamFirst), func(test *testing.T) {
			engine, _, source := sealFixture(test)
			attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
			if err != nil {
				test.Fatal(err)
			}
			defer bootstrap.Close()
			payload := []byte("synthetic bounded plaintext")
			producer := func(_ context.Context, output io.Writer) error { _, err := output.Write(payload); return err }
			var file SealedFile
			if streamFirst {
				file, err = engine.SealExtentSource(test.Context(), attempt, 0, int64(len(payload)), sha256.Sum256(payload), producer)
			} else {
				file, err = engine.SealExtent(test.Context(), attempt, 0, source)
			}
			if err != nil {
				test.Fatal(err)
			}
			defer file.Close()
			if streamFirst {
				if repeated, err := engine.SealExtent(test.Context(), attempt, 0, source); err == nil || repeated.get != nil {
					test.Fatal("file API reused the stream ordinal")
				}
			} else if repeated, err := engine.SealExtentSource(test.Context(), attempt, 0, int64(len(payload)), sha256.Sum256(payload), func(context.Context, io.Writer) error {
				test.Error("file ordinal reuse invoked source producer")
				return nil
			}); err == nil || repeated.get != nil {
				test.Fatal("stream API reused the file ordinal")
			}
			if attempt.get().dataCount != 1 || attempt.get().entries != 1 {
				test.Fatal("ordinal reuse admitted an additional record")
			}
		})
	}
}

func TestSealExtentSourceInvalidCalls(test *testing.T) {
	engine, attempt, _ := sourceAttempt(test)
	otherEngine, otherAttempt, _ := sourceAttempt(test)
	producer := func(context.Context, io.Writer) error {
		test.Error("invalid call invoked its producer")
		return nil
	}
	for _, call := range []struct {
		name    string
		engine  SealEngine
		attempt SealAttempt
		ctx     context.Context
		ordinal uint32
		produce func(context.Context, io.Writer) error
	}{
		{name: "zero-engine", attempt: attempt, ctx: test.Context(), produce: producer},
		{name: "zero-attempt", engine: engine, ctx: test.Context(), produce: producer},
		{name: "foreign-attempt", engine: otherEngine, attempt: attempt, ctx: test.Context(), produce: producer},
		{name: "nil-context", engine: engine, attempt: attempt, produce: producer},
		{name: "nil-producer", engine: engine, attempt: attempt, ctx: test.Context()},
		{name: "ordinal-limit", engine: engine, attempt: attempt, ctx: test.Context(), ordinal: engine.get().limits.MaxObjects, produce: producer},
	} {
		test.Run(call.name, func(test *testing.T) {
			if file, err := call.engine.SealExtentSource(call.ctx, call.attempt, call.ordinal, 1, [32]byte{}, call.produce); !errors.Is(err, ErrInvalid) || file.get != nil {
				test.Fatal("invalid source call admitted", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if file, err := engine.SealExtentSource(ctx, attempt, 0, 1, [32]byte{}, producer); !errors.Is(err, context.Canceled) || file.get != nil {
		test.Fatal("already-canceled source call admitted", err)
	}
	if err := otherAttempt.Close(); err != nil {
		test.Fatal(err)
	}
	if file, err := otherEngine.SealExtentSource(test.Context(), otherAttempt, 0, 1, [32]byte{}, producer); !errors.Is(err, ErrInvalid) || file.get != nil {
		test.Fatal("closed attempt admitted a source", err)
	}
	payload := []byte{1}
	file, err := engine.SealExtentSource(test.Context(), attempt, 0, 1, sha256.Sum256(payload), func(_ context.Context, output io.Writer) error {
		_, err := output.Write(payload)
		return err
	})
	if err != nil {
		test.Fatal("invalid API arguments or pre-cancellation burned an unused ordinal", err)
	}
	if err = file.Close(); err != nil {
		test.Fatal(err)
	}
	if file, err := engine.SealExtentSource(test.Context(), attempt, 0, 1, sha256.Sum256(payload), producer); err == nil || file.get != nil {
		test.Fatal("artifact cleanup allowed ordinal reuse")
	}
}

func TestSealExtentSourceOperationTimeout(test *testing.T) {
	engine, attempt, _ := sourceAttempt(test)
	engine.get().limits.OperationTimeout = 25 * time.Millisecond
	payload := []byte("deadline-secret-sentinel")
	file, err := engine.SealExtentSource(test.Context(), attempt, 0, int64(len(payload)), sha256.Sum256(payload), func(ctx context.Context, output io.Writer) error {
		if _, err := output.Write(payload); err != nil {
			return err
		}
		<-ctx.Done()
		if count, err := output.Write(payload); count != 0 || !errors.Is(err, context.DeadlineExceeded) {
			test.Error("timed-out producer could continue writing")
		}
		return nil
	})
	if file.get != nil || !errors.Is(err, context.DeadlineExceeded) || attempt.get().entries != 0 || !attempt.get().poison {
		test.Fatal("operation timeout succeeded or admitted a record", err)
	}
}
