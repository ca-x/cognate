package server_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ca-x/cognate/internal/server"
)

func TestAppInitializesMasterKeyOnlyForFreshState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix server file checks")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	config := server.Config{
		MasterKeyFile:       filepath.Join(base, "keys", "master.key"),
		DataDirectory:       filepath.Join(base, "data"),
		InitializeMasterKey: true,
	}
	for range 2 {
		app, err := server.Open(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.Close(); err != nil {
			t.Fatal(err)
		}
	}
	key, err := os.ReadFile(config.MasterKeyFile)
	defer clear(key)
	if err != nil || len(key) != 32 || bytes.Equal(key, make([]byte, 32)) {
		t.Fatal("master key was not generated")
	}
	if err := os.Remove(config.MasterKeyFile); err != nil {
		t.Fatal(err)
	}
	if app, err := server.Open(t.Context(), config); err == nil {
		_ = app.Close()
		t.Fatal("generated replacement key for existing database")
	}
	if _, err := os.Lstat(config.MasterKeyFile); !os.IsNotExist(err) {
		t.Fatal("lost master key was replaced")
	}
}
