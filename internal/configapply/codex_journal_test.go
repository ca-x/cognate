package configapply

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/codexconfig"
)

func TestCodexD2HistoricalOpen(test *testing.T) {
	for _, kind := range []string{"legacy", "v1", "v2", "nil-partial", "mixed", "unknown-lifecycle", "unknown-metadata"} {
		test.Run(kind, func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), nil, []byte(`{"models":[]}`)}
			operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			if kind != "legacy" && kind != "nil-partial" {
				launch := codexNativeLaunchFixture(test, home)
				if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
					descriptor, _, _, _, err := engine.private().captureCodexLaunch(ctx, launch)
					operation.Observation = &descriptor
					return err
				}); err != nil {
					test.Fatal("physical descriptor capture", err)
				}
				id, manifest, profile, template := codexconfig.Native0166Manifest()
				if kind == "v2" {
					id, manifest, profile, template = codexconfig.Native0166EffectiveManifest()
				}
				operation.ProfileIdentity = CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}
			}
			codexStageLiteral(test, engine, operation, bodies)
			if kind == "nil-partial" || kind == "mixed" || kind == "unknown-lifecycle" || kind == "unknown-metadata" {
				if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
					row := engine.private().inventory.Operations[operation.ID]
					switch kind {
					case "nil-partial", "mixed":
						row.ProfileIdentity.TemplateDigest[0] ^= 1
					case "unknown-lifecycle":
						row.LifecycleVersion = 9
					case "unknown-metadata":
						row.ManagedModelsVersion = 9
					}
					return engine.private().saveCodexInventory()
				}); err != nil {
					if errors.Is(err, ErrCodexAttention) {
						return
					}
					test.Fatal("invalid-anchor persistence", err)
				}
			}
			anchor, err := os.ReadFile(filepath.Join(stateRoot, codexAnchorName))
			if err != nil || engine.Close() != nil {
				test.Fatal("close historical fixture")
			}
			if binary := os.Getenv("COGNATE_D2_OLD_READER"); binary != "" && (kind == "legacy" || kind == "v1" || kind == "v2") {
				ctx, cancel := context.WithTimeout(test.Context(), 10*time.Second)
				defer cancel()
				child := exec.CommandContext(ctx, binary, "-test.run=^TestD2OldReader$", "-test.timeout=8s")
				child.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "D2_HOME=" + home, "D2_STATE=" + stateRoot, "D2_KIND=" + kind}
				if output, err := child.CombinedOutput(); err != nil || ctx.Err() != nil {
					test.Fatalf("fixed old-reader compatibility failed: %s", output)
				}
			}
			reopened, err := OpenCodex(test.Context(), home, stateRoot, key)
			if kind == "nil-partial" || kind == "mixed" || kind == "unknown-lifecycle" || kind == "unknown-metadata" {
				if reopened != nil {
					reopened.Close()
				}
				if !errors.Is(err, ErrCodexAttention) {
					test.Fatal("unknown or partial historical identity did not fail closed")
				}
			} else {
				if err != nil {
					test.Fatal("valid historical inventory refused", err)
				}
				defer reopened.Close()
				if err := reopened.private().withLock(test.Context(), func(ctx context.Context) error {
					row := reopened.private().inventory.Operations[operation.ID]
					if row.ProfileIdentity != operation.ProfileIdentity || row.LifecycleVersion != 0 || row.ManagedModelsVersion != 0 {
						test.Fatal("read upgraded historical identity or versions")
					}
					return reopened.private().validateCodexOperationObservation(ctx, row)
				}); err != nil {
					test.Fatal("historical local revalidation refused", err)
				}
				if fact, err := reopened.RecordedCodexProfile(test.Context(), operation.ID); err != nil || fact != operation.ProfileIdentity {
					test.Fatal("historical accessor returned active identity instead of stored identity")
				}
			}
			after, err := os.ReadFile(filepath.Join(stateRoot, codexAnchorName))
			if err != nil || !bytes.Equal(anchor, after) {
				test.Fatal("quiescent reopen rewrote encrypted anchor")
			}
			if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("read/reject changed target")
			}
		})
	}
}

func TestCodexD2HistoricalRecovery(test *testing.T) {
	for _, kind := range []string{"legacy", "v1", "v2"} {
		test.Run(kind, func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			launch := codexNativeLaunchFixture(test, home)
			auth := []byte(`{"OPENAI_API_KEY":"SYNTHETIC_D2_KEEP"}`)
			if os.WriteFile(filepath.Join(home, ".codex", "auth.json"), auth, 0o600) != nil {
				test.Fatal("auth fixture")
			}
			bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), auth, []byte(`{"models":[]}`)}
			operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			if kind != "legacy" {
				if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
					descriptor, _, _, _, err := engine.private().captureCodexLaunch(ctx, launch)
					operation.Observation = &descriptor
					return err
				}); err != nil {
					test.Fatal("capture historical descriptor")
				}
				id, manifest, profile, template := codexconfig.Native0166Manifest()
				if kind == "v2" {
					id, manifest, profile, template = codexconfig.Native0166EffectiveManifest()
				}
				operation.ProfileIdentity = CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}
			}
			codexStageLiteral(test, engine, operation, bodies)
			visited := false
			engine.private().fault = func(stage string) error {
				if stage == "directory-synced-0" {
					visited = true
					return ErrCodexUnknown
				}
				return nil
			}
			if result, err := codexPublishLiteral(test.Context(), engine, operation.ID); !visited || err == nil || result.State == CodexCompleted {
				test.Fatal("partial publication did not preserve uncertainty")
			}
			if engine.Close() != nil {
				test.Fatal("partial close")
			}
			reopened, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("historical recovery reopen")
			}
			defer reopened.Close()
			plan, err := reopened.PreviewCodexRecovery(test.Context(), operation.ID)
			if err != nil {
				test.Fatal("historical recovery preview")
			}
			defer plan.Destroy()
			confirmed, err := plan.Confirm(test.Context(), true)
			if err != nil {
				test.Fatal("historical recovery confirm")
			}
			defer confirmed.Destroy()
			if result, err := reopened.RecoverCodex(test.Context(), confirmed); err != nil || result.State != CodexCompleted {
				test.Fatal("historical recovery execution")
			}
			restore, err := reopened.PreviewCodexRestore(test.Context(), operation.ID)
			if err != nil {
				test.Fatal("historical restore preview")
			}
			defer restore.Destroy()
			restore, err = restore.Confirm(test.Context(), true)
			if err != nil {
				test.Fatal("historical restore confirm")
			}
			result, err := reopened.RestoreCodex(test.Context(), restore)
			if err != nil || result.State != CodexRestored {
				test.Fatal("historical restore execute")
			}
			for _, id := range []string{operation.ID, result.OperationID} {
				if fact, err := reopened.RecordedCodexProfile(test.Context(), id); err != nil || fact != operation.ProfileIdentity {
					test.Fatal("recovery/restore changed original identity")
				}
			}
			for _, role := range []string{"config.toml", "cognate-model-catalog.json"} {
				if _, err := os.Stat(filepath.Join(home, ".codex", role)); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("historical restore changed original absence")
				}
			}
			actual, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
			if err != nil || !bytes.Equal(auth, actual) {
				test.Fatal("historical restore changed Keep auth")
			}
		})
	}
}

func TestCodexD2HistoricalMixedAndStaged(test *testing.T) {
	for _, staged := range []bool{false, true} {
		test.Run(strconv.FormatBool(staged), func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			launch := codexNativeLaunchFixture(test, home)
			identities := make(map[string]CodexProfileIdentity)
			for _, kind := range []string{"legacy", "v1", "v2"} {
				bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), nil, []byte(`{"models":[]}`)}
				operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
				if kind != "legacy" {
					var descriptor codexLaunchDescriptor
					if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
						var err error
						descriptor, _, _, _, err = engine.private().captureCodexLaunch(ctx, launch)
						return err
					}); err != nil {
						test.Fatal("mixed descriptor")
					}
					id, manifest, profile, template := codexconfig.Native0166Manifest()
					if kind == "v2" {
						id, manifest, profile, template = codexconfig.Native0166EffectiveManifest()
					}
					operation.ProfileIdentity = CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}
					owned := codexCopyLaunchDescriptor(descriptor)
					operation.Observation = &owned
				}
				codexStageLiteral(test, engine, operation, bodies)
				if result, err := codexPublishLiteral(test.Context(), engine, operation.ID); err != nil || result.State != CodexCompleted {
					test.Fatal("mixed history sequential publish")
				}
				identities[operation.ID] = operation.ProfileIdentity
			}
			if staged {
				engine.private().fault = func(stage string) error {
					if stage == "anchor-staged" {
						return ErrCodexUnknown
					}
					return nil
				}
				if err := engine.private().withLock(test.Context(), func(ctx context.Context) error { return engine.private().saveCodexInventory() }); !errors.Is(err, ErrCodexUnknown) {
					test.Fatal("staged-anchor boundary not reached")
				}
			}
			if engine.Close() != nil {
				test.Fatal("mixed close")
			}
			reopened, err := OpenCodex(test.Context(), home, stateRoot, key)
			if err != nil {
				test.Fatal("mixed/staged inventory refused")
			}
			defer reopened.Close()
			anchor, err := os.ReadFile(filepath.Join(stateRoot, codexAnchorName))
			if err != nil {
				test.Fatal("repaired anchor")
			}
			for id, want := range identities {
				if identity, err := reopened.RecordedCodexProfile(test.Context(), id); err != nil || identity != want {
					test.Fatal("mixed/staged read reinterpreted identity")
				}
			}
			after, err := os.ReadFile(filepath.Join(stateRoot, codexAnchorName))
			if err != nil || !bytes.Equal(anchor, after) {
				test.Fatal("quiescent mixed fact lookup wrote anchor")
			}
			if _, err := os.Stat(filepath.Join(stateRoot, codexNextAnchorName)); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("staged anchor not drained")
			}
		})
	}
}

func TestCodexObservationDescriptorIsBoundedAndPrivate(test *testing.T) {
	engine, home, stateRoot, _ := codexFixture(test)
	launch := codexNativeLaunchFixture(test, home)
	var descriptor codexLaunchDescriptor
	if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
		var err error
		descriptor, _, _, _, err = engine.private().captureCodexLaunch(ctx, launch)
		return err
	}); err != nil {
		test.Fatal("descriptor fixture failed")
	}
	bodies := [3][]byte{[]byte("model=\"synthetic-after\"\n"), nil, []byte(`{"models":[]}`)}
	operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	operation.ProfileIdentity = codexNative0166Identity()
	operation.Observation = &descriptor
	codexStageLiteral(test, engine, operation, bodies)
	for _, name := range []string{codexAnchorName, codexNextAnchorName} {
		raw, err := os.ReadFile(filepath.Join(stateRoot, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			test.Fatal("encrypted anchor read failed")
		}
		if bytes.Contains(raw, []byte(launch.Directory)) || bytes.Contains(raw, []byte("SYNTHETIC_")) || bytes.Contains(raw, []byte("app-server")) {
			test.Fatal("observation descriptor escaped encrypted journal")
		}
	}
	for _, kind := range []string{"version", "source-count", "relative", "arguments", "digest"} {
		owned := codexCopyLaunchDescriptor(descriptor)
		switch kind {
		case "version":
			owned.Version = 2
		case "source-count":
			owned.Sources = make([]codexSourceStamp, 65)
		case "relative":
			owned.Launch.Directory = "relative"
		case "arguments":
			owned.Launch.Arguments = []string{"app-server", "-c", "model_context_window=4096"}
		case "digest":
			owned.Executable.SHA256[0] ^= 1
		}
		if codexDescriptorShape(&owned) == nil {
			test.Fatal("unbounded or unknown descriptor accepted")
		}
	}
	before := operation.Before[1]
	operation.Before[1].Present = !before.Present
	if codexInventoryShape(&engine.private().inventory) == nil {
		test.Fatal("journal accepted mismatched auth observation")
	}
	operation.Before[1] = before
}

func TestCodexStagedAnchorDoesNotTrustUnknownOrInvalidState(test *testing.T) {
	for _, scenario := range []string{"equal-sequence", "gap", "device", "tamper", "shape", "unknown-vault", "missing-candidate", "next-mode"} {
		test.Run(scenario, func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			bodies := [3][]byte{[]byte("anchor-validation-config"), nil, []byte("anchor-validation-catalog")}
			operation := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			codexStageLiteral(test, engine, operation, bodies)
			state := engine.private()
			state.fault = func(label string) error {
				if label == "anchor-staged" {
					return ErrCodexUnknown
				}
				return nil
			}
			if err := state.withLock(test.Context(), func(ctx context.Context) error { return state.saveCodexInventory() }); !errors.Is(err, ErrCodexUnknown) {
				test.Fatal("staged anchor fixture did not retain authentic pair")
			}
			state.fault = nil
			next, encoded, err := state.state.read(codexNextAnchorName, codexMetadataLimit*2+1024)
			if err != nil || !next.Present {
				test.Fatal("next anchor fixture missing")
			}
			var sibling codexInventory
			if err := state.unseal("anchor", nil, encoded, &sibling, codexMetadataLimit); err != nil {
				test.Fatal(err)
			}
			clear(encoded)
			switch scenario {
			case "equal-sequence":
				sibling.Sequence--
			case "gap":
				sibling.Sequence++
			case "device":
				sibling.Device = codexRandomID()
			case "shape":
				sibling.Operations = nil
			case "unknown-vault":
				if _, err := state.vault.write("unowned-extra", []byte("synthetic-unknown")); err != nil {
					test.Fatal(err)
				}
			case "missing-candidate":
				if err := state.vault.removeOwn(operation.ID+".candidate", operation.Candidate); err != nil {
					test.Fatal(err)
				}
			}
			encoded, err = state.seal("anchor", nil, sibling, codexMetadataLimit)
			if err != nil {
				test.Fatal(err)
			}
			if scenario == "tamper" {
				encoded[len(encoded)/2] ^= 1
			}
			if err := state.state.removeOwn(codexNextAnchorName, next); err != nil {
				test.Fatal(err)
			}
			if _, err := state.state.write(codexNextAnchorName, encoded); err != nil {
				test.Fatal(err)
			}
			clear(encoded)
			if err := engine.Close(); err != nil {
				test.Fatal(err)
			}
			if scenario == "next-mode" {
				if err := os.Chmod(filepath.Join(stateRoot, codexNextAnchorName), 0o644); err != nil {
					test.Fatal(err)
				}
			}
			anchorBefore, err := os.ReadFile(filepath.Join(stateRoot, codexAnchorName))
			if err != nil {
				test.Fatal(err)
			}
			nextBefore, err := os.ReadFile(filepath.Join(stateRoot, codexNextAnchorName))
			if err != nil {
				test.Fatal(err)
			}
			opened, err := OpenCodex(test.Context(), home, stateRoot, key)
			if opened != nil {
				_ = opened.Close()
			}
			if err == nil {
				test.Fatal("invalid anchor pair or unknown side inventory was accepted")
			}
			anchorAfter, err := os.ReadFile(filepath.Join(stateRoot, codexAnchorName))
			if err != nil || !bytes.Equal(anchorBefore, anchorAfter) {
				test.Fatal("refusal changed authenticated committed anchor")
			}
			nextAfter, err := os.ReadFile(filepath.Join(stateRoot, codexNextAnchorName))
			if err != nil || !bytes.Equal(nextBefore, nextAfter) {
				test.Fatal("refusal discarded unknown next anchor")
			}
			for _, name := range codexRoleNames {
				if _, err := os.Stat(filepath.Join(home, ".codex", name)); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("invalid anchor recovery published target")
				}
			}
		})
	}
}

func TestCodexInitialStagedAnchorReopens(test *testing.T) {
	engine, home, stateRoot, key := codexFixture(test)
	state := engine.private()
	if err := state.state.removeOwn(codexAnchorName, state.anchor); err != nil {
		test.Fatal(err)
	}
	state.anchor, state.sequence, state.inventory.Sequence = nativeImage{}, 0, 0
	state.fault = func(label string) error {
		if label == "anchor-staged" {
			return ErrCodexUnknown
		}
		return nil
	}
	if err := state.saveCodexInventory(); !errors.Is(err, ErrCodexUnknown) {
		test.Fatal("initial anchor write did not retain durable staging")
	}
	if err := engine.Close(); err != nil {
		test.Fatal(err)
	}
	fresh, err := OpenCodex(test.Context(), home, stateRoot, key)
	if err != nil {
		test.Fatal("authentic initial sequence1 staging refused", err)
	}
	defer fresh.Close()
	if fresh.private().sequence != 1 || len(fresh.private().inventory.Operations) != 0 {
		test.Fatal("initial anchor invented operation state")
	}
}

func TestCodexEncryptedBundlesBindDomainOwnerRootAndOperation(test *testing.T) {
	engine, home, stateRoot, _ := codexFixture(test)
	secret := []byte("SYNTHETIC-D1-SECRET-NOT-FOR-OUTPUT")
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), secret, 0o600); err != nil {
		test.Fatal(err)
	}
	bodies := [3][]byte{[]byte("NEW-SYNTHETIC-PRIVATE-KEY"), nil, []byte("private-catalog")}
	op := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
	codexStageLiteral(test, engine, op, bodies)
	if err := engine.private().withLock(test.Context(), func(ctx context.Context) error {
		operation := engine.private().inventory.Operations[op.ID]
		_, encoded, err := engine.private().vault.read(op.ID+".backup", 2*codexBundleLimit+1024)
		if err != nil {
			return err
		}
		defer clear(encoded)
		for _, mutate := range []func(*codexOperation){func(value *codexOperation) { value.Owner = "other-owner" }, func(value *codexOperation) { value.Origin = "https://other.invalid" }, func(value *codexOperation) { value.Profile = "other-profile" }, func(value *codexOperation) { value.ID = codexRandomID() }, func(value *codexOperation) { value.Binding[0] ^= 1 }, func(value *codexOperation) { value.Target.Inode++ }} {
			changed := *operation
			mutate(&changed)
			var bundle codexBundle
			if err := engine.private().unseal("backup", &changed, encoded, &bundle, codexBundleLimit); !errors.Is(err, ErrCodexAttention) {
				test.Fatal("AAD mismatch accepted")
			}
		}
		var bundle codexBundle
		if err := engine.private().unseal("candidate", operation, encoded, &bundle, codexBundleLimit); !errors.Is(err, ErrCodexAttention) {
			test.Fatal("domain replay accepted")
		}
		other, _, _, _ := codexFixture(test)
		if err := other.private().unseal("backup", operation, encoded, &bundle, codexBundleLimit); !errors.Is(err, ErrCodexAttention) {
			test.Fatal("physical root replay accepted")
		}
		return nil
	}); err != nil {
		test.Fatal(err)
	}
	for _, directory := range []string{stateRoot, filepath.Join(stateRoot, "codex-local-v1")} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			test.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			body, err := os.ReadFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				test.Fatal(err)
			}
			if bytes.Contains(body, secret) || bytes.Contains(body, bodies[0]) {
				test.Fatal("plaintext in durable state")
			}
		}
	}
}

func TestCodexJournalMissingUnknownTamperedAndBounds(test *testing.T) {
	for _, mode := range []string{"delete-bundle", "unknown-entry", "tamper-bundle", "missing-anchor", "entry-overflow", "wrong-key"} {
		test.Run(mode, func(test *testing.T) {
			engine, home, stateRoot, key := codexFixture(test)
			bodies := [3][]byte{[]byte("config-secret"), nil, []byte("catalog-private")}
			op := codexLiteral(test, engine, bodies, [3]bool{true, false, true})
			codexStageLiteral(test, engine, op, bodies)
			if err := engine.Close(); err != nil {
				test.Fatal(err)
			}
			vault := filepath.Join(stateRoot, "codex-local-v1")
			switch mode {
			case "delete-bundle":
				if err := os.Remove(filepath.Join(vault, op.ID+".backup")); err != nil {
					test.Fatal(err)
				}
			case "unknown-entry":
				if err := os.WriteFile(filepath.Join(vault, "unknown"), []byte("not-owned"), 0o600); err != nil {
					test.Fatal(err)
				}
			case "tamper-bundle":
				path := filepath.Join(vault, op.ID+".candidate")
				body, err := os.ReadFile(path)
				if err != nil {
					test.Fatal(err)
				}
				body[len(body)/2] ^= 1
				if err := os.WriteFile(path, body, 0o600); err != nil {
					test.Fatal(err)
				}
			case "missing-anchor":
				if err := os.Remove(filepath.Join(stateRoot, codexAnchorName)); err != nil {
					test.Fatal(err)
				}
			case "entry-overflow":
				for index := range codexEntryLimit + 1 {
					if err := os.WriteFile(filepath.Join(vault, "unknown-"+strconv.Itoa(index)), nil, 0o600); err != nil {
						test.Fatal(err)
					}
				}
			case "wrong-key":
				key = bytes.Repeat([]byte{0x43}, 32)
			}
			opened, err := OpenCodex(test.Context(), home, stateRoot, key)
			if opened != nil {
				_ = opened.Close()
			}
			if err == nil {
				test.Fatal("invalid authenticated inventory accepted")
			}
			if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("invalid journal changed target")
			}
		})
	}
}

func TestCodexJournalCapacityBeforeTargets(test *testing.T) {
	engine, home, _, _ := codexFixture(test)
	bodies := [3][]byte{bytes.Repeat([]byte{'x'}, codexRoleLimit+1), nil, nil}
	op := codexLiteral(test, engine, bodies, [3]bool{true, false, false})
	err := engine.private().withLock(test.Context(), func(ctx context.Context) error { return engine.private().prepareCodexOperation(ctx, op, bodies) })
	if !errors.Is(err, ErrCodexCapacity) {
		test.Fatal("oversized role accepted")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("capacity failure wrote target")
	}
	if _, err := engine.private().seal("anchor", nil, bytes.Repeat([]byte{'x'}, codexMetadataLimit), codexMetadataLimit); !errors.Is(err, ErrCodexCapacity) {
		test.Fatal("metadata budget not enforced")
	}
}
