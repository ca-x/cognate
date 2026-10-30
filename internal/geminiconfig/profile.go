package geminiconfig

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"slices"
	"strings"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func validateProfile(ctx context.Context, value nativeProfile) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if !validModel(value.id) || len(value.id) > 128 || value.dialect != "dotenv17-jsonc-v1" || value.sourceDigest == [32]byte{} || len(value.ignoredEnv) > 512 || len(value.ignoredJSON) > 512 {
		return ErrUnsupportedNativeProfile
	}
	for _, names := range [][]string{value.ignoredEnv, value.ignoredJSON} {
		for index, name := range names {
			if len(name) < 1 || len(name) > 256 || slices.Contains(names[:index], name) {
				return ErrUnsupportedNativeProfile
			}
		}
	}
	return checkContext(ctx)
}

var deniedEnv = [...]string{"GOOGLE_API_KEY", "GOOGLE_GENAI_USE_VERTEXAI", "GOOGLE_GENAI_USE_GCA", "GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID", "GOOGLE_CLOUD_LOCATION", "GOOGLE_VERTEX_BASE_URL", "GOOGLE_GENAI_API_VERSION", "GEMINI_CLI_USE_COMPUTE_ADC", "GEMINI_DEFAULT_AUTH_TYPE", "GEMINI_CLI_CUSTOM_HEADERS", "CODE_ASSIST_ENDPOINT", "CODE_ASSIST_API_VERSION", "GEMINI_CLI_HOME", "GEMINI_SANDBOX", "GEMINI_SYSTEM_MD", "GEMINI_CLI_SYSTEM_SETTINGS_PATH", "GEMINI_CLI_SYSTEM_DEFAULTS_PATH", "GEMINI_CLI_TRUST_WORKSPACE", "GEMINI_RESTRICTED_MODE", "GEMINI_CLI_TRUSTED_FOLDERS_PATH", "CLOUD_SHELL", "NODE_OPTIONS", "NODE_EXTRA_CA_CERTS", "HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"}

func validateSettingsScope(ctx context.Context, node *jsonNode, path string, profile nativeProfile) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	switch path {
	case "/model/name", "/security/auth/selectedType":
		if node.kind != '"' {
			return ErrInvalidLocalImage
		}
		if path == "/model/name" && !validModel(node.text) || path == "/security/auth/selectedType" && node.text != "oauth-personal" && node.text != "gemini-api-key" {
			return ErrUnresolvedLocalAuth
		}
		return nil
	case "/security/auth/enforcedType", "/security/auth/useExternal", "/modelConfigs", "/tools/sandbox", "/advanced/ignoreLocalEnv", "/advanced/excludedEnvVars":
		return ErrUnresolvedLocalAuth
	case "", "/model", "/security", "/security/auth":
		if node.kind != '{' {
			return ErrInvalidLocalImage
		}
		for _, member := range node.members {
			segment := strings.ReplaceAll(strings.ReplaceAll(member.name, "~", "~0"), "/", "~1")
			if err := validateSettingsScope(ctx, member.value, path+"/"+segment, profile); err != nil {
				return err
			}
		}
		return nil
	default:
		if !slices.Contains(profile.ignoredJSON, path) {
			return ErrOwnershipConflict
		}
		return nil
	}
}

func validateLocal(ctx context.Context, env *envDocument, settings *jsonDocument, local resolutionData, profile nativeProfile) error {
	if local.profileID != profile.id || local.scope != CompleteEffective || local.witness == [32]byte{} {
		return ErrUnresolvedLocalAuth
	}
	for _, entry := range env.entries {
		if err := checkContext(ctx); err != nil {
			return err
		}
		if slices.Contains(deniedEnv[:], entry.name) {
			return ErrUnresolvedLocalAuth
		}
		if slices.Contains(fields[:4], Field(entry.name)) {
			continue
		}
		if !slices.Contains(profile.ignoredEnv, entry.name) {
			return ErrOwnershipConflict
		}
	}
	return validateSettingsScope(ctx, settings.root, "", profile)
}

type nativeProfile struct {
	id, dialect             string
	sourceDigest            [32]byte
	ignoredEnv, ignoredJSON []string
}

func profileDigest(value nativeProfile) [32]byte {
	encoded, _ := json.Marshal(struct {
		ID, Dialect string
		Source      [32]byte
		Env, JSON   []string
	}{value.id, value.dialect, value.sourceDigest, value.ignoredEnv, value.ignoredJSON})
	return sha256.Sum256(encoded)
}
func CheckProvider(ctx context.Context, images LocalImages, declaration agentconfig.GeminiProviderDeclarationV1, local LocalResolution) (result ProviderPlan, returnedError error) {
	defer func() {
		if err := checkContext(ctx); err != nil {
			result.Destroy()
			result = ProviderPlan{}
			returnedError = err
		}
	}()
	if err := checkContext(ctx); err != nil {
		return ProviderPlan{}, err
	}
	return ProviderPlan{}, ErrUnsupportedNativeProfile
}
