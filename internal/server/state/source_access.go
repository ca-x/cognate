package state

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/githubcredential"
	"github.com/ca-x/cognate/internal/server/ent/githubrepository"
	"github.com/ca-x/cognate/internal/server/ent/repositoryprivatehistory"
)

var ErrSourceAccessUnavailable = errors.New("repository source authorization is unavailable or changed")

type SourceAccessTicket struct{ proof sourceAccessCapability }
type sourceAccessCapability func() *sourceAccessProof

func (capability sourceAccessCapability) get() *sourceAccessProof {
	if capability == nil {
		return nil
	}
	return capability()
}

func sourceAccessCapabilityFor(proof *sourceAccessProof) sourceAccessCapability {
	if proof == nil {
		return nil
	}
	return func() *sourceAccessProof { return proof }
}

type sourceAccessProof struct {
	origin                    *sourceAccessProof
	issuer                    *storeState
	owner                     string
	ownerID                   int
	reference                 repository.Reference
	credentialID              int
	viewer, generation, keyID string
	token                     github.Token
	star                      *sourceRefreshStar
	metadata                  *repository.GitHubMetadata
}
type sourceAccessContextKey struct{}

func (SourceAccessTicket) Format(destination fmt.State, _ rune) {
	_, _ = io.WriteString(destination, "[repository source access]")
}

func (ticket SourceAccessTicket) Authenticated() bool {
	return ticket.proof.get() != nil && ticket.proof.get().credentialID != 0
}

func (ticket SourceAccessTicket) Reference() repository.Reference {
	if ticket.proof.get() == nil {
		return repository.Reference{}
	}
	return ticket.proof.get().reference
}

func (ticket SourceAccessTicket) Token() github.Token {
	if ticket.proof.get() == nil {
		return github.Token{}
	}
	return ticket.proof.get().token.AccessOnly()
}

func (ticket SourceAccessTicket) Deadline() time.Time {
	if ticket.proof.get() != nil && ticket.proof.get().star != nil {
		return ticket.proof.get().star.expiresAt
	}
	deadline, _ := ticket.Token().ExpiresAt()
	return deadline
}

func (store *Store) BeginSourceAccess(ctx context.Context, owner string, reference repository.Reference) (SourceAccessTicket, error) {
	var proof *sourceAccessProof
	err := store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		var err error
		proof, err = store.readSourceAccess(ctx, tx, owner, reference, nil)
		return err
	})
	if err != nil {
		return SourceAccessTicket{}, err
	}
	return SourceAccessTicket{proof: sourceAccessCapabilityFor(proof)}, nil
}

func (store *Store) readSourceAccess(ctx context.Context, tx *ent.Tx, ownerKey string, reference repository.Reference, star *sourceRefreshStar) (*sourceAccessProof, error) {
	canonical, err := repository.ParseReference(reference.NameWithOwner, reference.Branch)
	if err != nil || canonical != reference {
		return nil, ErrSourceAccessUnavailable
	}
	owner, err := findUser(ctx, tx.Client(), ownerKey)
	if err != nil {
		return nil, err
	}
	proof := &sourceAccessProof{issuer: store.state, owner: ownerKey, ownerID: owner.ID, reference: reference, star: star}
	proof.origin = proof
	credential, err := tx.GitHubCredential.Query().Where(githubcredential.UserIDEQ(owner.ID)).Only(ctx)
	if ent.IsNotFound(err) {
		known, err := tx.GitHubRepository.Query().Where(githubrepository.UserIDEQ(owner.ID), githubrepository.NameWithOwnerEqualFold(reference.NameWithOwner), githubrepository.PrivateEQ(true)).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if !known {
			known, err = tx.RepositoryPrivateHistory.Query().Where(repositoryprivatehistory.UserIDEQ(owner.ID), repositoryprivatehistory.NameWithOwnerEQ(reference.NameWithOwner)).Exist(ctx)
			if err != nil {
				return nil, err
			}
		}
		if known || star != nil {
			return nil, ErrSourceAccessUnavailable
		}
		return proof, nil
	}
	if err != nil {
		return nil, err
	}
	_, credential, token, err := store.catalogOwner(ctx, tx.Client(), ownerKey)
	if err != nil {
		return nil, ErrSourceAccessUnavailable
	}
	if star != nil && !star.private {
		return proof, nil
	}
	proof.credentialID, proof.viewer, proof.generation, proof.keyID = credential.ID, credential.ViewerID, credential.Generation, credential.KeyID
	proof.token = token.AccessOnly()
	return proof, nil
}

func (store *Store) CheckSourceAccess(ctx context.Context, ticket SourceAccessTicket) error {
	return store.catalogTransaction(ctx, func(tx *ent.Tx) error {
		return store.checkSourceAccess(ctx, tx, ticket.proof.get(), true)
	})
}

func (store *Store) checkSourceAccess(ctx context.Context, tx *ent.Tx, proof *sourceAccessProof, lock bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if proof == nil || proof.issuer != store.state {
		return ErrSourceAccessUnavailable
	}
	if lock {
		if proof.credentialID != 0 {
			if _, _, _, err := store.lockCatalogOwner(ctx, tx, proof.owner); err != nil {
				return err
			}
		} else {
			if _, err := tx.GitHubCredential.Update().Where(githubcredential.UserIDEQ(proof.ownerID), githubcredential.IDLT(0)).SetSyncSequence(0).Save(ctx); err != nil {
				return err
			}
		}
	}
	if proof.star != nil {
		entry := &ent.CollectedRepository{UserID: proof.ownerID, NameWithOwner: proof.reference.NameWithOwner, Branch: proof.reference.Branch}
		current, err := store.readSourceRefreshStar(ctx, tx, proof.owner, entry, proof.star.repositoryID, lock)
		if err != nil {
			return err
		}
		if !sameSourceRefreshStar(current, proof.star) {
			return ErrSourceAccessUnavailable
		}
		if !proof.star.private {
			return nil
		}
	}
	current, err := store.readSourceAccess(ctx, tx, proof.owner, proof.reference, proof.star)
	if err != nil {
		return err
	}
	if current.ownerID != proof.ownerID || current.credentialID != proof.credentialID || current.viewer != proof.viewer || current.generation != proof.generation || current.keyID != proof.keyID || current.token.Reveal() != proof.token.Reveal() || !currentDeadline(current).Equal(currentDeadline(proof)) {
		return ErrSourceAccessUnavailable
	}
	return nil
}

func currentDeadline(proof *sourceAccessProof) time.Time {
	deadline, _ := proof.token.ExpiresAt()
	return deadline
}

func (store *Store) BindSourceMetadata(ctx context.Context, ticket SourceAccessTicket, metadata repository.GitHubMetadata) (SourceAccessTicket, error) {
	if err := store.CheckSourceAccess(ctx, ticket); err != nil {
		return SourceAccessTicket{}, err
	}
	if !ticket.Authenticated() || metadata.NameWithOwner != ticket.proof.get().reference.NameWithOwner || len(metadata.NodeID) < 1 || len(metadata.NodeID) > 256 || strings.ContainsFunc(metadata.NodeID, func(character rune) bool { return character < 0x21 || character > 0x7e }) {
		return SourceAccessTicket{}, ErrSourceAccessUnavailable
	}
	if star := ticket.proof.get().star; star != nil && (metadata.NodeID != star.repositoryKey || metadata.Private != star.private) {
		return SourceAccessTicket{}, ErrSourceAccessUnavailable
	}
	proof := *ticket.proof.get()
	proof.metadata = new(metadata)
	return SourceAccessTicket{proof: sourceAccessCapabilityFor(&proof)}, nil
}

func (store *Store) WithSourceAccess(ctx context.Context, ticket SourceAccessTicket) context.Context {
	return context.WithValue(ctx, sourceAccessContextKey{}, ticket)
}

func (store *Store) sourceAccessTarget(ctx context.Context, owner string, reference repository.Reference) error {
	value := ctx.Value(sourceAccessContextKey{})
	if value == nil {
		return nil
	}
	ticket, valid := value.(SourceAccessTicket)
	proof := ticket.proof.get()
	if !valid || proof == nil || proof.issuer != store.state || proof.owner != owner || proof.reference != reference {
		return ErrSourceAccessUnavailable
	}
	return nil
}

func (store *Store) guardSourceAccessTransaction(ctx context.Context, tx *ent.Tx) error {
	value := ctx.Value(sourceAccessContextKey{})
	if value == nil {
		return nil
	}
	ticket, valid := value.(SourceAccessTicket)
	proof := ticket.proof.get()
	if !valid {
		return ErrSourceAccessUnavailable
	}
	if err := store.checkSourceAccess(ctx, tx, proof, true); err != nil {
		return err
	}
	if proof.credentialID != 0 && proof.metadata == nil {
		return ErrSourceAccessUnavailable
	}
	if err := store.guardMCPTransaction(ctx, tx, proof.owner, MCPCatalogWrite); err != nil {
		return err
	}
	if proof.metadata != nil {
		if err := rememberPrivateRepository(ctx, tx, proof.ownerID, proof.metadata.NameWithOwner, proof.metadata.Private); err != nil {
			return err
		}
	}
	if proof.metadata != nil && proof.star == nil {
		metadata := proof.metadata
		entry, err := tx.GitHubRepository.Query().Where(githubrepository.UserIDEQ(proof.ownerID), githubrepository.ViewerIDEQ(proof.viewer), githubrepository.RepositoryKeyEQ(metadata.NodeID)).Only(ctx)
		if err == nil {
			if err := rememberPrivateRepository(ctx, tx, proof.ownerID, entry.NameWithOwner, entry.Private); err != nil {
				return err
			}
		} else if !ent.IsNotFound(err) {
			return err
		}
		if ent.IsNotFound(err) {
			err = tx.GitHubRepository.Create().SetUserID(proof.ownerID).SetViewerID(proof.viewer).SetRepositoryKey(metadata.NodeID).SetNameWithOwner(metadata.NameWithOwner).SetPrivate(metadata.Private).Exec(ctx)
		} else if err == nil {
			err = tx.GitHubRepository.UpdateOne(entry).SetNameWithOwner(metadata.NameWithOwner).SetPrivate(metadata.Private).Exec(ctx)
		}
		if err != nil {
			return err
		}
	}
	tx.OnCommit(func(next ent.Committer) ent.Committer {
		return ent.CommitFunc(func(commitContext context.Context, transaction *ent.Tx) error {
			if err := store.checkSourceAccess(commitContext, transaction, proof, false); err != nil {
				return err
			}
			return next.Commit(commitContext, transaction)
		})
	})
	return nil
}

func rememberPrivateRepository(ctx context.Context, tx *ent.Tx, ownerID int, name string, private bool) error {
	if !private {
		return nil
	}
	reference, err := repository.ParseReference(name, "")
	if err != nil || !strings.EqualFold(name, reference.NameWithOwner) {
		return ErrSourceAccessUnavailable
	}
	exists, err := tx.RepositoryPrivateHistory.Query().Where(repositoryprivatehistory.UserIDEQ(ownerID), repositoryprivatehistory.NameWithOwnerEQ(reference.NameWithOwner)).Exist(ctx)
	if err != nil || exists {
		return err
	}
	return tx.RepositoryPrivateHistory.Create().SetUserID(ownerID).SetNameWithOwner(reference.NameWithOwner).Exec(ctx)
}

func (store *Store) BeginListSourceAccess(ctx context.Context, result ListSyncResult, repositoryID string, reference repository.Reference) (SourceAccessTicket, error) {
	var proof *sourceAccessProof
	err := store.withCurrentListSync(ctx, result, func(ctx context.Context, tx *ent.Tx, _ *ent.GitHubCredential, _ github.Token) error {
		entry := &ent.CollectedRepository{UserID: result.ticket.userID, NameWithOwner: reference.NameWithOwner, Branch: reference.Branch}
		star, err := store.readSourceRefreshStar(ctx, tx, result.ticket.userKey, entry, 0, false)
		if err != nil {
			return err
		}
		if star.repositoryKey != repositoryID {
			return ErrSourceAccessUnavailable
		}
		proof, err = store.readSourceAccess(ctx, tx, result.ticket.userKey, reference, star)
		return err
	})
	if err != nil {
		return SourceAccessTicket{}, err
	}
	return SourceAccessTicket{proof: sourceAccessCapabilityFor(proof)}, nil
}
