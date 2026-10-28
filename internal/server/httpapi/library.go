package httpapi

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	libraryv1 "github.com/ca-x/skillhub/gen/skillhub/library/v1"
	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/library"
	"github.com/ca-x/skillhub/internal/server/state"
)

type libraryHandler struct {
	auth    *authHandler
	service *library.Service
}

func (handler *libraryHandler) CollectRepository(ctx context.Context, request *connect.Request[libraryv1.CollectRepositoryRequest]) (*connect.Response[libraryv1.CollectRepositoryResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entry, err := handler.service.Collect(ctx, user, request.Msg.Repository, request.Msg.Branch)
	if err != nil {
		return nil, libraryError(ctx, err)
	}
	return connect.NewResponse(&libraryv1.CollectRepositoryResponse{Repository: libraryRepository(entry)}), nil
}

func (handler *libraryHandler) ListRepositories(ctx context.Context, request *connect.Request[libraryv1.ListRepositoriesRequest]) (*connect.Response[libraryv1.ListRepositoriesResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entries, err := handler.service.List(ctx, user)
	if err != nil {
		return nil, libraryError(ctx, err)
	}
	response := &libraryv1.ListRepositoriesResponse{Repositories: make([]*libraryv1.Repository, 0, len(entries))}
	for _, entry := range entries {
		response.Repositories = append(response.Repositories, libraryRepository(entry))
	}
	return connect.NewResponse(response), nil
}

func (handler *libraryHandler) GetRepository(ctx context.Context, request *connect.Request[libraryv1.GetRepositoryRequest]) (*connect.Response[libraryv1.GetRepositoryResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entry, err := handler.service.Get(ctx, user, request.Msg.RepositoryId)
	if err != nil {
		return nil, libraryError(ctx, err)
	}
	response := &libraryv1.GetRepositoryResponse{Repository: libraryRepository(entry), Skills: make([]*libraryv1.Skill, 0, len(entry.Skills))}
	for _, entrySkill := range entry.Skills {
		response.Skills = append(response.Skills, &libraryv1.Skill{Id: entrySkill.ID, Name: entrySkill.Name, Description: entrySkill.Description, Path: entrySkill.Path, ContentHash: entrySkill.ContentHash})
	}
	return connect.NewResponse(response), nil
}

func libraryRepository(entry state.CollectedRepository) *libraryv1.Repository {
	return &libraryv1.Repository{Id: entry.ID, NameWithOwner: entry.NameWithOwner, Branch: entry.Branch, ArchiveHash: entry.ArchiveHash, SkillCount: uint32(entry.SkillCount), Manual: entry.Manual, CollectedAtUnix: entry.CollectedAt.Unix()}
}

func libraryError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	switch {
	case errors.Is(err, repository.ErrInvalidReference):
		return connect.NewError(connect.CodeInvalidArgument, repository.ErrInvalidReference)
	case errors.Is(err, state.ErrRepositoryNotFound), errors.Is(err, repository.ErrArchiveUnavailable):
		return connect.NewError(connect.CodeNotFound, errors.New("repository unavailable; verify public repository and branch"))
	case errors.Is(err, repository.ErrArchiveInvalid):
		return connect.NewError(connect.CodeFailedPrecondition, repository.ErrArchiveInvalid)
	case errors.Is(err, library.ErrArchiveMissing):
		return connect.NewError(connect.CodeDataLoss, library.ErrArchiveMissing)
	case errors.Is(err, library.ErrCapacity), errors.Is(err, library.ErrCacheFull), errors.Is(err, state.ErrLibraryLimit), errors.Is(err, repository.ErrArchiveTooLarge), errors.Is(err, repository.ErrArchiveRateLimited):
		return connect.NewError(connect.CodeResourceExhausted, errors.New("repository collection capacity or size limit exceeded; retry later or choose a smaller repository"))
	case errors.Is(err, library.ErrClosed):
		return connect.NewError(connect.CodeUnavailable, errors.New("repository library is shutting down"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("repository collection unavailable"))
	}
}
