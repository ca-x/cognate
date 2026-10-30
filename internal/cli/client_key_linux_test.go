package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/clientkey"
	"golang.org/x/sys/unix"
)

func TestClientCredentialKeyPrivatePersistentAndNotRecreated(test *testing.T) {
	home := test.TempDir()
	if err := clientkey.Initialize(test.Context(), home); err != nil {
		test.Fatal(err)
	}
	first, err := loadClientKey(test.Context(), home)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(first)
	second, err := loadClientKey(test.Context(), home)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(second)
	if len(first) != 32 || !bytes.Equal(first, second) {
		test.Fatal("client credential key changed")
	}
	path := filepath.Join(home, ".config", "skillhub", "client.key")
	if err := os.Chmod(path, 0o644); err != nil {
		test.Fatal(err)
	}
	if _, err := loadClientKey(test.Context(), home); err == nil {
		test.Fatal("readable key accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
		test.Fatal(err)
	}
	if _, err := loadClientKey(test.Context(), home); err == nil {
		test.Fatal("truncated key silently recreated")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, []byte("truncated")) {
		test.Fatalf("malformed existing key was changed: %v", err)
	}
}

func TestClientCredentialKeyRejectsLinks(test *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		test.Run(kind, func(test *testing.T) {
			home := test.TempDir()
			directory := filepath.Join(home, ".config", "skillhub")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			outside := filepath.Join(home, "unrelated.key")
			if err := os.WriteFile(outside, bytes.Repeat([]byte{21}, 32), 0o600); err != nil {
				test.Fatal(err)
			}
			link := os.Link
			if kind == "symlink" {
				link = os.Symlink
			}
			if err := link(outside, filepath.Join(directory, "client.key")); err != nil {
				test.Fatal(err)
			}
			if _, err := loadClientKey(test.Context(), home); err == nil {
				test.Fatal("linked key accepted")
			}
		})
	}
}

func TestClientCredentialKeyReplacementMustNotBlock(test *testing.T) {
	home := test.TempDir()
	directory := filepath.Join(home, ".config", "skillhub")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	keyPath := filepath.Join(directory, "client.key")
	replacementPath := filepath.Join(directory, "replacement.key")
	expected := bytes.Repeat([]byte{37}, 32)
	if err := os.WriteFile(keyPath, expected, 0o600); err != nil {
		test.Fatal(err)
	}
	if err := syscall.Mkfifo(replacementPath, 0o600); err != nil {
		test.Fatal(err)
	}
	beforeKey, err := os.Lstat(keyPath)
	if err != nil {
		test.Fatal(err)
	}
	beforeReplacement, err := os.Lstat(replacementPath)
	if err != nil {
		test.Fatal(err)
	}
	result, err := runClientKeyReplacement(test.Context(), home, "TestClientCredentialKeyReplacementHelper", "")
	if err != nil {
		test.Fatalf("replacement subprocess after %d completed loads: %v", result.completed, err)
	}
	test.Logf("replacement completed=%d waited=%t readerJoined=%t", result.completed, result.waited, result.readerJoined)
	for _, path := range []string{keyPath, replacementPath} {
		after, err := os.Lstat(path)
		if err != nil {
			test.Fatal(err)
		}
		if os.SameFile(beforeKey, after) {
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, expected) {
				test.Fatalf("existing key changed: %v", err)
			}
		} else if !os.SameFile(beforeReplacement, after) || after.Mode()&os.ModeNamedPipe == 0 {
			test.Fatal("malformed key entry was replaced")
		}
	}
}

var (
	errReplacementNoProgress = errors.New("replacement subprocess made no completed-load progress for five seconds")
	errReplacementProtocol   = errors.New("invalid replacement progress protocol")
	errReplacementIncomplete = errors.New("replacement subprocess did not complete 1000 loads")
)

type clientKeyReplacementResult struct {
	completed    uint32
	ready        bool
	pid          int
	waited       bool
	readerJoined bool
}

func runClientKeyReplacement(ctx context.Context, home, helper, mode string) (result clientKeyReplacementResult, returnedErr error) {
	deadline, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	reader, writer, err := os.Pipe()
	if err != nil {
		return result, errors.New("replacement progress pipe unavailable")
	}
	defer reader.Close()
	defer writer.Close()
	child := exec.CommandContext(deadline, os.Args[0], "-test.run=^"+helper+"$")
	child.Env = append(os.Environ(), "SKILLHUB_CLIENT_KEY_REPLACEMENT_HOME="+home, "SKILLHUB_CLIENT_KEY_REPLACEMENT_MODE="+mode, "GOMAXPROCS=2")
	child.ExtraFiles = []*os.File{writer}
	child.Stdout, child.Stderr = io.Discard, io.Discard
	if err := child.Start(); err != nil {
		return result, errors.New("replacement subprocess could not start")
	}
	result.pid = child.Process.Pid
	_ = writer.Close()
	progress := make(chan uint32)
	processDone := make(chan struct{})
	var workers sync.WaitGroup
	var readErr, waitErr error
	workers.Go(func() {
		defer close(progress)
		for {
			var frame [4]byte
			if _, readErr = io.ReadFull(reader, frame[:]); readErr != nil {
				return
			}
			select {
			case progress <- binary.BigEndian.Uint32(frame[:]):
			case <-deadline.Done():
				return
			}
		}
	})
	workers.Go(func() {
		waitErr = child.Wait()
		close(processDone)
	})
	defer func() {
		cancel()
		_ = reader.Close()
		workers.Wait()
		result.waited, result.readerJoined = true, true
		if returnedErr == nil && waitErr != nil {
			returnedErr = errors.New("replacement subprocess did not exit successfully")
		}
	}()
	idle := time.NewTimer(5 * time.Second)
	defer idle.Stop()
	for progress != nil || processDone != nil {
		if err := deadline.Err(); err != nil {
			return result, err
		}
		select {
		case completed, open := <-progress:
			if !open {
				progress = nil
				continue
			}
			if completed == 0 {
				if result.ready {
					return result, errReplacementProtocol
				}
				result.ready = true
			} else {
				if !result.ready || completed != result.completed+1 || completed > 1000 {
					return result, errReplacementProtocol
				}
				result.completed = completed
			}
			idle.Reset(5 * time.Second)
		case <-processDone:
			processDone = nil
		case <-idle.C:
			if err := deadline.Err(); err != nil {
				return result, err
			}
			return result, errReplacementNoProgress
		case <-deadline.Done():
			return result, deadline.Err()
		}
	}
	if !errors.Is(readErr, io.EOF) {
		return result, errReplacementProtocol
	}
	if !result.ready || result.completed != 1000 {
		return result, errReplacementIncomplete
	}
	return result, nil
}

func writeClientKeyReplacementProgress(test *testing.T, progress *os.File, completed uint32) {
	test.Helper()
	var frame [4]byte
	binary.BigEndian.PutUint32(frame[:], completed)
	if count, err := progress.Write(frame[:]); err != nil || count != len(frame) {
		test.Fatal("replacement progress could not be sent")
	}
}

func TestClientCredentialKeyReplacementMonitor(test *testing.T) {
	for _, mode := range []string{"blocked", "duplicate-ready", "incomplete", "canceled"} {
		test.Run(mode, func(test *testing.T) {
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			if mode == "canceled" {
				timer := time.AfterFunc(250*time.Millisecond, cancel)
				defer timer.Stop()
			}
			result, err := runClientKeyReplacement(ctx, test.TempDir(), "TestClientCredentialKeyReplacementMonitorHelper", mode)
			expected := errReplacementNoProgress
			switch mode {
			case "duplicate-ready":
				expected = errReplacementProtocol
			case "incomplete":
				expected = errReplacementIncomplete
			case "canceled":
				expected = context.Canceled
			}
			if !errors.Is(err, expected) || !result.waited || !result.readerJoined || result.pid == 0 {
				test.Fatalf("replacement monitor did not classify and join: mode=%s completed=%d error=%v", mode, result.completed, err)
			}
			var status unix.WaitStatus
			if _, err := unix.Wait4(result.pid, &status, unix.WNOHANG, nil); !errors.Is(err, unix.ECHILD) {
				test.Fatal("replacement subprocess was not reaped")
			}
		})
	}
}

func TestClientCredentialKeyReplacementMonitorHelper(test *testing.T) {
	home, mode := os.Getenv("SKILLHUB_CLIENT_KEY_REPLACEMENT_HOME"), os.Getenv("SKILLHUB_CLIENT_KEY_REPLACEMENT_MODE")
	if home == "" || mode == "" {
		test.Skip("isolated monitor subprocess only")
	}
	progress := os.NewFile(3, "replacement-progress")
	defer progress.Close()
	writeClientKeyReplacementProgress(test, progress, 0)
	if mode == "duplicate-ready" {
		writeClientKeyReplacementProgress(test, progress, 0)
	}
	if mode == "incomplete" {
		writeClientKeyReplacementProgress(test, progress, 1)
		return
	}
	path := filepath.Join(home, "blocked-load.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		test.Fatal("monitor FIFO could not be created")
	}
	file, err := os.Open(path)
	if err == nil {
		_ = file.Close()
	}
	test.Fatal("monitor FIFO unexpectedly returned")
}

func TestClientCredentialKeyReplacementHelper(test *testing.T) {
	home := os.Getenv("SKILLHUB_CLIENT_KEY_REPLACEMENT_HOME")
	if home == "" {
		test.Skip("isolated subprocess only")
	}
	progress := os.NewFile(3, "replacement-progress")
	defer progress.Close()
	directory := filepath.Join(home, ".config", "skillhub")
	keyPath := filepath.Join(directory, "client.key")
	replacementPath := filepath.Join(directory, "replacement.key")
	ctx, cancel := context.WithCancel(test.Context())
	var swapping sync.WaitGroup
	var replacementErr error
	swapping.Go(func() {
		for ctx.Err() == nil {
			if err := unix.Renameat2(unix.AT_FDCWD, keyPath, unix.AT_FDCWD, replacementPath, unix.RENAME_EXCHANGE); err != nil {
				replacementErr = err
				return
			}
		}
	})
	defer func() {
		cancel()
		swapping.Wait()
		if replacementErr != nil {
			test.Fatal(replacementErr)
		}
	}()
	writeClientKeyReplacementProgress(test, progress, 0)
	for completed := range 1000 {
		key, err := loadClientKey(test.Context(), home)
		if err == nil && !bytes.Equal(key, bytes.Repeat([]byte{37}, 32)) {
			test.Error("replacement returned a different client key")
		}
		clear(key)
		writeClientKeyReplacementProgress(test, progress, uint32(completed+1))
	}
}
