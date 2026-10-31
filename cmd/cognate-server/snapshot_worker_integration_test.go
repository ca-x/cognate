//go:build linux

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/state"
	"golang.org/x/sys/unix"
)

func TestSnapshotActualBinaries(test *testing.T) {
	for _, commandName := range []string{"cognate", "cognate-server"} {
		test.Run(commandName, func(test *testing.T) {
			binaryPath := filepath.Join(test.TempDir(), commandName)
			build := exec.CommandContext(test.Context(), "go", "build", "-o", binaryPath, "../"+commandName)
			if output, err := build.CombinedOutput(); err != nil {
				test.Fatalf("build prerequisite failed: %s", output)
			}
			for _, pageSize := range []int{512, 1024, 2048, 4096, 8192, 16384, 32768, 65536} {
				test.Run(fmt.Sprint(pageSize), func(test *testing.T) {
					scenarios := []string{"valid"}
					if pageSize == 512 {
						scenarios = append(scenarios, "R553")
					}
					if pageSize == 4096 {
						scenarios = append(scenarios, "database-budget", "wal-budget", "bad-header", "unknown-schema", "short-request", "trailing-request", "zero-nonce", "regular-request")
					}
					for _, scenario := range scenarios {
						test.Run(scenario, func(test *testing.T) {
							directory := test.TempDir()
							if err := os.Chmod(directory, 0700); err != nil {
								test.Fatal(err)
							}
							path := filepath.Join(directory, "source.sqlite")
							empty, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
							if err != nil {
								test.Fatal(err)
							}
							if err = empty.Close(); err != nil {
								test.Fatal(err)
							}
							seed, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path}).String())
							if err != nil {
								test.Fatal(err)
							}
							if _, err = seed.ExecContext(test.Context(), fmt.Sprintf("PRAGMA page_size=%d; VACUUM", pageSize)); err != nil {
								test.Fatal(err)
							}
							if err = seed.Close(); err != nil {
								test.Fatal(err)
							}
							store, err := state.Open(test.Context(), path, bytes.Repeat([]byte{91}, 32))
							if err != nil {
								test.Fatal(err)
							}
							defer store.Close()
							if err = store.CreateUser(test.Context(), "snapshot-task0-owner"); err != nil {
								test.Fatal(err)
							}
							if scenario == "unknown-schema" {
								foreign, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path}).String())
								if err != nil {
									test.Fatal(err)
								}
								if _, err = foreign.ExecContext(test.Context(), "CREATE TABLE unapproved_source(value TEXT)"); err != nil {
									test.Fatal(err)
								}
								if err = foreign.Close(); err != nil {
									test.Fatal(err)
								}
							}
							if scenario == "bad-header" {
								file, err := os.OpenFile(path, os.O_RDWR, 0)
								if err != nil {
									test.Fatal(err)
								}
								if _, err = file.WriteAt([]byte("not a sqlite db!\x00"), 0); err != nil {
									test.Fatal(err)
								}
								if err = file.Close(); err != nil {
									test.Fatal(err)
								}
							}
							if scenario == "R553" {
								if err = os.Truncate(path+"-shm", 553*32768); err != nil {
									test.Fatal(err)
								}
							}
							wal, err := os.Stat(path + "-wal")
							if err != nil || wal.Size() <= 32 {
								test.Fatal("no committed WAL")
							}
							parent, err := os.Open(directory)
							if err != nil {
								test.Fatal(err)
							}
							defer parent.Close()
							source, err := os.Open(path)
							if err != nil {
								test.Fatal(err)
							}
							defer source.Close()
							stagePath := filepath.Join(directory, "snapshot-stage")
							if err = os.Mkdir(stagePath, 0700); err != nil {
								test.Fatal(err)
							}
							stage, err := os.Open(stagePath)
							if err != nil {
								test.Fatal(err)
							}
							defer stage.Close()
							fence, err := os.OpenFile(filepath.Join(directory, ".snapshot-worker.lock"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
							if err != nil {
								test.Fatal(err)
							}
							defer fence.Close()
							if err = unix.Flock(int(fence.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
								test.Fatal(err)
							}
							image, err := os.Open(binaryPath)
							if err != nil {
								test.Fatal(err)
							}
							defer image.Close()
							requestReader, requestWriter, err := os.Pipe()
							if err != nil {
								test.Fatal(err)
							}
							defer requestReader.Close()
							defer requestWriter.Close()
							responseReader, responseWriter, err := os.Pipe()
							if err != nil {
								test.Fatal(err)
							}
							defer responseReader.Close()
							defer responseWriter.Close()
							var request [448]byte
							copy(request[:8], "CGB2WR01")
							binary.BigEndian.PutUint16(request[8:10], 1)
							request[10] = 1
							binary.BigEndian.PutUint32(request[12:16], 448)
							copy(request[16:32], bytes.Repeat([]byte{11}, 16))
							for index, file := range []*os.File{source, parent, stage} {
								info, err := file.Stat()
								if err != nil {
									test.Fatal(err)
								}
								attributes := info.Sys().(*syscall.Stat_t)
								binary.BigEndian.PutUint64(request[32+index*16:], uint64(attributes.Dev))
								binary.BigEndian.PutUint64(request[40+index*16:], attributes.Ino)
							}
							for index, value := range []uint64{8 << 20, 4 << 20, 100000, 64 << 20, 32 << 20, 16 << 30, 10, 64 << 20, 1 << 20, uint64(10 * time.Second)} {
								binary.BigEndian.PutUint64(request[80+index*8:], value)
							}
							binary.BigEndian.PutUint16(request[160:162], uint16(len("source.sqlite")))
							copy(request[164:], "source.sqlite")
							if scenario == "database-budget" {
								binary.BigEndian.PutUint64(request[80:88], 1)
							}
							if scenario == "wal-budget" {
								binary.BigEndian.PutUint64(request[136:144], 32)
							}
							if scenario == "R553" {
								binary.BigEndian.PutUint64(request[144:152], 32<<20)
							}
							if scenario == "zero-nonce" {
								clear(request[16:32])
							}
							wire := request[:]
							if scenario == "short-request" {
								wire = wire[:len(wire)-1]
							}
							if scenario == "trailing-request" {
								wire = append(bytes.Clone(wire), 1)
							}
							if count, err := requestWriter.Write(wire); err != nil || count != len(wire) {
								test.Fatal("request write failed")
							}
							if err = requestWriter.Close(); err != nil {
								test.Fatal(err)
							}
							home := test.TempDir()
							ctx, cancel := context.WithTimeout(test.Context(), 20*time.Second)
							defer cancel()
							command := exec.CommandContext(ctx, "/proc/self/fd/5", "--cognate-local-snapshot-worker-v1")
							command.Env = []string{"HOME=" + home, "TMPDIR=" + home, "LANG=C", "LC_ALL=C"}
							requestInput := requestReader
							if scenario == "regular-request" {
								regular, err := os.CreateTemp(directory, "regular-request-")
								if err != nil {
									test.Fatal(err)
								}
								defer regular.Close()
								if _, err = regular.Write(wire); err != nil {
									test.Fatal(err)
								}
								if _, err = regular.Seek(0, io.SeekStart); err != nil {
									test.Fatal(err)
								}
								requestInput = regular
							}
							command.ExtraFiles = []*os.File{requestInput, responseWriter, image, parent, source, stage, fence}
							if err = command.Start(); err != nil {
								test.Fatal(err)
							}
							_ = responseWriter.Close()
							response, readErr := io.ReadAll(io.LimitReader(responseReader, 257))
							waitErr := command.Wait()
							if scenario != "valid" {
								if waitErr == nil || readErr != nil || len(response) > 256 || len(response) == 256 && response[11] == 0 {
									test.Fatal("invalid source or request produced success")
								}
								if _, err := os.Stat(filepath.Join(stagePath, "snapshot.sqlite")); !os.IsNotExist(err) {
									test.Fatal("pre-Backup refusal created a target")
								}
								return
							}
							if readErr != nil || waitErr != nil || len(response) != 256 || string(response[:8]) != "CGB2RS01" || response[11] != 0 {
								test.Fatalf("actual helper snapshot receipt refused: bytes=%d terminal=%t read=%t", len(response), waitErr == nil, readErr == nil)
							}
							if !bytes.Equal(response[16:32], request[16:32]) || binary.BigEndian.Uint64(response[32:40]) != 16<<30 || binary.BigEndian.Uint64(response[40:48]) != 32<<20 || binary.BigEndian.Uint64(response[64:72]) > 16<<30 {
								test.Fatal("resource receipt did not prove configured controls")
							}
							if binary.BigEndian.Uint64(response[80:88]) != uint64(pageSize) || binary.BigEndian.Uint64(response[72:80]) != binary.BigEndian.Uint64(response[88:96])*uint64(pageSize) {
								test.Fatal("actual page geometry differs from fixed transaction")
							}
							entries, err := os.ReadDir(home)
							if err != nil || len(entries) != 0 {
								test.Fatal("helper performed ordinary configuration/bootstrap effects")
							}
							pool, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: filepath.Join(stagePath, "snapshot.sqlite"), RawQuery: "mode=ro&immutable=1"}).String())
							if err != nil {
								test.Fatal(err)
							}
							defer pool.Close()
							var count int
							if err = pool.QueryRowContext(test.Context(), "SELECT count(*) FROM users WHERE user_key='snapshot-task0-owner'").Scan(&count); err != nil || count != 1 {
								test.Fatal("independent restored SQL lost WAL-only row")
							}
							var integrity string
							if err = pool.QueryRowContext(test.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
								test.Fatal("independent restored SQL integrity failed")
							}
							test.Log("actual CLI/server helper: resource receipt, committed WAL row, independent restore SQL and zero ordinary bootstrap")
						})
					}
				})
			}
		})
	}
}
