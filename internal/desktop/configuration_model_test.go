package desktop

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/nativeconfig"
)

func configurationModelFixture(test *testing.T, backend Backend) (*Model, chan func()) {
	test.Helper()
	queue := make(chan func(), 128)
	model := NewModel(backend, func(update func()) { queue <- update })
	test.Cleanup(func() {
		model.Close()
		model.Wait()
		for len(queue) > 0 {
			(<-queue)()
		}
	})
	return model, queue
}

func configurationPump(test *testing.T, model *Model, queue <-chan func(), ready func() bool) {
	test.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !ready() {
		select {
		case update := <-queue:
			update()
		case <-timer.C:
			test.Fatalf("configuration event completion missing; busy=%t pending=%d", model.Busy, model.pendingJobs.Load())
		}
	}
}

func configurationLoadModel(test *testing.T, fixture *configurationFixture) (*Model, chan func()) {
	test.Helper()
	model, queue := configurationModelFixture(test, fixture.service)
	model.SetOrigin(fixture.server.URL)
	model.session.set(fixture.session)
	model.Page = 4
	model.LoadConfigurations()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if model.configurationState().Identity.OwnerID != fixture.owner {
		test.Fatal("model did not load actual authenticated owner")
	}
	return model, queue
}

func TestNativeConfigurationModelRemoteOnly(test *testing.T) {
	fixture := newConfigurationFixture(test)
	model, queue := configurationLoadModel(test, fixture)
	model.NewConfiguration()
	state := model.configurationState()
	state.Form.Name = "Model real asset"
	state.Form.Mode = agentconfig.ClaudeAPIKeyAuth
	state.Form.Actions = [2]configurationSecretAction{configurationReplace, configurationClear}
	state.Form.secret()[0] = "synthetic-model-replacement"
	state.Form.Dirty = true
	model.SaveConfiguration()
	if state.Form.secret()[0] != "" {
		test.Fatal("save retained UI replacement")
	}
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if state.Form.Dirty || state.Form.Asset.Revision != 1 || state.Form.Asset.ID == "" || fixture.count("BeginNativeConsumer") != 0 {
		test.Fatal("save did not remain remote-only")
	}
	if _, err := os.Stat(filepath.Join(fixture.service.Home, ".config", "skillhub", "client.key")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("remote edit initialized key")
	}
	model.CopyConfiguration()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if !state.Form.NeedsCredential || len(state.Assets) != 2 {
		test.Fatal("copy retained secret or lost asset")
	}
	state.Form.Dirty = true
	model.configurationNavigate(3)
	if model.Page != 4 || state.PendingPage != 3 {
		test.Fatal("dirty navigation silently discarded draft")
	}
	state.Form.Dirty = false
	state.ConfirmDelete = true
	model.DeleteConfiguration()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if len(state.Assets) != 1 || state.Form != nil {
		test.Fatal("real CAS delete did not update safe state")
	}
}

func configurationApplyModel(test *testing.T, mode agentconfig.ClaudeAuthMode) (*configurationFixture, *Model, chan func()) {
	test.Helper()
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	secrets := [2]configurationSecretEdit{{Action: configurationClear}, {Action: configurationClear}}
	if mode == agentconfig.ClaudeAPIKeyAuth {
		secrets[0] = configurationSecretEdit{Action: configurationReplace, Replacement: []byte("synthetic-model-apply-secret")}
	}
	draft, err := fixture.service.SaveConfiguration(test.Context(), identity, newConfigurationEdit(configurationAsset{}, "Model apply", agentconfig.ClaudeProviderDesired{AuthMode: mode}, secrets))
	if err != nil {
		test.Fatal("model apply asset fixture")
	}
	if err := clientkey.Initialize(test.Context(), fixture.service.Home); err != nil {
		test.Fatal("model synthetic key initialization")
	}
	if err := os.Mkdir(filepath.Join(fixture.service.Home, ".claude"), 0700); err != nil {
		test.Fatal(err)
	}
	model, queue := configurationLoadModel(test, fixture)
	model.SelectConfiguration(draft.Asset.ID)
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	return fixture, model, queue
}

func TestNativeConfigurationExactApproval(test *testing.T) {
	for _, mode := range []agentconfig.ClaudeAuthMode{agentconfig.ClaudeAPIKeyAuth, agentconfig.ClaudeNativeLogin} {
		test.Run(string(mode), func(test *testing.T) {
			fixture, model, queue := configurationApplyModel(test, mode)
			model.PreviewConfiguration()
			configurationPump(test, model, queue, func() bool { return !model.Busy || model.configurationState().Prompt != nil })
			if model.configurationState().Prompt != nil {
				test.Fatal("preview requested final approval instead of remaining preview")
			}
			previewID := model.configurationState().OperationID
			if previewID == "" || fixture.count("AuthorizeNativeMaterialization") != 0 {
				test.Fatal("false preview authorized materialization")
			}
			model.ApplyConfiguration()
			configurationPump(test, model, queue, func() bool { return model.configurationState().Prompt != nil || !model.Busy })
			state := model.configurationState()
			prompt := state.Prompt
			if prompt == nil || prompt.Plan.OperationID == previewID || fixture.count("AuthorizeNativeMaterialization") != 0 {
				test.Fatal("new true apply reused false approval or skipped final modal")
			}
			for _, checked := range prompt.Checked {
				if checked {
					test.Fatal("secret slot defaulted checked")
				}
			}
			if len(prompt.Slots) > 0 {
				model.ReplyConfigurationApproval(true)
				if state.Prompt == nil || fixture.count("ConsumeNativeMaterialization") != 0 {
					test.Fatal("missing slot approval was accepted")
				}
			}
			for index := range prompt.Checked {
				prompt.Checked[index] = true
			}
			model.ReplyConfigurationApproval(true)
			configurationPump(test, model, queue, func() bool { return !model.Busy })
			if state.Outcome.Local.Status != "AppliedOnDisk" || strings.Contains(model.Status, "未完成") {
				test.Fatalf("confirmed model apply did not complete: %s", model.Status)
			}
			if fixture.count("ConsumeNativeMaterialization") > 1 {
				test.Fatal("model replayed materialization")
			}
			if data, err := os.ReadFile(filepath.Join(fixture.service.Home, ".claude", "settings.json")); err != nil || mode == agentconfig.ClaudeAPIKeyAuth && !bytes.Contains(data, []byte("synthetic-model-apply-secret")) {
				test.Fatal("model did not publish real target")
			}
		})
	}
}

func TestNativeConfigurationApprovalCancellation(test *testing.T) {
	for _, scenario := range []string{"cancel", "origin", "logout", "close", "profile", "asset", "deny"} {
		test.Run(scenario, func(test *testing.T) {
			fixture, model, queue := configurationApplyModel(test, agentconfig.ClaudeAPIKeyAuth)
			model.ApplyConfiguration()
			configurationPump(test, model, queue, func() bool { return model.configurationState().Prompt != nil || !model.Busy })
			if model.configurationState().Prompt == nil {
				test.Fatal("no real in-flight approval")
			}
			switch scenario {
			case "cancel":
				model.Cancel()
			case "origin":
				model.SetOrigin("https://other.example")
			case "logout":
				model.Logout()
			case "close":
				model.Close()
			case "profile":
				model.configurationState().Profile = "changed"
				model.cancelJob()
			case "asset":
				model.SelectConfiguration(model.configurationState().Selected)
			case "deny":
				model.ReplyConfigurationApproval(false)
			}
			model.Wait()
			for len(queue) > 0 {
				(<-queue)()
			}
			if fixture.count("AuthorizeNativeMaterialization") != 0 || fixture.count("ConsumeNativeMaterialization") != 0 || model.pendingJobs.Load() != 0 {
				test.Fatal("canceled approval authorized or failed to drain")
			}
			if _, err := os.Stat(filepath.Join(fixture.service.Home, ".claude", "settings.json")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("canceled approval wrote target")
			}
		})
	}
}

type configurationWitness struct {
	*Service
	Started, Release chan struct{}
	Outcome          configurationOutcome
	Failure          error
}

func (backend *configurationWitness) ApplyConfiguration(ctx context.Context, _ configurationIdentity, _ configurationAsset, _ string, _ bool, _ nativeconfig.Interaction) (configurationOutcome, error) {
	close(backend.Started)
	<-backend.Release
	return backend.Outcome, backend.Failure
}

func TestNativeConfigurationActualDrainAndQueuedGeneration(test *testing.T) {
	for _, scenario := range []string{"cancel", "origin", "close", "queued"} {
		test.Run(scenario, func(test *testing.T) {
			backend := &configurationWitness{Service: &Service{}, Started: make(chan struct{}), Release: make(chan struct{}), Outcome: configurationOutcome{Local: configapply.SafeResult{OperationID: "old-operation", Status: "AppliedOnDisk"}}}
			model, queue := configurationModelFixture(test, backend)
			test.Cleanup(func() {
				select {
				case <-backend.Release:
				default:
					close(backend.Release)
				}
			})
			state := model.configurationState()
			state.Identity = configurationIdentity{Origin: "https://synthetic.example", OwnerID: "owner", private: func() *configurationIdentityData { return &configurationIdentityData{session: "synthetic"} }}
			state.Form = &configurationForm{Asset: configurationAsset{ID: "asset", Revision: 1, Editable: true}}
			model.ApplyConfiguration()
			<-backend.Started
			if scenario == "queued" {
				close(backend.Release)
				model.Wait()
			}
			switch scenario {
			case "origin":
				model.SetOrigin("https://changed.example")
			case "close":
				model.Close()
			default:
				model.cancelJob()
			}
			if scenario != "queued" {
				if !model.localWorkBlocked() {
					test.Fatal("cancel released eligibility before actual return/cleanup")
				}
				model.ApplyConfiguration()
				close(backend.Release)
			}
			model.Wait()
			for len(queue) > 0 {
				(<-queue)()
			}
			if model.localWorkBlocked() || strings.Contains(model.Status, "已应用到磁盘") || model.configurationState().Outcome.Local.OperationID == "old-operation" {
				test.Fatal("old completion repopulated UI or worker did not drain")
			}
		})
	}
}

func TestNativeConfigurationUnknownAndClose(test *testing.T) {
	for _, failure := range []error{configclient.ErrNativeUnknown, configapply.ErrOutcomeUnknown, errors.New("synthetic-close-error-with-secret")} {
		backend := &configurationWitness{Service: &Service{}, Started: make(chan struct{}), Release: make(chan struct{}), Outcome: configurationOutcome{Local: configapply.SafeResult{OperationID: "uncertain-operation", Status: "AppliedOnDisk"}}, Failure: failure}
		model, queue := configurationModelFixture(test, backend)
		state := model.configurationState()
		state.Identity = configurationIdentity{Origin: "https://synthetic.example", OwnerID: "owner", private: func() *configurationIdentityData { return &configurationIdentityData{} }}
		state.Form = &configurationForm{Asset: configurationAsset{ID: "asset", Revision: 1, Editable: true}}
		model.ApplyConfiguration()
		<-backend.Started
		close(backend.Release)
		configurationPump(test, model, queue, func() bool { return !model.Busy })
		if state.OperationID != "uncertain-operation" || strings.Contains(model.Status, "操作完成") || strings.Contains(model.Status, "已应用到磁盘") || strings.Contains(configurationOutcomeMessage(state.History[0], nil), "已应用到磁盘") || strings.Contains(model.Status, "synthetic") {
			test.Fatal("unsafe completion swallowed unknown/close failure")
		}
	}
}

func TestNativeConfigurationQueuedDeadline(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		queue := make(chan func(), 8)
		model := NewModel(&Service{}, func(update func()) { queue <- update })
		defer model.Close()
		model.jobWithTimeout("deadline witness", 20*time.Second, func(context.Context) (func(), error) { return func() { model.Status = "stale-deadline-result" }, nil })
		model.Wait()
		time.Sleep(21 * time.Second)
		for len(queue) > 0 {
			(<-queue)()
		}
		if model.Status == "stale-deadline-result" {
			test.Fatal("queued result crossed completed job deadline")
		}
	})
}

type configurationNoApply struct {
	*Service
	Calls int
}

func (backend *configurationNoApply) ApplyConfiguration(context.Context, configurationIdentity, configurationAsset, string, bool, nativeconfig.Interaction) (configurationOutcome, error) {
	backend.Calls++
	return configurationOutcome{}, errConfigurationUnavailable
}

func TestNativeConfigurationSaveNeverDelegatesApply(test *testing.T) {
	fixture := newConfigurationFixture(test)
	backend := &configurationNoApply{Service: fixture.service}
	model, queue := configurationModelFixture(test, backend)
	state := model.configurationState()
	state.Identity = configurationTestIdentity(test, fixture)
	state.Form = &configurationForm{Name: "Save delegation oracle", Mode: agentconfig.ClaudeNativeLogin, Asset: configurationAsset{AgentID: "claude", Kind: "provider", SchemaVersion: 1, Editable: true}, Actions: [2]configurationSecretAction{configurationClear, configurationClear}}
	model.SaveConfiguration()
	configurationPump(test, model, queue, func() bool { return !model.Busy })
	if backend.Calls != 0 || fixture.count("CreateAsset") != 1 {
		test.Fatal("saving delegated local apply instead of remaining real remote CRUD")
	}
}

func TestNativeConfigurationUnknownSticky(test *testing.T) {
	model, _ := configurationModelFixture(test, &Service{})
	model.configurationRemember(configurationOutcome{Plan: configapply.SafePlan{OperationID: "sticky-id"}, RemoteUnknown: true, LocalUnknown: true, RemoteState: configclient.NativeConsumed})
	model.configurationRemember(configurationOutcome{Local: configapply.SafeResult{OperationID: "sticky-id", Status: "Planned"}})
	outcome := model.configurationState().Outcome
	if !outcome.RemoteUnknown || !outcome.LocalUnknown || outcome.RemoteState != configclient.NativeConsumed || strings.Contains(configurationOutcomeMessage(outcome, nil), "完成") {
		test.Fatal("status erased prior uncertainty")
	}
}

func TestNativeConfigurationEditorPrivacy(test *testing.T) {
	form := configurationForm{}
	form.secret()[0] = "synthetic-editor-secret"
	model, _ := configurationModelFixture(test, &Service{})
	model.configurationState().Form = &form
	for _, value := range []any{form, &form, []any{form}, map[string]any{"editor": form}, model, []*Model{model}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%t", "%d", "%x"} {
			if strings.Contains(fmt.Sprintf(verb, value), "synthetic-editor-secret") {
				test.Fatal("editor debug formatting disclosed replacement")
			}
		}
	}
	model.password.set("synthetic-editor-password")
	model.session.set("synthetic-editor-session")
	model.shareToken.set("synthetic-editor-share")
	secrets := []string{"synthetic-editor-secret", "synthetic-editor-password", "synthetic-editor-session", "synthetic-editor-share"}
	var output bytes.Buffer
	oldWriter, oldLogger := log.Writer(), slog.Default()
	log.SetOutput(&output)
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	defer func() { slog.SetDefault(oldLogger); log.SetOutput(oldWriter) }()
	for index, value := range []any{
		form, &form, model, struct{ private *Model }{model}, struct{ private configurationForm }{form},
		[]any{model}, map[string]any{"model": model},
		model.password, &model.session, struct{ private modelCredential }{model.shareToken},
		struct{ private []modelCredential }{[]modelCredential{model.password, model.shareToken, model.session}},
	} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%b", "%o", "%O", "%c", "%U", "%e", "%E", "%f", "%F", "%g", "%G", "%t", "%p", "%T", "%w"} {
			text := fmt.Sprintf(verb, value)
			for secretIndex, secret := range secrets {
				if strings.Contains(text, secret) {
					test.Errorf("private fmt leak: container=%d verb=%s secretClass=%d", index, verb, secretIndex)
				}
			}
			fmt.Fprintln(&output, text)
		}
		encoded, err := json.Marshal(value)
		if err != nil && index < 3 {
			test.Fatal("privacy JSON could not encode safe view")
		}
		output.Write(encoded)
		log.Printf("%+v", value)
		slog.Info("privacy witness", "value", value)
		slog.New(slog.NewJSONHandler(&output, nil)).Info("privacy witness", "value", value)
	}
	for _, secret := range secrets {
		if strings.Contains(output.String(), secret) {
			test.Fatal("private nested editor/model reached fmt, JSON or real logger")
		}
	}
}

func TestNativeConfigurationCredentialLifetime(test *testing.T) {
	for _, action := range []string{"cancel", "origin", "logout", "close"} {
		test.Run(action, func(test *testing.T) {
			model, _ := configurationModelFixture(test, &Service{Home: test.TempDir()})
			password, shareToken, session := model.password.input(), model.shareToken.input(), model.session.input()
			if password != model.password.input() || shareToken != model.shareToken.input() || session != model.session.input() {
				test.Fatal("native input storage changed across frames")
			}
			*password, *shareToken, *session = "synthetic-password", "synthetic-share", "synthetic-session"
			if model.password.value() != *password || model.shareToken.value() != *shareToken || model.session.value() != *session {
				test.Fatal("opaque input does not feed the credential used by the model")
			}
			switch action {
			case "cancel":
				model.Cancel()
			case "origin":
				model.SetOrigin("https://synthetic.example")
			case "logout":
				model.Logout()
			case "close":
				model.Close()
			}
			if *password != "" || *shareToken != "" || *session != "" || model.password.value() != "" || model.shareToken.value() != "" || model.session.value() != "" {
				test.Fatal("lifecycle did not clear actual opaque credential cells")
			}
		})
	}
}

func TestNativeConfigurationApprovalExpiry(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		queue := make(chan func(), 8)
		model := NewModel(&Service{}, func(update func()) { queue <- update })
		defer model.Close()
		state := model.configurationState()
		plan := configapply.SafePlan{OperationID: "expiry-operation", Kind: "restore", Agent: "claude", Target: agentconfig.ClaudeSettings, ExpiresAt: time.Now().Add(time.Second)}
		interaction := model.configurationInteraction(0, configurationIdentity{}, configurationAsset{}, state.Profile)
		result := make(chan error, 1)
		go func() { _, err := interaction.Approve(test.Context(), plan, nil); result <- err }()
		(<-queue)()
		if state.Prompt == nil {
			test.Fatal("expiry witness did not show final approval")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
			test.Fatal("expired approval did not release actual waiter")
		}
		model.ReplyConfigurationApproval(true)
		if state.Prompt != nil {
			test.Fatal("expired modal retained reusable approval")
		}
	})
}
