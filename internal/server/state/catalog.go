package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/githubcredential"
	"github.com/ca-x/cognate/internal/server/ent/githublist"
	"github.com/ca-x/cognate/internal/server/ent/githubmembership"
	"github.com/ca-x/cognate/internal/server/ent/githubrepository"
)

var ErrObservationExpired = errors.New("GitHub catalog observation expired")

type GitHubList struct {
	ID          string
	Name        string
	Slug        string
	Private     bool
	Subscribed  bool
	Visible     bool
	UpdatedAt   time.Time
	LastAddedAt time.Time
	VerifiedAt  time.Time
}

type GitHubRepository struct {
	ID            string
	NameWithOwner string
	DatabaseID    *int64
	Private       bool
}

type CatalogSnapshot struct {
	ViewerID     string
	Generation   string
	Lists        []GitHubList
	Repositories []GitHubRepository
	ValidUntil   time.Time
}

func (store *Store) ApplyListSync(ctx context.Context, result ListSyncResult) error {
	return store.CommitListSync(ctx, result, func(ctx context.Context, tx *ent.Tx, snapshot github.ListsSnapshot) error {
		return store.applyListSnapshot(ctx, tx, result.ticket, snapshot)
	})
}

func (store *Store) applyListSnapshot(ctx context.Context, tx *ent.Tx, ticket ListSyncTicket, snapshot github.ListsSnapshot) error {
	verifiedAt := snapshot.ObservedAt()
	if verifiedAt.IsZero() {
		return fmt.Errorf("GitHub observation time is unavailable")
	}
	lists, repositories, err := snapshotRecords(ctx, tx, ticket, snapshot)
	if err != nil {
		return err
	}
	if _, err := tx.GitHubList.Update().Where(githublist.UserIDEQ(ticket.userID), githublist.ViewerIDEQ(ticket.viewerID), githublist.VisibleEQ(true)).SetVisible(false).Save(ctx); err != nil {
		return fmt.Errorf("mark GitHub lists unobserved: %w", err)
	}
	observed, names := map[string]github.ListRepository{}, map[string]string{}
	for _, item := range snapshot.Lists {
		entry, exists := lists[item.ID]
		listVerifiedAt := verifiedAt
		if item.ItemsUnchanged {
			if !exists || !store.reusableList(entry, ticket) || !sameListWatermark(listWatermark(entry), item) {
				return ErrSyncSuperseded
			}
			listVerifiedAt = *entry.ItemsVerifiedAt
		}
		if !exists {
			entry, err = tx.GitHubList.Create().SetUserID(ticket.userID).SetViewerID(ticket.viewerID).SetListKey(item.ID).SetCredentialGeneration(ticket.generation).SetName(item.Name).SetSlug(item.Slug).SetPrivate(item.Private).SetUpdatedAt(item.UpdatedAt).SetLastAddedAt(item.LastAddedAt).SetVerifiedAt(verifiedAt).SetVisible(true).Save(ctx)
		} else {
			entry, err = tx.GitHubList.UpdateOneID(entry.ID).SetCredentialGeneration(ticket.generation).SetName(item.Name).SetSlug(item.Slug).SetPrivate(item.Private).SetUpdatedAt(item.UpdatedAt).SetLastAddedAt(item.LastAddedAt).SetVerifiedAt(listVerifiedAt).SetVisible(true).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("save GitHub list: %w", err)
		}
		if !entry.Subscribed || item.ItemsUnchanged {
			continue
		}
		if err := tx.GitHubList.UpdateOneID(entry.ID).SetItemsVerifiedAt(verifiedAt).Exec(ctx); err != nil {
			return fmt.Errorf("save GitHub list watermark: %w", err)
		}
		if _, err := tx.GitHubMembership.Update().Where(githubmembership.ListIDEQ(entry.ID), githubmembership.VisibleEQ(true)).SetVisible(false).Save(ctx); err != nil {
			return fmt.Errorf("mark GitHub list members unobserved: %w", err)
		}
		for _, repository := range item.Repositories {
			if previous, exists := observed[repository.ID]; exists && !sameRepository(previous, repository) {
				return fmt.Errorf("GitHub repository metadata changed during synchronization")
			}
			name := strings.ToLower(repository.NameWithOwner)
			if previous, exists := names[name]; exists && previous != repository.ID {
				return fmt.Errorf("GitHub repository name maps to multiple identities")
			}
			observed[repository.ID], names[name] = repository, repository.ID
			record, exists := repositories[repository.ID]
			if exists {
				if err := rememberPrivateRepository(ctx, tx, ticket.userID, record.NameWithOwner, record.Private); err != nil {
					return err
				}
			}
			if err := rememberPrivateRepository(ctx, tx, ticket.userID, repository.NameWithOwner, repository.Private); err != nil {
				return err
			}
			if !exists {
				record, err = tx.GitHubRepository.Create().SetUserID(ticket.userID).SetViewerID(ticket.viewerID).SetRepositoryKey(repository.ID).SetNameWithOwner(repository.NameWithOwner).SetPrivate(repository.Private).SetNillableDatabaseID(repository.DatabaseID).Save(ctx)
			} else {
				update := tx.GitHubRepository.UpdateOneID(record.ID).SetNameWithOwner(repository.NameWithOwner).SetPrivate(repository.Private)
				if repository.DatabaseID == nil {
					update.ClearDatabaseID()
				} else {
					update.SetDatabaseID(*repository.DatabaseID)
				}
				record, err = update.Save(ctx)
			}
			if err != nil {
				return fmt.Errorf("save GitHub repository: %w", err)
			}
			repositories[repository.ID] = record
			membership, err := tx.GitHubMembership.Query().Where(githubmembership.ListIDEQ(entry.ID), githubmembership.RepositoryIDEQ(record.ID)).Only(ctx)
			if ent.IsNotFound(err) {
				err = tx.GitHubMembership.Create().SetListID(entry.ID).SetRepositoryID(record.ID).SetVisible(true).Exec(ctx)
			} else if err == nil {
				err = tx.GitHubMembership.UpdateOneID(membership.ID).SetVisible(true).Exec(ctx)
			}
			if err != nil {
				return fmt.Errorf("save GitHub list membership: %w", err)
			}
		}
	}
	if _, err := tx.GitHubList.Update().Where(githublist.UserIDEQ(ticket.userID), githublist.ViewerIDEQ(ticket.viewerID), githublist.VisibleEQ(false)).ClearItemsVerifiedAt().Save(ctx); err != nil {
		return fmt.Errorf("invalidate missing GitHub list watermarks: %w", err)
	}
	return nil
}

func sameRepository(first, second github.ListRepository) bool {
	if first.ID != second.ID || first.NameWithOwner != second.NameWithOwner || first.Private != second.Private || (first.DatabaseID == nil) != (second.DatabaseID == nil) {
		return false
	}
	return first.DatabaseID == nil || *first.DatabaseID == *second.DatabaseID
}

func (store *Store) SetListSubscription(ctx context.Context, userKey, listID string, subscribed bool) error {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return admissionError
	}
	defer releaseWriter()
	if err := validateIdentifier(userKey); err != nil {
		return err
	}
	if err := validateIdentifier(listID); err != nil {
		return err
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start list subscription transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	owner, credential, token, err := store.catalogOwner(ctx, tx.Client(), userKey)
	if err != nil {
		return err
	}
	now := store.state.now().UTC()
	list, err := tx.GitHubList.Query().Where(githublist.UserIDEQ(owner.ID), githublist.ViewerIDEQ(credential.ViewerID), githublist.CredentialGenerationEQ(credential.Generation), githublist.VerifiedAtGT(now.Add(-store.state.observationMaxAge)), githublist.VerifiedAtLTE(now), githublist.ListKeyEQ(listID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("read list subscription: %w", err)
	}
	if list.VerifiedAt == nil {
		return ErrObservationExpired
	}
	if list.Subscribed == subscribed {
		if err := store.checkCatalogFresh(token, []time.Time{*list.VerifiedAt}); err != nil {
			return err
		}
		return tx.Commit()
	}
	count, err := tx.GitHubCredential.Update().Where(githubcredential.IDEQ(credential.ID), githubcredential.GenerationEQ(credential.Generation), githubcredential.StatusEQ(githubcredential.StatusReady), githubcredential.KeyIDEQ(store.state.keyID), githubcredential.SyncSequenceLT(math.MaxInt64)).AddSyncSequence(1).SetSyncFetchID("").Save(ctx)
	if err != nil {
		return fmt.Errorf("invalidate pending GitHub synchronization: %w", err)
	}
	if count != 1 {
		return ErrSyncSuperseded
	}
	count, err = tx.GitHubList.Update().Where(githublist.IDEQ(list.ID), githublist.SubscribedEQ(list.Subscribed)).SetSubscribed(subscribed).ClearItemsVerifiedAt().Save(ctx)
	if err != nil {
		return fmt.Errorf("update list subscription: %w", err)
	}
	if count != 1 {
		return ErrSyncSuperseded
	}
	if _, err := tx.GitHubMembership.Update().Where(githubmembership.ListIDEQ(list.ID), githubmembership.VisibleEQ(true)).SetVisible(false).Save(ctx); err != nil {
		return fmt.Errorf("invalidate prior list members: %w", err)
	}
	if err := store.checkCatalogFresh(token, []time.Time{*list.VerifiedAt}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("list subscription commit outcome may be uncertain: %w", err)
	}
	return nil
}

func (store *Store) GitHubLists(ctx context.Context, userKey string) ([]GitHubList, error) {
	catalog, err := store.GitHubCatalog(ctx, userKey)
	return catalog.Lists, err
}

func (store *Store) SubscribedRepositories(ctx context.Context, userKey string) ([]GitHubRepository, error) {
	catalog, err := store.GitHubCatalog(ctx, userKey)
	return catalog.Repositories, err
}

func (store *Store) GitHubCatalog(ctx context.Context, userKey string) (CatalogSnapshot, error) {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return CatalogSnapshot{}, admissionError
	}
	defer releaseWriter()
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return CatalogSnapshot{}, fmt.Errorf("start GitHub catalog read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	owner, credential, token, err := store.lockCatalogOwner(ctx, tx, userKey)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	now := store.state.now().UTC()
	entries, err := tx.GitHubList.Query().Where(githublist.UserIDEQ(owner.ID), githublist.ViewerIDEQ(credential.ViewerID), githublist.CredentialGenerationEQ(credential.Generation), githublist.VerifiedAtGT(now.Add(-store.state.observationMaxAge)), githublist.VerifiedAtLTE(now), githublist.VisibleEQ(true)).Order(ent.Asc(githublist.FieldSlug), ent.Asc(githublist.FieldListKey)).All(ctx)
	if err != nil {
		return CatalogSnapshot{}, fmt.Errorf("read GitHub lists: %w", err)
	}
	lists := make([]GitHubList, 0, len(entries))
	observed := make([]time.Time, 0, len(entries))
	for _, entry := range entries {
		if entry.VerifiedAt == nil {
			return CatalogSnapshot{}, ErrObservationExpired
		}
		observed = append(observed, *entry.VerifiedAt)
		lists = append(lists, GitHubList{ID: entry.ListKey, Name: entry.Name, Slug: entry.Slug, Private: entry.Private, Subscribed: entry.Subscribed, Visible: true, UpdatedAt: entry.UpdatedAt, LastAddedAt: entry.LastAddedAt, VerifiedAt: *entry.VerifiedAt})
	}
	memberships, err := tx.GitHubMembership.Query().Where(githubmembership.HasListWith(githublist.UserIDEQ(owner.ID), githublist.ViewerIDEQ(credential.ViewerID), githublist.CredentialGenerationEQ(credential.Generation), githublist.VerifiedAtGT(now.Add(-store.state.observationMaxAge)), githublist.VerifiedAtLTE(now), githublist.SubscribedEQ(true), githublist.VisibleEQ(true)), githubmembership.VisibleEQ(true)).WithList().All(ctx)
	if err != nil {
		return CatalogSnapshot{}, fmt.Errorf("read subscribed GitHub memberships: %w", err)
	}
	ids := make([]int, 0, len(memberships))
	for _, membership := range memberships {
		if membership.Edges.List == nil || membership.Edges.List.VerifiedAt == nil {
			return CatalogSnapshot{}, ErrObservationExpired
		}
		observed = append(observed, *membership.Edges.List.VerifiedAt)
		ids = append(ids, membership.RepositoryID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	repositories := make([]GitHubRepository, 0, len(ids))
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		entries, err := tx.GitHubRepository.Query().Where(githubrepository.IDIn(ids[start:end]...), githubrepository.UserIDEQ(owner.ID), githubrepository.ViewerIDEQ(credential.ViewerID)).All(ctx)
		if err != nil {
			return CatalogSnapshot{}, fmt.Errorf("read subscribed GitHub repositories: %w", err)
		}
		if len(entries) != end-start {
			return CatalogSnapshot{}, fmt.Errorf("GitHub membership ownership is inconsistent")
		}
		for _, entry := range entries {
			repositories = append(repositories, GitHubRepository{ID: entry.RepositoryKey, NameWithOwner: entry.NameWithOwner, DatabaseID: entry.DatabaseID, Private: entry.Private})
		}
	}
	slices.SortFunc(repositories, func(first, second GitHubRepository) int {
		if order := strings.Compare(first.NameWithOwner, second.NameWithOwner); order != 0 {
			return order
		}
		return strings.Compare(first.ID, second.ID)
	})
	if err := store.checkCatalogFresh(token, observed); err != nil {
		return CatalogSnapshot{}, err
	}
	validUntil := now.Add(store.state.observationMaxAge)
	for _, verified := range observed {
		if expires := verified.Add(store.state.observationMaxAge); expires.Before(validUntil) {
			validUntil = expires
		}
	}
	if expires, exists := token.ExpiresAt(); exists && expires.Before(validUntil) {
		validUntil = expires
	}
	if err := tx.Commit(); err != nil {
		return CatalogSnapshot{}, fmt.Errorf("commit GitHub catalog read: %w", err)
	}
	if err := store.checkCatalogFresh(token, observed); err != nil {
		return CatalogSnapshot{}, err
	}
	if !store.state.now().Before(validUntil) {
		return CatalogSnapshot{}, ErrObservationExpired
	}
	return CatalogSnapshot{ViewerID: credential.ViewerID, Generation: credential.Generation, Lists: lists, Repositories: repositories, ValidUntil: validUntil}, nil
}

func (store *Store) lockCatalogOwner(ctx context.Context, tx *ent.Tx, userKey string) (*ent.User, *ent.GitHubCredential, github.Token, error) {
	owner, credential, token, err := store.catalogOwner(ctx, tx.Client(), userKey)
	if err != nil {
		return nil, nil, github.Token{}, err
	}
	count, err := tx.GitHubCredential.Update().Where(githubcredential.IDEQ(credential.ID), githubcredential.GenerationEQ(credential.Generation), githubcredential.ViewerIDEQ(credential.ViewerID), githubcredential.KeyIDEQ(store.state.keyID), githubcredential.StatusEQ(githubcredential.StatusReady), githubcredential.SyncSequenceEQ(credential.SyncSequence)).SetSyncSequence(credential.SyncSequence).Save(ctx)
	if err != nil {
		return nil, nil, github.Token{}, fmt.Errorf("lock GitHub catalog generation: %w", err)
	}
	if count != 1 {
		return nil, nil, github.Token{}, ErrSyncSuperseded
	}
	return owner, credential, token, nil
}

func (store *Store) catalogOwner(ctx context.Context, client *ent.Client, userKey string) (*ent.User, *ent.GitHubCredential, github.Token, error) {
	if err := validateIdentifier(userKey); err != nil {
		return nil, nil, github.Token{}, err
	}
	owner, err := findUser(ctx, client, userKey)
	if err != nil {
		return nil, nil, github.Token{}, err
	}
	credential, err := client.GitHubCredential.Query().Where(githubcredential.UserIDEQ(owner.ID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil, github.Token{}, ErrCredentialNotFound
	}
	if err != nil {
		return nil, nil, github.Token{}, fmt.Errorf("read GitHub credential: %w", err)
	}
	if credential.Status != githubcredential.StatusReady {
		return nil, nil, github.Token{}, ErrCredentialUnavailable
	}
	token, err := store.decryptCredential(userKey, credential)
	if err != nil {
		return nil, nil, github.Token{}, err
	}
	if expires, exists := token.ExpiresAt(); exists && !store.state.now().Before(expires) {
		return nil, nil, github.Token{}, ErrCredentialUnavailable
	}
	return owner, credential, token, nil
}

func (store *Store) checkCatalogFresh(token github.Token, observed []time.Time) error {
	now := store.state.now()
	if expires, exists := token.ExpiresAt(); exists && !now.Before(expires) {
		return ErrCredentialUnavailable
	}
	for _, verified := range observed {
		if verified.IsZero() || verified.After(now) || !now.Before(verified.Add(store.state.observationMaxAge)) {
			return ErrObservationExpired
		}
	}
	return nil
}
