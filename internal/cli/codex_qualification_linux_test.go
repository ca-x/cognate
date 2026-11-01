package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"github.com/ca-x/cognate/internal/server/configuration"
)

func TestCodexProductionQualificationIdentity(test *testing.T) {
	id, manifest, profile, template := codexconfig.Native0166EffectiveManifest()
	test.Logf("approved-tuple id=%s manifest=%x profile=%x template=%x", id, manifest, profile, template)
	actual, err := configapply.QualifiedCodexNativeProfile()
	if runtime.GOARCH != "amd64" {
		if !errors.Is(err, configapply.ErrCodexUnsupported) || actual != (configapply.CodexProfileIdentity{}) {
			test.Fatal("unqualified architecture acquired native authority")
		}
		return
	}
	if err != nil || actual != (configapply.CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}) {
		test.Fatal("ordinary production registry did not select the approved complete tuple")
	}
}

type codexConsumerOracle struct {
	Model, Display, Instruction, DefaultEffort, WireEffort string
	RootEffort                                             *string
	ContextWindow                                          uint64
}

func codexQualificationConsumer(test *testing.T, fixture *codexCLIFixture, label string, oracle codexConsumerOracle) {
	test.Helper()
	harness, evidence := os.Getenv("COGNATE_C6_CONSUMER"), os.Getenv("COGNATE_C6_EVIDENCE")
	if harness == "" || evidence == "" {
		test.Fatal("explicit isolated consumer harness and evidence directory required")
	}
	body, err := json.Marshal(oracle)
	if err != nil {
		test.Fatal("consumer oracle")
	}
	defer clear(body)
	ctx, cancel := context.WithTimeout(test.Context(), 30*time.Second)
	defer cancel()
	receipt := filepath.Join(evidence, label+".json")
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-I", harness, fixture.home, fixture.request.Directory, fixture.request.Executable, receipt)
	command.Env = []string{"HOME=" + fixture.home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8", "TMPDIR=" + os.TempDir()}
	command.Stdin = bytes.NewReader(body)
	output, err := command.CombinedOutput()
	defer clear(output)
	if err != nil || ctx.Err() != nil {
		test.Fatalf("fixed consumer failed: receipt=%s diagnostic-sha256=%x", receipt, sha256.Sum256(output))
	}
	var result struct{ Pass bool }
	if json.Unmarshal(output, &result) != nil || !result.Pass {
		test.Fatal("fixed consumer did not return a successful bounded receipt")
	}
	test.Logf("fixed consumer receipt=%s sha256=%x", receipt, sha256.Sum256(output))
}

func codexQualificationRoles(test *testing.T, fixture *codexCLIFixture) [3][]byte {
	test.Helper()
	var roles [3][]byte
	for index, name := range []string{"config.toml", "auth.json", "cognate-model-catalog.json"} {
		body, err := os.ReadFile(filepath.Join(fixture.home, ".codex", name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			test.Fatal("read synthetic role")
		}
		roles[index] = body
	}
	test.Cleanup(func() {
		for _, body := range roles {
			clear(body)
		}
	})
	return roles
}

func codexQualificationSameRoles(test *testing.T, before, after [3][]byte) {
	test.Helper()
	for index := range before {
		if !bytes.Equal(before[index], after[index]) || (before[index] == nil) != (after[index] == nil) {
			test.Fatal("native role bytes/presence changed")
		}
	}
}

func TestCodexCLIProductionConsumer(test *testing.T) {
	if os.Getenv("COGNATE_C6_CONSUMER") == "" {
		test.Skip("explicit fixed consumer in isolated network namespace required")
	}
	for _, login := range []string{"missing", "api", "oauth"} {
		test.Run(login, func(test *testing.T) {
			fixture := newCodexCLIFixture(test, "")
			authPath := filepath.Join(fixture.home, ".codex", "auth.json")
			switch login {
			case "missing":
				if os.Remove(authPath) != nil {
					test.Fatal("synthetic missing auth")
				}
			case "api":
				if os.WriteFile(authPath, []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"SYNTHETIC_C6_OLD_API"}`), 0o600) != nil {
					test.Fatal("synthetic API auth")
				}
			}
			original := codexQualificationRoles(test, fixture)
			firstOracle := codexConsumerOracle{Model: "c6-model-a", Display: "C6 model A", Instruction: "SYNTHETIC C6 A INSTRUCTIONS", DefaultEffort: "low", WireEffort: "low", ContextWindow: 32768}
			secondOracle := codexConsumerOracle{Model: "c6-model-b", Display: "C6 model B", Instruction: "SYNTHETIC C6 B INSTRUCTIONS", DefaultEffort: "low", WireEffort: "high", RootEffort: new("high"), ContextWindow: 65536}
			var first string
			var firstRoles [3][]byte
			for index, oracle := range []codexConsumerOracle{firstOracle, secondOracle} {
				view, err := fixture.domain.Edit(test.Context(), fixture.request.OwnerID, fixture.request.AssetID)
				if err != nil {
					test.Fatal("synthetic owner edit")
				}
				provider := view.CodexProvider
				provider.Endpoint, provider.Model = "https://127.0.0.1:18443/v1", oracle.Model
				provider.Catalog.Models[0] = agentconfig.CodexCatalogModelDesired{Model: oracle.Model, DisplayName: new(oracle.Display), ContextWindow: new(oracle.ContextWindow), InputModalities: new([]agentconfig.CodexModality{"text"}), BaseInstructions: new(oracle.Instruction), ReasoningLevels: new([]agentconfig.CodexReasoningEffort{"low", "high"}), DefaultReasoningLevel: new(agentconfig.CodexReasoningEffort(oracle.DefaultEffort))}
				if index == 0 {
					provider.Catalog.Models[0].SupportsParallelToolCalls = new(true)
				} else {
					provider.ReasoningEffort = new(agentconfig.CodexReasoningEffort(*oracle.RootEffort))
				}
				updated, err := fixture.domain.Update(test.Context(), fixture.request.OwnerID, fixture.request.AssetID, view.Metadata.Revision, configuration.Draft{Name: "C6 qualification", CodexProvider: provider}, nil)
				if err != nil {
					test.Fatal("synthetic asset CAS")
				}
				fixture.request.AssetRevision = updated.Metadata.Revision
				output, operation, code := codexRunCLI(test, fixture, append(fixture.arguments(), "--apply"), "exact")
				if code != 0 || !strings.Contains(output, "Completed") || len(operation) != 32 {
					test.Fatal("ordinary production CLI apply")
				}
				current := codexQualificationRoles(test, fixture)
				test.Logf("ordinary CLI apply: exit=%d operation=%s config-sha256=%x catalog-sha256=%x", code, operation, sha256.Sum256(current[0]), sha256.Sum256(current[2]))
				if !bytes.Equal(original[1], current[1]) || (original[1] == nil) != (current[1] == nil) {
					test.Fatal("auth Keep violated")
				}
				codexQualificationConsumer(test, fixture, login+"-"+oracle.Model, oracle)
				codexQualificationSameRoles(test, current, codexQualificationRoles(test, fixture))
				if index == 0 {
					first, firstRoles = operation, current
					continue
				}
				if bytes.Equal(firstRoles[0], current[0]) || bytes.Equal(firstRoles[2], current[2]) {
					test.Fatal("second selection did not produce genuine native config/catalog differences")
				}
				_, _, code = codexRunCLI(test, fixture, []string{"config", "restore", "--target", "codex.config", "--source-operation", first}, "")
				if code == 0 {
					test.Fatal("stale selection restore allowed")
				}
				codexQualificationSameRoles(test, current, codexQualificationRoles(test, fixture))
				output, restoredOperation, code := codexRunCLI(test, fixture, []string{"config", "restore", "--target", "codex.config", "--source-operation", operation, "--apply"}, "exact")
				if code != 0 || len(restoredOperation) != 32 || restoredOperation == operation || !strings.Contains(output, "Restored") {
					test.Fatal("ordinary production CLI Present restore")
				}
				test.Logf("ordinary CLI restore: exit=%d source-operation=%s plan-operation=%s", code, operation, restoredOperation)
				codexQualificationSameRoles(test, firstRoles, codexQualificationRoles(test, fixture))
				codexQualificationConsumer(test, fixture, login+"-restored-a", firstOracle)
				codexQualificationSameRoles(test, firstRoles, codexQualificationRoles(test, fixture))
			}
			if fixture.authorizes.Load() != 2 || fixture.consumes.Load() != 2 || fixture.consumed(test) != 2 {
				test.Fatal("two explicit authorizations/consumptions or local-only restore violated")
			}
			test.Log("two explicit secret approvals/authorizations/consumptions; stale restore refused; Present restore exact; auth Keep")
		})
	}
}

func TestCodexCLIProductionExpiredObservation(test *testing.T) {
	fixture := newCodexCLIFixture(test, "")
	authPath := filepath.Join(fixture.home, ".codex", "auth.json")
	body, err := os.ReadFile(authPath)
	if err != nil {
		test.Fatal("synthetic OAuth read")
	}
	defer clear(body)
	var login map[string]any
	if json.Unmarshal(body, &login) != nil {
		test.Fatal("synthetic OAuth decode")
	}
	deadline := time.Now().Add(12 * time.Second)
	login["last_refresh"] = deadline.Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	body, err = json.Marshal(login)
	if err != nil || os.WriteFile(authPath, body, 0o600) != nil {
		test.Fatal("synthetic bounded observation")
	}
	defer clear(body)
	before := codexQualificationRoles(test, fixture)
	var reached bool
	fixture.promptHook = func() {
		if !time.Now().Before(deadline) {
			test.Fatal("observation expired before real review control")
		}
		reached = true
		time.Sleep(time.Until(deadline) + 100*time.Millisecond)
	}
	output, _, code := codexRunCLI(test, fixture, append(fixture.arguments(), "--apply"), "expired")
	test.Logf("expired review: exit=%d reached=%t review=%t authorizes=%d consumes=%d durable-consumes=%d", code, reached, strings.Contains(output, "CODEX REVIEW END"), fixture.authorizes.Load(), fixture.consumes.Load(), fixture.consumed(test))
	if code == 0 || !reached || !strings.Contains(output, "CODEX REVIEW END") || strings.Contains(output, "：Completed；") || fixture.authorizes.Load() != 0 || fixture.consumes.Load() != 0 || fixture.consumed(test) != 0 {
		test.Fatal("expired actual review authorized, consumed or silently skipped")
	}
	codexQualificationSameRoles(test, before, codexQualificationRoles(test, fixture))
}
