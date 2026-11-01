package desktop

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func configurationOrderLoadModel(test *testing.T, fixture *configurationFixture) (*Model, chan func()) {
	test.Helper()
	model, queue := configurationModelFixture(test, fixture.service)
	model.SetOrigin(fixture.server.URL)
	model.session.set(fixture.session)
	model.Page = 6
	model.LoadConfigurationOrder()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if !model.orderState().Loaded || model.orderState().OwnerID != fixture.owner {
		test.Fatal("native order did not load authenticated partition")
	}
	return model, queue
}

func TestConfigurationOrderModelCancelsRealTLSRead(test *testing.T) {
	fixture := newConfigurationFixture(test)
	defer fixture.server.CloseClientConnections()
	configurationOrderSeed(test, fixture, fixture.owner, "claude-desktop", "mcp", "Cancelable metadata")
	started, canceled := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		if !strings.HasSuffix(request.URL.Path, "/GetAssetOrder") {
			return false
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(request.Body, 128<<10))
		_ = request.Body.Close()
		close(started)
		select {
		case <-request.Context().Done():
			close(canceled)
		case <-release:
		}
		return true
	})
	model, queue := configurationModelFixture(test, fixture.service)
	model.SetOrigin(fixture.server.URL)
	model.session.set(fixture.session)
	model.LoadConfigurationOrder()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		test.Fatal("real TLS order read did not begin")
	}
	model.Cancel()
	model.Wait()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		test.Fatal("real TLS order read was not canceled")
	}
	for len(queue) > 0 {
		(<-queue)()
	}
	if model.order != nil || model.pendingJobs.Load() != 0 {
		test.Fatal("canceled read published stale metadata or failed to drain")
	}
}

func configurationOrderPrepare(test *testing.T, model *Model) string {
	test.Helper()
	state := model.orderState()
	state.Selected = 0
	state.move(1)
	model.PrepareConfigurationOrder()
	if state.Review == nil {
		test.Fatal("complete order review missing")
	}
	state.Confirmation = state.Review.Confirmation
	return state.Confirmation
}

func TestConfigurationOrderModelIndependentReviewAndDirty(test *testing.T) {
	fixture := newConfigurationFixture(test)
	configurationOrderSeed(test, fixture, fixture.owner, "grokbuild", "provider", "First")
	configurationOrderSeed(test, fixture, fixture.owner, "grokbuild", "provider", "Second")
	model, queue := configurationOrderLoadModel(test, fixture)
	form := &configurationForm{Dirty: true, Name: "unsaved Claude"}
	model.configurationState().Form = form
	grok := &grokForm{Dirty: true, Name: "unsaved Grok"}
	model.grokState().Form = grok
	state := model.orderState()
	confirmation := configurationOrderPrepare(test, model)
	if fixture.count("ReplaceAssetOrder") != 0 || state.Review.OwnerID != fixture.owner || state.Review.Origin != fixture.server.URL || state.Review.Before.Revision != state.Snapshot.Revision || len(state.Review.IDs) != 2 {
		test.Fatal("order review lost complete identity/revision binding or wrote early")
	}
	state.Confirmation = "inexact"
	model.SubmitConfigurationOrder()
	if model.Busy || fixture.count("ReplaceAssetOrder") != 0 || state.Review == nil {
		test.Fatal("inexact confirmation consumed or submitted review")
	}
	state.Confirmation = confirmation
	expected := slices.Clone(state.Review.IDs)
	model.SubmitConfigurationOrder()
	model.SubmitConfigurationOrder()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Blocked || state.Review != nil || state.dirty() || !slices.Equal(configurationOrderIDs(state.Snapshot.Assets), expected) || fixture.count("ReplaceAssetOrder") != 1 {
		test.Fatal("order submit was not single-use complete CAS")
	}
	if !form.Dirty || model.configurationState().Form != form || !grok.Dirty || model.grokState().Form != grok {
		test.Fatal("sorting cleared or replaced an unrelated unsaved content draft")
	}
	configurationOrderNoContentCalls(test, fixture)
}

func TestConfigurationOrderModelConflictAndUnknownRequireFreshReview(test *testing.T) {
	for _, scenario := range []string{"conflict", "unknown"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			configurationOrderSeed(test, fixture, fixture.owner, "codex", "mcp", "First")
			configurationOrderSeed(test, fixture, fixture.owner, "codex", "mcp", "Second")
			model, queue := configurationOrderLoadModel(test, fixture)
			state := model.orderState()
			oldConfirmation := configurationOrderPrepare(test, model)
			before := state.Review.Before
			if scenario == "conflict" {
				identity := configurationTestIdentity(test, fixture)
				if _, err := fixture.service.ReplaceConfigurationOrder(test.Context(), identity, before, state.Review.IDs); err != nil {
					test.Fatal("peer CAS fixture failed")
				}
				identity.clear()
			} else {
				configurationOrderDropReceipt(test, fixture)
			}
			reads := fixture.count("GetAssetOrder")
			model.SubmitConfigurationOrder()
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if !state.Blocked || state.Review != nil || fixture.count("GetAssetOrder") != reads || scenario == "unknown" && !strings.Contains(model.Status, "Unknown") {
				test.Fatal("failed mutation auto-read, auto-replayed or retained review")
			}
			mutations := fixture.count("ReplaceAssetOrder")
			model.PrepareConfigurationOrder()
			state.Confirmation = oldConfirmation
			model.SubmitConfigurationOrder()
			if state.Review != nil || fixture.count("ReplaceAssetOrder") != mutations {
				test.Fatal("blocked old approval could be replayed")
			}
			fixture.setIntercept(nil)
			model.LoadConfigurationOrder()
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if state.Blocked || state.Snapshot.Revision != before.Revision+1 || state.dirty() {
				test.Fatal("explicit read failed to reconcile actual order")
			}
			newConfirmation := configurationOrderPrepare(test, model)
			if newConfirmation == oldConfirmation {
				test.Fatal("explicit reread reused old approval")
			}
			state.Confirmation = oldConfirmation
			model.SubmitConfigurationOrder()
			if model.Busy || fixture.count("ReplaceAssetOrder") != mutations {
				test.Fatal("old confirmation accepted for new review")
			}
			state.Confirmation = newConfirmation
			model.SubmitConfigurationOrder()
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if state.Blocked || fixture.count("ReplaceAssetOrder") != mutations+1 {
				test.Fatal("fresh review after explicit read failed")
			}
		})
	}
}

func TestConfigurationOrderModelRevalidatesOwner(test *testing.T) {
	fixture := newConfigurationFixture(test)
	configurationOrderSeed(test, fixture, fixture.owner, "claude", "provider", "First")
	configurationOrderSeed(test, fixture, fixture.owner, "claude", "provider", "Second")
	model, queue := configurationOrderLoadModel(test, fixture)
	configurationOrderPrepare(test, model)
	model.session.set(fixture.foreignSession)
	model.SubmitConfigurationOrder()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if fixture.count("ReplaceAssetOrder") != 0 || model.order != nil {
		test.Fatal("session owner changed after review but still submitted")
	}
}

func TestConfigurationOrderModelRetainsEmptyPartitionRevision(test *testing.T) {
	fixture := newConfigurationFixture(test)
	asset := configurationOrderSeed(test, fixture, fixture.owner, "omp", "workspace", "Temporary")
	model, queue := configurationOrderLoadModel(test, fixture)
	if err := fixture.store.DeleteConfigurationAsset(test.Context(), fixture.owner, asset.ID, asset.Revision); err != nil {
		test.Fatal("remove temporary member")
	}
	model.LoadConfigurationOrder()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	state := model.orderState()
	if !state.Loaded || len(state.Partitions) != 1 || len(state.Draft) != 0 || state.Snapshot.Revision != 2 || state.Snapshot.AgentID != "omp" {
		test.Fatal("last-member removal lost empty partition or ABA revision")
	}
	model.PrepareConfigurationOrder()
	if state.Review != nil || fixture.count("ReplaceAssetOrder") != 0 {
		test.Fatal("empty unchanged order produced a mutation review")
	}
}

type configurationOrderHeldBackend struct {
	*Service
	started, release chan struct{}
}

func (backend *configurationOrderHeldBackend) ConfigurationOrder(ctx context.Context, identity configurationIdentity, partition configurationOrderPartition) (configurationOrderSnapshot, error) {
	snapshot, err := backend.Service.ConfigurationOrder(ctx, identity, partition)
	close(backend.started)
	<-backend.release
	return snapshot, err
}

func TestConfigurationOrderModelDrainAndGeneration(test *testing.T) {
	for _, scenario := range []string{"cancel", "origin", "owner", "logout", "close", "queued"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			configurationOrderSeed(test, fixture, fixture.owner, "claude", "prompt", "Held metadata")
			backend := &configurationOrderHeldBackend{Service: fixture.service, started: make(chan struct{}), release: make(chan struct{})}
			model, queue := configurationModelFixture(test, backend)
			releaseHeld := sync.OnceFunc(func() { close(backend.release) })
			test.Cleanup(releaseHeld)
			model.SetOrigin(fixture.server.URL)
			model.session.set(fixture.session)
			model.Page = 6
			model.LoadConfigurationOrder()
			<-backend.started
			if scenario == "queued" {
				releaseHeld()
				model.Wait()
				if len(queue) == 0 {
					test.Fatal("no real queued callback to fence")
				}
			}
			switch scenario {
			case "cancel":
				model.Cancel()
			case "origin", "queued":
				model.SetOrigin("https://other.invalid")
			case "owner":
				model.Cancel()
				model.clearOwner()
				model.session.set(fixture.foreignSession)
			case "logout":
				model.Logout()
			case "close":
				model.Close()
			}
			if scenario != "queued" {
				drained := make(chan struct{})
				go func() { model.Wait(); close(drained) }()
				select {
				case <-drained:
					test.Fatal("tracked worker reported drained before actual completion")
				default:
				}
				releaseHeld()
				<-drained
			}
			model.Wait()
			for len(queue) > 0 {
				(<-queue)()
			}
			if model.order != nil || model.pendingJobs.Load() != 0 || fixture.count("ReplaceAssetOrder") != 0 {
				test.Fatal("canceled or stale generation repopulated owner metadata or failed to drain")
			}
		})
	}
}

func TestConfigurationOrderModelRejectsChangedReview(test *testing.T) {
	for _, scenario := range []string{"generation", "permutation", "partition", "revision"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			configurationOrderSeed(test, fixture, fixture.owner, "pi", "agent-settings", "First")
			configurationOrderSeed(test, fixture, fixture.owner, "pi", "agent-settings", "Second")
			model, _ := configurationOrderLoadModel(test, fixture)
			configurationOrderPrepare(test, model)
			state := model.orderState()
			switch scenario {
			case "generation":
				model.generation++
			case "permutation":
				state.Draft[0], state.Draft[1] = state.Draft[1], state.Draft[0]
			case "partition":
				state.Snapshot.Kind = "workspace"
			case "revision":
				state.Snapshot.Revision++
			}
			model.SubmitConfigurationOrder()
			if !state.Blocked || state.Review != nil || fixture.count("ReplaceAssetOrder") != 0 {
				test.Fatal("changed review binding still authorized a mutation")
			}
		})
	}
}
