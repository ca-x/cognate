package importer_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/importer"
)

const npxSelectionSource = importer.SelectionSource("npx-skills-lock")

func npxLock(test *testing.T, skills map[string]any) []byte {
	test.Helper()
	raw, err := json.Marshal(map[string]any{"version": 3, "skills": skills})
	if err != nil {
		test.Fatal("cannot encode synthetic lock")
	}
	return raw
}

func TestNPXSkillsSelectionBatchLimits(test *testing.T) {
	for _, scenario := range []struct {
		name  string
		count int
		repos bool
		limit bool
	}{
		{"skills-at-limit", 128, false, false},
		{"skills-over-limit", 129, false, true},
		{"repos-at-limit", 16, true, false},
		{"repos-over-limit", 17, true, true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			skills := make(map[string]any)
			for index := range scenario.count {
				entry := npxEntry()
				if scenario.repos {
					entry["source"] = fmt.Sprintf("example/repo-%d", index)
					delete(entry, "sourceUrl")
				}
				skills[fmt.Sprintf("skill-%d", index)] = entry
			}
			projection, err := importer.ProjectSelection(test.Context(), npxSelectionSource, npxLock(test, skills))
			defer projection.Destroy()
			if scenario.limit && !errors.Is(err, importer.ErrSelectionLimit) || !scenario.limit && err != nil {
				test.Fatal("selection response capacity was not enforced before publishing candidates")
			}
		})
	}
}

func TestNPXSkillsSelectionDirectoryHints(test *testing.T) {
	for _, hint := range []string{"skills/alpha", "skills/alpha/SKILL.md", ".", "SKILL.md"} {
		entry := npxEntry()
		entry["skillPath"] = hint
		projection, err := importer.ProjectSelection(test.Context(), npxSelectionSource, npxLock(test, map[string]any{"alpha": entry}))
		if err != nil {
			test.Fatal("valid repository-relative directory hint was rejected")
		}
		view, err := projection.View()
		projection.Destroy()
		expected := "skills/alpha"
		if hint == "." || hint == "SKILL.md" {
			expected = "."
		}
		if err != nil || len(view.Skills) != 1 || view.Skills[0].Path == nil || *view.Skills[0].Path != expected {
			test.Fatal("file hint did not normalize to the scanned logical directory")
		}
	}
}

func npxEntry() map[string]any {
	return map[string]any{"sourceType": "github", "source": "Example/Toolkit", "sourceUrl": "https://github.com/Example/Toolkit", "ref": "main", "skillPath": "skills/alpha", "skillFolderHash": strings.Repeat("1", 40), "installedAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"}
}

func TestNPXSkillsSelectionProjection(test *testing.T) {
	first, second := npxEntry(), npxEntry()
	second["skillPath"] = "skills/beta/SKILL.md"
	second["source_type"] = "github"
	delete(second, "sourceType")
	raw := npxLock(test, map[string]any{"beta": second, "alpha": first, "private-source": map[string]any{"sourceType": "local", "source": "/synthetic/private/location"}})
	before := bytes.Clone(raw)
	projection, err := importer.ProjectSelection(test.Context(), npxSelectionSource, raw)
	if err != nil {
		test.Fatal("npx skills GitHub source was not projected")
	}
	defer projection.Destroy()
	view, err := projection.View()
	if err != nil || view.Source != npxSelectionSource || len(view.Repositories) != 1 || len(view.Skills) != 2 || len(view.Configurations) != 0 {
		test.Fatal("lock did not produce only deduplicated GitHub metadata")
	}
	if view.Repositories[0].Repository != "example/toolkit" || view.Repositories[0].Branch != "main" || view.Skills[0].Name != "alpha" || view.Skills[1].Name != "beta" {
		test.Fatal("source order, normalization or ref was lost")
	}
	for index, expected := range []string{"skills/alpha", "skills/beta"} {
		candidate := view.Skills[index]
		if candidate.Path == nil || *candidate.Path != expected || candidate.Status != importer.SelectionNeedsBinding || candidate.Binding != nil {
			test.Fatal("unverified lock data became a bound or installed skill")
		}
	}
	encoded, err := json.Marshal(view)
	if err != nil || bytes.Contains(encoded, []byte("synthetic/private")) || bytes.Contains(encoded, []byte(strings.Repeat("1", 40))) || !bytes.Equal(raw, before) {
		test.Fatal("private source metadata escaped projection or input changed")
	}
	candidates, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{})
	if err != nil {
		test.Fatal("server npx moejs adapter did not execute")
	}
	defer candidates.Destroy()
	actual, err := candidates.View()
	if err != nil || len(actual.Repositories) != 1 || len(actual.Skills) != 2 || actual.Skills[0].ID != view.Skills[0].ID {
		test.Fatal("adapter changed normalized candidate identity")
	}
	for _, source := range []importer.SelectionSource{importer.SelectionMagpie, importer.SelectionCCSwitch24, npxSelectionSource} {
		first, firstErr := importer.SelectionAdapterDigest(source)
		second, secondErr := importer.SelectionAdapterDigest(source)
		if firstErr != nil || secondErr != nil || len(first) != 64 || first != second {
			test.Fatal("adapter identity is not deterministic")
		}
	}
}

func TestNPXSkillsSelectionInvalidInput(test *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"conflicting-type", func(entry map[string]any) { entry["source_type"] = "local" }},
		{"credential-url", func(entry map[string]any) { entry["sourceUrl"] = "https://synthetic@github.com/Example/Toolkit" }},
		{"query-url", func(entry map[string]any) {
			entry["sourceUrl"] = "https://github.com/Example/Toolkit?synthetic=private"
		}},
		{"mismatched-url", func(entry map[string]any) { entry["sourceUrl"] = "https://github.com/Other/Toolkit" }},
		{"other-host", func(entry map[string]any) { entry["sourceUrl"] = "https://github.com.invalid/Example/Toolkit" }},
		{"http-url", func(entry map[string]any) { entry["sourceUrl"] = "http://github.com/Example/Toolkit" }},
		{"unsafe-ref", func(entry map[string]any) { entry["ref"] = "../main" }},
		{"absolute-path", func(entry map[string]any) { entry["skillPath"] = "/skills/alpha" }},
		{"parent-path", func(entry map[string]any) { entry["skillPath"] = "skills/../alpha" }},
		{"windows-path", func(entry map[string]any) { entry["skillPath"] = `skills\alpha` }},
		{"control-name", func(entry map[string]any) { entry["source"] = "example/\ntoolkit" }},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			entry := npxEntry()
			scenario.change(entry)
			projection, err := importer.ProjectSelection(test.Context(), npxSelectionSource, npxLock(test, map[string]any{"alpha": entry}))
			defer projection.Destroy()
			if !errors.Is(err, importer.ErrSelectionInvalid) {
				test.Fatal("unsafe lock was not refused with a safe input error")
			}
		})
	}
	for _, raw := range []string{
		`{"version":2,"skills":{}}`,
		`{"version":4,"skills":{}}`,
		`{"version":3,"version":3,"skills":{}}`,
		`{"version":3,"skills":{"alpha":{"sourceType":"github","sourceType":"github","source":"example/toolkit"}}}`,
		`{"version":3,"skills":[]}`,
		`{"version":3,"skills":{}} null`,
		`{"version":3,"skills":`,
	} {
		projection, err := importer.ProjectSelection(test.Context(), npxSelectionSource, []byte(raw))
		projection.Destroy()
		if err == nil {
			test.Fatal("unknown schema or malformed lock was accepted")
		}
	}
}

func TestNPXSkillsSelectionBoundsAndCancellation(test *testing.T) {
	raw := npxLock(test, map[string]any{})
	projection, err := importer.ProjectSelection(test.Context(), npxSelectionSource, raw)
	if err != nil {
		test.Fatal("empty supported lock was rejected")
	}
	projection.Destroy()
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := importer.ProjectSelection(ctx, npxSelectionSource, raw); !errors.Is(err, context.Canceled) {
		test.Fatal("cancelled input did not stop")
	}
	if _, err := importer.ProjectSelection(test.Context(), npxSelectionSource, bytes.Repeat([]byte(" "), (1<<20)+1)); !errors.Is(err, importer.ErrSelectionLimit) {
		test.Fatal("oversized lock was not bounded")
	}
	skills := make(map[string]any)
	for index := range 1001 {
		skills[strings.Repeat("a", index+1)] = npxEntry()
	}
	if _, err := importer.ProjectSelection(test.Context(), npxSelectionSource, npxLock(test, skills)); !errors.Is(err, importer.ErrSelectionLimit) {
		test.Fatal("skill count exceeded the projection budget")
	}
}
