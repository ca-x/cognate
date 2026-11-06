//go:build unix

package githubproxysettings

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSettingsRejectUnsafeFilesAndDirectories(test *testing.T) {
	for _, failure := range []string{"public data root", "public directory", "public file", "file symlink", "file hardlink", "file directory", "file fifo", "public lock", "lock symlink", "lock hardlink", "network symlink"} {
		test.Run(failure, func(test *testing.T) {
			directory := testDirectory(test)
			manager := openTestManager(test, directory)
			if _, err := manager.Update(test.Context(), 1, syntheticURL); err != nil {
				test.Fatal(err)
			}
			if err := manager.Close(); err != nil {
				test.Fatal(err)
			}
			network := filepath.Join(directory, "network")
			path := filepath.Join(network, settingsName)
			var err error
			switch failure {
			case "public data root":
				err = os.Chmod(directory, 0o755)
			case "public directory":
				err = os.Chmod(network, 0o755)
			case "public file":
				err = os.Chmod(path, 0o644)
			case "public lock":
				err = os.Chmod(filepath.Join(network, lockName), 0o644)
			case "file hardlink":
				err = os.Link(path, filepath.Join(directory, "linked"))
			case "lock hardlink":
				err = os.Link(filepath.Join(network, lockName), filepath.Join(directory, "linked"))
			case "network symlink":
				moved := filepath.Join(directory, "moved-network")
				if err := os.Rename(network, moved); err != nil {
					test.Fatal(err)
				}
				err = os.Symlink(moved, network)
			default:
				if failure == "lock symlink" {
					path = filepath.Join(network, lockName)
				}
				if err := os.Remove(path); err != nil {
					test.Fatal(err)
				}
				switch failure {
				case "file directory":
					err = os.Mkdir(path, 0o700)
				case "file fifo":
					err = syscall.Mkfifo(path, 0o600)
				default:
					outside := filepath.Join(directory, "outside")
					if err := os.WriteFile(outside, []byte("synthetic-proxy-password"), 0o600); err != nil {
						test.Fatal(err)
					}
					err = os.Symlink(outside, path)
				}
			}
			if err != nil {
				test.Fatal(err)
			}
			opened, err := Open(test.Context(), directory, testMaster())
			if opened != nil || !errors.Is(err, ErrUnavailable) {
				test.Fatalf("unsafe settings path was accepted: %v", err)
			}
			assertPrivate(test, err)
		})
	}
}

func TestSettingsFileOwnerAndLinkValidation(test *testing.T) {
	directory := testDirectory(test)
	path := filepath.Join(directory, "file")
	if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
		test.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !privateFileInfo(info) {
		test.Fatal("owned private file was rejected")
	}
	attributes := *info.Sys().(*syscall.Stat_t)
	attributes.Uid = uint32(os.Geteuid()) + 1
	if privateFileInfo(testFileInfo{FileInfo: info, attributes: &attributes}) {
		test.Fatal("foreign file owner was accepted")
	}
	attributes.Uid = uint32(os.Geteuid())
	attributes.Nlink = 2
	if privateFileInfo(testFileInfo{FileInfo: info, attributes: &attributes}) || privateFileInfo(nil) {
		test.Fatal("invalid link count or missing metadata was accepted")
	}
}

type testFileInfo struct {
	os.FileInfo
	attributes *syscall.Stat_t
}

func (info testFileInfo) Sys() any { return info.attributes }

func TestSettingsUncertainCloseRetainsOwnership(test *testing.T) {
	directory := testDirectory(test)
	manager := openTestManager(test, directory)
	manager.owner = failingOwner{owner: manager.owner}
	if err := manager.Close(); !errors.Is(err, ErrUncertain) {
		test.Fatal("uncertain close did not report failure")
	}
	if duplicate, err := Open(test.Context(), directory, testMaster()); duplicate != nil || !errors.Is(err, ErrOwned) {
		test.Fatal("ownership was released after an uncertain close")
	}
	owners.CompareAndDelete(manager.ownerPath, manager)
}

type failingOwner struct{ owner interface{ Close() error } }

func (owner failingOwner) Close() error {
	_ = owner.owner.Close()
	return syntheticFailure
}

func TestSettingsOwnerAcrossProcesses(test *testing.T) {
	const directoryVariable = "COGNATE_GITHUB_PROXY_TEST_OWNER_DIRECTORY"
	const modeVariable = "COGNATE_GITHUB_PROXY_TEST_OWNER_MODE"
	if directory := os.Getenv(directoryVariable); directory != "" {
		ctx, cancel := context.WithTimeout(test.Context(), 250*time.Millisecond)
		defer cancel()
		manager, err := Open(ctx, directory, testMaster())
		if os.Getenv(modeVariable) == "blocked" {
			if manager != nil {
				_ = manager.Close()
				test.Fatal("another process acquired the settings owner lock")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				test.Fatalf("owner wait did not respect its context: %v", err)
			}
			return
		}
		if err != nil {
			test.Fatal(err)
		}
		if err := manager.Close(); err != nil {
			test.Fatal(err)
		}
		return
	}
	directory := testDirectory(test)
	manager := openTestManager(test, directory)
	for _, mode := range []string{"blocked", "free"} {
		if mode == "free" {
			if err := manager.Close(); err != nil {
				test.Fatal(err)
			}
		}
		command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestSettingsOwnerAcrossProcesses$", "-test.count=1", "-test.timeout=10s")
		command.Env = []string{directoryVariable + "=" + directory, modeVariable + "=" + mode}
		if output, err := command.CombinedOutput(); err != nil {
			test.Fatalf("cross-process owner verification: %v\n%s", err, output)
		}
	}
}
