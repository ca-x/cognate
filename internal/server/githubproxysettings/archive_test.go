package githubproxysettings

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/repository"
)

type archiveObservation struct {
	host               string
	serverName         string
	connectTarget      string
	requestURI         string
	authorization      string
	proxyAuthorization string
}

func TestManagerArchiveFetcherLocalProxy(test *testing.T) {
	fixture := isolatedTLSFixture(test)
	if fixture == "" {
		return
	}
	certificate, err := tls.LoadX509KeyPair(filepath.Join(fixture, "certificate.pem"), filepath.Join(fixture, "key.pem"))
	if err != nil {
		test.Fatal(err)
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || defaultTransport.TLSClientConfig != nil && defaultTransport.TLSClientConfig.InsecureSkipVerify {
		test.Fatal("local integration requires verified default TLS")
	}
	archive := makeArchiveFixture(test)
	var routes sync.Map
	observed := make(chan archiveObservation, 8)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		route, exists := routes.Load(request.RemoteAddr)
		if !exists || request.TLS == nil {
			test.Error("GitHub fixture was reached outside its controlled TLS tunnel")
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		observed <- archiveObservation{
			host: request.Host, serverName: request.TLS.ServerName, connectTarget: route.(connectionRoute).target,
			requestURI: request.URL.RequestURI(), authorization: request.Header.Get("Authorization"),
			proxyAuthorization: request.Header.Get("Proxy-Authorization"),
		}
		if request.Method != http.MethodGet {
			test.Error("archive fetcher changed the HTTP method")
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch request.Host + request.URL.RequestURI() {
		case "codeload.github.com/synthetic/public/tar.gz/main", "codeload.github.com/synthetic/private/legacy.tar.gz/main?download=synthetic":
			writer.Header().Set("Content-Type", "application/gzip")
			_, _ = writer.Write(archive)
		case "api.github.com/repos/synthetic/private":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"node_id":"R_syntheticPrivate","full_name":"synthetic/private","private":true}`)
		case "api.github.com/repos/synthetic/private/tarball/main":
			writer.Header().Set("Location", "https://codeload.github.com/synthetic/private/legacy.tar.gz/main?download=synthetic")
			writer.WriteHeader(http.StatusFound)
		default:
			test.Error("archive fetcher changed its GitHub host or request URL")
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
	origin.Config.ErrorLog = log.New(io.Discard, "", 0)
	origin.StartTLS()
	test.Cleanup(origin.Close)
	proxy := newConnectProxy(test, origin.Listener.Addr().String(), &routes)
	manager := openTestManager(test, testDirectory(test))
	if _, err := manager.Update(test.Context(), 1, localProxyURL(test, proxy.server.URL, "synthetic-proxy-user", "synthetic-proxy-password")); err != nil {
		test.Fatal(err)
	}
	fetcher := repository.NewArchiveFetcher(&http.Client{Transport: manager})
	public, err := fetcher.Fetch(test.Context(), repository.Reference{NameWithOwner: "synthetic/public", Branch: "main"})
	if err != nil {
		test.Fatalf("public ArchiveFetcher through manager: %v", err)
	}
	assertArchiveFixture(test, public, archive)
	token, err := github.NewToken(strings.TrimPrefix(localToken, "Bearer "), nil)
	if err != nil {
		test.Fatal("create synthetic GitHub token")
	}
	var checks atomic.Int64
	authorize := func(ctx context.Context) error {
		checks.Add(1)
		return ctx.Err()
	}
	reference := repository.Reference{NameWithOwner: "synthetic/private", Branch: "main"}
	metadata, err := fetcher.FetchMetadata(test.Context(), reference, token, authorize)
	if err != nil || metadata != (repository.GitHubMetadata{NodeID: "R_syntheticPrivate", NameWithOwner: reference.NameWithOwner, Private: true}) {
		test.Fatalf("metadata ArchiveFetcher through manager: %v", err)
	}
	private, err := fetcher.FetchPrivate(test.Context(), reference, token, authorize)
	if err != nil {
		test.Fatalf("private ArchiveFetcher through manager: %v", err)
	}
	assertArchiveFixture(test, private, archive)
	if checks.Load() == 0 {
		test.Fatal("private requests bypassed the existing authorization check")
	}
	for index, expected := range []archiveObservation{
		{host: "codeload.github.com", serverName: "codeload.github.com", connectTarget: "codeload.github.com:443", requestURI: "/synthetic/public/tar.gz/main"},
		{host: "api.github.com", serverName: "api.github.com", connectTarget: "api.github.com:443", requestURI: "/repos/synthetic/private", authorization: localToken},
		{host: "api.github.com", serverName: "api.github.com", connectTarget: "api.github.com:443", requestURI: "/repos/synthetic/private/tarball/main", authorization: localToken},
		{host: "codeload.github.com", serverName: "codeload.github.com", connectTarget: "codeload.github.com:443", requestURI: "/synthetic/private/legacy.tar.gz/main?download=synthetic"},
	} {
		select {
		case actual := <-observed:
			if actual != expected {
				test.Fatalf("GitHub request %d changed destination, TLS SNI, URL or authorization isolation", index)
			}
		default:
			test.Fatalf("GitHub request %d was not observed at the local TLS origin", index)
		}
	}
	if len(observed) != 0 {
		test.Fatal("archive fetcher sent unexpected requests")
	}
	seenTargets := make(map[string]bool)
	expectedProxyAuthorization := "Basic " + base64.StdEncoding.EncodeToString([]byte("synthetic-proxy-user:synthetic-proxy-password"))
	for range proxy.requests.Load() {
		select {
		case observation := <-proxy.observed:
			seenTargets[observation.target] = true
			if observation.authorization != "" || observation.proxyAuthorization != expectedProxyAuthorization {
				test.Fatal("CONNECT leaked GitHub authorization or lost proxy authentication")
			}
		default:
			test.Fatal("controlled proxy did not observe its CONNECT handshake")
		}
	}
	if len(seenTargets) != 2 || !seenTargets["api.github.com:443"] || !seenTargets["codeload.github.com:443"] {
		test.Fatal("CONNECT destinations did not preserve both GitHub API and archive hosts")
	}
	if http.DefaultTransport != defaultTransport {
		test.Fatal("manager replaced the default transport")
	}
}

func makeArchiveFixture(test *testing.T) []byte {
	test.Helper()
	var contents bytes.Buffer
	compressed := gzip.NewWriter(&contents)
	archive := tar.NewWriter(compressed)
	document := "---\nname: ProxyFixture\ndescription: Synthetic proxy integration\n---\nLocal fixture only.\n"
	if err := archive.WriteHeader(&tar.Header{Name: "repo-main/.claude/skills/fixture/SKILL.md", Mode: 0o644, Size: int64(len(document)), Typeflag: tar.TypeReg}); err != nil {
		test.Fatal(err)
	}
	if _, err := io.WriteString(archive, document); err != nil {
		test.Fatal(err)
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		test.Fatal(err)
	}
	return contents.Bytes()
}

func assertArchiveFixture(test *testing.T, snapshot *repository.ArchiveSnapshot, archive []byte) {
	test.Helper()
	if snapshot == nil || !bytes.Equal(snapshot.Data, archive) || len(snapshot.Hash) != 64 || len(snapshot.Skills) != 1 || snapshot.Skills[0].Name != "ProxyFixture" || snapshot.Skills[0].Path != ".claude/skills/fixture" {
		test.Fatal("ArchiveFetcher did not inspect the synthetic archive")
	}
}
