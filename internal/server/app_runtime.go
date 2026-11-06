package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/server/state"
)

type appRuntimeHooks struct {
	observe func(string)
	close   func(string, error) error
	store   func(context.Context, string, []byte, state.Options) (*state.Store, error)
}

type appRuntime struct {
	lease      *runtimegen.Runtime
	life       context.Context
	cancel     context.CancelFunc
	mutex      sync.Mutex
	closing    bool
	serving    bool
	serveDone  chan struct{}
	listener   *runtimeListener
	requests   sync.WaitGroup
	uncertain  bool
	closeOnce  sync.Once
	closeError error
	hooks      appRuntimeHooks
}

var uncertainRuntimeApps sync.Map

type runtimeListener struct {
	net.Listener
	once sync.Once
	err  error
}

func (listener *runtimeListener) Close() error {
	listener.once.Do(func() { listener.err = closeRuntimeListener(listener.Listener) })
	return listener.err
}

func (app *App) observeRuntime(stage string) {
	if app.runtime.hooks.observe != nil {
		app.runtime.hooks.observe(stage)
	}
}

func (app *App) openRuntime(ctx context.Context, root string) error {
	if err := state.PrepareDirectory(root); err != nil {
		return err
	}
	var err error
	if runtime.GOOS == "linux" {
		app.runtime.lease, err = runtimegen.OpenRuntime(ctx, root)
		if err == nil {
			app.observeRuntime("lease")
			err = app.runtime.lease.CheckLegacy(ctx, root)
		}
	} else {
		err = runtimegen.CheckLegacyLayout(ctx, root)
	}
	return err
}

func (app *App) markRuntimeUncertain() {
	app.runtime.mutex.Lock()
	app.runtime.uncertain = true
	app.runtime.mutex.Unlock()
}

func (app *App) closeRuntimeResource(stage string, closeResource func() error) error {
	app.observeRuntime(stage + "-closing")
	err := closeResource()
	app.observeRuntime(stage + "-closed")
	if app.runtime.hooks.close != nil && app.runtime.hooks.close(stage, err) != nil {
		err = errors.Join(err, runtimegen.ErrUncertain)
	}
	return err
}

func (app *App) beginRuntimeServe(ctx context.Context, listener *runtimeListener) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, runtimegen.ErrInvalid
	}
	app.runtime.mutex.Lock()
	defer app.runtime.mutex.Unlock()
	if app.runtime.closing || app.runtime.serving || app.runtime.uncertain || app.runtime.life == nil {
		return nil, nil, runtimegen.ErrInvalid
	}
	app.runtime.serving = true
	app.runtime.listener = listener
	app.runtime.serveDone = make(chan struct{})
	scoped, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(app.runtime.life, cancel)
	return scoped, func() {
		stop()
		cancel()
		app.observeRuntime("serve-tail")
		app.runtime.mutex.Lock()
		close(app.runtime.serveDone)
		app.runtime.serving = false
		app.runtime.mutex.Unlock()
	}, nil
}

func (app *App) runtimeHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		app.runtime.mutex.Lock()
		if app.runtime.closing {
			app.runtime.mutex.Unlock()
			http.Error(writer, "server is closed", http.StatusServiceUnavailable)
			return
		}
		app.runtime.requests.Add(1)
		app.runtime.mutex.Unlock()
		defer app.runtime.requests.Done()
		app.handler.ServeHTTP(writer, request)
	})
}

func (app *App) Close() error {
	if app == nil {
		return nil
	}
	app.runtime.closeOnce.Do(func() {
		app.runtime.mutex.Lock()
		app.runtime.closing = true
		if app.runtime.cancel != nil {
			app.runtime.cancel()
		}
		serveDone := app.runtime.serveDone
		app.runtime.mutex.Unlock()
		result := app.closeRuntimeDependents()
		if result == nil {
			if serveDone != nil {
				<-serveDone
			}
			app.runtime.requests.Wait()
			var shutdown sync.WaitGroup
			var libraryError error
			if app.packs != nil {
				shutdown.Go(app.packs.Close)
			}
			if app.library != nil {
				shutdown.Go(func() {
					libraryError = app.closeRuntimeResource("library", func() error { return app.closeRuntimeResource("cache", app.library.Close) })
				})
			} else if app.cache != nil {
				shutdown.Go(func() { libraryError = app.closeRuntimeResource("cache", app.cache.Close) })
			}
			if app.device != nil {
				shutdown.Go(app.device.Close)
			}
			if app.oidc != nil {
				shutdown.Go(app.oidc.Close)
			}
			shutdown.Wait()
			result = libraryError
			if result == nil && app.store != nil {
				result = app.closeRuntimeResource("store", app.store.Close)
			}
			if result == nil && app.logs != nil {
				result = app.closeRuntimeResource("logs", app.logs.Close)
			}
		}
		app.runtime.mutex.Lock()
		uncertain := app.runtime.uncertain
		app.runtime.mutex.Unlock()
		if result != nil || uncertain {
			app.runtime.closeError = errors.Join(result, runtimegen.ErrUncertain)
			uncertainRuntimeApps.Store(app, struct{}{})
			return
		}
		if app.runtime.lease != nil {
			app.runtime.closeError = app.closeRuntimeResource("lease", app.runtime.lease.Close)
			if app.runtime.closeError != nil {
				uncertainRuntimeApps.Store(app, struct{}{})
			}
		}
	})
	return app.runtime.closeError
}

func (app *App) closeRuntimeDependents() error {
	var result error
	if app.backupSettings != nil || app.backups != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := app.closeRuntimeResource("backup", func() error {
			if app.backupSettings != nil {
				return app.backupSettings.Close(ctx)
			}
			return app.backups.Close(ctx)
		})
		cancel()
		if err != nil {
			uncertainBackupApps.Store(app, struct{}{})
			result = errors.Join(result, err)
		}
	}
	if app.mcpConfiguration != nil {
		result = errors.Join(result, app.mcpConfiguration.Close())
	}
	if app.native != nil {
		app.native.Close()
	}
	if app.sourceRefresh != nil {
		result = errors.Join(result, app.sourceRefresh.Close())
	}
	if app.scheduler != nil {
		result = errors.Join(result, app.scheduler.Close())
	}
	if app.imports != nil {
		result = errors.Join(result, app.imports.Close())
	} else if app.selection != nil {
		result = errors.Join(result, app.selection.Close())
	}
	if app.mcp != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		result = errors.Join(result, app.mcp.Close(ctx))
		cancel()
	}
	if app.public != nil {
		result = errors.Join(result, app.public.Close())
	}
	return result
}

func closeRuntimeListener(listener net.Listener) error {
	err := listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
