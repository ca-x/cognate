package httpapi

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/importer"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestImportSelectionResponseBudgets(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		for _, shape := range []struct {
			name    string
			limit   int
			message func(string) proto.Message
		}{
			{"preview", 2 << 20, func(value string) proto.Message {
				return &importv1.PreviewImportSelectionResponse{Skills: []*importv1.ImportSkillCandidate{{SafeName: value}}}
			}},
			{"commit", 2 << 20, func(value string) proto.Message {
				return &importv1.CommitImportSelectionResponse{Receipt: &importv1.ImportReceipt{CreatedAssets: []*configurationv1.Asset{{Name: value}}}}
			}},
			{"status", 128 << 10, func(value string) proto.Message {
				return &importv1.GetImportCommitStatusResponse{Receipt: &importv1.ImportReceipt{CreatedAssets: []*configurationv1.Asset{{Name: value}}}}
			}},
		} {
			test.Run(encoding+"/"+shape.name, func(test *testing.T) {
				marshal := proto.Marshal
				if encoding == "json" {
					marshal = protojson.Marshal
				}
				for _, delta := range []int{-1, 0, 1} {
					value := strings.Repeat("x", shape.limit-100)
					encoded, err := marshal(shape.message(value))
					if err != nil {
						test.Fatal("budget fixture failed to encode")
					}
					value += strings.Repeat("x", shape.limit+delta-len(encoded))
					message := shape.message(value)
					want, err := marshal(message)
					if err != nil || len(want) != shape.limit+delta {
						test.Fatal("budget fixture did not hit exact wire boundary")
					}
					actual, err := (importCodec{name: encoding}).Marshal(message)
					if delta <= 0 {
						if err != nil || !bytes.Equal(actual, want) {
							test.Fatal("in-budget response was rejected or changed")
						}
					} else if connect.CodeOf(err) != connect.CodeResourceExhausted || actual != nil {
						test.Fatal("oversized response escaped its exact encoded byte budget")
					}
				}
			})
		}
		legacy := &importv1.PreviewLibraryExportResponse{Repositories: []*importv1.RepositoryCandidate{{Repository: strings.Repeat("x", (2<<20)+100)}}}
		if _, err := (importCodec{name: encoding}).Marshal(legacy); err != nil {
			test.Fatal("new-response budget altered legacy codec behavior")
		}
	}
}

func TestImportSelectionOptionsMapping(test *testing.T) {
	options := []*importv1.ImportCandidateOptions{
		{CandidateId: strings.Repeat("a", 64), Name: new("Renamed"), Auth: &importv1.ImportCandidateOptions_ClaudeAuthMode{ClaudeAuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_AUTH_TOKEN}},
		{CandidateId: strings.Repeat("b", 64), Auth: &importv1.ImportCandidateOptions_GeminiAuthMode{GeminiAuthMode: configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_API_KEY}, GeminiKeyTransport: new(configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_BEARER)},
		{CandidateId: strings.Repeat("c", 64), SkillBinding: &importv1.SkillBinding{RepositoryId: "repo-id", ArchiveHash: strings.Repeat("d", 64), SkillId: "skill-id", LogicalPath: "nested/SKILL.md", ContentHash: strings.Repeat("e", 64), Repository: "owner/repo", Branch: "release"}},
		{CandidateId: strings.Repeat("f", 64), Name: new("")},
	}
	want := importer.SelectionOptions{Candidates: []importer.SelectionCandidateOptions{
		{CandidateID: strings.Repeat("a", 64), Name: new("Renamed"), ClaudeAuthMode: new(configuration.AuthToken)},
		{CandidateID: strings.Repeat("b", 64), GeminiAuthMode: new(agentconfig.GeminiAPIKeyAuth), GeminiKeyTransport: new(agentconfig.GeminiBearer)},
		{CandidateID: strings.Repeat("c", 64), SkillBinding: &importer.SelectionSkillBinding{RepositoryID: "repo-id", ArchiveHash: strings.Repeat("d", 64), SkillID: "skill-id", Path: "nested/SKILL.md", ContentHash: strings.Repeat("e", 64), Repository: "owner/repo", Branch: "release"}},
		{CandidateID: strings.Repeat("f", 64), Name: new("")},
	}}
	actual, err := importSelectionOptions(options)
	if err != nil || !reflect.DeepEqual(actual, want) {
		test.Fatal("option mapping lost ordering, explicit presence, enum meaning or binding fields")
	}
	*actual.Candidates[0].Name = "changed"
	actual.Candidates[2].SkillBinding.Branch = "changed"
	if options[0].GetName() != "Renamed" || options[2].GetSkillBinding().GetBranch() != "release" {
		test.Fatal("option mapping aliases caller-owned data")
	}
}

func TestImportSelectionInput(test *testing.T) {
	data := make([]byte, importer.MaxCCSwitchSnapshotBytes+1)
	for _, source := range []struct {
		wire    importv1.ImportSelectionSource
		want    importer.SelectionSource
		maximum int
	}{
		{importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24, importer.SelectionCCSwitch24, importer.MaxCCSwitchSnapshotBytes},
		{importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON, importer.SelectionMagpie, importer.MaxMagpieExportBytes},
	} {
		for _, length := range []int{1, source.maximum} {
			actual, options, err := importSelectionInput(source.wire, data[:length], []*importv1.ImportCandidateOptions{{Name: new("Renamed")}})
			if err != nil || actual != source.want || len(options.Candidates) != 1 || options.Candidates[0].Name == nil || *options.Candidates[0].Name != "Renamed" {
				test.Fatal("source, decoded byte boundary or options were not mapped")
			}
		}
		for _, length := range []int{0, source.maximum + 1} {
			actual, options, err := importSelectionInput(source.wire, data[:length], nil)
			want := importer.ErrSelectionInvalid
			if length > 0 {
				want = importer.ErrSelectionLimit
			}
			if !errors.Is(err, want) || actual != "" || len(options.Candidates) != 0 {
				test.Fatal("invalid decoded source returned output or wrong safe error")
			}
		}
	}
	for _, source := range []importv1.ImportSelectionSource{0, -1, 3, 99} {
		actual, options, err := importSelectionInput(source, data[:1], nil)
		if !errors.Is(err, importer.ErrSelectionInvalid) || actual != "" || len(options.Candidates) != 0 {
			test.Fatal("unknown source was accepted")
		}
	}
	actual, options, err := importSelectionInput(importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_MAGPIE_LIBRARY_JSON, data[:1], []*importv1.ImportCandidateOptions{nil})
	if !errors.Is(err, importer.ErrSelectionInvalid) || actual != "" || len(options.Candidates) != 0 {
		test.Fatal("invalid options leaked a partially mapped source")
	}
}

func TestImportSelectionOperation(test *testing.T) {
	value := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	actual, err := importSelectionOperation(value)
	if err != nil || actual != [16]byte(value) {
		test.Fatal("operation ID bytes were not preserved")
	}
	actual[0] = 99
	if value[0] != 1 {
		test.Fatal("operation ID aliases request bytes")
	}
	for _, invalid := range [][]byte{nil, {}, value[:15], append(append([]byte(nil), value...), 17), make([]byte, 16)} {
		actual, err := importSelectionOperation(invalid)
		if !errors.Is(err, importer.ErrSelectionInvalid) || actual != [16]byte{} {
			test.Fatal("invalid operation ID accepted or returned partial bytes")
		}
	}
}

func TestImportSelectionOptionsEnums(test *testing.T) {
	for index, mode := range []configuration.AuthMode{configuration.NativeLogin, configuration.APIKey, configuration.AuthToken} {
		actual, err := importSelectionOptions([]*importv1.ImportCandidateOptions{{Auth: &importv1.ImportCandidateOptions_ClaudeAuthMode{ClaudeAuthMode: configurationv1.ClaudeAuthMode(index + 1)}}})
		if err != nil || len(actual.Candidates) != 1 || actual.Candidates[0].ClaudeAuthMode == nil || *actual.Candidates[0].ClaudeAuthMode != mode {
			test.Fatal("Claude auth enum was not explicitly mapped")
		}
	}
	for index, mode := range []agentconfig.GeminiAuthMode{agentconfig.GeminiNativeLogin, agentconfig.GeminiAPIKeyAuth} {
		actual, err := importSelectionOptions([]*importv1.ImportCandidateOptions{{Auth: &importv1.ImportCandidateOptions_GeminiAuthMode{GeminiAuthMode: configurationv1.GeminiAuthMode(index + 1)}}})
		if err != nil || len(actual.Candidates) != 1 || actual.Candidates[0].GeminiAuthMode == nil || *actual.Candidates[0].GeminiAuthMode != mode {
			test.Fatal("Gemini auth enum was not explicitly mapped")
		}
	}
	for index, transport := range []agentconfig.GeminiKeyTransport{agentconfig.GeminiNativeDefault, agentconfig.GeminiBearer} {
		actual, err := importSelectionOptions([]*importv1.ImportCandidateOptions{{GeminiKeyTransport: new(configurationv1.GeminiKeyTransport(index + 1))}})
		if err != nil || len(actual.Candidates) != 1 || actual.Candidates[0].GeminiKeyTransport == nil || *actual.Candidates[0].GeminiKeyTransport != transport {
			test.Fatal("Gemini transport enum was not explicitly mapped")
		}
	}
}

func TestImportSelectionOptionsRejectInvalidShape(test *testing.T) {
	tooMany := make([]*importv1.ImportCandidateOptions, 161)
	for index := range tooMany {
		tooMany[index] = &importv1.ImportCandidateOptions{}
	}
	for name, options := range map[string][]*importv1.ImportCandidateOptions{
		"nil-entry":          {nil},
		"partial-before-nil": {{Name: new("private-marker")}, nil},
		"limit":              tooMany,
		"nil-claude-oneof":   {{Auth: (*importv1.ImportCandidateOptions_ClaudeAuthMode)(nil)}},
		"nil-gemini-oneof":   {{Auth: (*importv1.ImportCandidateOptions_GeminiAuthMode)(nil)}},
		"zero-claude":        {{Auth: &importv1.ImportCandidateOptions_ClaudeAuthMode{}}},
		"unknown-claude":     {{Auth: &importv1.ImportCandidateOptions_ClaudeAuthMode{ClaudeAuthMode: 99}}},
		"zero-gemini":        {{Auth: &importv1.ImportCandidateOptions_GeminiAuthMode{}}},
		"unknown-gemini":     {{Auth: &importv1.ImportCandidateOptions_GeminiAuthMode{GeminiAuthMode: 99}}},
		"zero-transport":     {{GeminiKeyTransport: new(configurationv1.GeminiKeyTransport(0))}},
		"unknown-transport":  {{GeminiKeyTransport: new(configurationv1.GeminiKeyTransport(99))}},
	} {
		test.Run(name, func(test *testing.T) {
			actual, err := importSelectionOptions(options)
			if !errors.Is(err, importer.ErrSelectionInvalid) || len(actual.Candidates) != 0 || strings.Contains(err.Error(), "private-marker") {
				test.Fatal("malformed options returned partial output or non-sanitized error")
			}
		})
	}
	maximum := tooMany[:160]
	actual, err := importSelectionOptions(maximum)
	if err != nil || len(actual.Candidates) != 160 {
		test.Fatal("exact option count boundary rejected")
	}
	actual, err = importSelectionOptions(nil)
	if err != nil || len(actual.Candidates) != 0 {
		test.Fatal("absent options must remain absent")
	}
}
