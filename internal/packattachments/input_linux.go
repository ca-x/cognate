package packattachments

import (
	"context"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func readReferencesFile(ctx context.Context, path string) (data []byte, result error) {
	if ctx.Err() != nil || path == "" {
		return nil, ErrInvalid
	}
	for part := range strings.SplitSeq(path, "/") {
		if part == ".." {
			return nil, ErrInvalid
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrInvalid
	}
	directory, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrInvalid
	}
	defer func() {
		if directory >= 0 && unix.Close(directory) != nil {
			clear(data)
			data = nil
			result = ErrInvalid
		}
	}()
	parts := strings.Split(strings.TrimPrefix(absolute, "/"), "/")
	for index, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if index < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(directory, part, flags, 0)
		if err != nil {
			return nil, ErrInvalid
		}
		if index < len(parts)-1 {
			previous := directory
			directory = -1
			if unix.Close(previous) != nil {
				_ = unix.Close(next)
				return nil, ErrInvalid
			}
			directory = next
			continue
		}
		file := os.NewFile(uintptr(next), "pack attachment references")
		defer func() {
			if file.Close() != nil {
				clear(data)
				data = nil
				result = ErrInvalid
			}
		}()
		before, err := file.Stat()
		if err != nil || !before.Mode().IsRegular() || before.Size() < 2 || before.Size() > 64<<10 {
			return nil, ErrInvalid
		}
		info, ok := before.Sys().(*syscall.Stat_t)
		if !ok || info.Uid != uint32(os.Geteuid()) || info.Nlink != 1 || before.Mode().Perm()&0022 != 0 || before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return nil, ErrInvalid
		}
		data, err = io.ReadAll(io.LimitReader(file, (64<<10)+1))
		after, statErr := file.Stat()
		if err != nil || statErr != nil || int64(len(data)) != before.Size() || !os.SameFile(before, after) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || ctx.Err() != nil {
			clear(data)
			return nil, ErrInvalid
		}
		afterInfo, ok := after.Sys().(*syscall.Stat_t)
		if !ok || afterInfo.Ctim != info.Ctim || afterInfo.Nlink != info.Nlink || afterInfo.Uid != info.Uid || after.Mode() != before.Mode() {
			clear(data)
			return nil, ErrInvalid
		}
		return data, nil
	}
	return nil, ErrInvalid
}
