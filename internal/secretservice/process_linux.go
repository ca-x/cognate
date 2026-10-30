package secretservice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

func Read(ctx context.Context, request Key) Result {
	if ctx == nil || ctx.Err() != nil {
		return Result{}
	}
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		return Result{}
	}
	defer image.Close()
	command := exec.Command("/proc/self/fd/5", helperMode)
	return readProcess(ctx, command, request, image)
}

func readProcess(ctx context.Context, command *exec.Cmd, request Key, image *os.File) Result {
	if ctx == nil || ctx.Err() != nil || command == nil {
		return Result{}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	frame, err := encodeKey(request)
	if err != nil {
		return Result{}
	}
	defer clear(frame)
	home, err := os.MkdirTemp("", "helper-home-")
	if err != nil {
		return Result{}
	}
	defer os.RemoveAll(home)
	requestReader, requestWriter, err := os.Pipe()
	if err != nil {
		return Result{}
	}
	defer requestReader.Close()
	defer requestWriter.Close()
	responseReader, responseWriter, err := os.Pipe()
	if err != nil {
		return Result{}
	}
	defer responseReader.Close()
	defer responseWriter.Close()
	if count, err := requestWriter.Write(frame); err != nil || count != len(frame) {
		return Result{}
	}
	if requestWriter.Close() != nil {
		return Result{}
	}
	quiet, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return Result{}
	}
	defer quiet.Close()
	command.Stdin = quiet
	command.Stdout = quiet
	command.Stderr = quiet
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + home, "XDG_DATA_HOME=" + home, "XDG_CACHE_HOME=" + home, "XDG_RUNTIME_DIR=" + home, "TMPDIR=" + home, "DBUS_SYSTEM_BUS_ADDRESS=unix:path=" + filepath.Join(home, "no-system-bus"), "SYSTEMD_BYPASS_USERDB=1", "SYSTEMD_NSS_DYNAMIC_BYPASS=1", "LANG=C", "LC_ALL=C"}
	command.ExtraFiles = []*os.File{requestReader, responseWriter}
	if image != nil {
		command.ExtraFiles = append(command.ExtraFiles, image)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if ctx.Err() != nil || command.Start() != nil {
		return Result{}
	}
	_ = requestReader.Close()
	_ = responseWriter.Close()
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = command.Process.Kill()
		_ = responseReader.Close()
		close(interrupted)
	})
	result, readErr := readResponse(responseReader)
	if readErr != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if !stop() {
		<-interrupted
	}
	if readErr != nil || waitErr != nil || ctx.Err() != nil {
		result.Secret.Destroy()
		return Result{}
	}
	return result
}

func hardenHelper() bool {
	if syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{Cur: 0, Max: 0}) != nil {
		return false
	}
	_, _, failure := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0)
	return failure == 0
}

func serveProcess() int {
	if len(os.Args) != 2 || os.Args[1] != helperMode || !hardenHelper() {
		return 2
	}
	if os.Args[0] == "/proc/self/fd/5" {
		_ = syscall.Close(5)
	}
	timer := time.AfterFunc(2500*time.Millisecond, func() { os.Exit(3) })
	defer timer.Stop()
	input := os.NewFile(3, "private-request")
	output := os.NewFile(4, "private-response")
	if input == nil || output == nil {
		return 2
	}
	defer input.Close()
	defer output.Close()
	request, err := decodeKey(input)
	if err != nil {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := lookup(ctx, readerOptions{Socket: request.Socket, InputBytes: 256 << 10}, request.Service, request.Account)
	defer result.Secret.Destroy()
	if writeResponse(output, result) != nil {
		return 2
	}
	return 0
}
