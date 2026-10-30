package codexconfig

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

type bundleGolden struct {
	Qualification           string         `json:"qualification"`
	Config                  string         `json:"config"`
	Auth                    string         `json:"auth"`
	Catalog                 string         `json:"catalog"`
	Secret                  string         `json:"secret"`
	ExpectedCatalog         string         `json:"expectedCatalog"`
	ExpectedConfig          string         `json:"expectedConfig"`
	ExpectedPreview         []FieldPreview `json:"expectedPreview"`
	ExpectedBaselineDigests [3]string      `json:"expectedBaselineDigests"`
	ConfigNewlineDigest     string         `json:"configNewlineDigest"`
}

func goldenBundle(test *testing.T) bundleGolden {
	test.Helper()
	raw, err := os.ReadFile("testdata/bundle-v1-golden.json")
	var fixture bundleGolden
	if err != nil || json.Unmarshal(raw, &fixture, json.RejectUnknownMembers(true)) != nil || fixture.Qualification != "synthetic-only-not-native-profile-approval" {
		test.Fatal("bundle golden")
	}
	return fixture
}

func TestCodexCatalogAllFields(test *testing.T) {
	fixture := goldenBundle(test)
	image, _ := NewImage(Present, []byte(fixture.Catalog))
	output, _, removed, err := rewriteCatalog(test.Context(), image, goldenDeclaration(test), ownershipData{catalog: Managed}, syntheticProfile())
	if err != nil || removed != 0 || !bytes.Equal(output, []byte(fixture.ExpectedCatalog)) {
		test.Fatal("full eight-field native catalog golden mismatch")
	}
	declaration := goldenDeclaration(test)
	for index := 1; index < 32; index++ {
		model := declaration.Catalog.Models[0]
		model.Model = "model-" + strconv.Itoa(index)
		declaration.Catalog.Models = append(declaration.Catalog.Models, model)
	}
	if _, _, _, err := rewriteCatalog(test.Context(), image, declaration, ownershipData{catalog: Managed}, syntheticProfile()); err != nil {
		test.Fatal("32 full models rejected")
	}
}

func TestCodexCatalogPresenceAndOwnership(test *testing.T) {
	base := []byte(`{"models":[{"slug":"old","description":"old"},{"slug":"Case/model:1","description":"old","supports_parallel_tool_calls":false,"base_instructions":"  OLD\r\n ","supported_reasoning_levels":[{"effort":"none","description":"old"}],"default_reasoning_level":"none"}]}`)
	image, _ := NewImage(Present, base)
	declaration := goldenDeclaration(test)
	declaration.ReasoningEffort = nil
	declaration.Catalog.Models = []agentconfig.CodexCatalogModelDesired{{Model: "Case/model:1"}}
	output, _, removed, err := rewriteCatalog(test.Context(), image, declaration, ownershipData{catalog: Managed, models: []string{"old", "Case/model:1"}}, syntheticProfile())
	if err != nil || removed != 1 {
		test.Fatal("managed replacement failed")
	}
	for _, token := range [][]byte{[]byte(`"supports_parallel_tool_calls":false`), []byte(`"base_instructions":"  OLD\r\n "`), []byte(`"description":"old"`)} {
		if !bytes.Contains(output, token) {
			test.Fatal("optional absence overwritten")
		}
	}
	if bytes.Contains(output, []byte(`"slug":"old"`)) {
		test.Fatal("managed removal missing")
	}
	if _, _, _, err := rewriteCatalog(test.Context(), image, declaration, ownershipData{catalog: Managed, models: []string{"Case/model:1"}}, syntheticProfile()); !errors.Is(err, ErrOwnershipConflict) {
		test.Fatal("foreign model silently deleted")
	}
	missing, _ := NewImage(Missing, nil)
	if _, _, _, err := rewriteCatalog(test.Context(), missing, declaration, ownershipData{catalog: Absent}, syntheticProfile()); err != nil {
		test.Fatal("native-optional absence rejected")
	}
}

func TestCodexCatalogRawPreservation(test *testing.T) {
	base := []byte("{ \"opaque\": {\"n\":9007199254740993123456789}, \"models\": [{\"slug\":\"second\",\"description\":\"two\",\"opaque\":{\"x\":\"\\u0061\"}},{ \"slug\" : \"Case/model:1\", \"description\" : \"one\", \"opaque\" : [9007199254740993123456789]}] }")
	image, _ := NewImage(Present, base)
	declaration := goldenDeclaration(test)
	declaration.Catalog.Models = append(declaration.Catalog.Models, agentconfig.CodexCatalogModelDesired{Model: "second"})
	output, _, _, err := rewriteCatalog(test.Context(), image, declaration, ownershipData{catalog: Managed, models: []string{"second", "Case/model:1"}}, syntheticProfile())
	if err != nil {
		test.Fatal("owned ordered rewrite")
	}
	for _, token := range [][]byte{[]byte(`"opaque": {"n":9007199254740993123456789}`), []byte(`"opaque":{"x":"\u0061"}`), []byte(`"opaque" : [9007199254740993123456789]`)} {
		if !bytes.Contains(output, token) {
			test.Fatal("catalog unknown bytes normalized")
		}
	}
	if bytes.Index(output, []byte(`"Case/model:1"`)) > bytes.Index(output, []byte(`"second"`)) {
		test.Fatal("catalog order lost")
	}
	for _, body := range []string{`{"models":[{"slug":"Case/model:1","description":"x","env_key":"bad"}]}`, `{"models":[{"slug":"Case/model:1","description":"x","model_messages":{}}]}`, `{"models":[{"slug":"a","description":"x"},{"slug":"\u0061","description":"x"}]}`} {
		value, _ := NewImage(Present, []byte(body))
		if _, _, _, err := rewriteCatalog(test.Context(), value, declaration, ownershipData{catalog: Managed, models: []string{"Case/model:1", "a"}}, syntheticProfile()); err == nil {
			test.Fatal("ambiguous/actionable catalog accepted")
		}
	}
}

func TestCodexCatalogRequiredNativeFields(test *testing.T) {
	missing, _ := NewImage(Missing, nil)
	profile := syntheticProfile()
	profile.modelDefaults = []byte(`{}`)
	if _, _, _, err := rewriteCatalog(test.Context(), missing, goldenDeclaration(test), ownershipData{catalog: Absent}, profile); !errors.Is(err, ErrUnsupportedNativeProfile) {
		test.Fatal("required native default guessed")
	}
	profile = syntheticProfile()
	profile.parallel = parallelUnverified
	if _, _, _, err := rewriteCatalog(test.Context(), missing, goldenDeclaration(test), ownershipData{catalog: Absent}, profile); !errors.Is(err, ErrUnsupportedNativeProfile) {
		test.Fatal("parallel field dropped")
	}
}

func TestCodexCatalogIncompatibleRetainedValues(test *testing.T) {
	for _, literal := range []string{`1.0`, `1e0`, `"1"`, `0`, `2147483648`, `null`, `-1`} {
		image, _ := NewImage(Present, []byte(`{"models":[{"slug":"Case/model:1","description":"old","context_window":`+literal+`}]}`))
		if _, _, _, err := rewriteCatalog(test.Context(), image, goldenDeclaration(test), ownershipData{catalog: Managed, models: []string{"Case/model:1"}}, syntheticProfile()); err == nil {
			test.Fatal("non-exact native integer accepted")
		}
	}
	image, _ := NewImage(Present, []byte(`{"models":[{"slug":"Case/model:1","description":"old","supported_reasoning_levels":[{"effort":"none","description":"old"}],"default_reasoning_level":"none"}]}`))
	declaration := goldenDeclaration(test)
	declaration.ReasoningEffort = nil
	declaration.Catalog.Models[0].DefaultReasoningLevel = nil
	declaration.Catalog.Models[0].ReasoningLevels = new([]agentconfig.CodexReasoningEffort{"high"})
	if _, _, _, err := rewriteCatalog(test.Context(), image, declaration, ownershipData{catalog: Managed, models: []string{"Case/model:1"}}, syntheticProfile()); !errors.Is(err, ErrOwnershipConflict) {
		test.Fatal("incompatible retained default accepted")
	}
	missing, _ := NewImage(Missing, nil)
	profile := syntheticProfile()
	profile.modelDefaults = []byte(`{"description":"` + strings.Repeat("x", (64<<10)) + `"}`)
	if _, _, _, err := rewriteCatalog(test.Context(), missing, goldenDeclaration(test), ownershipData{catalog: Absent}, profile); !errors.Is(err, ErrCodecLimit) {
		test.Fatal("template budget ignored")
	}
}
