package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
)

type deadlineWatchWriter struct{ file *os.File }

func (writer deadlineWatchWriter) Write([]byte) (int, error) {
	return 0, errWatchOutputUnsupported
}

func (writer deadlineWatchWriter) WriteContext(ctx context.Context, data []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	deadline := time.Now().Add(5 * time.Second)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	if err := writer.file.SetWriteDeadline(deadline); err != nil {
		return 0, errWatchOutputFailed
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = writer.file.SetWriteDeadline(time.Now())
		close(finished)
	})
	count, err := writer.file.Write(data)
	if !stop() {
		<-finished
	}
	if resetErr := writer.file.SetWriteDeadline(time.Time{}); resetErr != nil {
		return count, errWatchOutputFailed
	}
	return count, err
}

func prepareWatchFileOutput(ctx context.Context, file *os.File) (io.Writer, func() error, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, nil, errWatchOutputUnsupported
	}
	if info.Mode().IsRegular() {
		return checkedWatchWriter{ctx: ctx, writer: file}, func() error { return nil }, nil
	}
	if info.Mode()&(os.ModeNamedPipe|os.ModeCharDevice) == 0 {
		return nil, nil, errWatchOutputUnsupported
	}
	connection, err := file.SyscallConn()
	if err != nil {
		return nil, nil, errWatchOutputUnsupported
	}
	var owned *os.File
	var openErr error
	err = connection.Control(func(descriptor uintptr) {
		owned, openErr = os.OpenFile(fmt.Sprintf("/proc/self/fd/%d", descriptor), os.O_WRONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	})
	if err != nil || openErr != nil || owned == nil {
		if owned != nil {
			_ = owned.Close()
		}
		return nil, nil, errWatchOutputUnsupported
	}
	ownedInfo, err := owned.Stat()
	if err != nil || !os.SameFile(info, ownedInfo) || owned.SetWriteDeadline(time.Time{}) != nil {
		_ = owned.Close()
		return nil, nil, errWatchOutputUnsupported
	}
	return checkedWatchWriter{ctx: ctx, writer: deadlineWatchWriter{file: owned}}, func() error {
		if err := owned.Close(); err != nil {
			return errWatchOutputFailed
		}
		return nil
	}, nil
}
