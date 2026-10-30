package httpapi

import (
	"errors"
	"slices"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/state"
)

var errImportReceiptUnavailable = errors.New("import receipt unavailable")

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
