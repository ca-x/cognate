package desktop

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/mcpclient"
)

func mcpLoadController(test *testing.T, fixture *mcpDesktopFixture) (*Model, chan func()) {
	test.Helper()
	model, queue := configurationModelFixture(test, fixture.service)
	model.SetOrigin(fixture.origin)
	model.session.set(fixture.session)
	model.Page = 4
	model.configurationState().MCPActive = true
	model.LoadMCPAssets()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if model.mcpState().session() == nil {
		test.Fatal("controller did not open actual owner session", model.Status)
	}
	return model, queue
}

func mcpControllerForm(test *testing.T, model *Model) *mcpForm {
	test.Helper()
	model.mcpAction("new")
	form := model.mcpState().Form
	form.Draft, form.Dirty = mcpDesktopDraft(), true
	if _, err := form.syncSlots(); err != nil {
		test.Fatal(err)
	}
	for _, slot := range form.Slots {
		slot.Action, slot.ValueSet = mcpclient.Replace, true
		slot.Value.set("synthetic-controller-credential")
	}
	return form
}

func mcpApproveController(test *testing.T, model *Model, queue chan func()) {
	test.Helper()
	state := model.mcpState()
	if state.Review == nil {
		test.Fatal("actual prepared review missing", model.Status)
	}
	state.Confirmation = state.Review.Confirmation
	for index := range state.Authorized {
		state.Authorized[index] = true
	}
	model.SubmitMCP()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
}

func TestMCPControllerPreviewApprovalAndOneShot(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	model, queue := mcpLoadController(test, fixture)
	form := mcpControllerForm(test, model)
	model.PrepareMCP("preview")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	state := model.mcpState()
	if state.Preview == "" || strings.Contains(state.Preview, "synthetic-controller-credential") || fixture.commits(test, "create") != 0 || fixture.count("Preview") != 1 {
		test.Fatal("preview was absent, secret-bearing or mutating", model.Status)
	}
	for _, slot := range form.Slots {
		if slot.Value.value() != "" || slot.ValueSet {
			test.Fatal("preview retained replacement")
		}
	}
	model.PrepareMCP("create")
	if state.Review != nil || model.Busy {
		test.Fatal("cleared replacement silently became an empty replacement")
	}
	for _, slot := range form.Slots {
		slot.ValueSet = true
		slot.Value.set("synthetic-controller-credential")
	}
	model.PrepareMCP("create")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Review == nil || len(state.Review.Slots) != 2 || fixture.commits(test, "create") != 0 {
		test.Fatal("no actual per-slot review", model.Status)
	}
	state.Confirmation = state.Review.Confirmation
	model.SubmitMCP()
	if model.Busy || fixture.count("CreateAsset") != 0 {
		test.Fatal("unchecked replacement authorized")
	}
	for index := range state.Authorized {
		state.Authorized[index] = true
	}
	state.Confirmation = "yes"
	model.SubmitMCP()
	if model.Busy || fixture.count("CreateAsset") != 0 {
		test.Fatal("inexact confirmation accepted")
	}
	mcpApproveController(test, model, queue)
	model.SubmitMCP()
	model.PrepareMCP("create")
	if !state.Blocked || state.Review != nil || fixture.count("CreateAsset") != 1 || fixture.commits(test, "create") != 1 {
		test.Fatal("controller submission replayed", model.Status)
	}
	for _, procedure := range []string{"BeginNativeConsumer", "AuthorizeNativeMaterialization", "ConsumeNativeMaterialization"} {
		if fixture.count(procedure) != 0 {
			test.Fatal("asset edit crossed native boundary")
		}
	}
	if _, err := os.Stat(filepath.Join(fixture.service.Home, ".config", "skillhub", "client.key")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("asset edit discovered or initialized native key")
	}
}

func TestMCPFormRemovedReferencesAndExplicitEmpty(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	assets := mcpDesktopModel(test, fixture)
	created := mcpDesktopCreate(test, assets)
	view, err := assets.Read(test.Context(), created.ID)
	if err != nil {
		test.Fatal(err)
	}
	form := newMCPForm(view)
	form.Draft.Servers[0].Stdio.Args = nil
	input, err := form.input(test.Context())
	if err == nil {
		_, err = assets.Prepare(test.Context(), mcpclient.Change{Action: "update", ID: created.ID, Revision: 1}, input)
		input.Close()
	}
	if !errors.Is(err, mcpclient.ErrInvalid) || len(form.Slots) != 2 {
		test.Fatal("removed original ref was dropped or implicitly cleared", err)
	}
	form.Slots[0].Action, form.Slots[0].Confirmed = mcpclient.Clear, true
	form.Slots[1].Action, form.Slots[1].ValueSet = mcpclient.Replace, true
	input, err = form.input(test.Context())
	if err != nil {
		test.Fatal("explicit empty replacement refused", err)
	}
	defer input.Close()
	review, err := assets.Prepare(test.Context(), mcpclient.Change{Action: "update", ID: created.ID, Revision: 1}, input)
	if err != nil || len(review.Slots) != 2 {
		test.Fatal("removed CLEAR/empty REPLACE missing from approval", err)
	}
	if _, err := assets.Submit(test.Context(), review.Confirmation); err != nil {
		test.Fatal(err)
	}
	view, err = assets.Read(test.Context(), created.ID)
	if err != nil || len(view.Secrets) != 1 || !view.Secrets[0].Present || view.Draft.Servers[0].Stdio.Args != nil {
		test.Fatal("clear and present-empty replacement lost", err)
	}
}

func TestMCPControllerCancelAndOwnerDrain(test *testing.T) {
	for _, action := range []string{"cancel", "origin", "close"} {
		test.Run(action, func(test *testing.T) {
			fixture := newMCPDesktopFixture(test)
			assets := mcpDesktopModel(test, fixture)
			created := mcpDesktopCreate(test, assets)
			model, queue := mcpLoadController(test, fixture)
			entered, released := make(chan struct{}), make(chan struct{})
			var once sync.Once
			fixture.mutex.Lock()
			fixture.intercept = func(writer http.ResponseWriter, request *http.Request) bool {
				if !strings.HasSuffix(request.URL.Path, "/ReadDraft") {
					return false
				}
				_, _ = io.Copy(io.Discard, request.Body)
				_ = request.Body.Close()
				once.Do(func() { close(entered) })
				select {
				case <-request.Context().Done():
				case <-test.Context().Done():
				}
				close(released)
				return true
			}
			fixture.mutex.Unlock()
			model.mcpAction(created.ID)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				test.Fatal("actual read did not enter cancellation barrier")
			}
			old := model.mcpState()
			switch action {
			case "cancel":
				model.Cancel()
			case "origin":
				model.SetOrigin("https://different.synthetic.invalid")
			case "close":
				model.Close()
			}
			model.Wait()
			select {
			case <-released:
			case <-time.After(5 * time.Second):
				test.Fatal("actual HTTP request not canceled/drained")
			}
			for len(queue) > 0 {
				(<-queue)()
			}
			if old.session() != nil || model.pendingJobs.Load() != 0 || old.Form != nil || fixture.commits(test, "create") != 1 {
				test.Fatal("owner/cancel left work or stale result")
			}
		})
	}
}

func TestMCPControllerUnknownAndReviewCancellation(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	model, queue := mcpLoadController(test, fixture)
	mcpControllerForm(test, model)
	model.PrepareMCP("create")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	state := model.mcpState()
	model.CancelMCPReview()
	if state.Review != nil || state.session().private().pending != nil || fixture.count("CreateAsset") != 0 {
		test.Fatal("cancel retained actual submission capability")
	}
	for _, slot := range state.Form.Slots {
		slot.ValueSet = true
	}
	model.PrepareMCP("create")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	fixture.mutex.Lock()
	fixture.drop = "CreateAsset"
	fixture.mutex.Unlock()
	mcpApproveController(test, model, queue)
	model.SubmitMCP()
	model.PrepareMCP("create")
	if !state.Blocked || !strings.Contains(model.Status, "Unknown") || fixture.count("CreateAsset") != 1 || fixture.commits(test, "create") != 1 {
		test.Fatal("Unknown was replayable or not physically committed", model.Status)
	}
	model.LoadMCPAssets()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if len(model.mcpState().Assets) != 1 || model.mcpState().Form != nil || fixture.count("CreateAsset") != 1 {
		test.Fatal("explicit reconciliation replayed draft")
	}
}

func TestMCPFormCanceledInput(test *testing.T) {
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	form := newMCPForm(mcpclient.EditView{Draft: mcpDesktopDraft()})
	if input, err := form.input(ctx); !errors.Is(err, context.Canceled) {
		if input != nil {
			input.Close()
		}
		test.Fatal("canceled form admitted")
	}
}

func TestMCPControllerOwnerChangeDropsSuccessfulQueuedRead(test *testing.T) {
	fixture := newMCPDesktopFixture(test)
	assets := mcpDesktopModel(test, fixture)
	created := mcpDesktopCreate(test, assets)
	model, queue := mcpLoadController(test, fixture)
	model.mcpAction(created.ID)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	var delivery func()
	select {
	case delivery = <-queue:
	case <-timer.C:
		test.Fatal("successful read delivery missing")
	}
	old := model.mcpState()
	model.SetOrigin("https://another.synthetic.invalid")
	delivery()
	model.Wait()
	if old.Form != nil || old.session() != nil || model.configuration != nil || model.Busy || model.session.value() != "" {
		test.Fatal("successful old-owner delivery repainted or kept authority")
	}
}
