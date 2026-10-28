package install_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"uuid"

	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/ca-x/cognate/internal/state"
	"github.com/spf13/afero"
)

func TestSubscriptionInstallClaimsOwnershipOnlyWithDurableDesiredMembership(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("Linux installer")
	}
	home, source, directory := test.TempDir(), test.TempDir(), test.TempDir()
	for _, path := range []string{directory, filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			test.Fatal(err)
		}
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: Demo\ndescription: Example\n---\nHello"), 0o600); err != nil {
		test.Fatal(err)
	}
	hash, err := skill.ContentHash(afero.NewOsFs(), source)
	if err != nil {
		test.Fatal(err)
	}
	session, err := install.OpenSession(test.Context(), directory)
	if err != nil {
		test.Fatal(err)
	}
	defer session.Close()
	if _, err := session.InstallSubscription(test.Context(), home, "demo", source, hash); err == nil {
		test.Fatal("unsubscribed operation claimed subscription ownership")
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "demo")); !os.IsNotExist(err) {
		test.Fatal("failed membership check published content")
	}
	store, err := state.Open(test.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	record, err := store.CreateSubscription(test.Context(), state.SubscriptionDraft{ID: uuid.New().String(), Server: "https://hub.example", PackID: "ExamplePack", Name: "Example", SealedCredential: make([]byte, 64)})
	if err != nil {
		test.Fatal(err)
	}
	if err := store.StageSubscriptionSync(test.Context(), []state.SubscriptionSnapshot{{ID: record.ID, ExpectedVersion: record.Version, Name: record.Name, Revision: 1, ManifestHash: strings.Repeat("c", 64), Skills: []state.SubscriptionSkill{{Slug: "demo", DesiredHash: hash}}}}); err != nil {
		test.Fatal(err)
	}
	if _, err := session.InstallSubscription(test.Context(), home, "demo", source, hash); err != nil {
		test.Fatal(err)
	}
	installed, err := session.Installation(test.Context(), "demo")
	if err != nil || !installed.SubscriptionManaged || installed.Generation == "" {
		test.Fatalf("subscription ownership not atomically recorded: %v", err)
	}
	if _, _, err := session.PreflightSubscription(test.Context(), home, "demo", hash); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agents", "skills", "demo", "SKILL.md"), []byte("local edit"), 0o600); err != nil {
		test.Fatal(err)
	}
	if _, _, err := session.PreflightSubscription(test.Context(), home, "demo", hash); err == nil {
		test.Fatal("preflight accepted local edits")
	}
}
