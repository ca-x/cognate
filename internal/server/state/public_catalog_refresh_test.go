package state

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
)

func TestAuthorizePublicSkillRefreshSnapshotCoherence(test *testing.T) {
	for _, requestedRevision := range []int64{2, 5} {
		test.Run(map[int64]string{2: "old-coherent", 5: "reject-mixed"}[requestedRevision], func(test *testing.T) {
			store, peer := refreshStores(test)
			base := refreshCollect(test, store, "org/source", refreshSkill("skills/shared", "b"))
			if _, err := store.SetPublication(test.Context(), "alice", base.Repository.ID, 1, true, true); err != nil {
				test.Fatal(err)
			}
			interleaved := false
			var competingError error
			store.state.client.CollectedRepository.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
					if !interleaved {
						interleaved = true
						if _, competingError = peer.SetPublication(ctx, "alice", base.Repository.ID, 2, false, false); competingError != nil {
							return nil, competingError
						}
						if _, competingError = peer.RefreshCollectedRepository(ctx, "alice", base.Repository.ID, RepositoryVersion{ArchiveHash: base.Version.ArchiveHash, PublicationRevision: 3}, strings.Repeat("c", 64), []repository.ArchivedSkill{refreshSkill("skills/shared", "d")}); competingError != nil {
							return nil, competingError
						}
						if _, competingError = peer.SetPublication(ctx, "alice", base.Repository.ID, 4, true, true); competingError != nil {
							return nil, competingError
						}
					}
					return next.Query(ctx, query)
				})
			}))
			result, err := store.AuthorizePublicSkill(test.Context(), base.Repository.Skills[0].ID, base.Repository.Skills[0].ContentHash, requestedRevision)
			if !interleaved || competingError != nil {
				test.Fatalf("competing commits missing: %v", competingError)
			}
			if requestedRevision == 2 {
				if err != nil || result.ArchiveHash != base.Version.ArchiveHash || result.ContentHash != base.Repository.Skills[0].ContentHash {
					test.Fatalf("torn read instead of original snapshot: %+v %v", result, err)
				}
			} else if !errors.Is(err, ErrPublicationConflict) || result != (PublicSkillArchive{}) {
				test.Fatalf("accepted mixed old skill/new archive: %+v %v", result, err)
			}
			if _, err := store.AuthorizePublicSkill(test.Context(), base.Repository.Skills[0].ID, base.Repository.Skills[0].ContentHash, 2); !errors.Is(err, ErrPublicationConflict) {
				test.Fatalf("fresh authorization accepted old generation: %v", err)
			}
			if _, err := store.AuthorizePublicSkill(test.Context(), base.Repository.Skills[0].ID, strings.Repeat("d", 64), 5); err != nil {
				test.Fatal(err)
			}
		})
	}
}
