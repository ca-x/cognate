package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/ca-x/cognate/internal/server/state"
)

type serverBackupSettings struct {
	Endpoint          string `json:"endpoint"`
	Region            string `json:"region"`
	Bucket            string `json:"bucket"`
	Prefix            string `json:"prefix"`
	AccessKeyID       string `json:"access_key_id"`
	SecretAccessKey   string `json:"secret_access_key"`
	SessionToken      string `json:"session_token,omitempty"`
	RecoveryKeyID     string `json:"recovery_key_id"`
	MaxDatabaseBytes  int64  `json:"max_database_bytes"`
	MaxContentBytes   int64  `json:"max_content_bytes"`
	JobTimeoutSeconds int64  `json:"job_timeout_seconds"`
}

func (serverBackupSettings) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private server backup settings]")
}

func (serverBackupSettings) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (serverBackupSettings) LogValue() slog.Value {
	return slog.StringValue("[private server backup settings]")
}

func validateServerBackupConfig(ctx context.Context, config Config) error {
	if !config.BackupEnabled {
		if config.BackupS3ConfigFile != "" || config.BackupRecoveryKeyFile != "" {
			return errors.New("backup files require explicit --backup-enabled")
		}
		return nil
	}
	if config.BackupS3ConfigFile == "" || config.BackupRecoveryKeyFile == "" {
		return errors.New("backup requires private S3 configuration and recovery key files")
	}
	if err := maintenance.CheckPlatform(ctx); err != nil || runtime.GOOS != "linux" {
		return errors.New("managed server backup is unsupported on this platform")
	}
	return nil
}

func readServerBackupSettings(path string) (serverBackupSettings, error) {
	data, err := state.ReadPrivateFile(path, 64<<10)
	if err != nil {
		return serverBackupSettings{}, errors.New("cannot read private backup S3 configuration")
	}
	defer clear(data)
	var settings serverBackupSettings
	if err := json.Unmarshal(data, &settings, json.RejectUnknownMembers(true)); err != nil {
		return serverBackupSettings{}, errors.New("invalid backup S3 configuration")
	}
	if settings.MaxDatabaseBytes == 0 {
		settings.MaxDatabaseBytes = 256 << 20
	}
	if settings.MaxContentBytes == 0 {
		settings.MaxContentBytes = 1 << 30
	}
	if settings.JobTimeoutSeconds == 0 {
		settings.JobTimeoutSeconds = 900
	}
	identifier, err := uuid.Parse(settings.RecoveryKeyID)
	if err != nil || identifier.String() != settings.RecoveryKeyID || settings.RecoveryKeyID == "00000000-0000-0000-0000-000000000000" || settings.MaxDatabaseBytes < 1<<20 || settings.MaxDatabaseBytes > 8<<30 || settings.MaxContentBytes < settings.MaxDatabaseBytes || settings.MaxContentBytes > 16<<30 || settings.JobTimeoutSeconds < 60 || settings.JobTimeoutSeconds > 86400 {
		return serverBackupSettings{}, errors.New("invalid backup identity or resource limits")
	}
	if _, err := settings.targetOptions([16]byte{1}); err != nil {
		return serverBackupSettings{}, errors.New("invalid backup S3 target")
	}
	return settings, nil
}

func (settings serverBackupSettings) targetOptions(instance [16]byte) (backup.S3Options, error) {
	access, secret, token := []byte(settings.AccessKeyID), []byte(settings.SecretAccessKey), []byte(settings.SessionToken)
	defer clear(access)
	defer clear(secret)
	defer clear(token)
	credentials, err := backup.NewCredentials(access, secret, token)
	if err != nil {
		return backup.S3Options{}, err
	}
	return backup.NewS3Options(settings.Endpoint, settings.Region, settings.Bucket, settings.Prefix, instance, credentials, false)
}

func (settings serverBackupSettings) generation(master, recovery []byte) ([32]byte, error) {
	if len(master) != 32 || len(recovery) != 32 {
		return [32]byte{}, errors.New("invalid backup key material")
	}
	type settingsDocument serverBackupSettings
	encoded, err := json.Marshal(struct {
		Settings settingsDocument
		Content  library.ContentSnapshotLimits
		Seal     backup.SealLimits
		MaxJobs  uint32
		Version  string
	}{Settings: settingsDocument(settings), Content: settings.contentLimits(), Seal: settings.sealLimits(), MaxJobs: 1000, Version: serverBackupVersion()}, json.WithMarshalers(json.MarshalFunc(func(duration time.Duration) ([]byte, error) {
		return strconv.AppendInt(nil, int64(duration), 10), nil
	})), json.Deterministic(true))
	if err != nil {
		return [32]byte{}, errors.New("cannot bind backup configuration")
	}
	defer clear(encoded)
	commitment := hmac.New(sha256.New, master)
	_, _ = commitment.Write([]byte("cognate/server-backup/generation/v1\x00"))
	_, _ = commitment.Write(encoded)
	recoveryHash := sha256.Sum256(recovery)
	defer clear(recoveryHash[:])
	_, _ = commitment.Write(recoveryHash[:])
	var generation [32]byte
	copy(generation[:], commitment.Sum(nil))
	return generation, nil
}

func (settings serverBackupSettings) contentLimits() library.ContentSnapshotLimits {
	return library.ContentSnapshotLimits{
		Database: state.LocalSnapshotLimits{
			MaxDBBytes: settings.MaxDatabaseBytes, MaxInventoryBytes: 8 << 20, MaxStagingBytes: settings.MaxDatabaseBytes + 16<<20, MaxRecords: 100000,
			SQLiteHeapBytes: 64 << 20, WorkerAddressSpaceBytes: 16 << 30, WorkerCPUSeconds: 60, MaxWALBytes: 512 << 20, MaxSHMBytes: 8 << 20,
			OperationTimeout: min(120*time.Second, time.Duration(settings.JobTimeoutSeconds)*time.Second), DrainTimeout: 5 * time.Second,
		},
		MaxArchives: 10000, MaxInventoryBytes: 16 << 20, MaxContentBytes: settings.MaxContentBytes,
	}
}

func (settings serverBackupSettings) sealLimits() backup.SealLimits {
	return backup.SealLimits{
		TotalPlainBytes: settings.MaxContentBytes + 64<<20, TotalCipherBytes: settings.MaxContentBytes*2 + 64<<20, StagingBytes: settings.MaxContentBytes*2 + 256<<20,
		InputStagingBytes: 64 << 20, MaxObjects: 100000, MaxInventoryRecords: 100001,
		OperationTimeout: min(120*time.Second, time.Duration(settings.JobTimeoutSeconds)*time.Second),
	}
}

func serverBackupVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && len(info.Main.Version) > 0 && len(info.Main.Version) <= 64 {
		return info.Main.Version
	}
	return "dev"
}
