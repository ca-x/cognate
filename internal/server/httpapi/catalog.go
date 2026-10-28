package httpapi

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/gen/skillhub/catalog/v1/catalogv1connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/labstack/echo/v4"
)

func registerCatalog(server *echo.Echo, authentication *authHandler, options Options) {
	if options.Store != nil {
		owner := &catalogOwnerHandler{auth: authentication, store: options.Store}
		path, handler := catalogv1connect.NewCategoryServiceHandler(owner, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(1<<20))
		server.Any(path+"*", echo.WrapHandler(handler))
		path, handler = catalogv1connect.NewPublicationServiceHandler(owner, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(4096))
		server.Any(path+"*", echo.WrapHandler(handler))
	}
	if options.PublicCatalog != nil {
		path, handler := catalogv1connect.NewPublicCatalogServiceHandler(&publicCatalogHandler{service: options.PublicCatalog}, connect.WithReadMaxBytes(4096), connect.WithSendMaxBytes(1<<20), connect.WithInterceptors(distributionDeadlines{}))
		server.Any(path+"*", echo.WrapHandler(distributionRequests(options.PublicCatalog, handler)))
	}
}

type catalogOwnerHandler struct {
	auth  *authHandler
	store *state.Store
}

func (handler *catalogOwnerHandler) ListCategories(ctx context.Context, request *connect.Request[catalogv1.ListCategoriesRequest]) (*connect.Response[catalogv1.ListCategoriesResponse], error) {
	owner, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entries, err := handler.store.ListCategories(ctx, owner)
	if err != nil {
		return nil, catalogError(ctx, err)
	}
	return connect.NewResponse(&catalogv1.ListCategoriesResponse{Categories: categoryMessages(entries)}), nil
}

func (handler *catalogOwnerHandler) CreateCategory(ctx context.Context, request *connect.Request[catalogv1.CreateCategoryRequest]) (*connect.Response[catalogv1.CreateCategoryResponse], error) {
	owner, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entry, err := handler.store.CreateCategory(ctx, owner, request.Msg.Name)
	if err != nil {
		return nil, catalogError(ctx, err)
	}
	return connect.NewResponse(&catalogv1.CreateCategoryResponse{Category: categoryMessage(entry)}), nil
}

func (handler *catalogOwnerHandler) UpdateCategory(ctx context.Context, request *connect.Request[catalogv1.UpdateCategoryRequest]) (*connect.Response[catalogv1.UpdateCategoryResponse], error) {
	owner, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entry, err := handler.store.UpdateCategory(ctx, owner, request.Msg.CategoryId, request.Msg.ExpectedRevision, request.Msg.Name)
	if err != nil {
		return nil, catalogError(ctx, err)
	}
	return connect.NewResponse(&catalogv1.UpdateCategoryResponse{Category: categoryMessage(entry)}), nil
}

func (handler *catalogOwnerHandler) DeleteCategory(ctx context.Context, request *connect.Request[catalogv1.DeleteCategoryRequest]) (*connect.Response[catalogv1.DeleteCategoryResponse], error) {
	owner, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if err := handler.store.DeleteCategory(ctx, owner, request.Msg.CategoryId, request.Msg.ExpectedRevision); err != nil {
		return nil, catalogError(ctx, err)
	}
	return connect.NewResponse(&catalogv1.DeleteCategoryResponse{}), nil
}

func (handler *catalogOwnerHandler) GetSkillCategories(ctx context.Context, request *connect.Request[catalogv1.GetSkillCategoriesRequest]) (*connect.Response[catalogv1.GetSkillCategoriesResponse], error) {
	owner, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entries, err := handler.store.SkillCategories(ctx, owner, request.Msg.SkillId)
	if err != nil {
		return nil, catalogError(ctx, err)
	}
	return connect.NewResponse(&catalogv1.GetSkillCategoriesResponse{Categories: categoryMessages(entries)}), nil
}

func (handler *catalogOwnerHandler) SetSkillCategories(ctx context.Context, request *connect.Request[catalogv1.SetSkillCategoriesRequest]) (*connect.Response[catalogv1.SetSkillCategoriesResponse], error) {
	owner, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entries, err := handler.store.SetSkillCategories(ctx, owner, request.Msg.SkillId, request.Msg.CategoryIds)
	if err != nil {
		return nil, catalogError(ctx, err)
	}
	return connect.NewResponse(&catalogv1.SetSkillCategoriesResponse{Categories: categoryMessages(entries)}), nil
}

func (handler *catalogOwnerHandler) GetPublication(ctx context.Context, request *connect.Request[catalogv1.GetPublicationRequest]) (*connect.Response[catalogv1.GetPublicationResponse], error) {
	owner, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entry, err := handler.store.Publication(ctx, owner, request.Msg.RepositoryId)
	if err != nil {
		return nil, catalogError(ctx, err)
	}
	return connect.NewResponse(&catalogv1.GetPublicationResponse{Publication: publicationMessage(entry)}), nil
}

func (handler *catalogOwnerHandler) SetPublication(ctx context.Context, request *connect.Request[catalogv1.SetPublicationRequest]) (*connect.Response[catalogv1.SetPublicationResponse], error) {
	owner, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entry, err := handler.store.SetPublication(ctx, owner, request.Msg.RepositoryId, request.Msg.ExpectedRevision, request.Msg.Published, request.Msg.ConfirmFullContents)
	if err != nil {
		return nil, catalogError(ctx, err)
	}
	return connect.NewResponse(&catalogv1.SetPublicationResponse{Publication: publicationMessage(entry)}), nil
}

func categoryMessage(entry state.Category) *catalogv1.Category {
	return &catalogv1.Category{Id: entry.ID, Name: entry.Name, SkillCount: uint32(entry.SkillCount), Revision: entry.Revision}
}

func categoryMessages(entries []state.Category) []*catalogv1.Category {
	result := make([]*catalogv1.Category, 0, len(entries))
	for _, entry := range entries {
		result = append(result, categoryMessage(entry))
	}
	return result
}

func publicationMessage(entry state.Publication) *catalogv1.Publication {
	return &catalogv1.Publication{RepositoryId: entry.RepositoryID, Published: entry.Published, Revision: entry.Revision}
}

type publicCatalogHandler struct{ service *publiccatalog.Service }

func (handler *publicCatalogHandler) ListSkills(ctx context.Context, request *connect.Request[catalogv1.ListSkillsRequest]) (*connect.Response[catalogv1.ListSkillsResponse], error) {
	page, err := handler.service.ListSkills(ctx, state.PublicSkillQuery{Query: request.Msg.Query, CategoryID: request.Msg.CategoryId, Limit: request.Msg.Limit, Cursor: request.Msg.Cursor})
	if err != nil {
		return nil, catalogError(ctx, err)
	}
	result := &catalogv1.ListSkillsResponse{Skills: make([]*catalogv1.PublicSkill, 0, len(page.Skills)), NextCursor: page.NextCursor}
	for _, entry := range page.Skills {
		result.Skills = append(result.Skills, publicSkillMessage(entry))
	}
	return connect.NewResponse(result), nil
}

func (handler *publicCatalogHandler) ListCategories(ctx context.Context, request *connect.Request[catalogv1.PublicCatalogServiceListCategoriesRequest]) (*connect.Response[catalogv1.PublicCatalogServiceListCategoriesResponse], error) {
	page, err := handler.service.ListCategories(ctx, request.Msg.Limit, request.Msg.Cursor)
	if err != nil {
		return nil, catalogError(ctx, err)
	}
	return connect.NewResponse(&catalogv1.PublicCatalogServiceListCategoriesResponse{Categories: publicCategoryMessages(page.Categories), NextCursor: page.NextCursor}), nil
}

func (handler *publicCatalogHandler) GetSkill(ctx context.Context, request *connect.Request[catalogv1.GetSkillRequest]) (*connect.Response[catalogv1.GetSkillResponse], error) {
	entry, err := handler.service.GetSkill(ctx, request.Msg.SkillId)
	if err != nil {
		return nil, catalogError(ctx, err)
	}
	return connect.NewResponse(&catalogv1.GetSkillResponse{Skill: publicSkillMessage(entry)}), nil
}

func (handler *publicCatalogHandler) DownloadSkill(ctx context.Context, request *connect.Request[catalogv1.DownloadSkillRequest], stream *connect.ServerStream[packv1.DownloadSkillResponse]) error {
	err := handler.service.Download(ctx, request.Msg.SkillId, request.Msg.ContentHash, request.Msg.PublicationRevision, func(chunk packs.Chunk) error {
		return stream.Send(&packv1.DownloadSkillResponse{Offset: chunk.Offset, Data: chunk.Data, Final: chunk.Final, TransferSha256: chunk.SHA256, ContentHash: chunk.ContentHash})
	})
	if err != nil {
		return catalogError(ctx, err)
	}
	return nil
}

func publicCategoryMessages(entries []state.PublicCategory) []*catalogv1.PublicCategory {
	result := make([]*catalogv1.PublicCategory, 0, len(entries))
	for _, entry := range entries {
		result = append(result, &catalogv1.PublicCategory{Id: entry.ID, Name: entry.Name, SkillCount: uint32(entry.SkillCount)})
	}
	return result
}

func publicSkillMessage(entry state.PublicSkill) *catalogv1.PublicSkill {
	return &catalogv1.PublicSkill{Id: entry.ID, Slug: entry.Slug, Name: entry.Name, Description: entry.Description, ContentHash: entry.ContentHash, RepositoryId: entry.RepositoryID, RepositoryName: entry.RepositoryName, Path: entry.Path, Branch: entry.Branch, PublicationRevision: entry.PublicationRevision, Categories: publicCategoryMessages(entry.Categories)}
}

func catalogError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	switch {
	case errors.Is(err, state.ErrCategoryInvalid), errors.Is(err, state.ErrPublicationInvalid), errors.Is(err, state.ErrPublicCatalogInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid catalog fields, revision, query, limit, or cursor"))
	case errors.Is(err, state.ErrCategoryNotFound):
		return connect.NewError(connect.CodeNotFound, state.ErrCategoryNotFound)
	case errors.Is(err, state.ErrCategorySkillUnavailable):
		return connect.NewError(connect.CodeNotFound, state.ErrCategorySkillUnavailable)
	case errors.Is(err, state.ErrPublicSkillNotFound):
		return connect.NewError(connect.CodeNotFound, state.ErrPublicSkillNotFound)
	case errors.Is(err, state.ErrRepositoryNotFound):
		return connect.NewError(connect.CodeNotFound, state.ErrRepositoryNotFound)
	case errors.Is(err, state.ErrCategoryConflict), errors.Is(err, state.ErrPublicationConflict):
		return connect.NewError(connect.CodeAborted, errors.New("catalog revision changed"))
	case errors.Is(err, state.ErrCategoryExists):
		return connect.NewError(connect.CodeAlreadyExists, state.ErrCategoryExists)
	case errors.Is(err, state.ErrPublicationConfirmation):
		return connect.NewError(connect.CodeFailedPrecondition, state.ErrPublicationConfirmation)
	case errors.Is(err, state.ErrCategoryLimit), errors.Is(err, publiccatalog.ErrBusy):
		return connect.NewError(connect.CodeResourceExhausted, errors.New("catalog capacity exhausted"))
	case errors.Is(err, publiccatalog.ErrClosed):
		return connect.NewError(connect.CodeUnavailable, publiccatalog.ErrClosed)
	default:
		return libraryError(ctx, err)
	}
}
