package codexconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func syntheticProfile() nativeProfile {
	return nativeProfile{
		id: "synthetic.full-v1", dialect: "toml-1.1", defaultStore: File,
		parallel: parallelCatalog, legacyInstructions: true, relativeCatalog: true, bearerPrecedence: true,
		rootDefaults: []byte(`{"models":[]}`), modelDefaults: []byte(`{"description":"SYNTHETIC_ONLY"}`),
		requiredRoot: []string{"models"}, requiredModel: []string{"slug", "description"},
		ignoredRoot: []string{"keep", "other", "note", "model_providers.cognate"}, ignoredProvider: []string{"description"},
		ignoredCatalogRoot: []string{"opaque"}, ignoredModel: []string{"opaque"},
		efforts:      []agentconfig.CodexReasoningEffort{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
		descriptions: map[agentconfig.CodexReasoningEffort]string{"none": "synthetic none", "minimal": "synthetic minimal", "low": "synthetic low", "medium": "synthetic medium", "high": "synthetic high", "xhigh": "synthetic xhigh", "max": "synthetic max"},
	}
}

func syntheticResolution(test *testing.T, provider, catalog Ownership, models []string, store AuthStore, keyring KeyringState, login LoginState) LocalResolution {
	test.Helper()
	owners, err := NewOwnership(provider, catalog, models)
	if err != nil {
		test.Fatal("fixture ownership")
	}
	auth, err := NewAuthFacts(store, keyring, login)
	if err != nil {
		test.Fatal("fixture auth")
	}
	resolution, err := NewResolution(syntheticProfile().id, CompleteEffective, owners, auth, [32]byte{1})
	if err != nil {
		test.Fatal("fixture resolution")
	}
	return resolution
}

func TestCodexLocalAuthPolicy(test *testing.T) {
	document, _ := indexTOML(test.Context(), nil)
	missing, _ := NewImage(Missing, nil)
	present, _ := NewImage(Present, []byte(`{"metadata":"not inferred"}`))
	unobserved, _ := NewImage(NotObserved, nil)
	for _, fixture := range []struct {
		store   AuthStore
		keyring KeyringState
		login   LoginState
		image   LocalImage
		want    bool
		valid   bool
	}{
		{File, KeyringNotObserved, CredentialLogin, present, true, true}, {File, KeyringNotObserved, NoCredentialLogin, present, false, true},
		{File, KeyringNotObserved, NoCredentialLogin, missing, false, true}, {File, KeyringNotObserved, CredentialLogin, missing, false, false},
		{File, KeyringNotObserved, NoCredentialLogin, unobserved, false, false}, {File, KeyringNotObserved, LoginUnknown, present, false, false},
		{Keyring, FoundCredentialLogin, LoginNotObserved, unobserved, true, true}, {Keyring, KeyringMissing, LoginNotObserved, unobserved, false, true},
		{Keyring, KeyringUnknown, NoCredentialLogin, present, false, false}, {Auto, FoundCredentialLogin, LoginUnknown, unobserved, true, true},
		{Auto, KeyringMissing, CredentialLogin, present, true, true}, {Auto, KeyringMissing, NoCredentialLogin, missing, false, true},
		{Auto, KeyringUnknown, NoCredentialLogin, present, false, false}, {Ephemeral, KeyringMissing, NoCredentialLogin, missing, false, false},
		{AuthStoreUnknown, KeyringMissing, NoCredentialLogin, present, false, false},
	} {
		profile := syntheticProfile()
		profile.defaultStore = fixture.store
		resolution := syntheticResolution(test, Absent, Absent, nil, fixture.store, fixture.keyring, fixture.login)
		actual, err := resolveLocalAuth(test.Context(), document, fixture.image, *resolution.get[resolutionData](), profile)
		if (err == nil) != fixture.valid || err == nil && actual != fixture.want {
			test.Fatal("auth policy table mismatch")
		}
	}
	resolution := syntheticResolution(test, Absent, Absent, nil, File, KeyringNotObserved, NoCredentialLogin)
	bad, _ := NewImage(Present, []byte(`{"bad":`))
	if _, err := resolveLocalAuth(test.Context(), document, bad, *resolution.get[resolutionData](), syntheticProfile()); !errors.Is(err, ErrInvalidLocalImage) {
		test.Fatal("bad auth treated missing")
	}
	explicit, _ := indexTOML(test.Context(), []byte("cli_auth_credentials_store='keyring'"))
	if _, err := resolveLocalAuth(test.Context(), explicit, missing, *resolution.get[resolutionData](), syntheticProfile()); err == nil {
		test.Fatal("effective store mismatch")
	}
}

func TestCodexNativeProfileGate(test *testing.T) {
	if _, found := approvedProfile(syntheticProfile().id); found {
		test.Fatal("synthetic profile registered in production")
	}
	profile := syntheticProfile()
	if validateProfile(test.Context(), profile) != nil {
		test.Fatal("full synthetic profile rejected")
	}
	profile.parallel = parallelUnverified
	if !errors.Is(validateProfile(test.Context(), profile), ErrUnsupportedNativeProfile) {
		test.Fatal("missing parallel mapping accepted")
	}
	profile = syntheticProfile()
	profile.legacyInstructions = false
	if validateProfile(test.Context(), profile) == nil {
		test.Fatal("canonical instructions mismatch accepted")
	}
	profile = syntheticProfile()
	profile.ignoredRoot = append(profile.ignoredRoot, "invalid\xff")
	if validateProfile(test.Context(), profile) == nil {
		test.Fatal("invalid profile metadata can collapse binding digest")
	}
}

func TestCodexProfileTemplateContext(test *testing.T) {
	profile := syntheticProfile()
	offset := int64(2)
	for _, template := range []struct {
		name string
		raw  []byte
	}{{"root", profile.rootDefaults}, {"model", profile.modelDefaults}} {
		counter := &cancelAtContext{Context: test.Context(), at: -1}
		if _, err := readJSON(counter, template.raw); err != nil {
			test.Fatal("valid template counting control failed")
		}
		checks := counter.calls.Load()
		for step := range checks {
			phase := offset + step + 1
			for _, deadline := range []bool{false, true} {
				name := fmt.Sprintf("%s/check-%d/deadline-%t", template.name, step+1, deadline)
				test.Run(name, func(test *testing.T) {
					synctest.Test(test, func(test *testing.T) {
						images, resolution := syntheticInputs(test)
						defer images.Destroy()
						defer resolution.Destroy()
						var base context.Context
						var cancel context.CancelFunc
						var trigger context.CancelFunc
						want := context.Canceled
						if deadline {
							base, cancel = context.WithTimeout(test.Context(), time.Second)
							trigger = func() {
								time.Sleep(time.Second)
								synctest.Wait()
							}
							want = context.DeadlineExceeded
						} else {
							base, cancel = context.WithCancel(test.Context())
							trigger = cancel
						}
						defer cancel()
						ctx := &cancelAtContext{Context: base, cancel: trigger, at: phase}
						plan, err := checkProvider(ctx, images, goldenDeclaration(test), resolution, profile)
						defer plan.Destroy()
						if !errors.Is(base.Err(), want) || !errors.Is(err, want) || plan != (ProviderPlan{}) || ctx.calls.Load() != phase {
							test.Fatal("template context sentinel or zero plan lost at selected checkpoint")
						}
					})
				})
			}
		}
		offset += checks
	}
}

func TestCodexProfileTemplateValidation(test *testing.T) {
	for _, model := range []bool{false, true} {
		for _, fixture := range []struct {
			name string
			raw  string
			want error
		}{
			{"valid", "", nil},
			{"syntax", `{"TEMPLATE_PRIVATE_CANARY":`, ErrUnsupportedNativeProfile},
			{"non-object", `[]`, ErrUnsupportedNativeProfile},
			{"duplicate", `{"TEMPLATE_PRIVATE_CANARY":1,"TEMPLATE_PRIVATE_CANARY":2}`, ErrUnsupportedNativeProfile},
			{"depth", `{"TEMPLATE_PRIVATE_CANARY":` + strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33) + "}", ErrUnsupportedNativeProfile},
		} {
			test.Run(fmt.Sprintf("model-%t/%s", model, fixture.name), func(test *testing.T) {
				profile := syntheticProfile()
				if fixture.raw != "" {
					if model {
						profile.modelDefaults = []byte(fixture.raw)
					} else {
						profile.rootDefaults = []byte(fixture.raw)
					}
				}
				images, resolution := syntheticInputs(test)
				defer images.Destroy()
				defer resolution.Destroy()
				plan, err := checkProvider(test.Context(), images, goldenDeclaration(test), resolution, profile)
				defer plan.Destroy()
				if fixture.want == nil {
					if err != nil || plan == (ProviderPlan{}) {
						test.Fatal("valid template rejected or returned zero plan")
					}
				} else if !errors.Is(err, fixture.want) || err.Error() != fixture.want.Error() || plan != (ProviderPlan{}) {
					test.Fatal("invalid template bypassed validation, exposed parser detail, or returned a plan")
				}
			})
		}
	}
}

func TestCodexOwnershipConflict(test *testing.T) {
	declaration := goldenDeclaration(test)
	missing, _ := NewImage(Missing, nil)
	for _, raw := range []string{"[model_providers.cognate]\nenv_key='PRIVATE_CANARY'", "base_url='https://legacy.invalid'", "[profiles.active]\nmodel='foreign'"} {
		document, err := indexTOML(test.Context(), []byte(raw))
		if err != nil {
			test.Fatal("fixture syntax")
		}
		resolution := syntheticResolution(test, Managed, Absent, nil, File, KeyringNotObserved, NoCredentialLogin)
		if validateLocal(test.Context(), document, missing, declaration, *resolution.get[resolutionData](), syntheticProfile()) == nil {
			test.Fatal("actionable conflict accepted")
		}
	}
	document, _ := indexTOML(test.Context(), []byte("[model_providers.cognate]\nname='existing'"))
	resolution := syntheticResolution(test, Absent, Absent, nil, File, KeyringNotObserved, NoCredentialLogin)
	if !errors.Is(validateLocal(test.Context(), document, missing, declaration, *resolution.get[resolutionData](), syntheticProfile()), ErrOwnershipConflict) {
		test.Fatal("same name treated owned")
	}
}
