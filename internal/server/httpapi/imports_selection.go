package httpapi

import (
	"context"
	"errors"
	"slices"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/state"
)

var errImportReceiptUnavailable = errors.New("import receipt unavailable")

type importSelectionCallKey struct{}

type importSelectionAdmission struct {
	call   *importer.SelectionCall
	ticket []byte
}

func (handler *ImportHandler) selectionAdmission(ctx context.Context) (*importSelectionAdmission, error) {
	if handler.selection == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("import method is not available"))
	}
	admission, ok := ctx.Value(importSelectionCallKey{}).(*importSelectionAdmission)
	if !ok || admission == nil || admission.call == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid import admission"))
	}
	return admission, nil
}

func (handler *ImportHandler) PreviewImportSelection(ctx context.Context, request *connect.Request[importv1.PreviewImportSelectionRequest]) (*connect.Response[importv1.PreviewImportSelectionResponse], error) {
	admission, err := handler.selectionAdmission(ctx)
	if err != nil {
		return nil, err
	}
	data := request.Msg.SourceData
	request.Msg.SourceData = nil
	defer clear(data)
	source, options, err := importSelectionInput(request.Msg.Source, data, request.Msg.Options)
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	preview, err := handler.selection.Preview(admission.call, source, data, options)
	defer preview.Destroy()
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	view, err := preview.View()
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	ticket := preview.RevealTicket()
	defer clear(ticket)
	response, err := importSelectionPreviewResponse(view, ticket)
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	admission.ticket = response.Ticket
	return connect.NewResponse(response), nil
}

func (handler *ImportHandler) CommitImportSelection(ctx context.Context, request *connect.Request[importv1.CommitImportSelectionRequest]) (*connect.Response[importv1.CommitImportSelectionResponse], error) {
	admission, err := handler.selectionAdmission(ctx)
	if err != nil {
		return nil, err
	}
	message := request.Msg
	data, ticket := message.SourceData, message.Ticket
	message.SourceData, message.Ticket = nil, nil
	defer clear(data)
	defer clear(ticket)
	source, options, err := importSelectionInput(message.Source, data, message.Options)
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	operation, err := importSelectionOperation(message.OperationId)
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	input, err := importer.NewSelectionCommitRequest(importer.SelectionCommitInput{Ticket: ticket, Source: source, SourceData: data, Options: options, OperationID: operation, RepositoryIDs: message.RepositoryIds, SkillIDs: message.SkillIds, ConfigurationIDs: message.ConfigurationIds, AckNoticeIDs: message.AckNoticeIds})
	defer input.Destroy()
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	view, err := handler.selection.Commit(admission.call, input)
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	receipt, err := importSelectionReceipt(view)
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	return connect.NewResponse(&importv1.CommitImportSelectionResponse{Receipt: receipt}), nil
}

func (handler *ImportHandler) GetImportCommitStatus(ctx context.Context, request *connect.Request[importv1.GetImportCommitStatusRequest]) (*connect.Response[importv1.GetImportCommitStatusResponse], error) {
	admission, err := handler.selectionAdmission(ctx)
	if err != nil {
		return nil, err
	}
	operation, err := importSelectionOperation(request.Msg.OperationId)
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	view, err := handler.selection.Status(admission.call, operation)
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	response := &importv1.GetImportCommitStatusResponse{}
	switch view.Observation {
	case importer.SelectionNotObserved:
		if view.Receipt != nil {
			return nil, importSelectionError(ctx, errImportReceiptUnavailable)
		}
		response.Observation = importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_NOT_OBSERVED
		return connect.NewResponse(response), nil
	case importer.SelectionInProgress:
		response.Observation = importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_IN_PROGRESS
	case importer.SelectionUnknown:
		response.Observation = importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_UNKNOWN
	case importer.SelectionTerminal:
		response.Observation = importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_TERMINAL
	default:
		return nil, importSelectionError(ctx, errImportReceiptUnavailable)
	}
	if view.Receipt == nil || view.Receipt.OperationID != operation {
		return nil, importSelectionError(ctx, errImportReceiptUnavailable)
	}
	terminal := view.Receipt.Phase == state.ImportCompleted || view.Receipt.Phase == state.ImportPartial || view.Receipt.Phase == state.ImportFailed
	if terminal != (view.Observation == importer.SelectionTerminal) {
		return nil, importSelectionError(ctx, errImportReceiptUnavailable)
	}
	response.Receipt, err = importSelectionReceipt(*view.Receipt)
	if err != nil {
		return nil, importSelectionError(ctx, err)
	}
	return connect.NewResponse(response), nil
}

func importSelectionError(ctx context.Context, err error) error {
	if errors.Is(err, state.ErrImportOutcomeUnknown) {
		return connect.NewError(connect.CodeUnknown, errors.New("import outcome unknown"))
	}
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	var code connect.Code
	var message string
	switch {
	case errors.Is(err, auth.ErrInvalidSession), errors.Is(err, state.ErrImportUnauthorized):
		code, message = connect.CodeUnauthenticated, "invalid import session"
	case errors.Is(err, importer.ErrSelectionBusy), errors.Is(err, importer.ErrSelectionLimit), errors.Is(err, state.ErrImportQuota):
		code, message = connect.CodeResourceExhausted, "import capacity exhausted"
	case errors.Is(err, importer.ErrSelectionInvalid), errors.Is(err, state.ErrImportInvalid):
		code, message = connect.CodeInvalidArgument, "invalid import request"
	case errors.Is(err, state.ErrImportConflict):
		code, message = connect.CodeAborted, "import input or revision conflict"
	case errors.Is(err, state.ErrImportPrecondition), errors.Is(err, importer.ErrSelectionUnsupported):
		code, message = connect.CodeFailedPrecondition, "import precondition failed"
	case errors.Is(err, importer.ErrSelectionClosed), errors.Is(err, importer.ErrSelectionUnavailable), errors.Is(err, state.ErrImportUnavailable):
		code, message = connect.CodeUnavailable, "import unavailable"
	default:
		code, message = connect.CodeInternal, "import unavailable"
	}
	return connect.NewError(code, errors.New(message))
}

func importSelectionPreviewResponse(view importer.SelectionPreviewView, ticket []byte) (*importv1.PreviewImportSelectionResponse, error) {
	if len(ticket) != state.ImportTicketBytes || view.ExpiresAt.UnixMilli() <= 0 || len(view.Repositories) > 16 || len(view.Skills) > 128 || len(view.Configurations) > 32 {
		return nil, errImportReceiptUnavailable
	}
	notices, err := importSelectionNotices(view.Notices)
	if err != nil {
		return nil, err
	}
	result := &importv1.PreviewImportSelectionResponse{Ticket: slices.Clone(ticket), ExpiresMs: view.ExpiresAt.UnixMilli(), Notices: notices}
	noticeCount := len(notices)
	for _, candidate := range view.Repositories {
		status, err := importSelectionStatus(candidate.Status)
		if err != nil {
			return nil, err
		}
		result.Repositories = append(result.Repositories, &importv1.ImportRepositoryCandidate{Id: candidate.ID, Status: status, Hint: &importv1.RepositoryCandidate{Repository: candidate.Repository, Branch: candidate.Branch, ExistingRepositoryId: candidate.ExistingRepositoryID, SourceEnabled: configurationClonePointer(candidate.SourceEnabled)}})
	}
	for _, candidate := range view.Skills {
		status, err := importSelectionStatus(candidate.Status)
		if err != nil {
			return nil, err
		}
		notices, err := importSelectionNotices(candidate.Notices)
		if err != nil {
			return nil, err
		}
		noticeCount += len(notices)
		if noticeCount > 2048 {
			return nil, errImportReceiptUnavailable
		}
		converted := &importv1.ImportSkillCandidate{Id: candidate.ID, SafeName: candidate.SafeName, RepositoryCandidateId: candidate.RepositoryCandidateID, Status: status, Notices: notices}
		if binding := candidate.Binding; binding != nil {
			converted.Binding = &importv1.SkillBinding{RepositoryId: binding.RepositoryID, ArchiveHash: binding.ArchiveHash, SkillId: binding.SkillID, LogicalPath: binding.LogicalPath, ContentHash: binding.ContentHash, Repository: binding.Repository, Branch: binding.Branch}
		}
		result.Skills = append(result.Skills, converted)
	}
	for _, candidate := range view.Configurations {
		status, err := importSelectionStatus(candidate.Status)
		if err != nil {
			return nil, err
		}
		kind, err := importSelectionKind(candidate.Kind, candidate.Status)
		if err != nil {
			return nil, err
		}
		notices, err := importSelectionNotices(candidate.Notices)
		if err != nil {
			return nil, err
		}
		noticeCount += len(notices)
		if noticeCount > 2048 {
			return nil, errImportReceiptUnavailable
		}
		converted := &importv1.ImportConfigurationCandidate{Id: candidate.ID, KnownAgentId: candidate.KnownAgentID, Kind: kind, SafeName: candidate.SafeName, Status: status, Notices: notices}
		if candidate.Status == importer.SelectionCandidateReady {
			if kind != configurationv1.AssetKind_ASSET_KIND_PROVIDER {
				return nil, errImportReceiptUnavailable
			}
			converted.Draft, err = importSelectionDraft(candidate)
			if err != nil {
				return nil, err
			}
		} else if candidate.Draft != nil {
			return nil, errImportReceiptUnavailable
		}
		for _, slot := range candidate.CredentialSlots {
			converted.CredentialSlots = append(converted.CredentialSlots, &importv1.ImportCredentialSlot{SlotId: slot.Slot, Present: slot.Present})
		}
		result.Configurations = append(result.Configurations, converted)
	}
	return result, nil
}

func importSelectionStatus(status importer.SelectionCandidateStatus) (importv1.ImportCandidateStatus, error) {
	switch status {
	case importer.SelectionCandidateReady:
		return importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY, nil
	case importer.SelectionCandidateNeedsChoice:
		return importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_NEEDS_CHOICE, nil
	case importer.SelectionCandidateNeedsRepositoryImport:
		return importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_NEEDS_REPOSITORY_IMPORT, nil
	case importer.SelectionCandidateUnresolved:
		return importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_UNRESOLVED, nil
	case importer.SelectionCandidateUnsupportedAgent:
		return importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_UNSUPPORTED_AGENT, nil
	case importer.SelectionCandidateUnsupportedKind:
		return importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_UNSUPPORTED_KIND, nil
	case importer.SelectionCandidateUnsupportedField:
		return importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_UNSUPPORTED_FIELD, nil
	case importer.SelectionCandidateNameConflict:
		return importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_NAME_CONFLICT, nil
	default:
		return 0, errImportReceiptUnavailable
	}
}

func importSelectionKind(kind string, status importer.SelectionCandidateStatus) (configurationv1.AssetKind, error) {
	switch state.ConfigurationKind(kind) {
	case state.ConfigurationKindProvider:
		return configurationv1.AssetKind_ASSET_KIND_PROVIDER, nil
	case state.ConfigurationKindMCP:
		return configurationv1.AssetKind_ASSET_KIND_MCP, nil
	case state.ConfigurationKindPrompt:
		return configurationv1.AssetKind_ASSET_KIND_PROMPT, nil
	case state.ConfigurationKindAgentSettings:
		return configurationv1.AssetKind_ASSET_KIND_AGENT_SETTINGS, nil
	case state.ConfigurationKindWorkspace:
		return configurationv1.AssetKind_ASSET_KIND_WORKSPACE, nil
	default:
		if status == importer.SelectionCandidateUnsupportedKind {
			return configurationv1.AssetKind_ASSET_KIND_UNSPECIFIED, nil
		}
		return 0, errImportReceiptUnavailable
	}
}

func importSelectionDraft(candidate importer.SelectionConfigurationCandidate) (*configurationv1.Draft, error) {
	input := candidate.Draft
	if input == nil || input.Name != candidate.SafeName {
		return nil, errImportReceiptUnavailable
	}
	result := &configurationv1.Draft{Name: input.Name}
	switch candidate.KnownAgentID {
	case "claude":
		if input.CodexProvider != nil || input.GeminiProvider != nil || input.OpenCodeProvider != nil || input.GrokBuildProvider != nil {
			return nil, errImportReceiptUnavailable
		}
		provider := &configurationv1.ClaudeProvider{Endpoint: configurationClonePointer(input.Provider.Endpoint)}
		switch input.Provider.AuthMode {
		case configuration.NativeLogin:
			provider.AuthMode = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_NATIVE_LOGIN
		case configuration.APIKey:
			provider.AuthMode = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY
		case configuration.AuthToken:
			provider.AuthMode = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_AUTH_TOKEN
		default:
			return nil, errImportReceiptUnavailable
		}
		models := input.Provider.Models
		provider.Models = &configurationv1.ClaudeModels{DefaultModel: configurationClonePointer(models.Default), Haiku: configurationClonePointer(models.Haiku), Sonnet: configurationClonePointer(models.Sonnet), Opus: configurationClonePointer(models.Opus)}
		result.Payload = &configurationv1.Draft_ClaudeProvider{ClaudeProvider: provider}
	case "codex":
		if input.GeminiProvider != nil || input.OpenCodeProvider != nil || input.GrokBuildProvider != nil || input.Provider != (configuration.ClaudeProvider{}) {
			return nil, errImportReceiptUnavailable
		}
		provider, err := configurationCodexProvider(input.CodexProvider)
		if err != nil {
			return nil, errImportReceiptUnavailable
		}
		result.Payload = &configurationv1.Draft_CodexProvider{CodexProvider: provider}
	case "gemini":
		if input.CodexProvider != nil || input.OpenCodeProvider != nil || input.GrokBuildProvider != nil || input.Provider != (configuration.ClaudeProvider{}) {
			return nil, errImportReceiptUnavailable
		}
		provider, err := configurationGeminiProvider(input.GeminiProvider)
		if err != nil {
			return nil, errImportReceiptUnavailable
		}
		result.Payload = &configurationv1.Draft_GeminiProvider{GeminiProvider: provider}
	case "grokbuild":
		if input.Provider != (configuration.ClaudeProvider{}) || input.CodexProvider != nil || input.GeminiProvider != nil || input.OpenCodeProvider != nil || input.GrokBuildProvider == nil || input.GrokBuildProvider.Mode != agentconfig.GrokBuildCustomModel || !configurationGrokBuildModelValid(input.GrokBuildProvider.Model) {
			return nil, errImportReceiptUnavailable
		}
		model := input.GrokBuildProvider.Model
		result.Payload = &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: &configurationv1.GrokBuildProvider{Mode: configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL, Model: &configurationv1.GrokBuildModel{Profile: model.Profile, UpstreamModel: model.UpstreamModel, Endpoint: model.Endpoint, Name: model.Name, ApiBackend: model.APIBackend, ContextWindow: model.ContextWindow, EnvKey: configurationClonePointer(model.EnvKey)}}}
	default:
		return nil, errImportReceiptUnavailable
	}
	return result, nil
}

func importSelectionNotices(notices []state.ImportReceiptNotice) ([]*importv1.ImportNotice, error) {
	if len(notices) > 2048 {
		return nil, errImportReceiptUnavailable
	}
	var result []*importv1.ImportNotice
	for _, notice := range notices {
		if notice.Count == 0 {
			return nil, errImportReceiptUnavailable
		}
		converted := &importv1.ImportNotice{Id: notice.ID, Count: notice.Count}
		switch notice.Code {
		case state.ImportCredentialsOmitted:
			converted.Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_CREDENTIALS_OMITTED
		case state.ImportMetadataExcluded:
			converted.Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_METADATA_EXCLUDED
		case state.ImportOtherSourceDataExcluded:
			converted.Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_OTHER_SOURCE_DATA_EXCLUDED
		case state.ImportSourceContentNotCompared:
			converted.Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_SOURCE_CONTENT_NOT_COMPARED
		case state.ImportUnknownSourceSectionExcluded:
			converted.Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_UNKNOWN_SOURCE_SECTION_EXCLUDED
		default:
			return nil, errImportReceiptUnavailable
		}
		result = append(result, converted)
	}
	return result, nil
}

func importSelectionInput(source importv1.ImportSelectionSource, data []byte, options []*importv1.ImportCandidateOptions) (importer.SelectionSource, importer.SelectionOptions, error) {
	var selected importer.SelectionSource
	var maximum int
	switch source {
	case importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24:
		selected, maximum = importer.SelectionCCSwitch24, importer.MaxCCSwitchSnapshotBytes
	case importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON:
		selected, maximum = importer.SelectionMagpie, importer.MaxMagpieExportBytes
	default:
		return "", importer.SelectionOptions{}, importer.ErrSelectionInvalid
	}
	if len(data) == 0 {
		return "", importer.SelectionOptions{}, importer.ErrSelectionInvalid
	}
	if len(data) > maximum {
		return "", importer.SelectionOptions{}, importer.ErrSelectionLimit
	}
	converted, err := importSelectionOptions(options)
	if err != nil {
		return "", importer.SelectionOptions{}, err
	}
	return selected, converted, nil
}

func importSelectionOperation(value []byte) ([16]byte, error) {
	if len(value) != 16 {
		return [16]byte{}, importer.ErrSelectionInvalid
	}
	operation := [16]byte(value)
	if operation == [16]byte{} {
		return [16]byte{}, importer.ErrSelectionInvalid
	}
	return operation, nil
}

func importSelectionOptions(options []*importv1.ImportCandidateOptions) (importer.SelectionOptions, error) {
	if len(options) > 160 {
		return importer.SelectionOptions{}, importer.ErrSelectionInvalid
	}
	var result importer.SelectionOptions
	for _, option := range options {
		if option == nil {
			return importer.SelectionOptions{}, importer.ErrSelectionInvalid
		}
		candidate := importer.SelectionCandidateOptions{CandidateID: option.CandidateId}
		if option.Name != nil {
			candidate.Name = new(*option.Name)
		}
		switch auth := option.Auth.(type) {
		case nil:
		case *importv1.ImportCandidateOptions_ClaudeAuthMode:
			if auth == nil {
				return importer.SelectionOptions{}, importer.ErrSelectionInvalid
			}
			switch auth.ClaudeAuthMode {
			case configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_NATIVE_LOGIN:
				candidate.ClaudeAuthMode = new(configuration.NativeLogin)
			case configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY:
				candidate.ClaudeAuthMode = new(configuration.APIKey)
			case configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_AUTH_TOKEN:
				candidate.ClaudeAuthMode = new(configuration.AuthToken)
			default:
				return importer.SelectionOptions{}, importer.ErrSelectionInvalid
			}
		case *importv1.ImportCandidateOptions_GeminiAuthMode:
			if auth == nil {
				return importer.SelectionOptions{}, importer.ErrSelectionInvalid
			}
			switch auth.GeminiAuthMode {
			case configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_NATIVE_LOGIN:
				candidate.GeminiAuthMode = new(agentconfig.GeminiNativeLogin)
			case configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_API_KEY:
				candidate.GeminiAuthMode = new(agentconfig.GeminiAPIKeyAuth)
			default:
				return importer.SelectionOptions{}, importer.ErrSelectionInvalid
			}
		default:
			return importer.SelectionOptions{}, importer.ErrSelectionInvalid
		}
		if option.GeminiKeyTransport != nil {
			switch *option.GeminiKeyTransport {
			case configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_NATIVE_DEFAULT:
				candidate.GeminiKeyTransport = new(agentconfig.GeminiNativeDefault)
			case configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_BEARER:
				candidate.GeminiKeyTransport = new(agentconfig.GeminiBearer)
			default:
				return importer.SelectionOptions{}, importer.ErrSelectionInvalid
			}
		}
		if binding := option.SkillBinding; binding != nil {
			candidate.SkillBinding = &importer.SelectionSkillBinding{
				RepositoryID: binding.RepositoryId, ArchiveHash: binding.ArchiveHash, SkillID: binding.SkillId,
				Path: binding.LogicalPath, ContentHash: binding.ContentHash, Repository: binding.Repository, Branch: binding.Branch,
			}
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	return result, nil
}

func importSelectionReceipt(view state.ImportReceiptView) (*importv1.ImportReceipt, error) {
	if view.OperationID == [16]byte{} || view.Revision < 1 || len(view.RepositoryOutcomes) > 16 || len(view.SelectedSkills) > 128 || len(view.CreatedAssets) > 32 || len(view.Notices) > 2048 {
		return nil, errImportReceiptUnavailable
	}
	var phase importv1.ImportReceiptPhase
	switch view.Phase {
	case state.ImportReserved:
		phase = importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_RESERVED
	case state.ImportCollecting:
		phase = importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COLLECTING
	case state.ImportCompleted:
		phase = importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED
	case state.ImportPartial:
		phase = importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_PARTIAL
	case state.ImportFailed:
		phase = importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_FAILED
	default:
		return nil, errImportReceiptUnavailable
	}
	result := &importv1.ImportReceipt{OperationId: slices.Clone(view.OperationID[:]), Revision: view.Revision, Phase: phase}
	for _, outcome := range view.RepositoryOutcomes {
		converted := &importv1.ImportRepositoryOutcome{CandidateId: outcome.CandidateID}
		switch outcome.Code {
		case state.ImportRepositoryCollected:
			converted.Code = importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_COLLECTED
		case state.ImportRepositoryAlreadyCollected:
			converted.Code = importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_ALREADY_COLLECTED
		case state.ImportRepositoryNotAttempted:
			converted.Code = importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_NOT_ATTEMPTED
		case state.ImportRepositoryCancelled:
			converted.Code = importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_CANCELLED
		case state.ImportRepositoryAuthorizationLost:
			converted.Code = importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_AUTHORIZATION_LOST
		case state.ImportRepositorySourceRejected:
			converted.Code = importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_SOURCE_REJECTED
		case state.ImportRepositoryCapacityExhausted:
			converted.Code = importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_CAPACITY_EXHAUSTED
		case state.ImportRepositoryUnknown:
			converted.Code = importv1.ImportRepositoryOutcomeCode_IMPORT_REPOSITORY_OUTCOME_CODE_UNKNOWN
		default:
			return nil, errImportReceiptUnavailable
		}
		success := outcome.Code == state.ImportRepositoryCollected || outcome.Code == state.ImportRepositoryAlreadyCollected
		if success != (outcome.RepositoryID != "") {
			return nil, errImportReceiptUnavailable
		}
		if success {
			converted.RepositoryId = new(outcome.RepositoryID)
		}
		result.RepositoryOutcomes = append(result.RepositoryOutcomes, converted)
	}
	for _, binding := range view.SelectedSkills {
		result.SelectedSkillBindings = append(result.SelectedSkillBindings, &importv1.SkillBinding{
			RepositoryId: binding.RepositoryID, ArchiveHash: binding.ArchiveHash, SkillId: binding.SkillID,
			LogicalPath: binding.LogicalPath, ContentHash: binding.ContentHash, Repository: binding.Repository, Branch: binding.Branch,
		})
	}
	for _, metadata := range view.CreatedAssets {
		asset, err := configurationAsset(metadata)
		if err != nil {
			return nil, errImportReceiptUnavailable
		}
		result.CreatedAssets = append(result.CreatedAssets, asset)
	}
	for _, notice := range view.Notices {
		if notice.Count == 0 {
			return nil, errImportReceiptUnavailable
		}
		converted := &importv1.ImportNotice{Id: notice.ID, Count: notice.Count}
		switch notice.Code {
		case state.ImportCredentialsOmitted:
			converted.Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_CREDENTIALS_OMITTED
		case state.ImportMetadataExcluded:
			converted.Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_METADATA_EXCLUDED
		case state.ImportOtherSourceDataExcluded:
			converted.Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_OTHER_SOURCE_DATA_EXCLUDED
		case state.ImportSourceContentNotCompared:
			converted.Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_SOURCE_CONTENT_NOT_COMPARED
		case state.ImportUnknownSourceSectionExcluded:
			converted.Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_UNKNOWN_SOURCE_SECTION_EXCLUDED
		default:
			return nil, errImportReceiptUnavailable
		}
		result.Notices = append(result.Notices, converted)
	}
	return result, nil
}
