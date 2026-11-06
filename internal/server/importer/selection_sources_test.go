package importer

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
)

var selectionSourcesFixture = []byte(`{"skills":[{"name":"review-guide","source":{"kind":"github","repo":"example/review","path":"skills/review"}},{"name":"another-guide","source":{"kind":"github","repo":"example/review","path":"skills/another"}},{"name":"root-guide","source":{"kind":"github","repo":"example/root","path":"."}}]}`)

func TestSelectionRegisterSourcesColdLibrary(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	raw := []byte(`{"skills":[{"name":"review-guide","source":{"kind":"github","repo":"example/review","path":"skills/review"}},{"name":"root-guide","source":{"kind":"github","repo":"example/root"}}]}`)
	preview := selectionServicePreview(test, fixture, SelectionMagpie, raw, SelectionOptions{RegisterSources: true})
	view, err := preview.View()
	if err != nil {
		test.Fatal(err)
	}
	if len(view.Skills) != 2 || int(view.Skills[0].Status) != 9 || int(view.Skills[1].Status) != 9 {
		test.Fatal("cold-library skills must be selectable as saved source references")
	}
	if fixture.counts.size() != 0 {
		test.Fatal("source preview performed a network request")
	}
}

func sourceSelectionInput(test *testing.T, fixture selectionServiceFixture, raw []byte, source SelectionSource, options SelectionOptions, mask int, operation byte) SelectionCommitInput {
	test.Helper()
	preview := selectionServicePreview(test, fixture, source, raw, options)
	view, err := preview.View()
	if err != nil {
		test.Fatal(err)
	}
	input := SelectionCommitInput{Ticket: preview.RevealTicket(), Source: source, SourceData: raw, Options: options, OperationID: [16]byte{operation}}
	if mask&1 != 0 {
		for _, candidate := range view.Repositories {
			input.RepositoryIDs = append(input.RepositoryIDs, candidate.ID)
		}
	}
	if mask&2 != 0 {
		for _, candidate := range view.Skills {
			if candidate.Status != SelectionCandidateSourceReferenceReady || candidate.SourceReference == nil || candidate.Binding != nil {
				test.Fatal("unfetched skill was not a plain source reference")
			}
			input.SkillIDs = append(input.SkillIDs, candidate.ID)
		}
	}
	for _, notice := range selectionAllNotices(view) {
		input.AckNoticeIDs = append(input.AckNoticeIDs, notice.ID)
	}
	return input
}

func TestSelectionRegisterSourcesOfflineAndIdempotent(test *testing.T) {
	for _, mask := range []int{1, 2, 3} {
		test.Run(fmt.Sprint(mask), func(test *testing.T) {
			fixture := newSelectionServiceFixture(test)
			options := SelectionOptions{RegisterSources: true}
			input := sourceSelectionInput(test, fixture, selectionSourcesFixture, SelectionMagpie, options, mask, 11)
			receipt, err := selectionServiceCommit(test, fixture, input)
			if err != nil || receipt.Phase != state.ImportCompleted || fixture.counts.size() != 0 {
				test.Fatal("offline import failed or downloaded content", err)
			}
			if len(receipt.SelectedSkills) != 0 || len(receipt.SelectedSkillSources) != len(input.SkillIDs) {
				test.Fatal("source references masqueraded as verified bindings")
			}
			for _, outcome := range receipt.RepositoryOutcomes {
				if outcome.Code != state.ImportRepositoryRegistered || outcome.RepositoryID == "" {
					test.Fatal("registration outcome was not explicit")
				}
			}
			for _, source := range receipt.SelectedSkillSources {
				if source.SourceID == "" || source.Repository == "" {
					test.Fatal("source reference was not persisted")
				}
			}
			credential, err := fixture.authentication.AuthenticateImport(test.Context(), fixture.bearer)
			if err != nil {
				test.Fatal(err)
			}
			stored, found, err := fixture.store.ReadImportReceipt(test.Context(), credential, input.OperationID)
			if err != nil || !found || stored.Phase != state.ImportCompleted || len(stored.SelectedSkillSources) != len(input.SkillIDs) {
				test.Fatal("committed source receipt did not survive readback", err)
			}
			replayed, err := selectionServiceCommit(test, fixture, input)
			if err != nil || replayed.Revision != receipt.Revision {
				test.Fatal("terminal receipt replay was not idempotent", err)
			}
			second := sourceSelectionInput(test, fixture, selectionSourcesFixture, SelectionMagpie, options, mask, 12)
			again, err := selectionServiceCommit(test, fixture, second)
			if err != nil || again.Phase != state.ImportCompleted {
				test.Fatal("repeat registration failed", err)
			}
			for _, outcome := range again.RepositoryOutcomes {
				if outcome.Code != state.ImportRepositoryAlreadyRegistered {
					test.Fatal("repeat import duplicated a repository source")
				}
			}
			sources, err := fixture.store.ListRepositorySources(test.Context(), fixture.owner)
			if err != nil || len(sources) != 2 {
				test.Fatal("registered repository sources absent", err)
			}
			count := 0
			for _, source := range sources {
				count += len(source.Skills)
				if source.CollectedRepositoryID != "" {
					test.Fatal("unfetched source falsely reported collected")
				}
			}
			if count != len(input.SkillIDs) || fixture.counts.size() != 0 {
				test.Fatal("duplicate references or network use")
			}
			verified, err := fixture.store.ListCollectedRepositories(test.Context(), fixture.owner)
			if err != nil || len(verified) != 0 {
				test.Fatal("source registration fabricated collected content", err)
			}
			if err := fixture.content.Close(); err != nil {
				test.Fatal(err)
			}
			if err := fixture.store.Close(); err != nil {
				test.Fatal(err)
			}
			reopened, err := state.Open(test.Context(), fixture.path, bytes.Repeat([]byte{81}, 32))
			if err != nil {
				test.Fatal(err)
			}
			defer reopened.Close()
			persisted, err := reopened.ListRepositorySources(test.Context(), fixture.owner)
			if err != nil || len(persisted) != 2 || persisted[0].ID != sources[0].ID {
				test.Fatal("source registration did not survive restart", err)
			}
		})
	}
}

func TestSelectionSourcesRetryAndOwnerIsolation(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	input := sourceSelectionInput(test, fixture, selectionSourcesFixture, SelectionMagpie, SelectionOptions{RegisterSources: true}, 3, 21)
	if _, err := selectionServiceCommit(test, fixture, input); err != nil {
		test.Fatal(err)
	}
	other, err := fixture.authentication.Register(test.Context(), "source-other", []byte("synthetic-other-password"))
	if err != nil {
		test.Fatal(err)
	}
	foreign, err := fixture.store.ListRepositorySources(test.Context(), other)
	if err != nil || len(foreign) != 0 {
		test.Fatal("other owner saw private source references", err)
	}
	sources, err := fixture.store.ListRepositorySources(test.Context(), fixture.owner)
	if err != nil || len(sources) != 2 {
		test.Fatal(err)
	}
	if err := fixture.store.DeleteRepositorySource(test.Context(), other, sources[0].ID); !errors.Is(err, state.ErrRepositoryNotFound) {
		test.Fatal("other owner deleted a private source", err)
	}
	fixture.counts.mutex.Lock()
	fixture.counts.hook = func(output http.ResponseWriter, _ *http.Request) bool {
		output.WriteHeader(http.StatusServiceUnavailable)
		return true
	}
	fixture.counts.mutex.Unlock()
	if _, err := fixture.content.Collect(test.Context(), fixture.owner, "example/review", ""); !errors.Is(err, repository.ErrArchiveUnavailable) {
		test.Fatal("failed synchronization was not reported", err)
	}
	retained, err := fixture.store.ListRepositorySources(test.Context(), fixture.owner)
	if err != nil || len(retained) != 2 || retained[0].ID != sources[0].ID {
		test.Fatal("network failure discarded saved sources", err)
	}
	fixture.counts.mutex.Lock()
	fixture.counts.hook = nil
	fixture.counts.mutex.Unlock()
	collected, err := fixture.content.Collect(test.Context(), fixture.owner, "example/review", "")
	if err != nil {
		test.Fatal(err)
	}
	sources, err = fixture.store.ListRepositorySources(test.Context(), fixture.owner)
	if err != nil {
		test.Fatal(err)
	}
	for _, source := range sources {
		if source.NameWithOwner == "example/review" {
			if source.CollectedRepositoryID != collected.ID || len(source.Skills) != 2 {
				test.Fatal("explicit sync did not link verified content")
			}
			if err := fixture.store.DeleteRepositorySource(test.Context(), fixture.owner, source.ID); err != nil {
				test.Fatal(err)
			}
		}
	}
	if _, err := fixture.store.CollectedRepository(test.Context(), fixture.owner, collected.ID); err != nil {
		test.Fatal("removing a source deleted verified skills", err)
	}
}

func TestSelectionSourcesModeBoundToPreview(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	input := sourceSelectionInput(test, fixture, selectionSourcesFixture, SelectionMagpie, SelectionOptions{RegisterSources: true}, 3, 31)
	input.Options.RegisterSources = false
	if _, err := selectionServiceCommit(test, fixture, input); !errors.Is(err, state.ErrImportPrecondition) {
		test.Fatal("commit changed its sealed registration mode", err)
	}
	sources, err := fixture.store.ListRepositorySources(test.Context(), fixture.owner)
	if err != nil || len(sources) != 0 || fixture.counts.size() != 0 {
		test.Fatal("rejected preview wrote or downloaded sources", err)
	}
}

func TestSelectionCCSwitchColdSourcesIgnoreLocalPaths(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	statements, err := os.ReadFile("testdata/selection/ccswitch-schema24.sql")
	if err != nil {
		test.Fatal(err)
	}
	raw := selectionServiceSnapshot(test, string(statements)+`DELETE FROM skill_repos;`)
	defer clear(raw)
	input := sourceSelectionInput(test, fixture, raw, SelectionCCSwitch24, SelectionOptions{RegisterSources: true}, 2, 41)
	receipt, err := selectionServiceCommit(test, fixture, input)
	if err != nil || receipt.Phase != state.ImportCompleted || len(receipt.SelectedSkillSources) != 1 || len(receipt.CreatedAssets) != 0 || len(receipt.SelectedSkills) != 0 {
		test.Fatal("cc-switch cold skill-only import failed", err)
	}
	if receipt.SelectedSkillSources[0].LogicalPath != "" {
		test.Fatal("cc-switch local directory was guessed to be a repository path")
	}
	sources, err := fixture.store.ListRepositorySources(test.Context(), fixture.owner)
	if err != nil || len(sources) != 1 || len(sources[0].Skills) != 1 || sources[0].Skills[0].Path != "" || fixture.counts.size() != 0 {
		test.Fatal("skill-only import failed to register the required repository", err)
	}
}

func TestSelectionSourceCapacityRollsBackWholeRegistration(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	pool, err := sql.Open("sqlite3", fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer pool.Close()
	transaction, err := pool.BeginTx(test.Context(), nil)
	if err != nil {
		test.Fatal(err)
	}
	defer transaction.Rollback()
	for index := range 255 {
		_, err := transaction.ExecContext(test.Context(), `INSERT INTO repository_sources(public_id,user_id,name_with_owner,branch,skills,registered_at) SELECT ?,id,?,'','[]',? FROM users WHERE user_key=?`, rand.Text(), fmt.Sprintf("quota/source-%d", index), time.Now(), fixture.owner)
		if err != nil {
			test.Fatal(err)
		}
	}
	if err := transaction.Commit(); err != nil {
		test.Fatal(err)
	}
	input := sourceSelectionInput(test, fixture, selectionSourcesFixture, SelectionMagpie, SelectionOptions{RegisterSources: true}, 3, 51)
	receipt, err := selectionServiceCommit(test, fixture, input)
	if err != nil || receipt.Phase != state.ImportFailed || len(receipt.SelectedSkills)+len(receipt.SelectedSkillSources)+len(receipt.CreatedAssets) != 0 {
		test.Fatal("capacity rollback did not leave a safe terminal receipt", err)
	}
	sources, err := fixture.store.ListRepositorySources(test.Context(), fixture.owner)
	if err != nil || len(sources) != 255 || fixture.counts.size() != 0 {
		test.Fatal("capacity failure left partial sources or attempted a download", err)
	}
	for _, source := range sources {
		if source.NameWithOwner == "example/root" || source.NameWithOwner == "example/review" {
			test.Fatal("short transaction retained a partially registered source")
		}
	}
}

func TestSelectionSkillSourceCapacityBoundaryAndRollback(test *testing.T) {
	for _, scenario := range []struct {
		name               string
		initialReferences  int
		phase              state.ImportReceiptPhase
		expectedSources    int
		expectedReferences int
	}{
		{name: "exact-limit", initialReferences: 509, phase: state.ImportCompleted, expectedSources: 2, expectedReferences: 512},
		{name: "overflow", initialReferences: 511, phase: state.ImportFailed, expectedSources: 1, expectedReferences: 511},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			fixture := newSelectionServiceFixture(test)
			pool, err := sql.Open("sqlite3", fixture.path)
			if err != nil {
				test.Fatal(err)
			}
			defer pool.Close()
			references := make([]repository.SkillSource, scenario.initialReferences)
			for index := range references {
				references[index] = repository.SkillSource{Name: fmt.Sprintf("quota-guide-%d", index), Path: fmt.Sprintf("skills/quota-%d", index)}
			}
			encoded, err := json.Marshal(references)
			if err != nil {
				test.Fatal(err)
			}
			_, err = pool.ExecContext(test.Context(), `INSERT INTO repository_sources(public_id,user_id,name_with_owner,branch,skills,registered_at) SELECT ?,id,'example/review','',?,? FROM users WHERE user_key=?`, rand.Text(), string(encoded), time.Now(), fixture.owner)
			if err != nil {
				test.Fatal(err)
			}
			input := sourceSelectionInput(test, fixture, selectionSourcesFixture, SelectionMagpie, SelectionOptions{RegisterSources: true}, 3, 71)
			receipt, err := selectionServiceCommit(test, fixture, input)
			if err != nil || receipt.Phase != scenario.phase || fixture.counts.size() != 0 {
				test.Fatal("skill source quota did not produce a safe offline terminal receipt", err)
			}
			if scenario.phase == state.ImportFailed && len(receipt.SelectedSkills)+len(receipt.SelectedSkillSources)+len(receipt.CreatedAssets) != 0 {
				test.Fatal("skill source quota failure retained partial effects in the receipt")
			}
			saved, err := fixture.store.ListRepositorySources(test.Context(), fixture.owner)
			if err != nil || len(saved) != scenario.expectedSources {
				test.Fatal("skill source quota failure retained a partially registered repository", err)
			}
			count := 0
			for _, source := range saved {
				count += len(source.Skills)
			}
			if count != scenario.expectedReferences {
				test.Fatal("skill source quota did not preserve the exact reference boundary")
			}
			replayed, err := selectionServiceCommit(test, fixture, input)
			if err != nil || replayed.Phase != receipt.Phase || replayed.Revision != receipt.Revision {
				test.Fatal("skill source quota terminal receipt was not safely replayable", err)
			}
		})
	}
}

func TestSelectionSourcesKeepVerifiedBindingsDistinct(test *testing.T) {
	fixture := newSelectionServiceFixture(test)
	if _, err := fixture.content.Collect(test.Context(), fixture.owner, "example/review", ""); err != nil {
		test.Fatal(err)
	}
	before := fixture.counts.size()
	raw := []byte(`{"skills":[{"name":"review-guide","source":{"kind":"github","repo":"example/review","path":"skills/review"}},{"name":"root-guide","source":{"kind":"github","repo":"example/root","path":"."}}]}`)
	options := SelectionOptions{RegisterSources: true}
	preview := selectionServicePreview(test, fixture, SelectionMagpie, raw, options)
	view, err := preview.View()
	if err != nil || view.Skills[0].Binding == nil || view.Skills[0].SourceReference != nil || view.Skills[1].Binding != nil || view.Skills[1].SourceReference == nil {
		test.Fatal("verified content and source hints were not kept distinct", err)
	}
	input := SelectionCommitInput{Ticket: preview.RevealTicket(), Source: SelectionMagpie, SourceData: raw, Options: options, OperationID: [16]byte{61}}
	for _, candidate := range view.Repositories {
		input.RepositoryIDs = append(input.RepositoryIDs, candidate.ID)
	}
	for _, candidate := range view.Skills {
		input.SkillIDs = append(input.SkillIDs, candidate.ID)
	}
	for _, notice := range selectionAllNotices(view) {
		input.AckNoticeIDs = append(input.AckNoticeIDs, notice.ID)
	}
	receipt, err := selectionServiceCommit(test, fixture, input)
	if err != nil || receipt.Phase != state.ImportCompleted || len(receipt.SelectedSkills) != 1 || len(receipt.SelectedSkillSources) != 1 || receipt.SelectedSkills[0] != *view.Skills[0].Binding || receipt.SelectedSkillSources[0].Repository != "example/root" || fixture.counts.size() != before {
		test.Fatal("mixed import changed a verified pin or downloaded missing content", err)
	}
}
