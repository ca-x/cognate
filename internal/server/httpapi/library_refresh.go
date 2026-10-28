package httpapi

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	libraryv1 "github.com/ca-x/cognate/gen/skillhub/library/v1"
	"github.com/ca-x/cognate/internal/server/state"
)

func (handler *libraryHandler) RefreshRepository(ctx context.Context, request *connect.Request[libraryv1.RefreshRepositoryRequest]) (*connect.Response[libraryv1.RefreshRepositoryResponse], error) {
	owner, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if !validRefreshRequest(request.Msg) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid repository refresh request"))
	}
	result, err := handler.service.Refresh(ctx, owner, request.Msg.RepositoryId, state.RepositoryVersion{ArchiveHash: request.Msg.ExpectedArchiveHash, PublicationRevision: request.Msg.ExpectedPublicationRevision})
	if err != nil {
		return nil, libraryError(ctx, err)
	}
	response := &libraryv1.RefreshRepositoryResponse{
		Repository: libraryRepository(result.Repository), Changed: result.Changed,
		Added: uint32(result.Added), Updated: uint32(result.Updated), Removed: uint32(result.Removed),
		Packs: make([]*libraryv1.RefreshedPack, 0, len(result.Packs)),
	}
	for _, pack := range result.Packs {
		response.Packs = append(response.Packs, &libraryv1.RefreshedPack{PackId: pack.ID, Revision: pack.Revision})
	}
	return connect.NewResponse(response), nil
}

func validRefreshRequest(request *libraryv1.RefreshRepositoryRequest) bool {
	if request == nil || len(request.RepositoryId) < 1 || len(request.RepositoryId) > 64 || len(request.ExpectedArchiveHash) != 64 || request.ExpectedPublicationRevision < 1 {
		return false
	}
	for _, letter := range request.RepositoryId {
		if !(letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z' || letter >= '0' && letter <= '9' || letter == '_' || letter == '-') {
			return false
		}
	}
	for _, letter := range request.ExpectedArchiveHash {
		if !(letter >= '0' && letter <= '9' || letter >= 'a' && letter <= 'f') {
			return false
		}
	}
	return true
}
