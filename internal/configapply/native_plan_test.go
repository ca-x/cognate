//go:build linux

package configapply

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func TestNativePlanZeroValueGetters(test *testing.T) {
	for name, plan := range map[string]*Plan{"nil": nil, "zero": {}} {
		test.Run(name, func(test *testing.T) {
			checks := map[string]func() bool{
				"Confirm":                func() bool { return plan.Confirm() == nil },
				"Commitment":             func() bool { return plan.Commitment() == [32]byte{} },
				"OperationID":            func() bool { return plan.OperationID() == "" },
				"ExpiresAt":              func() bool { return plan.ExpiresAt().IsZero() },
				"PreparationExpiresAt":   func() bool { return plan.PreparationExpiresAt().IsZero() },
				"PreparationPreparedAt":  func() bool { return plan.PreparationPreparedAt().IsZero() },
				"PreparationID":          func() bool { return plan.PreparationID() == "" },
				"SelectionRevision":      func() bool { return plan.SelectionRevision() == 0 },
				"ConfirmationRevision":   func() bool { return plan.ConfirmationRevision() == 0 },
				"PolicyRevision":         func() bool { return plan.PolicyRevision() == 0 },
				"PolicyID":               func() bool { return plan.PolicyID() == "" },
				"RequiredSlots":          func() bool { return plan.RequiredSlots() == nil },
				"View":                   func() bool { return reflect.DeepEqual(plan.View(), SafePlan{}) },
				"NativeSessionExpiresAt": func() bool { expiry, present := plan.NativeSessionExpiresAt(); return expiry.IsZero() && !present },
			}
			for getter, check := range checks {
				test.Run(getter, func(test *testing.T) {
					defer func() {
						if recover() != nil {
							test.Error("zero-value getter panicked")
						}
					}()
					if !check() {
						test.Error("zero-value getter returned nonzero result")
					}
				})
			}
		})
	}
}

func TestNativeExpiryAtPreparedBeforeReservation(test *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, present := range []bool{false, true} {
			for _, rollback := range []bool{false, true} {
				name := map[bool]string{false: "apply", true: "restore"}[restore] + map[bool]string{false: "-absent", true: "-existing"}[present] + map[bool]string{false: "-expired", true: "-clock-rollback"}[rollback]
				test.Run(name, func(test *testing.T) {
					engine, home, _, _ := nativeTestFixture(test)
					target := filepath.Join(home, ".claude", "settings.json")
					if present {
						if err := os.WriteFile(target, []byte(`{"CUSTOM":"preserved"}`), 0o600); err != nil {
							test.Fatal("private target fixture")
						}
					}
					current := time.Now().UTC()
					engine.private().now = func() time.Time { return current }
					plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
					if err != nil {
						test.Fatal("preview fixture")
					}
					secret, _ := NewSecretMaterial([]byte("expiry-fixture-material"))
					defer secret.Destroy()
					material := map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret}
					if restore {
						if _, err := engine.Apply(test.Context(), plan.Confirm(), material); err != nil {
							test.Fatal("source apply fixture")
						}
						plan, err = engine.PreviewRestore(test.Context(), plan.OperationID())
						if err != nil {
							test.Fatal("restore preview fixture")
						}
					}
					before, beforeError := os.ReadFile(target)
					reached, reserved := false, false
					engine.private().fault = func(boundary string) error {
						if boundary == "prepared-durable" {
							reached = true
							current = plan.ExpiresAt()
							if rollback {
								current = plan.private().data.PreviewedAt.Add(-time.Nanosecond)
							}
						}
						reserved = reserved || boundary == "reserve-intent-durable" || boundary == "publish-intent-durable"
						return nil
					}
					var result SafeResult
					if restore {
						result, err = engine.Restore(test.Context(), plan.Confirm())
					} else {
						result, err = engine.Apply(test.Context(), plan.Confirm(), material)
					}
					after, afterError := os.ReadFile(target)
					if !reached || reserved || !errors.Is(err, ErrPlanExpired) || result.Status == "OutcomeUnknown" || result.Status == "AppliedOnDisk" || result.Status == "Restored" || !bytes.Equal(before, after) || os.IsNotExist(beforeError) != os.IsNotExist(afterError) {
						test.Fatal("expired pre-reservation intent changed target or crossed reservation")
					}
				})
			}
		}
	}
}

func TestNativeExpiryAfterReservationRemainsUnknown(test *testing.T) {
	for _, present := range []bool{false, true} {
		boundaries := []string{"publish-intent-durable"}
		if present {
			boundaries = append(boundaries, "reserve-intent-durable", "entry-captured", "capture-synced")
		}
		for _, boundary := range boundaries {
			test.Run(map[bool]string{false: "absent-", true: "existing-"}[present]+boundary, func(test *testing.T) {
				engine, home, stateRoot, key := nativeTestFixture(test)
				target := filepath.Join(home, ".claude", "settings.json")
				if present {
					if err := os.WriteFile(target, []byte(`{"CUSTOM":"unpublished"}`), 0o600); err != nil {
						test.Fatal("private target fixture")
					}
				}
				before, beforeError := os.ReadFile(target)
				current := time.Now().UTC()
				engine.private().now = func() time.Time { return current }
				plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
				if err != nil {
					test.Fatal("preview fixture")
				}
				reached := false
				engine.private().fault = func(phase string) error {
					if phase == boundary {
						reached = true
						current = plan.ExpiresAt()
					}
					return nil
				}
				secret, _ := NewSecretMaterial([]byte("expiry-fixture-material"))
				defer secret.Destroy()
				result, err := engine.Apply(test.Context(), plan.Confirm(), map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret})
				if !reached || !errors.Is(err, ErrOutcomeUnknown) || errors.Is(err, ErrPlanExpired) || result.Status != "OutcomeUnknown" {
					test.Fatal("post-reservation expiry was not an unknown outcome")
				}
				if err := engine.Close(); err != nil {
					test.Fatal("close before expired recovery")
				}
				reopened, err := Open(test.Context(), home, stateRoot, key)
				if err != nil {
					test.Fatal("reopen expired operation")
				}
				defer reopened.Close()
				reopened.private().now = func() time.Time { return current }
				if _, err := reopened.Reconcile(test.Context()); err != nil {
					test.Fatal("expired recovery")
				}
				after, afterError := os.ReadFile(target)
				status, statusError := reopened.Status(test.Context(), plan.OperationID())
				if statusError != nil || status.Status == "AppliedOnDisk" || afterError == nil && !bytes.Equal(before, after) || os.IsNotExist(beforeError) && !os.IsNotExist(afterError) {
					test.Fatal("expired unpublished intent was forwarded during recovery")
				}
				if present && os.IsNotExist(afterError) {
					entries, err := os.ReadDir(filepath.Dir(target))
					if err != nil || status.Status != "NeedsAttention" {
						test.Fatal("absent target not reported as needing attention")
					}
					preserved := false
					for _, entry := range entries {
						if entry.IsDir() && strings.HasPrefix(entry.Name(), "cognate-") {
							holding, err := os.ReadFile(filepath.Join(filepath.Dir(target), entry.Name(), "holding"))
							preserved = preserved || err == nil && bytes.Equal(holding, before)
						}
					}
					if !preserved {
						test.Fatal("captured preimage was not preserved for recovery")
					}
				}
			})
		}
	}
}

func TestNativeExpiredRecoveryKeepsPublishedFact(test *testing.T) {
	for _, restore := range []bool{false, true} {
		test.Run(map[bool]string{false: "apply", true: "restore"}[restore], func(test *testing.T) {
			engine, home, stateRoot, key := nativeTestFixture(test)
			target := filepath.Join(home, ".claude", "settings.json")
			base := []byte(`{"CUSTOM":"restore-private-preimage"}`)
			if err := os.WriteFile(target, base, 0o600); err != nil {
				test.Fatal("private target fixture")
			}
			current := time.Now().UTC()
			engine.private().now = func() time.Time { return current }
			plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
			if err != nil {
				test.Fatal("preview fixture")
			}
			secret, _ := NewSecretMaterial([]byte("published-expiry-material"))
			defer secret.Destroy()
			material := map[agentconfig.ClaudeSecretSlot]SecretMaterial{agentconfig.ClaudeAPIKeySecret: secret}
			if restore {
				if _, err := engine.Apply(test.Context(), plan.Confirm(), material); err != nil {
					test.Fatal("source apply fixture")
				}
				plan, err = engine.PreviewRestore(test.Context(), plan.OperationID())
				if err != nil {
					test.Fatal("restore preview fixture")
				}
			}
			reached := false
			engine.private().fault = func(boundary string) error {
				if boundary == "entry-published" {
					reached = true
					current = plan.ExpiresAt().Add(time.Nanosecond)
					return errors.New("synthetic postpublication interruption")
				}
				return nil
			}
			var result SafeResult
			if restore {
				result, err = engine.Restore(test.Context(), plan.Confirm())
			} else {
				result, err = engine.Apply(test.Context(), plan.Confirm(), material)
			}
			if !reached || !errors.Is(err, ErrOutcomeUnknown) || result.Status != "OutcomeUnknown" {
				test.Fatal("postpublication interruption misreported as ordinary expiry")
			}
			published, err := os.ReadFile(target)
			if err != nil || restore && !bytes.Equal(published, base) || !restore && !bytes.Contains(published, []byte("published-expiry-material")) {
				test.Fatal("publication fixture did not reach its actual postimage")
			}
			identity, err := os.Stat(target)
			if err != nil || engine.Close() != nil {
				test.Fatal("close published fixture")
			}
			reopened, err := Open(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("reopen expired published operation")
			}
			defer reopened.Close()
			reopened.private().now = func() time.Time { return current }
			if _, err := reopened.Reconcile(test.Context()); err != nil {
				test.Fatal("reconcile published expired operation")
			}
			status, err := reopened.Status(test.Context(), plan.OperationID())
			if err != nil || status.Status != map[bool]string{false: "AppliedOnDisk", true: "Restored"}[restore] {
				test.Fatal("expiry hid the actual published fact")
			}
			after, err := os.ReadFile(target)
			afterIdentity, statError := os.Stat(target)
			if err != nil || statError != nil || !bytes.Equal(published, after) || !os.SameFile(identity, afterIdentity) {
				test.Fatal("expired recovery replayed or changed the published postimage")
			}
		})
	}
}

func TestNativePreparationAndSessionTTLBoundPlan(test *testing.T) {
	for _, withSession := range []bool{false, true} {
		test.Run(map[bool]string{false: "preparation", true: "session"}[withSession], func(test *testing.T) {
			engine, _, _, _ := nativeTestFixture(test)
			now := time.Now().UTC()
			input := cloneSelection(nativeTestSelection(test).private().SelectionInput)
			input.PreparationPreparedAt = now.Add(-4 * time.Minute)
			input.PreparationExpiresAt = now.Add(time.Minute)
			expected := input.PreparationExpiresAt
			if withSession {
				expected = now.Add(30 * time.Second)
				input.NativeSessionExpiresAt = new(expected)
			}
			selection, err := NewSelection(input)
			if err != nil {
				test.Fatal(err)
			}
			engine.private().now = func() time.Time { return now }
			plan, err := engine.Preview(test.Context(), selection)
			if err != nil || !plan.ExpiresAt().Equal(expected) || !plan.PreparationExpiresAt().Equal(input.PreparationExpiresAt) {
				test.Fatal("preview reopened server lifetime")
			}
			engine.private().now = func() time.Time { return expected }
			if _, err := engine.Apply(test.Context(), plan.Confirm(), nil); !errors.Is(err, ErrPlanExpired) {
				test.Fatal("expired plan reached materialization")
			}
		})
	}
}

func TestNativeCanonicalCommitmentBindsAllIntentDimensions(test *testing.T) {
	engine, _, _, _ := nativeTestFixture(test)
	plan, err := engine.Preview(test.Context(), nativeTestSelection(test))
	if err != nil {
		test.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		mutate func(*Plan)
	}{
		{"origin", func(plan *Plan) { plan.private().data.Selection.Origin += "x" }},
		{"owner", func(plan *Plan) { plan.private().data.Selection.Owner += "x" }},
		{"profile", func(plan *Plan) { plan.private().data.Selection.Profile += "x" }},
		{"device", func(plan *Plan) { plan.private().data.Selection.DeviceID += "x" }},
		{"asset", func(plan *Plan) { plan.private().data.Selection.AssetID += "x" }},
		{"schema", func(plan *Plan) { plan.private().data.Selection.SchemaVersion++ }},
		{"revision", func(plan *Plan) { plan.private().data.Selection.AssetRevision++ }},
		{"adapter-api", func(plan *Plan) { plan.private().data.Selection.Adapter.Manifest.APIVersion++ }},
		{"adapter-version", func(plan *Plan) { plan.private().data.Selection.Adapter.Manifest.PluginVersion += "x" }},
		{"adapter-digest", func(plan *Plan) { plan.private().data.Selection.Adapter.DefinitionDigest += "x" }},
		{"capabilities", func(plan *Plan) { plan.private().data.Selection.RequiredCapabilities = nil }},
		{"selection", func(plan *Plan) { plan.private().data.Selection.SelectionRevision++ }},
		{"confirmation", func(plan *Plan) { plan.private().data.Selection.ConfirmationRevision++ }},
		{"policy", func(plan *Plan) { plan.private().data.Selection.PolicyRevision++ }},
		{"policy-id", func(plan *Plan) { plan.private().data.Selection.PolicyID = "other-policy" }},
		{"preview-time", func(plan *Plan) { plan.private().data.PreviewedAt = plan.private().data.PreviewedAt.Add(time.Second) }},
		{"restore-source-commitment", func(plan *Plan) { plan.private().data.SourceCommitment[0] ^= 1 }},
		{"scope", func(plan *Plan) { plan.private().data.Selection.Scope = "workspace" }},
		{"parent", func(plan *Plan) { plan.private().data.Parent.Inode++ }},
		{"base-presence", func(plan *Plan) { plan.private().data.Base.Present = !plan.private().data.Base.Present }},
		{"operation", func(plan *Plan) { plan.private().data.Selection.OperationID += "x" }},
		{"preparation", func(plan *Plan) { plan.private().data.Selection.PreparationID += "x" }},
		{"prepared-at", func(plan *Plan) {
			plan.private().data.Selection.PreparationPreparedAt = plan.private().data.Selection.PreparationPreparedAt.Add(time.Second)
		}},
		{"preparation-expiry", func(plan *Plan) {
			plan.private().data.Selection.PreparationExpiresAt = plan.private().data.Selection.PreparationExpiresAt.Add(time.Second)
		}},
		{"session-presence", func(plan *Plan) {
			plan.private().data.Selection.NativeSessionExpiresAt = new(plan.private().data.ExpiresAt)
		}},
		{"pack-presence", func(plan *Plan) {
			plan.private().data.Selection.Pack = &PackBinding{ID: "pack", Revision: 1, AttachmentRevision: 1}
		}},
		{"managed-action", func(plan *Plan) { plan.private().data.Selection.Patch.Operations[0].Action = "remove" }},
		{"literal", func(plan *Plan) { *plan.private().data.Selection.Patch.Operations[3].Literal = "changed" }},
		{"secret-ref", func(plan *Plan) { *plan.private().data.Selection.Patch.Operations[0].Reference = "changed" }},
		{"expiry", func(plan *Plan) { plan.private().data.ExpiresAt = plan.private().data.ExpiresAt.Add(time.Second) }},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			changed := plan.Confirm()
			scenario.mutate(changed)
			if nativeMAC(engine.private().keys.intent, nativePlanBytes(changed.private().data)) == plan.Commitment() {
				test.Fatal("unbound canonical intent field")
			}
			if _, err := engine.Apply(test.Context(), changed, nil); !errors.Is(err, ErrIdempotency) {
				test.Fatalf("mutable plan accepted: %v", err)
			}
		})
	}
}

func TestNativeClockRollbackRefusesPlan(test *testing.T) {
	engine, _, _, _ := nativeTestFixture(test)
	now := time.Now().UTC()
	input := cloneSelection(nativeTestSelection(test).private().SelectionInput)
	input.PreparationPreparedAt = now.Add(-time.Minute)
	selection, err := NewSelection(input)
	if err != nil {
		test.Fatal(err)
	}
	engine.private().now = func() time.Time { return now }
	plan, err := engine.Preview(test.Context(), selection)
	if err != nil {
		test.Fatal(err)
	}
	engine.private().now = func() time.Time { return now.Add(-time.Second) }
	if _, err := engine.Apply(test.Context(), plan.Confirm(), nil); !errors.Is(err, ErrPlanExpired) {
		test.Fatal("rollback extended a confirmed plan")
	}
}
