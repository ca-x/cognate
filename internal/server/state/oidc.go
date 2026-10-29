package state

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/oidcidentity"
	"github.com/ca-x/cognate/internal/server/ent/session"
)

var ErrOIDCIdentityConflict = errors.New("OpenID Connect identity already belongs to another user")

type OIDCSessionPlan struct {
	Issuer          string
	Subject         string
	BindUser        string
	BindSessionHash []byte
	TokenHash       []byte
	IssuedAt        time.Time
	ExpiresAt       time.Time
}

func (OIDCSessionPlan) String() string   { return "[redacted OpenID Connect session]" }
func (OIDCSessionPlan) GoString() string { return "[redacted OpenID Connect session]" }

func (store *Store) CreateOIDCSession(ctx context.Context, plan OIDCSessionPlan) (string, error) {
	ctx, releaseWriter, admissionError := store.enterWriter(ctx)
	if admissionError != nil {
		return "", admissionError
	}
	defer releaseWriter()
	issuer, err := url.Parse(plan.Issuer)
	if err != nil || len(plan.Issuer) == 0 || len(plan.Issuer) > 2048 || !utf8.ValidString(plan.Issuer) || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || (issuer.Scheme != "https" && issuer.Scheme != "http") || len(plan.Subject) == 0 || len(plan.Subject) > 255 || !utf8.ValidString(plan.Subject) || strings.ContainsAny(plan.Subject, "\x00\r\n") || len(plan.TokenHash) != 32 || plan.IssuedAt.IsZero() || !plan.ExpiresAt.After(plan.IssuedAt) {
		return "", fmt.Errorf("invalid OpenID Connect session")
	}
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return "", fmt.Errorf("start OpenID Connect session transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var bindingExpires time.Time
	if plan.BindUser != "" {
		if len(plan.BindSessionHash) != 32 {
			return "", ErrSessionInvalid
		}
		now := store.state.now().UTC()
		proof, err := tx.Session.Query().Where(session.TokenHashEQ(plan.BindSessionHash), session.IssuedAtLTE(now), session.ExpiresAtGT(now)).WithUser().Only(ctx)
		if ent.IsNotFound(err) {
			return "", ErrSessionInvalid
		}
		if err != nil {
			return "", fmt.Errorf("verify OpenID Connect binding session: %w", err)
		}
		if proof.Edges.User == nil || proof.Edges.User.UserKey != plan.BindUser {
			return "", ErrSessionInvalid
		}
		bindingExpires = proof.ExpiresAt
	}
	identity, err := tx.OIDCIdentity.Query().Where(oidcidentity.IssuerEQ(plan.Issuer), oidcidentity.SubjectEQ(plan.Subject)).WithUser().Only(ctx)
	var owner *ent.User
	if err == nil {
		owner = identity.Edges.User
		if owner == nil {
			return "", fmt.Errorf("OpenID Connect identity owner is unavailable")
		}
		if plan.BindUser != "" && plan.BindUser != owner.UserKey {
			return "", ErrOIDCIdentityConflict
		}
	} else if ent.IsNotFound(err) {
		if plan.BindUser != "" {
			owner, err = findUser(ctx, tx.Client(), plan.BindUser)
		} else {
			owner, err = tx.User.Create().SetUserKey(uuid.New().String()).Save(ctx)
		}
		if err != nil {
			return "", fmt.Errorf("create OpenID Connect user: %w", err)
		}
		if err := tx.OIDCIdentity.Create().SetUserID(owner.ID).SetIssuer(plan.Issuer).SetSubject(plan.Subject).Exec(ctx); err != nil {
			if ent.IsConstraintError(err) {
				return "", ErrOIDCIdentityConflict
			}
			return "", fmt.Errorf("create OpenID Connect identity: %w", err)
		}
	} else {
		return "", fmt.Errorf("read OpenID Connect identity: %w", err)
	}
	if err := tx.Session.Create().SetUserID(owner.ID).SetTokenHash(plan.TokenHash).SetIssuedAt(plan.IssuedAt).SetExpiresAt(plan.ExpiresAt).Exec(ctx); err != nil {
		return "", fmt.Errorf("create OpenID Connect session: %w", err)
	}
	if !bindingExpires.IsZero() && !store.state.now().Before(bindingExpires) {
		return "", ErrSessionInvalid
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("OpenID Connect session outcome may be uncertain: %w", err)
	}
	return owner.UserKey, nil
}
