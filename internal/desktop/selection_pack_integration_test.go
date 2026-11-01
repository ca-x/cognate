package desktop

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/packattachments"
	"github.com/egoist/mygo/ui"
)

type selectionPackFixture struct {
	pack                          *packConfigurationFixture
	service                       *Service
	origin                        string
	dropImport, dropPack, holdGet atomic.Bool
	imports, replacements         atomic.Int32
	entered, canceled             chan struct{}
}

func newSelectionPackFixture(test *testing.T) *selectionPackFixture {
	test.Helper()
	upstream := newSelectionFixture(test)
	previous := http.DefaultTransport
	http.DefaultTransport = upstream.upstream
	test.Cleanup(func() { http.DefaultTransport = previous })
	fixture := &selectionPackFixture{pack: newPackConfigurationFixture(test), entered: make(chan struct{}, 1), canceled: make(chan struct{}, 1)}
	target, err := url.Parse(fixture.pack.origin)
	if err != nil {
		test.Fatal(err)
	}
	transport := fixture.pack.service.HTTP.Transport.(*http.Transport).Clone()
	test.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.ModifyResponse = func(response *http.Response) error {
		path := response.Request.URL.Path
		if response.StatusCode == 200 && (fixture.dropImport.Load() && strings.HasSuffix(path, "/CommitImportSelection") || fixture.dropPack.Load() && strings.HasSuffix(path, "/ReplaceAttachments")) {
			if err := response.Body.Close(); err != nil {
				return err
			}
			response.Body = io.NopCloser(bytes.NewReader([]byte{255}))
			response.ContentLength = 1
			response.Header.Set("Content-Length", "1")
			response.Header.Del("Content-Encoding")
		}
		return nil
	}
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/CommitImportSelection") {
			fixture.imports.Add(1)
		}
		if strings.HasSuffix(request.URL.Path, "/ReplaceAttachments") {
			fixture.replacements.Add(1)
		}
		if fixture.holdGet.Load() && strings.HasSuffix(request.URL.Path, "/GetAttachments") {
			_, _ = io.Copy(io.Discard, request.Body)
			_ = request.Body.Close()
			fixture.entered <- struct{}{}
			select {
			case <-request.Context().Done():
				fixture.canceled <- struct{}{}
			case <-test.Context().Done():
			}
			return
		}
		proxy.ServeHTTP(writer, request)
	}))
	test.Cleanup(endpoint.Close)
	fixture.origin, fixture.service = endpoint.URL, &Service{Home: test.TempDir(), HTTP: endpoint.Client()}
	return fixture
}

func selectionPackPump(test *testing.T, model *Model, queue chan func()) {
	test.Helper()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	model.Wait()
}

func selectionPackStage(test *testing.T, fixture *selectionPackFixture) (*Model, chan func()) {
	test.Helper()
	model, queue := configurationModelFixture(test, fixture.service)
	model.SetOrigin(fixture.origin)
	model.session.set(fixture.pack.session)
	model.Page = 1
	tester := ui.NewTester(model.View, 920, 760)
	if !tester.HasText("刷新订阅") || !tester.HasText("所有者配置附件") {
		test.Fatal("composition removed the independent subscription/owner entries")
	}
	model.LoadPackConfigurations()
	selectionPackPump(test, model, queue)
	packConfigurationAddProvider(test, model)
	packConfigurationPrepareModel(test, model, queue)
	connection, err := fixture.service.OpenSelection(test.Context(), fixture.origin, fixture.pack.session)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(connection.Close)
	state := model.selectionState()
	state.Scope = new(selectionScope)
	state.Scope.attach(connection)
	state.Path, state.Source, state.UploadApproved = selectionExport(test, 3), importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_NPX_SKILLS_LOCK, true
	preview, err := connection.Preview(test.Context(), state.Source, state.Path, "", nil)
	if err != nil {
		test.Fatal("actual shared-App import preview", err)
	}
	state.Owner = connection.Owner
	state.load(preview, state.Source, state.Path, nil)
	selectionApproveRepositories(test, model)
	if model.packConfigurationState().Review == nil || state.Review == nil {
		test.Fatal("both real prepared reviews must exist at the lifecycle seam")
	}
	return model, queue
}

func selectionPackClosed(test *testing.T, selection *selectionConnection, pack *packConfigurationConnection) {
	test.Helper()
	selection.private().mutex.Lock()
	closed := selection.private().closed
	selection.private().mutex.Unlock()
	identity := selection.private().identity.private()
	identity.mutex.Lock()
	session := identity.session
	identity.mutex.Unlock()
	pack.private().mutex.Lock()
	retired := pack.private().closed && pack.private().session == "" && pack.private().owner == "" && pack.private().operation == nil
	pack.private().mutex.Unlock()
	if !closed || session != "" || !retired {
		test.Fatal("shared lifecycle hid an old connection without physically closing/clearing it")
	}
}

func TestSelectionPackSharedLifecycle(test *testing.T) {
	for _, action := range []string{"cancel", "owner", "origin", "close"} {
		test.Run(action, func(test *testing.T) {
			fixture := newSelectionPackFixture(test)
			model, queue := selectionPackStage(test, fixture)
			selection, pack := model.selectionState(), model.packConfigurationState()
			selectionConnection, packConnection := selection.Scope.get(), pack.Connection
			test.Cleanup(packConnection.Close)
			operation := packConnection.private().operation
			switch action {
			case "cancel":
				model.Cancel()
			case "owner":
				model.Username = "pack-foreign"
				model.password.set("synthetic-foreign-password")
				model.Login()
				selectionPackPump(test, model, queue)
				identity, err := fixture.service.ConfigurationIdentity(test.Context(), fixture.origin, model.session.value())
				if err != nil {
					test.Fatal("real replacement-owner login failed", err)
				}
				defer identity.clear()
				if identity.OwnerID == fixture.pack.owner {
					test.Fatal("owner transition did not authenticate a different real owner")
				}
			case "origin":
				model.SetOrigin("http://127.0.0.1:1")
			case "close":
				model.Close()
			}
			model.Wait()
			for len(queue) > 0 {
				(<-queue)()
			}
			selectionPackClosed(test, selectionConnection, packConnection)
			if selection.Review != nil || selection.Scope != nil || pack.Review != nil || pack.Connection != nil || model.selectionLaunching {
				test.Fatal("old owner review remained actionable")
			}
			if _, err := operation.Review(); !errors.Is(err, packattachments.ErrClosed) || fixture.imports.Load() != 0 || fixture.replacements.Load() != 0 {
				test.Fatal("lifecycle transition retained a capability or replayed work")
			}
		})
	}
	test.Run("selection-unknown-then-pack", func(test *testing.T) {
		fixture := newSelectionPackFixture(test)
		model, queue := selectionPackStage(test, fixture)
		fixture.dropImport.Store(true)
		model.CommitSelection()
		selectionPackPump(test, model, queue)
		state := model.selectionState()
		id := state.OperationID
		if id == "" || len(model.selectionBarriers) != 1 || fixture.imports.Load() != 1 {
			test.Fatal("actual lost import receipt did not establish a single-write barrier")
		}
		model.LoadPackConfigurations()
		selectionPackPump(test, model, queue)
		packConfigurationAddProvider(test, model)
		packConfigurationPrepareModel(test, model, queue)
		pack := model.packConfigurationState()
		pack.Confirmation = pack.Review.Confirmation
		model.SubmitPackConfigurations()
		selectionPackPump(test, model, queue)
		model.CommitSelection()
		if len(model.selectionBarriers) != 1 || state.OperationID != id || fixture.imports.Load() != 1 || fixture.replacements.Load() != 1 || pack.Snapshot.PackRevision != 2 || pack.Snapshot.ConfigurationRevision != 2 {
			test.Fatal("Pack work replayed Unknown import or lost its barrier/dual CAS")
		}
		model.QuerySelectionStatus()
		selectionPackPump(test, model, queue)
		if state.Result.Observation != 4 || state.Result.Phase != 3 || len(model.selectionBarriers) != 0 || fixture.imports.Load() != 1 {
			test.Fatal("only explicit read-only terminal status may resolve the original import barrier", model.Status)
		}
	})
	test.Run("pack-unknown-then-selection", func(test *testing.T) {
		fixture := newSelectionPackFixture(test)
		model, queue := selectionPackStage(test, fixture)
		pack := model.packConfigurationState()
		draft := slices.Clone(pack.Draft)
		fixture.dropPack.Store(true)
		pack.Confirmation = pack.Review.Confirmation
		model.SubmitPackConfigurations()
		selectionPackPump(test, model, queue)
		if !pack.Blocked || !strings.Contains(model.Status, "Unknown") {
			test.Fatal("actual Pack receipt loss was not Unknown", model.Status)
		}
		state := model.selectionState()
		state.UploadApproved = true
		model.PreviewSelection()
		selectionPackPump(test, model, queue)
		if state.Preview == nil || !pack.Blocked || !slices.Equal(pack.Draft, draft) {
			test.Fatal("explicit import launch lost its preview or reauthorized Unknown Pack")
		}
		selectionApproveRepositories(test, model)
		model.CommitSelection()
		selectionPackPump(test, model, queue)
		model.SubmitPackConfigurations()
		model.PreparePackConfigurations()
		if model.Busy || !pack.Blocked || fixture.replacements.Load() != 1 || fixture.imports.Load() != 1 || state.Result.Phase != 3 {
			test.Fatal("import work replayed the old Unknown attachment replacement")
		}
		model.LoadPackConfigurations()
		selectionPackPump(test, model, queue)
		if !pack.Blocked || pack.Snapshot.PackRevision != 2 || pack.Snapshot.ConfigurationRevision != 2 || fixture.replacements.Load() != 1 {
			test.Fatal("explicit readonly Pack recovery silently reauthorized or replayed")
		}
	})
	test.Run("launch-cancels-old-pack-work", func(test *testing.T) {
		fixture := newSelectionPackFixture(test)
		model, queue := selectionPackStage(test, fixture)
		model.packConfigurationClearReview()
		fixture.holdGet.Store(true)
		model.LoadPackConfigurations()
		old := model.packConfigurationState().Connection
		test.Cleanup(old.Close)
		select {
		case <-fixture.entered:
		case <-time.After(5 * time.Second):
			test.Fatal("old Pack HTTP work did not enter")
		}
		connection, err := fixture.service.OpenSelection(test.Context(), fixture.origin, fixture.pack.session)
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(connection.Close)
		state := model.selectionState()
		state.Scope = new(selectionScope)
		state.Scope.attach(connection)
		model.selectionJob("explicit cross-editor launch", func(ctx context.Context) (func(), error) {
			preview, err := connection.Preview(ctx, state.Source, state.Path, "", nil)
			return func() { state.Preview = preview }, err
		})
		selectionPackPump(test, model, queue)
		select {
		case <-fixture.canceled:
		case <-time.After(5 * time.Second):
			test.Fatal("selectionLaunching let old Pack HTTP work continue")
		}
		old.private().mutex.Lock()
		retired := old.private().closed && old.private().session == ""
		old.private().mutex.Unlock()
		if !retired || state.Preview == nil || state.Scope.get() != connection || model.selectionLaunching {
			test.Fatal("launch guard protected the wrong editor or escaped its scheduling scope")
		}
		model.Close()
		model.Wait()
		selectionPackClosed(test, connection, old)
	})
}
