package desktop

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/grokclient"
	"github.com/ca-x/cognate/internal/server/httpapi"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"github.com/egoist/mygo/ui"
)

func TestGrokNativeEditorFormContract(test *testing.T) {
	form := newGrokForm(grokclient.EditView{Draft: grokclient.Draft{Name: strings.Repeat("界", 42), Mode: grokclient.CustomModel, Model: &grokclient.Model{Profile: "local", UpstreamModel: "synthetic", Endpoint: "https://synthetic.invalid/v1", Name: "model", APIBackend: "responses", ContextWindow: 9007199254740991}}})
	draft, err := form.draft()
	if err != nil || draft.Model.ContextWindow != 9007199254740991 || draft.Model.EnvKey != nil {
		test.Fatal("integer precision or absent env lost", err)
	}
	form.ContextWindow = "9007199254740992"
	if _, err := form.draft(); !errors.Is(err, grokclient.ErrInvalid) {
		test.Fatal("unsafe context accepted", err)
	}
	form.ContextWindow = "9007199254740991"
	form.Name = strings.Repeat("界", 43)
	if _, err := form.draft(); !errors.Is(err, grokclient.ErrInvalid) {
		test.Fatal("128-byte name limit lost", err)
	}
	form.Secret.set("synthetic-private-key")
	form.setMode(grokclient.NativeLogin)
	if form.Secret.value() != "" {
		test.Fatal("mode retained secret or authorization")
	}
}

func grokEditorFixture(test *testing.T, fixture *configurationFixture) (*Model, chan func()) {
	test.Helper()
	model, queue := configurationModelFixture(test, fixture.service)
	model.SetOrigin(fixture.server.URL)
	model.session.set(fixture.session)
	model.Page = 5
	model.LoadGrokAssets()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if model.grokState().session() == nil {
		test.Fatal("Grok session not opened", model.Status)
	}
	return model, queue
}

func grokCustomForm() *grokForm {
	return newGrokForm(grokclient.EditView{Draft: grokclient.Draft{Name: "Seven fields", Mode: grokclient.CustomModel, Model: &grokclient.Model{Profile: "local", UpstreamModel: "synthetic-model", Endpoint: "https://synthetic.invalid/v1", Name: "Synthetic model", APIBackend: "responses", ContextWindow: 9007199254740991}}})
}

func TestGrokNativeEditorSecretAuthorizationAndSevenFields(test *testing.T) {
	fixture := newConfigurationFixture(test)
	model, queue := grokEditorFixture(test, fixture)
	state := model.grokState()
	state.Form = grokCustomForm()
	state.Form.Action = grokclient.ReplaceKey
	state.Form.Secret.set("synthetic-editor-key")
	model.PrepareGrok(grokclient.Create)
	if state.Form.Secret.value() != "" {
		test.Fatal("prepare retained widget secret")
	}
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Review == nil || !strings.Contains(state.Review.Summary, "contextWindow=9007199254740991 envKey=<absent>") || strings.Contains(state.Review.Summary, "synthetic-editor-key") {
		test.Fatal("incomplete or unsafe review", model.Status)
	}
	state.Confirmation = state.Review.Confirmation
	model.SubmitGrok()
	if model.Busy || fixture.count("CreateAsset") != 0 {
		test.Fatal("Replace bypassed independent authorization")
	}
	state.KeyAuthorized = true
	model.SubmitGrok()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if fixture.count("CreateAsset") != 1 || !state.Blocked {
		test.Fatal("create not once or retry remained available", model.Status)
	}
	model.SubmitGrok()
	if fixture.count("CreateAsset") != 1 {
		test.Fatal("submission replayed")
	}
	model.LoadGrokAssets()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	state = model.grokState()
	if len(state.Assets) != 1 {
		test.Fatal("create receipt did not match actual server")
	}
	model.grokAction(state.Assets[0].ID)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	form := state.Form
	if !form.KeyPresent || form.ContextWindow != "9007199254740991" || form.EnvPresent || form.APIBackend != "responses" || form.Profile != "local" || form.UpstreamModel != "synthetic-model" || form.Endpoint != "https://synthetic.invalid/v1" || form.ModelName != "Synthetic model" {
		test.Fatal("seven fields roundtrip failed")
	}
	form.setMode(grokclient.NativeLogin)
	model.PrepareGrok(grokclient.Update)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Review != nil || fixture.count("UpdateAsset") != 0 || form.Action != grokclient.KeepKey {
		test.Fatal("native switch silently cleared existing credential")
	}
	form.Action = grokclient.ClearKey
	model.PrepareGrok(grokclient.Update)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Review == nil {
		test.Fatal("native clear review missing", model.Status)
	}
	state.Confirmation = state.Review.Confirmation
	model.SubmitGrok()
	if model.Busy || fixture.count("UpdateAsset") != 0 || state.KeyAuthorized {
		test.Fatal("Clear reused Replace approval")
	}
	state.KeyAuthorized = true
	model.SubmitGrok()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	view, err := state.session().Editable(test.Context(), form.Metadata.ID)
	if err != nil || view.KeyPresent || view.Draft.Mode != grokclient.NativeLogin {
		test.Fatal("native explicit clear failed", err, model.Status)
	}
}

func TestGrokNativeEditorPasswordPixelsAndEscape(test *testing.T) {
	fixture := newConfigurationFixture(test)
	model, queue := grokEditorFixture(test, fixture)
	state := model.grokState()
	state.Form = grokCustomForm()
	state.Form.Action = grokclient.ReplaceKey
	state.Form.Secret.set(strings.Repeat("M", 32))
	tester := ui.NewTester(model.View, 920, 760)
	configurationClick(test, tester, "Grok 新密钥（仅内存）")
	before := bytes.Clone(tester.Image().Pix)
	state.Form.Secret.set(strings.Repeat("Z", 32))
	tester.Frame()
	if !bytes.Equal(before, tester.Image().Pix) {
		test.Fatal("native password component exposed key-dependent pixels")
	}
	model.PrepareGrok(grokclient.Create)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	tester.Frame()
	if state.Review == nil || !tester.HasText("完整 Grok 操作审阅 · 不含密钥值") {
		test.Fatal("real native review modal missing", model.Status)
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Review != nil || state.KeyAuthorized || state.Form.Secret.value() != "" || fixture.count("CreateAsset") != 0 {
		test.Fatal("Escape retained or submitted approval")
	}
	if _, err := state.session().Submit(test.Context(), "cancelled"); !errors.Is(err, grokclient.ErrClosed) {
		test.Fatal("cancelled operation retained secret", err)
	}
}

func TestGrokNativeEditorExactNativeApproval(test *testing.T) {
	fixture := newConfigurationFixture(test)
	model, queue := grokEditorFixture(test, fixture)
	state := model.grokState()
	state.Form = grokCustomForm()
	state.Form.Action = grokclient.ReplaceKey
	state.Form.Secret.set("synthetic-real-native-approval")
	model.PrepareGrok(grokclient.Create)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Review == nil {
		test.Fatal("approval not prepared", model.Status)
	}
	tester := ui.NewTester(model.View, 920, 760)
	configurationClick(test, tester, "本次 exact 确认")
	tester.Type(state.Review.Confirmation)
	configurationClick(test, tester, "最终确认并提交一次")
	if model.Busy || fixture.count("CreateAsset") != 0 {
		test.Fatal("typed final confirmation bypassed independent Replace checkbox")
	}
	configurationClick(test, tester, "仅授权本次 Replace grokbuild.api-key")
	if !state.KeyAuthorized || fixture.count("CreateAsset") != 0 {
		test.Fatal("checkbox was not independent of submission")
	}
	configurationClick(test, tester, "最终确认并提交一次")
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if fixture.count("CreateAsset") != 1 || state.Review != nil || !state.Blocked {
		test.Fatal("actual native final action did not commit exactly once", model.Status)
	}
	if strings.Contains(strings.Join(tester.Texts(), "\n"), "synthetic-real-native-approval") {
		test.Fatal("native labels exposed replacement")
	}
}

func TestGrokNativeEditorIdentityLifecycle(test *testing.T) {
	for _, event := range []string{"cancel", "logout", "origin", "owner", "close", "leave"} {
		test.Run(event, func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			model, queue := grokEditorFixture(test, fixture)
			state := model.grokState()
			assets := state.session()
			state.Form = grokCustomForm()
			state.Form.Action = grokclient.ReplaceKey
			state.Form.Secret.set("synthetic-lifecycle-key")
			model.PrepareGrok(grokclient.Create)
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if state.Review == nil {
				test.Fatal("review missing")
			}
			switch event {
			case "cancel":
				model.Cancel()
			case "logout":
				model.Logout()
			case "origin":
				model.SetOrigin("https://other.synthetic.invalid")
			case "owner":
				model.clearOwner()
			case "close":
				model.Close()
			case "leave":
				model.configurationNavigate(3)
			}
			if state.Form.Secret.value() != "" || state.Review != nil {
				test.Fatal("lifecycle retained secret or review")
			}
			before := fixture.count("")
			if _, err := assets.List(test.Context()); !errors.Is(err, grokclient.ErrClosed) || fixture.count("") != before {
				test.Fatal("lifecycle left session alive", err)
			}
		})
	}
}

func TestGrokNativeEditorUnknownAndConflict(test *testing.T) {
	for _, outcome := range []string{"unknown", "conflict"} {
		test.Run(outcome, func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			source := grokDesktopSeed(test, fixture)
			model, queue := grokEditorFixture(test, fixture)
			state := model.grokState()
			model.grokAction(source.Id)
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			state.Form.Name = "Edited safely"
			state.Form.Dirty = true
			model.PrepareGrok(grokclient.Update)
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if state.Review == nil {
				test.Fatal("missing update review", model.Status)
			}
			if outcome == "unknown" {
				handler := httpapi.NewWithOptions(fixture.auth, httpapi.Options{Configuration: fixture.domain, PublicOrigin: fixture.server.URL})
				fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
					if !strings.HasSuffix(request.URL.Path, "/UpdateAsset") {
						return false
					}
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, request)
					if recorder.Code != http.StatusOK {
						test.Error("synthetic update failed before response loss")
					}
					connection, _, err := writer.(http.Hijacker).Hijack()
					if err == nil {
						_ = connection.Close()
					}
					return true
				})
			} else {
				peer := grokDesktopModel(test, fixture)
				view, err := peer.Editable(test.Context(), source.Id)
				if err != nil {
					test.Fatal(err)
				}
				view.Draft.Name = "Concurrent peer"
				review, err := peer.PrepareInput(test.Context(), grokclient.Change{Action: grokclient.Update, ID: source.Id, ExpectedRevision: 1}, view.Draft, grokclient.KeepKey, nil)
				if err != nil {
					test.Fatal(err)
				}
				if _, err := peer.Submit(test.Context(), review.Confirmation); err != nil {
					test.Fatal(err)
				}
			}
			state.Confirmation = state.Review.Confirmation
			model.SubmitGrok()
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if !state.Blocked || state.Review != nil || !strings.Contains(model.Status, map[string]string{"unknown": "Unknown", "conflict": "CAS"}[outcome]) {
				test.Fatal("outcome not safely blocked", model.Status)
			}
			if !state.Form.Dirty {
				test.Fatal("unacknowledged form write discarded dirty state", outcome)
			}
			assertGrokDirtyReloadProtected(test, model, state, fixture)
			count := fixture.count("UpdateAsset")
			model.SubmitGrok()
			model.PrepareGrok(grokclient.Update)
			if model.Busy || fixture.count("UpdateAsset") != count {
				test.Fatal("blocked mutation replayed")
			}
			view, err := state.session().Editable(test.Context(), source.Id)
			if err != nil || view.Metadata.Revision != 2 {
				test.Fatal("unable to reconcile actual version", err)
			}
			if outcome == "conflict" && view.Draft.Name != "Concurrent peer" {
				test.Fatal("CAS overwrote peer")
			}
		})
	}
}

func assertGrokDirtyReloadProtected(test *testing.T, model *Model, state *grokEditorState, fixture *configurationFixture) {
	test.Helper()
	form := state.Form
	before := fixture.count("")
	model.grokRequest("reload")
	if model.Busy || model.grokState() != state || state.Form != form || state.Pending != "reload" || fixture.count("") != before {
		test.Fatal("reload bypassed dirty-draft confirmation")
	}
	tester := ui.NewTester(model.View, 920, 760)
	if !tester.HasText("放弃 Grok 未保存草稿？新密钥已清空。") {
		test.Fatal("reload did not show native discard confirmation")
	}
	click(test, tester, "保留 Grok 草稿")
	if state.Pending != "" || state.Form != form || !form.Dirty || model.grokState() != state || fixture.count("") != before {
		test.Fatal("keeping draft during reload lost unsaved form")
	}
}

func TestGrokNativeEditorSortPreservesDirtyForm(test *testing.T) {
	for _, scenario := range []string{"unchanged", "changed", "conflict"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			source := grokDesktopSeed(test, fixture)
			grokDesktopSeed(test, fixture)
			model, queue := grokEditorFixture(test, fixture)
			state := model.grokState()
			model.grokAction(source.Id)
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			form := state.Form
			form.Name, form.Dirty = "Unsaved form beside order", true
			if scenario == "changed" {
				state.move(1, -1)
			}
			model.PrepareGrok(grokclient.ReplaceOrder)
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if state.Review == nil {
				test.Fatal("real order review missing", model.Status)
			}
			if scenario == "conflict" {
				peer := grokDesktopModel(test, fixture)
				order, err := peer.Order(test.Context())
				if err != nil {
					test.Fatal(err)
				}
				review, err := peer.Prepare(test.Context(), grokclient.Change{Action: grokclient.ReplaceOrder, ExpectedOrderRevision: order.Revision, OrderIDs: []string{order.Assets[1].ID, order.Assets[0].ID}})
				if err != nil {
					test.Fatal(err)
				}
				if _, err := peer.Submit(test.Context(), review.Confirmation); err != nil {
					test.Fatal(err)
				}
			}
			state.Confirmation = state.Review.Confirmation
			model.SubmitGrok()
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if scenario == "conflict" && !strings.Contains(model.Status, "CAS") {
				test.Fatal("concurrent sort did not produce actual CAS conflict", model.Status)
			}
			if scenario != "conflict" && !strings.Contains(model.Status, "已提交一次") {
				test.Fatal("order submission did not succeed", model.Status)
			}
			if state.Form != form || !form.Dirty || form.Name != "Unsaved form beside order" {
				test.Fatal("order submission marked unsaved form clean", scenario)
			}
			view, err := state.session().Editable(test.Context(), source.Id)
			if err != nil || view.Draft.Name == form.Name || view.Metadata.Revision != 1 {
				test.Fatal("order mutation wrote the unrelated form", err)
			}
			assertGrokDirtyReloadProtected(test, model, state, fixture)
		})
	}
}

func TestGrokNativeEditorSuccessfulFormSaveClearsDirty(test *testing.T) {
	for _, action := range []grokclient.Action{grokclient.Create, grokclient.Update} {
		test.Run(map[grokclient.Action]string{grokclient.Create: "create", grokclient.Update: "update"}[action], func(test *testing.T) {
			fixture := newConfigurationFixture(test)
			source := grokDesktopSeed(test, fixture)
			model, queue := grokEditorFixture(test, fixture)
			state := model.grokState()
			if action == grokclient.Create {
				model.grokAction("new")
			} else {
				model.grokAction(source.Id)
				configurationPump(test, model, queue, func() bool { return !model.Busy })
			}
			state.Form.Name, state.Form.Dirty = "Acknowledged form", true
			model.PrepareGrok(action)
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if state.Review == nil {
				test.Fatal("real form review missing", model.Status)
			}
			state.Confirmation = state.Review.Confirmation
			model.SubmitGrok()
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if state.Form.Dirty || !strings.Contains(model.Status, "已提交一次") {
				test.Fatal("acknowledged form remained dirty", model.Status)
			}
			assets, err := state.session().List(test.Context())
			if err != nil {
				test.Fatal(err)
			}
			for _, asset := range assets {
				if asset.Name == "Acknowledged form" {
					return
				}
			}
			test.Fatal("successful form was not saved on actual server")
		})
	}
}

func TestGrokNativeEditorCancelledContext(test *testing.T) {
	fixture := newConfigurationFixture(test)
	assets := grokDesktopModel(test, fixture)
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := assets.PrepareInput(ctx, grokclient.Change{Action: grokclient.Create}, grokclient.Draft{Name: "cancelled", Mode: grokclient.NativeLogin}, grokclient.NoKeyMutation, nil); !errors.Is(err, context.Canceled) {
		test.Fatal("cancelled editor entered prepare", err)
	}
	if fixture.count("CreateAsset") != 0 {
		test.Fatal("cancelled editor wrote")
	}
}

func TestGrokNativeEditorCopySortDeleteAndEnvKeep(test *testing.T) {
	fixture := newConfigurationFixture(test)
	source := grokDesktopSeed(test, fixture)
	model, queue := grokEditorFixture(test, fixture)
	state := model.grokState()
	model.grokAction(source.Id)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	state.Form.EnvPresent, state.Form.EnvKey = true, "SYNTHETIC_GROK_KEY"
	model.PrepareGrok(grokclient.Update)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Review == nil {
		test.Fatal("env/Keep preview failed", model.Status)
	}
	state.Confirmation = state.Review.Confirmation
	model.SubmitGrok()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	view, err := state.session().Editable(test.Context(), source.Id)
	if err != nil || view.Draft.Model.EnvKey == nil || *view.Draft.Model.EnvKey != "SYNTHETIC_GROK_KEY" || !view.KeyPresent {
		test.Fatal("env presence or Keep lost", err)
	}
	model.LoadGrokAssets()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	state = model.grokState()
	model.grokAction(source.Id)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	state.CopyName = "Independent copy"
	model.PrepareGrok(grokclient.Copy)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Review == nil {
		test.Fatal("copy preview failed", model.Status)
	}
	state.Confirmation = state.Review.Confirmation
	model.SubmitGrok()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	model.LoadGrokAssets()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	state = model.grokState()
	if len(state.Order.Assets) != 2 {
		test.Fatal("copy did not appear in actual order")
	}
	copyID := state.Order.Assets[1].ID
	copyView, err := state.session().Editable(test.Context(), copyID)
	if err != nil || copyView.KeyPresent || copyView.Draft.Model.EnvKey == nil {
		test.Fatal("copy lost nonsecret fields or copied secret", err)
	}
	state.move(1, -1)
	if !state.OrderDirty {
		test.Fatal("sort did not mark pending edit")
	}
	state.discardOrder()
	if state.Order.Assets[0].ID != source.Id || state.OrderDirty {
		test.Fatal("discard did not restore server snapshot")
	}
	state.move(1, -1)
	model.PrepareGrok(grokclient.ReplaceOrder)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Review == nil || !strings.Contains(state.Review.Summary, "BEFORE IDs=") || !strings.Contains(state.Review.Summary, "AFTER IDs=") {
		test.Fatal("full order preview absent", model.Status)
	}
	state.Confirmation = state.Review.Confirmation
	model.SubmitGrok()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	order, err := state.session().Order(test.Context())
	if err != nil || order.Assets[0].ID != copyID || fixture.count("ReplaceAssetOrder") != 1 {
		test.Fatal("sort not committed once", err)
	}
	model.LoadGrokAssets()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	state = model.grokState()
	model.grokAction(copyID)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	model.PrepareGrok(grokclient.Delete)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Review == nil {
		test.Fatal("delete preview absent", model.Status)
	}
	state.Confirmation = state.Review.Confirmation
	model.SubmitGrok()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if _, err := state.session().Editable(test.Context(), copyID); !errors.Is(err, grokclient.ErrNotFound) || fixture.count("DeleteAsset") != 1 {
		test.Fatal("delete not committed once", err)
	}
}

func TestGrokNativeEditorFutureFieldsReadonly(test *testing.T) {
	fixture := newConfigurationFixture(test)
	asset, err := fixture.store.CreateConfigurationAsset(test.Context(), fixture.owner, serverstate.ConfigurationAssetDraft{AgentID: "grokbuild", Kind: serverstate.ConfigurationKindProvider, Name: "Future native", SchemaVersion: 1, Payload: serverstate.NewConfigurationPayload([]byte(`{"version":1,"variant":"grokbuild-provider","provider":{"mode":"native-login"},"secrets":{},"future":{"secret":"synthetic-future-value"}}`))})
	if err != nil {
		test.Fatal(err)
	}
	model, queue := grokEditorFixture(test, fixture)
	state := model.grokState()
	model.grokAction(asset.ID)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Form == nil || !state.Form.ReadOnly || state.Form.Mode != grokclient.NativeLogin {
		test.Fatal("future fields not readonly", model.Status)
	}
	before := fixture.count("")
	for _, action := range []grokclient.Action{grokclient.Update, grokclient.Copy, grokclient.Delete} {
		model.PrepareGrok(action)
	}
	if model.Busy || state.Review != nil || fixture.count("") != before {
		test.Fatal("readonly editor reached transport")
	}
	tester := ui.NewTester(model.View, 1100, 900)
	if !tester.HasText("未知字段或版本：只读，不能保存、复制或删除；请使用支持该格式的客户端。") || tester.HasText("synthetic-future-value") {
		test.Fatal("readonly notice absent or future content leaked")
	}
}

func TestGrokNativeEditorCreateAndEdit(test *testing.T) {
	fixture := newConfigurationFixture(test)
	assets := grokDesktopModel(test, fixture)
	draft := grokclient.Draft{Name: "Native editor", Mode: grokclient.NativeLogin}
	review, err := assets.PrepareInput(test.Context(), grokclient.Change{Action: grokclient.Create}, draft, grokclient.NoKeyMutation, nil)
	if err != nil || fixture.count("CreateAsset") != 0 {
		test.Fatal("create preview", err)
	}
	result, err := assets.Submit(test.Context(), review.Confirmation)
	if err != nil || result.Metadata == nil {
		test.Fatal("create", err)
	}
	editable, err := assets.Editable(test.Context(), result.Metadata.ID)
	if err != nil || editable.Draft.Mode != grokclient.NativeLogin || editable.ReadOnly {
		test.Fatal("editable DTO", err)
	}
	editable.Draft.Name = "Updated editor"
	review, err = assets.PrepareInput(test.Context(), grokclient.Change{Action: grokclient.Update, ID: result.Metadata.ID, ExpectedRevision: result.Metadata.Revision}, editable.Draft, grokclient.KeepKey, nil)
	if err != nil {
		test.Fatal("update preview", err)
	}
	if _, err := assets.Submit(test.Context(), review.Confirmation); err != nil {
		test.Fatal("update", err)
	}
}

func TestGrokNativeEditorEntryAndPrivacy(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
	tester := ui.NewTester(model.View, 1100, 900)
	click(test, tester, "Grok Build")
	if model.Page != 5 || !tester.HasText("Grok Build · 仅管理服务端资产") {
		test.Fatal("real entry missing")
	}
	state := model.grokState()
	state.Form = newGrokForm(grokclient.EditView{Draft: grokclient.Draft{Name: "private", Mode: grokclient.CustomModel}})
	state.Form.Secret.set("synthetic-private-key")
	state.Form.Dirty = true
	model.configurationNavigate(3)
	if state.Form.Secret.value() != "" {
		test.Fatal("leaving retained secret")
	}
	tester.Frame()
	if model.Page != 5 || !tester.HasText("放弃 Grok 未保存草稿？新密钥已清空。") {
		test.Fatal("dirty Grok navigation bypassed confirmation")
	}
	click(test, tester, "保留 Grok 草稿")
	if model.Page != 5 {
		test.Fatal("dismiss discarded Grok draft")
	}
	model.configurationNavigate(3)
	tester.Frame()
	click(test, tester, "放弃 Grok 草稿并继续")
	if model.Page != 3 || model.grok != nil {
		test.Fatal("explicit discard did not close editor")
	}
}
