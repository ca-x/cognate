package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/githubauth"
	"github.com/ca-x/cognate/internal/server/githubproxysettings"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/logging"
	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/nativeconsumer"
	"github.com/ca-x/cognate/internal/server/oidclogin"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/server/scheduler"
	"github.com/ca-x/cognate/internal/server/sourcerefresh"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/web"
)

type Config struct {
	AdminUserIDs               []string
	BootstrapAdminUsername     string
	BootstrapAdminPasswordFile string
	DataDirectory              string
	MasterKeyFile              string
	InitializeMasterKey        bool
	BackupEnabled              bool
	BackupS3ConfigFile         string
	BackupRecoveryKeyFile      string
	TLSCertificate             string
	TLSKey                     string
	AllowHTTP                  bool
	PublicOrigin               string
	GitHubClientID             string
	OIDCIssuer                 string
	OIDCClientID               string
	OIDCClientSecretFile       string
	ArchiveCacheBytes          int64
	ListSyncInterval           time.Duration
	SourceRefreshInterval      *time.Duration
}

type App struct {
	runtime          appRuntime
	cache            *library.ArchiveCache
	backups          *serverBackup
	backupSettings   *backupSettingsManager
	githubProxy      *githubproxysettings.Manager
	logs             *logging.Manager
	store            *state.Store
	handler          http.Handler
	tlsConfig        *tls.Config
	allowHTTP        bool
	device           *githubauth.Service
	oidc             *oidclogin.Manager
	library          *library.Service
	packs            *packs.Service
	mcp              *mcpserver.Server
	public           *publiccatalog.Service
	scheduler        *scheduler.Scheduler
	sourceRefresh    *sourcerefresh.Worker
	imports          *httpapi.ImportHandler
	selection        *importer.SelectionService
	native           *nativeconsumer.Service
	mcpConfiguration *httpapi.MCPConfigurationHandler
}

func Open(ctx context.Context, config Config) (*App, error) {
	return openApp(ctx, config, appRuntimeHooks{})
}

func openApp(ctx context.Context, config Config, hooks appRuntimeHooks) (result *App, returnedError error) {
	if ctx == nil {
		return nil, fmt.Errorf("server context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateServerBackupConfig(ctx, config); err != nil {
		return nil, err
	}
	if len(config.AdminUserIDs) > 32 {
		return nil, fmt.Errorf("invalid administrator user IDs")
	}
	if (config.BootstrapAdminUsername == "") != (config.BootstrapAdminPasswordFile == "") {
		return nil, fmt.Errorf("initial administrator username and password file must be configured together")
	}
	if config.BootstrapAdminUsername != "" && len(config.AdminUserIDs) == 32 {
		return nil, fmt.Errorf("administrator initialization requires a free administrator slot")
	}
	admins := make(map[string]struct{}, len(config.AdminUserIDs))
	for _, id := range config.AdminUserIDs {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return nil, fmt.Errorf("administrator user IDs must be canonical account UUIDs")
		}
		if _, duplicate := admins[id]; duplicate {
			return nil, fmt.Errorf("duplicate administrator user ID")
		}
		admins[id] = struct{}{}
	}
	if config.ListSyncInterval < 0 || config.ListSyncInterval > 24*time.Hour {
		return nil, fmt.Errorf("GitHub list synchronization interval must be between zero and 24 hours")
	}
	sourceInterval := sourcerefresh.DefaultInterval
	if config.SourceRefreshInterval != nil {
		sourceInterval = *config.SourceRefreshInterval
	}
	if sourceInterval != 0 && (sourceInterval < time.Second || sourceInterval > 24*time.Hour) {
		return nil, fmt.Errorf("source refresh interval must be zero or between one second and 24 hours")
	}
	if !filepath.IsAbs(config.DataDirectory) || filepath.Clean(config.DataDirectory) != config.DataDirectory {
		return nil, fmt.Errorf("server data directory must be absolute and clean")
	}
	if (config.TLSCertificate == "") != (config.TLSKey == "") {
		return nil, fmt.Errorf("TLS certificate and key must be configured together")
	}
	if config.PublicOrigin != "" {
		origin, err := url.Parse(config.PublicOrigin)
		if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.ForceQuery {
			return nil, fmt.Errorf("public origin must contain only an http(s) scheme and host")
		}
	}
	var tlsConfig *tls.Config
	if config.TLSCertificate != "" {
		keyPath, err := filepath.Abs(config.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("invalid TLS key path")
		}
		keyData, err := state.ReadPrivateFile(keyPath, 1<<20)
		if err != nil {
			return nil, fmt.Errorf("TLS private key must be a private, owned regular file in a trusted directory")
		}
		defer clear(keyData)
		certificateData, err := os.ReadFile(config.TLSCertificate)
		if err != nil {
			return nil, fmt.Errorf("cannot read TLS certificate")
		}
		certificate, err := tls.X509KeyPair(certificateData, keyData)
		if err != nil {
			return nil, fmt.Errorf("cannot load TLS certificate and key")
		}
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	}
	app := &App{tlsConfig: tlsConfig, allowHTTP: config.AllowHTTP}
	app.runtime.hooks = hooks
	app.runtime.life, app.runtime.cancel = context.WithCancel(context.Background())
	defer func() {
		if returnedError != nil {
			returnedError = errors.Join(returnedError, app.Close())
		}
	}()
	if err := app.openRuntime(ctx, config.DataDirectory); err != nil {
		return nil, err
	}
	var key []byte
	var err error
	if config.InitializeMasterKey {
		if app.runtime.lease != nil {
			key, err = state.InitializeMasterKeyWithRuntime(ctx, config.MasterKeyFile, config.DataDirectory, app.runtime.lease)
		} else {
			key, err = state.InitializeMasterKey(ctx, config.MasterKeyFile, config.DataDirectory)
		}
	} else {
		key, err = state.ReadMasterKey(config.MasterKeyFile)
	}
	if err != nil {
		app.markRuntimeUncertain()
		return nil, err
	}
	defer clear(key)
	app.observeRuntime("store-opening")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	openStore := state.OpenWithOptions
	if hooks.store != nil {
		openStore = hooks.store
	}
	managedBackup := runtime.GOOS == "linux" && maintenance.CheckPlatform(ctx) == nil
	store, err := openStore(ctx, filepath.Join(config.DataDirectory, "server.db"), key, state.Options{MaintenanceManaged: managedBackup, Runtime: app.runtime.lease})
	app.store = store
	if err != nil {
		app.markRuntimeUncertain()
		if managedBackup && errors.Is(err, maintenance.ErrLock) {
			return nil, fmt.Errorf("another server is using this data directory or maintenance ownership is unavailable: %w", err)
		}
		return nil, err
	}
	service, err := auth.NewService(store, key)
	if err != nil {
		return nil, err
	}
	if config.BootstrapAdminUsername != "" {
		administrator, err := service.BootstrapAdministrator(ctx, config.BootstrapAdminUsername, config.BootstrapAdminPasswordFile)
		if err != nil {
			return nil, err
		}
		if _, exists := admins[administrator]; !exists {
			config.AdminUserIDs = append(config.AdminUserIDs, administrator)
		}
	}
	var device *githubauth.Service
	if config.GitHubClientID != "" {
		device, err = githubauth.NewService(store, config.GitHubClientID, nil)
		app.device = device
		if err != nil {
			app.markRuntimeUncertain()
			return nil, err
		}
	}
	var login *oidclogin.Manager
	if config.OIDCIssuer != "" || config.OIDCClientID != "" || config.OIDCClientSecretFile != "" {
		var secret []byte
		if config.OIDCClientSecretFile != "" {
			secret, err = state.ReadPrivateFile(config.OIDCClientSecretFile, 4096)
		}
		if err == nil {
			login, err = oidclogin.New(ctx, service, oidclogin.Config{Issuer: config.OIDCIssuer, ClientID: config.OIDCClientID, ClientSecret: strings.TrimRight(string(secret), "\r\n"), PublicOrigin: config.PublicOrigin})
			app.oidc = login
		}
		clear(secret)
		if err != nil {
			app.markRuntimeUncertain()
			return nil, err
		}
	}
	cache, err := library.OpenArchiveCacheWithOptions(ctx, filepath.Join(config.DataDirectory, "archives"), library.CacheOptions{MaxBytes: config.ArchiveCacheBytes, MaintenanceManaged: managedBackup})
	app.cache = cache
	if err != nil {
		app.markRuntimeUncertain()
		return nil, err
	}
	app.githubProxy, err = githubproxysettings.Open(ctx, config.DataDirectory, key)
	if err != nil {
		if errors.Is(err, githubproxysettings.ErrOwned) {
			return nil, ErrBackupSettingsOwned
		}
		return nil, fmt.Errorf("cannot initialize GitHub proxy settings: %w", err)
	}
	archiveClient := &http.Client{Transport: app.githubProxy}
	if hooks.archiveClient != nil {
		archiveClient = hooks.archiveClient
	}
	fetcher := repository.NewArchiveFetcher(archiveClient)
	collection, err := library.NewService(store, cache, fetcher)
	app.library = collection
	if err != nil {
		app.markRuntimeUncertain()
		return nil, err
	}
	app.cache = nil
	distribution, err := packs.NewService(store, collection, key)
	app.packs = distribution
	if err != nil {
		app.markRuntimeUncertain()
		return nil, err
	}
	var listScheduler *scheduler.Scheduler
	if config.ListSyncInterval != 0 {
		listScheduler, err = scheduler.New(store, cache, fetcher, nil, scheduler.Options{Interval: config.ListSyncInterval})
		app.scheduler = listScheduler
		if err != nil {
			app.markRuntimeUncertain()
			return nil, err
		}
	}
	if managedBackup {
		app.backupSettings, err = openBackupSettingsManager(ctx, config, key, collection)
		if err != nil {
			if !errors.Is(err, ErrBackupSettingsOwned) {
				app.markRuntimeUncertain()
			}
			return nil, err
		}
		app.backups = app.backupSettings.current
	}
	app.mcpConfiguration, err = httpapi.NewMCPConfigurationHandler(service, store)
	if err != nil {
		app.markRuntimeUncertain()
		return nil, err
	}
	if sourceInterval != 0 {
		app.sourceRefresh, err = sourcerefresh.New(store, collection, sourcerefresh.Options{Interval: sourceInterval, DataDirectory: config.DataDirectory})
		if err != nil {
			app.markRuntimeUncertain()
			return nil, err
		}
	}
	selection, selectionErr := importer.NewSelectionService(store, collection, service, config.PublicOrigin)
	app.selection = selection
	if selectionErr != nil {
		app.markRuntimeUncertain()
		return nil, selectionErr
	}
	app.imports, err = httpapi.NewImportHandlerWithSelection(service, collection, selection)
	if err != nil {
		app.markRuntimeUncertain()
		return nil, err
	}
	app.selection = nil
	access, err := mcpaccess.NewService(store)
	if err != nil {
		return nil, err
	}
	app.mcp, err = mcpserver.New(mcpserver.Options{Access: access, Store: store, Library: collection, PublicOrigin: config.PublicOrigin})
	if err != nil {
		app.markRuntimeUncertain()
		return nil, err
	}
	app.public, err = publiccatalog.NewService(store, collection)
	if err != nil {
		app.markRuntimeUncertain()
		return nil, err
	}
	if tlsConfig != nil && strings.HasPrefix(config.PublicOrigin, "https://") && nativesessionauth.ValidServerOrigin(config.PublicOrigin) {
		app.native, err = nativeconsumer.New(store, service, key, config.PublicOrigin)
		if err != nil {
			app.markRuntimeUncertain()
			return nil, err
		}
	}
	app.logs, err = logging.Open(ctx, config.DataDirectory)
	if err != nil {
		app.markRuntimeUncertain()
		return nil, fmt.Errorf("cannot initialize server logging")
	}
	options := httpapi.Options{MCPConfiguration: app.mcpConfiguration, Logging: app.logs, AdminUserIDs: config.AdminUserIDs, PublicOrigin: config.PublicOrigin, Store: store, WebUI: web.Handler(), GitHubDevice: device, OIDC: login, Library: collection, Packs: distribution, MCPAccess: access, MCP: app.mcp, PublicCatalog: app.public, Imports: app.imports, Configuration: configuration.New(store), NativeConfiguration: app.native}
	options.GitHubProxy = app.githubProxy
	if app.backupSettings != nil {
		options.Backups = app.backupSettings
	}
	app.handler = httpapi.NewWithOptions(service, options)
	return app, nil
}

func (app *App) Serve(ctx context.Context, listener net.Listener) (result error) {
	if listener == nil {
		return fmt.Errorf("server listener is required")
	}
	ownedListener := &runtimeListener{Listener: listener}
	listener = ownedListener
	scoped, finish, err := app.beginRuntimeServe(ctx, ownedListener)
	if err != nil {
		_ = listener.Close()
		return err
	}
	defer finish()
	ctx = scoped
	defer func() {
		if err := app.closeRuntimeResource("listener", func() error { return closeRuntimeListener(listener) }); err != nil {
			app.markRuntimeUncertain()
			result = errors.Join(result, runtimegen.ErrUncertain)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if app.tlsConfig == nil && !app.allowHTTP {
		address, ok := listener.Addr().(*net.TCPAddr)
		if !ok || !address.IP.IsLoopback() {
			return fmt.Errorf("non-loopback HTTP requires TLS or explicit --allow-http for a trusted private network")
		}
	}
	defer func() {
		if app.logs != nil {
			kind := logging.EventServerStopped
			if result != nil {
				kind = logging.EventServerFailed
			}
			_ = app.logs.Record(logging.Event{Kind: kind})
		}
	}()
	if app.native != nil {
		if err := app.native.Start(ctx); err != nil {
			return err
		}
		defer app.native.Close()
	}
	if app.scheduler != nil {
		if err := app.scheduler.Start(ctx); err != nil {
			return err
		}
		defer func() {
			if err := app.scheduler.Close(); err != nil {
				app.markRuntimeUncertain()
				result = errors.Join(result, err)
			}
		}()
	}
	if app.sourceRefresh != nil {
		if err := app.sourceRefresh.Start(ctx); err != nil {
			return err
		}
		defer func() {
			if err := app.sourceRefresh.Close(); err != nil {
				app.markRuntimeUncertain()
				result = errors.Join(result, err)
			}
		}()
	}
	if app.backupSettings != nil {
		if err := app.backupSettings.StartAutomatic(ctx); err != nil {
			return err
		}
		defer func() {
			stopContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := app.backupSettings.StopAutomatic(stopContext); err != nil {
				app.markRuntimeUncertain()
				result = errors.Join(result, err)
			}
		}()
	}
	httpServer := &http.Server{
		Handler: app.runtimeHandler(), TLSConfig: app.tlsConfig,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx },
		ErrorLog:    log.New(io.Discard, "", 0),
	}
	if app.logs != nil {
		httpServer.ErrorLog = app.logs.HTTPErrorLog()
		_ = app.logs.Record(logging.Event{Kind: logging.EventServerStarted})
	}
	defer func() {
		if err := httpServer.Close(); err != nil {
			app.markRuntimeUncertain()
			result = errors.Join(result, runtimegen.ErrUncertain)
		}
	}()
	finished := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := httpServer.Shutdown(shutdownCtx)
		if err != nil {
			_ = httpServer.Close()
			err = runtimegen.ErrUncertain
		}
		finished <- err
	})
	var serveErr error
	if app.tlsConfig != nil {
		serveErr = httpServer.ServeTLS(listener, "", "")
	} else {
		serveErr = httpServer.Serve(listener)
	}
	var shutdownErr error
	if !stop() {
		shutdownErr = <-finished
		if shutdownErr != nil {
			app.markRuntimeUncertain()
		}
	}
	if errors.Is(serveErr, http.ErrServerClosed) && ctx.Err() != nil {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}
