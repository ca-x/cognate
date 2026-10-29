package logging

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSettingsCASAndRestart(test *testing.T) {
	directory := privateDirectory(test)
	first, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	defer first.Close()
	peer, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("peer open failed")
	}
	defer peer.Close()
	settings := Settings{Level: "warn", MaxFileMiB: 2, MaxBackups: 3, RetentionDays: 2}
	updated, err := first.Update(test.Context(), 1, settings)
	if err != nil || updated.Settings.Level != "warn" || updated.Settings.Revision != 2 {
		test.Fatal("CAS did not persist the proposed policy")
	}
	observed, err := peer.Snapshot(test.Context())
	if err != nil || observed.Settings != updated.Settings {
		test.Fatal("peer did not read the latest persisted policy")
	}
	before, err := os.ReadFile(filepath.Join(directory, "logs", "settings.json"))
	if err != nil {
		test.Fatal("cannot read fixture policy")
	}
	if result, err := peer.Update(test.Context(), 1, settings); !errors.Is(err, ErrConflict) || result != (Snapshot{}) {
		test.Fatal("stale revision was not rejected without a result")
	}
	settings.Level = "unknown"
	if _, err := first.Update(test.Context(), 2, settings); !errors.Is(err, ErrInvalid) {
		test.Fatal("invalid settings accepted")
	}
	after, err := os.ReadFile(filepath.Join(directory, "logs", "settings.json"))
	if err != nil || string(before) != string(after) {
		test.Fatal("rejected update mutated persisted policy")
	}
}

func TestSettingsUnknownPublicationAndCleanupFence(test *testing.T) {
	for _, stage := range []string{"file-sync", "rename-before", "rename-after", "directory-sync", "cleanup"} {
		test.Run(stage, func(test *testing.T) {
			directory := privateDirectory(test)
			manager, err := Open(test.Context(), directory)
			if err != nil {
				test.Fatal("open failed")
			}
			defer manager.Close()
			manager.warningWriter = io.Discard
			old := seedSegment(test, directory, time.Now().Add(-48*time.Hour), []byte("{}\n"))
			original := manager.files
			failure := errors.New("synthetic-private-error-must-not-escape")
			manager.files = faultFiles{fileOperations: original, open: func(name string, flags int, file diskFile) diskFile {
				if stage == "file-sync" && strings.HasSuffix(name, ".tmp") || stage == "directory-sync" && name == "." {
					return faultFile{diskFile: file, sync: func() error { return failure }}
				}
				return file
			}, rename: func(old, next string) error {
				if stage == "rename-before" {
					return failure
				}
				err := original.Rename(old, next)
				if stage == "rename-after" {
					return failure
				}
				return err
			}, remove: func(name string) error {
				if stage == "cleanup" {
					return failure
				}
				return original.Remove(name)
			}}
			settings := Settings{Level: "warn", MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 1}
			result, err := manager.Update(test.Context(), 1, settings)
			if err != ErrUnavailable || result != (Snapshot{}) {
				test.Fatal("storage failure leaked detail or acknowledged a change")
			}
			manager.files = original
			visible, err := manager.Snapshot(test.Context())
			if err != nil {
				test.Fatal("readback failed")
			}
			wantRevision := int64(1)
			if stage == "rename-after" || stage == "directory-sync" {
				wantRevision = 2
			}
			if visible.Settings.Revision != wantRevision {
				test.Fatal("unknown publication result was rebased or concealed")
			}
			if stage == "cleanup" {
				if visible.Health.CleanupFailures == 0 {
					test.Fatal("cleanup failure was not counted")
				}
				if _, err := os.Stat(filepath.Join(directory, "logs", old)); err != nil {
					test.Fatal("failed cleanup lost its preserved segment")
				}
			} else if _, err := os.Stat(filepath.Join(directory, "logs", old)); !os.IsNotExist(err) {
				test.Fatal("settings publication ran before enforcing proposed policy")
			}
			if wantRevision == 2 {
				if _, err := manager.Update(test.Context(), 1, settings); !errors.Is(err, ErrConflict) {
					test.Fatal("unknown outcome permitted automatic CAS replay")
				}
			}
		})
	}
}

func TestSettingsRejectAmbiguityAndMalformedPersistence(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	for _, proposed := range []Settings{
		{Level: "info", MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 1, Revision: 9},
		{Level: "info", MaxFileMiB: 0, MaxBackups: 1, RetentionDays: 1},
		{Level: "info", MaxFileMiB: 101, MaxBackups: 1, RetentionDays: 1},
		{Level: "info", MaxFileMiB: 1, MaxBackups: 0, RetentionDays: 1},
		{Level: "info", MaxFileMiB: 1, MaxBackups: 101, RetentionDays: 1},
		{Level: "info", MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 0},
		{Level: "info", MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 366},
	} {
		if result, err := manager.Update(test.Context(), 1, proposed); !errors.Is(err, ErrInvalid) || result != (Snapshot{}) {
			test.Fatal("invalid policy accepted")
		}
	}
	if manager.Close() != nil {
		test.Fatal("close failed")
	}
	path := filepath.Join(directory, "logs", "settings.json")
	for _, invalid := range []string{`{"version":1,"version":1}`, `{"version":2}`, `{"version":1,"private":"synthetic"}`, strings.Repeat("x", 4097)} {
		if os.WriteFile(path, []byte(invalid), 0o600) != nil {
			test.Fatal("malformed fixture failed")
		}
		if manager, err := Open(test.Context(), directory); manager != nil || !errors.Is(err, ErrUnavailable) {
			test.Fatal("malformed settings silently fell back")
		}
	}
}

func TestRemnantsShareCapacityAndBlockUnsafeGrowth(test *testing.T) {
	directory := privateDirectory(test)
	manager, err := Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open failed")
	}
	defer manager.Close()
	manager.warningWriter = io.Discard
	settings := Settings{Level: "info", MaxFileMiB: 1, MaxBackups: 1, RetentionDays: 1}
	if _, err := manager.Update(test.Context(), 1, settings); err != nil {
		test.Fatal("update failed")
	}
	seedSegment(test, directory, time.Now().Add(-time.Hour), fullSegment())
	seedSegment(test, directory, time.Now(), fullSegment())
	original := manager.files
	manager.files = faultFiles{fileOperations: original, open: func(name string, flags int, file diskFile) diskFile {
		if strings.HasSuffix(name, ".tmp") {
			return faultFile{diskFile: file, sync: func() error { return errors.New("synthetic-sync-failure") }}
		}
		return file
	}}
	if _, err := manager.Update(test.Context(), 2, settings); err != ErrUnavailable {
		test.Fatal("failed temporary publication was accepted")
	}
	var before int64
	for _, info := range segments(test, directory) {
		before += info.Size()
	}
	if before > 2<<20 {
		test.Fatal("failed publication remnants exceeded shared capacity")
	}
	manager.files = faultFiles{fileOperations: original, remove: func(string) error { return errors.New("synthetic-remnant-cleanup-failure") }}
	if manager.Record(Event{Kind: EventServerStarted}) != ErrUnavailable {
		test.Fatal("new record bypassed unrecoverable remnant capacity")
	}
	var after int64
	for _, info := range segments(test, directory) {
		after += info.Size()
	}
	if after != before {
		test.Fatal("failed cleanup grew the segment set")
	}
	manager.files = original
	if manager.Record(Event{Kind: EventServerStarted}) != nil {
		test.Fatal("safe remnant recovery failed")
	}
}
