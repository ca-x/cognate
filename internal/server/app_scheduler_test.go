package server

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/scheduler"
)

func TestAppConfiguresSchedulerLifecycle(t *testing.T) {
	for _, interval := range []time.Duration{0, time.Minute} {
		t.Run(interval.String(), func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				t.Fatal(err)
			}
			keyFile := filepath.Join(directory, "master.key")
			if err := os.WriteFile(keyFile, key, 0o600); err != nil {
				t.Fatal(err)
			}
			app, err := Open(t.Context(), Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: keyFile, ListSyncInterval: interval})
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			if (app.scheduler == nil) != (interval == 0) {
				t.Fatal("configured scheduler enablement does not match the interval")
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			finished := make(chan error, 1)
			go func() { finished <- app.Serve(ctx, listener) }()
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Get("http://" + listener.Addr().String() + "/")
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if app.scheduler != nil {
				if err := app.scheduler.Start(ctx); !errors.Is(err, scheduler.ErrStarted) {
					t.Fatalf("Serve did not start the scheduler: %v", err)
				}
			}
			cancel()
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Serve did not cancel the scheduler")
			}
			if app.scheduler != nil {
				if err := app.scheduler.RunOnce(t.Context()); !errors.Is(err, scheduler.ErrClosed) {
					t.Fatalf("Serve returned with a running scheduler: %v", err)
				}
			}
		})
	}
}

func TestAppRejectsInvalidSchedulerInterval(t *testing.T) {
	for _, interval := range []time.Duration{-time.Second, 25 * time.Hour} {
		if app, err := Open(t.Context(), Config{ListSyncInterval: interval}); err == nil {
			_ = app.Close()
			t.Fatalf("invalid scheduler interval accepted: %v", interval)
		}
	}
}
