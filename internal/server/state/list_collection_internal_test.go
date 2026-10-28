package state

import (
	"errors"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/server/ent"
)

func TestCollectionObservationsRequireFreshnessEvidence(t *testing.T) {
	for _, memberships := range [][]*ent.GitHubMembership{nil, {{}}, {{Edges: ent.GitHubMembershipEdges{List: &ent.GitHubList{}}}}} {
		entry := &ent.GitHubRepository{Edges: ent.GitHubRepositoryEdges{Memberships: memberships}}
		if _, err := collectionObservations(entry); !errors.Is(err, ErrObservationExpired) {
			t.Fatalf("collection accepted missing permission observation: %v", err)
		}
	}
	observedAt := time.Now().UTC()
	entry := &ent.GitHubRepository{Edges: ent.GitHubRepositoryEdges{Memberships: []*ent.GitHubMembership{{Edges: ent.GitHubMembershipEdges{List: &ent.GitHubList{VerifiedAt: new(observedAt)}}}}}}
	observed, err := collectionObservations(entry)
	if err != nil || len(observed) != 1 || !observed[0].Equal(observedAt) {
		t.Fatalf("collection discarded permission evidence: %v", err)
	}
}
