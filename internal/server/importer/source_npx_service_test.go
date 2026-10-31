package importer

import (
	"bytes"
	"testing"

	"github.com/ca-x/cognate/internal/server/state"
)

func TestNPXSelectionExplicitOwnerBindingAndReceipt(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	existing, err := fixture.content.Collect(test.Context(), fixture.owner, "example/toolkit", "main")
	if err != nil {
		test.Fatal("synthetic repository could not be collected")
	}
	existing, err = fixture.store.CollectedRepository(test.Context(), fixture.owner, existing.ID)
	if err != nil || len(existing.Skills) != 1 {
		test.Fatal("synthetic collected content missing")
	}
	raw := []byte(`{"version":3,"skills":{"review":{"sourceType":"github","source":"example/toolkit","sourceUrl":"https://github.com/example/toolkit","ref":"main","skillPath":"skills/review","skillFolderHash":"1111111111111111111111111111111111111111"}}}`)
	preview := selectionServicePreview(test, fixture, SelectionNPXSkills, raw, SelectionOptions{})
	view, err := preview.View()
	if err != nil || len(view.Skills) != 1 || view.Skills[0].Status == SelectionCandidateReady || view.Skills[0].Binding != nil {
		test.Fatal("lock provenance silently authorized an existing content binding")
	}
	skill := existing.Skills[0]
	projection, err := ProjectSelection(test.Context(), SelectionNPXSkills, raw)
	if err != nil {
		test.Fatal("fixture source could not be projected")
	}
	defer projection.Destroy()
	projected, err := projection.View()
	if err != nil || projected.Skills[0].Path == nil || *projected.Skills[0].Path != skill.Path || projected.Skills[0].Repository != existing.NameWithOwner || projected.Skills[0].Branch != existing.Branch {
		test.Fatalf("synthetic lock binding differs from archive: path=%q repository=%q branch=%q", skill.Path, existing.NameWithOwner, existing.Branch)
	}
	options := SelectionOptions{Candidates: []SelectionCandidateOptions{{CandidateID: view.Skills[0].ID, SkillBinding: &SelectionSkillBinding{Repository: existing.NameWithOwner, Branch: existing.Branch, RepositoryID: existing.ID, ArchiveHash: existing.ArchiveHash, SkillID: skill.ID, Path: skill.Path, ContentHash: skill.ContentHash}}}}
	preview = selectionServicePreview(test, fixture, SelectionNPXSkills, raw, options)
	view, err = preview.View()
	if err != nil || view.Skills[0].Status != SelectionCandidateReady || view.Skills[0].Binding == nil {
		test.Fatal("explicit real owner content binding was not validated")
	}
	contentNotices := 0
	for _, notice := range view.Skills[0].Notices {
		if notice.Code == state.ImportSourceContentNotCompared {
			contentNotices++
			if notice.Count != 1 {
				test.Fatal("one skill binding counted its unverified source content more than once")
			}
		}
	}
	if contentNotices != 1 {
		test.Fatal("binding lacks one explicit source-content-not-compared acknowledgment")
	}
	input := SelectionCommitInput{Ticket: preview.RevealTicket(), Source: SelectionNPXSkills, SourceData: raw, Options: options, OperationID: [16]byte{119}, SkillIDs: []string{view.Skills[0].ID}}
	for _, notice := range selectionAllNotices(view) {
		input.AckNoticeIDs = append(input.AckNoticeIDs, notice.ID)
	}
	changed := input
	changed.SourceData = append(bytes.Clone(raw), ' ')
	if _, err := selectionServiceCommit(test, fixture, changed); err == nil {
		test.Fatal("source drift bypassed the original preview ticket")
	}
	selectionAssertNoReservation(test, fixture, input.OperationID)
	unacknowledged := input
	unacknowledged.AckNoticeIDs = nil
	if _, err := selectionServiceCommit(test, fixture, unacknowledged); err == nil {
		test.Fatal("source exclusions and content-not-compared acknowledgments were bypassed")
	}
	selectionAssertNoReservation(test, fixture, input.OperationID)
	before := fixture.counts.size()
	receipt, err := selectionServiceCommit(test, fixture, input)
	if err != nil || receipt.Phase != state.ImportCompleted || len(receipt.SelectedSkills) != 1 || len(receipt.CreatedAssets) != 0 || len(receipt.RepositoryOutcomes) != 0 || fixture.counts.size() != before {
		test.Fatal("skill-only npx commit changed unselected repositories or configurations")
	}
	repeated, err := selectionServiceCommit(test, fixture, input)
	if err != nil || len(repeated.SelectedSkills) != 1 || fixture.counts.size() != before {
		test.Fatal("same operation did not return its existing receipt without refetching")
	}
	call, err := fixture.service.Begin(test.Context(), SelectionStatusMethod, fixture.bearer)
	if err != nil {
		test.Fatal("owner status unavailable")
	}
	observation, err := fixture.service.Status(call, input.OperationID)
	call.Release()
	if err != nil || observation.Observation != SelectionTerminal || observation.Receipt == nil || len(observation.Receipt.SelectedSkills) != 1 {
		test.Fatal("committed npx operation was not available to read-only reconciliation")
	}
	assets, err := fixture.store.ListConfigurationAssets(test.Context(), fixture.owner)
	if err != nil || len(assets) != 0 {
		test.Fatal("npx source unexpectedly imported auxiliary configuration")
	}
}

func TestNPXSelectionGlobalExclusionAcknowledgments(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	raw := []byte(`{"version":3,"skills":{"review":{"sourceType":"github","source":"example/toolkit"},"local":{"source_type":"local","source":"/synthetic/private/path"}},"dismissed":{"notice":true},"lastSelectedAgents":["synthetic-agent"],"futureSection":{"private":"SYNTHETIC-EXCLUDED-DATA"}}`)
	preview := selectionServicePreview(test, fixture, SelectionNPXSkills, raw, SelectionOptions{})
	view, err := preview.View()
	if err != nil || len(view.Notices) != 3 || len(view.Repositories) != 1 || len(view.Skills) != 1 {
		test.Fatal("npx global exclusions did not preserve distinct acknowledgment categories")
	}
	counts := map[state.ImportReceiptNoticeCode]uint32{}
	for _, notice := range view.Notices {
		counts[notice.Code] = notice.Count
	}
	if counts[state.ImportMetadataExcluded] != 2 || counts[state.ImportOtherSourceDataExcluded] != 1 || counts[state.ImportUnknownSourceSectionExcluded] != 1 {
		test.Fatal("npx root metadata, providers and unknown sections were misclassified")
	}
	input := SelectionCommitInput{Ticket: preview.RevealTicket(), Source: SelectionNPXSkills, SourceData: raw, OperationID: [16]byte{120}, RepositoryIDs: []string{view.Repositories[0].ID}}
	if _, err := selectionServiceCommit(test, fixture, input); err == nil {
		test.Fatal("global exclusions were accepted without explicit acknowledgment")
	}
	selectionAssertNoReservation(test, fixture, input.OperationID)
	for _, notice := range selectionAllNotices(view) {
		input.AckNoticeIDs = append(input.AckNoticeIDs, notice.ID)
	}
	receipt, err := selectionServiceCommit(test, fixture, input)
	if err != nil || receipt.Phase != state.ImportCompleted || len(receipt.RepositoryOutcomes) != 1 || len(receipt.Notices) != 3 || len(receipt.SelectedSkills) != 0 || len(receipt.CreatedAssets) != 0 {
		test.Fatal("acknowledged repository-only commit did not preserve its exclusion receipt")
	}
}
