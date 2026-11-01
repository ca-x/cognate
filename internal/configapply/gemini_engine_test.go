package configapply

import (
	"bytes"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/geminiconfig"
)

func TestIndependentGeminiExpiryDuringLastFreshScanCannotPublish(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	_, prepared, declaration := geminiTestPlan(test, fixture, agentconfig.GeminiAPIKeyAuth)
	current, err := fixture.engine.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil {
		test.Fatal("selection setup", err)
	}
	defer current.Destroy()
	originalExpiry := time.Now().Add(12 * time.Second)
	selection, err := NewGeminiSelection(prepared, declaration, originalExpiry, current)
	if err != nil {
		test.Fatal("short session setup", err)
	}
	defer selection.Destroy()
	observed, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, originalExpiry)
	if err != nil {
		test.Fatal("short observation setup", err)
	}
	defer observed.Destroy()
	plan, err := fixture.engine.PreviewGeminiObserved(test.Context(), selection, observed)
	if err != nil {
		test.Fatal("preview setup", err)
	}
	defer plan.Destroy()
	confirmed := geminiApproveTestPlan(test, fixture, plan)
	binding := geminiAuthorizeTestPlan(test, fixture, confirmed, prepared)
	if err := fixture.engine.AdmitGeminiConsume(test.Context(), confirmed); err != nil {
		test.Fatal("consume admission setup", err)
	}
	if err := fixture.engine.RecordGeminiConsumption(test.Context(), confirmed, binding); err != nil {
		test.Fatal("consumption setup", err)
	}
	secret, err := geminiconfig.NewSelectedSecret(agentconfig.GeminiAPIKeySecret, []byte("SYNTHETIC_INDEPENDENT_EXPIRY_KEY"))
	if err != nil {
		test.Fatal("synthetic secret setup", err)
	}
	defer secret.Destroy()
	if err := fixture.engine.MaterializeGemini(test.Context(), confirmed, []geminiconfig.SelectedSecret{secret}); err != nil {
		test.Fatal("materialization setup", err)
	}
	scanCount := 0
	waited := false
	var publishedAt time.Time
	fixture.engine.private().fault = func(label string) error {
		if label == "observation-between" {
			scanCount++
			if scanCount == 2 {
				if !time.Now().Before(originalExpiry) {
					test.Fatal("probe missed live start boundary")
				}
				waited = true
				timer := time.NewTimer(time.Until(originalExpiry.Add(30 * time.Millisecond)))
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-test.Context().Done():
					return test.Context().Err()
				}
			}
		}
		if label == "entry-published-0" {
			publishedAt = time.Now()
		}
		return nil
	}
	defer func() { fixture.engine.private().fault = nil }()
	result, applyErr := fixture.engine.ApplyGemini(test.Context(), confirmed)
	body, readErr := os.ReadFile(filepath.Join(fixture.home, ".gemini", ".env"))
	defer clear(body)
	test.Logf("waited=%t scans=%d context_live=%t expired_error=%t wrote_after_expiry=%t secret_present=%t state=%d consumed=%t applied=%t", waited, scanCount, test.Context().Err() == nil, errors.Is(applyErr, ErrGeminiExpired), !publishedAt.IsZero() && publishedAt.After(originalExpiry), bytes.Contains(body, []byte("SYNTHETIC_INDEPENDENT_EXPIRY_KEY")), result.State, result.Consumed, result.Applied)
	if !waited || test.Context().Err() != nil {
		test.Fatal("probe setup did not cross only the original review expiry")
	}
	if !publishedAt.IsZero() || !errors.Is(readErr, os.ErrNotExist) {
		test.Fatal("expired observation authorized a new first-role target write")
	}
	if !errors.Is(applyErr, ErrGeminiExpired) {
		test.Fatalf("expiry was not preserved: %v", applyErr)
	}
}

type geminiFixture struct {
	engine          *GeminiEngine
	home, stateRoot string
	launch          GeminiLaunch
	key             []byte
}

func TestGeminiFixedProfileSourceIdentity(test *testing.T) {
	identity := geminiNativeIdentity()
	if identity.ID != geminiconfig.Native0610ProfileID || hex.EncodeToString(identity.NodeSHA256[:]) != geminiconfig.Native0610NodeSHA256 || hex.EncodeToString(identity.ManifestSHA256[:]) != geminiconfig.Native0610PackageManifestSHA256 || identity.ProfileSHA256 == [32]byte{} || identity.CodecSHA256 == [32]byte{} {
		test.Fatal("fixed source identity incomplete")
	}
	if len(geminiQualifiedProfiles) != 1 || geminiQualifiedProfiles[0] != identity {
		test.Fatal("proposed literal tuple does not exactly match fixed source contract")
	}
	test.Logf("profile_sha256=%x codec_sha256=%x", identity.ProfileSHA256, identity.CodecSHA256)
}
func newGeminiFixture(test *testing.T, env, settings []byte) *geminiFixture {
	test.Helper()
	node, packageRoot := os.Getenv("COGNATE_GEMINI_NODE"), os.Getenv("COGNATE_GEMINI_PACKAGE")
	if node == "" || packageRoot == "" {
		test.Skip("explicit complete pinned Gemini/Node input required; no qualification from compilation")
	}
	root := test.TempDir()
	home, stateRoot, workspace := filepath.Join(root, "home"), filepath.Join(root, "state"), filepath.Join(root, "work")
	for _, path := range []string{root, home, stateRoot, workspace, filepath.Join(home, ".gemini")} {
		if err := os.Mkdir(path, 0o700); err != nil && path != root {
			test.Fatal("private fixture directory")
		}
		if os.Chmod(path, 0o700) != nil {
			test.Fatal("private fixture mode")
		}
	}
	test.Setenv("HOME", home)
	if env != nil {
		if os.WriteFile(filepath.Join(home, ".gemini", ".env"), env, 0o600) != nil {
			test.Fatal("synthetic env fixture")
		}
	}
	if settings != nil {
		if os.WriteFile(filepath.Join(home, ".gemini", "settings.json"), settings, 0o600) != nil {
			test.Fatal("synthetic settings fixture")
		}
	}
	trust, _ := json.Marshal(map[string]string{workspace: "TRUST_FOLDER"})
	if os.WriteFile(filepath.Join(home, ".gemini", "trustedFolders.json"), trust, 0o600) != nil {
		test.Fatal("synthetic existing trust")
	}
	previous := geminiQualifiedProfiles
	geminiQualifiedProfiles = []GeminiProfileIdentity{geminiNativeIdentity()}
	test.Cleanup(func() { geminiQualifiedProfiles = previous })
	key := bytes.Repeat([]byte{0x69}, 32)
	engine, err := OpenGemini(test.Context(), home, stateRoot, key)
	if err != nil {
		test.Fatal("private Gemini open refused")
	}
	test.Cleanup(func() {
		if engine.Close() != nil {
			test.Error("private engine close")
		}
	})
	return &geminiFixture{engine: engine, home: home, stateRoot: stateRoot, key: key, launch: GeminiLaunch{NodeExecutable: node, PackageRoot: packageRoot, Directory: workspace}}
}

func TestGeminiQualificationGateBeforePathAccess(test *testing.T) {
	previous := geminiQualifiedProfiles
	geminiQualifiedProfiles = nil
	defer func() { geminiQualifiedProfiles = previous }()
	home := filepath.Join(test.TempDir(), "not-created")
	if engine, err := OpenGemini(test.Context(), home, filepath.Join(home, "state"), bytes.Repeat([]byte{9}, 32)); err != ErrGeminiUnsupported || engine != nil {
		test.Fatal("unqualified profile did not fail first")
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		test.Fatal("unqualified profile touched HOME")
	}
}

func geminiTestPlan(test *testing.T, fixture *geminiFixture, mode agentconfig.GeminiAuthMode) (GeminiPlan, *nativev1.PreparationBinding, agentconfig.GeminiProviderDeclarationV1) {
	test.Helper()
	current, err := fixture.engine.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil {
		test.Fatal("current Gemini selection")
	}
	declaration := agentconfig.GeminiProviderDeclarationV1{Version: 1, Target: agentconfig.GeminiSettings, AuthMode: mode, SelectedType: "oauth-personal", RequiredSlots: []agentconfig.GeminiSecretSlot{}, LocalAuthPolicy: agentconfig.GeminiPreserveLocalLoginV1, EnvModel: new("env-model"), SettingsModel: new("settings-model")}
	var slots []nativev1.SecretSlot
	if mode == agentconfig.GeminiAPIKeyAuth {
		declaration.SelectedType = "gemini-api-key"
		declaration.CredentialReference = new(agentconfig.GeminiAPIKeySecret)
		declaration.KeyTransport = new(agentconfig.GeminiBearer)
		declaration.Endpoint = new("https://127.0.0.1:18444")
		declaration.RequiredSlots = []agentconfig.GeminiSecretSlot{agentconfig.GeminiAPIKeySecret}
		slots = []nativev1.SecretSlot{4}
	}
	digest := geminiDeclarationHash(declaration)
	now := time.Now()
	operation := codexRandomID()
	rawOperation, _ := hex.DecodeString(operation)
	prepared := &nativev1.PreparationBinding{OwnerId: "fixture-owner", ServerOrigin: "https://synthetic.invalid", NativeSessionBinding: bytes.Repeat([]byte{5}, 32), OperationId: rawOperation, AssetId: strings.Repeat("A", 26), AssetRevision: 1, AgentId: "gemini", AssetKind: 1, SchemaVersion: 1, Adapter: &nativev1.AdapterBinding{ApiVersion: 1, PluginId: "builtin.gemini", PluginVersion: "1.1.0", DefinitionDigest: "b0ba50fab171fea7889625da4ed9d36dc9b811303a166b66fb23bb1b232bd095", RequiredCapabilities: []string{"gemini.provider-plan.v1"}}, Target: 3, Scope: 1, ProfileId: "fixture-profile", SelectionRevision: current.Revision() + 1, RequiredSlots: slots, PrepareExpiresAtUnixMs: now.Add(2 * time.Minute).UnixMilli(), ConsumerKeySha256: bytes.Repeat([]byte{6}, 32), PrepareId: "synthetic-prepare", PreparedAtUnixMs: now.UnixMilli(), Gemini: &nativev1.GeminiPreparationV1{ContractVersion: 1, DeclarationSha256: digest[:]}}
	selection, err := NewGeminiSelection(prepared, declaration, now.Add(2*time.Minute), current)
	if err != nil {
		test.Fatal("Gemini selection setup")
	}
	test.Cleanup(selection.Destroy)
	observation, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, now.Add(2*time.Minute))
	if err != nil {
		test.Fatal("physical Gemini observation")
	}
	test.Cleanup(observation.Destroy)
	plan, err := fixture.engine.PreviewGeminiObserved(test.Context(), selection, observation)
	if err != nil {
		test.Fatal("Gemini pair preview")
	}
	test.Cleanup(plan.Destroy)
	return plan, prepared, declaration
}
func geminiApproveTestPlan(test *testing.T, fixture *geminiFixture, plan GeminiPlan) GeminiPlan {
	test.Helper()
	var approvals []GeminiFieldApproval
	for _, change := range plan.View().Changes {
		approvals = append(approvals, GeminiFieldApproval{Field: change.Field, Takeover: change.Ownership == geminiconfig.Foreign, Clear: change.Cleanup})
	}
	next, err := fixture.engine.ConfirmGemini(test.Context(), plan, approvals, nil, false)
	if err != nil {
		test.Fatal("explicit Gemini field review")
	}
	test.Cleanup(next.Destroy)
	if next.View().PlanID == plan.View().PlanID || !next.View().FinalReview {
		test.Fatal("new exact final-review plan not created")
	}
	confirmed, err := fixture.engine.ConfirmGemini(test.Context(), next, approvals, next.View().RequiredSlots, true)
	if err != nil {
		test.Fatal("independent slots and final confirmation")
	}
	return confirmed
}
func geminiAuthorizeTestPlan(test *testing.T, fixture *geminiFixture, plan GeminiPlan, prepared *nativev1.PreparationBinding) *nativev1.ApplyBinding {
	test.Helper()
	constraints, err := fixture.engine.AdmitGeminiAuthorization(test.Context(), plan)
	if err != nil {
		test.Fatal("durable AuthorizeAttempted")
	}
	defer constraints.Destroy()
	commitment := constraints.Commitment()
	binding := &nativev1.ApplyBinding{Preparation: prepared, IntentCommitment: commitment[:], IntentExpiresAtUnixMs: constraints.ExpiresAt().UnixMilli(), GeminiLocalBinding: constraints.CopyProjection()}
	if err := fixture.engine.RecordGeminiAuthorization(test.Context(), plan, binding); err != nil {
		test.Fatal("bound authorization receipt")
	}
	return binding
}
func TestGeminiPairApplyExplicitOwnershipAndSelection(test *testing.T) {
	for _, initial := range []string{"missing", "present"} {
		test.Run(initial, func(test *testing.T) {
			var env, settings []byte
			if initial == "present" {
				env = []byte("# retained\r\nGEMINI_API_KEY='old'\r\nGOOGLE_GEMINI_BASE_URL='https://old.invalid'\r\nGEMINI_API_KEY_AUTH_MECHANISM='bearer'\r\nGEMINI_MODEL='old-env'\r\nFOREIGN=keep\r\n")
				settings = []byte(`{"model":{"name":"old-settings","other":9007199254740993999},"security":{"auth":{"selectedType":"gemini-api-key","note":"keep"}},"ui":{"theme":"dark"}}`)
			}
			fixture := newGeminiFixture(test, env, settings)
			plan, prepared, _ := geminiTestPlan(test, fixture, agentconfig.GeminiAPIKeyAuth)
			view := plan.View()
			if len(view.Changes) != 6 || view.FinalReview || view.SelectionRevision != 1 {
				test.Fatal("incomplete pre-approval safe view")
			}
			if initial == "present" {
				var absent []GeminiFieldApproval
				for _, change := range view.Changes {
					absent = append(absent, GeminiFieldApproval{Field: change.Field})
				}
				if _, err := fixture.engine.ConfirmGemini(test.Context(), plan, absent, nil, false); !errors.Is(err, ErrGeminiDenied) {
					test.Fatal("foreign fields admitted without explicit takeover")
				}
			}
			confirmed := geminiApproveTestPlan(test, fixture, plan)
			binding := geminiAuthorizeTestPlan(test, fixture, confirmed, prepared)
			if err := fixture.engine.AdmitGeminiConsume(test.Context(), confirmed); err != nil {
				test.Fatal("durable ConsumeAttempted")
			}
			if err := fixture.engine.RecordGeminiConsumption(test.Context(), confirmed, binding); err != nil {
				test.Fatal("bound consume receipt")
			}
			secret, err := geminiconfig.NewSelectedSecret(agentconfig.GeminiAPIKeySecret, []byte("synthetic#=$'\"key"))
			if err != nil {
				test.Fatal("synthetic material setup")
			}
			defer secret.Destroy()
			if err := fixture.engine.MaterializeGemini(test.Context(), confirmed, []geminiconfig.SelectedSecret{secret}); err != nil {
				test.Fatalf("pair materialization: %v", err)
			}
			before, err := fixture.engine.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
			if err != nil || before.Revision() != 0 {
				test.Fatal("selection advanced before pair applied")
			}
			result, err := fixture.engine.ApplyGemini(test.Context(), confirmed)
			if err != nil || result.State != GeminiCompleted || !result.Applied || !result.Consumed {
				test.Fatalf("pair apply: state=%d safe_error=%v", result.State, err)
			}
			current, err := fixture.engine.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
			if err != nil || current.Revision() != 1 || current.ConfirmationRevision() != 1 {
				test.Fatal("selection-last CAS missing")
			}
			body, err := os.ReadFile(filepath.Join(fixture.home, ".gemini", "settings.json"))
			if err != nil || !bytes.Contains(body, []byte(`"name":"settings-model"`)) {
				test.Fatal("actual settings selector/model not applied")
			}
			if initial == "present" && !bytes.Contains(body, []byte(`9007199254740993999`)) {
				test.Fatal("unrelated numeric lexeme changed")
			}
			if _, err := fixture.engine.AdmitGeminiAuthorization(test.Context(), confirmed); !errors.Is(err, ErrGeminiConflict) {
				test.Fatal("same operation authorizes again")
			}
		})
	}
}

func TestGeminiDurableConsumeAdmissionBeforeNetwork(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	plan, prepared, _ := geminiTestPlan(test, fixture, agentconfig.GeminiAPIKeyAuth)
	confirmed := geminiApproveTestPlan(test, fixture, plan)
	geminiAuthorizeTestPlan(test, fixture, confirmed, prepared)
	if fixture.engine.AdmitGeminiConsume(test.Context(), confirmed) != nil {
		test.Fatal("consume admission")
	}
	reopened, err := OpenGemini(test.Context(), fixture.home, fixture.stateRoot, fixture.key)
	if err != nil {
		test.Fatal("independent authenticated journal reader")
	}
	defer reopened.Close()
	status, err := reopened.StatusGemini(test.Context(), confirmed.View().OperationID, "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil || status.State != GeminiConsumeAttempted || !status.RemoteOutcomeUnknown || status.Consumed || status.Applied {
		test.Fatal("ConsumeAttempted was not durable before allowing RPC")
	}
	if fixture.engine.AdmitGeminiConsume(test.Context(), confirmed) == nil {
		test.Fatal("consume admission replayed")
	}
}

func TestGeminiPerPublishFreshnessAndSecondCAS(test *testing.T) {
	for _, mutation := range []string{"source", "second-role"} {
		test.Run(mutation, func(test *testing.T) {
			fixture := newGeminiFixture(test, nil, []byte(`{"ui":{"theme":"initial"}}`))
			plan := geminiDurableTestPlan(test, fixture)
			external := []byte(`{"ui":{"theme":"external"}}`)
			triggered := false
			fixture.engine.private().fault = func(label string) error {
				if label == "before-publish-1" && !triggered {
					triggered = true
					if mutation == "source" {
						if os.WriteFile(filepath.Join(fixture.launch.Directory, ".env"), []byte("FOREIGN=late\n"), 0o600) != nil {
							test.Fatal("late effective source fixture")
						}
					} else {
						if os.WriteFile(filepath.Join(fixture.home, ".gemini", "settings.json"), external, 0o600) != nil {
							test.Fatal("late second role fixture")
						}
					}
				}
				return nil
			}
			result, err := fixture.engine.ApplyGemini(test.Context(), plan)
			if !triggered || err == nil || result.Applied {
				test.Fatal("late source/second-role change did not block publication")
			}
			current, err := fixture.engine.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
			if err != nil || current.Revision() != 0 {
				test.Fatal("selection advanced with a rejected/mixed pair")
			}
			body, err := os.ReadFile(filepath.Join(fixture.home, ".gemini", "settings.json"))
			defer clear(body)
			wanted := []byte(`{"ui":{"theme":"initial"}}`)
			if mutation == "second-role" {
				wanted = external
			}
			if err != nil || !bytes.Equal(body, wanted) {
				test.Fatal("late rejection overwrote second role")
			}
		})
	}
}

func TestGeminiSelectionRemainsBeforeUntilTargetsDurable(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	plan := geminiDurableTestPlan(test, fixture)
	triggered := false
	fixture.engine.private().fault = func(label string) error {
		if label == "before-publish-0" {
			triggered = true
			return ErrGeminiUnknown
		}
		return nil
	}
	_, applyError := fixture.engine.ApplyGemini(test.Context(), plan)
	fixture.engine.private().fault = nil
	current, err := fixture.engine.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil || current.Revision() != 0 {
		test.Fatal("selection-last was not durable across failed publication")
	}
	if !errors.Is(applyError, ErrGeminiUnknown) || !triggered {
		test.Fatal("prepublication boundary not reached")
	}
}

func TestGeminiTicketOpaqueAndDestroyedAfterDrain(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	plan, _, _ := geminiTestPlan(test, fixture, agentconfig.GeminiAPIKeyAuth)
	confirmed := geminiApproveTestPlan(test, fixture, plan)
	private := []byte("synthetic-private-status-bearer")
	if fixture.engine.BindGeminiStatusTicket(test.Context(), confirmed, private) != nil {
		test.Fatal("private status-ticket binding")
	}
	ticket, err := fixture.engine.GeminiStatusTicket(test.Context(), confirmed.View().OperationID, "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil {
		test.Fatal("owned status ticket")
	}
	encoded, err := json.Marshal(ticket)
	if err != nil || bytes.Contains(encoded, private) || strings.Contains(fmt.Sprintf("%+v %#v", ticket, ticket), string(private)) {
		test.Fatal("status ticket escaped through generic formatting")
	}
	copyBytes := ticket.CopyBytes()
	if !bytes.Equal(copyBytes, private) {
		test.Fatal("explicit status-only owned bytes missing")
	}
	clear(copyBytes)
	ticket.Destroy()
	if len(ticket.CopyBytes()) != 0 {
		test.Fatal("ticket destroy left retrievable bytes")
	}
	retained := fixture.engine.private().inventory.Operations[confirmed.View().OperationID].StatusTicket
	if fixture.engine.Close() != nil {
		test.Fatal("owned close drain")
	}
	if !bytes.Equal(retained, make([]byte, len(retained))) {
		test.Fatal("status bearer bytes survived successful engine drain/destroy")
	}
}

func TestGeminiForeignDisplacedStageCannotCommitSelection(test *testing.T) {
	fixture := newGeminiFixture(test, nil, []byte(`{"ui":{"theme":"before"}}`))
	plan := geminiDurableTestPlan(test, fixture)
	foreign := []byte(`{"ui":{"theme":"unrelated-external-write"}}`)
	stagePath := filepath.Join(fixture.home, ".gemini", "gemini-"+plan.View().OperationID+"-1.stage")
	triggered := false
	fixture.engine.private().fault = func(label string) error {
		if label == "before-selection" {
			triggered = true
			if os.WriteFile(stagePath, foreign, 0o600) != nil {
				test.Fatal("late foreign displaced-stage fixture")
			}
		}
		return nil
	}
	result, err := fixture.engine.ApplyGemini(test.Context(), plan)
	if !triggered || err == nil || result.Applied {
		test.Fatal("foreign displaced stage committed successful selection")
	}
	fixture.engine.private().fault = nil
	current, err := fixture.engine.CurrentGeminiSelection(test.Context(), "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil || current.Revision() != 0 {
		test.Fatal("foreign displaced stage advanced durable selection")
	}
	status, err := fixture.engine.StatusGemini(test.Context(), plan.View().OperationID, "https://synthetic.invalid", "fixture-owner", "fixture-profile")
	if err != nil || status.State != GeminiNeedsAttention || status.Applied {
		test.Fatal("read-only status concealed foreign displaced stage")
	}
	body, err := os.ReadFile(stagePath)
	defer clear(body)
	if err != nil || !bytes.Equal(body, foreign) {
		test.Fatal("unknown displaced bytes removed or adopted")
	}
}
