//go:build linux && amd64

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/server/state"
	"golang.org/x/sys/unix"
)

type revokeWireIdentity struct{ Device, Inode uint64 }
type revokeWireDatabase struct {
	DatabaseBytes            int64
	DatabaseHash, SchemaHash [32]byte
	KeyID                    string
	ReferenceBytes           int64
	ReferenceHash            [32]byte
	Records                  uint64
	Counts                   [4]uint64
	Policy                   int64
}
type revokeWireFacts struct {
	Database  revokeWireDatabase
	Protected [32]byte
}
type revokeWireRequest struct {
	Nonce                        [16]byte
	Mode                         string
	Operation                    [16]byte
	RevokedAt                    time.Time
	Root, Lock, Parent, Database revokeWireIdentity
	Before                       revokeWireDatabase
	Protected                    [32]byte
	Limits                       state.LocalSnapshotLimits
}
type revokeWireResponse struct {
	Nonce  [16]byte
	Status string
	Facts  revokeWireFacts
}

func TestRestoreRevokeWorkerActualExecutable(test *testing.T) {
	binary := filepath.Join(test.TempDir(), "cognate-server")
	build := exec.CommandContext(test.Context(), "go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		test.Fatalf("build prerequisite: %s", output)
	}
	imageBytes, err := os.ReadFile(binary)
	if err != nil {
		test.Fatal(err)
	}
	imageHash := sha256.Sum256(imageBytes)
	clear(imageBytes)
	test.Log("actual_binary_sha256=" + hex.EncodeToString(imageHash[:]))
	seed := test.TempDir()
	if err := os.Chmod(seed, 0700); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{81}, 32)
	store, err := state.Open(test.Context(), filepath.Join(seed, "seed.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	if err := store.CreateUser(test.Context(), "t4-synthetic-owner"); err != nil {
		test.Fatal(err)
	}
	token, err := github.NewToken("t4-synthetic-token", []string{"repo"})
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.PutCredential(test.Context(), "t4-synthetic-owner", "t4-viewer", "", token); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(seed, "seed.db"))
	if err != nil {
		test.Fatal(err)
	}
	rootPath := test.TempDir()
	if err := os.Chmod(rootPath, 0700); err != nil {
		test.Fatal(err)
	}
	parentPath := filepath.Join(rootPath, ".restore-01000000000000000000000000000000", "candidate")
	if err := os.MkdirAll(parentPath, 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentPath, "server.db"), data, 0600); err != nil {
		test.Fatal(err)
	}
	parent, err := os.Open(parentPath)
	if err != nil {
		test.Fatal(err)
	}
	defer parent.Close()
	database, err := os.Open(filepath.Join(parentPath, "server.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	root, err := runtimegen.LockOffline(test.Context(), rootPath)
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	limits := state.LocalSnapshotLimits{MaxDBBytes: 8 << 20, MaxInventoryBytes: 4 << 20, MaxStagingBytes: 64 << 20, MaxRecords: 100000, SQLiteHeapBytes: 32 << 20, WorkerAddressSpaceBytes: 16 << 30, WorkerCPUSeconds: 10, MaxWALBytes: 64 << 20, MaxSHMBytes: 1 << 20, OperationTimeout: 10 * time.Second, DrainTimeout: time.Second}
	err = root.WithWorkerFiles(test.Context(), func(rootFile, lock *os.File) error {
		request := revokeWireRequest{Nonce: [16]byte{1}, Mode: "inspect", Limits: limits}
		for index, file := range []*os.File{rootFile, lock, parent, database} {
			var stat unix.Stat_t
			if unix.Fstat(int(file.Fd()), &stat) != nil {
				return errors.New("identity class")
			}
			identity := revokeWireIdentity{uint64(stat.Dev), stat.Ino}
			switch index {
			case 0:
				request.Root = identity
			case 1:
				request.Lock = identity
			case 2:
				request.Parent = identity
			case 3:
				request.Database = identity
			}
		}
		before, err := revokeActualWorker(test.Context(), binary, rootFile, lock, parent, database, key, request)
		if err != nil || before.Status != "ok" {
			return errors.New("actual executable inspect refused")
		}
		socket, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			return err
		}
		socketFile := os.NewFile(uintptr(socket), "synthetic inherited socket")
		defer socketFile.Close()
		extras := make([]*os.File, 1014)
		extras[len(extras)-1] = socketFile
		inherited, err := revokeActualWorker(test.Context(), binary, rootFile, lock, parent, database, key, request, extras...)
		if err == nil || inherited.Status == "ok" {
			return errors.New("high inherited socket accepted")
		}
		wrong, err := revokeActualWorker(test.Context(), binary, rootFile, lock, parent, database, bytes.Repeat([]byte{82}, 32), request)
		if err == nil || wrong.Status != "key" {
			return errors.New("actual executable wrong original key not refused")
		}
		request.Mode, request.Before, request.Operation, request.RevokedAt = "revoke", before.Facts.Database, [16]byte{1}, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
		after, err := revokeActualWorker(test.Context(), binary, rootFile, lock, parent, database, key, request)
		if err != nil || after.Status != "ok" || after.Facts.Protected == [32]byte{} || after.Facts.Database.DatabaseHash == before.Facts.Database.DatabaseHash {
			return errors.New("actual executable revocation refused")
		}
		request.Mode, request.Before, request.Protected = "postinspect", after.Facts.Database, after.Facts.Protected
		post, err := revokeActualWorker(test.Context(), binary, rootFile, lock, parent, database, key, request)
		if err != nil || post.Status != "ok" || post.Facts != after.Facts {
			return errors.New("actual executable independent postinspection refused")
		}
		return nil
	})
	if err != nil {
		test.Fatal(err)
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(database.Name() + suffix); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("sidecar survived actual worker")
		}
	}
}

func revokeActualWorker(ctx context.Context, binary string, root, lock, parent, database *os.File, key []byte, request revokeWireRequest, extras ...*os.File) (response revokeWireResponse, returnedError error) {
	input, inputWriter, err := os.Pipe()
	if err != nil {
		return response, err
	}
	defer input.Close()
	output, outputWriter, err := os.Pipe()
	if err != nil {
		return response, err
	}
	defer output.Close()
	keyPipe, keyWriter, err := os.Pipe()
	if err != nil {
		return response, err
	}
	defer keyPipe.Close()
	image, err := os.Open(binary)
	if err != nil {
		return response, err
	}
	defer image.Close()
	quiet, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return response, err
	}
	defer quiet.Close()
	frame, err := json.Marshal(request, json.WithMarshalers(json.MarshalFunc(func(duration time.Duration) ([]byte, error) {
		return []byte(strconv.FormatInt(int64(duration), 10)), nil
	})))
	if err != nil {
		return response, err
	}
	if _, err = inputWriter.Write(append(frame, '\n')); err != nil {
		return response, err
	}
	if err = inputWriter.Close(); err != nil {
		return response, err
	}
	if _, err = keyWriter.Write(key); err != nil {
		return response, err
	}
	if err = keyWriter.Close(); err != nil {
		return response, err
	}
	command := exec.CommandContext(ctx, "/proc/self/fd/5", "--cognate-restore-revoke-worker-v1")
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	command.Stdin, command.Stdout, command.Stderr = quiet, quiet, quiet
	command.ExtraFiles = []*os.File{input, outputWriter, image, parent, database, keyPipe, lock, root}
	command.ExtraFiles = append(command.ExtraFiles, extras...)
	if err = command.Start(); err != nil {
		return response, err
	}
	if err = outputWriter.Close(); err != nil {
		return response, err
	}
	payload, readError := io.ReadAll(io.LimitReader(output, 16385))
	waitError := command.Wait()
	if readError != nil || len(payload) > 16384 || json.Unmarshal(payload, &response, json.RejectUnknownMembers(true)) != nil {
		return response, errors.New("bounded result class")
	}
	return response, waitError
}
