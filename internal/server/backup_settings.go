package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
	"uuid"

	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

const backupSettingsAAD = "cognate/server-backup/settings/v1"

var backupSettingsOwners sync.Map
var ErrBackupSettingsOwned = errors.New("another server is using this data directory")

type backupSettingsDocument serverBackupSettings

type backupSettingsRecord struct {
	Version         int
	Revision        int64
	Enabled         bool
	RecoveryKeyFile string
	Settings        backupSettingsDocument
}

type backupSettingsManager struct {
	mutex     sync.Mutex
	config    Config
	master    []byte
	service   *library.Service
	root      *os.Root
	ownerPath string
	aead      cipher.AEAD
	record    backupSettingsRecord
	current   *serverBackup
	uncertain bool
	closed    bool
	lifetime  context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	wake      chan struct{}
}

func (*backupSettingsManager) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private backup settings manager]")
}

func (*backupSettingsManager) LogValue() slog.Value {
	return slog.StringValue("[private backup settings manager]")
}

func openBackupSettingsManager(ctx context.Context, config Config, master []byte, service *library.Service) (*backupSettingsManager, error) {
	root, err := state.OpenPrivateDirectory(filepath.Join(config.DataDirectory, "backup", "settings"))
	if err != nil {
		return nil, backup.ErrUnavailable
	}
	derived := sha256.Sum256(append([]byte(backupSettingsAAD+"\x00"), master...))
	block, err := aes.NewCipher(derived[:])
	clear(derived[:])
	if err != nil {
		_ = root.Close()
		return nil, backup.ErrUnavailable
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		_ = root.Close()
		return nil, backup.ErrUnavailable
	}
	manager := &backupSettingsManager{config: config, master: append([]byte(nil), master...), service: service, root: root, aead: aead, wake: make(chan struct{}, 1)}
	manager.ownerPath, err = filepath.EvalSymlinks(root.Name())
	if err != nil {
		clear(manager.master)
		_ = root.Close()
		return nil, backup.ErrUnavailable
	}
	if _, loaded := backupSettingsOwners.LoadOrStore(manager.ownerPath, manager); loaded {
		clear(manager.master)
		_ = root.Close()
		return nil, ErrBackupSettingsOwned
	}
	manager.record = backupSettingsRecord{Version: 1, Revision: 1, Settings: backupSettingsDocument{MaxDatabaseBytes: 256 << 20, MaxContentBytes: 1 << 30, JobTimeoutSeconds: 900}}
	data, err := state.ReadPrivateFile(filepath.Join(root.Name(), "settings.enc"), 128<<10)
	if err == nil {
		defer clear(data)
		plaintext, openError := aead.Open(nil, nil, data, []byte(backupSettingsAAD))
		if openError != nil {
			err = backup.ErrUnavailable
		} else {
			defer clear(plaintext)
			err = json.Unmarshal(plaintext, &manager.record, json.RejectUnknownMembers(true))
			if err == nil && (manager.record.Version != 1 || manager.record.Revision < 1) {
				err = backup.ErrInvalid
			}
		}
	} else if _, statError := root.Lstat("settings.enc"); errors.Is(statError, os.ErrNotExist) {
		err = nil
		if config.BackupEnabled {
			var settings serverBackupSettings
			settings, err = readServerBackupSettings(config.BackupS3ConfigFile)
			manager.record.Settings, manager.record.Enabled, manager.record.RecoveryKeyFile = backupSettingsDocument(settings), true, config.BackupRecoveryKeyFile
		}
	}
	var runtimeError error
	if err == nil && manager.record.Enabled {
		var settings serverBackupSettings
		settings, err = validateServerBackupSettings(serverBackupSettings(manager.record.Settings))
		if err == nil {
			manager.current, err = manager.open(ctx, manager.record, settings)
			runtimeError = err
		}
	}
	if err != nil {
		if manager.current != nil {
			manager.uncertain = true
			uncertainServerBackups.Store(manager, struct{}{})
			return manager, backup.ErrControlUncertain
		}
		clear(manager.master)
		if err := root.Close(); err != nil {
			manager.uncertain = true
			return manager, backup.ErrControlUncertain
		}
		backupSettingsOwners.CompareAndDelete(manager.ownerPath, manager)
		if runtimeError != nil {
			return nil, runtimeError
		}
		return nil, backup.ErrUnavailable
	}
	return manager, nil
}

func (manager *backupSettingsManager) open(ctx context.Context, record backupSettingsRecord, settings serverBackupSettings) (*serverBackup, error) {
	config := manager.config
	config.BackupRecoveryKeyFile = record.RecoveryKeyFile
	return openServerBackupSettings(ctx, config, manager.master, manager.service, settings)
}

func (manager *backupSettingsManager) view() *managementv1.BackupSettings {
	record, settings := manager.record, manager.record.Settings
	return &managementv1.BackupSettings{Enabled: record.Enabled, Revision: record.Revision, AutomaticIntervalSeconds: settings.AutomaticIntervalSeconds, Endpoint: settings.Endpoint, Region: settings.Region, Bucket: settings.Bucket, Prefix: settings.Prefix, RecoveryKeyId: settings.RecoveryKeyID, RecoveryKeyFile: record.RecoveryKeyFile, MaxDatabaseBytes: settings.MaxDatabaseBytes, MaxContentBytes: settings.MaxContentBytes, JobTimeoutSeconds: settings.JobTimeoutSeconds, CredentialsConfigured: settings.AccessKeyID != "" && settings.SecretAccessKey != ""}
}

func (manager *backupSettingsManager) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if manager.uncertain {
		return backup.ErrControlUncertain
	}
	if manager.closed {
		return backup.ErrClosed
	}
	return nil
}

func (manager *backupSettingsManager) GetBackupSettings(ctx context.Context) (*managementv1.BackupSettings, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if err := manager.check(ctx); err != nil {
		return nil, err
	}
	return manager.view(), nil
}

func (manager *backupSettingsManager) candidate(request *managementv1.UpdateBackupSettingsRequest) (backupSettingsRecord, error) {
	if request == nil || request.Settings == nil || request.ExpectedRevision < 1 {
		return backupSettingsRecord{}, backup.ErrInvalid
	}
	if request.ExpectedRevision != manager.record.Revision {
		return backupSettingsRecord{}, backup.ErrControlConflict
	}
	if manager.record.Revision == math.MaxInt64 {
		return backupSettingsRecord{}, backup.ErrControlBudget
	}
	input := request.Settings
	record := manager.record
	record.Revision++
	record.Enabled, record.RecoveryKeyFile = input.Enabled, input.RecoveryKeyFile
	settings := serverBackupSettings(record.Settings)
	settings.Endpoint, settings.Region, settings.Bucket, settings.Prefix = input.Endpoint, input.Region, input.Bucket, input.Prefix
	settings.RecoveryKeyID, settings.AutomaticIntervalSeconds = input.RecoveryKeyId, input.AutomaticIntervalSeconds
	if input.MaxDatabaseBytes != 0 {
		settings.MaxDatabaseBytes = input.MaxDatabaseBytes
	}
	if input.MaxContentBytes != 0 {
		settings.MaxContentBytes = input.MaxContentBytes
	}
	if input.JobTimeoutSeconds != 0 {
		settings.JobTimeoutSeconds = input.JobTimeoutSeconds
	}
	switch request.CredentialsAction {
	case "keep":
		if request.AccessKeyId != "" || request.SecretAccessKey != "" || request.SessionToken != "" {
			return backupSettingsRecord{}, backup.ErrInvalid
		}
	case "replace":
		settings.AccessKeyID, settings.SecretAccessKey, settings.SessionToken = request.AccessKeyId, request.SecretAccessKey, request.SessionToken
	default:
		return backupSettingsRecord{}, backup.ErrInvalid
	}
	if settings.AutomaticIntervalSeconds != 0 && (settings.AutomaticIntervalSeconds < 60 || settings.AutomaticIntervalSeconds > 86400) {
		return backupSettingsRecord{}, backup.ErrInvalid
	}
	retained := serverBackupSettings(manager.record.Settings)
	retained.AutomaticIntervalSeconds = settings.AutomaticIntervalSeconds
	if !record.Enabled && settings == retained && record.RecoveryKeyFile == manager.record.RecoveryKeyFile && request.CredentialsAction == "keep" {
		record.Settings = backupSettingsDocument(settings)
		return record, nil
	}
	validated, err := validateServerBackupSettings(settings)
	if err != nil {
		return backupSettingsRecord{}, backup.ErrInvalid
	}
	if !filepath.IsAbs(record.RecoveryKeyFile) || filepath.Clean(record.RecoveryKeyFile) != record.RecoveryKeyFile {
		return backupSettingsRecord{}, backup.ErrInvalid
	}
	recovery, err := state.ReadMasterKey(record.RecoveryKeyFile)
	if err != nil {
		return backupSettingsRecord{}, backup.ErrInvalid
	}
	defer clear(recovery)
	if subtle.ConstantTimeCompare(recovery, manager.master) == 1 {
		return backupSettingsRecord{}, backup.ErrInvalid
	}
	if validated.AutomaticIntervalSeconds != 0 && len(manager.config.AdminUserIDs) == 0 {
		return backupSettingsRecord{}, backup.ErrInvalid
	}
	record.Settings = backupSettingsDocument(validated)
	return record, nil
}

func (manager *backupSettingsManager) UpdateBackupSettings(ctx context.Context, request *managementv1.UpdateBackupSettingsRequest) (*managementv1.BackupSettings, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if err := manager.check(ctx); err != nil {
		return nil, err
	}
	record, err := manager.candidate(request)
	if err != nil {
		return nil, err
	}
	if manager.current != nil {
		if err := manager.current.controller.CheckReconfiguration(ctx); err != nil {
			return nil, err
		}
	}
	operation, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if manager.current != nil {
		if err := manager.current.Close(operation); err != nil {
			manager.uncertain = true
			return nil, backup.ErrControlUncertain
		}
		manager.current = nil
	}
	if record.Enabled {
		manager.current, err = manager.open(operation, record, serverBackupSettings(record.Settings))
		if err != nil {
			return nil, manager.rollback(operation, err)
		}
	}
	ambiguous, err := manager.persist(record)
	if err != nil {
		if ambiguous {
			manager.uncertain = true
			return nil, backup.ErrControlUncertain
		}
		if manager.current != nil {
			if closeError := manager.current.Close(operation); closeError != nil {
				manager.uncertain = true
				return nil, backup.ErrControlUncertain
			}
			manager.current = nil
		}
		return nil, manager.rollback(operation, err)
	}
	manager.record = record
	select {
	case manager.wake <- struct{}{}:
	default:
	}
	return manager.view(), nil
}

func (manager *backupSettingsManager) rollback(ctx context.Context, cause error) error {
	if manager.current != nil {
		manager.uncertain = true
		return backup.ErrControlUncertain
	}
	if manager.record.Enabled {
		var err error
		manager.current, err = manager.open(ctx, manager.record, serverBackupSettings(manager.record.Settings))
		if err != nil {
			manager.uncertain = true
			return backup.ErrControlUncertain
		}
	}
	return cause
}

func (manager *backupSettingsManager) persist(record backupSettingsRecord) (bool, error) {
	plaintext, err := json.Marshal(record)
	if err != nil {
		return false, backup.ErrUnavailable
	}
	defer clear(plaintext)
	sealed := manager.aead.Seal(nil, nil, plaintext, []byte(backupSettingsAAD))
	defer clear(sealed)
	name := "settings-" + uuid.New().String() + ".tmp"
	file, err := manager.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false, backup.ErrUnavailable
	}
	defer func() { _ = manager.root.Remove(name) }()
	written, writeError := file.Write(sealed)
	if writeError == nil && written != len(sealed) {
		writeError = io.ErrShortWrite
	}
	if writeError == nil {
		writeError = file.Sync()
	}
	closeError := file.Close()
	if writeError != nil || closeError != nil {
		return false, backup.ErrUnavailable
	}
	if err := manager.root.Rename(name, "settings.enc"); err != nil {
		return true, backup.ErrControlUncertain
	}
	directory, err := manager.root.Open(".")
	if err != nil {
		return true, backup.ErrControlUncertain
	}
	syncError := directory.Sync()
	closeError = directory.Close()
	if syncError != nil || closeError != nil {
		return true, backup.ErrControlUncertain
	}
	return false, nil
}

func (manager *backupSettingsManager) Enabled() bool {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	return manager.current != nil && !manager.closed && !manager.uncertain
}

func (manager *backupSettingsManager) AutomaticEnabled() bool {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	return manager.current != nil && !manager.closed && !manager.uncertain && manager.record.Settings.AutomaticIntervalSeconds != 0
}

func (manager *backupSettingsManager) controller(ctx context.Context) (*backup.Controller, error) {
	if err := manager.check(ctx); err != nil {
		return nil, err
	}
	if manager.current == nil {
		return nil, backup.ErrClosed
	}
	return manager.current.controller, nil
}

func (manager *backupSettingsManager) Start(ctx context.Context, principal, request string) (backup.Job, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	controller, err := manager.controller(ctx)
	if err != nil {
		return backup.Job{}, err
	}
	return controller.Start(ctx, principal, request)
}

func (manager *backupSettingsManager) Get(ctx context.Context, id string) (backup.Job, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	controller, err := manager.controller(ctx)
	if err != nil {
		return backup.Job{}, err
	}
	return controller.Get(ctx, id)
}

func (manager *backupSettingsManager) List(ctx context.Context, limit int, cursor string) (backup.JobPage, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	controller, err := manager.controller(ctx)
	if err != nil {
		return backup.JobPage{}, err
	}
	return controller.List(ctx, limit, cursor)
}

func (manager *backupSettingsManager) Cancel(ctx context.Context, principal, id string) (backup.Job, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	controller, err := manager.controller(ctx)
	if err != nil {
		return backup.Job{}, err
	}
	return controller.Cancel(ctx, principal, id)
}

func (manager *backupSettingsManager) Reconcile(ctx context.Context, principal, id string) (backup.Job, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	controller, err := manager.controller(ctx)
	if err != nil {
		return backup.Job{}, err
	}
	return controller.Reconcile(ctx, principal, id)
}

func (manager *backupSettingsManager) StartAutomatic(ctx context.Context) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if err := manager.check(ctx); err != nil {
		return err
	}
	if manager.done != nil {
		return backup.ErrBusy
	}
	manager.lifetime, manager.cancel = context.WithCancel(ctx)
	manager.done = make(chan struct{})
	go manager.automaticLoop(manager.lifetime, manager.done)
	return nil
}

func (manager *backupSettingsManager) automaticLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	backupSettingsSchedule(ctx, manager.wake, func() (time.Duration, int64) {
		manager.mutex.Lock()
		defer manager.mutex.Unlock()
		interval := time.Duration(manager.record.Settings.AutomaticIntervalSeconds) * time.Second
		if manager.current == nil || manager.closed || manager.uncertain {
			interval = 0
		}
		return interval, manager.record.Revision
	}, manager.automaticStep)
}

func backupSettingsSchedule(ctx context.Context, wake <-chan struct{}, policy func() (time.Duration, int64), step func(context.Context, int64)) {
	for {
		interval, revision := policy()
		var tick <-chan time.Time
		var timer *time.Timer
		if interval != 0 {
			timer = time.NewTimer(interval)
			tick = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-wake:
			if timer != nil {
				timer.Stop()
			}
		case <-tick:
			step(ctx, revision)
		}
	}
}

func (manager *backupSettingsManager) automaticStep(ctx context.Context, revision int64) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	controller, err := manager.controller(ctx)
	if err != nil || manager.record.Settings.AutomaticIntervalSeconds == 0 || manager.record.Revision != revision {
		return
	}
	if err := controller.AutomaticStep(ctx); err != nil && !errors.Is(err, backup.ErrBusy) && !errors.Is(err, backup.ErrControlConflict) && !errors.Is(err, backup.ErrControlBudget) && ctx.Err() == nil {
		manager.uncertain = true
	}
}

func (manager *backupSettingsManager) StopAutomatic(ctx context.Context) error {
	if err := manager.lock(ctx); err != nil {
		return errors.Join(backup.ErrControlUncertain, err)
	}
	if manager.cancel != nil {
		manager.cancel()
	}
	done := manager.done
	manager.mutex.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return backup.ErrControlUncertain
	}
}

func (manager *backupSettingsManager) Close(ctx context.Context) error {
	if err := manager.StopAutomatic(ctx); err != nil {
		return err
	}
	if err := manager.lock(ctx); err != nil {
		return errors.Join(backup.ErrControlUncertain, err)
	}
	defer manager.mutex.Unlock()
	if manager.uncertain {
		uncertainServerBackups.Store(manager, struct{}{})
		return backup.ErrControlUncertain
	}
	if manager.closed {
		return nil
	}
	manager.closed = true
	if manager.current != nil {
		if err := manager.current.Close(ctx); err != nil {
			manager.uncertain = true
			uncertainServerBackups.Store(manager, struct{}{})
			return err
		}
	}
	clear(manager.master)
	manager.record.Settings.AccessKeyID, manager.record.Settings.SecretAccessKey, manager.record.Settings.SessionToken = "", "", ""
	manager.aead = nil
	if err := manager.root.Close(); err != nil {
		manager.uncertain = true
		uncertainServerBackups.Store(manager, struct{}{})
		return backup.ErrControlUncertain
	}
	backupSettingsOwners.CompareAndDelete(manager.ownerPath, manager)
	return nil
}

func (manager *backupSettingsManager) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if manager.mutex.TryLock() {
		return nil
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if manager.mutex.TryLock() {
				return nil
			}
		}
	}
}
