//go:build unix

package maintenance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/filelock"
)

type directoryCloser func() error

func (close directoryCloser) Close() error {
	return close()
}

func TestDirectoryReleaseProcessHelper(test *testing.T) {
	directory := os.Getenv("COGNATE_RELEASE_TEST_DIRECTORY")
	if directory == "" {
		return
	}
	ctx, cancel := context.WithTimeout(test.Context(), 150*time.Millisecond)
	defer cancel()
	instance, err := Join(ctx, directory)
	if errors.Is(err, context.DeadlineExceeded) {
		fmt.Fprintln(os.Stdout, "BLOCKED")
		return
	}
	if err != nil {
		test.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		test.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "ACQUIRED")
}

func releaseProbe(test *testing.T, directory, expected string) {
	test.Helper()
	executable, err := os.Executable()
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestDirectoryReleaseProcessHelper$", "-test.timeout=4s")
	command.Env = append(os.Environ(), "COGNATE_RELEASE_TEST_DIRECTORY="+directory)
	output, err := command.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(output), expected+"\n") {
		test.Fatalf("real process expected %s: error=%v output=%s", expected, err, output)
	}
}

func TestDirectoryReleaseKeepsPhysicalLockUntilCleanup(test *testing.T) {
	for _, failCleanup := range []bool{false, true} {
		test.Run(fmt.Sprintf("cleanup_failure=%t", failCleanup), func(test *testing.T) {
			directory := test.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				test.Fatal(err)
			}
			root, err := os.OpenRoot(directory)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = root.Close() })
			lock, err := filelock.Acquire(test.Context(), filepath.Join(directory, ".instance.lock"))
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = lock.Close() })
			cleanupFailure := errors.New("synthetic directory cleanup uncertainty")
			closer := directoryCloser(func() error {
				if err := root.Close(); err != nil {
					test.Fatal(err)
				}
				releaseProbe(test, directory, "BLOCKED")
				if failCleanup {
					return cleanupFailure
				}
				return nil
			})
			err = closeDirectory(closer, lock)
			if failCleanup {
				if !errors.Is(err, cleanupFailure) {
					test.Fatalf("lost cleanup failure: %v", err)
				}
				releaseProbe(test, directory, "BLOCKED")
				if err := lock.Close(); err != nil {
					test.Fatal(err)
				}
			} else if err != nil {
				test.Fatal(err)
			}
			releaseProbe(test, directory, "ACQUIRED")
		})
	}
}

func TestDirectoryReleasePreservesLockCloseError(test *testing.T) {
	cleanupComplete := false
	closeFailure := errors.New("synthetic lock close uncertainty")
	root := directoryCloser(func() error {
		cleanupComplete = true
		return nil
	})
	lock := directoryCloser(func() error {
		if !cleanupComplete {
			test.Fatal("lock closed before directory cleanup")
		}
		return closeFailure
	})
	if err := closeDirectory(root, lock); !errors.Is(err, closeFailure) {
		test.Fatalf("lost lock close failure: %v", err)
	}
}
