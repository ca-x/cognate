package configapply

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/geminiconfig"
)

func geminiDurableTestPlan(test *testing.T, fixture *geminiFixture) GeminiPlan {
	test.Helper()
	plan, prepared, _ := geminiTestPlan(test, fixture, agentconfig.GeminiAPIKeyAuth)
	confirmed := geminiApproveTestPlan(test, fixture, plan)
	binding := geminiAuthorizeTestPlan(test, fixture, confirmed, prepared)
	if fixture.engine.AdmitGeminiConsume(test.Context(), confirmed) != nil || fixture.engine.RecordGeminiConsumption(test.Context(), confirmed, binding) != nil {
		test.Fatal("durable synthetic consumption")
	}
	secret, err := geminiconfig.NewSelectedSecret(agentconfig.GeminiAPIKeySecret, []byte("synthetic-recovery-key"))
	if err != nil {
		test.Fatal("synthetic secret construction")
	}
	defer secret.Destroy()
	if fixture.engine.MaterializeGemini(test.Context(), confirmed, []geminiconfig.SelectedSecret{secret}) != nil {
		test.Fatal("durable pair candidates")
	}
	return confirmed
}

func TestGeminiExpiryPublicationAndSelectionRecovery(test *testing.T) {
	for _, boundary := range []string{"first-scan", "second-scan", "first-admission", "second-admission", "before-selection", "selection-staged"} {
		test.Run(boundary, func(test *testing.T) {
			fixture := newGeminiFixture(test, nil, nil)
			plan := geminiDurableTestPlan(test, fixture)
			deadline := plan.View().ExpiresAt
			state := fixture.engine.private()
			triggered, scans := false, 0
			state.fault = func(label string) error {
				if label == "observation-between" {
					scans++
				}
				if !triggered && (boundary == "first-scan" && label == "observation-between" && scans == 2 || boundary == "second-scan" && label == "observation-between" && scans == 4 || boundary == "first-admission" && label == "publish-admission-0" || boundary == "second-admission" && label == "publish-admission-1" || boundary == "before-selection" && label == boundary || boundary == "selection-staged" && label == "anchor-staged" && state.inventory.Operations[plan.View().OperationID].State == GeminiCompleted) {
					triggered = true
					state.now = func() time.Time { return deadline }
				}
				return nil
			}
			result, err := fixture.engine.ApplyGemini(test.Context(), plan)
			state.fault = nil
			if !triggered || !errors.Is(err, ErrGeminiExpired) || result.Applied || !result.Consumed {
				test.Fatal("expired publication/selection did not preserve consumed partial outcome", geminiSafeError(err))
			}
			for index, name := range geminiRoleNames {
				body, err := os.ReadFile(filepath.Join(fixture.home, ".gemini", name))
				present := boundary != "first-scan" && boundary != "first-admission" && (index == 0 || boundary != "second-scan" && boundary != "second-admission")
				if present && (err != nil || len(body) == 0) || !present && !errors.Is(err, os.ErrNotExist) {
					clear(body)
					test.Fatal("expiry published a later role or removed a committed role")
				}
				clear(body)
			}
			if !plan.View().ExpiresAt.Equal(deadline) {
				test.Fatal("original review deadline changed")
			}
			if fixture.engine.Close() != nil {
				test.Fatal("expired engine close")
			}
			reopened, err := OpenGemini(test.Context(), fixture.home, fixture.stateRoot, fixture.key)
			if err != nil {
				test.Fatal("expired partial reopen", geminiSafeError(err))
			}
			fixture.engine = reopened
			test.Cleanup(func() {
				if reopened.Close() != nil {
					test.Error("recovery engine close")
				}
			})
			current, err := reopened.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
			if err != nil || current.Revision() != 0 {
				test.Fatal("reopen promoted an uncommitted expired selection")
			}
			current.Destroy()
			status, err := reopened.StatusGemini(test.Context(), plan.View().OperationID, "https://synthetic.invalid", "fixture-owner", "fixture-profile")
			if err != nil || status.Applied || !status.Consumed {
				test.Fatal("expired partial status falsified consumption/application")
			}
			observed, err := reopened.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
			if err != nil {
				test.Fatal("new recovery observation")
			}
			defer observed.Destroy()
			recovery, err := reopened.PreviewGeminiRecovery(test.Context(), status.OperationID, "https://synthetic.invalid", "fixture-owner", "fixture-profile", "recover", observed)
			if err != nil {
				test.Fatal("fresh partial recovery review", geminiSafeError(err))
			}
			defer recovery.Destroy()
			if _, err := reopened.ExecuteGeminiRecovery(test.Context(), recovery); err == nil {
				test.Fatal("partial state silently reused old approval")
			}
			view := recovery.View()
			if reopened.ConfirmGeminiRecovery(test.Context(), recovery, view.PlanID, view.RequiredSlots, true) != nil {
				test.Fatal("fresh recovery confirmation")
			}
			result, err = reopened.ExecuteGeminiRecovery(test.Context(), recovery)
			if err != nil || !result.Applied || !result.Consumed {
				test.Fatal("newly approved partial recovery failed", geminiSafeError(err))
			}
		})
	}
}

func TestGeminiRestoreExpiryCannotChangePublishedMode(test *testing.T) {
	fixture := newGeminiFixture(test, nil, []byte(`{"ui":{"theme":"original"}}`))
	settings := filepath.Join(fixture.home, ".gemini", "settings.json")
	if os.Chmod(settings, 0o644) != nil {
		test.Fatal("synthetic original selector mode")
	}
	plan := geminiDurableTestPlan(test, fixture)
	if result, err := fixture.engine.ApplyGemini(test.Context(), plan); err != nil || !result.Applied {
		test.Fatal("restore original apply")
	}
	observed, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
	if err != nil {
		test.Fatal("restore observation")
	}
	defer observed.Destroy()
	restore, err := fixture.engine.PreviewGeminiRecovery(test.Context(), plan.View().OperationID, "https://synthetic.invalid", "fixture-owner", "fixture-profile", "restore", observed)
	if err != nil {
		test.Fatal("restore review")
	}
	defer restore.Destroy()
	view := restore.View()
	if fixture.engine.ConfirmGeminiRecovery(test.Context(), restore, view.PlanID, view.RequiredSlots, true) != nil {
		test.Fatal("restore confirmation")
	}
	state, triggered := fixture.engine.private(), false
	state.fault = func(label string) error {
		if label == "entry-published-1" {
			triggered = true
			state.now = func() time.Time { return view.ExpiresAt }
		}
		return nil
	}
	result, err := fixture.engine.ExecuteGeminiRecovery(test.Context(), restore)
	state.fault = nil
	info, statErr := os.Stat(settings)
	if !triggered || !errors.Is(err, ErrGeminiExpired) || result.Applied || result.Consumed || statErr != nil || info.Mode().Perm() != 0o600 {
		test.Fatal("restore changed target mode after expiry or misreported partial state", geminiSafeError(err))
	}
	if _, err := os.Lstat(filepath.Join(fixture.home, ".gemini", ".env")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("expiry undid a committed restore role")
	}
	current, err := fixture.engine.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil || current.Revision() != 1 {
		test.Fatal("expired restore advanced selection")
	}
	current.Destroy()
}

func TestGeminiTamperedDurableMaterialCannotPublish(test *testing.T) {
	for _, role := range []string{"candidate", "backup", "stage", "stage-late"} {
		test.Run(role, func(test *testing.T) {
			fixture := newGeminiFixture(test, nil, nil)
			plan := geminiDurableTestPlan(test, fixture)
			operation := fixture.engine.private().inventory.Operations[plan.View().OperationID]
			path := filepath.Join(fixture.stateRoot, "gemini-local-v1", operation.ID+"."+role)
			if role == "stage" || role == "stage-late" {
				path = filepath.Join(fixture.home, ".gemini", operation.StageNames[1])
			}
			foreign := []byte("synthetic-foreign-tamper\n")
			if role == "stage-late" {
				fixture.engine.private().fault = func(label string) error {
					if label == "directory-synced-0" {
						return os.WriteFile(path, foreign, 0o600)
					}
					return nil
				}
			} else if os.WriteFile(path, foreign, 0o600) != nil {
				test.Fatal("private tamper setup")
			}
			if result, err := fixture.engine.ApplyGemini(test.Context(), plan); err == nil || result.Applied {
				test.Fatal("tampered durable material published")
			}
			body, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(body, foreign) {
				test.Fatal("foreign durable material was removed or rewritten")
			}
			for index, name := range geminiRoleNames {
				_, err := os.Lstat(filepath.Join(fixture.home, ".gemini", name))
				if role == "stage-late" && index == 0 {
					if err != nil {
						test.Fatal("late corruption hid already-published env")
					}
				} else if !os.IsNotExist(err) {
					test.Fatal("corrupt candidate/stage changed target")
				}
			}
			if role == "stage-late" {
				current, err := fixture.engine.CurrentGeminiSelection(test.Context(), operation.Origin, operation.Owner, operation.Profile)
				defer current.Destroy()
				if err != nil || current.Revision() != 0 {
					test.Fatal("late corrupt stage committed selection")
				}
			}
		})
	}
}

func TestGeminiSelectionProcessChild(test *testing.T) {
	raw := os.Getenv("COGNATE_GEMINI_SELECTION_CHILD")
	if raw == "" {
		test.Skip("only actual cross-process parent supplies fixture")
	}
	var receipt geminiCrashReceipt
	if json.Unmarshal([]byte(raw), &receipt) != nil {
		test.Fatal("private child fixture")
	}
	key := bytes.Repeat([]byte{0x69}, 32)
	defer clear(key)
	engine, err := OpenGemini(test.Context(), receipt.Home, receipt.State, key)
	if err != nil {
		test.Fatal("child engine open")
	}
	defer engine.Close()
	fixture := &geminiFixture{engine: engine, home: receipt.Home, stateRoot: receipt.State, launch: receipt.Launch, key: key}
	plan := geminiDurableTestPlan(test, fixture)
	if result, err := engine.ApplyGemini(test.Context(), plan); err != nil || !result.Applied {
		test.Fatal("child public engine apply")
	}
}

func TestGeminiRealProcessStaleReviewCannotOverwrite(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	plan, _, _ := geminiTestPlan(test, fixture, agentconfig.GeminiAPIKeyAuth)
	var fields []GeminiFieldApproval
	for _, change := range plan.View().Changes {
		fields = append(fields, GeminiFieldApproval{Field: change.Field, Takeover: change.Ownership == geminiconfig.Foreign, Clear: change.Cleanup})
	}
	final, err := fixture.engine.ConfirmGemini(test.Context(), plan, fields, nil, false)
	if err != nil {
		test.Fatal("parent pre-final review")
	}
	defer final.Destroy()
	receipt, _ := json.Marshal(geminiCrashReceipt{Home: fixture.home, State: fixture.stateRoot, Launch: fixture.launch})
	ctx, cancel := context.WithTimeout(test.Context(), 90*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGeminiSelectionProcessChild$", "-test.count=1")
	child.Env = append(os.Environ(), "COGNATE_GEMINI_SELECTION_CHILD="+string(receipt))
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	err = child.Run()
	clear(output.Bytes())
	if err != nil || ctx.Err() != nil {
		test.Fatal("actual child did not apply+Wait")
	}
	if _, err := fixture.engine.ConfirmGemini(test.Context(), final, fields, final.View().RequiredSlots, true); !errors.Is(err, ErrGeminiConflict) {
		test.Fatal("stale cross-process final review overwrote newer selection")
	}
	current, err := fixture.engine.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	defer current.Destroy()
	if err != nil || current.Revision() != 1 || current.ConfirmationRevision() != 1 {
		test.Fatal("newer child selection not retained")
	}
}

type geminiCrashReceipt struct {
	Home, State, Operation string
	Launch                 GeminiLaunch
}

func geminiCrashOriginals(present bool) ([]byte, []byte) {
	if !present {
		return nil, nil
	}
	return []byte("# retained\r\nGEMINI_API_KEY='synthetic-foreign-crash-backup'\r\nFOREIGN=kept\r\n"), []byte(`{"ui":{"theme":"retained"},"model":{"name":"old","other":9007199254740993999},"security":{"auth":{"selectedType":"gemini-api-key"}}}`)
}

func TestGeminiActualKillChild(test *testing.T) {
	marker := os.Getenv("COGNATE_GEMINI_KILL_MARKER")
	if marker == "" {
		test.Skip("only actual crash parent supplies private marker")
	}
	present, restoring := os.Getenv("COGNATE_GEMINI_KILL_PRESENT") == "1", os.Getenv("COGNATE_GEMINI_KILL_RESTORE") == "1"
	env, settings := geminiCrashOriginals(present)
	fixture := newGeminiFixture(test, env, settings)
	if present && os.Chmod(filepath.Join(fixture.home, ".gemini", "settings.json"), 0o644) != nil {
		test.Fatal("present crash selector mode")
	}
	plan, prepared, _ := geminiTestPlan(test, fixture, agentconfig.GeminiAPIKeyAuth)
	confirmed := geminiApproveTestPlan(test, fixture, plan)
	binding := geminiAuthorizeTestPlan(test, fixture, confirmed, prepared)
	if fixture.engine.AdmitGeminiConsume(test.Context(), confirmed) != nil || fixture.engine.RecordGeminiConsumption(test.Context(), confirmed, binding) != nil {
		test.Fatal("crash consumption fixture")
	}
	operationID := confirmed.View().OperationID
	kill := func(label string) error {
		boundary := os.Getenv("COGNATE_GEMINI_KILL_BOUNDARY")
		selectionBoundary := boundary == "selection-staged" && label == "anchor-staged" || boundary == "selection-exchanged" && label == "anchor-exchanged"
		operation := fixture.engine.private().inventory.Operations[operationID]
		if label == boundary || selectionBoundary && operation != nil && (operation.State == GeminiCompleted || operation.State == GeminiRestored) {
			body, _ := json.Marshal(geminiCrashReceipt{Home: fixture.home, State: fixture.stateRoot, Operation: operationID, Launch: fixture.launch})
			if os.WriteFile(marker, body, 0o600) != nil {
				test.Fatal("private safe crash receipt")
			}
			process, _ := os.FindProcess(os.Getpid())
			_ = process.Kill()
			select {}
		}
		return nil
	}
	if !restoring {
		fixture.engine.private().fault = kill
	}
	secret, _ := geminiconfig.NewSelectedSecret(agentconfig.GeminiAPIKeySecret, []byte("synthetic-recovery-key"))
	defer secret.Destroy()
	if fixture.engine.MaterializeGemini(test.Context(), confirmed, []geminiconfig.SelectedSecret{secret}) != nil {
		test.Fatal("prekill materialization refused")
	}
	if _, err := fixture.engine.ApplyGemini(test.Context(), confirmed); err != nil {
		test.Fatal("source apply did not reach crash boundary")
	}
	if restoring {
		observed, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
		if err != nil {
			test.Fatal("restore crash fresh observation")
		}
		defer observed.Destroy()
		restore, err := fixture.engine.PreviewGeminiRecovery(test.Context(), operationID, "https://synthetic.invalid", "fixture-owner", "fixture-profile", "restore", observed)
		if err != nil {
			test.Fatal("restore crash new preview")
		}
		defer restore.Destroy()
		view := restore.View()
		if fixture.engine.ConfirmGeminiRecovery(test.Context(), restore, view.PlanID, view.RequiredSlots, true) != nil {
			test.Fatal("restore crash local-backup approval")
		}
		operationID = view.OperationID
		fixture.engine.private().fault = kill
		_, _ = fixture.engine.ExecuteGeminiRecovery(test.Context(), restore)
	}
	test.Fatal("actual SIGKILL boundary was not reached")
}

func TestGeminiActualKillReopenReview(test *testing.T) {
	geminiActualKillReview(test, []string{"anchor-staged", "anchor-exchanged", "anchor-durable", "vault-reserve-written", "target-reserve-written", "operation-reserved", "backup-written", "backup-durable", "candidate-written", "candidate-durable", "stage-written-0", "stage-written-1", "stage-durable-0", "stage-durable-1", "candidates-durable", "before-publish-0", "entry-published-0", "directory-synced-0", "before-publish-1", "entry-published-1", "directory-synced-1", "targets-durable", "before-selection", "completed-durable"})
}
func TestGeminiSelectionStageActualKillReopenReview(test *testing.T) {
	geminiActualKillReview(test, []string{"selection-staged", "selection-exchanged"})
}
func TestGeminiActualKillPresentAndRestore(test *testing.T) {
	for _, mode := range []string{"present-apply", "missing-restore", "present-restore"} {
		test.Run(mode, func(test *testing.T) {
			if strings.HasPrefix(mode, "present") {
				test.Setenv("COGNATE_GEMINI_KILL_PRESENT", "1")
			}
			if strings.HasSuffix(mode, "restore") {
				test.Setenv("COGNATE_GEMINI_KILL_RESTORE", "1")
			}
			geminiActualKillReview(test, []string{"candidate-written", "candidates-durable", "before-publish-0", "entry-published-0", "entry-published-1", "completed-durable"})
		})
	}
}
func geminiActualKillReview(test *testing.T, boundaries []string) {
	test.Helper()
	present, restoring := os.Getenv("COGNATE_GEMINI_KILL_PRESENT") == "1", os.Getenv("COGNATE_GEMINI_KILL_RESTORE") == "1"
	if os.Getenv("COGNATE_GEMINI_NODE") == "" || os.Getenv("COGNATE_GEMINI_PACKAGE") == "" {
		test.Skip("explicit complete runtime inputs required")
	}
	previous := geminiQualifiedProfiles
	geminiQualifiedProfiles = []GeminiProfileIdentity{geminiNativeIdentity()}
	defer func() { geminiQualifiedProfiles = previous }()
	for _, boundary := range boundaries {
		test.Run(boundary, func(test *testing.T) {
			root := test.TempDir()
			marker := filepath.Join(root, "crash-receipt")
			ctx, cancel := context.WithTimeout(test.Context(), 90*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGeminiActualKillChild$", "-test.count=1")
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "TMPDIR=") {
					child.Env = append(child.Env, entry)
				}
			}
			child.Env = append(child.Env, "TMPDIR="+root, "COGNATE_GEMINI_KILL_MARKER="+marker, "COGNATE_GEMINI_KILL_BOUNDARY="+boundary)
			var output bytes.Buffer
			child.Stdout = &output
			child.Stderr = &output
			err := child.Run()
			clear(output.Bytes())
			failure, ok := errors.AsType[*exec.ExitError](err)
			if !ok || failure.ProcessState.String() != "signal: killed" || ctx.Err() != nil {
				test.Fatal("real child did not SIGKILL+Wait at requested boundary")
			}
			body, err := os.ReadFile(marker)
			var receipt geminiCrashReceipt
			if err != nil || json.Unmarshal(body, &receipt) != nil {
				test.Fatal("safe crash receipt absent")
			}
			test.Setenv("HOME", receipt.Home)
			test.Setenv("TMPDIR", root)
			engine, err := OpenGemini(test.Context(), receipt.Home, receipt.State, bytes.Repeat([]byte{0x69}, 32))
			if err != nil {
				test.Fatal("authenticated crash reopen refused", geminiSafeError(err))
			}
			defer engine.Close()
			status, err := engine.StatusGemini(test.Context(), receipt.Operation, "https://synthetic.invalid", "fixture-owner", "fixture-profile")
			committed := boundary == "completed-durable" || boundary == "selection-exchanged"
			if err != nil || status.Applied != committed || status.Consumed == restoring {
				test.Fatal("crash status misreported Applied or consumption truth")
			}
			if committed {
				return
			}
			observed, err := engine.ObserveGemini(test.Context(), receipt.Launch, time.Now().Add(time.Minute))
			if err != nil {
				test.Fatal("fresh crash observation")
			}
			defer observed.Destroy()
			action := "recover"
			switch boundary {
			case "anchor-staged", "anchor-exchanged", "anchor-durable", "vault-reserve-written", "target-reserve-written", "operation-reserved", "backup-written", "backup-durable":
				action = "retire"
			}
			plan, err := engine.PreviewGeminiRecovery(test.Context(), receipt.Operation, "https://synthetic.invalid", "fixture-owner", "fixture-profile", action, observed)
			if err != nil {
				test.Fatal("fresh crash review refused")
			}
			defer plan.Destroy()
			view := plan.View()
			if _, err := engine.ExecuteGeminiRecovery(test.Context(), plan); err == nil {
				test.Fatal("restart restored old in-memory approval")
			}
			if engine.ConfirmGeminiRecovery(test.Context(), plan, view.PlanID, view.RequiredSlots, true) != nil {
				test.Fatal("new crash review confirmation")
			}
			result, err := engine.ExecuteGeminiRecovery(test.Context(), plan)
			if action == "retire" {
				if err != nil || result.Applied || result.State != GeminiRetiredUnpublished || !result.Consumed {
					test.Fatal("unpublished retirement falsified consumed truth")
				}
				originalEnv, originalSettings := geminiCrashOriginals(present)
				for index, name := range geminiRoleNames {
					body, err := os.ReadFile(filepath.Join(receipt.Home, ".gemini", name))
					defer clear(body)
					if present {
						wanted := originalEnv
						if index == 1 {
							wanted = originalSettings
						}
						if err != nil || !bytes.Equal(body, wanted) {
							test.Fatal("retirement changed present original")
						}
					} else if !os.IsNotExist(err) {
						test.Fatal("retirement wrote missing target")
					}
				}
				return
			}
			wantedState := GeminiCompleted
			if restoring {
				wantedState = GeminiRestored
			}
			if err != nil || !result.Applied || result.State != wantedState {
				test.Fatal("authenticated candidate recovery incomplete", geminiSafeError(err))
			}
		})
	}
}

func TestGeminiStatusReadOnlyMixedAndOwnerIsolation(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	plan := geminiDurableTestPlan(test, fixture)
	id := plan.View().OperationID
	fixture.engine.private().fault = func(label string) error {
		if label == "entry-published-0" {
			return ErrGeminiUnknown
		}
		return nil
	}
	if _, err := fixture.engine.ApplyGemini(test.Context(), plan); !errors.Is(err, ErrGeminiUnknown) {
		test.Fatal("partial publication fault not reached")
	}
	fixture.engine.private().fault = nil
	anchor, err := os.ReadFile(filepath.Join(fixture.stateRoot, geminiAnchorName))
	if err != nil {
		test.Fatal("private encrypted anchor read")
	}
	defer clear(anchor)
	status, err := fixture.engine.StatusGemini(test.Context(), id, "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil || status.Applied || !status.Consumed || status.State != GeminiReconcileRequired {
		test.Fatal("read-only status did not report real mixed pair")
	}
	again, err := os.ReadFile(filepath.Join(fixture.stateRoot, geminiAnchorName))
	defer clear(again)
	if err != nil || !bytes.Equal(anchor, again) {
		test.Fatal("status changed durable metadata")
	}
	if _, err := os.Lstat(filepath.Join(fixture.home, ".gemini", "settings.json")); !os.IsNotExist(err) {
		test.Fatal("status published second target")
	}
	if _, err := fixture.engine.StatusGemini(test.Context(), id, "https://synthetic.invalid", "other-owner", "fixture-profile"); !errors.Is(err, ErrGeminiConflict) {
		test.Fatal("status crossed owner")
	}
	observation, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
	if err != nil {
		test.Fatal("fresh mixed-pair observation")
	}
	defer observation.Destroy()
	if _, err := fixture.engine.PreviewGeminiRecovery(test.Context(), id, "https://synthetic.invalid", "other-owner", "fixture-profile", "recover", observation); !errors.Is(err, ErrGeminiConflict) {
		test.Fatal("recovery crossed owner")
	}
}

func TestGeminiRestartRecoverNewReviewAndRestorePresence(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	plan := geminiDurableTestPlan(test, fixture)
	id := plan.View().OperationID
	fixture.engine.private().fault = func(label string) error {
		if label == "directory-synced-0" {
			return ErrGeminiUnknown
		}
		return nil
	}
	if _, err := fixture.engine.ApplyGemini(test.Context(), plan); !errors.Is(err, ErrGeminiUnknown) {
		test.Fatal("partial fixture publication")
	}
	if fixture.engine.Close() != nil {
		test.Fatal("private close before restart")
	}
	reopened, err := OpenGemini(test.Context(), fixture.home, fixture.stateRoot, fixture.key)
	if err != nil {
		test.Fatal("authenticated restart refused")
	}
	fixture.engine = reopened
	test.Cleanup(func() { _ = reopened.Close() })
	observe := func() GeminiObservation {
		test.Helper()
		observed, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
		if err != nil {
			test.Fatal("fresh recovery observation")
		}
		test.Cleanup(observed.Destroy)
		return observed
	}
	recovery, err := fixture.engine.PreviewGeminiRecovery(test.Context(), id, "https://synthetic.invalid", "fixture-owner", "fixture-profile", "recover", observe())
	if err != nil {
		test.Fatal("fresh recovery preview refused")
	}
	defer recovery.Destroy()
	view := recovery.View()
	if view.PlanID == plan.View().PlanID || view.SourceOperation != id || !view.LocalMaterial || len(view.RequiredSlots) != 1 {
		test.Fatal("new local-candidate review binding missing")
	}
	if fixture.engine.ConfirmGeminiRecovery(test.Context(), recovery, plan.View().PlanID, view.RequiredSlots, true) == nil {
		test.Fatal("old plan confirmation accepted")
	}
	if fixture.engine.ConfirmGeminiRecovery(test.Context(), recovery, view.PlanID, nil, true) == nil {
		test.Fatal("local secret approval omitted")
	}
	if fixture.engine.ConfirmGeminiRecovery(test.Context(), recovery, view.PlanID, view.RequiredSlots, true) != nil {
		test.Fatal("explicit recovery confirmation")
	}
	result, err := fixture.engine.ExecuteGeminiRecovery(test.Context(), recovery)
	if err != nil || !result.Applied || !result.Consumed {
		test.Fatal("explicit local-only recovery did not complete pair")
	}
	if _, err := fixture.engine.ExecuteGeminiRecovery(test.Context(), recovery); err == nil {
		test.Fatal("recovery approval replayed")
	}
	restore, err := fixture.engine.PreviewGeminiRecovery(test.Context(), id, "https://synthetic.invalid", "fixture-owner", "fixture-profile", "restore", observe())
	if err != nil {
		test.Fatal("fresh restore preview refused")
	}
	defer restore.Destroy()
	restoreView := restore.View()
	if restoreView.OperationID == id || restoreView.SourceOperation != id || len(restoreView.RequiredSlots) != 0 {
		test.Fatal("restore is not a new absence operation")
	}
	if fixture.engine.ConfirmGeminiRecovery(test.Context(), restore, restoreView.PlanID, restoreView.RequiredSlots, true) != nil {
		test.Fatal("explicit restore confirmation")
	}
	result, err = fixture.engine.ExecuteGeminiRecovery(test.Context(), restore)
	if err != nil || result.State != GeminiRestored || !result.Applied {
		test.Fatal("restore absence failed")
	}
	for _, name := range geminiRoleNames {
		if _, err := os.Lstat(filepath.Join(fixture.home, ".gemini", name)); !os.IsNotExist(err) {
			test.Fatal("restore did not restore original absence")
		}
	}
	current, err := fixture.engine.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil || current.Revision() != 2 || current.ConfirmationRevision() != 2 {
		test.Fatal("restore selection CAS not advanced exactly once")
	}
}

func TestGeminiRestoreForeignBackupNeedsSlotExactBytesAndMode(test *testing.T) {
	initialEnv := []byte("# retained\r\nGEMINI_API_KEY='synthetic-foreign-backup-key'\r\nFOREIGN=kept\r\n")
	initialSettings := []byte(`{"ui":{"theme":"retained"},"security":{"auth":{"selectedType":"gemini-api-key","note":"keep"}},"model":{"name":"old","other":9007199254740993999}}`)
	fixture := newGeminiFixture(test, initialEnv, initialSettings)
	if os.Chmod(filepath.Join(fixture.home, ".gemini", "settings.json"), 0o644) != nil {
		test.Fatal("synthetic preexisting selector mode")
	}
	plan := geminiDurableTestPlan(test, fixture)
	if result, err := fixture.engine.ApplyGemini(test.Context(), plan); err != nil || !result.Applied {
		test.Fatal("foreign pair source apply")
	}
	observation, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
	if err != nil {
		test.Fatal("fresh exact restore observation")
	}
	defer observation.Destroy()
	restore, err := fixture.engine.PreviewGeminiRecovery(test.Context(), plan.View().OperationID, "https://synthetic.invalid", "fixture-owner", "fixture-profile", "restore", observation)
	if err != nil {
		test.Fatal("foreign backup restore preview")
	}
	defer restore.Destroy()
	view := restore.View()
	if len(view.RequiredSlots) != 1 || view.RequiredSlots[0] != agentconfig.GeminiAPIKeySecret || !view.LocalMaterial || view.Changes[1].Action != geminiconfig.Remove || !view.Changes[1].Cleanup {
		test.Fatal("restore omitted independent local-backup secret or exact endpoint removal review")
	}
	if fixture.engine.ConfirmGeminiRecovery(test.Context(), restore, view.PlanID, nil, true) == nil {
		test.Fatal("local backup key restored without new slot approval")
	}
	if fixture.engine.ConfirmGeminiRecovery(test.Context(), restore, view.PlanID, view.RequiredSlots, true) != nil {
		test.Fatal("new backup-slot and final approval")
	}
	fixture.engine.private().fault = func(label string) error {
		if label == "entry-published-1" {
			return ErrGeminiUnknown
		}
		return nil
	}
	result, err := fixture.engine.ExecuteGeminiRecovery(test.Context(), restore)
	if !errors.Is(err, ErrGeminiUnknown) || result.Applied {
		test.Fatal("restore mode-adjustment window was not interrupted")
	}
	if fixture.engine.Close() != nil {
		test.Fatal("close interrupted restore")
	}
	reopened, err := OpenGemini(test.Context(), fixture.home, fixture.stateRoot, fixture.key)
	if err != nil {
		test.Fatal("restart interrupted restore")
	}
	fixture.engine = reopened
	defer reopened.Close()
	observed, err := reopened.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
	if err != nil {
		test.Fatal("fresh interrupted-restore observation")
	}
	defer observed.Destroy()
	recovery, err := reopened.PreviewGeminiRecovery(test.Context(), view.OperationID, "https://synthetic.invalid", "fixture-owner", "fixture-profile", "recover", observed)
	if err != nil {
		test.Fatal("interrupted restore recovery preview")
	}
	defer recovery.Destroy()
	recoveryView := recovery.View()
	if len(recoveryView.RequiredSlots) != 1 {
		test.Fatal("interrupted restore bypassed new approval for foreign backup key")
	}
	if reopened.ConfirmGeminiRecovery(test.Context(), recovery, recoveryView.PlanID, nil, true) == nil {
		test.Fatal("interrupted restore reused old key approval")
	}
	if reopened.ConfirmGeminiRecovery(test.Context(), recovery, recoveryView.PlanID, recoveryView.RequiredSlots, true) != nil {
		test.Fatal("new interrupted-restore approval")
	}
	result, err = reopened.ExecuteGeminiRecovery(test.Context(), recovery)
	if err != nil || result.State != GeminiRestored {
		test.Fatal("foreign backup restore recovery failed", geminiSafeError(err))
	}
	for index, name := range geminiRoleNames {
		body, err := os.ReadFile(filepath.Join(fixture.home, ".gemini", name))
		defer clear(body)
		wanted := initialEnv
		mode := os.FileMode(0o600)
		if index == 1 {
			wanted = initialSettings
			mode = 0o644
		}
		info, statErr := os.Stat(filepath.Join(fixture.home, ".gemini", name))
		if err != nil || statErr != nil || !bytes.Equal(body, wanted) || info.Mode().Perm() != mode {
			test.Fatal("restore did not preserve original presence/bytes/mode")
		}
	}
}
