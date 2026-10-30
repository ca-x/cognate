//go:build linux

package configapply

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
)

func TestCodexTombstoneBlocksLatePreparation(test *testing.T) {
	engine, _, _, _ := codexFixture(test)
	bodies := [3][]byte{[]byte("late-config"), nil, []byte("late-catalog")}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
		target, err := engine.private().home.child(".codex", false)
		if err != nil {
			return err
		}
		defer target.close()
		operation.State = CodexAbandonedBeforeWrite
		return engine.private().reserveCodexOperation(target, operation)
	})
	if err != nil {
		test.Fatal("tombstone fixture could not persist")
	}
	err = engine.private().withLock(test.Context(), func(ctx context.Context) error {
		return engine.private().prepareCodexOperation(ctx, operation, bodies)
	})
	if !errors.Is(err, ErrCodexConflict) || operation.Candidate.Present {
		test.Fatal("durable terminal tombstone permitted late candidate preparation")
	}
}

func codexManagedPlanFixture(test *testing.T) (*CodexEngine, CodexPlan, string, string, []byte) {
	return codexManagedCatalogPlanFixture(test, nil)
}

func codexManagedCatalogPlanFixture(test *testing.T, catalog *agentconfig.CodexCatalogDesired) (*CodexEngine, CodexPlan, string, string, []byte) {
	test.Helper()
	engine, home, stateRoot, key := codexFixture(test)
	launch := codexNativeLaunchFixture(test, home)
	codexCandidateRegistryFixture(test)
	selection := codexShapeSelection(test, engine)
	selection.get[codexSelectionData]().declaration.Catalog = catalog
	test.Cleanup(selection.Destroy)
	observation, err := engine.ObserveCodex(test.Context(), selection, launch)
	if err != nil {
		test.Fatal("managed fixture physical observation refused")
	}
	test.Cleanup(observation.Destroy)
	plan, err := engine.PreviewCodexObserved(test.Context(), selection, observation)
	if err != nil {
		test.Fatal("managed fixture provider plan refused")
	}
	test.Cleanup(plan.Destroy)
	return engine, plan, home, stateRoot, key
}

func TestCodexManagedAfterOwnedCatalogMetadata(test *testing.T) {
	catalog := &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "synthetic-model", SupportsParallelToolCalls: new(true)}, {Model: "second-model", SupportsParallelToolCalls: new(true)}}}
	engine, plan, _, _, _ := codexManagedCatalogPlanFixture(test, catalog)
	admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
	if err != nil {
		test.Fatal("confirm")
	}
	defer admission.Destroy()
	constraints := admission.CopyAuthorizationConstraints()
	defer constraints.Destroy()
	if admission.BeginAuthorize(test.Context(), constraints) != nil || admission.BeginConsume(test.Context()) != nil {
		test.Fatal("attempts")
	}
	secret, err := codexconfig.NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("SYNTHETIC_AFTER_OWNER"))
	if err != nil {
		test.Fatal("secret")
	}
	defer secret.Destroy()
	candidate, err := engine.MaterializeCodex(test.Context(), admission, []codexconfig.SelectedSecret{secret})
	if err != nil {
		test.Fatal("render")
	}
	defer candidate.Destroy()
	if result, err := engine.ApplyCodex(test.Context(), admission, candidate); err != nil || result.State != CodexCompleted {
		test.Fatal("publish")
	}
	if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
		models := engine.private().inventory.Operations[plan.View().OperationID].ManagedModels
		if len(models) != 2 || models[0] != "synthetic-model" || models[1] != "second-model" {
			test.Fatal("first generated catalog did not persist after-owned model IDs in declaration order")
		}
		return nil
	}); err != nil {
		test.Fatal("owned metadata")
	}
}

func TestCodexPublicationOrderAuthenticatedMetadata(test *testing.T) {
	for _, kind := range []string{"nil", "empty", "legacy-before", "after-owned", "ordinary-apply", "present-catalog", "unknown-metadata-version"} {
		test.Run(kind, func(test *testing.T) {
			operation := &codexOperation{Restoring: true, Write: [3]bool{true, false, true}, ManagedModelsVersion: 1, ManagedModels: []string{"synthetic-model"}}
			switch kind {
			case "nil":
				operation.ManagedModels = nil
			case "empty":
				operation.ManagedModels = []string{}
			case "legacy-before":
				operation.ManagedModelsVersion = 0
			case "ordinary-apply":
				operation.Restoring = false
			case "present-catalog":
				operation.After[2].Present = true
			case "unknown-metadata-version":
				operation.ManagedModelsVersion = 2
			}
			want := [2]int{2, 0}
			if kind == "after-owned" {
				want = [2]int{0, 2}
			}
			if codexPublicationOrder(operation) != want {
				test.Fatal("publication order depended on unproven catalog metadata")
			}
			if kind == "unknown-metadata-version" && codexLifecycleShape(operation) == nil {
				test.Fatal("unknown producer metadata version accepted")
			}
		})
	}
	operation := &codexOperation{LifecycleVersion: 0, ManagedModelsVersion: 1, ManagedModels: []string{"forged-after"}}
	if codexLifecycleShape(operation) == nil {
		test.Fatal("legacy record was silently promoted to after-owned evidence")
	}
}

func TestCodexManagedConfirmationConstraints(test *testing.T) {
	engine, plan, _, _, _ := codexManagedPlanFixture(test)
	admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, true)
	if err != nil {
		test.Fatal("managed confirmation refused")
	}
	defer admission.Destroy()
	constraints := admission.CopyAuthorizationConstraints()
	defer constraints.Destroy()
	if constraints.OperationID() == [16]byte{} || constraints.ConfirmationRevision() != plan.View().ConfirmationRevision || !constraints.ApprovedDeadline().Equal(time.UnixMilli(plan.View().ExpiresAt.UnixMilli())) || len(constraints.RequiredSlots()) != 1 {
		test.Fatal("confirmed engine did not bind original immutable authorization constraints")
	}
}

func TestCodexQualifiedProfileQueryNoIO(test *testing.T) {
	before := codexQualifiedProfiles
	test.Cleanup(func() { codexQualifiedProfiles = before })
	home := filepath.Join(test.TempDir(), "absent-home")
	test.Setenv("HOME", home)
	for _, kind := range []string{"empty", "foreign-manifest", "foreign-profile", "foreign-template", "foreign-id", "exact"} {
		test.Run(kind, func(test *testing.T) {
			identity := codexNative0166Identity()
			if kind == "empty" {
				codexQualifiedProfiles = nil
			} else {
				candidate := identity
				switch kind {
				case "foreign-manifest":
					candidate.ManifestSHA256[0] ^= 1
				case "foreign-profile":
					candidate.ProfileDigest[0] ^= 1
				case "foreign-template":
					candidate.TemplateDigest[0] ^= 1
				case "foreign-id":
					candidate.ID = "foreign-profile"
				}
				codexQualifiedProfiles = []CodexProfileIdentity{candidate}
			}
			actual, err := QualifiedCodexNativeProfile()
			if kind == "exact" && runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
				if err != nil || actual != identity {
					test.Fatal("exact private fixture tuple refused")
				}
			} else if !errors.Is(err, ErrCodexUnsupported) || actual != (CodexProfileIdentity{}) {
				test.Fatal("unqualified tuple reported Ready")
			}
			if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("qualification query touched HOME")
			}
		})
	}
}

func TestCodexManagedAbortLifecycle(test *testing.T) {
	for _, phase := range []string{"confirm", "authorize", "consume", "render-failure", "rendered"} {
		test.Run(phase, func(test *testing.T) {
			engine, plan, home, stateRoot, key := codexManagedPlanFixture(test)
			admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
			if err != nil {
				test.Fatal("managed confirm refused")
			}
			defer admission.Destroy()
			constraints := admission.CopyAuthorizationConstraints()
			defer constraints.Destroy()
			if phase != "confirm" {
				if err := admission.BeginAuthorize(test.Context(), constraints); err != nil {
					test.Fatal("authorize intent refused")
				}
			}
			if phase == "consume" || phase == "render-failure" || phase == "rendered" {
				if err := admission.BeginConsume(test.Context()); err != nil {
					test.Fatal("consume intent refused")
				}
			}
			var candidate codexconfig.ProviderCandidate
			if phase == "render-failure" {
				candidate, err = engine.MaterializeCodex(test.Context(), admission, []codexconfig.SelectedSecret{{}})
				if err == nil || candidate.Binding().CopyObservation().ProfileID != "" {
					test.Fatal("bad material produced candidate")
				}
			}
			if phase == "rendered" {
				secret, err := codexconfig.NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("SYNTHETIC_MANAGED_MATERIAL"))
				if err != nil {
					test.Fatal("secret setup")
				}
				defer secret.Destroy()
				candidate, err = engine.MaterializeCodex(test.Context(), admission, []codexconfig.SelectedSecret{secret})
				if err != nil {
					test.Fatal("managed materialization refused")
				}
				defer candidate.Destroy()
			}
			if phase == "confirm" || phase == "authorize" {
				stop, err := engine.PreviewCodexAbandon(test.Context(), plan.View().OperationID)
				if err != nil {
					test.Fatal("safe abandon preview refused")
				}
				defer stop.Destroy()
				confirmed, err := stop.Confirm(test.Context(), true)
				if err != nil {
					test.Fatal("abandon confirm refused")
				}
				defer confirmed.Destroy()
				result, err := engine.AbandonCodex(test.Context(), confirmed)
				if err != nil || result.State != CodexAbandonedBeforeWrite || result.RemoteOutcomeUnknown {
					test.Fatal("safe abandon did not reach truthful terminal")
				}
				if _, err := os.Stat(filepath.Join(home, ".codex", "codex-"+plan.View().OperationID+".reserve")); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("abandon did not remove owned reservation")
				}
			} else {
				if _, err := engine.PreviewCodexAbandon(test.Context(), plan.View().OperationID); err == nil {
					test.Fatal("attempted consumption became safe abandon")
				}
				stop, err := engine.PreviewCodexRetireUnpublished(test.Context(), plan.View().OperationID)
				if err != nil {
					test.Fatal("manual retirement preview refused")
				}
				defer stop.Destroy()
				confirmed, err := stop.Confirm(test.Context(), true)
				if err != nil {
					test.Fatal("manual retirement confirm refused")
				}
				defer confirmed.Destroy()
				result, err := engine.RetireCodexUnpublished(test.Context(), confirmed)
				if err != nil || result.State != CodexRetiredUnpublished || !result.RemoteOutcomeUnknown || !result.NeedsAttention {
					test.Fatal("retirement erased remote uncertainty")
				}
				if _, err := os.Stat(filepath.Join(home, ".codex", "codex-"+plan.View().OperationID+".reserve")); err != nil {
					test.Fatal("retirement discarded unknown reservation")
				}
			}
			if err := admission.Revalidate(test.Context()); err == nil {
				test.Fatal("terminal admission revalidated")
			}
			if err := admission.BeginConsume(test.Context()); err == nil {
				test.Fatal("terminal admission consumed")
			}
			if _, err := engine.ApplyCodex(test.Context(), admission, candidate); err == nil {
				test.Fatal("late producer published after tombstone")
			}
			if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("abort wrote target")
			}
			if err := engine.Close(); err != nil {
				test.Fatal("close did not drain")
			}
			reopened, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("terminal journal did not reopen")
			}
			defer reopened.Close()
			result, err := reopened.StatusCodex(test.Context(), plan.View().OperationID)
			if err != nil || result.State == CodexRemoteOutcomeUnknown || result.SelectionRevision != 0 {
				test.Fatal("terminal state was lost or selection advanced")
			}
		})
	}
}

func TestCodexManagedConstraintsMismatchAndLegacy(test *testing.T) {
	for _, kind := range []string{"revision", "deadline", "binding", "legacy", "version"} {
		test.Run(kind, func(test *testing.T) {
			engine, plan, _, _, _ := codexManagedPlanFixture(test)
			var admission CodexAdmission
			var err error
			if kind == "legacy" {
				admission, err = engine.ConfirmCodex(test.Context(), plan, plan.View().RequiredSlots, true)
			} else {
				admission, err = engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
			}
			if err != nil {
				test.Fatal("confirm setup")
			}
			defer admission.Destroy()
			constraints := admission.CopyAuthorizationConstraints()
			defer constraints.Destroy()
			if record := constraints.get[codexAuthorizationRecord](); record != nil {
				if kind == "revision" {
					record.Revision++
				}
				if kind == "deadline" {
					record.DeadlineUnixMS++
				}
			}
			if kind == "binding" || kind == "version" {
				if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
					operation := engine.private().inventory.Operations[plan.View().OperationID]
					if kind == "binding" {
						operation.Binding[0] ^= 1
					} else {
						operation.LifecycleVersion = 2
						if codexInventoryShape(&engine.private().inventory) == nil {
							test.Fatal("unknown lifecycle version accepted")
						}
						operation.LifecycleVersion = 1
					}
					return engine.private().saveCodexInventory()
				}); err != nil {
					test.Fatal("record mismatch setup")
				}
			}
			if kind != "version" && admission.BeginAuthorize(test.Context(), constraints) == nil {
				test.Fatal("mismatched or legacy constraints persisted authorization intent")
			}
			if kind == "legacy" {
				if _, err := engine.PreviewCodexAbandon(test.Context(), plan.View().OperationID); err == nil {
					test.Fatal("legacy journal acquired nonconsume proof")
				}
			}
			if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
				if engine.private().inventory.Operations[plan.View().OperationID].State != CodexPreviewed {
					test.Fatal("mismatch advanced attempted state")
				}
				return nil
			}); err != nil {
				test.Fatal("record read")
			}
		})
	}
}

func TestCodexManagedPrivateContainers(test *testing.T) {
	engine, plan, _, _, _ := codexManagedPlanFixture(test)
	admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
	if err != nil {
		test.Fatal("confirm")
	}
	defer admission.Destroy()
	constraints := admission.CopyAuthorizationConstraints()
	defer constraints.Destroy()
	for _, value := range []any{constraints, struct{ Value CodexAuthorizationConstraints }{constraints}, []CodexAuthorizationConstraints{constraints}, map[string]any{"value": constraints}} {
		var output bytes.Buffer
		for _, verb := range []string{"%v", "%+v", "%#v", "%d", "%x", "%X"} {
			fmt.Fprintf(&output, verb, value)
		}
		encoded, _ := json.Marshal(value)
		output.Write(encoded)
		slog.New(slog.NewJSONHandler(&output, nil)).Info("private", "value", value)
		if strings.Contains(output.String(), "fixture-owner") || strings.Contains(output.String(), fmt.Sprint(constraints.ConfirmationRevision())) && strings.Contains(output.String(), "Revision") {
			test.Fatal("private constraints leaked")
		}
	}
}

func TestCodexManagedConstraintCanaryAndCopy(test *testing.T) {
	record := codexAuthorizationRecord{Operation: [16]byte{0xe1, 0x92, 0xa3, 0xb4, 0xc5, 0xd6, 0xe7, 0xf8, 0x19, 0x2a, 0x3b, 0x4c, 0x5d, 0x6e, 0x7f, 0x80}, Slot: agentconfig.CodexProviderKeySecret, Revision: 0x718293a4b5c6d7e8, DeadlineUnixMS: 0x0123456789abcdef}
	constraints := CodexAuthorizationConstraints{value: new(any(new(record)))}
	defer constraints.Destroy()
	canaries := []string{strconv.FormatUint(record.Revision, 10), strconv.FormatUint(record.Revision, 16), strings.ToUpper(strconv.FormatUint(record.Revision, 16)), strconv.FormatInt(record.DeadlineUnixMS, 10), strconv.FormatInt(record.DeadlineUnixMS, 16), fmt.Sprintf("%x", record.Operation), fmt.Sprintf("%v", record.Operation)}
	for _, value := range []any{constraints, new(constraints), struct{ Value CodexAuthorizationConstraints }{constraints}, struct{ value CodexAuthorizationConstraints }{constraints}, []CodexAuthorizationConstraints{constraints}, map[string]CodexAuthorizationConstraints{"value": constraints}} {
		var output bytes.Buffer
		for _, verb := range []string{"%v", "%+v", "%#v", "%d", "%x", "%X"} {
			fmt.Fprintf(&output, verb, value)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			output.WriteString(err.Error())
		}
		output.Write(encoded)
		slog.New(slog.NewJSONHandler(&output, nil)).Info("private", "value", value)
		for _, canary := range canaries {
			if strings.Contains(output.String(), canary) {
				test.Fatal("private numeric/hex constraint canary escaped")
			}
		}
	}
	engine, plan, _, _, _ := codexManagedPlanFixture(test)
	admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
	if err != nil {
		test.Fatal("confirm")
	}
	defer admission.Destroy()
	first, second := admission.CopyAuthorizationConstraints(), admission.CopyAuthorizationConstraints()
	defer first.Destroy()
	defer second.Destroy()
	operation := first.OperationID()
	operation[0] ^= 1
	slots := first.RequiredSlots()
	slots[0] = agentconfig.CodexSecretSlot("foreign-slot")
	if first.OperationID() == operation || first.RequiredSlots()[0] != agentconfig.CodexProviderKeySecret {
		test.Fatal("constraints accessor returned shared mutable storage")
	}
	first.Destroy()
	if first.OperationID() != [16]byte{} || first.ConfirmationRevision() != 0 || len(first.RequiredSlots()) != 0 || !first.ApprovedDeadline().IsZero() || second.OperationID() == [16]byte{} {
		test.Fatal("constraint copy/destroy ownership broken")
	}
}

func TestCodexManagedChild(test *testing.T) {
	mode := os.Getenv("COGNATE_MANAGED_CHILD")
	if mode == "" {
		test.Skip("explicit bounded subprocess fixture")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 15*time.Second)
	defer cancel()
	engine, err := OpenCodex(ctx, os.Getenv("COGNATE_MANAGED_HOME"), os.Getenv("COGNATE_MANAGED_STATE"), bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		test.Fatal("child open")
	}
	defer engine.Close()
	id := os.Getenv("COGNATE_MANAGED_OPERATION")
	if mode == "restore-crash" {
		plan, err := engine.PreviewCodexRestore(ctx, id)
		if err != nil {
			test.Fatal("child restore preview")
		}
		defer plan.Destroy()
		confirmed, err := plan.Confirm(ctx, true)
		if err != nil {
			test.Fatal("child restore confirm")
		}
		defer confirmed.Destroy()
		engine.private().fault = func(stage string) error {
			if stage == "directory-synced-0" {
				os.Exit(73)
			}
			return nil
		}
		_, _ = engine.RestoreCodex(ctx, confirmed)
		test.Fatal("real config-first crash boundary not reached")
	}
	if mode == "race" {
		fmt.Fprintln(os.Stdout, "READY")
		for {
			if _, err := os.Stat(os.Getenv("COGNATE_MANAGED_GATE")); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				test.Fatal("child schedule deadline")
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	if mode == "retire-crash" {
		plan, err := engine.PreviewCodexRetireUnpublished(ctx, id)
		if err != nil {
			test.Fatal("child retire preview")
		}
		defer plan.Destroy()
		confirmed, err := plan.Confirm(ctx, true)
		if err != nil {
			test.Fatal("child retire confirm")
		}
		defer confirmed.Destroy()
		engine.private().fault = func(stage string) error {
			if stage == "consumer-tombstone-durable" {
				os.Exit(73)
			}
			return nil
		}
		_, _ = engine.RetireCodexUnpublished(ctx, confirmed)
		test.Fatal("retire crash boundary not reached")
	}
	plan, err := engine.PreviewCodexAbandon(ctx, id)
	if err != nil {
		if mode == "race" {
			os.Exit(42)
		}
		test.Fatal("child abandon preview")
	}
	defer plan.Destroy()
	confirmed, err := plan.Confirm(ctx, true)
	if err != nil {
		if mode == "race" {
			os.Exit(42)
		}
		test.Fatal("child abandon confirm")
	}
	defer confirmed.Destroy()
	if mode == "abandon-crash" || mode == "cleanup-crash" {
		phase := "consumer-tombstone-durable"
		if mode == "cleanup-crash" {
			phase = "consumer-abandon-cleaned"
		}
		engine.private().fault = func(stage string) error {
			if stage == phase {
				os.Exit(73)
			}
			return nil
		}
	}
	_, err = engine.AbandonCodex(ctx, confirmed)
	if mode == "race" {
		if err != nil {
			os.Exit(42)
		}
		return
	}
	test.Fatal("abandon crash boundary not reached")
}

func codexManagedChildCommand(test *testing.T, ctx context.Context, mode, home, stateRoot, id, gate string) *exec.Cmd {
	test.Helper()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexManagedChild$", "-test.timeout=20s")
	child.Env = []string{"HOME=" + home, "TMPDIR=" + os.TempDir(), "PATH=/usr/bin:/bin", "COGNATE_Q2_NATIVE_FIXTURE=" + os.Getenv("COGNATE_Q2_NATIVE_FIXTURE"), "COGNATE_MANAGED_CHILD=" + mode, "COGNATE_MANAGED_HOME=" + home, "COGNATE_MANAGED_STATE=" + stateRoot, "COGNATE_MANAGED_OPERATION=" + id, "COGNATE_MANAGED_GATE=" + gate}
	return child
}

func TestCodexManagedTwoProcessConsumeAbandon(test *testing.T) {
	for _, schedule := range []string{"consume-first", "abandon-first", "race"} {
		test.Run(schedule, func(test *testing.T) {
			engine, plan, home, stateRoot, _ := codexManagedPlanFixture(test)
			admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
			if err != nil {
				test.Fatal("confirm")
			}
			defer admission.Destroy()
			constraints := admission.CopyAuthorizationConstraints()
			defer constraints.Destroy()
			if err := admission.BeginAuthorize(test.Context(), constraints); err != nil {
				test.Fatal("authorize intent")
			}
			ctx, cancel := context.WithTimeout(test.Context(), 20*time.Second)
			defer cancel()
			gate := filepath.Join(test.TempDir(), "release")
			child := codexManagedChildCommand(test, ctx, "race", home, stateRoot, plan.View().OperationID, gate)
			stdout, err := child.StdoutPipe()
			if err != nil {
				test.Fatal("child pipe")
			}
			defer stdout.Close()
			if child.Start() != nil {
				test.Fatal("child start")
			}
			defer func() {
				if child.ProcessState == nil {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			scanner := bufio.NewScanner(stdout)
			if !scanner.Scan() || scanner.Text() != "READY" {
				test.Fatal("child readiness")
			}
			var consumeErr, errorChild error
			if schedule == "consume-first" {
				consumeErr = admission.BeginConsume(ctx)
			}
			if os.WriteFile(gate, []byte("release"), 0o600) != nil {
				test.Fatal("release")
			}
			if schedule == "abandon-first" {
				errorChild = child.Wait()
			}
			if schedule != "consume-first" {
				consumeErr = admission.BeginConsume(ctx)
			}
			if schedule != "abandon-first" {
				errorChild = child.Wait()
			}
			abandoned := errorChild == nil
			if status, ok := errors.AsType[*exec.ExitError](errorChild); errorChild != nil && (!ok || status.ExitCode() != 42) {
				test.Fatal("child did not exit with bounded result")
			}
			if ctx.Err() != nil || (consumeErr == nil) == abandoned {
				test.Fatal("two processes both advanced or neither won")
			}
			if schedule == "consume-first" && consumeErr != nil || schedule == "abandon-first" && !abandoned {
				test.Fatal("controlled schedule did not reach intended winner")
			}
			if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("race wrote config")
			}
		})
	}
}

func TestCodexManagedRestoreDependencyPartialRecovery(test *testing.T) {
	for _, mode := range []string{"cancel", "child-crash"} {
		test.Run(mode, func(test *testing.T) {
			catalog := &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "synthetic-model", SupportsParallelToolCalls: new(true)}}}
			engine, plan, home, stateRoot, key := codexManagedCatalogPlanFixture(test, catalog)
			admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
			if err != nil {
				test.Fatal("confirm")
			}
			defer admission.Destroy()
			constraints := admission.CopyAuthorizationConstraints()
			defer constraints.Destroy()
			if admission.BeginAuthorize(test.Context(), constraints) != nil || admission.BeginConsume(test.Context()) != nil {
				test.Fatal("attempts")
			}
			secret, err := codexconfig.NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("SYNTHETIC_PARTIAL_RESTORE"))
			if err != nil {
				test.Fatal("secret")
			}
			defer secret.Destroy()
			candidate, err := engine.MaterializeCodex(test.Context(), admission, []codexconfig.SelectedSecret{secret})
			if err != nil {
				test.Fatal("render")
			}
			defer candidate.Destroy()
			if result, err := engine.ApplyCodex(test.Context(), admission, candidate); err != nil || result.State != CodexCompleted {
				test.Fatal("first apply")
			}
			if mode == "cancel" {
				restore, err := engine.PreviewCodexRestore(test.Context(), plan.View().OperationID)
				if err != nil {
					test.Fatal("restore preview")
				}
				defer restore.Destroy()
				confirmed, err := restore.Confirm(test.Context(), true)
				if err != nil {
					test.Fatal("restore confirm")
				}
				defer confirmed.Destroy()
				ctx, cancel := context.WithCancel(test.Context())
				defer cancel()
				visited := false
				engine.private().fault = func(stage string) error {
					if stage == "directory-synced-0" {
						visited = true
						cancel()
					}
					return nil
				}
				result, err := engine.RestoreCodex(ctx, confirmed)
				engine.private().fault = nil
				if !visited || err == nil || result.State == CodexRestored || result.State == CodexCompleted || result.SelectionRevision != 1 {
					test.Fatal("config-first cancellation falsely restored")
				}
			} else {
				if engine.Close() != nil {
					test.Fatal("close before child")
				}
				ctx, cancel := context.WithTimeout(test.Context(), 25*time.Second)
				defer cancel()
				child := codexManagedChildCommand(test, ctx, "restore-crash", home, stateRoot, plan.View().OperationID, "")
				status, ok := errors.AsType[*exec.ExitError](child.Run())
				if !ok || status.ExitCode() != 73 || ctx.Err() != nil {
					test.Fatal("real restore child did not join at config-first boundary")
				}
			}
			if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("config-first partial lost physical truth")
			}
			if _, err := os.Stat(filepath.Join(home, ".codex", "cognate-model-catalog.json")); err != nil {
				test.Fatal("partial prematurely removed catalog")
			}
			_ = engine.Close()
			reopened, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("partial reopen")
			}
			defer reopened.Close()
			results, err := reopened.ReconcileCodex(test.Context())
			if err != nil {
				test.Fatal("partial status reconciliation")
			}
			var partialID string
			for _, result := range results {
				if result.State == CodexReconcileRequired {
					partialID = result.OperationID
				}
			}
			if partialID == "" {
				test.Fatal("partial status was not reconcile-required")
			}
			recovery, err := reopened.PreviewCodexRecovery(test.Context(), partialID)
			if err != nil {
				test.Fatal("partial recovery preview")
			}
			defer recovery.Destroy()
			confirmed, err := recovery.Confirm(test.Context(), true)
			if err != nil {
				test.Fatal("partial recovery confirm")
			}
			defer confirmed.Destroy()
			result, err := reopened.RecoverCodex(test.Context(), confirmed)
			if err != nil || result.State != CodexRestored || result.SelectionRevision != 2 {
				test.Fatal("fresh explicit recovery did not finish restoration")
			}
			for _, name := range []string{"config.toml", "cognate-model-catalog.json"} {
				if _, err := os.Stat(filepath.Join(home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("recovery did not restore exact missing Before")
				}
			}
		})
	}
}

func TestCodexManagedCrashTombstones(test *testing.T) {
	for _, mode := range []string{"abandon-crash", "cleanup-crash", "retire-crash"} {
		test.Run(mode, func(test *testing.T) {
			engine, plan, home, stateRoot, key := codexManagedPlanFixture(test)
			admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
			if err != nil {
				test.Fatal("confirm")
			}
			defer admission.Destroy()
			if mode == "retire-crash" {
				constraints := admission.CopyAuthorizationConstraints()
				defer constraints.Destroy()
				if admission.BeginAuthorize(test.Context(), constraints) != nil || admission.BeginConsume(test.Context()) != nil {
					test.Fatal("attempt fixture")
				}
			}
			if engine.Close() != nil {
				test.Fatal("parent close")
			}
			ctx, cancel := context.WithTimeout(test.Context(), 20*time.Second)
			defer cancel()
			child := codexManagedChildCommand(test, ctx, mode, home, stateRoot, plan.View().OperationID, "")
			status, ok := errors.AsType[*exec.ExitError](child.Run())
			if !ok || status.ExitCode() != 73 || ctx.Err() != nil {
				test.Fatal("real child did not join at tombstone boundary")
			}
			reopened, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("reopen")
			}
			defer reopened.Close()
			result, err := reopened.StatusCodex(test.Context(), plan.View().OperationID)
			if err != nil {
				test.Fatal("terminal recovery")
			}
			if mode == "retire-crash" {
				if result.State != CodexRetiredUnpublished || !result.RemoteOutcomeUnknown {
					test.Fatal("unknown retirement lost")
				}
			} else if result.State != CodexAbandonedBeforeWrite {
				test.Fatal("abandon lost")
			}
			_, err = os.Stat(filepath.Join(home, ".codex", "codex-"+plan.View().OperationID+".reserve"))
			if mode == "retire-crash" && err != nil || mode != "retire-crash" && !errors.Is(err, os.ErrNotExist) {
				test.Fatal("reservation ownership cleanup mismatch")
			}
		})
	}
}
