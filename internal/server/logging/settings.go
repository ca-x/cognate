package logging

import (
	"errors"
	"io"
	"log/slog"
	"os"

	"encoding/json/v2"
)

var (
	ErrInvalid     = errors.New("invalid logging input")
	ErrConflict    = errors.New("logging settings conflict")
	ErrUnavailable = errors.New("logging unavailable")
	ErrClosed      = errors.New("logging closed")
)

type Settings struct {
	Level         string `json:"level"`
	MaxFileMiB    int    `json:"max_file_mib"`
	MaxBackups    int    `json:"max_backups"`
	RetentionDays int    `json:"retention_days"`
	Revision      int64  `json:"revision"`
}

type Health struct {
	WriteFailures   uint64
	CleanupFailures uint64
	DroppedRecords  uint64
	Degraded        bool
}

type Snapshot struct {
	Settings Settings
	Health   Health
}

func defaultSettings() Settings {
	return Settings{Level: "info", MaxFileMiB: 10, MaxBackups: 7, RetentionDays: 14, Revision: 1}
}

func (settings Settings) level() slog.Level {
	switch settings.Level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (settings Settings) valid() bool {
	return (settings.Level == "debug" || settings.Level == "info" || settings.Level == "warn" || settings.Level == "error") && settings.MaxFileMiB >= 1 && settings.MaxFileMiB <= 100 && settings.MaxBackups >= 1 && settings.MaxBackups <= 100 && settings.RetentionDays >= 1 && settings.RetentionDays <= 365
}

type settingsDocument struct {
	Version  int      `json:"version"`
	Settings Settings `json:"settings"`
}

func (manager *Manager) readSettings(create bool) (result Settings, resultError error) {
	_, err := manager.files.Lstat("settings.json")
	if errors.Is(err, os.ErrNotExist) && create && manager.allowInitialization {
		settings := defaultSettings()
		if err := manager.publishSettings(settings); err != nil {
			return Settings{}, err
		}
		return settings, nil
	}
	file, _, err := manager.openFile("settings.json", os.O_RDONLY)
	if err != nil {
		return Settings{}, ErrUnavailable
	}
	defer func() {
		if manager.closeFile(file) != nil {
			result = Settings{}
			resultError = ErrUnavailable
		}
	}()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	var document settingsDocument
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &document, json.RejectUnknownMembers(true)) != nil || document.Version != 1 || !document.Settings.valid() || document.Settings.Revision < 1 {
		return Settings{}, ErrUnavailable
	}
	return document.Settings, nil
}

func (manager *Manager) publishSettings(settings Settings) error {
	data, err := json.Marshal(settingsDocument{Version: 1, Settings: settings})
	if err != nil {
		return ErrUnavailable
	}
	name, err := randomName("settings-", ".tmp")
	if err != nil {
		return err
	}
	file, _, err := manager.openFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	written, writeError := file.Write(data)
	if writeError == nil && written != len(data) {
		writeError = io.ErrShortWrite
	}
	if writeError == nil {
		writeError = file.Sync()
	}
	closeError := manager.closeFile(file)
	if writeError != nil || closeError != nil {
		return ErrUnavailable
	}
	if err := manager.files.Rename(name, "settings.json"); err != nil {
		return ErrUnavailable
	}
	return manager.syncDirectory()
}
