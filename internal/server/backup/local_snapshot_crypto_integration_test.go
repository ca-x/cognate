//go:build linux

package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestMain(tests *testing.M) {
	if handled, code := state.SnapshotWorkerDispatch(os.Args); handled {
		os.Exit(code)
	}
	os.Exit(tests.Run())
}

func TestLocalSnapshotActualCryptoOfflineRecovery(test *testing.T) {
	ctx := test.Context()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "source.sqlite")
	master := bytes.Repeat([]byte{91}, 32)
	store, err := state.OpenWithOptions(ctx, path, master, state.Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	for _, owner := range []string{"offline-owner-one", "offline-owner-two"} {
		if err = store.CreateUser(ctx, owner); err != nil {
			test.Fatal(err)
		}
		token, err := github.NewToken("synthetic-offline-access", []string{"repo"})
		if err != nil {
			test.Fatal(err)
		}
		if _, err = store.PutCredential(ctx, owner, "viewer-offline", "", token); err != nil {
			test.Fatal(err)
		}
		if _, err = store.CreateConfigurationAsset(ctx, owner, state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "offline private config", SchemaVersion: 1, Payload: state.NewConfigurationPayload(bytes.Repeat([]byte("encrypted-config-"), 8192))}); err != nil {
			test.Fatal(err)
		}
	}
	outputs := test.TempDir()
	database, err := os.OpenFile(filepath.Join(outputs, "database"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	var references bytes.Buffer
	limits := state.LocalSnapshotLimits{MaxDBBytes: 8 << 20, MaxInventoryBytes: 4 << 20, MaxStagingBytes: 64 << 20, MaxRecords: 100000, SQLiteHeapBytes: 32 << 20, WorkerAddressSpaceBytes: 16 << 30, WorkerCPUSeconds: 10, MaxWALBytes: 64 << 20, MaxSHMBytes: 1 << 20, OperationTimeout: 10 * time.Second, DrainTimeout: time.Second}
	summary, err := store.WithLocalSnapshot(ctx, limits, func(ctx context.Context, view state.LocalSnapshotView) error {
		if _, err := view.CopyDatabase(ctx, database); err != nil {
			return err
		}
		_, err := view.CopyReferences(ctx, &references)
		return err
	})
	if err != nil {
		test.Fatal("real B2 cut refused", err)
	}
	if err = database.Sync(); err != nil {
		test.Fatal(err)
	}
	if references.Len() != 0 {
		test.Fatal("fixture unexpectedly has archive obligations")
	}
	if err = store.Close(); err != nil {
		test.Fatal(err)
	}
	offline := directory + "-unavailable"
	if err = os.Rename(directory, offline); err != nil {
		test.Fatal(err)
	}
	defer os.Rename(offline, directory)
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		test.Fatal("original source remains available")
	}
	inventoryBytes := []byte("{\"format\":\"b2-core-test-no-archive\",\"owner\":1,\"references\":0}\n{\"format\":\"b2-core-test-no-archive\",\"owner\":2,\"references\":0}\n")
	inventory, err := os.OpenFile(filepath.Join(outputs, "inventory"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		test.Fatal(err)
	}
	defer inventory.Close()
	if _, err = inventory.Write(inventoryBytes); err != nil {
		test.Fatal(err)
	}
	if err = inventory.Sync(); err != nil {
		test.Fatal(err)
	}
	databaseBytes, databaseHash := summary.Metadata().Database()
	if databaseBytes <= 0 {
		test.Fatal("outer B2 did not commit a result")
	}
	inventoryHash := sha256.Sum256(inventoryBytes)
	engine, key, _ := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(ctx, [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer attempt.Close()
	defer bootstrap.Close()
	var dataFiles []SealedFile
	var records []SealExtentRecord
	for ordinal, input := range []*os.File{database, inventory} {
		artifact, err := engine.SealExtent(ctx, attempt, uint32(ordinal), input)
		if err != nil {
			test.Fatal(err)
		}
		defer artifact.Close()
		dataFiles = append(dataFiles, artifact)
		kind, size, digest := "database", databaseBytes, databaseHash
		if ordinal == 1 {
			kind, size, digest = "inventory", int64(len(inventoryBytes)), inventoryHash
		}
		ref := artifact.Ref().read()
		records = append(records, SealExtentRecord{Kind: "extent", Ordinal: uint32(ordinal), FileKind: kind, FileID: kind, PlaintextBytes: size, PlaintextSHA256: hex.EncodeToString(digest[:]), CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])})
	}
	page, err := engine.SealPage(ctx, attempt, 1, 2, func(yield func(SealExtentRecord, error) bool) {
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
	schema := summary.Metadata().SchemaFingerprint()
	cutID := summary.Metadata().SnapshotID()
	root, err := engine.SealRoot(ctx, attempt, SealMetadata{AppVersion: "b2-core-integration", SchemaSHA256: hex.EncodeToString(schema[:]), CutID: hex.EncodeToString(cutID[:]), MasterKeyID: summary.Metadata().KeyIdentity(), Database: SealFileDescription{Bytes: databaseBytes, SHA256: hex.EncodeToString(databaseHash[:]), Extents: 1}, Inventory: SealInventoryDescription{Bytes: int64(len(inventoryBytes)), SHA256: hex.EncodeToString(inventoryHash[:]), Extents: 1, Records: 2, Owners: 2}, ClosureSHA256: hex.EncodeToString(inventoryHash[:]), ExternalConfigNames: []string{"MASTER_KEY_FILE"}})
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	complete, err := engine.SealComplete(ctx, attempt)
	if err != nil {
		test.Fatal(err)
	}
	defer complete.Close()
	expectedAttempt := Attempt{read: func() attemptData { return attempt.get().identity }}
	cipherDirectory := test.TempDir()
	if err = os.Chmod(cipherDirectory, 0700); err != nil {
		test.Fatal(err)
	}
	artifacts := append([]SealedFile{bootstrap, root, page}, dataFiles...)
	artifacts = append(artifacts, complete)
	copies := make([]*os.File, 0, len(artifacts))
	pins := make([]ObjectRef, 0, len(artifacts))
	for index, artifact := range artifacts {
		pins = append(pins, artifact.Ref())
		copyPath := filepath.Join(cipherDirectory, strconv.Itoa(index))
		output, err := os.OpenFile(copyPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			test.Fatal(err)
		}
		err = artifact.WithFile(ctx, func(input *os.File) error { _, err := io.Copy(output, input); return err })
		syncError, closeError := output.Sync(), output.Close()
		if err != nil || syncError != nil || closeError != nil {
			test.Fatal("exact ciphertext transfer failed")
		}
		input, err := os.Open(copyPath)
		if err != nil {
			test.Fatal(err)
		}
		defer input.Close()
		copies = append(copies, input)
	}
	for _, artifact := range artifacts {
		if err = artifact.Close(); err != nil {
			test.Fatal(err)
		}
	}
	if err = engine.Close(); err != nil {
		test.Fatal(err)
	}
	if err = database.Close(); err != nil {
		test.Fatal(err)
	}
	if err = inventory.Close(); err != nil {
		test.Fatal(err)
	}
	if err = os.Remove(database.Name()); err != nil {
		test.Fatal(err)
	}
	if err = os.Remove(inventory.Name()); err != nil {
		test.Fatal(err)
	}
	openParent, err := os.Open(cipherDirectory)
	if err != nil {
		test.Fatal(err)
	}
	defer openParent.Close()
	openEngine, err := NewSealEngine(openParent, key, SealLimits{TotalPlainBytes: 1 << 30, TotalCipherBytes: 1 << 30, StagingBytes: 2 << 30, InputStagingBytes: 128 << 20, MaxObjects: 100000, MaxInventoryRecords: 100000, OperationTimeout: time.Minute})
	if err != nil {
		test.Fatal(err)
	}
	defer openEngine.Close()
	session, err := openEngine.BeginOpen(ctx, copies[0], copies[len(copies)-1], expectedAttempt)
	if err != nil {
		test.Fatal(err)
	}
	defer session.Close()
	if _, err = session.Finish(ctx); err == nil {
		test.Fatal("private plaintext released before authenticated closure")
	}
	for index, pin := range pins[1 : len(pins)-1] {
		expected, err := session.NextRef(ctx)
		if err != nil || expected.read == nil {
			test.Fatal("missing authenticated object", err)
		}
		if expected.read() != pin.read() {
			test.Fatal("authenticated pin differs")
		}
		if err = session.AcceptFile(ctx, copies[index+1]); err != nil {
			test.Fatal(err)
		}
	}
	bundle, err := session.Finish(ctx)
	if err != nil {
		test.Fatal(err)
	}
	defer bundle.Close()
	restored, err := os.OpenFile(filepath.Join(outputs, "offline-restored.sqlite"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		test.Fatal(err)
	}
	if count, err := bundle.CopyFile(ctx, "database", "database", restored); err != nil || count != databaseBytes {
		test.Fatal("authenticated database not reconstructed", err)
	}
	if err = restored.Sync(); err != nil {
		test.Fatal(err)
	}
	if err = restored.Close(); err != nil {
		test.Fatal(err)
	}
	var inventoryCopy bytes.Buffer
	if _, err = bundle.CopyFile(ctx, "inventory", "inventory", &inventoryCopy); err != nil || !bytes.Equal(inventoryCopy.Bytes(), inventoryBytes) {
		test.Fatal("inventory changed", err)
	}
	pool, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: restored.Name(), RawQuery: "mode=ro&immutable=1"}).String())
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	for _, table := range []string{"users", "git_hub_credentials", "configuration_assets"} {
		var count int
		if pool.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count) != nil || count != 2 {
			test.Fatal("offline SQL lost owners or encrypted rows", table)
		}
	}
	var integrity string
	if pool.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity) != nil || integrity != "ok" {
		test.Fatal("offline integrity failed")
	}
	var keyID string
	var proof []byte
	if pool.QueryRowContext(ctx, "SELECT key_id,key_proof FROM server_metadata WHERE name='credential-key-v1'").Scan(&keyID, &proof) != nil {
		test.Fatal("offline key proof absent")
	}
	digest := sha256.Sum256(master)
	if keyID != base64.RawURLEncoding.EncodeToString(digest[:]) {
		test.Fatal("offline key identity changed")
	}
	block, _ := aes.NewCipher(master)
	aead, _ := cipher.NewGCM(block)
	if len(proof) < 28 {
		test.Fatal("short independent proof")
	}
	plain, err := aead.Open(nil, proof[:12], proof[12:], []byte("credential-key-v1:"+keyID))
	if err != nil || string(plain) != "credential-key-v1" {
		test.Fatal("independent AEAD proof failed")
	}
	clear(plain)
	file, err := os.Open(restored.Name())
	if err != nil {
		test.Fatal(err)
	}
	hash := sha256.New()
	_, copyError := io.Copy(hash, file)
	closeError := file.Close()
	if copyError != nil || closeError != nil || !bytes.Equal(hash.Sum(nil), databaseHash[:]) {
		test.Fatal("whole database raw bytes not preserved")
	}
}
