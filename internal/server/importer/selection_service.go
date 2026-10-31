package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
)

type SelectionCandidateStatus uint8

const (
	SelectionCandidateReady SelectionCandidateStatus = iota + 1
	SelectionCandidateNeedsChoice
	SelectionCandidateNeedsRepositoryImport
	SelectionCandidateUnresolved
	SelectionCandidateUnsupportedAgent
	SelectionCandidateUnsupportedKind
	SelectionCandidateUnsupportedField
	SelectionCandidateNameConflict
)

type SelectionRepositoryCandidate struct {
	ID                   string
	Repository           string
	Branch               string
	ExistingRepositoryID string
	SourceEnabled        *bool
	Status               SelectionCandidateStatus
}

type SelectionSkillCandidate struct {
	ID                    string
	SafeName              string
	RepositoryCandidateID string
	Status                SelectionCandidateStatus
	Binding               *state.ImportSkillBinding
	Notices               []state.ImportReceiptNotice
}

type SelectionConfigurationCandidate struct {
	ID              string
	KnownAgentID    string
	Kind            string
	SafeName        string
	Status          SelectionCandidateStatus
	Draft           *configuration.Draft
	CredentialSlots []SelectionSlot
	Notices         []state.ImportReceiptNotice
}

type SelectionPreviewView struct {
	ExpiresAt      time.Time
	Repositories   []SelectionRepositoryCandidate
	Skills         []SelectionSkillCandidate
	Configurations []SelectionConfigurationCandidate
	Notices        []state.ImportReceiptNotice
}

type SelectionCommitInput struct {
	Ticket           []byte
	Source           SelectionSource
	SourceData       []byte
	Options          SelectionOptions
	OperationID      [16]byte
	RepositoryIDs    []string
	SkillIDs         []string
	ConfigurationIDs []string
	AckNoticeIDs     []string
}

type selectionOwned[Value any] struct {
	mutex  sync.Mutex
	access func() *Value
}

func selectionOwn[Value any](value Value) *selectionOwned[Value] {
	return &selectionOwned[Value]{access: func() *Value { return &value }}
}

type SelectionCommitRequest struct {
	owned *selectionOwned[SelectionCommitInput]
}

func NewSelectionCommitRequest(input SelectionCommitInput) (SelectionCommitRequest, error) {
	if len(input.Ticket) != 290 || input.OperationID == [16]byte{} || len(input.Options.Candidates) > 160 || len(input.RepositoryIDs) > 16 || len(input.SkillIDs) > 128 || len(input.ConfigurationIDs) > 32 || len(input.AckNoticeIDs) > 2048 || len(input.RepositoryIDs)+len(input.SkillIDs)+len(input.ConfigurationIDs) == 0 {
		return SelectionCommitRequest{}, ErrSelectionInvalid
	}
	maximum := MaxMagpieExportBytes
	if input.Source == SelectionCCSwitch24 {
		maximum = MaxCCSwitchSnapshotBytes
	} else if input.Source != SelectionMagpie {
		return SelectionCommitRequest{}, ErrSelectionUnsupported
	}
	if len(input.SourceData) == 0 || len(input.SourceData) > maximum {
		return SelectionCommitRequest{}, ErrSelectionLimit
	}
	if _, err := selectionOptionsDigest(input.Options); err != nil {
		return SelectionCommitRequest{}, err
	}
	encoded, err := json.Marshal(input.Options)
	if err != nil {
		return SelectionCommitRequest{}, ErrSelectionInvalid
	}
	defer clear(encoded)
	if len(encoded) > MaxSelectionProjectionBytes {
		return SelectionCommitRequest{}, ErrSelectionLimit
	}
	var options SelectionOptions
	if json.Unmarshal(encoded, &options) != nil {
		return SelectionCommitRequest{}, ErrSelectionInvalid
	}
	input.Options = options
	input.Ticket, input.SourceData = bytes.Clone(input.Ticket), bytes.Clone(input.SourceData)
	input.RepositoryIDs = append([]string(nil), input.RepositoryIDs...)
	input.SkillIDs = append([]string(nil), input.SkillIDs...)
	input.ConfigurationIDs = append([]string(nil), input.ConfigurationIDs...)
	input.AckNoticeIDs = append([]string(nil), input.AckNoticeIDs...)
	return SelectionCommitRequest{selectionOwn(input)}, nil
}

func (request SelectionCommitRequest) Clone() SelectionCommitRequest {
	if request.owned == nil {
		return SelectionCommitRequest{}
	}
	request.owned.mutex.Lock()
	defer request.owned.mutex.Unlock()
	if request.owned.access == nil {
		return SelectionCommitRequest{}
	}
	clone, _ := NewSelectionCommitRequest(*request.owned.access())
	return clone
}

func (request SelectionCommitRequest) Destroy() {
	if request.owned == nil {
		return
	}
	request.owned.mutex.Lock()
	defer request.owned.mutex.Unlock()
	if request.owned.access != nil {
		value := request.owned.access()
		clear(value.Ticket)
		clear(value.SourceData)
		*value = SelectionCommitInput{}
		request.owned.access = nil
	}
}

type selectionPreviewData struct {
	view   SelectionPreviewView
	ticket []byte
}
type SelectionPreview struct {
	owned *selectionOwned[selectionPreviewData]
}

func (preview SelectionPreview) View() (SelectionPreviewView, error) {
	if preview.owned == nil {
		return SelectionPreviewView{}, ErrSelectionInvalid
	}
	preview.owned.mutex.Lock()
	defer preview.owned.mutex.Unlock()
	if preview.owned.access == nil {
		return SelectionPreviewView{}, ErrSelectionInvalid
	}
	encoded, err := json.Marshal(preview.owned.access().view)
	if err != nil {
		return SelectionPreviewView{}, ErrSelectionInvalid
	}
	defer clear(encoded)
	var view SelectionPreviewView
	err = json.Unmarshal(encoded, &view)
	return view, err
}

func (preview SelectionPreview) RevealTicket() []byte {
	if preview.owned == nil {
		return nil
	}
	preview.owned.mutex.Lock()
	defer preview.owned.mutex.Unlock()
	if preview.owned.access == nil {
		return nil
	}
	return bytes.Clone(preview.owned.access().ticket)
}

func (preview SelectionPreview) Clone() SelectionPreview {
	if preview.owned == nil {
		return SelectionPreview{}
	}
	preview.owned.mutex.Lock()
	defer preview.owned.mutex.Unlock()
	if preview.owned.access == nil {
		return SelectionPreview{}
	}
	value := preview.owned.access()
	encoded, err := json.Marshal(value.view)
	if err != nil {
		return SelectionPreview{}
	}
	defer clear(encoded)
	var view SelectionPreviewView
	if json.Unmarshal(encoded, &view) != nil {
		return SelectionPreview{}
	}
	return SelectionPreview{selectionOwn(selectionPreviewData{view, bytes.Clone(value.ticket)})}
}

func (preview SelectionPreview) Destroy() {
	if preview.owned == nil {
		return
	}
	preview.owned.mutex.Lock()
	defer preview.owned.mutex.Unlock()
	if preview.owned.access != nil {
		value := preview.owned.access()
		clear(value.ticket)
		*value = selectionPreviewData{}
		preview.owned.access = nil
	}
}

type SelectionObservationCode uint8

const (
	SelectionNotObserved SelectionObservationCode = iota + 1
	SelectionInProgress
	SelectionUnknown
	SelectionTerminal
)

type SelectionObservation struct {
	Observation SelectionObservationCode
	Receipt     *state.ImportReceiptView
}

func (service *SelectionService) Preview(call *SelectionCall, source SelectionSource, raw []byte, options SelectionOptions) (SelectionPreview, error) {
	exit, err := service.enter(call, SelectionPreviewMethod)
	if err != nil {
		return SelectionPreview{}, err
	}
	defer exit()
	ctx := call.ctx
	if source == SelectionMagpie {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, call.started.Add(5*time.Second))
		defer cancel()
	}
	maximum := MaxMagpieExportBytes
	if source == SelectionCCSwitch24 {
		maximum = MaxCCSwitchSnapshotBytes
	} else if source != SelectionMagpie {
		return SelectionPreview{}, ErrSelectionUnsupported
	}
	if len(raw) == 0 || len(raw) > maximum {
		return SelectionPreview{}, ErrSelectionLimit
	}
	raw = bytes.Clone(raw)
	defer clear(raw)
	resolved, err := service.resolve(ctx, call.owner, source, raw, options)
	if err != nil {
		return SelectionPreview{}, selectionServiceError(ctx, err)
	}
	defer resolved.destroy()
	ticket, err := service.store.SealImportTicket(ctx, call.credential, service.origin, resolved.claims)
	if err != nil {
		return SelectionPreview{}, selectionServiceError(ctx, err)
	}
	opened, err := service.store.OpenImportTicket(ctx, call.credential, service.origin, ticket)
	if err != nil {
		clear(ticket)
		return SelectionPreview{}, selectionServiceError(ctx, err)
	}
	resolved.preview.ExpiresAt = opened.ExpiresAt()
	encoded, err := json.Marshal(resolved.preview)
	if err != nil || len(encoded) > 2<<20 {
		clear(encoded)
		clear(ticket)
		return SelectionPreview{}, ErrSelectionLimit
	}
	clear(encoded)
	return SelectionPreview{selectionOwn(selectionPreviewData{resolved.preview, ticket})}, nil
}

func (service *SelectionService) Commit(call *SelectionCall, request SelectionCommitRequest) (state.ImportReceiptView, error) {
	exit, err := service.enter(call, SelectionCommitMethod)
	if err != nil {
		return state.ImportReceiptView{}, err
	}
	defer exit()
	owned := request.Clone()
	defer owned.Destroy()
	if owned.owned == nil {
		return state.ImportReceiptView{}, ErrSelectionInvalid
	}
	input := *owned.owned.access()
	ctx := call.ctx
	digest, err := selectionRequestDigest(call.owner, service.origin, input)
	if err != nil {
		return state.ImportReceiptView{}, selectionServiceError(ctx, err)
	}
	_, found, err := service.store.ReadImportReceipt(ctx, call.credential, input.OperationID)
	if err != nil {
		return state.ImportReceiptView{}, selectionServiceError(ctx, err)
	}
	plan := state.ImportSelectionPlan{RepositoryIDs: input.RepositoryIDs, SkillCount: len(input.SkillIDs), ConfigurationCount: len(input.ConfigurationIDs)}
	if found {
		observed, err := service.store.ReserveImportReceipt(ctx, call.credential, service.origin, state.ImportTicket{}, input.OperationID, digest, plan)
		if err != nil {
			return state.ImportReceiptView{}, selectionServiceError(ctx, err)
		}
		if observed.Execution.Granted() {
			return state.ImportReceiptView{}, state.ErrImportOutcomeUnknown
		}
		return observed.Receipt, nil
	}
	ticket, err := service.store.OpenImportTicket(ctx, call.credential, service.origin, input.Ticket)
	if err != nil {
		return state.ImportReceiptView{}, selectionServiceError(ctx, err)
	}
	resolved, err := service.resolve(ctx, call.owner, input.Source, input.SourceData, input.Options)
	if err != nil {
		return state.ImportReceiptView{}, selectionServiceError(ctx, err)
	}
	defer resolved.destroy()
	claims, valid := ticket.Claims()
	if !valid || claims != resolved.claims {
		return state.ImportReceiptView{}, state.ErrImportPrecondition
	}
	selected, err := selectionSelect(resolved.preview, input)
	if err != nil {
		return state.ImportReceiptView{}, selectionServiceError(ctx, err)
	}
	plan.Notices = selectionAllNotices(resolved.preview)
	reservation, err := service.store.ReserveImportReceipt(ctx, call.credential, service.origin, ticket, input.OperationID, digest, plan)
	if err != nil {
		return state.ImportReceiptView{}, selectionServiceError(ctx, err)
	}
	if !reservation.Execution.Granted() {
		return reservation.Receipt, nil
	}
	key := selectionLiveKey{call.owner, input.OperationID}
	service.mutex.Lock()
	service.live[key] = selectionLiveExecution{call: call, execution: reservation.Execution, view: reservation.Receipt}
	service.mutex.Unlock()
	defer func() {
		service.mutex.Lock()
		if service.live[key].call == call {
			delete(service.live, key)
		}
		service.mutex.Unlock()
	}()
	outcomes := slices.Clone(reservation.Receipt.RepositoryOutcomes)
	execution := reservation.Execution
	for index, candidate := range selected.repositories {
		if err := ctx.Err(); err != nil {
			return state.ImportReceiptView{}, err
		}
		current, exists, err := service.store.ReadImportReceipt(ctx, call.credential, input.OperationID)
		if err != nil {
			return state.ImportReceiptView{}, selectionServiceError(ctx, err)
		}
		if !exists || selectionTerminal(current.Phase) {
			return state.ImportReceiptView{}, state.ErrImportOutcomeUnknown
		}
		collected, collectErr := service.content.Collect(ctx, call.owner, candidate.Repository, candidate.Branch)
		if collectErr != nil {
			if errors.Is(collectErr, state.ErrImportOutcomeUnknown) {
				return state.ImportReceiptView{}, state.ErrImportOutcomeUnknown
			}
			if ctx.Err() != nil {
				return state.ImportReceiptView{}, ctx.Err()
			}
			if errors.Is(collectErr, state.ErrSourceAccessUnavailable) {
				return state.ImportReceiptView{}, ErrSelectionUnavailable
			}
			switch {
			case errors.Is(collectErr, repository.ErrArchiveInvalid), errors.Is(collectErr, repository.ErrArchiveTooLarge), errors.Is(collectErr, repository.ErrArchiveRateLimited), errors.Is(collectErr, repository.ErrArchiveUnavailable), errors.Is(collectErr, repository.ErrInvalidReference):
				outcomes[index].Code = state.ImportRepositorySourceRejected
			case errors.Is(collectErr, library.ErrCapacity):
				outcomes[index].Code = state.ImportRepositoryCapacityExhausted
			default:
				return state.ImportReceiptView{}, state.ErrImportOutcomeUnknown
			}
		} else {
			if err := ctx.Err(); err != nil {
				return state.ImportReceiptView{}, err
			}
			verified, err := service.store.CollectedRepository(ctx, call.owner, collected.ID)
			if err != nil || verified.NameWithOwner != candidate.Repository || verified.Branch != candidate.Branch || verified.ArchiveHash != collected.ArchiveHash {
				return state.ImportReceiptView{}, state.ErrImportOutcomeUnknown
			}
			outcomes[index].RepositoryID = verified.ID
			outcomes[index].Code = state.ImportRepositoryCollected
			if candidate.ExistingRepositoryID != "" {
				outcomes[index].Code = state.ImportRepositoryAlreadyCollected
			}
		}
		next, err := service.store.CheckpointImportReceipt(ctx, call.credential, execution, outcomes)
		if err != nil {
			return state.ImportReceiptView{}, selectionServiceError(ctx, err)
		}
		if !next.Execution.Granted() {
			return state.ImportReceiptView{}, state.ErrImportOutcomeUnknown
		}
		execution = next.Execution
		service.mutex.Lock()
		if service.live[key].call == call {
			service.live[key] = selectionLiveExecution{call: call, execution: next.Execution, view: next.Receipt}
		}
		service.mutex.Unlock()
	}
	finish := state.ImportFinishSelection{RepositoryOutcomes: outcomes, Skills: selected.skills}
	for _, candidate := range selected.configurations {
		if candidate.Draft == nil {
			return state.ImportReceiptView{}, state.ErrImportOutcomeUnknown
		}
		draft, err := configuration.PrepareImportedDraft(ctx, *candidate.Draft)
		if err != nil {
			return state.ImportReceiptView{}, selectionServiceError(ctx, err)
		}
		if draft.AgentID != candidate.KnownAgentID || string(draft.Kind) != candidate.Kind || draft.Name != candidate.SafeName {
			return state.ImportReceiptView{}, state.ErrImportOutcomeUnknown
		}
		finish.Drafts = append(finish.Drafts, draft)
	}
	finish.Phase = selectionFinishPhase(outcomes, len(finish.Drafts)+len(finish.Skills), true)
	view, err := service.store.FinishImportSelection(ctx, call.credential, execution, finish)
	if err == nil {
		return view, nil
	}
	if errors.Is(err, state.ErrImportOutcomeUnknown) || ctx.Err() != nil || errors.Is(err, state.ErrImportUnauthorized) {
		return state.ImportReceiptView{}, selectionServiceError(ctx, err)
	}
	if len(finish.Drafts)+len(finish.Skills) > 0 && selectionDefiniteRollback(err) {
		failure := state.ImportFinishSelection{Phase: selectionFinishPhase(outcomes, 0, false), RepositoryOutcomes: outcomes}
		terminal, finalErr := service.store.FinishImportSelection(ctx, call.credential, execution, failure)
		if finalErr != nil {
			return state.ImportReceiptView{}, selectionServiceError(ctx, finalErr)
		}
		return terminal, nil
	}
	return state.ImportReceiptView{}, selectionServiceError(ctx, err)
}

type selectionSelected struct {
	repositories   []SelectionRepositoryCandidate
	skills         []state.ImportSkillBinding
	configurations []SelectionConfigurationCandidate
}

func selectionSelect(view SelectionPreviewView, input SelectionCommitInput) (selectionSelected, error) {
	var result selectionSelected
	notices := selectionAllNotices(view)
	if len(input.AckNoticeIDs) != len(notices) {
		return result, state.ErrImportPrecondition
	}
	for index, notice := range notices {
		if input.AckNoticeIDs[index] != notice.ID {
			return result, state.ErrImportPrecondition
		}
	}
	for _, id := range input.RepositoryIDs {
		found := false
		for _, candidate := range view.Repositories {
			if candidate.ID == id {
				if candidate.Status != SelectionCandidateReady {
					return result, state.ErrImportPrecondition
				}
				result.repositories = append(result.repositories, candidate)
				found = true
				break
			}
		}
		if !found {
			return result, state.ErrImportPrecondition
		}
	}
	seenSkills := make(map[string]bool)
	for _, id := range input.SkillIDs {
		found := false
		for _, candidate := range view.Skills {
			if candidate.ID == id {
				if candidate.Status != SelectionCandidateReady || candidate.Binding == nil || seenSkills[candidate.Binding.SkillID] {
					return result, state.ErrImportPrecondition
				}
				seenSkills[candidate.Binding.SkillID] = true
				result.skills = append(result.skills, *candidate.Binding)
				found = true
				break
			}
		}
		if !found {
			return result, state.ErrImportPrecondition
		}
	}
	for _, id := range input.ConfigurationIDs {
		found := false
		for _, candidate := range view.Configurations {
			if candidate.ID == id {
				if candidate.Status != SelectionCandidateReady || candidate.Draft == nil {
					return result, state.ErrImportPrecondition
				}
				result.configurations = append(result.configurations, candidate)
				found = true
				break
			}
		}
		if !found {
			return result, state.ErrImportPrecondition
		}
	}
	return result, nil
}

func selectionRequestDigest(owner, origin string, input SelectionCommitInput) ([32]byte, error) {
	if len(input.Ticket) != 290 || input.OperationID == [16]byte{} || len(input.RepositoryIDs) > 16 || len(input.SkillIDs) > 128 || len(input.ConfigurationIDs) > 32 || len(input.AckNoticeIDs) > 2048 || len(input.RepositoryIDs)+len(input.SkillIDs)+len(input.ConfigurationIDs) == 0 {
		return [32]byte{}, ErrSelectionInvalid
	}
	options, err := selectionOptionsDigest(input.Options)
	if err != nil {
		return [32]byte{}, err
	}
	transcript := selectionTranscriptFor("cognate/import-selection/v1/request")
	transcript.text(owner)
	transcript.text(origin)
	transcript.blob(input.Ticket)
	transcript.text(string(input.Source))
	source := sha256.Sum256(input.SourceData)
	transcript.raw(source[:])
	transcript.raw(options[:])
	transcript.raw(input.OperationID[:])
	for _, group := range [][]string{input.RepositoryIDs, input.SkillIDs, input.ConfigurationIDs, input.AckNoticeIDs} {
		seen := make(map[string]bool)
		transcript.count(len(group))
		for _, id := range group {
			if !selectionHex(id) || seen[id] {
				return [32]byte{}, ErrSelectionInvalid
			}
			seen[id] = true
			transcript.text(id)
		}
	}
	return transcript.result()
}

func selectionFinishPhase(outcomes []state.ImportRepositoryOutcome, assets int, allAssets bool) state.ImportReceiptPhase {
	successes := 0
	for _, outcome := range outcomes {
		if outcome.Code == state.ImportRepositoryCollected || outcome.Code == state.ImportRepositoryAlreadyCollected {
			successes++
		}
	}
	if allAssets && successes == len(outcomes) {
		return state.ImportCompleted
	}
	if successes+assets > 0 {
		return state.ImportPartial
	}
	return state.ImportFailed
}

func selectionDefiniteRollback(err error) bool {
	for _, sentinel := range []error{state.ErrImportInvalid, state.ErrImportConflict, state.ErrImportPrecondition, state.ErrImportQuota, state.ErrImportUnavailable} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

func (service *SelectionService) Status(call *SelectionCall, operation [16]byte) (SelectionObservation, error) {
	exit, err := service.enter(call, SelectionStatusMethod)
	if err != nil {
		return SelectionObservation{}, err
	}
	defer exit()
	view, found, err := service.store.ReadImportReceipt(call.ctx, call.credential, operation)
	if err != nil {
		return SelectionObservation{}, selectionServiceError(call.ctx, err)
	}
	if !found {
		return SelectionObservation{Observation: SelectionNotObserved}, nil
	}
	observation := SelectionUnknown
	if selectionTerminal(view.Phase) {
		observation = SelectionTerminal
	} else {
		service.mutex.Lock()
		if current := service.live[selectionLiveKey{call.owner, operation}]; current.call != nil && current.execution.Granted() {
			observation = SelectionInProgress
		}
		service.mutex.Unlock()
	}
	return SelectionObservation{observation, &view}, nil
}

func selectionTerminal(phase state.ImportReceiptPhase) bool {
	return phase == state.ImportCompleted || phase == state.ImportPartial || phase == state.ImportFailed
}

func (SelectionCommitRequest) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted selection request]")
}
func (SelectionCommitRequest) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted selection request]"`), nil
}
func (SelectionCommitRequest) MarshalText() ([]byte, error) {
	return []byte("[redacted selection request]"), nil
}
func (SelectionCommitRequest) LogValue() slog.Value {
	return slog.StringValue("[redacted selection request]")
}
func (SelectionPreview) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted selection preview]")
}
func (SelectionPreview) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted selection preview]"`), nil
}
func (SelectionPreview) MarshalText() ([]byte, error) {
	return []byte("[redacted selection preview]"), nil
}
func (SelectionPreview) LogValue() slog.Value {
	return slog.StringValue("[redacted selection preview]")
}

//go:embed ccswitch.go magpie.go skills.go snapshot.go selection.go selection_snapshot.go source_claude.go source_codex.go source_gemini.go source_grok.go selection_service.go
var selectionHostSources embed.FS

type selectionTranscript struct {
	digest hash.Hash
	size   uint64
	err    error
}

func selectionTranscriptFor(domain string) *selectionTranscript {
	transcript := &selectionTranscript{digest: sha256.New()}
	transcript.text(domain)
	return transcript
}
func (transcript *selectionTranscript) raw(value []byte) {
	if transcript.err != nil {
		return
	}
	if uint64(len(value)) > 8<<20 || transcript.size+uint64(len(value)) > 8<<20 {
		transcript.err = ErrSelectionLimit
		return
	}
	transcript.size += uint64(len(value))
	_, _ = transcript.digest.Write(value)
}
func (transcript *selectionTranscript) count(value int) {
	if value < 0 || uint64(value) > math.MaxUint32 {
		transcript.err = ErrSelectionLimit
		return
	}
	transcript.raw(binary.BigEndian.AppendUint32(nil, uint32(value)))
}
func (transcript *selectionTranscript) text(value string) {
	transcript.count(len(value))
	transcript.raw([]byte(value))
}
func (transcript *selectionTranscript) blob(value []byte) {
	transcript.count(len(value))
	transcript.raw(value)
}
func (transcript *selectionTranscript) flag(value bool) {
	if value {
		transcript.raw([]byte{1})
	} else {
		transcript.raw([]byte{0})
	}
}
func (transcript *selectionTranscript) optional(value *string) {
	transcript.flag(value != nil)
	if value != nil {
		transcript.text(*value)
	}
}
func (transcript *selectionTranscript) result() ([32]byte, error) {
	return [32]byte(transcript.digest.Sum(nil)), transcript.err
}
func (transcript *selectionTranscript) hashText(value string) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != value {
		transcript.err = ErrSelectionInvalid
		return
	}
	transcript.raw(decoded)
}
func (transcript *selectionTranscript) binding(value *SelectionSkillBinding) {
	transcript.flag(value != nil)
	if value == nil {
		return
	}
	for _, field := range []string{value.RepositoryID, value.ArchiveHash, value.SkillID, value.Path, value.ContentHash, value.Repository, value.Branch} {
		transcript.text(field)
	}
}
func (transcript *selectionTranscript) notices(notices []SelectionNotice) {
	transcript.count(len(notices))
	for _, notice := range notices {
		transcript.text(string(notice.Code))
		transcript.count(notice.Count)
	}
}
func (transcript *selectionTranscript) draft(draft *configuration.Draft) {
	transcript.flag(draft != nil)
	if draft == nil {
		return
	}
	typed := 0
	for _, present := range []bool{draft.CodexProvider != nil, draft.GeminiProvider != nil, draft.GrokBuildProvider != nil} {
		if present {
			typed++
		}
	}
	if draft.OpenCodeProvider != nil || typed > 1 || typed != 0 && draft.Provider != (configuration.ClaudeProvider{}) {
		transcript.err = ErrSelectionInvalid
		return
	}
	transcript.text(draft.Name)
	if draft.CodexProvider != nil {
		transcript.raw([]byte{2})
		provider := draft.CodexProvider
		transcript.text(string(provider.AuthMode))
		transcript.text(provider.Endpoint)
		transcript.text(provider.Model)
		transcript.flag(provider.ReasoningEffort != nil)
		if provider.ReasoningEffort != nil {
			transcript.text(string(*provider.ReasoningEffort))
		}
		transcript.flag(provider.Catalog != nil)
		if provider.Catalog != nil {
			transcript.count(len(provider.Catalog.Models))
			for _, model := range provider.Catalog.Models {
				transcript.text(model.Model)
				transcript.optional(model.DisplayName)
				transcript.flag(model.ContextWindow != nil)
				if model.ContextWindow != nil {
					transcript.raw(binary.BigEndian.AppendUint64(nil, *model.ContextWindow))
				}
				transcript.flag(model.SupportsParallelToolCalls != nil)
				if model.SupportsParallelToolCalls != nil {
					transcript.flag(*model.SupportsParallelToolCalls)
				}
				transcript.flag(model.InputModalities != nil)
				if model.InputModalities != nil {
					transcript.count(len(*model.InputModalities))
					for _, value := range *model.InputModalities {
						transcript.text(string(value))
					}
				}
				transcript.optional(model.BaseInstructions)
				transcript.flag(model.ReasoningLevels != nil)
				if model.ReasoningLevels != nil {
					transcript.count(len(*model.ReasoningLevels))
					for _, value := range *model.ReasoningLevels {
						transcript.text(string(value))
					}
				}
				transcript.flag(model.DefaultReasoningLevel != nil)
				if model.DefaultReasoningLevel != nil {
					transcript.text(string(*model.DefaultReasoningLevel))
				}
			}
		}
	} else if draft.GeminiProvider != nil {
		transcript.raw([]byte{3})
		provider := draft.GeminiProvider
		transcript.text(string(provider.AuthMode))
		transcript.optional(provider.Endpoint)
		transcript.flag(provider.KeyTransport != nil)
		if provider.KeyTransport != nil {
			transcript.text(string(*provider.KeyTransport))
		}
		transcript.optional(provider.EnvModel)
		transcript.optional(provider.SettingsModel)
	} else if draft.GrokBuildProvider != nil {
		transcript.raw([]byte{4})
		provider := draft.GrokBuildProvider
		transcript.text(string(provider.Mode))
		transcript.flag(provider.Model != nil)
		if model := provider.Model; model != nil {
			transcript.text(model.Profile)
			transcript.text(model.UpstreamModel)
			transcript.text(model.Endpoint)
			transcript.text(model.Name)
			transcript.text(model.APIBackend)
			transcript.raw(binary.BigEndian.AppendUint64(nil, uint64(model.ContextWindow)))
			transcript.optional(model.EnvKey)
		}
	} else {
		transcript.raw([]byte{1})
		provider := draft.Provider
		transcript.optional(provider.Endpoint)
		transcript.text(string(provider.AuthMode))
		for _, model := range []*string{provider.Models.Default, provider.Models.Haiku, provider.Models.Sonnet, provider.Models.Opus} {
			transcript.optional(model)
		}
	}
}

func selectionProjectionDigest(view SelectionView) ([32]byte, error) {
	transcript := selectionTranscriptFor("cognate/import-selection/v1/projection")
	transcript.text(string(view.Source))
	transcript.hashText(view.SourceDigest)
	transcript.count(len(view.Repositories))
	for _, candidate := range view.Repositories {
		transcript.text(candidate.ID)
		transcript.text(candidate.Repository)
		transcript.text(candidate.Branch)
		transcript.flag(candidate.SourceEnabled != nil)
		if candidate.SourceEnabled != nil {
			transcript.flag(*candidate.SourceEnabled)
		}
	}
	transcript.count(len(view.Skills))
	for _, candidate := range view.Skills {
		for _, value := range []string{candidate.ID, candidate.Name, candidate.Repository, candidate.Branch} {
			transcript.text(value)
		}
		transcript.optional(candidate.Path)
		transcript.text(string(candidate.Status))
		transcript.binding(candidate.Binding)
		transcript.notices(candidate.Notices)
	}
	transcript.count(len(view.Configurations))
	for _, candidate := range view.Configurations {
		for _, value := range []string{candidate.ID, candidate.AgentID, candidate.Kind, candidate.Name, string(candidate.Status)} {
			transcript.text(value)
		}
		transcript.draft(candidate.Draft)
		transcript.count(len(candidate.Slots))
		for _, slot := range candidate.Slots {
			transcript.text(slot.Slot)
			transcript.flag(slot.Present)
		}
		transcript.notices(candidate.Notices)
	}
	transcript.notices(view.Notices)
	return transcript.result()
}

func selectionOptionsDigest(options SelectionOptions) ([32]byte, error) {
	if len(options.Candidates) > 160 {
		return [32]byte{}, ErrSelectionLimit
	}
	transcript := selectionTranscriptFor("cognate/import-selection/v1/options")
	transcript.count(len(options.Candidates))
	seen := make(map[string]bool)
	for _, option := range options.Candidates {
		if !selectionHex(option.CandidateID) || seen[option.CandidateID] || option.ClaudeAuthMode != nil && option.GeminiAuthMode != nil {
			return [32]byte{}, ErrSelectionInvalid
		}
		seen[option.CandidateID] = true
		if option.Name != nil && !selectionName(*option.Name) {
			return [32]byte{}, ErrSelectionInvalid
		}
		if option.ClaudeAuthMode != nil && *option.ClaudeAuthMode != configuration.NativeLogin && *option.ClaudeAuthMode != configuration.APIKey && *option.ClaudeAuthMode != configuration.AuthToken {
			return [32]byte{}, ErrSelectionInvalid
		}
		if option.GeminiAuthMode != nil && len(*option.GeminiAuthMode) > 64 || option.GeminiKeyTransport != nil && len(*option.GeminiKeyTransport) > 64 {
			return [32]byte{}, ErrSelectionInvalid
		}
		if binding := option.SkillBinding; binding != nil {
			if len(binding.RepositoryID) > 128 || len(binding.SkillID) > 128 || !selectionHex(binding.ArchiveHash) || !selectionHex(binding.ContentHash) || !selectionText(binding.Path, 1024) || !selectionText(binding.Repository, 512) || len(binding.Branch) > 256 {
				return [32]byte{}, ErrSelectionInvalid
			}
		}
		transcript.text(option.CandidateID)
		transcript.optional(option.Name)
		transcript.flag(option.ClaudeAuthMode != nil)
		if option.ClaudeAuthMode != nil {
			transcript.text(string(*option.ClaudeAuthMode))
		}
		transcript.flag(option.GeminiAuthMode != nil)
		if option.GeminiAuthMode != nil {
			transcript.text(string(*option.GeminiAuthMode))
		}
		transcript.flag(option.GeminiKeyTransport != nil)
		if option.GeminiKeyTransport != nil {
			transcript.text(string(*option.GeminiKeyTransport))
		}
		transcript.binding(option.SkillBinding)
	}
	return transcript.result()
}

func selectionHostDigest() ([32]byte, error) {
	transcript := selectionTranscriptFor("cognate/import-selection/v1/go-host-source")
	paths := []string{"ccswitch.go", "magpie.go", "skills.go", "snapshot.go", "selection.go", "selection_snapshot.go", "source_claude.go", "source_codex.go", "source_gemini.go", "source_grok.go", "selection_service.go"}
	transcript.count(len(paths))
	for _, path := range paths {
		data, err := selectionHostSources.ReadFile(path)
		if err != nil {
			return [32]byte{}, ErrSelectionUnavailable
		}
		transcript.text(path)
		transcript.blob(data)
	}
	return transcript.result()
}

func selectionBoundAdapterDigest(source SelectionSource) ([32]byte, error) {
	module, err := SelectionAdapterDigest(source)
	if err != nil {
		return [32]byte{}, err
	}
	host, err := selectionHostDigest()
	if err != nil {
		return [32]byte{}, err
	}
	transcript := selectionTranscriptFor("cognate/import-selection/v1/adapter-host")
	transcript.text(string(source))
	transcript.hashText(module)
	transcript.raw(host[:])
	return transcript.result()
}

func selectionHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

type selectionLiveKey struct {
	owner     string
	operation [16]byte
}
type selectionLiveExecution struct {
	call      *SelectionCall
	execution state.ImportReceiptExecution
	view      state.ImportReceiptView
}
type selectionResolution struct {
	preview      SelectionPreviewView
	claims       state.ImportTicketClaims
	repositories []state.CollectedRepository
	conflicts    [][]state.ConfigurationAsset
}

func (resolution *selectionResolution) destroy() {
	resolution.repositories = nil
	resolution.conflicts = nil
}

func selectionStatus(status SelectionStatus) SelectionCandidateStatus {
	switch status {
	case SelectionReady:
		return SelectionCandidateReady
	case SelectionNeedsChoice:
		return SelectionCandidateNeedsChoice
	case SelectionNeedsBinding:
		return SelectionCandidateUnresolved
	case SelectionUnsupportedAgent:
		return SelectionCandidateUnsupportedAgent
	case SelectionUnsupportedKind:
		return SelectionCandidateUnsupportedKind
	case SelectionUnsupportedField:
		return SelectionCandidateUnsupportedField
	default:
		return SelectionCandidateUnresolved
	}
}

func selectionStatusText(status SelectionCandidateStatus) string {
	switch status {
	case SelectionCandidateReady:
		return "ready"
	case SelectionCandidateNeedsChoice:
		return "needs-choice"
	case SelectionCandidateNeedsRepositoryImport:
		return "needs-repository-import"
	case SelectionCandidateUnresolved:
		return "unresolved"
	case SelectionCandidateUnsupportedAgent:
		return "unsupported-agent"
	case SelectionCandidateUnsupportedKind:
		return "unsupported-kind"
	case SelectionCandidateUnsupportedField:
		return "unsupported-field"
	case SelectionCandidateNameConflict:
		return "name-conflict"
	default:
		return "invalid"
	}
}

func selectionBinding(binding SelectionSkillBinding) state.ImportSkillBinding {
	return state.ImportSkillBinding{RepositoryID: binding.RepositoryID, ArchiveHash: binding.ArchiveHash, SkillID: binding.SkillID, LogicalPath: binding.Path, ContentHash: binding.ContentHash, Repository: binding.Repository, Branch: binding.Branch}
}

func selectionSourceBinding(binding state.ImportSkillBinding) *SelectionSkillBinding {
	return &SelectionSkillBinding{RepositoryID: binding.RepositoryID, ArchiveHash: binding.ArchiveHash, SkillID: binding.SkillID, Path: binding.LogicalPath, ContentHash: binding.ContentHash, Repository: binding.Repository, Branch: binding.Branch}
}

func selectionNoticeID(digest, kind, candidate string, code state.ImportReceiptNoticeCode, count int) string {
	transcript := selectionTranscriptFor("cognate/import-selection/v1/notice")
	transcript.hashText(digest)
	transcript.text(kind)
	transcript.text(candidate)
	transcript.text(selectionNoticeText(code))
	transcript.count(count)
	result, _ := transcript.result()
	return hex.EncodeToString(result[:])
}

func selectionNoticeText(code state.ImportReceiptNoticeCode) string {
	switch code {
	case state.ImportCredentialsOmitted:
		return "credentials-omitted"
	case state.ImportMetadataExcluded:
		return "metadata-excluded"
	case state.ImportOtherSourceDataExcluded:
		return "other-source-data-excluded"
	case state.ImportSourceContentNotCompared:
		return "source-content-not-compared"
	case state.ImportUnknownSourceSectionExcluded:
		return "unknown-source-section-excluded"
	default:
		return "invalid"
	}
}

func selectionMapNotices(digest, kind, candidate string, notices []SelectionNotice) ([]state.ImportReceiptNotice, error) {
	var result []state.ImportReceiptNotice
	for _, notice := range notices {
		var code state.ImportReceiptNoticeCode
		switch notice.Code {
		case SelectionCredentialsOmitted:
			code = state.ImportCredentialsOmitted
		case SelectionMetadataExcluded:
			code = state.ImportMetadataExcluded
		case SelectionSourceDataExcluded:
			code = state.ImportOtherSourceDataExcluded
		case SelectionContentNotCompared:
			code = state.ImportSourceContentNotCompared
		default:
			return nil, ErrSelectionInvalid
		}
		if notice.Count <= 0 || uint64(notice.Count) > math.MaxUint32 {
			return nil, ErrSelectionInvalid
		}
		result = append(result, state.ImportReceiptNotice{ID: selectionNoticeID(digest, kind, candidate, code, notice.Count), Code: code, Count: uint32(notice.Count)})
	}
	return result, nil
}

func selectionGlobalNotices(ctx context.Context, source SelectionSource, raw []byte, view SelectionView) ([]state.ImportReceiptNotice, error) {
	counts := [3]int{}
	if source == SelectionCCSwitch24 {
		err := readSelectionSnapshot(ctx, raw, func(ctx context.Context, connection *sql.Conn) error {
			return connection.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT IN ('skill_repos','skills','providers','provider_endpoints','mcp_servers','prompts')`).Scan(&counts[2])
		})
		if err != nil {
			return nil, err
		}
	} else {
		decoder := jsontext.NewDecoder(bytes.NewReader(raw))
		if token, err := decoder.ReadToken(); err != nil || token.Kind() != '{' {
			return nil, ErrSelectionInvalid
		}
		for decoder.PeekKind() != '}' {
			key, err := decoder.ReadToken()
			if err != nil {
				return nil, ErrSelectionInvalid
			}
			field := strings.Clone(key.String())
			value, err := decoder.ReadValue()
			if err != nil {
				return nil, ErrSelectionInvalid
			}
			switch field {
			case "skills", "mcp":
			case "instructions":
				object, err := sourceObject(value)
				if err != nil {
					return nil, err
				}
				if !sourceFields(object, "sets", "agents") {
					counts[1]++
				}
				clearSourceObject(object)
			case "applied", "seenSkills":
				counts[0]++
			default:
				counts[2]++
			}
		}
	}
	total := 0
	for _, notice := range view.Notices {
		if notice.Code != SelectionSourceDataExcluded || notice.Count <= 0 {
			return nil, ErrSelectionInvalid
		}
		total += notice.Count
	}
	if total != counts[0]+counts[1]+counts[2] {
		return nil, ErrSelectionInvalid
	}
	var result []state.ImportReceiptNotice
	for index, code := range []state.ImportReceiptNoticeCode{state.ImportMetadataExcluded, state.ImportOtherSourceDataExcluded, state.ImportUnknownSourceSectionExcluded} {
		if counts[index] > 0 {
			result = append(result, state.ImportReceiptNotice{ID: selectionNoticeID(view.SourceDigest, "global", "", code, counts[index]), Code: code, Count: uint32(counts[index])})
		}
	}
	return result, nil
}

func (service *SelectionService) resolve(ctx context.Context, owner string, source SelectionSource, raw []byte, options SelectionOptions) (selectionResolution, error) {
	var result selectionResolution
	optionsDigest, err := selectionOptionsDigest(options)
	if err != nil {
		return result, err
	}
	projection, err := ProjectSelection(ctx, source, raw)
	if err != nil {
		return result, err
	}
	defer projection.Destroy()
	candidates, err := BuildSelection(ctx, projection, options)
	if err != nil {
		return result, err
	}
	defer candidates.Destroy()
	view, err := candidates.View()
	if err != nil {
		return result, err
	}
	projectionDigest, err := selectionProjectionDigest(view)
	if err != nil {
		return result, err
	}
	adapterDigest, err := selectionBoundAdapterDigest(source)
	if err != nil {
		return result, err
	}
	globalNotices, err := selectionGlobalNotices(ctx, source, raw, view)
	if err != nil {
		return result, err
	}
	result.preview.Notices = globalNotices
	headers, err := service.store.ListCollectedRepositories(ctx, owner)
	if err != nil {
		return result, err
	}
	if len(headers) > 1000 {
		return result, ErrSelectionLimit
	}
	assets, err := service.store.ListConfigurationAssets(ctx, owner)
	if err != nil {
		return result, err
	}
	if len(assets) > 250 {
		return result, ErrSelectionLimit
	}
	for _, candidate := range view.Repositories {
		resolved := SelectionRepositoryCandidate{ID: candidate.ID, Repository: candidate.Repository, Branch: candidate.Branch, SourceEnabled: candidate.SourceEnabled, Status: SelectionCandidateReady}
		var observed state.CollectedRepository
		for _, header := range headers {
			if header.NameWithOwner == candidate.Repository && header.Branch == candidate.Branch {
				observed = header
				resolved.ExistingRepositoryID = header.ID
				break
			}
		}
		result.repositories = append(result.repositories, observed)
		result.preview.Repositories = append(result.preview.Repositories, resolved)
	}
	for _, candidate := range view.Skills {
		notices, err := selectionMapNotices(view.SourceDigest, "skill", candidate.ID, candidate.Notices)
		if err != nil {
			return result, err
		}
		resolved := SelectionSkillCandidate{ID: candidate.ID, SafeName: candidate.Name, Status: selectionStatus(candidate.Status), Notices: notices}
		for _, repo := range view.Repositories {
			if repo.Repository == candidate.Repository && repo.Branch == candidate.Branch {
				resolved.RepositoryCandidateID = repo.ID
				break
			}
		}
		if resolved.Status == SelectionCandidateReady || resolved.Status == SelectionCandidateUnresolved {
			resolved.Status = SelectionCandidateNeedsRepositoryImport
		}
		result.preview.Skills = append(result.preview.Skills, resolved)
	}
	for _, header := range headers {
		relevant := false
		for _, candidate := range view.Skills {
			if candidate.Repository == header.NameWithOwner && candidate.Branch == header.Branch {
				relevant = true
				break
			}
		}
		if !relevant {
			continue
		}
		current, err := service.store.CollectedRepository(ctx, owner, header.ID)
		if err != nil {
			return result, err
		}
		if len(current.Skills) > 512 {
			return result, ErrSelectionLimit
		}
		for index, candidate := range view.Skills {
			if candidate.Repository != current.NameWithOwner || candidate.Branch != current.Branch {
				continue
			}
			resolved := &result.preview.Skills[index]
			if resolved.Status != SelectionCandidateNeedsRepositoryImport {
				continue
			}
			resolved.Status = SelectionCandidateUnresolved
			var matches []state.ImportSkillBinding
			for _, skill := range current.Skills {
				binding := state.ImportSkillBinding{RepositoryID: current.ID, ArchiveHash: current.ArchiveHash, SkillID: skill.ID, LogicalPath: skill.Path, ContentHash: skill.ContentHash, Repository: current.NameWithOwner, Branch: current.Branch}
				if source == SelectionMagpie && candidate.Path != nil && *candidate.Path == skill.Path || source == SelectionCCSwitch24 && candidate.Binding != nil && selectionBinding(*candidate.Binding) == binding {
					matches = append(matches, binding)
				}
			}
			if len(matches) == 1 {
				resolved.Status = SelectionCandidateReady
				resolved.Binding = &matches[0]
			}
		}
	}
	for _, candidate := range view.Configurations {
		notices, err := selectionMapNotices(view.SourceDigest, "configuration", candidate.ID, candidate.Notices)
		if err != nil {
			return result, err
		}
		resolved := SelectionConfigurationCandidate{ID: candidate.ID, KnownAgentID: candidate.AgentID, Kind: candidate.Kind, SafeName: candidate.Name, Status: selectionStatus(candidate.Status), CredentialSlots: candidate.Slots, Notices: notices}
		var conflicts []state.ConfigurationAsset
		for _, asset := range assets {
			if asset.AgentID == candidate.AgentID && string(asset.Kind) == candidate.Kind && asset.Name == candidate.Name {
				conflicts = append(conflicts, asset)
			}
		}
		slices.SortFunc(conflicts, func(first, second state.ConfigurationAsset) int { return strings.Compare(first.ID, second.ID) })
		if resolved.Status == SelectionCandidateReady || resolved.Status == SelectionCandidateNeedsChoice {
			if len(conflicts) > 0 {
				resolved.Status = SelectionCandidateNameConflict
			}
			for _, other := range view.Configurations {
				if other.ID != candidate.ID && other.AgentID == candidate.AgentID && other.Kind == candidate.Kind && other.Name == candidate.Name {
					resolved.Status = SelectionCandidateNameConflict
				}
			}
		}
		if resolved.Status == SelectionCandidateReady {
			resolved.Draft = candidate.Draft
		}
		result.conflicts = append(result.conflicts, conflicts)
		result.preview.Configurations = append(result.preview.Configurations, resolved)
	}
	resolutionDigest, err := selectionResolutionDigest(owner, result)
	if err != nil {
		return result, err
	}
	profile := state.ImportSourceMagpie
	if source == SelectionCCSwitch24 {
		profile = state.ImportSourceCCSwitch24
	}
	result.claims = state.ImportTicketClaims{SourceProfile: profile, SourceDigest: sha256.Sum256(raw), ProjectionDigest: projectionDigest, AdapterDigest: adapterDigest, ResolutionDigest: resolutionDigest, OptionsDigest: optionsDigest}
	if len(selectionAllNotices(result.preview)) > 2048 {
		return result, ErrSelectionLimit
	}
	return result, ctx.Err()
}

func selectionAllNotices(view SelectionPreviewView) []state.ImportReceiptNotice {
	result := slices.Clone(view.Notices)
	for _, candidate := range view.Skills {
		result = append(result, candidate.Notices...)
	}
	for _, candidate := range view.Configurations {
		result = append(result, candidate.Notices...)
	}
	return result
}

func selectionResolutionDigest(owner string, resolution selectionResolution) ([32]byte, error) {
	transcript := selectionTranscriptFor("cognate/import-selection/v1/resolution")
	transcript.text(owner)
	transcript.count(len(resolution.preview.Repositories))
	for index, candidate := range resolution.preview.Repositories {
		transcript.text(candidate.ID)
		observed := resolution.repositories[index]
		transcript.flag(observed.ID != "")
		if observed.ID != "" {
			for _, value := range []string{observed.ID, observed.ArchiveHash, observed.NameWithOwner, observed.Branch} {
				transcript.text(value)
			}
		}
	}
	transcript.count(len(resolution.preview.Skills))
	for _, candidate := range resolution.preview.Skills {
		transcript.text(candidate.ID)
		transcript.text(selectionStatusText(candidate.Status))
		var binding *SelectionSkillBinding
		if candidate.Binding != nil {
			binding = selectionSourceBinding(*candidate.Binding)
		}
		transcript.binding(binding)
	}
	transcript.count(len(resolution.preview.Configurations))
	for index, candidate := range resolution.preview.Configurations {
		transcript.text(candidate.ID)
		transcript.text(selectionStatusText(candidate.Status))
		transcript.text(candidate.KnownAgentID)
		transcript.text(candidate.Kind)
		transcript.text(candidate.SafeName)
		transcript.count(len(resolution.conflicts[index]))
		for _, asset := range resolution.conflicts[index] {
			transcript.text(asset.ID)
			transcript.raw(binary.BigEndian.AppendUint64(nil, uint64(asset.Revision)))
		}
	}
	notices := selectionAllNotices(resolution.preview)
	transcript.count(len(notices))
	for _, notice := range notices {
		transcript.text(notice.ID)
		transcript.text(selectionNoticeText(notice.Code))
		transcript.count(int(notice.Count))
	}
	return transcript.result()
}

type SelectionMethod uint8

const (
	SelectionPreviewMethod SelectionMethod = iota + 1
	SelectionCommitMethod
	SelectionStatusMethod
)

var (
	ErrSelectionBusy        = errors.New("import selection capacity exhausted")
	ErrSelectionClosed      = errors.New("import selection service closed")
	ErrSelectionUnavailable = errors.New("import selection unavailable")
)

type SelectionService struct {
	store          *state.Store
	content        *library.Service
	authentication *auth.Service
	origin         string
	mutex          sync.Mutex
	closed         bool
	calls          map[*SelectionCall]bool
	owners         [2]map[string]bool
	counts         [2]int
	active         sync.WaitGroup
	live           map[selectionLiveKey]selectionLiveExecution
}

type SelectionCall struct {
	service    *SelectionService
	ctx        context.Context
	cancel     context.CancelFunc
	started    time.Time
	method     SelectionMethod
	lane       int
	owner      string
	credential state.ImportOwnerCredential
	once       sync.Once
	done       chan struct{}
	used       bool
	running    bool
	releasing  bool
}

func NewSelectionService(store *state.Store, content *library.Service, authentication *auth.Service, origin string) (*SelectionService, error) {
	if store == nil || content == nil || authentication == nil {
		return nil, ErrSelectionInvalid
	}
	return &SelectionService{store: store, content: content, authentication: authentication, origin: origin, calls: make(map[*SelectionCall]bool), owners: [2]map[string]bool{make(map[string]bool), make(map[string]bool)}, live: make(map[selectionLiveKey]selectionLiveExecution)}, nil
}

func (service *SelectionService) Begin(ctx context.Context, method SelectionMethod, bearer string) (*SelectionCall, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if service == nil {
		return nil, ErrSelectionClosed
	}
	if method < SelectionPreviewMethod || method > SelectionStatusMethod {
		return nil, ErrSelectionInvalid
	}
	if !nativesessionauth.ValidServerOrigin(service.origin) {
		return nil, state.ErrImportPrecondition
	}
	lane, limit, duration := 0, 2, 15*time.Second
	if method == SelectionCommitMethod {
		duration = 120 * time.Second
	}
	if method == SelectionStatusMethod {
		lane, limit, duration = 1, 1, 5*time.Second
	}
	service.mutex.Lock()
	if service.closed {
		service.mutex.Unlock()
		return nil, ErrSelectionClosed
	}
	if service.counts[lane] >= limit {
		service.mutex.Unlock()
		return nil, ErrSelectionBusy
	}
	linked, cancel := context.WithTimeout(ctx, duration)
	call := &SelectionCall{service: service, ctx: linked, cancel: cancel, started: time.Now(), method: method, lane: lane, done: make(chan struct{})}
	service.counts[lane]++
	service.calls[call] = true
	service.active.Add(1)
	service.mutex.Unlock()
	credential, err := service.authentication.AuthenticateImport(linked, bearer)
	if err != nil {
		failure := selectionServiceError(linked, err)
		call.Release()
		return nil, failure
	}
	service.mutex.Lock()
	if service.closed || linked.Err() != nil || service.owners[lane][credential.Owner()] {
		service.mutex.Unlock()
		failure := ErrSelectionBusy
		if linked.Err() != nil {
			failure = linked.Err()
		}
		call.Release()
		return nil, failure
	}
	call.owner, call.credential = credential.Owner(), credential
	service.owners[lane][call.owner] = true
	service.mutex.Unlock()
	return call, nil
}

func (call *SelectionCall) Context() context.Context {
	if call != nil && call.ctx != nil {
		return call.ctx
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func (call *SelectionCall) Release() {
	if call == nil || call.service == nil {
		return
	}
	call.once.Do(func() {
		service := call.service
		service.mutex.Lock()
		call.releasing = true
		call.cancel()
		running := call.running
		service.mutex.Unlock()
		if running {
			<-call.done
		}
		service.mutex.Lock()
		delete(service.calls, call)
		if call.owner != "" {
			delete(service.owners[call.lane], call.owner)
		}
		service.counts[call.lane]--
		call.credential = state.ImportOwnerCredential{}
		service.mutex.Unlock()
		service.active.Done()
	})
}

func (service *SelectionService) enter(call *SelectionCall, method SelectionMethod) (func(), error) {
	if service == nil || call == nil || call.service != service {
		return nil, ErrSelectionInvalid
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if !service.calls[call] || call.used || call.releasing || call.method != method {
		return nil, ErrSelectionInvalid
	}
	if err := call.ctx.Err(); err != nil {
		return nil, err
	}
	call.used, call.running = true, true
	return func() { service.mutex.Lock(); call.running = false; close(call.done); service.mutex.Unlock() }, nil
}

func (service *SelectionService) Close() error {
	if service == nil {
		return nil
	}
	service.mutex.Lock()
	service.closed = true
	for call := range service.calls {
		call.cancel()
	}
	service.mutex.Unlock()
	service.active.Wait()
	return nil
}

func selectionServiceError(ctx context.Context, err error) error {
	if errors.Is(err, state.ErrImportOutcomeUnknown) {
		return state.ErrImportOutcomeUnknown
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, sentinel := range []error{state.ErrImportInvalid, state.ErrImportUnauthorized, state.ErrImportPrecondition, state.ErrImportConflict, state.ErrImportQuota, state.ErrImportUnavailable, auth.ErrInvalidSession, ErrSelectionInvalid, ErrSelectionUnsupported, ErrSelectionPlugin, ErrSelectionLimit, ErrSelectionBusy, ErrSelectionClosed} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return ErrSelectionUnavailable
}
