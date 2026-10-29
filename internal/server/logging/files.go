package logging

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"encoding/json/v2"

	"github.com/ca-x/cognate/internal/filelock"
)

type diskFile interface {
	io.Reader
	io.Writer
	io.Seeker
	Stat() (os.FileInfo, error)
	ReadDir(int) ([]fs.DirEntry, error)
	Truncate(int64) error
	Sync() error
	Close() error
}

type fileOperations interface {
	Lstat(string) (os.FileInfo, error)
	OpenFile(string, int, os.FileMode) (diskFile, error)
	Remove(string) error
	Rename(string, string) error
	Close() error
}

type rootFiles struct{ root *os.Root }

func (files rootFiles) Lstat(name string) (os.FileInfo, error) { return files.root.Lstat(name) }
func (files rootFiles) OpenFile(name string, flags int, mode os.FileMode) (diskFile, error) {
	return files.root.OpenFile(name, privateOpenFlags(flags), mode)
}
func (files rootFiles) Remove(name string) error      { return files.root.Remove(name) }
func (files rootFiles) Rename(old, next string) error { return files.root.Rename(old, next) }
func (files rootFiles) Close() error                  { return files.root.Close() }

func (manager *Manager) verifyRoot() error {
	if !trustedLoggingPath(manager.directory) || !trustedLoggingPath(manager.dataDirectory) {
		return ErrUnavailable
	}
	for _, entry := range []struct {
		path     string
		identity os.FileInfo
	}{{manager.directory, manager.identity}, {manager.dataDirectory, manager.dataIdentity}} {
		info, err := os.Lstat(entry.path)
		if err != nil || !os.SameFile(info, entry.identity) {
			return ErrUnavailable
		}
	}
	return nil
}

func (manager *Manager) withLock(ctx context.Context, operation func(context.Context) (Settings, error)) (result Settings, resultError error) {
	ctx, cancel := context.WithTimeout(ctx, lockBudget)
	stop := context.AfterFunc(manager.life, cancel)
	defer func() { stop(); cancel() }()
	if ctx.Err() != nil || manager.verifyRoot() != nil {
		return Settings{}, ErrUnavailable
	}
	before, err := manager.files.Lstat(".logs.lock")
	if err == nil && (!validPrivateFileInfo(before) || manager.lockIdentity != nil && !os.SameFile(before, manager.lockIdentity)) {
		return Settings{}, ErrUnavailable
	}
	if err != nil && (!errors.Is(err, fs.ErrNotExist) || manager.lockIdentity != nil) {
		return Settings{}, ErrUnavailable
	}
	lock, err := filelock.Acquire(ctx, filepath.Join(manager.directory, ".logs.lock"))
	if err != nil {
		return Settings{}, ErrUnavailable
	}
	defer func() {
		if manager.closeFile(lock) != nil {
			result = Settings{}
			resultError = ErrUnavailable
		}
	}()
	info, err := manager.files.Lstat(".logs.lock")
	if err != nil || !validPrivateFileInfo(info) || before != nil && !os.SameFile(info, before) || manager.verifyRoot() != nil {
		return Settings{}, ErrUnavailable
	}
	if manager.lockIdentity == nil {
		manager.allowInitialization = before == nil
		manager.lockIdentity = info
	}
	if ctx.Err() != nil {
		return Settings{}, ErrUnavailable
	}
	result, err = operation(ctx)
	if err != nil {
		return Settings{}, publicError(err)
	}
	return result, nil
}

func (manager *Manager) openFile(name string, flags int) (diskFile, os.FileInfo, error) {
	if manager.verifyRoot() != nil {
		return nil, nil, ErrUnavailable
	}
	before, err := manager.files.Lstat(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, ErrUnavailable
	}
	if err == nil && !validPrivateFileInfo(before) {
		return nil, nil, ErrUnavailable
	}
	if errors.Is(err, fs.ErrNotExist) && flags&os.O_CREATE == 0 {
		return nil, nil, ErrUnavailable
	}
	file, err := manager.files.OpenFile(name, flags, 0o600)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	opened, err := file.Stat()
	current, inspectError := manager.files.Lstat(name)
	if err != nil || inspectError != nil || !validPrivateFileInfo(opened) || !validPrivateFileInfo(current) || !os.SameFile(opened, current) || before != nil && !os.SameFile(before, opened) {
		_ = manager.closeFile(file)
		return nil, nil, ErrUnavailable
	}
	return file, opened, nil
}

func (manager *Manager) syncDirectory() error {
	file, err := manager.files.OpenFile(".", os.O_RDONLY, 0)
	if err != nil {
		return ErrUnavailable
	}
	info, err := file.Stat()
	if err == nil && !os.SameFile(info, manager.identity) {
		err = ErrUnavailable
	}
	if err == nil {
		err = file.Sync()
	}
	if closeError := manager.closeFile(file); err != nil || closeError != nil {
		return ErrUnavailable
	}
	return nil
}

func randomName(prefix, suffix string) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", ErrUnavailable
	}
	return prefix + hex.EncodeToString(bytes[:]) + suffix, nil
}

type segment struct {
	name    string
	birth   time.Time
	info    os.FileInfo
	remnant bool
}

var segmentPattern = regexp.MustCompile(`^segment-([0-9]{20})-[0-9a-f]{32}\.jsonl(\.bad)?$`)
var temporaryPattern = regexp.MustCompile(`^settings-[0-9a-f]{32}\.tmp$`)

func (manager *Manager) scanSegments() ([]segment, error) {
	directory, err := manager.files.OpenFile(".", os.O_RDONLY, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	entries, readError := directory.ReadDir(4097)
	closeError := manager.closeFile(directory)
	if readError != nil && !errors.Is(readError, io.EOF) || closeError != nil || len(entries) > 4096 {
		return nil, ErrUnavailable
	}
	var result []segment
	for _, entry := range entries {
		name := entry.Name()
		parts := segmentPattern.FindStringSubmatch(name)
		temporary := temporaryPattern.MatchString(name)
		if parts == nil && !temporary {
			continue
		}
		info, err := manager.files.Lstat(name)
		if err != nil || !validPrivateFileInfo(info) || info.Size() < 0 {
			return nil, ErrUnavailable
		}
		value := segment{name: name, info: info, remnant: temporary || strings.HasSuffix(name, ".bad")}
		if parts != nil {
			birth, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return nil, ErrUnavailable
			}
			value.birth = time.Unix(0, birth)
		}
		result = append(result, value)
	}
	slices.SortFunc(result, func(first, second segment) int {
		if compared := first.birth.Compare(second.birth); compared != 0 {
			return compared
		}
		return cmp.Compare(first.name, second.name)
	})
	return result, nil
}

func (manager *Manager) removeSegment(entry segment) error {
	if manager.verifyRoot() != nil {
		return ErrUnavailable
	}
	current, err := manager.files.Lstat(entry.name)
	if err != nil || !validPrivateFileInfo(current) || !os.SameFile(current, entry.info) || current.Size() != entry.info.Size() {
		return ErrUnavailable
	}
	if err := manager.files.Remove(entry.name); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (manager *Manager) enforcePolicy(ctx context.Context, settings Settings, reserveCount int, reserveBytes int64) (result []segment, resultError error) {
	defer func() {
		if resultError != nil {
			manager.cleanupFailures.Add(1)
		}
	}()
	entries, err := manager.scanSegments()
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-time.Duration(settings.RetentionDays) * 24 * time.Hour)
	maximum := int64(settings.MaxFileMiB) << 20
	var total int64
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ErrUnavailable
		}
		if temporaryPattern.MatchString(entry.name) || !entry.birth.After(cutoff) || entry.info.Size() > maximum {
			if err := manager.removeSegment(entry); err != nil {
				return nil, err
			}
			continue
		}
		total += entry.info.Size()
		result = append(result, entry)
	}
	budget := int64(settings.MaxBackups+1) * maximum
	for len(result) > settings.MaxBackups+1-reserveCount || total+reserveBytes > budget {
		if len(result) == 0 || ctx.Err() != nil {
			return nil, ErrUnavailable
		}
		entry := result[0]
		if err := manager.removeSegment(entry); err != nil {
			return nil, err
		}
		total -= entry.info.Size()
		result = result[1:]
	}
	return result, nil
}

func (manager *Manager) validTail(file diskFile, size int64) (bool, error) {
	if size == 0 {
		return true, nil
	}
	length := min(size, 1025)
	if _, err := file.Seek(-length, io.SeekEnd); err != nil {
		return false, ErrUnavailable
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(file, data); err != nil {
		return false, ErrUnavailable
	}
	if data[len(data)-1] != '\n' {
		return false, nil
	}
	data = data[:len(data)-1]
	_, line, found := bytes.CutLast(data, []byte{'\n'})
	if !found {
		line = data
	}
	var decoded map[string]any
	return len(line) <= 1024 && json.Unmarshal(line, &decoded) == nil && decoded != nil, nil
}

func (manager *Manager) recoverTails(ctx context.Context, settings Settings) error {
	entries, err := manager.enforcePolicy(ctx, settings, 0, 0)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.remnant {
			continue
		}
		if ctx.Err() != nil {
			return ErrUnavailable
		}
		file, info, err := manager.openFile(entry.name, os.O_RDONLY)
		if err != nil {
			return err
		}
		valid, readError := manager.validTail(file, info.Size())
		closeError := manager.closeFile(file)
		if readError != nil || closeError != nil {
			return ErrUnavailable
		}
		if !valid && manager.quarantine(entry.name) != nil {
			return ErrUnavailable
		}
	}
	_, err = manager.enforcePolicy(ctx, settings, 0, 0)
	return err
}

func (manager *Manager) quarantine(name string) error {
	if manager.verifyRoot() != nil {
		return ErrUnavailable
	}
	if _, err := manager.files.Lstat(name + ".bad"); !errors.Is(err, fs.ErrNotExist) {
		return ErrUnavailable
	}
	if manager.files.Rename(name, name+".bad") != nil {
		return ErrUnavailable
	}
	return nil
}

func (manager *Manager) appendRecord(ctx context.Context, settings Settings, data []byte) error {
	entries, err := manager.enforcePolicy(ctx, settings, 0, 0)
	if err != nil {
		return err
	}
	maximum := int64(settings.MaxFileMiB) << 20
	for _, entry := range slices.Backward(entries) {
		if entry.remnant {
			continue
		}
		file, info, err := manager.openFile(entry.name, os.O_RDWR|os.O_APPEND)
		if err != nil {
			return err
		}
		valid, readError := manager.validTail(file, info.Size())
		if readError != nil {
			_ = manager.closeFile(file)
			return ErrUnavailable
		}
		if !valid {
			if manager.closeFile(file) != nil || manager.quarantine(entry.name) != nil {
				return ErrUnavailable
			}
		} else if info.Size()+int64(len(data)) <= maximum {
			return manager.writeRecord(file, entry.name, info.Size(), data)
		} else if manager.closeFile(file) != nil {
			return ErrUnavailable
		}
		break
	}
	if _, err := manager.enforcePolicy(ctx, settings, 1, int64(len(data))); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	name, err := randomName(fmt.Sprintf("segment-%020d-", time.Now().UnixNano()), ".jsonl")
	if err != nil {
		return err
	}
	file, _, err := manager.openFile(name, os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	return manager.writeRecord(file, name, 0, data)
}

func (manager *Manager) writeRecord(file diskFile, name string, previousSize int64, data []byte) error {
	written, writeError := file.Write(data)
	if writeError != nil || written != len(data) {
		rollbackError := file.Truncate(previousSize)
		closeError := manager.closeFile(file)
		if rollbackError != nil && closeError == nil {
			_ = manager.quarantine(name)
		}
		return ErrUnavailable
	}
	return manager.closeFile(file)
}
