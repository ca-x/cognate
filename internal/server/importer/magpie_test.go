package importer_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ca-x/skillhub/internal/server/importer"
)

func TestMagpiePreviewSourceBackedFixture(t *testing.T) {
	input := magpieFixture(t, "library.json")
	original := string(input)
	preview, err := importer.PreviewMagpie(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	want := importer.MagpiePreview{
		Found: true,
		Skills: []importer.MagpieSkill{
			{Name: "review-guide", Source: &importer.MagpieSource{Kind: "github", Repo: "example/toolkit", Path: "skills/review"}},
			{Name: "hidden-guide", Source: &importer.MagpieSource{Kind: "github", Repo: "example/toolkit", Path: ".claude/skills/hidden"}},
			{Name: "release-guide", Source: &importer.MagpieSource{Kind: "github", Repo: "example/toolkit", Ref: "release/v2"}},
			{Name: "lowercase-guide", Source: &importer.MagpieSource{Kind: "github", Repo: "example/toolkit", Ref: "head", Path: "skills/head"}},
			{Name: "git-guide", Source: &importer.MagpieSource{Kind: "git"}},
			{Name: "folder-guide", Source: &importer.MagpieSource{Kind: "folder"}},
			{Name: "untracked-guide"},
		},
		Repositories: []importer.MagpieRepository{
			{Repo: "example/toolkit"},
			{Repo: "example/toolkit", Ref: "release/v2"},
			{Repo: "example/toolkit", Ref: "head"},
		},
	}
	if !reflect.DeepEqual(preview, want) {
		t.Fatalf("preview = %+v, want %+v", preview, want)
	}
	if string(input) != original {
		t.Fatal("preview mutated the caller's export")
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"MAGPIE_FIXTURE_SECRET", "MAGPIE_FIXTURE_PRIVATE", "do-not-execute", "not-an-installed-record", "not-a-source", "ignored-content-hash"} {
		if strings.Contains(string(encoded), excluded) {
			t.Fatal("preview included data outside installed skill references")
		}
	}
	for range 3 {
		repeated, err := importer.PreviewMagpie(t.Context(), input)
		if err != nil || !reflect.DeepEqual(repeated, want) {
			t.Fatalf("non-deterministic preview, error = %v", err)
		}
	}
}

func TestMagpiePreviewMissingAndEmptyLibrary(t *testing.T) {
	missing, err := importer.PreviewMagpie(t.Context(), nil)
	if err != nil || missing.Found || len(missing.Skills) != 0 || len(missing.Repositories) != 0 {
		t.Fatalf("missing preview = %+v, error = %v", missing, err)
	}
	for _, input := range [][]byte{magpieFixture(t, "empty-library.json"), []byte(`{"skills":[]}`)} {
		empty, err := importer.PreviewMagpie(t.Context(), input)
		if err != nil || !empty.Found || len(empty.Skills) != 0 || len(empty.Repositories) != 0 {
			t.Fatalf("empty preview = %+v, error = %v", empty, err)
		}
	}
}

func TestMagpiePreviewRejectsMalformedExport(t *testing.T) {
	for _, name := range []string{"malformed-library.json", "truncated-library.json"} {
		t.Run(name, func(t *testing.T) {
			assertMagpieRejected(t, magpieFixture(t, name))
		})
	}
	for _, input := range []string{
		``, `null`, `[]`, `{}`, `{"Skills":[]}`, `{"skills":{}}`, `{"skills":false}`,
		`{"skills":[null]}`, `{"skills":[{}]}`, `{"skills":[],"skills":[]}`,
		`{"skills":[{"name":"one","name":"two"}]}`,
		`{"skills":[{"name":"one"},{"name":"one"}]}`,
		`{"skills":[{"name":"one","source":[]}]}`,
		`{"skills":[{"name":"one","source":{}}]}`,
		`{"skills":[{"name":"one","source":{"kind":"unknown"}}]}`,
		`{"skills":[{"name":"one","source":{"kind":"github","repo":42}}]}`,
		`{"skills":[{"name":"one","source":{"kind":"github","repo":"example/repo","ref":true}}]}`,
		`{"skills":[{"name":"one","source":{"kind":"github","repo":"example/repo","path":[]}}]}`,
		`{"skills":[]} {"MAGPIE_FIXTURE_SECRET":true}`,
	} {
		assertMagpieRejected(t, []byte(input))
	}
	assertMagpieRejected(t, []byte("{\"skills\":[{\"name\":\"\xff\"}]}"))
}

func TestMagpiePreviewRejectsUnsafeReferences(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape", "/absolute", `nested\skill`, "one\ntwo", "one\u202etwo", strings.Repeat("x", 257)} {
		assertMagpieRejected(t, magpieExport(t, []importer.MagpieSkill{{Name: name}}))
	}
	for _, repo := range []string{"", "example", "example/repo/other", "../repo", "example/..", "https://github.com/example/repo", "user:MAGPIE_FIXTURE_SECRET@example/repo", "example/repo?token=MAGPIE_FIXTURE_SECRET", strings.Repeat("x", 513)} {
		assertMagpieRejected(t, magpieExport(t, []importer.MagpieSkill{{Name: "one", Source: &importer.MagpieSource{Kind: "github", Repo: repo}}}))
	}
	for _, ref := range []string{"../main", "main\n", "main\u202e", "-option", "a.lock", "a//b", "a@{b", "a b", strings.Repeat("x", 257)} {
		assertMagpieRejected(t, magpieExport(t, []importer.MagpieSkill{{Name: "one", Source: &importer.MagpieSource{Kind: "github", Repo: "example/repo", Ref: ref}}}))
	}
	for _, path := range []string{"..", "../escape", "skills/../escape", "/absolute", `skills\escape`, "C:/escape", "skills//one", "skills/./one", "skills/one\n", "skills/one\u202e", strings.Repeat("x", 1025)} {
		assertMagpieRejected(t, magpieExport(t, []importer.MagpieSkill{{Name: "one", Source: &importer.MagpieSource{Kind: "github", Repo: "example/repo", Path: path}}}))
	}
}

func TestMagpiePreviewBounds(t *testing.T) {
	input := []byte(`{"skills":[]}`)
	input = append(input, []byte(strings.Repeat(" ", importer.MaxMagpieExportBytes-len(input)))...)
	preview, err := importer.PreviewMagpie(t.Context(), input)
	if err != nil || !preview.Found {
		t.Fatalf("exact byte limit rejected, error = %v", err)
	}
	assertMagpieRejected(t, append(input, ' '))
	skills := make([]importer.MagpieSkill, importer.MaxMagpieSkills)
	for index := range skills {
		skills[index].Name = "skill-" + strconv.Itoa(index)
	}
	preview, err = importer.PreviewMagpie(t.Context(), magpieExport(t, skills))
	if err != nil || len(preview.Skills) != importer.MaxMagpieSkills {
		t.Fatalf("exact skill limit rejected, error = %v", err)
	}
	skills = append(skills, importer.MagpieSkill{Name: "one-too-many"})
	assertMagpieRejected(t, magpieExport(t, skills))
	assertMagpieRejected(t, []byte(`{"skills":[`+strings.Repeat(`{},`, 100000)+`{}]}`))
}

func TestMagpiePreviewCanceledAndConcurrent(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, input := range [][]byte{nil, magpieFixture(t, "library.json")} {
		preview, err := importer.PreviewMagpie(ctx, input)
		if !errors.Is(err, context.Canceled) || preview.Found {
			t.Fatalf("canceled preview = %+v, error = %v", preview, err)
		}
	}
	input := magpieFixture(t, "library.json")
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			preview, err := importer.PreviewMagpie(t.Context(), input)
			if err != nil || !preview.Found || len(preview.Skills) != 7 || len(preview.Repositories) != 3 {
				t.Errorf("concurrent preview failed, error = %v", err)
			}
		})
	}
	workers.Wait()
}

func magpieFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/magpie/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func magpieExport(t *testing.T, skills []importer.MagpieSkill) []byte {
	t.Helper()
	data, err := json.Marshal(struct {
		Skills []importer.MagpieSkill `json:"skills"`
	}{Skills: skills})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertMagpieRejected(t *testing.T, input []byte) {
	t.Helper()
	preview, err := importer.PreviewMagpie(t.Context(), input)
	if err == nil || preview.Found || len(preview.Skills) != 0 || len(preview.Repositories) != 0 {
		t.Fatalf("malformed export not rejected atomically, error = %v", err)
	}
	for _, private := range []string{"MAGPIE_FIXTURE_SECRET", "MAGPIE_FIXTURE_PRIVATE", string(input)} {
		if private != "" && strings.Contains(err.Error(), private) {
			t.Fatal("preview error exposed source data")
		}
	}
}
