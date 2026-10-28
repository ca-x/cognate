package desktop

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/packs"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/ca-x/cognate/internal/subscription"
	"github.com/egoist/mygo/ui"
	"github.com/spf13/afero"
)

type watchRequestGate struct {
	started, canceled, release chan struct{}
	startOnce, cancelOnce      sync.Once
}

func newWatchGate() *watchRequestGate {
	return &watchRequestGate{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
}

type watchArchiveSource struct {
	mutex    sync.Mutex
	archives map[string][]byte
	calls    atomic.Int32
}

func (source *watchArchiveSource) WriteSkillArchive(ctx context.Context, _, _, hash string, destination io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source.mutex.Lock()
	data := source.archives[hash]
	source.mutex.Unlock()
	if data == nil {
		return errors.New("synthetic archive unavailable")
	}
	source.calls.Add(1)
	_, err := destination.Write(data)
	return err
}

type watchServerFixture struct {
	store   *serverstate.Store
	packs   *packs.Service
	source  *watchArchiveSource
	server  *httptest.Server
	service *Service
	home    string
	gate    atomic.Pointer[watchRequestGate]
	next    int
}

func newWatchServer(test *testing.T) *watchServerFixture {
	test.Helper()
	if runtime.GOOS != "linux" {
		test.Skip("real native watch installation currently Linux only")
	}
	fixture := &watchServerFixture{home: test.TempDir(), source: &watchArchiveSource{archives: make(map[string][]byte)}}
	test.Setenv("HOME", fixture.home)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(fixture.home, ".config"))
	test.Setenv("XDG_DATA_HOME", filepath.Join(fixture.home, ".local", "share"))
	test.Setenv("TMPDIR", test.TempDir())
	if err := os.MkdirAll(filepath.Join(fixture.home, ".claude", "skills"), 0o700); err != nil {
		test.Fatal(err)
	}
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{23}, 32)
	var err error
	fixture.store, err = serverstate.Open(test.Context(), filepath.Join(directory, "server.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = fixture.store.Close() })
	if err := fixture.store.CreateUser(test.Context(), "watch-owner"); err != nil {
		test.Fatal(err)
	}
	authentication, err := auth.NewService(fixture.store, key)
	if err != nil {
		test.Fatal(err)
	}
	fixture.packs, err = packs.NewService(fixture.store, fixture.source, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(fixture.packs.Close)
	handler := httpapi.NewWithOptions(authentication, httpapi.Options{Store: fixture.store, Packs: fixture.packs})
	fixture.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if gate := fixture.gate.Load(); gate != nil {
			gate.startOnce.Do(func() { close(gate.started) })
			select {
			case <-request.Context().Done():
				gate.cancelOnce.Do(func() { close(gate.canceled) })
				return
			case <-gate.release:
			}
		}
		handler.ServeHTTP(response, request)
	}))
	test.Cleanup(fixture.server.Close)
	fixture.service = &Service{Home: fixture.home, HTTP: fixture.server.Client()}
	return fixture
}

func (fixture *watchServerFixture) version(test *testing.T, content string) (serverstate.CollectedSkill, string) {
	test.Helper()
	fixture.next++
	directory := test.TempDir()
	data := []byte("---\nname: Watch Demo\ndescription: Synthetic native background fixture\n---\n" + content)
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), data, 0o600); err != nil {
		test.Fatal(err)
	}
	hash, err := skill.ContentHashContext(test.Context(), afero.NewOsFs(), directory)
	if err != nil {
		test.Fatal(err)
	}
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	writer := tar.NewWriter(compressed)
	if err := writer.WriteHeader(&tar.Header{Name: "skill/", Mode: 0o700, Typeflag: tar.TypeDir}); err != nil {
		test.Fatal(err)
	}
	if err := writer.WriteHeader(&tar.Header{Name: "skill/SKILL.md", Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		test.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(writer.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	fixture.source.mutex.Lock()
	fixture.source.archives[hash] = bytes.Clone(archive.Bytes())
	fixture.source.mutex.Unlock()
	reference, err := repository.ParseReference(fmt.Sprintf("watch/version-%d", fixture.next), "")
	if err != nil {
		test.Fatal(err)
	}
	archiveHash := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
	collected, err := fixture.store.CollectRepository(test.Context(), "watch-owner", reference, archiveHash, []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Watch Demo", Description: "Fixture", Path: "skills/demo"}, ContentHash: hash}})
	if err != nil {
		test.Fatal(err)
	}
	collected, err = fixture.store.CollectedRepository(test.Context(), "watch-owner", collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	return collected.Skills[0], hash
}

func (fixture *watchServerFixture) subscribe(test *testing.T, name, skillID string) (serverstate.Pack, packs.IssuedShare) {
	test.Helper()
	pack, err := fixture.store.CreatePack(test.Context(), "watch-owner", serverstate.PackDraft{Name: name, Items: []serverstate.PackSelection{{SkillID: skillID, Slug: "watch-demo"}}})
	if err != nil {
		test.Fatal(err)
	}
	share, err := fixture.packs.CreateShare(test.Context(), "watch-owner", pack.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.service.Packs(test.Context(), "subscribe", fixture.server.URL, share.Token.Reveal(), ""); err != nil {
		test.Fatal("could not prepare real encrypted subscription")
	}
	return pack, share
}

func assertWatchLockReleased(test *testing.T, home string) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	session, err := install.OpenSession(ctx, filepath.Join(home, ".local", "share", "skillhub"))
	if err != nil {
		test.Fatal("background worker retained installer lock while idle or after stopping")
	}
	if err := session.Close(); err != nil {
		test.Fatal(err)
	}
}

func watchTestModel(test *testing.T, fixture *watchServerFixture) (*Model, *ui.Tester, chan func()) {
	test.Helper()
	callbacks := make(chan func(), 32)
	model := NewModel(fixture.service, func(update func()) { callbacks <- update })
	model.watchOptions = subscription.WatchOptions{Interval: time.Second, AttemptTimeout: 5 * time.Second}
	model.SetOrigin(fixture.server.URL)
	model.Page = 1
	test.Cleanup(func() { model.Close(); model.Wait() })
	return model, ui.NewTester(model.View, 920, 760), callbacks
}

func TestNativeWatchTwoPackUpdateRevocationAndLocalEdits(test *testing.T) {
	fixture := newWatchServer(test)
	first, firstHash := fixture.version(test, "First version")
	second, secondHash := fixture.version(test, "Second version")
	firstPack, firstShare := fixture.subscribe(test, "First background Pack", first.ID)
	secondPack, secondShare := fixture.subscribe(test, "Second background Pack", first.ID)
	model, tester, callbacks := watchTestModel(test, fixture)
	click(test, tester, "启用后台同步")
	nextWatchUpdate(test, callbacks)
	tester.Frame()
	if !strings.Contains(model.WatchStatus, "安装 1") || len(model.Subscriptions) != 2 || fixture.source.calls.Load() != 1 {
		test.Fatal("immediate native watch did not deduplicate two real packs")
	}
	canonical := filepath.Join(fixture.home, ".agents", "skills", "watch-demo")
	if hash, err := skill.ContentHash(afero.NewOsFs(), canonical); err != nil || hash != firstHash {
		test.Fatal("first background cycle installed wrong content")
	}
	assertWatchLockReleased(test, fixture.home)
	gate := newWatchGate()
	fixture.gate.Store(gate)
	for _, pack := range []serverstate.Pack{firstPack, secondPack} {
		if _, err := fixture.store.UpdatePack(test.Context(), "watch-owner", pack.ID, pack.Revision, serverstate.PackDraft{Name: pack.Name, Items: []serverstate.PackSelection{{SkillID: second.ID, Slug: "watch-demo"}}}); err != nil {
			test.Fatal(err)
		}
	}
	fixture.gate.Store(nil)
	close(gate.release)
	nextWatchUpdate(test, callbacks)
	tester.Frame()
	if !strings.Contains(model.WatchStatus, "更新 1") || fixture.source.calls.Load() != 2 {
		test.Fatal("periodic native cycle did not update overlapping packs once")
	}
	if hash, err := skill.ContentHash(afero.NewOsFs(), canonical); err != nil || hash != secondHash {
		test.Fatal("periodic cycle did not install new server content")
	}
	if content, err := os.ReadFile(filepath.Join(fixture.home, ".claude", "skills", "watch-demo", "SKILL.md")); err != nil || !bytes.Contains(content, []byte("Second version")) {
		test.Fatal("background update did not reach real agent projection")
	}
	if err := fixture.store.RevokePackShare(test.Context(), "watch-owner", secondPack.ID, secondShare.Info.ID); err != nil {
		test.Fatal(err)
	}
	nextWatchUpdate(test, callbacks)
	if !strings.Contains(model.WatchStatus, "后台同步未完成") || fixture.source.calls.Load() != 2 {
		test.Fatal("revoked pack was reported successful or downloaded new data")
	}
	if hash, err := skill.ContentHash(afero.NewOsFs(), canonical); err != nil || hash != secondHash {
		test.Fatal("revocation silently removed local content")
	}
	assertWatchLockReleased(test, fixture.home)
	click(test, tester, "停止后台同步")
	nextWatchUpdate(test, callbacks)
	tester.Frame()
	replacement, err := fixture.packs.CreateShare(test.Context(), "watch-owner", secondPack.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.service.Packs(test.Context(), "subscribe", fixture.server.URL, replacement.Token.Reveal(), ""); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("local modification must survive"), 0o600); err != nil {
		test.Fatal(err)
	}
	click(test, tester, "启用后台同步")
	nextWatchUpdate(test, callbacks)
	tester.Frame()
	data, err := os.ReadFile(filepath.Join(canonical, "SKILL.md"))
	if err != nil || string(data) != "local modification must survive" || !strings.Contains(model.WatchStatus, "后台同步未完成") || fixture.source.calls.Load() != 2 {
		test.Fatal("background cycle lost local edits or masked a conflict")
	}
	click(test, tester, "停止后台同步")
	nextWatchUpdate(test, callbacks)
	tester.Frame()
	for _, token := range []string{firstShare.Token.Reveal(), secondShare.Token.Reveal(), replacement.Token.Reveal()} {
		if strings.Contains(strings.Join(tester.Texts(), " ")+model.WatchStatus+model.Status, token) {
			test.Fatal("background status rendered sharing token")
		}
	}
	assertWatchLockReleased(test, fixture.home)
	assertNoCredentialFiles(test, fixture.home, firstShare.Token.Reveal(), secondShare.Token.Reveal(), replacement.Token.Reveal())
}

func TestNativeWatchRealHTTPContextCancellationAndLockRelease(test *testing.T) {
	for _, action := range []string{"stop", "cancel", "origin", "logout", "close"} {
		test.Run(action, func(test *testing.T) {
			fixture := newWatchServer(test)
			selected, _ := fixture.version(test, "First version")
			fixture.subscribe(test, "First", selected.ID)
			fixture.subscribe(test, "Second", selected.ID)
			gate := newWatchGate()
			fixture.gate.Store(gate)
			model, tester, callbacks := watchTestModel(test, fixture)
			click(test, tester, "启用后台同步")
			select {
			case <-gate.started:
			case <-time.After(5 * time.Second):
				test.Fatal("watch did not reach actual HTTP handler")
			}
			switch action {
			case "stop":
				click(test, tester, "停止后台同步")
			case "cancel":
				click(test, tester, "取消任务")
			case "origin":
				model.SetOrigin("https://other.example")
			case "logout":
				model.Logout()
			case "close":
				model.Close()
			}
			select {
			case <-gate.canceled:
			case <-time.After(5 * time.Second):
				test.Fatal("actual network request was not canceled")
			}
			model.Wait()
			if action == "close" {
				if len(callbacks) != 0 {
					test.Fatal("close posted a late UI callback")
				}
			} else {
				nextWatchUpdate(test, callbacks)
			}
			assertWatchLockReleased(test, fixture.home)
			if fixture.source.calls.Load() != 0 || model.WatchActive || action != "close" && model.WatchStopping {
				test.Fatal("canceled HTTP cycle installed content or retained running state")
			}
		})
	}
}
