//go:build linux

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
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/state"
)

func restoreAuthenticatedFixture(test *testing.T) (*os.File, *os.File, SealMetadata) {
	test.Helper()
	service, store, cache, directory, _ := captureFixture(test)
	for _, owner := range []string{"capture-owner-one", "capture-owner-two"} {
		token, err := github.NewToken("restore-bundle-token-sentinel", []string{"repo"})
		if err != nil {
			test.Fatal(err)
		}
		if _, err := store.PutCredential(test.Context(), owner, "restore-viewer", "", token); err != nil {
			test.Fatal(err)
		}
		if _, err := store.CreateConfigurationAsset(test.Context(), owner, state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "restore-bundle-config", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte("restore-bundle-config-sentinel"))}); err != nil {
			test.Fatal(err)
		}
	}
	engine, recoveryKey, _ := sealFixture(test)
	captured, err := engine.CaptureSnapshot(test.Context(), service, captureOptions())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = captured.Close() })
	transferred := test.TempDir()
	if err := os.Chmod(transferred, 0700); err != nil {
		test.Fatal(err)
	}
	files := make(map[refData]*os.File)
	var bootstrap, complete *os.File
	var expected attemptData
	if err := captured.WithFiles(test.Context(), func(_ context.Context, ref ObjectRef, input *os.File) error {
		path := filepath.Join(transferred, fmt.Sprint(len(files)))
		output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyError := io.Copy(output, input)
		if err := errors.Join(copyError, output.Sync(), output.Close()); err != nil {
			return err
		}
		copy, err := os.Open(path)
		if err != nil {
			return err
		}
		test.Cleanup(func() { _ = copy.Close() })
		data := ref.read()
		files[data] = copy
		if data.kind == Bootstrap {
			bootstrap, expected = copy, data.attempt
		}
		if data.kind == Complete {
			complete = copy
		}
		return nil
	}); err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(captured.Close(), engine.Close(), service.Close(), cache.Close(), store.Close()); err != nil {
		test.Fatal(err)
	}
	if err := os.Rename(directory, directory+"-offline"); err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = os.Rename(directory+"-offline", directory) })
	parent, err := os.Open(transferred)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = parent.Close() })
	openEngine, err := NewSealEngine(parent, recoveryKey, SealLimits{TotalPlainBytes: 1 << 30, TotalCipherBytes: 1 << 30, StagingBytes: 2 << 30, InputStagingBytes: 128 << 20, MaxObjects: 100000, MaxInventoryRecords: 100000, OperationTimeout: time.Minute})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = openEngine.Close() })
	session, err := openEngine.BeginOpen(test.Context(), bootstrap, complete, Attempt{read: func() attemptData { return expected }})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = session.Close() })
	for {
		ref, err := session.NextRef(test.Context())
		if errors.Is(err, io.EOF) {
			break
		}
		if err == nil && ref.read == nil {
			break
		}
		if err != nil || files[ref.read()] == nil {
			test.Fatal("missing authenticated object", err)
		}
		if err := session.AcceptFile(test.Context(), files[ref.read()]); err != nil {
			test.Fatal(err)
		}
	}
	bundle, err := session.Finish(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = bundle.Close() })
	metadata, err := bundle.Metadata(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	candidate := test.TempDir()
	if err := os.Chmod(candidate, 0700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(candidate, "server.db")
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		test.Fatal(err)
	}
	_, copyError := bundle.CopyFile(test.Context(), "database", "database", output)
	if err := errors.Join(copyError, output.Sync(), output.Close(), bundle.Close(), session.Close(), openEngine.Close(), recoveryKey.Destroy()); err != nil {
		test.Fatal(err)
	}
	candidateParent, err := os.Open(candidate)
	if err != nil {
		test.Fatal(err)
	}
	database, err := os.Open(path)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = database.Close(); _ = candidateParent.Close() })
	if _, err := os.Stat(filepath.Join(directory, "server.db")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("source remained available")
	}
	return candidateParent, database, metadata
}

func TestRestoreDatabaseFromAuthenticatedBundle(test *testing.T) {
	parent, database, metadata := restoreAuthenticatedFixture(test)
	evidence, err := state.InspectRestoreDatabase(test.Context(), parent, database, bytes.Repeat([]byte{91}, 32), captureOptions().Limits.Database)
	if err != nil || !evidence.Valid() {
		test.Fatal("authenticated database refused", err)
	}
	if size, digest := evidence.Database(); size != metadata.Database.Bytes || hex.EncodeToString(digest[:]) != metadata.Database.SHA256 {
		test.Fatal("container database identity changed")
	}
	schema := evidence.SchemaFingerprint()
	if evidence.KeyIdentity() != metadata.MasterKeyID || hex.EncodeToString(schema[:]) != metadata.SchemaSHA256 {
		test.Fatal("schema or original master key identity differs")
	}
	if _, _, references := evidence.References(); references+1 != uint64(metadata.Inventory.Records) || metadata.Inventory.Owners != 2 || metadata.Archives != 1 {
		test.Fatal("existing all-owner inventory contract changed")
	}
	for _, wrong := range [][]byte{nil, bytes.Repeat([]byte{13}, 32)} {
		evidence, err := state.InspectRestoreDatabase(test.Context(), parent, database, wrong, captureOptions().Limits.Database)
		if !errors.Is(err, state.ErrRestoreKey) || evidence.Valid() {
			test.Fatal("recovery key substituted for original master key", err)
		}
	}
}

func TestRestoreDatabaseSourceCompatibility(test *testing.T) {
	parent, database, metadata := restoreAuthenticatedFixture(test)
	evidence, err := state.InspectRestoreDatabase(test.Context(), parent, database, bytes.Repeat([]byte{91}, 32), captureOptions().Limits.Database)
	if err != nil {
		test.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(parent.Name(), "server.db"))
	if err != nil {
		test.Fatal(err)
	}
	_, digest := evidence.Database()
	if digest != sha256.Sum256(before) || metadata.CutID == "" || metadata.ClosureSHA256 == "" {
		test.Fatal("existing authenticated format was replaced")
	}
}
