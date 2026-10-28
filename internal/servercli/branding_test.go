package servercli

import (
	"os"
	"testing"
)

func TestCognateConfigurationEnvironment(test *testing.T) {
	for _, setting := range []struct{ suffix, flag string }{
		{"MASTER_KEY_FILE", "key-file"},
		{"GITHUB_CLIENT_ID", "github-client-id"},
		{"OIDC_ISSUER", "oidc-issuer"},
		{"OIDC_CLIENT_ID", "oidc-client-id"},
		{"OIDC_CLIENT_SECRET_FILE", "oidc-client-secret-file"},
	} {
		test.Run(setting.suffix, func(test *testing.T) {
			test.Setenv("SKILLHUB_"+setting.suffix, "legacy-fixture")
			test.Setenv("COGNATE_"+setting.suffix, "cognate-fixture")
			if actual := New().Flags().Lookup(setting.flag).DefValue; actual != "cognate-fixture" {
				test.Fatal("new environment name did not take precedence")
			}
			test.Setenv("COGNATE_"+setting.suffix, "")
			if actual := New().Flags().Lookup(setting.flag).DefValue; actual != "" {
				test.Fatal("explicit empty new value did not disable legacy fallback")
			}
		})
	}
}

func TestCognateEnvironmentLegacyFallback(test *testing.T) {
	test.Setenv("SKILLHUB_MASTER_KEY_FILE", "legacy-fixture")
	test.Setenv("COGNATE_MASTER_KEY_FILE", "")
	if err := os.Unsetenv("COGNATE_MASTER_KEY_FILE"); err != nil {
		test.Fatal(err)
	}
	if actual := New().Flags().Lookup("key-file").DefValue; actual != "legacy-fixture" {
		test.Fatal("legacy environment fallback unavailable")
	}
}
