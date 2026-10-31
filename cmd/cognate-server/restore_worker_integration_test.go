//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/state"
	"golang.org/x/sys/unix"
)

func TestRestoreDatabaseActualServerWorker(test *testing.T) {
	binary := filepath.Join(test.TempDir(), "cognate-server")
	build := exec.CommandContext(test.Context(), "go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		test.Fatalf("server fixture build: %v %s", err, output)
	}
	binaryBytes, err := os.ReadFile(binary)
	if err != nil {
		test.Fatal(err)
	}
	test.Logf("actual server fixture SHA256=%x", sha256.Sum256(binaryBytes))
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "server.db")
	key := bytes.Repeat([]byte{91}, 32)
	store, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	if err := store.CreateUser(test.Context(), "restore-actual-owner"); err != nil {
		test.Fatal(err)
	}
	if _, err := store.CreateConfigurationAsset(test.Context(), "restore-actual-owner", state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, Name: "actual-worker", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte("actual-worker-private-sentinel"))}); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	parent, err := os.Open(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer parent.Close()
	database, err := os.Open(path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	var parentInfo, databaseInfo unix.Stat_t
	if unix.Fstat(int(parent.Fd()), &parentInfo) != nil || unix.Fstat(int(database.Fd()), &databaseInfo) != nil {
		test.Fatal("fixture identity")
	}
	type identity struct{ Device, Inode uint64 }
	request := struct {
		Nonce            [16]byte
		Parent, Database identity
		Limits           state.LocalSnapshotLimits
	}{Nonce: [16]byte{1}, Parent: identity{Device: uint64(parentInfo.Dev), Inode: parentInfo.Ino}, Database: identity{Device: uint64(databaseInfo.Dev), Inode: databaseInfo.Ino}, Limits: state.LocalSnapshotLimits{MaxDBBytes: 8 << 20, MaxInventoryBytes: 4 << 20, MaxStagingBytes: 64 << 20, MaxRecords: 100000, SQLiteHeapBytes: 32 << 20, WorkerAddressSpaceBytes: 16 << 30, WorkerCPUSeconds: 10, MaxWALBytes: 64 << 20, MaxSHMBytes: 1 << 20, OperationTimeout: 10 * time.Second, DrainTimeout: time.Second}}
	encoded, err := json.Marshal(request, json.WithMarshalers(json.MarshalFunc(func(duration time.Duration) ([]byte, error) {
		return []byte(strconv.FormatInt(int64(duration), 10)), nil
	})))
	if err != nil {
		test.Fatal(err)
	}
	encoded = append(encoded, '\n')
	for _, scenario := range []string{"valid", "wrong-image", "short-key", "trailing-request", "named-key", "cancel"} {
		test.Run(scenario, func(test *testing.T) {
			imagePath := binary
			if scenario == "wrong-image" {
				imagePath = "/proc/self/exe"
			}
			image, err := os.Open(imagePath)
			if err != nil {
				test.Fatal(err)
			}
			defer image.Close()
			input, inputWriter, err := os.Pipe()
			if err != nil {
				test.Fatal(err)
			}
			defer input.Close()
			output, outputWriter, err := os.Pipe()
			if err != nil {
				test.Fatal(err)
			}
			defer output.Close()
			keyReader, keyWriter, err := os.Pipe()
			if err != nil {
				test.Fatal(err)
			}
			if scenario == "named-key" {
				if err := errors.Join(keyReader.Close(), keyWriter.Close()); err != nil {
					test.Fatal(err)
				}
				fifoPath := filepath.Join(test.TempDir(), "key-fifo")
				if err := unix.Mkfifo(fifoPath, 0600); err != nil {
					test.Fatal(err)
				}
				readerFD, err := unix.Open(fifoPath, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
				if err != nil {
					test.Fatal(err)
				}
				keyReader = os.NewFile(uintptr(readerFD), "named key reader")
				writerFD, err := unix.Open(fifoPath, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
				if err != nil {
					_ = keyReader.Close()
					test.Fatal(err)
				}
				keyWriter = os.NewFile(uintptr(writerFD), "named key writer")
			}
			defer keyReader.Close()
			payload := bytes.Clone(encoded)
			if scenario == "trailing-request" {
				payload = append(payload, 'x')
			}
			if _, err := inputWriter.Write(payload); err != nil {
				test.Fatal(err)
			}
			if err := inputWriter.Close(); err != nil {
				test.Fatal(err)
			}
			keyBytes := key
			if scenario == "short-key" {
				keyBytes = key[:31]
			}
			if scenario != "cancel" {
				if _, err := keyWriter.Write(keyBytes); err != nil {
					test.Fatal(err)
				}
				if err := keyWriter.Close(); err != nil {
					test.Fatal(err)
				}
			}
			defer keyWriter.Close()
			command := exec.CommandContext(test.Context(), binary, "--cognate-restore-database-worker-v1")
			command.Env = []string{"LANG=C", "LC_ALL=C"}
			command.ExtraFiles = []*os.File{input, outputWriter, image, parent, database, keyReader}
			var diagnostics bytes.Buffer
			command.Stdout, command.Stderr = &diagnostics, &diagnostics
			if err := command.Start(); err != nil {
				test.Fatal(err)
			}
			if err := outputWriter.Close(); err != nil {
				test.Fatal(err)
			}
			if scenario == "cancel" {
				if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
					test.Fatal(err)
				}
			}
			responseBytes, readError := io.ReadAll(io.LimitReader(output, 16385))
			waitError := command.Wait()
			if readError != nil || len(responseBytes) > 16384 {
				test.Fatal("unbounded or unreadable response")
			}
			var response struct {
				Nonce  [16]byte
				Status string
				Facts  struct {
					DatabaseBytes            int64
					DatabaseHash, SchemaHash [32]byte
					KeyID                    string
					ReferenceBytes           int64
					ReferenceHash            [32]byte
					Records                  uint64
					Counts                   [4]uint64
					Policy                   int64
				}
			}
			decodeError := json.Unmarshal(responseBytes, &response)
			if scenario == "valid" {
				if waitError != nil || decodeError != nil || response.Status != "ok" {
					test.Fatalf("actual worker refused: wait=%v decode=%v status=%s", waitError, decodeError, response.Status)
				}
				content, err := os.ReadFile(path)
				if err != nil || response.Facts.DatabaseHash != sha256.Sum256(content) || response.Facts.DatabaseBytes != int64(len(content)) || response.Nonce != request.Nonce {
					test.Fatal("actual worker evidence differs")
				}
			} else if waitError == nil || decodeError == nil && response.Status == "ok" {
				test.Fatal("unsafe actual worker invocation admitted")
			}
			if diagnostics.Len() != 0 || strings.Contains(string(responseBytes), "actual-worker-private-sentinel") {
				test.Fatal("worker diagnostics disclosed private input")
			}
			if _, err := database.Stat(); err != nil {
				test.Fatal("borrowed actual database closed")
			}
		})
	}
	for _, args := range [][]string{{"--cognate-restore-database-worker-v1"}, {"--cognate-restore-database-worker-v1", "extra"}} {
		command := exec.CommandContext(test.Context(), binary, args...)
		if output, err := command.CombinedOutput(); err == nil || len(output) != 0 {
			test.Fatal("bare hidden flag was accepted or printed diagnostics")
		}
	}
}
