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
	"strings"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/githubauth"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/nativeconsumer"
	"github.com/ca-x/cognate/internal/server/oidclogin"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	"github.com/ca-x/cognate/internal/server/scheduler"
	"github.com/ca-x/cognate/internal/server/sourcerefresh"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/web"
)

type Config struct {
	DataDirectory         string
	MasterKeyFile         string
	TLSCertificate        string
	TLSKey                string
	AllowHTTP             bool
	PublicOrigin          string
	GitHubClientID        string
	OIDCIssuer            string
	OIDCClientID          string
	OIDCClientSecretFile  string
	ArchiveCacheBytes     int64
	ListSyncInterval      time.Duration
	SourceRefreshInterval *time.Duration
}

type App struct {
	store         *state.Store
	handler       http.Handler
	tlsConfig     *tls.Config
	allowHTTP     bool
	device        *githubauth.Service
	oidc          *oidclogin.Manager
	library       *library.Service
	packs         *packs.Service
	mcp           *mcpserver.Server
	public        *publiccatalog.Service
	scheduler     *scheduler.Scheduler
	sourceRefresh *sourcerefresh.Worker
	imports       *httpapi.ImportHandler
	native        *nativeconsumer.Service
}

func Open(ctx context.Context, config Config) (*App, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
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
	key, err := state.ReadMasterKey(config.MasterKeyFile)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	if err := state.PrepareDirectory(config.DataDirectory); err != nil {
		return nil, err
	}
	store, err := state.Open(ctx, filepath.Join(config.DataDirectory, "server.db"), key)
	if err != nil {
		return nil, err
	}
	service, err := auth.NewService(store, key)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	var device *githubauth.Service
	if config.GitHubClientID != "" {
		device, err = githubauth.NewService(store, config.GitHubClientID, nil)
		if err != nil {
			_ = store.Close()
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
		}
		clear(secret)
		if err != nil {
			if device != nil {
				device.Close()
			}
			_ = store.Close()
			return nil, err
		}
	}
	cache, err := library.OpenArchiveCacheWithOptions(ctx, filepath.Join(config.DataDirectory, "archives"), library.CacheOptions{MaxBytes: config.ArchiveCacheBytes})
	if err != nil {
		if login != nil {
			login.Close()
		}
		if device != nil {
			device.Close()
		}
		_ = store.Close()
		return nil, err
	}
	fetcher := repository.NewArchiveFetcher(nil)
	collection, err := library.NewService(store, cache, fetcher)
	if err != nil {
		_ = cache.Close()
		if login != nil {
			login.Close()
		}
		if device != nil {
			device.Close()
		}
		_ = store.Close()
		return nil, err
	}
	distribution, err := packs.NewService(store, collection, key)
	if err != nil {
		_ = collection.Close()
		if login != nil {
			login.Close()
		}
		if device != nil {
			device.Close()
		}
		_ = store.Close()
		return nil, err
	}
	var listScheduler *scheduler.Scheduler
	if config.ListSyncInterval != 0 {
		listScheduler, err = scheduler.New(store, cache, fetcher, nil, scheduler.Options{Interval: config.ListSyncInterval})
		if err != nil {
			distribution.Close()
			_ = collection.Close()
			if login != nil {
				login.Close()
			}
			if device != nil {
				device.Close()
			}
			_ = store.Close()
			return nil, err
		}
	}
	app := &App{store: store, tlsConfig: tlsConfig, allowHTTP: config.AllowHTTP, device: device, oidc: login, library: collection, packs: distribution, scheduler: listScheduler}
	if sourceInterval != 0 {
		app.sourceRefresh, err = sourcerefresh.New(store, collection, sourcerefresh.Options{Interval: sourceInterval, DataDirectory: config.DataDirectory})
		if err != nil {
			_ = app.Close()
			return nil, err
		}
	}
	app.imports, err = httpapi.NewImportHandler(service, collection)
	if err != nil {
		_ = app.Close()
		return nil, err
	}
	access, err := mcpaccess.NewService(store)
	if err != nil {
		_ = app.Close()
		return nil, err
	}
	app.mcp, err = mcpserver.New(mcpserver.Options{Access: access, Store: store, Library: collection, PublicOrigin: config.PublicOrigin})
	if err != nil {
		_ = app.Close()
		return nil, err
	}
	app.public, err = publiccatalog.NewService(store, collection)
	if err != nil {
		_ = app.Close()
		return nil, err
	}
	if tlsConfig != nil && strings.HasPrefix(config.PublicOrigin, "https://") && nativesessionauth.ValidServerOrigin(config.PublicOrigin) {
		app.native, err = nativeconsumer.New(store, service, key, config.PublicOrigin)
		if err != nil {
			_ = app.Close()
			return nil, err
		}
	}
	app.handler = httpapi.NewWithOptions(service, httpapi.Options{PublicOrigin: config.PublicOrigin, Store: store, WebUI: web.Handler(), GitHubDevice: device, OIDC: login, Library: collection, Packs: distribution, MCPAccess: access, MCP: app.mcp, PublicCatalog: app.public, Imports: app.imports, Configuration: configuration.New(store), NativeConfiguration: app.native})
	return app, nil
}

func (app *App) Close() error {
	if app.native != nil {
		app.native.Close()
	}
	if app.sourceRefresh != nil {
		_ = app.sourceRefresh.Close()
	}
	if app.scheduler != nil {
		_ = app.scheduler.Close()
	}
	if app.imports != nil {
		if err := app.imports.Close(); err != nil {
			return err
		}
	}
	if app.mcp != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := app.mcp.Close(ctx)
		cancel()
		if err != nil {
			return err
		}
	}
	if app.public != nil {
		if err := app.public.Close(); err != nil {
			return err
		}
	}
	var shutdown sync.WaitGroup
	var libraryError error
	if app.packs != nil {
		shutdown.Go(app.packs.Close)
	}
	if app.library != nil {
		shutdown.Go(func() { libraryError = app.library.Close() })
	}
	if app.device != nil {
		shutdown.Go(app.device.Close)
	}
	if app.oidc != nil {
		shutdown.Go(app.oidc.Close)
	}
	shutdown.Wait()
	return errors.Join(libraryError, app.store.Close())
}

func (app *App) Serve(ctx context.Context, listener net.Listener) error {
	defer listener.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if app.tlsConfig == nil && !app.allowHTTP {
		address, ok := listener.Addr().(*net.TCPAddr)
		if !ok || !address.IP.IsLoopback() {
			return fmt.Errorf("non-loopback HTTP requires TLS or explicit --allow-http for a trusted private network")
		}
	}
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
		defer app.scheduler.Close()
	}
	if app.sourceRefresh != nil {
		if err := app.sourceRefresh.Start(ctx); err != nil {
			return err
		}
		defer app.sourceRefresh.Close()
	}
	httpServer := &http.Server{
		Handler: app.handler, TLSConfig: app.tlsConfig,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx },
		ErrorLog:    log.New(io.Discard, "", 0),
	}
	defer httpServer.Close()
	finished := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := httpServer.Shutdown(shutdownCtx)
		if err != nil {
			_ = httpServer.Close()
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
	}
	if errors.Is(serveErr, http.ErrServerClosed) && ctx.Err() != nil {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}
