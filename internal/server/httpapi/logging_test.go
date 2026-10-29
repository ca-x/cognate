package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/gen/skillhub/management/v1/managementv1connect"
	"github.com/ca-x/cognate/internal/server/logging"
	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/state"
)

func testLogManager(test *testing.T) (*logging.Manager, string) {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal("prepare private logging fixture")
	}
	manager, err := logging.Open(test.Context(), directory)
	if err != nil {
		test.Fatal("open logging fixture")
	}
	test.Cleanup(func() { _ = manager.Close() })
	return manager, directory
}

func TestLogManagementAuthorizationAndCAS(test *testing.T) {
	service, store := testServiceAndStore(test)
	adminID, session := mcpSession(test, service, "administrator")
	otherID, otherSession := mcpSession(test, service, "ordinary")
	manager, _ := testLogManager(test)
	allowlist := []string{adminID}
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Logging: manager, AdminUserIDs: allowlist}))
	defer endpoint.Close()
	allowlist[0] = otherID
	client := managementv1connect.NewLogManagementServiceClient(endpoint.Client(), endpoint.URL)
	for _, token := range []string{"", "invalid-session"} {
		if _, err := client.GetLogSettings(test.Context(), mcpRequest(&managementv1.GetLogSettingsRequest{}, token)); connect.CodeOf(err) != connect.CodeUnauthenticated {
			test.Fatal("unauthenticated caller reached log settings")
		}
	}
	if _, err := client.GetLogSettings(test.Context(), mcpRequest(&managementv1.GetLogSettingsRequest{}, otherSession)); connect.CodeOf(err) != connect.CodePermissionDenied {
		test.Fatal("ordinary account read global log settings or mutable allowlist was retained")
	}
	access, err := mcpaccess.NewService(store)
	if err != nil {
		test.Fatal("prepare MCP access fixture")
	}
	_, mcpSecret, err := access.Create(test.Context(), adminID, state.MCPMetadata{Name: "synthetic", Scopes: []string{state.MCPCatalogRead}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		test.Fatal("create synthetic MCP token")
	}
	for _, token := range []string{mcpSecret.Reveal(), "shp1.synthetic-share-token"} {
		if _, err := client.GetLogSettings(test.Context(), mcpRequest(&managementv1.GetLogSettingsRequest{}, token)); connect.CodeOf(err) != connect.CodeUnauthenticated {
			test.Fatal("non-session credential reached log settings")
		}
	}
	current, err := client.GetLogSettings(test.Context(), mcpRequest(&managementv1.GetLogSettingsRequest{}, session))
	if err != nil || current.Msg.Settings == nil || current.Msg.Settings.Level != "info" || current.Msg.Settings.MaxFileMib != 10 || current.Msg.Settings.MaxBackups != 7 || current.Msg.Settings.RetentionDays != 14 || current.Msg.Settings.Revision < 1 || current.Msg.Health == nil || current.Header().Get("Cache-Control") != "no-store" {
		test.Fatal("administrator did not receive private defaults and safe health")
	}
	settings := &managementv1.LogSettings{Level: "debug", MaxFileMib: 2, MaxBackups: 3, RetentionDays: 4}
	request := &managementv1.UpdateLogSettingsRequest{Settings: settings, ExpectedRevision: current.Msg.Settings.Revision}
	if _, err := client.UpdateLogSettings(test.Context(), mcpRequest(request, otherSession)); connect.CodeOf(err) != connect.CodePermissionDenied {
		test.Fatal("ordinary account changed global logging settings")
	}
	updated, err := client.UpdateLogSettings(test.Context(), mcpRequest(request, session))
	if err != nil || updated.Msg.Settings == nil || updated.Msg.Settings.Level != "debug" || updated.Msg.Settings.Revision != current.Msg.Settings.Revision+1 {
		test.Fatalf("authorized logging settings update failed: code=%s response=%t", connect.CodeOf(err), updated != nil)
	}
	if _, err := client.UpdateLogSettings(test.Context(), mcpRequest(request, session)); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatal("stale logging update was not rejected")
	}
	stored, err := manager.Snapshot(test.Context())
	if err != nil || stored.Settings.Revision != updated.Msg.Settings.Revision || stored.Settings.MaxFileMiB != 2 {
		test.Fatal("settings RPC did not persist the expected revision")
	}
	if err := service.Logout(test.Context(), session); err != nil {
		test.Fatal("revoke synthetic administrator session")
	}
	if _, err := client.GetLogSettings(test.Context(), mcpRequest(&managementv1.GetLogSettingsRequest{}, session)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("revoked administrator session retained global access")
	}
}

func TestLogManagementInputOriginAndFailurePrivacy(test *testing.T) {
	service := testService(test)
	adminID, session := mcpSession(test, service, "administrator")
	manager, directory := testLogManager(test)
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Logging: manager, AdminUserIDs: []string{adminID}}))
	defer endpoint.Close()
	client := managementv1connect.NewLogManagementServiceClient(endpoint.Client(), endpoint.URL)
	current, err := manager.Snapshot(test.Context())
	if err != nil {
		test.Fatal("read fixture policy")
	}
	valid := func() *managementv1.UpdateLogSettingsRequest {
		return &managementv1.UpdateLogSettingsRequest{ExpectedRevision: current.Settings.Revision, Settings: &managementv1.LogSettings{Level: "info", MaxFileMib: 10, MaxBackups: 7, RetentionDays: 14}}
	}
	for _, mutate := range []func(*managementv1.UpdateLogSettingsRequest){
		func(request *managementv1.UpdateLogSettingsRequest) { request.Settings = nil },
		func(request *managementv1.UpdateLogSettingsRequest) { request.ExpectedRevision = 0 },
		func(request *managementv1.UpdateLogSettingsRequest) {
			request.Settings.Level = "synthetic-secret-input"
		},
		func(request *managementv1.UpdateLogSettingsRequest) { request.Settings.MaxFileMib = -1 },
		func(request *managementv1.UpdateLogSettingsRequest) { request.Settings.MaxFileMib = 101 },
		func(request *managementv1.UpdateLogSettingsRequest) { request.Settings.MaxBackups = 0 },
		func(request *managementv1.UpdateLogSettingsRequest) { request.Settings.MaxBackups = 101 },
		func(request *managementv1.UpdateLogSettingsRequest) { request.Settings.RetentionDays = 366 },
	} {
		request := valid()
		mutate(request)
		_, err := client.UpdateLogSettings(test.Context(), mcpRequest(request, session))
		if connect.CodeOf(err) != connect.CodeInvalidArgument || strings.Contains(err.Error(), "synthetic-secret-input") {
			test.Fatalf("invalid logging settings were accepted or reflected: code=%s", connect.CodeOf(err))
		}
	}
	request := mcpRequest(valid(), session)
	request.Header().Set("Origin", "https://untrusted.invalid")
	if _, err := client.UpdateLogSettings(test.Context(), request); connect.CodeOf(err) != connect.CodePermissionDenied {
		test.Fatal("cross-origin logging mutation accepted")
	}
	request = mcpRequest(valid(), session)
	request.Header().Add("Authorization", "Bearer synthetic-duplicate")
	if _, err := client.UpdateLogSettings(test.Context(), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
		test.Fatal("duplicate authorization reached logging administration")
	}
	snapshot, err := manager.Snapshot(test.Context())
	if err != nil || snapshot.Settings.Revision != current.Settings.Revision {
		test.Fatal("rejected settings request changed persisted policy")
	}
	if err := manager.Close(); err != nil {
		test.Fatal("close logger for fixed failure response")
	}
	_, err = client.GetLogSettings(test.Context(), mcpRequest(&managementv1.GetLogSettingsRequest{}, session))
	if connect.CodeOf(err) != connect.CodeUnavailable || strings.Contains(err.Error(), directory) {
		test.Fatal("closed storage response leaked a path or did not fail safely")
	}
}

func TestLogManagementEmptyAdministratorAllowlist(test *testing.T) {
	service := testService(test)
	_, session := mcpSession(test, service, "firstuser")
	manager, _ := testLogManager(test)
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Logging: manager}))
	defer endpoint.Close()
	client := managementv1connect.NewLogManagementServiceClient(endpoint.Client(), endpoint.URL)
	if _, err := client.GetLogSettings(test.Context(), mcpRequest(&managementv1.GetLogSettingsRequest{}, session)); connect.CodeOf(err) != connect.CodePermissionDenied {
		test.Fatal("first registered account became an administrator")
	}
}

func TestLogManagementRejectsOversizedRequest(test *testing.T) {
	service := testService(test)
	manager, _ := testLogManager(test)
	endpoint := httptest.NewServer(NewWithOptions(service, Options{Logging: manager}))
	defer endpoint.Close()
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, endpoint.URL+managementv1connect.LogManagementServiceUpdateLogSettingsProcedure, bytes.NewBufferString(`{"settings":{"level":"`+strings.Repeat("x", 8192)+`"}}`))
	if err != nil {
		test.Fatal("create oversized fixture")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := endpoint.Client().Do(request)
	if err != nil {
		test.Fatal("send oversized fixture")
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode < 400 || len(data) > 4096 || bytes.Contains(data, bytes.Repeat([]byte("x"), 128)) {
		test.Fatal("oversized logging request was accepted or echoed")
	}
}
