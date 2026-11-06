package httpapi

import (
	"fmt"
	"testing"

	"github.com/ca-x/cognate/internal/server/importer"
)

func TestSelectionOpenCodeRealConnectClient(test *testing.T) {
	settings := `{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://opencode.example/v1","apiKey":"SYNTHETIC-PRIVATE-MARKER","headers":{"Authorization":"SYNTHETIC-PRIVATE-MARKER"}},"models":{"a":{"limit":{"context":9007199254740991,"output":4096},"modalities":{"input":["text","image"],"output":["text"]}}}}`
	for _, version := range []int{23, 24} {
		for _, lost := range []bool{false, true} {
			test.Run(fmt.Sprintf("schema%d/lost-%t", version, lost), func(test *testing.T) {
				statement := fmt.Sprintf(`PRAGMA user_version=%d; INSERT INTO providers VALUES('opencode-source','opencode','OpenCode imported','%s','{"opencodeConfigFormat":"v1"}',NULL,NULL,0,0,NULL,NULL,NULL,0,0);`, version, settings)
				projection, err := importer.ProjectSelection(test.Context(), importer.SelectionCCSwitch24, importRoundtripSnapshot(test, statement))
				if err != nil {
					test.Fatal("projection failed", err)
				}
				defer projection.Destroy()
				built, err := importer.BuildSelection(test.Context(), projection, importer.SelectionOptions{})
				if err != nil {
					test.Fatal("adapter failed", err)
				}
				built.Destroy()
				selectionClientRealStore(test, 4, lost, statement)
			})
		}
	}
}
