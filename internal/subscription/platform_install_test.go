//go:build darwin

package subscription_test

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/cli"
	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/packs"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	clientstate "github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/subscription"
	"github.com/spf13/afero"
)

func newDarwinExecutionFixture(test *testing.T) *fixture {
	test.Helper()

	home, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil || os.Chmod(home, 0700) != nil {
		test.Fatal("DARWIN_INFRA canonical pack home")
	}
	directory, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil {
		test.Fatal("DARWIN_INFRA canonical pack state")
	}
	setup := &fixture{home: home, directory: directory, key: bytes.Repeat([]byte{13}, 32), source: &archiveSource{archives: make(map[string][]byte)}}
	for _, directory := range []string{setup.directory, filepath.Join(setup.home, ".agents", "skills"), filepath.Join(setup.home, ".claude", "skills")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			test.Fatal("DARWIN_INFRA real pack fixture")
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			test.Fatal("DARWIN_INFRA real pack fixture")
		}
	}
	serverDirectory, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil || os.Chmod(serverDirectory, 0o700) != nil {
		test.Fatal("DARWIN_INFRA real pack fixture")
	}
	store, err := serverstate.Open(test.Context(), filepath.Join(serverDirectory, "server.db"), bytes.Repeat([]byte{17}, 32))
	if err != nil {
		test.Fatal("DARWIN_INFRA real pack fixture")
	}
	setup.store = store
	test.Cleanup(func() { _ = store.Close() })
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal("DARWIN_INFRA real pack fixture")
	}
	authentication, err := auth.NewService(store, bytes.Repeat([]byte{17}, 32))
	if err != nil {
		test.Fatal("DARWIN_INFRA real pack fixture")
	}
	setup.packs, err = packs.NewService(store, setup.source, bytes.Repeat([]byte{17}, 32))
	if err != nil {
		test.Fatal("DARWIN_INFRA real pack fixture")
	}
	test.Cleanup(setup.packs.Close)
	setup.server = httptest.NewServer(httpapi.NewWithOptions(authentication, httpapi.Options{Store: store, Packs: setup.packs}))
	test.Cleanup(setup.server.Close)
	setup.manager, err = subscription.Open(test.Context(), setup.home, setup.directory, setup.key, setup.server.Client())
	if err != nil {
		test.Fatal("DARWIN_INFRA real pack fixture")
	}
	test.Cleanup(func() { _ = setup.manager.Close() })
	return setup
}

func TestDarwinExecutionPackLifecycle(test *testing.T) {
	setup := newDarwinExecutionFixture(test)
	test.Setenv("HOME", setup.home)
	first, firstHash := setup.newSkill(test, "Darwin first")
	second, secondHash := setup.newSkill(test, "Darwin second")
	firstPack, firstToken := setup.newPack(test, "First", first.ID)
	secondPack, secondToken := setup.newPack(test, "Second", first.ID)
	run := func(input string, args ...string) {
		test.Helper()
		command := cli.New()
		command.SetArgs(args)
		command.SetIn(strings.NewReader(input))
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		err := command.ExecuteContext(test.Context())
		if strings.Contains(output.String(), firstToken) || strings.Contains(output.String(), secondToken) {
			test.Fatal("pack command exposed sharing credential")
		}
		if err != nil {
			if errors.Is(err, clientkey.ErrUnsupported) {
				test.Log("DARWIN_BASELINE_REJECTION code=pack-core-unsupported")
			}
			test.Fatal("Darwin real Pack CLI command must succeed")
		}
	}
	run("", "client-key", "init")
	run(firstToken+"\n", "pack", "subscribe", "--server", setup.server.URL, "--token-stdin")
	run(secondToken+"\n", "pack", "subscribe", "--server", setup.server.URL, "--token-stdin")
	canonical := filepath.Join(setup.home, ".agents", "skills", "demo")
	if actual, err := skill.ContentHash(afero.NewOsFs(), canonical); err != nil || actual != firstHash || setup.source.calls.Load() != 1 {
		test.Fatal("two packs did not share exactly one initial archive")
	}
	for _, pack := range []serverstate.Pack{firstPack, secondPack} {
		if _, err := setup.store.UpdatePack(test.Context(), "alice", pack.ID, pack.Revision, serverstate.PackDraft{Name: pack.Name, Items: []serverstate.PackSelection{{SkillID: second.ID, Slug: "demo"}}}); err != nil {
			test.Fatal("DARWIN_INFRA upstream update")
		}
	}
	run("", "pack", "sync")
	if actual, err := skill.ContentHash(afero.NewOsFs(), canonical); err != nil || actual != secondHash || setup.source.calls.Load() != 2 {
		test.Fatal("two packs did not update with exactly one new archive")
	}
	directory := filepath.Join(setup.home, ".local", "share", "skillhub")
	local, err := clientstate.Open(test.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		test.Fatal("actual client database unavailable")
	}
	records, readErr := local.Subscriptions(test.Context())
	closeErr := local.Close()
	if readErr != nil || closeErr != nil || len(records) != 2 {
		test.Fatal("actual subscriptions not persisted")
	}
	for _, record := range records {
		if record.AppliedRevision == 0 || record.AppliedRevision != record.DesiredRevision {
			test.Fatal("pack revision not fully applied")
		}
	}
	run("", "pack", "remove", records[0].ID)
	if _, err := os.Lstat(canonical); err != nil {
		test.Fatal("one removal deleted remaining pack's content")
	}
	run("", "pack", "remove", records[1].ID)
	for _, path := range []string{canonical, filepath.Join(setup.home, ".claude", "skills", "demo")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("last removal left managed skill or projection")
		}
	}
	session, err := install.OpenSession(test.Context(), directory)
	if err != nil {
		test.Fatal("backup inspection session failed")
	}
	backups, readErr := session.Backups(test.Context())
	closeErr = session.Close()
	if readErr != nil || closeErr != nil || len(backups) < 2 {
		test.Fatal("updates/removal did not preserve backups")
	}
	backupID := ""
	for _, backup := range backups {
		if backup.InstallationHash == firstHash {
			backupID = backup.ID
		}
	}
	if backupID == "" {
		test.Fatal("original version backup missing")
	}
	restore, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil || os.Chmod(restore, 0700) != nil {
		test.Fatal("DARWIN_INFRA restore parent")
	}
	run("", "backup-list")
	run("", "restore-local", backupID, restore, "restored")
	if actual, err := skill.ContentHash(afero.NewOsFs(), filepath.Join(restore, "restored")); err != nil || actual != firstHash {
		test.Fatal("restored original pack content mismatch")
	}
	if setup.source.calls.Load() != 2 {
		test.Fatal("remove/restore unexpectedly downloaded content")
	}
}
