package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func TestSealTranscriptLiteral(test *testing.T) {
	header := sealHeader{purpose: 1, attempt: attemptData{instance: [16]byte{1}, backup: [16]byte{2}, attempt: [16]byte{3}}, ordinal: 7, length: 1, nonce: [8]byte{4}}
	actual := header.encode()
	expected, err := hex.DecodeString("4347424b5345303100010001010000000100000000000000000000000000000002000000000000000000000000000000030000000000000000000000000000000000000700100000000000000000000104000000000000000000000000000000")
	if err != nil || len(expected) != 96 || string(actual[:]) != string(expected) {
		test.Fatal("literal header transcript mismatch")
	}
	for _, example := range []struct{ plain, cipher int64 }{{1, 193}, {1048496, 1048688}, {1048576, 1048784}, {67108864, 67110080}} {
		actual, err := sealCipherSize(example.plain)
		if err != nil || actual != example.cipher {
			test.Fatal("literal ciphertext arithmetic mismatch", example.plain, actual)
		}
	}
	if _, err := sealCipherSize(0); err == nil {
		test.Fatal("empty typed payload accepted")
	}
}

func TestSealMetadataClosure(test *testing.T) {
	sealMetadataClosureCase(test, false)
}
func TestSealIndexCloseFailure(test *testing.T) {
	sealMetadataClosureCase(test, true)
}
func TestSealAggregateObjectQuota(test *testing.T) {
	for _, limit := range []uint32{3, 4} {
		test.Run(fmt.Sprint(limit), func(test *testing.T) { sealMetadataClosureCase(test, false, limit) })
	}
}
func sealMetadataClosureCase(test *testing.T, faultIndex bool, objectLimit ...uint32) {
	engine, _, source := sealFixture(test, objectLimit...)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	var records []SealExtentRecord
	var dataFiles []SealedFile
	for ordinal, kind := range []string{"database", "inventory"} {
		artifact, err := engine.SealExtent(test.Context(), attempt, uint32(ordinal), source)
		if err != nil {
			test.Fatal(err)
		}
		defer artifact.Close()
		dataFiles = append(dataFiles, artifact)
		ref := artifact.Ref().read()
		plain := sha256.Sum256([]byte("synthetic bounded plaintext"))
		records = append(records, SealExtentRecord{Kind: "extent", Ordinal: uint32(ordinal), FileKind: kind, FileID: kind, PlaintextBytes: 27, PlaintextSHA256: hex.EncodeToString(plain[:]), CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])})
	}
	page, err := engine.SealPage(test.Context(), attempt, 1, 2, func(yield func(SealExtentRecord, error) bool) {
		for _, record := range records {
			if !yield(record, nil) {
				return
			}
		}
	})
	if err != nil {
		test.Fatal("page refused", err)
	}
	defer page.Close()
	plain := sha256.Sum256([]byte("synthetic bounded plaintext"))
	hash := hex.EncodeToString(plain[:])
	root, err := engine.SealRoot(test.Context(), attempt, SealMetadata{AppVersion: "test", SchemaSHA256: hash, CutID: "01000000000000000000000000000000", MasterKeyID: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Database: SealFileDescription{Bytes: 27, SHA256: hash, Extents: 1}, Inventory: SealInventoryDescription{Bytes: 27, SHA256: hash, Extents: 1, Records: 1, Owners: 1}, ClosureSHA256: hash, ExternalConfigNames: []string{"MASTER_KEY_FILE"}})
	if len(objectLimit) > 0 && objectLimit[0] == 3 {
		if !errors.Is(err, ErrSealBudget) || root.get != nil {
			test.Fatal("root aggregate cap was not enforced", err)
		}
		return
	}
	if err != nil {
		test.Fatal("root refused", err)
	}
	defer root.Close()
	complete, err := engine.SealComplete(test.Context(), attempt)
	if err != nil {
		test.Fatal("complete refused", err)
	}
	defer complete.Close()
	var session OpenSession
	err = bootstrap.WithFile(test.Context(), func(bootstrapFile *os.File) error {
		return complete.WithFile(test.Context(), func(completeFile *os.File) error {
			var openErr error
			session, openErr = engine.BeginOpen(test.Context(), bootstrapFile, completeFile, Attempt{read: func() attemptData { return attempt.get().identity }})
			return openErr
		})
	})
	if err != nil {
		test.Fatal("open refused", err)
	}
	defer session.Close()
	if faultIndex {
		original := engine.get().ops.close
		engine.get().ops.close = func(file *os.File) error {
			err := original(file)
			if strings.HasSuffix(file.Name(), "-index") {
				return ErrSealCleanup
			}
			return err
		}
		defer func() { engine.get().ops.close = original }()
	}
	for _, artifact := range []SealedFile{root, page} {
		next, nextErr := session.NextRef(test.Context())
		if nextErr != nil || next.read == nil || next.read() != artifact.Ref().read() {
			test.Fatal("next metadata pin")
		}
		err = artifact.WithFile(test.Context(), func(input *os.File) error { return session.AcceptFile(test.Context(), input) })
		if faultIndex && artifact.Ref().read().ordinal == 1 {
			if !errors.Is(err, ErrSealUncertain) {
				test.Fatal("index writer Close not checked before exposing descriptors")
			}
			return
		}
		if err != nil {
			test.Fatal("metadata open refused", err)
		}
	}
	if _, err = session.Finish(test.Context()); err == nil {
		test.Fatal("incomplete bundle minted")
	}
	for _, artifact := range dataFiles {
		if err = artifact.WithFile(test.Context(), func(input *os.File) error { return session.AcceptFile(test.Context(), input) }); err != nil {
			test.Fatal("data open refused", err)
		}
	}
	bundle, err := session.Finish(test.Context())
	if err != nil {
		test.Fatal("complete bundle refused", err)
	}
	var output bytes.Buffer
	count, err := bundle.CopyFile(test.Context(), "database", "database", &output)
	if err != nil || count != 27 || output.String() != "synthetic bounded plaintext" {
		test.Fatal("independent reconstructed plaintext mismatch", err)
	}
	if err = bundle.Close(); err != nil {
		test.Fatal("bundle cleanup", err)
	}
}

func sealFixture(test *testing.T, objectLimit ...uint32) (SealEngine, RecoveryKey, *os.File) {
	test.Helper()
	if err := maintenance.CheckPlatform(test.Context()); err != nil {
		test.Skip("physical crypto filesystem unsupported on this platform")
	}
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	parent, err := os.Open(directory)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = parent.Close() })
	key, err := ImportRecoveryKey([16]byte{9}, bytes.Repeat([]byte{13}, 32))
	if err != nil {
		test.Fatal("key import refused", err)
	}
	test.Cleanup(func() { _ = key.Destroy() })
	maxObjects := uint32(100000)
	if len(objectLimit) > 0 {
		maxObjects = objectLimit[0]
	}
	engine, err := NewSealEngine(parent, key, SealLimits{TotalPlainBytes: 1 << 30, TotalCipherBytes: 1 << 30, StagingBytes: 2 << 30, InputStagingBytes: 128 << 20, MaxObjects: maxObjects, MaxInventoryRecords: 100000, OperationTimeout: time.Minute})
	if err != nil {
		test.Fatal("private engine refused", err)
	}
	test.Cleanup(func() { _ = engine.Close() })
	sourceName := filepath.Join(directory, "synthetic-source")
	if err = os.WriteFile(sourceName, []byte("synthetic bounded plaintext"), 0600); err != nil {
		test.Fatal(err)
	}
	source, err := os.Open(sourceName)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = source.Close() })
	return engine, key, source
}

func TestSealRealBootstrapExtent(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal("attempt refused", err)
	}
	defer bootstrap.Close()
	if bootstrap.Ref().read == nil || bootstrap.Ref().read().size != 156 {
		test.Fatal("bootstrap length/ref")
	}
	if err = bootstrap.WithFile(test.Context(), func(file *os.File) error {
		buffer := make([]byte, 157)
		count, readErr := file.Read(buffer)
		if count != 156 || readErr != nil || string(buffer[:8]) != "CGBKBT01" {
			return ErrInvalid
		}
		return nil
	}); err != nil {
		test.Fatal(err)
	}
	if _, err = source.Seek(3, io.SeekStart); err != nil {
		test.Fatal(err)
	}
	sealed, err := engine.SealExtent(test.Context(), attempt, 0, source)
	if err != nil {
		test.Fatal("extent refused", err)
	}
	defer sealed.Close()
	position, _ := source.Seek(0, io.SeekCurrent)
	if position != 3 {
		test.Fatal("borrowed offset changed")
	}
	if sealed.Ref().read().size != 219 {
		test.Fatal("cipher length")
	}
	if _, err = engine.SealExtent(context.Background(), attempt, 0, source); err == nil {
		test.Fatal("one-shot slot reused")
	}
}
