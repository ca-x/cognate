package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"testing"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/server/importer"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/proto"
)

func importPreviewMappingBatch(test *testing.T, repositories, skills, configurations int) importer.SelectionPreviewView {
	test.Helper()
	view := importPreviewMappingFixture(test)
	view.Repositories = slices.Repeat(view.Repositories, repositories)
	view.Skills = slices.Repeat(view.Skills, skills)
	view.Configurations = slices.Repeat(view.Configurations, (configurations+2)/3)[:configurations]
	for index := range view.Repositories {
		view.Repositories[index].ID = fmt.Sprintf("%064x", index+1)
	}
	for index := range view.Skills {
		view.Skills[index].ID = fmt.Sprintf("%064x", index+10000)
	}
	for index := range view.Configurations {
		view.Configurations[index].ID = fmt.Sprintf("%064x", index+20000)
	}
	return view
}

func TestImportSelectionPreviewMappingIndependentLimits(test *testing.T) {
	for _, counts := range [][3]int{{75, 81, 18}, {importer.MaxSelectionPreviewRepositories, importer.MaxSelectionPreviewSkills, importer.MaxSelectionPreviewConfigurations}} {
		test.Run(fmt.Sprintf("%d-%d-%d", counts[0], counts[1], counts[2]), func(test *testing.T) {
			view := importPreviewMappingBatch(test, counts[0], counts[1], counts[2])
			actual, err := importSelectionPreviewResponse(view, make([]byte, state.ImportTicketBytes))
			if err != nil || actual == nil || len(actual.Repositories) != counts[0] || len(actual.Skills) != counts[1] || len(actual.Configurations) != counts[2] {
				test.Fatal("valid preview was rejected or truncated at a commit-only limit")
			}
			if actual.Repositories[counts[0]-1].Id != view.Repositories[counts[0]-1].ID || actual.Skills[counts[1]-1].Id != view.Skills[counts[1]-1].ID || actual.Configurations[counts[2]-1].Id != view.Configurations[counts[2]-1].ID {
				test.Fatal("preview did not preserve the final candidates")
			}
		})
	}
	for _, counts := range [][3]int{{importer.MaxSelectionPreviewRepositories + 1, 1, 1}, {1, importer.MaxSelectionPreviewSkills + 1, 1}, {1, 1, importer.MaxSelectionPreviewConfigurations + 1}} {
		view := importPreviewMappingBatch(test, counts[0], counts[1], counts[2])
		actual, err := importSelectionPreviewResponse(view, make([]byte, state.ImportTicketBytes))
		if err == nil || actual != nil {
			test.Fatal("oversized preview returned full or partial output")
		}
	}
	for name, mutate := range map[string]func(*importer.SelectionPreviewView){
		"last-repository-status": func(view *importer.SelectionPreviewView) {
			view.Repositories[len(view.Repositories)-1].Status = 99
		},
		"last-skill-status": func(view *importer.SelectionPreviewView) { view.Skills[len(view.Skills)-1].Status = 99 },
		"last-configuration-kind": func(view *importer.SelectionPreviewView) {
			view.Configurations[len(view.Configurations)-1].Kind = "invalid"
		},
		"last-configuration-draft": func(view *importer.SelectionPreviewView) { view.Configurations[len(view.Configurations)-1].Draft = nil },
		"last-configuration-notice": func(view *importer.SelectionPreviewView) {
			view.Configurations[len(view.Configurations)-1].Notices = []state.ImportReceiptNotice{{ID: "invalid", Code: 99, Count: 1}}
		},
	} {
		test.Run(name, func(test *testing.T) {
			view := importPreviewMappingBatch(test, 75, 81, 18)
			mutate(&view)
			actual, err := importSelectionPreviewResponse(view, make([]byte, state.ImportTicketBytes))
			if err == nil || actual != nil {
				test.Fatal("large preview bypassed per-candidate validation or returned partial output")
			}
		})
	}
}

func importPreviewBatchSnapshot(test *testing.T, repositories, skills, providers int) []byte {
	test.Helper()
	return importRoundtripSnapshot(test, fmt.Sprintf(`
DELETE FROM skill_repos;
DELETE FROM skills;
DELETE FROM providers;
WITH RECURSIVE entries(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM entries WHERE i+1<%d)
INSERT INTO skill_repos(owner,name,branch,enabled) SELECT 'example',printf('synthetic-%%04d',i),'HEAD',1 FROM entries;
WITH RECURSIVE entries(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM entries WHERE i+1<%d)
INSERT INTO skills(id,name,repo_owner,repo_name,repo_branch) SELECT printf('skill-%%04d',i),printf('Synthetic skill %%04d',i),'example','synthetic-0000','HEAD' FROM entries;
WITH RECURSIVE entries(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM entries WHERE i+1<%d)
INSERT INTO providers(id,app_type,name,settings_config,meta,is_current,in_failover_queue)
SELECT printf('provider-%%04d',i),'claude',printf('Synthetic provider %%04d',i),'{'||'"env":{"ANTHROPIC_BASE_URL":"https://synthetic.example/v1","ANTHROPIC_API_KEY":"SYNTHETIC-PRIVATE-MARKER"}}','{}',0,0 FROM entries;
`, repositories, skills, providers))
}

func TestImportSelectionCommitLimitsRemainIndependent(test *testing.T) {
	for _, scenario := range []struct {
		name  string
		group int
		limit int
	}{
		{"repositories", 0, 16}, {"skills", 1, 128}, {"configurations", 2, 32},
	} {
		for _, extra := range []int{0, 1} {
			test.Run(fmt.Sprintf("%s-%d", scenario.name, scenario.limit+extra), func(test *testing.T) {
				identifiers := make([]string, scenario.limit+extra)
				for index := range identifiers {
					identifiers[index] = fmt.Sprintf("%064x", index+1)
				}
				input := importer.SelectionCommitInput{Ticket: make([]byte, state.ImportTicketBytes), Source: importer.SelectionMagpie, SourceData: []byte(`{"skills":[]}`), OperationID: [16]byte{1}}
				switch scenario.group {
				case 0:
					input.RepositoryIDs = identifiers
				case 1:
					input.SkillIDs = identifiers
				case 2:
					input.ConfigurationIDs = identifiers
				}
				request, err := importer.NewSelectionCommitRequest(input)
				defer request.Destroy()
				if extra == 0 && err != nil || extra == 1 && !errors.Is(err, importer.ErrSelectionInvalid) {
					test.Fatal("commit cardinality limits changed with preview limits")
				}
			})
		}
	}
}

func TestImportSelectionLargeCCSwitchHTTPPreview(test *testing.T) {
	for _, encoding := range []string{"proto", "json"} {
		for _, scenario := range []struct {
			name                            string
			repositories, skills, providers int
			reject                          bool
		}{
			{"realistic-batch", 75, 81, 16, false},
			{"preview-boundaries", importer.MaxSelectionPreviewRepositories, importer.MaxSelectionPreviewSkills, importer.MaxSelectionProviders, false},
			{"repositories-over-boundary", importer.MaxSelectionPreviewRepositories + 1, 1, 1, true},
			{"skills-over-boundary", 1, importer.MaxSelectionPreviewSkills + 1, 1, true},
			{"providers-over-boundary", 1, 1, importer.MaxSelectionProviders + 1, true},
		} {
			test.Run(encoding+"/"+scenario.name, func(test *testing.T) {
				fixture := newNPXSelectionHTTPFixture(test)
				client := fixture.client(test, encoding, fixture.sessions[0].Reveal())
				raw := importPreviewBatchSnapshot(test, scenario.repositories, scenario.skills, scenario.providers)
				defer clear(raw)
				response, err := client.PreviewImportSelection(test.Context(), connect.NewRequest(&importv1.PreviewImportSelectionRequest{Source: importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24, SourceData: raw}))
				if scenario.reject {
					if err == nil || response != nil {
						test.Fatal("oversized source preview was not rejected in full")
					}
				} else {
					if err != nil {
						test.Fatalf("large real HTTP preview failed: %s", connect.CodeOf(err))
					}
					if len(response.Msg.Repositories) != scenario.repositories || len(response.Msg.Skills) != scenario.skills || len(response.Msg.Configurations) != scenario.providers+importer.MaxSelectionPreviewConfigurations-importer.MaxSelectionProviders || len(response.Msg.Ticket) != state.ImportTicketBytes {
						test.Fatal("real HTTP preview rejected or truncated candidates")
					}
					encoded, err := proto.Marshal(response.Msg)
					if err != nil || bytes.Contains(encoded, []byte("SYNTHETIC-PRIVATE-MARKER")) {
						test.Fatal("preview response contained excluded source data")
					}
				}
				if fixture.downloads.Load() != 0 || fixture.observed[0].Load() != 1 || fixture.observed[1].Load() != 0 || fixture.observed[2].Load() != 0 {
					test.Fatal("preview performed downloads or non-preview operations")
				}
				assets, assetErr := fixture.store.ListConfigurationAssets(test.Context(), fixture.owners[0])
				repositories, repositoryErr := fixture.store.ListCollectedRepositories(test.Context(), fixture.owners[0])
				if assetErr != nil || repositoryErr != nil || len(assets) != 0 || len(repositories) != 0 {
					test.Fatal("preview created persistent assets or repositories")
				}
			})
		}
	}
}
