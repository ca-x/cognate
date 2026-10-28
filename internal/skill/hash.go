package skill

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/afero"
)

func ContentHash(filesystem afero.Fs, root string) (string, error) {
	return ContentHashContext(context.Background(), filesystem, root)
}

func ContentHashContext(ctx context.Context, filesystem afero.Fs, root string) (string, error) {
	return hashContext(ctx, filesystem, root, false)
}

func ContentHashIncludingGitContext(ctx context.Context, filesystem afero.Fs, root string) (string, error) {
	return hashContext(ctx, filesystem, root, true)
}

func hashContext(ctx context.Context, filesystem afero.Fs, root string, includeGit bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	filesystem, root, closeRoot, err := confinedSource(filesystem, root)
	if err != nil {
		return "", fmt.Errorf("open skill root: %w", err)
	}
	defer closeRoot()
	return contentHashIncludingGit(ctx, filesystem, root, includeGit)
}

func contentHash(ctx context.Context, filesystem afero.Fs, root string) (string, error) {
	return contentHashIncludingGit(ctx, filesystem, root, false)
}

func contentHashIncludingGit(ctx context.Context, filesystem afero.Fs, root string, includeGit bool) (string, error) {
	return contentHashAvoiding(ctx, filesystem, root, includeGit, nil)
}

func contentHashAvoiding(ctx context.Context, filesystem afero.Fs, root string, includeGit bool, forbidden fs.FileInfo) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	hash := sha256.New()
	err := afero.Walk(filesystem, root, func(path string, info fs.FileInfo, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if forbidden != nil && info.IsDir() && os.SameFile(info, forbidden) {
			return fmt.Errorf("destination cannot be inside source")
		}
		if path == root {
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
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		_, _ = hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(relative))))
		_, _ = io.WriteString(hash, relative)
		if info.IsDir() {
			_, _ = hash.Write([]byte{'d'})
			return nil
		}
		_, _ = hash.Write([]byte{'f'})
		if info.Mode().Perm()&0o111 != 0 {
			_, _ = hash.Write([]byte{1})
		} else {
			_, _ = hash.Write([]byte{0})
		}
		file, err := filesystem.Open(path)
		if err != nil {
			return err
		}
		if err := confirmEntry(filesystem, info, file); err != nil {
			_ = file.Close()
			return err
		}
		fileHash := sha256.New()
		_, readErr := io.Copy(fileHash, contextReader{ctx: ctx, reader: file})
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		_, _ = hash.Write(fileHash.Sum(nil))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash skill: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func contentModeHash(ctx context.Context, filesystem afero.Fs, root string) (string, error) {
	hash := sha256.New()
	err := afero.Walk(filesystem, root, func(path string, info fs.FileInfo, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			if !info.IsDir() {
				return fmt.Errorf("skill root must be a directory")
			}
			return nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		_, _ = hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(relative))))
		_, _ = io.WriteString(hash, relative)
		_, _ = hash.Write(binary.BigEndian.AppendUint32(nil, uint32(info.Mode().Perm())))
		if info.IsDir() {
			_, _ = hash.Write([]byte{'d'})
		} else {
			_, _ = hash.Write([]byte{'f'})
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash skill permissions: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
