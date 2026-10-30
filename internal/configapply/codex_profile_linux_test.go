//go:build linux

package configapply

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
)

func TestCodexPhysicalLaunchObservation(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	launch := codexNativeLaunchFixture(test, home)
	err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
		descriptor, baseline, target, expires, err := engine.private().captureCodexLaunch(ctx, launch)
		if err != nil || descriptor.Version != 1 || !descriptor.Executable.Present || descriptor.Layers == [32]byte{} || target.Inode == 0 || baseline[1].Present || !expires.After(engine.private().now()) {
			test.Fatal("physical native source observation did not produce bounded engine-owned descriptor")
		}
		return nil
	})
	if err != nil {
		test.Fatal("observation fixture failed")
	}
}

func TestCodexLaunchOverridesAndUnknownSourceRefuse(test *testing.T) {
	for _, kind := range []string{"override", "environment", "project", "untrusted-executable"} {
		test.Run(kind, func(test *testing.T) {
			engine, home, _, _ := codexFixture(test)
			launch := codexNativeLaunchFixture(test, home)
			switch kind {
			case "override":
				launch.Arguments = append(launch.Arguments, "-c", "model_context_window=4096")
			case "environment":
				launch.Environment = []string{"CODEX_HOME=/foreign"}
			case "project":
				if os.Mkdir(filepath.Join(launch.Directory, ".codex"), 0o700) != nil || os.WriteFile(filepath.Join(launch.Directory, ".codex", "config.toml"), []byte("model_context_window=4096\n"), 0o600) != nil {
					test.Fatal("project fixture setup failed")
				}
			case "untrusted-executable":
				launch.Executable = "/usr/bin/true"
			}
			err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
				_, _, _, _, err := engine.private().captureCodexLaunch(ctx, launch)
				return err
			})
			if err == nil {
				test.Fatal("unproven launch or source closure accepted")
			}
		})
	}
}

func TestCodexObservationCanceled(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	launch := codexNativeLaunchFixture(test, home)
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	_, _, _, _, err := engine.private().captureCodexLaunch(ctx, launch)
	if !errors.Is(err, context.Canceled) {
		test.Fatal("observation ignored caller cancellation")
	}
}

func codexCandidateRegistryFixture(test *testing.T) {
	test.Helper()
	before := codexQualifiedProfiles
	codexQualifiedProfiles = []CodexProfileIdentity{codexNative0166Identity()}
	test.Cleanup(func() { codexQualifiedProfiles = before })
}

func codexOAuthFileFixture(test *testing.T) []byte {
	test.Helper()
	identity := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"synthetic@example.invalid"}`)) + ".synthetic"
	raw, err := json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]any{"id_token": identity, "access_token": "SYNTHETIC_ACCESS", "refresh_token": "SYNTHETIC_REFRESH", "account_id": "synthetic-account"}, "last_refresh": time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		test.Fatal("synthetic auth fixture failed")
	}
	return raw
}

func TestCodexObservedPreviewAndMaterialization(test *testing.T) {
	for _, mode := range []string{"missing", "api", "oauth"} {
		test.Run(mode, func(test *testing.T) {
			engine, home, _, _ := codexFixture(test)
			launch := codexNativeLaunchFixture(test, home)
			codexCandidateRegistryFixture(test)
			var auth []byte
			if mode == "api" {
				auth = []byte(`{"OPENAI_API_KEY":"SYNTHETIC_API"}`)
			}
			if mode == "oauth" {
				auth = codexOAuthFileFixture(test)
			}
			if auth != nil && os.WriteFile(filepath.Join(home, ".codex", "auth.json"), auth, 0o600) != nil {
				test.Fatal("auth fixture write failed")
			}
			selection := codexShapeSelection(test, engine)
			selection.get[codexSelectionData]().declaration.Catalog = &agentconfig.CodexCatalogDesired{Models: []agentconfig.CodexCatalogModelDesired{{Model: "synthetic-model", DisplayName: new("Synthetic model"), ContextWindow: new(uint64(32768)), SupportsParallelToolCalls: new(true), InputModalities: new([]agentconfig.CodexModality{"text"}), BaseInstructions: new("SYNTHETIC INSTRUCTIONS"), ReasoningLevels: new([]agentconfig.CodexReasoningEffort{"low", "high"}), DefaultReasoningLevel: new(agentconfig.CodexReasoningEffort("low"))}}}
			observation, err := engine.ObserveCodex(test.Context(), selection, launch)
			if err != nil {
				test.Fatal("real consumer observation refused")
			}
			defer observation.Destroy()
			plan, err := engine.PreviewCodexObserved(test.Context(), selection, observation)
			if err != nil || plan.View().OperationID == "" {
				test.Fatal("observed plan missing")
			}
			defer plan.Destroy()
			admission, err := engine.ConfirmCodex(test.Context(), plan, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, true)
			if err != nil {
				test.Fatal("observed confirmation refused")
			}
			defer admission.Destroy()
			secret, err := codexconfig.NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("SYNTHETIC_NEW_PROVIDER"))
			if err != nil {
				test.Fatal("secret fixture failed")
			}
			defer secret.Destroy()
			candidate, err := engine.MaterializeCodex(test.Context(), admission, []codexconfig.SelectedSecret{secret})
			if err != nil {
				test.Fatal("observed materialization refused")
			}
			defer candidate.Destroy()
			if candidate.Action(codexconfig.AuthRole) != codexconfig.Keep {
				test.Fatal("auth is not Keep")
			}
			result, err := engine.ApplyCodex(test.Context(), admission, candidate)
			if err != nil || result.State != CodexCompleted {
				test.Fatal("observed candidate publication refused")
			}
			for _, role := range []codexconfig.Role{codexconfig.ConfigRole, codexconfig.CatalogRole} {
				image := candidate.CopyImage(role)
				presence := image.Presence()
				wanted := image.CopyBytes()
				image.Destroy()
				name := codexRoleNames[0]
				if role == codexconfig.CatalogRole {
					name = codexRoleNames[2]
				}
				actual, err := os.ReadFile(filepath.Join(home, ".codex", name))
				matches := err == nil && bytes.Equal(actual, wanted)
				if presence == codexconfig.Missing {
					matches = errors.Is(err, os.ErrNotExist)
				}
				clear(actual)
				clear(wanted)
				if !matches {
					test.Fatal("published role differs from approved candidate")
				}
			}
			if auth != nil {
				actual, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
				if err != nil || !bytes.Equal(auth, actual) {
					test.Fatal("local auth changed")
				}
			}
		})
	}
}

func TestCodexObservedPublisherCloseDrainsFreshObservation(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	launch := codexNativeLaunchFixture(test, home)
	var descriptor codexLaunchDescriptor
	if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
		var err error
		descriptor, _, _, _, err = engine.private().captureCodexLaunch(ctx, launch)
		return err
	}); err != nil {
		test.Fatal("publisher Close descriptor fixture failed")
	}
	bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), nil, []byte(`{"models":[]}`)}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	operation.ProfileIdentity = codexNative0166Identity()
	operation.Observation = &descriptor
	codexStageLiteral(test, engine, operation, bodies)
	entered := make(chan struct{})
	engine.private().fault = func(stage string) error {
		if stage == "observation-config-started" {
			close(entered)
			<-engine.private().life.Done()
		}
		return nil
	}
	type outcome struct {
		result CodexResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := codexPublishLiteral(test.Context(), engine, operation.ID)
		done <- outcome{result, err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		test.Fatal("publisher fresh observation was not reached")
	}
	if err := engine.Close(); err != nil {
		test.Fatal("Close failed to drain publisher")
	}
	select {
	case actual := <-done:
		if !errors.Is(actual.err, ErrCodexClosed) && !errors.Is(actual.err, context.Canceled) || actual.result.State == CodexCompleted {
			test.Fatal("Close permitted publication after fresh observation cancellation")
		}
	case <-time.After(time.Second):
		test.Fatal("publisher observer remained active after Close")
	}
	for _, index := range []int{0, 2} {
		if _, err := os.Stat(filepath.Join(home, ".codex", codexRoleNames[index])); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("closed publisher advanced a role")
		}
	}
}

func TestCodexObservedMutationBeforeSecretRefuses(test *testing.T) {
	for _, kind := range []string{"source", "auth", "executable", "expired", "other-engine"} {
		test.Run(kind, func(test *testing.T) {
			engine, home, _, _ := codexFixture(test)
			launch := codexNativeLaunchFixture(test, home)
			codexCandidateRegistryFixture(test)
			if kind == "executable" {
				raw, err := os.ReadFile(launch.Executable)
				if err != nil {
					test.Fatal("executable fixture read failed")
				}
				launch.Executable = filepath.Join(filepath.Dir(home), "pinned-native")
				if os.WriteFile(launch.Executable, raw, 0o500) != nil {
					test.Fatal("executable fixture copy failed")
				}
				clear(raw)
			}
			selection := codexShapeSelection(test, engine)
			observed, err := engine.ObserveCodex(test.Context(), selection, launch)
			if err != nil {
				test.Fatal("mutation observation setup failed")
			}
			defer observed.Destroy()
			plan, err := engine.PreviewCodexObserved(test.Context(), selection, observed)
			if err != nil {
				test.Fatal("mutation preview setup failed")
			}
			defer plan.Destroy()
			admission, err := engine.ConfirmCodex(test.Context(), plan, []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}, true)
			if err != nil {
				test.Fatal("mutation admission setup failed")
			}
			defer admission.Destroy()
			switch kind {
			case "source":
				if os.Mkdir(filepath.Join(launch.Directory, ".codex"), 0o700) != nil || os.WriteFile(filepath.Join(launch.Directory, ".codex", "config.toml"), []byte("model_context_window=4096\n"), 0o600) != nil {
					test.Fatal("mutation fixture failed")
				}
			case "auth":
				if os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"unrecognized_synthetic_auth":true}`), 0o600) != nil {
					test.Fatal("mutation fixture failed")
				}
			case "executable":
				if os.Remove(launch.Executable) != nil || os.WriteFile(launch.Executable, []byte("changed"), 0o500) != nil {
					test.Fatal("mutation fixture failed")
				}
			case "expired":
				original := engine.private().now
				engine.private().now = func() time.Time { return original().Add(time.Hour) }
			case "other-engine":
				peer, _, _, _ := codexFixture(test)
				if _, err := peer.PreviewCodexObserved(test.Context(), selection, observed); !errors.Is(err, ErrCodexInvalid) {
					test.Fatal("foreign engine accepted observation")
				}
				return
			}
			secret, err := codexconfig.NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("SYNTHETIC_UNCONSUMED"))
			if err != nil {
				test.Fatal("secret fixture failed")
			}
			defer secret.Destroy()
			candidate, err := engine.MaterializeCodex(test.Context(), admission, []codexconfig.SelectedSecret{secret})
			defer candidate.Destroy()
			if err == nil || admission.get[codexAdmissionData]().materialized {
				test.Fatal("changed observation reached secret materialization")
			}
		})
	}
}

func TestCodexObservationCloseDrainsMergedContext(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	launch := codexNativeLaunchFixture(test, home)
	selection := codexShapeSelection(test, engine)
	reached := make(chan struct{})
	engine.private().fault = func(stage string) error {
		if stage == "observation-config-started" {
			close(reached)
			<-engine.private().life.Done()
		}
		return nil
	}
	done := make(chan error, 1)
	var workers sync.WaitGroup
	defer func() { _ = engine.Close(); workers.Wait() }()
	workers.Go(func() {
		observed, err := engine.ObserveCodex(test.Context(), selection, launch)
		observed.Destroy()
		done <- err
	})
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		test.Fatal("native observation checkpoint not reached")
	}
	if err := engine.Close(); err != nil {
		test.Fatal("Close failed")
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrCodexClosed) && !errors.Is(err, context.Canceled) {
			test.Fatal("Close allowed late observation")
		}
	case <-time.After(time.Second):
		test.Fatal("Close did not drain observation")
	}
}
