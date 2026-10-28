package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func copySnapshotTo(filesystem afero.Fs, source, destination string, expectedDestination fs.FileInfo) error {
	var sourcePath, destinationPath string
	var err error
	sourceToOpen := source
	if _, ok := filesystem.(*afero.OsFs); ok {
		sourcePath, err = ResolvePath(source)
		if err != nil {
			return fmt.Errorf("resolve source: %w", err)
		}
		sourceToOpen = sourcePath
		destinationPath, err = ResolvePath(destination)
		if err != nil {
			return fmt.Errorf("resolve destination: %w", err)
		}
	} else {
		sourcePath, err = filepath.Abs(source)
		if err != nil {
			return fmt.Errorf("resolve source: %w", err)
		}
		destinationPath, err = filepath.Abs(destination)
		if err != nil {
			return fmt.Errorf("resolve destination: %w", err)
		}
	}
	relative, err := filepath.Rel(sourcePath, destinationPath)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("destination cannot be inside source")
	}
	sourceFS, sourceRoot, closeSource, err := confinedSource(filesystem, sourceToOpen)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer closeSource()
	var destinationFS afero.Fs
	var destinationRoot string
	var destinationInfo fs.FileInfo
	switch filesystem.(type) {
	case *afero.OsFs:
		info, err := os.Lstat(destination)
		if err != nil {
			return fmt.Errorf("inspect destination: %w", err)
		}
		if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("destination must be a directory, not a symlink")
		}
		root, err := os.OpenRoot(destination)
		if err != nil {
			return fmt.Errorf("open destination: %w", err)
		}
		defer root.Close()
		destinationInfo, err = root.Stat(".")
		if err != nil || !os.SameFile(info, destinationInfo) {
			return fmt.Errorf("destination changed while opening")
		}
		if expectedDestination != nil && !os.SameFile(expectedDestination, destinationInfo) {
			return fmt.Errorf("destination no longer matches the owned stage")
		}
		destinationFS, destinationRoot = rootFS{root: root}, "."
	case *afero.MemMapFs:
		destinationFS, destinationRoot = filesystem, destination
	default:
		return fmt.Errorf("filesystem does not support secure snapshot access")
	}
	return copySnapshotContents(context.Background(), sourceFS, sourceRoot, destinationFS, destinationRoot, destinationInfo, false)
}

func copySnapshotContents(ctx context.Context, sourceFS afero.Fs, sourceRoot string, destinationFS afero.Fs, destinationRoot string, destinationInfo fs.FileInfo, includeGit bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := destinationFS.Stat(destinationRoot)
	if err != nil {
		return fmt.Errorf("inspect destination: %w", err)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("destination must be a directory, not a symlink")
	}
	entries, err := afero.ReadDir(destinationFS, destinationRoot)
	if err != nil {
		return fmt.Errorf("read destination: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("destination must be empty")
	}
	type directoryMode struct {
		path string
		mode fs.FileMode
	}
	var directories []directoryMode
	err = afero.Walk(sourceFS, sourceRoot, func(path string, info fs.FileInfo, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if destinationInfo != nil && info.IsDir() && os.SameFile(info, destinationInfo) {
			return fmt.Errorf("destination cannot be inside source")
		}
		if path == sourceRoot {
			if !info.IsDir() {
				return fmt.Errorf("skill root must be a directory")
			}
			return nil
		}
		if !includeGit && info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", path)
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destinationRoot, relative)
		if info.IsDir() {
			if err := destinationFS.Mkdir(target, 0o700); err != nil {
				return err
			}
			if includeGit {
				directories = append(directories, directoryMode{path: target, mode: info.Mode().Perm()})
			}
			return nil
		}
		return copySnapshotFile(ctx, sourceFS, destinationFS, path, target, info)
	})
	if err != nil {
		return fmt.Errorf("copy skill snapshot: %w", err)
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		directory, err := destinationFS.Open(directories[index].path)
		if err != nil {
			return fmt.Errorf("open copied directory: %w", err)
		}
		chmod, ok := directory.(interface{ Chmod(os.FileMode) error })
		if !ok {
			_ = directory.Close()
			return fmt.Errorf("copied directory does not support handle-based chmod")
		}
		if err := errors.Join(chmod.Chmod(directories[index].mode), directory.Close()); err != nil {
			return fmt.Errorf("preserve copied directory permissions: %w", err)
		}
	}
	return ctx.Err()
}

func copySnapshotFile(ctx context.Context, sourceFS, destinationFS afero.Fs, source, destination string, info fs.FileInfo) error {
	input, err := sourceFS.Open(source)
	if err != nil {
		return err
	}
	if err := confirmEntry(sourceFS, info, input); err != nil {
		_ = input.Close()
		return err
	}
	output, err := destinationFS.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		_ = input.Close()
		return err
	}
	_, copyErr := io.Copy(output, contextReader{ctx: ctx, reader: input})
	if copyErr == nil {
		copyErr = ctx.Err()
	}
	var modeErr error
	if copyErr == nil {
		if chmod, ok := output.(interface{ Chmod(os.FileMode) error }); ok {
			modeErr = chmod.Chmod(info.Mode().Perm())
		} else {
			modeErr = destinationFS.Chmod(destination, info.Mode().Perm())
		}
	}
	return errors.Join(copyErr, modeErr, input.Close(), output.Close())
}
