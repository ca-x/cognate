package importer_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/importer"
)

func selectionFixture(test *testing.T, name string) []byte {
	test.Helper()
	data, err := os.ReadFile("testdata/selection/" + name)
	if err != nil {
		test.Fatal("synthetic selection fixture unavailable")
	}
	return data
}

func TestSelectionMagpieProjection(test *testing.T) {
	raw := selectionFixture(test, "magpie.json")
	before := bytes.Clone(raw)
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionMagpie, raw)
	if err != nil {
		test.Fatal("Magpie selection projection unavailable")
	}
	defer projection.Destroy()
	view, err := projection.View()
	if err != nil || len(view.Repositories) != 1 || len(view.Skills) != 2 || len(view.Configurations) != 2 {
		test.Fatal("three source columns were not projected")
	}
	if view.Repositories[0].Repository != "example/toolkit" || view.Skills[0].Path == nil || *view.Skills[0].Path != "skills/review" || view.Skills[1].Status != importer.SelectionUnsupportedKind {
		test.Fatal("skill source bounds changed")
	}
	for _, candidate := range view.Configurations {
		if candidate.Status != importer.SelectionUnsupportedKind || candidate.Draft != nil {
			test.Fatal("unsupported auxiliary configuration became portable")
		}
	}
	encoded, err := json.Marshal(view)
	if err != nil || bytes.Contains(encoded, []byte("SYNTHETIC-PRIVATE-MARKER")) {
		test.Fatal("private source data escaped safe projection")
	}
	if !bytes.Equal(raw, before) {
		test.Fatal("caller snapshot bytes were mutated")
	}
}

func TestSelectionPluginParityAndPrivacy(test *testing.T) {
	for _, source := range []importer.SelectionSource{importer.SelectionCCSwitch24, importer.SelectionMagpie} {
		raw := selectionFixture(test, "magpie.json")
		if source == importer.SelectionCCSwitch24 {
			raw = ccSwitchSnapshot(test, string(selectionFixture(test, "ccswitch-schema24.sql")))
		}
		projection, err := importer.ProjectSelection(test.Context(), source, raw)
		if err != nil {
			test.Fatal("qualified source projection failed")
		}
		defer projection.Destroy()
		view, _ := projection.View()
		options := importer.SelectionOptions{}
		if source == importer.SelectionCCSwitch24 {
			options = selectionOptions(view)
		}
		candidates, err := importer.BuildSelection(test.Context(), projection, options)
		if err != nil {
			test.Fatal("real fixed source module/host parity rejected valid candidate")
		}
		defer candidates.Destroy()
		built, err := candidates.View()
		if err != nil || built.SourceDigest != view.SourceDigest || len(built.Repositories) != len(view.Repositories) || len(built.Skills) != len(view.Skills) || len(built.Configurations) != len(view.Configurations) {
			test.Fatal("adapter changed source inventory")
		}
		var logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		logger.Info("synthetic", "projection", projection, "candidates", candidates)
		for _, value := range []any{projection, candidates} {
			for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
				if strings.Contains(fmt.Sprintf(format, value), "example") {
					test.Fatal("opaque formatter leaked a safe payload")
				}
			}
			encoded, err := json.Marshal(value)
			if err != nil || bytes.Contains(encoded, []byte("example")) {
				test.Fatal("opaque JSON leaked payload")
			}
		}
		encoded, err := json.Marshal(built)
		if err != nil || bytes.Contains(encoded, []byte("SYNTHETIC-PRIVATE-MARKER")) || bytes.Contains(logs.Bytes(), []byte("SYNTHETIC-PRIVATE-MARKER")) {
			test.Fatal("private source content escaped projection/log boundary")
		}
		digest, err := importer.SelectionAdapterDigest(source)
		if err != nil || len(digest) != 64 {
			test.Fatal("fixed adapter identity unavailable")
		}
	}
}

func TestSelectionContextAndClosedOptions(test *testing.T) {
	projection := selectionProviderSource(test, "claude", []byte(`{"env":{}}`))
	view, _ := projection.View()
	for _, option := range []importer.SelectionCandidateOptions{
		{CandidateID: strings.Repeat("a", 64), ClaudeAuthMode: new(configuration.APIKey)},
		{CandidateID: view.Configurations[0].ID, ClaudeAuthMode: new(configuration.AuthMode("future"))},
		{CandidateID: view.Configurations[0].ID, Name: new("  silently-trimmed  ")},
	} {
		if result, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{option}}); err == nil {
			result.Destroy()
			test.Fatal("unknown ID/auth/implicit name normalization accepted")
		}
	}
	option := importer.SelectionCandidateOptions{CandidateID: view.Configurations[0].ID, ClaudeAuthMode: new(configuration.APIKey)}
	if result, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{option, option}}); err == nil {
		result.Destroy()
		test.Fatal("duplicate candidate options accepted")
	}
	for _, expired := range []bool{false, true} {
		ctx, cancel := context.WithCancel(test.Context())
		wanted := context.Canceled
		if expired {
			cancel()
			ctx, cancel = context.WithDeadline(test.Context(), time.Unix(0, 0))
			wanted = context.DeadlineExceeded
		}
		cancel()
		if _, err := importer.BuildSelection(ctx, projection, importer.SelectionOptions{}); !errors.Is(err, wanted) {
			test.Fatal("Build context sentinel lost")
		}
		if _, err := importer.ProjectSelection(ctx, importer.SelectionMagpie, selectionFixture(test, "magpie.json")); !errors.Is(err, wanted) {
			test.Fatal("Project context sentinel lost")
		}
	}
	if _, err := importer.SelectionAdapterDigest(importer.SelectionSource("unknown")); !errors.Is(err, importer.ErrSelectionUnsupported) {
		test.Fatal("unknown source admitted")
	}
}

func TestSelectionOwnedCopyAndSkillBinding(test *testing.T) {
	projection, err := importer.ProjectSelection(test.Context(), importer.SelectionMagpie, selectionFixture(test, "magpie.json"))
	if err != nil {
		test.Fatal("projection unavailable")
	}
	defer projection.Destroy()
	view, _ := projection.View()
	binding := importer.SelectionSkillBinding{Repository: "example/toolkit", Branch: "", RepositoryID: "repo-owned", ArchiveHash: strings.Repeat("a", 64), SkillID: "skill-owned", Path: "skills/review", ContentHash: strings.Repeat("b", 64)}
	option := importer.SelectionCandidateOptions{CandidateID: view.Skills[0].ID, SkillBinding: &binding}
	candidates, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{option}})
	if err != nil {
		test.Fatal("explicit same-repository binding unavailable")
	}
	clone := candidates.Clone()
	defer clone.Destroy()
	binding.Path = "mutated"
	first, _ := candidates.View()
	if first.Skills[0].Status != importer.SelectionReady || first.Skills[0].Binding == nil || first.Skills[0].Binding.Path != "skills/review" || len(first.Skills[0].Notices) != 1 || first.Skills[0].Notices[0].Code != importer.SelectionContentNotCompared {
		test.Fatal("binding was guessed, aliased or falsely content-compared")
	}
	first.Skills[0].Binding.Path = "mutated-view"
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 10 {
				view, err := clone.View()
				if err != nil || view.Skills[0].Binding.Path != "skills/review" {
					test.Error("owned candidate view leaked mutable aliases")
				}
			}
		})
	}
	group.Wait()
	candidates.Destroy()
	if _, err := candidates.View(); !errors.Is(err, importer.ErrSelectionInvalid) {
		test.Fatal("destroyed candidates remained readable")
	}
	if _, err := clone.View(); err != nil {
		test.Fatal("independent clone destroyed by alias")
	}
	wrong := binding
	wrong.Repository = "other/repository"
	if result, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{{CandidateID: view.Skills[0].ID, SkillBinding: &wrong}}}); err == nil {
		result.Destroy()
		test.Fatal("cross-repository binding accepted")
	}
	projection.Destroy()
	if result, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{}); err == nil {
		result.Destroy()
		test.Fatal("destroyed source projection used")
	}
}

func TestSelectionLimitsAndUnsupportedProfiles(test *testing.T) {
	for _, sample := range []struct {
		source importer.SelectionSource
		raw    []byte
		wanted error
	}{
		{importer.SelectionMagpie, nil, importer.ErrSelectionInvalid},
		{importer.SelectionMagpie, bytes.Repeat([]byte{' '}, importer.MaxMagpieExportBytes+1), importer.ErrSelectionLimit},
		{importer.SelectionCCSwitch24, bytes.Repeat([]byte{0}, importer.MaxCCSwitchSnapshotBytes+1), importer.ErrSelectionLimit},
		{importer.SelectionSource("future"), []byte(`{}`), importer.ErrSelectionUnsupported},
		{importer.SelectionMagpie, []byte(`{"skills":null}`), importer.ErrSelectionInvalid},
		{importer.SelectionMagpie, []byte(`{"skills":[],"mcp":[`), importer.ErrSelectionInvalid},
	} {
		if result, err := importer.ProjectSelection(test.Context(), sample.source, sample.raw); !errors.Is(err, sample.wanted) {
			result.Destroy()
			test.Fatal("closed source/limit failure lost its safe classification")
		}
	}
}
