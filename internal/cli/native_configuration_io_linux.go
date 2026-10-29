package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type nativeFile struct {
	file *os.File
	ctx  context.Context
}

func nativeConfigurationPlatform(ctx context.Context) error { return ctx.Err() }

func nativeReopen(file *os.File, write bool) (*os.File, error) {
	before, err := file.Stat()
	if err != nil || before.Mode()&(os.ModeNamedPipe|os.ModeCharDevice) == 0 {
		return nil, errNativeIO
	}
	connection, err := file.SyscallConn()
	if err != nil {
		return nil, errNativeIO
	}
	var owned *os.File
	var openErr error
	flags := os.O_RDONLY
	if write {
		flags = os.O_WRONLY
	}
	err = connection.Control(func(descriptor uintptr) {
		owned, openErr = os.OpenFile(fmt.Sprintf("/proc/self/fd/%d", descriptor), flags|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	})
	if err != nil || openErr != nil || owned == nil {
		return nil, errNativeIO
	}
	after, err := owned.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = owned.Close()
		return nil, errNativeIO
	}
	if after.Mode()&os.ModeCharDevice != 0 && !nativeIsTerminal(owned) {
		_ = owned.Close()
		return nil, errNativeIO
	}
	return owned, nil
}

func nativeIsTerminal(file *os.File) bool {
	connection, err := file.SyscallConn()
	if err != nil {
		return false
	}
	var terminalErr error
	err = connection.Control(func(descriptor uintptr) { _, terminalErr = unix.IoctlGetTermios(int(descriptor), unix.TCGETS) })
	return err == nil && terminalErr == nil
}

func prepareNativeFileInput(ctx context.Context, file *os.File) (io.ReadCloser, error) {
	owned, err := nativeReopen(file, false)
	if err != nil {
		return nil, err
	}
	if nativeIsTerminal(owned) {
		_ = owned.Close()
		return nil, errNativeIO
	}
	if err := owned.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = owned.Close()
		return nil, errNativeIO
	}
	return &nativeFile{file: owned, ctx: ctx}, nil
}

func prepareNativeFileOutput(ctx context.Context, file *os.File) (io.WriteCloser, error) {
	owned, err := nativeReopen(file, true)
	if err != nil {
		return nil, err
	}
	if err := owned.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		_ = owned.Close()
		return nil, errNativeIO
	}
	return &nativeFile{file: owned, ctx: ctx}, nil
}

func openNativeConfirmationTTY(ctx context.Context) (nativeTerminal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile("/dev/tty", os.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errNativeIO
	}
	if !nativeIsTerminal(file) {
		_ = file.Close()
		return nil, errNativeIO
	}
	if err := errors.Join(file.SetReadDeadline(time.Now().Add(time.Minute)), file.SetWriteDeadline(time.Now().Add(5*time.Second))); err != nil {
		_ = file.Close()
		return nil, errNativeIO
	}
	return &nativeFile{file: file, ctx: ctx}, nil
}

func (file *nativeFile) Read(data []byte) (int, error)  { return file.ReadContext(file.ctx, data) }
func (file *nativeFile) Write(data []byte) (int, error) { return file.WriteContext(file.ctx, data) }
func (file *nativeFile) Close() error                   { return file.file.Close() }

func (file *nativeFile) operation(ctx context.Context, write bool, action func() (int, error)) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	deadline := time.Now().Add(5 * time.Second)
	if !write {
		deadline = time.Now().Add(time.Minute)
	}
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	set := file.file.SetReadDeadline
	if write {
		set = file.file.SetWriteDeadline
	}
	if err := set(deadline); err != nil {
		return 0, errNativeIO
	}
	joined := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = set(time.Now()); close(joined) })
	count, err := action()
	if !stop() {
		<-joined
	}
	if resetErr := set(time.Time{}); resetErr != nil {
		return count, errNativeIO
	}
	if errors.Is(err, io.EOF) {
		return count, io.EOF
	}
	if err != nil {
		return count, errNativeIO
	}
	return count, ctx.Err()
}
func (file *nativeFile) ReadContext(ctx context.Context, data []byte) (int, error) {
	return file.operation(ctx, false, func() (int, error) { return file.file.Read(data) })
}
func (file *nativeFile) WriteContext(ctx context.Context, data []byte) (int, error) {
	return file.operation(ctx, true, func() (int, error) { return file.file.Write(data) })
}
