package desktop

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/grokclient"
	"github.com/ca-x/cognate/internal/server/httpapi"
	serverstate "github.com/ca-x/cognate/internal/server/state"
)

func grokDesktopSeed(test *testing.T, fixture *configurationFixture) *configurationv1.Asset {
	test.Helper()
	request := connect.NewRequest(&configurationv1.CreateAssetRequest{
		Draft: &configurationv1.Draft{Name: "Synthetic Grok", Payload: &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: &configurationv1.GrokBuildProvider{
			Mode:  configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL,
			Model: &configurationv1.GrokBuildModel{Profile: "local", UpstreamModel: "synthetic-model", Endpoint: "https://synthetic.invalid/v1", Name: "Synthetic provider", ApiBackend: "responses", ContextWindow: 9007199254740991},
		}}},
		Secrets: []*configurationv1.SecretMutation{{SlotId: "grokbuild.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-grok-desktop-secret"}}},
	})
	request.Header().Set("Authorization", "Bearer "+fixture.session)
	client := configurationv1connect.NewConfigurationServiceClient(fixture.server.Client(), fixture.server.URL)
	response, err := client.CreateAsset(test.Context(), request)
	if err != nil {
		test.Fatal(err)
	}
	return response.Msg.View.Asset
}

func grokDesktopModel(test *testing.T, fixture *configurationFixture) *grokAssetsModel {
	test.Helper()
	model, err := fixture.service.openGrokAssets(test.Context(), configurationTestIdentity(test, fixture))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(model.Close)
	return model
}

func TestGrokDesktopReadCopyOrderDelete(test *testing.T) {
	fixture := newConfigurationFixture(test)
	source := grokDesktopSeed(test, fixture)
	model := grokDesktopModel(test, fixture)
	assets, err := model.List(test.Context())
	if err != nil || len(assets) != 1 || assets[0].ID != source.Id {
		test.Fatal("authenticated Grok list", err)
	}
	summary, err := model.Read(test.Context(), source.Id)
	if err != nil || !strings.Contains(summary, "contextWindow=9007199254740991 envKey=<absent>") || !strings.Contains(summary, "key-present=true") || strings.Contains(summary, "synthetic-grok-desktop-secret") {
		test.Fatal("safe seven-field summary", err)
	}
	review, err := model.Prepare(test.Context(), grokclient.Change{Action: grokclient.Copy, ID: source.Id, ExpectedRevision: 1, Name: "Without credentials"})
	if err != nil || !strings.Contains(review.Summary, "key-present=false") || fixture.count("CopyAsset") != 0 {
		test.Fatal("prepare must be read-only", err)
	}
	if _, err := model.Submit(test.Context(), "wrong approval"); !errors.Is(err, grokclient.ErrInvalid) || fixture.count("CopyAsset") != 0 {
		test.Fatal("inexact confirmation published", err)
	}
	result, err := model.Submit(test.Context(), review.Confirmation)
	if err != nil || result.Metadata == nil || result.Metadata.ID == source.Id {
		test.Fatal("copy", err)
	}
	copyID := result.Metadata.ID
	if _, err := model.Submit(test.Context(), review.Confirmation); !errors.Is(err, grokclient.ErrClosed) || fixture.count("CopyAsset") != 1 {
		test.Fatal("approval replay", err)
	}
	summary, err = model.Read(test.Context(), copyID)
	if err != nil || !strings.Contains(summary, "key-present=false") || !strings.Contains(summary, "needs-credential") {
		test.Fatal("copy retained credential", err)
	}
	order, err := model.Order(test.Context())
	if err != nil || len(order.Assets) != 2 {
		test.Fatal("order", err)
	}
	review, err = model.Prepare(test.Context(), grokclient.Change{Action: grokclient.ReplaceOrder, ExpectedOrderRevision: order.Revision, OrderIDs: []string{copyID, source.Id}})
	if err != nil {
		test.Fatal(err)
	}
	result, err = model.Submit(test.Context(), review.Confirmation)
	if err != nil || result.Order == nil || result.Order.Assets[0].ID != copyID {
		test.Fatal("CAS order", err)
	}
	review, err = model.Prepare(test.Context(), grokclient.Change{Action: grokclient.Delete, ID: copyID, ExpectedRevision: 1})
	if err != nil {
		test.Fatal(err)
	}
	result, err = model.Submit(test.Context(), review.Confirmation)
	if err != nil || !result.Deleted {
		test.Fatal("CAS delete", err)
	}
	if fixture.count("BeginNativeConsumer") != 0 || fixture.count("ConsumeNativeGrant") != 0 {
		test.Fatal("asset operations invoked native flow")
	}
	if _, err := os.Stat(filepath.Join(fixture.service.Home, ".config", "skillhub", "client.key")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("asset operations initialized local key")
	}
}

func TestGrokDesktopReviewCASAndCancel(test *testing.T) {
	fixture := newConfigurationFixture(test)
	source := grokDesktopSeed(test, fixture)
	model := grokDesktopModel(test, fixture)
	if _, err := model.Prepare(test.Context(), grokclient.Change{Action: grokclient.Copy, ID: source.Id, ExpectedRevision: 2, Name: "stale"}); !errors.Is(err, grokclient.ErrConflict) {
		test.Fatal("stale revision accepted", err)
	}
	for _, action := range []grokclient.Action{grokclient.Create, grokclient.Update} {
		if _, err := model.Prepare(test.Context(), grokclient.Change{Action: action}); !errors.Is(err, grokclient.ErrUnsupported) {
			test.Fatal("unfinished editing became reachable", err)
		}
	}
	review, err := model.Prepare(test.Context(), grokclient.Change{Action: grokclient.Copy, ID: source.Id, ExpectedRevision: 1, Name: "cancelled"})
	if err != nil {
		test.Fatal(err)
	}
	model.CancelReview()
	if _, err := model.Submit(test.Context(), review.Confirmation); !errors.Is(err, grokclient.ErrClosed) || fixture.count("CopyAsset") != 0 {
		test.Fatal("cancelled review published", err)
	}
	for _, text := range []string{fmt.Sprintf("%+v", model), fmt.Sprintf("%#v", model)} {
		if strings.Contains(text, fixture.session) || strings.Contains(text, "synthetic-grok-desktop-secret") {
			test.Fatal("model formatting leaked")
		}
	}
	encoded, err := json.Marshal(model)
	if err != nil || strings.Contains(string(encoded), fixture.session) {
		test.Fatal("model JSON leaked", err)
	}
	model.Close()
	if _, err := model.List(test.Context()); !errors.Is(err, grokclient.ErrClosed) {
		test.Fatal("closed model admitted work", err)
	}
}

func TestGrokDesktopUnknownNeverReplays(test *testing.T) {
	fixture := newConfigurationFixture(test)
	source := grokDesktopSeed(test, fixture)
	model := grokDesktopModel(test, fixture)
	review, err := model.Prepare(test.Context(), grokclient.Change{Action: grokclient.Copy, ID: source.Id, ExpectedRevision: 1, Name: "lost receipt"})
	if err != nil {
		test.Fatal(err)
	}
	handler := httpapi.NewWithOptions(fixture.auth, httpapi.Options{Configuration: fixture.domain, PublicOrigin: fixture.server.URL})
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		if !strings.HasSuffix(request.URL.Path, "/CopyAsset") {
			return false
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			test.Error("synthetic copy did not commit")
		}
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
		return true
	})
	if _, err := model.Submit(test.Context(), review.Confirmation); !errors.Is(err, grokclient.ErrUnknown) {
		test.Fatal("lost receipt not Unknown", err)
	}
	assets, err := model.List(test.Context())
	if err != nil || len(assets) != 2 {
		test.Fatal("Unknown must allow read reconciliation", err)
	}
	if _, err := model.Submit(test.Context(), review.Confirmation); !errors.Is(err, grokclient.ErrClosed) {
		test.Fatal("consumed review replayed", err)
	}
	if _, err := model.Prepare(test.Context(), grokclient.Change{Action: grokclient.Copy, ID: source.Id, ExpectedRevision: 1, Name: "replay"}); !errors.Is(err, grokclient.ErrUnknown) || fixture.count("CopyAsset") != 1 {
		test.Fatal("read reconciliation re-enabled writes", err)
	}
}

func TestGrokDesktopSubmitCAS(test *testing.T) {
	fixture := newConfigurationFixture(test)
	source := grokDesktopSeed(test, fixture)
	model := grokDesktopModel(test, fixture)
	review, err := model.Prepare(test.Context(), grokclient.Change{Action: grokclient.Copy, ID: source.Id, ExpectedRevision: 1, Name: "stale before submit"})
	if err != nil {
		test.Fatal(err)
	}
	client := configurationv1connect.NewConfigurationServiceClient(fixture.server.Client(), fixture.server.URL)
	read := connect.NewRequest(&configurationv1.ReadDraftRequest{Id: source.Id})
	read.Header().Set("Authorization", "Bearer "+fixture.session)
	current, err := client.ReadDraft(test.Context(), read)
	if err != nil {
		test.Fatal(err)
	}
	current.Msg.View.Draft.Name = "Changed remotely"
	request := connect.NewRequest(&configurationv1.UpdateAssetRequest{Id: source.Id, ExpectedRevision: 1, Draft: current.Msg.View.Draft})
	request.Header().Set("Authorization", "Bearer "+fixture.session)
	if _, err := client.UpdateAsset(test.Context(), request); err != nil {
		test.Fatal(err)
	}
	if _, err := model.Submit(test.Context(), review.Confirmation); !errors.Is(err, grokclient.ErrConflict) {
		test.Fatal("changed source was copied", err)
	}
	if _, err := model.Submit(test.Context(), review.Confirmation); !errors.Is(err, grokclient.ErrClosed) || fixture.count("CopyAsset") != 1 {
		test.Fatal("CAS failure retained approval", err)
	}
}

func TestGrokDesktopFutureFieldsReadonly(test *testing.T) {
	fixture := newConfigurationFixture(test)
	raw := `{"version":1,"variant":"grokbuild-provider","provider":{"mode":"native-login"},"secrets":{},"future":{"secret":"synthetic-future-secret"}}`
	asset, err := fixture.store.CreateConfigurationAsset(test.Context(), fixture.owner, serverstate.ConfigurationAssetDraft{AgentID: "grokbuild", Kind: serverstate.ConfigurationKindProvider, Name: "Future native", SchemaVersion: 1, Payload: serverstate.NewConfigurationPayload([]byte(raw))})
	if err != nil {
		test.Fatal(err)
	}
	model := grokDesktopModel(test, fixture)
	summary, err := model.Read(test.Context(), asset.ID)
	if err != nil || !strings.Contains(summary, "extras=true") || !strings.Contains(summary, "NATIVE_LOGIN") || strings.Contains(summary, "synthetic-future-secret") {
		test.Fatal("future fields were exposed or native was lost", err)
	}
	if _, err := model.Prepare(test.Context(), grokclient.Change{Action: grokclient.Copy, ID: asset.ID, ExpectedRevision: 1, Name: "must not lose extras"}); !errors.Is(err, grokclient.ErrUnsupported) || fixture.count("CopyAsset") != 0 {
		test.Fatal("future fields became copyable", err)
	}
}

func TestGrokDesktopBusyCloseAndIdentity(test *testing.T) {
	fixture := newConfigurationFixture(test)
	identity := configurationTestIdentity(test, fixture)
	forged := identity
	forged.OwnerID = "other-owner"
	if _, err := fixture.service.openGrokAssets(test.Context(), forged); !errors.Is(err, errConfigurationIdentity) {
		test.Fatal("identity mismatch accepted", err)
	}
	model := grokDesktopModel(test, fixture)
	entered, exited := make(chan struct{}), make(chan struct{})
	unblock := make(chan struct{})
	test.Cleanup(func() { close(unblock) })
	fixture.setIntercept(func(writer http.ResponseWriter, request *http.Request) bool {
		if !strings.HasSuffix(request.URL.Path, "/GetSession") {
			return false
		}
		_, _ = io.Copy(io.Discard, request.Body)
		close(entered)
		select {
		case <-request.Context().Done():
		case <-unblock:
		}
		close(exited)
		return true
	})
	var workers sync.WaitGroup
	result := make(chan error, 1)
	workers.Go(func() {
		_, err := model.List(test.Context())
		result <- err
	})
	deadline, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-entered:
	case <-deadline.Done():
		test.Fatal("request never entered actual handler")
	}
	if _, err := model.List(test.Context()); !errors.Is(err, grokclient.ErrBusy) {
		test.Fatal("concurrent operation admitted", err)
	}
	closed := make(chan struct{})
	workers.Go(func() {
		model.Close()
		close(closed)
	})
	select {
	case <-closed:
	case <-deadline.Done():
		test.Fatal("close failed to drain actual worker")
	}
	select {
	case err := <-result:
		if err == nil {
			test.Fatal("cancelled worker reported success")
		}
	case <-deadline.Done():
		test.Fatal("worker did not return")
	}
	select {
	case <-exited:
	case <-deadline.Done():
		test.Fatal("actual HTTP request not cancelled")
	}
	workers.Wait()
	before := fixture.count("")
	if _, err := model.List(test.Context()); !errors.Is(err, grokclient.ErrClosed) || fixture.count("") != before {
		test.Fatal("closed model reached transport", err)
	}
}
