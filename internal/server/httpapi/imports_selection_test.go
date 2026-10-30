package httpapi

import (
	"bytes"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

func importPreviewMappingFixture(test *testing.T) importer.SelectionPreviewView {
	test.Helper()
	view := importer.SelectionPreviewView{
		ExpiresAt:    time.UnixMilli(1700000000123),
		Repositories: []importer.SelectionRepositoryCandidate{{ID: strings.Repeat("a", 64), Repository: "owner/repo", Branch: "release", ExistingRepositoryID: "repo-id", SourceEnabled: new(false), Status: importer.SelectionCandidateReady}},
		Skills:       []importer.SelectionSkillCandidate{{ID: strings.Repeat("b", 64), SafeName: "Selected skill", RepositoryCandidateID: strings.Repeat("a", 64), Status: importer.SelectionCandidateReady, Binding: &state.ImportSkillBinding{RepositoryID: "repo-id", ArchiveHash: strings.Repeat("c", 64), SkillID: "skill-id", LogicalPath: "nested/SKILL.md", ContentHash: strings.Repeat("d", 64), Repository: "owner/repo", Branch: "release"}}},
		Notices:      importMappingReceiptFixture().Notices,
	}
	for index, wire := range []*configurationv1.Draft{
		{Name: "Claude fixture", Payload: &configurationv1.Draft_ClaudeProvider{ClaudeProvider: &configurationv1.ClaudeProvider{AuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_AUTH_TOKEN, Endpoint: new("https://api.example.test"), Models: &configurationv1.ClaudeModels{DefaultModel: new("model"), Haiku: new(""), Sonnet: new("sonnet"), Opus: new("opus")}}}},
		codexConfigurationDraft(), geminiAPIConfigurationDraft(),
	} {
		draft, secrets, err := configurationInput(wire, nil)
		if err != nil || len(secrets) != 0 {
			test.Fatal("invalid secret-free preview fixture")
		}
		view.Configurations = append(view.Configurations, importer.SelectionConfigurationCandidate{ID: strings.Repeat(strconv.Itoa(index+1), 64), KnownAgentID: []string{"claude", "codex", "gemini"}[index], Kind: "provider", SafeName: draft.Name, Status: importer.SelectionCandidateReady, Draft: &draft, CredentialSlots: []importer.SelectionSlot{{Slot: "slot", Present: true}}, Notices: view.Notices[:1]})
	}
	return view
}

func TestImportSelectionPreviewMapping(test *testing.T) {
	view := importPreviewMappingFixture(test)
	ticket := bytes.Repeat([]byte{4}, state.ImportTicketBytes)
	actual, err := importSelectionPreviewResponse(view, ticket)
	if err != nil || actual == nil || !bytes.Equal(actual.Ticket, ticket) || actual.ExpiresMs != view.ExpiresAt.UnixMilli() || len(actual.Repositories) != 1 || len(actual.Skills) != 1 || len(actual.Configurations) != 3 || len(actual.Notices) != 5 {
		test.Fatal("preview lost ticket, expiry, ordering or candidate collections")
	}
	repository := actual.Repositories[0]
	if repository.Id != view.Repositories[0].ID || repository.Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY || repository.Hint == nil || repository.Hint.Repository != "owner/repo" || repository.Hint.Branch != "release" || repository.Hint.ExistingRepositoryId != "repo-id" || repository.Hint.SourceEnabled == nil || *repository.Hint.SourceEnabled {
		test.Fatal("preview repository lost hint fields or explicit false presence")
	}
	skill := actual.Skills[0]
	if skill.Id != view.Skills[0].ID || skill.SafeName != "Selected skill" || skill.RepositoryCandidateId != view.Repositories[0].ID || skill.Binding == nil || skill.Binding.RepositoryId != "repo-id" || skill.Binding.ArchiveHash != strings.Repeat("c", 64) || skill.Binding.SkillId != "skill-id" || skill.Binding.LogicalPath != "nested/SKILL.md" || skill.Binding.ContentHash != strings.Repeat("d", 64) || skill.Binding.Repository != "owner/repo" || skill.Binding.Branch != "release" {
		test.Fatal("preview skill lost identity, dependency or binding fields")
	}
	for index, candidate := range actual.Configurations {
		original := view.Configurations[index]
		draft, secrets, err := configurationInput(candidate.Draft, nil)
		if err != nil || len(secrets) != 0 || !reflect.DeepEqual(draft, *original.Draft) || candidate.Id != original.ID || candidate.KnownAgentId != original.KnownAgentID || candidate.SafeName != original.SafeName || candidate.Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY || candidate.Kind != configurationv1.AssetKind_ASSET_KIND_PROVIDER || len(candidate.CredentialSlots) != 1 || candidate.CredentialSlots[0].SlotId != "slot" || !candidate.CredentialSlots[0].Present || len(candidate.Notices) != 1 {
			test.Fatal("preview lost provider data, explicit presence, slot metadata or notices")
		}
	}
	for index, notice := range actual.Notices {
		if notice.Id != view.Notices[index].ID || int32(notice.Code) != int32(index+1) || notice.Count != uint32(index+1) {
			test.Fatal("preview notice was not explicitly mapped")
		}
	}
	actual.Ticket[0] = 99
	*actual.Repositories[0].Hint.SourceEnabled = true
	*actual.Configurations[0].Draft.GetClaudeProvider().Endpoint = "changed"
	*actual.Configurations[2].Draft.GetGeminiProvider().EnvModel = "changed"
	if ticket[0] != 4 || *view.Repositories[0].SourceEnabled || *view.Configurations[0].Draft.Provider.Endpoint != "https://api.example.test" || *view.Configurations[2].Draft.GeminiProvider.EnvModel != "private-gemini-env" {
		test.Fatal("preview response aliases controller-owned data")
	}
}

func TestImportSelectionPreviewStatusMapping(test *testing.T) {
	for index := range 8 {
		status := importer.SelectionCandidateStatus(index + 1)
		view := importer.SelectionPreviewView{ExpiresAt: time.Unix(1000, 0), Skills: []importer.SelectionSkillCandidate{{ID: "candidate", Status: status}}}
		actual, err := importSelectionPreviewResponse(view, make([]byte, state.ImportTicketBytes))
		if err != nil || len(actual.Skills) != 1 || int32(actual.Skills[0].Status) != int32(index+1) {
			test.Fatal("candidate status was not explicitly mapped")
		}
	}
}

func TestImportSelectionPreviewRejectsMalformedView(test *testing.T) {
	for name, mutate := range map[string]func(*importer.SelectionPreviewView){
		"expiry":         func(view *importer.SelectionPreviewView) { view.ExpiresAt = time.Time{} },
		"zero-status":    func(view *importer.SelectionPreviewView) { view.Skills[0].Status = 0 },
		"unknown-status": func(view *importer.SelectionPreviewView) { view.Skills[0].Status = 9 },
		"missing-draft":  func(view *importer.SelectionPreviewView) { view.Configurations[0].Draft = nil },
		"non-ready-draft": func(view *importer.SelectionPreviewView) {
			view.Configurations[0].Status = importer.SelectionCandidateNeedsChoice
		},
		"unknown-kind":      func(view *importer.SelectionPreviewView) { view.Configurations[0].Kind = "private-kind-marker" },
		"mismatched-agent":  func(view *importer.SelectionPreviewView) { view.Configurations[0].KnownAgentID = "codex" },
		"mismatched-name":   func(view *importer.SelectionPreviewView) { view.Configurations[0].SafeName = "other" },
		"unknown-notice":    func(view *importer.SelectionPreviewView) { view.Notices[0].Code = 99 },
		"zero-notice-count": func(view *importer.SelectionPreviewView) { view.Notices[0].Count = 0 },
		"repo-bound": func(view *importer.SelectionPreviewView) {
			view.Repositories = make([]importer.SelectionRepositoryCandidate, 17)
		},
		"skill-bound": func(view *importer.SelectionPreviewView) { view.Skills = make([]importer.SelectionSkillCandidate, 129) },
		"config-bound": func(view *importer.SelectionPreviewView) {
			view.Configurations = make([]importer.SelectionConfigurationCandidate, 33)
		},
		"notice-bound": func(view *importer.SelectionPreviewView) { view.Notices = make([]state.ImportReceiptNotice, 2049) },
	} {
		test.Run(name, func(test *testing.T) {
			view := importPreviewMappingFixture(test)
			mutate(&view)
			actual, err := importSelectionPreviewResponse(view, make([]byte, state.ImportTicketBytes))
			if err == nil || actual != nil || strings.Contains(err.Error(), "private-kind-marker") {
				test.Fatal("invalid preview returned partial output or unsafe error")
			}
		})
	}
	for _, length := range []int{0, 289, 291} {
		actual, err := importSelectionPreviewResponse(importPreviewMappingFixture(test), make([]byte, length))
		if err == nil || actual != nil {
			test.Fatal("invalid ticket length accepted")
		}
	}
}

func importMappingReceiptFixture() state.ImportReceiptView {
	view := state.ImportReceiptView{
		OperationID: [16]byte{1, 2, 3}, Revision: 17, Phase: state.ImportPartial,
		SelectedSkills: []state.ImportSkillBinding{{RepositoryID: "repository-id", ArchiveHash: strings.Repeat("a", 64), SkillID: "skill-id", LogicalPath: "nested/SKILL.md", ContentHash: strings.Repeat("b", 64), Repository: "owner/repo", Branch: "release"}},
		CreatedAssets:  []state.ConfigurationAsset{{ID: "asset-id", AgentID: "claude-code", Kind: state.ConfigurationKindProvider, Name: "Imported configuration", SchemaVersion: 1, Revision: 3, CreatedAt: time.Unix(1000, 0), UpdatedAt: time.Unix(2000, 0)}},
	}
	for index := range 8 {
		outcome := state.ImportRepositoryOutcome{CandidateID: strings.Repeat(strconv.Itoa(index+1), 64), Code: state.ImportRepositoryOutcomeCode(index + 1)}
		if index < 2 {
			outcome.RepositoryID = "repository-" + strconv.Itoa(index)
		}
		view.RepositoryOutcomes = append(view.RepositoryOutcomes, outcome)
	}
	for index := range 5 {
		view.Notices = append(view.Notices, state.ImportReceiptNotice{ID: strings.Repeat(strconv.Itoa(index+1), 64), Code: state.ImportReceiptNoticeCode(index + 1), Count: uint32(index + 1)})
	}
	return view
}

func TestImportSelectionReceiptMapping(test *testing.T) {
	view := importMappingReceiptFixture()
	want := &importv1.ImportReceipt{
		OperationId: slices.Clone(view.OperationID[:]), Revision: 17, Phase: importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_PARTIAL,
		SelectedSkillBindings: []*importv1.SkillBinding{{RepositoryId: "repository-id", ArchiveHash: strings.Repeat("a", 64), SkillId: "skill-id", LogicalPath: "nested/SKILL.md", ContentHash: strings.Repeat("b", 64), Repository: "owner/repo", Branch: "release"}},
		CreatedAssets:         []*configurationv1.Asset{{Id: "asset-id", AgentId: "claude-code", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, Name: "Imported configuration", SchemaVersion: 1, Revision: 3, CreatedAtUnix: 1000, UpdatedAtUnix: 2000}},
	}
	for index := range 8 {
		outcome := &importv1.ImportRepositoryOutcome{CandidateId: strings.Repeat(strconv.Itoa(index+1), 64), Code: importv1.ImportRepositoryOutcomeCode(index + 1)}
		if index < 2 {
			outcome.RepositoryId = new("repository-" + strconv.Itoa(index))
		}
		want.RepositoryOutcomes = append(want.RepositoryOutcomes, outcome)
	}
	for index := range 5 {
		want.Notices = append(want.Notices, &importv1.ImportNotice{Id: strings.Repeat(strconv.Itoa(index+1), 64), Code: importv1.ImportNoticeCode(index + 1), Count: uint32(index + 1)})
	}
	actual, err := importSelectionReceipt(view)
	if err != nil || !proto.Equal(actual, want) {
		test.Fatal("receipt mapping lost fields, ordering, presence or closed enum values")
	}
	actual.OperationId[0] = 99
	actual.RepositoryOutcomes[0].CandidateId = "changed"
	actual.SelectedSkillBindings[0].LogicalPath = "changed"
	actual.CreatedAssets[0].Name = "changed"
	actual.Notices[0].Count = 99
	if view.OperationID[0] != 1 || view.RepositoryOutcomes[0].CandidateID != strings.Repeat("1", 64) || view.SelectedSkills[0].LogicalPath != "nested/SKILL.md" || view.CreatedAssets[0].Name != "Imported configuration" || view.Notices[0].Count != 1 {
		test.Fatal("wire receipt aliases the durable detached view")
	}
}

func TestImportSelectionReceiptPhaseMapping(test *testing.T) {
	for index, phase := range []state.ImportReceiptPhase{state.ImportReserved, state.ImportCollecting, state.ImportCompleted, state.ImportPartial, state.ImportFailed} {
		view := importMappingReceiptFixture()
		view.Phase = phase
		actual, err := importSelectionReceipt(view)
		if err != nil || int32(actual.GetPhase()) != int32(index+1) {
			test.Fatalf("phase %s was not mapped to its permanent protocol number", phase)
		}
	}
}

func TestImportSelectionReceiptRejectsInvalidMapping(test *testing.T) {
	for name, mutate := range map[string]func(*state.ImportReceiptView){
		"zero-operation":       func(view *state.ImportReceiptView) { view.OperationID = [16]byte{} },
		"zero-revision":        func(view *state.ImportReceiptView) { view.Revision = 0 },
		"negative-revision":    func(view *state.ImportReceiptView) { view.Revision = -1 },
		"unknown-phase":        func(view *state.ImportReceiptView) { view.Phase = "private-marker-phase" },
		"zero-outcome":         func(view *state.ImportReceiptView) { view.RepositoryOutcomes[0].Code = 0 },
		"unknown-outcome":      func(view *state.ImportReceiptView) { view.RepositoryOutcomes[0].Code = 9 },
		"success-without-id":   func(view *state.ImportReceiptView) { view.RepositoryOutcomes[0].RepositoryID = "" },
		"failure-with-id":      func(view *state.ImportReceiptView) { view.RepositoryOutcomes[2].RepositoryID = "private-marker-repo" },
		"zero-notice":          func(view *state.ImportReceiptView) { view.Notices[0].Code = 0 },
		"unknown-notice":       func(view *state.ImportReceiptView) { view.Notices[0].Code = 6 },
		"zero-notice-count":    func(view *state.ImportReceiptView) { view.Notices[0].Count = 0 },
		"unknown-asset-kind":   func(view *state.ImportReceiptView) { view.CreatedAssets[0].Kind = "private-marker-kind" },
		"invalid-asset-schema": func(view *state.ImportReceiptView) { view.CreatedAssets[0].SchemaVersion = 0 },
		"repository-bound": func(view *state.ImportReceiptView) {
			view.RepositoryOutcomes = slices.Repeat(view.RepositoryOutcomes, 3)[:17]
		},
		"skill-bound":  func(view *state.ImportReceiptView) { view.SelectedSkills = slices.Repeat(view.SelectedSkills, 129) },
		"asset-bound":  func(view *state.ImportReceiptView) { view.CreatedAssets = slices.Repeat(view.CreatedAssets, 33) },
		"notice-bound": func(view *state.ImportReceiptView) { view.Notices = slices.Repeat(view.Notices, 410)[:2049] },
	} {
		test.Run(name, func(test *testing.T) {
			view := importMappingReceiptFixture()
			mutate(&view)
			actual, err := importSelectionReceipt(view)
			if err == nil || actual != nil || strings.Contains(err.Error(), "private-marker") {
				test.Fatal("invalid receipt produced a partial response or leaked input")
			}
		})
	}
}

func TestImportSelectionReceiptMappingBoundaries(test *testing.T) {
	view := importMappingReceiptFixture()
	view.RepositoryOutcomes = slices.Repeat(view.RepositoryOutcomes, 2)
	view.SelectedSkills = slices.Repeat(view.SelectedSkills, 128)
	view.CreatedAssets = slices.Repeat(view.CreatedAssets, 32)
	view.Notices = slices.Repeat(view.Notices, 410)[:2048]
	actual, err := importSelectionReceipt(view)
	if err != nil || len(actual.GetRepositoryOutcomes()) != 16 || len(actual.GetSelectedSkillBindings()) != 128 || len(actual.GetCreatedAssets()) != 32 || len(actual.GetNotices()) != 2048 {
		test.Fatal("exact receipt count limits were rejected or truncated")
	}
}
