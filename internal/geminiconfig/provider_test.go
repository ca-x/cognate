package geminiconfig

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/agentadapter"
)

func TestGeminiProviderPairAndBinding(test *testing.T) {
	base := "# fixed\r\nFOREIGN=keep\r\n"
	settings := `{"ui":{"theme":"dark"},"model":{"other":9007199254740993999},"security":{"auth":{"note":"keep"}},"mcpServers":{"foreign":{"unknown":true}}}`
	images := syntheticImages(test, base, settings)
	resolution := syntheticResolution(test, images)
	want := declaration(agentconfig.GeminiAPIKeyAuth)
	want.Endpoint = new("https://Example.invalid/Case")
	want.KeyTransport = new(agentconfig.GeminiBearer)
	want.EnvModel = new("Env/One:1")
	want.SettingsModel = new("Settings/Two:2")
	plan, err := checkProvider(test.Context(), images, want, resolution, syntheticProfile())
	if err != nil {
		test.Fatal("complete synthetic pair check failed")
	}
	defer plan.Destroy()
	images.Destroy()
	resolution.Destroy()
	*want.EnvModel = "caller-mutated"
	secret, _ := NewSelectedSecret(agentconfig.GeminiAPIKeySecret, []byte(privacyMarker))
	defer secret.Destroy()
	candidate, err := RenderProvider(test.Context(), plan, []SelectedSecret{secret})
	if err != nil {
		test.Fatal("complete synthetic pair render failed")
	}
	defer candidate.Destroy()
	env := candidate.CopyImage(EnvRole)
	defer env.Destroy()
	parsed, err := readEnv(test.Context(), env.get[imageData]().body)
	if err != nil {
		test.Fatal("rendered env invalid")
	}
	defer parsed.destroy()
	if !bytes.Equal(parsed.find("GEMINI_API_KEY").value, []byte(privacyMarker)) || !bytes.Equal(parsed.find("GEMINI_MODEL").value, []byte("Env/One:1")) || !bytes.HasPrefix(env.CopyBytes(), []byte(base)) {
		test.Fatal("pair materialization changed literal or foreign bytes")
	}
	jsonImage := candidate.CopyImage(SettingsRole)
	defer jsonImage.Destroy()
	if !bytes.Contains(jsonImage.CopyBytes(), []byte(`"name":"Settings/Two:2"`)) || !bytes.Contains(jsonImage.CopyBytes(), []byte(`9007199254740993999`)) || !bytes.Contains(jsonImage.CopyBytes(), []byte(`"mcpServers":{"foreign":{"unknown":true}}`)) {
		test.Fatal("settings sibling or independent model lost")
	}
	for _, material := range [][]SelectedSecret{nil, {secret, secret}, {{}}} {
		result, err := RenderProvider(test.Context(), plan, material)
		if err == nil || result.get[candidateData]() != nil {
			test.Fatal("invalid material set accepted")
		}
	}
	binding := plan.Binding()
	defer binding.Destroy()
	if binding.CopyBaselines()[0].Role != EnvRole || binding.CopyBaselines()[1].Role != SettingsRole || binding.CopyObservation().Witness == [32]byte{} || binding.CopyDeclaration().EnvModel == nil || *binding.CopyDeclaration().EnvModel != "Env/One:1" {
		test.Fatal("complete immutable binding missing")
	}
	copyDeclaration := binding.CopyDeclaration()
	*copyDeclaration.EnvModel = "mutated"
	if *binding.CopyDeclaration().EnvModel != "Env/One:1" {
		test.Fatal("binding aliases accessor")
	}
	for _, object := range []any{plan, candidate, binding, env, jsonImage, struct{ hidden ProviderCandidate }{candidate}} {
		assertPrivate(test, object)
	}
	cloned := candidate.CopyImage(EnvRole)
	defer cloned.Destroy()
	candidate.Destroy()
	if !bytes.Contains(cloned.CopyBytes(), []byte(privacyMarker)) {
		test.Fatal("owned copy destroyed with source")
	}
	for _, entry := range []struct{ env, settings string }{{base + "# changed", settings}, {base, settings + " "}} {
		images := syntheticImages(test, entry.env, entry.settings)
		resolution := syntheticResolution(test, images)
		other, err := checkProvider(test.Context(), images, declaration(agentconfig.GeminiAPIKeyAuth), resolution, syntheticProfile())
		images.Destroy()
		resolution.Destroy()
		if err != nil {
			test.Fatal("binding mutation fixture invalid")
		}
		actual := other.Binding()
		other.Destroy()
		if actual.CopyBaselines() == binding.CopyBaselines() {
			test.Fatal("changed raw baseline did not bind")
		}
		actual.Destroy()
	}
	bad := syntheticImages(test, "GEMINI_API_KEY='old'\nGOOGLE_GEMINI_BASE_URL='https://old.invalid'\nGEMINI_API_KEY_AUTH_MECHANISM='bearer'\nFOREIGN=keep", `{"security":{"auth":{"selectedType":"gemini-api-key"}}}`)
	defer bad.Destroy()
	retained := syntheticResolution(test, bad)
	defer retained.Destroy()
	if result, err := checkProvider(test.Context(), bad, declaration(agentconfig.GeminiNativeLogin), retained, syntheticProfile()); err == nil || result.get[planData]() != nil {
		test.Fatal("native-login retained API routing state")
	}
	cleared := syntheticResolution(test, bad, APIKeyField, EndpointField, TransportField)
	defer cleared.Destroy()
	login, err := checkProvider(test.Context(), bad, declaration(agentconfig.GeminiNativeLogin), cleared, syntheticProfile())
	if err != nil {
		test.Fatal("explicit managed mode transition rejected")
	}
	defer login.Destroy()
	loginCandidate, err := RenderProvider(test.Context(), login, nil)
	if err != nil {
		test.Fatal("zero-slot native-login render failed")
	}
	defer loginCandidate.Destroy()
	loginEnv := loginCandidate.CopyImage(EnvRole)
	defer loginEnv.Destroy()
	if !bytes.Equal(loginEnv.CopyBytes(), []byte("FOREIGN=keep")) {
		test.Fatal("clear transition touched unrelated env")
	}
	if result, err := RenderProvider(test.Context(), login, []SelectedSecret{secret}); err == nil || result.get[candidateData]() != nil {
		test.Fatal("native-login accepted material")
	}
	api := declaration(agentconfig.GeminiAPIKeyAuth)
	api.KeyTransport = new(agentconfig.GeminiBearer)
	api.Endpoint = new("https://New.invalid")
	control, err := checkProvider(test.Context(), bad, api, retained, syntheticProfile())
	if err != nil {
		test.Fatal("foreign rejection control is not a valid plan")
	}
	control.Destroy()
	for _, field := range fields {
		facts := *retained.get[resolutionData]()
		index := int(0)
		for position, known := range fields {
			if known == field {
				index = position
			}
		}
		facts.fields[index].Ownership = Foreign
		foreign := LocalResolution{opaque: own(&facts)}
		result, err := checkProvider(test.Context(), bad, api, foreign, syntheticProfile())
		foreign.Destroy()
		if err == nil || result.get[planData]() != nil {
			test.Fatal("foreign or mismatched ownership accepted")
		}
	}
}

func declaration(mode agentconfig.GeminiAuthMode) agentconfig.GeminiProviderDeclarationV1 {
	value := agentconfig.GeminiProviderDeclarationV1{Version: 1, Target: agentconfig.GeminiSettings, AuthMode: mode, SelectedType: "oauth-personal", RequiredSlots: []agentconfig.GeminiSecretSlot{}, LocalAuthPolicy: agentconfig.GeminiPreserveLocalLoginV1}
	if mode == agentconfig.GeminiAPIKeyAuth {
		value.SelectedType = "gemini-api-key"
		value.KeyTransport = new(agentconfig.GeminiNativeDefault)
		value.CredentialReference = new(agentconfig.GeminiAPIKeySecret)
		value.RequiredSlots = []agentconfig.GeminiSecretSlot{agentconfig.GeminiAPIKeySecret}
	}
	return value
}

func TestGeminiExplicitSixFieldTakeover(test *testing.T) {
	images := syntheticImages(test, "# retained\r\nGEMINI_API_KEY='old'\r\nGOOGLE_GEMINI_BASE_URL='https://old.invalid'\r\nGEMINI_API_KEY_AUTH_MECHANISM='bearer'\r\nGEMINI_MODEL='old-env'\r\nFOREIGN=keep\r\n", `{"model":{"name":"old-settings","other":9007199254740993999},"security":{"auth":{"selectedType":"gemini-api-key","note":"keep"}},"ui":{"theme":"dark"}}`)
	defer images.Destroy()
	facts := make([]FieldFact, 0, len(fields))
	for _, field := range fields {
		facts = append(facts, FieldFact{Field: field, Ownership: Foreign, Cleanup: Preserve})
	}
	var witness [32]byte
	witness[0] = 1
	foreign, err := NewResolution(syntheticProfile().id, CompleteEffective, facts, witness)
	if err != nil {
		test.Fatal("foreign resolution setup")
	}
	defer foreign.Destroy()
	want := declaration(agentconfig.GeminiNativeLogin)
	if plan, err := checkProvider(test.Context(), images, want, foreign, syntheticProfile()); !errors.Is(err, ErrOwnershipConflict) {
		plan.Destroy()
		test.Fatal("foreign fields accepted without explicit takeover")
	}
	for index := range facts {
		facts[index].Ownership = ExplicitTakeover
		if index < 3 {
			facts[index].Cleanup = Clear
		}
	}
	taken, err := NewResolution(syntheticProfile().id, CompleteEffective, facts, witness)
	if err != nil {
		test.Fatal("exact explicitly approved six-field takeover rejected")
	}
	defer taken.Destroy()
	plan, err := checkProvider(test.Context(), images, want, taken, syntheticProfile())
	if err != nil {
		test.Fatal("exact takeover plan rejected")
	}
	defer plan.Destroy()
	binding := plan.Binding()
	defer binding.Destroy()
	for _, fact := range binding.CopyObservation().Fields {
		if fact.Ownership != ExplicitTakeover {
			test.Fatal("takeover misrepresented as managed")
		}
	}
	candidate, err := RenderProvider(test.Context(), plan, nil)
	if err != nil {
		test.Fatal("explicit native-login cleanup render rejected")
	}
	defer candidate.Destroy()
	env := candidate.CopyImage(EnvRole)
	defer env.Destroy()
	if !bytes.Equal(env.CopyBytes(), []byte("# retained\r\nGEMINI_MODEL='old-env'\r\nFOREIGN=keep\r\n")) {
		test.Fatal("cleanup changed unrelated bytes or retained old API routing")
	}
	settings := candidate.CopyImage(SettingsRole)
	defer settings.Destroy()
	if !bytes.Equal(settings.CopyBytes(), []byte(`{"model":{"name":"old-settings","other":9007199254740993999},"security":{"auth":{"selectedType":"oauth-personal","note":"keep"}},"ui":{"theme":"dark"}}`)) {
		test.Fatal("takeover lost independent model or unknown sibling")
	}
	for index := range facts {
		mutated := slices.Clone(facts)
		mutated[index].Ownership = Foreign
		resolution, err := NewResolution(syntheticProfile().id, CompleteEffective, mutated, witness)
		if err != nil {
			continue
		}
		result, err := checkProvider(test.Context(), images, want, resolution, syntheticProfile())
		resolution.Destroy()
		result.Destroy()
		if !errors.Is(err, ErrOwnershipConflict) {
			test.Fatal("partial takeover allowed a remaining foreign field")
		}
	}
}

func TestGeminiProviderDeclarationParity(test *testing.T) {
	registry, err := agentadapter.Builtins()
	if err != nil {
		test.Fatal("builtin registration failed")
	}
	var descriptor agentconfig.Descriptor
	for _, item := range registry.List() {
		if item.Manifest.AgentID == "gemini" {
			descriptor = item
		}
	}
	if descriptor.DefinitionDigest == "" {
		test.Fatal("Gemini definition missing")
	}
	for _, mode := range []agentconfig.GeminiAuthMode{agentconfig.GeminiNativeLogin, agentconfig.GeminiAPIKeyAuth} {
		for mask := range 16 {
			want := declaration(mode)
			if mask&1 != 0 {
				want.EnvModel = new("Env/Case:1")
			}
			if mask&2 != 0 {
				want.SettingsModel = new("Settings/Other:2")
			}
			if mode == agentconfig.GeminiAPIKeyAuth {
				if mask&4 != 0 {
					want.Endpoint = new("https://Example.invalid:443/Case/%76")
				}
				if mask&8 != 0 {
					want.KeyTransport = new(agentconfig.GeminiBearer)
				}
			}
			desired := agentconfig.GeminiProviderDesired{AuthMode: mode, Endpoint: want.Endpoint, KeyTransport: want.KeyTransport, EnvModel: want.EnvModel, SettingsModel: want.SettingsModel, KeyPresent: mode == agentconfig.GeminiAPIKeyAuth}
			actual, err := registry.PlanGeminiProvider(test.Context(), agentconfig.GeminiProviderPlanRequest{APIVersion: 1, PluginID: descriptor.Manifest.PluginID, PluginVersion: descriptor.Manifest.PluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: descriptor.Manifest.Target, Capabilities: []agentconfig.Capability{agentconfig.GeminiProviderPlanV1}, Desired: desired})
			if err != nil || !reflect.DeepEqual(actual, want) {
				test.Fatal("real planner declaration differs from literal vector")
			}
			if validateDeclaration(actual) != nil {
				test.Fatal("legal real planner declaration rejected")
			}
		}
	}
	for _, mutate := range []func(*agentconfig.GeminiProviderDeclarationV1){
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.Version = 0 },
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.Target = agentconfig.ClaudeSettings },
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.SelectedType = "oauth-personal" },
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.AuthMode = "unknown" },
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.KeyTransport = nil },
		func(value *agentconfig.GeminiProviderDeclarationV1) {
			value.KeyTransport = new(agentconfig.GeminiKeyTransport("unknown"))
		},
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.CredentialReference = nil },
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.RequiredSlots = nil },
		func(value *agentconfig.GeminiProviderDeclarationV1) {
			value.RequiredSlots = append(value.RequiredSlots, agentconfig.GeminiAPIKeySecret)
		},
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.LocalAuthPolicy = "unknown" },
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.Endpoint = new("http://127.0.0.1/") },
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.Endpoint = new("https://x.invalid/%0a") },
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.Endpoint = new("https://x.invalid:0/") },
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.EnvModel = new("") },
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.SettingsModel = new(" bad ") },
	} {
		value := declaration(agentconfig.GeminiAPIKeyAuth)
		mutate(&value)
		if !errors.Is(validateDeclaration(value), ErrInvalidDeclaration) {
			test.Fatal("invalid complete declaration accepted")
		}
	}
	for _, mutate := range []func(*agentconfig.GeminiProviderDeclarationV1){
		func(value *agentconfig.GeminiProviderDeclarationV1) { value.Endpoint = new("https://x.invalid") },
		func(value *agentconfig.GeminiProviderDeclarationV1) {
			value.KeyTransport = new(agentconfig.GeminiBearer)
		},
		func(value *agentconfig.GeminiProviderDeclarationV1) {
			value.CredentialReference = new(agentconfig.GeminiAPIKeySecret)
		},
		func(value *agentconfig.GeminiProviderDeclarationV1) {
			value.RequiredSlots = []agentconfig.GeminiSecretSlot{agentconfig.GeminiAPIKeySecret}
		},
	} {
		value := declaration(agentconfig.GeminiNativeLogin)
		mutate(&value)
		if validateDeclaration(value) == nil {
			test.Fatal("native-login accepted key branch")
		}
	}
}

func TestGeminiProviderPresenceAndZeroOnFailure(test *testing.T) {
	missing, _ := NewImage(Missing, nil)
	defer missing.Destroy()
	images, err := NewImages(missing, missing)
	if err != nil {
		test.Fatal("missing pair rejected")
	}
	defer images.Destroy()
	facts := syntheticResolution(test, images)
	defer facts.Destroy()
	secret, _ := NewSelectedSecret(agentconfig.GeminiAPIKeySecret, []byte(privacyMarker))
	defer secret.Destroy()
	for _, mode := range []agentconfig.GeminiAuthMode{agentconfig.GeminiNativeLogin, agentconfig.GeminiAPIKeyAuth} {
		for mask := range 8 {
			value := declaration(mode)
			if mask&1 != 0 {
				value.EnvModel = new("Independent/Env:1")
			}
			if mask&2 != 0 {
				value.SettingsModel = new("Different/Settings:2")
			}
			if mask&4 != 0 && mode == agentconfig.GeminiAPIKeyAuth {
				value.KeyTransport = new(agentconfig.GeminiBearer)
			}
			plan, err := checkProvider(test.Context(), images, value, facts, syntheticProfile())
			if err != nil {
				test.Fatal("presence matrix Check failed")
			}
			binding := plan.Binding()
			if !reflect.DeepEqual(binding.CopyDeclaration(), value) || binding.CopyBaselines()[0].Presence != Missing || binding.CopyBaselines()[1].Presence != Missing {
				test.Fatal("presence or declaration absent binding lost")
			}
			binding.Destroy()
			var material []SelectedSecret
			if mode == agentconfig.GeminiAPIKeyAuth {
				material = []SelectedSecret{secret}
			}
			candidate, err := RenderProvider(test.Context(), plan, material)
			plan.Destroy()
			if err != nil {
				test.Fatal("presence matrix Render failed")
			}
			envImage, settingsImage := candidate.CopyImage(EnvRole), candidate.CopyImage(SettingsRole)
			candidate.Destroy()
			if mode == agentconfig.GeminiNativeLogin && mask&1 == 0 && envImage.Presence() != Missing {
				test.Fatal("zero-env native-login created an empty artifact")
			}
			env, err := readEnv(test.Context(), envImage.CopyBytes())
			if err != nil {
				test.Fatal("presence env invalid")
			}
			settings, err := readJSON(test.Context(), settingsImage.CopyBytes())
			if err != nil {
				test.Fatal("presence settings invalid")
			}
			if (env.find("GEMINI_MODEL") != nil) != (value.EnvModel != nil) {
				test.Fatal("env model absence defaulted")
			}
			model := settings.root.member("model")
			if (model != nil && model.member("name") != nil) != (value.SettingsModel != nil) {
				test.Fatal("settings model absence defaulted")
			}
			env.destroy()
			settings.destroy()
			envImage.Destroy()
			settingsImage.Destroy()
		}
	}
	for _, raw := range []string{`{"broken":`, `{"model":null}`, `{"model":{"name":"a","name":"b"}}`} {
		bad := syntheticImages(test, "", raw)
		result, err := checkProvider(test.Context(), bad, declaration(agentconfig.GeminiAPIKeyAuth), facts, syntheticProfile())
		bad.Destroy()
		if err == nil || result.get[planData]() != nil {
			test.Fatal("failed second role returned a partial plan")
		}
	}
	plan, err := checkProvider(test.Context(), images, declaration(agentconfig.GeminiAPIKeyAuth), facts, syntheticProfile())
	if err != nil {
		test.Fatal("unrepresentable key control invalid")
	}
	defer plan.Destroy()
	value := []byte(privacyMarker + "'\"`#unrepresentable")
	unrepresentable, _ := NewSelectedSecret(agentconfig.GeminiAPIKeySecret, value)
	defer unrepresentable.Destroy()
	result, err := RenderProvider(test.Context(), plan, []SelectedSecret{unrepresentable})
	if !errors.Is(err, ErrUnsupportedRepresentation) || result.get[candidateData]() != nil || !bytes.Equal(unrepresentable.get[secretData]().body, value) {
		test.Fatal("unrepresentable key returned a partial pair or damaged material")
	}
}
