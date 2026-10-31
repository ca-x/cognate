package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"uuid"

	"connectrpc.com/connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/internal/server/backup"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

type backupManagementHandler struct {
	auth       *authHandler
	controller *backup.Controller
	admins     []string
}

func (handler *backupManagementHandler) administrator(ctx context.Context, header http.Header) (string, error) {
	principal, err := handler.auth.authenticate(ctx, header)
	if err != nil {
		return "", err
	}
	if !slices.Contains(handler.admins, principal) {
		return "", connect.NewError(connect.CodePermissionDenied, errors.New("backup administration requires an authorized account"))
	}
	return principal, nil
}

func (handler *backupManagementHandler) requireEnabled(ctx context.Context, header http.Header) (string, error) {
	principal, err := handler.administrator(ctx, header)
	if err != nil {
		return "", err
	}
	if handler.controller == nil {
		return "", connect.NewError(connect.CodeFailedPrecondition, errors.New("server backup is disabled"))
	}
	return principal, nil
}

func (handler *backupManagementHandler) GetBackupStatus(ctx context.Context, request *connect.Request[managementv1.GetBackupStatusRequest]) (*connect.Response[managementv1.GetBackupStatusResponse], error) {
	if _, err := handler.administrator(ctx, request.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&managementv1.GetBackupStatusResponse{Enabled: handler.controller != nil, Scope: "instance"}), nil
}

func (handler *backupManagementHandler) StartBackup(ctx context.Context, request *connect.Request[managementv1.StartBackupRequest]) (*connect.Response[managementv1.StartBackupResponse], error) {
	principal, err := handler.requireEnabled(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if !backupIdentifier(request.Msg.RequestId) {
		return nil, backupManagementError(ctx, backup.ErrInvalid)
	}
	job, err := handler.controller.Start(ctx, principal, request.Msg.RequestId)
	if err != nil {
		return nil, backupManagementError(ctx, err)
	}
	return connect.NewResponse(&managementv1.StartBackupResponse{Job: backupJobView(job)}), nil
}

func (handler *backupManagementHandler) GetBackupJob(ctx context.Context, request *connect.Request[managementv1.GetBackupJobRequest]) (*connect.Response[managementv1.GetBackupJobResponse], error) {
	if _, err := handler.requireEnabled(ctx, request.Header()); err != nil {
		return nil, err
	}
	if !backupIdentifier(request.Msg.JobId) {
		return nil, backupManagementError(ctx, backup.ErrInvalid)
	}
	job, err := handler.controller.Get(ctx, request.Msg.JobId)
	if err != nil {
		return nil, backupManagementError(ctx, err)
	}
	return connect.NewResponse(&managementv1.GetBackupJobResponse{Job: backupJobView(job)}), nil
}

func (handler *backupManagementHandler) ListBackupJobs(ctx context.Context, request *connect.Request[managementv1.ListBackupJobsRequest]) (*connect.Response[managementv1.ListBackupJobsResponse], error) {
	if _, err := handler.requireEnabled(ctx, request.Header()); err != nil {
		return nil, err
	}
	limit := request.Msg.Limit
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 || request.Msg.Cursor != "" && !backupIdentifier(request.Msg.Cursor) {
		return nil, backupManagementError(ctx, backup.ErrInvalid)
	}
	page, err := handler.controller.List(ctx, int(limit), request.Msg.Cursor)
	if err != nil {
		return nil, backupManagementError(ctx, err)
	}
	response := &managementv1.ListBackupJobsResponse{NextCursor: page.Next, Jobs: make([]*managementv1.BackupJob, 0, len(page.Jobs))}
	for _, job := range page.Jobs {
		response.Jobs = append(response.Jobs, backupJobView(job))
	}
	return connect.NewResponse(response), nil
}

func (handler *backupManagementHandler) CancelBackupJob(ctx context.Context, request *connect.Request[managementv1.CancelBackupJobRequest]) (*connect.Response[managementv1.CancelBackupJobResponse], error) {
	principal, err := handler.requireEnabled(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if !backupIdentifier(request.Msg.JobId) {
		return nil, backupManagementError(ctx, backup.ErrInvalid)
	}
	job, err := handler.controller.Cancel(ctx, principal, request.Msg.JobId)
	if err != nil {
		return nil, backupManagementError(ctx, err)
	}
	return connect.NewResponse(&managementv1.CancelBackupJobResponse{Job: backupJobView(job)}), nil
}

func (handler *backupManagementHandler) ReconcileBackupJob(ctx context.Context, request *connect.Request[managementv1.ReconcileBackupJobRequest]) (*connect.Response[managementv1.ReconcileBackupJobResponse], error) {
	principal, err := handler.requireEnabled(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if !backupIdentifier(request.Msg.JobId) {
		return nil, backupManagementError(ctx, backup.ErrInvalid)
	}
	job, err := handler.controller.Reconcile(ctx, principal, request.Msg.JobId)
	if err != nil {
		return nil, backupManagementError(ctx, err)
	}
	return connect.NewResponse(&managementv1.ReconcileBackupJobResponse{Job: backupJobView(job)}), nil
}

func backupIdentifier(value string) bool {
	identifier, err := uuid.Parse(value)
	return err == nil && identifier.String() == value && value != "00000000-0000-0000-0000-000000000000"
}

func backupJobView(job backup.Job) *managementv1.BackupJob {
	return &managementv1.BackupJob{Id: job.ID, Phase: job.Phase, ErrorCode: job.Code, CreatedUnixMillis: job.CreatedAt.UnixMilli(), UpdatedUnixMillis: job.UpdatedAt.UnixMilli(), Objects: job.Objects, CiphertextBytes: job.CiphertextBytes, CancelRequested: job.CancelRequested}
}

func backupManagementError(ctx context.Context, err error) error {
	for _, critical := range []error{backup.ErrControlUncertain, backup.ErrSealUncertain, backup.ErrSealCleanup, library.ErrSnapshotArchiveUncertain, state.ErrLocalSnapshotUncertain, state.ErrLocalSnapshotCleanup} {
		if errors.Is(err, critical) {
			return connect.NewError(connect.CodeUnavailable, errors.New("backup state requires reconciliation"))
		}
	}
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	for _, mapping := range []struct {
		cause error
		code  connect.Code
		text  string
	}{
		{backup.ErrInvalid, connect.CodeInvalidArgument, "invalid backup request"},
		{backup.ErrControlConflict, connect.CodeAborted, "backup request conflicts with recorded state"},
		{backup.ErrControlNotFound, connect.CodeNotFound, "backup job not found"},
		{backup.ErrControlBudget, connect.CodeResourceExhausted, "backup control capacity exhausted"},
		{backup.ErrBusy, connect.CodeResourceExhausted, "a backup operation is already active"},
	} {
		if errors.Is(err, mapping.cause) {
			return connect.NewError(mapping.code, errors.New(mapping.text))
		}
	}
	return connect.NewError(connect.CodeUnavailable, errors.New("backup control unavailable"))
}
