package objectid_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/objectid"
)

func TestValidate(t *testing.T) {
	for _, identity := range []string{
		"linux-dev-ino-v1:0:1",
		"linux-dev-ino-v1:18446744073709551615:18446744073709551615",
		"darwin-dev-ino-v1:0:1",
		"darwin-dev-ino-v1:4294967295:18446744073709551615",
	} {
		if err := objectid.Validate(identity); err != nil {
			t.Errorf("rejected %q: %v", identity, err)
		}
	}
	for _, identity := range []string{
		"", "other:1:1", "windows-file-id-v1:1:1",
		"linux-dev-ino-v1:0:0", "linux-dev-ino-v1:01:1", "linux-dev-ino-v1:-1:1",
		"linux-dev-ino-v1:18446744073709551616:1", "linux-dev-ino-v1:1:1:1",
		"darwin-dev-ino-v1:4294967296:1", "darwin-dev-ino-v1:1:18446744073709551616",
		"darwin-dev-ino-v1:0:0", "darwin-dev-ino-v1:01:1", "darwin-dev-ino-v1:1:01",
		"darwin-dev-ino-v1:-1:1", "darwin-dev-ino-v1:1:-1", "darwin-dev-ino-v1:+1:1",
		"darwin-dev-ino-v1:1:+1", "darwin-dev-ino-v1:1:1:1", "darwin-dev-ino-v1:1",
		"darwin-dev-ino-v1::1", "darwin-dev-ino-v1:1:", "darwin-dev-ino-v1: 1:1",
		"darwin-dev-ino-v1:1:1\n", "darwin-dev-ino-v1:0x1:1", "darwin-dev-ino-v1:１:1",
		"darwin-dev-ino-v2:1:1", "Darwin-dev-ino-v1:1:1",
	} {
		if err := objectid.Validate(identity); err == nil {
			t.Errorf("accepted %q", identity)
		}
	}
}

func TestIdentityDirectoryDomain(test *testing.T) {
	parent := test.TempDir()
	path := filepath.Join(parent, "directory")
	if err := os.Mkdir(path, 0o700); err != nil {
		test.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		test.Fatal(err)
	}
	identity, err := objectid.FromFileInfo(info)
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		if err == nil || identity != "" {
			test.Fatalf("unsupported platform returned %q, %v", identity, err)
		}
		if _, err := objectid.FromLinkInfo(info); err == nil {
			test.Fatal("unsupported platform accepted symlink identity")
		}
		return
	}
	if err != nil || objectid.Validate(identity) != nil || !strings.HasPrefix(identity, runtime.GOOS+"-dev-ino-v1:") {
		test.Fatalf("native identity = %q, error = %v", identity, err)
	}
	if _, err := objectid.FromFileInfo(nil); err == nil {
		test.Fatal("accepted missing directory")
	}
	if _, err := objectid.FromLinkInfo(info); err == nil {
		test.Fatal("accepted directory as link")
	}
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(path, moved); err != nil {
		test.Fatal(err)
	}
	info, err = os.Stat(moved)
	if err != nil {
		test.Fatal(err)
	}
	afterRename, err := objectid.FromFileInfo(info)
	if err != nil || afterRename != identity {
		test.Fatalf("renamed identity = %q, error = %v", afterRename, err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		test.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil {
		test.Fatal(err)
	}
	replacement, err := objectid.FromFileInfo(info)
	if err != nil || replacement == identity {
		test.Fatalf("replacement identity = %q, error = %v", replacement, err)
	}
}
