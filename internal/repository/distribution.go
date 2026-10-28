package repository

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/ca-x/skillhub/internal/skill"
	"github.com/spf13/afero"
)

const MaxSkillArchiveBytes = maxTarBytes + 1<<20

func WriteSkillArchive(ctx context.Context, data []byte, selectedPath, expectedHash string, destination io.Writer) error {
	if !fs.ValidPath(selectedPath) || strings.Contains(selectedPath, "\\") || len(expectedHash) != 64 {
		return ErrArchiveInvalid
	}
	filesystem, err := unpackArchive(ctx, data)
	if err != nil {
		return err
	}
	root := path.Join("/repository", selectedPath)
	info, err := filesystem.Stat(path.Join(root, "SKILL.md"))
	if err != nil || !info.Mode().IsRegular() {
		return ErrArchiveInvalid
	}
	actual, err := skill.ContentHashContext(ctx, filesystem, root)
	if err != nil {
		return err
	}
	if actual != expectedHash {
		return ErrArchiveInvalid
	}
	compressed := gzip.NewWriter(&boundedArchiveWriter{ctx: ctx, destination: destination, remaining: MaxSkillArchiveBytes})
	archive := tar.NewWriter(compressed)
	err = afero.Walk(filesystem, root, func(filename string, info fs.FileInfo, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if filename != root && info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		header := &tar.Header{Name: path.Join("skill", filepath.ToSlash(relative)), Mode: int64(0o644 | info.Mode().Perm()&0o111), Typeflag: tar.TypeReg, Size: info.Size(), Format: tar.FormatPAX}
		if info.IsDir() {
			header.Name += "/"
			header.Typeflag, header.Mode, header.Size = tar.TypeDir, 0o755, 0
		} else if !info.Mode().IsRegular() {
			return ErrArchiveInvalid
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		file, err := filesystem.Open(filename)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(archive, archiveContextReader{ctx: ctx, reader: file})
		return errors.Join(copyErr, file.Close())
	})
	if err != nil {
		return err
	}
	return errors.Join(archive.Close(), compressed.Close())
}

type boundedArchiveWriter struct {
	ctx         context.Context
	destination io.Writer
	remaining   int
}

func (writer *boundedArchiveWriter) Write(data []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	if len(data) > writer.remaining {
		return 0, ErrArchiveTooLarge
	}
	written, err := writer.destination.Write(data)
	writer.remaining -= written
	if written != len(data) && err == nil {
		err = io.ErrShortWrite
	}
	return written, err
}
