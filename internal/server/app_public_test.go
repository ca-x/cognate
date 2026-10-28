package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/skillhub/gen/skillhub/auth/v1"
	"github.com/ca-x/skillhub/gen/skillhub/auth/v1/authv1connect"
	catalogv1 "github.com/ca-x/skillhub/gen/skillhub/catalog/v1"
	"github.com/ca-x/skillhub/gen/skillhub/catalog/v1/catalogv1connect"
	"github.com/ca-x/skillhub/internal/cli"
	"github.com/ca-x/skillhub/internal/packclient"
	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/library"
	"github.com/ca-x/skillhub/internal/server/state"
	clientstate "github.com/ca-x/skillhub/internal/state"
)

type publicAppFetch func(*http.Request) (*http.Response, error)

func (fetch publicAppFetch) RoundTrip(request *http.Request) (*http.Response, error) {
	return fetch(request)
}

func seedPublicAppRepository(t *testing.T, app *App, config Config, owner string) state.CollectedRepository {
	t.Helper()
	var data bytes.Buffer
	compressed := gzip.NewWriter(&data)
	archive := tar.NewWriter(compressed)
	for _, entry := range []struct {
		path string
		data []byte
		mode int64
	}{
		{"repo-main/SKILL.md", []byte("---\nname: Public Binary\ndescription: Anonymous fixture\n---\nfixture\n"), 0o644},
		{"repo-main/.hidden", []byte{0, 0xff, 0x80, 0x0a}, 0o644},
		{"repo-main/bin/run", []byte("#!/bin/sh\nexit 0\n"), 0o751},
	} {
		if err := archive.WriteHeader(&tar.Header{Name: entry.path, Size: int64(len(entry.data)), Mode: entry.mode}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		t.Fatal(err)
	}
	fetcher := repository.NewArchiveFetcher(&http.Client{Transport: publicAppFetch(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "codeload.github.com" || request.URL.Path != "/org/public-skills/tar.gz/HEAD" || request.Header.Get("Authorization") != "" {
			return nil, errors.New("unexpected test source request")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data.Bytes())), Header: make(http.Header)}, nil
	})})
	reference, err := repository.ParseReference("org/public-skills", "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fetcher.Fetch(t.Context(), reference)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := library.OpenArchiveCacheWithOptions(t.Context(), filepath.Join(config.DataDirectory, "archives"), library.CacheOptions{MaxBytes: config.ArchiveCacheBytes})
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(cache.Put(t.Context(), snapshot.Hash, snapshot.Data), cache.Close()); err != nil {
		t.Fatal(err)
	}
	entry, err := app.store.CollectRepository(t.Context(), owner, reference, snapshot.Hash, snapshot.Skills)
	if err != nil {
		t.Fatal(err)
	}
	entry, err = app.store.CollectedRepository(t.Context(), owner, entry.ID)
	if err != nil || len(entry.Skills) != 1 {
		t.Fatal("fixture collection did not contain the scanned skill")
	}
	return entry
}

func TestAppPublicCatalogAndCLIInstallWithoutCredentials(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only client installation")
	}
	directory, home := t.TempDir(), t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(directory, "master.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	clear(key)
	config := Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: keyPath}
	app, err := Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	endpoint := httptest.NewServer(app.handler)
	t.Cleanup(endpoint.Close)
	accounts := authv1connect.NewAuthServiceClient(endpoint.Client(), endpoint.URL)
	password := rand.Text()
	registered, err := accounts.Register(t.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "public-owner", Password: password}))
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(t.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "public-owner", Password: password}))
	if err != nil {
		t.Fatal(err)
	}
	entry := seedPublicAppRepository(t, app, config, registered.Msg.UserId)
	selected := entry.Skills[0]
	public, err := packclient.NewPublic(endpoint.URL, endpoint.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := public.GetSkill(t.Context(), selected.ID); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("private/default publication leaked or route missing: %s", connect.CodeOf(err))
	}
	invokeCLI := func() error {
		var output bytes.Buffer
		command := cli.New()
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetArgs([]string{"install-public", "--server", endpoint.URL, "--skill", selected.ID})
		return command.ExecuteContext(t.Context())
	}
	if err := invokeCLI(); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("anonymous CLI did not reject unpublished skill")
	}
	for _, path := range []string{".agents", ".local", ".config"} {
		if _, err := os.Stat(filepath.Join(home, path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unpublished install initialized client state")
		}
	}
	publication := catalogv1connect.NewPublicationServiceClient(endpoint.Client(), endpoint.URL)
	publish := connect.NewRequest(&catalogv1.SetPublicationRequest{RepositoryId: entry.ID, ExpectedRevision: 1, Published: true})
	publish.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	if _, err := publication.SetPublication(t.Context(), publish); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("publication did not require full-contents confirmation")
	}
	publish.Msg.ConfirmFullContents = true
	published, err := publication.SetPublication(t.Context(), publish)
	if err != nil || !published.Msg.Publication.Published {
		t.Fatal("confirmed publication failed")
	}
	categories := catalogv1connect.NewCategoryServiceClient(endpoint.Client(), endpoint.URL)
	createCategory := connect.NewRequest(&catalogv1.CreateCategoryRequest{Name: "Development"})
	createCategory.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	category, err := categories.CreateCategory(t.Context(), createCategory)
	if err != nil || category.Msg.Category == nil {
		t.Fatal("category management not connected through App")
	}
	assign := connect.NewRequest(&catalogv1.SetSkillCategoriesRequest{SkillId: selected.ID, CategoryIds: []string{category.Msg.Category.Id}})
	assign.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	if _, err := categories.SetSkillCategories(t.Context(), assign); err != nil {
		t.Fatal("skill category management failed through App")
	}
	metadata, err := public.GetSkill(t.Context(), selected.ID)
	if err != nil || metadata.ContentHash != selected.ContentHash || len(metadata.Categories) != 1 || metadata.Categories[0].SkillCount != 1 {
		t.Fatal("published skill metadata unavailable to anonymous client")
	}
	guest := catalogv1connect.NewPublicCatalogServiceClient(endpoint.Client(), endpoint.URL)
	filtered, err := guest.ListSkills(t.Context(), connect.NewRequest(&catalogv1.ListSkillsRequest{CategoryId: category.Msg.Category.Id, Query: "Binary"}))
	if err != nil || len(filtered.Msg.Skills) != 1 || filtered.Msg.Skills[0].Id != selected.ID {
		t.Fatal("anonymous category/search filters failed through App")
	}
	for range 2 {
		if err := invokeCLI(); err != nil {
			t.Fatal(err)
		}
	}
	canonical := filepath.Join(home, ".agents", "skills", metadata.Slug)
	contents, err := os.ReadFile(filepath.Join(canonical, ".hidden"))
	if err != nil || !bytes.Equal(contents, []byte{0, 0xff, 0x80, 0x0a}) {
		t.Fatal("real server/client binary content did not round-trip")
	}
	if info, err := os.Stat(filepath.Join(canonical, "bin", "run")); err != nil || info.Mode()&0o111 == 0 {
		t.Fatal("public executable bit was not preserved")
	}
	if target, err := os.Readlink(filepath.Join(home, ".claude", "skills", metadata.Slug)); err != nil || target != canonical {
		t.Fatal("public skill did not project to detected agent")
	}
	local, err := clientstate.Open(t.Context(), filepath.Join(home, ".local", "share", "skillhub", "client.db"))
	if err != nil {
		t.Fatal(err)
	}
	record, lookupErr := local.Get(t.Context(), metadata.Slug)
	if err := errors.Join(lookupErr, local.Close()); err != nil || record.ContentHash != metadata.ContentHash || record.SubscriptionManaged {
		t.Fatal("standalone public install hash/ownership was not retained")
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("anonymous installation created credential storage")
	}
	deleteCategory := connect.NewRequest(&catalogv1.DeleteCategoryRequest{CategoryId: category.Msg.Category.Id, ExpectedRevision: category.Msg.Category.Revision})
	deleteCategory.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	if _, err := categories.DeleteCategory(t.Context(), deleteCategory); err != nil {
		t.Fatal("category removal failed through App")
	}
	uncategorized, err := public.GetSkill(t.Context(), selected.ID)
	if err != nil || len(uncategorized.Categories) != 0 || uncategorized.ContentHash != metadata.ContentHash {
		t.Fatal("category deletion changed skill content/visibility")
	}
	unpublish := connect.NewRequest(&catalogv1.SetPublicationRequest{RepositoryId: entry.ID, ExpectedRevision: published.Msg.Publication.Revision, Published: false})
	unpublish.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
	if _, err := publication.SetPublication(t.Context(), unpublish); err != nil {
		t.Fatal("unpublish failed")
	}
	if _, err := public.Download(t.Context(), metadata); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("stale anonymous metadata retained download authority")
	}
	if err := invokeCLI(); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("unpublished CLI install did not fail")
	}
	if _, err := os.Stat(filepath.Join(canonical, "SKILL.md")); err != nil {
		t.Fatal("unpublish deleted an existing independent local installation")
	}
	if strings.Contains(metadata.Path, directory) {
		t.Fatal("anonymous metadata exposed service filesystem path")
	}
}
