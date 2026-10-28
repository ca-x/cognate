package skill

import (
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/spf13/afero"
)

type rootFS struct {
	root *os.Root
}

func RootedFS(root *os.Root) afero.Fs { return rootFS{root: root} }

func (filesystem rootFS) Name() string { return "confined-os" }

func (filesystem rootFS) LstatIfPossible(name string) (os.FileInfo, bool, error) {
	info, err := filesystem.root.Lstat(name)
	return info, true, err
}

func (filesystem rootFS) Stat(name string) (os.FileInfo, error) {
	return filesystem.root.Lstat(name)
}

func (filesystem rootFS) Open(name string) (afero.File, error) {
	before, err := filesystem.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() && !before.Mode().IsRegular() {
		return nil, fmt.Errorf("unsupported file type: %s", name)
	}
	file, err := openRootRead(filesystem.root, name)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, fmt.Errorf("file changed while opening: %s", name)
	}
	return file, nil
}

func (filesystem rootFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if flag != os.O_WRONLY|os.O_CREATE|os.O_EXCL {
		return nil, fs.ErrPermission
	}
	file, err := filesystem.root.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (filesystem rootFS) Mkdir(name string, perm os.FileMode) error {
	return filesystem.root.Mkdir(name, perm)
}

func (filesystem rootFS) Remove(name string) error                   { return filesystem.root.Remove(name) }
func (filesystem rootFS) Create(string) (afero.File, error)          { return nil, fs.ErrPermission }
func (filesystem rootFS) MkdirAll(string, os.FileMode) error         { return fs.ErrPermission }
func (filesystem rootFS) RemoveAll(string) error                     { return fs.ErrPermission }
func (filesystem rootFS) Rename(string, string) error                { return fs.ErrPermission }
func (filesystem rootFS) Chmod(string, os.FileMode) error            { return fs.ErrPermission }
func (filesystem rootFS) Chown(string, int, int) error               { return fs.ErrPermission }
func (filesystem rootFS) Chtimes(string, time.Time, time.Time) error { return fs.ErrPermission }

func confinedSource(filesystem afero.Fs, path string) (afero.Fs, string, func(), error) {
	switch filesystem.(type) {
	case *afero.OsFs:
		root, err := os.OpenRoot(path)
		if err != nil {
			return nil, "", nil, err
		}
		return rootFS{root: root}, ".", func() { _ = root.Close() }, nil
	case *afero.MemMapFs:
		return filesystem, path, func() {}, nil
	default:
		return nil, "", nil, fmt.Errorf("filesystem does not support secure snapshot access")
	}
}

func confirmEntry(filesystem afero.Fs, expected fs.FileInfo, file afero.File) error {
	if _, ok := filesystem.(rootFS); !ok {
		return nil
	}
	actual, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(expected, actual) {
		return fmt.Errorf("file changed while opening")
	}
	return nil
}
