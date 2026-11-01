//go:build darwin && arm64

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/clientkey"
	"golang.org/x/sys/unix"
)

func TestDarwinClientKeyTTYPathGrammar(test *testing.T) {
	for _, value := range []string{"/dev/ttys000", "/dev/ttyA7"} {
		buffer := make([]byte, 1024)
		copy(buffer, value)
		if leaf, err := darwinClientKeyTTYLeaf(buffer); err != nil || leaf != strings.TrimPrefix(value, "/dev/") {
			test.Fatal("fixed devfs terminal path rejected")
		}
	}
	for _, value := range []string{"", "/dev/tty", "/dev/fd/1", "/dev/tty/child", "/dev/tty../other", "/tmp/ttys000", "/dev/tty\nsecret", "/dev/ttyé", "/dev/tty\\name"} {
		buffer := make([]byte, 1024)
		copy(buffer, value)
		if _, err := darwinClientKeyTTYLeaf(buffer); !errors.Is(err, clientkey.ErrUnsupported) {
			test.Fatal("untrusted terminal path accepted")
		}
	}
	for _, buffer := range [][]byte{[]byte("/dev/ttys000\x00"), bytes.Repeat([]byte{'a'}, 1024)} {
		if _, err := darwinClientKeyTTYLeaf(buffer); err == nil {
			test.Fatal("unbounded or unterminated path accepted")
		}
	}
}

func TestDarwinClientKeyTTYIdentity(test *testing.T) {
	original := unix.Stat_t{Mode: unix.S_IFCHR, Dev: 7, Ino: 11, Rdev: 19}
	if !darwinClientKeySameTTY(&original, &original) {
		test.Fatal("matching character device rejected")
	}
	for _, other := range []unix.Stat_t{
		{Mode: unix.S_IFCHR, Dev: 8, Ino: 11, Rdev: 19},
		{Mode: unix.S_IFCHR, Dev: 7, Ino: 12, Rdev: 19},
		{Mode: unix.S_IFCHR, Dev: 7, Ino: 11, Rdev: 20},
		{Mode: unix.S_IFREG, Dev: 7, Ino: 11, Rdev: 19},
	} {
		if darwinClientKeySameTTY(&original, &other) || darwinClientKeySameTTY(&other, &original) {
			test.Fatal("different terminal identity or type accepted")
		}
	}
}

func darwinClientKeyFlags(test *testing.T, file *os.File) (int, int) {
	test.Helper()
	connection, err := file.SyscallConn()
	if err != nil {
		test.Fatal("borrowed fixture descriptor unavailable")
	}
	var flags, descriptorFlags int
	var inspectErr error
	err = connection.Control(func(descriptor uintptr) {
		flags, inspectErr = unix.FcntlInt(descriptor, unix.F_GETFL, 0)
		if inspectErr == nil {
			descriptorFlags, inspectErr = unix.FcntlInt(descriptor, unix.F_GETFD, 0)
		}
	})
	if err != nil || inspectErr != nil {
		test.Fatal("fixture flags unavailable")
	}
	return flags, descriptorFlags
}

func TestDarwinClientKeySinkRefusals(test *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		test.Fatal("pipe fixture unavailable")
	}
	defer reader.Close()
	defer writer.Close()
	regular, err := os.CreateTemp(test.TempDir(), "regular")
	if err != nil {
		test.Fatal("regular fixture unavailable")
	}
	defer regular.Close()
	fifoPath := filepath.Join(test.TempDir(), "fifo")
	if unix.Mkfifo(fifoPath, 0o600) != nil {
		test.Fatal("FIFO fixture unavailable")
	}
	fifoFD, err := unix.Open(fifoPath, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		test.Fatal("FIFO descriptor unavailable")
	}
	fifo := os.NewFile(uintptr(fifoFD), "synthetic-fifo")
	defer fifo.Close()
	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		test.Fatal("socket fixture unavailable")
	}
	socket := os.NewFile(uintptr(sockets[0]), "synthetic-socket")
	defer socket.Close()
	defer unix.Close(sockets[1])
	device, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		test.Fatal("non-terminal fixture unavailable")
	}
	defer device.Close()
	for _, file := range []*os.File{writer, fifo, regular, socket, device} {
		home := darwinClientKeyHome(test)
		before, beforeFD := darwinClientKeyFlags(test, file)
		command := New()
		command.SetArgs([]string{"client-key", "init"})
		command.SetOut(file)
		if !errors.Is(command.ExecuteContext(test.Context()), clientkey.ErrUnsupported) {
			test.Fatal("non-terminal output did not refuse before init")
		}
		after, afterFD := darwinClientKeyFlags(test, file)
		if before != after || beforeFD != afterFD {
			test.Fatal("output refusal changed borrowed flags")
		}
		assertClientKeyHomeEmpty(test, home)
	}
	for _, output := range []io.Writer{new(strings.Builder), io.Discard, (*os.File)(nil), (*bytes.Buffer)(nil)} {
		if prepared, err := prepareClientKeyOutput(test.Context(), output); err == nil || prepared != nil {
			test.Fatal("unknown or nil output accepted")
		}
	}
}

func darwinClientKeyOwnedPipe(test *testing.T, full bool) (*darwinClientKeyFileOutput, int) {
	test.Helper()
	descriptors := make([]int, 2)
	if unix.Pipe(descriptors) != nil || unix.SetNonblock(descriptors[1], true) != nil {
		test.Fatal("owned low-level pipe fixture unavailable")
	}
	test.Cleanup(func() { _ = unix.Close(descriptors[0]) })
	if full {
		filled := false
		for range 2048 {
			_, err := unix.Write(descriptors[1], make([]byte, 4096))
			if errors.Is(err, unix.EAGAIN) {
				filled = true
				break
			}
			if err != nil {
				test.Fatal("owned pipe fill failed")
			}
		}
		if !filled {
			_ = unix.Close(descriptors[1])
			test.Fatal("owned pipe did not reach bounded full state")
		}
	}
	output := newDarwinClientKeyFileOutput(test.Context(), descriptors[1])
	test.Cleanup(func() { _ = output.Close() })
	return output, descriptors[0]
}

func TestDarwinClientKeyOwnedWriteLifecycle(test *testing.T) {
	output, reader := darwinClientKeyOwnedPipe(test, false)
	if count, err := output.WriteContext(test.Context(), []byte("owned-payload")); err != nil || count != 13 {
		test.Fatal("owned output incomplete")
	}
	data := make([]byte, 32)
	if count, err := unix.Read(reader, data); err != nil || string(data[:count]) != "owned-payload" {
		test.Fatal("owned output routed incorrectly")
	}
	if output.Close() != nil || output.Close() != nil {
		test.Fatal("owned close not idempotent")
	}
	if _, err := output.WriteContext(test.Context(), []byte("late")); !errors.Is(err, clientkey.ErrUnavailable) {
		test.Fatal("closed descriptor was reused")
	}
	if _, err := unix.FcntlInt(uintptr(reader), unix.F_GETFL, 0); err != nil {
		test.Fatal("close consumed the unowned reader")
	}
}

func TestDarwinClientKeyOwnedCancellationAndQueue(test *testing.T) {
	output, _ := darwinClientKeyOwnedPipe(test, true)
	ctx, cancel := context.WithTimeout(test.Context(), 70*time.Millisecond)
	defer cancel()
	started := time.Now()
	if count, err := output.WriteContext(ctx, []byte("blocked")); count != 0 || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		test.Fatal("full owned output did not honor deadline")
	}
	<-output.permit
	queued, stop := context.WithTimeout(test.Context(), 50*time.Millisecond)
	defer stop()
	count, err := output.WriteContext(queued, []byte("queued"))
	output.permit <- struct{}{}
	if count != 0 || !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal("queued output lost cancellation")
	}
}

func TestDarwinClientKeyOwnedCloseJoins(test *testing.T) {
	output, _ := darwinClientKeyOwnedPipe(test, true)
	darwinClientKeyCheckCloseJoins(test, output)
}

func darwinClientKeyCheckCloseJoins(test *testing.T, output *darwinClientKeyFileOutput) {
	test.Helper()
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := output.WriteContext(ctx, bytes.Repeat([]byte{'z'}, 1<<20)); done <- err }()
	defer func() { cancel(); _ = output.Close(); <-done }()
	deadline := time.Now().Add(time.Second)
	for len(output.permit) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(output.permit) != 0 {
		test.Fatal("writer did not enter owned operation")
	}
	started := time.Now()
	if err := output.Close(); err != nil || time.Since(started) > time.Second {
		test.Fatal("close did not join bounded writer")
	}
	select {
	case err := <-done:
		done <- err
		if !errors.Is(err, clientkey.ErrUnavailable) {
			test.Fatal("closed active write reported success")
		}
	case <-time.After(time.Second):
		test.Fatal("owned writer escaped Close")
	}
}

func TestDarwinClientKeyOwnedPlainCancellation(test *testing.T) {
	output, _ := darwinClientKeyOwnedPipe(test, true)
	darwinClientKeyCheckPlainCancel(test, output)
}

func darwinClientKeyCheckPlainCancel(test *testing.T, output *darwinClientKeyFileOutput) {
	test.Helper()
	ctx, cancel := context.WithCancel(context.WithoutCancel(test.Context()))
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := output.WriteContext(ctx, bytes.Repeat([]byte{'z'}, 1<<20)); done <- err }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(time.Second)
	for len(output.permit) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(output.permit) != 0 {
		test.Fatal("plain-cancel writer never entered operation")
	}
	cancel()
	select {
	case err := <-done:
		done <- err
		if !errors.Is(err, context.Canceled) {
			test.Fatal("plain cancellation classification lost")
		}
	case <-time.After(time.Second):
		_ = output.Close()
		test.Fatal("plain cancellation failed to drain")
	}
}

func TestDarwinClientKeyOwnedFiveSecondCap(test *testing.T) {
	output, _ := darwinClientKeyOwnedPipe(test, true)
	started := time.Now()
	if _, err := output.WriteContext(test.Context(), []byte("blocked")); !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 6*time.Second {
		test.Fatal("owned output lost fixed five-second cap")
	}
}

func TestDarwinClientKeyOwnedCloseError(test *testing.T) {
	output, _ := darwinClientKeyOwnedPipe(test, false)
	if unix.Close(output.file) != nil {
		test.Fatal("close-error fixture unavailable")
	}
	if !errors.Is(output.Close(), clientkey.ErrUnavailable) {
		test.Fatal("close failure swallowed")
	}
	file, err := os.CreateTemp(test.TempDir(), "descriptor-reuse")
	if err != nil {
		test.Fatal("descriptor-reuse fixture unavailable")
	}
	defer file.Close()
	if !errors.Is(output.Close(), clientkey.ErrUnavailable) {
		test.Fatal("close error not retained")
	}
	if _, err := file.WriteString("still-owned"); err != nil {
		test.Fatal("second Close consumed a recycled descriptor")
	}
}

func TestDarwinClientKeyPTYIO(test *testing.T) {
	if os.Getenv("COGNATE_DARWIN_KEY_IO_CHILD") == "1" {
		darwinClientKeyPTYChild(test)
		return
	}
	environment, _ := darwinPTYEnvironment(test)
	python := "import os,pty,sys\nmaster,slave=pty.openpty()\nos.set_blocking(master,False)\nos.set_inheritable(master,True)\nos.set_inheritable(slave,True)\nos.environ['COGNATE_DARWIN_KEY_IO_MASTER']=str(master)\nos.environ['COGNATE_DARWIN_KEY_IO_SLAVE']=str(slave)\nos.execv(sys.argv[1],[sys.argv[1],'-test.run=^TestDarwinClientKeyPTYIO$','-test.count=1'])\n"
	output, code := darwinPTYRun(test, append(environment, "COGNATE_DARWIN_KEY_IO_CHILD=1"), "/usr/bin/python3", "-c", python, os.Args[0])
	if code != 0 || !strings.Contains(output, "COGNATE_DARWIN_KEY_IO_OK") || strings.Contains(output, "COGNATE_SECOND_TTY") {
		test.Fatal("actual separate-terminal output/ownership failed")
	}
}

func darwinClientKeyPTYChild(test *testing.T) {
	test.Helper()
	masterFD, masterErr := strconv.Atoi(os.Getenv("COGNATE_DARWIN_KEY_IO_MASTER"))
	slaveFD, slaveErr := strconv.Atoi(os.Getenv("COGNATE_DARWIN_KEY_IO_SLAVE"))
	if masterErr != nil || slaveErr != nil || masterFD <= 2 || slaveFD <= 2 || masterFD == slaveFD {
		test.Fatal("private native terminal fixture missing")
	}
	master := os.NewFile(uintptr(masterFD), "private-pty-master")
	slave := os.NewFile(uintptr(slaveFD), "private-pty-slave")
	defer master.Close()
	defer slave.Close()
	var first, second unix.Stat_t
	connection, err := os.Stdout.SyscallConn()
	if err != nil || connection.Control(func(descriptor uintptr) { err = unix.Fstat(int(descriptor), &first) }) != nil || err != nil || unix.Fstat(slaveFD, &second) != nil || darwinClientKeySameTTY(&first, &second) {
		test.Fatal("real distinct terminal identities not distinguished")
	}
	before, beforeFD := darwinClientKeyFlags(test, slave)
	termios, err := unix.IoctlGetTermios(slaveFD, unix.TIOCGETA)
	if err != nil || before&unix.O_NONBLOCK != 0 {
		test.Fatal("blocking caller terminal fixture unavailable")
	}
	owned, err := openDarwinClientKeyOutput(test.Context(), slave)
	if err != nil {
		test.Fatal("F_GETPATH exact-terminal independent open failed")
	}
	if count, err := owned.WriteContext(test.Context(), []byte("COGNATE_SECOND_TTY\n")); err != nil || count != 19 {
		_ = owned.Close()
		test.Fatal("actual terminal write incomplete")
	}
	if owned.Close() != nil || owned.Close() != nil {
		test.Fatal("actual owned terminal close failed")
	}
	after, afterFD := darwinClientKeyFlags(test, slave)
	current, err := unix.IoctlGetTermios(slaveFD, unix.TIOCGETA)
	if err != nil || *termios != *current || before != after || beforeFD != afterFD {
		test.Fatal("independent open modified borrowed terminal state")
	}
	events := []unix.PollFd{{Fd: int32(masterFD), Events: unix.POLLIN}}
	if ready, err := unix.Poll(events, 1000); err != nil || ready != 1 {
		test.Fatal("actual destination terminal did not receive bytes")
	}
	data := make([]byte, 1024)
	count, err := unix.Read(masterFD, data)
	if err != nil || !bytes.Contains(data[:max(count, 0)], []byte("COGNATE_SECOND_TTY")) {
		test.Fatal("actual terminal routing mismatch")
	}
	if count, err := unix.Write(slaveFD, []byte("BORROWED_USABLE")); err != nil || count != 15 {
		test.Fatal("owned Close consumed borrowed terminal")
	}
	blocked, err := openDarwinClientKeyOutput(test.Context(), slave)
	if err != nil {
		test.Fatal("backpressure terminal admission failed")
	}
	defer blocked.Close()
	ctx, cancel := context.WithTimeout(test.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	count, err = blocked.WriteContext(ctx, bytes.Repeat([]byte{'x'}, 1<<20))
	if !errors.Is(err, context.DeadlineExceeded) || count <= 0 || count >= 1<<20 || time.Since(started) > time.Second {
		test.Fatal("actual full terminal did not stop with partial progress")
	}
	started = time.Now()
	if count, err := blocked.WriteContext(context.WithoutCancel(test.Context()), bytes.Repeat([]byte{'z'}, 1<<20)); !errors.Is(err, context.DeadlineExceeded) || count >= 1<<20 || time.Since(started) > 6*time.Second {
		test.Fatal("actual terminal lost fixed five-second cap")
	}
	darwinClientKeyCheckPlainCancel(test, blocked)
	darwinClientKeyCheckCloseJoins(test, blocked)
	disconnected, err := openDarwinClientKeyOutput(test.Context(), slave)
	if err != nil {
		test.Fatal("disconnect fixture admission failed")
	}
	defer disconnected.Close()
	if master.Close() != nil {
		test.Fatal("native disconnect failed")
	}
	if _, err := disconnected.WriteContext(test.Context(), []byte("disconnected")); err == nil {
		test.Fatal("disconnected terminal reported success")
	}
	if _, err := io.WriteString(os.Stdout, "COGNATE_DARWIN_KEY_IO_OK\n"); err != nil {
		test.Fatal("original terminal unavailable after owned cleanup")
	}
}

func TestDarwinClientKeyPTYActualMainErrors(test *testing.T) {
	binary := os.Getenv("COGNATE_DARWIN_BINARY")
	if !filepath.IsAbs(binary) {
		test.Fatal("actual caller binary fixture missing")
	}
	environment, home := darwinPTYEnvironment(test)
	for _, arguments := range [][]string{{"client-key", "unknown-private-argument"}, {"client-key", "init", "--timeout=0s"}, {"client-key", "init", "--unknown-private-argument"}} {
		output, code := darwinPTYRun(test, environment, append([]string{binary}, arguments...)...)
		if code != 1 || strings.Contains(output, "private-argument") || strings.Contains(output, home) || len(output) > 4096 {
			test.Fatal("actual caller exposed raw arguments/path or lost exit/budget")
		}
		for _, relative := range []string{".config", ".local", ".agents"} {
			if _, err := os.Lstat(filepath.Join(home, relative)); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("invalid actual command mutated client state")
			}
		}
	}
}
