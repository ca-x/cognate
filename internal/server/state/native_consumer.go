package state

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeconsumerchallenge"
	"github.com/ca-x/cognate/internal/server/ent/nativeconsumersession"
	"github.com/ca-x/cognate/internal/server/ent/session"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const NativeConfigurationApply = "configuration-apply"

var (
	ErrNativeUnauthorized   = errors.New("invalid native authorization")
	ErrNativeInvalid        = errors.New("invalid native request")
	ErrNativeQuota          = errors.New("native capacity exhausted")
	ErrNativeUnavailable    = errors.New("native storage unavailable")
	ErrNativeOutcomeUnknown = errors.New("native transaction outcome unknown")
)

type nativeBox[Value any] struct{ value *Value }
type nativeOpaque[Value any] struct{ box *nativeBox[Value] }

func nativeWrap[Value any](value Value) nativeOpaque[Value] {
	return nativeOpaque[Value]{box: &nativeBox[Value]{value: new(value)}}
}

func (nativeOpaque[Value]) String() string   { return "[redacted native state]" }
func (nativeOpaque[Value]) GoString() string { return "[redacted native state]" }
func (nativeOpaque[Value]) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted native state]")
}
func (nativeOpaque[Value]) LogValue() slog.Value { return slog.StringValue("[redacted native state]") }
func (nativeOpaque[Value]) MarshalText() ([]byte, error) {
	return []byte("[redacted native state]"), nil
}
func (nativeOpaque[Value]) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted native state]"`), nil
}

func (value nativeOpaque[Value]) present() bool { return value.box != nil && value.box.value != nil }

type OwnerSessionCredential struct{ nativeOpaque[[32]byte] }
type NativeConsumerCredential struct{ nativeOpaque[[32]byte] }
type NativeConsumerBindingProof struct {
	nativeOpaque[nativeBindEvidence]
}
type NativeConsumerChallenge struct {
	nativeOpaque[nativeChallengeSnapshot]
}
type NativeConsumerIdentity struct {
	nativeOpaque[nativeIdentitySnapshot]
}

type NativeConsumerStart struct {
	ExpectedOwner string
	Origin        string
	Purpose       string
	OperationID   []byte
	PublicKey     []byte
	ClientNonce   []byte
}

type NativeConsumerBind struct {
	ChallengeID string
	TokenHash   NativeConsumerCredential
	Proof       NativeConsumerBindingProof
}

type nativeChallengeSnapshot struct {
	ID              string
	Owner           string
	Origin          string
	Purpose         string
	UserID          int
	ParentID        int
	OperationID     [16]byte
	PublicKey       [32]byte
	ClientNonce     [32]byte
	Challenge       [32]byte
	Binding         [32]byte
	IssuedMS        int64
	ExpiresMS       int64
	NativeExpiresMS int64
}

type nativeBindEvidence struct {
	ParentHash [32]byte
	Challenge  nativeChallengeSnapshot
}

type nativeIdentitySnapshot struct {
	ID          string
	Owner       string
	Origin      string
	OperationID [16]byte
	PublicKey   [32]byte
	Binding     [32]byte
	ExpiresMS   int64
}

func (challenge NativeConsumerChallenge) ID() string {
	if !challenge.present() {
		return ""
	}
	return challenge.box.value.ID
}

func (identity NativeConsumerIdentity) ID() string {
	if !identity.present() {
		return ""
	}
	return identity.box.value.ID
}

func (identity NativeConsumerIdentity) Owner() string {
	if !identity.present() {
		return ""
	}
	return identity.box.value.Owner
}

type nativeLimit struct {
	rows  int
	bytes int64
}
type nativeBudget struct {
	tables   [2][3]nativeLimit
	totals   [3]nativeLimit
	rowBytes [2]int64
}

func nativeDefaultBudget() nativeBudget {
	return nativeBudget{
		tables: [2][3]nativeLimit{
			{{8, 32 << 10}, {32, 128 << 10}, {128, 512 << 10}},
			{{16, 64 << 10}, {32, 128 << 10}, {128, 512 << 10}},
		},
		totals:   [3]nativeLimit{{56, 416 << 10}, {128, 896 << 10}, {512, 3584 << 10}},
		rowBytes: [2]int64{4096, 4096},
	}
}

func nativeActive(issued, expires, now time.Time) bool {
	return !issued.IsZero() && expires.After(issued) && !now.Before(issued) && now.Before(expires)
}

func nativeParentGate(parent *ent.Session, now time.Time) error {
	if parent == nil || parent.Edges.User == nil || !nativeActive(parent.IssuedAt, parent.ExpiresAt, now) {
		return ErrNativeUnauthorized
	}
	return nil
}

func nativeParent(ctx context.Context, tx *ent.Tx, credential OwnerSessionCredential, now time.Time) (*ent.Session, error) {
	if !credential.present() {
		return nil, ErrNativeUnauthorized
	}
	parent, err := tx.Session.Query().Where(session.TokenHashEQ(credential.box.value[:])).WithUser().Only(ctx)
	if err != nil {
		return nil, err
	}
	return parent, nativeParentGate(parent, now)
}

func nativeStartValid(input NativeConsumerStart) bool {
	origin, err := url.Parse(input.Origin)
	return err == nil && len(input.Origin) <= 2048 && origin.Scheme == "https" && origin.Hostname() != "" && origin.User == nil && origin.Path == "" && origin.RawQuery == "" && !origin.ForceQuery && origin.Fragment == "" && origin.Opaque == "" && origin.String() == input.Origin && !strings.ContainsAny(input.Origin, "\r\n\t") && input.Purpose == NativeConfigurationApply && input.ExpectedOwner != "" && len(input.OperationID) == 16 && len(input.PublicKey) == 32 && len(input.ClientNonce) == 32
}

func nativeChallengeCharge(row *ent.NativeConsumerChallenge) int64 {
	return 512 + int64(len(row.PublicID)+len(row.Purpose)+len(row.Origin)+len(row.OperationID)+len(row.PublicKey)+len(row.ClientNonce)+len(row.Challenge)+len(row.SessionBinding))
}

func nativeSessionCharge(row *ent.NativeConsumerSession) int64 {
	return 512 + int64(len(row.PublicID)+len(row.Purpose)+len(row.Origin)+len(row.OperationID)+len(row.PublicKey)+len(row.SessionBinding)+len(row.TokenHash))
}

func nativeChallengeRecordValid(row *ent.NativeConsumerChallenge) bool {
	return validPackKey(row.PublicID) && row.UserID > 0 && row.ParentSessionID > 0 && string(row.Purpose) == NativeConfigurationApply && len(row.Origin) > 0 && len(row.Origin) <= 2048 && len(row.OperationID) == 16 && len(row.PublicKey) == 32 && len(row.ClientNonce) == 32 && len(row.Challenge) == 32 && len(row.SessionBinding) == 32 && row.IssuedMs > 0 && row.ExpiresMs > row.IssuedMs && row.ExpiresMs-row.IssuedMs <= 60000 && row.NativeExpiresMs >= row.ExpiresMs && row.NativeExpiresMs-row.IssuedMs <= 300000 && row.Charge == nativeChallengeCharge(row) && row.Charge >= 512 && row.Charge <= 4096
}

func nativeSessionRecordValid(row *ent.NativeConsumerSession) bool {
	return validPackKey(row.PublicID) && row.UserID > 0 && row.ParentSessionID > 0 && string(row.Purpose) == NativeConfigurationApply && len(row.Origin) > 0 && len(row.Origin) <= 2048 && len(row.OperationID) == 16 && len(row.PublicKey) == 32 && len(row.SessionBinding) == 32 && len(row.TokenHash) == 32 && row.IssuedMs > 0 && row.ExpiresMs > row.IssuedMs && row.ExpiresMs-row.IssuedMs <= 300000 && row.GateVersion >= 0 && row.Charge == nativeSessionCharge(row) && row.Charge >= 512 && row.Charge <= 4096
}

type nativeUsageRow struct {
	table  int
	parent int
	owner  int
	charge int64
}

func nativeCheckQuota(ctx context.Context, tx *ent.Tx, added nativeUsageRow, budget nativeBudget) error {
	if added.charge < 512 || added.charge > budget.rowBytes[added.table] {
		return ErrNativeQuota
	}
	challenges, err := tx.NativeConsumerChallenge.Query().Limit(129).All(ctx)
	if err != nil {
		return err
	}
	children, err := tx.NativeConsumerSession.Query().Limit(129).All(ctx)
	if err != nil {
		return err
	}
	if len(challenges) > 128 || len(children) > 128 {
		return ErrNativeUnavailable
	}
	rows := make([]nativeUsageRow, 0, len(challenges)+len(children)+1)
	for _, row := range challenges {
		if !nativeChallengeRecordValid(row) {
			return ErrNativeUnavailable
		}
		rows = append(rows, nativeUsageRow{0, row.ParentSessionID, row.UserID, row.Charge})
	}
	for _, row := range children {
		if !nativeSessionRecordValid(row) {
			return ErrNativeUnavailable
		}
		rows = append(rows, nativeUsageRow{1, row.ParentSessionID, row.UserID, row.Charge})
	}
	rows = append(rows, added)
	var usage [2][3]nativeLimit
	var total [3]nativeLimit
	for _, row := range rows {
		for scope := range 3 {
			if scope == 0 && row.parent != added.parent || scope == 1 && row.owner != added.owner {
				continue
			}
			usage[row.table][scope].rows++
			usage[row.table][scope].bytes += row.charge
			total[scope].rows++
			total[scope].bytes += row.charge
		}
	}
	for scope := range 3 {
		if total[scope].rows > budget.totals[scope].rows || total[scope].bytes > budget.totals[scope].bytes {
			return ErrNativeQuota
		}
		for table := range 2 {
			if usage[table][scope].rows > budget.tables[table][scope].rows || usage[table][scope].bytes > budget.tables[table][scope].bytes {
				return ErrNativeQuota
			}
		}
	}
	return nil
}

func nativeSnapshot(row *ent.NativeConsumerChallenge, owner string) nativeChallengeSnapshot {
	return nativeChallengeSnapshot{ID: row.PublicID, Owner: owner, Origin: row.Origin, Purpose: string(row.Purpose), UserID: row.UserID, ParentID: row.ParentSessionID, OperationID: [16]byte(row.OperationID), PublicKey: [32]byte(row.PublicKey), ClientNonce: [32]byte(row.ClientNonce), Challenge: [32]byte(row.Challenge), Binding: [32]byte(row.SessionBinding), IssuedMS: row.IssuedMs, ExpiresMS: row.ExpiresMs, NativeExpiresMS: row.NativeExpiresMs}
}

func (store *Store) BeginNativeConsumer(ctx context.Context, credential OwnerSessionCredential, input NativeConsumerStart) (NativeConsumerChallenge, error) {
	return store.beginNativeConsumer(ctx, credential, input, nativeDefaultBudget())
}

func (store *Store) beginNativeConsumer(ctx context.Context, credential OwnerSessionCredential, input NativeConsumerStart, budget nativeBudget) (NativeConsumerChallenge, error) {
	if !credential.present() {
		return NativeConsumerChallenge{}, ErrNativeUnauthorized
	}
	if !nativeStartValid(input) {
		return NativeConsumerChallenge{}, ErrNativeInvalid
	}
	var challenge, binding [32]byte
	_, _ = rand.Read(challenge[:])
	_, _ = rand.Read(binding[:])
	id := rand.Text()
	run := func() (NativeConsumerChallenge, error) {
		return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeConsumerChallenge, func(time.Time) error, error) {
			now := store.state.now().UTC()
			parent, err := nativeParent(ctx, tx, credential, now)
			if err != nil {
				return NativeConsumerChallenge{}, nil, err
			}
			if parent.Edges.User.UserKey != input.ExpectedOwner {
				return NativeConsumerChallenge{}, nil, ErrNativeUnauthorized
			}
			expires := min(now.Add(time.Minute).UnixMilli(), parent.ExpiresAt.UnixMilli())
			nativeExpires := min(now.Add(5*time.Minute).UnixMilli(), parent.ExpiresAt.UnixMilli())
			row := &ent.NativeConsumerChallenge{PublicID: id, UserID: parent.UserID, ParentSessionID: parent.ID, Purpose: nativeconsumerchallenge.PurposeConfigurationApply, Origin: input.Origin, OperationID: bytes.Clone(input.OperationID), PublicKey: bytes.Clone(input.PublicKey), ClientNonce: bytes.Clone(input.ClientNonce), Challenge: challenge[:], SessionBinding: binding[:], IssuedMs: now.UnixMilli(), ExpiresMs: expires, NativeExpiresMs: nativeExpires}
			row.Charge = nativeChallengeCharge(row)
			if err := nativeCheckQuota(ctx, tx, nativeUsageRow{0, parent.ID, parent.UserID, row.Charge}, budget); err != nil {
				return NativeConsumerChallenge{}, nil, err
			}
			_, err = tx.NativeConsumerChallenge.Create().SetPublicID(row.PublicID).SetUserID(row.UserID).SetParentSessionID(row.ParentSessionID).SetPurpose(row.Purpose).SetOrigin(row.Origin).SetOperationID(row.OperationID).SetPublicKey(row.PublicKey).SetClientNonce(row.ClientNonce).SetChallenge(row.Challenge).SetSessionBinding(row.SessionBinding).SetIssuedMs(row.IssuedMs).SetExpiresMs(row.ExpiresMs).SetNativeExpiresMs(row.NativeExpiresMs).SetCharge(row.Charge).Save(ctx)
			gate := func(instant time.Time) error {
				if err := nativeParentGate(parent, instant); err != nil {
					return err
				}
				if !nativeActive(time.UnixMilli(row.IssuedMs), time.UnixMilli(row.ExpiresMs), instant) {
					return ErrNativeUnauthorized
				}
				return nil
			}
			return NativeConsumerChallenge{nativeWrap(nativeSnapshot(row, parent.Edges.User.UserKey))}, gate, err
		})
	}
	result, err := run()
	if errors.Is(err, ErrNativeQuota) {
		if _, pruneErr := store.PruneNativeConsumers(ctx); pruneErr != nil {
			return NativeConsumerChallenge{}, pruneErr
		}
		return run()
	}
	return result, err
}

func (store *Store) BindNativeConsumer(ctx context.Context, parentCredential OwnerSessionCredential, input NativeConsumerBind) (NativeConsumerIdentity, error) {
	return store.bindNativeConsumer(ctx, parentCredential, input, nativeDefaultBudget())
}

func (store *Store) bindNativeConsumer(ctx context.Context, parentCredential OwnerSessionCredential, input NativeConsumerBind, budget nativeBudget) (NativeConsumerIdentity, error) {
	if !parentCredential.present() || !input.Proof.present() || !input.TokenHash.present() {
		return NativeConsumerIdentity{}, ErrNativeUnauthorized
	}
	if !validPackKey(input.ChallengeID) {
		return NativeConsumerIdentity{}, ErrNativeInvalid
	}
	id := rand.Text()
	run := func() (NativeConsumerIdentity, error) {
		return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeConsumerIdentity, func(time.Time) error, error) {
			now := store.state.now().UTC()
			parent, err := nativeParent(ctx, tx, parentCredential, now)
			if err != nil {
				return NativeConsumerIdentity{}, nil, err
			}
			challenge, err := tx.NativeConsumerChallenge.Query().Where(nativeconsumerchallenge.PublicIDEQ(input.ChallengeID), nativeconsumerchallenge.ParentSessionIDEQ(parent.ID), nativeconsumerchallenge.UserIDEQ(parent.UserID)).Only(ctx)
			if err != nil {
				return NativeConsumerIdentity{}, nil, err
			}
			if !nativeChallengeRecordValid(challenge) {
				return NativeConsumerIdentity{}, nil, ErrNativeUnavailable
			}
			if challenge.Used || !nativeActive(time.UnixMilli(challenge.IssuedMs), time.UnixMilli(challenge.ExpiresMs), now) {
				return NativeConsumerIdentity{}, nil, ErrNativeUnauthorized
			}
			evidence := input.Proof.box.value
			if subtle.ConstantTimeCompare(evidence.ParentHash[:], parentCredential.box.value[:]) != 1 || evidence.Challenge != nativeSnapshot(challenge, parent.Edges.User.UserKey) {
				return NativeConsumerIdentity{}, nil, ErrNativeUnauthorized
			}
			row := &ent.NativeConsumerSession{PublicID: id, UserID: parent.UserID, ParentSessionID: parent.ID, Purpose: nativeconsumersession.PurposeConfigurationApply, Origin: challenge.Origin, OperationID: bytes.Clone(challenge.OperationID), PublicKey: bytes.Clone(challenge.PublicKey), SessionBinding: bytes.Clone(challenge.SessionBinding), TokenHash: bytes.Clone(input.TokenHash.box.value[:]), IssuedMs: now.UnixMilli(), ExpiresMs: challenge.NativeExpiresMs}
			row.Charge = nativeSessionCharge(row)
			if err := nativeCheckQuota(ctx, tx, nativeUsageRow{1, parent.ID, parent.UserID, row.Charge}, budget); err != nil {
				return NativeConsumerIdentity{}, nil, err
			}
			count, err := tx.NativeConsumerChallenge.Update().Where(nativeconsumerchallenge.IDEQ(challenge.ID), nativeconsumerchallenge.UsedEQ(false)).SetUsed(true).Save(ctx)
			if err != nil {
				return NativeConsumerIdentity{}, nil, err
			}
			if count != 1 {
				return NativeConsumerIdentity{}, nil, ErrNativeUnauthorized
			}
			_, err = tx.NativeConsumerSession.Create().SetPublicID(row.PublicID).SetUserID(row.UserID).SetParentSessionID(row.ParentSessionID).SetPurpose(row.Purpose).SetOrigin(row.Origin).SetOperationID(row.OperationID).SetPublicKey(row.PublicKey).SetSessionBinding(row.SessionBinding).SetTokenHash(row.TokenHash).SetIssuedMs(row.IssuedMs).SetExpiresMs(row.ExpiresMs).SetCharge(row.Charge).Save(ctx)
			gate := func(instant time.Time) error {
				if err := nativeParentGate(parent, instant); err != nil {
					return err
				}
				if !nativeActive(time.UnixMilli(challenge.IssuedMs), time.UnixMilli(challenge.ExpiresMs), instant) || !nativeActive(time.UnixMilli(row.IssuedMs), time.UnixMilli(row.ExpiresMs), instant) {
					return ErrNativeUnauthorized
				}
				return nil
			}
			return nativeIdentity(row, parent.Edges.User.UserKey), gate, err
		})
	}
	result, err := run()
	if errors.Is(err, ErrNativeQuota) {
		if _, pruneErr := store.PruneNativeConsumers(ctx); pruneErr != nil {
			return NativeConsumerIdentity{}, pruneErr
		}
		return run()
	}
	return result, err
}

func nativeIdentity(row *ent.NativeConsumerSession, owner string) NativeConsumerIdentity {
	return NativeConsumerIdentity{nativeWrap(nativeIdentitySnapshot{ID: row.PublicID, Owner: owner, Origin: row.Origin, OperationID: [16]byte(row.OperationID), PublicKey: [32]byte(row.PublicKey), Binding: [32]byte(row.SessionBinding), ExpiresMS: row.ExpiresMs})}
}

func (store *Store) AuthenticateNativeConsumer(ctx context.Context, credential NativeConsumerCredential, origin string, operation []byte) (NativeConsumerIdentity, error) {
	if !credential.present() || len(operation) != 16 {
		return NativeConsumerIdentity{}, ErrNativeUnauthorized
	}
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativeConsumerIdentity, func(time.Time) error, error) {
		row, err := tx.NativeConsumerSession.Query().Where(nativeconsumersession.TokenHashEQ(credential.box.value[:])).WithUser().WithParentSession(func(query *ent.SessionQuery) { query.WithUser() }).Only(ctx)
		if err != nil {
			return NativeConsumerIdentity{}, nil, err
		}
		parent := row.Edges.ParentSession
		if !nativeSessionRecordValid(row) {
			return NativeConsumerIdentity{}, nil, ErrNativeUnavailable
		}
		if parent == nil || row.Edges.User == nil || parent.Edges.User == nil || row.UserID != parent.UserID || row.Edges.User.ID != parent.Edges.User.ID || row.Origin != origin || !bytes.Equal(row.OperationID, operation) || string(row.Purpose) != NativeConfigurationApply || row.Charge != nativeSessionCharge(row) || row.GateVersion == math.MaxInt64 {
			return NativeConsumerIdentity{}, nil, ErrNativeUnauthorized
		}
		gate := func(instant time.Time) error {
			if err := nativeParentGate(parent, instant); err != nil {
				return err
			}
			if !nativeActive(time.UnixMilli(row.IssuedMs), time.UnixMilli(row.ExpiresMs), instant) {
				return ErrNativeUnauthorized
			}
			return nil
		}
		if err := gate(store.state.now().UTC()); err != nil {
			return NativeConsumerIdentity{}, nil, err
		}
		count, err := tx.NativeConsumerSession.Update().Where(nativeconsumersession.IDEQ(row.ID), nativeconsumersession.GateVersionEQ(row.GateVersion)).AddGateVersion(1).Save(ctx)
		if err == nil && count != 1 {
			err = ErrNativeUnauthorized
		}
		return nativeIdentity(row, parent.Edges.User.UserKey), gate, err
	})
}

func nativeFailure(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, known := range []error{ErrNativeUnauthorized, ErrNativeInvalid, ErrNativeQuota, ErrNativeUnavailable, ErrNativeOutcomeUnknown} {
		if errors.Is(err, known) {
			return known
		}
	}
	if ent.IsNotFound(err) || ent.IsConstraintError(err) {
		return ErrNativeUnauthorized
	}
	return ErrNativeUnavailable
}

func (store *Store) nativeTransaction[Value any](ctx context.Context, operation func(*ent.Tx) (Value, func(time.Time) error, error)) (Value, error) {
	var zero Value
	for attempt := range catalogTransactionAttempts {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if attempt > 0 {
			timer := time.NewTimer(time.Millisecond << (attempt - 1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return zero, ctx.Err()
			case <-timer.C:
			}
		}
		tx, err := store.state.client.Tx(ctx)
		if err != nil {
			return zero, nativeFailure(ctx, err)
		}
		value, gate, err := operation(tx)
		if err != nil {
			rollbackErr := tx.Rollback()
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
			if rollbackErr != nil {
				return zero, ErrNativeUnavailable
			}
			if sqliteErr, ok := errors.AsType[*sqlite.Error](err); ok && sqliteErr.Code()&255 == sqlite3.SQLITE_BUSY {
				continue
			}
			return zero, nativeFailure(ctx, err)
		}
		attempted := false
		tx.OnCommit(func(next ent.Committer) ent.Committer {
			return ent.CommitFunc(func(ctx context.Context, tx *ent.Tx) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if gate != nil {
					if err := gate(store.state.now().UTC()); err != nil {
						return err
					}
				}
				attempted = true
				return next.Commit(ctx, tx)
			})
		})
		err = tx.Commit()
		if err == nil {
			return value, nil
		}
		_ = tx.Rollback()
		if attempted {
			return zero, errors.Join(ErrNativeOutcomeUnknown, ctx.Err())
		}
		return zero, nativeFailure(ctx, err)
	}
	return zero, ErrNativeUnavailable
}

type NativePruneResult struct {
	Roots int
	Rows  int
	Bytes int64
}
type nativePruneRoot struct {
	table   int
	id      int
	expires int64
	charge  int64
}

func (store *Store) PruneNativeConsumers(ctx context.Context) (NativePruneResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	return store.nativeTransaction(ctx, func(tx *ent.Tx) (NativePruneResult, func(time.Time) error, error) {
		now := store.state.now().UTC()
		used, err := tx.NativeConsumerChallenge.Query().Where(nativeconsumerchallenge.UsedEQ(true)).Order(ent.Asc(nativeconsumerchallenge.FieldExpiresMs), ent.Asc(nativeconsumerchallenge.FieldID)).Limit(64).All(ctx)
		if err != nil {
			return NativePruneResult{}, nil, err
		}
		expired, err := tx.NativeConsumerChallenge.Query().Where(nativeconsumerchallenge.UsedEQ(false), nativeconsumerchallenge.ExpiresMsLTE(now.UnixMilli())).Order(ent.Asc(nativeconsumerchallenge.FieldExpiresMs), ent.Asc(nativeconsumerchallenge.FieldID)).Limit(64).All(ctx)
		if err != nil {
			return NativePruneResult{}, nil, err
		}
		children, err := tx.NativeConsumerSession.Query().Where(nativeconsumersession.ExpiresMsLTE(now.UnixMilli())).Order(ent.Asc(nativeconsumersession.FieldExpiresMs), ent.Asc(nativeconsumersession.FieldID)).Limit(64).All(ctx)
		if err != nil {
			return NativePruneResult{}, nil, err
		}
		roots := make([]nativePruneRoot, 0, len(used)+len(expired)+len(children))
		for _, row := range append(used, expired...) {
			if !nativeChallengeRecordValid(row) {
				return NativePruneResult{}, nil, ErrNativeUnavailable
			}
			roots = append(roots, nativePruneRoot{0, row.ID, row.ExpiresMs, row.Charge})
		}
		for _, row := range children {
			if !nativeSessionRecordValid(row) {
				return NativePruneResult{}, nil, ErrNativeUnavailable
			}
			roots = append(roots, nativePruneRoot{1, row.ID, row.ExpiresMs, row.Charge})
		}
		slices.SortFunc(roots, func(first, second nativePruneRoot) int {
			return cmp.Or(cmp.Compare(first.expires, second.expires), cmp.Compare(first.id, second.id), cmp.Compare(first.table, second.table))
		})
		result := NativePruneResult{}
		for _, root := range roots {
			if result.Roots >= 64 || result.Rows >= 256 || root.charge > (1<<20)-result.Bytes {
				break
			}
			var count int
			instant := store.state.now().UTC()
			if root.table == 0 {
				count, err = tx.NativeConsumerChallenge.Delete().Where(nativeconsumerchallenge.IDEQ(root.id), nativeconsumerchallenge.Or(nativeconsumerchallenge.UsedEQ(true), nativeconsumerchallenge.ExpiresMsLTE(instant.UnixMilli()))).Exec(ctx)
			} else {
				count, err = tx.NativeConsumerSession.Delete().Where(nativeconsumersession.IDEQ(root.id), nativeconsumersession.ExpiresMsLTE(instant.UnixMilli())).Exec(ctx)
			}
			if err != nil {
				return NativePruneResult{}, nil, err
			}
			if count != 1 {
				continue
			}
			result.Roots++
			result.Rows++
			result.Bytes += root.charge
		}
		return result, nil, nil
	})
}
