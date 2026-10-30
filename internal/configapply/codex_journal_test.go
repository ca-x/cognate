package configapply

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

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
