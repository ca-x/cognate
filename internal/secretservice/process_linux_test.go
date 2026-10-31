package secretservice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"flag"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

var helperBinary = flag.String("helper-binary", os.Args[0], "private test helper binary")
var shippingDirectory = flag.String("shipping-directory", "", "prebuilt shipping binaries for syscall evidence")

func findFixtureExecutable(name string, directories []string) (string, error) {
	if name != "dbus-daemon" && name != "gnome-keyring-daemon" {
		return "", os.ErrNotExist
	}
	for _, directory := range directories {
		candidate := filepath.Join(directory, name)
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

func fixtureExecutable(test *testing.T, name string) string {
	test.Helper()
	path, err := findFixtureExecutable(name, []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"})
	if err != nil {
		test.Fatalf("fixture requires %s in standard system directories", name)
	}
	return path
}

func TestFixtureExecutableLayouts(test *testing.T) {
	for _, layout := range []string{"bin", "sbin"} {
		test.Run(layout, func(test *testing.T) {
			root := test.TempDir()
			bin, sbin := filepath.Join(root, "usr", "bin"), filepath.Join(root, "usr", "sbin")
			for _, directory := range []string{bin, sbin} {
				if err := os.MkdirAll(directory, 0700); err != nil {
					test.Fatal(err)
				}
			}
			for _, name := range []string{"dbus-daemon", "gnome-keyring-daemon"} {
				expected := filepath.Join(root, "usr", layout, name)
				if err := os.WriteFile(expected, []byte{0}, 0700); err != nil {
					test.Fatal(err)
				}
				actual, err := findFixtureExecutable(name, []string{sbin, bin})
				if err != nil || actual != expected {
					test.Fatal("fixture executable layout not supported")
				}
			}
			if err := os.WriteFile(filepath.Join(root, "usr", "dbus-daemon"), []byte{0}, 0700); err != nil {
				test.Fatal(err)
			}
			if _, err := findFixtureExecutable("../dbus-daemon", []string{sbin, bin}); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("fixture executable name not closed")
			}
			if err := os.Chmod(filepath.Join(root, "usr", layout, "dbus-daemon"), 0600); err != nil {
				test.Fatal(err)
			}
			if _, err := findFixtureExecutable("dbus-daemon", []string{sbin, bin}); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("nonexecutable fixture accepted")
			}
			if err := os.Remove(filepath.Join(root, "usr", layout, "gnome-keyring-daemon")); err != nil {
				test.Fatal(err)
			}
			if _, err := findFixtureExecutable("gnome-keyring-daemon", []string{sbin, bin}); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("missing fixture dependency accepted")
			}
		})
	}
}

func TestMain(tests *testing.M) {
	if code, handled := HelperDispatch(); handled {
		os.Exit(code)
	}
	if len(os.Args) >= 3 && os.Args[1] == "--probe-child" {
		marker := ""
		if len(os.Args) == 4 {
			marker = os.Args[3]
		}
		os.Exit(childFixture(os.Args[2], marker))
	}
	os.Exit(tests.Run())
}

func childFixture(mode, marker string) int {
	if !hardenHelper() {
		return 2
	}
	input, output := os.NewFile(3, "request"), os.NewFile(4, "response")
	if input == nil || output == nil {
		return 2
	}
	defer input.Close()
	defer output.Close()
	request, err := decodeKey(input)
	if err != nil {
		return 2
	}
	if mode == "oversize" {
		var header [9]byte
		copy(header[:4], "SSR1")
		header[4] = byte(Found)
		binary.BigEndian.PutUint32(header[5:], maxSecretBytes+1)
		_, _ = output.Write(header[:])
		_, _ = output.Write(make([]byte, maxSecretBytes+1))
		return 0
	}
	if mode == "trailing" {
		_ = writeResponse(output, Result{State: Missing})
		_, _ = output.Write([]byte{'x'})
		return 0
	}
	if mode == "missing-exit-error" {
		_ = writeResponse(output, Result{State: Missing})
		return 9
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := lookup(ctx, readerOptions{Socket: request.Socket, InputBytes: 256 << 10}, request.Service, request.Account)
	defer result.Secret.Destroy()
	if writeResponse(output, result) != nil || output.Close() != nil {
		return 2
	}
	switch mode {
	case "found-exit-error":
		return 9
	case "worker-exit":
		active, never := make(chan struct{}), make(chan struct{})
		go func() { close(active); <-never }()
		<-active
		return 0
	case "found-stall":
		copy := result.Secret.Copy()
		private := !bytes.Contains([]byte(strings.Join(os.Args, "\x00")), copy) && !bytes.Contains([]byte(strings.Join(os.Environ(), "\x00")), copy)
		clear(copy)
		var limits syscall.Rlimit
		private = private && syscall.Getrlimit(syscall.RLIMIT_CORE, &limits) == nil && limits.Cur == 0 && limits.Max == 0
		dumpable, _, failure := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0)
		private = private && failure == 0 && dumpable == 0
		for _, descriptor := range []string{"1", "2"} {
			target, err := os.Readlink("/proc/self/fd/" + descriptor)
			private = private && err == nil && target == os.DevNull
		}
		witness := strconv.Itoa(os.Getpid()) + "\nprivate=" + strconv.FormatBool(private)
		if os.WriteFile(marker, []byte(witness), 0600) != nil {
			return 2
		}
		time.Sleep(30 * time.Second)
		return 0
	}
	return 2
}

func requireReaped(test *testing.T, command *exec.Cmd) {
	test.Helper()
	if command.Process == nil || command.ProcessState == nil {
		test.Fatal("wait-not-consumed=false")
	}
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(command.Process.Pid, &status, syscall.WNOHANG, nil); err != syscall.ECHILD {
		test.Fatal("child-not-reaped=false")
	}
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(command.Process.Pid), "task")); !os.IsNotExist(err) {
		test.Fatal("child-threads-still-present=false")
	}
}

func cleanupCommand(test *testing.T, command *exec.Cmd) {
	test.Helper()
	test.Cleanup(func() {
		if command.Process != nil && command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
}

func fixtureSecret(test *testing.T) (fixture, Key, []byte) {
	test.Helper()
	fixture := newFixture(test, true)
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		test.Fatal("fixture-random")
	}
	value := []byte(base64.RawStdEncoding.EncodeToString(random))
	clear(random)
	test.Cleanup(func() { clear(value) })
	fixture.create(test, "service", "account", value, false)
	return fixture, Key{Socket: fixture.options.Socket, Service: "service", Account: "account"}, value
}

func childCommand(test *testing.T, mode, marker string) *exec.Cmd {
	test.Helper()
	executable, err := os.Executable()
	if err != nil {
		test.Fatal("fixture-executable")
	}
	args := []string{"--probe-child", mode}
	if marker != "" {
		args = append(args, marker)
	}
	command := exec.Command(executable, args...)
	cleanupCommand(test, command)
	return command
}

func TestRealHelperFound(test *testing.T) {
	if *helperBinary == "" {
		test.Fatal("invalid-setup-missing-helper")
	}
	fixture := newFixture(test, true)
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		test.Fatal("fixture-random")
	}
	defer clear(value)
	fixture.create(test, "service", "account", value, false)
	command := exec.Command(*helperBinary, helperMode)
	cleanupCommand(test, command)
	result := readCommand(test.Context(), command, Key{Socket: fixture.options.Socket, Service: "service", Account: "account"})
	defer result.Secret.Destroy()
	copy := result.Secret.Copy()
	defer clear(copy)
	if result.State != Found || !bytes.Equal(copy, value) || result.AllowFileFallback() {
		test.Fatal("helper-found=false")
	}
	requireReaped(test, command)
}

func TestSelfImageFDReexec(test *testing.T) {
	_, request, value := fixtureSecret(test)
	result := Read(test.Context(), request)
	defer result.Secret.Destroy()
	copy := result.Secret.Copy()
	defer clear(copy)
	if result.State != Found || !bytes.Equal(copy, value) || result.AllowFileFallback() {
		test.Fatal("self-image-reexec=false")
	}
}

func TestRealHelperStates(test *testing.T) {
	fixture, request, _ := fixtureSecret(test)
	missing := request
	missing.Account = "account-other"
	command := exec.Command(*helperBinary, helperMode)
	cleanupCommand(test, command)
	result := readCommand(test.Context(), command, missing)
	if result.State != Missing || !result.AllowFileFallback() || result.Secret.Copy() != nil {
		test.Fatal("helper-missing=false")
	}
	requireReaped(test, command)
	var locked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if fixture.admin.Object(secretService, secretPath).CallWithContext(test.Context(), serviceInterface+".Lock", 0, []dbus.ObjectPath{fixture.collection}).Store(&locked, &prompt) != nil || prompt != "/" {
		test.Fatal("fixture-lock")
	}
	command = exec.Command(*helperBinary, helperMode)
	cleanupCommand(test, command)
	result = readCommand(test.Context(), command, request)
	if result.State != Locked || result.AllowFileFallback() || result.Secret.Copy() != nil {
		test.Fatal("helper-locked=false")
	}
	requireReaped(test, command)
	bare := newFixture(test, false)
	request.Socket = bare.options.Socket
	command = exec.Command(*helperBinary, helperMode)
	cleanupCommand(test, command)
	result = readCommand(test.Context(), command, request)
	if result.State != Unknown || result.AllowFileFallback() {
		test.Fatal("helper-unknown=false")
	}
	requireReaped(test, command)
}

func TestProcessExitReclaimsLiveWorker(test *testing.T) {
	_, request, value := fixtureSecret(test)
	command := childCommand(test, "worker-exit", "")
	result := readCommand(test.Context(), command, request)
	defer result.Secret.Destroy()
	copy := result.Secret.Copy()
	defer clear(copy)
	if result.State != Found || !bytes.Equal(copy, value) {
		test.Fatal("worker-exit-result=false")
	}
	requireReaped(test, command)
}

func TestValidReplyDoesNotBypassWaitAndCancellation(test *testing.T) {
	_, request, value := fixtureSecret(test)
	marker := filepath.Join(test.TempDir(), "ready")
	command := childCommand(test, "found-stall", marker)
	ctx, cancel := context.WithCancel(test.Context())
	completed := make(chan struct{})
	var result Result
	go func() { result = readCommand(ctx, command, request); close(completed) }()
	defer func() {
		cancel()
		select {
		case <-completed:
		case <-time.After(time.Second):
			test.Error("parent-cancel-join=false")
		}
	}()
	deadline := time.Now().Add(time.Second)
	var pid int
	for {
		encoded, err := os.ReadFile(marker)
		if err == nil {
			pidText, witness, _ := strings.Cut(string(encoded), "\n")
			pid, _ = strconv.Atoi(pidText)
			if witness != "private=true" {
				test.Fatal("child-private-witness=false")
			}
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("fixture-reply-ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid <= 0 {
		test.Fatal("fixture-ready-pid")
	}
	for _, name := range []string{"cmdline", "environ"} {
		encoded, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), name))
		if (err != nil && !os.IsPermission(err)) || bytes.Contains(encoded, value) {
			test.Fatal("process-metadata-privacy=false")
		}
	}
	for _, descriptor := range []string{"1", "2"} {
		target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "fd", descriptor))
		if (err != nil && !os.IsPermission(err)) || (err == nil && target != os.DevNull) {
			test.Fatal("stdio-not-private=false")
		}
	}
	select {
	case <-completed:
		test.Fatal("released-before-wait=false")
	case <-time.After(100 * time.Millisecond):
	}
	other := exec.Command(*helperBinary, helperMode)
	cleanupCommand(test, other)
	independent := readCommand(test.Context(), other, request)
	defer independent.Secret.Destroy()
	if independent.State != Found {
		test.Fatal("independent-process=false")
	}
	requireReaped(test, other)
	cancel()
	select {
	case <-completed:
	case <-time.After(time.Second):
		test.Fatal("cancel-termination=false")
	}
	if result.State != Unknown || result.AllowFileFallback() || result.Secret.Copy() != nil {
		test.Fatal("cancel-withheld-result=false")
	}
	requireReaped(test, command)
}

func TestHelperNonzeroExitWithholdsReply(test *testing.T) {
	_, request, _ := fixtureSecret(test)
	for _, mode := range []string{"found-exit-error", "missing-exit-error"} {
		command := childCommand(test, mode, "")
		result := readCommand(test.Context(), command, request)
		defer result.Secret.Destroy()
		if result.State != Unknown || result.AllowFileFallback() || result.Secret.Copy() != nil {
			test.Fatal("nonzero-exit-withholding=false")
		}
		requireReaped(test, command)
	}
}

func TestHelperRejectsOversizeAndTrailing(test *testing.T) {
	bare := newFixture(test, false)
	request := Key{Socket: bare.options.Socket, Service: "service", Account: "account"}
	for _, mode := range []string{"oversize", "trailing"} {
		command := childCommand(test, mode, "")
		result := readCommand(test.Context(), command, request)
		defer result.Secret.Destroy()
		if result.State != Unknown || result.AllowFileFallback() || result.Secret.Copy() != nil {
			test.Fatal("ipc-envelope-acceptance=false")
		}
		requireReaped(test, command)
	}
}

func TestCanceledKeyStartsNoHelper(test *testing.T) {
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	command := exec.Command(*helperBinary, helperMode)
	result := readCommand(ctx, command, Key{Socket: "/synthetic", Service: "service", Account: "account"})
	if result.State != Unknown || result.AllowFileFallback() || command.Process != nil {
		test.Fatal("precancel-started-helper=false")
	}
}

type blockedSecretService struct {
	entered  chan struct{}
	release  chan struct{}
	returned chan struct{}
}

func (service *blockedSecretService) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	close(service.entered)
	<-service.release
	close(service.returned)
	return nil, nil, nil
}

func TestCancellationReapsHelperDuringRealDBusRPC(test *testing.T) {
	fixture := newFixture(test, false)
	service := &blockedSecretService{entered: make(chan struct{}), release: make(chan struct{}), returned: make(chan struct{})}
	if fixture.admin.Export(service, secretPath, serviceInterface) != nil {
		test.Fatal("fixture-export")
	}
	owner, err := fixture.admin.RequestName(secretService, dbus.NameFlagDoNotQueue)
	if err != nil || owner != dbus.RequestNameReplyPrimaryOwner {
		test.Fatal("fixture-service-owner")
	}
	var releaseOnce sync.Once
	test.Cleanup(func() {
		releaseOnce.Do(func() { close(service.release) })
		select {
		case <-service.entered:
			select {
			case <-service.returned:
			case <-time.After(time.Second):
				test.Error("fixture-handler-cleanup")
			}
		default:
		}
	})
	command := exec.Command(*helperBinary, helperMode)
	cleanupCommand(test, command)
	ctx, cancel := context.WithCancel(test.Context())
	completed := make(chan struct{})
	var result Result
	go func() {
		result = readCommand(ctx, command, Key{Socket: fixture.options.Socket, Service: "service", Account: "account"})
		close(completed)
	}()
	defer func() {
		cancel()
		select {
		case <-completed:
		case <-time.After(time.Second):
			test.Error("cancel-parent-join=false")
		}
	}()
	select {
	case <-service.entered:
	case <-time.After(time.Second):
		test.Fatal("fixture-rpc-entry")
	}
	processRoot := filepath.Join("/proc", strconv.Itoa(command.Process.Pid))
	if environment, err := os.ReadFile(filepath.Join(processRoot, "environ")); !os.IsPermission(err) {
		clear(environment)
		test.Fatal("helper-dumpable=false")
	}
	limits, err := os.ReadFile(filepath.Join(processRoot, "limits"))
	coreDisabled := false
	if err == nil {
		for line := range strings.SplitSeq(string(limits), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 6 && strings.Join(fields[:4], " ") == "Max core file size" {
				coreDisabled = fields[4] == "0" && fields[5] == "0"
			}
		}
	}
	if !coreDisabled {
		test.Fatal("helper-core-limit=false")
	}
	cancel()
	select {
	case <-completed:
	case <-time.After(time.Second):
		test.Fatal("rpc-cancel-termination=false")
	}
	if result.State != Unknown || result.AllowFileFallback() || result.Secret.Copy() != nil {
		test.Fatal("rpc-cancel-result=false")
	}
	requireReaped(test, command)
}

type fixture struct {
	options    readerOptions
	admin      *dbus.Conn
	collection dbus.ObjectPath
	session    dbus.ObjectPath
}

func startProcess(test *testing.T, environment []string, input io.Reader, name string, args ...string) {
	test.Helper()
	command := exec.Command(name, args...)
	command.Env = environment
	command.Stdin = input
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if command.Start() != nil {
		test.Fatal("fixture-process-start")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	test.Cleanup(func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				test.Error("fixture-process-join")
			}
		}
	})
}

func connectTest(test *testing.T, socket string, options ...dbus.ConnOption) *dbus.Conn {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 4*time.Second)
	test.Cleanup(cancel)
	raw, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		test.Fatal("fixture-connect")
	}
	_ = raw.SetDeadline(time.Now().Add(4 * time.Second))
	connection, err := dbus.NewConn(raw, append(options, dbus.WithContext(ctx))...)
	if err != nil {
		_ = raw.Close()
		test.Fatal("fixture-new-connection")
	}
	test.Cleanup(func() { _ = raw.Close(); _ = connection.Close() })
	if connection.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Geteuid()))}) != nil {
		test.Fatal("fixture-auth-external")
	}
	if connection.Hello() != nil {
		test.Fatal("fixture-hello")
	}
	return connection
}

func newFixture(test *testing.T, withKeyring bool) fixture {
	test.Helper()
	root, err := os.MkdirTemp("", "ss-")
	if err != nil {
		test.Fatal("fixture-temp")
	}
	test.Cleanup(func() { _ = os.RemoveAll(root) })
	home := filepath.Join(root, "home")
	runtime := filepath.Join(root, "runtime")
	for _, directory := range []string{home, runtime, filepath.Join(home, "config"), filepath.Join(home, "data"), filepath.Join(home, "cache"), filepath.Join(root, "control")} {
		if os.MkdirAll(directory, 0700) != nil {
			test.Fatal("fixture-directory")
		}
	}
	socket := filepath.Join(root, "bus")
	config := `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN" "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig><type>session</type><listen>unix:path=` + socket + `</listen><auth>EXTERNAL</auth>
<policy context="default"><allow send_destination="*"/><allow receive_sender="*"/><allow own="*"/></policy>
<limit name="max_message_size">262144</limit><limit name="max_incoming_bytes">1048576</limit><limit name="max_outgoing_bytes">1048576</limit></busconfig>`
	configPath := filepath.Join(root, "bus.conf")
	if os.WriteFile(configPath, []byte(config), 0600) != nil {
		test.Fatal("fixture-config")
	}
	environment := []string{"PATH=/usr/sbin:/usr/bin:/bin", "HOME=" + home, "XDG_RUNTIME_DIR=" + runtime, "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_DATA_HOME=" + filepath.Join(home, "data"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "DBUS_SESSION_BUS_ADDRESS=unix:path=" + socket, "DBUS_SYSTEM_BUS_ADDRESS=unix:path=" + filepath.Join(root, "no-system-bus"), "SYSTEMD_BYPASS_USERDB=1", "SYSTEMD_NSS_DYNAMIC_BYPASS=1", "LANG=C", "LC_ALL=C"}
	startProcess(test, environment, nil, fixtureExecutable(test, "dbus-daemon"), "--nofork", "--nopidfile", "--config-file="+configPath)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Lstat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("fixture-broker-ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	connection := connectTest(test, socket)
	fixture := fixture{options: readerOptions{Socket: socket, InputBytes: 256 << 10}, admin: connection}
	if !withKeyring {
		return fixture
	}
	password := make([]byte, 32)
	if _, err := rand.Read(password); err != nil {
		test.Fatal("fixture-random")
	}
	input := []byte(base64.StdEncoding.EncodeToString(password))
	clear(password)
	test.Cleanup(func() { clear(input) })
	startProcess(test, environment, bytes.NewReader(input), fixtureExecutable(test, "gnome-keyring-daemon"), "--foreground", "--unlock", "--components=secrets", "--control-directory="+filepath.Join(root, "control"))
	for {
		var owner string
		if connection.BusObject().CallWithContext(test.Context(), "org.freedesktop.DBus.GetNameOwner", 0, secretService).Store(&owner) == nil {
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("fixture-keyring-ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var output dbus.Variant
	if err := connection.Object(secretService, secretPath).CallWithContext(test.Context(), serviceInterface+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&output, &fixture.session); err != nil {
		test.Fatalf("fixture-open-session class=%s error_type=%T context_done=%t", fixtureRPCErrorClass(err), err, test.Context().Err() != nil)
	}
	if connection.Object(secretService, secretPath).CallWithContext(test.Context(), serviceInterface+".ReadAlias", 0, "default").Store(&fixture.collection) != nil || fixture.collection == "/" {
		test.Fatal("fixture-read-alias")
	}
	return fixture
}

func fixtureRPCErrorClass(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if failure, ok := errors.AsType[dbus.Error](err); ok {
		switch failure.Name {
		case "org.freedesktop.DBus.Error.UnknownMethod", "org.freedesktop.DBus.Error.UnknownObject", "org.freedesktop.DBus.Error.ServiceUnknown", "org.freedesktop.DBus.Error.NameHasNoOwner", "org.freedesktop.DBus.Error.NoReply", "org.freedesktop.DBus.Error.Disconnected", "org.freedesktop.DBus.Error.AccessDenied", "org.freedesktop.DBus.Error.Failed":
			return failure.Name
		}
	}
	return "other"
}

func TestFixtureRPCErrorClassDoesNotExposeBodies(test *testing.T) {
	for _, name := range []string{"UnknownMethod", "UnknownObject", "ServiceUnknown", "NameHasNoOwner", "NoReply", "Disconnected", "AccessDenied", "Failed"} {
		test.Run(name, func(test *testing.T) {
			qualifiedName := "org.freedesktop.DBus.Error." + name
			failure := dbus.Error{Name: qualifiedName, Body: []any{"synthetic-private-error-body"}}
			if fixtureRPCErrorClass(failure) != qualifiedName || fixtureRPCErrorClass(errors.Join(errors.New("synthetic-private-wrapper"), failure)) != qualifiedName {
				test.Fatal("known D-Bus classification lost")
			}
		})
	}
	if fixtureRPCErrorClass(context.DeadlineExceeded) != "deadline" || fixtureRPCErrorClass(context.Canceled) != "canceled" {
		test.Fatal("context classification lost")
	}
	for _, failure := range []error{errors.New("synthetic-private-error"), dbus.Error{Name: "synthetic-private-error-name", Body: []any{"synthetic-private-error-body"}}, dbus.Error{Name: "org.freedesktop.DBus.Error.synthetic-private-error-name"}} {
		if fixtureRPCErrorClass(failure) != "other" {
			test.Fatal("private error body or unknown name escaped")
		}
	}
}

func (fixture fixture) create(test *testing.T, service, account string, value []byte, extra bool) dbus.ObjectPath {
	test.Helper()
	attributes := map[string]string{"service": service, "account": account}
	if extra {
		attributes["extra"] = "synthetic"
	}
	properties := map[string]dbus.Variant{itemInterface + ".Label": dbus.MakeVariant("synthetic"), itemInterface + ".Attributes": dbus.MakeVariant(attributes)}
	var item, prompt dbus.ObjectPath
	if fixture.admin.Object(secretService, fixture.collection).CallWithContext(test.Context(), "org.freedesktop.Secret.Collection.CreateItem", 0, properties, wireSecret{fixture.session, nil, value, "text/plain"}, false).Store(&item, &prompt) != nil || prompt != "/" || item == "/" {
		test.Fatal("fixture-create-item")
	}
	return item
}

func readCommand(ctx context.Context, command *exec.Cmd, key Key) Result {
	return readProcess(ctx, command, key, nil)
}

func TestShippingBinaryEarlyDispatch(test *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		test.Fatal("fixture-root")
	}
	_, request, value := fixtureSecret(test)
	for _, target := range []string{"cognate", "cognate-desktop"} {
		test.Run(target, func(test *testing.T) {
			binary := filepath.Join(test.TempDir(), target)
			if *shippingDirectory != "" {
				binary = filepath.Join(*shippingDirectory, target)
			} else {
				build := exec.CommandContext(test.Context(), "go", "build", "-o", binary, "./cmd/"+target)
				build.Dir = root
				if output, err := build.CombinedOutput(); err != nil {
					test.Fatalf("fixture-build-failed output_bytes=%d", len(output))
				}
			}
			home := test.TempDir()
			input, writer, err := os.Pipe()
			if err != nil {
				test.Fatal("fixture-input")
			}
			defer input.Close()
			frame, err := encodeKey(request)
			if err != nil {
				test.Fatal("fixture-frame")
			}
			if count, err := writer.Write(frame); err != nil || count != len(frame) {
				test.Fatal("fixture-write")
			}
			if writer.Close() != nil {
				test.Fatal("fixture-close-input")
			}
			output, response, err := os.Pipe()
			if err != nil {
				test.Fatal("fixture-output")
			}
			defer output.Close()
			defer response.Close()
			image, err := os.Open(binary)
			if err != nil {
				test.Fatal("fixture-image")
			}
			defer image.Close()
			ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/proc/self/fd/5", helperMode)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + home, "XDG_DATA_HOME=" + home, "XDG_CACHE_HOME=" + home, "XDG_RUNTIME_DIR=" + home, "TMPDIR=" + home, "DBUS_SYSTEM_BUS_ADDRESS=unix:path=" + filepath.Join(home, "no-bus"), "SYSTEMD_BYPASS_USERDB=1", "SYSTEMD_NSS_DYNAMIC_BYPASS=1", "LANG=C", "LC_ALL=C"}
			quiet, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
			if err != nil {
				test.Fatal("fixture-null")
			}
			defer quiet.Close()
			command.Stdin = quiet
			var normalOutput bytes.Buffer
			command.Stdout = &normalOutput
			command.Stderr = &normalOutput
			command.ExtraFiles = []*os.File{input, response, image}
			if command.Start() != nil {
				test.Fatal("fixture-shipping-start")
			}
			cleanupCommand(test, command)
			_ = input.Close()
			_ = response.Close()
			stop := context.AfterFunc(ctx, func() { _ = output.Close() })
			defer stop()
			result, readErr := readResponse(output)
			defer result.Secret.Destroy()
			waitErr := command.Wait()
			copied := result.Secret.Copy()
			defer clear(copied)
			if readErr != nil || waitErr != nil || ctx.Err() != nil || result.State != Found || !bytes.Equal(copied, value) {
				test.Fatalf("shipping-helper=false ipc_ok=%t exit_ok=%t ctx_ok=%t output_bytes=%d", readErr == nil, waitErr == nil, ctx.Err() == nil, normalOutput.Len())
			}
			requireReaped(test, command)
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 0 || normalOutput.Len() != 0 {
				test.Fatal("ordinary-bootstrap-side-effect=false")
			}
			invalid := exec.CommandContext(ctx, binary, helperMode, "extra")
			invalid.Env = command.Env
			invalid.Dir = home
			captured, err := invalid.CombinedOutput()
			failure, ok := errors.AsType[*exec.ExitError](err)
			if !ok || failure.ExitCode() != 2 || len(captured) != 0 {
				clear(captured)
				test.Fatal("malformed-helper-entered-bootstrap=false")
			}
			entries, err = os.ReadDir(home)
			if err != nil || len(entries) != 0 {
				test.Fatal("malformed-helper-bootstrap-write=false")
			}
		})
	}
}

func TestRealExactMissingDuplicateLocked(test *testing.T) {
	fixture := newFixture(test, true)
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		test.Fatal("fixture-random")
	}
	defer clear(value)
	for _, query := range [][2]string{{"service", "account"}, {"service-x", "account"}, {"service", "account-x"}} {
		result := lookup(test.Context(), fixture.options, query[0], query[1])
		if result.State != Missing || !result.AllowFileFallback() || result.Secret.Copy() != nil {
			test.Fatal("missing-contract=false")
		}
	}
	fixture.create(test, "service", "account", value, true)
	for _, query := range [][2]string{{"service-x", "account"}, {"service", "account-x"}, {"serv", "account"}, {"service", "acc"}} {
		result := lookup(test.Context(), fixture.options, query[0], query[1])
		if result.State != Missing {
			test.Fatal("exact-pair=false")
		}
	}
	result := lookup(test.Context(), fixture.options, "service", "account")
	if result.State != Found {
		test.Fatal("pair-with-unrelated-attribute=false")
	}
	result.Secret.Destroy()
	var locked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if fixture.admin.Object(secretService, secretPath).CallWithContext(test.Context(), serviceInterface+".Lock", 0, []dbus.ObjectPath{fixture.collection}).Store(&locked, &prompt) != nil || prompt != "/" || len(locked) != 1 {
		test.Fatal("fixture-lock")
	}
	result = lookup(test.Context(), fixture.options, "service", "account")
	if result.State != Locked || result.AllowFileFallback() || result.Secret.Copy() != nil {
		test.Fatal("locked-contract=false")
	}
	var property dbus.Variant
	if fixture.admin.Object(secretService, fixture.collection).CallWithContext(test.Context(), "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.Secret.Collection", "Locked").Store(&property) != nil || property.Value() != true {
		test.Fatal("lookup-unlocked-or-prompted=false")
	}
}

func TestRealDuplicateIsUnknown(test *testing.T) {
	fixture := newFixture(test, true)
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		test.Fatal("fixture-random")
	}
	defer clear(value)
	fixture.create(test, "duplicate", "account", value, false)
	fixture.create(test, "duplicate", "account", value, false)
	result := lookup(test.Context(), fixture.options, "duplicate", "account")
	if result.State != Unknown || result.AllowFileFallback() || result.Secret.Copy() != nil {
		test.Fatal("duplicate-contract=false")
	}
}

func TestRealWholeConnectionBudget(test *testing.T) {
	fixture := newFixture(test, false)
	ctx, cancel := context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	options := fixture.options
	options.InputBytes = 4096
	connection, wire, cleanup, ok := privateConn(ctx, options)
	if !ok {
		test.Fatal("budget-fixture")
	}
	defer cleanup()
	if wire.read.Load() == 0 {
		test.Fatal("auth-hello-not-counted=false")
	}
	exhausted := false
	for range 100 {
		if connection.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetId", dbus.FlagNoAutoStart).Err != nil {
			exhausted = true
			break
		}
	}
	if !exhausted || wire.read.Load() != 4096 || ctx.Err() != nil {
		test.Fatal("whole-connection-budget=false")
	}
	options.InputBytes = 64
	result := lookup(test.Context(), options, "service", "account")
	if result.State != Unknown || result.AllowFileFallback() {
		test.Fatal("budget-fallback=false")
	}
}

func TestPreReadBoundaryCannotOverconsume(test *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = peer.Write(make([]byte, 1024))
	}()
	defer func() {
		_ = raw.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			test.Error("budget-peer-join=false")
		}
	}()
	wire := &boundedConn{Conn: raw, remaining: 17}
	buffer := make([]byte, 4096)
	count, err := wire.Read(buffer)
	if count != 17 || err != nil || wire.read.Load() != 17 {
		test.Fatal("pre-read-boundary=false")
	}
	count, err = wire.Read(buffer)
	if count != 0 || !errors.Is(err, errInputBudget) || wire.read.Load() != 17 {
		test.Fatal("exhausted-read-touched-socket=false")
	}
}
