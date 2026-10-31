package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var errAuthRun = errors.New("auth run refused or authentication unconfirmed; check route, origin, private terminal and account; lost Login may leave a session until expiry; no automatic retry")
var errAuthCleanup = errors.New("auth run session cleanup unconfirmed; business result unchanged; do not replay")
var errAuthChild = errors.New("auth run child failed or outcome unknown; business result unchanged; do not replay")

type authRedacted struct{}

func (authRedacted) String() string   { return "[private auth run]" }
func (authRedacted) GoString() string { return "[private auth run]" }
func (authRedacted) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private auth run]")
}
func (authRedacted) MarshalJSON() ([]byte, error) { return []byte(`"[private auth run]"`), nil }
func (authRedacted) MarshalText() ([]byte, error) { return []byte("[private auth run]"), nil }
func (authRedacted) LogValue() slog.Value         { return slog.StringValue("[private auth run]") }

type authMaterial struct {
	authRedacted
	username, password, token []byte
	owner                     string
	expires                   time.Time
	buffers                   [][]byte
}

type authPrivate struct {
	authRedacted
	material func() *authMaterial
}

func newAuthPrivate() *authPrivate {
	material := &authMaterial{}
	return &authPrivate{material: func() *authMaterial { return material }}
}

func (private *authPrivate) clear() {
	material := private.material()
	clear(material.username)
	clear(material.password)
	clear(material.token)
	for _, buffer := range material.buffers {
		clear(buffer)
	}
	*material = authMaterial{}
}

func authInvocation(arguments []string) bool { return len(arguments) > 0 && arguments[0] == "auth" }

func authExitDiagnostic(err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	output, prepareErr := prepareNativeOutput(ctx, os.Stderr)
	if prepareErr != nil {
		return
	}
	_, _ = fmt.Fprintln(output, err.Error())
	_ = output.Close()
}

func newAuthCommand() *cobra.Command {
	command := &cobra.Command{Use: "auth run --server ORIGIN -- OWNER-COMMAND", Short: "Run one owner command with a private local-account session (Linux)", DisableFlagParsing: true, SilenceUsage: true, SilenceErrors: true}
	command.RunE = func(command *cobra.Command, arguments []string) error {
		origin, route, native, err := authArguments(arguments)
		if err != nil {
			return errAuthRun
		}
		if err := authPlatform(); err != nil {
			return errAuthRun
		}
		private := newAuthPrivate()
		defer private.clear()
		if err := authCredentials(command.Context(), origin, private); err != nil {
			return errAuthRun
		}
		client := newAuthTransport(origin, private)
		defer client.close()
		return authExecute(command.Context(), client, private, route, native, command.OutOrStdout(), command.ErrOrStderr())
	}
	return command
}

func authArguments(arguments []string) (string, []string, bool, error) {
	if len(arguments) < 6 || len(arguments) > 69 || arguments[0] != "run" || arguments[1] != "--server" || arguments[3] != "--" {
		return "", nil, false, errAuthRun
	}
	origin, err := authOrigin(arguments[2])
	if err != nil {
		return "", nil, false, errAuthRun
	}
	route := arguments[4:]
	if len(route) > 64 {
		return "", nil, false, errAuthRun
	}
	size := 0
	for _, argument := range route {
		size += len(argument)
		if size > 32<<10 || strings.ContainsAny(argument, "\x00\r\n") {
			return "", nil, false, errAuthRun
		}
		flag, _, _ := strings.Cut(argument, "=")
		if flag == "--server" || flag == "--token-stdin" || flag == "--owner" || argument == "--" {
			return "", nil, false, errAuthRun
		}
	}
	valid, native := false, false
	switch route[0] {
	case "grok-assets":
		switch route[1] {
		case "list", "get", "read", "create", "update", "copy", "delete":
			valid = true
		case "order":
			valid = len(route) >= 3 && (route[2] == "get" || route[2] == "replace")
		}
	case "mcp-assets":
		switch route[1] {
		case "list", "get", "read", "preview", "create", "update", "copy", "delete":
			valid = true
		}
	case "pack":
		valid = len(route) >= 3 && route[1] == "configurations" && (route[2] == "list" || route[2] == "replace")
	case "import":
		valid = route[1] == "select" || route[1] == "status"
	case "config":
		if route[1] == "apply" {
			count := 0
			for index := 2; index < len(route); index++ {
				flag, value, equals := strings.Cut(route[index], "=")
				if flag != "--target" {
					continue
				}
				if !equals {
					index++
					if index >= len(route) {
						return "", nil, false, errAuthRun
					}
					value = route[index]
				}
				if value != "claude.settings" && value != "codex.config" {
					return "", nil, false, errAuthRun
				}
				count++
			}
			valid, native = count == 1, true
		}
	}
	if !valid {
		return "", nil, false, errAuthRun
	}
	extraCount, extraBytes := 3, len("--server")+len(origin)+len("--token-stdin")
	if native {
		extraCount += 2
		extraBytes += len("--owner") + 512
	}
	if len(route)+extraCount > 64 || size+extraBytes+len(route)+extraCount > 32<<10 {
		return "", nil, false, errAuthRun
	}
	return origin, append([]string(nil), route...), native, nil
}

func authExecute(ctx context.Context, client *authTransport, private *authPrivate, route []string, native bool, output, diagnostic io.Writer) (result error) {
	defer func() {
		if len(private.material().token) == 0 {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if client.logout(cleanup) != nil {
			result = errAuthCleanup
		}
	}()
	if client.login(ctx) != nil || client.identify(ctx) != nil {
		return errAuthRun
	}
	material := private.material()
	arguments := append(append([]string(nil), route...), "--server", client.origin, "--token-stdin")
	if native {
		arguments = append(arguments, "--owner", material.owner)
	}
	deadline := min(time.Now().Add(5*time.Minute).UnixMilli(), material.expires.UnixMilli())
	childCtx, cancel := context.WithDeadline(ctx, time.UnixMilli(deadline))
	defer cancel()
	if authChild(childCtx, arguments, material.token, output, diagnostic) != nil {
		return errAuthChild
	}
	return nil
}
