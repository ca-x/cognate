package logging

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"encoding/json/v2"
)

func privateDirectory(test *testing.T) string {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal("cannot make isolated fixture private")
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		test.Fatal("cannot canonicalize isolated fixture")
	}
	return directory
}

func TestFullStderrFailureAndConcurrentClose(test *testing.T) {
	for _, operation := range []string{"record", "update", "retention"} {
		test.Run(operation, func(test *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				test.Fatal("pipe setup failed")
			}
			defer reader.Close()
			defer writer.Close()
			if writer.SetWriteDeadline(time.Now().Add(20*time.Millisecond)) != nil {
				test.Fatal("pipe deadline setup failed")
			}
			if _, err := writer.Write(make([]byte, 4<<20)); !errors.Is(err, os.ErrDeadlineExceeded) {
				test.Fatal("pipe did not fill")
			}
			if writer.SetWriteDeadline(time.Time{}) != nil {
				test.Fatal("pipe deadline reset failed")
			}
			directory := privateDirectory(test)
			previous := os.Stderr
			os.Stderr = writer
			manager, err := Open(test.Context(), directory)
			os.Stderr = previous
			if err != nil {
				test.Fatal("open failed")
			}
			if os.Remove(filepath.Join(directory, "logs", "settings.json")) != nil {
				test.Fatal("fault setup failed")
			}
			finished := make(chan error, 1)
			go func() {
				switch operation {
				case "record":
					finished <- manager.Record(Event{Kind: EventServerStarted})
				case "update":
					_, err := manager.Update(test.Context(), 1, Settings{Level: "warn", MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 1})
					finished <- err
				case "retention":
					manager.retentionPass()
					finished <- ErrUnavailable
				}
			}()
			blocked := false
			select {
			case err := <-finished:
				if !errors.Is(err, ErrUnavailable) {
					test.Error("storage error classification changed")
				}
			case <-time.After(500 * time.Millisecond):
				blocked = true
			}
			closed := make(chan error, 2)
			for range 2 {
				go func() { closed <- manager.Close() }()
			}
			closeBlocked := false
			closeReturned := 0
			for range 2 {
				select {
				case <-closed:
					closeReturned++
				case <-time.After(500 * time.Millisecond):
					closeBlocked = true
				}
				if closeBlocked {
					break
				}
			}
			_ = reader.Close()
			if blocked {
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					test.Fatal("operation did not join after release")
				}
			}
			if closeBlocked {
				for range 2 - closeReturned {
					select {
					case <-closed:
					case <-time.After(3 * time.Second):
						test.Fatal("Close did not join after release")
					}
				}
			}
			health := manager.health()
			if !health.Degraded || operation == "retention" && health.CleanupFailures == 0 || operation != "retention" && health.WriteFailures == 0 {
				test.Error("failure was not observable")
			}
			if blocked || closeBlocked {
				test.Errorf("full stderr blocked operation=%t Close=%t", blocked, closeBlocked)
			}
		})
	}
}

func TestUnavailableWarningSinks(test *testing.T) {
	for _, sink := range []string{"closed", "blocking-pipe", "regular-file", "unknown", "nil"} {
		test.Run(sink, func(test *testing.T) {
			directory := privateDirectory(test)
			manager, err := Open(test.Context(), directory)
			if err != nil {
				test.Fatal("open failed")
			}
			defer manager.Close()
			var unknown bytes.Buffer
			switch sink {
			case "closed", "blocking-pipe":
				reader, writer, err := os.Pipe()
				if err != nil {
					test.Fatal("pipe setup failed")
				}
				defer reader.Close()
				defer writer.Close()
				if sink == "closed" {
					_ = writer.Close()
				} else {
					_ = writer.Fd()
				}
				manager.warningWriter = writer
				manager.warn()
				if sink == "blocking-pipe" {
					_ = writer.Close()
					data, err := io.ReadAll(reader)
					if err != nil || len(data) != 0 {
						test.Fatal("blocking sink was written")
					}
				}
			case "regular-file":
				file, err := os.CreateTemp(directory, "diagnostic-")
				if err != nil {
					test.Fatal("file setup failed")
				}
				defer file.Close()
				manager.warningWriter = file
				manager.warn()
				info, err := file.Stat()
				if err != nil || info.Size() != 0 {
					test.Fatal("regular diagnostic file was written")
				}
			case "unknown":
				manager.warningWriter = &unknown
				manager.warn()
				if unknown.Len() != 0 {
					test.Fatal("unrecognized sink was called")
				}
			case "nil":
				manager.warningWriter = (*os.File)(nil)
				manager.warn()
			}
			if manager.Record(Event{Kind: EventServerStarted}) != nil || !bytes.Contains(logBytes(test, directory), []byte("server_started")) {
				test.Fatal("unavailable diagnostic disabled operational data")
			}
		})
	}
}

func TestMissingSettingsDoesNotResetRevision(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	if _, err := manager.Update(test.Context(), 1, Settings{Level: "warn", MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 1}); err != nil {
		test.Fatal("update failed")
	}
	if manager.Close() != nil || os.Remove(filepath.Join(directory, "logs", "settings.json")) != nil {
		test.Fatal("missing settings fixture failed")
	}
	if reopened, err := Open(test.Context(), directory); reopened != nil || !errors.Is(err, ErrUnavailable) {
		if reopened != nil {
			_ = reopened.Close()
		}
		test.Fatal("missing policy silently reset the monotonic revision")
	}
}

func TestManagerIdleRetentionLoop(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		directory := privateDirectory(test)
		manager, err := Open(test.Context(), directory)
		if err != nil {
			test.Fatal("open failed")
		}
		defer manager.Close()
		name := seedSegment(test, directory, time.Now().Add(-14*24*time.Hour+30*time.Second), []byte("{}\n"))
		path := filepath.Join(directory, "logs", name)
		if os.Chtimes(path, time.Now(), time.Now()) != nil {
			test.Fatal("fixture mtime failed")
		}
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			test.Fatal("idle retention did not expire birth-aged active segment")
		}
		if manager.Close() != nil {
			test.Fatal("close did not join retention loop")
		}
		time.Sleep(2 * time.Minute)
		if !errors.Is(manager.Record(Event{Kind: EventServerStopped}), ErrClosed) {
			test.Fatal("closed manager accepted work")
		}
	})
}

func TestManagerCloseDrainAndFailedPhysicalClose(test *testing.T) {
	for _, failClose := range []bool{false, true} {
		test.Run(strconv.FormatBool(failClose), func(test *testing.T) {
			synctest.Test(test, func(test *testing.T) {
				directory := privateDirectory(test)
				manager, err := Open(test.Context(), directory)
				if err != nil {
					test.Fatal("open failed")
				}
				original := manager.files
				entered, release := make(chan struct{}), make(chan struct{})
				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()
				var once sync.Once
				closeCalls := 0
				manager.files = faultFiles{fileOperations: original, open: func(name string, flags int, file diskFile) diskFile {
					if !strings.HasSuffix(name, ".jsonl") {
						return file
					}
					return faultFile{diskFile: file, write: func(data []byte) (int, error) { once.Do(func() { close(entered); <-release }); return file.Write(data) }}
				}, close: func() error {
					closeCalls++
					if failClose {
						return errors.New("synthetic-close-unknown")
					}
					return original.Close()
				}}
				written := make(chan error, 1)
				go func() { written <- manager.Record(Event{Kind: EventServerStarted}) }()
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					test.Fatal("write barrier not reached")
				}
				closed := make(chan error, 4)
				go func() { closed <- manager.Close() }()
				synctest.Wait()
				if !errors.Is(manager.Record(Event{Kind: EventServerStopped}), ErrClosed) {
					test.Fatal("close admitted a new writer")
				}
				select {
				case <-closed:
					test.Fatal("close escaped an admitted physical write")
				default:
				}
				for range 3 {
					go func() { closed <- manager.Close() }()
				}
				close(release)
				if err := <-written; err != nil {
					test.Fatal("admitted write did not finish")
				}
				result := <-closed
				for range 3 {
					if <-closed != result {
						test.Fatal("concurrent close changed its cached outcome")
					}
				}
				if failClose != errors.Is(result, ErrUnavailable) {
					test.Fatal("physical close outcome was changed")
				}
				if manager.Close() != result || closeCalls != 1 {
					test.Fatal("repeated close retried or washed an unknown outcome")
				}
				if failClose {
					file, err := original.OpenFile("retained-resource", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
					if err != nil {
						test.Fatal("failure fixture had already closed the real resource")
					}
					if _, err := file.Write([]byte("still-open")); err != nil {
						test.Fatal("retained root is not physically live")
					}
					_ = file.Close()
					_ = original.Close()
				}
			})
		})
	}
}

func TestLoggingProcess(test *testing.T) {
	directory := os.Getenv("COGNATE_LOGGING_TEST_DIRECTORY")
	if directory == "" {
		test.Skip("cooperating subprocess helper")
	}
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("child open failed")
	}
	defer manager.Close()
	manager.warningWriter = io.Discard
	mode := os.Getenv("COGNATE_LOGGING_TEST_MODE")
	if mode == "crash-tail" {
		original := manager.files
		manager.files = faultFiles{fileOperations: original, open: func(name string, flags int, file diskFile) diskFile {
			if !strings.HasSuffix(name, ".jsonl") {
				return file
			}
			return faultFile{diskFile: file, write: func(data []byte) (int, error) {
				_, _ = file.Write(data[:len(data)/2])
				os.Exit(73)
				return 0, ErrUnavailable
			}}
		}}
		_ = manager.Record(Event{Kind: EventServerStarted})
		test.Fatal("child did not exit inside the physical append")
	}
	if mode == "cas" {
		_, err := manager.Update(test.Context(), 1, Settings{Level: "warn", MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 1})
		if err != nil && !errors.Is(err, ErrConflict) {
			test.Fatal("child CAS failed unexpectedly")
		}
		return
	}
	if mode == "wait-policy" {
		if os.WriteFile(filepath.Join(directory, "ready"), nil, 0o600) != nil {
			test.Fatal("child barrier failed")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(directory, "release")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				test.Fatal("child policy barrier timed out")
			}
			time.Sleep(time.Millisecond)
		}
	}
	for range 40 {
		if manager.Record(Event{Kind: EventRequestCompleted, Route: RouteNativeConfiguration, Status: 200, Duration: time.Millisecond}) != nil {
			test.Fatal("child write failed")
		}
	}
	if manager.Record(Event{Kind: EventServerFailed}) != nil {
		test.Fatal("child error event failed")
	}
}

func processCommand(test *testing.T, directory, mode string) *exec.Cmd {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	test.Cleanup(cancel)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLoggingProcess$", "-test.count=1")
	command.Env = append(os.Environ(), "COGNATE_LOGGING_TEST_DIRECTORY="+directory, "COGNATE_LOGGING_TEST_MODE="+mode)
	return command
}

func TestCooperatingProcessesRotationAndLatestPolicy(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	defer manager.Close()
	if _, err := manager.Update(test.Context(), 1, Settings{Level: "info", MaxFileMiB: 1, MaxBackups: 2, RetentionDays: 1}); err != nil {
		test.Fatal("update failed")
	}
	seedSegment(test, directory, time.Now().Add(-time.Hour), fullSegment())
	commands := []*exec.Cmd{processCommand(test, directory, "write"), processCommand(test, directory, "write")}
	for _, command := range commands {
		if command.Start() != nil {
			test.Fatal("cannot start cooperating process")
		}
	}
	for range 40 {
		if manager.Record(Event{Kind: EventServerStarted}) != nil {
			test.Fatal("parent write failed")
		}
	}
	for _, command := range commands {
		if command.Wait() != nil {
			test.Fatal("cooperating process failed")
		}
	}
	data := logBytes(test, directory)
	if bytes.Count(data, []byte(`"msg":"request_completed"`)) != 80 || bytes.Count(data, []byte(`"msg":"server_failed"`)) != 2 || bytes.Count(data, []byte(`"msg":"server_started"`)) != 40 {
		test.Fatal("cooperating processes lost or split accepted events")
	}
	command := processCommand(test, directory, "wait-policy")
	if command.Start() != nil {
		test.Fatal("cannot start policy observer")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(directory, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("policy observer did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := manager.Update(test.Context(), 2, Settings{Level: "error", MaxFileMiB: 1, MaxBackups: 2, RetentionDays: 1}); err != nil {
		test.Fatal("policy update failed")
	}
	if os.WriteFile(filepath.Join(directory, "release"), nil, 0o600) != nil {
		test.Fatal("policy release failed")
	}
	if command.Wait() != nil {
		test.Fatal("policy observer failed")
	}
	after := logBytes(test, directory)
	if bytes.Count(after, []byte(`"msg":"request_completed"`)) != 80 || bytes.Count(after, []byte(`"msg":"server_failed"`)) != 3 {
		test.Fatal("open peer did not use the latest lock-time policy")
	}
}

func TestCooperatingProcessCAS(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	defer manager.Close()
	commands := []*exec.Cmd{processCommand(test, directory, "cas"), processCommand(test, directory, "cas")}
	for _, command := range commands {
		if command.Start() != nil {
			test.Fatal("cannot start CAS process")
		}
	}
	for _, command := range commands {
		if command.Wait() != nil {
			test.Fatal("CAS process failed")
		}
	}
	state, err := manager.Snapshot(test.Context())
	if err != nil || state.Settings.Revision != 2 || state.Settings.Level != "warn" {
		test.Fatal("cooperating processes did not enforce a single CAS winner")
	}
}

func TestProcessCrashTailHandledAtRestart(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	if manager.Record(Event{Kind: EventServerStarted}) != nil || manager.Close() != nil {
		test.Fatal("initial fixture failed")
	}
	err = processCommand(test, directory, "crash-tail").Run()
	exited, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exited.ExitCode() != 73 {
		test.Fatal("child did not stop at the real partial-write boundary")
	}
	restarted, err := Open(test.Context(), directory)
	if err != nil {
		if err == ErrUnavailable {
			return
		}
		test.Fatal("restart returned a non-fixed failure")
	}
	defer restarted.Close()
	for _, line := range bytes.Split(bytes.TrimSpace(logBytes(test, directory)), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var decoded map[string]any
		if json.Unmarshal(line, &decoded) != nil {
			test.Fatal("restart left a crash-damaged segment eligible as a normal log")
		}
	}
	if restarted.Record(Event{Kind: EventServerStarted}) != nil {
		test.Fatal("record after crash recovery failed")
	}
}

func TestManagerDefaultsAndPersistence(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("manager open failed")
	}
	test.Cleanup(func() { _ = manager.Close() })
	snapshot, err := manager.Snapshot(test.Context())
	want := Settings{Level: "info", MaxFileMiB: 10, MaxBackups: 7, RetentionDays: 14, Revision: 1}
	if err != nil || snapshot.Settings != want || snapshot.Health != (Health{}) {
		test.Fatal("default snapshot does not match the persisted logging contract")
	}
	if err := manager.Record(Event{Kind: EventServerStarted}); err != nil {
		test.Fatal("lifecycle record failed")
	}
	entries, err := os.ReadDir(filepath.Join(directory, "logs"))
	if err != nil || len(entries) < 3 {
		test.Fatal("settings, lock and operational log were not created")
	}
	if err := manager.Close(); err != nil {
		test.Fatal("manager close failed")
	}
	reopened, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("manager reopen failed")
	}
	defer reopened.Close()
	again, err := reopened.Snapshot(test.Context())
	if err != nil || again.Settings != want {
		test.Fatal("reopen lost persisted policy")
	}
}
