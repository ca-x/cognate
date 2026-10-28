package httpapi

import (
	"context"
	"errors"
	"sync"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/skillhub/gen/skillhub/import/v1"
	"github.com/ca-x/skillhub/internal/server/auth"
	"github.com/ca-x/skillhub/internal/server/importer"
	"github.com/ca-x/skillhub/internal/server/library"
)

type ImportHandler struct {
	auth       *authHandler
	collection *library.Service
	mutex      sync.Mutex
	closed     bool
	owners     map[string]context.CancelFunc
	active     sync.WaitGroup
}

func NewImportHandler(authentication *auth.Service, collection *library.Service) (*ImportHandler, error) {
	if authentication == nil || collection == nil {
		return nil, errors.New("import preview requires authentication and library")
	}
	return &ImportHandler{auth: &authHandler{service: authentication}, collection: collection, owners: make(map[string]context.CancelFunc)}, nil
}

func (handler *ImportHandler) Close() error {
	handler.mutex.Lock()
	handler.closed = true
	for _, cancel := range handler.owners {
		cancel()
	}
	handler.mutex.Unlock()
	handler.active.Wait()
	return nil
}

func (handler *ImportHandler) PreviewLibraryExport(ctx context.Context, request *connect.Request[importv1.PreviewLibraryExportRequest]) (*connect.Response[importv1.PreviewLibraryExportResponse], error) {
	owner, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if admitted, ok := ctx.Value(importOwnerKey{}).(string); !ok || admitted != owner {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}
	if request.Msg.Format != importv1.ExportFormat_EXPORT_FORMAT_MAGPIE_LIBRARY_JSON || len(request.Msg.ExportData) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid export request"))
	}
	data := request.Msg.ExportData
	request.Msg.ExportData = nil
	defer clear(data)
	if len(data) > importer.MaxMagpieExportBytes {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("export size limit exceeded"))
	}
	preview, err := importer.PreviewMagpie(ctx, data)
	if canceled := contextError(ctx, err); canceled != nil {
		return nil, canceled
	}
	if err != nil || !preview.Found {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("export cannot be previewed"))
	}
	entries, err := handler.collection.List(ctx, owner)
	if err != nil {
		return nil, libraryError(ctx, err)
	}
	existing := make(map[importer.MagpieRepository]string, len(entries))
	for _, entry := range entries {
		existing[importer.MagpieRepository{Repo: entry.NameWithOwner, Ref: entry.Branch}] = entry.ID
	}
	response := &importv1.PreviewLibraryExportResponse{Repositories: make([]*importv1.RepositoryCandidate, 0, len(preview.Repositories)), ReferencedSkillCount: uint32(len(preview.Skills))}
	for _, candidate := range preview.Repositories {
		response.Repositories = append(response.Repositories, &importv1.RepositoryCandidate{Repository: candidate.Repo, Branch: candidate.Ref, ExistingRepositoryId: existing[candidate]})
	}
	if canceled := contextError(ctx, ctx.Err()); canceled != nil {
		return nil, canceled
	}
	return connect.NewResponse(response), nil
}
