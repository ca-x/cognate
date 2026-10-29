//go:build linux

package nativeconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/nativeconsumer"
	"github.com/ca-x/cognate/internal/server/state"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

func TestNativeConfigurationBindingsAndTTL(test *testing.T) {
	client, request, bearer, _, calls := realFixture(test, configuration.APIKey)
	calls.shortPreparation.Store(true)
	request.Apply = true
	interaction := nativeTestApproval()
	interaction.Approve = func(ctx context.Context, view configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot) (Approval, error) {
		if !view.ExpiresAt.Equal(time.UnixMilli(calls.preparationExpiry.Load())) {
			test.Fatal("server preparation deadline not bound to plan")
		}
		timer := time.NewTimer(time.Until(view.ExpiresAt) + 10*time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return Approval{}, ctx.Err()
		}
		return Approval{Confirmed: true, ApprovedSlots: slots}, nil
	}
	_, err := client.Apply(test.Context(), request, bearer, interaction)
	if !errors.Is(err, configapply.ErrPlanExpired) || calls.authorize.Load() != 0 || calls.consume.Load() != 0 {
		test.Fatal("approval after authoritative expiry authorized material")
	}
}

func TestNativeConfigurationCloseFailureIsNotSuccess(test *testing.T) {
	home := test.TempDir()
	stateRoot := filepath.Join(home, "state")
	if err := os.Mkdir(stateRoot, 0700); err != nil {
		test.Fatal(err)
	}
	client, err := New(home, stateRoot, "", bytes.Repeat([]byte{33}, 32), nil)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	faulted := false
	err = client.withEngine(test.Context(), func(engine *configapply.Engine) error {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := filepath.Join("/proc/self/fd", entry.Name())
			path, err := os.Readlink(name)
			if err != nil || path != filepath.Join(stateRoot, "native-configuration-v1") {
				continue
			}
			descriptor, err := strconv.Atoi(entry.Name())
			if err != nil {
				return err
			}
			if err := unix.Close(descriptor); err != nil {
				return err
			}
			faulted = true
			return nil
		}
		return errors.New("missing actual vault descriptor")
	})
	if !faulted || !errors.Is(err, configapply.ErrOutcomeUnknown) {
		test.Fatal("real engine Close failure acknowledged success")
	}
}

func TestNativeConfigurationCounterBoundaries(test *testing.T) {
	for _, current := range []configapply.SelectionState{
		{Exists: true, SelectionRevision: math.MaxUint64, ConfirmationRevision: 1, DeviceID: "fixed"},
		{Exists: true, SelectionRevision: 1, ConfirmationRevision: math.MaxUint64, DeviceID: "fixed"},
	} {
		if next, err := nextSelection(current); !errors.Is(err, ErrInvalid) || next != (configapply.SelectionState{}) {
			test.Fatal("counter overflow accepted/reset")
		}
	}
	current := configapply.SelectionState{Exists: true, SelectionRevision: 7, ConfirmationRevision: 9, PolicyRevision: 13, AutoApply: true, DeviceID: "fixed"}
	next, err := nextSelection(current)
	if err != nil || next.SelectionRevision != 8 || next.ConfirmationRevision != 10 || next.PolicyRevision != 13 || next.DeviceID != "fixed" || next.AutoApply {
		test.Fatal("existing metadata counter transition changed authority")
	}
}

func TestNativeConfigurationPresenceAndPreview(test *testing.T) {
	for _, mode := range []configuration.AuthMode{configuration.NativeLogin, configuration.APIKey, configuration.AuthToken} {
		test.Run(string(mode), func(test *testing.T) {
			client, request, bearer, home, calls := realFixture(test, mode)
			result, err := client.Apply(test.Context(), request, bearer, Interaction{})
			if err != nil || result.Preview.OperationID == "" || calls.authorize.Load() != 0 || calls.consume.Load() != 0 {
				test.Fatalf("preview did not remain unapproved: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("preview touched target")
			}
		})
	}
}

func TestNativeConfigurationExplicitApprovalAndRestore(test *testing.T) {
	for _, mode := range []configuration.AuthMode{configuration.NativeLogin, configuration.APIKey, configuration.AuthToken} {
		test.Run(string(mode), func(test *testing.T) {
			client, request, bearer, home, calls := realFixture(test, mode)
			request.Apply = true
			interaction := Interaction{Approve: func(ctx context.Context, view configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot) (Approval, error) {
				return Approval{Confirmed: true, ApprovedSlots: slots}, nil
			}}
			result, err := client.Apply(test.Context(), request, bearer, interaction)
			if err != nil || result.Local.Status != "AppliedOnDisk" || calls.authorize.Load() != 1 {
				test.Fatalf("real apply failed: %v", err)
			}
			expected := int32(1)
			if mode == configuration.NativeLogin {
				expected = 0
			}
			if calls.consume.Load() != expected {
				test.Fatal("wrong secret consume count")
			}
			data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
			if err != nil || mode != configuration.NativeLogin && !bytes.Contains(data, []byte("synthetic-C1-secret")) {
				test.Fatal("actual settings publication missing")
			}
			restored, err := client.Restore(test.Context(), result.Local.OperationID, true, interaction)
			if err != nil || restored.Local.Status != "Restored" {
				test.Fatalf("preview-confirm-restore failed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("restore did not recover absence")
			}
		})
	}
}

func TestNativeConfigurationRefusalAndClose(test *testing.T) {
	client, request, bearer, home, calls := realFixture(test, configuration.APIKey)
	request.Apply = true
	_, err := client.Apply(test.Context(), request, bearer, Interaction{})
	if err == nil || calls.authorize.Load() != 0 || calls.consume.Load() != 0 {
		test.Fatal("missing approval consumed material")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("refusal wrote target")
	}
	if err := client.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := client.Apply(test.Context(), request, bearer, Interaction{}); !errors.Is(err, ErrClosed) {
		test.Fatal("closed client accepted operation")
	}
}

func TestNativeConfigurationApprovalRefusal(test *testing.T) {
	for _, mode := range []configuration.AuthMode{configuration.NativeLogin, configuration.APIKey, configuration.AuthToken} {
		test.Run(string(mode), func(test *testing.T) {
			client, request, bearer, home, calls := realFixture(test, mode)
			request.Apply = true
			interaction := Interaction{Approve: func(ctx context.Context, view configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot) (Approval, error) {
				return Approval{Confirmed: false, ApprovedSlots: slots}, nil
			}}
			if _, err := client.Apply(test.Context(), request, bearer, interaction); !errors.Is(err, ErrDenied) {
				test.Fatal("explicit refusal was ignored")
			}
			if calls.authorize.Load() != 0 || calls.consume.Load() != 0 {
				test.Fatal("refusal authorized or consumed")
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("refusal wrote settings")
			}
		})
	}
}

type rpcCounts struct {
	ownerAuth                            *auth.Service
	domain                               *configuration.Service
	cancelAfterConsume                   atomic.Pointer[context.CancelFunc]
	committedBeforeCancel                atomic.Bool
	lockRoot                             atomic.Pointer[string]
	lockBlocked                          atomic.Bool
	authorize, consume, status, requests atomic.Int32
	loseAuthorize, loseConsume           atomic.Bool
	shortPreparation                     atomic.Bool
	preparationExpiry                    atomic.Int64
}

func realFixture(test *testing.T, mode configuration.AuthMode) (*Client, ApplyRequest, string, string, *rpcCounts) {
	test.Helper()
	master := bytes.Repeat([]byte{91}, 32)
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "server.db"), master)
	if err != nil {
		test.Fatal(err)
	}
	authentication, err := auth.NewService(store, master)
	if err != nil {
		test.Fatal(err)
	}
	token, owner, err := authentication.LoginOIDC(test.Context(), "https://synthetic.identity", "C1-owner", "", "")
	if err != nil {
		test.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "https://" + server.Listener.Addr().String()
	service, err := nativeconsumer.New(store, authentication, master, origin)
	if err != nil {
		test.Fatal(err)
	}
	counts := new(rpcCounts)
	counts.ownerAuth, counts.domain = authentication, configuration.New(store)
	handler := httpapi.NewWithOptions(authentication, httpapi.Options{PublicOrigin: origin, NativeConfiguration: service})
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		counts.requests.Add(1)
		if root := counts.lockRoot.Load(); root != nil {
			ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeConfigurationLockChild$")
			command.Env = append(os.Environ(), "COGNATE_NATIVE_LOCK_ROOT="+*root)
			if err := command.Run(); err != nil {
				counts.lockBlocked.Store(true)
				cancel()
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			cancel()
		}
		if strings.HasSuffix(request.URL.Path, "/PrepareNativeApply") && counts.shortPreparation.Load() {
			capture := nativeResponseCapture{ResponseRecorder: httptest.NewRecorder(), actual: writer}
			handler.ServeHTTP(capture, request)
			response := new(nativev1.PrepareNativeApplyResponse)
			if err := proto.Unmarshal(capture.Body.Bytes(), response); err != nil || response.Binding == nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			response.Binding.PrepareExpiresAtUnixMs = time.Now().Add(400 * time.Millisecond).UnixMilli()
			counts.preparationExpiry.Store(response.Binding.PrepareExpiresAtUnixMs)
			data, err := proto.Marshal(response)
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			for name, values := range capture.Header() {
				writer.Header()[name] = values
			}
			writer.Header().Del("Content-Length")
			writer.WriteHeader(capture.Code)
			_, _ = writer.Write(data)
			return
		}
		lose := false
		if strings.HasSuffix(request.URL.Path, "/AuthorizeNativeMaterialization") {
			counts.authorize.Add(1)
			lose = counts.loseAuthorize.Load()
		}
		if strings.HasSuffix(request.URL.Path, "/ConsumeNativeMaterialization") {
			counts.consume.Add(1)
			if cancel := counts.cancelAfterConsume.Load(); cancel != nil {
				capture := nativeResponseCapture{ResponseRecorder: httptest.NewRecorder(), actual: writer}
				handler.ServeHTTP(capture, request)
				counts.committedBeforeCancel.Store(capture.Code == http.StatusOK)
				(*cancel)()
				for name, values := range capture.Header() {
					writer.Header()[name] = values
				}
				writer.WriteHeader(capture.Code)
				_, _ = writer.Write(capture.Body.Bytes())
				clear(capture.Body.Bytes())
				return
			}
			lose = counts.loseConsume.Load()
		}
		if strings.HasSuffix(request.URL.Path, "/GetNativeMaterializationStatus") {
			counts.status.Add(1)
		}
		if lose {
			handler.ServeHTTP(nativeTruncatedResponse{writer}, request)
			return
		}
		handler.ServeHTTP(writer, request)
	})
	server.StartTLS()
	if err := service.Start(test.Context()); err != nil {
		test.Fatal(err)
	}
	native, err := configclient.NewNativeClient(origin, server.Client().Transport.(*http.Transport))
	if err != nil {
		test.Fatal(err)
	}
	var mutations []configuration.SecretMutation
	if mode != configuration.NativeLogin {
		slot := configuration.APIKeySlot
		if mode == configuration.AuthToken {
			slot = configuration.AuthTokenSlot
		}
		mutations = []configuration.SecretMutation{{Slot: slot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-C1-secret"))}}
	}
	asset, err := configuration.New(store).Create(test.Context(), owner, configuration.Draft{Name: "C1 fixture", Provider: configuration.ClaudeProvider{AuthMode: mode}}, mutations)
	if err != nil {
		test.Fatal(err)
	}
	home := test.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "skillhub"), 0700); err != nil {
		test.Fatal(err)
	}
	client, err := New(home, filepath.Join(home, ".local", "share", "skillhub"), origin, bytes.Repeat([]byte{37}, 32), native)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = client.Close(); native.Close(); service.Close(); server.Close(); _ = store.Close() })
	return client, ApplyRequest{OwnerID: owner, AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: "default"}, token.Reveal(), home, counts
}

type nativeTruncatedResponse struct{ http.ResponseWriter }

type nativeResponseCapture struct {
	*httptest.ResponseRecorder
	actual http.ResponseWriter
}

func (writer nativeResponseCapture) Unwrap() http.ResponseWriter { return writer.actual }

func (writer nativeTruncatedResponse) Unwrap() http.ResponseWriter { return writer.ResponseWriter }
func (writer nativeTruncatedResponse) Write(data []byte) (int, error) {
	_, err := writer.ResponseWriter.Write(data[:len(data)/2])
	return len(data), err
}

func TestNativeConfigurationRemoteUnknown(test *testing.T) {
	for _, stage := range []string{"authorize", "consume"} {
		test.Run(stage, func(test *testing.T) {
			client, request, bearer, home, calls := realFixture(test, configuration.APIKey)
			request.Apply = true
			if stage == "authorize" {
				calls.loseAuthorize.Store(true)
			} else {
				calls.loseConsume.Store(true)
			}
			result, err := client.Apply(test.Context(), request, bearer, nativeTestApproval())
			if !errors.Is(err, configclient.ErrNativeUnknown) || !result.RemoteUnknown || calls.authorize.Load() != 1 || calls.status.Load() != 1 {
				test.Fatal("uncertain COMMIT was retried or status absent")
			}
			if stage == "consume" && (calls.consume.Load() != 1 || result.RemoteState != configclient.NativeConsumed) {
				test.Fatal("actual consumed COMMIT not observed")
			}
			if stage == "authorize" && (calls.consume.Load() != 0 || result.RemoteState != configclient.NativeIssued) {
				test.Fatal("unknown authorization was consumed")
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("unknown delivery published settings")
			}
		})
	}
}

func TestNativeConfigurationRevocationAndBindings(test *testing.T) {
	for _, scenario := range []string{"rotation", "logout", "owner", "origin", "wrong-slot"} {
		test.Run(scenario, func(test *testing.T) {
			client, request, bearer, home, calls := realFixture(test, configuration.APIKey)
			request.Apply = true
			if scenario == "owner" {
				request.OwnerID = "another-owner"
			}
			if scenario == "origin" {
				other, err := New(home, client.private().stateRoot, "https://another.invalid", client.private().key, client.private().native)
				if err != nil {
					test.Fatal(err)
				}
				defer other.Close()
				client = other
			}
			interaction := nativeTestApproval()
			interaction.Approve = func(ctx context.Context, view configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot) (Approval, error) {
				if scenario == "rotation" {
					_, err := calls.domain.Update(ctx, request.OwnerID, request.AssetID, request.AssetRevision,
						configuration.Draft{Name: "rotated", Provider: configuration.ClaudeProvider{AuthMode: configuration.APIKey}},
						[]configuration.SecretMutation{{Slot: configuration.APIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-rotated"))}})
					if err != nil {
						test.Fatal(err)
					}
				}
				if scenario == "logout" {
					if err := calls.ownerAuth.Logout(ctx, bearer); err != nil {
						test.Fatal(err)
					}
				}
				if scenario == "wrong-slot" {
					slots = []agentconfig.ClaudeSecretSlot{agentconfig.ClaudeAuthTokenSecret}
				}
				return Approval{Confirmed: true, ApprovedSlots: slots}, nil
			}
			if _, err := client.Apply(test.Context(), request, bearer, interaction); err == nil {
				test.Fatal("changed authority or binding was accepted")
			}
			if calls.consume.Load() != 0 || calls.authorize.Load() > 1 {
				test.Fatal("stale/revoked/bad binding consumed or retried")
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("rejected authority wrote target")
			}
		})
	}
}

func TestNativeConfigurationCancelAfterCommit(test *testing.T) {
	client, request, bearer, home, calls := realFixture(test, configuration.APIKey)
	request.Apply = true
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	calls.cancelAfterConsume.Store(new(cancel))
	result, err := client.Apply(ctx, request, bearer, nativeTestApproval())
	if !errors.Is(err, configclient.ErrNativeUnknown) || !result.RemoteUnknown || !calls.committedBeforeCancel.Load() || calls.authorize.Load() != 1 || calls.consume.Load() != 1 || calls.status.Load() != 0 {
		test.Fatal("canceled actual COMMIT retried or detached status; lost unknown fact")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("cancel after consumption published settings")
	}
}

func nativeTestApproval() Interaction {
	return Interaction{Approve: func(ctx context.Context, view configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot) (Approval, error) {
		return Approval{Confirmed: true, ApprovedSlots: slots}, nil
	}}
}

func TestNativeConfigurationLocksAndCAS(test *testing.T) {
	client, request, bearer, home, calls := realFixture(test, configuration.APIKey)
	calls.lockRoot.Store(new(client.private().stateRoot))
	ctx, cancel := context.WithTimeout(test.Context(), 4*time.Second)
	defer cancel()
	request.Apply = true
	interaction := nativeTestApproval()
	interaction.Approve = func(ctx context.Context, view configapply.SafePlan, slots []agentconfig.ClaudeSecretSlot) (Approval, error) {
		childCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		command := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestNativeConfigurationLockChild$")
		command.Env = append(os.Environ(), "COGNATE_NATIVE_LOCK_ROOT="+client.private().stateRoot)
		if output, err := command.CombinedOutput(); err != nil {
			test.Fatalf("confirmation held installation lock: %v; %s", err, output)
		}
		if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"env":{"LOCAL":"preserve"}}`), 0600); err != nil {
			test.Fatal(err)
		}
		return Approval{Confirmed: true, ApprovedSlots: slots}, nil
	}
	result, err := client.Apply(ctx, request, bearer, interaction)
	if !errors.Is(err, configapply.ErrConflict) || calls.authorize.Load() != 1 || calls.consume.Load() != 1 || result.RemoteState != configclient.NativeConsumed || calls.lockBlocked.Load() {
		test.Fatal("CAS did not preserve actual remote consumption fact")
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || string(data) != `{"env":{"LOCAL":"preserve"}}` {
		test.Fatal("concurrent local edit overwritten")
	}
}

func TestNativeConfigurationLockChild(test *testing.T) {
	root := os.Getenv("COGNATE_NATIVE_LOCK_ROOT")
	if root == "" {
		return
	}
	session, err := install.OpenSession(test.Context(), root)
	if err != nil {
		test.Fatal(err)
	}
	if err := session.Close(); err != nil {
		test.Fatal(err)
	}
}

func TestNativeConfigurationLifecycleAndPrivacy(test *testing.T) {
	client, request, bearer, _, calls := realFixture(test, configuration.NativeLogin)
	for _, value := range []any{client, *client, struct{ hidden Client }{*client}, struct{ hidden any }{client}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%#080.64s"} {
			output := fmt.Sprintf(verb, value)
			if strings.Contains(output, client.private().home) || strings.Contains(output, client.private().origin) || strings.Contains(output, "[37 37") {
				test.Fatal("private client carrier exposed state/key")
			}
		}
		var buffer bytes.Buffer
		slog.New(slog.NewTextHandler(&buffer, nil)).Info("test", "client", value)
		if strings.Contains(buffer.String(), client.private().home) || strings.Contains(buffer.String(), bearer) {
			test.Fatal("slog leaked client")
		}
	}
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := client.Apply(test.Context(), request, bearer, Interaction{Preview: func(ctx context.Context, view configapply.SafePlan) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}})
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		test.Fatal("preview callback not reached")
	}
	if _, err := client.Apply(test.Context(), request, bearer, Interaction{}); !errors.Is(err, ErrBusy) {
		test.Fatal("concurrent operation admitted")
	}
	if err := client.Close(); err != nil {
		test.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			test.Fatal("Close failed to cancel current callback")
		}
	case <-time.After(time.Second):
		test.Fatal("Close left worker")
	}
	if calls.authorize.Load() != 0 || calls.consume.Load() != 0 {
		test.Fatal("cancel consumed material")
	}
}

func TestNativeConfigurationPreserveExtrasAndCounters(test *testing.T) {
	client, request, bearer, home, _ := realFixture(test, configuration.APIKey)
	base := []byte(`{"unrelated":{"large":9007199254740993},"env":{"CUSTOM":"keep-native-extra","ANTHROPIC_AUTH_TOKEN":"synthetic-old-token"}}`)
	target := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(target, base, 0644); err != nil {
		test.Fatal(err)
	}
	request.Apply = true
	result, err := client.Apply(test.Context(), request, bearer, nativeTestApproval())
	if err != nil {
		test.Fatal(err)
	}
	data, err := os.ReadFile(target)
	info, statErr := os.Stat(target)
	if err != nil || statErr != nil || info.Mode().Perm() != 0600 || !bytes.Contains(data, []byte("keep-native-extra")) || !bytes.Contains(data, []byte("9007199254740993")) || bytes.Contains(data, []byte("synthetic-old-token")) {
		test.Fatal("native extras/permissions/secret switching were lost")
	}
	read := func() configapply.SelectionState {
		var selection configapply.SelectionState
		if err := client.withEngine(test.Context(), func(engine *configapply.Engine) error {
			var err error
			selection, err = engine.CurrentSelection(test.Context(), client.private().origin, request.OwnerID, request.ProfileID)
			return err
		}); err != nil {
			test.Fatal(err)
		}
		return selection
	}
	first := read()
	if first.SelectionRevision != 1 || first.ConfirmationRevision != 1 || first.PolicyRevision != 0 || !first.Exists || first.DeviceID == "" {
		test.Fatal("first selection counters/device invalid")
	}
	if err := filepath.WalkDir(client.private().stateRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(data, []byte("synthetic-C1-secret")) || bytes.Contains(data, []byte("synthetic-old-token")) {
				test.Fatal("plaintext material/preimage persisted outside encrypted vault")
			}
		}
		return nil
	}); err != nil {
		test.Fatal(err)
	}
	if _, err := client.Restore(test.Context(), result.Local.OperationID, true, nativeTestApproval()); err != nil {
		test.Fatal(err)
	}
	data, err = os.ReadFile(target)
	if err != nil || !bytes.Equal(data, base) {
		test.Fatal("restore did not preserve original exact bytes")
	}
	paused := read()
	if paused.DeviceID != first.DeviceID || paused.AutoApply || paused.PolicyRevision <= first.PolicyRevision {
		test.Fatal("restore lost device/policy pause")
	}
	if _, err := client.Apply(test.Context(), request, bearer, nativeTestApproval()); err != nil {
		test.Fatal(err)
	}
	next := read()
	if next.DeviceID != paused.DeviceID || next.SelectionRevision != paused.SelectionRevision+1 || next.ConfirmationRevision != paused.ConfirmationRevision+1 || next.PolicyRevision != paused.PolicyRevision {
		test.Fatal("next apply reset restored selection/counters")
	}
}

func TestNativeConfigurationDamagedInventoryStopsNetwork(test *testing.T) {
	client, request, bearer, _, calls := realFixture(test, configuration.NativeLogin)
	if _, err := client.Apply(test.Context(), request, bearer, Interaction{}); err != nil {
		test.Fatal(err)
	}
	before := calls.requests.Load()
	wrong, err := New(client.private().home, client.private().stateRoot, client.private().origin, bytes.Repeat([]byte{29}, 32), client.private().native)
	if err != nil {
		test.Fatal(err)
	}
	defer wrong.Close()
	if _, err := wrong.Apply(test.Context(), request, bearer, Interaction{}); err == nil || calls.requests.Load() != before {
		test.Fatal("wrong key treated as absent or contacted server")
	}
}
