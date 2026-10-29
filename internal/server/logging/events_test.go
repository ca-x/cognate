package logging

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"encoding/json/v2"
)

func logBytes(test *testing.T, directory string) []byte {
	test.Helper()
	entries, err := os.ReadDir(filepath.Join(directory, "logs"))
	if err != nil {
		test.Fatal("cannot inspect logs")
	}
	var output []byte
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			data, err := os.ReadFile(filepath.Join(directory, "logs", entry.Name()))
			if err != nil {
				test.Fatal("cannot read log")
			}
			output = append(output, data...)
		}
	}
	return output
}

func TestTypedEventsAndTransportPrivacy(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	defer manager.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		test.Fatal("cannot create stderr capture")
	}
	previous := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = previous; _ = writer.Close(); _ = reader.Close() }()
	_, _ = io.WriteString(os.Stderr, "privacy-capture-control\n")
	manager.HTTPErrorLog().Print("synthetic-do-not-log-secret=https://example.invalid/?credential=sentinel")
	_ = writer.Close()
	os.Stderr = previous
	captured, err := io.ReadAll(reader)
	if err != nil || !bytes.Contains(captured, []byte("privacy-capture-control")) {
		test.Fatal("stderr capture was not active")
	}
	if bytes.Contains(captured, []byte("sentinel")) || bytes.Contains(captured, []byte("example.invalid")) {
		test.Fatal("raw HTTP diagnostic leaked to stderr")
	}
	for _, event := range []Event{{Kind: EventServerStarted}, {Kind: EventRequestCompleted, Route: RouteConfiguration, Status: 200, Duration: time.Second}} {
		if manager.Record(event) != nil {
			test.Fatal("safe event rejected")
		}
	}
	data := logBytes(test, directory)
	if bytes.Count(data, []byte(`"msg":"transport_error"`)) != 1 {
		test.Fatal("transport diagnostics were discarded instead of safely recorded")
	}
	if bytes.Contains(data, []byte("sentinel")) || bytes.Contains(data, []byte("example.invalid")) || bytes.Contains(data, []byte("synthetic")) {
		test.Fatal("raw HTTP diagnostic leaked into log")
	}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var decoded map[string]any
		if json.Unmarshal(line, &decoded) != nil {
			test.Fatal("log does not contain complete JSON records")
		}
	}
	for _, invalid := range []Event{{}, {Kind: 255}, {Kind: EventRequestCompleted, Route: 255, Status: 200}, {Kind: EventRequestCompleted, Route: RouteAuth, Status: 99}, {Kind: EventRequestCompleted, Route: RouteAuth, Status: 600}, {Kind: EventRequestCompleted, Route: RouteAuth, Status: 200, Duration: -1}, {Kind: EventServerFailed, Route: RouteAuth}} {
		if !errors.Is(manager.Record(invalid), ErrInvalid) {
			test.Fatal("unsupported event value was accepted")
		}
	}
	if !bytes.Equal(logBytes(test, directory), data) {
		test.Fatal("invalid event mutated log")
	}
}

func TestEventLevelsAndAllRoutes(test *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		test.Run(level, func(test *testing.T) {
			directory := privateDirectory(test)
			manager, err := Open(test.Context(), directory)
			if err != nil {
				test.Fatal("open failed")
			}
			defer manager.Close()
			if _, err := manager.Update(test.Context(), 1, Settings{Level: level, MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 1}); err != nil {
				test.Fatal("level update failed")
			}
			for route := RouteHealth; route <= RouteOther; route++ {
				for _, event := range []Event{{Kind: EventRequestStarted, Route: route}, {Kind: EventRequestCompleted, Route: route, Status: 200}, {Kind: EventRequestCompleted, Route: route, Status: 400}, {Kind: EventRequestCompleted, Route: route, Status: 500}} {
					if manager.Record(event) != nil {
						test.Fatal("declared route or level was rejected")
					}
				}
			}
			data := logBytes(test, directory)
			want := map[string]int{"debug": 4, "info": 3, "warn": 2, "error": 1}[level] * 14
			if bytes.Count(data, []byte{'\n'}) != want {
				test.Fatal("severity threshold did not match fixed event outcomes")
			}
		})
	}
}
