package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func fullWatchPipe(test *testing.T) (*os.File, *os.File, int) {
	test.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	descriptor := int(writer.Fd())
	if err := unix.SetNonblock(descriptor, true); err != nil {
		test.Fatal(err)
	}
	for {
		_, err := unix.Write(descriptor, make([]byte, 4096))
		if errors.Is(err, unix.EAGAIN) {
			break
		}
		if err != nil {
			test.Fatal(err)
		}
	}
	flags, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFL, 0)
	if err != nil {
		test.Fatal(err)
	}
	return reader, writer, flags
}

func TestNativeWatchPipeCancellationPreservesCaller(test *testing.T) {
	reader, writer, flags := fullWatchPipe(test)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	output, closeOutput, err := prepareWatchOutput(ctx, writer)
	if err != nil {
		test.Fatal("native full pipe was not supported")
	}
	defer closeOutput()
	started := make(chan struct{})
	completed := make(chan error, 1)
	go func() {
		close(started)
		_, err := output.Write([]byte("safe watch report"))
		completed <- err
	}()
	<-started
	select {
	case <-completed:
		test.Fatal("full pipe write did not wait for capacity")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			test.Fatal("pipe cancellation did not propagate")
		}
	case <-time.After(time.Second):
		_ = reader.Close()
		<-completed
		test.Fatal("pipe cancellation did not wake and drain output")
	}
	connection, err := writer.SyscallConn()
	if err != nil {
		test.Fatal(err)
	}
	var current int
	var flagsErr error
	if err := connection.Control(func(descriptor uintptr) {
		current, flagsErr = unix.FcntlInt(descriptor, unix.F_GETFL, 0)
	}); err != nil || flagsErr != nil || current != flags {
		test.Fatal("watch changed caller descriptor flags")
	}
	readerDescriptor := int(reader.Fd())
	if err := unix.SetNonblock(readerDescriptor, true); err != nil {
		test.Fatal(err)
	}
	for {
		_, err := unix.Read(readerDescriptor, make([]byte, 4096))
		if errors.Is(err, unix.EAGAIN) {
			break
		}
		if err != nil {
			test.Fatal(err)
		}
	}
	if _, err := writer.Write([]byte("caller still owns output")); err != nil {
		test.Fatal("watch closed or retained a deadline on caller output")
	}
}

func TestNativeWatchFullPipeHasBoundedWriteFailure(test *testing.T) {
	_, writer, _ := fullWatchPipe(test)
	output, closeOutput, err := prepareWatchOutput(test.Context(), writer)
	if err != nil {
		test.Fatal(err)
	}
	defer closeOutput()
	if _, err := output.Write([]byte("safe report")); !errors.Is(err, errWatchOutputFailed) {
		test.Fatal("full pipe did not stop with a fixed bounded output error")
	}
}

type opaqueWatchWriter struct{}

func (opaqueWatchWriter) Write(data []byte) (int, error) { return len(data), nil }

func TestPackWatchRejectsOpaqueOutputBeforeStorage(test *testing.T) {
	home := test.TempDir()
	test.Setenv("HOME", home)
	command := New()
	command.SetArgs([]string{"pack", "watch"})
	command.SetOut(opaqueWatchWriter{})
	if !errors.Is(command.ExecuteContext(test.Context()), errWatchOutputUnsupported) {
		test.Fatal("opaque writer did not fail closed")
	}
	if _, err := os.Lstat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("unsupported output touched client storage")
	}
}

func TestNativeWatchRegularFilePreservesOffsetAndOwnership(test *testing.T) {
	file, err := os.CreateTemp(test.TempDir(), "watch-output")
	if err != nil {
		test.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("before:"); err != nil {
		test.Fatal(err)
	}
	output, closeOutput, err := prepareWatchOutput(test.Context(), file)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := output.Write([]byte("report:")); err != nil {
		test.Fatal(err)
	}
	if err := closeOutput(); err != nil {
		test.Fatal(err)
	}
	if _, err := file.WriteString("after"); err != nil {
		test.Fatal("caller file was closed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		test.Fatal(err)
	}
	actual, err := io.ReadAll(file)
	if err != nil || string(actual) != "before:report:after" {
		test.Fatal("watch changed regular file offset or caller contents")
	}
}

func TestNativeWatchCLIUnwrappedFullPipeCancellation(test *testing.T) {
	home := test.TempDir()
	test.Setenv("HOME", home)
	reader, writer, _ := fullWatchPipe(test)
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	command := New()
	command.SetArgs([]string{"pack", "watch"})
	command.SetOut(writer)
	completed := make(chan error, 1)
	go func() { completed <- command.ExecuteContext(ctx) }()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(home, ".local", "share", "skillhub", "client.db")); err == nil {
			break
		}
		select {
		case <-deadline.C:
			cancel()
			_ = reader.Close()
			<-completed
			test.Fatal("watch did not reach its first real cycle")
		case <-ticker.C:
		}
	}
	select {
	case <-completed:
		test.Fatal("full-pipe watch exited before cancellation")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-completed:
		if err != nil {
			test.Fatal("full-pipe foreground cancellation was not clean")
		}
	case <-time.After(time.Second):
		_ = reader.Close()
		<-completed
		test.Fatal("real CLI retained blocked stdout after cancellation")
	}
	if _, err := writer.Stat(); err != nil {
		test.Fatal("CLI closed caller stdout")
	}
}

func TestNativeWatchTerminalOutputRemainsUsable(test *testing.T) {
	descriptor, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		test.Skip("PTY unavailable")
	}
	defer unix.Close(descriptor)
	if err := unix.IoctlSetPointerInt(descriptor, unix.TIOCSPTLCK, 0); err != nil {
		test.Fatal(err)
	}
	number, err := unix.IoctlGetInt(descriptor, unix.TIOCGPTN)
	if err != nil {
		test.Fatal(err)
	}
	terminal, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		test.Fatal(err)
	}
	defer terminal.Close()
	output, closeOutput, err := prepareWatchOutput(test.Context(), terminal)
	if err != nil {
		test.Fatal("interactive terminal is unsupported")
	}
	defer closeOutput()
	if _, err := output.Write([]byte("safe terminal report\n")); err != nil {
		test.Fatal(err)
	}
	if ready, err := unix.Poll([]unix.PollFd{{Fd: int32(descriptor), Events: unix.POLLIN}}, 1000); err != nil || ready != 1 {
		test.Fatal("terminal output did not arrive")
	}
	buffer := make([]byte, 256)
	count, err := unix.Read(descriptor, buffer)
	if err != nil || !strings.Contains(string(buffer[:max(0, count)]), "safe terminal report") {
		test.Fatal("terminal output differs")
	}
}

func TestNativeWatchOutputClosesOnlyOwnedDescriptors(test *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		test.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	readFlags := func() int {
		test.Helper()
		connection, err := writer.SyscallConn()
		if err != nil {
			test.Fatal(err)
		}
		var flags int
		var flagsErr error
		if err := connection.Control(func(descriptor uintptr) {
			flags, flagsErr = unix.FcntlInt(descriptor, unix.F_GETFL, 0)
		}); err != nil || flagsErr != nil {
			test.Fatal("caller flag inspection failed")
		}
		return flags
	}
	flags := readFlags()
	openAndClose := func() {
		test.Helper()
		_, closeOutput, err := prepareWatchOutput(test.Context(), writer)
		if err != nil {
			test.Fatal(err)
		}
		if err := closeOutput(); err != nil {
			test.Fatal(err)
		}
	}
	openAndClose()
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		test.Fatal(err)
	}
	for range 128 {
		openAndClose()
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil || len(before) != len(after) {
		test.Fatal("watch output leaked owned descriptors")
	}
	if readFlags() != flags {
		test.Fatal("normal watch exit changed caller flags")
	}
	if _, err := writer.Write([]byte("caller")); err != nil {
		test.Fatal("caller output was closed")
	}
}
