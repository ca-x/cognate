package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/ca-x/cognate/internal/objectid"
	"golang.org/x/sys/unix"
)

type darwinPrimitiveInfo struct {
	os.FileInfo
	attributes any
}

func (info darwinPrimitiveInfo) Sys() any {
	return info.attributes
}

func TestDarwinIdentityPrimitives(test *testing.T) {
	parent := test.TempDir()
	info, err := os.Stat(parent)
	if err != nil {
		test.Fatal(err)
	}
	identity, err := objectid.FromFileInfo(info)
	if err != nil || !strings.HasPrefix(identity, "darwin-dev-ino-v1:") || objectid.Validate(identity) != nil {
		test.Fatalf("Darwin directory identity = %q, %v", identity, err)
	}
	for _, fixture := range []struct {
		device int32
		inode  uint64
		want   string
	}{
		{0, 1, "darwin-dev-ino-v1:0:1"},
		{-1, ^uint64(0), "darwin-dev-ino-v1:4294967295:18446744073709551615"},
	} {
		metadata := darwinPrimitiveInfo{FileInfo: info, attributes: &syscall.Stat_t{Dev: fixture.device, Ino: fixture.inode}}
		got, err := objectid.FromFileInfo(metadata)
		if err != nil || got != fixture.want || objectid.Validate(got) != nil {
			test.Fatalf("Darwin identity = %q, %v; want %q", got, err, fixture.want)
		}
	}
	for _, metadata := range []os.FileInfo{
		nil,
		darwinPrimitiveInfo{FileInfo: info},
		darwinPrimitiveInfo{FileInfo: info, attributes: &syscall.Stat_t{Ino: 0}},
	} {
		if _, err := objectid.FromFileInfo(metadata); err == nil {
			test.Fatal("accepted unavailable directory identity")
		}
	}
	regular := filepath.Join(parent, "file")
	if err := os.WriteFile(regular, []byte("fixture"), 0o600); err != nil {
		test.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(parent, "link")); err != nil {
		test.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(parent, "fifo"), 0o600); err != nil {
		test.Fatal(err)
	}
	for _, name := range []string{"file", "link", "fifo"} {
		metadata, err := os.Lstat(filepath.Join(parent, name))
		if err != nil {
			test.Fatal(err)
		}
		if _, err := objectid.FromFileInfo(metadata); err == nil {
			test.Fatalf("accepted %s as directory", name)
		}
		linkID, err := objectid.FromLinkInfo(metadata)
		if name == "link" {
			if err != nil || !strings.HasPrefix(linkID, "darwin-dev-ino-v1:") || objectid.Validate(linkID) != nil {
				test.Fatalf("Darwin link identity = %q, %v", linkID, err)
			}
			if _, err := objectid.FromLinkInfo(darwinPrimitiveInfo{FileInfo: metadata}); err == nil {
				test.Fatal("accepted unavailable link identity")
			}
		} else if err == nil {
			test.Fatalf("accepted %s as link", name)
		}
	}
	if _, err := objectid.FromLinkInfo(info); err == nil {
		test.Fatal("accepted directory as link")
	}
	if _, err := objectid.FromLinkInfo(nil); err == nil {
		test.Fatal("accepted missing link")
	}
}

func TestDarwinTrustPrimitives(test *testing.T) {
	test.Run("private-owner-and-mode", func(test *testing.T) {
		parent := filepath.Join(test.TempDir(), "private")
		if err := os.Mkdir(parent, 0o700); err != nil {
			test.Fatal(err)
		}
		info, err := os.Stat(parent)
		if err != nil {
			test.Fatal(err)
		}
		attributes, ok := info.Sys().(*syscall.Stat_t)
		if !ok || attributes.Uid != uint32(os.Geteuid()) {
			test.Fatal("fixture owner differs from effective UID")
		}
		if err := validatePrivateDirectory(info); err != nil {
			test.Fatal(err)
		}
		wrongOwner := *attributes
		wrongOwner.Uid++
		if err := validatePrivateDirectory(darwinPrimitiveInfo{FileInfo: info, attributes: &wrongOwner}); err == nil {
			test.Fatal("accepted private directory owned by another UID")
		}
		if err := validatePrivateDirectory(darwinPrimitiveInfo{FileInfo: info}); err == nil {
			test.Fatal("accepted missing owner metadata")
		}
		for _, mode := range []os.FileMode{0o700, 0o500, 0o000, 0o750, 0o705, 0o770, 0o777} {
			if err := os.Chmod(parent, mode); err != nil {
				test.Fatal(err)
			}
			info, err := os.Stat(parent)
			if err != nil {
				test.Fatal(err)
			}
			if err := validatePrivateDirectory(info); (err == nil) != (mode&0o077 == 0) {
				test.Fatalf("private mode %04o: %v", mode, err)
			}
		}
		if err := os.Chmod(parent, 0o700); err != nil {
			test.Fatal(err)
		}
	})
	test.Run("tree-modes", func(test *testing.T) {
		for _, fixture := range []struct {
			directory os.FileMode
			file      os.FileMode
			trusted   bool
		}{
			{0o700, 0o600, true}, {0o755, 0o644, true}, {0o750, 0o640, true},
			{0o770, 0o600, false}, {0o777, 0o600, false},
			{0o700, 0o660, false}, {0o700, 0o606, false},
		} {
			test.Run(fmt.Sprintf("%04o-%04o", fixture.directory, fixture.file), func(test *testing.T) {
				parent := test.TempDir()
				path := filepath.Join(parent, "SKILL.md")
				if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
					test.Fatal(err)
				}
				if err := os.Chmod(path, fixture.file); err != nil {
					test.Fatal(err)
				}
				if err := os.Chmod(parent, fixture.directory); err != nil {
					test.Fatal(err)
				}
				if err := validateTrustedTree(test.Context(), parent, true); (err == nil) != fixture.trusted {
					test.Fatalf("tree trust: %v", err)
				}
			})
		}
	})
	test.Run("reject-links-and-special-files", func(test *testing.T) {
		for _, kind := range []string{"symlink", "fifo"} {
			test.Run(kind, func(test *testing.T) {
				parent := test.TempDir()
				path := filepath.Join(parent, "entry")
				var err error
				if kind == "symlink" {
					err = os.Symlink("missing", path)
				} else {
					err = unix.Mkfifo(path, 0o600)
				}
				if err != nil {
					test.Fatal(err)
				}
				info, err := os.Lstat(path)
				if err != nil {
					test.Fatal(err)
				}
				if err := validatePrivateDirectory(info); err == nil {
					test.Fatal("accepted non-directory as private directory")
				}
				if err := validateTrustedTree(test.Context(), parent, false); err == nil {
					test.Fatal("accepted untrusted tree entry")
				}
			})
		}
	})
	test.Run("git-scope-and-cancellation", func(test *testing.T) {
		parent := test.TempDir()
		git := filepath.Join(parent, ".git")
		if err := os.Mkdir(git, 0o700); err != nil {
			test.Fatal(err)
		}
		if err := os.Chmod(git, 0o777); err != nil {
			test.Fatal(err)
		}
		if err := validateTrustedTree(test.Context(), parent, false); err != nil {
			test.Fatal(err)
		}
		if err := validateTrustedTree(test.Context(), parent, true); err == nil {
			test.Fatal("accepted writable .git in complete tree")
		}
		ctx, cancel := context.WithCancel(test.Context())
		cancel()
		if err := validateTrustedTree(ctx, parent, false); !errors.Is(err, context.Canceled) {
			test.Fatalf("tree cancellation = %v", err)
		}
		root, err := os.OpenRoot(parent)
		if err != nil {
			test.Fatal(err)
		}
		defer root.Close()
		if err := validateTrustedRoot(ctx, root, false); !errors.Is(err, context.Canceled) {
			test.Fatalf("root cancellation = %v", err)
		}
	})
}

func darwinProjectionFixture(test *testing.T, canonical string) (*publishedProjection, string) {
	test.Helper()
	parent := filepath.Join(test.TempDir(), "agent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(parent, "link")
	if err := os.Symlink(canonical, path); err != nil {
		test.Fatal(err)
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = root.Close() })
	parentInfo, err := root.Stat(".")
	if err != nil {
		test.Fatal(err)
	}
	link, err := openProjectionLink(root, "link")
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = link.Close() })
	linkInfo, err := link.Stat()
	if err != nil {
		test.Fatal(err)
	}
	return &publishedProjection{root: root, link: link, parentInfo: parentInfo, linkInfo: linkInfo}, path
}

func TestDarwinProjectionLinkPrimitives(test *testing.T) {
	test.Run("nofollow-handles", func(test *testing.T) {
		outside := test.TempDir()
		for _, target := range []string{outside, filepath.Join(outside, "missing"), "missing", "link"} {
			test.Run(target, func(test *testing.T) {
				projection, path := darwinProjectionFixture(test, target)
				if err := projection.verify(path, target); err != nil {
					test.Fatal(err)
				}
				var attributes unix.Stat_t
				if err := unix.Fstat(int(projection.link.Fd()), &attributes); err != nil {
					test.Fatal(err)
				}
				if attributes.Mode&unix.S_IFMT != unix.S_IFLNK {
					test.Fatal("handle does not refer to the symlink itself")
				}
				current, err := os.Lstat(path)
				if err != nil || !os.SameFile(projection.linkInfo, current) {
					test.Fatalf("fstat/Lstat identity mismatch: %v", err)
				}
				identity, err := objectid.FromLinkInfo(projection.linkInfo)
				if err != nil || objectid.Validate(identity) != nil || !strings.HasPrefix(identity, "darwin-dev-ino-v1:") {
					test.Fatalf("handle identity = %q, %v", identity, err)
				}
				if err := projection.verify(path, target+"-different"); err == nil {
					test.Fatal("accepted different link text")
				}
			})
		}
	})
	test.Run("same-text-replacement", func(test *testing.T) {
		canonical := test.TempDir()
		projection, path := darwinProjectionFixture(test, canonical)
		if err := os.Rename(path, path+"-held"); err != nil {
			test.Fatal(err)
		}
		if err := os.Symlink(canonical, path); err != nil {
			test.Fatal(err)
		}
		if err := projection.verify(path, canonical); err == nil {
			test.Fatal("accepted replacement with identical link text")
		}
		held, err := projection.link.Stat()
		if err != nil || !os.SameFile(projection.linkInfo, held) {
			test.Fatalf("held link identity changed: %v", err)
		}
	})
	test.Run("parent-replacement", func(test *testing.T) {
		canonical := test.TempDir()
		projection, path := darwinProjectionFixture(test, canonical)
		parent := filepath.Dir(path)
		if err := os.Rename(parent, parent+"-held"); err != nil {
			test.Fatal(err)
		}
		if err := os.Mkdir(parent, 0o700); err != nil {
			test.Fatal(err)
		}
		if err := os.Symlink(canonical, path); err != nil {
			test.Fatal(err)
		}
		if err := projection.verify(path, canonical); err == nil {
			test.Fatal("accepted replacement parent")
		}
		original, err := projection.root.Lstat("link")
		if err != nil || !os.SameFile(projection.linkInfo, original) {
			test.Fatalf("root escaped original parent: %v", err)
		}
	})
	test.Run("reject-other-objects-and-paths", func(test *testing.T) {
		parent := test.TempDir()
		if err := os.WriteFile(filepath.Join(parent, "file"), []byte("fixture"), 0o600); err != nil {
			test.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(parent, "directory"), 0o700); err != nil {
			test.Fatal(err)
		}
		if err := unix.Mkfifo(filepath.Join(parent, "fifo"), 0o600); err != nil {
			test.Fatal(err)
		}
		if err := os.Symlink("missing", filepath.Join(parent, "link")); err != nil {
			test.Fatal(err)
		}
		if err := os.Symlink(parent, filepath.Join(parent, "alias")); err != nil {
			test.Fatal(err)
		}
		root, err := os.OpenRoot(parent)
		if err != nil {
			test.Fatal(err)
		}
		defer root.Close()
		for _, name := range []string{"file", "directory", "fifo", "absent", "", ".", "..", "../link", "alias/link", "./link", "link/", "/link", `alias\link`} {
			link, err := openProjectionLink(root, name)
			if link != nil {
				_ = link.Close()
				test.Fatalf("returned a handle for %q", name)
			}
			if err == nil {
				test.Fatalf("accepted %q", name)
			}
		}
	})
}

func TestDarwinRenameNoReplacePrimitives(test *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		test.Run("existing-"+kind, func(test *testing.T) {
			parent := test.TempDir()
			source := filepath.Join(parent, "stage")
			target := filepath.Join(parent, "target")
			if err := os.Mkdir(source, 0o700); err != nil {
				test.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("stage fixture"), 0o600); err != nil {
				test.Fatal(err)
			}
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(target, 0o700)
			case "file":
				err = os.WriteFile(target, []byte("existing fixture"), 0o600)
			case "symlink":
				err = os.Symlink("missing", target)
			}
			if err != nil {
				test.Fatal(err)
			}
			before, err := os.Lstat(target)
			if err != nil {
				test.Fatal(err)
			}
			stage, err := os.Stat(source)
			if err != nil {
				test.Fatal(err)
			}
			if err := publishDirectoryChecked(parent, "stage", "target", stage); err == nil {
				test.Fatal("publication overwrote an existing destination")
			}
			after, err := os.Lstat(target)
			if err != nil || !os.SameFile(before, after) {
				test.Fatalf("existing destination changed: %v", err)
			}
			retained, err := os.Stat(source)
			if err != nil || !os.SameFile(stage, retained) {
				test.Fatalf("stage disappeared or changed: %v", err)
			}
			content, err := os.ReadFile(filepath.Join(source, "SKILL.md"))
			if err != nil || string(content) != "stage fixture" {
				test.Fatalf("stage content changed: %q, %v", content, err)
			}
		})
	}
	test.Run("publish-retains-identity", func(test *testing.T) {
		parent := test.TempDir()
		source := filepath.Join(parent, "stage")
		if err := os.Mkdir(source, 0o700); err != nil {
			test.Fatal(err)
		}
		stage, err := os.Stat(source)
		if err != nil {
			test.Fatal(err)
		}
		identity, err := objectid.FromFileInfo(stage)
		if err != nil {
			test.Fatal(err)
		}
		if err := publishDirectoryChecked(parent, "stage", "target", stage); err != nil {
			test.Fatal(err)
		}
		published, err := os.Stat(filepath.Join(parent, "target"))
		if err != nil || !os.SameFile(stage, published) {
			test.Fatalf("published identity changed: %v", err)
		}
		got, err := objectid.FromFileInfo(published)
		if err != nil || got != identity {
			test.Fatalf("published identity = %q, %v; want %q", got, err, identity)
		}
		if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
			test.Fatalf("stage remains after publication: %v", err)
		}
	})
}
