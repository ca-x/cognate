package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/cli"
	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/subscription"
)

type failedWatchWriter struct{ failure error }

func (writer failedWatchWriter) Write([]byte) (int, error) { return 0, writer.failure }

func (writer failedWatchWriter) WriteContext(context.Context, []byte) (int, error) {
	return 0, writer.failure
}

func TestPackWatchRejectsInvalidOptionsBeforeStorage(test *testing.T) {
	home := test.TempDir()
	test.Setenv("HOME", home)
	for _, arguments := range [][]string{
		{"pack", "watch", "--interval=-1s"},
		{"pack", "watch", "--interval=0s"},
		{"pack", "watch", "--interval=25h"},
		{"pack", "watch", "--attempt-timeout=0s"},
		{"pack", "watch", "--attempt-timeout=11m"},
	} {
		command := cli.New()
		command.SetArgs(arguments)
		command.SetOut(new(bytes.Buffer))
		if !errors.Is(command.ExecuteContext(test.Context()), subscription.ErrInvalidWatchOptions) {
			test.Fatal("invalid watch options were not rejected")
		}
	}
	if _, err := os.Lstat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("invalid watch touched local storage")
	}
}

func TestPackWatchAlreadyCanceledDoesNotOpenStorage(test *testing.T) {
	home := test.TempDir()
	test.Setenv("HOME", home)
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	command := cli.New()
	command.SetArgs([]string{"pack", "watch"})
	command.SetOut(new(bytes.Buffer))
	if err := command.ExecuteContext(ctx); err != nil {
		test.Fatal("foreground cancellation was not a clean stop")
	}
	if _, err := os.Lstat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("canceled watch touched storage")
	}
}

func TestPackWatchOutputFailureStopsAndReleasesInstallationLock(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("Linux installer")
	}
	home := test.TempDir()
	test.Setenv("HOME", home)
	if err := clientkey.Initialize(test.Context(), home); err != nil {
		test.Fatal(err)
	}
	writeFailure := errors.New("synthetic output unavailable")
	command := cli.New()
	command.SetArgs([]string{"pack", "watch"})
	command.SetOut(failedWatchWriter{failure: writeFailure})
	ctx, cancel := context.WithTimeout(test.Context(), 2*time.Second)
	defer cancel()
	if err := command.ExecuteContext(ctx); err == nil || err.Error() != "watch output failed" || strings.Contains(err.Error(), writeFailure.Error()) {
		test.Fatal("watch did not stop on output failure")
	}
	session, err := install.OpenSession(ctx, filepath.Join(home, ".local", "share", "skillhub"))
	if err != nil {
		test.Fatal("failed output retained installation lock")
	}
	if err := session.Close(); err != nil {
		test.Fatal(err)
	}
}
