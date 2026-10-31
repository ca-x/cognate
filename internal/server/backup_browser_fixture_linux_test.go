package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json/v2"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type backupBrowserFixture struct {
	mutex         sync.Mutex
	objects       map[string][]byte
	puts          int
	reads         int
	completes     int
	violation     bool
	mode          string
	uploadGate    chan struct{}
	rpcGate       chan struct{}
	uploadsHeld   int
	rpcsHeld      int
	rpcsCanceled  int
	startIDs      []string
	startJobs     []string
	listCursors   []string
	faults        []string
	seedFailure   map[string]string
	public        *httptest.Server
	disabled      *httptest.Server
	control       *httptest.Server
	capability    string
	administrator string
	adminPassword string
	ordinary      string
	userPassword  string
	done          context.Context
	stop          context.CancelFunc
}

func (fixture *backupBrowserFixture) storage(writer http.ResponseWriter, request *http.Request) {
	fixture.mutex.Lock()
	query := request.URL.Query()
	operation := map[string]string{http.MethodPut: "PutObject", http.MethodGet: "GetObject", http.MethodHead: "HeadObject"}[request.Method]
	if operation == "" || len(query) > 1 || len(query) == 1 && query.Get("x-id") != operation || !strings.HasPrefix(request.URL.Path, "/cognate-tests/backup-tests/") || request.Header.Get("Authorization") == "" {
		fixture.violation = true
		fixture.mutex.Unlock()
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if request.Method != http.MethodPut {
		fixture.reads++
		data, exists := fixture.objects[request.URL.Path]
		fixture.mutex.Unlock()
		if !exists {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("ETag", `"synthetic-browser-etag"`)
		http.ServeContent(writer, request, "", time.Unix(1700000000, 0), bytes.NewReader(data))
		return
	}
	fixture.puts++
	gate := fixture.uploadGate
	if gate != nil {
		fixture.uploadsHeld++
	}
	fixture.mutex.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-request.Context().Done():
			return
		case <-fixture.done.Done():
			return
		}
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, (16<<20)+1))
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	if err != nil || len(data) > 16<<20 || request.Header.Get("If-None-Match") != "*" {
		fixture.violation = true
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, exists := fixture.objects[request.URL.Path]; exists {
		writer.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if prefix, complete := strings.CutSuffix(request.URL.Path, "complete/00000000"); complete {
		fixture.completes++
		kinds := make(map[string]bool)
		for key := range fixture.objects {
			if relative, matched := strings.CutPrefix(key, prefix); matched {
				kind, _, _ := strings.Cut(relative, "/")
				kinds[kind] = true
			}
		}
		if !kinds["data"] || !kinds["page"] || !kinds["bootstrap"] {
			fixture.violation = true
		}
	}
	fixture.objects[request.URL.Path] = data
	writer.Header().Set("ETag", `"synthetic-browser-etag"`)
}

func backupBrowserDecode(data []byte, contentType string, message proto.Message) error {
	if strings.Contains(contentType, "json") {
		return protojson.Unmarshal(data, message)
	}
	return proto.Unmarshal(data, message)
}

func backupBrowserEncode(message proto.Message, contentType string) ([]byte, error) {
	if strings.Contains(contentType, "json") {
		return protojson.Marshal(message)
	}
	return proto.Marshal(message)
}

func (fixture *backupBrowserFixture) handler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.Contains(request.URL.Path, ".BackupManagementService/") {
			handler.ServeHTTP(writer, request)
			return
		}
		isStart := strings.HasSuffix(request.URL.Path, "/StartBackup")
		isList := strings.HasSuffix(request.URL.Path, "/ListBackupJobs")
		subsequentPage := false
		if isStart || isList {
			data, err := io.ReadAll(io.LimitReader(request.Body, 4097))
			if err != nil || len(data) > 4096 {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			request.Body = io.NopCloser(bytes.NewReader(data))
			if isStart {
				message := new(managementv1.StartBackupRequest)
				if backupBrowserDecode(data, request.Header.Get("Content-Type"), message) == nil {
					fixture.mutex.Lock()
					fixture.startIDs = append(fixture.startIDs, message.RequestId)
					fixture.mutex.Unlock()
				}
			} else {
				message := new(managementv1.ListBackupJobsRequest)
				if backupBrowserDecode(data, request.Header.Get("Content-Type"), message) == nil {
					subsequentPage = message.Cursor != ""
					fixture.mutex.Lock()
					fixture.listCursors = append(fixture.listCursors, message.Cursor)
					fixture.mutex.Unlock()
				}
			}
		}
		recorded := httptest.NewRecorder()
		handler.ServeHTTP(recorded, request)
		data := recorded.Body.Bytes()
		if recorded.Header().Get("Content-Encoding") == "gzip" {
			compressed, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			data, err = io.ReadAll(io.LimitReader(compressed, 65537))
			closeErr := compressed.Close()
			if err != nil || closeErr != nil || len(data) > 65536 {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			recorded.Header().Del("Content-Encoding")
		}
		recorded.Header().Del("Content-Length")
		fixture.mutex.Lock()
		mode, gate := fixture.mode, fixture.rpcGate
		if isStart && recorded.Code == http.StatusOK {
			message := new(managementv1.StartBackupResponse)
			if backupBrowserDecode(data, recorded.Header().Get("Content-Type"), message) == nil && message.Job != nil {
				fixture.startJobs = append(fixture.startJobs, message.Job.Id)
			}
			if mode == "lost-start" || mode == "malformed-start" {
				fixture.mode = ""
			}
			if gate != nil {
				fixture.rpcsHeld++
			}
		} else {
			gate = nil
		}
		fixture.mutex.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-request.Context().Done():
				fixture.mutex.Lock()
				fixture.rpcsCanceled++
				fixture.mutex.Unlock()
				return
			case <-fixture.done.Done():
				return
			}
		}
		if recorded.Code == http.StatusOK {
			var corrupted proto.Message
			switch {
			case isStart && mode == "lost-start", strings.HasSuffix(request.URL.Path, "/GetBackupStatus") && mode == "status-error", isList && (mode == "list-error" || mode == "list-error-page2" && subsequentPage):
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = writer.Write([]byte(`{"code":"unavailable","message":"synthetic transport failure"}`))
				return
			case isStart && mode == "malformed-start":
				message := new(managementv1.StartBackupResponse)
				if backupBrowserDecode(data, recorded.Header().Get("Content-Type"), message) == nil && message.Job != nil {
					message.Job.Phase = "unrecognized_fixture_phase"
					corrupted = message
				}
				fixture.mutex.Lock()
				fixture.faults = append(fixture.faults, "malformed-start:"+recorded.Header().Get("Content-Type"))
				if corrupted == nil {
					fixture.violation = true
				}
				fixture.mutex.Unlock()
			case strings.HasSuffix(request.URL.Path, "/GetBackupStatus") && mode == "malformed-status":
				corrupted = &managementv1.GetBackupStatusResponse{Enabled: true, Scope: "unsupported"}
			case isList && (mode == "malformed-page" || mode == "malformed-page2" && subsequentPage):
				corrupted = &managementv1.ListBackupJobsResponse{NextCursor: "unsupported-cursor"}
			case isList && mode == "empty-page2" && subsequentPage:
				corrupted = &managementv1.ListBackupJobsResponse{}
			case strings.HasSuffix(request.URL.Path, "/GetBackupJob") && mode == "malformed-job":
				corrupted = &managementv1.GetBackupJobResponse{Job: &managementv1.BackupJob{Id: "unsupported-id", Phase: "remote_committed"}}
			}
			if corrupted != nil {
				var err error
				data, err = backupBrowserEncode(corrupted, recorded.Header().Get("Content-Type"))
				if err != nil {
					writer.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
		}
		for name, values := range recorded.Header() {
			for _, value := range values {
				writer.Header().Add(name, value)
			}
		}
		writer.WriteHeader(recorded.Code)
		_, _ = writer.Write(data)
	})
}

func (fixture *backupBrowserFixture) controlHandler(test *testing.T) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(fixture.capability)) != 1 {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /state":
			fixture.mutex.Lock()
			defer fixture.mutex.Unlock()
			data, _ := json.Marshal(map[string]any{"puts": fixture.puts, "reads": fixture.reads, "completes": fixture.completes, "objects": len(fixture.objects), "violation": fixture.violation, "uploadsHeld": fixture.uploadsHeld, "rpcsHeld": fixture.rpcsHeld, "rpcsCanceled": fixture.rpcsCanceled, "startIDs": fixture.startIDs, "startJobs": fixture.startJobs, "listCursors": fixture.listCursors, "faults": fixture.faults, "seedFailure": fixture.seedFailure})
			_, _ = writer.Write(data)
		case "POST /mode":
			var value struct {
				Mode string `json:"mode"`
			}
			data, err := io.ReadAll(io.LimitReader(request.Body, 4097))
			if err != nil || len(data) > 4096 || json.Unmarshal(data, &value) != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			fixture.mutex.Lock()
			defer fixture.mutex.Unlock()
			if fixture.uploadGate != nil {
				close(fixture.uploadGate)
				fixture.uploadGate = nil
			}
			if fixture.rpcGate != nil {
				close(fixture.rpcGate)
				fixture.rpcGate = nil
			}
			fixture.mode = value.Mode
			if value.Mode == "hold-upload" {
				fixture.uploadGate = make(chan struct{})
			}
			if value.Mode == "hold-start" {
				fixture.rpcGate = make(chan struct{})
			}
			writer.WriteHeader(http.StatusNoContent)
		case "POST /seed":
			client := managementv1connect.NewBackupManagementServiceClient(fixture.public.Client(), fixture.public.URL)
			authentication := authv1connect.NewAuthServiceClient(fixture.public.Client(), fixture.public.URL)
			login, err := authentication.Login(request.Context(), connect.NewRequest(&authv1.LoginRequest{Username: fixture.administrator, Password: fixture.adminPassword}))
			if err != nil {
				fixture.recordSeedFailure("login", err, nil)
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			for range 21 {
				started, err := client.StartBackup(request.Context(), backupRPCRequest(&managementv1.StartBackupRequest{RequestId: uuid.New().String()}, login.Msg.SessionToken))
				if err != nil || started.Msg.Job == nil {
					fixture.recordSeedFailure("start", err, nil)
					writer.WriteHeader(http.StatusInternalServerError)
					return
				}
				for {
					current, err := client.GetBackupJob(request.Context(), backupRPCRequest(&managementv1.GetBackupJobRequest{JobId: started.Msg.Job.Id}, login.Msg.SessionToken))
					var job *managementv1.BackupJob
					if current != nil {
						job = current.Msg.GetJob()
					}
					if err != nil || job == nil || job.Phase == "failed" || job.Phase == "interrupted" || job.Phase == "commit_unknown" {
						fixture.recordSeedFailure("wait", err, job)
						writer.WriteHeader(http.StatusInternalServerError)
						return
					}
					if current.Msg.Job.Phase == "remote_committed" {
						break
					}
					select {
					case <-request.Context().Done():
						return
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
			writer.WriteHeader(http.StatusNoContent)
		case "POST /stop":
			writer.WriteHeader(http.StatusNoContent)
			fixture.stop()
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	})
}

func (fixture *backupBrowserFixture) recordSeedFailure(operation string, err error, job *managementv1.BackupJob) {
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	fixture.seedFailure = map[string]string{"operation": operation}
	if err != nil {
		fixture.seedFailure["code"] = connect.CodeOf(err).String()
	}
	if job != nil {
		fixture.seedFailure["phase"], fixture.seedFailure["jobCode"] = job.Phase, job.ErrorCode
	}
}

func TestBackupBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_BACKUP_BROWSER_DIR")
	if directory == "" {
		test.Skip("dedicated fresh-process browser fixture only")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		test.Fatal("browser fixture requires an isolated private directory")
	}
	done, stop := context.WithCancel(test.Context())
	defer stop()
	fixture := &backupBrowserFixture{objects: make(map[string][]byte), capability: rand.Text(), administrator: "backup-admin", adminPassword: "synthetic-backup-password-71", ordinary: "backup-ordinary", userPassword: rand.Text(), done: done, stop: stop}
	storage := httptest.NewTLSServer(http.HandlerFunc(fixture.storage))
	defer storage.Close()
	certificate := filepath.Join(directory, "synthetic-ca.pem")
	if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: storage.Certificate().Raw}), 0600); err != nil {
		test.Fatal(err)
	}
	test.Setenv("SSL_CERT_FILE", certificate)
	test.Setenv("SSL_CERT_DIR", directory)
	config := backupAppConfig(test)
	config.BackupS3ConfigFile = backupSettingsFixture(test, func(document map[string]any) { document["endpoint"] = storage.URL })
	app, err := Open(test.Context(), config)
	if err != nil {
		test.Fatal(err)
	}
	defer app.Close()
	fixture.public = httptest.NewTLSServer(fixture.handler(app.handler))
	defer fixture.public.Close()
	authentication := authv1connect.NewAuthServiceClient(fixture.public.Client(), fixture.public.URL)
	if _, err := authentication.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: fixture.ordinary, Password: fixture.userPassword})); err != nil {
		test.Fatal("synthetic ordinary account unavailable")
	}
	disabledConfig := backupAppConfig(test)
	disabledConfig.BackupEnabled, disabledConfig.BackupS3ConfigFile, disabledConfig.BackupRecoveryKeyFile = false, "", ""
	disabledApp, err := Open(test.Context(), disabledConfig)
	if err != nil {
		test.Fatal(err)
	}
	defer disabledApp.Close()
	fixture.disabled = httptest.NewTLSServer(disabledApp.handler)
	defer fixture.disabled.Close()
	fixture.control = httptest.NewServer(fixture.controlHandler(test))
	defer fixture.control.Close()
	data, err := json.Marshal(map[string]any{"url": fixture.public.URL, "disabledURL": fixture.disabled.URL, "controlURL": fixture.control.URL, "capability": fixture.capability, "username": fixture.administrator, "password": fixture.adminPassword, "ordinaryUsername": fixture.ordinary, "ordinaryPassword": fixture.userPassword})
	if err != nil || os.WriteFile(filepath.Join(directory, "ready.json"), data, 0600) != nil {
		test.Fatal("private fixture readiness unavailable")
	}
	<-done.Done()
}
