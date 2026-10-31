package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

func authPlatform() error { return nil }

func authTermios(file *os.File, change func(int) error) error {
	connection, err := file.SyscallConn()
	if err != nil {
		return errAuthRun
	}
	var operation error
	if connection.Control(func(descriptor uintptr) { operation = change(int(descriptor)) }) != nil || operation != nil {
		return errAuthRun
	}
	return nil
}

func authCredentials(caller context.Context, origin string, private *authPrivate) (result error) {
	ctx, cancel := context.WithTimeout(caller, time.Minute)
	defer cancel()
	file, err := os.OpenFile("/dev/tty", os.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errAuthRun
	}
	var before *unix.Termios
	defer func() {
		if before != nil {
			if authTermios(file, func(descriptor int) error {
				restore := unix.IoctlSetTermios(descriptor, unix.TCSETS, before)
				flush := unix.IoctlSetInt(descriptor, unix.TCFLSH, unix.TCIFLUSH)
				return errors.Join(restore, flush)
			}) != nil {
				result = errAuthRun
			}
		}
		if file.Close() != nil {
			result = errAuthRun
		}
	}()
	if authTermios(file, func(descriptor int) error {
		var err error
		before, err = unix.IoctlGetTermios(descriptor, unix.TCGETS)
		if err != nil {
			return errAuthRun
		}
		foreground, err := unix.IoctlGetInt(descriptor, unix.TIOCGPGRP)
		if err != nil || foreground != unix.Getpgrp() || before.Lflag&unix.ICANON == 0 {
			return errAuthRun
		}
		hidden := *before
		hidden.Lflag &^= unix.ECHO | unix.ECHONL
		return unix.IoctlSetTermios(descriptor, unix.TCSETS, &hidden)
	}) != nil {
		return errAuthRun
	}
	terminal := &nativeFile{file: file, ctx: ctx}
	if authTTYWrite(terminal, "Local account at "+origin+"\nCurrent authenticated session only.\nUsername: ") != nil {
		return errAuthRun
	}
	username, err := authTTYLine(terminal, 64)
	if err != nil {
		return errAuthRun
	}
	private.material().username = username
	if len(username) < 3 {
		return errAuthRun
	}
	for _, character := range username {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-') {
			return errAuthRun
		}
	}
	if authTTYWrite(terminal, "\nPassword: ") != nil {
		return errAuthRun
	}
	password, err := authTTYLine(terminal, 1024)
	if err != nil {
		return errAuthRun
	}
	private.material().password = password
	if len(password) < 8 || !utf8.Valid(password) {
		return errAuthRun
	}
	if authTTYWrite(terminal, "\n") != nil {
		return errAuthRun
	}
	return ctx.Err()
}

func authTTYWrite(terminal *nativeFile, value string) error {
	count, err := terminal.Write([]byte(value))
	if err != nil || count != len(value) {
		return errAuthRun
	}
	return nil
}

func authTTYLine(terminal *nativeFile, limit int) ([]byte, error) {
	buffer := make([]byte, 0, limit)
	var single [1]byte
	defer clear(single[:])
	for len(buffer) <= limit {
		count, err := terminal.Read(single[:])
		if err != nil || count != 1 {
			clear(buffer)
			return nil, errAuthRun
		}
		if single[0] == '\n' {
			return buffer, nil
		}
		if single[0] < 32 || single[0] == 127 {
			clear(buffer)
			return nil, errAuthRun
		}
		buffer = append(buffer, single[0])
	}
	clear(buffer)
	return nil, errAuthRun
}

func authChild(ctx context.Context, arguments []string, token []byte, output, diagnostic io.Writer) error {
	if ctx.Err() != nil || !authValidToken(string(token)) {
		return errAuthRun
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return errAuthRun
	}
	defer reader.Close()
	if writer.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
		_ = writer.Close()
		return errAuthRun
	}
	payload := append(append([]byte(nil), token...), '\n')
	count, writeErr := writer.Write(payload)
	clear(payload)
	closeErr := writer.Close()
	if writeErr != nil || count != len(token)+1 || closeErr != nil || ctx.Err() != nil {
		return errAuthRun
	}
	command := exec.Command("/proc/self/exe", arguments...)
	command.Stdin, command.Stdout, command.Stderr = reader, output, diagnostic
	command.Env = authEnvironment()
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if command.Start() != nil {
		return errAuthRun
	}
	readerClose := reader.Close()
	done := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() { done <- command.Wait() })
	defer workers.Wait()
	select {
	case err := <-done:
		if err != nil || readerClose != nil {
			return errAuthChild
		}
		return ctx.Err()
	case <-ctx.Done():
		_ = command.Process.Signal(os.Interrupt)
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			_ = command.Process.Kill()
			<-done
		}
		return errAuthChild
	}
}

func authEnvironment() []string {
	environment := []string{"PATH=/usr/bin:/bin"}
	for _, name := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(name); ok && !strings.ContainsRune(value, '\x00') {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}
