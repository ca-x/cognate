//go:build linux

package servercli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/filelock"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"golang.org/x/sys/unix"
)

func offlineCacheFixture(test *testing.T) (string, string, string, []byte) {
	test.Helper()
	root := test.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		test.Fatal(err)
	}
	directory := filepath.Join(root, "archives")
	if err := os.Mkdir(directory, 0700); err != nil {
		test.Fatal(err)
	}
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	content := []byte("---\nname: example\ndescription: test skill\n---\nHello\n")
	if err := archive.WriteHeader(&tar.Header{Name: "example/SKILL.md", Mode: 0644, Size: int64(len(content))}); err != nil {
		test.Fatal(err)
	}
	if _, err := archive.Write(content); err != nil {
		test.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		test.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		test.Fatal(err)
	}
	data := output.Bytes()
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	path := filepath.Join(directory, hash+".tar.gz")
	if err := os.WriteFile(path, data, 0600); err != nil {
		test.Fatal(err)
	}
	return root, hash, path, data
}

func runCacheVerification(ctx context.Context, root, hash string) (string, error) {
	command := New()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"cache", "verify", "--data-dir", root, "--sha256", hash})
	err := command.ExecuteContext(ctx)
	return output.String(), err
}

func TestOfflineCacheVerify(test *testing.T) {
	root, hash, path, original := offlineCacheFixture(test)
	output, err := runCacheVerification(test.Context(), root, hash)
	if err != nil || output != "archive verified\n" {
		test.Fatalf("verification failed: %q %v", output, err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, original) {
		test.Fatal("verification changed archive")
	}
	if _, err := os.Stat(filepath.Join(root, "archives", ".cache-budget")); !os.IsNotExist(err) {
		test.Fatal("verification initialized cache policy")
	}
}

func TestOfflineCacheVerifyRejectsUnsafeInput(test *testing.T) {
	for _, fault := range []string{"corrupt", "not archive", "missing", "public file", "hardlink", "symlink", "fifo", "oversized", "public directory", "directory symlink", "generation", "intent", "bad hash", "relative root"} {
		test.Run(fault, func(test *testing.T) {
			root, hash, path, data := offlineCacheFixture(test)
			var err error
			switch fault {
			case "corrupt":
				err = os.WriteFile(path, bytes.Repeat([]byte("x"), len(data)), 0600)
			case "not archive":
				data = []byte("not a compressed tar archive")
				hash = fmt.Sprintf("%x", sha256.Sum256(data))
				err = os.WriteFile(filepath.Join(root, "archives", hash+".tar.gz"), data, 0600)
			case "missing":
				err = os.Remove(path)
			case "fifo":
				if err = os.Remove(path); err == nil {
					err = unix.Mkfifo(path, 0600)
				}
			case "oversized":
				err = os.Truncate(path, (32<<20)+1)
			case "public file":
				err = os.Chmod(path, 0644)
			case "hardlink":
				err = os.Link(path, filepath.Join(root, "alias"))
			case "symlink":
				if err = os.Rename(path, filepath.Join(root, "original")); err == nil {
					err = os.Symlink("../original", path)
				}
			case "public directory":
				err = os.Chmod(filepath.Dir(path), 0755)
			case "directory symlink":
				if err = os.Rename(filepath.Dir(path), filepath.Join(root, "other")); err == nil {
					err = os.Symlink("other", filepath.Join(root, "archives"))
				}
			case "generation":
				err = os.Mkdir(filepath.Join(root, "generations"), 0700)
			case "intent":
				err = os.WriteFile(filepath.Join(root, "switch.intent"), []byte("pending"), 0600)
			case "bad hash":
				hash = strings.Repeat("A", 64)
			case "relative root":
				root = "relative"
			}
			if err != nil {
				test.Fatal(err)
			}
			output, err := runCacheVerification(test.Context(), root, hash)
			if err == nil || strings.Contains(output, "archive verified") {
				test.Fatalf("unsafe input accepted: %q %v", output, err)
			}
		})
	}
}

func TestOfflineCacheVerifyRequiresExplicitParameters(test *testing.T) {
	root, hash, _, _ := offlineCacheFixture(test)
	for _, arguments := range [][]string{
		{"cache", "verify"},
		{"cache", "verify", "--data-dir", root},
		{"cache", "verify", "--sha256", hash},
	} {
		command := New()
		command.SetArgs(arguments)
		if err := command.ExecuteContext(test.Context()); err == nil {
			test.Fatal("implicit cache verification allowed", arguments)
		}
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if output, err := runCacheVerification(ctx, root, hash); err == nil || output != "" {
		test.Fatal("cancelled verification returned success", err)
	}
}

func TestOfflineCacheVerifyWaitsForRuntime(test *testing.T) {
	root, hash, _, _ := offlineCacheFixture(test)
	owner, err := runtimegen.OpenRuntime(test.Context(), root)
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 80*time.Millisecond)
	defer cancel()
	output, err := runCacheVerification(ctx, root, hash)
	if err == nil || strings.Contains(output, "archive verified") || ctx.Err() == nil {
		test.Fatal("verification bypassed active runtime", err)
	}
	if err := owner.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := runCacheVerification(test.Context(), root, hash); err != nil {
		test.Fatal("verification cannot run after runtime stops", err)
	}
}

func runCacheRepair(ctx context.Context, root, hash, input, repairID string) (string, error) {
	command := New()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"cache", "repair", "--data-dir", root, "--sha256", hash, "--archive-file", input, "--repair-id", repairID})
	err := command.ExecuteContext(ctx)
	return output.String(), err
}

func repairCacheFixture(test *testing.T) (string, string, string, string, []byte) {
	test.Helper()
	root, hash, target, data := offlineCacheFixture(test)
	cache, err := library.OpenArchiveCache(filepath.Dir(target))
	if err != nil {
		test.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		test.Fatal(err)
	}
	input := filepath.Join(root, "replacement.tar.gz")
	if err := os.WriteFile(input, data, 0600); err != nil {
		test.Fatal(err)
	}
	return root, hash, target, input, data
}

func TestOfflineCacheRepair(test *testing.T) {
	root, hash, target, input, data := repairCacheFixture(test)
	original := bytes.Repeat([]byte("x"), len(data))
	if err := os.WriteFile(target, original, 0600); err != nil {
		test.Fatal(err)
	}
	before, err := os.Stat(target)
	if err != nil {
		test.Fatal(err)
	}
	repairID := strings.Repeat("a", 32)
	output, err := runCacheRepair(test.Context(), root, hash, input, repairID)
	if err != nil || !strings.Contains(output, "archive repaired") {
		test.Fatalf("repair unavailable: %q %v", output, err)
	}
	retained := filepath.Join(root, "archives", ".repair-"+hash+"-"+repairID+".tar.gz")
	old, err := os.ReadFile(retained)
	if err != nil || !bytes.Equal(old, original) {
		test.Fatal("original damaged bytes not retained", err)
	}
	identity, err := os.Stat(retained)
	if err != nil || !os.SameFile(before, identity) {
		test.Fatal("original inode not retained", err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(actual, data) {
		test.Fatal("target archive not repaired", err)
	}
	if _, err := runCacheVerification(test.Context(), root, hash); err != nil {
		test.Fatal("repaired object fails independent verify", err)
	}
	output, err = runCacheRepair(test.Context(), root, hash, input, repairID)
	if err != nil || !strings.Contains(output, "archive already verified") {
		test.Fatal("explicit repeat changed a healthy target", err)
	}
}

func TestOfflineCacheRepairWaitsForBothLocks(test *testing.T) {
	for _, held := range []string{"runtime", "cache"} {
		test.Run(held, func(test *testing.T) {
			root, hash, target, input, data := repairCacheFixture(test)
			if err := os.WriteFile(target, bytes.Repeat([]byte("x"), len(data)), 0600); err != nil {
				test.Fatal(err)
			}
			var release func() error
			if held == "runtime" {
				owner, err := runtimegen.OpenRuntime(test.Context(), root)
				if err != nil {
					test.Fatal(err)
				}
				release = owner.Close
			} else {
				lock, err := filelock.Acquire(test.Context(), filepath.Join(root, "archives", ".cache.lock"))
				if err != nil {
					test.Fatal(err)
				}
				release = lock.Close
			}
			ctx, cancel := context.WithTimeout(test.Context(), 100*time.Millisecond)
			defer cancel()
			output, err := runCacheRepair(ctx, root, hash, input, strings.Repeat("a", 32))
			if err == nil || ctx.Err() == nil || strings.Contains(output, "archive repaired") {
				test.Fatal("repair bypassed live lock", held, output, err)
			}
			if err := release(); err != nil {
				test.Fatal(err)
			}
			if output, err := runCacheRepair(test.Context(), root, hash, input, strings.Repeat("a", 32)); err != nil || !strings.Contains(output, "archive repaired") {
				test.Fatal("repair did not resume after lock release", output, err)
			}
		})
	}
}

func TestOfflineCacheRepairRejectsInput(test *testing.T) {
	for _, fault := range []string{"missing", "relative", "public", "symlink", "hardlink", "fifo", "oversized", "wrong hash", "invalid archive"} {
		test.Run(fault, func(test *testing.T) {
			root, hash, target, input, data := repairCacheFixture(test)
			var err error
			switch fault {
			case "missing":
				err = os.Remove(input)
			case "relative":
				input = "relative.tar.gz"
			case "public":
				err = os.Chmod(input, 0644)
			case "symlink":
				err = os.Symlink(input, input+".link")
				input += ".link"
			case "hardlink":
				err = os.Link(input, input+".link")
			case "fifo":
				if err = os.Remove(input); err == nil {
					err = unix.Mkfifo(input, 0600)
				}
			case "oversized":
				err = os.Truncate(input, (32<<20)+1)
			case "wrong hash":
				err = os.WriteFile(input, []byte("wrong content"), 0600)
			case "invalid archive":
				invalid := []byte("matching hash, invalid archive")
				hash = fmt.Sprintf("%x", sha256.Sum256(invalid))
				err = os.WriteFile(input, invalid, 0600)
			}
			if err != nil {
				test.Fatal(err)
			}
			output, err := runCacheRepair(test.Context(), root, hash, input, strings.Repeat("a", 32))
			if err == nil || strings.Contains(output, "archive repaired") || strings.Contains(output, "archive already verified") {
				test.Fatal("unsafe input accepted", fault, output, err)
			}
			actual, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(actual, data) {
				test.Fatal("bad input modified target", err)
			}
		})
	}
}

func TestOfflineCacheRepairRequiresExplicitParameters(test *testing.T) {
	root, hash, _, input, _ := repairCacheFixture(test)
	for _, arguments := range [][]string{
		{"cache", "repair"},
		{"cache", "repair", "--data-dir", root, "--sha256", hash},
		{"cache", "repair", "--archive-file", input, "--sha256", hash},
		{"cache", "repair", "--data-dir", root, "--archive-file", input},
		{"cache", "repair", "--data-dir", root, "--sha256", hash, "--archive-file", input, "--repair-id", strings.Repeat("A", 32)},
	} {
		command := New()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetArgs(arguments)
		if err := command.ExecuteContext(test.Context()); err == nil || strings.Contains(output.String(), "archive already verified") {
			test.Fatal("implicit maintenance accepted", arguments, err)
		}
	}
	command := New()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"cache", "repair", "--data-dir", root, "--sha256", hash, "--archive-file", input})
	if err := command.ExecuteContext(test.Context()); err != nil {
		test.Fatal(err)
	}
	line, _, ok := strings.Cut(output.String(), "\n")
	identifier := strings.TrimPrefix(line, "archive repair started; repair-id=")
	if !ok || len(identifier) != 32 || !strings.Contains(output.String(), "archive already verified") {
		test.Fatal("missing random start ID or healthy receipt", output.String())
	}
}

type repairOutputFailure struct {
	failAt, writes int
}

func (output *repairOutputFailure) Write(data []byte) (int, error) {
	output.writes++
	if output.writes == output.failAt {
		return 0, syscall.EPIPE
	}
	return len(data), nil
}

func TestOfflineCacheRepairOutputFailure(test *testing.T) {
	for _, failAt := range []int{1, 2} {
		test.Run(fmt.Sprint(failAt), func(test *testing.T) {
			root, hash, target, input, data := repairCacheFixture(test)
			original := bytes.Repeat([]byte("x"), len(data))
			if err := os.WriteFile(target, original, 0600); err != nil {
				test.Fatal(err)
			}
			command := New()
			command.SetOut(&repairOutputFailure{failAt: failAt})
			command.SetArgs([]string{"cache", "repair", "--data-dir", root, "--sha256", hash, "--archive-file", input})
			err := command.ExecuteContext(test.Context())
			if !errors.Is(err, syscall.EPIPE) || errors.Is(err, library.ErrArchiveRepairUncertain) != (failAt == 2) {
				test.Fatal("incorrect reporting uncertainty", failAt, err)
			}
			actual, readError := os.ReadFile(target)
			want := original
			if failAt == 2 {
				want = data
			}
			if readError != nil || !bytes.Equal(actual, want) {
				test.Fatal("incorrect publication at output failure", readError)
			}
		})
	}
}

func TestOfflineCacheRepairRefusesLostLock(test *testing.T) {
	root, hash, target, input, data := repairCacheFixture(test)
	original := bytes.Repeat([]byte("x"), len(data))
	if err := os.WriteFile(target, original, 0600); err != nil {
		test.Fatal(err)
	}
	name := filepath.Join(root, "archives", ".cache.lock")
	lock, err := filelock.Acquire(test.Context(), name)
	if err != nil {
		test.Fatal(err)
	}
	defer lock.Close()
	if err := os.Remove(name); err != nil {
		test.Fatal(err)
	}
	if output, err := runCacheRepair(test.Context(), root, hash, input, strings.Repeat("a", 32)); err == nil || strings.Contains(output, "archive repaired") {
		test.Fatal("lost original lock replaced by a new coordination inode", output, err)
	}
	if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("refusal initialized a replacement lock", err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(actual, original) {
		test.Fatal("lost lock repair modified target", err)
	}
}
