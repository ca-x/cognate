package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
	"github.com/ca-x/cognate/internal/server/ent/collectedskill"
	"github.com/ca-x/cognate/internal/server/ent/configurationasset"
	"github.com/ca-x/cognate/internal/server/ent/importreceipt"
	"github.com/ca-x/cognate/internal/server/ent/session"
)

const ImportTicketBytes = 290
const importTicketLifetimeMS = 300000
const importTicketPurpose = "cognate/import-selection/v1/ticket"

var (
	ErrImportUnauthorized   = errors.New("invalid import authorization")
	ErrImportInvalid        = errors.New("invalid import request")
	ErrImportPrecondition   = errors.New("import precondition failed")
	ErrImportUnavailable    = errors.New("import storage unavailable")
	ErrImportConflict       = errors.New("import revision or input conflict")
	ErrImportQuota          = errors.New("import capacity exhausted")
	ErrImportOutcomeUnknown = errors.New("import transaction outcome unknown")
)

type importBox[Value any] struct{ value Value }
type importOpaque[Value any] struct{ box *importBox[Value] }

func importWrap[Value any](value Value) importOpaque[Value] {
	return importOpaque[Value]{box: &importBox[Value]{value: value}}
}

func (importOpaque[Value]) String() string   { return "[redacted import state]" }
func (importOpaque[Value]) GoString() string { return "[redacted import state]" }
func (importOpaque[Value]) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted import state]")
}
func (importOpaque[Value]) LogValue() slog.Value { return slog.StringValue("[redacted import state]") }
func (importOpaque[Value]) MarshalText() ([]byte, error) {
	return []byte("[redacted import state]"), nil
}
func (importOpaque[Value]) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted import state]"`), nil
}

type importOwnerCredential struct {
	owner  string
	digest [32]byte
	keyID  string
}

type ImportOwnerCredential struct {
	importOpaque[importOwnerCredential]
}

func (credential ImportOwnerCredential) Owner() string {
	if credential.box == nil {
		return ""
	}
	return credential.box.value.owner
}

type ImportSourceProfile uint8

const (
	ImportSourceCCSwitch24 ImportSourceProfile = 1
	ImportSourceMagpie     ImportSourceProfile = 2
)

type ImportTicketClaims struct {
	SourceProfile    ImportSourceProfile
	SourceDigest     [32]byte
	ProjectionDigest [32]byte
	AdapterDigest    [32]byte
	ResolutionDigest [32]byte
	OptionsDigest    [32]byte
}

type importTicket struct {
	issuerKeyID  string
	claims       ImportTicketClaims
	id           [16]byte
	issuedMS     int64
	expiresMS    int64
	ownerDigest  [32]byte
	originDigest [32]byte
}

type ImportTicket struct{ importOpaque[importTicket] }

func (ticket ImportTicket) Claims() (ImportTicketClaims, bool) {
	if ticket.box == nil {
		return ImportTicketClaims{}, false
	}
	return ticket.box.value.claims, true
}

func (ticket ImportTicket) ExpiresAt() time.Time {
	if ticket.box == nil {
		return time.Time{}
	}
	return time.UnixMilli(ticket.box.value.expiresMS)
}

func (store *Store) ImportCredentialForSession(ctx context.Context, digest [32]byte) (ImportOwnerCredential, error) {
	if err := ctx.Err(); err != nil {
		return ImportOwnerCredential{}, err
	}
	if store == nil || store.state == nil {
		return ImportOwnerCredential{}, ErrImportUnauthorized
	}
	owner, _, err := store.SessionUser(ctx, digest[:], store.state.now().UTC())
	if err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return ImportOwnerCredential{}, canceled
		}
		if errors.Is(err, ErrSessionInvalid) {
			return ImportOwnerCredential{}, ErrImportUnauthorized
		}
		return ImportOwnerCredential{}, ErrImportUnavailable
	}
	return ImportOwnerCredential{importWrap(importOwnerCredential{owner: owner, digest: digest, keyID: store.state.keyID})}, nil
}

func (store *Store) importOwner(ctx context.Context, sessions *ent.SessionClient, credential ImportOwnerCredential) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if credential.box == nil || credential.box.value.keyID != store.state.keyID {
		return "", ErrImportUnauthorized
	}
	value := credential.box.value
	entry, err := sessions.Query().Where(session.TokenHashEQ(value.digest[:])).WithUser().Only(ctx)
	if err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return "", canceled
		}
		if ent.IsNotFound(err) {
			return "", ErrImportUnauthorized
		}
		return "", ErrImportUnavailable
	}
	instant := store.state.now().UTC()
	if entry.Edges.User == nil || entry.Edges.User.UserKey != value.owner || instant.Before(entry.IssuedAt) || !instant.Before(entry.ExpiresAt) {
		return "", ErrImportUnauthorized
	}
	return value.owner, nil
}

func importTicketAAD(keyID, owner, origin string) []byte {
	result := []byte(importTicketPurpose)
	for _, field := range []string{keyID, owner, origin} {
		result = binary.BigEndian.AppendUint64(result, uint64(len(field)))
		result = append(result, field...)
	}
	return result
}

func importProfileValid(profile ImportSourceProfile) bool {
	return profile == ImportSourceCCSwitch24 || profile == ImportSourceMagpie
}

func importClaimsValid(claims ImportTicketClaims) bool {
	if !importProfileValid(claims.SourceProfile) {
		return false
	}
	for _, digest := range [][32]byte{claims.SourceDigest, claims.ProjectionDigest, claims.AdapterDigest, claims.ResolutionDigest, claims.OptionsDigest} {
		if digest == [32]byte{} {
			return false
		}
	}
	return true
}

func (store *Store) SealImportTicket(ctx context.Context, credential ImportOwnerCredential, origin string, claims ImportTicketClaims) ([]byte, error) {
	if !nativesessionauth.ValidServerOrigin(origin) {
		return nil, ErrImportPrecondition
	}
	if !importClaimsValid(claims) {
		return nil, ErrImportInvalid
	}
	if store == nil || store.state == nil {
		return nil, ErrImportUnauthorized
	}
	owner, err := store.importOwner(ctx, store.state.client.Session, credential)
	if err != nil {
		return nil, err
	}
	issuedMS := store.state.now().UTC().UnixMilli()
	if issuedMS <= 0 || issuedMS > math.MaxInt64-importTicketLifetimeMS {
		return nil, ErrImportPrecondition
	}
	var identifier [16]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		return nil, ErrImportUnavailable
	}
	plaintext := []byte{1, byte(claims.SourceProfile)}
	plaintext = binary.BigEndian.AppendUint64(plaintext, uint64(issuedMS))
	plaintext = binary.BigEndian.AppendUint64(plaintext, uint64(issuedMS+importTicketLifetimeMS))
	plaintext = append(plaintext, identifier[:]...)
	ownerDigest, originDigest := sha256.Sum256([]byte(owner)), sha256.Sum256([]byte(origin))
	for _, digest := range [][32]byte{ownerDigest, originDigest, claims.SourceDigest, claims.ProjectionDigest, claims.AdapterDigest, claims.ResolutionDigest, claims.OptionsDigest} {
		plaintext = append(plaintext, digest[:]...)
	}
	defer clear(plaintext)
	sealed := store.state.configurationAEAD.Seal([]byte("CIP1"), nil, plaintext, importTicketAAD(store.state.keyID, owner, origin))
	if err := ctx.Err(); err != nil {
		clear(sealed)
		return nil, err
	}
	if len(sealed) != ImportTicketBytes {
		clear(sealed)
		return nil, ErrImportUnavailable
	}
	return sealed, nil
}

func (store *Store) OpenImportTicket(ctx context.Context, credential ImportOwnerCredential, origin string, sealed []byte) (ImportTicket, error) {
	if !nativesessionauth.ValidServerOrigin(origin) {
		return ImportTicket{}, ErrImportPrecondition
	}
	if len(sealed) != ImportTicketBytes || string(sealed[:4]) != "CIP1" {
		return ImportTicket{}, ErrImportInvalid
	}
	if store == nil || store.state == nil {
		return ImportTicket{}, ErrImportUnauthorized
	}
	owner, err := store.importOwner(ctx, store.state.client.Session, credential)
	if err != nil {
		return ImportTicket{}, err
	}
	plaintext, err := store.state.configurationAEAD.Open(nil, nil, sealed[4:], importTicketAAD(store.state.keyID, owner, origin))
	if err != nil {
		return ImportTicket{}, ErrImportPrecondition
	}
	defer clear(plaintext)
	if len(plaintext) != 258 || plaintext[0] != 1 || !importProfileValid(ImportSourceProfile(plaintext[1])) {
		return ImportTicket{}, ErrImportPrecondition
	}
	issuedMS, expiresMS := binary.BigEndian.Uint64(plaintext[2:10]), binary.BigEndian.Uint64(plaintext[10:18])
	now := store.state.now().UTC().UnixMilli()
	if issuedMS == 0 || issuedMS > math.MaxInt64-importTicketLifetimeMS || expiresMS != issuedMS+importTicketLifetimeMS || now < 0 || uint64(now) < issuedMS || uint64(now) >= expiresMS {
		return ImportTicket{}, ErrImportPrecondition
	}
	ownerDigest, originDigest := sha256.Sum256([]byte(owner)), sha256.Sum256([]byte(origin))
	if subtle.ConstantTimeCompare(plaintext[34:66], ownerDigest[:]) != 1 || subtle.ConstantTimeCompare(plaintext[66:98], originDigest[:]) != 1 {
		return ImportTicket{}, ErrImportPrecondition
	}
	value := importTicket{issuerKeyID: store.state.keyID, issuedMS: int64(issuedMS), expiresMS: int64(expiresMS), id: [16]byte(plaintext[18:34]), ownerDigest: ownerDigest, originDigest: originDigest}
	value.claims = ImportTicketClaims{SourceProfile: ImportSourceProfile(plaintext[1]), SourceDigest: [32]byte(plaintext[98:130]), ProjectionDigest: [32]byte(plaintext[130:162]), AdapterDigest: [32]byte(plaintext[162:194]), ResolutionDigest: [32]byte(plaintext[194:226]), OptionsDigest: [32]byte(plaintext[226:258])}
	if !importClaimsValid(value.claims) {
		return ImportTicket{}, ErrImportPrecondition
	}
	if err := ctx.Err(); err != nil {
		return ImportTicket{}, err
	}
	return ImportTicket{importWrap(value)}, nil
}

type ImportReceiptPhase string

const (
	ImportReserved   ImportReceiptPhase = "reserved"
	ImportCollecting ImportReceiptPhase = "collecting"
	ImportCompleted  ImportReceiptPhase = "completed"
	ImportPartial    ImportReceiptPhase = "partial"
	ImportFailed     ImportReceiptPhase = "failed"
)

type ImportRepositoryOutcomeCode uint8

const (
	ImportRepositoryCollected ImportRepositoryOutcomeCode = iota + 1
	ImportRepositoryAlreadyCollected
	ImportRepositoryNotAttempted
	ImportRepositoryCancelled
	ImportRepositoryAuthorizationLost
	ImportRepositorySourceRejected
	ImportRepositoryCapacityExhausted
	ImportRepositoryUnknown
)

type ImportRepositoryOutcome struct {
	CandidateID  string
	Code         ImportRepositoryOutcomeCode
	RepositoryID string
}

func (outcome ImportRepositoryOutcome) succeeded() bool {
	return outcome.Code == ImportRepositoryCollected || outcome.Code == ImportRepositoryAlreadyCollected
}

type ImportSkillBinding struct {
	RepositoryID string
	ArchiveHash  string
	SkillID      string
	LogicalPath  string
	ContentHash  string
	Repository   string
	Branch       string
}

type ImportReceiptNoticeCode uint8

const (
	ImportCredentialsOmitted ImportReceiptNoticeCode = iota + 1
	ImportMetadataExcluded
	ImportOtherSourceDataExcluded
	ImportSourceContentNotCompared
	ImportUnknownSourceSectionExcluded
)

type ImportReceiptNotice struct {
	ID    string
	Code  ImportReceiptNoticeCode
	Count uint32
}

type ImportSelectionPlan struct {
	RepositoryIDs      []string
	SkillCount         int
	ConfigurationCount int
	Notices            []ImportReceiptNotice
}

type ImportReceiptView struct {
	OperationID        [16]byte
	Revision           int64
	Phase              ImportReceiptPhase
	RepositoryOutcomes []ImportRepositoryOutcome
	SelectedSkills     []ImportSkillBinding
	CreatedAssets      []ConfigurationAsset
	Notices            []ImportReceiptNotice
}

type importReceiptRecord struct {
	Version int
	Plan    ImportSelectionPlan
	View    ImportReceiptView
}

type importExecution struct {
	keyID     string
	owner     string
	operation [16]byte
	digest    [32]byte
	execution [16]byte
	revision  int64
}

type ImportReceiptExecution struct{ importOpaque[importExecution] }

func (execution ImportReceiptExecution) Granted() bool { return execution.box != nil }

type ImportReceiptReservation struct {
	Receipt   ImportReceiptView
	Execution ImportReceiptExecution
}

type ImportFinishSelection struct {
	Phase              ImportReceiptPhase
	RepositoryOutcomes []ImportRepositoryOutcome
	Skills             []ImportSkillBinding
	Drafts             []ConfigurationAssetDraft
}

func importTerminal(phase ImportReceiptPhase) bool {
	return phase == ImportCompleted || phase == ImportPartial || phase == ImportFailed
}

func importHexID(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func importPlanValid(plan ImportSelectionPlan) bool {
	if len(plan.RepositoryIDs) > 16 || plan.SkillCount < 0 || plan.SkillCount > 128 || plan.ConfigurationCount < 0 || plan.ConfigurationCount > 32 || len(plan.RepositoryIDs)+plan.SkillCount+plan.ConfigurationCount == 0 || len(plan.Notices) > 2048 {
		return false
	}
	seen := make(map[string]bool)
	for _, id := range plan.RepositoryIDs {
		if !importHexID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	clear(seen)
	for _, notice := range plan.Notices {
		if !importHexID(notice.ID) || seen[notice.ID] || notice.Code < ImportCredentialsOmitted || notice.Code > ImportUnknownSourceSectionExcluded || notice.Count == 0 {
			return false
		}
		seen[notice.ID] = true
	}
	return true
}

func importOutcomesValid(ids []string, outcomes []ImportRepositoryOutcome) bool {
	if len(ids) != len(outcomes) {
		return false
	}
	for index, outcome := range outcomes {
		if outcome.CandidateID != ids[index] || outcome.Code < ImportRepositoryCollected || outcome.Code > ImportRepositoryUnknown {
			return false
		}
		if outcome.succeeded() {
			if !validPackKey(outcome.RepositoryID) {
				return false
			}
		} else if outcome.RepositoryID != "" {
			return false
		}
	}
	return true
}

func importReceiptAAD(keyID, owner string, row *ent.ImportReceipt) []byte {
	result := []byte("cognate/import-selection/v1/receipt")
	for _, field := range [][]byte{[]byte(keyID), []byte(owner), row.OperationID, row.TicketID, row.RequestDigest, row.OriginDigest, row.ExecutionID, []byte(row.Phase)} {
		result = binary.BigEndian.AppendUint64(result, uint64(len(field)))
		result = append(result, field...)
	}
	return binary.BigEndian.AppendUint64(result, uint64(row.Revision))
}

func (store *Store) importSealRecord(owner string, row *ent.ImportReceipt, record importReceiptRecord) ([]byte, error) {
	encoded, err := json.Marshal(record)
	defer clear(encoded)
	if err != nil || len(encoded) > 120<<10 {
		return nil, ErrImportQuota
	}
	return store.state.configurationAEAD.Seal(nil, nil, encoded, importReceiptAAD(store.state.keyID, owner, row)), nil
}

func (store *Store) importReadRecord(owner string, row *ent.ImportReceipt) (importReceiptRecord, error) {
	if row.Revision < 1 || row.Charge != 131072 || len(row.OperationID) != 16 || len(row.TicketID) != 16 || len(row.ExecutionID) != 16 || len(row.RequestDigest) != 32 || len(row.OriginDigest) != 32 || len(row.Ciphertext) <= store.state.configurationAEAD.Overhead() || len(row.Ciphertext) > (120<<10)+store.state.configurationAEAD.Overhead() {
		return importReceiptRecord{}, ErrImportUnavailable
	}
	plaintext, err := store.state.configurationAEAD.Open(nil, nil, row.Ciphertext, importReceiptAAD(store.state.keyID, owner, row))
	if err != nil {
		return importReceiptRecord{}, ErrImportUnavailable
	}
	defer clear(plaintext)
	var record importReceiptRecord
	if json.Unmarshal(plaintext, &record, json.RejectUnknownMembers(true)) != nil || record.Version != 1 || !importPlanValid(record.Plan) || record.View.OperationID != [16]byte(row.OperationID) || record.View.Revision != row.Revision || record.View.Phase != ImportReceiptPhase(row.Phase) || !importOutcomesValid(record.Plan.RepositoryIDs, record.View.RepositoryOutcomes) || !slices.Equal(record.Plan.Notices, record.View.Notices) {
		return importReceiptRecord{}, ErrImportUnavailable
	}
	return record, nil
}

func importFailure(ctx context.Context, err error) error {
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	for _, known := range []error{ErrImportUnauthorized, ErrImportInvalid, ErrImportConflict, ErrImportPrecondition, ErrImportQuota, ErrImportUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	if ent.IsConstraintError(err) {
		return ErrImportConflict
	}
	return ErrImportUnavailable
}

func (store *Store) importTransaction[Value any](ctx context.Context, credential ImportOwnerCredential, operation func(*ent.Tx, string) (Value, error)) (result Value, outcomeErr error) {
	var zero Value
	if store == nil || store.state == nil {
		return zero, ErrImportUnauthorized
	}
	ctx, release, err := store.enterWriter(ctx)
	if err != nil {
		return zero, importFailure(ctx, err)
	}
	defer release()
	tx, err := store.state.client.Tx(ctx)
	if err != nil {
		return zero, importFailure(ctx, err)
	}
	committed := false
	defer func() {
		if !committed {
			if err := tx.Rollback(); err != nil {
				result = zero
				outcomeErr = errors.Join(ErrImportOutcomeUnknown, ctx.Err())
			}
		}
	}()
	owner, err := store.importOwner(ctx, tx.Session, credential)
	if err != nil {
		return zero, err
	}
	if err := store.guardSourceRefreshTransaction(ctx, tx); err != nil {
		return zero, importFailure(ctx, err)
	}
	value, err := operation(tx, owner)
	if err != nil {
		return zero, importFailure(ctx, err)
	}
	if _, err := store.importOwner(ctx, tx.Session, credential); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		return zero, errors.Join(ErrImportOutcomeUnknown, ctx.Err())
	}
	committed = true
	if err := ctx.Err(); err != nil {
		return zero, errors.Join(ErrImportOutcomeUnknown, err)
	}
	return value, nil
}

func (store *Store) ReserveImportReceipt(ctx context.Context, credential ImportOwnerCredential, origin string, ticket ImportTicket, operation [16]byte, digest [32]byte, plan ImportSelectionPlan) (ImportReceiptReservation, error) {
	if operation == [16]byte{} || digest == [32]byte{} || !nativesessionauth.ValidServerOrigin(origin) || !importPlanValid(plan) {
		return ImportReceiptReservation{}, ErrImportInvalid
	}
	return store.importTransaction(ctx, credential, func(tx *ent.Tx, owner string) (ImportReceiptReservation, error) {
		account, err := findUser(ctx, tx.Client(), owner)
		if err != nil {
			return ImportReceiptReservation{}, err
		}
		row, err := tx.ImportReceipt.Query().Where(importreceipt.UserIDEQ(account.ID), importreceipt.OperationIDEQ(operation[:])).Only(ctx)
		if err == nil {
			if subtle.ConstantTimeCompare(row.RequestDigest, digest[:]) != 1 {
				return ImportReceiptReservation{}, ErrImportConflict
			}
			record, err := store.importReadRecord(owner, row)
			return ImportReceiptReservation{Receipt: record.View}, err
		}
		if !ent.IsNotFound(err) {
			return ImportReceiptReservation{}, err
		}
		if ticket.box == nil {
			return ImportReceiptReservation{}, ErrImportPrecondition
		}
		claims := ticket.box.value
		now := store.state.now().UTC()
		if claims.issuerKeyID != store.state.keyID || claims.ownerDigest != sha256.Sum256([]byte(owner)) || claims.originDigest != sha256.Sum256([]byte(origin)) || now.UnixMilli() < claims.issuedMS || now.UnixMilli() >= claims.expiresMS {
			return ImportReceiptReservation{}, ErrImportPrecondition
		}
		ownerCount, err := tx.ImportReceipt.Query().Where(importreceipt.UserIDEQ(account.ID)).Count(ctx)
		if err != nil {
			return ImportReceiptReservation{}, err
		}
		globalCount, err := tx.ImportReceipt.Query().Count(ctx)
		if err != nil {
			return ImportReceiptReservation{}, err
		}
		if ownerCount >= 128 || globalCount >= 4096 {
			return ImportReceiptReservation{}, ErrImportQuota
		}
		var execution [16]byte
		if _, err := rand.Read(execution[:]); err != nil {
			return ImportReceiptReservation{}, ErrImportUnavailable
		}
		row = &ent.ImportReceipt{UserID: account.ID, OperationID: operation[:], TicketID: claims.id[:], RequestDigest: digest[:], OriginDigest: claims.originDigest[:], Revision: 1, Phase: importreceipt.PhaseReserved, ExecutionID: execution[:], CreatedAt: now, UpdatedAt: now, Charge: 131072}
		view := ImportReceiptView{OperationID: operation, Revision: 1, Phase: ImportReserved, Notices: slices.Clone(plan.Notices)}
		for _, id := range plan.RepositoryIDs {
			view.RepositoryOutcomes = append(view.RepositoryOutcomes, ImportRepositoryOutcome{CandidateID: id, Code: ImportRepositoryNotAttempted})
		}
		record := importReceiptRecord{Version: 1, Plan: plan, View: view}
		ciphertext, err := store.importSealRecord(owner, row, record)
		if err != nil {
			return ImportReceiptReservation{}, err
		}
		_, err = tx.ImportReceipt.Create().SetUserID(row.UserID).SetOperationID(row.OperationID).SetTicketID(row.TicketID).SetRequestDigest(row.RequestDigest).SetOriginDigest(row.OriginDigest).SetRevision(1).SetPhase(row.Phase).SetExecutionID(row.ExecutionID).SetCreatedAt(now).SetUpdatedAt(now).SetCiphertext(ciphertext).SetCharge(row.Charge).Save(ctx)
		if err != nil {
			return ImportReceiptReservation{}, err
		}
		granted := ImportReceiptExecution{importWrap(importExecution{keyID: store.state.keyID, owner: owner, operation: operation, digest: digest, execution: execution, revision: 1})}
		return ImportReceiptReservation{Receipt: view, Execution: granted}, nil
	})
}

func (store *Store) ReadImportReceipt(ctx context.Context, credential ImportOwnerCredential, operation [16]byte) (ImportReceiptView, bool, error) {
	if operation == [16]byte{} {
		return ImportReceiptView{}, false, ErrImportInvalid
	}
	type observed struct {
		view  ImportReceiptView
		found bool
	}
	result, err := store.importTransaction(ctx, credential, func(tx *ent.Tx, owner string) (observed, error) {
		account, err := findUser(ctx, tx.Client(), owner)
		if err != nil {
			return observed{}, err
		}
		row, err := tx.ImportReceipt.Query().Where(importreceipt.UserIDEQ(account.ID), importreceipt.OperationIDEQ(operation[:])).Only(ctx)
		if ent.IsNotFound(err) {
			return observed{}, nil
		}
		if err != nil {
			return observed{}, err
		}
		record, err := store.importReadRecord(owner, row)
		return observed{view: record.View, found: true}, err
	})
	return result.view, result.found, err
}

func (store *Store) importExecutionRow(ctx context.Context, tx *ent.Tx, owner string, execution ImportReceiptExecution) (*ent.ImportReceipt, importReceiptRecord, error) {
	if execution.box == nil {
		return nil, importReceiptRecord{}, ErrImportConflict
	}
	value := execution.box.value
	if value.owner != owner || value.keyID != store.state.keyID {
		return nil, importReceiptRecord{}, ErrImportUnauthorized
	}
	account, err := findUser(ctx, tx.Client(), owner)
	if err != nil {
		return nil, importReceiptRecord{}, err
	}
	row, err := tx.ImportReceipt.Query().Where(importreceipt.UserIDEQ(account.ID), importreceipt.OperationIDEQ(value.operation[:])).Only(ctx)
	if err != nil {
		return nil, importReceiptRecord{}, ErrImportConflict
	}
	if row.Revision != value.revision || importTerminal(ImportReceiptPhase(row.Phase)) || subtle.ConstantTimeCompare(row.RequestDigest, value.digest[:]) != 1 || subtle.ConstantTimeCompare(row.ExecutionID, value.execution[:]) != 1 {
		return nil, importReceiptRecord{}, ErrImportConflict
	}
	record, err := store.importReadRecord(owner, row)
	return row, record, err
}

func (store *Store) importSaveRecord(ctx context.Context, tx *ent.Tx, owner string, row *ent.ImportReceipt, record importReceiptRecord) (ImportReceiptView, error) {
	if row.Revision == math.MaxInt64 {
		return ImportReceiptView{}, ErrImportConflict
	}
	priorRevision := row.Revision
	row.Revision++
	row.Phase = importreceipt.Phase(record.View.Phase)
	record.View.Revision = row.Revision
	ciphertext, err := store.importSealRecord(owner, row, record)
	if err != nil {
		return ImportReceiptView{}, err
	}
	count, err := tx.ImportReceipt.Update().Where(importreceipt.IDEQ(row.ID), importreceipt.RevisionEQ(priorRevision), importreceipt.ExecutionIDEQ(row.ExecutionID)).SetRevision(row.Revision).SetPhase(row.Phase).SetCiphertext(ciphertext).SetUpdatedAt(store.state.now().UTC()).Save(ctx)
	if err != nil {
		return ImportReceiptView{}, err
	}
	if count != 1 {
		return ImportReceiptView{}, ErrImportConflict
	}
	return record.View, nil
}

func importOutcomesAdvance(previous, next []ImportRepositoryOutcome) bool {
	if len(previous) != len(next) {
		return false
	}
	for index, outcome := range previous {
		if outcome.Code != ImportRepositoryNotAttempted && outcome != next[index] {
			return false
		}
	}
	return true
}

func (store *Store) CheckpointImportReceipt(ctx context.Context, credential ImportOwnerCredential, execution ImportReceiptExecution, outcomes []ImportRepositoryOutcome) (ImportReceiptReservation, error) {
	return store.importTransaction(ctx, credential, func(tx *ent.Tx, owner string) (ImportReceiptReservation, error) {
		row, record, err := store.importExecutionRow(ctx, tx, owner, execution)
		if err != nil {
			return ImportReceiptReservation{}, err
		}
		if !importOutcomesValid(record.Plan.RepositoryIDs, outcomes) || !importOutcomesAdvance(record.View.RepositoryOutcomes, outcomes) {
			return ImportReceiptReservation{}, ErrImportInvalid
		}
		if err := importValidateRepositoryOutcomes(ctx, tx, row.UserID, outcomes); err != nil {
			return ImportReceiptReservation{}, err
		}
		record.View.RepositoryOutcomes = slices.Clone(outcomes)
		record.View.Phase = ImportCollecting
		view, err := store.importSaveRecord(ctx, tx, owner, row, record)
		if err != nil {
			return ImportReceiptReservation{}, err
		}
		updated := execution.box.value
		updated.revision = view.Revision
		return ImportReceiptReservation{Receipt: view, Execution: ImportReceiptExecution{importWrap(updated)}}, nil
	})
}

func importValidateRepositoryOutcomes(ctx context.Context, tx *ent.Tx, ownerID int, outcomes []ImportRepositoryOutcome) error {
	for _, outcome := range outcomes {
		if !outcome.succeeded() {
			continue
		}
		exists, err := tx.CollectedRepository.Query().Where(collectedrepository.PublicIDEQ(outcome.RepositoryID), collectedrepository.UserIDEQ(ownerID)).Exist(ctx)
		if err != nil {
			return err
		}
		if !exists {
			return ErrImportPrecondition
		}
	}
	return nil
}

func importFinalValid(plan ImportSelectionPlan, finish ImportFinishSelection) bool {
	if !importTerminal(finish.Phase) || !importOutcomesValid(plan.RepositoryIDs, finish.RepositoryOutcomes) {
		return false
	}
	allAssets := len(finish.Skills) == plan.SkillCount && len(finish.Drafts) == plan.ConfigurationCount
	noAssets := len(finish.Skills)+len(finish.Drafts) == 0
	if !allAssets && !noAssets {
		return false
	}
	successes := 0
	for _, outcome := range finish.RepositoryOutcomes {
		if outcome.succeeded() {
			successes++
		}
	}
	effects := successes + len(finish.Skills) + len(finish.Drafts)
	if finish.Phase == ImportCompleted {
		return allAssets && successes == len(plan.RepositoryIDs)
	}
	if finish.Phase == ImportFailed {
		return effects == 0
	}
	return effects > 0 && !(allAssets && successes == len(plan.RepositoryIDs))
}

func importValidateSkillPins(ctx context.Context, tx *ent.Tx, ownerID int, bindings []ImportSkillBinding) error {
	seen := make(map[string]bool)
	for _, binding := range bindings {
		if seen[binding.SkillID] {
			return ErrImportInvalid
		}
		seen[binding.SkillID] = true
		row, err := tx.CollectedSkill.Query().Where(collectedskill.PublicIDEQ(binding.SkillID)).WithRepository().Only(ctx)
		if ent.IsNotFound(err) {
			return ErrImportPrecondition
		}
		if err != nil {
			return err
		}
		repository := row.Edges.Repository
		if repository == nil || repository.UserID != ownerID || repository.PublicID != binding.RepositoryID || repository.ArchiveHash != binding.ArchiveHash || repository.NameWithOwner != binding.Repository || repository.Branch != binding.Branch || row.Path != binding.LogicalPath || row.ContentHash != binding.ContentHash {
			return ErrImportPrecondition
		}
	}
	return nil
}

func (store *Store) FinishImportSelection(ctx context.Context, credential ImportOwnerCredential, execution ImportReceiptExecution, finish ImportFinishSelection) (ImportReceiptView, error) {
	return store.importTransaction(ctx, credential, func(tx *ent.Tx, owner string) (ImportReceiptView, error) {
		row, record, err := store.importExecutionRow(ctx, tx, owner, execution)
		if err != nil {
			return ImportReceiptView{}, err
		}
		if !importFinalValid(record.Plan, finish) || !importOutcomesAdvance(record.View.RepositoryOutcomes, finish.RepositoryOutcomes) {
			return ImportReceiptView{}, ErrImportInvalid
		}
		if err := importValidateRepositoryOutcomes(ctx, tx, row.UserID, finish.RepositoryOutcomes); err != nil {
			return ImportReceiptView{}, err
		}
		if err := importValidateSkillPins(ctx, tx, row.UserID, finish.Skills); err != nil {
			return ImportReceiptView{}, err
		}
		account, err := findUser(ctx, tx.Client(), owner)
		if err != nil {
			return ImportReceiptView{}, err
		}
		for _, draft := range finish.Drafts {
			normalized, err := normalizeConfigurationDraft(draft)
			if err != nil || normalized.Name != draft.Name {
				return ImportReceiptView{}, ErrImportInvalid
			}
			exists, err := tx.ConfigurationAsset.Query().Where(configurationasset.UserIDEQ(row.UserID), configurationasset.AgentIDEQ(draft.AgentID), configurationasset.KindEQ(configurationasset.Kind(draft.Kind)), configurationasset.NameEQ(draft.Name)).Exist(ctx)
			if err != nil {
				return ImportReceiptView{}, err
			}
			if exists {
				return ImportReceiptView{}, ErrImportConflict
			}
			asset, err := store.createConfigurationAssetTx(ctx, tx, account, normalized)
			clear(normalized.Payload.data)
			if errors.Is(err, ErrConfigurationLimit) {
				return ImportReceiptView{}, ErrImportQuota
			}
			if err != nil {
				return ImportReceiptView{}, err
			}
			record.View.CreatedAssets = append(record.View.CreatedAssets, asset)
		}
		record.View.RepositoryOutcomes = slices.Clone(finish.RepositoryOutcomes)
		record.View.SelectedSkills = slices.Clone(finish.Skills)
		record.View.Phase = finish.Phase
		return store.importSaveRecord(ctx, tx, owner, row, record)
	})
}
