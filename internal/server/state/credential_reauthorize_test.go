package state

import (
	"bytes"
	"context"
	"crypto/cipher"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	entgo "entgo.io/ent"
	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/githubcredential"
)

func TestCredentialReauthorizeNeedsExplicitReplacement(test *testing.T) {
	store, observer := refreshStores(test)
	oldToken, err := github.NewToken("synthetic-old-reauthorization", nil)
	if err != nil {
		test.Fatal(err)
	}
	oldGeneration, err := store.PutCredential(test.Context(), "alice", "old-viewer", "", oldToken)
	if err != nil {
		test.Fatal(err)
	}
	if err := observer.state.client.GitHubCredential.Update().SetStatus(githubcredential.StatusNeedsReauthorization).SetRefreshAttempt("old-refresh").SetSyncFetchID("old-fetch").SetCollectionCursor(17).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	before, err := observer.state.client.GitHubCredential.Query().Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	if credential, err := store.Credential(test.Context(), "alice"); !errors.Is(err, ErrCredentialUnavailable) || credential.Generation != "" || credential.Token.Reveal() != "" {
		test.Fatal("ordinary credential read bypassed needs gate")
	}
	newToken, err := github.NewToken("synthetic-new-reauthorization", nil)
	if err != nil {
		test.Fatal(err)
	}
	newGeneration, err := store.PutCredential(test.Context(), "alice", "new-viewer", oldGeneration, newToken)
	if err != nil {
		test.Fatalf("explicit restored credential replacement rejected: %v", err)
	}
	credential, err := observer.Credential(test.Context(), "alice")
	if err != nil || credential.Generation != newGeneration || newGeneration == oldGeneration || credential.ViewerID != "new-viewer" || credential.Token.Reveal() != newToken.Reveal() {
		test.Fatal("replacement was not readable by independent Store")
	}
	after, err := observer.state.client.GitHubCredential.Query().Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	if after.Status != githubcredential.StatusReady || after.RefreshAttempt != "" || after.SyncFetchID != "" || after.CollectionCursor != 0 || bytes.Equal(before.Ciphertext, after.Ciphertext) || bytes.Contains(after.Ciphertext, []byte(newToken.Reveal())) {
		test.Fatal("replacement retained claims, cursor, old ciphertext or plaintext")
	}
	beforeReplay := reauthorizeStoredRows(test, observer)
	if _, err := observer.PutCredential(test.Context(), "alice", "old-viewer", oldGeneration, oldToken); !errors.Is(err, ErrCredentialConflict) || !bytes.Equal(beforeReplay, reauthorizeStoredRows(test, observer)) {
		test.Fatal("old expected generation replay changed replacement")
	}
}

func reauthorizeEntry(test *testing.T, store *Store, owner string) *ent.GitHubCredential {
	test.Helper()
	user, err := findUser(test.Context(), store.state.client, owner)
	if err != nil {
		test.Fatal(err)
	}
	entry, err := store.state.client.GitHubCredential.Query().Where(githubcredential.UserIDEQ(user.ID)).Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	return entry
}

func reauthorizeStoredRows(test *testing.T, store *Store) []byte {
	test.Helper()
	entries, err := store.state.client.GitHubCredential.Query().Order(githubcredential.ByID()).All(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	var records []any
	for _, entry := range entries {
		records = append(records, []any{entry, entry.Ciphertext})
	}
	data, err := json.Marshal(records)
	if err != nil {
		test.Fatal(err)
	}
	return data
}

func TestCredentialReauthorizeRejectsWithoutCommittedWrites(test *testing.T) {
	for _, scenario := range []string{"blank-generation", "stale-generation", "refreshing", "other-owner", "viewer-aad", "client-aad", "key-aad", "generation-aad", "owner-aad", "last-ciphertext-byte", "ciphertext-version", "short-ciphertext", "canceled"} {
		test.Run(scenario, func(test *testing.T) {
			store, peer := refreshStores(test)
			token, err := github.NewToken("synthetic-old-rejection", nil)
			if err != nil {
				test.Fatal(err)
			}
			generation, err := store.PutCredential(test.Context(), "alice", "old-viewer", "", token)
			if err != nil {
				test.Fatal(err)
			}
			if _, err := store.PutCredential(test.Context(), "bob", "bob-viewer", "", token); err != nil {
				test.Fatal(err)
			}
			entry := reauthorizeEntry(test, peer, "alice")
			update := peer.state.client.GitHubCredential.UpdateOneID(entry.ID).SetStatus(githubcredential.StatusNeedsReauthorization).SetRefreshAttempt("retained-refresh").SetSyncSequence(19).SetSyncAppliedSequence(11).SetSyncFetchID("retained-fetch").SetCollectionCursor(23)
			expected, owner := generation, "alice"
			wantConflict := false
			switch scenario {
			case "blank-generation":
				expected, wantConflict = "", true
			case "stale-generation":
				expected, wantConflict = "stale", true
			case "refreshing":
				update.SetStatus(githubcredential.StatusRefreshing)
				wantConflict = true
			case "other-owner":
				owner, wantConflict = "bob", true
			case "viewer-aad":
				update.SetViewerID("tampered-viewer")
			case "client-aad":
				update.SetClientID("tampered-client")
			case "key-aad":
				update.SetKeyID("tampered-key")
			case "generation-aad":
				expected = "tampered-generation"
				update.SetGeneration(expected)
			case "owner-aad":
				update.SetCiphertext(reauthorizeEntry(test, peer, "bob").Ciphertext)
			case "last-ciphertext-byte":
				entry.Ciphertext[len(entry.Ciphertext)-1] ^= 1
				update.SetCiphertext(entry.Ciphertext)
			case "ciphertext-version":
				entry.Ciphertext[0] = 2
				update.SetCiphertext(entry.Ciphertext)
			case "short-ciphertext":
				update.SetCiphertext([]byte{1})
			}
			if err := update.Exec(test.Context()); err != nil {
				test.Fatal(err)
			}
			before := reauthorizeStoredRows(test, peer)
			newToken, err := github.NewToken("synthetic-rejected-new-token", nil)
			if err != nil {
				test.Fatal(err)
			}
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			_, err = store.PutCredential(ctx, owner, "new-viewer", expected, newToken)
			if err == nil || (wantConflict && !errors.Is(err, ErrCredentialConflict)) || (scenario == "canceled" && !errors.Is(err, context.Canceled)) {
				test.Fatalf("invalid replacement category: %v", err)
			}
			if strings.Contains(err.Error(), newToken.Reveal()) || strings.Contains(err.Error(), token.Reveal()) || !bytes.Equal(before, reauthorizeStoredRows(test, peer)) {
				test.Fatal("rejected replacement disclosed a secret or changed committed rows")
			}
		})
	}
}

type reauthorizeForbiddenOpen struct {
	cipher.AEAD
	opened *bool
}

func (guard reauthorizeForbiddenOpen) Open([]byte, []byte, []byte, []byte) ([]byte, error) {
	*guard.opened = true
	return nil, errors.New("secret access forbidden")
}

func TestCredentialReauthorizeIdentityReadsOnlyMetadata(test *testing.T) {
	store, peer := refreshStores(test)
	token, err := github.NewToken("synthetic-never-read-secret", nil)
	if err != nil {
		test.Fatal(err)
	}
	generation, err := peer.PutCredential(test.Context(), "alice", "old-viewer", "", token)
	if err != nil {
		test.Fatal(err)
	}
	entry := reauthorizeEntry(test, peer, "alice")
	if err := peer.state.client.GitHubCredential.UpdateOneID(entry.ID).SetCiphertext([]byte{1}).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	opened := false
	store.state.aead = reauthorizeForbiddenOpen{AEAD: store.state.aead, opened: &opened}
	reads := 0
	store.state.client.GitHubCredential.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			fields := slices.Clone(entgo.QueryFromContext(ctx).Fields)
			slices.Sort(fields)
			if !reflect.DeepEqual(fields, []string{"generation", "status", "viewer_id"}) {
				test.Fatal("identity query selected fields outside passive metadata")
			}
			reads++
			return next.Query(ctx, query)
		})
	}))
	for _, status := range []githubcredential.Status{githubcredential.StatusReady, githubcredential.StatusNeedsReauthorization, githubcredential.StatusRefreshing} {
		if err := peer.state.client.GitHubCredential.UpdateOneID(entry.ID).SetStatus(status).Exec(test.Context()); err != nil {
			test.Fatal(err)
		}
		before := reauthorizeStoredRows(test, peer)
		identity, err := store.CredentialIdentity(test.Context(), "alice")
		if status == githubcredential.StatusRefreshing {
			if !errors.Is(err, ErrCredentialUnavailable) || identity != (CredentialIdentity{}) {
				test.Fatal("refreshing identity was not fail-closed")
			}
		} else if err != nil || identity != (CredentialIdentity{ViewerID: "old-viewer", Generation: generation}) {
			test.Fatal("passive identity required secret decryption")
		}
		if opened || !bytes.Equal(before, reauthorizeStoredRows(test, peer)) {
			test.Fatal("metadata lookup touched secret or changed state")
		}
	}
	if reads != 3 {
		test.Fatal("metadata queries did not execute")
	}
	if identity, err := store.CredentialIdentity(test.Context(), "bob"); !errors.Is(err, ErrCredentialNotFound) || identity != (CredentialIdentity{}) {
		test.Fatal("missing owner credential exposed another owner's identity")
	}
	if identity, err := store.CredentialIdentity(test.Context(), "missing"); err == nil || identity != (CredentialIdentity{}) {
		test.Fatal("missing owner accepted")
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if identity, err := store.CredentialIdentity(ctx, "alice"); !errors.Is(err, context.Canceled) || identity != (CredentialIdentity{}) {
		test.Fatal("canceled identity read was not empty")
	}
}

type reauthorizeTransport func(*http.Request) (*http.Response, error)

func (transport reauthorizeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func reauthorizeListsClient(test *testing.T, token github.Token, calls *int) *github.ListsClient {
	test.Helper()
	client, err := github.NewListsClient(token, &http.Client{Transport: reauthorizeTransport(func(*http.Request) (*http.Response, error) {
		(*calls)++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"viewer":{"id":"viewer","lists":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}`))}, nil
	})})
	if err != nil {
		test.Fatal(err)
	}
	return client
}

func TestCredentialReauthorizeFencesOldWorkAndObservation(test *testing.T) {
	store, peer, _, generation, _ := sourceStarFixture(test)
	oldCredential, err := store.Credential(test.Context(), "alice")
	if err != nil {
		test.Fatal(err)
	}
	access, err := store.BeginSourceAccess(test.Context(), "alice", repository.Reference{NameWithOwner: "org/source"})
	if err != nil || !access.Authenticated() {
		test.Fatal("old source proof setup failed")
	}
	oldTicket, err := store.BeginListSync(test.Context(), "alice", generation)
	if err != nil {
		test.Fatal(err)
	}
	calls := 0
	oldClient := reauthorizeListsClient(test, oldCredential.Token, &calls)
	oldResult, err := store.ReadListSync(test.Context(), oldTicket, oldClient)
	if err != nil {
		test.Fatal(err)
	}
	if err := peer.state.client.GitHubList.Update().SetVisible(true).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := peer.state.client.GitHubCredential.Update().SetStatus(githubcredential.StatusNeedsReauthorization).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	token, err := github.NewToken("synthetic-new-generation-fencing", nil)
	if err != nil {
		test.Fatal(err)
	}
	newGeneration, err := peer.PutCredential(test.Context(), "alice", "viewer", generation, token)
	if err != nil {
		test.Fatal(err)
	}
	if err := store.CheckSourceAccess(test.Context(), access); err == nil {
		test.Fatal("old authenticated source proof survived new generation")
	}
	if _, err := store.BeginListSync(test.Context(), "alice", generation); !errors.Is(err, ErrSyncSuperseded) {
		test.Fatal("old expected generation retained synchronization authority")
	}
	if err := store.ApplyListSync(test.Context(), oldResult); !errors.Is(err, ErrSyncSuperseded) {
		test.Fatal("old observation result survived new generation")
	}
	callsBefore := calls
	if _, err := store.ReadListSync(test.Context(), oldTicket, oldClient); !errors.Is(err, ErrSyncSuperseded) || calls != callsBefore {
		test.Fatal("old ticket reached upstream after reauthorization")
	}
	catalog, err := store.GitHubCatalog(test.Context(), "alice")
	if err != nil || catalog.Generation != newGeneration || len(catalog.Lists) != 0 || len(catalog.Repositories) != 0 {
		test.Fatal("old catalog content revived after reauthorization")
	}
	newTicket, err := store.BeginListSync(test.Context(), "alice", newGeneration)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.ReadListSync(test.Context(), newTicket, oldClient); !errors.Is(err, ErrSyncSuperseded) || calls != callsBefore {
		test.Fatal("new ticket laundered old credential client")
	}
	newClient := reauthorizeListsClient(test, token, &calls)
	newResult, err := store.ReadListSync(test.Context(), newTicket, newClient)
	if err != nil {
		test.Fatal(err)
	}
	if err := store.ApplyListSync(test.Context(), newResult); err != nil {
		test.Fatal(err)
	}
	newAccess, err := store.BeginSourceAccess(test.Context(), "alice", repository.Reference{NameWithOwner: "org/source"})
	if err != nil || store.CheckSourceAccess(test.Context(), newAccess) != nil {
		test.Fatal("new generation could not issue usable source access")
	}
}

func TestCredentialReauthorizeRollsBackFailedReplacement(test *testing.T) {
	store, peer := refreshStores(test)
	token, err := github.NewToken("synthetic-rollback-old", nil)
	if err != nil {
		test.Fatal(err)
	}
	generation, err := store.PutCredential(test.Context(), "alice", "old-viewer", "", token)
	if err != nil {
		test.Fatal(err)
	}
	if err := peer.state.client.GitHubCredential.Update().SetStatus(githubcredential.StatusNeedsReauthorization).SetRefreshAttempt("old-refresh").SetSyncFetchID("old-fetch").SetCollectionCursor(27).Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	before := reauthorizeStoredRows(test, peer)
	failure := errors.New("synthetic replacement update failed")
	store.state.client.GitHubCredential.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if _, changed := mutation.(*ent.GitHubCredentialMutation).Generation(); changed {
				return nil, failure
			}
			return next.Mutate(ctx, mutation)
		})
	})
	newToken, err := github.NewToken("synthetic-rollback-new", nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.PutCredential(test.Context(), "alice", "new-viewer", generation, newToken); !errors.Is(err, failure) {
		test.Fatal("injected update failure was not propagated")
	}
	if !bytes.Equal(before, reauthorizeStoredRows(test, peer)) {
		test.Fatal("failed update partially published new identity or ready status")
	}
	if _, err := peer.Credential(test.Context(), "alice"); !errors.Is(err, ErrCredentialUnavailable) {
		test.Fatal("failed replacement enabled secret read")
	}
}
