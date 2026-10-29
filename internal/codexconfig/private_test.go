package codexconfig

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/agentconfig"
)

func assertOpaque(test *testing.T, value any) {
	test.Helper()
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%020.4v", "%#020x"} {
		output := fmt.Sprintf(format, value)
		if strings.Contains(output, "PRIVATE_CANARY") || strings.Contains(output, "2147483647") {
			test.Fatal("private fmt leak")
		}
	}
	encoded, err := json.Marshal(value)
	if bytes.Contains(encoded, []byte("PRIVATE_CANARY")) || err != nil && strings.Contains(err.Error(), "PRIVATE_CANARY") {
		test.Fatal("private JSON leak")
	}
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Info("probe", "value", value)
	if strings.Contains(output.String(), "PRIVATE_CANARY") {
		test.Fatal("private slog leak")
	}
}

func TestCodexPrivateErrorsAndSizeBudgets(test *testing.T) {
	for _, raw := range []string{"PRIVATE_CANARY = {bad}", "x='PRIVATE_CANARY", "[PRIVATE_CANARY"} {
		_, err := indexTOML(test.Context(), []byte(raw))
		if err == nil || strings.Contains(err.Error(), "PRIVATE_CANARY") {
			test.Fatal("TOML error text leak")
		}
	}
	for _, raw := range []string{`{"PRIVATE_CANARY":`, `{"x":{"PRIVATE_CANARY":1,"PRIVATE_CANARY":2}}`} {
		_, err := readJSON(test.Context(), []byte(raw))
		if err == nil || strings.Contains(err.Error(), "PRIVATE_CANARY") {
			test.Fatal("JSON error text leak")
		}
	}
	if checkOutputSizes(MaxImageBytes, MaxImageBytes, MaxImageBytes, MaxImageBytes, true) != nil {
		test.Fatal("aggregate boundary")
	}
	for _, sizes := range [][4]int{{MaxImageBytes + 1, 0, 0, 0}, {0, MaxImageBytes + 1, 0, 0}, {0, 0, MaxImageBytes + 1, 0}, {0, 0, 0, MaxImageBytes + 1}} {
		if checkOutputSizes(sizes[0], sizes[1], sizes[2], sizes[3], true) != ErrCodecLimit {
			test.Fatal("output aggregate limit")
		}
	}
	for _, slot := range []agentconfig.CodexSecretSlot{"", "foreign", "codex.provider-key "} {
		if _, err := NewSelectedSecret(slot, []byte("PRIVATE_CANARY")); err != ErrInvalidSecret {
			test.Fatal("unsafe material slot")
		}
	}
	for _, value := range []any{OwnershipFacts{}, AuthFacts{}, LocalResolution{}, SelectedSecret{}, LocalImages{}} {
		assertOpaque(test, value)
	}
}

func TestCodexPrivateCarriers(test *testing.T) {
	image, _ := NewImage(Present, []byte("PRIVATE_CANARY"))
	secret, _ := NewSelectedSecret(agentconfig.CodexProviderKeySecret, []byte("PRIVATE_CANARY"))
	images, _ := NewImages(image, image, image)
	owners, _ := NewOwnership(Managed, Managed, []string{"PRIVATE_CANARY"})
	auth, _ := NewAuthFacts(File, KeyringNotObserved, CredentialLogin)
	resolution, _ := NewResolution("PRIVATE_CANARY", CompleteEffective, owners, auth, [32]byte{1})
	for _, value := range []any{image, &image, secret, &secret, images, owners, auth, resolution, LocalImage{}, ProviderPlan{}, ProviderCandidate{}, ProviderBinding{}, (*LocalImage)(nil)} {
		if _, err := json.Marshal(value); err != nil {
			test.Fatalf("wrapper JSON rejected %T", value)
		}
		assertOpaque(test, value)
		assertOpaque(test, []any{map[string]any{"nested": value}})
	}
	private := struct {
		hidden   LocalImage
		material SelectedSecret
	}{image, secret}
	assertOpaque(test, private)
	assertOpaque(test, []any{map[string]any{"nested": private}})
}
