package logging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"encoding/json/v2"
	"github.com/ca-x/cognate/internal/filelock"
)

type faultFiles struct {
	fileOperations
	open   func(string, int, diskFile) diskFile
	rename func(string, string) error
	remove func(string) error
	close  func() error
}

func (files faultFiles) OpenFile(name string, flags int, mode os.FileMode) (diskFile, error) {
	file, err := files.fileOperations.OpenFile(name, flags, mode)
	if err == nil && files.open != nil {
		file = files.open(name, flags, file)
	}
	return file, err
}
func (files faultFiles) Rename(old, next string) error {
	if files.rename != nil {
		return files.rename(old, next)
	}
	return files.fileOperations.Rename(old, next)
}
func (files faultFiles) Remove(name string) error {
	if files.remove != nil {
		return files.remove(name)
	}
	return files.fileOperations.Remove(name)
}
func (files faultFiles) Close() error {
	if files.close != nil {
		return files.close()
	}
	return files.fileOperations.Close()
}

type faultFile struct {
	diskFile
	read     func([]byte) (int, error)
	write    func([]byte) (int, error)
	sync     func() error
	truncate func(int64) error
	close    func() error
}

func (file faultFile) Read(data []byte) (int, error) {
	if file.read != nil {
		return file.read(data)
	}
	return file.diskFile.Read(data)
}

func (file faultFile) Write(data []byte) (int, error) {
	if file.write != nil {
		return file.write(data)
	}
	return file.diskFile.Write(data)
}
func (file faultFile) Sync() error {
	if file.sync != nil {
		return file.sync()
	}
	return file.diskFile.Sync()
}
func (file faultFile) Truncate(size int64) error {
	if file.truncate != nil {
		return file.truncate(size)
	}
	return file.diskFile.Truncate(size)
}
func (file faultFile) Close() error {
	if file.close != nil {
		return file.close()
	}
	return file.diskFile.Close()
}

func seedSegment(test *testing.T, directory string, birth time.Time, data []byte) string {
	test.Helper()
	name, err := randomName(fmt.Sprintf("segment-%020d-", birth.UnixNano()), ".jsonl")
	if err != nil {
		test.Fatal("fixture random failed")
	}
	if err := os.WriteFile(filepath.Join(directory, "logs", name), data, 0o600); err != nil {
		test.Fatal("fixture segment failed")
	}
	return name
}

func segments(test *testing.T, directory string) []os.FileInfo {
	test.Helper()
	entries, err := os.ReadDir(filepath.Join(directory, "logs"))
	if err != nil {
		test.Fatal("cannot list segments")
	}
	var result []os.FileInfo
	for _, entry := range entries {
		if segmentPattern.MatchString(entry.Name()) || temporaryPattern.MatchString(entry.Name()) {
			info, err := entry.Info()
			if err != nil {
				test.Fatal("cannot inspect segment")
			}
			result = append(result, info)
		}
	}
	return result
}

func fullSegment() []byte {
	data := bytes.Repeat([]byte("{}\n"), ((1<<20)-4)/3)
	return append(data, []byte("{} \n")...)
}

func TestExactSizeRotationAndSmallerPolicy(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	defer manager.Close()
	settings := Settings{Level: "info", MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 14}
	if _, err := manager.Update(test.Context(), 1, settings); err != nil {
		test.Fatal("update failed")
	}
	oldest := seedSegment(test, directory, time.Now().Add(-3*time.Hour), fullSegment())
	seedSegment(test, directory, time.Now().Add(-2*time.Hour), fullSegment())
	seedSegment(test, directory, time.Now().Add(-time.Hour), fullSegment())
	if manager.Record(Event{Kind: EventServerStarted}) != nil {
		test.Fatal("rotation failed")
	}
	if _, err := os.Lstat(filepath.Join(directory, "logs", oldest)); !os.IsNotExist(err) {
		test.Fatal("rotation kept the oldest segment over budget")
	}
	var total int64
	for _, info := range segments(test, directory) {
		total += info.Size()
		if info.Size() > 1<<20 {
			test.Fatal("record crossed per-segment boundary")
		}
	}
	if total > 2<<20 || len(segments(test, directory)) != 2 {
		test.Fatal("rotation did not enforce count and byte limits")
	}
	settings.MaxFileMiB = 2
	if _, err := manager.Update(test.Context(), 2, settings); err != nil {
		test.Fatal("larger policy failed")
	}
	large := seedSegment(test, directory, time.Now(), append(fullSegment(), []byte("{}\n")...))
	settings.MaxFileMiB = 1
	if _, err := manager.Update(test.Context(), 3, settings); err != nil {
		test.Fatal("smaller policy failed")
	}
	if _, err := os.Lstat(filepath.Join(directory, "logs", large)); !os.IsNotExist(err) {
		test.Fatal("smaller policy acknowledged an oversized old segment")
	}
}

func TestShortWriteRollbackAndDamagedRestart(test *testing.T) {
	for _, rollbackFails := range []bool{false, true} {
		test.Run(strconv.FormatBool(rollbackFails), func(test *testing.T) {
			directory := privateDirectory(test)
			manager, err := Open(test.Context(), directory)
			if err != nil {
				test.Fatal("open failed")
			}
			original := manager.files
			manager.warningWriter = io.Discard
			if manager.Record(Event{Kind: EventServerStarted}) != nil {
				test.Fatal("initial record failed")
			}
			before := logBytes(test, directory)
			manager.files = faultFiles{fileOperations: original, open: func(name string, flags int, file diskFile) diskFile {
				if !strings.HasSuffix(name, ".jsonl") {
					return file
				}
				result := faultFile{diskFile: file, write: func(data []byte) (int, error) { return file.Write(data[:len(data)/2]) }}
				if rollbackFails {
					result.truncate = func(int64) error { return errors.New("synthetic-private-driver-failure") }
				}
				return result
			}}
			if !errors.Is(manager.Record(Event{Kind: EventServerStopped}), ErrUnavailable) {
				test.Fatal("short write was reported as accepted")
			}
			state, err := manager.Snapshot(test.Context())
			if err != nil || state.Health.WriteFailures != 1 || state.Health.DroppedRecords != 1 || !state.Health.Degraded {
				test.Fatal("short write was not visible in safe health")
			}
			if !rollbackFails && !bytes.Equal(before, logBytes(test, directory)) {
				test.Fatal("short write rollback changed prior complete records")
			}
			manager.files = original
			if manager.Close() != nil {
				test.Fatal("close failed")
			}
			restarted, err := Open(test.Context(), directory)
			if err != nil {
				test.Fatal("damaged restart failed")
			}
			defer restarted.Close()
			if restarted.Record(Event{Kind: EventServerStarted}) != nil {
				test.Fatal("record after damaged restart failed")
			}
			for _, line := range bytes.Split(bytes.TrimSpace(logBytes(test, directory)), []byte{'\n'}) {
				var value map[string]any
				if json.Unmarshal(line, &value) != nil {
					test.Fatal("damaged tail was appended after restart")
				}
			}
		})
	}
}

func TestUnsafeEntriesAndIdentityAreRejected(test *testing.T) {
	for _, mode := range []string{"symlink", "hardlink", "permissions", "directory", "root-replaced", "lock-replaced"} {
		test.Run(mode, func(test *testing.T) {
			directory := privateDirectory(test)
			manager, err := Open(test.Context(), directory)
			if err != nil {
				test.Fatal("open failed")
			}
			defer manager.Close()
			manager.warningWriter = io.Discard
			name := seedSegment(test, directory, time.Now(), []byte("{}\n"))
			path := filepath.Join(directory, "logs", name)
			original, _ := os.ReadFile(path)
			switch mode {
			case "symlink":
				target := filepath.Join(directory, "untouched")
				if os.Rename(path, target) != nil || os.Symlink(target, path) != nil {
					test.Fatal("symlink fixture failed")
				}
			case "hardlink":
				if os.Link(path, filepath.Join(directory, "hardlink")) != nil {
					test.Fatal("hardlink fixture failed")
				}
			case "permissions":
				if os.Chmod(path, 0o644) != nil {
					test.Fatal("permission fixture failed")
				}
			case "directory":
				if os.Remove(path) != nil || os.Mkdir(path, 0o700) != nil {
					test.Fatal("directory fixture failed")
				}
			case "root-replaced":
				if os.Rename(filepath.Join(directory, "logs"), filepath.Join(directory, "saved")) != nil || os.Mkdir(filepath.Join(directory, "logs"), 0o700) != nil {
					test.Fatal("root fixture failed")
				}
			case "lock-replaced":
				lock := filepath.Join(directory, "logs", ".logs.lock")
				if os.Rename(lock, lock+".saved") != nil || os.WriteFile(lock, nil, 0o600) != nil {
					test.Fatal("lock fixture failed")
				}
			}
			if !errors.Is(manager.Record(Event{Kind: EventServerStarted}), ErrUnavailable) {
				test.Fatal("unsafe resource accepted a write")
			}
			if mode == "symlink" {
				after, err := os.ReadFile(filepath.Join(directory, "untouched"))
				if err != nil || !bytes.Equal(original, after) {
					test.Fatal("unsafe target was modified")
				}
			}
		})
	}
}

func TestLockBudgetAndFailureWarning(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	defer manager.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		test.Fatal("warning pipe setup failed")
	}
	defer reader.Close()
	defer writer.Close()
	manager.warningWriter = writer
	lock, err := filelock.Acquire(test.Context(), filepath.Join(directory, "logs", ".logs.lock"))
	if err != nil {
		test.Fatal("fixture lock failed")
	}
	for range 2 {
		if !errors.Is(manager.Record(Event{Kind: EventServerStarted}), ErrUnavailable) {
			test.Fatal("blocked record bypassed the fixed lock budget")
		}
	}
	if lock.Close() != nil {
		test.Fatal("fixture unlock failed")
	}
	if writer.Close() != nil {
		test.Fatal("warning pipe close failed")
	}
	warnings, err := io.ReadAll(reader)
	if err != nil || string(warnings) != "cognate: operational logging unavailable\n" {
		test.Fatal("warning was not fixed and rate limited")
	}
	state, err := manager.Snapshot(test.Context())
	if err != nil || state.Health.WriteFailures != 2 || state.Health.DroppedRecords != 2 || !state.Health.Degraded {
		test.Fatal("blocked writes were not counted")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if result, err := manager.Snapshot(ctx); !errors.Is(err, ErrUnavailable) || result != (Snapshot{}) {
		test.Fatal("canceled snapshot returned partial data")
	}
}

func TestFileCloseFailureKeepsUnknownResource(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	manager.warningWriter = io.Discard
	original := manager.files
	var retained diskFile
	manager.files = faultFiles{fileOperations: original, open: func(name string, flags int, file diskFile) diskFile {
		if !strings.HasSuffix(name, ".jsonl") {
			return file
		}
		retained = file
		return faultFile{diskFile: file, close: func() error { return errors.New("synthetic-close-still-live") }}
	}}
	if manager.Record(Event{Kind: EventServerStarted}) != ErrUnavailable {
		test.Fatal("unknown file close was acknowledged")
	}
	if retained == nil || !errors.Is(manager.Record(Event{Kind: EventServerStopped}), ErrClosed) {
		test.Fatal("unknown close did not reject new work")
	}
	if manager.Close() != ErrUnavailable || manager.Close() != ErrUnavailable {
		test.Fatal("unknown resource was falsely recovered")
	}
	if _, err := retained.Write([]byte("physically-live")); err != nil {
		test.Fatal("fixture secretly closed the unknown resource")
	}
	_ = retained.Close()
}

func TestTailReadFailureDoesNotDeleteValidHistory(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	defer manager.Close()
	manager.warningWriter = io.Discard
	if manager.Record(Event{Kind: EventServerStarted}) != nil {
		test.Fatal("initial record failed")
	}
	before := logBytes(test, directory)
	original := manager.files
	manager.files = faultFiles{fileOperations: original, open: func(name string, flags int, file diskFile) diskFile {
		if !strings.HasSuffix(name, ".jsonl") {
			return file
		}
		return faultFile{diskFile: file, read: func([]byte) (int, error) { return 0, errors.New("synthetic-tail-read-failure") }}
	}}
	if manager.Record(Event{Kind: EventServerStopped}) != ErrUnavailable {
		test.Fatal("I/O failure was mistaken for a structurally damaged tail")
	}
	if !bytes.Equal(before, logBytes(test, directory)) {
		test.Fatal("transient read failure deleted valid log history")
	}
	manager.files = original
}

func TestRotationCountAndAge(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	defer manager.Close()
	settings := Settings{Level: "info", MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 1}
	if _, err := manager.Update(test.Context(), 1, settings); err != nil {
		test.Fatal("update failed")
	}
	old, err := randomName("segment-"+strings.Repeat("0", 20)+"-", ".jsonl")
	if err != nil {
		test.Fatal("fixture name failed")
	}
	oldPath := filepath.Join(directory, "logs", old)
	if err := os.WriteFile(oldPath, []byte("{}\n"), 0o600); err != nil {
		test.Fatal("fixture write failed")
	}
	if err := os.Chtimes(oldPath, time.Now(), time.Now()); err != nil {
		test.Fatal("fixture timestamp failed")
	}
	for range 5 {
		if manager.Record(Event{Kind: EventServerStarted}) != nil {
			test.Fatal("record failed")
		}
	}
	if _, err := os.Lstat(oldPath); !os.IsNotExist(err) {
		test.Fatal("creation age was replaced by append mtime")
	}
	entries, err := os.ReadDir(filepath.Join(directory, "logs"))
	if err != nil {
		test.Fatal("cannot inspect logs")
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			count++
		}
	}
	if count > 2 {
		test.Fatal("segment count exceeds persisted retention limit")
	}
}
