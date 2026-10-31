package server

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/gen/skillhub/management/v1/managementv1connect"
	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestMain(tests *testing.M) {
	if handled, code := state.SnapshotWorkerDispatch(os.Args); handled {
		os.Exit(code)
	}
	os.Exit(tests.Run())
}

func backupAppConfig(test *testing.T) Config {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	master, recovery, password := filepath.Join(directory, "master.key"), filepath.Join(directory, "recovery.key"), filepath.Join(directory, "admin.password")
	for path, contents := range map[string][]byte{master: bytes.Repeat([]byte{31}, 32), recovery: bytes.Repeat([]byte{57}, 32), password: []byte("synthetic-backup-password-71")} {
		if err := os.WriteFile(path, contents, 0600); err != nil {
			test.Fatal(err)
		}
	}
	return Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: master, BootstrapAdminUsername: "backup-admin", BootstrapAdminPasswordFile: password, BackupEnabled: true, BackupS3ConfigFile: backupSettingsFixture(test, nil), BackupRecoveryKeyFile: recovery}
}

func TestServerBackupAppDefaultAndStartupFailure(test *testing.T) {
	for _, mode := range []string{"disabled", "enabled", "same-key", "short-key", "public-key", "invalid-settings"} {
		test.Run(mode, func(test *testing.T) {
			config := backupAppConfig(test)
			switch mode {
			case "disabled":
				config.BackupEnabled, config.BackupS3ConfigFile, config.BackupRecoveryKeyFile = false, "", ""
			case "same-key":
				config.BackupRecoveryKeyFile = config.MasterKeyFile
			case "short-key":
				if err := os.WriteFile(config.BackupRecoveryKeyFile, []byte("synthetic-invalid-key"), 0600); err != nil {
					test.Fatal(err)
				}
			case "public-key":
				if err := os.Chmod(config.BackupRecoveryKeyFile, 0644); err != nil {
					test.Fatal(err)
				}
			case "invalid-settings":
				config.BackupS3ConfigFile = backupSettingsFile(test, []byte(`{"secret_access_key":"synthetic-sensitive-invalid"}`))
			}
			app, err := Open(test.Context(), config)
			if mode != "enabled" && mode != "disabled" {
				if app != nil || err == nil || strings.Contains(err.Error(), "synthetic-") || strings.Contains(err.Error(), config.DataDirectory) {
					test.Fatal("invalid private backup configuration accepted or exposed")
				}
				config.BackupEnabled, config.BackupS3ConfigFile, config.BackupRecoveryKeyFile = false, "", ""
				app, err = Open(test.Context(), config)
			}
			if err != nil {
				test.Fatal(err)
			}
			defer app.Close()
			if app.store.MaintenanceManaged() != config.BackupEnabled || (app.backups != nil) != config.BackupEnabled {
				test.Fatal("App backup and maintenance configuration diverged")
			}
			if mode == "disabled" {
				if _, err := os.Stat(filepath.Join(config.DataDirectory, "backup")); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("disabled backup created control state")
				}
			}
			if config.BackupEnabled {
				if peer, err := Open(test.Context(), config); peer != nil || err == nil {
					test.Fatal("second App bypassed managed instance ownership")
				}
			}
		})
	}
}

func TestServerBackupAppTLSControl(test *testing.T) {
	config := backupAppConfig(test)
	var mutex sync.Mutex
	objects := make(map[string][]byte)
	var putOrder []string
	var diagnostics []string
	var violation bool
	storage := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		diagnostics = append(diagnostics, fmt.Sprintf("method=%s query_options=%d valid_prefix=%t", request.Method, len(request.URL.Query()), strings.HasPrefix(request.URL.Path, "/cognate-tests/backup-tests/")))
		query := request.URL.Query()
		operation := map[string]string{http.MethodPut: "PutObject", http.MethodGet: "GetObject", http.MethodHead: "HeadObject"}[request.Method]
		if len(query) > 1 || len(query) == 1 && query.Get("x-id") != operation || !strings.HasPrefix(request.URL.Path, "/cognate-tests/backup-tests/") {
			violation = true
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		switch request.Method {
		case http.MethodPut:
			if request.Header.Get("If-None-Match") != "*" || request.Header.Get("Authorization") == "" {
				violation = true
			}
			if _, exists := objects[request.URL.Path]; exists {
				writer.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			data, err := io.ReadAll(io.LimitReader(request.Body, 16<<20+1))
			if err != nil || len(data) > 16<<20 {
				violation = true
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			objects[request.URL.Path] = data
			putOrder = append(putOrder, request.URL.Path)
			writer.Header().Set("ETag", `"opaque-synthetic"`)
		case http.MethodGet, http.MethodHead:
			data, exists := objects[request.URL.Path]
			if !exists {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			writer.Header().Set("ETag", `"opaque-synthetic"`)
			http.ServeContent(writer, request, "", time.Unix(1700000000, 0), bytes.NewReader(data))
		default:
			violation = true
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer storage.Close()
	config.BackupS3ConfigFile = backupSettingsFixture(test, func(document map[string]any) { document["endpoint"] = storage.URL })
	certificate := filepath.Join(test.TempDir(), "synthetic-ca.pem")
	if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: storage.Certificate().Raw}), 0600); err != nil {
		test.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestServerBackupAppTLSWorker$", "-test.count=1", "-test.timeout=75s")
	command.Env = append(os.Environ(), "SSL_CERT_FILE="+certificate, "SSL_CERT_DIR="+filepath.Dir(certificate), "COGNATE_BACKUP_TEST_DATA="+config.DataDirectory, "COGNATE_BACKUP_TEST_MASTER="+config.MasterKeyFile, "COGNATE_BACKUP_TEST_RECOVERY="+config.BackupRecoveryKeyFile, "COGNATE_BACKUP_TEST_SETTINGS="+config.BackupS3ConfigFile, "COGNATE_BACKUP_TEST_PASSWORD="+config.BootstrapAdminPasswordFile)
	if output, err := command.CombinedOutput(); err != nil {
		mutex.Lock()
		metadata := strings.Join(diagnostics, "; ")
		mutex.Unlock()
		test.Fatalf("fresh trust-store App probe failed: %v; %s\n%s", err, metadata, output)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if violation || len(objects) < 6 || len(objects) != len(putOrder) || !strings.Contains(putOrder[len(putOrder)-1], "/complete/") {
		test.Fatal("App did not conditionally publish bounded ciphertext with complete last")
	}
	for path, contents := range objects {
		if bytes.Contains(contents, []byte("synthetic-backup-password-71")) || bytes.Contains(contents, bytes.Repeat([]byte{31}, 32)) || bytes.Contains(contents, bytes.Repeat([]byte{57}, 32)) {
			test.Fatal("private key or administrator password reached the remote target")
		}
		if !strings.Contains(path, "/data/") && !strings.Contains(path, "/page/") && !strings.Contains(path, "/bootstrap/") && !strings.Contains(path, "/complete/") {
			test.Fatal("unexpected remote object namespace")
		}
	}
}

func backupRPCRequest[Message any](message *Message, token string) *connect.Request[Message] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+token)
	return request
}

func TestServerBackupAppTLSWorker(test *testing.T) {
	if os.Getenv("COGNATE_BACKUP_TEST_DATA") == "" {
		test.Skip("fresh process TLS fixture only")
	}
	config := Config{BackupEnabled: true, DataDirectory: os.Getenv("COGNATE_BACKUP_TEST_DATA"), MasterKeyFile: os.Getenv("COGNATE_BACKUP_TEST_MASTER"), BackupRecoveryKeyFile: os.Getenv("COGNATE_BACKUP_TEST_RECOVERY"), BackupS3ConfigFile: os.Getenv("COGNATE_BACKUP_TEST_SETTINGS"), BootstrapAdminUsername: "backup-admin", BootstrapAdminPasswordFile: os.Getenv("COGNATE_BACKUP_TEST_PASSWORD")}
	requestID := uuid.New().String()
	var jobID string
	for round := range 2 {
		app, err := Open(test.Context(), config)
		if err != nil {
			test.Fatal("managed App did not open", err)
		}
		endpoint := httptest.NewTLSServer(app.handler)
		client := managementv1connect.NewBackupManagementServiceClient(endpoint.Client(), endpoint.URL)
		authentication := authv1connect.NewAuthServiceClient(endpoint.Client(), endpoint.URL)
		login, err := authentication.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "backup-admin", Password: "synthetic-backup-password-71"}))
		if err != nil {
			test.Fatal("bootstrap administrator did not log in", err)
		}
		token := login.Msg.SessionToken
		status, err := client.GetBackupStatus(test.Context(), backupRPCRequest(&managementv1.GetBackupStatusRequest{}, token))
		if err != nil || !status.Msg.Enabled || status.Msg.Scope != "instance" || status.Msg.AutomaticEnabled || status.Msg.RestoreAvailable || status.Header().Get("Cache-Control") != "no-store" {
			test.Fatal("enabled backup status inaccurate or cacheable", err)
		}
		if round == 0 {
			registration, err := authentication.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "backup-ordinary", Password: "synthetic-ordinary-password-73"}))
			if err != nil || registration.Msg.UserId == "" {
				test.Fatal("ordinary user creation failed", err)
			}
			ordinary, err := authentication.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "backup-ordinary", Password: "synthetic-ordinary-password-73"}))
			if err != nil {
				test.Fatal(err)
			}
			checkBackupRPCBoundary(test, client, ordinary.Msg.SessionToken, token)
		}
		requestContext, release := context.WithCancel(test.Context())
		started, err := client.StartBackup(requestContext, backupRPCRequest(&managementv1.StartBackupRequest{RequestId: requestID}, token))
		release()
		if err != nil || started.Msg.Job == nil || round == 1 && started.Msg.Job.Id != jobID {
			test.Fatal("persisted request identity lost", err)
		}
		jobID = started.Msg.Job.Id
		deadline := time.Now().Add(30 * time.Second)
		for {
			current, err := client.GetBackupJob(test.Context(), backupRPCRequest(&managementv1.GetBackupJobRequest{JobId: jobID}, token))
			if err != nil {
				test.Fatal(err)
			}
			job := current.Msg.Job
			if job.Phase == "remote_committed" {
				if job.Objects < 6 || job.CiphertextBytes <= 0 || job.ErrorCode != "" {
					test.Fatal("remote commit has no ciphertext evidence")
				}
				break
			}
			if time.Now().After(deadline) || job.Phase == "interrupted" || job.Phase == "failed" || job.Phase == "commit_unknown" || job.Phase == "canceled" {
				test.Fatalf("App backup did not commit: %s/%s", job.Phase, job.ErrorCode)
			}
			time.Sleep(10 * time.Millisecond)
		}
		page, err := client.ListBackupJobs(test.Context(), backupRPCRequest(&managementv1.ListBackupJobsRequest{Limit: 1}, token))
		if err != nil || len(page.Msg.Jobs) != 1 || page.Msg.Jobs[0].Id != jobID || page.Msg.NextCursor != "" {
			test.Fatal("real App jobs not durably visible", err)
		}
		canceled, err := client.CancelBackupJob(test.Context(), backupRPCRequest(&managementv1.CancelBackupJobRequest{JobId: jobID}, token))
		if err != nil || canceled.Msg.Job.Phase != "remote_committed" || canceled.Msg.Job.CancelRequested {
			test.Fatal("cancel falsely rolled back committed objects", err)
		}
		if round == 1 {
			reconciled, err := client.ReconcileBackupJob(test.Context(), backupRPCRequest(&managementv1.ReconcileBackupJobRequest{JobId: jobID}, token))
			if err != nil || reconciled.Msg.Job.Phase != "remote_committed" {
				test.Fatal("administrator could not read-only reconcile committed objects", err)
			}
		}
		endpoint.Close()
		if err := app.Close(); err != nil {
			test.Fatal("App did not drain and release backup ownership", err)
		}
	}
}

func checkBackupRPCBoundary(test *testing.T, client managementv1connect.BackupManagementServiceClient, ordinary, administrator string) {
	test.Helper()
	calls := []func(string) error{
		func(token string) error {
			_, err := client.GetBackupStatus(test.Context(), backupRPCRequest(&managementv1.GetBackupStatusRequest{}, token))
			return err
		},
		func(token string) error {
			_, err := client.StartBackup(test.Context(), backupRPCRequest(&managementv1.StartBackupRequest{RequestId: "synthetic-private-request"}, token))
			return err
		},
		func(token string) error {
			_, err := client.GetBackupJob(test.Context(), backupRPCRequest(&managementv1.GetBackupJobRequest{JobId: "synthetic-private-job"}, token))
			return err
		},
		func(token string) error {
			_, err := client.ListBackupJobs(test.Context(), backupRPCRequest(&managementv1.ListBackupJobsRequest{Limit: 51, Cursor: "synthetic-private-cursor"}, token))
			return err
		},
		func(token string) error {
			_, err := client.CancelBackupJob(test.Context(), backupRPCRequest(&managementv1.CancelBackupJobRequest{JobId: "synthetic-private-job"}, token))
			return err
		},
		func(token string) error {
			_, err := client.ReconcileBackupJob(test.Context(), backupRPCRequest(&managementv1.ReconcileBackupJobRequest{JobId: "synthetic-private-job"}, token))
			return err
		},
	}
	for index, call := range calls {
		for _, token := range []string{"", "invalid-session", "shp1.synthetic-share", "mcp1.synthetic-token", ordinary} {
			expected := connect.CodeUnauthenticated
			if token == ordinary {
				expected = connect.CodePermissionDenied
			}
			if err := call(token); connect.CodeOf(err) != expected || strings.Contains(err.Error(), "synthetic-private") {
				test.Fatal("caller bypassed enabled backup authorization or input leaked")
			}
		}
		if index > 0 {
			if err := call(administrator); connect.CodeOf(err) != connect.CodeInvalidArgument || strings.Contains(err.Error(), "synthetic-private") {
				test.Fatal("enabled backup accepted malformed private input")
			}
		}
	}
	missing := uuid.New().String()
	_, err := client.GetBackupJob(test.Context(), backupRPCRequest(&managementv1.GetBackupJobRequest{JobId: missing}, administrator))
	if connect.CodeOf(err) != connect.CodeNotFound || strings.Contains(fmt.Sprint(err), missing) {
		test.Fatal("missing job not safely mapped")
	}
}

func TestServerBackupRuntimeRetainsUncertainOwnership(test *testing.T) {
	runtime := &serverBackup{closeError: backup.ErrControlUncertain}
	runtime.closeOnce.Do(func() {})
	app := &App{backups: runtime}
	if err := app.Close(); !errors.Is(err, backup.ErrControlUncertain) {
		test.Fatal("critical shutdown uncertainty downgraded")
	}
	if _, retained := uncertainBackupApps.LoadAndDelete(app); !retained {
		test.Fatal("uncertain App ownership became collectible")
	}
}

func TestServerBackupAppRejectsDamagedControlJournal(test *testing.T) {
	config := backupAppConfig(test)
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	if err := app.Close(); err != nil {
		test.Fatal(err)
	}
	journal := filepath.Join(config.DataDirectory, "backup", "control", "control.log")
	damaged, err := os.ReadFile(journal)
	if err != nil || len(damaged) == 0 {
		test.Fatal("control journal not durably created", err)
	}
	damaged[len(damaged)-1] ^= 1
	if err := os.WriteFile(journal, damaged, 0600); err != nil {
		test.Fatal(err)
	}
	if app, err := Open(test.Context(), config); app != nil || err == nil {
		test.Fatal("corrupted encrypted journal silently treated as a new instance")
	}
	preserved, err := os.ReadFile(journal)
	if err != nil || !bytes.Equal(preserved, damaged) {
		test.Fatal("startup overwrote damaged control state", err)
	}
}

func TestServerBackupAppUncertainStartupRetainsManagedOwnership(test *testing.T) {
	config := backupAppConfig(test)
	directory := filepath.Join(config.DataDirectory, "backup", "control")
	if err := os.MkdirAll(directory, 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "control.lock"), nil, 0600); err != nil {
		test.Fatal(err)
	}
	if app, err := Open(test.Context(), config); app != nil || !errors.Is(err, backup.ErrControlUncertain) {
		test.Fatal("incomplete control initialization did not fail uncertain")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	master := bytes.Repeat([]byte{31}, 32)
	if control, err := backup.OpenControlStore(test.Context(), root, master); control != nil || !errors.Is(err, backup.ErrBusy) {
		test.Fatal("uncertain constructor did not retain the control lock")
	}
	executable, err := os.Executable()
	if err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestServerBackupManagedOwnershipWorker$", "-test.count=1", "-test.timeout=5s")
	command.Env = append(os.Environ(), "COGNATE_BACKUP_OWNERSHIP_DATA="+config.DataDirectory)
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("uncertain startup did not retain physical managed ownership: %v\n%s", err, output)
	}
}

func TestServerBackupManagedOwnershipWorker(test *testing.T) {
	directory := os.Getenv("COGNATE_BACKUP_OWNERSHIP_DATA")
	if directory == "" {
		test.Skip("physical process-lock probe only")
	}
	peer, err := state.OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), bytes.Repeat([]byte{31}, 32), state.Options{MaintenanceManaged: true})
	if peer != nil {
		defer peer.Close()
		test.Fatal("another process acquired managed ownership during uncertain startup")
	}
	if !errors.Is(err, maintenance.ErrLock) {
		test.Fatal("peer rejected for a reason other than retained physical lock")
	}
}
