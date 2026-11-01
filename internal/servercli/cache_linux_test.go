//go:build linux

package servercli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
