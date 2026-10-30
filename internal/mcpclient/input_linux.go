package mcpclient

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func readInputFile(ctx context.Context, path string, maximum int64, secret bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, safeContext(err)
	}
	if path == "" {
		return nil, ErrInvalid
	}
	for component := range strings.SplitSeq(path, "/") {
		if component == ".." {
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
	defer func() { _ = unix.Close(directory) }()
	parts := strings.Split(strings.TrimPrefix(absolute, "/"), "/")
	for index, component := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if index < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(directory, component, flags, 0)
		if err != nil {
			return nil, ErrInvalid
		}
		if index != len(parts)-1 {
			_ = unix.Close(directory)
			directory = next
			continue
		}
		file := os.NewFile(uintptr(next), "private MCP input")
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
			return nil, ErrInvalid
		}
		if secret {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0177 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
				return nil, ErrInvalid
			}
		}
		data, err := io.ReadAll(io.LimitReader(file, maximum+1))
		if err != nil || int64(len(data)) > maximum {
			clear(data)
			return nil, ErrInvalid
		}
		if err := ctx.Err(); err != nil {
			clear(data)
			return nil, safeContext(err)
		}
		return data, nil
	}
	return nil, ErrInvalid
}
