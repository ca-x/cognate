//go:build linux

package nativeconfig

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/geminiconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
)

func newGeminiKernelFixture(test *testing.T, mode agentconfig.GeminiAuthMode) (*Client, GeminiApplyRequest, string, *rpcCounts) {
	test.Helper()
	node, pack := os.Getenv("COGNATE_GEMINI_NODE"), os.Getenv("COGNATE_GEMINI_PACKAGE")
	if node == "" || pack == "" {
		test.Skip("complete explicit pinned consumer inputs required; no qualification from a skip")
	}
	client, base, session, home, calls := realFixture(test, configuration.NativeLogin)
	workspace := filepath.Join(home, "work")
	for _, path := range []string{filepath.Join(home, ".gemini"), workspace} {
		if os.Mkdir(path, 0o700) != nil {
			test.Fatal("private Gemini trusted directories")
		}
	}
	trust, _ := json.Marshal(map[string]string{workspace: "TRUST_FOLDER"})
	if os.WriteFile(filepath.Join(home, ".gemini", "trustedFolders.json"), trust, 0o600) != nil {
		test.Fatal("preexisting synthetic trust")
	}
	test.Setenv("HOME", home)
	provider := &configuration.GeminiProvider{AuthMode: mode, EnvModel: new("env-model"), SettingsModel: new("settings-model")}
	var mutations []configuration.SecretMutation
	if mode == agentconfig.GeminiAPIKeyAuth {
		provider.Endpoint = new("https://127.0.0.1:18444")
		provider.KeyTransport = new(agentconfig.GeminiNativeDefault)
		mutations = []configuration.SecretMutation{{Slot: configuration.GeminiAPIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-kernel#=$'\"key"))}}
	}
	asset, err := calls.domain.Create(test.Context(), base.OwnerID, configuration.Draft{Name: "Gemini complete kernel", GeminiProvider: provider}, mutations)
	if err != nil {
		test.Fatal("encrypted Gemini fixture asset")
	}
	return client, GeminiApplyRequest{OwnerID: base.OwnerID, AssetID: asset.Metadata.ID, AssetRevision: asset.Metadata.Revision, ProfileID: base.ProfileID, Launch: configapply.GeminiLaunch{NodeExecutable: node, PackageRoot: pack, Directory: workspace}}, session, calls
}

func geminiKernelConsent() GeminiInteraction {
	return GeminiInteraction{Approve: func(_ context.Context, view configapply.SafeGeminiPlan) (GeminiApproval, error) {
		approval := GeminiApproval{PlanID: view.PlanID, Confirmed: true}
		if view.SourceOperation == "" {
			for _, change := range view.Changes {
				approval.Fields = append(approval.Fields, configapply.GeminiFieldApproval{Field: change.Field, Takeover: change.Ownership == geminiconfig.Foreign, Clear: change.Cleanup})
			}
		}
		if view.FinalReview {
			approval.ApprovedSlots = append([]agentconfig.GeminiSecretSlot(nil), view.RequiredSlots...)
		}
		return approval, nil
	}}
}

func TestGeminiKernelRealTLSLifecycle(test *testing.T) {
	for _, mode := range []agentconfig.GeminiAuthMode{agentconfig.GeminiAPIKeyAuth, agentconfig.GeminiNativeLogin} {
		test.Run(string(mode), func(test *testing.T) {
			client, request, session, calls := newGeminiKernelFixture(test, mode)
			preview, err := client.ApplyGemini(test.Context(), request, session, GeminiInteraction{})
			if err != nil || len(preview.Preview.Changes) != 6 || calls.authorize.Load() != 0 || calls.consume.Load() != 0 {
				test.Fatal("real shared-kernel preview failed or acquired secrets", geminiFailure(err))
			}
			request.Apply = true
			result, err := client.ApplyGemini(test.Context(), request, session, geminiKernelConsent())
			expectedConsume := int32(0)
			if mode == agentconfig.GeminiAPIKeyAuth {
				expectedConsume = 1
			}
			if err != nil || !result.Local.Applied || result.Local.State != configapply.GeminiCompleted || calls.authorize.Load() != 1 || calls.consume.Load() != expectedConsume || calls.status.Load() != 0 {
				test.Fatal("real shared-kernel pair lifecycle failed", geminiFailure(err))
			}
			local := GeminiLocalRequest{OperationID: result.Local.OperationID, OwnerID: request.OwnerID, ProfileID: request.ProfileID, Action: "restore", Apply: true, Launch: request.Launch}
			restored, err := client.LocalGemini(test.Context(), local, geminiKernelConsent())
			if err != nil || restored.Local.State != configapply.GeminiRestored || calls.consume.Load() != expectedConsume {
				test.Fatal("local-only shared-kernel restore failed", geminiFailure(err))
			}
		})
	}
}

func TestGeminiKernelUnknownExplicitRestartStatusNoReplay(test *testing.T) {
	for _, phase := range []string{"authorize", "consume"} {
		test.Run(phase, func(test *testing.T) {
			client, request, session, calls := newGeminiKernelFixture(test, agentconfig.GeminiAPIKeyAuth)
			if phase == "authorize" {
				calls.loseAuthorize.Store(true)
			} else {
				calls.loseConsume.Store(true)
			}
			request.Apply = true
			result, err := client.ApplyGemini(test.Context(), request, session, geminiKernelConsent())
			if !errors.Is(err, configclient.ErrNativeUnknown) || !result.RemoteUnknown || result.Local.Applied || calls.authorize.Load() != 1 || calls.status.Load() != 0 {
				test.Fatal("lost real receipt was not Unknown, or auto Status occurred")
			}
			beforeRequests := calls.requests.Load()
			if _, err := client.ApplyGemini(test.Context(), request, session, geminiKernelConsent()); !errors.Is(err, configapply.ErrGeminiConflict) || calls.requests.Load() != beforeRequests {
				test.Fatal("pending Unknown allocated a fresh remote operation before explicit reconciliation")
			}
			state := client.private()
			remote := state.native
			if client.Close() != nil {
				test.Fatal("drain prior client")
			}
			restarted, err := New(state.home, state.stateRoot, state.origin, bytes.Repeat([]byte{37}, 32), remote)
			if err != nil {
				test.Fatal("restart private client")
			}
			defer restarted.Close()
			local := GeminiLocalRequest{OperationID: result.Local.OperationID, OwnerID: request.OwnerID, ProfileID: request.ProfileID, Action: "status", ReadRemote: true}
			status, err := restarted.LocalGemini(test.Context(), local, GeminiInteraction{})
			expected := configclient.NativeIssued
			expectedConsume := int32(0)
			if phase == "consume" {
				expected = configclient.NativeConsumed
				expectedConsume = 1
			}
			if err != nil || status.RemoteState != expected || status.Local.Applied || calls.consume.Load() != expectedConsume || calls.authorize.Load() != 1 || calls.status.Load() != 1 {
				test.Fatal("restart status-only ticket failed or replayed", geminiFailure(err))
			}
			local.OwnerID = "other-owner"
			if _, err := restarted.LocalGemini(test.Context(), local, GeminiInteraction{}); !errors.Is(err, configapply.ErrGeminiConflict) || calls.status.Load() != 1 {
				test.Fatal("ticket crossed owner")
			}
			local.OwnerID = request.OwnerID
			local.Action = "recover"
			local.ReadRemote = false
			local.Launch = request.Launch
			local.Apply = true
			if _, err := restarted.LocalGemini(test.Context(), local, geminiKernelConsent()); err == nil || calls.consume.Load() != expectedConsume {
				test.Fatal("status recovered nonexistent plaintext")
			}
		})
	}
}

func TestGeminiKernelCloseCancelsAndJoinsHumanReview(test *testing.T) {
	client, request, session, calls := newGeminiKernelFixture(test, agentconfig.GeminiAPIKeyAuth)
	request.Apply = true
	entered := make(chan struct{})
	finished := make(chan error, 1)
	var workers sync.WaitGroup
	ui := geminiKernelConsent()
	ui.Preview = func(ctx context.Context, _ configapply.SafeGeminiPlan) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	workers.Go(func() { _, err := client.ApplyGemini(test.Context(), request, session, ui); finished <- err })
	defer workers.Wait()
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		test.Fatal("real Gemini human review not reached")
	}
	if _, err := client.LocalGemini(test.Context(), GeminiLocalRequest{Action: "status"}, GeminiInteraction{}); !errors.Is(err, ErrBusy) {
		test.Fatal("concurrent shared client admission not busy")
	}
	if client.Close() != nil {
		test.Fatal("shared Gemini close")
	}
	select {
	case err := <-finished:
		if err == nil {
			test.Fatal("closed human review proceeded")
		}
	case <-time.After(time.Second):
		test.Fatal("shared Close failed to join human callback")
	}
	if calls.authorize.Load() != 0 || calls.consume.Load() != 0 || calls.status.Load() != 0 {
		test.Fatal("closed callback authorized/consumed or auto Status")
	}
}

func TestGeminiKernelManagedNativeLoginExactCleanup(test *testing.T) {
	client, request, session, calls := newGeminiKernelFixture(test, agentconfig.GeminiAPIKeyAuth)
	initial, err := calls.domain.Create(test.Context(), request.OwnerID, configuration.Draft{Name: "managed bearer precursor", GeminiProvider: &configuration.GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, Endpoint: new("https://127.0.0.1:18444"), KeyTransport: new(agentconfig.GeminiBearer), EnvModel: new("env-model"), SettingsModel: new("settings-model")}}, []configuration.SecretMutation{{Slot: configuration.GeminiAPIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-kernel#=$'\"key"))}})
	if err != nil {
		test.Fatal("explicit managed bearer fixture")
	}
	request.AssetID, request.AssetRevision = initial.Metadata.ID, initial.Metadata.Revision
	request.Apply = true
	if result, err := client.ApplyGemini(test.Context(), request, session, geminiKernelConsent()); err != nil || !result.Local.Applied {
		test.Fatal("managed API-key precursor")
	}
	asset, err := calls.domain.Create(test.Context(), request.OwnerID, configuration.Draft{Name: "explicit native-login transition", GeminiProvider: &configuration.GeminiProvider{AuthMode: agentconfig.GeminiNativeLogin}}, nil)
	if err != nil {
		test.Fatal("native-login metadata asset")
	}
	request.AssetID, request.AssetRevision = asset.Metadata.ID, asset.Metadata.Revision
	ui := geminiKernelConsent()
	ui.Preview = func(_ context.Context, view configapply.SafeGeminiPlan) error {
		if len(view.RequiredSlots) != 0 || len(view.Changes) != 6 {
			test.Fatal("native-login fabricated secret approval or omitted field review")
		}
		for _, change := range view.Changes[:3] {
			if change.Ownership != geminiconfig.Managed || change.Action != geminiconfig.Remove || !change.Cleanup {
				test.Fatal("native-login omitted exact managed routing cleanup")
			}
		}
		return nil
	}
	if result, err := client.ApplyGemini(test.Context(), request, session, ui); err != nil || !result.Local.Applied || result.Local.Consumed {
		test.Fatal("explicit native-login no-secret transition failed", geminiFailure(err))
	}
	env, err := os.ReadFile(filepath.Join(request.Launch.Directory, "..", ".gemini", ".env"))
	defer clear(env)
	if err != nil {
		test.Fatal("native-login retained model env image")
	}
	for _, removed := range []string{"GEMINI_API_KEY=", "GOOGLE_GEMINI_BASE_URL=", "GEMINI_API_KEY_AUTH_MECHANISM="} {
		if bytes.Contains(env, []byte(removed)) {
			test.Fatal("native-login retained old API routing")
		}
	}
	if !bytes.Contains(env, []byte("GEMINI_MODEL=")) || calls.authorize.Load() != 2 || calls.consume.Load() != 1 || calls.status.Load() != 0 {
		test.Fatal("native-login changed unrelated model or replayed material")
	}
}

func TestGeminiKernelTwoOwnersFreshTakeoverNotOldAuthority(test *testing.T) {
	client, request, session, calls := newGeminiKernelFixture(test, agentconfig.GeminiAPIKeyAuth)
	request.Apply = true
	first, err := client.ApplyGemini(test.Context(), request, session, geminiKernelConsent())
	if err != nil {
		test.Fatal("first owner complete pair")
	}
	otherSession, otherOwner, err := calls.ownerAuth.LoginOIDC(test.Context(), "https://synthetic.identity", "Gemini-other-owner", "", "")
	if err != nil {
		test.Fatal("second actual owner session")
	}
	asset, err := calls.domain.Create(test.Context(), otherOwner, configuration.Draft{Name: "other owner selected provider", GeminiProvider: &configuration.GeminiProvider{AuthMode: agentconfig.GeminiAPIKeyAuth, Endpoint: new("https://127.0.0.1:18444"), KeyTransport: new(agentconfig.GeminiBearer), EnvModel: new("other-owner-model")}}, []configuration.SecretMutation{{Slot: configuration.GeminiAPIKeySlot, Action: configuration.Replace, Replacement: configuration.NewSecret([]byte("synthetic-other-owner-Gemini-key"))}})
	if err != nil {
		test.Fatal("second owner encrypted Gemini asset")
	}
	other := request
	other.OwnerID, other.AssetID, other.AssetRevision = otherOwner, asset.Metadata.ID, asset.Metadata.Revision
	second, err := client.ApplyGemini(test.Context(), other, otherSession.Reveal(), geminiKernelConsent())
	if err != nil || !second.Local.Applied || calls.consume.Load() != 2 {
		test.Fatal("fresh explicit other-owner takeover incorrectly inherited or permanently blocked prior owner", geminiFailure(err))
	}
	if _, err := client.LocalGemini(test.Context(), GeminiLocalRequest{OperationID: first.Local.OperationID, OwnerID: request.OwnerID, ProfileID: request.ProfileID, Action: "restore", Apply: true, Launch: request.Launch}, geminiKernelConsent()); !errors.Is(err, configapply.ErrGeminiConflict) || calls.consume.Load() != 2 {
		test.Fatal("old owner's restore overwrote newer other-owner pair")
	}
	if _, err := client.LocalGemini(test.Context(), GeminiLocalRequest{OperationID: first.Local.OperationID, OwnerID: otherOwner, ProfileID: request.ProfileID, Action: "status"}, GeminiInteraction{}); !errors.Is(err, configapply.ErrGeminiConflict) {
		test.Fatal("other owner reused old operation authority")
	}
}
