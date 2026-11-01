package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ca-x/cognate/internal/clientkey"
	"golang.org/x/sys/unix"
)

func preparePlatformClientKeyOutput(ctx context.Context, writer io.Writer) (io.WriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.GOARCH != "arm64" {
		return nil, clientkey.ErrUnsupported
	}
	switch value := writer.(type) {
	case *bytes.Buffer:
		if value == nil {
			return nil, clientkey.ErrUnavailable
		}
		return &nativeOutput{ctx: ctx, writer: value, close: func() error { return nil }, remaining: 4 << 10}, nil
	case *os.File:
		owned, err := openDarwinClientKeyOutput(ctx, value)
		if err != nil {
			return nil, err
		}
		return &nativeOutput{ctx: ctx, writer: owned, close: owned.Close, remaining: 4 << 10}, nil
	default:
		return nil, clientkey.ErrUnsupported
	}
}

func darwinClientKeyTTYPath(descriptor uintptr) (string, error) {
	buffer := new([1024]byte)
	var pin runtime.Pinner
	pin.Pin(buffer)
	defer pin.Unpin()
	_, err := unix.FcntlInt(descriptor, unix.F_GETPATH, int(uintptr(unsafe.Pointer(&buffer[0]))))
	runtime.KeepAlive(buffer)
	if err != nil {
		return "", clientkey.ErrUnsupported
	}
	return darwinClientKeyTTYLeaf(buffer[:])
}

func darwinClientKeyTTYLeaf(buffer []byte) (string, error) {
	end := bytes.IndexByte(buffer, 0)
	if len(buffer) != 1024 || end <= 0 {
		return "", clientkey.ErrUnsupported
	}
	leaf, found := strings.CutPrefix(string(buffer[:end]), "/dev/")
	if !found || !strings.HasPrefix(leaf, "tty") || len(leaf) <= 3 {
		return "", clientkey.ErrUnsupported
	}
	for _, character := range leaf {
		if character < 'a' || character > 'z' {
			if character < 'A' || character > 'Z' {
				if character < '0' || character > '9' {
					return "", clientkey.ErrUnsupported
				}
			}
		}
	}
	return leaf, nil
}

func darwinClientKeySameTTY(first, second *unix.Stat_t) bool {
	return first.Mode&unix.S_IFMT == unix.S_IFCHR && second.Mode&unix.S_IFMT == unix.S_IFCHR && first.Dev == second.Dev && first.Ino == second.Ino && first.Rdev == second.Rdev
}

func darwinClientKeyDevFS(descriptor int) bool {
	var filesystem unix.Statfs_t
	return unix.Fstatfs(descriptor, &filesystem) == nil && unix.ByteSliceToString(filesystem.Fstypename[:]) == "devfs"
}

func openDarwinClientKeyOutput(ctx context.Context, file *os.File) (*darwinClientKeyFileOutput, error) {
	if file == nil {
		return nil, clientkey.ErrUnsupported
	}
	connection, err := file.SyscallConn()
	if err != nil {
		return nil, clientkey.ErrUnsupported
	}
	owned := -1
	var admittedErr error
	err = connection.Control(func(descriptor uintptr) {
		owned, admittedErr = reopenDarwinClientKeyTTY(int(descriptor))
	})
	if err != nil || admittedErr != nil || owned < 0 {
		if owned >= 0 {
			if unix.Close(owned) != nil {
				return nil, clientkey.ErrUnavailable
			}
		}
		if admittedErr != nil {
			return nil, admittedErr
		}
		return nil, clientkey.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		if unix.Close(owned) != nil {
			return nil, errors.Join(err, clientkey.ErrUnavailable)
		}
		return nil, err
	}
	return newDarwinClientKeyFileOutput(ctx, owned), nil
}

func reopenDarwinClientKeyTTY(descriptor int) (owned int, resultErr error) {
	owned = -1
	var original unix.Stat_t
	flags, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE == unix.O_RDONLY || unix.Fstat(descriptor, &original) != nil || original.Mode&unix.S_IFMT != unix.S_IFCHR {
		return -1, clientkey.ErrUnsupported
	}
	if _, err := unix.IoctlGetTermios(descriptor, unix.TIOCGETA); err != nil {
		return -1, clientkey.ErrUnsupported
	}
	leaf, err := darwinClientKeyTTYPath(uintptr(descriptor))
	if err != nil {
		return -1, err
	}
	parent, err := unix.Open("/dev", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, clientkey.ErrUnsupported
	}
	defer func() {
		if unix.Close(parent) != nil {
			resultErr = clientkey.ErrUnavailable
		}
		if resultErr != nil && owned >= 0 {
			if unix.Close(owned) != nil {
				resultErr = errors.Join(resultErr, clientkey.ErrUnavailable)
			}
			owned = -1
		}
	}()
	var directory, linked unix.Stat_t
	if unix.Fstat(parent, &directory) != nil || directory.Mode&unix.S_IFMT != unix.S_IFDIR || directory.Uid != 0 || directory.Mode&0o022 != 0 || !darwinClientKeyDevFS(parent) || unix.Fstatat(parent, leaf, &linked, unix.AT_SYMLINK_NOFOLLOW) != nil || !darwinClientKeySameTTY(&original, &linked) {
		return -1, clientkey.ErrUnsupported
	}
	owned, err = unix.Openat(parent, leaf, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, clientkey.ErrUnsupported
	}
	var opened, after, caller unix.Stat_t
	if unix.Fstat(owned, &opened) != nil || !darwinClientKeyDevFS(owned) || unix.Fstatat(parent, leaf, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstat(descriptor, &caller) != nil || !darwinClientKeySameTTY(&original, &opened) || !darwinClientKeySameTTY(&opened, &after) || !darwinClientKeySameTTY(&original, &caller) {
		return owned, clientkey.ErrUnsupported
	}
	current, currentErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
	ownedFlags, flagsErr := unix.FcntlInt(uintptr(owned), unix.F_GETFL, 0)
	closeFlags, closeErr := unix.FcntlInt(uintptr(owned), unix.F_GETFD, 0)
	_, terminalErr := unix.IoctlGetTermios(owned, unix.TIOCGETA)
	if currentErr != nil || flagsErr != nil || closeErr != nil || terminalErr != nil || current != flags || ownedFlags&unix.O_NONBLOCK == 0 || ownedFlags&unix.O_ACCMODE != unix.O_WRONLY || closeFlags&unix.FD_CLOEXEC == 0 {
		return owned, clientkey.ErrUnsupported
	}
	return owned, nil
}

type darwinClientKeyFileOutput struct {
	ctx     context.Context
	file    int
	permit  chan struct{}
	closing chan struct{}
	close   func() error
}

func newDarwinClientKeyFileOutput(ctx context.Context, descriptor int) *darwinClientKeyFileOutput {
	output := &darwinClientKeyFileOutput{ctx: ctx, file: descriptor, permit: make(chan struct{}, 1), closing: make(chan struct{})}
	output.permit <- struct{}{}
	output.close = sync.OnceValue(func() error {
		close(output.closing)
		<-output.permit
		if unix.Close(output.file) != nil {
			return clientkey.ErrUnavailable
		}
		return nil
	})
	return output
}

func (output *darwinClientKeyFileOutput) Close() error { return output.close() }
func (output *darwinClientKeyFileOutput) Write(data []byte) (int, error) {
	return output.WriteContext(output.ctx, data)
}

func (output *darwinClientKeyFileOutput) status(ctx context.Context, deadline time.Time) error {
	if err := output.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-output.closing:
		return clientkey.ErrUnavailable
	default:
	}
	if !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func (output *darwinClientKeyFileOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	deadline := time.Now().Add(5 * time.Second)
	for _, parent := range []context.Context{output.ctx, ctx} {
		if limit, ok := parent.Deadline(); ok && limit.Before(deadline) {
			deadline = limit
		}
	}
	if err := output.status(ctx, deadline); err != nil {
		return 0, err
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-output.ctx.Done():
		return 0, output.ctx.Err()
	case <-output.closing:
		return 0, clientkey.ErrUnavailable
	case <-timer.C:
		return 0, context.DeadlineExceeded
	case <-output.permit:
	}
	defer func() { output.permit <- struct{}{} }()
	written := 0
	for {
		if err := output.status(ctx, deadline); err != nil {
			return written, err
		}
		if written == len(data) {
			return written, nil
		}
		count, err := unix.Write(output.file, data[written:])
		if count > 0 {
			written += count
		}
		if err == nil && count > 0 || errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EAGAIN) {
			if canceled := output.status(ctx, deadline); canceled != nil {
				return written, canceled
			}
			return written, clientkey.ErrUnavailable
		}
		wait := min(20*time.Millisecond, time.Until(deadline))
		if wait <= 0 {
			continue
		}
		events := []unix.PollFd{{Fd: int32(output.file), Events: unix.POLLOUT}}
		_, err = unix.Poll(events, int((wait+time.Millisecond-1)/time.Millisecond))
		if err != nil && !errors.Is(err, unix.EINTR) || events[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			if canceled := output.status(ctx, deadline); canceled != nil {
				return written, canceled
			}
			return written, clientkey.ErrUnavailable
		}
	}
}
