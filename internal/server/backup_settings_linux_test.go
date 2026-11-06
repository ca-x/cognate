package server

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/gen/skillhub/management/v1/managementv1connect"
	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/types/descriptorpb"
)

func backupSettingsInput(config Config) *managementv1.UpdateBackupSettingsRequest {
	return &managementv1.UpdateBackupSettingsRequest{ExpectedRevision: 1, CredentialsAction: "replace", AccessKeyId: "synthetic-hot-access", SecretAccessKey: "synthetic-hot-secret", SessionToken: "synthetic-hot-token", Settings: &managementv1.BackupSettings{Enabled: true, Endpoint: "https://s3.example.invalid", Region: "test-1", Bucket: "cognate-tests", Prefix: "hot-backup", RecoveryKeyId: "00000000-0000-4000-8000-000000000001", RecoveryKeyFile: config.BackupRecoveryKeyFile}}
}

func TestServerBackupSettingsHTTPHotApplyPersistence(test *testing.T) {
	config := backupAppConfig(test)
	input := backupSettingsInput(config)
	config.BackupEnabled, config.BackupS3ConfigFile, config.BackupRecoveryKeyFile = false, "", ""
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	if !app.store.MaintenanceManaged() || app.backupSettings == nil || app.backupSettings.Enabled() {
		test.Fatal("initially disabled installation cannot hot enable")
	}
	endpoint := httptest.NewTLSServer(app.runtimeHandler())
	defer endpoint.Close()
	client := managementv1connect.NewBackupManagementServiceClient(endpoint.Client(), endpoint.URL)
	identity := authv1connect.NewAuthServiceClient(endpoint.Client(), endpoint.URL)
	login, err := identity.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: config.BootstrapAdminUsername, Password: "synthetic-backup-password-71"}))
	if err != nil {
		test.Fatal(err)
	}
	token := login.Msg.SessionToken
	if _, err := identity.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "settings-ordinary", Password: "synthetic-ordinary-password-73"})); err != nil {
		test.Fatal(err)
	}
	ordinary, err := identity.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "settings-ordinary", Password: "synthetic-ordinary-password-73"}))
	if err != nil {
		test.Fatal(err)
	}
	for _, caller := range []struct {
		token string
		code  connect.Code
	}{{"", connect.CodeUnauthenticated}, {ordinary.Msg.SessionToken, connect.CodePermissionDenied}} {
		_, getError := client.GetBackupSettings(test.Context(), backupRPCRequest(&managementv1.GetBackupSettingsRequest{}, caller.token))
		_, updateError := client.UpdateBackupSettings(test.Context(), backupRPCRequest(input, caller.token))
		if connect.CodeOf(getError) != caller.code || connect.CodeOf(updateError) != caller.code {
			test.Fatal("settings authorization did not precede state access", getError, updateError)
		}
	}
	initial, err := client.GetBackupSettings(test.Context(), backupRPCRequest(&managementv1.GetBackupSettingsRequest{}, token))
	if err != nil || initial.Msg.Settings.Revision != 1 || initial.Msg.Settings.Enabled || initial.Header().Get("Cache-Control") != "no-store" {
		test.Fatal("initial private settings inaccurate", err)
	}
	store := app.store
	updated, err := client.UpdateBackupSettings(test.Context(), backupRPCRequest(input, token))
	if err != nil || !updated.Msg.Settings.Enabled || updated.Msg.Settings.Revision != 2 || !updated.Msg.Settings.CredentialsConfigured || updated.Msg.Settings.MaxDatabaseBytes != 256<<20 || updated.Header().Get("Cache-Control") != "no-store" {
		test.Fatal("hot enabling did not apply", err)
	}
	if app.store != store || !app.backupSettings.Enabled() {
		test.Fatal("settings update replaced the application instead of hot applying")
	}
	if strings.Contains(updated.Msg.String(), "synthetic-hot-") || strings.Contains(fmt.Sprintf("%+v", input), "synthetic-hot-") {
		test.Fatal("credentials not redacted")
	}
	encoded, err := json.Marshal(input)
	if err != nil || bytes.Contains(encoded, []byte("synthetic-hot-")) {
		test.Fatal("ordinary JSON formatting disclosed write-only credentials", err)
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("settings update", "request", input, "manager", app.backupSettings)
	if strings.Contains(log.String(), "synthetic-hot-") {
		test.Fatal("structured logging disclosed write-only credentials")
	}
	for _, name := range []string{"access_key_id", "secret_access_key", "session_token"} {
		field := input.ProtoReflect().Descriptor().Fields().ByTextName(name)
		if !field.Options().(*descriptorpb.FieldOptions).GetDebugRedact() {
			test.Fatal("write-only credential missing debug_redact option")
		}
	}
	if _, err := client.UpdateBackupSettings(test.Context(), backupRPCRequest(input, token)); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("stale revision accepted", err)
	}
	input.ExpectedRevision, input.Settings, input.CredentialsAction = updated.Msg.Settings.Revision, updated.Msg.Settings, "keep"
	input.AccessKeyId, input.SecretAccessKey, input.SessionToken = "", "", ""
	previous := app.backupSettings.current
	input.Settings.Prefix, input.Settings.AutomaticIntervalSeconds = "hot-swapped", 60
	updated, err = client.UpdateBackupSettings(test.Context(), backupRPCRequest(input, token))
	if err != nil || app.backupSettings.current == previous || app.backupSettings.record.Settings.SecretAccessKey != "synthetic-hot-secret" || !app.backupSettings.AutomaticEnabled() {
		test.Fatal("target and schedule were not really hot replaced", err)
	}
	status, err := client.GetBackupStatus(test.Context(), backupRPCRequest(&managementv1.GetBackupStatusRequest{}, token))
	if err != nil || !status.Msg.Enabled || !status.Msg.AutomaticEnabled {
		test.Fatal("new runtime not visible through same client", err)
	}
	for _, enabled := range []bool{false, true} {
		input.ExpectedRevision, input.Settings = updated.Msg.Settings.Revision, updated.Msg.Settings
		input.Settings.Enabled = enabled
		updated, err = client.UpdateBackupSettings(test.Context(), backupRPCRequest(input, token))
		if err != nil || app.backupSettings.Enabled() != enabled || app.backupSettings.AutomaticEnabled() != enabled {
			test.Fatal("hot disable/re-enable did not apply", err)
		}
	}
	path := filepath.Join(config.DataDirectory, "backup", "settings", "settings.enc")
	sealed, err := os.ReadFile(path)
	if err != nil || bytes.Contains(sealed, []byte("synthetic-hot-")) || bytes.Contains(sealed, []byte("hot-swapped")) {
		test.Fatal("settings persisted as plaintext", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		test.Fatal("persisted settings permissions invalid", err)
	}
	if err := app.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal("encrypted settings not reloaded without CLI files", err)
	}
	defer reopened.Close()
	settings, err := reopened.backupSettings.GetBackupSettings(test.Context())
	if err != nil || settings.Revision != updated.Msg.Settings.Revision || settings.Prefix != "hot-swapped" || !settings.Enabled || !settings.CredentialsConfigured || !reopened.backupSettings.AutomaticEnabled() {
		test.Fatal("saved hot settings not recovered", err)
	}
}

func TestServerBackupSettingsRejectInvalidWithoutMutation(test *testing.T) {
	config := backupAppConfig(test)
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	original := app.backupSettings.current
	for name, change := range map[string]func(*managementv1.UpdateBackupSettingsRequest){
		"endpoint-user": func(request *managementv1.UpdateBackupSettingsRequest) {
			request.Settings.Endpoint = "https://private@s3.example.invalid"
		},
		"endpoint-query": func(request *managementv1.UpdateBackupSettingsRequest) {
			request.Settings.Endpoint += "?secret=private"
		},
		"endpoint-http": func(request *managementv1.UpdateBackupSettingsRequest) {
			request.Settings.Endpoint = "http://s3.example.invalid"
		},
		"same-key": func(request *managementv1.UpdateBackupSettingsRequest) {
			request.Settings.RecoveryKeyFile = config.MasterKeyFile
		},
		"missing-key":     func(request *managementv1.UpdateBackupSettingsRequest) { request.Settings.RecoveryKeyFile += ".absent" },
		"empty-secret":    func(request *managementv1.UpdateBackupSettingsRequest) { request.SecretAccessKey = "" },
		"implicit-action": func(request *managementv1.UpdateBackupSettingsRequest) { request.CredentialsAction = "" },
		"keep-with-input": func(request *managementv1.UpdateBackupSettingsRequest) { request.CredentialsAction = "keep" },
		"interval": func(request *managementv1.UpdateBackupSettingsRequest) {
			request.Settings.AutomaticIntervalSeconds = 59
		},
	} {
		test.Run(name, func(test *testing.T) {
			request := backupSettingsInput(config)
			change(request)
			if _, err := app.backupSettings.UpdateBackupSettings(test.Context(), request); !errors.Is(err, backup.ErrInvalid) {
				test.Fatal("invalid settings accepted", err)
			}
			if app.backupSettings.current != original || app.backupSettings.record.Revision != 1 {
				test.Fatal("rejected update changed runtime")
			}
		})
	}
	key, err := os.ReadFile(config.BackupRecoveryKeyFile)
	if err != nil || !bytes.Equal(key, bytes.Repeat([]byte{57}, 32)) {
		test.Fatal("recovery key was generated over existing file", err)
	}
}

func TestServerBackupSettingsAutomaticHotPolicy(test *testing.T) {
	config := backupAppConfig(test)
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	manager := app.backupSettings
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	if err := manager.StartAutomatic(ctx); err != nil {
		test.Fatal(err)
	}
	request := backupSettingsInput(config)
	for _, seconds := range []int64{60, 0, 60, 0} {
		request.Settings.AutomaticIntervalSeconds = seconds
		settings, err := manager.UpdateBackupSettings(test.Context(), request)
		if err != nil || manager.AutomaticEnabled() != (seconds != 0) {
			test.Fatal("schedule could not be disabled/re-enabled in same lifetime", err)
		}
		request.ExpectedRevision = settings.Revision
	}
	manager.automaticStep(test.Context(), manager.record.Revision-1)
	manager.automaticStep(test.Context(), manager.record.Revision)
	page, err := manager.List(test.Context(), 20, "")
	if err != nil || len(page.Jobs) != 0 {
		test.Fatal("disabled or stale schedule registered work", err)
	}
	stop, stopCancel := context.WithTimeout(test.Context(), time.Second)
	defer stopCancel()
	if err := manager.StopAutomatic(stop); err != nil {
		test.Fatal(err)
	}
}

func TestServerBackupSettingsPersistenceFailureRollsBack(test *testing.T) {
	config := backupAppConfig(test)
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	manager := app.backupSettings
	original := manager.current
	if err := manager.root.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := manager.UpdateBackupSettings(test.Context(), backupSettingsInput(config)); !errors.Is(err, backup.ErrUnavailable) {
		test.Fatal("failed persistence reported success", err)
	}
	if !manager.Enabled() || manager.current == original || manager.record.Revision != 1 || manager.record.Settings.Prefix != "backup-tests" {
		test.Fatal("failed settings write did not restore prior runtime")
	}
	manager.root, err = state.OpenPrivateDirectory(filepath.Join(config.DataDirectory, "backup", "settings"))
	if err != nil {
		test.Fatal(err)
	}
}

func TestServerBackupSettingsRejectsActiveJob(test *testing.T) {
	config := backupAppConfig(test)
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := app.store.WithMaintenanceWriter(test.Context(), func(context.Context) (bool, error) {
			close(entered)
			<-release
			return true, nil
		})
		done <- err
	}()
	defer close(release)
	<-entered
	manager := app.backupSettings
	original := manager.current
	job, err := manager.Start(test.Context(), manager.config.AdminUserIDs[0], uuid.New().String())
	if err != nil {
		test.Fatal(err)
	}
	if _, err := manager.UpdateBackupSettings(test.Context(), backupSettingsInput(config)); !errors.Is(err, backup.ErrBusy) {
		test.Fatal("active job did not block configuration update", err)
	}
	if manager.current != original || manager.record.Revision != 1 {
		test.Fatal("busy update mutated live runtime")
	}
	if _, err := manager.Cancel(test.Context(), manager.config.AdminUserIDs[0], job.ID); err != nil {
		test.Fatal(err)
	}
}

func TestServerBackupSettingsRejectsDamagedPersistence(test *testing.T) {
	for _, damage := range []string{"ciphertext", "permissions", "symlink", "hardlink"} {
		test.Run(damage, func(test *testing.T) {
			config := backupAppConfig(test)
			app, err := Open(test.Context(), config)
			if err != nil {
				test.Fatal(err)
			}
			if _, err := app.backupSettings.UpdateBackupSettings(test.Context(), backupSettingsInput(config)); err != nil {
				test.Fatal(err)
			}
			if err := app.Close(); err != nil {
				test.Fatal(err)
			}
			path := filepath.Join(config.DataDirectory, "backup", "settings", "settings.enc")
			data, err := os.ReadFile(path)
			if err != nil {
				test.Fatal(err)
			}
			switch damage {
			case "ciphertext":
				data[len(data)-1] ^= 1
				err = os.WriteFile(path, data, 0600)
			case "permissions":
				err = os.Chmod(path, 0644)
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					test.Fatal(err)
				}
				err = os.Symlink(path+".original", path)
			case "hardlink":
				err = os.Link(path, path+".linked")
			}
			if err != nil {
				test.Fatal(err)
			}
			if reopened, err := Open(test.Context(), config); reopened != nil || err == nil {
				if reopened != nil {
					_ = reopened.Close()
				}
				test.Fatal("damaged persisted settings accepted")
			}
			preserved, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(preserved, data) {
				test.Fatal("startup replaced damaged settings", err)
			}
		})
	}
}

func TestServerBackupSettingsDisableWithoutRecoveryFile(test *testing.T) {
	config := backupAppConfig(test)
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	settings, err := app.backupSettings.GetBackupSettings(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	if err := os.Remove(config.BackupRecoveryKeyFile); err != nil {
		test.Fatal(err)
	}
	settings.Enabled = false
	if _, err := app.backupSettings.UpdateBackupSettings(test.Context(), &managementv1.UpdateBackupSettingsRequest{ExpectedRevision: settings.Revision, Settings: settings, CredentialsAction: "keep"}); err != nil {
		test.Fatal("safe disabling unnecessarily required recovery key material", err)
	}
	if app.backupSettings.Enabled() {
		test.Fatal("disabled runtime still serving")
	}
}

func TestServerBackupSettingsConcurrentRevisionCAS(test *testing.T) {
	config := backupAppConfig(test)
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := app.backupSettings.UpdateBackupSettings(test.Context(), backupSettingsInput(config))
			results <- err
		}()
	}
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, backup.ErrControlConflict):
			conflicts++
		default:
			test.Fatal("unexpected concurrent update result", err)
		}
	}
	if successes != 1 || conflicts != 1 || app.backupSettings.record.Revision != 2 {
		test.Fatal("concurrent update lost the revision compare-and-swap")
	}
}

func TestServerBackupSettingsDisabledAppSingleOwner(test *testing.T) {
	config := backupAppConfig(test)
	config.BackupEnabled, config.BackupS3ConfigFile, config.BackupRecoveryKeyFile = false, "", ""
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	if duplicate, err := Open(test.Context(), config); duplicate != nil || !errors.Is(err, ErrBackupSettingsOwned) {
		if duplicate != nil {
			_ = duplicate.Close()
		}
		test.Fatal("same-process Apps bypassed settings revision ownership", err)
	}
	if _, err := app.backupSettings.GetBackupSettings(test.Context()); err != nil {
		test.Fatal("rejected duplicate damaged the original owner", err)
	}
	before, err := os.Stat(config.MasterKeyFile)
	if err != nil {
		test.Fatal(err)
	}
	if err := app.Close(); err != nil {
		test.Fatal(err)
	}
	probe, cancel := context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	lease, err := runtimegen.LockOffline(probe, config.DataDirectory)
	if err != nil {
		test.Fatal("rejected duplicate retained a shared runtime lease", err)
	}
	if err := lease.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal("closed first owner prevented a replacement App", err)
	}
	defer reopened.Close()
	after, err := os.Stat(config.MasterKeyFile)
	if err != nil || !os.SameFile(before, after) {
		test.Fatal("replacement App changed the original key", err)
	}
	key, err := os.ReadFile(config.MasterKeyFile)
	defer clear(key)
	if err != nil || !bytes.Equal(key, bytes.Repeat([]byte{31}, 32)) {
		test.Fatal("replacement App changed master key material", err)
	}
}

func TestServerBackupSettingsUncertainPublicationQuarantines(test *testing.T) {
	config := backupAppConfig(test)
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	manager := app.backupSettings
	if err := manager.root.Mkdir("settings.enc", 0700); err != nil {
		test.Fatal(err)
	}
	defer func() {
		manager.uncertain = false
		uncertainServerBackups.Delete(manager)
	}()
	if _, err := manager.UpdateBackupSettings(test.Context(), backupSettingsInput(config)); !errors.Is(err, backup.ErrControlUncertain) {
		test.Fatal("uncertain publication reported success", err)
	}
	retained := manager.current
	if manager.Enabled() || manager.record.Revision != 1 || retained == nil {
		test.Fatal("uncertain settings update released or reused its capability")
	}
	if _, err := manager.GetBackupSettings(test.Context()); !errors.Is(err, backup.ErrControlUncertain) {
		test.Fatal("uncertain settings claimed an applied revision", err)
	}
	if _, err := manager.UpdateBackupSettings(test.Context(), backupSettingsInput(config)); !errors.Is(err, backup.ErrControlUncertain) || manager.current != retained {
		test.Fatal("uncertain settings update was retried", err)
	}
	if err := manager.Close(test.Context()); !errors.Is(err, backup.ErrControlUncertain) || manager.current != retained || len(manager.master) != 32 {
		test.Fatal("uncertain lifetime was not retained", err)
	}
}
