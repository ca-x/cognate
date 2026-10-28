package install_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ca-x/skillhub/internal/install"
	"github.com/ca-x/skillhub/internal/skill"
	"github.com/spf13/afero"
)

func TestInstallLocalWithHashRequiresExpectedContentWithoutSubscriptionOwnership(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only publication")
	}
	home, source, directory := t.TempDir(), t.TempDir(), t.TempDir()
	canonical := filepath.Join(home, ".agents", "skills")
	for _, path := range []string{canonical, filepath.Join(home, ".claude", "skills")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("verified"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := skill.ContentHashContext(t.Context(), afero.NewOsFs(), source)
	if err != nil {
		t.Fatal(err)
	}
	session, err := install.OpenSession(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, wrong := range []string{"", strings.Repeat("z", 64), strings.Repeat("0", 64)} {
		if _, err := session.InstallLocalWithHash(t.Context(), home, "verified", source, wrong); err == nil {
			t.Fatal("expected content hash was not required")
		}
		if _, err := os.Lstat(filepath.Join(canonical, "verified")); !os.IsNotExist(err) {
			t.Fatal("invalid content changed canonical installation")
		}
	}
	for attempt := range 2 {
		matched, err := session.InstallLocalWithHash(t.Context(), home, "verified", source, hash)
		if err != nil || matched != (attempt == 1) {
			t.Fatalf("verified install/reuse failed: %v", err)
		}
	}
	record, err := session.Installation(t.Context(), "verified")
	if err != nil || record.SubscriptionManaged || record.ContentHash != hash {
		t.Fatal("standalone public installation was claimed by subscription ownership")
	}
	if _, err := os.Readlink(filepath.Join(home, ".claude", "skills", "verified")); err != nil {
		t.Fatal("verified install did not project into detected agent")
	}
}
