//go:build linux

package configapply

import (
	"errors"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func TestNativeSelectionRejectsImplicitAndUnsupportedAuthorization(test *testing.T) {
	base := nativeTestSelection(test).private().SelectionInput
	for _, scenario := range []struct {
		name   string
		mutate func(*SelectionInput)
		want   error
	}{
		{"not-selected", func(input *SelectionInput) { input.Selected = false }, ErrNotSelected},
		{"project", func(input *SelectionInput) { input.Scope = "project" }, ErrSelection},
		{"override", func(input *SelectionInput) { input.ConfigRootOverride = true }, ErrSelection},
		{"auto-not-authorized", func(input *SelectionInput) { input.AutoApply = true }, ErrSelection},
		{"http", func(input *SelectionInput) { input.Origin = "http://example.invalid" }, ErrSelection},
		{"duplicate-capability", func(input *SelectionInput) {
			input.RequiredCapabilities = append(input.RequiredCapabilities, agentconfig.ClaudeProviderSwitchV1)
		}, ErrSelection},
		{"missing-server-deadline", func(input *SelectionInput) { input.PreparationExpiresAt = time.Time{} }, ErrSelection},
		{"unknown-capability", func(input *SelectionInput) { input.RequiredCapabilities = []agentconfig.Capability{"future"} }, ErrSelection},
		{"unknown-target", func(input *SelectionInput) { input.Patch.Target = "other" }, ErrSelection},
		{"duplicate-slot", func(input *SelectionInput) { input.Patch.Operations[1] = input.Patch.Operations[0] }, ErrSelection},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			input := cloneSelection(base)
			scenario.mutate(&input)
			if _, err := NewSelection(input); !errors.Is(err, scenario.want) {
				test.Fatalf("authorization boundary: %v", err)
			}
		})
	}
}

func TestNativeSelectionAndPlanDeepCopy(test *testing.T) {
	engine, _, _, _ := nativeTestFixture(test)
	input := cloneSelection(nativeTestSelection(test).private().SelectionInput)
	input.Pack = &PackBinding{ID: "pack-one", Revision: 2, AttachmentRevision: 3}
	deadline := time.Now().Add(time.Minute)
	input.NativeSessionExpiresAt = &deadline
	selection, err := NewSelection(input)
	if err != nil {
		test.Fatal(err)
	}
	*input.Patch.Operations[3].Literal = "caller-mutation"
	input.Adapter.Manifest.Capabilities[0] = "caller-mutation"
	input.RequiredCapabilities[0] = "caller-mutation"
	input.Pack.Revision = 99
	*input.NativeSessionExpiresAt = time.Time{}
	plan, err := engine.Preview(test.Context(), selection)
	if err != nil {
		test.Fatal(err)
	}
	if *plan.private().data.Selection.Patch.Operations[3].Literal != "new-model" || plan.private().data.Selection.Pack.Revision != 2 || plan.private().data.Selection.NativeSessionExpiresAt.IsZero() {
		test.Fatal("caller mutated opaque selection")
	}
	confirmed := plan.Confirm()
	*confirmed.private().data.Selection.Patch.Operations[3].Literal = "changed"
	if *plan.private().data.Selection.Patch.Operations[3].Literal != "new-model" {
		test.Fatal("confirmation aliases original")
	}
	slots := plan.RequiredSlots()
	slots[0] = "changed"
	if plan.RequiredSlots()[0] != agentconfig.ClaudeAPIKeySecret {
		test.Fatal("getter aliases private plan")
	}
}
