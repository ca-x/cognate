package state

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
	"github.com/ca-x/cognate/internal/server/ent/githubrepository"
	"github.com/ca-x/cognate/internal/server/ent/user"
)

var ErrSourceRefreshUnavailable = errors.New("source refresh authorization is unavailable or changed")

type SourceRefreshCursor struct {
	AfterOwnerID int
	Slot         int
}
type sourceRefreshTarget struct {
	issuer              *storeState
	ownerID             int
	owner, repositoryID string
}
type SourceRefreshTarget struct{ target *sourceRefreshTarget }
type SourceRefreshItem struct {
	Target SourceRefreshTarget
	Next   SourceRefreshCursor
}
type SourceRefreshBatch struct {
	Items []SourceRefreshItem
	Next  SourceRefreshCursor
}
type SourceRefreshTicket struct{ proof *sourceRefreshProof }
type sourceRefreshProof struct {
	access   sourceAccessCapability
	issuer   *storeState
	ownerID  int
	owner    string
	snapshot RepositoryRefreshSnapshot
	star     *sourceRefreshStar
}

type sourceRefreshStar struct {
	private                                           bool
	credentialID, repositoryID                        int
	viewer, generation, keyID, fetchID, repositoryKey string
	sequence, applied                                 int64
	expiresAt                                         time.Time
}

func sameSourceRefreshStar(first, second *sourceRefreshStar) bool {
	left, right := *first, *second
	sameExpiry := left.expiresAt.Equal(right.expiresAt)
	left.expiresAt, right.expiresAt = time.Time{}, time.Time{}
	return sameExpiry && left == right
}

func (ticket SourceRefreshTicket) Deadline() time.Time {
	if ticket.proof != nil && ticket.proof.access.get() != nil && ticket.proof.access.get().credentialID != 0 {
		return currentDeadline(ticket.proof.access.get())
	}
	if ticket.proof == nil || ticket.proof.star == nil {
		return time.Time{}
	}
	return ticket.proof.star.expiresAt
}

func (ticket SourceRefreshTicket) Access() SourceAccessTicket {
	if ticket.proof == nil {
		return SourceAccessTicket{}
	}
	return SourceAccessTicket{proof: ticket.proof.access}
}

func (SourceRefreshTarget) Format(destination fmt.State, _ rune) {
	_, _ = io.WriteString(destination, "[source refresh target]")
}
func (SourceRefreshTicket) Format(destination fmt.State, _ rune) {
	_, _ = io.WriteString(destination, "[source refresh ticket]")
}

func (ticket SourceRefreshTicket) Snapshot() RepositoryRefreshSnapshot {
	if ticket.proof == nil {
		return RepositoryRefreshSnapshot{}
	}
	result := ticket.proof.snapshot
	result.Repository.Skills = slices.Clone(result.Repository.Skills)
	return result
}

func (store *Store) ListSourceRefreshBatch(ctx context.Context, cursor SourceRefreshCursor, limit int) (SourceRefreshBatch, error) {
	if cursor.AfterOwnerID < 0 || cursor.Slot < 0 || cursor.Slot >= 1000 || limit < 1 || limit > 16 {
		return SourceRefreshBatch{}, ErrSourceRefreshUnavailable
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return SourceRefreshBatch{}, err
	}
	defer tx.Rollback()
	query := tx.User.Query().Where(user.HasCollectedRepositories()).Order(ent.Asc(user.FieldID)).Limit(limit)
	owners, err := query.Clone().Where(user.IDGT(cursor.AfterOwnerID)).All(ctx)
	if err != nil {
		return SourceRefreshBatch{}, err
	}
	if len(owners) == 0 && cursor.AfterOwnerID != 0 {
		cursor = SourceRefreshCursor{Slot: (cursor.Slot + 1) % 1000}
		owners, err = query.All(ctx)
		if err != nil {
			return SourceRefreshBatch{}, err
		}
	}
	result := SourceRefreshBatch{Next: cursor}
	for _, owner := range owners {
		repositories := tx.CollectedRepository.Query().Where(collectedrepository.UserIDEQ(owner.ID))
		count, err := repositories.Clone().Count(ctx)
		if err != nil {
			return SourceRefreshBatch{}, err
		}
		if count < 1 || count > 1000 {
			return SourceRefreshBatch{}, ErrSourceRefreshUnavailable
		}
		entry, err := repositories.Order(ent.Asc(collectedrepository.FieldID)).Offset(cursor.Slot % count).First(ctx)
		if err != nil {
			return SourceRefreshBatch{}, err
		}
		result.Next = SourceRefreshCursor{AfterOwnerID: owner.ID, Slot: cursor.Slot}
		result.Items = append(result.Items, SourceRefreshItem{Target: SourceRefreshTarget{target: &sourceRefreshTarget{issuer: store.state, ownerID: owner.ID, owner: owner.UserKey, repositoryID: entry.PublicID}}, Next: result.Next})
	}
	if err := tx.Commit(); err != nil {
		return SourceRefreshBatch{}, err
	}
	return result, nil
}
func (store *Store) BeginSourceRefresh(ctx context.Context, target SourceRefreshTarget) (SourceRefreshTicket, error) {
	if target.target == nil || target.target.issuer != store.state {
		return SourceRefreshTicket{}, ErrSourceRefreshUnavailable
	}
	ticket, err := store.BeginRepositorySourceRefresh(ctx, target.target.owner, target.target.repositoryID)
	if err != nil {
		return SourceRefreshTicket{}, err
	}
	if ticket.proof.ownerID != target.target.ownerID {
		return SourceRefreshTicket{}, ErrSourceRefreshUnavailable
	}
	return ticket, nil
}

func (store *Store) BeginRepositorySourceRefresh(ctx context.Context, owner, id string) (SourceRefreshTicket, error) {
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return SourceRefreshTicket{}, err
	}
	defer tx.Rollback()
	entry, err := ownerPublication(ctx, tx, owner, id)
	if err != nil {
		return SourceRefreshTicket{}, err
	}
	detail, err := refreshRepositoryResult(ctx, tx, entry)
	if err != nil {
		return SourceRefreshTicket{}, err
	}
	proof := &sourceRefreshProof{issuer: store.state, ownerID: entry.UserID, owner: owner, snapshot: RepositoryRefreshSnapshot{Repository: detail, Published: entry.Published, Version: RepositoryVersion{ArchiveHash: entry.ArchiveHash, PublicationRevision: entry.PublicationRevision}}}
	if !entry.Manual {
		proof.star, err = store.readSourceRefreshStar(ctx, tx, proof.owner, entry, 0, false)
		if err != nil {
			return SourceRefreshTicket{}, err
		}
	}
	access, err := store.readSourceAccess(ctx, tx, owner, repository.Reference{NameWithOwner: entry.NameWithOwner, Branch: entry.Branch}, proof.star)
	if err != nil {
		return SourceRefreshTicket{}, err
	}
	proof.access = sourceAccessCapabilityFor(access)
	if err := tx.Commit(); err != nil {
		return SourceRefreshTicket{}, err
	}
	return SourceRefreshTicket{proof: proof}, nil
}
func (store *Store) CheckSourceRefresh(ctx context.Context, ticket SourceRefreshTicket) error {
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := store.checkSourceRefreshProof(ctx, tx, ticket.proof, true, false); err != nil {
		return err
	}
	return tx.Commit()
}
func (store *Store) ApplySourceRefresh(ctx context.Context, ticket SourceRefreshTicket, hash string, skills []repository.ArchivedSkill) (RepositoryRefreshResult, error) {
	if ticket.proof == nil || ticket.proof.issuer != store.state {
		return RepositoryRefreshResult{}, ErrSourceRefreshUnavailable
	}
	ctx = context.WithValue(ctx, sourceRefreshContextKey{}, ticket)
	if ctx.Value(sourceAccessContextKey{}) == nil {
		ctx = store.WithSourceAccess(ctx, ticket.Access())
	} else {
		accessTicket, valid := ctx.Value(sourceAccessContextKey{}).(SourceAccessTicket)
		access := accessTicket.proof.get()
		if !valid || access == nil || ticket.proof.access.get() == nil || access.origin != ticket.proof.access.get().origin {
			return RepositoryRefreshResult{}, ErrSourceAccessUnavailable
		}
	}
	return store.RefreshCollectedRepository(ctx, ticket.proof.owner, ticket.proof.snapshot.Repository.ID, ticket.proof.snapshot.Version, hash, skills)
}

type sourceRefreshContextKey struct{}

func (store *Store) checkSourceRefreshProof(ctx context.Context, tx *ent.Tx, proof *sourceRefreshProof, checkVersion, lock bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if proof == nil || proof.issuer != store.state {
		return ErrSourceRefreshUnavailable
	}
	entry, err := ownerPublication(ctx, tx, proof.owner, proof.snapshot.Repository.ID)
	if err != nil {
		return err
	}
	if entry.UserID != proof.ownerID || entry.NameWithOwner != proof.snapshot.Repository.NameWithOwner || entry.Branch != proof.snapshot.Repository.Branch || entry.Manual != proof.snapshot.Repository.Manual {
		return ErrSourceRefreshUnavailable
	}
	if checkVersion && (entry.ArchiveHash != proof.snapshot.Version.ArchiveHash || entry.PublicationRevision != proof.snapshot.Version.PublicationRevision) {
		return ErrRepositoryRefreshConflict
	}
	if entry.Manual {
		if proof.star != nil {
			return ErrSourceRefreshUnavailable
		}
		return store.checkSourceAccess(ctx, tx, proof.access.get(), false)
	}
	if proof.star == nil {
		return ErrSourceRefreshUnavailable
	}
	current, err := store.readSourceRefreshStar(ctx, tx, proof.owner, entry, proof.star.repositoryID, lock)
	if err != nil {
		return err
	}
	if !sameSourceRefreshStar(current, proof.star) {
		return ErrSourceRefreshUnavailable
	}
	return nil
}

func (store *Store) readSourceRefreshStar(ctx context.Context, tx *ent.Tx, ownerKey string, entry *ent.CollectedRepository, repositoryID int, lock bool) (*sourceRefreshStar, error) {
	if entry.Branch != "" {
		return nil, ErrSourceRefreshUnavailable
	}
	owner, credential, token, err := store.catalogOwner(ctx, tx.Client(), ownerKey)
	if lock {
		owner, credential, token, err = store.lockCatalogOwner(ctx, tx, ownerKey)
	}
	if err != nil {
		return nil, err
	}
	if owner.ID != entry.UserID || credential.SyncSequence < 1 || credential.SyncAppliedSequence != credential.SyncSequence || credential.SyncFetchID == "" {
		return nil, ErrSourceRefreshUnavailable
	}
	memberships := store.collectionMemberships(ListSyncResult{ticket: ListSyncTicket{userID: owner.ID, userKey: ownerKey, viewerID: credential.ViewerID, generation: credential.Generation, sequence: credential.SyncSequence}})
	query := tx.GitHubRepository.Query().Where(githubrepository.UserIDEQ(owner.ID), githubrepository.ViewerIDEQ(credential.ViewerID), githubrepository.NameWithOwnerEqualFold(entry.NameWithOwner), githubrepository.HasMembershipsWith(memberships...)).WithMemberships(func(query *ent.GitHubMembershipQuery) { query.Where(memberships...).WithList() })
	if repositoryID != 0 {
		query.Where(githubrepository.IDEQ(repositoryID))
	}
	remote, err := query.Only(ctx)
	if ent.IsNotFound(err) || ent.IsNotSingular(err) {
		return nil, ErrSourceRefreshUnavailable
	}
	if err != nil {
		return nil, err
	}
	observed, err := collectionObservations(remote)
	if err != nil {
		return nil, err
	}
	if err := store.checkCatalogFresh(token, observed); err != nil {
		return nil, err
	}
	expiresAt, _ := token.ExpiresAt()
	return &sourceRefreshStar{private: remote.Private, credentialID: credential.ID, repositoryID: remote.ID, viewer: credential.ViewerID, generation: credential.Generation, keyID: credential.KeyID, fetchID: credential.SyncFetchID, repositoryKey: remote.RepositoryKey, sequence: credential.SyncSequence, applied: credential.SyncAppliedSequence, expiresAt: expiresAt}, nil
}

func (store *Store) guardSourceRefreshTransaction(ctx context.Context, tx *ent.Tx) error {
	value := ctx.Value(sourceRefreshContextKey{})
	if value == nil {
		return store.guardSourceAccessTransaction(ctx, tx)
	}
	ticket, valid := value.(SourceRefreshTicket)
	proof := ticket.proof
	if !valid {
		return ErrSourceRefreshUnavailable
	}
	if err := store.checkSourceRefreshProof(ctx, tx, proof, true, true); err != nil {
		return err
	}
	if err := store.guardSourceAccessTransaction(ctx, tx); err != nil {
		return err
	}
	tx.OnCommit(func(next ent.Committer) ent.Committer {
		return ent.CommitFunc(func(commitContext context.Context, transaction *ent.Tx) error {
			if err := store.checkSourceRefreshProof(commitContext, transaction, proof, false, false); err != nil {
				return err
			}
			return next.Commit(commitContext, transaction)
		})
	})
	return nil
}
