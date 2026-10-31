package configapply

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
)

func TestCodexD2ActiveIdentityAndClosedRegistry(test *testing.T) {
	id, manifest, profile, template := codexconfig.Native0166EffectiveManifest()
	want := CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}
	if codexNative0166Identity() != want {
		test.Fatal("new producer does not select the exact effective-v2 tuple")
	}
	before := codexQualifiedProfiles
	test.Cleanup(func() { codexQualifiedProfiles = before })
	oldID, oldManifest, oldProfile, oldTemplate := codexconfig.Native0166Manifest()
	old := CodexProfileIdentity{ID: oldID, ManifestSHA256: oldManifest, ProfileDigest: oldProfile, TemplateDigest: oldTemplate}
	mixed := want
	mixed.TemplateDigest[0] ^= 1
	for _, registry := range [][]CodexProfileIdentity{nil, {old}, {mixed}, {{ID: id}}} {
		codexQualifiedProfiles = registry
		if result, err := QualifiedCodexNativeProfile(); !errors.Is(err, ErrCodexUnsupported) || result != (CodexProfileIdentity{}) {
			test.Fatal("inactive, mixed or partial tuple acquired new admission authority")
		}
	}
	codexQualifiedProfiles = []CodexProfileIdentity{want}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		if result, err := QualifiedCodexNativeProfile(); err != nil || result != want {
			test.Fatal("exact private qualification refused")
		}
	}
}

func TestCodexD2RecordedProfile(test *testing.T) {
	engine, _, stateRoot, _ := codexFixture(test)
	bodies := [3][]byte{[]byte("synthetic-config"), nil, []byte(`{"models":[]}`)}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	codexStageLiteral(test, engine, operation, bodies)
	anchor, err := os.ReadFile(filepath.Join(stateRoot, codexAnchorName))
	if err != nil {
		test.Fatal("anchor baseline")
	}
	identity, err := engine.RecordedCodexProfile(test.Context(), operation.ID)
	if err != nil || identity != (CodexProfileIdentity{}) {
		test.Fatal("legacy record fact is not the whole zero tuple")
	}
	identity.ID = "caller-mutated"
	again, err := engine.RecordedCodexProfile(test.Context(), operation.ID)
	if err != nil || again != (CodexProfileIdentity{}) {
		test.Fatal("accessor aliases inventory")
	}
	for _, id := range []string{"invalid", codexRandomID()} {
		if value, err := engine.RecordedCodexProfile(test.Context(), id); !errors.Is(err, ErrCodexInvalid) || value != (CodexProfileIdentity{}) {
			test.Fatal("missing operation returned identity fact")
		}
	}
	canceled, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := engine.RecordedCodexProfile(canceled, operation.ID); !errors.Is(err, context.Canceled) {
		test.Fatal("accessor ignored cancellation")
	}
	after, err := os.ReadFile(filepath.Join(stateRoot, codexAnchorName))
	if err != nil || !bytes.Equal(anchor, after) {
		test.Fatal("fact accessor rewrote quiescent inventory")
	}
	if engine.Close() != nil {
		test.Fatal("engine close")
	}
	if value, err := engine.RecordedCodexProfile(test.Context(), operation.ID); !errors.Is(err, ErrCodexClosed) || value != (CodexProfileIdentity{}) {
		test.Fatal("closed engine returned identity fact")
	}
}

func TestCodexD2OwnershipEvidence(test *testing.T) {
	for _, kind := range []string{"proven-v1", "metadata0", "lifecycle0", "legacy", "target", "after", "selection"} {
		test.Run(kind, func(test *testing.T) {
			catalog := &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "synthetic-model", SupportsParallelToolCalls: new(true)}}}
			engine, plan, home, _, _ := codexManagedCatalogPlanFixture(test, catalog)
			admission, err := engine.ConfirmCodexForConsumer(test.Context(), plan, plan.View().RequiredSlots, true)
			if err != nil {
				test.Fatal("first confirm")
			}
			defer admission.Destroy()
			constraints := admission.CopyAuthorizationConstraints()
			defer constraints.Destroy()
			if admission.BeginAuthorize(test.Context(), constraints) != nil || admission.BeginConsume(test.Context()) != nil {
				test.Fatal("first durable attempts")
			}
			secret, err := codexconfig.NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("SYNTHETIC_D2_OWNED"))
			if err != nil {
				test.Fatal("selected material")
			}
			defer secret.Destroy()
			candidate, err := engine.MaterializeCodex(test.Context(), admission, []codexconfig.SelectedSecret{secret})
			if err != nil {
				test.Fatal("first materialize")
			}
			defer candidate.Destroy()
			if _, err := engine.ApplyCodex(test.Context(), admission, candidate); err != nil {
				test.Fatal("first publish")
			}
			if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
				row := engine.private().inventory.Operations[plan.View().OperationID]
				id, manifest, profile, template := codexconfig.Native0166Manifest()
				row.ProfileIdentity = CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}
				switch kind {
				case "metadata0":
					row.ManagedModelsVersion = 0
				case "lifecycle0", "legacy":
					row.LifecycleVersion, row.ManagedModelsVersion, row.Authorization = 0, 0, nil
					if kind == "legacy" {
						row.Observation, row.ProfileIdentity = nil, CodexProfileIdentity{}
					}
				case "target":
					row.Target.Inode++
				case "after":
					row.After[0].SHA256[0] ^= 1
				case "selection":
					key := codexSelectionKey(row.Origin, row.Owner, row.Profile)
					current := engine.private().inventory.Selections[key]
					current.Operation = ""
					engine.private().inventory.Selections[key] = current
				}
				return engine.private().saveCodexInventory()
			}); err != nil {
				test.Fatal("historical authenticated fixture")
			}
			current, err := engine.CurrentCodexSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
			if err != nil {
				test.Fatal("new current")
			}
			defer current.Destroy()
			input := plan.get[codexPlanData]().selection
			prepared := input.prepared
			prepared.OperationId = bytes.Repeat([]byte{0x52}, 16)
			prepared.SelectionRevision, err = current.NextSelectionRevision()
			if err != nil {
				test.Fatal("next revision")
			}
			selection, err := NewCodexSelection(prepared, input.declaration, input.expires, current)
			if err != nil {
				test.Fatal("fresh selection")
			}
			defer selection.Destroy()
			launch := CodexLaunch{Executable: os.Getenv("COGNATE_Q2_NATIVE_FIXTURE"), Directory: filepath.Join(filepath.Dir(home), "workspace"), Arguments: []string{"app-server"}}
			observed, err := engine.ObserveCodex(test.Context(), selection, launch)
			if err != nil {
				test.Fatal("fresh observation")
			}
			defer observed.Destroy()
			next, err := engine.PreviewCodexObserved(test.Context(), selection, observed)
			if kind != "proven-v1" && errors.Is(err, ErrCodexAttention) && next.get[codexPlanData]() == nil {
				return
			}
			if err != nil {
				test.Fatal("fresh v2 preview", err)
			}
			defer next.Destroy()
			binding := next.get[codexPlanData]().provider.Binding()
			defer binding.Destroy()
			facts := binding.CopyObservation()
			want := codexconfig.Foreign
			if kind == "proven-v1" {
				want = codexconfig.Managed
			}
			if facts.ProviderOwnership != want || facts.CatalogOwnership != want || next.get[codexPlanData]().profile != codexNative0166Identity() {
				test.Fatal("cross-profile ownership lacks completed/current/after/lifecycle1/metadata1 proof")
			}
		})
	}
}

func TestCodexOriginalDeadlineAfterFreshObservation(test *testing.T) {
	for _, phase := range []string{"observe", "preview", "confirm", "revalidate", "materialize"} {
		for _, cutoff := range []string{"prepare", "child", "observation", "plan", "admission", "unchanged"} {
			if cutoff == "observation" && phase != "preview" && phase != "materialize" || cutoff == "plan" && phase != "confirm" && phase != "revalidate" && phase != "materialize" || (cutoff == "admission" || cutoff == "unchanged") && phase != "materialize" {
				continue
			}
			test.Run(phase+"/"+cutoff, func(test *testing.T) {
				engine, home, _, _ := codexFixture(test)
				launch := codexNativeLaunchFixture(test, home)
				previousProfiles := codexQualifiedProfiles
				codexQualifiedProfiles = []CodexProfileIdentity{codexNative0166Identity()}
				test.Cleanup(func() { codexQualifiedProfiles = previousProfiles })
				initial := engine.private().now()
				engine.private().now = func() time.Time { return initial }
				deadline := initial.Add(30 * time.Second).Truncate(time.Millisecond)
				template := codexShapeSelection(test, engine)
				defer template.Destroy()
				input := template.get[codexSelectionData]()
				childExpiry := initial.Add(time.Minute)
				if cutoff == "prepare" {
					input.prepared.PrepareExpiresAtUnixMs = deadline.UnixMilli()
				} else if cutoff == "child" {
					childExpiry = deadline
				}
				current, err := engine.CurrentCodexSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
				if err != nil {
					test.Fatal("deadline current selection fixture refused")
				}
				defer current.Destroy()
				selection, err := NewCodexSelection(input.prepared, input.declaration, childExpiry, current)
				if err != nil {
					test.Fatal("deadline preparation fixture refused")
				}
				defer selection.Destroy()
				selectionExpiry := selection.get[codexSelectionData]().expires
				prepareExpiry := selection.get[codexSelectionData]().prepared.PrepareExpiresAtUnixMs
				visited := false
				arm := func() {
					if cutoff == "admission" {
						engine.private().now = func() time.Time {
							if engine.private().guard == nil {
								return deadline
							}
							return initial
						}
					}
					engine.private().fault = func(stage string) error {
						if stage == "observation-config-started" {
							visited = true
							if cutoff != "unchanged" && cutoff != "admission" {
								engine.private().now = func() time.Time { return deadline }
							}
						}
						return nil
					}
				}
				defer func() {
					if !visited {
						test.Fatal("real native observation barrier was not reached")
					}
					if !selection.get[codexSelectionData]().expires.Equal(selectionExpiry) || selection.get[codexSelectionData]().prepared.PrepareExpiresAtUnixMs != prepareExpiry {
						test.Fatal("original child or preparation deadline was renewed")
					}
				}()
				if phase == "observe" {
					arm()
				}
				observed, err := engine.ObserveCodex(test.Context(), selection, launch)
				defer observed.Destroy()
				if phase == "observe" {
					if !errors.Is(err, ErrCodexExpired) || observed.get[codexObservationData]() != nil {
						test.Fatal("slow observation renewed original selection deadline")
					}
					return
				}
				if err != nil {
					test.Fatal("deadline observation fixture refused")
				}
				if cutoff == "observation" {
					observed.get[codexObservationData]().expires = deadline
				}
				if phase == "preview" {
					arm()
				}
				plan, err := engine.PreviewCodexObserved(test.Context(), selection, observed)
				defer plan.Destroy()
				if phase == "preview" {
					if !errors.Is(err, ErrCodexExpired) || plan.get[codexPlanData]() != nil {
						test.Fatal("slow preview renewed original observation deadline")
					}
					return
				}
				if err != nil {
					test.Fatal("deadline plan fixture refused")
				}
				if cutoff == "plan" {
					plan.get[codexPlanData]().view.ExpiresAt = deadline
				}
				originalPlanExpiry := plan.View().ExpiresAt
				if cutoff == "admission" {
					deadline = originalPlanExpiry
				}
				defer func() {
					if !plan.View().ExpiresAt.Equal(originalPlanExpiry) {
						test.Fatal("original plan deadline was renewed")
					}
				}()
				if phase == "confirm" {
					arm()
				}
				admission, err := engine.ConfirmCodex(test.Context(), plan, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, true)
				defer admission.Destroy()
				if phase == "confirm" {
					if !errors.Is(err, ErrCodexExpired) || admission.get[codexAdmissionData]() != nil || len(engine.private().inventory.Operations) != 0 {
						test.Fatal("slow confirmation reserved expired plan")
					}
					return
				}
				if err != nil {
					test.Fatal("deadline admission fixture refused")
				}
				arm()
				if phase == "revalidate" {
					if err := admission.Revalidate(test.Context()); !errors.Is(err, ErrCodexExpired) {
						test.Fatal("slow shared revalidation accepted expired plan")
					}
					return
				}
				secret, err := codexconfig.NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("SYNTHETIC_DEADLINE_MATERIAL"))
				if err != nil {
					test.Fatal("deadline selected material fixture refused")
				}
				defer secret.Destroy()
				candidate, err := engine.MaterializeCodex(test.Context(), admission, []codexconfig.SelectedSecret{secret})
				defer candidate.Destroy()
				if cutoff == "unchanged" {
					if err != nil || !admission.get[codexAdmissionData]().materialized || reflect.DeepEqual(candidate, codexconfig.ProviderCandidate{}) {
						test.Fatal("unchanged original deadline positive refused")
					}
				} else if !errors.Is(err, ErrCodexExpired) || admission.get[codexAdmissionData]().materialized || !reflect.DeepEqual(candidate, codexconfig.ProviderCandidate{}) {
					test.Fatal("expired plan admitted secret or returned nonzero candidate")
				}
			})
		}
	}
}

func TestCodexObservedFinalExpiryKeepsPublishedTruth(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	launch := codexNativeLaunchFixture(test, home)
	var descriptor codexLaunchDescriptor
	if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
		var err error
		descriptor, _, _, _, err = engine.private().captureCodexLaunch(ctx, launch)
		return err
	}); err != nil {
		test.Fatal("final expiry fixture capture failed")
	}
	bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), nil, []byte(`{"models":[]}`)}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	operation.ProfileIdentity = codexNative0166Identity()
	operation.Observation = &descriptor
	codexStageLiteral(test, engine, operation, bodies)
	visited := false
	originalClock := engine.private().now
	engine.private().fault = func(stage string) error {
		if stage == "targets-durable" {
			visited = true
			engine.private().now = func() time.Time { return originalClock().Add(time.Hour) }
		}
		return nil
	}
	result, err := codexPublishLiteral(test.Context(), engine, operation.ID)
	if !visited || !errors.Is(err, ErrCodexExpired) || result.State == CodexCompleted {
		test.Fatal("expired publication became Completed")
	}
	for _, index := range []int{0, 2} {
		actual, err := os.ReadFile(filepath.Join(home, ".codex", codexRoleNames[index]))
		if err != nil || !bytes.Equal(actual, bodies[index]) {
			test.Fatal("expiry misrepresented an already published role")
		}
	}
}

func TestCodexObservedRestoreSourceConflictKeepsDiskTruth(test *testing.T) {
	for _, phase := range []string{"before-publish-2", "targets-durable"} {
		test.Run(phase, func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			launch := codexNativeLaunchFixture(test, home)
			var descriptor codexLaunchDescriptor
			if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
				var err error
				descriptor, _, _, _, err = engine.private().captureCodexLaunch(ctx, launch)
				return err
			}); err != nil {
				test.Fatal("restore source descriptor fixture failed")
			}
			bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), nil, []byte(`{"models":[]}`)}
			operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			operation.ProfileIdentity = codexNative0166Identity()
			operation.Observation = &descriptor
			codexStageLiteral(test, engine, operation, bodies)
			if result, err := codexPublishLiteral(test.Context(), engine, operation.ID); err != nil || result.State != CodexCompleted {
				test.Fatal("restore baseline publication failed")
			}
			plan, err := engine.PreviewCodexRestore(test.Context(), operation.ID)
			if err != nil {
				test.Fatal("restore preview refused unchanged source")
			}
			defer plan.Destroy()
			approved, err := plan.Confirm(test.Context(), true)
			if err != nil {
				test.Fatal("restore confirmation refused unchanged source")
			}
			defer approved.Destroy()
			visited := false
			engine.private().fault = func(stage string) error {
				if stage == phase {
					visited = true
					if err := os.Mkdir(filepath.Join(launch.Directory, ".codex"), 0o700); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join(launch.Directory, ".codex", "config.toml"), []byte("model_context_window=4096\n"), 0o600)
				}
				return nil
			}
			result, err := engine.RestoreCodex(test.Context(), approved)
			if !visited || !errors.Is(err, ErrCodexConflict) || result.State == CodexRestored || result.State == CodexCompleted {
				test.Fatal("changed source produced a false restore success")
			}
			assertTruth := func() {
				test.Helper()
				for _, index := range []int{0, 2} {
					actual, err := os.ReadFile(filepath.Join(home, ".codex", codexRoleNames[index]))
					if phase == "targets-durable" {
						if !errors.Is(err, os.ErrNotExist) {
							test.Fatal("late restore conflict concealed physical restoration")
						}
					} else if err != nil || !bytes.Equal(actual, bodies[index]) {
						test.Fatal("early restore conflict changed a role")
					}
				}
			}
			assertTruth()
			if engine.Close() != nil {
				test.Fatal("restore conflict Close failed")
			}
			fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("restore conflict restart failed")
			}
			defer fresh.Close()
			if _, err := fresh.PreviewCodexRecovery(test.Context(), result.OperationID); !errors.Is(err, ErrCodexConflict) {
				test.Fatal("restart authorized restore recovery with changed source")
			}
			assertTruth()
		})
	}
}

func TestCodexObservedPublisherFreshCheckpoints(test *testing.T) {
	for _, phase := range []string{"publisher-entry", "before-publish-0", "targets-durable"} {
		test.Run(phase, func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			launch := codexNativeLaunchFixture(test, home)
			var descriptor codexLaunchDescriptor
			if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
				var err error
				descriptor, _, _, _, err = engine.private().captureCodexLaunch(ctx, launch)
				return err
			}); err != nil {
				test.Fatal("checkpoint descriptor fixture failed")
			}
			bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), nil, []byte(`{"models":[]}`)}
			operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			operation.ProfileIdentity = codexNative0166Identity()
			operation.Observation = &descriptor
			codexStageLiteral(test, engine, operation, bodies)
			sourceDirectory := filepath.Join(launch.Directory, ".codex")
			mutate := func() error {
				if err := os.Mkdir(sourceDirectory, 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(sourceDirectory, "config.toml"), []byte("model_context_window=4096\n"), 0o600)
			}
			visited := phase == "publisher-entry"
			if visited {
				if err := mutate(); err != nil {
					test.Fatal("entry source mutation failed")
				}
			} else {
				engine.private().fault = func(stage string) error {
					if stage == phase {
						visited = true
						return mutate()
					}
					return nil
				}
			}
			result, err := codexPublishLiteral(test.Context(), engine, operation.ID)
			if !visited || !errors.Is(err, ErrCodexConflict) || result.State == CodexCompleted || result.State == CodexRestored {
				test.Fatal("publisher checkpoint did not refuse changed source")
			}
			if phase == "publisher-entry" && result.State != CodexCandidatesDurable {
				test.Fatal("publisher entry conflict advanced persisted phase")
			}
			engine.private().fault = nil
			assertTruth := func() {
				test.Helper()
				for _, index := range []int{0, 2} {
					actual, err := os.ReadFile(filepath.Join(home, ".codex", codexRoleNames[index]))
					published := phase == "targets-durable" || phase == "before-publish-0" && index == 2
					if published {
						if err != nil || !bytes.Equal(actual, bodies[index]) {
							test.Fatal("conflict concealed or rolled back an actual publication")
						}
					} else if !errors.Is(err, os.ErrNotExist) {
						test.Fatal("conflict published an unauthorized role")
					}
				}
			}
			assertTruth()
			if engine.Close() != nil {
				test.Fatal("checkpoint engine close failed")
			}
			fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("checkpoint restart failed")
			}
			defer fresh.Close()
			if _, err := fresh.PreviewCodexRecovery(test.Context(), operation.ID); !errors.Is(err, ErrCodexConflict) {
				test.Fatal("restart granted recovery with changed source")
			}
			assertTruth()
		})
	}
}

func TestCodexObservedLatePublicationSourceCAS(test *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "unchanged"
		if changed {
			name = "late-project-source"
		}
		test.Run(name, func(test *testing.T) {
			engine, home, _, _ := codexFixture(test)
			launch := codexNativeLaunchFixture(test, home)
			var descriptor codexLaunchDescriptor
			if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
				var err error
				descriptor, _, _, _, err = engine.private().captureCodexLaunch(ctx, launch)
				return err
			}); err != nil {
				test.Fatal("late publication observation fixture failed")
			}
			bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), nil, []byte(`{"models":[]}`)}
			operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			operation.ProfileIdentity = codexNative0166Identity()
			operation.Observation = &descriptor
			codexStageLiteral(test, engine, operation, bodies)
			visited := false
			engine.private().fault = func(stage string) error {
				if stage == "before-publish-2" {
					visited = true
					if changed {
						if err := os.Mkdir(filepath.Join(launch.Directory, ".codex"), 0o700); err != nil {
							return err
						}
						return os.WriteFile(filepath.Join(launch.Directory, ".codex", "config.toml"), []byte("model_context_window=4096\n"), 0o600)
					}
				}
				return nil
			}
			var result CodexResult
			err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
				if err := engine.private().validateCodexOperationObservation(ctx, operation); err != nil {
					return err
				}
				var err error
				result, err = engine.private().publishCodex(ctx, operation)
				return err
			})
			if !visited {
				test.Fatal("late publication checkpoint not reached")
			}
			if changed {
				if !errors.Is(err, ErrCodexConflict) || result.State == CodexCompleted {
					test.Fatal("late non-role source mutation accepted by publisher")
				}
				if _, err := os.Stat(filepath.Join(home, ".codex", codexRoleNames[2])); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("late source conflict advanced managed catalog")
				}
			} else if err != nil || result.State != CodexCompleted {
				test.Fatal("unchanged publication positive control refused")
			}
		})
	}
}

func TestCodexProductionProfileAndAdmissionRemainClosed(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	selection := codexShapeSelection(test, engine)
	plan, err := engine.PreviewCodex(test.Context(), selection)
	if !errors.Is(err, ErrCodexUnsupported) || plan.View().OperationID != "" {
		test.Fatal("unqualified profile produced trusted plan")
	}
	if len(codexQualifiedProfiles) != 0 {
		test.Fatal("D1 registered profile qualification")
	}
	if err := os.Remove(filepath.Join(home, ".codex")); err != nil {
		test.Fatal(err)
	}
	if _, err := engine.PreviewCodex(test.Context(), selection); !errors.Is(err, ErrCodexUnsupported) {
		test.Fatal("closed qualification read target before refusing")
	}
	for _, handle := range []CodexPlan{{}, {value: new(any(&codexPlanData{engine: engine.private(), selection: selection.get[codexSelectionData](), profile: CodexProfileIdentity{ID: "forged-profile"}}))}} {
		admission, err := engine.ConfirmCodex(test.Context(), handle, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, true)
		if err == nil || admission.CopyPreparedBinding() != nil {
			test.Fatal("forged plan manufactured admission")
		}
	}
	if _, err := engine.ApplyCodex(test.Context(), CodexAdmission{}, codexconfig.ProviderCandidate{}); err == nil {
		test.Fatal("zero admission applied")
	}
	if _, err := engine.MaterializeCodex(test.Context(), CodexAdmission{}, nil); err == nil {
		test.Fatal("zero admission materialized")
	}
	if err := (CodexAdmission{}).Revalidate(test.Context()); err == nil {
		test.Fatal("zero admission revalidated")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("unsupported preview created target")
	}
}

func TestCodexPublishIdentityAndAuthKeepBaseline(test *testing.T) {
	for _, mode := range []string{"same-hash-new-inode", "parent-replaced", "auth-same-hash-new-inode"} {
		test.Run(mode, func(test *testing.T) {
			engine, home, _, _ := codexFixture(test)
			for _, name := range []string{"config.toml", "auth.json"} {
				if err := os.WriteFile(filepath.Join(home, ".codex", name), []byte("original"), 0o600); err != nil {
					test.Fatal(err)
				}
			}
			bodies := [3][]byte{[]byte("new-config"), nil, []byte("new-catalog")}
			op := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			codexStageLiteral(test, engine, op, bodies)
			if mode == "parent-replaced" {
				if err := os.Rename(filepath.Join(home, ".codex"), filepath.Join(home, "displaced")); err != nil {
					test.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(home, ".codex"), 0o700); err != nil {
					test.Fatal(err)
				}
			} else {
				name := "config.toml"
				if mode == "auth-same-hash-new-inode" {
					name = "auth.json"
				}
				path := filepath.Join(home, ".codex", name)
				if err := os.Rename(path, path+".old"); err != nil {
					test.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
					test.Fatal(err)
				}
			}
			result, err := codexPublishLiteral(test.Context(), engine, op.ID)
			if !errors.Is(err, ErrCodexConflict) || result.State == CodexCompleted {
				test.Fatal("physical identity change accepted")
			}
			if mode != "parent-replaced" {
				actual, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
				if err != nil || string(actual) != "original" {
					test.Fatal("CAS failure advanced config")
				}
			}
		})
	}
}
