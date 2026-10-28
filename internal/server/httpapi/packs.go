package httpapi

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	packv1 "github.com/ca-x/skillhub/gen/skillhub/pack/v1"
	"github.com/ca-x/skillhub/internal/server/packs"
	"github.com/ca-x/skillhub/internal/server/state"
)

type packHandler struct {
	auth    *authHandler
	store   *state.Store
	service *packs.Service
}

func (handler *packHandler) CreatePack(ctx context.Context, request *connect.Request[packv1.CreatePackRequest]) (*connect.Response[packv1.CreatePackResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	pack, err := handler.store.CreatePack(ctx, user, packDraft(request.Msg.Draft))
	if err != nil {
		return nil, packError(ctx, err)
	}
	return connect.NewResponse(&packv1.CreatePackResponse{Pack: packMessage(pack)}), nil
}

func (handler *packHandler) UpdatePack(ctx context.Context, request *connect.Request[packv1.UpdatePackRequest]) (*connect.Response[packv1.UpdatePackResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	pack, err := handler.store.UpdatePack(ctx, user, request.Msg.PackId, request.Msg.ExpectedRevision, packDraft(request.Msg.Draft))
	if err != nil {
		return nil, packError(ctx, err)
	}
	return connect.NewResponse(&packv1.UpdatePackResponse{Pack: packMessage(pack)}), nil
}

func (handler *packHandler) ListPacks(ctx context.Context, request *connect.Request[packv1.ListPacksRequest]) (*connect.Response[packv1.ListPacksResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entries, err := handler.store.ListPacks(ctx, user)
	if err != nil {
		return nil, packError(ctx, err)
	}
	result := &packv1.ListPacksResponse{Packs: make([]*packv1.Pack, 0, len(entries))}
	for _, entry := range entries {
		result.Packs = append(result.Packs, packMessage(entry))
	}
	return connect.NewResponse(result), nil
}

func (handler *packHandler) GetPack(ctx context.Context, request *connect.Request[packv1.GetPackRequest]) (*connect.Response[packv1.GetPackResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entry, err := handler.store.Pack(ctx, user, request.Msg.PackId)
	if err != nil {
		return nil, packError(ctx, err)
	}
	return connect.NewResponse(&packv1.GetPackResponse{Pack: packMessage(entry)}), nil
}

func (handler *packHandler) DeletePack(ctx context.Context, request *connect.Request[packv1.DeletePackRequest]) (*connect.Response[packv1.DeletePackResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if err := handler.store.DeletePack(ctx, user, request.Msg.PackId, request.Msg.ExpectedRevision); err != nil {
		return nil, packError(ctx, err)
	}
	return connect.NewResponse(&packv1.DeletePackResponse{}), nil
}

func (handler *packHandler) CreateShare(ctx context.Context, request *connect.Request[packv1.CreateShareRequest]) (*connect.Response[packv1.CreateShareResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	var expiry *time.Time
	if request.Msg.ExpiresAtUnix != 0 {
		expiry = new(time.Unix(request.Msg.ExpiresAtUnix, 0))
	}
	issued, err := handler.service.CreateShare(ctx, user, request.Msg.PackId, expiry)
	if err != nil {
		return nil, packError(ctx, err)
	}
	return connect.NewResponse(&packv1.CreateShareResponse{Share: shareMessage(issued.Info), ShareToken: issued.Token.Reveal()}), nil
}

func (handler *packHandler) ListShares(ctx context.Context, request *connect.Request[packv1.ListSharesRequest]) (*connect.Response[packv1.ListSharesResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	entries, err := handler.store.ListPackShares(ctx, user, request.Msg.PackId)
	if err != nil {
		return nil, packError(ctx, err)
	}
	result := &packv1.ListSharesResponse{Shares: make([]*packv1.Share, 0, len(entries))}
	for _, entry := range entries {
		result.Shares = append(result.Shares, shareMessage(entry))
	}
	return connect.NewResponse(result), nil
}

func (handler *packHandler) RevokeShare(ctx context.Context, request *connect.Request[packv1.RevokeShareRequest]) (*connect.Response[packv1.RevokeShareResponse], error) {
	user, err := handler.auth.authenticate(ctx, request.Header())
	if err != nil {
		return nil, err
	}
	if err := handler.store.RevokePackShare(ctx, user, request.Msg.PackId, request.Msg.ShareId); err != nil {
		return nil, packError(ctx, err)
	}
	return connect.NewResponse(&packv1.RevokeShareResponse{}), nil
}

func packDraft(input *packv1.PackDraft) state.PackDraft {
	if input == nil {
		return state.PackDraft{}
	}
	result := state.PackDraft{Name: input.Name, Description: input.Description, Items: make([]state.PackSelection, 0, len(input.Items))}
	for _, item := range input.Items {
		result.Items = append(result.Items, state.PackSelection{SkillID: item.GetSkillId(), Slug: item.GetSlug()})
	}
	return result
}

func packMessage(entry state.Pack) *packv1.Pack {
	return &packv1.Pack{Id: entry.ID, Name: entry.Name, Description: entry.Description, Revision: entry.Revision, ItemCount: uint32(entry.ItemCount), CreatedAtUnix: entry.CreatedAt.Unix(), UpdatedAtUnix: entry.UpdatedAt.Unix(), Items: packSkills(entry.Items)}
}

func packSkills(entries []state.PackSkill) []*packv1.PackSkill {
	result := make([]*packv1.PackSkill, 0, len(entries))
	for _, entry := range entries {
		result = append(result, &packv1.PackSkill{SkillId: entry.SkillID, Slug: entry.Slug, Name: entry.Name, Description: entry.Description, ContentHash: entry.ContentHash, RepositoryId: entry.RepositoryID, RepositoryName: entry.RepositoryName, Path: entry.Path})
	}
	return result
}

func shareMessage(entry state.PackShare) *packv1.Share {
	result := &packv1.Share{Id: entry.ID, PackId: entry.PackID, CreatedAtUnix: entry.CreatedAt.Unix()}
	if entry.ExpiresAt != nil {
		result.ExpiresAtUnix = entry.ExpiresAt.Unix()
	}
	if entry.RevokedAt != nil {
		result.RevokedAtUnix = entry.RevokedAt.Unix()
	}
	return result
}

func packError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	switch {
	case errors.Is(err, state.ErrPackNotFound):
		return connect.NewError(connect.CodeNotFound, state.ErrPackNotFound)
	case errors.Is(err, state.ErrPackInvalid), errors.Is(err, state.ErrPackSlugConflict):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("check pack fields and portable aliases; different content needs different aliases"))
	case errors.Is(err, state.ErrPackConflict):
		return connect.NewError(connect.CodeAborted, state.ErrPackConflict)
	case errors.Is(err, state.ErrPackShareUnavailable):
		return connect.NewError(connect.CodeUnauthenticated, state.ErrPackShareUnavailable)
	case errors.Is(err, state.ErrPackSkillUnavailable):
		return connect.NewError(connect.CodeFailedPrecondition, state.ErrPackSkillUnavailable)
	case errors.Is(err, state.ErrPackLimit), errors.Is(err, packs.ErrBusy):
		return connect.NewError(connect.CodeResourceExhausted, errors.New("pack capacity exhausted"))
	case errors.Is(err, packs.ErrClosed):
		return connect.NewError(connect.CodeUnavailable, packs.ErrClosed)
	default:
		return libraryError(ctx, err)
	}
}
