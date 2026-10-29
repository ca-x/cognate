package desktop

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/egoist/mygo/ui"
	"github.com/spf13/afero"
)

type archiveFixture struct {
	data      []byte
	downloads atomic.Int32
}

func (fixture *archiveFixture) WriteSkillArchive(ctx context.Context, _, _, _ string, destination io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fixture.downloads.Add(1)
	_, err := destination.Write(fixture.data)
	return err
}

type serverFixture struct {
	server             *httptest.Server
	home               string
	token              string
	source             *archiveFixture
	anonymousViolation atomic.Bool
}

func realServer(test *testing.T) *serverFixture {
	test.Helper()
	if runtime.GOOS != "linux" {
		test.Skip("real server state and installation fixture currently requires Linux")
	}
	home := test.TempDir()
	test.Setenv("HOME", home)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	test.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	test.Setenv("TMPDIR", test.TempDir())
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0700); err != nil {
		test.Fatal(err)
	}
	key := bytes.Repeat([]byte{17}, 32)
	serverDirectory := test.TempDir()
	if err := os.Chmod(serverDirectory, 0700); err != nil {
		test.Fatal(err)
	}
	store, err := serverstate.Open(test.Context(), filepath.Join(serverDirectory, "server.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	owner, err := authentication.Register(test.Context(), "desktop-user", []byte("fixture-password"))
	if err != nil {
		test.Fatal(err)
	}
	content := []byte("---\nname: Desktop Demo\ndescription: Native fixture\n---\nVerified fixture\n")
	sourceDirectory := test.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDirectory, "SKILL.md"), content, 0600); err != nil {
		test.Fatal(err)
	}
	hash, err := skill.ContentHashContext(test.Context(), afero.NewOsFs(), sourceDirectory)
	if err != nil {
		test.Fatal(err)
	}
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	writer := tar.NewWriter(compressed)
	if err := writer.WriteHeader(&tar.Header{Name: "skill/", Mode: 0700, Typeflag: tar.TypeDir}); err != nil {
		test.Fatal(err)
	}
	if err := writer.WriteHeader(&tar.Header{Name: "skill/SKILL.md", Mode: 0600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		test.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(writer.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	source := &archiveFixture{data: archive.Bytes()}
	reference, err := repository.ParseReference("fixture/desktop", "")
	if err != nil {
		test.Fatal(err)
	}
	collected, err := store.CollectRepository(test.Context(), owner, reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Desktop Demo", Description: "Native fixture", Path: "skills/demo"}, ContentHash: hash}})
	if err != nil {
		test.Fatal(err)
	}
	collected, err = store.CollectedRepository(test.Context(), owner, collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	publication, err := store.Publication(test.Context(), owner, collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.SetPublication(test.Context(), owner, collected.ID, publication.Revision, true, true); err != nil {
		test.Fatal(err)
	}
	packService, err := packs.NewService(store, source, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(packService.Close)
	pack, err := store.CreatePack(test.Context(), owner, serverstate.PackDraft{Name: "Desktop Pack", Items: []serverstate.PackSelection{{SkillID: collected.Skills[0].ID, Slug: "desktop-pack-demo"}}})
	if err != nil {
		test.Fatal(err)
	}
	issued, err := packService.CreateShare(test.Context(), owner, pack.ID, nil)
	if err != nil {
		test.Fatal(err)
	}
	catalogue, err := publiccatalog.NewService(store, source)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = catalogue.Close() })
	fixture := &serverFixture{home: home, token: issued.Token.Reveal(), source: source}
	handler := httpapi.NewWithOptions(authentication, httpapi.Options{Store: store, Packs: packService, PublicCatalog: catalogue})
	fixture.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "PublicCatalogService") && (request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "") {
			fixture.anonymousViolation.Store(true)
		}
		handler.ServeHTTP(response, request)
	}))
	test.Cleanup(fixture.server.Close)
	return fixture
}

func modelFixture(test *testing.T, backend Backend) (*Model, func()) {
	test.Helper()
	queue := make(chan func(), 64)
	model := NewModel(backend, func(commit func()) { queue <- commit })
	pump := func() {
		model.Wait()
		for {
			select {
			case commit := <-queue:
				commit()
			default:
				return
			}
		}
	}
	test.Cleanup(func() { model.Close(); pump() })
	return model, pump
}

func click(test *testing.T, tester *ui.Tester, label string) {
	test.Helper()
	if err := tester.Click(label); err != nil {
		test.Fatal(err)
	}
}

func assertNoCredentialFiles(test *testing.T, home string, secrets ...string) {
	test.Helper()
	if err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, secret := range secrets {
			if secret != "" && bytes.Contains(data, []byte(secret)) {
				test.Fatal("plaintext credential persisted in client home")
			}
		}
		return nil
	}); err != nil {
		test.Fatal(err)
	}
}

func TestNativeLoginAnonymousSelectionConfirmationAndInstall(test *testing.T) {
	fixture := realServer(test)
	service := &Service{Home: fixture.home, HTTP: fixture.server.Client()}
	model, pump := modelFixture(test, service)
	model.SetOrigin(fixture.server.URL)
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "账号")
	click(test, tester, "用户名")
	tester.Type("desktop-user")
	click(test, tester, "密码")
	tester.Type("fixture-password")
	if strings.Contains(strings.Join(tester.Texts(), " "), "fixture-password") {
		test.Fatal("password appears in rendered text")
	}
	click(test, tester, "登录")
	pump()
	tester.Frame()
	if model.session.value() == "" || model.password.value() != "" {
		test.Fatalf("login did not establish memory-only session: %s", model.Status)
	}
	click(test, tester, "公开目录")
	click(test, tester, "搜索")
	pump()
	tester.Frame()
	if len(model.Skills) != 1 {
		test.Fatalf("real anonymous catalogue: %s", model.Status)
	}
	model.skillList.Handle.Focus()
	tester.Frame()
	tester.Key(0, ui.KeyDown)
	if model.Selected < 0 {
		test.Fatal("native keyboard did not select skill")
	}
	click(test, tester, "查看详情")
	pump()
	tester.Frame()
	if model.Detail == nil {
		test.Fatalf("details missing: %s", model.Status)
	}
	model.Install()
	pump()
	if fixture.source.downloads.Load() != 0 {
		test.Fatal("installation occurred before confirmation")
	}
	for _, relative := range []string{".config/skillhub/client.key", ".local/share/skillhub/client.db", ".agents"} {
		if _, err := os.Lstat(filepath.Join(fixture.home, relative)); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("remote authentication/catalogue/details implicitly created client state")
		}
	}
	click(test, tester, "账号")
	click(test, tester, initializeClientKeyLabel)
	pump()
	tester.Frame()
	if !strings.Contains(model.Status, "已就绪") {
		test.Fatal("explicit native client initialization failed")
	}
	click(test, tester, "公开目录")
	click(test, tester, "确认下载完整技能内容并写入本地 ~/.agents/skills 与已适配 agent 目录")
	click(test, tester, "安装已确认技能")
	pump()
	tester.Frame()
	if !strings.HasPrefix(model.Status, "安装完成") {
		test.Fatalf("real install: %s", model.Status)
	}
	canonical := filepath.Join(fixture.home, ".agents", "skills", model.Detail.Slug, "SKILL.md")
	if _, err := os.Stat(canonical); err != nil {
		test.Fatal(err)
	}
	projected := filepath.Join(fixture.home, ".claude", "skills", model.Detail.Slug)
	if info, err := os.Lstat(projected); err != nil || info.Mode()&os.ModeSymlink == 0 {
		test.Fatal("shared kernel did not produce agent symlink")
	}
	if fixture.anonymousViolation.Load() {
		test.Fatal("authenticated session leaked into anonymous catalogue")
	}
	token := model.session.value()
	click(test, tester, "账号")
	click(test, tester, "退出登录")
	pump()
	if model.session.value() != "" {
		test.Fatal("logout retained session")
	}
	if err := service.Logout(test.Context(), fixture.server.URL, token); err == nil {
		test.Fatal("server session was not revoked")
	}
	assertNoCredentialFiles(test, fixture.home, "fixture-password", token)
}

func TestNativePackOperationsUseSharedPersistentCore(test *testing.T) {
	fixture := realServer(test)
	service := &Service{Home: fixture.home, HTTP: fixture.server.Client()}
	if err := service.InitializeClientKey(test.Context()); err != nil {
		test.Fatal("explicit client initialization failed")
	}
	model, pump := modelFixture(test, service)
	model.SetOrigin(fixture.server.URL)
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "Pack 订阅")
	click(test, tester, "分享令牌")
	tester.Type(fixture.token)
	if strings.Contains(strings.Join(tester.Texts(), " "), fixture.token) {
		test.Fatal("share token rendered")
	}
	click(test, tester, "保存订阅")
	pump()
	tester.Frame()
	if model.shareToken.value() != "" || len(model.Subscriptions) != 1 {
		test.Fatalf("subscribe: %s", model.Status)
	}
	if fixture.source.downloads.Load() != 0 {
		test.Fatal("save subscription silently installed skills")
	}
	click(test, tester, "同步并应用安装变更")
	pump()
	tester.Frame()
	if len(model.Subscriptions) != 1 || model.Subscriptions[0].AppliedRevision == 0 {
		test.Fatalf("sync: %s", model.Status)
	}
	if _, err := os.Stat(filepath.Join(fixture.home, ".agents", "skills", "desktop-pack-demo", "SKILL.md")); err != nil {
		test.Fatal(err)
	}
	click(test, tester, "刷新订阅")
	pump()
	tester.Frame()
	model.packList.Handle.Focus()
	tester.Frame()
	tester.Key(0, ui.KeyDown)
	click(test, tester, "移除所选订阅")
	pump()
	tester.Frame()
	click(test, tester, "同步并应用安装变更")
	pump()
	tester.Frame()
	if _, err := os.Stat(filepath.Join(fixture.home, ".agents", "skills", "desktop-pack-demo")); !errors.Is(err, os.ErrNotExist) {
		test.Fatalf("unsubscribe did not remove managed installation: %s", model.Status)
	}
	if strings.Contains(model.Status, fixture.token) {
		test.Fatal("status disclosed sharing token")
	}
	assertNoCredentialFiles(test, fixture.home, fixture.token)
}
