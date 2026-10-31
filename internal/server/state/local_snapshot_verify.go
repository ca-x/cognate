package state

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"github.com/ca-x/cognate/internal/server/ent"
	"io"
	"os"
	"slices"
	"strconv"
)

func snapshotRequiredMembers(data []byte, names []string) error {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	start, err := decoder.ReadToken()
	if err != nil || start.Kind() != '{' || len(names) > 63 {
		return ErrLocalSnapshotInvalid
	}
	var seen uint64
	for decoder.PeekKind() != '}' {
		key, err := decoder.ReadToken()
		if err != nil || key.Kind() != '"' {
			return ErrLocalSnapshotInvalid
		}
		index := slices.Index(names, key.String())
		if index < 0 || seen&(1<<index) != 0 || decoder.PeekKind() == 'n' {
			return ErrLocalSnapshotInvalid
		}
		seen |= 1 << index
		if decoder.SkipValue() != nil {
			return ErrLocalSnapshotInvalid
		}
	}
	if _, err = decoder.ReadToken(); err != nil || seen != (uint64(1)<<len(names))-1 {
		return ErrLocalSnapshotInvalid
	}
	if _, err = decoder.ReadToken(); err != io.EOF {
		return ErrLocalSnapshotInvalid
	}
	return nil
}

func snapshotVerifyStage(ctx context.Context, stage *localSnapshotStage, response snapshotWorkerResponse, store *Store, limits LocalSnapshotLimits) (manifest localSnapshotManifest, returnedError error) {
	if _, err := stage.root.Seek(0, io.SeekStart); err != nil {
		return manifest, ErrLocalSnapshotInvalid
	}
	entries, err := stage.root.ReadDir(4)
	if err != nil && !errors.Is(err, io.EOF) {
		return manifest, ErrLocalSnapshotInvalid
	}
	if len(entries) != 3 {
		return manifest, ErrLocalSnapshotInvalid
	}
	names := make([]string, 0, 3)
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"candidate.json", "references.ndjson", "snapshot.sqlite"}) {
		return manifest, ErrLocalSnapshotInvalid
	}
	file, err := stage.open("candidate.json")
	if err != nil {
		return manifest, err
	}
	info, statError := file.Stat()
	if statError != nil || info.Size() < 1 || info.Size() > 65536 {
		if file.Close() != nil {
			return manifest, ErrLocalSnapshotUncertain
		}
		return manifest, ErrLocalSnapshotInvalid
	}
	data, readError := io.ReadAll(io.LimitReader(file, 65537))
	closeError := file.Close()
	if closeError != nil {
		retainSnapshotResource(file)
		return manifest, ErrLocalSnapshotUncertain
	}
	if readError != nil || int64(len(data)) != info.Size() {
		return manifest, ErrLocalSnapshotInvalid
	}
	if snapshotRequiredMembers(data, []string{"format", "version", "contentClosure", "id", "database_bytes", "database_sha256", "sqlite_version", "schema_sha256", "reference_bytes", "reference_sha256", "repositories", "skills", "packs", "items", "total_records", "key_id", "key_proof", "policy_proof", "credentials"}) != nil || snapshotJSON(data, &manifest) != nil {
		return localSnapshotManifest{}, ErrLocalSnapshotInvalid
	}
	var proofRows struct {
		Credentials []jsontext.Value `json:"credentials"`
	}
	if json.Unmarshal(data, &proofRows) != nil || len(proofRows.Credentials) > 1 {
		return localSnapshotManifest{}, ErrLocalSnapshotInvalid
	}
	for _, row := range proofRows.Credentials {
		if snapshotRequiredMembers(row, []string{"user_key", "viewer_id", "client_id", "key_id", "generation", "ciphertext"}) != nil {
			return localSnapshotManifest{}, ErrLocalSnapshotInvalid
		}
	}
	if manifest.Format != "cognate-local-sqlite-candidate" || manifest.Version != 1 || manifest.ContentClosure != "not-included" || manifest.ID != hex.EncodeToString(response.nonce[:]) || manifest.DatabaseBytes != int64(response.databaseBytes) || manifest.DatabaseSHA256 != hex.EncodeToString(response.databaseHash[:]) || manifest.SchemaSHA256 != snapshotKnownSchema || manifest.SchemaSHA256 != hex.EncodeToString(response.schemaHash[:]) || manifest.ReferenceBytes < 0 || manifest.ReferenceBytes > limits.MaxInventoryBytes || manifest.ReferenceBytes != int64(response.referenceBytes) || manifest.ReferenceSHA256 != hex.EncodeToString(response.referenceHash[:]) || manifest.TotalRecords != response.records || manifest.TotalRecords > limits.MaxRecords || manifest.Repositories > manifest.TotalRecords || manifest.Skills > manifest.TotalRecords-manifest.Repositories || manifest.Packs > manifest.TotalRecords-manifest.Repositories-manifest.Skills || manifest.Items != manifest.TotalRecords-manifest.Repositories-manifest.Skills-manifest.Packs || len(manifest.SQLiteVersion) < 1 || len(manifest.SQLiteVersion) > 32 || len(manifest.KeyID) != 43 || len(manifest.KeyProof) < 1 || len(manifest.KeyProof) > 512 || len(manifest.PolicyProof) < 1 || len(manifest.PolicyProof) > 512 {
		return localSnapshotManifest{}, ErrLocalSnapshotInvalid
	}
	if manifest.DatabaseBytes > limits.MaxStagingBytes || manifest.ReferenceBytes > limits.MaxStagingBytes-manifest.DatabaseBytes || int64(len(data)) > limits.MaxStagingBytes-manifest.DatabaseBytes-manifest.ReferenceBytes {
		return localSnapshotManifest{}, ErrLocalSnapshotBudget
	}
	if verifyStoredKeyProof(store.state.aead, store.state.keyID, manifest.KeyID, manifest.KeyProof) != nil || verifyStoredObservationProof(store.state.aead, store.state.keyID, manifest.KeyID, manifest.PolicyProof, []byte(strconv.FormatInt(int64(store.state.observationMaxAge), 10))) != nil {
		return localSnapshotManifest{}, ErrLocalSnapshotInvalid
	}
	if len(manifest.Credentials) > 1 {
		return localSnapshotManifest{}, ErrLocalSnapshotInvalid
	}
	for _, credential := range manifest.Credentials {
		if validateIdentifier(credential.UserKey) != nil || validateIdentifier(credential.ViewerID) != nil || len(credential.ClientID) > 256 || len(credential.Generation) > 64 || credential.Generation == "" || len(credential.Ciphertext) > 32768 {
			return localSnapshotManifest{}, ErrLocalSnapshotInvalid
		}
		if _, err := store.decryptCredential(credential.UserKey, &ent.GitHubCredential{ViewerID: credential.ViewerID, ClientID: credential.ClientID, KeyID: credential.KeyID, Generation: credential.Generation, Ciphertext: credential.Ciphertext}); err != nil {
			return localSnapshotManifest{}, ErrLocalSnapshotInvalid
		}
		clear(credential.Ciphertext)
	}
	manifest.Credentials = nil
	for _, entry := range []struct {
		name string
		size int64
		hash string
	}{{"snapshot.sqlite", manifest.DatabaseBytes, manifest.DatabaseSHA256}, {"references.ndjson", manifest.ReferenceBytes, manifest.ReferenceSHA256}} {
		if _, err := snapshotCopy(ctx, stage, entry.name, entry.size, entry.hash, io.Discard); err != nil {
			return localSnapshotManifest{}, err
		}
	}
	return manifest, ctx.Err()
}

type snapshotContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader snapshotContextReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(data)
}

func snapshotCopy(ctx context.Context, stage *localSnapshotStage, name string, size int64, expected string, output io.Writer) (written int64, returnedError error) {
	if size < 0 || size == int64(^uint64(0)>>1) || !validContentHash(expected) || output == nil {
		return 0, ErrLocalSnapshotInvalid
	}
	file, err := stage.open(name)
	if err != nil {
		return 0, err
	}
	defer func() {
		if file.Close() != nil {
			retainSnapshotResource(file)
			returnedError = ErrLocalSnapshotUncertain
		}
		if returnedError != nil {
			written = 0
		}
	}()
	before, err := file.Stat()
	if err != nil || before.Size() != size {
		return 0, ErrLocalSnapshotInvalid
	}
	hash := sha256.New()
	buffer := make([]byte, 32768)
	count, err := io.CopyBuffer(struct{ io.Writer }{io.MultiWriter(output, hash)}, snapshotContextReader{ctx: ctx, reader: io.LimitReader(file, size+1)}, buffer)
	if err != nil {
		return 0, snapshotError(ctx, err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || count != size || hex.EncodeToString(hash.Sum(nil)) != expected {
		return 0, ErrLocalSnapshotInvalid
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return count, nil
}

func snapshotReadRow(reader *bufio.Reader) (snapshotReference, int64, error) {
	var row snapshotReference
	data, err := reader.ReadSlice('\n')
	if err != nil {
		return row, 0, err
	}
	if len(data) > 65536 {
		return row, 0, ErrLocalSnapshotBudget
	}
	if snapshotJSON(data, &row) != nil {
		return row, 0, ErrLocalSnapshotInvalid
	}
	var names []string
	switch row.Kind {
	case "repository":
		names = []string{"kind", "id", "public_id", "owner", "name", "branch", "archive_hash", "skill_count", "revision"}
	case "skill":
		names = []string{"kind", "id", "public_id", "repository", "path", "content_hash"}
	case "pack":
		names = []string{"kind", "id", "public_id", "owner", "revision", "item_count"}
	case "pack_item":
		names = []string{"kind", "id", "pack", "skill", "slug"}
	default:
		return row, 0, ErrLocalSnapshotInvalid
	}
	if snapshotRequiredMembers(data, names) != nil || row.ID < 1 {
		return row, 0, ErrLocalSnapshotInvalid
	}
	return row, int64(len(data)), nil
}
