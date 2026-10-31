package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

type serverBackup struct {
	controller *backup.Controller
	control    *backup.ControlStore
	target     *backup.S3Target
	engine     backup.SealEngine
	key        backup.RecoveryKey
	borrowed   []io.Closer
	closeOnce  sync.Once
	closeError error
}

var uncertainServerBackups sync.Map
var uncertainBackupApps sync.Map

func openServerBackup(ctx context.Context, config Config, master []byte, service *library.Service) (result *serverBackup, returnedError error) {
	settings, err := readServerBackupSettings(config.BackupS3ConfigFile)
	if err != nil {
		return nil, err
	}
	recovery, err := state.ReadMasterKey(config.BackupRecoveryKeyFile)
	if err != nil {
		return nil, errors.New("backup recovery key must be a private file containing exactly 32 raw bytes")
	}
	defer clear(recovery)
	if subtle.ConstantTimeCompare(recovery, master) == 1 {
		return nil, errors.New("backup recovery key must be independent from the server master key")
	}
	runtime := &serverBackup{}
	defer func() {
		if returnedError != nil {
			for _, critical := range []error{backup.ErrControlUncertain, backup.ErrSealUncertain, backup.ErrSealCleanup} {
				if errors.Is(returnedError, critical) {
					runtime.closeOnce.Do(func() {
						runtime.closeError = backup.ErrControlUncertain
						uncertainServerBackups.Store(runtime, struct{}{})
					})
					result = runtime
					returnedError = errors.Join(returnedError, backup.ErrControlUncertain)
					return
				}
			}
			closeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if closeError := runtime.Close(closeContext); closeError != nil {
				result = runtime
				returnedError = errors.Join(returnedError, closeError)
			}
		}
	}()
	controlRoot, err := state.OpenPrivateDirectory(filepath.Join(config.DataDirectory, "backup", "control"))
	if err != nil {
		return nil, errors.New("cannot open private backup control directory")
	}
	runtime.borrowed = append(runtime.borrowed, controlRoot)
	runtime.control, err = backup.OpenControlStore(ctx, controlRoot, master)
	if err != nil {
		return nil, err
	}
	options, err := settings.targetOptions(runtime.control.InstanceID())
	if err != nil {
		return nil, errors.New("cannot configure backup target")
	}
	runtime.target, err = backup.NewS3Target(options)
	if err != nil {
		return nil, err
	}
	identity, _ := uuid.Parse(settings.RecoveryKeyID)
	runtime.key, err = backup.ImportRecoveryKey([16]byte(identity), recovery)
	if err != nil {
		return nil, err
	}
	stageRoot, err := state.OpenPrivateDirectory(filepath.Join(config.DataDirectory, "backup", "artifacts"))
	if err != nil {
		return nil, errors.New("cannot open private backup artifact directory")
	}
	runtime.borrowed = append(runtime.borrowed, stageRoot)
	canonical, err := filepath.EvalSymlinks(stageRoot.Name())
	if err != nil {
		return nil, errors.New("cannot resolve backup artifact directory")
	}
	parent, err := os.Open(canonical)
	if err != nil {
		return nil, errors.New("cannot pin backup artifact directory")
	}
	runtime.borrowed = append(runtime.borrowed, parent)
	pinned, pinError := parent.Stat()
	expected, rootError := stageRoot.Stat(".")
	if pinError != nil || rootError != nil || !os.SameFile(pinned, expected) {
		return nil, errors.New("backup artifact directory identity changed")
	}
	runtime.engine, err = backup.NewSealEngine(parent, runtime.key, settings.sealLimits())
	if err != nil {
		return nil, err
	}
	generation, err := settings.generation(master, recovery)
	if err != nil {
		return nil, err
	}
	runtime.controller, err = backup.NewController(ctx, runtime.control, service, runtime.target, runtime.engine, backup.ControllerOptions{Generation: generation, AppVersion: serverBackupVersion(), ContentLimits: settings.contentLimits(), MaxJobs: 1000, JobTimeout: time.Duration(settings.JobTimeoutSeconds) * time.Second})
	if err != nil {
		return nil, err
	}
	return runtime, nil
}

func (runtime *serverBackup) Close(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	runtime.closeOnce.Do(func() {
		defer func() {
			if runtime.closeError != nil {
				uncertainServerBackups.Store(runtime, struct{}{})
			}
		}()
		if runtime.controller != nil {
			if runtime.closeError = runtime.controller.Close(ctx); runtime.closeError != nil {
				return
			}
		}
		if runtime.target != nil {
			if runtime.closeError = runtime.target.Close(); runtime.closeError != nil {
				return
			}
		}
		if runtime.closeError = runtime.engine.Close(); runtime.closeError != nil {
			return
		}
		if runtime.closeError = runtime.key.Destroy(); runtime.closeError != nil {
			return
		}
		if runtime.control != nil {
			if runtime.closeError = runtime.control.Close(); runtime.closeError != nil {
				return
			}
		}
		for index := len(runtime.borrowed) - 1; index >= 0; index-- {
			if err := runtime.borrowed[index].Close(); err != nil {
				runtime.closeError = backup.ErrControlUncertain
				return
			}
		}
		runtime.borrowed = nil
	})
	return runtime.closeError
}
