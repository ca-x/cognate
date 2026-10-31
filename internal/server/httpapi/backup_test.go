package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/gen/skillhub/management/v1/managementv1connect"
	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

func TestBackupManagementDisabledAuthorization(test *testing.T) {
	service := testService(test)
	admin, session := mcpSession(test, service, "backup-administrator")
	ordinary, ordinarySession := mcpSession(test, service, "backup-ordinary")
	admins := []string{admin}
	endpoint := httptest.NewTLSServer(NewWithOptions(service, Options{AdminUserIDs: admins}))
	defer endpoint.Close()
	admins[0] = ordinary
	client := managementv1connect.NewBackupManagementServiceClient(endpoint.Client(), endpoint.URL)
	calls := map[string]func(string) error{
		"status": func(token string) error {
			_, err := client.GetBackupStatus(test.Context(), mcpRequest(&managementv1.GetBackupStatusRequest{}, token))
			return err
		},
		"start": func(token string) error {
			_, err := client.StartBackup(test.Context(), mcpRequest(&managementv1.StartBackupRequest{RequestId: "synthetic-private-request"}, token))
			return err
		},
		"get": func(token string) error {
			_, err := client.GetBackupJob(test.Context(), mcpRequest(&managementv1.GetBackupJobRequest{JobId: "synthetic-private-job"}, token))
			return err
		},
		"list": func(token string) error {
			_, err := client.ListBackupJobs(test.Context(), mcpRequest(&managementv1.ListBackupJobsRequest{Limit: 1000, Cursor: "synthetic-private-cursor"}, token))
			return err
		},
		"cancel": func(token string) error {
			_, err := client.CancelBackupJob(test.Context(), mcpRequest(&managementv1.CancelBackupJobRequest{JobId: "synthetic-private-job"}, token))
			return err
		},
		"reconcile": func(token string) error {
			_, err := client.ReconcileBackupJob(test.Context(), mcpRequest(&managementv1.ReconcileBackupJobRequest{JobId: "synthetic-private-job"}, token))
			return err
		},
	}
	for name, call := range calls {
		test.Run(name, func(test *testing.T) {
			for _, token := range []string{"", "invalid-session", "shp1.synthetic-share-token", "mcp1.synthetic-token"} {
				if err := call(token); connect.CodeOf(err) != connect.CodeUnauthenticated || strings.Contains(err.Error(), "synthetic-private") {
					test.Fatal("non-session caller probed backup state or input leaked", err)
				}
			}
			if err := call(ordinarySession); connect.CodeOf(err) != connect.CodePermissionDenied || strings.Contains(err.Error(), "synthetic-private") {
				test.Fatal("ordinary owner probed global backup state", err)
			}
			if name != "status" {
				if err := call(session); connect.CodeOf(err) != connect.CodeFailedPrecondition || strings.Contains(err.Error(), "synthetic-private") {
					test.Fatal("disabled backup accepted a job or leaked input", err)
				}
			}
		})
	}
	status, err := client.GetBackupStatus(test.Context(), mcpRequest(&managementv1.GetBackupStatusRequest{}, session))
	if err != nil || status.Msg.Enabled || status.Msg.AutomaticEnabled || status.Msg.RestoreAvailable || status.Msg.Scope != "instance" || status.Header().Get("Cache-Control") != "no-store" {
		test.Fatal("administrator did not receive accurate private disabled status", err)
	}
	if err := service.Logout(test.Context(), session); err != nil {
		test.Fatal(err)
	}
	for _, call := range calls {
		if err := call(session); connect.CodeOf(err) != connect.CodeUnauthenticated {
			test.Fatal("revoked administrator retained backup access", err)
		}
	}
}

func TestBackupManagementErrorPriorityAndRedaction(test *testing.T) {
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	for _, critical := range []error{backup.ErrControlUncertain, backup.ErrSealUncertain, backup.ErrSealCleanup, library.ErrSnapshotArchiveUncertain, state.ErrLocalSnapshotUncertain, state.ErrLocalSnapshotCleanup} {
		actual := backupManagementError(ctx, errors.Join(context.Canceled, critical, errors.New("synthetic-private-cause")))
		if connect.CodeOf(actual) != connect.CodeUnavailable || strings.Contains(actual.Error(), "synthetic-private") {
			test.Fatal("critical cleanup uncertainty lost to cancellation or exposed its cause")
		}
	}
	for _, mapping := range []struct {
		cause error
		code  connect.Code
	}{
		{context.Canceled, connect.CodeCanceled}, {context.DeadlineExceeded, connect.CodeDeadlineExceeded},
		{backup.ErrInvalid, connect.CodeInvalidArgument}, {backup.ErrControlConflict, connect.CodeAborted},
		{backup.ErrControlNotFound, connect.CodeNotFound}, {backup.ErrControlBudget, connect.CodeResourceExhausted},
		{backup.ErrBusy, connect.CodeResourceExhausted}, {backup.ErrUnavailable, connect.CodeUnavailable},
	} {
		actual := backupManagementError(test.Context(), errors.Join(mapping.cause, errors.New("synthetic-private-cause")))
		if connect.CodeOf(actual) != mapping.code || strings.Contains(actual.Error(), "synthetic-private") {
			test.Fatal("safe backup error mapping changed", mapping.code, connect.CodeOf(actual))
		}
	}
}
