package desktop

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
)

func selectionLoadModel(test *testing.T, fixture *selectionFixture, source importv1.ImportSelectionSource) (*Model, func()) {
	test.Helper()
	model, pump := modelFixture(test, fixture.service)
	model.SetOrigin(fixture.server.URL)
	model.session.set(fixture.session)
	state := model.selectionState()
	state.Path, state.Source, state.UploadApproved = selectionExport(test, source), source, true
	model.PreviewSelection()
	pump()
	if state.Preview == nil {
		test.Fatal("real model preview unavailable", model.Status)
	}
	return model, pump
}

func TestSelectionTypedOptionsBindingsAndFinalConfirmation(test *testing.T) {
	fixture := newSelectionFixture(test)
	if _, err := fixture.library.Collect(test.Context(), fixture.owner, "example/toolkit", ""); err != nil {
		test.Fatal(err)
	}
	model, pump := selectionLoadModel(test, fixture, 1)
	state := model.selectionState()
	for index, form := range state.Configurations {
		switch form.Agent {
		case "claude":
			state.Configurations[index].Claude = configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY
		case "gemini":
			state.Configurations[index].Gemini = configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_API_KEY
			state.Configurations[index].Transport = configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_BEARER
		case "codex":
		default:
			continue
		}
		state.Configurations[index].Rename = true
		state.Configurations[index].Name = "Explicit " + form.Agent
	}
	model.SelectionOptionsChanged()
	if state.Preview != nil || state.Review != nil {
		test.Fatal("typed option retained approval")
	}
	model.PreviewSelection()
	pump()
	if state.Preview == nil {
		test.Fatal("typed option repreview failed", model.Status)
	}
	model.LoadSelectionBindings(0)
	pump()
	if len(state.Bindings) != 1 {
		test.Fatal("actual same-owner repository metadata missing", model.Status)
	}
	model.BindSelection(0)
	if state.Preview != nil {
		test.Fatal("binding failed to invalidate ticket")
	}
	model.PreviewSelection()
	pump()
	if state.View.Skills[0].Status != 1 {
		test.Fatal("explicit complete binding was not ready", model.Status)
	}
	model.ChooseSelection(0, 0, true)
	model.ChooseSelection(1, 0, true)
	for index := range state.View.Configurations {
		model.ChooseSelection(2, index, true)
	}
	model.ReviewSelection()
	if state.Review != nil {
		test.Fatal("required exclusions silently acknowledged")
	}
	for index := range state.Notices {
		state.Notices[index].Checked = true
	}
	model.ReviewSelection()
	if state.Review == nil || state.FinalApproved {
		test.Fatal("final review missing or preapproved", model.Status)
	}
	model.CommitSelection()
	pump()
	if fixture.count("CommitImportSelection") != 0 {
		test.Fatal("review without final approval wrote")
	}
	state.FinalApproved = true
	model.CommitSelection()
	model.CommitSelection()
	pump()
	if fixture.count("CommitImportSelection") != 1 || state.Result.Phase != 3 || state.Result.Repositories != 1 || state.Result.Skills != 1 || state.Result.Configurations != 3 {
		test.Fatal("three-kind actual receipt or single-write failed", model.Status, state.Result)
	}
}

func TestSelectionMagpieRequiresExplicitSameRepositoryRead(test *testing.T) {
	fixture := newSelectionFixture(test)
	if _, err := fixture.library.Collect(test.Context(), fixture.owner, "example/toolkit", ""); err != nil {
		test.Fatal(err)
	}
	model, pump := selectionLoadModel(test, fixture, 2)
	state := model.selectionState()
	if state.View.Skills[0].Status != 1 {
		test.Fatal("Magpie path resolution unavailable")
	}
	model.ChooseSelection(1, 0, true)
	if len(state.Choice.Skills) != 0 {
		test.Fatal("server READY bypassed explicit current-owner List/Get binding review")
	}
	model.LoadSelectionBindings(0)
	pump()
	model.BindSelection(0)
	if state.Preview == nil {
		test.Fatal("Magpie fixed-path confirmation invented unsupported binding options")
	}
	model.ChooseSelection(1, 0, true)
	if len(state.Choice.Skills) != 1 || fixture.count("ListRepositories") != 1 || fixture.count("GetRepository") != 1 {
		test.Fatal("explicit complete binding not selectable")
	}
}

func TestSelectionLostResponseIdentityBarrierAndReadOnlyStatus(test *testing.T) {
	fixture := newSelectionFixture(test)
	model, pump := selectionLoadModel(test, fixture, 3)
	state := model.selectionState()
	model.ChooseSelection(0, 0, true)
	for index := range state.Notices {
		state.Notices[index].Checked = true
	}
	model.ReviewSelection()
	state.FinalApproved = true
	connection := state.Scope.get()
	fixture.mutex.Lock()
	fixture.dropCommit = true
	fixture.mutex.Unlock()
	model.CommitSelection()
	pump()
	id := state.OperationID
	if id == "" || len(model.selectionBarriers) != 1 || state.Preview != nil || state.View != nil || state.Scope != nil || state.Hash != "" || len(state.VerifiedSkills) != 0 {
		test.Fatal("Unknown retained replayable state or lost operation barrier")
	}
	if !connection.private().closed || connection.private().identity.private().session != "" {
		test.Fatal("Unknown only hid the client without physically closing and clearing its owned session")
	}
	model.CommitSelection()
	model.Cancel()
	pump()
	model.session.set(fixture.session)
	state.UploadApproved = true
	model.PreviewSelection()
	pump()
	if fixture.count("PreviewImportSelection") != 1 || fixture.count("CommitImportSelection") != 1 {
		test.Fatal("same-owner new client bypassed Unknown barrier")
	}
	state.OperationID = id
	model.QuerySelectionStatus()
	pump()
	if state.Result.Phase != 3 || len(model.selectionBarriers) != 0 || fixture.count("GetImportCommitStatus") != 1 || fixture.count("CommitImportSelection") != 1 {
		test.Fatal("status did not reconcile original operation read-only", model.Status)
	}
}

func selectionApproveRepositories(test *testing.T, model *Model) {
	test.Helper()
	state := model.selectionState()
	model.ChooseSelection(0, 0, true)
	for index := range state.Notices {
		state.Notices[index].Checked = true
	}
	model.ReviewSelection()
	if state.Review == nil {
		test.Fatal("review unavailable", model.Status)
	}
	state.FinalApproved = true
}

func TestSelectionMutationGuardsCannotBeBypassedByUIState(test *testing.T) {
	for _, scenario := range []string{"no-approval", "choice", "notice", "path", "source", "expiry", "session", "option", "file-bytes"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newSelectionFixture(test)
			model, pump := selectionLoadModel(test, fixture, 1)
			state := model.selectionState()
			selectionApproveRepositories(test, model)
			switch scenario {
			case "no-approval":
				state.FinalApproved = false
			case "choice":
				state.Choice.Repositories = []int{100}
			case "notice":
				state.Notices[0].Checked = false
			case "path":
				state.Path += ".changed"
			case "source":
				state.Source = 2
			case "expiry":
				state.View.ExpiresMs = time.Now().Add(-time.Second).UnixMilli()
			case "session":
				model.session.set(fixture.foreign)
			case "option":
				state.Configurations[0].Rename = true
				state.Configurations[0].Name = "not-approved"
			case "file-bytes":
				if err := os.WriteFile(state.Path, []byte("not the previewed source"), 0o600); err != nil {
					test.Fatal(err)
				}
			}
			model.CommitSelection()
			pump()
			if fixture.count("CommitImportSelection") != 0 || fixture.downloads.Load() != 0 {
				test.Fatal("mutated authorization reached a write", scenario)
			}
		})
	}
}

func TestSelectionCancellationIdentityAndActualDrain(test *testing.T) {
	for _, scenario := range []string{"cancel", "origin", "logout", "close", "page", "file"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newSelectionFixture(test)
			model, pump := modelFixture(test, fixture.service)
			model.SetOrigin(fixture.server.URL)
			model.session.set(fixture.session)
			model.Page = 2
			state := model.selectionState()
			state.Source, state.Path, state.UploadApproved = 3, selectionExport(test, 3), true
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			fixture.mutex.Lock()
			fixture.intercept = func(writer http.ResponseWriter, request *http.Request) bool {
				if !strings.HasSuffix(request.URL.Path, "/PreviewImportSelection") {
					return false
				}
				_, _ = io.Copy(io.Discard, request.Body)
				close(entered)
				select {
				case <-request.Context().Done():
					close(canceled)
				case <-release:
				}
				return true
			}
			fixture.mutex.Unlock()
			defer close(release)
			model.PreviewSelection()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				test.Fatal("preview never entered")
			}
			scope := state.Scope
			switch scenario {
			case "cancel":
				model.Cancel()
			case "origin":
				model.SetOrigin("https://new.example.test")
			case "logout":
				model.Logout()
			case "close":
				model.Close()
			case "page":
				model.configurationNavigate(0)
			case "file":
				model.SelectionFileChanged()
			}
			pump()
			select {
			case <-canceled:
			case <-time.After(5 * time.Second):
				test.Fatal("physical HTTP work not canceled")
			}
			if state.View != nil || state.Preview != nil || state.Scope != nil || model.Busy || model.pendingJobs.Load() != 0 {
				test.Fatal("stale source repopulated or worker not drained")
			}
			if _, err := scope.connection.Preview(test.Context(), 3, state.Path, "", nil); err == nil {
				test.Fatal("detached client remained usable")
			}
		})
	}
}

func TestSelectionUnknownSurvivesOwnerAndOriginRoundTrip(test *testing.T) {
	fixture := newSelectionFixture(test)
	model, pump := selectionLoadModel(test, fixture, 3)
	state := model.selectionState()
	selectionApproveRepositories(test, model)
	fixture.mutex.Lock()
	fixture.dropCommit = true
	fixture.mutex.Unlock()
	model.CommitSelection()
	pump()
	id := state.OperationID
	model.SetOrigin("https://other.example.test")
	pump()
	model.SetOrigin(fixture.server.URL)
	pump()
	model.session.set(fixture.foreign)
	state.OperationID = id
	model.QuerySelectionStatus()
	pump()
	if state.Result.Observation != 1 || len(model.selectionBarriers) != 1 {
		test.Fatal("foreign NotObserved cleared another owner's barrier")
	}
	model.Cancel()
	pump()
	model.session.set(fixture.session)
	state.Source, state.Path, state.UploadApproved = 3, selectionExport(test, 3), true
	before := fixture.count("PreviewImportSelection")
	model.PreviewSelection()
	pump()
	if fixture.count("PreviewImportSelection") != before || len(model.selectionBarriers) != 1 {
		test.Fatal("round trip bypassed unknown ownership")
	}
	for _, value := range []any{model, state, state.Scope, state.Scope.connection, state.Scope.connection.private(), state.Scope.connection.private().identity} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			text := fmt.Sprintf(format, value)
			if strings.Contains(text, fixture.session) || strings.Contains(text, "SYNTHETIC-PRIVATE-MARKER") {
				test.Fatal("format-visible source or bearer")
			}
		}
	}
}

func TestSelectionCommittedThenCanceledDrainsWithoutReplay(test *testing.T) {
	fixture := newSelectionFixture(test)
	model, pump := selectionLoadModel(test, fixture, 3)
	state := model.selectionState()
	selectionApproveRepositories(test, model)
	entered, release := make(chan struct{}), make(chan struct{})
	fixture.mutex.Lock()
	fixture.responseEdit = func(path string, body []byte) []byte {
		if strings.HasSuffix(path, "/CommitImportSelection") {
			close(entered)
			<-release
		}
		return body
	}
	fixture.mutex.Unlock()
	model.CommitSelection()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		test.Fatal("actual committed response did not reach barrier")
	}
	model.Cancel()
	model.session.set(fixture.session)
	state.UploadApproved = true
	model.PreviewSelection()
	if fixture.count("PreviewImportSelection") != 1 || model.pendingJobs.Load() == 0 {
		close(release)
		test.Fatal("new source was admitted before old consumer drained")
	}
	close(release)
	pump()
	if len(model.selectionBarriers) != 1 || state.Preview != nil || state.Scope != nil || state.View != nil || model.pendingJobs.Load() != 0 {
		test.Fatal("late committed response escaped canceled generation")
	}
	model.QuerySelectionStatus()
	pump()
	if state.Result.Phase != 3 || fixture.count("CommitImportSelection") != 1 || fixture.count("GetImportCommitStatus") != 1 || len(model.selectionBarriers) != 0 {
		test.Fatal("canceled actual commit was replayed or lost instead of read-only reconciliation")
	}
}
