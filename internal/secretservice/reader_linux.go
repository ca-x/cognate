package secretservice

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

const secretService = "org.freedesktop.secrets"
const secretPath = dbus.ObjectPath("/org/freedesktop/secrets")
const serviceInterface = "org.freedesktop.Secret.Service"
const itemInterface = "org.freedesktop.Secret.Item"

var errInputBudget = errors.New("input-budget")

type wireSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

type boundedConn struct {
	net.Conn
	remaining int64
	read      atomic.Int64
	once      sync.Once
	closeErr  error
}

func (connection *boundedConn) Read(buffer []byte) (int, error) {
	if connection.remaining <= 0 {
		return 0, errInputBudget
	}
	buffer = buffer[:min(int64(len(buffer)), connection.remaining)]
	count, err := connection.Conn.Read(buffer)
	connection.remaining -= int64(count)
	connection.read.Add(int64(count))
	return count, err
}

func (connection *boundedConn) Close() error {
	connection.once.Do(func() { connection.closeErr = connection.Conn.Close() })
	return connection.closeErr
}

type noObjects struct{}

func (noObjects) LookupObject(dbus.ObjectPath) (dbus.ServerObject, bool) { return nil, false }

type noSignals struct{}

func (noSignals) DeliverSignal(string, string, *dbus.Signal) {}

func sameUID(raw net.Conn) bool {
	unix, ok := raw.(*net.UnixConn)
	if !ok {
		return false
	}
	control, err := unix.SyscallConn()
	if err != nil {
		return false
	}
	var credential *syscall.Ucred
	var credentialErr error
	if control.Control(func(descriptor uintptr) {
		credential, credentialErr = syscall.GetsockoptUcred(int(descriptor), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}) != nil {
		return false
	}
	return credentialErr == nil && credential != nil && credential.Uid == uint32(os.Geteuid())
}

func privateConn(ctx context.Context, options readerOptions) (*dbus.Conn, *boundedConn, func(), bool) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "unix", options.Socket)
	if err != nil {
		return nil, nil, nil, false
	}
	if !sameUID(raw) {
		_ = raw.Close()
		return nil, nil, nil, false
	}
	deadline, _ := ctx.Deadline()
	if raw.SetDeadline(deadline) != nil {
		_ = raw.Close()
		return nil, nil, nil, false
	}
	bounded := &boundedConn{Conn: raw, remaining: options.InputBytes}
	connection, err := dbus.NewConn(bounded, dbus.WithContext(ctx), dbus.WithHandler(noObjects{}), dbus.WithSignalHandler(noSignals{}))
	if err != nil {
		_ = bounded.Close()
		return nil, nil, nil, false
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = bounded.Close(); close(finished) })
	cleanup := func() {
		_ = bounded.Close()
		if !stop() {
			<-finished
		}
		_ = connection.Close()
	}
	if connection.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Geteuid()))}) != nil || connection.Hello() != nil {
		cleanup()
		return nil, nil, nil, false
	}
	return connection, bounded, cleanup, true
}

func readProperty(ctx context.Context, connection *dbus.Conn, owner string, path dbus.ObjectPath, name string) (dbus.Variant, bool) {
	var value dbus.Variant
	err := connection.Object(owner, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", dbus.FlagNoAutoStart, itemInterface, name).Store(&value)
	return value, err == nil
}

func ownerName(ctx context.Context, connection *dbus.Conn) (string, bool) {
	var owner string
	err := connection.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", dbus.FlagNoAutoStart, secretService).Store(&owner)
	return owner, err == nil && len(owner) > 0 && owner[0] == ':'
}

type readerOptions struct {
	Socket     string
	InputBytes int64
}

func lookup(parent context.Context, options readerOptions, service, account string) (result Result) {
	if parent == nil || parent.Err() != nil || !filepath.IsAbs(options.Socket) || options.InputBytes <= 0 || options.InputBytes > 256<<10 || len(service) == 0 || len(service) > 256 || len(account) == 0 || len(account) > 256 {
		return Result{}
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	connection, _, cleanup, ok := privateConn(ctx, options)
	if !ok {
		return Result{}
	}
	defer func() {
		cleanup()
		if ctx.Err() != nil {
			result.Secret.Destroy()
			result = Result{}
		}
	}()
	owner, ok := ownerName(ctx, connection)
	if !ok {
		return Result{}
	}
	defer func() {
		current, ok := ownerName(ctx, connection)
		if !ok || current != owner {
			result.Secret.Destroy()
			result = Result{}
		}
	}()
	var ownerUID uint32
	if connection.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixUser", dbus.FlagNoAutoStart, owner).Store(&ownerUID) != nil || ownerUID != uint32(os.Geteuid()) {
		return Result{}
	}
	object := connection.Object(owner, secretPath)
	var unlocked, locked []dbus.ObjectPath
	if object.CallWithContext(ctx, serviceInterface+".SearchItems", dbus.FlagNoAutoStart, map[string]string{"service": service, "account": account}).Store(&unlocked, &locked) != nil {
		return Result{}
	}
	if len(unlocked)+len(locked) == 0 {
		return Result{State: Missing}
	}
	if len(unlocked)+len(locked) != 1 {
		return Result{}
	}
	paths := append(unlocked, locked...)
	path := paths[0]
	if !path.IsValid() || path == "/" {
		return Result{}
	}
	attributes, ok := readProperty(ctx, connection, owner, path, "Attributes")
	if !ok {
		return Result{}
	}
	fields, ok := attributes.Value().(map[string]string)
	if !ok || fields["service"] != service || fields["account"] != account {
		return Result{}
	}
	lockedValue, ok := readProperty(ctx, connection, owner, path, "Locked")
	if !ok {
		return Result{}
	}
	isLocked, ok := lockedValue.Value().(bool)
	if !ok {
		return Result{}
	}
	if len(locked) > 0 || isLocked {
		return Result{State: Locked}
	}
	var output dbus.Variant
	var session dbus.ObjectPath
	if object.CallWithContext(ctx, serviceInterface+".OpenSession", dbus.FlagNoAutoStart, "plain", dbus.MakeVariant("")).Store(&output, &session) != nil || !session.IsValid() || session == "/" {
		return Result{}
	}
	defer func() {
		if connection.Object(owner, session).CallWithContext(ctx, "org.freedesktop.Secret.Session.Close", dbus.FlagNoAutoStart).Err != nil {
			result.Secret.Destroy()
			result = Result{}
		}
	}()
	var secret wireSecret
	call := connection.Object(owner, path).CallWithContext(ctx, itemInterface+".GetSecret", dbus.FlagNoAutoStart, session)
	if call.Store(&secret) != nil {
		if failure, ok := errors.AsType[dbus.Error](call.Err); ok && failure.Name == "org.freedesktop.Secret.Error.IsLocked" {
			return Result{State: Locked}
		}
		return Result{}
	}
	defer clear(secret.Value)
	if secret.Session != session || len(secret.Parameters) != 0 || len(secret.Value) > 64<<10 {
		return Result{}
	}
	value := append([]byte(nil), secret.Value...)
	return Result{State: Found, Secret: Secret{payload: &secretPayload{data: &value}}}
}

var _ io.ReadWriteCloser = (*boundedConn)(nil)
