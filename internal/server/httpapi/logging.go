package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"connectrpc.com/connect"
	managementv1 "github.com/ca-x/cognate/gen/skillhub/management/v1"
	"github.com/ca-x/cognate/internal/server/logging"
)

type logManagementHandler struct {
	auth   *authHandler
	logs   *logging.Manager
	admins []string
}

func (handler *logManagementHandler) GetLogSettings(ctx context.Context, request *connect.Request[managementv1.GetLogSettingsRequest]) (*connect.Response[managementv1.GetLogSettingsResponse], error) {
	if err := handler.administrator(ctx, request.Header()); err != nil {
		return nil, err
	}
	snapshot, err := handler.logs.Snapshot(ctx)
	if err != nil {
		return nil, logManagementError(ctx, err)
	}
	settings, health := logManagementView(snapshot)
	return connect.NewResponse(&managementv1.GetLogSettingsResponse{Settings: settings, Health: health}), nil
}

func (handler *logManagementHandler) UpdateLogSettings(ctx context.Context, request *connect.Request[managementv1.UpdateLogSettingsRequest]) (*connect.Response[managementv1.UpdateLogSettingsResponse], error) {
	if err := handler.administrator(ctx, request.Header()); err != nil {
		return nil, err
	}
	settings := request.Msg.Settings
	if settings == nil || request.Msg.ExpectedRevision <= 0 {
		return nil, logManagementError(ctx, logging.ErrInvalid)
	}
	snapshot, err := handler.logs.Update(ctx, request.Msg.ExpectedRevision, logging.Settings{Level: settings.Level, MaxFileMiB: int(settings.MaxFileMib), MaxBackups: int(settings.MaxBackups), RetentionDays: int(settings.RetentionDays), Revision: settings.Revision})
	if err != nil {
		return nil, logManagementError(ctx, err)
	}
	policy, health := logManagementView(snapshot)
	return connect.NewResponse(&managementv1.UpdateLogSettingsResponse{Settings: policy, Health: health}), nil
}

func (handler *logManagementHandler) administrator(ctx context.Context, header http.Header) error {
	owner, err := handler.auth.authenticate(ctx, header)
	if err != nil {
		return err
	}
	if !slices.Contains(handler.admins, owner) {
		return connect.NewError(connect.CodePermissionDenied, errors.New("log administration requires an authorized account"))
	}
	return nil
}

func logManagementError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	if errors.Is(err, logging.ErrInvalid) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid logging policy"))
	}
	if errors.Is(err, logging.ErrConflict) {
		return connect.NewError(connect.CodeAborted, errors.New("logging policy revision changed"))
	}
	return connect.NewError(connect.CodeUnavailable, errors.New("logging storage unavailable"))
}

func logManagementView(snapshot logging.Snapshot) (*managementv1.LogSettings, *managementv1.LogHealth) {
	settings := snapshot.Settings
	health := snapshot.Health
	return &managementv1.LogSettings{Level: settings.Level, MaxFileMib: int32(settings.MaxFileMiB), MaxBackups: int32(settings.MaxBackups), RetentionDays: int32(settings.RetentionDays), Revision: settings.Revision}, &managementv1.LogHealth{WriteFailures: health.WriteFailures, CleanupFailures: health.CleanupFailures, DroppedRecords: health.DroppedRecords, Degraded: health.Degraded}
}
