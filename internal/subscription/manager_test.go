package subscription_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/skillhub/internal/cli"
	"github.com/ca-x/skillhub/internal/install"
	"github.com/ca-x/skillhub/internal/packclient"
	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/auth"
	"github.com/ca-x/skillhub/internal/server/httpapi"
	"github.com/ca-x/skillhub/internal/server/packs"
	serverstate "github.com/ca-x/skillhub/internal/server/state"
	"github.com/ca-x/skillhub/internal/skill"
	clientstate "github.com/ca-x/skillhub/internal/state"
	"github.com/ca-x/skillhub/internal/subscription"
	"github.com/spf13/afero"
)

type archiveSource struct {
	archives map[string][]byte
	calls    atomic.Int64
}

func (source *archiveSource) WriteSkillArchive(_ context.Context, _, _, hash string, destination io.Writer) error {
	source.calls.Add(1)
	data, found := source.archives[hash]
	if !found {
		return fmt.Errorf("fixture archive unavailable")
	}
	_, err := destination.Write(data)
	return err
}

type fixture struct {
	manager   *subscription.Manager
	store     *serverstate.Store
	packs     *packs.Service
	source    *archiveSource
	server    *httptest.Server
	home      string
	directory string
	key       []byte
	next      int
}

func newFixture(test *testing.T) *fixture {
	test.Helper()
	if runtime.GOOS != "linux" {
		test.Skip("Linux installer")
	}
	setup := &fixture{home: test.TempDir(), directory: test.TempDir(), key: bytes.Repeat([]byte{13}, 32), source: &archiveSource{archives: make(map[string][]byte)}}
	for _, directory := range []string{setup.directory, filepath.Join(setup.home, ".agents", "skills"), filepath.Join(setup.home, ".claude", "skills")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			test.Fatal(err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			test.Fatal(err)
		}
	}
	serverDirectory := test.TempDir()
	if err := os.Chmod(serverDirectory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := serverstate.Open(test.Context(), filepath.Join(serverDirectory, "server.db"), bytes.Repeat([]byte{17}, 32))
	if err != nil {
		test.Fatal(err)
	}
	setup.store = store
	test.Cleanup(func() { _ = store.Close() })
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	authentication, err := auth.NewService(store, bytes.Repeat([]byte{17}, 32))
	if err != nil {
		test.Fatal(err)
	}
	setup.packs, err = packs.NewService(store, setup.source, bytes.Repeat([]byte{17}, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(setup.packs.Close)
	setup.server = httptest.NewServer(httpapi.NewWithOptions(authentication, httpapi.Options{Store: store, Packs: setup.packs}))
	test.Cleanup(setup.server.Close)
	setup.manager, err = subscription.Open(test.Context(), setup.home, setup.directory, setup.key, setup.server.Client())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = setup.manager.Close() })
	return setup
}

func (setup *fixture) newSkill(test *testing.T, content string) (serverstate.CollectedSkill, string) {
	test.Helper()
	setup.next++
	filesystem := afero.NewMemMapFs()
	if err := filesystem.MkdirAll("/demo/.hidden/empty", 0o700); err != nil {
		test.Fatal(err)
	}
	for name, data := range map[string][]byte{"SKILL.md": []byte("---\nname: Demo\ndescription: Example\n---\n" + content), ".hidden/payload.bin": {0, 255, 128, 0}, "run": []byte("#!/bin/sh\necho hello\n")} {
		mode := os.FileMode(0o600)
		if name == "run" {
			mode = 0o700
		}
		if err := afero.WriteFile(filesystem, "/demo/"+name, data, mode); err != nil {
			test.Fatal(err)
		}
	}
	hash, err := skill.ContentHash(filesystem, "/demo")
	if err != nil {
		test.Fatal(err)
	}
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	if err := afero.Walk(filesystem, "/demo", func(path string, info os.FileInfo, failure error) error {
		if failure != nil {
			return failure
		}
		relative := strings.TrimPrefix(path, "/demo")
		header := &tar.Header{Name: "skill" + relative, Mode: int64(info.Mode().Perm()), Size: info.Size(), Typeflag: tar.TypeReg}
		if info.IsDir() {
			header.Name += "/"
			header.Typeflag, header.Size = tar.TypeDir, 0
		}
		if err := archive.WriteHeader(header); err != nil || info.IsDir() {
			return err
		}
		data, err := afero.ReadFile(filesystem, path)
		if err != nil {
			return err
		}
		_, err = archive.Write(data)
		return err
	}); err != nil {
		test.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		test.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		test.Fatal(err)
	}
	setup.source.archives[hash] = bytes.Clone(buffer.Bytes())
	reference, err := repository.ParseReference(fmt.Sprintf("org/repository-%d", setup.next), "")
	if err != nil {
		test.Fatal(err)
	}
	collected, err := setup.store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("c", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Demo", Description: "Example", Path: "skills/demo"}, ContentHash: hash}})
	if err != nil {
		test.Fatal(err)
	}
	collected, err = setup.store.CollectedRepository(test.Context(), "alice", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	return collected.Skills[0], hash
}

func (setup *fixture) newPack(test *testing.T, name, skillID string) (serverstate.Pack, string) {
	test.Helper()
	pack, err := setup.store.CreatePack(test.Context(), "alice", serverstate.PackDraft{Name: name, Items: []serverstate.PackSelection{{SkillID: skillID, Slug: "demo"}}})
	if err != nil {
		test.Fatal(err)
	}
	issued, err := setup.packs.CreateShare(test.Context(), "alice", pack.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	return pack, issued.Token.Reveal()
}

func TestOverlappingSubscriptionsInstallOnceAndRemovalPreservesRemainingOwner(test *testing.T) {
	setup := newFixture(test)
	selected, hash := setup.newSkill(test, "First version")
	_, firstToken := setup.newPack(test, "First", selected.ID)
	_, secondToken := setup.newPack(test, "Second", selected.ID)
	first, err := setup.manager.Subscribe(test.Context(), setup.server.URL, firstToken)
	if err != nil {
		test.Fatal(err)
	}
	second, err := setup.manager.Subscribe(test.Context(), setup.server.URL, secondToken)
	if err != nil {
		test.Fatal(err)
	}
	result, err := setup.manager.Sync(test.Context())
	if err != nil || result.Installed != 1 || setup.source.calls.Load() != 1 {
		test.Fatalf("overlap did not install exactly once: %+v, %v", result, err)
	}
	canonical := filepath.Join(setup.home, ".agents", "skills", "demo")
	actual, err := skill.ContentHash(afero.NewOsFs(), canonical)
	if err != nil || actual != hash {
		test.Fatalf("installed content hash mismatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(setup.home, ".claude", "skills", "demo", ".hidden", "payload.bin")); err != nil {
		test.Fatal(err)
	}
	result, err = setup.manager.Sync(test.Context())
	if err != nil || result.Reused != 1 || setup.source.calls.Load() != 1 {
		test.Fatalf("unchanged sync downloaded content: %+v, %v", result, err)
	}
	if err := setup.manager.Unsubscribe(test.Context(), first.ID); err != nil {
		test.Fatal(err)
	}
	if _, err := setup.manager.Sync(test.Context()); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Stat(canonical); err != nil {
		test.Fatal("first removal deleted another subscription's content")
	}
	if err := setup.manager.Unsubscribe(test.Context(), second.ID); err != nil {
		test.Fatal(err)
	}
	result, err = setup.manager.Sync(test.Context())
	if err != nil || result.Removed != 1 || len(result.Backups) != 1 {
		test.Fatalf("final removal did not back up/uninstall: %+v, %v", result, err)
	}
	for _, path := range []string{canonical, filepath.Join(setup.home, ".claude", "skills", "demo")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			test.Fatal("uninstall left a target")
		}
	}
	for _, path := range []string{filepath.Join(setup.directory, "client.db"), filepath.Join(setup.directory, "client.db-wal")} {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			test.Fatal(err)
		}
		if bytes.Contains(data, []byte(firstToken)) || bytes.Contains(data, []byte(secondToken)) {
			test.Fatal("share credential persisted in plaintext")
		}
	}
}

func TestSubscriptionConflictFailsBeforeFilesystemMutation(test *testing.T) {
	setup := newFixture(test)
	first, _ := setup.newSkill(test, "First")
	second, _ := setup.newSkill(test, "Second")
	_, firstToken := setup.newPack(test, "First", first.ID)
	_, secondToken := setup.newPack(test, "Second", second.ID)
	for _, token := range []string{firstToken, secondToken} {
		if _, err := setup.manager.Subscribe(test.Context(), setup.server.URL, token); err != nil {
			test.Fatal(err)
		}
	}
	if _, err := setup.manager.Sync(test.Context()); err == nil {
		test.Fatal("conflicting slug/hash was silently overwritten")
	}
	if setup.source.calls.Load() != 0 {
		test.Fatal("conflict downloaded content")
	}
	if _, err := os.Stat(filepath.Join(setup.home, ".agents", "skills", "demo")); !os.IsNotExist(err) {
		test.Fatal("conflict mutated canonical content")
	}
}

func TestSubscriptionUpdateBacksUpAndPreservesLocalEdits(test *testing.T) {
	setup := newFixture(test)
	first, _ := setup.newSkill(test, "First")
	pack, token := setup.newPack(test, "First", first.ID)
	if _, err := setup.manager.Subscribe(test.Context(), setup.server.URL, token); err != nil {
		test.Fatal(err)
	}
	if _, err := setup.manager.Sync(test.Context()); err != nil {
		test.Fatal(err)
	}
	second, expected := setup.newSkill(test, "Second")
	if _, err := setup.store.UpdatePack(test.Context(), "alice", pack.ID, pack.Revision, serverstate.PackDraft{Name: "Changed", Items: []serverstate.PackSelection{{SkillID: second.ID, Slug: "demo"}}}); err != nil {
		test.Fatal(err)
	}
	result, err := setup.manager.Sync(test.Context())
	if err != nil || result.Updated != 1 || len(result.Backups) != 1 {
		test.Fatalf("version update failed: %+v, %v", result, err)
	}
	canonical := filepath.Join(setup.home, ".agents", "skills", "demo")
	actual, err := skill.ContentHash(afero.NewOsFs(), canonical)
	if err != nil || actual != expected {
		test.Fatal("updated logical hash differs")
	}
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("User-edited skill"), 0o600); err != nil {
		test.Fatal(err)
	}
	if _, err := setup.manager.Sync(test.Context()); err == nil {
		test.Fatal("user edits accepted as applied content")
	}
	content, err := os.ReadFile(filepath.Join(canonical, "SKILL.md"))
	if err != nil || string(content) != "User-edited skill" {
		test.Fatal("user edits were overwritten")
	}
}

func TestFailedDownloadKeepsPendingSnapshotWithoutAdvancingAppliedState(test *testing.T) {
	setup := newFixture(test)
	selected, hash := setup.newSkill(test, "First")
	_, token := setup.newPack(test, "First", selected.ID)
	if _, err := setup.manager.Subscribe(test.Context(), setup.server.URL, token); err != nil {
		test.Fatal(err)
	}
	valid := setup.source.archives[hash]
	setup.source.archives[hash] = []byte("not a skill archive")
	if _, err := setup.manager.Sync(test.Context()); err == nil {
		test.Fatal("malformed archive installed")
	}
	records, err := setup.manager.List(test.Context())
	if err != nil || len(records) != 1 || records[0].AppliedRevision != 0 || records[0].DesiredRevision != 1 {
		test.Fatalf("failed application advanced applied state: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(setup.home, ".agents", "skills", "demo")); !os.IsNotExist(err) {
		test.Fatal("malformed transfer published files")
	}
	setup.source.archives[hash] = valid
	if _, err := setup.manager.Sync(test.Context()); err != nil {
		test.Fatal(err)
	}
}

func TestMatchingLocalInstallIsNotClaimedOrDeletedBySubscriptions(test *testing.T) {
	setup := newFixture(test)
	selected, _ := setup.newSkill(test, "First")
	_, token := setup.newPack(test, "First", selected.ID)
	client, err := packclient.New(setup.server.URL, token, setup.server.Client())
	if err != nil {
		test.Fatal(err)
	}
	manifest, err := client.Manifest(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	snapshot, err := client.Download(test.Context(), manifest, manifest.Skills[0])
	if err != nil {
		test.Fatal(err)
	}
	defer snapshot.Close()
	if err := setup.manager.Close(); err != nil {
		test.Fatal(err)
	}
	session, err := install.OpenSession(test.Context(), setup.directory)
	if err != nil {
		test.Fatal(err)
	}
	if err := session.InstallLocal(test.Context(), setup.home, "demo", snapshot.Path()); err != nil {
		_ = session.Close()
		test.Fatal(err)
	}
	if err := session.Close(); err != nil {
		test.Fatal(err)
	}
	setup.manager, err = subscription.Open(test.Context(), setup.home, setup.directory, setup.key, setup.server.Client())
	if err != nil {
		test.Fatal(err)
	}
	record, err := setup.manager.Subscribe(test.Context(), setup.server.URL, token)
	if err != nil {
		test.Fatal(err)
	}
	result, err := setup.manager.Sync(test.Context())
	if err != nil || result.Reused != 1 {
		test.Fatalf("matching local install not reused: %+v %v", result, err)
	}
	if err := setup.manager.Unsubscribe(test.Context(), record.ID); err != nil {
		test.Fatal(err)
	}
	result, err = setup.manager.Sync(test.Context())
	if err != nil || result.Removed != 0 {
		test.Fatalf("unrelated local install was removed: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(setup.home, ".claude", "skills", "demo", "SKILL.md")); err != nil {
		test.Fatal("local installation did not survive unsubscribe")
	}
}

func TestCredentialKeyMismatchFailsBeforeMutatingSubscriptions(test *testing.T) {
	setup := newFixture(test)
	selected, _ := setup.newSkill(test, "First")
	pack, token := setup.newPack(test, "First", selected.ID)
	first, err := setup.manager.Subscribe(test.Context(), setup.server.URL, token)
	if err != nil {
		test.Fatal(err)
	}
	issued, err := setup.packs.CreateShare(test.Context(), "alice", pack.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	rotated, err := setup.manager.Subscribe(test.Context(), setup.server.URL, issued.Token.Reveal())
	if err != nil || rotated.ID != first.ID || rotated.Version != first.Version+1 {
		test.Fatalf("same pack created a duplicate subscription: %v", err)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		formatted := fmt.Sprintf(format, setup.manager)
		if strings.Contains(formatted, token) || strings.Contains(formatted, issued.Token.Reveal()) {
			test.Fatal("manager formatting leaked credentials")
		}
	}
	if err := setup.manager.Close(); err != nil {
		test.Fatal(err)
	}
	if incorrect, err := subscription.Open(test.Context(), setup.home, setup.directory, bytes.Repeat([]byte{33}, 32), setup.server.Client()); err == nil {
		_ = incorrect.Close()
		test.Fatal("wrong client key accepted existing encrypted subscriptions")
	}
	setup.manager, err = subscription.Open(test.Context(), setup.home, setup.directory, setup.key, setup.server.Client())
	if err != nil {
		test.Fatal(err)
	}
	if _, err := setup.manager.Sync(test.Context()); err != nil {
		test.Fatal(err)
	}
}

func TestManagerCloseCancelsInFlightManifest(test *testing.T) {
	setup := newFixture(test)
	selected, _ := setup.newSkill(test, "First")
	_, token := setup.newPack(test, "First", selected.ID)
	started := make(chan struct{})
	blocked := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer blocked.Close()
	finished := make(chan error, 1)
	go func() {
		_, err := setup.manager.Subscribe(test.Context(), blocked.URL, token)
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		test.Fatal("manifest request never started")
	}
	closed := make(chan error, 1)
	go func() { closed <- setup.manager.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(time.Second):
		test.Fatal("Close did not cancel and join manifest request")
	}
	if err := <-finished; err == nil {
		test.Fatal("cancelled request reported success")
	}
}

func TestPartialSyncKeepsDurableOwnershipAndRetriesWithoutRedownload(test *testing.T) {
	setup := newFixture(test)
	first, _ := setup.newSkill(test, "First")
	second, secondHash := setup.newSkill(test, "Second")
	pack, err := setup.store.CreatePack(test.Context(), "alice", serverstate.PackDraft{Name: "Two skills", Items: []serverstate.PackSelection{{SkillID: first.ID, Slug: "demo"}, {SkillID: second.ID, Slug: "second"}}})
	if err != nil {
		test.Fatal(err)
	}
	issued, err := setup.packs.CreateShare(test.Context(), "alice", pack.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := setup.manager.Subscribe(test.Context(), setup.server.URL, issued.Token.Reveal()); err != nil {
		test.Fatal(err)
	}
	valid := setup.source.archives[secondHash]
	setup.source.archives[secondHash] = []byte("broken archive")
	partial, err := setup.manager.Sync(test.Context())
	if err == nil || partial.Installed != 1 {
		test.Fatalf("expected one completed install before failed second transfer: %+v %v", partial, err)
	}
	records, err := setup.manager.List(test.Context())
	if err != nil || records[0].AppliedRevision != 0 || records[0].DesiredRevision != 1 {
		test.Fatal("partial sync was reported as fully applied")
	}
	setup.source.archives[secondHash] = valid
	result, err := setup.manager.Sync(test.Context())
	if err != nil || result.Reused != 1 || result.Installed != 1 || setup.source.calls.Load() != 3 {
		test.Fatalf("retry did not reuse completed managed install: %+v %v", result, err)
	}
	if err := setup.manager.Unsubscribe(test.Context(), records[0].ID); err != nil {
		test.Fatal(err)
	}
	result, err = setup.manager.Sync(test.Context())
	if err != nil || result.Removed != 2 {
		test.Fatalf("partial install lost cleanup ownership: %+v %v", result, err)
	}
}

func TestCLISubscribeListSyncAndRemoveRealServerFlow(test *testing.T) {
	setup := newFixture(test)
	selected, _ := setup.newSkill(test, "First")
	_, token := setup.newPack(test, "CLI pack", selected.ID)
	test.Setenv("HOME", setup.home)
	run := func(input string, arguments ...string) string {
		test.Helper()
		var output bytes.Buffer
		command := cli.New()
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetIn(strings.NewReader(input))
		command.SetArgs(arguments)
		err := command.ExecuteContext(test.Context())
		if strings.Contains(output.String(), token) || err != nil && strings.Contains(err.Error(), token) {
			test.Fatal("CLI flow leaked a credential")
		}
		if err != nil {
			test.Fatal(err)
		}
		return output.String()
	}
	if output := run(token+"\n", "pack", "subscribe", "--server", setup.server.URL, "--token-stdin"); !strings.Contains(output, "新增 1") {
		test.Fatal("CLI did not install a subscribed pack")
	}
	if output := run("", "pack", "list"); !strings.Contains(output, "CLI pack") || !strings.Contains(output, "已应用") {
		test.Fatal("CLI list did not show applied metadata")
	}
	if output := run("", "pack", "sync"); !strings.Contains(output, "复用 1") {
		test.Fatal("CLI unchanged sync did not reuse content")
	}
	keyPath := filepath.Join(setup.home, ".config", "skillhub", "client.key")
	if err := os.Rename(keyPath, keyPath+".saved"); err != nil {
		test.Fatal(err)
	}
	missingKey := cli.New()
	missingKey.SetArgs([]string{"pack", "list"})
	if err := missingKey.ExecuteContext(test.Context()); err == nil {
		test.Fatal("existing subscriptions accepted a lost key")
	}
	if _, err := os.Lstat(keyPath); !os.IsNotExist(err) {
		test.Fatal("lost client key was silently regenerated for existing subscriptions")
	}
	if err := os.Rename(keyPath+".saved", keyPath); err != nil {
		test.Fatal(err)
	}
	directory := filepath.Join(setup.home, ".local", "share", "skillhub")
	store, err := clientstate.Open(test.Context(), filepath.Join(directory, "client.db"))
	if err != nil {
		test.Fatal(err)
	}
	records, err := store.Subscriptions(test.Context())
	if err != nil || len(records) != 1 {
		_ = store.Close()
		test.Fatal("CLI did not persist one subscription")
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	if output := run("", "pack", "remove", records[0].ID); !strings.Contains(output, "移除 1") || !strings.Contains(output, "备份") {
		test.Fatal("CLI removal did not preserve a backup")
	}
	if output := run("", "pack", "list"); !strings.Contains(output, "没有") {
		test.Fatal("removed CLI subscription remains listed")
	}
}
