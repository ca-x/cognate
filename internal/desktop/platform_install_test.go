//go:build darwin

package desktop

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/httpapi"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

func darwinExecutionServer(test *testing.T) *serverFixture {
	test.Helper()

	home, canonicalErr := filepath.EvalSymlinks(test.TempDir())
	if canonicalErr != nil || os.Chmod(home, 0700) != nil {
		test.Fatal("DARWIN_INFRA private desktop home")
	}
	test.Setenv("HOME", home)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	test.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	test.Setenv("TMPDIR", test.TempDir())
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0700); err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	key := bytes.Repeat([]byte{17}, 32)
	serverDirectory, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil || os.Chmod(serverDirectory, 0700) != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	store, err := serverstate.Open(test.Context(), filepath.Join(serverDirectory, "server.db"), key)
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	test.Cleanup(func() { _ = store.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	owner, err := authentication.Register(test.Context(), "desktop-user", []byte("fixture-password"))
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	content := []byte("---\nname: Desktop Demo\ndescription: Native fixture\n---\nVerified fixture\n")
	sourceDirectory := test.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDirectory, "SKILL.md"), content, 0600); err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	hash, err := skill.ContentHashContext(test.Context(), afero.NewOsFs(), sourceDirectory)
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	writer := tar.NewWriter(compressed)
	if err := writer.WriteHeader(&tar.Header{Name: "skill/", Mode: 0700, Typeflag: tar.TypeDir}); err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	if err := writer.WriteHeader(&tar.Header{Name: "skill/SKILL.md", Mode: 0600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	if _, err := writer.Write(content); err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	if err := errors.Join(writer.Close(), compressed.Close()); err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	source := &archiveFixture{data: archive.Bytes()}
	reference, err := repository.ParseReference("fixture/desktop", "")
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	collected, err := store.CollectRepository(test.Context(), owner, reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Desktop Demo", Description: "Native fixture", Path: "skills/demo"}, ContentHash: hash}})
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	collected, err = store.CollectedRepository(test.Context(), owner, collected.ID)
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	publication, err := store.Publication(test.Context(), owner, collected.ID)
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	if _, err := store.SetPublication(test.Context(), owner, collected.ID, publication.Revision, true, true); err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	packService, err := packs.NewService(store, source, key)
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	test.Cleanup(packService.Close)
	pack, err := store.CreatePack(test.Context(), owner, serverstate.PackDraft{Name: "Desktop Pack", Items: []serverstate.PackSelection{{SkillID: collected.Skills[0].ID, Slug: "desktop-pack-demo"}}})
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	issued, err := packService.CreateShare(test.Context(), owner, pack.ID, nil)
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
	}
	catalogue, err := publiccatalog.NewService(store, source)
	if err != nil {
		test.Fatal("DARWIN_INFRA desktop fixture operation")
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

type darwinExecutionTransport struct {
	base  http.RoundTripper
	calls atomic.Int64
}

func (transport *darwinExecutionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return transport.base.RoundTrip(request)
}

func TestDarwinExecutionDesktopServiceInstall(test *testing.T) {
	fixture := darwinExecutionServer(test)
	transport := &darwinExecutionTransport{base: fixture.server.Client().Transport}
	service := &Service{Home: fixture.home, HTTP: &http.Client{Transport: transport}}
	entries, err := service.Search(test.Context(), fixture.server.URL, "", "")
	if err != nil || len(entries.Skills) != 1 {
		test.Fatal("DARWIN_INFRA anonymous remote browse")
	}
	selected, err := service.Details(test.Context(), fixture.server.URL, entries.Skills[0].Id)
	if err != nil {
		test.Fatal("DARWIN_INFRA real public metadata")
	}
	before := transport.calls.Load()
	err = service.Install(test.Context(), fixture.server.URL, selected)
	if transport.calls.Load() != before {
		test.Fatal("uninitialized native install made HTTP request")
	}
	if !errors.Is(err, clientkey.ErrInitializationRequired) {
		if errors.Is(err, errUnsupportedLocal) || errors.Is(err, clientkey.ErrUnsupported) {
			test.Log("DARWIN_BASELINE_REJECTION code=desktop-unsupported")
		}
		test.Fatal("Darwin native service must require explicit initialization")
	}
	if err := service.InitializeClientKey(test.Context()); err != nil {
		test.Fatal("native explicit initialization failed")
	}
	if transport.calls.Load() != before {
		test.Fatal("native initialization contacted server")
	}
	if err := service.Install(test.Context(), fixture.server.URL, selected); err != nil {
		test.Fatal("native anonymous installation failed")
	}
	canonical := filepath.Join(fixture.home, ".agents", "skills", selected.Slug)
	if actual, err := skill.ContentHash(afero.NewOsFs(), canonical); err != nil || actual != selected.ContentHash {
		test.Fatal("native anonymous canonical hash mismatch")
	}
	if fixture.anonymousViolation.Load() {
		test.Fatal("native anonymous request sent credentials")
	}
	packHome, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil || os.Chmod(packHome, 0700) != nil {
		test.Fatal("DARWIN_INFRA independent pack home")
	}
	if err := os.MkdirAll(filepath.Join(packHome, ".claude", "skills"), 0700); err != nil {
		test.Fatal("DARWIN_INFRA pack agent directory")
	}
	test.Setenv("HOME", packHome)
	test.Setenv("XDG_CONFIG_HOME", filepath.Join(packHome, ".config"))
	test.Setenv("XDG_DATA_HOME", filepath.Join(packHome, ".local", "share"))
	service = &Service{Home: packHome, HTTP: &http.Client{Transport: transport}}
	if err := service.InitializeClientKey(test.Context()); err != nil {
		test.Fatal("native pack explicit initialization failed")
	}
	if _, err := service.Packs(test.Context(), "subscribe", fixture.server.URL, fixture.token, ""); err != nil {
		test.Fatal("native actual pack subscription failed")
	}
	if _, _, err := service.WatchCycle(test.Context()); err != nil {
		test.Fatal("native actual watch cycle failed")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 500*time.Millisecond)
	session, err := install.OpenSession(ctx, filepath.Join(packHome, ".local", "share", "skillhub"))
	cancel()
	if err != nil {
		test.Fatal("native cycle retained physical installation lock")
	}
	if err := session.Close(); err != nil {
		test.Fatal("native idle session close failed")
	}
	assertNoCredentialFiles(test, fixture.home, fixture.token)
	assertNoCredentialFiles(test, packHome, fixture.token)
}
