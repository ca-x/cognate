//go:build linux

package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func newNPXSelectionCommandRPC(test *testing.T) *selectionCommandRPC {
	test.Helper()
	fixture := newSelectionCommandRPC(test)
	data := []byte(`{"version":3,"skills":{"selected-guide":{"source":"example/toolkit","sourceType":"github","skillPath":"guides/exact/SKILL.md"}},"private":"SYNTHETIC_SOURCE_CREDENTIAL"}`)
	if err := os.WriteFile(fixture.path, data, 0o600); err != nil {
		test.Fatal("cannot create synthetic global lock")
	}
	fixture.hash = fmt.Sprintf("%x", sha256.Sum256(data))
	fixture.preview.Configurations = nil
	fixture.preview.Notices[0].Code = importv1.ImportNoticeCode_IMPORT_NOTICE_CODE_METADATA_EXCLUDED
	return fixture
}

func TestImportSelectionNPXDefaultPreview(test *testing.T) {
	fixture := newNPXSelectionCommandRPC(test)
	var output bytes.Buffer
	if err := selectionCommandRun(test.Context(), fixture.arguments("--source", "npx-skills"), strings.NewReader(selectionCommandSession), &output); err != nil {
		test.Fatalf("npx global lock preview failed: %v", err)
	}
	if fixture.previews.Load() != 1 || fixture.commits.Load() != 0 || fixture.statuses.Load() != 0 || fixture.legacy.Load() != 0 {
		test.Fatal("default npx preview dispatched a write or wrong RPC")
	}
	fixture.mutex.Lock()
	request := fixture.lastPreview
	fixture.mutex.Unlock()
	if request == nil || request.Source != importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_NPX_SKILLS_LOCK || fmt.Sprintf("%x", sha256.Sum256(request.SourceData)) != fixture.hash || len(request.Options) != 0 {
		test.Fatal("npx preview changed the explicit file, source or options")
	}
	for _, text := range []string{fixture.hash, "REPOSITORY 1", "SKILL 1", "METADATA_EXCLUDED", "SOURCE_CONTENT_NOT_COMPARED"} {
		if !strings.Contains(output.String(), text) {
			test.Error("npx preview omitted a safe selection or exclusion")
		}
	}
	selectionCommandPrivate(test, fixture, output.String())
	if entries, err := os.ReadDir(fixture.home); err != nil || len(entries) != 0 {
		test.Fatal("npx preview wrote client state")
	}
}

func TestImportSelectionNPXHelpAndSourceValidation(test *testing.T) {
	var output bytes.Buffer
	if err := selectionCommandRun(test.Context(), []string{"import", "select", "--help"}, strings.NewReader(""), &output); err != nil {
		test.Fatal("selection help failed")
	}
	for _, text := range []string{"npx-skills", "v3", "1 MiB"} {
		if !strings.Contains(output.String(), text) {
			test.Errorf("selection help omits %s", text)
		}
	}
	fixture := newNPXSelectionCommandRPC(test)
	output.Reset()
	err := selectionCommandRun(test.Context(), fixture.arguments("--source", "private-invalid-source"), strings.NewReader(selectionCommandSession), &output)
	if err == nil || !strings.Contains(err.Error(), "npx-skills") || fixture.previews.Load() != 0 || fixture.commits.Load() != 0 {
		test.Fatal("invalid source did not report npx-skills before dispatch")
	}
	selectionCommandPrivate(test, fixture, err.Error()+output.String())
}

func TestImportSelectionNPXValidationIsPrivate(test *testing.T) {
	for _, extra := range [][]string{{"--apply"}, {"--apply", "--expected-sha256", strings.Repeat("0", 64)}, {"--skills", "1"}, {"--expected-sha256", strings.Repeat("0", 64)}, {"--ack-notice", strings.Repeat("f", 64)}, {"--token-stdin=false"}, {"--file", ""}} {
		fixture := newNPXSelectionCommandRPC(test)
		var output bytes.Buffer
		arguments := fixture.arguments("--source", "npx-skills")
		err := selectionCommandRun(test.Context(), append(arguments, extra...), strings.NewReader(selectionCommandSession), &output)
		if err == nil || fixture.previews.Load() != 0 || fixture.commits.Load() != 0 || fixture.legacy.Load() != 0 {
			test.Fatal("invalid npx flags dispatched a source or a write")
		}
		selectionCommandPrivate(test, fixture, err.Error()+output.String())
	}
	for _, input := range []string{"", "private-invalid-session", strings.Repeat("U", 43) + "." + strings.Repeat("V", 43)} {
		fixture := newNPXSelectionCommandRPC(test)
		var output bytes.Buffer
		err := selectionCommandRun(test.Context(), fixture.arguments("--source", "npx-skills"), strings.NewReader(input), &output)
		if err == nil || fixture.commits.Load() != 0 || fixture.legacy.Load() != 0 {
			test.Fatal("invalid owner session authorized npx import")
		}
		selectionCommandPrivate(test, fixture, err.Error()+output.String())
		if input != "" && strings.Contains(err.Error()+output.String(), input) {
			test.Fatal("npx validation exposed a private session")
		}
	}
}

func TestImportSelectionNPXTTYConsentAndUnknownStatus(test *testing.T) {
	for _, mode := range []string{"confirm", "decline", "no-tty", "stdin-yes", "changed-file", "unknown"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newNPXSelectionCommandRPC(test)
			fixture.lost = mode == "unknown"
			option := &importv1.ImportCandidateOptions{CandidateId: strings.Repeat("b", 64), SkillBinding: proto.Clone(fixture.preview.Skills[0].Binding).(*importv1.SkillBinding)}
			data, err := protojson.Marshal(&importv1.PreviewImportSelectionRequest{Options: []*importv1.ImportCandidateOptions{option}})
			if err != nil {
				test.Fatal("cannot encode npx skill binding")
			}
			optionsPath := filepath.Join(test.TempDir(), "private-npx-options.json")
			if err := os.WriteFile(optionsPath, data, 0o600); err != nil {
				test.Fatal("cannot write npx skill binding")
			}
			arguments := fixture.arguments("--source", "npx-skills", "--options-file", optionsPath, "--apply", "--expected-sha256", fixture.hash, "--repositories", "1", "--skills", "1", "--ack-notice", strings.Repeat("f", 64), "--ack-notice", strings.Repeat("9", 64))
			var master, slave *os.File
			if mode != "no-tty" && mode != "stdin-yes" {
				master, slave = nativeTestPTY(test)
			}
			command, capture, cancel := selectionCommandChild(test, fixture, arguments, slave, false, mode == "stdin-yes")
			defer cancel()
			var operation string
			if master != nil {
				select {
				case operation = <-capture.operation:
				case <-time.After(3 * time.Second):
					cancel()
					_ = command.Wait()
					test.Fatal("npx operation was not published before consent")
				}
				approval := selectionCommandPrompt(test, master, operation)
				for _, text := range []string{"BINDING repository_id=repository-pin", "logical_path=guides/exact/SKILL.md", "SELECTED repositories=[1] skills=[1] configurations=[]", "SOURCE_CONTENT_NOT_COMPARED"} {
					if !strings.Contains(approval, text) {
						test.Errorf("npx controlling-TTY approval omits %s", text)
					}
				}
				selectionCommandPrivate(test, fixture, approval)
				if fixture.commits.Load() != 0 {
					test.Fatal("npx committed before exact TTY consent")
				}
				if mode == "changed-file" {
					if err := os.WriteFile(fixture.path, []byte(`{"version":3,"skills":{}}`), 0o600); err != nil {
						test.Fatal("cannot change synthetic npx lock")
					}
				}
				answer := "import " + operation + "\n"
				if mode == "decline" {
					answer = "yes\n"
				}
				if _, err := master.Write([]byte(answer)); err != nil {
					test.Fatal("cannot answer npx confirmation")
				}
			}
			err = command.Wait()
			committed := mode == "confirm" || mode == "unknown"
			if (err == nil) != (mode == "confirm") || (fixture.commits.Load() == 1) != committed || fixture.commits.Load() > 1 || fixture.legacy.Load() != 0 {
				test.Fatal("npx consent, source pin or one-shot commit boundary changed")
			}
			selectionCommandPrivate(test, fixture, capture.text())
			if strings.Contains(capture.text(), optionsPath) {
				test.Fatal("npx output exposed the private options path")
			}
			if committed {
				fixture.mutex.Lock()
				request := fixture.lastCommit
				fixture.mutex.Unlock()
				if request == nil || request.Source != importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_NPX_SKILLS_LOCK || fmt.Sprintf("%x", sha256.Sum256(request.SourceData)) != fixture.hash || fmt.Sprintf("%x", request.OperationId) != operation || len(request.Options) != 1 || !proto.Equal(request.Options[0], option) || len(request.ConfigurationIds) != 0 {
					test.Fatal("npx commit changed the pinned source, operation or explicit skill binding")
				}
			}
			if mode == "unknown" {
				if !strings.Contains(capture.text(), "IMPORT_COMMIT_OBSERVATION_UNKNOWN") {
					test.Fatal("npx lost response did not report Unknown")
				}
				if err := os.Remove(fixture.path); err != nil {
					test.Fatal("cannot remove synthetic lock before read-only status")
				}
				before := fixture.previews.Load()
				var output bytes.Buffer
				statusArguments := []string{"import", "status", "--server", fixture.server.URL, "--operation-id", operation, "--token-stdin"}
				if err := selectionCommandRun(test.Context(), statusArguments, strings.NewReader(selectionCommandSession), &output); err != nil {
					test.Fatal("npx status did not recover independently of the source file")
				}
				if fixture.commits.Load() != 1 || fixture.previews.Load() != before || fixture.statuses.Load() != 1 || !strings.Contains(output.String(), "SELECTION_VERIFIED false") {
					test.Fatal("npx Unknown reconciliation replayed business or overclaimed verification")
				}
				selectionCommandPrivate(test, fixture, output.String())
			}
		})
	}
}
