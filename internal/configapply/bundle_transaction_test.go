package configapply

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBundleFixedRoleAndNoAuthWriter(test *testing.T) {
	for _, family := range []bundleFamily{0, bundleCodex, bundleGemini, 255} {
		for index := -1; index < 4; index++ {
			name, valid := bundleRole(family, index)
			wanted := family == bundleCodex && index >= 0 && index < 3 || family == bundleGemini && index >= 0 && index < 2
			if valid != wanted || valid && !bundleName(name) {
				test.Fatal("bundle accepted nonfixed role")
			}
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", "a\\b", "a\x00b"} {
		if bundleName(name) {
			test.Fatal("shared bundle admitted nonlocal stage name")
		}
	}
	if bundlePublishRole(nil, bundleCodex, 1, "auth-stage", nativeImage{}, nativeImage{}, nativeImage{}) == nil {
		test.Fatal("shared writer admitted Codex auth role")
	}
	before := [3]nativeImage{{Present: true, SHA256: [32]byte{1}}, {Present: true, SHA256: [32]byte{2}}}
	after := before
	after[1].SHA256[0] = 3
	writes := [3]bool{false, true, false}
	if bundleValidateMix(before, after, before, writes) != nil || bundleValidateMix(before, after, after, writes) != nil {
		test.Fatal("fixed-role before/after mixture rejected")
	}
	foreign := before
	foreign[1].SHA256[0] = 4
	if bundleValidateMix(before, after, foreign, writes) == nil {
		test.Fatal("foreign second role admitted")
	}
}

func TestD2OldReader(test *testing.T) {
	home, stateRoot, kind := os.Getenv("D2_HOME"), os.Getenv("D2_STATE"), os.Getenv("D2_KIND")
	if home == "" || stateRoot == "" || kind == "" {
		test.Skip("explicit cross-binary fixture parent required")
	}
	before, err := os.ReadFile(filepath.Join(stateRoot, codexAnchorName))
	if err != nil {
		test.Fatal("cross-reader encrypted anchor")
	}
	defer clear(before)
	engine, err := OpenCodex(test.Context(), home, stateRoot, bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		test.Fatal("cross-reader authenticated open")
	}
	if len(engine.private().inventory.Operations) != 1 {
		test.Fatal("cross-reader fixture operation count")
	}
	for _, operation := range engine.private().inventory.Operations {
		if operation.LifecycleVersion != 0 || operation.ManagedModelsVersion != 0 || operation.Before[1] != operation.After[1] {
			test.Fatal("cross-reader upgraded versions/auth Keep")
		}
		switch kind {
		case "legacy":
			if operation.ProfileIdentity != (CodexProfileIdentity{}) {
				test.Fatal("cross-reader legacy tuple changed")
			}
		case "v1":
			if operation.ProfileIdentity.ID != "codex-0.166.0-linux-amd64-full-responses-v1" {
				test.Fatal("cross-reader v1 tuple changed")
			}
		case "v2":
			if operation.ProfileIdentity.ID != "codex-0.166.0-linux-amd64-full-responses-effective-v2" {
				test.Fatal("cross-reader v2 tuple changed")
			}
		default:
			test.Fatal("unknown cross-reader fixture family")
		}
	}
	if engine.Close() != nil {
		test.Fatal("cross-reader close")
	}
	after, err := os.ReadFile(filepath.Join(stateRoot, codexAnchorName))
	defer clear(after)
	if err != nil || !bytes.Equal(before, after) {
		test.Fatal("cross-reader rewrote legacy ciphertext")
	}
	test.Log("cross-reader-executed", kind)
}
