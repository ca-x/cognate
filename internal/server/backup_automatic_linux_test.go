package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/gen/skillhub/management/v1/managementv1connect"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestServerBackupAutomaticStatus(test *testing.T) {
	config := backupAppConfig(test)
	config.BackupS3ConfigFile = backupSettingsFixture(test, func(document map[string]any) { document["automatic_interval_seconds"] = 60 })
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	endpoint := httptest.NewTLSServer(app.runtimeHandler())
	defer endpoint.Close()
	identity := authv1connect.NewAuthServiceClient(endpoint.Client(), endpoint.URL)
	session, err := identity.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: config.BootstrapAdminUsername, Password: "synthetic-backup-password-71"}))
	if err != nil {
		test.Fatal(err)
	}
	client := managementv1connect.NewBackupManagementServiceClient(endpoint.Client(), endpoint.URL)
	request := connect.NewRequest(&managementv1.GetBackupStatusRequest{})
	request.Header().Set("Authorization", "Bearer "+session.Msg.SessionToken)
	status, err := client.GetBackupStatus(test.Context(), request)
	if err != nil || !status.Msg.Enabled || !status.Msg.AutomaticEnabled || status.Msg.RestoreAvailable {
		test.Fatal("configured automatic policy is not visible through real management status", err)
	}
}

type automaticArchiveTransport struct {
	base       http.RoundTripper
	archive    http.RoundTripper
	archiveURL *url.URL
}

func (transport automaticArchiveTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != "codeload.github.com" {
		return transport.base.RoundTrip(request)
	}
	cloned := request.Clone(request.Context())
	cloned.URL = transport.archiveURL.Clone()
	cloned.Host = transport.archiveURL.Host
	return transport.archive.RoundTrip(cloned)
}

func automaticArchive(test *testing.T) []byte {
	test.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for _, file := range []struct {
		name string
		data []byte
	}{
		{"source-main/SKILL.md", []byte("---\nname: Automatic\ndescription: Synthetic automatic backup fixture\n---\nautomatic-private-content")},
		{"source-main/resources/binary.dat", []byte{0, 255, 128, 0, 1, 127, 254}},
	} {
		if err := archive.WriteHeader(&tar.Header{Name: file.name, Mode: 0600, Size: int64(len(file.data))}); err != nil {
			test.Fatal(err)
		}
		if _, err := archive.Write(file.data); err != nil {
			test.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	return output.Bytes()
}

func TestServerBackupAutomaticServeTLS(test *testing.T) {
	fixture := &backupBrowserFixture{objects: make(map[string][]byte)}
	storage := httptest.NewTLSServer(http.HandlerFunc(fixture.storage))
	defer storage.Close()
	certificate := filepath.Join(test.TempDir(), "synthetic-ca.pem")
	if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: storage.Certificate().Raw}), 0600); err != nil {
		test.Fatal(err)
	}
	test.Setenv("SSL_CERT_FILE", certificate)
	test.Setenv("SSL_CERT_DIR", filepath.Dir(certificate))
	archiveBytes := automaticArchive(test)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/gzip")
		_, _ = writer.Write(archiveBytes)
	}))
	defer upstream.Close()
	archiveURL, err := url.Parse(upstream.URL)
	if err != nil {
		test.Fatal(err)
	}
	previousTransport := http.DefaultTransport
	http.DefaultTransport = automaticArchiveTransport{base: previousTransport, archive: upstream.Client().Transport, archiveURL: archiveURL}
	defer func() { http.DefaultTransport = previousTransport }()
	config := backupAppConfig(test)
	config.SourceRefreshInterval = new(time.Duration(0))
	config.BackupS3ConfigFile = backupSettingsFixture(test, func(document map[string]any) {
		document["endpoint"], document["automatic_interval_seconds"] = storage.URL, 60
	})
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	started := time.Now()
	endpoint, stop := runtimeServe(test, app)
	identity := authv1connect.NewAuthServiceClient(http.DefaultClient, endpoint)
	session, err := identity.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: config.BootstrapAdminUsername, Password: "synthetic-backup-password-71"}))
	if err != nil {
		test.Fatal(err)
	}
	token := session.Msg.SessionToken
	owner, err := identity.GetSession(test.Context(), backupRPCRequest(&authv1.GetSessionRequest{}, token))
	if err != nil {
		test.Fatal(err)
	}
	entry, err := app.library.Collect(test.Context(), owner.Msg.UserId, "org/automatic", "")
	if err != nil {
		test.Fatal("real library collection failed", err)
	}
	entry, err = app.library.Get(test.Context(), owner.Msg.UserId, entry.ID)
	if err != nil || len(entry.Skills) != 1 {
		test.Fatal("real library source fixture failed", err)
	}
	if _, err := app.store.CreatePack(test.Context(), owner.Msg.UserId, state.PackDraft{Name: "automatic private pack", Items: []state.PackSelection{{SkillID: entry.Skills[0].ID, Slug: "automatic"}}}); err != nil {
		test.Fatal(err)
	}
	client := managementv1connect.NewBackupManagementServiceClient(http.DefaultClient, endpoint)
	var jobID string
	for {
		page, err := client.ListBackupJobs(test.Context(), backupRPCRequest(&managementv1.ListBackupJobsRequest{Limit: 20}, token))
		if err != nil {
			test.Fatal(err)
		}
		if len(page.Msg.Jobs) != 0 {
			if time.Since(started) < time.Minute || len(page.Msg.Jobs) != 1 {
				test.Fatal("automatic backup ran before its first interval or queued duplicate jobs")
			}
			job := page.Msg.Jobs[0]
			jobID = job.Id
			if job.Phase == "remote_committed" {
				if job.Objects < 6 || job.CiphertextBytes == 0 {
					test.Fatal("automatic job lacks authenticated ciphertext accounting")
				}
				break
			}
			if job.Phase == "failed" || job.Phase == "interrupted" || job.Phase == "commit_unknown" || job.Phase == "canceled" {
				test.Fatalf("automatic publication failed: %s/%s", job.Phase, job.ErrorCode)
			}
		}
		if time.Since(started) > 75*time.Second {
			test.Fatal("real Serve never produced its configured automatic backup")
		}
		time.Sleep(100 * time.Millisecond)
	}
	fixture.mutex.Lock()
	puts, reads, violation := fixture.puts, fixture.reads, fixture.violation
	fixture.mutex.Unlock()
	if puts < 6 || reads == 0 || violation {
		test.Fatal("automatic task bypassed real conditional TLS publication and exact verification")
	}
	stop()
	if err := app.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	job, err := reopened.backups.controller.Get(test.Context(), jobID)
	if err != nil || job.Phase != "remote_committed" {
		test.Fatal("automatic job was not durable across real App restart", err)
	}
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	if fixture.puts != puts {
		test.Fatal("restart replayed automatic upload")
	}
}

func TestServerBackupAutomaticRequiresAdministrator(test *testing.T) {
	config := backupAppConfig(test)
	config.BootstrapAdminUsername, config.BootstrapAdminPasswordFile = "", ""
	config.BackupS3ConfigFile = backupSettingsFixture(test, func(document map[string]any) { document["automatic_interval_seconds"] = 60 })
	app, err := Open(test.Context(), config)
	if app != nil {
		_ = app.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "automatic backups require a configured administrator") {
		test.Fatal("automatic backups can run without a managing administrator", err)
	}
}

func TestServerBackupAutomaticAdministrators(test *testing.T) {
	fixture := &backupBrowserFixture{objects: make(map[string][]byte), done: test.Context()}
	var unknown atomic.Bool
	storage := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if unknown.Load() && request.Method == http.MethodHead && strings.Contains(request.URL.Path, "/complete/") {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fixture.storage(writer, request)
	}))
	defer storage.Close()
	certificate := filepath.Join(test.TempDir(), "synthetic-ca.pem")
	if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: storage.Certificate().Raw}), 0600); err != nil {
		test.Fatal(err)
	}
	test.Setenv("SSL_CERT_FILE", certificate)
	test.Setenv("SSL_CERT_DIR", filepath.Dir(certificate))
	config := backupAppConfig(test)
	config.SourceRefreshInterval = new(time.Duration(0))
	config.BackupS3ConfigFile = backupSettingsFixture(test, func(document map[string]any) {
		document["endpoint"], document["automatic_interval_seconds"] = storage.URL, 60
	})
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	setup := httptest.NewTLSServer(app.runtimeHandler())
	identity := authv1connect.NewAuthServiceClient(setup.Client(), setup.URL)
	registered, err := identity.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "backup-admin-b", Password: "synthetic-backup-admin-b"}))
	if err != nil {
		test.Fatal(err)
	}
	config.AdminUserIDs = []string{registered.Msg.UserId}
	setup.Close()
	if err := app.Close(); err != nil {
		test.Fatal(err)
	}
	app, err = Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	endpoint, stop := runtimeServe(test, app)
	identity = authv1connect.NewAuthServiceClient(http.DefaultClient, endpoint)
	client := managementv1connect.NewBackupManagementServiceClient(http.DefaultClient, endpoint)
	login := func(name, password string) string {
		response, err := identity.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: name, Password: password}))
		if err != nil {
			test.Fatal(err)
		}
		return response.Msg.SessionToken
	}
	first := login("backup-admin", "synthetic-backup-password-71")
	second := login("backup-admin-b", "synthetic-backup-admin-b")
	if _, err := identity.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "automatic-ordinary", Password: "synthetic-ordinary-password"})); err != nil {
		test.Fatal(err)
	}
	ordinary := login("automatic-ordinary", "synthetic-ordinary-password")
	checkBackupRPCBoundary(test, client, ordinary, second)
	actor := uuid.UUID(app.backups.control.InstanceID()).String()
	gate := make(chan struct{})
	fixture.mutex.Lock()
	fixture.uploadGate = gate
	fixture.mutex.Unlock()
	job, err := app.backups.controller.Start(test.Context(), actor, uuid.New().String())
	if err != nil {
		test.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		fixture.mutex.Lock()
		held := fixture.uploadsHeld
		fixture.mutex.Unlock()
		if held > 0 {
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("instance actor did not enter real upload")
		}
		time.Sleep(10 * time.Millisecond)
	}
	page, err := client.ListBackupJobs(test.Context(), backupRPCRequest(&managementv1.ListBackupJobsRequest{}, first))
	if err != nil || len(page.Msg.Jobs) != 1 || page.Msg.Jobs[0].Id != job.ID {
		test.Fatal("administrator A could not discover instance-actor work", err)
	}
	canceled, err := client.CancelBackupJob(test.Context(), backupRPCRequest(&managementv1.CancelBackupJobRequest{JobId: job.ID}, second))
	if err != nil || !canceled.Msg.Job.CancelRequested {
		test.Fatal("administrator B could not cancel instance actor", err)
	}
	fixture.mutex.Lock()
	close(gate)
	fixture.uploadGate = nil
	fixture.mutex.Unlock()
	waitAutomaticServerJob(test, app, job.ID, "canceled")
	unknown.Store(true)
	job, err = app.backups.controller.Start(test.Context(), actor, uuid.New().String())
	if err != nil {
		test.Fatal(err)
	}
	waitAutomaticServerJob(test, app, job.ID, "commit_unknown")
	fixture.mutex.Lock()
	puts := fixture.puts
	fixture.mutex.Unlock()
	unknown.Store(false)
	reconciled, err := client.ReconcileBackupJob(test.Context(), backupRPCRequest(&managementv1.ReconcileBackupJobRequest{JobId: job.ID}, second))
	if err != nil || reconciled.Msg.Job.Phase != "remote_committed" {
		test.Fatal("administrator B could not reconcile unknown instance actor", err)
	}
	fixture.mutex.Lock()
	if fixture.puts != puts {
		test.Error("administrator reconciliation resubmitted ciphertext")
	}
	fixture.mutex.Unlock()
	retainedFirst := login("backup-admin", "synthetic-backup-password-71")
	if _, err := identity.Logout(test.Context(), backupRPCRequest(&authv1.LogoutRequest{}, first)); err != nil {
		test.Fatal(err)
	}
	if _, err := client.GetBackupStatus(test.Context(), backupRPCRequest(&managementv1.GetBackupStatusRequest{}, first)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("logged-out administrator retained control")
	}
	stop()
	if err := app.Close(); err != nil {
		test.Fatal(err)
	}
	config.BootstrapAdminUsername, config.BootstrapAdminPasswordFile = "", ""
	reopened, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	current := httptest.NewTLSServer(reopened.runtimeHandler())
	defer current.Close()
	client = managementv1connect.NewBackupManagementServiceClient(current.Client(), current.URL)
	if _, err := client.GetBackupJob(test.Context(), backupRPCRequest(&managementv1.GetBackupJobRequest{JobId: job.ID}, retainedFirst)); connect.CodeOf(err) != connect.CodePermissionDenied {
		test.Fatal("removed administrator still manages instance actor", err)
	}
	if response, err := client.GetBackupJob(test.Context(), backupRPCRequest(&managementv1.GetBackupJobRequest{JobId: job.ID}, second)); err != nil || response.Msg.Job.Phase != "remote_committed" {
		test.Fatal("current administrator lost old instance-actor work", err)
	}
}

func waitAutomaticServerJob(test *testing.T, app *App, identifier, phase string) {
	test.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		job, err := app.backups.controller.Get(test.Context(), identifier)
		if err != nil {
			test.Fatal(err)
		}
		if job.Phase == phase {
			return
		}
		if time.Now().After(deadline) || job.Phase == "failed" || job.Phase == "remote_committed" {
			test.Fatalf("instance actor did not reach %s: %s/%s", phase, job.Phase, job.Code)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServerBackupAutomaticRuntimeLease(test *testing.T) {
	for _, uncertain := range []bool{false, true} {
		test.Run(map[bool]string{false: "clean", true: "backup-unknown"}[uncertain], func(test *testing.T) {
			config := backupAppConfig(test)
			config.BackupS3ConfigFile = backupSettingsFixture(test, func(document map[string]any) { document["automatic_interval_seconds"] = 60 })
			app, err := openApp(test.Context(), config, appRuntimeHooks{close: func(stage string, original error) error {
				if uncertain && stage == "backup" {
					return errors.New("synthetic backup-close outcome unknown")
				}
				return nil
			}})
			if err != nil {
				test.Fatal(err)
			}
			_, stop := runtimeServe(test, app)
			requireRuntimeProbe(test, config.DataDirectory, "deadline")
			var workers sync.WaitGroup
			failures := make(chan error, 2)
			workers.Go(func() { failures <- app.Close() })
			workers.Go(func() { failures <- app.backups.controller.StopAutomatic(test.Context()) })
			workers.Wait()
			close(failures)
			unknownSeen := false
			for err := range failures {
				if err != nil {
					if !uncertain || !errors.Is(err, runtimegen.ErrUncertain) {
						test.Fatal(err)
					}
					unknownSeen = true
				}
			}
			stop()
			if uncertain {
				if !unknownSeen {
					test.Fatal("backup close uncertainty was hidden")
				}
				requireRuntimeProbe(test, config.DataDirectory, "deadline")
			} else {
				requireRuntimeProbe(test, config.DataDirectory, "acquired")
			}
		})
	}
}
