package httpapi

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

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
