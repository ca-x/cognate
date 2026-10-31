package secretservice

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fixtureBrokerReady(ctx context.Context, socket string, expectedPID int, announcement *os.File) bool {
	if ctx.Err() != nil || expectedPID <= 0 {
		return false
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = announcement.Close()
		close(closed)
	})
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	line, err := bufio.NewReader(io.LimitReader(announcement, 32)).ReadString('\n')
	if err != nil || line != strconv.Itoa(expectedPID)+"\n" || ctx.Err() != nil {
		return false
	}
	info, err := os.Lstat(socket)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func TestFixtureBrokerReadinessRejectsBoundSocket(test *testing.T) {
	root, err := os.MkdirTemp("", "ss-ready-")
	if err != nil {
		test.Fatal("fixture-temp")
	}
	test.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := filepath.Join(root, "bus")
	descriptor, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		test.Fatal("fixture-bound-socket")
	}
	defer syscall.Close(descriptor)
	if syscall.Bind(descriptor, &syscall.SockaddrUnix{Name: socket}) != nil {
		test.Fatal("fixture-bind")
	}
	info, err := os.Lstat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		test.Fatal("bound-socket-not-visible")
	}
	connection, err := (&net.Dialer{}).DialContext(test.Context(), "unix", socket)
	if connection != nil {
		_ = connection.Close()
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		test.Fatal("bound-socket-unexpected-connect-state")
	}
	readiness, writer, err := os.Pipe()
	if err != nil {
		test.Fatal("fixture-pipe")
	}
	defer readiness.Close()
	if writer.Close() != nil {
		test.Fatal("fixture-pipe-close")
	}
	if fixtureBrokerReady(test.Context(), socket, os.Getpid(), readiness) {
		test.Fatal("bound-socket-was-mistaken-for-broker-readiness")
	}
}

func TestFixtureBrokerReadinessFrames(test *testing.T) {
	root, err := os.MkdirTemp("", "ss-ready-")
	if err != nil {
		test.Fatal("fixture-temp")
	}
	test.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := filepath.Join(root, "bus")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		test.Fatal("fixture-listener")
	}
	defer listener.Close()
	for _, scenario := range []struct {
		name  string
		frame string
		ready bool
	}{
		{name: "announced", frame: "42\n", ready: true},
		{name: "unannounced"},
		{name: "wrong-pid", frame: "43\n"},
		{name: "incomplete", frame: "42"},
		{name: "oversize", frame: strings.Repeat("4", 64) + "\n"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			readiness, writer, err := os.Pipe()
			if err != nil {
				test.Fatal("fixture-pipe")
			}
			defer readiness.Close()
			if _, err := io.WriteString(writer, scenario.frame); err != nil {
				_ = writer.Close()
				test.Fatal("fixture-announcement")
			}
			if writer.Close() != nil {
				test.Fatal("fixture-pipe-close")
			}
			if fixtureBrokerReady(test.Context(), socket, 42, readiness) != scenario.ready {
				test.Fatal("broker-readiness-frame-mismatch")
			}
		})
	}
}

func TestFixtureBrokerReadinessCancellation(test *testing.T) {
	readiness, writer, err := os.Pipe()
	if err != nil {
		test.Fatal("fixture-pipe")
	}
	defer readiness.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	completed := make(chan bool, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		completed <- fixtureBrokerReady(ctx, "", 42, readiness)
	}()
	<-started
	cancel()
	select {
	case ready := <-completed:
		if ready {
			test.Fatal("canceled-broker-became-ready")
		}
	case <-time.After(time.Second):
		test.Fatal("broker-readiness-cancellation-blocked")
	}
}
