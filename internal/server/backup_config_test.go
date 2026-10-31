package server

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

func backupSettingsFixture(test *testing.T, change func(map[string]any)) string {
	test.Helper()
	if errors.Is(maintenance.CheckPlatform(test.Context()), maintenance.ErrUnsupported) {
		test.Skip("private configuration files unsupported on this platform")
	}
	document := map[string]any{
		"endpoint": "https://s3.example.invalid", "region": "test-1", "bucket": "cognate-tests", "prefix": "backup-tests",
		"access_key_id": "synthetic-backup-access", "secret_access_key": "synthetic-backup-secret", "session_token": "synthetic-backup-session",
		"recovery_key_id": "00000000-0000-4000-8000-000000000001",
	}
	if change != nil {
		change(document)
	}
	data, err := json.Marshal(document)
	if err != nil {
		test.Fatal(err)
	}
	return backupSettingsFile(test, data)
}

func backupSettingsFile(test *testing.T, data []byte) string {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "settings.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		test.Fatal(err)
	}
	return path
}

func TestServerBackupSettingsDefaultsAndRedaction(test *testing.T) {
	settings, err := readServerBackupSettings(backupSettingsFixture(test, nil))
	if err != nil || settings.MaxDatabaseBytes != 256<<20 || settings.MaxContentBytes != 1<<30 || settings.JobTimeoutSeconds != 900 {
		test.Fatal("private backup defaults invalid", err)
	}
	encoded, err := json.Marshal(settings)
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("settings", "backup", settings)
	formatted := fmt.Sprintf("%v %+v %#v %s", settings, settings, settings, settings)
	if err != nil || string(encoded) != "{}" || strings.Count(formatted, "[private server backup settings]") != 4 || strings.Contains(formatted+logs.String(), "synthetic-backup") || !strings.Contains(logs.String(), "[private server backup settings]") {
		test.Fatal("backup settings expose credentials in default representations")
	}
	content, seal := settings.contentLimits(), settings.sealLimits()
	if content.Database.MaxDBBytes != settings.MaxDatabaseBytes || content.MaxContentBytes != settings.MaxContentBytes || content.Database.MaxStagingBytes <= settings.MaxDatabaseBytes+content.Database.MaxInventoryBytes || seal.InputStagingBytes != 64<<20 || seal.TotalPlainBytes <= settings.MaxContentBytes || seal.TotalCipherBytes <= settings.MaxContentBytes || seal.StagingBytes <= seal.InputStagingBytes {
		test.Fatal("source and encrypted limits do not include bounded metadata overhead")
	}
}

func TestServerBackupSettingsRejectInvalid(test *testing.T) {
	for name, change := range map[string]map[string]any{
		"unknown":      {"unexpected_secret": "synthetic-backup-private"},
		"http":         {"endpoint": "http://s3.example.invalid"},
		"userinfo":     {"endpoint": "https://synthetic-backup-private@s3.example.invalid"},
		"query":        {"endpoint": "https://s3.example.invalid?synthetic-backup-private=value"},
		"empty-access": {"access_key_id": ""}, "empty-secret": {"secret_access_key": ""},
		"control-secret":  {"secret_access_key": "synthetic-backup-private\n"},
		"key-id":          {"recovery_key_id": "synthetic-backup-private"},
		"zero-key-id":     {"recovery_key_id": "00000000-0000-0000-0000-000000000000"},
		"trailing-key-id": {"recovery_key_id": "00000000-0000-4000-8000-000000000001 "},
		"small-db":        {"max_database_bytes": 1}, "large-db": {"max_database_bytes": 8<<30 + 1},
		"negative-db": {"max_database_bytes": -1}, "too-small-content": {"max_content_bytes": 1},
		"large-content": {"max_content_bytes": 16<<30 + 1}, "short-timeout": {"job_timeout_seconds": 59},
		"long-timeout": {"job_timeout_seconds": 86401}, "fractional": {"job_timeout_seconds": 60.5},
	} {
		test.Run(name, func(test *testing.T) {
			path := backupSettingsFixture(test, func(document map[string]any) { maps.Copy(document, change) })
			if _, err := readServerBackupSettings(path); err == nil || strings.Contains(err.Error(), "synthetic-backup") || strings.Contains(err.Error(), path) {
				test.Fatal("invalid or private settings were accepted or exposed")
			}
		})
	}
	for _, contents := range []string{`{}`, `{"bucket":"first","bucket":"second"}`, `{} {}`, `null`, `{"max_database_bytes":9223372036854775808}`, strings.Repeat("x", 65537)} {
		if _, err := readServerBackupSettings(backupSettingsFile(test, []byte(contents))); err == nil {
			test.Fatal("malformed, duplicate, empty, or unbounded settings accepted")
		}
	}
}

func TestServerBackupSettingsPrivateFile(test *testing.T) {
	for _, mode := range []string{"public", "symlink", "hardlink", "missing"} {
		test.Run(mode, func(test *testing.T) {
			path := backupSettingsFixture(test, nil)
			switch mode {
			case "public":
				if err := os.Chmod(path, 0644); err != nil {
					test.Fatal(err)
				}
			case "symlink", "hardlink":
				link := filepath.Join(filepath.Dir(path), "linked.json")
				var err error
				if mode == "symlink" {
					err = os.Symlink(path, link)
				} else {
					err = os.Link(path, link)
				}
				if err != nil {
					test.Fatal(err)
				}
				path = link
			case "missing":
				if err := os.Remove(path); err != nil {
					test.Fatal(err)
				}
			}
			if _, err := readServerBackupSettings(path); err == nil || strings.Contains(err.Error(), path) {
				test.Fatal("untrusted backup configuration accepted or its path leaked")
			}
		})
	}
}

func TestServerBackupGenerationBindsConfigurationAndKeys(test *testing.T) {
	settings, err := readServerBackupSettings(backupSettingsFixture(test, nil))
	if err != nil {
		test.Fatal(err)
	}
	master, recovery := bytes.Repeat([]byte{31}, 32), bytes.Repeat([]byte{57}, 32)
	baseline, err := settings.generation(master, recovery)
	if err != nil || baseline == [32]byte{} {
		test.Fatal("configuration commitment unavailable", err)
	}
	explicit, err := readServerBackupSettings(backupSettingsFixture(test, func(document map[string]any) {
		document["max_database_bytes"], document["max_content_bytes"], document["job_timeout_seconds"] = 256<<20, 1<<30, 900
	}))
	if err != nil {
		test.Fatal(err)
	}
	if actual, err := explicit.generation(master, recovery); err != nil || actual != baseline {
		test.Fatal("equivalent normalized configuration changed generation")
	}
	for _, change := range []func(*serverBackupSettings){
		func(settings *serverBackupSettings) { settings.SecretAccessKey += "-changed" },
		func(settings *serverBackupSettings) { settings.Prefix += "-changed" },
		func(settings *serverBackupSettings) { settings.MaxDatabaseBytes *= 2 },
		func(settings *serverBackupSettings) { settings.MaxContentBytes *= 2 },
		func(settings *serverBackupSettings) { settings.JobTimeoutSeconds++ },
		func(settings *serverBackupSettings) { settings.RecoveryKeyID = "00000000-0000-4000-8000-000000000002" },
	} {
		candidate := settings
		change(&candidate)
		if actual, err := candidate.generation(master, recovery); err != nil || actual == baseline {
			test.Fatal("changed immutable configuration retained generation")
		}
	}
	if actual, err := settings.generation(bytes.Repeat([]byte{32}, 32), recovery); err != nil || actual == baseline {
		test.Fatal("different master key retained generation")
	}
	if actual, err := settings.generation(master, bytes.Repeat([]byte{58}, 32)); err != nil || actual == baseline {
		test.Fatal("different recovery material retained generation")
	}
	if _, err := settings.generation(nil, recovery); err == nil {
		test.Fatal("missing master key accepted")
	}
}

func TestServerBackupOptInValidation(test *testing.T) {
	if err := validateServerBackupConfig(test.Context(), Config{}); err != nil {
		test.Fatal("default deployment changed", err)
	}
	for _, configuration := range []Config{{BackupS3ConfigFile: "/synthetic-private"}, {BackupRecoveryKeyFile: "/synthetic-private"}, {BackupEnabled: true}, {BackupEnabled: true, BackupS3ConfigFile: "/synthetic-private"}} {
		if err := validateServerBackupConfig(context.WithoutCancel(test.Context()), configuration); err == nil || strings.Contains(err.Error(), "/synthetic-private") {
			test.Fatal("partial backup configuration accepted or echoed")
		}
	}
}
