package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"uuid"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/githubcredential"
	"github.com/ca-x/cognate/internal/server/ent/githublist"
)

var ErrSyncSuperseded = errors.New("GitHub synchronization was superseded")

type ListSyncTicket struct {
	userID     int
	userKey    string
	viewerID   string
	generation string
	sequence   int64
}

type ListSyncResult struct {
	ticket   ListSyncTicket
	fetchID  string
	client   *github.ListsClient
	snapshot github.ListsSnapshot
}

func (ListSyncResult) String() string   { return "[redacted GitHub synchronization result]" }
func (ListSyncResult) GoString() string { return "[redacted GitHub synchronization result]" }

func (store *Store) BeginListSync(ctx context.Context, userKey, generation string) (ListSyncTicket, error) {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return ListSyncTicket{}, admissionError
	}
	defer releaseWriter()
	if err := validateIdentifier(userKey); err != nil {
		return ListSyncTicket{}, err
	}
	if generation == "" {
		return ListSyncTicket{}, ErrSyncSuperseded
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return ListSyncTicket{}, fmt.Errorf("start GitHub synchronization transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	owner, err := findUser(ctx, tx.Client(), userKey)
	if err != nil {
		return ListSyncTicket{}, err
	}
	count, err := tx.GitHubCredential.Update().Where(githubcredential.UserIDEQ(owner.ID), githubcredential.GenerationEQ(generation), githubcredential.KeyIDEQ(store.state.keyID), githubcredential.StatusEQ(githubcredential.StatusReady), githubcredential.SyncSequenceLT(math.MaxInt64)).AddSyncSequence(1).SetSyncFetchID("").Save(ctx)
	if err != nil {
		return ListSyncTicket{}, fmt.Errorf("reserve GitHub synchronization generation: %w", err)
	}
	if count != 1 {
		return ListSyncTicket{}, ErrSyncSuperseded
	}
	entry, err := tx.GitHubCredential.Query().Where(githubcredential.UserIDEQ(owner.ID), githubcredential.GenerationEQ(generation)).Only(ctx)
	if err != nil {
		return ListSyncTicket{}, fmt.Errorf("read GitHub synchronization generation: %w", err)
	}
	if _, err := store.decryptCredential(userKey, entry); err != nil {
		return ListSyncTicket{}, err
	}
	if _, err := tx.GitHubList.Update().Where(githublist.UserIDEQ(owner.ID), githublist.ViewerIDEQ(entry.ViewerID), githublist.VisibleEQ(true)).SetVisible(false).Save(ctx); err != nil {
		return ListSyncTicket{}, fmt.Errorf("invalidate GitHub list visibility: %w", err)
	}
	ticket := ListSyncTicket{userID: owner.ID, userKey: userKey, viewerID: entry.ViewerID, generation: generation, sequence: entry.SyncSequence}
	if err := tx.Commit(); err != nil {
		return ListSyncTicket{}, fmt.Errorf("GitHub synchronization reservation outcome may be uncertain: %w", err)
	}
	return ticket, nil
}

func (store *Store) ReadListSync(ctx context.Context, ticket ListSyncTicket, client *github.ListsClient) (ListSyncResult, error) {
	return store.readListSync(ctx, ticket, client, false)
}

func (store *Store) ReadSelectedListSync(ctx context.Context, ticket ListSyncTicket, client *github.ListsClient) (ListSyncResult, error) {
	return store.readListSync(ctx, ticket, client, true)
}

func (store *Store) readListSync(ctx context.Context, ticket ListSyncTicket, client *github.ListsClient, selectedOnly bool) (ListSyncResult, error) {
	if !validSyncTicket(ticket) || client == nil {
		return ListSyncResult{}, ErrSyncSuperseded
	}
	fetchID := uuid.New().String()
	if err := store.claimListRead(ctx, ticket, fetchID, client); err != nil {
		return ListSyncResult{}, err
	}
	var snapshot github.ListsSnapshot
	var err error
	if selectedOnly {
		selected, readErr := store.listWatermarks(ctx, ticket)
		if readErr != nil {
			return ListSyncResult{}, readErr
		}
		snapshot, err = client.ListsSelected(ctx, selected)
	} else {
		snapshot, err = client.Lists(ctx)
	}
	if err != nil {
		return ListSyncResult{}, err
	}
	if snapshot.ViewerID != ticket.viewerID {
		return ListSyncResult{}, ErrSyncSuperseded
	}
	return ListSyncResult{ticket: ticket, fetchID: fetchID, client: client, snapshot: snapshot}, nil
}

func validSyncTicket(ticket ListSyncTicket) bool {
	return ticket.userID > 0 && ticket.userKey != "" && ticket.viewerID != "" && ticket.generation != "" && ticket.sequence > 0
}

func (store *Store) claimListRead(ctx context.Context, ticket ListSyncTicket, fetchID string, client *github.ListsClient) error {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return admissionError
	}
	defer releaseWriter()
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start GitHub synchronization read claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	owner, err := findUser(ctx, tx.Client(), ticket.userKey)
	if err != nil {
		return err
	}
	if owner.ID != ticket.userID {
		return ErrSyncSuperseded
	}
	count, err := tx.GitHubCredential.Update().Where(githubcredential.UserIDEQ(ticket.userID), githubcredential.ViewerIDEQ(ticket.viewerID), githubcredential.GenerationEQ(ticket.generation), githubcredential.KeyIDEQ(store.state.keyID), githubcredential.StatusEQ(githubcredential.StatusReady), githubcredential.SyncSequenceEQ(ticket.sequence), githubcredential.SyncAppliedSequenceLT(ticket.sequence), githubcredential.SyncFetchIDEQ("")).SetSyncFetchID(fetchID).Save(ctx)
	if err != nil {
		return fmt.Errorf("claim GitHub synchronization read: %w", err)
	}
	if count != 1 {
		return ErrSyncSuperseded
	}
	entry, err := tx.GitHubCredential.Query().Where(githubcredential.UserIDEQ(ticket.userID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("read GitHub synchronization credential: %w", err)
	}
	token, err := store.decryptCredential(ticket.userKey, entry)
	if err != nil {
		return err
	}
	if !client.MatchesToken(token) {
		return ErrSyncSuperseded
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("GitHub synchronization read claim outcome may be uncertain: %w", err)
	}
	return nil
}

func (store *Store) CommitListSync(ctx context.Context, result ListSyncResult, apply func(context.Context, *ent.Tx, github.ListsSnapshot) error) error {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return admissionError
	}
	defer releaseWriter()
	ticket, client, snapshot := result.ticket, result.client, result.snapshot
	if !validSyncTicket(ticket) || result.fetchID == "" || client == nil || apply == nil || snapshot.ViewerID != ticket.viewerID {
		return ErrSyncSuperseded
	}
	return client.CommitSnapshot(ctx, snapshot, func(ctx context.Context, snapshot github.ListsSnapshot) error {
		tx, err := store.state.client.Tx(ctx)
		if err != nil {
			return fmt.Errorf("start GitHub synchronization commit: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		owner, err := findUser(ctx, tx.Client(), ticket.userKey)
		if err != nil {
			return err
		}
		if owner.ID != ticket.userID {
			return ErrSyncSuperseded
		}
		count, err := tx.GitHubCredential.Update().Where(githubcredential.UserIDEQ(ticket.userID), githubcredential.ViewerIDEQ(ticket.viewerID), githubcredential.GenerationEQ(ticket.generation), githubcredential.KeyIDEQ(store.state.keyID), githubcredential.StatusEQ(githubcredential.StatusReady), githubcredential.SyncSequenceEQ(ticket.sequence), githubcredential.SyncAppliedSequenceLT(ticket.sequence), githubcredential.SyncFetchIDEQ(result.fetchID)).SetSyncAppliedSequence(ticket.sequence).Save(ctx)
		if err != nil {
			return fmt.Errorf("check GitHub synchronization generation: %w", err)
		}
		if count != 1 {
			return ErrSyncSuperseded
		}
		entry, err := tx.GitHubCredential.Query().Where(githubcredential.UserIDEQ(ticket.userID)).Only(ctx)
		if err != nil {
			return fmt.Errorf("read GitHub synchronization credential: %w", err)
		}
		token, err := store.decryptCredential(ticket.userKey, entry)
		if err != nil {
			return err
		}
		if !client.MatchesToken(token) {
			return ErrSyncSuperseded
		}
		if err := apply(ctx, tx, snapshot); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("GitHub synchronization commit outcome may be uncertain: %w", err)
		}
		return nil
	})
}
