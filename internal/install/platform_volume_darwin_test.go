//go:build darwin

package install

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestDarwinAPFSExecutionPrerequisites(test *testing.T) {
	if runtime.GOARCH != "arm64" {
		test.Fatal("DARWIN_INFRA requires actual arm64 runner")
	}
	sensitive, insensitive := os.Getenv("COGNATE_APFS_SENSITIVE"), os.Getenv("COGNATE_APFS_INSENSITIVE")
	if sensitive == "" || insensitive == "" {
		test.Fatal("DARWIN_INFRA requires two mounted APFS fixtures")
	}
	device := func(path string) int32 {
		test.Helper()
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			test.Fatal("DARWIN_INFRA mounted volume missing")
		}
		attributes, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			test.Fatal("DARWIN_INFRA native stat unavailable")
		}
		var filesystem syscall.Statfs_t
		if err := syscall.Statfs(path, &filesystem); err != nil {
			test.Fatal("DARWIN_INFRA filesystem identity unavailable")
		}
		kind := make([]byte, 0, 16)
		for _, value := range filesystem.Fstypename {
			if value == 0 {
				break
			}
			kind = append(kind, byte(value))
		}
		if string(kind) != "apfs" {
			test.Fatal("DARWIN_INFRA fixture is not APFS")
		}
		return attributes.Dev
	}
	if device(sensitive) == device(insensitive) {
		test.Fatal("DARWIN_INFRA fixture devices are identical")
	}
	for _, volume := range []struct {
		path      string
		sensitive bool
	}{{sensitive, true}, {insensitive, false}} {
		root, err := os.MkdirTemp(volume.path, "fixture-")
		if err != nil {
			test.Fatal("DARWIN_INFRA volume fixture creation")
		}
		test.Cleanup(func() {
			if err := os.RemoveAll(root); err != nil {
				test.Error("DARWIN_INFRA volume fixture cleanup")
			}
		})
		if err := os.WriteFile(filepath.Join(root, "Case"), []byte("original"), 0600); err != nil {
			test.Fatal("DARWIN_INFRA case fixture")
		}
		file, err := os.OpenFile(filepath.Join(root, "case"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if volume.sensitive {
			if err != nil {
				test.Fatal("DARWIN_INFRA sensitive volume folds case")
			}
			if file.Close() != nil {
				test.Fatal("DARWIN_INFRA case close")
			}
		} else if !errors.Is(err, os.ErrExist) {
			if file != nil {
				_ = file.Close()
			}
			test.Fatal("DARWIN_INFRA insensitive volume admits case alias")
		}
		if err := os.WriteFile(filepath.Join(root, "\u00e9"), []byte("original"), 0600); err != nil {
			test.Fatal("DARWIN_INFRA Unicode fixture")
		}
		file, err = os.OpenFile(filepath.Join(root, "e\u0301"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if !errors.Is(err, os.ErrExist) {
			if file != nil {
				_ = file.Close()
			}
			test.Fatal("DARWIN_INFRA Unicode equivalent unexpectedly distinct")
		}
		if err := os.WriteFile(filepath.Join(root, "from"), []byte("new"), 0600); err != nil {
			test.Fatal("DARWIN_INFRA rename source")
		}
		directory, err := os.Open(root)
		if err != nil {
			test.Fatal("DARWIN_INFRA directory handle")
		}
		renameErr := renameNoReplace(directory, root, "from", "Case")
		closeErr := directory.Close()
		retained, readErr := os.ReadFile(filepath.Join(root, "Case"))
		if renameErr == nil || closeErr != nil || readErr != nil || string(retained) != "original" {
			test.Fatal("DARWIN_INFRA no-replace primitive failed")
		}
	}
	source := filepath.Join(sensitive, "cross-device-source")
	target := filepath.Join(insensitive, "cross-device-target")
	test.Cleanup(func() {
		for _, path := range []string{source, target} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				test.Error("DARWIN_INFRA cross-device fixture cleanup")
			}
		}
	})
	if err := os.WriteFile(source, []byte("retained"), 0600); err != nil {
		test.Fatal("DARWIN_INFRA cross-device source")
	}
	if err := os.Rename(source, target); !errors.Is(err, syscall.EXDEV) {
		test.Fatal("DARWIN_INFRA cross-device rename did not reject")
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "retained" {
		test.Fatal("DARWIN_INFRA cross-device source changed")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("DARWIN_INFRA cross-device destination created")
	}
	test.Log("DARWIN_CAPABILITY apfs=true arm64=true case_sensitive=true case_insensitive=true unicode_alias=true distinct_devices=true no_replace=true cross_device_refused=true")
}
