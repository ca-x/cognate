//go:build linux

package state

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/ent"
	"golang.org/x/sys/unix"
)

func restoreDatabaseWorkerServe() (exitCode int) {
	for _, descriptor := range []int{3, 4, 8} {
		link, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(descriptor))
		if err != nil || !strings.HasPrefix(link, "pipe:[") {
			return 2
		}
		var info unix.Stat_t
		if unix.Fstat(descriptor, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFIFO || info.Uid != uint32(os.Geteuid()) {
			return 2
		}
		flags, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
		expected := unix.O_RDONLY
		if descriptor == 4 {
			expected = unix.O_WRONLY
		}
		if err != nil || flags&unix.O_ACCMODE != expected {
			return 2
		}
	}
	var image, self unix.Stat_t
	if unix.Stat("/proc/self/exe", &self) != nil || unix.Fstat(5, &image) != nil || self.Dev != image.Dev || self.Ino != image.Ino || image.Mode&unix.S_IFMT != unix.S_IFREG {
		return 2
	}
	input, output := os.NewFile(3, "restore request"), os.NewFile(4, "restore response")
	defer func() {
		if output.Close() != nil {
			exitCode = 2
		}
	}()
	var request restoreDatabaseRequest
	readError := restoreReadMessage(input, 4096, &request)
	closeError := input.Close()
	if readError != nil || request.Nonce == ([16]byte{}) || !request.Limits.valid() {
		return 2
	}
	response := restoreDatabaseResponse{Nonce: request.Nonce, Status: "invalid"}
	inspectError := ErrRestoreInvalid
	if closeError != nil {
		inspectError = ErrRestoreUncertain
	} else {
		var controls snapshotWorkerResponse
		inspectError = snapshotInstallControls(request.Limits, &controls)
		if inspectError == nil {
			inspectError = snapshotSetLimit(unix.RLIMIT_NOFILE, 32)
		}
		if inspectError == nil {
			ctx, cancel := context.WithTimeout(context.Background(), request.Limits.OperationTimeout)
			response.Facts, inspectError = restoreDatabaseWorkerInspect(ctx, request)
			cancel()
		}
	}
	switch restoreDatabaseError(inspectError) {
	case nil:
		response.Status = "ok"
	case ErrRestoreKey:
		response.Status = "key"
	case ErrRestoreBudget:
		response.Status = "budget"
	case ErrRestoreUncertain:
		response.Status = "uncertain"
	}
	if inspectError != nil {
		response.Facts = restoreDatabaseFacts{}
	}
	if restoreWriteMessage(output, response) != nil {
		return 2
	}
	if inspectError != nil {
		return 1
	}
	return 0
}

func restoreDatabaseWorkerInspect(ctx context.Context, request restoreDatabaseRequest) (facts restoreDatabaseFacts, returnedError error) {
	parentName, err := os.Readlink("/proc/self/fd/6")
	if err != nil {
		return facts, ErrRestoreInvalid
	}
	parent, database, keyPipe := os.NewFile(6, parentName), os.NewFile(7, "restore database"), os.NewFile(8, "restore key")
	for _, file := range []*os.File{parent, database, keyPipe} {
		defer func() {
			if file.Close() != nil {
				facts = restoreDatabaseFacts{}
				returnedError = ErrRestoreUncertain
			}
		}()
	}
	parentIdentity, err := restoreDatabaseFileIdentity(parent)
	if err != nil || parentIdentity != request.Parent {
		return facts, ErrRestoreInvalid
	}
	databaseIdentity, err := restoreDatabaseFileIdentity(database)
	if err != nil || databaseIdentity != request.Database {
		return facts, ErrRestoreInvalid
	}
	before, err := restoreDatabasePin(parent, database)
	if err != nil {
		return facts, err
	}
	if before.Size() > request.Limits.MaxDBBytes || before.Size() > request.Limits.MaxStagingBytes {
		return facts, ErrRestoreBudget
	}
	key, err := io.ReadAll(io.LimitReader(keyPipe, 33))
	defer clear(key)
	if err != nil || len(key) != 32 {
		return facts, ErrRestoreKey
	}
	facts, err = restoreDatabaseCheck(ctx, database, key, before.Size(), request.Limits)
	if err != nil {
		return restoreDatabaseFacts{}, err
	}
	after, err := restoreDatabasePin(parent, database)
	if err != nil || !snapshotSourceSame(before, after) {
		return restoreDatabaseFacts{}, ErrRestoreInvalid
	}
	return facts, nil
}

func restoreDatabaseCheck(ctx context.Context, database *os.File, key []byte, size int64, limits LocalSnapshotLimits) (facts restoreDatabaseFacts, returnedError error) {
	pool, err := sql.Open("sqlite3", snapshotAddress("/proc/self/fd/7", "ro", "mmap_size(0)", "temp_store(MEMORY)", "query_only(1)", "trusted_schema(0)", "busy_timeout(0)")+"&immutable=1")
	if err != nil {
		return facts, ErrRestoreInvalid
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	defer func() {
		if pool.Close() != nil {
			facts = restoreDatabaseFacts{}
			returnedError = ErrRestoreUncertain
		}
	}()
	var readOnly int
	if pool.QueryRowContext(ctx, "PRAGMA query_only").Scan(&readOnly) != nil || readOnly != 1 {
		return facts, ErrRestoreInvalid
	}
	var pages, pageSize int64
	if pool.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize) != nil || pool.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages) != nil || pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 || pages < 1 || pages > limits.MaxDBBytes/pageSize || pages*pageSize != size {
		return facts, ErrRestoreInvalid
	}
	facts.SchemaHash, err = snapshotSchema(ctx, pool)
	if err != nil {
		return facts, err
	}
	var integrity string
	if pool.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity) != nil || integrity != "ok" {
		return facts, ErrRestoreInvalid
	}
	rows, err := pool.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return facts, ErrRestoreInvalid
	}
	invalid := rows.Next()
	readError, closeError := rows.Err(), rows.Close()
	if closeError != nil {
		return facts, ErrRestoreUncertain
	}
	if invalid || readError != nil {
		return facts, ErrRestoreInvalid
	}
	if err := restoreDatabaseIDDomains(ctx, pool); err != nil {
		return facts, err
	}
	counts, referenceBytes, referenceHash, err := snapshotWriteInventory(ctx, pool, io.Discard, limits)
	if err != nil {
		return facts, err
	}
	facts.ReferenceBytes, facts.ReferenceHash, facts.Records = referenceBytes, referenceHash, counts.total
	facts.Counts = [4]uint64{counts.repositories, counts.skills, counts.packs, counts.items}
	block, err := aes.NewCipher(key)
	if err != nil {
		return facts, ErrRestoreKey
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return facts, ErrRestoreKey
	}
	keyHash := sha256.Sum256(key)
	facts.KeyID = base64.RawURLEncoding.EncodeToString(keyHash[:])
	facts.Policy, err = restoreDatabaseProofs(ctx, pool, aead, facts.KeyID)
	if err != nil {
		return facts, err
	}
	configurationAEAD, err := newConfigurationAEAD(key)
	if err != nil {
		return facts, ErrRestoreKey
	}
	if err := restoreDatabaseCiphertexts(ctx, pool, aead, configurationAEAD, facts.KeyID, limits.MaxRecords); err != nil {
		return facts, err
	}
	if err := restoreDatabaseAssets(ctx, pool, limits.MaxRecords); err != nil {
		return facts, err
	}
	facts.DatabaseBytes = size
	facts.DatabaseHash, err = restoreDatabaseHash(ctx, database, size)
	if err != nil {
		return facts, err
	}
	return facts, ctx.Err()
}

func restoreDatabaseIDDomains(ctx context.Context, pool *sql.DB) error {
	for _, domain := range []string{
		"users WHERE id<=0",
		"collected_repositories WHERE id<=0 OR user_id<=0",
		"collected_skills WHERE id<=0 OR repository_id<=0",
		"packs WHERE id<=0 OR user_id<=0",
		"pack_items WHERE id<=0 OR pack_id<=0 OR skill_id<=0",
		"categories WHERE id<=0 OR user_id<=0",
		"category_skills WHERE category_id<=0 OR collected_skill_id<=0",
		"git_hub_credentials WHERE id<=0 OR user_id<=0",
		"configuration_assets WHERE id<=0 OR user_id<=0",
	} {
		var invalid bool
		if err := pool.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+domain+")").Scan(&invalid); err != nil || invalid {
			return ErrRestoreInvalid
		}
	}
	return nil
}

func restoreDatabaseProofs(ctx context.Context, pool *sql.DB, aead cipher.AEAD, keyID string) (int64, error) {
	proof := func(name string) (string, []byte, error) {
		var actual string
		var encoded []byte
		err := pool.QueryRowContext(ctx, "SELECT "+snapshotText("key_id", 64)+",CASE WHEN length(key_proof) BETWEEN 1 AND 512 THEN key_proof END FROM server_metadata WHERE name=?", name).Scan(&actual, &encoded)
		return actual, encoded, err
	}
	actual, encoded, err := proof(keyMetadataName)
	if err != nil || verifyStoredKeyProof(aead, keyID, actual, encoded) != nil {
		clear(encoded)
		return 0, ErrRestoreKey
	}
	clear(encoded)
	actual, encoded, err = proof(observationMetadataName)
	defer clear(encoded)
	if err != nil || actual != keyID {
		return 0, ErrRestoreKey
	}
	policy, err := aead.Open(nil, nil, encoded, []byte(observationMetadataName+":"+keyID))
	defer clear(policy)
	if err != nil {
		return 0, ErrRestoreKey
	}
	duration, err := strconv.ParseInt(string(policy), 10, 64)
	if err != nil || duration <= 0 || duration > int64(24*time.Hour) || strconv.FormatInt(duration, 10) != string(policy) || verifyStoredObservationProof(aead, keyID, actual, encoded, policy) != nil {
		return 0, ErrRestoreInvalid
	}
	return duration, nil
}

func restoreDatabaseCiphertexts(ctx context.Context, pool *sql.DB, githubAEAD, configurationAEAD cipher.AEAD, keyID string, maximum uint64) error {
	var seen uint64
	for _, kind := range []string{"github", "configuration"} {
		var after int
		for {
			query := "SELECT c.id," + snapshotText("u.user_key", 256) + "," + snapshotText("c.viewer_id", 256) + "," + snapshotText("c.client_id", 256) + "," + snapshotText("c.key_id", 64) + "," + snapshotText("c.generation", 64) + ",CASE WHEN length(c.ciphertext) BETWEEN 1 AND 32768 THEN c.ciphertext END FROM git_hub_credentials c JOIN users u ON u.id=c.user_id WHERE c.id>? ORDER BY c.id LIMIT 128"
			if kind == "configuration" {
				query = "SELECT c.id," + snapshotText("u.user_key", 256) + ",c.user_id," + snapshotText("c.public_id", 256) + "," + snapshotText("c.agent_id", 64) + "," + snapshotText("c.kind", 64) + "," + snapshotText("c.name", 128) + ",c.schema_version,c.revision,CASE WHEN length(c.ciphertext) BETWEEN 1 AND " + strconv.Itoa(MaxConfigurationPayloadBytes+configurationAEAD.Overhead()) + " THEN c.ciphertext END FROM configuration_assets c JOIN users u ON u.id=c.user_id WHERE c.id>? ORDER BY c.id LIMIT 128"
			}
			rows, err := pool.QueryContext(ctx, query, after)
			if err != nil {
				return ErrRestoreInvalid
			}
			count, next, checkError := restoreDatabaseCipherPage(ctx, rows, kind, githubAEAD, configurationAEAD, keyID, after, &seen, maximum)
			closeError := rows.Close()
			if closeError != nil {
				return ErrRestoreUncertain
			}
			if checkError != nil {
				return checkError
			}
			if count < 128 {
				break
			}
			after = next
		}
	}
	return nil
}

func restoreDatabaseCipherPage(ctx context.Context, rows *sql.Rows, kind string, githubAEAD, configurationAEAD cipher.AEAD, keyID string, after int, seen *uint64, maximum uint64) (int, int, error) {
	count := 0
	for rows.Next() {
		if ctx.Err() != nil {
			return count, after, ctx.Err()
		}
		if *seen >= maximum || count >= 128 {
			return count, after, ErrRestoreBudget
		}
		var owner string
		var identity int
		var err error
		if kind == "github" {
			var credential ent.GitHubCredential
			err = rows.Scan(&credential.ID, &owner, &credential.ViewerID, &credential.ClientID, &credential.KeyID, &credential.Generation, &credential.Ciphertext)
			identity = credential.ID
			if err == nil {
				err = restoreDatabaseGitHub(githubAEAD, keyID, owner, &credential)
			}
			clear(credential.Ciphertext)
		} else {
			var entry ent.ConfigurationAsset
			err = rows.Scan(&entry.ID, &owner, &entry.UserID, &entry.PublicID, &entry.AgentID, &entry.Kind, &entry.Name, &entry.SchemaVersion, &entry.Revision, &entry.Ciphertext)
			identity = entry.ID
			if err == nil && (len(entry.Ciphertext) < configurationAEAD.Overhead()+1 || len(entry.Ciphertext) > MaxConfigurationPayloadBytes+configurationAEAD.Overhead()) {
				err = ErrRestoreInvalid
			}
			if err == nil {
				var plaintext []byte
				plaintext, err = configurationAEAD.Open(nil, nil, entry.Ciphertext, configurationAAD(owner, &entry))
				clear(plaintext)
			}
			clear(entry.Ciphertext)
		}
		if err != nil || identity <= after {
			return count, after, ErrRestoreInvalid
		}
		after = identity
		count++
		*seen++
	}
	if rows.Err() != nil {
		return count, after, ErrRestoreInvalid
	}
	return count, after, nil
}

func restoreDatabaseGitHub(aead cipher.AEAD, keyID, owner string, credential *ent.GitHubCredential) error {
	if credential.KeyID != keyID || len(credential.Ciphertext) < 2 || credential.Ciphertext[0] != 1 {
		return ErrRestoreInvalid
	}
	aad, err := credentialAAD(owner, credential.ViewerID, credential.ClientID, credential.KeyID, credential.Generation)
	if err != nil {
		return ErrRestoreInvalid
	}
	material, err := aead.Open(nil, nil, credential.Ciphertext[1:], aad)
	defer clear(material)
	if err != nil {
		return ErrRestoreInvalid
	}
	token, err := github.DecodeStoredToken(material)
	if err != nil || token.ClientID() != credential.ClientID {
		return ErrRestoreInvalid
	}
	return nil
}
